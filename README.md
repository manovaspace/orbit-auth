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
go run ./cmd/auth
```

Default gRPC listen: `localhost:10100`.

## Documentation

- Contributing: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Security: [SECURITY.md](./SECURITY.md)
- Platform docs: https://manovaspace.github.io/docs/

## License

MIT — see [LICENSE](./LICENSE).
