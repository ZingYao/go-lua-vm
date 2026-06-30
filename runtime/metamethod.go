package runtime

import "errors"

var (
	// ErrUnsupportedMetamethod 表示元方法存在但当前 VM 还不能调用该函数值。
	ErrUnsupportedMetamethod = errors.New("unsupported metamethod")
)

const (
	// metamethodAdd 表示 Lua 5.3 加法 `__add` 元方法字段名。
	metamethodAdd = "__add"
	// metamethodSub 表示 Lua 5.3 减法 `__sub` 元方法字段名。
	metamethodSub = "__sub"
	// metamethodMul 表示 Lua 5.3 乘法 `__mul` 元方法字段名。
	metamethodMul = "__mul"
	// metamethodMod 表示 Lua 5.3 取模 `__mod` 元方法字段名。
	metamethodMod = "__mod"
	// metamethodPow 表示 Lua 5.3 幂运算 `__pow` 元方法字段名。
	metamethodPow = "__pow"
	// metamethodDiv 表示 Lua 5.3 浮点除法 `__div` 元方法字段名。
	metamethodDiv = "__div"
	// metamethodIDiv 表示 Lua 5.3 向下取整除法 `__idiv` 元方法字段名。
	metamethodIDiv = "__idiv"
	// metamethodUnm 表示 Lua 5.3 一元负号 `__unm` 元方法字段名。
	metamethodUnm = "__unm"
	// metamethodBand 表示 Lua 5.3 按位与 `__band` 元方法字段名。
	metamethodBand = "__band"
	// metamethodBor 表示 Lua 5.3 按位或 `__bor` 元方法字段名。
	metamethodBor = "__bor"
	// metamethodBXor 表示 Lua 5.3 按位异或 `__bxor` 元方法字段名。
	metamethodBXor = "__bxor"
	// metamethodShl 表示 Lua 5.3 左移 `__shl` 元方法字段名。
	metamethodShl = "__shl"
	// metamethodShr 表示 Lua 5.3 右移 `__shr` 元方法字段名。
	metamethodShr = "__shr"
	// metamethodBNot 表示 Lua 5.3 按位非 `__bnot` 元方法字段名。
	metamethodBNot = "__bnot"
	// metamethodEq 表示 Lua 5.3 相等比较 `__eq` 元方法字段名。
	metamethodEq = "__eq"
	// metamethodLt 表示 Lua 5.3 小于比较 `__lt` 元方法字段名。
	metamethodLt = "__lt"
	// metamethodLe 表示 Lua 5.3 小于等于比较 `__le` 元方法字段名。
	metamethodLe = "__le"
	// metamethodLen 表示 Lua 5.3 长度运算 `__len` 元方法字段名。
	metamethodLen = "__len"
	// metamethodConcat 表示 Lua 5.3 拼接运算 `__concat` 元方法字段名。
	metamethodConcat = "__concat"
	// metamethodCall 表示 Lua 5.3 调用运算 `__call` 元方法字段名。
	metamethodCall = "__call"
	// metamethodToString 表示 Lua 5.3 字符串转换 `__tostring` 元方法字段名。
	metamethodToString = "__tostring"
	// metamethodPairs 表示 Lua 5.3 pairs 兼容入口 `__pairs` 元方法字段名。
	metamethodPairs = "__pairs"
	// metamethodIPairs 表示 Lua 5.3 ipairs 兼容入口 `__ipairs` 元方法字段名。
	metamethodIPairs = "__ipairs"
)

// GoFunction 表示当前 VM 可直接调用的 Go 元方法函数。
//
// args 按 Lua 元方法调用顺序传入；返回值是元方法第一返回值。错误会原样上抛到 VM 指令，
// 用于后续接入 protected call 和 traceback。Lua closure 元方法需要完整调用栈，当前阶段
// 仍返回 ErrUnsupportedMetamethod。
type GoFunction func(args ...Value) (Value, error)

// GoUnaryFunction 表示单参数单返回的 Go closure 热路径。
//
// 参数按 Lua 调用前已经完成的寄存器布局传入；错误语义与 GoFunction 保持一致。该类型用于
// 标准库中高频的一元函数，避免 VM CALL 为单参数构造临时参数切片。
type GoUnaryFunction func(Value) (Value, error)

// GoFastUnaryFunction 表示可在无 hook 成功路径跳过 Go 调用帧的一元 Go closure。
//
// Function 必须在 AcceptedKinds 覆盖的参数类型下不依赖 Go 调用帧副作用；未命中类型时调用方
// 必须回退 GoUnaryFunction 的完整 debug-frame 路径，以保留参数错误 traceback 语义。
type GoFastUnaryFunction struct {
	// Function 保存真实一元函数入口。
	Function GoUnaryFunction
	// AcceptedKinds 用 bit mask 表示允许跳过 debug frame 的参数类型集合。
	AcceptedKinds uint64
}

// UnaryKindMask 构造 GoFastUnaryFunction 使用的 ValueKind bit mask。
//
// kinds 是允许走无 frame 成功路径的 Lua 值类型；重复类型会自然合并。
func UnaryKindMask(kinds ...ValueKind) uint64 {
	// 使用 uint64 bit mask 避免热路径 map 查询和额外分配。
	var mask uint64
	for _, kind := range kinds {
		// ValueKind 当前远小于 64，越界值忽略以防外部构造异常枚举。
		if kind < 64 {
			mask |= 1 << uint(kind)
		}
	}
	return mask
}

// Accepts 判断 value 是否满足 fast unary 的无 frame 调用类型约束。
func (function *GoFastUnaryFunction) Accepts(value Value) bool {
	// nil 函数或空 mask 不能走无 frame 快路径。
	if function == nil || function.Function == nil || value.Kind >= 64 {
		return false
	}
	return function.AcceptedKinds&(1<<uint(value.Kind)) != 0
}

// GoResultsFunction 表示当前 VM 可直接调用并返回多返回值的 Go 元方法函数。
//
// args 按 Lua 调用顺序传入；返回切片按 Lua 多返回值顺序排列。该类型主要服务 `__pairs`、
// `__ipairs` 和后续 Go bridge 多返回值路径，单返回值元方法仍可使用 GoFunction。
type GoResultsFunction func(args ...Value) ([]Value, error)

// GoFixedResultsFunction 表示有固定返回值上限的 Go 回调。
//
// MaxResults 必须覆盖 Function 快路径可能返回的最大结果数量；Function 将结果写入调用方提供的
// dst，返回实际结果数量和是否命中快路径。未命中时调用方必须回退到 Fallback，避免截断变长结果。
type GoFixedResultsFunction struct {
	// MaxResults 表示 Function 最多写入的返回值数量。
	MaxResults int
	// Function4 将最多四个参数按寄存器原值传入，避免热点固定结果函数构造参数切片。
	Function4 func(dst []Value, arg0 Value, arg1 Value, arg2 Value, arg3 Value, argCount int) (int, bool, error)
	// Function 将返回值写入 dst，并返回实际结果数量和是否命中快路径。
	Function func(dst []Value, args ...Value) (int, bool, error)
	// Fallback 保存未命中快路径时使用的完整多返回值函数。
	Fallback GoResultsFunction
}

// lookupMetamethod 查找一个值的元方法。
//
// 当前阶段支持 table 与 userdata 的专属元表，以及 nil、boolean、number、string 的类型级
// 共享元表。name 必须是 Lua 5.3 已知元方法字段名，例如 `__add` 或 `__index`。
func lookupMetamethod(value Value, name string) (Value, bool) {
	var metatable *Table
	switch value.Kind {
	case KindTable:
		// table 值从自身 raw 元表查找元方法。
		table, err := tableFromValue(value)
		if err != nil {
			// table 引用损坏时不能安全访问元表，这里按未命中处理并让原操作路径保留错误语义。
			return NilValue(), false
		}
		metatable = table.GetMetatable()
	case KindUserdata:
		// userdata 值从对象携带的 raw 元表查找元方法，服务 file/object method lookup。
		userdata, ok := value.Ref.(*Userdata)
		if !ok || userdata == nil {
			// userdata 引用损坏时不能安全访问元表，按未命中处理。
			return NilValue(), false
		}
		metatable = userdata.GetMetatable()
	case KindNil, KindBoolean, KindInteger, KindNumber, KindString:
		// 基础类型从类型级共享元表查找元方法，支持 debug.setmetatable 动态挂载行为。
		metatable = BasicTypeMetatable(value)
	default:
		// 其他类型当前没有可查询的元表，调用方继续尝试另一侧操作数或返回原错误。
		return NilValue(), false
	}

	if metatable == nil {
		// 没有元表时必然不存在该元方法。
		return NilValue(), false
	}

	method := metatable.RawGetString(name)
	if method.IsNil() {
		// 元表字段不存在或显式为 nil 时视为未定义元方法。
		return NilValue(), false
	}

	// 返回元表中保存的元方法值，后续由调用方按当前支持的函数类型执行。
	return method, true
}

// callGoMetamethod 调用当前阶段支持的 Go 元方法。
//
// method 必须是 KindGoClosure 且 Ref 保存 GoFunction；args 是已经按 Lua 规则排列的实参。
// 若元方法不是 GoFunction，则返回 ErrUnsupportedMetamethod，避免误把未接入的 Lua closure
// 当作已执行成功。
func callGoMetamethod(method Value, args ...Value) (Value, error) {
	if method.Kind != KindGoClosure {
		// 当前阶段只直接调用 Go closure，Lua closure 会在完整调用栈接入后支持。
		return NilValue(), ErrUnsupportedMetamethod
	}

	results, err := callGoClosureResults(method, args...)
	if err != nil {
		// Go closure 引用负载损坏或类型不匹配时返回明确错误。
		return NilValue(), err
	}
	if len(results) == 0 {
		// 没有返回值时按 Lua 调用取第一返回值语义补 nil。
		return NilValue(), nil
	}

	// 单值元方法取第一返回值，额外返回值由多返回值 helper 处理。
	return results[0], nil
}

// callMetamethodWithState 调用当前 State 支持的 Go 或 Lua 元方法。
//
// state 可为 nil；nil 时仅支持 Go closure 元方法。Lua closure 元方法需要 state 上层注入
// luaMetamethodRunner，否则返回 ErrUnsupportedMetamethod。
func callMetamethodWithState(state *State, method Value, name string, args ...Value) (Value, error) {
	if method.Kind == KindGoClosure {
		// Go closure 元方法不需要 State 调度器，复用已有路径。
		return callGoMetamethod(method, args...)
	}
	if method.Kind != KindLuaClosure {
		// 非函数值不能作为可调用元方法。
		return NilValue(), ErrUnsupportedMetamethod
	}
	if state == nil || state.luaMetamethodRunner == nil {
		// 没有上层执行器时无法从 runtime 包直接执行 Lua closure。
		return NilValue(), ErrUnsupportedMetamethod
	}

	results, err := state.luaMetamethodRunner(method, name, args...)
	if err != nil {
		// Lua closure 元方法抛错时原样传播。
		return NilValue(), err
	}
	if len(results) == 0 {
		// 没有返回值时按 Lua 调用取第一返回值语义补 nil。
		return NilValue(), nil
	}

	// 单值元方法取第一返回值，额外返回值由调用方忽略。
	return results[0], nil
}

// callBinaryMetamethod 按 Lua 二元运算优先级调用元方法。
//
// 查询顺序先左操作数后右操作数；若两侧都不存在 name，则返回 found=false。该函数只负责
// 查找和调用，不判断原始运算是否应该先尝试，调用方必须保持 raw 路径优先。
func callBinaryMetamethod(left Value, right Value, name string) (Value, bool, error) {
	// 无 VM/State runner 时保持旧行为，只能执行 Go closure 元方法。
	return callBinaryMetamethodWithRunner(nil, left, right, name)
}

// callBinaryMetamethodWithRunner 按 Lua 二元运算优先级调用元方法。
//
// runner 可为 nil；nil 时只支持 Go closure 元方法。非 nil runner 允许执行 Lua closure 元方法，
// 服务运行期由 Lua 脚本动态挂载的类型元表。
func callBinaryMetamethodWithRunner(runner LuaMetamethodRunner, left Value, right Value, name string) (Value, bool, error) {
	if method, ok := lookupMetamethod(left, name); ok {
		// 左操作数元方法优先级更高，找到后立即调用。
		result, err := callMetamethodValue(runner, method, name, left, right)
		return result, true, err
	}
	if method, ok := lookupMetamethod(right, name); ok {
		// 左侧未命中时才尝试右操作数元方法。
		result, err := callMetamethodValue(runner, method, name, left, right)
		return result, true, err
	}

	// 两侧都没有目标元方法，调用方应返回原始操作错误。
	return NilValue(), false, nil
}

// callMetamethodValue 调用 Go 或 Lua closure 元方法并取第一返回值。
//
// runner 为 nil 时 Lua closure 元方法不可执行，会返回 ErrUnsupportedMetamethod。Go closure
// 始终可直接执行，保持底层 runtime 单测无需构造 State。
func callMetamethodValue(runner LuaMetamethodRunner, method Value, name string, args ...Value) (Value, error) {
	if method.Kind == KindGoClosure {
		// Go closure 元方法复用已有直接调用路径。
		return callGoMetamethod(method, args...)
	}
	if method.Kind != KindLuaClosure {
		// 非函数值不能作为可调用元方法。
		return NilValue(), ErrUnsupportedMetamethod
	}
	if runner == nil {
		// 没有上层执行器时无法从 runtime 包直接执行 Lua closure。
		return NilValue(), ErrUnsupportedMetamethod
	}
	results, err := runner(method, name, args...)
	if err != nil {
		// Lua closure 元方法抛错时原样传播。
		return NilValue(), err
	}
	if len(results) == 0 {
		// 没有返回值时按 Lua 调用取第一返回值语义补 nil。
		return NilValue(), nil
	}

	// 二元元方法按 Lua 语义只使用第一返回值。
	return results[0], nil
}

// callGoClosureResults 调用当前阶段支持的 Go closure 并返回多返回值。
//
// method 必须是 KindGoClosure；Ref 可以是 GoFunction 或 GoResultsFunction。GoFunction 会
// 被提升为单元素返回值切片，便于调用方统一处理 `__pairs` 等多返回值语义。
func callGoClosureResults(method Value, args ...Value) ([]Value, error) {
	if method.Kind != KindGoClosure {
		// 非 Go closure 无法在当前阶段直接执行。
		return nil, ErrUnsupportedMetamethod
	}

	switch function := method.Ref.(type) {
	case GoFunction:
		// 单返回值 GoFunction 通过一层适配进入多返回值通道。
		if function == nil {
			return nil, ErrUnsupportedMetamethod
		}
		result, err := function(args...)
		if err != nil {
			// 函数自身错误必须原样上抛。
			return nil, err
		}
		return []Value{result}, nil
	case GoResultsFunction:
		// 多返回值 GoResultsFunction 直接执行。
		if function == nil {
			return nil, ErrUnsupportedMetamethod
		}
		return function(args...)
	case *GoClosureWithUpvalues:
		// 带 debug upvalue 元数据的 Go closure 仍通过 Function 字段执行。
		if function == nil || function.Function == nil {
			return nil, ErrUnsupportedMetamethod
		}
		return function.Function(args...)
	default:
		// 未知 Ref 类型表示 Go closure 负载损坏或尚未接入 bridge 适配器。
		return nil, ErrUnsupportedMetamethod
	}
}
