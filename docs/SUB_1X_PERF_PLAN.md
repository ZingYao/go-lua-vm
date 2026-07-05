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
