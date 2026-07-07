#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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
  append_target "${host_goos}/${host_goarch}"
  append_target "linux/${host_goarch}"
  append_target "darwin/${host_goarch}"
  append_target "windows/${host_goarch}"
fi

normalize_env_name() {
  echo "$1" | tr '[:lower:]/-' '[:upper:]__'
}

target_cc_for() {
  local target_goos="$1"
  local target_goarch="$2"
  local env_suffix
  local env_name
  local env_value

  env_suffix="$(normalize_env_name "${target_goos}_${target_goarch}")"
  env_name="NATIVE_CC_${env_suffix}"
  env_value="${!env_name:-}"
  if [[ -n "${env_value}" ]]; then
    echo "${env_value}"
    return 0
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
  target_goos="${target%%/*}"
  target_goarch="${target##*/}"
  output_dir="${build_root}/${target_goos}-${target_goarch}"
  test_output="${output_dir}/internal-native.test"
  glua_output="${output_dir}/glua-native"
  cc_var="NATIVE_CC_$(normalize_env_name "${target_goos}_${target_goarch}")"

  if [[ "${target_goos}" == "windows" ]]; then
    test_output="${test_output}.exe"
    glua_output="${glua_output}.exe"
  fi

  echo
  echo "target GOOS=${target_goos} GOARCH=${target_goarch}"
  echo "CC variable=${cc_var}"
  echo "test_output=${test_output}"
  echo "glua_output=${glua_output}"

  if ! cc="$(target_cc_for "${target_goos}" "${target_goarch}")"; then
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

  if ! CGO_ENABLED=1 GOOS="${target_goos}" GOARCH="${target_goarch}" CC="${cc}" \
    go test -c -tags native_modules -o "${test_output}" ./internal/native; then
    echo "native internal test compile failed for ${target_goos}/${target_goarch}" >&2
    status=1
    continue
  fi

  if ! CGO_ENABLED=1 GOOS="${target_goos}" GOARCH="${target_goarch}" CC="${cc}" \
    go build -tags native_modules -trimpath -o "${glua_output}" ./cmd/glua; then
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
