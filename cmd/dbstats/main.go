package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"pavel_ovsyannikov/internal/config"
	"pavel_ovsyannikov/internal/measurement"
	"pavel_ovsyannikov/internal/repository"
)

func main() {
	if e := run(); e != nil {
		slog.Error("db statistics failed", "error", e)
		os.Exit(1)
	}
}
func run() error {
	base := flag.String("base", "http://localhost:8080", "base URL")
	method := flag.String("method", "GET", "HTTP method")
	path := flag.String("path", "/api/appointments?page=1&size=20", "API path")
	body := flag.String("body", "", "JSON request body; omitted by default")
	warmupPath := flag.String("warmup-path", "", "optional warm-up path using a separate mutation fixture")
	warmupBody := flag.String("warmup-body", "", "optional warm-up JSON body using a separate mutation fixture")
	flag.Parse()
	*method = strings.ToUpper(*method)
	if !strings.HasPrefix(*path, "/api/") {
		return fmt.Errorf("path must start with /api/")
	}
	requestBody, e := parseBody(*body)
	if e != nil {
		return e
	}
	warmBody := requestBody
	if *warmupBody != "" {
		warmBody, e = parseBody(*warmupBody)
		if e != nil {
			return fmt.Errorf("warm-up: %w", e)
		}
	}
	if *warmupPath == "" {
		*warmupPath = *path
	}
	if !strings.HasPrefix(*warmupPath, "/api/") {
		return fmt.Errorf("warmup-path must start with /api/")
	}
	c, e := config.Load()
	if e != nil {
		return e
	}
	ctx := context.Background()
	r, e := repository.Open(ctx, c.DatabaseURL)
	if e != nil {
		return e
	}
	defer r.Pool.Close()
	client := measurement.New(*base)
	if e = client.Login("demo", "demo"); e != nil {
		return e
	}
	defer client.Logout()
	// Login measurements should not overwrite a live session and leave it orphaned.
	if *method == "POST" && *path == "/api/login" {
		if e = client.Logout(); e != nil {
			return e
		}
	}
	if _, _, e = client.Request(*method, *warmupPath, warmBody); e != nil {
		return fmt.Errorf("warm-up: %w", e)
	}
	if *method == "POST" && *path == "/api/login" {
		if e = client.Logout(); e != nil {
			return e
		}
	}
	if *method == "POST" && *path == "/api/logout" {
		if e = client.Login("demo", "demo"); e != nil {
			return e
		}
	}

	if _, e = r.Pool.Exec(ctx, `SELECT public.pg_stat_statements_reset()`); e != nil {
		return fmt.Errorf("reset statistics (requires privileged DB user): %w", e)
	}
	responseBody, total, e := client.Request(*method, *path, requestBody)
	if e != nil {
		return e
	}
	// One snapshot before any further SQL; the snapshot query itself is not completed yet.
	// Top-level statements already include trigger work; do not double-count nested SQL.
	rows, e := r.Pool.Query(ctx, `SELECT calls,total_exec_time,query FROM public.pg_stat_statements WHERE toplevel AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND query NOT LIKE '%pg_stat_statements%' ORDER BY total_exec_time DESC`)
	if e != nil {
		return e
	}
	defer rows.Close()
	type query struct {
		Calls int64   `json:"calls"`
		MS    float64 `json:"execution_ms"`
		SQL   string  `json:"sql"`
	}
	queries := []query{}
	var calls int64
	var db float64
	for rows.Next() {
		var q query
		if e = rows.Scan(&q.Calls, &q.MS, &q.SQL); e != nil {
			return e
		}
		queries = append(queries, q)
		calls += q.Calls
		db += q.MS
	}
	if e = rows.Err(); e != nil {
		return e
	}
	fmt.Printf("operation: %s %s\n", *method, *path)
	ms := float64(total.Microseconds()) / 1000
	fmt.Printf("total request: %.3f ms\nDB execution: %.3f ms\napproximate application + network + pool/planning: %.3f ms\nSQL calls: %d\nresponse bytes: %d\n", ms, db, ms-db, calls, len(responseBody))
	if len(queries) > 10 {
		queries = queries[:10]
	}
	b, e := json.MarshalIndent(queries, "", "  ")
	if e != nil {
		return e
	}
	fmt.Println(string(b))
	fmt.Println("Use an idle isolated stand: pg_stat_statements is global; execution time excludes network, pool waits and SQL planning.")
	return nil
}

func parseBody(value string) (any, error) {
	if value == "" {
		return nil, nil
	}
	if !json.Valid([]byte(value)) {
		return nil, fmt.Errorf("body must be valid JSON")
	}
	return json.RawMessage(value), nil
}
