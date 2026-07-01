# Benchmark 基线

本文记录当前纯 Go Lua 5.3 VM 的首个 runtime benchmark 基线，用于后续优化、回归和发布前对比。

## 执行环境

- 日期：2026-06-27
- 命令：`CGO_ENABLED=0 go test ./runtime -run=^$ -bench=. -benchtime=100ms`
- OS/Arch：`darwin/arm64`
- CPU：`Apple M1 Max`
- Go：项目 `go.mod` 声明 `go 1.26` 与 `toolchain go1.26.4`
- CGO：关闭

## 结果

```text
BenchmarkVMDispatch-10                         37104798      3.120 ns/op       0 B/op   0 allocs/op
BenchmarkTableReadWrite/raw_set_integer-10     23011464      5.130 ns/op       0 B/op   0 allocs/op
BenchmarkTableReadWrite/raw_get_integer-10     24501250      4.926 ns/op       0 B/op   0 allocs/op
BenchmarkGoFunctionCall-10                      2480064     49.25 ns/op      128 B/op   2 allocs/op
BenchmarkStringConcat-10                        3321932     35.90 ns/op       16 B/op   1 allocs/op
BenchmarkGoLuaCallback-10                        509391    268.5 ns/op       492 B/op   5 allocs/op
```

## 使用说明

- 该基线只覆盖 runtime 当前已有 benchmark，不代表完整 Lua 5.3 官方测试性能。
- 后续修改 VM dispatch、Table、字符串、Go/Lua 回调和 bridge 层时，应复跑同一命令并记录差异。
- 若硬件、Go toolchain、`benchtime` 或 CGO 设置变化，不能直接与本基线做精确比较。

## 官方 Lua 5.3.6 CLI 对比

### 执行环境

- 日期：2026-06-29
- OS/Arch：`darwin/arm64`
- macOS：`26.5.1`
- CPU：`Apple M1 Max`
- Go：`go version go1.26.4 darwin/arm64`
- CGO：本项目 `glua` / `gluac` 构建时关闭，命令为 `CGO_ENABLED=0 go build -o bin/glua ./cmd/glua` 与 `CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac`
- 官方 Lua：从 `https://www.lua.org/ftp/lua-5.3.6.tar.gz` 下载完整发布包到 `/tmp`，SHA256 为 `fc5fd69bb8736323f026672b1b7235da613d7177e72558893a0bdcd320466d60`
- 官方 Lua 构建：`make macosx MYCFLAGS='-DLUA_COMPAT_5_2'`
- 说明：仓库内 `third_party/lua-5.3.6/` 当前参考副本缺少 `luac.c`，因此 `luac` 对比使用官方完整发布包构建产物。

### 方法

- 每个用例先各自 warmup 一次，再交替执行官方工具与本项目工具各 5 次。
- 统计 wall-clock elapsed time 的中位数。
- `lua` 对比执行同一份临时 Lua 脚本，并校验 stdout 一致。
- `luac` 对比编译同一份 2500 个全局函数定义的 Lua 源码，并校验两侧均成功写出 chunk。

### 结果

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_loop` | 0.036815s | 0.923068s | 25.07x |
| `table_rw` | 0.014469s | 0.332654s | 22.99x |
| `function_call` | 0.027137s | 0.965794s | 35.59x |
| `string_concat` | 0.011507s | 1.064175s | 92.48x |
| `compile_2500_global_functions` | 0.007467s | 0.019272s | 2.58x |

### 结论

- 当前 `glua` 已以兼容性验收为第一目标，解释执行性能明显慢于官方 C Lua；算术循环、表读写和 Lua 函数调用约慢 23x 到 36x。
- 字符串连续拼接差距最大，约慢 92x，后续优化应优先检查 `CONCAT` 指令、字符串分配、短字符串驻留和 Lua 字符串 builder 路径。
- `gluac` 编译速度与官方 `luac` 的差距相对较小，当前临时源码编译约慢 2.6x，说明 lexer/parser/codegen 的首轮性能风险低于 VM 执行热路径。
- 该结果是单机、短脚本、wall-clock 基准，不作为发布性能承诺；后续优化需要补充更稳定的 benchmark harness，并分别跟踪 VM 指令分发、表、字符串、函数调用和 binary chunk 编解码。

## 发布验证结论同步

- 当前发布口径仍以 Lua 5.3 行为兼容和 `glua`/`gluac` 官方可执行文件兼容为优先级，不把性能追平官方 C Lua 作为首个 release 阻塞条件。
- VFS、动态库 loader、Go 封装 API 和 reflection 自动绑定属于 Go 嵌入增强能力；它们的验收以 `CGO_ENABLED=0 go test ./...`、`./scripts/check-go-gates.sh`、`docs/RELEASE_VALIDATION_TODO.md` 中列出的专项测试和发布限制文档为准。
- reflection 自动绑定已支持显式 opt-in 的函数和 struct 扫描，但尚未建立独立 benchmark；后续性能专项应补充自动函数调用、字段读写、方法调用与显式 binding 的对比。

## 官方 Lua 5.3.6 全方位对比

### 执行环境

- 日期：2026-06-30
- OS/Arch：`darwin/arm64`
- CPU：`Apple M1 Max`
- 官方 Lua：本机安装的官方 Lua 5.3.6 `lua` 与 `luac`，通过 `LUA_BIN` / `LUAC_BIN` 指定
- 本项目：`./bin/glua` 与 `./bin/gluac`
- 构建命令：`CGO_ENABLED=0 go build -o bin/glua ./cmd/glua` 与 `CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac`
- 统计口径：每个脚本 warmup 后交替运行 20 次，记录 wall-clock elapsed time 中位数；CLI 冷启动用例运行 30 次。

### 兼容性对比

`LUA_BIN=<lua-5.3.6>/bin/lua LUAC_BIN=<lua-5.3.6>/bin/luac GLUA_BIN=./bin/glua GLUAC_BIN=./bin/gluac ./scripts/compare-official-executables.sh`

该脚本当前未完全通过，差异集中在展示格式而非性能：

- `runtime_error` traceback 文案差异：官方为 `[C]: in function 'error'`，本项目为 `[C]: in global 'error'`。
- `luac -l` 与 `luac -l -l` 列表格式差异：官方 `luac` 使用原生列表格式，本项目 `gluac` 使用自定义反汇编格式。

### 脚本运行性能

#### 完整 benchmark 复核

2026-07-01 在 `quanquan/feature/perf-followup` 分支按完整脚本口径复测。官方工具不是 PATH 上的
Lua 5.5，而是在临时目录下载 `lua-5.3.6.tar.gz`、校验 SHA256 后构建出的官方 Lua 5.3.6 `lua` /
`luac`；本项目使用当前源码临时构建出的 `glua` / `gluac`。每个脚本 warmup 后交替运行 40 次，取
wall-clock 中位数；`compile_3000_functions` 运行 30 次；本项目构建仍使用 `CGO_ENABLED=0`。

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008531s | 0.030540s | 3.58x |
| `arith_mix_loop` | 0.007172s | 0.021086s | 2.94x |
| `arith_chain_temp` | 0.013937s | 0.058559s | 4.20x |
| `table_rw` | 0.004125s | 0.007255s | 1.76x |
| `function_call` | 0.003669s | 0.005258s | 1.43x |
| `string_concat` | 0.005871s | 0.010892s | 1.86x |
| `closure_upvalue` | 0.016460s | 0.046771s | 2.84x |
| `stdlib_math_string` | 0.004410s | 0.006968s | 1.58x |
| `recursion` | 0.003829s | 0.007652s | 2.00x |
| `compile_3000_functions` | 0.006208s | 0.015438s | 2.49x |

本轮完整口径下仍高于 3x 的路径为 `arith_add_loop` 与临时补充的 `arith_chain_temp`。其中
`arith_chain_temp` 覆盖 `sum = sum + i * 3 - 7` 这类左结合自二元链，用于区分截图中一度混用的
`arith_add_loop` 与混合算术链；后续需要把该 fixture 固化到稳定 benchmark harness 后再作为长期回归项。
原旧清单中的 `arith_mix_loop`、`closure_upvalue`、`stdlib_math_string` 和 `recursion` 在本轮复测中已经
低于 3x，但仍作为回归观察项。

#### 2026-07-01 MUL integer cache 顺序复核

本轮只调整 `MUL` 执行时 integer inline cache 与 number-constant 窄路径的尝试顺序：已建立
integer cache 后先执行 cache 命中路径，避免 `arith_chain_temp` 中 `i * 3` 每轮先检查 number
常量。该改动不改变 codegen，`arith_chain_temp` 的循环体仍与官方 Lua 5.3.6 一致：
`MUL; ADD; SUB; FORLOOP`，项目额外的循环退出零距离 `JMP` 仍只服务 line hook。

交替运行上一轮提交二进制与本轮临时二进制各 60 次，取 wall-clock 中位数：

| 用例 | 上轮 `glua` | 本轮 `glua` | 本轮较上轮 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.029943s | 0.029945s | +0.01% |
| `arith_chain_temp` | 0.058003s | 0.055846s | -3.72% |

因此本轮优化只确认降低 `arith_chain_temp` 的 VM 运行成本；`arith_add_loop` 仍需继续作为优先目标。

#### 短期性能优化复核历史

下表保留 2026-07-01 较窄短期目标脚本口径的历史复核结果。由于完整脚本口径覆盖的循环规模和标准库
调用组合不同，当前优化判断以上方完整 benchmark 复核为准。

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_mix_loop` | 0.006685s | 0.019309s | 2.89x |
| `table_rw` | 0.003398s | 0.005998s | 1.77x |
| `function_call` | 0.003023s | 0.004250s | 1.41x |
| `closure_upvalue` | 0.014514s | 0.043412s | 2.99x |
| `stdlib_math_string` | 0.003337s | 0.005193s | 1.56x |
| `recursion` | 0.003062s | 0.006723s | 2.20x |
| `compile_3000_functions` | 0.006180s | 0.013627s | 2.21x |

#### 优化前历史基线

下表保留 2026-06-30 左右的优化前历史数据，用于对比性能专项收益；当前结果以上方复核表为准。

| 用例 | 官方 `lua` 中位数 | `glua` 中位数 | `glua`/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.005855s | 0.021629s | 3.69x |
| `arith_mix_loop` | 0.008665s | 0.044818s | 5.17x |
| `table_rw` | 0.009094s | 0.048963s | 5.38x |
| `function_call` | 0.007181s | 0.034119s | 4.75x |
| `string_concat` | 0.003695s | 0.006298s | 1.70x |
| `closure_upvalue` | 0.008760s | 0.042832s | 4.89x |
| `stdlib_math_string` | 0.010161s | 0.082317s | 8.10x |
| `recursion` | 0.003958s | 0.014580s | 3.68x |
| `compile_3000_functions` | 0.008118s | 0.015539s | 1.91x |

```mermaid
xychart-beta
    title "Lua 5.3.6 vs glua/gluac median time"
    x-axis ["add", "mix", "table", "call", "concat", "closure", "stdlib", "recur", "compile"]
    y-axis "seconds" 0 --> 0.11
    line "official" [0.005855, 0.008665, 0.009094, 0.007181, 0.003695, 0.008760, 0.010161, 0.003958, 0.008118]
    line "glua/gluac" [0.021629, 0.044818, 0.048963, 0.034119, 0.006298, 0.042832, 0.082317, 0.014580, 0.015539]
```

慢速倍数保留在上方表格中，避免把耗时值和倍数值混入同一图表导致阅读误差。

### CLI 冷启动与小任务

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `lua_empty_script` | 0.002925s | 0.003839s | 1.31x |
| `lua_eval_empty` | 0.003037s | 0.004092s | 1.35x |
| `lua_version` | 0.003219s | 0.004219s | 1.31x |
| `luac_parse_only` | 0.002860s | 0.003698s | 1.29x |
| `luac_list` | 0.002942s | 0.003677s | 1.25x |
| `luac_compile_tiny` | 0.002849s | 0.003629s | 1.27x |

```mermaid
xychart-beta
    title "CLI cold start and tiny tasks"
    x-axis ["empty", "eval", "version", "parse", "list", "compile"]
    y-axis "seconds" 0 --> 0.005
    line "official" [0.002925, 0.003037, 0.003219, 0.002860, 0.002942, 0.002849]
    line "glua/gluac" [0.003839, 0.004092, 0.004219, 0.003698, 0.003677, 0.003629]
```

### Go 内部 Benchmark

命令：`CGO_ENABLED=0 go test ./runtime ./lua -run=^$ -bench=. -benchmem -benchtime=3s -count=3`

| 用例 | 当前结果 |
| --- | ---: |
| `BenchmarkVMDispatch` | 约 2.89 ns/op，0 allocs |
| `BenchmarkTableReadWrite/raw_set_integer` | 约 6.05 ns/op，0 allocs |
| `BenchmarkTableReadWrite/raw_get_integer` | 约 5.23 ns/op，0 allocs |
| `BenchmarkGoFunctionCall` | 约 46.8 ns/op，128 B/op，2 allocs |
| `BenchmarkStringConcat` | 约 34.7 ns/op，16 B/op，1 alloc |
| `BenchmarkVMConcatInstruction/binary_string` | 约 24.7 ns/op，8 B/op，1 alloc |
| `BenchmarkVMConcatInstruction/empty_right` | 约 4.20 ns/op，0 allocs |
| `BenchmarkVMConcatInstruction/empty_left` | 约 4.27 ns/op，0 allocs |
| `BenchmarkVMConcatInstruction/four_strings` | 约 39.9 ns/op，16 B/op，1 alloc |
| `BenchmarkGoLuaCallback` | 约 255 ns/op，约 584-590 B/op，5 allocs |
| `BenchmarkDoStringStringConcat` | 约 0.475 ms/op，约 2.23 MB/op，2317 allocs |
| `BenchmarkDoStringFunctionCall` | 约 0.534 ms/op，约 109 KB/op，372 allocs |

### 结论

- CLI 冷启动和小脚本差距较小，历史冷启动约 1.25x 到 1.35x；本轮 `compile_3000_functions` 为 2.49x，仍低于当前 3x 目标线。
- 按当前完整 benchmark 复核口径，`arith_add_loop` 与临时补充的 `arith_chain_temp` 仍高于 3x，需要继续作为短期优化目标；旧清单中的 `arith_mix_loop`、`closure_upvalue`、`stdlib_math_string`、`recursion` 本轮已低于 3x，但必须继续回归观察。
- 字符串拼接已较 2026-06-29 旧基线明显改善，从约 92x 收窄到约 1.70x。
- 后续优先优化方向应集中在 `arith_add_loop` 的 `ADD R,R` / `FORLOOP` 成本、稳定 benchmark harness、函数调用/upvalue 余量、VM dispatch code size 对无关路径的影响，以及标准库函数调用边界。
