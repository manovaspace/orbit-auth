# Migrations

This service uses [goose](https://github.com/pressly/goose) with SQL migrations
embedded in the binary (`internal/infrastructure/postgres/migrations`). The
hand-written runner that parsed these files is gone. There is no runtime
`migrations/` directory.

## Commands

```bash
# apply every pending migration (also the default at start-up)
go run ./cmd/auth migrate
go run ./cmd/auth migrate up

# roll back the most recent migration
go run ./cmd/auth migrate down

# show every embedded migration and the current version
go run ./cmd/auth migrate status
```

`MIGRATE_ON_START` defaults to `true`, which keeps the previous release
behaviour: the binary applies pending migrations before it opens the pool. Set
`MIGRATE_ON_START=false` when a deploy step runs `migrate` explicitly and the
service must not touch the schema.

`migrate down` rolls back exactly one migration. There is no `down --all`.

## Adding a migration

1. Add the next numbered file, for example
   `internal/infrastructure/postgres/migrations/004_add_something.sql`.
   The file must contain `-- +goose Up` first and `-- +goose Down` second.
2. Keep the Down section reversible. If a change cannot be reversed safely,
   write the Down to restore the object and document the manual step here.
3. Regenerate the sqlc package when a query or the schema changes:
   `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate`. Never edit
   `internal/infrastructure/postgres/sqlcgen` by hand.
4. Run the migration tests against a disposable database (below).
5. Fix a defect in a new migration; never edit a migration that has already run
   in production.

## Production adoption (existing database)

Production databases were created by the previous hand-written runner, which
recorded each applied file name in `schema_migrations` and never created
`goose_db_version`. The goose versions 1, 2 and 3 are `001_auth.sql`,
`002_mobile_otp.sql` and `003_password_api_tokens.sql`; their SQL is byte for
byte the SQL the old runner applied.

`migrate up` performs a one-time adoption before goose runs:

- when `goose_db_version` has no applied version and `schema_migrations` exists,
  every file name recorded there is mapped to its goose version;
- no migration SQL runs during adoption, and `schema_migrations` is read but
  never altered or dropped;
- the old runner's other case (`schema_migrations` empty but `users` already
  exists) is adopted the same way: every embedded version is recorded, because
  the old runner had marked every file applied. A fresh database has neither
  table and gets the normal goose path;
- the mapping is written with a conditional insert, so a second `migrate up`,
  and `migrate status` followed by `migrate up`, are both no-ops.

Exact adoption on a production database:

```bash
# 1. Back up first; this also gives the rollback point.
pg_dump "$DATABASE_URL" --format=custom --file auth-before-goose.dump

# 2. Inspect the legacy state.
psql "$DATABASE_URL" -c 'SELECT name FROM schema_migrations ORDER BY name'
psql "$DATABASE_URL" -c "SELECT to_regclass('goose_db_version')"

# 3. Adopt. No migration SQL runs on this database.
/app/auth migrate up

# 4. Verify the recorded versions and that schema_migrations is unchanged.
psql "$DATABASE_URL" -c 'SELECT version_id, is_applied FROM goose_db_version ORDER BY id'
psql "$DATABASE_URL" -c 'SELECT name FROM schema_migrations ORDER BY name'
```

The adoption is covered by `TestAdoptionOnLegacyDatabaseChangesNothing`: it
builds the exact legacy state with real users, OTP, session and API-token rows,
snapshots every table and index `relfilenode` plus all rows, runs `migrate up`,
and asserts the schema, the data and `schema_migrations` are identical while
versions 0, 1, 2 and 3 are recorded. A second `up` applies nothing.
`TestStatusBeforeUpStillAdopts` covers `migrate status` running first.

Run them with:

```bash
docker run -d --rm --name auth-pg \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=test \
  -p 55440:5432 postgres:18-alpine
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:55440/test?sslmode=disable' \
  go test ./internal/infrastructure/postgres/migrations/ -race -count=1 -v
```

## Rolling back

Rollback is migration-by-migration and destructive:

```bash
# 003: drops api_tokens and the password/display/status/profile columns
/app/auth migrate down

# 002: renames otp_challenges.identifier back to email and drops mobile
/app/auth migrate down

# 001: drops refresh_tokens, otp_challenges and users
/app/auth migrate down
```

`migrate down` is a schema rollback, not a deployment rollback. Deploying the
previous image does not need one when the change followed expand-then-contract;
the previous binary ignores new nullable columns and new tables.

To restore the full database from the adoption backup:

```bash
pg_restore --clean --if-exists --dbname "$DATABASE_URL" auth-before-goose.dump
```

## Expand, then contract

A breaking schema change takes two releases. Both releases must run against the
same schema, and the schema must be compatible with the release before them.

1. **Expand** (release N): add the new column/table, nullable or with a
   default; write both the old and new shapes; keep the old read path.
2. **Migrate data** (release N or a job): backfill in batches. Do not hold a
   long transaction.
3. **Switch reads** (release N+1): read the new shape, still write both if the
   previous release may still run.
4. **Contract** (release N+2): drop the old column/table in a migration whose
   Down restores it.

A single release never drops an object that the previous release still reads.

## One schema per service

`orbit-auth` owns the dedicated `auth` logical database and its `public`
schema. No other service reads these tables; consumers call the gRPC API or
verify access tokens through the public `token` package. That is the "one
schema per service" rule for this service.

No data moves in this change. `schema_migrations` stays in place until a later
contract migration removes it; no release reads it after adoption.

If the platform later gives every service a named schema inside a shared
database, the move is:

1. `CREATE SCHEMA auth;`
2. Set `search_path=auth,public` for this service's role/DSN.
3. `ALTER TABLE users SET SCHEMA auth;` (indexes and constraints follow the
   table; repeat for every table and `goose_db_version`).
4. Apply the same DSN change in every environment, then remove `public` from
   the service's search path in a later release.

That move is a deployment task with a maintenance window, not a migration.
