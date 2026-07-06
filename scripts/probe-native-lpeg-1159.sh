#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-lpeg/${target_goos}-${target_goarch}}"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-lpeg-1159.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native LPeg 1159 diagnostic probe"
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
  echo "ensure PATH resolves go to ${expected_go_version} before running native LPeg probe" >&2
  exit 1
fi

case "${target_goos}" in
  darwin|linux)
    ;;
  windows)
    echo "skip: Windows LPeg probe requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native LPeg probe target GOOS=${target_goos}" >&2
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

BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-lpeg.sh" >/dev/null

module_extension=".so"
module_path="${build_dir}/lpeg${module_extension}"
if [[ ! -f "${module_path}" ]]; then
  echo "LPeg module output missing: ${module_path}" >&2
  exit 1
fi

emit_probe_tail() {
  cat <<'LUA'
local attempts = {}
c = '[' * m.Cg(m.P'='^0, "init") * '[' *
    { m.Cmt(']' * m.C(m.P'='^0) * ']' * m.Cb("init"), function (_, pos, s1, s2)
                                               attempts[#attempts + 1] = pos .. ':' .. s1 .. ':' .. s2
                                               return s1 == s2 end)
       + 1 * m.V(1) } / 0
print("PROBE", c:match'[==[]]====]]]]==]===[]', table.concat(attempts, ","))
LUA
}

run_prefix_probe() {
  local line="$1"
  local source="${work_dir}/prefix_${line}.lua"
  local output="${work_dir}/prefix_${line}.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;$((line + 1)),\$d" "${repo_root}/third_party/lpeg/test.lua"
    emit_probe_tail
  } >"${source}"

  echo "run prefix probe through test.lua:${line}"
  "${glua_bin}" "${source}" >"${output}"
  printf 'line=%s ' "${line}"
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_isolated_deep_probe() {
  local source="${work_dir}/isolated_deep.lua"
  local output="${work_dir}/isolated_deep.out"

  {
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    cat <<'LUA'
local m = assert(require("lpeg"))
local function checkerr(msg, f, ...)
  local st, err = pcall(f, ...)
  assert(not st and m.match({ m.P(msg) + 1 * m.V(1) }, err))
end
local lim = 10000
p = m.P{ '0' * m.V(1) + '0' }
checkerr("stack overflow", m.match, p, string.rep("0", lim))
m.setmaxstack(2*lim)
checkerr("stack overflow", m.match, p, string.rep("0", lim))
m.setmaxstack(2*lim + 4)
assert(m.match(p, string.rep("0", lim)) == lim + 1)
LUA
    emit_probe_tail
  } >"${source}"

  echo "run isolated 645-651 probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'isolated_645_651 '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix_probe 558
run_prefix_probe 641
run_prefix_probe 646
run_prefix_probe 647
run_prefix_probe 651
run_prefix_probe 1153
run_isolated_deep_probe

run_post_overflow_cleanup_probe() {
  local source="${work_dir}/post_overflow_cleanup.lua"
  local output="${work_dir}/post_overflow_cleanup.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;648,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
p = nil
collectgarbage()
collectgarbage()
package.loaded.lpeg = nil
m = assert(require("lpeg"))
m.setmaxstack(1000000)
LUA
    emit_probe_tail
  } >"${source}"

  echo "run post-overflow cleanup/reload/high-max probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'post_overflow_cleanup '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_post_overflow_cleanup_probe

echo "diagnostic note: PROBE result below 18 marks the current LPeg 1159 narrowing point; this script reports evidence and does not assert the known mismatch."
