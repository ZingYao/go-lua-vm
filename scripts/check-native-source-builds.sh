#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/scripts/native-cross-targets.sh"
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
target_list="${NATIVE_SOURCE_BUILD_TARGETS:-${NATIVE_CROSS_TARGETS:-}}"
build_root="${BUILD_ROOT:-${repo_root}/build/native-source-builds}"
require_all="${NATIVE_SOURCE_REQUIRE_ALL:-0}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-source-builds.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native source build matrix"
echo "repo_root=${repo_root}"
echo "host_GOOS=${host_goos}"
echo "host_GOARCH=${host_goarch}"
echo "CGO_ENABLED=1"
echo "build_root=${build_root}"
echo "NATIVE_SOURCE_REQUIRE_ALL=${require_all}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native source builds" >&2
  exit 1
fi

targets=()
append_target() {
  local target="$1"
  local existing
  if [[ "${#targets[@]}" -gt 0 ]]; then
    for existing in "${targets[@]}"; do
      if [[ "${existing}" == "${target}" ]]; then
        return 0
      fi
    done
  fi
  targets+=("${target}")
}

if [[ -n "${target_list}" ]]; then
  for target in ${target_list}; do
    append_target "${target}"
  done
else
  while IFS= read -r target; do
    append_target "${target}"
  done < <(native_release_targets)
fi

normalize_env_name() {
  echo "$1" | tr '[:lower:]/-' '[:upper:]__'
}

target_cc_for() {
  local target_goos="$1"
  local target_goarch="$2"
  local target_goarm="${3:-}"
  local env_name
  local fallback_env_name
  local env_value

  env_name="$(native_target_cc_var "${target_goos}" "${target_goarch}" "${target_goarm}")"
  env_value="${!env_name:-}"
  if [[ -n "${env_value}" ]]; then
    echo "${env_value}"
    return 0
  fi

  if [[ "${target_goarch}" == "arm" && -n "${target_goarm}" ]]; then
    fallback_env_name="$(native_target_cc_var "${target_goos}" "${target_goarch}")"
    env_value="${!fallback_env_name:-}"
    if [[ -n "${env_value}" ]]; then
      echo "${env_value}"
      return 0
    fi
  fi

  if [[ "${target_goos}" == "${host_goos}" && "${target_goarch}" == "${host_goarch}" ]]; then
    echo "${CC:-cc}"
    return 0
  fi

  if [[ -n "${CC+x}" ]]; then
    echo "${CC}"
    return 0
  fi

  return 1
}

cc_executable_for() {
  local cc_command="$1"
  local cc_executable

  read -r cc_executable _ <<<"${cc_command}"
  echo "${cc_executable}"
}

modules=(
  "fixtures:scripts/build-native-fixtures.sh"
  "lua-cjson:scripts/build-native-cjson.sh"
  "lpeg:scripts/build-native-lpeg.sh"
  "luasocket:scripts/build-native-luasocket.sh"
)

module_count="${#modules[@]}"
status=0
built_count=0
skipped_count=0
failed_count=0
total_count=0

run_module_build() {
  local target_goos="$1"
  local target_goarch="$2"
  local target_goarm="$3"
  local cc="$4"
  local module_name="$5"
  local script_path="$6"
  local module_build_dir="$7"
  local target_name
  local output_file

  target_name="$(native_target_name "${target_goos}/${target_goarch}${target_goarm:+/${target_goarm}}")"
  output_file="${work_dir}/${target_name}_${module_name}.log"

  total_count=$((total_count + 1))
  echo "module=${module_name}"
  echo "script=${script_path}"
  echo "build_dir=${module_build_dir}"

  if TARGET_GOOS="${target_goos}" TARGET_GOARCH="${target_goarch}" TARGET_GOARM="${target_goarm}" BUILD_DIR="${module_build_dir}" \
    CGO_ENABLED=1 CC="${cc}" "${script_path}" >"${output_file}" 2>&1; then
    if grep -F "skip:" "${output_file}" >/dev/null; then
      grep -F "skip:" "${output_file}" | sed 's/^/  /'
      skipped_count=$((skipped_count + 1))
      if [[ "${require_all}" == "1" ]]; then
        echo "required source build unavailable: ${target_goos}/${target_goarch} ${module_name}" >&2
        status=1
      fi
      return 0
    fi

    echo "built source module ${module_name} for ${target_goos}/${target_goarch}"
    built_count=$((built_count + 1))
    return 0
  fi

  echo "native source build failed for ${target_goos}/${target_goarch} ${module_name}" >&2
  cat "${output_file}" >&2
  failed_count=$((failed_count + 1))
  status=1
}

for target in "${targets[@]}"; do
  target_goos="$(native_target_goos "${target}")"
  target_goarch="$(native_target_goarch "${target}")"
  target_goarm="$(native_target_goarm "${target}")"
  target_name="$(native_target_name "${target}")"
  output_dir="${build_root}/${target_name}"
  cc_var="$(native_target_cc_var "${target_goos}" "${target_goarch}" "${target_goarm}")"

  echo
  echo "target GOOS=${target_goos} GOARCH=${target_goarch}"
  if [[ -n "${target_goarm}" ]]; then
    echo "target GOARM=${target_goarm}"
  fi
  echo "CC variable=${cc_var}"
  echo "output_dir=${output_dir}"

  if ! cc="$(target_cc_for "${target_goos}" "${target_goarch}" "${target_goarm}")"; then
    echo "skip: no C compiler configured for ${target_goos}/${target_goarch}; set ${cc_var} or CC" >&2
    skipped_count=$((skipped_count + module_count))
    total_count=$((total_count + module_count))
    if [[ "${require_all}" == "1" ]]; then
      echo "required target unavailable: ${target_goos}/${target_goarch}" >&2
      status=1
    fi
    continue
  fi

  echo "CC=${cc}"
  cc_executable="$(cc_executable_for "${cc}")"
  if ! command -v "${cc_executable}" >/dev/null 2>&1; then
    echo "skip: C compiler not found for ${target_goos}/${target_goarch}: ${cc}" >&2
    skipped_count=$((skipped_count + module_count))
    total_count=$((total_count + module_count))
    if [[ "${require_all}" == "1" ]]; then
      echo "required target unavailable: ${target_goos}/${target_goarch}" >&2
      status=1
    fi
    continue
  fi

  mkdir -p "${output_dir}"
  for module in "${modules[@]}"; do
    module_name="${module%%:*}"
    script_relative="${module#*:}"
    script_path="${repo_root}/${script_relative}"
    module_build_dir="${output_dir}/${module_name}"
    run_module_build "${target_goos}" "${target_goarch}" "${target_goarm}" "${cc}" "${module_name}" "${script_path}" "${module_build_dir}"
  done
done

echo
echo "native source build summary: built=${built_count} skipped=${skipped_count} failed=${failed_count} modules=${total_count} targets=${#targets[@]}"

exit "${status}"
