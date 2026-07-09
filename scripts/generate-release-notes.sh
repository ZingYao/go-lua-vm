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
  echo "This release publishes full-feature CLI artifacts built with \`CGO_ENABLED=1\` and the \`native_modules\` build tag, so native Lua C module loading is included in the release build."
  echo
  echo "- Range: ${compare_text}"
  if [[ -n "${compare_url}" ]]; then
    echo "- Compare: ${compare_url}"
  fi
  echo
  echo "### Changes"
  echo
  printf '%s\n' "${commit_lines}"
  echo
  echo "## 中文"
  echo
  echo "本次发布会生成全功能 CLI 产物，构建时启用 \`CGO_ENABLED=1\` 和 \`native_modules\` build tag，因此发布版本包含 Lua C 原生模块加载能力。"
  echo
  echo "- 范围：${compare_text}"
  if [[ -n "${compare_url}" ]]; then
    echo "- 对比：${compare_url}"
  fi
  echo
  echo "### 变更列表"
  echo
  printf '%s\n' "${commit_lines}"
} >"${output_path}"

echo "generated release notes: ${output_path}"
