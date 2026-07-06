#include "lua.h"
#include "lauxlib.h"

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
