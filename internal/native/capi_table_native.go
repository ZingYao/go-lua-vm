//go:build native_modules

package native

/*
#include <stddef.h>

typedef struct lua_State lua_State;
typedef long long lua_Integer;
*/
import "C"

import (
	"unsafe"

	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	nativeLuaTypeNone     = -1
	nativeLuaTypeNil      = 0
	nativeLuaTypeBoolean  = 1
	nativeLuaTypeLightUD  = 2
	nativeLuaTypeNumber   = 3
	nativeLuaTypeString   = 4
	nativeLuaTypeTable    = 5
	nativeLuaTypeFunction = 6
	nativeLuaTypeUserdata = 7
	nativeLuaTypeThread   = 8
)

const (
	nativeLuaNoRef        = -2
	nativeLuaRefNil       = -1
	nativeLuaRefFreeIndex = 0
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
		// lightuserdata 与 full userdata 在 Lua C API 中使用不同 type code。
		if userdata, ok := value.Ref.(*runtime.Userdata); ok && userdata != nil {
			// nativeLightUserdata 由 lua_pushlightuserdata 创建，必须报告为 LUA_TLIGHTUSERDATA。
			if _, ok := userdata.Data.(nativeLightUserdata); ok {
				return nativeLuaTypeLightUD
			}
		}
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
func nativeLuaTableAt(luaState unsafe.Pointer, index int) (*runtime.Table, bool) {
	// 通过统一 C API 视角取值，确保正索引遵守当前 C function 调用帧基址。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效索引不能用于字段读写。
		return nil, false
	}
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
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标暂不触发元方法或错误，保持栈不变便于定位语义缺口。
		return
	}
	value, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见待写入值时保持栈不变，避免穿透外层 VM 栈。
		return
	}
	table.RawSetString(nativeLuaCString(keyPointer), value)
}

// nativeLuaGetField 按 string key 读取 table 字段并压入栈顶。
func nativeLuaGetField(luaState unsafe.Pointer, index int, keyPointer unsafe.Pointer) int {
	// 入口先解析 State；失效 handle 无可读栈，返回 LUA_TNONE。
	_, ok := lookupNativeStateHandle(luaState)
	if !ok || keyPointer == nil {
		// 无效 State 或 key 暂不产生 Lua 值。
		return nativeLuaTypeNone
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标在当前最小 shim 中返回 none，后续元方法阶段会补完整错误语义。
		return nativeLuaTypeNone
	}
	value := table.RawGetString(nativeLuaCString(keyPointer))
	nativeLuaPushValue(luaState, value)
	return nativeLuaTypeCode(value, false)
}

// nativeLuaGetTable 按栈顶 key 读取 table 字段，弹出 key 后压入读取结果。
func nativeLuaGetTable(luaState unsafe.Pointer, index int) int {
	// 入口先解析 State；失效 handle 无可读栈，返回 LUA_TNONE。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 或关闭 State 下不能弹栈或压栈。
		return nativeLuaTypeNone
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标在当前最小 shim 中保持 no-op，后续元方法和错误阶段再补齐完整语义。
		return nativeLuaTypeNone
	}
	key, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见 key 时保持栈不变，避免穿透外层 VM 栈。
		return nativeLuaTypeNone
	}
	value, err := table.RawGet(key)
	if err != nil {
		// 不支持的 key 类型通过 pending error 传回 C function 调用边界。
		setNativeStatePendingError(luaState, runtime.StringValue(err.Error()))
		nativeLuaPushNil(luaState)
		return nativeLuaTypeNil
	}
	nativeLuaPushValue(luaState, value)
	return nativeLuaTypeCode(value, false)
}

// nativeLuaSetTable 按栈顶 key/value 执行 Lua 5.3 C API 的 lua_settable。
func nativeLuaSetTable(luaState unsafe.Pointer, index int) {
	// settable 需要先解析目标 table；index 可能是负数，必须在弹出 key/value 前定位。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 不能弹栈或写表。
		return
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标当前保持 no-op，后续 api_check/错误边界再补齐完整失败语义。
		return
	}
	if nativeLuaStackTop(luaState) < 2 {
		// settable 需要当前 C 帧可见栈顶 value 和其下方 key。
		return
	}
	value, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧缺少可见 value 时保持 no-op。
		return
	}
	key, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// key 缺失时无法写入；value 已按 C API 消费，等待后续 api_check 统一收口。
		return
	}
	if err := table.Set(key, value); err != nil {
		// key 非法或 __newindex 执行失败时通过 pending error 传回 C function 调用边界。
		setNativeStatePendingError(luaState, runtime.StringValue(err.Error()))
	}
}

// nativeLuaRawGetI 按 integer key raw 读取 table 字段并压入栈顶。
func nativeLuaRawGetI(luaState unsafe.Pointer, index int, key int64) int {
	// rawgeti 不触发元方法；目标必须是 table 或 registry pseudo-index。
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标在当前最小 shim 中返回 none，后续 api_check 阶段再补齐错误语义。
		return nativeLuaTypeNone
	}
	value := table.RawGetInteger(key)
	nativeLuaPushValue(luaState, value)
	return nativeLuaTypeCode(value, false)
}

// nativeLuaRawGet 按栈顶 key raw 读取 table 字段，弹出 key 后压入读取结果。
func nativeLuaRawGet(luaState unsafe.Pointer, index int) int {
	// rawget 不触发 __index 元方法；目标必须是 table 或 registry pseudo-index。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 或关闭 State 下不能弹栈或压栈。
		return nativeLuaTypeNone
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标保持 key 不被吞掉，后续 api_check 阶段再补齐错误语义。
		return nativeLuaTypeNone
	}
	key, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见 key 时保持栈不变，避免穿透外层 VM 栈。
		return nativeLuaTypeNone
	}
	value, err := table.RawGet(key)
	if err != nil {
		// 不支持的 key 类型通过 pending error 传回 C function 调用边界。
		setNativeStatePendingError(luaState, runtime.StringValue(err.Error()))
		nativeLuaPushNil(luaState)
		return nativeLuaTypeNil
	}
	nativeLuaPushValue(luaState, value)
	return nativeLuaTypeCode(value, false)
}

// nativeLuaRawSetI 按 integer key raw 写入 table 字段并弹出栈顶值。
func nativeLuaRawSetI(luaState unsafe.Pointer, index int, key int64) {
	// 入口先解析 State；失效 handle 不能弹栈或写表。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 保持 no-op。
		return
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标保持栈不变，避免提前吞掉 C 模块传入的值。
		return
	}
	value, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见待写入值时保持栈不变，避免穿透外层 VM 栈。
		return
	}
	table.RawSetInteger(key, value)
}

// nativeLuaRawSet 按栈顶 key/value raw 写入 table，并弹出 key 和 value。
func nativeLuaRawSet(luaState unsafe.Pointer, index int) {
	// rawset 不触发元方法；目标必须是 table 或 registry pseudo-index。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 不能弹栈或写表。
		return
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标保持栈不变，后续 api_check 阶段再补齐错误语义。
		return
	}
	if nativeLuaStackTop(luaState) < 2 {
		// rawset 需要当前 C 帧可见栈顶 value 与其下方 key。
		return
	}
	value, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见 value 时保持栈不变，避免穿透外层 VM 栈。
		return
	}
	key, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见 key 时无法写入；前置数量检查正常会避免进入该分支。
		return
	}
	if err := table.RawSet(key, value); err != nil {
		// nil/NaN key 在 Lua C API 中属于运行期错误；当前通过 pending error 回传 Go 边界。
		setNativeStatePendingError(luaState, runtime.StringValue(err.Error()))
	}
}

// nativeLuaRawLen 实现 Lua 5.3 C API 的 lua_rawlen。
func nativeLuaRawLen(luaState unsafe.Pointer, index int) uintptr {
	// rawlen 不触发 __len 元方法，只读取 string/table/full userdata 的原始长度。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok {
		// 无效索引按 Lua C API 返回 0，不产生错误。
		return 0
	}
	switch value.Kind {
	case runtime.KindString:
		// Lua string 长度按字节数返回，允许内嵌 NUL。
		return uintptr(len(value.String))
	case runtime.KindTable:
		// table raw length 使用 runtime.Table 的基础边界搜索，不触发 __len。
		table, ok := value.Ref.(*runtime.Table)
		if !ok || table == nil {
			// 损坏的 table 引用不能提供可靠长度。
			return 0
		}
		length := table.Len()
		if length < 0 {
			// 防御异常边界，size_t 不能表达负数。
			return 0
		}
		return uintptr(length)
	case runtime.KindUserdata:
		// Lua 5.3 full userdata rawlen 返回分配块大小；lightuserdata 和 Go userdata 返回 0。
		userdata, ok := value.Ref.(*runtime.Userdata)
		if !ok || userdata == nil {
			// 损坏的 userdata 引用不能暴露长度。
			return 0
		}
		block, ok := userdata.Data.(*nativeUserdataBlock)
		if !ok || block == nil {
			// lightuserdata 或非 native full userdata 没有 C 分配块大小。
			return 0
		}
		return block.size
	default:
		// 其他类型没有 raw length。
		return 0
	}
}

// nativeLuaNext 实现 Lua 5.3 C API 的 raw next 迭代。
func nativeLuaNext(luaState unsafe.Pointer, index int) int {
	// lua_next 会弹出栈顶当前 key，命中后压入下一组 key/value。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 没有可迭代表。
		return 0
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标在当前最小 shim 中按迭代结束处理。
		return 0
	}
	key, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见 key 时无法继续迭代，且不得弹掉外层 VM 栈。
		return 0
	}
	nextKey, nextValue, hasNext, err := table.RawPairsNext(key)
	if err != nil {
		// 迭代错误以 pending error 形式传回 C function 调用边界。
		setNativeStatePendingError(luaState, runtime.StringValue(err.Error()))
		return 0
	}
	if !hasNext {
		// 迭代结束时仅弹出当前 key，不压入新值。
		return 0
	}
	nativeLuaPushValue(luaState, nextKey)
	nativeLuaPushValue(luaState, nextValue)
	return 1
}

// nativeLuaLRef 在指定 table 中为栈顶值创建 Lua 5.3 lauxlib 引用。
func nativeLuaLRef(luaState unsafe.Pointer, index int) int {
	// luaL_ref 必须先解析 State 和目标 table；当前最小 shim 对非法 table 返回 LUA_NOREF。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 无法弹栈或保存引用。
		return nativeLuaNoRef
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标保持栈不变，后续 api_check 阶段再收口错误边界。
		return nativeLuaNoRef
	}
	value, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见待引用值时保持外层栈不变。
		return nativeLuaNoRef
	}
	if value.IsNil() {
		// Lua 5.3 规定 nil 引用不写入 table，直接返回 LUA_REFNIL。
		return nativeLuaRefNil
	}

	freeHead := table.RawGetInteger(nativeLuaRefFreeIndex)
	if freeHead.Kind == runtime.KindInteger && freeHead.Integer > 0 {
		// freelist 非空时复用链表头，并把 t[0] 更新为下一空闲引用。
		reference := freeHead.Integer
		nextFree := table.RawGetInteger(reference)
		table.RawSetInteger(nativeLuaRefFreeIndex, nextFree)
		table.RawSetInteger(reference, value)
		return int(reference)
	}

	// freelist 为空时按 Lua 5.3 luaL_ref 语义使用 raw length 后的下一个正整数槽。
	reference := table.Len() + 1
	table.RawSetInteger(reference, value)
	return int(reference)
}

// nativeLuaLUnref 释放指定 table 中的 Lua 5.3 lauxlib 引用。
func nativeLuaLUnref(luaState unsafe.Pointer, index int, reference int) {
	// LUA_NOREF 和 LUA_REFNIL 是预定义空引用，释放时必须保持 no-op。
	if reference < 0 {
		// 负数引用没有 table 副作用。
		return
	}
	table, ok := nativeLuaTableAt(luaState, index)
	if !ok {
		// 非 table 目标保持 no-op，后续 api_check 阶段再补错误语义。
		return
	}

	// 将 ref 节点插回 t[0] freelist 头部：t[ref] = oldHead; t[0] = ref。
	freeHead := table.RawGetInteger(nativeLuaRefFreeIndex)
	table.RawSetInteger(int64(reference), freeHead)
	table.RawSetInteger(nativeLuaRefFreeIndex, runtime.IntegerValue(int64(reference)))
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

// lua_gettable 导出 Lua 5.3 C API 栈顶 key 字段读取入口。
//
//export lua_gettable
func lua_gettable(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，返回值是 Lua 5.3 C API 类型编号。
	return C.int(nativeLuaGetTable(unsafe.Pointer(luaState), int(index)))
}

// lua_settable 导出 Lua 5.3 C API 栈顶 key/value 字段写入入口。
//
//export lua_settable
func lua_settable(luaState *C.lua_State, index C.int) {
	// C API 入口只做类型转换，具体 key/value 出栈和写入语义由 Go helper 维护。
	nativeLuaSetTable(unsafe.Pointer(luaState), int(index))
}

// lua_rawgeti 导出 Lua 5.3 C API integer key raw 读取入口。
//
//export lua_rawgeti
func lua_rawgeti(luaState *C.lua_State, index C.int, key C.lua_Integer) C.int {
	// C API 入口只做类型转换，返回值是 Lua 5.3 C API 类型编号。
	return C.int(nativeLuaRawGetI(unsafe.Pointer(luaState), int(index), int64(key)))
}

// lua_rawget 导出 Lua 5.3 C API raw key 字段读取入口。
//
//export lua_rawget
func lua_rawget(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，返回值是 Lua 5.3 C API 类型编号。
	return C.int(nativeLuaRawGet(unsafe.Pointer(luaState), int(index)))
}

// lua_rawseti 导出 Lua 5.3 C API integer key raw 写入入口。
//
//export lua_rawseti
func lua_rawseti(luaState *C.lua_State, index C.int, key C.lua_Integer) {
	// C API 入口只做类型转换，具体写入和弹栈语义由 Go helper 维护。
	nativeLuaRawSetI(unsafe.Pointer(luaState), int(index), int64(key))
}

// lua_rawset 导出 Lua 5.3 C API raw key/value 写入入口。
//
//export lua_rawset
func lua_rawset(luaState *C.lua_State, index C.int) {
	// C API 入口只做类型转换，具体写入和弹栈语义由 Go helper 维护。
	nativeLuaRawSet(unsafe.Pointer(luaState), int(index))
}

// lua_rawlen 导出 Lua 5.3 C API raw length 查询入口。
//
//export lua_rawlen
func lua_rawlen(luaState *C.lua_State, index C.int) C.size_t {
	// C API 入口只做类型转换，raw length 不触发元方法且不改动栈。
	return C.size_t(nativeLuaRawLen(unsafe.Pointer(luaState), int(index)))
}

// lua_next 导出 Lua 5.3 C API table 迭代入口。
//
//export lua_next
func lua_next(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，返回 0 表示迭代结束，非 0 表示压入 key/value。
	return C.int(nativeLuaNext(unsafe.Pointer(luaState), int(index)))
}

// luaL_ref 导出 Lua 5.3 lauxlib 引用创建入口。
//
//export luaL_ref
func luaL_ref(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换，具体 freelist 语义由 Go helper 维护。
	return C.int(nativeLuaLRef(unsafe.Pointer(luaState), int(index)))
}

// luaL_unref 导出 Lua 5.3 lauxlib 引用释放入口。
//
//export luaL_unref
func luaL_unref(luaState *C.lua_State, index C.int, reference C.int) {
	// C API 入口只做类型转换，具体 freelist 语义由 Go helper 维护。
	nativeLuaLUnref(unsafe.Pointer(luaState), int(index), int(reference))
}
