#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-skips.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native skip reason check"
echo "repo_root=${repo_root}"
echo "host_GOOS=${host_goos}"
echo "host_GOARCH=${host_goarch}"

run_and_require_skip() {
  local label="$1"
  local expected="$2"
  shift 2

  local output_file="${work_dir}/${label//[^A-Za-z0-9_]/_}.log"

  echo
  echo "check ${label}: $*"
  if ! "$@" >"${output_file}" 2>&1; then
    echo "skip check command failed for ${label}" >&2
    cat "${output_file}" >&2
    exit 1
  fi

  if ! grep -F "${expected}" "${output_file}" >/dev/null; then
    echo "skip check missing expected reason for ${label}: ${expected}" >&2
    cat "${output_file}" >&2
    exit 1
  fi

  grep -F "skip:" "${output_file}" | sed 's/^/  /'
}

run_and_require_failure_skip() {
  local label="$1"
  local expected="$2"
  shift 2

  local output_file="${work_dir}/${label//[^A-Za-z0-9_]/_}.log"

  echo
  echo "check ${label}: $*"
  if "$@" >"${output_file}" 2>&1; then
    echo "skip check command unexpectedly succeeded for ${label}" >&2
    cat "${output_file}" >&2
    exit 1
  fi

  if ! grep -F "${expected}" "${output_file}" >/dev/null; then
    echo "skip check missing expected reason for ${label}: ${expected}" >&2
    cat "${output_file}" >&2
    exit 1
  fi

  grep -F "skip:" "${output_file}" | sed 's/^/  /'
}

cross_cc_var="NATIVE_CC_LINUX_$(printf '%s' "${host_goarch}" | tr '[:lower:]-' '[:upper:]_')"
mismatch_goos="linux"
if [[ "${host_goos}" == "linux" ]]; then
  mismatch_goos="darwin"
fi

run_and_require_skip \
  "windows fixture build" \
  "skip: Windows fixture build requires lua53.dll shim or import library, not implemented yet" \
  env TARGET_GOOS=windows "${repo_root}/scripts/build-native-fixtures.sh"

run_and_require_skip \
  "windows cjson build" \
  "skip: Windows lua-cjson build requires lua53.dll shim or import library, not implemented yet" \
  env TARGET_GOOS=windows "${repo_root}/scripts/build-native-cjson.sh"

run_and_require_skip \
  "windows lpeg build" \
  "skip: Windows LPeg build requires lua53.dll shim or import library, not implemented yet" \
  env TARGET_GOOS=windows "${repo_root}/scripts/build-native-lpeg.sh"

run_and_require_skip \
  "windows luasocket build" \
  "skip: Windows LuaSocket build requires lua53.dll shim or import library, not implemented yet" \
  env TARGET_GOOS=windows "${repo_root}/scripts/build-native-luasocket.sh"

run_and_require_skip \
  "windows luasocket runtime acceptance" \
  "skip: Windows LuaSocket runtime acceptance requires lua53.dll shim or import library, not implemented yet" \
  env TARGET_GOOS=windows "${repo_root}/scripts/test-native-luasocket.sh"

run_and_require_skip \
  "windows real module aggregate acceptance" \
  "skip: native real module acceptance requires target platform runtime; Windows requires lua53.dll shim or import library, not implemented yet" \
  env TARGET_GOOS=windows "${repo_root}/scripts/test-native-real-modules.sh"

run_and_require_skip \
  "non windows real module aggregate target mismatch" \
  "skip: native real module acceptance requires running on target platform ${mismatch_goos}/${host_goarch}; host is ${host_goos}/${host_goarch}" \
  env TARGET_GOOS="${mismatch_goos}" "${repo_root}/scripts/test-native-real-modules.sh"

run_and_require_skip \
  "linux missing cross compiler" \
  "skip: C compiler not found for linux/${host_goarch}: __glua_missing_native_cc__" \
  env NATIVE_CROSS_TARGETS="linux/${host_goarch}" "${cross_cc_var}=__glua_missing_native_cc__" "${repo_root}/scripts/check-native-cross-compile.sh"

run_and_require_failure_skip \
  "linux required missing cross compiler" \
  "skip: C compiler not found for linux/${host_goarch}: __glua_missing_native_cc__" \
  env NATIVE_CROSS_REQUIRE_ALL=1 NATIVE_CROSS_TARGETS="linux/${host_goarch}" "${cross_cc_var}=__glua_missing_native_cc__" "${repo_root}/scripts/check-native-cross-compile.sh"

echo
echo "native skip reason check passed"
