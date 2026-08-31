# orbit-auth

[![CI](https://github.com/manovaspace/orbit-auth/actions/workflows/ci.yml/badge.svg)](https://github.com/manovaspace/orbit-auth/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

Tier 2 product user authentication — OTP login, password login, JWT sessions, and API tokens.

Part of the [Manova / Orbit](https://github.com/manovaspace) open toolkit.

## Quick start (dev)

Requires Postgres and the [orbit-notifications](https://github.com/manovaspace/orbit-notifications) gRPC service.

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://orbit:orbit@localhost:10332/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10110
export GRPC_PORT=10100
export HEALTH_PORT=10101
go run ./cmd/auth
```

Default gRPC listen: `localhost:10100` (`GRPC_PORT`) · Health: `localhost:10101` (`HEALTH_PORT`).

## Features & Configuration

- **Feature Flags:** Uses Unleash (`UNLEASH_URL`, `UNLEASH_API_TOKEN`, `UNLEASH_APP_NAME`) to evaluate `manova.auth.email_otp` and `manova.auth.mobile_otp`.
- **Demo Mode:** When `DEMO_MODE=true` in dev, pre-seeds accounts from `AUTH_DEMO_USERS` JSON array.
- **Internal Auth:** Secured across Orbit gRPC mesh via `ORBIT_INTERNAL_TOKEN`.

## Documentation

- Contributing: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Security: [SECURITY.md](./SECURITY.md)
- Platform docs: https://manovaspace.github.io/docs/

## License

MIT — see [LICENSE](./LICENSE).
