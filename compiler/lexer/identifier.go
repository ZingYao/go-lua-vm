package lexer

// ScanIdentifier 尝试读取当前位置的 Lua 标识符。
//
// Lua 5.3 标识符由 ASCII 字母或下划线开头，后续可包含 ASCII 字母、数字或下划线；
// 关键字分类会在后续 TODO 中单独处理。
func (lexer *Lexer) ScanIdentifier() (string, Position, bool) {
	startPosition := lexer.source.Position()
	input := lexer.source.input
	startOffset := lexer.source.offset
	if startOffset >= len(input) {
		// EOF 不能形成标识符。
		return "", startPosition, false
	}
	if !isIdentifierStartByte(input[startOffset]) {
		// 非标识符起始字符不能被消费。
		return "", startPosition, false
	}

	// 标识符限定 ASCII，直接按 byte 扫描可避免每个字符重复 UTF-8 解码。
	currentOffset := startOffset + 1
	for currentOffset < len(input) && isIdentifierPartByte(input[currentOffset]) {
		// 当前 byte 仍属于标识符，继续前进到第一个非标识符 byte。
		currentOffset++
	}
	lexer.source.offset = currentOffset
	lexer.source.column += currentOffset - startOffset

	// 返回标识符文本和起始位置。
	return input[startOffset:currentOffset], startPosition, true
}

// isIdentifierStartByte 判断 byte 是否可以作为 Lua 标识符首字符。
//
// 当前标识符语义限定 ASCII，调用方可用该函数避开 UTF-8 解码。
func isIdentifierStartByte(value byte) bool {
	switch {
	case value == '_':
		// 下划线可以作为标识符首字符。
		return true
	case value >= 'a' && value <= 'z':
		// 小写 ASCII 字母可以作为标识符首字符。
		return true
	case value >= 'A' && value <= 'Z':
		// 大写 ASCII 字母可以作为标识符首字符。
		return true
	default:
		// 其他 byte 不能作为当前阶段标识符首字符。
		return false
	}
}

// isIdentifierPartByte 判断 byte 是否可以作为 Lua 标识符非首字符。
//
// 标识符后续字符允许 ASCII 字母、数字和下划线。
func isIdentifierPartByte(value byte) bool {
	if isIdentifierStartByte(value) {
		// 首字符集合也全部允许出现在后续位置。
		return true
	}
	if value >= '0' && value <= '9' {
		// ASCII 数字可以出现在标识符非首位置。
		return true
	}

	// 其他 byte 不能作为标识符组成部分。
	return false
}

// isIdentifierStart 判断 rune 是否可以作为 Lua 标识符首字符。
//
// 当前实现按 Lua 5.3 C locale 默认行为处理，只接受 ASCII 字母和下划线。
func isIdentifierStart(value rune) bool {
	if value < 0 || value > 0x7f {
		// 非 ASCII rune 不能作为当前阶段标识符首字符。
		return false
	}

	// ASCII rune 可直接复用 byte 判定逻辑。
	return isIdentifierStartByte(byte(value))
}

// isIdentifierPart 判断 rune 是否可以作为 Lua 标识符非首字符。
//
// 标识符后续字符允许 ASCII 字母、数字和下划线。
func isIdentifierPart(value rune) bool {
	if value < 0 || value > 0x7f {
		// 非 ASCII rune 不能作为当前阶段标识符组成部分。
		return false
	}

	// ASCII rune 可直接复用 byte 判定逻辑。
	return isIdentifierPartByte(byte(value))
}
