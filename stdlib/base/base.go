// Package base 实现 Lua 5.3 base 标准库的第一阶段能力。
//
// 本包只依赖 runtime 包，负责把 `_G`、`_VERSION` 和 base 函数注册到 State 的全局表。
package base

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/zing/go-lua-vm/bytecode"
	"github.com/zing/go-lua-vm/compiler/codegen"
	"github.com/zing/go-lua-vm/compiler/lexer"
	"github.com/zing/go-lua-vm/compiler/parser"
	"github.com/zing/go-lua-vm/runtime"
)

const (
	// VersionText 是 Lua 5.3 `_VERSION` 的可见文本。
	VersionText = "Lua 5.3"
)

var (
	// ErrBaseLibraryUnavailable 表示 base 库无法注册到目标 State。
	ErrBaseLibraryUnavailable = errors.New("base library unavailable")
	// ErrDofileExecutionUnsupported 表示 dofile 已读取文件但当前阶段尚未接入脚本执行器。
	ErrDofileExecutionUnsupported = errors.New("dofile execution is not wired yet")
	// ErrLoadFileStdinUnsupported 表示 loadfile 当前阶段不支持从 stdin 读取。
	ErrLoadFileStdinUnsupported = errors.New("loadfile stdin is not supported yet")
	// ErrPCallLuaClosureUnsupported 表示 pcall 当前阶段尚未接入 Lua closure 执行器。
	ErrPCallLuaClosureUnsupported = errors.New("pcall lua closure execution is not wired yet")
)

// Open 将 Lua 5.3 base 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后 `_G` 指向全局表自身，`_VERSION` 为 Lua 5.3 文本，
// 并注册 assert、collectgarbage、dofile、error、getmetatable、ipairs、load、loadfile、
// next、pairs、pcall、print、rawequal、rawget、rawlen、rawset、select、setmetatable、
// tonumber、tostring、type 和 xpcall。错误语义对齐运行时生命周期错误。
func Open(state *runtime.State) error {
	// 注册入口先校验 State 生命周期，避免在关闭后的 root 表上写入函数。
	if state == nil {
		// nil State 没有全局表，调用方需要先创建运行时。
		return fmt.Errorf("%w: %w", ErrBaseLibraryUnavailable, runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("%w: %w", ErrBaseLibraryUnavailable, runtime.ErrClosedState)
	}

	globals := state.Globals()
	if globals == nil {
		// 正常 State 必须带 globals；nil 表示运行时初始化或关闭路径异常。
		return ErrBaseLibraryUnavailable
	}

	// `_G` 按 Lua 5.3 约定指向全局环境表本身。
	globals.RawSetString("_G", runtime.ReferenceValue(runtime.KindTable, globals))
	// `_VERSION` 暴露 Lua 主版本文本。
	globals.RawSetString("_VERSION", runtime.StringValue(VersionText))
	// base 函数以 Go closure 注册，后续 VM CALL 会通过 bridge 调用。
	globals.RawSetString("assert", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Assert)))
	globals.RawSetString("collectgarbage", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// collectgarbage 需要读取当前 State 的 GC root 快照，因此通过闭包捕获 state。
		return CollectGarbage(state, args...)
	})))
	globals.RawSetString("dofile", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoClosureWithUpvalues{
		Function: runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			// dofile 需要读取当前 State 的全局环境并执行编译出的 Lua closure。
			return dofileWithState(state, args...)
		}),
		AllowYield: true,
	}))
	globals.RawSetString("error", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// error 需要当前 State 调用栈来实现 level 参数的 source:line 前缀。
		return errorWithState(state, args...)
	})))
	globals.RawSetString("getmetatable", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(GetMetatable)))
	globals.RawSetString("ipairs", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// ipairs 需要当前 State 才能执行 Lua closure 型 `__ipairs` 元方法。
		return iPairsWithState(state, args...)
	})))
	globals.RawSetString("load", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// load 编译出的顶层 closure 需要绑定当前 State 的 _ENV。
		return loadWithState(state, args...)
	})))
	globals.RawSetString("loadfile", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// loadfile 编译出的顶层 closure 需要绑定当前 State 的 _ENV。
		return loadFileWithState(state, args...)
	})))
	globals.RawSetString("next", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Next)))
	globals.RawSetString("pairs", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// pairs 需要当前 State 才能执行 Lua closure 型 `__pairs` 元方法。
		return pairsWithState(state, args...)
	})))
	globals.RawSetString("pcall", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// pcall 需要当前 State 才能执行 Lua closure。
		return pCallWithState(state, args...)
	})))
	globals.RawSetString("print", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// print 必须通过当前全局 tostring 转换参数，允许测试或用户替换 `_G.tostring`。
		return printWithState(state, os.Stdout, args...)
	})))
	globals.RawSetString("rawequal", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(RawEqual)))
	globals.RawSetString("rawget", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(RawGet)))
	globals.RawSetString("rawlen", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(RawLen)))
	globals.RawSetString("rawset", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(RawSet)))
	globals.RawSetString("select", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Select)))
	globals.RawSetString("setmetatable", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// setmetatable 需要 State 才能登记 table `__gc` 终结队列。
		return setMetatableWithState(state, args...)
	})))
	globals.RawSetString("tonumber", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(ToNumber)))
	globals.RawSetString("tostring", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// tostring 需要当前 State 才能执行 Lua closure `__tostring` 元方法。
		return toStringWithState(state, args...)
	})))
	globals.RawSetString("type", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Type)))
	globals.RawSetString("xpcall", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// xpcall 需要当前 State 才能执行 Lua closure 和 Lua handler。
		return xPCallWithState(state, args...)
	})))
	return nil
}

// Assert 实现 Lua 5.3 `assert` 的基础语义。
//
// 第一个参数为 truthy 时返回全部入参；第一个参数为 nil/false 时抛出 Lua error。
// 未传参数按 nil 处理，默认错误对象为字符串 `assertion failed!`。
func Assert(args ...runtime.Value) ([]runtime.Value, error) {
	// assert 先检查第一个参数的 Lua truthiness。
	if len(args) == 0 {
		// 无参数不是 assert(nil)，官方 Lua 5.3 会报告缺少 value。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'assert' (value expected)"))
	}
	if !args[0].Truthy() {
		// 第一个参数为 nil 或 false 时，第二个参数作为错误对象，否则使用默认文本。
		if len(args) >= 2 {
			// 保留调用方提供的任意 Lua 错误对象。
			return nil, runtime.RaiseError(args[1])
		}
		// 没有自定义错误对象时使用 Lua 常见默认文本。
		return nil, runtime.RaiseError(runtime.StringValue("assertion failed!"))
	}

	// 成功路径返回所有原始参数，符合 Lua assert 的透传语义。
	return append([]runtime.Value(nil), args...), nil
}

// CollectGarbage 实现 Lua 5.3 `collectgarbage` 的第一阶段命令集合。
//
// state 必须非 nil 且未关闭；支持 collect、count、isrunning、stop、restart、step、setpause
// 和 setstepmul。当前 Go GC 不模拟 Lua 增量收集，collect/step 返回兼容占位，
// count 返回可达 root 数量，控制参数仅维护 Lua 侧可观察状态。
func CollectGarbage(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// collectgarbage 需要 State 才能读取当前可达 root 快照。
	if state == nil {
		// nil State 无法执行 GC 命令，返回运行时生命周期错误。
		return nil, runtime.ErrNilState
	}
	if state.IsClosed() {
		// 关闭后没有可扫描 root。
		return nil, runtime.ErrClosedState
	}

	command := "collect"
	if len(args) > 0 {
		// Lua collectgarbage 第一个参数必须是命令字符串。
		if args[0].Kind != runtime.KindString {
			// 非 string 命令无法解析，按 Lua error 语义返回错误对象。
			return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'collectgarbage' (string expected)"))
		}
		command = args[0].String
	}

	switch command {
	case "collect":
		// 完整 GC 当前只更新 Lua 视角计数，不影响宿主 Go GC。
		liveRoots := int64(countSnapshotValues(state.SnapshotGCRoots()))
		if debugHookActive(state) {
			// debug hook 回调内部不能安全重入 table finalizer，先更新可见计数并延后终结器。
			state.FullGCDeferredFinalizers(liveRoots)
			return nil, nil
		}
		if err := state.FullGC(liveRoots); err != nil {
			// table `__gc` 错误需要原样传播给 pcall/调用方。
			return nil, err
		}
		return nil, nil
	case "count":
		// 用 Lua 视角 GC 计数作为阶段性可观测指标，单位沿用 KB 近似。
		return []runtime.Value{runtime.IntegerValue(state.GCCount(int64(countSnapshotValues(state.SnapshotGCRoots()))))}, nil
	case "isrunning":
		// 返回 Lua 视角自动 GC 状态，需受 stop/restart 影响。
		return []runtime.Value{runtime.BooleanValue(state.GCRunning())}, nil
	case "stop":
		// stop 只暂停 Lua 视角自动 GC，不影响宿主 Go GC。
		state.StopGC()
		return nil, nil
	case "restart":
		// restart 恢复 Lua 视角自动 GC，不影响宿主 Go GC。
		state.RestartGC()
		return nil, nil
	case "step":
		// step 可选第二参数是工作量提示，缺省为 0。
		stepSize, err := collectGarbageIntegerArg("step", args, 1, 0)
		if err != nil {
			// 参数错误必须直接返回 Lua error，避免继续执行占位步骤。
			return nil, err
		}
		return []runtime.Value{runtime.BooleanValue(state.RunGCStep(stepSize))}, nil
	case "setpause":
		// setpause 必须写入新 pause 并返回旧 pause。
		pause, err := collectGarbageIntegerArg("setpause", args, 1, 0)
		if err != nil {
			// 参数错误必须直接返回 Lua error，避免污染已保存的 GC 配置。
			return nil, err
		}
		return []runtime.Value{runtime.IntegerValue(state.SetGCPause(pause))}, nil
	case "setstepmul":
		// setstepmul 必须写入新 step multiplier 并返回旧值。
		multiplier, err := collectGarbageIntegerArg("setstepmul", args, 1, 0)
		if err != nil {
			// 参数错误必须直接返回 Lua error，避免污染已保存的 GC 配置。
			return nil, err
		}
		return []runtime.Value{runtime.IntegerValue(state.SetGCStepMultiplier(multiplier))}, nil
	default:
		// 未知命令必须显式报错，避免调用方误以为 Lua GC 指令已完整支持。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'collectgarbage' (invalid option)"))
	}
}

// hookActiveDebugEnvironment 表示 base 库只关心 debug 环境是否正在 hook 回调内。
type hookActiveDebugEnvironment interface {
	// HookActive 返回当前 debug 环境是否正在执行 hook 回调。
	HookActive() bool
}

// debugHookActive 判断当前 State 是否正在执行 debug hook 回调。
//
// state 必须允许访问绑定的 debug 环境；未打开 debug 库或环境类型不匹配时返回 false。
func debugHookActive(state *runtime.State) bool {
	// collectgarbage 的调用方已校验 State，这里保留防御性 nil 边界。
	if state == nil {
		// nil State 没有关联 debug 环境，视为不在 hook 内。
		return false
	}
	environment, ok := state.DebugEnvironment().(hookActiveDebugEnvironment)
	if !ok {
		// 未打开 debug 库或环境不支持 HookActive 时走普通 GC 路径。
		return false
	}
	return environment.HookActive()
}

// collectGarbageIntegerArg 解析 collectgarbage 的整数参数。
//
// command 用于拼接错误上下文；args 必须包含原始 Lua 参数；index 是 0 基下标；defaultValue
// 在参数缺省时返回。当前只接受 integer 和可精确截断的 number，错误返回 Lua 参数错误。
func collectGarbageIntegerArg(command string, args []runtime.Value, index int, defaultValue int64) (int64, error) {
	// 参数缺省时使用调用方指定默认值，匹配 step 的可选工作量语义。
	if index >= len(args) || args[index].Kind == runtime.KindNil {
		// nil 参数也按缺省处理，避免官方测试中的省略形式失败。
		return defaultValue, nil
	}

	value := args[index]
	switch value.Kind {
	case runtime.KindInteger:
		// integer 可直接作为 Lua 5.3 collectgarbage 控制数值。
		return value.Integer, nil
	case runtime.KindNumber:
		// number 只做 int64 截断，当前阶段不区分小数边界。
		return int64(value.Number), nil
	default:
		// 其他类型不是数值参数，返回 Lua error 让 pcall 可捕获。
		message := fmt.Sprintf("bad argument #%d to 'collectgarbage' (number expected for %s)", index+1, command)
		return 0, runtime.RaiseError(runtime.StringValue(message))
	}
}

// Dofile 实现 Lua 5.3 `dofile` 的文件读取边界。
//
// 第一个参数必须是文件名字符串；当前阶段会验证文件可读，但尚未接入 parser/codegen/VM 执行链路，
// 因此读取成功后返回 ErrDofileExecutionUnsupported。读取失败返回原始文件错误。
func Dofile(args ...runtime.Value) ([]runtime.Value, error) {
	// dofile 必须先取得文件名。
	if len(args) == 0 || args[0].Kind == runtime.KindNil {
		// 当前阶段不支持从 stdin 读取 chunk，避免阻塞测试和嵌入调用。
		return nil, runtime.RaiseError(runtime.StringValue("dofile stdin is not supported yet"))
	}
	if args[0].Kind != runtime.KindString {
		// 文件名必须是 string，其他 Lua 值不做隐式转换。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'dofile' (string expected)"))
	}

	_, err := readLoadFileBytes(nil, args[0].String)
	if err != nil {
		// 文件系统错误保留原始 error，便于 CLI 和嵌入调用区分不存在与权限失败。
		return nil, err
	}

	// 文件已可读，但当前尚未串起编译执行链路。
	return nil, ErrDofileExecutionUnsupported
}

// dofileWithState 实现 Lua 5.3 全局 `dofile` 的文件加载与执行语义。
//
// state 来自 Open 注册时捕获的运行时；第一个参数必须是文件名字符串。加载失败会抛 Lua error，
// 加载成功后执行文件 chunk 并把 chunk 的多返回值原样返回给调用方。
func dofileWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// dofile 必须先取得文件名。
	if len(args) == 0 || args[0].Kind == runtime.KindNil {
		// 当前阶段不支持从 stdin 读取 chunk，避免阻塞测试和嵌入调用。
		return nil, runtime.RaiseError(runtime.StringValue("dofile stdin is not supported yet"))
	}
	if args[0].Kind != runtime.KindString {
		// 文件名必须是 string，其他 Lua 值不做隐式转换。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'dofile' (string expected)"))
	}

	loadedResults, err := loadFileWithState(state, args[0])
	if err != nil {
		// loadfile 内部只在 State 生命周期异常时返回 Go error。
		return nil, err
	}
	if len(loadedResults) != 1 || loadedResults[0].Kind != runtime.KindLuaClosure {
		// dofile 对加载错误不返回 nil,error，而是按 Lua 5.3 语义抛出错误。
		if len(loadedResults) >= 2 && loadedResults[1].Kind == runtime.KindString {
			return nil, runtime.RaiseError(loadedResults[1])
		}
		return nil, runtime.RaiseError(runtime.StringValue("dofile failed to load chunk"))
	}

	if state != nil {
		// 有 lua 包注入的完整执行器时，优先走支持 coroutine continuation 的调用路径；这样
		// coroutine.wrap(dofile) 内的文件 chunk 可以按 Lua 5.3 语义 yield 和 resume。
		if results, callErr := state.CallLuaClosure(loadedResults[0]); callErr == nil || !errors.Is(callErr, runtime.ErrExpectedCallable) {
			return results, callErr
		}
	}

	// 没有完整执行器的底层单测环境退回 base 内部执行器，并保留 chunk 自身的多返回值。
	return callProtectedWithState(state, loadedResults[0])
}

// Error 实现 Lua 5.3 `error` 的基础抛错语义。
//
// 第一个参数会作为 Lua error object 原样抛出；未传参数时抛出 nil。该无 State 入口不处理
// level 参数；标准库注册时会使用 errorWithState 实现 Lua 5.3 的 source:line 前缀。
func Error(args ...runtime.Value) ([]runtime.Value, error) {
	// error 默认抛出 nil 对象。
	object := runtime.NilValue()
	if len(args) > 0 {
		// 第一个参数存在时必须原样作为 Lua error object。
		object = args[0]
	}

	// error 从不返回普通结果，直接进入 Lua error 链路。
	return nil, runtime.RaiseError(object)
}

// errorWithState 实现带调用栈 level 的 Lua 5.3 `error`。
//
// state 必须是当前执行 State；第一个参数作为 error object，第二个参数是 level。level=0 不补
// source:line；level>0 时仅对 string 错误对象补目标 Lua 调用帧位置，非 string 对象保持原样。
func errorWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// 先复用基础 error object 规则，确保 error() 与 error(nil) 保留 nil 对象。
	object := runtime.NilValue()
	if len(args) > 0 {
		// 第一个参数存在时必须原样作为 Lua error object。
		object = args[0]
	}

	level := int64(1)
	if len(args) >= 2 {
		// level 必须可转整数；不可转时沿用 base 库 bad argument 规则。
		parsedLevel, ok := args[1].ToInteger()
		if !ok {
			return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'error' (number expected)"))
		}
		level = parsedLevel
	}
	if object.Kind == runtime.KindString && level > 0 {
		// 只有 string error object 会拼接位置前缀，匹配 Lua 5.3 的 error(level) 行为。
		if prefix := errorLevelPrefix(state, int(level)); prefix != "" {
			object = runtime.StringValue(prefix + object.String)
		}
	}

	// error 从不返回普通结果，直接进入 Lua error 链路。
	return nil, runtime.RaiseError(object)
}

// errorLevelPrefix 根据 Lua 调用栈 level 生成 source:line 前缀。
//
// level 按 Lua 5.3 `error` 语义从调用 error 的函数向外计数；当前 Go closure `error` 帧不计入。
// 找不到目标 Lua 帧、目标行未知或 State 不可用时返回空字符串，保持原错误对象。
func errorLevelPrefix(state *runtime.State, level int) string {
	if state == nil || level <= 0 {
		// nil State 或 level=0 不生成前缀。
		return ""
	}
	remaining := level
	for _, frame := range state.TracebackFrames() {
		if frame.Kind != runtime.CallFrameKindLua {
			// Go closure 帧不参与 Lua level 计数。
			continue
		}
		remaining--
		if remaining != 0 {
			// 尚未到达目标 Lua 帧，继续向外层查找。
			continue
		}
		closure, ok := frame.Function.Ref.(*runtime.LuaClosure)
		if !ok || closure == nil || closure.Proto == nil {
			// 目标帧缺少 Proto 时无法可靠生成位置。
			return ""
		}
		prefix := luaRuntimeLocationPrefixForBase(closure.Proto.Source, closure.Proto.LineInfo, frame.CurrentPC)
		return prefix
	}
	return ""
}

// luaRuntimeLocationPrefixForBase 返回 base.error 使用的 source:line 前缀。
//
// source 按 Lua chunk name 规则展示；lineInfo 与 pc 来自目标 Lua 调用帧。缺少有效行号时返回
// 空字符串，避免 error(level) 为未知帧生成误导性 `:-1:`。
func luaRuntimeLocationPrefixForBase(source string, lineInfo []int, pc int) string {
	if source == "" {
		// 空 source 无法生成用户可见位置。
		return ""
	}
	if strings.HasPrefix(source, "=") {
		// `=` chunk name 表示直接使用后续文本。
		source = strings.TrimPrefix(source, "=")
	} else if strings.HasPrefix(source, "@") {
		// 文件 chunk 去掉 @ 前缀。
		source = strings.TrimPrefix(source, "@")
	}
	if source == "" || pc < 0 || pc >= len(lineInfo) || lineInfo[pc] <= 0 {
		// 缺少 source 或行号未知时不补前缀。
		return ""
	}
	return fmt.Sprintf("%s:%d: ", source, lineInfo[pc])
}

// GetMetatable 实现 Lua 5.3 `getmetatable` 的基础语义。
//
// table/userdata 值返回 Lua 可见元表；基础类型返回类型级共享元表；其他值当前返回 nil。
// 受保护 table/userdata 元表通过 `__metatable` 字段隐藏真实元表。
func GetMetatable(args ...runtime.Value) ([]runtime.Value, error) {
	// 缺少参数时按 nil 处理，返回 nil。
	if len(args) == 0 {
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if metatable := runtime.BasicTypeMetatable(args[0]); metatable != nil {
		// 基础类型使用类型级共享元表，debug.setmetatable 会动态替换该表。
		return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, metatable)}, nil
	}
	if args[0].Kind == runtime.KindUserdata {
		// userdata 与 table 一样允许 getmetatable 读取 Lua 可见元表。
		userdata, ok := args[0].Ref.(*runtime.Userdata)
		if !ok || userdata == nil {
			// 损坏 userdata 引用不能泄露内部错误，按无元表处理。
			return []runtime.Value{runtime.NilValue()}, nil
		}
		return []runtime.Value{visibleMetatableValue(userdata.GetMetatable())}, nil
	}
	if args[0].Kind != runtime.KindTable {
		// 没有类型级元表的非 table 类型返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	table, ok := args[0].Ref.(*runtime.Table)
	if !ok || table == nil {
		// 损坏 table 引用不能泄露内部错误，按无元表处理。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	// 返回 Lua 可见元表值，自动处理 `__metatable` 保护。
	return []runtime.Value{table.MetatableValue()}, nil
}

// visibleMetatableValue 返回 userdata 元表对 Lua 可见的值。
//
// metatable 为 nil 时返回 nil；若元表包含非 nil `__metatable` 字段，则返回该保护值，否则返回
// 真实元表 table。该规则与 table 的 MetatableValue 保持一致。
func visibleMetatableValue(metatable *runtime.Table) runtime.Value {
	if metatable == nil {
		// 无元表时 Lua getmetatable 返回 nil。
		return runtime.NilValue()
	}
	protectedValue := metatable.RawGetString("__metatable")
	if !protectedValue.IsNil() {
		// 受保护元表只能暴露保护字段值。
		return protectedValue
	}
	return runtime.ReferenceValue(runtime.KindTable, metatable)
}

// IPairs 实现 Lua 5.3 `ipairs` 的标准库入口。
//
// 第一个参数必须是 table 或带 `__ipairs` 元方法的值；返回 iterator、state、initial 三元组。
func IPairs(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 的兼容入口用于直接 Go 单测；Lua closure 元方法由 iPairsWithState 支持。
	return iPairsWithState(nil, args...)
}

// iPairsWithState 实现 Lua 5.3 `ipairs` 的标准库入口，并支持 Lua closure 元方法。
//
// state 为 nil 时只执行 Go 元方法；非 nil 时可调用 `__ipairs` Lua closure。
func iPairsWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// 缺少 table 参数时返回 Lua 标准 bad argument 错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'ipairs' (table expected)"))
	}

	// runtime.IPairs 已覆盖 `__ipairs` 和 raw integer 前缀迭代语义。
	results, err := runtime.IPairsWithState(state, args[0])
	if err != nil {
		// runtime 层的非 table 错误在 base 入口翻译为用户可见参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'ipairs' (table expected)"))
	}
	return results, nil
}

// Next 实现 Lua 5.3 `next` 的 raw table 迭代入口。
//
// 第一个参数必须是 table；第二个参数是上一次 key，省略时按 nil 起始。迭代结束返回单个 nil。
func Next(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 || args[0].Kind != runtime.KindTable {
		// next 只能接收 table 作为第一个参数。
		return nil, runtime.ErrExpectedTable
	}
	table, ok := args[0].Ref.(*runtime.Table)
	if !ok || table == nil {
		// table 引用损坏时返回明确 table 错误。
		return nil, runtime.ErrExpectedTable
	}

	key := runtime.NilValue()
	if len(args) > 1 {
		// 第二参数存在时作为 RawNext 的继续 key。
		key = args[1]
	}
	nextKey, nextValue, ok, err := table.RawNext(key)
	if err != nil {
		// RawNext 的非法 key 错误需要原样返回。
		return nil, err
	}
	if !ok {
		// 迭代结束返回单 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	// 返回下一项 key/value。
	return []runtime.Value{nextKey, nextValue}, nil
}

// Pairs 实现 Lua 5.3 `pairs` 的标准库入口。
//
// 第一个参数必须是 table 或带 `__pairs` 元方法的值；返回 iterator、state、initial 三元组。
func Pairs(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 的兼容入口用于直接 Go 单测；Lua closure 元方法由 pairsWithState 支持。
	return pairsWithState(nil, args...)
}

// pairsWithState 实现 Lua 5.3 `pairs` 的标准库入口，并支持 Lua closure 元方法。
//
// state 为 nil 时只执行 Go 元方法；非 nil 时可调用 `__pairs` Lua closure。
func pairsWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// 缺少 table 参数时返回 Lua 标准 bad argument 错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'pairs' (table expected)"))
	}

	// runtime.Pairs 已覆盖 `__pairs` 和 raw next 迭代语义。
	results, err := runtime.PairsWithState(state, args[0])
	if err != nil {
		// runtime 层的非 table 错误在 base 入口翻译为用户可见参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'pairs' (table expected)"))
	}
	return results, nil
}

// PCall 实现 Lua 5.3 `pcall` 的第一阶段可恢复调用语义。
//
// 第一个参数必须是 Go closure 或 Lua closure。当前仅执行 Go closure；Lua closure 返回 false
// 和阶段性错误对象，待 VM 调用执行器接入后再扩展。
func PCall(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// 缺少函数时，pcall 返回 false 和可见错误对象。
		return []runtime.Value{runtime.BooleanValue(false), runtime.StringValue(runtime.ErrExpectedCallable.Error())}, nil
	}

	callResults, err := callProtected(args[0], args[1:]...)
	if err != nil {
		// 被调函数错误需要转换为 false/errorObject，而不是继续上抛。
		return []runtime.Value{runtime.BooleanValue(false), runtime.ErrorObject(err)}, nil
	}
	results := []runtime.Value{runtime.BooleanValue(true)}
	results = append(results, callResults...)
	return results, nil
}

// Print 实现 Lua 5.3 `print` 的标准输出入口。
//
// 参数会通过 runtime.ToString 转换后以 tab 分隔并写入 os.Stdout；写入失败返回 Go error。
func Print(args ...runtime.Value) ([]runtime.Value, error) {
	// 默认 print 写入进程标准输出。
	return PrintTo(os.Stdout, args...)
}

// PrintTo 将 Lua 5.3 `print` 输出写入指定 writer。
//
// writer 必须非 nil；参数使用 tostring 语义转换，以 tab 分隔并以换行结束。该 helper 服务测试和 CLI 嵌入。
func PrintTo(writer io.Writer, args ...runtime.Value) ([]runtime.Value, error) {
	if writer == nil {
		// nil writer 无法接收输出，返回普通 Go error 供调用方定位接入问题。
		return nil, errors.New("nil print writer")
	}

	parts := make([]string, 0, len(args))
	for _, arg := range args {
		// print 按 Lua 语义逐个执行 tostring。
		textValue, err := runtime.ToString(arg)
		if err != nil {
			// tostring 失败时 print 整体失败。
			return nil, err
		}
		parts = append(parts, textValue.String)
	}
	if _, err := fmt.Fprintln(writer, strings.Join(parts, "\t")); err != nil {
		// 输出失败需要返回底层写入错误。
		return nil, err
	}
	return nil, nil
}

// printWithState 使用当前 State 的全局 tostring 实现 Lua 5.3 `print`。
//
// state 必须来自 base.Open 注册时捕获的运行时；writer 必须非 nil。该路径和 Lua 5.3 一样
// 每个参数都调用 `_G.tostring`，因此 tostring 缺失、不可调用或返回非 string 都必须让 print 抛错。
func printWithState(state *runtime.State, writer io.Writer, args ...runtime.Value) ([]runtime.Value, error) {
	if writer == nil {
		// nil writer 无法接收输出，返回普通 Go error 供调用方定位接入问题。
		return nil, errors.New("nil print writer")
	}
	if state == nil || state.Globals() == nil {
		// 没有 State 时无法读取全局 tostring，退回明确运行时错误。
		return nil, runtime.ErrNilState
	}

	tostringValue := state.Globals().RawGetString("tostring")
	if tostringValue.Kind != runtime.KindGoClosure && tostringValue.Kind != runtime.KindLuaClosure {
		// Lua 5.3 的 print 直接调用全局 tostring；缺失或非函数时应报告 nil 调用错误。
		return nil, runtime.RaiseError(runtime.StringValue("attempt to call a nil value"))
	}

	parts := make([]string, 0, len(args))
	for _, arg := range args {
		// 每个 print 参数都独立调用当前全局 tostring。
		results, err := callProtectedWithStateNamed(state, tostringValue, "tostring", "global", arg)
		if err != nil {
			// tostring 调用失败时 print 整体失败。
			return nil, err
		}
		if len(results) == 0 || results[0].Kind != runtime.KindString {
			// tostring 必须返回字符串，Lua 官方测试依赖该错误文本。
			return nil, runtime.RaiseError(runtime.StringValue("'tostring' must return a string to 'print'"))
		}
		parts = append(parts, results[0].String)
	}
	if _, err := fmt.Fprintln(writer, strings.Join(parts, "\t")); err != nil {
		// 输出失败需要返回底层写入错误。
		return nil, err
	}
	return nil, nil
}

// RawEqual 实现 Lua 5.3 `rawequal`。
//
// 缺少参数时按 nil 补齐；返回值表示两个参数的 raw equality，不触发任何元方法。
func RawEqual(args ...runtime.Value) ([]runtime.Value, error) {
	// rawequal 的缺省参数按 nil 处理，便于保持总是返回 boolean。
	left := runtime.NilValue()
	if len(args) > 0 {
		// 第一个参数存在时作为左值。
		left = args[0]
	}
	right := runtime.NilValue()
	if len(args) > 1 {
		// 第二个参数存在时作为右值。
		right = args[1]
	}

	// 使用 runtime.Value.RawEqual，确保不触发元方法。
	return []runtime.Value{runtime.BooleanValue(left.RawEqual(right))}, nil
}

// RawGet 实现 Lua 5.3 `rawget`。
//
// 第一个参数必须是 table；第二个参数是 key，省略时按 nil 处理。读取不触发 `__index` 元方法。
func RawGet(args ...runtime.Value) ([]runtime.Value, error) {
	table, err := tableArgument(args, "rawget")
	if err != nil {
		// 第一个参数不是 table 时返回明确错误。
		return nil, err
	}

	key := runtime.NilValue()
	if len(args) > 1 {
		// 第二个参数存在时作为 raw key。
		key = args[1]
	}
	value, err := table.RawGet(key)
	if err != nil {
		// raw key 编码失败时原样返回。
		return nil, err
	}

	// 返回 raw 读取结果，未命中时为 nil。
	return []runtime.Value{value}, nil
}

// RawLen 实现 Lua 5.3 `rawlen`。
//
// 参数为 string 时返回字节长度；参数为 table 时返回基础长度边界；其他类型返回 Lua error。
func RawLen(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// 缺少参数时不能计算长度。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'rawlen' (table or string expected)"))
	}

	switch args[0].Kind {
	case runtime.KindString:
		// Lua string 长度按字节计算。
		return []runtime.Value{runtime.IntegerValue(int64(len(args[0].String)))}, nil
	case runtime.KindTable:
		// table 长度使用 runtime 的 raw table 边界。
		table, ok := args[0].Ref.(*runtime.Table)
		if !ok || table == nil {
			// 损坏 table 引用不能计算长度。
			return nil, runtime.ErrExpectedTable
		}
		return []runtime.Value{runtime.IntegerValue(table.Len())}, nil
	default:
		// 其他类型不支持 rawlen。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'rawlen' (table or string expected)"))
	}
}

// RawSet 实现 Lua 5.3 `rawset`。
//
// 第一个参数必须是 table；第二个参数是 key；第三个参数是 value，省略时按 nil 删除。
// 写入不触发 `__newindex` 元方法，成功返回原 table。
func RawSet(args ...runtime.Value) ([]runtime.Value, error) {
	table, err := tableArgument(args, "rawset")
	if err != nil {
		// 第一个参数不是 table 时返回明确错误。
		return nil, err
	}
	key := runtime.NilValue()
	if len(args) > 1 {
		// 第二个参数存在时作为 raw key。
		key = args[1]
	}
	value := runtime.NilValue()
	if len(args) > 2 {
		// 第三个参数存在时作为写入值。
		value = args[2]
	}
	if err := table.RawSet(key, value); err != nil {
		// nil/NaN key 等 raw set 错误原样返回。
		return nil, err
	}

	// Lua rawset 返回原始 table。
	return []runtime.Value{args[0]}, nil
}

// Select 实现 Lua 5.3 `select`。
//
// 第一个参数为 `#` 时返回剩余参数数量；整数索引时返回从该位置开始的所有参数，支持负数倒数。
func Select(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// select 必须提供选择器。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'select' (number expected)"))
	}
	if args[0].Kind == runtime.KindString && args[0].String == "#" {
		// `#` 选择器返回可变参数数量。
		return []runtime.Value{runtime.IntegerValue(int64(len(args) - 1))}, nil
	}

	index, ok := args[0].ToInteger()
	if !ok {
		// 非整数选择器不能定位返回起点。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'select' (number expected)"))
	}
	count := int64(len(args) - 1)
	if index < 0 {
		// 负数索引从尾部倒数，Lua 语义为 n+i+1。
		index = count + index + 1
	}
	if index <= 0 || index > count+1 {
		// 0 或超出 n+1 的索引无效；n+1 允许返回空列表。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'select' (index out of range)"))
	}

	// 返回从归一化索引开始的参数切片。
	start := int(index)
	return append([]runtime.Value(nil), args[start:]...), nil
}

// SetMetatable 实现 Lua 5.3 `setmetatable`。
//
// 第一个参数必须是 table；第二个参数必须是 table 或 nil。受保护元表返回 ErrProtectedMetatable。
func SetMetatable(args ...runtime.Value) ([]runtime.Value, error) {
	table, err := tableArgument(args, "setmetatable")
	if err != nil {
		// 第一个参数不是 table 时返回明确错误。
		return nil, err
	}
	var metatable *runtime.Table
	if len(args) > 1 && !args[1].IsNil() {
		// 第二参数存在且非 nil 时必须是 table。
		if args[1].Kind != runtime.KindTable {
			return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'setmetatable' (nil or table expected)"))
		}
		convertedMetatable, ok := args[1].Ref.(*runtime.Table)
		if !ok || convertedMetatable == nil {
			// 损坏 table 引用不能作为元表。
			return nil, runtime.ErrExpectedTable
		}
		metatable = convertedMetatable
	}
	if err := table.SetMetatableChecked(metatable); err != nil {
		// 受保护元表错误原样返回，供调用方区分。
		return nil, err
	}

	// Lua setmetatable 返回原始 table。
	return []runtime.Value{args[0]}, nil
}

// setMetatableWithState 执行 setmetatable 并在出现 `__gc` 元方法时登记 table finalizer。
//
// state 可为 nil；nil 时退化为普通 SetMetatable。该函数只服务 base.Open 捕获 State 的全局函数。
func setMetatableWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	results, err := SetMetatable(args...)
	if err != nil {
		// 参数、保护元表或类型错误保持原始语义。
		return nil, err
	}
	if state == nil || len(args) == 0 || args[0].Kind != runtime.KindTable {
		// 没有 State 或 table 参数异常时无法登记 finalizer。
		return results, nil
	}
	if len(args) < 2 || args[1].IsNil() || args[1].Kind != runtime.KindTable {
		// 移除元表或非 table 元表不产生新的 finalizer 登记。
		return results, nil
	}
	metatable, ok := args[1].Ref.(*runtime.Table)
	if !ok || metatable == nil {
		// 损坏元表引用不登记；SetMetatable 已经覆盖正常错误路径。
		return results, nil
	}
	table, ok := args[0].Ref.(*runtime.Table)
	if !ok || table == nil {
		// 损坏 table 引用不登记。
		return results, nil
	}
	state.RegisterWeakTable(table)
	if metatable.RawGetString("__gc").IsNil() {
		// 没有 __gc 字段时不进入终结队列。
		return results, nil
	}

	// Lua 5.3 在设置带 __gc 的元表时登记对象；是否真正执行由 GC 阶段的可达性判断决定。
	state.RegisterTableFinalizer(table)
	return results, nil
}

// ToNumber 实现 Lua 5.3 `tonumber`。
//
// 无 base 时支持 number 原样返回和 string 到 number 转换；有 base 时仅接受 string，base 必须在 2..36。
// 转换失败返回 nil，不抛出错误。
func ToNumber(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// Lua 5.3 的 tonumber 不把缺参视为 nil，必须报告第一个参数缺失。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'tonumber' (value expected)"))
	}
	if len(args) == 1 || args[1].IsNil() {
		// 无 base 时 number 原样返回，string 使用 runtime 的 Lua 数字解析。
		if args[0].IsNumber() {
			return []runtime.Value{args[0]}, nil
		}
		if converted, ok := args[0].StringToNumber(); ok {
			// 字符串成功转换为 integer 或 number。
			return []runtime.Value{converted}, nil
		}
		return []runtime.Value{runtime.NilValue()}, nil
	}

	base, ok := args[1].ToInteger()
	if !ok || base < 2 || base > 36 {
		// base 参数必须是 2 到 36 的整数。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'tonumber' (base out of range)"))
	}
	if args[0].Kind != runtime.KindString {
		// 带 base 的 tonumber 只接受 string。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	integerValue, err := strconv.ParseInt(strings.TrimSpace(args[0].String), int(base), 64)
	if err != nil {
		// 进制解析失败按 Lua 语义返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.IntegerValue(integerValue)}, nil
}

// ToString 实现 Lua 5.3 `tostring`。
//
// 必须提供第一个参数；转换过程复用 runtime.ToString，并保留 `__tostring` 元方法错误。
func ToString(args ...runtime.Value) ([]runtime.Value, error) {
	// 不带 State 的入口保留给单测和纯 Go 元方法路径使用。
	return toStringWithState(nil, args...)
}

// toStringWithState 实现 Lua 5.3 `tostring`，并在可用时执行 Lua closure 元方法。
//
// state 可为 nil；nil 时仅支持基础转换和 Go closure `__tostring` 元方法。必须提供第一个参数；
// 转换过程复用 runtime.ToStringWithState，并保留 `__tostring` 元方法错误。
func toStringWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// Lua 5.3 的 tostring 不把缺参视为 nil，必须报告第一个参数缺失。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'tostring' (value expected)"))
	}
	value := args[0]
	converted, err := runtime.ToStringWithState(state, value)
	if err != nil {
		// tostring 元方法错误需要原样返回。
		return nil, err
	}
	return []runtime.Value{converted}, nil
}

// Type 实现 Lua 5.3 `type`。
//
// 必须提供第一个参数；返回 Lua 基础类型名，Go closure 与 Lua closure 都归类为 function。
func Type(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// Lua 5.3 的 type 不把缺参视为 nil，必须通过 pcall 暴露参数错误。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #1 to 'type' (value expected)"))
	}
	// 第一个参数存在时作为 type 查询目标。
	return []runtime.Value{runtime.StringValue(typeName(args[0]))}, nil
}

// XPCall 实现 Lua 5.3 `xpcall` 的第一阶段可恢复调用语义。
//
// 第一个参数是被调用函数，第二个参数是错误处理函数；当前阶段两者都支持 Go closure。
func XPCall(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) < 2 {
		// 缺少函数或 handler 时，返回 false 和可见错误对象。
		return []runtime.Value{runtime.BooleanValue(false), runtime.StringValue(runtime.ErrExpectedCallable.Error())}, nil
	}

	callResults, err := callProtected(args[0], args[2:]...)
	if err == nil {
		// 成功路径返回 true 后接被调函数返回值。
		results := []runtime.Value{runtime.BooleanValue(true)}
		results = append(results, callResults...)
		return results, nil
	}

	handlerResults, handlerErr := callProtected(args[1], runtime.ErrorObject(err))
	if handlerErr != nil {
		// handler 自身失败时，xpcall 返回 handler 的错误对象。
		return []runtime.Value{runtime.BooleanValue(false), runtime.ErrorObject(handlerErr)}, nil
	}
	if len(handlerResults) == 0 {
		// handler 无返回值时，错误对象按 nil 处理。
		return []runtime.Value{runtime.BooleanValue(false), runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.BooleanValue(false), handlerResults[0]}, nil
}

// pCallWithState 实现绑定 State 的 Lua 5.3 `pcall`。
//
// state 必须是注册 base 库时的运行时；第一个参数可以是 Go closure 或 Lua closure。成功返回
// true 后接返回值；失败返回 false 和 Lua error object。
func pCallWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// 缺少函数时，pcall 返回 false 和可见错误对象。
		return []runtime.Value{runtime.BooleanValue(false), runtime.StringValue(runtime.ErrExpectedCallable.Error())}, nil
	}
	callResults, err := callProtectedWithState(state, args[0], args[1:]...)
	if err != nil {
		// 被调函数错误需要转换为 false/errorObject，而不是继续上抛。
		return []runtime.Value{runtime.BooleanValue(false), runtime.ErrorObject(err)}, nil
	}
	results := []runtime.Value{runtime.BooleanValue(true)}
	results = append(results, callResults...)
	return results, nil
}

// xPCallWithState 实现绑定 State 的 Lua 5.3 `xpcall`。
//
// state 必须是注册 base 库时的运行时；第一个参数是被调用函数，第二个参数是错误处理函数。
func xPCallWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) < 2 {
		// 缺少函数或 handler 时，返回 false 和可见错误对象。
		return []runtime.Value{runtime.BooleanValue(false), runtime.StringValue(runtime.ErrExpectedCallable.Error())}, nil
	}
	callResults, err := callProtectedWithState(state, args[0], args[2:]...)
	if err == nil {
		// 成功路径返回 true 后接被调函数返回值。
		results := []runtime.Value{runtime.BooleanValue(true)}
		results = append(results, callResults...)
		return results, nil
	}
	handlerResults, handlerErr := callProtectedWithState(state, args[1], runtime.ErrorObject(err))
	if handlerErr != nil {
		// handler 自身失败时，xpcall 返回 handler 的错误对象。
		return []runtime.Value{runtime.BooleanValue(false), runtime.ErrorObject(handlerErr)}, nil
	}
	if len(handlerResults) == 0 {
		// handler 无返回值时，错误对象按 nil 处理。
		return []runtime.Value{runtime.BooleanValue(false), runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.BooleanValue(false), handlerResults[0]}, nil
}

// Load 实现 Lua 5.3 `load` 的字符串 chunk 编译入口。
//
// 第一个参数必须是源码字符串；第二个参数可选并作为 chunk source 名称；第三个参数 mode 当前只做
// 类型校验；第四个参数 env 会绑定为源码 chunk 的 `_ENV` upvalue。成功返回 Lua closure，失败返回
// nil 和错误文本，不抛出 Lua error，以对齐 load 的可恢复加载语义。
func Load(args ...runtime.Value) ([]runtime.Value, error) {
	// 直接调用版本没有 State，只返回未绑定宿主全局环境的 closure，主要服务低层单测。
	return loadWithState(nil, args...)
}

// loadWithState 实现 Lua 5.3 `load` 的字符串 chunk 编译入口，并可绑定宿主全局环境。
//
// state 可为 nil；未显式传入 env 且 state 非 nil 时会把顶层 `_ENV` upvalue 绑定到
// state.Globals()，使返回 closure 执行时能访问已注册标准库。加载错误通过 `(nil, message)` 返回，
// 不抛出 Lua error。
func loadWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 || (args[0].Kind != runtime.KindString && args[0].Kind != runtime.KindGoClosure && args[0].Kind != runtime.KindLuaClosure) {
		// load 的第一参数必须是 string chunk 或 reader function。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue("bad argument #1 to 'load' (function or string expected)")}, nil
	}

	chunkBytes, readErr := loadChunkBytesFromArgument(state, args[0])
	if readErr != nil {
		// reader function 错误按 load 语义返回 nil/message。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(baseLuaErrorText(readErr))}, nil
	}

	sourceName := args[0].String
	if len(args) > 1 && args[1].Kind != runtime.KindNil {
		// 第二参数存在且非 nil 时必须是 string，作为 Proto.Source。
		if args[1].Kind != runtime.KindString {
			return []runtime.Value{runtime.NilValue(), runtime.StringValue("bad argument #2 to 'load' (string expected)")}, nil
		}
		sourceName = args[1].String
	}
	mode := "bt"
	if len(args) > 2 && args[2].Kind != runtime.KindNil {
		// 第三参数 mode 必须是 string，并控制 text/binary chunk 接受范围。
		if args[2].Kind != runtime.KindString {
			return []runtime.Value{runtime.NilValue(), runtime.StringValue("bad argument #3 to 'load' (string expected)")}, nil
		}
		mode = args[2].String
	}
	if err := validateLoadMode(chunkBytes, mode); err != nil {
		// mode 不允许当前 chunk 类型时按 load 语义返回 nil/message。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(baseLuaErrorText(err))}, nil
	}

	var envValue *runtime.Value
	if len(args) > 3 {
		// 第四参数 env 按 Lua 5.3 语义原样绑定到第一个 upvalue；nil 也是显式绑定值。
		envValue = &args[3]
	}

	closure, err := loadChunkClosure(chunkBytes, sourceName, state, envValue)
	if err != nil {
		// 编译失败按 load 语义返回 nil 和错误文本，不触发 runtime error。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(loadErrorText(err, sourceName))}, nil
	}

	// 返回可由后续 VM 调用的 Lua closure。
	return []runtime.Value{closure}, nil
}

// loadChunkBytesFromArgument 将 load 的 string 或 reader function 参数转换为完整 chunk 字节。
//
// reader function 会被重复调用直到返回 nil；每次非 nil 返回必须是 string。该函数只负责读取，
// 编译错误和 mode 校验由调用方处理。
func loadChunkBytesFromArgument(state *runtime.State, value runtime.Value) ([]byte, error) {
	if value.Kind == runtime.KindString {
		// 字符串 chunk 直接使用原始字节，保留 NUL 字节。
		return []byte(value.String), nil
	}

	var buffer bytes.Buffer
	for {
		// reader function 每次无参调用，返回 nil 表示输入结束。
		results, err := callProtectedWithState(state, value)
		if err != nil {
			// reader 自身抛错时停止加载。
			return nil, err
		}
		if len(results) == 0 || results[0].IsNil() {
			// 无返回值或 nil 返回值都表示读取结束。
			break
		}
		if results[0].Kind != runtime.KindString {
			// Lua 5.3 要求 reader 返回 string 或 nil。
			return nil, runtime.RaiseError(runtime.StringValue("reader function must return a string"))
		}
		if results[0].String == "" {
			// reader 返回空字符串时按输入结束处理，兼容官方 tests/calls.lua 的逐字符 reader。
			break
		}
		buffer.WriteString(results[0].String)
	}

	// 返回拼接后的完整 chunk。
	return buffer.Bytes(), nil
}

// validateLoadMode 校验 load/loadfile 的 mode 参数是否允许当前 chunk 类型。
//
// mode 中包含 `t` 表示允许文本，包含 `b` 表示允许 binary。错误文本对齐官方测试匹配片段。
func validateLoadMode(chunkBytes []byte, mode string) error {
	isBinaryChunk := isBinaryChunkInput(chunkBytes)
	if isBinaryChunk && !strings.Contains(mode, "b") {
		// binary chunk 在 text-only 模式下必须被拒绝。
		return runtime.RaiseError(runtime.StringValue("attempt to load a binary chunk"))
	}
	if !isBinaryChunk && !strings.Contains(mode, "t") {
		// text chunk 在 binary-only 模式下必须被拒绝。
		return runtime.RaiseError(runtime.StringValue("attempt to load a text chunk"))
	}

	// mode 允许当前 chunk 类型。
	return nil
}

// LoadFile 实现 Lua 5.3 `loadfile` 的文件 chunk 编译入口。
//
// 第一个参数必须是文件名字符串；读取或编译失败时返回 nil 和错误文本。当前阶段不支持 nil 文件名
// 的 stdin 行为，避免测试和嵌入调用阻塞。
func LoadFile(args ...runtime.Value) ([]runtime.Value, error) {
	// 直接调用版本没有 State，只返回未绑定宿主全局环境的 closure，主要服务低层单测。
	return loadFileWithState(nil, args...)
}

// loadFileWithState 实现 Lua 5.3 `loadfile` 的文件 chunk 编译入口，并可绑定宿主全局环境。
//
// state 可为 nil；非 nil 时会把顶层 `_ENV` upvalue 绑定到 state.Globals()。读取或编译失败
// 按 loadfile 语义返回 `(nil, message)`，不抛出 Lua error。
func loadFileWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 || args[0].Kind == runtime.KindNil {
		// stdin 加载尚未接入 CLI 输入流。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(ErrLoadFileStdinUnsupported.Error())}, nil
	}
	if args[0].Kind != runtime.KindString {
		// 文件名必须是 string；错误按 loadfile 语义作为返回值。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue("bad argument #1 to 'loadfile' (string expected)")}, nil
	}
	mode := "bt"
	if len(args) > 1 && args[1].Kind != runtime.KindNil {
		// 第二参数 mode 必须是 string，并控制 text/binary chunk 接受范围。
		if args[1].Kind != runtime.KindString {
			return []runtime.Value{runtime.NilValue(), runtime.StringValue("bad argument #2 to 'loadfile' (string expected)")}, nil
		}
		mode = args[1].String
	}
	var envValue *runtime.Value
	if len(args) > 2 {
		// 第三参数 env 按 Lua 5.3 语义原样绑定到第一个 upvalue；nil 也是显式绑定值。
		envValue = &args[2]
	}

	sourceBytes, err := readLoadFileBytes(state, args[0].String)
	if err != nil {
		// 文件读取失败返回 nil 和文件系统错误文本。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(err.Error())}, nil
	}

	sourceBytes = normalizeFileChunkBytes(sourceBytes)
	if err := validateLoadMode(sourceBytes, mode); err != nil {
		// mode 不允许当前 chunk 类型时按 loadfile 语义返回 nil/message。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(baseLuaErrorText(err))}, nil
	}
	closure, err := loadChunkClosure(sourceBytes, "@"+args[0].String, state, envValue)
	if err != nil {
		// 编译失败返回 nil 和 parser/codegen 错误文本。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(loadErrorText(err, "@"+args[0].String))}, nil
	}

	// 返回文件编译出的 Lua closure。
	return []runtime.Value{closure}, nil
}

// readLoadFileBytes 按 Lua base 库权限策略读取 chunk 文件。
//
// state 为 nil 时保持底层无 State 单测入口的宿主读取行为；state 非 nil 时使用 State Options，
// 支持 VFS 优先、宿主授权兜底和宿主禁用错误。
func readLoadFileBytes(state *runtime.State, filename string) ([]byte, error) {
	// 无 State 的底层 API 没有权限容器，保持历史直接读宿主文件行为。
	if state == nil {
		return os.ReadFile(filename)
	}
	return runtime.ReadFileWithOptions(state.Options(), filename)
}

// normalizeFileChunkBytes 规范化文件加载路径的 chunk 字节。
//
// 文件路径允许开头 UTF-8 BOM 和首行 `#` 注释；当注释后紧跟 binary chunk 时必须去掉注释前缀，
// 让 binary loader 从 ESC 签名开始读取。文本 chunk 则保留 StripInitialShebang 的换行占位。
func normalizeFileChunkBytes(sourceBytes []byte) []byte {
	// 已经以 ESC 开头时直接按 binary chunk 处理，不改动字节流。
	if isBinaryChunkInput(sourceBytes) {
		return sourceBytes
	}
	if binaryBytes, ok := binaryChunkAfterInitialComment(sourceBytes); ok {
		// 官方 files.lua 允许 `#comment\n` 后拼接 string.dump 产物。
		return binaryBytes
	}

	// 普通文件源码允许首行 `#` 注释；load(string) 不走该预处理。
	return []byte(lexer.StripInitialShebang(string(sourceBytes)))
}

// binaryChunkAfterInitialComment 判断文件首行注释后是否紧跟 binary chunk。
//
// 返回的字节切片从 ESC 签名开始，供 bytecode loader 读取完整 header；若注释后不是 binary chunk，
// 返回 false 并由文本加载路径继续处理。
func binaryChunkAfterInitialComment(sourceBytes []byte) ([]byte, bool) {
	const utf8BOM = "\xef\xbb\xbf"

	offset := 0
	if bytes.HasPrefix(sourceBytes, []byte(utf8BOM)) {
		// BOM 只影响首行注释判断；binary chunk 本身必须从 ESC 签名开始。
		offset = len(utf8BOM)
	}
	if offset >= len(sourceBytes) || sourceBytes[offset] != '#' {
		// 没有首行注释时不存在注释后 binary chunk。
		return nil, false
	}

	newlineIndex := -1
	for index := offset; index < len(sourceBytes); index++ {
		// 首行注释可包含 NUL 等任意字节，直到 CR 或 LF 才结束。
		if sourceBytes[index] == '\n' || sourceBytes[index] == '\r' {
			newlineIndex = index
			break
		}
	}
	if newlineIndex < 0 {
		// 没有换行表示整份文件只有注释，不能构成 binary chunk。
		return nil, false
	}

	nextIndex := newlineIndex + 1
	if sourceBytes[newlineIndex] == '\r' && nextIndex < len(sourceBytes) && sourceBytes[nextIndex] == '\n' {
		// CRLF 注释行需要跳过两个换行字节。
		nextIndex++
	}
	if isBinaryChunkInput(sourceBytes[nextIndex:]) {
		// 注释后的剩余内容以 ESC 开头，交给 binary loader 做完整签名校验。
		return sourceBytes[nextIndex:], true
	}
	return nil, false
}

// loadChunkClosure 按 Lua 5.3 chunk 类型加载源码或 binary chunk。
//
// chunkBytes 以 Lua binary chunk 签名开头时直接走 bytecode loader；否则按源码解析编译。
// sourceName 只用于源码编译错误和 Proto.Source，binary chunk 保留自身 dump 出来的 Source。
func loadChunkClosure(chunkBytes []byte, sourceName string, state *runtime.State, envValue *runtime.Value) (runtime.Value, error) {
	if isBinaryChunkInput(chunkBytes) {
		// 预编译 chunk 必须保留二进制字节，不可转成源码再解析。
		proto, err := bytecode.LoadBinaryChunk(bytes.NewReader(chunkBytes))
		if err != nil {
			// loader 错误按 load/loadfile 的可恢复加载错误返回给调用方。
			return runtime.NilValue(), err
		}
		upvalues := loadedClosureUpvalues(state, proto, envValue)
		return runtime.ReferenceValue(runtime.KindLuaClosure, runtime.NewLuaClosure(proto, upvalues, loadedClosureUpvalueCells(upvalues))), nil
	}

	// 非 binary 签名输入按 Lua 源码 chunk 编译。
	return compileLuaClosure(string(chunkBytes), sourceName, state, envValue)
}

// isBinaryChunkInput 判断 load 输入是否应按预编译 chunk 处理。
//
// Lua 5.3 只要输入以 ESC 开头就进入 binary chunk 读取路径；即使签名尚未完整，也应返回
// truncated/corrupted binary chunk 错误，而不是把 ESC 当作源码非法字符解析。
func isBinaryChunkInput(chunkBytes []byte) bool {
	if len(chunkBytes) == 0 {
		// 空输入不是 binary chunk，后续按源码空 chunk 处理。
		return false
	}
	return chunkBytes[0] == byte(bytecode.ChunkSignature[0])
}

// loadErrorText 将内部 parser/codegen 错误转换为 Lua 5.3 load 可见错误文本。
//
// parser 内部保留较工程化的错误分类；对外 load 需要匹配官方测试使用的语法错误片段。
func loadErrorText(err error, sourceName string) string {
	if err == nil {
		// nil 错误没有文本。
		return ""
	}
	return parser.LoadErrorText(err, sourceName)
}

// compileLuaClosure 将 Lua 源码编译为当前 runtime 可保存的 Lua closure 值。
//
// source 是完整 chunk 文本；sourceName 写入 Proto.Source。返回值只表示已编译 closure，
// 不执行字节码，也不注册到 State。
func compileLuaClosure(source string, sourceName string, state *runtime.State, envValue *runtime.Value) (runtime.Value, error) {
	// 先解析 AST，保留 parser 的 syntax/semantic 错误文本。
	chunk, err := parser.New(source).ParseChunk()
	if err != nil {
		// parser 错误直接返回给 load/loadfile 作为第二返回值文本。
		return runtime.NilValue(), err
	}

	proto, err := codegen.CompileChunk(chunk, sourceName)
	if err != nil {
		// codegen 错误同样作为加载错误传播。
		return runtime.NilValue(), err
	}

	// Lua closure 当前只保存 Proto 和 upvalue 快照，执行链路由后续 VM 调用任务接入。
	upvalues := loadedClosureUpvalues(state, proto, envValue)
	return runtime.ReferenceValue(runtime.KindLuaClosure, runtime.NewLuaClosure(proto, upvalues, loadedClosureUpvalueCells(upvalues))), nil
}

// loadedClosureUpvalues 为 load/loadfile 生成的顶层 closure 绑定 upvalue。
//
// envValue 非 nil 时优先绑定到 `_ENV`；否则 state 为 nil 或已关闭时无法提供宿主全局表，所有
// upvalue 使用 nil 占位；非 nil State 会把名为 `_ENV` 的 upvalue 绑定到 globals table，其余
// upvalue 保持 nil。
func loadedClosureUpvalues(state *runtime.State, proto *bytecode.Proto, envValue *runtime.Value) []runtime.Value {
	if proto == nil || len(proto.Upvalues) == 0 {
		// 没有 upvalue 时直接返回 nil，避免分配空切片。
		return nil
	}

	upvalues := make([]runtime.Value, 0, len(proto.Upvalues))
	for upvalueIndex, upvalueDesc := range proto.Upvalues {
		// 顶层 _ENV 需要绑定到宿主全局表；binary chunk 可能缺少调试名，此时按 Lua 5.3
		// 主 chunk 第一个 upvalue 作为 `_ENV` 的约定兜底绑定。
		if upvalueDesc.Name == "_ENV" || (upvalueIndex == 0 && upvalueDesc.Name == "") {
			if envValue != nil {
				// 显式 env 参数原样成为 `_ENV`，即使 env 是 nil 也要覆盖默认 globals。
				upvalues = append(upvalues, *envValue)
				continue
			}
			if state != nil && !state.IsClosed() && state.Globals() != nil {
				// 没有显式 env 时使用当前 State 全局表。
				upvalues = append(upvalues, runtime.ReferenceValue(runtime.KindTable, state.Globals()))
				continue
			}
		}
		// 其他 upvalue 当前没有外部绑定来源，保持 nil 占位以维持索引稳定。
		upvalues = append(upvalues, runtime.NilValue())
	}
	return upvalues
}

// loadedClosureUpvalueCells 为 load/loadfile 顶层 closure 创建闭合 upvalue cell。
//
// dump/load 后的函数没有外层栈帧，但 Lua 5.3 仍允许 debug.setupvalue 和 SETUPVAL 改写
// upvalue；闭合 cell 用于保存跨多次调用的可变 upvalue 状态。
func loadedClosureUpvalueCells(upvalues []runtime.Value) []*runtime.UpvalueCell {
	if len(upvalues) == 0 {
		// 没有 upvalue 时保持 nil，避免生成空 cell 列表。
		return nil
	}
	cells := make([]*runtime.UpvalueCell, 0, len(upvalues))
	for _, upvalue := range upvalues {
		// 每个顶层 upvalue 使用独立闭合 cell，初值与 Upvalues 快照一致。
		cells = append(cells, runtime.NewClosedUpvalueCell(upvalue))
	}
	return cells
}

// callProtected 执行 pcall 当前阶段支持的 callable。
//
// function 必须是 Go closure 或 Lua closure；Lua closure 当前返回阶段性未接入错误。GoFunction 会
// 被适配为单返回值列表，GoResultsFunction 保留多返回值，GoFixedResultsFunction 在命中固定返回
// 快路径时复用预分配结果槽，未命中则回退完整多返回实现。
func callProtected(function runtime.Value, args ...runtime.Value) ([]runtime.Value, error) {
	switch function.Kind {
	case runtime.KindGoClosure:
		// Go closure 是当前可直接执行的 callable。
		switch goFunction := function.Ref.(type) {
		case runtime.GoFunction:
			// GoFunction 只有一个返回值，需要适配为多返回值列表。
			if goFunction == nil {
				// nil 函数负载表示不可调用。
				return nil, runtime.ErrExpectedCallable
			}
			result, err := goFunction(args...)
			if err != nil {
				// GoFunction 错误交由 pcall 转成 false/errorObject。
				return nil, err
			}
			return []runtime.Value{result}, nil
		case runtime.GoResultsFunction:
			// GoResultsFunction 直接返回多值。
			if goFunction == nil {
				// nil 函数负载表示不可调用。
				return nil, runtime.ErrExpectedCallable
			}
			return goFunction(args...)
		case *runtime.GoFixedResultsFunction:
			// 固定上限多返回回调用声明上限构造结果槽；未命中时回退完整函数，避免截断变长返回。
			if goFunction == nil || goFunction.Function == nil {
				// nil 包装或 nil 函数字段表示不可调用。
				return nil, runtime.ErrExpectedCallable
			}
			results := make([]runtime.Value, goFunction.MaxResults)
			resultCount, handled, err := goFunction.Function(results, args...)
			if err != nil {
				// 固定回调错误交由 pcall 转成 false/errorObject。
				return nil, err
			}
			if handled {
				// 命中快路径时只返回实际写入的前缀。
				return results[:resultCount], nil
			}
			if goFunction.Fallback == nil {
				// 没有回退函数时按不可调用错误暴露损坏注册。
				return nil, runtime.ErrExpectedCallable
			}
			return goFunction.Fallback(args...)
		case *runtime.GoClosureWithUpvalues:
			// 带 debug upvalue 元数据的 Go closure 仍通过内部 Function 执行。
			if goFunction == nil || goFunction.Function == nil {
				// nil 包装或 nil 函数字段表示不可调用。
				return nil, runtime.ErrExpectedCallable
			}
			return goFunction.Function(args...)
		default:
			// 未知 Ref 类型表示 Go closure 负载损坏。
			return nil, runtime.ErrExpectedCallable
		}
	case runtime.KindLuaClosure:
		// Lua closure 执行器尚未接入 base pcall。
		return nil, ErrPCallLuaClosureUnsupported
	default:
		// 非函数值不可调用。
		return nil, runtime.ErrExpectedCallable
	}
}

// callProtectedWithState 执行 pcall/xpcall 当前阶段支持的 callable。
//
// state 来自 base.Open 注册时捕获的 State；Go closure 直接执行，Lua closure 通过最小 VM 执行。
func callProtectedWithState(state *runtime.State, function runtime.Value, args ...runtime.Value) ([]runtime.Value, error) {
	// 默认 protected call 不携带调试名称。
	return callProtectedWithStateNamed(state, function, "", "", args...)
}

// callProtectedWithStateNamed 执行 pcall/xpcall 当前阶段支持的 callable，并可写入调试名称。
//
// name/nameWhat 主要供 Lua closure 元方法调用使用，确保 xpcall(debug.traceback) 能看到
// `__index`、`__newindex` 等 Lua 5.3 元方法名称。
func callProtectedWithStateNamed(state *runtime.State, function runtime.Value, name string, nameWhat string, args ...runtime.Value) ([]runtime.Value, error) {
	// 默认 protected call 不是 tail call。
	return callProtectedWithStateNamedTail(state, function, name, nameWhat, false, args...)
}

// callProtectedWithStateNamedTail 执行 pcall/xpcall 当前阶段支持的 callable，并标记 tail call 调试语义。
//
// tailCall=true 表示被调 Lua closure 由 TAILCALL 进入，debug.getinfo(..., "t") 必须暴露
// istailcall=true。Go closure 目前只复用无状态调用路径，不需要额外帧标记。
func callProtectedWithStateNamedTail(state *runtime.State, function runtime.Value, name string, nameWhat string, tailCall bool, args ...runtime.Value) ([]runtime.Value, error) {
	switch function.Kind {
	case runtime.KindGoClosure:
		// Go closure 复用无状态执行路径。
		return callProtected(function, args...)
	case runtime.KindLuaClosure:
		// Lua closure 需要 State 承载调用帧、context 和全局环境。
		return executeBaseLuaClosure(state, function, name, nameWhat, tailCall, args...)
	default:
		// 非函数值不可调用。
		return nil, runtime.ErrExpectedCallable
	}
}

// executeBaseLuaClosure 执行 base 库 pcall/xpcall 中的 Lua closure。
//
// state 必须非 nil 且未关闭；function 必须是 KindLuaClosure。执行循环支持固定寄存器窗口、
// Go/Lua 同步调用、RETURN 退出和 context 检查。
func executeBaseLuaClosure(state *runtime.State, function runtime.Value, name string, nameWhat string, tailCall bool, args ...runtime.Value) (results []runtime.Value, err error) {
	if state == nil {
		// nil State 无法承载调用帧和 context。
		return nil, runtime.ErrNilState
	}
	if err := state.CheckContext(); err != nil {
		// State 已关闭或 context 已取消时不进入 VM。
		return nil, err
	}
	closure, ok := function.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || closure.Proto == nil {
		// Lua closure 引用负载异常时按不可调用错误处理。
		return nil, runtime.ErrExpectedCallable
	}
	proto := closure.Proto
	varargs := baseLuaClosureVarargs(proto, args)
	registerCount := baseLuaClosureRegisterCount(proto, len(args), len(varargs))
	vm := runtime.NewVMWithBorrowedPrototypeData(registerCount, proto.Constants, closure.Upvalues, proto.Protos, varargs)
	vm.BindPrototype(proto)
	vm.BindUpvalueCells(closure.UpvalueCells)
	vm.BindLuaMetamethodRunner(func(method runtime.Value, name string, args ...runtime.Value) ([]runtime.Value, error) {
		// base 库内部 pcall/xpcall 执行器也必须能运行 Lua closure 元方法，避免 protected call 中降级为 unsupported metamethod。
		results, err := callProtectedWithStateNamed(state, method, name, "metamethod", args...)
		if err != nil && name != "" {
			// base 执行器会在错误返回前弹出帧；把元方法名写入错误对象，确保 xpcall(debug.traceback) 仍可匹配来源。
			return nil, runtime.NewRuntimeError(runtime.StringValue(fmt.Sprintf("error in '%s': %s", name, baseLuaErrorText(err))), err)
		}
		return results, err
	})
	state.PushActiveVM(vm)
	defer state.PopActiveVM(vm)
	fixedArgumentCount := baseLuaClosureFixedArgumentCount(proto, len(args))
	for argumentIndex := 0; argumentIndex < fixedArgumentCount; argumentIndex++ {
		// 固定参数从 R0 开始写入。
		if argumentIndex >= registerCount {
			// 寄存器窗口不足时终止执行。
			return nil, runtime.ErrRegisterOutOfRange
		}
		if err := vm.SetRegister(argumentIndex, args[argumentIndex]); err != nil {
			// 写入参数失败时直接返回底层寄存器错误。
			return nil, err
		}
	}
	frame := runtime.NewLuaCallFrame(function, state.StackTop()+1, -1)
	frame.Name = name
	frame.NameWhat = nameWhat
	frame.TailCall = tailCall
	if len(varargs) > 0 {
		// 可变参数需要复制到调用帧快照，供 debug.getlocal 负索引读取。
		frame.Varargs = &runtime.VarargSnapshot{Values: append([]runtime.Value(nil), varargs...)}
	}
	if err := state.PushCallFrame(frame); err != nil {
		// 调用帧压入失败说明调用深度或 State 生命周期不可用。
		return nil, err
	}
	defer func() {
		// 当前执行器由 pcall/xpcall 直接调用，必须自行弹出帧，避免错误路径泄漏调用深度。
		_, _ = state.PopCallFrame()
	}()
	pc := 0
	for pc >= 0 && pc < len(proto.Code) {
		// 先同步当前 PC，供 collectgarbage 执行时按 local 生命周期裁剪活动寄存器根。
		vm.SetCurrentPC(pc)
		// 每条指令前检查 context，支持宿主取消长脚本。
		if err := state.CheckContext(); err != nil {
			return nil, err
		}
		frame.CurrentPC = pc
		if err := state.ReplaceCurrentCallFrame(frame); err != nil {
			// 当前帧缺失说明调用栈边界被破坏。
			return nil, err
		}
		instruction := proto.Code[pc]
		if err := vm.Step(instruction); err != nil {
			// VM 单步错误直接返回给 pcall 捕获。
			return nil, baseLuaStepError(proto, pc, instruction, err)
		}
		if instruction.OpCode() == bytecode.OpNewTable || instruction.OpCode() == bytecode.OpClosure || instruction.OpCode() == bytecode.OpConcat {
			// 分配压力指令后给自动 GC 一次推进机会，覆盖 pcall 内 table、closure 和字符串拼接。
			state.NoteTableAllocation()
		}
		if returnValues := vm.ReturnValues(); returnValues != nil {
			// RETURN 指令结束当前 closure，并返回快照。
			return returnValues, nil
		}
		if callRequest := vm.LastCallRequest(); callRequest != nil {
			if callRequest.Tail {
				// TAILCALL 直接把被调函数结果作为本 closure 结果返回。
				arguments, err := baseLuaCallArguments(vm, callRequest)
				if err != nil {
					// 参数区间读取失败时停止当前调用。
					return nil, err
				}
				functionValue, ok := vm.Register(callRequest.FunctionIndex)
				if !ok {
					// 函数寄存器缺失说明 codegen 或 VM 状态异常。
					return nil, runtime.ErrRegisterOutOfRange
				}
				name, nameWhat := baseLuaCallDebugNameAtCall(functionValue, proto, vm, pc)
				return callProtectedWithStateNamedTail(state, functionValue, name, nameWhat, true, arguments...)
			}
			// 普通 CALL 请求递归消费并写回寄存器。
			if err := executeBaseLuaCallRequest(state, vm, proto, pc, callRequest); err != nil {
				return nil, err
			}
		}
		pc++
		if vm.SkipNext() {
			// 测试类指令要求跳过下一条指令。
			pc++
		}
		pc += vm.PCOffset()
	}
	return nil, nil
}

// baseLuaStepError 把 base 内部 VM 错误转换为更接近 Lua 5.3 的错误对象。
//
// proto 和 pc 来自当前执行点；当 `GETTABLE` 的接收者来自上一条 `_ENV[name]` 读取且为 nil 时，
// 官方错误文本需要标明 global 名称，供 pcall/xpcall 调用方按文本判断来源。
func baseLuaStepError(proto *bytecode.Proto, pc int, instruction bytecode.Instruction, err error) error {
	if err == nil {
		// 没有错误时保持 nil。
		return nil
	}
	if errors.Is(err, runtime.ErrExpectedTable) && instruction.OpCode() == bytecode.OpGetTable {
		// 非 table 字段访问若来自上一条全局读取，补充 global 名称。
		if name, ok := baseLuaPreviousGlobalName(proto, pc, instruction.B()); ok {
			return runtime.NewRuntimeError(runtime.StringValue(fmt.Sprintf("attempt to index a nil value (global '%s')", name)), err)
		}
	}
	return err
}

// baseLuaPreviousGlobalName 尝试从上一条 GETTABUP 指令推断全局变量名。
//
// targetRegister 是当前 GETTABLE 的接收者寄存器；只有上一条指令把 `_ENV[const-string]`
// 写入同一寄存器时才返回名称。
func baseLuaPreviousGlobalName(proto *bytecode.Proto, pc int, targetRegister int) (string, bool) {
	if proto == nil || pc <= 0 || pc-1 >= len(proto.Code) {
		// 缺少上一条指令时无法推断。
		return "", false
	}
	previousInstruction := bytecode.Instruction(0)
	for previousPC := pc - 1; previousPC >= 0; previousPC-- {
		// 向前寻找最近一次写入接收者寄存器的指令；中间可能有 LOADK 写入索引临时寄存器。
		candidate := proto.Code[previousPC]
		if candidate.OpCode().SetsA() && candidate.A() == targetRegister {
			previousInstruction = candidate
			break
		}
	}
	if previousInstruction.OpCode() != bytecode.OpGetTabUp {
		// 最近写入不是 GETTABUP 时无法证明该值来自全局读取。
		return "", false
	}
	name, ok := baseLuaGlobalNameFromRK(proto, pc, previousInstruction.C())
	if !ok {
		// GETTABUP 的 key 不是可追踪字符串常量时无法推断。
		return "", false
	}
	return name, true
}

// baseLuaGlobalNameFromRK 从 RK 操作数或其寄存器来源推断字符串常量。
//
// pc 是当前出错指令位置；当 RK 使用寄存器时，会向前查找最近一次 LOADK 写入该寄存器的字符串。
func baseLuaGlobalNameFromRK(proto *bytecode.Proto, pc int, rk int) (string, bool) {
	if bytecode.IsK(rk) {
		// key 直接以内联常量形式编码。
		return baseLuaStringConstant(proto, bytecode.IndexK(rk))
	}
	for previousPC := pc - 1; previousPC >= 0; previousPC-- {
		// 大常量索引会先 LOADK 到临时寄存器，再用寄存器形式传给 GETTABUP。
		candidate := proto.Code[previousPC]
		if candidate.OpCode().SetsA() && candidate.A() == rk {
			if candidate.OpCode() == bytecode.OpLoadK {
				// 普通 LOADK 直接携带 Bx 常量索引。
				return baseLuaStringConstant(proto, candidate.Bx())
			}
			if candidate.OpCode() == bytecode.OpLoadKX {
				// LOADKX 的常量索引由后一条 EXTRAARG 的 Ax 字段提供。
				if previousPC+1 < len(proto.Code) && proto.Code[previousPC+1].OpCode() == bytecode.OpExtraArg {
					return baseLuaStringConstant(proto, proto.Code[previousPC+1].Ax())
				}
				return "", false
			}
			{
				// 最近写入不是 LOADK 时不能证明该寄存器仍保存字符串常量。
				return "", false
			}
		}
	}
	return "", false
}

// baseLuaStringConstant 读取指定常量位置的非空字符串。
func baseLuaStringConstant(proto *bytecode.Proto, constantIndex int) (string, bool) {
	if constantIndex < 0 || constantIndex >= len(proto.Constants) {
		// 常量索引异常时放弃名称推断，保留原错误。
		return "", false
	}
	constant := proto.Constants[constantIndex]
	if constant.Kind != bytecode.ConstantString || constant.String == "" {
		// 全局名必须是非空字符串常量。
		return "", false
	}
	return constant.String, true
}

// baseLuaErrorText 返回适合拼入 Lua 错误对象的文本。
func baseLuaErrorText(err error) string {
	errorObject := runtime.ErrorObject(err)
	if errorObject.Kind == runtime.KindString {
		// 字符串错误对象直接使用原文本，避免 DebugString 额外包裹 string(...)。
		return errorObject.String
	}
	return errorObject.DebugString()
}

// baseLuaClosureRegisterCount 计算执行 Lua closure 所需寄存器数量。
//
// proto.MaxStackSize 来自 codegen；入参数量和运行期 vararg 数量可能大于静态估算。
func baseLuaClosureRegisterCount(proto *bytecode.Proto, argumentCount int, varargCount int) int {
	registerCount := int(proto.MaxStackSize)
	if registerCount < int(proto.NumParams) {
		// 固定参数寄存器必须至少覆盖 NumParams。
		registerCount = int(proto.NumParams)
	}
	fixedArgumentCount := baseLuaClosureFixedArgumentCount(proto, argumentCount)
	if registerCount < fixedArgumentCount {
		// 固定参数需要写入 R0..，寄存器窗口必须覆盖它们。
		registerCount = fixedArgumentCount
	}
	for _, instruction := range proto.Code {
		// 开放 VARARG 的实际写入数量只有运行期才知道。
		if instruction.OpCode() == bytecode.OpVararg && instruction.B() == 0 {
			requiredRegisterCount := instruction.A() + varargCount
			if registerCount < requiredRegisterCount {
				// B=0 会从 A 连续写入所有 vararg。
				registerCount = requiredRegisterCount
			}
		}
	}
	if registerCount < 1 {
		// VM 至少保留一个寄存器便于错误路径和 RETURN R0。
		registerCount = 1
	}
	return registerCount
}

// baseLuaClosureFixedArgumentCount 计算需要写入寄存器的固定参数数量。
//
// Lua 多余实参不会写入固定参数寄存器；vararg 部分单独放入 VM varargs 快照。
func baseLuaClosureFixedArgumentCount(proto *bytecode.Proto, argumentCount int) int {
	fixedCount := int(proto.NumParams)
	if argumentCount < fixedCount {
		// 实参数量不足时只写入已有实参，缺失参数保持 nil。
		return argumentCount
	}
	return fixedCount
}

// baseLuaClosureVarargs 计算 Lua closure 的 vararg 快照。
//
// 非 vararg 函数不暴露额外参数；vararg 函数只把固定参数之后的实参放入 varargs。
func baseLuaClosureVarargs(proto *bytecode.Proto, args []runtime.Value) []runtime.Value {
	if !proto.IsVararg || len(args) <= int(proto.NumParams) {
		// 非 vararg 或没有额外参数时返回空 vararg。
		return nil
	}
	return append([]runtime.Value(nil), args[int(proto.NumParams):]...)
}

// executeBaseLuaCallRequest 消费 VM 执行中产生的调用请求。
//
// callRequest 必须来自同一个 vm 最近一次 Step；调用结果会写回 vm 寄存器窗口。
func executeBaseLuaCallRequest(state *runtime.State, vm *runtime.VM, proto *bytecode.Proto, callPC int, callRequest *runtime.CallRequest) error {
	arguments, err := baseLuaCallArguments(vm, callRequest)
	if err != nil {
		// 参数区间读取失败时停止当前调用。
		return err
	}
	functionValue, ok := vm.Register(callRequest.FunctionIndex)
	if !ok {
		// 函数寄存器缺失说明 codegen 或 VM 状态异常。
		return runtime.ErrRegisterOutOfRange
	}
	name, nameWhat := baseLuaCallDebugNameAtCall(functionValue, proto, vm, callPC)
	results, err := callProtectedWithStateNamed(state, functionValue, name, nameWhat, arguments...)
	if err != nil {
		// 被调函数错误向上传播，由外层 pcall/xpcall 处理。
		return err
	}
	return writeBaseLuaCallResults(vm, callRequest, results)
}

// baseLuaCallDebugNameAtCall 按 pcall/xpcall 内部执行器的 CALL 指令上下文推断调试名称。
//
// function 是当前被调值；proto/vm/callPC 来自调用方 VM。返回值写入调用帧的 name/namewhat，
// 使 debug.getinfo 在 protected call 内仍能观察 local、field、global 和 upvalue 调用来源。
func baseLuaCallDebugNameAtCall(function runtime.Value, proto *bytecode.Proto, vm *runtime.VM, callPC int) (string, string) {
	if proto == nil || vm == nil || callPC <= 0 || callPC >= len(proto.Code) {
		// 缺少调用点上下文时不能推断名称。
		return "", ""
	}
	callInstruction := proto.Code[callPC]
	if callInstruction.OpCode() != bytecode.OpCall && callInstruction.OpCode() != bytecode.OpTailCall {
		// 只处理普通 CALL/TAILCALL 调用点。
		return "", ""
	}
	previousInstruction, writerPC, ok := basePreviousRegisterWriterAt(proto, callPC, callInstruction.A())
	if !ok {
		// 找不到函数寄存器来源时保持匿名。
		return "", ""
	}
	if baseIsShortCircuitCallValue(proto, writerPC, callPC) {
		// and/or 短路表达式结果被调用时，错误文本只保留值类型，不暴露最后分支变量名。
		return "", ""
	}
	switch previousInstruction.OpCode() {
	case bytecode.OpMove:
		// local 函数调用通常由 MOVE 把局部函数搬到调用寄存器。
		return baseInferLocalDebugName(function, proto, vm, previousInstruction.B(), callPC)
	case bytecode.OpGetUpval:
		// 递归闭包可通过 GETUPVAL 读取外层 local 函数。
		return baseUpvalueDebugName(proto, previousInstruction.B())
	case bytecode.OpGetTable:
		// table.field() 通过 GETTABLE 加载字段函数。
		return baseConstantStringDebugName(proto, previousInstruction.C(), "field")
	case bytecode.OpGetTabUp:
		// 全局函数调用通过 GETTABUP _ENV K(name) 加载函数。
		return baseConstantStringDebugName(proto, previousInstruction.C(), "global")
	default:
		// 其他取值形态暂不暴露静态名称。
		return "", ""
	}
}

// basePreviousRegisterWriter 向前查找最近一次写入目标寄存器的取值指令。
func basePreviousRegisterWriter(proto *bytecode.Proto, callPC int, targetRegister int) (bytecode.Instruction, bool) {
	instruction, _, ok := basePreviousRegisterWriterAt(proto, callPC, targetRegister)
	return instruction, ok
}

// basePreviousRegisterWriterAt 向前查找最近一次写入目标寄存器的取值指令及其 PC。
func basePreviousRegisterWriterAt(proto *bytecode.Proto, callPC int, targetRegister int) (bytecode.Instruction, int, bool) {
	for pc := callPC - 1; pc >= 0; pc-- {
		// 仅识别当前 debug 名称推断需要的函数来源指令。
		instruction := proto.Code[pc]
		switch instruction.OpCode() {
		case bytecode.OpMove, bytecode.OpGetUpval, bytecode.OpGetTable, bytecode.OpGetTabUp:
			if instruction.A() == targetRegister {
				// 命中目标寄存器最近写入者。
				return instruction, pc, true
			}
		case bytecode.OpCall, bytecode.OpTailCall:
			if instruction.A() == targetRegister {
				// 动态返回值作为函数时不能静态命名。
				return instruction, pc, true
			}
		default:
			// 其他指令暂不作为函数来源。
		}
	}
	return bytecode.Instruction(0), -1, false
}

// baseIsShortCircuitCallValue 判断 CALL 的函数槽是否来自紧邻的短路表达式结果。
func baseIsShortCircuitCallValue(proto *bytecode.Proto, writerPC int, callPC int) bool {
	if writerPC != callPC-1 {
		// 分支内部直接调用会先写函数槽、再准备参数；这类调用仍应保留函数名。
		return false
	}
	return baseIsShortCircuitBranchValue(proto, writerPC)
}

// baseIsShortCircuitBranchValue 判断最近写入值是否位于 and/or 短路分支后。
func baseIsShortCircuitBranchValue(proto *bytecode.Proto, writerPC int) bool {
	if proto == nil || writerPC <= 0 || writerPC >= len(proto.Code) {
		// 缺少前置指令或写入位置越界时不能判定为短路表达式。
		return false
	}
	previousInstruction := proto.Code[writerPC-1]
	if previousInstruction.OpCode() == bytecode.OpJmp || previousInstruction.OpCode() == bytecode.OpTest || previousInstruction.OpCode() == bytecode.OpTestSet {
		// codegen 为 and/or 分支生成 TEST/JMP 后再写入候选结果，此时寄存器不是直接变量来源。
		return true
	}
	return false
}

// baseInferLocalDebugName 根据活动 local 与闭包 Proto identity 推断 local 调用名。
func baseInferLocalDebugName(function runtime.Value, proto *bytecode.Proto, vm *runtime.VM, register int, callPC int) (string, string) {
	if name, nameWhat := baseRecentClosureLocalDebugName(function, proto, register, callPC); name != "" {
		// 最近一次写入源寄存器的 CLOSURE 与当前被调函数一致时，优先使用对应活动 local。
		return name, nameWhat
	}
	for localIndex := len(proto.LocalVars) - 1; localIndex >= 0; localIndex-- {
		// 优先使用 local function 声明对应的 CLOSURE Proto identity，避免 dumped chunk 寄存器重建误判。
		localVar := proto.LocalVars[localIndex]
		if localVar.Name == "" || !localVar.ActiveAt(callPC) || localVar.StartPC <= 0 || localVar.StartPC > len(proto.Code) {
			// 非活动、无名称或缺少声明指令时不能作为可靠候选。
			continue
		}
		declarationInstruction := proto.Code[localVar.StartPC-1]
		if declarationInstruction.OpCode() == bytecode.OpClosure && !baseDeclarationClosureMatchesFunction(proto, declarationInstruction, function) {
			// local function 必须匹配当前被调闭包。
			continue
		}
		if baseInstructionWritesRegister(declarationInstruction, register) || baseDeclarationClosureMatchesFunction(proto, declarationInstruction, function) {
			// 命中声明写入或闭包 Proto identity 时返回 local 名称来源。
			return localVar.Name, "local"
		}
	}
	for localIndex := len(proto.LocalVars) - 1; localIndex >= 0; localIndex-- {
		// 非 local function 退回到活动寄存器槽位匹配。
		localVar := proto.LocalVars[localIndex]
		if localVar.Register == register && localVar.ActiveAt(callPC) && localVar.Name != "" {
			// 命中活动 local 时返回 local 名称来源。
			return localVar.Name, "local"
		}
	}
	return "", ""
}

// baseRecentClosureLocalDebugName 根据源寄存器最近一次 CLOSURE 写入推断 local 调用名。
func baseRecentClosureLocalDebugName(function runtime.Value, proto *bytecode.Proto, register int, callPC int) (string, string) {
	for pc := callPC - 1; pc >= 0; pc-- {
		// 只关心写入源寄存器的最近指令。
		instruction := proto.Code[pc]
		if !baseInstructionWritesRegister(instruction, register) {
			// 未写入源寄存器时继续向前查找。
			continue
		}
		if instruction.OpCode() != bytecode.OpClosure || !baseDeclarationClosureMatchesFunction(proto, instruction, function) {
			// 最近写入不是当前闭包时不能使用更早声明猜测名称。
			return "", ""
		}
		localName := baseRecentClosureLocalName(proto, register, callPC, pc)
		if localName == "" {
			// 找不到活动 local 时保持匿名。
			return "", ""
		}
		return localName, "local"
	}
	return "", ""
}

// baseRecentClosureLocalName 为指定 CLOSURE 写入点选择对应活动 local 名称。
func baseRecentClosureLocalName(proto *bytecode.Proto, register int, callPC int, writerPC int) string {
	fallbackName := ""
	fallbackStartPC := -1
	for localIndex := len(proto.LocalVars) - 1; localIndex >= 0; localIndex-- {
		// 优先选择同寄存器活动 local；寄存器不可用时退回到写入点之前最近启用的 local。
		localVar := proto.LocalVars[localIndex]
		if localVar.Name == "" || !localVar.ActiveAt(callPC) || localVar.StartPC > writerPC+1 {
			// 无名、非活动或晚于写入点才开始的 local 不能作为候选。
			continue
		}
		if localVar.Register == register {
			// 寄存器精确命中最可靠。
			return localVar.Name
		}
		if localVar.StartPC > fallbackStartPC {
			// 寄存器不可用时选择离写入点最近的活动 local。
			fallbackName = localVar.Name
			fallbackStartPC = localVar.StartPC
		}
	}
	return fallbackName
}

// baseDeclarationClosureMatchesFunction 判断 local 声明闭包是否就是当前被调函数。
func baseDeclarationClosureMatchesFunction(proto *bytecode.Proto, instruction bytecode.Instruction, function runtime.Value) bool {
	if proto == nil || instruction.OpCode() != bytecode.OpClosure || function.Kind != runtime.KindLuaClosure {
		// 只有 Lua CLOSURE 声明具备可比较的 Proto identity。
		return false
	}
	closure, ok := function.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || closure.Proto == nil {
		// 损坏闭包引用不能参与推断。
		return false
	}
	childIndex := instruction.Bx()
	if childIndex < 0 || childIndex >= len(proto.Protos) {
		// 损坏子 Proto 索引不能参与推断。
		return false
	}
	return proto.Protos[childIndex] == closure.Proto
}

// baseInstructionWritesRegister 判断指令是否会把结果写入指定寄存器。
func baseInstructionWritesRegister(instruction bytecode.Instruction, register int) bool {
	// 按 opcode 判断 A 或连续结果区间是否覆盖目标寄存器。
	switch instruction.OpCode() {
	case bytecode.OpMove, bytecode.OpLoadK, bytecode.OpLoadKX, bytecode.OpLoadBool, bytecode.OpGetUpval,
		bytecode.OpGetTabUp, bytecode.OpGetTable, bytecode.OpNewTable, bytecode.OpSelf, bytecode.OpAdd,
		bytecode.OpSub, bytecode.OpMul, bytecode.OpMod, bytecode.OpPow, bytecode.OpDiv, bytecode.OpIDiv,
		bytecode.OpBAnd, bytecode.OpBOr, bytecode.OpBXor, bytecode.OpShl, bytecode.OpShr, bytecode.OpUnm,
		bytecode.OpBNot, bytecode.OpNot, bytecode.OpLen, bytecode.OpConcat, bytecode.OpClosure:
		// 单结果指令以 A 作为目标寄存器。
		return instruction.A() == register
	case bytecode.OpLoadNil:
		// LOADNIL 写入 A 到 A+B。
		return register >= instruction.A() && register <= instruction.A()+instruction.B()
	case bytecode.OpCall, bytecode.OpTailCall, bytecode.OpVararg:
		// 多返回指令至少写入 A，精确数量由运行时决定。
		return instruction.A() == register
	default:
		// 其他指令不作为 local 声明写入来源。
		return false
	}
}

// baseUpvalueDebugName 根据 upvalue 描述推断调用名称。
func baseUpvalueDebugName(proto *bytecode.Proto, upvalueIndex int) (string, string) {
	if proto == nil || upvalueIndex < 0 || upvalueIndex >= len(proto.Upvalues) {
		// 缺少有效 upvalue 描述时不能推断。
		return "", ""
	}
	upvalueName := proto.Upvalues[upvalueIndex].Name
	if upvalueName == "" {
		// 无名 upvalue 不暴露调试名称。
		return "", ""
	}
	return upvalueName, "upvalue"
}

// baseConstantStringDebugName 从 RK 常量操作数提取字符串名称。
func baseConstantStringDebugName(proto *bytecode.Proto, operand int, nameWhat string) (string, string) {
	if !bytecode.IsK(operand) {
		// 寄存器 key 暂不做静态名称推断。
		return "", ""
	}
	constantIndex := bytecode.IndexK(operand)
	if constantIndex < 0 || constantIndex >= len(proto.Constants) {
		// 损坏常量索引不能参与推断。
		return "", ""
	}
	constant := proto.Constants[constantIndex]
	if constant.Kind != bytecode.ConstantString || constant.String == "" {
		// 只有非空字符串 key 能作为名称。
		return "", ""
	}
	return constant.String, nameWhat
}

// baseLuaCallArguments 从 VM 寄存器窗口读取调用实参。
//
// 当前最小执行循环只支持固定参数数量；开放参数数量需要完整栈顶语义，后续补齐。
func baseLuaCallArguments(vm *runtime.VM, callRequest *runtime.CallRequest) ([]runtime.Value, error) {
	if callRequest.ArgumentCount < 0 {
		// 开放参数需要真实栈顶，当前执行循环暂不支持。
		return nil, runtime.ErrUnsupportedInstruction
	}
	arguments := make([]runtime.Value, 0, callRequest.ArgumentCount)
	for argumentIndex := 0; argumentIndex < callRequest.ArgumentCount; argumentIndex++ {
		// 普通 CALL 的实参紧跟函数寄存器；TFORCALL 的实参固定为 state/control。
		argumentRegister := callRequest.FunctionIndex + argumentIndex + 1
		if callRequest.GenericFor {
			// 泛型 for 的参数布局为 R(A+1), R(A+2)，与结果写入区 R(A+3).. 分离。
			argumentRegister = callRequest.FunctionIndex + argumentIndex + 1
		}
		value, ok := vm.Register(argumentRegister)
		if !ok {
			return nil, runtime.ErrRegisterOutOfRange
		}
		arguments = append(arguments, value)
	}
	return arguments, nil
}

// writeBaseLuaCallResults 将调用结果写回 VM 寄存器窗口。
//
// 固定返回数量会用 nil 补齐；开放返回数量写入所有结果，当前由寄存器窗口边界控制。
func writeBaseLuaCallResults(vm *runtime.VM, callRequest *runtime.CallRequest, results []runtime.Value) error {
	resultCount := callRequest.ReturnCount
	if resultCount < 0 {
		// 开放返回写入被调函数实际返回的所有结果。
		resultCount = len(results)
		if !callRequest.GenericFor {
			// 开放返回数量运行时才知道，先扩展寄存器窗口再逐项写回。
			vm.EnsureRegisterCount(callRequest.FunctionIndex + resultCount)
		}
	}
	for resultIndex := 0; resultIndex < resultCount; resultIndex++ {
		// 普通 CALL 返回值从函数寄存器开始覆盖；TFORCALL 返回值写入 ResultIndex。
		resultValue := runtime.NilValue()
		if resultIndex < len(results) {
			// 被调函数实际返回的结果优先写入。
			resultValue = results[resultIndex]
		}
		resultRegister := callRequest.FunctionIndex + resultIndex
		if callRequest.GenericFor {
			// 泛型 for 不能覆盖迭代函数/state/control，必须写入迭代变量区。
			resultRegister = callRequest.ResultIndex + resultIndex
		}
		if err := vm.SetRegister(resultRegister, resultValue); err != nil {
			// 写回超过寄存器窗口时返回边界错误。
			return err
		}
	}
	if callRequest.ReturnCount < 0 && !callRequest.GenericFor {
		// CALL C=0 表示开放返回，记录实际结果上界供后续 CALL B=0 消费。
		vm.SetOpenTop(callRequest.FunctionIndex + len(results))
	} else {
		// 固定返回数量不形成开放列表。
		vm.SetOpenTop(-1)
	}
	return nil
}

// tableArgument 读取 base 标准库第一个 table 参数。
//
// args[0] 必须是 KindTable 且 Ref 为 *runtime.Table；functionName 用于构造参数错误文本。
func tableArgument(args []runtime.Value, functionName string) (*runtime.Table, error) {
	if len(args) == 0 || args[0].Kind != runtime.KindTable {
		// 缺少 table 或类型不是 table 时返回 Lua 参数错误。
		return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #1 to '%s' (table expected)", functionName)))
	}
	table, ok := args[0].Ref.(*runtime.Table)
	if !ok || table == nil {
		// table 引用损坏时返回 runtime 层 table 错误。
		return nil, runtime.ErrExpectedTable
	}

	// 返回已验证的 table 指针。
	return table, nil
}

// typeName 返回 Lua 5.3 `type` 可见类型名。
//
// function 包含 Lua closure 和 Go closure；thread、userdata、table 等按基础类型名输出。
func typeName(value runtime.Value) string {
	switch value.Kind {
	case runtime.KindNil:
		// nil 的类型名固定为 nil。
		return "nil"
	case runtime.KindBoolean:
		// boolean 的类型名固定为 boolean。
		return "boolean"
	case runtime.KindInteger, runtime.KindNumber:
		// integer 和 float 在 Lua type 中都显示为 number。
		return "number"
	case runtime.KindString:
		// string 的类型名固定为 string。
		return "string"
	case runtime.KindTable:
		// table 的类型名固定为 table。
		return "table"
	case runtime.KindLuaClosure, runtime.KindGoClosure:
		// Lua 和 Go callable 都是 function。
		return "function"
	case runtime.KindUserdata:
		// userdata 的类型名固定为 userdata。
		return "userdata"
	case runtime.KindThread:
		// thread/coroutine 的类型名固定为 thread。
		return "thread"
	default:
		// 未知类型按 nil 兜底，避免泄露内部数字标签。
		return "nil"
	}
}

// countSnapshotValues 统计 GC root 快照中的值数量。
//
// snapshot 可以为空；返回值只作为 collectgarbage("count") 的阶段性可观测指标。
func countSnapshotValues(snapshot runtime.GCRootSnapshot) int {
	// 逐类累加 root 数量，避免依赖 map 遍历顺序。
	total := 0
	for _, values := range snapshot.Batches {
		// 每个 batch 的长度代表该分类采集到的 root 样本数。
		total += len(values)
	}
	return total
}
