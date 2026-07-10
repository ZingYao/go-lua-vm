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
  PERFORMANCE.md
  NATIVE_BUILD_GUIDE.md
  SYNTAX_EXTENSIONS.md
  EXTENSION_APIS.md
  glua-event.md
  glua-serialization.md
  glua-utilities.md
  assets/theme.css
  assets/favicon.svg
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

printf 'Docsify site built: %s\n' "${output_directory}"
