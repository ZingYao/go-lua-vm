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

// nativeLuaMetatableFromTop 从栈顶读取 lua_setmetatable 使用的新元表。
func nativeLuaMetatableFromTop(luaState unsafe.Pointer) (*runtime.Table, bool) {
	// 栈顶必须存在；nil 表示移除元表，table 表示写入新元表。
	value, ok := nativeLuaValueAt(luaState, -1)
	if !ok {
		// 缺少栈顶值时无法执行 setmetatable。
		return nil, false
	}
	if value.IsNil() {
		// nil metatable 是合法输入，用于清除已有元表。
		return nil, true
	}
	if value.Kind != runtime.KindTable {
		// 当前 C API shim 不接受非 table/nil 元表，保持失败安全。
		return nil, false
	}
	table, ok := value.Ref.(*runtime.Table)
	if !ok || table == nil {
		// table 引用损坏时拒绝写入。
		return nil, false
	}
	return table, true
}

// nativeLuaMetatableForValue 读取值的 raw 元表。
func nativeLuaMetatableForValue(value runtime.Value) *runtime.Table {
	switch value.Kind {
	case runtime.KindTable:
		// table 使用对象级 raw 元表。
		table, ok := value.Ref.(*runtime.Table)
		if !ok || table == nil {
			// 损坏 table 引用视为无元表。
			return nil
		}
		return table.GetMetatable()
	case runtime.KindUserdata:
		// userdata 使用对象级 raw 元表，供 C module 方法表绑定使用。
		userdata, ok := value.Ref.(*runtime.Userdata)
		if !ok || userdata == nil {
			// 损坏 userdata 引用视为无元表。
			return nil
		}
		return userdata.GetMetatable()
	default:
		// Lua C API 允许基础类型拥有类型级元表；复用 runtime 的共享元表槽。
		return runtime.BasicTypeMetatable(value)
	}
}

// nativeLuaSetMetatable 实现 Lua 5.3 C API 的 lua_setmetatable。
func nativeLuaSetMetatable(luaState unsafe.Pointer, index int) int {
	// 入口先解析 State；失败时不能弹栈，避免破坏外层 VM 状态。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 无法设置元表。
		return 0
	}
	metatable, ok := nativeLuaMetatableFromTop(luaState)
	if !ok {
		// 栈顶不是 table/nil 时保持 no-op，后续错误边界再补齐 api_check 语义。
		return 0
	}
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效目标索引不能设置元表。
		return 0
	}

	switch value.Kind {
	case runtime.KindTable:
		// table 目标写入对象级 raw 元表。
		table, ok := value.Ref.(*runtime.Table)
		if !ok || table == nil {
			// 损坏 table 引用不执行弹栈。
			return 0
		}
		table.SetMetatable(metatable)
	case runtime.KindUserdata:
		// userdata 目标写入对象级 raw 元表。
		userdata, ok := value.Ref.(*runtime.Userdata)
		if !ok || userdata == nil {
			// 损坏 userdata 引用不执行弹栈。
			return 0
		}
		if err := userdata.SetMetatable(metatable); err != nil {
			// SetMetatable 只会在 nil userdata 上失败，这里保持 C API 失败返回。
			return 0
		}
	default:
		// 基础类型按 Lua C API 使用类型级 raw 元表；不支持的引用类型返回失败。
		if !runtime.SetBasicTypeMetatable(value, metatable) {
			// function/thread 等当前没有共享元表槽。
			return 0
		}
	}
	if _, err := state.Pop(); err != nil {
		// 成功设置后弹出新元表；弹栈失败说明 State 已异常，按失败返回。
		return 0
	}
	return 1
}

// nativeLuaGetMetatable 实现 Lua 5.3 C API 的 lua_getmetatable。
func nativeLuaGetMetatable(luaState unsafe.Pointer, index int) int {
	// 通过统一 helper 读取 C API 视角下的目标值。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效目标索引没有元表。
		return 0
	}
	metatable := nativeLuaMetatableForValue(value)
	if metatable == nil {
		// 无 raw 元表时不压栈并返回 0。
		return 0
	}
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindTable, metatable))
	return 1
}

// lua_setmetatable 导出 Lua 5.3 C API raw 元表写入入口。
//
//export lua_setmetatable
func lua_setmetatable(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，具体弹栈和目标分派由 Go helper 维护。
	return C.int(nativeLuaSetMetatable(unsafe.Pointer(luaState), int(index)))
}

// lua_getmetatable 导出 Lua 5.3 C API raw 元表读取入口。
//
//export lua_getmetatable
func lua_getmetatable(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，返回 1 表示已把元表压栈，0 表示无元表。
	return C.int(nativeLuaGetMetatable(unsafe.Pointer(luaState), int(index)))
}
