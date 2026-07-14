//go:build cgo

package native

import (
	"errors"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestNativeLuaCallCFunctionRejectsInvalidHandle 验证失效 State 不会进入本机函数。
func TestNativeLuaCallCFunctionRejectsInvalidHandle(t *testing.T) {
	// 无效 handle 必须直接返回 runtime error，避免 C 函数在已关闭 VM 上执行。
	results, err := nativeLuaCallCFunction(nil, nil, nil)
	if err == nil || !errors.Is(err, runtime.ErrClosedState) {
		// nil handle 没有可映射 State，应按关闭 State 分类。
		t.Fatalf("nativeLuaCallCFunction nil state err = %v, want ErrClosedState", err)
	}
	if results != nil {
		// 失败路径不能返回结果。
		t.Fatalf("nativeLuaCallCFunction nil state results = %#v, want nil", results)
	}
}

// TestNativeLuaPushCClosureCapturesUpvalues 验证 C closure 能捕获栈顶 upvalue。
func TestNativeLuaPushCClosureCapturesUpvalues(t *testing.T) {
	// cjson 通过 luaL_setfuncs(..., nup=1) 给多个 C 函数共享配置 userdata。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C closure 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 1)
	nativeLuaPushCClosure(luaState, nil, 0)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// nil 函数指针必须保持栈不变。
		t.Fatalf("nil C closure top = %d, want 1", got)
	}
	nativeLuaPushCClosure(luaState, luaState, 1)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// nup>0 应弹出原始 upvalue 并压入一个 closure。
		t.Fatalf("upvalue C closure top = %d, want 1", got)
	}
	closureValue := state.ValueAt(-1)
	closure, ok := closureValue.Ref.(*runtime.GoClosureWithUpvalues)
	if closureValue.Kind != runtime.KindGoClosure || !ok || closure == nil {
		// 栈顶必须是带 upvalue 元数据的 Go closure。
		t.Fatalf("upvalue C closure value = %#v, want GoClosureWithUpvalues", closureValue)
	}
	if len(closure.Upvalues) != 1 || !closure.Upvalues[0].RawEqual(runtime.IntegerValue(1)) {
		// 第一个 upvalue 必须保留原始栈值。
		t.Fatalf("upvalue C closure upvalues = %#v, want [1]", closure.Upvalues)
	}
}

// TestNativeLuaPushCClosureCapturesVisibleFrameUpvalues 验证 C closure 只捕获当前 C 帧可见 upvalue。
func TestNativeLuaPushCClosureCapturesVisibleFrameUpvalues(t *testing.T) {
	// 外层 sentinel 模拟调用者栈；当前 C 帧只能捕获其后压入的 visible upvalue。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C closure 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if err := state.Push(runtime.StringValue("outer")); err != nil {
		// 外层 sentinel 压栈失败时无法验证调用帧隔离。
		t.Fatalf("push outer sentinel failed: %v", err)
	}
	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, nil) {
		// C frame 基址记录失败时无法验证 visible upvalue 捕获。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaPushInteger(luaState, 7)
	nativeLuaPushCClosure(luaState, luaState, 1)
	if got := state.StackTop(); got != baseTop+1 {
		// 捕获 visible upvalue 后应弹出 upvalue 并压入 closure，外层 sentinel 保留。
		t.Fatalf("global top after visible C closure = %d, want %d", got, baseTop+1)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 当前 C 帧只应看到新压入的 closure。
		t.Fatalf("visible top after visible C closure = %d, want 1", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层 sentinel 不能被 upvalue 捕获弹栈误消费。
		t.Fatalf("outer sentinel after visible C closure = %#v, want outer string", value)
	}
	closureValue := state.ValueAt(-1)
	closure, ok := closureValue.Ref.(*runtime.GoClosureWithUpvalues)
	if closureValue.Kind != runtime.KindGoClosure || !ok || closure == nil {
		// 栈顶必须是捕获 visible upvalue 后创建的 Go closure。
		t.Fatalf("visible C closure value = %#v, want GoClosureWithUpvalues", closureValue)
	}
	if len(closure.Upvalues) != 1 || !closure.Upvalues[0].RawEqual(runtime.IntegerValue(7)) {
		// 捕获值必须来自当前 C 帧可见栈，而不是外层 sentinel。
		t.Fatalf("visible C closure upvalues = %#v, want [7]", closure.Upvalues)
	}
}

// TestNativeLuaPushCClosureCapturesMultipleUpvaluesAndKeepsVisiblePrefix 验证 C closure 构造只消费栈顶 upvalue 后缀。
func TestNativeLuaPushCClosureCapturesMultipleUpvaluesAndKeepsVisiblePrefix(t *testing.T) {
	// 构造外层调用者栈和当前 C 帧，锁定 lua_pushcclosure 的通用 upvalue 弹栈边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C closure 构造边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if err := state.Push(runtime.StringValue("outer")); err != nil {
		// 外层 sentinel 压栈失败时无法验证当前 C 帧不会被弹栈穿透。
		t.Fatalf("push outer sentinel failed: %v", err)
	}
	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, nil) {
		// C frame 基址记录失败时无法验证多 upvalue 捕获顺序。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	firstUpvalue := runtime.NewTable()
	firstUpvalue.RawSetString("marker", runtime.StringValue("first-upvalue"))
	nativeLuaPushValue(luaState, runtime.StringValue("visible-prefix"))
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindTable, firstUpvalue))
	nativeLuaPushInteger(luaState, 99)

	nativeLuaPushCClosure(luaState, luaState, 2)
	if got := state.StackTop(); got != baseTop+2 {
		// nup=2 只能弹出两个 upvalue 并压入一个 closure，当前 C 帧前缀必须保留。
		t.Fatalf("global top after multi-upvalue C closure = %d, want %d", got, baseTop+2)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 当前 C 帧应只看到保留下来的前缀值和新 closure。
		t.Fatalf("visible top after multi-upvalue C closure = %d, want 2", got)
	}
	if value := state.ValueAt(baseTop + 1); value.Kind != runtime.KindString || value.String != "visible-prefix" {
		// 非 upvalue 的当前 C 帧前缀不能被 lua_pushcclosure 的弹栈逻辑误消费。
		t.Fatalf("visible prefix after multi-upvalue C closure = %#v, want visible-prefix", value)
	}
	closureValue := state.ValueAt(-1)
	closure, ok := closureValue.Ref.(*runtime.GoClosureWithUpvalues)
	if closureValue.Kind != runtime.KindGoClosure || !ok || closure == nil {
		// 栈顶必须是携带 upvalue 快照的 Go closure。
		t.Fatalf("multi-upvalue C closure value = %#v, want GoClosureWithUpvalues", closureValue)
	}
	if len(closure.Upvalues) != 2 {
		// 捕获数量必须与 lua_pushcclosure 的 nup 完全一致。
		t.Fatalf("multi-upvalue C closure upvalue count = %d, want 2", len(closure.Upvalues))
	}
	if closure.Upvalues[0].Kind != runtime.KindTable || closure.Upvalues[0].Ref != firstUpvalue {
		// 第一个 upvalue 必须保持原始 table identity，供后续 C closure 调用通过 lua_upvalueindex 读取。
		t.Fatalf("first multi-upvalue C closure upvalue = %#v, want table %p", closure.Upvalues[0], firstUpvalue)
	}
	if !closure.Upvalues[1].RawEqual(runtime.IntegerValue(99)) {
		// 第二个 upvalue 必须按栈上原始顺序保存。
		t.Fatalf("second multi-upvalue C closure upvalue = %#v, want 99", closure.Upvalues[1])
	}
}

// TestNativeLuaPushCClosureRejectsUpvaluesOutsideCurrentCFrame 验证 C closure 不穿透外层栈捕获 upvalue。
func TestNativeLuaPushCClosureRejectsUpvaluesOutsideCurrentCFrame(t *testing.T) {
	// 当前 C 帧没有可见 upvalue 时，nup=1 必须 no-op，不能捕获并弹掉外层调用者栈。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C closure 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if err := state.Push(runtime.StringValue("outer")); err != nil {
		// 外层 sentinel 压栈失败时无法验证调用帧隔离。
		t.Fatalf("push outer sentinel failed: %v", err)
	}
	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, nil) {
		// C frame 基址记录失败时无法验证 upvalue 数量边界。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaPushCClosure(luaState, luaState, 1)
	if got := state.StackTop(); got != baseTop {
		// visible upvalue 不足时不能弹掉调用者栈，也不能额外压入 closure。
		t.Fatalf("global top after missing-upvalue C closure = %d, want %d", got, baseTop)
	}
	if got := nativeLuaStackTop(luaState); got != 0 {
		// 当前 C 帧仍应为空。
		t.Fatalf("visible top after missing-upvalue C closure = %d, want 0", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层 sentinel 不能被当作 upvalue 捕获或弹出。
		t.Fatalf("outer sentinel after missing-upvalue C closure = %#v, want outer string", value)
	}
}

// TestNativeCFrameVisibleStackRootsWeakSweep 验证 C frame 可见临时栈值参与弱表强根扫描。
func TestNativeCFrameVisibleStackRootsWeakSweep(t *testing.T) {
	// native C API 构造 userdata、ktable 或 capture 时会把临时对象压在当前 C frame 可见栈上。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C frame 栈根。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	weakValueTable := runtime.NewTable()
	weakMetatable := runtime.NewTable()
	weakMetatable.RawSetString("__mode", runtime.StringValue("v"))
	weakValueTable.SetMetatable(weakMetatable)
	state.SetGlobal("weak", runtime.ReferenceValue(runtime.KindTable, weakValueTable))

	temporaryRoot := runtime.NewTable()
	temporaryRoot.RawSetString("marker", runtime.StringValue("native-c-frame"))
	weakValueTable.RawSetString("from-c-frame", runtime.ReferenceValue(runtime.KindTable, temporaryRoot))

	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, nil) {
		// C frame 基址记录失败时无法验证临时栈 root。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindTable, temporaryRoot))

	if removed := state.SweepWeakTables(); removed != 0 {
		// 当前 C frame 栈上的 table 是唯一强根，weak value 不应被本轮 sweep 清掉。
		t.Fatalf("weak sweep removed %d entries while C frame root is visible", removed)
	}
	if got := weakValueTable.RawGetString("from-c-frame"); got.IsNil() {
		// 临时 C frame 栈值间接保活的 weak value 必须仍可见。
		t.Fatalf("weak value reachable through native C frame stack was removed")
	}
	if got := state.StackTop(); got != baseTop+1 {
		// sweep 不应修改 C frame 可见栈。
		t.Fatalf("stack top after weak sweep = %d, want %d", got, baseTop+1)
	}
}

// TestNativeCFrameUpvaluesRootWeakSweep 验证 C closure 调用期 upvalue 参与弱表强根扫描。
func TestNativeCFrameUpvaluesRootWeakSweep(t *testing.T) {
	// C 模块可在回调内触发 GC；当前 C closure 的 upvalue 必须像 Lua 调用帧函数一样保活。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C closure 调用期根。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	weakValueTable := runtime.NewTable()
	weakMetatable := runtime.NewTable()
	weakMetatable.RawSetString("__mode", runtime.StringValue("v"))
	weakValueTable.SetMetatable(weakMetatable)
	state.SetGlobal("weak", runtime.ReferenceValue(runtime.KindTable, weakValueTable))

	upvalueRoot := runtime.NewTable()
	onlyThroughUpvalue := runtime.NewTable()
	onlyThroughUpvalue.RawSetString("marker", runtime.StringValue("native-c-upvalue"))
	upvalueRoot.RawSetString("child", runtime.ReferenceValue(runtime.KindTable, onlyThroughUpvalue))
	weakValueTable.RawSetString("from-c-upvalue", runtime.ReferenceValue(runtime.KindTable, onlyThroughUpvalue))

	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, []runtime.Value{runtime.ReferenceValue(runtime.KindTable, upvalueRoot)}) {
		// C frame 基址记录失败时无法验证调用期 upvalue root。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	if removed := state.SweepWeakTables(); removed != 0 {
		// 当前 C closure upvalue 是唯一强根，weak value 不应被本轮 sweep 清掉。
		t.Fatalf("weak sweep removed %d entries while C closure upvalue root is active", removed)
	}
	if got := weakValueTable.RawGetString("from-c-upvalue"); got.IsNil() {
		// C closure upvalue 间接保活的 weak value 必须仍可见。
		t.Fatalf("weak value reachable through native C closure upvalue was removed")
	}
}
