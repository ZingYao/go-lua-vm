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

// TestNativeCAPIRawSetAndNext 验证 lua_rawset 与 lua_next 的基础 table 语义。
func TestNativeCAPIRawSetAndNext(t *testing.T) {
	// cjson 编码 table 时依赖 rawset/next 这类不触发元方法的 public C API。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 1)
	keyBytes := []byte{'k', 0}
	nativeLuaPushString(luaState, unsafe.Pointer(&keyBytes[0]))
	nativeLuaPushInteger(luaState, 7)
	nativeLuaRawSet(luaState, 1)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// rawset 必须弹出 key/value，只保留 table。
		t.Fatalf("lua_rawset top = %d, want 1", got)
	}
	tableValue := state.ValueAt(1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 栈底必须是 rawset 操作的 table。
		t.Fatalf("table value = %#v, want table", tableValue)
	}
	if value := tableRef.RawGetString("k"); !value.RawEqual(runtime.IntegerValue(7)) {
		// rawset 应按原始 string key 写入 integer value。
		t.Fatalf("table.k = %#v, want 7", value)
	}

	nativeLuaPushNil(luaState)
	if got := nativeLuaNext(luaState, 1); got != 1 {
		// 首次 next(nil) 应返回一组 key/value。
		t.Fatalf("lua_next first = %d, want 1", got)
	}
	if got := nativeLuaStackTop(luaState); got != 3 {
		// next 命中后栈顶应为 table,key,value。
		t.Fatalf("lua_next first top = %d, want 3", got)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "k" {
		// next 返回的 key 必须是原始 string key。
		t.Fatalf("lua_next key = %#v, want k", value)
	}
	if value := state.ValueAt(3); !value.RawEqual(runtime.IntegerValue(7)) {
		// next 返回的 value 必须是原始 integer value。
		t.Fatalf("lua_next value = %#v, want 7", value)
	}
	nativeLuaSetTop(luaState, 2)
	if got := nativeLuaNext(luaState, 1); got != 0 {
		// 单元素 table 的第二次 next 应结束迭代。
		t.Fatalf("lua_next end = %d, want 0", got)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 迭代结束时 lua_next 只弹出 key，不压入新值。
		t.Fatalf("lua_next end top = %d, want 1", got)
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

// TestNativeCAPILRefRegistryAllocatesAndReuses 验证 luaL_ref/luaL_unref 的 registry freelist 语义。
func TestNativeCAPILRefRegistryAllocatesAndReuses(t *testing.T) {
	// luaL_ref 是 userdata/registry 阶段的关键能力，必须能分配引用并复用已释放槽位。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	registry := state.Registry()

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'f', 'i', 'r', 's', 't', 0}[0]))
	firstRef := nativeLuaLRef(luaState, runtime.RegistryPseudoIndex)
	if firstRef <= 0 {
		// 非 nil 值必须获得正整数引用。
		t.Fatalf("first luaL_ref = %d, want positive", firstRef)
	}
	if got := nativeLuaStackTop(luaState); got != 0 {
		// luaL_ref 成功后必须弹出被引用值。
		t.Fatalf("first luaL_ref top = %d, want 0", got)
	}
	if value := registry.RawGetInteger(int64(firstRef)); value.Kind != runtime.KindString || value.String != "first" {
		// registry[ref] 必须保存第一个字符串。
		t.Fatalf("registry[firstRef] = %#v, want first", value)
	}

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'s', 'e', 'c', 'o', 'n', 'd', 0}[0]))
	secondRef := nativeLuaLRef(luaState, runtime.RegistryPseudoIndex)
	if secondRef <= firstRef {
		// freelist 为空时第二个引用应追加到更大的正整数槽位。
		t.Fatalf("second luaL_ref = %d, want > %d", secondRef, firstRef)
	}

	nativeLuaLUnref(luaState, runtime.RegistryPseudoIndex, firstRef)
	if value := registry.RawGetInteger(int64(firstRef)); !value.IsNil() {
		// 释放首个引用时，空 freelist 会让该槽位变为 nil。
		t.Fatalf("registry[firstRef] after unref = %#v, want nil", value)
	}
	if value := registry.RawGetInteger(nativeLuaRefFreeIndex); value.Kind != runtime.KindInteger || value.Integer != int64(firstRef) {
		// t[0] 必须记录 freelist 头。
		t.Fatalf("registry freelist head = %#v, want %d", value, firstRef)
	}

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'t', 'h', 'i', 'r', 'd', 0}[0]))
	reusedRef := nativeLuaLRef(luaState, runtime.RegistryPseudoIndex)
	if reusedRef != firstRef {
		// 新引用必须复用刚释放的 freelist 头。
		t.Fatalf("reused luaL_ref = %d, want %d", reusedRef, firstRef)
	}
	if value := registry.RawGetInteger(nativeLuaRefFreeIndex); !value.IsNil() {
		// freelist 只有一个节点时复用后 t[0] 回到 nil。
		t.Fatalf("registry freelist head after reuse = %#v, want nil", value)
	}
	if value := registry.RawGetInteger(int64(reusedRef)); value.Kind != runtime.KindString || value.String != "third" {
		// 复用槽位必须保存新的字符串。
		t.Fatalf("registry[reusedRef] = %#v, want third", value)
	}
	if value := registry.RawGetInteger(int64(secondRef)); value.Kind != runtime.KindString || value.String != "second" {
		// 未释放的第二个引用不能被 freelist 复用破坏。
		t.Fatalf("registry[secondRef] = %#v, want second", value)
	}
}

// TestNativeCAPILRefNilAndInvalidTargets 验证 luaL_ref 的预定义引用和失败安全边界。
func TestNativeCAPILRefNilAndInvalidTargets(t *testing.T) {
	// nil 引用必须返回 LUA_REFNIL；非法 table 目标当前保持 LUA_NOREF/no-op。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	registry := state.Registry()

	nativeLuaPushNil(luaState)
	if ref := nativeLuaLRef(luaState, runtime.RegistryPseudoIndex); ref != nativeLuaRefNil {
		// nil 值按 Lua 5.3 lauxlib 语义返回 LUA_REFNIL。
		t.Fatalf("nil luaL_ref = %d, want %d", ref, nativeLuaRefNil)
	}
	if got := nativeLuaStackTop(luaState); got != 0 {
		// nil 引用也必须弹出 nil 值。
		t.Fatalf("nil luaL_ref top = %d, want 0", got)
	}
	nativeLuaLUnref(luaState, runtime.RegistryPseudoIndex, nativeLuaNoRef)
	nativeLuaLUnref(luaState, runtime.RegistryPseudoIndex, nativeLuaRefNil)
	if value := registry.RawGetInteger(nativeLuaRefFreeIndex); !value.IsNil() {
		// 释放预定义负数引用不能污染 freelist。
		t.Fatalf("registry freelist after negative unref = %#v, want nil", value)
	}

	nativeLuaPushInteger(luaState, 1)
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'v', 0}[0]))
	if ref := nativeLuaLRef(luaState, 1); ref != nativeLuaNoRef {
		// 非 table 目标当前返回 LUA_NOREF，后续 api_check 阶段再补错误。
		t.Fatalf("invalid luaL_ref = %d, want %d", ref, nativeLuaNoRef)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 非 table 目标不能弹出待引用值，避免提前吞掉 C 模块数据。
		t.Fatalf("invalid luaL_ref top = %d, want 2", got)
	}
}
