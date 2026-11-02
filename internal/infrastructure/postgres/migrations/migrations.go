// Package migrations owns the embedded goose migration files and applies them
// through goose's Provider API. The previous hand-written runner is gone; the
// three migration files keep its exact SQL.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

const (
	defaultTableName = "goose_db_version"
	legacyTableName  = "schema_migrations"
	// advisoryLockID serialises first-start adoption between processes.
	advisoryLockID = 741238419
)

// Migrator applies the embedded migrations to one database.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
}

// Open connects to databaseURL and prepares the embedded migrations. The
// returned Migrator must be closed by the caller.
func Open(databaseURL string) (*Migrator, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("migrations: open database: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrations: prepare embedded migrations: %w", err)
	}
	return &Migrator{db: db, provider: provider}, nil
}

// Up adopts a legacy schema if needed, then applies every pending migration.
func (m *Migrator) Up(ctx context.Context) ([]*goose.MigrationResult, error) {
	if err := m.adoptLegacy(ctx); err != nil {
		return nil, err
	}
	results, err := m.provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrations: up: %w", err)
	}
	return results, nil
}

// Down rolls back the most recently applied migration.
func (m *Migrator) Down(ctx context.Context) (*goose.MigrationResult, error) {
	if err := m.adoptLegacy(ctx); err != nil {
		return nil, err
	}
	result, err := m.provider.Down(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrations: down: %w", err)
	}
	return result, nil
}

// Status reports every embedded migration and whether it is applied.
func (m *Migrator) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	if err := m.adoptLegacy(ctx); err != nil {
		return nil, err
	}
	status, err := m.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrations: status: %w", err)
	}
	return status, nil
}

// Version returns the current and latest migration versions.
func (m *Migrator) Version(ctx context.Context) (current, target int64, err error) {
	if err := m.adoptLegacy(ctx); err != nil {
		return 0, 0, err
	}
	current, target, err = m.provider.GetVersions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("migrations: version: %w", err)
	}
	return current, target, nil
}

// Close closes the migration database handle.
func (m *Migrator) Close() error {
	return m.db.Close()
}

// adoptLegacy maps the versions the previous hand-written runner recorded in
// schema_migrations to goose versions without running any migration SQL. It
// runs on every entry point, so `migrate status` followed by `migrate up` on a
// production database cannot re-run migration 002.
//
// The legacy table is read, never altered or dropped.
func (m *Migrator) adoptLegacy(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrations: begin adoption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(advisoryLockID)); err != nil {
		return fmt.Errorf("migrations: lock adoption: %w", err)
	}

	var gooseExists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, defaultTableName).Scan(&gooseExists); err != nil {
		return fmt.Errorf("migrations: inspect version table: %w", err)
	}
	if gooseExists {
		var applied int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM `+defaultTableName+` WHERE version_id > 0 AND is_applied`).Scan(&applied); err != nil {
			return fmt.Errorf("migrations: read version table: %w", err)
		}
		if applied > 0 {
			// Already a goose database, or adoption already ran.
			return tx.Commit()
		}
	}

	var legacyExists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, legacyTableName).Scan(&legacyExists); err != nil {
		return fmt.Errorf("migrations: inspect legacy table: %w", err)
	}
	if !legacyExists {
		return tx.Commit()
	}

	names, err := legacyNames(ctx, tx)
	if err != nil {
		return err
	}
	applyAll := false
	if len(names) == 0 {
		// The old runner also adopted a database whose schema_migrations was
		// empty but whose users table already existed: it marked every file
		// applied. Keep that state safe without running any SQL.
		var hasUsers bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = current_schema() AND table_name = 'users'
			)`).Scan(&hasUsers); err != nil {
			return fmt.Errorf("migrations: inspect legacy users table: %w", err)
		}
		if !hasUsers {
			return tx.Commit()
		}
		applyAll = true
	}

	if !gooseExists {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE `+defaultTableName+` (
			id integer PRIMARY KEY GENERATED BY DEFAULT AS IDENTITY,
			version_id bigint NOT NULL,
			is_applied boolean NOT NULL,
			tstamp timestamp NOT NULL DEFAULT now()
		)`); err != nil {
			return fmt.Errorf("migrations: create version table: %w", err)
		}
		if err := insertVersion(ctx, tx, 0); err != nil {
			return err
		}
	}

	for _, source := range m.provider.ListSources() {
		if !applyAll && !names[path.Base(source.Path)] {
			continue
		}
		if err := insertVersion(ctx, tx, source.Version); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func legacyNames(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM `+legacyTableName)
	if err != nil {
		return nil, fmt.Errorf("migrations: read legacy versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("migrations: scan legacy version: %w", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrations: read legacy versions: %w", err)
	}
	return names, nil
}

func insertVersion(ctx context.Context, tx *sql.Tx, version int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+defaultTableName+` (version_id, is_applied)
		SELECT $1, TRUE WHERE NOT EXISTS (
			SELECT 1 FROM `+defaultTableName+` WHERE version_id = $1 AND is_applied
		)`, version); err != nil {
		return fmt.Errorf("migrations: record adopted version %d: %w", version, err)
	}
	return nil
}
