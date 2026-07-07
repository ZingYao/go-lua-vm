//go:build native_modules

package native

import (
	"errors"
	"testing"

	"github.com/zing/go-lua-vm/runtime"
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
