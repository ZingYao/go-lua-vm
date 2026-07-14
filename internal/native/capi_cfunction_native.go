//go:build cgo

package native

/*
typedef struct lua_State lua_State;
typedef int (*lua_CFunction)(lua_State *L);
*/
import "C"

import (
	"unsafe"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// nativeLuaPushCClosure 把 Lua C 函数指针包装为当前 Go VM 可调用 closure。
func nativeLuaPushCClosure(luaState unsafe.Pointer, function unsafe.Pointer, upvalueCount int) {
	// nil 函数指针不能伪装成可调用 closure，保持栈不变暴露能力边界。
	if function == nil || upvalueCount < 0 {
		// 非法函数指针或 upvalue 数量不修改栈。
		return
	}
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// 无效 State 无法创建绑定到 VM 的 C function wrapper。
		return
	}
	baseTop := 0
	if currentBaseTop, ok := currentNativeStateCallBase(luaState); ok {
		// C function 内只能从当前调用帧可见栈顶捕获 upvalue，不能穿透外层 Go VM 栈。
		baseTop = currentBaseTop
	}
	visibleTop := state.StackTop() - baseTop
	if visibleTop < 0 {
		// 调用帧基址损坏时保持 no-op，避免继续读写错误栈区间。
		return
	}
	if upvalueCount > visibleTop {
		// 调用方没有提供足够 upvalue 时保持 no-op，后续 api_check 阶段再收口错误。
		return
	}
	upvalues := make([]runtime.Value, upvalueCount)
	firstUpvalueIndex := baseTop + visibleTop - upvalueCount + 1
	for upvalueIndex := 0; upvalueIndex < upvalueCount; upvalueIndex++ {
		// upvalue 按栈上原始顺序捕获，lua_upvalueindex(1) 对应最先压入的值。
		upvalues[upvalueIndex] = state.ValueAt(firstUpvalueIndex + upvalueIndex)
	}
	nativeLuaRestoreStackTop(state, baseTop+visibleTop-upvalueCount)
	closure := runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// C function 调用时重新校验 handle，避免 State 关闭后继续执行本机函数。
		return nativeLuaCallCFunction(luaState, function, upvalues, args...)
	})
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoClosureWithUpvalues{
		Function: closure,
		Upvalues: upvalues,
	}))
}

// nativeLuaCallCFunction 在 Go closure 调用期间建立 Lua C API 可见的临时栈。
func nativeLuaCallCFunction(luaState unsafe.Pointer, function unsafe.Pointer, upvalues []runtime.Value, args ...runtime.Value) ([]runtime.Value, error) {
	// 每次调用都重新查表，确保已关闭 State 不会继续进入本机函数。
	state, ok := lookupNativeStateHandle(luaState)
	if !ok {
		// State 已失效时返回 Lua runtime error，让上层 protected call 能捕获。
		return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrClosedState.Error()), runtime.ErrClosedState)
	}
	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, upvalues) {
		// 无法建立调用帧时不能进入 C 函数，否则 C API 正索引会读到错误槽位。
		return nil, runtime.NewRuntimeError(runtime.StringValue(runtime.ErrClosedState.Error()), runtime.ErrClosedState)
	}
	defer popNativeStateCallFrame(luaState)
	for argumentIndex := range args {
		// C API 约定函数入口栈上从 1 开始排列实参；这里把 Go 调用参数临时压入 State 栈。
		if err := state.Push(args[argumentIndex]); err != nil {
			// 压参失败时恢复到调用前栈顶，避免半写入参数污染后续调用。
			nativeLuaRestoreStackTop(state, baseTop)
			return nil, err
		}
	}

	resultCount := nativeLuaInvokeCFunction(luaState, function)
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// lua_error/luaL_error 通过 pending error 传回 Go 边界；此时忽略 C 返回数量并按 Lua error 传播。
		nativeLuaRestoreStackTop(state, baseTop)
		return nil, runtime.RaiseError(errorObject)
	}
	if resultCount < 0 {
		// C 函数返回负数不是 Lua 5.3 合法结果数量，按运行时错误处理。
		nativeLuaRestoreStackTop(state, baseTop)
		return nil, runtime.NewRuntimeError(runtime.StringValue("native C function returned negative result count"), runtime.ErrLuaError)
	}
	currentTop := state.StackTop()
	availableResults := currentTop - baseTop
	if resultCount > availableResults {
		// 防御损坏 C 模块：不能从栈上读取不存在的返回值。
		nativeLuaRestoreStackTop(state, baseTop)
		return nil, runtime.NewRuntimeError(runtime.StringValue("native C function returned more results than values on stack"), runtime.ErrLuaError)
	}
	results := make([]runtime.Value, resultCount)
	firstResultIndex := currentTop - resultCount + 1
	for resultIndex := 0; resultIndex < resultCount; resultIndex++ {
		// 返回值位于当前栈顶的连续后缀，顺序与 Lua C API 返回语义一致。
		results[resultIndex] = state.ValueAt(firstResultIndex + resultIndex)
	}
	nativeLuaRestoreStackTop(state, baseTop)
	return results, nil
}

// nativeLuaRestoreStackTop 恢复 native C function 调用前的 Go State 栈顶。
func nativeLuaRestoreStackTop(state *runtime.State, top int) {
	// C function wrapper 只会在调用期间向栈顶追加参数或返回值，因此恢复时只需要弹出后缀。
	for state != nil && state.StackTop() > top {
		// Pop 失败表示 State 已关闭或栈损坏；恢复路径不能继续扩大副作用。
		if _, err := state.Pop(); err != nil {
			// 恢复失败时停止循环，由后续 State 生命周期检查暴露问题。
			return
		}
	}
}

// lua_pushcclosure 导出 Lua 5.3 C API C closure 压栈入口。
//
//export lua_pushcclosure
func lua_pushcclosure(luaState *C.lua_State, function C.lua_CFunction, upvalueCount C.int) {
	// C API 入口只做类型转换；当前阶段只支持 nup==0 的 C function wrapper。
	nativeLuaPushCClosure(unsafe.Pointer(luaState), unsafe.Pointer(function), int(upvalueCount))
}

// lua_pushcfunction 导出 Lua 5.3 C API C function 压栈入口。
//
//export lua_pushcfunction
func lua_pushcfunction(luaState *C.lua_State, function C.lua_CFunction) {
	// lua_pushcfunction 等价于 lua_pushcclosure(L, f, 0)。
	nativeLuaPushCClosure(unsafe.Pointer(luaState), unsafe.Pointer(function), 0)
}
