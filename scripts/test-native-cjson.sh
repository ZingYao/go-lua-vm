#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-cjson/${target_goos}-${target_goarch}}"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
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
    cpath_pattern="${build_dir}/?.so;${build_dir}/?.dylib"
    required_modules=("${build_dir}/cjson.so" "${build_dir}/cjson.dylib")
    ;;
  linux)
    cpath_pattern="${build_dir}/?.so"
    required_modules=("${build_dir}/cjson.so")
    ;;
  windows)
    echo "skip: Windows lua-cjson runtime acceptance requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
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
  go build -tags native_modules -trimpath -o "${glua_bin}" ./cmd/glua
elif [[ ! -x "${glua_bin}" ]]; then
  echo "GLUA_BIN is not executable: ${glua_bin}" >&2
  exit 1
fi

BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-cjson.sh"

for module_path in "${required_modules[@]}"; do
  if [[ ! -f "${module_path}" ]]; then
    echo "lua-cjson module output missing: ${module_path}" >&2
    exit 1
  fi
done

lua_string_literal() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  printf '"%s"' "${text}"
}

package_path_literal="$(lua_string_literal "${work_dir}/missing/?.lua")"
package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"
acceptance_source="${work_dir}/cjson_acceptance.lua"

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

print("native lua-cjson runtime acceptance passed", cjson._NAME, cjson._VERSION, encoded)
EOF

echo "run native lua-cjson acceptance: ${acceptance_source}"
"${glua_bin}" "${acceptance_source}"

echo "native lua-cjson runtime acceptance passed"
