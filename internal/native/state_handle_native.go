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

	"github.com/ZingYao/go-lua-vm/runtime"
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
	// nativeStateCallBuffers 保存当前 lua_State handle 上嵌套 C function 调用期间创建的临时 C buffer。
	nativeStateCallBuffers = make(map[uintptr][][]unsafe.Pointer)
	// nativeStateCallBases 保存当前 lua_State handle 上嵌套 C function 调用的 Go State 栈基址。
	nativeStateCallBases = make(map[uintptr][]int)
	// nativeStateCallUpvalues 保存当前 lua_State handle 上嵌套 C closure 调用的 upvalue 快照。
	nativeStateCallUpvalues = make(map[uintptr][][]runtime.Value)
	// nativeStatePendingErrors 保存 C API lua_error/luaL_error 在返回 C 边界前记录的 Lua error object。
	nativeStatePendingErrors = make(map[uintptr]runtime.Value)
	// nativeStateLightUserdatas 保存 lightuserdata 指针到 Lua userdata identity 的稳定映射。
	nativeStateLightUserdatas = make(map[uintptr]map[uintptr]*runtime.Userdata)
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
	nativeStateLightUserdatas[uintptr(token)] = make(map[uintptr]*runtime.Userdata)
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
	key := uintptr(token)
	state := nativeStateHandles[key]
	callFrameCount := len(nativeStateCallBases[key])
	delete(nativeStateHandles, key)
	buffers := nativeStateBuffers[key]
	callBuffers := nativeStateCallBuffers[key]
	delete(nativeStateBuffers, key)
	delete(nativeStateCallBuffers, key)
	delete(nativeStateCallBases, key)
	delete(nativeStateCallUpvalues, key)
	delete(nativeStatePendingErrors, key)
	delete(nativeStateLightUserdatas, key)
	nativeStateHandlesMu.Unlock()
	for index := 0; index < callFrameCount; index++ {
		// handle 关闭时若仍有未弹出的 C frame，同步回收对应 external root 帧。
		if state != nil {
			state.PopExternalGCRootFrame()
		}
	}
	for _, buffer := range buffers {
		// buffer 都由 C malloc 分配，可安全在 handle 生命周期结束时统一释放。
		C.glua_free_native_buffer(buffer)
	}
	for _, buffers := range callBuffers {
		// 异常关闭时仍可能存在未弹出的 C frame buffer，需要随 handle 一起兜底释放。
		freeNativeStateBuffers(buffers)
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

// pushNativeStateCallFrame 记录一次 C function 调用进入时的 Go State 栈基址与 upvalue。
func pushNativeStateCallFrame(token unsafe.Pointer, baseTop int, upvalues []runtime.Value) bool {
	// C API 正索引需要相对当前 C function 参数区，而不是整个 Go State 栈底。
	if token == nil {
		// nil token 无法记录调用帧。
		return false
	}
	upvalueSnapshot := append([]runtime.Value(nil), upvalues...)
	nativeStateHandlesMu.Lock()
	key := uintptr(token)
	state := nativeStateHandles[key]
	if state == nil || state.IsClosed() {
		// State 已失效时不能建立 C 调用帧。
		nativeStateHandlesMu.Unlock()
		return false
	}
	nativeStateCallBases[key] = append(nativeStateCallBases[key], baseTop)
	nativeStateCallUpvalues[key] = append(nativeStateCallUpvalues[key], upvalueSnapshot)
	nativeStateCallBuffers[key] = append(nativeStateCallBuffers[key], nil)
	nativeStateHandlesMu.Unlock()
	if !state.PushExternalGCRootFrame(upvalueSnapshot) {
		// external root 登记失败时回滚刚建立的 C frame，避免 upvalue 栈与 GC 根栈错位。
		popNativeStateCallFrame(token)
		return false
	}
	return true
}

// popNativeStateCallFrame 弹出当前 C function 调用基址与 upvalue。
func popNativeStateCallFrame(token unsafe.Pointer) {
	// 调用退出时恢复上一层 C function 基址，支持后续 lua_call/pcall 阶段的嵌套 C 调用。
	if token == nil {
		// nil token 没有可清理状态。
		return
	}
	nativeStateHandlesMu.Lock()
	key := uintptr(token)
	state := nativeStateHandles[key]
	bases := nativeStateCallBases[key]
	if len(bases) == 0 {
		// 没有活动调用帧时保持 no-op。
		nativeStateHandlesMu.Unlock()
		return
	}
	callBuffers := nativeStateCallBuffers[key]
	var buffers []unsafe.Pointer
	if len(callBuffers) > 0 {
		// C frame buffer 与调用帧同步压栈；弹出当前帧时只释放最内层 buffer。
		buffers = callBuffers[len(callBuffers)-1]
		if len(callBuffers) == 1 {
			// 最后一层 frame buffer 退出后删除 map 项，避免长期保留空切片。
			delete(nativeStateCallBuffers, key)
		} else {
			// 外层 C frame 仍在执行，保留其对应的 buffer 切片。
			nativeStateCallBuffers[key] = callBuffers[:len(callBuffers)-1]
		}
	}
	if len(bases) == 1 {
		// 最后一层退出后删除 map 项，避免长期保留空切片。
		delete(nativeStateCallBases, key)
		delete(nativeStateCallUpvalues, key)
		nativeStateHandlesMu.Unlock()
		freeNativeStateBuffers(buffers)
		if state != nil {
			// 同步弹出当前 C frame 的 external root 帧。
			state.PopExternalGCRootFrame()
		}
		return
	}
	nativeStateCallBases[key] = bases[:len(bases)-1]
	upvalueFrames := nativeStateCallUpvalues[key]
	if len(upvalueFrames) <= 1 {
		// upvalue 栈与基址栈理论上同步；异常时清空，避免后续读到错配闭包。
		delete(nativeStateCallUpvalues, key)
		nativeStateHandlesMu.Unlock()
		freeNativeStateBuffers(buffers)
		if state != nil {
			// 即便 upvalue 栈异常，也要弹出与基址帧对应的 external root。
			state.PopExternalGCRootFrame()
		}
		return
	}
	nativeStateCallUpvalues[key] = upvalueFrames[:len(upvalueFrames)-1]
	nativeStateHandlesMu.Unlock()
	freeNativeStateBuffers(buffers)
	if state != nil {
		// 同步弹出当前 C frame 的 external root 帧。
		state.PopExternalGCRootFrame()
	}
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

// currentNativeStateCallUpvalue 返回当前 C closure 调用的指定 upvalue。
func currentNativeStateCallUpvalue(token unsafe.Pointer, index int) (runtime.Value, bool) {
	// Lua C API 的 upvalue 从 1 开始编号；0 或负数不是合法 upvalue。
	if token == nil || index <= 0 {
		// 无效 token 或编号不能读出 upvalue。
		return runtime.NilValue(), false
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	upvalueFrames := nativeStateCallUpvalues[uintptr(token)]
	if len(upvalueFrames) == 0 {
		// 当前没有 C closure 调用帧。
		return runtime.NilValue(), false
	}
	upvalues := upvalueFrames[len(upvalueFrames)-1]
	if index > len(upvalues) {
		// 超出当前 closure 捕获数量时按 none 处理。
		return runtime.NilValue(), false
	}
	return upvalues[index-1], true
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
	callBuffers := nativeStateCallBuffers[key]
	if len(callBuffers) > 0 {
		// 活动 C function 内返回的 C 字符串指针只需撑到当前 C 调用退出，避免长连接反复转换泄漏。
		lastFrame := len(callBuffers) - 1
		callBuffers[lastFrame] = append(callBuffers[lastFrame], buffer)
		nativeStateCallBuffers[key] = callBuffers
		return true
	}
	nativeStateBuffers[key] = append(nativeStateBuffers[key], buffer)
	return true
}

// freeNativeStateBuffers 释放由 C malloc 分配并挂到 native State 生命周期里的 buffer。
func freeNativeStateBuffers(buffers []unsafe.Pointer) {
	// buffer 均由 native C helper 分配，释放时不触碰 Go VM 栈和值对象。
	for _, buffer := range buffers {
		// nil buffer 可能来自异常回滚路径，保持 no-op。
		if buffer == nil {
			// nil 指针不需要释放，继续处理后续 buffer。
			continue
		}
		C.glua_free_native_buffer(buffer)
	}
}

// nativeLightUserdata 保存 Lua lightuserdata 的 C 指针 identity。
type nativeLightUserdata struct {
	// pointer 保存 C 模块传入的裸指针数值；0 对应合法的 NULL lightuserdata。
	pointer uintptr
}

// nativeLightUserdataValue 返回当前 lua_State 内指定指针的稳定 lightuserdata 值。
func nativeLightUserdataValue(token unsafe.Pointer, pointer unsafe.Pointer) (runtime.Value, bool) {
	// lightuserdata 是不可回收裸指针；这里用 runtime.Userdata 承载 Lua 侧 identity。
	if token == nil {
		// nil lua_State 无法建立 per-State identity。
		return runtime.NilValue(), false
	}
	nativeStateHandlesMu.Lock()
	defer nativeStateHandlesMu.Unlock()
	key := uintptr(token)
	state := nativeStateHandles[key]
	if state == nil || state.IsClosed() {
		// State 已失效时不能创建或读取 Lua 值。
		return runtime.NilValue(), false
	}
	pointerKey := uintptr(pointer)
	userdatas := nativeStateLightUserdatas[key]
	if userdatas == nil {
		// 防御旧 handle 或测试直接构造 map 缺失场景。
		userdatas = make(map[uintptr]*runtime.Userdata)
		nativeStateLightUserdatas[key] = userdatas
	}
	userdata := userdatas[pointerKey]
	if userdata == nil {
		// 同一 State 内同一裸指针必须复用同一 userdata 对象，保证 raw equality 稳定。
		userdata = runtime.NewUserdata(nativeLightUserdata{pointer: pointerKey})
		userdatas[pointerKey] = userdata
	}
	return userdata.Value(), true
}
