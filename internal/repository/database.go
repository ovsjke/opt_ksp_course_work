package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Repository, error) {
	c, e := pgxpool.ParseConfig(url)
	if e != nil {
		return nil, fmt.Errorf("parse database URL: %w", e)
	}
	c.ConnConfig.RuntimeParams["search_path"] = "pavel_ovsyannikov,public"
	p, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		return nil, fmt.Errorf("create pool: %w", e)
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, fmt.Errorf("ping database: %w", e)
	}
	return &Repository{p}, nil
}
