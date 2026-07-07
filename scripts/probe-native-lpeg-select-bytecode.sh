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

emit_lpeg_selected_decls() {
  cat <<'LUA'
local attempts = {}
local probe_open
local probe_close
local probe_any
local probe_close_head
local probe_close_back
local probe_close_func
local dummy_func
local dummy_capture
local dummy_back
local dummy_value
LUA
}

emit_lpeg_error_number_perturbation() {
  cat <<'LUA'
local count = select("#", "alpha", "beta")
if count ~= 2 then
  error("unexpected select count")
end
local skipped = error
local payload = 17
LUA
}

emit_lpeg_warmup_string_close() {
  cat <<'LUA'
local probe_warmup = ']'
probe_warmup = nil
LUA
}

emit_lpeg_warmup_string_close_after_dead_pad() {
  cat <<'LUA'
if false then
  local probe_pad = 'pad-before-close'
end
local probe_warmup = ']'
probe_warmup = nil
LUA
}

emit_lpeg_warmup_string_close_dead_branch() {
  cat <<'LUA'
local probe_warmup
if false then
  probe_warmup = ']'
end
probe_warmup = nil
LUA
}

emit_lpeg_warmup_string_close_dead_branch_after_dead_pad() {
  cat <<'LUA'
if false then
  local probe_pad = 'pad-before-close'
end
local probe_warmup
if false then
  probe_warmup = ']'
end
probe_warmup = nil
LUA
}

emit_lpeg_warmup_string_close_dead_branch_after_dead_pads8() {
  cat <<'LUA'
if false then
  local probe_pad1 = 'pad-before-close-1'
  local probe_pad2 = 'pad-before-close-2'
  local probe_pad3 = 'pad-before-close-3'
  local probe_pad4 = 'pad-before-close-4'
  local probe_pad5 = 'pad-before-close-5'
  local probe_pad6 = 'pad-before-close-6'
  local probe_pad7 = 'pad-before-close-7'
  local probe_pad8 = 'pad-before-close-8'
end
local probe_warmup
if false then
  probe_warmup = ']'
end
probe_warmup = nil
LUA
}

emit_lpeg_warmup_string_dead_pads8_only() {
  cat <<'LUA'
if false then
  local probe_pad1 = 'pad-before-close-1'
  local probe_pad2 = 'pad-before-close-2'
  local probe_pad3 = 'pad-before-close-3'
  local probe_pad4 = 'pad-before-close-4'
  local probe_pad5 = 'pad-before-close-5'
  local probe_pad6 = 'pad-before-close-6'
  local probe_pad7 = 'pad-before-close-7'
  local probe_pad8 = 'pad-before-close-8'
end
local probe_warmup = nil
probe_warmup = nil
LUA
}

emit_lpeg_warmup_string_close_dead_function() {
  cat <<'LUA'
local function probe_warmup_const()
  return ']'
end
local probe_warmup = nil
LUA
}

emit_lpeg_warmup_string_close_called_function() {
  cat <<'LUA'
local function probe_warmup_const()
  return ']'
end
local probe_warmup = probe_warmup_const()
probe_warmup = nil
LUA
}

emit_lpeg_default_tail() {
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

emit_lpeg_selected_tail() {
  cat <<'LUA'
attempts = {}
if probe_open == nil then
  probe_open = '[' * m.Cg(m.P'='^0, "init") * '['
end
if probe_close == nil then
  if probe_close_head == nil then
    probe_close_head = ']' * m.C(m.P'='^0) * ']'
  end
  if probe_close_back == nil then
    probe_close_back = m.Cb("init")
  end
  if probe_close_func == nil then
    probe_close_func = function (_, pos, s1, s2)
                                               attempts[#attempts + 1] = pos .. ':' .. s1 .. ':' .. s2
                                               return s1 == s2 end
  end
  probe_close = m.Cmt(probe_close_head * probe_close_back, probe_close_func)
end
if probe_any == nil then
  probe_any = m.P(1)
end
c = probe_open * { probe_close + probe_any * m.V(1) } / 0
print("PROBE", c:match'[==[]]====]]]]==]===[]', table.concat(attempts, ","))
LUA
}

emit_lpeg_split_head_tail() {
  cat <<'LUA'
attempts = {}
if probe_open == nil then
  probe_open = '[' * m.Cg(m.P'='^0, "init") * '['
end
if probe_close == nil then
  if probe_close_head == nil then
    if probe_head_left == nil then
      probe_head_left = ']'
    end
    if probe_head_unit == nil then
      probe_head_unit = m.P'='^0
    end
    if probe_head_capture == nil then
      probe_head_capture = m.C(probe_head_unit)
    end
    if probe_head_right == nil then
      probe_head_right = ']'
    end
    probe_close_head = probe_head_left * probe_head_capture * probe_head_right
  end
  if probe_close_back == nil then
    probe_close_back = m.Cb("init")
  end
  if probe_close_func == nil then
    probe_close_func = function (_, pos, s1, s2)
                                               attempts[#attempts + 1] = pos .. ':' .. s1 .. ':' .. s2
                                               return s1 == s2 end
  end
  probe_close = m.Cmt(probe_close_head * probe_close_back, probe_close_func)
end
if probe_any == nil then
  probe_any = m.P(1)
end
c = probe_open * { probe_close + probe_any * m.V(1) } / 0
print("PROBE", c:match'[==[]]====]]]]==]===[]', table.concat(attempts, ","))
LUA
}

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
    fixed-result-select-one-string-error-number)
      cat <<'LUA'
local count = select("#", "alpha")
if count ~= 1 then
  error("unexpected one-string select count")
end
local skipped = error
local payload = 17
LUA
      ;;
    fixed-result-select-two-numbers-error-number)
      cat <<'LUA'
local count = select("#", 17, 25)
if count ~= 2 then
  error("unexpected numeric select count")
end
local skipped = error
local payload = 17
LUA
      ;;
    fixed-result-select-two-booleans-error-number)
      cat <<'LUA'
local count = select("#", true, false)
if count ~= 2 then
  error("unexpected boolean select count")
end
local skipped = error
local payload = 17
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
    fixed-result-rawequal-strings-false-error-number)
      cat <<'LUA'
local count = rawequal("alpha", "beta")
if count ~= false then
  error("unexpected rawequal false result")
end
local skipped = error
local payload = 17
LUA
      ;;
    fixed-result-rawequal-strings-true-error-number)
      cat <<'LUA'
local count = rawequal("alpha", "alpha")
if count ~= true then
  error("unexpected rawequal true result")
end
local skipped = error
local payload = 17
LUA
      ;;
    fixed-result-rawequal-numbers-false-error-number)
      cat <<'LUA'
local count = rawequal(17, 25)
if count ~= false then
  error("unexpected numeric rawequal false result")
end
local skipped = error
local payload = 17
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
    lpeg-default-tail-error-number)
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-selected-decls-default-tail-error-number)
      emit_lpeg_selected_decls
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-selected-tail-only-error-number)
      emit_lpeg_error_number_perturbation
      emit_lpeg_selected_tail
      ;;
    lpeg-decls-only-selected-tail-error-number)
      emit_lpeg_selected_decls
      emit_lpeg_error_number_perturbation
      emit_lpeg_selected_tail
      ;;
    lpeg-split-head-tail-only-error-number)
      emit_lpeg_error_number_perturbation
      emit_lpeg_split_head_tail
      ;;
    lpeg-warmup-string-close-default-tail-error-number)
      emit_lpeg_warmup_string_close
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-warmup-string-close-after-dead-pad-default-tail-error-number)
      emit_lpeg_warmup_string_close_after_dead_pad
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-warmup-string-close-dead-branch-default-tail-error-number)
      emit_lpeg_warmup_string_close_dead_branch
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-warmup-string-close-dead-branch-after-dead-pad-default-tail-error-number)
      emit_lpeg_warmup_string_close_dead_branch_after_dead_pad
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-warmup-string-close-dead-branch-after-dead-pads8-default-tail-error-number)
      emit_lpeg_warmup_string_close_dead_branch_after_dead_pads8
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-warmup-string-dead-pads8-only-default-tail-error-number)
      emit_lpeg_warmup_string_dead_pads8_only
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-warmup-string-close-dead-function-default-tail-error-number)
      emit_lpeg_warmup_string_close_dead_function
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    lpeg-warmup-string-close-called-function-default-tail-error-number)
      emit_lpeg_warmup_string_close_called_function
      emit_lpeg_error_number_perturbation
      emit_lpeg_default_tail
      ;;
    *)
      echo "unknown select bytecode probe mode: ${mode}" >&2
      return 1
      ;;
  esac
}

modes=(
  select-count-consume
  fixed-result-select-one-string-error-number
  fixed-result-select-two-numbers-error-number
  fixed-result-select-two-booleans-error-number
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
  fixed-result-rawequal-strings-false-error-number
  fixed-result-rawequal-strings-true-error-number
  fixed-result-rawequal-numbers-false-error-number
  builtin-tonumber-string-base
  lpeg-default-tail-error-number
  lpeg-selected-decls-default-tail-error-number
  lpeg-selected-tail-only-error-number
  lpeg-decls-only-selected-tail-error-number
  lpeg-split-head-tail-only-error-number
  lpeg-warmup-string-close-default-tail-error-number
  lpeg-warmup-string-close-after-dead-pad-default-tail-error-number
  lpeg-warmup-string-close-dead-branch-default-tail-error-number
  lpeg-warmup-string-close-dead-branch-after-dead-pad-default-tail-error-number
  lpeg-warmup-string-close-dead-branch-after-dead-pads8-default-tail-error-number
  lpeg-warmup-string-dead-pads8-only-default-tail-error-number
  lpeg-warmup-string-close-dead-function-default-tail-error-number
  lpeg-warmup-string-close-called-function-default-tail-error-number
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
