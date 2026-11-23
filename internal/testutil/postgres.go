// Package testutil provides opt-in integration-test infrastructure. Tests that
// call OpenPostgres skip when TEST_DATABASE_URL is unset, and refuse any
// database whose name does not start with "test". Each call gets its own
// database, created from template0, so tests never share schema-qualified
// objects or search_path state.
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
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/searchpath"
)

// Database is one isolated, freshly created database on the test server.
type Database struct {
	// Pool connects with the orbit-auth search_path (searchpath.Value).
	Pool *pgxpool.Pool
	// URL is the DSN Pool uses: the base URL with Name as the database and
	// the orbit-auth search_path applied.
	URL string
	// Name is the database created for this test.
	Name string
	// Schema is "public": the schema the pre-schema-move migrations create
	// objects in and where goose_db_version lives. Tests that filter
	// information_schema by schema use it.
	Schema string

	basePool *pgxpool.Pool
}

// OpenPostgres skips unless TEST_DATABASE_URL names a database whose name
// starts with "test", then creates a fresh database and drops it on cleanup.
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

	dbName := "test_auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := basePool.Exec(ctx, `CREATE DATABASE "`+dbName+`" TEMPLATE template0`); err != nil {
		basePool.Close()
		t.Fatalf("create isolated test database: %v", err)
	}

	scopedBase, err := withDatabaseName(rawURL, dbName)
	if err != nil {
		_ = dropDatabase(basePool, dbName)
		basePool.Close()
		t.Fatalf("scope test database URL: %v", err)
	}
	scopedURL, err := searchpath.Apply(scopedBase)
	if err != nil {
		_ = dropDatabase(basePool, dbName)
		basePool.Close()
		t.Fatalf("apply search_path to test database URL: %v", err)
	}
	pool, err := pgxpool.New(ctx, scopedURL)
	if err != nil {
		_ = dropDatabase(basePool, dbName)
		basePool.Close()
		t.Fatalf("connect isolated test database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		_ = dropDatabase(basePool, dbName)
		basePool.Close()
		t.Fatalf("ping isolated test database: %v", err)
	}

	db := &Database{Pool: pool, URL: scopedURL, Name: dbName, Schema: "public", basePool: basePool}
	t.Cleanup(func() {
		pool.Close()
		if err := dropDatabase(basePool, dbName); err != nil {
			t.Errorf("drop isolated test database: %v", err)
		}
		basePool.Close()
	})
	return db
}

// dropDatabase force-drops name through pool. Callers close every connection
// to the database first; WITH (FORCE) ends any that remain.
func dropDatabase(pool *pgxpool.Pool, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
	return err
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

// withDatabaseName returns rawURL with its database name replaced by name.
// Host, credentials and query parameters are kept.
func withDatabaseName(rawURL, name string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + name
	parsed.RawPath = ""
	return parsed.String(), nil
}
