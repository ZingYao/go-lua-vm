#include "lua.h"
#include "lauxlib.h"
#include <string.h>

static int glua_native_add(lua_State *L) {
	lua_Integer a = luaL_checkinteger(L, 1);
	lua_Integer b = luaL_checkinteger(L, 2);
	lua_pushinteger(L, a + b);
	return 1;
}

static int glua_native_echo(lua_State *L) {
	size_t length = 0;
	const char *text = luaL_checklstring(L, 1, &length);
	lua_pushlstring(L, text, length);
	return 1;
}

static int glua_native_multi(lua_State *L) {
	lua_pushinteger(L, 1);
	lua_pushstring(L, "two");
	lua_pushboolean(L, 1);
	return 3;
}

static int glua_native_fail(lua_State *L) {
	const char *text = luaL_checkstring(L, 1);
	return luaL_error(L, "native failure: %s", text);
}

static int glua_native_raise(lua_State *L) {
	lua_pushstring(L, "native lua_error object");
	return lua_error(L);
}

static int glua_native_alloc_roundtrip(lua_State *L) {
	void *ud = NULL;
	lua_Alloc alloc = lua_getallocf(L, &ud);
	if (alloc == NULL) {
		return luaL_error(L, "native allocator missing");
	}
	char *block = (char*)alloc(ud, NULL, 0, 16);
	if (block == NULL) {
		return luaL_error(L, "native allocator malloc failed");
	}
	memset(block, 0x2a, 16);
	char *grown = (char*)alloc(ud, block, 16, 32);
	if (grown == NULL) {
		alloc(ud, block, 16, 0);
		return luaL_error(L, "native allocator realloc failed");
	}
	int preserved = 1;
	for (int i = 0; i < 16; i++) {
		if ((unsigned char)grown[i] != 0x2a) {
			preserved = 0;
			break;
		}
	}
	memset(grown + 16, 0x7f, 16);
	alloc(ud, grown, 32, 0);
	lua_pushboolean(L, preserved && ud == NULL);
	return 1;
}

typedef struct glua_native_counter {
	lua_Integer value;
} glua_native_counter;

static int glua_native_counter_add(lua_State *L) {
	glua_native_counter *counter = (glua_native_counter*)luaL_checkudata(L, 1, "glua_native_counter");
	lua_Integer delta = luaL_checkinteger(L, 2);
	counter->value += delta;
	lua_pushinteger(L, counter->value);
	return 1;
}

static int glua_native_counter_get(lua_State *L) {
	glua_native_counter *counter = (glua_native_counter*)luaL_checkudata(L, 1, "glua_native_counter");
	lua_pushinteger(L, counter->value);
	return 1;
}

static const luaL_Reg glua_native_counter_methods[] = {
	{"add", glua_native_counter_add},
	{"get", glua_native_counter_get},
	{NULL, NULL},
};

static void glua_native_register_counter(lua_State *L) {
	if (luaL_newmetatable(L, "glua_native_counter")) {
		lua_pushvalue(L, -1);
		lua_setfield(L, -2, "__index");
		luaL_setfuncs(L, glua_native_counter_methods, 0);
	}
	lua_settop(L, -2);
}

static int glua_native_new_counter(lua_State *L) {
	lua_Integer initial = luaL_checkinteger(L, 1);
	glua_native_counter *counter = (glua_native_counter*)lua_newuserdata(L, sizeof(glua_native_counter));
	counter->value = initial;
	luaL_getmetatable(L, "glua_native_counter");
	lua_setmetatable(L, -2);
	return 1;
}

static const luaL_Reg glua_native_smoke_funcs[] = {
	{"add", glua_native_add},
	{"echo", glua_native_echo},
	{"multi", glua_native_multi},
	{"new_counter", glua_native_new_counter},
	{"fail", glua_native_fail},
	{"raise", glua_native_raise},
	{"alloc_roundtrip", glua_native_alloc_roundtrip},
	{NULL, NULL},
};

int luaopen_glua_native_smoke(lua_State *L) {
	luaL_newlib(L, glua_native_smoke_funcs);
	glua_native_register_counter(L);
	return 1;
}

int luaopen_glua_native_failopen(lua_State *L) {
	return luaL_error(L, "native open failure");
}
