// Package runtime 提供 Lua 5.3 虚拟机运行期核心对象。
//
// 本包承载 State、Value、Table、Closure、Coroutine、GC 和错误恢复等内部实现。
// 对外嵌入 API 必须通过 lua 包暴露，避免调用方依赖运行时内部结构。
package runtime

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// ValueKind 表示 Lua 5.3 运行期值的基础类型。
//
// 该枚举对齐 Lua 5.3 的基础类型语义；integer 与 number 分开保存，以保留 Lua 5.3
// 的整数/浮点双数字模型。
type ValueKind uint8

const (
	// KindNil 表示 Lua nil。
	KindNil ValueKind = iota
	// KindBoolean 表示 Lua boolean。
	KindBoolean
	// KindInteger 表示 Lua integer。
	KindInteger
	// KindNumber 表示 Lua float number。
	KindNumber
	// KindString 表示 Lua string。
	KindString
	// KindTable 表示 Lua table，占位引用对象后续接入 Table 实现。
	KindTable
	// KindLuaClosure 表示 Lua closure，占位引用对象后续接入 Closure 实现。
	KindLuaClosure
	// KindGoClosure 表示 Go closure，占位引用对象后续接入 Go/Lua bridge。
	KindGoClosure
	// KindUserdata 表示 Lua userdata，占位引用对象后续接入 userdata 实现。
	KindUserdata
	// KindThread 表示 Lua thread/coroutine，占位引用对象后续接入 coroutine 实现。
	KindThread
)

// Value 表示 Lua 5.3 运行期值。
//
// Kind 决定当前值的有效字段；简单值直接存储在结构体内，复杂引用对象暂时通过 Ref
// 表示 identity，后续会替换为具体 Table、Closure、Userdata 和 Thread 类型。
type Value struct {
	// Kind 是当前值的 Lua 类型。
	Kind ValueKind
	// Bool 保存 KindBoolean 的布尔值。
	Bool bool
	// Integer 保存 KindInteger 的整数值。
	Integer int64
	// Number 保存 KindNumber 的浮点值。
	Number float64
	// String 保存 KindString 的字节字符串。
	String string
	// Ref 保存复杂对象的引用 identity。
	Ref any
}

// GoClosureWithUpvalues 表示带调试 upvalue 元数据的 Go closure。
//
// Function 是实际可调用入口；Upvalues 保存 debug.getupvalue 可见的 Go 闭包捕获值。Go 语言
// 自身闭包无法枚举捕获变量，因此只有显式构造该类型的标准库函数会暴露 upvalue。
type GoClosureWithUpvalues struct {
	// Function 保存实际执行的 Go 多返回函数。
	Function GoResultsFunction
	// Upvalues 保存 Lua debug 库可见的匿名或命名 upvalue 值。
	Upvalues []Value
	// AllowYield 表示该 Go closure 只是 Lua 可续执行入口的薄包装，协程 resume 时不应把它当作
	// 普通不可 yield 的 Go/C 回调边界；默认 false 保持标准库回调边界安全。
	AllowYield bool
}

// NilValue 创建 Lua nil 值。
//
// nil 没有附加负载，返回值只设置 Kind 字段。
func NilValue() Value {
	// nil 值只需要类型标记，其他字段保持零值。
	return Value{Kind: KindNil}
}

// BooleanValue 创建 Lua boolean 值。
//
// 入参 value 直接对应 Lua true 或 false。
func BooleanValue(value bool) Value {
	// boolean 值保存布尔负载，并通过 Kind 区分 false 与 nil。
	return Value{Kind: KindBoolean, Bool: value}
}

// IntegerValue 创建 Lua integer 值。
//
// 入参 value 使用 int64 表达 Lua 5.3 默认整数语义。
func IntegerValue(value int64) Value {
	// integer 值必须保留精确整数，不经过浮点转换。
	return Value{Kind: KindInteger, Integer: value}
}

// NumberValue 创建 Lua float number 值。
//
// 入参 value 使用 float64 表达 Lua 5.3 默认 lua_Number 语义。
func NumberValue(value float64) Value {
	// number 值保存浮点负载，整数应使用 IntegerValue 表示。
	return Value{Kind: KindNumber, Number: value}
}

// StringValue 创建 Lua string 值。
//
// 入参 value 按字节保存，允许包含任意二进制内容。
func StringValue(value string) Value {
	// string 值保存 Go 字符串；长度语义后续按字节计算。
	return Value{Kind: KindString, String: value}
}

// ReferenceValue 创建复杂 Lua 对象占位引用值。
//
// kind 必须是 table、closure、userdata 或 thread 等引用类型；ref 用于 raw equality 的
// identity 比较，后续具体对象实现接入后会替换为强类型字段。
func ReferenceValue(kind ValueKind, ref any) Value {
	// 引用值保存 kind 与 ref，调用方负责传入符合 kind 的对象。
	return Value{Kind: kind, Ref: ref}
}

// IsNil 判断值是否为 Lua nil。
//
// 返回 true 表示值的 Kind 为 KindNil。
func (value Value) IsNil() bool {
	// 直接比较类型标记，保持 Lua 5.3 ttisnil 语义。
	return value.Kind == KindNil
}

// IsBoolean 判断值是否为 Lua boolean。
//
// 返回 true 表示值的 Kind 为 KindBoolean。
func (value Value) IsBoolean() bool {
	// 直接比较类型标记，保持 Lua 5.3 ttisboolean 语义。
	return value.Kind == KindBoolean
}

// IsNumber 判断值是否为 Lua number。
//
// Lua 5.3 中 integer 和 float 都属于 number 基础类型，因此两者都返回 true。
func (value Value) IsNumber() bool {
	// integer 与 float number 都满足 Lua 5.3 ttisnumber 语义。
	return value.Kind == KindInteger || value.Kind == KindNumber
}

// IsString 判断值是否为 Lua string。
//
// 返回 true 表示值的 Kind 为 KindString。
func (value Value) IsString() bool {
	// 直接比较类型标记，后续短/长字符串驻留不会改变基础 string 语义。
	return value.Kind == KindString
}

// Truthy 返回 Lua 条件判断中的真值语义。
//
// Lua 5.3 只有 nil 和 false 为假，数字 0、空字符串、空表都为真。
func (value Value) Truthy() bool {
	// nil 在 Lua 条件中为 false。
	if value.Kind == KindNil {
		return false
	}
	// boolean false 在 Lua 条件中为 false，boolean true 为 true。
	if value.Kind == KindBoolean {
		return value.Bool
	}

	// 除 nil 和 false 之外，所有 Lua 值都为 true。
	return true
}

// RawEqual 判断两个 Lua 值是否 raw 相等。
//
// 该方法不触发 `__eq` 元方法；number 的 integer/float 混合比较按 Lua 5.3 规则处理。
func (value Value) RawEqual(other Value) bool {
	// 类型完全相同时，按各自类型的原始值比较。
	if value.Kind == other.Kind {
		return rawEqualSameKind(value, other)
	}
	// 类型不同但二者都是 number 时，允许 integer/float 等值比较。
	if value.IsNumber() && other.IsNumber() {
		return numericEqual(value, other)
	}

	// 其他不同类型在 raw equality 下必定不相等。
	return false
}

// DebugString 返回值的稳定调试展示。
//
// 该方法用于单测、trace 和错误消息，不等同于 Lua `tostring` 元方法语义。
func (value Value) DebugString() string {
	// 根据类型输出稳定文本，便于 golden 和日志对比。
	switch value.Kind {
	case KindNil:
		// nil 没有负载。
		return "nil"
	case KindBoolean:
		// boolean 输出 true 或 false。
		return fmt.Sprintf("boolean(%v)", value.Bool)
	case KindInteger:
		// integer 输出十进制整数。
		return fmt.Sprintf("integer(%d)", value.Integer)
	case KindNumber:
		// number 使用 %g 保持紧凑稳定。
		return fmt.Sprintf("number(%g)", value.Number)
	case KindString:
		// string 使用 %q 展示转义字节。
		return fmt.Sprintf("string(%q)", value.String)
	default:
		// 引用对象当前只输出类型与引用，后续具体对象会补更友好的格式。
		return fmt.Sprintf("ref(kind=%d)", value.Kind)
	}
}

// ToInteger 尝试把 Lua number 值转换为 integer。
//
// integer 值直接返回；float number 只有在有限、处于 Lua integer 可转换范围内且没有小数
// 部分时才能转换成功。非 number 值返回 false。
func (value Value) ToInteger() (int64, bool) {
	// integer 已经是目标类型，直接返回。
	if value.Kind == KindInteger {
		return value.Integer, true
	}
	// 非 float number 不能按 Lua number-to-integer 规则转换。
	if value.Kind != KindNumber {
		return 0, false
	}
	// NaN 或 Inf 不能转换为 Lua integer。
	if math.IsNaN(value.Number) || math.IsInf(value.Number, 0) {
		return 0, false
	}
	// 小于 int64 最小值时无法按 Lua integer 表示。
	if value.Number < float64(math.MinInt64) {
		return 0, false
	}
	if value.Number < -float64(math.MinInt64) {
		// int64 范围内的浮点整数直接转换并回查精确性。
		integerValue := int64(value.Number)
		if float64(integerValue) != value.Number {
			// 存在小数部分或不可精确回转时，不符合 Lua integer 转换要求。
			return 0, false
		}

		// 浮点值可无损表示为 int64。
		return integerValue, true
	}
	if value.Number >= math.Ldexp(1, 64) {
		// 大于等于 2^64 的浮点值无法按 64 位补码回绕表示。
		return 0, false
	}
	unsignedValue := uint64(value.Number)
	if float64(unsignedValue) != value.Number {
		// 0..2^64 范围内仍必须是可精确表示的整数值。
		return 0, false
	}

	// Lua 5.3 对位运算转换使用 lua_Unsigned 到 lua_Integer 的补码回绕语义。
	return int64(unsignedValue), true
}

// ToSignedInteger 尝试按 Lua 有符号 integer 范围转换值。
//
// integer 值直接返回；float number 必须是有限、无小数且处于 int64 可表示范围内。
// 该方法用于 `math.tointeger` 等不允许 2^63..2^64 浮点补码回绕的标准库语义。
func (value Value) ToSignedInteger() (int64, bool) {
	// integer 已经是有符号 Lua integer，直接返回。
	if value.Kind == KindInteger {
		return value.Integer, true
	}
	// 非 float number 不能按有符号 integer 转换。
	if value.Kind != KindNumber {
		return 0, false
	}
	// NaN、Inf 或超出 int64 数学范围时不能转换。
	if math.IsNaN(value.Number) || math.IsInf(value.Number, 0) || value.Number < float64(math.MinInt64) || value.Number >= -float64(math.MinInt64) {
		return 0, false
	}

	integerValue := int64(value.Number)
	if float64(integerValue) != value.Number {
		// 存在小数部分或不可精确回转时，不能无损表达为 Lua integer。
		return 0, false
	}

	// 返回有符号范围内的精确整数。
	return integerValue, true
}

// ToNumber 尝试把 Lua number 值转换为 float64。
//
// integer 会转换为 float64；float number 直接返回。非 number 值返回 false。
func (value Value) ToNumber() (float64, bool) {
	// integer 属于 Lua number 基础类型，可转换为 float64。
	if value.Kind == KindInteger {
		return float64(value.Integer), true
	}
	// float number 直接返回自身。
	if value.Kind == KindNumber {
		return value.Number, true
	}

	// 非 number 值不能转换为 number。
	return 0, false
}

// NumberToString 按 Lua 5.3 基础规则把 number 值转换为 string。
//
// integer 使用十进制整数格式；float number 使用紧凑格式，若结果看起来像整数则补 `.0`，
// 以对齐 Lua 5.3 luaO_tostring 在默认配置下区分 integer 与 float 字符串的行为。
func (value Value) NumberToString() (string, bool) {
	// integer 使用十进制整数格式。
	if value.Kind == KindInteger {
		return strconv.FormatInt(value.Integer, 10), true
	}
	// 非 float number 不能进行 number-to-string 转换。
	if value.Kind != KindNumber {
		return "", false
	}

	formatted := strconv.FormatFloat(value.Number, 'g', -1, 64)
	if looksLikeIntegerFloat(formatted) {
		// Lua 5.3 默认会为浮点整数外观追加 ".0"，避免与 integer 字符串混淆。
		formatted += ".0"
	}

	// 返回 Lua number 的字符串形式。
	return formatted, true
}

// StringToNumber 尝试按 Lua 5.3 规则把 string 值转换为 number。
//
// 该方法先尝试整数解析，再尝试浮点解析；成功时返回 IntegerValue 或 NumberValue。
// 当前实现支持首尾空白、十进制整数、0x/0X 十六进制整数、十进制浮点和 Go 支持的
// 十六进制浮点文本，并按 Lua 5.3 规则拒绝 NaN/Inf。
func (value Value) StringToNumber() (Value, bool) {
	// 只有 Lua string 才进入字符串转 number 路径。
	if value.Kind != KindString {
		return NilValue(), false
	}

	trimmed := strings.TrimSpace(value.String)
	if trimmed == "" {
		// 空字符串或纯空白不能转换为 Lua number。
		return NilValue(), false
	}

	if integerValue, ok := parseLuaInteger(trimmed); ok {
		// Lua 5.3 优先把合法整数字符串转换为 integer。
		return IntegerValue(integerValue), true
	}

	if numberValue, ok := parseLuaFloat(trimmed); ok {
		// 整数解析失败后再尝试 float number。
		return NumberValue(numberValue), true
	}

	// 整数和浮点解析均失败时，字符串不能转换为 number。
	return NilValue(), false
}

// rawEqualSameKind 比较相同 Kind 的两个值。
//
// 调用方必须保证两个值 Kind 相同；不同 Kind 的 number 混合比较由 numericEqual 处理。
func rawEqualSameKind(left Value, right Value) bool {
	// 根据类型比较对应负载。
	switch left.Kind {
	case KindNil:
		// nil 与 nil 永远相等。
		return true
	case KindBoolean:
		// boolean 必须布尔负载一致。
		return left.Bool == right.Bool
	case KindInteger:
		// integer 必须整数值一致。
		return left.Integer == right.Integer
	case KindNumber:
		// float number 使用 Go ==，符合 Lua 对普通浮点相等的基础语义，NaN 不等于自身。
		return left.Number == right.Number
	case KindString:
		// string 按字节序列比较。
		return left.String == right.String
	default:
		// 引用对象 raw equality 使用引用 identity 比较，不触发元方法。
		return rawReferenceEqual(left.Ref, right.Ref)
	}
}

// rawReferenceEqual 比较两个引用负载是否代表同一个 Lua 对象。
//
// left 和 right 来自相同 Kind 的引用值；可比较 Go 引用直接使用 ==，Go 函数值
// 不可直接比较，因此按函数入口指针兜底，以兼容 Lua 函数值可做 raw equality 的语义。
func rawReferenceEqual(left any, right any) bool {
	// 两个 nil 引用代表同一个空引用身份。
	if left == nil || right == nil {
		return left == right
	}
	if luaClosureEquivalent(left, right) {
		// Lua 5.3 可复用同一 Proto 且 upvalue 绑定相同的 closure identity。
		return true
	}

	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() {
		// 引用负载类型不同，不可能是同一个运行期对象。
		return false
	}
	if leftValue.Type().Comparable() {
		// 可比较引用保留原有 Go identity 语义，例如 table 指针、closure 指针等。
		return left == right
	}
	if leftValue.Kind() == reflect.Func {
		// Go 函数值不可直接 ==；反射指针可稳定比较同一已注册 Go 函数入口。
		return leftValue.Pointer() == rightValue.Pointer()
	}

	// 其他不可比较负载没有稳定 identity 表达，保守视为不相等。
	return false
}

// luaClosureEquivalent 判断两个 Lua closure 是否可视为同一运行期函数对象。
//
// left/right 可为任意引用负载；只有两者都是 *LuaClosure 时才继续比较。Lua 5.3 对相同
// 子 Proto 且捕获同一组 upvalue 的 closure 可复用 identity，closure.lua 依赖该相等语义；
// 捕获 numeric for 控制变量这类每轮独立 upvalue cell 的闭包仍必须不相等。
func luaClosureEquivalent(left any, right any) bool {
	leftClosure, leftOK := left.(*LuaClosure)
	rightClosure, rightOK := right.(*LuaClosure)
	if !leftOK || !rightOK {
		// 非 Lua closure 交回普通引用比较。
		return false
	}
	if leftClosure == nil || rightClosure == nil {
		// nil closure 只应由普通 nil 引用分支处理。
		return leftClosure == rightClosure
	}
	if leftClosure.Proto != rightClosure.Proto {
		// 不同 Proto 一定不是同一个 Lua 函数体。
		return false
	}
	if len(leftClosure.UpvalueCells) != len(rightClosure.UpvalueCells) {
		// upvalue cell 数量不同表示捕获环境不同。
		return false
	}
	for index := range leftClosure.UpvalueCells {
		if leftClosure.UpvalueCells[index] != rightClosure.UpvalueCells[index] {
			// 任一 upvalue cell 不同都会形成独立 closure identity。
			return false
		}
	}
	return true
}

// numericEqual 比较 Lua integer 与 float number 的混合相等。
//
// Lua 5.3 允许不同数字变体在可无损表示时相等，例如 integer(1) == number(1.0)。
func numericEqual(left Value, right Value) bool {
	// 两个 integer 已由 same-kind 处理；这里仍保留分支增强函数自洽性。
	if left.Kind == KindInteger && right.Kind == KindInteger {
		return left.Integer == right.Integer
	}
	// 两个 float number 已由 same-kind 处理；这里仍保留分支增强函数自洽性。
	if left.Kind == KindNumber && right.Kind == KindNumber {
		return left.Number == right.Number
	}
	// left 为 integer、right 为 float 时，检查 float 是否精确等于该整数。
	if left.Kind == KindInteger {
		return floatNumberEqualsInteger(right.Number, left.Integer)
	}

	// right 为 integer、left 为 float 时，检查 float 是否精确等于该整数。
	return floatNumberEqualsInteger(left.Number, right.Integer)
}

// floatNumberEqualsInteger 判断 Lua float number 是否与 integer 数学值精确相等。
//
// 不能直接把 integer 转成 float64 比较；大整数会在 float64 中舍入，导致 2^53 与
// 2^53+1 这类边界误判相等。
func floatNumberEqualsInteger(numberValue float64, integerValue int64) bool {
	if math.IsNaN(numberValue) || math.IsInf(numberValue, 0) {
		// NaN/Inf 不可能与有限 integer 相等。
		return false
	}
	if numberValue < float64(math.MinInt64) || numberValue >= -float64(math.MinInt64) {
		// 超出 int64 数学范围时，不可能与任一 Lua integer 相等。
		return false
	}
	convertedInteger := int64(numberValue)
	if convertedInteger != integerValue {
		// 可转换整数不同，混合数字不相等。
		return false
	}

	// 只有 float 可无损回转到同一个 int64 时才相等。
	return float64(convertedInteger) == numberValue
}

// looksLikeIntegerFloat 判断浮点格式化结果是否只包含可选符号和十进制数字。
//
// 返回 true 时，Lua 5.3 默认 number-to-string 需要追加 `.0`。
func looksLikeIntegerFloat(formatted string) bool {
	// 空字符串不是合法数字输出。
	if formatted == "" {
		return false
	}
	trimmed := formatted
	if formatted[0] == '-' {
		// 负号只允许出现在首位，后续必须仍有至少一位数字。
		trimmed = formatted[1:]
	}
	if trimmed == "" {
		// 只有符号没有数字时，不认为它是整数外观。
		return false
	}
	for _, digit := range trimmed {
		if digit < '0' || digit > '9' {
			// 出现小数点、指数、NaN 或 Inf 文本时，不追加 .0。
			return false
		}
	}

	// 只包含十进制数字，属于 Lua 需要补 .0 的浮点整数外观。
	return !strings.ContainsAny(trimmed, ".eE")
}

// parseLuaInteger 尝试解析 Lua 5.3 整数字符串。
//
// Lua 整数支持十进制和 0x/0X 十六进制；普通前导 0 仍按十进制处理，不按 Go 八进制处理。
func parseLuaInteger(trimmed string) (int64, bool) {
	// 先判断是否为十六进制形式，十六进制允许可选正负号。
	if hasHexIntegerPrefix(trimmed) {
		negative := strings.HasPrefix(trimmed, "-")
		unsignedText := strings.TrimPrefix(strings.TrimPrefix(trimmed, "+"), "-")
		unsignedValue, ok := parseLuaHexUnsigned(unsignedText[2:])
		if !ok {
			// 没有有效十六进制数字或包含非十六进制字符时，不是整数字面量。
			return 0, false
		}
		if negative {
			// 负十六进制整数字符串按 Lua 5.3 补码语义取负回绕。
			return int64(-unsignedValue), true
		}

		// 正十六进制整数字符串允许 uint64 全范围并回绕为 int64。
		return int64(unsignedValue), true
	}

	integerValue, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		// 十进制整数解析失败时，调用方会继续尝试浮点解析。
		return 0, false
	}

	// 十进制整数解析成功。
	return integerValue, true
}

// parseLuaHexUnsigned 按 Lua 5.3 十六进制整数规则解析无符号数值。
//
// text 不包含 0x/0X 前缀；长度可超过 64 位，累积时自然按 uint64 回绕，匹配 Lua integer
// 对十六进制字符串的补码截断语义。
func parseLuaHexUnsigned(text string) (uint64, bool) {
	if text == "" {
		// 0x 后必须至少有一位十六进制数字。
		return 0, false
	}
	var value uint64
	for _, digit := range text {
		// 每个十六进制字符都会把当前值左移 4 位并写入低位 nibble。
		digitValue, ok := luaHexDigitValue(digit)
		if !ok {
			// 小数点、指数或其他字符表示该文本不是十六进制整数。
			return 0, false
		}
		value = (value << 4) | uint64(digitValue)
	}

	// 返回按 64 位自然回绕后的结果。
	return value, true
}

// luaHexDigitValue 返回单个 Lua 十六进制数字的数值。
//
// ok=false 表示该 rune 不是 ASCII 十六进制数字。
func luaHexDigitValue(digit rune) (uint8, bool) {
	switch {
	case digit >= '0' && digit <= '9':
		// 数字字符映射到 0..9。
		return uint8(digit - '0'), true
	case digit >= 'a' && digit <= 'f':
		// 小写十六进制字符映射到 10..15。
		return uint8(digit-'a') + 10, true
	case digit >= 'A' && digit <= 'F':
		// 大写十六进制字符映射到 10..15。
		return uint8(digit-'A') + 10, true
	default:
		// 其他字符不是十六进制数字。
		return 0, false
	}
}

// parseLuaFloat 尝试解析 Lua 5.3 浮点数字符串。
//
// Lua 5.3 明确拒绝 inf 和 nan 文本；其他合法浮点格式交给 Go 的 ParseFloat 处理。
func parseLuaFloat(trimmed string) (float64, bool) {
	lowered := strings.ToLower(trimmed)
	if strings.Contains(lowered, "nan") || strings.Contains(lowered, "inf") {
		// Lua 5.3 l_str2d 会拒绝 inf/nan 文本，避免平台相关特殊值进入运行时。
		return 0, false
	}

	parseText := trimmed
	if hasHexIntegerPrefix(trimmed) && strings.Contains(trimmed, ".") && !strings.ContainsAny(trimmed, "pP") {
		// Lua 5.3 允许十六进制浮点省略 p/P 指数，语义等价于追加 p0。
		parseText += "p0"
	}
	numberValue, err := strconv.ParseFloat(parseText, 64)
	if err != nil {
		var numberError *strconv.NumError
		if !errors.As(err, &numberError) || !errors.Is(numberError.Err, strconv.ErrRange) {
			// 非范围错误表示字符串不是合法 Lua number。
			return 0, false
		}
	}
	if math.IsNaN(numberValue) {
		// 防御性拒绝 NaN，保持与 Lua 5.3 对 nan 文本的拒绝一致。
		return 0, false
	}

	// 浮点解析成功。
	return numberValue, true
}

// hasHexIntegerPrefix 判断字符串是否带有 Lua 5.3 十六进制整数前缀。
//
// 支持可选正负号，返回 true 时调用方可以使用 base 0 解析 0x/0X。
func hasHexIntegerPrefix(trimmed string) bool {
	// 去掉可选正负号后检查 0x/0X 前缀。
	unsigned := trimmed
	if strings.HasPrefix(unsigned, "+") || strings.HasPrefix(unsigned, "-") {
		// 只有符号没有后续内容时，不可能是十六进制整数。
		if len(unsigned) == 1 {
			return false
		}
		unsigned = unsigned[1:]
	}

	// Lua 十六进制整数必须以 0x 或 0X 开头。
	return strings.HasPrefix(unsigned, "0x") || strings.HasPrefix(unsigned, "0X")
}
