package runtime

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// TestGCRootsStateRoot 验证第一阶段 State root 中包含主线程起点。
//
// 该测试对应 TODO “设计第一阶段 GC 策略”与“标记 State root”的最小验收。
func TestGCRootsStateRoot(t *testing.T) {
	state := NewState()
	snapshot := state.SnapshotGCRoots()
	stateRoots, ok := snapshot.Batches[GCRootTypeState]
	if !ok {
		// 快照缺失 state-root 分类说明采样流程未建立。
		t.Fatalf("state root batch missing")
	}
	if len(stateRoots) != 1 {
		// 当前实现仅有主线程一个状态根，长度应为 1。
		t.Fatalf("expect exactly one state root, got %d", len(stateRoots))
	}
	if !stateRoots[0].RawEqual(ReferenceValue(KindThread, state.mainThread)) {
		// State root 必须可直接追溯到主线程对象。
		t.Fatalf("state root should include main thread")
	}
}

// TestGCRootsRegistryRoot 验证 registry root 覆盖 registry 与 _G。
//
// registry 和 globals 是最先级别可达入口，本测试覆盖 TODO “标记 registry root”。
func TestGCRootsRegistryRoot(t *testing.T) {
	state := NewState()
	snapshot := state.SnapshotGCRoots()
	registryRoots := snapshot.Batches[GCRootTypeRegistry]
	if len(registryRoots) != 2 {
		// 当前实现应采样 registry 与 globals 两类值。
		t.Fatalf("expect 2 registry roots, got %d", len(registryRoots))
	}
	if !registryRoots[0].RawEqual(ReferenceValue(KindTable, state.registry)) {
		// 第一项应当为 registry table。
		t.Fatalf("first registry root should be registry table")
	}
	if !registryRoots[1].RawEqual(ReferenceValue(KindTable, state.globals)) {
		// 第二项应当为 _G 视角的全局表。
		t.Fatalf("second registry root should be globals table")
	}
}

// TestGCRootsStackRoot 验证 stack root 采集当前主栈中的可达值。
//
// 该测试对应 TODO “标记 stack root”。
func TestGCRootsStackRoot(t *testing.T) {
	state := NewState()
	state.Push(IntegerValue(1))
	state.Push(StringValue("a"))
	snapshot := state.SnapshotGCRoots()
	stackRoots := snapshot.Batches[GCRootTypeStack]
	if len(stackRoots) != 2 {
		// main stack 已压入2个值，根快照也应完整保留。
		t.Fatalf("expect two stack roots, got %d", len(stackRoots))
	}
	if !stackRoots[0].RawEqual(IntegerValue(1)) || !stackRoots[1].RawEqual(StringValue("a")) {
		// 栈值顺序与 Push 顺序保持一致，便于单测可复现。
		t.Fatalf("unexpected stack root order/value: %#v", stackRoots)
	}
}

// TestGCRootsClosureUpvalueRoot 验证 closure 和 upvalue 被纳入根采样。
//
// 该测试对应 TODO “标记 closure/upvalue root”。
func TestGCRootsClosureUpvalueRoot(t *testing.T) {
	state := NewState()
	closure := &LuaClosure{
		Upvalues: []Value{IntegerValue(7), StringValue("x"), ReferenceValue(KindTable, NewTable())},
	}
	thread := state.NewThread(ReferenceValue(KindLuaClosure, closure))
	snapshot := state.SnapshotGCRoots()
	closureRoots := snapshot.Batches[GCRootTypeClosureUpvalue]
	if len(closureRoots) == 0 {
		// thread.function 为 Lua closure 时，应出现至少本体一个引用。
		t.Fatalf("expect closure roots, got 0")
	}

	if !containsValue(closureRoots, thread.function) {
		// 闭包本体应纳入 closure 根集合。
		t.Fatalf("closure root should include thread entry closure")
	}
	for index := 0; index < len(closure.Upvalues); index++ {
		// 每个 upvalue 都应可见，保证第一阶段 upvalue 根可追踪。
		if !containsValue(closureRoots, closure.Upvalues[index]) {
			t.Fatalf("closure root should include upvalue #%d", index)
		}
	}
}

// TestGCRootsTableKeyValueRoot 验证 table 中 key/value 会进入专门 root 扫描。
//
// table key/value 扫描用于为第一阶段 Lua 风格的 GC 可达性补齐 table 内部边。
func TestGCRootsTableKeyValueRoot(t *testing.T) {
	state := NewState()
	subTable := NewTable()
	tableObj := NewTable()

	tableObj.RawSetString("inner", ReferenceValue(KindTable, subTable))
	tableObj.RawSetInteger(1, ReferenceValue(KindTable, subTable))
	state.Push(ReferenceValue(KindTable, tableObj))

	snapshot := state.SnapshotGCRoots()
	tableKVRoots := snapshot.Batches[GCRootTypeTableKeyValue]
	if len(tableKVRoots) == 0 {
		// tableObj 入栈后应触发 table key/value 扫描，返回值不能为空。
		t.Fatalf("expect table key/value roots, got 0")
	}
	if !containsValue(tableKVRoots, StringValue("inner")) {
		// string key 是扫描入口之一，验证 key 被采样。
		t.Fatalf("table kv roots should include string key")
	}
	if !containsValue(tableKVRoots, ReferenceValue(KindTable, subTable)) {
		// value 下沉到 table value 区域，必须可见。
		t.Fatalf("table kv roots should include value table")
	}
}

// TestGCRootsCoroutineStackRoot 验证每个协程栈快照被独立采集。
//
// 该测试对应 TODO “标记 coroutine stack”，覆盖非主线程的 stack 作为可达入口。
func TestGCRootsCoroutineStackRoot(t *testing.T) {
	state := NewState()
	thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 简化协程入口，避免 Resume 侧效应影响 stack root 测试本身。
		return values, nil
	})))

	thread.stack = []Value{
		IntegerValue(7),
		StringValue("co-stack"),
	}

	snapshot := state.SnapshotGCRoots()
	coroutineStackRoots := snapshot.Batches[GCRootTypeCoroutineStack]
	if len(coroutineStackRoots) != 2 {
		// 当前 thread.stack 按长度完整采样，应返回 2 个元素。
		t.Fatalf("expect coroutine stack roots length 2, got %d", len(coroutineStackRoots))
	}
	if !containsValue(coroutineStackRoots, IntegerValue(7)) {
		// 协程栈中的参数需要保留，供后续 root 遍历。
		t.Fatalf("coroutine stack roots should include integer arg")
	}
	if !containsValue(coroutineStackRoots, StringValue("co-stack")) {
		// 协程栈中的 string 值同样应采样。
		t.Fatalf("coroutine stack roots should include string arg")
	}
}

// TestGCLifecycleStress 验证在高负载下快照与关闭路径仍可稳定运行。
//
// 该测试覆盖“生命周期压力测试” TODO：大量 thread、table 与 userdata 注册，
// 验证快照数量级与 close 最终释放路径。
func TestGCLifecycleStress(t *testing.T) {
	const (
		userdataCount = 200
		tableCount    = 200
		threadCount   = 100
	)

	state := NewState()
	var finalizerCalled int64

	for index := 0; index < userdataCount; index++ {
		// 构造 userdata 时使用 index 副本，避免闭包捕获迭代变量导致回调混淆。
		indexCopy := index
		userdata := NewUserdataWithFinalizer(indexCopy, func(payload any) error {
			// 记录 finalizer 命中次数，验证所有注册 userdata 都有关闭语义。
			atomic.AddInt64(&finalizerCalled, 1)

			// 人为注入可恢复边界，验证 Close 不受 error/panic 干扰。
			if payload.(int)%19 == 0 {
				panic("stress finalizer panic")
			}
			if payload.(int)%23 == 0 {
				return fmt.Errorf("stress finalizer error")
			}
			return nil
		})

		if err := state.RegisterUserdata(userdata); err != nil {
			// 注册失败会影响压力测试可行性，直接报错退出。
			t.Fatalf("register userdata #%d failed: %v", index, err)
		}
	}

	for index := 0; index < tableCount; index++ {
		// 每个 table 都挂接到 globals 作为 root，放大 table key/value 扫描负载。
		rootTable := NewTable()
		nestedTable := NewTable()
		nestedTable.RawSetString("seq", IntegerValue(int64(index)))
		rootTable.RawSetString("nested", ReferenceValue(KindTable, nestedTable))
		state.SetGlobal(fmt.Sprintf("k_%d", index), ReferenceValue(KindTable, rootTable))
	}

	for index := 0; index < threadCount; index++ {
		indexValue := int64(index)
		// 每个协程都带 upvalue 与独立栈，拉起 coroutine stack 与 closure/upvalue 两类采样。
		closure := &LuaClosure{
			Upvalues: []Value{IntegerValue(indexValue), ReferenceValue(KindTable, NewTable())},
		}
		thread := state.NewThread(ReferenceValue(KindLuaClosure, closure))
		thread.stack = []Value{
			IntegerValue(indexValue),
			ReferenceValue(KindTable, closure.Upvalues[1].Ref.(*Table)),
		}
		_ = thread
	}

	snapshot := state.SnapshotGCRoots()
	coroutineRoots := snapshot.Batches[GCRootTypeCoroutineStack]
	if len(coroutineRoots) != threadCount*2 {
		// 每个 thread.stack 含 2 个值，采样应完整包含。
		t.Fatalf("unexpected coroutine stack root count: got %d want %d", len(coroutineRoots), threadCount*2)
	}

	closureUpvalueRoots := snapshot.Batches[GCRootTypeClosureUpvalue]
	if len(closureUpvalueRoots) < threadCount*2 {
		// 每个 Lua closure 应携带 2 个 upvalue，至少应出现对应可达根。
		t.Fatalf("unexpected closure root count: got %d want >=%d", len(closureUpvalueRoots), threadCount*2)
	}

	tableRoots := snapshot.Batches[GCRootTypeTableKeyValue]
	if len(tableRoots) < tableCount*2 {
		// globals table 大量挂接时，至少应覆盖字符串 key 与 nested 值。
		t.Fatalf("unexpected table kv root count: got %d want >=%d", len(tableRoots), tableCount*2)
	}

	state.Close()

	if got := atomic.LoadInt64(&finalizerCalled); got != userdataCount {
		// finalizer 并发回收阶段应覆盖全部 userdata，尽管中途有 error/panic。
		t.Fatalf("finalizer should execute once per userdata: got %d want %d", got, userdataCount)
	}
}

// TestSweepWeakTablesEphemeronChain 验证 weak-key table 的 ephemeron 链固定点传播。
//
// 当最新 key 是强根时，value 中引用的前一个 key 应继续变强，直到整条链都被保留。
func TestSweepWeakTablesEphemeronChain(t *testing.T) {
	state := NewState()
	table := NewTable()
	metatable := NewTable()
	metatable.RawSetString("__mode", StringValue("k"))
	table.SetMetatable(metatable)

	var current Value = NilValue()
	for index := 0; index < 10; index++ {
		// 每轮创建新 key，并在 value 的嵌套 table 中保存前一个 key。
		key := ReferenceValue(KindTable, NewTable())
		nested := NewTable()
		nested.RawSetInteger(1, current)
		value := NewTable()
		value.RawSetString("k", ReferenceValue(KindTable, nested))
		if err := table.RawSet(key, ReferenceValue(KindTable, value)); err != nil {
			// weak-key table 写入不应失败。
			t.Fatalf("raw set ephemeron chain failed: %v", err)
		}
		current = key
	}
	state.Push(ReferenceValue(KindTable, table))
	state.Push(current)

	state.SweepWeakTables()

	count := 0
	next := current
	for !next.IsNil() {
		value, err := table.RawGet(next)
		if err != nil {
			// table key 读取不应失败。
			t.Fatalf("raw get ephemeron key failed: %v", err)
		}
		if value.IsNil() {
			// 固定点传播失败会导致链中间断开。
			t.Fatalf("ephemeron chain missing at %d", count)
		}
		valueTable := value.Ref.(*Table)
		nestedValue := valueTable.RawGetString("k")
		nestedTable := nestedValue.Ref.(*Table)
		next = nestedTable.RawGetInteger(1)
		count++
	}
	if count != 10 {
		// 整条链必须都被保留。
		t.Fatalf("ephemeron chain count = %d, want 10", count)
	}
}

// TestSweepWeakValuesBeforeFinalizersClearsWeakKVMetatableGraph 验证 finalizer 前弱表顺序。
//
// Lua 5.3 在执行 table `__gc` 前，会让待终结对象元表图里的普通 weak key/value 项不可见；
// 但普通 root 图中的 weak kv 表不能被这个预清理阶段全局清空。
func TestSweepWeakValuesBeforeFinalizersClearsWeakKVMetatableGraph(t *testing.T) {
	state := NewState()
	target := NewTable()
	finalizerMetatable := NewTable()
	weakKV := NewTable()
	weakKVMetatable := NewTable()
	weakKVMetatable.RawSetString("__mode", StringValue("kv"))
	weakKV.SetMetatable(weakKVMetatable)
	if err := weakKV.RawSet(ReferenceValue(KindTable, NewTable()), IntegerValue(1)); err != nil {
		// 待清理 weak key 写入不应失败。
		t.Fatalf("raw set weak key failed: %v", err)
	}
	if err := weakKV.RawSet(IntegerValue(0), ReferenceValue(KindTable, NewTable())); err != nil {
		// 待清理 weak value 写入不应失败。
		t.Fatalf("raw set weak value failed: %v", err)
	}
	finalizerMetatable.RawSetString("x", ReferenceValue(KindTable, weakKV))
	finalizerMetatable.RawSetString("__gc", BooleanValue(true))
	target.SetMetatable(finalizerMetatable)
	state.RegisterTableFinalizer(target)

	ordinaryWeakKV := NewTable()
	ordinaryMetatable := NewTable()
	ordinaryMetatable.RawSetString("__mode", StringValue("kv"))
	ordinaryWeakKV.SetMetatable(ordinaryMetatable)
	if err := ordinaryWeakKV.RawSet(ReferenceValue(KindTable, NewTable()), IntegerValue(1)); err != nil {
		// 普通 weak kv 表写入不应失败。
		t.Fatalf("raw set ordinary weak key failed: %v", err)
	}
	state.Push(ReferenceValue(KindTable, ordinaryWeakKV))

	state.SweepWeakValuesBeforeFinalizers()
	if len(weakKV.rawIterationEntries()) != 0 {
		// 待终结对象元表图中的 weak kv 项必须在 finalizer 前消失。
		t.Fatalf("finalizer metatable weak kv entries should be cleared before __gc")
	}
	if len(ordinaryWeakKV.rawIterationEntries()) == 0 {
		// 普通 root 图中的 weak kv 表不应被 finalizer 预清理误删。
		t.Fatalf("ordinary weak kv entries should not be globally cleared before finalizer")
	}
}

// TestTableFinalizerDelaysSuspendedThreadCycleOneCollection 验证协程自环 finalizer 延迟一轮。
//
// Lua 5.3 的 thread/closure/upvalue 周期需要两次 collectgarbage 才会运行 `__gc`；当前兼容层
// 发现待终结 table 仍被 suspended coroutine 图引用时，第一轮只标记延迟，第二轮再执行。
func TestTableFinalizerDelaysSuspendedThreadCycleOneCollection(t *testing.T) {
	state := NewState()
	target := NewTable()
	metatable := NewTable()
	metatable.RawSetString("__gc", ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 测试只关心 finalizer 是否被调用，不需要返回值。
		return nil, nil
	})))
	target.SetMetatable(metatable)
	state.RegisterTableFinalizer(target)

	thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 测试手动设置 suspended 状态，不实际执行入口。
		return nil, nil
	})))
	thread.stack = []Value{ReferenceValue(KindTable, target)}
	thread.status = CoroutineStatusSuspended

	called := 0
	state.SetTableFinalizerRunner(func(tableValue Value, finalizerValue Value) error {
		// 每次 runner 执行都计数，便于验证延迟轮次。
		called++
		return nil
	})
	if err := state.FullGC(1); err != nil {
		// 第一轮不应报错。
		t.Fatalf("first FullGC failed: %v", err)
	}
	if called != 0 {
		// suspended thread 图仍引用目标时，第一轮不应运行 finalizer。
		t.Fatalf("finalizer called during first cycle: %d", called)
	}
	if err := state.FullGC(1); err != nil {
		// 第二轮应允许运行 finalizer。
		t.Fatalf("second FullGC failed: %v", err)
	}
	if called != 1 {
		// 第二轮必须正好执行一次 finalizer。
		t.Fatalf("finalizer call count = %d, want 1", called)
	}
}

// containsValue 判断 slice 是否包含 target（按 RawEqual，避免 identity 偏差）。
//
// upvalue 语义验证只关注值等价，不要求同一个实例指针。
func containsValue(values []Value, target Value) bool {
	for index := 0; index < len(values); index++ {
		if values[index].RawEqual(target) {
			return true
		}
	}
	return false
}
