#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
def_file="${repo_root}/native/lua53/windows/lua53.def"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-def.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native Windows lua53.def check"
echo "repo_root=${repo_root}"
echo "def_file=${def_file}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native Windows def checks" >&2
  exit 1
fi

if [[ ! -f "${def_file}" ]]; then
  echo "Windows lua53.def file not found: ${def_file}" >&2
  exit 1
fi

generated_file="${work_dir}/lua53.def"

{
  printf '%s\n' '; Lua 5.3 ABI exports provided by the native_modules shim.'
  printf '%s\n' '; Keep this file in sync with scripts/check-native-windows-def.sh.'
  printf '%s\n' 'LIBRARY lua53.dll'
  printf '%s\n' 'EXPORTS'
  {
    rg -o '^//export lua(L)?_[A-Za-z0-9_]+' "${repo_root}/internal/native" \
      | awk '{print $2}'
    rg -o '^(const[[:space:]]+char[[:space:]]*\*[[:space:]]*|void[[:space:]]+|int[[:space:]]+)lua(L)?_[A-Za-z0-9_]+[[:space:]]*\(' "${repo_root}/internal/native" \
      | sed -E 's/.*(lua(L)?_[A-Za-z0-9_]+)[[:space:]]*\(.*/\1/'
  } | sort -u | sed 's/^/  /'
} >"${generated_file}"

if ! diff -u "${generated_file}" "${def_file}"; then
  echo "Windows lua53.def is out of sync with native Lua ABI declarations" >&2
  echo "regenerate ${def_file} from ${generated_file} and review the symbol change" >&2
  exit 1
fi

symbol_count="$(sed -n '/^EXPORTS$/,$p' "${def_file}" | tail -n +2 | sed '/^[[:space:]]*$/d' | wc -l | tr -d ' ')"
echo "native Windows lua53.def check passed"
echo "exported_symbols=${symbol_count}"
