//go:build native_modules

package native

/*
typedef struct lua_State lua_State;
typedef long long lua_Integer;
*/
import "C"

import (
	"unsafe"
)

// nativeLuaToInteger 按当前最小 Lua C API shim 读取 integer。
func nativeLuaToInteger(luaState unsafe.Pointer, index int) (int64, bool) {
	// 入口先解析 State；失效 handle 不能读取任何栈值。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 返回转换失败，避免 C 模块误读旧栈。
		return 0, false
	}
	value := state.ValueAt(index)
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
