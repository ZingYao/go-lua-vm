//go:build native_modules

package native

/*
#include <stdlib.h>

typedef struct lua_State lua_State;
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// nativeUserdataBlock 保存 Lua C full userdata 的 C 可见内存块。
type nativeUserdataBlock struct {
	// pointer 指向 C heap 上分配的 userdata 数据区，允许 C 模块直接读写。
	pointer unsafe.Pointer
	// size 记录 lua_newuserdata 请求的逻辑字节数，0 字节 userdata 仍会分配 1 字节哨兵。
	size uintptr
}

// newNativeUserdataBlock 分配一块由 Lua C 模块持有的 full userdata 内存。
func newNativeUserdataBlock(size uintptr) (*nativeUserdataBlock, bool) {
	// Lua 允许 0 字节 full userdata；C malloc(0) 结果不稳定，因此用 1 字节哨兵保持非 nil identity。
	allocationSize := size
	if allocationSize == 0 {
		// 0 字节 userdata 仍需要稳定地址，供 lua_touserdata 返回同一 identity。
		allocationSize = 1
	}
	pointer := C.calloc(1, C.size_t(allocationSize))
	if pointer == nil {
		// 分配失败时当前最小 shim 不 longjmp，调用方会返回 NULL 并保持栈不变。
		return nil, false
	}

	// C heap 指针只存放在 Go wrapper 中，不把 Go 指针暴露给 C 模块。
	return &nativeUserdataBlock{
		pointer: pointer,
		size:    size,
	}, true
}

// data 返回当前 userdata 数据区指针。
func (block *nativeUserdataBlock) data() unsafe.Pointer {
	if block == nil {
		// nil block 表示 userdata 构造路径异常，C API 视为不可转换。
		return nil
	}

	// 返回 C heap 指针，调用方不得在 State.Close 后继续使用。
	return block.pointer
}

// release 释放 C heap 上的 userdata 内存。
func (block *nativeUserdataBlock) release() {
	if block == nil || block.pointer == nil {
		// nil 或已释放 block 保持幂等。
		return
	}

	// C 内存由 native shim 分配，因此也必须由 native shim 释放。
	C.free(block.pointer)
	block.pointer = nil
}

// nativeLuaUserdataFinalizer 在 State.Close 阶段释放 native full userdata。
func nativeLuaUserdataFinalizer(payload any) error {
	// payload 必须来自 newNativeUserdataBlock；其他来源忽略以保持关闭路径稳健。
	block, ok := payload.(*nativeUserdataBlock)
	if !ok {
		// 非 native block 不是本 shim 的所有权范围。
		return nil
	}
	block.release()
	return nil
}

// nativeLuaNewUserdata 实现 Lua 5.3 C API 的 lua_newuserdata。
func nativeLuaNewUserdata(luaState unsafe.Pointer, size uintptr) unsafe.Pointer {
	// 先解析 opaque State，失效 State 不能分配或压栈。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 返回 NULL，当前阶段不跨 C 边界抛出错误。
		return nil
	}
	block, ok := newNativeUserdataBlock(size)
	if !ok {
		// 分配失败保持栈不变，后续错误阶段再补齐 longjmp 语义。
		return nil
	}

	// native full userdata 通过 runtime.Userdata 承载，并在 State.Close 时释放 C 内存。
	userdata := runtime.NewUserdataWithFinalizer(block, nativeLuaUserdataFinalizer)
	if err := state.RegisterUserdata(userdata); err != nil {
		// 注册失败说明 State 生命周期不可用，必须立即释放刚分配的 C 内存。
		block.release()
		return nil
	}
	if err := state.Push(userdata.Value()); err != nil {
		// 压栈失败时返回 NULL；userdata 已注册，会在 State.Close 中释放，避免无主泄漏。
		return nil
	}

	// 返回 C 模块可读写的数据区地址，栈顶保留对应 full userdata 对象。
	return block.data()
}

// nativeLuaToUserdata 实现 Lua 5.3 C API 的 lua_touserdata。
func nativeLuaToUserdata(luaState unsafe.Pointer, index int) unsafe.Pointer {
	// 通过统一 helper 读取 C API 视角下的索引，none/nil 都不可转换为 userdata。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok || value.Kind != runtime.KindUserdata {
		// 非 userdata 返回 NULL，与 Lua C API 转换失败语义一致。
		return nil
	}
	userdata, ok := value.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// 损坏的 userdata 引用不向 C 模块暴露。
		return nil
	}
	if light, ok := userdata.Data.(nativeLightUserdata); ok {
		// lightuserdata 按 Lua C API 直接返回原始裸指针数值；NULL 指针也允许返回 nil。
		return unsafe.Pointer(light.pointer)
	}
	block, ok := userdata.Data.(*nativeUserdataBlock)
	if !ok {
		// 当前 shim 只把 native 创建的 full userdata 和 lightuserdata 暴露为 C 指针；纯 Go userdata 不可转换。
		return nil
	}

	// 返回创建时分配的同一 C heap 指针。
	return block.data()
}

// nativeLuaGetUserValue 实现 Lua 5.3 C API 的 lua_getuservalue。
func nativeLuaGetUserValue(luaState unsafe.Pointer, index int) int {
	// 通过统一 helper 读取 C API 视角下的索引，确保 C function 内正索引按调用帧解释。
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok || value.Kind != runtime.KindUserdata {
		// 无效索引或非 userdata 没有 user value；Lua 5.3 C API 以压入 nil 表示未命中。
		nativeLuaPushNil(luaState)
		return nativeLuaTypeNil
	}
	userdata, ok := value.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// 损坏的 userdata 引用不向 C 模块暴露内部状态，回退为 nil。
		nativeLuaPushNil(luaState)
		return nativeLuaTypeNil
	}
	if _, ok := userdata.Data.(nativeLightUserdata); ok {
		// lightuserdata 没有 Lua 5.3 full userdata 的 user value 槽。
		nativeLuaPushNil(luaState)
		return nativeLuaTypeNil
	}

	// full userdata 的 user value 零值就是 Lua nil；保持 runtime.Value 原样压栈。
	userValue := userdata.UserValue
	nativeLuaPushValue(luaState, userValue)
	return nativeLuaTypeCode(userValue, false)
}

// nativeLuaSetUserValue 实现 Lua 5.3 C API 的 lua_setuservalue。
func nativeLuaSetUserValue(luaState unsafe.Pointer, index int) int {
	// 先解析 State 与目标 userdata；只有 full userdata 才能保存 user value。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 不能弹栈或写入，按失败返回。
		return 0
	}
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok || value.Kind != runtime.KindUserdata {
		// 无效索引或非 userdata 不应消费栈顶 user value。
		return 0
	}
	userdata, ok := value.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// 损坏的 userdata 引用不能保存 user value。
		return 0
	}
	if _, ok := userdata.Data.(nativeLightUserdata); ok {
		// lightuserdata 没有 Lua 5.3 full userdata 的 user value 槽。
		return 0
	}
	if _, ok := userdata.Data.(*nativeUserdataBlock); !ok {
		// 当前 shim 只允许 native full userdata 通过 C API 写入 user value。
		return 0
	}
	userValue, ok := nativeLuaPopVisible(luaState, state)
	if !ok {
		// 当前 C 帧没有可见 user value 时不能完成写入，且不得弹掉外层 VM 栈。
		return 0
	}
	userdata.UserValue = userValue
	return 1
}

// nativeLuaCheckUDataFailure 记录 luaL_checkudata 检查失败的错误对象。
func nativeLuaCheckUDataFailure(luaState unsafe.Pointer, index int, typeName string) unsafe.Pointer {
	// 当前 shim 不跨 Go/C 边界 longjmp；先记录错误对象，等待 C function 返回边界统一传播。
	message := fmt.Sprintf("bad argument #%d (%s expected)", index, typeName)
	_ = setNativeStatePendingError(luaState, runtime.StringValue(message))
	return nil
}

// nativeLuaNamedFullUserdata 查找与 registry 命名元表匹配的 native full userdata。
func nativeLuaNamedFullUserdata(luaState unsafe.Pointer, index int, typeNamePointer unsafe.Pointer) (unsafe.Pointer, string, bool) {
	// type name 是 registry 命名元表的 key，缺失时无法做可靠类型检查。
	if typeNamePointer == nil {
		// 空类型名指针不能参与 registry identity 判断。
		return nil, "userdata", false
	}
	typeName := nativeLuaCString(typeNamePointer)
	value, ok := nativeLuaValueAt(luaState, index)
	if !ok || value.Kind != runtime.KindUserdata {
		// 目标不是 userdata 时不能返回 C full userdata 指针。
		return nil, typeName, false
	}
	userdata, ok := value.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// userdata 引用损坏时按类型检查失败处理。
		return nil, typeName, false
	}
	block, ok := userdata.Data.(*nativeUserdataBlock)
	if !ok || block == nil {
		// 纯 Go userdata 没有 C full userdata 数据区，不能通过 Lua C API 暴露。
		return nil, typeName, false
	}
	metatable := userdata.GetMetatable()
	if metatable == nil {
		// 没有 raw 元表说明尚未绑定命名 userdata 类型。
		return nil, typeName, false
	}
	registry, ok := nativeLuaTableAt(luaState, runtime.RegistryPseudoIndex)
	if !ok {
		// registry 不可用时无法验证类型名。
		return nil, typeName, false
	}
	expectedValue := registry.RawGetString(typeName)
	if expectedValue.Kind != runtime.KindTable {
		// registry 中不存在命名元表或类型不匹配，检查失败。
		return nil, typeName, false
	}
	expectedMetatable, ok := expectedValue.Ref.(*runtime.Table)
	if !ok || expectedMetatable == nil || expectedMetatable != metatable {
		// Lua 5.3 以 registry[tname] 与 userdata raw metatable 的 identity 判断类型。
		return nil, typeName, false
	}

	// 类型匹配时返回 native full userdata 的 C 数据区指针。
	return block.data(), typeName, true
}

// nativeLuaTestUData 实现 Lua 5.3 lauxlib 的 luaL_testudata。
func nativeLuaTestUData(luaState unsafe.Pointer, index int, typeNamePointer unsafe.Pointer) unsafe.Pointer {
	// testudata 与 checkudata 使用同一命名元表匹配规则，但失败时只返回 NULL。
	pointer, _, ok := nativeLuaNamedFullUserdata(luaState, index, typeNamePointer)
	if !ok {
		// luaL_testudata 是非抛错探测入口，失败不能设置 pending error。
		return nil
	}
	return pointer
}

// nativeLuaCheckUData 实现 Lua 5.3 lauxlib 的 luaL_checkudata。
func nativeLuaCheckUData(luaState unsafe.Pointer, index int, typeNamePointer unsafe.Pointer) unsafe.Pointer {
	// checkudata 与 testudata 共享命名元表匹配，失败时额外记录 lauxlib 参数错误。
	pointer, typeName, ok := nativeLuaNamedFullUserdata(luaState, index, typeNamePointer)
	if !ok {
		// 保留错误对象，避免 C 模块把 nil 当作有效 full userdata 指针继续使用。
		return nativeLuaCheckUDataFailure(luaState, index, typeName)
	}
	return pointer
}

// lua_newuserdata 导出 Lua 5.3 C API full userdata 创建入口。
//
//export lua_newuserdata
func lua_newuserdata(luaState *C.lua_State, size C.size_t) unsafe.Pointer {
	// C API 入口只做类型转换，生命周期由 Go helper 绑定到 State.Close。
	return nativeLuaNewUserdata(unsafe.Pointer(luaState), uintptr(size))
}

// lua_touserdata 导出 Lua 5.3 C API userdata 指针查询入口。
//
//export lua_touserdata
func lua_touserdata(luaState *C.lua_State, index C.int) unsafe.Pointer {
	// C API 入口只做类型转换，具体索引与类型判断由 Go helper 维护。
	return nativeLuaToUserdata(unsafe.Pointer(luaState), int(index))
}

// lua_getuservalue 导出 Lua 5.3 C API userdata user value 读取入口。
//
//export lua_getuservalue
func lua_getuservalue(luaState *C.lua_State, index C.int) C.int {
	// C API 入口只做类型转换；具体 full userdata 与 nil 回退由 Go helper 维护。
	return C.int(nativeLuaGetUserValue(unsafe.Pointer(luaState), int(index)))
}

// lua_setuservalue 导出 Lua 5.3 C API userdata user value 写入入口。
//
//export lua_setuservalue
func lua_setuservalue(luaState *C.lua_State, index C.int) {
	// C API 入口只做类型转换；成功路径会弹出栈顶 user value。
	_ = nativeLuaSetUserValue(unsafe.Pointer(luaState), int(index))
}

// luaL_checkudata 导出 Lua 5.3 lauxlib full userdata 类型检查入口。
//
//export luaL_checkudata
func luaL_checkudata(luaState *C.lua_State, index C.int, typeName *C.char) unsafe.Pointer {
	// C API 入口只做类型转换；当前失败时记录 pending error 并返回 nil，等待 Go 边界传播。
	return nativeLuaCheckUData(unsafe.Pointer(luaState), int(index), unsafe.Pointer(typeName))
}

// luaL_testudata 导出 Lua 5.3 lauxlib full userdata 非抛错类型探测入口。
//
//export luaL_testudata
func luaL_testudata(luaState *C.lua_State, index C.int, typeName *C.char) unsafe.Pointer {
	// C API 入口只做类型转换；失败路径必须保持无错误副作用。
	return nativeLuaTestUData(unsafe.Pointer(luaState), int(index), unsafe.Pointer(typeName))
}
