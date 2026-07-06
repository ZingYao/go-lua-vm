//go:build native_modules

package native

/*
#include <stddef.h>
#include <stdlib.h>
#include <string.h>

typedef struct lua_State lua_State;
typedef long long lua_Integer;

#ifndef LUAL_BUFFERSIZE
#define LUAL_BUFFERSIZE ((int)(0x80 * sizeof(void*) * sizeof(lua_Integer)))
#endif

typedef struct luaL_Buffer {
	char *b;
	size_t size;
	size_t n;
	lua_State *L;
	char initb[LUAL_BUFFERSIZE];
} luaL_Buffer;

static size_t glua_luaL_buffer_default_size(void) {
	return LUAL_BUFFERSIZE;
}

static char* glua_luaL_buffer_init_storage(luaL_Buffer* B) {
	return B->initb;
}

static void glua_luaL_buffer_reset(luaL_Buffer* B, lua_State* L) {
	B->b = B->initb;
	B->size = LUAL_BUFFERSIZE;
	B->n = 0;
	B->L = L;
}

static char* glua_luaL_buffer_realloc(luaL_Buffer* B, size_t nextSize) {
	char* next;
	if (B->b == B->initb) {
		next = (char*)malloc(nextSize);
		if (next != NULL && B->n > 0) {
			memcpy(next, B->b, B->n);
		}
	} else {
		next = (char*)realloc(B->b, nextSize);
	}
	if (next == NULL) {
		return NULL;
	}
	B->b = next;
	B->size = nextSize;
	return next;
}

static void glua_luaL_buffer_free(luaL_Buffer* B) {
	if (B->b != NULL && B->b != B->initb) {
		free(B->b);
	}
	B->b = B->initb;
	B->size = LUAL_BUFFERSIZE;
	B->n = 0;
}
*/
import "C"

import (
	"math"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// nativeLuaBufferEnsure 保证 luaL_Buffer 至少能追加 extra 字节，返回当前写入起点；extra 为追加字节数，返回 nil 表示容量溢出或分配失败。
func nativeLuaBufferEnsure(buffer *C.luaL_Buffer, extra C.size_t) *C.char {
	// 空 buffer 不能安全写入，直接返回 nil 交由调用方忽略本次追加。
	if buffer == nil {
		return nil
	}
	// 未初始化的 buffer 按 Lua 5.3 lauxlib 约定回到内置短缓冲。
	if buffer.b == nil {
		buffer.b = (*C.char)(unsafe.Pointer(C.glua_luaL_buffer_init_storage(buffer)))
		buffer.size = C.glua_luaL_buffer_default_size()
		buffer.n = 0
	}

	used := uintptr(buffer.n)
	additional := uintptr(extra)
	// 容量计算溢出时不能继续写入，避免越界破坏宿主进程。
	if additional > math.MaxUint-used {
		return nil
	}
	required := used + additional
	currentSize := uintptr(buffer.size)
	// 现有容量足够时，直接返回追加起点。
	if required <= currentSize {
		return (*C.char)(unsafe.Add(unsafe.Pointer(buffer.b), used))
	}

	nextSize := currentSize
	defaultSize := uintptr(C.glua_luaL_buffer_default_size())
	// 零容量 buffer 使用 Lua 默认短缓冲大小作为扩容起点。
	if nextSize == 0 {
		nextSize = defaultSize
	}
	// 按倍增策略扩容，降低长字符串连续追加时的 realloc 次数。
	for nextSize < required {
		// 倍增会溢出时退化为刚好满足 required 的容量。
		if nextSize > math.MaxUint/2 {
			nextSize = required
			break
		}
		nextSize *= 2
	}
	// 底层分配失败时保持原 buffer 不变，由调用方放弃本次追加。
	if C.glua_luaL_buffer_realloc(buffer, C.size_t(nextSize)) == nil {
		return nil
	}
	return (*C.char)(unsafe.Add(unsafe.Pointer(buffer.b), used))
}

// nativeLuaBufferAppendBytes 将 C 字节片追加到 luaL_Buffer；source 必须在 length 字节内可读，分配失败时不改变可见 Lua 栈。
func nativeLuaBufferAppendBytes(buffer *C.luaL_Buffer, source unsafe.Pointer, length C.size_t) {
	// 空追加不需要分配，也不改变 buffer 状态。
	if length == 0 {
		return
	}
	// 非空追加必须有有效来源指针，否则直接忽略非法 C 调用。
	if source == nil {
		return
	}
	target := nativeLuaBufferEnsure(buffer, length)
	// 容量不足或分配失败时放弃本次追加，避免写越界。
	if target == nil {
		return
	}
	C.memcpy(unsafe.Pointer(target), source, length)
	buffer.n += length
}

// nativeLuaBufferAppendString 将 Go 字符串按原始字节追加到 luaL_Buffer；字符串内容不会经过编码转换。
func nativeLuaBufferAppendString(buffer *C.luaL_Buffer, text string) {
	// 空字符串追加在 Lua 语义上无可见变化。
	if text == "" {
		return
	}
	nativeLuaBufferAppendBytes(buffer, unsafe.Pointer(unsafe.StringData(text)), C.size_t(len(text)))
}

// nativeLuaBufferValueString 按 lua_tolstring 兼容边界把栈值转为可追加字符串；仅字符串和数字可转换。
func nativeLuaBufferValueString(value runtime.Value) (string, bool) {
	// 字符串值直接使用其原始内容。
	if value.Kind == runtime.KindString {
		return value.String, true
	}
	// 整数和浮点数沿用 VM 的 Lua 5.3 数字转字符串规则。
	if value.Kind == runtime.KindInteger || value.Kind == runtime.KindNumber {
		return value.NumberToString()
	}
	return "", false
}

// luaL_buffinit 初始化 Lua C API 的 luaL_Buffer；L 为原生 shim 状态句柄，B 由 C 模块分配。
//
//export luaL_buffinit
func luaL_buffinit(luaState *C.lua_State, buffer *C.luaL_Buffer) {
	// 空 buffer 无法初始化，直接返回以避免解引用崩溃。
	if buffer == nil {
		return
	}
	C.glua_luaL_buffer_reset(buffer, luaState)
}

// luaL_prepbuffsize 为 luaL_Buffer 预留追加空间；返回值是可写入 sz 字节的起始地址。
//
//export luaL_prepbuffsize
func luaL_prepbuffsize(buffer *C.luaL_Buffer, size C.size_t) *C.char {
	return nativeLuaBufferEnsure(buffer, size)
}

// luaL_addlstring 将指定长度的字节串追加到 luaL_Buffer；兼容 Lua 5.3 lauxlib 的原始字节语义。
//
//export luaL_addlstring
func luaL_addlstring(buffer *C.luaL_Buffer, source *C.char, length C.size_t) {
	nativeLuaBufferAppendBytes(buffer, unsafe.Pointer(source), length)
}

// luaL_addstring 将以 NUL 结尾的 C 字符串追加到 luaL_Buffer；nil 字符串不会产生可见效果。
//
//export luaL_addstring
func luaL_addstring(buffer *C.luaL_Buffer, source *C.char) {
	// nil 字符串无法计算长度，按无追加处理。
	if source == nil {
		return
	}
	nativeLuaBufferAppendBytes(buffer, unsafe.Pointer(source), C.strlen(source))
}

// luaL_addvalue 将 Lua 栈顶值按 lua_tolstring 规则追加到 luaL_Buffer，并弹出该栈顶值。
//
//export luaL_addvalue
func luaL_addvalue(buffer *C.luaL_Buffer) {
	// 空 buffer 或未绑定状态时无法访问 Lua 栈。
	if buffer == nil || buffer.L == nil {
		return
	}
	state, ok := lookupNativeStateHandle(unsafe.Pointer(buffer.L))
	// 未找到状态句柄说明 C 模块传入了非 glua 管理的 lua_State。
	if !ok {
		return
	}
	top := state.StackTop()
	// 空栈没有可追加值。
	if top <= 0 {
		return
	}
	text, convertible := nativeLuaBufferValueString(state.ValueAt(top))
	// 不可转字符串时保持 Lua 5.3 的错误语义边界：记录 pending error 并弹出消费的栈顶值。
	if !convertible {
		setNativeStatePendingError(unsafe.Pointer(buffer.L), runtime.StringValue("string expected"))
		state.Pop()
		return
	}
	nativeLuaBufferAppendString(buffer, text)
	state.Pop()
}

// luaL_pushresult 将 luaL_Buffer 当前内容压入 Lua 栈，并释放可能分配的堆缓冲。
//
//export luaL_pushresult
func luaL_pushresult(buffer *C.luaL_Buffer) {
	// 空 buffer 或未绑定状态不能压栈。
	if buffer == nil || buffer.L == nil {
		return
	}
	var text string
	// 非空内容按原始字节构造 Go 字符串再压入 Lua 栈。
	if buffer.b != nil && buffer.n > 0 {
		bytes := unsafe.Slice((*byte)(unsafe.Pointer(buffer.b)), int(buffer.n))
		text = string(bytes)
	}
	nativeLuaPushValue(unsafe.Pointer(buffer.L), runtime.StringValue(text))
	C.glua_luaL_buffer_free(buffer)
}

// luaL_pushresultsize 先确认最近写入的 size 字节进入有效长度，再把结果压入 Lua 栈。
//
//export luaL_pushresultsize
func luaL_pushresultsize(buffer *C.luaL_Buffer, size C.size_t) {
	// 空 buffer 无法调整结果长度。
	if buffer == nil {
		return
	}
	// 如果 C 模块直接写入了 luaL_prepbuffsize 返回的区域，这里补记有效长度。
	if nativeLuaBufferEnsure(buffer, size) != nil {
		buffer.n += size
	}
	luaL_pushresult(buffer)
}

// luaL_buffinitsize 初始化 luaL_Buffer 并预留 size 字节，返回可写入区域起点。
//
//export luaL_buffinitsize
func luaL_buffinitsize(luaState *C.lua_State, buffer *C.luaL_Buffer, size C.size_t) *C.char {
	luaL_buffinit(luaState, buffer)
	return luaL_prepbuffsize(buffer, size)
}
