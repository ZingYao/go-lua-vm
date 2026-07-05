# glua 最终性能收敛 TODO

## 分支与目标

- 目标分支：`quanquan/feature/glua-final-perf`
- 起点 commit：`de71f7c`
- 主目标：优先把 `compile_3000_functions` 从当前约 `1.33x` 压到稳定低于 `1.15x`。
- 次目标：只有 profile 证明存在明确主因时，才处理 `function_call` 和 `recursion` 的噪声带剩余项。
- 停止线：任一方案无法证明 Lua 5.3 语义等价，或 benchmark 未达到门槛，必须回退生产改动并记录证伪。

## 0. 最终分支基线

- [x] 重建 `bin/glua` 和 `bin/gluac`。
- [x] 确认官方 `lua` / `luac` 版本为 5.3.6。
- [x] 运行默认完整 benchmark 三轮，记录 `本项目/官方` 倍率并按倍率倒序排序。
- [ ] 对所有高于 `1.00x` 的项目补 Go micro 或 profile，确认是否是稳定差距。
- [x] 更新 `docs/BENCHMARK.md` 或本文的基线小节。

2026-07-05 在 `quanquan/feature/glua-final-perf` 起点重建 `bin/glua` / `bin/gluac`，并显式使用
`/Users/zing/.local/lua/5.3.6/bin/lua` 与 `/Users/zing/.local/lua/5.3.6/bin/luac` 作为官方工具。
三轮默认完整 benchmark 排序如下：

| 排名 | 用例 | 三轮倍率 | 平均 | 下一步 |
| ---: | --- | ---: | ---: | --- |
| 1 | `compile_3000_functions` | `1.30x / 1.32x / 1.31x` | `1.31x` | 进入 profile |
| 2 | `recursion` | `1.05x / 1.04x / 1.04x` | `1.04x` | 未达到 `1.08x` 进入门槛 |
| 3 | `function_call` | `1.03x / 1.01x / 1.02x` | `1.02x` | 未达到 `1.05x` 进入门槛 |
| 4 | `arith_mix_loop` | `1.03x / 1.01x / 1.02x` | `1.02x` | 仅回归复核 |
| 5 | `string_concat` | `1.00x / 1.03x / 1.01x` | `1.01x` | 仅回归复核 |
| 6 | `table_rw` | `0.88x / 0.87x / 0.85x` | `0.87x` | 停止扩张 |
| 7 | `closure_upvalue` | `0.85x / 0.85x / 0.85x` | `0.85x` | 停止扩张 |
| 8 | `arith_chain_temp` | `0.77x / 0.76x / 0.78x` | `0.77x` | 停止扩张 |
| 9 | `arith_add_loop` | `0.65x / 0.68x / 0.63x` | `0.65x` | 停止扩张 |
| 10 | `stdlib_math_string` | `0.57x / 0.57x / 0.57x` | `0.57x` | 停止扩张 |

结论：本轮无新增运行期主项。下一小切口只允许 profile `compile_3000_functions`，确认是否仍存在
parser AST 构造或 codegen arena 生命周期结构性空间。

## 1. `compile_3000_functions` 紧凑函数体设计与测试

- [x] 复跑当前 `BenchmarkCompileSource3000Functions` 五轮，记录 `ns/op`、`B/op`、`allocs/op`。
- [x] 生成 CPU 和 memory profile，确认剩余成本仍在函数体 AST 构造或 codegen arena 生命周期。
- [x] 设计 `compactSimpleFunctionBody` 私有结构，明确字段来源、生命周期和不可逃逸边界。
- [x] 列出 parser、semantic、codegen、debug、luac 反汇编、错误位置的逐项一致性要求。
- [x] 先补 bytecode/golden 测试：目标形态 `function fN(x) return x + K end` 的子 `Proto` 与普通路径逐项一致。
- [x] 先补 parser/lexer mark-reset 基础设施和定向测试，固定试探解析失败后可恢复 token 流。
- [x] 先补 debug 测试：`debug.getinfo` 行号、局部变量生命周期、`luac -l -l` 输出不变。
- [x] 先补回退测试：table/method 函数名、vararg、多参数、local/upvalue、嵌套函数、label/goto、复杂表达式、调用、字段访问、索引访问、拼接、幂运算、语法错误位置。
- [ ] 实现 prototype 1：parser 完整识别目标形态，并减少目标路径 AST 构造或 arena 留存。
- [ ] 使用 `gopls check <changed-go-files>` 做修改前后诊断。
- [ ] 执行 `gofmt`，并立即 `git add` 修改或新增的 Go 文件。
- [ ] 运行定向 parser/codegen/luac/debug 测试。
- [ ] 运行 `BenchmarkCompileSource3000Functions` 五轮。
- [ ] 若 wall-clock 未稳定下降至少 `5%`，或 B/op 高于当前约 `3.78 MB/op`，回退生产改动并记录证伪。
- [ ] 若通过门槛，重建 CLI 并运行完整 benchmark 三轮。
- [ ] 若完整 benchmark `compile_3000_functions` 未稳定低于当前约 `1.33x`，回退或降级为实验记录。

2026-07-05 在最终分支基线后补跑 `compile_3000_functions` Go micro/profile：

```bash
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=5s -count=1 \
  -cpuprofile /tmp/go-lua-vm-final-profiles/compile_3000_cpu.pprof \
  -memprofile /tmp/go-lua-vm-final-profiles/compile_3000_mem.pprof
CGO_ENABLED=0 go test ./internal/luac -run '^$' \
  -bench '^BenchmarkCompileSource3000Functions$' \
  -benchmem -benchtime=3s -count=5
```

结果：

- profile 单轮：`2.657751 ms/op`、`3,780,936 B/op`、`85 allocs/op`。
- 五轮 micro：`2.647352 / 2.662351 / 2.711824 / 2.710999 / 2.660101 ms/op`。
- 五轮 B/op 稳定约 `3.781 MB/op`，`87 allocs/op`。
- CPU profile 仍被 Go runtime/GC/调度噪声主导；源码侧可归因项分散在 `Lexer.NextToken`、`Parser.advance`、
  `parseFunctionStatement`、`parseFunctionBodyInto`、`parseReturnStatementInto` 和 `compileBlock`。
- alloc_space 主项为 `newFunctionStatement` 约 `36.22%`、`prepareDirectFunctionBlockCapacity`
  约 `26.59%`、`newLiteralExpression` 约 `10.07%`、`newBinaryExpression` 约 `5.99%`、
  `newNameExpression` 约 `3.54%`。
- alloc_objects 主项为 `parseExpressionList`、`prepareDirectFunctionBlockCapacity`、
  `newNameExpression`、`newFunctionStatement`、`newBinaryExpression` 和 `newLiteralExpression`。

结论：最终分支没有出现新的 lexer/token/Source 低风险单点热点；剩余差距仍对应函数体 AST 构造和
codegen arena 生命周期。下一步只能进入 `compactSimpleFunctionBody` 私有结构设计与定向测试，不能再做
局部扫描或容量调参。

2026-07-05 完成 `compactSimpleFunctionBody` 结构设计，下一轮生产实现必须遵守以下约束：

- parser 先补私有 token mark/reset helper，快照 `parser.current` 和底层 lexer/source 位置；没有回滚能力时
  禁止实现紧凑试探解析。
- compact 模式只由 `internal/luac.CompileSourceWithSyntax` 使用；`parser.New` / `parser.NewWithSyntax`
  继续返回完整 AST，避免破坏 parser 包调用方。
- summary 使用 parser 页式 arena 存储，`FunctionBody` 只保存指针或索引，避免把所有函数体结构膨胀为
  内嵌 Name/Literal/Binary 节点。
- summary 字段必须覆盖：参数名、整数常量、return 位置、操作符位置、字面量位置、函数体起止行、
  是否为 `param + Kinteger` 精确形态。
- 试探只覆盖单参数、非 vararg、token 序列精确为 `return <param> + <integer> end` 的普通简单函数名。
- 试探失败必须 reset 并重新走普通 `parseBlockUntilInto`，保证复杂函数体 AST、错误位置、semantic/goto
  校验和 debug 信息不变。
- codegen 只在 summary 与当前参数 local 一致时直发子 `Proto`；`ADD` 使用操作符行，`RETURN` 使用 return 行，
  local 生命周期、常量表、`MaxStackSize`、lineinfo 和 `luac -l -l` 必须与普通路径一致。
- 已有 `TestCompileSourceSimpleFunctionBodyPrototypeShape` 固定目标 bytecode/debug 形态；
  `TestCompileSourceSimpleFunctionBodyNonTargetKeepsOrdinaryShape` 固定最小非目标回退形态。后续实现前仍需继续
  补充多类非目标回退测试。

2026-07-05 已完成 mark-reset 基础设施小切口：`lexer.Lexer` 增加 `Mark` / `Reset`，parser 增加私有
`mark` / `reset`，并用 `TestParserMarkResetRestoresTokenStream` 固定恢复前瞻 token 和后续 token 流一致。
该切口不启用 compact 解析，也不改变任何 AST/codegen 行为；下一轮应继续补 debug 与非目标回退测试。

2026-07-05 已补 `TestCompileSourceSimpleFunctionBodyDebugAndListGuards`：通过 `DumpSource` 生成
binary chunk，再由 VM 执行并断言 `debug.getinfo(f0, "Slu")` 的行号、参数数量、vararg 语义，以及
`debug.getlocal(f0, 1)` 的参数名；同时用 `Run -l -l` 固定子 Proto 行号、locals 生命周期、
debug dump 关键片段。该切口只补 compact fast path 前置语义门禁，不启用生产优化。

2026-07-05 已补第一批非目标回退 guard：`TestCompileSourceSimpleFunctionBodySignatureFallbackGuards`
覆盖 table 字段函数名、method 隐式 `self`、vararg、多参数、local upvalue operand 和嵌套 closure body。
这些样例必须保留各自的顶层赋值/SELF、参数表、vararg 标记、upvalue 捕获、CLOSURE/TAILCALL 等普通路径特征。
复杂表达式、label/goto 和语法错误位置仍待后续小切口补齐。

2026-07-05 已补复杂表达式回退 guard：`TestCompileSourceSimpleFunctionBodyExpressionFallbackGuards`
覆盖函数调用操作数、字段访问、索引访问、拼接、幂运算和非 integer 数字常量。后续 compact summary
只能识别精确 `return <param> + <integer>`，这些样例必须保留 CALL、GETTABLE、CONCAT、POW、number
constant 等普通路径特征。剩余回退门禁集中在 label/goto 与语法错误位置。

2026-07-05 已补最后一组非目标回退 guard：
`TestCompileSourceSimpleFunctionBodyControlFlowAndErrorFallbackGuards` 覆盖 goto/label 控制流和不完整
`return x + end` 语法错误。后续 compact 试探失败时必须保留普通路径的 JMP 指令、非两指令函数体、
结构化 `parser.ParseError`、行列和表达式错误消息。至此 compactSimpleFunctionBody prototype 前置的
非目标回退测试已闭合，下一轮可开始实现 prototype 1。

## 2. `function_call` batch guard 冻结候选

- [ ] 仅在 `function_call` 完整三轮稳定高于 `1.05x` 时进入本项。
- [ ] 运行 `BenchmarkPreparedFunctionCallOfficial` 五轮和 CPU profile。
- [ ] 确认热点仍集中在 `TryExecuteFunctionCallAssignForLoopBatch` 的动态 guard，而不是官方基线噪声。
- [ ] 设计 batch 内可冻结字段：闭包寄存器、函数 Proto、leaf add-return descriptor、参数寄存器、结果寄存器。
- [ ] 补 guard 测试：函数值被替换、upvalue/环境变化、hook 打开、yield/continuation、错误 PC、traceback、context 取消。
- [ ] 实现最小 guard 冻结 prototype。
- [ ] 运行 `BenchmarkPreparedFunctionCallOfficial` 五轮；未稳定改善至少 `8%` 则回退。
- [ ] 运行完整 benchmark 三轮；`function_call` 未稳定低于 `1.00x` 则不保留生产改动。

## 3. `recursion` closure/upvalue 生命周期候选

- [ ] 仅在 `recursion` 完整三轮稳定高于 `1.08x` 时进入本项。
- [ ] 运行 `BenchmarkPreparedRecursion` 五轮和 CPU/memory profile。
- [ ] 证明剩余成本仍为 local `fib` closure 和 self upvalue cell 分配。
- [ ] 设计非逃逸 local function descriptor，列出返回、传参、存表、闭包身份比较、`debug.getupvalue`、hook、traceback、pcall/error、coroutine/yield 的回退边界。
- [ ] 补 guard 测试：闭包返回、闭包存表、闭包传参、`debug.getupvalue`、错误 traceback、line hook、call hook、yield。
- [ ] 实现最小 prototype。
- [ ] 运行 `BenchmarkPreparedRecursion` 五轮；未去掉当前 `2 allocs/op` 或 wall-clock 不降则回退。
- [ ] 运行完整 benchmark 三轮；`recursion` 未稳定低于 `1.00x` 则不保留生产改动。

## 4. 噪声带回归复核

- [ ] `string_concat` 只有在完整三轮稳定高于 `1.08x` 时重新 profile。
- [ ] `arith_mix_loop` 只有在完整三轮稳定高于 `1.08x` 时重新 profile。
- [ ] `arith_add_loop`、`arith_chain_temp`、`table_rw`、`closure_upvalue`、`stdlib_math_string` 当前已快于官方，默认不再扩张。
- [ ] 任一噪声带项目如果没有新的单一主因，不做生产改动，只更新证伪记录。

## 5. 全局验证与提交

- [ ] 每个 Go 改动提交前执行 `gopls check <changed-go-files>`。
- [ ] 每个 Go 改动提交前执行 `gofmt -w <changed-go-files>`。
- [ ] 每个 Go 改动后立即 `git add <changed-go-files>`。
- [ ] 执行相关定向测试和 benchmark。
- [ ] 执行 `CGO_ENABLED=0 go test ./...`。
- [ ] 执行 `./scripts/check-go-gates.sh`。
- [ ] 执行 `git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'`，结果必须为空。
- [ ] 涉及 CLI、bytecode、VM、stdlib 或官方兼容行为时重建 `bin/glua` / `bin/gluac`。
- [ ] 确认官方 `lua` / `luac` 为 5.3.6。
- [ ] 执行 `./scripts/compare-cli-golden.sh`。
- [ ] 执行 `./scripts/compare-official-executables.sh`。
- [ ] 执行 `./scripts/run-official-tests.sh`。
- [ ] 更新方案/TODO/benchmark 文档中的结果。
- [ ] 创建中文 commit，优先使用 `perf:`、`fix:`、`docs:` 前缀。
- [ ] 推送 `quanquan/feature/glua-final-perf`。

## 明确禁止重复的方向

- [ ] 不再围绕常量索引预留做调参。
- [ ] 不再围绕函数语句 arena 或顶层语句容量做调参。
- [ ] 不再围绕 expression arena 大页容量做调参。
- [ ] 不再围绕 operator 扫描、标识符扫描、keyword 判定、Source ASCII 读取做调参。
- [ ] 不再做 codegen-only 简单函数直发。
- [ ] 不再扩张已完成的 string concat builder、arith mix batch、table dense fast path。
- [ ] 不在无 profile 证据时扩张 `function_call` 或 `recursion` fast path。
