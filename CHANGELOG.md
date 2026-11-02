# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
