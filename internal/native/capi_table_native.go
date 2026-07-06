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

const (
	nativeLuaTypeNone     = -1
	nativeLuaTypeNil      = 0
	nativeLuaTypeBoolean  = 1
	nativeLuaTypeNumber   = 3
	nativeLuaTypeString   = 4
	nativeLuaTypeTable    = 5
	nativeLuaTypeFunction = 6
	nativeLuaTypeUserdata = 7
	nativeLuaTypeThread   = 8
)

// nativeLuaTypeCode 返回 Lua 5.3 C API 使用的基础类型编号。
func nativeLuaTypeCode(value runtime.Value, missing bool) int {
	// 越界索引与不可读取值在 C API 中用 LUA_TNONE 表示。
	if missing {
		// 调用方已确认该值来自无效索引，返回 none 而不是 nil。
		return nativeLuaTypeNone
	}
	switch value.Kind {
	case runtime.KindNil:
		// Lua nil 类型编号为 0。
		return nativeLuaTypeNil
	case runtime.KindBoolean:
		// Lua boolean 类型编号为 1。
		return nativeLuaTypeBoolean
	case runtime.KindInteger, runtime.KindNumber:
		// Lua 5.3 的 integer 和 float 都属于 number 基础类型。
		return nativeLuaTypeNumber
	case runtime.KindString:
		// Lua string 类型编号为 4。
		return nativeLuaTypeString
	case runtime.KindTable:
		// Lua table 类型编号为 5。
		return nativeLuaTypeTable
	case runtime.KindLuaClosure, runtime.KindGoClosure:
		// Lua/C/Go closure 对 C API 统一表现为 function。
		return nativeLuaTypeFunction
	case runtime.KindUserdata:
		// Lua full userdata 类型编号为 7。
		return nativeLuaTypeUserdata
	case runtime.KindThread:
		// Lua thread 类型编号为 8。
		return nativeLuaTypeThread
	default:
		// 未知内部类型按 none 处理，避免 C 模块误判为普通 nil。
		return nativeLuaTypeNone
	}
}

// nativeLuaTableAt 读取指定索引处的 table 引用。
func nativeLuaTableAt(state *runtime.State, index int) (*runtime.Table, bool) {
	// ValueAt 会处理正负索引和越界，非 table 值不能用于字段读写。
	value := state.ValueAt(index)
	if value.Kind != runtime.KindTable {
		// 只有 table 类型可进入当前最小字段 API；元方法和错误边界后续阶段补齐。
		return nil, false
	}
	table, ok := value.Ref.(*runtime.Table)
	if !ok || table == nil {
		// table 类型的引用负载不合法时拒绝继续操作。
		return nil, false
	}
	return table, true
}

// nativeLuaCreateTable 创建空 table 并压入当前 Go State 栈。
func nativeLuaCreateTable(luaState unsafe.Pointer, arraySize int, recordSize int) {
	// 当前 runtime 只公开普通 NewTable；array/hash 预留参数先保留接口语义，后续可优化容量。
	_, _ = arraySize, recordSize
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()))
}

// nativeLuaSetField 按 string key 设置 table 字段，并弹出栈顶 value。
func nativeLuaSetField(luaState unsafe.Pointer, index int, keyPointer unsafe.Pointer) {
	// 入口先解析 State；失效 handle 不能修改任何 Go VM 状态。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok || keyPointer == nil {
		// 无效 State 或 key 暂不抛出错误，后续 lua_error 边界补齐。
		return
	}
	absoluteIndex := state.AbsIndex(index)
	table, ok := nativeLuaTableAt(state, absoluteIndex)
	if !ok {
		// 非 table 目标暂不触发元方法或错误，保持栈不变便于定位语义缺口。
		return
	}
	value, err := state.Pop()
	if err != nil {
		// 空栈或关闭 State 时不能取得待写入值。
		return
	}
	table.RawSetString(nativeLuaCString(keyPointer), value)
}

// nativeLuaGetField 按 string key 读取 table 字段并压入栈顶。
func nativeLuaGetField(luaState unsafe.Pointer, index int, keyPointer unsafe.Pointer) int {
	// 入口先解析 State；失效 handle 无可读栈，返回 LUA_TNONE。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok || keyPointer == nil {
		// 无效 State 或 key 暂不产生 Lua 值。
		return nativeLuaTypeNone
	}
	table, ok := nativeLuaTableAt(state, index)
	if !ok {
		// 非 table 目标在当前最小 shim 中返回 none，后续元方法阶段会补完整错误语义。
		return nativeLuaTypeNone
	}
	value := table.RawGetString(nativeLuaCString(keyPointer))
	nativeLuaPushValue(luaState, value)
	return nativeLuaTypeCode(value, false)
}

// lua_createtable 导出 Lua 5.3 C API table 创建入口。
//
//export lua_createtable
func lua_createtable(luaState *C.lua_State, arraySize C.int, recordSize C.int) {
	// C API 入口只做类型转换，具体 table 创建由 Go helper 统一维护。
	nativeLuaCreateTable(unsafe.Pointer(luaState), int(arraySize), int(recordSize))
}

// lua_setfield 导出 Lua 5.3 C API string key 字段写入入口。
//
//export lua_setfield
func lua_setfield(luaState *C.lua_State, index C.int, key *C.char) {
	// C API 入口只做类型转换，具体字段写入和弹栈语义由 Go helper 统一维护。
	nativeLuaSetField(unsafe.Pointer(luaState), int(index), unsafe.Pointer(key))
}

// lua_getfield 导出 Lua 5.3 C API string key 字段读取入口。
//
//export lua_getfield
func lua_getfield(luaState *C.lua_State, index C.int, key *C.char) C.int {
	// C API 入口只做类型转换，返回值是 Lua 5.3 C API 类型编号。
	return C.int(nativeLuaGetField(unsafe.Pointer(luaState), int(index), unsafe.Pointer(key)))
}
