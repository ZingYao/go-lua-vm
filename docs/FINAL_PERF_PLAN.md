# glua 最终性能收敛方案

## 目标与当前基线

本文记录 `quanquan/feature/glua-final-perf` 分支的最终性能收敛方案。分支基于 `de71f7c`
创建，前序 `quanquan/feature/glua-next-perf` 已合入 `master`。

倍率语义：`本项目/官方 Lua 5.3.6`，低于 `1.00x` 表示本项目快于官方，高于 `1.00x` 表示仍慢于官方。
最新可用基线来自 `docs/NEXT_PERF_TODO.md` 中 `f5f9028` 后的三轮完整 benchmark：

| 用例 | 三轮倍率 | 平均 | 状态 |
| --- | ---: | ---: | --- |
| `stdlib_math_string` | `0.58x / 0.58x / 0.58x` | `0.58x` | 已快于官方，停止扩张 |
| `arith_add_loop` | `0.66x / 0.66x / 0.67x` | `0.66x` | 已快于官方，停止扩张 |
| `arith_chain_temp` | `0.76x / 0.77x / 0.78x` | `0.77x` | 已快于官方，停止扩张 |
| `table_rw` | `0.89x / 0.88x / 0.87x` | `0.88x` | 已快于官方，停止扩张 |
| `closure_upvalue` | `0.89x / 0.89x / 0.88x` | `0.89x` | 已快于官方，停止扩张 |
| `arith_mix_loop` | `1.03x / 1.02x / 1.03x` | `1.03x` | 噪声带，仅回归复核 |
| `string_concat` | `1.03x / 1.03x / 1.05x` | `1.04x` | 噪声带，仅回归复核 |
| `function_call` | `1.04x / 1.05x / 1.06x` | `1.05x` | 噪声带，需 profile 证明 |
| `recursion` | `1.09x / 1.07x / 1.08x` | `1.08x` | 噪声带，需 profile 证明 |
| `compile_3000_functions` | `1.33x / 1.33x / 1.32x` | `1.33x` | 唯一清晰主项 |

2026-07-05 在 `quanquan/feature/glua-final-perf` 起点重建 `bin/glua` / `bin/gluac`，并显式使用
`/Users/zing/.local/lua/5.3.6/bin/lua` 与 `/Users/zing/.local/lua/5.3.6/bin/luac` 复跑默认完整
benchmark 三轮后，排序仍保持一致：

| 排名 | 用例 | 三轮倍率 | 平均 | 判断 |
| ---: | --- | ---: | ---: | --- |
| 1 | `compile_3000_functions` | `1.30x / 1.32x / 1.31x` | `1.31x` | 唯一清晰主项 |
| 2 | `recursion` | `1.05x / 1.04x / 1.04x` | `1.04x` | 噪声带，未达进入门槛 |
| 3 | `function_call` | `1.03x / 1.01x / 1.02x` | `1.02x` | 噪声带，未达进入门槛 |
| 4 | `arith_mix_loop` | `1.03x / 1.01x / 1.02x` | `1.02x` | 噪声带，仅回归复核 |
| 5 | `string_concat` | `1.00x / 1.03x / 1.01x` | `1.01x` | 噪声带，仅回归复核 |
| 6 | `table_rw` | `0.88x / 0.87x / 0.85x` | `0.87x` | 已快于官方 |
| 7 | `closure_upvalue` | `0.85x / 0.85x / 0.85x` | `0.85x` | 已快于官方 |
| 8 | `arith_chain_temp` | `0.77x / 0.76x / 0.78x` | `0.77x` | 已快于官方 |
| 9 | `arith_add_loop` | `0.65x / 0.68x / 0.63x` | `0.65x` | 已快于官方 |
| 10 | `stdlib_math_string` | `0.57x / 0.57x / 0.57x` | `0.57x` | 已快于官方 |

因此最终分支第一阶段仍只允许继续推进 `compile_3000_functions` profile 和紧凑函数体设计；
`function_call` 与 `recursion` 未达到 TODO 中的进入门槛，`string_concat` 和 `arith_mix_loop` 不重新打开。

## 总体策略

最终阶段不再堆局部字段、容量 hint 或 token 分派微调。每个生产提交只允许一个可验证切口，并且必须先用
Go micro/profile 证明剩余差距属于该切口。

优先级如下：

1. `compile_3000_functions`：主攻简单函数体的紧凑编译结构，目标是减少 parser AST 构造或 codegen
   arena 生命周期成本。
2. `function_call`：只有 profile 再次证明 `sum = add(sum, i)` batch 内 guard 是稳定主因时，才评估
   batch guard 冻结。
3. `recursion`：只有能证明 local `fib` closure/upvalue 不逃逸且不可被 debug 观察时，才评估闭包生命周期
   复用或 direct descriptor。
4. `string_concat`、`arith_mix_loop`：当前已进入噪声带，不继续生产扩张，只做回归复核。

禁止事项：

- 不引入 CGO、不接 Lua C API、不新增外部依赖。
- 不实现 JIT；JIT 仍只作为 `docs/JIT_TODO.md` 长期专项。
- 不重复以下已完成或已证伪方向：常量索引预留、函数语句 arena、顶层语句容量、expression arena 大页、
  operator 扫描、标识符扫描、Source ASCII 读取、字符串/注释 guard、token 大类分派、parser 语句分派、
  codegen-only 简单函数直发、string concat builder、arith mix batch、table dense fast path。
- 不直接替换全 parser，不扩大到通用 `Block.Statements` union，不在没有 profile 证据时扩展 fast path。

## 方案 A：`compile_3000_functions` 简单函数体紧凑编译

目标源码形态：

```lua
function fN(x) return x + Kinteger end
```

激进方向：parser 在完整识别目标函数体后，记录私有 compact summary，避免或延后构造
`ReturnStatement`、`BinaryExpression`、`NameExpression`、`LiteralExpression` 等重复 AST 节点；codegen
只在 summary 与普通语义逐项一致时发出子 `Proto`。这次不能只做 codegen 直发，因为前序试验证明
codegen-only 不降低 wall-clock，且 B/op 反而上升。

必须保持一致的语义面：

- `LineDefined`、`LastLineDefined`、`Position`、行号表、局部变量生命周期、`luac -l -l` 输出。
- 参数限制、数字字面量错误、语法错误位置和 traceback 文案。
- 普通 AST 调用方仍能在非目标形态上看到完整 `FunctionStatement.Body`。

必须回退的形态：

- table/method 函数名、vararg、多参数、local/upvalue、嵌套函数、label/goto、尾调用。
- 复杂表达式、调用、字段访问、索引访问、拼接、幂运算、非 integer 常量、扩展语法。
- debug hook、`debug.getinfo` 或 luac/debug 信息无法证明完全一致的场景。

验收门槛：

- `BenchmarkCompileSource3000Functions` 五轮 wall-clock 稳定下降至少 `5%`。
- B/op 不高于当前约 `3.78 MB/op` 的噪声范围；allocs/op 下降但 wall-clock 不降不能接受。
- 完整 benchmark `compile_3000_functions` 稳定低于当前约 `1.33x`，目标小于 `1.15x`。
- 如果只改变 profile 采样或只降低 allocs/op，回退生产改动，只保留证伪记录。

### 2026-07-05 结构设计

当前 parser/codegen 结构已经证明两个事实：

- 简单 `function fN(x) return x + K end` 会走 `parseFunctionStatement -> parseFunctionBodyInto ->
  parseBlockUntilInto -> parseReturnStatementInto -> parseExpression`，因此仍会构造 `Block`、`ReturnStatement`、
  `NameExpression`、`LiteralExpression` 和 `BinaryExpression`。
- `Source` 已有 `Mark` / `Reset`，但 `Lexer` 和 `Parser` 还没有 token 级 mark/reset；如果没有回滚能力，
  “试探紧凑解析失败后回退普通 parser”会破坏复杂函数体的错误位置和 AST。

因此首个生产 prototype 应按三层实现，而不是直接在 `parseFunctionBodyInto` 中硬扫 token：

1. parser 增加私有 token mark/reset helper，快照包含 `parser.current` 和底层 lexer/source 位置。该 helper
   只服务局部语法试探，不能暴露为通用 public API。
2. parser 增加编译专用 compact 模式。`parser.New` / `parser.NewWithSyntax` 默认保持完整 AST；
   `internal/luac.CompileSourceWithSyntax` 可使用新入口启用 compact summary。这样独立 parser 用户仍拿到完整
   AST，不会因为 benchmark fast path 看到空 `Block.Return`。
3. `FunctionBody` 保存 parser-private compact summary，并通过只读方法向 codegen 暴露最小字段。summary 建议包含：
   参数名、整数常量、return 位置、操作符位置、字面量位置、函数体起止行，以及是否为 `param + Kinteger`
   的精确形态。summary 存储应使用 parser 页式 arena，避免为 3000 个函数引入逐对象分配。

试探解析流程：

- 只在单参数、非 vararg、函数体第一个 token 是 `return` 时进入试探。
- 试探成功的 token 序列必须精确为 `return <param> + <integer> end`，其中 `<param>` 必须等于唯一参数名。
- 试探成功后，函数体 `Body` 可保留最小 block/scope 信息供 semantic 参数局部变量分析使用；codegen 通过 summary
  直接生成子 `Proto`。
- 试探失败必须 reset 到进入试探前，再调用普通 `parseBlockUntilInto`，确保非目标函数体的 AST、错误位置和语义不变。

codegen 直发流程：

- `compileChildProto` 在定义参数 local 后，如果 summary 存在且形态仍满足约束，直接发出 `ADD; RETURN`。
- `ADD` 使用操作符位置作为 lineinfo，`RETURN` 使用 return 位置；`LineDefined`、`LastLineDefined` 仍来自
  `FunctionBody`。
- 参数 `x` 的 local 生命周期必须覆盖 `[0, len(code))`；`MaxStackSize`、常量表、行号表和 `luac -l -l`
  输出必须与普通路径一致。
- summary 缺失、参数不匹配、常量无法 RK 编码或 debug 信息无法对齐时，必须回退普通 `compileBlock`。

该设计的首个可实现切口是“parser mark/reset + compile-only compact summary + codegen direct child proto”。
它必须证明同时减少 AST 构造和 codegen arena 分配；如果只复现前序 codegen-only 直发，必须回退。

### 2026-07-05 prototype 1 结果

本轮已实现 compile-only compact summary：`internal/luac` 使用 `parser.NewCompactWithSyntax`，普通
`parser.New` / `parser.NewWithSyntax` 继续输出完整 AST。compact 只覆盖普通简单函数声明中的
`return <param> + <integer> end`，并通过 parser mark/reset 保证失败后回退普通 parser；method、
table field、local function、匿名 function、复杂表达式、label/goto 和语法错误位置已由 guard 测试固定。

Go micro 五轮从约 `2.65-2.71 ms/op`、`3.781 MB/op`、`87 allocs/op` 降到
`2.35 ms/op`、`3.50 MB/op`、`64-65 allocs/op`。重建 CLI 后默认完整 benchmark 三轮中
`compile_3000_functions` 为 `1.23x / 1.20x / 1.20x`，低于最终分支基线约 `1.31x`，
但仍未达到长期目标 `1.15x`。下一步必须重新 profile prototype 后剩余成本，再决定是否继续扩张
parser/codegen 结构性优化。

### 2026-07-05 prototype 1 后续 profile

在 `5008d65` 后补跑 `BenchmarkCompileSource3000Functions` profile，单轮结果为
`2.360645 ms/op`、`3,497,402 B/op`、`63 allocs/op`，与 prototype 1 五轮区间一致。CPU profile
仍主要受 Go runtime/GC/调度采样影响，源码侧累计项较分散：parser 约 `7.47%`，codegen 约
`2.93%`，`tryParseCompactSimpleFunctionBody` 约 `3.81%`。

memory profile 显示剩余编译器分配已经集中到三块：

- `newFunctionStatement`：`alloc_space` 约 `42.27%`，说明目标函数虽然跳过函数体表达式 AST，
  仍保留完整顶层 `FunctionStatement` 结构。
- `prepareDirectFunctionBlockCapacity`：`alloc_space` 约 `28.80%`，来自顶层子 Proto、Code/LineInfo、
  常量表和直接子函数 arena 的确定性容量。
- `newCompactSimpleFunctionBody`：`alloc_space` 约 `9.57%`，来自 compact summary 页式 arena。

结论：prototype 1 后没有出现新的 lexer/token/Source 低风险单点热点；继续降低
`compile_3000_functions` 需要先设计“顶层简单 function 声明紧凑表示”或“直接函数声明 codegen
容量生命周期”这样的结构性切口。单纯调整 compact summary 页大小、容量 hint 或局部扫描，不满足
本阶段的性能门槛和语义回退标准，不进入生产改动。

### 2026-07-05 prototype 2 设计：顶层简单函数语句紧凑表示

profile 指向 `newFunctionStatement` 后，下一候选不能再扩张函数体 summary，而是把“普通顶层
`function fN(x) return x + K end` 语句”整体压成编译专用紧凑语句。该方向只允许服务
`internal/luac.CompileSourceWithSyntax` 的 compact parser；`parser.New` / `parser.NewWithSyntax`
必须继续返回完整 `*FunctionStatement`，避免库调用方观察到私有 fast path。

允许形态必须同时满足：

- 函数名是单段普通 identifier，不能是 table field、method、local function 或匿名 function。
- 函数体已经命中 prototype 1 的 `return <param> + <integer>` summary。
- 所在 block 为当前直接语句列表，且语句自身不携带 label/goto、扩展语法、尾随额外语句或语法错误恢复状态。
- 语句位置、函数名、函数体行号和 debug metadata 足以复原普通路径的 `CLOSURE; SETGLOBAL` 与子 Proto。

拟定结构：

1. 新增 parser-private `compactSimpleFunctionStatement`，字段只包含函数名、函数体指针、function
   位置和名称位置。该结构可由独立页式 arena 保存，避免改变公开 `FunctionStatement` 布局。
2. 新增未导出的 statement 类型实现 `Statement`，只在 compact parser 模式且目标形态完全命中时写入
   `Block.Statements`；普通 parser 入口永不生成该类型。
3. semantic analyzer 必须识别该私有语句，并执行与普通 `FunctionStatement` 相同的
   `analyzeFunctionBody`，不得向父作用域新增 local 或 label。
4. codegen 的 `directFunctionBlockStatsFor` 与 `compileStatement` 必须按普通全局 function 语义处理：
   追加子 Proto，发出 `CLOSURE; SETGLOBAL`，函数名写入当前常量表，子函数继续复用 compact body
   直发 `ADD; RETURN`。

必须先补 guard，禁止直接实现生产改动：

- parser 入口 guard：`parser.New` 仍返回 `*FunctionStatement`，compact 入口才允许私有语句。
- semantic guard：嵌套函数、goto/label、扩展语法和错误路径仍执行普通语义校验。
- codegen/luac guard：目标源码的 Proto、常量表、lineinfo、locals、`luac -l -l` 输出与 prototype 1
  完全一致。
- 回退 guard：table field、method、local function、匿名 function、复杂函数体和语法错误位置不能进入
  紧凑语句。

验收门槛：只有在 `BenchmarkCompileSource3000Functions` 五轮继续稳定降低 wall-clock 至少 `5%`、
B/op 不高于当前约 `3.50 MB/op`，并且完整 benchmark `compile_3000_functions` 继续低于 `1.20x`
时，才能保留生产改动；否则回退实现，只保留设计和证伪记录。

## 方案 B：`function_call` batch guard 冻结

目标源码形态：

```lua
local function add(a, b)
  return a + b
end
local sum = 0
for i = 1, 200000 do
  sum = add(sum, i)
end
return sum
```

当前 `TryExecuteFunctionCallAssignForLoopBatch` 已是主路径，普通 `VM.Step` 和 `executeCall` 占比很低。
后续只能评估在 batch 窗口内冻结闭包寄存器、函数 Proto、leaf add-return descriptor、参数/结果寄存器
guard，减少每个窗口的重复动态验证。

必须保持一致的语义面：

- closure identity、upvalue、环境表、metatable、debug hook、yield/continuation、错误 PC 和 traceback。
- context 取消检查延迟不得无界扩大。
- 任一寄存器别名、函数值变化或 hook/debug 可见性打开时回退普通 batch 或普通 VM。

验收门槛：

- `BenchmarkPreparedFunctionCallOfficial` 稳定改善至少 `8%`。
- 完整 benchmark `function_call` 稳定低于 `1.00x`。
- 非目标 function/closure/recursion prepared 矩阵无稳定退化。

## 方案 C：`recursion` local closure/upvalue 生命周期优化

目标源码形态：

```lua
local function fib(n)
  if n < 2 then return n end
  return fib(n - 1) + fib(n - 2)
end
local sum = 0
for i = 1, 16 do
  sum = sum + fib(15)
end
return sum
```

当前自递归整数 fast path 本体已经很小，剩余 `2 allocs/op` 来自每次执行顶层 chunk 时创建 local `fib`
closure 和自递归 upvalue cell。激进方向只能是“非逃逸 local function descriptor”：在 prepared 顶层闭包
重复执行时证明该 closure 不返回、不传参、不存表、不被 debug/upvalue API 观察，再跳过普通 closure/upvalue
分配或复用等价结构。

必须保持一致的语义面：

- local function 每次执行创建新闭包的 Lua 5.3 可观察语义。
- `debug.getupvalue`、闭包身份比较、返回/传参/存表逃逸、hook、traceback、pcall/error、coroutine/yield。
- 任何逃逸、debug 可见性或错误路径不确定时必须回退普通 closure/upvalue 生命周期。

验收门槛：

- `BenchmarkPreparedRecursion` 去掉当前 `2 allocs/op`，且 wall-clock 稳定改善。
- 完整 benchmark `recursion` 稳定低于 `1.00x`。
- debug/upvalue/closure identity 定向测试全绿。

## 方案 D：噪声带项目只做回归

`string_concat` 和 `arith_mix_loop` 已完成主要收益切口，当前倍率分别约 `1.04x` 和 `1.03x`。这两项后续
只允许在完整三轮 benchmark 显示稳定高于 `1.08x`，且 profile 指向新的单一主因时重新打开。

## 全局正确性门禁

涉及 Go 代码的每个生产提交必须执行：

```bash
gopls check <changed-go-files>
gofmt -w <changed-go-files>
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

涉及 CLI、bytecode、VM、stdlib 或官方兼容行为时还必须执行：

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
./scripts/compare-cli-golden.sh
./scripts/compare-official-executables.sh
./scripts/run-official-tests.sh
```

提交前必须确认官方 `lua` / `luac` 为 5.3.6，并记录 benchmark 变化、语义依据和 commit hash。
