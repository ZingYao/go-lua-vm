//go:build !lua53 && (with_switch || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package parser

import (
	"fmt"
	"math"
	"strconv"

	"github.com/ZingYao/go-lua-vm/compiler/lexer"
	"github.com/ZingYao/go-lua-vm/extensions"
)

func init() {
	// 注册 switch 扩展语句解析与语义检查入口。
	registerExtensionStatementParser(parseSwitchExtensionStatement)
	registerExtensionSemanticAnalyzer(analyzeSwitchExtensionStatement)
}

// parseSwitchExtensionStatement 尝试解析 switch 扩展语句。
func parseSwitchExtensionStatement(parser *Parser) (Statement, bool, error) {
	if !parser.syntax.Has(extensions.SyntaxSwitch) || !parser.isContextKeyword("switch") {
		// 未启用 switch 或当前 token 不是 switch 时交回核心 parser。
		return nil, false, nil
	}
	statement, err := parser.parseSwitchStatement()
	return statement, true, err
}

// parseSwitchStatement 解析扩展 switch/case/default 语句。
//
// case/default 作为 switch 内的上下文关键字解析，不进入 lexer 全局保留字表，避免破坏普通变量名。
func (parser *Parser) parseSwitchStatement() (Statement, error) {
	startPosition := parser.current.Position
	if err := parser.expectContextKeyword("switch"); err != nil {
		// 调用方已经判断 switch，该错误只作为防御。
		return nil, err
	}
	switchExpression, err := parser.parseExpression()
	if err != nil {
		// switch 后必须有主表达式。
		return nil, err
	}
	if err := parser.expectKeyword("do"); err != nil {
		// switch 主表达式后必须跟 do。
		return nil, err
	}

	var cases []SwitchCase
	var defaultBlock *Block
	var defaultPosition lexer.Position
	for !(parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "end") {
		if parser.isContextKeyword("case") {
			// case 分支按顺序解析，允许多个匹配表达式。
			if defaultBlock != nil {
				// 第一阶段要求 default 位于最后，避免 default 后 case 的可读性和跳转语义歧义。
				return nil, parser.errorf(parser.current, "case after default")
			}
			switchCase, err := parser.parseSwitchCase()
			if err != nil {
				// 任一 case 解析失败都会终止整个 switch。
				return nil, err
			}
			cases = append(cases, switchCase)
			continue
		}
		if parser.isContextKeyword("default") {
			// default 分支最多出现一次，且必须位于最后。
			if defaultBlock != nil {
				// 重复 default 会让兜底路径不唯一，直接报错。
				return nil, parser.errorf(parser.current, "duplicate default")
			}
			defaultPosition = parser.current.Position
			parser.advance()
			defaultBlock, err = parser.parseBlockUntil(parser.isSwitchBlockEnd)
			if err != nil {
				// default block 内语句解析失败时返回错误。
				return nil, err
			}
			continue
		}
		if parser.current.Kind == lexer.TokenEOF {
			// switch 必须由 end 关闭，EOF 表示缺少结束关键字。
			return nil, parser.errorf(parser.current, "expected keyword %q", "end")
		}
		return nil, parser.errorf(parser.current, "expected case, default or end")
	}
	endPosition := parser.current.Position
	if err := parser.expectKeyword("end"); err != nil {
		// switch 语句必须以 end 结束。
		return nil, err
	}

	// 返回完整 switch 语句节点。
	return &SwitchStatement{Expression: switchExpression, Cases: cases, DefaultBlock: defaultBlock, Position: startPosition, DefaultPosition: defaultPosition, EndPosition: endPosition}, nil
}

// parseSwitchCase 解析 switch 内的单个 case 分支。
//
// case 后至少包含一个表达式，多个表达式用逗号分隔；表达式列表后的 block 解析到下一 case/default/end。
func (parser *Parser) parseSwitchCase() (SwitchCase, error) {
	position := parser.current.Position
	parser.advance()
	values, err := parser.parseExpressionList()
	if err != nil {
		// case 后必须至少包含一个匹配表达式。
		return SwitchCase{}, err
	}
	body, err := parser.parseBlockUntil(parser.isSwitchBlockEnd)
	if err != nil {
		// case body 内语句解析失败时返回错误。
		return SwitchCase{}, err
	}

	// 返回 case 分支节点。
	return SwitchCase{Values: values, Body: body, Position: position}, nil
}

// analyzeSwitchExtensionStatement 分析 switch 扩展语句。
func analyzeSwitchExtensionStatement(analyzer *semanticAnalyzer, _ *Block, scope *ScopeInfo, depth int, statementIndex int, statement Statement, namespace *functionNamespace) bool {
	typedStatement, ok := statement.(*SwitchStatement)
	if !ok {
		// 当前语句不是 switch 扩展节点。
		return false
	}
	analyzer.analyzeSwitchStatement(scope, depth, statementIndex, typedStatement, namespace)
	return true
}

// analyzeSwitchStatement 分析扩展 switch/case/default 的子 block。
//
// switch 不改变循环深度；因此 loop 内 switch 的 continue 仍绑定外层最近循环，函数内 switch 外
// continue 仍会被拒绝。
func (analyzer *semanticAnalyzer) analyzeSwitchStatement(parent *ScopeInfo, depth int, parentStatementIndex int, statement *SwitchStatement, namespace *functionNamespace) {
	analyzer.validateSwitchCaseValues(statement)
	for caseIndex := range statement.Cases {
		// 每个 case 分支都创建独立子作用域，避免 case 内 local 泄漏到后续分支。
		analyzer.analyzeBlock(statement.Cases[caseIndex].Body, parent, parentStatementIndex, depth+1, nil, false, namespace)
	}
	if statement.DefaultBlock != nil {
		// default 分支存在时同样创建独立子作用域。
		analyzer.analyzeBlock(statement.DefaultBlock, parent, parentStatementIndex, depth+1, nil, false, namespace)
	}
}

// validateSwitchCaseValues 校验同一个 switch 内可静态判断的重复 case 值。
func (analyzer *semanticAnalyzer) validateSwitchCaseValues(statement *SwitchStatement) {
	seen := make(map[string]lexer.Position)
	for caseIndex := range statement.Cases {
		switchCase := statement.Cases[caseIndex]
		for valueIndex := range switchCase.Values {
			valueExpression := switchCase.Values[valueIndex]
			key, ok := switchCaseStaticValueKey(valueExpression)
			if !ok {
				// 非字面量表达式无法在编译期可靠判断是否重复，避免误报。
				continue
			}
			if firstPosition, exists := seen[key]; exists {
				// 同一个 switch 内重复 case 值会让后续分支永远无法匹配，直接报语义错误。
				analyzer.addError(valueExpression.Pos(), fmt.Sprintf("duplicate switch case value; first declared at %d:%d", firstPosition.Line, firstPosition.Column))
				continue
			}
			seen[key] = valueExpression.Pos()
		}
	}
}

// switchCaseStaticValueKey 返回可静态比较的 case 字面量 key。
func switchCaseStaticValueKey(expression Expression) (string, bool) {
	literal, ok := expression.(*LiteralExpression)
	if !ok {
		// 目前只判断简单字面量，避免把运行期表达式错误折叠。
		return "", false
	}
	switch literal.Kind {
	case lexer.TokenNumber:
		// Lua 5.3 中 integer 与可精确表示的同值 float 比较相等，因此优先归一到整数 key。
		switch literal.Number.Kind {
		case lexer.NumberDecimalInteger, lexer.NumberHexInteger:
			return "number:int:" + strconv.FormatInt(literal.Number.Integer, 10), true
		case lexer.NumberDecimalFloat, lexer.NumberHexFloat:
			if math.Trunc(literal.Number.Number) == literal.Number.Number && literal.Number.Number >= -9223372036854775808 && literal.Number.Number <= 9223372036854775807 {
				return "number:int:" + strconv.FormatInt(int64(literal.Number.Number), 10), true
			}
			return "number:float:" + strconv.FormatFloat(literal.Number.Number, 'g', -1, 64), true
		default:
			return "number:text:" + literal.Value, true
		}
	case lexer.TokenString:
		// 字符串使用解码后的值，保证不同引号形式的同值字符串也能判重。
		return "string:" + literal.Value, true
	case lexer.TokenKeyword:
		if literal.Value == "nil" || literal.Value == "true" || literal.Value == "false" {
			// 只折叠基础布尔/nil 字面量。
			return "keyword:" + literal.Value, true
		}
	}
	return "", false
}
