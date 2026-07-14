#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-lpeg/${target_goos}-${target_goarch}}"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
module_extension="${MODULE_EXTENSION:-.so}"
good_result="${GOOD_RESULT:-18}"
low_line="${LOW:-620}"
high_line="${HIGH:-651}"
max_invalid_scan="${MAX_INVALID_SCAN:-12}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-lpeg-bisect.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native LPeg 1159 prefix bisect"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "GLUA_BIN=${glua_bin}"
echo "BUILD_DIR=${build_dir}"
echo "LOW=${low_line}"
echo "HIGH=${high_line}"
echo "GOOD_RESULT=${good_result}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native LPeg bisect" >&2
  exit 1
fi

case "${target_goos}" in
  darwin|linux)
    ;;
  windows)
    echo "skip: Windows LPeg bisect requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native LPeg bisect target GOOS=${target_goos}" >&2
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

last_probe_line=0
last_probe_value=""
last_probe_class=""

probe_line() {
  local line="$1"
  local source="${work_dir}/prefix_${line}.lua"
  local output="${work_dir}/prefix_${line}.out"

  {
    printf 'package.path = "%s"\n' "${repo_root}/third_party/lpeg/?.lua"
    printf 'package.cpath = "%s"\n' "${build_dir}/?${module_extension}"
    sed "1,4d;$((line + 1)),\$d" "${repo_root}/third_party/lpeg/test.lua"
    emit_probe_tail
  } >"${source}"

  last_probe_line="${line}"
  last_probe_value=""
  last_probe_class="invalid"

  if ! "${glua_bin}" "${source}" >"${output}" 2>&1; then
    echo "probe line=${line} invalid: glua exited non-zero"
    tail -n 2 "${output}" | sed 's/^/  /'
    return 2
  fi

  last_probe_value="$(awk '/^PROBE[[:space:]]/{print $2; exit}' "${output}")"
  if [[ -z "${last_probe_value}" ]]; then
    echo "probe line=${line} invalid: no PROBE output"
    tail -n 2 "${output}" | sed 's/^/  /'
    return 2
  fi

  if [[ "${last_probe_value}" == "${good_result}" ]]; then
    last_probe_class="good"
    echo "probe line=${line} result=${last_probe_value} class=good"
    return 0
  fi

  last_probe_class="bad"
  echo "probe line=${line} result=${last_probe_value} class=bad"
  return 1
}

probe_valid_near() {
  local preferred="$1"
  local low="$2"
  local high="$3"
  local offset
  local candidate

  for ((offset = 0; offset <= max_invalid_scan; offset++)); do
    candidate=$((preferred - offset))
    if ((candidate > low && candidate < high)); then
      set +e
      probe_line "${candidate}"
      local rc=$?
      if ((rc != 2)); then
        return "${rc}"
      fi
      set -e
    fi

    if ((offset == 0)); then
      continue
    fi

    candidate=$((preferred + offset))
    if ((candidate > low && candidate < high)); then
      set +e
      probe_line "${candidate}"
      local rc=$?
      if ((rc != 2)); then
        return "${rc}"
      fi
      set -e
    fi
  done

  echo "no valid probe line found near ${preferred} within ${max_invalid_scan} lines" >&2
  return 2
}

set +e
probe_line "${low_line}"
low_rc=$?
set -e
if ((low_rc != 0)); then
  echo "LOW must be a known-good prefix line; got class=${last_probe_class} value=${last_probe_value}" >&2
  exit 1
fi

set +e
probe_line "${high_line}"
high_rc=$?
set -e
if ((high_rc != 1)); then
  echo "HIGH must be a known-bad prefix line; got class=${last_probe_class} value=${last_probe_value}" >&2
  exit 1
fi

while ((high_line - low_line > 1)); do
  mid_line=$(((low_line + high_line) / 2))
  echo "bisect interval good=${low_line} bad=${high_line} mid=${mid_line}"

  set +e
  probe_valid_near "${mid_line}" "${low_line}" "${high_line}"
  mid_rc=$?
  set -e

  if ((mid_rc == 0)); then
    low_line="${last_probe_line}"
  elif ((mid_rc == 1)); then
    high_line="${last_probe_line}"
  else
    echo "bisect failed: no valid midpoint between ${low_line} and ${high_line}" >&2
    exit 1
  fi
done

echo "first_bad_prefix_line=${high_line}"
echo "last_good_prefix_line=${low_line}"
