#!/usr/bin/env bash
set -euo pipefail

# 从脚本位置解析仓库根目录，确保本地和 Actions 使用同一入口。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_directory="${repository_root}/docs"
output_directory="${1:-${repository_root}/build/docs-site}"

# 输出目录不能位于源码目录内部，否则复制会递归包含自身。
case "${output_directory}" in
  "${source_directory}"|"${source_directory}/"*)
    printf 'docs output directory must not be inside %s\n' "${source_directory}" >&2
    exit 2
    ;;
esac

# 站点入口、导航和核心公开文档缺一不可。
required_files=(
  index.html
  .nojekyll
  _coverpage.md
  _navbar.md
  _sidebar.md
  README.md
  LICENSING.md
  PLAYGROUND.md
  PERFORMANCE.md
  NATIVE_BUILD_GUIDE.md
  SYNTAX_EXTENSIONS.md
  EXTENSION_APIS.md
  glua-event.md
  glua-serialization.md
  glua-utilities.md
  assets/theme.css
  assets/favicon.svg
  assets/playground.js
  assets/playground-fs.js
  assets/playground-editor-core.js
  assets/playground-worker.js
  assets/prism-glua.js
)

for required_file in "${required_files[@]}"; do
  if [[ ! -e "${source_directory}/${required_file}" ]]; then
    printf 'missing required docs file: %s\n' "${required_file}" >&2
    exit 1
  fi
done

# 固定 Docsify 版本，避免 CDN latest 在无代码变更时改变站点行为。
if ! grep -Fq 'docsify@4.13.1' "${source_directory}/index.html"; then
  printf 'docs/index.html must pin docsify@4.13.1\n' >&2
  exit 1
fi

# Playground 编辑器依赖固定版本 Monaco，避免 CDN 浮动版本改变编辑、补齐或断点行为。
if ! grep -Fq 'monaco-editor@0.55.1' "${source_directory}/index.html"; then
  printf 'docs/index.html must pin monaco-editor@0.55.1\n' >&2
  exit 1
fi

# GLua 代码块必须加载固定版本 Lua 基础语法和项目扩展 grammar。
if ! grep -Fq 'prismjs@1.30.0/components/prism-lua.min.js' "${source_directory}/index.html" ||
  ! grep -Fq 'assets/prism-glua.js' "${source_directory}/index.html"; then
  printf 'docs/index.html must load the pinned GLua Prism grammar\n' >&2
  exit 1
fi

# 文档入口必须在 Docsify 启动前注册 Playground 插件，才能装饰每次路由渲染后的 Lua 代码块。
if ! grep -Fq 'assets/playground.js' "${source_directory}/index.html"; then
  printf 'docs/index.html must load the GLua Playground plugin\n' >&2
  exit 1
fi

# 默认 GitHub Pages 域名不应生成自定义 CNAME。
if [[ -e "${source_directory}/CNAME" ]]; then
  printf 'docs/CNAME is not allowed when using the default GitHub Pages domain\n' >&2
  exit 1
fi

# 导航中的仓库内 Markdown 链接必须指向存在文件。
for navigation_file in _navbar.md _sidebar.md _coverpage.md; do
  while IFS= read -r link_target; do
    link_target="${link_target%%#*}"
    if [[ -z "${link_target}" || "${link_target}" == http://* || "${link_target}" == https://* ]]; then
      continue
    fi
    if [[ ! -e "${source_directory}/${link_target}" ]]; then
      printf 'broken navigation link in %s: %s\n' "${navigation_file}" "${link_target}" >&2
      exit 1
    fi
  done < <(sed -nE 's/.*\]\(([^)]+)\).*/\1/p' "${source_directory}/${navigation_file}")
done

# 重建静态目录，避免旧文件残留到 Pages artifact。
rm -rf "${output_directory}"
mkdir -p "${output_directory}"
cp -R "${source_directory}/." "${output_directory}/"

# 内部推进计划和 TODO 不进入公开站点 artifact。
rm -f "${output_directory}"/*_TODO.md
rm -f "${output_directory}"/*_PLAN.md
rm -f "${output_directory}/PLAN.md"

# 文档 Playground 使用项目锁定的 Go 工具链生成纯 Go WebAssembly，禁止静默使用其他版本。
go_version="$(go env GOVERSION)"
if [[ "${go_version}" != "go1.26.4" ]]; then
  printf 'docs WebAssembly build requires go1.26.4, got %s\n' "${go_version}" >&2
  exit 1
fi

# wasm_exec.js 必须与生成 WebAssembly 的 Go SDK 同版本，否则运行时导入协议可能不兼容。
go_root="$(go env GOROOT)"
wasm_exec_source="${go_root}/lib/wasm/wasm_exec.js"
if [[ ! -f "${wasm_exec_source}" ]]; then
  printf 'missing Go WebAssembly runtime: %s\n' "${wasm_exec_source}" >&2
  exit 1
fi
cp "${wasm_exec_source}" "${output_directory}/assets/wasm_exec.js"

# 浏览器补齐复用 VS Code/LSP 的内置函数目录，避免文档端维护第二份函数签名与中文说明。
builtin_catalog_source="${repository_root}/vscode/extensions/glua-lsp/server/builtin-functions.json"
if [[ ! -f "${builtin_catalog_source}" ]]; then
  printf 'missing builtin completion catalog: %s\n' "${builtin_catalog_source}" >&2
  exit 1
fi
cp "${builtin_catalog_source}" "${output_directory}/assets/builtin-functions.json"

# WASM 只进入最终文档 artifact，不向源码目录提交大体积二进制。
(
  cd "${repository_root}"
  CGO_ENABLED=0 GOOS=js GOARCH=wasm go build -trimpath -ldflags='-s -w' \
    -o "${output_directory}/assets/glua.wasm" ./cmd/glua-wasm
)

printf 'Docsify site built: %s\n' "${output_directory}"
