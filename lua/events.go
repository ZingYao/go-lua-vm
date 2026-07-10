//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import (
	"sync"

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

// gluaEventCallback 保存一个 Lua 侧事件回调。
type gluaEventCallback struct {
	// value 保存用户注册的函数值。
	value Value
	// async 表示事件触发时只入队，等待 VM 安全点执行。
	async bool
}

// gluaEventTask 保存一次待执行的异步事件回调。
type gluaEventTask struct {
	// callback 保存事件触发时匹配到的回调。
	callback gluaEventCallback
	// context 保存传给 callback 的上下文表。
	context Value
}

// gluaEventRegistry 保存单个 State 的 glua 自定义事件注册表。
type gluaEventRegistry struct {
	// mu 保护事件表和异步队列，避免宿主并发调用 API 时产生 map 竞争。
	mu sync.Mutex
	// functionEvents 按 Lua closure 指针保存函数级事件回调。
	functionEvents map[*runtime.LuaClosure]map[string][]gluaEventCallback
	// progressEvents 按 Proto.Source 保存文件级事件回调。
	progressEvents map[string]map[string][]gluaEventCallback
	// queue 保存等待 VM 安全点执行的异步回调。
	queue []gluaEventTask
	// dispatchDepth 标记当前是否正在执行事件回调，用于屏蔽事件回调内的事件重入。
	dispatchDepth int
}

var (
	// gluaEventRegistries 保存 State 到事件注册表的弱语义映射；State 关闭后由 Go GC 回收剩余回调值。
	gluaEventRegistries sync.Map
)

// registerGluaEventGlobals 注册 glua 自定义事件相关全局函数和常量表。
func registerGluaEventGlobals(state *State) {
	// 注册入口先校验 State，避免半初始化环境写入全局表。
	if state == nil || state.Globals() == nil {
		// 无效 State 没有可写入的全局环境。
		return
	}
	globals := state.Globals()
	eventsTable := runtime.NewTable()
	eventsTable.RawSetString("function_call", runtime.StringValue(GluaEventFunctionCall))
	eventsTable.RawSetString("function_return", runtime.StringValue(GluaEventFunctionReturn))
	eventsTable.RawSetString("function_error", runtime.StringValue(GluaEventFunctionError))
	eventsTable.RawSetString("function_exit", runtime.StringValue(GluaEventFunctionExit))
	eventsTable.RawSetString("progress_line", runtime.StringValue(GluaEventProgressLine))
	eventsTable.RawSetString("progress_start", runtime.StringValue(GluaEventProgressStart))
	eventsTable.RawSetString("progress_end", runtime.StringValue(GluaEventProgressEnd))
	eventsTable.RawSetString("progress_error", runtime.StringValue(GluaEventProgressError))
	eventsTable.RawSetString("progress_exit", runtime.StringValue(GluaEventProgressExit))
	globals.RawSetString("events", runtime.ReferenceValue(runtime.KindTable, eventsTable))
	globals.RawSetString("setFunctionEvent", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 同步函数事件注册到当前 Lua 调用帧。
		return setGluaFunctionEvent(state, false, args...)
	})))
	globals.RawSetString("setFunctionEventAsync", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 异步函数事件注册到当前 Lua 调用帧。
		return setGluaFunctionEvent(state, true, args...)
	})))
	globals.RawSetString("setProgressEvent", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 同步进度事件注册到当前源码文件。
		return setGluaProgressEvent(state, false, args...)
	})))
	globals.RawSetString("setProgressEventAsync", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 异步进度事件注册到当前源码文件。
		return setGluaProgressEvent(state, true, args...)
	})))
	globals.RawSetString("callFunctionEvent", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 自定义函数事件同步触发当前 Lua 调用帧上的回调。
		return callGluaFunctionEvent(state, false, args...)
	})))
	globals.RawSetString("callFunctionEventAsync", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 自定义函数事件异步触发当前 Lua 调用帧上的回调。
		return callGluaFunctionEvent(state, true, args...)
	})))
	globals.RawSetString("callProgressEvent", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 自定义文件进度事件同步触发当前源码文件上的回调。
		return callGluaProgressEvent(state, false, args...)
	})))
	globals.RawSetString("callProgressEventAsync", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 自定义文件进度事件异步触发当前源码文件上的回调。
		return callGluaProgressEvent(state, true, args...)
	})))
}

// gluaRegistryForState 返回指定 State 的事件注册表。
func gluaRegistryForState(state *State) *gluaEventRegistry {
	// nil State 没有关联事件注册表。
	if state == nil {
		// 调用方会继续按无事件处理。
		return nil
	}
	if existing, ok := gluaEventRegistries.Load(state); ok {
		// 已有注册表直接复用，确保注册和触发共享同一份状态。
		registry, _ := existing.(*gluaEventRegistry)
		return registry
	}
	registry := &gluaEventRegistry{}
	actual, _ := gluaEventRegistries.LoadOrStore(state, registry)
	registered, _ := actual.(*gluaEventRegistry)
	return registered
}

// setGluaFunctionEvent 实现 setFunctionEvent 和 setFunctionEventAsync。
func setGluaFunctionEvent(state *State, async bool, args ...runtime.Value) ([]runtime.Value, error) {
	// 注册函数事件前先校验参数形态。
	eventName, callback, err := parseGluaEventRegistrationArgs("setFunctionEvent", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	closure := currentGluaCallerClosure(state)
	if closure == nil {
		// 函数事件必须绑定当前 Lua 函数，顶层或 Go 回调中调用没有稳定函数作用域。
		return nil, runtime.RaiseError(runtime.StringValue("setFunctionEvent must be called inside a Lua function"))
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表说明 State 不可用。
		return nil, runtime.ErrNilState
	}
	registry.mu.Lock()
	if registry.functionEvents == nil {
		// 首次注册函数事件时创建映射。
		registry.functionEvents = make(map[*runtime.LuaClosure]map[string][]gluaEventCallback)
	}
	eventsByName := registry.functionEvents[closure]
	if eventsByName == nil {
		// 首次为该 closure 注册事件时创建事件名映射。
		eventsByName = make(map[string][]gluaEventCallback)
		registry.functionEvents[closure] = eventsByName
	}
	eventsByName[eventName] = []gluaEventCallback{{value: callback, async: async}}
	registry.mu.Unlock()
	return nil, nil
}

// setGluaProgressEvent 实现 setProgressEvent 和 setProgressEventAsync。
func setGluaProgressEvent(state *State, async bool, args ...runtime.Value) ([]runtime.Value, error) {
	// 注册文件事件前先校验参数形态。
	eventName, callback, err := parseGluaEventRegistrationArgs("setProgressEvent", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	source := currentGluaCallerSource(state)
	if source == "" {
		// 当前源码名缺失时无法建立文件级作用域。
		return nil, runtime.RaiseError(runtime.StringValue("setProgressEvent requires a current Lua source"))
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表说明 State 不可用。
		return nil, runtime.ErrNilState
	}
	registry.mu.Lock()
	if registry.progressEvents == nil {
		// 首次注册进度事件时创建映射。
		registry.progressEvents = make(map[string]map[string][]gluaEventCallback)
	}
	eventsByName := registry.progressEvents[source]
	if eventsByName == nil {
		// 首次为该源码注册事件时创建事件名映射。
		eventsByName = make(map[string][]gluaEventCallback)
		registry.progressEvents[source] = eventsByName
	}
	eventsByName[eventName] = []gluaEventCallback{{value: callback, async: async}}
	registry.mu.Unlock()
	return nil, nil
}

// callGluaFunctionEvent 实现 callFunctionEvent 和 callFunctionEventAsync。
func callGluaFunctionEvent(state *State, forceAsync bool, args ...runtime.Value) ([]runtime.Value, error) {
	// 自定义函数事件至少需要事件名。
	eventName, payload, err := parseGluaCustomEventArgs("callFunctionEvent", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	closure := currentGluaCallerClosure(state)
	if closure == nil {
		// 函数事件必须由 Lua 函数内部触发。
		return nil, runtime.RaiseError(runtime.StringValue("callFunctionEvent must be called inside a Lua function"))
	}
	return dispatchGluaFunctionEvent(state, closure, eventName, payload, forceAsync)
}

// callGluaProgressEvent 实现 callProgressEvent 和 callProgressEventAsync。
func callGluaProgressEvent(state *State, forceAsync bool, args ...runtime.Value) ([]runtime.Value, error) {
	// 自定义文件事件至少需要事件名。
	eventName, payload, err := parseGluaCustomEventArgs("callProgressEvent", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	source := currentGluaCallerSource(state)
	if source == "" {
		// 当前源码名缺失时无法建立文件级作用域。
		return nil, runtime.RaiseError(runtime.StringValue("callProgressEvent requires a current Lua source"))
	}
	return dispatchGluaProgressEvent(state, source, eventName, payload, forceAsync)
}

// parseGluaEventRegistrationArgs 解析事件注册函数参数。
func parseGluaEventRegistrationArgs(functionName string, args []runtime.Value) (string, runtime.Value, error) {
	// 事件注册必须提供事件名和回调函数。
	if len(args) < 2 {
		// 参数不足时返回 Lua 风格错误对象。
		return "", runtime.NilValue(), runtime.RaiseError(runtime.StringValue(functionName + " expects event and callback"))
	}
	if args[0].Kind != runtime.KindString {
		// 事件名必须是字符串，events.xxx 常量也是字符串值。
		return "", runtime.NilValue(), runtime.RaiseError(runtime.StringValue("bad argument #1 to '" + functionName + "' (string expected)"))
	}
	if args[1].Kind != runtime.KindGoClosure && args[1].Kind != runtime.KindLuaClosure {
		// 回调必须是可调用函数值。
		return "", runtime.NilValue(), runtime.RaiseError(runtime.StringValue("bad argument #2 to '" + functionName + "' (function expected)"))
	}
	return args[0].String, args[1], nil
}

// parseGluaCustomEventArgs 解析自定义事件触发参数。
func parseGluaCustomEventArgs(functionName string, args []runtime.Value) (string, runtime.Value, error) {
	// 自定义事件必须提供事件名，payload 可省略。
	if len(args) < 1 {
		// 参数不足时返回 Lua 风格错误对象。
		return "", runtime.NilValue(), runtime.RaiseError(runtime.StringValue(functionName + " expects event"))
	}
	if args[0].Kind != runtime.KindString {
		// 事件名必须是字符串，events.xxx 常量也是字符串值。
		return "", runtime.NilValue(), runtime.RaiseError(runtime.StringValue("bad argument #1 to '" + functionName + "' (string expected)"))
	}
	payload := runtime.NilValue()
	if len(args) >= 2 {
		// 第二个参数作为 payload 原样放入 context。
		payload = args[1]
	}
	return args[0].String, payload, nil
}

// dispatchGluaFunctionEvent 触发指定 closure 上的函数事件。
func dispatchGluaFunctionEvent(state *State, closure *runtime.LuaClosure, eventName string, payload runtime.Value, forceAsync bool) ([]runtime.Value, error) {
	// 缺少 closure 或 registry 时没有事件可触发。
	registry := gluaRegistryForState(state)
	if registry == nil || closure == nil {
		// 无事件环境时保持无返回值。
		return nil, nil
	}
	callbacks := registry.functionCallbacks(closure, eventName)
	if len(callbacks) == 0 {
		// 当前函数没有注册该事件。
		return nil, nil
	}
	context := buildGluaEventContext(state, "function", eventName, payload, forceAsync, closure, closure.Proto)
	return registry.dispatchCallbacks(state, callbacks, context, forceAsync)
}

// dispatchGluaProgressEvent 触发指定源码文件上的进度事件。
func dispatchGluaProgressEvent(state *State, source string, eventName string, payload runtime.Value, forceAsync bool) ([]runtime.Value, error) {
	// 缺少 source 或 registry 时没有事件可触发。
	registry := gluaRegistryForState(state)
	if registry == nil || source == "" {
		// 无事件环境时保持无返回值。
		return nil, nil
	}
	callbacks := registry.progressCallbacks(source, eventName)
	if len(callbacks) == 0 {
		// 当前文件没有注册该事件。
		return nil, nil
	}
	context := buildGluaEventContext(state, "progress", eventName, payload, forceAsync, nil, currentGluaCallerProto(state))
	return registry.dispatchCallbacks(state, callbacks, context, forceAsync)
}

// functionCallbacks 返回函数事件回调快照。
func (registry *gluaEventRegistry) functionCallbacks(closure *runtime.LuaClosure, eventName string) []gluaEventCallback {
	// 读取回调前先校验 registry。
	if registry == nil {
		// nil 注册表没有回调。
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.dispatchDepth > 0 {
		// 事件回调执行期间屏蔽重入派发。
		return nil
	}
	eventsByName := registry.functionEvents[closure]
	if len(eventsByName) == 0 {
		// 该函数没有任何事件。
		return nil
	}
	callbacks := eventsByName[eventName]
	return append([]gluaEventCallback(nil), callbacks...)
}

// progressCallbacks 返回文件事件回调快照。
func (registry *gluaEventRegistry) progressCallbacks(source string, eventName string) []gluaEventCallback {
	// 读取回调前先校验 registry。
	if registry == nil {
		// nil 注册表没有回调。
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.dispatchDepth > 0 {
		// 事件回调执行期间屏蔽重入派发。
		return nil
	}
	eventsByName := registry.progressEvents[source]
	if len(eventsByName) == 0 {
		// 该源码没有任何事件。
		return nil
	}
	callbacks := eventsByName[eventName]
	return append([]gluaEventCallback(nil), callbacks...)
}

// dispatchCallbacks 执行或入队一组事件回调。
func (registry *gluaEventRegistry) dispatchCallbacks(state *State, callbacks []gluaEventCallback, context runtime.Value, forceAsync bool) ([]runtime.Value, error) {
	// 空回调列表不产生任何副作用。
	if registry == nil || len(callbacks) == 0 {
		// 无回调时保持无返回值。
		return nil, nil
	}
	for _, callback := range callbacks {
		// 异步回调只入队，等待 VM 安全点消费。
		if callback.async || forceAsync {
			setGluaContextAsync(context, true)
			registry.enqueue(callback, context)
			continue
		}
		setGluaContextAsync(context, false)
		if err := registry.callCallback(state, callback, context); err != nil {
			// 同步回调错误直接传播给当前 Lua 执行路径。
			return nil, err
		}
	}
	return nil, nil
}

// setGluaContextAsync 写入事件上下文的 async 标记。
func setGluaContextAsync(context runtime.Value, async bool) {
	// context 由 buildGluaEventContext 创建，只有 table 值才能更新字段。
	if context.Kind != runtime.KindTable {
		// 非 table 上下文忽略更新，避免异常路径 panic。
		return
	}
	table, ok := context.Ref.(*runtime.Table)
	if !ok || table == nil {
		// 损坏 table 引用无法更新。
		return
	}
	table.RawSetString("async", runtime.BooleanValue(async))
}

// enqueue 将异步事件任务追加到注册表队列。
func (registry *gluaEventRegistry) enqueue(callback gluaEventCallback, context runtime.Value) {
	// 入队前先校验 registry。
	if registry == nil {
		// nil 注册表无法保存任务。
		return
	}
	registry.mu.Lock()
	registry.queue = append(registry.queue, gluaEventTask{callback: callback, context: context})
	registry.mu.Unlock()
}

// drainGluaEventQueue 在 VM 安全点消费当前 State 的异步事件队列。
func drainGluaEventQueue(state *State) error {
	// 安全点消费异步队列，避免 goroutine 直接重入 Lua VM。
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 无注册表时没有异步任务。
		return nil
	}
	for {
		// 每轮只取当前队列快照，允许回调继续追加下一批任务。
		tasks := registry.takeQueuedTasks()
		if len(tasks) == 0 {
			// 队列耗尽后结束消费。
			return nil
		}
		for _, task := range tasks {
			// 异步回调错误在安全点传播，仍不会阻塞事件触发点本身。
			if err := registry.callCallback(state, task.callback, task.context); err != nil {
				return err
			}
		}
	}
}

// takeQueuedTasks 取出当前异步任务快照。
func (registry *gluaEventRegistry) takeQueuedTasks() []gluaEventTask {
	// 取队列前先校验 registry。
	if registry == nil {
		// nil 注册表没有任务。
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.dispatchDepth > 0 || len(registry.queue) == 0 {
		// 回调执行期间不递归消费队列，避免异步任务嵌套失控。
		return nil
	}
	tasks := append([]gluaEventTask(nil), registry.queue...)
	for index := range registry.queue {
		// 清空旧槽位，避免保留回调和上下文引用。
		registry.queue[index] = gluaEventTask{}
	}
	registry.queue = registry.queue[:0]
	return tasks
}

// callCallback 调用单个事件回调。
func (registry *gluaEventRegistry) callCallback(state *State, callback gluaEventCallback, context runtime.Value) error {
	// 事件回调只能在有效 State 上执行。
	if state == nil {
		// nil State 无法调用 Lua 回调。
		return runtime.ErrNilState
	}
	registry.mu.Lock()
	if registry.dispatchDepth > 0 {
		// 重入事件派发直接跳过，避免 callback 递归触发自身。
		registry.mu.Unlock()
		return nil
	}
	registry.dispatchDepth++
	registry.mu.Unlock()
	defer func() {
		registry.mu.Lock()
		registry.dispatchDepth--
		registry.mu.Unlock()
	}()
	_, err := callWithDebugName(state, callback.value, "", "event", context)
	return err
}

// buildGluaEventContext 构造传给 Lua callback 的上下文表。
func buildGluaEventContext(state *State, kind string, eventName string, payload runtime.Value, async bool, closure *runtime.LuaClosure, proto *bytecode.Proto) runtime.Value {
	// context 使用普通 Lua table，便于脚本按字段读取。
	context := runtime.NewTable()
	context.RawSetString("event", runtime.StringValue(eventName))
	context.RawSetString("kind", runtime.StringValue(kind))
	context.RawSetString("async", runtime.BooleanValue(async))
	context.RawSetString("payload", payload)
	if proto != nil {
		// Proto 元数据存在时补充源码和定义行范围。
		context.RawSetString("source", runtime.StringValue(proto.Source))
		context.RawSetString("lineDefined", runtime.IntegerValue(int64(proto.LineDefined)))
		context.RawSetString("lastLineDefined", runtime.IntegerValue(int64(proto.LastLineDefined)))
	}
	if frame, ok := currentGluaCallerFrame(state); ok {
		// 当前 Lua 帧存在时补充调用点名称和行号。
		context.RawSetString("functionName", runtime.StringValue(frame.Name))
		context.RawSetString("nameWhat", runtime.StringValue(frame.NameWhat))
		context.RawSetString("line", runtime.IntegerValue(currentGluaLine(frame)))
	}
	if closure != nil {
		// 函数级事件暴露被观察的函数值。
		context.RawSetString("function", runtime.ReferenceValue(runtime.KindLuaClosure, closure))
	}
	if locals := currentGluaLocalTable(state); locals != nil {
		// locals 是当前 PC 下的局部变量快照，供 callback 只读观察。
		context.RawSetString("locals", runtime.ReferenceValue(runtime.KindTable, locals))
	}
	if upvalues := gluaUpvalueTable(closure); upvalues != nil {
		// upvalues 是被观察函数当前 upvalue 快照。
		context.RawSetString("upvalues", runtime.ReferenceValue(runtime.KindTable, upvalues))
	}
	return runtime.ReferenceValue(runtime.KindTable, context)
}

// currentGluaCallerFrame 返回最近的 Lua 调用帧。
func currentGluaCallerFrame(state *State) (runtime.CallFrame, bool) {
	// 从 traceback 快照中跳过当前 Go closure 帧，寻找最近 Lua 帧。
	if state == nil {
		// nil State 没有调用帧。
		return runtime.CallFrame{}, false
	}
	for _, frame := range state.TracebackFrames() {
		if frame.Kind != runtime.CallFrameKindLua {
			// Go helper 帧不作为 glua 事件的业务调用方。
			continue
		}
		return frame, true
	}
	return runtime.CallFrame{}, false
}

// currentGluaCallerClosure 返回最近 Lua 调用帧中的 closure。
func currentGluaCallerClosure(state *State) *runtime.LuaClosure {
	// 先定位最近 Lua 帧，再解析函数值负载。
	frame, ok := currentGluaCallerFrame(state)
	if !ok || frame.Function.Kind != runtime.KindLuaClosure {
		// 没有 Lua 帧时无法绑定函数事件。
		return nil
	}
	closure, _ := frame.Function.Ref.(*runtime.LuaClosure)
	return closure
}

// currentGluaCallerProto 返回最近 Lua 调用帧中的 Proto。
func currentGluaCallerProto(state *State) *bytecode.Proto {
	// Proto 从当前 Lua closure 中取得。
	closure := currentGluaCallerClosure(state)
	if closure == nil {
		// 缺少 closure 时无源码元数据。
		return nil
	}
	return closure.Proto
}

// currentGluaCallerSource 返回最近 Lua 调用帧的源码名。
func currentGluaCallerSource(state *State) string {
	// Source 由当前 Lua Proto 提供。
	proto := currentGluaCallerProto(state)
	if proto == nil {
		// 缺少 Proto 时没有文件作用域。
		return ""
	}
	return proto.Source
}

// currentGluaLine 返回调用帧当前 PC 对应的源码行。
func currentGluaLine(frame runtime.CallFrame) int64 {
	// 只有 Lua closure 帧才能从 Proto.LineInfo 读取源码行。
	if frame.Function.Kind != runtime.KindLuaClosure {
		// 非 Lua 帧没有源码行。
		return 0
	}
	closure, _ := frame.Function.Ref.(*runtime.LuaClosure)
	if closure == nil || closure.Proto == nil {
		// 损坏 closure 没有源码行。
		return 0
	}
	if frame.CurrentPC < 0 || frame.CurrentPC >= len(closure.Proto.LineInfo) {
		// PC 越界时无法读取行号。
		return 0
	}
	return int64(closure.Proto.LineInfo[frame.CurrentPC])
}

// currentGluaLocalTable 返回当前活动 VM 的局部变量快照表。
func currentGluaLocalTable(state *State) *runtime.Table {
	// 事件上下文只暴露当前 Lua VM 的具名 local 快照。
	if state == nil {
		// nil State 没有活动 VM。
		return nil
	}
	vm, ok := state.ActiveVMAtLevel(1)
	if !ok {
		// 没有活动 VM 时无法读取 local。
		return nil
	}
	snapshots := vm.ActiveLocalSnapshots()
	if len(snapshots) == 0 {
		// 当前 PC 没有可见具名 local。
		return nil
	}
	table := runtime.NewTable()
	for _, snapshot := range snapshots {
		// 每个 local 以变量名作为 key 写入快照值。
		table.RawSetString(snapshot.Name, snapshot.Value)
	}
	return table
}

// gluaUpvalueTable 返回 Lua closure 的 upvalue 快照表。
func gluaUpvalueTable(closure *runtime.LuaClosure) *runtime.Table {
	// upvalue 快照依赖 closure 和 Proto 调试名。
	if closure == nil || closure.Proto == nil || len(closure.Proto.Upvalues) == 0 {
		// 没有 upvalue 元数据时不创建空表。
		return nil
	}
	table := runtime.NewTable()
	for index, upvalueDesc := range closure.Proto.Upvalues {
		// 优先读取共享 cell 当前值，缺失时退回闭包快照。
		value := runtime.NilValue()
		if index < len(closure.UpvalueCells) && closure.UpvalueCells[index] != nil {
			// cell 保存运行期共享 upvalue 的当前值。
			value = closure.UpvalueCells[index].Value()
		} else if index < len(closure.Upvalues) {
			// 无 cell 的旧路径读取创建时快照。
			value = closure.Upvalues[index]
		}
		name := upvalueDesc.Name
		if name == "" {
			// 匿名 upvalue 用 1-based 序号兜底。
			table.RawSetInteger(int64(index+1), value)
			continue
		}
		table.RawSetString(name, value)
	}
	return table
}

// triggerGluaFunctionLifecycleEvent 触发函数生命周期事件。
func triggerGluaFunctionLifecycleEvent(state *State, closure *runtime.LuaClosure, eventName string, payload runtime.Value) error {
	// 生命周期事件由 VM 调用路径触发。
	_, err := dispatchGluaFunctionEvent(state, closure, eventName, payload, false)
	return err
}

// triggerGluaProgressLineEvent 按源码行触发文件进度事件。
func triggerGluaProgressLineEvent(state *State, proto *bytecode.Proto, line int64) error {
	// 只有注册了 progress.line 的源码才需要构造事件。
	if proto == nil || line <= 0 {
		// 缺少源码或行号时不触发。
		return nil
	}
	payload := runtime.NewTable()
	payload.RawSetString("line", runtime.IntegerValue(line))
	_, err := dispatchGluaProgressEvent(state, proto.Source, GluaEventProgressLine, runtime.ReferenceValue(runtime.KindTable, payload), false)
	return err
}

// triggerGluaProgressLifecycleEvent 按源码文件触发进度生命周期事件。
func triggerGluaProgressLifecycleEvent(state *State, proto *bytecode.Proto, eventName string, payload runtime.Value) error {
	// 进度生命周期事件与 progress.line 使用同一个源码文件作用域。
	if proto == nil {
		// 缺少 Proto 时没有可匹配的 Source。
		return nil
	}
	_, err := dispatchGluaProgressEvent(state, proto.Source, eventName, payload, false)
	return err
}

// gluaValueListTable 将多返回值快照转换为 Lua 数组表。
func gluaValueListTable(values []runtime.Value) runtime.Value {
	// payload 使用 1-based 数组布局，符合 Lua 多返回值查看习惯。
	table := runtime.NewTable()
	for index, value := range values {
		// 返回值按原始顺序写入数组下标。
		table.RawSetInteger(int64(index+1), value)
	}
	return runtime.ReferenceValue(runtime.KindTable, table)
}

// gluaHasAnyEvent 判断当前 State 是否存在任意 glua 事件注册或异步任务。
func gluaHasAnyEvent(state *State) bool {
	// VM 用该判断决定是否关闭 superinstruction 热路径以保留事件精度。
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 无注册表视为无事件。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return len(registry.functionEvents) > 0 || len(registry.progressEvents) > 0 || len(registry.queue) > 0
}
