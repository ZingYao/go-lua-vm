package runtime

import (
	"math"
	"strings"
	"testing"
)

// TestValueConstructorsAndPredicates 验证基础 Lua 值构造和类型判断。
//
// 该测试覆盖 nil、boolean、integer、number 和 string 的最小值系统边界。
func TestValueConstructorsAndPredicates(t *testing.T) {
	values := []Value{
		NilValue(),
		BooleanValue(false),
		IntegerValue(7),
		NumberValue(1.5),
		StringValue("lua"),
	}

	// nil 构造值必须能被 IsNil 识别。
	if !values[0].IsNil() {
		t.Fatalf("nil predicate mismatch")
	}
	// boolean 构造值必须能被 IsBoolean 识别。
	if !values[1].IsBoolean() {
		t.Fatalf("boolean predicate mismatch")
	}
	// integer 与 number 都必须满足 IsNumber。
	if !values[2].IsNumber() || !values[3].IsNumber() {
		t.Fatalf("number predicate mismatch")
	}
	// string 构造值必须能被 IsString 识别。
	if !values[4].IsString() {
		t.Fatalf("string predicate mismatch")
	}
}

// TestValueTruthy 验证 Lua 5.3 条件判断真值语义。
//
// Lua 中只有 nil 和 false 为假，0 和空字符串都为真。
func TestValueTruthy(t *testing.T) {
	cases := []struct {
		name  string
		value Value
		want  bool
	}{
		{name: "nil", value: NilValue(), want: false},
		{name: "false", value: BooleanValue(false), want: false},
		{name: "true", value: BooleanValue(true), want: true},
		{name: "zero integer", value: IntegerValue(0), want: true},
		{name: "empty string", value: StringValue(""), want: true},
	}

	for _, testCase := range cases {
		// 每个用例独立校验，便于定位具体 Lua 真值语义退化。
		if got := testCase.value.Truthy(); got != testCase.want {
			t.Fatalf("%s truthy mismatch: got %v want %v", testCase.name, got, testCase.want)
		}
	}
}

// TestValueRawEqual 验证不触发元方法的 raw equality。
//
// 该测试覆盖 nil、boolean、string、number 混合变体和引用 identity。
func TestValueRawEqual(t *testing.T) {
	// nil 与 nil raw 相等。
	if !NilValue().RawEqual(NilValue()) {
		t.Fatalf("nil raw equality mismatch")
	}
	// boolean 必须值一致才相等。
	if BooleanValue(false).RawEqual(BooleanValue(true)) {
		t.Fatalf("boolean raw equality mismatch")
	}
	// string 按字节内容比较。
	if !StringValue("a\x00b").RawEqual(StringValue("a\x00b")) {
		t.Fatalf("string raw equality mismatch")
	}
	// integer 与 float number 在数值等价时相等。
	if !IntegerValue(3).RawEqual(NumberValue(3.0)) {
		t.Fatalf("mixed numeric raw equality mismatch")
	}
	// 2^53+1 转 float64 会舍入到 2^53，不能误判为相等。
	if IntegerValue((int64(1) << 53) + 1).RawEqual(NumberValue(float64(int64(1) << 53))) {
		t.Fatalf("rounded mixed numeric raw equality should differ")
	}
	// 不同基础类型不相等。
	if StringValue("3").RawEqual(IntegerValue(3)) {
		t.Fatalf("different kind raw equality mismatch")
	}

	ref := &struct{ name string }{name: "table"}
	// 同一引用对象 raw 相等。
	if !ReferenceValue(KindTable, ref).RawEqual(ReferenceValue(KindTable, ref)) {
		t.Fatalf("reference raw equality mismatch")
	}
	// 不同引用对象 raw 不相等，测试对象必须非零大小，避免 Go 零大小对象地址复用。
	if ReferenceValue(KindTable, &struct{ id int }{id: 1}).RawEqual(ReferenceValue(KindTable, &struct{ id int }{id: 2})) {
		t.Fatalf("different reference raw equality mismatch")
	}

	loader := GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 测试函数不实际执行，只作为 Go closure identity 比较的负载。
		return nil, nil
	})
	otherLoader := GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 第二个函数用于确认不同 Go closure 不会因为不可比较而 panic 或误判。
		return nil, nil
	})
	// 同一个 Go closure 引用必须可 raw 相等，兼容 Lua 函数值比较。
	if !ReferenceValue(KindGoClosure, loader).RawEqual(ReferenceValue(KindGoClosure, loader)) {
		t.Fatalf("go closure raw equality mismatch")
	}
	// 不同 Go closure 引用必须 raw 不相等。
	if ReferenceValue(KindGoClosure, loader).RawEqual(ReferenceValue(KindGoClosure, otherLoader)) {
		t.Fatalf("different go closure raw equality mismatch")
	}
}

// TestValueDebugString 验证值调试展示保持稳定。
//
// DebugString 用于日志和 golden，不依赖 Lua tostring 元方法。
func TestValueDebugString(t *testing.T) {
	// string 调试展示必须使用带引号的转义格式。
	if got := StringValue("a\nb").DebugString(); got != "string(\"a\\nb\")" {
		t.Fatalf("debug string mismatch: got %s", got)
	}
	// integer 调试展示必须保留整数类型信息。
	if got := IntegerValue(9).DebugString(); got != "integer(9)" {
		t.Fatalf("debug integer mismatch: got %s", got)
	}
}

// TestValueToInteger 验证 Lua number 到 integer 的转换边界。
//
// Lua 5.3 只有整数值或无小数部分且在范围内的 float number 能转换为 integer。
func TestValueToInteger(t *testing.T) {
	// integer 值必须直接转换成功。
	if value, ok := IntegerValue(42).ToInteger(); !ok || value != 42 {
		t.Fatalf("integer to integer mismatch: value=%d ok=%v", value, ok)
	}
	// 无小数部分的 float number 必须转换成功。
	if value, ok := NumberValue(42.0).ToInteger(); !ok || value != 42 {
		t.Fatalf("float integer to integer mismatch: value=%d ok=%v", value, ok)
	}
	// 大于 MaxInt64 但仍在 uint64 范围内的整值按 Lua 5.3 补码语义转换。
	if value, ok := NumberValue(0xF000000000000000).ToInteger(); !ok || value != -1152921504606846976 {
		t.Fatalf("wrapped float integer mismatch: value=%d ok=%v", value, ok)
	}
	// 有小数部分的 float number 必须转换失败。
	if _, ok := NumberValue(42.5).ToInteger(); ok {
		t.Fatalf("fractional float should not convert to integer")
	}
	// 非 number 值必须转换失败。
	if _, ok := StringValue("42").ToInteger(); ok {
		t.Fatalf("string should not convert to integer in numeric conversion path")
	}
}

// TestValueToSignedInteger 验证 Lua 有符号 integer 范围转换。
//
// 该转换用于 math.tointeger，必须拒绝位运算允许的 2^63 浮点补码回绕。
func TestValueToSignedInteger(t *testing.T) {
	// integer 值必须直接转换成功。
	if value, ok := IntegerValue(math.MinInt64).ToSignedInteger(); !ok || value != math.MinInt64 {
		t.Fatalf("signed integer conversion mismatch: value=%d ok=%v", value, ok)
	}
	// 无小数部分且在有符号范围内的 float number 必须转换成功。
	if value, ok := NumberValue(42.0).ToSignedInteger(); !ok || value != 42 {
		t.Fatalf("signed float conversion mismatch: value=%d ok=%v", value, ok)
	}
	// 2^63 超出 Lua integer 正边界，不能按 math.tointeger 语义回绕。
	if _, ok := NumberValue(math.Ldexp(1, 63)).ToSignedInteger(); ok {
		t.Fatalf("signed conversion should reject 2^63")
	}
	// 有小数部分的 float number 必须转换失败。
	if _, ok := NumberValue(1.5).ToSignedInteger(); ok {
		t.Fatalf("fractional float should not convert to signed integer")
	}
}

// TestValueToNumber 验证 Lua number 到 float64 的转换。
//
// integer 和 float number 都属于 Lua number 基础类型，非 number 值不能转换。
func TestValueToNumber(t *testing.T) {
	// integer 应转换为对应 float64。
	if value, ok := IntegerValue(7).ToNumber(); !ok || value != 7 {
		t.Fatalf("integer to number mismatch: value=%f ok=%v", value, ok)
	}
	// float number 应直接返回自身。
	if value, ok := NumberValue(1.25).ToNumber(); !ok || value != 1.25 {
		t.Fatalf("number to number mismatch: value=%f ok=%v", value, ok)
	}
	// nil 不是 number，必须转换失败。
	if _, ok := NilValue().ToNumber(); ok {
		t.Fatalf("nil should not convert to number")
	}
}

// TestValueNumberToString 验证 Lua number 到 string 的基础格式化规则。
//
// Lua 5.3 默认会为浮点整数外观追加 `.0`，以区别 integer 字符串。
func TestValueNumberToString(t *testing.T) {
	cases := []struct {
		name  string
		value Value
		want  string
		ok    bool
	}{
		{name: "integer", value: IntegerValue(12), want: "12", ok: true},
		{name: "float integer", value: NumberValue(12.0), want: "12.0", ok: true},
		{name: "float fraction", value: NumberValue(12.5), want: "12.5", ok: true},
		{name: "non number", value: StringValue("12"), want: "", ok: false},
	}

	for _, testCase := range cases {
		// 每个格式化用例独立校验，便于定位 Lua number-to-string 规则退化。
		got, ok := testCase.value.NumberToString()
		if got != testCase.want || ok != testCase.ok {
			t.Fatalf("%s number string mismatch: got=%q ok=%v want=%q ok=%v", testCase.name, got, ok, testCase.want, testCase.ok)
		}
	}
}

// TestValueStringToNumber 验证 Lua string 到 number 的基础解析规则。
//
// Lua 5.3 会优先解析整数，再解析浮点，并允许首尾空白。
func TestValueStringToNumber(t *testing.T) {
	cases := []struct {
		name string
		text string
		want Value
		ok   bool
	}{
		{name: "decimal integer", text: " 42 ", want: IntegerValue(42), ok: true},
		{name: "negative decimal integer", text: "-42", want: IntegerValue(-42), ok: true},
		{name: "leading zero decimal", text: "010", want: IntegerValue(10), ok: true},
		{name: "hex integer", text: "0x10", want: IntegerValue(16), ok: true},
		{name: "hex integer uint64 wrap", text: "0xffffffffffffffff", want: IntegerValue(-1), ok: true},
		{name: "hex integer long wrap to zero", text: "0x1000000000000000000000000000000", want: IntegerValue(0), ok: true},
		{name: "negative hex integer", text: "-0X10", want: IntegerValue(-16), ok: true},
		{name: "negative hex integer uint64 wrap", text: "-0xfffffffffffffffe", want: IntegerValue(2), ok: true},
		{name: "float", text: "3.5", want: NumberValue(3.5), ok: true},
		{name: "float exponent", text: "1e3", want: NumberValue(1000), ok: true},
		{name: "hex float without exponent", text: "0xAA.0", want: NumberValue(170), ok: true},
		{name: "hex float overflow", text: "0x" + strings.Repeat("f", 300) + ".0", want: NumberValue(math.Inf(1)), ok: true},
		{name: "empty", text: "   ", want: NilValue(), ok: false},
		{name: "invalid", text: "12abc", want: NilValue(), ok: false},
		{name: "nan rejected", text: "nan", want: NilValue(), ok: false},
		{name: "inf rejected", text: "inf", want: NilValue(), ok: false},
	}

	for _, testCase := range cases {
		// 每个解析用例独立校验，便于定位 Lua string-to-number 规则退化。
		got, ok := StringValue(testCase.text).StringToNumber()
		if ok != testCase.ok || !got.RawEqual(testCase.want) || got.Kind != testCase.want.Kind {
			t.Fatalf("%s string number mismatch: got=%#v ok=%v want=%#v ok=%v", testCase.name, got, ok, testCase.want, testCase.ok)
		}
	}
}

// TestValueStringToNumberRejectsNonString 验证非 string 不进入字符串转 number 路径。
//
// 运行时显式 number 转换应使用 ToNumber，StringToNumber 只服务字符串解析。
func TestValueStringToNumberRejectsNonString(t *testing.T) {
	// integer 不是 string，不能通过 StringToNumber 转换。
	if _, ok := IntegerValue(1).StringToNumber(); ok {
		t.Fatalf("integer should not convert through StringToNumber")
	}
}
