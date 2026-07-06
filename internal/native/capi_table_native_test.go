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

// TestNativeLuaSetFieldRespectsCurrentCFrameBase 验证 lua_setfield 不会弹出当前 C 帧之前的外层栈值。
func TestNativeLuaSetFieldRespectsCurrentCFrameBase(t *testing.T) {
	// registry 目标不需要当前 C 帧内 table 参数，可直接暴露“缺少 value 时是否穿透外层栈”的边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C frame 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'o', 'u', 't', 'e', 'r', 0}[0]))
	if !pushNativeStateCallFrame(luaState, state.StackTop(), nil) {
		// 无法建立 C 调用帧时，后续索引语义不可验证。
		t.Fatal("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	keyBytes := []byte{'f', 'i', 'e', 'l', 'd', 0}
	nativeLuaSetField(luaState, runtime.RegistryPseudoIndex, unsafe.Pointer(&keyBytes[0]))
	if got := state.StackTop(); got != 1 {
		// 当前 C 帧没有可见 value 时，setfield 不得弹掉外层 sentinel。
		t.Fatalf("lua_setfield global top = %d, want 1", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层栈值必须原样保留给调用者。
		t.Fatalf("outer stack value = %#v, want outer", value)
	}
	if value := state.Registry().RawGetString("field"); !value.IsNil() {
		// 缺少可见 value 时不得向 registry 写入错误值。
		t.Fatalf("registry.field = %#v, want nil", value)
	}
}

// TestNativeCAPIGetTableUsesStackKey 验证 lua_gettable 使用栈顶 key 读取并替换为结果。
func TestNativeCAPIGetTableUsesStackKey(t *testing.T) {
	// LPeg 会用 lua_gettable 查询普通 Lua table，本测试锁定最小 raw table 查询语义。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 gettable。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 2)
	tableValue := state.ValueAt(1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 栈底必须是 gettable 操作的 table。
		t.Fatalf("table value = %#v, want table", tableValue)
	}
	tableRef.RawSetString("name", runtime.StringValue("glua"))
	tableRef.RawSetInteger(2, runtime.IntegerValue(200))

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'n', 'a', 'm', 'e', 0}[0]))
	typeCode := nativeLuaGetTable(luaState, 1)
	if typeCode != nativeLuaTypeString {
		// string key 命中时返回 string 类型编号。
		t.Fatalf("lua_gettable string type = %d, want %d", typeCode, nativeLuaTypeString)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// gettable 必须弹出 key 并压入 value，因此总栈高保持 table,value。
		t.Fatalf("lua_gettable string top = %d, want 2", got)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindString || value.String != "glua" {
		// 栈顶必须是 table["name"] 的值。
		t.Fatalf("lua_gettable string value = %#v, want glua", value)
	}

	nativeLuaSetTop(luaState, 1)
	nativeLuaPushInteger(luaState, 2)
	typeCode = nativeLuaGetTable(luaState, 1)
	if typeCode != nativeLuaTypeNumber {
		// integer key 命中 integer 值时返回 number 类型编号。
		t.Fatalf("lua_gettable integer type = %d, want %d", typeCode, nativeLuaTypeNumber)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindInteger || value.Integer != 200 {
		// 栈顶必须是 table[2] 的值。
		t.Fatalf("lua_gettable integer value = %#v, want 200", value)
	}

	nativeLuaSetTop(luaState, 1)
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'m', 'i', 's', 's', 0}[0]))
	typeCode = nativeLuaGetTable(luaState, 1)
	if typeCode != nativeLuaTypeNil {
		// 缺失 key 应按 Lua table 语义压入 nil。
		t.Fatalf("lua_gettable missing type = %d, want %d", typeCode, nativeLuaTypeNil)
	}
	if value := state.ValueAt(-1); !value.IsNil() {
		// 缺失 key 的查询结果必须是 nil。
		t.Fatalf("lua_gettable missing value = %#v, want nil", value)
	}
}

// TestNativeLuaGetTableRespectsCurrentCFrameBase 验证 lua_gettable 不会弹出当前 C 帧之前的外层栈值。
func TestNativeLuaGetTableRespectsCurrentCFrameBase(t *testing.T) {
	// registry 目标不需要当前 C 帧内 table 参数，可直接暴露“缺少 key 时是否穿透外层栈”的边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C frame 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'o', 'u', 't', 'e', 'r', 0}[0]))
	if !pushNativeStateCallFrame(luaState, state.StackTop(), nil) {
		// 无法建立 C 调用帧时，后续索引语义不可验证。
		t.Fatal("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	if typeCode := nativeLuaGetTable(luaState, runtime.RegistryPseudoIndex); typeCode != nativeLuaTypeNone {
		// 缺少可见 key 时返回 none，不压入 nil 伪造查询结果。
		t.Fatalf("lua_gettable type = %d, want %d", typeCode, nativeLuaTypeNone)
	}
	if got := state.StackTop(); got != 1 {
		// 当前 C 帧没有可见 key 时，gettable 不得弹掉外层 sentinel。
		t.Fatalf("lua_gettable global top = %d, want 1", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层栈值必须原样保留给调用者。
		t.Fatalf("outer stack value = %#v, want outer", value)
	}
}

// TestNativeCAPIGetTableRejectsInvalidTarget 验证 lua_gettable 对无效目标保持失败安全。
func TestNativeCAPIGetTableRejectsInvalidTarget(t *testing.T) {
	// 当前最小 shim 不做 api_check；非 table 目标保持 key 不被吞掉。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 invalid gettable。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 1)
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'x', 0}[0]))
	if typeCode := nativeLuaGetTable(luaState, 1); typeCode != nativeLuaTypeNone {
		// 非 table 目标在当前阶段返回 none。
		t.Fatalf("invalid lua_gettable type = %d, want %d", typeCode, nativeLuaTypeNone)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 无效目标不能弹出 key，避免掩盖后续错误边界。
		t.Fatalf("invalid lua_gettable top = %d, want 2", got)
	}
}

// TestNativeCAPISetTableUsesStackKeyAndValue 验证 lua_settable 使用栈顶 key/value 写入 table。
func TestNativeCAPISetTableUsesStackKeyAndValue(t *testing.T) {
	// LPeg 构建位置表时依赖 lua_settable 写入普通 table。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 settable。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 1)
	tableValue := state.ValueAt(1)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 栈底必须是 settable 操作的 table。
		t.Fatalf("table value = %#v, want table", tableValue)
	}
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'n', 'a', 'm', 'e', 0}[0]))
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'g', 'l', 'u', 'a', 0}[0]))
	nativeLuaSetTable(luaState, 1)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// settable 必须弹出 key/value，只保留 table。
		t.Fatalf("lua_settable top = %d, want 1", got)
	}
	if value := tableRef.RawGetString("name"); value.Kind != runtime.KindString || value.String != "glua" {
		// table["name"] 必须写入栈顶 value。
		t.Fatalf("table.name = %#v, want glua", value)
	}

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'n', 'a', 'm', 'e', 0}[0]))
	nativeLuaPushNil(luaState)
	nativeLuaSetTable(luaState, 1)
	if value := tableRef.RawGetString("name"); !value.IsNil() {
		// 既有 raw 字段写入 nil 应删除该字段，不触发 __newindex。
		t.Fatalf("table.name after nil settable = %#v, want nil", value)
	}
}

// TestNativeLuaSetTableRespectsCurrentCFrameBase 验证 lua_settable 不会弹出当前 C 帧之前的外层栈值。
func TestNativeLuaSetTableRespectsCurrentCFrameBase(t *testing.T) {
	// registry 目标不需要当前 C 帧内 table 参数，可直接暴露“缺少 key/value 时是否穿透外层栈”的边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C frame 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'o', 'u', 't', 'e', 'r', '_', 'k', 'e', 'y', 0}[0]))
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'o', 'u', 't', 'e', 'r', '_', 'v', 'a', 'l', 'u', 'e', 0}[0]))
	if !pushNativeStateCallFrame(luaState, state.StackTop(), nil) {
		// 无法建立 C 调用帧时，后续索引语义不可验证。
		t.Fatal("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaSetTable(luaState, runtime.RegistryPseudoIndex)
	if got := state.StackTop(); got != 2 {
		// 当前 C 帧没有可见 key/value 时，settable 不得弹掉外层 sentinel。
		t.Fatalf("lua_settable global top = %d, want 2", got)
	}
	if key := state.ValueAt(1); key.Kind != runtime.KindString || key.String != "outer_key" {
		// 外层 key sentinel 必须原样保留给调用者。
		t.Fatalf("outer key value = %#v, want outer_key", key)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "outer_value" {
		// 外层 value sentinel 必须原样保留给调用者。
		t.Fatalf("outer value = %#v, want outer_value", value)
	}
	if value := state.Registry().RawGetString("outer_key"); !value.IsNil() {
		// 缺少可见 key/value 时不得向 registry 写入错误值。
		t.Fatalf("registry.outer_key = %#v, want nil", value)
	}
}

// TestNativeCAPISetTableUsesTableNewIndex 验证 lua_settable 遵守 table 型 __newindex。
func TestNativeCAPISetTableUsesTableNewIndex(t *testing.T) {
	// 当前 native shim 使用 runtime.Table.Set，至少覆盖 table 型 __newindex 链。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	sourceTable := runtime.NewTable()
	targetTable := runtime.NewTable()
	metatable := runtime.NewTable()
	metatable.RawSetString("__newindex", runtime.ReferenceValue(runtime.KindTable, targetTable))
	sourceTable.SetMetatable(metatable)
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindTable, sourceTable))

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'k', 0}[0]))
	nativeLuaPushInteger(luaState, 7)
	nativeLuaSetTable(luaState, 1)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// settable 必须消费 key/value。
		t.Fatalf("newindex lua_settable top = %d, want 1", got)
	}
	if value := sourceTable.RawGetString("k"); !value.IsNil() {
		// raw 未命中且存在 __newindex table 时，源 table 不应落盘。
		t.Fatalf("source table k = %#v, want nil", value)
	}
	if value := targetTable.RawGetString("k"); !value.RawEqual(runtime.IntegerValue(7)) {
		// 写入必须转发到 __newindex table。
		t.Fatalf("target table k = %#v, want 7", value)
	}
}

// TestNativeCAPISetTableRejectsInvalidTarget 验证 lua_settable 对无效目标保持失败安全。
func TestNativeCAPISetTableRejectsInvalidTarget(t *testing.T) {
	// 当前最小 shim 不做 api_check；非 table 目标保持 key/value 不被吞掉。
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
	nativeLuaPushInteger(luaState, 2)
	nativeLuaSetTable(luaState, 1)
	if got := nativeLuaStackTop(luaState); got != 3 {
		// 无效目标不能弹出 key/value，避免掩盖后续错误边界。
		t.Fatalf("invalid lua_settable top = %d, want 3", got)
	}
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// 无效目标 no-op 不应留下 pending error。
		t.Fatalf("invalid lua_settable pending error = %#v", errorObject)
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

// TestNativeLuaRawSetRespectsCurrentCFrameBase 验证 lua_rawset 不会弹出当前 C 帧之前的外层栈值。
func TestNativeLuaRawSetRespectsCurrentCFrameBase(t *testing.T) {
	// registry 目标在当前 C 帧无可见 key/value 时，最容易暴露 rawset 是否误弹调用者栈。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C frame 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	keyBytes := []byte{'o', 'u', 't', 'e', 'r', '_', 'k', 'e', 'y', 0}
	valueBytes := []byte{'o', 'u', 't', 'e', 'r', '_', 'v', 'a', 'l', 'u', 'e', 0}
	nativeLuaPushString(luaState, unsafe.Pointer(&keyBytes[0]))
	nativeLuaPushString(luaState, unsafe.Pointer(&valueBytes[0]))
	if !pushNativeStateCallFrame(luaState, state.StackTop(), nil) {
		// 无法建立 C 调用帧时，后续索引语义不可验证。
		t.Fatal("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaRawSet(luaState, runtime.RegistryPseudoIndex)
	if got := state.StackTop(); got != 2 {
		// 当前 C 帧没有可见 key/value 时，rawset 不得弹掉外层 sentinel。
		t.Fatalf("lua_rawset global top = %d, want 2", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer_key" {
		// 外层 key sentinel 必须原样保留给调用者。
		t.Fatalf("outer key stack value = %#v, want outer_key", value)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "outer_value" {
		// 外层 value sentinel 必须原样保留给调用者。
		t.Fatalf("outer value stack value = %#v, want outer_value", value)
	}
	if value := state.Registry().RawGetString("outer_key"); !value.IsNil() {
		// 缺少可见 key/value 时不得向 registry 写入错误值。
		t.Fatalf("registry.outer_key = %#v, want nil", value)
	}
}

// TestNativeCAPIRawLen 验证 lua_rawlen 的 string/table/full userdata 原始长度语义。
func TestNativeCAPIRawLen(t *testing.T) {
	// rawlen 只读取原始长度，不触发元方法，也不改变栈顶。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	text := []byte{'a', 0, 'b'}
	nativeLuaPushLString(luaState, unsafe.Pointer(&text[0]), uintptr(len(text)))
	nativeLuaCreateTable(luaState, 0, 0)
	tableValue := state.ValueAt(2)
	tableRef, ok := tableValue.Ref.(*runtime.Table)
	if tableValue.Kind != runtime.KindTable || !ok || tableRef == nil {
		// 第二个栈槽必须是用于 rawlen 的 table。
		t.Fatalf("table value = %#v, want table", tableValue)
	}
	tableRef.RawSetInteger(1, runtime.StringValue("x"))
	tableRef.RawSetInteger(2, runtime.StringValue("y"))
	tableRef.RawSetInteger(3, runtime.StringValue("z"))
	if pointer := nativeLuaNewUserdata(luaState, 12); pointer == nil {
		// full userdata 分配失败时无法验证 userdata rawlen。
		t.Fatal("nativeLuaNewUserdata returned nil")
	}
	nativeLuaPushBoolean(luaState, true)

	if got := nativeLuaRawLen(luaState, 1); got != uintptr(len(text)) {
		// string raw length 必须按字节数统计，内嵌 NUL 也计入。
		t.Fatalf("lua_rawlen string = %d, want %d", got, len(text))
	}
	if got := nativeLuaRawLen(luaState, 2); got != 3 {
		// table raw length 使用基础数组边界。
		t.Fatalf("lua_rawlen table = %d, want 3", got)
	}
	if got := nativeLuaRawLen(luaState, 3); got != 12 {
		// full userdata raw length 返回 lua_newuserdata 请求的逻辑字节数。
		t.Fatalf("lua_rawlen userdata = %d, want 12", got)
	}
	if got := nativeLuaRawLen(luaState, 4); got != 0 {
		// boolean 没有 raw length。
		t.Fatalf("lua_rawlen boolean = %d, want 0", got)
	}
	if got := nativeLuaRawLen(luaState, 99); got != 0 {
		// 无效索引按 Lua C API 返回 0。
		t.Fatalf("lua_rawlen missing = %d, want 0", got)
	}
	if got := nativeLuaStackTop(luaState); got != 4 {
		// rawlen 不能压栈或弹栈。
		t.Fatalf("lua_rawlen top = %d, want 4", got)
	}
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// rawlen 查询不应留下 pending error。
		t.Fatalf("lua_rawlen pending error = %#v", errorObject)
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

// TestNativeLuaRawSetIRespectsCurrentCFrameBase 验证 lua_rawseti 不会弹出当前 C 帧之前的外层栈值。
func TestNativeLuaRawSetIRespectsCurrentCFrameBase(t *testing.T) {
	// registry 目标在当前 C 帧无可见 value 时，最容易暴露 rawseti 是否误弹调用者栈。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C frame 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'o', 'u', 't', 'e', 'r', 0}[0]))
	if !pushNativeStateCallFrame(luaState, state.StackTop(), nil) {
		// 无法建立 C 调用帧时，后续索引语义不可验证。
		t.Fatal("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaRawSetI(luaState, runtime.RegistryPseudoIndex, 77)
	if got := state.StackTop(); got != 1 {
		// 当前 C 帧没有可见 value 时，rawseti 不得弹掉外层 sentinel。
		t.Fatalf("lua_rawseti global top = %d, want 1", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层栈值必须原样保留给调用者。
		t.Fatalf("outer stack value = %#v, want outer", value)
	}
	if value := state.Registry().RawGetInteger(77); !value.IsNil() {
		// 缺少可见 value 时不得向 registry 写入错误值。
		t.Fatalf("registry[77] = %#v, want nil", value)
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
