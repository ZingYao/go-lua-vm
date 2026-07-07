//go:build native_modules

package native

/*
#include <stddef.h>

typedef struct lua_State lua_State;
typedef long long lua_Integer;
typedef double lua_Number;
*/
import "C"

import (
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

const nativeMaxInt = int(^uint(0) >> 1)

// nativeLuaStackTop 读取 C API shim 视角下的当前栈顶。
func nativeLuaStackTop(luaState unsafe.Pointer) int {
	// 入口先把 opaque token 映射回 Go State，失效 handle 按 Lua C API 防御边界返回空栈。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效或已关闭 State 没有可读栈，返回 0 让 C 模块留在失败安全路径。
		return 0
	}
	if baseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内的 lua_gettop 返回当前调用帧可见槽位数，即参数和后续压入值数量。
		visibleTop := state.StackTop() - baseTop
		if visibleTop < 0 {
			// 栈基址损坏时返回 0，避免 C 模块继续误读。
			return 0
		}
		return visibleTop
	}
	return state.StackTop()
}

// nativeLuaAbsIndex 把 Lua C API 索引规范化为当前调用帧可见的稳定正索引。
func nativeLuaAbsIndex(luaState unsafe.Pointer, index int) int {
	// 入口先校验 State 生命周期；无效 State 不返回可继续使用的栈索引。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效或已关闭 State 没有可规范化的栈索引。
		return 0
	}
	if index <= runtime.RegistryPseudoIndex {
		// registry 和 upvalue pseudo-index 不是普通栈槽，Lua 5.3 规定 absindex 保持原值。
		return index
	}
	if index > 0 {
		// 正索引已经是当前 C API 视角的稳定索引，不能转换成 Go State 全局槽位。
		return index
	}
	if index == 0 {
		// Lua 栈索引 0 无效，返回 0 供调用方识别。
		return 0
	}
	visibleTop := state.StackTop()
	if baseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内负索引必须按当前调用帧可见栈顶计算，不能穿透到外层 Go VM 栈。
		visibleTop -= baseTop
	}
	if visibleTop < 0 {
		// 调用帧基址损坏时不返回可用索引。
		return 0
	}
	return visibleTop + index + 1
}

// nativeLuaPopVisible 从当前 C API 可见栈顶弹出一个值。
func nativeLuaPopVisible(luaState unsafe.Pointer, state *runtime.State) (runtime.Value, bool) {
	// 弹栈前先确认当前调用帧基址，防止 C API 在参数不足时穿透外层 Go VM 栈。
	if state == nil || state.IsClosed() {
		// 无效或已关闭 State 没有可安全消费的栈值。
		return runtime.NilValue(), false
	}
	baseTop := 0
	if currentBaseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内只能消费进入该 C 帧之后的可见槽位。
		baseTop = currentBaseTop
	}
	if state.StackTop() <= baseTop {
		// 当前 C 帧没有可见值时保持栈不变，避免弹掉调用者栈上的值。
		return runtime.NilValue(), false
	}
	value, err := state.Pop()
	if err != nil {
		// Pop 失败说明 State 生命周期或栈边界异常，调用方应保持 no-op。
		return runtime.NilValue(), false
	}
	return value, true
}

// nativeLuaSetTop 按 Lua 5.3 lua_settop 语义调整栈顶。
func nativeLuaSetTop(luaState unsafe.Pointer, index int) {
	// 入口先解析 State，失效 handle 不能修改任何 Go VM 状态。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// native 模块持有的 State 已失效，当前最小 shim 选择 no-op，错误边界后续由 lua_error 阶段补齐。
		return
	}
	baseTop := 0
	if currentBaseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内的正索引和 top 都相对当前调用帧参数区。
		baseTop = currentBaseTop
	}
	currentTop := state.StackTop()
	currentVisibleTop := currentTop - baseTop
	if currentVisibleTop < 0 {
		// 基址异常时不调整栈，避免破坏外层 VM 栈。
		return
	}
	targetTop := index
	if index < 0 {
		// Lua 负索引从当前栈顶计算目标位置，-1 表示保持原栈顶，-2 表示弹出一个值。
		targetTop = currentVisibleTop + index + 1
	}
	if targetTop < 0 {
		// 官方 API 对无效负索引有 api_check；本 shim 暂不 panic/longjmp，保持原栈不变。
		return
	}
	globalTargetTop := baseTop + targetTop
	for currentTop > globalTargetTop {
		// 目标栈顶更低时逐个弹出，Pop 会清理槽位避免保留引用。
		if _, err := state.Pop(); err != nil {
			// Pop 失败只可能来自关闭或下溢；此处停止调整，避免扩大副作用。
			return
		}
		currentTop--
	}
	for currentTop < globalTargetTop {
		// 目标栈顶更高时按 Lua 语义用 nil 填充新增槽位。
		if err := state.Push(runtime.NilValue()); err != nil {
			// 栈上限错误暂时停止扩展，后续错误阶段会把该状态转换成 Lua 错误。
			return
		}
		currentTop++
	}
}

// nativeLuaCheckStack 检查当前最小 shim 是否可继续扩展栈。
func nativeLuaCheckStack(luaState unsafe.Pointer, extraSlots int) bool {
	// 当前 Go State 栈是动态 slice，最小 shim 只需要校验 State 生命周期和非负请求。
	if extraSlots < 0 {
		// 负数扩展请求非法，按失败处理。
		return false
	}
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 不能继续压栈。
		return false
	}
	_, _ = state, extraSlots
	return true
}

// nativeLuaRotate 按 Lua 5.3 C API 规则旋转 idx..top 的栈段。
func nativeLuaRotate(luaState unsafe.Pointer, index int, rotateCount int) {
	// 入口先解析 State；失效 handle 不能修改任何 Go VM 状态。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 保持 no-op。
		return
	}
	top := state.StackTop()
	absoluteIndex := state.AbsIndex(index)
	if baseTop, ok := currentNativeStateCallBase(luaState); ok {
		if index > 0 {
			// C function 内正索引从当前调用帧参数区开始。
			// index 是 1-based 可见槽位，因此全局绝对索引需要加上调用进入前栈顶。
			absoluteIndex = baseTop + index
		} else if index < 0 && absoluteIndex <= baseTop {
			// C function 内负索引不得把旋转区间扩展到调用帧之前的外层 Go VM 栈。
			return
		}
	}
	if absoluteIndex <= 0 || absoluteIndex > top {
		// 无效区间按当前 api_check 策略保持 no-op。
		return
	}
	segmentLength := top - absoluteIndex + 1
	if segmentLength <= 1 {
		// 长度 0/1 的区间旋转没有可观察副作用。
		return
	}
	offset := rotateCount % segmentLength
	if offset < 0 {
		// 负数左旋转转换为等价右旋转。
		offset += segmentLength
	}
	if offset == 0 {
		// 整圈旋转没有副作用。
		return
	}
	values := make([]runtime.Value, segmentLength)
	for valueIndex := 0; valueIndex < segmentLength; valueIndex++ {
		// 保存原始区间，避免边改边读造成错位。
		values[valueIndex] = state.ValueAt(absoluteIndex + valueIndex)
	}
	rotated := append(values[segmentLength-offset:], values[:segmentLength-offset]...)
	nativeLuaRestoreStackTop(state, absoluteIndex-1)
	for valueIndex := range rotated {
		// 重新压入旋转后的区间，保持区间外栈值不变。
		if err := state.Push(rotated[valueIndex]); err != nil {
			// 写回失败时停止，后续 State 错误边界会暴露异常。
			return
		}
	}
}

// nativeLuaAbsoluteStackIndex 把 native C API 栈索引转换为 Go State 的绝对栈槽。
func nativeLuaAbsoluteStackIndex(luaState unsafe.Pointer, state *runtime.State, index int) (int, bool) {
	// pseudo-index 不能作为普通栈槽写入；registry/upvalue 的写入语义后续随完整 API 扩展。
	if index <= runtime.RegistryPseudoIndex {
		// 当前 helper 只服务 lua_copy 的目标槽和 rotate 区间，不接受 pseudo-index。
		return 0, false
	}
	absoluteIndex := state.AbsIndex(index)
	if baseTop, ok := currentNativeStateCallBase(luaState); ok {
		if index > 0 {
			// C function 内正索引从当前调用帧参数区开始。
			// index 是 1-based 可见槽位，因此全局绝对索引需要加上调用进入前栈顶。
			absoluteIndex = baseTop + index
		} else if index < 0 && absoluteIndex <= baseTop {
			// C function 内负索引只能写当前调用帧可见栈槽，不能覆盖外层 Go VM 栈。
			return 0, false
		}
	}
	if absoluteIndex <= 0 || absoluteIndex > state.StackTop() {
		// 栈索引 0、越界正索引和越界负索引都不是可写目标槽。
		return 0, false
	}
	return absoluteIndex, true
}

// nativeLuaWriteStackValue 把值写回指定 C API 栈槽，并保持原栈顶不变。
func nativeLuaWriteStackValue(luaState unsafe.Pointer, state *runtime.State, index int, value runtime.Value) bool {
	// 写回前先校验 State 生命周期，避免在已关闭 VM 上重建栈段。
	if state == nil || state.IsClosed() {
		// 无效或已关闭 State 不能安全写回任何栈槽。
		return false
	}
	absoluteTarget, ok := nativeLuaAbsoluteStackIndex(luaState, state, index)
	if !ok {
		// 目标必须是当前 C API 可见的真实栈槽；pseudo-index 和越界索引保持 no-op。
		return false
	}
	top := state.StackTop()
	suffix := make([]runtime.Value, top-absoluteTarget)
	for valueIndex := range suffix {
		// 保存目标槽之后的后缀，避免重建栈段时改变后续值顺序。
		suffix[valueIndex] = state.ValueAt(absoluteTarget + 1 + valueIndex)
	}
	nativeLuaRestoreStackTop(state, absoluteTarget-1)
	if err := state.Push(value); err != nil {
		// 写回原有槽位理论上不会扩栈失败；失败时停止，调用方可按 no-op 处理。
		return false
	}
	for valueIndex := range suffix {
		// 重新压回后缀，保证该 helper 不改变栈顶和目标槽之外的内容。
		if err := state.Push(suffix[valueIndex]); err != nil {
			// 后缀恢复失败时停止，避免继续扩大状态损坏。
			return false
		}
	}
	return true
}

// nativeLuaCopy 按 Lua 5.3 C API 的 lua_copy 语义复制 fromidx 到 toidx。
func nativeLuaCopy(luaState unsafe.Pointer, fromIndex int, toIndex int) {
	// 入口先解析 State；失效 handle 不能修改任何 Go VM 状态。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// native 模块持有无效 State 时保持 no-op，避免破坏外层 VM。
		return
	}
	value, ok := nativeLuaValueAt(luaState, fromIndex)
	if !ok {
		// fromidx 必须是可读索引；当前最小 shim 对无效索引保持 no-op。
		return
	}
	if !nativeLuaWriteStackValue(luaState, state, toIndex, value) {
		// toidx 必须是当前栈上的有效槽位；pseudo-index 写入留到后续完整 API 阶段。
		return
	}
}

// nativeLuaPushValue 把一个运行期值压入 native C API 对应的 Go State 栈。
func nativeLuaPushValue(luaState unsafe.Pointer, value runtime.Value) {
	// 入口先解析 State，失效 handle 不能修改任何 Go VM 状态。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// native 模块持有无效 State 时保持 no-op，避免对已关闭 VM 产生副作用。
		return
	}
	_ = state.Push(value)
}

// nativeLuaPushValueAt 复制指定索引处的 Lua 值到栈顶。
func nativeLuaPushValueAt(luaState unsafe.Pointer, index int) {
	// 通过统一索引读取逻辑复制值，确保 C function 内正索引相对当前调用帧。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效索引在当前最小 shim 中保持 no-op，后续 api_check 阶段补齐错误边界。
		return
	}
	nativeLuaPushValue(luaState, value)
}

// nativeLuaPushNil 把 Lua nil 压入 native C API 对应的 Go State 栈。
func nativeLuaPushNil(luaState unsafe.Pointer) {
	// nil 没有负载，直接压入运行期 nil 值。
	nativeLuaPushValue(luaState, runtime.NilValue())
}

// nativeLuaPushBoolean 把 Lua boolean 压入 native C API 对应的 Go State 栈。
func nativeLuaPushBoolean(luaState unsafe.Pointer, value bool) {
	// boolean 值直接映射 Go bool 负载。
	nativeLuaPushValue(luaState, runtime.BooleanValue(value))
}

// nativeLuaPushInteger 把 Lua integer 压入 native C API 对应的 Go State 栈。
func nativeLuaPushInteger(luaState unsafe.Pointer, value int64) {
	// integer 值直接映射 Go int64 负载。
	nativeLuaPushValue(luaState, runtime.IntegerValue(value))
}

// nativeLuaPushNumber 把 Lua number 压入 native C API 对应的 Go State 栈。
func nativeLuaPushNumber(luaState unsafe.Pointer, value float64) {
	// number 值直接映射 Go float64 负载。
	nativeLuaPushValue(luaState, runtime.NumberValue(value))
}

// nativeLuaPushLString 把指定长度 C 字符串片段压入 native C API 对应的 Go State 栈。
func nativeLuaPushLString(luaState unsafe.Pointer, text unsafe.Pointer, length uintptr) unsafe.Pointer {
	// 指定长度字符串允许内嵌 NUL，必须按长度复制而不能按 NUL 结尾截断。
	value, ok := nativeLuaCStringN(text, length)
	if !ok {
		// 无效指针或长度暂不抛出 longjmp，保持 no-op 并返回 nil。
		return nil
	}
	buffer, _, ok := nativeLuaAllocCString(luaState, value)
	if !ok {
		// 无效 State 或分配失败时不能向 C 模块返回可用内部字符串指针。
		return nil
	}
	nativeLuaPushValue(luaState, runtime.StringValue(value))
	return buffer
}

// nativeLuaPushString 把 NUL 结尾 C 字符串压入 native C API 对应的 Go State 栈。
func nativeLuaPushString(luaState unsafe.Pointer, text unsafe.Pointer) unsafe.Pointer {
	// 官方语义中 NULL 会压入 nil，并返回 NULL。
	if text == nil {
		// NULL 字符串不表示空字符串，而是 Lua nil。
		nativeLuaPushNil(luaState)
		return nil
	}
	value := nativeLuaCString(text)
	buffer, _, ok := nativeLuaAllocCString(luaState, value)
	if !ok {
		// 无效 State 或分配失败时不能向 C 模块返回可用内部字符串指针。
		return nil
	}
	nativeLuaPushValue(luaState, runtime.StringValue(value))
	return buffer
}

// nativeLuaPushFormattedString 压入 C 层已经格式化完成的 Lua string。
func nativeLuaPushFormattedString(luaState unsafe.Pointer, text unsafe.Pointer) unsafe.Pointer {
	// C wrapper 负责处理 varargs 和格式化；Go 侧只复制最终 NUL 结尾字符串。
	value := ""
	if text != nil {
		// 非空格式化结果按 C 字符串复制为 Go-owned string。
		value = nativeLuaCString(text)
	}
	buffer, _, ok := nativeLuaAllocCString(luaState, value)
	if !ok {
		// 无效 State 或分配失败时不能向 C 模块返回可用指针。
		return nil
	}
	nativeLuaPushValue(luaState, runtime.StringValue(value))
	return buffer
}

// nativeLuaPushLightUserdata 把 C 裸指针作为 lightuserdata 压入栈顶。
func nativeLuaPushLightUserdata(luaState unsafe.Pointer, pointer unsafe.Pointer) {
	// 同一个 lua_State 内同一裸指针必须映射为稳定 Lua identity。
	value, ok := nativeLightUserdataValue(luaState, pointer)
	if !ok {
		// 无效 State 下保持 no-op。
		return
	}
	nativeLuaPushValue(luaState, value)
}

// nativeLuaCStringN 把 C 字符串片段复制成 Go 字符串。
func nativeLuaCStringN(text unsafe.Pointer, length uintptr) (string, bool) {
	// nil 指针只允许空长度字符串；非空长度说明 C 模块传入了无效指针。
	if text == nil {
		// 空指针加 0 长度按空字符串处理，便于支持 lua_pushlstring(L, NULL, 0) 的防御场景。
		return "", length == 0
	}
	if length > uintptr(nativeMaxInt) {
		// Go 字符串长度不能超过 int 上限，拒绝复制避免整数截断。
		return "", false
	}
	// C 模块传入的内存可能来自临时解析缓冲区，必须复制成 Go-owned string。
	return string(unsafe.Slice((*byte)(text), int(length))), true
}

// nativeLuaCString 把 NUL 结尾 C 字符串复制成 Go 字符串。
func nativeLuaCString(text unsafe.Pointer) string {
	// 调用方保证 text 非 nil；循环扫描到首个 NUL，保持 lua_pushstring 的 C 字符串语义。
	length := 0
	for *(*byte)(unsafe.Add(text, length)) != 0 {
		// 每次迭代只前进一个字节，直到 C 字符串终止符。
		length++
	}
	// C 字符串可能来自动态库临时内存或可复用缓冲区，返回前必须复制。
	return string(unsafe.Slice((*byte)(text), length))
}

// lua_gettop 导出 Lua 5.3 C API 栈顶查询入口。
//
//export lua_gettop
func lua_gettop(luaState *C.lua_State) C.int {
	// C API 入口只做 token 转发，实际生命周期校验集中在 Go helper。
	return C.int(nativeLuaStackTop(unsafe.Pointer(luaState)))
}

// lua_absindex 导出 Lua 5.3 C API 绝对索引规范化入口。
//
//export lua_absindex
func lua_absindex(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，具体 C frame 可见栈语义由 Go helper 维护。
	return C.int(nativeLuaAbsIndex(unsafe.Pointer(luaState), int(index)))
}

// lua_settop 导出 Lua 5.3 C API 栈顶调整入口。
//
//export lua_settop
func lua_settop(luaState *C.lua_State, index C.int) {
	// C API 入口只做类型转换，具体正负索引语义由 Go helper 统一维护。
	nativeLuaSetTop(unsafe.Pointer(luaState), int(index))
}

// lua_checkstack 导出 Lua 5.3 C API 栈空间检查入口。
//
//export lua_checkstack
func lua_checkstack(luaState *C.lua_State, extraSlots C.int) C.int {
	// 当前 Go 栈按需扩展；返回值只表示 State 生命周期和参数是否合法。
	if nativeLuaCheckStack(unsafe.Pointer(luaState), int(extraSlots)) {
		// 非 0 表示检查成功。
		return 1
	}
	return 0
}

// lua_rotate 导出 Lua 5.3 C API 栈区间旋转入口。
//
//export lua_rotate
func lua_rotate(luaState *C.lua_State, index C.int, rotateCount C.int) {
	// C API 入口只做类型转换，具体区间读写由 Go helper 维护。
	nativeLuaRotate(unsafe.Pointer(luaState), int(index), int(rotateCount))
}

// lua_copy 导出 Lua 5.3 C API 栈槽复制入口。
//
//export lua_copy
func lua_copy(luaState *C.lua_State, fromIndex C.int, toIndex C.int) {
	// C API 入口只做类型转换，具体索引解析和不变栈顶语义由 Go helper 统一维护。
	nativeLuaCopy(unsafe.Pointer(luaState), int(fromIndex), int(toIndex))
}

// lua_pushvalue 导出 Lua 5.3 C API 值复制入口。
//
//export lua_pushvalue
func lua_pushvalue(luaState *C.lua_State, index C.int) {
	// C API 入口只做类型转换，具体索引解析和压栈语义由 Go helper 统一维护。
	nativeLuaPushValueAt(unsafe.Pointer(luaState), int(index))
}

// lua_pushnil 导出 Lua 5.3 C API nil 压栈入口。
//
//export lua_pushnil
func lua_pushnil(luaState *C.lua_State) {
	// C API 入口只做类型转换，具体压栈语义由 Go helper 统一维护。
	nativeLuaPushNil(unsafe.Pointer(luaState))
}

// lua_pushboolean 导出 Lua 5.3 C API boolean 压栈入口。
//
//export lua_pushboolean
func lua_pushboolean(luaState *C.lua_State, value C.int) {
	// Lua C API 中 0 为 false，非 0 为 true。
	nativeLuaPushBoolean(unsafe.Pointer(luaState), value != 0)
}

// lua_pushinteger 导出 Lua 5.3 C API integer 压栈入口。
//
//export lua_pushinteger
func lua_pushinteger(luaState *C.lua_State, value C.lua_Integer) {
	// C API 入口只做类型转换，具体 integer 压栈语义由 Go helper 统一维护。
	nativeLuaPushInteger(unsafe.Pointer(luaState), int64(value))
}

// lua_pushnumber 导出 Lua 5.3 C API number 压栈入口。
//
//export lua_pushnumber
func lua_pushnumber(luaState *C.lua_State, value C.lua_Number) {
	// C API 入口只做类型转换，具体 number 压栈语义由 Go helper 统一维护。
	nativeLuaPushNumber(unsafe.Pointer(luaState), float64(value))
}

// lua_pushlstring 导出 Lua 5.3 C API 指定长度字符串压栈入口。
//
//export lua_pushlstring
func lua_pushlstring(luaState *C.lua_State, text *C.char, length C.size_t) *C.char {
	// C API 入口只做类型转换，具体字符串复制语义由 Go helper 统一维护。
	return (*C.char)(nativeLuaPushLString(unsafe.Pointer(luaState), unsafe.Pointer(text), uintptr(length)))
}

// lua_pushstring 导出 Lua 5.3 C API NUL 结尾字符串压栈入口。
//
//export lua_pushstring
func lua_pushstring(luaState *C.lua_State, text *C.char) *C.char {
	// C API 入口只做类型转换，具体 NUL 结尾字符串语义由 Go helper 统一维护。
	return (*C.char)(nativeLuaPushString(unsafe.Pointer(luaState), unsafe.Pointer(text)))
}

// glua_lua_pushfstring_message 接收 C wrapper 已经格式化完成的 lua_pushfstring 文本。
//
//export glua_lua_pushfstring_message
func glua_lua_pushfstring_message(luaState *C.lua_State, text *C.char) *C.char {
	// 函数名加 glua 前缀避免与 variadic lua_pushfstring ABI 冲突。
	return (*C.char)(nativeLuaPushFormattedString(unsafe.Pointer(luaState), unsafe.Pointer(text)))
}

// lua_pushlightuserdata 导出 Lua 5.3 C API lightuserdata 压栈入口。
//
//export lua_pushlightuserdata
func lua_pushlightuserdata(luaState *C.lua_State, pointer unsafe.Pointer) {
	// C API 入口只做类型转换，具体 identity 映射由 Go helper 维护。
	nativeLuaPushLightUserdata(unsafe.Pointer(luaState), pointer)
}
