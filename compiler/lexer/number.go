package lexer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrInvalidNumber 表示源码中的数字字面量不符合 Lua 5.3 数字语法。
	ErrInvalidNumber = errors.New("invalid number literal")
)

// NumberKind 表示 Lua 数字字面量的扫描分类。
//
// 当前分类只描述词法层形态，不决定运行期一定保存为 integer 或 float；后续 parser/codegen
// 会结合 Lua 5.3 转换规则写入常量表。
type NumberKind string

const (
	// NumberDecimalInteger 表示十进制整数字面量。
	NumberDecimalInteger NumberKind = "decimal_integer"
	// NumberDecimalFloat 表示十进制浮点数字面量。
	NumberDecimalFloat NumberKind = "decimal_float"
	// NumberHexInteger 表示十六进制整数字面量。
	NumberHexInteger NumberKind = "hex_integer"
	// NumberHexFloat 表示十六进制浮点数字面量。
	NumberHexFloat NumberKind = "hex_float"
)

// NumberLiteral 表示扫描出的 Lua 数字字面量。
//
// Text 保存源码原文；Kind 保存词法分类；Integer 仅在整数分类中有效；Number 仅在浮点分类中有效。
type NumberLiteral struct {
	// Text 保存数字字面量源码文本。
	Text string
	// Kind 保存数字字面量词法分类。
	Kind NumberKind
	// Integer 保存整数值，只有 NumberDecimalInteger 和 NumberHexInteger 使用。
	Integer int64
	// Number 保存浮点值，只有 NumberDecimalFloat 和 NumberHexFloat 使用。
	Number float64
	// Position 保存数字字面量起始位置。
	Position Position
}

// ScanNumber 尝试读取当前位置的 Lua 5.3 数字字面量。
//
// 当前位置必须以 ASCII 数字开头，或以 `.` 后接 ASCII 数字开头；返回 ok=false 表示当前位置不是数字，且不会推进输入。
func (lexer *Lexer) ScanNumber() (NumberLiteral, bool, error) {
	startPosition := lexer.source.Position()
	firstRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 不能形成数字字面量。
		return NumberLiteral{Position: startPosition}, false, nil
	}
	if firstRune == '.' && lexer.peekOffsetIsDecimalDigit(1) {
		// Lua 允许 `.0` 和 `.2e2` 这类省略整数部分的十进制浮点。
		return lexer.scanLeadingDotDecimalNumber(startPosition)
	}
	if !isDecimalDigit(firstRune) {
		// 当前字符不是数字，交给其他 token 扫描。
		return NumberLiteral{Position: startPosition}, false, nil
	}
	if firstRune == '0' && lexer.peekIsHexPrefix() {
		// 0x/0X 前缀进入十六进制数字扫描。
		return lexer.scanHexNumber(startPosition)
	}

	// 非十六进制前缀按十进制数字扫描。
	return lexer.scanDecimalNumber(startPosition)
}

// scanLeadingDotDecimalNumber 扫描以小数点开头的十进制浮点数字面量。
//
// startPosition 是小数点起始位置；调用方已确认当前位置是 `.` 且后一个字符是十进制数字。
func (lexer *Lexer) scanLeadingDotDecimalNumber(startPosition Position) (NumberLiteral, bool, error) {
	var builder strings.Builder
	lexer.source.Next()
	builder.WriteByte('.')
	if !lexer.consumeRequiredDecimalDigits(&builder) {
		// 理论上调用方已验证数字存在；保留防御分支避免未来调用错误静默成功。
		return NumberLiteral{Text: builder.String(), Position: startPosition}, true, ErrInvalidNumber
	}
	if lexer.peekRune('e') || lexer.peekRune('E') {
		// e/E 指数表示十进制浮点数。
		lexer.source.Next()
		builder.WriteByte('e')
		lexer.consumeOptionalSign(&builder)
		if !lexer.consumeRequiredDecimalDigits(&builder) {
			// 指数符号后必须至少有一位十进制数字。
			return NumberLiteral{Text: builder.String(), Position: startPosition}, true, ErrInvalidNumber
		}
	}

	text := builder.String()
	numberValue, err := strconv.ParseFloat(text, 64)
	if err != nil {
		// 点开头数字只可能是十进制浮点，解析失败时返回数字字面量错误。
		return NumberLiteral{Text: text, Kind: NumberDecimalFloat, Position: startPosition}, true, fmt.Errorf("%w: %s", ErrInvalidNumber, text)
	}
	return NumberLiteral{Text: text, Kind: NumberDecimalFloat, Number: numberValue, Position: startPosition}, true, nil
}

// scanDecimalNumber 扫描十进制整数或浮点数字面量。
//
// startPosition 是数字起始位置；调用方已确认当前位置是十进制数字。
func (lexer *Lexer) scanDecimalNumber(startPosition Position) (NumberLiteral, bool, error) {
	var builder strings.Builder
	for {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// EOF 结束整数部分。
			break
		}
		if !isDecimalDigit(nextRune) {
			// 非数字结束整数连续段，后续再判断小数点或指数。
			break
		}
		lexer.source.Next()
		builder.WriteRune(nextRune)
	}

	isFloat := false
	if lexer.peekRune('.') && !lexer.peekRuneOffset(1, '.') {
		// 单个点表示小数点；两个点是连接操作符，不能被数字吞掉。
		isFloat = true
		lexer.source.Next()
		builder.WriteRune('.')
		for {
			nextRune, ok := lexer.source.Peek()
			if !ok {
				// EOF 结束小数部分。
				break
			}
			if !isDecimalDigit(nextRune) {
				// 非数字结束小数部分。
				break
			}
			lexer.source.Next()
			builder.WriteRune(nextRune)
		}
	}
	if lexer.peekRune('e') || lexer.peekRune('E') {
		// e/E 指数表示十进制浮点数。
		isFloat = true
		lexer.source.Next()
		builder.WriteByte('e')
		lexer.consumeOptionalSign(&builder)
		if !lexer.consumeRequiredDecimalDigits(&builder) {
			// 指数符号后必须至少有一位十进制数字。
			return NumberLiteral{Text: builder.String(), Position: startPosition}, true, ErrInvalidNumber
		}
	}

	text := builder.String()
	if isFloat {
		// 十进制浮点交给 strconv.ParseFloat 解析，保持 Go 与 Lua 都接受的指数形式。
		numberValue, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return NumberLiteral{Text: text, Kind: NumberDecimalFloat, Position: startPosition}, true, fmt.Errorf("%w: %s", ErrInvalidNumber, text)
		}
		return NumberLiteral{Text: text, Kind: NumberDecimalFloat, Number: numberValue, Position: startPosition}, true, nil
	}

	integerValue, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		// 十进制整数超出 int64 时按 Lua 5.3 numeral 规则回退为浮点字面量。
		numberValue, floatErr := strconv.ParseFloat(text, 64)
		if floatErr != nil {
			// 整数和浮点都无法解析时，返回数字字面量错误。
			return NumberLiteral{Text: text, Kind: NumberDecimalInteger, Position: startPosition}, true, fmt.Errorf("%w: %s", ErrInvalidNumber, text)
		}
		return NumberLiteral{Text: text, Kind: NumberDecimalFloat, Number: numberValue, Position: startPosition}, true, nil
	}
	return NumberLiteral{Text: text, Kind: NumberDecimalInteger, Integer: integerValue, Position: startPosition}, true, nil
}

// scanHexNumber 扫描十六进制整数或浮点数字面量。
//
// startPosition 是数字起始位置；调用方已确认当前位置是 0x/0X 前缀。
func (lexer *Lexer) scanHexNumber(startPosition Position) (NumberLiteral, bool, error) {
	var builder strings.Builder
	lexer.source.Next()
	builder.WriteByte('0')
	prefixRune, _, _ := lexer.source.Next()
	builder.WriteRune(prefixRune)

	digitCount := lexer.consumeHexDigits(&builder)
	isFloat := false
	if lexer.peekRune('.') {
		// 十六进制小数点进入 hex float 形态。
		isFloat = true
		lexer.source.Next()
		builder.WriteByte('.')
		digitCount += lexer.consumeHexDigits(&builder)
	}
	if digitCount == 0 {
		// 0x 后必须至少有一个十六进制数字，整数和浮点都一样。
		return NumberLiteral{Text: builder.String(), Position: startPosition}, true, ErrInvalidNumber
	}
	if lexer.peekRune('p') || lexer.peekRune('P') {
		// p/P 指数是 Lua 十六进制浮点标志。
		isFloat = true
		lexer.source.Next()
		builder.WriteByte('p')
		lexer.consumeOptionalSign(&builder)
		if !lexer.consumeRequiredDecimalDigits(&builder) {
			// p/P 后必须至少有一位十进制指数数字。
			return NumberLiteral{Text: builder.String(), Position: startPosition}, true, ErrInvalidNumber
		}
	}

	text := builder.String()
	if isFloat {
		// Go ParseFloat 要求十六进制浮点必须带 p/P 指数；Lua 5.3 允许 `0xF0.0`
		// 省略指数，语义等价于 `0xF0.0p0`。
		parseText := text
		if !strings.ContainsAny(text, "pP") {
			// 临时补零指数只用于数值解析，NumberLiteral.Text 保留源码原文。
			parseText += "p0"
		}
		numberValue, err := strconv.ParseFloat(parseText, 64)
		if err != nil {
			return NumberLiteral{Text: text, Kind: NumberHexFloat, Position: startPosition}, true, fmt.Errorf("%w: %s", ErrInvalidNumber, text)
		}
		return NumberLiteral{Text: text, Kind: NumberHexFloat, Number: numberValue, Position: startPosition}, true, nil
	}

	unsignedIntegerValue, ok := parseHexUnsignedWrap(text[2:])
	if !ok {
		// 十六进制整数文本异常时返回数字字面量错误。
		return NumberLiteral{Text: text, Kind: NumberHexInteger, Position: startPosition}, true, fmt.Errorf("%w: %s", ErrInvalidNumber, text)
	}
	integerValue := int64(unsignedIntegerValue)
	return NumberLiteral{Text: text, Kind: NumberHexInteger, Integer: integerValue, Position: startPosition}, true, nil
}

// parseHexUnsignedWrap 按 Lua 5.3 十六进制整数规则解析无符号整数。
//
// text 不含 0x/0X 前缀；长度可以超过 64 位，累积时自然按 uint64 回绕，以匹配 Lua
// 对源码十六进制整数字面量的低位截断语义。
func parseHexUnsignedWrap(text string) (uint64, bool) {
	if text == "" {
		// 0x 后必须至少有一个十六进制数字。
		return 0, false
	}
	var value uint64
	for _, digit := range text {
		// 每个十六进制字符写入低 4 位，uint64 溢出自然丢弃高位。
		digitValue, ok := hexDigitValue(digit)
		if !ok {
			// 非十六进制字符表示该整数文本非法。
			return 0, false
		}
		value = (value << 4) | uint64(digitValue)
	}

	// 返回按 64 位截断后的无符号值。
	return value, true
}

// hexDigitValue 返回十六进制字符对应的 nibble 值。
//
// 支持 Lua 5.3 源码中的 ASCII 0-9、a-f、A-F；其他 rune 返回 ok=false。
func hexDigitValue(digit rune) (uint8, bool) {
	switch {
	case digit >= '0' && digit <= '9':
		// 数字字符直接映射到 0..9。
		return uint8(digit - '0'), true
	case digit >= 'a' && digit <= 'f':
		// 小写十六进制字符映射到 10..15。
		return uint8(digit-'a') + 10, true
	case digit >= 'A' && digit <= 'F':
		// 大写十六进制字符映射到 10..15。
		return uint8(digit-'A') + 10, true
	default:
		// 其他字符不是合法十六进制数字。
		return 0, false
	}
}

// peekIsHexPrefix 判断当前位置是否是 Lua 十六进制数字前缀。
//
// 调用方通常已经确认当前位置为 `0`；本方法只检查后一个 rune 是否是 x/X。
func (lexer *Lexer) peekIsHexPrefix() bool {
	nextRune, ok := lexer.source.PeekOffset(1)
	if !ok {
		// EOF 前只有 0，不是十六进制前缀。
		return false
	}
	if nextRune == 'x' || nextRune == 'X' {
		// 0x 或 0X 都是 Lua 十六进制数字前缀。
		return true
	}

	// 其他字符不构成十六进制前缀。
	return false
}

// peekRune 判断当前位置的 rune 是否等于 expected。
//
// EOF 或不匹配都返回 false，且不推进输入。
func (lexer *Lexer) peekRune(expected rune) bool {
	nextRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 不能匹配任何普通 rune。
		return false
	}
	if nextRune == expected {
		// 当前 rune 与期望值一致。
		return true
	}

	// 当前 rune 与期望值不一致。
	return false
}

// peekRuneOffset 判断当前位置之后 runeOffset 个 rune 是否等于 expected。
//
// runeOffset 必须大于等于 0；EOF 或不匹配都返回 false。
func (lexer *Lexer) peekRuneOffset(runeOffset int, expected rune) bool {
	nextRune, ok := lexer.source.PeekOffset(runeOffset)
	if !ok {
		// 目标位置不存在时不能匹配。
		return false
	}
	if nextRune == expected {
		// 目标位置 rune 与期望值一致。
		return true
	}

	// 目标位置 rune 与期望值不一致。
	return false
}

// peekOffsetIsDecimalDigit 判断当前位置之后 runeOffset 个 rune 是否是十进制数字。
//
// runeOffset 必须大于等于 0；EOF 或非数字都返回 false。
func (lexer *Lexer) peekOffsetIsDecimalDigit(runeOffset int) bool {
	nextRune, ok := lexer.source.PeekOffset(runeOffset)
	if !ok {
		// 目标位置不存在时不能形成数字。
		return false
	}
	if isDecimalDigit(nextRune) {
		// 目标位置是 ASCII 十进制数字。
		return true
	}

	// 目标位置不是数字。
	return false
}

// consumeOptionalSign 消费可选的正负号并写入 builder。
//
// 该 helper 用于数字指数部分；非正负号时不推进输入。
func (lexer *Lexer) consumeOptionalSign(builder *strings.Builder) {
	nextRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 没有可选符号。
		return
	}
	if nextRune == '+' || nextRune == '-' {
		// 指数符号只能是 + 或 -。
		lexer.source.Next()
		builder.WriteRune(nextRune)
	}
}

// consumeRequiredDecimalDigits 消费至少一个十进制数字。
//
// 返回 true 表示成功消费一位或多位数字；返回 false 表示没有数字可消费。
func (lexer *Lexer) consumeRequiredDecimalDigits(builder *strings.Builder) bool {
	count := 0
	for {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// EOF 结束数字序列。
			break
		}
		if !isDecimalDigit(nextRune) {
			// 非数字结束数字序列。
			break
		}
		lexer.source.Next()
		builder.WriteRune(nextRune)
		count++
	}
	if count > 0 {
		// 至少消费一个数字，满足指数语法。
		return true
	}

	// 没有消费任何数字，调用方应报告语法错误。
	return false
}

// consumeHexDigits 消费连续十六进制数字。
//
// 返回消费的数字数量；非十六进制字符会保留给后续扫描。
func (lexer *Lexer) consumeHexDigits(builder *strings.Builder) int {
	count := 0
	for {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// EOF 结束十六进制数字序列。
			return count
		}
		if !isHexDigit(nextRune) {
			// 非十六进制字符不能被本方法消费。
			return count
		}
		lexer.source.Next()
		builder.WriteRune(nextRune)
		count++
	}
}
