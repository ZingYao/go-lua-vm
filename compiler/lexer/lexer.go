package lexer

import "strings"

// Lexer 表示 Lua 5.3 词法分析器状态。
//
// 当前阶段实现输入推进、空白跳过、注释跳过和字符串扫描；后续 token 识别会复用同一个 Source。
type Lexer struct {
	// source 保存带行列信息的输入流。
	source *Source
}

// New 创建新的 Lua 词法分析器。
//
// input 是完整 Lua 源码文本；调用方可以通过 ReadRune、PeekRune 和 SkipIgnored 逐步消费。
func New(input string) *Lexer {
	// 初始化 Source，后续 token 扫描共享同一位置状态。
	return &Lexer{source: NewSource(input)}
}

// StripInitialShebang 跳过 Lua 文件加载允许的首行 shebang。
//
// 只有输入第一个字节是 `#` 时才启用；返回值保留一个换行占位，确保后续源码行号仍从
// 原始文件第二行开始。该 helper 只应由文件加载路径调用；load(string) 必须保留首字节 `#`。
func StripInitialShebang(input string) string {
	// UTF-8 BOM 可出现在文件开头；Lua 5.3 会忽略 BOM 后继续识别首行 shebang。
	prefix := ""
	checkInput := input
	if strings.HasPrefix(input, utf8BOM) {
		// 保留 BOM 交给 Source 统一跳过，只在判断 shebang 时越过它。
		prefix = utf8BOM
		checkInput = input[len(utf8BOM):]
	}
	// 空输入或非 shebang 输入不做任何改写。
	if checkInput == "" || checkInput[0] != '#' {
		// 普通源码必须保持原样，避免影响 `#` 长度操作符。
		return input
	}
	newlineIndex := strings.IndexAny(checkInput, "\r\n")
	if newlineIndex < 0 {
		// 只有 shebang 且没有后续源码时，返回空行占位。
		return prefix + "\n"
	}
	if checkInput[newlineIndex] == '\r' && newlineIndex+1 < len(checkInput) && checkInput[newlineIndex+1] == '\n' {
		// CRLF shebang 保留一个 LF 占位，并跳过整个 CRLF。
		return prefix + "\n" + checkInput[newlineIndex+2:]
	}
	// LF 或单独 CR shebang 保留一个 LF 占位，并跳过换行符本身。
	return prefix + "\n" + checkInput[newlineIndex+1:]
}

// Position 返回下一次读取所在源码位置。
//
// 返回值用于错误报告和测试断言，不会暴露内部可变状态。
func (lexer *Lexer) Position() Position {
	// 委托 Source 返回位置快照。
	return lexer.source.Position()
}

// PeekRune 查看下一个 rune 但不推进 lexer。
//
// 返回 ok=false 表示已经到达 EOF。
func (lexer *Lexer) PeekRune() (rune, bool) {
	// 读取预览必须保持位置不变。
	return lexer.source.Peek()
}

// ReadNextRune 读取一个 rune 并推进 lexer。
//
// 返回的 Position 是 rune 读取前的位置；返回 ok=false 表示 EOF。
func (lexer *Lexer) ReadNextRune() (rune, Position, bool) {
	// 所有实际推进都经过 Source，以统一维护行列和字节偏移。
	return lexer.source.Next()
}

// SkipIgnored 跳过 Lua 源码中当前可忽略的空白和短注释。
//
// 本方法目前支持空白字符、换行、`--` 短注释和 `--[=*[...]=*]` 长注释。
func (lexer *Lexer) SkipIgnored() {
	for {
		// 优先跳过空白，避免空白后的短注释被漏掉。
		if lexer.skipWhitespace() {
			continue
		}
		if lexer.skipLongComment() {
			// 长注释可能跨越多行，结束后继续跳过后续空白。
			continue
		}
		if lexer.skipShortComment() {
			// 短注释结束后可能紧跟换行或下一段空白，需要继续循环。
			continue
		}

		// 当前字符不再属于可忽略内容，交还给后续 token 扫描。
		return
	}
}

// skipWhitespace 跳过连续空白字符。
//
// 返回 true 表示至少消费了一个空白字符；空白集合按 Lua 5.3 lisspace 语义覆盖常见 ASCII 空白。
func (lexer *Lexer) skipWhitespace() bool {
	consumed := false
	for {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// EOF 没有空白可消费，返回本轮是否曾消费过字符。
			return consumed
		}
		if !isLuaWhitespace(nextRune) {
			// 非空白字符不能被本方法消费，保留给注释或 token 扫描。
			return consumed
		}

		// 当前 rune 是空白，读取并丢弃。
		lexer.source.Next()
		consumed = true
	}
}

// skipShortComment 跳过 Lua `--` 开头的短注释。
//
// 返回 true 表示当前位置确实消费了短注释；长注释由 skipLongComment 在本方法之前处理。
func (lexer *Lexer) skipShortComment() bool {
	firstRune, ok := lexer.source.PeekOffset(0)
	if !ok {
		// EOF 不能形成短注释。
		return false
	}
	secondRune, ok := lexer.source.PeekOffset(1)
	if !ok {
		// 只有一个 `-` 时不是短注释，必须交给操作符 token 扫描。
		return false
	}
	if firstRune != '-' || secondRune != '-' {
		// Lua 短注释必须以两个连续减号开头。
		return false
	}

	// 消费两个注释前缀减号。
	lexer.source.Next()
	lexer.source.Next()
	for {
		nextRune, ok := lexer.source.Peek()
		if !ok {
			// 注释可以延伸到 EOF，消费完成后返回成功。
			return true
		}
		if nextRune == '\n' || nextRune == '\r' {
			// 短注释不消费换行，换行交给空白跳过路径统一更新行列。
			return true
		}

		// 普通注释内容逐字符丢弃。
		lexer.source.Next()
	}
}

// isLuaWhitespace 判断 rune 是否属于 Lua 5.3 ASCII 空白集合。
//
// Lua 词法空白只依赖 ASCII 控制字符；非 ASCII 空格当前不作为源码空白处理。
func isLuaWhitespace(value rune) bool {
	switch value {
	case ' ', '\f', '\n', '\r', '\t', '\v':
		// Lua 5.3 lisspace 覆盖空格、换页、换行、回车、制表和垂直制表。
		return true
	default:
		// 其他 rune 不是 Lua 词法空白。
		return false
	}
}
