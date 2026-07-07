// Package formatter 提供 Lua 源码的保守格式化能力。
package formatter

import (
	"strings"

	"github.com/ZingYao/go-lua-vm/compiler/lexer"
	"github.com/ZingYao/go-lua-vm/compiler/parser"
	"github.com/ZingYao/go-lua-vm/extensions"
)

const indentText = "  "

type blockKind string

const (
	blockNormal blockKind = "normal"
	blockRepeat blockKind = "repeat"
	blockSwitch blockKind = "switch"
	blockCase   blockKind = "case"
)

type blockFrame struct {
	kind blockKind
}

// Format 使用指定语法扩展集合格式化 Lua 源码字符串。
//
// source 必须是完整 chunk；syntax 控制 switch/continue 等项目扩展是否可用。返回值是格式化后
// 源码；语法错误或基础语义错误会原样返回。格式化过程只规范缩进和常见 token 空格，不重排表达式。
func Format(source string, syntax extensions.SyntaxSet) (string, error) {
	// 先用正式 parser 校验源码，确保 formatter 与 glua 执行语法边界一致。
	if _, err := parser.NewWithSyntax(source, syntax).ParseChunk(); err != nil {
		return "", err
	}

	lines := strings.Split(source, "\n")
	frames := make([]blockFrame, 0, 8)
	formattedLines := make([]string, 0, len(lines))
	for lineIndex, rawLine := range lines {
		if lineIndex == len(lines)-1 && rawLine == "" && strings.HasSuffix(source, "\n") {
			// Split 会把尾随换行转成最后一个空元素，这里跳过并在末尾统一补回换行。
			continue
		}
		line := strings.TrimSuffix(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			// 空行保留为空行，不继承或输出多余空白。
			formattedLines = append(formattedLines, "")
			continue
		}

		code, comment := splitLineComment(trimmed)
		code = strings.TrimSpace(code)
		classification := classifyLine(code)
		frames = adjustBeforeLine(frames, classification)

		formattedCode := formatCodeLine(code)
		formattedLine := strings.Repeat(indentText, len(frames)) + joinCodeAndComment(formattedCode, comment)
		formattedLines = append(formattedLines, formattedLine)
		frames = adjustAfterLine(frames, classification)
	}

	result := strings.Join(formattedLines, "\n")
	if strings.HasSuffix(source, "\n") && !strings.HasSuffix(result, "\n") {
		// 输入带尾随换行时输出也保持尾随换行，避免格式化造成 POSIX 文本文件噪音。
		return result + "\n", nil
	}
	return result, nil
}

type lineKind int

const (
	lineOther lineKind = iota
	lineEnd
	lineUntil
	lineElse
	lineElseIf
	lineCase
	lineDefault
	lineSwitch
	lineRepeat
	lineOpens
)

// splitLineComment 在忽略字符串内容的前提下拆分短注释。
//
// line 是单行源码；返回值分别是注释前代码和注释文本。字符串中的 `--` 不会被识别为注释。
func splitLineComment(line string) (string, string) {
	// 逐 rune 扫描当前行，使用 quote/escaped 维护短字符串状态。
	quote := rune(0)
	escaped := false
	for index, value := range line {
		if quote != 0 {
			if escaped {
				// 上一个字符是反斜杠，本字符无论是什么都只作为字符串内容。
				escaped = false
				continue
			}
			if value == '\\' {
				// 反斜杠转义会影响下一个字符是否结束字符串。
				escaped = true
				continue
			}
			if value == quote {
				// 命中同类引号时结束当前短字符串状态。
				quote = 0
			}
			continue
		}
		if value == '\'' || value == '"' {
			// 进入短字符串状态，后续注释标记需要忽略。
			quote = value
			continue
		}
		if value == '-' && index+1 < len(line) && line[index+1] == '-' {
			// 非字符串位置的 -- 开启短注释。
			return line[:index], line[index:]
		}
	}
	return line, ""
}

// classifyLine 识别单行源码的 block 布局标记。
//
// code 是去掉注释和首尾空白后的源码；返回值用于缩进栈调整。
func classifyLine(code string) lineKind {
	// 空代码行不影响缩进栈。
	if code == "" {
		return lineOther
	}
	tokens := lineTokens(code)
	if len(tokens) == 0 {
		// 无法提取 token 时按普通行处理，避免 formatter 误改结构。
		return lineOther
	}
	first := tokens[0]
	switch first {
	case "end":
		return lineEnd
	case "until":
		return lineUntil
	case "else":
		return lineElse
	case "elseif":
		return lineElseIf
	case "case":
		return lineCase
	case "default":
		return lineDefault
	case "switch":
		return lineSwitch
	case "repeat":
		return lineRepeat
	}
	if lineOpensBlock(tokens) {
		// 其他包含 then/do/function 的行按普通 block 开启处理。
		return lineOpens
	}
	return lineOther
}

// lineTokens 返回单行源码的 token 文本列表。
//
// code 必须是不含短注释的单行源码；遇到非法 token 时返回已扫描 token，供 formatter 保守处理。
func lineTokens(code string) []string {
	// 使用项目 lexer 识别 token，避免手写关键字切分与 parser 漂移。
	tokenLexer := lexer.New(code)
	tokens := make([]string, 0, 8)
	for {
		token := tokenLexer.NextToken()
		if token.Kind == lexer.TokenEOF || token.Kind == lexer.TokenIllegal {
			// EOF 或非法 token 都终止当前行扫描。
			return tokens
		}
		tokens = append(tokens, token.Text)
	}
}

// lineOpensBlock 判断当前行是否开启嵌套语句块。
//
// tokens 是单行 token 文本；返回 true 表示后续行应增加一级缩进。
func lineOpensBlock(tokens []string) bool {
	// 从左到右查找 Lua block 关键 token。
	for index, token := range tokens {
		switch token {
		case "then":
			// if/elseif 条件以 then 开启 body。
			return true
		case "function":
			// function 语句或表达式都会引入后续 body 缩进。
			return true
		case "do":
			if index > 0 && tokens[0] != "switch" {
				// while/for/switch 之外的 do 块需要增加普通缩进。
				return true
			}
			if len(tokens) == 1 {
				// 独立 do 语句开启显式 block。
				return true
			}
		}
	}
	return false
}

// adjustBeforeLine 根据闭合或同级 block 标记调整当前行之前的缩进栈。
//
// frames 是当前缩进栈；kind 是当前行类型。返回值是当前行应使用的缩进栈。
func adjustBeforeLine(frames []blockFrame, kind lineKind) []blockFrame {
	// 先处理会影响当前行缩进的 end/until/else/case/default。
	switch kind {
	case lineEnd:
		frames = popKind(frames, blockCase)
		return popOne(frames)
	case lineUntil:
		return popUntil(frames, blockRepeat)
	case lineElse, lineElseIf:
		return popOne(frames)
	case lineCase, lineDefault:
		frames = popKind(frames, blockCase)
		return frames
	default:
		// 普通行不在输出前改变缩进。
		return frames
	}
}

// adjustAfterLine 记录当前行打开的后续 block。
//
// frames 是当前行输出后的缩进栈；kind 是当前行类型。返回值用于下一行缩进。
func adjustAfterLine(frames []blockFrame, kind lineKind) []blockFrame {
	// 根据当前行类型把下一行需要进入的 block 压栈。
	switch kind {
	case lineSwitch:
		return append(frames, blockFrame{kind: blockSwitch})
	case lineCase, lineDefault:
		return append(frames, blockFrame{kind: blockCase})
	case lineRepeat:
		return append(frames, blockFrame{kind: blockRepeat})
	case lineElse, lineElseIf:
		return append(frames, blockFrame{kind: blockNormal})
	case lineOpens:
		return append(frames, blockFrame{kind: blockNormal})
	default:
		// 普通行不改变后续缩进。
		return frames
	}
}

// popOne 弹出最内层 block。
//
// frames 是当前缩进栈；空栈时原样返回。
func popOne(frames []blockFrame) []blockFrame {
	// 空栈说明源码结构由 parser 保证但 formatter 仍做防御处理。
	if len(frames) == 0 {
		return frames
	}
	return frames[:len(frames)-1]
}

// popKind 在最内层 block 类型匹配时弹出该 block。
//
// frames 是当前缩进栈；kind 是期望弹出的 block 类型。
func popKind(frames []blockFrame, kind blockKind) []blockFrame {
	// 只有栈顶类型匹配时才弹出，避免误删外层结构。
	if len(frames) == 0 || frames[len(frames)-1].kind != kind {
		return frames
	}
	return frames[:len(frames)-1]
}

// popUntil 从栈顶开始弹出，直到并包含第一个匹配类型的 block。
//
// frames 是当前缩进栈；kind 是用于停止弹出的 block 类型。
func popUntil(frames []blockFrame, kind blockKind) []blockFrame {
	// repeat-until 可能包含内层 case 等辅助帧，因此按类型向外查找。
	for len(frames) > 0 {
		last := frames[len(frames)-1]
		frames = frames[:len(frames)-1]
		if last.kind == kind {
			// 找到目标 block 后停止弹出。
			return frames
		}
	}
	return frames
}

// formatCodeLine 规范化单行源码 token 之间的空格。
//
// code 是不含短注释的单行源码。返回值保留原始 token 拼写，尤其是字符串字面量引号与转义。
func formatCodeLine(code string) string {
	// 空代码行直接返回空字符串。
	if code == "" {
		return ""
	}
	tokenLexer := lexer.New(code)
	tokens := make([]lexer.Token, 0, 8)
	for {
		token := tokenLexer.NextToken()
		if token.Kind == lexer.TokenEOF || token.Kind == lexer.TokenIllegal {
			// EOF 或非法 token 都终止当前行扫描。
			break
		}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		// 无 token 时只做首尾空白裁剪。
		return strings.TrimSpace(code)
	}

	var builder strings.Builder
	var previous lexer.Token
	for index, token := range tokens {
		if builder.Len() > 0 && needsSpace(previous, token) {
			// 相邻 token 需要分隔时插入单个空格。
			builder.WriteByte(' ')
		}
		builder.WriteString(rawTokenText(code, tokens, index))
		previous = token
	}
	return builder.String()
}

// rawTokenText 恢复单个 token 的原始源码拼写。
//
// code 是当前行源码；tokens 是该行 token 列表；index 指向需要恢复的 token。
func rawTokenText(code string, tokens []lexer.Token, index int) string {
	// 使用当前 token 起点和下一个 token 起点切片，保留字符串字面量原始引号。
	start := tokens[index].Position.Offset
	end := len(code)
	if index+1 < len(tokens) {
		end = tokens[index+1].Position.Offset
	}
	if start < 0 || start > len(code) || end < start || end > len(code) {
		// offset 异常时回退到 lexer 文本，保证错误路径不 panic。
		return tokens[index].Text
	}
	return strings.TrimSpace(code[start:end])
}

// needsSpace 判断相邻两个 token 是否需要插入空格。
//
// previous 和 current 必须来自同一行 token 序列。
func needsSpace(previous lexer.Token, current lexer.Token) bool {
	// 首个 token 前不插入空格。
	if previous.Text == "" {
		return false
	}
	if isSpacingPunctuation(previous.Text) || isSpacingPunctuation(current.Text) {
		// 括号、索引、逗号、点号等标点按文本规则处理，避免 lexer kind 差异导致 a[1] 被拆开。
		return operatorNeedsSpace(previous, current)
	}
	if current.Kind == lexer.TokenOperator || previous.Kind == lexer.TokenOperator {
		// 任一 token 是操作符时交给操作符规则判断。
		return operatorNeedsSpace(previous, current)
	}
	return true
}

// isSpacingPunctuation 判断 token 文本是否需要按标点空格规则处理。
//
// formatter 只依赖文本判断这些标点，避免 lexer 将括号或逗号归入非 operator kind 时产生多余空格。
func isSpacingPunctuation(text string) bool {
	switch text {
	case "(", ")", "[", "]", "{", "}", ",", ";", ":", ".":
		// 这些标点都有专门的前后空格规则。
		return true
	default:
		// 其他 token 继续按 kind 或默认规则处理。
		return false
	}
}

// operatorNeedsSpace 处理标点和中缀操作符的空格规则。
//
// previous 和 current 至少有一个是操作符 token。
func operatorNeedsSpace(previous lexer.Token, current lexer.Token) bool {
	// 标点前后空格规则使用小表表达，保持 formatter 行为可读。
	noSpaceBefore := map[string]bool{
		")": true, "[": true, "]": true, "}": true, ",": true, ";": true, ":": true, ".": true,
	}
	noSpaceAfter := map[string]bool{
		"(": true, "[": true, "{": true, ".": true, ":": true,
	}
	if noSpaceBefore[current.Text] || noSpaceAfter[previous.Text] {
		// 右括号、逗号、点号等标点附近不加空格。
		return false
	}
	if current.Text == "(" && (previous.Kind == lexer.TokenIdentifier || previous.Kind == lexer.TokenKeyword) {
		// 函数调用或关键字后的括号不插入空格。
		return false
	}
	if current.Text == "{" {
		// table constructor 前保留一个空格，便于和赋值/return 等分隔。
		return true
	}
	if previous.Text == "#" {
		// 长度操作符为前缀操作符，后续表达式直接黏接，不增加空格。
		return false
	}
	if previous.Text == "," || previous.Text == ";" {
		// 逗号和分号后统一保留一个空格。
		return true
	}
	return true
}

// joinCodeAndComment 合并格式化后的代码和保留的短注释。
//
// code 是格式化后的代码部分；comment 是原始短注释文本。
func joinCodeAndComment(code string, comment string) string {
	// 注释自身只裁剪首尾空白，不改写注释内容。
	comment = strings.TrimSpace(comment)
	if code == "" {
		// 纯注释行直接返回注释。
		return comment
	}
	if comment == "" {
		// 没有注释时直接返回代码。
		return code
	}
	return code + " " + comment
}
