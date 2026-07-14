//go:build cgo

package native

/*
#include <stddef.h>
#include <stdlib.h>

typedef struct lua_State lua_State;
typedef void * (*lua_Alloc) (void *ud, void *ptr, size_t osize, size_t nsize);

static void* glua_native_lua_alloc(void* ud, void* ptr, size_t osize, size_t nsize) {
	(void)ud;
	(void)osize;
	if (nsize == 0) {
		free(ptr);
		return NULL;
	}
	return realloc(ptr, nsize);
}

static lua_Alloc glua_native_lua_alloc_function(void) {
	return glua_native_lua_alloc;
}
*/
import "C"

import "unsafe"

// nativeLuaGetAllocF 返回 native shim 的 Lua 5.3 兼容 C heap 分配器。
func nativeLuaGetAllocF(luaState unsafe.Pointer, userData *unsafe.Pointer) C.lua_Alloc {
	// 当前 native shim 的 C 模块内存均走 C heap；lua_State 只用于匹配 Lua 5.3 public API 签名。
	_ = luaState
	if userData != nil {
		// 分配器不需要额外上下文，向 C 模块明确返回 NULL ud。
		*userData = nil
	}
	return C.glua_native_lua_alloc_function()
}

// lua_getallocf 导出 Lua 5.3 C API allocator 查询入口。
//
//export lua_getallocf
func lua_getallocf(luaState *C.lua_State, userData *unsafe.Pointer) C.lua_Alloc {
	// C API 入口只做 token 和 ud 转发，具体 allocator 选择由 Go helper 统一维护。
	return nativeLuaGetAllocF(unsafe.Pointer(luaState), userData)
}
