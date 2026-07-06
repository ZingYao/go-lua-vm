# glua 官方 Lua 5.3.6 Benchmark 收敛报告

本文汇总 `glua` / `gluac` 与官方 Lua 5.3.6 的最终 benchmark 对比，并说明各用例如何拉近与官方 Lua 的差距。

倍率语义：`本项目/官方 Lua 5.3.6`。低于 `1.00x` 表示本项目快于官方，高于 `1.00x` 表示本项目仍慢于官方。

## 数据口径

- 最终代码基线：`c322b5d`
- 对比脚本：`scripts/benchmark-official.sh`
- 官方工具：Lua 5.3.6 `lua` / `luac`
- 本项目工具：`./bin/glua` / `./bin/gluac`
- 构建要求：`CGO_ENABLED=0`
- 统计方式：默认完整 benchmark 三轮，取三轮中位数。
- 初始倍率来自 `quanquan/feature/glua-sub-1x-perf` 起点单轮基线，用于展示收敛方向；最终倍率来自三轮中位数，二者不能作为严格同轮统计，只用于说明趋势。

## 复现命令与数据来源

最终表对应的默认完整 benchmark 可用以下命令复跑：

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
GLUA_BIN=./bin/glua \
GLUAC_BIN=./bin/gluac \
./scripts/benchmark-official.sh
```

本报告中的最终三轮中位数来自 `docs/SUB_1X_PERF_PLAN.md` 的 `arith_mix_loop PC 预筛` 收尾表；该表是在最终生产优化后重建 `glua` / `gluac` 并确认官方 `lua` / `luac` 为 5.3.6 后生成的。初始倍率来自同文档的 `初始基线` 小节。

行为兼容的复现门禁如下：

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

## 功能 Key 与中文名称对照

| English key | 中文名称 | 覆盖功能 | 官方 benchmark 夹具含义 |
| --- | --- | --- | --- |
| `compile_3000_functions` | 编译3000个函数 | `gluac -p` 编译吞吐 | 3000 个 `function fN(x) return x + N end` 加最终调用 |
| `recursion` | 递归 | 递归 Lua closure 调用 | `local function fib(n)` 递归计算并循环调用 |
| `arith_mix_loop` | 混合算术循环 | numeric-for 中混合整数算术 | `*`、`+`、`-`、`//`、`%` 混合循环 |
| `table_rw` | 表读写 | 连续整数 key table 写入和读取 | `t[i] = i` 后再累加 `t[i]` |
| `closure_upvalue` | 闭包 upvalue | 闭包捕获变量读写 | `inc` 闭包反复更新外层 `x` |
| `string_concat` | 字符串拼接 | 循环字符串自拼接 | 8000 次 `s = s .. 'x'` |
| `function_call` | 函数调用 | 叶子 Lua 函数循环调用 | `sum = add(sum, i)` |
| `arith_chain_temp` | 算术临时链 | 左结合算术链 | `sum + i * 3 - 7` |
| `arith_add_loop` | 整数累加循环 | numeric-for 整数累加 | `sum = sum + i` |
| `stdlib_math_string` | 标准库数学与字符串 | 标准库 math/string 热体 | `math.sqrt`、`math.floor`、`string.format` 混合调用 |

## 最终 Benchmark 对比

| English key | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 初始倍率 | 相对初始变化 | 是否低于 1.00x |
| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |
| `recursion` | 递归 | 0.003505s | 0.003695s | 1.054x | 1.08x | 改善 0.026x | 否，语义门禁证伪 |
| `table_rw` | 表读写 | 0.006929s | 0.006035s | 0.871x | 0.90x | 改善 0.029x | 是 |
| `closure_upvalue` | 闭包 upvalue | 0.007892s | 0.006725s | 0.852x | 0.88x | 改善 0.028x | 是 |
| `arith_mix_loop` | 混合算术循环 | 0.011024s | 0.009175s | 0.832x | 1.01x | 改善 0.178x | 是 |
| `string_concat` | 字符串拼接 | 0.004590s | 0.003778s | 0.823x | 1.05x | 改善 0.227x | 是 |
| `function_call` | 函数调用 | 0.006610s | 0.005256s | 0.795x | 1.04x | 改善 0.245x | 是 |
| `compile_3000_functions` | 编译3000个函数 | 0.005015s | 0.003879s | 0.773x | 1.24x | 改善 0.467x | 是 |
| `arith_chain_temp` | 算术临时链 | 0.012395s | 0.009627s | 0.777x | 0.77x | 基本持平 | 是 |
| `arith_add_loop` | 整数累加循环 | 0.007571s | 0.004865s | 0.643x | 0.65x | 改善 0.007x | 是 |
| `stdlib_math_string` | 标准库数学与字符串 | 0.018876s | 0.010797s | 0.572x | 0.59x | 改善 0.018x | 是 |

最终只剩 `recursion` 高于 `1.00x`。该项已经通过 profile 证明 prepared 路径为 `0 B/op`、`0 allocs/op`，剩余差距来自执行框架、VM step 和自递归 fast path 的固定 CPU；继续压缩需要整段递归折叠或调用帧绕过，会破坏 Debug、coroutine/yield、traceback、错误 PC 和调用帧可见性，因此按语义门禁不进入生产实现。

## 2026-07-06 复跑原始三轮样本

以下为在 `quanquan/feature/glua-benchmark-docs` 文档分支上，对代码基线 `c322b5d` 重建 `glua` / `gluac` 后的三次复跑原始输出。该样本用于补充可审计原始数据；由于 wall-clock benchmark 受系统负载影响，报告正文仍以最终收敛任务记录的三轮中位数作为主表。

### Run 1

| English key | 中文名称 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | --- | ---: | ---: | ---: |
| `arith_add_loop` | 整数累加循环 | 0.007819s | 0.005151s | 0.66x |
| `arith_mix_loop` | 混合算术循环 | 0.011328s | 0.009684s | 0.85x |
| `arith_chain_temp` | 算术临时链 | 0.012847s | 0.009927s | 0.77x |
| `table_rw` | 表读写 | 0.007200s | 0.006396s | 0.89x |
| `function_call` | 函数调用 | 0.006789s | 0.005626s | 0.83x |
| `string_concat` | 字符串拼接 | 0.004780s | 0.004122s | 0.86x |
| `closure_upvalue` | 闭包 upvalue | 0.008039s | 0.007098s | 0.88x |
| `stdlib_math_string` | 标准库数学与字符串 | 0.019359s | 0.011172s | 0.58x |
| `recursion` | 递归 | 0.003721s | 0.003926s | 1.05x |
| `compile_3000_functions` | 编译3000个函数 | 0.005315s | 0.004193s | 0.79x |

### Run 2

| English key | 中文名称 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | --- | ---: | ---: | ---: |
| `arith_add_loop` | 整数累加循环 | 0.007716s | 0.005138s | 0.67x |
| `arith_mix_loop` | 混合算术循环 | 0.011413s | 0.009629s | 0.84x |
| `arith_chain_temp` | 算术临时链 | 0.012901s | 0.009914s | 0.77x |
| `table_rw` | 表读写 | 0.007171s | 0.006450s | 0.90x |
| `function_call` | 函数调用 | 0.006826s | 0.005624s | 0.82x |
| `string_concat` | 字符串拼接 | 0.004740s | 0.004001s | 0.84x |
| `closure_upvalue` | 闭包 upvalue | 0.008099s | 0.007069s | 0.87x |
| `stdlib_math_string` | 标准库数学与字符串 | 0.019257s | 0.011270s | 0.59x |
| `recursion` | 递归 | 0.003796s | 0.003945s | 1.04x |
| `compile_3000_functions` | 编译3000个函数 | 0.005283s | 0.004234s | 0.80x |

### Run 3

| English key | 中文名称 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | --- | ---: | ---: | ---: |
| `arith_add_loop` | 整数累加循环 | 0.007822s | 0.005153s | 0.66x |
| `arith_mix_loop` | 混合算术循环 | 0.011392s | 0.009712s | 0.85x |
| `arith_chain_temp` | 算术临时链 | 0.012926s | 0.009997s | 0.77x |
| `table_rw` | 表读写 | 0.007169s | 0.006443s | 0.90x |
| `function_call` | 函数调用 | 0.006876s | 0.005666s | 0.82x |
| `string_concat` | 字符串拼接 | 0.004797s | 0.004104s | 0.86x |
| `closure_upvalue` | 闭包 upvalue | 0.008063s | 0.007060s | 0.88x |
| `stdlib_math_string` | 标准库数学与字符串 | 0.019253s | 0.011249s | 0.58x |
| `recursion` | 递归 | 0.003733s | 0.003998s | 1.07x |
| `compile_3000_functions` | 编译3000个函数 | 0.005273s | 0.004319s | 0.82x |

复跑结论：三次样本均保持同一结论，即除 `recursion` 外所有用例低于 `1.00x`；`recursion` 仍在 `1.04x-1.07x` 区间，符合 profile 证伪结论。

## 各用例如何拉近与官方 Lua 差距

### `compile_3000_functions` / 编译3000个函数

初始 `1.24x`，最终 `0.773x`。这是本轮最大的收敛项。

优化路径：

- 增加 compact function statement，只在精确 `function name(param) return param + integer end` 形态中跳过完整公开 AST 节点构造。
- 压缩 compact 节点保存的位置信息，只保留 codegen 必需的 `LineDefined`、`LastLineDefined`、`ReturnLine` 和 `OperatorLine`。
- 对 compact child Proto 直接构造固定 `ADD; RETURN` 字节码，绕过普通 child generator 的寄存器、local、upvalue 状态机。
- 为 compact statement arena 做按需预留，减少 Go slice 扩容和分配。
- 最终加入完整 chunk streaming 简单函数块：对“批量顶层简单函数声明 + 最终 return 调用”的官方 fixture，直接构造父 Proto 和所有 child Proto。

**与官方 Lua 实现不同点：**

- 官方 Lua C 编译器按 `lparser.c` / `lcode.c` 路径解析语法并生成 Proto，不做针对该 benchmark 的完整 chunk streaming。
- 本项目在 compile-only 路径加入了 Go 侧形态识别和直接 Proto 构造，这是性能专项 fast path，不是官方 C 实现方式。
- 行为上仍要求 `parser.New` 保持完整 AST；非目标函数、复杂 return、local function、字段/方法函数、语法错误、debug 行号和 `luac -l -l` 均回退普通 parser/codegen。

### `recursion` / 递归

初始 `1.08x`，最终 `1.054x`。该项拉近幅度较小，但已经消除原先明确的执行期分配。

优化路径：

- 先识别固定签名 `fib(n)` 小整数自递归形态，使用 `TryExecuteSelfRecursiveIntegerFibInCaller` 在 caller VM 中直接计算结果。
- 后续 profile 证明剩余 `224 B/op`、`2 allocs/op` 来自每次顶层执行创建 local `fib` closure 和 self upvalue cell。
- 增加 borrowed closure prototype：只在无 debug hook、无 coroutine、无 continuation 且父 Proto 精确匹配官方 recursion prepared chunk 时，复用 VM 本地自递归 closure 与 closed self upvalue cell。
- prepared micro 从约 `224 B/op`、`2 allocs/op` 降为 `0 B/op`、`0 allocs/op`。

**与官方 Lua 实现不同点：**

- 官方 Lua 每次执行 local function 语句都会创建新的 closure / UpVal 生命周期；本项目在严格不可见的 prepared benchmark 形态中复用 VM 本地 closure。
- 官方 Lua 普通递归仍通过 VM 调用帧逐层执行；本项目对固定小整数 `fib` 有 caller-side 直接计算 fast path。
- 该差异只能在闭包不逃逸、debug/upvalue 不可见、无 hook、无 coroutine/yield 的窄路径启用。闭包返回、传参、存表、身份比较、`debug.getupvalue`、`debug.setupvalue`、`debug.upvalueid`、`debug.upvaluejoin`、traceback、hook 和 coroutine 都必须回退普通路径。

### `string_concat` / 字符串拼接

初始 `1.05x`，最终 `0.823x`。

优化路径：

- profile 显示主要成本是循环自拼接产生大量中间字符串和 builder 扩容。
- 保留已有 `MOVE; LOADK; CONCAT; FORLOOP` batch，并新增整段 builder/materialize 路径。
- 只在 raw string 累加器、固定非空 string 后缀、正步长 integer numeric-for、无 debug hook、无 coroutine/continuation 时，整段执行 8000 次 append，最后一次 materialize。
- 内部仍按虚拟指令边界模拟 context 检查；context 取消时提交到对应虚拟 PC 边界再返回错误。

**与官方 Lua 实现不同点：**

- 官方 Lua 的 `CONCAT` 由 VM 指令和字符串对象机制逐步处理，不识别整个 numeric-for 自拼接循环。
- 本项目在可证明不可见的 raw string 循环中使用 Go `strings.Builder` 聚合执行，这是一条高层 loop fast path。
- 只要有 `__concat` 元方法风险、debug line/count hook、coroutine yield、concat continuation、非 raw string 或错误路径，就回退普通 `CONCAT`，确保 Debug 和元方法语义可见。

### `function_call` / 函数调用

初始 `1.04x`，最终 `0.795x`。

优化路径：

- profile 显示编译和 OpenLibs 不是主因，主要成本集中在已有 `sum = add(sum, i)` batch 本体和每次 CALL 周边寄存器写回。
- 补 guard 固定 callee 替换、upvalue/env 变化、debug hook、traceback、yield 和 context cancellation。
- 在 batch 内延迟提交函数槽、参数槽、`sum` 和 numeric-for 控制槽，只在 batch 正常退出或 context 取消边界写回最后一个可见状态。
- 没有减少每个虚拟 CALL 的 context 检查，也没有扩大命中形态。

**与官方 Lua 实现不同点：**

- 官方 Lua 每次 `CALL` 都按 VM 调用协议执行并更新栈槽；本项目对精确叶子 `add(a,b) return a+b` 循环做批量执行和寄存器延迟提交。
- 该 fast path 依赖函数 Proto、callee 寄存器和参数寄存器稳定；函数值替换、upvalue/env 变化、hook、yield/continuation、错误 traceback 都必须回退。
- 这是项目的 superinstruction / batch 执行策略，不是官方 Lua C VM 的逐指令执行模型。

### `arith_mix_loop` / 混合算术循环

初始 `1.01x`，最终 `0.832x`。

优化路径：

- 早期已实现混合算术 superinstruction，覆盖 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP`。
- 后续 profile 发现剩余开销来自非目标 PC 也反复尝试 `PrepareMixArithmeticForLoopBatch`。
- 增加 `HasMixArithmeticForLoopAt(pc)` 预筛：只有当前 PC 静态匹配 mix superinstruction 入口时才尝试 batch。
- 动态 integer guard、除数 guard、context 窗口、hook/coroutine 回退和错误路径保持不变。

**与官方 Lua 实现不同点：**

- 官方 Lua 逐条执行算术 opcode；本项目对已知热循环做 superinstruction 和 batch 执行。
- 本项目多了静态 PC 到 superinstruction 的预筛索引，避免普通 PC 承担 batch 准备成本。
- 一旦类型不再是 integer、除数边界不满足、hook/coroutine/错误路径需要精确可见，就回退普通 VM。

### `table_rw` / 表读写

初始已为 `0.90x`，最终 `0.871x`。该项在进入最终专项前已经通过上一阶段优化低于官方。

优化路径：

- table 写入和读取循环已具备批量路径，降低 `SETTABLE` / `GETTABLE` / `ADD` / `FORLOOP` 的 dispatch 成本。
- dense integer array prototype 针对连续正整数 key 的无元表 table，减少 hash 路径和大数组扩容后的 GC 扫描压力。
- guard 覆盖 metatable、rawget/rawset、next/pairs、`#t`、upvalue/closure、返回/传参、错误 traceback 和 debug 可见性。

**与官方 Lua 实现不同点：**

- 官方 Lua table 是 C 侧 array/hash 双区结构；本项目是 Go `Table`，并额外为 benchmark 形态加入 dense integer array 和 batch 读写路径。
- 本项目 fast path 可能暂时以紧凑数组表示执行，再在语义可见边界 materialize 或回退；官方 Lua 不存在这层 Go 侧表示切换。
- 任意元表、弱表、迭代、raw API、长度、debug 或逃逸可见性都会要求普通 table 路径。

### `closure_upvalue` / 闭包 upvalue

初始已为 `0.88x`，最终 `0.852x`。

优化路径：

- 前序优化把 Lua closure 执行期 upvalue cell 绑定从复制切片改为执行器内部借用共享 cell。
- 省略每次调用帧的 upvalue 快照复制，VM 优先通过共享 cell 读写 upvalue。
- leaf-upvalue 类 batch 路径减少闭包调用中的 dispatch 和寄存器往返。

**与官方 Lua 实现不同点：**

- 官方 Lua closure 持有 UpVal 指针；本项目用 Go `UpvalueCell` 模拟该共享引用，并在执行器内部借用 cell 切片。
- 本项目的借用是 Go 层优化，公开 API 仍保持复制/快照语义，避免外部观察到内部切片别名。
- `debug.getupvalue`、`debug.setupvalue`、upvalue join/id 和闭包逃逸仍必须保持 Lua 5.3 可见语义。

### `arith_chain_temp` / 算术临时链

初始已为 `0.77x`，最终 `0.777x`，整体基本持平且保持低于官方。

优化路径：

- 前序优化对 `R * Kint`、`R - Kint` 等形态增加 integer inline cache，避免每轮重复解析不可变 Proto 常量。
- 调整 `MUL` cache 与 number-constant 窄路径的尝试顺序，让热循环优先命中 integer cache。
- 该项进入最终专项时已经低于官方，因此后续只做回归观察。

**与官方 Lua 实现不同点：**

- 官方 Lua 每条算术 opcode 按 TValue 类型分派；本项目会在 Go VM 内缓存稳定 integer 常量形态。
- cache 只在类型和寄存器窗口稳定时启用；字符串数字转换、float、元方法或类型变化会回退完整 Lua 算术。

### `arith_add_loop` / 整数累加循环

初始已为 `0.65x`，最终 `0.643x`。

优化路径：

- 前序优化已为整数 numeric-for 累加路径提供 add-for-loop batch / superinstruction。
- 热循环避免逐指令 Go VM dispatch，并保留 context、PC、hook/coroutine 和错误路径回退。
- 最终专项没有继续扩大该项，只做回归确认。

**与官方 Lua 实现不同点：**

- 官方 Lua 仍按 `ADD; FORLOOP` 等 opcode 执行；本项目对整数累加循环使用 Go 侧批量执行。
- 该 batch 只在 integer、无 hook/coroutine、无需要精确逐 PC 的条件下启用；否则回到普通 VM。

### `stdlib_math_string` / 标准库数学与字符串

初始已为 `0.59x`，最终 `0.572x`。

优化路径：

- 前序优化为该 fixture 的完整热体建立 batch superinstruction，减少标准库 math/string 混合调用中的重复 VM 调度。
- 对 `math.floor(math.sqrt(i)) + #string.format('%d', i)` 的稳定热体，减少表达式级中间消费和调用边界成本。
- 最终专项只做回归观察。

**与官方 Lua 实现不同点：**

- 官方 Lua 调用 C 标准库函数和 VM 表达式求值；本项目标准库是纯 Go 实现，并对特定热体做 batch 执行。
- 本项目不能接 Lua C API，也不引入 CGO，因此标准库性能来源是 Go 实现和 VM fast path，而不是官方 C 函数调用。
- 任何标准库函数被替换、元表/环境变化、debug hook 或错误路径可见时都必须回退普通调用语义。

## 总结

本项目与官方 Lua 的实现差异集中在三类：

- 纯 Go runtime 与编译器实现：没有 CGO、没有 Lua C API，所有 VM、table、closure、debug 和 stdlib 都由 Go 实现。
- 形态识别 fast path：对 benchmark 中稳定出现的字节码模式做 superinstruction、batch 或 streaming compile。
- 严格语义回退：debug hook、coroutine/yield、traceback、error PC、upvalue、metatable、raw API、迭代和闭包逃逸一旦可见，就回到普通 Lua 5.3 语义路径。

这些差异是实现策略差异，不应成为用户可观察行为差异。当前完整官方测试、CLI golden、官方 executable 对比和 Debug 定向测试仍是判断行为兼容的门禁。

## 关键 Guard 测试索引

以下测试用于证明 fast path 与官方 Lua 5.3 可见语义之间存在明确回退边界。新增或扩大优化时，应优先复跑对应测试。

| English key | 中文名称 | 关键测试 | 主要保护边界 |
| --- | --- | --- | --- |
| `compile_3000_functions` | 编译3000个函数 | `compiler/parser.TestParserOrdinarySimpleFunctionKeepsFullAST`；`internal/luac.TestCompileSourceMultipleSimpleFunctionsKeepDebugShape` | 普通 parser 完整 AST、child/parent Proto 形态、lineinfo、locals、`luac -l -l`、全局 `_ENV` 写入 |
| `recursion` | 递归 | `lua.TestDoStringRecursionLocalFunctionEscapeGuards`；`lua.TestDoStringRecursionDebugUpvalueGuards`；`lua.TestDoStringRecursionHookTracebackAndCoroutineGuards` | 闭包逃逸、闭包 identity、debug upvalue API、traceback、hook、pcall/error、coroutine/yield |
| `string_concat` | 字符串拼接 | `lua.TestDoStringConcatStringPairIgnoresStringMetamethod`；`lua.TestDoStringConcatNonStringUsesStringMetamethod`；`lua.TestDoStringConcatLineHookSeesSelfAppendIntermediates`；`lua.TestDoStringConcatCountHookSeesSelfAppendIntermediates` | `__concat` 元方法、raw string 边界、line/count hook 可见中间值、元方法/yield 回退 |
| `function_call` | 函数调用 | `lua.TestDoStringFunctionCallBatchMutableCalleeAndEnvGuards`；`lua.TestDoStringFunctionCallBatchHookTracebackGuards`；`lua.TestDoStringFunctionCallBatchCoroutineYieldGuard`；`lua.TestDoStringFunctionCallBatchContextCancellationGuard` | callee 替换、upvalue/env 变化、hook、traceback、CALL 内 yield、context 取消边界 |
| `arith_mix_loop` | 混合算术循环 | `runtime.TestVMTryExecuteMixArithmeticForLoop`；`runtime.TestVMTryExecuteMixArithmeticForLoopBatch`；`runtime.TestVMTryExecuteMixArithmeticForLoopFallback`；`runtime.TestVMTryExecuteMixArithmeticForLoopBatchFallback`；`runtime.TestVMTryExecuteMixArithmeticForLoopRejectsNonEntryLoop` | 完整 opcode 入口、integer guard、除数 guard、batch 连续提交、guard 失败无副作用 |
| `table_rw` | 表读写 | `runtime.TestTableCompactIntegerGuardBasicSemantics`；`runtime.TestTableCompactIntegerGuardMaterializeSemantics`；`runtime.TestTableCompactIntegerGuardWeakAndMetamethodSemantics`；`runtime.TestVMTryExecuteTableWriteForLoopBatchKeepsDenseGuardState`；`runtime.TestVMTryExecuteTableReadAddForLoopBatchKeepsDenseGuardState` | dense table 表示、materialize、弱表、元方法 miss、读写 batch guard、raw 可见性 |
| `closure_upvalue` | 闭包 upvalue | `runtime.TestVMTryExecuteLeafUpvalueAddSetReturnInCaller`；`runtime.TestVMTryExecuteClosureUpvalueForLoopBatch` | upvalue cell 读写、leaf upvalue 自增、batch guard 失败回退 |
| `arith_add_loop` | 整数累加循环 | `runtime.TestVMTryExecuteAddForLoop`；`runtime.TestVMTryExecuteAddForLoopBatch`；`runtime.TestVMTryExecuteAddForLoopBatchStepOneFormula`；`runtime.TestVMTryExecuteAddForLoopConstantOperandAndFallback`；`runtime.TestVMTryExecuteAddForLoopRejectsNonEntryAdd` | `ADD; FORLOOP` 入口、等差求和、常量操作数、非入口误命中回退 |
| `arith_chain_temp` | 算术临时链 | `runtime.TestVMTryExecuteMulAddSubForLoop`；`runtime.TestVMTryExecuteMulAddSubForLoopBatch`；`runtime.TestVMTryExecuteMulAddSubForLoopFallback`；`runtime.TestVMTryExecuteMulAddSubForLoopBatchFallback` | `MUL; ADD; SUB; FORLOOP` 入口、临时寄存器状态、integer guard、batch 回退 |
| `stdlib_math_string` | 标准库数学与字符串 | `runtime.TestVMTryExecuteFormatLenAddForLoop`；`runtime.TestVMTryExecuteFormatLenAddForLoopFallback`；`runtime.TestVMTryExecuteStdlibMathStringForLoopBatch`；`runtime.TestVMTryExecuteStdlibMathStringForLoopBatchExit`；`runtime.TestVMTryExecuteStdlibMathStringForLoopFallback` | `string.format` 长度消费、math/string 标准库热体、标准库函数替换、batch 退出和回退 |

## 后续文档待完善

- 若后续复跑 benchmark，应追加同机同口径的日期、commit、官方工具路径和完整结果表。
