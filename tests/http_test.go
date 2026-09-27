package tests

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pavel_ovsyannikov/internal/domain"
	"pavel_ovsyannikov/internal/handler"
	"pavel_ovsyannikov/internal/service"
	"pavel_ovsyannikov/internal/session"
	"pavel_ovsyannikov/seed"
)

type fake struct {
	service.Store
	sessions map[string]time.Time
}

func (f *fake) User(_ context.Context, login string) (domain.User, error) {
	if login != "demo" {
		return domain.User{}, domain.ErrNotFound
	}
	return domain.User{ID: 1, Login: login, PasswordHash: seed.DemoHash}, nil
}
func (f *fake) CreateSession(_ context.Context, id string, _ int64, expires time.Time) error {
	f.sessions[id] = expires
	return nil
}
func (f *fake) Session(_ context.Context, id string) (domain.User, error) {
	if exp, ok := f.sessions[id]; !ok || !exp.After(time.Now()) {
		return domain.User{}, domain.ErrNotFound
	}
	return domain.User{ID: 1, Login: "demo"}, nil
}
func (f *fake) DeleteSession(_ context.Context, id string) error { delete(f.sessions, id); return nil }
func (f *fake) Appointments(_ context.Context, _ int64, q domain.Filter) (domain.Page, error) {
	return domain.Page{Items: []domain.Appointment{}, Total: 0, Page: q.Page, Size: q.Size}, nil
}
func (f *fake) Appointment(context.Context, int64, int64) (domain.Appointment, error) {
	return domain.Appointment{}, domain.ErrNotFound
}
func (f *fake) Services(context.Context) ([]domain.Service, error) {
	return []domain.Service{{ID: 1, Name: "Consultation", DurationMinutes: 30}}, nil
}
func (f *fake) Specialists(context.Context) ([]domain.Specialist, error) {
	return []domain.Specialist{{ID: 1, Name: "Specialist"}}, nil
}
func (f *fake) Slots(context.Context, int64, time.Time, time.Time) ([]domain.Slot, error) {
	return []domain.Slot{}, nil
}
func (f *fake) Dashboard(context.Context, time.Time, time.Time) (domain.Dashboard, error) {
	return domain.Dashboard{Specialists: []domain.Load{}}, nil
}
func setup(t *testing.T) (http.Handler, *fake) {
	t.Helper()
	f := &fake{sessions: map[string]time.Time{}}
	tpl, e := template.ParseGlob("../templates/*.html")
	if e != nil {
		t.Fatal(e)
	}
	h := &handler.Handler{Service: &service.Service{Store: f}, Sessions: &session.Manager{Store: f, TTL: time.Hour}, Templates: tpl}
	return h.Router(), f
}
func request(h http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := request(h, "POST", "/api/login", `{"login":"demo","password":"demo"}`, nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "session_id" || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteLaxMode || len(cs[0].Value) != 64 {
		t.Fatalf("invalid cookie: %v", cs)
	}
	return cs[0]
}
func TestHTTP(t *testing.T) {
	t.Run("unauthorized", func(t *testing.T) {
		h, _ := setup(t)
		if w := request(h, "GET", "/api/appointments", "", nil); w.Code != 401 {
			t.Fatal(w.Code)
		}
	})
	t.Run("login and session lifecycle", func(t *testing.T) {
		h, f := setup(t)
		c := login(t, h)
		if w := request(h, "GET", "/api/appointments", "", c); w.Code != 200 {
			t.Fatal(w.Code)
		}
		f.sessions[c.Value] = time.Now().Add(-time.Second)
		if w := request(h, "GET", "/api/appointments", "", c); w.Code != 401 {
			t.Fatal(w.Code)
		}
		c = login(t, h)
		if w := request(h, "POST", "/api/logout", "", c); w.Code != 200 {
			t.Fatal(w.Code)
		}
		if w := request(h, "GET", "/api/appointments", "", c); w.Code != 401 {
			t.Fatal(w.Code)
		}
	})
	t.Run("bad login", func(t *testing.T) {
		h, _ := setup(t)
		for _, body := range []string{`{"login":"demo","password":"wrong"}`, `{"login":"nobody","password":"demo"}`} {
			if w := request(h, "POST", "/api/login", body, nil); w.Code != 401 {
				t.Fatal(w.Code)
			}
		}
	})
	t.Run("list contract", func(t *testing.T) {
		h, _ := setup(t)
		w := request(h, "GET", "/api/appointments?page=2&size=7", "", login(t, h))
		var p map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
			t.Fatal(w.Body.String())
		}
		for _, k := range []string{"items", "total", "page", "size"} {
			if _, ok := p[k]; !ok {
				t.Fatal(k)
			}
		}
		if string(p["items"]) != "[]" || string(p["page"]) != "2" || string(p["size"]) != "7" {
			t.Fatal(w.Body.String())
		}
	})
	t.Run("unknown appointment", func(t *testing.T) {
		h, _ := setup(t)
		if w := request(h, "GET", "/api/appointments/99999", "", login(t, h)); w.Code != 404 {
			t.Fatal(w.Code)
		}
	})
	t.Run("validation", func(t *testing.T) {
		h, _ := setup(t)
		c := login(t, h)
		for _, body := range []string{`{}`, `{"slot_id":-1,"service_id":1}`, `{"slot_id":"bad"}`, `{"unknown":2}`, `{} {}`} {
			if w := request(h, "POST", "/api/appointments", body, c); w.Code != 400 {
				t.Fatalf("%s: %d", body, w.Code)
			}
		}
		for _, q := range []string{"size=101", "page=0", "status=no", "date_from=oops", "date_from=2026-10-02&date_to=2026-10-01"} {
			if w := request(h, "GET", "/api/appointments?"+q, "", c); w.Code != 400 {
				t.Fatal(q, w.Code)
			}
		}
	})
	t.Run("HTML", func(t *testing.T) {
		h, _ := setup(t)
		c := login(t, h)
		for _, path := range []string{"/login", "/appointments", "/booking?service_id=1", "/dashboard"} {
			w := request(h, "GET", path, "", c)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "<html") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
		}
	})
}
