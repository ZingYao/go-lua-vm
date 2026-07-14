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
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-lpeg-call-kinds.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native LPeg 1159 call-kind probe"
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
  echo "ensure PATH resolves go to ${expected_go_version} before running native LPeg call-kind probe" >&2
  exit 1
fi

case "${target_goos}" in
  darwin|linux)
    ;;
  windows)
    echo "skip: Windows LPeg call-kind probe requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native LPeg call-kind target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

export CGO_ENABLED=1

if [[ -z "${GLUA_BIN:-}" ]]; then
  mkdir -p "$(dirname "${glua_bin}")"
  echo "build native glua: ${glua_bin}"
  go build -trimpath -o "${glua_bin}" ./cmd/glua
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
    define-local-no-call)
      cat <<'LUA'
local function diag()
end
if diag == nil then
  error("local function missing")
end
LUA
      ;;
    anonymous-assign-no-call)
      cat <<'LUA'
local diag = function()
  return "unused"
end
if diag == nil then
  error("anonymous function missing")
end
LUA
      ;;
    table-field-read-no-call)
      cat <<'LUA'
local holder = {
  diag = function()
    return "unused"
  end,
}
local diag = holder.diag
if diag == nil then
  error("table function missing")
end
LUA
      ;;
    lua-empty-call)
      cat <<'LUA'
local function diag()
end
diag()
LUA
      ;;
    lua-local-only-call)
      cat <<'LUA'
local function diag()
  local first = 17
  local second = first + 25
  if second ~= 42 then
    error("unexpected local result")
  end
end
diag()
LUA
      ;;
    builtin-type-call)
      cat <<'LUA'
local kind = type("alpha")
if kind ~= "string" then
  error("unexpected type result")
end
LUA
      ;;
    builtin-select-call)
      cat <<'LUA'
local count = select("#", "alpha", "beta")
if count ~= 2 then
  error("unexpected select result")
end
LUA
      ;;
    builtin-select-count-empty-call)
      cat <<'LUA'
local count = select("#")
if count ~= 0 then
  error("unexpected empty select count")
end
LUA
      ;;
    builtin-select-index-call)
      cat <<'LUA'
local first, second = select(1, "alpha", "beta")
if first ~= "alpha" or second ~= "beta" then
  error("unexpected select index result")
end
LUA
      ;;
    builtin-tostring-call)
      cat <<'LUA'
local text = tostring(42)
if text ~= "42" then
  error("unexpected tostring result")
end
LUA
      ;;
    builtin-pcall-empty-call)
      cat <<'LUA'
local ok, err = pcall(function()
end)
if not ok or err ~= nil then
  error("unexpected pcall result")
end
LUA
      ;;
    lpeg-parity-match-call)
      cat <<'LUA'
p = m.P{
  [1] = '0' * m.V(2) + '1' * m.V(3) + -1,
  [2] = '0' * m.V(1) + '1' * m.V(4),
  [3] = '0' * m.V(4) + '1' * m.V(1),
  [4] = '0' * m.V(3) + '1' * m.V(2),
}
if not p:match(string.rep("00", 10000)) then
  error("unexpected parity match result")
end
LUA
      ;;
    *)
      echo "unknown call-kind probe mode: ${mode}" >&2
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
    tail -n 2 "${output}" | sed 's/^/  /'
    return 1
  fi

  result="$(awk '/^PROBE[[:space:]]/{print $2; exit}' "${output}")"
  if [[ -z "${result}" ]]; then
    echo "mode=${mode} class=invalid"
    tail -n 2 "${output}" | sed 's/^/  /'
    return 1
  fi

  if [[ "${result}" == "${good_result}" ]]; then
    class="good"
  else
    class="bad"
  fi

  echo "mode=${mode} result=${result} class=${class}"
}

if [[ -n "${MODES:-}" ]]; then
  read -r -a modes <<<"${MODES}"
else
  modes=(
    baseline
    define-local-no-call
    anonymous-assign-no-call
    table-field-read-no-call
    lua-empty-call
    lua-local-only-call
    builtin-type-call
    builtin-select-call
    builtin-select-count-empty-call
    builtin-select-index-call
    builtin-tostring-call
    builtin-pcall-empty-call
    lpeg-parity-match-call
  )
fi

for mode in "${modes[@]}"; do
  probe_mode "${mode}"
done
