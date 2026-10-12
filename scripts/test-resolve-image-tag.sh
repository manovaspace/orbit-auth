#!/usr/bin/env bash
set -euo pipefail

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
resolver="$script_dir/resolve-image-tag.sh"
workflow="$script_dir/../.github/workflows/publish-image.yml"

assert_output() {
  local expected="$1"
  shift
  local actual
  actual="$("$resolver" "$@")"
  if [[ "$actual" != "$expected" ]]; then
    printf 'expected <%s>, got <%s>\n' "$expected" "$actual" >&2
    exit 1
  fi
}

assert_output 0.1.1 '' tag v0.1.1
assert_output 0.1.1 v0.1.1 branch main
assert_output '' '' branch feature/v0.1.1

if "$resolver" v0.1.1-rc.1 branch main >/dev/null 2>&1; then
  echo 'expected prerelease source tag to be rejected' >&2
  exit 1
fi

semver_value_count="$(grep -Fc 'value=${{ steps.release.outputs.version }}' "$workflow" || true)"
if [[ "$semver_value_count" -ne 2 ]]; then
  echo 'both semver metadata tags must use the resolved release version' >&2
  exit 1
fi

if grep -Eq '^[[:space:]]*context: git[[:space:]]*$' "$workflow"; then
  echo 'semver metadata must not infer versions from ambiguous Git refs' >&2
  exit 1
fi

echo 'image tag resolution checks passed'
