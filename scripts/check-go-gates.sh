#!/usr/bin/env bash
set -euo pipefail

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"

if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH points to /Users/zing/sdk/go/go1.26.4/bin before running project commands" >&2
  exit 1
fi

if command -v rg >/dev/null 2>&1; then
  rg -n 'import\s+"C"|^\s*"C"\s*$' --glob '*.go' . >/tmp/go-lua-vm-cgo-check.txt || true
else
  git grep -nE 'import[[:space:]]+"C"|^[[:space:]]*"C"[[:space:]]*$' -- '*.go' >/tmp/go-lua-vm-cgo-check.txt || true
fi

if [[ -s /tmp/go-lua-vm-cgo-check.txt ]]; then
  cat /tmp/go-lua-vm-cgo-check.txt >&2
  echo "CGO is forbidden: remove import \"C\"" >&2
  exit 1
fi

git ls-files --others --exclude-standard | grep -E '\.go$|_test\.go$' >/tmp/go-lua-vm-untracked-go.txt || true
if [[ -s /tmp/go-lua-vm-untracked-go.txt ]]; then
  cat /tmp/go-lua-vm-untracked-go.txt >&2
  echo "untracked Go files must be added before delivery" >&2
  exit 1
fi

CGO_ENABLED=0 go test ./...
