package parser

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zing/go-lua-vm/compiler/lexer"
	"github.com/zing/go-lua-vm/extensions"
)

const (
	// maxSyntaxCLevels 对齐 Lua 5.3 LUAI_MAXCCALLS，用于限制 parser 递归语法层级。
	maxSyntaxCLevels = 200
)

// Parser 表示 Lua 5.3 递归下降语法分析器。
//
// Parser 持有 lexer 和单 token 前瞻；当前阶段只实现最小语句子集。
type Parser struct {
	// input 保存完整源码文本，用于 load 错误格式化时恢复 near token 的原始字面量。
	input string
	// lexer 保存底层词法分析器。
	lexer *lexer.Lexer
	// current 保存当前前瞻 token。
	current lexer.Token
	// syntax 保存当前 parser 启用的扩展语法集合。
	syntax extensions.SyntaxSet
	// syntaxDepth 保存当前 parser 递归层级，用于模拟 Lua 5.3 的 C 调用深度限制。
	syntaxDepth int
}

// New 创建新的 Parser。
//
// input 是完整 Lua 源码文本；Parser 会立即读取第一个 token 作为前瞻。
func New(input string) *Parser {
	// 默认 parser 使用当前构建产物启用的语法扩展集合。
	return NewWithSyntax(input, extensions.Default())
}

// NewWithSyntax 创建带指定语法扩展集合的 Parser。
//
// input 是完整 Lua 源码文本；syntax 会裁剪到当前构建产物已编译的扩展集合，未编译扩展不会生效。
// Parser 会立即读取第一个 token 作为前瞻。
func NewWithSyntax(input string, syntax extensions.SyntaxSet) *Parser {
	// 初始化 lexer 并读取首个 token，后续 parse 方法只消费 current。
	tokenLexer := lexer.New(input)
	return &Parser{input: input, lexer: tokenLexer, current: tokenLexer.NextToken(), syntax: syntax & extensions.Compiled()}
}

// ParseChunk 解析完整 Lua chunk。
//
// 当前阶段 chunk 只允许由已支持的语句组成，并要求最终到达 EOF。
func (parser *Parser) ParseChunk() (*Chunk, error) {
	// chunk 起始位置使用当前 token 位置，空文件时即 EOF 位置。
	startPosition := parser.current.Position
	block, err := parser.ParseBlock()
	if err != nil {
		// block 解析失败时直接返回错误，调用方不应继续使用部分 AST。
		return nil, err
	}
	if parser.current.Kind != lexer.TokenEOF {
		// chunk 必须消费到 EOF，防止静默忽略尾部非法语法。
		return nil, parser.errorf(parser.current, "expected EOF")
	}
	chunk := &Chunk{Block: block, Position: startPosition}
	analyzer := &semanticAnalyzer{}
	if err := analyzer.analyzeChunk(chunk); err != nil {
		// 语法解析成功后执行作用域与 goto/label 校验，失败时返回聚合错误。
		return nil, err
	}

	// 返回包含顶层 block 的 chunk。
	return chunk, nil
}

// ParseBlock 解析 Lua block。
//
// 当前阶段 block 持续读取语句直到 EOF 或后续控制流结束关键字。
func (parser *Parser) ParseBlock() (*Block, error) {
	// 普通 Lua block 使用标准 end/else/elseif/until/EOF 作为终止条件。
	return parser.parseBlockUntil(nil)
}

// parseBlockUntil 解析 Lua block，并允许调用方提供额外终止条件。
//
// extraEnd 为 nil 时只使用标准 block 结束 token；switch case/default 这类上下文边界会通过
// extraEnd 注入，避免把 case/default 变成全局保留字。
func (parser *Parser) parseBlockUntil(extraEnd func(lexer.Token) bool) (*Block, error) {
	if err := parser.enterSyntaxLevel(parser.current); err != nil {
		// block 嵌套超过 Lua 5.3 parser 限制时直接返回兼容错误。
		return nil, err
	}
	defer parser.leaveSyntaxLevel()

	// block 起始位置使用当前 token 位置。
	block := &Block{Position: parser.current.Position}
	for !parser.isBlockEndToken(parser.current, extraEnd) {
		if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "return" {
			// return 是 block 的终结语句，解析后停止收集普通语句。
			returnStatement, err := parser.parseReturnStatementUntil(extraEnd)
			if err != nil {
				// return 解析失败时终止 block 解析。
				return nil, err
			}
			block.Return = returnStatement
			break
		}
		statement, err := parser.parseStatement()
		if err != nil {
			// 任一语句解析失败都会终止 block 解析。
			return nil, err
		}
		block.Statements = append(block.Statements, statement)
	}

	// 返回按源码顺序收集的语句列表。
	return block, nil
}

// parseStatement 解析当前 token 开始的一条语句。
//
// 当前阶段支持空语句、函数语句、控制流、local 赋值和普通名称赋值。
func (parser *Parser) parseStatement() (Statement, error) {
	if parser.current.Kind == lexer.TokenIllegal {
		// 非法 token 不能进入语法树，直接返回词法错误。
		return nil, parser.illegalTokenError(parser.current)
	}
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == ";" {
		// 分号表示空语句。
		return parser.parseEmptyStatement()
	}
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "::" {
		// 双冒号开启 Lua label 语句。
		return parser.parseLabelStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "do" {
		// do 关键字开启显式词法 block。
		return parser.parseDoStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "local" {
		// local 关键字可能开启 local function 或局部变量声明赋值。
		return parser.parseLocalAssignmentStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "break" {
		// break 关键字开启循环跳出语句。
		return parser.parseBreakStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "goto" {
		// goto 关键字开启标签跳转语句。
		return parser.parseGotoStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "for" {
		// for 关键字可能开启 numeric for 或 generic for。
		return parser.parseForStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "function" {
		// function 关键字开启全局/普通函数定义语句。
		return parser.parseFunctionStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "if" {
		// if 关键字开启条件分支语句。
		return parser.parseIfStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "while" {
		// while 关键字开启前置条件循环语句。
		return parser.parseWhileStatement()
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "repeat" {
		// repeat 关键字开启后置条件循环语句。
		return parser.parseRepeatUntilStatement()
	}
	if statement, matched, err := parser.parseExtensionStatement(); matched || err != nil {
		// 已编译进当前产物的扩展语句在普通标识符语句前解析。
		return statement, err
	}
	if parser.current.Kind == lexer.TokenIdentifier {
		// 普通标识符开头可能是赋值语句，也可能是函数调用语句。
		return parser.parseAssignmentOrCallStatement()
	}
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "(" {
		// 括号开头的 prefix expression 只能作为函数调用语句，例如 `(f or g)(...)`。
		return parser.parsePrefixCallStatement()
	}

	// 其他语句类型尚未实现，返回明确错误。
	return nil, parser.errorf(parser.current, "unsupported statement")
}

// parseEmptyStatement 解析空语句。
//
// 当前 token 必须是 `;`，解析后消费该 token。
func (parser *Parser) parseEmptyStatement() (Statement, error) {
	position := parser.current.Position
	if err := parser.expectOperator(";"); err != nil {
		// 调用方已经判断过分号，该错误只作为防御。
		return nil, err
	}

	// 返回空语句节点。
	return &EmptyStatement{Position: position}, nil
}

// parseDoStatement 解析 do-end 显式 block 语句。
//
// do 语句会创建一个子 block，直到匹配的 end 关键字结束。
func (parser *Parser) parseDoStatement() (Statement, error) {
	position := parser.current.Position
	if err := parser.expectKeyword("do"); err != nil {
		// 调用方已经判断 do，该错误只作为防御。
		return nil, err
	}
	body, err := parser.ParseBlock()
	if err != nil {
		// do body 内语句解析失败时返回错误。
		return nil, err
	}
	if err := parser.expectKeyword("end"); err != nil {
		// do 语句必须以 end 关闭。
		return nil, err
	}

	// 返回 do 语句节点。
	return &DoStatement{Body: body, Position: position}, nil
}

// parseAssignmentStatement 解析普通赋值语句。
//
// 当前阶段左侧支持名称和点号字段访问列表，右侧支持基础表达式列表。
func (parser *Parser) parseAssignmentStatement() (Statement, error) {
	startPosition := parser.current.Position
	leftExpressions, err := parser.parseNameExpressionList()
	if err != nil {
		// 左侧变量列表解析失败时无法构造赋值语句。
		return nil, err
	}
	if err := parser.expectOperator("="); err != nil {
		// 普通赋值必须包含 `=`。
		return nil, err
	}
	rightExpressions, err := parser.parseExpressionList()
	if err != nil {
		// 右侧表达式列表解析失败时无法构造赋值语句。
		return nil, err
	}

	// 返回普通赋值语句节点。
	return &AssignmentStatement{Left: leftExpressions, Right: rightExpressions, Position: startPosition}, nil
}

// parseAssignmentOrCallStatement 解析标识符开头的赋值或调用语句。
//
// Lua 语句层允许 varlist `=` explist 或 functioncall；当前阶段 varlist 支持名称和点号字段访问。
func (parser *Parser) parseAssignmentOrCallStatement() (Statement, error) {
	startPosition := parser.current.Position
	firstExpression, err := parser.parsePostfixExpression()
	if err != nil {
		// 标识符开头必须先能解析为前缀表达式。
		return nil, err
	}
	if parser.isCallExpression(firstExpression) {
		// 函数调用或方法调用可以独立作为语句，返回值会在运行期被丢弃。
		return &FunctionCallStatement{Call: firstExpression, Position: startPosition}, nil
	}
	if !parser.isAssignmentTarget(firstExpression) {
		// 非调用表达式也不是合法左值时，当前阶段不能作为赋值左侧。
		return nil, parser.errorf(parser.current, "expected assignment or function call")
	}
	leftExpressions := []Expression{firstExpression}
	for parser.current.Kind == lexer.TokenOperator && parser.current.Text == "," {
		if err := parser.checkSyntaxListLimit(len(leftExpressions) + 1); err != nil {
			// 过长 varlist 按 Lua 5.3 parser 递归限制返回 too many C levels。
			return nil, err
		}
		// 普通赋值左侧允许多个变量，逗号后必须继续跟合法左值表达式。
		parser.advance()
		nextExpression, err := parser.parsePostfixExpression()
		if err != nil {
			// 逗号后缺少可解析表达式时无法构造赋值左侧列表。
			return nil, err
		}
		if !parser.isAssignmentTarget(nextExpression) {
			// 逗号后的表达式必须仍然是名称或字段访问左值。
			return nil, parser.errorf(parser.current, "expected assignment target")
		}
		leftExpressions = append(leftExpressions, nextExpression)
	}
	if err := parser.expectOperator("="); err != nil {
		// 非调用语句必须是赋值语句，因此名称列表后必须跟等号。
		return nil, err
	}
	rightExpressions, err := parser.parseExpressionList()
	if err != nil {
		// 赋值右侧表达式列表解析失败时返回错误。
		return nil, err
	}

	// 返回普通赋值语句节点。
	return &AssignmentStatement{Left: leftExpressions, Right: rightExpressions, Position: startPosition}, nil
}

// parsePrefixCallStatement 解析括号开头的函数调用语句。
//
// Lua 允许 `(f or g)(...)` 作为语句；该入口只接受最终为普通或方法调用的表达式，其他
// prefix expression 仍按不支持语句返回错误。
func (parser *Parser) parsePrefixCallStatement() (Statement, error) {
	startPosition := parser.current.Position
	callExpression, err := parser.parsePostfixExpression()
	if err != nil {
		// prefix expression 解析失败时直接返回。
		return nil, err
	}
	if !parser.isCallExpression(callExpression) {
		// 非调用 prefix expression 不能单独构成语句。
		return nil, parser.errorf(parser.current, "expected function call statement")
	}

	// 返回调用语句，调用结果在运行期丢弃。
	return &FunctionCallStatement{Call: callExpression, Position: startPosition}, nil
}

// parseLocalAssignmentStatement 解析 local 赋值语句。
//
// 当前阶段支持 `local a, b` 和 `local a, b = expr, expr` 两种形式。
func (parser *Parser) parseLocalAssignmentStatement() (Statement, error) {
	startPosition := parser.current.Position
	if err := parser.expectKeyword("local"); err != nil {
		// 调用方已经判断 local，该错误只作为防御。
		return nil, err
	}
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "function" {
		// local function 语法先声明局部函数名，再解析函数体。
		parser.advance()
		name, err := parser.expectIdentifier()
		if err != nil {
			// local function 后必须有函数名。
			return nil, err
		}
		body, err := parser.parseFunctionBody()
		if err != nil {
			// 函数体解析失败时无法构造 local function。
			return nil, err
		}
		body.LineDefined = startPosition.Line
		return &LocalFunctionStatement{Name: name, Body: body, Position: startPosition}, nil
	}
	names, err := parser.parseNameList()
	if err != nil {
		// local 后必须存在至少一个变量名。
		return nil, err
	}
	if len(names) > maxFunctionLocals {
		// 过多局部变量应优先于后续缺失 end 等语法错误报告。
		return nil, parser.tooManyLocalVariablesError(startPosition)
	}

	var values []Expression
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "=" {
		// local 声明带初始化列表时，消费等号并解析表达式列表。
		parser.advance()
		values, err = parser.parseExpressionList()
		if err != nil {
			// 初始化表达式列表解析失败时返回错误。
			return nil, err
		}
	}

	// 返回 local 赋值语句节点。
	return &LocalAssignmentStatement{Names: names, Values: values, Position: startPosition}, nil
}

// parseFunctionStatement 解析普通 function 语句。
//
// 支持 `function name (...)`、`function table.field (...)` 和 `function table:method (...)`。
// 点号/冒号形式在 AST 中降为普通字段赋值，冒号方法会向函数体参数列表前补入 `self`。
func (parser *Parser) parseFunctionStatement() (Statement, error) {
	startPosition := parser.current.Position
	if err := parser.expectKeyword("function"); err != nil {
		// 调用方已经判断 function，该错误只作为防御。
		return nil, err
	}
	targetExpression, isMethod, err := parser.parseFunctionName()
	if err != nil {
		// function 后必须有合法函数名。
		return nil, err
	}
	body, err := parser.parseFunctionBody()
	if err != nil {
		// 函数体解析失败时无法构造 function 语句。
		return nil, err
	}
	body.LineDefined = startPosition.Line
	if isMethod {
		// 冒号定义等价于函数体首个形参为 self。
		body.Params = append([]string{"self"}, body.Params...)
	}
	if nameExpression, ok := targetExpression.(*NameExpression); ok && !isMethod {
		// 简单函数名保留原有 AST，兼容既有 codegen 路径。
		return &FunctionStatement{Name: nameExpression.Name, Body: body, Position: startPosition}, nil
	}

	// 字段或方法函数定义等价于一次赋值语句。
	return &AssignmentStatement{
		Left:     []Expression{targetExpression},
		Right:    []Expression{&FunctionExpression{Body: body, Position: startPosition}},
		Position: startPosition,
	}, nil
}

// parseFunctionName 解析 function 语句中的函数名。
//
// 名称必须以 identifier 开始；点号字段可重复；冒号方法只能出现在末尾。返回表达式可作为
// 赋值左值使用，isMethod 表示调用方需要向函数体注入 self 参数。
func (parser *Parser) parseFunctionName() (Expression, bool, error) {
	nameToken := parser.current
	name, err := parser.expectIdentifier()
	if err != nil {
		// function 后必须以 identifier 开始。
		return nil, false, err
	}
	var targetExpression Expression = &NameExpression{Name: name, Position: nameToken.Position}
	for parser.current.Kind == lexer.TokenOperator && parser.current.Text == "." {
		// 点号字段定义会继续构造字段左值链。
		parser.advance()
		fieldName, err := parser.expectIdentifier()
		if err != nil {
			// 点号后必须跟字段名。
			return nil, false, err
		}
		targetExpression = &FieldAccessExpression{Receiver: targetExpression, Field: fieldName, Position: targetExpression.Pos()}
	}
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == ":" {
		// 冒号方法定义只允许作为函数名最后一段。
		parser.advance()
		methodName, err := parser.expectIdentifier()
		if err != nil {
			// 冒号后必须跟方法名。
			return nil, false, err
		}
		return &FieldAccessExpression{Receiver: targetExpression, Field: methodName, Position: targetExpression.Pos()}, true, nil
	}

	// 未出现冒号时是普通函数或字段函数定义。
	return targetExpression, false, nil
}

// parseIfStatement 解析 if/elseif/else 语句。
//
// 当前阶段条件表达式使用基础表达式集合，block 按已支持语句递归解析。
func (parser *Parser) parseIfStatement() (Statement, error) {
	startPosition := parser.current.Position
	ifClause, err := parser.parseIfClause("if")
	if err != nil {
		// if 主分支解析失败时无法构造 if 语句。
		return nil, err
	}
	clauses := []IfClause{ifClause}
	for parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "elseif" {
		// elseif 可以出现多次，每个分支都有条件和 then block。
		nextClause, err := parser.parseIfClause("elseif")
		if err != nil {
			// 任一 elseif 分支解析失败都会终止整个 if。
			return nil, err
		}
		clauses = append(clauses, nextClause)
	}

	var elseBlock *Block
	var elsePosition lexer.Position
	if parser.current.Kind == lexer.TokenKeyword && parser.current.Text == "else" {
		// else 分支没有条件，消费 else 后解析到 end。
		elsePosition = parser.current.Position
		parser.advance()
		elseBlock, err = parser.ParseBlock()
		if err != nil {
			// else block 内语句解析失败时返回错误。
			return nil, err
		}
	}
	endPosition := parser.current.Position
	if err := parser.expectKeyword("end"); err != nil {
		// if 语句必须以 end 结束。
		return nil, err
	}

	// 返回完整 if 语句节点。
	return &IfStatement{Clauses: clauses, ElseBlock: elseBlock, Position: startPosition, ElsePosition: elsePosition, EndPosition: endPosition}, nil
}

// parseIfClause 解析 if 或 elseif 的单个条件分支。
//
// keyword 必须是 `if` 或 `elseif`；调用方负责按 Lua 语法顺序调用。
func (parser *Parser) parseIfClause(keyword string) (IfClause, error) {
	position := parser.current.Position
	if err := parser.expectKeyword(keyword); err != nil {
		// 当前 token 必须匹配指定分支关键字。
		return IfClause{}, err
	}
	condition, err := parser.parseExpression()
	if err != nil {
		// if/elseif 后必须有条件表达式。
		return IfClause{}, err
	}
	thenPosition := parser.current.Position
	if err := parser.expectKeyword("then"); err != nil {
		// 条件表达式后必须跟 then。
		return IfClause{}, err
	}
	block, err := parser.ParseBlock()
	if err != nil {
		// then block 内语句解析失败时返回错误。
		return IfClause{}, err
	}

	// 返回条件分支。
	return IfClause{Condition: condition, Block: block, Position: position, ThenPosition: thenPosition}, nil
}

// parseWhileStatement 解析 while 循环语句。
//
// 语法为 `while exp do block end`。
func (parser *Parser) parseWhileStatement() (Statement, error) {
	startPosition := parser.current.Position
	if err := parser.expectKeyword("while"); err != nil {
		// 调用方已经判断 while，该错误只作为防御。
		return nil, err
	}
	condition, err := parser.parseExpression()
	if err != nil {
		// while 后必须有条件表达式。
		return nil, err
	}
	doPosition := parser.current.Position
	if err := parser.expectKeyword("do"); err != nil {
		// while 条件后必须跟 do。
		return nil, err
	}
	body, err := parser.ParseBlock()
	if err != nil {
		// while block 内语句解析失败时返回错误。
		return nil, err
	}
	endPosition := parser.current.Position
	if err := parser.expectKeyword("end"); err != nil {
		// while 语句必须以 end 结束。
		return nil, err
	}

	// 返回 while 语句节点。
	return &WhileStatement{Condition: condition, Body: body, Position: startPosition, DoPosition: doPosition, EndPosition: endPosition}, nil
}

// parseRepeatUntilStatement 解析 repeat-until 循环语句。
//
// 语法为 `repeat block until exp`。
func (parser *Parser) parseRepeatUntilStatement() (Statement, error) {
	startPosition := parser.current.Position
	if err := parser.expectKeyword("repeat"); err != nil {
		// 调用方已经判断 repeat，该错误只作为防御。
		return nil, err
	}
	body, err := parser.ParseBlock()
	if err != nil {
		// repeat block 内语句解析失败时返回错误。
		return nil, err
	}
	untilPosition := parser.current.Position
	if err := parser.expectKeyword("until"); err != nil {
		// repeat 语句必须包含 until。
		return nil, err
	}
	condition, err := parser.parseExpression()
	if err != nil {
		// until 后必须有条件表达式。
		return nil, err
	}

	// 返回 repeat-until 语句节点。
	return &RepeatUntilStatement{Body: body, Condition: condition, Position: startPosition, UntilPosition: untilPosition}, nil
}

// parseForStatement 解析 Lua for 循环语句。
//
// 读取 for 和第一个名称后，根据后续 token 是 `=` 还是 `in` 区分 numeric/generic for。
func (parser *Parser) parseForStatement() (Statement, error) {
	startPosition := parser.current.Position
	if err := parser.expectKeyword("for"); err != nil {
		// 调用方已经判断 for，该错误只作为防御。
		return nil, err
	}
	firstName, err := parser.expectIdentifier()
	if err != nil {
		// for 后必须先出现循环变量名。
		return nil, err
	}
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "=" {
		// `for name = ...` 是 numeric for。
		return parser.parseNumericForStatement(startPosition, firstName)
	}

	// 其他 for 形态按 generic for 解析，要求后续是逗号名称列表或 in。
	return parser.parseGenericForStatement(startPosition, firstName)
}

// parseNumericForStatement 解析数值 for 循环。
//
// startPosition 是 for 关键字位置；firstName 是已经读取的循环变量名。
func (parser *Parser) parseNumericForStatement(startPosition lexer.Position, firstName string) (Statement, error) {
	if err := parser.expectOperator("="); err != nil {
		// numeric for 的变量名后必须是等号。
		return nil, err
	}
	initExpression, err := parser.parseExpression()
	if err != nil {
		// numeric for 必须有初始值表达式。
		return nil, err
	}
	if err := parser.expectOperator(","); err != nil {
		// 初始值后必须跟逗号和边界值。
		return nil, err
	}
	limitExpression, err := parser.parseExpression()
	if err != nil {
		// numeric for 必须有边界值表达式。
		return nil, err
	}

	var stepExpression Expression
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "," {
		// 第三个表达式是可选步长。
		parser.advance()
		stepExpression, err = parser.parseExpression()
		if err != nil {
			// 步长逗号后必须有表达式。
			return nil, err
		}
	}
	if err := parser.expectKeyword("do"); err != nil {
		// numeric for 表达式列表后必须跟 do。
		return nil, err
	}
	body, err := parser.ParseBlock()
	if err != nil {
		// numeric for body 内语句解析失败时返回错误。
		return nil, err
	}
	endPosition := parser.current.Position
	if err := parser.expectKeyword("end"); err != nil {
		// numeric for 必须以 end 结束。
		return nil, err
	}

	// 返回 numeric for 语句节点。
	return &NumericForStatement{Name: firstName, Init: initExpression, Limit: limitExpression, Step: stepExpression, Body: body, Position: startPosition, EndPosition: endPosition}, nil
}

// parseGenericForStatement 解析泛型 for 循环。
//
// startPosition 是 for 关键字位置；firstName 是已经读取的第一个迭代变量名。
func (parser *Parser) parseGenericForStatement(startPosition lexer.Position, firstName string) (Statement, error) {
	names := []string{firstName}
	for parser.current.Kind == lexer.TokenOperator && parser.current.Text == "," {
		if err := parser.checkSyntaxListLimit(len(names) + 1); err != nil {
			// 过长泛型 for 名称列表按 Lua 5.3 parser 递归限制返回错误。
			return nil, err
		}
		// generic for 允许多个迭代变量名。
		parser.advance()
		nextName, err := parser.expectIdentifier()
		if err != nil {
			// 逗号后必须继续出现变量名。
			return nil, err
		}
		names = append(names, nextName)
	}
	if err := parser.expectKeyword("in"); err != nil {
		// generic for 名称列表后必须是 in。
		return nil, err
	}
	iterators, err := parser.parseExpressionList()
	if err != nil {
		// in 后必须有迭代表达式列表。
		return nil, err
	}
	if err := parser.expectKeyword("do"); err != nil {
		// generic for 表达式列表后必须跟 do。
		return nil, err
	}
	body, err := parser.ParseBlock()
	if err != nil {
		// generic for body 内语句解析失败时返回错误。
		return nil, err
	}
	endPosition := parser.current.Position
	if err := parser.expectKeyword("end"); err != nil {
		// generic for 必须以 end 结束。
		return nil, err
	}

	// 返回 generic for 语句节点。
	return &GenericForStatement{Names: names, Iterators: iterators, Body: body, Position: startPosition, EndPosition: endPosition}, nil
}

// parseBreakStatement 解析 break 语句。
//
// 当前阶段只构造 AST，不检查它是否位于循环内部。
func (parser *Parser) parseBreakStatement() (Statement, error) {
	position := parser.current.Position
	if err := parser.expectKeyword("break"); err != nil {
		// 调用方已经判断 break，该错误只作为防御。
		return nil, err
	}

	// 返回 break 语句节点。
	return &BreakStatement{Position: position}, nil
}

// parseGotoStatement 解析 goto 语句。
//
// 语法为 `goto name`；标签存在性和作用域合法性后续单独检查。
func (parser *Parser) parseGotoStatement() (Statement, error) {
	position := parser.current.Position
	if err := parser.expectKeyword("goto"); err != nil {
		// 调用方已经判断 goto，该错误只作为防御。
		return nil, err
	}
	labelName, err := parser.expectIdentifier()
	if err != nil {
		// goto 后必须跟目标标签名。
		return nil, err
	}

	// 返回 goto 语句节点。
	return &GotoStatement{Label: labelName, Position: position}, nil
}

// parseLabelStatement 解析 label 语句。
//
// 语法为 `::name::`；当前阶段只保留标签名。
func (parser *Parser) parseLabelStatement() (Statement, error) {
	position := parser.current.Position
	if err := parser.expectOperator("::"); err != nil {
		// 调用方已经判断双冒号，该错误只作为防御。
		return nil, err
	}
	labelName, err := parser.expectIdentifier()
	if err != nil {
		// label 中间必须是名称。
		return nil, err
	}
	if err := parser.expectOperator("::"); err != nil {
		// label 必须用第二个双冒号关闭。
		return nil, err
	}

	// 返回 label 语句节点。
	return &LabelStatement{Name: labelName, Position: position}, nil
}

// parseFunctionBody 解析函数体。
//
// 当前阶段支持普通参数名列表和空参数；函数体以 end 结束。
func (parser *Parser) parseFunctionBody() (*FunctionBody, error) {
	startPosition := parser.current.Position
	if err := parser.expectOperator("("); err != nil {
		// 函数体必须以左括号开始参数列表。
		return nil, err
	}
	var params []string
	var vararg bool
	if !(parser.current.Kind == lexer.TokenOperator && parser.current.Text == ")") {
		// 非空参数列表支持普通名称和末尾 vararg。
		for {
			if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "..." {
				// `...` 只能作为当前阶段参数列表的最后一项。
				vararg = true
				parser.advance()
				break
			}
			name, err := parser.expectIdentifier()
			if err != nil {
				// 参数项必须是 identifier 或 `...`。
				return nil, err
			}
			params = append(params, name)
			if !(parser.current.Kind == lexer.TokenOperator && parser.current.Text == ",") {
				// 没有逗号时参数列表结束。
				break
			}
			if err := parser.checkSyntaxListLimit(len(params) + 1); err != nil {
				// 过长参数列表按 Lua 5.3 parser 递归限制返回 too many C levels。
				return nil, err
			}
			parser.advance()
			if parser.current.Kind == lexer.TokenOperator && parser.current.Text == ")" {
				// 逗号后不能直接结束参数列表。
				return nil, parser.errorf(parser.current, "expected parameter")
			}
		}
	}
	if err := parser.expectOperator(")"); err != nil {
		// 参数列表必须用右括号关闭。
		return nil, err
	}
	body, err := parser.ParseBlock()
	if err != nil {
		// 函数体 block 内语句解析失败时返回错误。
		return nil, err
	}
	endPosition := parser.current.Position
	if err := parser.expectKeyword("end"); err != nil {
		// 函数体必须以 end 结束。
		return nil, err
	}

	// 返回函数体节点。
	return &FunctionBody{Params: params, Vararg: vararg, Body: body, Position: startPosition, LastLineDefined: endPosition.Line}, nil
}

// parseReturnStatement 解析 return 语句。
//
// return 后可以没有表达式；若存在表达式，则按当前表达式列表能力解析。
func (parser *Parser) parseReturnStatement() (*ReturnStatement, error) {
	// 普通 return 语句使用标准 block 结束 token 判定表达式列表边界。
	return parser.parseReturnStatementUntil(nil)
}

// parseReturnStatementUntil 解析 return 语句，并允许上下文额外定义 return 结束 token。
//
// extraEnd 用于 switch case/default 这类上下文边界；为 nil 时仅使用标准 Lua block 边界。
func (parser *Parser) parseReturnStatementUntil(extraEnd func(lexer.Token) bool) (*ReturnStatement, error) {
	position := parser.current.Position
	if err := parser.expectKeyword("return"); err != nil {
		// 调用方已经判断 return，该错误只作为防御。
		return nil, err
	}
	var values []Expression
	if parser.isReturnEndToken(parser.current, extraEnd) {
		// return 后直接结束 block 或语句，表示无返回值。
		if parser.current.Kind == lexer.TokenOperator && parser.current.Text == ";" {
			// Lua 允许 `return;`，分号属于 return 语句自身而不是后续空语句。
			parser.advance()
		}
		return &ReturnStatement{Values: values, Position: position}, nil
	}
	var err error
	values, err = parser.parseExpressionList()
	if err != nil {
		// return 后表达式列表解析失败时返回错误。
		return nil, err
	}
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == ";" {
		// return 后允许可选分号。
		parser.advance()
	}

	// 返回 return 语句节点。
	return &ReturnStatement{Values: values, Position: position}, nil
}

// parseNameList 解析一个或多个逗号分隔名称。
//
// 返回值至少包含一个名称；当前位置必须是 identifier。
func (parser *Parser) parseNameList() ([]string, error) {
	firstName, err := parser.expectIdentifier()
	if err != nil {
		// 名称列表必须以 identifier 开头。
		return nil, err
	}
	names := []string{firstName}
	for parser.current.Kind == lexer.TokenOperator && parser.current.Text == "," {
		// 逗号后必须继续出现一个名称。
		parser.advance()
		nextName, err := parser.expectIdentifier()
		if err != nil {
			// 缺少逗号后的名称时返回错误。
			return nil, err
		}
		names = append(names, nextName)
	}

	// 返回完整名称列表。
	return names, nil
}

// parseNameExpressionList 解析一个或多个名称表达式。
//
// 当前阶段用于普通赋值左侧，后续会扩展 table field 和 upvalue 等可赋值表达式。
func (parser *Parser) parseNameExpressionList() ([]Expression, error) {
	startPosition := parser.current.Position
	names, err := parser.parseNameList()
	if err != nil {
		// 名称列表解析失败时直接返回错误。
		return nil, err
	}
	expressions := make([]Expression, 0, len(names))
	for _, name := range names {
		// 当前没有逐个保存名称位置，先使用列表起始位置；后续 token span 会细化。
		expressions = append(expressions, &NameExpression{Name: name, Position: startPosition})
	}

	// 返回名称表达式列表。
	return expressions, nil
}

// parseExpressionList 解析一个或多个逗号分隔表达式。
//
// 表达式按 Lua 5.3 优先级解析；返回值至少包含一个表达式。
func (parser *Parser) parseExpressionList() ([]Expression, error) {
	firstExpression, err := parser.parseExpression()
	if err != nil {
		// 表达式列表必须至少包含一个表达式。
		return nil, err
	}
	expressions := []Expression{firstExpression}
	for parser.current.Kind == lexer.TokenOperator && parser.current.Text == "," {
		// 逗号后必须继续出现表达式。
		parser.advance()
		nextExpression, err := parser.parseExpression()
		if err != nil {
			// 缺少逗号后的表达式时返回错误。
			return nil, err
		}
		expressions = append(expressions, nextExpression)
	}

	// 返回完整表达式列表。
	return expressions, nil
}

// parseExpression 解析 Lua 表达式。
//
// 该入口从最低优先级开始，内部通过 precedence climbing 对齐 Lua 5.3 二元操作符结合性。
func (parser *Parser) parseExpression() (Expression, error) {
	// 从最低优先级 1 开始解析完整表达式。
	return parser.parseSubExpression(1)
}

// parseSubExpression 解析指定最低优先级以上的表达式。
//
// minimumPriority 是当前调用允许绑定的最低二元优先级；右结合操作符会保持同级递归。
func (parser *Parser) parseSubExpression(minimumPriority int) (Expression, error) {
	if err := parser.enterSyntaxLevel(parser.current); err != nil {
		// 表达式递归过深时返回 Lua 5.3 兼容的 parser 层级错误。
		return nil, err
	}
	defer parser.leaveSyntaxLevel()

	leftExpression, err := parser.parseUnaryExpression()
	if err != nil {
		// 左侧表达式解析失败时无法继续归约二元表达式。
		return nil, err
	}
	for {
		priority, rightAssociative, ok := parser.binaryPriority(parser.current)
		if !ok || priority < minimumPriority {
			// 当前 token 不是可绑定的二元操作符，表达式在此结束。
			break
		}
		operatorToken := parser.current
		parser.advance()
		nextMinimumPriority := priority + 1
		if rightAssociative {
			// 右结合操作符允许右侧再次绑定同优先级，Lua 5.3 中 `^` 和 `..` 使用该规则。
			nextMinimumPriority = priority
		}
		rightExpression, err := parser.parseSubExpression(nextMinimumPriority)
		if err != nil {
			// 二元操作符右侧必须存在合法表达式。
			return nil, err
		}
		leftExpression = &BinaryExpression{Operator: operatorToken.Text, Left: leftExpression, Right: rightExpression, Position: operatorToken.Position}
	}

	// 返回按优先级归约后的表达式。
	return leftExpression, nil
}

// parseUnaryExpression 解析 Lua 一元表达式。
//
// 支持 `not`、`#`、`-`、`~`；一元表达式优先级低于幂运算以匹配 Lua 5.3。
func (parser *Parser) parseUnaryExpression() (Expression, error) {
	token := parser.current
	if parser.isUnaryOperator(token) {
		// 一元操作符右侧按 Lua 5.3 一元优先级解析，因此 `-a^b` 会归约为 `-(a^b)`。
		parser.advance()
		operand, err := parser.parseSubExpression(11)
		if err != nil {
			// 一元操作符后必须存在操作数。
			return nil, err
		}
		return &UnaryExpression{Operator: token.Text, Operand: operand, Position: token.Position}, nil
	}

	// 非一元开头时解析 primary/postfix 表达式。
	return parser.parsePostfixExpression()
}

// parsePostfixExpression 解析 primary 表达式及其后缀。
//
// 当前阶段支持点号字段访问 `a.b`、索引访问 `a[b]`、普通函数调用 `f(...)` 和方法调用 `receiver:name(...)`。
func (parser *Parser) parsePostfixExpression() (Expression, error) {
	expression, err := parser.parsePrimaryExpression()
	if err != nil {
		// primary 表达式解析失败时无法继续处理调用后缀。
		return nil, err
	}
	for {
		if !isPostfixPrefixExpression(expression) {
			// Lua 5.3 只有 prefixexp 可以继续接字段、索引或调用后缀；table constructor
			// 和裸 function literal 不能与下一行括号粘连成调用。
			break
		}
		if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "." {
			// 点号后缀表示字段访问，允许后续继续接方法调用或普通调用。
			position := expression.Pos()
			parser.advance()
			fieldName, err := parser.expectIdentifier()
			if err != nil {
				// 点号后必须跟字段名，否则字段访问无法形成合法 prefix expression。
				return nil, err
			}
			expression = &FieldAccessExpression{Receiver: expression, Field: fieldName, Position: position}
			continue
		}
		if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "[" {
			// 方括号后缀表示通用索引访问，索引值按普通表达式解析。
			position := expression.Pos()
			parser.advance()
			indexExpression, err := parser.parseExpression()
			if err != nil {
				// 左方括号后必须跟合法索引表达式。
				return nil, err
			}
			if err := parser.expectOperator("]"); err != nil {
				// 索引表达式必须用右方括号关闭。
				return nil, err
			}
			expression = &IndexExpression{Receiver: expression, Index: indexExpression, Position: position}
			continue
		}
		if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "(" {
			// 左括号后缀表示普通函数调用。
			arguments, err := parser.parseCallArguments()
			if err != nil {
				// 调用参数解析失败时返回错误。
				return nil, err
			}
			expression = &FunctionCallExpression{Function: expression, Arguments: arguments, Position: expression.Pos()}
			continue
		}
		if parser.isShortcutCallArgumentStart(parser.current) {
			// 字符串或 table constructor 可作为无括号单参数调用后缀。
			arguments, err := parser.parseCallArguments()
			if err != nil {
				// 调用简写参数解析失败时返回错误。
				return nil, err
			}
			expression = &FunctionCallExpression{Function: expression, Arguments: arguments, Position: expression.Pos()}
			continue
		}
		if parser.current.Kind == lexer.TokenOperator && parser.current.Text == ":" {
			// 冒号后缀表示 method call，隐式 self 会在 codegen 阶段补入。
			parser.advance()
			methodName, err := parser.expectIdentifier()
			if err != nil {
				// 冒号后必须跟方法名。
				return nil, err
			}
			arguments, err := parser.parseCallArguments()
			if err != nil {
				// 方法名后必须跟合法调用参数。
				return nil, err
			}
			expression = &MethodCallExpression{Receiver: expression, Method: methodName, Arguments: arguments, Position: expression.Pos()}
			continue
		}

		// 当前 token 不再是调用后缀，返回已归约表达式。
		break
	}

	// 返回带调用后缀的表达式。
	return expression, nil
}

// isPostfixPrefixExpression 判断表达式是否属于 Lua 5.3 可接后缀的 prefixexp。
//
// 只有变量、函数调用和括号表达式能继续接 `.name`、`[exp]`、`:name(args)` 或 `(args)`；
// 裸 table constructor、字面量和 function literal 不能作为后缀链起点。
func isPostfixPrefixExpression(expression Expression) bool {
	switch expression.(type) {
	case *NameExpression, *FieldAccessExpression, *IndexExpression, *FunctionCallExpression, *MethodCallExpression, *PrefixExpression:
		// 这些节点对应 Lua prefixexp 或其后缀归约结果。
		return true
	default:
		// 其他表达式必须先用括号包裹才能作为 prefixexp。
		return false
	}
}

// parsePrimaryExpression 解析当前阶段支持的基础表达式。
//
// 支持 identifier、number、string、nil/true/false、vararg、table constructor 和括号表达式。
func (parser *Parser) parsePrimaryExpression() (Expression, error) {
	token := parser.current
	switch token.Kind {
	case lexer.TokenIllegal:
		// 非法 token 不能作为表达式，直接返回带 near 片段的词法错误。
		return nil, parser.illegalTokenError(token)
	case lexer.TokenIdentifier:
		// 标识符表达式表示读取变量。
		parser.advance()
		return &NameExpression{Name: token.Text, Position: token.Position}, nil
	case lexer.TokenNumber:
		// 数字字面量表达式保留 lexer 数字解析结果。
		parser.advance()
		return &LiteralExpression{Kind: token.Kind, Value: token.Text, Number: token.Number, Position: token.Position}, nil
	case lexer.TokenString:
		// 字符串字面量表达式使用解码后的 Literal。
		parser.advance()
		return &LiteralExpression{Kind: token.Kind, Value: token.Literal, Position: token.Position}, nil
	case lexer.TokenOperator:
		// 操作符开头当前支持 vararg、table constructor 和括号 prefix expression。
		if token.Text == "..." {
			parser.advance()
			return &VarargExpression{Position: token.Position}, nil
		}
		if token.Text == "{" {
			return parser.parseTableConstructorExpression()
		}
		if token.Text == "(" {
			return parser.parsePrefixExpression()
		}
	case lexer.TokenKeyword:
		// nil/true/false 是 Lua 基础字面量关键字。
		if token.Text == "nil" || token.Text == "true" || token.Text == "false" {
			parser.advance()
			return &LiteralExpression{Kind: token.Kind, Value: token.Text, Position: token.Position}, nil
		}
		if token.Text == "function" {
			// function 关键字在表达式位置表示匿名函数。
			return parser.parseFunctionExpression()
		}
	}

	// 其他表达式类型尚未实现。
	return nil, parser.errorf(token, "expected expression")
}

// parseFunctionExpression 解析匿名函数表达式。
//
// 当前 token 必须是 function 关键字，函数体复用普通函数体解析逻辑。
func (parser *Parser) parseFunctionExpression() (Expression, error) {
	position := parser.current.Position
	if err := parser.expectKeyword("function"); err != nil {
		// 调用方已经判断 function，该错误只作为防御。
		return nil, err
	}
	body, err := parser.parseFunctionBody()
	if err != nil {
		// 函数体解析失败时无法构造匿名函数表达式。
		return nil, err
	}
	body.LineDefined = position.Line

	// 返回匿名函数表达式节点。
	return &FunctionExpression{Body: body, Position: position}, nil
}

// parseCallArguments 解析函数或方法调用参数。
//
// 支持括号参数列表，以及 Lua 5.3 的字符串/table constructor 单参数简写。
func (parser *Parser) parseCallArguments() ([]Expression, error) {
	if parser.current.Kind == lexer.TokenString {
		// 字符串字面量可直接作为函数调用的唯一参数。
		token := parser.current
		parser.advance()
		return []Expression{&LiteralExpression{Kind: token.Kind, Value: token.Literal, Position: token.Position}}, nil
	}
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "{" {
		// table constructor 可直接作为函数调用的唯一参数。
		tableExpression, err := parser.parseTableConstructorExpression()
		if err != nil {
			// table constructor 参数解析失败时返回错误。
			return nil, err
		}
		return []Expression{tableExpression}, nil
	}
	if err := parser.expectOperator("("); err != nil {
		// 调用参数必须是括号、字符串或 table constructor。
		return nil, err
	}
	var arguments []Expression
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == ")" {
		// 空参数列表表示无显式参数。
		parser.advance()
		return arguments, nil
	}
	var err error
	arguments, err = parser.parseExpressionList()
	if err != nil {
		// 非空参数列表必须是表达式列表。
		return nil, err
	}
	if err := parser.expectOperator(")"); err != nil {
		// 调用参数列表必须用右括号关闭。
		return nil, err
	}

	// 返回调用参数列表。
	return arguments, nil
}

// isShortcutCallArgumentStart 判断 token 是否可开启无括号调用参数。
//
// Lua 5.3 允许 `f "x"` 和 `f {}` 作为单参数函数调用。
func (parser *Parser) isShortcutCallArgumentStart(token lexer.Token) bool {
	if token.Kind == lexer.TokenString {
		// 字符串字面量可直接开启调用简写。
		return true
	}
	if token.Kind == lexer.TokenOperator && token.Text == "{" {
		// table constructor 可直接开启调用简写。
		return true
	}

	// 其他 token 不能作为调用简写后缀。
	return false
}

// parseTableConstructorExpression 解析 table constructor 表达式。
//
// 当前阶段支持数组风格字段和 `name = value` 记录字段，字段分隔符允许 `,` 或 `;`。
func (parser *Parser) parseTableConstructorExpression() (Expression, error) {
	position := parser.current.Position
	if err := parser.expectOperator("{"); err != nil {
		// 调用方已经判断 `{`，该错误只作为防御。
		return nil, err
	}
	var fields []Expression
	var recordFields []TableRecordField
	var indexFields []TableIndexField
	for !(parser.current.Kind == lexer.TokenOperator && parser.current.Text == "}") {
		// 逐个解析 table 字段，保持数组字段和记录字段各自源码顺序。
		fieldExpression, recordField, indexField, fieldKind, err := parser.parseTableField()
		if err != nil {
			// 字段必须是当前阶段支持的表达式。
			return nil, err
		}
		if fieldKind == tableFieldRecord {
			// 记录字段写入 RecordFields，key 是字段名字符串。
			recordFields = append(recordFields, recordField)
		} else if fieldKind == tableFieldIndex {
			// 动态键字段写入 IndexFields，key 在 codegen 阶段求值。
			indexFields = append(indexFields, indexField)
		} else {
			// 数组字段写入 Fields，后续 codegen 按 1-based integer key 编号。
			fields = append(fields, fieldExpression)
		}
		if parser.current.Kind == lexer.TokenOperator && (parser.current.Text == "," || parser.current.Text == ";") {
			// 字段分隔符可以是逗号或分号，尾随分隔符也允许。
			parser.advance()
			if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "}" {
				// 尾随分隔符后直接结束 table constructor。
				break
			}
			continue
		}
		if !(parser.current.Kind == lexer.TokenOperator && parser.current.Text == "}") {
			// 非右花括号且没有分隔符，说明字段列表非法。
			return nil, parser.errorf(parser.current, "expected table field separator or closing brace")
		}
	}
	if err := parser.expectOperator("}"); err != nil {
		// table constructor 必须用右花括号关闭。
		return nil, err
	}

	// 返回 table constructor 表达式节点。
	return &TableConstructorExpression{Fields: fields, RecordFields: recordFields, IndexFields: indexFields, Position: position}, nil
}

type tableFieldKind int

const (
	// tableFieldArray 表示数组风格字段。
	tableFieldArray tableFieldKind = iota
	// tableFieldRecord 表示 `name = value` 字段。
	tableFieldRecord
	// tableFieldIndex 表示 `[key] = value` 字段。
	tableFieldIndex
)

// parseTableField 解析 table constructor 的单个字段。
//
// 返回值中 fieldKind 指明应使用数组字段、名称字段还是动态键字段。
func (parser *Parser) parseTableField() (Expression, TableRecordField, TableIndexField, tableFieldKind, error) {
	if parser.current.Kind == lexer.TokenOperator && parser.current.Text == "[" {
		// 方括号开头表示 `[key] = value` 动态键字段。
		position := parser.current.Position
		parser.advance()
		keyExpression, err := parser.parseExpression()
		if err != nil {
			// key 必须是合法表达式。
			return nil, TableRecordField{}, TableIndexField{}, tableFieldArray, err
		}
		if err := parser.expectOperator("]"); err != nil {
			// 动态 key 必须用右方括号关闭。
			return nil, TableRecordField{}, TableIndexField{}, tableFieldArray, err
		}
		if err := parser.expectOperator("="); err != nil {
			// `[key]` 字段必须跟等号和值表达式。
			return nil, TableRecordField{}, TableIndexField{}, tableFieldArray, err
		}
		valueExpression, err := parser.parseExpression()
		if err != nil {
			// 动态键字段等号右侧必须是合法表达式。
			return nil, TableRecordField{}, TableIndexField{}, tableFieldArray, err
		}
		return nil, TableRecordField{}, TableIndexField{Key: keyExpression, Value: valueExpression, Position: position}, tableFieldIndex, nil
	}
	fieldExpression, err := parser.parseExpression()
	if err != nil {
		// table 字段必须是合法表达式，或者裸 name 后跟等号的记录字段。
		return nil, TableRecordField{}, TableIndexField{}, tableFieldArray, err
	}
	if nameExpression, ok := fieldExpression.(*NameExpression); ok && parser.current.Kind == lexer.TokenOperator && parser.current.Text == "=" {
		// 裸名称后直接跟等号时按 `name = value` 记录字段解析；函数调用等后缀表达式不会进入该分支。
		parser.advance()
		valueExpression, err := parser.parseExpression()
		if err != nil {
			// 记录字段等号右侧必须是合法表达式。
			return nil, TableRecordField{}, TableIndexField{}, tableFieldArray, err
		}
		return nil, TableRecordField{Name: nameExpression.Name, Value: valueExpression, Position: nameExpression.Position}, TableIndexField{}, tableFieldRecord, nil
	}

	// 返回数组风格字段表达式。
	return fieldExpression, TableRecordField{}, TableIndexField{}, tableFieldArray, nil
}

// parsePrefixExpression 解析括号形式 prefix expression。
//
// 当前阶段只支持 `(exp)`，后续 function call 和字段访问会扩展该路径。
func (parser *Parser) parsePrefixExpression() (Expression, error) {
	position := parser.current.Position
	if err := parser.expectOperator("("); err != nil {
		// 调用方已经判断 `(`，该错误只作为防御。
		return nil, err
	}
	innerExpression, err := parser.parseExpression()
	if err != nil {
		// 括号内必须是当前阶段支持的表达式。
		return nil, err
	}
	if err := parser.expectOperator(")"); err != nil {
		// prefix expression 必须用右括号关闭。
		return nil, err
	}

	// 返回括号 prefix expression。
	return &PrefixExpression{Inner: innerExpression, Position: position}, nil
}

// binaryPriority 返回 Lua 5.3 二元操作符优先级和结合性。
//
// ok=false 表示当前 token 不是二元操作符；数字越大表示绑定越紧。
func (parser *Parser) binaryPriority(token lexer.Token) (priority int, rightAssociative bool, ok bool) {
	if token.Kind == lexer.TokenKeyword {
		// `and` 和 `or` 是 Lua 二元关键字操作符。
		switch token.Text {
		case "or":
			return 1, false, true
		case "and":
			return 2, false, true
		default:
			return 0, false, false
		}
	}
	if token.Kind != lexer.TokenOperator {
		// 非操作符 token 不能作为二元操作符。
		return 0, false, false
	}
	switch token.Text {
	case "<", ">", "<=", ">=", "~=", "==":
		// 比较操作符共享同一优先级。
		return 3, false, true
	case "|":
		// 位或优先级低于位异或。
		return 4, false, true
	case "~":
		// 位异或优先级位于位或和位与之间。
		return 5, false, true
	case "&":
		// 位与优先级高于位异或。
		return 6, false, true
	case "<<", ">>":
		// 位移优先级高于位与。
		return 7, false, true
	case "..":
		// 字符串连接在 Lua 5.3 中右结合。
		return 8, true, true
	case "+", "-":
		// 加减共享同一优先级。
		return 9, false, true
	case "*", "/", "//", "%":
		// 乘除、整除和取模共享同一优先级。
		return 10, false, true
	case "^":
		// 幂运算在 Lua 5.3 中右结合，且优先级高于一元操作符。
		return 12, true, true
	default:
		return 0, false, false
	}
}

// isUnaryOperator 判断 token 是否是一元操作符。
//
// Lua 5.3 一元操作符包括关键字 `not` 和操作符 `#`、`-`、`~`。
func (parser *Parser) isUnaryOperator(token lexer.Token) bool {
	if token.Kind == lexer.TokenKeyword && token.Text == "not" {
		// `not` 是逻辑取反一元操作符。
		return true
	}
	if token.Kind == lexer.TokenOperator {
		// `#`、`-` 和 `~` 分别表示长度、算术取负和按位取反。
		switch token.Text {
		case "#", "-", "~":
			return true
		default:
			return false
		}
	}

	// 其他 token 不是一元操作符。
	return false
}

// isCallExpression 判断表达式是否是可作为语句的调用表达式。
//
// Lua 5.3 只允许函数调用和方法调用独立成为语句，普通表达式不能直接作为语句。
func (parser *Parser) isCallExpression(expression Expression) bool {
	switch expression.(type) {
	case *FunctionCallExpression:
		// 普通函数调用可以作为语句。
		return true
	case *MethodCallExpression:
		// 方法调用可以作为语句。
		return true
	default:
		return false
	}
}

// isAssignmentTarget 判断表达式是否可以作为赋值左值。
//
// Lua 5.3 varlist 允许名称、表字段和索引访问；当前阶段已支持名称和点号字段访问。
func (parser *Parser) isAssignmentTarget(expression Expression) bool {
	switch expression.(type) {
	case *NameExpression:
		// 名称表达式可作为局部或全局赋值目标。
		return true
	case *FieldAccessExpression:
		// 点号字段访问可作为表字段赋值目标。
		return true
	case *IndexExpression:
		// 方括号索引访问可作为表字段赋值目标。
		return true
	default:
		// 其他表达式不能出现在赋值左侧。
		return false
	}
}

// expectIdentifier 消费并返回当前 identifier token 文本。
//
// 当前 token 不是 identifier 时返回语法错误。
func (parser *Parser) expectIdentifier() (string, error) {
	if parser.current.Kind != lexer.TokenIdentifier {
		// Lua 名称必须由 lexer 识别为 identifier。
		return "", parser.errorf(parser.current, "expected identifier")
	}
	text := parser.current.Text
	parser.advance()

	// 返回名称文本。
	return text, nil
}

// expectKeyword 消费指定关键字。
//
// 当前 token 不是该关键字时返回语法错误。
func (parser *Parser) expectKeyword(text string) error {
	if parser.current.Kind != lexer.TokenKeyword || parser.current.Text != text {
		// 当前 token 与期望关键字不一致。
		return parser.errorf(parser.current, "expected keyword %q", text)
	}
	parser.advance()

	// 关键字匹配成功。
	return nil
}

// expectContextKeyword 消费指定上下文关键字。
//
// 当前 token 必须是指定文本的 identifier；该 helper 用于扩展语法，避免扩展词污染 Lua 5.3
// 全局保留字表。
func (parser *Parser) expectContextKeyword(text string) error {
	if !parser.isContextKeyword(text) {
		// 当前 token 与期望上下文关键字不一致。
		return parser.errorf(parser.current, "expected keyword %q", text)
	}
	parser.advance()

	// 上下文关键字匹配成功。
	return nil
}

// expectOperator 消费指定操作符。
//
// 当前 token 不是该操作符时返回语法错误。
func (parser *Parser) expectOperator(text string) error {
	if parser.current.Kind != lexer.TokenOperator || parser.current.Text != text {
		// 当前 token 与期望操作符不一致。
		return parser.errorf(parser.current, "expected operator %q", text)
	}
	parser.advance()

	// 操作符匹配成功。
	return nil
}

// advance 消费当前 token 并读取下一个 token。
//
// lexer.NextToken 会跳过空白和注释，因此 parser 只处理有效 token。
func (parser *Parser) advance() {
	// 将 lexer 下一个 token 保存为当前前瞻。
	parser.current = parser.lexer.NextToken()
}

// enterSyntaxLevel 进入一个 parser 递归语法层级。
//
// token 用于错误定位；层级超过 Lua 5.3 LUAI_MAXCCALLS 时返回 `too many C levels`。
func (parser *Parser) enterSyntaxLevel(token lexer.Token) error {
	parser.syntaxDepth++
	if parser.syntaxDepth > maxSyntaxCLevels {
		// 超出官方 parser 递归上限时使用固定错误文本，供 errors.lua syntax limits 匹配。
		return ParseError{Position: token.Position, Message: "too many C levels", Near: parser.loadNearText(token)}
	}

	// 未超过上限时允许调用方继续解析。
	return nil
}

// leaveSyntaxLevel 离开一个 parser 递归语法层级。
//
// 调用方必须与 enterSyntaxLevel 成对 defer 使用，避免错误返回路径泄漏深度计数。
func (parser *Parser) leaveSyntaxLevel() {
	if parser.syntaxDepth <= 0 {
		// 防御异常重复退出，避免计数变成负数影响后续诊断。
		parser.syntaxDepth = 0
		return
	}
	parser.syntaxDepth--
}

// checkSyntaxListLimit 校验逗号列表长度是否超过 Lua 5.3 parser 层级上限。
//
// count 是即将形成的列表元素数量；超过上限时返回 `too many C levels`。
func (parser *Parser) checkSyntaxListLimit(count int) error {
	if count > maxSyntaxCLevels {
		// 官方 errors.lua 使用超长 varlist/explist 触发同一 parser 层级错误。
		return ParseError{Position: parser.current.Position, Message: "too many C levels", Near: parser.loadNearText(parser.current)}
	}

	// 列表长度仍在官方 parser 层级预算内。
	return nil
}

// tooManyLocalVariablesError 构造 Lua 5.3 局部变量数量上限错误。
//
// position 是 local 关键字位置；官方错误文本以所在函数起始行附近的 `line N` 展示。
func (parser *Parser) tooManyLocalVariablesError(position lexer.Position) error {
	line := position.Line
	if line > 1 {
		// Lua 5.3 对函数体内局部变量过多的错误会归因到函数定义行。
		line--
	}
	return ParseError{Position: position, Message: fmt.Sprintf("line %d: too many local variables", line)}
}

// isBlockEnd 判断 token 是否表示当前 block 结束。
//
// EOF 或控制流结束关键字会终止 block 解析。
func (parser *Parser) isBlockEnd(token lexer.Token) bool {
	// 对外保持原有标准 Lua block 结束语义。
	return parser.isBlockEndToken(token, nil)
}

// isBlockEndToken 判断 token 是否表示当前 block 结束，并允许调用方追加上下文边界。
func (parser *Parser) isBlockEndToken(token lexer.Token, extraEnd func(lexer.Token) bool) bool {
	if extraEnd != nil && extraEnd(token) {
		// switch case/default 等上下文边界可额外终止当前 block。
		return true
	}
	if token.Kind == lexer.TokenEOF {
		// EOF 结束顶层 block。
		return true
	}
	if token.Kind == lexer.TokenKeyword {
		// end/else/elseif/until 会结束当前 block，留给上层结构消费。
		switch token.Text {
		case "end", "else", "elseif", "until":
			return true
		default:
			return false
		}
	}

	// 其他 token 不结束 block。
	return false
}

// isReturnEnd 判断 token 是否表示 return 语句的表达式列表结束。
//
// EOF、block 结束关键字或分号都可以结束 return。
func (parser *Parser) isReturnEnd(token lexer.Token) bool {
	// 对外保持原有标准 Lua return 结束语义。
	return parser.isReturnEndToken(token, nil)
}

// isReturnEndToken 判断 token 是否表示 return 语句的表达式列表结束。
func (parser *Parser) isReturnEndToken(token lexer.Token, extraEnd func(lexer.Token) bool) bool {
	if parser.isBlockEndToken(token, extraEnd) {
		// block 结束 token 同时结束 return。
		return true
	}
	if token.Kind == lexer.TokenOperator && token.Text == ";" {
		// 分号可以结束 return 语句。
		return true
	}

	// 其他 token 需要继续按表达式列表解析。
	return false
}

// isContextKeyword 判断当前 token 是否是指定上下文关键字。
//
// case/default 不进入全局保留字表，避免破坏普通 Lua 变量名；switch 内部通过该函数识别边界。
func (parser *Parser) isContextKeyword(text string) bool {
	if parser.current.Kind != lexer.TokenIdentifier || parser.current.Text != text {
		// 只有指定 identifier 文本才可在上下文中当作关键字。
		return false
	}

	// 命中上下文关键字。
	return true
}

// isSwitchBlockEnd 判断 token 是否结束当前 switch case/default block。
func (parser *Parser) isSwitchBlockEnd(token lexer.Token) bool {
	if token.Kind == lexer.TokenIdentifier && (token.Text == "case" || token.Text == "default") {
		// switch 内的 case/default 会结束前一个分支 block。
		return true
	}
	if token.Kind == lexer.TokenKeyword && token.Text == "end" {
		// end 关闭整个 switch，同时也结束当前分支 block。
		return true
	}

	// 其他 token 不结束 switch 分支 block。
	return false
}

// errorf 构造带源码位置的 parser 错误。
//
// token 提供错误位置；format 和 args 描述具体语法期望。
func (parser *Parser) errorf(token lexer.Token, format string, args ...any) error {
	// 错误消息包含行列，便于后续 golden 对比。
	message := fmt.Sprintf(format, args...)
	return ParseError{Position: token.Position, Message: message, Near: parser.loadNearText(token)}
}

// LoadErrorText 将 parser/codegen 错误转换成 Lua 5.3 `load` 可见错误文本。
//
// err 可以是 ParseError、ParseErrorList 或普通编译错误；sourceName 是 chunk source 名称。解析错误
// 会格式化为 `[string "..."]:line: message near token`，普通错误保持原文本。
func LoadErrorText(err error, sourceName string) string {
	if err == nil {
		// nil 错误没有可展示文本。
		return ""
	}
	parseError, ok := firstParseError(err)
	if !ok {
		// 非 parser 错误不具备 near/source 结构，保留原始错误。
		return err.Error()
	}
	message := parseError.Message
	if strings.Contains(message, "unsupported statement") {
		// Lua 5.3 对非法语句起始符使用 unexpected symbol 语义。
		message = strings.ReplaceAll(message, "unsupported statement", "unexpected symbol")
	}
	if parseError.Near != "" && !strings.Contains(message, " near ") {
		// parser 内部 expected 文本不含 near；load 对外错误必须补出失败 token。
		message = message + " near " + parseError.Near
	}
	return fmt.Sprintf("%s:%d: %s", loadSourceID(sourceName), parseError.Position.Line, message)
}

// firstParseError 从错误链中取出首个 parser 错误。
//
// err 可为单个 ParseError 或 ParseErrorList；返回 false 表示不是 parser 错误。
func firstParseError(err error) (ParseError, bool) {
	var parseError ParseError
	if errors.As(err, &parseError) {
		// 单个结构化 parser 错误可直接返回。
		return parseError, true
	}
	var parseErrors ParseErrorList
	if errors.As(err, &parseErrors) && len(parseErrors) > 0 {
		// 聚合错误按 Lua 5.3 load 语义展示第一处错误。
		return parseErrors[0], true
	}
	return ParseError{}, false
}

// loadNearText 生成 Lua 5.3 load 错误的 near token 文本。
//
// token 来自 parser 当前前瞻；EOF 和 char(...) 片段不加引号，其余 token 使用单引号包裹。
func (parser *Parser) loadNearText(token lexer.Token) string {
	switch token.Kind {
	case lexer.TokenEOF:
		// EOF 在 Lua 错误文本中展示为 <eof>。
		return "<eof>"
	case lexer.TokenString:
		// 字符串需要使用源码字面量，例如 `'aa'` 或 `[[a]]`，以匹配官方 near 片段。
		return "'" + parser.rawStringTokenText(token) + "'"
	case lexer.TokenIllegal:
		// 非法字符和非法字符串保留 lexer 给出的 near 片段。
		if token.Text == "<eof>" || strings.HasPrefix(token.Text, "char(") || strings.HasPrefix(token.Text, "<") {
			return token.Text
		}
		if rawText := parser.rawIllegalTokenText(token); rawText != "" {
			return rawText
		}
		return "'" + token.Text + "'"
	default:
		// 普通标识符、关键字、数字和操作符统一使用单引号包裹。
		if token.Text == "" {
			return ""
		}
		return "'" + token.Text + "'"
	}
}

// rawStringTokenText 从源码中恢复字符串 token 的原始字面量。
//
// token 必须是 TokenString；无法恢复时回退到解码文本，保证错误消息仍可读。
func (parser *Parser) rawStringTokenText(token lexer.Token) string {
	offset := token.Position.Offset
	if offset < 0 || offset >= len(parser.input) {
		// offset 无效时回退到解码文本。
		return token.Text
	}
	switch parser.input[offset] {
	case '\'', '"':
		// 短字符串从起始引号扫描到未转义的同类结束引号。
		quote := parser.input[offset]
		for index := offset + 1; index < len(parser.input); index++ {
			if parser.input[index] == '\\' {
				// 跳过转义字符后的一个字节，避免误判转义引号。
				index++
				continue
			}
			if parser.input[index] == quote {
				return parser.input[offset : index+1]
			}
		}
	case '[':
		// 长字符串从 `[=*[` 扫描到匹配的 `]=*]`。
		if endIndex, ok := rawLongStringEnd(parser.input, offset); ok {
			return parser.input[offset:endIndex]
		}
	}
	// 其他情况回退到 lexer 解码后的文本。
	return token.Text
}

// rawLongStringEnd 返回长字符串源码字面量的右边界。
func rawLongStringEnd(input string, offset int) (int, bool) {
	if offset >= len(input) || input[offset] != '[' {
		// 长字符串必须以 `[` 开始。
		return 0, false
	}
	level := 0
	index := offset + 1
	for index < len(input) && input[index] == '=' {
		// 统计 `[=*[` 中的等号层级。
		level++
		index++
	}
	if index >= len(input) || input[index] != '[' {
		// 不是合法长字符串起始。
		return 0, false
	}
	closeText := "]" + strings.Repeat("=", level) + "]"
	if closeOffset := strings.Index(input[index+1:], closeText); closeOffset >= 0 {
		// 返回包含关闭分隔符的右边界。
		return index + 1 + closeOffset + len(closeText), true
	}
	return 0, false
}

// rawIllegalTokenText 生成非法字符 token 的 Lua near 片段。
//
// 控制字符和非 ASCII 字节按 `<byte>` 形式展示，以匹配官方 errors.lua 的 checksyntax 断言。
func (parser *Parser) rawIllegalTokenText(token lexer.Token) string {
	offset := token.Position.Offset
	if offset < 0 || offset >= len(parser.input) {
		// offset 无效时无法恢复原始字节。
		return ""
	}
	rawByte := parser.input[offset]
	if rawByte < 32 || rawByte >= 127 {
		// Lua 5.3 对不可打印字符使用尖括号包裹十进制字节，并作为普通 token 加引号。
		return fmt.Sprintf("'<\\%d>'", rawByte)
	}
	return ""
}

// loadSourceID 生成 Lua 5.3 错误前缀中的 source 标识。
//
// sourceName 遵循 Lua chunk name 规则：`=` 前缀表示精确名称，`@` 前缀表示文件名，其余按字符串
// chunk 展示为 `[string "..."]`，并限制长度不超过 Lua 5.3 的 LUA_IDSIZE-1。
func loadSourceID(sourceName string) string {
	const maxSourceIDLength = 59
	if strings.HasPrefix(sourceName, "=") {
		// `=name` 直接使用 name，过长时截断到可见上限。
		return truncateRight(sourceName[1:], maxSourceIDLength)
	}
	if strings.HasPrefix(sourceName, "@") {
		// 文件名过长时保留尾部，符合 Lua 对长路径的摘要策略。
		return truncateLeft(sourceName[1:], maxSourceIDLength)
	}
	firstLine := sourceName
	if newlineIndex := strings.IndexAny(firstLine, "\r\n"); newlineIndex >= 0 {
		// 字符串 chunk 只展示首行摘要。
		firstLine = firstLine[:newlineIndex]
	}
	escaped := strings.ReplaceAll(firstLine, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	prefix := `[string "`
	suffix := `"]`
	available := maxSourceIDLength - len(prefix) - len(suffix)
	if available < 0 {
		// 理论上不会发生；保留防御，避免负长度切片。
		available = 0
	}
	return prefix + truncateRight(escaped, available) + suffix
}

// truncateRight 从右侧截断字符串到最多 limit 字节。
func truncateRight(text string, limit int) string {
	if limit < 0 {
		// 非法上限按空字符串处理。
		return ""
	}
	if len(text) <= limit {
		// 未超过上限时原样返回。
		return text
	}
	if limit <= 3 {
		// 极小上限无法保留省略号和内容，只返回截断省略号。
		return strings.Repeat(".", limit)
	}
	return text[:limit-3] + "..."
}

// truncateLeft 从左侧截断字符串到最多 limit 字节。
func truncateLeft(text string, limit int) string {
	if limit < 0 {
		// 非法上限按空字符串处理。
		return ""
	}
	if len(text) <= limit {
		// 未超过上限时原样返回。
		return text
	}
	if limit <= 3 {
		// 极小上限无法保留省略号和内容，只返回截断省略号。
		return strings.Repeat(".", limit)
	}
	return "..." + text[len(text)-(limit-3):]
}

// illegalTokenError 构造 Lua 风格非法 token 错误。
//
// Lua 5.3 的 scanner 错误会带 `near` 片段；官方 literals.lua 用该片段匹配非法 escape
// 和未终止字符串，因此 parser 不能把非法 token 降级成普通 expected expression。
func (parser *Parser) illegalTokenError(token lexer.Token) error {
	nearText := token.Text
	if nearText == "" {
		// 缺少 token 文本时回退到 token kind，保证错误消息仍有 near 片段。
		nearText = string(token.Kind)
	}
	if nearText == "<eof>" {
		// 未终止字符串按官方 Lua 语义报告 near <eof>，不额外加引号。
		return parser.errorf(token, "near <eof>: illegal token: %v", token.Err)
	}
	if rawText := parser.rawIllegalTokenText(token); rawText != "" {
		// 语句起始处的不可打印非法字节也必须使用 `<\ddd>` 形态，供 load 错误统一补 near。
		return parser.errorf(token, "illegal token: %v", token.Err)
	}

	// 非 EOF 非法 token 使用单引号包裹源码片段，匹配 Lua 5.3 scanner 错误文案形态。
	return parser.errorf(token, "near '%s': illegal token: %v", nearText, token.Err)
}
