# Table 设计说明

## 迭代稳定性

当前 `runtime.Table` 已实现 `RawNext`、`RawPairsNext` 和 `RawIPairsNext`。

需要明确区分两层语义：

- Lua 5.3 语言规范不保证 `next` 和 `pairs` 的 table 遍历顺序。
- 本项目当前 Go 实现为方便测试、golden 对比和 VM 行为复现，提供稳定 raw 迭代顺序。

当前稳定顺序为：

1. 数组区正整数 key 按升序输出。
2. hash 区 key 按 `ValueKind` 类型顺序输出。
3. 同类型 hash key 再按各自负载排序：boolean 为 `false` 后 `true`，integer 和 number 按数值升序，string 按字节字典序。

该顺序只属于本项目内部可复现策略，不代表 Lua 5.3 官方 C 实现的 hash 遍历顺序。兼容测试不得依赖官方 Lua 与本项目在 hash key 上输出相同顺序。

## RawNext

`RawNext(key)` 对齐 Lua `next(table, key)` 的基础边界：

- `key == nil` 时从第一个非 nil raw 项开始。
- 返回 `ok=false` 表示迭代结束。
- 迭代跳过数组区 nil 槽位和 hash 区 nil 值。
- 传入不存在于当前 table 的继续 key 时返回 `ErrInvalidTableIterationKey`。
- 当前实现每次调用构建一次稳定快照，优先保证正确性和可复现性，后续 Table resize 与性能阶段再评估增量游标优化。

## RawPairsNext

`RawPairsNext(key)` 是当前阶段 `pairs` 的 raw 迭代入口，直接复用 `RawNext`：

- 不触发 `__pairs` 元方法。
- 不承诺 hash 遍历顺序兼容官方 Lua。
- 标准库 `pairs` 接入后，应在 stdlib 层处理 `__pairs` 兼容策略。

## RawIPairsNext

`RawIPairsNext(index)` 对齐 `ipairs` 的基础前缀遍历：

- 通常从 `index=0` 开始。
- 每次读取 `index+1` 的 raw integer 值。
- 遇到第一个 nil 正整数槽位时结束。
- 不触发 `__index` 或 `__ipairs`。
- `index` 到达 int64 上限时直接结束，避免整数上溢。

## 与 resize 的关系

后续实现 Table resize 时必须保持：

- 正整数 key 无论位于数组区还是 hash 区，都能被 `RawGetInteger`、`Len` 和 `RawIPairsNext` 正确读取。
- `RawNext` 输出仍跳过 nil 项。
- resize 不得让同一 key 在数组区和 hash 区同时保留两个非 nil 值。
- resize 后测试应覆盖数组区扩张、收缩、整数 key 迁移和迭代快照。

## 与 weak table 的关系

Lua 5.3 通过 metatable 中的 `__mode` 字符串声明弱表：

- `k` 表示 weak key。
- `v` 表示 weak value。
- 同时包含 `k` 和 `v` 表示 key 与 value 都弱引用。

当前项目阶段尚未实现完整 GC、对象标记和生命周期边界，因此 weak table 只完成策略评估，不在 `runtime.Table` 中启用弱引用清理。第一阶段实现选择如下：

- `__mode` 字段暂不改变 raw get/set/next 行为。
- weak key/value 不在当前 Table 存储结构中做特殊标记。
- 标准库或 debug 层如果读取到 `__mode`，只能把它作为普通元表字段处理。
- 兼容测试不得在 GC 尚未实现前断言 weak table 自动清理行为。

后续 GC 阶段启用 weak table 时必须满足：

- weak key 被判定不可达后，不得继续出现在 `RawNext` 快照中。
- weak value 被判定不可达后，应表现为该 key 的 value 为 nil，并从迭代中跳过。
- ephemeron 语义需要单独评估，避免 value 反向保活 weak key 的判断错误。
- 已清理的 weak entry 必须同时从数组区或 hash 区删除，不能保留 tombstone 值影响 `Len`、`RawNext` 或 `RawIPairsNext`。
- 迭代过程中触发 GC 时，需要定义当前快照是否保持稳定；默认建议 RawNext 在单次调用内使用调用时快照，不承诺跨调用期间 table 被 GC 修改后的顺序稳定。

实现顺序建议：

1. 先完成 Go GC 与 Lua object 生命周期边界设计。
2. 增加对象可达性标记接口，明确 table、closure、userdata、thread 的引用扫描规则。
3. 增加 `__mode` 解析和缓存，避免每次访问重复解析元表字符串。
4. 在 GC sweep 阶段清理 weak entry。
5. 补充 weak key、weak value、weak key/value 组合以及迭代清理后的测试。
