# glua 激进性能优化 TODO

## 目标与边界

本文记录 `quanquan/feature/glua-aggressive-perf` 分支的激进性能优化计划。目标是在不引入 CGO、不接入 Lua C API、不增加外部依赖的前提下，减少 glua 与 Lua 5.3.6 官方解释器在主要 benchmark 上的差距。

优化对象只覆盖 glua 运行期和必要的编译期元数据，不改变 Lua 5.3 用户可见语义。`debug hook`、`coroutine/yield`、`traceback`、`error`、`debug.getinfo`、upvalue、弱表和 finalizer 语义必须优先保持正确；任何 fast path 不能证明等价时必须回退现有普通 VM 路径。

本专项不实现 JIT。JIT 仍按 `docs/JIT_TODO.md` 作为长期方向单独推进。

## 当前基线

截至 2026-07-03，`master` 已通过：

- `CGO_ENABLED=0 go test ./...`
- `./scripts/check-go-gates.sh`
- 重建 `bin/glua` / `bin/gluac`
- 官方 `lua` / `luac` 5.3.6 版本门禁
- `./scripts/compare-cli-golden.sh`
- `./scripts/compare-official-executables.sh`
- `./scripts/run-official-tests.sh`

最近默认完整 benchmark 已达到主要路径三轮低于 3x 的短期目标，但 glua 与官方 C Lua 仍有明显差距。继续优化不应再堆局部字段和分支微调，应优先减少解释器循环、opcode 解码、CALL 边界和 table 扩容等结构性成本。

### 2026-07-03 激进分支基线

分支：`quanquan/feature/glua-aggressive-perf`

版本门禁：重建 `bin/glua` / `bin/gluac` 后，官方 `lua` / `luac` 与本项目 `glua` / `gluac` 均确认为 Lua 5.3.6。

official-sized Go micro 三轮基线：

| 用例 | 时间 | 分配 |
| --- | ---: | ---: |
| `BenchmarkDoStringArithAddLoopOfficial` | 16.53 / 16.57 / 16.62ms/op | 58.4KB/op, 216 allocs/op |
| `BenchmarkPreparedArithAddLoopOfficial` | 16.44 / 16.47 / 16.61ms/op | 9-10B/op, 0 allocs/op |
| `BenchmarkDoStringArithMixLoopOfficial` | 28.59 / 28.48 / 28.46ms/op | 60.4KB/op, 237 allocs/op |
| `BenchmarkPreparedArithMixLoopOfficial` | 28.33 / 28.30 / 28.40ms/op | 20B/op, 0 allocs/op |
| `BenchmarkDoStringArithChainTempOfficial` | 33.83 / 33.79 / 33.78ms/op | 59.3KB/op, 223 allocs/op |
| `BenchmarkPreparedArithChainTempOfficial` | 33.71 / 33.79 / 33.73ms/op | 22B/op, 0 allocs/op |
| `BenchmarkDoStringTableReadWriteOfficial` | 15.21 / 16.17 / 15.08ms/op | 33.56MB/op, 265 allocs/op |
| `BenchmarkPreparedTableReadWriteOfficial` | 14.15 / 13.63 / 14.00ms/op | 33.50MB/op, 18 allocs/op |
| `BenchmarkDoStringStdlibMathString` | 36.21 / 39.69 / 38.95ms/op | 38.88MB/op, 400148 allocs/op |
| `BenchmarkDoStringFunctionCall` | 413 / 416 / 416us/op | 61.8KB/op, 253 allocs/op |
| `BenchmarkDoStringClosureUpvalueOfficial` | 16.77 / 16.73 / 16.76ms/op | 62.7KB/op, 269 allocs/op |
| `BenchmarkPreparedClosureUpvalueOfficial` | 16.31 / 16.30 / 16.41ms/op | 492B/op, 4 allocs/op |
| `BenchmarkDoStringRecursion` | 7.95 / 7.59 / 7.66ms/op | 89.6KB/op, 362 allocs/op |
| `BenchmarkPreparedRecursion` | 7.40 / 7.39 / 7.39ms/op | 292B/op, 2 allocs/op |
| `BenchmarkCompileSource3000Functions` | 8.34 / 8.41 / 8.41ms/op | 7.58MB/op, 81151-81152 allocs/op |

默认完整 benchmark 三轮：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | 2.80x / 2.70x / 2.77x |
| `arith_mix_loop` | 2.89x / 2.89x / 2.90x |
| `arith_chain_temp` | 3.02x / 2.95x / 2.96x |
| `table_rw` | 2.97x / 2.96x / 2.95x |
| `function_call` | 2.71x / 2.68x / 2.76x |
| `string_concat` | 1.79x / 1.81x / 1.78x |
| `closure_upvalue` | 2.62x / 2.61x / 2.64x |
| `stdlib_math_string` | 2.17x / 2.20x / 2.17x |
| `recursion` | 3.16x / 3.16x / 3.17x |
| `compile_3000_functions` | 2.29x / 2.25x / 2.31x |

结论：算术 prepared 与 DoString wall-clock 基本重合，说明编译、OpenLibs 和 State 初始化不是 `arith_*` 的主要耗时；`table_rw` prepared 仍约 33.5MB/op，继续指向运行期 table 数组扩容；`recursion` 默认完整三轮稳定高于 3x，是当前首要边缘项。下一轮按 TODO 生成 `arith_add_loop`、`arith_chain_temp`、`arith_mix_loop` CPU profile，再决定 Proto 预解码和 arithmetic superinstruction 的最小切口。

### 2026-07-03 arithmetic CPU profile

命令：`CGO_ENABLED=0 go test ./lua -run '^$' -bench '^BenchmarkPreparedArith...Official$' -benchmem -benchtime=5s -count=1 -cpuprofile /tmp/go-lua-vm-aggressive-profiles/<case>.pprof`

profile 使用 prepared 口径，避免编译、OpenLibs 和 State 初始化噪声。

| 用例 | benchmark | CPU 主要热点 |
| --- | ---: | --- |
| `PreparedArithAddLoopOfficial` | 16.47ms/op, 0 allocs/op | `executePreparedLuaClosureWithDebugNameTailFromArgs` 25.00% flat / 96.60% cum；`tryCachedIntegerAddArithmetic` 22.04%；`VM.Step` 16.42% flat / 60.50% cum；`executeForLoop` 16.12%；`NextPC` 3.85%；`SetCurrentPC` 2.66%；`isLuaHotNoPostProcessOpcode` 1.92% |
| `PreparedArithChainTempOfficial` | 33.61ms/op, 0 allocs/op | `executePreparedLuaClosureWithDebugNameTailFromArgs` 25.70% flat / 96.08% cum；`VM.Step` 13.22% flat / 56.92% cum；`tryCachedIntegerAddArithmetic` 10.89%；`tryCachedIntegerMulArithmetic` 8.81%；`executeForLoop` 8.69%；`tryCachedIntegerSubArithmetic` 7.59%；`NextPC` 4.28%；`SetCurrentPC` 3.43% |
| `PreparedArithMixLoopOfficial` | 28.27ms/op, 0 allocs/op | `executePreparedLuaClosureWithDebugNameTailFromArgs` 21.10% flat / 96.55% cum；`VM.Step` 14.07% flat / 65.35% cum；`tryCachedIntegerAddArithmetic` 8.82%；`integerFloorDiv` 7.80%；`tryCachedIntegerModArithmetic` 7.03% cum；`tryCachedIntegerMulArithmetic` 5.12%；`executeForLoop` 4.86%；`tryCachedIntegerIDivArithmetic` 12.53% cum；`SetCurrentPC` 2.69%；`NextPC` 2.05% |

结论：三条 arithmetic prepared 路径均为纯执行 CPU，热点仍集中在执行循环、`VM.Step` 分发、integer arithmetic cache 和 `FORLOOP`。`SetCurrentPC`、`NextPC`、`isLuaHotNoPostProcessOpcode` 在 tight loop 中也有稳定固定成本。该 profile 支持下一步优先做 Proto 预解码和 arithmetic superinstruction 原型；不建议继续重复已证伪的单个 opcode 局部字段/分支微调。

### 2026-07-03 Proto 预解码最小切口

实现：在 `runtime.VM` 内新增 VM-local 懒预解码缓存，绑定当前 `Proto`，不写入 `bytecode.Proto`，避免多个 State 并发执行同一 Proto 时共享可变 cache。预解码项保存原始 `Instruction`、`OpCode`、`A/B/C/Bx/sBx/Ax` 字段，以及 B/C 两侧 RK 形态；只有 integer 常量会缓存值，越界常量或非 integer 常量只记录形态并让后续 fast path 回退。

语义边界：

- 普通 `Step` 执行路径暂不读取该缓存，因此 parser/codegen/bytecode 输出、debug、hook、traceback、error path 和 coroutine/yield 语义不变。
- `BindPrototype(nil)` 会清空预解码；VM 池切换到不同 Proto 时丢弃旧缓存，避免相同 PC 误读不同 Proto 的字段。
- stripped chunk 不依赖 line/local/upvalue debug 信息；预解码只依赖 `Code` 与 `Constants`。

测试：新增 `TestVMDecodedInstructionCacheFollowsBoundProto` 和 `TestVMDecodedInstructionHandlesStrippedAndInvalidRK`，覆盖 Proto 切换、VM 复用边界、常量 RK、越界 RK 和 stripped chunk。

### 2026-07-03 `ADD; FORLOOP` superinstruction 原型

实现：在 `runtime.VM` 中新增 `ADD; FORLOOP` 预匹配表和 `TryExecuteAddForLoop`，API 执行循环仅在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 检查窗口允许时启用。fast path 只处理 integer ADD 和 integer numeric-for；任一操作数、控制槽、Proto 绑定或 PC 形态不匹配时回退普通 VM。

关键 guard：

- `FORLOOP` 的回跳目标必须正好是当前 `ADD`，只覆盖 `arith_add_loop` 的完整循环体。
- `arith_mix_loop` 末尾也存在相邻 `ADD; FORLOOP`，但它回跳到更早的 `MUL`；该形态已通过 `TestVMTryExecuteAddForLoopRejectsNonEntryAdd` 排除，留给后续完整 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` superinstruction。
- superinstruction 表只在存在真实匹配形态时分配；无匹配 Proto 记录为 nil，避免非目标函数每轮执行承担 fast path 调用。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringArithAddLoopOfficial` | `15.92 / 15.88 / 16.45 ms/op`，约 `59.5 KB/op`，`217 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `15.98 / 15.86 / 15.88 ms/op`，`0 allocs/op` |

对比激进分支基线约 `16.5 ms/op`，`arith_add_loop` 有小幅稳定收益。中间版本曾误匹配 `arith_mix_loop` 的末尾 `ADD; FORLOOP`，导致 mix 明显退化；收紧回跳目标后 mix 回到同轮环境约 `29.6-30.7 ms/op`。后续继续实现 `MUL; ADD; SUB; FORLOOP` 时必须使用完整循环体级别匹配，不能只看相邻 opcode。

## 优化路线

### 1. Proto 预解码

在 `Proto` 绑定或 VM 初始化阶段缓存不可变字节码派生信息，例如 opcode、A/B/C/Bx/sBx、RK 操作数形态、常量值和目标寄存器。执行期 fast path 读取预解码结构，减少每步重复指令解码和 RK 判断。

约束：

- 不修改原始 bytecode。
- 预解码数据必须与 `Proto` 生命周期绑定，不得被跨 Proto 误用。
- debug、traceback 和反汇编仍以原始 bytecode 为准。

### 2. Arithmetic Superinstruction

对官方 benchmark 中稳定出现的热循环形态做模式识别，并在无 hook、无 yield、无 coroutine continuation、无精确逐 PC 需求时使用紧凑执行路径：

- `arith_add_loop`: `ADD; FORLOOP`
- `arith_chain_temp`: `MUL; ADD; SUB; FORLOOP`
- `arith_mix_loop`: `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP`

每条 superinstruction 必须带运行期 guard。只要寄存器类型、常量形态、hook 状态、context 检查或 PC 语义不满足条件，立即回退普通 VM。

### 3. Table 连续数组写入预分配

探索基于 bytecode/data-flow 的 table 数组区预估，而不是再次调整扩容阈值。只考虑强约束形态：新建本地 table 后，在同一作用域中由 numeric-for 对正整数连续下标写入。

约束：

- table 在预分配前不能逃逸到外部函数、upvalue、全局或元方法可见路径。
- 不能改变 `next`、`#`、弱表、metatable 或错误路径行为。
- 任何数据流不确定时回退普通 table 扩容逻辑。

### 4. Guarded Lua CALL Fast Path

只扩展可证明安全的小函数调用：

- 固定参数、固定返回。
- 无 vararg、无 active hook、无 yield、无 coroutine continuation。
- 可保留等价 call depth、traceback 和错误路径。
- 不扩大非叶子 direct CALL，除非测试完整覆盖 debug name、`debug.getinfo`、hook、yield、coroutine continuation、traceback 和 error path。

### 5. 自递归固定签名路径

针对 `recursion` 的 Lua CALL 边界，探索同一 closure 自调用的固定签名 fast path。该路径必须保留 stack overflow 检查和错误栈语义，且只在无 hook、无 coroutine、无 yield 的普通执行场景启用。

## TODO

- [x] 跑激进分支基线：默认完整 benchmark 三轮、official-sized Go micro 矩阵、`BenchmarkPrepared*` 相关项。
- [x] 生成 `arith_add_loop`、`arith_chain_temp`、`arith_mix_loop` 的 CPU profile，确认当前热点仍集中在 VM.Step、整数算术和 FORLOOP。
- [x] 设计 Proto 预解码结构，明确字段、生命周期、VM 池复用安全边界和回退策略。
- [x] 实现最小 Proto 预解码，只服务 arithmetic hot path，不改普通解释器语义。
- [x] 为预解码补单测，覆盖 Proto 切换、VM 复用、常量 RK、寄存器越界和 stripped chunk。
- [x] 实现 `ADD; FORLOOP` superinstruction 原型。
- [x] 复跑 `BenchmarkDoStringArithAddLoopOfficial` 与 `BenchmarkPreparedArithAddLoopOfficial`，记录收益和误差。
- [ ] 实现 `MUL; ADD; SUB; FORLOOP` superinstruction 原型。
- [ ] 复跑 `BenchmarkDoStringArithChainTempOfficial` 与 `BenchmarkPreparedArithChainTempOfficial`，确认低于 3x 且无退化。
- [ ] 评估 `arith_mix_loop` 的 IDIV/MOD superinstruction 是否收益稳定；若收益不稳定，记录证伪并回退。
- [ ] 基于 profile 重新评估 `table_rw`，只在能证明 table 未逃逸时设计数组预分配。
- [ ] 基于 profile 重新评估 `recursion`，只在能证明自递归固定签名语义等价时设计 fast call。
- [ ] 每个生产优化 commit 后更新 `docs/BENCHMARK.md` 或本文件的结果摘要。

## 预期验收标准

### 正确性门禁

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

### 性能门禁

激进优化的单项提交必须至少满足：

- 对应目标 benchmark 有稳定收益，或明确记录证伪结果并不提交生产代码。
- 非目标官方规模 Go micro 不出现稳定退化。
- 默认完整 benchmark 主要路径保持低于 3x，重点观察 `arith_chain_temp`、`recursion`、`table_rw` 和 `closure_upvalue`。
- `BenchmarkPreparedRecursion` 的剩余 allocs/op 不再作为简单临时分配删除方向，除非能完整证明开放 upvalue cell、闭合值、debug/upvaluejoin/yield 语义不变。

### 语义回退标准

任一 fast path 满足以下条件必须回退普通 VM：

- debug hook 启用。
- 当前线程处于 coroutine continuation 或可能 yield 的路径。
- 需要精确逐 PC 更新 call frame。
- 操作数类型或常量形态不满足 fast path guard。
- 目标 Proto 或 VM 复用状态与预解码缓存不匹配。
- 错误路径需要补充 Lua 风格变量名、traceback 或 debug name。

## 风险与回滚

主要风险是 fast path 跳过普通 VM 后改变 debug、coroutine、traceback 或错误栈细节。因此每个激进优化都必须小步提交，并保证能通过单 commit revert 回滚。

不允许在本专项中引入以下手段：

- CGO 或 Lua C API。
- JIT 或运行期代码生成。
- 默认构建依赖外部动态库。
- 绕过官方测试失败继续提交。
- 在 `main`、`master`、`test` 保护分支直接提交。
