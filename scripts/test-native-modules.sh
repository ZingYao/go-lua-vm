#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-fixtures/${target_goos}-${target_goarch}}"
if [[ "${target_goos}" == "windows" && -z "${GLUA_BIN:-}" ]]; then
  glua_bin="${build_dir}/glua-native.exe"
else
  glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
fi
template_file="${repo_root}/tests/native_modules/fixtures/glua_native_smoke.lua"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-modules.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native module CLI smoke"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "GLUA_BIN=${glua_bin}"
echo "BUILD_DIR=${build_dir}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native module smoke" >&2
  exit 1
fi

case "${target_goos}" in
  darwin)
    runtime_extensions=(".dylib" ".so")
    ;;
  linux)
    runtime_extensions=(".so")
    ;;
  windows)
    runtime_extensions=(".dll")
    ;;
  *)
    echo "skip: unsupported native module CLI smoke target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

export CGO_ENABLED=1

if [[ -z "${GLUA_BIN:-}" ]]; then
  mkdir -p "$(dirname "${glua_bin}")"
  echo "build native glua: ${glua_bin}"
  go build -trimpath -o "${glua_bin}" ./cmd/glua
elif [[ ! -x "${glua_bin}" ]]; then
  echo "GLUA_BIN is not executable: ${glua_bin}" >&2
  exit 1
fi

if [[ "${target_goos}" == "windows" ]]; then
  BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-windows-lua53-shim.sh"
  export LUA53_IMPORT_LIB="${build_dir}/liblua53.dll.a"
fi

BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-fixtures.sh"

lua_string_literal() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  printf '"%s"' "${text}"
}

runtime_path() {
  local path="$1"
  if [[ "${target_goos}" == "windows" ]]; then
    cygpath -m "${path}"
    return 0
  fi
  echo "${path}"
}

missing_lua_pattern="${work_dir}/missing/?.lua"
package_path_literal="$(lua_string_literal "${missing_lua_pattern}")"

for extension in "${runtime_extensions[@]}"; do
  smoke_module="${build_dir}/glua_native_smoke${extension}"
  failopen_module="${build_dir}/glua_native_failopen${extension}"
  if [[ ! -f "${smoke_module}" || ! -f "${failopen_module}" ]]; then
    echo "native fixture outputs missing for ${extension}:" >&2
    echo "  ${smoke_module}" >&2
    echo "  ${failopen_module}" >&2
    exit 1
  fi

  suffix_name="${extension#.}"
  cpath_pattern="$(runtime_path "${build_dir}")/?${extension}"
  package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"

  smoke_source="${work_dir}/glua_native_smoke_${suffix_name}.lua"
  smoke_template="$(<"${template_file}")"
  smoke_template="${smoke_template//@GLUA_NATIVE_PACKAGE_PATH@/${package_path_literal}}"
  smoke_template="${smoke_template//@GLUA_NATIVE_PACKAGE_CPATH@/${package_cpath_literal}}"
  printf '%s\n' "${smoke_template}" >"${smoke_source}"

  failopen_source="${work_dir}/glua_native_failopen_${suffix_name}.lua"
  cat >"${failopen_source}" <<EOF
package.path = ${package_path_literal}
package.cpath = ${package_cpath_literal}

local ok, message = pcall(require, "glua_native_failopen")
assert(ok == false, "require unexpectedly succeeded")
assert(string.find(message, "native open failure", 1, true), message)
assert(package.loaded["glua_native_failopen"] == nil)
EOF

  echo "run native smoke require (${extension}): ${smoke_source}"
  "${glua_bin}" "${smoke_source}"

  echo "run native failopen require (${extension}): ${failopen_source}"
  "${glua_bin}" "${failopen_source}"
done

echo "native module CLI smoke passed"
