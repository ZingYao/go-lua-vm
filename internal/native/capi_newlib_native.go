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

	"github.com/zing/go-lua-vm/runtime"
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

// nativeLuaLSetFuncs 把函数列表注册到 table，并按 Lua 5.3 语义复制 upvalue。
func nativeLuaLSetFuncs(luaState unsafe.Pointer, functions []nativeLuaLibraryFunction, upvalueCount int) bool {
	// luaL_setfuncs 要求 table 位于 upvalue 下方；注册结束后会弹出原始 upvalue。
	if upvalueCount < 0 {
		// 负数 upvalue 数量不是合法 Lua C API 调用，保持 no-op。
		return false
	}
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 不能注册任何函数。
		return false
	}
	baseTop := 0
	if currentBaseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内只能从当前调用帧可见栈读取 table 和 upvalue，不能穿透外层 Go VM 栈。
		baseTop = currentBaseTop
	}
	visibleTop := state.StackTop() - baseTop
	if visibleTop < 0 {
		// 调用帧基址损坏时保持 no-op，避免继续读写错误栈区间。
		return false
	}
	if upvalueCount > visibleTop {
		// 调用方没有提供足够 upvalue，保持栈不变便于定位错误 C 模块。
		return false
	}
	tableIndex := -(upvalueCount + 1)
	table, ok := nativeLuaTableAt(luaState, tableIndex)
	if !ok {
		// luaL_setfuncs 要求目标 table 在 upvalue 下方；当前错误 longjmp 尚未接入，失败时保持 no-op。
		return false
	}
	upvalues := make([]runtime.Value, upvalueCount)
	firstUpvalueIndex := baseTop + visibleTop - upvalueCount + 1
	for upvalueIndex := 0; upvalueIndex < upvalueCount; upvalueIndex++ {
		// 每个函数都需要捕获同一组 upvalue 副本，不能共享后续被弹出的栈槽。
		upvalues[upvalueIndex] = state.ValueAt(firstUpvalueIndex + upvalueIndex)
	}
	for functionIndex := range functions {
		// nil C 函数指针不注册，避免创建无法调用的 table 字段。
		if functions[functionIndex].function == nil {
			// 继续处理后续条目，便于部分损坏列表仍尽量注册有效函数。
			continue
		}
		for upvalueIndex := range upvalues {
			// lua_pushcclosure 会从栈顶弹出 nup 个值，因此这里先为当前函数压入捕获副本。
			nativeLuaPushValue(luaState, upvalues[upvalueIndex])
		}
		nativeLuaPushCClosure(luaState, functions[functionIndex].function, upvalueCount)
		value, ok := nativeLuaPopVisible(luaState, state)
		if !ok {
			// 压 closure 后无法弹出说明 State 已损坏，停止处理避免继续扩大副作用。
			return false
		}
		table.RawSetString(functions[functionIndex].name, value)
	}
	nativeLuaRestoreStackTop(state, baseTop+visibleTop-upvalueCount)
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
	// Lua 5.3 真实 luaL_newlib 宏会调用该符号；upvalue 复制语义由 Go helper 维护。
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
