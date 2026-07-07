#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-lpeg/${target_goos}-${target_goarch}}"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
lpeg_source_dir="${repo_root}/third_party/lpeg"
lpeg_test_file="${lpeg_source_dir}/test.lua"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-lpeg.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native LPeg runtime acceptance"
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
  echo "ensure PATH resolves go to ${expected_go_version} before running native LPeg acceptance" >&2
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
    echo "skip: Windows LPeg runtime acceptance requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native LPeg runtime target GOOS=${target_goos}" >&2
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

BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-lpeg.sh"

if [[ ! -f "${lpeg_test_file}" ]]; then
  echo "LPeg official test file missing: ${lpeg_test_file}" >&2
  exit 1
fi

lua_string_literal() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  printf '"%s"' "${text}"
}

package_path_literal="$(lua_string_literal "${lpeg_source_dir}/?.lua;${work_dir}/missing/?.lua")"
lpeg_test_literal="$(lua_string_literal "${lpeg_test_file}")"

for extension in "${runtime_extensions[@]}"; do
  module_path="${build_dir}/lpeg${extension}"
  if [[ ! -f "${module_path}" ]]; then
    echo "LPeg module output missing for ${extension}: ${module_path}" >&2
    exit 1
  fi

  suffix_name="${extension#.}"
  cpath_pattern="${build_dir}/?${extension}"
  package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"
  acceptance_source="${work_dir}/lpeg_acceptance_${suffix_name}.lua"

  cat >"${acceptance_source}" <<EOF
package.path = ${package_path_literal}
package.cpath = ${package_cpath_literal}

local lpeg = assert(require("lpeg"))
local P, R, S, C = lpeg.P, lpeg.R, lpeg.S, lpeg.C

assert(type(lpeg) == "table")
assert(type(P) == "function")
assert(type(lpeg.match) == "function")

assert(lpeg.match(P("abc"), "abcdef") == 4)
assert(lpeg.match(P(1)^0, "abcd") == 5)
assert(lpeg.match(R("az")^1 * -1, "abc") == 4)
assert(lpeg.match(S("ab")^1, "abba!") == 5)
assert(lpeg.match(C(R("az")^1), "lua53") == "lua")
assert(lpeg.match(P(false) + "a", "a") == 2)

dofile(${lpeg_test_literal})

print("native LPeg full official test passed", "${extension}")
EOF

  echo "run native LPeg acceptance (${extension}): ${acceptance_source}"
  "${glua_bin}" "${acceptance_source}"
done

echo "native LPeg runtime acceptance passed"
