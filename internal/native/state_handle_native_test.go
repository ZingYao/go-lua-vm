//go:build native_modules

package native

import (
	"errors"
	"testing"
	"unsafe"

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

// TestNativeStateBuffersFollowCallFrames 验证 C frame 内创建的临时 buffer 随 frame 退出回收。
func TestNativeStateBuffersFollowCallFrames(t *testing.T) {
	// 构造真实 State 和 native handle，直接观测 handle 级与 C frame 级 buffer 归属。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 buffer 生命周期。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	token := uintptr(luaState)

	outside := []byte{'o', 'u', 't', 's', 'i', 'd', 'e', 0}
	if returned := nativeLuaPushString(luaState, unsafe.Pointer(&outside[0])); returned == nil {
		// 无 C frame 时仍需返回 handle 生命周期内稳定的 C 字符串副本。
		t.Fatalf("nativeLuaPushString outside frame returned nil")
	}
	nativeStateHandlesMu.Lock()
	handleBufferCount := len(nativeStateBuffers[token])
	callFrameCount := len(nativeStateCallBuffers[token])
	nativeStateHandlesMu.Unlock()
	if handleBufferCount != 1 || callFrameCount != 0 {
		// 无活动 C frame 的 buffer 必须保持在 handle 级列表，不能误进 frame 栈。
		t.Fatalf("outside buffer counts = handle %d frame %d, want 1 0", handleBufferCount, callFrameCount)
	}

	if !pushNativeStateCallFrame(luaState, state.StackTop(), nil) {
		// 无法建立 outer C frame 时，本测试不能继续验证 frame buffer。
		t.Fatalf("pushNativeStateCallFrame outer failed")
	}
	outer := []byte{'o', 'u', 't', 'e', 'r', 0}
	if returned := nativeLuaPushString(luaState, unsafe.Pointer(&outer[0])); returned == nil {
		// outer frame 内仍需向 C 模块返回可读指针。
		t.Fatalf("nativeLuaPushString outer frame returned nil")
	}

	if !pushNativeStateCallFrame(luaState, state.StackTop(), nil) {
		// 无法建立 inner C frame 时，需要先弹出 outer frame 再失败。
		popNativeStateCallFrame(luaState)
		t.Fatalf("pushNativeStateCallFrame inner failed")
	}
	inner := []byte{'i', 'n', 'n', 'e', 'r', 0}
	if returned := nativeLuaPushString(luaState, unsafe.Pointer(&inner[0])); returned == nil {
		// inner frame 内仍需向 C 模块返回可读指针。
		popNativeStateCallFrame(luaState)
		popNativeStateCallFrame(luaState)
		t.Fatalf("nativeLuaPushString inner frame returned nil")
	}

	nativeStateHandlesMu.Lock()
	handleBufferCount = len(nativeStateBuffers[token])
	callBuffers := nativeStateCallBuffers[token]
	outerBufferCount := 0
	if len(callBuffers) > 0 {
		// 只有实际建立了 outer frame buffer 后才能读取该层计数。
		outerBufferCount = len(callBuffers[0])
	}
	innerBufferCount := 0
	if len(callBuffers) > 1 {
		// 只有实际建立了 inner frame buffer 后才能读取该层计数。
		innerBufferCount = len(callBuffers[1])
	}
	nativeStateHandlesMu.Unlock()
	if handleBufferCount != 1 || len(callBuffers) != 2 || outerBufferCount != 1 || innerBufferCount != 1 {
		// 嵌套 C frame 必须各自持有本层创建的临时 buffer。
		popNativeStateCallFrame(luaState)
		popNativeStateCallFrame(luaState)
		t.Fatalf("nested buffer counts = handle %d frames %d outer %d inner %d, want 1 2 1 1", handleBufferCount, len(callBuffers), outerBufferCount, innerBufferCount)
	}

	popNativeStateCallFrame(luaState)
	nativeStateHandlesMu.Lock()
	handleBufferCount = len(nativeStateBuffers[token])
	callBuffers = nativeStateCallBuffers[token]
	nativeStateHandlesMu.Unlock()
	if handleBufferCount != 1 || len(callBuffers) != 1 || len(callBuffers[0]) != 1 {
		// 弹出 inner frame 后只应保留 outer frame 的临时 buffer。
		popNativeStateCallFrame(luaState)
		t.Fatalf("after inner pop counts = handle %d frames %d, want 1 1", handleBufferCount, len(callBuffers))
	}

	popNativeStateCallFrame(luaState)
	nativeStateHandlesMu.Lock()
	handleBufferCount = len(nativeStateBuffers[token])
	_, stillTracksFrames := nativeStateCallBuffers[token]
	nativeStateHandlesMu.Unlock()
	if handleBufferCount != 1 || stillTracksFrames {
		// 所有 C frame 退出后，frame buffer 栈必须清空且不影响 handle 级 buffer。
		t.Fatalf("after outer pop = handle %d tracksFrames %v, want 1 false", handleBufferCount, stillTracksFrames)
	}
}
