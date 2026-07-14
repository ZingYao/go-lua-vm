#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-cjson/${target_goos}-${target_goarch}}"
if [[ "${target_goos}" == "windows" && -z "${GLUA_BIN:-}" ]]; then
  glua_bin="${build_dir}/glua-native.exe"
else
  glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
fi
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-cjson.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native lua-cjson runtime acceptance"
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
  echo "ensure PATH resolves go to ${expected_go_version} before running native lua-cjson acceptance" >&2
  exit 1
fi

case "${target_goos}" in
  darwin)
    runtime_extensions=(".so" ".dylib")
    ;;
  linux)
    runtime_extensions=(".so")
    ;;
  windows)
    runtime_extensions=(".dll")
    ;;
  *)
    echo "skip: unsupported native lua-cjson runtime target GOOS=${target_goos}" >&2
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

BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-cjson.sh"

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

list_undefined_lua_abi_symbols() {
  local module_path="$1"
  local output
  output="$(nm -u "${module_path}")"
  printf '%s\n' "${output}" \
    | awk '{print $NF}' \
    | sed 's/^_//' \
    | grep -E '^lua(L)?_' \
    | sort -u \
    || true
}

list_exported_lua_abi_symbols() {
  local binary_path="$1"
  local output
  output="$(nm -g "${binary_path}" 2>/dev/null || true)"
  if [[ -z "${output}" ]]; then
    output="$(nm -D "${binary_path}" 2>/dev/null || true)"
  fi
  printf '%s\n' "${output}" \
    | awk '{print $NF}' \
    | sed 's/^_//' \
    | grep -E '^lua(L)?_' \
    | sort -u \
    || true
}

check_module_does_not_link_external_lua() {
  local module_path="$1"
  case "${target_goos}" in
    darwin)
      if command -v otool >/dev/null 2>&1; then
        if otool -L "${module_path}" | grep -E 'liblua|lua5[.0-9-]*|lua53' >/dev/null; then
          echo "lua-cjson module links an external Lua runtime: ${module_path}" >&2
          otool -L "${module_path}" >&2
          exit 1
        fi
      fi
      ;;
    linux)
      if command -v ldd >/dev/null 2>&1; then
        if ldd "${module_path}" | grep -E 'liblua|lua5[.0-9-]*|lua53' >/dev/null; then
          echo "lua-cjson module links an external Lua runtime: ${module_path}" >&2
          ldd "${module_path}" >&2
          exit 1
        fi
      fi
      ;;
  esac
}

check_lua_abi_symbols_resolve_to_glua() {
  local module_path="$1"
  local undefined_file="${work_dir}/undefined_lua_abi.txt"
  local exported_file="${work_dir}/exported_lua_abi.txt"
  local missing_file="${work_dir}/missing_lua_abi.txt"

  if ! command -v nm >/dev/null 2>&1; then
    echo "nm is required for lua-cjson ABI symbol validation" >&2
    exit 1
  fi

  list_undefined_lua_abi_symbols "${module_path}" >"${undefined_file}"
  if [[ ! -s "${undefined_file}" ]]; then
    echo "lua-cjson module does not expose unresolved Lua ABI symbols: ${module_path}" >&2
    exit 1
  fi

  list_exported_lua_abi_symbols "${glua_bin}" >"${exported_file}"
  if [[ ! -s "${exported_file}" ]]; then
    echo "native glua binary does not export Lua ABI shim symbols: ${glua_bin}" >&2
    exit 1
  fi

  comm -23 "${undefined_file}" "${exported_file}" >"${missing_file}"
  if [[ -s "${missing_file}" ]]; then
    echo "native glua binary is missing Lua ABI symbols required by ${module_path}:" >&2
    cat "${missing_file}" >&2
    exit 1
  fi

  check_module_does_not_link_external_lua "${module_path}"

  echo "lua-cjson ABI symbols resolved by native glua shim (${module_path}):"
  sed 's/^/  /' "${undefined_file}"
}

package_path_literal="$(lua_string_literal "${work_dir}/missing/?.lua")"

for extension in "${runtime_extensions[@]}"; do
  module_path="${build_dir}/cjson${extension}"
  if [[ ! -f "${module_path}" ]]; then
    echo "lua-cjson module output missing for ${extension}: ${module_path}" >&2
    exit 1
  fi

  if [[ "${target_goos}" != "windows" ]]; then
    check_lua_abi_symbols_resolve_to_glua "${module_path}"
  fi

  suffix_name="${extension#.}"
  cpath_pattern="$(runtime_path "${build_dir}")/?${extension}"
  package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"
  acceptance_source="${work_dir}/cjson_acceptance_${suffix_name}.lua"

  cat >"${acceptance_source}" <<EOF
package.path = ${package_path_literal}
package.cpath = ${package_cpath_literal}

local cjson = assert(require("cjson"))
assert(cjson._NAME == "cjson", cjson._NAME)
assert(type(cjson.encode) == "function")
assert(type(cjson.decode) == "function")
assert(type(cjson.null) == "userdata")

local encoded = cjson.encode({ a = 1, b = true, c = cjson.null, list = { 1, 2, "x" } })
local decoded = cjson.decode(encoded)
assert(decoded.a == 1, decoded.a)
assert(decoded.b == true, tostring(decoded.b))
assert(decoded.c == cjson.null, tostring(decoded.c))
assert(decoded.list[1] == 1 and decoded.list[2] == 2 and decoded.list[3] == "x")

assert(cjson.decode("1") == 1)
assert(cjson.decode("true") == true)
assert(cjson.decode("null") == cjson.null)

local ok, message = pcall(cjson.decode, "{")
assert(ok == false, "invalid JSON unexpectedly decoded")
assert(type(message) == "string" and string.find(message, "Expected", 1, true), message)

local encode_ok, encode_message = pcall(cjson.encode, function() end)
assert(encode_ok == false, "function unexpectedly encoded")
assert(type(encode_message) == "string" and string.find(encode_message, "Cannot serialise", 1, true), encode_message)

print("native lua-cjson runtime acceptance passed", "${extension}", cjson._NAME, cjson._VERSION, encoded)
EOF

  echo "run native lua-cjson acceptance (${extension}): ${acceptance_source}"
  "${glua_bin}" "${acceptance_source}"
done

echo "native lua-cjson runtime acceptance passed"
