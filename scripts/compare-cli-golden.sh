#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lua_bin="${LUA_BIN:-lua}"
glua_bin="${GLUA_BIN:-${repo_root}/bin/glua}"
case_dir="${CASE_DIR:-${repo_root}/tests/compat/cases}"
golden_root="${GOLDEN_DIR:-${repo_root}/tests/compat/golden}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-compat.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

if [[ ! -x "${glua_bin}" ]]; then
  echo "glua binary not found or not executable: ${glua_bin}" >&2
  echo "build glua first or set GLUA_BIN=/path/to/glua" >&2
  exit 1
fi

if ! command -v "${lua_bin}" >/dev/null 2>&1; then
  echo "official lua binary not found on PATH: ${lua_bin}" >&2
  echo "set LUA_BIN=/path/to/lua5.3" >&2
  exit 1
fi

if [[ ! -d "${case_dir}" ]]; then
  echo "compat case directory not found: ${case_dir}" >&2
  exit 1
fi

mkdir -p "${golden_root}/stdout" "${golden_root}/stderr" "${golden_root}/exitcode"

status=0
while IFS= read -r -d '' case_file; do
  case_name="$(basename "${case_file}" .lua)"
  lua_stdout="${work_dir}/${case_name}.lua.out"
  lua_stderr="${work_dir}/${case_name}.lua.err"
  lua_code="${work_dir}/${case_name}.lua.code"
  glua_stdout="${work_dir}/${case_name}.glua.out"
  glua_stderr="${work_dir}/${case_name}.glua.err"
  glua_code="${work_dir}/${case_name}.glua.code"

  set +e
  "${lua_bin}" "${case_file}" >"${lua_stdout}" 2>"${lua_stderr}"
  echo "$?" >"${lua_code}"
  "${glua_bin}" "${case_file}" >"${glua_stdout}" 2>"${glua_stderr}"
  echo "$?" >"${glua_code}"
  set -e

  stdout_golden="${golden_root}/stdout/${case_name}.out"
  stderr_golden="${golden_root}/stderr/${case_name}.err"
  exitcode_golden="${golden_root}/exitcode/${case_name}.code"

  if [[ ! -f "${stdout_golden}" ]]; then
    cp "${lua_stdout}" "${stdout_golden}"
  fi

  if [[ ! -f "${stderr_golden}" ]]; then
    cp "${lua_stderr}" "${stderr_golden}"
  fi

  if [[ ! -f "${exitcode_golden}" ]]; then
    cp "${lua_code}" "${exitcode_golden}"
  fi

  if ! diff -u "${stdout_golden}" "${glua_stdout}"; then
    echo "stdout mismatch: ${case_file}" >&2
    status=1
  fi

  if ! diff -u "${stderr_golden}" "${glua_stderr}"; then
    echo "stderr mismatch: ${case_file}" >&2
    status=1
  fi

  if ! diff -u "${exitcode_golden}" "${glua_code}"; then
    echo "exit code mismatch: ${case_file}" >&2
    status=1
  fi
done < <(find "${case_dir}" -type f -name '*.lua' -print0 | sort -z)

exit "${status}"
