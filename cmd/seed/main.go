package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"pavel_ovsyannikov/internal/config"
	"pavel_ovsyannikov/internal/repository"
	"pavel_ovsyannikov/seed"
)

func main() {
	if e := run(); e != nil {
		slog.Error("seed failed", "error", e)
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "small", "small or working; replaces all application data")
	flag.Parse()
	c, e := config.Load()
	if e != nil {
		return e
	}
	r, e := repository.Open(context.Background(), c.DatabaseURL)
	if e != nil {
		return e
	}
	defer r.Pool.Close()
	counts, e := seed.Run(context.Background(), r.Pool, *mode, seed.DemoHash)
	if e != nil {
		return e
	}
	for _, t := range []string{"users", "specialists", "services", "specialist_services", "slots", "appointments", "sessions"} {
		fmt.Printf("%s: %d\n", t, counts[t])
	}
	return nil
}
