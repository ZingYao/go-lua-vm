#!/usr/bin/env bash
set -euo pipefail

# 结构体字段会影响第三方 Go 调用方的复合字面量和序列化约定，因此单独做稳定快照校验。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
expected_surface="${repository_root}/scripts/public-go-api-fields.txt"
actual_surface="$(mktemp "${TMPDIR:-/tmp}/glua-public-go-api-fields.XXXXXX")"
trap 'rm -f "${actual_surface}"' EXIT

while IFS= read -r type_name; do
  [[ -z "${type_name}" || "${type_name}" == \#* ]] && continue
  go -C "${repository_root}" doc "github.com/ZingYao/go-lua-vm/lua.${type_name}" |
    awk -v type_name="${type_name}" '/^\t[A-Z][A-Za-z0-9_]* / { print type_name "." $1 " " $2 }'
done <<'TYPES' | LC_ALL=C sort -u >"${actual_surface}"
Options
ProgressEventOptions
ProgressEventOptionsPatch
ProgressEventListener
ProgressEventSummary
ProgressEventTrace
TYPES

if ! diff -u "${expected_surface}" "${actual_surface}"; then
  echo "public lua API struct fields changed; review compatibility and update scripts/public-go-api-fields.txt intentionally" >&2
  exit 1
fi

printf 'GLUA_PUBLIC_GO_API_FIELDS_OK\n'
