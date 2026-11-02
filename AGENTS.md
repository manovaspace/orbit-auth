---
read_with:
  - handbook/docs/orbit/architecture/go-toolchain.md
  - handbook/docs/orbit/architecture/library-design.md
  - handbook/docs/orbit/decisions/011-platform-auth-notifications.md
  - handbook/docs/orbit/decisions/016-orbit-auth-password-and-api-tokens.md
  - handbook/docs/orbit/guides/platform-dev-quickstart.md
---

# orbit-auth

Tier 2 product user authentication — OTP login, password login, JWT sessions, API tokens. Staff SSO is Keycloak workforce identity (ADR-027).

## Commands

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://auth:<password>@127.0.0.1:10333/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10110
export GRPC_PORT=10100
export HEALTH_PORT=10101
export AUTH_AUTO_SELECT_SINGLE_ORG=true
export AUTH_SIGNUP=off
go run ./cmd/auth
go run ./cmd/auth migrate status   # up, down, status
go test ./...
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:55440/test?sslmode=disable' go test ./... -race -count=1
./scripts/generate-proto.sh   # after proto changes
```

Ports: gRPC on **10100** (`GRPC_PORT`), HTTP health on **10101** (`HEALTH_PORT`, `/healthz`, `/readyz`). HTTP runs on Fiber v3; gRPC is unchanged.
Requires `orbit-notifications` on **10110** and dev Postgres.

Start with orbit-infra: `orbit/orbit-infra/scripts/start-tier2.sh`

## Architecture

- `internal/domain` — user, OTP and API-token types, lifecycle event payloads, pinned token rules and the ports: `OTPStore`, `UserStore`, `RefreshTokenStore`, `APITokenStore`, `Notifier`, `Flags`, `Clock`, `IDGenerator`, `TokenSigner`, `Hasher`, `CodeGenerator`, `SecretGenerator`, `EventSink`. It imports no infrastructure, driver, HTTP framework or process package.
- `internal/application` — OTP, password, refresh-token, API-token and demo-seed use cases. It receives `application.Deps` and reads no environment variable.
- `internal/infrastructure` — adapters: `postgres` (sqlc-generated queries), `postgres/migrations` (embedded goose), `grpc`, `httpapi` (Fiber v3 probes), `notifications` (gRPC client), `featureflags`, `credentials` (bcrypt, OTP codes, API secrets), `jwt` (RS256 signer), `clock`, `ids`.
- `cmd/auth` — wiring only, including `migrate` and demo-mode parsing.
- `.golangci.yml` `depguard` rules enforce both boundaries. Do not weaken them to land a change.

## Non-negotiables

1. **Tenant isolation is sacred** — all user queries and data operations must be scoped by tenant/user ID. Zero cross-tenant leakage.
2. **JWT verification before mutations** — never trust unauthenticated claims or bypass token validation.
3. **Notifications strictly via gRPC** — call `orbit-notifications` on port 10110; never implement direct SMTP or raw mail calls in orbit-auth.
4. **`log/slog` only** — `log.Printf` and `fmt.Println` logging are strictly forbidden.
5. **Sequential embedded migrations** — files in `internal/infrastructure/postgres/migrations/` are numbered sequentially without gaps. Never alter previously applied migrations. Adoption of the old `schema_migrations` state is documented in [docs/migrations.md](./docs/migrations.md).
6. **Zero credential leakage** — never log passwords, plaintext OTP codes, or JWT signing keys in stdout/stderr.
7. **Credential formats are contracts** — bcrypt cost, OTP rules, refresh-token shape, `oat_` API-token format and RS256 claims are pinned by characterization tests. Changing one needs a migration and an ADR, not a refactor.

## Engineering gotchas

- **gRPC interceptor order:** Panic recovery must wrap the outermost boundary; auth token validation must precede tenant context extraction in the interceptor chain.
- **Proto code regeneration:** When editing `api/proto/auth/v1/`, run `./scripts/generate-proto.sh`. Never manually modify generated `*.pb.go` files.
- **Unleash flag fallback:** When Unleash flags (`manova.auth.email_otp`, `manova.auth.mobile_otp`) are toggled off or unreachable, handlers return typed errors (`ErrEmailOTPDisabled`). Unit tests must mock the flag provider.
- **Demo mode guard:** `DEMO_MODE=true` is permitted only when `DEPLOYMENT_ENVIRONMENT=dev`. `cmd/auth` fails closed before the service starts if demo mode is enabled elsewhere.
- **Migration adoption:** `migrate up` maps the old `schema_migrations` rows to goose versions without running SQL and without touching `schema_migrations`. Do not drop that table in this release.

## Feature Flags & Demo Mode

- **Unleash flags:** `manova.auth.email_otp` and `manova.auth.mobile_otp` (`UNLEASH_URL`, `UNLEASH_API_TOKEN`, `UNLEASH_APP_NAME`). When disabled, `RequestOTP` returns `ErrEmailOTPDisabled` or `ErrMobileOTPDisabled`.
- **Organization auto-select:** `AUTH_AUTO_SELECT_SINGLE_ORG` (default `true`) adds the optional `org` access-token claim at login when the user has exactly one active membership. Zero or many memberships omit it. `SelectOrg` rotates the refresh token and issues a pair carrying the selected org.
- **Demo seeding:** `DEMO_MODE=true` (dev only) and `AUTH_DEMO_USERS` JSON array seeds test user accounts into Postgres on startup. `cmd/auth` parses the JSON; the application validates and hashes each entry.
- **Self-serve sign-up:** `AUTH_SIGNUP` is `open` or `off` (default `off`), read in `cmd/auth` and injected through `application.Deps`. While off, `SignupService` returns `codes.FailedPrecondition` "sign-up is disabled" and writes nothing. A sign-up account is `signup_pending` until `VerifySignup` receives both the `signup_verify` OTP and the pending account password; password and OTP login refuse it with the generic invalid-credentials error. A retry for a still-pending address replaces its credentials and reissues the code (at most once per 60 seconds); an active account is left untouched. Admin and demo accounts are verified from the start, and a valid invite token verifies the address without an OTP.

## Events

Lifecycle events are written to the Postgres outbox (`outbox_events`, migration 008) in the same transaction as the change and relayed by `outbox.Relay` to `orbit-message-bus`:

- `auth.user.created` — a user is created by OTP login, sign-up, invite-sign-up or demo seeding
- `auth.org.created` — `CreateOrg` or sign-up with `org_name`
- `auth.membership.added` — `AddMember`, invite acceptance, or the creator membership
- `auth.membership.removed` — `RemoveMember`
- `auth.membership.suspended` — `SuspendMember`

Subjects are the three-segment `{domain}.{entity}.{action}` shape. The version is envelope metadata `version="1"`, not a subject suffix. Payloads carry ids, the organization name for `org.created`, the role-free membership status and `occurred_at`; they never carry emails, tokens or secrets.

`BUS_TRANSPORT` selects the transport: `inproc` (default; relayed and logged, no subscribers), `nats` with `BUS_NATS_URL`, or `redis` with `BUS_REDIS_URL`. The relay runs in `cmd/auth` for the life of the process and stops on graceful shutdown; publish failures are logged with `slog` and retried, never crashing the gRPC server.

## Stack

- Go 1.27.3, gRPC + Fiber v3 health + Postgres
- Embedded goose migrations in `internal/infrastructure/postgres/migrations/`
- sqlc queries in `internal/infrastructure/postgres/queries/`, generated package committed
- Proto in `api/proto/auth/v1/`

## Docs

| Topic | Path |
| --- | --- |
| Migrations, adoption, rollback | `docs/migrations.md` |
| Platform auth ADR | `handbook/docs/orbit/decisions/011-platform-auth-notifications.md` |
| Password + API tokens | `handbook/docs/orbit/decisions/016-orbit-auth-password-and-api-tokens.md` |
| Dev quickstart | `handbook/docs/orbit/guides/platform-dev-quickstart.md` |
| Go toolchain | `handbook/docs/orbit/architecture/go-toolchain.md` |

## Structure

| Path | Role |
| --- | --- |
| `cmd/auth/` | gRPC server entry, wiring and `migrate` |
| `internal/domain/` | Entities, pinned rules, ports |
| `internal/application/` | OTP, password, token and demo use cases |
| `internal/infrastructure/postgres/` | Store adapter, sqlc queries and embedded goose migrations |
| `internal/infrastructure/grpc/` | gRPC handlers |
| `internal/infrastructure/httpapi/` | Fiber v3 health probes |
| `internal/infrastructure/credentials/` | bcrypt, OTP code and API-secret adapters |
| `internal/infrastructure/jwt/` | RS256 signer adapter |
| `internal/testutil/` | disposable-schema integration-test helper |

## Session pre-flight

1. `rtk o port list` — verify 10100 (gRPC) and 10101 (health) allocations
2. `rtk o status orbit` — check dependency state (`orbit-notifications`, dev Postgres)
3. `rtk git log --oneline -6 origin/main` — inspect recently landed changes

## Definition of done

- [ ] `go test ./...` exits 0
- [ ] `go test ./... -race -count=1` with `TEST_DATABASE_URL` exits 0
- [ ] `go vet ./...` exits 0 and `golangci-lint run` reports 0 issues
- [ ] `./scripts/generate-proto.sh` leaves no unstaged proto drift
- [ ] New migrations include clean rollback statements
- [ ] Docs updated in `handbook/docs/orbit/` if auth contracts or endpoints changed

## Do / don't

- Use `log/slog` — not `log.Printf`
- Notification delivery via `orbit-notifications` gRPC — not direct SMTP
- Migrations are sequential — do not skip numbers
- Never edit `internal/infrastructure/postgres/sqlcgen` by hand
- No commit unless user asks
