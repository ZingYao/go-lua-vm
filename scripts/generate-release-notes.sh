#!/usr/bin/env bash
set -euo pipefail

output_path="${1:-release-notes.md}"
tag_name="${GITHUB_REF_NAME:-}"
current_ref="${GITHUB_SHA:-HEAD}"

if [[ -z "${tag_name}" ]]; then
  tag_name="$(git describe --tags --exact-match "${current_ref}" 2>/dev/null || git rev-parse --short "${current_ref}")"
fi

previous_tag="$(git describe --tags --abbrev=0 "${tag_name}^" 2>/dev/null || true)"
if [[ -n "${previous_tag}" ]]; then
  log_args=("${previous_tag}..${current_ref}")
  compare_text="${previous_tag}...${tag_name}"
else
  log_args=("${current_ref}")
  compare_text="initial release through ${tag_name}"
fi

repo_slug="${GITHUB_REPOSITORY:-}"
compare_url=""
if [[ -n "${repo_slug}" && -n "${previous_tag}" ]]; then
  compare_url="https://github.com/${repo_slug}/compare/${previous_tag}...${tag_name}"
fi

commit_lines="$(git log --no-merges --format='- %s (%h)' "${log_args[@]}" || true)"
if [[ -z "${commit_lines}" ]]; then
  commit_lines="- No non-merge commits detected for this tag."
fi

{
  echo "# ${tag_name} Release Notes / ${tag_name} 发布说明"
  echo
  echo "## English"
  echo
  echo "This release publishes full-feature CLI artifacts for non-macOS targets built with \`CGO_ENABLED=1\` and the \`native_modules\` build tag, so native Lua C module loading is included in those release builds. macOS native artifacts are built locally and uploaded separately."
  echo
  echo "- Range: ${compare_text}"
  if [[ -n "${compare_url}" ]]; then
    echo "- Compare: ${compare_url}"
  fi
  echo
  echo "### Highlights"
  echo
  echo "- Improved beyond stock Lua 5.3 as a pure-Go VM distribution: compatible Lua 5.3 behavior is paired with embeddable Go APIs, \`glua\`/\`gluac\`/\`gluals\` command-line tools, DAP debugging, editor integration, bytecode inspection, formatting, and native Lua C module loading in native_modules builds."
  echo "- Improved execution efficiency across the compiler, VM, CLI, and release build paths, while keeping GLua event correctness by disabling Lua closure fast paths only when registered events must observe lifecycle frames."
  echo "- Added and refined editor extension support for VS Code and JetBrains, including completion, diagnostics, formatting, debugging, DAP variable editing, source navigation, settings, and localized UI text."
  echo "- Added new GLua syntax sugar and language-server awareness for \`const\`, \`continue\`, \`switch/case/default\`, and conditional extension modes selected by syntax sets such as \`lua53\`, \`extended\`, \`all\`, or comma-separated extension names."
  echo "- Added GLua extension methods: \`setFunctionEvent\`, \`setFunctionEventAsync\`, \`callFunctionEvent\`, \`callFunctionEventAsync\`, \`setProgressEvent\`, \`setProgressEventAsync\`, \`callProgressEvent\`, and \`callProgressEventAsync\`, with preset \`events.function_*\` and \`events.progress_*\` constants including call/return/error/exit and line/start/end/error/exit."
  echo "- Added multilingual CLI help and documentation output. Only English and Chinese are supported; choose output with \`GLUA_LANG=en\` or \`GLUA_LANG=zh-CN\` (the tools also follow \`LC_ALL\`, \`LC_MESSAGES\`, and \`LANG\` when \`GLUA_LANG\` is not set)."
  echo "- Release CLI artifacts are native_modules builds for Linux, Windows, and Android; macOS native_modules packages are produced from a local Mac build."
  echo
  echo "### Changes"
  echo
  printf '%s\n' "${commit_lines}"
  echo
  echo "## 中文"
  echo
  echo "本次发布会为非 macOS 目标生成全功能 CLI 产物，构建时启用 \`CGO_ENABLED=1\` 和 \`native_modules\` build tag，因此这些发布版本包含 Lua C 原生模块加载能力。macOS native 产物由本机编译后单独上传。"
  echo
  echo "- 范围：${compare_text}"
  if [[ -n "${compare_url}" ]]; then
    echo "- 对比：${compare_url}"
  fi
  echo
  echo "### 更新重点"
  echo
  echo "- 在官方 Lua 5.3 行为兼容基础上提供纯 Go VM 发行能力：同时包含可嵌入 Go API、\`glua\`/\`gluac\`/\`gluals\` 命令行工具、DAP 调试、编辑器集成、字节码查看、格式化，以及 native_modules 构建中的 Lua C 原生模块加载。"
  echo "- 提升 compiler、VM、CLI 与发布构建链路的执行效率；同时在注册 GLua event 后只关闭会影响生命周期观测的 Lua closure 快路径，保证事件正确性。"
  echo "- 完善 VS Code 与 JetBrains 扩展支持，覆盖补全、诊断、格式化、调试、DAP 变量修改、源码跳转、设置项和界面多语言。"
  echo "- 新增 GLua 语法糖与语言服务语义支持，包括 \`const\`、\`continue\`、\`switch/case/default\`，并支持通过 \`lua53\`、\`extended\`、\`all\` 或逗号分隔扩展名选择条件扩展模式。"
  echo "- 新增 GLua 扩展方法：\`setFunctionEvent\`、\`setFunctionEventAsync\`、\`callFunctionEvent\`、\`callFunctionEventAsync\`、\`setProgressEvent\`、\`setProgressEventAsync\`、\`callProgressEvent\`、\`callProgressEventAsync\`；预设 \`events.function_*\` 和 \`events.progress_*\` 常量，覆盖 call/return/error/exit 与 line/start/end/error/exit。"
  echo "- 增加 CLI 工具的多语言帮助与文档输出。目前仅支持英文和中文；可使用 \`GLUA_LANG=en\` 或 \`GLUA_LANG=zh-CN\` 选择输出语言，未设置 \`GLUA_LANG\` 时会读取 \`LC_ALL\`、\`LC_MESSAGES\` 和 \`LANG\`。"
  echo "- Linux、Windows 和 Android 的 CLI 发布产物均为 native_modules 构建；macOS native_modules 包由本机 Mac 构建补充。"
  echo
  echo "### 变更列表"
  echo
  printf '%s\n' "${commit_lines}"
} >"${output_path}"

echo "generated release notes: ${output_path}"
