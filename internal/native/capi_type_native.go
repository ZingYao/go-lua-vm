//go:build native_modules

package native

/*
typedef struct lua_State lua_State;
typedef double lua_Number;

static const char* glua_lua_typename(int type_code) {
	switch (type_code) {
	case -1:
		return "no value";
	case 0:
		return "nil";
	case 1:
		return "boolean";
	case 2:
		return "userdata";
	case 3:
		return "number";
	case 4:
		return "string";
	case 5:
		return "table";
	case 6:
		return "function";
	case 7:
		return "userdata";
	case 8:
		return "thread";
	default:
		return NULL;
	}
}
*/
import "C"

import (
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// nativeLuaValueAt 读取 C API shim 视角下的栈值并区分 none 与 nil。
func nativeLuaValueAt(luaState unsafe.Pointer, index int) (runtime.Value, bool) {
	// 入口先解析 State；失效 handle 不能读取任何 Go VM 栈值。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 按 Lua C API 越界读取处理为 LUA_TNONE。
		return runtime.NilValue(), false
	}
	if index <= runtime.RegistryPseudoIndex {
		// registry pseudo-index 与 C closure upvalue pseudo-index 都在该区间内。
		if index < runtime.RegistryPseudoIndex {
			// lua_upvalueindex(i) 定义为 LUA_REGISTRYINDEX - i，因此小于 registry 的索引读取当前 C closure upvalue。
			upvalueIndex := runtime.RegistryPseudoIndex - index
			return currentNativeStateCallUpvalue(luaState, upvalueIndex)
		}
		if index != runtime.RegistryPseudoIndex {
			// 其他未支持 pseudo-index 不能误判成 nil。
			return runtime.NilValue(), false
		}
		value := state.ValueAt(index)
		if value.IsNil() {
			// 关闭 State 已由 handle lookup 拦截；这里仍保留防御判断。
			return runtime.NilValue(), false
		}
		return value, true
	}
	absoluteIndex := state.AbsIndex(index)
	if index > 0 {
		// C function 内正索引从当前 C 调用帧参数区开始，而不是整个 Go State 栈底。
		if baseTop, ok := currentNativeStateCallBase(luaState); ok {
			// index 是 1-based 可见槽位，因此全局绝对索引需要加上调用进入前栈顶。
			absoluteIndex = baseTop + index
		}
	}
	if absoluteIndex <= 0 || absoluteIndex > state.StackTop() {
		// 栈索引 0、越界正索引和越界负索引都属于 LUA_TNONE。
		return runtime.NilValue(), false
	}
	return state.ValueAt(absoluteIndex), true
}

// nativeLuaType 读取指定索引的 Lua 5.3 C API 类型编号。
func nativeLuaType(luaState unsafe.Pointer, index int) int {
	// 先用可区分 none/nil 的 helper 读取值，再映射到 Lua C API 类型编号。
	value, ok := nativeLuaValueAt(luaState, index)
	return nativeLuaTypeCode(value, !ok)
}

// nativeLuaToBoolean 按 Lua 5.3 C API truthiness 读取 boolean 结果。
func nativeLuaToBoolean(luaState unsafe.Pointer, index int) bool {
	// Lua C API 中 none 与 nil 一样视为 false。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 越界索引不能读取到真值。
		return false
	}
	return value.Truthy()
}

// nativeLuaIsString 按 Lua 5.3 C API 判断值是否可作为 string 读取。
func nativeLuaIsString(luaState unsafe.Pointer, index int) bool {
	// lua_isstring 对 string 和 number 都返回 true，因为 lua_tolstring 可把 number 转成字符串。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效索引属于 none，不能作为字符串读取。
		return false
	}
	if value.Kind == runtime.KindString {
		// 原生 string 可直接作为字符串读取。
		return true
	}
	if value.Kind == runtime.KindInteger || value.Kind == runtime.KindNumber {
		// Lua 5.3 把 number 视为可转换字符串，因此 lua_isstring 返回 true。
		return true
	}
	return false
}

// nativeLuaToNumber 按当前最小 Lua C API shim 读取 number。
func nativeLuaToNumber(luaState unsafe.Pointer, index int) (float64, bool) {
	// 入口通过统一 helper 区分 none 与 nil；两者都不能转换为 number。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效索引返回转换失败。
		return 0, false
	}
	numberValue, ok := value.ToNumber()
	if !ok {
		// 当前阶段只覆盖 runtime.Value 的 number/integer 转换；字符串转数字留到完整 C API 阶段。
		return 0, false
	}
	return numberValue, true
}

// lua_type 导出 Lua 5.3 C API 类型查询入口。
//
//export lua_type
func lua_type(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，具体 none/nil 区分由 Go helper 统一维护。
	return C.int(nativeLuaType(unsafe.Pointer(luaState), int(index)))
}

// lua_typename 导出 Lua 5.3 C API 类型名称查询入口。
//
//export lua_typename
func lua_typename(luaState *C.lua_State, typeCode C.int) *C.char {
	// lua_typename 不依赖 State 内容；保留参数只为匹配 Lua 5.3 ABI。
	_ = luaState
	return (*C.char)(unsafe.Pointer(C.glua_lua_typename(typeCode)))
}

// lua_toboolean 导出 Lua 5.3 C API truthiness 查询入口。
//
//export lua_toboolean
func lua_toboolean(luaState *C.lua_State, index C.int) C.int {
	// Lua C API 使用 int 表示 boolean，0 为 false，非 0 为 true。
	if nativeLuaToBoolean(unsafe.Pointer(luaState), int(index)) {
		// 非 0 表示真值。
		return 1
	}
	return 0
}

// lua_isstring 导出 Lua 5.3 C API 字符串可转换性判断入口。
//
//export lua_isstring
func lua_isstring(luaState *C.lua_State, index C.int) C.int {
	// C API 使用 int 表示 boolean，string 与 number 都按 true 返回。
	if nativeLuaIsString(unsafe.Pointer(luaState), int(index)) {
		// 非 0 表示可按字符串读取。
		return 1
	}
	return 0
}

// lua_tonumberx 导出 Lua 5.3 C API number 转换入口。
//
//export lua_tonumberx
func lua_tonumberx(luaState *C.lua_State, index C.int, isNumber *C.int) C.lua_Number {
	// C API 入口只做类型转换，具体栈读取和转换语义由 Go helper 维护。
	numberValue, ok := nativeLuaToNumber(unsafe.Pointer(luaState), int(index))
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
	return C.lua_Number(numberValue)
}
