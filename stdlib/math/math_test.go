package mathlib

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestOpenRegistersMathLibrary 验证 Open 会注册 math 库和本阶段支持的函数。
//
// 测试通过全局表读取库对象，确认每个函数都以 Go closure 暴露，后续 VM CALL 可进入标准库。
func TestOpenRegistersMathLibrary(t *testing.T) {
	// 测试先创建新的 State，避免污染其他标准库注册用例。
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// Open 失败表示 math 标准库无法作为全局库暴露。
		t.Fatalf("Open failed: %v", err)
	}

	libraryValue := state.GetGlobal("math")
	if libraryValue.Kind != runtime.KindTable {
		// math 全局变量必须指向库表。
		t.Fatalf("global math kind mismatch: %v", libraryValue.DebugString())
	}
	library, ok := libraryValue.Ref.(*runtime.Table)
	if !ok || library == nil {
		// KindTable 的引用负载必须是 runtime.Table。
		t.Fatalf("global math payload mismatch: %#v", libraryValue.Ref)
	}

	for _, name := range []string{"abs", "acos", "asin", "atan", "ceil", "cos", "deg", "exp", "floor", "fmod", "log", "max", "min", "modf", "rad", "random", "randomseed", "sin", "sqrt", "tan", "tointeger", "type", "ult"} {
		// 每个本阶段函数都应作为 Go closure 注册在库表上。
		functionValue := library.RawGetString(name)
		if functionValue.Kind != runtime.KindGoClosure {
			// 缺失或类型错误都会导致 VM CALL 无法进入标准库函数。
			t.Fatalf("math.%s kind mismatch: %v", name, functionValue.DebugString())
		}
	}

	hugeValue := library.RawGetString("huge")
	if hugeValue.Kind != runtime.KindNumber || !math.IsInf(hugeValue.Number, 1) {
		// math.huge 必须是正无穷 float number 常量。
		t.Fatalf("math.huge mismatch: %v", hugeValue.DebugString())
	}
	maxIntegerValue := library.RawGetString("maxinteger")
	if maxIntegerValue.Kind != runtime.KindInteger || maxIntegerValue.Integer != math.MaxInt64 {
		// math.maxinteger 必须是 Lua integer 最大值。
		t.Fatalf("math.maxinteger mismatch: %v", maxIntegerValue.DebugString())
	}
	minIntegerValue := library.RawGetString("mininteger")
	if minIntegerValue.Kind != runtime.KindInteger || minIntegerValue.Integer != math.MinInt64 {
		// math.mininteger 必须是 Lua integer 最小值。
		t.Fatalf("math.mininteger mismatch: %v", minIntegerValue.DebugString())
	}
	piValue := library.RawGetString("pi")
	if piValue.Kind != runtime.KindNumber || piValue.Number != math.Pi {
		// math.pi 必须是 pi float number 常量。
		t.Fatalf("math.pi mismatch: %v", piValue.DebugString())
	}
}

// TestNumberArgumentReportsMetatableName 验证 number 参数错误展示元表 `__name`。
//
// 参数必须是非 number Lua 值；带字符串型 `__name` 的 table 应在 math.sin 错误中显示该
// 类型名，兼容 Lua 5.3 errors.lua 的 named object 小节。
func TestNumberArgumentReportsMetatableName(t *testing.T) {
	// 构造带 __name 的 table，模拟 Lua 侧自定义类型名。
	table := runtime.NewTable()
	metatable := runtime.NewTable()
	metatable.RawSetString("__name", runtime.StringValue("My Type"))
	table.SetMetatable(metatable)

	_, err := Sin(runtime.ReferenceValue(runtime.KindTable, table))
	if err == nil {
		// 非 number 参数必须返回 Lua 参数错误。
		t.Fatalf("Sin with named table succeeded")
	}
	if !errors.Is(err, runtime.ErrLuaError) || !strings.Contains(runtime.ErrorObject(err).String, "number expected, got My Type") {
		// 错误文本必须包含 got My Type，不能只报告 number expected。
		t.Fatalf("Sin error = %v", err)
	}
}

// TestAbsPreservesIntegerAndHandlesFloat 验证 math.abs 的 integer/float 结果类型。
//
// Lua 5.3 math.abs 对 integer 输入应返回 integer，对 float number 输入返回 number。
func TestAbsPreservesIntegerAndHandlesFloat(t *testing.T) {
	// 负整数应返回正整数。
	results, err := Abs(runtime.IntegerValue(-7))
	if err != nil {
		// 合法 integer 调用不应失败。
		t.Fatalf("Abs integer failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 7 {
		// integer abs 必须保留 integer 类型。
		t.Fatalf("Abs integer result mismatch: %#v", results)
	}

	results, err = Abs(runtime.NumberValue(-1.25))
	if err != nil {
		// 合法 float 调用不应失败。
		t.Fatalf("Abs number failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNumber || results[0].Number != 1.25 {
		// float abs 必须返回 float number。
		t.Fatalf("Abs number result mismatch: %#v", results)
	}
}

// TestACosASinAndATan 验证反三角函数基础结果。
//
// 用例覆盖 acos、asin 以及 atan 的双参数 atan2 语义。
func TestACosASinAndATan(t *testing.T) {
	// acos(1) 应为 0。
	acosResults, err := ACos(runtime.IntegerValue(1))
	if err != nil {
		// 合法 acos 调用不应失败。
		t.Fatalf("ACos failed: %v", err)
	}
	if len(acosResults) != 1 || acosResults[0].Kind != runtime.KindNumber || acosResults[0].Number != 0 {
		// acos(1) 的结果应为 0。
		t.Fatalf("ACos result mismatch: %#v", acosResults)
	}

	asinResults, err := ASin(runtime.IntegerValue(0))
	if err != nil {
		// 合法 asin 调用不应失败。
		t.Fatalf("ASin failed: %v", err)
	}
	if len(asinResults) != 1 || asinResults[0].Kind != runtime.KindNumber || asinResults[0].Number != 0 {
		// asin(0) 的结果应为 0。
		t.Fatalf("ASin result mismatch: %#v", asinResults)
	}

	atanResults, err := ATan(runtime.IntegerValue(1), runtime.IntegerValue(1))
	if err != nil {
		// 合法 atan 调用不应失败。
		t.Fatalf("ATan failed: %v", err)
	}
	if len(atanResults) != 1 || atanResults[0].Kind != runtime.KindNumber || math.Abs(atanResults[0].Number-math.Pi/4) > 1e-12 {
		// atan(1,1) 应等于 pi/4。
		t.Fatalf("ATan result mismatch: %#v", atanResults)
	}
}

// TestCeilReturnsIntegerWhenPossible 验证 math.ceil 的 integer 返回语义。
//
// float number 可安全取整时应返回 Lua integer，integer 输入应原样返回。
func TestCeilReturnsIntegerWhenPossible(t *testing.T) {
	// 浮点值 1.2 向上取整为 integer 2。
	results, err := Ceil(runtime.NumberValue(1.2))
	if err != nil {
		// 合法 ceil 调用不应失败。
		t.Fatalf("Ceil number failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 2 {
		// 可表达整数结果应返回 integer。
		t.Fatalf("Ceil number result mismatch: %#v", results)
	}

	results, err = Ceil(runtime.IntegerValue(3))
	if err != nil {
		// 合法 integer ceil 调用不应失败。
		t.Fatalf("Ceil integer failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 3 {
		// integer 输入应原样返回。
		t.Fatalf("Ceil integer result mismatch: %#v", results)
	}
}

// TestCosDegAndExp 验证三角、角度转换和指数函数基础结果。
//
// 用例使用确定性输入，确认 math.cos、math.deg 和 math.exp 均返回 Lua float number。
func TestCosDegAndExp(t *testing.T) {
	// cos(0) 应返回 1。
	cosResults, err := Cos(runtime.IntegerValue(0))
	if err != nil {
		// 合法 cos 调用不应失败。
		t.Fatalf("Cos failed: %v", err)
	}
	if len(cosResults) != 1 || cosResults[0].Kind != runtime.KindNumber || cosResults[0].Number != 1 {
		// cos(0) 必须返回 float number 1。
		t.Fatalf("Cos result mismatch: %#v", cosResults)
	}

	degResults, err := Deg(runtime.NumberValue(math.Pi))
	if err != nil {
		// 合法 deg 调用不应失败。
		t.Fatalf("Deg failed: %v", err)
	}
	if len(degResults) != 1 || degResults[0].Kind != runtime.KindNumber || math.Abs(degResults[0].Number-180) > 1e-12 {
		// pi 弧度应转换为 180 度。
		t.Fatalf("Deg result mismatch: %#v", degResults)
	}

	expResults, err := Exp(runtime.IntegerValue(0))
	if err != nil {
		// 合法 exp 调用不应失败。
		t.Fatalf("Exp failed: %v", err)
	}
	if len(expResults) != 1 || expResults[0].Kind != runtime.KindNumber || expResults[0].Number != 1 {
		// exp(0) 必须返回 float number 1。
		t.Fatalf("Exp result mismatch: %#v", expResults)
	}
}

// TestFloorReturnsIntegerWhenPossible 验证 math.floor 的 integer 返回语义。
//
// float number 可安全取整时应返回 Lua integer，integer 输入应原样返回。
func TestFloorReturnsIntegerWhenPossible(t *testing.T) {
	// 浮点值 1.8 向下取整为 integer 1。
	results, err := Floor(runtime.NumberValue(1.8))
	if err != nil {
		// 合法 floor 调用不应失败。
		t.Fatalf("Floor number failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 1 {
		// 可表达整数结果应返回 integer。
		t.Fatalf("Floor number result mismatch: %#v", results)
	}

	results, err = Floor(runtime.IntegerValue(-3))
	if err != nil {
		// 合法 integer floor 调用不应失败。
		t.Fatalf("Floor integer failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != -3 {
		// integer 输入应原样返回。
		t.Fatalf("Floor integer result mismatch: %#v", results)
	}
}

// TestFModIntegerFloatAndZeroDivisor 验证 math.fmod 的整数、浮点和除零行为。
//
// 两个 integer 输入应保留 integer 结果；任一 float number 输入应返回 number；除数为 0
// 时必须返回 Lua error，避免 Go 整数除零异常。
func TestFModIntegerFloatAndZeroDivisor(t *testing.T) {
	// 两个 integer 参数使用整数取余。
	results, err := FMod(runtime.IntegerValue(7), runtime.IntegerValue(3))
	if err != nil {
		// 合法 integer fmod 调用不应失败。
		t.Fatalf("FMod integer failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 1 {
		// integer fmod 必须保留 integer 类型。
		t.Fatalf("FMod integer result mismatch: %#v", results)
	}

	results, err = FMod(runtime.NumberValue(7.5), runtime.IntegerValue(2))
	if err != nil {
		// 合法 float fmod 调用不应失败。
		t.Fatalf("FMod number failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNumber || math.Abs(results[0].Number-1.5) > 1e-12 {
		// 混合 number fmod 应返回 float number。
		t.Fatalf("FMod number result mismatch: %#v", results)
	}

	_, err = FMod(runtime.IntegerValue(1), runtime.IntegerValue(0))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 除数为 0 必须以 Lua 参数错误传播。
		t.Fatalf("FMod zero divisor error mismatch: %v", err)
	}
}

// TestLogDefaultBaseAndExplicitBase 验证 math.log 的默认底数和显式底数。
//
// Lua 5.3 在未传 base 时返回自然对数，base 为 10 时使用十进制对数，其他 base 使用
// 换底公式。
func TestLogDefaultBaseAndExplicitBase(t *testing.T) {
	// log(e) 默认返回 1。
	results, err := Log(runtime.NumberValue(math.E))
	if err != nil {
		// 合法自然对数调用不应失败。
		t.Fatalf("Log default failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNumber || math.Abs(results[0].Number-1) > 1e-12 {
		// log(e) 应接近 1。
		t.Fatalf("Log default result mismatch: %#v", results)
	}

	results, err = Log(runtime.IntegerValue(100), runtime.IntegerValue(10))
	if err != nil {
		// 合法十进制对数调用不应失败。
		t.Fatalf("Log base10 failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNumber || math.Abs(results[0].Number-2) > 1e-12 {
		// log(100,10) 应接近 2。
		t.Fatalf("Log base10 result mismatch: %#v", results)
	}

	results, err = Log(runtime.IntegerValue(8), runtime.IntegerValue(2))
	if err != nil {
		// 合法换底对数调用不应失败。
		t.Fatalf("Log base2 failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNumber || math.Abs(results[0].Number-3) > 1e-12 {
		// log(8,2) 应接近 3。
		t.Fatalf("Log base2 result mismatch: %#v", results)
	}
}

// TestMaxAndMinPreserveWinningValueType 验证 math.max 与 math.min 返回获胜原始参数。
//
// 全整数比较应返回 integer；混合比较中获胜的 float number 应保留 number 类型。
func TestMaxAndMinPreserveWinningValueType(t *testing.T) {
	// 全整数 max 应返回最大 integer。
	maxResults, err := Max(runtime.IntegerValue(-1), runtime.IntegerValue(9), runtime.IntegerValue(3))
	if err != nil {
		// 合法 max 调用不应失败。
		t.Fatalf("Max integer failed: %v", err)
	}
	if len(maxResults) != 1 || maxResults[0].Kind != runtime.KindInteger || maxResults[0].Integer != 9 {
		// integer 获胜时必须保留 integer 类型。
		t.Fatalf("Max integer result mismatch: %#v", maxResults)
	}

	maxResults, err = Max(runtime.IntegerValue(1), runtime.NumberValue(2.5), runtime.IntegerValue(2))
	if err != nil {
		// 合法混合 max 调用不应失败。
		t.Fatalf("Max mixed failed: %v", err)
	}
	if len(maxResults) != 1 || maxResults[0].Kind != runtime.KindNumber || maxResults[0].Number != 2.5 {
		// float number 获胜时必须保留 number 类型。
		t.Fatalf("Max mixed result mismatch: %#v", maxResults)
	}

	minResults, err := Min(runtime.IntegerValue(4), runtime.IntegerValue(-2), runtime.NumberValue(-1.5))
	if err != nil {
		// 合法 min 调用不应失败。
		t.Fatalf("Min integer failed: %v", err)
	}
	if len(minResults) != 1 || minResults[0].Kind != runtime.KindInteger || minResults[0].Integer != -2 {
		// integer 最小值获胜时必须保留 integer 类型。
		t.Fatalf("Min integer result mismatch: %#v", minResults)
	}
}

// TestModFReturnsIntegerAndFractionalParts 验证 math.modf 的双返回值语义。
//
// integer 输入应返回原 integer 与 0.0；float number 输入应拆分为 integer 部分和小数部分。
func TestModFReturnsIntegerAndFractionalParts(t *testing.T) {
	// integer 输入没有小数部分。
	results, err := ModF(runtime.IntegerValue(7))
	if err != nil {
		// 合法 integer modf 调用不应失败。
		t.Fatalf("ModF integer failed: %v", err)
	}
	if len(results) != 2 || results[0].Kind != runtime.KindInteger || results[0].Integer != 7 || results[1].Kind != runtime.KindNumber || results[1].Number != 0 {
		// integer 输入应返回原 integer 与 number 0。
		t.Fatalf("ModF integer result mismatch: %#v", results)
	}

	results, err = ModF(runtime.NumberValue(-1.25))
	if err != nil {
		// 合法 float modf 调用不应失败。
		t.Fatalf("ModF number failed: %v", err)
	}
	if len(results) != 2 || results[0].Kind != runtime.KindInteger || results[0].Integer != -1 || results[1].Kind != runtime.KindNumber || math.Abs(results[1].Number+0.25) > 1e-12 {
		// float 输入应按趋零整数部分和保留符号的小数部分拆分。
		t.Fatalf("ModF number result mismatch: %#v", results)
	}

	results, err = ModF(runtime.NumberValue(math.Inf(-1)))
	if err != nil {
		// 合法负无穷 modf 调用不应失败。
		t.Fatalf("ModF inf failed: %v", err)
	}
	if len(results) != 2 || results[0].Kind != runtime.KindNumber || !math.IsInf(results[0].Number, -1) || results[1].Kind != runtime.KindNumber || results[1].Number != 0 {
		// Lua 5.3 要求无穷值的小数部分为 0.0，而不是 Go math.Modf 的 NaN。
		t.Fatalf("ModF inf result mismatch: %#v", results)
	}
}

// TestRadConvertsDegreesToRadians 验证 math.rad 的角度到弧度转换。
//
// 输入 180 度应返回 pi 弧度，结果类型保持 Lua float number。
func TestRadConvertsDegreesToRadians(t *testing.T) {
	// 180 度对应 pi 弧度。
	results, err := Rad(runtime.IntegerValue(180))
	if err != nil {
		// 合法 rad 调用不应失败。
		t.Fatalf("Rad failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNumber || math.Abs(results[0].Number-math.Pi) > 1e-12 {
		// rad(180) 应接近 pi。
		t.Fatalf("Rad result mismatch: %#v", results)
	}
}

// TestRandomReturnsValuesInsideLuaIntervals 验证 math.random 的三种参数形态。
//
// 测试只断言返回类型和闭区间边界，避免依赖随机序列具体值；randomseed 的确定性语义后续补齐。
func TestRandomReturnsValuesInsideLuaIntervals(t *testing.T) {
	// 无参数 random 应返回 [0,1) 内的 number。
	results, err := Random()
	if err != nil {
		// 合法无参 random 调用不应失败。
		t.Fatalf("Random default failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNumber || results[0].Number < 0 || results[0].Number >= 1 {
		// 无参结果必须落在半开区间 [0,1)。
		t.Fatalf("Random default result mismatch: %#v", results)
	}

	results, err = Random(runtime.IntegerValue(3))
	if err != nil {
		// 合法单参数 random 调用不应失败。
		t.Fatalf("Random upper failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer < 1 || results[0].Integer > 3 {
		// 单参数结果必须落在闭区间 [1, upper]。
		t.Fatalf("Random upper result mismatch: %#v", results)
	}

	results, err = Random(runtime.IntegerValue(-2), runtime.IntegerValue(2))
	if err != nil {
		// 合法双参数 random 调用不应失败。
		t.Fatalf("Random range failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer < -2 || results[0].Integer > 2 {
		// 双参数结果必须落在闭区间 [low, high]。
		t.Fatalf("Random range result mismatch: %#v", results)
	}
}

// TestRandomSeedMakesRandomSequenceRepeatable 验证 math.randomseed 会重置随机序列。
//
// 使用同一 seed 两次初始化后，下一次 math.random 的结果应一致。
func TestRandomSeedMakesRandomSequenceRepeatable(t *testing.T) {
	// 第一次设置 seed 并读取一个随机数。
	results, err := RandomSeed(runtime.IntegerValue(12345))
	if err != nil {
		// 合法 randomseed 调用不应失败。
		t.Fatalf("RandomSeed first failed: %v", err)
	}
	if len(results) != 0 {
		// Lua 5.3 randomseed 不返回任何值。
		t.Fatalf("RandomSeed first result mismatch: %#v", results)
	}
	firstResults, err := Random()
	if err != nil {
		// 合法 random 调用不应失败。
		t.Fatalf("Random first failed: %v", err)
	}

	results, err = RandomSeed(runtime.IntegerValue(12345))
	if err != nil {
		// 第二次使用同一 seed 不应失败。
		t.Fatalf("RandomSeed second failed: %v", err)
	}
	if len(results) != 0 {
		// Lua 5.3 randomseed 第二次调用同样不返回任何值。
		t.Fatalf("RandomSeed second result mismatch: %#v", results)
	}
	secondResults, err := Random()
	if err != nil {
		// 合法 random 调用不应失败。
		t.Fatalf("Random second failed: %v", err)
	}
	if len(firstResults) != 1 || len(secondResults) != 1 || firstResults[0].Number != secondResults[0].Number {
		// 同一 seed 后的第一项随机序列必须可复现。
		t.Fatalf("RandomSeed repeatability mismatch: %#v %#v", firstResults, secondResults)
	}
}

// TestSinSqrtAndTan 验证 math.sin、math.sqrt 和 math.tan 的基础数值结果。
//
// 这些函数都接收 number 并返回 Lua float number，底层 NaN/Inf 语义由 Go math 包承接。
func TestSinSqrtAndTan(t *testing.T) {
	// sin(0) 应返回 0。
	sinResults, err := Sin(runtime.IntegerValue(0))
	if err != nil {
		// 合法 sin 调用不应失败。
		t.Fatalf("Sin failed: %v", err)
	}
	if len(sinResults) != 1 || sinResults[0].Kind != runtime.KindNumber || sinResults[0].Number != 0 {
		// sin(0) 必须返回 float number 0。
		t.Fatalf("Sin result mismatch: %#v", sinResults)
	}
	sinUnaryResult, err := SinUnaryValue(runtime.IntegerValue(0))
	if err != nil {
		// 一元 fast path 对合法 integer 入参不应失败。
		t.Fatalf("SinUnaryValue failed: %v", err)
	}
	if sinUnaryResult.Kind != runtime.KindNumber || sinUnaryResult.Number != 0 {
		// fast path 必须保持 math.sin 返回 float number 的语义。
		t.Fatalf("SinUnaryValue result mismatch: %#v", sinUnaryResult)
	}

	sqrtResults, err := Sqrt(runtime.IntegerValue(9))
	if err != nil {
		// 合法 sqrt 调用不应失败。
		t.Fatalf("Sqrt failed: %v", err)
	}
	if len(sqrtResults) != 1 || sqrtResults[0].Kind != runtime.KindNumber || sqrtResults[0].Number != 3 {
		// sqrt(9) 必须返回 float number 3。
		t.Fatalf("Sqrt result mismatch: %#v", sqrtResults)
	}

	tanResults, err := Tan(runtime.IntegerValue(0))
	if err != nil {
		// 合法 tan 调用不应失败。
		t.Fatalf("Tan failed: %v", err)
	}
	if len(tanResults) != 1 || tanResults[0].Kind != runtime.KindNumber || tanResults[0].Number != 0 {
		// tan(0) 必须返回 float number 0。
		t.Fatalf("Tan result mismatch: %#v", tanResults)
	}
}

// TestMathNumberArgumentsAcceptNumericStrings 验证 math 数值参数接受 numeric string。
//
// Lua 5.3 的 luaL_checknumber/luaL_checkinteger 会接受可转换的字符串；LPeg 等 C 模块
// 通过 capture 把文本传给 math.sin 时依赖该通用标准库语义。
func TestMathNumberArgumentsAcceptNumericStrings(t *testing.T) {
	// 普通多返回入口应把 numeric string 转为 number 后计算。
	sinResults, err := Sin(runtime.StringValue("2.34"))
	if err != nil {
		// numeric string 是合法 number 参数，不应返回参数错误。
		t.Fatalf("Sin numeric string failed: %v", err)
	}
	if len(sinResults) != 1 || sinResults[0].Kind != runtime.KindNumber || math.Abs(sinResults[0].Number-math.Sin(2.34)) > 1e-12 {
		// math.sin("2.34") 应等价于 math.sin(2.34)。
		t.Fatalf("Sin numeric string result mismatch: %#v", sinResults)
	}

	sinUnaryResult, err := SinUnaryValue(runtime.StringValue("2.34"))
	if err != nil {
		// native C callback 直接调用 fast unary 负载时也必须接受 numeric string。
		t.Fatalf("SinUnaryValue numeric string failed: %v", err)
	}
	if sinUnaryResult.Kind != runtime.KindNumber || math.Abs(sinUnaryResult.Number-math.Sin(2.34)) > 1e-12 {
		// 一元入口结果必须与普通入口一致。
		t.Fatalf("SinUnaryValue numeric string result mismatch: %#v", sinUnaryResult)
	}

	floorUnaryResult, err := FloorUnaryValue(runtime.StringValue("9.75"))
	if err != nil {
		// floor 的一元入口同样复用 number 参数转换规则。
		t.Fatalf("FloorUnaryValue numeric string failed: %v", err)
	}
	if floorUnaryResult.Kind != runtime.KindInteger || floorUnaryResult.Integer != 9 {
		// 可表达的 floor 结果应返回 Lua integer。
		t.Fatalf("FloorUnaryValue numeric string result mismatch: %#v", floorUnaryResult)
	}

	ultResults, err := ULT(runtime.StringValue("1"), runtime.StringValue("2"))
	if err != nil {
		// integer 参数也应接受可无损转换的 numeric string。
		t.Fatalf("ULT numeric string failed: %v", err)
	}
	if len(ultResults) != 1 || ultResults[0].Kind != runtime.KindBoolean || !ultResults[0].Bool {
		// 无符号比较 1 < 2 应返回 true。
		t.Fatalf("ULT numeric string result mismatch: %#v", ultResults)
	}

	if _, err := Sin(runtime.StringValue("not-a-number")); err == nil {
		// 不可转换字符串仍必须保持 number expected 参数错误。
		t.Fatalf("Sin invalid numeric string succeeded")
	}

	typeResults, err := Type(runtime.StringValue("2.34"))
	if err != nil {
		// math.type 不属于 number 参数检查，不应因为字符串可转换而报错。
		t.Fatalf("Type numeric string failed: %v", err)
	}
	if len(typeResults) != 1 || !typeResults[0].IsNil() {
		// math.type 按 Lua 5.3 只识别真实 integer/float，不隐式转换 string。
		t.Fatalf("Type numeric string result mismatch: %#v", typeResults)
	}
}

// TestToIntegerReturnsIntegerOrNil 验证 math.tointeger 的转换语义。
//
// 可无损转换的 number 返回 integer；不可转换值返回 nil；缺失参数属于参数错误。
func TestToIntegerReturnsIntegerOrNil(t *testing.T) {
	// 整数型浮点可无损转换为 Lua integer。
	results, err := ToInteger(runtime.NumberValue(42))
	if err != nil {
		// 合法 tointeger 调用不应失败。
		t.Fatalf("ToInteger convertible failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 42 {
		// 可转换值必须返回 Lua integer。
		t.Fatalf("ToInteger convertible result mismatch: %#v", results)
	}

	results, err = ToInteger(runtime.StringValue("-9223372036854775808"))
	if err != nil {
		// 合法整数字符串 tointeger 调用不应失败。
		t.Fatalf("ToInteger string mininteger failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != math.MinInt64 {
		// Lua 5.3 要求 math.tointeger 先解析字符串再尝试 integer 转换。
		t.Fatalf("ToInteger string mininteger result mismatch: %#v", results)
	}

	results, err = ToInteger(runtime.NumberValue(1.5))
	if err != nil {
		// 不可转换值应返回 nil 而不是错误。
		t.Fatalf("ToInteger fractional failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNil {
		// 非整数 number 必须返回 nil。
		t.Fatalf("ToInteger fractional result mismatch: %#v", results)
	}

	results, err = ToInteger(runtime.NumberValue(math.Ldexp(1, 63)))
	if err != nil {
		// 超出有符号范围的整数型 float 应返回 nil 而不是错误。
		t.Fatalf("ToInteger out-of-range float failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNil {
		// math.tointeger 不允许复用位运算的 2^63 补码回绕。
		t.Fatalf("ToInteger out-of-range float result mismatch: %#v", results)
	}

	results, err = ToInteger(runtime.StringValue("1.5"))
	if err != nil {
		// 字符串可解析但不能无损转换为 integer 时也不应失败。
		t.Fatalf("ToInteger fractional string failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNil {
		// 非整数数字字符串必须返回 nil。
		t.Fatalf("ToInteger fractional string result mismatch: %#v", results)
	}
}

// TestTypeReportsLuaNumberSubtype 验证 math.type 的 Lua number 子类型语义。
//
// integer 返回 "integer"，float number 返回 "float"，非 number 返回 nil。
func TestTypeReportsLuaNumberSubtype(t *testing.T) {
	// Lua integer 应报告 integer。
	results, err := Type(runtime.IntegerValue(7))
	if err != nil {
		// 合法 integer type 调用不应失败。
		t.Fatalf("Type integer failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "integer" {
		// integer 输入必须返回字符串 integer。
		t.Fatalf("Type integer result mismatch: %#v", results)
	}

	results, err = Type(runtime.NumberValue(7))
	if err != nil {
		// 合法 float type 调用不应失败。
		t.Fatalf("Type float failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "float" {
		// float number 输入必须返回字符串 float。
		t.Fatalf("Type float result mismatch: %#v", results)
	}

	results, err = Type(runtime.StringValue("7"))
	if err != nil {
		// 非 number type 调用不应失败。
		t.Fatalf("Type string failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNil {
		// 非 number 输入必须返回 nil。
		t.Fatalf("Type string result mismatch: %#v", results)
	}
}

// TestULTUsesUnsignedIntegerOrdering 验证 math.ult 的无符号比较语义。
//
// 负数转为 uint64 后大于非负数，因此 ult(-1, 1) 为 false，ult(1, -1) 为 true。
func TestULTUsesUnsignedIntegerOrdering(t *testing.T) {
	// 1 按无符号比较小于 -1 的 uint64 表示。
	results, err := ULT(runtime.IntegerValue(1), runtime.IntegerValue(-1))
	if err != nil {
		// 合法 ult 调用不应失败。
		t.Fatalf("ULT positive negative failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindBoolean || !results[0].Bool {
		// uint64(1) < uint64(-1) 必须为 true。
		t.Fatalf("ULT positive negative result mismatch: %#v", results)
	}

	results, err = ULT(runtime.IntegerValue(-1), runtime.IntegerValue(1))
	if err != nil {
		// 合法 ult 调用不应失败。
		t.Fatalf("ULT negative positive failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindBoolean || results[0].Bool {
		// uint64(-1) < uint64(1) 必须为 false。
		t.Fatalf("ULT negative positive result mismatch: %#v", results)
	}
}

// TestMathArgumentErrors 验证 math 标准库参数错误以 Lua error 传播。
//
// 不可转换为 number 的参数必须返回错误，错误链必须包含 runtime.ErrLuaError。
func TestMathArgumentErrors(t *testing.T) {
	// 非 numeric string 不能作为 number，Abs 应返回 Lua 参数错误。
	_, err := Abs(runtime.StringValue("not-a-number"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 参数错误必须以 Lua error 形式传播。
		t.Fatalf("Abs argument error mismatch: %v", err)
	}

	_, err = Max()
	if !errors.Is(err, runtime.ErrLuaError) {
		// max 缺失首个参数时必须以 Lua error 形式传播。
		t.Fatalf("Max missing argument error mismatch: %v", err)
	}
	if errorObject := runtime.ErrorObject(err); errorObject.Kind != runtime.KindString || errorObject.String != "bad argument #1 to 'max' (value expected)" {
		// Lua 5.3 官方测试要求 max 缺参错误文本包含 value expected。
		t.Fatalf("Max missing argument text mismatch: %#v", errorObject)
	}

	_, err = Min(runtime.IntegerValue(1), runtime.StringValue("bad"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// min 任一非 number 参数都必须以 Lua error 形式传播。
		t.Fatalf("Min argument error mismatch: %v", err)
	}

	_, err = Random(runtime.IntegerValue(0))
	if !errors.Is(err, runtime.ErrLuaError) {
		// random 空区间必须以 Lua error 形式传播。
		t.Fatalf("Random empty interval error mismatch: %v", err)
	}

	_, err = Random(runtime.IntegerValue(math.MinInt64), runtime.IntegerValue(0))
	if !errors.Is(err, runtime.ErrLuaError) {
		// random 区间跨度超过 2^63 个值时必须以 Lua error 形式传播。
		t.Fatalf("Random too-large interval error mismatch: %v", err)
	}

	_, err = Random(runtime.NumberValue(1.5))
	if !errors.Is(err, runtime.ErrLuaError) {
		// random 非整数区间参数必须以 Lua error 形式传播。
		t.Fatalf("Random integer argument error mismatch: %v", err)
	}

	_, err = RandomSeed(runtime.StringValue("seed"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// randomseed 非整数 seed 必须以 Lua error 形式传播。
		t.Fatalf("RandomSeed argument error mismatch: %v", err)
	}

	_, err = ToInteger()
	if !errors.Is(err, runtime.ErrLuaError) {
		// tointeger 缺失参数必须以 Lua error 形式传播。
		t.Fatalf("ToInteger missing argument error mismatch: %v", err)
	}

	_, err = Type()
	if !errors.Is(err, runtime.ErrLuaError) {
		// type 缺失参数必须以 Lua error 形式传播。
		t.Fatalf("Type missing argument error mismatch: %v", err)
	}

	_, err = ULT(runtime.IntegerValue(1), runtime.NumberValue(1.5))
	if !errors.Is(err, runtime.ErrLuaError) {
		// ult 非整数参数必须以 Lua error 形式传播。
		t.Fatalf("ULT argument error mismatch: %v", err)
	}
}
