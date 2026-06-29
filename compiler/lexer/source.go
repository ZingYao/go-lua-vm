// Package lexer 提供 Lua 5.3 词法分析器。
//
// 本包当前先承载输入流、UTF-8 读取、位置跟踪和注释/空白跳过能力；完整 token
// 识别会在后续任务中逐步接入。
package lexer

import "unicode/utf8"

const (
	// eofRune 表示输入流已经到达末尾。
	eofRune rune = -1
	// utf8BOM 表示 Lua 5.3 文件加载路径允许忽略的 UTF-8 BOM。
	utf8BOM = "\xef\xbb\xbf"
)

// Position 表示 Lua 源码中的字节位置。
//
// Line 和 Column 从 1 开始，Offset 从 0 开始；Offset 按 UTF-8 原始字节计数，
// 以便后续错误消息能同时定位源码行列与字节偏移。
type Position struct {
	// Line 表示当前源码行号，从 1 开始。
	Line int
	// Column 表示当前源码列号，从 1 开始，按 rune 读取步数计数。
	Column int
	// Offset 表示当前源码字节偏移，从 0 开始。
	Offset int
}

// Source 提供带位置跟踪的 Lua 源码读取流。
//
// Source 保存原始 UTF-8 字符串并按 rune 读取；Lua 字符串语义仍按字节处理，词法层
// 保存 Offset 用于后续需要按字节切片的路径。
type Source struct {
	// input 保存完整源码文本。
	input string
	// offset 保存下一次读取的字节偏移。
	offset int
	// line 保存下一次读取位置所在行号。
	line int
	// column 保存下一次读取位置所在列号。
	column int
}

// NewSource 创建新的源码读取流。
//
// input 必须是 Go string；非法 UTF-8 字节会按 utf8.RuneError 读取，并以单字节步长前进，
// 以保留 Lua 源码允许任意字节进入字符串字面量的后续扩展空间。
func NewSource(input string) *Source {
	// 初始位置对齐常见编辑器行列显示，行列都从 1 开始。
	source := &Source{input: input, line: 1, column: 1}
	if len(input) >= len(utf8BOM) && input[:len(utf8BOM)] == utf8BOM {
		// Lua 5.3 会忽略 chunk 开头的 UTF-8 BOM；行列仍保持源码第一行第一列。
		source.offset = len(utf8BOM)
	}
	return source
}

// Position 返回下一次读取所在的位置。
//
// 返回值是快照，调用方修改不会影响 Source 内部状态。
func (source *Source) Position() Position {
	// 直接复制当前位置字段，避免暴露内部可变状态。
	return Position{Line: source.line, Column: source.column, Offset: source.offset}
}

// Peek 查看下一个 rune 但不推进输入流。
//
// 返回 ok=false 表示已经到达 EOF；非法 UTF-8 字节会返回 utf8.RuneError 和 ok=true。
func (source *Source) Peek() (rune, bool) {
	// EOF 时没有可读取 rune，调用方必须按 ok=false 处理。
	if source.offset >= len(source.input) {
		return eofRune, false
	}

	// 使用 utf8.DecodeRuneInString 保留 Go 标准 UTF-8 解码语义。
	nextRune, _ := utf8.DecodeRuneInString(source.input[source.offset:])
	return nextRune, true
}

// PeekOffset 查看距离当前位置 runeOffset 个 rune 的字符。
//
// runeOffset 必须大于等于 0；返回 ok=false 表示目标位置超过 EOF。
func (source *Source) PeekOffset(runeOffset int) (rune, bool) {
	// 负数偏移没有明确词法语义，按不可读取处理。
	if runeOffset < 0 {
		return eofRune, false
	}

	byteOffset := source.offset
	for remaining := runeOffset; remaining > 0; remaining-- {
		// 中途到达 EOF 时，目标 rune 不存在。
		if byteOffset >= len(source.input) {
			return eofRune, false
		}
		_, width := utf8.DecodeRuneInString(source.input[byteOffset:])
		byteOffset += width
	}
	if byteOffset >= len(source.input) {
		// 目标位置正好落在 EOF，也视为不可读取。
		return eofRune, false
	}

	// 解码目标位置 rune，调用方不获得新的位置状态。
	nextRune, _ := utf8.DecodeRuneInString(source.input[byteOffset:])
	return nextRune, true
}

// Next 读取下一个 rune 并推进输入流。
//
// 返回的 Position 是该 rune 读取前的位置；返回 ok=false 表示 EOF，Position 为 EOF 所在位置。
func (source *Source) Next() (rune, Position, bool) {
	position := source.Position()
	if source.offset >= len(source.input) {
		// EOF 不推进位置，便于调用方重复探测 EOF。
		return eofRune, position, false
	}

	nextRune, width := utf8.DecodeRuneInString(source.input[source.offset:])
	source.offset += width
	if nextRune == '\r' {
		// Lua 5.3 把 CR、CRLF 和 LFCR 都归一为单个换行；混合双字符换行只推进一行。
		if source.offset < len(source.input) && source.input[source.offset] == '\n' {
			// CRLF 的 LF 是同一个换行的一部分，必须一起消费但不能额外增加行号。
			source.offset++
		}
		source.line++
		source.column = 1
		return '\n', position, true
	}
	if nextRune == '\n' {
		// LFCR 也是同一个换行序列，Lua 5.3 会把它当作单个换行处理。
		if source.offset < len(source.input) && source.input[source.offset] == '\r' {
			// LFCR 的 CR 是同一个换行的一部分，必须一起消费但不能额外增加行号。
			source.offset++
		}
		// Lua 5.3 把 LF 换行推进到下一行。
		source.line++
		source.column = 1
	} else {
		// 普通字符只推进列号，Offset 已按 UTF-8 字节宽度更新。
		source.column++
	}

	// 返回读取到的 rune 和读取前的位置。
	return nextRune, position, true
}

// Mark 返回当前输入流的可恢复位置标记。
//
// 标记只供 lexer 内部在尝试匹配长括号、字符串等结构失败时回退；调用方不得跨 Source 使用。
func (source *Source) Mark() Position {
	// 当前 Source 的位置三元组足够恢复读取状态。
	return source.Position()
}

// Reset 回退 Source 到 Mark 返回的位置。
//
// mark 必须来自同一个 Source；本方法不校验来源，调用方负责只在局部试探匹配中使用。
func (source *Source) Reset(mark Position) {
	// 直接恢复 offset、line 和 column，保证后续读取与打标时一致。
	source.offset = mark.Offset
	source.line = mark.Line
	source.column = mark.Column
}
