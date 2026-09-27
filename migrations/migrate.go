package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var files embed.FS

func Apply(ctx context.Context, p *pgxpool.Pool) error {
	tx, e := p.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7483921)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.pavel_ovsyannikov_migrations(name text PRIMARY KEY)`); e != nil {
		return e
	}
	names, e := fs.Glob(files, "*.sql")
	if e != nil {
		return e
	}
	for _, n := range names {
		var done bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.pavel_ovsyannikov_migrations WHERE name=$1)`, n).Scan(&done); e != nil {
			return e
		}
		if done {
			continue
		}
		b, e := files.ReadFile(n)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, string(b)); e != nil {
			return fmt.Errorf("migration %s: %w", n, e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO public.pavel_ovsyannikov_migrations VALUES($1)`, n); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
