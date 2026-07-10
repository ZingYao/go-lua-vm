//go:build lua53 || (!with_events && !with_all && (with_switch || with_continue || with_const))

package lua

import (
	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	// GluaEventFunctionCall 表示 Lua 函数调用进入事件。
	GluaEventFunctionCall = "function.call"
	// GluaEventFunctionReturn 表示 Lua 函数正常返回事件。
	GluaEventFunctionReturn = "function.return"
	// GluaEventFunctionError 表示 Lua 函数错误退出事件。
	GluaEventFunctionError = "function.error"
	// GluaEventFunctionExit 表示 Lua 函数离开事件，成功和失败都会触发。
	GluaEventFunctionExit = "function.exit"
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
)

// registerGluaEventGlobals 在未编译事件能力时保持无操作。
func registerGluaEventGlobals(state *State) {
	// 当前构建不包含 glua events，不向全局表注册任何事件 API。
}

// triggerGluaFunctionLifecycleEvent 在未编译事件能力时保持无操作。
func triggerGluaFunctionLifecycleEvent(state *State, closure *runtime.LuaClosure, eventName string, payload runtime.Value) error {
	// 当前构建不包含 glua events，函数生命周期事件直接跳过。
	return nil
}

// drainGluaEventQueue 在未编译事件能力时保持无操作。
func drainGluaEventQueue(state *State) error {
	// 当前构建不包含 glua events，没有异步队列需要消费。
	return nil
}

// gluaValueListTable 在未编译事件能力时返回 nil 占位值。
func gluaValueListTable(values []runtime.Value) runtime.Value {
	// 当前构建不包含 glua events，调用方不会使用返回值构造事件 payload。
	return runtime.NilValue()
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
