#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export PATH="${HOME}/go/bin:${PATH}"
protoc \
  -I "${ROOT}/api/proto" \
  --go_out="${ROOT}/api" --go_opt=paths=source_relative \
  --go-grpc_out="${ROOT}/api" --go-grpc_opt=paths=source_relative \
  "${ROOT}/api/proto/auth/v1/auth.proto"
