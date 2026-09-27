package seed

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Fixed bcrypt hash of the intentionally public educational password demo.
const DemoHash = "$2a$10$liMSj2ZlyUoRdnaiMWWx9OaTG3BPbNs8m0/zYumnNRxpugi0hbhKy"

func Run(ctx context.Context, p *pgxpool.Pool, mode, hash string) (map[string]int64, error) {
	ns, nv, nslots, na := 10, 10, 500, 300
	if mode == "working" {
		ns, nv, nslots, na = 200, 30, 200000, 75000
	} else if mode != "small" {
		return nil, fmt.Errorf("mode must be small or working")
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `TRUNCATE sessions,appointments,slots,specialist_services,services,specialists,users RESTART IDENTITY CASCADE`); e != nil {
		return nil, e
	}
	counts := map[string]int64{}
	copyRows := func(table string, cols []string, rows [][]any) error {
		n, e := tx.CopyFrom(ctx, pgx.Identifier{"pavel_ovsyannikov", table}, cols, pgx.CopyFromRows(rows))
		counts[table] = n
		return e
	}
	if e = copyRows("users", []string{"id", "login", "password_hash"}, [][]any{{int64(1), "demo", hash}}); e != nil {
		return nil, e
	}
	rows := [][]any{}
	for i := 1; i <= ns; i++ {
		rows = append(rows, []any{int64(i), fmt.Sprintf("Специалист %03d", i)})
	}
	if e = copyRows("specialists", []string{"id", "name"}, rows); e != nil {
		return nil, e
	}
	rows = nil
	for i := 1; i <= nv; i++ {
		rows = append(rows, []any{int64(i), fmt.Sprintf("Консультация %02d", i), 15 * (1 + (i-1)%4)})
	}
	if e = copyRows("services", []string{"id", "name", "duration_minutes"}, rows); e != nil {
		return nil, e
	}
	supported := make(map[int][]int)
	rows = nil
	for i := 1; i <= ns; i++ {
		for j := 1; j <= nv; j++ {
			if (i+j)%3 != 0 {
				supported[i] = append(supported[i], j)
				rows = append(rows, []any{int64(i), int64(j)})
			}
		}
	}
	if e = copyRows("specialist_services", []string{"specialist_id", "service_id"}, rows); e != nil {
		return nil, e
	}
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	starts := make([]time.Time, nslots)
	rows = make([][]any, 0, nslots)
	for i := 0; i < nslots; i++ {
		round := i / ns
		starts[i] = base.AddDate(0, 0, round/8).Add(time.Duration(round%8) * time.Hour)
		rows = append(rows, []any{int64(i + 1), int64(i%ns + 1), starts[i], starts[i].Add(time.Hour)})
	}
	if e = copyRows("slots", []string{"id", "specialist_id", "starts_at", "ends_at"}, rows); e != nil {
		return nil, e
	}
	rng := rand.New(rand.NewSource(42))
	perm := rng.Perm(nslots)
	rows = make([][]any, 0, na)
	for i := 0; i < na; i++ {
		slot := perm[i]
		sp := slot%ns + 1
		sv := supported[sp][rng.Intn(len(supported[sp]))]
		status := "booked"
		created := starts[slot].Add(-time.Duration(24+rng.Intn(168)) * time.Hour)
		var cancelled any
		if rng.Intn(4) == 0 {
			status = "cancelled"
			cancelled = created.Add(2 * time.Hour)
		}
		rows = append(rows, []any{int64(i + 1), int64(1), int64(sp), int64(sv), int64(slot + 1), status, created, cancelled})
	}
	if e = copyRows("appointments", []string{"id", "user_id", "specialist_id", "service_id", "slot_id", "status", "created_at", "cancelled_at"}, rows); e != nil {
		return nil, e
	}
	counts["sessions"] = 0
	for _, t := range []string{"users", "specialists", "services", "slots", "appointments"} {
		if _, e = tx.Exec(ctx, fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('pavel_ovsyannikov.%s','id'),(SELECT max(id) FROM %s))`, t, t)); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return counts, nil
}
