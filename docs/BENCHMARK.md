# Benchmark 最终结果

本文只保留当前 `glua` / `gluac` 对官方 Lua 5.3.6 的最终 benchmark 结果。完整优化过程、每个用例如何拉近差距、与官方 Lua C 实现不同的地方，以及 guard 测试索引见 `docs/PERFORMANCE_CLOSURE_REPORT.md`。

倍率语义：`本项目/官方 Lua 5.3.6`。低于 `1.00x` 表示本项目快于官方，高于 `1.00x` 表示本项目仍慢于官方。

## 数据口径

- 最终代码基线：`c322b5d`
- 对比脚本：`scripts/benchmark-official.sh`
- 官方工具：Lua 5.3.6 `lua` / `luac`
- 本项目工具：`./bin/glua` / `./bin/gluac`
- 构建要求：`CGO_ENABLED=0`
- 统计方式：默认完整 benchmark 三轮，取三轮中位数。

## 最终对比表

| English key | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 状态 |
| --- | --- | ---: | ---: | ---: | --- |
| `recursion` | 递归 | 0.003505s | 0.003695s | 1.054x | 剩余项，语义门禁证伪 |
| `table_rw` | 表读写 | 0.006929s | 0.006035s | 0.871x | 低于 1.00x |
| `closure_upvalue` | 闭包 upvalue | 0.007892s | 0.006725s | 0.852x | 低于 1.00x |
| `arith_mix_loop` | 混合算术循环 | 0.011024s | 0.009175s | 0.832x | 低于 1.00x |
| `string_concat` | 字符串拼接 | 0.004590s | 0.003778s | 0.823x | 低于 1.00x |
| `function_call` | 函数调用 | 0.006610s | 0.005256s | 0.795x | 低于 1.00x |
| `compile_3000_functions` | 编译3000个函数 | 0.005015s | 0.003879s | 0.773x | 低于 1.00x |
| `arith_chain_temp` | 算术临时链 | 0.012395s | 0.009627s | 0.777x | 低于 1.00x |
| `arith_add_loop` | 整数累加循环 | 0.007571s | 0.004865s | 0.643x | 低于 1.00x |
| `stdlib_math_string` | 标准库数学与字符串 | 0.018876s | 0.010797s | 0.572x | 低于 1.00x |

最终只有 `recursion` 高于 `1.00x`。该项已经通过 profile 证明 prepared 路径为 `0 B/op`、`0 allocs/op`，剩余差距来自执行框架、VM step 和自递归 fast path 的固定 CPU；继续压缩需要整段递归折叠或调用帧绕过，会破坏 Debug、coroutine/yield、traceback、错误 PC 和调用帧可见性，因此按语义门禁不进入生产实现。

## 2026-07-08 平台复核

本段记录 `native_modules` 收尾阶段按既有 benchmark 脚本路径做的平台复核；benchmark 均使用默认 no-CGO `glua` / `gluac`，未为性能数据改动 Go 代码。各平台结果受 CPU、调度器、电源模式、终端环境和系统负载影响，不能直接混合作为同一台机器上的回归判断；同一平台内的趋势用于发布说明和后续优化排查。

复核口径：

- 当前复核基线：`ac6a3e3`
- 对比脚本：`scripts/benchmark-official.sh`
- 统计方式：macOS 与 Linux 各跑 5 轮，按 5 轮结果分别取官方工具中位数和本项目中位数后计算倍率。
- Android：设备侧 benchmark 跑 5 轮；每轮的运行用例 warmup 5 次、计时 40 次取中位数，编译用例 warmup 5 次、计时 30 次取中位数；再按 5 轮结果分别取官方工具中位数和本项目中位数后计算倍率。
- Windows：当前记录为 3 轮默认 benchmark 中位数；后续如需与 macOS/Linux 完全同轮数对齐，可在 Windows 上追加 5 轮复核。
- 依赖拉取：macOS host 和 Linux VM 内均执行 `go mod download`，结果均为 `go: no module dependencies to download`。

### macOS arm64 5 轮结果

环境：

- OS：Darwin arm64。
- Go：`go version go1.26.4 darwin/arm64`。
- 官方工具：`build/official-lua-5.3.6/lua-5.3.6/src/lua` / `luac`。
- 本项目工具：默认 no-CGO 重建的 `bin/glua` / `bin/gluac`。

| 用例 | 官方 5 轮中位数 | 本项目 5 轮中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.003956s | 0.003207s | 0.81x |
| `arith_mix_loop` | 0.005777s | 0.006414s | 1.11x |
| `arith_chain_temp` | 0.006183s | 0.006523s | 1.05x |
| `table_rw` | 0.004103s | 0.003880s | 0.95x |
| `function_call` | 0.003941s | 0.003614s | 0.92x |
| `string_concat` | 0.002925s | 0.002484s | 0.85x |
| `closure_upvalue` | 0.004186s | 0.004573s | 1.09x |
| `stdlib_math_string` | 0.011843s | 0.007687s | 0.65x |
| `recursion` | 0.002150s | 0.002384s | 1.11x |
| `compile_3000_functions` | 0.003417s | 0.002677s | 0.78x |

### Linux arm64 5 轮结果

环境：

- OrbStack VM：`glua-bench-linux-20260708c`，benchmark 后已执行 `orbctl delete --force glua-bench-linux-20260708c` 清理。
- OS：Ubuntu 24.04.4 LTS `noble`，arm64。
- Go：`go version go1.26.4 linux/arm64`。
- C toolchain：`gcc (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0`。
- 官方工具：从 `build/official-lua-5.3.6/lua-5.3.6` 源码复制到 VM `/tmp` 后重新 `make linux`。
- 本项目工具：VM 内默认 no-CGO 重建的 `/tmp/glua-bench-bin-20260708/glua` / `gluac`。

| 用例 | 官方 5 轮中位数 | 本项目 5 轮中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.002801s | 0.001145s | 0.41x |
| `arith_mix_loop` | 0.005348s | 0.003869s | 0.72x |
| `arith_chain_temp` | 0.005402s | 0.004240s | 0.78x |
| `table_rw` | 0.002473s | 0.001812s | 0.73x |
| `function_call` | 0.002370s | 0.001475s | 0.62x |
| `string_concat` | 0.001357s | 0.000460s | 0.34x |
| `closure_upvalue` | 0.002692s | 0.002368s | 0.88x |
| `stdlib_math_string` | 0.009816s | 0.005226s | 0.53x |
| `recursion` | 0.000634s | 0.000449s | 0.71x |
| `compile_3000_functions` | 0.001633s | 0.000643s | 0.39x |

### Android arm64 5 轮结果

环境：

- Device：`24129PN74C`，ABI `arm64-v8a`。
- Android：`16`，SDK `36`。
- Kernel：`Linux localhost 6.6.77-android15-8-gf9a1d4bd8353-abogki440974771-4k #1 SMP PREEMPT Fri Aug 29 01:48:34 UTC 2025 aarch64 Toybox`。
- 官方工具：Lua 5.3.6 源码用 Android NDK `aarch64-linux-android35-clang` 构建后推送到 `/data/local/tmp/glua-bench-20260708-rerun/`。
- 本项目工具：`GOOS=android GOARCH=arm64 CGO_ENABLED=0` 构建 `glua` / `gluac` 后推送到同目录。
- 计时方式：Android 设备侧 shell 使用 `date +%s%N` 包住单次命令，Mac 端只收集输出并计算中位数。

| 用例 | 官方 5 轮中位数 | 本项目 5 轮中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.028197s | 0.015842s | 0.56x |
| `arith_mix_loop` | 0.038319s | 0.024276s | 0.63x |
| `arith_chain_temp` | 0.046780s | 0.026184s | 0.56x |
| `table_rw` | 0.022113s | 0.018205s | 0.82x |
| `function_call` | 0.024726s | 0.018068s | 0.73x |
| `string_concat` | 0.018042s | 0.014551s | 0.81x |
| `closure_upvalue` | 0.027542s | 0.020648s | 0.75x |
| `stdlib_math_string` | 0.051390s | 0.029732s | 0.58x |
| `recursion` | 0.015599s | 0.014101s | 0.90x |
| `compile_3000_functions` | 0.019182s | 0.014471s | 0.75x |

### Windows 11 amd64

- 日期：2026-07-08
- 系统：Microsoft Windows 11 企业版 10.0.26200，64 位
- 机器：HP Victus by HP Gaming Laptop 16-r1xxx
- CPU：Intel(R) Core(TM) i7-14650HX，16 cores / 24 logical processors，MaxClockSpeed 2200 MHz
- 内存：68408893440 bytes，约 63.7 GiB
- Go：go1.26.4 windows/amd64
- 官方 Lua：Lua 5.3.6，路径 `C:\mise-data\installs\lua\5.3.6\bin`
- 本项目产物：`bin\glua.exe` / `bin\gluac.exe`
- 构建模式：`CGO_ENABLED=0`
- 脚本：`scripts/benchmark-official.sh`

| English key | 官方 Lua 5.3.6 中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.049096s | 0.125993s | 2.57x |
| `arith_mix_loop` | 0.049923s | 0.121414s | 2.43x |
| `arith_chain_temp` | 0.050234s | 0.120178s | 2.39x |
| `table_rw` | 0.044570s | 0.114574s | 2.57x |
| `function_call` | 0.054492s | 0.144964s | 2.66x |
| `string_concat` | 0.061553s | 0.119760s | 1.95x |
| `closure_upvalue` | 0.050301s | 0.126592s | 2.52x |
| `stdlib_math_string` | 0.062930s | 0.131121s | 2.08x |
| `recursion` | 0.053992s | 0.118328s | 2.19x |
| `compile_3000_functions` | 0.056098s | 0.101286s | 1.81x |

Windows 本轮结果显示 no-CGO `glua` 在全部用例上慢于官方 Lua 5.3.6 C 实现，倍率范围为 `1.81x` 到 `2.66x`。该记录仅代表上述 Windows 机器和当次运行环境。

#### Windows 11 amd64 摊销启动成本复核

该复核用于区分 Windows CLI 冷启动成本和 VM/编译器热路径成本。原始 `scripts/benchmark-official.sh` 每个样本都会重新启动一次 `lua.exe` / `glua.exe`，Windows 上进程创建、文件系统检查、Defender/安全扫描和 Go runtime 初始化会让空脚本启动时间接近正式短用例耗时。摊销方案改为每个样本只启动一次进程，在该进程内循环执行同一 fixture 多次，再由 Python 对多轮样本取中位数。

- 日期：2026-07-08
- 系统：Microsoft Windows 11 企业版 10.0.26200，64 位
- 机器：HP Victus by HP Gaming Laptop 16-r1xxx
- CPU：Intel(R) Core(TM) i7-14650HX，16 cores / 24 logical processors，MaxClockSpeed 2200 MHz
- 内存：68408893440 bytes，约 63.7 GiB
- Go：go1.26.4 windows/amd64
- 官方 Lua：Lua 5.3.6，路径 `C:\mise-data\installs\lua\5.3.6\bin`
- 本项目产物：`bin\glua.exe`
- 构建模式：`CGO_ENABLED=0`
- 脚本：`scripts/benchmark-official-amortized.sh`
- 统计方式：每项 warmup 1 轮，计时 5 轮取中位数。
- 运行用例：每个计时样本启动一次进程，进程内重复执行 fixture 30 次。
- 编译用例：每个计时样本启动一次进程，进程内对 3000 函数源码重复 `load(source)` 10 次，并执行最后一次编译结果做正确性检查。

| English key | 官方 Lua 5.3.6 摊销中位数 | 本项目摊销中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.154923s | 0.153945s | 0.99x |
| `arith_mix_loop` | 0.262008s | 0.287075s | 1.10x |
| `arith_chain_temp` | 0.333187s | 0.321460s | 0.96x |
| `table_rw` | 0.197526s | 0.189425s | 0.96x |
| `function_call` | 0.150339s | 0.176525s | 1.17x |
| `string_concat` | 0.166109s | 0.130346s | 0.78x |
| `closure_upvalue` | 0.166237s | 0.209514s | 1.26x |
| `stdlib_math_string` | 0.549857s | 0.391544s | 0.71x |
| `recursion` | 0.064265s | 0.123286s | 1.92x |
| `compile_3000_functions` | 0.071103s | 0.154198s | 2.17x |

摊销复核后，Windows 运行类用例多数回落到 `0.71x` 到 `1.26x`，说明冷启动记录中的 `1.81x` 到 `2.66x` 主要由 Windows 短进程固定成本放大。`recursion` 与 `compile_3000_functions` 仍明显慢于官方，分别为 `1.92x` 和 `2.17x`，后续如继续 Windows 性能优化，应优先用进程内 benchmark 或 Go micro/profile 定位这两项，而不是用 CLI 冷启动结果直接判断 VM 热路径。

## 复现命令

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
GLUA_BIN=./bin/glua \
GLUAC_BIN=./bin/gluac \
./scripts/benchmark-official.sh

LUA_BIN=/path/to/lua-5.3.6 \
GLUA_BIN=./bin/glua \
./scripts/benchmark-official-amortized.sh
```

## 兼容门禁

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
./scripts/compare-cli-golden.sh
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
./scripts/compare-official-executables.sh
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
./scripts/run-official-tests.sh
```
