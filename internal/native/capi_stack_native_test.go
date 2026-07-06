//go:build native_modules

package native

import (
	"testing"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeCAPIStackPrimitives 验证 Lua C API 最小栈 shim 可操作 Go State 栈。
func TestNativeCAPIStackPrimitives(t *testing.T) {
	// 测试使用真实 runtime.State 和 opaque handle，确保 C 调用路径不依赖影子栈。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明上一阶段 State 映射不可用，本阶段无法继续验证。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if got := nativeLuaStackTop(luaState); got != 0 {
		// 新 State 应该从空栈开始。
		t.Fatalf("lua_gettop empty = %d, want 0", got)
	}
	nativeLuaPushNil(luaState)
	nativeLuaPushBoolean(luaState, false)
	nativeLuaPushBoolean(luaState, true)
	nativeLuaPushInteger(luaState, 53)
	nativeLuaPushNumber(luaState, 2.5)
	cString := []byte{'l', 'u', 'a', 0}
	cStringPointer := unsafe.Pointer(&cString[0])
	if returned := nativeLuaPushString(luaState, cStringPointer); returned != cStringPointer {
		// lua_pushstring 至少应返回传入的 C 字符串指针，后续会替换成稳定内部字符串指针策略。
		t.Fatalf("lua_pushstring returned %p, want %p", returned, cStringPointer)
	}
	binary := []byte{'a', 0, 'b'}
	cBinary := unsafe.Pointer(&binary[0])
	if returned := nativeLuaPushLString(luaState, cBinary, uintptr(len(binary))); returned != cBinary {
		// lua_pushlstring 当前返回传入指针，证明 C ABI 返回路径贯通。
		t.Fatalf("lua_pushlstring returned %p, want %p", returned, cBinary)
	}

	if got := nativeLuaStackTop(luaState); got != 7 {
		// 七次压栈后栈顶必须与 Go State 栈深一致。
		t.Fatalf("lua_gettop after pushes = %d, want 7", got)
	}
	if value := state.ValueAt(1); !value.IsNil() {
		// 第一项由 lua_pushnil 压入。
		t.Fatalf("stack[1] = %#v, want nil", value)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindBoolean || value.Bool {
		// 第二项由 lua_pushboolean false 压入。
		t.Fatalf("stack[2] = %#v, want false", value)
	}
	if value := state.ValueAt(3); value.Kind != runtime.KindBoolean || !value.Bool {
		// 第三项由 lua_pushboolean true 压入。
		t.Fatalf("stack[3] = %#v, want true", value)
	}
	if value := state.ValueAt(4); value.Kind != runtime.KindInteger || value.Integer != 53 {
		// 第四项由 lua_pushinteger 压入。
		t.Fatalf("stack[4] = %#v, want integer 53", value)
	}
	if value := state.ValueAt(5); value.Kind != runtime.KindNumber || value.Number != 2.5 {
		// 第五项由 lua_pushnumber 压入。
		t.Fatalf("stack[5] = %#v, want number 2.5", value)
	}
	if value := state.ValueAt(6); value.Kind != runtime.KindString || value.String != "lua" {
		// 第六项由 lua_pushstring 压入。
		t.Fatalf("stack[6] = %#v, want string lua", value)
	}
	if value := state.ValueAt(7); value.Kind != runtime.KindString || value.String != string(binary) {
		// 第七项由 lua_pushlstring 压入，必须保留内嵌 NUL 字节。
		t.Fatalf("stack[7] = %#v, want binary string", value)
	}
	nativeLuaPushValueAt(luaState, 4)
	if got := nativeLuaStackTop(luaState); got != 8 {
		// pushvalue 必须把指定索引的值复制到栈顶。
		t.Fatalf("lua_pushvalue top = %d, want 8", got)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindInteger || value.Integer != 53 {
		// 第四项 integer 53 应被复制到新栈顶。
		t.Fatalf("lua_pushvalue copied value = %#v, want integer 53", value)
	}
	nativeLuaPushValueAt(luaState, 99)
	if got := nativeLuaStackTop(luaState); got != 8 {
		// 无效索引复制保持 no-op，避免破坏 C 模块当前栈。
		t.Fatalf("invalid lua_pushvalue top = %d, want 8", got)
	}

	nativeLuaSetTop(luaState, 10)
	if got := nativeLuaStackTop(luaState); got != 10 {
		// 正索引扩栈必须用 nil 补齐到目标栈顶。
		t.Fatalf("lua_settop grow top = %d, want 10", got)
	}
	if value := state.ValueAt(10); !value.IsNil() {
		// 扩栈新增槽位必须是 nil。
		t.Fatalf("stack[10] = %#v, want nil", value)
	}
	nativeLuaSetTop(luaState, -2)
	if got := nativeLuaStackTop(luaState); got != 9 {
		// 负索引 -2 应弹出一个栈顶值。
		t.Fatalf("lua_settop -2 top = %d, want 9", got)
	}
	nativeLuaSetTop(luaState, 0)
	if got := nativeLuaStackTop(luaState); got != 0 {
		// settop(0) 必须清空栈。
		t.Fatalf("lua_settop clear top = %d, want 0", got)
	}
}

// TestNativeCAPIStackPrimitivesRejectInvalidState 验证失效 State handle 的最小安全边界。
func TestNativeCAPIStackPrimitivesRejectInvalidState(t *testing.T) {
	// nil lua_State* 没有可映射 State，所有操作必须保持失败安全。
	if got := nativeLuaStackTop(nil); got != 0 {
		// 无效 State 查询栈顶固定为 0。
		t.Fatalf("lua_gettop nil = %d, want 0", got)
	}
	nativeLuaSetTop(nil, 3)
	nativeLuaPushNil(nil)
	nativeLuaPushBoolean(nil, true)
	nativeLuaPushInteger(nil, 1)
	nativeLuaPushNumber(nil, 1)
	nativeLuaPushString(nil, nil)
	nativeLuaPushLString(nil, nil, 0)

	state := runtime.NewState()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明测试无法建立关闭 State 场景。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	luaState := handle.pointer()
	state.Close()
	nativeLuaPushInteger(luaState, 7)
	if got := nativeLuaStackTop(luaState); got != 0 {
		// State 关闭后 lookup 必须拒绝，C API 不能继续写入栈。
		t.Fatalf("lua_gettop closed = %d, want 0", got)
	}
	handle.close()
}
