//go:build native_modules

package native

/*
 #include <stddef.h>
 #include <stdlib.h>
 #include <string.h>

typedef struct lua_State lua_State;
typedef long long lua_Integer;

static char* glua_alloc_native_lstring(const void* data, size_t length) {
	if (length > 0 && data == NULL) {
		return NULL;
	}
	char* buffer = (char*)malloc(length + 1);
	if (buffer == NULL) {
		return NULL;
	}
	if (length > 0) {
		memcpy(buffer, data, length);
	}
	buffer[length] = '\0';
	return buffer;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// nativeLuaToInteger 按当前最小 Lua C API shim 读取 integer。
func nativeLuaToInteger(luaState unsafe.Pointer, index int) (int64, bool) {
	// 入口通过统一 helper 区分 none 与 nil，并处理 C function 当前调用帧基址。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效索引或无效 State 返回转换失败，避免 C 模块误读旧栈。
		return 0, false
	}
	integerValue, ok := value.ToInteger()
	if !ok {
		// 当前阶段只覆盖 runtime.Value 的 number/integer 转换；字符串转数字留到完整 C API 兼容阶段。
		return 0, false
	}
	return integerValue, true
}

// nativeLuaCheckInteger 实现 luaL_checkinteger 的临时最小边界。
func nativeLuaCheckInteger(luaState unsafe.Pointer, index int) int64 {
	// 先复用基础 integer 转换；失败时暂不 longjmp，后续 luaL_error 阶段补齐。
	integerValue, ok := nativeLuaToInteger(luaState, index)
	if !ok {
		// luaL_error 尚未实现前返回 0，测试和 TODO 会明确这是临时边界。
		return 0
	}
	return integerValue
}

// nativeLuaArgError 记录 lauxlib 参数错误，并返回 Lua 5.3 API 约定的不可达返回值。
func nativeLuaArgError(luaState unsafe.Pointer, index int, extra unsafe.Pointer) int {
	// 当前 shim 不跨 Go/C 边界 longjmp，先把错误对象挂到 State，等待 C function 返回边界传播。
	message := fmt.Sprintf("bad argument #%d", index)
	if extra != nil {
		// extra 是 lauxlib 调用方提供的补充原因。
		message = fmt.Sprintf("%s (%s)", message, nativeLuaCString(extra))
	}
	_ = setNativeStatePendingError(luaState, runtime.StringValue(message))
	return 0
}

// nativeLuaOptionAt 读取以 NULL 结尾的 C 字符串选项数组。
func nativeLuaOptionAt(options unsafe.Pointer, index int) unsafe.Pointer {
	// lauxlib 选项表是 const char *const []，当前 helper 只做只读指针扫描。
	if options == nil || index < 0 {
		// 缺失数组或非法下标表示没有可读选项。
		return nil
	}
	pointerSize := unsafe.Sizeof(uintptr(0))
	return *(*unsafe.Pointer)(unsafe.Add(options, uintptr(index)*pointerSize))
}

// nativeLuaCheckOption 实现 luaL_checkoption 的字符串匹配语义。
func nativeLuaCheckOption(luaState unsafe.Pointer, index int, defaultValue unsafe.Pointer, options unsafe.Pointer) int {
	// 参数缺失或为 nil 时，只有提供默认值才可继续匹配。
	var option string
	value, ok := nativeLuaValueAt(luaState, index)
	if (!ok || value.IsNil()) && defaultValue != nil {
		// 默认值由调用方保证来自静态 C 字符串。
		option = nativeLuaCString(defaultValue)
	} else {
		// 非默认路径要求参数能转换为 Lua string。
		buffer, _, converted := nativeLuaToLString(luaState, index)
		if !converted {
			// 不能转换为字符串时记录参数错误。
			message := fmt.Sprintf("bad argument #%d (string expected)", index)
			_ = setNativeStatePendingError(luaState, runtime.StringValue(message))
			return 0
		}
		option = nativeLuaCString(buffer)
	}
	for optionIndex := 0; ; optionIndex++ {
		// 选项数组以 NULL 终止，逐项做完全匹配。
		optionPointer := nativeLuaOptionAt(options, optionIndex)
		if optionPointer == nil {
			// 没有匹配项时记录 invalid option 错误。
			message := fmt.Sprintf("invalid option '%s'", option)
			_ = setNativeStatePendingError(luaState, runtime.StringValue(message))
			return 0
		}
		if option == nativeLuaCString(optionPointer) {
			// 返回匹配选项的 0-based 下标，符合 lauxlib 语义。
			return optionIndex
		}
	}
}

// nativeLuaAllocCString 为 Lua C API 返回值分配 C 可见字符串。
func nativeLuaAllocCString(luaState unsafe.Pointer, text string) (unsafe.Pointer, uintptr, bool) {
	// 空字符串也必须分配一个包含 NUL 结尾的 C buffer，保证返回指针非 nil。
	var data unsafe.Pointer
	if len(text) > 0 {
		// 非空字符串传入 Go 字符串只读内存，C helper 会立即复制，不保留 Go 指针。
		data = unsafe.Pointer(unsafe.StringData(text))
	}
	buffer := C.glua_alloc_native_lstring(data, C.size_t(len(text)))
	if buffer == nil {
		// C 分配失败时不能返回可用字符串。
		return nil, 0, false
	}
	if !rememberNativeStateBuffer(luaState, unsafe.Pointer(buffer)) {
		// handle 无效时 rememberNativeStateBuffer 已释放 buffer。
		return nil, 0, false
	}
	return unsafe.Pointer(buffer), uintptr(len(text)), true
}

// nativeLuaToLString 按当前最小 Lua C API shim 读取字符串。
func nativeLuaToLString(luaState unsafe.Pointer, index int) (unsafe.Pointer, uintptr, bool) {
	// 入口通过统一 helper 区分 none 与 nil，并处理 C function 当前调用帧基址。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效索引或 nil 不能按 string 转换。
		return nil, 0, false
	}
	switch value.Kind {
	case runtime.KindString:
		// Lua string 可直接复制到 C buffer。
		return nativeLuaAllocCString(luaState, value.String)
	case runtime.KindInteger, runtime.KindNumber:
		// number-to-string 使用 runtime 已有 Lua 5.3 基础格式；当前不回写栈槽，后续完整 lua_tolstring 语义补齐。
		text, ok := value.NumberToString()
		if !ok {
			// 理论上 number 类型均可格式化；失败时按转换失败处理。
			return nil, 0, false
		}
		return nativeLuaAllocCString(luaState, text)
	default:
		// 其他类型不能按最小字符串 API 转换。
		return nil, 0, false
	}
}

// nativeLuaCheckLString 实现 luaL_checklstring 的临时最小边界。
func nativeLuaCheckLString(luaState unsafe.Pointer, index int) (unsafe.Pointer, uintptr, bool) {
	// 先复用基础字符串转换；失败时暂不 longjmp，后续 luaL_error 阶段补齐。
	return nativeLuaToLString(luaState, index)
}

// lua_tointegerx 导出 Lua 5.3 C API integer 转换入口。
//
//export lua_tointegerx
func lua_tointegerx(luaState *C.lua_State, index C.int, isNumber *C.int) C.lua_Integer {
	// C API 入口只做类型转换，具体栈读取和转换语义由 Go helper 维护。
	integerValue, ok := nativeLuaToInteger(unsafe.Pointer(luaState), int(index))
	if isNumber != nil {
		// isnum 非空时必须明确写入转换是否成功。
		if ok {
			// 非 0 表示转换成功。
			*isNumber = 1
		} else {
			// 0 表示转换失败。
			*isNumber = 0
		}
	}
	return C.lua_Integer(integerValue)
}

// luaL_checkinteger 导出 Lua 5.3 lauxlib integer 参数检查入口。
//
//export luaL_checkinteger
func luaL_checkinteger(luaState *C.lua_State, index C.int) C.lua_Integer {
	// 当前阶段只返回转换结果；失败错误会在 luaL_error/longjmp 阶段接入。
	return C.lua_Integer(nativeLuaCheckInteger(unsafe.Pointer(luaState), int(index)))
}

// glua_luaL_argerror_record 记录 Lua 5.3 lauxlib 参数错误。
//
//export glua_luaL_argerror_record
func glua_luaL_argerror_record(luaState *C.lua_State, index C.int, extra *C.char) C.int {
	// C wrapper 会在记录后 longjmp 回当前 C function 调用入口。
	return C.int(nativeLuaArgError(unsafe.Pointer(luaState), int(index), unsafe.Pointer(extra)))
}

// luaL_checkoption 导出 Lua 5.3 lauxlib 选项检查入口。
//
//export luaL_checkoption
func luaL_checkoption(luaState *C.lua_State, index C.int, defaultValue *C.char, options **C.char) C.int {
	// C API 入口只做类型转换；具体默认值、字符串转换和匹配语义由 Go helper 维护。
	return C.int(nativeLuaCheckOption(unsafe.Pointer(luaState), int(index), unsafe.Pointer(defaultValue), unsafe.Pointer(options)))
}

// luaL_checkstack 导出 Lua 5.3 lauxlib 栈检查入口。
//
//export luaL_checkstack
func luaL_checkstack(luaState *C.lua_State, size C.int, message *C.char) {
	// lauxlib 失败时应抛出错误；当前 shim 用 pending error 延迟到 Go 边界传播。
	if nativeLuaCheckStack(unsafe.Pointer(luaState), int(size)) {
		// 栈可扩展时没有可见副作用。
		return
	}
	errorText := "stack overflow"
	if message != nil {
		// 调用方提供 message 时作为错误补充。
		errorText = fmt.Sprintf("%s (%s)", errorText, nativeLuaCString(unsafe.Pointer(message)))
	}
	_ = setNativeStatePendingError(unsafe.Pointer(luaState), runtime.StringValue(errorText))
}

// lua_tolstring 导出 Lua 5.3 C API 字符串转换入口。
//
//export lua_tolstring
func lua_tolstring(luaState *C.lua_State, index C.int, length *C.size_t) *C.char {
	// C API 入口只做类型转换，具体字符串复制和生命周期由 Go helper 统一维护。
	buffer, bufferLength, ok := nativeLuaToLString(unsafe.Pointer(luaState), int(index))
	if length != nil {
		// length 非空时必须写入返回字符串长度或失败长度 0。
		if ok {
			// 成功时返回字节长度，允许内嵌 NUL。
			*length = C.size_t(bufferLength)
		} else {
			// 失败时长度为 0。
			*length = 0
		}
	}
	if !ok {
		// 当前阶段转换失败返回 NULL；错误 longjmp 后续由 luaL_error 补齐。
		return nil
	}
	return (*C.char)(buffer)
}

// luaL_checklstring 导出 Lua 5.3 lauxlib 字符串参数检查入口。
//
//export luaL_checklstring
func luaL_checklstring(luaState *C.lua_State, index C.int, length *C.size_t) *C.char {
	// 当前阶段只返回转换结果；失败错误会在 luaL_error/longjmp 阶段接入。
	buffer, bufferLength, ok := nativeLuaCheckLString(unsafe.Pointer(luaState), int(index))
	if length != nil {
		// length 非空时必须写入返回字符串长度或失败长度 0。
		if ok {
			// 成功时返回字节长度，允许内嵌 NUL。
			*length = C.size_t(bufferLength)
		} else {
			// 失败时长度为 0。
			*length = 0
		}
	}
	if !ok {
		// 当前阶段检查失败返回 NULL；错误 longjmp 后续由 luaL_error 补齐。
		return nil
	}
	return (*C.char)(buffer)
}
