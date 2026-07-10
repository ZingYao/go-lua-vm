#!/usr/bin/env bash
set -euo pipefail

# 解析仓库根目录，允许从任意工作目录运行脚本。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
glua_binary="${GLUA_BIN:-${repository_root}/bin/glua}"
serialization_script="${repository_root}/tests/serialization/serialization.glua"

# 先检查可执行文件，避免把构建环境问题误判为编解码失败。
if [[ ! -x "${glua_binary}" ]]; then
  printf 'GLua executable not found: %s\n' "${glua_binary}" >&2
  exit 2
fi

# 固定工作目录并验证完成标记。
cd "${repository_root}"
serialization_output="$(${glua_binary} "${serialization_script}")"
printf '%s\n' "${serialization_output}"
grep -Fxq 'GLUA_SERIALIZATION_OK' <<<"${serialization_output}"
