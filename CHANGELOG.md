# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.13.0](https://github.com/manovaspace/orbit-auth/compare/v0.12.0...v0.13.0) (2026-11-28)


### Features

* **auth:** confirm phone invitations with a one-time code ([7eced23](https://github.com/manovaspace/orbit-auth/commit/7eced23f755feebe5ab4033238ae889f7aff247a))
* **auth:** confirm phone invitations with a one-time code ([0a1eb39](https://github.com/manovaspace/orbit-auth/commit/0a1eb39c6ad6dc70eb712f4e1e27dadcd0c1a239))
* **auth:** refresh-token families with reuse detection ([1da8d22](https://github.com/manovaspace/orbit-auth/commit/1da8d225de0ab7e2181b7141c7fbdb9f62338295))
* **auth:** refresh-token families with reuse detection ([bed200d](https://github.com/manovaspace/orbit-auth/commit/bed200d03aa5a23935c4bc4ad5305470d6ee3e59)), closes [#122](https://github.com/manovaspace/orbit-auth/issues/122)
* **db:** move auth tables into identity, tenancy and platform schemas ([5b6dd33](https://github.com/manovaspace/orbit-auth/commit/5b6dd337de63c0645aae9af7b1b05c3df4363a67))
* **db:** move auth tables into identity, tenancy and platform schemas ([91a3673](https://github.com/manovaspace/orbit-auth/commit/91a367375bd6109059be641ddfdb7bc48c1270ee))


### Bug Fixes

* **auth:** count OTP attempts atomically and bind API token RPCs to the caller ([dd5f7d4](https://github.com/manovaspace/orbit-auth/commit/dd5f7d485497aeca6b34b2d54ca2af537bc65d93))
* **auth:** count OTP attempts atomically and bind API token RPCs to the caller ([bad34e8](https://github.com/manovaspace/orbit-auth/commit/bad34e817300848269eff749a326ccd320eb350f))
* **auth:** OTP challenges carry a purpose ([c9107f1](https://github.com/manovaspace/orbit-auth/commit/c9107f17002fc41d7e2a9e0de3ca5a3a13da5e8a))
* **auth:** OTP challenges carry a purpose ([02d56b6](https://github.com/manovaspace/orbit-auth/commit/02d56b68b8ebe2af940be4b0bd17444c11c93cbd)), closes [#128](https://github.com/manovaspace/orbit-auth/issues/128)
* **db:** identity constraints, unique refresh-token hash and delete rules ([1efd418](https://github.com/manovaspace/orbit-auth/commit/1efd41827911b690a1c025c98e441b09827ff9e1))
* **db:** identity constraints, unique refresh-token hash and delete rules ([cc5d40e](https://github.com/manovaspace/orbit-auth/commit/cc5d40e88133c6d566022096e4e93fd0635663e6))
* **flags:** retry the first flags fetch quickly after startup ([cc0e799](https://github.com/manovaspace/orbit-auth/commit/cc0e799ccc4cd755be378a9b6782278b76c3008b))
* **flags:** retry the first flags fetch quickly after startup ([b060cf2](https://github.com/manovaspace/orbit-auth/commit/b060cf25cb51831673e917f6322ac856a1041d28))
* **migrations:** keep goose version table in public after the schema move ([#133](https://github.com/manovaspace/orbit-auth/issues/133)) ([1e4678e](https://github.com/manovaspace/orbit-auth/commit/1e4678e1d5f49b3734502a24bd6611fedf259632))
* **slug:** reserve console for the staff console host ([e674361](https://github.com/manovaspace/orbit-auth/commit/e674361309de6862cc18ba6b0d25c1022412f4d5))
* **slug:** reserve console for the staff console host ([bfdc043](https://github.com/manovaspace/orbit-auth/commit/bfdc043bcba815b42958a425e70cc828d27195dd))

## [0.12.0](https://github.com/manovaspace/orbit-auth/compare/v0.11.0...v0.12.0) (2026-11-22)


### Features

* accept email invitations with sign-up and link them to the product ([#117](https://github.com/manovaspace/orbit-auth/issues/117)) ([846dc14](https://github.com/manovaspace/orbit-auth/commit/846dc14dd07dd462974b49ad86895c315b5543df))
* accept invitations with Google and manage sign-in methods ([#121](https://github.com/manovaspace/orbit-auth/issues/121)) ([bd60f3e](https://github.com/manovaspace/orbit-auth/commit/bd60f3e25507f73d4ea7a17c8fddf66db04b3d04))
* account and membership states with an audit trail ([#120](https://github.com/manovaspace/orbit-auth/issues/120)) ([ee0c8d6](https://github.com/manovaspace/orbit-auth/commit/ee0c8d66ae6d02180a40bc0ff7183a8230eb97a8))
* **token:** accept a plain-HTTP JWKS from a container host name ([#119](https://github.com/manovaspace/orbit-auth/issues/119)) ([345cc44](https://github.com/manovaspace/orbit-auth/commit/345cc44e2e0f8369da809d860047dc9bc6574565))

## [0.11.0](https://github.com/manovaspace/orbit-auth/compare/v0.10.3...v0.11.0) (2026-11-22)


### Features

* sign in with a username or a local mobile number ([#114](https://github.com/manovaspace/orbit-auth/issues/114)) ([9018229](https://github.com/manovaspace/orbit-auth/commit/9018229b3686ccafc1d0a393a20ca5a89f4ae1d8))
* sign in with Google ([#116](https://github.com/manovaspace/orbit-auth/issues/116)) ([3a9c872](https://github.com/manovaspace/orbit-auth/commit/3a9c8726fb598dc6ebbdcfde7fae9b946f395128))

## [0.10.3](https://github.com/manovaspace/orbit-auth/compare/v0.10.2...v0.10.3) (2026-11-21)


### Bug Fixes

* **deps:** move to orbit-kit from the retired Orbit libraries ([#109](https://github.com/manovaspace/orbit-auth/issues/109)) ([4932e6c](https://github.com/manovaspace/orbit-auth/commit/4932e6c427cabc0fee353bf07e608dbff22d51ea))

## [0.10.2](https://github.com/manovaspace/orbit-auth/compare/v0.10.1...v0.10.2) (2026-11-20)


### Bug Fixes

* **deps:** upgrade Go to 1.27.4 and patch vulnerable modules ([#103](https://github.com/manovaspace/orbit-auth/issues/103)) ([f7d9669](https://github.com/manovaspace/orbit-auth/commit/f7d9669efb9da205358b02644ffd2837653c3e08))

## [0.10.1](https://github.com/manovaspace/orbit-auth/compare/v0.10.0...v0.10.1) (2026-11-09)


### Bug Fixes

* **grpc:** read the staff reason as UTF-8 binary metadata ([2bfb384](https://github.com/manovaspace/orbit-auth/commit/2bfb384af17e8eecf68530b2e75007cb1a44baae))
* **grpc:** read the staff reason as UTF-8 binary metadata ([e6d4489](https://github.com/manovaspace/orbit-auth/commit/e6d4489f6aec9528e95dee021c11cdfaf09fe03b))

## [0.10.0](https://github.com/manovaspace/orbit-auth/compare/v0.9.0...v0.10.0) (2026-11-09)


### Features

* **orgs:** staff organization administration ([58fe148](https://github.com/manovaspace/orbit-auth/commit/58fe148ae79245caff1a5e4e64e65c0f21d7bb73))
* **orgs:** staff organization administration ([9a5f017](https://github.com/manovaspace/orbit-auth/commit/9a5f017e2d2a658b8f83fd3ae2c87cf3001f7ac5)), closes [#94](https://github.com/manovaspace/orbit-auth/issues/94)

## [0.9.0](https://github.com/manovaspace/orbit-auth/compare/v0.8.1...v0.9.0) (2026-11-08)


### Features

* **invites:** invite members by phone number with an invited tier ([23ecd5f](https://github.com/manovaspace/orbit-auth/commit/23ecd5f001acda6b3699d26a3c84b64fcf22c976))
* **invites:** invite members by phone number with an invited tier ([5b23a88](https://github.com/manovaspace/orbit-auth/commit/5b23a88eadded186e0732cc4cd0056053c39169c))
* **invites:** phone invite RPCs, invite preview and sign-up on accept ([5f37882](https://github.com/manovaspace/orbit-auth/commit/5f37882a29136bbcf1a0be7d264bbfd01dd79d9a))
* **invites:** phone invite RPCs, invite preview and sign-up on accept ([63f4ef6](https://github.com/manovaspace/orbit-auth/commit/63f4ef6db9480677950019082ec9a9ac27ba48bd)), closes [#93](https://github.com/manovaspace/orbit-auth/issues/93)

## [0.8.1](https://github.com/manovaspace/orbit-auth/compare/v0.8.0...v0.8.1) (2026-11-08)


### Bug Fixes

* **auth:** redeem refresh tokens once and add RevokeRefreshToken ([0692d08](https://github.com/manovaspace/orbit-auth/commit/0692d08029cb01a6f5b023988152b3d9344bdd9e))
* **auth:** redeem refresh tokens once and add RevokeRefreshToken ([000fd01](https://github.com/manovaspace/orbit-auth/commit/000fd01ea5f266f4f1550db45926eb9ef7a42f5e)), closes [#88](https://github.com/manovaspace/orbit-auth/issues/88)

## [0.8.0](https://github.com/manovaspace/orbit-auth/compare/v0.7.1...v0.8.0) (2026-11-07)


### Features

* **orgs:** organization slugs and the public tenant lookup ([f037ba1](https://github.com/manovaspace/orbit-auth/commit/f037ba1565717c4c43050b04c43445516e745e8e))

## [0.7.1](https://github.com/manovaspace/orbit-auth/compare/v0.7.0...v0.7.1) (2026-11-07)


### Bug Fixes

* **migrations:** satisfy squawk in 010 ([d74dd71](https://github.com/manovaspace/orbit-auth/commit/d74dd71719233aa1e56354867fc4208c16513599))
* **orgs:** recheck membership authority under the organization lock ([361c417](https://github.com/manovaspace/orbit-auth/commit/361c417589e6517d66aec5b642d4a44ec22ac702))
* **orgs:** require org-admin authority for membership writes ([90f9026](https://github.com/manovaspace/orbit-auth/commit/90f90268ce5c5fe0ad8371b92c8302f361e25ff6))
* **orgs:** require org-admin authority for membership writes ([9a3c6f0](https://github.com/manovaspace/orbit-auth/commit/9a3c6f01efd19e957e3777fdd7803b9220dfd1c3)), closes [#82](https://github.com/manovaspace/orbit-auth/issues/82)

## [0.7.0](https://github.com/manovaspace/orbit-auth/compare/v0.6.1...v0.7.0) (2026-11-05)


### Features

* **ports:** move orbit-auth into the 50-port orbit block ([a2fc78b](https://github.com/manovaspace/orbit-auth/commit/a2fc78ba400764e7bf1aec01cc7dadcbd7148942))
* **ports:** move orbit-auth into the 50-port orbit block ([5060dcd](https://github.com/manovaspace/orbit-auth/commit/5060dcde931b12ede54ad88dace107173c87f690))

## [0.6.1](https://github.com/manovaspace/orbit-auth/compare/v0.6.0...v0.6.1) (2026-11-05)


### Bug Fixes

* regenerate the stale sqlc models for feature flags ([153c567](https://github.com/manovaspace/orbit-auth/commit/153c567cb2b1d4a04927821ecf9768dafb0f42d4))

## [0.6.0](https://github.com/manovaspace/orbit-auth/compare/v0.5.1...v0.6.0) (2026-11-05)


### Features

* **dev:** use the orbit-block dev tooling ports ([d4189be](https://github.com/manovaspace/orbit-auth/commit/d4189bebe2e03ea2b3551034b9cd561c672882b0))
* **dev:** use the orbit-block dev tooling ports ([4238f3b](https://github.com/manovaspace/orbit-auth/commit/4238f3bd24602b419e0ca38232df8948981d3ab0))

## [0.5.1](https://github.com/manovaspace/orbit-auth/compare/v0.5.0...v0.5.1) (2026-11-03)


### Bug Fixes

* **build:** report the real service version ([#67](https://github.com/manovaspace/orbit-auth/issues/67)) ([69d6471](https://github.com/manovaspace/orbit-auth/commit/69d64713815bc3b605398a6b73d356f68e616b40))

## [0.5.0](https://github.com/manovaspace/orbit-auth/compare/v0.4.0...v0.5.0) (2026-11-03)


### Features

* **flags:** add the platform flag store and public read API ([#64](https://github.com/manovaspace/orbit-auth/issues/64)) ([ea6721c](https://github.com/manovaspace/orbit-auth/commit/ea6721cb9183d1dca54de8e9a117029e5f54d47d))
* **flags:** read feature flags from the platform endpoint ([#66](https://github.com/manovaspace/orbit-auth/issues/66)) ([e68286e](https://github.com/manovaspace/orbit-auth/commit/e68286e0b191d187168429542b737d9b33145442))

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
