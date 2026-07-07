//go:build native_modules

package native

import (
	"errors"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestNativeStateHandleLifecycle 验证 opaque lua_State* handle 能绑定并释放当前 State。
func TestNativeStateHandleLifecycle(t *testing.T) {
	// 构造未关闭 State，模拟 luaopen_* 调用期间的 VM 上下文。
	state := runtime.NewState()
	defer state.Close()

	handle, err := newNativeStateHandle(state)
	if err != nil {
		// 有效 State 应能创建 native handle。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	if handle.pointer() == nil {
		// C 模块需要非 nil lua_State* 身份。
		t.Fatalf("native state handle pointer is nil")
	}

	gotState, ok := lookupNativeStateHandle(handle.pointer())
	if !ok || gotState != state {
		// token 必须稳定映射回创建它的 State。
		t.Fatalf("lookupNativeStateHandle = state=%p ok=%v, want %p true", gotState, ok, state)
	}

	handle.close()
	if handle.pointer() != nil {
		// 关闭后不得再暴露已释放 C token。
		t.Fatalf("native state handle pointer after close = %p, want nil", handle.pointer())
	}
	if gotState, ok := lookupNativeStateHandle(handle.pointer()); ok || gotState != nil {
		// 已关闭 handle 不能再被查到。
		t.Fatalf("lookup closed handle = state=%p ok=%v, want nil false", gotState, ok)
	}
	handle.close()
}

// TestNativeStateHandleRejectsInvalidState 验证 nil 和已关闭 State 不会生成 C token。
func TestNativeStateHandleRejectsInvalidState(t *testing.T) {
	// nil State 没有可映射的 VM 上下文。
	if handle, err := newNativeStateHandle(nil); err == nil || handle != nil {
		t.Fatalf("newNativeStateHandle(nil) = handle=%v err=%v, want error", handle, err)
	}

	state := runtime.NewState()
	state.Close()
	handle, err := newNativeStateHandle(state)
	if err == nil || handle != nil {
		// 已关闭 State 不能被 C API shim 访问。
		t.Fatalf("newNativeStateHandle(closed) = handle=%v err=%v, want error", handle, err)
	}
	if !errors.Is(err, runtime.ErrClosedState) {
		// 错误链需要保留 ErrClosedState，便于上层分类。
		t.Fatalf("newNativeStateHandle(closed) err = %v, want ErrClosedState", err)
	}
}

// TestNativeStateHandleLookupRejectsClosedState 验证 State 关闭后 token 查找会被拒绝。
func TestNativeStateHandleLookupRejectsClosedState(t *testing.T) {
	// 创建 handle 后关闭 State，模拟 C 模块持有过期 lua_State* 的情况。
	state := runtime.NewState()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// 有效 State 应能创建 handle。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()

	state.Close()
	if gotState, ok := lookupNativeStateHandle(handle.pointer()); ok || gotState != nil {
		// State 关闭后不能再把 token 映射回 VM。
		t.Fatalf("lookup after state close = state=%p ok=%v, want nil false", gotState, ok)
	}
}
