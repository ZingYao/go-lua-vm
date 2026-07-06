//go:build native_modules

package native

/*
typedef struct lua_State lua_State;
*/
import "C"

import (
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// nativeLuaError 实现 Lua C API 的 lua_error 错误对象记录。
func nativeLuaError(luaState unsafe.Pointer) int {
	// Lua C API 约定 lua_error 使用栈顶作为 error object；这里记录对象并由 Go 调用边界转换。
	object, ok := nativeLuaValueAt(luaState, -1)
	if !ok {
		// 栈顶缺失时仍抛出 nil error object，保持 Lua `error(nil)` 的可捕获语义。
		object = runtime.NilValue()
	}
	if !setNativeStatePendingError(luaState, object) {
		// 无法记录错误说明 State 已失效；C 侧返回 0，由 Go 边界的 State 校验暴露失败。
		return 0
	}
	return 0
}

// nativeLuaErrorMessage 记录 luaL_error 已经格式化后的错误文本。
func nativeLuaErrorMessage(luaState unsafe.Pointer, message unsafe.Pointer) int {
	// luaL_error 的格式化在 C wrapper 内完成，Go 侧只接收最终字符串作为 Lua error object。
	text := ""
	if message != nil {
		// 非空 C 字符串按 NUL 结尾读取，符合 luaL_error 格式化结果。
		text = nativeLuaCString(message)
	}
	if !setNativeStatePendingError(luaState, runtime.StringValue(text)) {
		// 无效 State 下无法回传错误对象，保持返回 0 让调用边界统一处理。
		return 0
	}
	return 0
}

// lua_error 导出 Lua 5.3 C API error 入口。
//
//export lua_error
func lua_error(luaState *C.lua_State) C.int {
	// C API 入口只做类型转换；真实错误传播在 nativeLuaCallCFunction 返回边界完成。
	return C.int(nativeLuaError(unsafe.Pointer(luaState)))
}

// glua_luaL_error_message 接收 C wrapper 格式化后的 luaL_error 文本。
//
//export glua_luaL_error_message
func glua_luaL_error_message(luaState *C.lua_State, message *C.char) C.int {
	// C API 入口只做类型转换；函数名加 glua 前缀避免与 variadic luaL_error ABI 冲突。
	return C.int(nativeLuaErrorMessage(unsafe.Pointer(luaState), unsafe.Pointer(message)))
}
