# Go GC 与 Lua 对象生命周期边界

本文记录当前项目在纯 Go、无 CGO 前提下的对象生命周期策略。目标是先建立清晰 root 边界，后续再逐步补齐 Lua 5.3 的 GC 语义。

## 设计目标

- 核心 VM 对象全部由 Go 对象承载，不接入 Lua C API 或 C 分配器。
- 当前阶段依赖 Go GC 回收不可达对象，不实现自定义内存移动或手动释放。
- Lua 可达性通过 State root、registry、stack、call frame、table、closure、upvalue 和 coroutine 逐步建模。
- 未来如需模拟 Lua 5.3 增量 GC，应建立在显式对象图之上，而不是绕过 Go GC。

## 当前 root 边界

- `State.registry` 是 registry table root，保存主线程和全局环境。
- `State.globals` 是 `_G` root，与 registry 中的 globals 槽位指向同一 table。
- `State.stack` 保存当前主线程栈上的 Lua value root。
- `State.callFrames` 保存当前调用链帧元数据，间接保留正在执行的函数值。
- `StringPool` 只持有短字符串驻留条目，长字符串不进入池。

`State.Close` 会清空 registry、globals、stack、callFrames 和 context 引用，使 Go GC 可以回收不再可达的对象。

## 第一阶段策略

- 以 `State.SnapshotGCRoots()` 建立可验证 root 快照，先只做可达入口采样，不实现回收流程。
- 采样分六类：`state-root`、`registry-root`、`stack-root`、`closure-upvalue-root`、`table-key-value-root`、`coroutine-stack-root`。
- 下个阶段在此基础上增加闭包/线程关联的递归标记与垃圾清理接口。

## 字符串生命周期

- 短字符串：长度不超过 `maxShortStringLen` 的字符串进入 `StringPool`，相同字节序列复用同一池条目。
- 长字符串：不进入驻留池，由 `StringStorageLongOwned` 标记，并由返回的 `Value` 持有 Go string 内容。
- 字符串 hash 和字节长度在 `StringStorage` 中显式记录，供后续 table key、debug 和 GC 统计复用。

## Table 与引用对象

- Table key/value 都是 Lua value，可形成对象图边。
- 当前 table 依赖 Go map/slice 持有强引用。
- weak key/weak value 当前只完成策略评估，尚未实现弱引用语义。
- 后续接入 Lua GC 时，table 需要支持 mark 阶段遍历 key/value，并按 weak 模式调整标记行为。

## Closure、upvalue 与 coroutine

- Lua closure、Go closure、userdata 和 thread 当前使用 `ReferenceValue` 保存引用 identity。
- 后续 closure 需要显式持有 Proto 与 upvalue 列表。
- upvalue 需要区分 open upvalue 与 closed upvalue，并明确从 stack 到 heap 的迁移时机。
- coroutine 需要独立栈和调用帧 root，主线程 registry 槽位只保存 main thread。

## Finalizer 与 userdata

- 当前阶段不实现 Lua 语义级别的 `__gc`，但已建立显式关闭协议：`State` 维护 `RegisterUserdata` 列表，`State.Close` 以逆序执行用户回调。
- 本阶段 `userdata` 关闭行为优先保障确定性：回调失败或 panic 仅影响记录，不影响其他 userdata 和关闭流程。
- `Close` 并不隐式使用 Go `runtime.SetFinalizer`，避免对 GC 时机和外部资源释放顺序的不可预测性；Go finalizer 仅保留为可观测手段。
- 含外部资源的 userdata 必须暴露显式 close/release 或 finalizer 注册路径，并由 State 关闭流程统一触发。

## `__gc` 与增量 GC 评估

- `__gc` 当前评估结论：短期先不在纯 Go 实现中模拟 `collectgarbage` 触发路径，统一通过 `State.Close` 提供确定性收口，避免并发 GC 与回调时序对接困难。
- 后续阶段若要支持 `__gc`，先增加元表标记记录与关闭队列，再在 snapshot root 追踪到 `finalizer` 表项，最后在 `close` 与显式 GC pass 上引入可选的用户回调执行。
- 增量 GC 与官方 Lua 的差异评估结论：当前以 Go GC 负责主回收路径；官方 `lgc` 的步进和三色标记语义无法完全等价复现，但可在后续实现中提供“近似 API 行为层”与计数型可观察指标。
- 生命周期文档与验收策略先固定为：
  - 明确区分“确定性 Close”与“Lua `__gc` 延迟回收”；
  - 在 `collectgarbage`、`userdata` 与标准库边界测试中统一声明差异范围。

## 未来 Lua GC 接入顺序

1. 建立对象接口，统一 table、closure、userdata、thread 的 mark 行为。
2. 在 State 中维护可选对象注册表，仅用于 Lua GC 统计与遍历。
3. 实现 stop-the-world mark/sweep 版本，先保证语义正确。
4. 增加 weak table 清理规则。
5. 增加 `__gc` finalizer 队列和错误处理策略。
6. 评估是否需要近似 Lua 5.3 增量 GC 的步进接口。

## 当前限制

- 不承诺与 Lua 5.3 GC 时机一致。
- 不暴露 `collectgarbage` 的完整行为。
- 不支持弱表自动清理。
- 不支持 userdata `__gc`。
- 资源限制目前通过 Options 检查栈深度、调用深度和分配预算，尚未绑定到对象分配路径。
