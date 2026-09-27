package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pavel_ovsyannikov/internal/domain"
	"pavel_ovsyannikov/internal/measurement"
)

type fakeFixtures struct {
	mu                           sync.Mutex
	sessions                     map[string]bool
	slots                        map[int64]bool
	appointments                 map[int64]domain.Appointment
	nextSlot, nextAppointment    int64
	prepares, restores, cleanups int
}

func newFakeFixtures() *fakeFixtures {
	cancelled := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	return &fakeFixtures{sessions: map[string]bool{}, slots: map[int64]bool{7: true},
		appointments: map[int64]domain.Appointment{1: {ID: 1, SlotID: 7, Status: "cancelled", CancelledAt: &cancelled}}, nextSlot: 100, nextAppointment: 100}
}
func (f *fakeFixtures) Counts(context.Context) (counts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := counts{1, 1, 1, 1, int64(len(f.slots)), int64(len(f.appointments)), int64(len(f.sessions)), 0, 0}
	for _, a := range f.appointments {
		if a.Status == "booked" {
			c[7]++
		} else {
			c[8]++
		}
	}
	return c, nil
}
func (f *fakeFixtures) BindSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.sessions[id] {
		return fmt.Errorf("session not found")
	}
	return nil
}
func (f *fakeFixtures) Prepare(_ context.Context, slot, service int64, booked bool) (fixture, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if slot != 7 || service != 1 || len(f.slots) != 1 || len(f.appointments) != 1 {
		return fixture{}, fmt.Errorf("fixture setup on contaminated dataset")
	}
	f.prepares++
	f.nextSlot++
	v := fixture{SlotID: f.nextSlot}
	f.slots[v.SlotID] = true
	if booked {
		f.nextAppointment++
		v.AppointmentID = f.nextAppointment
		f.appointments[v.AppointmentID] = domain.Appointment{ID: v.AppointmentID, SlotID: v.SlotID, Status: "booked"}
	}
	return v, nil
}
func (f *fakeFixtures) Restore(_ context.Context, v fixture) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.appointments[v.AppointmentID]
	if !ok {
		return fmt.Errorf("fixture missing")
	}
	f.restores++
	a.Status = "booked"
	a.CancelledAt = nil
	f.appointments[a.ID] = a
	return nil
}
func (f *fakeFixtures) Cleanup(_ context.Context, v fixture) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v.SlotID == 7 {
		return fmt.Errorf("attempt to delete original slot")
	}
	if v.AppointmentID != 0 {
		a := f.appointments[v.AppointmentID]
		if a.Status != "booked" || a.CancelledAt != nil {
			return fmt.Errorf("cancel fixture was not restored before cleanup")
		}
	}
	for id, a := range f.appointments {
		if a.SlotID == v.SlotID {
			delete(f.appointments, id)
		}
	}
	delete(f.slots, v.SlotID)
	f.cleanups++
	return nil
}

// The HTTP fixture checks that all reads see the original dataset and mutations
// operate only on private records. failAfter simulates a lost/failed response
// after the server has already changed state.
func measurementServer(t *testing.T, f *fakeFixtures, failAfter string) (*httptest.Server, map[string]int) {
	t.Helper()
	calls := map[string]int{}
	nextSession := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			key = "POST cancel"
		}
		calls[key]++
		write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		if r.URL.Path == "/api/login" {
			nextSession++
			id := strconv.Itoa(nextSession)
			f.sessions[id] = true
			http.SetCookie(w, &http.Cookie{Name: "session_id", Value: id, Path: "/"})
			write(map[string]string{"status": "ok"})
			return
		}
		cookie, err := r.Cookie("session_id")
		if err != nil || !f.sessions[cookie.Value] {
			http.Error(w, "missing session", 401)
			return
		}
		if r.URL.Path == "/api/logout" {
			delete(f.sessions, cookie.Value)
			http.SetCookie(w, &http.Cookie{Name: "session_id", Path: "/", MaxAge: -1})
			write(map[string]string{"status": "ok"})
			return
		}
		if cookie.Value != "1" {
			t.Errorf("business session changed to %s", cookie.Value)
		}
		if r.Method == "GET" && (len(f.slots) != 1 || len(f.appointments) != 1) {
			t.Errorf("read %s sees benchmark fixtures", r.URL.Path)
		}
		switch {
		case r.URL.Path == "/api/appointments" && r.Method == "GET":
			write(domain.Page{Items: []domain.Appointment{f.appointments[1]}, Total: 1, Page: 1, Size: 20})
		case r.URL.Path == "/api/appointments/1":
			write(f.appointments[1])
		case r.URL.Path == "/api/services":
			write([]domain.Service{{ID: 1}})
		case r.URL.Path == "/api/specialists":
			write([]domain.Specialist{{ID: 1}})
		case r.URL.Path == "/api/slots":
			write([]domain.Slot{{ID: 7}})
		case r.URL.Path == "/api/appointments" && r.Method == "POST":
			var body struct {
				SlotID    int64 `json:"slot_id"`
				ServiceID int64 `json:"service_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !f.slots[body.SlotID] || body.SlotID == 7 || body.ServiceID != 1 {
				http.Error(w, "bad private fixture", 400)
				return
			}
			f.nextAppointment++
			a := domain.Appointment{ID: f.nextAppointment, SlotID: body.SlotID, Status: "booked"}
			f.appointments[a.ID] = a
			if failAfter == "book" {
				http.Error(w, "response failed after insert", 500)
				return
			}
			write(a)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			parts := strings.Split(r.URL.Path, "/")
			id, _ := strconv.ParseInt(parts[3], 10, 64)
			a, ok := f.appointments[id]
			if !ok || id == 1 || a.Status != "booked" {
				http.Error(w, "not a booked fixture", 409)
				return
			}
			now := time.Now()
			a.Status = "cancelled"
			a.CancelledAt = &now
			f.appointments[id] = a
			if failAfter == "cancel" {
				http.Error(w, "response failed after cancel", 500)
				return
			}
			write(map[string]string{"status": "cancelled"})
		case r.URL.Path == "/api/dashboard":
			write(domain.Dashboard{Total: 1, Cancelled: 1})
		default:
			http.NotFound(w, r)
		}
	}))
	return server, calls
}

func TestFullMeasurementScenario(t *testing.T) {
	f := newFakeFixtures()
	original := f.appointments[1]
	server, calls := measurementServer(t, f, "")
	defer server.Close()
	var output bytes.Buffer
	if err := measure(context.Background(), measurement.New(server.URL), f, 20, "small", &output); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, want := range map[string]int{
		"POST /api/login": 43, "POST /api/logout": 43,
		"GET /api/appointments": 43, "GET /api/appointments/1": 21,
		"GET /api/services": 21, "GET /api/specialists": 21, "GET /api/slots": 22,
		"POST /api/appointments": 21, "POST cancel": 21, "GET /api/dashboard": 21,
	} {
		if calls[key] != want {
			t.Errorf("%s calls=%d want=%d", key, calls[key], want)
		}
	}
	if f.prepares != 42 || f.cleanups != 42 || f.restores != 21 {
		t.Fatalf("setup/cleanup/restore: %d/%d/%d", f.prepares, f.cleanups, f.restores)
	}
	if len(f.appointments) != 1 || len(f.slots) != 1 || len(f.sessions) != 0 || f.appointments[1] != original {
		t.Fatal("original state changed")
	}
	for _, name := range []string{"services", "specialists", "cancel appointment", "login", "logout", "state check: PASS"} {
		if !strings.Contains(output.String(), name) {
			t.Errorf("missing output: %s", name)
		}
	}
}

func TestMutationErrorStillCleansWarmup(t *testing.T) {
	for _, failAfter := range []string{"book", "cancel"} {
		t.Run(failAfter, func(t *testing.T) {
			f := newFakeFixtures()
			original := f.appointments[1]
			before, _ := f.Counts(context.Background())
			server, _ := measurementServer(t, f, failAfter)
			defer server.Close()
			var out bytes.Buffer
			if err := measure(context.Background(), measurement.New(server.URL), f, 20, "small", &out); err == nil {
				t.Fatal("expected HTTP failure")
			}
			after, _ := f.Counts(context.Background())
			if after != before || f.appointments[1] != original {
				t.Fatalf("state changed: %v -> %v", before, after)
			}
			if f.cleanups != f.prepares {
				t.Fatal("failed warm-up fixture not cleaned")
			}
		})
	}
}

func TestRejectTooFewSamples(t *testing.T) {
	if err := measure(context.Background(), measurement.New("http://unused.invalid"), nil, 19, "small", &bytes.Buffer{}); err == nil {
		t.Fatal("accepted fewer than 20 samples")
	}
}

func TestCountMismatchFailsRun(t *testing.T) {
	before := counts{}
	after := counts{}
	after[5] = 1
	if err := reportCounts(&bytes.Buffer{}, before, after); err == nil {
		t.Fatal("count mismatch was ignored")
	}
}
