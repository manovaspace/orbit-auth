# orbit-auth

[![CI](https://github.com/manovaspace/orbit-auth/actions/workflows/ci.yml/badge.svg)](https://github.com/manovaspace/orbit-auth/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

Tier 2 product user authentication — OTP login, password login, JWT sessions, and API tokens.

Part of the [Manova / Orbit](https://github.com/manovaspace) open toolkit.

## Quick start (dev)

Requires Postgres and the [orbit-notifications](https://github.com/manovaspace/orbit-notifications) gRPC service.

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://auth:<password>@127.0.0.1:10000/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10012
export GRPC_PORT=10010
export HEALTH_PORT=10011
export JWT_PRIVATE_KEY_FILE=./secrets/jwt-private.pem
export JWT_ACTIVE_KEY_ID=orbit-dev-1
export JWT_PUBLIC_KEYS_DIR=./secrets/jwt-public-keys
export AUTH_AUTO_SELECT_SINGLE_ORG=true
export AUTH_SIGNUP=off
export BUS_TRANSPORT=inproc
# export BUS_NATS_URL=nats://localhost:10006
go run ./cmd/auth
```

Orbit Auth signs access tokens with RS256, issuer `orbit-auth`, and the
configured `JWT_ACTIVE_KEY_ID` (`kid`). Use an RSA key of at least 2048 bits.
Keep the private PEM readable only by the auth service. The gateway publishes
the matching public PEMs as JWKS; consumers can verify tokens but cannot mint
them. See [the token contract](./token/README.md) for key rotation.

Default gRPC listen: `localhost:10010` (`GRPC_PORT`) · Health: `localhost:10011` (`HEALTH_PORT`).

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
  grpc/                            AuthService, OrgService and SignupService adapters
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

The NATS JetStream durable-consumer test runs when `TEST_NATS_URL` is set (or
`BUS_NATS_URL` as a fallback) and skips when neither is set:

```bash
docker run -d --rm --name auth-nats -p 4222:4222 nats:2.14-alpine -js

TEST_DATABASE_URL='postgres://postgres:postgres@localhost:55440/test?sslmode=disable' \
TEST_NATS_URL='nats://localhost:4222' \
  go test ./... -race -count=1

docker stop auth-nats
```

Regenerate the sqlc package after changing SQL:

```bash
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
```

Generated code in `internal/infrastructure/postgres/sqlcgen` is committed and
must never be edited by hand.

## Features & Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN owned by this service |
| `NOTIFICATIONS_GRPC_ADDR` | `localhost:10012` | `orbit-notifications` gRPC endpoint |
| `GRPC_PORT` | `10010` | gRPC listen port |
| `HEALTH_PORT` | `10011` | Fiber health port (`/healthz`, `/readyz`) |
| `MIGRATE_ON_START` | `true` | Apply embedded goose migrations on start |
| `AUTH_AUTO_SELECT_SINGLE_ORG` | `true` | Add the `org` claim when the user has one active membership |
| `AUTH_SIGNUP` | `off` | `open` enables self-serve sign-up |
| `DEMO_MODE` | `false` | Dev-only demo account seeding |
| `BUS_TRANSPORT` | `inproc` | Lifecycle event transport: `inproc`, `nats` or `redis` |
| `BUS_NATS_URL` | unset | NATS endpoint when `BUS_TRANSPORT=nats` |
| `BUS_REDIS_URL` | unset | Redis endpoint when `BUS_TRANSPORT=redis` |

- **Feature Flags:** Uses Unleash (`UNLEASH_URL`, `UNLEASH_API_TOKEN`, `UNLEASH_APP_NAME`) to evaluate `manova.auth.email_otp` and `manova.auth.mobile_otp`.
- **Demo Mode:** When `DEMO_MODE=true` in dev, pre-seeds accounts from `AUTH_DEMO_USERS` JSON array. Enabling it outside dev fails closed.
- **Internal Auth:** Secured across Orbit gRPC mesh via `ORBIT_INTERNAL_TOKEN`.
- **Migrations:** embedded goose, `MIGRATE_ON_START` (default `true`).
- **Organization auto-select:** `AUTH_AUTO_SELECT_SINGLE_ORG` (default `true`) puts the `org` claim in a new login pair when the user has exactly one active membership. `SelectOrg` switches organization and rotates the refresh token.
- **Self-serve sign-up:** `AUTH_SIGNUP=open` enables `SignupService.RegisterWithEmail` and `SignupService.VerifySignup`; the default `off` rejects both with `FailedPrecondition` before anything is written. A new email account stays `signup_pending` until `VerifySignup` receives both the `signup_verify` OTP and the pending account password. A retry for a still-pending address replaces its credentials and reissues the code (at most once per 60 seconds); an active account is left untouched. Admin and demo accounts are verified from the start, and a valid `invite_token` verifies the address without an OTP.
- **Lifecycle events:** `BUS_TRANSPORT` selects the message bus (`inproc` default, `nats` with `BUS_NATS_URL`, `redis` with `BUS_REDIS_URL`). See [Events](#events).

## Events

Lifecycle events are written to the Postgres outbox (`outbox_events`) in the same transaction as the change, then relayed to `orbit-message-bus` by a relay in `cmd/auth`:

| Subject | Metadata version | Trigger |
| --- | --- | --- |
| `auth.user.created` | `1` | OTP first login, sign-up, invite-sign-up, demo seed |
| `auth.org.created` | `1` | `CreateOrg`, sign-up with `org_name` |
| `auth.membership.added` | `1` | `AddMember`, invite acceptance, creator membership |
| `auth.membership.removed` | `1` | `RemoveMember` |
| `auth.membership.suspended` | `1` | `SuspendMember` |

Subjects use the bus's three-segment `{domain}.{entity}.{action}` form; the event version is the envelope `metadata.version` value (`"1"`), not a subject suffix. Payloads carry ids, the organization name for `org.created`, the role-free membership status and `occurred_at`, and never carry emails, tokens or secrets. Delivery is at-least-once; consumers are idempotent on the envelope `id`.

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
