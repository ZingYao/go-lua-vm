#!/usr/bin/env bash
set -euo pipefail

# 解析仓库根目录，允许从任意工作目录调用。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
glua_binary="${GLUA_BIN:-${repository_root}/bin/glua}"
normal_script="${repository_root}/tests/events/event_api.glua"
error_script="${repository_root}/tests/events/event_error.glua"
management_script="${repository_root}/tests/events/event_management.glua"

# 先校验可执行文件，避免将环境错误误报成事件回归。
if [[ ! -x "${glua_binary}" ]]; then
  printf 'GLua executable not found: %s\n' "${glua_binary}" >&2
  exit 2
fi

# 固定工作目录，使测试脚本中的跨文件模块路径不受调用位置影响。
cd "${repository_root}"

# 正常路径必须完整通过并输出完成标记。
normal_output="$(${glua_binary} "${normal_script}")"
printf '%s\n' "${normal_output}"
grep -Fxq 'GLUA_EVENT_API_OK' <<<"${normal_output}"
grep -Fq 'EVENT_CALLBACK' <<<"${normal_output}"

# 监听器治理、只读上下文与 pcall 行为必须完整通过。
management_output="$(${glua_binary} "${management_script}")"
printf '%s\n' "${management_output}"
grep -Fxq 'GLUA_EVENT_MANAGEMENT_OK' <<<"${management_output}"

# 文件错误事件必须被观察到，同时脚本本身仍按 Lua 错误语义失败。
set +e
error_output="$(${glua_binary} "${error_script}" 2>&1)"
error_status=$?
set -e
printf '%s\n' "${error_output}"
if [[ ${error_status} -eq 0 ]]; then
  printf 'expected event_error.glua to fail\n' >&2
  exit 1
fi
grep -Fq 'GLUA_EVENT_FILE_ERROR_OK' <<<"${error_output}"
grep -Fq 'GLUA_EVENT_FILE_EXIT_OK' <<<"${error_output}"
grep -Fq 'GLUA_EVENT_FUNCTION_ERROR_OK' <<<"${error_output}"
grep -Fq 'GLUA_EVENT_FUNCTION_EXIT_OK' <<<"${error_output}"
grep -Fq 'expected file event failure' <<<"${error_output}"
grep -Fq 'EVENT_CALLBACK' <<<"${error_output}"

printf 'GLUA_EVENT_SUITE_OK\n'
