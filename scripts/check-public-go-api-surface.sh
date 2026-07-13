#!/usr/bin/env bash
set -euo pipefail

# 解析仓库根目录，确保从任意工作目录执行时都检查同一份公开 lua API 快照。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
expected_surface="${repository_root}/scripts/public-go-api-surface.txt"
actual_surface="$(mktemp "${TMPDIR:-/tmp}/glua-public-api-surface.XXXXXX")"
trap 'rm -f "${actual_surface}"' EXIT

# 提取包级常量、变量、函数、方法和类型，忽略中文注释与布局，保证快照可跨 Go 小版本稳定比较。
go -C "${repository_root}" doc -all github.com/ZingYao/go-lua-vm/lua | awk '
/^(const|var) \(/ { section=$1; next }
section != "" && /^\)/ { section=""; next }
section != "" && /^[[:space:]]*[A-Z][A-Za-z0-9_]*/ {
    line=$0
    sub(/^[[:space:]]*/, "", line)
    split(line, fields, /[[:space:]]+/)
    print toupper(section) " " fields[1]
    next
}
/^[[:space:]]*func / {
    line=$0
    sub(/^[[:space:]]*/, "", line)
    sub(/ \{.*$/, "", line)
    print "FUNC " line
    next
}
/^[[:space:]]*type / {
    line=$0
    sub(/^[[:space:]]*/, "", line)
    sub(/ \{.*$/, "", line)
    print "TYPE " line
    next
}
' | LC_ALL=C sort -u >"${actual_surface}"

# 公开导出面变化必须显式审核并更新快照，避免无意破坏第三方 Go 调用方。
if ! diff -u "${expected_surface}" "${actual_surface}"; then
    echo "public lua API surface changed; review compatibility and update scripts/public-go-api-surface.txt intentionally" >&2
    exit 1
fi

printf 'GLUA_PUBLIC_GO_API_SURFACE_OK\n'
