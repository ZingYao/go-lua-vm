//go:build native_modules

package native

import (
	"testing"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaNewUserdata 验证 lua_newuserdata 创建 full userdata 并返回稳定 C 指针。
func TestNativeLuaNewUserdata(t *testing.T) {
	// 使用真实 State 与 opaque handle，确保 userdata 进入 Go VM 栈和 State 关闭路径。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用，无法验证 userdata API。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	pointer := nativeLuaNewUserdata(luaState, 8)
	if pointer == nil {
		// 有效 State 和小内存分配必须返回非 nil C 数据区。
		t.Fatalf("lua_newuserdata returned nil")
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// lua_newuserdata 必须把 full userdata 压到栈顶。
		t.Fatalf("lua_newuserdata top = %d, want 1", got)
	}
	value := state.ValueAt(-1)
	if value.Kind != runtime.KindUserdata {
		// 栈顶值必须是 runtime userdata。
		t.Fatalf("lua_newuserdata value = %#v, want userdata", value)
	}
	userdata, ok := value.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// userdata 引用负载必须是 *runtime.Userdata。
		t.Fatalf("lua_newuserdata ref = %#v, want *runtime.Userdata", value.Ref)
	}
	block, ok := userdata.Data.(*nativeUserdataBlock)
	if !ok || block == nil {
		// native userdata 必须保存 C 内存 block，供 lua_touserdata 返回。
		t.Fatalf("lua_newuserdata data = %#v, want *nativeUserdataBlock", userdata.Data)
	}
	if block.size != 8 {
		// 逻辑长度需要保留，后续 luaL_checkudata 和调试边界会依赖该元信息。
		t.Fatalf("userdata block size = %d, want 8", block.size)
	}
	if got := nativeLuaToUserdata(luaState, -1); got != pointer {
		// touserdata 必须返回创建时同一 C 数据区地址。
		t.Fatalf("lua_touserdata = %p, want %p", got, pointer)
	}

	bytes := unsafe.Slice((*byte)(pointer), 8)
	bytes[0] = 0x53
	bytes[7] = 0x36
	if bytes[0] != 0x53 || bytes[7] != 0x36 {
		// C 可见内存必须可读写，后续 C 模块才能保存原生对象状态。
		t.Fatalf("userdata memory write/read mismatch")
	}

	state.Close()
	if block.data() != nil {
		// State.Close 必须触发 native userdata finalizer，释放 C 内存并清空指针。
		t.Fatalf("userdata block after State.Close = %p, want nil", block.data())
	}
}

// TestNativeLuaNewZeroSizeUserdata 验证 0 字节 full userdata 仍有稳定 identity。
func TestNativeLuaNewZeroSizeUserdata(t *testing.T) {
	// Lua 允许 0 字节 userdata；shim 需要返回可比较的非 nil 指针。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	pointer := nativeLuaNewUserdata(luaState, 0)
	if pointer == nil {
		// 0 字节 userdata 也需要稳定 C identity。
		t.Fatalf("zero-size lua_newuserdata returned nil")
	}
	if got := nativeLuaToUserdata(luaState, -1); got != pointer {
		// 0 字节 userdata 的 touserdata 仍必须返回同一地址。
		t.Fatalf("zero-size lua_touserdata = %p, want %p", got, pointer)
	}
}

// TestNativeLuaToUserdataRejectsNonUserdata 验证非 native userdata 不会被误暴露为 C 指针。
func TestNativeLuaToUserdataRejectsNonUserdata(t *testing.T) {
	// 构造普通栈值和纯 Go userdata，覆盖转换失败边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 1)
	if got := nativeLuaToUserdata(luaState, -1); got != nil {
		// integer 不能转换为 userdata。
		t.Fatalf("lua_touserdata(integer) = %p, want nil", got)
	}

	goUserdata := runtime.NewUserdata("go-only")
	if err := state.Push(goUserdata.Value()); err != nil {
		// 测试需要把纯 Go userdata 放入栈顶。
		t.Fatalf("push go userdata failed: %v", err)
	}
	if got := nativeLuaToUserdata(luaState, -1); got != nil {
		// 非 native 创建的 userdata 没有 C full userdata 数据区，必须返回 nil。
		t.Fatalf("lua_touserdata(go userdata) = %p, want nil", got)
	}
	if got := nativeLuaToUserdata(nil, -1); got != nil {
		// nil State 不能读取任何 userdata。
		t.Fatalf("lua_touserdata(nil state) = %p, want nil", got)
	}
}
