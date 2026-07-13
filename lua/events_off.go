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

// setGluaProgressEventTraceHook 在关闭 Event 构建中返回统一不可用错误。
func setGluaProgressEventTraceHook(state *State, hook ProgressEventTraceHook) error {
	// 无 Event registry 时不能保存宿主观测 hook。
	return ErrGluaEventsUnavailable
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

// setGluaProgressEventForSource 在未编译事件能力时返回不可用错误。
func setGluaProgressEventForSource(state *State, source string, eventName string, callback runtime.Value, options ProgressEventOptions) (int64, error) {
	// 条件编译关闭 Event 后不创建监听器。
	return 0, ErrGluaEventsUnavailable
}

// dispatchGluaProgressEventFromGo 在未编译事件能力时返回不可用错误。
func dispatchGluaProgressEventFromGo(state *State, source string, eventName string, payload runtime.Value, async bool) error {
	// 条件编译关闭 Event 后不触发回调。
	return ErrGluaEventsUnavailable
}

// flushGluaProgressEventsFromGo 在未编译事件能力时返回不可用错误。
func flushGluaProgressEventsFromGo(state *State) (int, error) {
	// 条件编译关闭 Event 后没有可消费队列。
	return 0, ErrGluaEventsUnavailable
}

// removeGluaProgressEventFromGo 在未编译事件能力时返回不可用错误。
func removeGluaProgressEventFromGo(state *State, eventID int64) (bool, error) {
	// 条件编译关闭 Event 后没有监听器可删除。
	return false, ErrGluaEventsUnavailable
}

// setGluaProgressEventMutedFromGo 在未编译事件能力时返回不可用错误。
func setGluaProgressEventMutedFromGo(state *State, eventID int64, muted bool) (bool, error) {
	// 条件编译关闭 Event 后没有监听器可静音。
	return false, ErrGluaEventsUnavailable
}

// setGluaProgressEventCallbackFromGo 在未编译事件能力时返回不可用错误。
func setGluaProgressEventCallbackFromGo(state *State, eventID int64, callback runtime.Value) (bool, error) {
	// 条件编译关闭 Event 后没有 callback 可替换。
	return false, ErrGluaEventsUnavailable
}

// setGluaProgressEventOptionsFromGo 在未编译事件能力时返回不可用错误。
func setGluaProgressEventOptionsFromGo(state *State, eventID int64, options ProgressEventOptions) (bool, error) {
	// 条件编译关闭 Event 后没有配置可更新。
	return false, ErrGluaEventsUnavailable
}

// getGluaProgressEventFromGo 在未编译事件能力时返回不可用错误。
func getGluaProgressEventFromGo(state *State, eventID int64) (runtime.Value, error) {
	// 条件编译关闭 Event 后没有监听器快照。
	return runtime.NilValue(), ErrGluaEventsUnavailable
}

// listGluaProgressEventsFromGo 在未编译事件能力时返回不可用错误。
func listGluaProgressEventsFromGo(state *State, source string) (runtime.Value, error) {
	// 条件编译关闭 Event 后没有监听器统计。
	return runtime.NilValue(), ErrGluaEventsUnavailable
}

// clearGluaProgressEventsFromGo 在未编译事件能力时返回不可用错误。
func clearGluaProgressEventsFromGo(state *State, source string, eventName string, group string) (int, error) {
	// 条件编译关闭 Event 后没有监听器可清理。
	return 0, ErrGluaEventsUnavailable
}

// setGluaProgressEventGroupMutedFromGo 在未编译事件能力时返回不可用错误。
func setGluaProgressEventGroupMutedFromGo(state *State, source string, group string, muted bool) (int, error) {
	// 条件编译关闭 Event 后没有分组可静音。
	return 0, ErrGluaEventsUnavailable
}
