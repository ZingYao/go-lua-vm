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

// TestGCRootsActiveVMRegisterRoot 验证活动 VM 寄存器被纳入 stack root。
//
// active VM 的局部寄存器是 Lua 调用中的可达值；native/C API 回调期间也可能触发 GC root
// 快照，必须把仍活动的寄存器作为栈根并继续采样其中 table 的 key/value。
func TestGCRootsActiveVMRegisterRoot(t *testing.T) {
	state := NewState()
	activeTable := NewTable()
	activeTable.RawSetString("marker", StringValue("active"))

	vm := NewVM(2)
	if err := vm.SetRegister(0, StringValue("active-register")); err != nil {
		// 测试初始化寄存器失败说明 VM 基础写入异常，直接终止。
		t.Fatalf("set active register string failed: %v", err)
	}
	if err := vm.SetRegister(1, ReferenceValue(KindTable, activeTable)); err != nil {
		// table 寄存器是本测试的 key/value 采样入口，写入失败时无法继续。
		t.Fatalf("set active register table failed: %v", err)
	}

	state.PushActiveVM(vm)
	defer state.PopActiveVM(vm)

	snapshot := state.SnapshotGCRoots()
	stackRoots := snapshot.Batches[GCRootTypeStack]
	if !containsValue(stackRoots, StringValue("active-register")) {
		// 活动 VM 的普通寄存器值必须作为运行栈根保留。
		t.Fatalf("stack roots should include active VM string register: %#v", stackRoots)
	}
	if !containsValue(stackRoots, ReferenceValue(KindTable, activeTable)) {
		// 活动 VM 的 table 寄存器必须作为运行栈根保留。
		t.Fatalf("stack roots should include active VM table register: %#v", stackRoots)
	}

	tableKVRoots := snapshot.Batches[GCRootTypeTableKeyValue]
	if !containsValue(tableKVRoots, StringValue("marker")) || !containsValue(tableKVRoots, StringValue("active")) {
		// table 寄存器进入 root 后，其 key/value 也必须继续采样。
		t.Fatalf("table kv roots should include active VM table content: %#v", tableKVRoots)
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

// TestGCRootsLuaClosureUpvalueCellCurrentValue 验证 Lua closure root 使用 upvalue cell 当前值。
//
// 共享 upvalue cell 是闭包运行期真实读写来源；创建时 Upvalues 快照可能在外层局部变量变化后过期。
// GC root 采样必须扫描当前 cell，避免构造期或闭包回调中被外层局部更新的新对象失根。
func TestGCRootsLuaClosureUpvalueCellCurrentValue(t *testing.T) {
	state := NewState()
	staleTable := NewTable()
	currentTable := NewTable()
	closure := &LuaClosure{
		Upvalues:     []Value{ReferenceValue(KindTable, staleTable)},
		UpvalueCells: []*UpvalueCell{NewClosedUpvalueCell(ReferenceValue(KindTable, currentTable))},
	}
	thread := state.NewThread(ReferenceValue(KindLuaClosure, closure))

	snapshot := state.SnapshotGCRoots()
	closureRoots := snapshot.Batches[GCRootTypeClosureUpvalue]
	if !containsValue(closureRoots, thread.function) {
		// 闭包本体仍应作为 closure root 出现。
		t.Fatalf("closure root should include thread entry closure")
	}
	if !containsValue(closureRoots, ReferenceValue(KindTable, currentTable)) {
		// 当前 upvalue cell 值必须进入 root，而不是只保留创建时快照。
		t.Fatalf("closure roots should include current upvalue cell value: %#v", closureRoots)
	}
	if containsValue(closureRoots, ReferenceValue(KindTable, staleTable)) {
		// 共享 cell 存在时过期快照不应继续作为当前 upvalue 强根。
		t.Fatalf("closure roots should not retain stale upvalue snapshot: %#v", closureRoots)
	}
}

// TestGCRootsGoClosureWithUpvaluesRoot 验证显式 Go closure upvalue 被纳入根采样。
//
// native C closure、string.gmatch 与 dofile trampoline 都会使用 GoClosureWithUpvalues；这些
// upvalue 对 Lua 来说是可见强引用，必须与 Lua closure upvalue 一样进入 GC root。
func TestGCRootsGoClosureWithUpvaluesRoot(t *testing.T) {
	state := NewState()
	closure := &GoClosureWithUpvalues{
		Function: GoResultsFunction(func(values ...Value) ([]Value, error) {
			// 测试只关注 root 采样，不实际调用该 closure。
			return values, nil
		}),
		Upvalues: []Value{StringValue("go-upvalue"), ReferenceValue(KindTable, NewTable())},
	}
	thread := state.NewThread(ReferenceValue(KindGoClosure, closure))

	snapshot := state.SnapshotGCRoots()
	closureRoots := snapshot.Batches[GCRootTypeClosureUpvalue]
	if !containsValue(closureRoots, thread.function) {
		// Go closure 本体应纳入 closure 根集合。
		t.Fatalf("closure root should include Go thread entry closure")
	}
	for index := 0; index < len(closure.Upvalues); index++ {
		// 每个显式 Go closure upvalue 都应可见，保证 native C closure 持值可追踪。
		if !containsValue(closureRoots, closure.Upvalues[index]) {
			t.Fatalf("closure root should include Go upvalue #%d", index)
		}
	}
}

// TestGCRootsStackGoClosureWithUpvaluesRoot 验证栈上显式 Go closure upvalue 被纳入根采样。
//
// native C closure 作为普通 Lua 值压在栈或寄存器中时，本体已经属于 stack root；它携带的显式
// upvalue 也必须进入 closure root，避免 root 快照漏掉构造期关联表或 registry 引用。
func TestGCRootsStackGoClosureWithUpvaluesRoot(t *testing.T) {
	state := NewState()
	upvalueTable := NewTable()
	upvalueTable.RawSetString("marker", StringValue("stack-go-upvalue"))
	closure := &GoClosureWithUpvalues{
		Function: GoResultsFunction(func(values ...Value) ([]Value, error) {
			// 测试只关注 root 采样，不实际调用该 closure。
			return values, nil
		}),
		Upvalues: []Value{StringValue("stack-upvalue"), ReferenceValue(KindTable, upvalueTable)},
	}
	stackClosure := ReferenceValue(KindGoClosure, closure)
	if err := state.Push(stackClosure); err != nil {
		// Go closure 需要作为主栈值进入根采样。
		t.Fatalf("push Go closure failed: %v", err)
	}

	snapshot := state.SnapshotGCRoots()
	stackRoots := snapshot.Batches[GCRootTypeStack]
	if !containsValue(stackRoots, stackClosure) {
		// closure 本体应由 stack root 承载。
		t.Fatalf("stack root should include Go closure")
	}
	closureRoots := snapshot.Batches[GCRootTypeClosureUpvalue]
	for index := 0; index < len(closure.Upvalues); index++ {
		// 每个显式 upvalue 都应从栈上 closure 展开到 closure root。
		if !containsValue(closureRoots, closure.Upvalues[index]) {
			t.Fatalf("closure root should include stack Go upvalue #%d", index)
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

// TestGCRootsUserdataAssociationRoot 验证 userdata 关联边进入根快照。
//
// Lua 5.3 full userdata 的 user value 和 raw metatable 都是对象结构强边；native C 模块常用
// user value 保存 ktable/capture 关联表，root 快照必须能观察这些关联值及其 table 内容。
func TestGCRootsUserdataAssociationRoot(t *testing.T) {
	state := NewState()
	userValueTable := NewTable()
	userValueTable.RawSetString("uv-key", StringValue("uv-value"))
	metatable := NewTable()
	metatable.RawSetString("__name", StringValue("native-ud"))
	userdata := NewUserdata("payload")
	userdata.UserValue = ReferenceValue(KindTable, userValueTable)
	if err := userdata.SetMetatable(metatable); err != nil {
		// 测试初始化 raw 元表失败说明 userdata 基础语义异常。
		t.Fatalf("set userdata metatable failed: %v", err)
	}
	if err := state.Push(userdata.Value()); err != nil {
		// userdata 需要作为栈根进入快照。
		t.Fatalf("push userdata failed: %v", err)
	}

	snapshot := state.SnapshotGCRoots()
	userdataRoots := snapshot.Batches[GCRootTypeUserdataAssociation]
	if !containsValue(userdataRoots, ReferenceValue(KindTable, userValueTable)) {
		// user value table 必须作为 userdata 关联根可见。
		t.Fatalf("userdata association roots should include user value table: %#v", userdataRoots)
	}
	if !containsValue(userdataRoots, ReferenceValue(KindTable, metatable)) {
		// raw metatable 也必须作为 userdata 关联根可见。
		t.Fatalf("userdata association roots should include raw metatable: %#v", userdataRoots)
	}

	tableKVRoots := snapshot.Batches[GCRootTypeTableKeyValue]
	if !containsValue(tableKVRoots, StringValue("uv-key")) || !containsValue(tableKVRoots, StringValue("uv-value")) {
		// user value table 内容需要展开到 table key/value 根。
		t.Fatalf("table kv roots should include user value table content: %#v", tableKVRoots)
	}
	if !containsValue(tableKVRoots, StringValue("__name")) || !containsValue(tableKVRoots, StringValue("native-ud")) {
		// userdata raw metatable 内容也需要展开到 table key/value 根。
		t.Fatalf("table kv roots should include userdata metatable content: %#v", tableKVRoots)
	}
}

// TestSweepWeakTablesKeepsUserdataAssociationValues 验证 userdata 关联边保活 weak value。
//
// weak value 表不能把 value 自身当强根；只有 userdata.UserValue 和 raw metatable 可达时，
// 对应 value 才应在 SweepWeakTables 后保留。该门禁覆盖 native userdata 生命周期通用语义，
// 不依赖具体 C 模块实现。
func TestSweepWeakTablesKeepsUserdataAssociationValues(t *testing.T) {
	state := NewState()
	weakValueTable := NewTable()
	weakMetatable := NewTable()
	weakMetatable.RawSetString("__mode", StringValue("v"))
	weakValueTable.SetMetatable(weakMetatable)
	state.SetGlobal("weak", ReferenceValue(KindTable, weakValueTable))

	userValueTarget := NewTable()
	metatableTarget := NewTable()
	weakValueTable.RawSetString("from-user-value", ReferenceValue(KindTable, userValueTarget))
	weakValueTable.RawSetString("from-metatable", ReferenceValue(KindTable, metatableTarget))

	userdata := NewUserdata("payload")
	userValueRoot := NewTable()
	userValueRoot.RawSetString("target", ReferenceValue(KindTable, userValueTarget))
	userdata.UserValue = ReferenceValue(KindTable, userValueRoot)
	metatableRoot := NewTable()
	metatableRoot.RawSetString("target", ReferenceValue(KindTable, metatableTarget))
	if err := userdata.SetMetatable(metatableRoot); err != nil {
		// 测试初始化 raw 元表失败说明 userdata 基础语义异常。
		t.Fatalf("set userdata metatable failed: %v", err)
	}
	if err := state.Push(userdata.Value()); err != nil {
		// userdata 需要作为强根进入 weak sweep。
		t.Fatalf("push userdata failed: %v", err)
	}

	state.SweepWeakTables()
	if got := weakValueTable.RawGetString("from-user-value"); got.IsNil() {
		// user value 间接引用的对象必须保活 weak value 项。
		t.Fatalf("weak value reachable through userdata user value was removed")
	}
	if got := weakValueTable.RawGetString("from-metatable"); got.IsNil() {
		// raw metatable 间接引用的对象必须保活 weak value 项。
		t.Fatalf("weak value reachable through userdata metatable was removed")
	}
}

// TestSweepWeakTablesKeepsGoClosureUpvalueValues 验证显式 Go closure upvalue 保活 weak value。
//
// native C closure upvalue 常用于保存 registry、ktable 或 C 模块构造期对象；weak sweep 必须把这些
// upvalue 视为强边，否则构造期临时对象可能被错误清理。
func TestSweepWeakTablesKeepsGoClosureUpvalueValues(t *testing.T) {
	state := NewState()
	weakValueTable := NewTable()
	weakMetatable := NewTable()
	weakMetatable.RawSetString("__mode", StringValue("v"))
	weakValueTable.SetMetatable(weakMetatable)
	state.SetGlobal("weak", ReferenceValue(KindTable, weakValueTable))

	upvalueTarget := NewTable()
	weakValueTable.RawSetString("from-go-upvalue", ReferenceValue(KindTable, upvalueTarget))

	upvalueRoot := NewTable()
	upvalueRoot.RawSetString("target", ReferenceValue(KindTable, upvalueTarget))
	closure := &GoClosureWithUpvalues{
		Function: GoResultsFunction(func(values ...Value) ([]Value, error) {
			// 测试只关注 weak sweep 可达图，不实际调用该 closure。
			return values, nil
		}),
		Upvalues: []Value{ReferenceValue(KindTable, upvalueRoot)},
	}
	if err := state.Push(ReferenceValue(KindGoClosure, closure)); err != nil {
		// Go closure 需要作为主栈强根进入 weak sweep。
		t.Fatalf("push Go closure failed: %v", err)
	}

	state.SweepWeakTables()
	if got := weakValueTable.RawGetString("from-go-upvalue"); got.IsNil() {
		// Go closure upvalue 间接引用的对象必须保活 weak value 项。
		t.Fatalf("weak value reachable through Go closure upvalue was removed")
	}
}

// TestSweepWeakTablesKeepsLuaClosureUpvalueCellValues 验证 Lua closure upvalue cell 保活 weak value。
//
// 内层 Lua closure 捕获外层局部后，外层局部可能在闭包创建后改写；weak sweep 必须跟随共享
// cell 的当前值，而不是只扫描创建时 Upvalues 快照。
func TestSweepWeakTablesKeepsLuaClosureUpvalueCellValues(t *testing.T) {
	state := NewState()
	weakValueTable := NewTable()
	weakMetatable := NewTable()
	weakMetatable.RawSetString("__mode", StringValue("v"))
	weakValueTable.SetMetatable(weakMetatable)
	state.SetGlobal("weak", ReferenceValue(KindTable, weakValueTable))

	upvalueTarget := NewTable()
	weakValueTable.RawSetString("from-lua-upvalue-cell", ReferenceValue(KindTable, upvalueTarget))

	upvalueRoot := NewTable()
	upvalueRoot.RawSetString("target", ReferenceValue(KindTable, upvalueTarget))
	staleRoot := NewTable()
	closure := &LuaClosure{
		Upvalues:     []Value{ReferenceValue(KindTable, staleRoot)},
		UpvalueCells: []*UpvalueCell{NewClosedUpvalueCell(ReferenceValue(KindTable, upvalueRoot))},
	}
	if err := state.Push(ReferenceValue(KindLuaClosure, closure)); err != nil {
		// Lua closure 需要作为主栈强根进入 weak sweep。
		t.Fatalf("push Lua closure failed: %v", err)
	}

	state.SweepWeakTables()
	if got := weakValueTable.RawGetString("from-lua-upvalue-cell"); got.IsNil() {
		// 当前 upvalue cell 间接引用的对象必须保活 weak value 项。
		t.Fatalf("weak value reachable through Lua closure upvalue cell was removed")
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

// TestFullGCRefreshesReachableWeakTableFlag 验证完整 GC 后会收敛自动 weak sweep 标志。
//
// 弱表一旦出现需要登记自动清理，但当该弱表已从可达图移除时，后续普通分配不应继续承担
// 周期性全图 weak sweep；显式 collectgarbage 仍会执行完整弱表清理。
func TestFullGCRefreshesReachableWeakTableFlag(t *testing.T) {
	state := NewState()
	weakTable := NewTable()
	metatable := NewTable()
	metatable.RawSetString("__mode", StringValue("v"))
	weakTable.SetMetatable(metatable)

	state.RegisterWeakTable(weakTable)
	state.SetGlobal("weak", ReferenceValue(KindTable, weakTable))
	if !state.hasWeakTables {
		// 登记后必须开启自动 weak sweep 标志。
		t.Fatalf("weak table flag should be set after registration")
	}
	if err := state.FullGC(1); err != nil {
		// 可达弱表存在时完整 GC 不应失败。
		t.Fatalf("FullGC with reachable weak table failed: %v", err)
	}
	if !state.hasWeakTables {
		// 弱表仍挂在 globals 中，标志不能被误清。
		t.Fatalf("reachable weak table should keep weak sweep flag")
	}

	state.SetGlobal("weak", NilValue())
	if err := state.FullGC(1); err != nil {
		// 移除 root 后完整 GC 应重新计算弱表可达性。
		t.Fatalf("FullGC after dropping weak table failed: %v", err)
	}
	if state.hasWeakTables {
		// 不可达弱表不应让后续分配继续触发自动 weak sweep。
		t.Fatalf("unreachable weak table should clear weak sweep flag")
	}
}

// TestFullGCKeepsWeakTableFlagThroughUserdataAssociation 验证 userdata 关联边保留弱表扫描标志。
//
// native full userdata 常通过 user value 或 raw metatable 挂接构造期表；如果弱表只从这些关联边
// 可达，完整 GC 后仍必须保留 hasWeakTables，否则后续自动 GC 会跳过必要的 weak sweep。
func TestFullGCKeepsWeakTableFlagThroughUserdataAssociation(t *testing.T) {
	tests := []struct {
		name   string
		attach func(userdata *Userdata, associatedTable *Table) error
	}{
		{
			name: "user-value",
			attach: func(userdata *Userdata, associatedTable *Table) error {
				// user value 是 Lua 5.3 full userdata 的普通强关联边。
				userdata.UserValue = ReferenceValue(KindTable, associatedTable)
				return nil
			},
		},
		{
			name: "raw-metatable",
			attach: func(userdata *Userdata, associatedTable *Table) error {
				// raw metatable 同样是 full userdata 的强关联边。
				return userdata.SetMetatable(associatedTable)
			},
		},
	}

	for index := range tests {
		testCase := tests[index]
		t.Run(testCase.name, func(t *testing.T) {
			state := NewState()
			weakTable := NewTable()
			weakMetatable := NewTable()
			weakMetatable.RawSetString("__mode", StringValue("v"))
			weakTable.SetMetatable(weakMetatable)
			state.RegisterWeakTable(weakTable)

			associatedTable := NewTable()
			associatedTable.RawSetString("weak", ReferenceValue(KindTable, weakTable))
			userdata := NewUserdata("payload")
			if err := testCase.attach(userdata, associatedTable); err != nil {
				// 测试初始化 userdata 关联边失败说明基础结构异常。
				t.Fatalf("attach userdata association failed: %v", err)
			}
			if err := state.Push(userdata.Value()); err != nil {
				// userdata 必须作为栈根进入完整 GC 可达图。
				t.Fatalf("push userdata failed: %v", err)
			}

			if err := state.FullGC(1); err != nil {
				// 仅有 userdata 关联弱表时完整 GC 不应失败。
				t.Fatalf("FullGC with userdata-associated weak table failed: %v", err)
			}
			if !state.hasWeakTables {
				// 弱表仍从 userdata 关联边可达，自动 weak sweep 标志不能被误清。
				t.Fatalf("userdata-associated weak table should keep weak sweep flag")
			}
		})
	}
}

// TestNoteTableAllocationSeparatesFinalizerAndWeakSweep 验证自动 finalizer 节拍不连带 weak sweep。
//
// 强可达 finalizer 对象会让 finalizer 队列保持非空；这种状态不能导致普通分配每 16 次就执行
// finalizer 前弱值全图扫描，否则官方 all.lua 在 gc.lua 后会拖慢后续 constructs.lua。
func TestNoteTableAllocationSeparatesFinalizerAndWeakSweep(t *testing.T) {
	state := NewState()
	weakTable := NewTable()
	weakMetatable := NewTable()
	weakMetatable.RawSetString("__mode", StringValue("v"))
	weakTable.SetMetatable(weakMetatable)
	state.RegisterWeakTable(weakTable)
	state.SetGlobal("weak", ReferenceValue(KindTable, weakTable))

	weakValue := NewTable()
	weakTable.RawSetString("item", ReferenceValue(KindTable, weakValue))

	finalizerTarget := NewTable()
	finalizerMetatable := NewTable()
	finalizerCalls := 0
	finalizerMetatable.RawSetString("__gc", ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 强可达对象不应被自动 finalizer 执行。
		finalizerCalls++
		return nil, nil
	})))
	finalizerTarget.SetMetatable(finalizerMetatable)
	state.RegisterTableFinalizer(finalizerTarget)
	state.SetGlobal("target", ReferenceValue(KindTable, finalizerTarget))

	for index := int64(0); index < autoGCFinalizerInterval*4; index++ {
		// 多次跨过 finalizer 节拍；强可达 finalizer 不应触发弱值表清理。
		state.NoteTableAllocation()
	}
	if finalizerCalls != 0 {
		// target 仍在 globals 中，自动 GC 不能执行其 __gc。
		t.Fatalf("reachable finalizer should not run automatically: %d", finalizerCalls)
	}
	if weakTable.RawGetString("item").IsNil() {
		// 没有显式 collectgarbage，也没有实际执行 finalizer 时，weak value 不应被 finalizer 节拍误清。
		t.Fatalf("weak value should not be swept by finalizer-only ticks")
	}
}

// TestNoteTableAllocationSweepsWeakTablesAfterAutoFinalizer 验证自动终结周期会推进 weak sweep。
//
// 官方 gc.lua 的 GC() 通过分配压力等待自动 finalizer 运行；该周期也必须清理 weak key/value，
// 否则 ephemeron 链在压力 GC 后不会收敛。
func TestNoteTableAllocationSweepsWeakTablesAfterAutoFinalizer(t *testing.T) {
	state := NewState()
	weakTable := NewTable()
	weakMetatable := NewTable()
	weakMetatable.RawSetString("__mode", StringValue("v"))
	weakTable.SetMetatable(weakMetatable)
	state.RegisterWeakTable(weakTable)
	state.SetGlobal("weak", ReferenceValue(KindTable, weakTable))

	weakValue := NewTable()
	weakTable.RawSetString("item", ReferenceValue(KindTable, weakValue))

	finalizerTarget := NewTable()
	finalizerMetatable := NewTable()
	finalizerMetatable.RawSetString("__gc", ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// finalizer 函数本体只作为可调用标记，实际调用由 runner 计数。
		return nil, nil
	})))
	finalizerTarget.SetMetatable(finalizerMetatable)
	state.RegisterTableFinalizer(finalizerTarget)

	finalizerCalls := 0
	state.SetTableFinalizerRunner(func(tableValue Value, finalizerValue Value) error {
		// 自动 finalizer 成功运行后应触发一轮完整 weak sweep。
		finalizerCalls++
		return nil
	})
	for index := int64(0); index < autoGCFinalizerInterval; index++ {
		// 跨过一次自动 finalizer 节拍。
		state.NoteTableAllocation()
	}
	if finalizerCalls != 1 {
		// 不可达 finalizer 对象应在自动周期中运行一次。
		t.Fatalf("auto finalizer calls = %d, want 1", finalizerCalls)
	}
	if !weakTable.RawGetString("item").IsNil() {
		// 自动终结周期应跟随完整 weak sweep，清理只由 weak value 持有的对象。
		t.Fatalf("weak value should be swept after auto finalizer")
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
