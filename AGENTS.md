# orbit-auth

Tier 2 product user authentication — OTP login, password login, JWT sessions, API tokens. Staff SSO remains Authelia.

## Commands

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://orbit:orbit@localhost:10332/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10110
export GRPC_PORT=10100
export HEALTH_PORT=10101
go run ./cmd/auth
go test ./...
./scripts/generate-proto.sh   # after proto changes
```

Ports: gRPC on **10100** (`GRPC_PORT`), HTTP health on **10101** (`HEALTH_PORT`, `/healthz`, `/readyz`).
Requires `orbit-notifications` on **10110** and dev Postgres.

Start with orbit-infra: `orbit/orbit-infra/scripts/start-tier2.sh`

## Non-negotiables

1. **Tenant isolation is sacred** — all user queries and data operations must be scoped by tenant/user ID. Zero cross-tenant leakage.
2. **JWT verification before mutations** — never trust unauthenticated claims or bypass token validation.
3. **Notifications strictly via gRPC** — call `orbit-notifications` on port 10110; never implement direct SMTP or raw mail calls in orbit-auth.
4. **`log/slog` only** — `log.Printf` and `fmt.Println` logging are strictly forbidden.
5. **Sequential migrations** — migrations in `migrations/` must be numbered sequentially without gaps. Never alter previously applied migrations.
6. **Zero credential leakage** — never log passwords, plaintext OTP codes, or JWT signing keys in stdout/stderr.

## Engineering gotchas

- **gRPC interceptor order:** Panic recovery must wrap the outermost boundary; auth token validation must precede tenant context extraction in the interceptor chain.
- **Proto code regeneration:** When editing `api/proto/auth/v1/`, run `./scripts/generate-proto.sh`. Never manually modify generated `*.pb.go` files.
- **Unleash flag fallback:** When Unleash flags (`manova.auth.email_otp`, `manova.auth.mobile_otp`) are toggled off or unreachable, handlers return typed errors (`ErrEmailOTPDisabled`). Unit tests must mock the flag provider.
- **Demo mode guard:** `DEMO_MODE=true` is permitted only when `DEPLOYMENT_ENVIRONMENT=dev`. The server must fail closed if demo mode is enabled in production.


## Feature Flags & Demo Mode

- **Unleash flags:** `manova.auth.email_otp` and `manova.auth.mobile_otp` (`UNLEASH_URL`, `UNLEASH_API_TOKEN`, `UNLEASH_APP_NAME`). When disabled, `RequestOTP` returns `ErrEmailOTPDisabled` or `ErrMobileOTPDisabled`.
- **Demo seeding:** `DEMO_MODE=true` (dev only) and `AUTH_DEMO_USERS` JSON array seeds test user accounts into Postgres on startup.

## Stack

- Go 1.26, gRPC + Postgres
- Migrations in `migrations/`
- Proto in `api/proto/auth/v1/`

## Docs

| Topic | Path |
| --- | --- |
| Platform auth ADR | `handbook/docs/orbit/decisions/011-platform-auth-notifications.md` |
| Password + API tokens | `handbook/docs/orbit/decisions/016-orbit-auth-password-and-api-tokens.md` |
| Dev quickstart | `handbook/docs/orbit/guides/platform-dev-quickstart.md` |
| Go toolchain | `handbook/docs/orbit/architecture/go-toolchain.md` |

## Structure

| Path | Role |
| --- | --- |
| `cmd/auth/` | gRPC server entry |
| `internal/domain/` | Ports, entities |
| `internal/application/` | OTP, password, token use cases |
| `internal/infrastructure/postgres/` | Store adapter |
| `internal/infrastructure/grpc/` | gRPC handlers |

## Session pre-flight

1. `rtk o port list` — verify 10100 (gRPC) and 10101 (health) allocations
2. `rtk o status orbit` — check dependency state (`orbit-notifications`, dev Postgres)
3. `rtk git log --oneline -6 origin/main` — inspect recently landed changes

## Definition of done

- [ ] `go test ./...` exits 0
- [ ] `go vet ./...` exits 0
- [ ] `./scripts/generate-proto.sh` leaves no unstaged proto drift
- [ ] New migrations include clean rollback statements
- [ ] Docs updated in `handbook/docs/orbit/` if auth contracts or endpoints changed

## Do / don't


- Use `log/slog` — not `log.Printf`
- Notification delivery via `orbit-notifications` gRPC — not direct SMTP
- Migrations are sequential — do not skip numbers
- No commit unless user asks
