# JIT 专项 TODO

本文只记录 JIT 长期专项，不替代当前解释器性能优化计划。短期目标仍是先把主要 benchmark 路径压到目标区间；JIT 只有在解释器热点边界、语义测试和 benchmark harness 稳定后再进入原型。

## 0. 当前定位

- [ ] JIT 不作为首阶段发布门禁。
- [ ] JIT 不用于绕过 Lua 5.3 兼容语义、debug hook 语义、协程 yield 语义、错误 traceback 语义和扩展语法行为。
- [ ] JIT 默认关闭，后续只能通过显式运行时选项启用，不得新增产品功能 build tag。
- [ ] JIT 原型不得引入 CGO，不依赖 C Lua 动态库。
- [ ] JIT 原型必须保留解释器 fallback；任一语义不确定路径回退解释执行。
- [ ] JIT 原型实验、设计验证和正式实现必须单独创建 feature 分支推进，禁止混入解释器短期性能优化分支。

## 1. 解释器优先优化顺序

- [ ] 完成 table/global 读取优化：继续扩展版本化 inline cache，覆盖 `_ENV.math`、`math.floor`、`string.find`、`string.len` 等稳定字符串字段读取链。
- [ ] 完成标准库函数调用边界优化：为无 hook、无 debug 活跃的纯标准库函数增加 fastcall，减少 Go call frame、参数切片和结果切片成本。
- [ ] 完成 VM 指令 dispatch 优化：围绕 `GETTABUP`、`GETTABLE`、`CALL`、`ADD`、`SUB`、`MUL`、`FORLOOP` 建立热点路径和回归 benchmark。
- [ ] 完成算术循环 typed fast path：为 integer `FORLOOP`、integer arithmetic、RK integer 常量组合补齐缓存和 codegen hint。
- [ ] 完成函数调用与 upvalue 优化：扩大 direct leaf call 覆盖范围，复用调用帧、参数区、返回区和 upvalue 读取快路径。

## 2. JIT 前置验收

- [ ] 建立稳定的官方 Lua 对比 benchmark harness，并固化到 `scripts/`。
- [ ] 为 `arith_loop`、`table_rw`、`function_call`、`closure_upvalue`、`stdlib_math_string`、`recursion` 建立可重复 benchmark 输入。
- [ ] 为每个热点记录解释器优化后的基线、profile top 和剩余瓶颈。
- [ ] 确认 debug hook、line hook、count hook、coroutine yield、pcall/xpcall、metamethod、weak table、GC root 裁剪在解释器路径全部稳定。
- [ ] 设计 JIT fallback 触发条件：debug hook 开启、协程可 yield 调用、元方法不确定、table 版本失效、类型 guard 失败、资源限制触发。

## 3. JIT 原型范围

- [ ] 第一阶段只考虑 trace JIT 或 basic block JIT，不做完整方法 JIT。
- [ ] 第一阶段只覆盖无 hook、无 yield、无 Lua closure 元方法的热循环。
- [ ] 第一阶段只覆盖 integer numeric for、integer arithmetic、局部寄存器读写和无元表 table 字符串字段读取。
- [ ] 第一阶段只允许生成 Go 内部可执行计划或解释器 micro-op，不直接生成机器码。
- [ ] 第一阶段必须支持 guard 失败后回到解释器，并保持寄存器、PC、open upvalue 和 traceback 状态可恢复。

## 4. JIT 设计任务

- [ ] 定义热度计数策略：按 Proto PC、循环回边或 basic block 记录执行次数。
- [ ] 定义 IR 或 micro-op 表示：包含寄存器读写、常量、算术、比较、跳转、table 读取和返回。
- [ ] 定义类型 guard：integer、number、string、table identity、table mutation version、metatable nil 状态。
- [ ] 定义 deopt 状态：PC、寄存器窗口、openTop、pending call、pending comparison、closeFrom。
- [ ] 定义与 GC 的关系：JIT 执行期间必须让活动寄存器和临时 Value 可被扫描或安全保守保活。
- [ ] 定义与 debug 的关系：debug hook 激活时禁用 JIT；debug 库创建后是否保守禁用需要单独验证。
- [ ] 定义与 coroutine 的关系：可 yield 调用点不进入 JIT；已创建 coroutine 后是否禁用 JIT 需要单独评估。

## 5. JIT 实现任务

- [ ] 增加 JIT 运行配置：默认关闭，支持 State Options 显式启用实验模式。
- [ ] 增加热点计数器：绑定 Proto PC，避免 VM 池复用污染。
- [ ] 增加 trace/basic block 采集器：只采集白名单 opcode。
- [ ] 增加 guard 编译与校验：guard 失败直接回退解释器。
- [ ] 增加 micro-op 执行器：先使用纯 Go 结构化执行计划验证收益。
- [ ] 增加 JIT cache 失效：Proto、table version、debug 状态、State 关闭和配置变化必须失效。
- [ ] 增加 fallback 测试：覆盖类型变化、table 重赋值、metatable 添加、debug hook 打开、pcall 错误和 coroutine yield。

## 6. JIT Benchmark 与回归

- [ ] 增加解释器 vs JIT 的同进程 benchmark，避免 CLI 冷启动影响判断。
- [ ] 增加官方 Lua vs glua interpreter vs glua JIT 三列对比。
- [ ] 增加 JIT 命中率、guard 失败率、fallback 次数和编译成本统计。
- [ ] 增加 JIT 开启但未命中时的退化检测，确认普通脚本不会显著变慢。
- [ ] 增加长期回归表，记录每次 JIT 扩展覆盖的 opcode、收益和语义风险。

## 7. 暂不进入范围

- [ ] 暂不直接生成机器码。
- [ ] 暂不引入 CGO、LLVM、外部 JIT runtime 或 C Lua 动态库。
- [ ] 暂不 JIT Lua closure 元方法、debug hook 活跃路径、可 yield 标准库调用和复杂 pattern 引擎。
- [ ] 暂不为了 JIT 改变 bytecode 格式或破坏 `luac`/binary chunk 兼容。
