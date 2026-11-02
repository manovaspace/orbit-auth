# orbit-auth

[![CI](https://github.com/manovaspace/orbit-auth/actions/workflows/ci.yml/badge.svg)](https://github.com/manovaspace/orbit-auth/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

Tier 2 product user authentication — OTP login, password login, JWT sessions, and API tokens.

Part of the [Manova / Orbit](https://github.com/manovaspace) open toolkit.

## Quick start (dev)

Requires Postgres and the [orbit-notifications](https://github.com/manovaspace/orbit-notifications) gRPC service.

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://auth:<password>@127.0.0.1:10333/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10110
export GRPC_PORT=10100
export HEALTH_PORT=10101
export JWT_PRIVATE_KEY_FILE=./secrets/jwt-private.pem
export JWT_ACTIVE_KEY_ID=orbit-dev-1
export JWT_PUBLIC_KEYS_DIR=./secrets/jwt-public-keys
export AUTH_AUTO_SELECT_SINGLE_ORG=true
go run ./cmd/auth
```

Orbit Auth signs access tokens with RS256, issuer `orbit-auth`, and the
configured `JWT_ACTIVE_KEY_ID` (`kid`). Use an RSA key of at least 2048 bits.
Keep the private PEM readable only by the auth service. The gateway publishes
the matching public PEMs as JWKS; consumers can verify tokens but cannot mint
them. See [the token contract](./token/README.md) for key rotation.

Default gRPC listen: `localhost:10100` (`GRPC_PORT`) · Health: `localhost:10101` (`HEALTH_PORT`).

Migrations are embedded in the binary. The binary applies pending migrations
on start (`MIGRATE_ON_START`, default `true`) and exposes the same commands
explicitly:

```bash
go run ./cmd/auth migrate status
go run ./cmd/auth migrate up
go run ./cmd/auth migrate down
```

Production adoption of the previous hand-written runner, rollback and the
one-schema rule: [docs/migrations.md](./docs/migrations.md).

## Layout

```
cmd/auth/                          wiring only: config, migrations, pool, servers
internal/domain/                   entities, pinned token rules and ports
internal/application/              OTP, password, refresh, API-token use cases
internal/infrastructure/
  clock/                           system clock (UTC)
  credentials/                     bcrypt, OTP code and API-secret adapters
  featureflags/                    Unleash/OpenFeature adapter
  grpc/                            AuthService adapter
  httpapi/                         Fiber v3 health server (/healthz, /readyz)
  ids/                             UUID identifiers
  jwt/                             RS256 access-token signer
  notifications/                   orbit-notifications gRPC client
  postgres/                        stores, sqlc queries and generated code
  postgres/migrations/             embedded goose migrations
internal/testutil/                 disposable-schema integration-test helper
```

The domain imports no infrastructure package. The application layer receives
every dependency through `application.Deps` and reads no environment variable;
`cmd/` only wires adapters.

## Development

```bash
cp .env.example .env   # set DATABASE_URL and the JWT key paths for local runs
go test ./...
```

Integration tests run against a disposable database and skip without it:

```bash
docker run -d --rm --name auth-pg \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=test \
  -p 55440:5432 postgres:18-alpine

TEST_DATABASE_URL='postgres://postgres:postgres@localhost:55440/test?sslmode=disable' \
  go test ./... -race -count=1

docker stop auth-pg
```

`TEST_DATABASE_URL` is accepted only when the connected database name starts
with `test`; each test creates and drops its own schema. The migration tests
also prove adoption of a database created by the old runner: schema, data and
`schema_migrations` stay unchanged.

Regenerate the sqlc package after changing SQL:

```bash
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
```

Generated code in `internal/infrastructure/postgres/sqlcgen` is committed and
must never be edited by hand.

## Features & Configuration

- **Feature Flags:** Uses Unleash (`UNLEASH_URL`, `UNLEASH_API_TOKEN`, `UNLEASH_APP_NAME`) to evaluate `manova.auth.email_otp` and `manova.auth.mobile_otp`.
- **Demo Mode:** When `DEMO_MODE=true` in dev, pre-seeds accounts from `AUTH_DEMO_USERS` JSON array. Enabling it outside dev fails closed.
- **Internal Auth:** Secured across Orbit gRPC mesh via `ORBIT_INTERNAL_TOKEN`.
- **Migrations:** embedded goose, `MIGRATE_ON_START` (default `true`).
- **Organization auto-select:** `AUTH_AUTO_SELECT_SINGLE_ORG` (default `true`) puts the `org` claim in a new login pair when the user has exactly one active membership. `SelectOrg` switches organization and rotates the refresh token.

## Events

This service publishes and consumes no events. It calls `orbit-notifications`
over gRPC, so `messagebus.Bus` is not wired here.

## Reusable authorization policy

The public `authorization` package evaluates product-owned role/permission
policies without importing gRPC, Fiber, or persistence packages. Orbit Auth
owns identity and authentication; each product owns role assignments and its
product-specific permissions. See `authorization/README.md` and its GoDoc
before integrating it.

## Reusable access token contract

The public `token` package owns RS256 access-token signing, key loading, and
issuer/expiry/subject validation for Orbit Auth and its consumers, plus the
optional `org` claim for the active organization. It does not own product roles
or refresh-token persistence. See `token/README.md` before integrating another
verifier.

## Documentation

- Contributing: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Security: [SECURITY.md](./SECURITY.md)
- Platform docs: https://manovaspace.github.io/docs/

## License

MIT — see [LICENSE](./LICENSE).
