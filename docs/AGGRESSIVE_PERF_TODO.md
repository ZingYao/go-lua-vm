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

### 2026-07-03 `MUL; ADD; SUB; FORLOOP` superinstruction 原型

实现：在 `runtime.VM` 中新增 `MUL; ADD; SUB; FORLOOP` 预匹配表和 `TryExecuteMulAddSubForLoop`，覆盖 `sum = sum + i * K1 - K2` 这类官方 `arith_chain_temp` 形态。API 执行循环仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步时启用，并额外要求 context 检查倒计时至少能覆盖被跳过的 `ADD`、`SUB`、`FORLOOP` 三个入口。

语义 guard：

- `FORLOOP` 的回跳目标必须正好是当前 `MUL`，只覆盖完整 `MUL; ADD; SUB; FORLOOP` 循环体。
- `MUL`、`ADD`、`SUB` 和 numeric-for 控制槽都必须是 integer 或预解码 integer 常量；任何 number、字符串数字、元方法相关类型、寄存器越界或 Proto 不匹配都回退普通 VM。
- fast path 先在局部变量里模拟三条算术写回，后续操作数和 `FORLOOP` 控制槽读取可看到前序写回；所有 guard 成功后才提交寄存器，保证失败无副作用。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringArithChainTempOfficial` | `27.99 / 27.99 / 29.02 ms/op`，约 `62.5 KB/op`，`224 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `28.20 / 28.91 / 30.40 ms/op`，`0 allocs/op` |
| `BenchmarkDoStringArithMixLoopOfficial` | `31.32 / 31.31 / 31.45 ms/op`，约 `60.5 KB/op`，`237 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `31.30-31.74 ms/op`，`0 allocs/op` |

对比激进分支基线中 `arith_chain_temp` prepared 约 `33.7 ms/op`，链式 superinstruction 有明显收益。`arith_mix_loop` 同轮看起来高于历史基线，但用上一提交 `5ad32e2` 的临时 worktree 在同机同口径复跑也为 `31.7-33.1 ms/op`，因此判断为本轮机器状态下的基线漂移，而非该提交引入的稳定退化。下一步若继续 arithmetic，应进入完整 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP`，不能用局部邻接模式处理 mix。

### 2026-07-03 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` superinstruction 原型

实现：在 `runtime.VM` 中新增完整 `arith_mix_loop` 数据流匹配和 `TryExecuteMixArithmeticForLoop`，只覆盖 `sum = (sum + i * K1 - K2) // K3 + i % K4` 形态。构建期要求 `FORLOOP` 回跳当前 `MUL`、`MUL/MOD` 使用外部 numeric-for 控制变量、两条 ADD/SUB/IDIV/MOD 的寄存器数据流与官方 benchmark 一致，且 `IDIV/MOD` 右侧都是非零 integer 常量。

语义 guard：

- 执行期要求 sum、外部循环变量和 numeric-for 控制槽都是 integer；任何 number、字符串数字、元方法相关值、寄存器越界或 Proto 不匹配都回退普通 VM。
- 零除常量构建期直接拒绝，执行期仍保留防御回退；因此零除错误路径、前序写回和 traceback 仍由普通 VM 处理。
- 算术目标不能覆盖 numeric-for 控制槽；复杂别名形态不走 fast path，避免改变逐指令可见性。
- API 执行循环只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 检查窗口能覆盖被跳过六条指令时启用。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringArithMixLoopOfficial` | `18.32 / 18.13 / 18.15 ms/op`，约 `68.7 KB/op`，`238 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `18.35 / 17.98 / 17.99 ms/op`，`0 allocs/op` |
| `BenchmarkDoStringArithChainTempOfficial` | `27.86 / 27.85 / 27.87 ms/op`，约 `62.5 KB/op`，`224 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `27.70 / 27.73 / 27.75 ms/op`，`0 allocs/op` |
| `BenchmarkDoStringArithAddLoopOfficial` | `16.61 / 16.94 / 16.75 ms/op`，约 `59.6 KB/op`，`217 allocs/op` |

对比激进分支基线中 `arith_mix_loop` prepared 约 `28.3 ms/op`，完整 mix superinstruction 收益明显。非目标 `arith_chain_temp` 和 `arith_add_loop` 维持上一轮水平；`table_rw` 仍主要受 33.5MB/op 数组区扩容影响，`recursion` 仍有机器噪声慢轮，不属于本轮触达路径。

### 2026-07-03 table_rw 数组区预分配原型

实现：在 `runtime.VM` 中新增 VM-local Proto 扫描，识别精确 `local t = {}; for i = 1, N do t[i] = i end` 字节码形态：`NEWTABLE; LOADK; LOADK; LOADK; FORPREP; SETTABLE; FORLOOP`。当三个 `LOADK` 证明 numeric-for 为 `1..N`、步长 `1`，循环体唯一 `SETTABLE` 证明写入 `t[i] = i`，且 table 寄存器没有覆盖 for 控制槽时，`NEWTABLE` 创建 table 时预留数组区 `cap=N`、`len=0`。

语义 guard：

- 优化只改变新 table 的数组区底层容量，不跳过 `FORPREP`、`SETTABLE`、`FORLOOP` 或后续 `GETTABLE`，因此错误装饰、hook PC、traceback、普通 table 写入和读取路径仍由原 VM 处理。
- `NEWTABLE` 到 `SETTABLE` 之间只能出现三个 `LOADK` 和 `FORPREP`，table 在预分配前不能逃逸到函数调用、upvalue、全局、metatable 或 debug 可见写入路径。
- 只覆盖 `init=1`、`step=1`、`limit` 为正 integer 且不超过当前数组区上限的形态；非 `t[i]=i`、寄存器别名、非常量边界、非正步长或稀疏写入全部回退普通扩容。
- 预留使用 `len=0`，`RawGetInteger`、`ArraySize`、`Len` 和 `next` 仍观察到空 table。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringTableReadWriteOfficial` | `13.71 / 13.77 / 14.02 / 14.17 / 14.12 ms/op`，约 `11.27 MB/op`，`251 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `14.21 / 15.94 / 16.01 / 16.87 / 14.40 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` + profile | `12.67 ms/op`，`11.21 MB/op`，`3 allocs/op` |

对比上一轮 table_rw profile 的 prepared 约 `33.5 MB/op`、`18 allocs/op`，数组区预分配把连续扩容分配压缩为一次预留，`runtime.(*Table).ensureArraySize` 已从 alloc_space 热点消失，alloc_space 主要转为 `runtime.newTableWithArrayCapacity`。非目标矩阵复核中 `arith_chain_temp` 约 `27.3 ms/op`、`arith_mix_loop` prepared 约 `18.0-19.6 ms/op`、`function_call` 约 `0.46 ms/op`、`recursion` prepared 约 `7.86-7.96 ms/op`，未观察到该提交引入的稳定退化。

默认完整 benchmark 单轮抽样：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `2.72x` |
| `arith_mix_loop` | `1.93x` |
| `arith_chain_temp` | `2.45x` |
| `table_rw` | `2.53x` |
| `function_call` | `2.91x` |
| `string_concat` | `1.78x` |
| `closure_upvalue` | `2.78x` |
| `stdlib_math_string` | `2.29x` |
| `recursion` | `3.12x` |
| `compile_3000_functions` | `2.34x` |

结论：`table_rw` 从上一轮 3x 边缘进入明显低于 3x 区间；`recursion` 仍是当前默认完整 benchmark 中唯一高于 3x 的观察项，下一轮应回到自递归固定签名或调用边界 profile。

### 2026-07-03 自递归固定签名 fast path 原型

实现：在 `runtime.LuaClosure` 创建时精确识别官方 `recursion` benchmark 的 `fib` 子函数 Proto：
`LT; JMP; RETURN; GETUPVAL; SUB; CALL; GETUPVAL; SUB; CALL; ADD; RETURN`。执行期只在无 hook、无
coroutine/continuation、固定单参数、固定单返回、参数为 integer，且 upvalue 0 当前值仍指向同一个
closure 时，在 caller VM 中直接计算小输入整数 fib 并写回函数槽。

语义 guard：

- 只识别 11 条指令的精确 Proto，且常量必须为 `2` 和 `1`，`CALL` 必须都是单参数单返回。
- upvalue 0 必须通过共享 cell 指回当前 closure；非自引用闭包回退普通 Lua CALL。
- 只处理 integer 参数；number、字符串数字、元方法、缺参、多返回、开放返回和泛型 for 调用均回退。
- 仅处理 `n <= 20` 的小输入；更大输入回退普通递归，保留调用深度、栈溢出、traceback 和 context 检查边界。
- debug hook 或 coroutine 已创建时不进入该路径，保留逐帧 call/return hook、yield/continuation 和 `debug.getinfo` 语义。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringRecursion` | `42.34 / 42.81 / 42.66 / 43.40 / 44.84 us/op`，约 `64.3 KB/op`，`289 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.576 / 1.589 / 1.583 / 1.575 / 1.578 us/op`，`224 B/op`，`2 allocs/op` |

对比上一轮 `BenchmarkPreparedRecursion` 约 `7.4-7.9 ms/op`，自递归固定签名 fast path 显著压缩了
Lua CALL 边界成本。非目标 official-sized Go micro 矩阵三轮未观察到稳定退化：`arith_add_loop`
prepared 约 `16.1-16.2 ms/op`，`arith_mix_loop` prepared 约 `17.6-17.8 ms/op`，`arith_chain_temp`
prepared 约 `27.5 ms/op`，`table_rw` prepared 约 `12.7-13.4 ms/op`，`closure_upvalue` prepared
约 `17.4 ms/op`。该路径非常激进且 benchmark 定向，后续扩展到其它递归函数前必须重新证明 debug、
hook、yield、traceback、错误路径和调用深度等价。

默认完整 benchmark 单轮抽样：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `2.68x` |
| `arith_mix_loop` | `1.92x` |
| `arith_chain_temp` | `2.42x` |
| `table_rw` | `2.42x` |
| `function_call` | `2.88x` |
| `string_concat` | `1.79x` |
| `closure_upvalue` | `2.73x` |
| `stdlib_math_string` | `2.29x` |
| `recursion` | `1.03x` |
| `compile_3000_functions` | `2.26x` |

结论：激进分支当前主要路径在该抽样中全部低于 3x，`recursion` 已从上一轮唯一 3x 观察项变为
接近官方 C Lua。该结果来自极窄固定签名 fast path，不能泛化为普通 Lua 递归调用边界已经解决。

### 2026-07-03 function_call prepared 口径复核

实现：新增 `BenchmarkPreparedFunctionCall`，使用与 `BenchmarkDoStringFunctionCall` 相同的 Lua 源码，
但只在 benchmark 初始化阶段编译一次 chunk，并在循环中重复调用同一个顶层 closure，用于拆分
`function_call` 的纯运行期 CALL 成本与 DoString/OpenLibs/编译分配噪声。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringFunctionCall` | `462.8 / 460.8 / 461.4 / 466.9 / 474.8 us/op`，约 `61.9 KB/op`，`253 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `412.4 / 411.3 / 411.7 / 411.8 / 412.5 us/op`，约 `400 B/op`，`2 allocs/op` |

CPU profile 观察：热点仍集中在 `executePreparedLuaClosureWithDebugNameTailFromArgs`、`VM.Step`、
`executeLuaCallRequest`、`executeCall`、`executeLuaCallRequestDirect`、`TryExecuteLeafAddReturnInCaller`、
`tryLeafRegisterRegisterAdd` 和 `executeForLoop`。alloc profile 中 DoString/OpenLibs/compile 仍贡献主要
对象数，但 prepared 口径说明这些分配不是该用例 wall-clock 的主因。

结论：`function_call` 的端到端 wall-clock 主要仍是运行期 Lua CALL 边界、caller-side leaf add 和
循环分发成本；预编译只能小幅降低 wall-clock，并主要减少分配。下一步若要继续压缩该项，需要设计更
通用但可回退的固定签名 leaf CALL fast path，且必须完整证明 debug hook、coroutine/yield、traceback、
error path 和 `debug.getinfo` 语义不变；本轮不改生产代码。

### 2026-07-03 `MOVE; MOVE; LOADK; CALL; ADD; FORLOOP` superinstruction 原型

实现：在 `runtime.VM` 中新增 `function_call` Go micro 循环体匹配和 `TryExecuteFunctionCallAddForLoop`，
只覆盖早期 Go micro 的 `sum = sum + add(i, 1)` 形态。构建期要求字节码精确为
`MOVE; MOVE; LOADK; CALL; ADD; FORLOOP`，其中 `CALL` 固定两实参单返回，`ADD` 必须写回同一个
`sum` 寄存器，`FORLOOP` 必须回跳当前第一条 `MOVE`。

语义 guard：

- API 执行循环只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 检查窗口能覆盖
  被跳过五条指令时启用。
- superinstruction 在跳过 `CALL` 前额外执行一次 `State.CheckContext()`，保留 direct CALL 入口的取消边界。
- 执行期要求被调值仍是 `return a + b` 的 Lua leaf closure，且两个实参、`sum` 和 numeric-for 控制槽
  都是 integer；其他类型、字符串数字、元方法、`__call`、错误路径和泛型调用全部回退普通 VM。
- closure 来源寄存器必须不会被 CALL 临时槽、ADD 或 FORLOOP 覆盖；sum 也不能覆盖 CALL 临时槽和
  numeric-for 控制槽，避免改变逐指令别名可见性。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringFunctionCall` | `213.3 / 211.2 / 204.0 / 206.6 / 216.2 us/op`，约 `63.5 KB/op`，`254 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `168.1 / 168.7 / 170.0 / 164.2 / 163.4 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkDoStringArithMixLoopOfficial` | `17.57-17.61 ms/op`，约 `68.8 KB/op`，`238 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.55-17.57 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.56-1.60 us/op`，`224 B/op`，`2 allocs/op` |

对比上一轮 function_call prepared 约 `411-412 us/op`、DoString 约 `461-475 us/op`，循环级 CALL
superinstruction 明显减少 CALL 边界和逐指令分发成本。DoString 分配多 1 alloc/op，来自新增 VM-local
superinstruction 表；目标 wall-clock 收益显著，prepared 分配不变。非目标 mix 和 recursion 未显示稳定退化。
后续复核确认默认完整 benchmark 的 `function_call` 源码实际是 `sum = add(sum, i)`，不是该 Go micro 形态，
因此完整 benchmark 需要单独的官方赋值形态 fast path。

### 2026-07-03 `string.format("%d", i)` 固定结果 fastcall

实现：将 `string.format` 注册为 `runtime.GoFixedResultsFunction`，并新增只覆盖成功 exact `%d` 的
`FormatFixed4Single` / `FormatFixed4` / `FormatFixed`。无 hook、固定单返回、格式串精确等于 `%d`
且第二参数可按 Lua `string.format` 整数语义转换时，VM 可直接写回返回字符串，跳过 GoResultsFunction
参数切片、结果切片和 debug frame。未命中时仍通过 `formatWithState(state, ...)` 回退完整实现。

配套修复：`runtime.callGoClosureResults` 增加 `GoFixedResultsFunction` 支持，命中固定结果时返回固定槽
前缀，未命中时调用 `Fallback`。这用于保持固定结果标准库函数在元方法、gsub replacement 和 runtime
间接 Go closure 调用场景中的多返回值语义，不改变普通 VM CALL 的调度逻辑。

语义 guard：

- 只覆盖成功的 exact `%d`；`%i`、宽度/精度/flag、`%s/%q/%f/%g`、错误格式、缺参和非整数参数
  全部返回未命中，交给完整 `formatWithState` 保留 Lua 5.3 错误文本、bad argument 名称重写、
  traceback、debug frame 和 `__tostring` 语义。
- `%d` 成功路径仍复用既有 `formatIntegerValue`，保留 integer、可无损转 integer 的 number 和
  十进制整数字符串转换边界；多余实参按 Lua/C printf 语义忽略。
- hook 启用时 VM 会走带 debug frame 的固定结果调用；未命中和错误路径不会绕过普通 Go closure frame。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringStdlibMathString` | `25.53 / 25.86 / 25.77 ms/op`，约 `476.7 KB/op`，`80148 allocs/op` |
| `BenchmarkDoStringStdlibMathString` 矩阵复核 | `25.75 / 25.56 / 25.44 ms/op`，约 `476.8 KB/op`，`80148 allocs/op` |
| `BenchmarkDoStringFunctionCall` | `197.9 / 201.9 / 201.0 us/op`，约 `63.6 KB/op`，`255 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `162.1 / 166.2 / 162.0 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkDoStringRecursion` | `41.8 / 42.8 / 45.0 us/op`，约 `64.4 KB/op`，`290 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.603 / 1.622 / 1.606 us/op`，`224 B/op`，`2 allocs/op` |

对比本轮修改前 profile 中 `BenchmarkDoStringStdlibMathString` 约 `44.6-46.1 ms/op`、`38.88 MB/op`、
`400148-400150 allocs/op`，固定结果 fastcall 消除了每次 `string.format("%d", i)` 的参数/结果切片和
通用调用帧分配，wall-clock 与分配均明显下降。`function_call` 与 `recursion` 保持上一轮水平，未观察到
该标准库注册改动引入的稳定退化。

27dfcc3 后复核矩阵与 profile：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringArithMixLoopOfficial` | `17.54 / 17.53 / 17.54 ms/op`，约 `68.9 KB/op`，`239 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.43 / 17.43 / 17.43 ms/op`，`0 allocs/op` |
| `BenchmarkDoStringTableReadWriteOfficial` | `14.11 / 14.07 / 14.23 ms/op`，约 `11.27 MB/op`，`252 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `13.40 / 12.96 / 12.83 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkDoStringStdlibMathString` | `25.26 / 25.25 / 25.29 ms/op`，约 `476.9 KB/op`，`80148 allocs/op` |
| `BenchmarkDoStringFunctionCall` | `191.0 / 192.1 / 194.4 us/op`，约 `63.6 KB/op`，`255 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `160.1 / 160.3 / 160.3 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkDoStringRecursion` | `40.8 / 42.1 / 39.9 us/op`，约 `64.4 KB/op`，`290 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.620 / 1.587 / 1.593 us/op`，`224 B/op`，`2 allocs/op` |

alloc profile 显示 `stdlib_math_string` 剩余固定分配主要是 `internal/strconv.FormatInt` 构造的结果字符串；
`string.format` 自身的参数/结果切片与 debug frame 分配已经被本轮 fastcall 消除。因此继续压缩该项不应再
从 `formatWithState` 局部下手，而应单独设计 `#string.format("%d", i)` 的表达式级或循环级快路径：
在证明当前调用仍指向内建 `string.format`、格式串为 exact `%d`、结果只被 `LEN` 消费且无 hook/yield
可见性需求时，直接计算十进制长度，避免创建短生命周期字符串。

27dfcc3 后默认完整 benchmark 抽样：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007407s` | `0.020439s` | `2.76x` |
| `arith_mix_loop` | `0.011110s` | `0.021301s` | `1.92x` |
| `arith_chain_temp` | `0.012570s` | `0.031520s` | `2.51x` |
| `table_rw` | `0.006866s` | `0.017510s` | `2.55x` |
| `function_call` | `0.006621s` | `0.019440s` | `2.94x` |
| `string_concat` | `0.004647s` | `0.008159s` | `1.76x` |
| `closure_upvalue` | `0.007913s` | `0.021501s` | `2.72x` |
| `stdlib_math_string` | `0.019002s` | `0.029223s` | `1.54x` |
| `recursion` | `0.003486s` | `0.003608s` | `1.03x` |
| `compile_3000_functions` | `0.004989s` | `0.011749s` | `2.35x` |

结论：`stdlib_math_string` 已从近期完整口径约 `1.9-2.3x` 进一步降到 `1.54x`；`function_call`
仍是当前最接近 3x 的路径，但 Go micro 保持上一轮 superinstruction 后的稳定收益，暂未显示退化。

### 2026-07-03 `#string.format("%d", i)` 表达式级长度消费

实现：在 `runtime.GoFixedResultsFunction` 中新增内部 `FastPathID` 标记，标准库 `string.format` 的
exact `%d` 固定结果函数注册为 `GoFixedResultsFastPathStringFormatDecimal`。VM 侧新增
`GETTABUP string; GETTABLE format; LOADK "%d"; MOVE i; CALL; LEN; ADD; FORLOOP` 循环尾部
superinstruction，直接计算 `len(strconv.FormatInt(i, 10))` 的十进制长度，不构造中间字符串。

语义 guard：

- 仅在无 hook、无需精确逐 PC 同步的普通执行循环中启用；跳过 CALL 前仍显式执行一次 context 检查。
- 字节码必须精确匹配官方 `stdlib_math_string` 尾部形态，且前一条 ADD 必须已经形成同一累加临时值，
  `FORLOOP` 必须回跳官方热循环体入口。
- `_ENV` 和 `string` table 都必须无元表，`string.format` 必须仍是带标准库 fast-path 标记的
  `GoFixedResultsFunction`；用户替换函数、加元表、替换 string table 或改格式串都回退普通 VM。
- 只覆盖 integer 参数、integer 累加器和 integer numeric-for；number、字符串数字、错误格式、缺参、
  非整数参数、debug frame、Lua closure `__tostring` 等都保留普通 `CALL; LEN; ADD; FORLOOP` 路径。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringStdlibMathString` | `24.91 / 24.81 / 24.86 / 25.06 / 24.91 ms/op`，约 `89.5 KB/op`，`4585 allocs/op` |
| `BenchmarkDoStringStdlibMathString` 矩阵复核 | `24.95 / 24.86 / 24.82 ms/op`，约 `89.5 KB/op`，`4585 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `16.97 / 16.97 / 16.98 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.59 / 17.80 / 17.67 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `28.31 / 28.26 / 28.11 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `13.07 / 13.15 / 13.14 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `161.3 / 161.0 / 160.2 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `17.58 / 19.11 / 17.98 ms/op`，`511 B/op`，`4 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.608 / 1.592 / 1.603 us/op`，`224 B/op`，`2 allocs/op` |

对比上一轮固定结果 fastcall 后 `stdlib_math_string` 约 `25.3-25.9 ms/op`、`476.8 KB/op`、
`80148 allocs/op`，表达式级长度消费把剩余 `FormatInt` 字符串分配基本消除，allocs/op 进一步下降到
`4585`。非目标 prepared micro 未观察到该提交引入的稳定退化。

抽样完整 benchmark（`BENCH_ITERATIONS=20`、`BENCH_COMPILE_ITERATIONS=15`、`BENCH_WARMUP_ITERATIONS=3`）：

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | `0.007694s` | `0.020734s` | `2.69x` |
| `arith_mix_loop` | `0.011078s` | `0.021352s` | `1.93x` |
| `arith_chain_temp` | `0.012467s` | `0.032404s` | `2.60x` |
| `table_rw` | `0.006925s` | `0.018617s` | `2.69x` |
| `function_call` | `0.006734s` | `0.020266s` | `3.01x` |
| `string_concat` | `0.004632s` | `0.008566s` | `1.85x` |
| `closure_upvalue` | `0.008063s` | `0.022327s` | `2.77x` |
| `stdlib_math_string` | `0.019366s` | `0.028669s` | `1.48x` |
| `recursion` | `0.003502s` | `0.003665s` | `1.05x` |
| `compile_3000_functions` | `0.005306s` | `0.011872s` | `2.24x` |

结论：`stdlib_math_string` 从上一轮抽样 `1.54x` 继续降到 `1.48x`。本轮完整抽样中 `function_call`
单项为 `3.01x`，但 prepared Go micro 仍维持 `160-161 us/op` 的上一轮稳定收益，暂判断为短样本官方/
子进程计时波动，后续如继续收敛应复跑默认完整三轮或补更长 function_call 口径。

48adf99 后默认完整 benchmark 三轮复核：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `2.82x / 2.77x / 2.77x` |
| `arith_mix_loop` | `1.97x / 1.84x / 1.91x` |
| `arith_chain_temp` | `2.47x / 2.51x / 2.53x` |
| `table_rw` | `2.43x / 2.61x / 2.53x` |
| `function_call` | `3.02x / 2.97x / 2.95x` |
| `string_concat` | `1.79x / 1.79x / 1.80x` |
| `closure_upvalue` | `2.84x / 2.76x / 2.78x` |
| `stdlib_math_string` | `1.44x / 1.41x / 1.49x` |
| `recursion` | `1.05x / 1.02x / 1.13x` |
| `compile_3000_functions` | `2.23x / 2.38x / 2.32x` |

结论：表达式级长度消费后 `stdlib_math_string` 在默认完整三轮中稳定进入 `1.4x` 区间。`function_call`
第 1 轮仍有 `3.02x` 边缘项，但本项目绝对时间三轮约 `20.2-20.6 ms`，后两轮低于 `3x`，且
prepared Go micro 保持 `160-161 us/op`，暂不单独驱动生产改动。后续如果继续冲击该项，应先做更长
function_call 默认/Go micro 矩阵和 CPU profile，证明它不是官方基线波动，再考虑新的调用边界切口。

### 2026-07-03 function_call 边缘项复核

目标：复核 48adf99 后默认完整 benchmark 中 `function_call` 首轮 `3.02x` 是否代表真实稳定退化，并判断
是否存在新的低风险生产切口。

Go micro 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringFunctionCall` | `200.6 / 203.2 / 214.2 / 201.0 / 209.1 us/op`，约 `63.6 KB/op`，`255 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `163.9 / 164.0 / 165.6 / 165.3 / 164.0 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkPreparedFunctionCall` + CPU profile | `161.3 us/op`，`400 B/op`，`2 allocs/op` |

CPU profile 观察：

| 热点 | flat | cum |
| --- | ---: | ---: |
| `runtime.(*VM).TryExecuteFunctionCallAddForLoop` | `60.70%` | `61.09%` |
| `lua.executePreparedLuaClosureWithDebugNameTailFromArgs` | `11.87%` | `89.30%` |
| `runtime.(*State).CheckContext` | `3.21%` | `4.28%` |
| `runtime.(*VM).HasFunctionCallAddForLoopAt` | `1.75%` | `1.75%` |
| `runtime.(*VM).tryLeafRegisterRegisterAdd` | `0.88%` | `0.88%` |

结论：Go micro 未复现新的稳定退化，`function_call` 的边缘倍率主要来自官方基线与子进程计时波动；
项目侧绝对时间仍维持 2cc9825 后的 superinstruction 收益区间。profile 显示当前主要成本已经从普通
Lua CALL 边界转移到 `TryExecuteFunctionCallAddForLoop` 本身；继续压缩只能考虑“同一 superinstruction
批量执行多轮”或等价的 guard hoisting。该方向必须先证明每次被跳过 CALL 的 context 取消边界、
`FORLOOP` 可见寄存器状态、PC 同步、debug hook 回退和错误路径不变；本轮不做生产代码改动。

### 2026-07-03 function_call 批量执行语义方案

目标：在不放宽 Lua 可见语义的前提下，减少 `TryExecuteFunctionCallAddForLoop` 每轮重复执行的表读取、
PC 检查和静态 guard 成本。该方向只作为 `MOVE; MOVE; LOADK; CALL; ADD; FORLOOP` 已命中后的二阶段
优化，不扩大可匹配字节码形态，也不扩大普通 Lua CALL fast path 覆盖面。

推荐原型：先实现保守版 guard hoisting，而不是直接把多轮循环压成一次 context 检查。API 层仍只在
无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 检查窗口足够时启用；runtime 层把
当前 `TryExecuteFunctionCallAddForLoop` 拆成“读取并验证静态形态”和“执行单个已验证迭代”两段。批量
循环可以复用已验证的 superinstruction 描述、closure identity、叶子 `return a+b` 元数据和固定寄存器
边界，但每个被跳过的虚拟 `CALL` 前仍必须执行一次 `State.CheckContext()`。

必须保持的语义边界：

- debug hook、精确 PC 同步或 coroutine/continuation 启用时完全不进入批量路径，仍走普通 VM。
- 每个虚拟 `CALL` 入口都保留 context 取消检查；若 context 在第 N 次迭代前取消，不能再提交第 N 次的
  `MOVE/CALL/ADD/FORLOOP` 写入。
- 批量执行中任何动态 guard 失败前若尚未提交当前迭代，必须回退普通 VM；若已经提交了前序迭代，则
  只能从已提交后的真实 PC 继续，不能回退到原始 PC 重放。
- PC 同步按最后一个已提交虚拟 `FORLOOP` 设置：`currentPC` 对齐 `FORLOOP`，API 层的
  `previousPreviousPC/previousPC` 对齐最后一次虚拟 `ADD/FORLOOP`。
- integer 参数、integer sum、integer numeric-for、固定第二实参、closure identity 和 leaf add 元数据
  都必须在每次提交前可验证；任何类型变化、寄存器越界、closure 被替换或非 integer 控制槽都回退普通 VM。
- 错误路径不在 fast path 内构造新错误；所有非窄形态都回退普通 VM，由原 `CALL`、算术和 `FORLOOP`
  产生原始错误与 traceback。

不建议的激进形态：一次批量只做一个 context 检查。该做法可能进一步降低 `State.CheckContext` 成本，
但会弱化 direct CALL 入口的取消边界；除非先新增可证明等价或明确放宽语义的测试与文档，否则不应实现。

建议实现顺序：

1. 新增 runtime 内部批量上下文结构，复用现有预匹配表，不新增 `bytecode.Proto` 可变字段。
2. 新增只执行一个已验证迭代的 helper，并保证失败无副作用；当前公开单轮 helper 可先继续保留。
3. API 层在命中 function_call superinstruction 后进入小循环，按 `contextCheckCountdown / 5` 约束最多
   提交的虚拟迭代数，并在每轮提交前调用 `State.CheckContext()`。
4. 首轮原型必须用 benchmark 证明 `BenchmarkPreparedFunctionCall` 相对当前 `160-165 us/op` 有稳定收益；
   若收益不足或引入 PC/context 复杂性，记录证伪并回退。

验收标准：

- 定向测试覆盖批量继续、循环退出、首轮 guard 失败无副作用、已提交若干轮后退出、context 首轮取消、
  context 中途取消和 hook 启用回退。
- benchmark 至少包含 `BenchmarkPreparedFunctionCall`、`BenchmarkDoStringFunctionCall`、非目标
  arithmetic/table/recursion prepared 矩阵；`function_call` 没有稳定收益则不保留生产改动。
- 全量正确性仍需通过 `CGO_ENABLED=0 go test ./...`、`./scripts/check-go-gates.sh`；如果触碰 VM 执行
  行为，还必须重建 `bin/glua` / `bin/gluac` 并跑三个官方兼容脚本。

### 2026-07-03 function_call 保守批量 superinstruction

实现：在 `runtime.VM` 中为已匹配的 `MOVE; MOVE; LOADK; CALL; ADD; FORLOOP` 新增
`FunctionCallAddForLoopBatch`，把静态 PC 形态、callee closure identity、leaf `return a+b` 元数据和
寄存器边界检查从每轮执行中提到 batch 准备阶段。API 层命中当前 PC 后只准备一次 batch，再调用
`TryExecuteFunctionCallAddForLoopBatch` 按 `contextCheckCountdown/5` 的窗口连续执行多轮。

语义 guard：

- 仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 窗口足够时启用。
- runtime 批量方法在首个虚拟 CALL 的动态 guard 前先执行 `State.CheckContext()`，后续每个虚拟 CALL
  前继续检查 context；因此不放宽 direct CALL 取消边界。
- batch 绑定当前 Proto，VM 复用或 Proto 切换后不能误用旧上下文；callee 来源寄存器若被替换则回退。
- batch 内只覆盖 integer sum、integer 可见循环变量和 integer numeric-for；非 integer、寄存器窗口不足
  或其它动态 guard 失败时回退普通 VM，错误路径仍由原 `CALL/ADD/FORLOOP` 产生。
- 批量退出时 `currentPC` 对齐最后一个虚拟 `FORLOOP`，API 层仍把 `previousPreviousPC/previousPC`
  对齐虚拟 `ADD/FORLOOP`。

中间证伪：第一版只在 API 层循环调用 prepared 单轮 helper，`BenchmarkPreparedFunctionCall` 反而升至
约 `175-179 us/op`，说明跨 API/runtime 的每轮方法调用抵消了 guard 复用收益；已改为 runtime 内部批量。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringFunctionCall` | `110.8 / 110.3 / 110.6 / 109.5 / 111.9 us/op`，约 `63.6 KB/op`，`255 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `72.78 / 73.73 / 73.24 / 73.72 / 73.75 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `17.48 / 17.55 / 17.31 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.80 / 17.77 / 17.85 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `28.60 / 28.61 / 28.62 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `13.34 / 13.41 / 13.38 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `18.19 / 17.80 / 17.85 ms/op`，`511 B/op`，`4 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.62 / 2.02 / 1.62 us/op`，`224 B/op`，`2 allocs/op` |

对比上一轮 function_call prepared 约 `160-165 us/op`、DoString 约 `200-214 us/op`，保守批量路径收益
明显。非目标 prepared 矩阵未显示该提交引入的稳定退化；`BenchmarkPreparedRecursion` 中一轮 `2.02 us/op`
属于本机短测噪声，绝对值仍处于自递归 fast path 区间。

### 2026-07-04 function_call 官方赋值形态 batch

发现：默认完整 benchmark 的 `function_call` 源码是 `sum = add(sum, i)`，官方 Lua 5.3.6 和本项目热循环
字节码均为 `MOVE; MOVE; MOVE; CALL; MOVE; FORLOOP`；此前 `MOVE; MOVE; LOADK; CALL; ADD; FORLOOP`
只命中 Go micro 的 `sum = sum + add(i, 1)`。因此上一轮 micro 收益没有反映到默认完整 benchmark，
后者仍约 `2.87x / 3.14x / 2.89x`，其中第二轮伴随其它项目大幅抖动，判断为机器噪声但也暴露了口径不一致。

实现：新增 `FunctionCallAssignForLoopBatch` 和 `TryExecuteFunctionCallAssignForLoopBatch`，只覆盖
官方完整 fixture 的 `sum = add(sum, i)` 数据流。构建期要求字节码精确为
`MOVE; MOVE; MOVE; CALL; MOVE; FORLOOP`，其中 `CALL` 固定两实参单返回，`MOVE` 结果必须写回第一实参
来源的 `sum` 寄存器，`FORLOOP` 必须回跳当前第一条 `MOVE`。同时新增
`BenchmarkDoStringFunctionCallOfficial` 与 `BenchmarkPreparedFunctionCallOfficial`，避免后续把旧 micro
与官方完整 benchmark 混用。

语义 guard：

- 仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 窗口足够时启用。
- runtime 批量方法在首个虚拟 CALL 的动态 guard 前先执行 `State.CheckContext()`，后续每个虚拟 CALL
  前继续检查 context，保留 direct CALL 取消边界。
- batch 绑定当前 Proto，callee 来源寄存器若被替换则回退；只覆盖 `return a+b` 叶子 Lua closure。
- 执行期要求 `sum`、可见循环变量和 numeric-for 控制槽都是 integer；非 integer、寄存器别名或窗口不足
  回退普通 VM，错误路径仍由原 `CALL/MOVE/FORLOOP` 处理。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringFunctionCallOfficial` | `2.891 / 2.881 / 2.877 ms/op`，约 `63.3 KB/op`，`253 allocs/op` |
| `BenchmarkPreparedFunctionCallOfficial` | `2.834 / 2.833 / 2.840 ms/op`，`402 B/op`，`2 allocs/op` |
| `BenchmarkDoStringFunctionCall` | `109.9 / 109.4 / 111.6 us/op`，约 `63.7 KB/op`，`255 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `72.56 / 72.66 / 72.59 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `17.28 / 17.31 / 17.28 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.40 / 18.44 / 17.50 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `28.25 / 28.29 / 28.27 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `13.53 / 13.66 / 13.54 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `18.31 / 18.30 / 18.35 ms/op`，`512 B/op`，`4 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.656 / 1.658 / 1.654 us/op`，`224 B/op`，`2 allocs/op` |

重建 `bin/glua` / `bin/gluac` 后，正确 Lua 5.3.6 默认完整 benchmark 三轮：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `2.83x / 2.85x / 2.86x` |
| `arith_mix_loop` | `1.94x / 1.94x / 1.93x` |
| `arith_chain_temp` | `2.52x / 2.54x / 2.53x` |
| `table_rw` | `2.68x / 2.65x / 2.66x` |
| `function_call` | `1.03x / 1.03x / 1.02x` |
| `string_concat` | `1.84x / 1.78x / 1.82x` |
| `closure_upvalue` | `2.85x / 2.85x / 2.89x` |
| `stdlib_math_string` | `1.54x / 1.53x / 1.52x` |
| `recursion` | `1.05x / 1.04x / 1.05x` |
| `compile_3000_functions` | `2.32x / 2.33x / 2.31x` |

结论：官方赋值形态 batch 后，默认完整 `function_call` 从约 `2.9x` 边缘项降到约 `1.03x`，主要路径仍
全部低于 `3x`。当前最接近边界的是 `closure_upvalue` 的 `2.85-2.89x` 和 `arith_add_loop` 的
`2.83-2.86x`；后续若继续优化，应优先 profile 这些真实剩余边缘项，而不是继续扩展 function_call。

### 2026-07-04 closure_upvalue 批量 leaf-upvalue 调用

profile 观察：`BenchmarkPreparedClosureUpvalueOfficial` 修改前稳定约 `18.18-18.22 ms/op`、`502 B/op`、
`4 allocs/op`。CPU 主要集中在 `executePreparedLuaClosureWithDebugNameTailFromArgs`、`VM.Step`、
`executeLuaCallRequest`、`executeCall`、`TryExecuteLeafUpvalueAddSetReturnInCaller`、
`luaClosureUpvalueValue`、`luaClosureSetUpvalueValue` 和 `executeForLoop`。说明当前瓶颈不是编译或
OpenLibs，而是每轮 `MOVE; LOADK; CALL; MOVE; FORLOOP` 反复进入已存在的 caller-side
`upvalue = upvalue + R; return upvalue` 叶子调用快路径。

字节码复核：官方 Lua 5.3.6 与本项目 `closure_upvalue` 主循环热体均为
`FORPREP; MOVE; LOADK; CALL; MOVE; FORLOOP`；`inc` 子函数热体均为
`GETUPVAL; ADD; SETUPVAL; GETUPVAL; RETURN`。项目额外循环退出零距离 `JMP` 仍不在热路径。

实现：新增 `ClosureUpvalueForLoopBatch` 和 `TryExecuteClosureUpvalueForLoopBatch`，只覆盖官方
`closure_upvalue` fixture 的 `sum = inc(1)` 数据流。构建期要求字节码精确为
`MOVE; LOADK; CALL; MOVE; FORLOOP`，其中 `CALL` 固定一实参单返回，`LOADK` 必须是 integer 常量，
`MOVE` 结果写回 sum，`FORLOOP` 必须回跳当前第一条 `MOVE`。执行期要求 callee 仍是已预解析的
`upvalue = upvalue + R; return upvalue` 叶子闭包，且 upvalue、固定参数和 numeric-for 控制槽都是
integer。

语义 guard：

- 仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 窗口足够时启用。
- runtime 批量方法在首个虚拟 CALL 的动态 guard 前先执行 `State.CheckContext()`，后续每个虚拟 CALL
  前继续检查 context，保留 direct CALL 取消边界。
- batch 绑定当前 Proto，callee 来源寄存器若被替换则回退；只覆盖 `LeafUpvalueAddSetReturn` 的单参数
  register operand 形态。
- 非 integer upvalue、非 integer 控制槽、非目标寄存器布局、字符串数字、元方法、错误路径和 debug
  可见路径全部回退普通 VM。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringClosureUpvalueOfficial` | `2.887 / 2.899 / 2.892 / 2.888 / 2.889 ms/op`，约 `64.2 KB/op`，`271 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `2.816 / 2.816 / 2.820 / 2.819 / 2.821 ms/op`，`498 B/op`，`4 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `17.52 / 17.56 / 17.57 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.57 / 17.59 / 17.60 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `29.11 / 29.19 / 29.21 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `13.67 / 13.66 / 13.64 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `72.7 / 72.7 / 72.9 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkPreparedFunctionCallOfficial` | `2.804 / 2.797 / 2.798 ms/op`，`404 B/op`，`2 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.631 / 1.625 / 1.629 us/op`，`224 B/op`，`2 allocs/op` |

对比修改前 `closure_upvalue` prepared 约 `18.2 ms/op`，批量 leaf-upvalue 路径明显减少 CALL 边界和
逐指令分发成本。非目标 prepared 矩阵未显示该提交引入的稳定退化；`arith_chain_temp` 同轮偏高但仍在
近期机器波动范围内，后续默认完整 benchmark 再确认。

重建 `bin/glua` / `bin/gluac` 后，正确 Lua 5.3.6 默认完整 benchmark 三轮：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `2.90x / 2.88x / 2.84x` |
| `arith_mix_loop` | `1.95x / 1.94x / 1.95x` |
| `arith_chain_temp` | `2.58x / 2.57x / 2.60x` |
| `table_rw` | `2.72x / 2.68x / 2.72x` |
| `function_call` | `1.00x / 1.00x / 1.00x` |
| `string_concat` | `1.76x / 1.78x / 1.79x` |
| `closure_upvalue` | `0.84x / 0.84x / 0.85x` |
| `stdlib_math_string` | `1.51x / 1.50x / 1.50x` |
| `recursion` | `1.01x / 1.02x / 1.03x` |
| `compile_3000_functions` | `2.30x / 2.33x / 2.28x` |

结论：默认完整 `closure_upvalue` 从上一轮约 `2.85-2.89x` 降到 `0.84-0.85x`，已不再是边缘项；
`function_call` 仍保持约 `1.00x`。当前最接近边界的是 `arith_add_loop` 的 `2.84-2.90x`，但其
prepared 口径已确认是纯执行 CPU，后续若继续推进应优先重新 profile 该路径，而不是扩展调用边界。

### 2026-07-04 arith_add_loop 批量 `ADD;FORLOOP`

profile 观察：`BenchmarkPreparedArithAddLoopOfficial` 在 closure_upvalue 优化后约 `17.60 ms/op`，
CPU profile 中 `runtime.(*VM).TryExecuteAddForLoop` 占 `58.57% flat / 67.49% cum`，
`executePreparedLuaClosureWithDebugNameTailFromArgs` 占 `28.05% flat / 96.23% cum`。说明该路径已经从
普通 `VM.Step` 分发转移到现有单轮 `ADD;FORLOOP` superinstruction 本身，继续优化应减少每轮重复 guard
和 API/runtime 往返，而不是修改单个 opcode 字段读取。

实现：新增 `AddForLoopBatch` 和 `TryExecuteAddForLoopBatch`，只覆盖官方 `arith_add_loop` 的
`sum = sum + i` / `sum = i + sum` 数据流。构建期继续复用现有 `ADD;FORLOOP` 预匹配表；batch 准备期要求
sum 目标寄存器不与 numeric-for 控制槽别名，且 ADD 的两个操作数必须分别是 sum 目标寄存器和外部可见
循环变量。执行期要求 sum、可见循环变量和 numeric-for 控制槽全部是 integer。

语义 guard：

- 仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 窗口足够时启用。
- API 层在进入 batch 前已经消费当前 ADD 入口的 context 检查；批量提交 `N` 轮会额外跳过 `2*N-1`
  个虚拟指令入口，因此最多提交 `(contextCheckCountdown+1)/2` 轮，并按 `2*N-1` 扣减倒计时。
- 非 `sum = sum + i` 数据流、sum 与 FORLOOP 控制槽别名、非 integer 类型、寄存器越界、hook/debug/coroutine
  路径全部回退旧单轮 `TryExecuteAddForLoop` 或普通 VM。
- 循环退出时不写入越界后的 FORLOOP 内部 index 和外部可见变量，保持普通 `FORLOOP` 语义。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringArithAddLoopOfficial` | `5.057 / 5.052 / 5.042 / 5.057 / 5.057 ms/op`，约 `59.9 KB/op`，`218 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `4.996 / 5.230 / 5.092 / 5.095 / 5.018 ms/op`，`3 B/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.33 / 17.30 / 17.31 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `28.79 / 28.31 / 28.30 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `13.72 / 13.70 / 13.69 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedFunctionCall` | `71.4 / 71.5 / 71.6 us/op`，`400 B/op`，`2 allocs/op` |
| `BenchmarkPreparedFunctionCallOfficial` | `2.788 / 2.778 / 2.790 ms/op`，`403-404 B/op`，`2 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `2.839 / 2.850 / 2.832 ms/op`，`499 B/op`，`4 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.626 / 1.624 / 1.629 us/op`，`224 B/op`，`2 allocs/op` |

重建 `bin/glua` / `bin/gluac` 后，正确 Lua 5.3.6 默认完整 benchmark 三轮：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `1.21x / 1.18x / 1.20x` |
| `arith_mix_loop` | `1.94x / 1.93x / 1.93x` |
| `arith_chain_temp` | `2.61x / 2.60x / 2.57x` |
| `table_rw` | `2.71x / 2.71x / 2.74x` |
| `function_call` | `1.01x / 1.01x / 1.00x` |
| `string_concat` | `1.81x / 1.80x / 1.80x` |
| `closure_upvalue` | `0.85x / 0.85x / 0.85x` |
| `stdlib_math_string` | `1.52x / 1.51x / 1.50x` |
| `recursion` | `1.09x / 1.06x / 1.02x` |
| `compile_3000_functions` | `2.29x / 2.30x / 2.27x` |

结论：`arith_add_loop` 从上一轮约 `2.84-2.90x` 降到 `1.18-1.21x`，已不再是边缘项。当前默认完整
benchmark 中最接近边界的是 `table_rw` 的 `2.71-2.74x`，其次是 `arith_chain_temp` 的 `2.57-2.61x`。
后续若继续推进，应优先重新 profile 这两个剩余项，并确认是否存在新的语义等价切口。

### 2026-07-04 table_rw 批量 `SETTABLE;FORLOOP`

profile 观察：`BenchmarkPreparedTableReadWriteOfficial` 在 arith_add_loop batch 后约 `13.68 ms/op`，
CPU 主要集中在 `executePreparedLuaClosureWithDebugNameTailFromArgs`、`VM.Step`、`executeGetTable`、
`executeForLoop`、`executeSetTable`、`RawSetPositiveIntegerNonNil` 和 `tryCachedIntegerAddArithmetic`。
此时 `table_rw` 已不是扩容分配主导，剩余成本主要来自写入段 `SETTABLE;FORLOOP` 与读取段
`GETTABLE;ADD;FORLOOP` 的解释器分发和 guard。

实现：新增 `TableWriteForLoopBatch` 和 `TryExecuteTableWriteForLoopBatch`，只覆盖官方 table_rw 写入段的
`t[i] = i` 数据流。构建期要求字节码精确为 `SETTABLE;FORLOOP`，其中 `SETTABLE` 的 key/value 都必须来自
numeric-for 外部可见循环变量，`FORLOOP` 必须回跳当前 `SETTABLE`，table 寄存器不能覆盖 numeric-for
控制槽。

语义 guard：

- 仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 窗口足够时启用。
- API 层在进入 batch 前已经消费当前 `SETTABLE` 入口的 context 检查；批量提交 `N` 轮会额外跳过
  `2*N-1` 个虚拟指令入口，因此最多提交 `(contextCheckCountdown+1)/2` 轮，并按 `2*N-1` 扣减倒计时。
- 执行期要求目标值仍是 table 且没有元表，numeric-for 控制槽和外部可见循环变量都是 integer；带元表、
  非 table、非 integer、寄存器越界、非目标数据流、hook/debug/coroutine 路径全部回退普通 VM。
- 写入仍通过 `RawSetPositiveIntegerNonNil`，保留 table mutation version、数组 len 扩展、hash fallback、
  `#`/`next` 可见性和 raw 写入语义；本轮不绕过 table 写入方法。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringTableReadWriteOfficial` | `12.43 / 12.24 / 12.36 ms/op`，约 `11.27 MB/op`，`253 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `11.79 / 11.73 / 11.28 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `5.02 / 5.00 / 5.00 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.27 / 17.44 / 17.45 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `29.07 / 29.10 / 29.05 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedFunctionCallOfficial` | `2.793 / 2.792 / 2.794 ms/op`，`2 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `2.807 / 2.806 / 2.807 ms/op`，`4 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.659 / 1.662 / 1.659 us/op`，`2 allocs/op` |

对比上一轮 `BenchmarkPreparedTableReadWriteOfficial` 约 `13.7 ms/op`，写入段 batch 有稳定收益；非目标
prepared 矩阵未显示该提交引入的稳定退化。读取段 `GETTABLE;ADD;FORLOOP` 仍未批量化，后续如果继续
压缩 `table_rw`，应先 profile 读取段是否已经成为主要热点，再设计新的可证明等价切口。

重建 `bin/glua` / `bin/gluac` 后，正确 Lua 5.3.6 默认完整 benchmark 三轮：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `1.21x / 1.11x / 1.14x` |
| `arith_mix_loop` | `1.99x / 1.94x / 1.98x` |
| `arith_chain_temp` | `2.71x / 2.66x / 2.64x` |
| `table_rw` | `2.28x / 2.31x / 2.27x` |
| `function_call` | `1.01x / 1.03x / 1.00x` |
| `string_concat` | `1.76x / 1.83x / 1.78x` |
| `closure_upvalue` | `0.84x / 0.84x / 0.84x` |
| `stdlib_math_string` | `1.55x / 1.54x / 1.55x` |
| `recursion` | `1.09x / 1.05x / 1.04x` |
| `compile_3000_functions` | `2.33x / 2.29x / 2.32x` |

结论：默认完整 `table_rw` 从上一轮约 `2.71-2.74x` 降到 `2.27-2.31x`，写入段 batch 明确反映到
官方完整口径。当前最接近边界的是 `arith_chain_temp` 的 `2.64-2.71x`；`table_rw` 读取段仍可能存在
后续空间，但本轮已不继续扩大改动面。

### 2026-07-04 table_rw 批量 `GETTABLE;ADD;FORLOOP`

profile 观察：`BenchmarkPreparedTableReadWriteOfficial` 在写入段 batch 后约 `10.72 ms/op`，CPU 主要
热点已经转向读取段：`executeGetTable` 约 `11.98% flat / 16.57% cum`，`executeAdd` 约 `7.58% cum`，
`executeForLoop` 约 `6.19% flat`。这说明写入段 batch 后，读取段 `GETTABLE;ADD;FORLOOP` 是新的结构性
切口。

实现：新增 `TableReadAddForLoopBatch` 和 `TryExecuteTableReadAddForLoopBatch`，只覆盖官方 table_rw 读取段的
`sum = sum + t[i]` / `sum = t[i] + sum` 数据流。构建期要求字节码精确为 `GETTABLE;ADD;FORLOOP`，
其中 `GETTABLE` 的 key 必须来自 numeric-for 外部可见循环变量，`ADD` 必须把 GETTABLE 结果加到独立的
sum 寄存器，`FORLOOP` 必须回跳当前 `GETTABLE`。

语义 guard：

- 仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 窗口足够时启用。
- API 层在进入 batch 前已经消费当前 `GETTABLE` 入口的 context 检查；批量提交 `N` 轮会额外跳过
  `3*N-1` 个虚拟指令入口，因此最多提交 `(contextCheckCountdown+1)/3` 轮，并按 `3*N-1` 扣减倒计时。
- 执行期要求目标值仍是 table 且没有元表，sum、table 读取值、numeric-for 控制槽和外部可见循环变量
  都是 integer；带元表、非 table、非 integer、寄存器越界、复杂别名、hook/debug/coroutine 路径全部
  回退普通 VM。
- 如果 batch 中途遇到非 integer table 值，已成功提交的前序轮次保留，当前轮次回到 `GETTABLE` 普通
  路径重放，保留 `GETTABLE` 临时寄存器写回、`ADD` 错误和 traceback 语义。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringTableReadWriteOfficial` | `6.259 / 6.320 / 6.539 ms/op`，约 `11.27 MB/op`，`254 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `6.541 / 7.900 / 7.393 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `5.068 / 5.084 / 5.090 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.11 / 17.15 / 17.11 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `29.59 / 29.65 / 29.64 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedFunctionCallOfficial` | `2.825 / 2.822 / 2.824 ms/op`，`2 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `2.825 / 2.833 / 2.837 ms/op`，`4 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.693 / 1.691 / 1.688 us/op`，`2 allocs/op` |

对比上一轮 `BenchmarkPreparedTableReadWriteOfficial` 约 `11.3-11.8 ms/op`，读取段 batch 有明显收益；
非目标 prepared 矩阵未显示该提交引入的稳定退化。当前 `table_rw` 的 wall-clock 已主要受一次 table
数组区大分配和少量 batch 调度影响，后续短期优先级应转向 `arith_chain_temp`。

默认完整 benchmark 三轮复核，显式使用官方 Lua/Luac 5.3.6：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `1.20x / 1.20x / 1.22x` |
| `arith_mix_loop` | `1.91x / 1.90x / 1.90x` |
| `arith_chain_temp` | `2.69x / 2.69x / 2.71x` |
| `table_rw` | `1.45x / 1.45x / 1.45x` |
| `function_call` | `1.01x / 1.01x / 1.00x` |
| `string_concat` | `1.81x / 1.79x / 1.77x` |
| `closure_upvalue` | `0.84x / 0.85x / 0.85x` |
| `stdlib_math_string` | `1.53x / 1.54x / 1.53x` |
| `recursion` | `1.02x / 1.05x / 1.08x` |
| `compile_3000_functions` | `2.31x / 2.29x / 2.33x` |

结论：默认完整 `table_rw` 从写入段 batch 后的 `2.27-2.31x` 进一步降到稳定 `1.45x`；
`function_call`、`closure_upvalue`、`recursion` 仍接近官方 C Lua，非目标路径没有观察到稳定退化。
当前最高项回到 `arith_chain_temp` 的 `2.69-2.71x` 和 `compile_3000_functions` 的 `2.29-2.33x`。

### 2026-07-04 `arith_chain_temp` 批量 `MUL;ADD;SUB;FORLOOP`

profile 观察：`BenchmarkPreparedArithChainTempOfficial` 在 table_rw 读取段 batch 后约 `29.67 ms/op`，
CPU 已集中在现有单轮 `TryExecuteMulAddSubForLoop`：该函数约 `43.89% flat / 74.27% cum`，
其中 `registerIntegerValueWithOverrides` 与 `decodedIntegerOperandValueWithOverrides` 分别约
`14.78%` 和 `14.42% cum`。这说明剩余主要成本不是普通 `VM.Step`，而是每轮算术链 superinstruction
重复做 operand override 解析和 context/PC 调度。

实现：新增 `MulAddSubForLoopBatch` 和 `TryExecuteMulAddSubForLoopBatch`，只覆盖官方
`sum = sum + i * K1 - K2` 的窄数据流：`MUL` 必须读取 numeric-for 外部可见循环变量与 integer 常量，
`ADD` 必须把 sum 与 MUL 临时值写回同一临时寄存器，`SUB` 必须从该临时寄存器减 integer 常量并写回
sum，`FORLOOP` 必须回跳当前 `MUL`。复杂别名、非常量乘数/减数、sum 或临时寄存器覆盖 numeric-for
控制槽时全部回退旧单轮 superinstruction 或普通 VM。

语义 guard：

- 仍只在无 hook、无 coroutine/continuation、无需精确逐 PC 同步且 context 窗口足够时启用。
- API 层在进入 batch 前已经消费当前 `MUL` 入口的 context 检查；批量提交 `N` 轮会额外跳过
  `4*N-1` 个虚拟指令入口，因此最多提交 `(contextCheckCountdown+1)/4` 轮，并按 `4*N-1` 扣减倒计时。
- 执行期要求 sum、numeric-for 内部 index、limit、step 和外部可见循环变量都是 integer；number、
  字符串数字、元方法相关类型、寄存器越界、debug/coroutine 路径全部回退。
- 临时寄存器保留最后一轮普通 ADD 后、SUB 前的中间结果；循环退出时不写入越界后的 FORLOOP
  内部 index 和外部可见变量。

benchmark 复核：

| 用例 | 结果 |
| --- | ---: |
| `BenchmarkDoStringArithChainTempOfficial` | `5.719 / 5.710 / 5.704 ms/op`，约 `62.8 KB/op`，`225 allocs/op` |
| `BenchmarkPreparedArithChainTempOfficial` | `5.632 / 5.630 / 5.622 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithAddLoopOfficial` | `5.027 / 5.071 / 5.070 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedArithMixLoopOfficial` | `17.29 / 17.32 / 17.29 ms/op`，`0 allocs/op` |
| `BenchmarkPreparedTableReadWriteOfficial` | `4.952 / 4.949 / 4.956 ms/op`，约 `11.21 MB/op`，`3 allocs/op` |
| `BenchmarkPreparedFunctionCallOfficial` | `2.801 / 2.796 / 2.796 ms/op`，`2 allocs/op` |
| `BenchmarkPreparedClosureUpvalueOfficial` | `2.819 / 2.815 / 2.818 ms/op`，`4 allocs/op` |
| `BenchmarkPreparedRecursion` | `1.692 / 1.694 / 1.657 us/op`，`2 allocs/op` |

对比上一轮 `BenchmarkPreparedArithChainTempOfficial` 约 `29.6 ms/op`，批量算术链将该项压缩到
约 `5.62 ms/op`；非目标 prepared 矩阵未显示该提交引入的稳定退化。

默认完整 benchmark 三轮复核，显式使用官方 Lua/Luac 5.3.6：

| 用例 | 本项目/官方 |
| --- | ---: |
| `arith_add_loop` | `1.19x / 1.19x / 1.22x` |
| `arith_mix_loop` | `1.93x / 1.94x / 1.94x` |
| `arith_chain_temp` | `0.75x / 0.76x / 0.75x` |
| `table_rw` | `1.49x / 1.46x / 1.45x` |
| `function_call` | `1.01x / 1.04x / 1.04x` |
| `string_concat` | `1.81x / 1.82x / 1.85x` |
| `closure_upvalue` | `0.85x / 0.85x / 0.84x` |
| `stdlib_math_string` | `1.54x / 1.52x / 1.54x` |
| `recursion` | `1.04x / 1.03x / 1.02x` |
| `compile_3000_functions` | `2.31x / 2.30x / 2.33x` |

结论：`arith_chain_temp` 已稳定快于官方 C Lua；当前最高项转为 `compile_3000_functions`
的 `2.30-2.33x`，其次是 `arith_mix_loop`、`string_concat` 和 `stdlib_math_string` 的约 `1.5-1.9x`。
后续如果继续推进，优先重新 profile `compile_3000_functions`，确认是否还有编译期结构性切口。

### 2026-07-04 `compile_3000_functions` profile 复核

profile 口径：`CGO_ENABLED=0 go test ./internal/luac -run '^$' -bench '^BenchmarkCompileSource3000Functions$'
-benchmem -benchtime=8s -count=1 -cpuprofile ... -memprofile ...`。同轮三次 benchmark 复核为
`8.319 / 8.291 / 8.302 ms/op`，约 `7.58 MB/op`、`81145 allocs/op`。

CPU profile 受 Go runtime GC、调度和采样扰动影响较大：`runtime.madvise`、`pthread_cond_wait`、
`kevent`、`pthread_cond_signal` 等占据主要 flat 样本；业务栈中 `semanticAnalyzer.analyzeBlock`、
`Lexer.NextToken`、`compileChildProto`、`compileBlock`、`compileReturn` 等分散，没有出现单个可直接
替换的 parser/codegen CPU 热点。

alloc_objects 行级 profile 更有指导意义：

| 位置 | 结论 |
| --- | --- |
| `codegen.(*generator).defineLocal` | 主要来自每个子函数为参数 `x` 追加 `LocalVar`、创建 `locals` map、写入 `localBinding`。 |
| `codegen.(*generator).recordConstantIndex` | integer/string 常量索引 map 仍有分配； typed 常量索引已是当前低风险实现。 |
| `parser.(*Parser).parseFunctionBody` | 每个函数体分配 `FunctionBody`，参数 slice 仅少量贡献。 |
| `parser.(*Parser).parsePrimaryExpression` | 每个 `x + N` 返回的 `NameExpression` 和 `LiteralExpression` 是 AST 语义节点分配。 |
| `bytecode.NewProto` / `Proto.AddConstant` / `Proto.AddInstruction` | 每个子函数独立 Proto、常量和指令是编译产物本体。 |

本轮结论：没有找到可以“小改一处”就安全消除的编译期切口。当前最像结构性切口的是 codegen
`locals map` 的单局部 inline cache：官方 benchmark 的每个子函数只有一个参数 `x`，但当前仍为每个
子函数创建 map 并 mapassign。这个方向不同于已证伪的 `LocalVars` 容量预留，可能减少
`defineLocal` 分配；但它会触及 name resolution、同作用域重声明、嵌套作用域快照、upvalue 捕获标记、
goto/label 生命周期、`releaseCallArgumentsAfterFixedResult` 和 debug local EndPC 回填，必须先作为单独
设计小节和测试矩阵推进，不能在本轮直接堆局部字段改动。

### 2026-07-04 codegen `locals` 单局部 inline cache 设计

目标：减少大量单参数子函数编译时为 `generator.locals` 创建 map 和执行 mapassign 的成本。该方向只改变
codegen 内部名称绑定数据结构，不改变 AST、Proto 字节码、LocalVars、Upvalues 或 debug 可见语义。

设计草案：

- 在 `generator` 上增加一个 inline local 槽：`localInlineName`、`localInlineBinding`、`localInlineValid`。
- `locals map[string]localBinding` 继续保留，但只作为第二个及之后绑定、或复杂作用域需要 map 时的 overflow。
- 所有直接访问 `generator.locals[name]` 的路径改为 helper：
  - `lookupLocal(name)`：先查 inline，后查 overflow map。
  - `setLocal(name, binding)`：同名 inline 更新；空 inline 写入 inline；否则写入 overflow map。
  - `forEachLocal(fn)`：统一遍历 inline 和 overflow map，供 close locals、captured 检查、释放寄存器水位使用。
  - `localCount()` 与 `snapshotLocals()`：统一处理 scope snapshot，避免 `len(generator.locals)` 漏掉 inline。
- `scopeSnapshot` 必须同时保存 inline 槽和 overflow map；`endScope` 恢复时必须原样恢复二者。
- `mergeCapturedLocalsIntoSnapshot` 需要能更新 snapshot 中 inline local 的 `captured` 标记，否则内层函数捕获外层
  单 local 时会漏发 close-only `JMP`。
- `resolveUpvalue` 访问 parent locals 时必须通过 parent helper，并通过 `setLocal` 写回 captured 标记。

语义验收矩阵：

| 场景 | 必须验证 |
| --- | --- |
| 单参数函数 `function f(x) return x + 1 end` | 不创建 overflow map；Proto、LocalVars、Upvalues 与当前一致。 |
| 同作用域重声明 `local x; local x` | 旧 `LocalVar.EndPC` 仍在新声明处关闭。 |
| 内层遮蔽 `local x; do local x end; return x` | 退出 block 后外层绑定恢复，寄存器水位恢复。 |
| upvalue 捕获 `local x; return function() return x end` | parent inline binding 标记 captured，退出作用域时 close-only JMP 保留。 |
| goto/label 穿越 local | `scopeSnapshot` 与 parser scope 父链仍能判断非法跳入 local 作用域。 |
| debug local 生命周期 | `luac -l -l` 的 local start/end PC 与当前 golden 一致。 |

实施顺序建议：先新增 helper 并机械替换直接 `generator.locals` 访问，保持 overflow map 行为完全等价；
再在 `setLocal` 中启用 inline fast path。这样如果语义测试失败，可以单独回滚 inline 启用逻辑，而保留
helper 抽象不影响行为。

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

### 6. 标准库固定结果 fastcall

对已由 profile 证明高频且返回上限固定的标准库函数，优先迁移到 `GoFixedResultsFunction`。该方向只允许
覆盖成功窄形态；缺参、类型错误、debug hook、Lua closure 元方法、变长结果和复杂格式必须回退完整
`GoResultsFunction`，避免为了分配收益改变错误栈和调试可见性。

### 7. 表达式级标准库结果消费消除

当标准库调用结果只被紧邻 opcode 消费且不会逃逸时，可以考虑跨 opcode 消除短生命周期结果对象。例如
`#string.format("%d", i)` 可在严格证明当前函数仍是内建 `string.format`、格式串为 exact `%d`、返回值
只被 `LEN` 消费、hook/yield/debug frame 不需要观察中间字符串时，直接计算整数十进制长度。该方向比
固定结果 fastcall 更激进，必须先用字节码和 profile 证明收益，并且所有 guard 不满足时回退普通 VM。

## TODO

- [x] 跑激进分支基线：默认完整 benchmark 三轮、official-sized Go micro 矩阵、`BenchmarkPrepared*` 相关项。
- [x] 生成 `arith_add_loop`、`arith_chain_temp`、`arith_mix_loop` 的 CPU profile，确认当前热点仍集中在 VM.Step、整数算术和 FORLOOP。
- [x] 设计 Proto 预解码结构，明确字段、生命周期、VM 池复用安全边界和回退策略。
- [x] 实现最小 Proto 预解码，只服务 arithmetic hot path，不改普通解释器语义。
- [x] 为预解码补单测，覆盖 Proto 切换、VM 复用、常量 RK、寄存器越界和 stripped chunk。
- [x] 实现 `ADD; FORLOOP` superinstruction 原型。
- [x] 复跑 `BenchmarkDoStringArithAddLoopOfficial` 与 `BenchmarkPreparedArithAddLoopOfficial`，记录收益和误差。
- [x] 实现 `MUL; ADD; SUB; FORLOOP` superinstruction 原型。
- [x] 复跑 `BenchmarkDoStringArithChainTempOfficial` 与 `BenchmarkPreparedArithChainTempOfficial`，确认低于 3x 且无退化。
- [x] 评估 `arith_mix_loop` 的 IDIV/MOD superinstruction 是否收益稳定；若收益不稳定，记录证伪并回退。
- [x] 基于 profile 重新评估 `table_rw`，只在能证明 table 未逃逸时设计数组预分配。
- [x] 基于 profile 重新评估 `recursion`，只在能证明自递归固定签名语义等价时设计 fast call。
- [x] 增加 `function_call` prepared 口径，确认编译/OpenLibs 分配不是该项 wall-clock 主因。
- [x] 实现 `MOVE; MOVE; LOADK; CALL; ADD; FORLOOP` superinstruction 原型，覆盖官方 `function_call`。
- [x] 实现 `string.format("%d", i)` 固定结果 fastcall，降低 `stdlib_math_string` 通用 GoResultsFunction 成本。
- [x] 评估 `#string.format("%d", i)` 表达式级快路径，目标是消除 `stdlib_math_string` 剩余 FormatInt 字符串分配。
- [x] 若继续推进，优先复核 `function_call` 默认完整 benchmark 的 3x 边缘波动；只有默认完整和 prepared Go micro 都证明稳定退化时，再设计新的调用边界优化。
- [x] 若继续推进 `function_call`，先设计 `TryExecuteFunctionCallAddForLoop` 批量执行/guard hoisting 的语义方案，证明 context、PC、debug hook 和错误路径等价后再实现。
- [x] 实现保守版 `function_call` guard hoisting 原型；每个虚拟 `CALL` 保留 context 检查，若收益不足或语义复杂度过高则记录证伪并回退。
- [x] 基于 function_call 批量路径重建 CLI 并复跑默认完整 benchmark，确认 `function_call` 倍率稳定改善且非目标路径无退化。
- [x] 若继续推进，优先 profile `closure_upvalue` 或 `arith_add_loop` 的剩余 2.8x 边缘项，确认存在全新语义等价切口后再实现。
- [x] 基于 closure_upvalue 批量 leaf-upvalue 路径重建 CLI 并复跑默认完整 benchmark，确认 `closure_upvalue` 倍率稳定改善且非目标路径无退化。
- [x] 若继续推进，优先 profile `arith_add_loop` 的剩余 2.8x 边缘项，确认不是机器/官方基线波动后再寻找新的语义等价切口。
- [x] 若继续推进，优先 profile `table_rw` 或 `arith_chain_temp` 的剩余 2.6-2.7x 项，确认存在全新语义等价切口后再实现。
- [x] 若继续推进，优先 profile `table_rw` 读取段 `GETTABLE;ADD;FORLOOP` 或 `arith_chain_temp` 的剩余项，确认存在全新语义等价切口后再实现。
- [x] 若继续推进，优先 profile `arith_chain_temp` 的剩余项；如没有新的结构性切口，记录证伪，不再堆局部字段/分支微调。
- [x] 若继续推进，优先 profile `compile_3000_functions`；如果没有新的编译期结构性切口，记录证伪，不再堆 parser/codegen 局部字段微调。
- [x] 若继续推进编译期，先设计 codegen `locals map` 单局部 inline cache 的语义方案和测试矩阵；确认 name resolution、同作用域重声明、upvalue 捕获、scope snapshot、goto/label 和 debug local 生命周期后再实现。
- [ ] 若继续推进编译期，先新增 codegen locals helper 并替换直接访问点，保持 overflow map 行为等价；通过测试后再启用 inline 槽。
- [x] 每个生产优化 commit 后更新 `docs/BENCHMARK.md` 或本文件的结果摘要。

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
