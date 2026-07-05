package lua

import "testing"

// runRecursionGuardScript 打开标准库并执行递归 guard 脚本。
//
// source 必须是自包含 Lua 片段；脚本内通过 assert 表达语义边界，执行失败即表示未来递归优化破坏
// Lua 5.3 可见行为。
func runRecursionGuardScript(t *testing.T, source string) {
	// 每个 guard 使用独立 State，避免 debug hook、coroutine 或 upvalue 状态跨用例污染。
	t.Helper()
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法验证 debug/coroutine 语义，直接终止当前用例。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := DoString(state, source); err != nil {
		// Lua assert 或运行期错误都表示该 guard 语义未满足。
		t.Fatalf("DoString recursion guard failed: %v", err)
	}
}

// TestDoStringRecursionLocalFunctionEscapeGuards 验证递归 local function 逃逸时必须保持普通闭包语义。
//
// 未来若为 prepared recursion 引入非逃逸 local function descriptor，本用例要求闭包返回、传参、存表
// 和身份比较全部回退普通 closure/cell 路径，避免借用表示泄漏到 Lua 可见对象。
func TestDoStringRecursionLocalFunctionEscapeGuards(t *testing.T) {
	// 通过多种逃逸形态固定 fib 必须仍是同一个 Lua closure。
	runRecursionGuardScript(t, `
local function fib(n)
  if n < 2 then return n end
  return fib(n - 1) + fib(n - 2)
end

local returned = (function() return fib end)()
local function apply(fn, value)
  return fn(value)
end
local box = {fn = fib}

assert(returned(6) == 8)
assert(apply(fib, 7) == 13)
assert(box.fn(8) == 21)
assert(returned == fib)
assert(box.fn == fib)
`)
}

// TestDoStringRecursionDebugUpvalueGuards 验证递归 local function 的 self upvalue 对 debug API 可见。
//
// `debug.getupvalue`、`debug.setupvalue`、`debug.upvalueid` 和 `debug.upvaluejoin` 都能观察或修改递归
// 函数捕获的 self upvalue；未来优化只能在这些能力不可见时启用。
func TestDoStringRecursionDebugUpvalueGuards(t *testing.T) {
	// debug upvalue API 必须读取到真实 self closure，并能按现有 upvaluejoin 语义复制来源快照。
	runRecursionGuardScript(t, `
local function fib(n)
  if n < 2 then return n end
  return fib(n - 1) + fib(n - 2)
end

local name, value = debug.getupvalue(fib, 1)
assert(name == "fib")
assert(value == fib)
local identity = debug.upvalueid(fib, 1)
assert(debug.setupvalue(fib, 1, fib) == "fib")
assert(debug.upvalueid(fib, 1) == identity)

local holder
local function other(n)
  if n < 2 then return n end
  return holder(n - 1) + holder(n - 2)
end
holder = other
local otherName = debug.getupvalue(other, 1)
assert(otherName == "holder")
debug.upvaluejoin(other, 1, fib, 1)
local joinedName, joinedValue = debug.getupvalue(other, 1)
assert(joinedName == "holder")
assert(joinedValue == fib)
assert(other(6) == 8)
`)
}

// TestDoStringRecursionHookTracebackAndCoroutineGuards 验证递归路径必须保留 hook、traceback 与 yield。
//
// 递归函数在 pcall/error、debug line/call hook、traceback 和 coroutine.yield 中都暴露调用帧与恢复边界；
// 任何借用 descriptor 优化都必须在这些调试或协程语义打开时回退。
func TestDoStringRecursionHookTracebackAndCoroutineGuards(t *testing.T) {
	// 组合调试 hook、错误 traceback 与协程挂起，固定递归执行栈的可见性。
	runRecursionGuardScript(t, `
local events = {}
debug.sethook(function(event, line)
  if event == "call" or event == "return" or event == "line" then
    events[#events + 1] = event
  end
end, "crl")

local function boom(n)
  if n == 0 then error("guard-boom") end
  return boom(n - 1)
end
local ok, message = pcall(boom, 3)
debug.sethook()
assert(not ok)
assert(string.find(message, "guard%-boom"))
assert(#events > 0)

local traced = debug.traceback(message)
assert(string.find(traced, "boom"))

local co = coroutine.create(function()
  local function fib(n)
    if n < 2 then return n end
    coroutine.yield("step", n)
    return fib(n - 1) + fib(n - 2)
  end
  return fib(4)
end)

local ok1, tag, n = coroutine.resume(co)
assert(ok1 and tag == "step" and n == 4)
local ok2 = coroutine.resume(co)
assert(ok2)
`)
}
