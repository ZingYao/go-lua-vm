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
BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-fixtures.sh" >/dev/null

module_extension=".so"
module_path="${build_dir}/lpeg${module_extension}"
if [[ ! -f "${module_path}" ]]; then
  echo "LPeg module output missing: ${module_path}" >&2
  exit 1
fi
smoke_module_path="${build_dir}/glua_native_smoke${module_extension}"
if [[ ! -f "${smoke_module_path}" ]]; then
  echo "native smoke module output missing: ${smoke_module_path}" >&2
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

run_prefix646_pcall_only_probe() {
  local source="${work_dir}/prefix646_pcall_only.lua"
  local output="${work_dir}/prefix646_pcall_only.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;647,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
local st, err = pcall(m.match, p, string.rep("0", lim))
assert(not st and string.find(tostring(err), "stack overflow", 1, true))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix646 + overflow pcall-only probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix646_pcall_only '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_parity_construct_overflow_probe() {
  local source="${work_dir}/prefix620_parity_construct_overflow.lua"
  local output="${work_dir}/prefix620_parity_construct_overflow.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
p = m.P{
  [1] = '0' * m.V(2) + '1' * m.V(3) + -1,
  [2] = '0' * m.V(1) + '1' * m.V(4),
  [3] = '0' * m.V(4) + '1' * m.V(1),
  [4] = '0' * m.V(3) + '1' * m.V(2),
}
local lim = 10000
p = m.P{ '0' * m.V(1) + '0' }
local st, err = pcall(m.match, p, string.rep("0", lim))
assert(not st and string.find(tostring(err), "stack overflow", 1, true))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + parity construction + overflow pcall-only probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_parity_construct_overflow '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_parity_matches_overflow_probe() {
  local source="${work_dir}/prefix620_parity_matches_overflow.lua"
  local output="${work_dir}/prefix620_parity_matches_overflow.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
p = m.P{
  [1] = '0' * m.V(2) + '1' * m.V(3) + -1,
  [2] = '0' * m.V(1) + '1' * m.V(4),
  [3] = '0' * m.V(4) + '1' * m.V(1),
  [4] = '0' * m.V(3) + '1' * m.V(2),
}
assert(p:match(string.rep("00", 10000)))
assert(p:match(string.rep("01", 10000)))
assert(p:match(string.rep("011", 10000)))
assert(not p:match(string.rep("011", 10000) .. "1"))
assert(not p:match(string.rep("011", 10001)))
local lim = 10000
p = m.P{ '0' * m.V(1) + '0' }
local st, err = pcall(m.match, p, string.rep("0", lim))
assert(not st and string.find(tostring(err), "stack overflow", 1, true))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + parity matches + overflow pcall-only probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_parity_matches_overflow '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_require_smoke_probe() {
  local source="${work_dir}/prefix620_require_smoke.lua"
  local output="${work_dir}/prefix620_require_smoke.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
assert(require("glua_native_smoke"))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + require native smoke probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_require_smoke '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_parity_matches_require_smoke_probe() {
  local source="${work_dir}/prefix620_parity_matches_require_smoke.lua"
  local output="${work_dir}/prefix620_parity_matches_require_smoke.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
p = m.P{
  [1] = '0' * m.V(2) + '1' * m.V(3) + -1,
  [2] = '0' * m.V(1) + '1' * m.V(4),
  [3] = '0' * m.V(4) + '1' * m.V(1),
  [4] = '0' * m.V(3) + '1' * m.V(2),
}
assert(p:match(string.rep("00", 10000)))
assert(p:match(string.rep("01", 10000)))
assert(p:match(string.rep("011", 10000)))
assert(not p:match(string.rep("011", 10000) .. "1"))
assert(not p:match(string.rep("011", 10001)))
assert(require("glua_native_smoke"))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + parity matches + require native smoke probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_parity_matches_require_smoke '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_fresh_loadlib_missing_file_probe() {
  local source="${work_dir}/fresh_loadlib_missing_file.lua"
  local output="${work_dir}/fresh_loadlib_missing_file.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    cat <<LUA
local m = assert(require("lpeg"))
local f, message, where = package.loadlib("${build_dir}/no_such_native_module${module_extension}", "luaopen_missing")
assert(f == nil and where == "open", tostring(message) .. " / " .. tostring(where))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run fresh lpeg + missing-file package.loadlib probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'fresh_loadlib_missing_file '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_loadlib_missing_file_probe() {
  local source="${work_dir}/prefix620_loadlib_missing_file.lua"
  local output="${work_dir}/prefix620_loadlib_missing_file.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
local f, message, where = package.loadlib("${build_dir}/no_such_native_module${module_extension}", "luaopen_missing")
assert(f == nil and where == "open", tostring(message) .. " / " .. tostring(where))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + missing-file package.loadlib probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_loadlib_missing_file '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_synthetic_loadlib_error_probe() {
  local source="${work_dir}/prefix620_synthetic_loadlib_error.lua"
  local output="${work_dir}/prefix620_synthetic_loadlib_error.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
local f, message, where = nil, "synthetic loadlib error", "open"
assert(f == nil and message == "synthetic loadlib error" and where == "open")
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + synthetic loadlib error triple probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_synthetic_loadlib_error '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_loadlib_bad_argument_probe() {
  local source="${work_dir}/prefix620_loadlib_bad_argument.lua"
  local output="${work_dir}/prefix620_loadlib_bad_argument.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<'LUA'
local ok, message = pcall(package.loadlib, 123, "luaopen_missing")
assert(ok == false and type(message) == "string", tostring(message))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + package.loadlib bad-argument probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_loadlib_bad_argument '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_loadlib_regular_file_probe() {
  local source="${work_dir}/prefix620_loadlib_regular_file.lua"
  local output="${work_dir}/prefix620_loadlib_regular_file.out"
  local regular_file="${work_dir}/not_a_dynamic_library${module_extension}"

  printf 'not a dynamic library\n' >"${regular_file}"
  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
local f, message, where = package.loadlib("${regular_file}", "luaopen_missing")
assert(f == nil and where == "open", tostring(message) .. " / " .. tostring(where))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + regular-file package.loadlib probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_loadlib_regular_file '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_loadlib_diagnostic_probe() {
  local mode="$1"
  local label="$2"
  local source="${work_dir}/${label}.lua"
  local output="${work_dir}/${label}.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
local f, message, where = package.loadlib("${build_dir}/no_such_native_module${module_extension}", "luaopen_missing")
assert(f == nil and where == "open", tostring(message) .. " / " .. tostring(where))
assert(tostring(message):find("${mode}", 1, true), tostring(message))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + package.loadlib diagnostic ${mode} probe"
  GLUA_NATIVE_DLOPEN_DIAGNOSTIC="${mode}" \
    GLUA_NATIVE_DLOPEN_DIAGNOSTIC_MATCH="no_such_native_module" \
    "${glua_bin}" "${source}" >"${output}"
  printf '%s ' "${label}"
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_loader_diagnostic_probe() {
  local mode="$1"
  local label="$2"
  local target_path="$3"
  local target_symbol="$4"
  local match_fragment="$5"
  local expected_where="$6"
  local expected_message_fragment="${7:-${mode}}"
  local source="${work_dir}/${label}.lua"
  local output="${work_dir}/${label}.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
local f, message, where = package.loadlib("${target_path}", "${target_symbol}")
assert(f == nil and where == "${expected_where}", tostring(message) .. " / " .. tostring(where))
assert(tostring(message):find("${expected_message_fragment}", 1, true), tostring(message))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + native loader diagnostic ${mode} probe"
  GLUA_NATIVE_LOADER_DIAGNOSTIC="${mode}" \
    GLUA_NATIVE_LOADER_DIAGNOSTIC_MATCH="${match_fragment}" \
    "${glua_bin}" "${source}" >"${output}"
  printf '%s ' "${label}"
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_loadlib_branch_probe() {
  local mode="$1"
  local label="$2"
  local expected_count="$3"
  local target_path="${build_dir}/no_such_native_module${module_extension}"
  local source="${work_dir}/${label}.lua"
  local output="${work_dir}/${label}.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
local function capture(...)
  return select("#", ...), ...
end
local n, f, message, where = capture(package.loadlib("${target_path}", "luaopen_missing"))
assert(n == ${expected_count}, tostring(n))
assert(f == nil, tostring(f))
if ${expected_count} >= 2 then
  assert(tostring(message):find("${mode}", 1, true), tostring(message))
end
if ${expected_count} >= 3 then
  assert(where == "open", tostring(message) .. " / " .. tostring(where))
end
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + package.loadlib branch diagnostic ${mode} (${expected_count} returns) probe"
  GLUA_PACKAGE_LOADLIB_DIAGNOSTIC="${mode}" \
    GLUA_PACKAGE_LOADLIB_DIAGNOSTIC_MATCH="no_such_native_module" \
    "${glua_bin}" "${source}" >"${output}"
  printf '%s ' "${label}"
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_equivalent_go_closure_probe() {
  local mode="$1"
  local label="$2"
  local expected_count="$3"
  local expected_kind="$4"
  local scope="${5:-package}"
  local target_path="${build_dir}/no_such_native_module${module_extension}"
  local source="${work_dir}/${label}.lua"
  local output="${work_dir}/${label}.out"
  local diag_expr='package._glua_loadlib_diag'
  if [[ "${scope}" == "global" ]]; then
    diag_expr='_glua_loadlib_diag'
  fi

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
local diag = assert(${diag_expr})
local function capture(...)
  return select("#", ...), ...
end
local n, f, message, where = capture(diag("${target_path}", "luaopen_missing"))
assert(n == ${expected_count}, tostring(n))
local expected_kind = "${expected_kind}"
if expected_kind == "nil" then
  assert(f == nil, tostring(f))
elseif expected_kind == "true" then
  assert(f == true, tostring(f))
elseif expected_kind ~= "none" then
  assert(type(f) == expected_kind, tostring(type(f)) .. " / " .. tostring(f))
end
if ${expected_count} >= 2 then
  assert(tostring(message):find("equivalent", 1, true), tostring(message))
end
if ${expected_count} >= 3 then
  assert(where == "open", tostring(message) .. " / " .. tostring(where))
end
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + ${scope} equivalent Go closure diagnostic ${mode} (${expected_count} returns, ${expected_kind}) probe"
  GLUA_PACKAGE_LOADLIB_EQUIVALENT_DIAGNOSTIC="${mode}" \
    GLUA_PACKAGE_LOADLIB_EQUIVALENT_DIAGNOSTIC_MATCH="no_such_native_module" \
    "${glua_bin}" "${source}" >"${output}"
  printf '%s ' "${label}"
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_loadlib_existing_symbol_probe() {
  local source="${work_dir}/prefix620_loadlib_existing_symbol.lua"
  local output="${work_dir}/prefix620_loadlib_existing_symbol.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
local loader = assert(package.loadlib("${smoke_module_path}", "luaopen_glua_native_smoke"))
assert(type(loader) == "function")
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + existing-symbol package.loadlib probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_loadlib_existing_symbol '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix620_parity_matches_loadlib_missing_file_probe() {
  local source="${work_dir}/prefix620_parity_matches_loadlib_missing_file.lua"
  local output="${work_dir}/prefix620_parity_matches_loadlib_missing_file.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;621,\$d" "${repo_root}/third_party/lpeg/test.lua"
    cat <<LUA
p = m.P{
  [1] = '0' * m.V(2) + '1' * m.V(3) + -1,
  [2] = '0' * m.V(1) + '1' * m.V(4),
  [3] = '0' * m.V(4) + '1' * m.V(1),
  [4] = '0' * m.V(3) + '1' * m.V(2),
}
assert(p:match(string.rep("00", 10000)))
assert(p:match(string.rep("01", 10000)))
assert(p:match(string.rep("011", 10000)))
assert(not p:match(string.rep("011", 10000) .. "1"))
assert(not p:match(string.rep("011", 10001)))
local f, message, where = package.loadlib("${build_dir}/no_such_native_module${module_extension}", "luaopen_missing")
assert(f == nil and where == "open", tostring(message) .. " / " .. tostring(where))
LUA
    emit_probe_tail
  } >"${source}"

  echo "run prefix620 + parity matches + missing-file package.loadlib probe"
  "${glua_bin}" "${source}" >"${output}"
  printf 'prefix620_parity_matches_loadlib_missing_file '
  rg '^PROBE' "${output}" || tail -n 3 "${output}"
}

run_prefix646_pcall_only_probe
run_prefix620_parity_construct_overflow_probe
run_prefix620_parity_matches_overflow_probe
run_prefix620_require_smoke_probe
run_prefix620_parity_matches_require_smoke_probe
run_fresh_loadlib_missing_file_probe
run_prefix620_loadlib_missing_file_probe
run_prefix620_synthetic_loadlib_error_probe
run_prefix620_loadlib_bad_argument_probe
run_prefix620_loadlib_regular_file_probe
run_prefix620_loadlib_diagnostic_probe before-cstring prefix620_loadlib_diag_before_cstring
run_prefix620_loadlib_diagnostic_probe after-cstring prefix620_loadlib_diag_after_cstring
run_prefix620_loadlib_diagnostic_probe after-clear prefix620_loadlib_diag_after_clear
run_prefix620_loadlib_diagnostic_probe after-dlopen-no-dlerror prefix620_loadlib_diag_after_dlopen_no_dlerror
run_prefix620_loader_diagnostic_probe dynamic-error prefix620_loader_diag_dynamic_error "${build_dir}/no_such_native_module${module_extension}" luaopen_missing no_such_native_module open
run_prefix620_loader_diagnostic_probe plain-error prefix620_loader_diag_plain_error "${build_dir}/no_such_native_module${module_extension}" luaopen_missing no_such_native_module open
run_prefix620_loader_diagnostic_probe noncallable prefix620_loader_diag_noncallable "${build_dir}/no_such_native_module${module_extension}" luaopen_missing no_such_native_module init "not return a callable"
run_prefix620_loader_diagnostic_probe after-open-dynamic-error prefix620_loader_diag_after_open_dynamic_error "${smoke_module_path}" luaopen_glua_native_smoke glua_native_smoke open
run_prefix620_loadlib_branch_probe before-args-fixed prefix620_loadlib_branch_before_args_fixed 3
run_prefix620_loadlib_branch_probe before-loader-fixed prefix620_loadlib_branch_before_loader_fixed 3
run_prefix620_loadlib_branch_probe after-args-one-return prefix620_loadlib_branch_after_args_one_return 1
run_prefix620_loadlib_branch_probe after-args-two-return prefix620_loadlib_branch_after_args_two_return 2
run_prefix620_loadlib_branch_probe after-loader-fixed prefix620_loadlib_branch_after_loader_fixed 3
run_prefix620_equivalent_go_closure_probe empty-return prefix620_equivalent_go_closure_empty_return 0 none
run_prefix620_equivalent_go_closure_probe one-return prefix620_equivalent_go_closure_one_return 1 nil
run_prefix620_equivalent_go_closure_probe two-return prefix620_equivalent_go_closure_two_return 2 nil
run_prefix620_equivalent_go_closure_probe three-return prefix620_equivalent_go_closure_three_return 3 nil
run_prefix620_equivalent_go_closure_probe true-return prefix620_equivalent_go_closure_true_return 1 true
run_prefix620_equivalent_go_closure_probe string-return prefix620_equivalent_go_closure_string_return 1 string
run_prefix620_equivalent_go_closure_probe table-return prefix620_equivalent_go_closure_table_return 1 table
run_prefix620_equivalent_go_closure_probe callable-return prefix620_equivalent_go_closure_callable_return 1 function
run_prefix620_equivalent_go_closure_probe one-return prefix620_global_go_closure_one_return 1 nil global
run_prefix620_equivalent_go_closure_probe callable-return prefix620_global_go_closure_callable_return 1 function global
run_prefix620_loadlib_existing_symbol_probe
run_prefix620_parity_matches_loadlib_missing_file_probe

echo "diagnostic note: PROBE result below 18 marks the current LPeg 1159 narrowing point; this script reports evidence and does not assert the known mismatch."
