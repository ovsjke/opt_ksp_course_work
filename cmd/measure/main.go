package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/url"
	"os"
	"sort"
	"time"

	"pavel_ovsyannikov/internal/config"
	"pavel_ovsyannikov/internal/domain"
	"pavel_ovsyannikov/internal/measurement"
	"pavel_ovsyannikov/internal/repository"
)

func main() {
	if err := run(); err != nil {
		slog.Error("measurement failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	base := flag.String("base", "http://localhost:8080", "base URL")
	n := flag.Int("n", 20, "repetitions, minimum 20")
	profile := flag.String("profile", "small", "seed profile label: small or working")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := repository.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	return measure(ctx, measurement.New(*base), &postgresFixtures{pool: db.Pool}, *n, *profile, os.Stdout)
}

func measure(ctx context.Context, c *measurement.Client, store benchmarkStore, n int, profile string, out io.Writer) (resultErr error) {
	if n < 20 {
		return fmt.Errorf("n must be >=20")
	}
	if profile != "small" && profile != "working" {
		return fmt.Errorf("unknown profile")
	}
	before, err := store.Counts(ctx)
	if err != nil {
		return err
	}
	loggedIn := false
	defer func() {
		if loggedIn {
			resultErr = errors.Join(resultErr, c.Logout())
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		after, err := store.Counts(cleanupCtx)
		resultErr = errors.Join(resultErr, err)
		if err == nil {
			resultErr = errors.Join(resultErr, reportCounts(out, before, after))
		}
	}()
	if err := c.Login("demo", "demo"); err != nil {
		return err
	}
	loggedIn = true
	baseURL, err := url.Parse(c.Base)
	if err != nil {
		return err
	}
	var sessionID string
	for _, cookie := range c.HTTP.Jar.Cookies(baseURL) {
		if cookie.Name == "session_id" {
			sessionID = cookie.Value
		}
	}
	if err := store.BindSession(ctx, sessionID); err != nil {
		return err
	}
	const period = "date_from=2026-10-01&date_to=2026-10-08"
	b, _, err := c.Request("GET", "/api/appointments?size=1", nil)
	if err != nil {
		return err
	}
	var page domain.Page
	if err = json.Unmarshal(b, &page); err != nil {
		return err
	}
	if len(page.Items) == 0 {
		return fmt.Errorf("seed database first")
	}
	b, _, err = c.Request("GET", "/api/slots?service_id=1&"+period, nil)
	if err != nil {
		return err
	}
	var slots []domain.Slot
	if err = json.Unmarshal(b, &slots); err != nil {
		return err
	}
	if len(slots) == 0 {
		return fmt.Errorf("no free slots for service 1")
	}

	type operation struct {
		name    string
		request func() ([]byte, time.Duration, error)
	}
	get := func(path string) func() ([]byte, time.Duration, error) {
		return func() ([]byte, time.Duration, error) { return c.Request("GET", path, nil) }
	}
	mutation := func(cancelAppointment bool) func() ([]byte, time.Duration, error) {
		return func() (body []byte, duration time.Duration, requestErr error) {
			f, err := store.Prepare(ctx, slots[0].ID, 1, cancelAppointment)
			if err != nil {
				return nil, 0, err
			}
			// Request stops its timer before this deferred restoration/cleanup runs.
			// This also executes after warm-up, an HTTP error, or a malformed response.
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if cancelAppointment {
					requestErr = errors.Join(requestErr, store.Restore(cleanupCtx, f))
				}
				requestErr = errors.Join(requestErr, store.Cleanup(cleanupCtx, f))
			}()
			if cancelAppointment {
				return c.Request("POST", fmt.Sprintf("/api/appointments/%d/cancel", f.AppointmentID), nil)
			}
			return c.Request("POST", "/api/appointments", map[string]int64{"slot_id": f.SlotID, "service_id": 1})
		}
	}
	// Business requests all use c's original session. Authorization has its own jar.
	auth := measurement.New(c.Base)
	ops := []operation{
		{"appointments first page", get("/api/appointments?page=1&size=20")},
		{"appointments filtered", get("/api/appointments?specialist_id=1&status=booked&" + period)},
		{"appointment detail", get(fmt.Sprintf("/api/appointments/%d", page.Items[0].ID))},
		{"services", get("/api/services")},
		{"specialists", get("/api/specialists")},
		{"available slots", get("/api/slots?service_id=1&" + period)},
		{"book appointment", mutation(false)},
		{"cancel appointment", mutation(true)},
		{"dashboard", get("/api/dashboard?" + period)},
		{"login", func() ([]byte, time.Duration, error) {
			b, duration, err := auth.Request("POST", "/api/login", map[string]string{"login": "demo", "password": "demo"})
			if err != nil {
				return nil, 0, err
			}
			if err := auth.Logout(); err != nil {
				return nil, 0, fmt.Errorf("login cleanup: %w", err)
			}
			return b, duration, nil
		}},
		{"logout", func() ([]byte, time.Duration, error) {
			if err := auth.Login("demo", "demo"); err != nil {
				return nil, 0, fmt.Errorf("logout setup: %w", err)
			}
			return auth.Request("POST", "/api/logout", nil)
		}},
	}
	fmt.Fprintf(out, "profile=%s repetitions=%d period=[2026-10-01,2026-10-08) UTC; durations include body read\n", profile, n)
	fmt.Fprintf(out, "%-28s %10s %10s %10s %12s\n", "operation", "p50 ms", "p95 ms", "max ms", "bytes min/max")
	for _, op := range ops {
		// Warm-up runs the same setup/cleanup as a sample but is never recorded.
		if _, _, err := op.request(); err != nil {
			return fmt.Errorf("%s warm-up: %w", op.name, err)
		}
		times := make([]float64, 0, n)
		minSize, maxSize := math.MaxInt, 0
		for i := 0; i < n; i++ {
			b, duration, err := op.request()
			if err != nil {
				return fmt.Errorf("%s sample %d: %w", op.name, i+1, err)
			}
			times = append(times, float64(duration)/float64(time.Millisecond))
			minSize = min(minSize, len(b))
			maxSize = max(maxSize, len(b))
		}
		sort.Float64s(times)
		fmt.Fprintf(out, "%-28s %10.3f %10.3f %10.3f %5d/%d\n", op.name, times[int(math.Ceil(.50*float64(n)))-1], times[int(math.Ceil(.95*float64(n)))-1], times[len(times)-1], minSize, maxSize)
	}
	return nil
}
