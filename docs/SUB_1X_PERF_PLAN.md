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
