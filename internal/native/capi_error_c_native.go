//go:build native_modules

package native

/*
#include <stdarg.h>
#include <stdio.h>

typedef struct lua_State lua_State;

extern int glua_luaL_error_message(lua_State *L, const char *message);

int luaL_error(lua_State *L, const char *fmt, ...) {
	char buffer[512];
	va_list args;
	va_start(args, fmt);
	vsnprintf(buffer, sizeof(buffer), fmt, args);
	va_end(args);
	buffer[sizeof(buffer) - 1] = '\0';
	return glua_luaL_error_message(L, buffer);
}
*/
import "C"
