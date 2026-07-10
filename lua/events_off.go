//go:build lua53 || (!with_events && !with_all && (with_switch || with_continue || with_const))

package lua

import (
	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	// GluaEventProgressLine 表示当前文件执行到新的源码行。
	GluaEventProgressLine = "progress.line"
	// GluaEventProgressStart 表示当前文件内的 Lua 代码块开始执行。
	GluaEventProgressStart = "progress.start"
	// GluaEventProgressEnd 表示当前文件内的 Lua 代码块正常执行完成。
	GluaEventProgressEnd = "progress.end"
	// GluaEventProgressError 表示当前文件内的 Lua 代码块因错误退出。
	GluaEventProgressError = "progress.error"
	// GluaEventProgressExit 表示当前文件内的 Lua 代码块离开，成功和失败都会触发。
	GluaEventProgressExit = "progress.exit"
	// GluaEventProgressFunctionCall 表示当前文件内即将发生一次函数调用。
	GluaEventProgressFunctionCall = "progress.function_call"
	// GluaEventProgressFunctionReturn 表示当前文件内一次函数调用正常返回。
	GluaEventProgressFunctionReturn = "progress.function_return"
	// GluaEventProgressFunctionError 表示当前文件内一次函数调用错误退出。
	GluaEventProgressFunctionError = "progress.function_error"
	// GluaEventProgressFunctionExit 表示当前文件内一次函数调用离开，成功和失败都会触发。
	GluaEventProgressFunctionExit = "progress.function_exit"
)

// registerGluaEventGlobals 在未编译事件能力时保持无操作。
func registerGluaEventGlobals(state *State) {
	// 当前构建不包含 glua events，不向全局表注册任何事件 API。
}

// drainGluaEventQueue 在未编译事件能力时保持无操作。
func drainGluaEventQueue(state *State) error {
	// 当前构建不包含 glua events，没有异步队列需要消费。
	return nil
}

// gluaHasAnyEvent 在未编译事件能力时固定返回 false。
func gluaHasAnyEvent(state *State) bool {
	// 当前构建不包含 glua events，VM 不需要启用事件精确帧同步。
	return false
}

// triggerGluaProgressLineEvent 在未编译事件能力时保持无操作。
func triggerGluaProgressLineEvent(state *State, proto *bytecode.Proto, line int64) error {
	// 当前构建不包含 glua events，进度事件直接跳过。
	return nil
}

// triggerGluaProgressLifecycleEvent 在未编译事件能力时保持无操作。
func triggerGluaProgressLifecycleEvent(state *State, proto *bytecode.Proto, eventName string, payload runtime.Value) error {
	// 当前构建不包含 glua events，进度生命周期事件直接跳过。
	return nil
}

// triggerGluaProgressFunctionEvent 在未编译事件能力时保持无操作。
func triggerGluaProgressFunctionEvent(state *State, callerProto *bytecode.Proto, eventName string, payload runtime.Value, details runtime.Value, callee runtime.Value, calleeName string, calleeNameWhat string, callPC int) error {
	// 当前构建不包含 glua events，函数调用进度事件直接跳过。
	return nil
}

// newGluaFunctionEventDetails 在未编译事件能力时返回 nil 占位值。
func newGluaFunctionEventDetails(arguments []Value, results []Value, callErr error, durationNs int64) runtime.Value {
	// 调用路径由 gluaHasAnyEvent=false 跳过，该函数仅满足条件编译符号完整性。
	return runtime.NilValue()
}

// beginProtectedGluaFunctionEvent 在未编译事件能力时返回无操作收尾函数。
func beginProtectedGluaFunctionEvent(state *State, function Value, arguments []Value) (func([]Value, error) error, error) {
	// protected call 保持原始执行路径，不产生事件副作用。
	return func([]Value, error) error { return nil }, nil
}
