package lua

import (
	"errors"
	"fmt"

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
)

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

// SetProgressEvent 从 Go 注册一个进度事件监听器并返回事件 ID。
//
// source 必须与 Lua Proto.Source 完全一致；event 不能为空；callback 不能为空。默认监听当前 State
// 内全部 Lua 文件，options.Scope=file 时仅监听 source。State 必须先调用 OpenLibs。
func SetProgressEvent(state *State, source string, event string, callback ProgressEventCallback, options ...ProgressEventOptions) (int64, error) {
	// 先把类型化 Go callback 包装成现有 Lua callable，确保派发和错误策略完全复用。
	if callback == nil {
		// nil callback 不能形成可调用监听器。
		return 0, ErrExpectedCallable
	}
	callbackValue := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
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
	}))
	return SetProgressEventValue(state, source, event, callbackValue, options...)
}

// SetProgressEventValue 从 Go 注册已有 Lua 或 Go closure 值并返回事件 ID。
//
// callback 必须是 KindLuaClosure 或 KindGoClosure；其他约束与 SetProgressEvent 相同。
func SetProgressEventValue(state *State, source string, event string, callback Value, options ...ProgressEventOptions) (int64, error) {
	// 统一解析可选参数后进入条件编译实现，避免调用方触碰 runtime registry。
	parsedOptions, err := normalizeProgressEventOptions(options)
	if err != nil {
		// 参数数量或 scope 错误直接返回给 Go 调用方。
		return 0, err
	}
	return setGluaProgressEventForSource(state, source, event, callback, parsedOptions)
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
		return ProgressEventOptions{}, fmt.Errorf("set progress event accepts at most one options value")
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
		return ProgressEventOptions{}, fmt.Errorf("progress event scope must be %q or %q", ProgressEventScopeRuntime, ProgressEventScopeFile)
	}
	return result, nil
}

// callProgressEventFromGo 校验 Go 触发参数并调用条件编译实现。
func callProgressEventFromGo(state *State, source string, event string, async bool, payload []Value) error {
	// 先拒绝多 payload，保持与 Lua callProgress 的单 payload 契约一致。
	if len(payload) > 1 {
		// 多余参数不执行隐式打包，避免改变 payload 结构。
		return fmt.Errorf("call progress event accepts at most one payload value")
	}
	payloadValue := runtime.NilValue()
	if len(payload) == 1 {
		// 单个 payload 原样进入只读事件上下文。
		payloadValue = payload[0]
	}
	return dispatchGluaProgressEventFromGo(state, source, event, payloadValue, async)
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
