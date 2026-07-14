package parser

import "github.com/ZingYao/go-lua-vm/extensions"

func init() {
	// 注册 continue 扩展语句解析与语义检查入口。
	registerExtensionStatementParser(parseContinueExtensionStatement)
	registerExtensionSemanticAnalyzer(analyzeContinueExtensionStatement)
}

// parseContinueExtensionStatement 尝试解析 continue 扩展语句。
func parseContinueExtensionStatement(parser *Parser) (Statement, bool, error) {
	if !parser.syntax.Has(extensions.SyntaxContinue) || !parser.isContextKeyword("continue") {
		// 未启用 continue 或当前 token 不是 continue 时交回核心 parser。
		return nil, false, nil
	}
	statement, err := parser.parseContinueStatement()
	return statement, true, err
}

// parseContinueStatement 解析 continue 语句。
//
// 当前阶段只构造 AST，是否位于循环内部由语义阶段统一检查。
func (parser *Parser) parseContinueStatement() (Statement, error) {
	position := parser.current.Position
	if err := parser.expectContextKeyword("continue"); err != nil {
		// 调用方已经判断 continue，该错误只作为防御。
		return nil, err
	}

	// 返回 continue 语句节点。
	return &ContinueStatement{Position: position}, nil
}

// analyzeContinueExtensionStatement 分析 continue 扩展语句。
func analyzeContinueExtensionStatement(analyzer *semanticAnalyzer, _ *Block, _ *ScopeInfo, _ int, _ int, statement Statement, _ *functionNamespace) bool {
	typedStatement, ok := statement.(*ContinueStatement)
	if !ok {
		// 当前语句不是 continue 扩展节点。
		return false
	}
	if analyzer.loopDepth == 0 {
		// continue 只能续迭代最近一层循环；循环外没有合法目标。
		analyzer.addError(typedStatement.Position, "continue outside loop")
	}
	return true
}
