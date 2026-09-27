package main

import (
	"context"
	"log/slog"
	"os"

	"pavel_ovsyannikov/internal/config"
	"pavel_ovsyannikov/internal/repository"
	"pavel_ovsyannikov/migrations"
)

func main() {
	if e := run(); e != nil {
		slog.Error("migration failed", "error", e)
		os.Exit(1)
	}
}
func run() error {
	c, e := config.Load()
	if e != nil {
		return e
	}
	r, e := repository.Open(context.Background(), c.DatabaseURL)
	if e != nil {
		return e
	}
	defer r.Pool.Close()
	return migrations.Apply(context.Background(), r.Pool)
}
