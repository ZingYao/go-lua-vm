// Package mathlib 实现 Lua 5.3 math 标准库的第一阶段能力。
//
// 本包负责注册 `math` 库表，并提供已迁移的 Lua 5.3 math 基础行为。
package mathlib

import (
	"fmt"
	"math"
	mrand "math/rand"
	"sync"

	"github.com/zing/go-lua-vm/runtime"
)

// randomMutex 保护 math.random 使用的包级随机源，避免并发嵌入调用产生数据竞争。
var randomMutex sync.Mutex

// randomGenerator 是 math.random/math.randomseed 共用的独立随机源，不依赖 Go 顶层 rand 默认源。
var randomGenerator = mrand.New(mrand.NewSource(1))

// Open 将 Lua 5.3 math 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后全局 `math` 字段指向库表，并注册 abs、acos、asin、
// atan、ceil、cos、deg、exp、floor、fmod、log、max、min、modf、rad、random、
// randomseed、sin、sqrt、tan、tointeger、type 和 ult，并写入 huge、maxinteger、
// mininteger 与 pi 常量。错误语义对齐运行时生命周期错误和 Lua 标准库参数错误。
func Open(state *runtime.State) error {
	// 注册入口先校验 State 生命周期，避免向关闭后的全局表写入库函数。
	if state == nil {
		// nil State 没有 globals，调用方需要先创建 runtime.State。
		return fmt.Errorf("math library unavailable: %w", runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("math library unavailable: %w", runtime.ErrClosedState)
	}

	library := runtime.NewTable()
	numberUnaryKinds := runtime.UnaryKindMask(runtime.KindInteger, runtime.KindNumber)
	// math 库函数以 Go closure 注册，后续 VM CALL 会通过 bridge 调用。
	library.RawSetString("abs", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: AbsUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("acos", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: ACosUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("asin", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: ASinUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("atan", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: ATanUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("ceil", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: CeilUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("cos", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: CosUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("deg", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: DegUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("exp", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: ExpUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("floor", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: FloorUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("fmod", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(FMod)))
	library.RawSetString("huge", runtime.NumberValue(math.Inf(1)))
	library.RawSetString("log", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Log)))
	library.RawSetString("max", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Max)))
	library.RawSetString("maxinteger", runtime.IntegerValue(math.MaxInt64))
	library.RawSetString("min", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Min)))
	library.RawSetString("mininteger", runtime.IntegerValue(math.MinInt64))
	library.RawSetString("modf", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(ModF)))
	library.RawSetString("pi", runtime.NumberValue(math.Pi))
	library.RawSetString("rad", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: RadUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("random", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Random)))
	library.RawSetString("randomseed", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(RandomSeed)))
	library.RawSetString("sin", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: SinUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("sqrt", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: SqrtUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("tan", runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{Function: TanUnaryValue, AcceptedKinds: numberUnaryKinds}))
	library.RawSetString("tointeger", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(ToInteger)))
	library.RawSetString("type", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Type)))
	library.RawSetString("ult", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(ULT)))
	state.SetGlobal("math", runtime.ReferenceValue(runtime.KindTable, library))
	return nil
}

// Abs 实现 Lua 5.3 `math.abs` 的基础数值语义。
//
// 第一个参数必须是 number；integer 入参返回 integer，float number 入参返回 number。
// int64 最小值无法取正数，当前按 Lua integer 补码边界保留原值，后续官方兼容测试再收敛。
func Abs(args ...runtime.Value) ([]runtime.Value, error) {
	// abs 首先解析 number 参数。
	value, err := numberValueArgument(args, 1, "abs")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}
	if value.Kind == runtime.KindInteger {
		// integer 快路径保留整数类型。
		if value.Integer < 0 && value.Integer != math.MinInt64 {
			// 普通负整数可以安全取反。
			return []runtime.Value{runtime.IntegerValue(-value.Integer)}, nil
		}
		// 非负整数或 MinInt64 边界直接返回原 integer。
		return []runtime.Value{value}, nil
	}

	// float number 使用 Go math.Abs。
	return []runtime.Value{runtime.NumberValue(math.Abs(value.Number))}, nil
}

// AbsUnaryValue 实现 Lua 5.3 `math.abs` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；integer 入参返回 integer，float number 入参返回 number。
// 该入口服务 VM CALL fast path，避免标准库热点调用构造临时参数和结果切片。
func AbsUnaryValue(value runtime.Value) (runtime.Value, error) {
	// abs 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("abs", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 快路径保留整数类型。
		if value.Integer < 0 && value.Integer != math.MinInt64 {
			// 普通负整数可以安全取反。
			return runtime.IntegerValue(-value.Integer), nil
		}
		// 非负整数或 MinInt64 边界直接返回原 integer。
		return value, nil
	}

	// float number 使用 Go math.Abs。
	return runtime.NumberValue(math.Abs(value.Number)), nil
}

// ACos 实现 Lua 5.3 `math.acos`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是 float number，非法定义域
// 由 Go math.Acos 返回 NaN。
func ACos(args ...runtime.Value) ([]runtime.Value, error) {
	// acos 首先解析 number 参数。
	value, err := numberArgument(args, 1, "acos")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 返回反余弦结果。
	return []runtime.Value{runtime.NumberValue(math.Acos(value))}, nil
}

// ACosUnaryValue 实现 Lua 5.3 `math.acos` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数和结果切片。
func ACosUnaryValue(value runtime.Value) (runtime.Value, error) {
	// acos 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("acos", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后计算反余弦。
		return runtime.NumberValue(math.Acos(float64(value.Integer))), nil
	}

	// number 入参直接计算反余弦。
	return runtime.NumberValue(math.Acos(value.Number)), nil
}

// ASin 实现 Lua 5.3 `math.asin`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是 float number，非法定义域
// 由 Go math.Asin 返回 NaN。
func ASin(args ...runtime.Value) ([]runtime.Value, error) {
	// asin 首先解析 number 参数。
	value, err := numberArgument(args, 1, "asin")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 返回反正弦结果。
	return []runtime.Value{runtime.NumberValue(math.Asin(value))}, nil
}

// ASinUnaryValue 实现 Lua 5.3 `math.asin` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数和结果切片。
func ASinUnaryValue(value runtime.Value) (runtime.Value, error) {
	// asin 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("asin", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后计算反正弦。
		return runtime.NumberValue(math.Asin(float64(value.Integer))), nil
	}

	// number 入参直接计算反正弦。
	return runtime.NumberValue(math.Asin(value.Number)), nil
}

// ATan 实现 Lua 5.3 `math.atan`。
//
// 第一个参数 y 必须是 number；第二个参数 x 可选且必须是 number，默认 1。返回值为
// `atan2(y, x)`，与 Lua 5.3 `math.atan(y [, x])` 兼容。
func ATan(args ...runtime.Value) ([]runtime.Value, error) {
	// atan 首先解析 y 参数。
	y, err := numberArgument(args, 1, "atan")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	x := float64(1)
	if len(args) >= 2 {
		// 第二个参数存在时作为 atan2 的 x 参数。
		x, err = numberArgument(args, 2, "atan")
		if err != nil {
			// 第二个参数不是 number 时直接返回 Lua 参数错误。
			return nil, err
		}
	}

	// 返回 atan2(y, x) 结果。
	return []runtime.Value{runtime.NumberValue(math.Atan2(y, x))}, nil
}

// ATanUnaryValue 实现 Lua 5.3 `math.atan(y)` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口只覆盖默认
// x=1 的单参数调用，多参数 `math.atan(y, x)` 仍由通用 Go closure 路径处理。
func ATanUnaryValue(value runtime.Value) (runtime.Value, error) {
	// atan 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("atan", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64，并按 Lua 5.3 默认 x=1 计算 atan2。
		return runtime.NumberValue(math.Atan2(float64(value.Integer), 1)), nil
	}

	// number 入参直接按默认 x=1 计算 atan2。
	return runtime.NumberValue(math.Atan2(value.Number, 1)), nil
}

// Ceil 实现 Lua 5.3 `math.ceil`。
//
// 第一个参数必须是 number；integer 入参原样返回，float number 入参返回向上取整后的
// integer。若结果超出 int64 或为 NaN/Inf，则返回 float number，避免 Go 整数转换溢出。
func Ceil(args ...runtime.Value) ([]runtime.Value, error) {
	// ceil 首先解析 number 参数。
	value, err := numberValueArgument(args, 1, "ceil")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}
	if value.Kind == runtime.KindInteger {
		// integer 已经是整数，直接返回。
		return []runtime.Value{value}, nil
	}

	ceiled := math.Ceil(value.Number)
	if math.IsNaN(ceiled) || math.IsInf(ceiled, 0) || ceiled < float64(math.MinInt64) || ceiled >= -float64(math.MinInt64) {
		// 非有限或超出 int64 范围时保留 float number。
		return []runtime.Value{runtime.NumberValue(ceiled)}, nil
	}

	// 有限且可表达时返回 Lua integer。
	return []runtime.Value{runtime.IntegerValue(int64(ceiled))}, nil
}

// CeilUnaryValue 实现 Lua 5.3 `math.ceil` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；integer 入参原样返回，float number 入参按 Lua 5.3
// 语义向上取整。该入口服务 VM CALL fast path，避免构造临时参数和结果切片。
func CeilUnaryValue(value runtime.Value) (runtime.Value, error) {
	// ceil 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if !value.IsNumber() {
		// 非 number 类型不做字符串转数值隐式转换。
		return runtime.NilValue(), badArgument("ceil", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 已经是整数，直接返回。
		return value, nil
	}

	ceiled := math.Ceil(value.Number)
	if math.IsNaN(ceiled) || math.IsInf(ceiled, 0) || ceiled < float64(math.MinInt64) || ceiled >= -float64(math.MinInt64) {
		// 非有限或超出 int64 范围时保留 float number。
		return runtime.NumberValue(ceiled), nil
	}

	// 有限且可表达时返回 Lua integer。
	return runtime.IntegerValue(int64(ceiled)), nil
}

// Cos 实现 Lua 5.3 `math.cos`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是 float number，入参单位为
// 弧度，定义域和 NaN/Inf 行为由 Go math.Cos 承接。
func Cos(args ...runtime.Value) ([]runtime.Value, error) {
	// cos 首先解析弧度参数。
	value, err := numberArgument(args, 1, "cos")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 返回余弦结果。
	return []runtime.Value{runtime.NumberValue(math.Cos(value))}, nil
}

// CosUnaryValue 实现 Lua 5.3 `math.cos` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数和结果切片。
func CosUnaryValue(value runtime.Value) (runtime.Value, error) {
	// cos 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("cos", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后计算三角函数。
		return runtime.NumberValue(math.Cos(float64(value.Integer))), nil
	}

	// number 入参直接计算三角函数。
	return runtime.NumberValue(math.Cos(value.Number)), nil
}

// Deg 实现 Lua 5.3 `math.deg`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是从弧度转换得到的角度
// float number，转换公式为 `x * 180 / pi`。
func Deg(args ...runtime.Value) ([]runtime.Value, error) {
	// deg 首先解析弧度参数。
	value, err := numberArgument(args, 1, "deg")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 返回角度结果。
	return []runtime.Value{runtime.NumberValue(value * 180 / math.Pi)}, nil
}

// DegUnaryValue 实现 Lua 5.3 `math.deg` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数和结果切片。
func DegUnaryValue(value runtime.Value) (runtime.Value, error) {
	// deg 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("deg", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后执行弧度到角度转换。
		return runtime.NumberValue(float64(value.Integer) * 180 / math.Pi), nil
	}

	// number 入参直接执行弧度到角度转换。
	return runtime.NumberValue(value.Number * 180 / math.Pi), nil
}

// Exp 实现 Lua 5.3 `math.exp`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是 e 的 x 次幂，溢出、
// 下溢、NaN 和 Inf 行为由 Go math.Exp 承接。
func Exp(args ...runtime.Value) ([]runtime.Value, error) {
	// exp 首先解析指数参数。
	value, err := numberArgument(args, 1, "exp")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 返回指数结果。
	return []runtime.Value{runtime.NumberValue(math.Exp(value))}, nil
}

// ExpUnaryValue 实现 Lua 5.3 `math.exp` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数和结果切片。
func ExpUnaryValue(value runtime.Value) (runtime.Value, error) {
	// exp 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("exp", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后计算指数。
		return runtime.NumberValue(math.Exp(float64(value.Integer))), nil
	}

	// number 入参直接计算指数。
	return runtime.NumberValue(math.Exp(value.Number)), nil
}

// Floor 实现 Lua 5.3 `math.floor`。
//
// 第一个参数必须是 number；integer 入参原样返回，float number 入参返回向下取整后的
// integer。若结果超出 int64 或为 NaN/Inf，则返回 float number，避免 Go 整数转换溢出。
func Floor(args ...runtime.Value) ([]runtime.Value, error) {
	// floor 首先执行单返回入口，保持导出多返回签名兼容既有调用方。
	value, err := FloorValue(args...)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}

	// 多返回 API 包装为单元素切片。
	return []runtime.Value{value}, nil
}

// FloorValue 实现 Lua 5.3 `math.floor` 的单返回热路径。
//
// 第一个参数必须是 number；integer 入参原样返回，float number 入参返回向下取整后的
// integer。错误语义与 Floor 保持一致，供 VM 内部 GoFunction 快路径避免结果切片分配。
func FloorValue(args ...runtime.Value) (runtime.Value, error) {
	// floor 首先解析 number 参数。
	value, err := numberValueArgument(args, 1, "floor")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return runtime.NilValue(), err
	}
	if value.Kind == runtime.KindInteger {
		// integer 已经是整数，直接返回。
		return value, nil
	}

	floored := math.Floor(value.Number)
	if math.IsNaN(floored) || math.IsInf(floored, 0) || floored < float64(math.MinInt64) || floored >= -float64(math.MinInt64) {
		// 非有限或超出 int64 范围时保留 float number。
		return runtime.NumberValue(floored), nil
	}

	// 有限且可表达时返回 Lua integer。
	return runtime.IntegerValue(int64(floored)), nil
}

// FloorUnaryValue 实现 Lua 5.3 `math.floor` 的单参数单返回热路径。
//
// value 是调用方已经从 Lua 寄存器读取出的第一个参数；返回语义和错误语义与 FloorValue 保持一致。
func FloorUnaryValue(value runtime.Value) (runtime.Value, error) {
	// floor 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if !value.IsNumber() {
		// 非 number 类型不做字符串转数值隐式转换。
		return runtime.NilValue(), badArgument("floor", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 已经是整数，直接返回。
		return value, nil
	}

	floored := math.Floor(value.Number)
	if math.IsNaN(floored) || math.IsInf(floored, 0) || floored < float64(math.MinInt64) || floored >= -float64(math.MinInt64) {
		// 非有限或超出 int64 范围时保留 float number。
		return runtime.NumberValue(floored), nil
	}

	// 有限且可表达时返回 Lua integer。
	return runtime.IntegerValue(int64(floored)), nil
}

// FMod 实现 Lua 5.3 `math.fmod`。
//
// 前两个参数必须是 number；两个 integer 入参走整数取余并返回 integer，否则转换为
// float64 后使用 math.Mod。除数为 0 时返回 Lua 参数错误，避免宿主整数除零崩溃。
func FMod(args ...runtime.Value) ([]runtime.Value, error) {
	// fmod 首先解析被除数参数，并保留 integer/float 类型信息。
	xValue, err := numberValueArgument(args, 1, "fmod")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}
	yValue, err := numberValueArgument(args, 2, "fmod")
	if err != nil {
		// 第二个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	if yValue.Kind == runtime.KindInteger && yValue.Integer == 0 {
		// integer 除数为 0 会触发 Go 运行时异常，因此按 Lua 参数错误提前退出。
		return nil, badArgument("fmod", 2, "zero divisor")
	}

	yNumber, _ := yValue.ToNumber()
	if yValue.Kind == runtime.KindNumber && yNumber == 0 {
		// float 除数为 0 没有有效余数，按 Lua 参数错误提前退出。
		return nil, badArgument("fmod", 2, "zero divisor")
	}

	if xValue.Kind == runtime.KindInteger && yValue.Kind == runtime.KindInteger {
		// 两个 integer 走整数取余，并保留 Lua integer 结果类型。
		return []runtime.Value{runtime.IntegerValue(xValue.Integer % yValue.Integer)}, nil
	}

	xNumber, _ := xValue.ToNumber()
	// 混合 number 或纯 float 使用 IEEE 浮点取模。
	return []runtime.Value{runtime.NumberValue(math.Mod(xNumber, yNumber))}, nil
}

// Log 实现 Lua 5.3 `math.log`。
//
// 第一个参数必须是 number；第二个参数可选且必须是 number。未提供 base 时返回自然
// 对数；base 为 10 时使用 math.Log10，其余 base 返回 `log(x)/log(base)`。
func Log(args ...runtime.Value) ([]runtime.Value, error) {
	// log 首先解析待求对数的 number 参数。
	value, err := numberArgument(args, 1, "log")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}
	if len(args) < 2 {
		// 未提供 base 时按 Lua 5.3 默认自然对数返回。
		return []runtime.Value{runtime.NumberValue(math.Log(value))}, nil
	}

	base, err := numberArgument(args, 2, "log")
	if err != nil {
		// 第二个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}
	if base == 10 {
		// base 为 10 时走 Log10，匹配 Lua C 实现的专门分支。
		return []runtime.Value{runtime.NumberValue(math.Log10(value))}, nil
	}

	// 其他 base 使用换底公式，NaN/Inf 语义由 Go math.Log 承接。
	return []runtime.Value{runtime.NumberValue(math.Log(value) / math.Log(base))}, nil
}

// Max 实现 Lua 5.3 `math.max`。
//
// 至少需要一个 number 参数；所有参数都必须是 number。返回值是数值最大的原始参数，
// 因此 integer 获胜时保留 integer，float number 获胜时保留 number。
func Max(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// Lua 5.3 对 max 缺失首个值报告 value expected，区别于非 number 参数。
		return nil, badArgument("max", 1, "value expected")
	}
	// max 先读取第一个参数作为当前最大值。
	best, err := numberValueArgument(args, 1, "max")
	if err != nil {
		// 缺失参数或第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	for index := 2; index <= len(args); index++ {
		// 逐个读取后续 number 参数，并用 Lua 1-based 位置生成错误信息。
		candidate, err := numberValueArgument(args, index, "max")
		if err != nil {
			// 任一参数不是 number 时直接返回 Lua 参数错误。
			return nil, err
		}
		if compareNumberValues(candidate, best) > 0 {
			// 候选值更大时替换当前最大值，保留候选原始 Lua 类型。
			best = candidate
		}
	}

	// 返回遍历得到的最大原始参数。
	return []runtime.Value{best}, nil
}

// Min 实现 Lua 5.3 `math.min`。
//
// 至少需要一个 number 参数；所有参数都必须是 number。返回值是数值最小的原始参数，
// 因此 integer 获胜时保留 integer，float number 获胜时保留 number。
func Min(args ...runtime.Value) ([]runtime.Value, error) {
	if len(args) == 0 {
		// Lua 5.3 对 min 缺失首个值报告 value expected，区别于非 number 参数。
		return nil, badArgument("min", 1, "value expected")
	}
	// min 先读取第一个参数作为当前最小值。
	best, err := numberValueArgument(args, 1, "min")
	if err != nil {
		// 缺失参数或第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	for index := 2; index <= len(args); index++ {
		// 逐个读取后续 number 参数，并用 Lua 1-based 位置生成错误信息。
		candidate, err := numberValueArgument(args, index, "min")
		if err != nil {
			// 任一参数不是 number 时直接返回 Lua 参数错误。
			return nil, err
		}
		if compareNumberValues(candidate, best) < 0 {
			// 候选值更小时替换当前最小值，保留候选原始 Lua 类型。
			best = candidate
		}
	}

	// 返回遍历得到的最小原始参数。
	return []runtime.Value{best}, nil
}

// ModF 实现 Lua 5.3 `math.modf`。
//
// 第一个参数必须是 number。返回两个值：整数部分与小数部分；integer 入参返回原 integer
// 与 number 0，float number 的整数部分可安全转换时返回 integer，否则保留 number。
func ModF(args ...runtime.Value) ([]runtime.Value, error) {
	// modf 首先解析 number 参数，并保留 integer/float 类型信息。
	value, err := numberValueArgument(args, 1, "modf")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}
	if value.Kind == runtime.KindInteger {
		// integer 没有小数部分，按 Lua 5.3 返回原整数与 0.0。
		return []runtime.Value{value, runtime.NumberValue(0)}, nil
	}
	if math.IsInf(value.Number, 0) {
		// Lua 5.3 对无穷值返回无穷整数部分和带正号的 0.0 小数部分。
		return []runtime.Value{runtime.NumberValue(value.Number), runtime.NumberValue(0)}, nil
	}

	integerPart, fractionalPart := math.Modf(value.Number)
	if math.IsNaN(integerPart) || math.IsInf(integerPart, 0) || integerPart < float64(math.MinInt64) || integerPart >= -float64(math.MinInt64) {
		// 非有限或超出 int64 范围时，整数部分保留 float number。
		return []runtime.Value{runtime.NumberValue(integerPart), runtime.NumberValue(fractionalPart)}, nil
	}

	// 有限且可表达时，整数部分返回 Lua integer，小数部分返回 Lua number。
	return []runtime.Value{runtime.IntegerValue(int64(integerPart)), runtime.NumberValue(fractionalPart)}, nil
}

// Rad 实现 Lua 5.3 `math.rad`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是从角度转换得到的弧度
// float number，转换公式为 `x * pi / 180`。
func Rad(args ...runtime.Value) ([]runtime.Value, error) {
	// rad 首先解析角度参数。
	value, err := numberArgument(args, 1, "rad")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 返回弧度结果。
	return []runtime.Value{runtime.NumberValue(value * math.Pi / 180)}, nil
}

// RadUnaryValue 实现 Lua 5.3 `math.rad` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数和结果切片。
func RadUnaryValue(value runtime.Value) (runtime.Value, error) {
	// rad 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("rad", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后执行角度到弧度转换。
		return runtime.NumberValue(float64(value.Integer) * math.Pi / 180), nil
	}

	// number 入参直接执行角度到弧度转换。
	return runtime.NumberValue(value.Number * math.Pi / 180), nil
}

// Random 实现 Lua 5.3 `math.random` 的基础区间语义。
//
// 无参数时返回 `[0,1)` 的 number；一个 integer 参数 upper 时返回 `[1, upper]` 的
// integer；两个 integer 参数 low、high 时返回 `[low, high]` 的 integer。非法区间返回
// Lua 参数错误。随机种子控制由后续 `math.randomseed` TODO 补齐。
func Random(args ...runtime.Value) ([]runtime.Value, error) {
	// random 先按参数数量选择 Lua 5.3 的三个调用形态。
	switch len(args) {
	case 0:
		// 无参数返回半开区间 [0,1) 的浮点随机数。
		return []runtime.Value{runtime.NumberValue(randomFloat64())}, nil
	case 1:
		// 单参数形态等价于区间 [1, upper]。
		upper, err := integerArgument(args, 1, "random")
		if err != nil {
			// upper 不是 integer 时直接返回 Lua 参数错误。
			return nil, err
		}
		if upper < 1 {
			// 上界小于 1 时区间为空，按 Lua 参数错误提前退出。
			return nil, badArgument("random", 1, "interval is empty")
		}
		// 返回闭区间 [1, upper] 内的 integer。
		return []runtime.Value{runtime.IntegerValue(randomIntegerInRange(1, upper))}, nil
	case 2:
		// 双参数形态使用调用方给定的闭区间 [low, high]。
		low, err := integerArgument(args, 1, "random")
		if err != nil {
			// low 不是 integer 时直接返回 Lua 参数错误。
			return nil, err
		}
		high, err := integerArgument(args, 2, "random")
		if err != nil {
			// high 不是 integer 时直接返回 Lua 参数错误。
			return nil, err
		}
		if low > high {
			// 下界大于上界时区间为空，按 Lua 参数错误提前退出。
			return nil, badArgument("random", 1, "interval is empty")
		}
		if uint64(high)-uint64(low) > uint64(math.MaxInt64) {
			// Lua 5.3 random 整数区间最多覆盖 2^63 个值，超过时报告区间过大。
			return nil, badArgument("random", 1, "interval is too large")
		}
		// 返回闭区间 [low, high] 内的 integer。
		return []runtime.Value{runtime.IntegerValue(randomIntegerInRange(low, high))}, nil
	default:
		// Lua 5.3 math.random 最多接受两个区间参数。
		return nil, badArgument("random", 3, "too many arguments")
	}
}

// RandomSeed 实现 Lua 5.3 `math.randomseed`。
//
// 第一个参数必须可无损转换为 integer；该值用于重置 math.random 的包级随机源。函数不
// 返回值，后续 random 调用会从新种子开始生成可复现序列。
func RandomSeed(args ...runtime.Value) ([]runtime.Value, error) {
	// randomseed 首先解析 seed 参数。
	seed, err := integerArgument(args, 1, "randomseed")
	if err != nil {
		// seed 不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}

	randomMutex.Lock()
	// 在互斥保护下重置独立随机源，避免并发 random 读取半更新状态。
	randomGenerator.Seed(seed)
	randomMutex.Unlock()

	// Lua 5.3 randomseed 不产生返回值。
	return nil, nil
}

// Sin 实现 Lua 5.3 `math.sin`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是 float number，入参单位为
// 弧度，定义域和 NaN/Inf 行为由 Go math.Sin 承接。
func Sin(args ...runtime.Value) ([]runtime.Value, error) {
	// sin 首先执行单返回入口，保持导出多返回签名兼容既有调用方。
	value, err := SinValue(args...)
	if err != nil {
		// 参数错误直接返回给调用方。
		return nil, err
	}

	// 返回正弦结果。
	return []runtime.Value{value}, nil
}

// SinValue 实现 Lua 5.3 `math.sin` 的单返回热路径。
//
// args 必须提供第一个 number 参数；返回值始终是 Lua float number，错误语义与 Sin 保持一致。
func SinValue(args ...runtime.Value) (runtime.Value, error) {
	// sin 首先解析弧度参数。
	value, err := numberArgument(args, 1, "sin")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return runtime.NilValue(), err
	}

	// 返回正弦结果。
	return runtime.NumberValue(math.Sin(value)), nil
}

// SinUnaryValue 实现 Lua 5.3 `math.sin` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数切片。
func SinUnaryValue(value runtime.Value) (runtime.Value, error) {
	// sin 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("sin", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后计算三角函数。
		return runtime.NumberValue(math.Sin(float64(value.Integer))), nil
	}

	// number 入参直接计算三角函数。
	return runtime.NumberValue(math.Sin(value.Number)), nil
}

// Sqrt 实现 Lua 5.3 `math.sqrt`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是平方根 float number，
// 负数定义域由 Go math.Sqrt 返回 NaN。
func Sqrt(args ...runtime.Value) ([]runtime.Value, error) {
	// sqrt 首先执行单返回入口，保持导出多返回签名兼容既有调用方。
	value, err := SqrtValue(args...)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}

	// 多返回 API 包装为单元素切片。
	return []runtime.Value{value}, nil
}

// SqrtValue 实现 Lua 5.3 `math.sqrt` 的单返回热路径。
//
// 第一个参数必须是 number；integer 会转换为 float64。错误语义与 Sqrt 保持一致，供 VM
// 内部 GoFunction 快路径避免结果切片分配。
func SqrtValue(args ...runtime.Value) (runtime.Value, error) {
	// sqrt 首先解析 number 参数。
	value, err := numberArgument(args, 1, "sqrt")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return runtime.NilValue(), err
	}

	// 返回平方根结果。
	return runtime.NumberValue(math.Sqrt(value)), nil
}

// SqrtUnaryValue 实现 Lua 5.3 `math.sqrt` 的单参数单返回热路径。
//
// value 是调用方已经从 Lua 寄存器读取出的第一个参数；返回语义和错误语义与 SqrtValue 保持一致。
func SqrtUnaryValue(value runtime.Value) (runtime.Value, error) {
	// sqrt 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if !value.IsNumber() {
		// 非 number 类型不做字符串转数值隐式转换。
		return runtime.NilValue(), badArgument("sqrt", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	numberValue, _ := value.ToNumber()

	// 返回平方根结果。
	return runtime.NumberValue(math.Sqrt(numberValue)), nil
}

// Tan 实现 Lua 5.3 `math.tan`。
//
// 第一个参数必须是 number；integer 会转换为 float64。返回值是 float number，入参单位为
// 弧度，定义域和 NaN/Inf 行为由 Go math.Tan 承接。
func Tan(args ...runtime.Value) ([]runtime.Value, error) {
	// tan 首先解析弧度参数。
	value, err := numberArgument(args, 1, "tan")
	if err != nil {
		// 第一个参数不是 number 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 返回正切结果。
	return []runtime.Value{runtime.NumberValue(math.Tan(value))}, nil
}

// TanUnaryValue 实现 Lua 5.3 `math.tan` 的单参数单返回热路径。
//
// value 必须是 number 或 integer；返回值始终是 Lua float number。该入口服务 VM CALL
// fast path，避免标准库热点调用构造临时参数和结果切片。
func TanUnaryValue(value runtime.Value) (runtime.Value, error) {
	// tan 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindInteger && value.Kind != runtime.KindNumber {
		// 非 number 入参按 Lua 标准库参数错误返回。
		return runtime.NilValue(), badArgument("tan", 1, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}
	if value.Kind == runtime.KindInteger {
		// integer 入参转换为 float64 后计算三角函数。
		return runtime.NumberValue(math.Tan(float64(value.Integer))), nil
	}

	// number 入参直接计算三角函数。
	return runtime.NumberValue(math.Tan(value.Number)), nil
}

// ToInteger 实现 Lua 5.3 `math.tointeger`。
//
// 参数必须存在；若参数可无损转换为 Lua integer，则返回该 integer，否则返回 nil。
// 字符串参数会先按 Lua number 文本解析，再尝试无损转换为 integer。
func ToInteger(args ...runtime.Value) ([]runtime.Value, error) {
	// tointeger 需要至少一个待转换值。
	if len(args) == 0 {
		// 缺失参数时按 Lua 参数错误提前退出。
		return nil, badArgument("tointeger", 1, "value expected")
	}

	value := args[0]
	if value.Kind == runtime.KindString {
		// Lua 5.3 math.tointeger 会对字符串执行数字解析，例如 mininteger 拼接出的十进制文本。
		converted, ok := value.StringToNumber()
		if !ok {
			// 字符串不是合法 number 时返回 nil，而不是抛出参数错误。
			return []runtime.Value{runtime.NilValue()}, nil
		}
		value = converted
	}

	integerValue, ok := value.ToSignedInteger()
	if !ok {
		// 不可无损转换为 integer 时按 Lua 5.3 返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	// 可转换时返回 Lua integer。
	return []runtime.Value{runtime.IntegerValue(integerValue)}, nil
}

// Type 实现 Lua 5.3 `math.type`。
//
// 参数必须存在；integer 返回字符串 `"integer"`，float number 返回字符串 `"float"`，
// 非 number 返回 nil。该函数不执行字符串到 number 的隐式转换。
func Type(args ...runtime.Value) ([]runtime.Value, error) {
	// type 需要至少一个待判断值。
	if len(args) == 0 {
		// 缺失参数时按 Lua 参数错误提前退出。
		return nil, badArgument("type", 1, "value expected")
	}

	switch args[0].Kind {
	case runtime.KindInteger:
		// Lua integer 返回固定字符串 integer。
		return []runtime.Value{runtime.StringValue("integer")}, nil
	case runtime.KindNumber:
		// Lua float number 返回固定字符串 float。
		return []runtime.Value{runtime.StringValue("float")}, nil
	default:
		// 非 number 值按 Lua 5.3 返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
}

// ULT 实现 Lua 5.3 `math.ult`。
//
// 前两个参数必须可无损转换为 Lua integer；比较时转为 uint64 后执行无符号小于判断，
// 返回 Lua boolean。
func ULT(args ...runtime.Value) ([]runtime.Value, error) {
	// ult 首先解析左侧 integer 参数。
	left, err := integerArgument(args, 1, "ult")
	if err != nil {
		// 第一个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}
	right, err := integerArgument(args, 2, "ult")
	if err != nil {
		// 第二个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}

	// 使用 uint64 表达 Lua 5.3 unsigned integer 比较语义。
	return []runtime.Value{runtime.BooleanValue(uint64(left) < uint64(right))}, nil
}

// numberValueArgument 按 Lua 标准库参数规则提取 number 值。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。返回原始 Lua number
// 值，便于调用方决定是否保留 integer 类型。
func numberValueArgument(args []runtime.Value, position int, functionName string) (runtime.Value, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失参数按 nil 处理，并报告 number expected。
		return runtime.NilValue(), badArgument(functionName, position, "number expected")
	}
	value := args[position-1]
	if !value.IsNumber() {
		// 非 number 类型不做字符串转数值隐式转换。
		return runtime.NilValue(), badArgument(functionName, position, fmt.Sprintf("number expected, got %s", runtime.LuaErrorTypeName(value)))
	}

	// 返回原始 number 值，保留 integer/float 区分。
	return value, nil
}

// numberArgument 按 Lua 标准库参数规则提取 float64 number。
//
// integer 会转换为 float64；float number 原样返回。非 number 返回 Lua 参数错误。
func numberArgument(args []runtime.Value, position int, functionName string) (float64, error) {
	// 先提取原始 number 值。
	value, err := numberValueArgument(args, position, functionName)
	if err != nil {
		// 参数错误直接返回。
		return 0, err
	}

	numberValue, _ := value.ToNumber()
	return numberValue, nil
}

// integerArgument 按 Lua 标准库参数规则提取 integer。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。integer 与可无损转换的
// float number 都会被接受，其他类型返回 Lua 参数错误。
func integerArgument(args []runtime.Value, position int, functionName string) (int64, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失整数参数无法提供默认值，由调用方决定哪些参数可选。
		return 0, badArgument(functionName, position, "integer expected")
	}

	integerValue, ok := args[position-1].ToInteger()
	if !ok {
		// 非整数值不能作为 math 标准库整数参数。
		return 0, badArgument(functionName, position, "integer expected")
	}

	// 返回已转换的 int64 Lua integer。
	return integerValue, nil
}

// compareNumberValues 比较两个 Lua number 值。
//
// left 和 right 必须已通过 numberValueArgument 校验。返回值大于 0 表示 left 更大，
// 小于 0 表示 left 更小，等于 0 表示数值相等；两个 integer 走 int64 比较，混合类型走
// float64 比较以兼容 Lua 5.3 number 比较规则。
func compareNumberValues(left runtime.Value, right runtime.Value) int {
	// 两侧均为 integer 时优先使用精确整数比较。
	if left.Kind == runtime.KindInteger && right.Kind == runtime.KindInteger {
		// 左侧整数更大时返回正数。
		if left.Integer > right.Integer {
			return 1
		}
		if left.Integer < right.Integer {
			// 左侧整数更小时返回负数。
			return -1
		}
		// 两个整数相等时返回 0。
		return 0
	}

	leftNumber, _ := left.ToNumber()
	rightNumber, _ := right.ToNumber()
	if leftNumber > rightNumber {
		// 左侧浮点数值更大时返回正数。
		return 1
	}
	if leftNumber < rightNumber {
		// 左侧浮点数值更小时返回负数。
		return -1
	}
	// 两个浮点数值相等或包含 NaN 无法排序时返回 0，保持先出现参数。
	return 0
}

// randomIntegerInRange 返回闭区间 [low, high] 内的随机 Lua integer。
//
// 调用方必须保证 low <= high。内部使用 uint64 跨越有符号边界，支持包含负数以及完整
// int64 范围的区间；当前使用取模映射，后续可在 randomseed 兼容验证时再收敛偏差。
func randomIntegerInRange(low int64, high int64) int64 {
	// 使用无符号差值处理跨越负数的闭区间长度。
	span := uint64(high) - uint64(low) + 1
	if span == 0 {
		// span 为 0 表示覆盖完整 uint64 空间，直接平移随机 64 位值。
		return int64(uint64(low) + randomUint64())
	}

	// 普通区间使用取模获得偏移，再平移回 Lua integer 区间。
	return int64(uint64(low) + (randomUint64() % span))
}

// randomFloat64 从包级随机源读取 `[0,1)` 的 float64。
//
// 调用方无需额外加锁；本函数内部使用 randomMutex 保证并发安全。
func randomFloat64() float64 {
	randomMutex.Lock()
	// 在互斥保护下读取浮点随机数。
	value := randomGenerator.Float64()
	randomMutex.Unlock()
	return value
}

// randomUint64 从包级随机源读取 uint64。
//
// 调用方无需额外加锁；本函数内部使用 randomMutex 保证并发安全。
func randomUint64() uint64 {
	randomMutex.Lock()
	// 在互斥保护下读取 64 位随机数。
	value := randomGenerator.Uint64()
	randomMutex.Unlock()
	return value
}

// badArgument 构造 Lua 标准库参数错误。
//
// functionName 是标准库函数名，position 是 Lua 1-based 参数序号，detail 是期望或错误原因。
func badArgument(functionName string, position int, detail string) error {
	// 标准库参数错误以 Lua string error object 传播。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (%s)", position, functionName, detail)))
}
