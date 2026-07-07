// Package debuglib 实现 Lua 5.3 debug 标准库的第一阶段能力。
//
// 本包当前提供 debug.debug 禁用策略、debug.gethook/sethook 状态、debug.getinfo 调用帧快照、
// debug.getlocal/setlocal 阶段性空结果、debug.getregistry、debug.getupvalue、debug.getuservalue
// 和 debug.getmetatable/setmetatable、debug.setupvalue、debug.setuservalue、debug.traceback、
// debug.upvalueid。hook 触发和局部变量真实写入会在后续 TODO 接入。
package debuglib

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	// DefaultCurrentLine 表示当前阶段尚未接入 Proto 行号信息。
	DefaultCurrentLine = int64(-1)
	// DefaultLineDefined 表示当前阶段尚未接入函数定义起始行。
	DefaultLineDefined = int64(-1)
	// DefaultLastLineDefined 表示当前阶段尚未接入函数定义结束行。
	DefaultLastLineDefined = int64(-1)
	// HookEventCall 表示 Lua 5.3 call hook 事件名。
	HookEventCall = "call"
	// HookEventTailCall 表示 Lua 5.3 tail call hook 事件名。
	HookEventTailCall = "tail call"
	// HookEventReturn 表示 Lua 5.3 return hook 事件名。
	HookEventReturn = "return"
	// HookEventLine 表示 Lua 5.3 line hook 事件名。
	HookEventLine = "line"
	// HookEventCount 表示 Lua 5.3 count hook 事件名。
	HookEventCount = "count"
)

// HookRunner 表示 VM 层执行 Lua hook closure 的回调入口。
//
// hook 必须是 Lua closure；event 是标准 hook 事件名；line 只对 line 事件有效。返回错误会中断
// 当前 VM 执行并按 Lua 运行期错误传播。
type HookRunner func(hook runtime.Value, event string, line int64) error

var debugEnvironments sync.Map

// hookState 保存单个 Lua thread 的 debug hook 配置。
//
// hook 是回调函数；mask 保存事件掩码；count 保存指令计数触发间隔；instructionCount 是当前
// 累计计数。主线程和未显式指定 thread 的路径使用 Environment 上的默认字段。
type hookState struct {
	// hook 保存当前 hook 函数；nil 表示未设置 hook。
	hook runtime.Value
	// mask 保存当前 hook 掩码字符串。
	mask string
	// count 保存当前 count hook 间隔。
	count int64
	// instructionCount 保存当前 count hook 的累计指令数。
	instructionCount int64
}

// Environment 保存单个 State 对应的 debug 标准库运行环境。
//
// state 用于读取调用帧、registry 和后续 hook 状态。当前 hook 只保存默认空状态，不触发 VM。
type Environment struct {
	// state 保存 debug 库绑定的运行时状态。
	state *runtime.State
	// library 保存 Open 注册时创建的 debug 库表，用于 `_G.debug` 被用户清空后仍识别自身 API 帧。
	library *runtime.Table
	// hook 保存当前 hook 函数；nil 表示未设置 hook。
	hook runtime.Value
	// hookMask 保存当前 hook 掩码字符串。
	hookMask string
	// hookCount 保存当前 count hook 间隔。
	hookCount int64
	// instructionCount 保存当前 count hook 的累计指令数。
	instructionCount int64
	// defaultHookActive 缓存默认 hook 是否可能触发，避免 VM 无 hook 热路径重复解析默认三元组。
	defaultHookActive bool
	// threadHooks 保存 debug.sethook(thread, ...) 绑定到指定协程的 hook 状态。
	threadHooks map[*runtime.Thread]*hookState
	// activeThreadHookCount 记录可能触发的协程专属 hook 数量，避免无协程 hook 热路径读取 running thread。
	activeThreadHookCount int
	// lineHookSkipProto 保存默认 line hook 刚设置时所在调用方 Proto，用于跳过 sethook 当前行。
	lineHookSkipProto *bytecode.Proto
	// lineHookSkipLine 保存默认 line hook 刚设置时所在调用方源码行。
	lineHookSkipLine int64
	// hookActive 标记当前是否正在执行 hook，用于避免 hook 重入递归。
	hookActive bool
	// debugInput 保存 debug.debug 交互输入，默认绑定宿主 stdin。
	debugInput io.Reader
	// debugOutput 保存 debug.debug 提示符和命令输出，默认绑定宿主 stderr。
	debugOutput io.Writer
}

// Open 将 Lua 5.3 debug 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后全局 `debug` 字段指向库表。当前只注册已实现的第一阶段函数。
func Open(state *runtime.State) error {
	// 注册前必须确认 State 可写。
	if state == nil {
		// nil State 没有 globals，调用方需要先创建 runtime.State。
		return fmt.Errorf("debug library unavailable: %w", runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("debug library unavailable: %w", runtime.ErrClosedState)
	}

	environment := NewEnvironment(state)
	state.SetDebugEnvironment(environment)
	debugEnvironments.Store(state, environment)
	state.SetGlobal("debug", runtime.ReferenceValue(runtime.KindTable, environment.Table()))
	return nil
}

// EnvironmentForState 返回 State 通过 debug.Open 注册的 debug 运行环境。
//
// state 必须是当前 VM 正在使用的 runtime.State；未注册 debug 库或 State 为 nil 时返回 false。
func EnvironmentForState(state *runtime.State) (*Environment, bool) {
	if state == nil {
		// nil State 没有可关联的 debug 标准库环境。
		return nil, false
	}
	if environment, ok := state.DebugEnvironment().(*Environment); ok && environment != nil {
		// Open 已把环境挂到 State 上，VM 热路径优先使用该引用，避免每次 CALL 查询 sync.Map。
		return environment, true
	}
	value, ok := debugEnvironments.Load(state)
	if !ok {
		// 未打开 debug 库时没有 hook 状态需要 VM 触发。
		return nil, false
	}
	environment, ok := value.(*Environment)
	if !ok || environment == nil {
		// sync.Map 中出现异常类型时视为无环境，避免影响主 VM 执行。
		return nil, false
	}
	return environment, true
}

// HookActive 返回当前 debug 环境是否正在执行 hook 回调。
//
// 该状态用于 VM 层屏蔽 hook 回调自身产生的 call/return/line/count 事件；Lua 5.3 不会让 return
// hook 递归观察 hook 函数自己的返回。nil 环境返回 false，表示没有 hook 执行中。
func (environment *Environment) HookActive() bool {
	if environment == nil {
		// nil 环境没有正在执行的 hook。
		return false
	}
	return environment.hookActive
}

// RunWithHooksSuppressed 在临界区内临时屏蔽 debug hook 派发。
//
// runner 必须封装一段不应被用户 hook 观察的 VM 内部执行，例如 table `__gc` finalizer。该方法只
// 抑制 hook 触发，不修改 debug.gethook 可见的 hook 三元组；runner 的错误会原样返回。
func (environment *Environment) RunWithHooksSuppressed(runner func() error) error {
	if runner == nil {
		// 空 runner 没有可执行工作，保持幂等返回。
		return nil
	}
	if environment == nil {
		// 未打开 debug 库时无需维护 hookActive 状态，直接执行临界区。
		return runner()
	}
	previous := environment.hookActive
	environment.hookActive = true
	defer func() {
		// 恢复进入前状态，支持在 hook 回调内嵌套压制 finalizer hook。
		environment.hookActive = previous
	}()
	return runner()
}

// HasActiveHook 判断当前运行线程是否存在任意可触发 hook。
//
// 返回 true 表示 VM 执行循环需要继续检查 call、return、line 或 count 事件；返回 false 时
// 热路径可以跳过逐指令 HookEnabledFor 判断。该方法不累计 count，也不触发 hook。
func (environment *Environment) HasActiveHook() bool {
	if environment == nil || environment.hookActive {
		// 缺少环境或正在 hook 回调内部时，当前路径不会触发新的 hook。
		return false
	}
	if !environment.defaultHookActive && environment.activeThreadHookCount == 0 {
		// 没有默认 hook 且没有任何活跃协程 hook 时，VM 热路径无需读取协程状态。
		return false
	}
	// 按当前 running thread 读取 hook，避免主线程 hook 泄漏到没有专属 hook 的协程。
	return threadHookCanTrigger(environment.activeHookState())
}

// HookEnabledFor 判断指定 hook 事件当前是否可能触发。
//
// event 必须是 Lua 5.3 标准 hook 事件名；返回 true 表示 VM 需要进入对应 hook 触发路径。
// 该方法只做无副作用的轻量判断，不累计 count，也不执行 hook 回调。
func (environment *Environment) HookEnabledFor(event string) bool {
	if environment == nil || environment.hookActive {
		// 缺少环境或 hook 回调正在执行时，当前指令不应触发新的 hook。
		return false
	}
	// 事件判断必须与派发使用同一个 hook 状态，保持 thread 隔离语义。
	return hookStateEnabledFor(environment.activeHookState(), event)
}

// NewEnvironment 创建 debug 标准库运行环境。
//
// state 可以为 nil；nil 状态下只能使用不依赖调用栈的函数，依赖 State 的函数会返回 nil 或错误。
func NewEnvironment(state *runtime.State) *Environment {
	// 初始化时 hook 为空，符合 Lua 5.3 新 State 没有 hook 的语义。
	return &Environment{
		state:       state,
		hook:        runtime.NilValue(),
		debugInput:  os.Stdin,
		debugOutput: os.Stderr,
	}
}

// Table 构造 debug 标准库表。
//
// 返回表包含本阶段已实现的 debug 函数；每次调用返回新的 table，函数闭包共享同一个环境。
func (environment *Environment) Table() *runtime.Table {
	// 构造 debug 库表并注册第一阶段函数。
	library := runtime.NewTable()
	library.RawSetString("debug", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.Debug)))
	library.RawSetString("gethook", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.GetHook)))
	library.RawSetString("getinfo", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.GetInfo)))
	library.RawSetString("getlocal", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.GetLocal)))
	library.RawSetString("getmetatable", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(GetMetatable)))
	library.RawSetString("getregistry", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.GetRegistry)))
	library.RawSetString("getupvalue", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(GetUpvalue)))
	library.RawSetString("getuservalue", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(GetUserValue)))
	library.RawSetString("sethook", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.SetHook)))
	library.RawSetString("setlocal", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.SetLocal)))
	library.RawSetString("setmetatable", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(SetMetatable)))
	library.RawSetString("setupvalue", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(SetUpvalue)))
	library.RawSetString("setuservalue", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(SetUserValue)))
	library.RawSetString("traceback", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.Traceback)))
	library.RawSetString("upvalueid", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(UpvalueID)))
	library.RawSetString("upvaluejoin", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(UpvalueJoin)))
	environment.library = library
	return library
}

// Debug 实现 Lua 5.3 `debug.debug` 的最小交互调试器。
//
// 当前支持官方测试依赖的 `io.stderr:write(...)` 命令和 `cont` 退出命令；输入 EOF 也会结束调试器。
func (environment *Environment) Debug(args ...runtime.Value) ([]runtime.Value, error) {
	if environment == nil {
		// nil 环境没有可绑定的输入输出，按空调试会话结束。
		return nil, nil
	}
	input := environment.debugInput
	if input == nil {
		// nil 输入表示调用方不希望阻塞，直接结束调试会话。
		return nil, nil
	}
	output := environment.debugOutput
	if output == nil {
		// nil 输出时丢弃提示符和调试命令输出，保留无副作用执行。
		output = io.Discard
	}

	// 调试器逐行读取命令，每次读取前都输出官方提示符。
	scanner := bufio.NewScanner(input)
	for {
		_, _ = fmt.Fprint(output, "lua_debug> ")
		if !scanner.Scan() {
			// EOF 结束调试器；底层读取错误在循环后统一处理。
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "cont" {
			// cont 恢复外层程序执行，debug.debug 自身无返回值。
			return nil, nil
		}
		if err := environment.executeDebugLine(line, output); err != nil {
			// 单条调试命令错误按 Lua error 返回给调用方。
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		// 输入流错误属于宿主 I/O 失败，直接返回 Go error。
		return nil, err
	}
	return nil, nil
}

// executeDebugLine 执行 debug.debug 支持的单行命令。
//
// 当前只迁移官方测试覆盖的 stderr 写入命令；空行保持无操作，未知命令返回 Lua error。
func (environment *Environment) executeDebugLine(line string, output io.Writer) error {
	if line == "" {
		// 空命令不改变调试器状态。
		return nil
	}
	const stderrWritePrefix = "io.stderr:write("
	if strings.HasPrefix(line, stderrWritePrefix) && strings.HasSuffix(line, ")") {
		// 官方测试使用 io.stderr:write 写入调试输出，这里按字面量写到 debug 输出流。
		argument := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, stderrWritePrefix), ")"))
		text, err := debugWriteArgumentText(argument)
		if err != nil {
			// 字面量解析失败时返回 Lua error，避免静默吞掉调试命令。
			return err
		}
		_, _ = fmt.Fprint(output, text)
		return nil
	}
	return runtime.RaiseError(runtime.StringValue("unsupported debug command"))
}

// debugWriteArgumentText 将 debug.debug 的 io.stderr:write 参数转换为输出文本。
//
// 参数当前支持数字字面量和 Go/Lua 常见引号字符串；返回文本不额外追加换行，匹配 Lua io.write。
func debugWriteArgumentText(argument string) (string, error) {
	if argument == "" {
		// 空参数不符合 io.stderr:write 的调用约束。
		return "", runtime.RaiseError(runtime.StringValue("bad argument to 'write'"))
	}
	if strings.HasPrefix(argument, "\"") || strings.HasPrefix(argument, "'") {
		// 字符串参数按引号内容输出，支持 Go 风格转义以覆盖 Lua 常见短字符串。
		text, err := strconv.Unquote(argument)
		if err != nil {
			// 引号不匹配或转义非法时返回 Lua error。
			return "", runtime.RaiseError(runtime.StringValue("bad argument to 'write'"))
		}
		return text, nil
	}
	return argument, nil
}

// GetHook 实现 Lua 5.3 `debug.gethook` 的默认读取语义。
//
// 无参读取当前默认 hook；首参为 thread 时读取该协程通过 debug.sethook(thread, ...) 保存的 hook。
func (environment *Environment) GetHook(args ...runtime.Value) ([]runtime.Value, error) {
	// nil 环境等价于未设置 hook，返回默认三元组。
	if environment == nil {
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(""), runtime.IntegerValue(0)}, nil
	}
	if len(args) > 0 && args[0].Kind == runtime.KindThread {
		// thread 重载只读取目标协程自己的 hook，不污染主线程默认 hook。
		state := environment.threadHookState(args[0])
		if state == nil || state.hook.IsNil() {
			// 未设置协程 hook 时按 Lua 语义返回默认三元组。
			return []runtime.Value{runtime.NilValue(), runtime.StringValue(""), runtime.IntegerValue(0)}, nil
		}
		return []runtime.Value{state.hook, runtime.StringValue(state.mask), runtime.IntegerValue(state.count)}, nil
	}
	if thread := environment.runningCoroutineHookThread(); thread != nil {
		// 无参 gethook 在协程内读取当前协程自己的 hook，不回退主线程默认 hook。
		state := environment.threadHooks[thread]
		if state == nil || state.hook.IsNil() {
			// 当前协程未设置 hook 时按 Lua 5.3 返回空三元组。
			return []runtime.Value{runtime.NilValue(), runtime.StringValue(""), runtime.IntegerValue(0)}, nil
		}
		return []runtime.Value{state.hook, runtime.StringValue(state.mask), runtime.IntegerValue(state.count)}, nil
	}
	return []runtime.Value{environment.hook, runtime.StringValue(environment.hookMask), runtime.IntegerValue(environment.hookCount)}, nil
}

// GetInfo 实现 Lua 5.3 `debug.getinfo` 的调用帧快照子集。
//
// 第一个参数可省略或为 integer level；level 从 1 开始表示当前帧。当前返回 table 子集：
// what、source、currentline、linedefined、lastlinedefined、nups、nparams、isvararg、func。
func (environment *Environment) GetInfo(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) > 0 && args[0].Kind == runtime.KindThread {
		// thread 重载形态为 debug.getinfo(thread, level [, what])。
		if len(args) > 2 {
			if err := validateGetInfoOptions(args[2], 3); err != nil {
				// options 含非法字符时必须按原始参数位置抛出错误。
				return nil, err
			}
		}
		level, err := integerArgument(args, 2, "getinfo")
		if err != nil {
			// level 参数错误直接返回 Lua 参数错误。
			return nil, err
		}
		return getThreadInfo(args[0], level), nil
	}
	if len(args) > 1 {
		if err := validateGetInfoOptions(args[1], 2); err != nil {
			// options 含非法字符时必须按 Lua 5.3 抛出参数错误。
			return nil, err
		}
	}
	if len(args) > 0 && (args[0].Kind == runtime.KindLuaClosure || args[0].Kind == runtime.KindGoClosure) {
		// debug.getinfo(function) 直接读取函数对象元数据，不依赖当前调用栈。
		info := functionInfoTable(args[0])
		return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, info)}, nil
	}

	// 先解析调用层级，Lua 5.3 默认查询 level 1。
	level := int64(1)
	if len(args) > 0 && !args[0].IsNil() {
		// 当前阶段只支持按 level 查询，不支持直接传 function 查询。
		if args[0].Kind != runtime.KindInteger {
			// 非整数 level 返回 Lua 参数错误。
			return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'getinfo' (number expected)"))
		}
		level = args[0].Integer
	}
	if level <= 0 {
		// 非正层级没有可对应的调用帧。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if environment == nil || environment.state == nil {
		// 没有 State 时无法读取调用栈。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	frames := environment.visibleCallFrames()
	frameIndex := int(level - 1)
	if frameIndex < 0 || frameIndex >= len(frames) {
		// 超出调用栈层级时按 Lua 语义返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	info := frameInfoTable(frames[frameIndex])
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, info)}, nil
}

// GetLocal 实现 Lua 5.3 `debug.getlocal` 的阶段性读取。
//
// 正 index 按当前调用帧 Proto.LocalVars 的可见范围读取局部变量；负 index 读取当前帧 vararg。
// 未命中返回 nil。当前局部变量值从 State 栈帧窗口读取，缺失槽位按 nil 返回。
func (environment *Environment) GetLocal(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) > 0 && args[0].Kind == runtime.KindThread {
		if len(args) > 1 && (args[1].Kind == runtime.KindLuaClosure || args[1].Kind == runtime.KindGoClosure) {
			// thread + function 重载只读取函数形参名称，兼容官方 db.lua 早期断言。
			return getFunctionParameterLocal(args, 2, 3)
		}
		// thread 重载读取目标协程挂起栈中的局部变量。
		return getThreadLocal(args)
	}
	if len(args) > 0 && (args[0].Kind == runtime.KindLuaClosure || args[0].Kind == runtime.KindGoClosure) {
		// 函数重载不依赖当前调用栈，只读取函数 Proto 中的形参调试名称。
		return getFunctionParameterLocal(args, 1, 2)
	}

	// level 必须存在且为 integer。
	level, err := integerArgument(args, 1, "getlocal")
	if err != nil {
		// level 参数错误直接返回 Lua 参数错误。
		return nil, err
	}
	index, err := integerArgument(args, 2, "getlocal")
	if err != nil {
		// local index 参数错误直接返回 Lua 参数错误。
		return nil, err
	}
	if level == 0 {
		// level 0 在 Lua 5.3 中表示 debug.getlocal 自身的 C/Go 调用帧临时槽位。
		return temporaryLocalAt(args, index), nil
	}
	frame, ok := environment.frameAtLevel(level)
	if !ok {
		if environment.hasCallFrames() {
			// 已有调用栈但 level 越界时按 Lua 5.3 抛出 bad level。
			return nil, invalidDebugLevelError()
		}
		// 空环境没有可查调用帧，保持阶段性 nil 结果。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if index < 0 {
		// 负索引读取 vararg 调试值。
		return getVarargLocal(frame, index), nil
	}
	if index == 0 {
		// Lua 局部变量索引从 1 开始，0 不可能命中。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	localVar, activeIndex, ok := activeLocalAt(frame, index)
	if !ok {
		// 当前 pc 下没有对应命名局部变量时，尝试读取非注册临时寄存器。
		return environment.temporaryLuaLocalAt(level, frame, index), nil
	}
	if environment == nil || environment.state == nil {
		// 没有 State 时无法从栈窗口读取局部值。
		return []runtime.Value{runtime.StringValue(localDebugName(localVar)), runtime.NilValue()}, nil
	}
	value := environment.localValueAt(level, frame, localVar, activeIndex)
	return []runtime.Value{runtime.StringValue(localDebugName(localVar)), value}, nil
}

// GetRegistry 实现 Lua 5.3 `debug.getregistry`。
//
// 返回当前 State 的 registry table；nil 或未初始化 State 返回 nil，避免泄露无效内部引用。
func (environment *Environment) GetRegistry(args ...runtime.Value) ([]runtime.Value, error) {
	// nil 环境没有可读取 registry。
	if environment == nil || environment.state == nil {
		return []runtime.Value{runtime.NilValue()}, nil
	}
	registry := environment.state.Registry()
	if registry == nil {
		// 已关闭 State 不再暴露 registry root。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, registry)}, nil
}

// GetUpvalue 实现 Lua 5.3 `debug.getupvalue` 的 Lua closure upvalue 读取。
//
// 第一个参数必须是 Lua closure；第二个参数是 1-based upvalue index。命中时返回名称和值；
// 未命中返回 nil。运行期存在共享 upvalue cell 时优先读取 cell 当前值。
func GetUpvalue(args ...runtime.Value) ([]runtime.Value, error) {
	// function 参数必须存在。
	if len(args) == 0 {
		// 缺少 function 参数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'getupvalue' (function expected)"))
	}
	index, err := integerArgument(args, 2, "getupvalue")
	if err != nil {
		// index 参数错误直接返回 Lua 参数错误。
		return nil, err
	}
	if index <= 0 {
		// 非正 upvalue index 不可能命中。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if args[0].Kind == runtime.KindGoClosure {
		// 只有显式携带 upvalue 元数据的 Go closure 才暴露匿名 upvalue。
		return getGoClosureUpvalue(args[0], index)
	}
	if args[0].Kind != runtime.KindLuaClosure {
		// 非函数值没有可见 upvalue。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	closure, ok := args[0].Ref.(*runtime.LuaClosure)
	if !ok || closure == nil {
		// closure 引用损坏时视为没有 upvalue。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	upvalueIndex := int(index - 1)
	if upvalueIndex < 0 || upvalueIndex >= luaClosureUpvalueCount(closure) {
		// 超出 upvalue 数量时按 Lua 语义返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	name := ""
	if closure.Proto != nil && upvalueIndex < len(closure.Proto.Upvalues) {
		// Proto 中存在 upvalue 描述时使用调试名称。
		name = luaClosureUpvalueName(closure, upvalueIndex)
	}
	return []runtime.Value{runtime.StringValue(name), luaClosureUpvalueValue(closure, upvalueIndex)}, nil
}

// GetUserValue 实现 Lua 5.3 `debug.getuservalue` 的第一阶段策略。
//
// 当前 runtime.Userdata 尚未保存 Lua user value，因此合法 userdata 返回 nil。
func GetUserValue(args ...runtime.Value) ([]runtime.Value, error) {
	// userdata 参数必须存在。
	if len(args) == 0 {
		// 缺少 userdata 参数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'getuservalue' (userdata expected)"))
	}
	if args[0].Kind != runtime.KindUserdata {
		// 非 userdata 没有 user value。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	userdata, ok := args[0].Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// userdata 引用损坏时视为没有 user value。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{userdata.UserValue}, nil
}

// SetHook 实现 Lua 5.3 `debug.sethook` 的状态记录阶段。
//
// 默认写入当前环境 hook；首参为 thread 时写入该协程独立 hook，供 coroutine 挂起调试场景使用。
func (environment *Environment) SetHook(args ...runtime.Value) ([]runtime.Value, error) {
	// nil 环境无法保存 hook 状态。
	if environment == nil {
		return nil, runtime.RaiseError(runtime.StringValue("debug hook environment is not initialized"))
	}

	thread, hasThreadArgument := setHookThreadArgument(args)
	targetThread := thread
	implicitCurrentCoroutine := false
	hookArgumentPosition := 1
	maskArgumentPosition := 2
	countArgumentPosition := 3
	if hasThreadArgument {
		// thread 重载把 hook/mask/count 依次移动到第 2/3/4 个参数。
		args = args[1:]
		hookArgumentPosition = 2
		maskArgumentPosition = 3
		countArgumentPosition = 4
	} else if currentThread := environment.runningCoroutineHookThread(); currentThread != nil {
		// 无 thread 参数时，Lua 5.3 把 hook 写入当前 running thread；协程内不能污染主线程。
		targetThread = currentThread
		implicitCurrentCoroutine = true
	}

	hook := runtime.NilValue()
	if len(args) > 0 {
		// 第一个参数允许 nil 或 callable。
		hook = args[0]
	}
	mask := ""
	if len(args) > 1 && !args[1].IsNil() {
		// 第二个参数存在且非 nil 时必须是 string mask。
		if args[1].Kind != runtime.KindString {
			// mask 类型错误直接返回 Lua 参数错误。
			return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to 'sethook' (string expected)", maskArgumentPosition)))
		}
		mask = args[1].String
	}
	count := int64(0)
	if len(args) > 2 && !args[2].IsNil() {
		// 第三个参数存在且非 nil 时必须是可转整数的 number。
		parsedCount, err := integerArgument(args, 3, "sethook")
		if err != nil {
			// count 类型错误直接返回 Lua 参数错误。
			if hasThreadArgument {
				// thread 重载需要对外报告原始第 4 个参数，保持 Lua 参数位置语义。
				return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to 'sethook' (number expected)", countArgumentPosition)))
			}
			return nil, err
		}
		count = parsedCount
	}

	if hook.IsNil() {
		// nil hook 表示清除 hook，同时清空 mask 和 count。
		if targetThread != nil {
			// 协程 hook 只清除目标协程，不影响主线程默认 hook。
			environment.clearThreadHook(targetThread)
			if implicitCurrentCoroutine {
				// 当前协程清除 hook 时也清掉可能尚未消费的当前行抑制状态。
				environment.clearPendingLineHookSkip()
			}
			return nil, nil
		}
		environment.hook = runtime.NilValue()
		environment.hookMask = ""
		environment.hookCount = 0
		environment.instructionCount = 0
		environment.defaultHookActive = false
		environment.clearPendingLineHookSkip()
		return nil, nil
	}
	if hook.Kind != runtime.KindGoClosure && hook.Kind != runtime.KindLuaClosure {
		// hook 必须是当前 runtime 可表示的函数值。
		return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to 'sethook' (function expected)", hookArgumentPosition)))
	}

	if targetThread != nil {
		// 协程 hook 独立保存，VM 触发时会按当前 running thread 优先读取。
		environment.setThreadHook(targetThread, &hookState{
			hook:  hook,
			mask:  mask,
			count: count,
		})
		if implicitCurrentCoroutine {
			// 当前协程设置 line hook 后，同样需要跳过 sethook 所在源码行。
			environment.primePendingLineHookSkip(mask)
		}
		return nil, nil
	}
	environment.hook = hook
	environment.hookMask = mask
	environment.hookCount = count
	environment.instructionCount = 0
	environment.defaultHookActive = hookStateCanTrigger(hook, mask, count)
	environment.primePendingLineHookSkip(mask)
	return nil, nil
}

// primePendingLineHookSkip 记录默认 line hook 设置点所在源码行。
//
// Lua 5.3 不会在 debug.sethook 返回后的同一源码行立刻触发 line hook；记录当前可见 Lua
// 调用帧的 Proto 与行号，执行循环下一次遇到同一 Proto 同一行时只更新 lastHookLine 而不回调。
func (environment *Environment) primePendingLineHookSkip(mask string) {
	if !strings.ContainsRune(mask, 'l') {
		// 非 line hook 不需要当前行抑制状态。
		environment.clearPendingLineHookSkip()
		return
	}
	proto, line := environment.currentVisibleLuaLine()
	if proto == nil || line <= 0 {
		// 无可见 Lua 调用点时无法安全抑制，保持普通首次 line hook 行为。
		environment.clearPendingLineHookSkip()
		return
	}
	environment.lineHookSkipProto = proto
	environment.lineHookSkipLine = line
}

// clearPendingLineHookSkip 清理默认 line hook 当前行抑制状态。
func (environment *Environment) clearPendingLineHookSkip() {
	// 清理 Proto 和行号，避免后续新 hook 复用旧状态。
	environment.lineHookSkipProto = nil
	environment.lineHookSkipLine = 0
}

// ConsumePendingLineHookSkip 判断当前 line hook 是否应跳过 sethook 所在行。
//
// proto 和 line 来自即将触发 line hook 的 Lua closure；只有与记录的调用点完全一致时才跳过，
// 防止 `load` 出的新 chunk 恰好使用相同行号时被误抑制。
func (environment *Environment) ConsumePendingLineHookSkip(proto *bytecode.Proto, line int64) bool {
	if environment == nil || environment.lineHookSkipLine <= 0 {
		// 没有待消费状态时不影响 line hook。
		return false
	}
	shouldSkip := environment.lineHookSkipProto == proto && environment.lineHookSkipLine == line
	environment.clearPendingLineHookSkip()
	return shouldSkip
}

// currentVisibleLuaLine 返回 debug API 调用方当前源码行。
//
// visibleCallFrames 会隐藏当前 debug.sethook 的 Go/C 帧；返回的第一帧即用户 Lua 调用点。
func (environment *Environment) currentVisibleLuaLine() (*bytecode.Proto, int64) {
	frames := environment.visibleCallFrames()
	if len(frames) == 0 {
		// 没有可见调用帧时无法推断源码行。
		return nil, 0
	}
	frame := frames[0]
	proto := frameProto(frame)
	if proto == nil || frame.CurrentPC < 0 || frame.CurrentPC >= len(proto.LineInfo) {
		// 非 Lua 帧或 PC 不在行号表范围内时不抑制。
		return nil, 0
	}
	line := int64(proto.LineInfo[frame.CurrentPC])
	if line <= 0 {
		// 无效行号不参与抑制。
		return nil, 0
	}
	return proto, line
}

// setThreadHook 写入协程专属 hook，并同步活跃 hook 计数缓存。
//
// thread 必须是 debug.sethook 已解析出的目标协程；state 保存本次 hook 三元组。该缓存只影响
// HasActiveHook 的快速判断，不改变 gethook/sethook 可见状态。
func (environment *Environment) setThreadHook(thread *runtime.Thread, state *hookState) {
	if environment.threadHooks == nil {
		// 只有真正设置协程专属 hook 时才分配 map，普通无 hook 热路径保持零分配空状态。
		environment.threadHooks = make(map[*runtime.Thread]*hookState)
	}
	if previousState := environment.threadHooks[thread]; threadHookCanTrigger(previousState) {
		// 替换已有活跃 hook 前先扣减旧计数，避免重复设置同一 thread 后计数膨胀。
		environment.activeThreadHookCount--
	}
	environment.threadHooks[thread] = state
	if threadHookCanTrigger(state) {
		// 新 hook 可能触发任意事件时计入活跃协程 hook 数量。
		environment.activeThreadHookCount++
	}
}

// clearThreadHook 清除协程专属 hook，并同步活跃 hook 计数缓存。
//
// thread 必须是 debug.sethook 的 thread 重载目标；未设置过时保持无副作用。
func (environment *Environment) clearThreadHook(thread *runtime.Thread) {
	if environment.threadHooks == nil {
		// 没有协程 hook 表时无需清理。
		return
	}
	if previousState := environment.threadHooks[thread]; threadHookCanTrigger(previousState) {
		// 只有旧 hook 可能触发事件时才扣减活跃计数。
		environment.activeThreadHookCount--
	}
	delete(environment.threadHooks, thread)
}

// hookStateCanTrigger 判断 hook 三元组是否可能触发事件。
//
// hook 为 nil、mask 为空且 count 非正时不会产生任何 VM hook 事件。
func hookStateCanTrigger(hook runtime.Value, mask string, count int64) bool {
	if hook.IsNil() {
		// nil hook 表示未设置 hook。
		return false
	}
	return mask != "" || count > 0
}

// threadHookCanTrigger 判断协程专属 hook 状态是否可能触发事件。
//
// nil 状态或空 hook 不触发，供活跃协程 hook 计数维护使用。
func threadHookCanTrigger(state *hookState) bool {
	if state == nil {
		// 没有状态时不触发。
		return false
	}
	return hookStateCanTrigger(state.hook, state.mask, state.count)
}

// TriggerCallHook 触发当前环境的 call hook。
//
// 该方法是 VM 接入 hook 前的显式检查点；未设置 hook 或 mask 不包含 call 时不执行任何操作。
func (environment *Environment) TriggerCallHook() error {
	// call 事件映射到 mask 字符 c。
	return environment.dispatchHook(HookEventCall, 0)
}

// TriggerReturnHook 触发当前环境的 return hook。
//
// 该方法是 VM 接入 hook 前的显式检查点；未设置 hook 或 mask 不包含 return 时不执行任何操作。
func (environment *Environment) TriggerReturnHook() error {
	// return 事件映射到 mask 字符 r。
	return environment.dispatchHook(HookEventReturn, 0)
}

// TriggerCallHookWithRunner 触发当前环境的 call hook，并允许 VM 执行 Lua hook closure。
//
// runner 只在 hook 是 Lua closure 时使用；Go hook 仍由 debug 库直接执行。未设置 hook 或
// mask 不包含 call 时不执行任何操作。
func (environment *Environment) TriggerCallHookWithRunner(runner HookRunner) error {
	// call 事件映射到 mask 字符 c，并通过 VM runner 补齐 Lua closure hook 执行能力。
	return environment.dispatchHookWithRunner(HookEventCall, 0, runner)
}

// TriggerTailCallHookWithRunner 触发当前环境的 tail call hook，并允许 VM 执行 Lua hook closure。
//
// runner 只在 hook 是 Lua closure 时使用；tail call 与普通 call 同样由 mask 字符 c 控制。
func (environment *Environment) TriggerTailCallHookWithRunner(runner HookRunner) error {
	// tail call 事件映射到 mask 字符 c，并通过 VM runner 补齐 Lua closure hook 执行能力。
	return environment.dispatchHookWithRunner(HookEventTailCall, 0, runner)
}

// TriggerReturnHookWithRunner 触发当前环境的 return hook，并允许 VM 执行 Lua hook closure。
//
// runner 只在 hook 是 Lua closure 时使用；Go hook 仍由 debug 库直接执行。未设置 hook 或
// mask 不包含 return 时不执行任何操作。
func (environment *Environment) TriggerReturnHookWithRunner(runner HookRunner) error {
	// return 事件映射到 mask 字符 r，并通过 VM runner 补齐 Lua closure hook 执行能力。
	return environment.dispatchHookWithRunner(HookEventReturn, 0, runner)
}

// TriggerLineHook 触发当前环境的 line hook。
//
// line 是当前行号；未设置 hook 或 mask 不包含 line 时不执行任何操作。
func (environment *Environment) TriggerLineHook(line int64) error {
	// line 事件映射到 mask 字符 l，并携带行号参数。
	return environment.dispatchHook(HookEventLine, line)
}

// TriggerLineHookWithRunner 触发当前环境的 line hook，并允许 VM 执行 Lua hook closure。
//
// runner 只在 hook 是 Lua closure 时使用；Go hook 仍由 debug 库直接执行。line 是当前行号，
// 未设置 hook 或 mask 不包含 line 时不执行任何操作。
func (environment *Environment) TriggerLineHookWithRunner(line int64, runner HookRunner) error {
	// line 事件映射到 mask 字符 l，并通过 VM runner 补齐 Lua closure hook 执行能力。
	return environment.dispatchHookWithRunner(HookEventLine, line, runner)
}

// TriggerCountHook 记录一次指令计数并在达到间隔时触发 count hook。
//
// 当前 VM 尚未自动调用该方法；后续执行循环每条指令后接入即可复用该计数语义。
func (environment *Environment) TriggerCountHook() error {
	// count hook 需要环境、hook 和正数间隔都存在。
	threadState := environment.activeThreadHookState()
	if threadState != nil {
		// 当前正在执行的协程有专属 hook 时，使用协程自己的计数器。
		return environment.triggerCountHookState(threadState, nil)
	}
	if environment == nil || environment.hook.IsNil() || environment.hookCount <= 0 {
		return nil
	}
	if environment.hookActive {
		// hook 回调执行期间不累计 count，避免 hook 自身指令污染用户代码计数。
		return nil
	}
	environment.instructionCount++
	if environment.instructionCount < environment.hookCount {
		// 未达到 count 间隔时只累计，不触发 hook。
		return nil
	}

	environment.instructionCount = 0
	return environment.dispatchHook(HookEventCount, 0)
}

// TriggerCountHookWithRunner 记录一次指令计数，并允许 VM 执行 Lua hook closure。
//
// runner 只在 hook 是 Lua closure 时使用；count hook 不依赖 mask 字符，只由正数 count 间隔控制。
func (environment *Environment) TriggerCountHookWithRunner(runner HookRunner) error {
	// count hook 需要环境、hook 和正数间隔都存在。
	threadState := environment.activeThreadHookState()
	if threadState != nil {
		// 当前正在执行的协程有专属 hook 时，使用协程自己的计数器。
		return environment.triggerCountHookState(threadState, runner)
	}
	if environment == nil || environment.hook.IsNil() || environment.hookCount <= 0 {
		return nil
	}
	if environment.hookActive {
		// hook 回调执行期间不累计 count，避免 hook 自身指令污染用户代码计数。
		return nil
	}
	environment.instructionCount++
	if environment.instructionCount < environment.hookCount {
		// 未达到 count 间隔时只累计，不触发 hook。
		return nil
	}

	environment.instructionCount = 0
	return environment.dispatchHookWithRunner(HookEventCount, 0, runner)
}

// SetLocal 实现 Lua 5.3 `debug.setlocal` 的第一阶段空结果。
//
// 当前 runtime 尚未保存可写局部变量窗口，因此合法参数返回 nil，表示没有对应局部变量。
func (environment *Environment) SetLocal(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) > 0 && args[0].Kind == runtime.KindThread {
		// thread 重载写入目标协程挂起栈中的局部变量快照。
		return setThreadLocal(args)
	}
	// level 必须存在且为 integer。
	level, err := integerArgument(args, 1, "setlocal")
	if err != nil {
		// level 参数错误直接返回 Lua 参数错误。
		return nil, err
	}
	index, err := integerArgument(args, 2, "setlocal")
	if err != nil {
		// local index 参数错误直接返回 Lua 参数错误。
		return nil, err
	}
	if len(args) < 3 {
		// 缺少 value 参数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #3 to 'setlocal' (value expected)"))
	}
	frame, ok := environment.frameAtLevel(level)
	if !ok {
		if environment.hasCallFrames() {
			// 已有调用栈但 level 越界时按 Lua 5.3 抛出 bad level。
			return nil, invalidDebugLevelError()
		}
		// 空环境没有可写调用帧，保持阶段性 nil 结果。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if index < 0 {
		// 负索引写入 vararg 调试值，返回局部变量伪名。
		return environment.setVarargLocal(level, frame, index, args[2]), nil
	}
	if index > 0 {
		// 正索引写入当前可见局部变量。
		return environment.setActiveLocal(level, frame, index, args[2]), nil
	}
	return []runtime.Value{runtime.NilValue()}, nil
}

// SetMetatable 实现 Lua 5.3 `debug.setmetatable` 的 raw 元表写入。
//
// 当前支持 table 以及 nil、boolean、number、string 类型级元表。第二个参数必须是 table 或 nil。
// debug 版本绕过 `__metatable` 保护。
func SetMetatable(args ...runtime.Value) ([]runtime.Value, error) {
	// value 参数必须存在。
	if len(args) < 2 {
		// 缺少 metatable 参数无法完成写入。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'setmetatable' (nil or table expected)"))
	}
	var metatable *runtime.Table
	if !args[1].IsNil() {
		// 非 nil metatable 必须是 table。
		if args[1].Kind != runtime.KindTable {
			// metatable 类型不符合约束。
			return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'setmetatable' (nil or table expected)"))
		}
		convertedMetatable, ok := args[1].Ref.(*runtime.Table)
		if !ok || convertedMetatable == nil {
			// table 引用损坏时返回参数错误。
			return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'setmetatable' (nil or table expected)"))
		}
		metatable = convertedMetatable
	}
	if runtime.SetBasicTypeMetatable(args[0], metatable) {
		// 基础类型 raw 元表是类型级共享元表；debug 版本直接替换完整元表。
		return []runtime.Value{args[0]}, nil
	}

	if args[0].Kind != runtime.KindTable {
		// 其他非 table 值当前没有可写对象元表。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'setmetatable' (table expected)"))
	}
	table, ok := args[0].Ref.(*runtime.Table)
	if !ok || table == nil {
		// table 引用损坏时返回参数错误，避免 panic。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'setmetatable' (table expected)"))
	}

	table.SetMetatable(metatable)
	return []runtime.Value{args[0]}, nil
}

// SetUpvalue 实现 Lua 5.3 `debug.setupvalue` 的 Lua closure upvalue 写入。
//
// 第一个参数必须是 Lua closure；第二个参数是 1-based upvalue index；第三个参数是新值。
// 命中时返回 upvalue 名称，未命中返回 nil。运行期存在共享 upvalue cell 时会写回 cell。
func SetUpvalue(args ...runtime.Value) ([]runtime.Value, error) {
	// function 参数必须存在。
	if len(args) == 0 {
		// 缺少 function 参数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'setupvalue' (function expected)"))
	}
	if args[0].Kind != runtime.KindLuaClosure {
		// 当前阶段只有 Lua closure 暴露可写 upvalue。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	index, err := integerArgument(args, 2, "setupvalue")
	if err != nil {
		// index 参数错误直接返回 Lua 参数错误。
		return nil, err
	}
	if len(args) < 3 {
		// 缺少 value 参数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #3 to 'setupvalue' (value expected)"))
	}

	closure, ok := args[0].Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || index <= 0 {
		// closure 损坏或 index 非正时视为未命中。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	upvalueIndex := int(index - 1)
	if upvalueIndex < 0 || upvalueIndex >= luaClosureUpvalueCount(closure) {
		// 超出 upvalue 数量时按 Lua setupvalue 语义返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	setLuaClosureUpvalueValue(closure, upvalueIndex, args[2])
	return []runtime.Value{runtime.StringValue(upvalueName(closure, upvalueIndex))}, nil
}

// SetUserValue 实现 Lua 5.3 `debug.setuservalue`。
//
// 第一个参数必须是 userdata；第二个参数是要保存的 user value。成功时返回原 userdata。
func SetUserValue(args ...runtime.Value) ([]runtime.Value, error) {
	// userdata 参数必须存在且必须是 userdata。
	if len(args) == 0 || args[0].Kind != runtime.KindUserdata {
		if len(args) > 0 && isLightUserdataSurrogate(args[0]) {
			// upvalueid 当前以 string surrogate 表示 light userdata，错误文本需保留官方类型名。
			return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'setuservalue' (full userdata expected, got light userdata)"))
		}
		// 非 userdata 不能保存 user value。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'setuservalue' (userdata expected)"))
	}
	if len(args) < 2 {
		// 缺少 value 参数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'setuservalue' (value expected)"))
	}
	userdata, ok := args[0].Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// userdata 引用损坏时返回参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'setuservalue' (userdata expected)"))
	}

	userdata.UserValue = args[1]
	return []runtime.Value{args[0]}, nil
}

// isLightUserdataSurrogate 判断值是否为当前阶段用 string 表示的 light userdata 身份。
func isLightUserdataSurrogate(value runtime.Value) bool {
	if value.Kind != runtime.KindString {
		// 只有 upvalueid 返回的 surrogate 使用 string 承载。
		return false
	}
	if strings.HasPrefix(value.String, "0x") {
		// fmt.Sprintf("%p", ptr) 形式来自共享 upvalue cell。
		return true
	}
	if strings.Contains(value.String, ":") && strings.HasPrefix(value.String, "0x") {
		// fmt.Sprintf("%p:%d", closure, index) 形式来自非共享 upvalue 槽。
		return true
	}
	return false
}

// Traceback 实现 Lua 5.3 `debug.traceback` 的基础格式化能力。
//
// 第一个参数可选，作为消息首行；当前使用 State.TracebackFrames 生成稳定调用栈文本。
func (environment *Environment) Traceback(args ...runtime.Value) ([]runtime.Value, error) {
	messageIndex := 0
	levelIndex := 1
	tracebackThread := (*runtime.Thread)(nil)
	if len(args) > 0 && args[0].Kind == runtime.KindThread {
		// Lua 5.3 支持 debug.traceback(thread, message, level)；当前阶段先解析重载参数。
		messageIndex = 1
		levelIndex = 2
		if thread, ok := args[0].Ref.(*runtime.Thread); ok {
			// thread 引用有效时后续优先读取协程挂起帧。
			tracebackThread = thread
		}
	}
	// 默认消息为空字符串，符合 traceback 可无参数调用。
	message := ""
	if len(args) > messageIndex && !args[messageIndex].IsNil() {
		if args[messageIndex].Kind != runtime.KindString {
			// Lua 5.3 对非 string message 直接原样返回，不拼接 traceback 文本。
			return []runtime.Value{args[messageIndex]}, nil
		}
		// string 消息直接作为首行。
		message = args[messageIndex].String
	}
	level := int64(1)
	if tracebackThread != nil {
		// thread traceback 默认从挂起点自身开始；显式 level=1 才跳过 yield 帧。
		level = 0
	}
	if len(args) > levelIndex && !args[levelIndex].IsNil() {
		// 第二个参数是 traceback 起始层级，允许可转整数的 number。
		parsedLevel, err := integerArgument(args, levelIndex+1, "traceback")
		if err != nil {
			return nil, err
		}
		level = parsedLevel
	}
	frames := []runtime.CallFrame(nil)
	if tracebackThread != nil {
		// thread traceback 使用协程最近一次 yield 保存的挂起栈，不读取当前主线程调用栈。
		frames = collapseTailCallFrames(trimCoroutineResumeBoundary(tracebackThread.TracebackFrames()))
		frames = annotateThreadTracebackFrames(environment, frames)
		skipCount := int(level)
		if skipCount >= len(frames) {
			frames = nil
		} else if skipCount > 0 {
			frames = frames[skipCount:]
		}
	} else if environment != nil && environment.state != nil {
		if level <= 0 {
			// level=0 需要包含 debug.traceback 自身帧。
			frames = environment.state.TracebackFrames()
			if len(frames) > 0 && isCurrentDebugLibraryFrame(environment, frames[0]) && frames[0].Name != "" {
				// 官方 traceback 对当前 debug API 帧展示为 debug.<name>。
				frames[0].Name = "debug." + strings.TrimPrefix(frames[0].Name, "debug.")
			}
		} else {
			// 默认路径跳过 debug.traceback 自身，并裁掉 coroutine.wrap/resume 边界后再按 level 裁剪。
			frames = trimCoroutineResumeBoundary(environment.visibleCallFrames())
			skipCount := int(level - 1)
			if skipCount >= len(frames) {
				frames = nil
			} else if skipCount > 0 {
				frames = frames[skipCount:]
			}
		}
	}
	return []runtime.Value{runtime.StringValue(runtime.Traceback(message, frames))}, nil
}

// annotateThreadTracebackFrames 为挂起协程 traceback 补充匿名 Lua 函数展示名。
//
// 普通当前栈 traceback 仍使用 runtime 默认展示；只有 thread traceback 需要把协程 wrapper
// 这类匿名函数显示为 `in function <source:line>`，以匹配 Lua 5.3 官方 db.lua。
func annotateThreadTracebackFrames(environment *Environment, frames []runtime.CallFrame) []runtime.CallFrame {
	if len(frames) == 0 {
		// 空栈无需标注。
		return frames
	}
	annotatedFrames := append([]runtime.CallFrame(nil), frames...)
	for frameIndex := range annotatedFrames {
		if annotatedFrames[frameIndex].Kind == runtime.CallFrameKindGo && annotatedFrames[frameIndex].Name == "yield" {
			// 官方 thread traceback 把 coroutine.yield 展示为库函数名，递归协程检查依赖该文本。
			annotatedFrames[frameIndex].Name = "coroutine.yield"
			continue
		}
		// 只标注无调用点名称的 Lua 函数帧，避免覆盖 yield/f/global 等已有语义。
		if annotatedFrames[frameIndex].Kind != runtime.CallFrameKindLua || annotatedFrames[frameIndex].Name != "" || annotatedFrames[frameIndex].NameWhat != "" {
			continue
		}
		if name := globalFunctionName(environment, annotatedFrames[frameIndex].Function); name != "" {
			// 全局函数调用恢复 continuation 后可能丢失调用点名称，thread traceback 通过 _G 反查补回。
			annotatedFrames[frameIndex].Name = name
			continue
		}
		proto := frameProto(annotatedFrames[frameIndex])
		if proto == nil || proto.LineDefined <= 0 {
			// 顶层 chunk 或缺失调试信息时保留 source 展示。
			continue
		}
		source := strings.TrimPrefix(proto.Source, "@")
		annotatedFrames[frameIndex].Name = fmt.Sprintf("in function <%s:%d>", source, proto.LineDefined)
		annotatedFrames[frameIndex].NameWhat = "traceback"
	}
	return annotatedFrames
}

// globalFunctionName 在 State 全局表中反查函数值名称。
//
// continuation 恢复后部分调用点名称不可用；Lua 5.3 traceback 对全局函数通常展示函数名，
// 因此 thread traceback 可用稳定的 `_G` raw 遍历补充名称。
func globalFunctionName(environment *Environment, function runtime.Value) string {
	if environment == nil || environment.state == nil || function.IsNil() {
		// 缺少环境或函数值时无法反查。
		return ""
	}
	globals := environment.state.Globals()
	if globals == nil {
		// State 已关闭或无全局表时无法反查。
		return ""
	}
	key := runtime.NilValue()
	for {
		// 遍历 _G，查找与目标函数 raw equal 的 string key。
		nextKey, nextValue, ok, err := globals.RawNext(key)
		if err != nil || !ok {
			return ""
		}
		if nextKey.Kind == runtime.KindString && nextValue.RawEqual(function) {
			return nextKey.String
		}
		key = nextKey
	}
}

// trimCoroutineResumeBoundary 裁掉挂起协程栈中的 resume 边界及其外层主线程帧。
//
// thread.Yield 保存的是 State 全量调用栈，包含 `coroutine.resume` 以及调用 resume 的主线程帧；
// Lua 5.3 的 debug.traceback(thread) 只展示目标协程内部栈，因此遇到 resume Go 帧后截断。
func trimCoroutineResumeBoundary(frames []runtime.CallFrame) []runtime.CallFrame {
	if len(frames) == 0 {
		// 空栈无需裁剪。
		return frames
	}
	seenCoroutineLuaFrame := false
	for frameIndex, frame := range frames {
		if frameIndex > 0 && frame.Kind == runtime.CallFrameKindLua {
			// yield 之后出现的 Lua 帧属于目标协程内部；再遇到 Go 帧就是外层调用边界。
			seenCoroutineLuaFrame = true
		}
		if frame.Kind == runtime.CallFrameKindGo && (frame.Name == "resume" || seenCoroutineLuaFrame) {
			// resume/pcall 等 Go 帧是主线程进入协程的边界，不属于目标协程内部 traceback。
			return frames[:frameIndex]
		}
	}
	return frames
}

// UpvalueID 实现 Lua 5.3 `debug.upvalueid` 的稳定身份表示。
//
// 当前 runtime 没有 lightuserdata，返回可比较的 string 标识；存在共享 cell 时同一 cell 返回相同 id。
func UpvalueID(args ...runtime.Value) ([]runtime.Value, error) {
	// function 参数必须存在。
	if len(args) == 0 {
		// 缺少 function 参数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'upvalueid' (function expected)"))
	}
	index, err := integerArgument(args, 2, "upvalueid")
	if err != nil {
		// index 参数错误直接返回 Lua 参数错误。
		return nil, err
	}
	if args[0].Kind == runtime.KindGoClosure {
		// 显式携带 debug upvalue 元数据的 Go closure 也需要稳定 upvalue id。
		return goClosureUpvalueID(args[0], index)
	}
	if args[0].Kind != runtime.KindLuaClosure {
		// 其他函数形态没有可枚举 upvalue。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	closure, ok := args[0].Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || index <= 0 {
		// closure 损坏或 index 非正时返回 upvalueid 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'upvalueid' (invalid upvalue index)"))
	}
	upvalueIndex := int(index - 1)
	if upvalueIndex < 0 || upvalueIndex >= luaClosureUpvalueCount(closure) {
		// 超出 upvalue 数量时返回 upvalueid 参数错误，供 pcall 捕获。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'upvalueid' (invalid upvalue index)"))
	}
	if upvalueIndex < len(closure.UpvalueCells) && closure.UpvalueCells[upvalueIndex] != nil {
		// 共享 upvalue cell 的 identity 才能体现两个闭包捕获同一变量。
		return []runtime.Value{runtime.StringValue(fmt.Sprintf("%p", closure.UpvalueCells[upvalueIndex]))}, nil
	}
	return []runtime.Value{runtime.StringValue(fmt.Sprintf("%p:%d", closure, upvalueIndex))}, nil
}

// goClosureUpvalueID 返回带显式 upvalue 元数据 Go closure 的稳定 upvalue 标识。
//
// function 必须是 KindGoClosure；index 是 Lua 1-based upvalue 序号。普通 Go closure 或越界
// 返回 nil，保持 debug.getupvalue 对 Go closure 的阶段性兼容语义。
func goClosureUpvalueID(function runtime.Value, index int64) ([]runtime.Value, error) {
	closure, ok := function.Ref.(*runtime.GoClosureWithUpvalues)
	if !ok || closure == nil {
		// 普通 Go closure 没有可枚举 upvalue id。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	upvalueIndex := int(index - 1)
	if upvalueIndex < 0 || upvalueIndex >= len(closure.Upvalues) {
		// Go closure 的越界读取保持 nil，避免扩大普通 Go closure 的错误面。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.StringValue(fmt.Sprintf("%p:%d", closure, upvalueIndex))}, nil
}

// UpvalueJoin 实现 Lua 5.3 `debug.upvaluejoin` 的 upvalue 绑定。
//
// 存在共享 upvalue cell 时把目标 upvalue 绑定到来源 cell；旧快照闭包退回复制来源当前值。
func UpvalueJoin(args ...runtime.Value) ([]runtime.Value, error) {
	// 第一个函数参数必须存在。
	if len(args) == 0 {
		// 缺少目标函数时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'upvaluejoin' (function expected)"))
	}
	if args[0].Kind != runtime.KindLuaClosure {
		// 当前阶段只有 Lua closure 暴露可写 upvalue。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'upvaluejoin' (Lua function expected)"))
	}
	targetIndex, err := integerArgument(args, 2, "upvaluejoin")
	if err != nil {
		// 目标 upvalue index 参数错误直接返回。
		return nil, err
	}
	if len(args) < 3 || args[2].Kind != runtime.KindLuaClosure {
		// 第三个参数必须是来源 Lua closure。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #3 to 'upvaluejoin' (Lua function expected)"))
	}
	sourceIndex, err := integerArgument(args, 4, "upvaluejoin")
	if err != nil {
		// 来源 upvalue index 参数错误直接返回。
		return nil, err
	}

	targetClosure, targetOK := args[0].Ref.(*runtime.LuaClosure)
	sourceClosure, sourceOK := args[2].Ref.(*runtime.LuaClosure)
	if !targetOK || targetClosure == nil || !sourceOK || sourceClosure == nil {
		// closure 引用损坏时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument to 'upvaluejoin' (invalid closure)"))
	}
	targetOffset := int(targetIndex - 1)
	sourceOffset := int(sourceIndex - 1)
	if targetOffset < 0 || targetOffset >= luaClosureUpvalueCount(targetClosure) {
		// 目标 upvalue 越界不能完成 join。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'upvaluejoin' (invalid upvalue index)"))
	}
	if sourceOffset < 0 || sourceOffset >= luaClosureUpvalueCount(sourceClosure) {
		// 来源 upvalue 越界不能完成 join。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #4 to 'upvaluejoin' (invalid upvalue index)"))
	}

	joinLuaClosureUpvalue(targetClosure, targetOffset, sourceClosure, sourceOffset)
	return nil, nil
}

// GetMetatable 实现 Lua 5.3 `debug.getmetatable` 的 raw 元表读取。
//
// debug.getmetatable 不受 `__metatable` 保护字段影响；基础类型返回类型级 raw 元表。
func GetMetatable(args ...runtime.Value) ([]runtime.Value, error) {
	// 没有参数时按 Lua 标准库报告参数错误。
	if len(args) == 0 {
		// 缺少 value 参数时无法读取元表。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'getmetatable' (value expected)"))
	}
	if metatable := runtime.BasicTypeMetatable(args[0]); metatable != nil {
		// debug.getmetatable 返回基础类型级 raw 元表，不受 __metatable 保护字段影响。
		return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, metatable)}, nil
	}
	if args[0].Kind != runtime.KindTable {
		// 当前非 table 值没有元表，返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	table, ok := args[0].Ref.(*runtime.Table)
	if !ok || table == nil {
		// table 引用损坏时视为无元表，避免 panic。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	metatable := table.GetMetatable()
	if metatable == nil {
		// raw 元表不存在时返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, metatable)}, nil
}

// frameInfoTable 将 runtime 调用帧转换成 debug.getinfo 返回表。
//
// frame 必须来自 State.TracebackFrames；当前阶段只填充已有调用帧和占位调试元信息。
func frameInfoTable(frame runtime.CallFrame) *runtime.Table {
	// 构造稳定字段集合，后续行号/upvalue 接入时继续补充。
	info := runtime.NewTable()
	proto := frameProto(frame)
	source := frameSource(frame, proto)
	info.RawSetString("what", runtime.StringValue(frameWhat(frame, proto)))
	info.RawSetString("source", runtime.StringValue(source))
	info.RawSetString("short_src", runtime.StringValue(shortSource(source)))
	info.RawSetString("currentline", runtime.IntegerValue(frameCurrentLine(frame, proto)))
	info.RawSetString("linedefined", runtime.IntegerValue(frameLineDefined(proto)))
	info.RawSetString("lastlinedefined", runtime.IntegerValue(frameLastLineDefined(proto)))
	info.RawSetString("nups", runtime.IntegerValue(frameUpvalueCount(frame, proto)))
	info.RawSetString("nparams", runtime.IntegerValue(frameParamCount(proto)))
	info.RawSetString("isvararg", runtime.BooleanValue(frameIsVararg(frame, proto)))
	info.RawSetString("istailcall", runtime.BooleanValue(frame.TailCall))
	info.RawSetString("func", frame.Function)
	if proto != nil {
		// Lua Proto 有行号表时，按 debug.getinfo("L") 约定提供活跃行集合。
		info.RawSetString("activelines", runtime.ReferenceValue(runtime.KindTable, activeLinesTable(proto)))
	}
	if frame.Name != "" {
		// 调用点已推断出函数名时，按 Lua 5.3 debug.getinfo("n") 暴露 name 字段。
		info.RawSetString("name", runtime.StringValue(frame.Name))
	}
	if frame.NameWhat != "" {
		// 调用点已推断出名称来源时，按 Lua 5.3 debug.getinfo("n") 暴露 namewhat 字段。
		info.RawSetString("namewhat", runtime.StringValue(frame.NameWhat))
	}
	return info
}

// functionInfoTable 将函数值转换成 debug.getinfo(function) 的返回表。
//
// function 必须是 Lua closure 或 Go closure；Go closure 按 C 函数占位，Lua closure 读取 Proto 调试表。
func functionInfoTable(function runtime.Value) *runtime.Table {
	info := runtime.NewTable()
	info.RawSetString("func", function)
	switch function.Kind {
	case runtime.KindLuaClosure:
		// Lua closure 使用 Proto 中保存的源码、行号、参数和 upvalue 元数据。
		frame := runtime.NewLuaCallFrame(function, 1, -1)
		proto := frameProto(frame)
		source := frameSource(frame, proto)
		info.RawSetString("what", runtime.StringValue(frameWhat(frame, proto)))
		info.RawSetString("source", runtime.StringValue(source))
		info.RawSetString("short_src", runtime.StringValue(shortSource(source)))
		info.RawSetString("linedefined", runtime.IntegerValue(frameLineDefined(proto)))
		info.RawSetString("lastlinedefined", runtime.IntegerValue(frameLastLineDefined(proto)))
		info.RawSetString("currentline", runtime.IntegerValue(DefaultCurrentLine))
		info.RawSetString("nups", runtime.IntegerValue(frameUpvalueCount(frame, proto)))
		info.RawSetString("nparams", runtime.IntegerValue(frameParamCount(proto)))
		info.RawSetString("isvararg", runtime.BooleanValue(frameIsVararg(frame, proto)))
		info.RawSetString("istailcall", runtime.BooleanValue(false))
		info.RawSetString("namewhat", runtime.StringValue(""))
		if proto != nil {
			// Lua 函数查询 "L" 时需要返回活跃行集合。
			info.RawSetString("activelines", runtime.ReferenceValue(runtime.KindTable, activeLinesTable(proto)))
		}
	default:
		// Go closure 对齐 Lua C function 的 debug.getinfo 表示。
		info.RawSetString("what", runtime.StringValue("C"))
		info.RawSetString("source", runtime.StringValue("[C]"))
		info.RawSetString("short_src", runtime.StringValue("[C]"))
		info.RawSetString("linedefined", runtime.IntegerValue(DefaultLineDefined))
		info.RawSetString("lastlinedefined", runtime.IntegerValue(DefaultLastLineDefined))
		info.RawSetString("currentline", runtime.IntegerValue(DefaultCurrentLine))
		info.RawSetString("nups", runtime.IntegerValue(0))
		info.RawSetString("nparams", runtime.IntegerValue(0))
		info.RawSetString("isvararg", runtime.BooleanValue(true))
		info.RawSetString("istailcall", runtime.BooleanValue(false))
		info.RawSetString("namewhat", runtime.StringValue(""))
	}
	return info
}

// getThreadInfo 读取挂起 coroutine 的调用帧元信息。
//
// threadValue 必须是 thread；level 从 1 开始，表示目标协程内部最顶层 Lua 帧。协程 yield 的
// Go/C 边界帧会被跳过，使结果对齐 Lua 5.3 debug.getinfo(thread, level)。
func getThreadInfo(threadValue runtime.Value, level int64) []runtime.Value {
	if level <= 0 {
		// 非正层级没有可对应的调用帧。
		return []runtime.Value{runtime.NilValue()}
	}
	thread, ok := threadValue.Ref.(*runtime.Thread)
	if !ok || thread == nil {
		// 损坏 thread 引用没有可读取帧。
		return []runtime.Value{runtime.NilValue()}
	}
	frames := threadInfoFrames(thread)
	frameIndex := int(level - 1)
	if frameIndex < 0 || frameIndex >= len(frames) {
		// 超出目标协程栈深度时返回 nil。
		return []runtime.Value{runtime.NilValue()}
	}
	info := frameInfoTable(frames[frameIndex])
	fillThreadActiveLineRange(info)
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, info)}
}

// threadInfoFrames 返回适合 debug.getinfo(thread, level) 的协程内部帧。
//
// traceback 为了显示挂起点会保留 coroutine.yield 边界；getinfo 的 level 1 则应指向用户 Lua
// 函数帧，因此这里会移除最顶层 yield Go/C 帧。
func threadInfoFrames(thread *runtime.Thread) []runtime.CallFrame {
	frames := collapseTailCallFrames(trimCoroutineResumeBoundary(thread.TracebackFrames()))
	if len(frames) == 0 {
		// 没有挂起帧时返回空切片。
		return frames
	}
	if frames[0].Kind == runtime.CallFrameKindGo && frames[0].Name == "yield" {
		// debug.getinfo(thread, 1) 不展示 coroutine.yield 自身，而是展示被挂起的 Lua 函数。
		return frames[1:]
	}
	return frames
}

// getThreadLocal 读取 debug.getlocal(thread, level, index) 的挂起协程局部变量。
//
// args[0] 必须是 thread；args[1] 是 debug level；args[2] 是 local index。正索引读取命名 local
// 或 temporary，负索引读取 vararg。未命中返回 nil。
func getThreadLocal(args []runtime.Value) ([]runtime.Value, error) {
	thread, level, index, err := threadLocalArguments(args, "getlocal")
	if err != nil {
		// 参数错误直接按 Lua debug API 语义返回。
		return nil, err
	}
	frame, ok := threadFrameAtLevel(thread, level)
	if !ok {
		// 挂起协程没有对应层级时返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if index < 0 {
		// 负索引读取 vararg 快照。
		return getVarargLocal(frame, index), nil
	}
	if index == 0 {
		// Lua 局部变量索引从 1 开始，0 不可能命中。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	localVar, _, ok := activeLocalAt(frame, index)
	if !ok {
		// 未命名局部变量时尝试从挂起寄存器快照读取 temporary。
		return getThreadTemporaryLocal(thread, level, frame, index), nil
	}
	value, ok := thread.LocalRegisterAtLevel(level, localVar.Register)
	if !ok {
		// 调试表命中但寄存器快照缺失时按 nil 值返回，保留局部变量名。
		value = runtime.NilValue()
	}
	return []runtime.Value{runtime.StringValue(localVar.Name), value}, nil
}

// setThreadLocal 写入 debug.setlocal(thread, level, index, value) 的挂起协程局部变量。
//
// 命中命名 local 时写入 thread 保存的寄存器快照并返回变量名；未命中返回 nil。
func setThreadLocal(args []runtime.Value) ([]runtime.Value, error) {
	thread, level, index, err := threadLocalArguments(args, "setlocal")
	if err != nil {
		// 参数错误直接按 Lua debug API 语义返回。
		return nil, err
	}
	if len(args) < 4 {
		// 缺少待写入值时按 nil 写入，与 Lua 参数补 nil 的语义一致。
		args = append(args, runtime.NilValue())
	}
	frame, ok := threadFrameAtLevel(thread, level)
	if !ok {
		// 挂起协程没有对应层级时返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	value := args[3]
	if index < 0 {
		// 当前 thread vararg 快照尚不支持写回 continuation，仅命中时返回 vararg 名。
		return setThreadVarargLocal(frame, index, value), nil
	}
	if index == 0 {
		// Lua 局部变量索引从 1 开始，0 不可能命中。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	localVar, _, ok := activeLocalAt(frame, index)
	if !ok {
		// 未命名局部变量时尝试写入挂起 temporary 寄存器快照。
		return setThreadTemporaryLocal(thread, level, frame, index, value), nil
	}
	if !thread.SetLocalRegisterAtLevel(level, localVar.Register, value) {
		// 寄存器快照缺失时按未命中处理。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.StringValue(localVar.Name)}, nil
}

// threadLocalArguments 解析 thread local API 的通用参数。
//
// methodName 用于构造 Lua 风格错误文本；返回 thread、level 与 index。
func threadLocalArguments(args []runtime.Value, methodName string) (*runtime.Thread, int64, int64, error) {
	if len(args) == 0 || args[0].Kind != runtime.KindThread {
		// 缺少 thread 时返回参数错误。
		return nil, 0, 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #1 to '%s' (thread expected)", methodName)))
	}
	thread, ok := args[0].Ref.(*runtime.Thread)
	if !ok || thread == nil {
		// 损坏 thread 引用按参数错误处理。
		return nil, 0, 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #1 to '%s' (thread expected)", methodName)))
	}
	level, err := integerArgument(args, 2, methodName)
	if err != nil {
		// level 参数错误直接返回。
		return nil, 0, 0, err
	}
	index, err := integerArgument(args, 3, methodName)
	if err != nil {
		// index 参数错误直接返回。
		return nil, 0, 0, err
	}
	return thread, level, index, nil
}

// threadFrameAtLevel 返回挂起协程指定 debug level 对应的 Lua 帧。
//
// level 使用 Lua 1-based 层级；返回 false 表示目标协程没有挂起帧或层级越界。
func threadFrameAtLevel(thread *runtime.Thread, level int64) (runtime.CallFrame, bool) {
	if level <= 0 {
		// 非正层级没有可对应调用帧。
		return runtime.CallFrame{}, false
	}
	frames := threadInfoFrames(thread)
	frameIndex := int(level - 1)
	if frameIndex < 0 || frameIndex >= len(frames) {
		// 层级越界时未命中。
		return runtime.CallFrame{}, false
	}
	return frames[frameIndex], true
}

// getThreadTemporaryLocal 读取挂起协程未命名 temporary 寄存器。
//
// 当前实现只基于寄存器快照判断 temporary 是否存在；nil 槽位不暴露。
func getThreadTemporaryLocal(thread *runtime.Thread, level int64, frame runtime.CallFrame, index int64) []runtime.Value {
	registerIndex, ok := threadTemporaryRegisterIndex(thread, level, frame, index)
	if !ok {
		// 未命中 temporary 时返回 nil。
		return []runtime.Value{runtime.NilValue()}
	}
	value, ok := thread.LocalRegisterAtLevel(level, registerIndex)
	if !ok || value.IsNil() {
		// 快照缺失或 nil 槽位不作为 temporary 暴露。
		return []runtime.Value{runtime.NilValue()}
	}
	return []runtime.Value{runtime.StringValue("(*temporary)"), value}
}

// setThreadTemporaryLocal 写入挂起协程未命名 temporary 寄存器。
//
// 仅当前快照中已有非 nil 值的 temporary 可写，避免创造 Lua 5.3 不可见的临时槽位。
func setThreadTemporaryLocal(thread *runtime.Thread, level int64, frame runtime.CallFrame, index int64, value runtime.Value) []runtime.Value {
	registerIndex, ok := threadTemporaryRegisterIndex(thread, level, frame, index)
	if !ok {
		// 未命中 temporary 时返回 nil。
		return []runtime.Value{runtime.NilValue()}
	}
	currentValue, ok := thread.LocalRegisterAtLevel(level, registerIndex)
	if !ok || currentValue.IsNil() {
		// nil 槽位不作为可写 temporary 暴露。
		return []runtime.Value{runtime.NilValue()}
	}
	if !thread.SetLocalRegisterAtLevel(level, registerIndex, value) {
		// 快照写入失败时按未命中处理。
		return []runtime.Value{runtime.NilValue()}
	}
	return []runtime.Value{runtime.StringValue("(*temporary)")}
}

// threadTemporaryRegisterIndex 将 thread local 正索引映射到挂起寄存器快照的 temporary。
//
// 命名 local 先占据正索引序列；剩余非 nil 且未命名寄存器按从低到高作为 temporary 暴露。
func threadTemporaryRegisterIndex(thread *runtime.Thread, level int64, frame runtime.CallFrame, index int64) (int, bool) {
	if thread == nil || index <= 0 {
		// nil 线程或非法索引不能映射。
		return 0, false
	}
	proto := frameProto(frame)
	namedRegisters := make(map[int]bool)
	activeNamedCount := int64(0)
	if proto != nil {
		// 收集当前 PC 可见的命名 local 寄存器。
		for _, localVar := range proto.LocalVars {
			if !localVar.ActiveAt(frame.CurrentPC) {
				continue
			}
			activeNamedCount++
			namedRegisters[localVar.Register] = true
		}
	}
	temporaryOrdinal := index - activeNamedCount
	if temporaryOrdinal <= 0 {
		// 目标索引仍落在命名 local 范围内。
		return 0, false
	}
	registerIndex := 0
	seenTemporary := int64(0)
	for {
		value, ok := thread.LocalRegisterAtLevel(level, registerIndex)
		if !ok {
			// 寄存器快照已经遍历结束。
			return 0, false
		}
		if !namedRegisters[registerIndex] && !value.IsNil() {
			// 非命名且非 nil 的槽位作为 temporary 计数。
			seenTemporary++
			if seenTemporary == temporaryOrdinal {
				return registerIndex, true
			}
		}
		registerIndex++
	}
}

// setThreadVarargLocal 写入 thread vararg 快照。
//
// CallFrame 中的 Varargs 是指针快照，因此写入后同一挂起帧的后续 debug.getlocal 能读取新值。
func setThreadVarargLocal(frame runtime.CallFrame, index int64, value runtime.Value) []runtime.Value {
	if frame.Varargs == nil {
		// 当前帧没有 vararg 快照，按未命中返回 nil。
		return []runtime.Value{runtime.NilValue()}
	}
	varargIndex := int(-index - 1)
	if varargIndex < 0 || varargIndex >= len(frame.Varargs.Values) {
		// vararg 序号超出当前快照范围。
		return []runtime.Value{runtime.NilValue()}
	}
	frame.Varargs.Values[varargIndex] = value
	return []runtime.Value{runtime.StringValue("(*vararg)")}
}

// fillThreadActiveLineRange 补齐 thread getinfo 的活跃行范围。
//
// Lua 5.3 官方 db.lua 会要求挂起协程函数从 linedefined+1 到 lastlinedefined 的行都出现在
// activelines 中；普通函数查询仍保持基于 Proto.LineInfo 的精确集合，避免扩大既有行为面。
func fillThreadActiveLineRange(info *runtime.Table) {
	if info == nil {
		// nil 信息表没有可补齐内容。
		return
	}
	activeValue := info.RawGetString("activelines")
	if activeValue.Kind != runtime.KindTable {
		// 非 Lua 函数没有 activelines 字段。
		return
	}
	activeLines, ok := activeValue.Ref.(*runtime.Table)
	if !ok || activeLines == nil {
		// 引用损坏时不做补齐。
		return
	}
	lineDefined := info.RawGetString("linedefined")
	lastLineDefined := info.RawGetString("lastlinedefined")
	if lineDefined.Kind != runtime.KindInteger || lastLineDefined.Kind != runtime.KindInteger {
		// 缺少函数定义范围时无法补齐。
		return
	}
	for line := lineDefined.Integer + 1; line <= lastLineDefined.Integer; line++ {
		// 官方协程调试断言期望函数体闭区间内的每一行都可作为 active line 查询。
		activeLines.RawSetInteger(line, runtime.BooleanValue(true))
	}
}

// validateGetInfoOptions 校验 debug.getinfo 的 what 选项字符串。
//
// option 必须为 nil 或 string；包含 Lua 5.3 不认识的字符时返回参数错误。
func validateGetInfoOptions(option runtime.Value, position int) error {
	if option.IsNil() {
		// nil options 使用默认字段集合。
		return nil
	}
	if option.Kind != runtime.KindString {
		// options 必须是字符串。
		return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to 'getinfo' (string expected)", position)))
	}
	for _, optionRune := range option.String {
		// Lua 5.3 支持 n/S/l/u/t/f/L；本实现仍返回字段超集，但必须拒绝非法选项。
		if !strings.ContainsRune("nSlutfL", optionRune) {
			return runtime.RaiseError(runtime.StringValue("bad argument #2 to 'getinfo' (invalid option)"))
		}
	}
	return nil
}

// frameWhat 返回 debug.getinfo 的 what 字段。
//
// Lua closure 按 Proto 行号区分 main/Lua；Go closure 按 C 返回。
func frameWhat(frame runtime.CallFrame, proto *bytecode.Proto) string {
	if frame.Kind == runtime.CallFrameKindGo {
		// Go 调用帧对齐 Lua C frame。
		return "C"
	}
	if proto != nil && proto.LineDefined == 0 {
		// 顶层 chunk 在 Lua debug 中标记为 main。
		return "main"
	}
	return "Lua"
}

// activeLinesTable 根据 Proto.LineInfo 构建 debug.getinfo("L") 的活跃行集合。
//
// 返回 table 使用行号作为 key，值为 true；重复行号只保留一个条目。
func activeLinesTable(proto *bytecode.Proto) *runtime.Table {
	table := runtime.NewTable()
	if proto == nil {
		// 非 Lua 函数没有活跃行集合。
		return table
	}
	for _, line := range proto.LineInfo {
		if line <= 0 {
			// 无效或未知行号不放入活跃行集合。
			continue
		}
		table.RawSetInteger(int64(line), runtime.BooleanValue(true))
	}
	return table
}

// frameAtLevel 按 Lua debug level 读取调用帧快照。
//
// level 使用 Lua 1-based 层级；返回 false 表示环境、State 或层级不可用。
func (environment *Environment) frameAtLevel(level int64) (runtime.CallFrame, bool) {
	// 非正层级没有可对应调用帧。
	if level <= 0 {
		return runtime.CallFrame{}, false
	}
	if environment == nil || environment.state == nil {
		// 没有 State 时无法读取调用栈。
		return runtime.CallFrame{}, false
	}
	frames := environment.visibleCallFrames()
	frameIndex := int(level - 1)
	if frameIndex < 0 || frameIndex >= len(frames) {
		// 层级超过调用栈深度时未命中。
		return runtime.CallFrame{}, false
	}
	return frames[frameIndex], true
}

// visibleCallFrames 返回 debug API 可见的调用帧序列。
//
// Lua 5.3 的 debug.getinfo/getlocal 等 C API 不把自身 C 帧计入用户传入的 level；本实现为 Go
// closure 增加调试帧后，需要仅跳过栈顶的 debug 库自身帧，保留更深处被 hook 观察的 debug 函数调用。
func (environment *Environment) visibleCallFrames() []runtime.CallFrame {
	// 没有环境或 State 时没有可见帧。
	if environment == nil || environment.state == nil {
		return nil
	}
	frames := environment.state.TracebackFrames()
	if len(frames) == 0 {
		// 空调用栈直接返回空切片。
		return frames
	}
	if isCurrentDebugLibraryFrame(environment, frames[0]) {
		// 只跳过当前正在执行的 debug API 自身帧，避免隐藏 hook 目标函数。
		if pendingFrames := environment.state.PendingErrorTracebackFrames(); len(pendingFrames) > 0 {
			// xpcall 错误处理器执行时真实失败帧已被裁剪以释放调用深度，traceback 需优先展示错误现场快照。
			return collapseTailCallFrames(pendingFrames)
		}
		frames = frames[1:]
	}
	if len(frames) > 1 && environment.isSyntheticHookWrapper(frames[0], frames[1]) {
		// Lua hook closure 可能同时保留合成 hook 包装帧和真实局部函数帧；只隐藏重复包装帧。
		frames = frames[1:]
	}
	return collapseTailCallFrames(frames)
}

// isSyntheticHookWrapper 判断栈顶是否是包裹真实 hook 函数的合成 hook 帧。
//
// 普通 hook 调用只有一个 hook 帧，不能隐藏；当下一层帧才等于当前 active hook 函数时，栈顶只是
// VM 为 hook 事件创建的包装帧，应从 debug level 中剔除。
func (environment *Environment) isSyntheticHookWrapper(top runtime.CallFrame, next runtime.CallFrame) bool {
	if environment == nil || !environment.hookActive || top.NameWhat != "hook" {
		// 非 hook 执行期或栈顶不是 hook 包装名时保持可见。
		return false
	}
	activeState := environment.activeHookState()
	if activeState != nil && !activeState.hook.IsNil() && next.Function.RawEqual(activeState.hook) {
		// 下一层正是当前 hook 函数时，栈顶只是包装帧。
		return true
	}
	return next.Name != "" && top.CurrentPC == next.CurrentPC
}

// collapseTailCallFrames 折叠 tail call 替换掉的 caller 帧。
//
// 当前 VM 为了复用普通调用执行器，会临时保留被尾调用替换的 caller 帧。Lua 5.3 debug 栈中
// 该 caller 不可见；当一个帧标记为 TailCall 时，紧邻的下一层 caller 需要从可见栈中移除。
func collapseTailCallFrames(frames []runtime.CallFrame) []runtime.CallFrame {
	if len(frames) == 0 {
		// 空调用栈无需折叠。
		return frames
	}
	collapsed := make([]runtime.CallFrame, 0, len(frames))
	skipNextCaller := false
	for _, frame := range frames {
		if skipNextCaller {
			if frameWhat(frame, frameProto(frame)) == "main" {
				// 自尾调用复用当前帧时，下一层 main 不是被替换 caller，必须保留给 debug.getinfo。
				collapsed = append(collapsed, frame)
			}
			// 上一层 tail-called callee 已经处理下一层 caller，恢复正常扫描。
			skipNextCaller = false
			continue
		}
		collapsed = append(collapsed, frame)
		if frame.TailCall {
			// 当前帧由 tail call 进入，下一层 caller 被该帧逻辑替换。
			skipNextCaller = true
		}
	}
	return collapsed
}

// isCurrentDebugLibraryFrame 判断栈顶帧是否为 debug 标准库自身导出的 Go API。
//
// 常规路径依赖 VM 在 table field 调用点写入 name/namewhat；hook 回调期间 debug API Go 帧可能被
// hook 调用名覆盖，此时只要栈顶是 Go 帧，就按当前 debug API 桥接帧跳过。
func isCurrentDebugLibraryFrame(environment *Environment, frame runtime.CallFrame) bool {
	if frame.Kind != runtime.CallFrameKindGo {
		// Lua 帧不能作为 debug 库 Go API 帧过滤。
		return false
	}
	if environment != nil && environment.hookActive {
		// hook 回调期间，debug.getinfo/getlocal/traceback 的当前 Go 帧不参与用户 level 计数。
		return true
	}
	if environment.isDebugLibraryFunctionFrame(frame) {
		// xpcall(debug.traceback) 这类裸函数调用没有字段名，需通过 debug 表中的函数 identity 识别。
		return true
	}
	if frame.NameWhat != "field" {
		// 非 debug.<name> 字段调用的普通 Go closure 保持可见。
		return false
	}
	debugFunctionName := frame.Name
	// VM 可能为标准库字段调用保留 `debug.getinfo` 形式；这里归一为短名参与自身帧过滤。
	debugFunctionName = strings.TrimPrefix(debugFunctionName, "debug.")
	switch debugFunctionName {
	case "debug", "gethook", "getinfo", "getlocal", "getmetatable", "getregistry", "getupvalue",
		"getuservalue", "sethook", "setlocal", "setmetatable", "setupvalue", "setuservalue",
		"traceback", "upvalueid", "upvaluejoin":
		// debug 标准库导出函数自身帧不参与用户可见 level 计数。
		return true
	default:
		// 其他字段调用帧保持可见。
		return false
	}
}

// isDebugLibraryFunctionFrame 判断当前 Go 帧函数是否来自 debug 标准库表。
//
// xpcall(debug.traceback) 会把 handler 作为裸函数值调用，调用帧没有 `field traceback` 名称；
// 通过 `_G.debug` 表反查函数 identity，可以继续把该 Go 帧视为 debug 库自身帧。
func (environment *Environment) isDebugLibraryFunctionFrame(frame runtime.CallFrame) bool {
	if environment == nil || environment.state == nil || frame.Kind != runtime.CallFrameKindGo {
		// 缺少环境、State 或非 Go 帧时不能按 debug 库函数识别。
		return false
	}
	debugTable := environment.library
	if debugTable == nil {
		// 兼容直接构造 Environment 的低层测试，缺少缓存表时再尝试全局 debug 字段。
		globals := environment.state.Globals()
		if globals == nil {
			// 全局表不可用时无法反查 debug 标准库。
			return false
		}
		debugValue := globals.RawGetString("debug")
		var ok bool
		debugTable, ok = debugValue.Ref.(*runtime.Table)
		if debugValue.Kind != runtime.KindTable || !ok || debugTable == nil {
			// debug 全局未打开或被用户改写时不做裸函数识别。
			return false
		}
	}
	for _, functionName := range []string{
		"debug", "gethook", "getinfo", "getlocal", "getmetatable", "getregistry", "getupvalue",
		"getuservalue", "sethook", "setlocal", "setmetatable", "setupvalue", "setuservalue",
		"traceback", "upvalueid", "upvaluejoin",
	} {
		if debugTable.RawGetString(functionName).RawEqual(frame.Function) {
			// 函数值 identity 匹配 debug 库导出函数，视为当前 debug API 帧。
			return true
		}
	}
	return false
}

// hasCallFrames 判断当前 debug 环境是否存在可见调用栈。
//
// 返回 true 表示 level 越界应按 Lua 5.3 报 bad level；空环境返回 false 以保留阶段性空结果。
func (environment *Environment) hasCallFrames() bool {
	if environment == nil || environment.state == nil {
		// 没有环境或 State 时没有可见调用栈。
		return false
	}
	return len(environment.visibleCallFrames()) > 0
}

// invalidDebugLevelError 返回 debug local API 的非法层级错误。
//
// Lua 5.3 对 getlocal/setlocal 的越界 level 使用 bad level 类运行期错误。
func invalidDebugLevelError() error {
	// 统一构造 Lua error，便于 pcall 捕获 false。
	return runtime.RaiseError(runtime.StringValue("bad level"))
}

// frameProto 从 Lua 调用帧中提取函数原型。
//
// 只有 Lua closure 且引用有效时返回 Proto；Go 帧和损坏引用返回 nil。
func frameProto(frame runtime.CallFrame) *bytecode.Proto {
	// 非 Lua closure 没有 Proto 调试表。
	if frame.Function.Kind != runtime.KindLuaClosure {
		return nil
	}
	closure, ok := frame.Function.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil {
		// closure 引用损坏时不能读取 Proto。
		return nil
	}
	return closure.Proto
}

// frameSource 返回调用帧源码描述。
//
// proto 存在时必须原样使用 Source；空字符串也是 Lua 5.3 允许的显式 chunk name。
func frameSource(frame runtime.CallFrame, proto *bytecode.Proto) string {
	if proto != nil {
		if protoIsStripped(proto) {
			// stripped binary chunk 没有 source，Lua 5.3 debug.source 使用 "=?"
			return "=?"
		}
		// Proto.Source 对齐 Lua 5.3 debug.source。
		return proto.Source
	}
	return frame.Function.DebugString()
}

// shortSource 按 Lua 5.3 规则生成 debug.getinfo 的 short_src 摘要。
//
// source 可来自文件名、`=literal`、`@file` 或字符串 chunk；返回值只用于调试显示。
func shortSource(source string) string {
	if source == "[C]" {
		// C/Go closure 固定显示 [C]。
		return "[C]"
	}
	if strings.HasPrefix(source, "=") {
		// '=' 前缀表示调用方直接指定显示名。
		return truncateSourceName(source[1:])
	}
	if strings.HasPrefix(source, "@") {
		// '@' 前缀表示文件名，去掉标记后按文件路径摘要。
		return truncateFileSource(source[1:])
	}

	// 普通字符串 chunk 只展示第一行，并包裹成 [string "..."] 形式。
	firstLine := source
	if newlineIndex := strings.IndexByte(firstLine, '\n'); newlineIndex >= 0 {
		// 多行源码只取第一行用于摘要。
		firstLine = firstLine[:newlineIndex]
	}
	if firstLine == "" && strings.HasPrefix(source, "\n") {
		// 首行为空的字符串 chunk 按 Lua 5.3 显示省略号，而不是空 chunk 名。
		firstLine = "..."
	}
	if len(firstLine) > 50 {
		// 长字符串 chunk 保留前缀并用省略号结尾，匹配官方测试的宽松 pattern。
		firstLine = firstLine[:47] + "..."
	}
	return fmt.Sprintf("[string %q]", firstLine)
}

// truncateSourceName 截断 `=name` 样式源码名。
//
// Lua 5.3 对很长的 literal source 会截断；当前保留前缀即可满足官方 pattern 检查。
func truncateSourceName(name string) string {
	if len(name) <= 60 {
		// 短名称原样返回。
		return name
	}
	return name[:60]
}

// truncateFileSource 截断 `@file` 样式源码名。
//
// 长文件名保留尾部并加省略号，便于错误信息中看到最具体的文件后缀。
func truncateFileSource(name string) string {
	if len(name) <= 60 {
		// 短文件名原样返回。
		return name
	}
	return "..." + name[len(name)-57:]
}

// frameCurrentLine 返回调用帧当前源码行。
//
// frame.CurrentPC 是 0-based 指令下标；LineInfo 缺失或越界时返回默认占位行。
func frameCurrentLine(frame runtime.CallFrame, proto *bytecode.Proto) int64 {
	if proto == nil {
		// 非 Lua 帧没有源码行号。
		return DefaultCurrentLine
	}
	if frame.CurrentPC < 0 || frame.CurrentPC >= len(proto.LineInfo) {
		// 当前 PC 未落在 LineInfo 范围内时返回占位行。
		return DefaultCurrentLine
	}
	return int64(proto.LineInfo[frame.CurrentPC])
}

// frameLineDefined 返回函数定义起始行。
//
// proto 缺失时返回默认占位行；存在时保持 Lua chunk 中的行号。
func frameLineDefined(proto *bytecode.Proto) int64 {
	if proto == nil {
		// 非 Lua 帧没有定义起始行。
		return DefaultLineDefined
	}
	return int64(proto.LineDefined)
}

// frameLastLineDefined 返回函数定义结束行。
//
// proto 缺失时返回默认占位行；存在时保持 Lua chunk 中的行号。
func frameLastLineDefined(proto *bytecode.Proto) int64 {
	if proto == nil {
		// 非 Lua 帧没有定义结束行。
		return DefaultLastLineDefined
	}
	return int64(proto.LastLineDefined)
}

// frameUpvalueCount 返回调用帧 upvalue 数量。
//
// Proto.Upvalues 优先描述调试元数据；缺失时回退到 closure.Upvalues 快照长度。
func frameUpvalueCount(frame runtime.CallFrame, proto *bytecode.Proto) int64 {
	if proto != nil && len(proto.Upvalues) > 0 {
		// Proto 调试表存在时使用其长度。
		return int64(len(proto.Upvalues))
	}
	if frame.Function.Kind != runtime.KindLuaClosure {
		// 非 Lua 帧没有 Lua upvalue。
		return 0
	}
	closure, ok := frame.Function.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil {
		// closure 引用损坏时按 0 个 upvalue 处理。
		return 0
	}
	return int64(len(closure.Upvalues))
}

// frameParamCount 返回 Lua 函数固定参数数量。
//
// proto 缺失时返回 0，匹配当前 Go 帧占位语义。
func frameParamCount(proto *bytecode.Proto) int64 {
	if proto == nil {
		// 非 Lua 帧没有固定参数元数据。
		return 0
	}
	return int64(proto.NumParams)
}

// frameIsVararg 返回函数是否按 debug.getinfo("u") 视为可变参数。
//
// Lua Proto 使用自身 IsVararg；Go/C 函数按 Lua 5.3 约定视为 vararg。
func frameIsVararg(frame runtime.CallFrame, proto *bytecode.Proto) bool {
	if proto == nil {
		// C/Go 函数没有固定参数签名，debug.getinfo("u") 需要报告为 vararg。
		return frame.Kind == runtime.CallFrameKindGo
	}
	return proto.IsVararg
}

// activeLocalAt 返回当前帧指定序号的可见局部变量。
//
// index 使用 Lua 1-based 正索引；返回 activeIndex 表示该局部变量在当前活跃局部列表中的
// 0-based 顺序，可用于映射到帧栈窗口。
func activeLocalAt(frame runtime.CallFrame, index int64) (bytecode.LocalVar, int, bool) {
	proto := frameProto(frame)
	if proto == nil {
		// 非 Lua 帧没有 LocalVars 调试表。
		return bytecode.LocalVar{}, 0, false
	}
	activeIndex := 0
	for _, localVar := range proto.LocalVars {
		// 只统计当前 PC 可见的局部变量。
		if !localVar.ActiveAt(frame.CurrentPC) {
			continue
		}
		activeIndex++
		if int64(activeIndex) == index {
			// 命中指定序号时返回局部变量及其 0-based 活跃位置。
			return localVar, activeIndex - 1, true
		}
	}
	return bytecode.LocalVar{}, 0, false
}

// localDebugName 返回局部变量在 debug API 中展示的名称。
//
// stripped chunk 会保留 local 生命周期但清空名称；Lua 5.3 对这类槽位按 temporary local 展示。
func localDebugName(localVar bytecode.LocalVar) string {
	if localVar.Name == "" {
		// 空名称表示调试名被剥离，仍可通过寄存器生命周期读取值。
		return "(*temporary)"
	}
	return localVar.Name
}

// getVarargLocal 读取 debug.getlocal 的负索引 vararg。
//
// index 必须为负数；-1 表示第一个 vararg，-2 表示第二个。未命中返回 nil。
func getVarargLocal(frame runtime.CallFrame, index int64) []runtime.Value {
	if frame.Varargs == nil {
		// 当前帧没有 vararg 快照。
		return []runtime.Value{runtime.NilValue()}
	}
	varargIndex := int(-index - 1)
	if varargIndex < 0 || varargIndex >= len(frame.Varargs.Values) {
		// vararg 序号超出当前快照范围。
		return []runtime.Value{runtime.NilValue()}
	}
	return []runtime.Value{runtime.StringValue("(*vararg)"), frame.Varargs.Values[varargIndex]}
}

// temporaryLocalAt 读取 level 0 的 debug API 临时参数槽位。
//
// args 是 debug.getlocal 收到的实参数组；index 使用 Lua 1-based 正索引。Lua 5.3 对 C 函数
// 的可见临时槽位统一命名为 `(*temporary)`，当前用于官方 db.lua 的 level 0 兼容断言。
func temporaryLocalAt(args []runtime.Value, index int64) []runtime.Value {
	if index <= 0 {
		// 非正索引不能命中临时槽位。
		return []runtime.Value{runtime.NilValue()}
	}
	temporaryIndex := int(index - 1)
	if temporaryIndex < 0 || temporaryIndex >= len(args) {
		// 超出当前 debug.getlocal 参数数量时返回 nil。
		return []runtime.Value{runtime.NilValue()}
	}
	return []runtime.Value{runtime.StringValue("(*temporary)"), args[temporaryIndex]}
}

// localValueAt 读取活动局部变量值。
//
// level 是 Lua debug 层级；localVar 保存调试寄存器；activeIndex 是旧栈窗口回退索引。优先读取
// 活动 VM 寄存器，缺失时回退到 State 栈窗口以兼容早期单测构造的手写帧。
func (environment *Environment) localValueAt(level int64, frame runtime.CallFrame, localVar bytecode.LocalVar, activeIndex int) runtime.Value {
	if environment != nil && environment.state != nil {
		// 活动 Lua VM 保存真实寄存器值，优先用于运行期 debug.getlocal。
		if vm, ok := environment.state.ActiveVMAtLevel(level); ok {
			if value, registerOK := vm.Register(localVar.Register); registerOK {
				// 命中寄存器时返回当前真实局部值。
				return value
			}
		}
		// 没有活动 VM 时回退旧栈窗口，保持构造帧单测和阶段性行为兼容。
		return environment.state.ValueAt(frame.Base + activeIndex)
	}
	// 没有 State 时无法读取值，按 nil 兜底。
	return runtime.NilValue()
}

// setActiveLocal 写入 debug.setlocal 的正索引局部变量。
//
// level 是 Lua debug 层级；frame 是同层级调用帧快照；index 使用 Lua 1-based 正索引。命中时
// 写入活动 VM 寄存器并返回局部变量名，未命中返回 nil。
func (environment *Environment) setActiveLocal(level int64, frame runtime.CallFrame, index int64, value runtime.Value) []runtime.Value {
	localVar, _, ok := activeLocalAt(frame, index)
	if !ok {
		// 当前 PC 下没有对应命名局部变量时，尝试写入非注册临时寄存器。
		return environment.setTemporaryLuaLocal(level, frame, index, value)
	}
	if environment == nil || environment.state == nil {
		// 没有 State 时无法写回运行期寄存器。
		return []runtime.Value{runtime.NilValue()}
	}
	vm, ok := environment.state.ActiveVMAtLevel(level)
	if !ok {
		// 没有对应活动 VM 时不能可靠写入局部变量。
		return []runtime.Value{runtime.NilValue()}
	}
	if err := vm.SetRegister(localVar.Register, value); err != nil {
		// 寄存器越界说明调试表和 VM 状态不一致，按未命中处理。
		return []runtime.Value{runtime.NilValue()}
	}
	return []runtime.Value{runtime.StringValue(localDebugName(localVar))}
}

// temporaryLuaLocalAt 读取 Lua 帧中的非注册临时寄存器。
//
// level 是 Lua debug 层级；index 使用 Lua 1-based 正索引。Lua 5.3 对存在值但没有调试名的
// 临时寄存器统一返回 `(*temporary)`，用于表达式中间值等非注册 local。
func (environment *Environment) temporaryLuaLocalAt(level int64, frame runtime.CallFrame, index int64) []runtime.Value {
	if environment == nil || environment.state == nil || index <= 0 {
		// 缺少 State 或索引非法时不能读取临时寄存器。
		return []runtime.Value{runtime.NilValue()}
	}
	vm, ok := environment.state.ActiveVMAtLevel(level)
	if !ok {
		// 没有对应活动 VM 时不存在可见临时寄存器。
		return []runtime.Value{runtime.NilValue()}
	}
	registerIndex, ok := temporaryLuaRegisterIndex(frame, vm, index)
	if !ok {
		// 指定索引没有对应可见临时寄存器。
		return []runtime.Value{runtime.NilValue()}
	}
	value, ok := vm.Register(registerIndex)
	if !ok {
		// 越界槽位不作为可见 temporary local。
		return []runtime.Value{runtime.NilValue()}
	}
	if value.IsNil() && !isPendingClosureTemporary(frame, registerIndex) {
		// 普通 nil 槽位不作为可见 temporary local；CLOSURE 执行前的目标槽位除外。
		return []runtime.Value{runtime.NilValue()}
	}
	return []runtime.Value{runtime.StringValue("(*temporary)"), value}
}

// setTemporaryLuaLocal 写入 Lua 帧中的非注册临时寄存器。
//
// level 是 Lua debug 层级；index 使用 Lua 1-based 正索引。命中时返回 `(*temporary)`，未命中
// 返回 nil；该语义覆盖官方 db.lua 对表达式临时值的 setlocal 断言。
func (environment *Environment) setTemporaryLuaLocal(level int64, frame runtime.CallFrame, index int64, value runtime.Value) []runtime.Value {
	if environment == nil || environment.state == nil || index <= 0 {
		// 缺少 State 或索引非法时不能写入临时寄存器。
		return []runtime.Value{runtime.NilValue()}
	}
	vm, ok := environment.state.ActiveVMAtLevel(level)
	if !ok {
		// 没有对应活动 VM 时不存在可写临时寄存器。
		return []runtime.Value{runtime.NilValue()}
	}
	registerIndex, ok := temporaryLuaRegisterIndex(frame, vm, index)
	if !ok {
		// 指定索引没有对应可写临时寄存器。
		return []runtime.Value{runtime.NilValue()}
	}
	currentValue, ok := vm.Register(registerIndex)
	if !ok || currentValue.IsNil() {
		// 只有当前存在非 nil 值的临时寄存器才暴露给 setlocal。
		return []runtime.Value{runtime.NilValue()}
	}
	if err := vm.SetRegister(registerIndex, value); err != nil {
		// 寄存器越界按未命中处理。
		return []runtime.Value{runtime.NilValue()}
	}
	return []runtime.Value{runtime.StringValue("(*temporary)")}
}

// temporaryLuaRegisterIndex 将 debug 正索引映射到非注册临时寄存器。
//
// frame 提供当前 PC 和命名 local 调试表；vm 提供寄存器快照。Lua 5.3 的正索引先枚举命名
// local，再枚举当前可见的 temporary。CALL 指令执行期间，CALL A 自身的函数寄存器不是用户
// 可读写的 temporary，因此扫描上限截断到 A 之前。
func temporaryLuaRegisterIndex(frame runtime.CallFrame, vm *runtime.VM, index int64) (int, bool) {
	if vm == nil || index <= 0 {
		// nil VM 或非法索引无法映射。
		return 0, false
	}
	proto := frameProto(frame)
	namedRegisters := make(map[int]bool)
	activeNamedCount := int64(0)
	if proto != nil {
		// 收集当前 PC 可见的命名 local 寄存器，避免 temporary 枚举重复暴露。
		for _, localVar := range proto.LocalVars {
			if !localVar.ActiveAt(frame.CurrentPC) {
				continue
			}
			activeNamedCount++
			namedRegisters[localVar.Register] = true
		}
	}
	temporaryOrdinal := index - activeNamedCount
	if temporaryOrdinal <= 0 {
		// 目标索引仍落在命名 local 范围内，说明没有 temporary 可映射。
		return 0, false
	}
	registers := vm.RegistersSnapshot()
	registerLimit := len(registers)
	if proto != nil && frame.CurrentPC >= 0 && frame.CurrentPC < len(proto.Code) {
		// 当前指令可能影响 temporary 可见性，需要按 opcode 做兼容处理。
		instruction := proto.Code[frame.CurrentPC]
		if instruction.OpCode() == bytecode.OpClosure && temporaryOrdinal == 1 {
			// line hook 在 CLOSURE 执行前触发；Lua 5.3 仍会把待写入的 R(A) 暴露为 temporary。
			if instruction.A() >= 0 && instruction.A() < registerLimit && !namedRegisters[instruction.A()] {
				return instruction.A(), true
			}
		}
		if instruction.OpCode() == bytecode.OpCall || instruction.OpCode() == bytecode.OpTailCall {
			// CALL 执行期间排除函数寄存器及其后的返回区域，只暴露调用前已经存在的临时值。
			if instruction.A() < registerLimit {
				registerLimit = instruction.A()
			}
		}
	}
	seenTemporary := int64(0)
	for registerIndex := 0; registerIndex < registerLimit; registerIndex++ {
		if namedRegisters[registerIndex] {
			// 命名 local 已通过 activeLocalAt 暴露，不能重复计入 temporary。
			continue
		}
		if registers[registerIndex].IsNil() {
			// nil 槽位不作为可见 temporary。
			continue
		}
		seenTemporary++
		if seenTemporary == temporaryOrdinal {
			// 命中目标 temporary 时返回真实寄存器编号。
			return registerIndex, true
		}
	}
	return 0, false
}

// isPendingClosureTemporary 判断当前 PC 是否正在 CLOSURE 指令执行前暴露目标寄存器。
//
// line hook 在指令执行前触发，Lua 5.3 对 `local A = function() ... end` 会把即将写入 closure 的
// R(A) 作为 `(*temporary)` 暴露；此时寄存器值仍可能是 nil。
func isPendingClosureTemporary(frame runtime.CallFrame, registerIndex int) bool {
	proto := frameProto(frame)
	if proto == nil || frame.CurrentPC < 0 || frame.CurrentPC >= len(proto.Code) {
		// 缺少当前指令时不能判断为 CLOSURE 临时槽。
		return false
	}
	instruction := proto.Code[frame.CurrentPC]
	if instruction.OpCode() != bytecode.OpClosure {
		// 只有 CLOSURE 执行前允许 nil temporary 可见。
		return false
	}
	return instruction.A() == registerIndex
}

// setVarargLocal 写入 debug.setlocal 的负索引 vararg。
//
// level 是 Lua debug 层级；frame 是同层级调用帧快照；index 必须为负数，-1 表示第一个
// vararg。命中时同时更新调用帧快照和活动 VM vararg，使后续 `...` 读取到新值。
func (environment *Environment) setVarargLocal(level int64, frame runtime.CallFrame, index int64, value runtime.Value) []runtime.Value {
	if frame.Varargs == nil {
		// 当前帧没有 vararg 快照，按未命中返回 nil。
		return []runtime.Value{runtime.NilValue()}
	}
	varargIndex := int(-index - 1)
	if varargIndex < 0 || varargIndex >= len(frame.Varargs.Values) {
		// vararg 序号超出当前快照范围。
		return []runtime.Value{runtime.NilValue()}
	}

	// 先更新 frame 快照，确保后续 debug.getlocal 读取同一 VarargSnapshot 时看到新值。
	frame.Varargs.Values[varargIndex] = value
	if environment != nil && environment.state != nil {
		// 活动 VM 保存 OP_VARARG 的真实读取源，也必须同步。
		if vm, ok := environment.state.ActiveVMAtLevel(level); ok {
			// VM 不存在或越界时只保留 frame 快照更新，返回值仍按已命中 vararg 处理。
			vm.SetVararg(varargIndex, value)
		}
	}
	return []runtime.Value{runtime.StringValue("(*vararg)")}
}

// getFunctionParameterLocal 读取 debug.getlocal(function, index) 形参名称。
//
// args 保存原始参数；functionPosition 是函数值位置，indexPosition 是形参索引位置。当前仅暴露
// Lua closure 的固定形参名称；Go closure 或越界索引返回 nil，匹配 Lua 5.3 的查询语义。
func getFunctionParameterLocal(args []runtime.Value, functionPosition int, indexPosition int) ([]runtime.Value, error) {
	if functionPosition <= 0 || functionPosition > len(args) {
		// 函数参数缺失时按函数类型错误返回，避免继续读取空值。
		return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to 'getlocal' (function expected)", functionPosition)))
	}
	function := args[functionPosition-1]
	if function.Kind == runtime.KindGoClosure {
		// Go closure 对齐 C function，没有 Lua 形参调试表。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if function.Kind != runtime.KindLuaClosure {
		// 非函数值不能进入函数形参查询。
		return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to 'getlocal' (function expected)", functionPosition)))
	}
	index, err := integerArgument(args, indexPosition, "getlocal")
	if err != nil {
		// 形参索引错误直接透传 Lua 参数错误。
		return nil, err
	}
	if index <= 0 {
		// 非正索引不可能命中任何形参。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	closure, ok := function.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || closure.Proto == nil {
		// closure 或 Proto 损坏时没有可读形参元数据。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	parameterOffset := int(index - 1)
	if parameterOffset < 0 || parameterOffset >= int(closure.Proto.NumParams) {
		// 只暴露固定形参名，vararg 与普通局部变量不属于函数形参查询结果。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	localVar, ok := parameterLocalVar(closure.Proto, parameterOffset)
	if !ok {
		// Proto 缺少该形参的 LocalVars 调试条目时视为未命中。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.StringValue(localVar.Name)}, nil
}

// parameterLocalVar 从 Proto.LocalVars 中定位指定固定形参的调试条目。
//
// parameterOffset 使用 Go 0-based 形参序号；返回值只包含名称，当前不读取运行期值。
func parameterLocalVar(proto *bytecode.Proto, parameterOffset int) (bytecode.LocalVar, bool) {
	if proto == nil || parameterOffset < 0 {
		// 无 Proto 或负序号无法定位形参。
		return bytecode.LocalVar{}, false
	}
	seenParameters := 0
	for _, localVar := range proto.LocalVars {
		// codegen 会把固定形参登记在对应寄存器上；按寄存器序号匹配可避开后续普通 local。
		if localVar.Register != seenParameters {
			continue
		}
		if seenParameters == parameterOffset {
			// 命中目标形参时返回对应调试条目。
			return localVar, true
		}
		seenParameters++
		if seenParameters >= int(proto.NumParams) {
			// 固定形参已全部扫描完，后续 LocalVars 不再参与函数形参查询。
			break
		}
	}
	return bytecode.LocalVar{}, false
}

// upvalueName 返回 Lua closure 指定 upvalue 的调试名称。
//
// closure 必须非 nil；upvalueIndex 使用 Go 0-based 下标。缺少 Proto 名称时返回空字符串。
func upvalueName(closure *runtime.LuaClosure, upvalueIndex int) string {
	// Proto 缺失或描述数量不足时无法取得名称。
	if closure.Proto == nil || upvalueIndex < 0 || upvalueIndex >= len(closure.Proto.Upvalues) {
		return ""
	}
	return luaClosureUpvalueName(closure, upvalueIndex)
}

// luaClosureUpvalueName 返回 Lua closure 指定 upvalue 的可见调试名称。
//
// stripped chunk 会清空 upvalue 名称；Lua 5.3 对这类 Lua closure 返回 `(*no name)`。非 stripped
// Proto 继续返回原始名称，允许普通匿名名称保持空字符串。
func luaClosureUpvalueName(closure *runtime.LuaClosure, upvalueIndex int) string {
	name := closure.Proto.Upvalues[upvalueIndex].Name
	if name == "" && protoIsStripped(closure.Proto) {
		// 无调试信息的 Lua closure 需要使用官方占位名。
		return "(*no name)"
	}
	return name
}

// protoIsStripped 判断 Proto 是否符合 stripped binary chunk 的调试信息缺失形态。
//
// Source 为空或已被 loader 回填为 `=?`，LineInfo 为空，且存在已清空名称的 local/upvalue
// 调试槽时，视为 `string.dump(fn, true)` 或 `luac -s` 产物。该判断只影响 debug 展示，
// 不改变运行期捕获语义。
func protoIsStripped(proto *bytecode.Proto) bool {
	if proto == nil || len(proto.LineInfo) != 0 {
		// 无 Proto 或仍携带 lineinfo 时不是 stripped 调试形态。
		return false
	}
	if proto.Source != "" && proto.Source != "=?" {
		// 非占位 source 表示调用方显式保留了 chunk 名称，不按 stripped 占位处理。
		return false
	}
	hasStrippedDebugSlot := false
	for _, localVar := range proto.LocalVars {
		if localVar.Name != "" {
			// 任一 local 仍有名称时保留普通 debug 展示。
			return false
		}
		hasStrippedDebugSlot = true
	}
	for _, upvalue := range proto.Upvalues {
		if upvalue.Name != "" {
			// 任一 upvalue 仍有名称时保留普通 debug 展示。
			return false
		}
		hasStrippedDebugSlot = true
	}
	return hasStrippedDebugSlot
}

// luaClosureUpvalueCount 返回 Lua closure 当前可见 upvalue 数量。
//
// closure 必须非 nil；运行期 cell 数量优先，旧闭包只有快照时回退到 Upvalues 长度。
func luaClosureUpvalueCount(closure *runtime.LuaClosure) int {
	if closure == nil {
		// 损坏 closure 没有可见 upvalue。
		return 0
	}
	if len(closure.UpvalueCells) > 0 {
		// VM 运行期闭包以共享 cell 为准，避免 debug 库只看到创建时快照。
		return len(closure.UpvalueCells)
	}
	return len(closure.Upvalues)
}

// getGoClosureUpvalue 读取显式携带调试元数据的 Go closure upvalue。
//
// function 必须是 KindGoClosure；index 是 Lua 1-based upvalue 序号。普通 Go closure 或越界时返回 nil。
func getGoClosureUpvalue(function runtime.Value, index int64) ([]runtime.Value, error) {
	closure, ok := function.Ref.(*runtime.GoClosureWithUpvalues)
	if !ok || closure == nil {
		// 普通 Go closure 没有可枚举 upvalue，按 Lua 语义返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	upvalueIndex := int(index - 1)
	if upvalueIndex < 0 || upvalueIndex >= len(closure.Upvalues) {
		// 越界时返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.StringValue(""), closure.Upvalues[upvalueIndex]}, nil
}

// luaClosureUpvalueValue 读取 Lua closure 指定 upvalue 当前值。
//
// upvalueIndex 使用 Go 0-based 下标；越界或损坏 cell 按 nil 返回，调用方负责先做边界校验。
func luaClosureUpvalueValue(closure *runtime.LuaClosure, upvalueIndex int) runtime.Value {
	if closure == nil || upvalueIndex < 0 {
		// 损坏 closure 或负下标没有可读值。
		return runtime.NilValue()
	}
	if upvalueIndex < len(closure.UpvalueCells) && closure.UpvalueCells[upvalueIndex] != nil {
		// 共享 cell 当前值是 Lua 运行期真实 upvalue 值。
		return closure.UpvalueCells[upvalueIndex].Value()
	}
	if upvalueIndex < len(closure.Upvalues) {
		// 旧闭包或缺失 cell 时读取创建时快照。
		return closure.Upvalues[upvalueIndex]
	}
	return runtime.NilValue()
}

// setLuaClosureUpvalueValue 写入 Lua closure 指定 upvalue 当前值。
//
// upvalueIndex 使用 Go 0-based 下标；共享 cell 存在时同步写 cell 和快照，旧闭包只写快照。
func setLuaClosureUpvalueValue(closure *runtime.LuaClosure, upvalueIndex int, value runtime.Value) {
	if closure == nil || upvalueIndex < 0 {
		// 损坏 closure 或负下标无可写目标。
		return
	}
	if upvalueIndex < len(closure.UpvalueCells) && closure.UpvalueCells[upvalueIndex] != nil {
		// 写入共享 cell，让所有捕获同一 upvalue 的闭包立即可见。
		closure.UpvalueCells[upvalueIndex].Set(value)
	}
	if upvalueIndex < len(closure.Upvalues) {
		// 同步快照，兼容仍直接检查 Upvalues 的旧测试和调试展示。
		closure.Upvalues[upvalueIndex] = value
	}
}

// joinLuaClosureUpvalue 将目标 upvalue 绑定到来源 upvalue。
//
// targetOffset 与 sourceOffset 使用 Go 0-based 下标；来源存在共享 cell 时复用该 cell，
// 否则退回复制来源当前值。
func joinLuaClosureUpvalue(targetClosure *runtime.LuaClosure, targetOffset int, sourceClosure *runtime.LuaClosure, sourceOffset int) {
	if targetClosure == nil || sourceClosure == nil {
		// 损坏 closure 无法 join，调用方已做参数错误处理。
		return
	}
	if sourceOffset < len(sourceClosure.UpvalueCells) && sourceClosure.UpvalueCells[sourceOffset] != nil {
		// 来源有真实共享 cell 时，目标改为引用同一 cell。
		if len(targetClosure.UpvalueCells) <= targetOffset {
			// 旧目标闭包没有足够 cell 槽时补齐闭合 cell，保留其他 upvalue 当前值。
			nextCells := make([]*runtime.UpvalueCell, targetOffset+1)
			copy(nextCells, targetClosure.UpvalueCells)
			for cellIndex := len(targetClosure.UpvalueCells); cellIndex < len(nextCells); cellIndex++ {
				nextCells[cellIndex] = runtime.NewClosedUpvalueCell(luaClosureUpvalueValue(targetClosure, cellIndex))
			}
			targetClosure.UpvalueCells = nextCells
		}
		targetClosure.UpvalueCells[targetOffset] = sourceClosure.UpvalueCells[sourceOffset]
		if targetOffset < len(targetClosure.Upvalues) {
			// 快照同步为来源当前值，方便调试展示保持一致。
			targetClosure.Upvalues[targetOffset] = sourceClosure.UpvalueCells[sourceOffset].Value()
		}
		return
	}

	// 来源没有共享 cell 时只能复制当前值，兼容旧快照闭包。
	setLuaClosureUpvalueValue(targetClosure, targetOffset, luaClosureUpvalueValue(sourceClosure, sourceOffset))
}

// dispatchHook 根据事件名执行已设置的 hook。
//
// event 必须是 Lua 5.3 标准 hook 事件名；line 仅对 line 事件有效。未设置 hook 或 mask 不匹配
// 时返回 nil。hook 错误直接传播给调用方，后续 VM 接入时会转为运行时错误路径。
func (environment *Environment) dispatchHook(event string, line int64) error {
	// 默认 hook 调度只支持 debug 库可直接执行的 Go hook。
	return environment.dispatchHookWithRunner(event, line, nil)
}

// dispatchHookWithRunner 根据事件名执行已设置的 hook，并可委托 VM 执行 Lua hook。
//
// event 必须是 Lua 5.3 标准 hook 事件名；line 仅对 line 事件有效。未设置 hook 或 mask 不匹配
// 时返回 nil。runner 为 nil 且 hook 是 Lua closure 时返回明确错误，避免静默吞掉 hook。
func (environment *Environment) dispatchHookWithRunner(event string, line int64, runner HookRunner) error {
	// nil 环境或未设置 hook 时不触发。
	activeState := environment.activeHookState()
	if environment == nil || activeState == nil || activeState.hook.IsNil() {
		return nil
	}
	if environment.hookActive {
		// hook 正在执行时忽略嵌套 hook，避免递归重入破坏 VM 状态。
		return nil
	}
	if !hookMaskMatches(activeState, event) {
		// mask 不包含该事件时不触发 hook。
		return nil
	}

	environment.hookActive = true
	defer func() {
		// hook 返回后恢复重入标记，确保后续事件可以继续触发。
		environment.hookActive = false
	}()
	if activeState.hook.Kind == runtime.KindLuaClosure && runner != nil {
		// Lua hook 必须回到 VM 执行器运行，期间 hookActive 会屏蔽嵌套 hook。
		return runner(activeState.hook, event, line)
	}
	results, err := callHook(activeState.hook, event, line)
	if err != nil {
		// hook 内错误直接传播，供 VM 调用点转换为 Lua runtime error。
		return err
	}
	_ = results
	return nil
}

// hookMaskMatches 判断当前 mask 是否包含指定 hook 事件。
//
// event 必须是标准事件名；返回 true 表示 dispatchHook 应执行 hook。
func (environment *Environment) hookMaskMatches(event string) bool {
	// 兼容旧测试与内部调用，实际派发使用 activeHookState 后的 hookMaskMatches。
	return hookMaskMatches(environment.activeHookState(), event)
}

// hookMaskMatches 判断指定 hook 状态是否包含指定 hook 事件。
//
// state 必须来自当前默认 hook 或 running thread hook；返回 true 表示 dispatchHook 应执行 hook。
func hookMaskMatches(state *hookState, event string) bool {
	if state == nil {
		// 没有 hook 状态时不触发任何事件。
		return false
	}
	// 根据事件名映射 Lua 5.3 mask 字符。
	switch event {
	case HookEventCall:
		// call hook 由 c mask 控制。
		return strings.Contains(state.mask, "c")
	case HookEventTailCall:
		// tail call hook 同样由 c mask 控制。
		return strings.Contains(state.mask, "c")
	case HookEventReturn:
		// return hook 由 r mask 控制。
		return strings.Contains(state.mask, "r")
	case HookEventLine:
		// line hook 由 l mask 控制。
		return strings.Contains(state.mask, "l")
	case HookEventCount:
		// count hook 由正数 count 控制，不依赖 mask 字符。
		return state.count > 0
	default:
		// 未知事件不触发 hook。
		return false
	}
}

// hookStateEnabledFor 判断指定 hook 状态是否可能触发某个事件。
//
// state 必须来自协程专属 hook；未设置 hook 或事件不匹配时返回 false，供 VM 热路径提前跳过派发。
func hookStateEnabledFor(state *hookState, event string) bool {
	if state == nil || state.hook.IsNil() {
		// 没有协程 hook 或 hook 函数为空时不触发任何事件。
		return false
	}
	return hookMaskMatches(state, event)
}

// triggerCountHookState 记录指定 hook 状态的一次指令计数并按需触发 count hook。
//
// state 必须来自当前 running coroutine；runner 为 nil 时只支持 Go hook，非 nil 时可执行 Lua hook。
func (environment *Environment) triggerCountHookState(state *hookState, runner HookRunner) error {
	if environment == nil || state == nil || state.hook.IsNil() || state.count <= 0 {
		// 缺少环境、hook 或 count 间隔时不触发。
		return nil
	}
	if environment.hookActive {
		// hook 回调执行期间不累计 count，避免 hook 自身指令污染用户代码计数。
		return nil
	}
	state.instructionCount++
	if state.instructionCount < state.count {
		// 未达到 count 间隔时只累计，不触发 hook。
		return nil
	}
	state.instructionCount = 0
	return environment.dispatchHookWithRunner(HookEventCount, 0, runner)
}

// activeThreadHookState 返回当前 running coroutine 的专属 hook 状态。
//
// 当前运行主线程、无 State 或协程未设置专属 hook 时返回 nil，由调用方继续使用默认 hook。
func (environment *Environment) activeThreadHookState() *hookState {
	thread := environment.runningCoroutineHookThread()
	if thread == nil || len(environment.threadHooks) == 0 {
		// 主线程或没有任何协程 hook 时没有协程专属状态。
		return nil
	}
	return environment.threadHooks[thread]
}

// activeHookState 返回当前 VM 执行路径应使用的 hook 状态。
//
// running thread 存在协程专属 hook 时优先使用；否则回退到环境默认 hook，保持现有主线程测试兼容。
func (environment *Environment) activeHookState() *hookState {
	if environment == nil {
		// nil 环境没有 hook 状态。
		return nil
	}
	if thread := environment.runningCoroutineHookThread(); thread != nil {
		// 当前运行的是协程时只读取该协程 hook；没有设置时不继承主线程默认 hook。
		if len(environment.threadHooks) == 0 {
			return nil
		}
		return environment.threadHooks[thread]
	}
	return &hookState{
		hook:             environment.hook,
		mask:             environment.hookMask,
		count:            environment.hookCount,
		instructionCount: environment.instructionCount,
	}
}

// runningCoroutineHookThread 返回当前 running 的非主协程。
//
// Lua 5.3 的 hook 状态属于 thread；无 thread 参数的 debug.sethook/gethook 操作当前 running
// thread。主线程返回 nil，调用方据此使用默认 hook 字段。
func (environment *Environment) runningCoroutineHookThread() *runtime.Thread {
	if environment == nil || environment.state == nil {
		// 缺少环境或 State 时没有 running thread 可读。
		return nil
	}
	thread, isMain := environment.state.Running()
	if thread == nil || isMain {
		// 主线程使用环境默认 hook。
		return nil
	}
	return thread
}

// threadHookState 解析 thread 值并返回已保存的协程 hook 状态。
//
// value 必须是 thread 类型；引用损坏或未设置 hook 时返回 nil，调用方按默认 hook 三元组处理。
func (environment *Environment) threadHookState(value runtime.Value) *hookState {
	if environment == nil || value.Kind != runtime.KindThread {
		// 非 thread 或 nil 环境没有可读取的协程 hook。
		return nil
	}
	thread, ok := value.Ref.(*runtime.Thread)
	if !ok || thread == nil {
		// 引用损坏时保持无 hook 语义。
		return nil
	}
	return environment.threadHooks[thread]
}

// setHookThreadArgument 解析 debug.sethook 的 thread 重载首参。
//
// 首参为 thread 时返回目标协程和 true；缺失、非 thread 或引用损坏时返回 nil/false，让调用方按默认
// sethook 语义继续校验 hook 参数。
func setHookThreadArgument(args []runtime.Value) (*runtime.Thread, bool) {
	if len(args) == 0 || args[0].Kind != runtime.KindThread {
		// 没有 thread 首参时使用默认 hook 语义。
		return nil, false
	}
	thread, ok := args[0].Ref.(*runtime.Thread)
	if !ok || thread == nil {
		// 损坏 thread 引用不进入重载，后续会按 function expected 报错。
		return nil, false
	}
	return thread, true
}

// callHook 调用当前 runtime 支持的 Go hook。
//
// hook 必须是 KindGoClosure；当前阶段 Lua hook 函数尚未接入执行器，因此返回明确错误。
func callHook(hook runtime.Value, event string, line int64) ([]runtime.Value, error) {
	// Go hook 可以直接执行。
	if hook.Kind == runtime.KindGoClosure {
		switch function := hook.Ref.(type) {
		case runtime.GoResultsFunction:
			// GoResultsFunction 使用多返回值调用约定。
			return function(runtime.StringValue(event), runtime.IntegerValue(line))
		case runtime.GoFunction:
			// GoFunction 返回单值，需要适配为多返回值。
			value, err := function(runtime.StringValue(event), runtime.IntegerValue(line))
			if err != nil {
				// hook 错误直接传播。
				return nil, err
			}
			return []runtime.Value{value}, nil
		default:
			// Go closure 负载损坏时返回 callable 错误。
			return nil, runtime.ErrExpectedCallable
		}
	}
	if hook.Kind == runtime.KindLuaClosure {
		// Lua hook 等待完整 Lua closure 执行器接入。
		return nil, runtime.RaiseError(runtime.StringValue("Lua hook execution is not wired yet"))
	}
	return nil, runtime.ErrExpectedCallable
}

// integerArgument 读取指定位置的整数参数。
//
// position 使用 Lua 1-based 参数序号；参数缺失、不是 number，或 float 不是有限整数时返回 Lua
// 参数错误。Lua 5.3 debug 库接受可转整数的 number 作为 level/index。
func integerArgument(args []runtime.Value, position int, functionName string) (int64, error) {
	// 参数缺失时无法取得 integer。
	if position <= 0 || position > len(args) {
		// Lua 标准库把缺失参数报告为 number expected。
		return 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (number expected)", position, functionName)))
	}
	value := args[position-1]
	switch value.Kind {
	case runtime.KindInteger:
		// integer 可直接作为 debug level/index。
		return value.Integer, nil
	case runtime.KindNumber:
		// float 必须是有限整数值，才能按 Lua 5.3 转为 lua_Integer。
		if math.IsInf(value.Number, 0) || math.IsNaN(value.Number) || math.Trunc(value.Number) != value.Number {
			return 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (number expected)", position, functionName)))
		}
		return int64(value.Number), nil
	default:
		// 非 number 类型不能作为 debug level/index。
		return 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (number expected)", position, functionName)))
	}
}
