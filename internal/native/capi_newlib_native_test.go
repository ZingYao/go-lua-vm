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

// TestNativeLuaLSetFuncsRejectsUnsupportedUpvalues 验证 nup>0 当前保持 no-op。
func TestNativeLuaLSetFuncsRejectsUnsupportedUpvalues(t *testing.T) {
	// C closure upvalue 尚未实现，setfuncs 不能消耗 upvalue 或写入错误 closure。
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
	if nativeLuaLSetFuncs(luaState, []nativeLuaLibraryFunction{{name: "add", function: luaState}}, 1) {
		// nup>0 当前必须明确返回 false。
		t.Fatalf("nativeLuaLSetFuncs nup>0 returned true")
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// no-op 边界必须保留 table 和调用方压入的 upvalue。
		t.Fatalf("nativeLuaLSetFuncs nup>0 top = %d, want 2", got)
	}
	tableValue := state.ValueAt(1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 第一个槽位仍应是原始 table。
		t.Fatalf("nativeLuaLSetFuncs nup>0 target = %#v, want table", tableValue)
	}
	if value := tableRef.RawGetString("add"); !value.IsNil() {
		// nup>0 未实现时不能写入 add 字段。
		t.Fatalf("table.add after nup>0 = %#v, want nil", value)
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
