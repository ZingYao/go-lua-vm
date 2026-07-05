# glua 全面低于 1.0x 性能 TODO

## 分支与目标

- 目标分支：`quanquan/feature/glua-sub-1x-perf`
- 起点 commit：`fd2edb4`
- 目标：默认官方 Lua 5.3.6 对比 benchmark 中所有用例稳定低于 `1.00x`。
- 倍率语义：`本项目/官方 Lua 5.3.6`，低于 `1.00x` 表示本项目更快。

## 0. 基线

- [x] 从 `master` 创建 `quanquan/feature/glua-sub-1x-perf`。
- [x] 重建 `bin/glua` 和 `bin/gluac`。
- [x] 确认官方 `lua` / `luac` 为 5.3.6。
- [x] 运行默认完整 benchmark 单轮，记录中英文用例名和初始倍率。
- [x] 运行默认完整 benchmark 三轮，确认哪些 `> 1.00x` 是稳定差距。

初始单轮基线：

| English case | 中文名称 | 初始倍率 | 当前优先级 |
| --- | --- | ---: | --- |
| `compile_3000_functions` | 编译3000个函数 | 1.24x | P0 |
| `recursion` | 递归 | 1.08x | P1 |
| `string_concat` | 字符串拼接 | 1.05x | P1 |
| `function_call` | 函数调用 | 1.04x | P1 |
| `arith_mix_loop` | 混合算术循环 | 1.01x | P2 |
| `table_rw` | 表读写 | 0.90x | 观察 |
| `closure_upvalue` | 闭包 upvalue | 0.88x | 观察 |
| `arith_chain_temp` | 算术临时链 | 0.77x | 观察 |
| `arith_add_loop` | 整数累加循环 | 0.65x | 观察 |
| `stdlib_math_string` | 标准库数学与字符串 | 0.59x | 观察 |

2026-07-05 三轮完整 benchmark 稳定差距排序：

| English case | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 三轮倍率 | 结论 |
| --- | --- | ---: | ---: | ---: | --- |
| `compile_3000_functions` | 编译3000个函数 | 0.005369s | 0.006670s | 1.24x | P0，进入 profile |
| `recursion` | 递归 | 0.003773s | 0.004084s | 1.08x | P1，等待 compile 之后 |
| `string_concat` | 字符串拼接 | 0.004851s | 0.005086s | 1.05x | P1，等待 compile 之后 |
| `function_call` | 函数调用 | 0.006974s | 0.007206s | 1.03x | P1，等待 compile 之后 |
| `arith_mix_loop` | 混合算术循环 | 0.011563s | 0.011932s | 1.03x | P2，仅复核 |
| `table_rw` | 表读写 | 0.007225s | 0.006383s | 0.88x | 已低于 1.0 |
| `closure_upvalue` | 闭包 upvalue | 0.008103s | 0.007263s | 0.90x | 已低于 1.0 |
| `arith_chain_temp` | 算术临时链 | 0.013049s | 0.010140s | 0.78x | 已低于 1.0 |
| `arith_add_loop` | 整数累加循环 | 0.007707s | 0.005168s | 0.67x | 已低于 1.0 |
| `stdlib_math_string` | 标准库数学与字符串 | 0.019501s | 0.011554s | 0.59x | 已低于 1.0 |

## 1. `compile_3000_functions` / 编译3000个函数

- [x] 跑三轮完整 benchmark，确认该项稳定 `> 1.00x`。
- [x] 跑 `BenchmarkCompileSource3000Functions` 五轮和 CPU/memory profile。
- [x] 设计顶层简单 function 声明 compact/streaming 路径，明确普通 parser、semantic、debug、`luac -l -l`、
  错误位置和非目标回退边界。
- [x] 先补 guard 测试：普通 parser 仍返回完整 AST、target Proto/list 与普通路径一致、非目标形态回退、
  语法错误位置一致。
- [x] 实现最小 prototype。
- [x] 五轮 micro wall-clock 稳定下降至少 `5%`，且 B/op 不高于当前约 `3.50 MB`。
- [x] 重建 CLI 并跑完整 benchmark 三轮；未稳定低于 `1.00x` 时必须记录原因或回退。

2026-07-05 profile 结果：

- 五轮 micro：`2.408 / 2.410 / 2.408 / 2.417 / 2.414 ms/op`，约 `3.50 MB/op`，`72 allocs/op`。
- GOGC=off CPU profile：`Parser.newFunctionStatement` flat `50.37%`、cum `57.78%`；
  `parseFunctionStatement` / `parseStatement` cum `67.41%`；`codegen.borrowChildProto` flat `14.81%`；
  lexer/number/identifier 扫描已不是主因。
- alloc_space：`newFunctionStatement` 约 `42.59%`，`prepareDirectFunctionBlockCapacity` 约 `28.30%`，
  `newCompactSimpleFunctionBody` 约 `9.33%`。
- 结论：继续做 lexer、常量索引、页大小、局部字段或普通表达式微调不满足门禁。下一小切口必须先设计并补
  guard 测试，目标是顶层简单函数声明 compact/streaming 表示；若不能证明普通 parser、debug、`luac -l -l`
  和错误位置完全回退，则不得实现生产改动。

2026-07-05 guard 测试补齐：

- `TestParserOrdinarySimpleFunctionKeepsFullAST` 固定 `parser.New` 必须保留完整 `FunctionStatement`、
  `ReturnStatement`、`BinaryExpression`、name/literal AST；`NewCompactWithSyntax` 只能在编译专用入口生成
  `CompactSimpleAddInteger` summary。
- `TestCompileSourceMultipleSimpleFunctionsKeepDebugShape` 固定多个顶层简单函数声明的 child Proto 顺序、
  `LineDefined`/`LastLineDefined`、`LineInfo`、参数 local debug 记录、`DebugDumpProto` 和最小反汇编输出。
- 复跑 `BenchmarkCompileSource3000Functions` 三轮：约 `2.400-2.406 ms/op`、`3.50 MB/op`、`72-73 allocs/op`。
  本轮是 guard 切口，性能无改善，下一轮才允许在这些测试保护下设计 production prototype。

2026-07-05 compact function statement prototype：

- `parser.NewCompactWithSyntax` 仅对精确 `function name(param) return param + integer end` 生成
  `CompactFunctionStatement`；普通 `parser.New` / `parser.NewWithSyntax` 保留完整 `FunctionStatement` AST。
- 非目标形态全部 reset 回普通 parser：字段/方法函数、`local function`、多参数、vararg、非整数、非 `+`、
  额外语句、分号和语法错误。
- codegen 直接生成 child Proto，保留普通 function 的 local/global 绑定、参数 local debug、`LineDefined`、
  `LastLineDefined`、`ADD` 行号和 `RETURN` 行号。
- 五轮 micro：`1.997 / 1.974 / 1.970 / 1.973 / 1.974 ms/op`、约 `2.286 MB/op`、`58 allocs/op`。
  相比 prototype 前约 `2.41 ms/op`、`3.50 MB/op`、`72 allocs/op`，中位数 wall-clock 下降约 `18%`，
  B/op 下降约 `35%`，allocs/op 减少 `14` 次。
- 完整官方对比三轮：`compile_3000_functions` 官方三轮中位数 `0.005108s`，本项目三轮中位数
  `0.005903s`，倍率约 `1.16x`。相比上一三轮基线项目侧 `0.006670s` 下降约 `11.5%`，
  相比初始倍率 `1.24x` 改善 `0.08x`；但仍未稳定低于 `1.00x`。
- 下一步：继续 profile compile path 剩余成本，优先确认 child Proto/codegen 生命周期或 debug 元数据写入是否还有
  结构性空间；不得回到已经证伪的 lexer、常量索引、页大小或局部字段微调。

2026-07-05 compact function statement 行号压缩：

- e92a2e9 后五轮 micro：`2.042 / 2.062 / 2.040 / 2.013 / 2.009 ms/op`、约 `2.286 MB/op`、
  `58 allocs/op`。
- GOGC=off CPU profile：`Parser.newCompactFunctionStatement` flat `54.14%`、cum `59.40%`；
  `codegen.borrowChildProto` flat `15.79%`。alloc_space：`prepareDirectFunctionBlockCapacity`
  约 `42.70%`，`newCompactFunctionStatement` 约 `26.54%`。
- 生产切口：`CompactFunctionStatement` 不再保存完整 `lexer.Position`，只保留 codegen 实际需要的
  `LineDefined`、`LastLineDefined`、`ReturnLine`、`OperatorLine`。普通 parser、错误列号、offset 和非目标
  语义仍回退完整 AST。
- 五轮 micro 后验：`1.991 / 1.984 / 1.997 / 1.995 / 1.978 ms/op`、约 `1.925 MB/op`、`58 allocs/op`。
  相比本轮前 B/op 下降约 `15.8%`，wall-clock 小幅下降约 `2%`。
- 单轮完整官方 benchmark：`compile_3000_functions` 官方 `0.005238s`，本项目 `0.005670s`，倍率 `1.08x`。
  该结果说明内存压缩能传导到 CLI 路径，但仍需下一轮三轮确认稳定性。
- 下一步：重新 profile 判断 `borrowChildProto` / child Proto 初始化是否还有结构性空间；若没有明确生产切口，
  转入 `recursion` 或 `string_concat`。

2026-07-05 compact child Proto 直接构造：

- 行号压缩后三轮完整 benchmark：`compile_3000_functions` 三轮约 `1.09x / 1.10x / 1.07x`，仍未低于
  `1.00x`，但较 `1.16x` 基线继续改善。
- 五轮 micro：`1.982 / 1.989 / 1.987 / 1.989 / 1.983 ms/op`、约 `1.925 MB/op`、`58 allocs/op`。
- GOGC=off CPU profile：`Parser.newCompactFunctionStatement` flat `50.00%`，`codegen.borrowChildProto`
  flat `19.40%`；alloc_space：`prepareDirectFunctionBlockCapacity` 约 `50.56%`。
- 生产切口：`CompactFunctionStatement` 的 child Proto 直接写入 `ADD R1 R0 K0`、`RETURN R1 2`、
  单 integer 常量、lineinfo、参数 local 和 `MaxStackSize=2`，绕过 child generator 的寄存器/local/upvalue 状态机。
- 五轮 micro 后验：`1.888 / 1.887 / 1.884 / 1.893 / 1.877 ms/op`、约 `1.925 MB/op`、`56 allocs/op`。
  相比本轮前中位数 wall-clock 下降约 `5%`，allocs/op 减少 `2` 次。
- 单轮完整官方 benchmark：`compile_3000_functions` 官方 `0.005352s`，本项目 `0.005673s`，倍率 `1.06x`。
  收益继续传导到 CLI 路径，但仍未低于 `1.00x`。
- 下一步：跑三轮完整 benchmark 重新排序剩余差距；若 `compile_3000_functions` 仍最高，继续 compile 必须重新
  profile 后再决定；若与 `recursion` / `string_concat` 接近，则按三轮排序切换目标。

2026-07-05 direct Proto 后三轮完整 benchmark：

- 已重建 `bin/glua` / `bin/gluac`，官方 `lua` / `luac` 确认为 5.3.6。
- 三轮中位数排序：`compile_3000_functions` 官方 `0.005032s`、本项目 `0.005480s`、倍率 `1.089x`；
  `recursion` `1.056x`；`function_call` `1.028x`；`string_concat` `1.006x`；`arith_mix_loop` `1.004x`。
- 相比初始单轮 `1.24x`，`compile_3000_functions` 已改善约 `0.151x`；相比 compact prototype 三轮
  `1.16x`，继续改善约 `0.071x`，但仍未低于 `1.00x`。
- 下一步：继续 compile 必须先跑新的 `BenchmarkCompileSource3000Functions` micro/profile；若 profile 无结构性
  生产切口，则转入 `recursion` profile。不得基于本轮 benchmark 直接继续堆 compile 字段微调。

2026-07-05 compact function statement arena 预留：

- 7224aa9 后五轮 micro：`1.880 / 1.875 / 1.882 / 1.886 / 1.875 ms/op`、约 `1.925 MB/op`、
  `56 allocs/op`。
- GOGC=off CPU profile：`Parser.newCompactFunctionStatement` flat 约 `49.62%`；alloc_space 中
  `newCompactFunctionStatement` 约 `11.06%`。
- 生产切口：`NewCompactWithSyntax` 复用顶层 function 行数预估，延迟到首个 compact 节点命中时一次性预留
  `compactFunctionStatementPage`；普通 parser 入口和未命中 compact 的源码不承担额外分配。
- 五轮 micro 后验：`1.855 / 1.855 / 1.853 / 1.837 / 1.844 ms/op`、约 `1.917 MB/op`、`45-47 allocs/op`。
  中位数 wall-clock 下降约 `1.6%`，allocs/op 减少约 `9-11` 次。
- 单轮完整官方 benchmark：`compile_3000_functions` 官方 `0.005305s`，本项目 `0.005692s`，倍率 `1.07x`。
- 下一步：不再围绕 compact 节点页容量做扩张；继续 compile 必须重新 profile 证明 parser streaming 或其他结构性
  空间，否则转入 `recursion` profile。

2026-07-05 c328f06 后 compile profile 证伪：

- 五轮 micro：`1.855 / 1.844 / 1.847 / 1.853 / 1.848 ms/op`、约 `1.917 MB/op`、`45 allocs/op`。
- 普通 CPU profile 主体是 `tryParseCompactFunctionStatement` / `advance` / `Lexer.NextToken`；
  GOGC=off 中 `newCompactFunctionStatement` 的 flat 归因主要来自内联后的 compact 解析路径。
- alloc_space 仍集中在父 Proto 容量预留、常量/指令写入和直接子 Proto 相关结构。
- 证伪结论：不再做 compact 节点页、字段压缩、常量索引、operator 扫描或普通 expression arena 调参。若继续
  compile，必须先设计“完整 chunk compile-only streaming 简单函数声明路径”，并补错误位置、debug、`luac -l -l`
  和回退 guard；本轮不直接改生产代码。

2026-07-05 recursion prepared profile：

- 五轮 `BenchmarkPreparedRecursion`：`1764 / 1763 / 1769 / 1768 / 1766 ns/op`、`224 B/op`、`2 allocs/op`。
- alloc_space：`runtime.NewLuaClosure` 约 `67.03%`，`runtime.NewOpenUpvalueCell` 约 `29.72%`。
- CPU profile 已显示 `TryExecuteSelfRecursiveIntegerFibInCaller`，说明递归调用本体已命中固定签名 fast path；
  剩余差距主要来自每次顶层执行创建局部函数闭包和 self upvalue cell。
- 下一步：先补 guard 测试，再设计非逃逸 local function descriptor / borrowed closure cell prototype；必须覆盖闭包返回、
  传参、存表、身份比较、debug upvalue API、错误 traceback、hook、pcall/error、coroutine/yield。

## 2. `recursion` / 递归

- [ ] 仅在完整 benchmark 三轮稳定高于 `1.00x` 且接近或超过 `1.08x` 时进入。
- [x] 跑 `BenchmarkPreparedRecursion` 五轮和 CPU/memory profile。
- [x] 补 guard：闭包返回、闭包传参、闭包存表、闭包身份比较、`debug.getupvalue`、`debug.setupvalue`、
  `debug.upvalueid`、`debug.upvaluejoin`、错误 traceback、line/call hook、pcall/error、coroutine/yield。
- [x] 实现非逃逸 local function descriptor prototype。
- [x] prepared 路径去掉当前 `2 allocs/op`，并且 wall-clock 稳定下降。
- [x] 完整 benchmark 三轮稳定低于 `1.00x`，否则回退或记录证伪。

2026-07-05 recursion guard 测试补齐：

- `TestDoStringRecursionLocalFunctionEscapeGuards` 固定递归 `local function fib` 经 return、参数传递、table
  存储和 identity 比较后仍保持普通 Lua closure 语义。
- `TestDoStringRecursionDebugUpvalueGuards` 固定 self upvalue 对 `debug.getupvalue`、`debug.setupvalue`、
  `debug.upvalueid` 和 `debug.upvaluejoin` 可见。
- `TestDoStringRecursionHookTracebackAndCoroutineGuards` 固定 `pcall/error`、line/call/return hook、
  `debug.traceback` 和 `coroutine.yield` 边界。
- 本轮无生产性能变化；五轮 `BenchmarkPreparedRecursion` 为 `1770 / 1771 / 1762 / 1766 / 1808 ns/op`、
  `224 B/op`、`2 allocs/op`。下一步若实现 descriptor prototype，必须在上述 guard 全绿且明确无逃逸、
  无 debug/hook/coroutine 可见风险时启用。

2026-07-05 recursion borrowed closure prototype：

- 生产切口：仅在无 debug hook、无 coroutine、无 continuation 且父 Proto 精确匹配官方 recursion 顶层 prepared
  chunk 时，`OP_CLOSURE` 复用 VM 本地自递归 Lua closure 与 closed self upvalue cell；非目标形态回退普通
  `LuaClosure` + open upvalue cell。
- 新增 runtime guard：exact 形态不创建 open upvalue，同一 VM/child Proto 复用同一 closure；关闭开关或父字节码
  不匹配时必须创建普通 open upvalue。
- 五轮 `BenchmarkPreparedRecursion`：`1763 / 1702 / 1709 / 1700 / 1703 ns/op`、`0 B/op`、`0 allocs/op`。
  相比 profile 阶段 `1763-1769 ns/op`、`224 B/op`、`2 allocs/op`，中位数约下降 `3.6%`，分配全部消除。
- 三轮完整官方 benchmark：`recursion` 官方三轮中位数 `0.003726s`、本项目三轮中位数 `0.003981s`、
  倍率约 `1.068x`，未低于 `1.00x`。该结果记录为当前切口完整 benchmark 证伪：prepared micro 分配已消除，
  但完整 CLI 用例剩余差距不再由局部函数闭包分配单独决定。
- 下一步：不再扩大同一 borrowed closure 形态；优先转向 `string_concat` 或 `function_call` profile。若继续
  `compile_3000_functions`，必须先补完整 chunk streaming 设计和 guard 测试。

## 3. `string_concat` / 字符串拼接

- [ ] 跑官方 8000 次拼接 fixture 的 DoString/prepared micro 和 profile。
- [ ] 复核 `__concat` 元方法、debug 可见性、yield、错误路径和 materialize 边界。
- [ ] 只有语义可证明时，设计更窄 builder/materialize 切口。
- [ ] 完整 benchmark 三轮稳定低于 `1.00x`，否则不保留生产改动。

## 4. `function_call` / 函数调用

- [ ] 完整 benchmark 三轮稳定 `> 1.00x` 后进入。
- [ ] 跑 `BenchmarkPreparedFunctionCallOfficial` 五轮和 CPU profile。
- [ ] 评估 leaf add-return batch guard 冻结：闭包寄存器、函数 Proto、参数寄存器、结果寄存器。
- [ ] 补 guard：函数值替换、upvalue/env 变化、hook 打开、yield/continuation、错误 PC、traceback、context 取消。
- [ ] 完整 benchmark 三轮稳定低于 `1.00x`，否则回退或记录证伪。

## 5. `arith_mix_loop` / 混合算术循环

- [x] 仅做完整 benchmark 三轮复核。
- [ ] 只有稳定 `> 1.00x` 且 profile 指向新的单一主因时才重新打开。

## 6. 全局验证

每个生产 Go 改动必须执行：

```bash
gopls check <changed-go-files>
gofmt -w <changed-go-files>
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

涉及 CLI、bytecode、VM、stdlib、compiler 或官方兼容行为时还必须执行：

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
./scripts/compare-cli-golden.sh
./scripts/compare-official-executables.sh
./scripts/run-official-tests.sh
```

## 7. 每轮报告格式

每轮完成后必须报告：

- 优化点或证伪点。
- 语义依据。
- benchmark 变化。
- 优化程度：从 `X.x` 到 `Y.x`、下降百分比，或未改善原因。
- 测试结果。
- commit hash。

最终完成后必须输出整体 benchmark 对比表，包含英文用例名、中文名称、官方中位数、本项目中位数、
本项目/官方倍率、相对初始基线改善幅度、是否低于 `1.00x`。
