package runtime

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNilState 表示运行时 helper 收到了空 State 指针。
	ErrNilState = errors.New("nil lua state")
	// ErrToStringMetamethod 表示 `__tostring` 元方法没有返回 Lua string。
	ErrToStringMetamethod = errors.New("'__tostring' must return a string")
	// ErrExpectedCallable 表示调用语义遇到了既不是函数也没有 `__call` 的值。
	ErrExpectedCallable = errors.New("attempt to call a non-function value")
	// ErrLuaError 表示 Lua `error` 主动抛出的运行时错误原因。
	ErrLuaError = errors.New("lua error")
)

const (
	// tracebackHeadFrameLimit 表示 Lua 5.3 深栈 traceback 压缩前段保留帧数。
	tracebackHeadFrameLimit = 10
	// tracebackTailFrameLimit 表示 Lua 5.3 深栈 traceback 压缩尾段保留帧数。
	tracebackTailFrameLimit = 11
)

// ErrorHandler 表示 xpcall 使用的错误处理函数。
//
// 入参是 Lua error object；返回值是处理后的 Lua error object。handler 自身失败时，xpcall
// 会把 handler 的错误对象作为失败结果返回。
type ErrorHandler func(object Value) (Value, error)

// ToString 按 Lua 5.3 tostring 语义把值转换为 string 值。
//
// value 可以是任意 Lua 值；table 会优先调用 `__tostring` 元方法。返回值必须是 KindString，
// 否则返回 ErrToStringMetamethod。没有元方法时返回基础稳定文本，后续标准库 tostring 会
// 直接复用该 helper。
func ToString(value Value) (Value, error) {
	// 不带 State 的转换只支持 Go closure 元方法，保持旧调用方行为。
	return ToStringWithState(nil, value)
}

// ToStringWithState 按 Lua 5.3 tostring 语义把值转换为 string 值。
//
// state 可为 nil；非 nil 时 table 等引用值上的 Lua closure `__tostring` 会通过 State 注入的
// 元方法执行器运行。返回值必须是 KindString，否则返回 ErrToStringMetamethod。
func ToStringWithState(state *State, value Value) (Value, error) {
	// tostring 先检查 `__tostring` 元方法，匹配 Lua 5.3 的优先级。
	if method, ok := lookupMetamethod(value, metamethodToString); ok {
		// 存在 __tostring 时必须调用元方法，并要求第一返回值是 string。
		result, err := callMetamethodWithState(state, method, metamethodToString, value)
		if err != nil {
			// 元方法存在但调用失败时返回调用错误。
			return NilValue(), err
		}
		if result.Kind != KindString {
			// Lua 5.3 要求 __tostring 返回 string，否则 tostring 调用失败。
			return NilValue(), ErrToStringMetamethod
		}
		return result, nil
	}

	switch value.Kind {
	case KindNil:
		// nil 的基础 tostring 文本固定为 "nil"。
		return StringValue("nil"), nil
	case KindBoolean:
		// boolean 的基础 tostring 文本固定为 "true" 或 "false"。
		if value.Bool {
			return StringValue("true"), nil
		}
		return StringValue("false"), nil
	case KindInteger, KindNumber:
		// number 使用已有 Lua 5.3 number-to-string 基础转换。
		if converted, ok := value.NumberToString(); ok {
			return StringValue(converted), nil
		}
		return StringValue(value.DebugString()), nil
	case KindString:
		// string 的 tostring 返回自身。
		return value, nil
	default:
		// 其他引用对象返回 Lua 5.3 兼容的类型加地址格式，例如 table: 0x...。
		return StringValue(referenceToString(value)), nil
	}
}

// referenceToString 返回 Lua 5.3 风格的引用值 tostring 文本。
//
// value 应为 table/function/userdata/thread 等引用值；没有可用引用负载时仍返回类型前缀和 0x0，
// 保证 `tostring` 不泄露内部 DebugString 文本。
func referenceToString(value Value) string {
	// 先按 Lua 基础类型确定可见前缀。
	typeName := referenceTypeName(value.Kind)
	if method, ok := lookupMetamethod(value, "__name"); ok && method.Kind == KindString {
		// Lua 5.3 在缺少 __tostring 时使用元表 __name 覆盖可见类型名。
		typeName = method.String
	}
	if value.Ref == nil {
		// 损坏或空引用仍保持 Lua 风格文本，避免回退到 ref(kind=...)。
		return fmt.Sprintf("%s: 0x0", typeName)
	}

	// %p 对指针、函数、map、slice 等引用负载会输出稳定的 0x... 形态。
	return fmt.Sprintf("%s: %p", typeName, value.Ref)
}

// referenceTypeName 返回引用值在 Lua tostring 中使用的类型前缀。
//
// 函数闭包统一展示为 function；未知引用类型回退到 userdata，避免出现内部枚举值。
func referenceTypeName(kind ValueKind) string {
	switch kind {
	case KindTable:
		// table 引用按 Lua 标准展示 table 前缀。
		return "table"
	case KindLuaClosure, KindGoClosure:
		// Lua 和 Go closure 在 Lua 侧都展示为 function。
		return "function"
	case KindUserdata:
		// userdata 保留 Lua 标准前缀。
		return "userdata"
	case KindThread:
		// coroutine/thread 保留 Lua 标准前缀。
		return "thread"
	default:
		// 防御未知引用类型，避免输出内部 kind 数字。
		return "userdata"
	}
}

// Pairs 按 Lua 5.3 pairs 语义生成迭代三元组。
//
// value 必须是 table 或带 `__pairs` 元方法的值；有 `__pairs` 时直接返回元方法的多返回值。
// 无元方法时返回 next-like GoResultsFunction、原 table 和 nil 初始 key。
func Pairs(value Value) ([]Value, error) {
	// 无 State 的调用只能直接执行 Go closure 元方法，Lua closure 元方法由 PairsWithState 支持。
	return pairsWithState(nil, value)
}

// PairsWithState 按 Lua 5.3 pairs 语义生成迭代三元组，并支持 Lua closure 元方法。
//
// state 用于执行 `__pairs` Lua closure；value 必须是 table 或带 `__pairs` 元方法的值。
func PairsWithState(state *State, value Value) ([]Value, error) {
	// 带 State 的入口允许 base 标准库调用 Lua closure 型 `__pairs`。
	return pairsWithState(state, value)
}

// pairsWithState 实现 pairs 的共享元方法与 raw 迭代回退逻辑。
//
// state 为 nil 时只支持 Go closure 元方法；非 nil 时可通过 State runner 执行 Lua closure。
func pairsWithState(state *State, value Value) ([]Value, error) {
	if method, ok := lookupMetamethod(value, metamethodPairs); ok {
		// __pairs 自定义迭代协议优先于 raw next 迭代。
		return callClosureResultsWithState(state, method, value)
	}
	if _, err := tableFromValue(value); err != nil {
		// 无 __pairs 的非 table 值不能执行 pairs。
		return nil, err
	}

	// 返回 raw pairs 迭代器、状态 table 和 nil 初始 key。
	return []Value{ReferenceValue(KindGoClosure, GoResultsFunction(rawPairsIterator)), value, NilValue()}, nil
}

// IPairs 按 Lua 5.3 ipairs 兼容语义生成迭代三元组。
//
// value 必须是 table 或带 `__ipairs` 元方法的值；Lua 5.3 仍保留 `__ipairs` 兼容入口。
// 无元方法时返回 raw ipairs 迭代器、原 table 和 0 初始索引。
func IPairs(value Value) ([]Value, error) {
	// 无 State 的调用只能直接执行 Go closure 元方法，Lua closure 元方法由 IPairsWithState 支持。
	return ipairsWithState(nil, value)
}

// IPairsWithState 按 Lua 5.3 ipairs 兼容语义生成迭代三元组，并支持 Lua closure 元方法。
//
// state 用于执行 `__ipairs` Lua closure；value 必须是 table 或带 `__ipairs` 元方法的值。
func IPairsWithState(state *State, value Value) ([]Value, error) {
	// 带 State 的入口允许 base 标准库调用 Lua closure 型 `__ipairs`。
	return ipairsWithState(state, value)
}

// ipairsWithState 实现 ipairs 的共享元方法与 raw integer 迭代回退逻辑。
//
// state 为 nil 时只支持 Go closure 元方法；非 nil 时可通过 State runner 执行 Lua closure。
func ipairsWithState(state *State, value Value) ([]Value, error) {
	if method, ok := lookupMetamethod(value, metamethodIPairs); ok {
		// __ipairs 自定义迭代协议优先于 raw integer 前缀迭代。
		return callClosureResultsWithState(state, method, value)
	}
	if _, err := tableFromValue(value); err != nil {
		// 无 __ipairs 的非 table 值不能执行 ipairs。
		return nil, err
	}
	if state != nil {
		// 带 State 的 ipairs 需要普通索引读取，以便触发 Lua closure 型 `__index`。
		iterator := GoResultsFunction(func(args ...Value) ([]Value, error) {
			// 捕获 State 的迭代器仅用于当前标准库调用链。
			return stateIPairsIterator(state, args...)
		})
		return []Value{ReferenceValue(KindGoClosure, iterator), value, IntegerValue(0)}, nil
	}

	// 返回 raw ipairs 迭代器、状态 table 和 0 初始索引。
	return []Value{ReferenceValue(KindGoClosure, GoResultsFunction(rawIPairsIterator)), value, IntegerValue(0)}, nil
}

// callClosureResultsWithState 调用 Go 或 Lua closure，并保留 Lua 多返回值。
//
// method 是元表中取出的元方法；Go closure 直接执行，Lua closure 需要 state 注入的 runner。
func callClosureResultsWithState(state *State, method Value, args ...Value) ([]Value, error) {
	if method.Kind == KindGoClosure {
		// Go closure 元方法可直接在 runtime 层执行。
		return callGoClosureResults(method, args...)
	}
	if method.Kind == KindLuaClosure && state != nil {
		// Lua closure 元方法通过 State runner 进入完整脚本执行链路。
		return state.CallLuaClosure(method, args...)
	}

	// 其他函数形态当前不能作为可执行元方法。
	return nil, ErrUnsupportedMetamethod
}

// RaiseError 构造 Lua `error` 语义的 RuntimeError。
//
// object 是 Lua 侧 error object，可以是任意 Lua 值；返回错误链携带 ErrLuaError，调用方可
// 通过 ErrorObject 取回原始对象。该 helper 不拼接 traceback，traceback 在 debug 阶段补齐。
func RaiseError(object Value) error {
	// 直接构造 RuntimeError，避免 NewRuntimeError 把 nil 错误对象改写成空字符串。
	return &RuntimeError{Object: object, Cause: ErrLuaError}
}

// PCall 执行 Lua 5.3 pcall 基础保护语义。
//
// state 必须是未关闭 State；call 在 State.ProtectedCall 边界内执行。成功时返回
// true 加 call 期间压入的新栈值；失败时返回 false 和 Lua error object，并吞掉 Go error。
func PCall(state *State, call ProtectedCallFunc) ([]Value, error) {
	if state == nil {
		// 空 State 无法建立保护调用边界。
		return nil, ErrNilState
	}

	startTop := state.StackTop()
	err := state.ProtectedCall(call)
	if err != nil {
		// pcall 捕获错误并把 Lua error object 作为第二个返回值。
		return []Value{BooleanValue(false), ErrorObject(err)}, nil
	}

	results := []Value{BooleanValue(true)}
	for stackIndex := startTop + 1; stackIndex <= state.StackTop(); stackIndex++ {
		// 成功路径收集 call 新压入的返回值，保持原始顺序。
		results = append(results, state.ValueAt(stackIndex))
	}
	return results, nil
}

// XPCall 执行 Lua 5.3 xpcall 基础保护语义。
//
// handler 在 call 失败后接收原始 Lua error object，并返回替换后的错误对象。成功时行为与
// PCall 相同；handler 自身返回错误时，错误对象取 handler 错误链中的 Lua error object。
func XPCall(state *State, call ProtectedCallFunc, handler ErrorHandler) ([]Value, error) {
	if state == nil {
		// 空 State 无法建立保护调用边界。
		return nil, ErrNilState
	}
	if handler == nil {
		// Lua xpcall 需要错误处理函数，当前 helper 对 nil handler 明确返回 callable 错误。
		return nil, ErrExpectedCallable
	}

	startTop := state.StackTop()
	err := state.ProtectedCall(call)
	if err == nil {
		// 成功路径复用 pcall 的返回布局：true 后接函数返回值。
		results := []Value{BooleanValue(true)}
		for stackIndex := startTop + 1; stackIndex <= state.StackTop(); stackIndex++ {
			results = append(results, state.ValueAt(stackIndex))
		}
		return results, nil
	}

	handledObject, handlerErr := handler(ErrorObject(err))
	if handlerErr != nil {
		// error handler 自身失败时，xpcall 返回 handler 的错误对象。
		return []Value{BooleanValue(false), ErrorObject(handlerErr)}, nil
	}
	return []Value{BooleanValue(false), handledObject}, nil
}

// PanicToError 把 Go panic 值转换为可传播的 RuntimeError。
//
// recovered 必须是 recover 捕获到的值；nil 表示没有 panic 并返回 nil。返回错误链携带
// panic 文本，同时 Lua error object 保存 panic 的字符串表示。
func PanicToError(recovered any) error {
	if recovered == nil {
		// 没有 panic 时不构造错误。
		return nil
	}

	panicErr := fmt.Errorf("protected call panic: %v", recovered)
	return NewRuntimeError(StringValue(fmt.Sprintf("%v", recovered)), panicErr)
}

// LuaErrorFromGo 把普通 Go error 转换为携带 Lua error object 的错误链。
//
// err 为 nil 时返回 nil；已有 RuntimeError 时保持原样；普通 Go error 会转换为 Lua string
// error object，供 bridge 和标准库统一传播。
func LuaErrorFromGo(err error) error {
	// 复用 State protected call 内部包装逻辑，保持错误链语义一致。
	return ensureRuntimeError(err)
}

// Traceback 拼接 Lua 5.3 风格的基础 traceback 文本。
//
// message 是错误消息首行；frames 按 TracebackFrames 返回的顺序传入，即当前帧到最早帧。
// 当前阶段没有源码行号，先输出帧类型和函数调试展示，后续 debug 元信息接入后扩展位置。
func Traceback(message string, frames []CallFrame) string {
	result := "stack traceback:"
	if message != "" {
		// 非空消息单独作为首行；空消息直接从 traceback 标题开始，匹配 debug.traceback()。
		result = message + "\n" + result
	}
	visibleFrames, hasSkippedFrames := tracebackVisibleFrames(frames)
	for frameIndex, frame := range visibleFrames {
		if hasSkippedFrames && frameIndex == tracebackHeadFrameLimit {
			// 深栈中间段按 Lua 5.3 规则折叠为省略号行，避免 traceback 无限制膨胀。
			result += "\n\t..."
		}
		// 每个调用帧单独占一行，保持 Lua traceback 逐帧展开形态。
		frameName := frame.Function.DebugString()
		if frame.NameWhat == "traceback" && frame.Name != "" {
			// debug 库可为特殊 traceback 场景提供已格式化的官方风格帧名。
			frameName = frame.Name
		} else if frame.Name != "" {
			// 有调用点名称时同时保留 Lua source，兼容官方 traceback 中 source 与函数名并存的格式。
			frameName = tracebackNamedFrame(frame)
		} else if frame.NameWhat != "" {
			// hook/local/field 等调用点来源写入帧行，供 debug.traceback 可见。
			frameName = frame.NameWhat + " " + frameName
		} else if sourceName := tracebackFrameSource(frame); sourceName != "" {
			// Lua 帧没有函数名时至少展示 source，供 debug.traceback(thread) 匹配脚本文件。
			if line := tracebackFrameLine(frame); line > 0 {
				// 未命名 Lua 帧也要保留 source:line，供官方 errors.lua 识别外层调用点。
				frameName = fmt.Sprintf("%s:%d:", sourceName, line)
			} else {
				// 缺失行号时保持历史 source-only 展示。
				frameName = sourceName
			}
		}
		result += fmt.Sprintf("\n\t[%s] %s", frame.Kind, frameName)
	}
	return result
}

// tracebackVisibleFrames 返回 Lua 5.3 traceback 可展示帧，并标记是否折叠了中间帧。
//
// frames 按当前帧到最早帧排列；当帧数超过 21 时，Lua 5.3 保留前 10 帧、后 11 帧，并在中间
// 插入 `...`。返回切片是新切片，调用方不得修改原始调用栈快照。
func tracebackVisibleFrames(frames []CallFrame) ([]CallFrame, bool) {
	frameLimit := tracebackHeadFrameLimit + tracebackTailFrameLimit
	if len(frames) <= frameLimit {
		// 未超过 Lua 5.3 截断阈值时完整展示调用栈。
		return append([]CallFrame(nil), frames...), false
	}

	visibleFrames := make([]CallFrame, 0, frameLimit)
	visibleFrames = append(visibleFrames, frames[:tracebackHeadFrameLimit]...)
	visibleFrames = append(visibleFrames, frames[len(frames)-tracebackTailFrameLimit:]...)
	return visibleFrames, true
}

// tracebackNamedFrame 返回带调用点名称的 Lua traceback 帧展示。
//
// frame 必须携带 Name；Lua 帧若存在 source，需要同时展示 source 与 namewhat，保证官方
// db.lua 既能匹配 `db.lua`，也能匹配递归函数名 `'f'`。
func tracebackNamedFrame(frame CallFrame) string {
	namedFrame := fmt.Sprintf("function '%s'", frame.Name)
	if frame.NameWhat != "" {
		// upvalue/local/field 等来源需要暴露在函数名前，贴近 Lua 5.3 traceback 的命名语义。
		namedFrame = fmt.Sprintf("%s '%s'", frame.NameWhat, frame.Name)
	}
	if sourceName := tracebackFrameSource(frame); sourceName != "" {
		// Lua 命名帧保留 source 前缀，避免有名称时丢失文件路径。
		if line := tracebackFrameLine(frame); line > 0 {
			// 命名 Lua 帧也要输出 source:line，官方 errors.lua 会从 traceback 中提取深栈行号。
			return fmt.Sprintf("%s:%d: in %s", sourceName, line, namedFrame)
		}
		return fmt.Sprintf("%s: in %s", sourceName, namedFrame)
	}
	return namedFrame
}

// tracebackFrameSource 返回调用帧的 Lua source 展示。
//
// frame 必须来自调用栈快照；仅 Lua closure 且携带 Proto source 时返回非空字符串。返回值会去掉
// 文件 chunk 前缀 `@`，使 traceback 文本包含用户可见路径。
func tracebackFrameSource(frame CallFrame) string {
	if frame.Kind != CallFrameKindLua || frame.Function.Kind != KindLuaClosure {
		// 非 Lua 帧没有 Proto source。
		return ""
	}
	closure, ok := frame.Function.Ref.(*LuaClosure)
	if !ok || closure == nil || closure.Proto == nil {
		// 损坏 closure 不能提供 source。
		return ""
	}
	source := closure.Proto.Source
	if source == "" {
		// 空 source 无法提供有意义展示。
		return ""
	}
	if strings.HasPrefix(source, "@") {
		// 文件 chunk 去掉 @ 前缀，便于与官方 traceback 文本匹配。
		return strings.TrimPrefix(source, "@")
	}
	return source
}

// tracebackFrameLine 返回调用帧当前 PC 对应的源码行号。
//
// 仅 Lua closure 且携带 LineInfo 时返回正数；缺失调试信息或 PC 越界时返回 -1，调用方应省略行号。
func tracebackFrameLine(frame CallFrame) int {
	if frame.Kind != CallFrameKindLua || frame.Function.Kind != KindLuaClosure {
		// 非 Lua 帧没有 Proto 行号。
		return -1
	}
	closure, ok := frame.Function.Ref.(*LuaClosure)
	if !ok || closure == nil || closure.Proto == nil {
		// 损坏 closure 不能提供行号。
		return -1
	}
	if frame.CurrentPC < 0 || frame.CurrentPC >= len(closure.Proto.LineInfo) {
		// PC 越界或 lineinfo 缺失时不输出行号。
		return -1
	}
	line := closure.Proto.LineInfo[frame.CurrentPC]
	if line <= 0 {
		// 非正行号没有用户可见意义。
		return -1
	}
	return line
}

// rawPairsIterator 执行 pairs 的 raw next 迭代器。
//
// args[0] 必须是 table，args[1] 可选并表示上一次 key；返回 nil 表示迭代结束，否则返回
// key 和 value 两个 Lua 结果。
func rawPairsIterator(args ...Value) ([]Value, error) {
	if len(args) == 0 {
		// 缺少 table 状态时无法迭代，返回明确 table 错误。
		return nil, ErrExpectedTable
	}
	table, err := tableFromValue(args[0])
	if err != nil {
		// 第一个参数必须是 table。
		return nil, err
	}

	key := NilValue()
	if len(args) > 1 {
		// 第二个参数存在时作为 RawNext 的当前 key。
		key = args[1]
	}
	nextKey, nextValue, ok, err := table.RawPairsNext(key)
	if err != nil {
		// RawNext 边界错误需要原样返回。
		return nil, err
	}
	if !ok {
		// 迭代结束时返回单个 nil，符合 Lua next/pairs 结束信号。
		return []Value{NilValue()}, nil
	}

	// 返回下一组 key/value。
	return []Value{nextKey, nextValue}, nil
}

// rawIPairsIterator 执行 ipairs 的 raw integer 前缀迭代器。
//
// args[0] 必须是 table，args[1] 可选并表示上一次 integer 索引；返回 nil 表示迭代结束，
// 否则返回下一索引和对应值。
func rawIPairsIterator(args ...Value) ([]Value, error) {
	if len(args) == 0 {
		// 缺少 table 状态时无法迭代，返回明确 table 错误。
		return nil, ErrExpectedTable
	}
	table, err := tableFromValue(args[0])
	if err != nil {
		// 第一个参数必须是 table。
		return nil, err
	}

	var currentIndex int64
	if len(args) > 1 {
		// 第二个参数存在时必须能转换为 Lua integer。
		convertedIndex, ok := valueToLuaInteger(args[1])
		if !ok {
			return nil, ErrIntegerOperand
		}
		currentIndex = convertedIndex
	}
	nextIndex, nextValue, ok := table.RawIPairsNext(currentIndex)
	if !ok {
		// 迭代结束时返回单个 nil，符合 Lua ipairs 结束信号。
		return []Value{NilValue()}, nil
	}

	// 返回下一组 index/value。
	return []Value{IntegerValue(nextIndex), nextValue}, nil
}

// stateIPairsIterator 执行 ipairs 的普通索引迭代器。
//
// state 用于执行 `__index` Lua closure；args[0] 必须是 table，args[1] 是上一次 integer 索引。
func stateIPairsIterator(state *State, args ...Value) ([]Value, error) {
	if len(args) == 0 {
		// 缺少 table 状态时无法迭代，返回明确 table 错误。
		return nil, ErrExpectedTable
	}
	table, err := tableFromValue(args[0])
	if err != nil {
		// 第一个参数必须是 table。
		return nil, err
	}

	var currentIndex int64
	if len(args) > 1 {
		// 第二个参数存在时必须能转换为 Lua integer。
		convertedIndex, ok := valueToLuaInteger(args[1])
		if !ok {
			return nil, ErrIntegerOperand
		}
		currentIndex = convertedIndex
	}
	if currentIndex >= maxTableIntegerIndex {
		// 索引到达 int64 上限时无法安全递增，直接结束迭代。
		return []Value{NilValue()}, nil
	}

	nextIndex := currentIndex + 1
	nextValue, err := table.GetWithRunner(IntegerValue(nextIndex), func(method Value, name string, args ...Value) ([]Value, error) {
		// 普通索引路径遇到 Lua closure 元方法时交给 State runner 执行。
		return state.CallLuaClosure(method, args...)
	})
	if err != nil {
		// `__index` 元方法错误必须原样上抛。
		return nil, err
	}
	if nextValue.IsNil() {
		// ipairs 遇到第一个 nil 值结束。
		return []Value{NilValue()}, nil
	}

	// 返回下一组 index/value。
	return []Value{IntegerValue(nextIndex), nextValue}, nil
}

// LuaTypeName 返回 Lua 错误消息中使用的基础类型名。
//
// value 可以是任意 Lua 值；integer 和 float 在 Lua 侧统一展示为 number，Go/Lua closure
// 统一展示为 function。该名称用于错误文本，不等同于 DebugString。
func LuaTypeName(value Value) string {
	// 按 Lua 5.3 用户可见类型合并内部 ValueKind。
	switch value.Kind {
	case KindNil:
		// nil 的类型名固定为 nil。
		return "nil"
	case KindBoolean:
		// boolean 的类型名固定为 boolean。
		return "boolean"
	case KindInteger, KindNumber:
		// Lua 5.3 对 integer/float 统一暴露为 number。
		return "number"
	case KindString:
		// string 的类型名固定为 string。
		return "string"
	default:
		// 引用类型复用 tostring 前缀规则，确保 closure 统一展示为 function。
		return referenceTypeName(value.Kind)
	}
}

// LuaErrorTypeName 返回 Lua 错误消息中可被 `__name` 覆盖的类型名。
//
// value 可以是任意 Lua 值；table/userdata 等引用值带字符串型 `__name` 元字段时返回该名称，
// 否则回退到 LuaTypeName。该 helper 用于运算、比较和标准库参数错误，兼容 Lua 5.3
// errors.lua 对 named objects 的断言。
func LuaErrorTypeName(value Value) string {
	// 优先读取 metatable.__name，Lua 5.3 错误消息会把它作为用户可见类型名。
	if method, ok := lookupMetamethod(value, "__name"); ok && method.Kind == KindString && method.String != "" {
		// 非空字符串才覆盖类型名，避免空字符串让错误消息不可读。
		return method.String
	}

	// 无可用 __name 时保持基础类型名兼容既有错误文本。
	return LuaTypeName(value)
}

// CallErrorTextWithName 返回 Lua 5.3 风格的非函数调用错误文本。
//
// value 是被调用的 Lua 值；name/nameWhat 来自 CALL 调用点的 debug 名称推断，可为空。返回
// 文本只描述错误对象本身，外层 protected call 会按调用栈追加 traceback。
func CallErrorTextWithName(value Value, name string, nameWhat string) string {
	// 调用错误以 Lua 可见类型为核心，避免泄露 integer/table 引用内部表示。
	typeName := LuaTypeName(value)
	if name != "" && nameWhat != "" {
		// 已知调用点来源时保留 global/method/field/local 等上下文，兼容官方 errors.lua。
		return fmt.Sprintf("attempt to call a %s '%s' (a %s value)", nameWhat, name, typeName)
	}
	return fmt.Sprintf("attempt to call a %s value", typeName)
}

// callErrorText 返回调用错误的基础文本。
//
// value 是被调用的 Lua 值；返回文本用于当前最小 VM 的非函数调用错误，后续 traceback 会
// 在该文本基础上追加位置信息。
func callErrorText(value Value) string {
	// 无调用点上下文时只输出 Lua 可见类型。
	return CallErrorTextWithName(value, "", "")
}
