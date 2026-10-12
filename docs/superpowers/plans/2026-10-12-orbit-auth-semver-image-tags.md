# Orbit Auth Semver Image Tags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure release builds publish `vX.Y.Z` and `X.Y` GHCR tags, and keep workflow-dispatch builds from inventing release tags.

**Architecture:** Resolve the release tag from the explicit dispatch input or the tag-triggered GitHub ref in a small testable shell helper. Feed its normalized version explicitly to docker/metadata-action and add a focused shell regression test.

**Tech Stack:** Bash, GitHub Actions, docker/metadata-action.

**Spec:** The publish workflow’s existing contract: stable `vX.Y.Z` tags publish semver, `latest`, and `edge`; workflow dispatch without a source tag publishes `edge` and a SHA tag.

## Global Constraints

- Never rewrite or repush existing release tags.
- Workflow dispatch must not infer a semver tag from branch refs.
- Only exact stable `vX.Y.Z` values are release inputs.

## Review Focus

- Tag event with another branch pointing at the same commit must still produce version tags; regression test uses tag ref explicitly.
- Workflow dispatch on a normal branch must not produce semver tags; regression test covers empty source tag.
- Malformed source tags must fail closed; regression test covers invalid stable-tag input.

---

### Task 1: Resolve release tags deterministically

**Files:**
- Create: `scripts/resolve-image-tag.sh`
- Modify: `.github/workflows/publish-image.yml`
- Modify: `.github/workflows/ci.yml`
- Test: `scripts/test-resolve-image-tag.sh`
- Create: `docs/superpowers/plans/2026-10-12-orbit-auth-semver-image-tags.md`

**Interfaces:**
- Consumes: source tag, GitHub ref type, GitHub ref name as command arguments.
- Produces: normalized `MAJOR.MINOR.PATCH` on stdout, empty output when no release tag applies, nonzero exit for malformed nonempty tag.

- [ ] Write shell regression tests for tag event, explicit dispatch tag, branch dispatch, and malformed tag.
- [ ] Run the tests and verify they fail because the resolver does not exist.
- [ ] Implement the resolver and wire its output as an explicit semver `value` to metadata-action.
- [ ] Run the focused regression test and validate both workflow files.
- [ ] After merge, run `workflow_dispatch` from `main` with `source_tag=v0.1.1`; verify the successful run publishes GHCR tags `0.1.1` and `0.1` without changing the Git tag.
- [ ] Review the workflow diff, commit, push a branch, and open a PR.
