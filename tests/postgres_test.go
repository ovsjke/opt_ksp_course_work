package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"pavel_ovsyannikov/internal/domain"
	"pavel_ovsyannikov/internal/handler"
	"pavel_ovsyannikov/internal/repository"
	"pavel_ovsyannikov/internal/service"
	"pavel_ovsyannikov/internal/session"
	"pavel_ovsyannikov/migrations"
	"pavel_ovsyannikov/seed"
)

// Integration tests deliberately require a separate database with a _test suffix.
func TestPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to a dedicated *_test database")
	}
	ctx := context.Background()
	r, e := repository.Open(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Pool.Close()
	var name string
	if e = r.Pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("refusing to reset database without _test suffix")
	}
	if e = migrations.Apply(ctx, r.Pool); e != nil {
		t.Fatal(e)
	}
	reset := func(t *testing.T) {
		t.Helper()
		_, e := r.Pool.Exec(ctx, `TRUNCATE sessions,appointments,slots,specialist_services,services,specialists,users RESTART IDENTITY CASCADE; INSERT INTO users VALUES(1,'demo','unused');INSERT INTO specialists VALUES(1,'One'),(2,'Two');INSERT INTO services VALUES(1,'30 minutes',30),(2,'90 minutes',90);INSERT INTO specialist_services VALUES(1,1),(1,2),(2,1);INSERT INTO slots VALUES(1,1,'2026-10-01 08:00Z','2026-10-01 09:00Z'),(2,1,'2026-10-01 08:30Z','2026-10-01 09:30Z'),(3,1,'2026-10-01 09:00Z','2026-10-01 10:00Z'),(4,2,'2026-10-01 08:00Z','2026-10-01 09:00Z');`)
		if e != nil {
			t.Fatal(e)
		}
	}
	svc := service.Service{Store: r}
	t.Run("book conflict overlap cancel rebook", func(t *testing.T) {
		reset(t)
		a, e := svc.Book(ctx, 1, 1, 1)
		if e != nil {
			t.Fatal(e)
		}
		for _, slot := range []int64{1, 2} {
			if _, e = svc.Book(ctx, 1, slot, 1); !errors.Is(e, domain.ErrConflict) {
				t.Fatalf("slot %d: %v", slot, e)
			}
		}
		if e = svc.Cancel(ctx, 1, a.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = svc.Book(ctx, 1, 2, 1); e != nil {
			t.Fatal(e)
		}
		if _, e = svc.Book(ctx, 1, 4, 1); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("short missing unsupported adjacent", func(t *testing.T) {
		reset(t)
		for _, tc := range []struct {
			slot, sv int64
			want     error
		}{{1, 2, domain.ErrInvalid}, {999, 1, domain.ErrNotFound}, {1, 999, domain.ErrNotFound}, {4, 2, domain.ErrInvalid}} {
			if _, e = svc.Book(ctx, 1, tc.slot, tc.sv); !errors.Is(e, tc.want) {
				t.Fatalf("%+v: %v", tc, e)
			}
		}
		if _, e = svc.Book(ctx, 1, 1, 1); e != nil {
			t.Fatal(e)
		}
		if _, e = svc.Book(ctx, 1, 3, 1); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("concurrent overlapping bookings", func(t *testing.T) {
		reset(t)
		var wg sync.WaitGroup
		results := make(chan error, 12)
		start := make(chan struct{})
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(slot int64) { defer wg.Done(); <-start; _, e := svc.Book(ctx, 1, slot, 1); results <- e }(int64(i%2 + 1))
		}
		close(start)
		wg.Wait()
		close(results)
		ok, conflict := 0, 0
		for e := range results {
			if e == nil {
				ok++
			} else if errors.Is(e, domain.ErrConflict) {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if ok != 1 || conflict != 11 {
			t.Fatalf("success=%d conflict=%d", ok, conflict)
		}
	})
	t.Run("dashboard ownership availability", func(t *testing.T) {
		reset(t)
		a, e := svc.Book(ctx, 1, 1, 1)
		if e != nil {
			t.Fatal(e)
		}
		b, e := svc.Book(ctx, 1, 3, 1)
		if e != nil {
			t.Fatal(e)
		}
		if e = svc.Cancel(ctx, 1, b.ID); e != nil {
			t.Fatal(e)
		}
		from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		to := from.Add(24 * time.Hour)
		d, e := r.Dashboard(ctx, from, to)
		if e != nil {
			t.Fatal(e)
		}
		if d.Total != 2 || d.Booked != 1 || d.Cancelled != 1 || d.CancellationRate != 50 || d.Specialists[0].BookedMinutes != 60 {
			t.Fatalf("%+v", d)
		}
		slots, e := r.Slots(ctx, 1, from, to)
		if e != nil {
			t.Fatal(e)
		}
		for _, s := range slots {
			if s.ID == 1 || s.ID == 2 {
				t.Fatalf("busy slot returned: %d", s.ID)
			}
		}
		if len(slots) != 2 {
			t.Fatal(slots)
		}
		if _, e = r.Appointment(ctx, 2, a.ID); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if e = r.Cancel(ctx, 2, a.ID); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
	})
	t.Run("persistent sessions", func(t *testing.T) {
		reset(t)
		if _, e = r.Pool.Exec(ctx, `UPDATE users SET password_hash=$1 WHERE id=1`, seed.DemoHash); e != nil {
			t.Fatal(e)
		}
		m := session.Manager{Store: r, TTL: time.Hour}
		id, e := m.Login(ctx, "demo", "demo")
		if e != nil {
			t.Fatal(e)
		}
		r2, e := repository.Open(ctx, url)
		if e != nil {
			t.Fatal(e)
		}
		defer r2.Pool.Close()
		if _, e = r2.Session(ctx, id); e != nil {
			t.Fatal(e)
		}
		if _, e = r.Pool.Exec(ctx, `UPDATE sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
		if _, e = r2.Session(ctx, id); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
	})
	t.Run("HTTP booking workflow with PostgreSQL", func(t *testing.T) {
		reset(t)
		if _, e := r.Pool.Exec(ctx, `UPDATE users SET password_hash=$1 WHERE id=1`, seed.DemoHash); e != nil {
			t.Fatal(e)
		}
		tpl, e := template.ParseGlob("../templates/*.html")
		if e != nil {
			t.Fatal(e)
		}
		h := (&handler.Handler{Service: &svc, Sessions: &session.Manager{Store: r, TTL: time.Hour}, Templates: tpl}).Router()
		cookie := login(t, h)
		response := request(h, "POST", "/api/appointments", `{"slot_id":1,"service_id":1}`, cookie)
		if response.Code != 201 {
			t.Fatalf("book: %d %s", response.Code, response.Body)
		}
		var a domain.Appointment
		if e := json.Unmarshal(response.Body.Bytes(), &a); e != nil {
			t.Fatal(e)
		}
		if a.Slot.ID != 1 || a.Service.ID != 1 || a.Status != "booked" {
			t.Fatalf("unexpected appointment: %+v", a)
		}
		response = request(h, "POST", "/api/appointments", `{"slot_id":2,"service_id":1}`, cookie)
		if response.Code != 409 {
			t.Fatalf("overlap: %d %s", response.Code, response.Body)
		}
		response = request(h, "GET", fmt.Sprintf("/appointments/%d", a.ID), "", cookie)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "One") {
			t.Fatalf("HTML card: %d %s", response.Code, response.Body)
		}
		response = request(h, "POST", fmt.Sprintf("/api/appointments/%d/cancel", a.ID), "", cookie)
		if response.Code != 200 {
			t.Fatalf("cancel: %d %s", response.Code, response.Body)
		}
		response = request(h, "POST", "/api/appointments", `{"slot_id":1,"service_id":1}`, cookie)
		if response.Code != 201 {
			t.Fatalf("rebook: %d %s", response.Code, response.Body)
		}
	})

	t.Run("seed repeatability", func(t *testing.T) {
		var previous string
		for i := 0; i < 2; i++ {
			counts, e := seed.Run(ctx, r.Pool, "small", seed.DemoHash)
			if e != nil {
				t.Fatal(e)
			}
			if counts["appointments"] != 300 || counts["slots"] != 500 {
				t.Fatal(counts)
			}
			var digest string
			e = r.Pool.QueryRow(ctx, `SELECT md5(string_agg(row_to_json(a)::text,'' ORDER BY id)) FROM appointments a`).Scan(&digest)
			if e != nil {
				t.Fatal(e)
			}
			if i == 1 && digest != previous {
				t.Fatal("seed changed")
			}
			previous = digest
		}
	})
}
