# native_modules Windows 最终 Benchmark 手册

本文用于在 Windows 目标平台生成最终 benchmark 结果。它只回答性能问题：当前分支默认 no-CGO `glua` / `gluac` 与官方 Lua 5.3.6 的差异。功能验收另见 `docs/NATIVE_MODULES_WINDOWS_FUNCTIONAL_TEST.md`。

## 最近一次结果

2026-07-08 已在 Windows amd64 上完成默认 cold-start benchmark，并将结果写入 `docs/BENCHMARK.md`。同日追加 `scripts/benchmark-official-amortized.sh` 摊销启动成本复核，用于区分 Windows 短进程固定成本与 VM/编译器热路径成本。

环境摘要：

- OS：Microsoft Windows 11 企业版 10.0.26200，64 位。
- Go：`go version go1.26.4 windows/amd64`。
- 官方 Lua：Lua 5.3.6，路径 `C:\mise-data\installs\lua\5.3.6\bin`。
- 本项目产物：默认 no-CGO `bin\glua.exe` / `bin\gluac.exe`。

Cold-start benchmark 结论：本项目相对官方 Lua 5.3.6 为 `1.81x` 到 `2.66x`，主要受 Windows 进程启动、文件系统检查和 Go runtime 初始化固定成本影响。

摊销启动成本复核命令：

```bash
LUA_BIN=/c/mise-data/installs/lua/5.3.6/bin/lua.exe \
GLUA_BIN="$PWD/bin/glua.exe" \
./scripts/benchmark-official-amortized.sh
```

摊销复核结果：多数运行类用例回落到 `0.71x` 到 `1.26x`；`recursion` 和 `compile_3000_functions` 仍明显慢于官方，分别为 `1.92x` 和 `2.17x`。后续 Windows 性能优化应优先使用进程内 benchmark 或 Go micro/profile 定位这两项。

## 前置条件

必须先完成 Windows 功能验收，并且 `scripts/test-native-windows-manual.ps1 -StrictRuntime` 通过。

Benchmark 使用默认 no-CGO 构建，不能启用 `native_modules`：

```powershell
$env:CGO_ENABLED = "0"
```

需要准备：

- Go `go1.26.4`。
- Git Bash 或 MSYS2 bash。
- Python 3，可由 bash 环境访问。
- 官方 Lua 5.3.6 `lua.exe`。
- 官方 Lua 5.3.6 `luac.exe`。

`scripts/benchmark-official.sh` 会检查官方工具版本，`lua.exe -v` 和 `luac.exe -v` 必须包含 `Lua 5.3.6`。

## 构建本项目工具

在仓库根目录用 PowerShell 执行：

```powershell
$env:CGO_ENABLED = "0"
go build -o bin\glua.exe .\cmd\glua
go build -o bin\gluac.exe .\cmd\gluac
```

## 单轮预检查

先在 Git Bash 中跑一轮，确认路径、版本和 Python 都可用。

请替换官方 Lua 路径：

```bash
export LUA_BIN="/c/path/to/lua-5.3.6/lua.exe"
export LUAC_BIN="/c/path/to/lua-5.3.6/luac.exe"
export GLUA_BIN="$PWD/bin/glua.exe"
export GLUAC_BIN="$PWD/bin/gluac.exe"

BENCH_ITERATIONS=40 \
BENCH_COMPILE_ITERATIONS=30 \
BENCH_WARMUP_ITERATIONS=5 \
./scripts/benchmark-official.sh
```

如果这里失败，先修复工具路径或官方 Lua 版本，不要进入最终轮次。

## 最终轮次

最终 benchmark 跑 5 轮；如果机器时间有限，最低不得少于 3 轮。每轮使用脚本默认完整参数：

- `BENCH_ITERATIONS=40`
- `BENCH_COMPILE_ITERATIONS=30`
- `BENCH_WARMUP_ITERATIONS=5`

Git Bash 命令：

```bash
set -euo pipefail

export LUA_BIN="/c/path/to/lua-5.3.6/lua.exe"
export LUAC_BIN="/c/path/to/lua-5.3.6/luac.exe"
export GLUA_BIN="$PWD/bin/glua.exe"
export GLUAC_BIN="$PWD/bin/gluac.exe"

mkdir -p build/bench-windows

for round in 1 2 3 4 5; do
  echo "== Windows benchmark round ${round} =="
  BENCH_ITERATIONS=40 \
  BENCH_COMPILE_ITERATIONS=30 \
  BENCH_WARMUP_ITERATIONS=5 \
  ./scripts/benchmark-official.sh | tee "build/bench-windows/round-${round}.md"
done
```

## 汇总结果

在同一个 Git Bash 中执行，并保存为 `build/bench-windows/final-summary.md`：

```bash
python3 - <<'PY' | tee build/bench-windows/final-summary.md
from pathlib import Path
from statistics import median

rows = {}
for path in sorted(Path("build/bench-windows").glob("round-*.md")):
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.startswith("| `"):
            continue
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        if len(cells) != 4:
            continue
        name = cells[0].strip("`")
        official = float(cells[1].removesuffix("s"))
        project = float(cells[2].removesuffix("s"))
        rows.setdefault(name, {"official": [], "project": []})
        rows[name]["official"].append(official)
        rows[name]["project"].append(project)

print("| 用例 | 官方多轮中位数 | 本项目多轮中位数 | 本项目/官方 |")
print("| --- | ---: | ---: | ---: |")
for name, values in rows.items():
    official = median(values["official"])
    project = median(values["project"])
    ratio = project / official if official else float("nan")
    print(f"| `{name}` | {official:.6f}s | {project:.6f}s | {ratio:.2f}x |")
PY
```

## 需要反馈的结果

请反馈以下内容：

- Windows 版本和架构。
- `go version`。
- 官方 `lua.exe -v` 与 `luac.exe -v`。
- 实际执行轮数，推荐 5 轮，最低 3 轮。
- `build/bench-windows/round-*.md`。
- 最终汇总表。

## 判定口径

倍率语义为 `本项目/官方 Lua 5.3.6`：

- 低于 `1.00x`：本项目快于官方。
- 等于 `1.00x` 附近：性能基本持平。
- 高于 `1.00x`：本项目慢于官方，需要结合 Mac/Linux 结果判断是否为 Windows 特有回退。

Benchmark 不改变代码，不替代功能验收，也不替代默认 no-CGO 门禁。
