//go:build native_modules

package native

import (
	"testing"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaUserdataMetatable 验证 native userdata 可通过 C API 设置和读取 raw 元表。
func TestNativeLuaUserdataMetatable(t *testing.T) {
	// 使用真实 State 让 set/getmetatable 的弹栈副作用和 userdata raw 元表同时可见。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if pointer := nativeLuaNewUserdata(luaState, 4); pointer == nil {
		// 测试需要一个 native full userdata 作为目标。
		t.Fatalf("lua_newuserdata returned nil")
	}
	nativeLuaCreateTable(luaState, 0, 1)
	metatableValue := state.ValueAt(-1)
	if metatableValue.Kind != runtime.KindTable {
		// 新建的元表必须位于栈顶。
		t.Fatalf("metatable value = %#v, want table", metatableValue)
	}
	metatable := metatableValue.Ref.(*runtime.Table)
	metatable.RawSetString("__name", runtime.StringValue("native.ud"))

	if got := nativeLuaSetMetatable(luaState, 1); got != 1 {
		// 设置 userdata 元表应成功并弹出栈顶元表。
		t.Fatalf("lua_setmetatable(userdata) = %d, want 1", got)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 成功设置后只应剩下 userdata。
		t.Fatalf("top after setmetatable = %d, want 1", got)
	}
	if got := nativeLuaGetMetatable(luaState, 1); got != 1 {
		// 读取已设置元表应返回 1 并压入元表。
		t.Fatalf("lua_getmetatable(userdata) = %d, want 1", got)
	}
	if gotValue := state.ValueAt(-1); gotValue.Kind != runtime.KindTable || gotValue.Ref != metatable {
		// 压栈的元表必须是刚设置的同一个 raw table。
		t.Fatalf("userdata metatable value = %#v, want %p", gotValue, metatable)
	}

	nativeLuaSetTop(luaState, 1)
	nativeLuaPushNil(luaState)
	if got := nativeLuaSetMetatable(luaState, 1); got != 1 {
		// nil metatable 应清除已有元表。
		t.Fatalf("clear userdata metatable = %d, want 1", got)
	}
	if got := nativeLuaGetMetatable(luaState, 1); got != 0 {
		// 清除后 getmetatable 不压栈并返回 0。
		t.Fatalf("lua_getmetatable(cleared userdata) = %d, want 0", got)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 读取不存在的元表不能改变栈。
		t.Fatalf("top after missing getmetatable = %d, want 1", got)
	}
}

// TestNativeLuaTableMetatable 验证 table 目标也能使用同一 C API raw 元表路径。
func TestNativeLuaTableMetatable(t *testing.T) {
	// table metatable 支持是 luaL_newmetatable 与 registry 阶段复用的基础。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 0)
	targetValue := state.ValueAt(-1)
	target := targetValue.Ref.(*runtime.Table)
	nativeLuaCreateTable(luaState, 0, 0)
	metatable := state.ValueAt(-1).Ref.(*runtime.Table)
	if got := nativeLuaSetMetatable(luaState, 1); got != 1 {
		// table 目标设置元表应成功。
		t.Fatalf("lua_setmetatable(table) = %d, want 1", got)
	}
	if target.GetMetatable() != metatable {
		// runtime table raw 元表必须被写入。
		t.Fatalf("table metatable mismatch")
	}
	if got := nativeLuaGetMetatable(luaState, 1); got != 1 {
		// table 目标读取元表应成功。
		t.Fatalf("lua_getmetatable(table) = %d, want 1", got)
	}
	if gotValue := state.ValueAt(-1); gotValue.Ref != metatable {
		// getmetatable 压入的必须是同一元表。
		t.Fatalf("table metatable value = %#v, want %p", gotValue, metatable)
	}
}

// TestNativeLuaSetMetatableRejectsInvalidInput 验证无效输入保持失败安全边界。
func TestNativeLuaSetMetatableRejectsInvalidInput(t *testing.T) {
	// 当前 shim 不执行 api_check panic/longjmp；失败时返回 0 且保持栈不变。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaCreateTable(luaState, 0, 0)
	nativeLuaPushInteger(luaState, 7)
	if got := nativeLuaSetMetatable(luaState, 1); got != 0 {
		// 非 table/nil 元表必须拒绝。
		t.Fatalf("lua_setmetatable(invalid metatable) = %d, want 0", got)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 失败路径不得弹出栈顶，便于后续错误阶段定位问题。
		t.Fatalf("top after invalid setmetatable = %d, want 2", got)
	}
	if got := nativeLuaGetMetatable(luaState, 99); got != 0 {
		// 无效索引读取元表返回 0。
		t.Fatalf("lua_getmetatable(invalid index) = %d, want 0", got)
	}
}

// TestNativeLuaLNewMetatableCreatesAndReusesRegistryEntry 验证 luaL_newmetatable 命名元表语义。
func TestNativeLuaLNewMetatableCreatesAndReusesRegistryEntry(t *testing.T) {
	// 命名元表必须存放在 registry，后续 luaL_checkudata 会按同一名字取回。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	typeNameBytes := []byte{'g', 'l', 'u', 'a', '.', 'u', 'd', 0}
	typeNamePointer := unsafe.Pointer(&typeNameBytes[0])

	if got := nativeLuaLNewMetatable(luaState, typeNamePointer); got != 1 {
		// 首次创建命名元表必须返回 1。
		t.Fatalf("luaL_newmetatable first = %d, want 1", got)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 新元表必须留在栈顶。
		t.Fatalf("top after first luaL_newmetatable = %d, want 1", got)
	}
	createdValue := state.ValueAt(-1)
	if createdValue.Kind != runtime.KindTable {
		// 新建结果必须是 table。
		t.Fatalf("luaL_newmetatable value = %#v, want table", createdValue)
	}
	registryValue := state.ValueAt(runtime.RegistryPseudoIndex)
	registry := registryValue.Ref.(*runtime.Table)
	if registry.RawGetString("glua.ud").Ref != createdValue.Ref {
		// registry 中必须保存同一个命名元表引用。
		t.Fatalf("registry named metatable mismatch")
	}

	if got := nativeLuaLNewMetatable(luaState, typeNamePointer); got != 0 {
		// 第二次遇到已有名字必须返回 0。
		t.Fatalf("luaL_newmetatable second = %d, want 0", got)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 第二次调用会把既有值再压栈。
		t.Fatalf("top after second luaL_newmetatable = %d, want 2", got)
	}
	if reusedValue := state.ValueAt(-1); reusedValue.Ref != createdValue.Ref {
		// 复用路径必须压入同一元表引用，而不是创建新表。
		t.Fatalf("luaL_newmetatable reused = %#v, want %p", reusedValue, createdValue.Ref)
	}
}

// TestNativeLuaLGetMetatableReadsRegistryEntry 验证 luaL_getmetatable 读取 registry 命名元表。
func TestNativeLuaLGetMetatableReadsRegistryEntry(t *testing.T) {
	// 该 helper 与 Lua 5.3 头文件宏保持一致：读取 registry[name] 并压入结果。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	typeNameBytes := []byte{'g', 'l', 'u', 'a', '.', 'm', 't', 0}
	typeNamePointer := unsafe.Pointer(&typeNameBytes[0])

	if got := nativeLuaLGetMetatable(luaState, typeNamePointer); got != nativeLuaTypeNil {
		// 不存在的名字应压入 nil 并返回 nil 类型编号。
		t.Fatalf("missing luaL_getmetatable type = %d, want %d", got, nativeLuaTypeNil)
	}
	if value := state.ValueAt(-1); !value.IsNil() {
		// 缺失命名元表读取结果必须是 nil。
		t.Fatalf("missing luaL_getmetatable value = %#v, want nil", value)
	}
	nativeLuaSetTop(luaState, 0)

	if got := nativeLuaLNewMetatable(luaState, typeNamePointer); got != 1 {
		// 创建命名元表用于后续读取。
		t.Fatalf("luaL_newmetatable = %d, want 1", got)
	}
	created := state.ValueAt(-1)
	nativeLuaSetTop(luaState, 0)
	if got := nativeLuaLGetMetatable(luaState, typeNamePointer); got != nativeLuaTypeTable {
		// 已存在名字应压入 table 并返回 table 类型编号。
		t.Fatalf("existing luaL_getmetatable type = %d, want %d", got, nativeLuaTypeTable)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindTable || value.Ref != created.Ref {
		// 读取到的命名元表必须是 registry 中同一个引用。
		t.Fatalf("existing luaL_getmetatable value = %#v, want %p", value, created.Ref)
	}
}
