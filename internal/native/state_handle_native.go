//go:build native_modules

package native

/*
#include <stdlib.h>

static void* glua_alloc_state_token(void) {
	return malloc(1);
}

static void glua_free_state_token(void* token) {
	free(token);
}

static void glua_free_native_buffer(void* buffer) {
	free(buffer);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// nativeStateHandle 保存 Lua C API shim 使用的 opaque lua_State* 身份。
type nativeStateHandle struct {
	// token 保存 C 分配的不可解引用哨兵指针，后续会作为 lua_State* 传给 C 模块。
	token unsafe.Pointer
}

var (
	// nativeStateHandlesMu 保护 token 到 Go State 的映射，避免 C 回调与 Go loader 并发访问产生竞争。
	nativeStateHandlesMu sync.Mutex
	// nativeStateHandles 保存 C token 与 Go State 的生命周期绑定；key 不保存 Go 指针。
	nativeStateHandles = make(map[uintptr]*runtime.State)
	// nativeStateBuffers 保存 C API 返回给 C 模块的临时 C buffer，并绑定到 lua_State handle 生命周期。
	nativeStateBuffers = make(map[uintptr][]unsafe.Pointer)
	// nativeStateCallBases 保存当前 lua_State handle 上嵌套 C function 调用的 Go State 栈基址。
	nativeStateCallBases = make(map[uintptr][]int)
	// nativeStatePendingErrors 保存 C API lua_error/luaL_error 在返回 C 边界前记录的 Lua error object。
	nativeStatePendingErrors = make(map[uintptr]runtime.Value)
)

// newNativeStateHandle 为当前 State 创建 C 可见的 opaque lua_State* handle。
//
// state 必须非 nil 且未关闭；返回 handle 持有 C 分配的 token，调用方必须在 luaopen_* 或 C
// callback 边界结束后 close，防止 nativeStateHandles 泄漏。
func newNativeStateHandle(state *runtime.State) (*nativeStateHandle, error) {
	// 创建 handle 前先校验 State 生命周期，避免 C 模块拿到已失效 VM 上下文。
	if state == nil {
		// nil State 无法映射到 Lua C API 调用上下文。
		return nil, fmt.Errorf("native lua_State handle requires non-nil state")
	}
	if state.IsClosed() {
		// 已关闭 State 的栈和全局表不可再被 C API shim 访问。
		return nil, fmt.Errorf("native lua_State handle requires open state: %w", runtime.ErrClosedState)
	}
	token := C.glua_alloc_state_token()
	if token == nil {
		// C 分配失败时无法提供稳定的 lua_State* 身份。
		return nil, fmt.Errorf("native lua_State handle allocation failed")
	}
	handle := &nativeStateHandle{token: token}
	nativeStateHandlesMu.Lock()
	nativeStateHandles[uintptr(token)] = state
	nativeStateBuffers[uintptr(token)] = nil
	nativeStateHandlesMu.Unlock()
	return handle, nil
}

// pointer 返回后续可转换为 lua_State* 的 opaque C token。
func (handle *nativeStateHandle) pointer() unsafe.Pointer {
	// nil handle 没有可传给 C 模块的身份。
	if handle == nil {
		return nil
	}
	return handle.token
}

// close 释放 nativeStateHandle 并解除 token 到 State 的映射。
//
// close 可重复调用；首次调用后 token 被清空，后续调用保持 no-op。
func (handle *nativeStateHandle) close() {
	// nil 或已关闭 handle 不需要释放。
	if handle == nil || handle.token == nil {
		// 重复 close 对调用方透明，便于 defer 清理。
		return
	}
	token := handle.token
	handle.token = nil

	nativeStateHandlesMu.Lock()
	delete(nativeStateHandles, uintptr(token))
	buffers := nativeStateBuffers[uintptr(token)]
	delete(nativeStateBuffers, uintptr(token))
	delete(nativeStateCallBases, uintptr(token))
	delete(nativeStatePendingErrors, uintptr(token))
	nativeStateHandlesMu.Unlock()
	for _, buffer := range buffers {
		// buffer 都由 C malloc 分配，可安全在 handle 生命周期结束时统一释放。
		C.glua_free_native_buffer(buffer)
	}
	C.glua_free_state_token(token)
}

// setNativeStatePendingError 记录 C 模块请求抛出的 Lua error object。
func setNativeStatePendingError(token unsafe.Pointer, object runtime.Value) bool {
	// lua_error/luaL_error 不能跨 Go/C 边界 longjmp，因此先把错误对象暂存在 handle 上。
	if token == nil {
		// nil token 无法绑定错误对象，调用方随后会走普通失败路径。
		return false
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	key := uintptr(token)
	state := nativeStateHandles[key]
	if state == nil || state.IsClosed() {
		// State 已失效时不能再把错误传播回 VM。
		return false
	}
	nativeStatePendingErrors[key] = object
	return true
}

// takeNativeStatePendingError 取出并清理当前 handle 上挂起的 Lua error object。
func takeNativeStatePendingError(token unsafe.Pointer) (runtime.Value, bool) {
	// C 函数返回 Go 边界时只消费一次 pending error，避免后续正常调用误报旧错误。
	if token == nil {
		// nil token 没有可消费错误。
		return runtime.NilValue(), false
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	key := uintptr(token)
	object, ok := nativeStatePendingErrors[key]
	if !ok {
		// 没有挂起错误时保持正常返回路径。
		return runtime.NilValue(), false
	}
	delete(nativeStatePendingErrors, key)
	return object, true
}

// pushNativeStateCallBase 记录一次 C function 调用进入时的 Go State 栈基址。
func pushNativeStateCallBase(token unsafe.Pointer, baseTop int) bool {
	// C API 正索引需要相对当前 C function 参数区，而不是整个 Go State 栈底。
	if token == nil {
		// nil token 无法记录调用帧。
		return false
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	key := uintptr(token)
	state := nativeStateHandles[key]
	if state == nil || state.IsClosed() {
		// State 已失效时不能建立 C 调用帧。
		return false
	}
	nativeStateCallBases[key] = append(nativeStateCallBases[key], baseTop)
	return true
}

// popNativeStateCallBase 弹出当前 C function 调用基址。
func popNativeStateCallBase(token unsafe.Pointer) {
	// 调用退出时恢复上一层 C function 基址，支持后续 lua_call/pcall 阶段的嵌套 C 调用。
	if token == nil {
		// nil token 没有可清理状态。
		return
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	key := uintptr(token)
	bases := nativeStateCallBases[key]
	if len(bases) == 0 {
		// 没有活动调用帧时保持 no-op。
		return
	}
	if len(bases) == 1 {
		// 最后一层退出后删除 map 项，避免长期保留空切片。
		delete(nativeStateCallBases, key)
		return
	}
	nativeStateCallBases[key] = bases[:len(bases)-1]
}

// currentNativeStateCallBase 返回当前 C function 调用的 Go State 栈基址。
func currentNativeStateCallBase(token unsafe.Pointer) (int, bool) {
	// 没有活动 C function 时，C API helper 继续使用全局 State 栈索引。
	if token == nil {
		// nil token 没有活动调用帧。
		return 0, false
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	bases := nativeStateCallBases[uintptr(token)]
	if len(bases) == 0 {
		// 未处于 C function 调用中。
		return 0, false
	}
	return bases[len(bases)-1], true
}

// lookupNativeStateHandle 按 C token 查找对应 Go State。
//
// token 必须来自 newNativeStateHandle；未命中或对应 State 已关闭时返回 false。
func lookupNativeStateHandle(token unsafe.Pointer) (*runtime.State, bool) {
	// nil token 不是有效 lua_State*。
	if token == nil {
		return nil, false
	}
	nativeStateHandlesMu.Lock()
	state := nativeStateHandles[uintptr(token)]
	nativeStateHandlesMu.Unlock()
	if state == nil || state.IsClosed() {
		// State 关闭后不允许 C API shim 继续访问 VM。
		return nil, false
	}
	return state, true
}

// rememberNativeStateBuffer 将 C buffer 绑定到 lua_State handle 生命周期。
//
// buffer 必须来自 C malloc；若 token 无效，该方法会释放 buffer 并返回 false，避免泄漏。
func rememberNativeStateBuffer(token unsafe.Pointer, buffer unsafe.Pointer) bool {
	// nil token 或 nil buffer 都没有可记录的生命周期关系。
	if token == nil || buffer == nil {
		// 无效输入直接返回 false，调用方负责保持 no-op。
		return false
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	key := uintptr(token)
	if state := nativeStateHandles[key]; state == nil || state.IsClosed() {
		// handle 已失效时释放刚分配的 C buffer，避免 C API 失败路径泄漏。
		C.glua_free_native_buffer(buffer)
		return false
	}
	nativeStateBuffers[key] = append(nativeStateBuffers[key], buffer)
	return true
}
