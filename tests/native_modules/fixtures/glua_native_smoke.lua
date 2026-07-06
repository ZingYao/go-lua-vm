package.path = @GLUA_NATIVE_PACKAGE_PATH@
package.cpath = @GLUA_NATIVE_PACKAGE_CPATH@

local mod = require("glua_native_smoke")
assert(mod.add(20, 22) == 42)
assert(mod.echo("hello") == "hello")

local a, b, c = mod.multi()
assert(a == 1 and b == "two" and c == true)
assert(mod.alloc_roundtrip() == true)

local counter = mod.new_counter(10)
assert(counter:add(5) == 15)
assert(counter:get() == 15)
assert(counter:add(-2) == 13)
assert(counter:get() == 13)

local ok, message = pcall(mod.fail, "boom")
assert(ok == false and string.find(message, "native failure: boom", 1, true), message)

local raised, object = pcall(mod.raise)
assert(raised == false and object == "native lua_error object", object)

local traced, traceback = xpcall(function()
	return mod.fail("trace")
end, debug.traceback)
assert(traced == false, "xpcall unexpectedly succeeded")
assert(string.find(traceback, "native failure: trace", 1, true), traceback)
assert(string.find(traceback, "stack traceback:", 1, true), traceback)

assert(require("glua_native_smoke") == mod)
