#!/usr/bin/env bash
set -euo pipefail

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"

if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running project commands" >&2
  exit 1
fi

: >/tmp/go-lua-vm-cgo-check.txt
while IFS= read -r go_file; do
  if grep -Eq 'import[[:space:]]+"C"|^[[:space:]]*"C"[[:space:]]*$' "${go_file}"; then
    if [[ "${go_file}" != internal/native/* ]]; then
      echo "${go_file}: CGO is only allowed under internal/native/" >>/tmp/go-lua-vm-cgo-check.txt
      continue
    fi
    if ! grep -Eq '^//go:build .*native_modules' "${go_file}"; then
      echo "${go_file}: CGO files must be guarded by the native_modules build tag" >>/tmp/go-lua-vm-cgo-check.txt
    fi
  fi
done < <(git ls-files '*.go')

if [[ -s /tmp/go-lua-vm-cgo-check.txt ]]; then
  cat /tmp/go-lua-vm-cgo-check.txt >&2
  echo "CGO is forbidden outside the internal/native native_modules boundary" >&2
  exit 1
fi

git ls-files --others --exclude-standard | grep -E '\.go$|_test\.go$' >/tmp/go-lua-vm-untracked-go.txt || true
if [[ -s /tmp/go-lua-vm-untracked-go.txt ]]; then
  cat /tmp/go-lua-vm-untracked-go.txt >&2
  echo "untracked Go files must be added before delivery" >&2
  exit 1
fi

CGO_ENABLED=0 go test ./...
./scripts/check-performance-smoke.sh
./scripts/check-performance-baseline.sh
./scripts/check-public-go-api-surface.sh
./scripts/check-public-go-api-fields.sh
./scripts/test-public-go-api.sh

if [[ "${GO_LUA_VM_CHECK_NATIVE_MODULES:-0}" == "1" ]]; then
  CGO_ENABLED=1 go test -tags native_modules ./internal/native ./internal/cli ./cmd/glua
else
  echo "skip native_modules gate: set GO_LUA_VM_CHECK_NATIVE_MODULES=1 to run CGO_ENABLED=1 go test -tags native_modules ./internal/native ./internal/cli ./cmd/glua"
fi
