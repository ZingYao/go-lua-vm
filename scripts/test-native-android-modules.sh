#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="android"
target_goarch="${TARGET_GOARCH:-arm64}"
android_api="${ANDROID_API_LEVEL:-35}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-android-modules/${target_goarch}}"
device_dir="${ANDROID_DEVICE_DIR:-/data/local/tmp/glua-native-modules}"
template_file="${repo_root}/tests/native_modules/fixtures/glua_native_smoke.lua"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
fixture_dir="${build_dir}/fixtures"

if [[ "${target_goarch}" != "arm64" ]]; then
  echo "Android native module smoke currently supports TARGET_GOARCH=arm64, got ${target_goarch}" >&2
  exit 1
fi

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running Android native module smoke" >&2
  exit 1
fi

find_android_cc() {
  local candidate

  if [[ -n "${CC:-}" ]]; then
    echo "${CC}"
    return 0
  fi

  candidate="aarch64-linux-android${android_api}-clang"
  if command -v "${candidate}" >/dev/null 2>&1; then
    echo "${candidate}"
    return 0
  fi

  if [[ -n "${ANDROID_NDK_HOME:-}" ]]; then
    for candidate in "${ANDROID_NDK_HOME}"/toolchains/llvm/prebuilt/*/bin/aarch64-linux-android"${android_api}"-clang; do
      if [[ -x "${candidate}" ]]; then
        echo "${candidate}"
        return 0
      fi
    done
  fi

  return 1
}

android_cc=""
if ! android_cc="$(find_android_cc)"; then
  echo "Android clang not found; install Android NDK and expose aarch64-linux-android${android_api}-clang on PATH or set CC" >&2
  exit 1
fi

if [[ -n "${ADB_SERIAL:-}" ]]; then
  adb_command=(adb -s "${ADB_SERIAL}")
else
  adb_command=(adb)
fi

echo "Android native module CLI smoke"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "ANDROID_API_LEVEL=${android_api}"
echo "CC=${android_cc}"
echo "BUILD_DIR=${build_dir}"
echo "ANDROID_DEVICE_DIR=${device_dir}"

if [[ ! -f "${template_file}" ]]; then
  echo "native fixture Lua template not found: ${template_file}" >&2
  exit 1
fi

mkdir -p "${build_dir}" "${fixture_dir}"

echo "build Android native glua: ${glua_bin}"
GOOS="${target_goos}" GOARCH="${target_goarch}" CGO_ENABLED=1 CC="${android_cc}" \
  go build -trimpath -o "${glua_bin}" ./cmd/glua

echo "build Android native fixture modules"
TARGET_GOOS="${target_goos}" TARGET_GOARCH="${target_goarch}" BUILD_DIR="${fixture_dir}" CGO_ENABLED=1 CC="${android_cc}" \
  "${repo_root}/scripts/build-native-fixtures.sh"

smoke_module="${fixture_dir}/glua_native_smoke.so"
failopen_module="${fixture_dir}/glua_native_failopen.so"
if [[ ! -f "${smoke_module}" || ! -f "${failopen_module}" ]]; then
  echo "Android native fixture outputs missing:" >&2
  echo "  ${smoke_module}" >&2
  echo "  ${failopen_module}" >&2
  exit 1
fi

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-android-native.XXXXXX")"
cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

lua_string_literal() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  printf '"%s"' "${text}"
}

missing_lua_pattern="${device_dir}/missing/?.lua"
cpath_pattern="${device_dir}/?.so"
package_path_literal="$(lua_string_literal "${missing_lua_pattern}")"
package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"

smoke_source="${work_dir}/glua_native_smoke_android.lua"
smoke_template="$(<"${template_file}")"
smoke_template="${smoke_template//@GLUA_NATIVE_PACKAGE_PATH@/${package_path_literal}}"
smoke_template="${smoke_template//@GLUA_NATIVE_PACKAGE_CPATH@/${package_cpath_literal}}"
printf '%s\n' "${smoke_template}" >"${smoke_source}"

failopen_source="${work_dir}/glua_native_failopen_android.lua"
cat >"${failopen_source}" <<EOF
package.path = ${package_path_literal}
package.cpath = ${package_cpath_literal}

local ok, message = pcall(require, "glua_native_failopen")
assert(ok == false, "require unexpectedly succeeded")
assert(string.find(message, "native open failure", 1, true), message)
assert(package.loaded["glua_native_failopen"] == nil)
EOF

"${adb_command[@]}" shell "rm -rf $(printf '%q' "${device_dir}"); mkdir -p $(printf '%q' "${device_dir}")"
"${adb_command[@]}" push "${glua_bin}" "${smoke_module}" "${failopen_module}" "${smoke_source}" "${failopen_source}" "${device_dir}/" >/dev/null
"${adb_command[@]}" shell "chmod 755 $(printf '%q' "${device_dir}")/*"

echo "device info:"
"${adb_command[@]}" shell "getprop ro.product.model; getprop ro.product.cpu.abi; getprop ro.build.version.release; getprop ro.build.version.sdk; uname -a"

echo "run Android native smoke require"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && ./glua-native glua_native_smoke_android.lua"

echo "run Android native failopen require"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && ./glua-native glua_native_failopen_android.lua"

echo "Android native module CLI smoke passed"
