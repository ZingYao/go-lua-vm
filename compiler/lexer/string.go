package lexer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	// ErrUnterminatedLongString 表示长字符串或长注释没有找到匹配的关闭长括号。
	ErrUnterminatedLongString = errors.New("unterminated long string")
	// ErrUnterminatedShortString 表示短字符串没有找到匹配的引号。
	ErrUnterminatedShortString = errors.New("unterminated short string")
	// ErrInvalidEscape 表示短字符串中出现非法转义序列。
	ErrInvalidEscape = errors.New("invalid escape sequence")
)

// ScanLongString 尝试读取当前位置的 Lua 长字符串。
//
// 当前位置必须位于 `[=*[` 开始分隔符；返回 ok=false 表示当前位置不是长字符串，且不会推进输入。
func (lexer *Lexer) ScanLongString() (string, Position, bool, error) {
	startPosition := lexer.source.Position()
	level, ok := lexer.consumeLongOpenBracket()
	if !ok {
		// 当前不是长括号开头，调用方应继续尝试其他 token。
		lexer.source.Reset(startPosition)
		return "", startPosition, false, nil
	}

	text, err := lexer.readLongContent(level)
	if err != nil {
		// 长字符串已经开始但未关闭时，必须返回错误，不回退输入以便错误位置可定位到 EOF。
		return "", startPosition, true, err
	}

	// 返回去掉长括号分隔符后的原始内容。
	return text, startPosition, true, nil
}

// ScanShortString 尝试读取当前位置的 Lua 单引号或双引号短字符串。
//
// 返回 ok=false 表示当前位置不是短字符串；支持 Lua 5.3 常见转义、十进制转义、十六进制转义、
// Unicode 转义和 `\z` 空白吞噬。
func (lexer *Lexer) ScanShortString() (string, Position, bool, error) {
	startPosition := lexer.source.Position()
	quoteRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 不能形成短字符串。
		return "", startPosition, false, nil
	}
	if quoteRune != '\'' && quoteRune != '"' {
		// 只有单引号或双引号可以开启短字符串。
		return "", startPosition, false, nil
	}

	// 消费开头引号，后续读取直到遇到同类闭合引号。
	lexer.source.Next()
	var builder strings.Builder
	for {
		nextRune, nextPosition, ok := lexer.source.Next()
		if !ok {
			// 短字符串到 EOF 仍未闭合，报告未终止错误。
			return "", startPosition, true, ErrUnterminatedShortString
		}
		if nextRune == quoteRune {
			// 遇到匹配引号时字符串结束。
			return builder.String(), startPosition, true, nil
		}
		if nextRune == '\n' || nextRune == '\r' {
			// Lua 短字符串不能包含未转义换行。
			return "", startPosition, true, ErrUnterminatedShortString
		}
		if nextRune == '\\' {
			// 反斜杠后进入 Lua 5.3 转义序列解析。
			escapedText, err := lexer.readEscapeSequence()
			if err != nil {
				// 转义序列非法时，保留错误语义给调用方构造词法错误。
				return "", startPosition, true, err
			}
			builder.WriteString(escapedText)
			continue
		}

		// 普通字符原样写入短字符串内容，非法 UTF-8 源字节按 Lua 字节字符串语义保留。
		lexer.writeSourceRune(&builder, nextRune, nextPosition)
	}
}

// skipLongComment 跳过 Lua `--[=*[...]=*]` 长注释。
//
// 返回 true 表示当前位置确实消费了长注释；长注释内容不会作为 token 输出。
func (lexer *Lexer) skipLongComment() bool {
	startPosition := lexer.source.Mark()
	firstRune, ok := lexer.source.PeekOffset(0)
	if !ok {
		// EOF 不能形成长注释。
		return false
	}
	secondRune, ok := lexer.source.PeekOffset(1)
	if !ok {
		// 只有一个字符时，不可能匹配 `--` 长注释前缀。
		return false
	}
	if firstRune != '-' || secondRune != '-' {
		// 长注释必须以 `--` 开始。
		return false
	}

	// 消费 `--` 前缀后再尝试长括号开头。
	lexer.source.Next()
	lexer.source.Next()
	level, ok := lexer.consumeLongOpenBracket()
	if !ok {
		// `--` 后不是长括号，回退给短注释逻辑处理。
		lexer.source.Reset(startPosition)
		return false
	}
	_, err := lexer.readLongContent(level)
	if err != nil {
		// 未闭合长注释当前按消费到 EOF 处理，后续完整 token 错误会报告该问题。
		return true
	}

	// 长注释完整闭合，已消费到关闭分隔符之后。
	return true
}

// consumeLongOpenBracket 消费 Lua 长括号开头 `[=*[`。
//
// 返回 level 表示等号数量；当前位置不是合法长括号时返回 ok=false 并保持调用方可自行回退。
func (lexer *Lexer) consumeLongOpenBracket() (int, bool) {
	firstRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 不能形成长括号。
		return 0, false
	}
	if firstRune != '[' {
		// 长括号必须以 `[` 开头。
		return 0, false
	}

	// 消费开头 `[` 后统计连续等号数量。
	lexer.source.Next()
	level := 0
	for {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// 输入结束前没有第二个 `[`，不是合法长括号。
			return 0, false
		}
		if nextRune != '=' {
			// 等号序列结束，继续检查闭合的第二个 `[`。
			break
		}
		lexer.source.Next()
		level++
	}

	nextRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 前缺少第二个 `[`，不是合法长括号。
		return 0, false
	}
	if nextRune != '[' {
		// `[=` 后不是 `[` 时，不构成长字符串分隔符。
		return 0, false
	}
	lexer.source.Next()

	// 返回等号数量，后续关闭分隔符必须使用相同数量。
	return level, true
}

// readLongContent 读取长括号内容直到匹配的关闭分隔符。
//
// level 是打开长括号中的等号数量；返回内容不包含打开和关闭分隔符。
func (lexer *Lexer) readLongContent(level int) (string, error) {
	lexer.skipInitialLongStringNewline()
	var builder strings.Builder
	for {
		nextRune, nextPosition, ok := lexer.source.Next()
		if !ok {
			// 长括号内容到 EOF 仍未关闭，返回未终止错误。
			return "", ErrUnterminatedLongString
		}
		if nextRune == ']' && lexer.consumeLongCloseAfterFirstBracket(level) {
			// 已消费完整关闭分隔符，长字符串内容结束。
			return builder.String(), nil
		}

		// 普通内容原样保留，包含换行和任意非分隔符字符；非法 UTF-8 字节必须保持单字节。
		lexer.writeSourceRune(&builder, nextRune, nextPosition)
	}
}

// writeSourceRune 把 Source 读取到的字符写入 Lua 字符串字面量 builder。
//
// Lua 5.3 字符串是任意字节序列；当 Source 因非法 UTF-8 单字节返回 RuneError 时，本方法写回
// 原始字节，避免把 0x80..0xff 膨胀成 UTF-8 编码的 RuneError。
func (lexer *Lexer) writeSourceRune(builder *strings.Builder, value rune, position Position) {
	if value == utf8.RuneError && position.Offset < len(lexer.source.input) {
		// DecodeRuneInString 对非法 UTF-8 单字节返回 RuneError 且宽度为 1；合法 RuneError 是三字节。
		_, width := utf8.DecodeRuneInString(lexer.source.input[position.Offset:])
		if width == 1 && !utf8.ValidString(lexer.source.input[position.Offset:position.Offset+1]) {
			// 保留 Lua 源码中的原始单字节高位字符。
			builder.WriteByte(lexer.source.input[position.Offset])
			return
		}
	}

	// 合法 UTF-8 rune 和普通 ASCII 按原有文本语义写入。
	builder.WriteRune(value)
}

// skipInitialLongStringNewline 跳过长字符串开头后紧跟的首个换行。
//
// Lua 5.3 会忽略长字符串或长注释分隔符后的第一个换行，便于源码排版。
func (lexer *Lexer) skipInitialLongStringNewline() {
	nextRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 没有可跳过的初始换行。
		return
	}
	if nextRune == '\n' {
		// Unix 换行直接消费一个字符。
		lexer.source.Next()
		return
	}
	if nextRune == '\r' {
		// Windows CRLF 或单独 CR 都作为首个换行跳过。
		lexer.source.Next()
		nextRune, ok = lexer.source.Peek()
		if ok && nextRune == '\n' {
			// CRLF 中的 LF 是同一个换行的一部分，也需要一起消费。
			lexer.source.Next()
		}
	}
}

// consumeLongCloseAfterFirstBracket 尝试消费长括号关闭分隔符的剩余部分。
//
// 调用方已经消费第一个 `]`；若后续等号数量和第二个 `]` 不匹配，会把输入回退到第一个 `]` 后。
func (lexer *Lexer) consumeLongCloseAfterFirstBracket(level int) bool {
	mark := lexer.source.Mark()
	for equalIndex := 0; equalIndex < level; equalIndex++ {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// EOF 前关闭分隔符不完整，回退后让上层继续按普通内容处理或报 EOF。
			lexer.source.Reset(mark)
			return false
		}
		if nextRune != '=' {
			// 等号数量不足，不是匹配的关闭分隔符。
			lexer.source.Reset(mark)
			return false
		}
		lexer.source.Next()
	}
	nextRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 前缺少最后一个 `]`，不是完整关闭分隔符。
		lexer.source.Reset(mark)
		return false
	}
	if nextRune != ']' {
		// 等号数量后面不是 `]`，不是关闭分隔符。
		lexer.source.Reset(mark)
		return false
	}
	lexer.source.Next()

	// 成功消费关闭分隔符。
	return true
}

// readEscapeSequence 读取短字符串反斜杠后的 Lua 5.3 转义序列。
//
// 调用方已经消费反斜杠；返回值是转义后的实际字符串片段。
func (lexer *Lexer) readEscapeSequence() (string, error) {
	escapeRune, _, ok := lexer.source.Next()
	if !ok {
		// 反斜杠后到达 EOF，属于未终止短字符串。
		return "", ErrUnterminatedShortString
	}
	switch escapeRune {
	case 'a':
		// `\a` 表示响铃字符。
		return "\a", nil
	case 'b':
		// `\b` 表示退格字符。
		return "\b", nil
	case 'f':
		// `\f` 表示换页字符。
		return "\f", nil
	case 'n':
		// `\n` 表示换行字符。
		return "\n", nil
	case 'r':
		// `\r` 表示回车字符。
		return "\r", nil
	case 't':
		// `\t` 表示水平制表符。
		return "\t", nil
	case 'v':
		// `\v` 表示垂直制表符。
		return "\v", nil
	case '\\', '"', '\'':
		// 反斜杠和两种引号可通过反斜杠转义为自身。
		return string(escapeRune), nil
	case '\n', '\r':
		// 反斜杠后接真实换行时，Lua 会把它归一为换行字符。
		return "\n", nil
	case 'z':
		// `\z` 会吞掉后续所有 Lua 空白，用于源码排版。
		lexer.skipWhitespace()
		return "", nil
	case 'x':
		// `\xXX` 读取两个十六进制数字并写入对应字节。
		return lexer.readHexEscape()
	case 'u':
		// `\u{XXX}` 读取 Unicode code point 并写入 UTF-8。
		return lexer.readUnicodeEscape()
	default:
		// 十进制转义以数字开头，最多读取三位。
		if isDecimalDigit(escapeRune) {
			return lexer.readDecimalEscape(escapeRune)
		}
		// 其他转义在 Lua 5.3 中非法。
		return "", fmt.Errorf("%w: \\%c", ErrInvalidEscape, escapeRune)
	}
}

// readDecimalEscape 读取 Lua 短字符串十进制转义。
//
// firstDigit 是已经读取到的首位数字；最多再读取两位，结果必须在单字节范围内。
func (lexer *Lexer) readDecimalEscape(firstDigit rune) (string, error) {
	digits := []rune{firstDigit}
	for len(digits) < 3 {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// EOF 结束十进制转义，使用已经读取的数字。
			break
		}
		if !isDecimalDigit(nextRune) {
			// 非数字结束十进制转义，保留给后续字符串扫描。
			break
		}
		lexer.source.Next()
		digits = append(digits, nextRune)
	}

	value, err := strconv.Atoi(string(digits))
	if err != nil {
		// 理论上 digits 已保证为数字，若失败说明内部转换异常。
		return "", fmt.Errorf("%w: decimal %q", ErrInvalidEscape, string(digits))
	}
	if value > 255 {
		// Lua 5.3 十进制转义必须落在单字节范围内。
		return "", fmt.Errorf("%w: decimal %q", ErrInvalidEscape, string(digits))
	}

	// 返回单字节字符串，保持 Lua string 字节语义。
	return string([]byte{byte(value)}), nil
}

// readHexEscape 读取 Lua `\xXX` 十六进制转义。
//
// 必须恰好读取两个十六进制数字，返回对应单字节字符串。
func (lexer *Lexer) readHexEscape() (string, error) {
	hexDigits := make([]rune, 0, 2)
	for digitIndex := 0; digitIndex < 2; digitIndex++ {
		nextRune, _, ok := lexer.source.Next()
		if !ok {
			// EOF 前不足两位十六进制数字，属于非法转义。
			return "", fmt.Errorf("%w: short hex", ErrInvalidEscape)
		}
		if !isHexDigit(nextRune) {
			// 非十六进制字符不能出现在 `\x` 转义中。
			return "", fmt.Errorf("%w: hex %q", ErrInvalidEscape, string(nextRune))
		}
		hexDigits = append(hexDigits, nextRune)
	}

	value, err := strconv.ParseUint(string(hexDigits), 16, 8)
	if err != nil {
		// ParseUint 失败说明内部十六进制校验存在漏洞。
		return "", fmt.Errorf("%w: hex %q", ErrInvalidEscape, string(hexDigits))
	}

	// 返回解析后的单字节内容。
	return string([]byte{byte(value)}), nil
}

// readUnicodeEscape 读取 Lua `\u{XXX}` Unicode 转义。
//
// 大括号内必须包含至少一个十六进制数字，解析结果必须是合法 Unicode code point。
func (lexer *Lexer) readUnicodeEscape() (string, error) {
	openRune, _, ok := lexer.source.Next()
	if !ok {
		// EOF 前缺少 `{`，属于非法 Unicode 转义。
		return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
	}
	if openRune != '{' {
		// Lua 5.3 Unicode 转义必须使用 `\u{...}` 形式。
		return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
	}

	var digits []rune
	for {
		nextRune, _, ok := lexer.source.Next()
		if !ok {
			// EOF 前缺少 `}`，属于非法 Unicode 转义。
			return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
		}
		if nextRune == '}' {
			// 右大括号结束 Unicode 转义。
			break
		}
		if !isHexDigit(nextRune) {
			// 大括号中只能出现十六进制数字。
			return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
		}
		digits = append(digits, nextRune)
	}
	if len(digits) == 0 {
		// 空 Unicode 转义没有 code point。
		return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
	}

	value, err := strconv.ParseInt(string(digits), 16, 32)
	if err != nil {
		// 超出 32 位范围或格式错误都视作非法转义。
		return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
	}
	if value < 0 || value > 0x10FFFF {
		// Unicode code point 不能超过 U+10FFFF。
		return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
	}
	if value >= 0xD800 && value <= 0xDFFF {
		// UTF-16 surrogate 区间不是合法 Unicode scalar value。
		return "", fmt.Errorf("%w: unicode", ErrInvalidEscape)
	}

	// Go string 会把 rune 编码为 UTF-8，符合 Lua 5.3 `\u{}` 转义输出。
	return string(rune(value)), nil
}

// isDecimalDigit 判断 rune 是否是 ASCII 十进制数字。
//
// Lua 数字和转义解析只接受 ASCII 数字，不接受其他 Unicode 数字。
func isDecimalDigit(value rune) bool {
	switch {
	case value >= '0' && value <= '9':
		// ASCII 0-9 是合法十进制数字。
		return true
	default:
		// 其他 rune 不参与十进制解析。
		return false
	}
}

// isHexDigit 判断 rune 是否是 ASCII 十六进制数字。
//
// Lua 5.3 十六进制转义支持 0-9、a-f 和 A-F。
func isHexDigit(value rune) bool {
	switch {
	case value >= '0' && value <= '9':
		// ASCII 数字是十六进制数字。
		return true
	case value >= 'a' && value <= 'f':
		// 小写 a-f 是十六进制数字。
		return true
	case value >= 'A' && value <= 'F':
		// 大写 A-F 是十六进制数字。
		return true
	default:
		// 其他 rune 不是十六进制数字。
		return false
	}
}
