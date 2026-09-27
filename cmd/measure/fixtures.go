package main

import (
	"context"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These fixtures belong only to the CLI. No application repository behavior changes.
// A private slot identifies benchmark rows even if an HTTP response is lost.
type fixture struct{ SlotID, AppointmentID int64 }
type counts [9]int64

var countNames = [...]string{"users", "specialists", "services", "specialist_services", "slots", "appointments", "sessions", "booked", "cancelled"}

type benchmarkStore interface {
	Counts(context.Context) (counts, error)
	BindSession(context.Context, string) error
	Prepare(context.Context, int64, int64, bool) (fixture, error)
	Restore(context.Context, fixture) error
	Cleanup(context.Context, fixture) error
}

type postgresFixtures struct {
	pool   *pgxpool.Pool
	userID int64
}

func (s *postgresFixtures) Counts(ctx context.Context) (counts, error) {
	var c counts
	err := s.pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM users), (SELECT count(*) FROM specialists),
 (SELECT count(*) FROM services), (SELECT count(*) FROM specialist_services),
 (SELECT count(*) FROM slots), (SELECT count(*) FROM appointments),
 (SELECT count(*) FROM sessions),
 (SELECT count(*) FROM appointments WHERE status='booked'),
 (SELECT count(*) FROM appointments WHERE status='cancelled')`).Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5], &c[6], &c[7], &c[8])
	if err != nil {
		return c, fmt.Errorf("read benchmark counts: %w", err)
	}
	return c, nil
}

func (s *postgresFixtures) BindSession(ctx context.Context, id string) error {
	// Refuse to clean up a different DB when --base and DATABASE_URL disagree.
	if err := s.pool.QueryRow(ctx, `SELECT user_id FROM sessions WHERE id=$1 AND expires_at>now()`, id).Scan(&s.userID); err != nil {
		return fmt.Errorf("benchmark session missing in DATABASE_URL database (check --base): %w", err)
	}
	return nil
}

func (s *postgresFixtures) Prepare(ctx context.Context, sourceSlot, serviceID int64, booked bool) (fixture, error) {
	var f fixture
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return f, fmt.Errorf("begin fixture setup: %w", err)
	}
	defer tx.Rollback(ctx)
	var specialistID int64
	// Copy the suitable free slot without modifying it or its appointment history.
	err = tx.QueryRow(ctx, `INSERT INTO slots(specialist_id,starts_at,ends_at)
 SELECT specialist_id,starts_at,ends_at FROM slots WHERE id=$1
 RETURNING id,specialist_id`, sourceSlot).Scan(&f.SlotID, &specialistID)
	if err != nil {
		return f, fmt.Errorf("prepare benchmark slot: %w", err)
	}
	if booked {
		err = tx.QueryRow(ctx, `INSERT INTO appointments(user_id,specialist_id,service_id,slot_id,status)
 VALUES($1,$2,$3,$4,'booked') RETURNING id`, s.userID, specialistID, serviceID, f.SlotID).Scan(&f.AppointmentID)
		if err != nil {
			return f, fmt.Errorf("prepare benchmark appointment: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return f, fmt.Errorf("commit fixture setup: %w", err)
	}
	return f, nil
}

func (s *postgresFixtures) Restore(ctx context.Context, f fixture) error {
	tag, err := s.pool.Exec(ctx, `UPDATE appointments SET status='booked',cancelled_at=NULL
 WHERE id=$1 AND slot_id=$2 AND user_id=$3`, f.AppointmentID, f.SlotID, s.userID)
	if err != nil {
		return fmt.Errorf("restore benchmark appointment: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("benchmark appointment %d missing during restore", f.AppointmentID)
	}
	return nil
}

func (s *postgresFixtures) Cleanup(ctx context.Context, f fixture) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fixture cleanup: %w", err)
	}
	defer tx.Rollback(ctx)
	// Only this invocation's private slot can match: original records are never deleted.
	if _, err = tx.Exec(ctx, `DELETE FROM appointments WHERE slot_id=$1 AND user_id=$2`, f.SlotID, s.userID); err != nil {
		return fmt.Errorf("delete benchmark appointments: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM slots WHERE id=$1`, f.SlotID); err != nil {
		return fmt.Errorf("delete benchmark slot: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fixture cleanup: %w", err)
	}
	return nil
}

func reportCounts(out io.Writer, before, after counts) error {
	fmt.Fprintln(out, "state check (before -> after):")
	for i, name := range countNames {
		fmt.Fprintf(out, "  %-20s %d -> %d\n", name, before[i], after[i])
	}
	if before != after {
		return fmt.Errorf("benchmark changed table/status counts; inspect state before another run")
	}
	fmt.Fprintln(out, "state check: PASS")
	return nil
}
