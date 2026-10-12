# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
