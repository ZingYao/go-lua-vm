//go:build native_modules

package native

import (
	"testing"

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
