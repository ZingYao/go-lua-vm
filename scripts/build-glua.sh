#!/usr/bin/env bash
set -euo pipefail

output_path="${1:-bin/glua}"

mkdir -p "$(dirname "${output_path}")"

go_version="$(go version | awk '{print $3}')"
if [[ "${go_version}" != "go1.26.4" ]]; then
  echo "go version mismatch: expected go1.26.4, got ${go_version}" >&2
  echo "ensure PATH points to go1.26.4 before building glua" >&2
  exit 1
fi

CGO_ENABLED=0 go build -trimpath -o "${output_path}" ./cmd/glua

echo "built ${output_path}"
