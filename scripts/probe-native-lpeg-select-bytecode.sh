#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-select-bytecode.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "select bytecode shape probe"
echo "repo_root=${repo_root}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running select bytecode probe" >&2
  exit 1
fi

emit_mode_body() {
  local mode="$1"

  case "${mode}" in
    select-count-consume)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count ~= 2 then
  error("unexpected select count")
end
LUA
      ;;
    select-count-discard)
      cat <<'LUA'
select("#", "alpha", "beta")
local marker = 2
if marker ~= 2 then
  error("unexpected marker")
end
LUA
      ;;
    select-count-table-constructor)
      cat <<'LUA'
local values = {select("#", "alpha", "beta")}
if values[1] ~= 2 then
  error("unexpected table select count")
end
LUA
      ;;
    builtin-rawequal-two-strings)
      cat <<'LUA'
local same = rawequal("alpha", "beta")
if same then
  error("unexpected rawequal result")
end
LUA
      ;;
    builtin-tonumber-string-base)
      cat <<'LUA'
local number = tonumber("17", 10)
if number ~= 17 then
  error("unexpected tonumber result")
end
LUA
      ;;
    *)
      echo "unknown select bytecode probe mode: ${mode}" >&2
      return 1
      ;;
  esac
}

modes=(
  select-count-consume
  select-count-discard
  select-count-table-constructor
  builtin-rawequal-two-strings
  builtin-tonumber-string-base
)

if (($# > 0)); then
  modes=("$@")
elif [[ -n "${PROBE_MODES:-}" ]]; then
  read -r -a modes <<<"${PROBE_MODES}"
fi

for mode in "${modes[@]}"; do
  source="${work_dir}/${mode}.lua"
  emit_mode_body "${mode}" >"${source}"
  echo
  echo "mode=${mode}"
  go run ./cmd/glua --glua-list-bytecode "${source}"
done
