package.path = @GLUA_NATIVE_PACKAGE_PATH@
package.cpath = @GLUA_NATIVE_PACKAGE_CPATH@

local mod = require("glua_native_smoke")
assert(mod.add(20, 22) == 42)
assert(mod.echo("hello") == "hello")

local a, b, c = mod.multi()
assert(a == 1 and b == "two" and c == true)
assert(mod.alloc_roundtrip() == true)

local cleanup_result, cleanup_top = mod.runtimecap_cleanup_probe(function(subject, position, s1, s2)
	assert(subject == "subject")
	assert(position == 12)
	assert(s1 == "==")
	assert(s2 == "==")
	return s1 == s2
end)
assert(cleanup_result == true)
assert(cleanup_top == 2, cleanup_top)

local outer_capture_marker = "before"
local seen_positions = {}
local sequence_matches, sequence_top, sentinel_stable = mod.runtimecap_sequence_probe(function(subject, position, s1, s2)
	assert(subject == "subject")
	outer_capture_marker = tostring(position)
	seen_positions[#seen_positions + 1] = tostring(position)
	return s1 == s2
end)
assert(table.concat(seen_positions, ",") == "7,12,13,14,15,18", table.concat(seen_positions, ","))
assert(outer_capture_marker == "18", outer_capture_marker)
assert(sequence_matches == 1, sequence_matches)
assert(sequence_top == 8, sequence_top)
assert(sentinel_stable == true)

local overflow_ok, overflow_message = pcall(mod.doublestack_overflow_probe, 64)
assert(overflow_ok == false, "doublestack overflow probe unexpectedly succeeded")
assert(string.find(overflow_message, "native doublestack overflow after 64 replacements", 1, true), overflow_message)

local after_overflow_positions = {}
local after_overflow_matches, after_overflow_top, after_overflow_sentinel = mod.runtimecap_sequence_probe(function(subject, position, s1, s2)
	assert(subject == "subject")
	after_overflow_positions[#after_overflow_positions + 1] = tostring(position)
	return s1 == s2
end)
assert(table.concat(after_overflow_positions, ",") == "7,12,13,14,15,18", table.concat(after_overflow_positions, ","))
assert(after_overflow_matches == 1, after_overflow_matches)
assert(after_overflow_top == 8, after_overflow_top)
assert(after_overflow_sentinel == true)

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
