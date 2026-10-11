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
export JWT_PRIVATE_KEY_FILE=./secrets/jwt-private.pem
export JWT_ACTIVE_KEY_ID=orbit-dev-1
export JWT_PUBLIC_KEYS_DIR=./secrets/jwt-public-keys
go run ./cmd/auth
```

Orbit Auth signs access tokens with RS256, issuer `orbit-auth`, and the
configured `JWT_ACTIVE_KEY_ID` (`kid`). Use an RSA key of at least 2048 bits.
Keep the private PEM readable only by the auth service. The gateway publishes
the matching public PEMs as JWKS; consumers can verify tokens but cannot mint
them. See [the token contract](./token/README.md) for key rotation.

Default gRPC listen: `localhost:10100` (`GRPC_PORT`) · Health: `localhost:10101` (`HEALTH_PORT`).

## Features & Configuration

- **Feature Flags:** Uses Unleash (`UNLEASH_URL`, `UNLEASH_API_TOKEN`, `UNLEASH_APP_NAME`) to evaluate `manova.auth.email_otp` and `manova.auth.mobile_otp`.
- **Demo Mode:** When `DEMO_MODE=true` in dev, pre-seeds accounts from `AUTH_DEMO_USERS` JSON array.
- **Internal Auth:** Secured across Orbit gRPC mesh via `ORBIT_INTERNAL_TOKEN`.

## Reusable authorization policy

The public `authorization` package evaluates product-owned role/permission
policies without importing gRPC, Fiber, or persistence packages. Orbit Auth
owns identity and authentication; each product owns role assignments and its
product-specific permissions. See `authorization/README.md` and its GoDoc
before integrating it.

## Reusable access token contract

The public `token` package owns RS256 access-token signing, key loading, and
issuer/expiry/subject validation for Orbit Auth and its consumers. It does not
own product roles or refresh-token persistence. See `token/README.md` before
integrating another verifier.

## Documentation

- Contributing: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Security: [SECURITY.md](./SECURITY.md)
- Platform docs: https://manovaspace.github.io/docs/

## License

MIT — see [LICENSE](./LICENSE).
