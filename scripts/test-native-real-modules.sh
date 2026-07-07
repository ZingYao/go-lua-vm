#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"

echo "native real module acceptance suite"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native real module acceptance" >&2
  exit 1
fi

run_acceptance() {
  local label="$1"
  shift

  echo
  echo "run ${label}: $*"
  CGO_ENABLED=1 TARGET_GOOS="${target_goos}" TARGET_GOARCH="${target_goarch}" "$@"
}

run_acceptance "fixture loader smoke" "${repo_root}/scripts/test-native-modules.sh"
run_acceptance "lua-cjson runtime acceptance" "${repo_root}/scripts/test-native-cjson.sh"
run_acceptance "LPeg runtime acceptance" "${repo_root}/scripts/test-native-lpeg.sh"
run_acceptance "LuaSocket runtime acceptance" "${repo_root}/scripts/test-native-luasocket.sh"

echo
echo "native real module acceptance suite passed"
