package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"pavel_ovsyannikov/internal/domain"
	mw "pavel_ovsyannikov/internal/middleware"
	"pavel_ovsyannikov/internal/service"
	"pavel_ovsyannikov/internal/session"
)

type Handler struct {
	Service   *service.Service
	Sessions  *session.Manager
	Templates *template.Template
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer, mw.NoCache)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]string{"status": "ok", "project": "pavel_ovsyannikov"})
	})
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) { h.render(w, "login", nil) })
	r.Post("/login", h.login)
	r.Post("/api/login", h.login)
	r.Group(func(r chi.Router) {
		r.Use(mw.Auth(h.Sessions))
		r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/appointments", 303) })
		r.Post("/logout", h.logout)
		r.Post("/api/logout", h.logout)
		r.Get("/api/services", h.services)
		r.Get("/api/specialists", h.specialists)
		r.Get("/api/slots", h.slots)
		r.Get("/api/appointments", h.list)
		r.Get("/api/appointments/{id}", h.detail)
		r.Post("/api/appointments", h.book)
		r.Post("/api/appointments/{id}/cancel", h.cancel)
		r.Get("/api/dashboard", h.dashboard)
		r.Get("/appointments", h.list)
		r.Get("/appointments/{id}", h.detail)
		r.Post("/appointments", h.book)
		r.Post("/appointments/{id}/cancel", h.cancel)
		r.Get("/booking", h.booking)
		r.Get("/dashboard", h.dashboard)
	})
	return r
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if e := json.NewEncoder(w).Encode(v); e != nil {
		slog.Error("encode response", "error", e)
	}
}
func fail(w http.ResponseWriter, e error) {
	status := 500
	switch {
	case errors.Is(e, domain.ErrInvalid):
		status = 400
	case errors.Is(e, domain.ErrUnauthorized):
		status = 401
	case errors.Is(e, domain.ErrNotFound):
		status = 404
	case errors.Is(e, domain.ErrConflict):
		status = 409
	}
	msg := e.Error()
	if status == 500 {
		slog.Error("request failed", "error", e)
		msg = "internal server error"
	}
	jsonResponse(w, status, map[string]string{"error": msg})
}
func (h *Handler) render(w http.ResponseWriter, name string, v any) {
	var b bytes.Buffer
	if e := h.Templates.ExecuteTemplate(&b, name, v); e != nil {
		fail(w, e)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b.Bytes())
}
func api(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") }
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return domain.ErrInvalid
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if api(r) {
		if e := decode(w, r, &v); e != nil {
			fail(w, e)
			return
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if e := r.ParseForm(); e != nil {
			fail(w, domain.ErrInvalid)
			return
		}
		v.Login = r.FormValue("login")
		v.Password = r.FormValue("password")
	}
	if len(v.Login) > 100 || len(v.Password) > 72 {
		fail(w, domain.ErrUnauthorized)
		return
	}
	id, e := h.Sessions.Login(r.Context(), v.Login, v.Password)
	if e != nil {
		fail(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "session_id", Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(h.Sessions.TTL.Seconds()), Expires: time.Now().Add(h.Sessions.TTL)})
	if api(r) {
		jsonResponse(w, 200, map[string]string{"status": "ok"})
	} else {
		http.Redirect(w, r, "/appointments", 303)
	}
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	c, e := r.Cookie("session_id")
	if e == nil {
		if e = h.Sessions.Store.DeleteSession(r.Context(), c.Value); e != nil {
			fail(w, e)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "session_id", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if api(r) {
		jsonResponse(w, 200, map[string]string{"status": "ok"})
	} else {
		http.Redirect(w, r, "/login", 303)
	}
}
func number(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || n > 10000000 {
		return 0, domain.ErrInvalid
	}
	return n, nil
}
func date(s string, def time.Time) (time.Time, error) {
	if s == "" {
		return def, nil
	}
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		t, e = time.Parse("2006-01-02", s)
	}
	if e != nil {
		return t, domain.ErrInvalid
	}
	return t, nil
}
func filter(r *http.Request) (domain.Filter, error) {
	q := r.URL.Query()
	var f domain.Filter
	var e error
	f.Page, e = number(q.Get("page"), 1)
	if e != nil {
		return f, e
	}
	f.Size, e = number(q.Get("size"), 20)
	if e != nil || f.Size > 100 {
		return f, domain.ErrInvalid
	}
	n, e := number(q.Get("specialist_id"), 0)
	if e != nil {
		return f, e
	}
	f.SpecialistID = int64(n)
	n, e = number(q.Get("service_id"), 0)
	if e != nil {
		return f, e
	}
	f.ServiceID = int64(n)
	f.Status = q.Get("status")
	if f.Status != "" && f.Status != "booked" && f.Status != "cancelled" {
		return f, domain.ErrInvalid
	}
	f.From, e = date(q.Get("date_from"), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	if e != nil {
		return f, e
	}
	f.To, e = date(q.Get("date_to"), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if e != nil || !f.To.After(f.From) {
		return f, domain.ErrInvalid
	}
	return f, nil
}
func routeID(r *http.Request) (int64, error) {
	n, e := number(chi.URLParam(r, "id"), 0)
	if e != nil || n == 0 {
		return 0, domain.ErrInvalid
	}
	return int64(n), nil
}
func (h *Handler) services(w http.ResponseWriter, r *http.Request) {
	v, e := h.Service.Store.Services(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	jsonResponse(w, 200, v)
}
func (h *Handler) specialists(w http.ResponseWriter, r *http.Request) {
	v, e := h.Service.Store.Specialists(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	jsonResponse(w, 200, v)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	f, e := filter(r)
	if e != nil {
		fail(w, e)
		return
	}
	v, e := h.Service.Store.Appointments(r.Context(), mw.User(r).ID, f)
	if e != nil {
		fail(w, e)
		return
	}
	if api(r) {
		jsonResponse(w, 200, v)
		return
	}
	ss, e := h.Service.Store.Specialists(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	sv, e := h.Service.Store.Services(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	q := r.URL.Query()
	q.Set("page", strconv.Itoa(f.Page+1))
	next := "/appointments?" + q.Encode()
	q.Set("page", strconv.Itoa(f.Page-1))
	prev := "/appointments?" + q.Encode()
	h.render(w, "appointments", map[string]any{"Page": v, "Specialists": ss, "Services": sv, "Filter": f, "Query": r.URL.Query(), "Next": next, "Prev": prev, "HasNext": f.Page*f.Size < v.Total, "HasPrev": f.Page > 1})
}
func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, e := routeID(r)
	if e != nil {
		fail(w, e)
		return
	}
	v, e := h.Service.Store.Appointment(r.Context(), mw.User(r).ID, id)
	if e != nil {
		fail(w, e)
		return
	}
	if api(r) {
		jsonResponse(w, 200, v)
	} else {
		h.render(w, "detail", v)
	}
}
func (h *Handler) slots(w http.ResponseWriter, r *http.Request) {
	f, e := filter(r)
	if e != nil || f.ServiceID == 0 {
		fail(w, domain.ErrInvalid)
		return
	}
	v, e := h.Service.Store.Slots(r.Context(), f.ServiceID, f.From, f.To)
	if e != nil {
		fail(w, e)
		return
	}
	jsonResponse(w, 200, v)
}
func (h *Handler) book(w http.ResponseWriter, r *http.Request) {
	var v struct {
		SlotID    int64 `json:"slot_id"`
		ServiceID int64 `json:"service_id"`
	}
	if api(r) {
		if e := decode(w, r, &v); e != nil {
			fail(w, e)
			return
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if e := r.ParseForm(); e != nil {
			fail(w, domain.ErrInvalid)
			return
		}
		v.SlotID, _ = strconv.ParseInt(r.FormValue("slot_id"), 10, 64)
		v.ServiceID, _ = strconv.ParseInt(r.FormValue("service_id"), 10, 64)
	}
	a, e := h.Service.Book(r.Context(), mw.User(r).ID, v.SlotID, v.ServiceID)
	if e != nil {
		fail(w, e)
		return
	}
	if api(r) {
		jsonResponse(w, 201, a)
	} else {
		http.Redirect(w, r, "/appointments/"+strconv.FormatInt(a.ID, 10), 303)
	}
}
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, e := routeID(r)
	if e == nil {
		e = h.Service.Cancel(r.Context(), mw.User(r).ID, id)
	}
	if e != nil {
		fail(w, e)
		return
	}
	if api(r) {
		jsonResponse(w, 200, map[string]string{"status": "cancelled"})
	} else {
		http.Redirect(w, r, "/appointments/"+strconv.FormatInt(id, 10), 303)
	}
}
func (h *Handler) booking(w http.ResponseWriter, r *http.Request) {
	f, e := filter(r)
	if e != nil {
		fail(w, e)
		return
	}
	sv, e := h.Service.Store.Services(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	slots := []domain.Slot{}
	if f.ServiceID > 0 {
		slots, e = h.Service.Store.Slots(r.Context(), f.ServiceID, f.From, f.To)
		if e != nil {
			fail(w, e)
			return
		}
	}
	h.render(w, "booking", map[string]any{"Services": sv, "Slots": slots, "ServiceID": f.ServiceID, "Query": r.URL.Query()})
}
func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	f, e := filter(r)
	if e != nil {
		fail(w, e)
		return
	}
	d, e := h.Service.Store.Dashboard(r.Context(), f.From, f.To)
	if e != nil {
		fail(w, e)
		return
	}
	if api(r) {
		jsonResponse(w, 200, d)
	} else {
		h.render(w, "dashboard", map[string]any{"Dashboard": d, "Query": r.URL.Query()})
	}
}
