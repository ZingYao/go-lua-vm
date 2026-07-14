#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/scripts/native-cross-targets.sh"
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
target_list="${NATIVE_CROSS_TARGETS:-}"
build_root="${BUILD_ROOT:-${repo_root}/build/native-cross}"
require_all="${NATIVE_CROSS_REQUIRE_ALL:-0}"

echo "native cross compile check"
echo "repo_root=${repo_root}"
echo "host_GOOS=${host_goos}"
echo "host_GOARCH=${host_goarch}"
echo "CGO_ENABLED=1"
echo "build_root=${build_root}"
echo "NATIVE_CROSS_REQUIRE_ALL=${require_all}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native cross checks" >&2
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

  # 允许 NATIVE_CC_* / CC 传入带参数的编译器命令，例如：
  # NATIVE_CC_LINUX_ARM64="zig cc -target aarch64-linux-musl"
  # command -v 只校验第一个可执行文件，完整命令仍原样交给 Go/cgo 的 CC。
  read -r cc_executable _ <<< "${cc_command}"
  echo "${cc_executable}"
}

status=0
compiled_count=0
skipped_count=0
for target in "${targets[@]}"; do
  target_goos="$(native_target_goos "${target}")"
  target_goarch="$(native_target_goarch "${target}")"
  target_goarm="$(native_target_goarm "${target}")"
  target_name="$(native_target_name "${target}")"
  output_dir="${build_root}/${target_name}"
  test_output="${output_dir}/internal-native.test"
  glua_output="${output_dir}/glua-native"
  cc_var="$(native_target_cc_var "${target_goos}" "${target_goarch}" "${target_goarm}")"

  if [[ "${target_goos}" == "windows" ]]; then
    test_output="${test_output}.exe"
    glua_output="${glua_output}.exe"
  fi

  echo
  echo "target GOOS=${target_goos} GOARCH=${target_goarch}"
  if [[ -n "${target_goarm}" ]]; then
    echo "target GOARM=${target_goarm}"
  fi
  echo "CC variable=${cc_var}"
  echo "test_output=${test_output}"
  echo "glua_output=${glua_output}"

  if ! cc="$(target_cc_for "${target_goos}" "${target_goarch}" "${target_goarm}")"; then
    echo "skip: no C compiler configured for ${target_goos}/${target_goarch}; set ${cc_var} or CC" >&2
    skipped_count=$((skipped_count + 1))
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
    skipped_count=$((skipped_count + 1))
    if [[ "${require_all}" == "1" ]]; then
      echo "required target unavailable: ${target_goos}/${target_goarch}" >&2
      status=1
    fi
    continue
  fi

  mkdir -p "${output_dir}"
  go_env=(CGO_ENABLED=1 GOOS="${target_goos}" GOARCH="${target_goarch}" CC="${cc}")
  if [[ -n "${target_goarm}" ]]; then
    go_env+=(GOARM="${target_goarm}")
  fi

  if ! env "${go_env[@]}" go test -c -o "${test_output}" ./internal/native; then
    echo "native internal test compile failed for ${target_goos}/${target_goarch}" >&2
    status=1
    continue
  fi

  if ! env "${go_env[@]}" go build -trimpath -o "${glua_output}" ./cmd/glua; then
    echo "native glua compile failed for ${target_goos}/${target_goarch}" >&2
    status=1
    continue
  fi

  echo "compiled ${test_output}"
  echo "compiled ${glua_output}"
  compiled_count=$((compiled_count + 1))
done

echo
echo "native cross compile summary: compiled=${compiled_count} skipped=${skipped_count} targets=${#targets[@]}"

exit "${status}"
