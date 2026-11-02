// Package testutil provides opt-in integration-test infrastructure. Tests that
// call OpenPostgres skip when TEST_DATABASE_URL is unset, and refuse any
// database whose name does not start with "test".
package testutil

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Database is one isolated schema inside the disposable test database.
type Database struct {
	// Pool has search_path set to Schema,public.
	Pool *pgxpool.Pool
	// URL is a DSN with search_path set to Schema,public.
	URL string
	// Schema is the isolated schema every object is created in.
	Schema string

	basePool *pgxpool.Pool
}

// OpenPostgres skips unless TEST_DATABASE_URL names a database whose name
// starts with "test", then creates an isolated schema and drops it on cleanup.
func OpenPostgres(t testing.TB) *Database {
	t.Helper()

	rawURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if rawURL == "" {
		t.Skip("set TEST_DATABASE_URL to an isolated disposable database")
	}
	name, err := databaseName(rawURL)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	if !strings.HasPrefix(name, "test") {
		t.Fatalf("refusing integration tests against database %q; name must start with test", name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	basePool, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	if err := basePool.Ping(ctx); err != nil {
		basePool.Close()
		t.Fatalf("ping test database: %v", err)
	}

	schema := "auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := basePool.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		basePool.Close()
		t.Fatalf("create isolated test schema: %v", err)
	}

	scopedURL, err := withSearchPath(rawURL, schema)
	if err != nil {
		_, _ = basePool.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
		basePool.Close()
		t.Fatalf("scope test database URL: %v", err)
	}
	pool, err := pgxpool.New(ctx, scopedURL)
	if err != nil {
		_, _ = basePool.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
		basePool.Close()
		t.Fatalf("connect isolated test schema: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		_, _ = basePool.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
		basePool.Close()
		t.Fatalf("ping isolated test schema: %v", err)
	}

	db := &Database{Pool: pool, URL: scopedURL, Schema: schema, basePool: basePool}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := basePool.Exec(ctx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
		basePool.Close()
	})
	return db
}

// databaseName returns the database name from a PostgreSQL URL.
func databaseName(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	if name == "" {
		return "", fmt.Errorf("database URL has no database name")
	}
	return name, nil
}

// withSearchPath adds search_path=schema,public to a PostgreSQL URL.
func withSearchPath(rawURL, schema string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("search_path", schema+",public")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
