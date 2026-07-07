//go:build native_modules

package native

/*
#include <stddef.h>

typedef struct lua_State lua_State;
typedef ptrdiff_t lua_KContext;
typedef int (*lua_KFunction)(lua_State *L, int status, lua_KContext ctx);
*/
import "C"

import (
	"unsafe"

	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	nativeLuaOK       = 0
	nativeLuaErrRun   = 2
	nativeLuaMultiRet = -1
)

// nativeLuaCallValue 调用当前 native shim 可识别的 Lua/Go closure。
func nativeLuaCallValue(state *runtime.State, function runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	// native pcall 需要在 runtime 层调用闭包，避免把 C API protected call 引回 package/searcher。
	if state == nil {
		// nil State 无法执行任何 closure。
		return nil, runtime.ErrClosedState
	}
	callFrameFunction := runtime.ReferenceValue(runtime.KindGoClosure, "native lua_call")
	frame := runtime.NewGoCallFrame(callFrameFunction, state.StackTop()+1, -1)
	frame.Name = "lua_call"
	frame.NameWhat = "C"
	if err := state.PushCallFrame(frame); err != nil {
		// C API 调 Lua/Go closure 时也需要可见 C 帧，否则 error(level) 会越过 native 边界。
		return nil, err
	}
	defer func() {
		// 调用完成后恢复调用栈，避免 C API 临时帧泄漏到后续 Lua 执行。
		_, _ = state.PopCallFrame()
	}()
	switch function.Kind {
	case runtime.KindLuaClosure:
		// Lua closure 使用 State 注入的 runner，保持 VM 栈帧与 upvalue 语义。
		return state.CallLuaClosure(function, args...)
	case runtime.KindGoClosure:
		// Go closure 需要覆盖 native C function wrapper 使用的带 upvalue 形态。
		return nativeLuaCallGoClosure(function, args)
	default:
		// 非 callable 值按 Lua runtime call 错误传播。
		return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrExpectedCallable.Error()), runtime.ErrExpectedCallable)
	}
}

// nativeLuaCallGoClosure 执行当前 native shim 可识别的 Go closure 负载。
func nativeLuaCallGoClosure(function runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	// 根据 runtime.Value.Ref 的实际回调形态分发，语义与 lua.Call 的 Go closure 路径保持一致。
	if function.Kind != runtime.KindGoClosure {
		// 非 Go closure 不能进入本 helper。
		return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrExpectedCallable.Error()), runtime.ErrExpectedCallable)
	}
	switch callback := function.Ref.(type) {
	case runtime.GoResultsFunction:
		// 多返回 Go 回调直接返回结果列表。
		return callback(args...)
	case *runtime.GoClosureWithUpvalues:
		// native C closure 通过该包装保存 upvalue 元数据。
		if callback == nil || callback.Function == nil {
			// 损坏 closure 按不可调用处理。
			return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrExpectedCallable.Error()), runtime.ErrExpectedCallable)
		}
		return callback.Function(args...)
	case runtime.GoFunction:
		// 单返回 Go 回调转换为单元素结果列表。
		result, err := callback(args...)
		if err != nil {
			// 回调错误原样传播，protected call 边界会转换为 Lua error object。
			return nil, err
		}
		return []runtime.Value{result}, nil
	case runtime.GoUnaryFunction:
		// 单参数 Go 回调是 GoFunction 的热路径形态；C API 调用仍按普通 Lua 调用只读取首个实参。
		argument := runtime.NilValue()
		if len(args) > 0 {
			// Lua 调用允许多余实参，一元标准库函数只消费第一个实参。
			argument = args[0]
		}
		result, err := callback(argument)
		if err != nil {
			// 回调错误原样传播，protected call 边界会转换为 Lua error object。
			return nil, err
		}
		return []runtime.Value{result}, nil
	case *runtime.GoFastUnaryFunction:
		// fast unary 在 native C API 回调中不能跳过调用边界，按其普通一元函数入口执行。
		if callback == nil || callback.Function == nil {
			// 损坏 closure 按不可调用处理。
			return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrExpectedCallable.Error()), runtime.ErrExpectedCallable)
		}
		argument := runtime.NilValue()
		if len(args) > 0 {
			// Lua 调用允许多余实参，一元标准库函数只消费第一个实参。
			argument = args[0]
		}
		result, err := callback.Function(argument)
		if err != nil {
			// 回调错误原样传播，protected call 边界会转换为 Lua error object。
			return nil, err
		}
		return []runtime.Value{result}, nil
	case *runtime.GoFixedResultsFunction:
		// 固定结果回调先尝试声明的通用入口，未命中再走 fallback。
		if callback == nil || callback.Function == nil {
			// 损坏 callback 按不可调用处理。
			return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrExpectedCallable.Error()), runtime.ErrExpectedCallable)
		}
		results := make([]runtime.Value, callback.MaxResults)
		resultCount, handled, err := callback.Function(results, args...)
		if err != nil {
			// 回调错误原样传播。
			return nil, err
		}
		if handled {
			// 命中固定结果路径时只返回已写入前缀。
			return results[:resultCount], nil
		}
		if callback.Fallback == nil {
			// 没有 fallback 无法保持完整语义。
			return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrExpectedCallable.Error()), runtime.ErrExpectedCallable)
		}
		return callback.Fallback(args...)
	default:
		// 未知 Go closure 负载暂不暴露为可调用。
		return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrExpectedCallable.Error()), runtime.ErrExpectedCallable)
	}
}

// nativeLuaPushCallResults 按 lua_pcallk 的 nresults 语义压回结果。
func nativeLuaPushCallResults(luaState unsafe.Pointer, results []runtime.Value, resultCount int) {
	// LUA_MULTRET 表示保留所有返回值；固定数量需要截断或补 nil。
	if resultCount == nativeLuaMultiRet {
		// 所有返回值按原顺序压入。
		for resultIndex := range results {
			nativeLuaPushValue(luaState, results[resultIndex])
		}
		return
	}
	if resultCount < 0 {
		// 其他负数不是合法 nresults，当前最小 shim 当作 0 结果。
		resultCount = 0
	}
	for resultIndex := 0; resultIndex < resultCount; resultIndex++ {
		// 结果不足时按 Lua 调用语义补 nil。
		if resultIndex >= len(results) {
			nativeLuaPushNil(luaState)
			continue
		}
		nativeLuaPushValue(luaState, results[resultIndex])
	}
}

// nativeLuaCallK 实现不支持 yield continuation 的非 protected call，并返回是否需要 C 入口抛错。
func nativeLuaCallK(luaState unsafe.Pointer, argumentCount int, resultCount int) bool {
	// 当前 native shim 不支持 C continuation/yield；无 yield 时 lua_callk 等价于非 protected lua_call。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 没有可回填的错误栈，保持 no-op 让外层边界暴露 State 生命周期问题。
		return false
	}
	if argumentCount < 0 {
		// 非 protected 调用没有错误码返回；记录 pending error 后由 C 入口按 lua_call 语义立即跳出。
		_ = setNativeStatePendingError(luaState, runtime.StringValue("bad argument count to lua_callk"))
		return true
	}
	baseTop := 0
	if currentBaseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内 lua_callk 只能消费当前 C 帧可见栈，不能穿透外层 Go VM 栈。
		baseTop = currentBaseTop
	}
	if state.StackTop()-baseTop < argumentCount+1 {
		// 当前 C 帧可见槽不足时记录错误，但不弹栈，避免误删外层调用帧值。
		_ = setNativeStatePendingError(luaState, runtime.StringValue("attempt to call a missing native value"))
		return true
	}
	functionIndex := state.StackTop() - argumentCount
	if functionIndex <= baseTop {
		// 栈上没有被调函数时记录运行期错误，避免 C 模块继续读取伪造结果。
		_ = setNativeStatePendingError(luaState, runtime.StringValue("attempt to call a missing native value"))
		return true
	}
	function := state.ValueAt(functionIndex)
	args := make([]runtime.Value, argumentCount)
	for argumentIndex := 0; argumentIndex < argumentCount; argumentIndex++ {
		// 调用前复制实参快照，后续按 Lua C API 语义移除函数和参数槽。
		args[argumentIndex] = state.ValueAt(functionIndex + 1 + argumentIndex)
	}
	nativeLuaRestoreStackTop(state, functionIndex-1)

	var results []runtime.Value
	var callErr error
	func() {
		// 非 protected C API 不能让 Go panic 穿过 cgo 边界；转换为 pending Lua error 交给外层传播。
		defer func() {
			if recovered := recover(); recovered != nil {
				// panic 值按 Lua error object 语义转换，保持和 runtime protected call 的错误对象规则一致。
				callErr = runtime.PanicToError(recovered)
			}
		}()
		results, callErr = nativeLuaCallValue(state, function, args)
	}()
	if callErr != nil {
		// 非 protected 调用失败时不压栈返回错误对象，而是让 C 入口立刻跳出当前 C 调用链。
		_ = setNativeStatePendingError(luaState, runtime.ErrorObject(callErr))
		return true
	}
	nativeLuaPushCallResults(luaState, results, resultCount)
	return false
}

// nativeLuaPCallK 实现不支持 yield continuation 的 protected call。
func nativeLuaPCallK(luaState unsafe.Pointer, argumentCount int, resultCount int, errorFunction int) int {
	// 当前 native shim 不支持 C continuation/yield；无 yield 时 lua_pcallk 等价于 lua_pcall。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 没有错误栈可写，只返回运行期错误码。
		return nativeLuaErrRun
	}
	_ = errorFunction
	if argumentCount < 0 {
		// 参数数量非法时压入错误对象，便于调用方 pcall 捕获。
		nativeLuaPushValue(luaState, runtime.StringValue("bad argument count to lua_pcallk"))
		return nativeLuaErrRun
	}
	baseTop := 0
	if currentBaseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内 lua_pcallk 只能消费当前 C 帧可见栈，不能穿透外层 Go VM 栈。
		baseTop = currentBaseTop
	}
	if state.StackTop()-baseTop < argumentCount+1 {
		// 当前 C 帧可见槽不足时按 protected call 语义压入错误对象，但不弹外层栈。
		nativeLuaPushValue(luaState, runtime.StringValue("attempt to call a missing native value"))
		return nativeLuaErrRun
	}
	functionIndex := state.StackTop() - argumentCount
	if functionIndex <= baseTop {
		// 栈上缺失被调函数时按运行期错误处理。
		nativeLuaPushValue(luaState, runtime.StringValue("attempt to call a missing native value"))
		return nativeLuaErrRun
	}
	function := state.ValueAt(functionIndex)
	args := make([]runtime.Value, argumentCount)
	for argumentIndex := 0; argumentIndex < argumentCount; argumentIndex++ {
		// 从函数后连续复制 C API 栈上的实参快照。
		args[argumentIndex] = state.ValueAt(functionIndex + 1 + argumentIndex)
	}
	nativeLuaRestoreStackTop(state, functionIndex-1)

	var results []runtime.Value
	err := state.ProtectedCall(func(protectedState *runtime.State) error {
		// ProtectedCall 捕获 Lua/Go panic 和 runtime error，并在失败时恢复边界。
		callResults, callErr := nativeLuaCallValue(protectedState, function, args)
		if callErr != nil {
			// 错误交给 ProtectedCall 转换为带 traceback 的 RuntimeError。
			return callErr
		}
		results = callResults
		return nil
	})
	if err != nil {
		// Lua C API 要求 pcall 失败时压入 error object 并返回错误码。
		nativeLuaPushValue(luaState, runtime.ErrorObject(err))
		return nativeLuaErrRun
	}
	nativeLuaPushCallResults(luaState, results, resultCount)
	return nativeLuaOK
}

// lua_pcallk 导出 Lua 5.3 C API protected call 入口。
//
//export lua_pcallk
func lua_pcallk(luaState *C.lua_State, argumentCount C.int, resultCount C.int, errorFunction C.int, context C.lua_KContext, continuation C.lua_KFunction) C.int {
	// 当前 VM 不支持从 C API yield；非 yield 场景忽略 continuation/context，按 pcall 语义执行。
	_, _ = context, continuation
	return C.int(nativeLuaPCallK(unsafe.Pointer(luaState), int(argumentCount), int(resultCount), int(errorFunction)))
}

// glua_lua_callk_record 执行 Lua 5.3 C API 非 protected call，并把错误交给 C wrapper longjmp。
//
//export glua_lua_callk_record
func glua_lua_callk_record(luaState *C.lua_State, argumentCount C.int, resultCount C.int) C.int {
	// C wrapper 已忽略 continuation/context；本 helper 只负责执行调用并报告是否需要非 protected 抛错。
	if nativeLuaCallK(unsafe.Pointer(luaState), int(argumentCount), int(resultCount)) {
		return 1
	}
	return 0
}
