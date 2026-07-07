//go:build native_modules

package native

/*
#include <setjmp.h>
#include <stddef.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

typedef struct lua_State lua_State;
typedef int (*lua_CFunction)(lua_State *L);
typedef ptrdiff_t lua_KContext;
typedef int (*lua_KFunction)(lua_State *L, int status, lua_KContext ctx);

extern int glua_lua_error_record(lua_State *L);
extern int glua_luaL_argerror_record(lua_State *L, int arg, const char *extra);
extern int glua_luaL_error_message(lua_State *L, const char *message);
extern const char* glua_lua_pushfstring_message(lua_State *L, const char *message);
extern int glua_lua_callk_record(lua_State *L, int argument_count, int result_count);

#if defined(_MSC_VER)
#define GLUA_THREAD_LOCAL __declspec(thread)
#else
#define GLUA_THREAD_LOCAL __thread
#endif

static GLUA_THREAD_LOCAL jmp_buf *glua_native_error_target = NULL;

static void glua_lua_error_jump(void) {
	if (glua_native_error_target != NULL) {
		longjmp(*glua_native_error_target, 1);
	}
	abort();
}

void lua_callk(lua_State *L, int argument_count, int result_count, lua_KContext context, lua_KFunction continuation) {
	(void)context;
	(void)continuation;
	if (glua_lua_callk_record(L, argument_count, result_count) != 0) {
		glua_lua_error_jump();
	}
}

static int glua_call_lua_cfunction(void* function, lua_State* L) {
	if (function == NULL) {
		return -1;
	}
	jmp_buf env;
	jmp_buf *previous = glua_native_error_target;
	glua_native_error_target = &env;
	if (setjmp(env) != 0) {
		glua_native_error_target = previous;
		return -2;
	}
	lua_CFunction fn = (lua_CFunction)function;
	int result = fn(L);
	glua_native_error_target = previous;
	return result;
}

int lua_error(lua_State *L) {
	glua_lua_error_record(L);
	glua_lua_error_jump();
	return 0;
}

int luaL_argerror(lua_State *L, int arg, const char *extra) {
	glua_luaL_argerror_record(L, arg, extra);
	glua_lua_error_jump();
	return 0;
}

int luaL_error(lua_State *L, const char *fmt, ...) {
	char stack_buffer[512];
	va_list args;
	va_start(args, fmt);
	int required = vsnprintf(NULL, 0, fmt, args);
	va_end(args);
	if (required < 0) {
		glua_luaL_error_message(L, "native luaL_error formatting failed");
		glua_lua_error_jump();
		return 0;
	}
	if ((size_t)required < sizeof(stack_buffer)) {
		va_start(args, fmt);
		vsnprintf(stack_buffer, sizeof(stack_buffer), fmt, args);
		va_end(args);
		stack_buffer[sizeof(stack_buffer) - 1] = '\0';
		glua_luaL_error_message(L, stack_buffer);
		glua_lua_error_jump();
		return 0;
	}
	char *heap_buffer = (char*)malloc((size_t)required + 1);
	if (heap_buffer == NULL) {
		glua_luaL_error_message(L, "native luaL_error memory allocation failed");
		glua_lua_error_jump();
		return 0;
	}
	va_start(args, fmt);
	vsnprintf(heap_buffer, (size_t)required + 1, fmt, args);
	va_end(args);
	heap_buffer[required] = '\0';
	glua_luaL_error_message(L, heap_buffer);
	free(heap_buffer);
	glua_lua_error_jump();
	return 0;
}

static const char* glua_push_formatted_string(lua_State *L, const char *fmt, va_list args) {
	char stack_buffer[512];
	if (fmt == NULL) {
		return glua_lua_pushfstring_message(L, "");
	}
	va_list count_args;
	va_copy(count_args, args);
	int required = vsnprintf(NULL, 0, fmt, count_args);
	va_end(count_args);
	if (required < 0) {
		return glua_lua_pushfstring_message(L, "native lua_pushfstring formatting failed");
	}
	if ((size_t)required < sizeof(stack_buffer)) {
		va_list format_args;
		va_copy(format_args, args);
		vsnprintf(stack_buffer, sizeof(stack_buffer), fmt, format_args);
		va_end(format_args);
		stack_buffer[sizeof(stack_buffer) - 1] = '\0';
		return glua_lua_pushfstring_message(L, stack_buffer);
	}
	char *heap_buffer = (char*)malloc((size_t)required + 1);
	if (heap_buffer == NULL) {
		return glua_lua_pushfstring_message(L, "native lua_pushfstring memory allocation failed");
	}
	va_list format_args;
	va_copy(format_args, args);
	vsnprintf(heap_buffer, (size_t)required + 1, fmt, format_args);
	va_end(format_args);
	heap_buffer[required] = '\0';
	const char *result = glua_lua_pushfstring_message(L, heap_buffer);
	free(heap_buffer);
	return result;
}

const char *lua_pushvfstring(lua_State *L, const char *fmt, va_list argp) {
	return glua_push_formatted_string(L, fmt, argp);
}

const char *lua_pushfstring(lua_State *L, const char *fmt, ...) {
	va_list args;
	va_start(args, fmt);
	const char *result = glua_push_formatted_string(L, fmt, args);
	va_end(args);
	return result;
}
*/
import "C"

import "unsafe"

// nativeLuaInvokeCFunction 在 C 层建立 setjmp 边界后调用 lua_CFunction。
func nativeLuaInvokeCFunction(luaState unsafe.Pointer, function unsafe.Pointer) int {
	// lua_error/luaL_error/luaL_argerror 会 longjmp 回该 C helper，再由 Go 边界读取 pending error。
	return int(C.glua_call_lua_cfunction(function, (*C.lua_State)(luaState)))
}
