//go:build native_modules

package native

import (
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaLSetFuncsRegistersGoCallableFields 验证无 upvalue 函数列表可注册到 table。
func TestNativeLuaLSetFuncsRegistersGoCallableFields(t *testing.T) {
	// 使用非 nil 指针作为 C 函数地址占位，当前测试只验证注册结构，不执行该占位函数。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 newlib shim。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 2)
	functions := []nativeLuaLibraryFunction{
		{name: "add", function: luaState},
		{name: "skip", function: nil},
		{name: "echo", function: luaState},
	}
	if !nativeLuaLSetFuncs(luaState, functions, 0) {
		// nup==0 的注册必须成功。
		t.Fatalf("nativeLuaLSetFuncs returned false")
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// setfuncs 不应改变目标 table 所在栈顶。
		t.Fatalf("nativeLuaLSetFuncs top = %d, want 1", got)
	}
	tableValue := state.ValueAt(-1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 栈顶必须保持为目标 table。
		t.Fatalf("nativeLuaLSetFuncs target = %#v, want table", tableValue)
	}
	if value := tableRef.RawGetString("add"); value.Kind != runtime.KindGoClosure {
		// 有效函数条目必须注册为 Go closure。
		t.Fatalf("table.add = %#v, want Go closure", value)
	}
	if value := tableRef.RawGetString("echo"); value.Kind != runtime.KindGoClosure {
		// 第二个有效函数条目也必须注册。
		t.Fatalf("table.echo = %#v, want Go closure", value)
	}
	if value := tableRef.RawGetString("skip"); !value.IsNil() {
		// nil 函数指针不能注册半成品 closure。
		t.Fatalf("table.skip = %#v, want nil", value)
	}
}

// TestNativeLuaLSetFuncsRegistersUpvalueClosures 验证 nup>0 会复制 upvalue 并注册闭包。
func TestNativeLuaLSetFuncsRegistersUpvalueClosures(t *testing.T) {
	// lua-cjson 的函数表会通过 nup=1 捕获同一个配置 userdata。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 nup 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 1)
	nativeLuaPushInteger(luaState, 9)
	if !nativeLuaLSetFuncs(luaState, []nativeLuaLibraryFunction{{name: "add", function: luaState}}, 1) {
		// nup>0 应注册成功并在结束时弹出原始 upvalue。
		t.Fatalf("nativeLuaLSetFuncs nup>0 returned false")
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// luaL_setfuncs 成功后必须只保留目标 table。
		t.Fatalf("nativeLuaLSetFuncs nup>0 top = %d, want 1", got)
	}
	tableValue := state.ValueAt(1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 第一个槽位仍应是原始 table。
		t.Fatalf("nativeLuaLSetFuncs nup>0 target = %#v, want table", tableValue)
	}
	value := tableRef.RawGetString("add")
	closure, ok := value.Ref.(*runtime.GoClosureWithUpvalues)
	if value.Kind != runtime.KindGoClosure || !ok || closure == nil {
		// 注册字段必须是带 upvalue 的 Go closure。
		t.Fatalf("table.add after nup>0 = %#v, want GoClosureWithUpvalues", value)
	}
	if len(closure.Upvalues) != 1 || !closure.Upvalues[0].RawEqual(runtime.IntegerValue(9)) {
		// 注册时捕获的 upvalue 必须来自调用栈原始值。
		t.Fatalf("table.add upvalues = %#v, want [9]", closure.Upvalues)
	}
}

// TestNativeLuaLSetFuncsUsesCurrentCFrameVisibleStack 验证 setfuncs 只读取当前 C 帧可见 table/upvalue。
func TestNativeLuaLSetFuncsUsesCurrentCFrameVisibleStack(t *testing.T) {
	// 外层 sentinel 模拟 Go VM 调用者栈；当前 C 帧只应消费其后压入的 table/upvalue。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 setfuncs 调用帧隔离。
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
		// C frame 基址记录失败时无法验证 visible table/upvalue 读取。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaCreateTable(luaState, 0, 1)
	nativeLuaPushInteger(luaState, 11)
	if !nativeLuaLSetFuncs(luaState, []nativeLuaLibraryFunction{{name: "add", function: luaState}}, 1) {
		// visible table/upvalue 足够时必须注册成功。
		t.Fatalf("nativeLuaLSetFuncs visible nup returned false")
	}
	if got := state.StackTop(); got != baseTop+1 {
		// 成功注册后只保留 visible table，并弹出原始 upvalue，外层 sentinel 保留。
		t.Fatalf("global top after visible setfuncs = %d, want %d", got, baseTop+1)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 当前 C 帧只应看到目标 table。
		t.Fatalf("visible top after visible setfuncs = %d, want 1", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层 sentinel 不能被 upvalue 恢复或 closure 弹栈误消费。
		t.Fatalf("outer sentinel after visible setfuncs = %#v, want outer string", value)
	}
	tableValue := state.ValueAt(-1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 当前 C 帧保留的可见值必须是目标 table。
		t.Fatalf("visible setfuncs table = %#v, want table", tableValue)
	}
	closureValue := tableRef.RawGetString("add")
	closure, ok := closureValue.Ref.(*runtime.GoClosureWithUpvalues)
	if closureValue.Kind != runtime.KindGoClosure || !ok || closure == nil {
		// 注册字段必须是捕获 visible upvalue 的 Go closure。
		t.Fatalf("visible setfuncs add = %#v, want GoClosureWithUpvalues", closureValue)
	}
	if len(closure.Upvalues) != 1 || !closure.Upvalues[0].RawEqual(runtime.IntegerValue(11)) {
		// 捕获值必须来自当前 C 帧可见栈，而不是外层 sentinel。
		t.Fatalf("visible setfuncs upvalues = %#v, want [11]", closure.Upvalues)
	}
}

// TestNativeLuaLSetFuncsRejectsUpvaluesOutsideCurrentCFrame 验证 setfuncs 不穿透外层栈读取 upvalue。
func TestNativeLuaLSetFuncsRejectsUpvaluesOutsideCurrentCFrame(t *testing.T) {
	// 当前 C 帧为空而外层有 sentinel 时，nup=1 必须失败且保持栈不变。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 setfuncs upvalue 边界。
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

	if nativeLuaLSetFuncs(luaState, []nativeLuaLibraryFunction{{name: "add", function: luaState}}, 1) {
		// visible upvalue 不足时必须失败，不能把外层 sentinel 当作 upvalue。
		t.Fatalf("nativeLuaLSetFuncs missing visible upvalue returned true")
	}
	if got := state.StackTop(); got != baseTop {
		// 失败路径不得弹掉或写入调用者栈。
		t.Fatalf("global top after missing visible upvalue = %d, want %d", got, baseTop)
	}
	if got := nativeLuaStackTop(luaState); got != 0 {
		// 当前 C 帧仍应为空。
		t.Fatalf("visible top after missing visible upvalue = %d, want 0", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层 sentinel 不能被注册流程读取、捕获或弹出。
		t.Fatalf("outer sentinel after missing visible upvalue = %#v, want outer string", value)
	}
}

// TestNativeLuaLNewLibCreatesTable 验证 luaL_newlib 宏等价路径能创建并填充 table。
func TestNativeLuaLNewLibCreatesTable(t *testing.T) {
	// newlib helper 应创建 table 并复用 setfuncs 注册无 upvalue 函数。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 newlib helper。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if !nativeLuaLNewLib(luaState, []nativeLuaLibraryFunction{{name: "add", function: luaState}}) {
		// newlib helper 必须创建 table 并注册函数。
		t.Fatalf("nativeLuaLNewLib returned false")
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// newlib 成功后栈顶应只有新 table。
		t.Fatalf("nativeLuaLNewLib top = %d, want 1", got)
	}
	tableValue := state.ValueAt(-1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 栈顶必须是新建 table。
		t.Fatalf("nativeLuaLNewLib value = %#v, want table", tableValue)
	}
	if value := tableRef.RawGetString("add"); value.Kind != runtime.KindGoClosure {
		// 函数表条目必须注册进新 table。
		t.Fatalf("newlib.add = %#v, want Go closure", value)
	}
}
