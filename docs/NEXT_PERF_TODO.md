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

当前基线为 `f5f9028` 后重建 `bin/glua` / `bin/gluac`，显式使用官方 Lua/Luac 5.3.6，
按 `scripts/benchmark-official.sh` 默认参数三轮复核的结果。

| 排名 | 用例 | 三轮倍率 | 平均 |
| ---: | --- | ---: | ---: |
| 1 | `stdlib_math_string` | `0.58x / 0.58x / 0.58x` | `0.58x` |
| 2 | `arith_add_loop` | `0.66x / 0.66x / 0.67x` | `0.66x` |
| 3 | `arith_chain_temp` | `0.76x / 0.77x / 0.78x` | `0.77x` |
| 4 | `table_rw` | `0.89x / 0.88x / 0.87x` | `0.88x` |
| 5 | `closure_upvalue` | `0.89x / 0.89x / 0.88x` | `0.89x` |
| 6 | `arith_mix_loop` | `1.03x / 1.02x / 1.03x` | `1.03x` |
| 7 | `string_concat` | `1.03x / 1.03x / 1.05x` | `1.04x` |
| 8 | `function_call` | `1.04x / 1.05x / 1.06x` | `1.05x` |
| 9 | `recursion` | `1.09x / 1.07x / 1.08x` | `1.08x` |
| 10 | `compile_3000_functions` | `1.33x / 1.33x / 1.32x` | `1.33x` |

低于 `1.00x` 表示本项目快于官方 Lua 5.3.6。2026-07-04 在 `f5f9028` 后重建
`bin/glua` / `bin/gluac` 并显式使用官方 Lua/Luac 5.3.6 复跑三轮后，`stdlib_math_string`、
`arith_add_loop`、`arith_chain_temp`、`table_rw` 和 `closure_upvalue` 已稳定快于官方；`arith_mix_loop`、
`string_concat`、`function_call` 和 `recursion` 均接近 `1.0x` 噪声带。剩余唯一清晰主项为
`compile_3000_functions`，但最近 profile 已判定没有新的低风险编译期小切口，继续推进必须先进入
函数体紧凑 AST / codegen arena 生命周期设计。

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

2026-07-04 复核官方规模 fixture：

```lua
local s = ''
for i = 1, 8000 do
  s = s .. 'x'
end
return #s
```

官方 Lua 5.3.6 `luac -l -l` 热循环为：

```text
FORPREP; MOVE; LOADK; CONCAT; FORLOOP
```

本项目 `gluac -l -l` 热循环同样为 `FORPREP; MOVE; LOADK; CONCAT; FORLOOP`；项目在循环退出后额外
保留一个零距离 `JMP`，不在热路径内。当前热体每轮会把 `s` 复制到临时寄存器、加载常量 `"x"`，
再执行 `CONCAT A=0 B=5 C=6` 写回 `s`，因此可见语义上的中间值是每轮循环结束后的新字符串。

元方法可见性复核：

- 官方 Lua 5.3.6 和 glua 均不会在 `"a" .. "b"` 两侧都可直接转字符串时调用 string 类型
  `__concat`。
- 官方 Lua 5.3.6 和 glua 均会在 `{} .. "b"` 这类存在不可直接转字符串操作数时查找并调用
  `__concat`。
- 现有测试已经覆盖 `__concat` 的 `debug.getinfo(1)` tagmethod 名称，以及连续 `CONCAT`
  元方法 yield 后继续折叠的 coroutine 语义。

因此，后续若实现 builder fast path，只能作为更窄的运行期 guard：

- 只匹配 `s = s .. Kstring` 这一类 `MOVE; LOADK; CONCAT; FORLOOP`，且目标寄存器、源寄存器、
  临时寄存器和 numeric-for 控制寄存器互不破坏。
- 运行期必须确认参与拼接的当前值与右侧常量都是 raw string；任一 operand 不是 string 时立即回退
  普通 `CONCAT`，保留 number 转换、错误文本和 `__concat` 元方法机会。
- debug hook、精确帧同步、coroutine continuation、yield 恢复或需要逐条 PC 可见性的场景必须回退普通 VM；
  builder 批量执行会跳过每轮 `s` 的寄存器写回和行事件，不能让 debug hook 观察到压缩后的中间状态。
- 批量窗口仍需要保留 context 取消检查边界，不能把 8000 次循环压成一次不可中断的大块。
- 实现前必须先补定向测试：string metatable `__concat` 不拦截 string/string、非 string operand 仍触发
  `__concat`、yielding `__concat` 继续折叠、debug hook 下不命中 builder、错误路径和 `#s` 结果一致。

2026-07-04 已补齐 builder 进入生产实现前的核心 guard 测试：

- `TestDoStringConcatStringPairIgnoresStringMetamethod` 固定 string/string 基础拼接不触发 string 类型
  `__concat`。
- `TestDoStringConcatNonStringUsesStringMetamethod` 固定存在非 string 操作数时仍回退普通 `__concat`
  查找路径。
- `TestDoStringConcatLineHookSeesSelfAppendIntermediates` 固定 line hook 打开时必须能观察每轮
  `s = s .. "x"` 前的中间长度 `0,1,2`。
- 既有 `TestDoStringCoroutineYieldInConcatMetamethod` 继续覆盖 yielding `__concat` 的连续折叠恢复。

这些测试只锁定回退与可见性边界，不引入生产 fast path。下一步若实现 builder 原型，应先在
runtime 侧构建只覆盖无 hook、无 continuation、raw string operand、固定右侧 string 常量的预匹配表；
任一 guard 失败必须保持上述测试覆盖的普通路径。

2026-07-04 已实现最小窄形态 builder 原型：在无 hook、无 precise frame sync 的普通执行路径中，
预匹配 `MOVE; LOADK; CONCAT; FORLOOP`，只覆盖 `s = s .. Kstring` 且目标、临时槽、numeric-for
控制槽互不别名的形态。执行期仍要求当前累加值为 raw string、右侧为构建期固定 string 常量、
numeric-for 控制槽为 integer；任一 guard 失败即回退普通 VM，保留 number 转换、`__concat`、错误和
yield/continuation 语义。批量窗口按 context 倒计时限制，每轮等价跳过 `MOVE; LOADK; CONCAT; FORLOOP`
四个入口，提交后补齐最后一轮的 `MOVE`/`LOADK` 临时槽和 FORLOOP 最后有效 index。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkDoStringStringConcat$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkDoStringStringConcat`：约 `84.29 / 87.42 / 90.03 / 91.61 / 92.77 us/op`。
- B/op 约 `310.8 KB/op`，`370 allocs/op`。

对比前一轮 profile 记录的约 `435.49 us/op`、`2.25 MB/op`、`2196 allocs/op`，短命字符串分配和
端到端耗时均显著下降。该 benchmark 规模为 Go micro 中的 2000 次追加；官方完整 benchmark 的
8000 次追加仍需在重建 `bin/glua` / `bin/gluac` 后用 Lua 5.3.6 版本门禁脚本复核。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 复核结果：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007777s` | `0.009493s` | `1.22x` |
| `arith_mix_loop` | `0.011546s` | `0.013304s` | `1.15x` |
| `arith_chain_temp` | `0.012929s` | `0.009896s` | `0.77x` |
| `table_rw` | `0.007038s` | `0.010420s` | `1.48x` |
| `function_call` | `0.006842s` | `0.006951s` | `1.02x` |
| `string_concat` | `0.004748s` | `0.004906s` | `1.03x` |
| `closure_upvalue` | `0.008107s` | `0.007019s` | `0.87x` |
| `stdlib_math_string` | `0.019468s` | `0.030226s` | `1.55x` |
| `recursion` | `0.003745s` | `0.003910s` | `1.04x` |
| `compile_3000_functions` | `0.005270s` | `0.009731s` | `1.85x` |

结论：`string_concat` 已从当前基线约 `1.83x` 降到 `1.03x`，说明 builder 原型在官方 8000 次追加
口径下也有效；该项暂不应继续扩张，后续优先级回到 `stdlib_math_string`、`table_rw` 和
`compile_3000_functions`。

### `stdlib_math_string`

2026-07-04 在 `string_concat` builder 后复核剩余热点：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkDoStringStdlibMathString$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/stdlib_math_string_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/stdlib_math_string_mem.pprof
go tool pprof -top /tmp/go-lua-vm-next-profiles/stdlib_math_string_cpu.pprof
go tool pprof -top -alloc_objects /tmp/go-lua-vm-next-profiles/stdlib_math_string_mem.pprof
go tool pprof -top -alloc_space /tmp/go-lua-vm-next-profiles/stdlib_math_string_mem.pprof
```

结果：

- `BenchmarkDoStringStdlibMathString`：约 `25.54 ms/op`、`151.6 KB/op`、`4555 allocs/op`。
- CPU top 中 `runtime.(*VM).TryExecuteFormatLenAddForLoop` 约 `12.81%` flat / `32.03%` cum；
  `runtime.(*VM).executeGetTabUp`、`runtime.(*Table).RawGetString`、`runtime.mapaccess2` 和普通
  `VM.Step` 仍占明显比例。
- alloc_objects 中 `internal/strconv.FormatInt` 仍是采样主项，但该分配来自未完全命中的普通
  `string.format("%d", i)` 路径；alloc_space 中 parser/OpenLibs 噪声占比较高。

字节码复核：

```text
GETTABUP math; GETTABLE floor; GETTABUP math; GETTABLE sqrt; MOVE i; CALL sqrt;
CALL floor; ADD; GETTABUP string; GETTABLE format; LOADK "%d"; MOVE i; CALL format;
LEN; ADD; FORLOOP
```

官方 Lua 5.3.6 与本项目热体一致。此前只覆盖尾部 `#string.format("%d", i)`，前半段
`math.floor(math.sqrt(i))` 每轮仍要执行全局 table 查找、两次 Go fast unary CALL 和普通 dispatch。

2026-07-04 已实现完整热体 batch superinstruction：新增标准库一元函数 fast-path 标记，只把内建
`math.floor` 和 `math.sqrt` 暴露给 VM 的跨 opcode guard。运行期只在无 hook、无 precise frame sync、
环境 table 无元表、`math`/`string` 均为无元表 table、`math.floor`/`math.sqrt`/`string.format`
仍是带 fast-path 标记的标准库函数、sum 和 numeric-for 控制槽均为非负 integer 且官方寄存器复用布局
完全匹配时命中。任一条件不满足都会回退普通 `GETTABUP; GETTABLE; CALL; LEN; ADD; FORLOOP`，保留
用户替换函数、元表 `__index`、参数错误、NaN/number 路径、debug hook、yield/continuation 和
context 取消语义。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkDoStringStdlibMathString$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkDoStringStdlibMathString`：约 `7.00 / 6.97 / 6.96 / 6.98 / 6.99 ms/op`。
- B/op 约 `154.6 KB/op`，`4556 allocs/op`。

对比本轮 profile 记录的约 `25.54 ms/op`，wall-clock 降幅约 `72%`；分配数量基本不变，说明收益主要来自
跳过 table lookup、Go CALL 边界和 dispatch，而不是继续压缩格式化分配。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 复核结果：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007632s` | `0.009347s` | `1.22x` |
| `arith_mix_loop` | `0.011592s` | `0.013402s` | `1.16x` |
| `arith_chain_temp` | `0.013125s` | `0.009932s` | `0.76x` |
| `table_rw` | `0.007193s` | `0.010633s` | `1.48x` |
| `function_call` | `0.006847s` | `0.007052s` | `1.03x` |
| `string_concat` | `0.004872s` | `0.004988s` | `1.02x` |
| `closure_upvalue` | `0.008196s` | `0.007017s` | `0.86x` |
| `stdlib_math_string` | `0.019454s` | `0.011246s` | `0.58x` |
| `recursion` | `0.003783s` | `0.003860s` | `1.02x` |
| `compile_3000_functions` | `0.005355s` | `0.009714s` | `1.81x` |

结论：`stdlib_math_string` 已从当前基线约 `1.54x` 降到 `0.58x`，该项暂不继续扩张。下一轮优先级
转向 `table_rw` 的数组分配/预估入口，或继续压缩 `compile_3000_functions` 的编译期差距。

### `table_rw`

2026-07-04 在 `48c3491` 后复核 `table_rw` prepared profile：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkPreparedTableReadWriteOfficial$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/table_rw_prepared_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/table_rw_prepared_mem.pprof
go tool pprof -top /tmp/go-lua-vm-next-profiles/table_rw_prepared_cpu.pprof
go tool pprof -top -alloc_space /tmp/go-lua-vm-next-profiles/table_rw_prepared_mem.pprof
go tool pprof -top -alloc_objects /tmp/go-lua-vm-next-profiles/table_rw_prepared_mem.pprof
```

结果：

- `BenchmarkPreparedTableReadWriteOfficial`：约 `4.98 ms/op`、`11.21 MB/op`、`3 allocs/op`。
- CPU profile 主要受大数组分配后的 GC/runtime 调度影响；可归因的 VM 热点中
  `TryExecuteTableReadAddForLoopBatch` 仍是主要执行成本，`RawGetInteger` 和普通 `VM.Step` 占比已经较低。
- `alloc_space` 中 `runtime.newTableWithArrayCapacity` 占 `100%`，说明此前数组预分配、table write batch
  和 table read-add batch 已经消除了连续扩容和大部分 dispatch 成本。
- `alloc_objects` 中剩余对象主要是一轮执行创建的 table、数组 backing store 以及少量 VM/proto 绑定对象。

结论：当前 `table_rw` 剩余差距不再是简单扩容阈值、读写 opcode dispatch 或 table lookup 分支问题，而是
官方规模 `local t = {}; for i = 1, 200000 do t[i] = i end` 必然触发的一次大 `[]Value` backing array
分配和 GC 扫描成本。继续调 `ensureArraySize` 或重复增加局部读写 fast path 预计收益有限。

下一步若继续激进优化，必须先补设计而不是直接改生产代码：

- 候选一：`Table` 增加可回退的 dense integer array 表示，用无指针整数 backing store 承载连续正整数键和值；
  任一 nil、非整数、hash、元表、弱表、`next`/`pairs` 可见性或 debug/错误路径 guard 失败时 materialize 回普通
  `[]Value` 数组区。
- 候选二：对 `NEWTABLE; table write for-loop; table read-add for-loop; RETURN` 做逃逸证明，只在 table 完全不逃逸、
  中间无 hook/调用/元表观察且最终只消费求和结果时消除 table 存储。该方向语义风险更高，更接近 benchmark
  专项，不应在没有设计和定向测试前实现。
- 回滚标准：任一方案如果改变 `#t`、`rawget/rawset`、`next/pairs` 顺序、弱表/finalizer、metatable、
  debug hook 或错误路径可见性，必须回退普通 table。

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

### 1. `table_rw`：dense integer table 与逃逸消除边界

`table_rw` 当前仍约 `1.48x`。上一阶段已经完成数组预分配、table write batch 和 table read-add batch，
prepared profile 显示剩余分配主要是一次 `[]Value` 大数组 backing store 以及随后的 GC 扫描。
继续调扩容阈值或增加局部 `RawGetInteger` 分支不能触及主因。

首选激进方案是可回退的 dense integer table 表示，而不是整段 benchmark 逃逸消除：

- 只由 VM 中已证明的 table write batch 创建 compact table；普通 `NewTable`、外部 Go API 和未证明的
  `OP_NEWTABLE` 保持现有 `[]Value` 数组区。
- compact table 只承载连续正整数 key，首轮只覆盖官方 fixture 的 `t[i] = i` / `t[i] = Kinteger`
  这类非 nil integer value；key 隐含为 `1..n`，value 放入无指针 `[]int64` backing store。
- `RawGetInteger`、`RawSetPositiveIntegerNonNil`、`Len`、`RawIPairsNext` 可以在 compact 表示上直接读写；
  返回值仍即时构造 `IntegerValue`，不得向外泄露内部表示。
- 任一非 integer value、nil 删除、稀疏写入、hash key 写入、非正整数 key、超出 compact 上限、
  `RawSet` 的 NaN/nil key 错误路径、或需要稳定 `Value` 槽位地址的路径都必须先 materialize 到普通
  `[]Value` 数组区，再沿现有逻辑执行。
- `ArraySize`、`RawNext`、`RawPairsNext`、raw 迭代快照、迭代中删除当前 key 后继续、`next` 非法 key
  错误边界，首轮应直接 materialize 后复用现有实现，避免维护两套迭代顺序。
- `SetMetatable` / `SetMetatableChecked` 本身不必 materialize；但弱表 sweep、finalizer 前 weak 清理、
  `__mode` 后续变化、以及任何遍历或对外 raw 快照都必须在观察前 materialize，保证弱表和 finalizer
  不需要理解 compact 内部表示。
- 普通 `Get` / `Set` 仍先经 raw 路径；raw miss 后的 `__index` / `__newindex` 链必须与现有 table 一致。
  如果 compact 状态下出现 miss 且后续需要元方法，允许不 materialize；但一旦元方法可能观察 table 本体
  或写回 table，必须回到普通路径。
- debug hook、precise frame sync、yield/continuation 和错误 traceback 不应看到压缩过的 table 状态；
  创建 compact table 的 VM batch 必须沿用现有无 hook、无 continuation guard。

首轮验收：

- `BenchmarkPreparedTableReadWriteOfficial` 的 B/op 至少下降 `70%`，或 wall-clock 稳定下降 `15%` 以上。
- 默认完整 `table_rw` 稳定低于 `1.25x`；若官方基线波动较大，至少要求 Go micro 五轮稳定改善。
- 必须新增定向测试覆盖 compact table 的 `RawGetInteger`、连续写入、`Len`、`ipairs`、nil 删除 materialize、
  非 integer 写入 materialize、hash 写入 materialize、`RawNext`/`pairs` 顺序、`SetMetatableChecked`、
  weak value sweep 和 `__index`/`__newindex` miss 语义。
- 官方兼容脚本必须全绿；任何 `#t`、`rawget/rawset`、`next/pairs` 顺序、弱表/finalizer、metatable、
  debug hook 或错误路径差异都回退。

2026-07-04 已实现 dense integer array prototype：`newTableWithArrayCapacity` 只在 VM 已证明的预分配入口
创建 compact table，普通 `NewTable` 和外部 Go API 仍保持原数组区；compact 表只接受连续正整数 key 和
integer value，复杂写入、raw 迭代、弱表清理和完整数组扩容都会 materialize 回普通 `[]Value` 数组区。
`d674551` 中补齐的 guard 测试保持全绿。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^Benchmark(DoString|Prepared)TableReadWriteOfficial$' \
  -benchmem -benchtime=2s -count=3
```

- `BenchmarkDoStringTableReadWriteOfficial`：约 `4.88-4.89 ms/op`、`1.73 MB/op`、`222 allocs/op`。
- `BenchmarkPreparedTableReadWriteOfficial`：约 `4.77-4.84 ms/op`、`1.61 MB/op`、`3 allocs/op`。

对比 `5956414` profile 记录的 prepared 约 `4.98 ms/op`、`11.21 MB/op`、`3 allocs/op`，B/op 降幅约
`86%`，满足首轮内存验收；wall-clock 只小幅下降，说明当前剩余时间主要不再由 Go 指针数组 GC 扫描主导。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 复核结果：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007259s` | `0.008775s` | `1.21x` |
| `arith_mix_loop` | `0.010733s` | `0.012462s` | `1.16x` |
| `arith_chain_temp` | `0.012182s` | `0.009348s` | `0.77x` |
| `table_rw` | `0.006678s` | `0.008521s` | `1.28x` |
| `function_call` | `0.006258s` | `0.006602s` | `1.05x` |
| `string_concat` | `0.004339s` | `0.004583s` | `1.06x` |
| `closure_upvalue` | `0.007639s` | `0.006533s` | `0.86x` |
| `stdlib_math_string` | `0.018605s` | `0.010701s` | `0.58x` |
| `recursion` | `0.003240s` | `0.003513s` | `1.08x` |
| `compile_3000_functions` | `0.004890s` | `0.009239s` | `1.89x` |

结论：`table_rw` 从前一轮完整 benchmark 的约 `1.48x` 降到 `1.28x`，端到端收益明确但仍略高于
首轮 `1.25x` 稳定目标；Go micro 已证明主内存分配大幅下降，后续若继续压缩 table 路径，应先 profile
剩余执行成本，避免直接进入更接近 benchmark 专项的整段 table 逃逸消除。

2026-07-04 在 `9b8eb2d` 后复核 `table_rw` prepared profile：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' \
  -bench '^BenchmarkPreparedTableReadWriteOfficial$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/table_rw_after_9b8eb2d_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/table_rw_after_9b8eb2d_mem.pprof
```

- `BenchmarkPreparedTableReadWriteOfficial`：约 `4.78 ms/op`、`1.61 MB/op`、`3 allocs/op`。
- 五轮复跑稳定在约 `4.79-4.81 ms/op`、`1.61 MB/op`、`3 allocs/op`。
- CPU profile 中可归因热点主要是 `TryExecuteTableWriteForLoopBatch`、`TryExecuteTableReadAddForLoopBatch`、
  `RawSetPositiveIntegerNonNil`、`tryRawSetDenseInteger` 和 `RawGetInteger`；`alloc_space` 仍约 `99.75%`
  落在 `newTableWithArrayCapacity` 的 dense backing array。

结论：dense integer array 原型已经把 B/op 从约 `11.21 MB/op` 压到 `1.61 MB/op`，剩余主要是每次执行
仍必须创建一份可观察 table 内容，以及写/读 batch 内部每轮都走 raw helper、mutation/cache 维护和
`IntegerValue` 构造。继续推进 table 路径不应直接做整段逃逸消除；更小的候选是先为 batch 内 dense-only
直写/直读补 guard 测试和设计，再决定是否实现：

- 写入 batch 只有在目标 table 为无元表 dense 表、key/value 都等于 for 循环当前 integer、连续正整数前缀、
  且执行期无 hook/continuation 时，才允许直接追加或覆盖 `denseIntegerValues`，并在 batch 末尾统一维护
  mutation/length cache；guard 失败必须回退现有 `RawSetPositiveIntegerNonNil`。
- 读取 batch 只有在 table 为无元表 dense 表、读取 key 位于连续前缀内、读出值仍是 integer、累加寄存器为
  integer 时，才允许直接从 `denseIntegerValues` 累加；guard 失败必须回退现有 `RawGetInteger` + ADD。
- 定向测试必须覆盖 partial batch、最终退出 batch、元表回退、稀疏/非 integer materialize 后回退、
  `RawNext`/`pairs` 观察前 materialize、length cache 和 mutationVersion 只在语义可见处变化。
- 如果该方向只能降低 CPU 小分支而不能降低 `1.61 MB/op` 主分配，则不应继续扩大；下一阶段再评估整段 table
  逃逸消除的 hook、metatable、rawget/rawset、next/pairs、`#t`、upvalue/closure、返回/传参、错误 traceback
  和 debug 可见性边界。

2026-07-04 已先补齐 batch 内 dense-only guard 测试，覆盖写入 batch 保持 dense 表示、`RawNext`
观察前 materialize、读取 batch 不 materialize、不改变 `mutationVersion`，以及读取路径带元表 table
必须无副作用回退普通 `GETTABLE`。随后实现 batch 内 dense-only 直写/直读：写路径只在无元表 dense table、
`step == 1`、内部 index 与外部可见 index 对齐、key 连续且仍在数组区上限内时，直接更新
`denseIntegerValues`，并在 batch 末尾统一维护长度缓存和 mutation 版本；读路径只在无元表 dense table、
`step == 1`、key 位于连续 dense 前缀内时，直接读取 int64 并累加，缺失或 guard 失败时回退现有
`RawGetInteger` + `ADD` 路径。该实现不改变普通 `NewTable`、外部 Go API、materialize、元表、弱表、
迭代、debug hook 或错误路径。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkPreparedTableReadWriteOfficial$' \
  -benchmem -benchtime=2s -count=5
CGO_ENABLED=0 go test ./lua -run '^$' -bench '^Benchmark(DoString|Prepared)TableReadWriteOfficial$' \
  -benchmem -benchtime=2s -count=3
```

- `BenchmarkPreparedTableReadWriteOfficial` 五轮约
  `2.11 / 2.09 / 2.09 / 2.09 / 2.10 ms/op`、约 `1.61 MB/op`、`3 allocs/op`。
- `BenchmarkDoStringTableReadWriteOfficial` 三轮约
  `2.20 / 2.20 / 2.19 ms/op`、约 `1.73 MB/op`、`222 allocs/op`。
- `BenchmarkPreparedTableReadWriteOfficial` 复跑三轮约
  `2.16 / 2.13 / 2.07 ms/op`、约 `1.61 MB/op`、`3 allocs/op`。

对比 `aeb2109` 记录的 prepared 约 `4.79-4.81 ms/op`，wall-clock 降幅约 `55%`，说明剩余 CPU
确实主要来自 batch 内每轮 raw helper、mutation/cache 维护和 `IntegerValue` 构造；B/op 不变，符合该
切口只压缩执行 CPU 而不做整段 table 逃逸消除的预期。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 抽样结果：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007720s` | `0.009673s` | `1.25x` |
| `arith_mix_loop` | `0.011703s` | `0.013463s` | `1.15x` |
| `arith_chain_temp` | `0.013018s` | `0.010131s` | `0.78x` |
| `table_rw` | `0.007197s` | `0.006396s` | `0.89x` |
| `function_call` | `0.006793s` | `0.007428s` | `1.09x` |
| `string_concat` | `0.004956s` | `0.005076s` | `1.02x` |
| `closure_upvalue` | `0.008312s` | `0.007183s` | `0.86x` |
| `stdlib_math_string` | `0.019470s` | `0.011597s` | `0.60x` |
| `recursion` | `0.003707s` | `0.003962s` | `1.07x` |
| `compile_3000_functions` | `0.005335s` | `0.007114s` | `1.33x` |

结论：`table_rw` 从 dense integer array 原型后的约 `1.28x` 进一步降到 `0.89x`，已经快于官方 Lua
5.3.6；后续不要继续扩大 table 路径，除非新的完整三轮基线显示稳定回退并有 profile 证据。剩余高于
`1.0x` 的项目应重新排序，优先看 `compile_3000_functions`、`arith_add_loop`、`arith_mix_loop` 或
`function_call` 是否仍有结构性小切口。

更激进的后备方案是整段 table 逃逸消除：

- 只匹配 `NEWTABLE; numeric-for t[i]=i; numeric-for sum=sum+t[i]; RETURN` 这一类 table 完全不逃逸的固定形态。
- 必须证明 table 没有被 upvalue 捕获、没有赋给全局/返回/传参、两个循环之间没有调用、没有 hook、
  没有元表观察、没有 `next/pairs/#/rawget/rawset` 或 debug API 读取局部 `t` 的机会。
- 命中后可以不创建 table，直接计算读循环结果；但这更接近 benchmark 专项，必须在 dense integer table
  方案收益不足且文档化语义证明后才允许进入 prototype。

### 2. `compile_3000_functions`：typed statement / AST 生命周期结构

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

2026-07-04 在 `fe051cc` 后复核剩余 profile：`BenchmarkCompileSource3000Functions` 约
`5.39 ms/op`、`5.25 MB/op`、`122 allocs/op`；CPU 中可归因部分仍集中在 lexer/parser，alloc_space 中
`prepareDirectFunctionBlockCapacity` 和 `newFunctionStatement` 仍是大头，alloc_objects 中
`recordConstantIndex` 说明顶层 3000 个普通 function 名称常量索引仍会触发 string map 增长。已实现最小
codegen 切口：复用 `directFunctionBlockStatsFor` 已知的普通 function 名称数量，为当前 Proto 的 string
常量索引 map 预留容量，不改变常量插入、去重、顺序或 Proto 输出。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkCompileSource3000Functions`：约 `5.33 / 5.33 / 5.33 / 5.43 / 5.38 ms/op`。
- B/op 约 `5.14 MB/op`，`104-105 allocs/op`。

结论：该切口收益较小但稳定，主要减少 string 常量索引 map 扩容对象和少量空间；后续不应继续围绕
常量索引做局部调参，剩余差距应回到 lexer/parser CPU 或更明确的 codegen arena 空间设计。重建
`bin/glua` / `bin/gluac` 后单轮完整 benchmark 中 `compile_3000_functions` 为官方 `0.004795s`、
本项目 `0.009207s`、倍率 `1.92x`，端到端只有小幅变化。

2026-07-04 继续实现顶层 block 语句容量 hint：只在顶层标准 block 中扫描源码行首
`function` / `local function` 声明数量，超过阈值时预留 `Block.Statements` 容量。该 hint 只改变
slice cap，不跳过 lexer/parser，不改变语句数量、顺序、AST 类型、错误文本或函数体语义；误判只会带来
有上限的容量预留。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkCompileSource3000Functions`：约 `5.22 / 5.25 / 5.23 / 5.22 / 5.25 ms/op`。
- B/op 约 `5.03 MB/op`，`91 allocs/op`。

结论：该切口继续小幅压低顶层大量函数声明的 `Block.Statements` 扩容成本；剩余差距仍主要来自真实
lexer/parser CPU、函数体 AST 与 codegen arena 空间，不应扩大为 `Block.Statements` union 或全 parser
替换。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 单轮复核中，
`compile_3000_functions` 为官方 `0.004755s`、本项目 `0.009248s`、倍率 `1.94x`。因此该切口主要是
Go micro 层面的分配与小幅 CPU 改善，端到端剩余差距仍需要新的 profile 证据指向 lexer/parser 或
codegen arena，不能继续围绕简单容量预留做局部调参。

2026-07-04 在 `94852d7` 后重新 profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_94852d7_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_94852d7_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `5.33 ms/op`、`5.03 MB/op`、`87 allocs/op`。
- CPU 采样中可归因部分仍集中在 lexer/parser：`Lexer.NextToken`、`parseBlockUntilInto`、
  `parseFunctionBodyInto`、`parseFunctionStatement` 和 `parseReturnStatementInto`。
- alloc_space 中 `prepareDirectFunctionBlockCapacity`、`newFunctionStatement`、`newLiteralExpression`、
  `newBinaryExpression`、`newNameExpression` 仍可见；alloc_objects 中表达式 arena 页分配仍占一定比例。

试验过把大量顶层函数数量作为 name/literal/binary expression arena 首屏容量 hint：首版因为尾部
`return f2999(1)` 在大页已满后再次分配大页，导致 B/op 升至约 `5.52 MB/op`，已修正为只对首屏页生效。
修正后五轮约 `5.25-5.35 ms/op`、`5.05 MB/op`、`60 allocs/op`；同机 detached `94852d7` 基线五轮约
`5.27-5.30 ms/op`、`5.03 MB/op`、`91 allocs/op`。该试验只稳定降低 allocs/op，没有证明 wall-clock
收益，且 B/op 略升，因此已回退生产改动。后续不要重复做 expression arena 大页容量 hint；若继续推进
编译期，必须找到能同时降低 CPU 或显著降低 B/op 的结构性切口，例如 lexer token 解码成本、函数体 AST
专用紧凑表示，或 codegen arena 空间策略。

2026-07-04 实现 lexer 操作符扫描的 ASCII 首字节最长匹配：将原先每个操作符 token 遍历完整操作符
列表并多次 `PeekOffset` 的路径，改为按首字节 `switch` 直接识别 `...`、`//`、`<<`、`<=`、`::`
等 Lua 5.3 操作符。所有消费仍通过 `Source.Next` 维护行列、Offset、CR/LF 语义；非 ASCII 和非法字符仍回到
非法 token 路径。该切口不改变 token 文本、token 顺序或错误语义，并新增测试覆盖全部操作符最长匹配。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- 首轮五轮：约 `4.64 / 4.64 / 4.61 / 4.64 / 4.63 ms/op`，约 `5.03 MB/op`，`90 allocs/op`。
- 清理旧列表匹配实现后三轮：约 `4.71 / 4.71 / 4.69 ms/op`，约 `5.03 MB/op`，`90 allocs/op`。

结论：相比 `94852d7` 后约 `5.22-5.25 ms/op`，该 lexer 切口稳定降低 `compile_3000_functions`
wall-clock，且 B/op 基本不变；这是当前编译期剩余差距中比容量预留更明确的结构性收益。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 单轮复核中，
`compile_3000_functions` 为官方 `0.004812s`、本项目 `0.008535s`、倍率 `1.77x`；相比上一轮单轮
约 `1.94x`，CLI 端到端也有明确收益。

2026-07-04 实现 lexer 标识符 ASCII byte 扫描：当前标识符语义本就限定 ASCII 字母、数字和下划线，
因此 `ScanIdentifier` 可直接从 `Source` 的原始 byte 输入中扫描连续标识符，避免对每个字符重复
`Peek` / `Next` 的 UTF-8 解码和位置更新。非 ASCII 起始字符仍不消费，标识符后的非 ASCII 字符仍保留给
普通非法 token 路径按完整 UTF-8 rune 消费；新增测试固定 `abc界` 只识别 `abc`，并确认后续非法 token
文本仍是完整 `界`。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- 五轮：约 `4.24 / 4.22 / 4.25 / 4.24 / 4.22 ms/op`，约 `5.03 MB/op`，`89 allocs/op`。

结论：相比操作符扫描切口后的约 `4.61-4.71 ms/op`，identifier byte 扫描继续稳定降低
`compile_3000_functions` wall-clock，且 B/op 基本不变；这是 lexer token 解码成本上的结构性收益，
后续不要再围绕标识符基础扫描做局部分支调参，除非 profile 指向 keyword 判定或 Source ASCII 读取。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 单轮复核中，
`compile_3000_functions` 为官方 `0.005393s`、本项目 `0.008631s`、倍率 `1.60x`；相比上一轮单轮
约 `1.77x`，CLI 端到端继续收敛。

2026-07-04 在 `51bf3e6` 后重新 profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_51bf3e6_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_51bf3e6_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `4.30 ms/op`、`5.03 MB/op`、`86 allocs/op`。
- CPU 采样仍显示 lexer/parser 是主要可归因部分，其中 `scanNumberToken` / `ScanNumber` 累计约
  `0.47s`，`ScanIdentifier` 已降到较低占比。
- alloc_space 仍主要来自 `prepareDirectFunctionBlockCapacity`、`newFunctionStatement`、
  `newLiteralExpression`、`newBinaryExpression`、`newNameExpression` 和 Proto/codegen 结构。

试验过为纯 int64 十进制整数字面量增加 byte 扫描快路径：复杂数字、指数、浮点、十六进制和溢出均回落
通用路径，定向测试覆盖最大 int64、非 ASCII 后缀、`1..x` 和非法指数。五轮结果约
`4.31 / 4.31 / 4.31 / 4.31 / 4.45 ms/op`、约 `5.03 MB/op`、`89-90 allocs/op`，没有证明相比
`51bf3e6` 后 `4.22-4.30 ms/op` 的稳定 wall-clock 收益，且 allocs/op 未改善；生产改动已回退。
后续不要重复做纯十进制 int64 byte fast path，除非 profile 指向更具体的 number 解析子路径并能证明
B/op 或 wall-clock 稳定下降。

同轮试验过把 `skipWhitespace` 改为 ASCII byte 直扫并手工维护 `Source` 的 offset、line 和 column：
CRLF 与 LFCR 仍按 Lua 5.3 归一为单个换行，普通空白只推进列号，定向测试覆盖混合换行行列。五轮结果约
`4.52 / 4.38 / 4.38 / 4.39 / 4.40 ms/op`、约 `5.03 MB/op`、`89-90 allocs/op`，相比现有
`51bf3e6` 后基线没有收益且略慢；生产改动已回退。后续不要重复做 `skipWhitespace` byte 直扫，
除非 profile 证明空白跳过重新成为独立主热点并能在同机基线中稳定降低 wall-clock。

同轮还试验过在 `parseFunctionStatement` 和 `parseReturnStatementInto` 中跳过防御式
`expectKeyword("function")` / `expectKeyword("return")`，改为调用方已确认 keyword 后直接
`advance`。该改动不改变正常 AST 路径，定向 parser 测试通过，但
`BenchmarkCompileSource3000Functions` 五轮约 `4.34 / 4.35 / 4.38 / 4.34 / 4.36 ms/op`、
约 `5.03 MB/op`、`89 allocs/op`，没有证明相比当前基线有稳定收益；生产改动已回退。后续不要重复做
已知 keyword 直接 advance 这类 parser 防御检查微调，除非 profile 显示 `expectKeyword` 自身成为
独立 flat 热点。

2026-07-04 在 `2f9dfda` 后再次复核 `compile_3000_functions`：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_2f9dfda_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_2f9dfda_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `4.416 ms/op`、`5.03 MB/op`、`87 allocs/op`。
- CPU 采样中 `Lexer.NextToken` 约 `19.78%` cum，`Parser.advance` 约 `22.27%` cum，
  `parseFunctionStatement` 约 `25.55%` cum；运行时调度和 GC 噪声占比较高。
- `alloc_objects` 中 `parseExpressionList`、`newNameExpression`、`newFunctionStatement`、
  `newBinaryExpression`、`newLiteralExpression` 仍可见；`parseExpressionList` 主要对应尾部
  `return f2999(1)` 的单参数列表，单独消除只能减少约 1 alloc/op，不满足明显收益验收。
- `alloc_space` 仍由 `prepareDirectFunctionBlockCapacity`、`newFunctionStatement`、表达式 arena
  和 Proto/codegen 结构主导；其中 codegen arena 是前序优化用空间换低对象数的结果，不能直接删除。

评估过一个新的但更大的结构切口：为 `return x + 123` 这类 `name <op> literal` 表达式建立紧凑
operand storage，最终 AST 仍尽量暴露为 `*BinaryExpression`，左右操作数仍保持
`*NameExpression` / `*LiteralExpression` 可见，从而减少函数体 AST 的三节点页分配。该方向要真正省掉
`NameExpression` / `LiteralExpression` 分配，需要重构 parser 的 primary/postfix/binary 入口，让
identifier 在确认不是函数调用、字段访问或复杂右结合表达式后再进入紧凑路径；否则只能在已经分配节点后
做无效搬运。这个改动会影响表达式优先级、AST 指针稳定性、parser 对外类型断言、codegen RK 快路径和错误
位置，因此不能作为本轮小切口直接实现。若后续推进，必须先补设计与定向测试，至少覆盖：

- `return x + 1` 仍对外表现为 `*BinaryExpression`，且左右类型、位置、源码行号和 codegen 字节码不变。
- `x + 1 * y`、`x + 1 + y`、`x ^ 1 ^ y`、`x .. "a" .. y` 的优先级和结合性不变。
- `x(1) + 2`、`x.y + 2`、`x[1] + 2` 不进入简单 name fast path。
- parser 错误文本、goto/label 语义、debug local 生命周期和官方反汇编不变。
- Go micro 需要证明 wall-clock 稳定下降或 B/op 明显下降；只减少 1-2 个 alloc/op 不足以保留生产改动。

2026-07-04 实现 codegen 子 generator scratch 复用：保留直接子 Proto arena 和 `Proto.p` 指针顺序，
但不再按直接函数数量预分配 `[]generator`。`generator` 只在当前子函数编译调用栈内使用，最终 Proto
只保存 `child.proto`；直接子函数按源码顺序同步编译，因此父 generator 只需惰性分配一个
`childGeneratorScratch` 并顺序复用。该改动不改变 `CLOSURE Bx` 顺序、子 Proto identity、upvalue
捕获、debug local 生命周期或 binary chunk 输出；超出直接子 Proto 预估时仍回退独立 Proto，避免覆盖
已追加到父 Proto 的子原型。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkCompileSource3000Functions`：约
  `4.19 / 4.19 / 4.19 / 4.19 / 4.30 ms/op`。
- B/op 约 `3.78 MB/op`，`89-90 allocs/op`。

结论：相比 `2f9dfda` 后 profile 记录的约 `4.416 ms/op`、`5.03 MB/op`、`87 allocs/op`，
该切口显著降低 codegen arena 空间成本，wall-clock 小幅下降，allocs/op 小幅回升但仍处于低对象数口径；
收益来自把“每个直接子函数一个 generator arena slot”改为“每个父 generator 一个顺序 scratch”。
后续若继续压缩编译期，重点应转向函数体 AST 紧凑表达式或 Source/token 读取结构，不再恢复大段
child generator arena。

2026-07-04 在 `41c03e7` 后重新 profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_41c03e7_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_41c03e7_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `4.327 ms/op`、`3.78 MB/op`、`87 allocs/op`。
- CPU profile 中 `Lexer.NextToken` 仍约 `18%` cum，`Source.Peek` 与 `Source.PeekOffset` 仍可见；
  alloc_space 已从上一轮大段 child generator arena 转向 `FunctionStatement`、直接函数容量预留和表达式 AST。

实现 `Source.Peek` / `PeekOffset` ASCII 快路径：ASCII 源码直接按单字节返回 rune，非 ASCII 与非法
UTF-8 仍交给 `utf8.DecodeRuneInString`，因此不改变 UTF-8 字节 Offset、rune 列号、BOM、CR/LF 归一、
非法字符 token 或 Mark/Reset 语义。新增测试固定 ASCII 预读、跨 ASCII 到 UTF-8 的 `PeekOffset` 和
预读不推进位置。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkCompileSource3000Functions`：约
  `4.18 / 4.16 / 4.23 / 4.12 / 4.12 ms/op`。
- B/op 约 `3.78 MB/op`，`89 allocs/op`。

结论：该切口只压缩 Source 预读的 ASCII 热路径，收益小但与 profile 方向一致，B/op 基本不变；
后续不要继续围绕 `Peek` 做局部分支堆叠，除非新的 profile 指向 `Source.Next` 或字符串/数字扫描中的
更具体读取成本。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 抽样中，
`compile_3000_functions` 为官方 `0.005389s`、本项目 `0.008717s`、倍率 `1.62x`；该结果与上一轮
`41c03e7` 后 `1.57x` 抽样处于同一量级，仍低于 `dd2c206` 后约 `1.77x` 的端到端口径。

同轮继续试验过 `Source.Next` ASCII 快路径：ASCII 按单字节推进，CR/LF 仍保持 Lua 5.3 的 CRLF/LFCR
单换行归一，非 ASCII 与非法 UTF-8 回落标准解码路径。该试验通过 gopls 与 Source/invalid UTF-8
定向测试，但 `BenchmarkCompileSource3000Functions` 五轮约 `4.24 / 4.22 / 4.21 / 4.22 / 4.24 ms/op`，
相比当前约 `4.10-4.23 ms/op` 没有证明稳定收益，生产改动已回退。后续不要重复做 `Source.Next`
ASCII 直推，除非 profile 显示它成为独立主热点并能在同机基线中稳定降低 wall-clock。

2026-07-04 在 `2f8dd80` 后重新 profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_2f8dd80_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_2f8dd80_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `4.17 ms/op`、`3.78 MB/op`、`87 allocs/op`。
- CPU profile 中可归因部分仍主要在 `Lexer.NextToken`、`Parser.advance` 和表达式 parser；
  `scanStringToken`、`skipLongComment`、`skipShortComment` 这类 token 起始试探仍会被普通
  identifier/number/operator 热路径反复调用。
- alloc_space 仍以 `FunctionStatement` 内嵌 AST、`prepareDirectFunctionBlockCapacity` 和表达式节点为主；
  本轮不继续改 AST 结构。

已实现 token 起始 guard：`NextToken` 只有在首字节为 `[`、`'` 或 `"` 时才进入字符串扫描；
`SkipIgnored` 只有在当前位置确实是 `--` 时才尝试长注释/短注释扫描。真正的长括号层级、短字符串转义、
注释消费、非法字符和普通 `[` 操作符仍由原有 scanner 处理；新增测试固定短注释、长注释、长字符串、
短字符串和普通方括号 token 流不变。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkCompileSource3000Functions`：约
  `3.29 / 3.29 / 3.28 / 3.28 / 3.29 ms/op`。
- B/op 约 `3.78 MB/op`，`88 allocs/op`。

结论：相比当前 `Source.Peek` 优化后约 `4.10-4.23 ms/op` 的口径，token 起始 guard 稳定降低
`compile_3000_functions` wall-clock，B/op 基本不变；收益来自跳过不可能命中的字符串和注释试探，
不是改变 Lua token 识别语义。后续不要继续围绕字符串/注释起始 guard 做分支堆叠，除非 profile
显示某个具体 scanner 重新成为独立主热点。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 抽样中，
`compile_3000_functions` 为官方 `0.005229s`、本项目 `0.007505s`、倍率 `1.44x`；相比上一轮
`Source.Peek` 优化后约 `1.57-1.62x` 的端到端口径继续下降。

2026-07-04 在 `4bafcbc` 后重新 profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_4bafcbc_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_4bafcbc_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `3.35 ms/op`、`3.78 MB/op`、`86 allocs/op`。
- CPU profile 中 `NextToken` 仍约 `19.53%` cum；`scanNumberToken` / `ScanNumber` 仍可见，说明
  普通 identifier/keyword 仍会先进入数字扫描试探。
- alloc_space 仍主要来自 AST 和 codegen 容量，未出现新的分配型小切口。

已实现 `NextToken` 首字节分派：跳过空白和注释后直接按当前 byte 判断字符串、数字、标识符或操作符。
数字边界保留 Lua 5.3 的 `.5` / `.2e2` 前导点浮点，单独 `.`、`..` 和 `...` 仍交给 operator
最长匹配；identifier 仍调用现有 ASCII 扫描与 keyword 判定；字符串、注释和非法非 ASCII token
仍沿用原 scanner。新增测试固定 `.5 .. . name 12` 的 token 流，避免把点号操作符误吞进数字路径。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkCompileSource3000Functions`：约
  `2.79 / 2.77 / 2.78 / 2.79 / 2.79 ms/op`。
- B/op 约 `3.78 MB/op`，`87 allocs/op`。

结论：相比 token 起始 guard 后约 `3.28-3.35 ms/op` 的口径，首字节分派继续稳定降低
`compile_3000_functions` wall-clock，且 B/op/allocs 不增加。后续不要再围绕 token 大类分派做重复微调；
若继续推进编译期，必须重新 profile，确认剩余是否进入 parser 表达式、语义分析或 codegen 小结构。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 抽样中，
`compile_3000_functions` 为官方 `0.005228s`、本项目 `0.006875s`、倍率 `1.31x`；相比 token
起始 guard 后约 `1.44x` 的端到端口径继续下降。该轮排序中剩余最高项转为 `table_rw` 约 `1.27x`，
其次 `arith_add_loop` 约 `1.21x`、`arith_mix_loop` 约 `1.19x`，`function_call`、`string_concat`
和 `recursion` 已接近 `1.0x`。

2026-07-04 在 `b002cc8` 后重新 profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_b002cc8_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_b002cc8_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `2.69 ms/op`、`3.78 MB/op`、`85 allocs/op`。
- CPU profile 中 `Lexer.NextToken` 已降到约 `9.70%` cum；剩余可见成本分散在
  `Parser.advance`、`parseBlockUntilInto`、`parseSubExpression`、`parseFunctionBodyInto` 和 codegen。
- alloc profile 已没有新的低风险单对象删除空间，主要是 parser/codegen arena 大块所有权。

已实现 `parseStatement` 分层 dispatch：先按 token kind 区分 illegal/operator/keyword，再在 keyword
内用 `switch currentToken.Text` 分派 `do/local/break/goto/for/function/if/while/repeat`。扩展语法仍在
普通 identifier/call 语句之前尝试，`(` 开头 call statement、label、空语句和非法 token 错误语义保持不变。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

- `BenchmarkCompileSource3000Functions`：约
  `2.67 / 2.67 / 2.67 / 2.68 / 2.94 ms/op`；其中最后一轮为慢轮。
- 单独 5s 复核为约 `2.63 ms/op`、`3.78 MB/op`、`85 allocs/op`。

结论：相比首字节分派后约 `2.77-2.79 ms/op` 的 Go micro 口径，parser 语句分派压缩能稳定降低
编译入口 wall-clock；但两次完整 benchmark 抽样的 `compile_3000_functions` 仍约 `1.33x`，与上一轮
`1.31x` 同属端到端噪声带，不能宣称 CLI 口径突破。继续推进编译期必须重新 profile；若没有新的
结构性 parser/codegen 切口，应转向当前同样接近最高项的 `table_rw`。

2026-07-04 在 `e5b67a2` 三轮完整基线后重新 profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_e5b67a2_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_e5b67a2_mem.pprof
```

- `BenchmarkCompileSource3000Functions`：约 `2.674 ms/op`、`3.78 MB/op`、`85 allocs/op`。
- CPU profile 中 runtime 调度、GC 和 madvise 噪声占比较高；可归因部分分散在 `Lexer.NextToken`
  约 `13.51%` cum、`Parser.advance` 约 `16.22%` cum、`parseBlockUntilInto` 约 `24.01%` cum、
  `parseFunctionStatement` 约 `22.58%` cum、`parseSubExpression` 约 `8.74%` cum。
- `alloc_space` 仍主要来自 `newFunctionStatement`、`prepareDirectFunctionBlockCapacity`、
  `newLiteralExpression`、`newBinaryExpression`、`newNameExpression` 和 Proto/codegen 结构。
- `alloc_objects` 的采样分布仍能看到表达式 AST、FunctionStatement arena、codegen inline capacity 和
  benchmark fixture 构造噪声；实际 per-op 已稳定在约 `85 allocs/op`，不再是简单对象数问题。

结论：当前编译期已经从 lexer 大类分派、标识符扫描、Source 预读、parser 语句分派和 codegen scratch
几个低风险切口中吃完主要收益；本轮 profile 没有新的独立 flat 热点。`expectKeyword`、十进制整数扫描、
`skipWhitespace`、`Source.Next`、expression arena 大页容量和紧凑表达式 AST 均已在前文证伪或被判定为
非小切口，不能继续局部调参。后续若继续压缩 `compile_3000_functions`，应先进入函数体紧凑 AST /
codegen arena 生命周期设计；否则下一轮更适合转向当前完整基线中仍高于 `1.0x` 的 `arith_add_loop`
prepared profile。

2026-07-04 在 `976728e` 后按最新排序重新复核 `compile_3000_functions`：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_976728e_cpu.pprof \
  -memprofile /tmp/go-lua-vm-next-profiles/compile_3000_after_976728e_mem.pprof
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=2s -count=5
```

- 单轮 profile：约 `2.586 ms/op`、`3.78 MB/op`、`85 allocs/op`。
- 五轮 micro：约 `2.570-2.579 ms/op`、`3.78 MB/op`、`88-89 allocs/op`。
- CPU profile 仍被 `runtime.madvise`、`runtime.kevent`、`runtime.usleep`、`runtime.pthread_cond_wait`
  等 runtime/GC/调度噪声主导；可归因源码侧中 `Lexer.NextToken` 约 `5.09%` cum、
  `Parser.advance` 约 `5.96%` cum、`parseBlockUntilInto` 约 `9.59%` cum、
  `parseFunctionBodyInto` 约 `7.41%` cum、codegen `compileBlock` 约 `3.49%` cum。
- `alloc_space` 主项仍是 `newFunctionStatement` 约 `35.90%`、`prepareDirectFunctionBlockCapacity`
  约 `26.50%`、`newLiteralExpression` 约 `10.84%`、`newBinaryExpression` 约 `6.23%`。
- `alloc_objects` 仍分散在 `parseExpressionList`、表达式 AST arena、函数语句 arena 和 codegen
  inline 预留；per-op 对象数已接近 `85-89 allocs/op`，没有新的单点删除空间。

结论：当前 `compile_3000_functions` 的 micro 已稳定在约 `2.57-2.59 ms/op`，但剩余差距不再对应
lexer token 解码、keyword 判定、Source ASCII 读取或函数语句容量预留这类低风险小切口。继续生产改动
必须先做更大的函数体紧凑 AST / codegen arena 生命周期方案，并接受 parser、semantic、codegen、错误位置
与 debug 信息的联合验收；本轮不保留生产改动，只记录证伪基线。

下一阶段允许评估的唯一编译期生产候选是“简单函数体紧凑编译计划”，先设计后实现，禁止直接替换全 parser：

- 目标形态只覆盖官方 fixture 同构源码：顶层 `function fN(x) return x + Kinteger end`，函数名为简单
  identifier，单个参数，函数体只有一条 `return <param> + <integer literal>`，无 vararg、无局部变量、无
  upvalue、无嵌套函数、无 table/method 函数名、无扩展语法。
- parser 仍必须消费完整 token 流，并在失败时返回原 parser 错误；紧凑路径只能在完整识别函数体后记录一个
  私有 `compactSimpleFunctionBody` 摘要。任一 token 不符合目标形态时回到现有 AST 节点构造路径。
- AST 对外语义暂不改变：`FunctionStatement.Body` 仍存在，`LineDefined`、`LastLineDefined`、`Position`、
  `Block.Return` 和 `ReturnStatement.Values` 对普通调用方仍可用。首轮 prototype 可只在 parser 内保存
  可选摘要，由 codegen 识别摘要后直发 Proto；不能让最终 Proto 持有 parser 临时状态。
- codegen 只允许在摘要和完整 AST 一致时使用直发路径：生成的子 Proto、参数局部变量、`LOADK/ADD/RETURN`
  指令、行号表、常量表、`MaxStackSize`、`LineDefined/LastLineDefined` 与普通路径必须逐项一致。
- semantic / scope 边界：若函数体涉及 upvalue、label/goto、local、vararg、尾调用、方法 self 或任何需要
  作用域分析的结构，必须回退普通 AST/codegen；局部变量生命周期和 debug local 信息必须与普通路径一致。
- 错误与 debug 边界：语法错误位置、`too many C levels`、函数参数限制、数字字面量错误、`debug.getinfo`
  可见的行号和 `luac -l -l` 反汇编必须保持一致。实现前必须新增官方 fixture 子集的 bytecode/golden
  对比测试，以及至少一个非目标函数体回退测试。
- 验收门槛：Go micro 必须在 `BenchmarkCompileSource3000Functions` 上稳定降低 wall-clock 至少 `5%`，
  且 B/op 不高于当前约 `3.78 MB/op` 的噪声范围；完整 benchmark 的 `compile_3000_functions` 应稳定低于
  当前约 `1.33x`。若只降低 allocs/op 或只改变采样 profile，必须回退生产改动并只保留证伪记录。

### `arith_add_loop`

2026-07-04 在 `38e9ad4` 后按最新排序转向 `arith_add_loop` prepared profile：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' \
  -bench '^BenchmarkPreparedArithAddLoopOfficial$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/arith_add_after_38e9ad4_cpu.pprof
go tool pprof -top /tmp/go-lua-vm-next-profiles/arith_add_after_38e9ad4_cpu.pprof
```

- `BenchmarkPreparedArithAddLoopOfficial`：约 `5.08 ms/op`、`0 allocs/op`。
- CPU profile 中 `runtime.(*VM).TryExecuteAddForLoopBatch` 约 `83.65%` flat / `84.16%` cum；
  `PrepareAddForLoopBatch` 约 `2.90%` flat；普通 `VM.Step` 只约 `1.36%` cum。

结论：该项剩余成本已经集中在现有 `ADD; FORLOOP` batch 本体。现有 batch 按 context 窗口一次最多提交
约 64 轮，但窗口内仍逐轮执行 `sum += i`、更新 FORLOOP 控制槽和判断退出；继续做 dispatch 或
`VM.Step` 微调没有意义。

已实现 `step == 1` 窄形态等差求和 batch：只在 `sum = sum + i` / `sum = i + sum` 已通过
`PrepareAddForLoopBatch`、控制槽和 sum 均为 integer、`step == 1`、外部可见循环变量等于内部 index、
且 index 非负时命中。命中后仍只提交调用方按 context 窗口给出的 `maxIterations`，不会扩大取消检查边界；
未到 limit 时写入下一轮 index 并跳回 ADD，退出时保留最后一次有效 index，不写入越界后的 nextIndex。
sum 使用 `uint64` 计算等差项和再转回 `int64`，保持 Lua integer 加法的二进制 wrap 语义。任一 guard
不满足继续走原逐轮 batch 或普通 VM。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' \
  -bench '^Benchmark(DoString|Prepared)ArithAddLoopOfficial$' \
  -benchmem -benchtime=2s -count=5
```

- `BenchmarkDoStringArithAddLoopOfficial`：约 `1.09-1.10 ms/op`、约 `122.4 KB/op`、`194 allocs/op`。
- `BenchmarkPreparedArithAddLoopOfficial`：约 `1.046-1.048 ms/op`、`0 allocs/op`。

对比本轮 profile 前约 `5.06-5.15 ms/op`，wall-clock 降幅约 `79%`；分配保持不变，收益完全来自
窗口内公式化提交。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 抽样结果：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007727s` | `0.005106s` | `0.66x` |
| `arith_mix_loop` | `0.011497s` | `0.013674s` | `1.19x` |
| `arith_chain_temp` | `0.013112s` | `0.010085s` | `0.77x` |
| `table_rw` | `0.007171s` | `0.006376s` | `0.89x` |
| `function_call` | `0.006864s` | `0.007085s` | `1.03x` |
| `string_concat` | `0.004702s` | `0.004977s` | `1.06x` |
| `closure_upvalue` | `0.008108s` | `0.007027s` | `0.87x` |
| `stdlib_math_string` | `0.019504s` | `0.011330s` | `0.58x` |
| `recursion` | `0.003691s` | `0.003941s` | `1.07x` |
| `compile_3000_functions` | `0.005249s` | `0.006853s` | `1.31x` |

结论：`arith_add_loop` 从当前三轮基线约 `1.23x` 降到抽样 `0.66x`，已经快于官方 Lua 5.3.6；
后续不要继续扩张该项。剩余高于 `1.0x` 的主项回到 `compile_3000_functions`、`arith_mix_loop`、
`recursion`、`string_concat` 和 `function_call`，但后三者接近噪声带，继续优化前必须先 profile。

### 3. `arith_mix_loop`：批量 mix arithmetic superinstruction

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

2026-07-04 在 `8fd6bce` 后重新 profile：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' \
  -bench '^BenchmarkPreparedArithMixLoopOfficial$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-next-profiles/arith_mix_after_8fd6bce_cpu.pprof
go tool pprof -top /tmp/go-lua-vm-next-profiles/arith_mix_after_8fd6bce_cpu.pprof
```

- `BenchmarkPreparedArithMixLoopOfficial`：约 `8.95 ms/op`、`0 allocs/op`。
- CPU profile 中 `PrepareMixArithmeticForLoopBatch` 约 `21.25%` flat，`TryExecuteMixArithmeticForLoopBatch`
  约 `16.31%` flat / `22.73%` cum；普通 `VM.Step` 约 `13.51%` cum。

结论：当前剩余成本不是普通 dispatch，而是 batch 路径每个 context 窗口都重复执行静态 batch guard。
已把 `PrepareMixArithmeticForLoopBatch` 中的目标寄存器、别名、控制槽和 `maxRegisterIndex` 等静态条件
前移到 `buildMixArithmeticForLoopSuperInstructions`，预匹配表额外记录 batch 元数据；执行期仍保留原有
Proto 绑定、寄存器边界、integer 类型、除数非零和写回 guard。别名形态继续不设置 `BatchValid`，回退
单轮 superinstruction 或普通 VM，保留逐指令覆盖顺序。

目标 Go micro 结果：

```bash
CGO_ENABLED=0 go test ./lua -run '^$' \
  -bench '^Benchmark(DoString|Prepared)ArithMixLoopOfficial$' \
  -benchmem -benchtime=2s -count=5
```

- `BenchmarkDoStringArithMixLoopOfficial`：约 `7.21-7.24 ms/op`、约 `130.4 KB/op`、`199 allocs/op`。
- `BenchmarkPreparedArithMixLoopOfficial`：约 `7.13-7.17 ms/op`、`0 allocs/op`。

对比本轮 profile 前 prepared 约 `8.95 ms/op`，wall-clock 降幅约 `20%`；分配保持不变，收益来自减少
每个 batch 窗口的重复静态验证。

重建 `bin/glua` / `bin/gluac` 后，显式使用官方 Lua/Luac 5.3.6 的完整 benchmark 抽样结果：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007364s` | `0.004623s` | `0.63x` |
| `arith_mix_loop` | `0.010693s` | `0.010906s` | `1.02x` |
| `arith_chain_temp` | `0.012983s` | `0.009527s` | `0.73x` |
| `table_rw` | `0.006723s` | `0.005993s` | `0.89x` |
| `function_call` | `0.006256s` | `0.006649s` | `1.06x` |
| `string_concat` | `0.004286s` | `0.004625s` | `1.08x` |
| `closure_upvalue` | `0.007626s` | `0.006503s` | `0.85x` |
| `stdlib_math_string` | `0.018863s` | `0.010749s` | `0.57x` |
| `recursion` | `0.003336s` | `0.003616s` | `1.08x` |
| `compile_3000_functions` | `0.004798s` | `0.006472s` | `1.35x` |

结论：`arith_mix_loop` 从上一轮抽样约 `1.19x` 降到 `1.02x`，已经进入官方基线噪声带；后续不应继续
围绕 mix arithmetic 局部扩张。剩余高于 `1.0x` 的主项仍是 `compile_3000_functions`，但前文已判定
需要更大的紧凑 AST / codegen arena 生命周期设计；其他 `recursion`、`string_concat`、`function_call`
也接近噪声带，继续优化前必须先 profile。

### 4. `string_concat`：窄形态字符串构建

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

### 5. `stdlib_math_string`：表达式级 math/string folding 扩展

该项当前约 `1.5x`。上一阶段已经覆盖 `string.format("%d", i)` 固定结果和
`#string.format("%d", i)` 长度消费；剩余可能来自 `math.sqrt`、`math.floor` 与浮点边界。

激进方案：

- 只在 profile 证明收益时，评估 `math.floor(math.sqrt(i))` 的表达式级窄形态。
- 必须对齐 Lua 5.3 的 number、NaN、-0、整数/浮点转换和错误文本。

验收：

- 默认完整 `stdlib_math_string` 稳定低于 `1.25x`。
- math/string 标准库官方测试继续通过。

## TODO

- [x] 跑下一阶段基线：默认完整 benchmark 三轮、剩余高于 `1.0x` 项的 Go micro 矩阵。
- [x] profile `compile_3000_functions`，确认 `FunctionStatement` / typed statement storage 仍是主切口，同时记录 codegen arena 空间成本。
- [x] 为 typed statement / compact AST 方案补设计小节，列出 parser、semantic、codegen、debug 和错误语义影响面。
- [x] 实现最小 typed statement prototype；若收益不足或 B/op 升高，记录证伪并回退。
- [x] profile `arith_mix_loop` prepared，确认当前主要成本在现有 mix fast path 内。
- [x] 补跑 `arith_mix_loop` DoString benchmark，确认端到端同步受益且没有新的编译期噪声。
- [x] 设计并实现 batch mix arithmetic superinstruction，证明 context、PC、debug hook、coroutine 和错误路径等价。
- [x] profile `string_concat` CPU/alloc，确认主因是 `executeConcat` 短命字符串分配和 GC 压力。
- [x] 复核 `string_concat` 官方 fixture 字节码与元方法可见性，再决定是否进入窄形态 builder。
- [x] 设计并补齐 `string_concat` builder guard 定向测试后，再决定是否实现窄形态 builder。
- [x] 实现 `string_concat` 窄形态 builder 原型，或先 profile `stdlib_math_string` 作为下一小切口。
- [x] 重建 CLI 后复核官方完整 benchmark，确认 `string_concat` 8000 次追加端到端收益。
- [x] profile `stdlib_math_string` 剩余热点，确认完整热体 batch 仍有表达式级消费消除空间。
- [x] 实现 `stdlib_math_string` 完整热体 batch superinstruction，并复核官方完整 benchmark。
- [x] profile `table_rw`，确认当前主要成本已从连续扩容和 dispatch 转为大数组分配与 GC 扫描。
- [x] 为 `table_rw` dense integer array 或逃逸消除方案补设计小节，先列出 materialize、迭代、元表、弱表和 debug 可见性边界。
- [x] 为 `table_rw` compact table 补最小 guard 测试，再决定是否实现 dense integer array prototype。
- [x] 实现 `table_rw` dense integer array prototype，若 B/op 或官方兼容语义不满足验收则回退。
- [x] 重建 CLI 后复核官方完整 benchmark，确认 `table_rw` 端到端倍率变化。
- [x] 复核 dense integer array 后 `table_rw` 剩余 CPU/alloc profile，记录 batch 内 dense-only 候选边界。
- [x] 为 `table_rw` batch 内 dense-only 直写/直读补 guard 测试，固定 materialize、元表和 mutation 可见性边界。
- [x] 实现 `table_rw` batch 内 dense-only 直写/直读，并复核 Go micro 与官方完整 benchmark。
- [x] 在 `77611b9` 后重建 CLI 并复跑默认完整 benchmark 三轮，重新排序剩余高于 `1.0x` 的路径。
- [x] 优化 lexer 标识符 ASCII 扫描，复核 `compile_3000_functions` Go micro 与 CLI 端到端收益。
- [x] 证伪纯十进制 int64 byte 快路径，未保留生产改动。
- [x] 证伪 `skipWhitespace` byte 直扫，未保留生产改动。
- [x] 证伪已知 keyword 直接 advance，未保留生产改动。
- [x] 优化 `Source.Peek` / `PeekOffset` ASCII 预读，复核 `compile_3000_functions` Go micro 收益。
- [x] 证伪 `Source.Next` ASCII 直推，未保留生产改动。
- [x] 优化 lexer 字符串与注释起始 guard，复核 `compile_3000_functions` Go micro 收益。
- [x] 优化 lexer token 首字节分派，复核 `compile_3000_functions` Go micro 收益。
- [x] 优化 parser 语句分派，复核 `compile_3000_functions` Go micro 与 CLI 抽样口径。
- [x] 在 `e5b67a2` 后重新 profile `compile_3000_functions`，记录暂无新的低风险编译期小切口。
- [x] 优化 `arith_add_loop` step=1 batch 等差求和，复核 Go micro 与 CLI 抽样收益。
- [x] 优化 `arith_mix_loop` batch 静态 guard 前移，复核 Go micro 与 CLI 抽样收益。
- [x] 在 `976728e` 后重新 profile `compile_3000_functions`，记录仍无低风险编译期小切口。
- [x] 在 `f5f9028` 后重建 CLI 并复跑默认完整 benchmark 三轮，确认剩余唯一清晰主项是编译期。
- [x] 为 `compile_3000_functions` 简单函数体紧凑编译计划补设计门禁，限定目标形态和回退验收。
- [x] 为简单函数体紧凑编译计划补 bytecode/debug 定向测试，固定目标形态和非目标回退形态。
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
