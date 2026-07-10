#!/usr/bin/env bash
set -euo pipefail

# 解析仓库根目录，允许从任意工作目录运行。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
glua_binary="${GLUA_BIN:-${repository_root}/bin/glua}"
utility_script="${repository_root}/tests/extensions/utility_api.glua"

# 先验证可执行文件，避免把构建环境问题误报为扩展回归。
if [[ ! -x "${glua_binary}" ]]; then
  printf 'GLua executable not found: %s\n' "${glua_binary}" >&2
  exit 2
fi

# 固定工作目录并验证脚本完成标记。
cd "${repository_root}"
utility_output="$(${glua_binary} "${utility_script}")"
printf '%s\n' "${utility_output}"
grep -Fxq 'GLUA_UTILITY_API_OK' <<<"${utility_output}"
