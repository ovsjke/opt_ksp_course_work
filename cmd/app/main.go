package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pavel_ovsyannikov/internal/config"
	"pavel_ovsyannikov/internal/handler"
	"pavel_ovsyannikov/internal/repository"
	"pavel_ovsyannikov/internal/service"
	"pavel_ovsyannikov/internal/session"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if e := run(); e != nil {
		slog.Error("application stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	c, e := config.Load()
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r, e := repository.Open(ctx, c.DatabaseURL)
	if e != nil {
		return e
	}
	defer r.Pool.Close()
	t, e := template.ParseGlob("templates/*.html")
	if e != nil {
		return fmt.Errorf("templates: %w", e)
	}
	h := &handler.Handler{Service: &service.Service{Store: r}, Sessions: &session.Manager{Store: r, TTL: c.SessionTTL}, Templates: t}
	srv := &http.Server{Addr: ":" + c.Port, Handler: h.Router(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	slog.Info("started", "project", "pavel_ovsyannikov", "port", c.Port)
	select {
	case e := <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
