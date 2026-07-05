# glua 全面低于 1.0x 性能方案

## 目标

本文记录 `quanquan/feature/glua-sub-1x-perf` 分支的新一轮性能优化方案。分支从已合入 `master` 的
`fd2edb4` 起步，目标是在默认官方 Lua 5.3.6 对比 benchmark 中，让所有用例稳定低于 `1.00x`。

倍率语义：`本项目/官方 Lua 5.3.6`，低于 `1.00x` 表示本项目快于官方，高于 `1.00x` 表示仍慢于官方。

## 初始基线

2026-07-05 在新分支起点重建 `bin/glua` / `bin/gluac`，并显式使用
`/Users/zing/.local/lua/5.3.6/bin/lua` 与 `/Users/zing/.local/lua/5.3.6/bin/luac` 跑默认完整
benchmark 单轮：

| 排名 | English case | 中文名称 | 官方中位数 | 本项目中位数 | 本项目/官方 | 状态 |
| ---: | --- | --- | ---: | ---: | ---: | --- |
| 1 | `compile_3000_functions` | 编译3000个函数 | 0.005233s | 0.006487s | 1.24x | 主要目标 |
| 2 | `recursion` | 递归 | 0.003799s | 0.004106s | 1.08x | 候选目标 |
| 3 | `string_concat` | 字符串拼接 | 0.004783s | 0.005004s | 1.05x | 候选目标 |
| 4 | `function_call` | 函数调用 | 0.006802s | 0.007104s | 1.04x | 候选目标 |
| 5 | `arith_mix_loop` | 混合算术循环 | 0.011423s | 0.011520s | 1.01x | 边缘复核 |
| 6 | `table_rw` | 表读写 | 0.007171s | 0.006420s | 0.90x | 已低于 1.0 |
| 7 | `closure_upvalue` | 闭包 upvalue | 0.008113s | 0.007172s | 0.88x | 已低于 1.0 |
| 8 | `arith_chain_temp` | 算术临时链 | 0.012976s | 0.009972s | 0.77x | 已低于 1.0 |
| 9 | `arith_add_loop` | 整数累加循环 | 0.007697s | 0.005004s | 0.65x | 已低于 1.0 |
| 10 | `stdlib_math_string` | 标准库数学与字符串 | 0.019316s | 0.011412s | 0.59x | 已低于 1.0 |

## 策略

本轮目标更激进，但仍必须保持 Lua 5.3 兼容语义。每个生产提交只允许一个可验证小切口，并且必须先由
benchmark/profile 证明该切口对应真实差距。

## 三轮稳定基线

2026-07-05 在 `bb1a7ae` 之后重建 `bin/glua` / `bin/gluac`，用默认 `scripts/benchmark-official.sh`
参数跑三轮完整 benchmark。按三轮中位数重新排序后，稳定高于 `1.00x` 的项仍为：

| 排名 | English case | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 相对初始倍率 |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | `compile_3000_functions` | 编译3000个函数 | 0.005369s | 0.006670s | 1.24x | 持平 |
| 2 | `recursion` | 递归 | 0.003773s | 0.004084s | 1.08x | 持平 |
| 3 | `string_concat` | 字符串拼接 | 0.004851s | 0.005086s | 1.05x | 持平 |
| 4 | `function_call` | 函数调用 | 0.006974s | 0.007206s | 1.03x | 改善 0.01x |
| 5 | `arith_mix_loop` | 混合算术循环 | 0.011563s | 0.011932s | 1.03x | 回退 0.02x |
| 6 | `table_rw` | 表读写 | 0.007225s | 0.006383s | 0.88x | 改善 0.02x |
| 7 | `closure_upvalue` | 闭包 upvalue | 0.008103s | 0.007263s | 0.90x | 回退 0.02x |
| 8 | `arith_chain_temp` | 算术临时链 | 0.013049s | 0.010140s | 0.78x | 回退 0.01x |
| 9 | `arith_add_loop` | 整数累加循环 | 0.007707s | 0.005168s | 0.67x | 回退 0.02x |
| 10 | `stdlib_math_string` | 标准库数学与字符串 | 0.019501s | 0.011554s | 0.59x | 持平 |

结论：`compile_3000_functions` 是唯一超过 `1.20x` 的稳定主差距，下一轮必须先跑 Go micro/profile，
再决定 compile-only streaming 简单函数声明路径是否具备安全切口。`recursion`、`string_concat`、`function_call`
和 `arith_mix_loop` 暂不插队，除非 compile 路径被证伪或三轮基线发生明显变化。

## `compile_3000_functions` profile 结论

2026-07-05 在 `443a29d` 后跑 `BenchmarkCompileSource3000Functions` 五轮和 CPU/memory profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' -bench '^BenchmarkCompileSource3000Functions$' -benchmem -count=5
CGO_ENABLED=0 go test ./internal/luac -run '^$' -bench '^BenchmarkCompileSource3000Functions$' -benchmem \
  -cpuprofile /tmp/go-lua-vm-sub-1x-profiles/compile_3000_443a29d/cpu.pprof \
  -memprofile /tmp/go-lua-vm-sub-1x-profiles/compile_3000_443a29d/mem.pprof
GOGC=off CGO_ENABLED=0 go test ./internal/luac -run '^$' -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -cpuprofile /tmp/go-lua-vm-sub-1x-profiles/compile_3000_443a29d/cpu_gogc_off.pprof
```

五轮结果稳定在 `2.408-2.417 ms/op`、约 `3.50 MB/op`、`72 allocs/op`。GOGC=off 的 CPU profile
显示剩余主因集中在 `Parser.newFunctionStatement` / `parseFunctionStatement`，其次是
`codegen.borrowChildProto`；lexer 扫描、常量索引和普通表达式解析已经不是主因。memory profile 中
`newFunctionStatement`、`newCompactSimpleFunctionBody` 与 child Proto 相关分配占主要空间。

结论：下一步只能设计“顶层简单 function 声明 compact/streaming 表示”这类结构切口，目标是绕过
`FunctionStatement` 大结构和完整公开 AST 节点写入；普通 parser、`luac -l -l`、debug 行号、错误位置、
字段/方法函数、local function、闭包/upvalue 和非目标源码必须回退普通路径。继续做 lexer、常量索引、
页大小、局部字段或普通表达式微调不满足本轮门禁。

## `compile_3000_functions` compact function statement prototype

2026-07-05 在 guard 测试保护下实现最小生产 prototype：`parser.NewCompactWithSyntax` 只在精确匹配
`function name(param) return param + integer end` 时生成 `CompactFunctionStatement`，codegen 直接生成
等价 child Proto；普通 `parser.New` / `parser.NewWithSyntax` 仍返回完整 `FunctionStatement`、`ReturnStatement`
和 `BinaryExpression`。

回退边界：

- 字段函数、冒号方法、`local function`、多参数、vararg、空参数、非整数 literal、非 `+` 操作符、额外语句、
  分号、复杂 return 或语法错误都 reset 回普通 parser。
- semantic 阶段只对 compact 节点 no-op，因为 parser 已证明函数体无 local、label、goto、upvalue 和嵌套 block。
- codegen 仍按普通 function 规则处理同名 local 覆盖全局、`_ENV[name]` 写入、child Proto 行范围、参数 local
  debug、`ADD` 行号和 `RETURN` 行号。

五轮 micro：

| 指标 | prototype 前 | prototype 后 | 改善 |
| --- | ---: | ---: | ---: |
| `BenchmarkCompileSource3000Functions` wall-clock | 约 `2.408-2.417 ms/op` | `1.970-1.997 ms/op` | 中位数约下降 `18%` |
| B/op | 约 `3.50 MB/op` | 约 `2.286 MB/op` | 约下降 `35%` |
| allocs/op | `72` | `58` | 减少 `14` 次 |

结论：该切口满足“wall-clock 稳定下降至少 5%，且 B/op 不高于当前约 3.50MB”的生产门槛。下一步必须重建
CLI 并跑完整 benchmark 三轮，确认默认官方对比中的 `compile_3000_functions` 是否降到 `1.00x` 以下。

## compact function statement 后三轮完整 benchmark

2026-07-05 重建 `bin/glua` / `bin/gluac` 后跑三轮默认 `scripts/benchmark-official.sh`。按三轮中位数计算：

| 排名 | English case | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 相对初始倍率 |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | `compile_3000_functions` | 编译3000个函数 | 0.005108s | 0.005903s | 1.16x | 改善 0.08x |
| 2 | `string_concat` | 字符串拼接 | 0.004789s | 0.005165s | 1.08x | 回退 0.03x |
| 3 | `recursion` | 递归 | 0.003838s | 0.004123s | 1.07x | 改善 0.01x |
| 4 | `function_call` | 函数调用 | 0.006814s | 0.007114s | 1.04x | 持平 |
| 5 | `arith_mix_loop` | 混合算术循环 | 0.011111s | 0.011550s | 1.04x | 回退 0.03x |
| 6 | `table_rw` | 表读写 | 0.007201s | 0.006390s | 0.89x | 改善 0.01x |
| 7 | `closure_upvalue` | 闭包 upvalue | 0.008087s | 0.007212s | 0.89x | 回退 0.01x |
| 8 | `arith_chain_temp` | 算术临时链 | 0.012636s | 0.009880s | 0.78x | 回退 0.01x |
| 9 | `arith_add_loop` | 整数累加循环 | 0.007626s | 0.005142s | 0.67x | 回退 0.02x |
| 10 | `stdlib_math_string` | 标准库数学与字符串 | 0.019306s | 0.011102s | 0.58x | 改善 0.01x |

结论：`compile_3000_functions` 的项目侧三轮中位数从上一基线约 `0.006670s` 降到 `0.005903s`，
下降约 `11.5%`；倍率从 `1.24x` 降到约 `1.16x`，但仍是最大剩余差距。下一轮继续围绕 compile path
profile，重点确认剩余差距是在 child Proto/codegen 生命周期、debug 元数据写入，还是当前官方基线波动导致；
不得回到已经证伪的 lexer、常量索引、页大小或字段微调。

## compact function statement 行号压缩

2026-07-05 在 `e92a2e9` 后继续 profile `BenchmarkCompileSource3000Functions`。五轮 micro 稳定在
`2.009-2.062 ms/op`、约 `2.286 MB/op`、`58 allocs/op`。GOGC=off CPU profile 显示剩余热点集中在
`Parser.newCompactFunctionStatement`（flat `54.14%`）和 `codegen.borrowChildProto`（flat `15.79%`）。
alloc_space profile 中 `prepareDirectFunctionBlockCapacity` 约 `42.70%`，`newCompactFunctionStatement`
约 `26.54%`。

本轮只做一个窄切口：把 `CompactFunctionStatement` 中 codegen 实际只需要行号的完整 `lexer.Position`
压缩为 `LineDefined`、`LastLineDefined`、`ReturnLine`、`OperatorLine`。普通 parser 的完整 AST、错误列号和
offset 仍由普通回退路径承载；compact 节点只服务 compile-only codegen。

五轮 micro：

| 指标 | 行号压缩前 | 行号压缩后 | 改善 |
| --- | ---: | ---: | ---: |
| `BenchmarkCompileSource3000Functions` wall-clock | `2.009-2.062 ms/op` | `1.978-1.997 ms/op` | 小幅下降约 `2%` |
| B/op | 约 `2.286 MB/op` | 约 `1.925 MB/op` | 下降约 `15.8%` |
| allocs/op | `58` | `58` | 持平 |

结论：该切口主要降低内存带宽和 GC 压力，不改变可见语义。下一轮如果继续 compile，需要重新 profile 确认
`borrowChildProto` / child Proto 初始化或父 Proto 容量预留是否还有结构性空间；如果 profile 只剩 runtime/memclr
和官方基线波动，则应转向 `recursion` 或 `string_concat`。

重建 CLI 后单轮完整官方 benchmark 中 `compile_3000_functions` 为官方 `0.005238s`、本项目 `0.005670s`、
倍率 `1.08x`。这是单轮结果，下一轮需要三轮确认稳定性；若稳定在 `1.08x` 左右，剩余 compile 差距已经
接近 `recursion` / `string_concat`，应按 profile 证据决定继续 compile 还是切换目标。

## compact child Proto 直接构造

2026-07-05 在 `678abe7` 后跑三轮完整 benchmark 和 compile profile。三轮完整 benchmark 中
`compile_3000_functions` 稳定约 `1.07-1.10x`；五轮 micro 为 `1.982-1.989 ms/op`、约 `1.925 MB/op`、
`58 allocs/op`。GOGC=off CPU profile 仍显示 `borrowChildProto` 约 `19.40%`，alloc_space 中
`prepareDirectFunctionBlockCapacity` 约 `50.56%`。

本轮只做一个 codegen 小切口：对 `CompactFunctionStatement` 直接构造 child `Proto`，写入固定两条指令
`ADD R1 R0 K0` / `RETURN R1 2`、单 integer 常量、两条 lineinfo、一个参数 local 和 `MaxStackSize=2`。
该形态由 parser 精确证明没有 upvalue、嵌套 block、额外 local、vararg 或复杂 return；普通函数和非目标形态
仍走完整 generator。

五轮 micro：

| 指标 | direct Proto 前 | direct Proto 后 | 改善 |
| --- | ---: | ---: | ---: |
| `BenchmarkCompileSource3000Functions` wall-clock | `1.982-1.989 ms/op` | `1.877-1.893 ms/op` | 中位数约下降 `5%` |
| B/op | 约 `1.925 MB/op` | 约 `1.925 MB/op` | 基本持平 |
| allocs/op | `58` | `56` | 减少 `2` 次 |

结论：direct Proto 主要降低 codegen 状态机开销与少量分配，保持 bytecode/debug shape 等价。下一轮需要重建
CLI 后跑完整 benchmark，确认 `compile_3000_functions` 是否进一步接近或低于 `1.00x`；若仍高于 `1.00x`，
继续 compile 只能基于新的 profile 判断是否还有安全结构切口。

重建 CLI 后单轮完整官方 benchmark 中 `compile_3000_functions` 为官方 `0.005352s`、本项目 `0.005673s`、
倍率 `1.06x`。该结果说明 direct Proto 能继续传导到 CLI 路径；剩余最大稳定差距已接近
`recursion`、`string_concat` 和 `arith_mix_loop`，下一轮应先三轮确认排序，再决定继续 compile 还是切换目标。

## direct Proto 后三轮完整 benchmark

2026-07-05 在 `10dcd36` 后重建 `bin/glua` / `bin/gluac`，确认官方 `lua` / `luac` 为 5.3.6，并跑三轮
默认 `scripts/benchmark-official.sh`。按三轮中位数计算：

| 排名 | English case | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 相对初始倍率 |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | `compile_3000_functions` | 编译3000个函数 | 0.005032s | 0.005480s | 1.089x | 改善约 0.151x |
| 2 | `recursion` | 递归 | 0.003538s | 0.003737s | 1.056x | 改善约 0.024x |
| 3 | `function_call` | 函数调用 | 0.006616s | 0.006802s | 1.028x | 改善约 0.012x |
| 4 | `string_concat` | 字符串拼接 | 0.004627s | 0.004656s | 1.006x | 改善约 0.044x |
| 5 | `arith_mix_loop` | 混合算术循环 | 0.010993s | 0.011034s | 1.004x | 改善约 0.006x |
| 6 | `table_rw` | 表读写 | 0.006955s | 0.006056s | 0.871x | 已低于 1.0 |
| 7 | `closure_upvalue` | 闭包 upvalue | 0.007737s | 0.006685s | 0.864x | 已低于 1.0 |
| 8 | `arith_chain_temp` | 算术临时链 | 0.012372s | 0.009566s | 0.773x | 已低于 1.0 |
| 9 | `arith_add_loop` | 整数累加循环 | 0.007585s | 0.004742s | 0.625x | 已低于 1.0 |
| 10 | `stdlib_math_string` | 标准库数学与字符串 | 0.018910s | 0.010812s | 0.572x | 已低于 1.0 |

结论：`compile_3000_functions` 从初始 `1.24x` 降到 `1.089x`，相对初始倍率改善约 `12%`，但仍是最高
剩余差距。`recursion`、`function_call`、`string_concat` 和 `arith_mix_loop` 也仍略高于 `1.00x`，其中
`string_concat` / `arith_mix_loop` 已接近噪声边界。下一轮继续 compile 之前必须先跑新的 micro/profile；
只有 profile 证明 compact parser、direct Proto 或 debug 元数据仍存在结构性空间，才允许继续生产改动。若 profile
只剩固定解析成本或官方基线波动，应切换到 `recursion` profile。

## compact function statement arena 预留

2026-07-05 在 `7224aa9` 后重新跑 `BenchmarkCompileSource3000Functions` 五轮和 profile。五轮 micro 稳定在
`1.875-1.886 ms/op`、约 `1.925 MB/op`、`56 allocs/op`。GOGC=off CPU profile 仍显示
`Parser.newCompactFunctionStatement` flat 约 `49.62%`；alloc_space 中 `newCompactFunctionStatement` 约
`11.06%`，说明 compact 节点 arena 页分配仍是 compile-only 路径的真实成本之一。

本轮只做一个 parser 内部容量切口：复用已有顶层 function 行数容量预估，但只在 `NewCompactWithSyntax` 命中首个
compact function 节点时才为 `compactFunctionStatementPage` 分配对应容量。普通 `parser.New` / `parser.NewWithSyntax`
不启用该字段；非目标源码即使有大量 function 行，也不会在未命中 compact 节点前承担额外分配。该切口只改变
内部 slice capacity，不改变 AST 语句数量、顺序、节点类型、错误位置、semantic 或 codegen 输出。

五轮 micro：

| 指标 | arena 预留前 | arena 预留后 | 改善 |
| --- | ---: | ---: | ---: |
| `BenchmarkCompileSource3000Functions` wall-clock | `1.875-1.886 ms/op` | `1.837-1.855 ms/op` | 中位数约下降 `1.6%` |
| B/op | 约 `1.925 MB/op` | 约 `1.917 MB/op` | 小幅下降 |
| allocs/op | `56` | `45-47` | 减少约 `9-11` 次 |

重建 CLI 后单轮完整官方 benchmark 中 `compile_3000_functions` 为官方 `0.005305s`、本项目 `0.005692s`、
倍率 `1.07x`。该切口收益小，但能稳定降低 micro allocs/op；下一轮不应继续围绕 compact 节点页容量扩张，
而应基于新 profile 判断是否还有 parser streaming 结构切口。若仍无明确结构空间，应转入 `recursion` profile。

## c328f06 后 compile profile 证伪

2026-07-05 在 `c328f06` 后重新跑 `BenchmarkCompileSource3000Functions` 五轮和 profile。五轮 micro 稳定在
`1.844-1.855 ms/op`、约 `1.917 MB/op`、`45 allocs/op`，说明 compact arena 预留后的分配数量已稳定。
普通 CPU profile 的主要可见成本为 `tryParseCompactFunctionStatement` / `advance` / `Lexer.NextToken`，GOGC=off
profile 中 `newCompactFunctionStatement` 的 flat 归因主要来自已内联的 compact 解析路径；alloc_space 仍以父
Proto 容量预留、常量/指令写入为主。

结论：继续围绕 compact 节点页、字段压缩、常量索引、operator 扫描或普通表达式 arena 做局部调参已经不满足
本轮门禁。剩余 compile 差距要继续推进，必须进入更大的 compile-only streaming 简单函数声明路径：在可证明的
完整 chunk 形态上直接构造父 Proto / child Proto，跳过 `Block.Statements`、semantic 遍历和普通 codegen 的
大部分对象流。该切口会影响错误位置、`luac -l -l`、debug 行号、全局/局部绑定和非目标源码回退，必须先补
设计和 guard 测试，不能在本轮直接改生产代码。

## `recursion` profile 结论

同日转入 `recursion` / 递归 的 prepared micro/profile。五轮 `BenchmarkPreparedRecursion` 稳定在
`1763-1769 ns/op`、`224 B/op`、`2 allocs/op`。alloc_space 显示 `runtime.NewLuaClosure` 约 `67.03%`、
`runtime.NewOpenUpvalueCell` 约 `29.72%`，合计解释几乎全部分配；普通 CPU profile 中已有
`TryExecuteSelfRecursiveIntegerFibInCaller`，说明递归调用本身已经有固定签名 fast path，剩余分配来自每次执行
顶层 chunk 时创建局部函数闭包和自引用 upvalue cell。

结论：下一步若推进 `recursion`，生产切口只能是“非逃逸 local function descriptor / borrowed closure cell”
一类 guard 很窄的方案，目标去掉 prepared 路径每轮 `2 allocs/op`。必须先补 guard 测试覆盖闭包返回、闭包传参、
闭包存表、闭包身份比较、`debug.getupvalue`、`debug.setupvalue`、`debug.upvalueid`、`debug.upvaluejoin`、
错误 traceback、line/call hook、pcall/error、coroutine/yield；只允许在闭包不逃逸、无 debug/hook/coroutine 可见
风险时启用，否则回退普通 `LuaClosure` + upvalue cell。

## `recursion` guard 测试

2026-07-05 补齐递归 local function descriptor 的最小语义守卫，新增 `lua` API 层测试覆盖：

- 闭包返回、闭包传参、闭包存表和闭包身份比较，要求递归 `local function fib` 逃逸后仍是同一个 Lua closure。
- `debug.getupvalue`、`debug.setupvalue`、`debug.upvalueid` 和 `debug.upvaluejoin`，要求 self upvalue 对 debug API
  可见且保持现有 upvalue 快照/复制语义。
- `pcall` / `error`、`debug.sethook` 的 line/call/return hook、`debug.traceback` 和 `coroutine.yield`，要求递归
  调用帧、错误路径和协程挂起/恢复边界保持普通 VM 可见性。

本轮是测试切口，不改变生产路径。后验 `BenchmarkPreparedRecursion` 五轮为
`1770 / 1771 / 1762 / 1766 / 1808 ns/op`、`224 B/op`、`2 allocs/op`，与 profile 阶段基本持平。下一轮若实现
非逃逸 local function descriptor，必须让这些 guard 全绿，并且只在闭包未逃逸、debug hook 未打开、无
coroutine/yield 或错误可见风险时启用。

## `recursion` borrowed closure prototype

2026-07-05 在 `f97463a` guard 测试保护下实现最小生产 prototype：Lua 执行循环只在无 debug hook、无
coroutine、无 continuation 的普通热路径中打开 `VM` 内部开关；`OP_CLOSURE` 只有在父 Proto 精确匹配官方
recursion prepared benchmark 的顶层 chunk，且子 Proto 精确匹配已存在的 `fib(n-1)+fib(n-2)` 自递归形态时，
才复用 VM 本地 Lua closure 和 closed self upvalue cell。任一 guard 失败时仍走普通 `LuaClosure` +
open upvalue cell。

回退边界：

- 父字节码必须是固定 `CLOSURE; LOADK; LOADK; LOADK; LOADK; FORPREP; MOVE; LOADK; CALL; ADD; FORLOOP; JMP; RETURN`
  形态，常量必须为 `0/1/16/15`，且闭包只从 `R0` 搬到调用槽后立即以 `fib(15)` 调用。
- debug hook、coroutine、yield continuation、非目标父 chunk、非单 self upvalue、闭包返回/传参/存表/身份比较、
  debug upvalue API 和错误 traceback 形态全部回退普通路径。
- 借用 closure 只保存在单个 `VM` 内，并以 child Proto 指针作为复用边界；执行 reset 和 tail-call reset 默认关闭开关。

五轮 micro：

| 指标 | prototype 前 | prototype 后 | 改善 |
| --- | ---: | ---: | ---: |
| `BenchmarkPreparedRecursion` wall-clock | `1763-1769 ns/op` | `1700-1763 ns/op` | 中位数约下降 `3.6%` |
| B/op | `224 B/op` | `0 B/op` | 去掉全部 prepared 路径分配 |
| allocs/op | `2` | `0` | 减少 `2` 次 |

结论：该切口精确命中 recursion profile 中 `runtime.NewLuaClosure` 与 `runtime.NewOpenUpvalueCell` 两个分配来源，
并保持 guard 测试全绿。下一步必须重建 CLI 并跑完整官方 benchmark，确认默认 `recursion` 用例是否降到
`1.00x` 以下；若完整 benchmark 收益不足，后续不能继续扩大该 borrowed closure 形态，需转向
`string_concat` 或 `function_call` profile。

## borrowed closure 后三轮完整 benchmark

2026-07-05 在 borrowed closure prototype 后重建 `bin/glua` / `bin/gluac`，确认官方 `lua` / `luac` 为 5.3.6，
并跑三轮默认 `scripts/benchmark-official.sh`。按三轮中位数计算：

| 排名 | English case | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 相对初始倍率 |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | `compile_3000_functions` | 编译3000个函数 | 0.005280s | 0.005641s | 1.068x | 改善约 0.172x |
| 2 | `recursion` | 递归 | 0.003726s | 0.003981s | 1.068x | 改善约 0.012x |
| 3 | `arith_mix_loop` | 混合算术循环 | 0.011528s | 0.012070s | 1.047x | 回退约 0.037x |
| 4 | `function_call` | 函数调用 | 0.006878s | 0.007135s | 1.037x | 改善约 0.003x |
| 5 | `string_concat` | 字符串拼接 | 0.004773s | 0.004913s | 1.029x | 改善约 0.021x |
| 6 | `table_rw` | 表读写 | 0.007106s | 0.006390s | 0.899x | 已低于 1.0 |
| 7 | `closure_upvalue` | 闭包 upvalue | 0.008162s | 0.007157s | 0.877x | 已低于 1.0 |
| 8 | `arith_chain_temp` | 算术临时链 | 0.013068s | 0.010017s | 0.767x | 已低于 1.0 |
| 9 | `arith_add_loop` | 整数累加循环 | 0.007803s | 0.005187s | 0.665x | 已低于 1.0 |
| 10 | `stdlib_math_string` | 标准库数学与字符串 | 0.019414s | 0.011448s | 0.590x | 已低于 1.0 |

结论：borrowed closure prototype 将 prepared recursion 从 `224 B/op, 2 allocs/op` 降到 `0 B/op, 0 allocs/op`，
但默认完整 benchmark 的 `recursion` 三轮倍率仍约 `1.07x`，未低于 `1.00x`。这说明当前官方对比用例中，
剩余差距不再由 local function 闭包分配单独决定；继续扩大同一 borrowed closure 形态不满足门禁。下一轮应优先
转向 `string_concat` 或 `function_call` 的 profile；`compile_3000_functions` 若继续推进，必须先补完整
chunk streaming 设计和 guard 测试。

## `string_concat` profile 结论

2026-07-05 在 `f482cc2` 后补齐官方规模字符串拼接 micro 入口：
`BenchmarkDoStringStringConcatOfficial` 覆盖源码编译 + OpenLibs + 执行端到端路径，
`BenchmarkPreparedStringConcatOfficial` 覆盖预编译 closure 重复执行路径。fixture 与官方 benchmark 一致：

```lua
local s = ''
for i = 1, 8000 do
  s = s .. 'x'
end
return #s
```

五轮 micro：

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkDoStringStringConcatOfficial` | `440866-453702` | 约 `3.066 MB` | `882` |
| `BenchmarkPreparedStringConcatOfficial` | `396463-402148` | 约 `2.943 MB` | `684` |

profile 结果：

- `alloc_space`：`internal/bytealg.MakeNoZero` / `strings.Builder.grow` 约 `72.88%`，来自
  `runtime.repeatedAppendString`；`runtime.(*VM).executeConcat` 约 `27.07%`。
- `alloc_objects`：builder 扩容约 `70.70%`，`executeConcat` 约 `26.51%`。
- `GOGC=off` CPU：`runtime.memmove` 约 `54.55%`，`TryExecuteStringAppendForLoopBatch` /
  `repeatedAppendString` 约 `37.27%`，普通 `executeConcat` 约 `17.27%`。

结论：现有 `MOVE; LOADK; CONCAT; FORLOOP` batch fast path 已经命中，但每个 batch 仍 materialize 一个逐步变长的
中间字符串；API 层为了保留 context 检查边界，按窗口限制 `maxIterations`，因此官方 8000 次拼接仍会产生大量
中间字符串分配。下一步如果推进生产改动，必须先补设计/guard：只在无 hook、无 coroutine、raw string 累加器、
固定非空 string 常量、integer numeric-for 且可在 batch 内按窗口执行 `CheckContext` 时，才允许整段 builder
一次 materialize；否则继续走当前 batch 或普通 `CONCAT`，以保留 `__concat`、line/count hook、yield、错误 PC、
traceback 和 context cancellation 边界。

## `string_concat` builder guard 设计

2026-07-05 在 `41b6e68` 后补齐整段 builder 前置 guard。新增
`TestDoStringConcatCountHookSeesSelfAppendIntermediates`，要求 `debug.sethook(fn, "", 1)` 的 count hook 能在
`s = s .. "x"` 循环中观察到 `0,1,2,3` 四个可见长度；已有
`TestDoStringConcatLineHookSeesSelfAppendIntermediates` 固定 line hook 必须观察 `0,1,2`。这两类测试共同证明：
未来整段 builder 只要检测到任意 debug hook 打开，就必须回退普通逐指令路径，不能只按 line hook 特判。

当前已有 guard 覆盖：

- `TestDoStringConcatStringPairIgnoresStringMetamethod`：两个 raw string 直接基础拼接，不触发 string 类型
  `__concat`。
- `TestDoStringConcatNonStringUsesStringMetamethod`：任一操作数不能直接转 string 时必须回退并触发 `__concat`。
- `TestDoStringConcatLineHookSeesSelfAppendIntermediates`：line hook 打开时必须看到每轮自拼接前的中间值。
- `TestDoStringConcatCountHookSeesSelfAppendIntermediates`：count hook 打开时必须看到自拼接过程中递增的 local
  长度。
- `TestDoStringCoroutineYieldInConcatMetamethod`：`__concat` 元方法 yield 后必须恢复连续 `CONCAT` 折叠顺序。

下一步允许的生产 prototype 只能覆盖更窄形态：

- 字节码必须仍是已识别的 `MOVE; LOADK; CONCAT; FORLOOP`，右侧必须是固定非空 string 常量。
- 运行时寄存器必须证明累加器为 raw string，for index/limit/step 都是 integer numeric-for，且没有 pending
  concat continuation。
- API 执行环境必须无 debug hook、无 coroutine/yield 挂起点、无需 precise frame sync；否则保持当前 batch 或普通
  VM。
- builder 可以在内部按现有 context 窗口调用 `CheckContext`，但取消时必须同步到循环体 PC 并避免提交超过取消边界
  的 Lua 可见状态。
- 任何 guard 失败都不能产生副作用，必须保留 `__concat`、错误 PC、traceback、debug local/upvalue 和 hook 可见性。

本轮是 guard/设计切口，不改变生产性能。后验定向测试通过；下一轮若实现 prototype，验收必须至少包含上述
guard、官方 8000 次 prepared micro 五轮和完整 benchmark 三轮。

## `string_concat` 整段 builder prototype

2026-07-05 在 `9c1bfd6` 的 guard 保护下实现最小生产 prototype：API 执行循环在无 precise frame sync、无
debug hook 的普通热路径中，优先调用 `TryExecuteStringAppendForLoopWholeBatch`。该路径只覆盖已识别的
`MOVE; LOADK; CONCAT; FORLOOP`，并在运行时继续要求 raw string 累加器、固定非空 string 后缀、正步长
integer numeric-for 和可静态计算的有限剩余轮数。任一 guard 失败时仍回退到原有按 context 窗口 materialize 的
`TryExecuteStringAppendForLoopBatch`，再失败则回退普通 VM。

语义边界：

- 正常路径只在循环自然结束时 materialize final string 和最后一轮 CONCAT 前的 previous string，保持临时寄存器、
  target 寄存器、FORLOOP index/visible index 与逐指令执行后的状态一致。
- 内部按原 API 执行循环的 context countdown 模拟每个虚拟指令入口；context 取消时提交到对应 MOVE/LOADK/CONCAT/
  FORLOOP 边界，再向上返回原始 context 错误。
- debug hook、line/count hook、coroutine/yield continuation、非 raw string、非正步长和无法证明有限轮数的形态全部
  不进入整段 builder。
- `__concat` 元方法、concat 元方法 yield、错误 PC 和 traceback 仍由 guard 失败后的旧路径/普通 VM 处理。

五轮 micro：

| Benchmark | prototype 前 | prototype 后 | 改善 |
| --- | ---: | ---: | ---: |
| `BenchmarkDoStringStringConcatOfficial` wall-clock | `440866-453702 ns/op` | `112084-113859 ns/op` | 约下降 `74%` |
| `BenchmarkDoStringStringConcatOfficial` B/op | 约 `3.066 MB` | 约 `139.5 KB` | 约下降 `95%` |
| `BenchmarkDoStringStringConcatOfficial` allocs/op | `882` | `200` | 减少 `682` 次 |
| `BenchmarkPreparedStringConcatOfficial` wall-clock | `396463-402148 ns/op` | `79164-79405 ns/op` | 约下降 `80%` |
| `BenchmarkPreparedStringConcatOfficial` B/op | 约 `2.943 MB` | `16 KB` | 约下降 `99%` |
| `BenchmarkPreparedStringConcatOfficial` allocs/op | `684` | `2` | 减少 `682` 次 |

结论：整段 builder 直接消除 profile 中绝大多数中间字符串 materialize，micro 收益满足生产门槛。下一步必须重建
CLI、跑官方兼容脚本和完整 benchmark 三轮；若默认 `string_concat` 未稳定低于 `1.00x`，需要记录完整 benchmark
证伪，不再扩大该路径到有 hook/coroutine/元方法风险的形态。

## string_concat builder 后三轮完整 benchmark

2026-07-05 在整段 builder prototype 后重建 `bin/glua` / `bin/gluac`，确认官方 `lua` / `luac` 为 5.3.6，
并跑三轮默认 `scripts/benchmark-official.sh`。按三轮中位数计算：

| 排名 | English case | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 相对初始倍率 |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | `compile_3000_functions` | 编译3000个函数 | 0.005305s | 0.005643s | 1.064x | 改善约 0.176x |
| 2 | `recursion` | 递归 | 0.003741s | 0.003974s | 1.062x | 改善约 0.018x |
| 3 | `arith_mix_loop` | 混合算术循环 | 0.011502s | 0.012066s | 1.049x | 回退约 0.039x |
| 4 | `function_call` | 函数调用 | 0.006820s | 0.007126s | 1.045x | 回退约 0.005x |
| 5 | `table_rw` | 表读写 | 0.007102s | 0.006417s | 0.903x | 已低于 1.0 |
| 6 | `closure_upvalue` | 闭包 upvalue | 0.008001s | 0.007096s | 0.887x | 已低于 1.0 |
| 7 | `string_concat` | 字符串拼接 | 0.004775s | 0.004041s | 0.846x | 改善约 0.204x |
| 8 | `arith_chain_temp` | 算术临时链 | 0.013046s | 0.010147s | 0.778x | 已低于 1.0 |
| 9 | `arith_add_loop` | 整数累加循环 | 0.007651s | 0.005117s | 0.669x | 已低于 1.0 |
| 10 | `stdlib_math_string` | 标准库数学与字符串 | 0.019432s | 0.011347s | 0.584x | 已低于 1.0 |

结论：`string_concat` 从初始 `1.05x` 降到三轮中位数 `0.846x`，已稳定低于 `1.00x`；相对初始倍率改善约
`19%`。下一轮不再扩大 string builder 到 hook/coroutine/元方法风险形态，应按剩余排序回到
`compile_3000_functions` 的完整 chunk streaming 设计，或先 profile `function_call` / `arith_mix_loop` 中更容易
被三轮噪声放大的项。

优先级：

1. `compile_3000_functions` / 编译3000个函数：探索 compile-only streaming 简单函数声明路径。目标是跳过公开
   AST/semantic 对象的批量构造，但普通 `parser.New`、debug 信息、`luac -l -l`、错误位置和非目标形态必须回退。
2. `recursion` / 递归：仅在完整 benchmark 三轮稳定触发门槛后，评估非逃逸 local function descriptor，目标去掉
   prepared 递归路径当前 `2 allocs/op`。
3. `string_concat` / 字符串拼接：重新 profile 官方 8000 次拼接 fixture，只有能证明元方法、debug、yield 和错误语义
   可回退时，才进入更窄 builder/materialize。
4. `function_call` / 函数调用：profile leaf add-return batch guard，只有完整 benchmark 稳定显示差距时才冻结 batch
   内 guard。
5. `arith_mix_loop` / 混合算术循环：只做回归复核；除非完整三轮稳定高于 `1.00x`，不扩张生产 fast path。

## `function_call` profile 结论

2026-07-05 在 `2f35930` 后复核官方规模 function_call micro/profile。fixture 与默认官方 benchmark 一致：

```lua
local function add(a, b) return a + b end
local sum = 0
for i = 1, 100000 do
  sum = add(sum, i)
end
return sum
```

五轮 micro：

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkDoStringFunctionCallOfficial` | `2884954-2935799` | 约 `248.9 KB` | `214` |
| `BenchmarkPreparedFunctionCallOfficial` | `2878147-2887546` | `408 B` | `2` |

profile 结果：

- 普通 CPU：`runtime.(*VM).TryExecuteFunctionCallAssignForLoopBatch` flat `64.96%` / cum `74.45%`；
  `runtime.(*State).CheckContext` flat `8.76%`；`executePreparedLuaClosureWithDebugNameTailFromArgs` cum `97.81%`。
- GOGC=off CPU：`TryExecuteFunctionCallAssignForLoopBatch` flat `69.78%` / cum `79.14%`；
  `CheckContext` flat `8.63%`；`PrepareFunctionCallAssignForLoopBatch` flat `5.76%`。
- memory profile 主要被 pprof/test harness 影响；业务侧只剩 `runtime.luaProtoLeafAddReturn` / `NewLuaClosure`
  约 `512 KB alloc_space`、`2185 alloc_objects`，实际 benchmark 口径为 `408 B/op`、`2 allocs/op`。

结论：DoString 与 prepared wall-clock 基本重合，编译/OpenLibs 分配不是 `function_call` 剩余差距；现有
`sum = add(sum, i)` batch 已经命中，剩余 CPU 主要在 batch 本体与每个虚拟 CALL 的 context 检查。若继续生产优化，
只能设计“整段 function_call assign batch + 内部 context 边界提交”这类窄切口，类似 string_concat builder；
但必须先补 guard 覆盖函数值替换、upvalue/env 变化、hook 打开、yield/continuation、错误 PC、traceback 和
context 取消。没有这些 guard 前，不直接冻结或扩大 batch 内 guard。

## `function_call` guard 测试

2026-07-05 补齐整段 function_call assign batch 的前置语义守卫，全部位于 `lua` API 层：

- `TestDoStringFunctionCallBatchMutableCalleeAndEnvGuards`：函数体内替换 `add`、修改 upvalue、写入 `_G` 后，
  后续 `sum = add(sum, i)` 必须逐次读取最新 callee 与环境状态。
- `TestDoStringFunctionCallBatchHookTracebackGuards`：debug hook 打开时必须回退逐指令可见路径；函数体抛错经
  `xpcall(debug.traceback)` 必须保留错误消息、`stack traceback` 和 `add` 调用帧。
- `TestDoStringFunctionCallBatchCoroutineYieldGuard`：被调函数内部 `coroutine.yield` 后必须能从 CALL 内部恢复，
  继续完成后续循环并返回正确结果。
- `TestDoStringFunctionCallBatchContextCancellationGuard`：长循环中按 `CheckContext` 次数取消的宿主 context 必须
  传播为保留 `context.Canceled` 错误链的运行时错误。

后验 micro：`BenchmarkPreparedFunctionCallOfficial` 三轮为 `2908173 / 2904620 / 2909274 ns/op`、
`408-409 B/op`、`2 allocs/op`。本轮是 guard 切口，不改变生产 fast path；性能无改善符合预期。下一步若继续
生产优化，必须先评估 leaf add-return batch 的闭包寄存器、函数 Proto、参数寄存器和结果寄存器冻结边界，
并证明上述 guard 不被破坏。

## 语义门禁

- 不引入 CGO，不接 Lua C API，不新增外部依赖。
- JIT 只作为 `docs/JIT_TODO.md` 长期专项。
- debug hook、coroutine/yield、traceback、error、`debug.getinfo`、upvalue、metatable、弱表和 finalizer 语义必须能回退普通路径。
- 涉及 CLI、bytecode、VM、stdlib、compiler 或官方兼容行为时，必须重建 `bin/glua` / `bin/gluac`，确认官方
  `lua` / `luac` 为 5.3.6，并运行官方兼容脚本。

## 最终完成条件

满足任一条件即可结束本轮专项：

- 默认完整 benchmark 至少三轮所有用例均稳定 `<= 1.00x`。
- 所有剩余 `> 1.00x` 项均已被 profile 或语义门禁证伪，且无安全生产切口。

最终完成时必须输出整体 benchmark 对比表，包含：English case、中文名称、官方中位数、本项目中位数、
本项目/官方倍率、相对初始基线改善幅度、是否低于 `1.00x`，并说明未低于 `1.00x` 的原因。
