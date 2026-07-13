package lua

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// ProgressEventScope 表示进度事件监听器的匹配范围。
//
// runtime 范围匹配当前 State 内全部 Lua source；file 范围只匹配注册时指定的 source。
type ProgressEventScope string

const (
	// ProgressEventScopeRuntime 表示监听当前 State 内全部 Lua 文件，且是默认范围。
	ProgressEventScopeRuntime ProgressEventScope = "runtime"
	// ProgressEventScopeFile 表示监听注册 source 对应的单个 Lua 文件。
	ProgressEventScopeFile ProgressEventScope = "file"
)

var (
	// ErrGluaEventsUnavailable 表示当前构建或 State 配置没有启用 GLua Event。
	ErrGluaEventsUnavailable = errors.New("glua events are unavailable")
	// ErrGluaEventsNotOpen 表示 State 尚未通过 OpenLibs 初始化 GLua Event 命名空间和 GC 根。
	ErrGluaEventsNotOpen = errors.New("glua events are not initialized; call lua.OpenLibs first")
	// ErrProgressEventInvalidArgument 表示公开 Go Event API 收到了非法参数。
	ErrProgressEventInvalidArgument = errors.New("invalid progress event argument")
	// ErrProgressEventConfigConflict 表示原始 Config 与类型化字段重复定义了同一配置。
	ErrProgressEventConfigConflict = errors.New("progress event config conflict")
	// ErrProgressEventLimitExceeded 表示 State 的 Event 监听器、队列或 drain 预算已经耗尽。
	ErrProgressEventLimitExceeded = errors.New("progress event limit exceeded")
	// ErrProgressEventQueueFull 表示单个异步监听器的队列已按 error 策略拒绝新任务。
	ErrProgressEventQueueFull = errors.New("progress event listener queue is full")
)

// ProgressEventLimitKind 表示触发的 Event State 级预算类型。
type ProgressEventLimitKind string

const (
	// ProgressEventLimitListeners 表示监听器总数达到 State 上限。
	ProgressEventLimitListeners ProgressEventLimitKind = "listeners"
	// ProgressEventLimitQueuedTasks 表示异步待执行任务总数达到 State 上限。
	ProgressEventLimitQueuedTasks ProgressEventLimitKind = "queued_tasks"
	// ProgressEventLimitDrainTasks 表示单次 drain 或 flush 达到执行任务上限。
	ProgressEventLimitDrainTasks ProgressEventLimitKind = "drain_tasks"
)

// ProgressEventArgumentError 描述公开 Go Event API 的具体非法参数。
type ProgressEventArgumentError struct {
	// Field 是发生错误的参数或配置字段名。
	Field string
	// Message 是面向日志的具体错误说明。
	Message string
}

// Error 返回包含字段名的 Event 参数错误文本。
func (eventError *ProgressEventArgumentError) Error() string {
	// nil 错误没有可展示信息。
	if eventError == nil {
		// nil 接收者保持空字符串，避免错误格式化 panic。
		return ""
	}
	if eventError.Field == "" {
		// 没有单一字段时直接返回整体错误说明。
		return eventError.Message
	}
	return fmt.Sprintf("progress event %s: %s", eventError.Field, eventError.Message)
}

// Unwrap 返回稳定的非法参数错误分类。
func (eventError *ProgressEventArgumentError) Unwrap() error {
	// 所有该类型实例都归入公开非法参数 sentinel。
	return ErrProgressEventInvalidArgument
}

// ProgressEventConfigConflictError 描述原始 Config 与类型化配置的重复字段。
type ProgressEventConfigConflictError struct {
	// Field 是发生重复定义的规范字段名。
	Field string
}

// Error 返回配置字段冲突文本。
func (eventError *ProgressEventConfigConflictError) Error() string {
	// nil 接收者没有字段信息。
	if eventError == nil {
		// 返回稳定分类文本，避免格式化 nil 指针。
		return ErrProgressEventConfigConflict.Error()
	}
	return fmt.Sprintf("progress event Config.%s conflicts with typed option %s", eventError.Field, eventError.Field)
}

// Unwrap 返回稳定的配置冲突错误分类。
func (eventError *ProgressEventConfigConflictError) Unwrap() error {
	// 允许宿主使用 errors.Is 识别所有字段冲突。
	return ErrProgressEventConfigConflict
}

// ProgressEventLimitError 描述一次 State 级 Event 预算超限。
type ProgressEventLimitError struct {
	// Kind 标识监听器、队列或 drain 预算。
	Kind ProgressEventLimitKind
	// Limit 是当前 State 配置上限。
	Limit int
	// Actual 是被拒绝时请求达到的数量。
	Actual int
}

// ProgressEventQueueFullError 描述单个监听器因 overflow=error 拒绝异步任务的详细信息。
type ProgressEventQueueFullError struct {
	// EventID 是拒绝入队的监听器编号。
	EventID int64
	// Limit 是该监听器配置的异步队列上限。
	Limit int
	// Pending 是拒绝发生时该监听器已经等待执行的任务数。
	Pending int
}

// Error 返回可直接记录的单监听器队列满错误文本。
func (eventError *ProgressEventQueueFullError) Error() string {
	// nil 接收者没有可展示的具体监听器信息。
	if eventError == nil {
		// 返回稳定分类文本，避免日志格式化 nil 指针。
		return ErrProgressEventQueueFull.Error()
	}
	return fmt.Sprintf("progress event listener queue is full: eventID=%d limit=%d pending=%d", eventError.EventID, eventError.Limit, eventError.Pending)
}

// Unwrap 返回稳定的单监听器队列满错误分类。
func (eventError *ProgressEventQueueFullError) Unwrap() error {
	// 调用方可用 errors.Is 识别 error 溢出策略，而不匹配内部 Lua 错误文本。
	return ErrProgressEventQueueFull
}

// Error 返回可直接记录的 Event 预算错误文本。
func (eventError *ProgressEventLimitError) Error() string {
	// nil 接收者没有具体预算信息。
	if eventError == nil {
		// 返回稳定分类文本，避免调用方日志 panic。
		return ErrProgressEventLimitExceeded.Error()
	}
	return fmt.Sprintf("progress event %s limit exceeded: limit=%d actual=%d", eventError.Kind, eventError.Limit, eventError.Actual)
}

// Unwrap 返回稳定的 Event 预算超限分类。
func (eventError *ProgressEventLimitError) Unwrap() error {
	// 所有预算类型都可以先通过 errors.Is 做粗粒度识别。
	return ErrProgressEventLimitExceeded
}

// ProgressEventOptions 保存 Go 注册进度事件时使用的范围、异步和高级 Lua 配置。
//
// Scope 为空时使用 runtime；Config 可传入 Lua table 复用 priority、group、过滤和可靠性配置。
// Async=true 时回调进入 State 事件队列，由 VM 安全点或 FlushProgressEvents 消费。
type ProgressEventOptions struct {
	// Scope 控制监听整个 State 还是仅监听 Source。
	Scope ProgressEventScope
	// Async 表示回调异步入队而不阻塞触发位置。
	Async bool
	// Config 保存可选 Lua 配置 table；零值等价于未提供配置。
	Config Value
	// Once 表示成功占用一次派发后自动删除监听器。
	Once bool
	// MaxCalls 限制最多实际执行次数，0 表示不限制。
	MaxCalls int64
	// Priority 越大越先派发，相同优先级按事件 ID 排序。
	Priority int64
	// Group 保存监听器分组名称。
	Group string
	// QueueLimit 限制异步监听器待处理任务数，0 表示不限制。
	QueueLimit int
	// Overflow 设置 drop_oldest、drop_newest 或 error 队列溢出策略。
	Overflow string
	// OnError 设置 propagate、ignore、mute 或 remove 错误策略。
	OnError string
	// Mutable 允许回调保留业务 table 的可变引用，默认使用只读快照。
	Mutable bool
	// Throttle 设置前沿节流窗口，0 表示关闭。
	Throttle time.Duration
	// Debounce 设置异步防抖窗口，只能用于异步监听器。
	Debounce time.Duration
	// SampleRate 设置确定性采样比例；nil 使用默认值 1，指向 0 表示全部采样丢弃。
	SampleRate *float64
	// WhitelistNames 保存函数进度事件允许的调试名称。
	WhitelistNames []string
	// WhitelistFunctions 保存函数进度事件允许的精确函数值。
	WhitelistFunctions []Value
	// BlacklistNames 保存函数进度事件排除的调试名称。
	BlacklistNames []string
	// BlacklistFunctions 保存函数进度事件排除的精确函数值。
	BlacklistFunctions []Value
}

// ProgressEventOptionsPatch 表示监听器配置的部分更新。
//
// 每个字段使用指针区分“保持原值”和“显式设置零值”。Patch 不改变异步注册属性；需要改 callback 时使用
// SetProgressEventCallback，更新过滤器或未列出的 legacy 字段时使用完整的 SetProgressEventOptions。
type ProgressEventOptionsPatch struct {
	// Scope 非 nil 时更新 runtime 或 file 监听范围。
	Scope *ProgressEventScope
	// Once 非 nil 时显式启用或关闭单次监听。
	Once *bool
	// MaxCalls 非 nil 时更新最大执行次数，指向 0 表示取消次数限制。
	MaxCalls *int64
	// Priority 非 nil 时更新调度优先级，指向 0 表示默认优先级。
	Priority *int64
	// Group 非 nil 时更新分组，指向空字符串表示移除分组。
	Group *string
	// QueueLimit 非 nil 时更新单监听器异步队列上限，指向 0 表示不限制。
	QueueLimit *int
	// Overflow 非 nil 时更新队列溢出策略。
	Overflow *string
	// OnError 非 nil 时更新 callback 错误治理策略。
	OnError *string
	// Mutable 非 nil 时显式切换业务 table 快照是否可变。
	Mutable *bool
	// Throttle 非 nil 时更新节流窗口，指向 0 表示关闭节流。
	Throttle *time.Duration
	// Debounce 非 nil 时更新异步防抖窗口，指向 0 表示关闭防抖。
	Debounce *time.Duration
	// SampleRate 非 nil 时更新确定性采样比例，指向 0 表示全部采样丢弃。
	SampleRate *float64
}

// ProgressEventListener 是 Go 侧可读取的监听器状态快照。
type ProgressEventListener struct {
	// ID 是当前 State 内稳定的监听器编号。
	ID int64
	// Event 是监听的预设或自定义事件名。
	Event string
	// Source 是监听器注册来源。
	Source string
	// Scope 是 runtime 或 file 匹配范围。
	Scope ProgressEventScope
	// Async 表示监听器回调通过安全点队列执行。
	Async bool
	// Muted 表示监听器当前被静音。
	Muted bool
	// Group 是监听器分组名称。
	Group string
	// Priority 是监听器派发优先级。
	Priority int64
	// Once 表示监听器最多成功派发一次。
	Once bool
	// MaxCalls 是监听器配置的最大执行次数，0 表示不限制。
	MaxCalls int64
	// RemainingCalls 是剩余执行次数，-1 表示不限制。
	RemainingCalls int64
	// Throttle 是前沿节流窗口。
	Throttle time.Duration
	// Debounce 是异步防抖窗口。
	Debounce time.Duration
	// SampleRate 是确定性采样比例。
	SampleRate float64
	// OnError 是 callback 错误治理策略。
	OnError string
	// DispatchCount 是已经完成的回调次数。
	DispatchCount int64
	// ErrorCount 是回调错误累计次数。
	ErrorCount int64
	// DroppedCount 是队列溢出丢弃次数。
	DroppedCount int64
	// RejectedCount 是 overflow=error 拒绝新异步任务的次数。
	RejectedCount int64
	// SuppressedCount 是限次、节流、采样或防抖抑制次数。
	SuppressedCount int64
	// ThrottledCount 是被节流抑制的触发次数。
	ThrottledCount int64
	// SampledOutCount 是被采样丢弃的触发次数。
	SampledOutCount int64
	// DebouncedCount 是被防抖合并的触发次数。
	DebouncedCount int64
	// TotalDuration 是 callback 累计执行耗时。
	TotalDuration time.Duration
	// AverageDuration 是 callback 平均执行耗时。
	AverageDuration time.Duration
	// Raw 保存完整 Lua 状态快照。
	Raw Value
}

// ProgressEventNameSummary 保存单个事件名聚合后的监听器和执行统计。
type ProgressEventNameSummary struct {
	// Event 是预设或自定义事件名。
	Event string
	// Listeners 是当前匹配的监听器总数。
	Listeners int
	// Active 是未静音监听器数量。
	Active int
	// Muted 是静音监听器数量。
	Muted int
	// Sync 是同步监听器数量。
	Sync int
	// Async 是异步监听器数量。
	Async int
	// DispatchCount 是累计 callback 执行次数。
	DispatchCount int64
	// ErrorCount 是累计 callback 错误次数。
	ErrorCount int64
	// DroppedCount 是累计队列丢弃次数。
	DroppedCount int64
	// RejectedCount 是累计因 overflow=error 拒绝入队的次数。
	RejectedCount int64
	// SuppressedCount 是累计可靠性策略抑制次数。
	SuppressedCount int64
	// TotalDuration 是该事件全部 callback 的累计耗时。
	TotalDuration time.Duration
}

// ProgressEventSummary 保存指定 source 可见的类型化 Event 统计快照。
type ProgressEventSummary struct {
	// Source 是查询 file scope 匹配时使用的源码标识。
	Source string
	// TotalEvents 是当前可见事件名数量。
	TotalEvents int
	// TotalListeners 是当前可见监听器总数。
	TotalListeners int
	// ActiveListeners 是未静音监听器数量。
	ActiveListeners int
	// MutedListeners 是静音监听器数量。
	MutedListeners int
	// SyncListeners 是同步监听器数量。
	SyncListeners int
	// AsyncListeners 是异步监听器数量。
	AsyncListeners int
	// QueuedTasks 是当前 State 全局异步队列长度。
	QueuedTasks int
	// DroppedTasks 是 State 累计丢弃任务数。
	DroppedTasks int64
	// RejectedTasks 是 State 累计因监听器 error 溢出策略拒绝的任务数。
	RejectedTasks int64
	// CallbackErrors 是 State 累计 callback 错误数。
	CallbackErrors int64
	// SuppressedEvents 是 State 累计被抑制触发数。
	SuppressedEvents int64
	// DebouncedTasks 是 State 累计防抖合并任务数。
	DebouncedTasks int64
	// DrainedTasks 是异步队列已经取出并处理的任务数，包含失效监听器任务。
	DrainedTasks int64
	// QueuedTaskDuration 是异步任务累计排队等待时间。
	QueuedTaskDuration time.Duration
	// AverageQueuedTaskDuration 是已处理异步任务的平均排队等待时间。
	AverageQueuedTaskDuration time.Duration
	// MaxQueuedTaskDuration 是已处理异步任务的最大排队等待时间。
	MaxQueuedTaskDuration time.Duration
	// LastDrainAt 是最近一次异步任务离开队列的时间，零值表示尚未 drain。
	LastDrainAt time.Time
	// LastCallbackError 是最近一次事件回调错误文本，空字符串表示尚未出现错误。
	LastCallbackError string
	// LastCallbackErrorAt 是最近一次事件回调错误时间，零值表示尚未出现错误。
	LastCallbackErrorAt time.Time
	// Sequence 是 State 当前事件序号。
	Sequence int64
	// TraceSequence 是 State 当前调用链序号。
	TraceSequence int64
	// ListenerLimit 是 State 允许的监听器总数。
	ListenerLimit int
	// QueuedTaskLimit 是 State 允许的异步待执行任务总数。
	QueuedTaskLimit int
	// TasksPerDrainLimit 是单次安全点或 flush 允许处理的任务数。
	TasksPerDrainLimit int
	// Events 按事件名升序保存聚合统计。
	Events []ProgressEventNameSummary
	// Raw 保存与 Lua event.eventList 相同的完整 table。
	Raw Value
}

// FileProgressEventSource 把宿主文件路径转换为 Lua `@file` Source。
//
// path 不能为空且不能包含 NUL；返回值使用当前宿主平台的 filepath.Clean 词法规则，不访问文件系统。
func FileProgressEventSource(path string) (string, error) {
	// 文件 Source 只做词法清理，不判断文件是否存在。
	if path == "" || strings.ContainsRune(path, '\x00') {
		// 空路径或 NUL 无法形成稳定 Proto.Source。
		return "", newProgressEventArgumentError("source", "file path must not be empty or contain NUL")
	}
	return "@" + filepath.Clean(path), nil
}

// ChunkProgressEventSource 把宿主定义的 chunk 名转换为 Lua `=name` Source。
//
// name 不能为空且不能包含 NUL；该 helper 适合内存脚本和 VFS 逻辑名称。
func ChunkProgressEventSource(name string) (string, error) {
	// chunk Source 保留调用方名称，不执行路径清理。
	if name == "" || strings.ContainsRune(name, '\x00') {
		// 空名称或 NUL 会破坏 Lua Source 展示和匹配。
		return "", newProgressEventArgumentError("source", "chunk name must not be empty or contain NUL")
	}
	return "=" + name, nil
}

// NormalizeProgressEventSource 规范化宿主传入的 Event Source。
//
// 已带 `@` 或 `=` 的 Source 原样保留；无前缀值按文件路径处理。空值、只有前缀或包含 NUL 时返回错误。
func NormalizeProgressEventSource(source string) (string, error) {
	// 先处理调用方已经使用 Lua chunk source 约定的值。
	if strings.HasPrefix(source, "@") || strings.HasPrefix(source, "=") {
		// 前缀后必须有内容，且 Source 不能包含 NUL。
		if len(source) == 1 || strings.ContainsRune(source, '\x00') {
			// 损坏前缀 Source 不执行猜测性修复。
			return "", newProgressEventArgumentError("source", "prefixed source must contain a name and no NUL")
		}
		return source, nil
	}
	return FileProgressEventSource(source)
}

// ProgressEventContext 是 Go callback 可直接读取的事件上下文快照。
//
// RawValue 保留完整 Lua table；Field 可读取未提升为结构字段的 args、results、locals 等扩展字段。
type ProgressEventContext struct {
	raw Value

	// Event 是预设或自定义事件名。
	Event string
	// Kind 是事件类别，当前进度事件固定为 progress。
	Kind string
	// Source 是实际产生事件的 Lua Proto.Source，而不是监听器注册来源。
	Source string
	// Payload 是触发方传入的 Lua 值。
	Payload Value
	// Timestamp 是 Unix 毫秒时间戳。
	Timestamp int64
	// Sequence 是当前 State 内单调递增的事件序号。
	Sequence int64
	// EventID 与 Sequence 相同，用于调用链关联。
	EventID int64
	// TraceID 标识当前 State 内的一条执行链。
	TraceID int64
	// ParentEventID 是最近父事件编号；根事件为 0。
	ParentEventID int64
	// Depth 是触发事件时的调用帧深度。
	Depth int64
	// ListenerID 是接收当前上下文的监听器 ID。
	ListenerID int64
	// Async 表示当前回调是否按异步任务执行。
	Async bool
	// Scope 是当前监听器的匹配范围。
	Scope ProgressEventScope
}

// RawValue 返回完整只读 Lua 事件上下文 table。
//
// 返回值可继续传回 Lua；调用方不应修改其内部 table，默认事件上下文会冻结嵌套业务 table。
func (context ProgressEventContext) RawValue() Value {
	// 原样返回构造上下文时保存的 Lua 值。
	return context.raw
}

// Field 按字段名读取完整 Lua 事件上下文中的值。
//
// name 为空、上下文损坏或字段不存在时返回 Lua nil；读取不会触发元方法。
func (context ProgressEventContext) Field(name string) Value {
	// 只允许从有效 table 上执行 raw 字段读取。
	if name == "" || context.raw.Kind != KindTable {
		// 无效字段名或上下文没有可读取字段。
		return runtime.NilValue()
	}
	table, _ := context.raw.Ref.(*runtime.Table)
	if table == nil {
		// 损坏 table 引用按字段不存在处理。
		return runtime.NilValue()
	}
	return table.RawGetString(name)
}

// ProgressEventCallback 表示 Go 侧进度事件回调。
//
// callback 返回 error 时沿用监听器 onError 策略；同步 propagate 会中止触发路径。
type ProgressEventCallback func(context ProgressEventContext) error

// ProgressEventTrace 保存一次 Event callback 完成后的只读观测记录。
//
// 该记录在 callback 返回后生成，因此不会改变 Lua 事件上下文、调度顺序或错误治理；Error 为空表示 callback
// 成功。Trace hook 应快速返回，避免把监控 I/O 放在 VM 安全点执行路径。
type ProgressEventTrace struct {
	// EventID 是监听器稳定编号。
	EventID int64
	// Event 是预设或自定义事件名。
	Event string
	// Source 是触发回调的源码来源。
	Source string
	// Async 表示本次回调是否经异步队列执行。
	Async bool
	// Timestamp 是 callback 完成时的 Unix 毫秒时间戳。
	Timestamp int64
	// Duration 是 callback 实际执行耗时。
	Duration time.Duration
	// Error 保存 callback 错误文本，成功时为空字符串。
	Error string
}

// ProgressEventTraceHook 接收一次已完成的 Event callback 观测记录。
//
// hook 不得调用同一 State 的 Lua API 或阻塞执行；hook panic 会被 Event 运行时恢复并忽略，避免监控代码中断 Lua。
type ProgressEventTraceHook func(trace ProgressEventTrace)

// SetProgressEvent 从 Go 注册一个进度事件监听器并返回事件 ID。
//
// source 必须与 Lua Proto.Source 完全一致；event 不能为空；callback 不能为空。默认监听当前 State
// 内全部 Lua 文件，options.Scope=file 时仅监听 source。State 必须先调用 OpenLibs。
func SetProgressEvent(state *State, source string, event string, callback ProgressEventCallback, options ...ProgressEventOptions) (int64, error) {
	// 先把类型化 Go callback 包装成现有 Lua callable，确保派发和错误策略完全复用。
	callbackValue, err := progressEventCallbackValue(callback)
	if err != nil {
		// nil callback 不能形成可调用监听器。
		return 0, err
	}
	return SetProgressEventValue(state, source, event, callbackValue, options...)
}

// SetProgressEventValue 从 Go 注册已有 Lua 或 Go closure 值并返回事件 ID。
//
// callback 必须是 KindLuaClosure 或 KindGoClosure；其他约束与 SetProgressEvent 相同。
func SetProgressEventValue(state *State, source string, event string, callback Value, options ...ProgressEventOptions) (int64, error) {
	// 统一解析可选参数后进入条件编译实现，避免调用方触碰 runtime registry。
	parsedOptions, err := prepareProgressEventOptions(options)
	if err != nil {
		// 参数数量或 scope 错误直接返回给 Go 调用方。
		return 0, err
	}
	return setGluaProgressEventForSource(state, source, event, callback, parsedOptions)
}

// RemoveProgressEvent 从 Go 删除监听器及其尚未执行的异步任务。
func RemoveProgressEvent(state *State, eventID int64) (bool, error) {
	// 条件编译实现负责 State 校验和注册表删除。
	return removeGluaProgressEventFromGo(state, eventID)
}

// SetProgressEventMuted 从 Go 静音或恢复指定监听器。
func SetProgressEventMuted(state *State, eventID int64, muted bool) (bool, error) {
	// 静音只改变后续派发，不删除监听器和统计。
	return setGluaProgressEventMutedFromGo(state, eventID, muted)
}

// SetProgressEventCallback 从 Go 替换指定监听器的类型化 callback。
func SetProgressEventCallback(state *State, eventID int64, callback ProgressEventCallback) (bool, error) {
	// 类型化 callback 先转换为 VM callable，再进入统一更新路径。
	callbackValue, err := progressEventCallbackValue(callback)
	if err != nil {
		// nil callback 不覆盖现有监听器。
		return false, err
	}
	return SetProgressEventCallbackValue(state, eventID, callbackValue)
}

// SetProgressEventCallbackValue 从 Go 替换指定监听器的 Lua 或 Go closure。
func SetProgressEventCallbackValue(state *State, eventID int64, callback Value) (bool, error) {
	// 条件编译实现校验 callback 类型并刷新 GC 根。
	return setGluaProgressEventCallbackFromGo(state, eventID, callback)
}

// SetProgressEventTraceHook 设置当前 State 的 Event callback 观测 hook。
//
// hook 为 nil 时清除已有 hook；State 必须已 OpenLibs。hook 在 callback 返回后同步执行，panic 会被恢复并忽略，
// 因此调用方应仅做非阻塞指标采集或自行转交后台队列。
func SetProgressEventTraceHook(state *State, hook ProgressEventTraceHook) error {
	// 条件编译实现把 hook 保存到当前 State 的 Event registry。
	return setGluaProgressEventTraceHook(state, hook)
}

// SetProgressEventOptions 从 Go 替换监听器作用域、过滤器和可靠性配置。
//
// Async 在注册后不可修改；传入零值会恢复 runtime scope 和其他默认可靠性配置。
func SetProgressEventOptions(state *State, eventID int64, options ProgressEventOptions) (bool, error) {
	// 单个配置复用注册时的类型化构造和校验规则。
	if options.Async {
		// 同步与异步注册使用不同派发路径，注册后不能通过配置切换。
		return false, newProgressEventArgumentError("Async", "cannot be changed after registration")
	}
	prepared, err := prepareProgressEventOptions([]ProgressEventOptions{options})
	if err != nil {
		// 无效配置不修改现有监听器。
		return false, err
	}
	return setGluaProgressEventOptionsFromGo(state, eventID, prepared)
}

// PatchProgressEventOptions 从 Go 部分更新监听器配置，并允许显式写入 false、0 或空字符串。
//
// state 必须已 OpenLibs；eventID 必须为正数且属于当前 State。返回 false, nil 表示监听器不存在或已删除；
// 参数、配置和异步防抖约束错误会原样返回，更新失败时原监听器配置保持不变。
func PatchProgressEventOptions(state *State, eventID int64, patch ProgressEventOptionsPatch) (bool, error) {
	// 先读取当前原始配置并在独立 table 上合并，避免部分更新覆盖未声明字段。
	value, err := getGluaProgressEventFromGo(state, eventID)
	if err != nil {
		// State 或 id 前置条件错误直接返回。
		return false, err
	}
	if value.IsNil() {
		// 已删除监听器没有可更新配置。
		return false, nil
	}
	snapshot, err := progressEventValueTable(value, "listener")
	if err != nil {
		// 损坏的监听器快照不能安全合并配置。
		return false, err
	}
	config, err := cloneProgressEventConfigTable(snapshot.RawGetString("config"))
	if err != nil {
		// 原始配置损坏时不执行任何更新。
		return false, err
	}
	if err := applyProgressEventOptionsPatch(config, patch); err != nil {
		// Patch 值非法时保持原监听器配置。
		return false, err
	}
	prepared, err := prepareProgressEventOptions([]ProgressEventOptions{{Config: runtime.ReferenceValue(runtime.KindTable, config)}})
	if err != nil {
		// 统一解析器负责校验枚举、冲突和过滤器边界。
		return false, err
	}
	return setGluaProgressEventOptionsFromGo(state, eventID, prepared)
}

// GetProgressEvent 从 Go 返回指定监听器的类型化状态快照。
func GetProgressEvent(state *State, eventID int64) (ProgressEventListener, bool, error) {
	// 条件编译实现先生成与 Lua event.get 相同的原始快照。
	value, err := getGluaProgressEventFromGo(state, eventID)
	if err != nil {
		// State 或 Event 不可用时返回错误。
		return ProgressEventListener{}, false, err
	}
	if value.IsNil() {
		// 未找到监听器不是运行错误。
		return ProgressEventListener{}, false, nil
	}
	listener, err := decodeProgressEventListener(value)
	if err != nil {
		// 损坏快照不能伪装成未找到。
		return ProgressEventListener{}, false, err
	}
	return listener, true, nil
}

// ListProgressEvents 从 Go 返回对指定 source 生效的类型化监听器和队列统计。
func ListProgressEvents(state *State, source string) (ProgressEventSummary, error) {
	// 先读取原始 Lua table，再解码为第三方宿主可直接使用的稳定结构。
	value, err := listGluaProgressEventsFromGo(state, source)
	if err != nil {
		// State、Source 或 Event 生命周期错误直接返回。
		return ProgressEventSummary{}, err
	}
	return decodeProgressEventSummary(value)
}

// ListProgressEventsRaw 从 Go 返回与 Lua event.eventList 相同的原始统计 table。
//
// 普通宿主应优先使用 ListProgressEvents；该入口用于读取未来新增但尚未类型化的字段。
func ListProgressEventsRaw(state *State, source string) (Value, error) {
	// 高级入口保留原始 table，不执行字段解码。
	return listGluaProgressEventsFromGo(state, source)
}

// ClearProgressEvents 从 Go 删除指定注册来源下的全部或同名监听器。
func ClearProgressEvents(state *State, source string, event string) (int, error) {
	// event 为空时清理该注册来源的全部监听器。
	return clearGluaProgressEventsFromGo(state, source, event, "")
}

// SetProgressEventGroupMuted 从 Go 批量静音或恢复注册来源下的监听器分组。
func SetProgressEventGroupMuted(state *State, source string, group string, muted bool) (int, error) {
	// 分组操作按注册所有权而不是 runtime 匹配范围执行。
	return setGluaProgressEventGroupMutedFromGo(state, source, group, muted)
}

// RemoveProgressEventGroup 从 Go 删除注册来源下的监听器分组。
func RemoveProgressEventGroup(state *State, source string, group string) (int, error) {
	// 复用清理入口并使用 group 作为筛选条件。
	return clearGluaProgressEventsFromGo(state, source, "", group)
}

// CallProgressEvent 从 Go 同步触发指定 Lua source 的自定义事件。
//
// source 决定 file 监听器匹配和上下文 source；runtime 监听器始终参与。payload 可省略，最多一个。
func CallProgressEvent(state *State, source string, event string, payload ...Value) error {
	// 同步入口委托统一触发函数，回调完成前不会返回。
	return callProgressEventFromGo(state, source, event, false, payload)
}

// CallProgressEventAsync 从 Go 异步触发指定 Lua source 的自定义事件。
//
// 匹配回调只进入 State 队列；后续 VM 安全点或 FlushProgressEvents 负责执行。
func CallProgressEventAsync(state *State, source string, event string, payload ...Value) error {
	// 异步入口强制所有匹配回调入队，不在当前 Go 调用栈执行 Lua callback。
	return callProgressEventFromGo(state, source, event, true, payload)
}

// FlushProgressEvents 立即消费当前 State 已接受的异步事件任务。
//
// 返回实际执行数量；回调错误按各监听器 onError 策略返回。未启用 Event 时返回明确错误。
func FlushProgressEvents(state *State) (int, error) {
	// 条件编译实现负责检查 State 配置并在 VM 安全边界同步执行队列。
	return flushGluaProgressEventsFromGo(state)
}

// normalizeProgressEventOptions 校验 Go API 的可选配置并补齐默认 runtime scope。
func normalizeProgressEventOptions(options []ProgressEventOptions) (ProgressEventOptions, error) {
	// 可选参数保持调用简洁，同时拒绝多个配置造成覆盖歧义。
	if len(options) > 1 {
		// 多个 options 没有稳定合并规则。
		return ProgressEventOptions{}, newProgressEventArgumentError("options", "accepts at most one value")
	}
	result := ProgressEventOptions{Scope: ProgressEventScopeRuntime}
	if len(options) == 1 {
		// 单个 options 按值复制，避免调用方后续修改影响注册结果。
		result = options[0]
		if result.Scope == "" {
			// 空 scope 使用与 Lua setProgress 相同的 runtime 默认值。
			result.Scope = ProgressEventScopeRuntime
		}
	}
	if result.Scope != ProgressEventScopeRuntime && result.Scope != ProgressEventScopeFile {
		// 未知 scope 不能静默回退，否则监听范围可能意外扩大。
		return ProgressEventOptions{}, newProgressEventArgumentError("scope", fmt.Sprintf("must be %q or %q", ProgressEventScopeRuntime, ProgressEventScopeFile))
	}
	return result, nil
}

// prepareProgressEventOptions 校验并把类型化字段合并为内部 Lua config table。
func prepareProgressEventOptions(options []ProgressEventOptions) (ProgressEventOptions, error) {
	// 先补齐 scope，再构造不会修改调用方原 table 的配置副本。
	rawOptions := ProgressEventOptions{}
	if len(options) == 1 {
		// 保留调用方是否显式设置类型化字段的信息，用于检测与 Config 的重复定义。
		rawOptions = options[0]
	}
	result, err := normalizeProgressEventOptions(options)
	if err != nil {
		// scope 或参数数量错误直接返回。
		return ProgressEventOptions{}, err
	}
	configTable, err := cloneProgressEventConfigTable(result.Config)
	if err != nil {
		// Config 必须是有效 table 或 nil。
		return ProgressEventOptions{}, err
	}
	if err := ensureProgressEventConfigNoTypedConflicts(configTable, rawOptions); err != nil {
		// 重复定义不再按隐式覆盖顺序处理，调用方必须选择一种配置入口。
		return ProgressEventOptions{}, err
	}
	if rawOptions.Scope != "" {
		// 显式类型化 scope 写入配置，供 Lua callback 的 config 字段保持一致。
		configTable.RawSetString("scope", runtime.StringValue(string(result.Scope)))
	} else if configuredScope := configTable.RawGetString("scope"); !configuredScope.IsNil() {
		// 仅使用原始 Config 时保留其 scope，同时执行与类型化入口相同的枚举校验。
		if configuredScope.Kind != KindString || (configuredScope.String != string(ProgressEventScopeRuntime) && configuredScope.String != string(ProgressEventScopeFile)) {
			// 非法 raw scope 不能被默认 runtime 静默覆盖。
			return ProgressEventOptions{}, newProgressEventArgumentError("Config.scope", "must be runtime or file")
		}
		result.Scope = ProgressEventScope(configuredScope.String)
	} else {
		// 没有 raw scope 时写入规范默认值，保证 callback 配置快照完整。
		configTable.RawSetString("scope", runtime.StringValue(string(result.Scope)))
	}
	if result.Once {
		// true 才覆盖原始配置；false 保留 Config 显式值或默认 false。
		configTable.RawSetString("once", runtime.BooleanValue(true))
	}
	if result.MaxCalls != 0 {
		// 非零执行上限交由统一解析器验证正负范围。
		configTable.RawSetString("maxCalls", runtime.IntegerValue(result.MaxCalls))
	}
	if result.Priority != 0 {
		// 非零优先级覆盖高级 Config。
		configTable.RawSetString("priority", runtime.IntegerValue(result.Priority))
	}
	if result.Group != "" {
		// 非空分组覆盖高级 Config。
		configTable.RawSetString("group", runtime.StringValue(result.Group))
	}
	if result.QueueLimit != 0 {
		// 非零队列上限交由统一解析器验证。
		configTable.RawSetString("queueLimit", runtime.IntegerValue(int64(result.QueueLimit)))
	}
	if result.Overflow != "" {
		// 溢出策略字符串由统一解析器验证枚举值。
		configTable.RawSetString("overflow", runtime.StringValue(result.Overflow))
	}
	if result.OnError != "" {
		// 错误策略字符串由统一解析器验证枚举值。
		configTable.RawSetString("onError", runtime.StringValue(result.OnError))
	}
	if result.Mutable {
		// true 才允许业务 table 可变引用。
		configTable.RawSetString("mutable", runtime.BooleanValue(true))
	}
	if err := setProgressEventDuration(configTable, "throttleMs", result.Throttle); err != nil {
		// 非法节流时长不进入注册表。
		return ProgressEventOptions{}, err
	}
	if err := setProgressEventDuration(configTable, "debounceMs", result.Debounce); err != nil {
		// 非法防抖时长不进入注册表。
		return ProgressEventOptions{}, err
	}
	if result.SampleRate != nil {
		// 指针允许调用方显式设置 0 采样率。
		configTable.RawSetString("sampleRate", runtime.NumberValue(*result.SampleRate))
	}
	if err := setProgressEventFilterConfig(configTable, "whitelist", result.WhitelistNames, result.WhitelistFunctions); err != nil {
		// 非函数白名单值不进入注册表。
		return ProgressEventOptions{}, err
	}
	if err := setProgressEventFilterConfig(configTable, "blacklist", result.BlacklistNames, result.BlacklistFunctions); err != nil {
		// 非函数黑名单值不进入注册表。
		return ProgressEventOptions{}, err
	}
	result.Config = runtime.ReferenceValue(runtime.KindTable, configTable)
	return result, nil
}

// cloneProgressEventConfigTable 复制高级 Config，避免规范化时修改调用方 table。
func cloneProgressEventConfigTable(config Value) (*runtime.Table, error) {
	// nil 和 Value 零值都创建空配置表。
	result := runtime.NewTable()
	if config.Kind == 0 || config.IsNil() {
		// 空配置直接返回新 table。
		return result, nil
	}
	if config.Kind != KindTable {
		// 非 table 高级配置无法合并。
		return nil, newProgressEventArgumentError("Config", "must be a Lua table")
	}
	source, _ := config.Ref.(*runtime.Table)
	if source == nil {
		// 损坏引用不能安全复制。
		return nil, newProgressEventArgumentError("Config", "contains an invalid table reference")
	}
	key := runtime.NilValue()
	for {
		// RawNext 复制全部高级字段并保留函数引用身份。
		nextKey, nextValue, ok, err := source.RawNext(key)
		if err != nil {
			// table 迭代错误直接返回。
			return nil, err
		}
		if !ok {
			// 遍历完成后返回独立 table。
			break
		}
		if err := result.RawSet(nextKey, nextValue); err != nil {
			// 无效键值不应被静默忽略。
			return nil, err
		}
		key = nextKey
	}
	return result, nil
}

// ensureProgressEventConfigNoTypedConflicts 拒绝原始 Config 与类型化字段重复定义。
//
// configTable 是已经复制的原始配置；options 保留调用方输入零值，用于判断字段是否显式选择类型化入口。
func ensureProgressEventConfigNoTypedConflicts(configTable *runtime.Table, options ProgressEventOptions) error {
	// 每个类型化字段只在调用方给出非零值时占有对应配置名称。
	checks := []struct {
		field   string
		typed   bool
		aliases []string
	}{
		{field: "scope", typed: options.Scope != "", aliases: []string{"scope"}},
		{field: "once", typed: options.Once, aliases: []string{"once"}},
		{field: "maxCalls", typed: options.MaxCalls != 0, aliases: []string{"maxCalls"}},
		{field: "priority", typed: options.Priority != 0, aliases: []string{"priority"}},
		{field: "group", typed: options.Group != "", aliases: []string{"group"}},
		{field: "queueLimit", typed: options.QueueLimit != 0, aliases: []string{"queueLimit"}},
		{field: "overflow", typed: options.Overflow != "", aliases: []string{"overflow"}},
		{field: "onError", typed: options.OnError != "", aliases: []string{"onError"}},
		{field: "mutable", typed: options.Mutable, aliases: []string{"mutable"}},
		{field: "throttleMs", typed: options.Throttle != 0, aliases: []string{"throttleMs"}},
		{field: "debounceMs", typed: options.Debounce != 0, aliases: []string{"debounceMs"}},
		{field: "sampleRate", typed: options.SampleRate != nil, aliases: []string{"sampleRate"}},
		{field: "whitelist", typed: len(options.WhitelistNames) > 0 || len(options.WhitelistFunctions) > 0, aliases: []string{"whitelist", "whiteList", "include", "only"}},
		{field: "blacklist", typed: len(options.BlacklistNames) > 0 || len(options.BlacklistFunctions) > 0, aliases: []string{"blacklist", "blackList", "exclude", "ignore"}},
	}
	for _, check := range checks {
		// 未使用类型化字段时，原始 Config 继续拥有该配置。
		if !check.typed {
			// 允许 legacy 或自定义构造完全通过 Config 表达。
			continue
		}
		for _, alias := range check.aliases {
			// 任一规范名称或兼容别名重复都属于同一字段冲突。
			if !configTable.RawGetString(alias).IsNil() {
				// 不比较值是否相同，避免后续修改一侧时重新引入覆盖顺序。
				return &ProgressEventConfigConflictError{Field: check.field}
			}
		}
	}
	return nil
}

// setProgressEventDuration 把整毫秒 duration 写入配置 table。
func setProgressEventDuration(table *runtime.Table, field string, duration time.Duration) error {
	// 零值保留高级 Config 或内部默认值。
	if duration == 0 {
		// 没有类型化覆盖时不写字段。
		return nil
	}
	if duration < 0 || duration%time.Millisecond != 0 {
		// Event 配置协议使用非负整数毫秒，拒绝隐式截断。
		return newProgressEventArgumentError(field, "must be a non-negative whole millisecond duration")
	}
	table.RawSetString(field, runtime.IntegerValue(duration.Milliseconds()))
	return nil
}

// applyProgressEventOptionsPatch 把非 nil Patch 字段写入独立配置 table。
func applyProgressEventOptionsPatch(table *runtime.Table, patch ProgressEventOptionsPatch) error {
	// Patch 只能写入可变配置表；nil 表无法保证原子更新。
	if table == nil {
		// 内部调用路径损坏时返回公开参数错误。
		return newProgressEventArgumentError("patch", "requires a configuration table")
	}
	if patch.Scope != nil {
		// scope 通过统一解析器校验 runtime/file 枚举。
		table.RawSetString("scope", runtime.StringValue(string(*patch.Scope)))
	}
	if patch.Once != nil {
		// bool 指针允许显式关闭 once。
		table.RawSetString("once", runtime.BooleanValue(*patch.Once))
	}
	if patch.MaxCalls != nil {
		// 0 表示无限制，负值由解析器拒绝。
		table.RawSetString("maxCalls", runtime.IntegerValue(*patch.MaxCalls))
	}
	if patch.Priority != nil {
		// 0 表示默认优先级。
		table.RawSetString("priority", runtime.IntegerValue(*patch.Priority))
	}
	if patch.Group != nil {
		// 空字符串表示移除分组。
		table.RawSetString("group", runtime.StringValue(*patch.Group))
	}
	if patch.QueueLimit != nil {
		// 0 表示不限制，负值由解析器拒绝。
		table.RawSetString("queueLimit", runtime.IntegerValue(int64(*patch.QueueLimit)))
	}
	if patch.Overflow != nil {
		// 枚举字符串由解析器拒绝未知值。
		table.RawSetString("overflow", runtime.StringValue(*patch.Overflow))
	}
	if patch.OnError != nil {
		// 错误治理策略统一交给现有枚举校验。
		table.RawSetString("onError", runtime.StringValue(*patch.OnError))
	}
	if patch.Mutable != nil {
		// bool 指针允许显式恢复只读快照行为。
		table.RawSetString("mutable", runtime.BooleanValue(*patch.Mutable))
	}
	if patch.Throttle != nil {
		// 指向 0 的 duration 会关闭节流，而不是沿用旧值。
		if err := setProgressEventPatchDuration(table, "throttleMs", *patch.Throttle); err != nil {
			return err
		}
	}
	if patch.Debounce != nil {
		// 指向 0 的 duration 会关闭异步防抖。
		if err := setProgressEventPatchDuration(table, "debounceMs", *patch.Debounce); err != nil {
			return err
		}
	}
	if patch.SampleRate != nil {
		// sampleRate=0 具有明确的全部抑制语义。
		table.RawSetString("sampleRate", runtime.NumberValue(*patch.SampleRate))
	}
	return nil
}

// setProgressEventPatchDuration 把 Patch duration 写入配置，允许显式使用零值关闭窗口。
func setProgressEventPatchDuration(table *runtime.Table, field string, duration time.Duration) error {
	// Patch 的零值表示关闭，仍需写入 table 覆盖旧配置。
	if duration < 0 || duration%time.Millisecond != 0 {
		// 非整毫秒会导致 Lua 配置失真，必须拒绝。
		return newProgressEventArgumentError(field, "must be a non-negative whole millisecond duration")
	}
	table.RawSetString(field, runtime.IntegerValue(duration.Milliseconds()))
	return nil
}

// setProgressEventFilterConfig 把名称和函数值写入 Lua 过滤数组。
func setProgressEventFilterConfig(table *runtime.Table, field string, names []string, functions []Value) error {
	// 空类型化过滤器保留高级 Config 中的对应字段。
	if len(names) == 0 && len(functions) == 0 {
		// 没有覆盖内容时无需创建空数组。
		return nil
	}
	filterTable := runtime.NewTable()
	index := int64(1)
	for _, name := range names {
		// 空名称没有稳定匹配语义。
		if name == "" {
			// 拒绝空名称，避免配置看似生效却永远无法匹配。
			return newProgressEventArgumentError(field, "contains an empty function name")
		}
		filterTable.RawSetInteger(index, runtime.StringValue(name))
		index++
	}
	for _, function := range functions {
		// 精确过滤只接受 Lua 或 Go closure。
		if function.Kind != KindLuaClosure && function.Kind != KindGoClosure {
			// 非函数值不能形成函数身份过滤器。
			return newProgressEventArgumentError(field, "contains a non-function value")
		}
		filterTable.RawSetInteger(index, function)
		index++
	}
	table.RawSetString(field, runtime.ReferenceValue(runtime.KindTable, filterTable))
	return nil
}

// progressEventCallbackValue 把类型化 Go callback 包装为 VM callable。
func progressEventCallbackValue(callback ProgressEventCallback) (Value, error) {
	// nil callback 不能形成可调用监听器。
	if callback == nil {
		// 返回稳定 callable 错误供注册和替换共同使用。
		return runtime.NilValue(), ErrExpectedCallable
	}
	return runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 事件派发固定把上下文 table 作为第一个参数传入。
		if len(args) == 0 {
			// 缺少上下文说明调用路径损坏，返回稳定错误而不是 panic。
			return nil, runtime.RaiseError(runtime.StringValue("progress event callback requires context"))
		}
		context, err := decodeProgressEventContext(args[0])
		if err != nil {
			// 上下文解析错误进入既有 callback 错误治理路径。
			return nil, err
		}
		if err := callback(context); err != nil {
			// Go callback 错误按监听器 onError 策略处理。
			return nil, err
		}
		return nil, nil
	})), nil
}

// decodeProgressEventListener 把 Lua listener 快照转换为稳定 Go 结构。
func decodeProgressEventListener(value Value) (ProgressEventListener, error) {
	// 监听器快照必须是有效 table。
	if value.Kind != KindTable {
		// 非 table 不能解码为监听器。
		return ProgressEventListener{}, runtime.RaiseError(runtime.StringValue("progress event listener must be table"))
	}
	table, _ := value.Ref.(*runtime.Table)
	if table == nil {
		// 损坏引用不能安全读取。
		return ProgressEventListener{}, runtime.RaiseError(runtime.StringValue("progress event listener table is invalid"))
	}
	return ProgressEventListener{
		ID: table.RawGetString("id").Integer, Event: table.RawGetString("event").String,
		Source: table.RawGetString("source").String, Scope: ProgressEventScope(table.RawGetString("scope").String),
		Async: table.RawGetString("async").Bool, Muted: table.RawGetString("muted").Bool,
		Group: table.RawGetString("group").String, Priority: table.RawGetString("priority").Integer,
		Once: table.RawGetString("once").Bool, MaxCalls: table.RawGetString("maxCalls").Integer,
		RemainingCalls: table.RawGetString("remainingCalls").Integer,
		Throttle:       time.Duration(table.RawGetString("throttleMs").Integer) * time.Millisecond,
		Debounce:       time.Duration(table.RawGetString("debounceMs").Integer) * time.Millisecond,
		SampleRate:     table.RawGetString("sampleRate").Number, OnError: table.RawGetString("onError").String,
		DispatchCount: table.RawGetString("dispatchCount").Integer, ErrorCount: table.RawGetString("errorCount").Integer,
		DroppedCount: table.RawGetString("droppedCount").Integer, RejectedCount: table.RawGetString("rejectedCount").Integer,
		SuppressedCount: table.RawGetString("suppressedCount").Integer,
		ThrottledCount:  table.RawGetString("throttledCount").Integer,
		SampledOutCount: table.RawGetString("sampledOutCount").Integer,
		DebouncedCount:  table.RawGetString("debouncedCount").Integer,
		TotalDuration:   time.Duration(table.RawGetString("totalDurationNs").Integer),
		AverageDuration: time.Duration(table.RawGetString("averageDurationNs").Integer),
		Raw:             value,
	}, nil
}

// decodeProgressEventSummary 把 Lua eventList table 转换为类型化 Go 统计。
func decodeProgressEventSummary(value Value) (ProgressEventSummary, error) {
	// 顶层快照必须是有效 Lua table。
	table, err := progressEventValueTable(value, "summary")
	if err != nil {
		// 损坏统计不能返回看似有效的零值。
		return ProgressEventSummary{}, err
	}
	integerField := func(field string) int64 {
		// eventList 所有计数都使用 Lua integer，类型不匹配时保留零值。
		fieldValue := table.RawGetString(field)
		if fieldValue.Kind != KindInteger {
			// 未知或未来字段类型不执行隐式转换。
			return 0
		}
		return fieldValue.Integer
	}
	queuedTaskDuration := time.Duration(integerField("queuedTaskDurationNs"))
	lastDrainAtMs := integerField("lastDrainAtMs")
	lastCallbackErrorAtMs := integerField("lastCallbackErrorAtMs")
	summary := ProgressEventSummary{
		Source:      table.RawGetString("source").String,
		TotalEvents: int(integerField("totalEvents")), TotalListeners: int(integerField("totalListeners")),
		ActiveListeners: int(integerField("activeListeners")), MutedListeners: int(integerField("mutedListeners")),
		SyncListeners: int(integerField("syncListeners")), AsyncListeners: int(integerField("asyncListeners")),
		QueuedTasks: int(integerField("queuedTasks")), DroppedTasks: integerField("droppedTasks"), RejectedTasks: integerField("rejectedTasks"),
		CallbackErrors: integerField("callbackErrors"), SuppressedEvents: integerField("suppressedEvents"),
		DebouncedTasks: integerField("debouncedTasks"), DrainedTasks: integerField("drainedTasks"),
		QueuedTaskDuration: queuedTaskDuration, MaxQueuedTaskDuration: time.Duration(integerField("maxQueuedTaskDurationNs")),
		LastCallbackError: table.RawGetString("lastCallbackError").String,
		Sequence:          integerField("sequence"),
		TraceSequence:     integerField("traceSequence"), ListenerLimit: int(integerField("listenerLimit")),
		QueuedTaskLimit: int(integerField("queuedTaskLimit")), TasksPerDrainLimit: int(integerField("tasksPerDrainLimit")), Raw: value,
	}
	if summary.DrainedTasks > 0 {
		// 已处理任务数非零时才计算平均等待，避免零除并保留零值语义。
		summary.AverageQueuedTaskDuration = queuedTaskDuration / time.Duration(summary.DrainedTasks)
	}
	if lastDrainAtMs > 0 {
		// Unix 毫秒快照转换为 Go 时间，零值继续表示尚未 drain。
		summary.LastDrainAt = time.UnixMilli(lastDrainAtMs)
	}
	if lastCallbackErrorAtMs > 0 {
		// Unix 毫秒快照转换为 Go 时间，零值继续表示尚未出现 callback 错误。
		summary.LastCallbackErrorAt = time.UnixMilli(lastCallbackErrorAtMs)
	}
	eventsValue := table.RawGetString("events")
	if eventsValue.IsNil() {
		// 空统计允许省略事件数组。
		return summary, nil
	}
	eventsTable, err := progressEventValueTable(eventsValue, "summary.events")
	if err != nil {
		// 非 table 事件列表表示内部快照损坏。
		return ProgressEventSummary{}, err
	}
	summary.Events = make([]ProgressEventNameSummary, 0, summary.TotalEvents)
	for index := 1; index <= summary.TotalEvents; index++ {
		// eventList 按事件名排序并使用连续的 Lua 1-based 数组。
		entryTable, entryErr := progressEventValueTable(eventsTable.RawGetInteger(int64(index)), "summary.events")
		if entryErr != nil {
			// 任一缺失或损坏项都拒绝部分解码结果。
			return ProgressEventSummary{}, entryErr
		}
		entryInteger := func(field string) int64 {
			// 单事件统计同样只接受精确 Lua integer。
			fieldValue := entryTable.RawGetString(field)
			if fieldValue.Kind != KindInteger {
				// 类型不匹配保持零值并由 Raw 供高级调用方诊断。
				return 0
			}
			return fieldValue.Integer
		}
		summary.Events = append(summary.Events, ProgressEventNameSummary{
			Event:     entryTable.RawGetString("event").String,
			Listeners: int(entryInteger("listeners")), Active: int(entryInteger("active")), Muted: int(entryInteger("muted")),
			Sync: int(entryInteger("sync")), Async: int(entryInteger("async")),
			DispatchCount: entryInteger("dispatchCount"), ErrorCount: entryInteger("errorCount"),
			DroppedCount: entryInteger("droppedCount"), RejectedCount: entryInteger("rejectedCount"),
			SuppressedCount: entryInteger("suppressedCount"),
			TotalDuration:   time.Duration(entryInteger("totalDurationNs")),
		})
	}
	return summary, nil
}

// progressEventValueTable 从公开 Event 快照值中提取有效 table。
func progressEventValueTable(value Value, field string) (*runtime.Table, error) {
	// 快照解码不触发元方法，只接受真实 table 引用。
	if value.Kind != KindTable {
		// 非 table 值不能形成类型化 Event 快照。
		return nil, newProgressEventArgumentError(field, "must be a Lua table")
	}
	table, _ := value.Ref.(*runtime.Table)
	if table == nil {
		// 损坏引用不能安全读取字段。
		return nil, newProgressEventArgumentError(field, "contains an invalid table reference")
	}
	return table, nil
}

// callProgressEventFromGo 校验 Go 触发参数并调用条件编译实现。
func callProgressEventFromGo(state *State, source string, event string, async bool, payload []Value) error {
	// 先拒绝多 payload，保持与 Lua callProgress 的单 payload 契约一致。
	if len(payload) > 1 {
		// 多余参数不执行隐式打包，避免改变 payload 结构。
		return newProgressEventArgumentError("payload", "accepts at most one value")
	}
	payloadValue := runtime.NilValue()
	if len(payload) == 1 {
		// 单个 payload 原样进入只读事件上下文。
		payloadValue = payload[0]
	}
	return dispatchGluaProgressEventFromGo(state, source, event, payloadValue, async)
}

// newProgressEventArgumentError 构造公开 Go Event API 的稳定参数错误。
func newProgressEventArgumentError(field string, message string) error {
	// 集中构造保证所有入口都支持 errors.Is/As 分类。
	return &ProgressEventArgumentError{Field: field, Message: message}
}

// decodeProgressEventContext 把内部 Lua table 解码为稳定 Go 回调上下文。
func decodeProgressEventContext(value Value) (ProgressEventContext, error) {
	// 解码只读取 raw table，不触发 Lua 元方法或修改上下文。
	if value.Kind != KindTable {
		// 非 table 不能作为事件上下文。
		return ProgressEventContext{}, runtime.RaiseError(runtime.StringValue("progress event context must be table"))
	}
	table, _ := value.Ref.(*runtime.Table)
	if table == nil {
		// 损坏引用不能安全读取字段。
		return ProgressEventContext{}, runtime.RaiseError(runtime.StringValue("progress event context table is invalid"))
	}
	stringField := func(name string) string {
		// string 字段类型不匹配时保持零值，RawValue 仍保留原始信息。
		field := table.RawGetString(name)
		if field.Kind != KindString {
			// 非字符串字段不执行隐式转换。
			return ""
		}
		return field.String
	}
	integerField := func(name string) int64 {
		// integer 字段类型不匹配时保持零值。
		field := table.RawGetString(name)
		if field.Kind != KindInteger {
			// Event 标识和时间戳必须保持精确整数。
			return 0
		}
		return field.Integer
	}
	booleanField := func(name string) bool {
		// boolean 字段类型不匹配时保持 false。
		field := table.RawGetString(name)
		if field.Kind != KindBoolean {
			// 不使用 Lua truthy 转换，避免损坏上下文被误判。
			return false
		}
		return field.Bool
	}
	return ProgressEventContext{
		raw:   value,
		Event: stringField("event"), Kind: stringField("kind"), Source: stringField("source"),
		Payload: table.RawGetString("payload"), Timestamp: integerField("timestamp"),
		Sequence: integerField("sequence"), EventID: integerField("eventId"), TraceID: integerField("traceId"),
		ParentEventID: integerField("parentEventId"), Depth: integerField("depth"), ListenerID: integerField("id"),
		Async: booleanField("async"), Scope: ProgressEventScope(stringField("scope")),
	}, nil
}
