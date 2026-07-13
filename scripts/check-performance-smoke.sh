#!/usr/bin/env bash
set -euo pipefail

# 解析仓库根目录并使用项目固定的纯 Go 构建边界执行基准 smoke。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"

# 工具链不匹配时拒绝采集，避免把不同 Go 版本的基准行为混入项目基线。
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
    echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
    exit 1
fi

# 每个 benchmark 仅运行一次，不把本机 wall-clock 波动当作 CI 性能阈值；目标是发现 panic、死锁和构建回归。
CGO_ENABLED=0 go -C "${repository_root}" test ./... -run '^$' -bench . -benchtime=1x -count=1
printf 'GLUA_PERFORMANCE_SMOKE_OK\n'
