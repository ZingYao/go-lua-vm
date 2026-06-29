package parser

// extensionStatementParser 表示一个可选语法扩展的语句解析入口。
type extensionStatementParser func(*Parser) (Statement, bool, error)

// extensionStatementParsers 保存当前构建产物编译进来的扩展语句解析器。
var extensionStatementParsers []extensionStatementParser

// registerExtensionStatementParser 注册一个扩展语句解析器。
func registerExtensionStatementParser(parser extensionStatementParser) {
	// 扩展文件在 init 阶段注册，核心 parser 不直接引用具体扩展类型。
	extensionStatementParsers = append(extensionStatementParsers, parser)
}

// parseExtensionStatement 尝试按已编译扩展解析当前语句。
func (parser *Parser) parseExtensionStatement() (Statement, bool, error) {
	for _, parse := range extensionStatementParsers {
		// 按注册顺序尝试扩展语句；未命中时继续尝试后续扩展。
		statement, matched, err := parse(parser)
		if matched || err != nil {
			// 命中或出错都交给调用方处理，避免扩展语法被普通标识符路径吞掉。
			return statement, matched, err
		}
	}

	// 没有任何扩展匹配当前 token。
	return nil, false, nil
}

// extensionSemanticAnalyzer 表示一个可选语法扩展的语义分析入口。
type extensionSemanticAnalyzer func(*semanticAnalyzer, *Block, *ScopeInfo, int, int, Statement, *functionNamespace) bool

// extensionSemanticAnalyzers 保存当前构建产物编译进来的扩展语义分析器。
var extensionSemanticAnalyzers []extensionSemanticAnalyzer

// registerExtensionSemanticAnalyzer 注册一个扩展语义分析器。
func registerExtensionSemanticAnalyzer(analyzer extensionSemanticAnalyzer) {
	// 扩展文件在 init 阶段注册，核心 semantic 不直接引用具体扩展类型。
	extensionSemanticAnalyzers = append(extensionSemanticAnalyzers, analyzer)
}

// analyzeExtensionStatement 尝试按已编译扩展分析当前语句。
func (analyzer *semanticAnalyzer) analyzeExtensionStatement(block *Block, scope *ScopeInfo, depth int, statementIndex int, statement Statement, namespace *functionNamespace) bool {
	for _, analyze := range extensionSemanticAnalyzers {
		// 扩展分析器自行判断语句类型，命中时返回 true。
		if analyze(analyzer, block, scope, depth, statementIndex, statement, namespace) {
			return true
		}
	}

	// 没有任何扩展处理当前语句。
	return false
}
