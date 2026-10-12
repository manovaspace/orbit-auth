#!/usr/bin/env bash
set -euo pipefail

source_tag="${1:-}"
ref_type="${2:-}"
ref_name="${3:-}"

tag="$source_tag"
if [[ -z "$tag" && "$ref_type" == tag ]]; then
  tag="$ref_name"
fi

if [[ -z "$tag" ]]; then
  exit 0
fi

if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'release tag must use stable vX.Y.Z format: %s\n' "$tag" >&2
  exit 1
fi

printf '%s\n' "${tag#v}"
