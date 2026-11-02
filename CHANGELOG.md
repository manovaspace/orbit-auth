# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.0](https://github.com/manovaspace/orbit-auth/compare/v0.3.0...v0.4.0) (2026-11-02)


### Features

* **auth:** self-serve sign-up with email verification behind AUTH_SIGNUP ([#57](https://github.com/manovaspace/orbit-auth/issues/57)) ([673b32c](https://github.com/manovaspace/orbit-auth/commit/673b32c2fd36f1ababbfd40501cb71d800aa3824))
* **events:** publish auth lifecycle events through the Postgres outbox ([#58](https://github.com/manovaspace/orbit-auth/issues/58)) ([c2154e6](https://github.com/manovaspace/orbit-auth/commit/c2154e613214498189e0c7e8362f2ace14e72387))
* **orgs:** invite members by email ([#52](https://github.com/manovaspace/orbit-auth/issues/52)) ([cbed92d](https://github.com/manovaspace/orbit-auth/commit/cbed92d8c13c5ea1c7c53c23f1c7662611ec4b21))


### Bug Fixes

* **events:** read the release tier's TEST_NATS_URL in the NATS test ([#59](https://github.com/manovaspace/orbit-auth/issues/59)) ([9a7be75](https://github.com/manovaspace/orbit-auth/commit/9a7be753119a93fd7cff98299514b94ab3a0ce20))

## [0.3.0](https://github.com/manovaspace/orbit-auth/compare/v0.2.1...v0.3.0) (2026-11-02)


### Features

* **auth:** carry the active organization in access tokens ([#50](https://github.com/manovaspace/orbit-auth/issues/50)) ([bf77079](https://github.com/manovaspace/orbit-auth/commit/bf770794061d30806425d33252d9abd56a8669f7))
* **authorization:** scoped RBAC v2 behind an Authorizer port ([#48](https://github.com/manovaspace/orbit-auth/issues/48)) ([1143b94](https://github.com/manovaspace/orbit-auth/commit/1143b94c145d7f14c92f9a59be6c61a29229ea44))
* **orgs:** add organizations and memberships ([#46](https://github.com/manovaspace/orbit-auth/issues/46)) ([2dec468](https://github.com/manovaspace/orbit-auth/commit/2dec4683911ec92439de25cfd5d8122c2c33fde8))
* **orgs:** expose OrgService over gRPC ([#49](https://github.com/manovaspace/orbit-auth/issues/49)) ([e4859be](https://github.com/manovaspace/orbit-auth/commit/e4859bee63c96cdf85c60e0cddab2a26b75ec0c2))
* **tenancy:** carry the active organization through requests and transactions ([#51](https://github.com/manovaspace/orbit-auth/issues/51)) ([5e42901](https://github.com/manovaspace/orbit-auth/commit/5e42901794d18e6b159a52dae71d37b1e046a8c4))

## [0.2.1](https://github.com/manovaspace/orbit-auth/compare/v0.2.0...v0.2.1) (2026-11-02)


### Bug Fixes

* **featureflags:** never block startup on Unleash ([#38](https://github.com/manovaspace/orbit-auth/issues/38)) ([e036229](https://github.com/manovaspace/orbit-auth/commit/e0362291667eed93c396e3f3b24b1a57370ade83))

## [0.2.0](https://github.com/manovaspace/orbit-auth/compare/v0.1.1...v0.2.0) (2026-11-02)


### Features

* accept mobile identifier for password login ([#28](https://github.com/manovaspace/orbit-auth/issues/28)) ([4b066c7](https://github.com/manovaspace/orbit-auth/commit/4b066c77dacfc7d107b53c75b3627fa4b6a67079))


### Bug Fixes

* bump Go to 1.26.10 for crypto/x509 advisory ([#29](https://github.com/manovaspace/orbit-auth/issues/29)) ([8156ec0](https://github.com/manovaspace/orbit-auth/commit/8156ec02a339ad933e666de91a47b3a8e38aab99))
* publish Orbit Auth semver image tags ([#24](https://github.com/manovaspace/orbit-auth/issues/24)) ([8929ae0](https://github.com/manovaspace/orbit-auth/commit/8929ae08392daa1627b8736df4d113c3f122cdfd))
* require DATABASE_URL instead of a stale dev default ([#32](https://github.com/manovaspace/orbit-auth/issues/32)) ([12d92a4](https://github.com/manovaspace/orbit-auth/commit/12d92a4ecc72bd731f292e6a1c2ea7c6b19326a7))


### Code Refactoring

* adopt the Go service standard ([#35](https://github.com/manovaspace/orbit-auth/issues/35)) ([426570d](https://github.com/manovaspace/orbit-auth/commit/426570db18ee20d4c072a833090c2f895d4c86a1))

## [Unreleased]

## [0.1.0] - 2026-10-11

### Added

- Password login accepts an E.164 mobile number as well as an email through the new `LoginWithPasswordRequest.identifier` field. The `email` field is deprecated but still honoured.
- Demo users (`AUTH_DEMO_USERS`) may carry a `mobile`; an email is no longer required when a mobile is present.

## [0.1.1] - 2026-10-12

### Fixed

- Pin Orbit Notifications and Observability to immutable source revisions so standalone module and image builds resolve the same dependencies.
- Download Go modules before copying application sources in the container build.
- Remove the CI checkout step for sibling repositories no longer used by local module replacements.

## [0.1.0] - 2026-10-11

### Added

- Initial public MIT release.
- Reusable RS256 access-token signing and verification with public JWKS support.
- Framework-neutral role and permission policy evaluation for product APIs.
