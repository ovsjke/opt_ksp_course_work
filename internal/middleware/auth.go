package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"pavel_ovsyannikov/internal/domain"
	"pavel_ovsyannikov/internal/session"
)

type userKey struct{}

func User(r *http.Request) domain.User { u, _ := r.Context().Value(userKey{}).(domain.User); return u }
func Auth(m *session.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, e := r.Cookie("session_id")
			if e == nil {
				u, err := m.Store.Session(r.Context(), c.Value)
				if err == nil {
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
					return
				}
				if !errors.Is(err, domain.ErrNotFound) {
					slog.Error("session lookup", "error", err)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(500)
					w.Write([]byte(`{"error":"internal server error"}`))
					return
				}
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"authentication required"}`))
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
		})
	}
}
func NoCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
