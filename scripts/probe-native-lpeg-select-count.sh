#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-lpeg/${target_goos}-${target_goarch}}"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
module_extension="${MODULE_EXTENSION:-.so}"
prefix_line="${PREFIX_LINE:-620}"
good_result="${GOOD_RESULT:-18}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-lpeg-select-count.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native LPeg select-count boundary probe"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "GLUA_BIN=${glua_bin}"
echo "BUILD_DIR=${build_dir}"
echo "PREFIX_LINE=${prefix_line}"
echo "GOOD_RESULT=${good_result}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native LPeg select-count probe" >&2
  exit 1
fi

case "${target_goos}" in
  darwin|linux)
    ;;
  windows)
    echo "skip: Windows LPeg select-count probe requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native LPeg select-count target GOOS=${target_goos}" >&2
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

emit_mode_body() {
  local mode="$1"

  case "${mode}" in
    baseline)
      ;;
    select-count-empty)
      cat <<'LUA'
local count = select("#")
if count ~= 0 then
  error("unexpected empty select count")
end
LUA
      ;;
    select-index-nonempty)
      cat <<'LUA'
local first, second = select(1, "alpha", "beta")
if first ~= "alpha" or second ~= "beta" then
  error("unexpected select index result")
end
LUA
      ;;
    select-count-nonempty)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count ~= 2 then
  error("unexpected non-empty select count")
end
LUA
      ;;
    select-count-inline-if)
      cat <<'LUA'
if select("#", "alpha", "beta") ~= 2 then
  error("unexpected inline select count")
end
LUA
      ;;
    select-count-do-block)
      cat <<'LUA'
do
  local count = select("#", "alpha", "beta")
  if count ~= 2 then
    error("unexpected do-block select count")
  end
end
LUA
      ;;
    select-count-nil-after-use)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count ~= 2 then
  error("unexpected nil-after-use select count")
end
count = nil
LUA
      ;;
    select-count-overwrite-args-nil)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local clear1, clear2 = nil, nil
if count ~= 2 or clear1 ~= nil or clear2 ~= nil then
  error("unexpected nil-overwrite select count")
end
LUA
      ;;
    select-count-overwrite-args-false)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
local clear1, clear2 = false, false
if count ~= 2 or clear1 ~= false or clear2 ~= false then
  error("unexpected false-overwrite select count")
end
LUA
      ;;
    select-count-nonempty-discard)
      cat <<'LUA'
select("#", "alpha", "beta")
LUA
      ;;
    select-count-two-nil)
      cat <<'LUA'
local count = select("#", nil, nil)
if count ~= 2 then
  error("unexpected two-nil select count")
end
LUA
      ;;
    select-count-first-string-second-nil)
      cat <<'LUA'
local count = select("#", "alpha", nil)
if count ~= 2 then
  error("unexpected first-string second-nil select count")
end
LUA
      ;;
    select-count-first-nil-second-string)
      cat <<'LUA'
local count = select("#", nil, "beta")
if count ~= 2 then
  error("unexpected first-nil second-string select count")
end
LUA
      ;;
    select-count-two-false)
      cat <<'LUA'
local count = select("#", false, false)
if count ~= 2 then
  error("unexpected two-false select count")
end
LUA
      ;;
    select-count-first-false-second-nil)
      cat <<'LUA'
local count = select("#", false, nil)
if count ~= 2 then
  error("unexpected first-false second-nil select count")
end
LUA
      ;;
    select-count-first-nil-second-false)
      cat <<'LUA'
local count = select("#", nil, false)
if count ~= 2 then
  error("unexpected first-nil second-false select count")
end
LUA
      ;;
    select-count-three-strings)
      cat <<'LUA'
local count = select("#", "alpha", "beta", "gamma")
if count ~= 3 then
  error("unexpected three-string select count")
end
LUA
      ;;
    select-count-one-string)
      cat <<'LUA'
local count = select("#", "alpha")
if count ~= 1 then
  error("unexpected one-string select count")
end
LUA
      ;;
    select-count-one-number)
      cat <<'LUA'
local count = select("#", 17)
if count ~= 1 then
  error("unexpected one-number select count")
end
LUA
      ;;
    select-count-two-numbers)
      cat <<'LUA'
local count = select("#", 17, 25)
if count ~= 2 then
  error("unexpected two-number select count")
end
LUA
      ;;
    select-count-multivar)
      cat <<'LUA'
local count, extra = select("#", "alpha", "beta")
if count ~= 2 or extra ~= nil then
  error("unexpected multi-var select count")
end
LUA
      ;;
    select-count-table-constructor)
      cat <<'LUA'
local packed = {select("#", "alpha", "beta")}
if #packed ~= 1 or packed[1] ~= 2 then
  error("unexpected table constructor select count")
end
LUA
      ;;
    literal-integer-two)
      cat <<'LUA'
local count = 2
if count ~= 2 then
  error("unexpected literal integer")
end
LUA
      ;;
    table-length-two)
      cat <<'LUA'
local count = #{"alpha", "beta"}
if count ~= 2 then
  error("unexpected table length")
end
LUA
      ;;
    lua-return-integer-two)
      cat <<'LUA'
local function diag()
  return 2
end
local count = diag()
if count ~= 2 then
  error("unexpected Lua integer return")
end
LUA
      ;;
    lua-return-integer-two-after-vararg)
      cat <<'LUA'
local function diag(...)
  return 2
end
local count = diag("alpha", "beta")
if count ~= 2 then
  error("unexpected Lua vararg integer return")
end
LUA
      ;;
    lua-return-select-count-nonempty)
      cat <<'LUA'
local function diag(...)
  return select("#", ...)
end
local count = diag("alpha", "beta")
if count ~= 2 then
  error("unexpected Lua select count return")
end
LUA
      ;;
    builtin-assert-integer-two)
      cat <<'LUA'
local count = assert(2)
if count ~= 2 then
  error("unexpected assert integer return")
end
LUA
      ;;
    builtin-assert-two-values)
      cat <<'LUA'
local first, second = assert(2, "ok")
if first ~= 2 or second ~= "ok" then
  error("unexpected assert multi return")
end
LUA
      ;;
    *)
      echo "unknown select-count probe mode: ${mode}" >&2
      return 1
      ;;
  esac
}

probe_mode() {
  local mode="$1"
  local source="${work_dir}/${mode}.lua"
  local output="${work_dir}/${mode}.out"
  local result
  local class

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;$((prefix_line + 1)),\$d" "${repo_root}/third_party/lpeg/test.lua"
    emit_mode_body "${mode}"
    emit_probe_tail
  } >"${source}"

  if ! "${glua_bin}" "${source}" >"${output}" 2>&1; then
    echo "mode=${mode} class=invalid"
    tail -n 4 "${output}" | sed 's/^/  /'
    return 1
  fi

  result="$(awk '/^PROBE[[:space:]]/{print $2; exit}' "${output}")"
  if [[ -z "${result}" ]]; then
    echo "mode=${mode} class=invalid"
    tail -n 4 "${output}" | sed 's/^/  /'
    return 1
  fi

  if [[ "${result}" == "${good_result}" ]]; then
    class="good"
  else
    class="bad"
  fi

  echo "mode=${mode} result=${result} class=${class}"
}

modes=(
  baseline
  select-count-empty
  select-index-nonempty
  select-count-nonempty
  select-count-inline-if
  select-count-do-block
  select-count-nil-after-use
  select-count-overwrite-args-nil
  select-count-overwrite-args-false
  select-count-nonempty-discard
  select-count-two-nil
  select-count-first-string-second-nil
  select-count-first-nil-second-string
  select-count-two-false
  select-count-first-false-second-nil
  select-count-first-nil-second-false
  select-count-three-strings
  select-count-one-string
  select-count-one-number
  select-count-two-numbers
  select-count-multivar
  select-count-table-constructor
  literal-integer-two
  table-length-two
  lua-return-integer-two
  lua-return-integer-two-after-vararg
  lua-return-select-count-nonempty
  builtin-assert-integer-two
  builtin-assert-two-values
)

for mode in "${modes[@]}"; do
  probe_mode "${mode}"
done
