//go:build native_modules

package native

import (
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaBufferAddVisibleValueConsumesVisibleTop 验证 luaL_addvalue 核心逻辑追加并弹出当前可见栈顶。
func TestNativeLuaBufferAddVisibleValueConsumesVisibleTop(t *testing.T) {
	// 使用纯 Go helper 覆盖 luaL_addvalue 的栈读写语义，避免在 _test.go 中引入 cgo。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushValue(luaState, runtime.StringValue("visible"))
	var appended string
	if ok := nativeLuaBufferAddVisibleValue(luaState, func(text string) {
		// 记录追加内容，验证 helper 没有改变 Lua 字符串字节。
		appended += text
	}); !ok {
		// 可见字符串应追加成功。
		t.Fatalf("nativeLuaBufferAddVisibleValue returned false, want true")
	}
	if got := state.StackTop(); got != 0 {
		// 栈顶字符串被追加后必须被消费。
		t.Fatalf("top after add visible value = %d, want 0", got)
	}
	if appended != "visible" {
		// 追加内容必须来自刚才的可见栈顶。
		t.Fatalf("appended value = %q, want visible", appended)
	}
}

// TestNativeLuaBufferAddVisibleValueRespectsCurrentCFrameBase 验证 luaL_addvalue 核心逻辑不穿透当前 C 帧基址。
func TestNativeLuaBufferAddVisibleValueRespectsCurrentCFrameBase(t *testing.T) {
	// 外层 sentinel 模拟调用者栈；当前 C 帧没有可见值时 addvalue 必须保持 no-op。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
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
		// C frame 基址记录失败时无法验证可见栈边界。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	var appended string
	if ok := nativeLuaBufferAddVisibleValue(luaState, func(text string) {
		// 无可见值时该回调不应被调用。
		appended += text
	}); ok {
		// 当前 C 帧没有可见值，helper 应保持失败安全 no-op。
		t.Fatalf("nativeLuaBufferAddVisibleValue returned true, want false")
	}
	if got := state.StackTop(); got != baseTop {
		// 当前 C 帧没有可见值，不能弹掉调用者栈上的 sentinel。
		t.Fatalf("global top after empty-frame add value = %d, want %d", got, baseTop)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层 sentinel 必须仍保持原值。
		t.Fatalf("outer sentinel after empty-frame add value = %#v, want outer string", value)
	}
	if appended != "" {
		// 无可见值时不能向 buffer 追加调用者栈内容。
		t.Fatalf("appended after empty-frame add value = %q, want empty", appended)
	}
}
