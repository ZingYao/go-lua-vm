#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
suite_dir="${repo_root}/third_party/lua-5.3.6/testes"
glua_bin="${GLUA_BIN:-${repo_root}/bin/glua}"

if [[ ! -d "${suite_dir}" ]]; then
  echo "official Lua 5.3 test suite not found: ${suite_dir}" >&2
  exit 1
fi

if [[ ! -x "${glua_bin}" ]]; then
  echo "glua binary not found or not executable: ${glua_bin}" >&2
  echo "build glua first or set GLUA_BIN=/path/to/glua" >&2
  exit 1
fi

cd "${suite_dir}"

if [[ $# -gt 0 ]]; then
  for test_file in "$@"; do
    "${glua_bin}" "${test_file}"
  done
  exit 0
fi

"${glua_bin}" all.lua
