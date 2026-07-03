#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lua_bin="${LUA_BIN:-lua}"
luac_bin="${LUAC_BIN:-luac}"
glua_bin="${GLUA_BIN:-${repo_root}/bin/glua}"
gluac_bin="${GLUAC_BIN:-${repo_root}/bin/gluac}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-cli.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

resolve_tool() {
  local tool="$1"
  if [[ -f "${tool}" && -x "${tool}" ]]; then
    printf '%s\n' "${tool}"
    return 0
  fi
  command -v "${tool}"
}

require_tool() {
  local label="$1"
  local tool="$2"
  if ! resolve_tool "${tool}" >/dev/null 2>&1; then
    echo "${label} not found or not executable: ${tool}" >&2
    exit 1
  fi
}

require_tool "official lua" "${lua_bin}"
require_tool "official luac" "${luac_bin}"
require_tool "glua" "${glua_bin}"
require_tool "gluac" "${gluac_bin}"

lua_bin="$(resolve_tool "${lua_bin}")"
luac_bin="$(resolve_tool "${luac_bin}")"
glua_bin="$(resolve_tool "${glua_bin}")"
gluac_bin="$(resolve_tool "${gluac_bin}")"

check_lua53_tool() {
  local label="$1"
  local tool="$2"
  local version_output
  version_output="$("${tool}" -v 2>&1 || true)"
  if [[ "${version_output}" != *"Lua 5.3"* ]]; then
    echo "${label} must be Lua 5.3.x for this compatibility check: ${tool}" >&2
    echo "actual version output: ${version_output}" >&2
    echo "set LUA_BIN=/path/to/lua5.3 and LUAC_BIN=/path/to/luac5.3" >&2
    exit 1
  fi
}

check_lua53_tool "official lua" "${lua_bin}"
check_lua53_tool "official luac" "${luac_bin}"

fixture_dir="${work_dir}/fixtures"
mkdir -p "${fixture_dir}/mods"

printf 'print("hello")\n' >"${fixture_dir}/hello.lua"
printf 'print(arg[0]); print(arg[1]); print(arg[2])\n' >"${fixture_dir}/args.lua"
printf 'return {value = 42}\n' >"${fixture_dir}/mods/samplemod.lua"
printf 'if\n' >"${fixture_dir}/syntax_error.lua"
printf 'error("boom")\n' >"${fixture_dir}/runtime_error.lua"
export LUA_PATH="${fixture_dir}/mods/?.lua;;"

status=0

normalize_output() {
  local input="$1"
  local output="$2"
  sed \
    -e "s#${work_dir}#<tmp>#g" \
    -e "s#${lua_bin}#<lua>#g" \
    -e "s#${glua_bin}#<lua>#g" \
    -e "s#${luac_bin}#<luac>#g" \
    -e "s#${gluac_bin}#<luac>#g" \
    -e "s#${repo_root}#<repo>#g" \
    -e 's#<repo>/bin/glua#<lua>#g' \
    -e 's#<repo>/bin/gluac#<luac>#g' \
    -e 's#\.\.\.[^:]*go-lua-vm-cli\.[^/]*/fixtures#<tmp>/fixtures#g' \
    -e 's#^lua[0-9.]*:#<lua>:#' \
    -e 's#^glua:#<lua>:#' \
    -e 's#^luac[0-9.]*:#<luac>:#' \
    -e 's#^gluac:#<luac>:#' \
    "${input}" >"${output}"
}

run_capture() {
  local prefix="$1"
  shift
  set +e
  "$@" >"${prefix}.stdout" 2>"${prefix}.stderr"
  local code=$?
  set -e
  printf '%s\n' "${code}" >"${prefix}.code"
}

compare_files() {
  local label="$1"
  local official="$2"
  local actual="$3"
  if ! diff -u "${official}" "${actual}"; then
    echo "mismatch: ${label}" >&2
    status=1
  fi
}

compare_run() {
  local label="$1"
  local official_prefix="$2"
  local actual_prefix="$3"
  local official_stdout="${official_prefix}.stdout.norm"
  local actual_stdout="${actual_prefix}.stdout.norm"
  local official_stderr="${official_prefix}.stderr.norm"
  local actual_stderr="${actual_prefix}.stderr.norm"

  normalize_output "${official_prefix}.stdout" "${official_stdout}"
  normalize_output "${actual_prefix}.stdout" "${actual_stdout}"
  normalize_output "${official_prefix}.stderr" "${official_stderr}"
  normalize_output "${actual_prefix}.stderr" "${actual_stderr}"

  compare_files "${label} stdout" "${official_stdout}" "${actual_stdout}"
  compare_files "${label} stderr" "${official_stderr}" "${actual_stderr}"
  compare_files "${label} exit code" "${official_prefix}.code" "${actual_prefix}.code"
}

check_version_output() {
  local label="$1"
  local prefix="$2"
  if ! grep -Eq 'Lua 5\.3|Lua 5\.3\.6|compatible' "${prefix}.stdout" "${prefix}.stderr"; then
    echo "version output does not mention Lua 5.3 compatibility: ${label}" >&2
    status=1
  fi
  compare_files "${label} exit code" "${prefix}.code" "${work_dir}/zero.code"
}

printf '0\n' >"${work_dir}/zero.code"

run_lua_case() {
  local name="$1"
  shift
  local official_prefix="${work_dir}/lua_${name}"
  local actual_prefix="${work_dir}/glua_${name}"
  run_capture "${official_prefix}" "${lua_bin}" "$@"
  run_capture "${actual_prefix}" "${glua_bin}" "$@"
  compare_run "lua ${name}" "${official_prefix}" "${actual_prefix}"
}

run_luac_case() {
  local name="$1"
  shift
  local official_prefix="${work_dir}/luac_${name}"
  local actual_prefix="${work_dir}/gluac_${name}"
  run_capture "${official_prefix}" "${luac_bin}" "$@"
  run_capture "${actual_prefix}" "${gluac_bin}" "$@"
  compare_run "luac ${name}" "${official_prefix}" "${actual_prefix}"
}

check_gluac_list_case() {
  local name="$1"
  shift
  local actual_prefix="${work_dir}/gluac_${name}"
  run_capture "${actual_prefix}" "${gluac_bin}" "$@"
  compare_files "gluac ${name} exit code" "${actual_prefix}.code" "${work_dir}/zero.code"
  if [[ ! -s "${actual_prefix}.stdout" ]]; then
    echo "gluac ${name} produced empty listing" >&2
    status=1
    return
  fi
  if ! grep -Eq 'main <|GETTABUP|LOADK|CALL|RETURN' "${actual_prefix}.stdout"; then
    echo "gluac ${name} listing does not contain expected bytecode markers" >&2
    status=1
  fi
}

run_lua_case "script" "${fixture_dir}/hello.lua"
run_lua_case "eval" -e 'print(1 + 2)'
run_lua_case "multi_eval" -e 'a = 7' -e 'print(a)'
run_lua_case "module_load" -l samplemod -e 'print(samplemod.value)'
run_lua_case "script_args" "${fixture_dir}/args.lua" first second
run_lua_case "syntax_error" "${fixture_dir}/syntax_error.lua"
run_lua_case "runtime_error" "${fixture_dir}/runtime_error.lua"

run_capture "${work_dir}/lua_version" "${lua_bin}" -v
run_capture "${work_dir}/glua_version" "${glua_bin}" -v
compare_files "lua -v exit code" "${work_dir}/lua_version.code" "${work_dir}/glua_version.code"
check_version_output "glua -v" "${work_dir}/glua_version"

official_luac_out="${work_dir}/official.luac.out"
actual_luac_out="${work_dir}/actual.luac.out"
run_luac_case "parse_only" -p "${fixture_dir}/hello.lua"
check_gluac_list_case "list" -l "${fixture_dir}/hello.lua"
check_gluac_list_case "list_detail" -l -l "${fixture_dir}/hello.lua"

run_capture "${work_dir}/luac_compile" "${luac_bin}" -o "${official_luac_out}" "${fixture_dir}/hello.lua"
run_capture "${work_dir}/gluac_compile" "${gluac_bin}" -o "${actual_luac_out}" "${fixture_dir}/hello.lua"
compare_run "luac compile" "${work_dir}/luac_compile" "${work_dir}/gluac_compile"

if [[ ! -s "${official_luac_out}" ]]; then
  echo "official luac did not create output: ${official_luac_out}" >&2
  status=1
fi

if [[ ! -s "${actual_luac_out}" ]]; then
  echo "gluac did not create output: ${actual_luac_out}" >&2
  status=1
fi

run_capture "${work_dir}/lua_load_official_chunk" "${lua_bin}" "${official_luac_out}"
run_capture "${work_dir}/glua_load_actual_chunk" "${glua_bin}" "${actual_luac_out}"
compare_files "compiled chunk load exit code" "${work_dir}/lua_load_official_chunk.code" "${work_dir}/glua_load_actual_chunk.code"

run_capture "${work_dir}/luac_version" "${luac_bin}" -v
run_capture "${work_dir}/gluac_version" "${gluac_bin}" -v
compare_files "luac -v exit code" "${work_dir}/luac_version.code" "${work_dir}/gluac_version.code"
check_version_output "gluac -v" "${work_dir}/gluac_version"

exit "${status}"
