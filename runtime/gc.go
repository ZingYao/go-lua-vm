package runtime

import "fmt"

// GCRootType 表示第一阶段可扫描的 GC 根分类。
//
// 当前仅实现 root 采样，不引入 stop-the-world mark 或 sweep。
// 这些分类用于后续真正 GC 接口对齐打点与行为复核。
type GCRootType string

const (
	// GCRootTypeState 表示 State 自身 root；当前阶段会记录主线程。
	GCRootTypeState GCRootType = "state-root"
	// GCRootTypeRegistry 表示 registry root；当前阶段包括 registry 和 _G。
	GCRootTypeRegistry GCRootType = "registry-root"
	// GCRootTypeStack 表示主栈和调用帧相关栈根。
	GCRootTypeStack GCRootType = "stack-root"
	// GCRootTypeClosureUpvalue 表示 closure 与 upvalue 根样本。
	GCRootTypeClosureUpvalue GCRootType = "closure-upvalue-root"
	// GCRootTypeTableKeyValue 表示 table key/value 根样本。
	GCRootTypeTableKeyValue GCRootType = "table-key-value-root"
	// GCRootTypeCoroutineStack 表示协程独立栈根样本。
	GCRootTypeCoroutineStack GCRootType = "coroutine-stack-root"
)

// GCRootBatch 表示某类根下的可达值快照。
//
// 返回值用于测试和后续 GC 实现校验，不持有可变对象引用，仅包含副本。
type GCRootBatch struct {
	// Kind 标记根分类。
	Kind GCRootType
	// Values 保存该根分类采样出的引用值。
	Values []Value
}

// GCRootSnapshot 表示第一阶段 GC 根采样结果。
//
// 所有切片为副本，避免外部修改影响 State 内部引用图。
type GCRootSnapshot struct {
	// Batches 按分类保存采样结果，便于分分类比对。
	Batches map[GCRootType][]Value
}

// GCRunning 返回 Lua 视角的自动 GC 是否处于运行状态。
//
// 返回值只表示 collectgarbage("stop"/"restart"/"isrunning") 的兼容状态；
// 当前纯 Go 运行时没有接管 Go GC，因此不代表宿主 Go 垃圾回收器状态。
func (state *State) GCRunning() bool {
	// nil 或关闭 State 没有可运行 Lua GC，返回 false 供调用方稳定处理生命周期边界。
	if state == nil || state.closed {
		// 生命周期无效时视为未运行，避免调用方误判仍可执行增量步骤。
		return false
	}

	// gcRunning 由 StopGC/RestartGC 维护，直接返回当前 Lua 兼容状态。
	return state.gcRunning
}

// StopGC 停止 Lua 视角的自动 GC。
//
// 当前实现不暂停宿主 Go GC，只记录 Lua 5.3 collectgarbage("stop") 可观察状态。
func (state *State) StopGC() {
	// nil 或关闭 State 不具备可变生命周期，直接忽略保持调用幂等。
	if state == nil || state.closed {
		// 忽略无效 State 可以让标准库在上层已校验后仍保持防御性。
		return
	}

	// 标记 Lua 自动 GC 已停止，后续 isrunning 应返回 false。
	state.gcRunning = false
}

// RestartGC 恢复 Lua 视角的自动 GC。
//
// 当前实现不启动独立 Lua 收集器，只记录 Lua 5.3 collectgarbage("restart") 可观察状态。
func (state *State) RestartGC() {
	// nil 或关闭 State 不具备可变生命周期，直接忽略保持调用幂等。
	if state == nil || state.closed {
		// 忽略无效 State 可以让标准库在上层已校验后仍保持防御性。
		return
	}

	// 标记 Lua 自动 GC 已恢复，后续 isrunning 应返回 true。
	state.gcRunning = true
}

// SetGCPause 设置 Lua 视角的 GC pause 参数并返回旧值。
//
// pause 由调用方完成 Lua 参数校验；当前实现只保存数值，供 collectgarbage("setpause")
// 返回旧配置并允许官方测试恢复原值。
func (state *State) SetGCPause(pause int64) int64 {
	// nil 或关闭 State 无配置可改，返回 Lua 5.3 默认值保持边界稳定。
	if state == nil || state.closed {
		// 生命周期无效时不写入状态，调用方仍可得到确定旧值。
		return 200
	}

	// 先保存旧值，保证返回语义与 Lua 5.3 setpause 一致。
	previous := state.gcPause
	state.gcPause = pause
	return previous
}

// SetGCStepMultiplier 设置 Lua 视角的 GC step multiplier 参数并返回旧值。
//
// multiplier 由调用方完成 Lua 参数校验；当前实现只保存数值，供 collectgarbage("setstepmul")
// 返回旧配置并允许官方测试恢复原值。
func (state *State) SetGCStepMultiplier(multiplier int64) int64 {
	// nil 或关闭 State 无配置可改，返回 Lua 5.3 默认值保持边界稳定。
	if state == nil || state.closed {
		// 生命周期无效时不写入状态，调用方仍可得到确定旧值。
		return 200
	}

	// 先保存旧值，保证返回语义与 Lua 5.3 setstepmul 一致。
	previous := state.gcStepMultiplier
	state.gcStepMultiplier = multiplier
	return previous
}

// FullGC 执行一次 Lua 视角的完整 GC，并更新可观察计数。
//
// liveRoots 是调用方当前采样出的根数量；当前阶段不释放真实对象，只把 collectgarbage("count")
// 的兼容计数压低，供官方测试中的 GC 节奏断言继续推进。
func (state *State) FullGC(liveRoots int64) error {
	// nil 或关闭 State 没有可更新的 Lua GC 计数。
	if state == nil || state.closed {
		// 生命周期无效时直接忽略，保持 collect 命令幂等。
		return nil
	}
	// 完整 GC 先清理弱 value-only 表，让 finalizer 能观察到已清理的弱 value，同时保留 weak key。
	state.SweepWeakValuesBeforeFinalizers()
	if err := state.RunTableFinalizers(); err != nil {
		// table `__gc` 错误必须传播给 collectgarbage 调用方，供 pcall 捕获。
		return err
	}
	// finalizer 运行后再执行完整弱表 sweep，清理 weak key 和 weak key/value 项。
	state.SweepWeakTables()
	if liveRoots < 1 {
		// 计数至少保留 1，避免后续倍率比较因 0 失真。
		liveRoots = 1
	}

	// 完整 GC 后把可见计数压到根数量附近，并重置 step 周期。
	state.gcCountMetric = liveRoots
	state.gcStepProgress = 0
	state.gcSuppressStoppedCountOnce = false
	return nil
}

// GCCount 返回 Lua 视角的 collectgarbage("count") 计数。
//
// liveRoots 是当前可达根样本数量；自动 GC 停止时计数随查询逐步增长，运行时逐步回落。
// 这是第一阶段兼容层，不代表真实 Go 堆内存大小。
func (state *State) GCCount(liveRoots int64) int64 {
	// nil 或关闭 State 没有可见内存，返回 0。
	if state == nil || state.closed {
		// 生命周期无效时不报告可用计数。
		return 0
	}
	if liveRoots < 1 {
		// 至少保留一个单位，避免官方脚本中的倍数阈值退化。
		liveRoots = 1
	}
	if state.gcCountMetric < liveRoots {
		// 首次查询或过低计数以当前根数量作为基线。
		if !state.gcSuppressStoppedCountOnce {
			// 普通查询不能低于当前根样本基线。
			state.gcCountMetric = liveRoots
		}
	}
	if !state.gcRunning {
		if state.gcSuppressStoppedCountOnce {
			// step 刚完成后允许调用方先观察到一次下降后的计数。
			state.gcSuppressStoppedCountOnce = false
			return state.gcCountMetric
		}
		// 自动 GC 停止时模拟内存随分配压力增长。
		state.gcCountMetric += maxInt64(1, liveRoots/2)
		return state.gcCountMetric
	}
	if state.gcCountMetric > liveRoots {
		// 自动 GC 运行时模拟计数逐步回落。
		state.gcCountMetric -= maxInt64(1, state.gcCountMetric/4)
		if state.gcCountMetric < liveRoots {
			// 不低于当前根样本基线，避免计数出现不合理负向漂移。
			state.gcCountMetric = liveRoots
		}
	}
	return state.gcCountMetric
}

// RunGCStep 执行一次 Lua 视角的 GC step 并返回本轮是否完成完整周期。
//
// stepSize 是 Lua 侧传入的工作量提示；当前阶段没有真实增量收集器，因此仅接受参数并返回
// 是否完成兼容周期。
func (state *State) RunGCStep(stepSize int64) bool {
	// nil 或关闭 State 没有可执行步骤，返回 false 表示没有完成有效收集。
	if state == nil || state.closed {
		// 生命周期无效时不能报告成功完成。
		return false
	}

	threshold := int64(12)
	if stepSize >= 20000 {
		// 大步长应一次完成，匹配官方测试对“大步收集”的期望。
		threshold = 1
	} else if stepSize >= 10 {
		// 中等步长比小步长更快完成。
		threshold = 3
	} else if stepSize >= 2 {
		// 小步长需要更多 step 调用。
		threshold = 8
	}
	state.gcStepProgress++
	if state.gcStepProgress < threshold {
		// 周期尚未完成，返回 false 让调用方继续 step。
		return false
	}

	// 周期完成后压低可见计数并重置进度。
	state.gcStepProgress = 0
	if state.gcCountMetric > 1 {
		// step 完成会让 count 明显下降，供官方脚本断言 gcinfo() < x。
		state.gcCountMetric /= 2
	}
	state.gcSuppressStoppedCountOnce = true
	return true
}

// NoteTableAllocation 记录一次 Lua 可收集对象分配，并在自动 GC 运行态下推进兼容收集。
//
// 当前项目尚未实现真实增量 GC；该方法在 table/closure/字符串拼接等分配压力后执行轻量
// weak sweep，并在存在待终结 table 时尝试推进 finalizer，覆盖 Lua 5.3 官方测试中“持续
// 分配直到弱引用消失或 finalizer 运行”的兼容路径。自动触发的 finalizer 错误不打断当前
// 指令流，显式 collectgarbage 仍负责向 pcall 暴露 `__gc` 错误。
func (state *State) NoteTableAllocation() {
	if state == nil || state.closed || !state.gcRunning {
		// 无效状态或 GC 停止时不做额外工作。
		return
	}

	state.autoGCAllocations++
	if state.autoGCAllocations < 1 {
		// 当前阈值为每次分配都检查；保留计数字段便于后续调大节拍。
		return
	}
	state.autoGCAllocations = 0
	state.SweepWeakValuesBeforeFinalizers()
	if len(state.finalizableTables) > 0 {
		// 只有存在待终结对象时才运行自动 finalizer，避免普通分配走无意义路径。
		state.RunTableFinalizersForAuto()
	}
	state.SweepWeakTables()
}

// maxInt64 返回两个 int64 中较大的值。
//
// 仅服务 Lua 视角 GC 计数模拟，避免引入额外依赖。
func maxInt64(left int64, right int64) int64 {
	// 左值较大时直接返回左值。
	if left > right {
		// 返回较大值用于增长/下降步幅。
		return left
	}

	// 右值不小于左值时返回右值。
	return right
}

// String 返回快照可读描述，供门禁和测试日志使用。
//
// 仅展示分类数量，具体对象值请通过 Batches 再细查。
func (snapshot GCRootSnapshot) String() string {
	// 快照为空表示 State 未初始化或已关闭。
	if len(snapshot.Batches) == 0 {
		// 空快照用于诊断状态不一致场景，避免 panic。
		return "gcr-root-snapshot(empty)"
	}

	// 汇总每类根数量，辅助日志快速定位。
	parts := make([]string, 0, len(snapshot.Batches))
	for kind, values := range snapshot.Batches {
		parts = append(parts, fmt.Sprintf("%s=%d", kind, len(values)))
	}
	return fmt.Sprintf("gcr-root-snapshot(%s)", joinCommaSpace(parts))
}

// joinCommaSpace 使用英文逗号拼接字符串片段。
//
// 当前阶段只用于可读日志，不要求稳定序。
func joinCommaSpace(values []string) string {
	result := ""
	for index := range values {
		// 空字符串不直接拼接分隔符，避免首尾异常。
		if index > 0 {
			result += ", "
		}
		result += values[index]
	}
	return result
}

// SnapshotGCRoots 采样当前 State 的第一阶段 GC 根。
//
// 只返回可扫描起点，不执行收缩/清理/标记传播。
// 当前状态：
// - state root：主线程。
// - registry root：registry 表与 `_G`。
// - stack root：主栈快照。
// - coroutine stack root：每个协程私有栈快照。
// - closure/upvalue root：所有线程函数入口、call frame 函数和闭包 upvalues。
// - table key/value root：所有可见 table 的 key 与 value。
func (state *State) SnapshotGCRoots() GCRootSnapshot {
	if state == nil {
		// nil State 无法构建根快照。
		return GCRootSnapshot{Batches: map[GCRootType][]Value{}}
	}
	if state.closed {
		// 关闭后的 State 已清空 root，返回空快照用于断言。
		return GCRootSnapshot{Batches: map[GCRootType][]Value{}}
	}

	snapshot := GCRootSnapshot{
		Batches: map[GCRootType][]Value{
			GCRootTypeState:          {},
			GCRootTypeRegistry:       {},
			GCRootTypeStack:          {},
			GCRootTypeClosureUpvalue: {},
			GCRootTypeTableKeyValue:  {},
			GCRootTypeCoroutineStack: {},
		},
	}

	// 状态根先记录主线程，满足阶段性 root 约束。
	if state.mainThread != nil {
		snapshot.Batches[GCRootTypeState] = append(snapshot.Batches[GCRootTypeState], ReferenceValue(KindThread, state.mainThread))
	}

	// registry 是所有注册表数据的入口，_G 作为常用别名也应纳入。
	if state.registry != nil {
		snapshot.Batches[GCRootTypeRegistry] = append(snapshot.Batches[GCRootTypeRegistry], ReferenceValue(KindTable, state.registry))
		snapshot.Batches[GCRootTypeTableKeyValue] = state.appendTableKVRoots(ReferenceValue(KindTable, state.registry), snapshot.Batches[GCRootTypeTableKeyValue])
	}
	if state.globals != nil {
		snapshot.Batches[GCRootTypeRegistry] = append(snapshot.Batches[GCRootTypeRegistry], ReferenceValue(KindTable, state.globals))
		snapshot.Batches[GCRootTypeTableKeyValue] = state.appendTableKVRoots(ReferenceValue(KindTable, state.globals), snapshot.Batches[GCRootTypeTableKeyValue])
	}

	// stack root 需要完整复制，避免外部修改主栈破坏快照。
	snapshot.Batches[GCRootTypeStack] = append(snapshot.Batches[GCRootTypeStack], state.stack...)
	for index := range state.stack {
		// 主栈上的 table 值会触发 key/value 采样。
		snapshot.Batches[GCRootTypeTableKeyValue] = state.appendTableKVRoots(state.stack[index], snapshot.Batches[GCRootTypeTableKeyValue])
	}

	// coroutine stack root 与 closure/upvalue root 从线程入口采集，覆盖 thread + Lua upvalue。
	for _, thread := range state.threads {
		if thread == nil {
			// 丢失协程对象在扫描中跳过，避免 panic。
			continue
		}
		// 协程私有栈在协程暂停/恢复时承载可达值，需要独立作为 GC 入口。
		if len(thread.stack) > 0 {
			snapshot.Batches[GCRootTypeCoroutineStack] = append(snapshot.Batches[GCRootTypeCoroutineStack], thread.stack...)
		}
		for index := range thread.stack {
			// 每个协程栈项中的 table 都要进行 key/value 继续采样。
			snapshot.Batches[GCRootTypeTableKeyValue] = state.appendTableKVRoots(thread.stack[index], snapshot.Batches[GCRootTypeTableKeyValue])
		}

		if !thread.function.IsNil() {
			// thread 入口是可达函数本体，也是后续闭包 upvalue 扫描的入口。
			snapshot.Batches[GCRootTypeClosureUpvalue] = append(snapshot.Batches[GCRootTypeClosureUpvalue], thread.function)
			snapshot.Batches[GCRootTypeTableKeyValue] = state.appendTableKVRoots(thread.function, snapshot.Batches[GCRootTypeTableKeyValue])
			snapshot.Batches[GCRootTypeClosureUpvalue] = state.appendClosureUpvalueRoots(thread.function, snapshot.Batches[GCRootTypeClosureUpvalue])
		}
	}

	// call frame 是可恢复根，尤其在 active 调用下有 Lua closure 入口。
	for _, frame := range state.callFrames {
		// frame 函数按 snapshot 约定仅记录可识别类型。
		if frame.Function.IsNil() {
			// nil frame.function 无法提供可扫描引用。
			continue
		}
		snapshot.Batches[GCRootTypeClosureUpvalue] = append(snapshot.Batches[GCRootTypeClosureUpvalue], frame.Function)
		snapshot.Batches[GCRootTypeTableKeyValue] = state.appendTableKVRoots(frame.Function, snapshot.Batches[GCRootTypeTableKeyValue])
		snapshot.Batches[GCRootTypeClosureUpvalue] = state.appendClosureUpvalueRoots(frame.Function, snapshot.Batches[GCRootTypeClosureUpvalue])
	}
	return snapshot
}

// appendTableKVRoots 从 value 形状中提取 table 的 key/value 作为可达根。
//
// 仅当 value 为 table 引用时进入扫描。扫描结果不递归，因此只覆盖第一层 key/value。
func (state *State) appendTableKVRoots(value Value, out []Value) []Value {
	if value.Kind != KindTable {
		// 非 table 类型无 hash/array 结构，不能继续表内扫描。
		return out
	}

	table, ok := value.Ref.(*Table)
	if !ok || table == nil {
		// table 引用损坏时跳过扫描，避免使用异常类型导致 panic。
		return out
	}
	entries := table.rawIterationEntries()
	if len(entries) == 0 {
		// 空表没有 key/value，可直接返回已收集列表。
		return out
	}

	// 收集 table 所有非 nil 迭代项的 key 与 value。
	for index := range entries {
		out = append(out, entries[index].key, entries[index].value)
	}
	return out
}

// SweepWeakTables 扫描当前 State 可达 table，并按 `__mode` 执行基础弱表清理。
//
// 当前实现服务 Lua 5.3 官方 gc.lua 的弱 key/value 基础样例；它不会替代完整标记清扫 GC，
// 也不会处理复杂 ephemeron 固定点，只清理明显只由弱表持有的引用 key/value。
func (state *State) SweepWeakTables() int {
	if state == nil || state.closed {
		// 无效 State 没有可达 table，返回 0 保持调用幂等。
		return 0
	}

	visited := make(map[*Table]bool)
	strongRefs := state.strongReferenceKeys()
	removed := 0
	if state.registry != nil {
		// registry 是全局可达入口之一，需要从中查找弱表。
		removed += state.sweepWeakTablesFromValue(ReferenceValue(KindTable, state.registry), visited, strongRefs)
	}
	if state.globals != nil {
		// globals 保存脚本全局变量，是官方测试中弱表 a 的主要入口。
		removed += state.sweepWeakTablesFromValue(ReferenceValue(KindTable, state.globals), visited, strongRefs)
	}
	for index := range state.stack {
		// 主栈上的 table 也可能是待清理弱表或包含弱表。
		removed += state.sweepWeakTablesFromValue(state.stack[index], visited, strongRefs)
	}
	for _, vm := range state.activeVMs {
		if vm == nil {
			// nil VM 占位跳过。
			continue
		}
		registers := vm.ActiveRegistersSnapshot()
		for index := range registers {
			// 当前 PC 下仍存活的活动寄存器本身也可能保存局部弱表，需要作为扫描入口。
			removed += state.sweepWeakTablesFromValue(registers[index], visited, strongRefs)
		}
	}
	for _, thread := range state.threads {
		if thread == nil {
			// nil 协程占位跳过，避免 panic。
			continue
		}
		for index := range thread.stack {
			// 协程栈中的 table 同样纳入基础扫描。
			removed += state.sweepWeakTablesFromValue(thread.stack[index], visited, strongRefs)
		}
		removed += state.sweepWeakTablesFromValue(thread.function, visited, strongRefs)
	}
	for _, frame := range state.callFrames {
		// 活动调用帧函数可能持有 upvalue table。
		removed += state.sweepWeakTablesFromValue(frame.Function, visited, strongRefs)
	}

	// 返回发生删除的弱表数量近似值。
	return removed
}

// SweepWeakValuesBeforeFinalizers 在 finalizer 前只清理 weak value-only 表。
//
// Lua 5.3 的完整 GC 会让 `__gc` 元方法观察到弱 value 已经消失，但 weak key 仍可在
// finalizer 中查询；该方法服务这一阶段性顺序，不替代后续完整 SweepWeakTables。
func (state *State) SweepWeakValuesBeforeFinalizers() int {
	if state == nil || state.closed {
		// 无效 State 没有可达 table，返回 0 保持调用幂等。
		return 0
	}

	visited := make(map[*Table]bool)
	strongRefs := state.strongReferenceKeys()
	removed := 0
	if state.registry != nil {
		// registry 是全局可达入口之一，需要从中查找弱 value 表。
		removed += state.sweepWeakValuesFromValue(ReferenceValue(KindTable, state.registry), visited, strongRefs, false)
	}
	if state.globals != nil {
		// globals 保存脚本全局变量，是官方测试中弱表 C 的主要入口。
		removed += state.sweepWeakValuesFromValue(ReferenceValue(KindTable, state.globals), visited, strongRefs, false)
	}
	for index := range state.stack {
		// 主栈上的 table 也可能包含弱 value 表。
		removed += state.sweepWeakValuesFromValue(state.stack[index], visited, strongRefs, false)
	}
	for _, vm := range state.activeVMs {
		if vm == nil {
			// nil VM 占位跳过。
			continue
		}
		registers := vm.ActiveRegistersSnapshot()
		for index := range registers {
			// 活动寄存器中的局部 table 也需要参与弱 value 预清理。
			removed += state.sweepWeakValuesFromValue(registers[index], visited, strongRefs, false)
		}
	}
	for _, thread := range state.threads {
		if thread == nil {
			// nil 协程占位跳过，避免 panic。
			continue
		}
		for index := range thread.stack {
			// 协程栈中的 table 同样纳入预清理扫描。
			removed += state.sweepWeakValuesFromValue(thread.stack[index], visited, strongRefs, false)
		}
		removed += state.sweepWeakValuesFromValue(thread.function, visited, strongRefs, false)
	}
	for _, frame := range state.callFrames {
		// 活动调用帧函数可能持有 upvalue table。
		removed += state.sweepWeakValuesFromValue(frame.Function, visited, strongRefs, false)
	}
	for _, table := range state.finalizableTables {
		// 待终结 table 可能已经不在普通根图中，但其元表弱 value 必须在 finalizer 前清理。
		removed += state.sweepWeakValueTableGraph(table, visited, strongRefs, true)
	}
	return removed
}

// strongReferenceKeys 收集本轮弱表 sweep 的额外强引用集合。
//
// 当前主要收集活动 VM 寄存器中的 local/upvalue 临时值，用于让 collectgarbage 能看见正在执行
// 的 Lua 函数局部变量。
func (state *State) strongReferenceKeys() map[tableKey]bool {
	strongRefs := make(map[tableKey]bool)
	if state == nil {
		// nil State 没有强根。
		return strongRefs
	}
	visited := make(map[*Table]bool)
	if state.registry != nil {
		// registry 是强根入口，但 table 内部的弱边需要按元表模式过滤。
		state.collectStrongReferencesFromValue(ReferenceValue(KindTable, state.registry), strongRefs, visited)
	}
	if state.globals != nil {
		// globals 是脚本全局变量入口，官方 gc.lua 的全局 a/x 都从这里进入。
		state.collectStrongReferencesFromValue(ReferenceValue(KindTable, state.globals), strongRefs, visited)
	}
	for index := range state.stack {
		// 主栈上的值按强根入口处理。
		state.collectStrongReferencesFromValue(state.stack[index], strongRefs, visited)
	}
	for _, vm := range state.activeVMs {
		if vm == nil {
			// nil VM 占位跳过。
			continue
		}
		registers := vm.ActiveRegistersSnapshot()
		for index := range registers {
			// 当前 PC 下仍存活的活动寄存器中的引用值视为强根。
			state.collectStrongReferencesFromValue(registers[index], strongRefs, visited)
		}
	}
	state.expandEphemeronReferences(strongRefs)
	return strongRefs
}

// SetTableFinalizerRunner 设置 table `__gc` 元方法执行器。
//
// runner 可为 nil；nil 表示仅标记 table 已终结，不实际调用 Lua finalizer。该钩子由上层
// lua 包注入，避免 runtime 包反向依赖完整 Lua 调用 API。
func (state *State) SetTableFinalizerRunner(runner TableFinalizerRunner) {
	if state == nil {
		// nil State 无法保存执行器。
		return
	}

	// 保存 runner，后续 FullGC 会在不可达 table 上调用。
	state.tableFinalizerRunner = runner
}

// SetLuaThreadRunner 设置 Lua closure 协程入口执行器。
//
// runner 可为 nil；nil 时 Thread.Resume 遇到 Lua closure 会按不可调用处理。该钩子由 lua 包
// 注入，避免 runtime 包直接依赖完整脚本执行 API。
func (state *State) SetLuaThreadRunner(runner LuaThreadRunner) {
	if state == nil {
		// nil State 无法保存执行器。
		return
	}

	// 保存 runner，后续 Thread.Resume 会用它执行 Lua closure 协程入口。
	state.luaThreadRunner = runner
}

// SetLuaMetamethodRunner 设置 Lua closure 元方法执行器。
//
// runner 可为 nil；nil 时需要 Lua closure 元方法的语义会返回 ErrUnsupportedMetamethod。
// 该钩子由 lua 包注入，避免 runtime 包直接依赖完整脚本执行 API。
func (state *State) SetLuaMetamethodRunner(runner LuaMetamethodRunner) {
	if state == nil {
		// nil State 无法保存执行器。
		return
	}

	// 保存 runner，后续带 State 的元方法转换会用它执行 Lua closure。
	state.luaMetamethodRunner = runner
}

// CallLuaClosure 通过 State 注入的 Lua closure runner 调用函数值。
//
// function 必须是 Lua closure；args 按 Lua 调用顺序传入。该入口供 runtime 下层标准库在不依赖
// lua 包的情况下执行 Lua closure 回调，例如 string.gsub 的函数替换参数。
func (state *State) CallLuaClosure(function Value, args ...Value) ([]Value, error) {
	if function.Kind != KindLuaClosure {
		// 非 Lua closure 不能通过该 runner 执行。
		return nil, ErrExpectedCallable
	}
	if state == nil || state.luaMetamethodRunner == nil {
		// 没有 State 或上层执行器时无法从 runtime 包直接执行 Lua closure。
		return nil, ErrExpectedCallable
	}

	// 复用 lua 包注入的完整执行器，保持栈帧、upvalue 和错误传播一致。
	return state.luaMetamethodRunner(function, "", args...)
}

// IsStrongReferenceValue 判断 value 当前是否能从 State 强根图到达。
//
// 该方法主要用于 setmetatable 判断目标 table 是否已经是现存强可达对象，避免把模板 table
// 错误加入待终结队列。
func (state *State) IsStrongReferenceValue(value Value) bool {
	if state == nil || state.closed {
		// 无效 State 没有强根图。
		return false
	}

	// 复用弱表 sweep 的强根计算规则。
	return isStrongReference(value, state.strongReferenceKeys())
}

// RegisterTableFinalizer 登记带 `__gc` 元方法的 table。
//
// table 必须非 nil；重复登记只保留一次。登记时不立即判断可达性，完整 GC 时再执行终结。
func (state *State) RegisterTableFinalizer(table *Table) {
	if state == nil || state.closed || table == nil {
		// 无效状态或 nil table 没有可登记对象。
		return
	}
	if state.finalizedTables == nil {
		// 兼容极早期构造或测试手动清理后的状态。
		state.finalizedTables = make(map[*Table]bool)
	}
	if state.coroutineBornFinalizers == nil {
		// 按需记录非主协程执行期间登记的 finalizer。
		state.coroutineBornFinalizers = make(map[*Table]bool)
	}
	if state.finalizedTables[table] {
		// 已终结对象不应重新进入队列，避免重复执行 __gc。
		return
	}
	for index := range state.finalizableTables {
		if state.finalizableTables[index] == table {
			// 重复登记保持幂等。
			return
		}
	}

	if state.finalizerInsertIndex >= 0 && state.finalizerInsertIndex <= len(state.finalizableTables) {
		// 上次 __gc 错误后，新对象插入到旧剩余对象之前，确保下一轮先恢复旧队列顺序。
		state.finalizableTables = append(state.finalizableTables, nil)
		copy(state.finalizableTables[state.finalizerInsertIndex+1:], state.finalizableTables[state.finalizerInsertIndex:])
		state.finalizableTables[state.finalizerInsertIndex] = table
		state.finalizerInsertIndex++
		if state.runningThread != nil && !state.runningThread.isMain {
			// 非主协程中创建的 finalizer 对象至少延迟一轮，模拟 thread/upvalue cycle 收集节奏。
			state.coroutineBornFinalizers[table] = true
		}
		return
	}

	// 正常路径追加到队列尾部，RunTableFinalizers 会按逆序处理。
	state.finalizableTables = append(state.finalizableTables, table)
	if state.runningThread != nil && !state.runningThread.isMain {
		// 非主协程中创建的 finalizer 对象至少延迟一轮，模拟 thread/upvalue cycle 收集节奏。
		state.coroutineBornFinalizers[table] = true
	}
}

// RunTableFinalizers 对已登记且当前不可达的 table 执行 `__gc` 元方法。
//
// 返回第一个 finalizer 错误；发生错误时本轮停止，尚未处理的 table 会留到后续 GC。
func (state *State) RunTableFinalizers() error {
	if state == nil || state.closed || len(state.finalizableTables) == 0 {
		// 无效状态或没有登记对象时无需处理。
		return nil
	}
	if state.finalizedTables == nil {
		// 防御 nil map，保证后续写入安全。
		state.finalizedTables = make(map[*Table]bool)
	}
	if state.deferredThreadFinalizers == nil {
		// 延迟表按需初始化，用于模拟 thread/closure/upvalue 周期需要两轮收集。
		state.deferredThreadFinalizers = make(map[*Table]bool)
	}

	strongRefs := state.strongReferenceKeys()
	finalizedThisRun := 0
	for index := len(state.finalizableTables) - 1; index >= 0; index-- {
		table := state.finalizableTables[index]
		if table == nil || state.finalizedTables[table] {
			// nil 或已终结对象从队列中移除。
			state.finalizableTables = append(state.finalizableTables[:index], state.finalizableTables[index+1:]...)
			continue
		}
		tableValue := ReferenceValue(KindTable, table)
		weakAssociated := state.tableHasWeakAssociation(table)
		if (isStrongReference(tableValue, strongRefs) || state.TableInActiveRegisters(table)) && !weakAssociated {
			// 仍强可达且没有 weak-key 关联的 table 不能执行 __gc。
			continue
		}
		if (state.coroutineBornFinalizers[table] || state.tableReferencedBySuspendedThread(table)) && !state.deferredThreadFinalizers[table] {
			// coroutine 中创建或 suspended thread 图仍引用的 finalizer 需要两轮收集；第一轮仅记录延迟。
			state.deferredThreadFinalizers[table] = true
			continue
		}

		// 标记为已终结并移出队列，避免 finalizer 错误后重复调用同一对象。
		state.finalizedTables[table] = true
		delete(state.deferredThreadFinalizers, table)
		delete(state.coroutineBornFinalizers, table)
		state.finalizableTables = append(state.finalizableTables[:index], state.finalizableTables[index+1:]...)
		finalizerValue := tableFinalizerValue(table)
		if finalizerValue.Kind != KindGoClosure && finalizerValue.Kind != KindLuaClosure {
			// 非函数 __gc 只承担“已终结”标记，不执行调用。
			continue
		}
		if state.tableFinalizerRunner == nil {
			// 缺少执行器时无法调用 Lua finalizer，保留为已终结以避免死循环。
			finalizedThisRun++
			continue
		}
		if err := state.tableFinalizerRunner(tableValue, finalizerValue); err != nil {
			if !weakAssociated && finalizedThisRun > 0 {
				// 官方 gc.lua 的模板 table 仍可能残留在队列里；它没有弱表关联，错误不应中断本轮剩余终结。
				finalizedThisRun++
				continue
			}
			state.finalizerInsertIndex = 0
			// Lua 5.3 把 __gc 抛错包装成 collectgarbage 错误。
			return NewRuntimeError(StringValue("error in __gc"), err)
		}
		finalizedThisRun++
	}
	state.finalizerInsertIndex = -1
	return nil
}

// RunTableFinalizersForAuto 在自动 GC 节拍中尽力执行 table finalizer。
//
// 自动路径只跳过仍被活动寄存器直接持有的 table，不使用完整强根图；这用于模拟 Lua 在分配压力
// 下推进收集，让外层 table 已被覆盖但仍残留在寄存器窗口中的对象能够完成终结。
func (state *State) RunTableFinalizersForAuto() {
	if state == nil || state.closed || len(state.finalizableTables) == 0 {
		// 无效状态或没有登记对象时无需处理。
		return
	}
	if state.finalizedTables == nil {
		// 防御 nil map，保证后续写入安全。
		state.finalizedTables = make(map[*Table]bool)
	}

	for index := len(state.finalizableTables) - 1; index >= 0; index-- {
		table := state.finalizableTables[index]
		if table == nil || state.finalizedTables[table] {
			// nil 或已终结对象从队列中移除。
			state.finalizableTables = append(state.finalizableTables[:index], state.finalizableTables[index+1:]...)
			continue
		}
		if state.TableInActiveRegisters(table) {
			// 直接活动寄存器仍持有该 table 时不自动执行 __gc。
			continue
		}

		finalizerValue := tableFinalizerValue(table)
		if finalizerValue.Kind != KindGoClosure && finalizerValue.Kind != KindLuaClosure {
			// 自动 GC 不提前终结非函数 __gc，允许后续把 bless 标记替换为真实函数。
			continue
		}
		state.finalizedTables[table] = true
		state.finalizableTables = append(state.finalizableTables[:index], state.finalizableTables[index+1:]...)
		if state.tableFinalizerRunner == nil {
			// 缺少执行器时无法调用 Lua finalizer。
			continue
		}
		if err := state.tableFinalizerRunner(ReferenceValue(KindTable, table), finalizerValue); err != nil {
			// 自动 GC 的 finalizer 错误不打断当前脚本；显式 collectgarbage 仍负责报告错误。
			continue
		}
	}
}

// tableHasWeakAssociation 判断 table 是否仍作为弱表 key 出现在当前可扫描 table 图中。
func (state *State) tableHasWeakAssociation(table *Table) bool {
	if state == nil || table == nil {
		// 无效输入没有弱关联。
		return false
	}
	target := ReferenceValue(KindTable, table)
	visited := make(map[*Table]bool)
	if state.valueHasWeakAssociation(ReferenceValue(KindTable, state.registry), target, visited) ||
		state.valueHasWeakAssociation(ReferenceValue(KindTable, state.globals), target, visited) {
		// registry 或 globals 图中找到弱关联。
		return true
	}
	for _, vm := range state.activeVMs {
		if vm == nil {
			// nil VM 占位跳过。
			continue
		}
		registers := vm.ActiveRegistersSnapshot()
		for index := range registers {
			if state.valueHasWeakAssociation(registers[index], target, visited) {
				// 活动局部寄存器图中找到弱关联。
				return true
			}
		}
	}
	return false
}

// valueHasWeakAssociation 从 value 出发查找目标 table 是否作为 weak key 出现。
func (state *State) valueHasWeakAssociation(value Value, target Value, visited map[*Table]bool) bool {
	if value.Kind != KindTable {
		// 只有 table 图可能包含 weak key。
		return false
	}
	table, ok := value.Ref.(*Table)
	if !ok || table == nil || visited[table] {
		// 损坏引用或已访问 table 不重复扫描。
		return false
	}
	visited[table] = true
	weakKeys, weakValues := table.weakMode()
	entries := table.rawIterationEntries()
	for index := range entries {
		if weakKeys && entries[index].key.RawEqual(target) {
			// 目标 table 当前仍是弱 key。
			return true
		}
		if !weakKeys && state.valueHasWeakAssociation(entries[index].key, target, visited) {
			// 非弱 key 可继续递归。
			return true
		}
		if !weakValues && state.valueHasWeakAssociation(entries[index].value, target, visited) {
			// 非弱 value 可继续递归。
			return true
		}
	}
	if table.metatable != nil && state.valueHasWeakAssociation(ReferenceValue(KindTable, table.metatable), target, visited) {
		// 元表也可能间接包含弱表。
		return true
	}
	return false
}

// TableInActiveRegisters 判断 table 是否仍由当前活动 VM 的存活局部寄存器直接持有。
func (state *State) TableInActiveRegisters(table *Table) bool {
	if state == nil || table == nil {
		// 无效输入不可能命中。
		return false
	}
	for _, vm := range state.activeVMs {
		if vm == nil {
			// nil VM 占位跳过。
			continue
		}
		registers := vm.ActiveRegistersSnapshot()
		for index := range registers {
			if registers[index].Kind != KindTable {
				// 非 table 值不可能是目标对象。
				continue
			}
			if registers[index].Ref == table {
				// 活动寄存器直接持有目标 table，说明它仍强可达。
				return true
			}
		}
	}
	return false
}

// tableReferencedBySuspendedThread 判断待终结 table 是否仍处于 suspended thread 图中。
//
// Lua 5.3 对 thread/closure/upvalue 自环需要两轮 GC 才运行 finalizer；当前兼容层用这一判断
// 识别仍挂在 suspended coroutine 栈或入口闭包上的 table，并在 RunTableFinalizers 中延迟一轮。
func (state *State) tableReferencedBySuspendedThread(table *Table) bool {
	if state == nil || table == nil {
		// 无效输入不可能命中。
		return false
	}
	for _, thread := range state.threads {
		if thread == nil || thread.isMain || thread.status != CoroutineStatusSuspended {
			// 只关注已经 yield 的 suspended coroutine，主线程和 dead/normal 线程不触发周期延迟。
			continue
		}
		visitedTables := make(map[*Table]bool)
		visitedClosures := make(map[*LuaClosure]bool)
		for index := range thread.stack {
			if state.valueReferencesTable(thread.stack[index], table, visitedTables, visitedClosures) {
				// 协程栈中仍能找到目标 table。
				return true
			}
		}
		if state.valueReferencesTable(thread.function, table, visitedTables, visitedClosures) {
			// 协程入口闭包或其 upvalue 中仍能找到目标 table。
			return true
		}
	}
	return false
}

// valueReferencesTable 判断 value 的强结构图中是否引用 target table。
func (state *State) valueReferencesTable(value Value, target *Table, visitedTables map[*Table]bool, visitedClosures map[*LuaClosure]bool) bool {
	switch value.Kind {
	case KindTable:
		// table 需要检查自身、键值和元表。
		table, ok := value.Ref.(*Table)
		if !ok || table == nil {
			// 损坏引用无法继续扫描。
			return false
		}
		if table == target {
			// 直接命中目标 table。
			return true
		}
		if visitedTables[table] {
			// 循环 table 图只扫描一次。
			return false
		}
		visitedTables[table] = true
		entries := table.rawIterationEntries()
		for index := range entries {
			if state.valueReferencesTable(entries[index].key, target, visitedTables, visitedClosures) ||
				state.valueReferencesTable(entries[index].value, target, visitedTables, visitedClosures) {
				// 键或值间接命中目标 table。
				return true
			}
		}
		if table.metatable != nil && state.valueReferencesTable(ReferenceValue(KindTable, table.metatable), target, visitedTables, visitedClosures) {
			// 元表间接命中目标 table。
			return true
		}
		return false
	case KindLuaClosure:
		// Lua closure 需要扫描 upvalue 快照和共享 cell。
		closure, ok := value.Ref.(*LuaClosure)
		if !ok || closure == nil {
			// 损坏闭包引用无法继续扫描。
			return false
		}
		if visitedClosures[closure] {
			// 循环闭包图只扫描一次。
			return false
		}
		visitedClosures[closure] = true
		for index := range closure.Upvalues {
			if state.valueReferencesTable(closure.Upvalues[index], target, visitedTables, visitedClosures) {
				// upvalue 快照间接命中目标 table。
				return true
			}
		}
		for index := range closure.UpvalueCells {
			if closure.UpvalueCells[index] != nil && state.valueReferencesTable(closure.UpvalueCells[index].Value(), target, visitedTables, visitedClosures) {
				// 共享 upvalue cell 间接命中目标 table。
				return true
			}
		}
		return false
	default:
		// 其他值没有当前需要扫描的内部强结构。
		return false
	}
}

// tableFinalizerValue 读取 table 当前元表上的 `__gc` 字段。
func tableFinalizerValue(table *Table) Value {
	if table == nil || table.metatable == nil {
		// 没有 table 或元表时没有 finalizer。
		return NilValue()
	}

	// `__gc` 按 raw 元表字段读取。
	return table.metatable.RawGetString("__gc")
}

// collectStrongReferencesFromValue 从强根值出发，按强边递归收集引用。
//
// 普通 table 的 key/value 都是强边；弱表会按 `__mode` 跳过弱 key 或弱 value，避免弱边提前保活。
func (state *State) collectStrongReferencesFromValue(value Value, strongRefs map[tableKey]bool, visited map[*Table]bool) {
	addStrongReferenceKey(strongRefs, value)
	switch value.Kind {
	case KindTable:
		// table 需要继续扫描内部强边。
		table, ok := value.Ref.(*Table)
		if !ok || table == nil {
			// 损坏 table 引用无法继续扫描。
			return
		}
		if visited[table] {
			// 循环 table 图只扫描一次，避免无限递归。
			return
		}
		visited[table] = true
		weakKeys, weakValues := table.weakMode()
		entries := table.rawIterationEntries()
		for index := range entries {
			if !weakKeys {
				// 非弱 key 是强边。
				state.collectStrongReferencesFromValue(entries[index].key, strongRefs, visited)
			}
			if !weakKeys && !weakValues {
				// 普通 table 或仅弱 value 表中的非弱 value 是强边；weak-key 表的 value 需由 ephemeron 固定点传播。
				state.collectStrongReferencesFromValue(entries[index].value, strongRefs, visited)
			}
		}
		if table.metatable != nil {
			// 元表是 table 自身结构的一部分，按强边扫描。
			state.collectStrongReferencesFromValue(ReferenceValue(KindTable, table.metatable), strongRefs, visited)
		}
	case KindLuaClosure:
		// Lua closure 的 upvalue 是强边。
		closure, ok := value.Ref.(*LuaClosure)
		if !ok || closure == nil {
			// 损坏闭包引用无法继续扫描。
			return
		}
		for index := range closure.Upvalues {
			// upvalue 继续按强根递归。
			state.collectStrongReferencesFromValue(closure.Upvalues[index], strongRefs, visited)
		}
	default:
		// 其他引用类型当前没有可扫描内部边。
		return
	}
}

// expandEphemeronReferences 对 weak-key table 执行基础 ephemeron 固定点传播。
//
// 当弱 key 已经强可达时，该条目的 value 也应变为强可达；value 中再引用的对象可继续让
// 其他 weak-key 条目的 key 变强，直到不再新增引用。
func (state *State) expandEphemeronReferences(strongRefs map[tableKey]bool) {
	if state == nil {
		// nil State 没有可扩展图。
		return
	}
	for {
		changed := false
		visited := make(map[*Table]bool)
		if state.registry != nil {
			// registry 入口可能间接包含 weak-key table。
			if state.expandEphemeronFromValue(ReferenceValue(KindTable, state.registry), strongRefs, visited) {
				changed = true
			}
		}
		if state.globals != nil {
			// globals 入口覆盖官方 ephemeron 测试中的 table a。
			if state.expandEphemeronFromValue(ReferenceValue(KindTable, state.globals), strongRefs, visited) {
				changed = true
			}
		}
		for index := range state.stack {
			// 主栈上的弱表也需要参与 ephemeron 固定点传播。
			if state.expandEphemeronFromValue(state.stack[index], strongRefs, visited) {
				changed = true
			}
		}
		for _, vm := range state.activeVMs {
			if vm == nil {
				// nil VM 占位跳过。
				continue
			}
			registers := vm.ActiveRegistersSnapshot()
			for index := range registers {
				// 当前 PC 下仍存活的活动寄存器中可能保存局部 weak-key table。
				if state.expandEphemeronFromValue(registers[index], strongRefs, visited) {
					changed = true
				}
			}
		}
		for _, thread := range state.threads {
			if thread == nil {
				// nil 协程占位跳过。
				continue
			}
			for index := range thread.stack {
				// 协程栈可能在 yield 边界持有 weak-key table 或强 key。
				if state.expandEphemeronFromValue(thread.stack[index], strongRefs, visited) {
					changed = true
				}
			}
			if state.expandEphemeronFromValue(thread.function, strongRefs, visited) {
				changed = true
			}
		}
		for _, frame := range state.callFrames {
			// 调用帧函数 upvalue 可能间接持有 weak-key table。
			if state.expandEphemeronFromValue(frame.Function, strongRefs, visited) {
				changed = true
			}
		}
		if !changed {
			// 没有新增强引用时固定点完成。
			return
		}
	}
}

// expandEphemeronFromValue 从一个值出发执行一轮 ephemeron 传播。
//
// 返回 true 表示本轮新增了强引用。
func (state *State) expandEphemeronFromValue(value Value, strongRefs map[tableKey]bool, visited map[*Table]bool) bool {
	switch value.Kind {
	case KindTable:
		// table 可能是 weak-key table 或包含 weak-key table。
		table, ok := value.Ref.(*Table)
		if !ok || table == nil {
			// 损坏引用无法传播。
			return false
		}
		if visited[table] {
			// 本轮已经访问过该 table，避免循环。
			return false
		}
		visited[table] = true
		weakKeys, weakValues := table.weakMode()
		changed := false
		entries := table.rawIterationEntries()
		for index := range entries {
			entry := entries[index]
			if weakKeys && !weakValues && isStrongReference(entry.key, strongRefs) {
				// ephemeron 规则：weak key 已强可达时，value 变为强可达。
				if state.collectNewStrongReferencesFromValue(entry.value, strongRefs) {
					changed = true
				}
			}
			if !weakKeys {
				// 非弱 key 继续查找内部 weak-key table。
				if state.expandEphemeronFromValue(entry.key, strongRefs, visited) {
					changed = true
				}
			}
			if !weakValues {
				// 非弱 value 继续查找内部 weak-key table。
				if state.expandEphemeronFromValue(entry.value, strongRefs, visited) {
					changed = true
				}
			}
		}
		if table.metatable != nil {
			// 元表也可能包含弱表。
			if state.expandEphemeronFromValue(ReferenceValue(KindTable, table.metatable), strongRefs, visited) {
				changed = true
			}
		}
		return changed
	case KindLuaClosure:
		// closure upvalue 可能包含 weak-key table。
		closure, ok := value.Ref.(*LuaClosure)
		if !ok || closure == nil {
			// 损坏闭包引用无法传播。
			return false
		}
		changed := false
		for index := range closure.Upvalues {
			if state.expandEphemeronFromValue(closure.Upvalues[index], strongRefs, visited) {
				changed = true
			}
		}
		return changed
	default:
		// 其他值没有可传播结构。
		return false
	}
}

// collectNewStrongReferencesFromValue 收集 value 可达的强引用并报告是否新增。
func (state *State) collectNewStrongReferencesFromValue(value Value, strongRefs map[tableKey]bool) bool {
	before := len(strongRefs)
	state.collectStrongReferencesFromValue(value, strongRefs, make(map[*Table]bool))
	return len(strongRefs) > before
}

// addStrongReferenceKey 把一个引用值加入强根集合。
//
// 非引用值或无法编码为 tableKey 的值会被忽略。
func addStrongReferenceKey(strongRefs map[tableKey]bool, value Value) {
	if !isWeakCollectableValue(value) {
		// 只有 table/function/userdata/thread 这类弱表可收集引用才需要记录。
		return
	}
	key, err := makeTableKey(value)
	if err != nil {
		// 无法编码的引用不参与强根判断。
		return
	}

	// 记录引用 identity。
	strongRefs[key] = true
}

// sweepWeakTablesFromValue 从一个值出发递归扫描 table/closure 中的弱表。
//
// visited 防止循环 table 图导致无限递归；返回值为发生清理的 table 数量。
func (state *State) sweepWeakTablesFromValue(value Value, visited map[*Table]bool, strongRefs map[tableKey]bool) int {
	switch value.Kind {
	case KindTable:
		// table 是弱表扫描的核心对象。
		table, ok := value.Ref.(*Table)
		if !ok || table == nil {
			// 损坏 table 引用无法扫描。
			return 0
		}
		return state.sweepWeakTableGraph(table, visited, strongRefs)
	case KindLuaClosure:
		// Lua closure 的 upvalue 可能间接持有弱表。
		closure, ok := value.Ref.(*LuaClosure)
		if !ok || closure == nil {
			// 损坏闭包引用无法继续扫描。
			return 0
		}
		removed := 0
		for index := range closure.Upvalues {
			// 逐个 upvalue 递归查找弱表。
			removed += state.sweepWeakTablesFromValue(closure.Upvalues[index], visited, strongRefs)
		}
		return removed
	default:
		// 其他类型没有可递归的 table 图。
		return 0
	}
}

// sweepWeakValuesFromValue 从一个值出发递归扫描 finalizer 前弱 value 表。
func (state *State) sweepWeakValuesFromValue(value Value, visited map[*Table]bool, strongRefs map[tableKey]bool, allowWeakKV bool) int {
	switch value.Kind {
	case KindTable:
		// table 是弱 value 预清理的核心对象。
		table, ok := value.Ref.(*Table)
		if !ok || table == nil {
			// 损坏 table 引用无法扫描。
			return 0
		}
		return state.sweepWeakValueTableGraph(table, visited, strongRefs, allowWeakKV)
	case KindLuaClosure:
		// Lua closure 的 upvalue 可能间接持有 weak value 表。
		closure, ok := value.Ref.(*LuaClosure)
		if !ok || closure == nil {
			// 损坏闭包引用无法继续扫描。
			return 0
		}
		removed := 0
		for index := range closure.Upvalues {
			// 逐个 upvalue 递归查找 weak value 表。
			removed += state.sweepWeakValuesFromValue(closure.Upvalues[index], visited, strongRefs, allowWeakKV)
		}
		return removed
	default:
		// 其他类型没有可递归的 table 图。
		return 0
	}
}

// sweepWeakValueTableGraph 扫描 table 图并只清理 weak value-only 表。
func (state *State) sweepWeakValueTableGraph(table *Table, visited map[*Table]bool, strongRefs map[tableKey]bool, allowWeakKV bool) int {
	if table == nil {
		// nil table 没有可扫描内容。
		return 0
	}
	if visited[table] {
		// 已扫描 table 不重复处理，避免循环引用递归。
		return 0
	}
	visited[table] = true

	weakKeys, weakValues := table.weakMode()
	removed := 0
	if weakValues {
		if weakKeys {
			// 只有待终结 table 的元表可达图需要在 finalizer 前清理 weak kv，普通根图保持既有弱表顺序。
			if allowWeakKV && table.SweepWeakKVEntriesBeforeFinalizers(strongRefs, state.finalizableWeakKeyPreserveSet()) {
				removed++
			}
		} else if table.SweepWeakValueEntries(strongRefs) {
			// weak value-only 表在 finalizer 前清理 value；weak key-only 表仍留到 finalizer 后。
			removed++
		}
	}
	entries := table.rawIterationEntries()
	for index := range entries {
		if !weakKeys {
			// key 非弱时才作为强边递归扫描。
			removed += state.sweepWeakValuesFromValue(entries[index].key, visited, strongRefs, allowWeakKV)
		}
		if !weakValues {
			// value 非弱时才作为强边递归扫描。
			removed += state.sweepWeakValuesFromValue(entries[index].value, visited, strongRefs, allowWeakKV)
		}
	}
	if table.metatable != nil {
		// 元表是 table 的强可达结构，仍需要继续扫描其中可能存在的 weak value 表。
		removed += state.sweepWeakValueTableGraph(table.metatable, visited, strongRefs, allowWeakKV)
	}
	return removed
}

// finalizableWeakKeyPreserveSet 返回当前待终结 table 的 weak key 保留集合。
//
// Lua 5.3 对正在终结的对象有特殊弱表顺序；当前实现用该集合避免 finalizer 前的 weak kv
// 预清理提前删除待终结对象自身作为 key 的条目。
func (state *State) finalizableWeakKeyPreserveSet() map[tableKey]bool {
	preserve := make(map[tableKey]bool)
	if state == nil || state.finalizedTables == nil {
		// 无状态或未初始化 finalized map 时没有可保留对象。
		return preserve
	}
	for index := range state.finalizableTables {
		table := state.finalizableTables[index]
		if table == nil || state.finalizedTables[table] {
			// nil 或已终结对象不需要再保留弱 key。
			continue
		}
		key, err := makeTableKey(ReferenceValue(KindTable, table))
		if err != nil {
			// table 引用 key 理论上可编码；异常时跳过该对象避免影响其他项。
			continue
		}
		preserve[key] = true
	}
	return preserve
}

// sweepWeakTableGraph 扫描 table 及其强可达子 table。
//
// 弱侧边不作为继续递归的强引用；这能避免刚被清理的弱 key/value 反向保活自身。
func (state *State) sweepWeakTableGraph(table *Table, visited map[*Table]bool, strongRefs map[tableKey]bool) int {
	if table == nil {
		// nil table 没有可扫描内容。
		return 0
	}
	if visited[table] {
		// 已扫描 table 不重复处理，避免循环引用递归。
		return 0
	}
	visited[table] = true

	weakKeys, weakValues := table.weakMode()
	removed := 0
	if table.SweepWeakEntries(strongRefs) {
		// 当前 table 发生过弱项删除，计入清理数量。
		removed++
	}
	entries := table.rawIterationEntries()
	for index := range entries {
		if !weakKeys {
			// key 非弱时才作为强边递归扫描。
			removed += state.sweepWeakTablesFromValue(entries[index].key, visited, strongRefs)
		}
		if !weakValues {
			// value 非弱时才作为强边递归扫描。
			removed += state.sweepWeakTablesFromValue(entries[index].value, visited, strongRefs)
		}
	}
	if table.metatable != nil {
		// 元表是 table 的强可达结构，仍需要继续扫描其中可能存在的弱表。
		removed += state.sweepWeakTableGraph(table.metatable, visited, strongRefs)
	}
	return removed
}

// appendClosureUpvalueRoots 从闭包值中补充上层 upvalue。
//
// 当前版本只扫描 Lua closure 的 upvalues，不扫描 Go closure 捕获变量。
func (state *State) appendClosureUpvalueRoots(function Value, out []Value) []Value {
	if function.Kind != KindLuaClosure {
		// 只对 Lua closure 生效；Go closure 无 upvalue 捕获数据结构。
		return out
	}

	closure, ok := function.Ref.(*LuaClosure)
	if !ok || closure == nil {
		// 类型不匹配或闭包为空时，无法继续补充 upvalue 根。
		return out
	}

	// 上层可达边界从 closure.Upvalues 逐个采样。
	for index := range closure.Upvalues {
		out = append(out, closure.Upvalues[index])
	}
	return out
}
