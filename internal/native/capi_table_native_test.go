//go:build native_modules

package native

import (
	"testing"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeCAPITableFieldPrimitives 验证最小 table 字段 C API shim 能操作 Go table。
func TestNativeCAPITableFieldPrimitives(t *testing.T) {
	// 测试通过 opaque handle 进入 Go State，覆盖 create/set/get 的栈副作用。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 table shim。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 1)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// createtable 必须把新 table 压到栈顶。
		t.Fatalf("lua_createtable top = %d, want 1", got)
	}
	tableValue := state.ValueAt(-1)
	if tableValue.Kind != runtime.KindTable {
		// 栈顶必须是 table 引用。
		t.Fatalf("lua_createtable value = %#v, want table", tableValue)
	}
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if !ok || tableRef == nil {
		// table 引用负载必须是 runtime.Table。
		t.Fatalf("lua_createtable ref = %#v, want *runtime.Table", tableValue.Ref)
	}

	keyBytes := []byte{'n', 'a', 'm', 'e', 0}
	keyPointer := unsafe.Pointer(&keyBytes[0])
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'g', 'l', 'u', 'a', 0}[0]))
	nativeLuaSetField(luaState, -2, keyPointer)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// setfield 必须弹出待写入值，保留 table 自身。
		t.Fatalf("lua_setfield top = %d, want 1", got)
	}
	if value := tableRef.RawGetString("name"); value.Kind != runtime.KindString || value.String != "glua" {
		// table 字段必须写入指定 string key。
		t.Fatalf("table.name = %#v, want glua", value)
	}

	typeCode := nativeLuaGetField(luaState, -1, keyPointer)
	if typeCode != nativeLuaTypeString {
		// getfield 返回值必须是 Lua C API string 类型编号。
		t.Fatalf("lua_getfield type = %d, want %d", typeCode, nativeLuaTypeString)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// getfield 必须把读取结果压入栈顶。
		t.Fatalf("lua_getfield top = %d, want 2", got)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindString || value.String != "glua" {
		// getfield 读取结果必须等于已写入字段。
		t.Fatalf("lua_getfield value = %#v, want glua", value)
	}

	nativeLuaSetTop(luaState, 1)
	nativeLuaPushNil(luaState)
	nativeLuaSetField(luaState, -2, keyPointer)
	if value := tableRef.RawGetString("name"); !value.IsNil() {
		// setfield 写入 nil 应按 Lua table 语义删除字段。
		t.Fatalf("table.name after nil set = %#v, want nil", value)
	}
	typeCode = nativeLuaGetField(luaState, -1, keyPointer)
	if typeCode != nativeLuaTypeNil {
		// 缺失字段读取应返回 nil 类型编号，并压入 Lua nil。
		t.Fatalf("lua_getfield missing type = %d, want %d", typeCode, nativeLuaTypeNil)
	}
	if value := state.ValueAt(-1); !value.IsNil() {
		// 缺失字段读取结果必须是 nil。
		t.Fatalf("lua_getfield missing value = %#v, want nil", value)
	}
}

// TestNativeCAPITableFieldPrimitivesRejectInvalidTarget 验证无效目标不会破坏栈。
func TestNativeCAPITableFieldPrimitivesRejectInvalidTarget(t *testing.T) {
	// 当前最小 shim 不做 longjmp；无效 table 目标保持 no-op/none，后续错误阶段补齐。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 invalid target。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	keyBytes := []byte{'x', 0}
	keyPointer := unsafe.Pointer(&keyBytes[0])

	nativeLuaPushInteger(luaState, 1)
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'v', 0}[0]))
	nativeLuaSetField(luaState, 1, keyPointer)
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 非 table 目标保持栈不变，避免提前吞掉 C 模块传入的值。
		t.Fatalf("invalid lua_setfield top = %d, want 2", got)
	}
	if typeCode := nativeLuaGetField(luaState, 1, keyPointer); typeCode != nativeLuaTypeNone {
		// 非 table 目标读取在当前阶段返回 none。
		t.Fatalf("invalid lua_getfield type = %d, want %d", typeCode, nativeLuaTypeNone)
	}
}

// TestNativeCAPIRawIntegerPrimitives 验证 lua_rawgeti/lua_rawseti 的整数 key raw 语义。
func TestNativeCAPIRawIntegerPrimitives(t *testing.T) {
	// 测试覆盖 table 数组区写入、读取、nil 删除和返回类型编号。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 raw integer API。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 0)
	tableValue := state.ValueAt(-1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if !ok || tableRef == nil {
		// raw integer API 需要一个真实 runtime table 目标。
		t.Fatalf("table ref = %#v, want *runtime.Table", tableValue.Ref)
	}
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'v', 'a', 'l', 'u', 'e', 0}[0]))
	nativeLuaRawSetI(luaState, 1, 3)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// rawseti 必须弹出待写入值，只保留 table。
		t.Fatalf("lua_rawseti top = %d, want 1", got)
	}
	if value := tableRef.RawGetInteger(3); value.Kind != runtime.KindString || value.String != "value" {
		// table[3] 必须写入字符串。
		t.Fatalf("table[3] = %#v, want value", value)
	}

	typeCode := nativeLuaRawGetI(luaState, 1, 3)
	if typeCode != nativeLuaTypeString {
		// rawgeti 命中 string 值时返回 string 类型编号。
		t.Fatalf("lua_rawgeti type = %d, want %d", typeCode, nativeLuaTypeString)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindString || value.String != "value" {
		// rawgeti 必须把读取结果压到栈顶。
		t.Fatalf("lua_rawgeti value = %#v, want value", value)
	}

	nativeLuaSetTop(luaState, 1)
	nativeLuaPushNil(luaState)
	nativeLuaRawSetI(luaState, 1, 3)
	if value := tableRef.RawGetInteger(3); !value.IsNil() {
		// rawseti 写入 nil 应删除整数 key。
		t.Fatalf("table[3] after nil rawseti = %#v, want nil", value)
	}
	typeCode = nativeLuaRawGetI(luaState, 1, 3)
	if typeCode != nativeLuaTypeNil {
		// 缺失整数 key 读取应返回 nil 类型编号。
		t.Fatalf("lua_rawgeti missing type = %d, want %d", typeCode, nativeLuaTypeNil)
	}
	if value := state.ValueAt(-1); !value.IsNil() {
		// 缺失整数 key 读取结果必须是 nil。
		t.Fatalf("lua_rawgeti missing value = %#v, want nil", value)
	}
}

// TestNativeCAPIRawIntegerRegistry 验证 rawgeti/rawseti 可操作 registry pseudo-index。
func TestNativeCAPIRawIntegerRegistry(t *testing.T) {
	// registry pseudo-index 是 luaL_ref 的底层存储位置，必须支持 integer key。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 99)
	nativeLuaRawSetI(luaState, runtime.RegistryPseudoIndex, 42)
	if got := nativeLuaStackTop(luaState); got != 0 {
		// registry rawseti 也必须弹出待写入值。
		t.Fatalf("registry lua_rawseti top = %d, want 0", got)
	}
	typeCode := nativeLuaRawGetI(luaState, runtime.RegistryPseudoIndex, 42)
	if typeCode != nativeLuaTypeNumber {
		// Lua C API 中 integer 归类为 number。
		t.Fatalf("registry lua_rawgeti type = %d, want %d", typeCode, nativeLuaTypeNumber)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindInteger || value.Integer != 99 {
		// registry[42] 必须保存并读取 integer 99。
		t.Fatalf("registry[42] = %#v, want 99", value)
	}
}

// TestNativeCAPIRawIntegerRejectsInvalidTarget 验证 raw integer API 的失败安全边界。
func TestNativeCAPIRawIntegerRejectsInvalidTarget(t *testing.T) {
	// 当前最小 shim 不做 api_check；无效目标保持 no-op/none。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 1)
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'x', 0}[0]))
	nativeLuaRawSetI(luaState, 1, 1)
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 非 table 目标写入失败时不得弹栈。
		t.Fatalf("invalid lua_rawseti top = %d, want 2", got)
	}
	if typeCode := nativeLuaRawGetI(luaState, 1, 1); typeCode != nativeLuaTypeNone {
		// 非 table 目标读取返回 none。
		t.Fatalf("invalid lua_rawgeti type = %d, want %d", typeCode, nativeLuaTypeNone)
	}
}
