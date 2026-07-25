# orbit-auth

Tier 2 product user authentication — OTP login, password login, JWT sessions, API tokens. Staff SSO remains Authelia.

## Commands

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://orbit:orbit@localhost:10332/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10110
go run ./cmd/auth
go test ./...
./scripts/generate-proto.sh   # after proto changes
```

gRPC: **10100**. Requires `orbit-notifications` on **10110** and dev Postgres.

Start with orbit-infra: `orbit/orbit-infra/scripts/start-tier2.sh`

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

## Do / don't

- Use `log/slog` — not `log.Printf`
- Notification delivery via `orbit-notifications` gRPC — not direct SMTP
- Migrations are sequential — do not skip numbers
- No commit unless user asks
