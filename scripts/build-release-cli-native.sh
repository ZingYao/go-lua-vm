#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/scripts/native-cross-targets.sh"

target_name="${1:?usage: scripts/build-release-cli-native.sh <target-name> <goos> <goarch> [goarm]}"
target_goos="${2:?usage: scripts/build-release-cli-native.sh <target-name> <goos> <goarch> [goarm]}"
target_goarch="${3:?usage: scripts/build-release-cli-native.sh <target-name> <goos> <goarch> [goarm]}"
target_goarm="${4:-}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before building release artifacts" >&2
  exit 1
fi

cc_var="$(native_target_cc_var "${target_goos}" "${target_goarch}" "${target_goarm}")"
cc_value="${!cc_var:-}"
if [[ -z "${cc_value}" ]]; then
  echo "${cc_var} is required for the full-feature CGO release build" >&2
  exit 1
fi

cc_executable="${cc_value%% *}"
if ! command -v "${cc_executable}" >/dev/null 2>&1; then
  echo "C compiler executable not found for ${target_name}: ${cc_value}" >&2
  exit 1
fi

dist_root="${DIST_ROOT:-${repo_root}/dist}"
output_dir="${dist_root}/${target_name}"
mkdir -p "${output_dir}"

exe=""
if [[ "${target_goos}" == "windows" ]]; then
  exe=".exe"
fi

go_env=(
  CGO_ENABLED=1
  GOOS="${target_goos}"
  GOARCH="${target_goarch}"
  CC="${cc_value}"
)
if [[ -n "${target_goarm}" ]]; then
  go_env+=(GOARM="${target_goarm}")
fi

echo "full-feature release build"
echo "target=${target_name}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
if [[ -n "${target_goarm}" ]]; then
  echo "GOARM=${target_goarm}"
fi
echo "CGO_ENABLED=1"
echo "CC=${cc_value}"
echo "custom_tags=none"

for cmd in glua gluac gluals; do
  env "${go_env[@]}" go build -trimpath -ldflags="-s -w" -o "${output_dir}/${cmd}${exe}" "./cmd/${cmd}"
done

# 发布目录必须携带非商业许可证和商业授权说明，避免二进制脱离仓库后丢失授权边界。
cp "${repo_root}/LICENSE" "${output_dir}/LICENSE"
cp "${repo_root}/COMMERCIAL_LICENSE.md" "${output_dir}/COMMERCIAL_LICENSE.md"
