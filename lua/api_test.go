package lua

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/compiler/parser"
	"github.com/ZingYao/go-lua-vm/runtime"
)

// quoteLuaString 返回可嵌入测试 Lua 源码的字符串字面量。
//
// value 是宿主路径或文本；返回值使用 Go 双引号转义格式，Lua 5.3 同样接受这些基础转义。
func quoteLuaString(value string) string {
	// strconv.Quote 会处理路径中的反斜杠、引号和控制字符。
	return strconv.Quote(value)
}

// cancelAfterErrContext 是测试专用的可取消 context。
//
// Context 会在第 checks 次 Err 调用后稳定返回 context.Canceled，用于验证 VM 热路径在长循环中
// 仍能通过 State.CheckContext 观察宿主取消信号。
type cancelAfterErrContext struct {
	context.Context
	remainingErrChecks int
	canceled           bool
}

// newCancelAfterErrContext 创建按 Err 调用次数取消的测试 context。
//
// checks 小于 1 时会归一为 1，避免构造永不取消或初始状态不明确的测试上下文。
func newCancelAfterErrContext(checks int) *cancelAfterErrContext {
	// 先归一化检查次数，保证测试上下文总会在有限 CheckContext 调用内取消。
	if checks < 1 {
		// 非正数没有明确的倒计时语义，按首次 Err 调用即取消处理。
		checks = 1
	}
	return &cancelAfterErrContext{
		Context:            context.Background(),
		remainingErrChecks: checks,
	}
}

// Err 按倒计时返回 context.Canceled。
//
// 返回 nil 表示 VM 仍可继续执行；首次返回 context.Canceled 后保持幂等，符合 context.Err 的稳定语义。
func (ctx *cancelAfterErrContext) Err() error {
	// 已取消后必须稳定返回同一个取消错误，避免 VM 多次检查观察到回退状态。
	if ctx.canceled {
		// context.Canceled 是宿主取消的标准错误对象，State.CheckContext 会保留错误链。
		return context.Canceled
	}
	ctx.remainingErrChecks--
	if ctx.remainingErrChecks <= 0 {
		// 倒计时耗尽时进入取消态，并从本次检查开始对 VM 可见。
		ctx.canceled = true
		return context.Canceled
	}
	return nil
}

// TestNewStateAndOptions 验证 lua 包 API 与 runtime 状态默认选项一致。
//
// 校验默认状态可创建、关闭后幂等，以及自定义 options 在构造时生效。
func TestNewStateAndOptions(t *testing.T) {
	state := NewState()
	if state == nil {
		t.Fatal("NewState should return non-nil state")
	}
	if state.IsClosed() {
		t.Fatal("new state should not be closed")
	}
	if state.Options().MaxStackDepth != runtime.DefaultMaxStackDepth {
		t.Fatalf("unexpected default max stack depth: got=%d want=%d", state.Options().MaxStackDepth, runtime.DefaultMaxStackDepth)
	}
	if state.Options().MaxCallDepth != runtime.DefaultMaxCallDepth {
		t.Fatalf("unexpected default max call depth: got=%d want=%d", state.Options().MaxCallDepth, runtime.DefaultMaxCallDepth)
	}
	if err := SetContext(state, context.Background()); err != nil {
		t.Fatalf("set valid context should succeed")
	}
	state.Close()
	state.Close()
	if !state.IsClosed() {
		t.Fatal("state should remain closed after repeated Close")
	}

	custom := NewStateWithOptions(Options{MaxStackDepth: 123, MaxCallDepth: 77, MaxAllocationBudget: 4096})
	if custom.Options().MaxStackDepth != 123 {
		t.Fatalf("custom max stack depth should be preserved, got=%d", custom.Options().MaxStackDepth)
	}
	if custom.Options().MaxCallDepth != 77 {
		t.Fatalf("custom max call depth should be preserved, got=%d", custom.Options().MaxCallDepth)
	}
	if custom.Options().MaxAllocationBudget != 4096 {
		t.Fatalf("custom allocation budget should be preserved, got=%d", custom.Options().MaxAllocationBudget)
	}
	custom.Close()
}

// TestContextAndResourceOptionsAPI 验证 context 取消入口和资源限制配置 API。
//
// NewStateWithContext 必须拒绝 nil context；DefaultOptions 与 NormalizeOptions 必须对外暴露与
// runtime 一致的资源限制配置；Call 和 ProtectedCall 必须在回调执行前观察 context 取消。
func TestContextAndResourceOptionsAPI(t *testing.T) {
	// nil context 会破坏取消语义，必须被拒绝。
	var nilContext context.Context
	if state, err := NewStateWithContext(nilContext, Options{}); state != nil || !errors.Is(err, ErrNilContext) {
		t.Fatalf("NewStateWithContext nil context = state=%v err=%v", state, err)
	}

	defaultOptions := DefaultOptions()
	if defaultOptions.MaxStackDepth != DefaultMaxStackDepth {
		// 默认栈深度必须与 runtime 默认值一致。
		t.Fatalf("default max stack depth = %d", defaultOptions.MaxStackDepth)
	}
	if defaultOptions.MaxCallDepth != DefaultMaxCallDepth {
		// 默认调用深度必须与 runtime 默认值一致。
		t.Fatalf("default max call depth = %d", defaultOptions.MaxCallDepth)
	}
	if defaultOptions.MaxAllocationBudget != 0 {
		// 默认分配预算 0 表示不限制。
		t.Fatalf("default allocation budget = %d", defaultOptions.MaxAllocationBudget)
	}

	normalized := NormalizeOptions(Options{MaxStackDepth: -1, MaxCallDepth: 0, MaxAllocationBudget: -32})
	if normalized.MaxStackDepth != DefaultMaxStackDepth {
		// 非正栈深度必须回落到默认值。
		t.Fatalf("normalized max stack depth = %d", normalized.MaxStackDepth)
	}
	if normalized.MaxCallDepth != DefaultMaxCallDepth {
		// 非正调用深度必须回落到默认值。
		t.Fatalf("normalized max call depth = %d", normalized.MaxCallDepth)
	}
	if normalized.MaxAllocationBudget != 0 {
		// 负分配预算必须归一为不限制。
		t.Fatalf("normalized allocation budget = %d", normalized.MaxAllocationBudget)
	}

	ctx, cancel := context.WithCancel(context.Background())
	state, err := NewStateWithContext(ctx, Options{MaxStackDepth: 9})
	if err != nil {
		// 有效 context 和 options 不应创建失败。
		t.Fatalf("NewStateWithContext failed: %v", err)
	}
	defer state.Close()
	if state.Context() != ctx {
		// 新建 State 必须保存宿主传入的 context。
		t.Fatalf("state context should be the supplied context")
	}
	if state.Options().MaxStackDepth != 9 {
		// 自定义资源限制必须在创建时生效。
		t.Fatalf("state max stack depth = %d", state.Options().MaxStackDepth)
	}

	cancel()
	called := false
	_, err = Call(state, runtime.ReferenceValue(KindGoClosure, runtime.GoResultsFunction(func(args ...Value) ([]Value, error) {
		// context 已取消时该回调不应被执行。
		called = true
		return nil, nil
	})))
	if !errors.Is(err, context.Canceled) {
		// Call 必须保留 context.Canceled 错误链。
		t.Fatalf("Call canceled error = %v", err)
	}
	if called {
		// 取消后不应进入 Go 回调，避免宿主副作用。
		t.Fatalf("Call should not execute callback after context cancellation")
	}
	err = ProtectedCall(state, func(callState *State) error {
		// context 已取消时该回调不应被执行。
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		// ProtectedCall 必须保留 context.Canceled 错误链。
		t.Fatalf("ProtectedCall canceled error = %v", err)
	}
}

// TestCloseWrapper 验证 lua.Close 对生命周期关闭和 nil State 的行为。
//
// Close 必须调用底层 runtime.Close，并对 nil State 返回 ErrNilState。
func TestCloseWrapper(t *testing.T) {
	// nil State 必须返回明确错误，避免宿主误以为关闭成功。
	if err := Close(nil); !errors.Is(err, ErrNilState) {
		t.Fatalf("Close nil state should return ErrNilState, got=%v", err)
	}
	state := NewState()
	if err := Close(state); err != nil {
		// 非 nil State 关闭不应失败。
		t.Fatalf("Close failed: %v", err)
	}
	if !state.IsClosed() {
		// Close 包装函数必须真正关闭底层 State。
		t.Fatalf("state should be closed")
	}
	if err := Close(state); err != nil {
		// 重复关闭保持幂等。
		t.Fatalf("Close repeated failed: %v", err)
	}
}

// TestOpenLibsRegistersStandardLibraries 验证 lua.OpenLibs 注册标准库集合。
//
// 当前已实现库应一次性写入全局环境，便于嵌入方直接获得接近 CLI 的运行环境。
func TestOpenLibsRegistersStandardLibraries(t *testing.T) {
	// 创建独立 State 并注册全部标准库。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 全库注册不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	for _, name := range []string{"_G", "_VERSION", "package", "require", "coroutine", "table", "string", "math", "io", "os", "utf8", "debug"} {
		// 每个核心全局符号都必须被注册。
		value, err := GetGlobal(state, name)
		if err != nil {
			t.Fatalf("GetGlobal %s failed: %v", name, err)
		}
		if value.IsNil() {
			t.Fatalf("global %s should be registered", name)
		}
	}
	requireValue, err := GetGlobal(state, "require")
	if err != nil {
		// require 全局读取不应失败。
		t.Fatalf("GetGlobal require failed: %v", err)
	}
	stringResults, err := Call(state, requireValue, runtime.StringValue("string"))
	if err != nil {
		// OpenLibs 后标准库模块应能通过 require 命中 package.loaded。
		t.Fatalf("require string failed: %v", err)
	}
	stringGlobal, err := GetGlobal(state, "string")
	if err != nil {
		// string 全局读取不应失败。
		t.Fatalf("GetGlobal string failed: %v", err)
	}
	if len(stringResults) != 1 || !stringResults[0].RawEqual(stringGlobal) {
		// require("string") 应返回已打开的 string 库表。
		t.Fatalf("require string results = %#v, want global string", stringResults)
	}
	if err := OpenLibs(nil); !errors.Is(err, ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("OpenLibs nil state should return ErrNilState, got=%v", err)
	}
}

// TestOpenLibsHonorsHostAccessOptions 验证标准库注册会遵守 State 宿主访问权限。
//
// 默认嵌入模式必须拒绝环境变量和文件系统访问；显式授权的 State 才可运行官方 CLI 类脚本。
func TestOpenLibsHonorsHostAccessOptions(t *testing.T) {
	// 默认 State 打开标准库后仍应保持宿主访问禁用。
	defaultState := NewState()
	defer defaultState.Close()
	if err := OpenLibs(defaultState); err != nil {
		// 标准库注册本身不应失败。
		t.Fatalf("OpenLibs default failed: %v", err)
	}
	err := DoString(defaultState, `
local ok, message = pcall(os.getenv, "PATH")
assert(not ok and string.find(message, "host environment access is disabled", 1, true))
ok, message = pcall(io.open, "missing.tmp", "w")
assert(not ok and string.find(message, "host filesystem access is disabled", 1, true))
`)
	if err != nil {
		// 默认权限应以可捕获 Lua error 表达，脚本断言必须通过。
		t.Fatalf("default host access policy failed: %v", err)
	}

	// 显式授权 State 应能读取环境变量并创建/删除测试文件。
	t.Setenv("GO_LUA_VM_HOST_ACCESS_TEST", "enabled")
	scriptPath := filepath.Join(t.TempDir(), "host-access.tmp")
	allowedState := NewStateWithOptions(Options{AllowEnvironment: true, AllowHostFilesystem: true})
	defer allowedState.Close()
	if err := OpenLibs(allowedState); err != nil {
		// 授权 State 打开标准库不应失败。
		t.Fatalf("OpenLibs allowed failed: %v", err)
	}
	err = DoString(allowedState, `
assert(os.getenv("GO_LUA_VM_HOST_ACCESS_TEST") == "enabled")
local file = assert(io.open(`+quoteLuaString(scriptPath)+`, "w"))
assert(file:write("ok"))
assert(file:close())
assert(os.remove(`+quoteLuaString(scriptPath)+`))
`)
	if err != nil {
		// 显式授权后官方 CLI 类宿主访问应可运行。
		t.Fatalf("allowed host access failed: %v", err)
	}
}

// TestLocalAssignmentExpandsTrailingMethodCall 验证 local 赋值会展开尾部冒号方法调用多返回值。
//
// Lua 5.3 中 `local a,b,c = obj:method()` 必须像普通函数调用一样按剩余局部变量数量接收多返回值。
func TestLocalAssignmentExpandsTrailingMethodCall(t *testing.T) {
	// 创建完整 State，确保方法调用和多返回赋值都经过真实编译与 VM 执行路径。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册不应影响本测试。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := DoString(state, `
local object = {}
function object:seek()
  return nil, "message", 1
end
local status, message, code = object:seek()
assert(status == nil and message == "message" and code == 1)
`); err != nil {
		// 尾部方法调用必须展开到所有 local 目标。
		t.Fatalf("method call local assignment failed: %v", err)
	}
}

// TestDoStringLoopLocalUpvalueClosesEachIteration 验证循环体局部 upvalue 每轮独立闭合。
//
// Lua 5.3 closure.lua 会在 numeric for 中创建捕获块内 local 的闭包；每轮结束时必须通过
// OP_JMP close 关闭该 local 的 open upvalue，否则后续迭代或后续 local 复用寄存器会污染旧闭包。
func TestDoStringLoopLocalUpvalueClosesEachIteration(t *testing.T) {
	// 使用完整标准库执行 assert/collectgarbage，覆盖官方 closure.lua 的关键失败形态。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行 Lua 断言和 GC。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := DoString(state, `
local A, B = 0, {g = 10}
function f(x)
  local a = {}
  for i = 1, 10 do
    local y = 0
    do
      a[i] = function ()
        B.g = B.g + 1
        y = y + x
        return y + A
      end
    end
  end
  local dummy = function () return a[A] end
  collectgarbage()
  A = 1; assert(dummy() == a[1]); A = 0
  assert(a[1]() == x)
  assert(a[1]() == x + x)
  assert(a[3]() == x)
  collectgarbage()
  assert(B.g == 13)
end
f(10)
`); err != nil {
		// 任一断言失败都说明 loop-local upvalue 生命周期与官方 Lua 5.3 不一致。
		t.Fatalf("loop local upvalue close failed: %v", err)
	}
}

// TestDoStringBreakClosesCapturedForControlVariable 验证 break 会闭合被捕获的 for 控制变量。
//
// 官方 closure.lua 要求每轮 numeric-for 可见控制变量拥有独立 upvalue cell；break 跳出循环时
// 也必须携带 OP_JMP close 参数，否则 break 会跳过循环体末尾 close-only JMP，导致前几轮闭包共享
// 同一个寄存器 cell。
func TestDoStringBreakClosesCapturedForControlVariable(t *testing.T) {
	// 打开标准库以使用 assert，脚本直接复现官方 closure.lua 第 60-70 行的控制变量场景。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行 Lua 断言。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := DoString(state, `
local a = {}
for i = 1, 10 do
  a[i] = {
    set = function(x) i = x end,
    get = function() return i end,
  }
  if i == 3 then break end
end
assert(a[4] == nil)
a[1].set(10)
assert(a[2].get() == 2)
a[2].set("a")
assert(a[3].get() == 3)
assert(a[2].get() == "a")
`); err != nil {
		// break 必须关闭当前迭代的控制变量，不得让 a[2] 的写入污染 a[3]。
		t.Fatalf("break for-control upvalue close failed: %v", err)
	}
}

// TestCoroutineLibraryRunsLuaClosureToYield 验证 coroutine 标准库能执行 Lua closure 到 yield。
//
// 该用例覆盖官方 gc.lua 的 self-referenced threads 前置需求：create/resume/yield 全局可用，
// 且 Lua closure 在协程中能读取外层 upvalue 并把 yield 值返回给 resume。
func TestCoroutineLibraryRunsLuaClosureToYield(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local thread_id = 0
local threads = {}
local function fn(thread)
  local x = {}
  threads[thread_id] = function() thread = x end
  return coroutine.yield(100)
end
local co = coroutine.create(fn)
local ok, value = coroutine.resume(co, co)
assert(ok and value == 100)
assert(type(threads[0]) == "function")
assert(coroutine.status(co) == "suspended")
`)
	if err != nil {
		// create/resume/yield 任一环节失败都会暴露为脚本执行错误。
		t.Fatalf("coroutine script failed: %v", err)
	}
}

// TestCoroutineIsYieldableMatchesMainCoroutineAndGoCallback 验证 coroutine.isyieldable 的可见语义。
//
// 官方 coroutine.lua 在开头要求主线程不可 yield、Lua 协程内部可 yield、普通 Go/C 回调边界内
// 不可 yield；该测试覆盖三类上下文，避免库函数缺失或边界判断回退。
func TestCoroutineIsYieldableMatchesMainCoroutineAndGoCallback(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.isyieldable。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
assert(coroutine.isyieldable() == false)
local co = coroutine.wrap(function ()
  assert(coroutine.isyieldable() == true)
  coroutine.yield("yieldable")
  return coroutine.isyieldable()
end)
assert(co() == "yieldable")
assert(co() == true)
local value = string.gsub("a", ".", function (c)
  assert(coroutine.isyieldable() == false)
  return c .. c
end)
assert(value == "aa")
`)
	if err != nil {
		// 任一上下文判断失败都会暴露为脚本错误。
		t.Fatalf("coroutine.isyieldable script failed: %v", err)
	}
}

// TestDoStringCoroutineYieldOutsideCoroutineMessage 验证主线程 yield 错误文本。
//
// Lua 5.3 官方 errors.lua 要求 `coroutine.yield()` 在主线程返回包含 `outside a coroutine` 的错误。
func TestDoStringCoroutineYieldOutsideCoroutineMessage(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// coroutine.yield 与 assert/string.find 来自标准库，必须先打开。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	err := DoString(state, `
local ok, message = pcall(coroutine.yield)
assert(not ok)
assert(string.find(message, "outside a coroutine", 1, true))
`)
	if err != nil {
		// 主线程 yield 文本必须匹配官方断言。
		t.Fatalf("DoString coroutine yield outside message failed: %v", err)
	}
}

// TestCoroutineTailCallYieldDoesNotCorruptOuterForRegisters 验证尾调用 yield 不污染外层 for 寄存器。
//
// 官方 coroutine.lua 的 `return coroutine.yield(i)` 会让内层函数通过尾调用挂起；恢复时必须把
// resume 实参作为该函数调用结果写回，而不能用内层 tail-call VM 的局部快照覆盖外层 numeric-for
// 控制寄存器，否则下一轮 FORLOOP 会报 number expected。
func TestCoroutineTailCallYieldDoesNotCorruptOuterForRegisters(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine tail-call yield。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local x
local function foo(i)
  return coroutine.yield(i)
end
local f = coroutine.wrap(function ()
  for i = 1, 10 do
    assert(foo(i) == x)
  end
  return "done"
end)
for i = 1, 10 do
  x = i
  assert(f(i) == i)
end
x = "done"
assert(f("done") == "done")
`)
	if err != nil {
		// 恢复后外层 numeric for 控制寄存器必须保持数值。
		t.Fatalf("tail-call yield corrupted outer for registers: %v", err)
	}
}

// TestCoroutineNestedWrapSieveDoesNotRestoreParentFrames 验证嵌套 wrap 链不会恢复父协程帧。
//
// 官方 coroutine.lua 的素数筛会构造多层 coroutine.wrap 迭代器；子协程 yield 时保存的 traceback
// 包含父协程经 Go wrapper 进入的边界，continuation 恢复必须在 Go 边界截断，否则外层帧会指数膨胀
// 并触发 C stack overflow。
func TestCoroutineNestedWrapSieveDoesNotRestoreParentFrames(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.wrap 链。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
function gen(n)
  return coroutine.wrap(function ()
    for i = 2, n do coroutine.yield(i) end
  end)
end
function filter(p, g)
  return coroutine.wrap(function ()
    while true do
      local n = g()
      if n == nil then return end
      if math.fmod(n, p) ~= 0 then coroutine.yield(n) end
    end
  end)
end
local x = gen(100)
local a = {}
while true do
  local n = x()
  if n == nil then break end
  table.insert(a, n)
  x = filter(n, x)
end
assert(#a == 25 and a[#a] == 97)
`)
	if err != nil {
		// 多层 wrap 链应正常终止并产出 100 以内素数。
		t.Fatalf("nested coroutine.wrap sieve failed: %v", err)
	}
}

// TestCoroutineTableSortComparatorCannotYield 验证 table.sort comparator 不可跨 Go/C 边界 yield。
//
// 官方 coroutine.lua 会在协程中用 pcall 包住 table.sort(..., coroutine.yield)；该 yield 必须
// 作为 comparator 边界错误被捕获，且不能把当前 coroutine 切回 suspended，否则后续
// coroutine.isyieldable 与 coroutine.yield 都会失真。
func TestCoroutineTableSortComparatorCannotYield(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 table.sort comparator 边界。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local co = coroutine.wrap(function ()
  assert(not pcall(table.sort, {1, 2, 3}, coroutine.yield))
  assert(coroutine.isyieldable())
  coroutine.yield(20)
  return 30
end)
assert(co() == 20)
assert(co() == 30)
`)
	if err != nil {
		// comparator 里的 yield 不应挂起当前协程。
		t.Fatalf("table.sort comparator yield boundary failed: %v", err)
	}
}

// TestCoroutinePCallXPCallContinuationCapturesFinalError 验证 pcall/xpcall 跨 yield 的恢复语义。
//
// 官方 coroutine.lua 会用 xpcall(pcall, handler, yieldingFunction) 包住一个多次 yield 后再抛出
// table error object 的函数；恢复链必须先回到 pcall 捕获最终错误，再由 xpcall 成功返回。
func TestCoroutinePCallXPCallContinuationCapturesFinalError(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 protected-call continuation。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local f = function (_, i) return coroutine.yield(i) end
local wrapped = coroutine.wrap(function ()
  return xpcall(pcall, function (...) return ... end, function ()
    local s = 0
    for i in f, nil, 1 do
      pcall(function () s = s + i end)
    end
    error({s})
  end)
end)
assert(wrapped() == 1)
for i = 1, 10 do
  assert(wrapped(i) == i)
end
local ok, protectedOK, errorObject = wrapped(nil)
assert(ok and not protectedOK and errorObject[1] == 55)
`)
	if err != nil {
		// pcall 必须在 yield 后捕获最终 table error object，xpcall 外层必须视为成功。
		t.Fatalf("pcall/xpcall continuation failed: %v", err)
	}
}

// TestCoroutineStringGSubReplacementCannotYield 验证 string.gsub replacement 不可跨 Go 回调边界 yield。
//
// 官方 coroutine.lua 会在协程里调用 string.gsub，并在 Lua replacement 中断言
// coroutine.isyieldable() 为 false；该边界等价 Lua 5.3 的 C 回调，不能允许 coroutine.yield 穿透。
func TestCoroutineStringGSubReplacementCannotYield(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 string.gsub replacement 边界。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local function replacement(c)
  assert(not coroutine.isyieldable())
  return c .. c
end
local wrapped = coroutine.wrap(function ()
  assert(coroutine.isyieldable())
  return string.gsub("a", ".", replacement)
end)
local value, count = wrapped()
assert(value == "aa" and count == 1)
`)
	if err != nil {
		// gsub replacement 中的 Lua closure 必须不可 yield，且替换结果仍要正常返回。
		t.Fatalf("string.gsub replacement yield boundary failed: %v", err)
	}
}

// TestCoroutineResumePreservesLuaErrorObject 验证 coroutine.resume 保留 Lua error object。
//
// Lua 5.3 中协程内 `error(functionValue)` 后，coroutine.resume 应返回 false 和原始函数对象，
// 而不是把错误对象降级成字符串消息。
func TestCoroutineResumePreservesLuaErrorObject(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.resume 错误对象。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local function marker() end
local co = coroutine.create(function ()
  coroutine.yield(1)
  error(marker)
end)
local ok, value = coroutine.resume(co)
assert(ok and value == 1)
ok, value = coroutine.resume(co)
assert(not ok and value == marker and coroutine.status(co) == "dead")
`)
	if err != nil {
		// resume 错误返回值必须保持 Lua error object identity。
		t.Fatalf("coroutine.resume error object preservation failed: %v", err)
	}
}

// TestCoroutineRecursiveLuaCallContinuationRestoresAllFrames 验证递归 Lua 调用跨 yield 恢复全部调用栈。
//
// 官方 coroutine.lua 使用递归函数 all({}, 5, 4) 生成 5^4 个组合；每次 yield 后都必须恢复
// 所有父级 Lua CALL continuation，且不能用旧快照覆盖父级 numeric-for 控制寄存器。
func TestCoroutineRecursiveLuaCallContinuationRestoresAllFrames(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.wrap 递归恢复。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local function all(a, n, k)
  if k == 0 then
    coroutine.yield(a)
  else
    for i = 1, n do
      a[k] = i
      all(a, n, k - 1)
    end
  end
end
local count = 0
for _ in coroutine.wrap(function () all({}, 5, 4) end) do
  count = count + 1
end
assert(count == 5^4)
`)
	if err != nil {
		// 递归多层 continuation 必须完整恢复到每一层 FORLOOP。
		t.Fatalf("recursive coroutine continuation failed: %v", err)
	}
}

// TestCoroutineWrapNestedYieldRestoresParentThread 验证嵌套 coroutine.wrap yield 后恢复父协程。
//
// 官方 big.lua 会在外层 loadfile 协程中再执行 coroutine.wrap(f)，内层协程 yield 返回后，
// 外层脚本末尾仍需要能继续 coroutine.yield；若运行态错误恢复到主线程，会报 main thread cannot yield。
func TestCoroutineWrapNestedYieldRestoresParentThread(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.wrap。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local function inner()
  coroutine.yield("inner-yield")
  return "inner-done"
end

local function outer()
  local wrapped = coroutine.wrap(inner)
  assert(wrapped() == "inner-yield")
  coroutine.yield("outer-yield")
  assert(wrapped() == "inner-done")
  return "outer-done"
end

local wrappedOuter = coroutine.wrap(outer)
assert(wrappedOuter() == "outer-yield")
assert(wrappedOuter() == "outer-done")
`)
	if err != nil {
		// 内层 wrap yield 后必须恢复外层协程，否则 outer 的 yield 会被判定为主线程 yield。
		t.Fatalf("nested coroutine.wrap script failed: %v", err)
	}
}

// TestCoroutineWrapDofileCanYield 验证 coroutine.wrap(dofile) 执行文件 chunk 时允许 yield。
//
// Lua 5.3 官方 files.lua 会把 dofile 作为协程入口，文件 chunk 内连续 yield 两次并在第三次
// resume 后返回计算结果；dofile 只是 Lua chunk trampoline，不应被视为普通不可 yield 的 Go 回调。
func TestCoroutineWrapDofileCanYield(t *testing.T) {
	state := NewStateWithOptions(Options{AllowHostFilesystem: true})
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 dofile 与 coroutine.wrap 组合。
		t.Fatalf("open libraries failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "yield_dofile.lua")
	if err := os.WriteFile(path, []byte("local x, z = coroutine.yield(10)\nlocal y = coroutine.yield(20)\nreturn x + y * z\n"), 0o600); err != nil {
		// 测试 chunk 必须写入成功。
		t.Fatalf("write dofile chunk failed: %v", err)
	}

	script := fmt.Sprintf(`
local f = coroutine.wrap(dofile)
assert(f(%q) == 10)
assert(f(100, 101) == 20)
assert(f(200) == 100 + 200 * 101)
`, path)
	if err := DoString(state, script); err != nil {
		// dofile 内部 yield 必须按 coroutine.wrap 协议恢复。
		t.Fatalf("coroutine.wrap(dofile) failed: %v", err)
	}
}

// TestCoroutineWrapSelfResumeReportsNonSuspended 验证 wrap 内自递归 resume 的错误文本。
//
// 官方 coroutine.lua 的历史回归用例会在 wrap 协程内部通过 `pcall(A, 1)` 再次调用同一个
// wrapper。该路径必须被 pcall 捕获为 `cannot resume non-suspended coroutine`，而不是泄漏
// 实现内部的 running 状态描述。
func TestCoroutineWrapSelfResumeReportsNonSuspended(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.wrap。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local A
A = coroutine.wrap(function() return pcall(A, 1) end)
local st, res = A()
assert(not st and string.find(res, "non%-suspended"))
`)
	if err != nil {
		// 错误文本不兼容会在 string.find 断言处失败。
		t.Fatalf("self resume wrap script failed: %v", err)
	}
}

// TestCoroutineStatusReportsNormalForParentCoroutine 验证父协程调用子协程时显示 normal。
//
// Lua 5.3 中协程 A resume 后调用协程 B，B 运行期间查询 A 的状态应为 `normal`，且尝试
// resume A 必须失败；该语义用于区分当前 running 协程和调用链上的非当前协程。
func TestCoroutineStatusReportsNormalForParentCoroutine(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.status。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local co1, co2
co1 = coroutine.create(function () return co2() end)
co2 = coroutine.wrap(function ()
  assert(coroutine.status(co1) == "normal")
  assert(not coroutine.resume(co1))
  coroutine.yield(3)
end)
local ok, value = coroutine.resume(co1)
assert(ok and value == 3)
assert(coroutine.status(co1) == "dead")
`)
	if err != nil {
		// 父协程状态或 normal resume 拒绝语义不兼容会触发断言。
		t.Fatalf("parent coroutine normal status script failed: %v", err)
	}
}

// TestCoroutineResumeFailsWhenUnpackReturnsTooManyValues 验证协程返回超大 unpack 结果会触发栈限制。
//
// 官方 coroutine.lua 的 `bug (stack overflow)` 小节会让协程 `return table.unpack(t)` 返回接近
// LUAI_MAXSTACK 的结果数量。该路径必须在展开返回值前失败，避免构造超大返回切片并误报成功。
func TestCoroutineResumeFailsWhenUnpackReturnsTooManyValues(t *testing.T) {
	state := NewStateWithOptions(Options{MaxStackDepth: 24})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 table.unpack。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local co = coroutine.create(function ()
  local t = {}
  for i = 1, 20 do t[i] = i end
  return table.unpack(t)
end)
local ok, message = coroutine.resume(co)
assert(not ok and string.find(message, "stack overflow"))
`)
	if err != nil {
		// unpack 返回结果没有触发栈限制时会在断言处失败。
		t.Fatalf("oversized unpack coroutine script failed: %v", err)
	}
}

// TestCoroutineYieldDoesNotLeakGoDebugFrames 验证大量 coroutine.yield 不泄漏 Go 调试帧。
//
// 官方 gc.lua 会连续创建并 resume 大量挂起协程；coroutine.yield 的 Go closure 帧必须在
// ErrCoroutineYield 返回后弹出，否则调用深度会累积到 C stack overflow。
func TestCoroutineYieldDoesNotLeakGoDebugFrames(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法测试 coroutine.yield。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local thread_id = 0
local threads = {}
local function fn(thread)
  local x = {}
  threads[thread_id] = function()
    thread = x
  end
  coroutine.yield()
end
while thread_id < 260 do
  local thread = coroutine.create(fn)
  assert(coroutine.resume(thread, thread))
  thread_id = thread_id + 1
end
`)
	if err != nil {
		// 若 yield Go 帧泄漏，默认 MaxCallDepth 会在循环中触发 C stack overflow。
		t.Fatalf("many coroutine yields failed: %v", err)
	}
	if state.CallDepth() != 0 {
		// 脚本结束后调用帧栈必须恢复为空。
		t.Fatalf("call depth leaked after coroutine yields: %d", state.CallDepth())
	}
}

// TestOpenIndividualLibraries 验证按库加载 API。
//
// 单库加载用于嵌入方按需构建沙箱环境；每个函数必须只依赖传入 State。
func TestOpenIndividualLibraries(t *testing.T) {
	// 每个用例使用新 State，避免全局环境互相污染。
	tests := []struct {
		name       string
		open       func(*State) error
		globalName string
	}{
		{name: "base", open: OpenBase, globalName: "_VERSION"},
		{name: "package", open: OpenPackage, globalName: "package"},
		{name: "table", open: OpenTable, globalName: "table"},
		{name: "string", open: OpenString, globalName: "string"},
		{name: "math", open: OpenMath, globalName: "math"},
		{name: "io", open: OpenIO, globalName: "io"},
		{name: "os", open: OpenOS, globalName: "os"},
		{name: "utf8", open: OpenUTF8, globalName: "utf8"},
		{name: "debug", open: OpenDebug, globalName: "debug"},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			// 单库加载必须注册对应全局符号。
			state := NewState()
			defer state.Close()
			if err := testCase.open(state); err != nil {
				t.Fatalf("%s open failed: %v", testCase.name, err)
			}
			value, err := GetGlobal(state, testCase.globalName)
			if err != nil {
				t.Fatalf("GetGlobal %s failed: %v", testCase.globalName, err)
			}
			if value.IsNil() {
				t.Fatalf("global %s should be registered", testCase.globalName)
			}
			if err := testCase.open(nil); !errors.Is(err, ErrNilState) {
				t.Fatalf("%s nil state should return ErrNilState, got=%v", testCase.name, err)
			}
		})
	}
}

// TestGetSetGlobalWrappers 验证 lua.GetGlobal 和 lua.SetGlobal 包装。
//
// 包装函数必须对 nil State 做错误防护，并保留 runtime 全局环境读写语义。
func TestGetSetGlobalWrappers(t *testing.T) {
	// nil State 读写都必须返回 ErrNilState。
	if _, err := GetGlobal(nil, "x"); !errors.Is(err, ErrNilState) {
		t.Fatalf("GetGlobal nil state should return ErrNilState, got=%v", err)
	}
	if err := SetGlobal(nil, "x", runtime.IntegerValue(1)); !errors.Is(err, ErrNilState) {
		t.Fatalf("SetGlobal nil state should return ErrNilState, got=%v", err)
	}

	state := NewState()
	defer state.Close()
	if err := SetGlobal(state, "answer", runtime.IntegerValue(42)); err != nil {
		// 有效 State 写入全局变量不应失败。
		t.Fatalf("SetGlobal failed: %v", err)
	}
	value, err := GetGlobal(state, "answer")
	if err != nil {
		// 有效 State 读取全局变量不应失败。
		t.Fatalf("GetGlobal failed: %v", err)
	}
	if integer, ok := value.ToInteger(); !ok || integer != 42 {
		// 读取值必须与写入值一致。
		t.Fatalf("GetGlobal answer = %#v", value)
	}
}

// TestValueAndFunctionAliases 验证 lua.Value 与 lua.Function 是稳定对外表示。
//
// Value 必须能直接承载 runtime.Value 语义；Function 必须支持 Go 回调多返回值和错误传播。
func TestValueAndFunctionAliases(t *testing.T) {
	// 使用 lua.Value 接收构造出的 runtime 值，确认对外表示不丢失类型和转换能力。
	var value Value = runtime.IntegerValue(53)
	if integer, ok := value.ToInteger(); !ok || integer != 53 {
		// Value 别名必须保留 runtime.Value 的整数转换语义。
		t.Fatalf("Value alias integer mismatch: value=%#v ok=%v", integer, ok)
	}

	// Function 是推荐的多返回 Go 回调签名。
	var function Function = func(args ...Value) ([]Value, error) {
		// 回调收到参数后返回两个 Lua 值，用于后续 bridge 层写回 Lua。
		return []Value{args[0], runtime.StringValue("ok")}, nil
	}
	results, err := function(runtime.StringValue("arg"))
	if err != nil {
		// 普通回调不应返回错误。
		t.Fatalf("Function alias call failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "arg" || results[1].String != "ok" {
		// Function 必须保留多返回值顺序。
		t.Fatalf("Function alias results = %#v", results)
	}
}

// TestCallAndRegisterGoFunctions 验证 Go 函数注册和调用 API。
//
// Register 必须把 Go 多返回回调写入全局环境；Call 必须能调用 Go closure，并对 Lua closure
// 返回当前阶段可识别的执行器未接入错误。
func TestCallAndRegisterGoFunctions(t *testing.T) {
	// nil State 和 nil function 必须在注册阶段返回明确错误。
	if err := Register(nil, "bad", func(args ...Value) ([]Value, error) {
		// 该回调不会执行，仅用于构造非 nil 函数。
		return nil, nil
	}); !errors.Is(err, ErrNilState) {
		t.Fatalf("Register nil state should return ErrNilState, got=%v", err)
	}

	state := NewState()
	defer state.Close()
	if err := Register(state, "twice", nil); !errors.Is(err, ErrExpectedCallable) {
		// nil Go 函数没有可调用目标。
		t.Fatalf("Register nil function should return ErrExpectedCallable, got=%v", err)
	}
	if err := Register(state, "twice", func(args ...Value) ([]Value, error) {
		// 测试函数读取第一个整数参数并返回翻倍结果和状态字符串。
		integerValue, ok := args[0].ToInteger()
		if !ok {
			// 参数不是整数时模拟 Lua 运行期错误，方便后续 bridge 层复用。
			return nil, RaiseError(runtime.StringValue("integer expected"))
		}
		return []Value{runtime.IntegerValue(integerValue * 2), runtime.StringValue("ok")}, nil
	}); err != nil {
		// 有效注册不应失败。
		t.Fatalf("Register failed: %v", err)
	}

	functionValue, err := GetGlobal(state, "twice")
	if err != nil {
		// 已注册全局函数应可读取。
		t.Fatalf("GetGlobal function failed: %v", err)
	}
	results, err := Call(state, functionValue, runtime.IntegerValue(21))
	if err != nil {
		// Go closure 调用路径当前阶段必须可用。
		t.Fatalf("Call Go closure failed: %v", err)
	}
	if len(results) != 2 {
		// Go 多返回回调必须保留返回值数量。
		t.Fatalf("Call result length = %d", len(results))
	}
	if got, ok := results[0].ToInteger(); !ok || got != 42 {
		// 第一个返回值必须是翻倍后的整数。
		t.Fatalf("Call first result = %#v", results[0])
	}
	if results[1].Kind != KindString || results[1].String != "ok" {
		// 第二个返回值必须保留字符串状态。
		t.Fatalf("Call second result = %#v", results[1])
	}

	singleValue := runtime.ReferenceValue(KindGoClosure, runtime.GoFunction(func(args ...Value) (Value, error) {
		// 单返回 GoFunction 应被 Call 包装成单元素结果列表。
		return runtime.StringValue("single"), nil
	}))
	singleResults, err := Call(state, singleValue)
	if err != nil {
		// 单返回 GoFunction 调用不应失败。
		t.Fatalf("Call GoFunction failed: %v", err)
	}
	if len(singleResults) != 1 || singleResults[0].String != "single" {
		// GoFunction 结果必须保留在单元素列表中。
		t.Fatalf("Call GoFunction results = %#v", singleResults)
	}

	if _, err := Call(nil, functionValue); !errors.Is(err, ErrNilState) {
		// nil State 调用必须被拒绝。
		t.Fatalf("Call nil state should return ErrNilState, got=%v", err)
	}
	if _, err := Call(state, runtime.IntegerValue(1)); !errors.Is(err, ErrExpectedCallable) {
		// 非函数值调用必须返回不可调用错误。
		t.Fatalf("Call non-function should return ErrExpectedCallable, got=%v", err)
	}
	if err := LoadString(state, "return 1", ""); err != nil {
		// 合法源码加载不应失败。
		t.Fatalf("LoadString failed: %v", err)
	}
	luaClosure := state.ValueAt(-1)
	luaResults, err := Call(state, luaClosure)
	if err != nil {
		// Lua closure 当前应通过最小 VM 执行循环返回结果。
		t.Fatalf("Call Lua closure failed: %v", err)
	}
	if len(luaResults) != 1 || luaResults[0].Kind != KindInteger || luaResults[0].Integer != 1 {
		// return 1 必须返回单个 integer 结果。
		t.Fatalf("Call Lua closure results = %#v", luaResults)
	}
	if err := LoadString(state, "return twice(21)", "tail-go.lua"); err != nil {
		// 合法尾调用源码加载不应失败。
		t.Fatalf("LoadString tail call failed: %v", err)
	}
	tailClosure := state.ValueAt(-1)
	tailResults, err := Call(state, tailClosure)
	if err != nil {
		// TAILCALL 到 Go closure 必须由执行器直接返回被调结果。
		t.Fatalf("Call Lua tail closure failed: %v", err)
	}
	if len(tailResults) != 2 || tailResults[0].Integer != 42 || tailResults[1].String != "ok" {
		// 尾调用必须保留 Go closure 的多返回结果。
		t.Fatalf("Call Lua tail closure results = %#v", tailResults)
	}
}

// TestPackageLoadLibCanBeOverriddenByHostLoader 验证宿主可接入第三方 C 动态库加载链路。
//
// 本仓库默认构建不引入 CGO，也不内置动态库打开逻辑；嵌入方可以在宿主程序或可选扩展中
// 实现自己的 C loader，再覆盖 package.loadlib。该测试用纯 Go loader 模拟第三方动态库入口，
// 验证 Lua 侧调用形态可用。
func TestPackageLoadLibCanBeOverriddenByHostLoader(t *testing.T) {
	// 创建完整标准库环境，确保 package 表和 Lua assert/type 等基础函数都可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法验证 package.loadlib 覆盖链路。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	packageValue, err := GetGlobal(state, "package")
	if err != nil {
		// package 全局读取失败表示 State 封装不可用。
		t.Fatalf("GetGlobal package failed: %v", err)
	}
	packageTable, ok := packageValue.Ref.(*runtime.Table)
	if packageValue.Kind != runtime.KindTable || !ok || packageTable == nil {
		// package 必须是 table，宿主才能覆盖 loadlib 字段。
		t.Fatalf("package global = %#v, want table", packageValue)
	}

	loadCount := 0
	packageTable.RawSetString("loadlib", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...Value) ([]Value, error) {
		// 宿主自定义 loader 接收官方 loadlib 形态的 filename 和 symbol 参数。
		if len(args) != 2 || args[0].String != "third_party/libdemo.so" || args[1].String != "luaopen_demo" {
			// 参数不符合预期时返回 Lua error，避免测试误判默认 loader。
			return nil, RaiseError(runtime.StringValue("unexpected dynamic library request"))
		}
		loadCount++
		loader := runtime.GoResultsFunction(func(args ...Value) ([]Value, error) {
			// 模拟第三方 C 模块 luaopen_* 返回模块值；真实宿主可在这里桥接 C 动态库。
			return []Value{runtime.StringValue("third-party-c-loader:luaopen_demo")}, nil
		})
		return []Value{runtime.ReferenceValue(runtime.KindGoClosure, loader)}, nil
	})))

	err = DoString(state, `
		local loader = assert(package.loadlib("third_party/libdemo.so", "luaopen_demo"))
		assert(type(loader) == "function")
		assert(loader() == "third-party-c-loader:luaopen_demo")
	`)
	if err != nil {
		// Lua 侧必须能通过覆盖后的 package.loadlib 获取并执行宿主 loader。
		t.Fatalf("DoString host loadlib override failed: %v", err)
	}
	if loadCount != 1 {
		// 自定义 loader 必须被调用一次，证明链路没有落回默认禁用策略。
		t.Fatalf("host loader call count = %d, want 1", loadCount)
	}
}

// TestPackageDynamicLibraryLoaderOption 验证 lua.Options 可为 OpenLibs 注入动态库 loader。
//
// 该入口保持默认构建无 CGO；宿主通过纯 Go 回调接管 package.loadlib 的 filename/symbol 解析。
func TestPackageDynamicLibraryLoaderOption(t *testing.T) {
	// 创建带动态库 loader 的 State，模拟宿主程序接入平台相关加载层。
	loadCount := 0
	state := NewStateWithOptions(Options{
		PackageDynamicLibraryLoader: func(filename string, symbol string) (runtime.Value, error) {
			if filename != "libdemo.dylib" || symbol != "luaopen_demo" {
				// 参数不符合预期时返回普通错误，loadlib 会转换为 nil,error,open。
				return runtime.NilValue(), fmt.Errorf("unexpected dynamic library request")
			}
			loadCount++
			loader := runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
				// 模拟动态库入口返回模块值。
				return []runtime.Value{runtime.StringValue("options-dynamic-loader")}, nil
			})
			return runtime.ReferenceValue(runtime.KindGoClosure, loader), nil
		},
	})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法验证 Options 注入链路。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	err := DoString(state, `
		local loader = assert(package.loadlib("libdemo.dylib", "luaopen_demo"))
		assert(loader() == "options-dynamic-loader")
	`)
	if err != nil {
		// Lua 侧必须能通过 Options 注入的 loader 获取动态库入口。
		t.Fatalf("DoString options dynamic loader failed: %v", err)
	}
	if loadCount != 1 {
		// loader 必须只被 package.loadlib 调用一次。
		t.Fatalf("options dynamic loader call count = %d, want 1", loadCount)
	}
}

// TestPackageDynamicLibraryLoaderForStateOption 验证 lua.Options 可为当前 State 创建动态库 loader。
//
// Lua C API shim 需要在 luaopen_* loader 执行时访问真实 State；该入口让 native_modules 构建
// 可以在 package 库注册阶段绑定当前 State，同时保留默认无 CGO 构建不启用动态库加载。
func TestPackageDynamicLibraryLoaderForStateOption(t *testing.T) {
	// 创建带状态感知动态库 loader 的 State，并同时设置无状态 loader 以验证优先级。
	statefulLoadCount := 0
	fallbackLoadCount := 0
	var capturedState *runtime.State
	var state *State
	state = NewStateWithOptions(Options{
		PackageDynamicLibraryLoader: func(filename string, symbol string) (runtime.Value, error) {
			// 状态感知 loader 存在时不应落回无状态 loader。
			fallbackLoadCount++
			return runtime.NilValue(), fmt.Errorf("fallback dynamic loader should not be used")
		},
		PackageDynamicLibraryLoaderForState: func(loaderState *runtime.State) func(filename string, symbol string) (runtime.Value, error) {
			// 工厂必须接收当前正在注册 package 库的 State。
			capturedState = loaderState
			return func(filename string, symbol string) (runtime.Value, error) {
				if filename != "libstateful.so" || symbol != "luaopen_stateful" {
					// 参数不符合预期时返回普通错误，loadlib 会转换为 nil,error,open。
					return runtime.NilValue(), fmt.Errorf("unexpected stateful dynamic library request")
				}
				statefulLoadCount++
				loader := runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
					// 模拟状态绑定的 luaopen_* loader 返回模块值。
					return []runtime.Value{runtime.StringValue("stateful-dynamic-loader")}, nil
				})
				return runtime.ReferenceValue(runtime.KindGoClosure, loader), nil
			}
		},
	})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册失败时无法验证 Options 注入链路。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if capturedState != (*runtime.State)(state) {
		// 工厂必须拿到当前 State，后续 native lua_State* handle 才能绑定正确运行时。
		t.Fatalf("captured state = %p, want %p", capturedState, (*runtime.State)(state))
	}

	err := DoString(state, `
		local loader = assert(package.loadlib("libstateful.so", "luaopen_stateful"))
		assert(loader() == "stateful-dynamic-loader")
	`)
	if err != nil {
		// Lua 侧必须能通过状态感知 Options loader 获取动态库入口。
		t.Fatalf("DoString stateful dynamic loader failed: %v", err)
	}
	if statefulLoadCount != 1 {
		// 状态感知 loader 必须被调用一次。
		t.Fatalf("stateful dynamic loader call count = %d, want 1", statefulLoadCount)
	}
	if fallbackLoadCount != 0 {
		// 状态感知 loader 优先时，无状态 loader 不应被调用。
		t.Fatalf("fallback dynamic loader call count = %d, want 0", fallbackLoadCount)
	}
}

// TestPushAndToWrappers 验证 lua 包栈压入和类型转换 API。
//
// Push 系列必须把值写入 State 栈；To 系列必须按 Lua 5.3 基础类型语义读取栈值。
func TestPushAndToWrappers(t *testing.T) {
	// nil State 的 Push 和 ToValue 都必须返回 ErrNilState。
	if err := Push(nil, runtime.NilValue()); !errors.Is(err, ErrNilState) {
		t.Fatalf("Push nil state should return ErrNilState, got=%v", err)
	}
	if _, err := ToValue(nil, 1); !errors.Is(err, ErrNilState) {
		t.Fatalf("ToValue nil state should return ErrNilState, got=%v", err)
	}

	state := NewState()
	defer state.Close()
	if err := PushNil(state); err != nil {
		// nil 压栈不应失败。
		t.Fatalf("PushNil failed: %v", err)
	}
	if err := PushBoolean(state, false); err != nil {
		// boolean 压栈不应失败。
		t.Fatalf("PushBoolean failed: %v", err)
	}
	if err := PushInteger(state, 53); err != nil {
		// integer 压栈不应失败。
		t.Fatalf("PushInteger failed: %v", err)
	}
	if err := PushNumber(state, 2.5); err != nil {
		// number 压栈不应失败。
		t.Fatalf("PushNumber failed: %v", err)
	}
	if err := PushString(state, "lua"); err != nil {
		// string 压栈不应失败。
		t.Fatalf("PushString failed: %v", err)
	}

	value, err := ToValue(state, -1)
	if err != nil {
		// 有效 State 读取栈顶不应失败。
		t.Fatalf("ToValue failed: %v", err)
	}
	if value.Kind != KindString || value.String != "lua" {
		// 栈顶必须是最后压入的字符串。
		t.Fatalf("ToValue top = %#v", value)
	}
	booleanValue, ok, err := ToBoolean(state, 1)
	if err != nil || !ok || booleanValue {
		// Lua nil 在条件判断中为 false。
		t.Fatalf("ToBoolean nil = value=%v ok=%v err=%v", booleanValue, ok, err)
	}
	booleanValue, ok, err = ToBoolean(state, 3)
	if err != nil || !ok || !booleanValue {
		// Lua integer 53 在条件判断中为 true。
		t.Fatalf("ToBoolean integer = value=%v ok=%v err=%v", booleanValue, ok, err)
	}
	integerValue, ok, err := ToInteger(state, 3)
	if err != nil || !ok || integerValue != 53 {
		// integer 栈值必须可读为 int64。
		t.Fatalf("ToInteger = value=%d ok=%v err=%v", integerValue, ok, err)
	}
	numberValue, ok, err := ToNumber(state, 4)
	if err != nil || !ok || numberValue != 2.5 {
		// float number 栈值必须可读为 float64。
		t.Fatalf("ToNumber = value=%g ok=%v err=%v", numberValue, ok, err)
	}
	stringValue, ok, err := ToString(state, 3)
	if err != nil || !ok || stringValue != "53" {
		// integer 的基础 tostring 应输出十进制整数。
		t.Fatalf("ToString integer = value=%q ok=%v err=%v", stringValue, ok, err)
	}
	stringValue, ok, err = ToString(state, -1)
	if err != nil || !ok || stringValue != "lua" {
		// string 的基础 tostring 返回自身内容。
		t.Fatalf("ToString string = value=%q ok=%v err=%v", stringValue, ok, err)
	}
}

// TestErrorExports 验证 lua 包导出的错误类型和分类 helper。
//
// 嵌入方应可只依赖 lua 包完成 RuntimeError 识别、Lua error object 提取和资源限制分类。
func TestErrorExports(t *testing.T) {
	// RaiseError 必须保留 Lua error object。
	err := RaiseError(runtime.StringValue("boom"))
	if !errors.Is(err, ErrLuaError) {
		// Lua error 错误链必须支持 ErrLuaError 判断。
		t.Fatalf("RaiseError should wrap ErrLuaError, got=%v", err)
	}
	if !IsRuntimeError(err) {
		// Lua error 应被归类为 runtime error。
		t.Fatalf("RaiseError should be runtime error")
	}
	if ClassifyError(err) != ErrorClassRuntime {
		// RuntimeError 分类必须对外可见。
		t.Fatalf("runtime error class mismatch: %s", ClassifyError(err))
	}
	object := ErrorObject(err)
	if object.Kind != KindString || object.String != "boom" {
		// ErrorObject 必须取回原始 Lua 错误对象。
		t.Fatalf("ErrorObject mismatch: %#v", object)
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		// RuntimeError 类型别名必须支持 errors.As。
		t.Fatalf("RaiseError should expose RuntimeError")
	}

	limitErr := &ResourceLimitError{Kind: ResourceLimitStack, Limit: 1, Actual: 2, Message: "stack overflow"}
	if !IsResourceLimitError(limitErr) {
		// ResourceLimitError 必须可通过 lua 包 helper 识别。
		t.Fatalf("resource limit should be recognized")
	}
	if ClassifyError(limitErr) != ErrorClassResourceLimit {
		// 资源限制分类必须优先于普通 runtime 分类。
		t.Fatalf("resource limit class mismatch: %s", ClassifyError(limitErr))
	}
	if IsRuntimeError(limitErr) {
		// 资源限制错误不应混入普通 runtime error。
		t.Fatalf("resource limit should not be plain runtime error")
	}
}

// TestDoStringLuaStackOverflowHasSourceLine 验证 Lua 递归栈溢出携带源码位置。
//
// Lua 5.3 官方 errors.lua 的 checkstackmessage 要求 `source:line: stack overflow`，不能把底层
// Go 调用深度限制的 `C stack overflow` 直接暴露给 Lua 错误对象。
func TestDoStringLuaStackOverflowHasSourceLine(t *testing.T) {
	// 使用较小调用深度快速触发递归溢出，避免测试依赖默认上限的长耗时。
	state := NewStateWithOptions(Options{MaxCallDepth: 24})
	defer state.Close()

	err := DoString(state, `
local function y()
  local value = y()
  return value
end
y()
`)
	if err == nil {
		// 无限递归必须被调用深度限制拦截。
		t.Fatalf("DoString recursive call should fail")
	}
	message := runtime.ErrorObject(err).String
	if !strings.Contains(message, ": stack overflow") || strings.Contains(message, "C stack overflow") {
		// Lua 可见错误对象必须符合官方 source:line 格式，并隐藏底层 C/Go 调用深度细节。
		t.Fatalf("stack overflow message mismatch: %q", message)
	}
}

// TestDoStringCoroutineStackOverflowKeepsCStackText 验证协程递归溢出保留 C stack overflow 文本。
//
// Lua 5.3 官方 errors.lua 对 coroutine.create/resume 递归的错误断言是 `C stack overflow`；
// 该路径不同于主线程 Lua 递归，不能被重写成 `source:line: stack overflow`。
func TestDoStringCoroutineStackOverflowKeepsCStackText(t *testing.T) {
	state := NewStateWithOptions(Options{MaxCallDepth: 24})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// coroutine 与 string.find 来自标准库，必须先打开。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	err := DoString(state, `
local function f()
  local c = coroutine.create(f)
  local ok, message = coroutine.resume(c)
  return message
end
local message = f()
assert(string.find(message, "C stack overflow"))
`)
	if err != nil {
		// 协程递归溢出必须保留官方期望文本。
		t.Fatalf("DoString coroutine stack overflow failed: %v", err)
	}
}

// TestDoStringXPCallStackOverflowTracebackLines 验证 xpcall(debug.traceback) 可读取栈溢出现场。
//
// 官方 errors.lua 要求深递归栈溢出时，traceback 先重复递归函数调用行，再出现错误处理器外层
// 当前行；同时 error handler 自身抛 nil 时，xpcall 返回的错误对象必须是 string。
func TestDoStringXPCallStackOverflowTracebackLines(t *testing.T) {
	state := NewStateWithOptions(Options{MaxCallDepth: 48})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// xpcall、debug.traceback、string/table 库均来自标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local res, msg = xpcall(error, error)
assert(not res and type(msg) == "string")
C = 0
local l = debug.getinfo(1, "l").currentline; function y () C=C+1; y() end
local l1
local function g(x)
  l1 = debug.getinfo(x, "l").currentline; y()
end
local _, stackmsg = xpcall(g, debug.traceback, 1)
local stack = {}
for line in string.gmatch(stackmsg, "[^\n]*") do
  local curr = string.match(line, ":(%d+):")
  if curr then table.insert(stack, tonumber(curr)) end
end
local i=1
while stack[i] ~= l1 do
  assert(stack[i] == l)
  i = i+1
end
assert(i > 15)
`
	if err := DoString(state, source); err != nil {
		// traceback 行号顺序或 handler 错误对象类型不兼容时会触发 Lua assert。
		t.Fatalf("DoString xpcall stack overflow traceback lines failed: %v", err)
	}
}

// TestDoStringTableUnpackTooManyResultsMessage 验证默认栈上限下 table.unpack 巨大区间错误文本。
//
// 官方 errors.lua 在默认资源限制下使用空表和接近 LUAI_MAXSTACK 的上界检查错误文本，期望
// 包含 `too many results`；这不同于人为缩小 MaxStackDepth 的协程返回栈限制。
func TestDoStringTableUnpackTooManyResultsMessage(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// table.unpack 和 string.find 来自标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function f ()
  for i = 999900, 1000000, 1 do table.unpack({}, 1, i) end
end
local ok, message = pcall(f)
assert(not ok and string.find(message, "too many results"))
`
	if err := DoString(state, source); err != nil {
		// 默认上限附近的 table.unpack 巨大区间必须返回官方期望文本。
		t.Fatalf("DoString table.unpack too many results failed: %v", err)
	}
}

// TestDoStringContinueAndSwitchExtensions 验证扩展 continue 与 switch/case/default 的运行期行为。
//
// 该测试覆盖 while 内 continue、switch 多值 case、default 分支，以及 case 作为普通变量名的兼容性。
func TestDoStringContinueAndSwitchExtensions(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxContinue | SyntaxSwitch) {
		// 当前构建未编译控制流扩展时跳过正向运行期用例。
		t.Skip("control-flow syntax extensions are not compiled")
	}
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// assert 来自 base 标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local i = 0
local out = 0
local case = 100
while i < 5 do
  i = i + 1
  if i == 2 then
    continue
  end
  switch i do
  case 1, 3
    out = out + i
  default
    out = out + 10
  end
end
assert(case == 100)
assert(out == 24)
`
	if err := DoString(state, source); err != nil {
		// continue 和 switch 扩展语法必须按预期执行。
		t.Fatalf("DoString continue/switch extensions failed: %v", err)
	}
}

// TestDoStringRejectsDuplicateSwitchCaseValue 验证 GLua switch 会拒绝重复 case 值。
func TestDoStringRejectsDuplicateSwitchCaseValue(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxSwitch) {
		// 当前构建未编译 switch 扩展时跳过扩展语法用例。
		t.Skip("switch syntax extension is not compiled")
	}
	state := NewState()
	defer state.Close()

	err := DoString(state, "switch 1 do\ncase 1, 2\nprint('x')\ncase 2\nprint('y')\nend\n")
	if err == nil {
		// 重复 case 值必须在编译阶段被拒绝，CLI/IDE 才能一致报错。
		t.Fatalf("DoString should reject duplicate switch case value")
	}
	if !strings.Contains(err.Error(), "duplicate switch case value") {
		// 错误文本应能指导用户定位重复 case。
		t.Fatalf("error = %v", err)
	}
}

// TestDoStringLua53SyntaxDisablesExtensions 验证 Go API 可关闭扩展语法。
//
// 关闭扩展后 continue 和 switch 应按普通标识符处理，语法糖语句不再被接受。
func TestDoStringLua53SyntaxDisablesExtensions(t *testing.T) {
	options := WithSyntaxExtensions(DefaultOptions(), 0)
	state := NewStateWithOptions(options)
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// assert 来自 base 标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	if err := DoString(state, "local continue = 1 local switch = continue + 1 assert(switch == 2)"); err != nil {
		// 关闭扩展后同名变量应保持 Lua 5.3 标识符语义。
		t.Fatalf("DoString lua53 identifiers failed: %v", err)
	}
	if err := DoString(state, "while true do continue end"); err == nil {
		// 关闭扩展后 continue 语句不能再通过解析。
		t.Fatalf("DoString should reject disabled continue syntax")
	}
	if err := DoString(state, "switch 1 do default end"); err == nil {
		// 关闭扩展后 switch 语句不能再通过解析。
		t.Fatalf("DoString should reject disabled switch syntax")
	}
	if err := DoString(state, "const answer = 42"); err == nil {
		// 关闭扩展后 const 语法糖不能再通过解析。
		t.Fatalf("DoString should reject disabled const syntax")
	}
}

// TestOpenLibsCanDisableGluaEvents 验证 State 选项可关闭 glua 自定义事件全局 API。
func TestOpenLibsCanDisableGluaEvents(t *testing.T) {
	// 创建显式关闭事件能力的 State。
	state := NewStateWithOptions(WithGluaEvents(DefaultOptions(), false))
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库注册仍应成功。
		t.Fatalf("OpenLibs with events disabled failed: %v", err)
	}
	gluaValue := state.Globals().RawGetString("glua")
	gluaTable, ok := gluaValue.Ref.(*runtime.Table)
	if gluaValue.Kind != runtime.KindTable || !ok || gluaTable == nil {
		// 序列化扩展始终需要 glua 根命名空间。
		t.Fatalf("glua serialization namespace should remain available: %#v", gluaValue)
	}
	if value := gluaTable.RawGetString("event"); !value.IsNil() {
		// 关闭事件能力后只移除 glua.event，不影响其他 glua 扩展。
		t.Fatalf("glua.event should not be registered when events are disabled: %#v", value)
	}
	for _, namespace := range []string{"json", "yaml", "xml", "toml", "codec", "hash", "regex", "uuid", "zip", "schema"} {
		// 序列化和通用扩展命名空间与事件条件编译相互独立。
		if value := gluaTable.RawGetString(namespace); value.Kind != runtime.KindTable {
			// 缺少任一命名空间都表示 OpenLibs 注册不完整。
			t.Fatalf("glua.%s should remain available: %#v", namespace, value)
		}
	}
	for _, functionName := range []string{"array", "object"} {
		// 结构形状函数也不依赖 Event 开关。
		if value := gluaTable.RawGetString(functionName); value.Kind != runtime.KindGoClosure {
			// 缺少形状标记入口会破坏空容器往返。
			t.Fatalf("glua.%s should remain available: %#v", functionName, value)
		}
	}
}

// TestDoStringGluaSerialization 验证 JSON、YAML 和 XML 的 Lua 公共 API。
//
// 测试无外部输入；三种格式必须支持中文、数组、对象、null 和错误传播，JSON/XML 选项必须生效。
func TestDoStringGluaSerialization(t *testing.T) {
	// 创建完整标准库 State，使 pcall、assert 和 string 方法可用于公开 API 验收。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 序列化命名空间依赖 OpenLibs 注册。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := DoString(state, `
local value = {
  name = "zing",
  active = true,
  count = 3,
  items = { 1, glua.null, "三" },
}

local compact = glua.json.encode(value)
assert(compact == '{"active":true,"count":3,"items":[1,null,"三"],"name":"zing"}', compact)
local pretty = glua.json.encode(value, true)
assert(pretty:find("\n", 1, true))
local jsonValue = glua.json.decode(compact)
assert(jsonValue.name == "zing" and jsonValue.active and jsonValue.count == 3)
assert(jsonValue.items[2] == glua.null and jsonValue.items[2] == glua.json.null)

local yamlText = glua.yaml.encode(value)
assert(yamlText:find("name: zing", 1, true))
local yamlValue = glua.yaml.decode(yamlText)
assert(yamlValue.items[2] == glua.yaml.null and yamlValue.items[3] == "三")

local xmlText = glua.xml.encode({
  person = { _attr = { id = 7 }, name = "zing", active = true },
  items = { 1, 2, 3 },
}, { root = "document", pretty = true })
assert(xmlText:find("<document>", 1, true))
assert(xmlText:find('<person id="7">', 1, true))
local xmlValue = glua.xml.decode(xmlText)
assert(xmlValue.person._attr.id == 7)
assert(xmlValue.person.name == "zing" and xmlValue.person.active == true)
assert(xmlValue.items[1] == 1 and xmlValue.items[3] == 3)
local xmlStrings = glua.xml.decode('<root><value>001</value></root>', { inferTypes = false })
assert(xmlStrings.value == "001")

local cycle = {}
cycle.self = cycle
assert(not pcall(glua.json.encode, cycle))
assert(not pcall(glua.yaml.encode, { [1] = "array", name = "object" }))
assert(not pcall(glua.xml.encode, { ["bad name"] = true }))
assert(not pcall(glua.json.decode, "{} {}"))
assert(not pcall(glua.json.decode, "9223372036854775808"))
assert(not pcall(glua.yaml.decode, "---\na: 1\n---\nb: 2\n"))
`); err != nil {
		// 任一公开 API 语义失败都输出 Lua 错误对象，便于定位具体断言。
		t.Fatalf("GLua serialization failed: %v object=%s", err, runtime.ErrorObject(err).DebugString())
	}
}

// TestDoStringMathHugeBitwiseErrorKeepsFieldQuotes 验证 math.huge 位运算错误保留字段引号。
//
// 官方 math.lua 会用 `checkerror("field 'huge'", "return math.huge << 1")` 检查错误文本；
// 运行期错误重写不能把 field 来源名的单引号剥掉。
func TestDoStringMathHugeBitwiseErrorKeepsFieldQuotes(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// math、pcall 和 string.find 来自标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local fn = assert(load("return math.huge << 1"))
local ok, message = pcall(fn)
assert(not ok and string.find(message, "field 'huge'", 1, true), message)
`
	if err := DoString(state, source); err != nil {
		// bitwise integer 转换错误必须保留官方期望的 field 名称格式。
		t.Fatalf("DoString math.huge bitwise error failed: %v", err)
	}
}

// TestDoStringMathLogHonorsExplicitBase 验证 math.log 的可选底数参数。
//
// 官方 math.lua 用 `math.log(math.maxinteger, 2)` 推导整数位数；标准库注册层必须保留第二个
// base 参数，不能把 math.log 当成纯一元 fastcall 后忽略多余实参。
func TestDoStringMathLogHonorsExplicitBase(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// math 和 assert 来自标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local maxint = math.maxinteger
local intbits = math.floor(math.log(maxint, 2) + 0.5) + 1
assert(intbits == 64)
assert((1 << intbits) == 0)
`
	if err := DoString(state, source); err != nil {
		// 显式 base 参数必须对齐 Lua 5.3 math.log(x, base) 语义。
		t.Fatalf("DoString math.log explicit base failed: %v", err)
	}
}

// TestDoStringMathAtanHonorsOptionalX 验证 math.atan 的可选 x 参数。
//
// Lua 5.3 `math.atan(y [, x])` 使用 atan2(y, x)；标准库注册层必须保留第二个参数，不能把
// math.atan 当成纯一元 fastcall 后忽略 x。
func TestDoStringMathAtanHonorsOptionalX(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// math 和 assert 来自标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function eq(a, b)
  return a == b or math.abs(a - b) < 1e-12
end
assert(eq(math.atan(1), math.pi / 4))
assert(eq(math.atan(1, 0), math.pi / 2))
`
	if err := DoString(state, source); err != nil {
		// 可选 x 参数必须对齐 Lua 5.3 math.atan(y, x) 语义。
		t.Fatalf("DoString math.atan optional x failed: %v", err)
	}
}

// TestDoStringDefaultAssertErrorHasSourceLine 验证 assert(false) 默认错误携带调用点行号。
//
// 官方 errors.lua 要求 `pcall(function () assert(false) end)` 返回的字符串以 `chunk:line:
// assertion failed!` 结尾；显式错误对象仍保持原对象，不应被默认前缀逻辑改写。
func TestDoStringDefaultAssertErrorHasSourceLine(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// assert、string.match 和 debug.getinfo 来自标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local marker = {}
local expectedLine = debug.getinfo(1, "l").currentline + 1
local res, msg = pcall(function () assert(false) end)
local line = string.match(msg, ":(%d+): assertion failed!$")
assert(tonumber(line) == expectedLine)
res, msg = pcall(function () assert(false, marker) end)
assert(not res and msg == marker)
res, msg = pcall(assert)
assert(not res and string.find(msg, "value expected"))
`
	if err := DoString(state, source); err != nil {
		// 默认 assert 错误必须带行号，显式 table 错误对象必须保持 identity。
		t.Fatalf("DoString default assert error source line failed: %v", err)
	}
}

// TestDoStringGenericForNonCallableIteratorHasSourceLine 验证泛型 for 非函数迭代器错误行号。
//
// Lua 5.3 官方 errors.lua 的 lineerror 小节要求 `for k,v in 3 do` 这类运行期调用错误携带
// 当前 for 表达式所在行号；该错误由 VM 的调用请求路径产生，不能丢失 source:line 前缀。
func TestDoStringGenericForNonCallableIteratorHasSourceLine(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// pcall/assert/string.find 来自标准库，必须先打开。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	err := DoString(state, `
local ok, message = pcall(load("\n local a \n for k,v in 3 \n do \n print(k) \n end"))
assert(not ok)
assert(string.find(message, ":3:"))
`)
	if err != nil {
		// lineerror 断言必须在 Lua 侧通过。
		t.Fatalf("DoString generic for line error failed: %v", err)
	}
}

// TestDoStringGenericForExpandsIteratorCallResults 验证泛型 for 会展开迭代表达式调用结果。
//
// Lua 5.3 的 `for a,b in makeiter() do` 需要把 makeiter 的返回值填入 iterator/state/control
// 三元组；编译期间用于求值 `makeiter()` 的临时寄存器不能挤占后续迭代变量寄存器。
func TestDoStringGenericForExpandsIteratorCallResults(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库提供 assert，必须先打开。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	err := DoString(state, `
local function makeiter()
  local index = 0
  return function()
    index = index + 1
    if index > 1 then return nil end
    return "0", "1"
  end
end
local count = 0
for a, b in makeiter() do
  count = count + 1
  assert(a == "0" and b == "1")
end
assert(count == 1)
`)
	if err != nil {
		// 迭代变量若错位，会在 Lua 侧 assert 失败。
		t.Fatalf("DoString generic for iterator call expansion failed: %v", err)
	}
}

// TestPCallAndXPCall 验证 lua 包对外 API 的 protected call 行为。
//
// 成功路径返回 true + 函数返回值，失败路径返回 false + 错误对象。
func TestPCallAndXPCall(t *testing.T) {
	state := NewState()
	defer state.Close()

	pCallOK, err := PCall(state, func(callState *State) error {
		// 在 pcall 中压入返回值后，框架会原样收集并返回给调用方。
		return callState.Push(runtime.IntegerValue(7))
	})
	if err != nil {
		t.Fatalf("PCall with success should not return Go error: %v", err)
	}
	if len(pCallOK) != 2 {
		t.Fatalf("PCall success result should include true and one payload, got=%d", len(pCallOK))
	}
	if pCallOK[0].Kind != KindBoolean || !pCallOK[0].Bool {
		t.Fatalf("PCall success result first value should be true")
	}
	if got, ok := pCallOK[1].ToInteger(); !ok || got != 7 {
		t.Fatalf("PCall payload should be 7, got=%#v", pCallOK[1])
	}

	xpcallError, err := XPCall(state, func(callState *State) error {
		// 触发错误后，xpcall 应返回 false 和错误对象。
		return runtime.NewRuntimeError(runtime.StringValue("xpcall test"), runtime.ErrLuaError)
	}, func(object Value) (Value, error) {
		// handler 直接返回错误对象，确保返回布局保持 [false, returnedObject]。
		return runtime.StringValue("handled:" + object.DebugString()), nil
	})
	if err != nil {
		t.Fatalf("XPCall wrapper should not return Go error: %v", err)
	}
	if len(xpcallError) != 2 {
		t.Fatalf("XPCall error result should include false and handler object, got=%d", len(xpcallError))
	}
	if xpcallError[0].Kind != KindBoolean || xpcallError[0].Bool {
		t.Fatalf("XPCall error result first value should be false")
	}
	if xpcallError[1].Kind != KindString || xpcallError[1].String != "handled:string(\"xpcall test\")" {
		t.Fatalf("XPCall handler result mismatch: got=%q", xpcallError[1].String)
	}
}

// TestLoadStringAndLoadFileCompileClosures 验证加载 API 会编译源码并压入 Lua closure。
//
// LoadString 和 LoadFile 当前负责 parser/codegen/closure 入栈，不负责执行 Proto。
func TestLoadStringAndLoadFileCompileClosures(t *testing.T) {
	// LoadString 成功后栈顶必须是 Lua closure，Proto.Source 使用传入 chunkName。
	state := NewState()
	defer state.Close()
	if err := LoadString(state, "return 1", "@inline.lua"); err != nil {
		// 合法源码加载不应失败。
		t.Fatalf("LoadString failed: %v", err)
	}
	value := state.ValueAt(-1)
	if value.Kind != KindLuaClosure {
		// 栈顶必须是可执行 Lua closure。
		t.Fatalf("LoadString top kind = %v, want Lua closure", value.Kind)
	}
	closure, ok := value.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || closure.Proto == nil || closure.Proto.Source != "@inline.lua" {
		// closure 必须携带编译后的 Proto 和 chunkName。
		t.Fatalf("LoadString closure mismatch: %#v", value.Ref)
	}
	globalState := NewState()
	defer globalState.Close()
	if err := LoadString(globalState, "return _VERSION", "@global.lua"); err != nil {
		// 全局名称读取应通过 `_ENV` 编译并成功加载。
		t.Fatalf("LoadString global chunk failed: %v", err)
	}
	globalClosure, ok := globalState.ValueAt(-1).Ref.(*runtime.LuaClosure)
	if !ok || len(globalClosure.Upvalues) != 1 || globalClosure.Upvalues[0].Kind != KindTable || globalClosure.Upvalues[0].Ref != globalState.Globals() {
		// 顶层 `_ENV` upvalue 必须绑定到当前 State 的 globals 表。
		t.Fatalf("global closure _ENV mismatch: %#v", globalClosure)
	}

	// LoadFile 成功后使用 @path 作为 chunk source。
	fileState := NewState()
	defer fileState.Close()
	path := filepath.Join(t.TempDir(), "chunk.lua")
	if err := os.WriteFile(path, []byte("return 2"), 0o600); err != nil {
		// 测试夹具必须可写入。
		t.Fatalf("write fixture failed: %v", err)
	}
	if err := LoadFile(fileState, path); err != nil {
		// 合法文件加载不应失败。
		t.Fatalf("LoadFile failed: %v", err)
	}
	fileValue := fileState.ValueAt(-1)
	fileClosure, ok := fileValue.Ref.(*runtime.LuaClosure)
	if fileValue.Kind != KindLuaClosure || !ok || fileClosure.Proto.Source != "@"+path {
		// 文件加载必须保留 @path 调试来源。
		t.Fatalf("LoadFile closure mismatch: value=%#v closure=%#v", fileValue, fileClosure)
	}
	if err := LoadString(nil, "return 1", ""); !errors.Is(err, ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("LoadString nil state should return ErrNilState, got=%v", err)
	}
	if err := LoadFile(nil, path); !errors.Is(err, ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("LoadFile nil state should return ErrNilState, got=%v", err)
	}
}

// TestLoadFileSyntaxErrorReturnsStructuredError 验证 Go API 加载错误提供紧凑主消息和扩展详情。
//
// LoadFile 语法错误应返回 *SyntaxError；Error() 面向 CLI/日志，Details 面向宿主自定义展示。
func TestLoadFileSyntaxErrorReturnsStructuredError(t *testing.T) {
	state := NewState()
	defer state.Close()
	path := filepath.Join(t.TempDir(), "bad.lua")
	if err := os.WriteFile(path, []byte("local a = 1\n\ne"), 0o600); err != nil {
		// 测试夹具必须可写入。
		t.Fatalf("write fixture failed: %v", err)
	}

	err := LoadFile(state, path)
	var syntaxErr *SyntaxError
	if !errors.As(err, &syntaxErr) {
		// Go API 必须返回结构化语法错误，便于宿主读取扩展字段。
		t.Fatalf("LoadFile error = %T %[1]v, want *SyntaxError", err)
	}
	wantMessage := path + ":3:2: syntax error near <eof>"
	if syntaxErr.Error() != wantMessage {
		// 主消息只包含命令行可直接展示的紧凑定位文本。
		t.Fatalf("SyntaxError message = %q, want %q", syntaxErr.Error(), wantMessage)
	}
	if syntaxErr.Details.SourceName != "@"+path || syntaxErr.Details.SourceID != path ||
		syntaxErr.Details.Line != 3 || syntaxErr.Details.Column != 2 ||
		syntaxErr.Details.Near != "<eof>" || syntaxErr.Details.Expected != `expected operator "="` ||
		syntaxErr.Details.Hint != `expected assignment operator "=" or function call arguments` ||
		syntaxErr.Details.LineText != "e" {
		// 扩展字段必须保留源码名、位置、near token、原始 expected 和源码行。
		t.Fatalf("SyntaxError details mismatch: %#v", syntaxErr.Details)
	}
	var parseErr parser.ParseError
	if !errors.As(err, &parseErr) {
		// 包装错误仍应允许调用方取回 parser.ParseError。
		t.Fatalf("SyntaxError should unwrap parser.ParseError")
	}
}

// TestDoStringAndDoFileExecuteChunks 验证执行 API 会运行已加载 chunk。
//
// DoString/DoFile 必须在 protected call 边界内加载源码、执行 closure，并丢弃脚本入口返回值。
func TestDoStringAndDoFileExecuteChunks(t *testing.T) {
	// DoString 加载成功后执行 chunk，并且不在宿主栈上泄漏加载用 closure。
	state := NewState()
	defer state.Close()
	err := DoString(state, "return 1")
	if err != nil {
		// 最小 return chunk 应可执行成功。
		t.Fatalf("DoString failed: %v", err)
	}
	if state.StackTop() != 0 {
		// 成功路径也不能保留 LoadString 压入的 closure。
		t.Fatalf("DoString should leave empty stack, top=%d", state.StackTop())
	}
	err = DoString(state, "if true then return end leaked = true")
	if err != nil {
		// 无返回值 RETURN 也必须终止当前 chunk。
		t.Fatalf("DoString early return failed: %v", err)
	}
	leakedValue, err := GetGlobal(state, "leaked")
	if err != nil {
		// 读取全局变量失败说明 State API 异常。
		t.Fatalf("GetGlobal leaked failed: %v", err)
	}
	if !leakedValue.IsNil() {
		// return 后续语句不应继续执行，否则官方 api.lua 无法跳过 C API 测试。
		t.Fatalf("statement after return executed: %s", leakedValue.DebugString())
	}

	// DoFile 也应保留同样的执行语义。
	fileState := NewState()
	defer fileState.Close()
	path := filepath.Join(t.TempDir(), "chunk.lua")
	if err := os.WriteFile(path, []byte("return 2"), 0o600); err != nil {
		// 测试夹具必须可写入。
		t.Fatalf("write fixture failed: %v", err)
	}
	err = DoFile(fileState, path)
	if err != nil {
		// 文件 chunk 应可执行成功。
		t.Fatalf("DoFile failed: %v", err)
	}
	if err := DoString(nil, "return 1"); !errors.Is(err, ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("DoString nil state should return ErrNilState, got=%v", err)
	}
	if err := DoFile(nil, path); !errors.Is(err, ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("DoFile nil state should return ErrNilState, got=%v", err)
	}
}

// TestDoFileExecutesBinaryChunk 验证 Go API 文件入口可执行 Lua 5.3 binary chunk。
//
// `glua` 脚本入口复用 DoFile；因此该用例确保 `gluac`/string.dump 产出的 chunk 不会被误当作源码
// 解析，也会绑定顶层 `_ENV` 以便写入全局变量。
func TestDoFileExecutesBinaryChunk(t *testing.T) {
	compileState := NewState()
	defer compileState.Close()
	closureValue, err := compileString(compileState, "result = 42", "@binary.lua")
	if err != nil {
		// 测试源码必须可编译。
		t.Fatalf("compile binary fixture failed: %v", err)
	}
	closure, ok := closureValue.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil {
		// compileString 成功后必须得到 Lua closure。
		t.Fatalf("compiled value = %#v, want Lua closure", closureValue)
	}
	path := filepath.Join(t.TempDir(), "binary.luac")
	if err := os.WriteFile(path, bytecode.DumpBinaryChunk(closure.Proto), 0o600); err != nil {
		// binary chunk 夹具必须可写入。
		t.Fatalf("write binary chunk failed: %v", err)
	}

	state := NewState()
	defer state.Close()
	if err := DoFile(state, path); err != nil {
		// binary chunk 应直接经 loader 执行。
		t.Fatalf("DoFile binary chunk failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// 读取全局结果不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindInteger || result.Integer != 42 {
		// 顶层 `_ENV` 必须绑定到当前 State globals。
		t.Fatalf("result = %#v, want integer 42", result)
	}
}

// TestDoStringMultipleAssignmentEvaluatesRHSBeforeWrites 验证多重赋值先完整求值 RHS 再写回。
//
// Lua 5.3 官方 attrib.lua 依赖该语义处理 `a[1], f(a)[2], b, c = ...` 冲突；RHS 的
// `a[1]` 和 `a[f]` 必须读取赋值前的旧表内容，不能被前两个 table 写回提前覆盖。
func TestDoStringMultipleAssignmentEvaluatesRHSBeforeWrites(t *testing.T) {
	// 打开基础库以使用 assert，并执行官方失败点的最小化片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
function f(a) return a end
a = {10, 9; [f] = print}
a[1], f(a)[2], b, c = {alo = assert}, 10, a[1], a[f], 6
a[1].alo(a[2] == 10 and b == 10 and c == print)
`)
	if err != nil {
		// 多重赋值冲突必须按官方 Lua 顺序通过。
		t.Fatalf("DoString multiple assignment failed: %v", err)
	}
}

// TestDoStringSelfBinaryAssignmentReadsLocalAfterRHSCall 验证自二元赋值对齐 Lua 5.3 求值形态。
//
// 官方 Lua 5.3 对 `x = x + f()` 会先执行 RHS 调用，再由 ADD 指令读取当前 open upvalue
// `x`；如果 f 修改了 x，最终结果应使用修改后的值，而不是调用前提前缓存的旧值。
func TestDoStringSelfBinaryAssignmentReadsLocalAfterRHSCall(t *testing.T) {
	// 使用闭包修改 open upvalue，覆盖 codegen 是否提前读取左操作数的差异。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// assert 由 base 库提供，打开标准库失败则测试无意义。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local x = 1
local function f()
  x = 10
  return 2
end
x = x + f()
assert(x == 12)
`)
	if err != nil {
		// 自二元赋值必须读取 RHS 调用后的 open upvalue 值。
		t.Fatalf("DoString self binary assignment failed: %v", err)
	}
}

// TestDoStringNumericForBodyLocalsCloseEachIteration 验证 numeric for 体内 local 每轮独立闭合。
//
// 官方 closure.lua 在 `for` 循环内创建闭包捕获 `local y`；每次迭代都必须创建并关闭独立
// upvalue，否则不同迭代的闭包会共享同一个 y，导致后续调用结果串扰。
func TestDoStringNumericForBodyLocalsCloseEachIteration(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// assert 由 base 库提供，打开标准库失败则测试无意义。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local a = {}
for i = 1, 3 do
  local y = 0
  a[i] = function ()
    y = y + 10
    return y
  end
end
assert(a[1]() == 10)
assert(a[3]() == 10)
assert(a[1]() == 20)
`)
	if err != nil {
		// for body local upvalue 必须按迭代隔离。
		t.Fatalf("DoString numeric for closure failed: %v", err)
	}
}

// TestDoStringLuaClosureEqualityUsesProtoAndUpvalues 验证 Lua closure 相等语义。
//
// Lua 5.3 的 closure.lua 期望相同函数 Proto 且 upvalue 绑定相同的闭包相等；一旦闭包捕获
// numeric for 控制变量，每轮独立 upvalue cell 会让闭包不相等。
func TestDoStringLuaClosureEqualityUsesProtoAndUpvalues(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// assert 由 base 库提供，打开标准库失败则测试无意义。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
a = {}
for i = 1, 5 do
  a[i] = function (x) return x + a + _ENV end
end
assert(a[3] == a[4] and a[4] == a[5])
for i = 1, 5 do
  a[i] = function (x) return i + a + _ENV end
end
assert(a[3] ~= a[4] and a[4] ~= a[5])
`)
	if err != nil {
		// closure equality 必须同时区分 Proto 和 upvalue cell。
		t.Fatalf("DoString Lua closure equality failed: %v", err)
	}
}

// TestDoStringReturnDoesNotOverwriteLaterLocals 验证 return 求值不会覆盖后续局部读取。
//
// 官方 attrib.lua 覆盖 `local and upvalue have same index`：`return a, b` 中 a 是 upvalue、
// b 是同下标 local；返回 a 时不能先覆盖 b 的寄存器。
func TestDoStringReturnDoesNotOverwriteLaterLocals(t *testing.T) {
	// 使用全局 result 暴露返回值校验结果。
	state := NewState()
	defer state.Close()
	source := `
local function foo ()
  local a
  return function ()
    local b
    a, b = 3, 14
    return a, b
  end
end
local a, b = foo()()
result = (a == 3 and b == 14)
`
	if err := DoString(state, source); err != nil {
		// 合法闭包返回脚本应执行成功。
		t.Fatalf("DoString return overwrite failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// 读取验证结果失败说明 State API 异常。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindBoolean || !result.Bool {
		// 返回值必须保持 a=3、b=14。
		t.Fatalf("return overwrite result = %#v", result)
	}
}

// TestRequireExecutesLuaPreloadClosure 验证 package.preload 中的 Lua closure 可被 require 执行。
//
// 官方 attrib.lua 会把 Lua 函数写入 package.preload；require 必须用模块名调用 loader 并缓存结果。
func TestRequireExecutesLuaPreloadClosure(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// require 依赖 base/package/table 等标准库初始化。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
package.preload.pl = function (...)
  local module = {...}
  module.name = module[1]
  return module
end
local first = require "pl"
local second = require "pl"
result = first == second and first.name == "pl" and first[2] == nil
`)
	if err != nil {
		// Lua closure preload 应可正常加载。
		t.Fatalf("DoString require preload failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// 读取验证结果失败说明 State API 异常。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindBoolean || !result.Bool {
		// require 必须执行 Lua loader、传入模块名并缓存模块表。
		t.Fatalf("require preload result = %#v", result)
	}
}

// TestRequireLuaFileSyntaxErrorUsesModuleLoadMessage 验证 Lua 文件加载错误会按 require 语义包装。
//
// 官方 attrib.lua 断言语法错误模块的 pcall(require, name) 错误文本包含
// `error loading module`；运行期错误仍由 chunk 执行阶段原样返回。
func TestRequireLuaFileSyntaxErrorUsesModuleLoadMessage(t *testing.T) {
	// 用临时目录提供一个语法错误模块，并把 package.path 指向该目录。
	state := NewStateWithOptions(Options{AllowHostFilesystem: true})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		t.Fatalf("OpenLibs failed: %v", err)
	}
	modulePath := filepath.Join(t.TempDir(), "bad.lua")
	if err := os.WriteFile(modulePath, []byte("B ="), 0o600); err != nil {
		// 测试夹具必须可写。
		t.Fatalf("write bad module failed: %v", err)
	}
	source := "package.path = " + quoteLuaString(modulePath) + "\n" +
		"local ok, msg = pcall(require, 'bad')\n" +
		"result = (not ok) and string.find(msg, 'error loading module', 1, true) ~= nil\n"
	if err := DoString(state, source); err != nil {
		// pcall 应捕获 require 错误，不应让 chunk 本身失败。
		t.Fatalf("DoString require bad module failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// 读取验证结果失败说明 State API 异常。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindBoolean || !result.Bool {
		// 错误文本必须包含官方 require 加载错误前缀。
		t.Fatalf("require syntax error result = %#v", result)
	}
}

// TestRequireLuaFileCanReplaceChunkEnv 验证模块 chunk 内 `_ENV = {}` 会替换当前环境。
//
// Lua 5.3 官方 attrib.lua 的子包测试会在模块文件开头执行 `_ENV = {}`；后续赋值应写入
// 新环境表并作为模块返回，不能污染宿主全局变量。
func TestRequireLuaFileCanReplaceChunkEnv(t *testing.T) {
	// 准备一个会替换 `_ENV` 并返回新环境的模块文件。
	state := NewStateWithOptions(Options{AllowHostFilesystem: true})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		t.Fatalf("OpenLibs failed: %v", err)
	}
	modulePath := filepath.Join(t.TempDir(), "envmod.lua")
	moduleSource := "_ENV = {}\nAA = 10\nreturn _ENV\n"
	if err := os.WriteFile(modulePath, []byte(moduleSource), 0o600); err != nil {
		// 测试模块必须可写。
		t.Fatalf("write env module failed: %v", err)
	}
	source := "AA = 0\npackage.path = " + quoteLuaString(modulePath) + "\n" +
		"local m = require 'envmod'\n" +
		"result = (AA == 0 and m.AA == 10)\n"
	if err := DoString(state, source); err != nil {
		// require 模块应成功执行并返回自定义环境。
		t.Fatalf("DoString require env module failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// 读取验证结果失败说明 State API 异常。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindBoolean || !result.Bool {
		// 模块内赋值必须写入替换后的 _ENV，不能污染全局 AA。
		t.Fatalf("require env module result = %#v", result)
	}
}

// TestVirtualFilesystemCoversLoadRequireAndIO 验证 Go fs.FS VFS 覆盖 Lua 文件加载与只读 io。
//
// State 配置 VirtualFilesystem 后，loadfile、dofile、require 的 Lua 文件 loader 以及 io.open、
// io.lines 都应从 VFS 读取；未开启 AllowHostFilesystem 时写模式仍被拒绝。
func TestVirtualFilesystemCoversLoadRequireAndIO(t *testing.T) {
	state := NewStateWithOptions(Options{VirtualFilesystem: fstest.MapFS{
		"scripts/value.lua": {Data: []byte("return 41\n")},
		"mods/answer.lua":   {Data: []byte("local name, filename = ...\nreturn {name = name, filename = filename, value = 42}\n")},
		"data/text.txt":     {Data: []byte("first\nsecond\n")},
	}})
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// VFS 测试依赖 base、package 和 io 标准库。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local loaded = assert(loadfile("scripts/value.lua"))
local loadedValue = loaded()
local dofileValue = dofile("./scripts/value.lua")
local mod = require "mods.answer"
local file = assert(io.open("data/text.txt", "r"))
local all = file:read("*a")
file:close()
local lineCount = 0
for line in io.lines("data/text.txt") do
  lineCount = lineCount + 1
end
local writeOK, writeErr = pcall(io.open, "data/text.txt", "w")
result = loadedValue == 41
  and dofileValue == 41
  and mod.name == "mods.answer"
  and mod.filename == "./mods/answer.lua"
  and mod.value == 42
  and all == "first\nsecond\n"
  and lineCount == 2
  and not writeOK
  and string.find(writeErr, "filesystem access is disabled", 1, true) ~= nil
`
	if err := DoString(state, source); err != nil {
		// VFS 读路径应完整执行，写模式错误由 pcall 捕获。
		t.Fatalf("DoString VFS script failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// 读取验证结果失败说明 State API 异常。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindBoolean || !result.Bool {
		// 所有 VFS 路径都必须命中，并且写模式必须被权限策略拒绝。
		t.Fatalf("VFS result mismatch: %#v", result)
	}
}

// TestVirtualFilesystemPriorityAndTraversalPolicy 验证 VFS 与宿主优先级以及路径穿越拒绝。
//
// 默认策略应优先读取 VFS；PreferHostFilesystem 开启且宿主授权时改为宿主优先。宿主未授权时，
// `..` 路径必须被 VFS 清洗层拒绝，避免逃出 fs.FS 根目录。
func TestVirtualFilesystemPriorityAndTraversalPolicy(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "same.lua"), []byte(`result = "host"`), 0o600); err != nil {
		// 宿主优先级夹具必须可写。
		t.Fatalf("write host fixture failed: %v", err)
	}
	virtualFS := fstest.MapFS{
		"same.lua": {Data: []byte(`result = "virtual"`)},
	}

	virtualFirst := NewStateWithOptions(Options{VirtualFilesystem: virtualFS, AllowHostFilesystem: true})
	defer virtualFirst.Close()
	if err := OpenLibs(virtualFirst); err != nil {
		// loadfile 需要 base 标准库。
		t.Fatalf("OpenLibs virtual-first failed: %v", err)
	}
	if err := DoString(virtualFirst, `assert(loadfile("same.lua"))()`); err != nil {
		// 默认优先级下应读取 VFS 文件。
		t.Fatalf("virtual-first loadfile failed: %v", err)
	}
	virtualResult, _ := GetGlobal(virtualFirst, "result")
	if virtualResult.Kind != runtime.KindString || virtualResult.String != "virtual" {
		// VFS 默认优先，不能被同名宿主文件覆盖。
		t.Fatalf("virtual-first result mismatch: %#v", virtualResult)
	}

	hostFirst := NewStateWithOptions(Options{VirtualFilesystem: virtualFS, AllowHostFilesystem: true, PreferHostFilesystem: true})
	defer hostFirst.Close()
	if err := OpenLibs(hostFirst); err != nil {
		// loadfile 需要 base 标准库。
		t.Fatalf("OpenLibs host-first failed: %v", err)
	}
	if err := DoString(hostFirst, `assert(loadfile("same.lua"))()`); err != nil {
		// 宿主优先级下应读取当前工作目录的宿主文件。
		t.Fatalf("host-first loadfile failed: %v", err)
	}
	hostResult, _ := GetGlobal(hostFirst, "result")
	if hostResult.Kind != runtime.KindString || hostResult.String != "host" {
		// PreferHostFilesystem 必须允许宿主同名文件覆盖 VFS。
		t.Fatalf("host-first result mismatch: %#v", hostResult)
	}

	sandboxed := NewStateWithOptions(Options{VirtualFilesystem: virtualFS})
	defer sandboxed.Close()
	if err := OpenLibs(sandboxed); err != nil {
		// loadfile 需要 base 标准库。
		t.Fatalf("OpenLibs sandboxed failed: %v", err)
	}
	if err := DoString(sandboxed, `local _, msg = loadfile("../same.lua"); result = string.find(msg, "escapes root", 1, true) ~= nil`); err != nil {
		// 路径穿越应作为 loadfile 的第二返回值暴露，不应让测试 chunk 失败。
		t.Fatalf("sandbox traversal script failed: %v", err)
	}
	traversalResult, _ := GetGlobal(sandboxed, "result")
	if traversalResult.Kind != runtime.KindBoolean || !traversalResult.Bool {
		// VFS 清洗层必须拒绝 `..` 穿越。
		t.Fatalf("traversal result mismatch: %#v", traversalResult)
	}
}

// TestDoStringKeepsBlockLocalsScoped 验证嵌套 block 的 local 不会泄漏到外层。
//
// Lua 5.3 要求 do、if、for 等 block 内的同名 local 在 block 结束后恢复外层绑定；官方
// main.lua 依赖 `do local out = getoutput() end` 不覆盖外层重定向路径。
func TestDoStringKeepsBlockLocalsScoped(t *testing.T) {
	// 使用全局 result 暴露脚本内部最终值，避免依赖 print 捕获。
	state := NewState()
	defer state.Close()
	source := `
local out = "outer"
do
  local out = "inner"
end
if true then
  local out = "branch"
end
for i = 1, 1 do
  local out = "loop"
end
result = out
`
	if err := DoString(state, source); err != nil {
		// 合法作用域脚本应执行成功。
		t.Fatalf("DoString scoped locals failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindString || result.String != "outer" {
		// 内层 local 不能覆盖外层 out。
		t.Fatalf("result = %#v, want outer", result)
	}
}

// TestDoStringSelfConcatInsideNumericFor 验证 numeric for 内自拼接不会读取循环控制寄存器。
//
// OP_CONCAT 的 B..C 是连续寄存器区间；codegen 优化 `s = s .. "x"` 时必须保证区间只覆盖
// 真实拼接操作数，不能把 numeric for 的 index/limit/step 临时寄存器拼入结果。
func TestDoStringSelfConcatInsideNumericFor(t *testing.T) {
	// 使用完整 API 路径覆盖 parser、codegen、VM 和赋值写回。
	state := NewState()
	defer state.Close()
	source := `
local s = ""
for i = 1, 3 do
  s = s .. "x"
end
result = s
`
	if err := DoString(state, source); err != nil {
		// 自拼接脚本必须执行成功。
		t.Fatalf("self concat script failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// 测试结果应写入全局变量。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindString || result.String != "xxx" {
		// 结果只能包含三次追加的字面量，不能混入循环控制数字。
		t.Fatalf("self concat result mismatch: %#v", result)
	}
}

// TestDoStringConcatStringPairIgnoresStringMetamethod 验证 string/string 基础拼接不触发 __concat。
//
// Lua 5.3 对两个可直接转 string 的操作数优先执行基础拼接；即使 debug.setmetatable 为 string 类型
// 写入 `__concat`，也不能让元方法拦截 `"a" .. "b"`。后续 string_concat builder fast path 必须保留
// 该边界。
func TestDoStringConcatStringPairIgnoresStringMetamethod(t *testing.T) {
	// 创建完整标准库 State，确保 debug.setmetatable 可写入 string 类型级元表。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local called = false
debug.setmetatable("", {
  __concat = function()
    called = true
    return "bad"
  end,
})
assert(("a" .. "b") == "ab")
assert(called == false)
`
	if err := DoString(state, source); err != nil {
		// 纯 string 基础拼接不应进入 string 类型 __concat。
		t.Fatalf("DoString string concat metamethod guard failed: %v", err)
	}
}

// TestDoStringConcatNonStringUsesStringMetamethod 验证非 string 操作数仍可通过 string __concat 处理。
//
// 当任一操作数不能直接转 string 时，Lua 5.3 会查找两侧 `__concat`。这里左侧 table 没有元表、
// 右侧 string 有类型级元表，必须调用 string `__concat`；builder fast path 的 operand guard 失败时
// 也必须回退到该普通路径。
func TestDoStringConcatNonStringUsesStringMetamethod(t *testing.T) {
	// 创建完整标准库 State，确保 debug.setmetatable 可写入 string 类型级元表。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local calls = 0
debug.setmetatable("", {
  __concat = function(a, b)
    calls = calls + 1
    assert(type(a) == "table")
    assert(b == "b")
    return "ok"
  end,
})
assert(({} .. "b") == "ok")
if calls ~= 1 then error("calls=" .. tostring(calls)) end
`
	if err := DoString(state, source); err != nil {
		// 非 string 操作数必须继续触发 `__concat` 元方法。
		t.Fatalf("DoString non-string concat metamethod failed: %v", err)
	}
}

// TestDoStringConcatLineHookSeesSelfAppendIntermediates 验证 line hook 可观察自拼接中间值。
//
// `s = s .. "x"` 的普通 VM 路径每轮都会在执行语句前触发 line hook，并在上一轮结束后把新字符串
// 写回 `s`。若未来 builder fast path 批量压缩循环，在 debug hook 打开时必须回退普通路径，否则
// hook 将无法看到 `0,1,2` 这三个中间长度。
func TestDoStringConcatLineHookSeesSelfAppendIntermediates(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook 与 table.concat 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `local seen = {}
local s = ""
local appendLine = debug.getinfo(1).currentline + 8
debug.sethook(function(event, line)
  assert(event == "line")
  if line == appendLine then
    seen[#seen + 1] = #s
  end
end, "l")
for i = 1, 3 do
  s = s .. "x"
end
debug.sethook()
assert(table.concat(seen, ",") == "0,1,2")
assert(s == "xxx")
`
	if err := DoString(state, source); err != nil {
		// line hook 必须观察到每轮自拼接前的可见中间值。
		t.Fatalf("DoString concat line hook visibility failed: %v", err)
	}
}

// TestDoStringConcatCountHookSeesSelfAppendIntermediates 验证 count hook 可观察自拼接中间值。
//
// count hook 没有源码行参数，但它仍会在 VM 指令边界读取闭包中的 local `s`。未来若将字符串自拼接
// 压缩成整段 builder，必须在任意 debug hook 打开时回退普通路径，否则 count hook 会看不到
// `0,1,2,3` 这些递增长度。
func TestDoStringConcatCountHookSeesSelfAppendIntermediates(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook 与 table.concat 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `local seen = {}
local seenMap = {}
local s = ""
debug.sethook(function(event)
  assert(event == "count")
  local length = #s
  if not seenMap[length] then
    seenMap[length] = true
    seen[#seen + 1] = length
  end
end, "", 1)
for i = 1, 3 do
  s = s .. "x"
end
debug.sethook()
assert(s == "xxx")
assert(seenMap[0] and seenMap[1] and seenMap[2] and seenMap[3], table.concat(seen, ","))
`
	if err := DoString(state, source); err != nil {
		// count hook 必须观察到自拼接过程中的全部可见长度。
		t.Fatalf("DoString concat count hook visibility failed: %v", err)
	}
}

// TestDoStringSupportsStringMethodIndex 验证字符串值可通过类型级方法表调用 string 库方法。
//
// Lua 5.3 标准库打开后，`("..."):format(x)` 应等价于 `string.format("...", x)`；官方
// main.lua 使用该语法生成临时脚本文本。
func TestDoStringSupportsStringMethodIndex(t *testing.T) {
	// 打开标准库以注册 string 方法表。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := DoString(state, `result = ("value=%s"):format("ok")`); err != nil {
		// 字符串冒号方法调用应执行成功。
		t.Fatalf("DoString string method failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindString || result.String != "value=ok" {
		// format 方法必须来自 string 库表。
		t.Fatalf("result = %#v, want value=ok", result)
	}
}

// TestDoStringSupportsMethodFunctionDefinition 验证冒号方法定义语法。
//
// `function a:test()` 必须等价于 `a.test = function(self)`，官方 gc.lua 使用该形式定义方法。
func TestDoStringSupportsMethodFunctionDefinition(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
		a = {}
		function a:test()
			return self
		end
		result = (a:test() == a)
	`
	if err := DoString(state, source); err != nil {
		// 冒号方法定义和调用应执行成功。
		t.Fatalf("DoString method function definition failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindBoolean || !result.Bool {
		// 方法体首参 self 必须绑定到调用接收者 a。
		t.Fatalf("result = %#v, want true", result)
	}
}

// TestDoStringSupportsTableIndexFieldConstructor 验证 table constructor 动态 key 字段。
//
// Lua 5.3 允许 `{[expr] = value}`，官方 gc.lua 使用 table 作为 weak table 的 key。
func TestDoStringSupportsTableIndexFieldConstructor(t *testing.T) {
	state := NewState()
	defer state.Close()
	source := `
		k = {}
		t = {[k] = 7}
		result = t[k]
	`
	if err := DoString(state, source); err != nil {
		// 动态 key table constructor 应执行成功。
		t.Fatalf("DoString table index field failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindInteger || result.Integer != 7 {
		// 动态 key 必须能按原 key 读回值。
		t.Fatalf("result = %#v, want 7", result)
	}
}

// TestDoStringSupportsParenthesizedCallStatement 验证括号表达式调用语句。
//
// Lua 5.3 允许 `(Message or print)(...)` 这种调用语句，官方 gc.lua 的跳过分支依赖该语法。
func TestDoStringSupportsParenthesizedCallStatement(t *testing.T) {
	state := NewState()
	defer state.Close()
	source := `
		f = function(value)
			result = value
		end;
		(nil or f)(9)
	`
	if err := DoString(state, source); err != nil {
		// 括号表达式调用语句应执行成功。
		t.Fatalf("DoString parenthesized call statement failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindInteger || result.Integer != 9 {
		// 调用实参必须传入最终函数。
		t.Fatalf("result = %#v, want 9", result)
	}
}

// TestDoStringSupportsRepeatUntil 验证 repeat-until 后置条件循环。
//
// repeat 循环体至少执行一次，并在 until 条件为真时退出；官方 gc.lua 大量使用该语句。
func TestDoStringSupportsRepeatUntil(t *testing.T) {
	state := NewState()
	defer state.Close()
	source := `
		i = 0
		repeat
			i = i + 1
		until i == 3
		result = i
	`
	if err := DoString(state, source); err != nil {
		// repeat-until 脚本应执行成功。
		t.Fatalf("DoString repeat-until failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindInteger || result.Integer != 3 {
		// 循环应在 i == 3 时退出。
		t.Fatalf("result = %#v, want 3", result)
	}
}

// TestDoStringExpandsFunctionCallLocalAssignment 验证 local 声明接收函数多返回值。
//
// Lua 5.3 中初始化列表最后一个函数调用会展开到剩余变量；官方 main.lua 的 `local f, pid = runback()`
// 依赖第二个返回值不被丢弃。
func TestDoStringExpandsFunctionCallLocalAssignment(t *testing.T) {
	state := NewState()
	defer state.Close()
	source := `
local function pair()
  return "file", 42
end
local f, pid = pair()
result = pid
`
	if err := DoString(state, source); err != nil {
		// 多返回 local 初始化脚本应执行成功。
		t.Fatalf("DoString multi return local assignment failed: %v", err)
	}
	result, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if result.Kind != runtime.KindInteger || result.Integer != 42 {
		// 第二个 local 必须接收函数调用的第二个返回值。
		t.Fatalf("result = %#v, want 42", result)
	}
}

// TestDoStringGenericForKeepsTableKeys 验证对外 API 执行泛型 for 时保留 table key。
//
// pairs 返回的 table key 必须作为下一轮 next 控制变量写回，不能覆盖 iterator/state/control。
func TestDoStringGenericForKeepsTableKeys(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local lim = 15
local a = {}
for i = 1, lim do
  a[{}] = i
end
local b = {}
for k, v in pairs(a) do
  if type(k) ~= "table" then
    error("bad key")
  end
  b[k] = v
end
for n in pairs(b) do
  a[n] = nil
  assert(type(n) == "table" and next(n) == nil)
end
assert(next(a) == nil)
`
	if err := DoString(state, source); err != nil {
		// 泛型 for 控制变量若写回错误，会在 next 或最终清空断言处失败。
		t.Fatalf("DoString generic for table key failed: %v", err)
	}
}

// TestDoStringCollectGarbageSweepsBasicWeakTables 验证 collectgarbage 会清理基础弱表项。
//
// 该用例覆盖官方 gc.lua 中 weak key 与 weak value 的前两个小节，不涵盖复杂 ephemeron 固定点。
func TestDoStringCollectGarbageSweepsBasicWeakTables(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local lim = 15
a = {}
setmetatable(a, {__mode = "k"})
for i = 1, lim do a[{}] = i end
for i = 1, lim do a[i] = i end
for i = 1, lim do local s = string.rep("@", i); a[s] = s .. "#" end
collectgarbage()
local count = 0
for k, v in pairs(a) do
  assert(k == v or k .. "#" == v)
  count = count + 1
end
assert(count == 2 * lim)

a = {}
setmetatable(a, {__mode = "v"})
a[1] = string.rep("b", 21)
collectgarbage()
assert(a[1])
a[1] = nil
for i = 1, lim do a[i] = {} end
for i = 1, lim do a[i .. "x"] = {} end
for i = 1, lim do local t = {}; a[t] = t end
for i = 1, lim do a[i + lim] = i .. "x" end
collectgarbage()
count = 0
for k, v in pairs(a) do
  assert(k == v or k - lim .. "x" == v)
  count = count + 1
end
assert(count == 2 * lim)
`
	if err := DoString(state, source); err != nil {
		// 弱 key/value 基础清理失败会触发断言或类型错误。
		t.Fatalf("DoString weak table GC failed: %v", err)
	}
}

// TestDoStringCollectGarbageIgnoresStaleTemporaryCoroutineWrap 验证弱表清理不被历史临时寄存器保活。
//
// 官方 coroutine.lua 在前置 xpcall(pcall, ...) continuation 后，会创建新的 coroutine.wrap 并只放入
// weak key/value 表；`x=nil; collectgarbage()` 必须清掉该 weak value。此前活动寄存器 GC 根扫描把
// `(*temporary)` 调试槽位中的旧 wrap 闭包当成强根，导致完整前缀下 `C[1]` 残留。
func TestDoStringCollectGarbageIgnoresStaleTemporaryCoroutineWrap(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local f = function (s, i) return coroutine.yield(i) end
local f1 = coroutine.wrap(function ()
             return xpcall(pcall, function (...) return ... end,
               function ()
                 local s = 0
                 for i in f, nil, 1 do pcall(function () s = s + i end) end
                 error({s})
               end)
           end)

f1()
for i = 1, 10 do assert(f1(i) == i) end
local r1, r2, v = f1(nil)
assert(r1 and not r2 and v[1] == (10 + 1)*10/2)

local C = {}; setmetatable(C, {__mode = "kv"})
local x = coroutine.wrap(function ()
  local a = 10
  local function f () a = a + 10; return a end
  while true do
    a = a + 1
    coroutine.yield(f)
  end
end)
C[1] = x
assert(C[1] ~= f1)
local f = x()
assert(f() == 21 and x()() == 32 and x() == f)
x = nil
collectgarbage()
assert(C[1] == nil)
assert(f() == 43 and f() == 53)
`
	if err := DoString(state, source); err != nil {
		// 前置 continuation 造成的临时寄存器残留若被误扫为强根，会在 C[1] 断言处失败。
		t.Fatalf("DoString coroutine weak table GC failed: %v", err)
	}
}

// TestDoStringAutoGCSweepsWeakTableDuringAllocationPressure 验证自动 GC 会在分配压力下清理弱表。
//
// 官方 closure.lua 使用 `while x[1] do ... end` 等待 weak value 自动消失；没有显式
// collectgarbage 调用时，table/closure/字符串拼接分配仍必须给兼容 GC 一次推进机会。
func TestDoStringAutoGCSweepsWeakTableDuringAllocationPressure(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	err := DoString(state, `
local A = 1
local x = {[1] = {}}
setmetatable(x, {__mode = "kv"})
local guard = 0
while x[1] do
  local a = A .. A .. A .. A
  A = A + 1
  guard = guard + 1
  assert(guard < 100)
end
assert(getmetatable(x).__mode == "kv")
`)
	if err != nil {
		// 自动 GC 若没有 sweep weak table，guard 会触发断言。
		t.Fatalf("DoString auto weak table GC failed: %v", err)
	}
}

// TestDoStringCollectGarbageKeepsEphemeronChain 验证 collectgarbage 支持 weak-key ephemeron 固定点。
//
// 该用例覆盖官方 gc.lua 中 `a[n] = {k = {x}}` 的链式弱 key 场景：当最新 key 仍强可达时，
// value 中引用的上一个 key 应继续保留；当强根移除后，弱 key 项应被完整清理。
func TestDoStringCollectGarbageKeepsEphemeronChain(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local mt = {__mode = "k"}
a = {{10}, {20}, {30}, {40}}
setmetatable(a, mt)
x = nil
for i = 1, 100 do
  local n = {}
  a[n] = {k = {x}}
  x = n
end
collectgarbage()
local n = x
local count = 0
while n do
  local value = a[n]
  assert(value)
  n = value.k[1]
  count = count + 1
end
assert(count == 100)
x = nil
collectgarbage()
for i = 1, 4 do
  assert(a[i][1] == i * 10)
  a[i] = nil
end
assert(next(a) == nil)
`
	if err := DoString(state, source); err != nil {
		// ephemeron 固定点传播或最终清理失败会触发断言。
		t.Fatalf("DoString ephemeron weak key GC failed: %v", err)
	}
}

// TestDoStringCollectGarbageKeepsNestedEphemeronChain 验证嵌套 weak-key 表的 ephemeron 固定点。
//
// 该用例覆盖官方 gc.lua 中 `a[a[K][nk]][n] = {x, k = k}` 的多弱表链：外层 K
// 和索引表保持强可达时，内层 weak-key table 必须通过 value 链传播保留全部历史 key。
func TestDoStringCollectGarbageKeepsNestedEphemeronChain(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local function GC()
  collectgarbage()
  collectgarbage()
end
local mt = {__mode = "k"}
a = {}
setmetatable(a, mt)
local K = {}
a[K] = {}
for i = 1, 10 do
  a[K][i] = {}
  a[a[K][i]] = setmetatable({}, mt)
end
x = nil
local k = 1
for j = 1, 100 do
  local n = {}
  local nk = k % 10 + 1
  a[a[K][nk]][n] = {x, k = k}
  x = n
  k = nk
end
GC()
local n = x
local count = 0
while n do
  local value = a[a[K][k]][n]
  assert(value)
  n = value[1]
  k = value.k
  count = count + 1
end
assert(count == 100)
`
	if err := DoString(state, source); err != nil {
		// 嵌套 ephemeron 固定点传播失败会触发断言。
		t.Fatalf("DoString nested ephemeron weak key GC failed: %v", err)
	}
}

// TestDoStringCollectGarbageRunsTableFinalizers 验证 collectgarbage 执行 table `__gc` 元方法。
//
// 该用例覆盖官方 gc.lua 中 table finalizer 的错误顺序：第一次 GC 在 i=8 处抛错并由
// pcall 捕获，第二次 GC 继续处理剩余对象，新登记对象不能抢在旧队列剩余对象之前。
func TestDoStringCollectGarbageRunsTableFinalizers(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
collectgarbage("stop")
local u = {}
local s = {}
setmetatable(s, {__mode = "k"})
setmetatable(u, {__gc = function(o)
  local i = s[o]
  s[i] = true
  assert(not s[i - 1])
  if i == 8 then error("here") end
end})
for i = 6, 10 do
  local n = setmetatable({}, getmetatable(u))
  s[n] = i
end
local ok = pcall(collectgarbage)
assert(not ok)
for i = 8, 10 do assert(s[i]) end
for i = 1, 5 do
  local n = setmetatable({}, getmetatable(u))
  s[n] = i
end
collectgarbage()
for i = 1, 10 do assert(s[i]) end
getmetatable(u).__gc = false
setmetatable({}, {__gc = function() error{} end})
local success, message = pcall(collectgarbage)
assert(not success and type(message) == "string" and string.find(message, "error in __gc"))
`
	if err := DoString(state, source); err != nil {
		// table finalizer 顺序或错误包装失败会触发断言。
		t.Fatalf("DoString table finalizer GC failed: %v", err)
	}
	if state.CallDepth() != 0 {
		// finalizer 抛错被 pcall 捕获后，`__gc` 调试帧也必须清理，避免后续 GC 累积调用深度。
		t.Fatalf("call depth leaked after finalizer errors: %d", state.CallDepth())
	}
}

// TestDoStringCollectGarbageInsideHookDefersFinalizers 验证 hook 回调内 GC 不重入 table finalizer。
//
// Lua 5.3 官方 db.lua 会在 debug hook 中调用 collectgarbage；本运行时的 table `__gc` 会执行 Lua
// 函数，因此 hook 内必须延后 finalizer，避免 `__gc` 产生的 Lua 调用扰动当前 hook 栈。普通
// collectgarbage 仍应在 hook 退出后执行已排队的 finalizer。
func TestDoStringCollectGarbageInsideHookDefersFinalizers(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local finalized = 0
do
  local object = setmetatable({}, {__gc = function() finalized = finalized + 1 end})
  object = nil
end
local observed = false
debug.sethook(function()
  observed = true
  collectgarbage()
  for i = 1, 32 do local temporary = {} end
  assert(finalized == 0)
end, "l")
local line = 1
debug.sethook()
collectgarbage()
assert(observed and finalized == 1 and line == 1)
`
	if err := DoString(state, source); err != nil {
		// hook 内重入 finalizer 或 hook 退出后未执行 finalizer 都会触发断言。
		t.Fatalf("DoString hook collectgarbage finalizer deferral failed: %v", err)
	}
}

// TestDoStringTableFinalizerSuppressesDebugHooks 验证 table finalizer 内部调用不污染用户 hook。
//
// Lua 5.3 的 GC 元方法属于 VM 内部清理路径；当用户正在收集 call hook 事件时，`__gc` 内部调用
// 不应被当成被测程序调用。官方 all.lua 先运行 gc.lua 再运行 db.lua，会留下会调用 print 的
// finalizer，本用例用本地 marker 函数复现同类污染。
func TestDoStringTableFinalizerSuppressesDebugHooks(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local seen = {}
local markerCalls = 0
local function marker() markerCalls = markerCalls + 1 end
do
  local object = setmetatable({}, {__gc = function() marker() end})
  object = nil
end
debug.sethook(function(event)
  if event == "call" then
    local info = debug.getinfo(2, "f")
    seen[info.func] = true
  end
end, "c")
collectgarbage()
debug.sethook()
assert(markerCalls == 1 and not seen[marker])
`
	if err := DoString(state, source); err != nil {
		// finalizer 内部 marker 调用若被 hook 捕获会触发断言。
		t.Fatalf("DoString table finalizer hook suppression failed: %v", err)
	}
}

// TestDoStringAutoFinalizerKeepsReachableGlobal 验证自动 GC 不提前终结全局强引用对象。
//
// Lua 5.3 官方 gc.lua 会把一个带 `__gc` 的 table 保存在全局变量里，期望它只在关闭 State 时
// 终结；其 finalizer 会复活自身并创建新的 finalizable table。自动 GC 如果忽略全局强根，会在
// 后续分配压力下提前执行该 finalizer，并可能不断创建新待终结对象，导致 all.lua 长时间无法结束。
func TestDoStringAutoFinalizerKeepsReachableGlobal(t *testing.T) {
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local finalized = 0
local mt = {}
mt.__gc = function(o)
  finalized = finalized + 1
  _G.kept = o
  setmetatable({}, mt)
end
_G.kept = setmetatable({}, mt)
for i = 1, 1024 do
  local temporary = {}
end
assert(finalized == 0)
_G.kept = nil
collectgarbage()
assert(finalized == 1)
`
	if err := DoString(state, source); err != nil {
		// 全局强引用被自动 GC 忽略或释放后 finalizer 未运行都会触发断言。
		t.Fatalf("DoString auto finalizer global reachability failed: %v", err)
	}
}

// TestDoStringExpandsOpenVarargRegisters 验证开放 VARARG 会按运行期实参数扩展寄存器窗口。
//
// Lua 5.3 中 `string.format(p, ...)` 会生成 VARARG B=0；当实参多于 codegen 静态窗口时，
// 执行器必须扩展寄存器窗口，避免在官方 main.lua 的 RUN 辅助函数中越界。
func TestDoStringExpandsOpenVarargRegisters(t *testing.T) {
	// 创建带标准库的 State，使用 string.format 覆盖开放 vararg 调用路径。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local function formatAll(pattern, ...)
  return string.format(pattern, ...)
end
result = formatAll("%s%s%s%s", "a", "b", "c", "d")
`
	if err := DoString(state, source); err != nil {
		// 开放 vararg 不应触发 register out of range。
		t.Fatalf("DoString open vararg failed: %v", err)
	}
	resultValue, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if resultValue.Kind != runtime.KindString || resultValue.String != "abcd" {
		// string.format 应收到全部 vararg。
		t.Fatalf("result = %#v, want abcd", resultValue)
	}
}

// TestDoStringRepeatUntilConditionSeesBodyLocal 验证 repeat-until 条件可见循环体局部变量。
//
// Lua 5.3 规定 repeat 块内声明的 local 在 until 条件中仍然可见；官方 locals.lua 依赖
// `repeat local b; a,b=1,2 until a+b==3` 在首轮退出。
func TestDoStringRepeatUntilConditionSeesBodyLocal(t *testing.T) {
	// 创建带标准库的 State，执行覆盖 repeat body local 遮蔽外层同名变量的片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local outer = 10
local a
repeat
  local outer
  a, outer = 1, 2
  assert(a + outer == 3)
until a + outer == 3
result = outer
`
	if err := DoString(state, source); err != nil {
		// until 条件应解析到 repeat body 内的 outer，否则会持续循环或断言失败。
		t.Fatalf("DoString repeat-until local scope failed: %v", err)
	}
	resultValue, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if resultValue.Kind != runtime.KindInteger || resultValue.Integer != 10 {
		// repeat body 内局部变量不能泄漏到外层 outer。
		t.Fatalf("result = %#v, want outer integer 10", resultValue)
	}
}

// TestDoStringLoadBindsExplicitEnvironment 验证 load 第四参数会绑定 chunk 环境。
//
// Lua 5.3 的 `load(chunk, chunkname, mode, env)` 会把 env 设置为返回 closure 的第一个
// `_ENV` upvalue，官方 locals.lua 使用该能力隔离全局赋值。
func TestDoStringLoadBindsExplicitEnvironment(t *testing.T) {
	// 创建带标准库的 State，执行 load env 隔离全局赋值的片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local env = {}
local chunk = assert(load("a = 3", nil, nil, env))
chunk()
assert(env.a == 3)
result = _G.a
`
	if err := DoString(state, source); err != nil {
		// load 显式 env 应接收 chunk 内全局赋值。
		t.Fatalf("DoString load env failed: %v", err)
	}
	resultValue, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if resultValue.Kind != runtime.KindNil {
		// chunk 内 a=3 不能污染默认全局环境。
		t.Fatalf("result = %#v, want nil", resultValue)
	}
}

// TestDoStringClosesBlockLocalEnvUpvalue 验证离开 block 时会闭合被捕获的 local `_ENV`。
//
// Lua 5.3 中 `do local _ENV = mt; function foo() ... end end` 生成的 foo 必须继续使用 mt，
// 不能被后续寄存器复用污染；官方 locals.lua 的 lexical environments 覆盖该路径。
func TestDoStringClosesBlockLocalEnvUpvalue(t *testing.T) {
	// 创建带标准库的 State，执行 local `_ENV` 被函数捕获并跨 block 调用的片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local mt = {_G = _G}
local foo
A = false
do
  local _ENV = mt
  function foo(x)
    A = x
    do local _ENV = _G; A = 1000 end
    return function (suffix) return A .. suffix end
  end
end
local closure = foo("hi")
assert(mt.A == "hi" and A == 1000)
result = closure("*")
`
	if err := DoString(state, source); err != nil {
		// 捕获的 local `_ENV` 应在 block 结束后保持 mt。
		t.Fatalf("DoString block local _ENV upvalue failed: %v", err)
	}
	resultValue, err := GetGlobal(state, "result")
	if err != nil {
		// result 全局读取不应失败。
		t.Fatalf("GetGlobal result failed: %v", err)
	}
	if resultValue.Kind != runtime.KindString || resultValue.String != "hi*" {
		// 内层返回闭包必须继续读取 mt.A。
		t.Fatalf("result = %#v, want hi*", resultValue)
	}
}

// TestDoStringTableConstructorExpandsTrailingCall 验证 table constructor 末尾函数调用展开。
//
// Lua 5.3 中 `{f(3), f(10)}` 的最后一个函数调用必须保留全部返回值，官方 constructs.lua
// 使用该语义构造递归返回列表。
func TestDoStringTableConstructorExpandsTrailingCall(t *testing.T) {
	// 创建带标准库的 State，执行 table constructor 末尾 call 多返回值展开片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
function f(i)
  if i > 0 then return i, f(i - 1) end
end
result = {f(3), f(5), f(10)}
assert(result[1] == 3 and result[2] == 5 and result[3] == 10)
assert(result[4] == 9 and result[12] == 1)
`
	if err := DoString(state, source); err != nil {
		// 末尾函数调用必须展开为 table 数组字段。
		t.Fatalf("DoString trailing table call failed: %v", err)
	}
}

// TestDoStringNumericForInitUsesOuterLoopVariable 验证 numeric for 初始表达式读取外层变量。
//
// Lua 5.3 中 `for i = i, 1, -1 do` 的右侧 i 必须解析为外层可见变量，新的循环变量只在
// 表达式求值完成后进入循环体作用域。
func TestDoStringNumericForInitUsesOuterLoopVariable(t *testing.T) {
	// 创建带标准库的 State，执行 constructs.lua 中嵌套 numeric for 的核心片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local n = 6
local outer = 3
local total = 0
for i = 1, n do
  for i = i, 1, -1 do
    total = total + 1
  end
end
assert(total == n * (n + 1) / 2 and outer == 3)
`
	if err := DoString(state, source); err != nil {
		// numeric for 初始表达式不应读取尚未进入作用域的新循环变量。
		t.Fatalf("DoString nested numeric for failed: %v", err)
	}
}

// TestDoStringDebugGetInfoReportsGlobalFunctionName 验证 debug.getinfo 返回全局函数名。
//
// Lua 5.3 官方 constructs.lua 会在全局函数 F 内断言 `debug.getinfo(1, "n").name == "F"`，
// VM 调用路径需要把全局调用点名称写入 Lua 调用帧。
func TestDoStringDebugGetInfoReportsGlobalFunctionName(t *testing.T) {
	// 创建带 debug 标准库的 State，执行全局函数自查名称片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
function F(a)
  local info = debug.getinfo(1, "n")
  assert(info.name == "F" and info.namewhat == "global")
  return a, 2, 3
end
local x, y, z = F(1)
assert(x == 1 and y == 2 and z == 3)
`
	if err := DoString(state, source); err != nil {
		// 全局函数调用必须能为 debug.getinfo 提供名称元信息。
		t.Fatalf("DoString debug getinfo name failed: %v", err)
	}
}

// TestDoStringDebugGetInfoReportsLocalAndFieldFunctionName 验证调用点 local/field 名称。
//
// Lua 5.3 官方 db.lua 会断言本地函数调用显示 namewhat=local，字段函数调用显示 namewhat=field。
func TestDoStringDebugGetInfoReportsLocalAndFieldFunctionName(t *testing.T) {
	// 创建带 debug 标准库的 State，执行 local function 和 table field 两种调用点。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local g = {
  x = function()
    local current = debug.getinfo(1, "n")
    assert(current.name == "x" and current.namewhat == "field")
    return "xixi"
  end,
}
local f = function()
  local current = debug.getinfo(1, "n")
  assert(current.name == "f" and current.namewhat == "local")
  return g.x()
end
assert(f() == "xixi")
`
	if err := DoString(state, source); err != nil {
		// local/field 调用点名称必须能写入 Lua 调用帧。
		t.Fatalf("DoString debug local/field name failed: %v", err)
	}
}

// TestDoStringDebugLuaLineHookRuns 验证 Lua closure line hook 会由 VM 执行。
//
// 官方 db.lua 使用 Lua 函数作为 debug.sethook 回调；执行循环必须能触发该回调并传入
// event="line" 与当前行号。复杂控制流的精确行号轨迹由官方套件继续覆盖。
func TestDoStringDebugLuaLineHookRuns(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook 和 table 操作可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local hookCalls = 0
debug.sethook(function (event, line)
  assert(event == "line")
  assert(type(line) == "number" and line > 0)
  assert(debug.getinfo(1).namewhat == "hook")
  hookCalls = hookCalls + 1
end, "l")
assert(load([[a=1
b=2
]]))()
debug.sethook()
assert(hookCalls >= 2)
`
	if err := DoString(state, source); err != nil {
		// Lua line hook 必须被执行并记录前两条源码行。
		t.Fatalf("DoString debug Lua line hook failed: %v", err)
	}
}

// TestDoStringDebugSetHookSkipsCurrentLine 验证设置 line hook 后不报告当前行。
//
// 官方 db.lua 在 `debug.sethook(f,"l"); load(s)(); debug.sethook()` 同一行安装 hook 并执行新
// chunk；line hook 必须只看到新 chunk 的行号，不能额外报告 sethook 所在调用方行号。
func TestDoStringDebugSetHookSkipsCurrentLine(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook、load 和 math.sin 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local seen = {}
local chunk = [[if
math.sin(1)
then
  a=1
else
  a=2
end
]]
local function hook(event, line)
  assert(event == "line")
  seen[#seen + 1] = line
end
debug.sethook(hook, "l"); assert(load(chunk))(); debug.sethook()
assert(#seen == 4, table.concat(seen, ","))
assert(seen[1] == 2 and seen[2] == 3 and seen[3] == 4 and seen[4] == 7,
       table.concat(seen, ","))
`
	if err := DoString(state, source); err != nil {
		// line hook 轨迹必须与 Lua 5.3 官方 db.lua 的首个 test 用例一致。
		t.Fatalf("DoString sethook current line skip failed: %v", err)
	}
}

// TestDoStringDebugThreadLineHookRuns 验证 debug.sethook 的 thread 重载会绑定到目标协程。
//
// 官方 db.lua 使用 debug.sethook(co, hook, "lcr") 调试挂起协程；主线程 hook 不应被污染，
// 协程恢复执行时则必须触发该协程自己的 line hook。
func TestDoStringDebugThreadLineHookRuns(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine、debug.sethook 和 table 访问可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local lines = {}
local co = coroutine.create(function ()
  local x = 1
  coroutine.yield(x)
  x = x + 1
  return x
end)
debug.sethook(co, function (event, line)
  if event == "line" then
    lines[#lines + 1] = line
  end
end, "l")
assert(not debug.gethook())
assert(debug.gethook(co))
local ok, value = coroutine.resume(co)
assert(ok and value == 1)
assert(#lines > 0)
debug.sethook(co)
assert(not debug.gethook(co))
`
	if err := DoString(state, source); err != nil {
		// 协程专属 line hook 必须执行，并与主线程 hook 状态隔离。
		t.Fatalf("DoString debug thread line hook failed: %v", err)
	}
}

// TestDoStringDebugSetHookInsideCoroutineDoesNotPolluteMain 验证协程内无参 sethook 不污染主线程。
//
// 官方 all.lua 会先在 db.lua 的协程内设置 line hook，随后在 big.lua 中通过
// xpcall(debug.traceback) 检查 `__newindex` 元方法帧；协程 hook 若错误泄漏到主线程，会让后续
// traceback 丢失元方法名称。
func TestDoStringDebugSetHookInsideCoroutineDoesNotPolluteMain(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine、debug.sethook、load 和 xpcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local debug = require "debug"
local co = coroutine.wrap(function ()
  debug.sethook(function () end, "l")
  for i = 1, 2 do local x = i end
  assert(debug.gethook())
end)
co()
assert(not debug.gethook())

local env = {}
setmetatable(env, {__newindex = function () error("hi") end})
local f = assert(load("X = 1", nil, nil, env))
local ok, msg = xpcall(f, debug.traceback)
assert(not ok and msg:find("'__newindex'"))
`
	if err := DoString(state, source); err != nil {
		// 协程内无参 sethook 不能污染主线程后续 traceback。
		t.Fatalf("DoString coroutine sethook isolation failed: %v", err)
	}
}

// TestDoStringDebugTracebackUsesLibraryWhenGlobalCleared 验证全局 debug 被清空后 traceback 仍识别自身帧。
//
// 官方 all.lua 会先执行 `debug = nil`，后续 big.lua 通过 `local debug = require"debug"` 获得库表并把
// `debug.traceback` 作为 xpcall handler 裸传；此时不能依赖 `_G.debug` 反查 debug API 帧。
func TestDoStringDebugTracebackUsesLibraryWhenGlobalCleared(t *testing.T) {
	// 创建完整标准库 State，确保 require、debug.traceback、load 和 xpcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
debug = nil
local debug = require "debug"
local env = {}
setmetatable(env, {__newindex = function () error("hi") end})
local f = assert(load("X = 1", nil, nil, env))
local ok, msg = xpcall(f, debug.traceback)
assert(not ok and msg:find("'__newindex'"))
`
	if err := DoString(state, source); err != nil {
		// debug.traceback 必须使用注册时缓存的库表识别自身 Go 帧。
		t.Fatalf("DoString debug traceback after global clear failed: %v", err)
	}
}

// TestDoStringDebugThreadGetInfoReadsSuspendedFrame 验证 debug.getinfo 的 thread 重载。
//
// 官方 db.lua 在协程 yield 后通过 debug.getinfo(co, 1, "lfLS") 读取挂起帧；level 1 应指向
// 用户 Lua 函数帧，而不是 coroutine.yield 的 Go/C 边界帧。
func TestDoStringDebugThreadGetInfoReadsSuspendedFrame(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine 和 debug.getinfo 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local co = coroutine.create(function (x)
  local a = 1
  coroutine.yield(debug.getinfo(1, "l"))
  return a + x
end)
local ok, lineinfo = coroutine.resume(co, 10)
assert(ok and type(lineinfo) == "table")
local info = debug.getinfo(co, 1, "lfLS")
assert(info.currentline == lineinfo.currentline)
assert(type(info.func) == "function")
assert(info.activelines[info.currentline])
assert(not debug.getinfo(co, 2))
`
	if err := DoString(state, source); err != nil {
		// 挂起协程帧必须能被 debug.getinfo(thread, level) 读取。
		t.Fatalf("DoString debug thread getinfo failed: %v", err)
	}
}

// TestDoStringDebugThreadGetSetLocalReadsSuspendedFrame 验证 thread getlocal/setlocal 读取挂起帧。
//
// 官方 db.lua 在 coroutine.yield 后读取形参 x 和局部变量 a，并通过 setlocal 修改 a；本用例覆盖
// 挂起寄存器快照的读取与写入。
func TestDoStringDebugThreadGetSetLocalReadsSuspendedFrame(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine 和 debug local API 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local co = coroutine.create(function (x)
  local a = 1
  coroutine.yield(debug.getinfo(1, "l"))
  return a + x
end)
local ok = coroutine.resume(co, 10)
assert(ok)
local name, value = debug.getlocal(co, 1, 1)
assert(name == "x" and value == 10)
name, value = debug.getlocal(co, 1, 2)
assert(name == "a" and value == 1)
assert(debug.setlocal(co, 1, 2, "hi") == "a")
name, value = debug.getlocal(co, 1, 2)
assert(name == "a" and value == "hi")
`
	if err := DoString(state, source); err != nil {
		// 挂起协程局部变量必须可读取并写入快照。
		t.Fatalf("DoString debug thread get/setlocal failed: %v", err)
	}
}

// TestDoStringCoroutineContinuationResumesAfterYield 验证 Lua coroutine yield 后继续执行。
//
// 官方 db.lua 要求第二次 coroutine.resume 从上次 coroutine.yield 调用后继续执行，并让
// debug.setlocal(thread, ...) 在挂起期间写入的局部变量影响最终返回值。
func TestDoStringCoroutineContinuationResumesAfterYield(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine、debug 与 pcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local co = coroutine.create(function (x)
  local a = 1
  coroutine.yield(debug.getinfo(1, "l"))
  coroutine.yield(debug.getinfo(1, "l").currentline)
  return a
end)
local ok, lineinfo = coroutine.resume(co, 10)
assert(ok and type(lineinfo) == "table")
assert(debug.setlocal(co, 1, 2, "hi") == "a")
local protected, resumed, line = pcall(coroutine.resume, co)
assert(protected and resumed and line == lineinfo.currentline + 1)
ok, line = coroutine.resume(co)
assert(ok and line == "hi")
assert(coroutine.status(co) == "dead")
`
	if err := DoString(state, source); err != nil {
		// continuation 必须恢复到 yield 后，并读取被 debug.setlocal 修改的 local。
		t.Fatalf("DoString coroutine continuation failed: %v", err)
	}
}

// TestDoStringDebugRecursiveCoroutineTraceback 验证递归协程的挂起与错误 traceback。
//
// 官方 db.lua 会在递归 coroutine 中多次 yield，再在最终 error 后继续读取 dead coroutine
// traceback；递归函数 f 是外层 local 被 function 语句复写后的 upvalue 形态，需要保留
// coroutine.yield、递归 f 帧和匿名 coroutine wrapper 帧。
func TestDoStringDebugRecursiveCoroutineTraceback(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine、debug、string 和 table 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function checktraceback (co, p, level)
  local tb = debug.traceback(co, nil, level)
  local i = 0
  for l in string.gmatch(tb, "[^\n]+\n?") do
    assert(i == 0 or string.find(l, p[i]), tb)
    i = i + 1
  end
  assert(p[i] == nil, tb)
end
local f = function (i) return i end
function f(i) if i == 0 then error(i) else coroutine.yield(); f(i - 1) end end
local co = coroutine.create(function (x) f(x) end)
local a, b = coroutine.resume(co, 3)
local t = {"'coroutine.yield'", "'f'", "in function <"}
while coroutine.status(co) == "suspended" do
  checktraceback(co, t)
  a, b = coroutine.resume(co)
  table.insert(t, 2, "'f'")
end
t[1] = "'error'"
checktraceback(co, t)
`
	if err := DoString(state, source); err != nil {
		// traceback 文本必须匹配官方递归协程断言。
		t.Fatalf("DoString recursive coroutine traceback failed: %v", err)
	}
}

// TestDoStringDebugCallReturnHookReportsFunction 验证 call/return hook 的被调函数元数据。
//
// 官方 db.lua 在 crl hook 中用 debug.getinfo(2, "f") 记录 Lua closure 与 Go closure；VM 必须
// 在被调帧可见时触发 hook，且 Go closure 也需要临时调试帧。
func TestDoStringDebugCallReturnHookReportsFunction(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook、assert 与 debug.getinfo 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local seen = {}
local returns = 0
local function f()
  assert(true)
end
debug.sethook(function (event)
  if event == "call" then
    local info = debug.getinfo(2, "f")
    seen[info.func] = true
  else
    assert(event == "return")
    returns = returns + 1
  end
end, "cr")
f()
debug.getlocal(1, 1)
debug.sethook()
assert(seen[f])
assert(seen[assert])
assert(seen[debug.getlocal])
assert(not seen[print])
assert(returns > 0)
`
	if err := DoString(state, source); err != nil {
		// call/return hook 必须能观察 Lua 与 Go closure 的被调函数。
		t.Fatalf("DoString debug call/return hook failed: %v", err)
	}
}

// TestDoStringDebugReturnHookSeesEmptyReturnFrame 验证裸 return 也会触发 return hook。
//
// 官方 db.lua 在 `local function foo(...) return end` 的 return hook 中读取 `debug.getinfo(2)` 和
// 活动 local；VM 必须把 0 返回值 RETURN 与“未返回”区分开。
func TestDoStringDebugReturnHookSeesEmptyReturnFrame(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook 与 debug.getinfo 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local seen = false
local function foo(a, b)
  do local x, y, z end
  local c, d = 10, 20
  return
end
debug.sethook(function (event)
  if event == "return" then
    local info = debug.getinfo(2)
    if info.name == "foo" then
      local locals = {}
      for i = 1, 10 do
        local name, value = debug.getlocal(2, i)
        if not name then break end
        locals[name] = value
      end
      assert(locals.a == 100 and locals.b == 200 and locals.c == 10 and locals.d == 20)
      seen = true
    end
  end
end, "r")
foo(100, 200)
debug.sethook()
assert(seen)
`
	if err := DoString(state, source); err != nil {
		// 裸 return 函数帧必须在 return hook 中可见。
		t.Fatalf("DoString empty return hook frame failed: %v", err)
	}
}

// TestDoStringDebugReturnHookSkipsNamedHookFrame 验证命名 hook 函数自身返回不会重入 return hook。
//
// 官方 all.lua 通过 dump/load 运行 db.lua；命名 `aux` hook 不能在自身 return 时再次触发 hook，
// 否则 `debug.getinfo(2)` 会看到 aux 而不是正在裸 return 的 foo。
func TestDoStringDebugReturnHookSkipsNamedHookFrame(t *testing.T) {
	// 创建完整标准库 State，确保 load、string.dump、debug.sethook 与 debug.getlocal 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local chunk = [[
local seen = 0
local function foo(a, b)
  do local x, y, z end
  local c, d = 10, 20
  return
end
local function aux(event)
  if event == "return" then
    local info = debug.getinfo(2)
    assert(info.name ~= "aux")
    if info.name == "foo" then
      local locals = {}
      for i = 1, 10 do
        local name, value = debug.getlocal(2, i)
        if not name then break end
        locals[name] = value
      end
      assert(locals.a == 100 and locals.b == 200 and locals.c == 10 and locals.d == 20)
      seen = seen + 1
    end
  end
end
debug.sethook(aux, "r")
foo(100, 200)
debug.sethook()
assert(seen == 1)
]]
assert(load(string.dump(assert(load(chunk, "@return-hook.lua")))))()
`
	if err := DoString(state, source); err != nil {
		// 命名 hook 自身返回必须被屏蔽，真实 foo 返回帧仍要保持可见。
		t.Fatalf("DoString named return hook frame failed: %v", err)
	}
}

// TestDoStringDebugReturnHookLevelSkipsLocalHookFrame 验证 hook 回调帧不参与 debug level 计数。
//
// 官方 db.lua 在 dump/load 入口下使用局部函数 aux 作为 return hook；aux 自身虽然可被调试名推断为
// local，但 `debug.getinfo(2)` 必须跳过 hook 回调帧并看到正在返回的 foo。
func TestDoStringDebugReturnHookLevelSkipsLocalHookFrame(t *testing.T) {
	// 创建完整标准库 State，确保 dump/load、debug hook 和局部变量查询可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local chunk = [[
local function collectlocals(level)
  local tab = {}
  for i = 1, 10 do
    local name, value = debug.getlocal(level + 1, i)
    if not name then break end
    tab[name] = value
  end
  return tab
end
do
  local function foo(a, b)
    do local x, y, z end
    local c, d = 10, 20
    return
  end

  local function aux()
    local info = debug.getinfo(2)
    if info.name == "foo" then
      foo = nil
      local locals = collectlocals(2)
      assert(locals.a == 100 and locals.b == 200 and locals.c == 10 and locals.d == 20)
    end
  end

  debug.sethook(aux, "r"); foo(100, 200); debug.sethook()
  assert(foo == nil)
end
]]
assert(load(string.dump(assert(load(chunk, "@return-hook-level.lua")))))()
`
	if err := DoString(state, source); err != nil {
		// return hook 内的 level 查询必须跳过 hook 回调帧。
		t.Fatalf("DoString return hook level skip failed: %v", err)
	}
}

// TestDoStringPcallOpenReturnFeedsOuterCall 验证 pcall 开放返回可继续作为外层调用实参。
//
// 官方 db.lua 在 dumped chunk 中执行 `assert(not pcall(...))`；pcall 内部执行器必须把 CALL C=0
// 的实际返回上界记录给下一条 CALL B=0，避免外层调用读取实参时触发寄存器越界。
func TestDoStringPcallOpenReturnFeedsOuterCall(t *testing.T) {
	// 创建完整标准库 State，确保 print、pcall、load 与 string.dump 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local chunk = [[
local function sink (...) return ... end
local ok = {sink("A", pcall(function () end))}
assert(ok[1] == "A")
assert(ok[2] == true)
assert(ok[3] == nil)
assert(({pcall(function () end)})[1] == true)
assert(not pcall(error, "boom"))
assert(not pcall(debug.getinfo, print, "X"))
]]
assert(load(string.dump(assert(load(chunk, "@pcall-open-return.lua")))))()
`
	if err := DoString(state, source); err != nil {
		// pcall 开放返回必须能喂给外层 CALL B=0 和表构造表达式。
		t.Fatalf("DoString pcall open return failed: %v", err)
	}
}

// TestDoStringPcallTailCallDebugInfo 验证 pcall 内尾调用仍保留 debug tail 元信息。
//
// 官方 db.lua 的 tail-call 小节会在 dump/load 入口下通过 pcall/xpcall 间接执行 Lua closure；
// base protected-call 执行器必须保留 tail call 的调试名称和 istailcall 标记。
func TestDoStringPcallTailCallDebugInfo(t *testing.T) {
	// 创建完整标准库 State，确保 pcall、debug.getinfo、load 与 string.dump 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local chunk = [[
local function f (x)
  if x then
    assert(debug.getinfo(1, "t").istailcall == true)
    local tail = debug.getinfo(2)
    assert(tail.func == g1 and tail.istailcall == true)
    assert(debug.getinfo(3, "S").what == "C")
  end
end
function g(x) return f(x) end
function g1(x) g(x) end
local function h (x) local f=g1; return f(x) end
assert(pcall(h, true))
]]
assert(load(string.dump(assert(load(chunk, "@pcall-tail.lua")))))()
`
	if err := DoString(state, source); err != nil {
		// pcall 内尾调用必须保持 debug.getinfo 可见的 tail 元信息。
		t.Fatalf("DoString pcall tail call debug info failed: %v", err)
	}
}

// TestDoStringDebugCallReturnHookLineArgumentIsNil 验证非 line hook 不传行号。
//
// Lua 5.3 对 call/return hook 的第二个参数没有有效行号；传 0 会在 Lua 中被 `if line then`
// 当作真值，官方 db.lua 的 coroutine hook 依赖 nil 语义过滤非 line 事件。
func TestDoStringDebugCallReturnHookLineArgumentIsNil(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook 和函数调用可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local seen = 0
debug.sethook(function (event, line)
  assert(event == "call" or event == "return")
  assert(line == nil)
  seen = seen + 1
end, "cr")
local function f() return 1 end
assert(f() == 1)
debug.sethook()
assert(seen > 0)
`
	if err := DoString(state, source); err != nil {
		// call/return hook 不应向 Lua 回调传入 0 行号。
		t.Fatalf("DoString debug call/return nil line failed: %v", err)
	}
}

// TestDoStringDebugLocalRejectsInvalidLevel 验证 getlocal/setlocal 的非法 level 错误。
//
// Lua 5.3 官方 db.lua 要求已有调用栈中越界 level 通过 pcall 捕获为失败，而不是返回 nil。
func TestDoStringDebugLocalRejectsInvalidLevel(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getlocal/debug.setlocal 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
assert(not pcall(debug.getlocal, 20, 1))
assert(not pcall(debug.setlocal, -1, 1, 10))
`
	if err := DoString(state, source); err != nil {
		// 非法 level 必须被 pcall 捕获为 Lua error。
		t.Fatalf("DoString debug invalid local level failed: %v", err)
	}
}

// TestDoStringDebugGetLocalReadsFunctionParameters 验证 getlocal 的函数形参名查询。
//
// Lua 5.3 官方 db.lua 会调用 debug.getlocal(function, index) 和 debug.getlocal(thread, function, index)
// 读取固定形参名称；Go/C closure 查询形参应返回 nil。
func TestDoStringDebugGetLocalReadsFunctionParameters(t *testing.T) {
	// 创建完整标准库 State，确保 debug 与 coroutine 都可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function foo (a,b,...) local d, e end
local co = coroutine.create(foo)
assert(debug.getlocal(foo, 1) == "a")
assert(debug.getlocal(foo, 2) == "b")
assert(not debug.getlocal(foo, 3))
assert(debug.getlocal(co, foo, 1) == "a")
assert(debug.getlocal(co, foo, 2) == "b")
assert(not debug.getlocal(co, foo, 3))
assert(not debug.getlocal(print, 1))
`
	if err := DoString(state, source); err != nil {
		// 函数形参查询必须与官方 db.lua 断言一致。
		t.Fatalf("DoString debug function local failed: %v", err)
	}
}

// TestDoStringDebugSetLocalWritesVararg 验证 setlocal 负索引写回 vararg。
//
// Lua 5.3 官方 db.lua 会在内层函数中调用 debug.setlocal(2, -i, x) 修改外层函数的 vararg；
// 随后的 `...` 必须读取到被改写后的值。
func TestDoStringDebugSetLocalWritesVararg(t *testing.T) {
	// 创建完整标准库 State，确保 debug.setlocal 与 table.pack 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function foo (a, ...)
  local t = table.pack(...)
  for i = 1, t.n do
    local n, v = debug.getlocal(1, -i)
    assert(n == "(*vararg)" and v == t[i])
  end
  assert(not debug.setlocal(1, -(t.n + 1), 30))
  if t.n > 0 then
    (function (x)
      assert(debug.setlocal(2, -1, x) == "(*vararg)")
      assert(debug.setlocal(2, -t.n, x) == "(*vararg)")
    end)(430)
    assert(... == 430)
  end
end
foo(1, 10, 20)
`
	if err := DoString(state, source); err != nil {
		// vararg 写回必须能影响后续 `...` 展开。
		t.Fatalf("DoString debug setlocal vararg failed: %v", err)
	}
}

// TestDoStringDebugSetLocalWritesActiveLocal 验证 setlocal 正索引写回活动局部变量。
//
// Lua 5.3 官方 db.lua 在函数内部读取参数 a/b，再通过 debug.setlocal(1, 2, 10) 修改 b；
// 后续表达式必须看到新值。
func TestDoStringDebugSetLocalWritesActiveLocal(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getlocal/debug.setlocal 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function f(a, b)
  local _, x = debug.getlocal(1, 1)
  local _, y = debug.getlocal(1, 2)
  assert(x == a and y == b)
  assert(debug.setlocal(1, 2, 10) == "b")
  assert(b == 10)
  return a + b
end
assert(f(1, 2) == 11)
`
	if err := DoString(state, source); err != nil {
		// 正索引 local 写回必须影响活动寄存器。
		t.Fatalf("DoString debug setlocal active local failed: %v", err)
	}
}

// TestDoStringDebugGetLocalLevelZeroTemporaries 验证 debug.getlocal level 0 临时槽位。
//
// 官方 db.lua 要求 debug.getlocal(0, 1/2) 返回当前 debug.getlocal 调用自身的临时参数，
// 名称为 `(*temporary)`；越界和 0 索引返回 nil。
func TestDoStringDebugGetLocalLevelZeroTemporaries(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getlocal 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local n, v = debug.getlocal(0, 1)
assert(v == 0 and n == "(*temporary)")
n, v = debug.getlocal(0, 2)
assert(v == 2 and n == "(*temporary)")
assert(not debug.getlocal(0, 3))
assert(not debug.getlocal(0, 0))
`
	if err := DoString(state, source); err != nil {
		// level 0 temporary local 语义必须与官方 db.lua 对齐。
		t.Fatalf("DoString debug getlocal level0 temporaries failed: %v", err)
	}
}

// TestDoStringDebugSetLocalWritesTemporaryRegister 验证 setlocal 写回外层临时寄存器。
//
// 官方 db.lua 在 `return (a+1) + f()` 中从 f 读取 caller 的第三个 local，实际是表达式
// `a+1` 的 temporary register；setlocal 写回后应改变外层表达式结果。
func TestDoStringDebugSetLocalWritesTemporaryRegister(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getlocal/debug.setlocal 与 select 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
function f()
  assert(select(2, debug.getlocal(2, 3)) == 1)
  assert(not debug.getlocal(2, 4))
  assert(debug.setlocal(2, 3, 10) == "(*temporary)")
  return 20
end
function g(a, b)
  return (a + 1) + f()
end
assert(g(0, 0) == 30)
`
	if err := DoString(state, source); err != nil {
		// temporary register 写回必须影响外层表达式后续计算。
		t.Fatalf("DoString debug setlocal temporary register failed: %v", err)
	}
}

// TestDoStringDebugNameCacheTracksGlobalMutation 验证 debug 名称缓存随全局表变更失效。
//
// 同一个本地 closure 先以无全局名调用形成负缓存，随后赋给全局变量必须重新推断名称；
// 删除全局变量后再次调用则应回到无名称，避免缓存返回过期 name。
func TestDoStringDebugNameCacheTracksGlobalMutation(t *testing.T) {
	// 创建带 debug 标准库的 State，执行全局赋值前后名称变化片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local expect
local expectWhat
local function f()
  local info = debug.getinfo(1, "n")
  assert(info.name == expect and info.namewhat == expectWhat)
end
expect = "f"
expectWhat = "local"
f()
G = f
expect = "G"
expectWhat = "global"
G()
G = nil
expect = "f"
expectWhat = "local"
f()
`
	if err := DoString(state, source); err != nil {
		// 全局表变更后缓存必须重新推断或清空名称。
		t.Fatalf("DoString debug name cache mutation failed: %v", err)
	}
}

// TestDoStringDebugLocalFunctionCallAfterBreak 验证控制流占位跳转后 local 调用名不丢失。
//
// 官方 db.lua 在已有 `local f` 作用域内使用 `function f (...) ... end` 重写局部函数，
// 随后执行 `if ... then break end; f()`；CALL 前的 close-only JMP 不能被误判为短路表达式。
func TestDoStringDebugLocalFunctionCallAfterBreak(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getinfo 可读取当前 Lua 帧的调用点名称。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
repeat
  local f = function () return 1 end
  function f (x, name)
    name = name or "f"
    local info = debug.getinfo(1)
    assert(info.name == name and info.namewhat == "local")
    return x
  end
  if 3 > 4 then break end; f()
  if 3 < 4 then _G.db_name_probe = 1 else break end; f()
  while 1 do local x = 10; break end; f()
  repeat local x = 20; if 4 > 3 then f() else break end; f() until 1
until 1
`
	if err := DoString(state, source); err != nil {
		// 控制流后的 local 调用名必须对齐官方 db.lua:93。
		t.Fatalf("DoString debug local call after break failed: %v", err)
	}
}

// TestDoStringExpandsTrailingCallArguments 验证函数调用尾部实参展开。
//
// Lua 5.3 中 `select(2, load(badChunk))` 必须把 load 的第二返回值作为 select 的第二个
// 实参传入；官方 constructs.lua 用该语义检查语法错误消息。
func TestDoStringExpandsTrailingCallArguments(t *testing.T) {
	// 创建带标准库的 State，执行 load 失败返回值经 select 展开的片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local message = select(2, load("for x do"))
assert(type(message) == "string" and string.find(message, "expected"))
`
	if err := DoString(state, source); err != nil {
		// 尾部函数调用实参必须保留 load 的错误消息返回值。
		t.Fatalf("DoString trailing call arguments failed: %v", err)
	}
}

// TestDoStringSelectCountFixedResultDoesNotLeakArguments 验证 select 计数固定返回不会泄漏实参。
//
// 顶层 `select("#", ...)` 会编译成固定单返回 CALL；后续调用必须只读取显式传入的
// 实参，不能复用上一条 CALL 的参数槽、开放返回状态或未清理栈顶。
func TestDoStringSelectCountFixedResultDoesNotLeakArguments(t *testing.T) {
	// 创建带标准库的 State，执行顶层 select 计数后继续调用 vararg 函数。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local count = select("#", "alpha", "beta")
local function diag(...)
  return select("#", ...), ...
end
local n, first, second = diag(count, "tail")
assert(count == 2)
assert(n == 2 and first == 2 and second == "tail")
local packed = {select("#", "alpha", "beta")}
assert(#packed == 1 and packed[1] == 2)
`
	if err := DoString(state, source); err != nil {
		// 固定单返回 CALL 不应污染后续 vararg 调用或 table constructor。
		t.Fatalf("DoString select count fixed result failed: %v", err)
	}
	if state.StackTop() != 0 {
		// chunk 执行结束后公开 State 栈应保持清空。
		t.Fatalf("DoString select count left stack values: top=%d", state.StackTop())
	}
}

// TestDoStringPrintUsesGlobalToString 验证 print 按 Lua 5.3 调用当前全局 tostring。
//
// 官方 calls.lua 会临时把 `_G.tostring` 设为 nil 或返回非 string 的函数；print 必须抛错，
// 让 pcall(print, value) 返回 false 和可匹配的错误文本。
func TestDoStringPrintUsesGlobalToString(t *testing.T) {
	// 创建带标准库的 State，执行覆盖全局 tostring 的 print 错误路径。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local original = tostring
tostring = nil
local ok, message = pcall(print, 1)
assert(ok == false and string.find(message, "attempt to call a nil value"))
tostring = function() return {} end
ok, message = pcall(print, 1)
assert(ok == false and string.find(message, "must return a string"))
tostring = original
`
	if err := DoString(state, source); err != nil {
		// print 必须通过全局 tostring 并保留错误文本。
		t.Fatalf("DoString print global tostring failed: %v", err)
	}
}

// TestDoStringTableSortLuaComparatorIgnoresExtraArgs 验证 table.sort 支持 Lua comparator 并忽略额外参数。
//
// 官方 calls.lua 会传入第三个无关参数；Lua 5.3 应忽略该参数，只用第二参数 comparator 排序。
func TestDoStringTableSortLuaComparatorIgnoresExtraArgs(t *testing.T) {
	// 创建带标准库的 State，执行 table.sort Lua comparator 路径。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local values = {10, 9, 8, 4, 19, 23, 0, 0}
table.sort(values, function(a, b) return a < b end, "extra arg")
for index = 2, #values do
  assert(values[index - 1] <= values[index])
end
`
	if err := DoString(state, source); err != nil {
		// Lua comparator 与额外参数必须兼容官方 calls.lua。
		t.Fatalf("DoString table.sort Lua comparator failed: %v", err)
	}
}

// TestDoStringLoadReaderFunction 验证 load 支持 reader function 和 mode 校验。
//
// 官方 calls.lua 使用逐字符 reader 读取含 NUL 的文本 chunk；binary-only mode 必须拒绝文本，
// reader 返回非 string/nil 时必须加载失败。
func TestDoStringLoadReaderFunction(t *testing.T) {
	// 创建带标准库的 State，执行 reader function 形式的 load。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
local chunk = "x = 10 + 23; return '\\0'"
local i = 0
local function reader()
  i = i + 1
  return string.sub(chunk, i, i)
end
local f = assert(load(reader, "modname", "t", _G))
assert(f() == "\0" and x == 33)
assert(debug.getinfo(f).source == "modname")
local bad, message = load(chunk, "modname", "b")
assert(not bad and string.find(message, "attempt to load a text chunk"))
bad, message = load(function() return true end)
assert(not bad and string.find(message, "reader function"))
bad, message = load("*a = 123")
assert(not bad and string.find(message, "unexpected symbol"))
`
	if err := DoString(state, source); err != nil {
		// load(reader) 必须兼容官方 calls.lua。
		t.Fatalf("DoString load reader function failed: %v", err)
	}
}

// TestProtectedCallWrapper 验证 lua.ProtectedCall 包装。
//
// 包装函数必须保留 runtime protected call 的回滚语义，并对 nil 参数返回明确错误。
func TestProtectedCallWrapper(t *testing.T) {
	// nil State 和 nil callback 必须返回稳定错误。
	if err := ProtectedCall(nil, func(state *State) error { return nil }); !errors.Is(err, ErrNilState) {
		t.Fatalf("ProtectedCall nil state should return ErrNilState, got=%v", err)
	}
	state := NewState()
	defer state.Close()
	if err := ProtectedCall(state, nil); !errors.Is(err, ErrNilProtectedCall) {
		t.Fatalf("ProtectedCall nil callback should return ErrNilProtectedCall, got=%v", err)
	}

	// 成功路径保留栈变更，错误路径回滚栈变更。
	if err := ProtectedCall(state, func(callState *State) error {
		// 成功路径压入一个返回值。
		return callState.Push(runtime.StringValue("ok"))
	}); err != nil {
		t.Fatalf("ProtectedCall success failed: %v", err)
	}
	if state.StackTop() != 1 {
		// 成功路径必须保留栈变更。
		t.Fatalf("ProtectedCall success stack top = %d", state.StackTop())
	}
	err := ProtectedCall(state, func(callState *State) error {
		// 错误路径先压栈再返回错误，用于验证回滚。
		if pushErr := callState.Push(runtime.StringValue("temp")); pushErr != nil {
			return pushErr
		}
		return ErrExecutionUnavailable
	})
	if !errors.Is(err, ErrExecutionUnavailable) {
		// 错误链必须保留原始错误。
		t.Fatalf("ProtectedCall error = %v, want ErrExecutionUnavailable", err)
	}
	if state.StackTop() != 1 {
		// 错误路径必须回滚到进入前栈顶。
		t.Fatalf("ProtectedCall error stack top = %d", state.StackTop())
	}
}

// TestSetContextNilState 验证 SetContext 对空 State 的防护。
//
// nil state 应返回 ErrNilState 而不是触发 panic。
func TestSetContextNilState(t *testing.T) {
	if err := SetContext(nil, context.Background()); !errors.Is(err, ErrNilState) {
		t.Fatalf("SetContext nil state should return ErrNilState, got=%v", err)
	}
}

// TestDoStringStringMetatableLuaBitwiseMetamethod 验证 string 类型级 Lua 元方法。
//
// 官方 bwcoercion.lua 会通过 getmetatable("") 给字符串元表动态挂载位运算元方法；VM 指令执行
// 必须能从 string 类型元表找到 Lua closure 元方法并通过当前 State 调用。
func TestDoStringStringMetatableLuaBitwiseMetamethod(t *testing.T) {
	// 创建完整标准库 State，确保 string 类型元表已经由 string 库注册。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local mt = getmetatable("")
assert(type(mt) == "table")
mt.__band = function (x, y)
  return tonumber(x) & y
end
assert(("6" & 3) == 2)
mt.__band = nil
`
	if err := DoString(state, source); err != nil {
		// 字符串类型级 Lua closure 位运算元方法必须可被 VM 调用。
		t.Fatalf("DoString string bitwise metamethod failed: %v", err)
	}
}

// TestDoStringDebugTagMethodLuaClosureName 验证 Lua closure 元方法的 debug 名称。
//
// 官方 db.lua 的 tagmethod 小节会在 `__index`、算术、位运算、比较和一元元方法内部调用
// debug.getinfo(1)，要求 namewhat 为 metamethod，name 为当前触发的元方法名。
func TestDoStringDebugTagMethodLuaClosureName(t *testing.T) {
	// 创建完整标准库 State，确保 debug、table 元表和 VM 元方法 runner 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local a = {}
local function f (t)
  local info = debug.getinfo(1)
  assert(info.namewhat == "metamethod")
  a.op = info.name
  return info.name
end
setmetatable(a, {
  __index = f; __add = f; __div = f; __mod = f; __concat = f; __pow = f;
  __mul = f; __idiv = f; __unm = f; __len = f; __sub = f;
  __shl = f; __shr = f; __bor = f; __bxor = f;
  __eq = f; __le = f; __lt = f; __band = f; __bnot = f;
})
local b = setmetatable({}, getmetatable(a))
assert(a[3] == "__index" and a^3 == "__pow" and a..a == "__concat")
assert(a/3 == "__div" and 3%a == "__mod")
assert(a+3 == "__add" and 3-a == "__sub" and a*3 == "__mul" and
       -a == "__unm" and #a == "__len" and a&3 == "__band")
assert(a|3 == "__bor" and 3~a == "__bxor" and a<<3 == "__shl" and
       a>>1 == "__shr")
assert(a==b and a.op == "__eq")
assert(a>=b and a.op == "__le")
assert(a>b and a.op == "__lt")
assert(~a == "__bnot")
`
	if err := DoString(state, source); err != nil {
		// tagmethod 内部 debug 名称必须与官方 db.lua 断言一致。
		t.Fatalf("DoString debug tagmethod name failed: %v", err)
	}
}

// TestDoStringCoroutineYieldInEqualityMetamethod 验证 __eq 元方法 yield 后恢复比较表达式。
//
// 官方 coroutine.lua 的 `testing yields inside metamethods` 小节会在 `a==b` 的 `__eq` 中
// yield；恢复后必须继续完成原 OP_EQ 测试并进入 false 分支，而不是把后续寄存器误当函数调用。
func TestDoStringCoroutineYieldInEqualityMetamethod(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine 与元方法 Lua runner 都可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行协程兼容脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local mt = {}
local function f(a, b)
  coroutine.yield(nil, "eq")
  return a.x == b.x
end
mt.__eq = f
mt.__le = function(a, b)
  coroutine.yield(nil, "le")
  return a - b <= 0
end
mt.__sub = function(a, b)
  coroutine.yield(nil, "sub")
  return a.x - b.x
end
local a = setmetatable({x = 1}, mt)
local b = setmetatable({x = 2}, mt)

local function run(fn, expected)
  local index = 1
  local co = coroutine.wrap(fn)
  while true do
    local result, status = co()
    if result then
      assert(expected[index] == nil)
      return result
    end
    assert(status == expected[index])
    index = index + 1
  end
end

assert(run(function()
  if a == b then
    return "=="
  else
    return "~="
  end
end, {"eq"}) == "~=")
assert(run(function()
  if a >= b then
    return ">="
  else
    return "<"
  end
end, {"le", "sub"}) == "<")
`
	if err := DoString(state, source); err != nil {
		// __eq yield 恢复必须回到原比较表达式并产生 false 分支结果。
		t.Fatalf("DoString equality metamethod yield failed: %v", err)
	}
}

// TestDoStringCoroutineYieldInConcatMetamethod 验证连续 CONCAT 元方法 yield 后继续折叠。
//
// 官方 coroutine.lua 会在 `a .. b .. c .. a` 这类单条 OP_CONCAT 内连续多次触发
// __concat 并 yield；恢复时必须把每次元方法返回值作为中间折叠结果，而不是提前跳到下一条
// 指令返回半成品。
func TestDoStringCoroutineYieldInConcatMetamethod(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine 与 Lua 元方法 runner 都可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行协程兼容脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local mt = {
  __concat = function(a, b)
    coroutine.yield(nil, "concat")
    a = type(a) == "table" and a.x or a
    b = type(b) == "table" and b.x or b
    return a .. b
  end,
}
local function new(x)
  return setmetatable({x = x}, mt)
end
local a = new(10)
local b = new(12)
local c = new("hello")

local function run(fn, expected)
  local index = 1
  local co = coroutine.wrap(fn)
  while true do
    local result, status = co()
    if result then
      assert(expected[index] == nil)
      return result
    end
    assert(status == expected[index])
    index = index + 1
  end
end

assert(run(function()
  return a .. b
end, {"concat"}) == "1012")
assert(run(function()
  return a .. b .. c .. a
end, {"concat", "concat", "concat"}) == "1012hello10")
assert(run(function()
  return "a" .. "b" .. a .. "c" .. c .. b .. "x"
end, {"concat", "concat", "concat"}) == "ab10chello12x")
`
	if err := DoString(state, source); err != nil {
		// 连续 CONCAT yield 必须保持官方 coroutine.lua 的恢复顺序和最终结果。
		t.Fatalf("DoString concat metamethod yield failed: %v", err)
	}
}

// TestDoStringOfficialBetterErrorMessages 验证官方 errors.lua 的 better error messages 小节。
//
// 该小节只匹配错误文本中的关键片段；测试覆盖位运算、比较、调用、拼接和长度运算的 Lua 5.3
// 用户可见类型名及调用点来源。
func TestDoStringOfficialBetterErrorMessages(t *testing.T) {
	// 创建完整标准库 State，确保 pcall、ipairs、string.find 和全局 print 都可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行官方错误消息探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local cases = {
  {function() return {} | 1 end, "bitwise operation", nil},
  {function() return {} < 1 end, "attempt to compare", nil},
  {function() bbbb = 2; return bbbb(3) end, "global 'bbbb'", nil},
  {function() local a = {}; return a:bbbb(3) end, "method 'bbbb'", nil},
  {function() local a = {}; return a.bbbb(3) end, "field 'bbbb'", nil},
  {function() local a = {13}; local bbbb = 1; return a[bbbb](3) end, "number", "bbbb"},
  {function() return (1)..{} end, "a table value", nil},
  {function() return #print end, "length of a function value", nil},
  {function() return #3 end, "length of a number value", nil},
}
for i, case in ipairs(cases) do
  local ok, message = pcall(case[1])
  assert(not ok, "case " .. i .. " unexpectedly succeeded")
  message = tostring(message)
  assert(string.find(message, case[2], 1, true), "case " .. i .. " missing " .. case[2] .. " in " .. message)
  if case[3] then
    assert(not string.find(message, case[3], 1, true), "case " .. i .. " should not mention " .. case[3] .. " in " .. message)
  end
end
local ok, message = pcall(function() aaa = {}; return (aaa or aaa) + (aaa and aaa) end)
assert(not ok, "short-circuit arithmetic unexpectedly succeeded")
message = tostring(message)
assert(string.find(message, "arithmetic", 1, true), message)
assert(not string.find(message, "'aaa'", 1, true), message)
ok, message = pcall(function() aaa = {}; return (aaa or aaa)() end)
assert(not ok, "short-circuit call unexpectedly succeeded")
message = tostring(message)
assert(string.find(message, "call a table value", 1, true), message)
assert(not string.find(message, "'aaa'", 1, true), message)
`
	if err := DoString(state, source); err != nil {
		// 错误文本必须包含官方断言需要的关键片段。
		t.Fatalf("DoString better error messages failed: %v", err)
	}
}

// TestDoStringNilEmptyMetatableStillReportsIndexReceiver 验证 nil 空元表不会吞掉索引错误。
//
// 官方 all.lua 会在 events.lua 末尾留下 `debug.setmetatable(nil, {})`；随后 errors.lua 仍要求
// `aaa.bbb:ddd(9)` 在访问 nil 全局 `aaa` 时立即报错，并在错误文本中包含 `global 'aaa'`。
func TestDoStringNilEmptyMetatableStillReportsIndexReceiver(t *testing.T) {
	// 清理基础类型全局元表，避免当前测试失败时污染后续用例。
	defer runtime.SetBasicTypeMetatable(runtime.NilValue(), nil)

	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行 debug.setmetatable 和 pcall。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local debug = require "debug"
debug.setmetatable(nil, {})
local function doit (s)
  local f = assert(load(s))
  local ok, message = pcall(f)
  return (not ok) and tostring(message)
end
aaa = nil
local message = doit("aaa.bbb:ddd(9)")
assert(string.find(message, "global 'aaa'", 1, true), message)
`
	if err := DoString(state, source); err != nil {
		// nil 空元表不能把索引错误延后到 method 调用点。
		t.Fatalf("DoString nil empty metatable index error failed: %v", err)
	}
}

// TestDoStringOfficialNamedObjectErrorMessages 验证官方 errors.lua 的 named object 错误文本。
//
// Lua 5.3 会在 table/userdata 元表提供字符串型 `__name` 时，把该名称用于参数错误、算术、
// 位运算和有序比较错误。本测试覆盖官方 named objects 小节中的关键匹配片段。
func TestDoStringOfficialNamedObjectErrorMessages(t *testing.T) {
	// 创建完整标准库 State，确保 math/io/debug/setmetatable 等依赖可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行 named object 探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function check(prog, want)
  local fn = assert(load(prog))
  local ok, message = pcall(fn)
  assert(not ok, prog)
  message = tostring(message)
  assert(string.find(message, want, 1, true), message)
end
XX = setmetatable({}, {__name = "My Type"})
check("return math.sin(io.input())", "number expected, got FILE*")
check("return io.input(XX)", "FILE* expected, got My Type")
check("return XX + 1", "on a My Type value")
check("return ~io.stdin", "on a FILE* value")
check("return XX < XX", "two My Type values")
check("return {} < XX", "table with My Type")
check("return XX < io.stdin", "My Type with FILE*")
`
	if err := DoString(state, source); err != nil {
		// named object 错误文本必须包含官方断言需要的类型名片段。
		t.Fatalf("DoString named object error messages failed: %v", err)
	}
}

// TestDoStringOfficialStrippedRuntimeErrorPrefix 验证 stripped chunk 运行期错误位置前缀。
//
// Lua 5.3 errors.lua 会把 strip=true 的 binary chunk 重新 load 后触发运行期错误，并要求错误
// 文本以 `?:-1:` 开头；无 debug info 时不应泄露 local 名称，但仍要保留操作数类型。
func TestDoStringOfficialStrippedRuntimeErrorPrefix(t *testing.T) {
	// 创建完整标准库 State，确保 load、string.dump 与 pcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行 stripped 错误探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function checkerr (pattern, f, ...)
  local ok, message = pcall(f, ...)
  assert(not ok, "call unexpectedly succeeded")
  message = tostring(message)
  assert(string.find(message, pattern), message)
end
local f = function (a) return a + 1 end
f = assert(load(string.dump(f, true)))
assert(f(3) == 4)
checkerr("^%?:%-1:", f, {})
f = function () local a; a = {}; return a + 2 end
f = assert(load(string.dump(f, true)))
checkerr("^%?:%-1:.*table value", f)
`
	if err := DoString(state, source); err != nil {
		// stripped chunk 的运行期错误必须带 ?:-1: 前缀。
		t.Fatalf("DoString stripped runtime error prefix failed: %v", err)
	}
}

// TestDoStringOfficialRKLimitDebugNames 验证超过 RK 常量上限后的错误名称推断。
//
// Lua 5.3 errors.lua 会先生成 1000 个不同字段名常量，再检查后续 global/field/method 错误
// 仍能显示源码名称；寄存器来源倒查必须在 LOADK 等覆盖指令处停止，不能越过最近写入者。
func TestDoStringOfficialRKLimitDebugNames(t *testing.T) {
	// 创建完整标准库 State，确保 load、table.concat、pcall 与 string.find 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行 RK limit 探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function doit (s)
  local f, msg = load(s)
  if f == nil then return msg end
  local ok, message = pcall(f)
  return (not ok) and tostring(message)
end
local function checkmessage (prog, want)
  local message = doit(prog)
  assert(string.find(message, want, 1, true), message)
end
local t = {}
for i = 1, 1000 do
  t[i] = "a = x" .. i
end
local s = table.concat(t, "; ")
checkmessage(s.."; a = bbb + 1", "global 'bbb'")
checkmessage("local _ENV=_ENV;"..s.."; a = bbb + 1", "global 'bbb'")
checkmessage(s.."; local t = {}; a = t.bbb + 1", "field 'bbb'")
checkmessage(s.."; local t = {}; t:bbb()", "method 'bbb'")
`
	if err := DoString(state, source); err != nil {
		// RK 常量上限后的错误名称必须继续对齐官方 errors.lua。
		t.Fatalf("DoString RK limit debug names failed: %v", err)
	}
}

// TestDoStringRKOverflowBinaryLeftLiteralSurvivesRightCall 验证溢出 RK 范围的左字面量生命周期。
//
// 当常量索引超过 RK 可编码范围时，codegen 会把字面量先装入临时寄存器。左字面量参与二元运算、
// 右侧又包含嵌套调用时，该临时寄存器必须在发射二元指令前保持有效，不能被右侧求值覆盖。
func TestDoStringRKOverflowBinaryLeftLiteralSurvivesRightCall(t *testing.T) {
	// 创建完整标准库 State，确保 setmetatable、assert 和 tostring 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行元方法与断言脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	var source strings.Builder
	source.WriteString("local sink = false\n")
	for constantIndex := 0; constantIndex < 270; constantIndex++ {
		// 生成不可达但会进入常量表的唯一字符串，把后续 `]` 推到 RK 常量范围之外。
		fmt.Fprintf(&source, "if sink then local _ = %q end\n", fmt.Sprintf("pad-%03d", constantIndex))
	}
	source.WriteString(`
local seen
local mt = {}
mt.__mul = function(a, b)
  if seen == nil then
    seen = a
  end
  return setmetatable({}, mt)
end
mt.__pow = function(a, b)
  return setmetatable({}, mt)
end
local m = {}
function m.P(x)
  return setmetatable({}, mt)
end
function m.C(x)
  return setmetatable({}, mt)
end
local _ = "]" * m.C(m.P("=") ^ 0)
assert(seen == "]", tostring(seen))
`)
	if err := DoString(state, source.String()); err != nil {
		// 首个 __mul 左操作数必须仍是溢出 RK 范围的 `]` 字面量。
		t.Fatalf("DoString RK overflow binary literal failed: %v", err)
	}
}

// TestDoStringOfficialShortCircuitIndexReceiverName 验证短路分支内索引接收者错误名称。
//
// 官方 errors.lua 会在 table constructor 内执行 `x and aaa[x or y]`；虽然 `aaa` 位于短路分支
// 跳转之后，但它仍是实际被索引的全局对象，错误文本必须包含 `global 'aaa'`。
func TestDoStringOfficialShortCircuitIndexReceiverName(t *testing.T) {
	// 创建完整标准库 State，确保 load、pcall 和 string.find 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行官方错误消息探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function checkmessage (prog, want)
  local f = assert(load(prog))
  local ok, message = pcall(f)
  assert(not ok, "chunk unexpectedly succeeded")
  message = tostring(message)
  assert(string.find(message, want, 1, true), message)
end
checkmessage([[aaa=9
repeat until 3==3
local x=math.sin(math.cos(3))
if math.sin(1) == x then return math.sin(1) end
local a,b = 1, {
  {x='a'..'b'..'c', y='b', z=x},
  {1,2,3,4,5} or 3+3<=3+3,
  3+1>3+1,
  {d = x and aaa[x or y]}}
]], "global 'aaa'")
local ok, message = pcall(function() aaa = {}; return (aaa or aaa) + (aaa and aaa) end)
assert(not ok, "short-circuit arithmetic unexpectedly succeeded")
message = tostring(message)
assert(not string.find(message, "'aaa'", 1, true), message)
`
	if err := DoString(state, source); err != nil {
		// 短路分支内的索引接收者必须保留名称，同时普通短路算术仍不能泄露名称。
		t.Fatalf("DoString short-circuit index receiver name failed: %v", err)
	}
}

// TestDoStringStandardLibraryBadArgumentUsesDottedName 验证标准库字段函数参数错误保留库名前缀。
//
// 官方 errors.lua 会通过 `(io.write or print){}`、`table.sort(..., table.sort)` 等表达式检查
// bad argument 文本；Go closure 调用边界需要把字段调用点补成 io.write/table.sort/string.gsub。
func TestDoStringStandardLibraryBadArgumentUsesDottedName(t *testing.T) {
	// 创建完整标准库 State，确保 io/table/string 函数都已注册。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行官方参数错误探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function check(prog, want)
  local fn = assert(load(prog))
  local ok, message = pcall(fn)
  assert(not ok, prog)
  message = tostring(message)
  assert(string.find(message, want, 1, true), message)
end
check("(io.write or print){}", "io.write")
check("(collectgarbage or print){}", "collectgarbage")
check("table.sort({1,2,3}, table.sort)", "table.sort")
check("string.gsub('s', 's', setmetatable)", "setmetatable")
`
	if err := DoString(state, source); err != nil {
		// 标准库参数错误必须包含官方期望的调用点名称。
		t.Fatalf("DoString stdlib bad argument name failed: %v", err)
	}
}

// TestDoStringTailCallBadArgumentKeepsShortFunctionName 验证 tail-call bad argument 保留短函数名。
//
// 官方 errors.lua 对 `return math.sin("a")` 只断言短函数名片段 `'sin'`；Go closure 参数错误在
// 补齐 `math.sin` 完整名后仍必须保留短名片段，避免 tail-call 错误文本兼容性回退。
func TestDoStringTailCallBadArgumentKeepsShortFunctionName(t *testing.T) {
	// 创建完整标准库 State，确保 math.sin、load 和 pcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行官方参数错误探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local f = assert(load([[return math.sin("a")]]))
local ok, message = pcall(f)
assert(not ok, "chunk unexpectedly succeeded")
message = tostring(message)
assert(string.find(message, "math.sin", 1, true), message)
assert(string.find(message, "'sin'", 1, true), message)
`
	if err := DoString(state, source); err != nil {
		// tail-call 标准库参数错误必须同时包含完整库名和短函数名。
		t.Fatalf("DoString tail-call bad argument name failed: %v", err)
	}
}

// TestDoStringFileGCMissingSelfReportsNoValue 验证 file 元表 __gc 的缺参错误文本。
//
// 官方 errors.lua 会直接调用 `getmetatable(io.stdin).__gc()`，此时 `getmetatable` 必须能看到
// file userdata 元表，且 `__gc` 缺少 self 时错误文本需要包含 `no value`。
func TestDoStringFileGCMissingSelfReportsNoValue(t *testing.T) {
	// 创建完整标准库 State，确保 io.stdin、getmetatable 和 pcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行官方参数错误探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local mt = getmetatable(io.stdin)
assert(type(mt) == "table", type(mt))
assert(type(mt.__gc) == "function", type(mt.__gc))
local ok, message = pcall(mt.__gc)
assert(not ok, "file __gc unexpectedly succeeded")
message = tostring(message)
assert(string.find(message, "no value", 1, true), message)
`
	if err := DoString(state, source); err != nil {
		// file __gc 直接调用必须对齐官方 errors.lua 的 no value 断言。
		t.Fatalf("DoString file __gc missing self failed: %v", err)
	}
}

// TestDoStringStringMethodBadArgumentNumbers 验证字符串方法调用的 self 与参数编号。
//
// 官方 errors.lua 同时检查 `a:sub()` 的 bad self、普通函数调用 `string.sub('a', {})` 的 #2，
// 以及冒号调用 `('a'):sub{}` 的 #1；Go closure 参数错误需要按 method 调用点修正。
func TestDoStringStringMethodBadArgumentNumbers(t *testing.T) {
	// 创建完整标准库 State，确保 string 方法表、setmetatable、load 和 pcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开失败时无法执行官方参数错误探测脚本。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function checkmessage (prog, want)
  local f = assert(load(prog))
  local ok, message = pcall(f)
  assert(not ok, "chunk unexpectedly succeeded")
  message = tostring(message)
  assert(string.find(message, want, 1, true), message)
end
a = {}; setmetatable(a, {__index = string})
checkmessage("a:sub()", "bad self")
checkmessage("string.sub('a', {})", "#2")
checkmessage("('a'):sub{}", "#1")
`
	if err := DoString(state, source); err != nil {
		// string method 的 self 与参数编号必须对齐官方 errors.lua。
		t.Fatalf("DoString string method bad arguments failed: %v", err)
	}
}

// TestDoStringDebugForIteratorName 验证泛型 for 迭代器调试名称。
//
// 官方 db.lua 要求 for 迭代器函数内部读取 debug.getinfo(1).name 时返回固定名称
// `for iterator`，该名称不是普通 local/global 调用点推断结果。
func TestDoStringDebugForIteratorName(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getinfo 和泛型 for 执行路径可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function f()
  assert(debug.getinfo(1).name == "for iterator")
end
for _ in f do end
`
	if err := DoString(state, source); err != nil {
		// 泛型 for 迭代器必须暴露官方固定调试名称。
		t.Fatalf("DoString for iterator name failed: %v", err)
	}
}

// TestDoStringDebugFinalizerMetamethodName 验证 table `__gc` finalizer 的调用方调试名称。
//
// 官方 db.lua 会在 finalizer 内读取 `debug.getinfo(2, "n")`，要求调用方 namewhat 为
// metamethod 且 name 为 `__gc`。该测试同时依赖自动 GC 在持续 table 分配压力下触发 finalizer。
func TestDoStringDebugFinalizerMetamethodName(t *testing.T) {
	// 创建完整标准库 State，确保 setmetatable、debug.getinfo 与自动 table finalizer 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local name = nil
setmetatable({}, {__gc = function()
  local info = debug.getinfo(2, "n")
  assert(info.namewhat == "metamethod")
  name = info.name
end})
for i = 1, 20 do
  local a = {}
end
assert(name == "__gc")
`
	if err := DoString(state, source); err != nil {
		// table finalizer 必须能在 debug 层暴露 __gc 元方法调用方。
		t.Fatalf("DoString debug finalizer name failed: %v", err)
	}
}

// TestDoStringGSubLuaClosureReplacement 验证 string.gsub 可调用 Lua closure 替换函数。
//
// Lua 5.3 官方 math.lua 使用该形态修改 maxinteger/mininteger 的十进制末位；替换函数必须能
// 通过标准库 State 调用 Lua closure，并把首个返回值作为替换文本。
func TestDoStringGSubLuaClosureReplacement(t *testing.T) {
	// 创建完整标准库 State，确保 string.gsub 由带 State runner 的入口注册。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local value, count = string.gsub("9223372036854775807", "%d$", function (d)
  assert(d == "7")
  return string.char(string.byte(d) + 1)
end)
assert(value == "9223372036854775808")
assert(count == 1)
`
	if err := DoString(state, source); err != nil {
		// Lua closure 替换函数必须能在 gsub 中被调用并返回替换文本。
		t.Fatalf("DoString gsub Lua closure replacement failed: %v", err)
	}
}

// TestDoStringDebugTailCallInfo 验证 debug.getinfo 暴露 tail call 帧。
//
// Lua 5.3 在被尾调用函数内会把当前帧和连续尾调用上层帧标记为 istailcall=true，并隐藏被替换
// 的中间 caller；官方 db.lua 用该行为检查尾调用调试栈。
func TestDoStringDebugTailCallInfo(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getinfo 可读取调用帧和 tail call 标记。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function f (x)
  if x then
    local i1 = debug.getinfo(1, "Stf")
    assert(i1.what == "Lua")
    assert(i1.istailcall == true)
    assert(i1.func == f)
    local i2 = debug.getinfo(2, "Stf")
    assert(i2.istailcall == true)
    assert(i2.func == g1)
    local i3 = debug.getinfo(3, "S")
    assert(i3.what == "main")
  end
end
function g(x) return f(x) end
function g1(x) g(x) end
local function h (x) local f=g1; return f(x) end
h(true)
`
	if err := DoString(state, source); err != nil {
		// tail call 调试栈必须对齐官方 db.lua 的连续尾调用断言。
		t.Fatalf("DoString debug tail call info failed: %v", err)
	}
}

// TestDoStringDebugTailCallHookEvents 验证 tail call hook 事件序列。
//
// Lua 5.3 的 hook mask `c` 同时覆盖 call 与 tail call；尾调用进入目标函数时事件名必须是
// `tail call`，且被替换 caller 不应额外产生普通 return 事件。
func TestDoStringDebugTailCallHookEvents(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook 和 table 操作均可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local events = {}
local function f (x) return x end
function g(x) return f(x) end
function g1(x) g(x) end
local function h (x) local f=g1; return f(x) end
debug.sethook(function (event)
  events[#events + 1] = event
end, "cr")
h(false)
debug.sethook()
local expected = {
  "return",
  "call",
  "tail call",
  "call",
  "tail call",
  "return",
  "return",
  "call",
}
assert(#events == #expected, #events)
for index = 1, #expected do
  assert(events[index] == expected[index], index .. ":" .. tostring(events[index]))
end
`
	if err := DoString(state, source); err != nil {
		// hook 事件序列必须与官方 db.lua 对 tail call 的期望一致。
		t.Fatalf("DoString debug tail call hook events failed: %v", err)
	}
}

// TestDoStringDebugDeepSelfTailCall 验证深度自尾调用不增长 Go 调用栈。
//
// 官方 db.lua 会执行 30000 层自尾递归并在最深处检查 debug.getinfo；该路径必须复用当前 Lua
// 调用帧，而不是递归进入 Go 执行器。
func TestDoStringDebugDeepSelfTailCall(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getinfo 可在最深层读取尾调用标记。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local limit = 30000
local function foo (x)
  if x == 0 then
    assert(debug.getinfo(2).what == "main")
    local info = debug.getinfo(1)
    assert(info.istailcall == true and info.func == foo)
  else
    return foo(x - 1)
  end
end
foo(limit)
`
	if err := DoString(state, source); err != nil {
		// 深度自尾调用必须不触发 Go/C 栈溢出。
		t.Fatalf("DoString deep self tail call failed: %v", err)
	}
}

// TestDoStringCallsLuaDeepRecursionBudget 验证官方 calls.lua 的深递归预算和 method 尾调用。
//
// `deep(200)` 是普通递归，默认 MaxCallDepth 必须给主 chunk 等外层帧留余量；`a:deep(30000)`
// 依赖 method call 在 return 位置生成 TAILCALL，不能增长调用深度。
func TestDoStringCallsLuaDeepRecursionBudget(t *testing.T) {
	// 创建完整标准库 State，执行 calls.lua 同形态的递归片段。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
function deep (n)
  if n > 0 then deep(n - 1) end
end
deep(200)
a = {}
function a:deep (n)
  if n > 0 then return self:deep(n - 1) else return 101 end
end
assert(a:deep(30000) == 101)
`
	if err := DoString(state, source); err != nil {
		// 普通深递归和 method 尾递归都必须通过。
		t.Fatalf("DoString calls.lua deep recursion failed: %v", err)
	}
}

// TestDoStringDebugLocalFunctionDefinitionLines 验证局部函数表达式定义期 line hook。
//
// 官方 db.lua 要求 `local A = function () ... end` 在 closure 创建期间先以 `(*temporary)` 暴露
// 初始化寄存器，随后 local 名称 `A` 才进入作用域。
func TestDoStringDebugLocalFunctionDefinitionLines(t *testing.T) {
	// 创建完整标准库 State，确保 load、debug.sethook 和 debug.getlocal 都可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local chunk = load[[
  local A = function ()
    return x
  end
  return
]]
local count = 0
debug.sethook(function (event, line)
  if line == 3 then
    count = count + 1
    assert(debug.getlocal(2, 1) == "(*temporary)")
  elseif line == 4 then
    count = count + 1
    assert(debug.getlocal(2, 1) == "A")
  end
end, "l")
chunk()
debug.sethook()
assert(count == 2, count)
`
	if err := DoString(state, source); err != nil {
		// local 函数表达式定义期的 line hook 必须对齐官方 db.lua。
		t.Fatalf("DoString local function definition line hook failed: %v", err)
	}
}

// TestDoStringCallTemporaryCleanupGuardSemantics 验证 CALL 临时区清理不得破坏可见语义。
//
// 固定返回 CALL 会把函数值、self 与实参放在调用临时寄存器中；未来若清理非结果临时槽，必须保留
// 活动 local、debug.getlocal、开放返回、TFORCALL 和 __call 元方法的 Lua 5.3 可见行为。
func TestDoStringCallTemporaryCleanupGuardSemantics(t *testing.T) {
	// 创建完整标准库 State，确保 debug hook、table、ipairs、setmetatable 和 select 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败，否则无法覆盖 debug 与 base 调用语义。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local phase = ""
local checks = {}

debug.sethook(function()
  if phase == "local_go" then
    local values = {}
    for index = 1, 8 do
      local name, value = debug.getlocal(2, index)
      if not name then break end
      values[name] = value
    end
    checks.local_go = values.f == select and values.count == 1
    phase = ""
  elseif phase == "method" then
    local values = {}
    for index = 1, 8 do
      local name, value = debug.getlocal(2, index)
      if not name then break end
      values[name] = value
    end
    checks.method = values.t and values.t.value == 41 and values.method_result == 42
    phase = ""
  elseif phase == "call_metamethod" then
    local values = {}
    for index = 1, 8 do
      local name, value = debug.getlocal(2, index)
      if not name then break end
      values[name] = value
    end
    checks.call_metamethod = values.callable and values.callable.base == 40 and values.meta_result == 42
    phase = ""
  end
end, "l")

local function many()
  return "x", "y", "z"
end

local function run()
  local f = select
  local count = f("#", "alpha")
  phase = "local_go"
  local preserved = f
  assert(preserved == select and count == 1)

  local t = { value = 41, f = function(self, add) return self.value + add end }
  local method_result = t:f(1)
  phase = "method"
  local kept_method_receiver = t
  assert(kept_method_receiver.value == 41 and method_result == 42)

  local callable = setmetatable({ base = 40 }, { __call = function(self, add) return self.base + add end })
  local meta_result = callable(2)
  phase = "call_metamethod"
  local kept_callable = callable
  assert(kept_callable.base == 40 and meta_result == 42)

  local packed = { many() }
  local second = packed[2]
  assert(#packed == 3 and second == "y")
  checks.open_return = packed[1] == "x" and packed[2] == "y" and packed[3] == "z"

  local total = 0
  for _, value in ipairs({ 1, 2, 3 }) do
    total = total + value
  end
  local final_total = total
  assert(final_total == 6)
  checks.generic_for = total == 6
end

run()
debug.sethook()
__call_temp_checks = checks
`
	if err := DoString(state, source); err != nil {
		// 任一断言失败都说明 CALL 临时区清理门禁的可见语义不成立。
		t.Fatalf("DoString call temporary cleanup guard failed: %v", err)
	}
	checksValue, err := GetGlobal(state, "__call_temp_checks")
	if err != nil {
		// 全局读取失败说明脚本未能暴露检查结果。
		t.Fatalf("read call temporary checks failed: %v", err)
	}
	checksTable, ok := checksValue.Ref.(*runtime.Table)
	if checksValue.Kind != runtime.KindTable || !ok || checksTable == nil {
		// 检查结果必须是 Lua table，便于逐项报告失败门禁。
		t.Fatalf("call temporary checks kind = %v, want table", checksValue.Kind)
	}
	for _, checkName := range []string{"local_go", "method", "call_metamethod", "open_return", "generic_for"} {
		// 每个门禁项必须由 hook 或后续语句实际确认。
		checkValue := checksTable.RawGetString(checkName)
		if checkValue.Kind != runtime.KindBoolean || !checkValue.Bool {
			// 逐项报告失败名称，避免 Lua error 抹掉具体门禁。
			t.Fatalf("call temporary cleanup guard %s = %#v, want true", checkName, checkValue)
		}
	}
}

// TestDoStringCallTemporaryCleanupTraceHookGuards 验证 CALL 临时区清理不得破坏 hook 与 traceback。
//
// 固定返回 CALL 后的非结果临时槽若被未来生产修复清理，仍必须保留 count hook、return hook 与
// traceback 在当前 Lua 帧中观察活动 local 和错误现场的 Lua 5.3 语义。
func TestDoStringCallTemporaryCleanupTraceHookGuards(t *testing.T) {
	// 创建完整标准库 State，确保 debug hook、traceback、select 和 xpcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败，否则无法覆盖 debug hook 与 traceback 语义。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local phase = ""
local checks = {}

debug.sethook(function(event)
  if event == "count" and phase == "count_guard" and not checks.count_hook then
    local values = {}
    for index = 1, 8 do
      local name, value = debug.getlocal(2, index)
      if not name then break end
      values[name] = value
    end
    checks.count_hook = values.f == select and values.count == 1
  elseif event == "return" and phase == "return_guard" and not checks.return_hook then
    local values = {}
    for index = 1, 8 do
      local name, value = debug.getlocal(2, index)
      if not name then break end
      values[name] = value
    end
    checks.return_hook = values.f == select and values.count == 1
    phase = ""
  end
end, "cr", 1)

local function count_guard()
  local f = select
  local count = f("#", "alpha")
  phase = "count_guard"
  local marker = count
  phase = ""
  return marker
end
assert(count_guard() == 1)

local function return_guard()
  local f = select
  local count = f("#", "alpha")
  phase = "return_guard"
  return count
end
assert(return_guard() == 1)

debug.sethook()

local function trace_guard()
  local f = select
  local count = f("#", "alpha")
  local preserved = f
  assert(preserved == select and count == 1)
  error("call-temp-trace-guard")
end

local ok, trace = xpcall(trace_guard, debug.traceback)
checks.traceback = (not ok) and
  string.find(trace, "call-temp-trace-guard", 1, true) ~= nil and
  string.find(trace, "stack traceback:", 1, true) ~= nil

__call_temp_trace_hook_checks = checks
`
	if err := DoString(state, source); err != nil {
		// 任一断言或 hook 运行失败都说明 CALL 临时区 hook/traceback 门禁不成立。
		t.Fatalf("DoString call temporary trace hook guard failed: %v", err)
	}
	checksValue, err := GetGlobal(state, "__call_temp_trace_hook_checks")
	if err != nil {
		// 全局读取失败说明脚本未能暴露 hook/traceback 检查结果。
		t.Fatalf("read call temporary trace hook checks failed: %v", err)
	}
	checksTable, ok := checksValue.Ref.(*runtime.Table)
	if checksValue.Kind != runtime.KindTable || !ok || checksTable == nil {
		// 检查结果必须是 Lua table，便于逐项报告失败门禁。
		t.Fatalf("call temporary trace hook checks kind = %v, want table", checksValue.Kind)
	}
	for _, checkName := range []string{"count_hook", "return_hook", "traceback"} {
		// 每个门禁项必须由 hook 或 traceback 实际确认。
		checkValue := checksTable.RawGetString(checkName)
		if checkValue.Kind != runtime.KindBoolean || !checkValue.Bool {
			// 逐项报告失败名称，避免 Lua error 抹掉具体门禁。
			t.Fatalf("call temporary trace hook guard %s = %#v, want true", checkName, checkValue)
		}
	}
}

// TestDoStringCallTemporaryCleanupCallMetamethodArgumentGuards 验证 __call 改写参数区间。
//
// 固定返回 CALL 调用带 __call 元方法的值时，VM 会把原始被调用值改写为元方法的 self 参数；未来
// 若清理 CALL 临时槽，不能提前清掉元方法帧仍需要读取的 self、命名参数和 vararg。
func TestDoStringCallTemporaryCleanupCallMetamethodArgumentGuards(t *testing.T) {
	// 创建完整标准库 State，确保 setmetatable、debug.getlocal、select 和 tostring 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败，否则无法覆盖 __call 元方法参数语义。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local checks = {}

local function metamethod(self, first, second, ...)
  local values = {}
  for index = 1, 12 do
    local name, value = debug.getlocal(1, index)
    if not name then break end
    values[name] = value
  end
  local vararg_name_first, vararg_value_first = debug.getlocal(1, -1)
  local vararg_name_second, vararg_value_second = debug.getlocal(1, -2)
  local vararg_name_third, vararg_value_third = debug.getlocal(1, -3)

  local tail_count = select("#", ...)
  local tail_first, tail_second, tail_third = ...
  checks.arguments =
    self.marker == "callable" and
    first == "alpha" and
    second == 17 and
    tail_count == 3 and
    tail_first == "x" and
    tail_second == false and
    tail_third == "z"
  checks.debug_locals =
    values.self == self and
    values.first == first and
    values.second == second and
    vararg_name_first == "(*vararg)" and
    vararg_value_first == tail_first and
    vararg_name_second == "(*vararg)" and
    vararg_value_second == tail_second and
    vararg_name_third == "(*vararg)" and
    vararg_value_third == tail_third
  return first .. ":" .. tostring(second) .. ":" .. tostring(tail_count)
end

local callable = setmetatable({ marker = "callable" }, { __call = metamethod })
local fixed_result = callable("alpha", 17, "x", false, "z")
local kept_callable = callable
checks.fixed_result = fixed_result == "alpha:17:3" and kept_callable.marker == "callable"

__call_temp_metamethod_argument_checks = checks
`
	if err := DoString(state, source); err != nil {
		// 任一断言或元方法执行失败都说明 __call 参数区间门禁不成立。
		t.Fatalf("DoString call metamethod argument guard failed: %v", err)
	}
	checksValue, err := GetGlobal(state, "__call_temp_metamethod_argument_checks")
	if err != nil {
		// 全局读取失败说明脚本未能暴露 __call 参数检查结果。
		t.Fatalf("read call metamethod argument checks failed: %v", err)
	}
	checksTable, ok := checksValue.Ref.(*runtime.Table)
	if checksValue.Kind != runtime.KindTable || !ok || checksTable == nil {
		// 检查结果必须是 Lua table，便于逐项报告失败门禁。
		t.Fatalf("call metamethod argument checks kind = %v, want table", checksValue.Kind)
	}
	for _, checkName := range []string{"arguments", "debug_locals", "fixed_result"} {
		// 每个门禁项必须由元方法参数读取、debug local 或固定返回写回实际确认。
		checkValue := checksTable.RawGetString(checkName)
		if checkValue.Kind != runtime.KindBoolean || !checkValue.Bool {
			// 逐项报告失败名称，避免 Lua error 抹掉具体门禁。
			t.Fatalf("call metamethod argument guard %s = %#v, want true", checkName, checkValue)
		}
	}
}

// TestDoStringCallTemporaryCleanupTForCallResultGuards 验证 TFORCALL 结果区不会被普通 CALL 清理误伤。
//
// 泛型 for 的 TFORCALL 会把迭代结果写入循环变量区，普通 fixed-result CALL 后续清理临时槽时，
// 不能清掉这些仍活跃的循环变量，也不能破坏 TFORLOOP 下一轮使用的控制变量。
func TestDoStringCallTemporaryCleanupTForCallResultGuards(t *testing.T) {
	// 创建完整标准库 State，确保泛型 for、debug.getlocal、table length 和字符串拼接可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败，否则无法覆盖泛型 for 与 debug local 语义。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local checks = {}

local function ordinary(left, right)
  return left .. right
end

local function iter(state, control)
  local next_index = control + 1
  if next_index > #state then
    return nil
  end
  return next_index, state[next_index], "extra-" .. next_index
end

local function run()
  local rows = { "a", "b" }
  local seen = {}
  local debug_ok = true

  for key, value, extra in iter, rows, 0 do
    local fixed_result = ordinary("x", "y")
    local values = {}
    for index = 1, 20 do
      local name, local_value = debug.getlocal(1, index)
      if not name then break end
      values[name] = local_value
    end
    debug_ok =
      debug_ok and
      values.key == key and
      values.value == value and
      values.extra == extra and
      values.fixed_result == fixed_result
    seen[#seen + 1] = key .. ":" .. value .. ":" .. extra .. ":" .. fixed_result
  end

  checks.iteration =
    seen[1] == "1:a:extra-1:xy" and
    seen[2] == "2:b:extra-2:xy" and
    seen[3] == nil
  checks.debug_locals = debug_ok
  checks.control_progress = #seen == 2
end

run()
__call_temp_tforcall_result_checks = checks
`
	if err := DoString(state, source); err != nil {
		// 任一断言或泛型 for 执行失败都说明 TFORCALL 结果区门禁不成立。
		t.Fatalf("DoString TFORCALL result guard failed: %v", err)
	}
	checksValue, err := GetGlobal(state, "__call_temp_tforcall_result_checks")
	if err != nil {
		// 全局读取失败说明脚本未能暴露 TFORCALL 结果检查。
		t.Fatalf("read TFORCALL result checks failed: %v", err)
	}
	checksTable, ok := checksValue.Ref.(*runtime.Table)
	if checksValue.Kind != runtime.KindTable || !ok || checksTable == nil {
		// 检查结果必须是 Lua table，便于逐项报告失败门禁。
		t.Fatalf("TFORCALL result checks kind = %v, want table", checksValue.Kind)
	}
	for _, checkName := range []string{"iteration", "debug_locals", "control_progress"} {
		// 每个门禁项必须由迭代结果、debug local 或控制变量推进实际确认。
		checkValue := checksTable.RawGetString(checkName)
		if checkValue.Kind != runtime.KindBoolean || !checkValue.Bool {
			// 逐项报告失败名称，避免 Lua error 抹掉具体门禁。
			t.Fatalf("TFORCALL result guard %s = %#v, want true", checkName, checkValue)
		}
	}
}

// TestDoStringCallTemporaryCleanupClosureRootShapeGuards 验证固定返回 CALL 后的闭包根形态。
//
// native LPeg 诊断已把差异收敛到 selected tail 是否复用前置 local root；该测试不依赖 LPeg
// 或 native_modules，用纯 Lua closure 覆盖 long-lived local、_ENV/global 和 shadow local 三种形态。
func TestDoStringCallTemporaryCleanupClosureRootShapeGuards(t *testing.T) {
	// 创建完整标准库 State，确保 select、collectgarbage、tostring 和 table 操作可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败，否则无法覆盖 GC 与 closure upvalue 语义。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local checks = {}

local function allocate_pressure()
  for index = 1, 200 do
    local holder = { index, tostring(index), { nested = index } }
    assert(holder[1] == index)
  end
  collectgarbage()
end

local function make_local_root_probe()
  local attempts = {}
  local probe_open
  local probe_close
  local probe_any
  local probe_close_head
  local probe_close_back
  local probe_close_func
  local dummy_func
  local dummy_capture
  local dummy_back
  local dummy_value

  local count = select("#", "alpha", "beta")
  if count ~= 2 then
    error("unexpected select count")
  end
  local skipped = error
  local payload = 17

  attempts = {}
  if probe_open == nil then
    probe_open = function()
      return "open", count
    end
  end
  if probe_close_func == nil then
    probe_close_func = function(_, pos, s1, s2)
      attempts[#attempts + 1] = pos .. ":" .. s1 .. ":" .. s2
      return s1 == s2
    end
  end
  if probe_close == nil then
    probe_close = function(_, pos, s1, s2)
      return probe_close_func(_, pos, s1, s2)
    end
  end
  if probe_any == nil then
    probe_any = "any"
  end

  return function()
    local ok = probe_close(nil, 18, "==", "==")
    local open_marker, local_count = probe_open()
    return ok, attempts[1], open_marker, local_count, payload, skipped == error, probe_any
  end
end

local function make_global_root_probe()
  __call_temp_global_attempts = {}
  __call_temp_global_probe_open = nil
  __call_temp_global_probe_close_func = nil

  local count = select("#", "alpha", "beta")
  if count ~= 2 then
    error("unexpected global select count")
  end
  local skipped = error
  local payload = 17

  __call_temp_global_attempts = {}
  if __call_temp_global_probe_open == nil then
    __call_temp_global_probe_open = function()
      return "global-open", count
    end
  end
  if __call_temp_global_probe_close_func == nil then
    __call_temp_global_probe_close_func = function(_, pos, s1, s2)
      __call_temp_global_attempts[#__call_temp_global_attempts + 1] = pos .. ":" .. s1 .. ":" .. s2
      return s1 == s2
    end
  end

  return function()
    local ok = __call_temp_global_probe_close_func(nil, 18, "==", "==")
    local open_marker, local_count = __call_temp_global_probe_open()
    return ok, __call_temp_global_attempts[1], open_marker, local_count, payload, skipped == error
  end
end

local function make_shadow_root_probe()
  local attempts = {}
  local probe_marker = attempts

  local count = select("#", "alpha", "beta")
  if count ~= 2 then
    error("unexpected shadow select count")
  end
  local skipped = error
  local payload = 17

  local attempts = {}
  local callback = function(_, pos, s1, s2)
    attempts[#attempts + 1] = pos .. ":" .. s1 .. ":" .. s2
    return s1 == s2
  end

  return function()
    local ok = callback(nil, 18, "==", "==")
    return ok, attempts[1], probe_marker ~= attempts, payload, skipped == error
  end
end

local local_probe = make_local_root_probe()
allocate_pressure()
local ok, attempt, open_marker, local_count, payload, skipped_ok, any_marker = local_probe()
checks.local_upvalue =
  ok and
  attempt == "18:==:==" and
  open_marker == "open" and
  local_count == 2 and
  payload == 17 and
  skipped_ok and
  any_marker == "any"

local global_probe = make_global_root_probe()
allocate_pressure()
ok, attempt, open_marker, local_count, payload, skipped_ok = global_probe()
checks.global_upvalue =
  ok and
  attempt == "18:==:==" and
  open_marker == "global-open" and
  local_count == 2 and
  payload == 17 and
  skipped_ok

local shadow_probe = make_shadow_root_probe()
allocate_pressure()
local shadowed
ok, attempt, shadowed, payload, skipped_ok = shadow_probe()
checks.shadow_upvalue =
  ok and
  attempt == "18:==:==" and
  shadowed and
  payload == 17 and
  skipped_ok

__call_temp_closure_root_shape_checks = checks
__call_temp_global_attempts = nil
__call_temp_global_probe_open = nil
__call_temp_global_probe_close_func = nil
`
	if err := DoString(state, source); err != nil {
		// 任一断言或闭包调用失败都说明模块无关 closure root 形态门禁不成立。
		t.Fatalf("DoString closure root shape guard failed: %v", err)
	}
	checksValue, err := GetGlobal(state, "__call_temp_closure_root_shape_checks")
	if err != nil {
		// 全局读取失败说明脚本未能暴露 closure root 检查结果。
		t.Fatalf("read closure root shape checks failed: %v", err)
	}
	checksTable, ok := checksValue.Ref.(*runtime.Table)
	if checksValue.Kind != runtime.KindTable || !ok || checksTable == nil {
		// 检查结果必须是 Lua table，便于逐项报告失败门禁。
		t.Fatalf("closure root shape checks kind = %v, want table", checksValue.Kind)
	}
	for _, checkName := range []string{"local_upvalue", "global_upvalue", "shadow_upvalue"} {
		// 每个门禁项必须由对应 closure/root 形态实际确认。
		checkValue := checksTable.RawGetString(checkName)
		if checkValue.Kind != runtime.KindBoolean || !checkValue.Bool {
			// 逐项报告失败名称，避免 Lua error 抹掉具体门禁。
			t.Fatalf("closure root shape guard %s = %#v, want true", checkName, checkValue)
		}
	}
}

// TestDoStringDebugDumpedLocalFunctionCallName 验证 dumped chunk 仍能反查局部函数调用名。
//
// 官方 all.lua 会将 db.lua 通过 string.dump/load 后执行；binary chunk 不保存 locvar 寄存器号，
// debug.getinfo(2) 仍必须在 table field 调用中看到 caller 的局部函数名 `f`。
func TestDoStringDebugDumpedLocalFunctionCallName(t *testing.T) {
	// 创建完整标准库 State，确保 load、string.dump 和 debug.getinfo 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local source = [[
local debug = require'debug'
local g = {x = function ()
  local info = debug.getinfo(2)
  assert(info.name == 'f' and info.namewhat == 'local')
  return 'xixi'
end}
local f = function () return 1+1 and (not 1 or g.x()) end
assert(f() == 'xixi')
]]
local f = assert(load(string.dump(assert(load(source, '@dumped-local.lua')))))
f()
`
	if err := DoString(state, source); err != nil {
		// dump/load 后的 local 调用名必须对齐官方 db.lua。
		t.Fatalf("DoString dumped local function call name failed: %v", err)
	}
}

// TestDoStringDebugSetupValueUpdatesSharedUpvalue 验证 debug.setupvalue 写回共享 upvalue。
//
// 官方 db.lua 要求两个闭包捕获同一外层 local 时，setupvalue 修改一个闭包的 upvalue 后，
// getupvalue 读取另一个闭包的同一 cell 必须立即看到新值。
func TestDoStringDebugSetupValueUpdatesSharedUpvalue(t *testing.T) {
	// 创建完整标准库 State，确保 debug.getupvalue/setupvalue 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local a,b,c = 1,2,3
local function foo1 (a) b = a; return c end
local function foo2 (x) a = x; return c+b end
assert(debug.setupvalue(foo1, 1, "xuxu") == "b")
local name, value = debug.getupvalue(foo2, 3)
assert(name == "b" and value == "xuxu")
`
	if err := DoString(state, source); err != nil {
		// debug.setupvalue 必须写入共享 upvalue cell，而不是只更新闭包创建时快照。
		t.Fatalf("DoString debug shared upvalue setup failed: %v", err)
	}
}

// TestDoStringDumpLoadUpvaluesRemainMutable 验证 dump/load 后顶层 upvalue 可被持久改写。
//
// 官方 calls.lua 要求二进制加载出来的函数在 debug.setupvalue 和函数内部 SETUPVAL 后，
// 后续调用仍能看到同一个 upvalue cell 的最新值。
func TestDoStringDumpLoadUpvaluesRemainMutable(t *testing.T) {
	// 创建完整标准库 State，确保 load、string.dump 和 debug.setupvalue 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local a, b = 20, 30
local x = load(string.dump(function (x)
  if x == "set" then a = 10 + b; b = b + 1 else
    return a
  end
end), "", "b", nil)
assert(x() == nil)
assert(debug.setupvalue(x, 1, "hi") == "a")
assert(x() == "hi")
assert(debug.setupvalue(x, 2, 13) == "b")
assert(not debug.setupvalue(x, 3, 10))
x("set")
assert(x() == 23)
x("set")
assert(x() == 24)
`
	if err := DoString(state, source); err != nil {
		// dump/load 后 upvalue 顺序与可变 cell 必须同时满足官方 calls.lua 断言。
		t.Fatalf("DoString dump/load upvalue mutability failed: %v", err)
	}
}

// TestDoStringDebugGetUpvalueReadsGMatchIterator 验证 Go iterator closure 的匿名 upvalue。
//
// 官方 db.lua 要求 `string.gmatch` 返回的 C closure 至少暴露一个名称为空字符串的 upvalue。
func TestDoStringDebugGetUpvalueReadsGMatchIterator(t *testing.T) {
	// 创建完整标准库 State，确保 string.gmatch 和 debug.getupvalue 都可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `assert(debug.getupvalue(string.gmatch("x", "x"), 1) == "")`
	if err := DoString(state, source); err != nil {
		// gmatch iterator 的 Go closure 必须暴露匿名 upvalue 名称。
		t.Fatalf("DoString debug gmatch upvalue failed: %v", err)
	}
}

// TestDoStringDebugCountHookNumericFor 验证 count hook 在 numeric for 空循环中的计数范围。
//
// 官方 db.lua 要求 `for i=1,1000 do end` 在 count=1 时触发次数落在 1000 到 1012 之间；
// count hook 需要在指令执行后触发，避免断言表达式读取 `a` 前被额外推进。
func TestDoStringDebugCountHookNumericFor(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local a=0
debug.sethook(function (e) a=a+1 end, "", 1)
a=0; for i=1,1000 do end; assert(1000 < a and a < 1012, a)
debug.sethook(function (e) a=a+1 end, "", 4)
a=0; for i=1,1000 do end; assert(250 < a and a < 255, a)
debug.sethook(function (e) a=a+1 end, "", 4000)
a=0; for i=1,1000 do end; assert(a == 0, a)
debug.sethook()
`
	if err := DoString(state, source); err != nil {
		// numeric for 空循环的 count hook 次数必须落在官方 db.lua 允许范围内。
		t.Fatalf("DoString debug count hook numeric for failed: %v", err)
	}
}

// TestDoStringDebugTracebackLevel 验证 debug.traceback 的 level 参数与当前 debug 帧展示。
//
// 官方 db.lua 要求 level=0 时 traceback 文本包含 `debug.traceback` 自身，无参调用从
// `stack traceback:\n` 开始。
func TestDoStringDebugTracebackLevel(t *testing.T) {
	// 创建完整标准库 State，确保 debug.traceback 和 string.find 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
assert(string.find(debug.traceback("hi", 0), "'debug.traceback'"))
assert(string.find(debug.traceback(), "^stack traceback:\n"))
`
	if err := DoString(state, source); err != nil {
		// traceback level=0 与空消息格式必须对齐官方 db.lua。
		t.Fatalf("DoString debug traceback level failed: %v", err)
	}
}

// TestDoStringDebugTracebackSizes 验证深栈 traceback 的 Lua 5.3 折叠规则。
//
// 官方 db.lua 在协程中递归构造深栈，并要求 traceback 按 level 裁剪后最多保留前 10 行与
// 后 11 行，中间用 `...` 折叠。
func TestDoStringDebugTracebackSizes(t *testing.T) {
	// 创建完整标准库 State，确保 debug.traceback、string.gsub 与 coroutine.wrap 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function countlines (s)
  return select(2, string.gsub(s, "\n", ""))
end
local function deep (lvl, n)
  if lvl == 0 then
    return (debug.traceback("message", n))
  else
    return (deep(lvl-1, n))
  end
end
local function checkdeep (total, start)
  local s = deep(total, start)
  local rest = string.match(s, "^message\nstack traceback:\n(.*)$")
  local cl = countlines(rest)
  assert(cl <= 10 + 11 + 1)
  local brk = string.find(rest, "%.%.%.")
  if brk then
    local rest1 = string.sub(rest, 1, brk)
    local rest2 = string.sub(rest, brk, #rest)
    assert(countlines(rest1) == 10 and countlines(rest2) == 11)
  else
    assert(cl == total - start + 2)
  end
end
for d = 1, 51, 10 do
  for l = 1, d do
    coroutine.wrap(checkdeep)(d, l)
  end
end
`
	if err := DoString(state, source); err != nil {
		// traceback 深栈折叠和协程边界裁剪必须对齐官方 db.lua。
		t.Fatalf("DoString debug traceback sizes failed: %v", err)
	}
}

// TestDoStringDebugStrippedChunkInfo 验证 stripped binary chunk 的调试信息剥离。
//
// 官方 db.lua 会通过 `string.dump(load(prog), true)` 构造无调试信息 chunk，并要求 local/upvalue
// 名称、source、行号和 activelines 按 Lua 5.3 stripped 语义展示。
func TestDoStringDebugStrippedChunkInfo(t *testing.T) {
	// 创建完整标准库 State，确保 load、string.dump 和 debug API 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
prog = [[-- program to be loaded without debug information
local debug = require'debug'
local a = 12
local n, v = debug.getlocal(1, 1)
assert(n == "(*temporary)" and v == debug)
n, v = debug.getlocal(1, 2)
assert(n == "(*temporary)" and v == 12)
local f = function () local x; return a end
n, v = debug.getupvalue(f, 1)
assert(n == "(*no name)" and v == 12)
assert(debug.setupvalue(f, 1, 13) == "(*no name)")
assert(a == 13)
local t = debug.getinfo(f)
assert(t.name == nil and t.linedefined > 0 and
       t.lastlinedefined == t.linedefined and
       t.short_src == "?")
assert(debug.getinfo(1).currentline == -1)
t = debug.getinfo(f, "L").activelines
assert(next(t) == nil)
f = load(string.dump(f))
t = debug.getinfo(f)
assert(t.name == nil and t.linedefined > 0 and
       t.lastlinedefined == t.linedefined and
       t.short_src == "?")
assert(debug.getinfo(1).currentline == -1)
return a
]]
local f = assert(load(string.dump(load(prog), true)))
assert(f() == 13)
`
	if err := DoString(state, source); err != nil {
		// stripped chunk 的 debug 展示必须对齐官方 db.lua。
		t.Fatalf("DoString debug stripped chunk info failed: %v", err)
	}
}

// TestDoStringReturnClosurePreservesCapturedParameter 验证 return closure 不覆盖被捕获形参。
//
// 官方 db.lua 会把返回闭包继续 `string.dump/load` 后执行；生成 RETURN 时不能把返回值搬回 R0
// 覆盖仍被内层闭包捕获的参数，否则 upvalue 会从数字错变为 closure 本身。
func TestDoStringReturnClosurePreservesCapturedParameter(t *testing.T) {
	// 创建完整标准库 State，确保 load、string.dump、debug.getupvalue 与 pcall 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local prog = [[
  return function (x)
    return function (y)
      return x + y
    end
  end
]]
for _, strip in ipairs{false, true} do
  local f = assert(load(string.dump(assert(load(prog, "src")), strip)))
  local h = f()(30)
  local name, value = debug.getupvalue(h, 1)
  if strip then
    assert(name == "(*no name)", name)
  else
    assert(name == "x", name)
  end
  assert(value == 30, value)
  assert(h(50) == 80)
end
`
	if err := DoString(state, source); err != nil {
		// 返回 closure 捕获的参数必须在源码执行和 dump/load 后保持一致。
		t.Fatalf("DoString return closure captured parameter failed: %v", err)
	}
}

// TestDoStringDebugTracebackPCallName 验证动态调用 pcall 时 traceback 保留全局 C 函数名。
//
// 官方 db.lua 通过 `(function () return pcall end)()(debug.traceback)` 检查 traceback 文本包含
// `pcall`；即使调用点来自动态返回值，Go closure 也需要回退到全局表推断名称。
func TestDoStringDebugTracebackPCallName(t *testing.T) {
	// 创建完整标准库 State，确保 pcall、debug.traceback 和 string.find 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local st, msg = (function () return pcall end)()(debug.traceback)
assert(st == true and string.find(msg, "pcall"), msg)
`
	if err := DoString(state, source); err != nil {
		// 动态 pcall 的 traceback 必须包含 pcall 名称。
		t.Fatalf("DoString debug traceback pcall name failed: %v", err)
	}
}

// TestDoStringDebugTracebackSuspendedCoroutine 验证 debug.traceback(thread) 读取挂起协程栈。
//
// 官方 db.lua 在 coroutine.yield 后要求 traceback(co) 从 yield 帧开始，并继续包含递归 Lua 函数帧；
// 这依赖 yield 时保存协程调用帧快照，而不是读取主线程当前调用栈。
func TestDoStringDebugTracebackSuspendedCoroutine(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine、debug 和 string 库可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function f(n)
  if n > 0 then f(n - 1) else coroutine.yield() end
end
local co = coroutine.create(f)
assert(coroutine.resume(co, 2))
local tb = debug.traceback(co)
assert(string.find(tb, "yield"), tb)
local tb1 = debug.traceback(co, nil, 1)
assert(not string.find(tb1, "yield"), tb1)
assert(string.find(tb1, "%[lua%]"), tb1)
local seen = 0
for line in string.gmatch(tb, "[^\n]+") do
  if string.find(line, "%[lua%]") then seen = seen + 1 end
end
assert(seen >= 3, tb)
`
	if err := DoString(state, source); err != nil {
		// 挂起协程 traceback 必须包含 yield 和递归 Lua 帧。
		t.Fatalf("DoString debug traceback suspended coroutine failed: %v", err)
	}
}

// TestDoStringLuaNewIndexMetamethodCanYield 验证 Lua closure `__newindex` 元方法可在协程中 yield。
//
// 官方 big.lua 会通过带 `__newindex` 的环境表运行大 chunk，并要求 SETTABUP 触发 Lua closure
// 元方法后能挂起和恢复；写入路径必须使用 VM 绑定的 Lua metamethod runner。
func TestDoStringLuaNewIndexMetamethodCanYield(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine、setmetatable 和 load 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local env = {}
_G.X = nil
local f = assert(load("X = 12; return X", nil, nil, env))
setmetatable(env, {
  __index = function (t, n)
    coroutine.yield("get", n)
    return _G[n]
  end,
  __newindex = function (t, n, v)
    coroutine.yield("set", n, v)
    _G[n] = v
  end,
})
local co = coroutine.wrap(f)
assert(co() == "set")
assert(rawget(env, "X") == nil)
assert(co() == "get")
assert(co() == 12)
assert(_G.X == 12)
_G.X = nil
`
	if err := DoString(state, source); err != nil {
		// Lua closure __newindex 必须能通过协程 yield 并恢复写入。
		t.Fatalf("DoString Lua __newindex yield failed: %v", err)
	}
}

// TestDoStringFunctionCallBatchMutableCalleeAndEnvGuards 验证函数调用 batch 的动态函数与环境边界。
//
// 未来若把 `sum = add(sum, i)` 压成整段 function_call batch，仍必须在函数值或闭包环境可能变化时
// 回退普通 VM；否则会跳过函数体内的 upvalue 写入、全局环境写入或 callee 替换副作用。
func TestDoStringFunctionCallBatchMutableCalleeAndEnvGuards(t *testing.T) {
	// 创建完整标准库 State，确保 `_G` 和 assert 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local offset = 0
local replaced = false
_G.function_call_guard_marker = 0
local function add(a, b)
  if b == 3 then
    offset = 100
    _G.function_call_guard_marker = b
  end
  if b == 4 then
    replaced = true
    add = function(x, y) return x - y + offset end
  end
  return a + b + offset
end
local sum = 0
for i = 1, 8 do
  sum = add(sum, i)
end
assert(replaced)
assert(_G.function_call_guard_marker == 3)
assert(sum == 584, sum)
`
	if err := DoString(state, source); err != nil {
		// 动态 callee/upvalue/env 变化必须按普通 Lua 调用语义逐次生效。
		t.Fatalf("DoString function call mutable guard failed: %v", err)
	}
}

// TestDoStringFunctionCallBatchHookTracebackGuards 验证 hook 与错误 traceback 边界。
//
// debug hook 打开时 function_call batch 必须回退逐指令路径；函数体抛错时也必须保留普通 CALL
// 帧与 xpcall(debug.traceback) 可见的错误现场。
func TestDoStringFunctionCallBatchHookTracebackGuards(t *testing.T) {
	// 创建完整标准库 State，确保 debug.sethook、xpcall 和 debug.traceback 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local hookEvents = 0
debug.sethook(function(event)
  assert(event == "line" or event == "call" or event == "return" or event == "count")
  hookEvents = hookEvents + 1
end, "crl", 1)
local function add(a, b)
  if b == 4 then error("function-call-guard") end
  return a + b
end
local ok, msg = xpcall(function()
  local sum = 0
  for i = 1, 5 do
    sum = add(sum, i)
  end
  return sum
end, debug.traceback)
debug.sethook()
assert(not ok)
assert(hookEvents > 0)
assert(string.find(msg, "function-call-guard", 1, true))
assert(string.find(msg, "stack traceback", 1, true))
assert(string.find(msg, "add", 1, true))
`
	if err := DoString(state, source); err != nil {
		// hook 与 traceback 必须保持普通函数调用路径的可见性。
		t.Fatalf("DoString function call hook traceback guard failed: %v", err)
	}
}

// TestDoStringFunctionCallBatchCoroutineYieldGuard 验证协程 yield 边界。
//
// 被调用函数可能在 CALL 内部 yield；未来整段 function_call batch 不能跨过 coroutine/continuation
// 边界直接提交后续迭代。
func TestDoStringFunctionCallBatchCoroutineYieldGuard(t *testing.T) {
	// 创建完整标准库 State，确保 coroutine 与 assert 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local co = coroutine.create(function()
  local function add(a, b)
    if b == 2 then coroutine.yield("pause", a, b) end
    return a + b
  end
  local sum = 0
  for i = 1, 3 do
    sum = add(sum, i)
  end
  return sum
end)
local ok, tag, a, b = coroutine.resume(co)
assert(ok and tag == "pause" and a == 1 and b == 2)
local ok2, result = coroutine.resume(co)
assert(ok2 and result == 6)
assert(coroutine.status(co) == "dead")
`
	if err := DoString(state, source); err != nil {
		// yield 后 resume 必须继续从函数 CALL 内部恢复并完成后续循环。
		t.Fatalf("DoString function call coroutine yield guard failed: %v", err)
	}
}

// TestDoStringFunctionCallBatchContextCancellationGuard 验证 context 取消边界。
//
// function_call batch 允许按窗口跳过普通指令入口，但每个虚拟 CALL 前仍必须能通过 State.CheckContext
// 观察宿主取消信号，并保留 context.Canceled 错误链。
func TestDoStringFunctionCallBatchContextCancellationGuard(t *testing.T) {
	// 使用按 CheckContext 次数取消的 context，避免测试依赖 goroutine 调度或真实时间。
	state, err := NewStateWithContext(newCancelAfterErrContext(50), Options{})
	if err != nil {
		// 有效测试 context 必须能创建 State。
		t.Fatalf("NewStateWithContext failed: %v", err)
	}
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local function add(a, b)
  return a + b
end
local sum = 0
for i = 1, 200000 do
  sum = add(sum, i)
end
return sum
`
	err = DoString(state, source)
	if !errors.Is(err, context.Canceled) {
		// 取消必须从热循环传播为保留 context.Canceled 错误链的运行时错误。
		t.Fatalf("DoString function call context cancellation error = %v", err)
	}
}

// TestWriteLuaCallResultsSingleReturnFastPath 验证普通单返回值写回语义。
//
// CALL C=2 是 Lua 函数调用最常见形态，快路径必须和通用路径一样覆盖函数槽、缺失返回补 nil，
// 清理开放返回边界，并丢弃不再属于结果区的临时实参槽。
func TestWriteLuaCallResultsSingleReturnFastPath(t *testing.T) {
	vm := runtime.NewVM(3)
	vm.SetOpenTop(2)
	if err := vm.SetRegister(1, runtime.StringValue("selector")); err != nil {
		// 第一个实参槽必须可写，供后续验证清理语义。
		t.Fatalf("set first argument failed: %v", err)
	}
	if err := vm.SetRegister(2, runtime.StringValue("payload")); err != nil {
		// 第二个实参槽必须可写，供后续验证清理语义。
		t.Fatalf("set second argument failed: %v", err)
	}
	request := &runtime.CallRequest{FunctionIndex: 0, ArgumentCount: 2, ReturnCount: 1}

	if err := writeLuaCallResults(vm, nil, 0, request, []runtime.Value{runtime.StringValue("ok")}); err != nil {
		// 单返回值写回合法寄存器必须成功。
		t.Fatalf("single return write failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(runtime.StringValue("ok")) {
		// 函数槽必须被第一个返回值覆盖。
		t.Fatalf("single return value mismatch: value=%#v ok=%v", value, ok)
	}
	finishLuaFixedCallResults(vm, nil, 0, request)
	firstArgument, firstArgumentOK := vm.Register(1)
	secondArgument, secondArgumentOK := vm.Register(2)
	if !firstArgumentOK || !firstArgument.IsNil() || !secondArgumentOK || !secondArgument.IsNil() {
		// 非结果实参槽不再属于活动 CALL 结果，必须被清理成 nil。
		t.Fatalf("call argument temporaries not cleared: first=%#v/%v second=%#v/%v", firstArgument, firstArgumentOK, secondArgument, secondArgumentOK)
	}
	if err := writeLuaCallResults(vm, nil, 0, request, nil); err != nil {
		// 缺失返回值也必须按 Lua 语义补 nil。
		t.Fatalf("single nil return write failed: %v", err)
	}
	value, ok = vm.Register(0)
	if !ok || !value.IsNil() {
		// 无实际返回值时固定返回槽必须写入 nil。
		t.Fatalf("single missing return mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestTryExecuteLeafAddReturnInCaller 验证 caller-side 叶子加法快路径。
//
// 该快路径必须只处理原生 integer/number 加法；需要字符串转换或元方法时必须回退完整 VM。
func TestTryExecuteLeafAddReturnInCaller(t *testing.T) {
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{bytecode.IntegerConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpAdd, 1, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	vm := runtime.NewVM(2)
	if err := vm.SetRegister(1, runtime.IntegerValue(41)); err != nil {
		// 测试准备阶段必须能写入调用实参。
		t.Fatalf("set caller argument failed: %v", err)
	}
	request := &runtime.CallRequest{FunctionIndex: 0, ArgumentCount: 1, ReturnCount: 1}
	closure := runtime.NewLuaClosure(proto, nil, nil)
	handled, err := tryExecuteLeafAddReturnInCaller(vm, closure, request)
	if err != nil {
		// 合法原生加法不应失败。
		t.Fatalf("caller leaf add failed: %v", err)
	}
	if !handled {
		// 目标形态必须被 caller-side 快路径处理。
		t.Fatalf("caller leaf add should be handled")
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(runtime.IntegerValue(42)) {
		// 结果必须直接写回函数槽。
		t.Fatalf("caller leaf add result mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(1, runtime.StringValue("41")); err != nil {
		// 测试准备阶段必须能覆盖调用实参。
		t.Fatalf("set string argument failed: %v", err)
	}
	handled, err = tryExecuteLeafAddReturnInCaller(vm, closure, request)
	if err != nil || handled {
		// 字符串数字需要完整 Lua 算术转换语义，必须回退。
		t.Fatalf("string operand should fallback: handled=%v err=%v", handled, err)
	}

	upvalueProto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 1, 0, 1),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	upvalueCell := runtime.NewClosedUpvalueCell(runtime.IntegerValue(7))
	upvalueClosure := runtime.NewLuaClosure(upvalueProto, nil, []*runtime.UpvalueCell{upvalueCell})
	if err := vm.SetRegister(1, runtime.IntegerValue(35)); err != nil {
		// 测试准备阶段必须能写入调用实参。
		t.Fatalf("set upvalue argument failed: %v", err)
	}
	handled, err = tryExecuteLeafAddReturnInCaller(vm, upvalueClosure, request)
	if err != nil {
		// 原生 upvalue 加法不应失败。
		t.Fatalf("upvalue leaf add failed: %v", err)
	}
	if !handled {
		// GETUPVAL;ADD;RETURN 形态必须被快路径识别。
		t.Fatalf("upvalue leaf add should be handled")
	}
	value, ok = vm.Register(0)
	if !ok || !value.RawEqual(runtime.IntegerValue(42)) {
		// 快路径必须读取 upvalue cell 当前值并写回结果。
		t.Fatalf("upvalue leaf add result mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestDoStringGluaProgressEvents 验证 glua.event 文件级事件、函数进度事件和事件管理能力。
func TestDoStringGluaProgressEvents(t *testing.T) {
	if !DefaultOptions().GluaEventsEnabled {
		// 当前构建未编译 glua events 时，正向事件用例不执行。
		t.Skip("glua events are not compiled")
	}
	// 创建完整标准库 State，确保 glua.event 命名空间可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local event = glua.event
assert(event.events.progress_function_call == "progress.function_call")
assert(event.events.progress_function_return == "progress.function_return")
assert(event.events.progress_function_error == "progress.function_error")
assert(event.events.progress_function_exit == "progress.function_exit")
local function observed(limit)
  local value = 0
  for index = 1, limit do value = value + index end
  return value
end
local moduleA = { run = function() return "a" end }
local moduleB = { run = function() return "b" end }
local function factory() return function() return "same-proto" end end
local sameProtoA = factory()
local sameProtoB = factory()
local calls = 0
local callID = event.setProgress(event.events.progress_function_call, function(ctx)
  calls = calls + 1
end, { whitelist = { "observed" } })
local config = event.getConfig(callID)
assert(config.whitelist[1] == "observed")
local exactCalls = 0
local exactID = event.setProgress(event.events.progress_function_call, function(ctx)
  exactCalls = exactCalls + 1
end, { whitelist = { moduleA.run } })
local initialList = event.eventList()
assert(initialList.totalEvents == 1, initialList.totalEvents)
assert(initialList.totalListeners == 2, initialList.totalListeners)
assert(initialList.activeListeners == 2, initialList.activeListeners)
assert(initialList.mutedListeners == 0, initialList.mutedListeners)
assert(initialList.syncListeners == 2, initialList.syncListeners)
assert(initialList.asyncListeners == 0, initialList.asyncListeners)
assert(initialList.events[1].event == event.events.progress_function_call)
assert(initialList.events[1].listeners == 2)
assert(moduleA.run() == "a")
assert(moduleB.run() == "b")
assert(exactCalls == 1, exactCalls)
assert(event.setConfig(exactID, { whitelist = { sameProtoA } }))
assert(sameProtoA() == "same-proto")
assert(sameProtoB() == "same-proto")
assert(exactCalls == 2, exactCalls)
assert(event.setConfig(exactID, { blacklist = { moduleA.run } }))
assert(moduleA.run() == "a")
assert(exactCalls == 2, exactCalls)
assert(event.setConfig(exactID, { whitelist = { moduleA.run } }))
assert(observed(3) == 6)
assert(calls == 1, calls)
assert(event.setConfig(callID, { blacklist = { "observed" } }))
assert(observed(1) == 1)
assert(calls == 1, calls)
assert(event.setConfig(callID, { whitelist = { "observed" } }))
assert(event.setMuted(callID, true))
local mutedList = event.eventList()
assert(mutedList.totalListeners == 2)
assert(mutedList.activeListeners == 1)
assert(mutedList.mutedListeners == 1)
assert(observed(1) == 1)
assert(calls == 1, calls)
assert(event.setMuted(callID, false))
assert(observed(1) == 1)
assert(calls == 2, calls)
assert(event.remove(exactID))
assert(event.remove(callID))
assert(not event.remove(callID))
local emptyList = event.eventList()
assert(emptyList.totalEvents == 0)
assert(emptyList.totalListeners == 0)
assert(observed(1) == 1)
assert(calls == 2, calls)
`
	if err := DoString(state, source); err != nil {
		// glua.event 必须覆盖文件生命周期、函数调用生命周期和事件管理操作。
		t.Fatalf("DoString glua progress events failed: %v object=%s", err, runtime.ErrorObject(err).DebugString())
	}
}

// TestDoStringGluaFileProgressEvents 验证 glua.event 的文件进度、异步自定义事件和毫秒时间戳。
func TestDoStringGluaFileProgressEvents(t *testing.T) {
	if !DefaultOptions().GluaEventsEnabled {
		// 当前构建未编译 glua events 时，正向事件用例不执行。
		t.Skip("glua events are not compiled")
	}
	// 创建完整标准库 State，确保异步事件可在 VM 安全点消费。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
local event = glua.event
local lines = 0
local starts = 0
local ends = 0
local asyncPayload
event.setProgress(event.events.progress_line, function(ctx)
  assert(ctx.timestamp > 0)
  lines = lines + 1
end)
event.setProgress(event.events.progress_start, function(ctx)
  starts = starts + 1
end)
event.setProgress(event.events.progress_end, function(ctx)
  ends = ends + 1
end)
event.setProgressAsync("custom.progress", function(ctx)
  assert(ctx.async)
  asyncPayload = ctx.payload
end)
local list = event.eventList()
assert(list.totalEvents == 4, list.totalEvents)
assert(list.totalListeners == 4, list.totalListeners)
assert(list.activeListeners == 4, list.activeListeners)
assert(list.mutedListeners == 0, list.mutedListeners)
assert(list.syncListeners == 3, list.syncListeners)
assert(list.asyncListeners == 1, list.asyncListeners)
event.callProgressAsync("custom.progress", "done")
local function work()
  return 1
end
assert(work() == 1)
assert(asyncPayload == "done", tostring(asyncPayload))
assert(lines > 0, lines)
assert(starts > 0, starts)
assert(ends > 0, ends)
`
	if err := DoString(state, source); err != nil {
		// 文件级进度事件必须覆盖 source line、嵌套代码块和异步自定义事件。
		t.Fatalf("DoString glua file progress events failed: %v object=%s", err, runtime.ErrorObject(err).DebugString())
	}
}

// TestDoStringConstSyntaxDefinesReadOnlyLocal 验证 const 语法糖声明只读绑定。
func TestDoStringConstSyntaxDefinesReadOnlyLocal(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxConst) {
		// 当前构建未编译 const 语法糖时，正向 const 用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// 创建完整标准库 State，确保 assert 可用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	source := `
const answer = 42
assert(answer == 42)
assert(_G.answer == 42)
do
  const answer = "inner"
  assert(answer == "inner")
  assert(_G.answer == 42)
end
assert(answer == 42)
`
	if err := DoString(state, source); err != nil {
		// 顶层 const 应进入当前环境，内层 const 仍按块级绑定读取并允许遮蔽。
		t.Fatalf("DoString const binding failed: %v", err)
	}
}

// TestLoadStringConstSyntaxRejectsAssignment 验证 const 绑定不能被重新赋值。
func TestLoadStringConstSyntaxRejectsAssignment(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxConst) {
		// 当前构建未编译 const 语法糖时，正向 const 用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// 创建完整标准库 State，用 LoadString 直接观察编译期错误。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	err := LoadString(state, "const answer = 42\nanswer = 7\n", "=(const-test)")
	if err == nil {
		// 对 const 绑定赋值必须在编译期失败。
		t.Fatalf("LoadString const assignment should fail")
	}
	if !strings.Contains(err.Error(), "cannot assign to const binding 'answer'") {
		// 错误文本应明确指出 const 覆盖。
		t.Fatalf("const assignment error mismatch: %v", err)
	}
}

// TestLoadStringConstSyntaxRejectsTopLevelLocalShadow 验证顶层同名 local 不能遮蔽全局 const。
func TestLoadStringConstSyntaxRejectsTopLevelLocalShadow(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxConst) {
		// 当前构建未编译 const 语法糖时，正向 const 用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// 创建带标准库的 State，编译顶层同名 local 遮蔽片段。
	state := NewState()
	OpenLibs(state)
	err := LoadString(state, "const answer = 42\nlocal answer = 7\n", "=(const-shadow-test)")
	if err == nil {
		// 顶层 local 会重新定义 ROOT 名称，必须按 const 约束拒绝。
		t.Fatalf("LoadString const top-level local shadow should fail")
	}
	if !strings.Contains(err.Error(), "cannot assign to const binding 'answer'") {
		// 错误文本应明确指出 const 覆盖。
		t.Fatalf("const top-level local shadow error mismatch: %v", err)
	}
}

// TestDoStringConstSyntaxProtectsGlobalsAcrossChunks 验证顶层 const 会在当前 State 的全局表中持久只读。
func TestDoStringConstSyntaxProtectsGlobalsAcrossChunks(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxConst) {
		// 当前构建未编译 const 语法糖时，正向 const 用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// 创建带标准库的 State，先声明全局 const，再用新 chunk 走运行期写保护。
	state := NewState()
	OpenLibs(state)
	if err := DoString(state, "const answer = 42\nconst missing = nil\n"); err != nil {
		// 顶层 const 声明应能投影到全局表，包括 nil 常量。
		t.Fatalf("DoString top-level const declaration failed: %v", err)
	}
	err := DoString(state, `
assert(answer == 42)
assert(missing == nil)
local okAnswer = pcall(function()
  answer = 7
end)
assert(not okAnswer)
local okMissing = pcall(function()
  missing = "filled"
end)
assert(not okMissing)
`)
	if err != nil {
		// 跨 chunk 写入应由 runtime table const key 拦截。
		t.Fatalf("DoString cross-chunk const protection failed: %v", err)
	}
}

// TestDoStringConstSyntaxSupportsMultipleNames 验证 const 支持 Lua 风格多名称声明。
func TestDoStringConstSyntaxSupportsMultipleNames(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxConst) {
		// 当前构建未编译 const 语法糖时，正向 const 用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// 创建带标准库的 State，执行多 const 绑定求值片段。
	state := NewState()
	OpenLibs(state)
	err := DoString(state, `
const first, second = 11, 31
assert(first + second == 42)
assert(_G.first == 11)
assert(_G.second == 31)
`)
	if err != nil {
		// 顶层多名称 const 声明应按当前环境只读全局读取。
		t.Fatalf("DoString const multi binding failed: %v", err)
	}
}

// TestLoadStringConstSyntaxRejectsCapturedAssignment 验证 const 捕获为 upvalue 后仍不能赋值。
func TestLoadStringConstSyntaxRejectsCapturedAssignment(t *testing.T) {
	if !DefaultSyntaxExtensions().Has(SyntaxConst) {
		// 当前构建未编译 const 语法糖时，正向 const 用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// 创建带标准库的 State，编译内层函数覆盖外层 const 的片段。
	state := NewState()
	OpenLibs(state)
	err := LoadString(state, "const answer = 42\nlocal function rewrite()\n  answer = 7\nend\n", "=(const-upvalue-test)")
	if err == nil {
		// 对 const upvalue 赋值必须在编译期失败。
		t.Fatalf("LoadString const upvalue assignment should fail")
	}
	if !strings.Contains(err.Error(), "cannot assign to const binding 'answer'") {
		// 错误文本应明确指出 const 覆盖。
		t.Fatalf("const upvalue assignment error mismatch: %v", err)
	}
}

// TestDoStringGluaConstTableProjectsReadOnlyFields 验证 `_glua_const` 子表会投影为 ROOT 只读字段。
func TestDoStringGluaConstTableProjectsReadOnlyFields(t *testing.T) {
	// 创建带标准库的 State，执行 `_glua_const` 投影和写保护片段。
	state := NewState()
	OpenLibs(state)
	err := DoString(state, `
local ROOT = {
  _glua_const = {
    answer = 42,
    [2] = "two",
  },
}
assert(ROOT.answer == 42)
assert(ROOT[2] == "two")
local okField = pcall(function()
  ROOT.answer = 7
end)
assert(not okField)
local key = "answer"
local okDynamic = pcall(function()
  ROOT[key] = 8
end)
assert(not okDynamic)
local okIndex = pcall(function()
  ROOT[2] = "changed"
end)
assert(not okIndex)
ROOT.mutable = "kept"
assert(ROOT.mutable == "kept")
`)
	if err != nil {
		// `_glua_const` 投影字段应可读且只读，普通字段仍可写。
		t.Fatalf("DoString _glua_const table failed: %v", err)
	}
}
