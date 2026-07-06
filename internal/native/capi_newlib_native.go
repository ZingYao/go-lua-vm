//go:build native_modules

package native

/*
#include <stddef.h>

typedef struct lua_State lua_State;
typedef double lua_Number;
typedef int (*lua_CFunction)(lua_State *L);

typedef struct luaL_Reg {
	const char *name;
	lua_CFunction func;
} luaL_Reg;

static const char* glua_luaL_reg_name(const luaL_Reg* list, int index) {
	return list[index].name;
}

static void* glua_luaL_reg_func(const luaL_Reg* list, int index) {
	return (void*)list[index].func;
}
*/
import "C"

import (
	"unsafe"
)

// nativeLuaLibraryFunction 保存 luaL_Reg 的 Go 侧只读快照。
type nativeLuaLibraryFunction struct {
	name     string
	function unsafe.Pointer
}

// nativeLuaLRegList 把 C 侧以 nil name 结尾的 luaL_Reg 数组复制为 Go 快照。
func nativeLuaLRegList(list *C.luaL_Reg) []nativeLuaLibraryFunction {
	// nil 列表没有可注册函数，按空库处理。
	if list == nil {
		// 返回 nil 切片即可，调用方会创建空表或保持 no-op。
		return nil
	}
	functions := make([]nativeLuaLibraryFunction, 0, 8)
	for index := 0; ; index++ {
		// luaL_Reg 以 name == NULL 作为终止符。
		namePointer := C.glua_luaL_reg_name(list, C.int(index))
		if namePointer == nil {
			// 遇到终止符后停止复制。
			break
		}
		functionPointer := C.glua_luaL_reg_func(list, C.int(index))
		functions = append(functions, nativeLuaLibraryFunction{
			name:     nativeLuaCString(unsafe.Pointer(namePointer)),
			function: functionPointer,
		})
	}
	return functions
}

// nativeLuaLSetFuncs 把函数列表注册到栈顶 table。
func nativeLuaLSetFuncs(luaState unsafe.Pointer, functions []nativeLuaLibraryFunction, upvalueCount int) bool {
	// 当前小切口只支持无 upvalue 注册；带 upvalue 的复制和弹栈语义留到 closure upvalue 阶段。
	if upvalueCount != 0 {
		// 保持 no-op，避免消耗调用方栈上的 upvalue 后产生错误可见性差异。
		return false
	}
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 不能注册任何函数。
		return false
	}
	table, ok := nativeLuaTableAt(luaState, -1)
	if !ok {
		// luaL_setfuncs 要求栈顶是目标 table；当前错误 longjmp 尚未接入，失败时保持 no-op。
		return false
	}
	for functionIndex := range functions {
		// nil C 函数指针不注册，避免创建无法调用的 table 字段。
		if functions[functionIndex].function == nil {
			// 继续处理后续条目，便于部分损坏列表仍尽量注册有效函数。
			continue
		}
		nativeLuaPushCClosure(luaState, functions[functionIndex].function, 0)
		value, err := state.Pop()
		if err != nil {
			// 压 closure 后无法弹出说明 State 已损坏，停止处理避免继续扩大副作用。
			return false
		}
		table.RawSetString(functions[functionIndex].name, value)
	}
	return true
}

// nativeLuaLNewLib 创建 table 并注册无 upvalue 函数列表。
func nativeLuaLNewLib(luaState unsafe.Pointer, functions []nativeLuaLibraryFunction) bool {
	// luaL_newlib 宏语义是创建预分配 table 后调用 luaL_setfuncs(L, l, 0)。
	nativeLuaCreateTable(luaState, 0, len(functions))
	if nativeLuaType(luaState, -1) != nativeLuaTypeTable {
		// table 未能压入说明 State 无效或栈写入失败。
		return false
	}
	return nativeLuaLSetFuncs(luaState, functions, 0)
}

// luaL_setfuncs 导出 Lua 5.3 lauxlib 函数表注册入口。
//
//export luaL_setfuncs
func luaL_setfuncs(luaState *C.lua_State, list *C.luaL_Reg, upvalueCount C.int) {
	// Lua 5.3 真实 luaL_newlib 宏会调用该符号；当前阶段只支持 nup==0。
	_ = nativeLuaLSetFuncs(unsafe.Pointer(luaState), nativeLuaLRegList(list), int(upvalueCount))
}

// luaL_newlib 导出兼容函数，覆盖非宏方式调用的模块。
//
//export luaL_newlib
func luaL_newlib(luaState *C.lua_State, list *C.luaL_Reg) {
	// 官方头文件中 luaL_newlib 是宏；这里额外导出函数符号，便于兼容手工声明入口。
	_ = nativeLuaLNewLib(unsafe.Pointer(luaState), nativeLuaLRegList(list))
}

// luaL_checkversion_ 导出 Lua 5.3 lauxlib 版本检查入口。
//
//export luaL_checkversion_
func luaL_checkversion_(luaState *C.lua_State, version C.lua_Number, sizes C.size_t) {
	// 当前 native shim 固定 Lua 5.3 public header 与 runtime 版本，先接受头文件宏触发的版本检查。
	_, _, _ = luaState, version, sizes
}
