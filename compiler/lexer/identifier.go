package lexer

// ScanIdentifier 尝试读取当前位置的 Lua 标识符。
//
// Lua 5.3 标识符由 ASCII 字母或下划线开头，后续可包含 ASCII 字母、数字或下划线；
// 关键字分类会在后续 TODO 中单独处理。
func (lexer *Lexer) ScanIdentifier() (string, Position, bool) {
	startPosition := lexer.source.Position()
	firstRune, ok := lexer.source.Peek()
	if !ok {
		// EOF 不能形成标识符。
		return "", startPosition, false
	}
	if !isIdentifierStart(firstRune) {
		// 非标识符起始字符不能被消费。
		return "", startPosition, false
	}

	// 首字符合法后，读取完整标识符；标识符限定 ASCII，可在消费后直接切原始源码文本。
	for {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// EOF 结束标识符。
			break
		}
		if !isIdentifierPart(nextRune) {
			// 非标识符组成字符留给后续 token 扫描。
			break
		}
		lexer.source.Next()
	}

	// 返回标识符文本和起始位置。
	return lexer.source.input[startPosition.Offset:lexer.source.Position().Offset], startPosition, true
}

// isIdentifierStart 判断 rune 是否可以作为 Lua 标识符首字符。
//
// 当前实现按 Lua 5.3 C locale 默认行为处理，只接受 ASCII 字母和下划线。
func isIdentifierStart(value rune) bool {
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
		// 其他字符不能作为当前阶段标识符首字符。
		return false
	}
}

// isIdentifierPart 判断 rune 是否可以作为 Lua 标识符非首字符。
//
// 标识符后续字符允许 ASCII 字母、数字和下划线。
func isIdentifierPart(value rune) bool {
	if isIdentifierStart(value) {
		// 首字符集合也全部允许出现在后续位置。
		return true
	}
	if isDecimalDigit(value) {
		// ASCII 数字可以出现在标识符非首位置。
		return true
	}

	// 其他字符不能作为标识符组成部分。
	return false
}
