# glua 下一阶段性能优化 TODO

## 目标与边界

本文记录 `quanquan/feature/glua-next-perf` 分支的下一阶段性能优化计划。基线来自
`quanquan/feature/glua-aggressive-perf` 合入 `master` 前的收尾完整 benchmark：当前默认完整 benchmark
已经没有 `3x` 边缘项，下一阶段只关注仍高于官方 Lua 5.3.6 的路径。

允许采用更激进的方案，但必须满足以下边界：

- 不引入 CGO、不接 Lua C API、不新增外部依赖。
- 不实现 JIT；JIT 仍保留在 `docs/JIT_TODO.md`。
- debug hook、coroutine/yield、traceback、error、`debug.getinfo`、upvalue、metatable、弱表和 finalizer
  语义必须能回退普通路径。
- 每轮只推进一个可验证小切口；涉及大结构重构时先提交设计与 baseline，不直接改生产代码。
- 任一 fast path 不能证明语义等价时必须回退普通 VM。

## 当前基线

当前基线为重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6，
按 `scripts/benchmark-official.sh` 默认参数三轮复核的结果。

| 排名 | 用例 | 三轮倍率 | 平均 |
| ---: | --- | ---: | ---: |
| 1 | `arith_chain_temp` | `0.77x / 0.76x / 0.78x` | `0.77x` |
| 2 | `closure_upvalue` | `0.86x / 0.86x / 0.86x` | `0.86x` |
| 3 | `function_call` | `0.98x / 1.01x / 1.02x` | `1.00x` |
| 4 | `recursion` | `1.06x / 1.03x / 1.03x` | `1.04x` |
| 5 | `arith_add_loop` | `1.20x / 1.20x / 1.21x` | `1.20x` |
| 6 | `table_rw` | `1.45x / 1.44x / 1.46x` | `1.45x` |
| 7 | `stdlib_math_string` | `1.55x / 1.53x / 1.54x` | `1.54x` |
| 8 | `string_concat` | `1.82x / 1.84x / 1.82x` | `1.83x` |
| 9 | `arith_mix_loop` | `1.97x / 1.92x / 1.91x` | `1.93x` |
| 10 | `compile_3000_functions` | `2.01x / 1.91x / 1.90x` | `1.94x` |

低于 `1.00x` 表示本项目快于官方 Lua 5.3.6。下一阶段优先级按平均倍率、语义风险和泛化价值排序，
不再围绕已经快于官方或接近官方的 benchmark 定向路径继续扩张。

## 首轮 profile 基线

2026-07-04 在 `quanquan/feature/glua-next-perf` 上先补充了两个运行期高倍率项的 profile，
用于决定第一轮生产切口。

### `arith_mix_loop`

命令：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkPreparedArithMixLoopOfficial$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/arith_mix_prepared_cpu.pprof
go tool pprof -top /tmp/go-lua-vm-next-profiles/arith_mix_prepared_cpu.pprof
```

结果：

- `BenchmarkPreparedArithMixLoopOfficial`：约 `17.19 ms/op`、`90 B/op`、`0 allocs/op`。
- CPU top 中 `runtime.(*VM).TryExecuteMixArithmeticForLoop` 占约 `61.20%` flat / `74.79%` cum。
- `runtime.integerFloorDiv` 占约 `13.03%` flat。
- 普通 `VM.Step` 仅约 `1.68%` flat / `6.30%` cum。

结论：`arith_mix_loop` 已经主要落在现有单轮 mix fast path 内，继续做普通 dispatch 或局部 Step
微调收益有限。下一轮更合适的生产切口是 batch 版 mix arithmetic superinstruction：把固定寄存器、
常量、FORLOOP 回跳和整数操作 guard 前置，并在安全窗口内连续执行多轮。

2026-07-04 已实现最小 batch 版 mix arithmetic superinstruction：只覆盖官方 `arith_mix_loop`
中 sum、IDIV 临时寄存器、MOD 临时寄存器和 numeric-for 控制槽互不别名的窄形态；guard 不满足时
回退原单轮 superinstruction 或普通 VM。目标 benchmark 从 prepared 约 `17.19 ms/op` 降到
约 `8.94-8.96 ms/op`，DoString 约 `9.03 ms/op`，仍为 `0 allocs/op` 的纯运行期收益。

### `string_concat`

命令：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkDoStringStringConcat$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/string_concat_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/string_concat_mem.pprof
go tool pprof -top /tmp/go-lua-vm-next-profiles/string_concat_cpu.pprof
go tool pprof -top -alloc_objects /tmp/go-lua-vm-next-profiles/string_concat_mem.pprof
go tool pprof -top -alloc_space /tmp/go-lua-vm-next-profiles/string_concat_mem.pprof
```

结果：

- `BenchmarkDoStringStringConcat`：约 `435.49 us/op`、`2.25 MB/op`、`2196 allocs/op`。
- CPU 中 `runtime.concatstrings` 与 `runtime.(*VM).executeConcat` 合计贡献明显，同时 GC/runtime
  调度占比很高。
- `alloc_objects` 中 `runtime.(*VM).executeConcat` 占约 `90.90%` flat。
- `alloc_space` 中 `runtime.(*VM).executeConcat` 占约 `94.64%` flat，约 `48.32 GB` 采样分配。

结论：`string_concat` 的主因是循环内短命字符串分配和 GC 压力。窄形态 builder 仍可能有收益，
但必须处理字符串不可变、`__concat` 元方法、debug hook 和中间值可见性，语义风险高于
`arith_mix_loop` batch。除非先补充字节码和元方法可见性设计，本分支优先不直接实现该 fast path。

### `compile_3000_functions`

命令：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_mem.pprof
go tool pprof -top /tmp/go-lua-vm-next-profiles/compile_3000_cpu.pprof
go tool pprof -top -alloc_objects /tmp/go-lua-vm-next-profiles/compile_3000_mem.pprof
go tool pprof -top -alloc_space /tmp/go-lua-vm-next-profiles/compile_3000_mem.pprof
```

结果：

- `BenchmarkCompileSource3000Functions`：约 `5.83 ms/op`、`5.22 MB/op`、`3110 allocs/op`。
- CPU 中 `compiler/lexer.(*Lexer).NextToken` 约 `48.88%` cum；`parseFunctionStatement` 约
  `58.79%` cum；codegen 总体占比明显低于 parser/lexer。
- `alloc_objects` 中 `parseFunctionStatement` 约 `93.52%` flat，说明对象数主因仍是大量函数语句
  及其函数体 AST。
- `alloc_space` 中 `prepareDirectFunctionBlockCapacity` 约 `41.02%` flat，`parseFunctionStatement`
  约 `25.08%` flat。前者是上一阶段用 codegen arena 换低 allocs/op 的空间成本，不能简单删除。

结论：下一步不应直接微调 lexer dispatch 或删除 codegen arena。更合理的激进切口是先设计
typed statement / compact function statement prototype：在不破坏 parser、semantic、codegen 和错误语义的前提下，
减少 3000 个顶层 `FunctionStatement` 进入 `Block.Statements` interface 切片时的对象与接口成本；
同时需要评估 codegen arena 是否可以延迟或分段，避免用 alloc_space 换 allocs/op。

## 优化路线

### 1. `compile_3000_functions`：typed statement / AST 生命周期结构

当前最高项仍是编译期。上一阶段已经把 `compile_3000_functions` 从约 `8.34 ms/op`、`7.58 MB/op`、
`81151 allocs/op` 降到约 `6.1 ms/op`、`5.22 MB/op`、`3110-3114 allocs/op`。剩余对象主要来自
`FunctionStatement` 本体和真实表达式 AST；继续局部内嵌或 arena 已经不合适。

激进方案：

- 设计 `Block.Statements` 的 typed storage 或 compact statement representation，减少 interface 语句节点对象。
- 保持 AST 对外语义可被 semantic/codegen 稳定读取，避免最终 Proto 保留 parser/codegen 临时状态。
- 先做设计文档和 prototype profile，不直接替换全 parser。

验收：

- `compile_3000_functions` 平均倍率低于 `1.7x`，或 Go micro 至少稳定下降 `10%` wall-clock。
- B/op 不高于当前 `5.22 MB/op` 的噪声范围；不能再用大 backing array 换 allocs/op。
- 官方反汇编、debug local 生命周期、goto/label 错误、parser 错误文本保持一致。

设计切口：

- 目标对象：先只覆盖 parser 创建的简单 `FunctionStatement` / `LocalFunctionStatement` 节点，不替换
  `Block.Statements []Statement`，也不改 semantic/codegen 的公开遍历入口。
- 输入边界：`function name (...) ... end` 与 `local function name (...) ... end` 可进入 compact 路径；
  `function t.x(...)`、`function t:m(...)`、匿名 `function(...)`、控制流嵌套和扩展语法先保持原路径。
- 输出形态：parser 通过私有 chunked arena 分配 statement 值，返回值仍是 `*FunctionStatement` 或
  `*LocalFunctionStatement` 并写入现有 `Statement` 接口切片。这样可以减少每个函数语句一次独立对象分配，
  同时保持 AST 对外类型断言、`Pos()`、`Body`、`inlineBody` 和现有测试行为不变。
- 生命周期约束：arena chunk 必须随 AST 指针一同被 GC 保留；禁止复用 parser arena 到下一次解析，
  除非能证明上一棵 AST 已经不可达。chunk 扩容不能搬迁已返回 statement 地址。
- semantic 影响：`analyzeStatement` 仍按现有 type switch 处理，不新增作用域规则；local function 的
  先声明后分析语义不变。
- codegen 影响：`compileStatement`、`compileFunctionStatement`、`compileLocalFunction` 和
  `directFunctionBlockStatsFor` 不需要改变输入类型；`prepareDirectFunctionBlockCapacity` 暂不删除，
  后续只在 prototype 证明 parser 对象数下降后再评估分段或延迟 arena。
- debug / 错误语义：`LineDefined`、`LastLineDefined`、函数体 `Position`、parser 错误文本、goto/label
  和 local 生命周期必须保持现有 golden 行为。
- 回滚标准：若 prototype 只降低 allocs/op 但 B/op 或 wall-clock 稳定变差，或任何 parser/semantic/codegen
  测试行为变化，回退到普通堆分配路径并只保留 profile 记录。
- 明确不做：本阶段不引入全局 AST 对象池，不把 `Block.Statements` 改成 union，不删除 codegen arena，
  不跨解析任务共享 mutable state。

2026-07-04 已实现第一阶段 parser 私有 chunked statement arena prototype：简单 `function name`
和 `local function name` 语句仍返回原有 AST 指针类型，但节点来自不可搬迁的页式 slice。目标 benchmark
从约 `5.83 ms/op`、`5.22 MB/op`、`3110 allocs/op` 变为五轮约
`5.46-5.63 ms/op`、`5.25 MB/op`、`125 allocs/op`。对象数大幅下降，B/op 只有小幅上浮；
后续必须用完整 benchmark 和官方兼容脚本继续复核，暂不继续扩大到 `Block.Statements` union。

### 2. `arith_mix_loop`：批量 mix arithmetic superinstruction

`arith_mix_loop` 当前约 `1.9x`，运行期仍高于官方。上一阶段已有完整
`MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` 窄形态 fast path，但尚未像 `arith_add_loop` /
`arith_chain_temp` 一样做批量 guard hoisting。

激进方案：

- 新增 batch 版 mix arithmetic superinstruction，复用已识别的数据流。
- 把静态寄存器、常量、目标寄存器和 FORLOOP 回跳 guard 提前到 batch 准备阶段。
- 每个虚拟入口仍必须保持 context 取消边界，不放宽 debug/coroutine 回退条件。

验收：

- `BenchmarkPreparedArithMixLoopOfficial` 稳定低于当前约 `17-18 ms/op`。
- 默认完整 `arith_mix_loop` 稳定低于 `1.5x`。
- 非目标 arithmetic/table/function/recursion prepared 矩阵不出现稳定退化。

### 3. `string_concat`：窄形态字符串构建

`string_concat` 当前约 `1.8x`。官方 fixture 是循环内 `s = s .. 'x'`，可考虑把连续追加同一短字符串的
窄形态转为受 guard 保护的 builder 路径。

激进方案：

- 只覆盖无 metatable、无 hook、无 coroutine/continuation、固定右侧字符串常量的循环形态。
- 保留字符串不可变对外语义；中间字符串不能被 debug/hook 或外部函数观察时才允许合并构建。
- 任一 guard 失败回退普通 `CONCAT`。

风险：

- 该方向语义风险高于 arithmetic batch，因为 Lua 字符串不可变和 `__concat` 元方法必须严格保留。
- 首轮必须先 profile 和字节码复核，不直接实现生产 fast path。

验收：

- 默认完整 `string_concat` 稳定低于 `1.4x`。
- metatable `__concat`、错误路径、debug hook 场景有定向测试覆盖。

### 4. `stdlib_math_string`：表达式级 math/string folding 扩展

该项当前约 `1.5x`。上一阶段已经覆盖 `string.format("%d", i)` 固定结果和
`#string.format("%d", i)` 长度消费；剩余可能来自 `math.sqrt`、`math.floor` 与浮点边界。

激进方案：

- 只在 profile 证明收益时，评估 `math.floor(math.sqrt(i))` 的表达式级窄形态。
- 必须对齐 Lua 5.3 的 number、NaN、-0、整数/浮点转换和错误文本。

验收：

- 默认完整 `stdlib_math_string` 稳定低于 `1.25x`。
- math/string 标准库官方测试继续通过。

## TODO

- [ ] 跑下一阶段基线：默认完整 benchmark 三轮、剩余高于 `1.0x` 项的 Go micro 矩阵。
- [x] profile `compile_3000_functions`，确认 `FunctionStatement` / typed statement storage 仍是主切口，同时记录 codegen arena 空间成本。
- [x] 为 typed statement / compact AST 方案补设计小节，列出 parser、semantic、codegen、debug 和错误语义影响面。
- [x] 实现最小 typed statement prototype；若收益不足或 B/op 升高，记录证伪并回退。
- [x] profile `arith_mix_loop` prepared，确认当前主要成本在现有 mix fast path 内。
- [x] 补跑 `arith_mix_loop` DoString benchmark，确认端到端同步受益且没有新的编译期噪声。
- [x] 设计并实现 batch mix arithmetic superinstruction，证明 context、PC、debug hook、coroutine 和错误路径等价。
- [x] profile `string_concat` CPU/alloc，确认主因是 `executeConcat` 短命字符串分配和 GC 压力。
- [ ] 复核 `string_concat` 官方 fixture 字节码与元方法可见性，再决定是否进入窄形态 builder。
- [ ] profile `stdlib_math_string` 剩余热点，确认是否仍有表达式级消费消除空间。
- [ ] 每个生产优化 commit 后更新本文或 `docs/BENCHMARK.md`。

## 正确性门禁

每个生产优化提交前必须通过：

- `gopls check <changed-go-files>`
- `gofmt`
- `CGO_ENABLED=0 go test ./...`
- `./scripts/check-go-gates.sh`
- `git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'` 为空

涉及 CLI、bytecode、VM、stdlib 或官方兼容行为时，还必须通过：

- 重建 `bin/glua` / `bin/gluac`
- 官方 `lua` / `luac` 均确认为 Lua 5.3.6
- `./scripts/compare-cli-golden.sh`
- `./scripts/compare-official-executables.sh`
- `./scripts/run-official-tests.sh`

## 回滚标准

- 任一官方兼容脚本失败，立即停止并回滚或修复。
- benchmark 只减少 allocs/op 但明显增加 B/op，且不能证明实际 wall-clock 收益时回滚。
- fast path 改变 debug hook、yield、traceback、error path、metatable 或标准库边界时回滚。
- 无法用定向测试覆盖语义风险时，不保留生产改动。
