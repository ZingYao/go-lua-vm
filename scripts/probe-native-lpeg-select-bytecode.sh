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
    error-message-no-select)
      cat <<'LUA'
local skipped = error
local message = "unexpected falsy select count"
LUA
      ;;
    select-count-error-local-after)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local skipped = error
LUA
      ;;
    select-count-message-local-after)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local message = "unexpected falsy select count"
LUA
      ;;
    select-count-error-message-locals-after)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local skipped = error
local message = "unexpected falsy select count"
LUA
      ;;
    select-count-message-error-locals-after)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local message = "unexpected falsy select count"
local skipped = error
LUA
      ;;
    select-count-error-message-do-block)
      cat <<'LUA'
do
  local count = select("#", "alpha", "beta")
  local skipped = error
  local message = "unexpected falsy select count"
end
LUA
      ;;
    select-count-error-message-clear-nil)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local skipped = error
local message = "unexpected falsy select count"
skipped = nil
message = nil
LUA
      ;;
    select-count-error-message-clear-false)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local skipped = error
local message = "unexpected falsy select count"
skipped = false
message = false
LUA
      ;;
    select-count-type-message-locals-after)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local skipped = type
local message = "unexpected falsy select count"
LUA
      ;;
    select-count-if-truthy)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
  error("unexpected falsy select count")
end
LUA
      ;;
    select-count-if-not-empty)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
end
LUA
      ;;
    select-count-if-not-skip-loadk)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
  local skipped = 1
end
LUA
      ;;
    select-count-if-not-skip-global)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
  local skipped = error
end
LUA
      ;;
    select-count-if-not-skip-error-locals)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
  local skipped = error
  local message = "unexpected falsy select count"
end
LUA
      ;;
    select-count-if-not-skip-error-local-call)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
  local skipped = error
  skipped("unexpected falsy select count")
end
LUA
      ;;
    select-count-if-not-skip-type-call)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
  type("skipped")
end
LUA
      ;;
    select-count-if-not-skip-assert-false)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if not count then
  assert(false, "unexpected falsy select count")
end
LUA
      ;;
    select-count-if-eq-enter-loadk)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count == 2 then
  local entered = 1
end
LUA
      ;;
    select-count-if-eq-enter-type-call)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count == 2 then
  type("entered")
end
LUA
      ;;
    select-count-if-eq-enter-assert-true)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count == 2 then
  assert(true, "entered")
end
LUA
      ;;
    select-count-if-count-empty)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count then
end
LUA
      ;;
    select-count-if-eq-empty)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count == 2 then
end
LUA
      ;;
    select-count-eq-unused)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local equal = count == 2
LUA
      ;;
    select-count-table-store-count)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local packed = {count}
LUA
      ;;
    select-count-lua-arg)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local function sink(value)
end
sink(count)
LUA
      ;;
    select-count-go-arg-rawequal)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
rawequal(count, 2)
LUA
      ;;
    select-count-arith-add-zero)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local sum = count + 0
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
  error-message-no-select
  select-count-error-local-after
  select-count-message-local-after
  select-count-error-message-locals-after
  select-count-message-error-locals-after
  select-count-error-message-do-block
  select-count-error-message-clear-nil
  select-count-error-message-clear-false
  select-count-type-message-locals-after
  select-count-if-truthy
  select-count-if-not-empty
  select-count-if-not-skip-loadk
  select-count-if-not-skip-global
  select-count-if-not-skip-error-locals
  select-count-if-not-skip-error-local-call
  select-count-if-not-skip-type-call
  select-count-if-not-skip-assert-false
  select-count-if-eq-enter-loadk
  select-count-if-eq-enter-type-call
  select-count-if-eq-enter-assert-true
  select-count-if-count-empty
  select-count-if-eq-empty
  select-count-eq-unused
  select-count-table-store-count
  select-count-lua-arg
  select-count-go-arg-rawequal
  select-count-arith-add-zero
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
