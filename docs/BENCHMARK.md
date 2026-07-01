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
该口径已固化到 `scripts/benchmark-official.sh`，执行时显式传入官方 Lua 5.3.6 与本项目二进制：

```bash
LUA_BIN=<lua-5.3.6>/src/lua \
LUAC_BIN=<lua-5.3.6>/src/luac \
GLUA_BIN=./bin/glua \
GLUAC_BIN=./bin/gluac \
./scripts/benchmark-official.sh
```

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.007978s / 0.008171s | 0.024147s / 0.024050s | 3.03x / 2.94x |
| `arith_mix_loop` | 0.012123s / 0.012069s | 0.036264s / 0.036110s | 2.99x / 2.99x |
| `arith_chain_temp` | 0.013671s / 0.013745s | 0.041615s / 0.041806s | 3.04x / 3.04x |
| `table_rw` | 0.007561s / 0.007591s | 0.022541s / 0.022655s | 2.98x / 2.98x |
| `function_call` | 0.007370s / 0.007454s | 0.019381s / 0.019380s | 2.63x / 2.60x |
| `string_concat` | 0.005279s / 0.005360s | 0.009566s / 0.009634s | 1.81x / 1.80x |
| `closure_upvalue` | 0.009135s / 0.008513s | 0.021918s / 0.022009s | 2.40x / 2.59x |
| `stdlib_math_string` | 0.019856s / 0.020147s | 0.046101s / 0.046123s | 2.32x / 2.29x |
| `recursion` | 0.004164s / 0.004191s | 0.012543s / 0.012411s | 3.01x / 2.96x |
| `compile_3000_functions` | 0.005939s / 0.005624s | 0.015169s / 0.015002s | 2.55x / 2.67x |

本轮完整口径下仍高于 3x 的明确路径为 `arith_chain_temp`；`arith_add_loop` 与 `recursion`
受官方基线波动在 3x 附近，需要继续作为边缘观察项。`arith_mix_loop`、`table_rw`、
`function_call`、`closure_upvalue`、`stdlib_math_string` 与 `compile_3000_functions` 当前低于 3x，
但仍需作为回归观察项。
其中 `arith_chain_temp` 覆盖 `sum = sum + i * 3 - 7` 这类左结合自二元链，用于区分截图中
一度混用的 `arith_add_loop` 与混合算术链；该 fixture 已固化到 `scripts/benchmark-official.sh`，后续继续
作为长期回归项。`function_call` 本轮复测为 2.59x / 2.63x，低于 3x；`compile_3000_functions`
随官方工具中位数波动继续作为回归观察项。

#### 2026-07-01 table hash 懒分配复核

本轮只调整 `runtime.Table` 的 hash 区初始化策略：`NewTable` 不再为所有空表立即创建
`hashValues` 和 `hashKeys`，而是在首次 hash 写入前通过内部 `ensureHashStorage` 延迟创建。
数组区、raw get、raw next、弱表 sweep 和 delete nil map 均保持 Go 语义安全；测试中直接模拟
hash 区整数 key 的夹具显式初始化 hash 存储。该改动对齐 C Lua table 按实际数组/hash 需求
管理存储的方向，不改变字节码或 Lua 可观察语义。

Go 端 micro benchmark 复跑 5 次后，`BenchmarkDoStringTableReadWrite` 的 alloc/op 从约
`380 allocs` 降到 `372 allocs`，`BenchmarkDoStringArithAddLoop` 从约 `318 allocs` 降到
`312 allocs`，`BenchmarkDoStringRecursion` 从约 `526 allocs` 降到 `520 allocs`。完整官方脚本
两次复跑中，`table_rw` 项目绝对耗时为 `0.023583s` / `0.022989s`，倍率为 `3.07x` /
`2.99x`；该路径已经接近目标线，但仍需继续作为边缘回归项。

#### 2026-07-01 table 数组初始容量复核

本轮只调整数组区几何增长的初始容量：空数组区首次进入正整数数组写入时，从预留 4 个槽位改为
预留 8 个槽位。该改动只影响底层 slice capacity，不改变 `len(arrayValues)`，因此 `RawGet`、
`RawNext`、`Len` 和稀疏数组可见语义保持不变。`table_rw` 的两个热循环仍与官方 Lua 5.3.6 一致：
写入循环为 `SETTABLE; FORLOOP`，读取循环为 `GETTABLE; ADD; FORLOOP`；项目额外的两个 `JMP`
仍只位于循环退出后。

Go 端 `BenchmarkDoStringTableReadWrite` 复跑 5 次后，alloc/op 从 `372` 降到 `371`，耗时约
`1.46-1.56 ms/op`；`arith_chain_temp` 维持约 `3.62 ms/op`，没有明显回归。完整官方脚本两次
复跑中，`table_rw` 项目绝对耗时为 `0.021037s` / `0.021114s`，较上一轮 `0.021316s` 小幅下降；
倍率仍为 `3.04x` / `3.06x`，table 路径仍需继续作为短期目标。

#### 2026-07-01 执行期 upvalue cell 借用复核

本轮只调整 Lua closure 执行期 upvalue cell 绑定：保留公开 `BindUpvalueCells` 的复制语义，
新增执行器内部使用的 `BindBorrowedUpvalueCells`，直接借用 `LuaClosure.UpvalueCells` 切片头。
VM 只读取该切片并通过 cell 读写值，不修改切片结构；该模型对齐 Lua 5.3 closure 持有 UpVal
指针的实现，避免递归调用每帧复制 upvalue cell 切片。该改动不改变 codegen；`recursion` 的
`fib` 子函数热体仍与官方 Lua 5.3.6 一致：
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`。

Go 端 `BenchmarkDoStringRecursion` 复跑 5 次后，从上一轮约 `8.41-8.45 ms/op` 降到约
`7.65-8.05 ms/op`；alloc/op 从约 `403 KB` / `32095 allocs` 降到约 `151 KB` /
`526 allocs`。mem profile 中 `VM.BindUpvalueCells` 从约 98% alloc objects 的热点消失。
完整官方脚本三次复跑中，`recursion` 项目绝对耗时为 `0.012117s` / `0.012578s` /
`0.012342s`，低于上一轮约 `0.0129s`；倍率仍受官方基线波动影响，为 `3.15x` / `3.07x` /
`3.05x`，递归仍需继续优化。

#### 2026-07-01 执行期 upvalue 快照省略复核

本轮继续收窄递归调用成本：当 `LuaClosure` 已持有完整 `UpvalueCells` 时，执行期 VM 不再把
`closure.Upvalues` 快照复制进每个调用帧，而是直接通过共享 cell 读写 upvalue。为保持 Lua 5.3
闭包、`debug.getupvalue` / `debug.setupvalue`、`SETUPVAL`、`SETTABUP` 和子闭包捕获语义，
VM 的 upvalue 读写、判界与捕获统一改为优先检查共享 cell，只有无 cell 的旧路径继续读取快照。
该改动不改变 codegen；`recursion` 的 `fib` 子函数热体仍与官方 Lua 5.3.6 一致：
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`。

Go 端 `BenchmarkDoStringRecursion` 复跑 5 次后，从改动前约 `7.66-7.79 ms/op`、
`150.9-151.2 KB/op`、`520 allocs/op` 变为约 `7.48-8.36 ms/op`、`149.7 KB/op`、
`505 allocs/op`；收益主要体现在递归调用帧的 upvalue 快照分配减少。完整官方脚本两次复跑中，
`recursion` 项目绝对耗时为 `0.011476s` / `0.011297s`，低于上一轮约 `0.0121-0.0127s`；
但官方基线同轮为 `0.003677s` / `0.003689s`，倍率仍为 `3.12x` / `3.06x`。下一轮仍需优先
关注 `arith_chain_temp`、`arith_mix_loop`、`table_rw`、`arith_add_loop` 与 `recursion`。

#### 2026-07-01 CALL 协程状态复用复核

本轮只调整普通 Lua `CALL` 后处理：执行循环已经维护 `coroutinesCreated`，因此在 State 从未创建
coroutine 时，CALL 路径不再重复查询当前运行线程，也不再在 direct-call 判定中重复读取 State 的
coroutine 数量。已创建 coroutine、yield、continuation 和 hook 路径仍保留原查询与保存逻辑。
该改动不改变 codegen；`recursion` 的 `fib` 子函数热体仍与官方 Lua 5.3.6 一致：
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`，项目只少一个不可达
尾部 `RETURN`。

Go 端 `BenchmarkDoStringRecursion` 复跑 5 次后，从上一轮常见约 `8.50-8.60 ms/op`
降到约 `8.28-8.53 ms/op`，alloc/op 维持约 `403 KB` / `32094 allocs`。完整官方脚本两次复跑中，
`recursion` 项目绝对耗时为 `0.012973s` / `0.012894s`，低于上一轮 `0.013822s`；但官方基线同步
波动，倍率仍为 `3.25x` / `3.35x`，递归仍需继续优化。

#### 2026-07-01 SUB/MUL 右常量 integer cache 复核

本轮只调整 `SUB` / `MUL` integer inline cache：当首次完整执行确认形态为 `R - Kint` 或
`R * Kint` 后，后续命中直接复用右侧不可变 Proto integer 常量，只校验左侧寄存器仍为 integer。
若左侧类型变化或寄存器窗口变化，缓存会立即清空并回到完整 Lua 算术、字符串数字转换和元方法语义。
该改动不改变 codegen；`arith_chain_temp` 的热循环仍与官方 Lua 5.3.6 一致：
`MUL; ADD; SUB; FORLOOP`，项目额外的循环退出零距离 `JMP` 仍只服务 line hook。

Go 端 `BenchmarkDoStringArithChainTemp` 复跑 5 次后从本轮初始约 `3.82-4.35 ms/op`
降到约 `3.74-3.82 ms/op`，alloc/op 不变。完整官方脚本两次复跑中，
`arith_chain_temp` 项目绝对耗时为 `0.042499s` / `0.041891s`，较上一轮复核表中的
`0.041061s` 受构建和系统负载波动影响没有单调下降；但和同轮 helper 形态的
`0.044673s` / `0.043650s` 相比，内联右常量缓存降低了链式算术路径成本。后续仍需继续压低
`arith_chain_temp` 和递归路径。

#### 2026-07-01 递归 VM 池容量复核

本轮只把同寄存器窗口的 Lua VM pool 上限从 32 提高到 64，减少 `fib(15)` 递归调用链在同一
State 内反复创建 VM 的概率。该改动不改变 codegen；递归子函数热体仍与官方 Lua 5.3.6 一致：
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`，项目只少一个不可达
尾部 `RETURN`。

Go 端新增 `BenchmarkDoStringRecursion`，复跑 5 次后从约 `8.54-8.58 ms/op` 小幅降到约
`8.44-8.55 ms/op`，alloc/op 不变。完整官方脚本两次复跑中，本项目 `recursion` 绝对耗时为
`0.012477s` / `0.012268s`，低于上一轮 `0.012585s`；但官方基线波动到 `0.003473s` 时，
倍数仍为 3.53x，递归仍需继续优化。

#### 2026-07-01 table 连续数组追加复核

本轮只调整无元表 table 的正整数非 nil 写入路径：当 Lua 数组区是连续追加且已有预留容量时，
`RawSetPositiveIntegerNonNil` 直接 `append` 扩展一格，避免热循环每轮进入 `ensureArraySize`。
该改动不改变 codegen；`table_rw` 的两个热循环仍与官方 Lua 5.3.6 一致：写入循环为
`SETTABLE; FORLOOP`，读取循环为 `GETTABLE; ADD; FORLOOP`。项目额外的两个零距离 `JMP` 位于
循环退出后，只服务 line hook。

Go 端缩小版 table benchmark 复跑 5 次，`BenchmarkDoStringTableReadWrite` 从改动前约
`1.68 ms/op` 降到约 `1.49-1.61 ms/op`，alloc/op 不变。完整官方脚本中 `table_rw` 项目绝对耗时
较上一轮 `0.021510s` 到本轮两次复测的 `0.021020s` / `0.021823s`，受官方基线波动影响，
比值仍约 3.06x，需要继续作为短期目标。

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

#### 2026-07-01 字符串 table 读缓存懒分配复核

本轮只调整 VM 级字符串 table 读 inline cache 的分配时机：`BindPrototype` 切换 Proto 时仅失效旧缓存，
不再为每个 Lua 调用帧预分配 `stringTableReadCache`；首次遇到无元表 table 的字符串常量 key 读取时，
再按当前 Proto 指令数懒分配。该缓存只影响 `GETTABUP` / `GETTABLE` 的字符串 key 快路径，
未改变任何 Lua 5.3 table 读取、元方法、协程或 debug 语义。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`recursion` 的 `fib` 子函数热体仍为
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`；
`arith_chain_temp` 热循环仍为 `MUL; ADD; SUB; FORLOOP`；`arith_mix_loop` 热循环仍为
`MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP`。项目额外的循环退出零距离 `JMP` 不在热路径。

Go 端 micro benchmark 复跑 5 次后，`BenchmarkDoStringTableReadWrite` 从上一轮约 371 alloc/op
降到 370 alloc/op；`BenchmarkDoStringRecursion` 从约 505 alloc/op 降到 489 alloc/op。
交替 A/B 运行上一轮与本轮临时二进制各 40 次后，`recursion` 中位数为 `0.012317s` /
`0.012367s`，`table_rw` 中位数为 `0.022173s` / `0.022187s`，说明本轮主要收益是减少分配，
wall-clock 仍受当前 VM dispatch 与算术/调用成本主导。

完整官方脚本两次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008019s / 0.008056s | 0.023957s / 0.024011s | 2.99x / 2.98x |
| `arith_mix_loop` | 0.012036s / 0.011950s | 0.035924s / 0.035920s | 2.98x / 3.01x |
| `arith_chain_temp` | 0.013624s / 0.013596s | 0.041691s / 0.041667s | 3.06x / 3.06x |
| `table_rw` | 0.007536s / 0.007465s | 0.022277s / 0.022337s | 2.96x / 2.99x |
| `function_call` | 0.007525s / 0.007332s | 0.019158s / 0.019088s | 2.55x / 2.60x |
| `string_concat` | 0.005146s / 0.005165s | 0.009475s / 0.009591s | 1.84x / 1.86x |
| `closure_upvalue` | 0.008591s / 0.008588s | 0.021719s / 0.021720s | 2.53x / 2.53x |
| `stdlib_math_string` | 0.019956s / 0.019978s | 0.045643s / 0.045573s | 2.29x / 2.28x |
| `recursion` | 0.004146s / 0.004093s | 0.012444s / 0.012468s | 3.00x / 3.05x |
| `compile_3000_functions` | 0.005689s / 0.005701s | 0.014987s / 0.014892s | 2.63x / 2.61x |

当前仍需优先关注 `arith_chain_temp`、`arith_mix_loop` 与 `recursion`；`table_rw`、`arith_add_loop`
已低于 3x 但仍接近边缘，继续作为回归观察项。

#### 2026-07-01 固定参数函数寄存器数量早退复核

本轮只调整 Lua closure 调用前的寄存器窗口数量计算：非 vararg 函数不再逐次扫描 Proto 指令查找
开放 `VARARG`，直接按 `MaxStackSize`、固定参数数量和实参数量计算寄存器窗口。该逻辑与 Lua 5.3
固定参数函数语义一致，不改变 vararg 函数、debug 帧、协程 continuation、upvalue 或返回值行为。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`recursion` 的 `fib` 子函数热体仍为
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`；
`arith_chain_temp` 热循环仍为 `MUL; ADD; SUB; FORLOOP`。项目主函数在循环退出处仍有一次额外
零距离 `JMP`，但不在递归 `fib` 热体内。

Go 端 micro benchmark 复跑 5 次后，`BenchmarkDoStringRecursion` 从本轮 profile 基线约
`7.68 ms/op` 降到约 `7.53-7.56 ms/op`，alloc/op 维持 `489`；`BenchmarkDoStringArithChainTemp`
维持约 `3.72-3.73 ms/op`，未出现算术链回归。完整官方脚本两次复跑中，`recursion` 为
`3.01x` / `2.96x`，`function_call` 为 `2.63x` / `2.60x`；`arith_chain_temp` 仍为 `3.04x`，
下一轮继续优先优化算术链与 3x 边缘项。

#### 2026-07-01 SUB/MUL 右常量缓存热分支瘦身复核

本轮只调整 VM 中 `SUB` / `MUL` integer inline cache 的右侧 integer 常量命中路径：
调用方已完成目标寄存器越界检查后，将 `targetIndex` 传入缓存函数，避免热分支再次从指令中解析
`A`；同时在该热分支局部复用寄存器切片，并用单次 `uint` 边界检查覆盖负索引与越界索引。
非右常量 `SUB` / `MUL` 缓存路径复用已有通用 integer cache helper，保持缓存失效、类型变化、
常量操作数、元方法回退和错误语义不变。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`arith_chain_temp` 热循环仍为
`MUL; ADD; SUB; FORLOOP`；`recursion` 的 `fib` 子函数热体仍为
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`。
项目主函数循环退出处额外的零距离 `JMP` 不在热循环内。

Go 端 micro benchmark 复跑 5 次后，`BenchmarkDoStringArithChainTemp` 从本轮初始约
`3.73 ms/op` 收窄到约 `3.71-3.72 ms/op`，alloc/op 维持 `320`；`BenchmarkDoStringRecursion`
约 `7.48-7.55 ms/op`，alloc/op 维持 `489`，未出现明显回归。完整官方脚本两次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008265s / 0.008166s | 0.024236s / 0.024148s | 2.93x / 2.96x |
| `arith_mix_loop` | 0.012158s / 0.012017s | 0.036195s / 0.035877s | 2.98x / 2.99x |
| `arith_chain_temp` | 0.013786s / 0.013703s | 0.041558s / 0.041449s | 3.01x / 3.02x |
| `table_rw` | 0.007640s / 0.007529s | 0.022627s / 0.022361s | 2.96x / 2.97x |
| `function_call` | 0.007507s / 0.007307s | 0.019242s / 0.019210s | 2.56x / 2.63x |
| `string_concat` | 0.005624s / 0.005412s | 0.009617s / 0.009762s | 1.71x / 1.80x |
| `closure_upvalue` | 0.008540s / 0.008763s | 0.021849s / 0.022049s | 2.56x / 2.52x |
| `stdlib_math_string` | 0.020077s / 0.020130s | 0.046085s / 0.046046s | 2.30x / 2.29x |
| `recursion` | 0.004320s / 0.004311s | 0.012553s / 0.012520s | 2.91x / 2.90x |
| `compile_3000_functions` | 0.005898s / 0.005925s | 0.015291s / 0.015189s | 2.59x / 2.56x |

当前明确仍需继续优化的路径为 `arith_chain_temp`；`arith_add_loop`、`arith_mix_loop`、`table_rw`
和 `recursion` 虽低于或贴近 3x，但仍需作为边缘回归观察项。

#### 2026-07-01 ADD integer cache 命中路径瘦身复核

本轮只调整 VM 中既有 `ADD` integer inline cache 的命中路径：局部复用寄存器切片，用单次
`uint` 边界检查覆盖负索引与越界索引，并复用调用方已校验的 `targetIndex` 写回结果。该改动不新增
ADD 双寄存器缓存形态，不改变缓存建立、缓存失效、常量操作数、number fallback、字符串数字转换、
元方法回退或错误语义。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`arith_chain_temp` 热循环仍为
`MUL; ADD; SUB; FORLOOP`，指令数、寄存器占用和常量访问与官方热循环一致；项目主函数循环退出处
额外的零距离 `JMP` 仍只发生在循环结束后，不在热路径。

Go 端 micro benchmark 复跑 5 次后，`BenchmarkDoStringArithAddLoop` 从上一轮约 `1.98-1.99 ms/op`
降到约 `1.94-1.96 ms/op`；`BenchmarkDoStringArithChainTemp` 从约 `3.71-3.72 ms/op`
降到约 `3.69-3.72 ms/op`，alloc/op 维持 `320`。完整官方脚本三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008154s / 0.008187s / 0.008197s | 0.023483s / 0.024517s / 0.023853s | 2.88x / 2.99x / 2.91x |
| `arith_mix_loop` | 0.012001s / 0.012307s / 0.012127s | 0.035642s / 0.036404s / 0.036352s | 2.97x / 2.96x / 3.00x |
| `arith_chain_temp` | 0.013459s / 0.013967s / 0.013996s | 0.040967s / 0.041345s / 0.041442s | 3.04x / 2.96x / 2.96x |
| `table_rw` | 0.007468s / 0.007577s / 0.007632s | 0.022179s / 0.022216s / 0.022802s | 2.97x / 2.93x / 2.99x |
| `function_call` | 0.007247s / 0.007372s / 0.007411s | 0.019074s / 0.019757s / 0.019435s | 2.63x / 2.68x / 2.62x |
| `string_concat` | 0.005104s / 0.005171s / 0.005377s | 0.009339s / 0.009599s / 0.009911s | 1.83x / 1.86x / 1.84x |
| `closure_upvalue` | 0.008629s / 0.008462s / 0.008808s | 0.021718s / 0.021740s / 0.022153s | 2.52x / 2.57x / 2.52x |
| `stdlib_math_string` | 0.020071s / 0.019998s / 0.020319s | 0.045713s / 0.049228s / 0.045949s | 2.28x / 2.46x / 2.26x |
| `recursion` | 0.004020s / 0.004319s / 0.004232s | 0.012269s / 0.012468s / 0.012496s | 3.05x / 2.89x / 2.95x |
| `compile_3000_functions` | 0.005619s / 0.005897s / 0.005948s | 0.014869s / 0.015069s / 0.015059s | 2.65x / 2.56x / 2.53x |

当前没有三轮均明确高于 3x 的路径；`arith_chain_temp`、`arith_mix_loop`、`table_rw`、`recursion`
和 `arith_add_loop` 仍处于 3x 边缘，需要继续作为短期回归观察和优化目标。

#### 2026-07-01 MOD/IDIV integer cache 命中路径瘦身复核

本轮只调整 VM 中既有 `MOD` / `IDIV` integer inline cache 的命中路径：复用调用方已校验的
`targetIndex`，局部复用寄存器切片，并用单次 `uint` 边界检查覆盖负索引与越界索引。该改动不新增
除法类缓存形态，不改变零除错误、Lua floor modulo / floor division 语义、缓存建立、缓存失效、
number fallback、字符串数字转换或元方法回退。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`arith_mix_loop` 热循环仍为
`MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP`，7 slots、7 constants 与官方热循环一致；
`arith_chain_temp` 热循环仍为 `MUL; ADD; SUB; FORLOOP`。项目额外的循环退出零距离 `JMP`
仍不在热路径。

Go 端现有 micro benchmark 复跑 5 次后，`BenchmarkDoStringArithAddLoop` 维持约
`1.93-1.94 ms/op`，`BenchmarkDoStringArithChainTemp` 维持约 `3.67-3.71 ms/op`，
`BenchmarkDoStringRecursion` 约 `7.52-7.61 ms/op`，alloc/op 均未回归。完整官方脚本三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008362s / 0.008240s / 0.008135s | 0.023714s / 0.023840s / 0.023837s | 2.84x / 2.89x / 2.93x |
| `arith_mix_loop` | 0.012077s / 0.012194s / 0.012190s | 0.035716s / 0.035976s / 0.036026s | 2.96x / 2.95x / 2.96x |
| `arith_chain_temp` | 0.013629s / 0.013781s / 0.013740s | 0.041369s / 0.041439s / 0.041382s | 3.04x / 3.01x / 3.01x |
| `table_rw` | 0.007611s / 0.007709s / 0.007721s | 0.022358s / 0.022548s / 0.022546s | 2.94x / 2.93x / 2.92x |
| `function_call` | 0.007338s / 0.007419s / 0.007513s | 0.019198s / 0.019444s / 0.019478s | 2.62x / 2.62x / 2.59x |
| `string_concat` | 0.005322s / 0.005920s / 0.005318s | 0.009678s / 0.009730s / 0.009790s | 1.82x / 1.64x / 1.84x |
| `closure_upvalue` | 0.008583s / 0.008728s / 0.008732s | 0.022067s / 0.021905s / 0.022287s | 2.57x / 2.51x / 2.55x |
| `stdlib_math_string` | 0.020157s / 0.019862s / 0.020090s | 0.046005s / 0.045776s / 0.046248s | 2.28x / 2.30x / 2.30x |
| `recursion` | 0.004247s / 0.004169s / 0.004255s | 0.012394s / 0.012400s / 0.012384s | 2.92x / 2.97x / 2.91x |
| `compile_3000_functions` | 0.005807s / 0.005848s / 0.006014s | 0.015147s / 0.015229s / 0.015069s | 2.61x / 2.60x / 2.51x |

当前 `arith_chain_temp` 仍略高于 3x，属于明确优先项；`arith_mix_loop`、`table_rw`、`recursion`
和 `arith_add_loop` 在本轮三次复核中低于 3x，但继续作为边缘回归观察项。

#### 2026-07-01 算术缓存入口检查瘦身复核

本轮只调整 VM 中既有 `ADD` / `SUB` / `MUL` integer inline cache 的入口检查：局部复用
`arithmeticIntRegisterCache` 与 `arithmeticIntOperandCache` 切片，并用单次 `uint` 边界检查覆盖
负 PC 与越界 PC。该改动不新增缓存形态，不改变缓存命中、缓存失效、右常量缓存、number fallback、
字符串数字转换或元方法回退语义。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`arith_chain_temp` 热循环仍为
`MUL; ADD; SUB; FORLOOP`，指令数、寄存器占用和常量访问与官方热循环一致；项目额外的循环退出
零距离 `JMP` 不在热路径。

Go 端 micro benchmark 复跑 5 次后，`BenchmarkDoStringArithChainTemp` 约 `3.69 ms/op`，
`BenchmarkDoStringRecursion` 约 `7.46-7.51 ms/op`，alloc/op 未变化。完整官方脚本三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008109s / 0.008597s / 0.008172s | 0.023592s / 0.023488s / 0.023857s | 2.91x / 2.73x / 2.92x |
| `arith_mix_loop` | 0.012024s / 0.011966s / 0.012238s | 0.035818s / 0.035816s / 0.035664s | 2.98x / 2.99x / 2.91x |
| `arith_chain_temp` | 0.013877s / 0.013898s / 0.013614s | 0.041302s / 0.041421s / 0.041104s | 2.98x / 2.98x / 3.02x |
| `table_rw` | 0.007692s / 0.007610s / 0.007641s | 0.022570s / 0.022734s / 0.022476s | 2.93x / 2.99x / 2.94x |
| `function_call` | 0.007443s / 0.007377s / 0.007387s | 0.019392s / 0.019432s / 0.019439s | 2.61x / 2.63x / 2.63x |
| `string_concat` | 0.005360s / 0.005348s / 0.005287s | 0.009642s / 0.009937s / 0.009851s | 1.80x / 1.86x / 1.86x |
| `closure_upvalue` | 0.008632s / 0.008701s / 0.008690s | 0.022156s / 0.022127s / 0.022283s | 2.57x / 2.54x / 2.56x |
| `stdlib_math_string` | 0.020093s / 0.020162s / 0.020130s | 0.046182s / 0.046283s / 0.046277s | 2.30x / 2.30x / 2.30x |
| `recursion` | 0.004249s / 0.004428s / 0.004236s | 0.012487s / 0.012442s / 0.012526s | 2.94x / 2.81x / 2.96x |
| `compile_3000_functions` | 0.005975s / 0.005950s / 0.005818s | 0.015199s / 0.015242s / 0.015213s | 2.54x / 2.56x / 2.61x |

当前没有三轮均明确高于 3x 的路径；`arith_chain_temp`、`arith_mix_loop`、`table_rw`、`recursion`
和 `arith_add_loop` 仍贴近 3x，需要继续作为边缘回归观察项。

#### 2026-07-01 ADD 双寄存器缓存命中分支复核

本轮只在既有 `ADD` integer inline cache 命中路径中新增双寄存器窄执行分支：当缓存记录确认左右
操作数都来自寄存器时，直接读取两个寄存器并校验 `KindInteger`，避免每轮重复检查常量操作数形态。
该改动不新增缓存形态，不改变缓存记录、缓存失效、常量操作数、number fallback、字符串数字转换或
元方法回退语义。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`arith_chain_temp` 热循环仍为
`MUL; ADD; SUB; FORLOOP`，指令数、寄存器占用和常量访问与官方热循环一致；项目额外的循环退出
零距离 `JMP` 不在热路径。

Go 端 micro benchmark 复跑 6 次后，`BenchmarkDoStringArithAddLoop` 约 `1.92-1.94 ms/op`，
`BenchmarkDoStringArithChainTemp` 约 `3.67-3.68 ms/op`，`BenchmarkDoStringRecursion`
约 `7.48-7.56 ms/op`，alloc/op 未变化。

复核时发现本机 PATH 上的 `lua` / `luac` 已变为 Lua 5.5.0，会让默认官方脚本口径偏离本项目
要求的 Lua 5.3.6 基线。本轮已为 `scripts/benchmark-official.sh` 增加版本门禁：官方 `lua`
和 `luac` 必须输出 `Lua 5.3.6`，否则脚本直接失败并要求通过 `LUA_BIN` / `LUAC_BIN` 指定
官方 Lua 5.3.6 工具。以下是显式使用 Lua 5.3.6 官方工具后的三次复跑：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008314s / 0.008208s / 0.008111s | 0.024074s / 0.024062s / 0.023935s | 2.90x / 2.93x / 2.95x |
| `arith_mix_loop` | 0.012343s / 0.012402s / 0.012243s | 0.036350s / 0.036087s / 0.035943s | 2.95x / 2.91x / 2.94x |
| `arith_chain_temp` | 0.013797s / 0.013820s / 0.013825s | 0.041610s / 0.041430s / 0.041526s | 3.02x / 3.00x / 3.00x |
| `table_rw` | 0.007741s / 0.007743s / 0.007868s | 0.023266s / 0.023294s / 0.023430s | 3.01x / 3.01x / 2.98x |
| `function_call` | 0.007368s / 0.007368s / 0.007489s | 0.020344s / 0.019647s / 0.019556s | 2.76x / 2.67x / 2.61x |
| `string_concat` | 0.006745s / 0.005430s / 0.005302s | 0.009972s / 0.009896s / 0.009920s | 1.48x / 1.82x / 1.87x |
| `closure_upvalue` | 0.011494s / 0.008777s / 0.009039s | 0.022153s / 0.022427s / 0.022398s | 1.93x / 2.56x / 2.48x |
| `stdlib_math_string` | 0.021885s / 0.019968s / 0.020227s | 0.047434s / 0.046104s / 0.046243s | 2.17x / 2.31x / 2.29x |
| `recursion` | 0.004347s / 0.004219s / 0.004209s | 0.012729s / 0.012443s / 0.012499s | 2.93x / 2.95x / 2.97x |
| `compile_3000_functions` | 0.005944s / 0.005939s / 0.005925s | 0.015517s / 0.015402s / 0.015378s | 2.61x / 2.59x / 2.60x |

当前正确 Lua 5.3.6 口径下，`arith_chain_temp` 仍在 3x 附近或略高于 3x，`table_rw` 两轮略高于
3x、一轮低于 3x；`arith_mix_loop`、`recursion` 和 `arith_add_loop` 仍作为边缘回归观察项。

#### 2026-07-01 大数组区扩容策略复核

`table_rw` profile 显示，连续数组写入的主要成本来自数组区扩容后的清零、复制与 GC 扫描；
`GETTABLE` / `SETTABLE` 指令本身不是最大 flat 热点。本轮只调整 table 数组区容量策略：数组区容量
小于 64K 时仍按 2 倍增长，保持中小表追加的摊还 O(1) 成本；超过 64K 后改为 1.5 倍增长，减少
大表场景超过实际长度的 `Value` 槽位和 GC 扫描压力。该改动不改变 Lua 可见长度、正整数 key、
nil 删除、hash 区、元表或 `next` 语义。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`table_rw` 热写循环仍为
`SETTABLE; FORLOOP`，热读循环仍为 `GETTABLE; ADD; FORLOOP`；项目额外的循环退出零距离 `JMP`
不在热路径。

Go 端 micro benchmark 复跑 5 次后，`BenchmarkDoStringTableReadWrite` 从本轮 baseline 约
`1.58-1.68 ms/op` 降到约 `1.50-1.63 ms/op`；`BenchmarkTableReadWrite/raw_set_integer`
维持约 `5.85-5.90 ns/op`，`raw_get_integer` 维持约 `4.90 ns/op`，alloc/op 未变化。重建
`bin/glua` / `bin/gluac` 后，完整官方脚本三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008392s / 0.008101s / 0.008230s | 0.023502s / 0.023413s / 0.023573s | 2.80x / 2.89x / 2.86x |
| `arith_mix_loop` | 0.012118s / 0.012195s / 0.012123s | 0.035714s / 0.035781s / 0.035632s | 2.95x / 2.93x / 2.94x |
| `arith_chain_temp` | 0.013854s / 0.013742s / 0.013859s | 0.041092s / 0.041304s / 0.041376s | 2.97x / 3.01x / 2.99x |
| `table_rw` | 0.007756s / 0.007676s / 0.007656s | 0.021869s / 0.022078s / 0.022153s | 2.82x / 2.88x / 2.89x |
| `function_call` | 0.007372s / 0.007508s / 0.007418s | 0.019178s / 0.019610s / 0.019451s | 2.60x / 2.61x / 2.62x |
| `string_concat` | 0.005281s / 0.005399s / 0.005318s | 0.009715s / 0.009783s / 0.009812s | 1.84x / 1.81x / 1.84x |
| `closure_upvalue` | 0.008732s / 0.008794s / 0.008742s | 0.022068s / 0.022135s / 0.022008s | 2.53x / 2.52x / 2.52x |
| `stdlib_math_string` | 0.020218s / 0.020127s / 0.019959s | 0.046130s / 0.045691s / 0.045774s | 2.28x / 2.27x / 2.29x |
| `recursion` | 0.004257s / 0.004274s / 0.004171s | 0.012548s / 0.012429s / 0.012398s | 2.95x / 2.91x / 2.97x |
| `compile_3000_functions` | 0.005884s / 0.005771s / 0.005832s | 0.015201s / 0.015105s / 0.015134s | 2.58x / 2.62x / 2.59x |

当前正确 Lua 5.3.6 口径下，`table_rw` 已回到 3x 以下；`arith_chain_temp` 仍在 3x 附近，
是下一轮明确优先项。`arith_mix_loop`、`arith_add_loop` 与 `recursion` 继续作为边缘回归观察项。

#### 2026-07-01 SUB/MUL 右常量寄存器读取复核

`arith_chain_temp` profile 显示，剩余主要成本集中在 `SUB` / `MUL` 右 integer 常量缓存命中路径、
`ADD` 双寄存器缓存命中路径和 `FORLOOP`。本轮只调整既有 `SUB` / `MUL` 右常量缓存命中路径的
寄存器读取方式：命中时不再先复制完整 `Value` 结构，而是按索引读取 `Kind` 和 `Integer` 字段，
减少热路径结构体拷贝。该改动不新增缓存形态，不改变缓存失效、右常量、integer 溢出、number
fallback、字符串数字转换或元方法回退语义。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`arith_chain_temp` 热循环仍为
`MUL; ADD; SUB; FORLOOP`，指令数、寄存器占用和常量访问与官方热循环一致；项目额外的循环退出
零距离 `JMP` 不在热路径。

Go 端 micro benchmark 复跑 8 次后，`BenchmarkDoStringArithChainTemp` 多数样本从本轮 baseline
约 `3.69 ms/op` 降到约 `3.55-3.57 ms/op`，其中一轮调度噪声为 `3.78 ms/op`；
`BenchmarkDoStringRecursion` 维持约 `7.50-7.57 ms/op`。重建 `bin/glua` / `bin/gluac` 后，
完整官方脚本三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008041s / 0.008296s / 0.008177s | 0.023368s / 0.023656s / 0.023637s | 2.91x / 2.85x / 2.89x |
| `arith_mix_loop` | 0.012096s / 0.012077s / 0.012112s | 0.035081s / 0.035079s / 0.035021s | 2.90x / 2.90x / 2.89x |
| `arith_chain_temp` | 0.013644s / 0.013636s / 0.013662s | 0.040391s / 0.040035s / 0.040250s | 2.96x / 2.94x / 2.95x |
| `table_rw` | 0.007718s / 0.007778s / 0.007668s | 0.022464s / 0.022197s / 0.022226s | 2.91x / 2.85x / 2.90x |
| `function_call` | 0.007463s / 0.007395s / 0.007467s | 0.019378s / 0.019376s / 0.019457s | 2.60x / 2.62x / 2.61x |
| `string_concat` | 0.005279s / 0.005363s / 0.005403s | 0.009824s / 0.009754s / 0.009866s | 1.86x / 1.82x / 1.83x |
| `closure_upvalue` | 0.008713s / 0.008754s / 0.008675s | 0.022166s / 0.022120s / 0.022110s | 2.54x / 2.53x / 2.55x |
| `stdlib_math_string` | 0.020154s / 0.020070s / 0.020094s | 0.045525s / 0.045930s / 0.046006s | 2.26x / 2.29x / 2.29x |
| `recursion` | 0.004282s / 0.004309s / 0.004212s | 0.012415s / 0.012551s / 0.012481s | 2.90x / 2.91x / 2.96x |
| `compile_3000_functions` | 0.005851s / 0.005909s / 0.005955s | 0.015128s / 0.015413s / 0.015376s | 2.59x / 2.61x / 2.58x |

当前正确 Lua 5.3.6 口径下，`arith_chain_temp`、`table_rw`、`arith_mix_loop`、`arith_add_loop`
和 `recursion` 均低于 3x；后续仍需作为边缘回归观察项继续复核。

#### 2026-07-01 ADD 双寄存器读取复核

`arith_chain_temp` 的剩余 profile 中，`ADD` 双寄存器缓存命中路径仍是主要 flat 热点之一。
本轮只调整既有 `ADD` 双寄存器缓存命中分支的寄存器读取方式：命中时不再把左右操作数完整
`Value` 复制到局部变量，而是先检查 `Kind`，再读取两个 `Integer` 局部值后写回目标寄存器。
该改动不新增缓存形态，不改变缓存记录、缓存失效、常量操作数、integer 溢出、number fallback、
字符串数字转换或元方法回退语义；目标寄存器与左右操作数别名时，两个 integer 值也会在写回前完成读取。

该改动不改变 codegen。使用官方 Lua 5.3.6 反汇编复核，`arith_chain_temp` 热循环仍为
`MUL; ADD; SUB; FORLOOP`，指令数、寄存器占用和常量访问与官方热循环一致；项目额外的循环退出
零距离 `JMP` 不在热路径。

Go 端 micro benchmark 复跑 8 次后，`BenchmarkDoStringArithAddLoop` 从上一轮约 `1.91-1.95 ms/op`
降到约 `1.74-1.77 ms/op`，`BenchmarkDoStringArithChainTemp` 维持并小幅收窄到约
`3.52-3.56 ms/op`，alloc/op 不变。重建 `bin/glua` / `bin/gluac` 后，完整官方脚本三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008144s / 0.008237s / 0.008228s | 0.021687s / 0.021818s / 0.021847s | 2.66x / 2.65x / 2.66x |
| `arith_mix_loop` | 0.012080s / 0.012136s / 0.012106s | 0.034433s / 0.034367s / 0.034502s | 2.85x / 2.83x / 2.85x |
| `arith_chain_temp` | 0.013704s / 0.013682s / 0.013726s | 0.039240s / 0.039331s / 0.039305s | 2.86x / 2.87x / 2.86x |
| `table_rw` | 0.007589s / 0.007467s / 0.007645s | 0.021495s / 0.021383s / 0.021424s | 2.83x / 2.86x / 2.80x |
| `function_call` | 0.007413s / 0.007336s / 0.007374s | 0.019359s / 0.019436s / 0.019372s | 2.61x / 2.65x / 2.63x |
| `string_concat` | 0.005231s / 0.005241s / 0.005264s | 0.009714s / 0.009671s / 0.009591s | 1.86x / 1.85x / 1.82x |
| `closure_upvalue` | 0.008636s / 0.008409s / 0.008552s | 0.022047s / 0.021995s / 0.022049s | 2.55x / 2.62x / 2.58x |
| `stdlib_math_string` | 0.020028s / 0.019926s / 0.020053s | 0.045601s / 0.045478s / 0.045763s | 2.28x / 2.28x / 2.28x |
| `recursion` | 0.004148s / 0.004144s / 0.004228s | 0.012489s / 0.012451s / 0.012468s | 3.01x / 3.00x / 2.95x |
| `compile_3000_functions` | 0.005671s / 0.005813s / 0.005930s | 0.015063s / 0.015080s / 0.015110s | 2.66x / 2.59x / 2.55x |

当前正确 Lua 5.3.6 口径下，算术链路和表读写均低于 3x；`recursion` 仍有一轮在 3x 边缘上方，
因此继续作为明确边缘回归观察项。后续优先关注 `recursion`、`arith_chain_temp`、`table_rw`、
`arith_mix_loop` 与 `arith_add_loop` 的跨轮稳定性。

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
| `BenchmarkDoStringTableReadWrite` | 约 1.55-1.68 ms/op，约 3.79 MB/op，370 allocs |
| `BenchmarkDoStringRecursion` | 约 7.58-7.70 ms/op，约 135.5 KB/op，489 allocs |

### 2026-07-01 debug hook 状态快路径复核

本轮在 `debug` 环境未设置任何协程专属 hook 时，`activeThreadHookState` 直接返回默认 hook 路径，
避免无 hook 热路径每次检查都读取当前 running thread。该改动不改变 `debug.sethook(thread, ...)`
语义；一旦存在协程专属 hook，仍按当前 running thread 隔离读取 hook 状态。

字节码复核结果不变：`recursion` 的 `fib` 子函数热体仍与官方 Lua 5.3.6 一致，为
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`；`arith_chain_temp`
热循环仍为 `MUL; ADD; SUB; FORLOOP`。项目主函数循环退出处额外零距离 `JMP` 不在热路径。

Go 端 micro 复跑显示，`BenchmarkDoStringFunctionCall` 多数轮约 `0.43-0.45 ms/op`，
`BenchmarkDoStringRecursion` 多数轮约 `7.46-7.53 ms/op`，alloc/op 未变化。重建 `bin/glua`
/ `bin/gluac` 后，正确 Lua 5.3.6 完整 benchmark 三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008015s / 0.008169s / 0.008221s | 0.021716s / 0.021841s / 0.021743s | 2.71x / 2.67x / 2.64x |
| `arith_mix_loop` | 0.011952s / 0.012066s / 0.011972s | 0.034180s / 0.034432s / 0.034276s | 2.86x / 2.85x / 2.86x |
| `arith_chain_temp` | 0.013608s / 0.013908s / 0.013725s | 0.039235s / 0.039436s / 0.039356s | 2.88x / 2.84x / 2.87x |
| `table_rw` | 0.007699s / 0.007607s / 0.007595s | 0.021814s / 0.021581s / 0.022009s | 2.83x / 2.84x / 2.90x |
| `function_call` | 0.007430s / 0.007382s / 0.007352s | 0.019200s / 0.019226s / 0.019197s | 2.58x / 2.60x / 2.61x |
| `string_concat` | 0.005289s / 0.005324s / 0.005330s | 0.009734s / 0.009760s / 0.009705s | 1.84x / 1.83x / 1.82x |
| `closure_upvalue` | 0.008603s / 0.008528s / 0.008604s | 0.021813s / 0.021825s / 0.021723s | 2.54x / 2.56x / 2.52x |
| `stdlib_math_string` | 0.020045s / 0.020355s / 0.019964s | 0.045808s / 0.045755s / 0.045883s | 2.29x / 2.25x / 2.30x |
| `recursion` | 0.004152s / 0.004230s / 0.004268s | 0.012533s / 0.012480s / 0.012467s | 3.02x / 2.95x / 2.92x |
| `compile_3000_functions` | 0.005844s / 0.005787s / 0.005952s | 0.015105s / 0.015083s / 0.015212s | 2.58x / 2.61x / 2.56x |

### 2026-07-01 debug 协程 hook map 懒初始化复核

本轮将 `debug.Environment.threadHooks` 从 `NewEnvironment` 阶段 eager map 分配改为
`debug.sethook(thread, ...)` 首次设置协程专属 hook 时懒初始化。nil map 的读取、`len` 和
`delete` 行为与空 map 一致，因此未设置协程 hook 的路径保持 Lua 可见语义不变；设置协程 hook
时仍会创建 map 并按 running thread 隔离 hook 状态。

Go 端 micro 复跑显示，`BenchmarkDoStringArithChainTemp`、`BenchmarkDoStringTableReadWrite`、
`BenchmarkDoStringFunctionCall`、`BenchmarkDoStringStringConcat` 和 `BenchmarkDoStringRecursion`
均减少 1 alloc/op；`BenchmarkDoStringRecursion` 多数轮约 `7.43-7.53 ms/op`，alloc/op 从
`489` 降到 `488`。重建 `bin/glua` / `bin/gluac` 后，正确 Lua 5.3.6 完整 benchmark 三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008137s / 0.008523s / 0.008452s | 0.021624s / 0.021825s / 0.021956s | 2.66x / 2.56x / 2.60x |
| `arith_mix_loop` | 0.012034s / 0.012093s / 0.012144s | 0.034238s / 0.034528s / 0.034102s | 2.85x / 2.86x / 2.81x |
| `arith_chain_temp` | 0.013592s / 0.013796s / 0.013645s | 0.038953s / 0.039322s / 0.039180s | 2.87x / 2.85x / 2.87x |
| `table_rw` | 0.007518s / 0.007649s / 0.007655s | 0.021325s / 0.021704s / 0.021444s | 2.84x / 2.84x / 2.80x |
| `function_call` | 0.007394s / 0.007589s / 0.007476s | 0.019021s / 0.019307s / 0.019156s | 2.57x / 2.54x / 2.56x |
| `string_concat` | 0.005253s / 0.005435s / 0.005356s | 0.009388s / 0.009738s / 0.009759s | 1.79x / 1.79x / 1.82x |
| `closure_upvalue` | 0.008626s / 0.008604s / 0.008728s | 0.021973s / 0.021817s / 0.021887s | 2.55x / 2.54x / 2.51x |
| `stdlib_math_string` | 0.019971s / 0.020228s / 0.020213s | 0.045625s / 0.046074s / 0.045826s | 2.28x / 2.28x / 2.27x |
| `recursion` | 0.004325s / 0.004299s / 0.004153s | 0.012412s / 0.012536s / 0.012500s | 2.87x / 2.92x / 3.01x |
| `compile_3000_functions` | 0.005748s / 0.005891s / 0.005794s | 0.015216s / 0.015145s / 0.015152s | 2.65x / 2.57x / 2.62x |

### 2026-07-02 debug hook 活跃状态缓存复核

本轮在 `debug` 环境中维护默认 hook 是否可能触发和活跃协程专属 hook 数量两个派生状态，使
`HasActiveHook` 在无默认 hook 且无活跃协程 hook 时直接返回 false，避免 `recursion` 等无 hook
热路径重复读取默认 hook 三元组和 running thread。`debug.sethook` 是唯一写入口；重复设置同一
thread、设置空 mask/count 和清除 hook 都会同步维护活跃计数，因此不改变 `debug.sethook`、
`debug.gethook`、协程专属 hook、call/return/line/count hook 或 hook 重入语义。

字节码复核结果不变：`recursion` 的 `fib` 子函数热体仍与官方 Lua 5.3.6 一致，为
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`；`arith_chain_temp`
热循环仍为 `MUL; ADD; SUB; FORLOOP`。项目主函数循环退出处额外零距离 `JMP` 不在热路径。

Go 端 micro 复跑显示，`BenchmarkDoStringFunctionCall` 多数轮约 `0.427-0.439 ms/op`，
`BenchmarkDoStringRecursion` 多数轮约 `7.41-7.45 ms/op`，alloc/op 维持 `488`。重建
`bin/glua` / `bin/gluac` 后，正确 Lua 5.3.6 完整 benchmark 三次复跑如下：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.008152s / 0.008116s / 0.008080s | 0.021563s / 0.021655s / 0.021620s | 2.65x / 2.67x / 2.68x |
| `arith_mix_loop` | 0.012032s / 0.012062s / 0.011950s | 0.034203s / 0.034319s / 0.034341s | 2.84x / 2.85x / 2.87x |
| `arith_chain_temp` | 0.013654s / 0.013607s / 0.013632s | 0.039148s / 0.039123s / 0.039277s | 2.87x / 2.88x / 2.88x |
| `table_rw` | 0.007587s / 0.007540s / 0.007582s | 0.021469s / 0.021275s / 0.021319s | 2.83x / 2.82x / 2.81x |
| `function_call` | 0.007375s / 0.007391s / 0.007323s | 0.018761s / 0.018779s / 0.018861s | 2.54x / 2.54x / 2.58x |
| `string_concat` | 0.005204s / 0.005213s / 0.005247s | 0.009415s / 0.009381s / 0.009348s | 1.81x / 1.80x / 1.78x |
| `closure_upvalue` | 0.008695s / 0.008624s / 0.008552s | 0.021383s / 0.021418s / 0.021442s | 2.46x / 2.48x / 2.51x |
| `stdlib_math_string` | 0.019986s / 0.020050s / 0.019962s | 0.045220s / 0.045273s / 0.045261s | 2.26x / 2.26x / 2.27x |
| `recursion` | 0.004134s / 0.004132s / 0.004152s | 0.012273s / 0.012272s / 0.012258s | 2.97x / 2.97x / 2.95x |
| `compile_3000_functions` | 0.005774s / 0.005741s / 0.005687s | 0.014900s / 0.014972s / 0.014779s | 2.58x / 2.61x / 2.60x |

### 结论

- CLI 冷启动和小脚本差距较小，历史冷启动约 1.25x 到 1.35x；本轮 Lua 5.3.6 正确口径下 `compile_3000_functions` 为 2.58x / 2.61x / 2.60x，仍低于当前 3x 目标线。
- 按当前完整 benchmark 复核口径，所有主要脚本运行与编译路径三轮均低于 3x；`recursion` 已从上一轮边缘 3.01x 收窄到 2.97x / 2.97x / 2.95x，仍作为边缘回归观察项继续跟踪。
- 字符串拼接已较 2026-06-29 旧基线明显改善，从约 92x 收窄到约 1.86x。
- 后续优先优化方向应集中在算术链 `ADD`/`SUB`/`MUL` 与 `FORLOOP` 成本、递归函数调用边界、表读写热路径、VM dispatch code size 对无关路径的影响，以及标准库函数调用边界。
