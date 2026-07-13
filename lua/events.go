//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import (
	"fmt"
	"sort"
	"sync"
	"time"

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
	// maxGluaEventDurationMs 防止毫秒配置转换为纳秒时发生 int64 溢出。
	maxGluaEventDurationMs = int64(^uint64(0)>>1) / int64(time.Millisecond)
)

// gluaEventCallback 保存一个 Lua 侧事件回调。
type gluaEventCallback struct {
	// id 是单个事件注册在当前 State 内的稳定标识。
	id int64
	// value 保存用户注册的函数值。
	value Value
	// source 保存监听器注册时所属的源码文件。
	source string
	// eventName 保存监听器订阅的事件名称。
	eventName string
	// async 表示事件触发时只入队，等待 VM 安全点执行。
	async bool
	// muted 表示该事件注册临时静音，不参与派发但保留配置。
	muted bool
	// configValue 保存用户传入的原始配置表，事件上下文会原样暴露给 callback。
	configValue runtime.Value
	// filter 保存 progress.function_* 事件使用的函数名白名单和黑名单。
	filter gluaEventFilter
	// options 保存监听器治理、队列和错误处理配置。
	options gluaEventOptions
	// reservedCount 保存已经占用派发额度但尚未执行完成的同步或异步任务数。
	reservedCount int64
	// dispatchCount 保存回调实际执行次数。
	dispatchCount int64
	// errorCount 保存回调执行失败次数。
	errorCount int64
	// droppedCount 保存该监听器因队列溢出被丢弃的任务数。
	droppedCount int64
	// totalDurationNs 保存回调累计执行耗时。
	totalDurationNs int64
	// suppressedCount 保存因限次、节流、采样或防抖合并而未独立执行的触发次数。
	suppressedCount int64
	// throttledCount 保存被 throttleMs 时间窗抑制的触发次数。
	throttledCount int64
	// sampledOutCount 保存被确定性 sampleRate 过滤的触发次数。
	sampledOutCount int64
	// debouncedCount 保存被 debounceMs 合并进已有异步任务的触发次数。
	debouncedCount int64
	// lastAcceptedAt 保存最近一次通过节流检查的时间，并保留 Go 单调时钟部分。
	lastAcceptedAt time.Time
	// sampleBalance 保存确定性采样累加器，避免随机源导致测试和事件重放不稳定。
	sampleBalance float64
}

// gluaEventOptions 保存单个监听器的可选治理配置。
type gluaEventOptions struct {
	// scope 表示监听当前 State 全部 source 或仅监听注册 source。
	scope ProgressEventScope
	// once 表示监听器成功占用一次派发后自动删除。
	once bool
	// maxCalls 限制监听器最多实际执行次数，0 表示不限制。
	maxCalls int64
	// priority 越大越先派发；相同优先级保持注册顺序。
	priority int64
	// group 保存调用方定义的监听器分组名称。
	group string
	// queueLimit 限制该监听器在异步队列中的待处理任务数，0 表示不限制。
	queueLimit int
	// overflow 定义队列满时的处理方式。
	overflow string
	// onError 定义同步和异步回调错误的处理方式。
	onError string
	// mutable 表示上下文中的 locals/upvalues 是否允许保留可变引用。
	mutable bool
	// throttleNs 限制两次接受触发之间的最短纳秒间隔。
	throttleNs int64
	// debounceNs 延迟异步任务，并在时间窗内只保留最新上下文。
	debounceNs int64
	// sampleRate 使用 0 到 1 的确定性比例控制触发采样。
	sampleRate float64
}

// gluaEventFilter 保存函数调用进度事件的过滤配置。
type gluaEventFilter struct {
	// whitelist 非空时只允许匹配到名称或函数引用的调用触发。
	whitelist gluaEventFilterSet
	// blacklist 非空时跳过匹配到名称或函数引用的调用。
	blacklist gluaEventFilterSet
}

// gluaEventFilterSet 保存一组可按名称或 closure identity 匹配的函数选择器。
type gluaEventFilterSet struct {
	// names 保存兼容旧配置的函数名称选择器。
	names map[string]struct{}
	// functions 保存 Lua 侧直接传入的函数变量，使用 RawEqual 比较运行期身份。
	functions []runtime.Value
}

// gluaEventTask 保存一次待执行的异步事件回调。
type gluaEventTask struct {
	// callback 保存事件触发时匹配到的回调。
	callback *gluaEventCallback
	// context 保存传给 callback 的上下文表。
	context Value
	// readyAt 保存防抖任务最早允许执行的时间，零值表示立即可执行。
	readyAt time.Time
}

// gluaEventRegistry 保存单个 State 的 glua 自定义事件注册表。
type gluaEventRegistry struct {
	// mu 保护事件表和异步队列，避免宿主并发调用 API 时产生 map 竞争。
	mu sync.Mutex
	// nextID 保存下一次事件注册要分配的 id。
	nextID int64
	// eventsByID 保存 id 到事件回调的索引，供删除、静音和配置修改使用。
	eventsByID map[int64]*gluaEventCallback
	// progressEvents 按 Proto.Source 保存文件级事件回调。
	progressEvents map[string]map[string][]*gluaEventCallback
	// roots 保存挂入 glua.event 的 Lua 强引用表，防止仅由 Go 注册表持有的 callback/config 被 Lua GC 回收。
	roots *runtime.Table
	// queue 保存等待 VM 安全点执行的异步回调。
	queue []gluaEventTask
	// dispatchDepth 标记当前是否正在执行事件回调，用于屏蔽事件回调内的事件重入。
	dispatchDepth int
	// sequence 保存当前 State 内事件上下文的单调序号。
	sequence int64
	// droppedTasks 保存异步队列累计丢弃任务数。
	droppedTasks int64
	// callbackErrors 保存事件回调累计错误数。
	callbackErrors int64
	// suppressedEvents 保存因可靠性策略未独立执行的累计触发数。
	suppressedEvents int64
	// debouncedTasks 保存被合并到已有异步任务的累计触发数。
	debouncedTasks int64
	// traceSequence 保存当前 State 分配过的调用链编号。
	traceSequence int64
	// currentTraceID 保存当前代码执行链使用的编号。
	currentTraceID int64
	// traceDepth 保存当前根调用链开始时的 Lua 帧深度。
	traceDepth int64
	// lastEventByDepth 保存每个调用深度最近的事件编号，供 parentEventId 关联。
	lastEventByDepth map[int64]int64
}

var (
	// gluaEventRegistries 保存 State 到事件注册表的强引用映射；State.Close 钩子负责主动删除并清空内容。
	gluaEventRegistries sync.Map
	// gluaEventContextFields 定义事件上下文允许复制的固定字段，避免 RawNext 遗漏 RawSetString 写入的键。
	gluaEventContextFields = []string{
		"event", "kind", "async", "payload", "timestamp",
		"sequence", "eventId", "traceId", "parentEventId", "durationNs", "depth", "args", "results", "error",
		"source", "lineDefined", "lastLineDefined",
		"functionName", "nameWhat", "line", "function", "locals", "upvalues",
		"callee", "calleeName", "calleeNameWhat", "calleeType", "callPC",
		"calleeSource", "calleeLineDefined", "calleeLastLineDefined", "calleeUpvalues",
		"id", "config", "group", "priority", "once", "maxCalls",
		"throttleMs", "debounceMs", "sampleRate", "scope",
	}
)

// registerGluaEventGlobals 注册 glua.event 命名空间及其事件方法和常量。
func registerGluaEventGlobals(state *State) {
	// 注册入口先校验 State，避免半初始化环境写入全局表。
	if state == nil || state.Globals() == nil {
		// 无效 State 没有可写入的全局环境。
		return
	}
	globals := state.Globals()
	gluaTable := gluaNamespaceTable(globals)
	if gluaTable == nil {
		// 已有非 table 的 glua 全局值时不覆盖宿主变量，避免静默破坏其语义。
		return
	}
	eventsTable := runtime.NewTable()
	eventsTable.RawSetString("progress_line", runtime.StringValue(GluaEventProgressLine))
	eventsTable.RawSetString("progress_start", runtime.StringValue(GluaEventProgressStart))
	eventsTable.RawSetString("progress_end", runtime.StringValue(GluaEventProgressEnd))
	eventsTable.RawSetString("progress_error", runtime.StringValue(GluaEventProgressError))
	eventsTable.RawSetString("progress_exit", runtime.StringValue(GluaEventProgressExit))
	eventsTable.RawSetString("progress_function_call", runtime.StringValue(GluaEventProgressFunctionCall))
	eventsTable.RawSetString("progress_function_return", runtime.StringValue(GluaEventProgressFunctionReturn))
	eventsTable.RawSetString("progress_function_error", runtime.StringValue(GluaEventProgressFunctionError))
	eventsTable.RawSetString("progress_function_exit", runtime.StringValue(GluaEventProgressFunctionExit))
	eventTable := runtime.NewTable()
	registry := gluaRegistryForState(state)
	if registry != nil {
		// 注册表根表挂入全局命名空间，使 Lua GC 能扫描回调和配置值。
		registry.mu.Lock()
		registry.roots = runtime.NewTable()
		eventTable.RawSetString("_roots", runtime.ReferenceValue(runtime.KindTable, registry.roots))
		registry.mu.Unlock()
	}
	eventTable.RawSetString("events", runtime.ReferenceValue(runtime.KindTable, eventsTable))
	eventTable.RawSetString("setProgress", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 同步进度事件注册到当前源码文件。
		return setGluaProgressEvent(state, false, args...)
	})))
	eventTable.RawSetString("setProgressAsync", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 异步进度事件注册到当前源码文件。
		return setGluaProgressEvent(state, true, args...)
	})))
	eventTable.RawSetString("callProgress", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 自定义文件进度事件同步触发当前源码文件上的回调。
		return callGluaProgressEvent(state, false, args...)
	})))
	eventTable.RawSetString("callProgressAsync", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 自定义文件进度事件异步触发当前源码文件上的回调。
		return callGluaProgressEvent(state, true, args...)
	})))
	eventTable.RawSetString("remove", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 按 id 删除一个已注册事件。
		return removeGluaEvent(state, args...)
	})))
	eventTable.RawSetString("setMuted", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 按 id 设置事件静音状态。
		return setGluaEventMuted(state, args...)
	})))
	eventTable.RawSetString("setCallback", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 按 id 替换监听器回调函数。
		return setGluaEventCallback(state, args...)
	})))
	eventTable.RawSetString("setConfig", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 按 id 替换事件配置。
		return setGluaEventConfig(state, args...)
	})))
	eventTable.RawSetString("getConfig", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 按 id 返回当前事件配置。
		return getGluaEventConfig(state, args...)
	})))
	eventTable.RawSetString("eventList", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回当前源码文件的事件和监听器统计快照。
		return listGluaEvents(state, args...)
	})))
	eventTable.RawSetString("get", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回指定监听器的状态快照。
		return getGluaEvent(state, args...)
	})))
	eventTable.RawSetString("clear", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 清理当前源码文件的全部监听器或指定事件监听器。
		return clearGluaEvents(state, args...)
	})))
	eventTable.RawSetString("setGroupMuted", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 批量设置当前源码文件指定分组的静音状态。
		return setGluaEventGroupMuted(state, args...)
	})))
	eventTable.RawSetString("removeGroup", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 删除当前源码文件指定分组的监听器。
		return removeGluaEventGroup(state, args...)
	})))
	eventTable.RawSetString("flush", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 立即消费当前 State 的异步事件队列。
		return flushGluaEvents(state, args...)
	})))
	eventTable.RawSetString("stats", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回当前源码文件事件统计和队列状态。
		return gluaEventStats(state, args...)
	})))
	gluaTable.RawSetString("event", runtime.ReferenceValue(runtime.KindTable, eventTable))
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
	actual, loaded := gluaEventRegistries.LoadOrStore(state, registry)
	registered, _ := actual.(*gluaEventRegistry)
	if !loaded {
		// 仅映射创建者登记关闭钩子，避免并发 LoadOrStore 产生重复清理回调。
		state.AddCloseHook(func() {
			// State.Close 从全局索引删除注册表，并释放回调、配置和异步上下文引用。
			closeGluaEventRegistry(state, registry)
		})
	}
	return registered
}

// closeGluaEventRegistry 删除并清空指定 State 的事件注册表。
//
// state 和 registry 必须来自同一次 gluaRegistryForState 创建；函数没有返回值，重复调用保持幂等。
// 清理会取消全部待执行异步任务，并释放 Lua callback/config 根引用。
func closeGluaEventRegistry(state *State, registry *gluaEventRegistry) {
	// 先按 State 与 registry 的组合删除，避免误删并发替换后的注册表。
	if state == nil || registry == nil {
		// 无效参数没有可释放的事件资源。
		return
	}
	if !gluaEventRegistries.CompareAndDelete(state, registry) {
		// 映射已删除或已替换时，当前调用不再操作共享索引。
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for index := range registry.queue {
		// 清空队列槽位，释放 callback 和上下文强引用。
		registry.queue[index] = gluaEventTask{}
	}
	registry.queue = nil
	registry.eventsByID = nil
	registry.progressEvents = nil
	registry.roots = nil
	registry.dispatchDepth = 0
	registry.currentTraceID = 0
	registry.lastEventByDepth = nil
}

// setGluaProgressEvent 实现 glua.event.setProgress 和 glua.event.setProgressAsync。
func setGluaProgressEvent(state *State, async bool, args ...runtime.Value) ([]runtime.Value, error) {
	// 注册文件事件前先校验参数形态。
	eventName, callback, err := parseGluaEventRegistrationArgs("glua.event.setProgress", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	configValue, filter, err := parseGluaEventConfigArg("glua.event.setProgress", args)
	if err != nil {
		// 配置表错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	options, err := parseGluaEventOptions(configValue)
	if err != nil {
		// 治理配置错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	if options.debounceNs > 0 && !async {
		// 防抖需要延迟和合并任务，只能用于不会阻塞触发点的异步监听器。
		return nil, runtime.RaiseError(runtime.StringValue("event config debounceMs requires setProgressAsync"))
	}
	source := currentGluaCallerSource(state)
	if source == "" {
		// 当前源码名缺失时无法记录监听器注册来源，也无法支持 file scope。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.setProgress requires a current Lua source"))
	}
	eventID, err := registerGluaProgressEvent(state, source, eventName, callback, configValue, filter, options, async)
	if err != nil {
		// 注册表或 State 错误按 Lua error 语义返回。
		return nil, err
	}
	return []runtime.Value{runtime.IntegerValue(eventID)}, nil
}

// registerGluaProgressEvent 把已经解析的监听器写入 State 注册表。
//
// source、eventName 和 callback 必须有效；options.scope 必须已经归一化。返回新事件 ID。
func registerGluaProgressEvent(state *State, source string, eventName string, callback runtime.Value, configValue runtime.Value, filter gluaEventFilter, options gluaEventOptions, async bool) (int64, error) {
	// 注册入口集中维护索引、排序和 GC 根，供 Lua 与 Go API 共同复用。
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表说明 State 不可用。
		return 0, runtime.ErrNilState
	}
	registry.mu.Lock()
	if registry.progressEvents == nil {
		// 首次注册进度事件时创建映射。
		registry.progressEvents = make(map[string]map[string][]*gluaEventCallback)
	}
	if registry.eventsByID == nil {
		// 首次注册事件时创建 id 索引。
		registry.eventsByID = make(map[int64]*gluaEventCallback)
	}
	eventsByName := registry.progressEvents[source]
	if eventsByName == nil {
		// 首次为该源码注册事件时创建事件名映射。
		eventsByName = make(map[string][]*gluaEventCallback)
		registry.progressEvents[source] = eventsByName
	}
	registry.nextID++
	callbackEntry := &gluaEventCallback{
		id: registry.nextID, value: callback, source: source, eventName: eventName,
		async: async, configValue: configValue, filter: filter, options: options,
	}
	eventsByName[eventName] = append(eventsByName[eventName], callbackEntry)
	sort.SliceStable(eventsByName[eventName], func(left int, right int) bool {
		// 高优先级先执行；相同优先级保留注册顺序。
		leftCallback := eventsByName[eventName][left]
		rightCallback := eventsByName[eventName][right]
		if leftCallback.options.priority != rightCallback.options.priority {
			// priority 不同时按降序排列。
			return leftCallback.options.priority > rightCallback.options.priority
		}
		return leftCallback.id < rightCallback.id
	})
	registry.eventsByID[callbackEntry.id] = callbackEntry
	registry.rootEvent(callbackEntry)
	registry.mu.Unlock()
	return callbackEntry.id, nil
}

// setGluaProgressEventForSource 从 Go API 为明确 source 注册进度事件。
func setGluaProgressEventForSource(state *State, source string, eventName string, callback runtime.Value, apiOptions ProgressEventOptions) (int64, error) {
	// Go 入口先验证 State 生命周期和 Event 初始化状态，再复用 Lua 配置解析器。
	if state == nil {
		// nil State 无法保存监听器。
		return 0, runtime.ErrNilState
	}
	if err := state.CheckContext(); err != nil {
		// 已关闭或被取消的 State 不接受新监听器。
		return 0, err
	}
	if !state.Options().GluaEventsEnabled {
		// 当前 State 显式关闭了 Event 能力。
		return 0, ErrGluaEventsUnavailable
	}
	if source == "" || eventName == "" {
		// source 和事件名共同决定匹配边界，均不能为空。
		return 0, runtime.RaiseError(runtime.StringValue("progress event source and event must not be empty"))
	}
	if !gluaEventFilterFunction(callback) {
		// callback 必须能被 Lua VM 调用。
		return 0, ErrExpectedCallable
	}
	registry := gluaRegistryForState(state)
	if registry == nil || registry.roots == nil {
		// OpenLibs 负责创建 GC 根；未初始化时不能安全保存 callback。
		return 0, ErrGluaEventsNotOpen
	}
	configValue := apiOptions.Config
	if configValue.Kind == 0 {
		// Value 零值在公开 Go API 中表示未提供配置。
		configValue = runtime.NilValue()
	}
	filter, err := parseGluaEventFilter(configValue)
	if err != nil {
		// 过滤配置错误直接返回给 Go 调用方。
		return 0, err
	}
	options, err := parseGluaEventOptions(configValue)
	if err != nil {
		// 治理配置错误直接返回给 Go 调用方。
		return 0, err
	}
	options.scope = apiOptions.Scope
	if options.debounceNs > 0 && !apiOptions.Async {
		// 防抖依赖异步队列，不能用于同步 Go callback。
		return 0, runtime.RaiseError(runtime.StringValue("event config debounceMs requires an asynchronous listener"))
	}
	return registerGluaProgressEvent(state, source, eventName, callback, configValue, filter, options, apiOptions.Async)
}

// dispatchGluaProgressEventFromGo 从 Go API 为明确 source 触发进度事件。
func dispatchGluaProgressEventFromGo(state *State, source string, eventName string, payload runtime.Value, async bool) error {
	// Go 入口保持与注册入口相同的 State 和参数约束。
	if state == nil {
		// nil State 没有可触发的运行时。
		return runtime.ErrNilState
	}
	if err := state.CheckContext(); err != nil {
		// 已关闭或取消的 State 不能执行回调。
		return err
	}
	if !state.Options().GluaEventsEnabled {
		// 当前 State 没有启用 Event 能力。
		return ErrGluaEventsUnavailable
	}
	if source == "" || eventName == "" {
		// 显式来源和事件名不能为空。
		return runtime.RaiseError(runtime.StringValue("progress event source and event must not be empty"))
	}
	_, err := dispatchGluaProgressEvent(state, source, eventName, payload, async)
	return err
}

// flushGluaProgressEventsFromGo 从 Go API 强制消费当前 State 的异步事件队列。
func flushGluaProgressEventsFromGo(state *State) (int, error) {
	// flush 前确认 State 可用且 Event 能力已开启。
	if state == nil {
		// nil State 没有异步队列。
		return 0, runtime.ErrNilState
	}
	if err := state.CheckContext(); err != nil {
		// 已关闭或取消的 State 不能执行队列任务。
		return 0, err
	}
	if !state.Options().GluaEventsEnabled {
		// 当前 State 没有 Event 队列。
		return 0, ErrGluaEventsUnavailable
	}
	return drainGluaEventQueueCount(state, true)
}

// callGluaProgressEvent 实现 glua.event.callProgress 和 glua.event.callProgressAsync。
func callGluaProgressEvent(state *State, forceAsync bool, args ...runtime.Value) ([]runtime.Value, error) {
	// 自定义文件事件至少需要事件名。
	eventName, payload, err := parseGluaCustomEventArgs("glua.event.callProgress", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	source := currentGluaCallerSource(state)
	if source == "" {
		// 当前源码名缺失时无法建立文件级作用域。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.callProgress requires a current Lua source"))
	}
	return dispatchGluaProgressEvent(state, source, eventName, payload, forceAsync)
}

// removeGluaEvent 实现 glua.event.remove。
func removeGluaEvent(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 删除事件只需要 id，返回 boolean 表示是否找到并删除。
	eventID, err := parseGluaEventIDArg("glua.event.remove", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表时视为未找到事件。
		return []runtime.Value{runtime.BooleanValue(false)}, nil
	}
	removed := registry.removeEvent(eventID)
	return []runtime.Value{runtime.BooleanValue(removed)}, nil
}

// setGluaEventMuted 实现 glua.event.setMuted。
func setGluaEventMuted(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 静音事件需要 id 和 boolean 状态。
	eventID, muted, err := parseGluaEventMutedArgs(args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表时视为未找到事件。
		return []runtime.Value{runtime.BooleanValue(false)}, nil
	}
	updated := registry.setEventMuted(eventID, muted)
	return []runtime.Value{runtime.BooleanValue(updated)}, nil
}

// setGluaEventCallback 实现 glua.event.setCallback。
func setGluaEventCallback(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 修改回调需要事件 id 和新的函数值。
	if len(args) != 2 {
		// 参数数量不符时返回明确 Lua 错误。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.setCallback expects event id and callback"))
	}
	eventID, err := parseGluaEventIDArg("glua.event.setCallback", args[:1])
	if err != nil {
		// id 错误直接传播。
		return nil, err
	}
	if !gluaEventFilterFunction(args[1]) {
		// callback 必须是 Lua 或 Go function。
		return nil, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'glua.event.setCallback' (function expected)"))
	}
	registry := gluaRegistryForState(state)
	updated := registry != nil && registry.setEventCallback(eventID, args[1])
	return []runtime.Value{runtime.BooleanValue(updated)}, nil
}

// setGluaEventConfig 实现 glua.event.setConfig。
func setGluaEventConfig(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 修改事件配置需要 id 和配置 table。
	eventID, configValue, filter, err := parseGluaEventConfigUpdateArgs(args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表时视为未找到事件。
		return []runtime.Value{runtime.BooleanValue(false)}, nil
	}
	options, err := parseGluaEventOptions(configValue)
	if err != nil {
		// 治理配置错误直接传播。
		return nil, err
	}
	if options.debounceNs > 0 {
		// 更新为防抖配置前确认原监听器属于异步类型。
		async, found := registry.eventAsync(eventID)
		if found && !async {
			// 同步监听器不能延迟执行，调用方应重新注册异步监听器。
			return nil, runtime.RaiseError(runtime.StringValue("event config debounceMs requires an asynchronous listener"))
		}
	}
	updated := registry.setEventConfig(eventID, configValue, filter, options)
	return []runtime.Value{runtime.BooleanValue(updated)}, nil
}

// getGluaEventConfig 实现 glua.event.getConfig。
func getGluaEventConfig(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 读取配置只需要事件 id，未找到时返回 nil。
	eventID, err := parseGluaEventIDArg("glua.event.getConfig", args)
	if err != nil {
		// 参数错误按 Lua error 语义返回给调用方。
		return nil, err
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表时视为未找到配置。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{registry.eventConfig(eventID)}, nil
}

// listGluaEvents 实现 glua.event.eventList，返回当前源码文件的监听器统计。
func listGluaEvents(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// eventList 不接收参数，避免调用方误以为可以越过当前文件作用域查询其他源码。
	if len(args) > 0 {
		// 多余参数按 Lua 风格参数错误返回。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.eventList expects no arguments"))
	}
	source := currentGluaCallerSource(state)
	if source == "" {
		// 当前源码名缺失时无法确定统计作用域。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.eventList requires a current Lua source"))
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 缺少注册表时返回当前文件的空统计快照。
		return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, newGluaEventListTable(source, nil))}, nil
	}
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, registry.eventList(source))}, nil
}

// getGluaEvent 实现 glua.event.get。
func getGluaEvent(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 单监听器查询只接受一个事件 id。
	eventID, err := parseGluaEventIDArg("glua.event.get", args)
	if err != nil {
		// id 参数错误直接传播。
		return nil, err
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 无注册表时返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{registry.eventSnapshot(eventID)}, nil
}

// clearGluaEvents 实现 glua.event.clear。
func clearGluaEvents(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// clear 可省略事件名，也可只清理当前文件中的指定事件。
	if len(args) > 1 || (len(args) == 1 && args[0].Kind != runtime.KindString) {
		// 参数形态错误时返回 Lua 风格错误。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.clear expects optional event name"))
	}
	source := currentGluaCallerSource(state)
	if source == "" {
		// 缺少调用源码时不能越过文件作用域清理。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.clear requires a current Lua source"))
	}
	eventName := ""
	if len(args) == 1 {
		// 指定名称时仅删除该事件的监听器。
		eventName = args[0].String
	}
	registry := gluaRegistryForState(state)
	removed := 0
	if registry != nil {
		// 注册表存在时执行文件作用域批量删除。
		removed = registry.clearEvents(source, eventName, "")
	}
	return []runtime.Value{runtime.IntegerValue(int64(removed))}, nil
}

// setGluaEventGroupMuted 实现 glua.event.setGroupMuted。
func setGluaEventGroupMuted(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 分组静音要求 group 字符串和 boolean 状态。
	if len(args) != 2 || args[0].Kind != runtime.KindString || args[1].Kind != runtime.KindBoolean {
		// 参数错误不进行部分更新。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.setGroupMuted expects group and muted"))
	}
	source := currentGluaCallerSource(state)
	registry := gluaRegistryForState(state)
	updated := 0
	if registry != nil && source != "" {
		// 仅更新当前文件中同组监听器。
		updated = registry.setGroupMuted(source, args[0].String, args[1].Bool)
	}
	return []runtime.Value{runtime.IntegerValue(int64(updated))}, nil
}

// removeGluaEventGroup 实现 glua.event.removeGroup。
func removeGluaEventGroup(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// 删除分组只接受一个 group 字符串。
	if len(args) != 1 || args[0].Kind != runtime.KindString {
		// 参数错误不删除监听器。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.removeGroup expects group"))
	}
	source := currentGluaCallerSource(state)
	registry := gluaRegistryForState(state)
	removed := 0
	if registry != nil && source != "" {
		// group 作为第三个筛选条件执行批量删除。
		removed = registry.clearEvents(source, "", args[0].String)
	}
	return []runtime.Value{runtime.IntegerValue(int64(removed))}, nil
}

// flushGluaEvents 实现 glua.event.flush。
func flushGluaEvents(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// flush 不接收参数，返回本次实际执行任务数。
	if len(args) != 0 {
		// 参数存在时拒绝执行，避免误以为可刷新其他 State。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.flush expects no arguments"))
	}
	count, err := drainGluaEventQueueCount(state, true)
	if err != nil {
		// propagate 策略的异步错误由 flush 返回。
		return nil, err
	}
	return []runtime.Value{runtime.IntegerValue(int64(count))}, nil
}

// gluaEventStats 实现 glua.event.stats。
func gluaEventStats(state *State, args ...runtime.Value) ([]runtime.Value, error) {
	// stats 与 eventList 一样只查询当前源码文件。
	if len(args) != 0 {
		// 不允许传 source，防止越过文件隔离。
		return nil, runtime.RaiseError(runtime.StringValue("glua.event.stats expects no arguments"))
	}
	return listGluaEvents(state)
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

// parseGluaEventConfigArg 解析事件注册的可选配置表。
func parseGluaEventConfigArg(functionName string, args []runtime.Value) (runtime.Value, gluaEventFilter, error) {
	// 第三个参数缺省时使用 nil 配置，事件上下文仍会暴露 nil。
	if len(args) < 3 || args[2].IsNil() {
		// 没有配置表时不启用函数名过滤。
		return runtime.NilValue(), gluaEventFilter{}, nil
	}
	if args[2].Kind != runtime.KindTable {
		// 配置只接受 table，避免字符串等类型被误解释为白名单。
		return runtime.NilValue(), gluaEventFilter{}, runtime.RaiseError(runtime.StringValue("bad argument #3 to '" + functionName + "' (table expected)"))
	}
	filter, err := parseGluaEventFilter(args[2])
	if err != nil {
		// 配置表内容错误沿用 Lua error 语义。
		return runtime.NilValue(), gluaEventFilter{}, err
	}
	return args[2], filter, nil
}

// parseGluaEventIDArg 解析事件 id 参数。
func parseGluaEventIDArg(functionName string, args []runtime.Value) (int64, error) {
	// id 必须由 setProgressEvent 返回，当前用 Lua integer 表达。
	if len(args) < 1 {
		// 参数不足时返回 Lua 风格错误对象。
		return 0, runtime.RaiseError(runtime.StringValue(functionName + " expects event id"))
	}
	if args[0].Kind != runtime.KindInteger {
		// id 必须是整数，避免浮点精度损失。
		return 0, runtime.RaiseError(runtime.StringValue("bad argument #1 to '" + functionName + "' (integer expected)"))
	}
	return args[0].Integer, nil
}

// parseGluaEventMutedArgs 解析 setEventMuted 参数。
func parseGluaEventMutedArgs(args []runtime.Value) (int64, bool, error) {
	// 先读取 id，再读取 boolean 静音状态。
	eventID, err := parseGluaEventIDArg("glua.event.setMuted", args)
	if err != nil {
		// id 错误直接返回。
		return 0, false, err
	}
	if len(args) < 2 {
		// 缺少 muted 参数时返回 Lua 风格错误。
		return 0, false, runtime.RaiseError(runtime.StringValue("glua.event.setMuted expects event id and muted"))
	}
	if args[1].Kind != runtime.KindBoolean {
		// muted 必须明确为 boolean。
		return 0, false, runtime.RaiseError(runtime.StringValue("bad argument #2 to 'glua.event.setMuted' (boolean expected)"))
	}
	return eventID, args[1].Bool, nil
}

// parseGluaEventConfigUpdateArgs 解析 setEventConfig 参数。
func parseGluaEventConfigUpdateArgs(args []runtime.Value) (int64, runtime.Value, gluaEventFilter, error) {
	// 先读取 id，再读取新的配置 table。
	eventID, err := parseGluaEventIDArg("glua.event.setConfig", args)
	if err != nil {
		// id 错误直接返回。
		return 0, runtime.NilValue(), gluaEventFilter{}, err
	}
	if len(args) < 2 {
		// 缺少配置表时返回 Lua 风格错误。
		return 0, runtime.NilValue(), gluaEventFilter{}, runtime.RaiseError(runtime.StringValue("glua.event.setConfig expects event id and config"))
	}
	configArgs := []runtime.Value{runtime.StringValue(""), runtime.NilValue(), args[1]}
	configValue, filter, err := parseGluaEventConfigArg("glua.event.setConfig", configArgs)
	if err != nil {
		// 配置表错误直接返回。
		return 0, runtime.NilValue(), gluaEventFilter{}, err
	}
	return eventID, configValue, filter, nil
}

// parseGluaEventFilter 从配置表中解析函数名称或函数引用白名单和黑名单。
func parseGluaEventFilter(configValue runtime.Value) (gluaEventFilter, error) {
	// 配置表已经由调用方确认类型，这里只读取约定字段。
	configTable, _ := configValue.Ref.(*runtime.Table)
	if configTable == nil {
		// 损坏 table 引用视为无过滤配置。
		return gluaEventFilter{}, nil
	}
	whitelist, err := parseGluaEventFilterSet(firstGluaConfigValue(configTable, "whitelist", "whiteList", "include", "only"))
	if err != nil {
		// 白名单内容必须是名称或函数引用集合。
		return gluaEventFilter{}, err
	}
	blacklist, err := parseGluaEventFilterSet(firstGluaConfigValue(configTable, "blacklist", "blackList", "exclude", "ignore"))
	if err != nil {
		// 黑名单内容必须是名称或函数引用集合。
		return gluaEventFilter{}, err
	}
	return gluaEventFilter{whitelist: whitelist, blacklist: blacklist}, nil
}

// parseGluaEventOptions 从配置表解析监听器治理和异步队列选项。
func parseGluaEventOptions(configValue runtime.Value) (gluaEventOptions, error) {
	// 默认监听当前 State 全部 source，并保持持续监听、完整采样和回调错误向上传播。
	options := gluaEventOptions{scope: ProgressEventScopeRuntime, overflow: "drop_newest", onError: "propagate", sampleRate: 1}
	if configValue.IsNil() {
		// 未提供配置时直接返回兼容默认值。
		return options, nil
	}
	configTable, ok := configValue.Ref.(*runtime.Table)
	if !ok || configTable == nil {
		// 配置类型已经由上层校验，此处防御损坏引用。
		return options, runtime.RaiseError(runtime.StringValue("event config must be table"))
	}
	if value := configTable.RawGetString("scope"); !value.IsNil() {
		// scope 只接受 runtime 或 file，避免未知值意外扩大监听范围。
		if value.Kind != runtime.KindString || (value.String != string(ProgressEventScopeRuntime) && value.String != string(ProgressEventScopeFile)) {
			return options, runtime.RaiseError(runtime.StringValue("event config scope must be runtime or file"))
		}
		options.scope = ProgressEventScope(value.String)
	}
	if value := configTable.RawGetString("once"); !value.IsNil() {
		// once 只接受 boolean，避免字符串真值产生歧义。
		if value.Kind != runtime.KindBoolean {
			return options, runtime.RaiseError(runtime.StringValue("event config once must be boolean"))
		}
		options.once = value.Bool
	}
	if value := configTable.RawGetString("maxCalls"); !value.IsNil() {
		// maxCalls 使用非负整数，0 表示不限制。
		if value.Kind != runtime.KindInteger || value.Integer < 0 {
			return options, runtime.RaiseError(runtime.StringValue("event config maxCalls must be a non-negative integer"))
		}
		options.maxCalls = value.Integer
	}
	if options.once && options.maxCalls > 1 {
		// once 与大于一次的上限互相矛盾，拒绝模糊配置。
		return options, runtime.RaiseError(runtime.StringValue("event config once conflicts with maxCalls greater than 1"))
	}
	if value := configTable.RawGetString("priority"); !value.IsNil() {
		// priority 使用整数保证排序稳定。
		if value.Kind != runtime.KindInteger {
			return options, runtime.RaiseError(runtime.StringValue("event config priority must be integer"))
		}
		options.priority = value.Integer
	}
	if value := configTable.RawGetString("group"); !value.IsNil() {
		// group 为空字符串等同于未分组。
		if value.Kind != runtime.KindString {
			return options, runtime.RaiseError(runtime.StringValue("event config group must be string"))
		}
		options.group = value.String
	}
	if value := configTable.RawGetString("queueLimit"); !value.IsNil() {
		// 0 表示不限制，负数没有明确语义。
		if value.Kind != runtime.KindInteger || value.Integer < 0 {
			return options, runtime.RaiseError(runtime.StringValue("event config queueLimit must be a non-negative integer"))
		}
		options.queueLimit = int(value.Integer)
	}
	if value := configTable.RawGetString("overflow"); !value.IsNil() {
		// 溢出策略限定为三个稳定枚举值。
		if value.Kind != runtime.KindString || (value.String != "drop_oldest" && value.String != "drop_newest" && value.String != "error") {
			return options, runtime.RaiseError(runtime.StringValue("event config overflow must be drop_oldest, drop_newest, or error"))
		}
		options.overflow = value.String
	}
	if value := configTable.RawGetString("onError"); !value.IsNil() {
		// 错误策略同时适用于同步和异步回调，并兼容 throw/continue 自然别名。
		if value.Kind != runtime.KindString {
			return options, runtime.RaiseError(runtime.StringValue("event config onError must be propagate, ignore, mute, or remove"))
		}
		switch value.String {
		case "propagate", "throw":
			// throw 归一化为向当前执行路径传播。
			options.onError = "propagate"
		case "ignore", "continue":
			// continue 归一化为记录错误后继续执行。
			options.onError = "ignore"
		case "mute", "remove":
			// mute 和 remove 保留原值，由错误收尾路径治理监听器。
			options.onError = value.String
		default:
			// 未知策略不能静默降级。
			return options, runtime.RaiseError(runtime.StringValue("event config onError must be propagate, ignore, mute, or remove"))
		}
	}
	if value := configTable.RawGetString("mutable"); !value.IsNil() {
		// mutable 为后续上下文引用策略提供显式开关。
		if value.Kind != runtime.KindBoolean {
			return options, runtime.RaiseError(runtime.StringValue("event config mutable must be boolean"))
		}
		options.mutable = value.Bool
	}
	if value := configTable.RawGetString("throttleMs"); !value.IsNil() {
		// 节流时间使用非负整数毫秒，0 表示关闭。
		if value.Kind != runtime.KindInteger || value.Integer < 0 || value.Integer > maxGluaEventDurationMs {
			return options, runtime.RaiseError(runtime.StringValue("event config throttleMs must be a non-negative integer"))
		}
		options.throttleNs = value.Integer * int64(time.Millisecond)
	}
	if value := configTable.RawGetString("debounceMs"); !value.IsNil() {
		// 防抖时间使用非负整数毫秒，具体执行只允许异步监听器。
		if value.Kind != runtime.KindInteger || value.Integer < 0 || value.Integer > maxGluaEventDurationMs {
			return options, runtime.RaiseError(runtime.StringValue("event config debounceMs must be a non-negative integer"))
		}
		options.debounceNs = value.Integer * int64(time.Millisecond)
	}
	if value := configTable.RawGetString("sampleRate"); !value.IsNil() {
		// 采样率接受 integer 或 number，并限定在闭区间 0 到 1。
		switch value.Kind {
		case runtime.KindInteger:
			// 整数只可能合法表达 0 或 1。
			options.sampleRate = float64(value.Integer)
		case runtime.KindNumber:
			// 浮点数保留调用方给出的比例。
			options.sampleRate = value.Number
		default:
			// 其他 Lua 类型不执行隐式数值转换。
			return options, runtime.RaiseError(runtime.StringValue("event config sampleRate must be a number between 0 and 1"))
		}
		if options.sampleRate < 0 || options.sampleRate > 1 {
			// 越界比例没有稳定采样语义。
			return options, runtime.RaiseError(runtime.StringValue("event config sampleRate must be a number between 0 and 1"))
		}
	}
	return options, nil
}

// firstGluaConfigValue 按多个字段名读取第一个非 nil 配置值。
func firstGluaConfigValue(table *runtime.Table, names ...string) runtime.Value {
	// 多个别名按顺序匹配，便于 Lua 侧使用自然命名。
	if table == nil {
		// nil table 没有配置值。
		return runtime.NilValue()
	}
	for _, name := range names {
		// 找到第一个非 nil 字段就返回。
		value := table.RawGetString(name)
		if !value.IsNil() {
			// 非 nil 字段参与解析。
			return value
		}
	}
	return runtime.NilValue()
}

// parseGluaEventFilterSet 将 Lua table 转换为名称与函数引用选择器集合。
func parseGluaEventFilterSet(value runtime.Value) (gluaEventFilterSet, error) {
	// nil 表示没有启用该集合。
	if value.IsNil() {
		// 空集合不限制匹配。
		return gluaEventFilterSet{}, nil
	}
	if value.Kind != runtime.KindTable {
		// 白名单和黑名单字段必须是 table。
		return gluaEventFilterSet{}, runtime.RaiseError(runtime.StringValue("event config whitelist/blacklist must be table"))
	}
	sourceTable, _ := value.Ref.(*runtime.Table)
	if sourceTable == nil {
		// 损坏 table 引用视为空集合。
		return gluaEventFilterSet{}, nil
	}
	filterSet := gluaEventFilterSet{names: make(map[string]struct{})}
	currentKey := runtime.NilValue()
	for {
		// RawNext 覆盖数组式和 map 式两类配置。
		nextKey, nextValue, hasNext, err := sourceTable.RawNext(currentKey)
		if err != nil {
			// table 遍历错误按 Lua error 语义返回。
			return gluaEventFilterSet{}, err
		}
		if !hasNext {
			// 遍历完成后返回集合。
			break
		}
		if nextValue.Kind == runtime.KindString {
			// 数组式配置：{"print", "foo"}。
			filterSet.names[nextValue.String] = struct{}{}
		} else if gluaEventFilterFunction(nextValue) {
			// 数组式函数配置：{moduleA.run, moduleB.run}，按 closure identity 去重。
			filterSet.appendFunction(nextValue)
		} else if nextKey.Kind == runtime.KindString && (nextValue.Kind != runtime.KindBoolean || nextValue.Bool) {
			// map 式配置：{print=true, foo=true}，false 表示显式关闭该项。
			filterSet.names[nextKey.String] = struct{}{}
		} else if gluaEventFilterFunction(nextKey) && (nextValue.Kind != runtime.KindBoolean || nextValue.Bool) {
			// 函数也可作为 map key；boolean false 表示显式关闭该项。
			filterSet.appendFunction(nextKey)
		} else if nextValue.Kind != runtime.KindBoolean || nextValue.Bool {
			// 其他启用项无法形成稳定函数选择器，直接向调用方报告配置错误。
			return gluaEventFilterSet{}, runtime.RaiseError(runtime.StringValue("event config whitelist/blacklist entries must be strings or functions"))
		}
		currentKey = nextKey
	}
	return filterSet, nil
}

// gluaEventFilterFunction 判断 value 是否可作为事件函数身份选择器。
func gluaEventFilterFunction(value runtime.Value) bool {
	// Lua 和 Go closure 都是 Lua 侧可作为变量传入的函数值；当前函数进度事件只观察 Lua closure。
	return value.Kind == runtime.KindLuaClosure || value.Kind == runtime.KindGoClosure
}

// appendFunction 向过滤集合加入尚未存在的函数引用。
func (filterSet *gluaEventFilterSet) appendFunction(function runtime.Value) {
	// 精确引用身份用于区分同一 Proto 产生的不同 closure，同时不向 Lua 侧暴露宿主裸指针。
	if filterSet == nil || !gluaEventFilterFunction(function) {
		// nil 集合或非函数值不产生修改。
		return
	}
	for _, existing := range filterSet.functions {
		// 相同 closure 已存在时避免重复保存强引用。
		if gluaEventFunctionIdentityEqual(existing, function) {
			// 去重只影响配置内部存储，不改变匹配结果。
			return
		}
	}
	filterSet.functions = append(filterSet.functions, function)
}

// gluaEventFunctionIdentityEqual 判断两个函数值是否代表同一个运行期函数对象。
func gluaEventFunctionIdentityEqual(left runtime.Value, right runtime.Value) bool {
	// 不同函数类型不能共享同一运行期身份。
	if left.Kind != right.Kind {
		// Lua closure 与 Go closure 即使入口行为相同也不是同一对象。
		return false
	}
	if left.Kind == runtime.KindLuaClosure {
		// Lua closure 使用具体对象指针比较，避免 RawEqual 的 Proto/upvalue 等价规则合并独立实例。
		leftClosure, leftOK := left.Ref.(*runtime.LuaClosure)
		rightClosure, rightOK := right.Ref.(*runtime.LuaClosure)
		return leftOK && rightOK && leftClosure != nil && leftClosure == rightClosure
	}
	if left.Kind == runtime.KindGoClosure {
		// Go closure 当前不参与函数进度事件，但保留既有 RawEqual 作为配置去重语义。
		return left.RawEqual(right)
	}
	return false
}

// removeEvent 从注册表中删除指定 id。
func (registry *gluaEventRegistry) removeEvent(eventID int64) bool {
	// 删除需要同时清理 id 索引和 source/event 列表。
	if registry == nil {
		// nil 注册表没有事件可删。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	callback, ok := registry.eventsByID[eventID]
	if !ok || callback == nil {
		// 未找到 id 时返回 false。
		return false
	}
	delete(registry.eventsByID, eventID)
	queueWriteIndex := 0
	for _, task := range registry.queue {
		// 删除监听器时同步取消尚未执行的异步任务。
		if task.callback == callback {
			continue
		}
		registry.queue[queueWriteIndex] = task
		queueWriteIndex++
	}
	for clearIndex := queueWriteIndex; clearIndex < len(registry.queue); clearIndex++ {
		// 清理被取消任务持有的上下文引用。
		registry.queue[clearIndex] = gluaEventTask{}
	}
	registry.queue = registry.queue[:queueWriteIndex]
	if registry.roots != nil {
		// 删除注册时一并释放强引用，允许 callback 和配置表被 Lua GC 回收。
		registry.roots.RawSetInteger(eventID, runtime.NilValue())
	}
	for source, eventsByName := range registry.progressEvents {
		// 遍历所有源码作用域，删除匹配 id 的回调。
		for eventName, callbacks := range eventsByName {
			// 原地压缩回调列表，保持其他 id 的相对顺序。
			writeIndex := 0
			for _, candidate := range callbacks {
				// 非目标回调保留。
				if candidate == nil || candidate.id == eventID {
					continue
				}
				callbacks[writeIndex] = candidate
				writeIndex++
			}
			for clearIndex := writeIndex; clearIndex < len(callbacks); clearIndex++ {
				// 清理尾部旧指针，避免保留回调引用。
				callbacks[clearIndex] = nil
			}
			if writeIndex == 0 {
				// 当前事件名没有回调后删除事件项。
				delete(eventsByName, eventName)
				continue
			}
			eventsByName[eventName] = callbacks[:writeIndex]
		}
		if len(eventsByName) == 0 {
			// 当前源码没有任何事件后删除源码项。
			delete(registry.progressEvents, source)
		}
	}
	return true
}

// rootEvent 把回调和配置值保存在全局可达 table 中，供 Lua GC 标记。
func (registry *gluaEventRegistry) rootEvent(callback *gluaEventCallback) {
	// 调用方已持有 registry.mu；这里只写入同一注册表的根 table。
	if registry == nil || registry.roots == nil || callback == nil {
		// 未注册根表或 nil 回调时保持兼容，无需额外处理。
		return
	}
	root := runtime.NewTable()
	root.RawSetString("callback", callback.value)
	root.RawSetString("config", callback.configValue)
	registry.roots.RawSetInteger(callback.id, runtime.ReferenceValue(runtime.KindTable, root))
}

// setEventMuted 设置指定 id 的静音状态。
func (registry *gluaEventRegistry) setEventMuted(eventID int64, muted bool) bool {
	// 静音只修改回调状态，不改变注册顺序和配置。
	if registry == nil {
		// nil 注册表没有事件可改。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	callback := registry.eventsByID[eventID]
	if callback == nil {
		// 未找到 id 时返回 false。
		return false
	}
	callback.muted = muted
	return true
}

// setEventCallback 替换指定监听器的回调函数。
func (registry *gluaEventRegistry) setEventCallback(eventID int64, callbackValue runtime.Value) bool {
	// 修改回调时同步刷新 GC 根表，下一次派发立即使用新函数。
	if registry == nil {
		// nil 注册表没有事件可改。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	callback := registry.eventsByID[eventID]
	if callback == nil {
		// 已删除或不存在的 id 返回 false。
		return false
	}
	callback.value = callbackValue
	registry.rootEvent(callback)
	return true
}

// setEventConfig 替换指定 id 的配置表和解析后的过滤器。
func (registry *gluaEventRegistry) setEventConfig(eventID int64, configValue runtime.Value, filter gluaEventFilter, options gluaEventOptions) bool {
	// 配置更新会影响下一次事件派发。
	if registry == nil {
		// nil 注册表没有事件可改。
		return false
	}
	registry.mu.Lock()
	callback := registry.eventsByID[eventID]
	if callback == nil {
		// 未找到 id 时返回 false。
		registry.mu.Unlock()
		return false
	}
	callback.configValue = configValue
	callback.filter = filter
	callback.options = options
	callback.lastAcceptedAt = time.Time{}
	callback.sampleBalance = 0
	registry.rootEvent(callback)
	limit := callback.callLimit()
	if limit > 0 {
		// 收紧上限时取消超出剩余额度的最新异步任务。
		remaining := limit - callback.dispatchCount
		if remaining < 0 {
			// 已执行次数超过新上限时没有剩余额度。
			remaining = 0
		}
		registry.trimCallbackQueueLocked(callback, remaining)
	}
	callbacks := registry.progressEvents[callback.source][callback.eventName]
	sort.SliceStable(callbacks, func(left int, right int) bool {
		// 配置修改 priority 后立即重排，id 相对顺序保持稳定。
		if callbacks[left].options.priority != callbacks[right].options.priority {
			// priority 不同时按降序排列。
			return callbacks[left].options.priority > callbacks[right].options.priority
		}
		return callbacks[left].id < callbacks[right].id
	})
	limitReached := limit > 0 && callback.dispatchCount >= limit
	registry.mu.Unlock()
	if limitReached {
		// 新配置的执行上限已经耗尽时立即移除，避免留下永远不会触发的监听器。
		registry.removeEvent(eventID)
	}
	return true
}

// trimCallbackQueueLocked 把监听器待执行任务收紧到指定额度。
//
// 调用方必须持有 registry.mu；maximumReserved 不得为负。函数优先取消最新任务并更新抑制统计。
func (registry *gluaEventRegistry) trimCallbackQueueLocked(callback *gluaEventCallback, maximumReserved int64) {
	// 只有超出新额度时才需要修改队列。
	if registry == nil || callback == nil || callback.reservedCount <= maximumReserved {
		// 无超额任务时保持队列顺序。
		return
	}
	excess := callback.reservedCount - maximumReserved
	for index := len(registry.queue) - 1; index >= 0 && excess > 0; index-- {
		// 从队尾查找目标监听器，优先保留更早触发的任务。
		if registry.queue[index].callback != callback {
			// 其他监听器任务不能被当前配置更新取消。
			continue
		}
		copy(registry.queue[index:], registry.queue[index+1:])
		registry.queue[len(registry.queue)-1] = gluaEventTask{}
		registry.queue = registry.queue[:len(registry.queue)-1]
		callback.reservedCount--
		callback.suppressedCount++
		registry.suppressedEvents++
		excess--
	}
}

// eventAsync 查询指定监听器是否为异步注册。
//
// eventID 必须来自当前 State；返回 async、found。查询持锁，不暴露回调内部指针。
func (registry *gluaEventRegistry) eventAsync(eventID int64) (bool, bool) {
	// 读取监听器类型时与删除、更新配置互斥。
	if registry == nil {
		// nil 注册表没有监听器。
		return false, false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	callback := registry.eventsByID[eventID]
	if callback == nil {
		// 不存在的 id 返回 found=false。
		return false, false
	}
	return callback.async, true
}

// callLimit 返回监听器实际使用的最大执行次数。
//
// once 始终等价于一次，其他监听器使用 maxCalls；0 表示不限制。
func (callback *gluaEventCallback) callLimit() int64 {
	// once 优先于普通上限，解析阶段已经拒绝互相矛盾的值。
	if callback == nil {
		// nil 回调没有可执行额度。
		return 0
	}
	if callback.options.once {
		// once 固定只执行一次。
		return 1
	}
	return callback.options.maxCalls
}

// eventConfig 返回指定 id 当前保存的原始配置 table。
func (registry *gluaEventRegistry) eventConfig(eventID int64) runtime.Value {
	// 读取配置时持锁，避免与 setConfig 或 remove 并发读取产生数据竞争。
	if registry == nil {
		// nil 注册表没有配置可返回。
		return runtime.NilValue()
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	callback := registry.eventsByID[eventID]
	if callback == nil {
		// 已删除或不存在的 id 返回 nil。
		return runtime.NilValue()
	}
	return callback.configValue
}

// eventSnapshot 返回指定监听器的状态 table。
func (registry *gluaEventRegistry) eventSnapshot(eventID int64) runtime.Value {
	// 查询期间持锁，确保回调、配置和统计来自同一时刻。
	if registry == nil {
		// nil 注册表没有监听器。
		return runtime.NilValue()
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	callback := registry.eventsByID[eventID]
	if callback == nil {
		// 不存在或已删除的 id 返回 nil。
		return runtime.NilValue()
	}
	return runtime.ReferenceValue(runtime.KindTable, newGluaEventSnapshot(callback))
}

// newGluaEventSnapshot 构建单个监听器的公开状态 table。
func newGluaEventSnapshot(callback *gluaEventCallback) *runtime.Table {
	// 状态快照不暴露注册表内部指针，仅返回 Lua 可用字段。
	result := runtime.NewTable()
	if callback == nil {
		// nil 回调返回空表供防御路径使用。
		return result
	}
	result.RawSetString("id", runtime.IntegerValue(callback.id))
	result.RawSetString("event", runtime.StringValue(callback.eventName))
	result.RawSetString("source", runtime.StringValue(callback.source))
	result.RawSetString("scope", runtime.StringValue(string(callback.options.scope)))
	result.RawSetString("callback", callback.value)
	result.RawSetString("config", callback.configValue)
	result.RawSetString("async", runtime.BooleanValue(callback.async))
	result.RawSetString("muted", runtime.BooleanValue(callback.muted))
	result.RawSetString("once", runtime.BooleanValue(callback.options.once))
	result.RawSetString("maxCalls", runtime.IntegerValue(callback.callLimit()))
	result.RawSetString("priority", runtime.IntegerValue(callback.options.priority))
	result.RawSetString("group", runtime.StringValue(callback.options.group))
	result.RawSetString("throttleMs", runtime.IntegerValue(callback.options.throttleNs/int64(time.Millisecond)))
	result.RawSetString("debounceMs", runtime.IntegerValue(callback.options.debounceNs/int64(time.Millisecond)))
	result.RawSetString("sampleRate", runtime.NumberValue(callback.options.sampleRate))
	result.RawSetString("onError", runtime.StringValue(callback.options.onError))
	result.RawSetString("dispatchCount", runtime.IntegerValue(callback.dispatchCount))
	result.RawSetString("errorCount", runtime.IntegerValue(callback.errorCount))
	result.RawSetString("droppedCount", runtime.IntegerValue(callback.droppedCount))
	result.RawSetString("suppressedCount", runtime.IntegerValue(callback.suppressedCount))
	result.RawSetString("throttledCount", runtime.IntegerValue(callback.throttledCount))
	result.RawSetString("sampledOutCount", runtime.IntegerValue(callback.sampledOutCount))
	result.RawSetString("debouncedCount", runtime.IntegerValue(callback.debouncedCount))
	result.RawSetString("totalDurationNs", runtime.IntegerValue(callback.totalDurationNs))
	averageDurationNs := int64(0)
	if callback.dispatchCount > 0 {
		// 平均耗时只在至少执行一次后计算，避免除零。
		averageDurationNs = callback.totalDurationNs / callback.dispatchCount
	}
	result.RawSetString("averageDurationNs", runtime.IntegerValue(averageDurationNs))
	remainingCalls := int64(-1)
	if limit := callback.callLimit(); limit > 0 {
		// 有限监听器返回不小于零的剩余可执行次数。
		remainingCalls = limit - callback.dispatchCount - callback.reservedCount
		if remainingCalls < 0 {
			// 并发配置更新等边界下快照不暴露负数。
			remainingCalls = 0
		}
	}
	result.RawSetString("remainingCalls", runtime.IntegerValue(remainingCalls))
	return result
}

// clearEvents 删除指定源码下符合事件名或分组条件的监听器。
func (registry *gluaEventRegistry) clearEvents(source string, eventName string, group string) int {
	// 先收集 id 再复用单项删除，确保根表和队列语义一致。
	if registry == nil {
		// nil 注册表没有监听器。
		return 0
	}
	registry.mu.Lock()
	ids := make([]int64, 0)
	for id, callback := range registry.eventsByID {
		// source 必须匹配，event/group 为空时不作为筛选条件。
		if callback == nil || callback.source != source || (eventName != "" && callback.eventName != eventName) || (group != "" && callback.options.group != group) {
			continue
		}
		ids = append(ids, id)
	}
	registry.mu.Unlock()
	for _, id := range ids {
		// removeEvent 负责清理全部索引和 GC 根。
		registry.removeEvent(id)
	}
	return len(ids)
}

// setGroupMuted 批量修改指定源码分组的静音状态。
func (registry *gluaEventRegistry) setGroupMuted(source string, group string, muted bool) int {
	// 批量修改在同一锁内完成，调用方不会观察到部分状态。
	if registry == nil {
		// nil 注册表没有监听器。
		return 0
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	updated := 0
	for _, callback := range registry.eventsByID {
		// 只修改当前源码且 group 精确匹配的监听器。
		if callback == nil || callback.source != source || callback.options.group != group {
			continue
		}
		callback.muted = muted
		updated++
	}
	return updated
}

// eventList 返回指定源码文件按事件名聚合的监听器统计表。
func (registry *gluaEventRegistry) eventList(source string) *runtime.Table {
	// 读取统计时持锁，保证删除、静音和注册不会生成撕裂快照。
	if registry == nil {
		// nil 注册表返回当前源码的空统计。
		return newGluaEventListTable(source, nil)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	eventsByName := registry.relevantEventsLocked(source)
	result := newGluaEventListTable(source, eventsByName)
	result.RawSetString("queuedTasks", runtime.IntegerValue(int64(len(registry.queue))))
	result.RawSetString("droppedTasks", runtime.IntegerValue(registry.droppedTasks))
	result.RawSetString("callbackErrors", runtime.IntegerValue(registry.callbackErrors))
	result.RawSetString("suppressedEvents", runtime.IntegerValue(registry.suppressedEvents))
	result.RawSetString("debouncedTasks", runtime.IntegerValue(registry.debouncedTasks))
	result.RawSetString("sequence", runtime.IntegerValue(registry.sequence))
	result.RawSetString("traceSequence", runtime.IntegerValue(registry.traceSequence))
	return result
}

// relevantEventsLocked 聚合对指定 source 生效的 runtime 与 file 监听器。
func (registry *gluaEventRegistry) relevantEventsLocked(source string) map[string][]*gluaEventCallback {
	// 调用方已持有注册表锁，这里只构造独立切片供统计读取。
	result := make(map[string][]*gluaEventCallback)
	for registrationSource, eventsByName := range registry.progressEvents {
		// runtime 监听器跨 source 生效；file 监听器只匹配注册 source。
		for eventName, callbacks := range eventsByName {
			// 每个事件名独立聚合，最后统一排序。
			for _, callback := range callbacks {
				// 损坏回调或不匹配的 file 监听器不进入结果。
				if callback == nil || (callback.options.scope == ProgressEventScopeFile && registrationSource != source) {
					continue
				}
				result[eventName] = append(result[eventName], callback)
			}
		}
	}
	for eventName := range result {
		// 跨文件聚合后重新按优先级和注册 ID 建立全局稳定顺序。
		sortGluaEventCallbacks(result[eventName])
	}
	return result
}

// sortGluaEventCallbacks 按优先级降序和注册 ID 升序稳定排序。
func sortGluaEventCallbacks(callbacks []*gluaEventCallback) {
	// 所有派发与统计聚合共用同一排序规则。
	sort.SliceStable(callbacks, func(left int, right int) bool {
		// 高优先级先执行；相同优先级保持更早注册的监听器在前。
		if callbacks[left].options.priority != callbacks[right].options.priority {
			// priority 不同时按降序排列。
			return callbacks[left].options.priority > callbacks[right].options.priority
		}
		return callbacks[left].id < callbacks[right].id
	})
}

// newGluaEventListTable 构建稳定排序的事件监听器统计表。
func newGluaEventListTable(source string, eventsByName map[string][]*gluaEventCallback) *runtime.Table {
	// 事件名称排序后写入数组，保证 CLI、测试和编辑器展示顺序稳定。
	result := runtime.NewTable()
	result.RawSetString("source", runtime.StringValue(source))
	eventsTable := runtime.NewTable()
	eventNames := make([]string, 0, len(eventsByName))
	for eventName := range eventsByName {
		// map key 收集后统一排序。
		eventNames = append(eventNames, eventName)
	}
	sort.Strings(eventNames)
	totalListeners := 0
	totalActive := 0
	totalMuted := 0
	totalSync := 0
	totalAsync := 0
	for eventIndex, eventName := range eventNames {
		// 每个事件项分别统计有效监听器的当前状态。
		entry := runtime.NewTable()
		entry.RawSetString("event", runtime.StringValue(eventName))
		listeners := 0
		active := 0
		muted := 0
		syncListeners := 0
		asyncListeners := 0
		dispatchCount := int64(0)
		errorCount := int64(0)
		droppedCount := int64(0)
		suppressedCount := int64(0)
		totalDurationNs := int64(0)
		for _, callback := range eventsByName[eventName] {
			// nil 回调不属于有效监听器，可能仅在并发删除的旧切片中短暂出现。
			if callback == nil {
				// 跳过损坏槽位，不计入任何统计。
				continue
			}
			listeners++
			if callback.muted {
				// 静音监听器仍计入 listeners，但不计入 active。
				muted++
			} else {
				// 未静音监听器属于当前活跃监听器。
				active++
			}
			if callback.async {
				// 异步注册计入 async 数量。
				asyncListeners++
			} else {
				// 同步注册计入 sync 数量。
				syncListeners++
			}
			dispatchCount += callback.dispatchCount
			errorCount += callback.errorCount
			droppedCount += callback.droppedCount
			suppressedCount += callback.suppressedCount
			totalDurationNs += callback.totalDurationNs
		}
		entry.RawSetString("listeners", runtime.IntegerValue(int64(listeners)))
		entry.RawSetString("active", runtime.IntegerValue(int64(active)))
		entry.RawSetString("muted", runtime.IntegerValue(int64(muted)))
		entry.RawSetString("sync", runtime.IntegerValue(int64(syncListeners)))
		entry.RawSetString("async", runtime.IntegerValue(int64(asyncListeners)))
		entry.RawSetString("dispatchCount", runtime.IntegerValue(dispatchCount))
		entry.RawSetString("errorCount", runtime.IntegerValue(errorCount))
		entry.RawSetString("droppedCount", runtime.IntegerValue(droppedCount))
		entry.RawSetString("suppressedCount", runtime.IntegerValue(suppressedCount))
		entry.RawSetString("totalDurationNs", runtime.IntegerValue(totalDurationNs))
		eventsTable.RawSetInteger(int64(eventIndex+1), runtime.ReferenceValue(runtime.KindTable, entry))
		totalListeners += listeners
		totalActive += active
		totalMuted += muted
		totalSync += syncListeners
		totalAsync += asyncListeners
	}
	result.RawSetString("totalEvents", runtime.IntegerValue(int64(len(eventNames))))
	result.RawSetString("totalListeners", runtime.IntegerValue(int64(totalListeners)))
	result.RawSetString("activeListeners", runtime.IntegerValue(int64(totalActive)))
	result.RawSetString("mutedListeners", runtime.IntegerValue(int64(totalMuted)))
	result.RawSetString("syncListeners", runtime.IntegerValue(int64(totalSync)))
	result.RawSetString("asyncListeners", runtime.IntegerValue(int64(totalAsync)))
	result.RawSetString("events", runtime.ReferenceValue(runtime.KindTable, eventsTable))
	result.RawSetString("queuedTasks", runtime.IntegerValue(0))
	result.RawSetString("droppedTasks", runtime.IntegerValue(0))
	result.RawSetString("callbackErrors", runtime.IntegerValue(0))
	result.RawSetString("suppressedEvents", runtime.IntegerValue(0))
	result.RawSetString("debouncedTasks", runtime.IntegerValue(0))
	result.RawSetString("sequence", runtime.IntegerValue(0))
	result.RawSetString("traceSequence", runtime.IntegerValue(0))
	return result
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
	if contextTable, ok := context.Ref.(*runtime.Table); ok && contextTable != nil {
		// Go 触发或跨文件触发时，以显式 source 覆盖当前调用帧推导值。
		contextTable.RawSetString("source", runtime.StringValue(source))
	}
	return registry.dispatchCallbacks(state, callbacks, context, forceAsync)
}

// progressCallbacks 返回对指定 source 生效的事件回调快照。
func (registry *gluaEventRegistry) progressCallbacks(source string, eventName string) []*gluaEventCallback {
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
	callbacks := make([]*gluaEventCallback, 0)
	for registrationSource, eventsByName := range registry.progressEvents {
		// 从全部注册来源收集 runtime 监听器和当前 source 的 file 监听器。
		for _, callback := range eventsByName[eventName] {
			// nil 回调和不匹配的 file 监听器不参与派发。
			if callback == nil || (callback.options.scope == ProgressEventScopeFile && registrationSource != source) {
				continue
			}
			callbacks = append(callbacks, callback)
		}
	}
	sortGluaEventCallbacks(callbacks)
	return callbacks
}

// dispatchCallbacks 执行或入队一组事件回调。
func (registry *gluaEventRegistry) dispatchCallbacks(state *State, callbacks []*gluaEventCallback, context runtime.Value, forceAsync bool) ([]runtime.Value, error) {
	// 空回调列表不产生任何副作用。
	if registry == nil || len(callbacks) == 0 {
		// 无回调时保持无返回值。
		return nil, nil
	}
	for _, callback := range callbacks {
		if !registry.canDispatch(callback, nil, runtime.NilValue(), false) {
			// 已删除、损坏或静音的回调不参与本次派发。
			continue
		}
		callbackContext := prepareGluaEventContext(context, callback)
		setGluaContextCallbackMetadata(callbackContext, callback)
		// 异步回调只入队，等待 VM 安全点消费。
		if callback.async || forceAsync {
			// 异步任务必须得到独立快照，避免后续 callback 覆盖其 id/async 字段。
			setGluaContextAsync(callbackContext, true)
			accepted, enqueueErr := registry.enqueue(callback, callbackContext)
			if enqueueErr != nil {
				// error 溢出策略同步报告给事件触发点。
				registry.releaseDispatchClaim(callback)
				return nil, enqueueErr
			}
			if !accepted {
				// 任务被丢弃或防抖合并时释放本次派发额度。
				registry.releaseDispatchClaim(callback)
			}
			continue
		}
		setGluaContextAsync(callbackContext, false)
		err := registry.callCallback(state, callback, callbackContext)
		registry.finishDispatch(callback)
		if resolvedErr := registry.resolveCallbackError(callback, err); resolvedErr != nil {
			// propagate 策略把同步回调错误返回当前 Lua 执行路径。
			return nil, resolvedErr
		}
	}
	return nil, nil
}

// dispatchFunctionCallbacks 按函数名过滤后执行或入队一组函数进度事件回调。
func (registry *gluaEventRegistry) dispatchFunctionCallbacks(state *State, callbacks []*gluaEventCallback, context runtime.Value, candidates []string, callee runtime.Value) ([]runtime.Value, error) {
	// 函数进度事件需要在普通派发规则上叠加白名单和黑名单。
	if registry == nil || len(callbacks) == 0 {
		// 没有回调时不创建事件任务。
		return nil, nil
	}
	for _, callback := range callbacks {
		if !registry.canDispatch(callback, candidates, callee, true) {
			// 不满足过滤配置、已删除或静音时跳过当前回调。
			continue
		}
		callbackContext := prepareGluaEventContext(context, callback)
		setGluaContextCallbackMetadata(callbackContext, callback)
		if callback.async {
			// 异步注册只入队，保持调用点不被 callback 执行阻塞。
			setGluaContextAsync(callbackContext, true)
			accepted, enqueueErr := registry.enqueue(callback, callbackContext)
			if enqueueErr != nil {
				// error 溢出策略同步报告给调用点。
				registry.releaseDispatchClaim(callback)
				return nil, enqueueErr
			}
			if !accepted {
				// 未独立入队的任务释放本次派发额度。
				registry.releaseDispatchClaim(callback)
			}
			continue
		}
		setGluaContextAsync(callbackContext, false)
		err := registry.callCallback(state, callback, callbackContext)
		registry.finishDispatch(callback)
		if resolvedErr := registry.resolveCallbackError(callback, err); resolvedErr != nil {
			// propagate 策略中断当前函数调用。
			return nil, resolvedErr
		}
	}
	return nil, nil
}

// canDispatch 判断回调当前是否仍有效、未静音且满足函数名过滤。
func (registry *gluaEventRegistry) canDispatch(callback *gluaEventCallback, candidates []string, callee runtime.Value, applyFilter bool) bool {
	// 正常派发使用当前时间；测试和内部策略可调用带显式时间的实现。
	return registry.canDispatchAt(callback, candidates, callee, applyFilter, time.Now())
}

// canDispatchAt 判断回调是否满足过滤、限次、采样和节流规则。
//
// now 是调用方提供的当前时间；返回 true 时会占用一次执行额度，未执行任务必须显式释放。
func (registry *gluaEventRegistry) canDispatchAt(callback *gluaEventCallback, candidates []string, callee runtime.Value, applyFilter bool, now time.Time) bool {
	// 读取回调状态时持锁，避免删除或静音与 VM 派发并发产生数据竞争。
	if registry == nil || callback == nil {
		// nil 注册表或回调不能参与派发。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.eventsByID[callback.id] != callback || callback.muted {
		// 已删除或静音的注册在本次派发中失效。
		return false
	}
	if applyFilter {
		// 函数事件先应用黑白名单，普通事件跳过该分支。
		if !callback.filter.blacklist.empty() && callback.filter.blacklist.matches(candidates, callee) {
			// 黑名单优先级最高，命中时始终跳过。
			return false
		}
		if !callback.filter.whitelist.empty() && !callback.filter.whitelist.matches(candidates, callee) {
			// 非空白名单未命中时跳过。
			return false
		}
	}
	limit := callback.callLimit()
	if limit > 0 && callback.dispatchCount+callback.reservedCount >= limit {
		// 已执行和已占用任务达到上限后抑制后续触发。
		callback.suppressedCount++
		registry.suppressedEvents++
		return false
	}
	if callback.options.sampleRate < 1 {
		// 确定性累加采样保证相同事件序列可复现。
		callback.sampleBalance += callback.options.sampleRate
		if callback.sampleBalance < 1 {
			// 累加器尚未达到一次完整样本，本次被采样丢弃。
			callback.sampledOutCount++
			callback.suppressedCount++
			registry.suppressedEvents++
			return false
		}
		callback.sampleBalance--
	}
	if callback.options.throttleNs > 0 && !callback.lastAcceptedAt.IsZero() && now.Sub(callback.lastAcceptedAt) < time.Duration(callback.options.throttleNs) {
		// 两次接受触发间隔不足时执行前沿节流。
		callback.throttledCount++
		callback.suppressedCount++
		registry.suppressedEvents++
		return false
	}
	if callback.options.throttleNs > 0 {
		// 只有通过全部检查的触发才推进节流时间窗。
		callback.lastAcceptedAt = now
	}
	callback.reservedCount++
	return true
}

// empty 判断过滤集合是否没有名称和函数引用。
func (filterSet gluaEventFilterSet) empty() bool {
	// 任一类型存在选择器都表示集合非空。
	return len(filterSet.names) == 0 && len(filterSet.functions) == 0
}

// matches 判断候选函数名称或运行期函数身份是否命中过滤集合。
func (filterSet gluaEventFilterSet) matches(candidates []string, callee runtime.Value) bool {
	// 先匹配兼容的字符串名称选择器。
	for _, candidate := range candidates {
		if _, ok := filterSet.names[candidate]; ok {
			// 任一候选名命中即可视为匹配。
			return true
		}
	}
	if !gluaEventFilterFunction(callee) {
		// 当前被调值不是函数时，不可能命中 closure identity。
		return false
	}
	for _, function := range filterSet.functions {
		// 对象身份精确区分不同文件或不同实例的同名 Lua closure。
		if gluaEventFunctionIdentityEqual(function, callee) {
			// 任一函数引用命中即可视为匹配。
			return true
		}
	}
	return false
}

// cloneGluaEventContext 复制事件上下文表，避免多个 callback 共享可变 id/config 字段。
func cloneGluaEventContext(context runtime.Value) runtime.Value {
	// 非 table 上下文直接返回原值，当前正常路径都会传入 table。
	if context.Kind != runtime.KindTable {
		// 损坏上下文交由后续调用路径处理。
		return context
	}
	sourceTable, ok := context.Ref.(*runtime.Table)
	if !ok || sourceTable == nil {
		// 损坏 table 引用无法复制，保留原上下文。
		return context
	}
	clonedTable := runtime.NewTable()
	for _, fieldName := range gluaEventContextFields {
		// 事件上下文全部由固定字符串字段构成，直接读取可避开 RawNext 的原始 string-key 遍历边界。
		fieldValue := sourceTable.RawGetString(fieldName)
		if fieldValue.IsNil() {
			// 缺失字段按 Lua nil 语义不写入 clone。
			continue
		}
		_ = clonedTable.RawSet(runtime.StringValue(fieldName), fieldValue)
	}
	return runtime.ReferenceValue(runtime.KindTable, clonedTable)
}

// prepareGluaEventContext 为单个监听器复制上下文并应用只读快照策略。
func prepareGluaEventContext(context runtime.Value, callback *gluaEventCallback) runtime.Value {
	// 每个监听器获得独立顶层 table，避免回调之间相互污染字段。
	prepared := cloneGluaEventContext(context)
	if callback == nil || callback.options.mutable || prepared.Kind != runtime.KindTable {
		// mutable=true 保留历史引用语义；损坏上下文直接返回复制结果。
		return prepared
	}
	preparedTable, _ := prepared.Ref.(*runtime.Table)
	if preparedTable == nil {
		// 无有效 table 时无法替换快照字段。
		return prepared
	}
	visited := make(map[*runtime.Table]*runtime.Table)
	for _, fieldName := range []string{"locals", "upvalues", "calleeUpvalues", "args", "results", "error"} {
		// 默认递归复制并冻结所有嵌套 table，阻断回调修改业务变量。
		fieldValue := preparedTable.RawGetString(fieldName)
		if fieldValue.IsNil() {
			continue
		}
		preparedTable.RawSetString(fieldName, cloneGluaReadOnlyValue(fieldValue, visited))
	}
	return prepared
}

// cloneGluaReadOnlyValue 递归复制 table 值并冻结副本。
func cloneGluaReadOnlyValue(value runtime.Value, visited map[*runtime.Table]*runtime.Table) runtime.Value {
	// 非 table 值按 Lua 值语义直接复用。
	if value.Kind != runtime.KindTable {
		return value
	}
	sourceTable, _ := value.Ref.(*runtime.Table)
	if sourceTable == nil {
		// 损坏 table 引用保持原值，后续读取仍按原错误语义处理。
		return value
	}
	if existing := visited[sourceTable]; existing != nil {
		// 环形引用复用已经创建的副本，保持图结构。
		return runtime.ReferenceValue(runtime.KindTable, existing)
	}
	clonedTable := runtime.NewTable()
	visited[sourceTable] = clonedTable
	key := runtime.NilValue()
	for {
		// RawNext 复制当前可见键值；key 保留原身份，value 中的 table 递归复制。
		nextKey, nextValue, ok, err := sourceTable.RawNext(key)
		if err != nil || !ok {
			break
		}
		_ = clonedTable.RawSet(nextKey, cloneGluaReadOnlyValue(nextValue, visited))
		key = nextKey
	}
	clonedTable.Freeze()
	return runtime.ReferenceValue(runtime.KindTable, clonedTable)
}

// setGluaContextCallbackMetadata 写入单个回调对应的 id 和配置。
func setGluaContextCallbackMetadata(context runtime.Value, callback *gluaEventCallback) {
	// context 由 buildGluaEventContext 创建，只有 table 值才能写入字段。
	if context.Kind != runtime.KindTable || callback == nil {
		// 非 table 上下文或 nil 回调直接跳过。
		return
	}
	table, ok := context.Ref.(*runtime.Table)
	if !ok || table == nil {
		// 损坏 table 引用无法更新。
		return
	}
	table.RawSetString("id", runtime.IntegerValue(callback.id))
	table.RawSetString("config", callback.configValue)
	table.RawSetString("group", runtime.StringValue(callback.options.group))
	table.RawSetString("priority", runtime.IntegerValue(callback.options.priority))
	table.RawSetString("scope", runtime.StringValue(string(callback.options.scope)))
	table.RawSetString("once", runtime.BooleanValue(callback.options.once))
	table.RawSetString("maxCalls", runtime.IntegerValue(callback.callLimit()))
	table.RawSetString("throttleMs", runtime.IntegerValue(callback.options.throttleNs/int64(time.Millisecond)))
	table.RawSetString("debounceMs", runtime.IntegerValue(callback.options.debounceNs/int64(time.Millisecond)))
	table.RawSetString("sampleRate", runtime.NumberValue(callback.options.sampleRate))
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
func (registry *gluaEventRegistry) enqueue(callback *gluaEventCallback, context runtime.Value) (bool, error) {
	// 入队前先校验 registry。
	if registry == nil || callback == nil {
		// nil 注册表无法保存任务。
		return false, nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	readyAt := time.Time{}
	if callback.options.debounceNs > 0 {
		// 防抖任务只在时间窗结束后执行，并优先合并已有待处理任务。
		readyAt = time.Now().Add(time.Duration(callback.options.debounceNs))
		for index := range registry.queue {
			// 同一监听器最多保留一个防抖任务，后触发上下文覆盖旧上下文。
			if registry.queue[index].callback != callback {
				// 其他监听器任务不参与合并。
				continue
			}
			registry.queue[index].context = context
			registry.queue[index].readyAt = readyAt
			callback.debouncedCount++
			callback.suppressedCount++
			registry.debouncedTasks++
			registry.suppressedEvents++
			return false, nil
		}
	}
	if callback.options.queueLimit > 0 {
		// 队列上限按监听器计算，避免一个高频监听器挤占其他异步事件。
		pending := 0
		oldestIndex := -1
		for index, task := range registry.queue {
			// 仅统计同一监听器的待处理任务。
			if task.callback != callback {
				continue
			}
			pending++
			if oldestIndex < 0 {
				// 第一个匹配任务就是该监听器最旧任务。
				oldestIndex = index
			}
		}
		if pending >= callback.options.queueLimit {
			// 队列已满时按配置执行确定性溢出策略。
			switch callback.options.overflow {
			case "drop_oldest":
				// 删除最旧匹配任务，为新任务腾出一个槽位。
				copy(registry.queue[oldestIndex:], registry.queue[oldestIndex+1:])
				registry.queue[len(registry.queue)-1] = gluaEventTask{}
				registry.queue = registry.queue[:len(registry.queue)-1]
				if callback.reservedCount > 0 {
					// 被替换旧任务不再执行，需要释放它占用的额度。
					callback.reservedCount--
				}
			case "error":
				// error 策略不修改队列，并把溢出报告给触发点。
				return false, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("event async queue limit reached for listener %d", callback.id)))
			default:
				// drop_newest 保留旧任务并丢弃当前任务。
				callback.droppedCount++
				registry.droppedTasks++
				return false, nil
			}
			callback.droppedCount++
			registry.droppedTasks++
		}
	}
	registry.queue = append(registry.queue, gluaEventTask{callback: callback, context: context, readyAt: readyAt})
	return true, nil
}

// releaseDispatchClaim 释放未成功独立入队的派发额度。
//
// callback 必须来自当前注册表；函数没有返回值。删除后的监听器无需恢复额度。
func (registry *gluaEventRegistry) releaseDispatchClaim(callback *gluaEventCallback) {
	// 所有限次和不限次监听器都维护 reservedCount，保证统计和配置更新一致。
	if registry == nil || callback == nil {
		// 无效参数没有额度可释放。
		return
	}
	registry.mu.Lock()
	if registry.eventsByID[callback.id] == callback && callback.reservedCount > 0 {
		// 监听器仍有效且确有占用时减少一次。
		callback.reservedCount--
	}
	registry.mu.Unlock()
}

// finishDispatch 释放已执行任务额度，并在达到执行上限后删除监听器。
//
// callback 必须刚由 callCallback 执行；函数没有返回值。once 使用与 maxCalls 相同的收尾路径。
func (registry *gluaEventRegistry) finishDispatch(callback *gluaEventCallback) {
	// 完成路径先更新占用，再判断是否耗尽上限。
	if registry == nil || callback == nil {
		// 无效参数没有监听器可收尾。
		return
	}
	registry.mu.Lock()
	if callback.reservedCount > 0 {
		// 每个实际执行任务对应一个派发占用。
		callback.reservedCount--
	}
	limit := callback.callLimit()
	limitReached := limit > 0 && callback.dispatchCount >= limit
	registry.mu.Unlock()
	if limitReached {
		// 达到上限后删除监听器和它仍残留的异步任务。
		registry.removeEvent(callback.id)
	}
}

// drainGluaEventQueue 在 VM 安全点消费当前 State 的异步事件队列。
func drainGluaEventQueue(state *State) error {
	// 安全点消费异步队列，避免 goroutine 直接重入 Lua VM。
	_, err := drainGluaEventQueueCount(state, false)
	return err
}

// drainGluaEventQueueCount 消费异步队列并返回实际执行任务数。
func drainGluaEventQueueCount(state *State, force bool) (int, error) {
	// flush 和 VM 安全点共用同一消费路径，保证错误策略一致。
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 无注册表时没有异步任务。
		return 0, nil
	}
	executed := 0
	for {
		// 每轮只取当前队列快照，允许回调继续追加下一批任务。
		tasks := registry.takeQueuedTasks(time.Now(), force)
		if len(tasks) == 0 {
			// 队列耗尽后结束消费。
			return executed, nil
		}
		for _, task := range tasks {
			if !registry.queuedCallbackActive(task.callback) {
				// 已删除或静音的监听器不执行旧队列任务。
				registry.releaseDispatchClaim(task.callback)
				continue
			}
			// 异步回调错误在安全点传播，仍不会阻塞事件触发点本身。
			err := registry.callCallback(state, task.callback, task.context)
			executed++
			registry.finishDispatch(task.callback)
			if resolvedErr := registry.resolveCallbackError(task.callback, err); resolvedErr != nil {
				// propagate 在当前安全点返回错误，其他策略已经完成治理。
				return executed, resolvedErr
			}
		}
	}
}

// queuedCallbackActive 判断异步任务对应监听器是否仍可执行。
func (registry *gluaEventRegistry) queuedCallbackActive(callback *gluaEventCallback) bool {
	// 队列任务已经占用派发额度，但必须仍注册且未静音。
	if registry == nil || callback == nil {
		// 损坏任务不能执行。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.eventsByID[callback.id] == callback && !callback.muted
}

// takeQueuedTasks 取出已经到期或被强制刷新的异步任务。
//
// now 用于防抖到期判断；force=true 会忽略到期时间，供 flush 确定性清空队列。
func (registry *gluaEventRegistry) takeQueuedTasks(now time.Time, force bool) []gluaEventTask {
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
	tasks := make([]gluaEventTask, 0, len(registry.queue))
	pendingWriteIndex := 0
	for index, task := range registry.queue {
		// 普通任务立即可执行，防抖任务需要到期或显式 force。
		if force || task.readyAt.IsZero() || !task.readyAt.After(now) {
			// 到期任务移入本轮执行快照。
			tasks = append(tasks, task)
			registry.queue[index] = gluaEventTask{}
			continue
		}
		registry.queue[pendingWriteIndex] = task
		if pendingWriteIndex != index {
			// 已前移的旧槽位需要清空强引用。
			registry.queue[index] = gluaEventTask{}
		}
		pendingWriteIndex++
	}
	registry.queue = registry.queue[:pendingWriteIndex]
	return tasks
}

// resolveCallbackError 按监听器配置处理一次已经计入统计的回调错误。
//
// err 为 nil 时直接返回 nil；propagate 返回原错误，ignore 继续，mute 静音，remove 删除监听器。
func (registry *gluaEventRegistry) resolveCallbackError(callback *gluaEventCallback, err error) error {
	// 成功回调不触发任何治理动作。
	if err == nil || registry == nil || callback == nil {
		// nil 错误保持成功语义。
		return err
	}
	registry.mu.Lock()
	onError := callback.options.onError
	registry.mu.Unlock()
	switch onError {
	case "ignore":
		// 错误已经计入统计，但当前执行路径继续。
		return nil
	case "mute":
		// 静音保留监听器、配置与统计，允许后续显式恢复。
		registry.setEventMuted(callback.id, true)
		return nil
	case "remove":
		// 删除监听器并取消剩余异步任务。
		registry.removeEvent(callback.id)
		return nil
	default:
		// propagate 是默认值，未知内部值也安全地向上传播。
		return err
	}
}

// callCallback 调用单个事件回调。
func (registry *gluaEventRegistry) callCallback(state *State, callback *gluaEventCallback, context runtime.Value) error {
	// 事件回调只能在有效 State 上执行。
	if state == nil || callback == nil {
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
	// 同步回调前再次写入专属元数据，确保复制上下文不会遗漏该 callback 的 id/config。
	setGluaContextCallbackMetadata(context, callback)
	startedAt := time.Now()
	_, err := callWithDebugName(state, callback.value, "", "event", context)
	duration := time.Since(startedAt)
	registry.mu.Lock()
	callback.dispatchCount++
	callback.totalDurationNs += duration.Nanoseconds()
	if err != nil {
		// 回调错误同时计入监听器和注册表统计。
		callback.errorCount++
		registry.callbackErrors++
	}
	registry.mu.Unlock()
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
	context.RawSetString("timestamp", runtime.IntegerValue(time.Now().UnixMilli()))
	depth := int64(0)
	if state != nil {
		// depth 使用当前 Lua/Go 调用帧深度快照。
		depth = int64(state.CallDepth())
		context.RawSetString("depth", runtime.IntegerValue(depth))
	}
	if registry := gluaRegistryForState(state); registry != nil {
		// 事件编号、调用链和父事件在同一锁内分配，保证并发宿主触发时关系一致。
		eventID, traceID, parentEventID := registry.nextContextIdentity(eventName, depth)
		context.RawSetString("sequence", runtime.IntegerValue(eventID))
		context.RawSetString("eventId", runtime.IntegerValue(eventID))
		context.RawSetString("traceId", runtime.IntegerValue(traceID))
		if parentEventID > 0 {
			// 根事件没有 parentEventId 字段，子事件才写入有效编号。
			context.RawSetString("parentEventId", runtime.IntegerValue(parentEventID))
		}
	}
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

// nextContextIdentity 分配事件编号、调用链编号和父事件编号。
//
// eventName 和 depth 来自当前 VM 帧；返回值在单个 State 内稳定递增，并按较浅调用深度关联父事件。
func (registry *gluaEventRegistry) nextContextIdentity(eventName string, depth int64) (int64, int64, int64) {
	// 编号和调用链状态在同一临界区更新。
	if registry == nil {
		// nil 注册表没有可分配身份。
		return 0, 0, 0
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.sequence++
	eventID := registry.sequence
	if registry.currentTraceID == 0 || (eventName == GluaEventProgressStart && depth <= registry.traceDepth) {
		// 首个事件或新的根代码块开始时分配调用链，并清理旧深度关系。
		registry.traceSequence++
		registry.currentTraceID = registry.traceSequence
		registry.traceDepth = depth
		registry.lastEventByDepth = make(map[int64]int64)
	}
	parentEventID := int64(0)
	for parentDepth := depth - 1; parentDepth >= 0; parentDepth-- {
		// 选择最近的较浅调用深度事件作为父节点。
		if candidate := registry.lastEventByDepth[parentDepth]; candidate > 0 {
			// 找到最近父事件后停止向上扫描。
			parentEventID = candidate
			break
		}
	}
	if registry.lastEventByDepth == nil {
		// 非 start 事件也可能是调用链首个可见事件，需要初始化索引。
		registry.lastEventByDepth = make(map[int64]int64)
	}
	for trackedDepth := range registry.lastEventByDepth {
		// 当前深度的新事件会让更深层旧节点失效，避免跨已返回函数错误关联。
		if trackedDepth > depth {
			// 删除已经离开的更深调用层。
			delete(registry.lastEventByDepth, trackedDepth)
		}
	}
	registry.lastEventByDepth[depth] = eventID
	traceID := registry.currentTraceID
	if eventName == GluaEventProgressExit && depth <= registry.traceDepth {
		// 根代码块 exit 使用当前 traceId，后续独立事件再创建新调用链。
		registry.currentTraceID = 0
		registry.lastEventByDepth = nil
	}
	return eventID, traceID, parentEventID
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

// triggerGluaProgressFunctionEvent 触发当前文件内一次函数调用的进度事件。
func triggerGluaProgressFunctionEvent(state *State, callerProto *bytecode.Proto, eventName string, payload runtime.Value, details runtime.Value, callee runtime.Value, calleeName string, calleeNameWhat string, callPC int) error {
	// 函数调用事件按调用点所属源码文件匹配，而不是按被调 closure 绑定。
	if callerProto == nil {
		// 缺少调用方 Proto 时无法确定当前文件作用域。
		return nil
	}
	registry := gluaRegistryForState(state)
	if registry == nil {
		// 无注册表时保持无事件开销语义。
		return nil
	}
	callbacks := registry.progressCallbacks(callerProto.Source, eventName)
	if len(callbacks) == 0 {
		// 当前文件没有订阅该函数进度事件。
		return nil
	}
	context := buildGluaEventContext(state, "progress", eventName, payload, false, nil, callerProto)
	contextTable, _ := context.Ref.(*runtime.Table)
	if contextTable != nil {
		// 事件上下文同时描述调用点和被调对象，便于 callback 做关联观察。
		applyGluaFunctionEventDetails(contextTable, details)
		contextTable.RawSetString("callee", callee)
		contextTable.RawSetString("calleeName", runtime.StringValue(calleeName))
		contextTable.RawSetString("calleeNameWhat", runtime.StringValue(calleeNameWhat))
		contextTable.RawSetString("calleeType", runtime.StringValue(runtime.LuaTypeName(callee)))
		contextTable.RawSetString("callPC", runtime.IntegerValue(int64(callPC)))
		if calleeClosure, ok := callee.Ref.(*runtime.LuaClosure); ok && calleeClosure != nil && calleeClosure.Proto != nil {
			// Lua closure 额外暴露被调函数的源码范围和 upvalue 快照。
			contextTable.RawSetString("calleeSource", runtime.StringValue(calleeClosure.Proto.Source))
			contextTable.RawSetString("calleeLineDefined", runtime.IntegerValue(int64(calleeClosure.Proto.LineDefined)))
			contextTable.RawSetString("calleeLastLineDefined", runtime.IntegerValue(int64(calleeClosure.Proto.LastLineDefined)))
			if upvalues := gluaUpvalueTable(calleeClosure); upvalues != nil {
				// 被调 Lua closure 的 upvalue 在函数进度事件中可观测。
				contextTable.RawSetString("calleeUpvalues", runtime.ReferenceValue(runtime.KindTable, upvalues))
			}
		}
	}
	candidates := gluaFunctionEventCandidates(calleeName, calleeNameWhat, callee)
	_, err := registry.dispatchFunctionCallbacks(state, callbacks, context, candidates, callee)
	return err
}

// newGluaFunctionEventDetails 构造函数事件参数、结果、错误和耗时快照。
func newGluaFunctionEventDetails(arguments []Value, results []Value, callErr error, durationNs int64) runtime.Value {
	// 数组字段复制为 Lua table，避免暴露 Go slice 生命周期。
	details := runtime.NewTable()
	details.RawSetString("args", runtime.ReferenceValue(runtime.KindTable, gluaEventValueList(arguments)))
	details.RawSetString("results", runtime.ReferenceValue(runtime.KindTable, gluaEventValueList(results)))
	details.RawSetString("durationNs", runtime.IntegerValue(durationNs))
	if callErr != nil {
		// error 字段保留 Lua 可见错误对象。
		details.RawSetString("error", runtime.ErrorObject(callErr))
	}
	return runtime.ReferenceValue(runtime.KindTable, details)
}

// beginProtectedGluaFunctionEvent 为 pcall/xpcall 直接调用的 Lua closure 建立事件生命周期。
func beginProtectedGluaFunctionEvent(state *State, function Value, arguments []Value) (func([]Value, error) error, error) {
	// protected call 绕过 VM CALL 请求入口，需要在保护边界显式补发函数事件。
	if !gluaHasAnyEvent(state) || function.Kind != runtime.KindLuaClosure {
		// 无监听器或非 Lua closure 时返回无操作收尾函数。
		return func([]Value, error) error { return nil }, nil
	}
	callerProto := currentGluaCallerProto(state)
	if callerProto == nil {
		// 缺少调用方源码时无法匹配文件作用域。
		return func([]Value, error) error { return nil }, nil
	}
	startedAt := time.Now()
	callDetails := newGluaFunctionEventDetails(arguments, nil, nil, 0)
	if err := triggerGluaProgressFunctionEvent(state, callerProto, GluaEventProgressFunctionCall, runtime.NilValue(), callDetails, function, "", "", 0); err != nil {
		// 同步 call 监听器错误会阻止 protected target 执行。
		return nil, err
	}
	finished := false
	return func(results []Value, callErr error) error {
		// continuation 可能被防御性重复调用，生命周期只能收尾一次。
		if finished {
			return nil
		}
		finished = true
		durationNs := time.Since(startedAt).Nanoseconds()
		if callErr != nil {
			// 被 pcall 捕获的错误仍先触发 function_error。
			details := newGluaFunctionEventDetails(arguments, nil, callErr, durationNs)
			if err := triggerGluaProgressFunctionEvent(state, callerProto, GluaEventProgressFunctionError, runtime.ErrorObject(callErr), details, function, "", "", 0); err != nil {
				return err
			}
		} else {
			// 正常完成时暴露 protected target 的返回值。
			details := newGluaFunctionEventDetails(arguments, results, nil, durationNs)
			if err := triggerGluaProgressFunctionEvent(state, callerProto, GluaEventProgressFunctionReturn, runtime.NilValue(), details, function, "", "", 0); err != nil {
				return err
			}
		}
		exitDetails := newGluaFunctionEventDetails(arguments, results, callErr, durationNs)
		return triggerGluaProgressFunctionEvent(state, callerProto, GluaEventProgressFunctionExit, runtime.NilValue(), exitDetails, function, "", "", 0)
	}, nil
}

// gluaEventValueList 把值切片复制为 1-based Lua 数组 table。
func gluaEventValueList(values []Value) *runtime.Table {
	// nil 和空切片都返回可安全读取的空 table。
	result := runtime.NewTable()
	for index, value := range values {
		// Lua 数组索引从 1 开始。
		result.RawSetInteger(int64(index+1), value)
	}
	return result
}

// applyGluaFunctionEventDetails 把函数调用详情复制到公开上下文。
func applyGluaFunctionEventDetails(context *runtime.Table, details runtime.Value) {
	// 缺少 table 时保持基础上下文字段。
	if context == nil || details.Kind != runtime.KindTable {
		// 非函数事件不会提供 details。
		return
	}
	detailsTable, _ := details.Ref.(*runtime.Table)
	if detailsTable == nil {
		// 损坏详情引用无法读取。
		return
	}
	for _, fieldName := range []string{"args", "results", "error", "durationNs"} {
		// 详情字段按固定白名单复制，避免内部字段泄露。
		value := detailsTable.RawGetString(fieldName)
		if value.IsNil() {
			continue
		}
		context.RawSetString(fieldName, value)
	}
}

// gluaFunctionEventCandidates 生成白名单和黑名单可匹配的函数标识集合。
func gluaFunctionEventCandidates(calleeName string, calleeNameWhat string, callee runtime.Value) []string {
	// 名称、名称类别和运行时类型都可作为过滤标识，空字符串不加入集合。
	candidates := make([]string, 0, 3)
	if calleeName != "" {
		// 优先加入 Lua 调试推断出的调用名。
		candidates = append(candidates, calleeName)
	}
	if calleeNameWhat != "" {
		// 调用名类别可过滤 method/local/global 等调用形式。
		candidates = append(candidates, calleeNameWhat)
	}
	if runtime.LuaTypeName(callee) != "" {
		// 类型名允许统一过滤 LuaClosure/GoClosure 等类别。
		candidates = append(candidates, runtime.LuaTypeName(callee))
	}
	return candidates
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
	return len(registry.progressEvents) > 0 || len(registry.queue) > 0
}
