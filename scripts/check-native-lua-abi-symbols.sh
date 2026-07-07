#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_root="${BUILD_ROOT:-${repo_root}/build/native-abi/${target_goos}-${target_goarch}}"
glua_bin="${GLUA_BIN:-${build_root}/glua-native}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-abi.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native Lua ABI symbol coverage check"
echo "repo_root=${repo_root}"
echo "host_GOOS=${host_goos}"
echo "host_GOARCH=${host_goarch}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "BUILD_ROOT=${build_root}"
echo "GLUA_BIN=${glua_bin}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native ABI symbol checks" >&2
  exit 1
fi

if [[ "${target_goos}" == "windows" ]]; then
  echo "skip: Windows Lua ABI symbol coverage requires lua53.dll shim or import library, not implemented yet" >&2
  exit 0
fi

if [[ "${target_goos}/${target_goarch}" != "${host_goos}/${host_goarch}" ]]; then
  echo "skip: native Lua ABI symbol coverage requires running on target platform ${target_goos}/${target_goarch}; host is ${host_goos}/${host_goarch}" >&2
  exit 0
fi

case "${target_goos}" in
  darwin | linux)
    ;;
  *)
    echo "skip: unsupported native Lua ABI symbol target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

if ! command -v nm >/dev/null 2>&1; then
  echo "nm is required for native Lua ABI symbol coverage" >&2
  exit 1
fi

normalize_lua_abi_symbols() {
  awk '{
    for (fieldIndex = 1; fieldIndex <= NF; fieldIndex++) {
      symbol = $fieldIndex
      sub(/^_/, "", symbol)
      if (symbol ~ /^lua(L)?_[A-Za-z0-9_]+$/) {
        print symbol
      }
    }
  }' | sort -u
}

list_binary_lua_exports() {
  local binary_path="$1"
  local output

  output="$(nm -g "${binary_path}" 2>/dev/null || true)"
  if [[ -z "${output}" ]]; then
    output="$(nm -D -g "${binary_path}" 2>/dev/null || true)"
  fi
  printf '%s\n' "${output}" | normalize_lua_abi_symbols
}

list_module_lua_undefined() {
  local module_path="$1"
  nm -u "${module_path}" 2>/dev/null | normalize_lua_abi_symbols
}

source_declarations_file="${work_dir}/source_declarations.txt"
binary_exports_file="${work_dir}/binary_exports.txt"
required_symbols_file="${work_dir}/required_symbols.txt"
missing_binary_file="${work_dir}/missing_binary.txt"
missing_source_file="${work_dir}/missing_source.txt"
modules_file="${work_dir}/modules.txt"

{
  rg -o '^//export lua(L)?_[A-Za-z0-9_]+' "${repo_root}/internal/native" \
    | awk '{print $2}'
  rg -o '^(const[[:space:]]+char[[:space:]]*\*[[:space:]]*|void[[:space:]]+|int[[:space:]]+)lua(L)?_[A-Za-z0-9_]+[[:space:]]*\(' "${repo_root}/internal/native" \
    | sed -E 's/.*(lua(L)?_[A-Za-z0-9_]+)[[:space:]]*\(.*/\1/'
} | sort -u >"${source_declarations_file}"

export CGO_ENABLED=1
if [[ -z "${GLUA_BIN:-}" ]]; then
  mkdir -p "$(dirname "${glua_bin}")"
  echo "build native glua: ${glua_bin}"
  go build -tags native_modules -trimpath -o "${glua_bin}" ./cmd/glua
elif [[ ! -x "${glua_bin}" ]]; then
  echo "GLUA_BIN is not executable: ${glua_bin}" >&2
  exit 1
fi

BUILD_DIR="${build_root}/fixtures" CGO_ENABLED=1 "${repo_root}/scripts/build-native-fixtures.sh"
BUILD_DIR="${build_root}/cjson" CGO_ENABLED=1 "${repo_root}/scripts/build-native-cjson.sh"
BUILD_DIR="${build_root}/lpeg" CGO_ENABLED=1 "${repo_root}/scripts/build-native-lpeg.sh"
BUILD_DIR="${build_root}/luasocket" CGO_ENABLED=1 "${repo_root}/scripts/build-native-luasocket.sh"

find "${build_root}" -type f \( -name '*.so' -o -name '*.dylib' \) -print | sort >"${modules_file}"
if [[ ! -s "${modules_file}" ]]; then
  echo "no native module outputs found under ${build_root}" >&2
  exit 1
fi

: >"${required_symbols_file}"
while IFS= read -r module_path; do
  module_symbols_file="${work_dir}/$(basename "${module_path}").undefined.txt"
  list_module_lua_undefined "${module_path}" >"${module_symbols_file}"
  if [[ -s "${module_symbols_file}" ]]; then
    echo "module Lua ABI requirements: ${module_path}"
    sed 's/^/  /' "${module_symbols_file}"
    cat "${module_symbols_file}" >>"${required_symbols_file}"
  fi
done <"${modules_file}"

sort -u "${required_symbols_file}" -o "${required_symbols_file}"
if [[ ! -s "${required_symbols_file}" ]]; then
  echo "no unresolved Lua ABI symbols found in native module outputs" >&2
  exit 1
fi

list_binary_lua_exports "${glua_bin}" >"${binary_exports_file}"
comm -23 "${required_symbols_file}" "${binary_exports_file}" >"${missing_binary_file}"
comm -23 "${required_symbols_file}" "${source_declarations_file}" >"${missing_source_file}"

if [[ -s "${missing_source_file}" ]]; then
  echo "native modules require Lua ABI symbols missing from native source declarations:" >&2
  sed 's/^/  /' "${missing_source_file}" >&2
  exit 1
fi

if [[ -s "${missing_binary_file}" ]]; then
  echo "native glua binary is missing Lua ABI symbols required by native modules:" >&2
  sed 's/^/  /' "${missing_binary_file}" >&2
  exit 1
fi

required_count="$(wc -l <"${required_symbols_file}" | tr -d ' ')"
source_declaration_count="$(wc -l <"${source_declarations_file}" | tr -d ' ')"
binary_export_count="$(wc -l <"${binary_exports_file}" | tr -d ' ')"
module_count="$(wc -l <"${modules_file}" | tr -d ' ')"

echo
echo "native Lua ABI symbol coverage passed"
echo "modules=${module_count}"
echo "required_symbols=${required_count}"
echo "source_lua_declarations=${source_declaration_count}"
echo "binary_lua_exports=${binary_export_count}"
