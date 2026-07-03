// Package parser 提供 Lua 5.3 语法分析器。
//
// 本包当前实现 chunk、block、基础赋值、函数语句和基础控制流的最小 AST；
// 后续会逐步扩展控制流、函数、表达式优先级和作用域检查。
package parser

import "github.com/zing/go-lua-vm/compiler/lexer"

// Node 表示所有 AST 节点的公共接口。
//
// Position 返回节点起始 token 位置，用于错误报告和后续 debug info 生成。
type Node interface {
	// Pos 返回 AST 节点起始位置。
	Pos() lexer.Position
}

// Chunk 表示 Lua 源码文件或源码片段。
//
// Lua 5.3 chunk 由一个 block 构成；后续 parser 会在 Chunk 上挂载文件名和作用域信息。
type Chunk struct {
	// Block 保存 chunk 顶层 block。
	Block *Block
	// Position 保存 chunk 起始位置。
	Position lexer.Position
}

// Pos 返回 chunk 起始位置。
//
// 若 chunk 为空，位置通常指向 EOF。
func (chunk *Chunk) Pos() lexer.Position {
	// Chunk 的起始位置在构造时固定。
	return chunk.Position
}

// Block 表示 Lua 语句块。
//
// 当前阶段只保存顺序语句列表；return、break 和作用域信息会在后续任务中扩展。
type Block struct {
	// Statements 保存 block 内按源码顺序排列的语句。
	Statements []Statement
	// Return 保存可选 return 语句；Lua 语义要求 return 是 block 最后一条语句。
	Return *ReturnStatement
	// Scope 保存 block 对应的词法作用域信息，供 codegen 和 goto 合法性校验使用。
	Scope *ScopeInfo
	// Position 保存 block 起始位置。
	Position lexer.Position
}

// Pos 返回 block 起始位置。
//
// 空 block 的位置由调用方按当前 token 或 EOF 设置。
func (block *Block) Pos() lexer.Position {
	// Block 的起始位置在构造时固定。
	return block.Position
}

// ScopeInfo 表示 Lua block 的词法作用域摘要。
//
// ID 在单次 parser 运行内递增；ParentID 为 -1 表示顶层作用域。
type ScopeInfo struct {
	// ID 保存作用域唯一编号。
	ID int
	// ParentID 保存父作用域编号。
	ParentID int
	// ParentStatementIndex 保存当前 block 所属语句在父 block 中的下标，顶层为 -1。
	ParentStatementIndex int
	// Depth 保存从顶层 block 开始计算的作用域深度。
	Depth int
	// StatementCount 保存当前 block 的普通语句数量。
	StatementCount int
	// TrailingCondition 表示 block 尾部还有可见本 block local 的条件表达式，例如 repeat-until。
	TrailingCondition bool
	// Locals 保存当前 block 直接声明的局部变量生命周期。
	Locals []LocalInfo
	// Labels 保存当前 block 直接声明的 label。
	Labels []LabelInfo
	// Gotos 保存当前 block 直接出现的 goto。
	Gotos []GotoInfo
}

// LocalInfo 表示一个局部变量在 block 内的生命周期。
//
// StartStatement 是声明所在语句下标；EndStatement 是该 block 普通语句数量，表示生命周期延续到 block 结束。
type LocalInfo struct {
	// Name 保存局部变量名。
	Name string
	// StartStatement 保存局部变量声明所在语句下标。
	StartStatement int
	// EndStatement 保存局部变量生命周期结束语句下标。
	EndStatement int
	// Position 保存局部变量声明位置。
	Position lexer.Position
}

// LabelInfo 表示一个 Lua label 声明。
//
// StatementIndex 保存 label 在当前 block 中的语句下标。
type LabelInfo struct {
	// Name 保存 label 名称。
	Name string
	// StatementIndex 保存 label 语句下标。
	StatementIndex int
	// Position 保存 label 位置。
	Position lexer.Position
}

// GotoInfo 表示一个 Lua goto 声明。
//
// StatementIndex 保存 goto 在当前 block 中的语句下标。
type GotoInfo struct {
	// Name 保存 goto 目标 label 名称。
	Name string
	// StatementIndex 保存 goto 语句下标。
	StatementIndex int
	// Position 保存 goto 位置。
	Position lexer.Position
}

// Statement 表示 Lua 语句节点。
//
// statementNode 是内部标记方法，避免表达式误实现 Statement。
type Statement interface {
	Node
	// statementNode 标记当前节点是语句。
	statementNode()
}

// EmptyStatement 表示 Lua 空语句 `;`。
//
// 空语句不产生运行时效果，但 parser 需要保留它用于语法兼容测试。
type EmptyStatement struct {
	// Position 保存分号位置。
	Position lexer.Position
}

// Pos 返回空语句起始位置。
//
// 返回值指向 `;` token。
func (statement *EmptyStatement) Pos() lexer.Position {
	// 空语句位置就是分号 token 位置。
	return statement.Position
}

// statementNode 标记 EmptyStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *EmptyStatement) statementNode() {
	// 空实现用于接口标记。
}

// AssignmentStatement 表示普通赋值语句。
//
// 当前阶段 Left 只支持名称列表；Right 支持名称、数字、字符串、nil/true/false 基础表达式。
type AssignmentStatement struct {
	// Left 保存赋值左侧变量列表。
	Left []Expression
	// Right 保存赋值右侧表达式列表。
	Right []Expression
	// Position 保存赋值语句起始位置。
	Position lexer.Position
}

// Pos 返回赋值语句起始位置。
//
// 返回值通常指向左侧第一个变量。
func (statement *AssignmentStatement) Pos() lexer.Position {
	// 普通赋值语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 AssignmentStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *AssignmentStatement) statementNode() {
	// 空实现用于接口标记。
}

// FunctionCallStatement 表示 Lua 函数或方法调用语句。
//
// Call 保存调用表达式；Lua 允许函数调用作为语句并丢弃返回值。
type FunctionCallStatement struct {
	// Call 保存函数调用或方法调用表达式。
	Call Expression
	// Position 保存调用语句起始位置。
	Position lexer.Position
}

// Pos 返回函数调用语句起始位置。
//
// 返回值指向调用表达式起始位置。
func (statement *FunctionCallStatement) Pos() lexer.Position {
	// 函数调用语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 FunctionCallStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *FunctionCallStatement) statementNode() {
	// 空实现用于接口标记。
}

// DoStatement 表示 Lua `do ... end` 显式 block 语句。
//
// Body 保存 do/end 包裹的子 block；该语句主要用于创建词法作用域，不自带运行时控制流跳转。
type DoStatement struct {
	// Body 保存 do 语句内部 block。
	Body *Block
	// Position 保存 do 关键字位置。
	Position lexer.Position
}

// Pos 返回 do 语句起始位置。
//
// 返回值指向 do 关键字。
func (statement *DoStatement) Pos() lexer.Position {
	// do 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 DoStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *DoStatement) statementNode() {
	// 空实现用于接口标记。
}

// LocalAssignmentStatement 表示 Lua `local` 变量声明赋值语句。
//
// Names 保存声明的局部变量名；Values 保存可选初始化表达式列表。
type LocalAssignmentStatement struct {
	// Names 保存 local 声明中的变量名。
	Names []string
	// Values 保存 local 声明的初始化表达式。
	Values []Expression
	// Position 保存 local 关键字位置。
	Position lexer.Position
}

// Pos 返回 local 赋值语句起始位置。
//
// 返回值指向 `local` 关键字。
func (statement *LocalAssignmentStatement) Pos() lexer.Position {
	// local 赋值语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 LocalAssignmentStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *LocalAssignmentStatement) statementNode() {
	// 空实现用于接口标记。
}

// FunctionBody 表示 Lua 函数体。
//
// 当前阶段 Params 只保存普通参数名，Body 保存函数 block；vararg、method self 和 upvalue 后续扩展。
type FunctionBody struct {
	// Params 保存函数参数名列表。
	Params []string
	// inlineParams 保存最常见的单参数函数，避免为 Params 单独分配底层数组。
	inlineParams [1]string
	// Vararg 表示函数是否声明了 `...` 可变参数。
	Vararg bool
	// Body 保存函数体 block。
	Body *Block
	// inlineBody 保存函数体自身 block，避免为每个函数体单独分配 Block 对象。
	inlineBody Block
	// Position 保存左括号位置。
	Position lexer.Position
	// LineDefined 保存 function 关键字所在源码行，供 Proto debug 信息使用。
	LineDefined int
	// LastLineDefined 保存关闭函数体的 end 关键字所在源码行，供 Proto debug 信息使用。
	LastLineDefined int
}

// Pos 返回函数体起始位置。
//
// 返回值指向函数体左括号。
func (body *FunctionBody) Pos() lexer.Position {
	// 函数体位置在构造时固定。
	return body.Position
}

// FunctionExpression 表示 Lua 匿名函数表达式。
//
// Body 保存 function 关键字后的函数体；该表达式在 codegen 阶段生成 closure 值。
type FunctionExpression struct {
	// Body 保存匿名函数体。
	Body *FunctionBody
	// Position 保存 function 关键字位置。
	Position lexer.Position
}

// Pos 返回函数表达式起始位置。
//
// 返回值指向 function 关键字。
func (expression *FunctionExpression) Pos() lexer.Position {
	// 函数表达式位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 FunctionExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *FunctionExpression) expressionNode() {
	// 空实现用于接口标记。
}

// LocalFunctionStatement 表示 Lua `local function name (...) ... end`。
//
// Lua 会先声明 local 名称再绑定闭包；当前 AST 只保留语法结构，作用域后续单独实现。
type LocalFunctionStatement struct {
	// Name 保存 local function 名称。
	Name string
	// Body 保存函数体。
	Body *FunctionBody
	// Position 保存 local 关键字位置。
	Position lexer.Position
}

// Pos 返回 local function 语句起始位置。
//
// 返回值指向 local 关键字。
func (statement *LocalFunctionStatement) Pos() lexer.Position {
	// local function 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 LocalFunctionStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *LocalFunctionStatement) statementNode() {
	// 空实现用于接口标记。
}

// FunctionStatement 表示 Lua `function name (...) ... end`。
//
// 当前阶段 Name 只支持简单名称；字段函数和 method 语法后续随 prefix expression 扩展。
type FunctionStatement struct {
	// Name 保存函数名称。
	Name string
	// Body 保存函数体。
	Body *FunctionBody
	// Position 保存 function 关键字位置。
	Position lexer.Position
}

// Pos 返回 function 语句起始位置。
//
// 返回值指向 function 关键字。
func (statement *FunctionStatement) Pos() lexer.Position {
	// function 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 FunctionStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *FunctionStatement) statementNode() {
	// 空实现用于接口标记。
}

// IfClause 表示 if 或 elseif 的条件分支。
//
// Condition 保存分支条件，Block 保存条件为真时执行的语句块。
type IfClause struct {
	// Condition 保存 if/elseif 条件表达式。
	Condition Expression
	// Block 保存条件成立时执行的 block。
	Block *Block
	// Position 保存 if 或 elseif 关键字位置。
	Position lexer.Position
	// ThenPosition 保存 then 关键字位置，供 debug line hook 标注条件检查行号。
	ThenPosition lexer.Position
}

// IfStatement 表示 Lua if/elseif/else 语句。
//
// Clauses 至少包含一个 if 分支；ElseBlock 为空表示没有 else。
type IfStatement struct {
	// Clauses 保存 if 和 elseif 分支。
	Clauses []IfClause
	// ElseBlock 保存可选 else 分支。
	ElseBlock *Block
	// Position 保存 if 关键字位置。
	Position lexer.Position
	// ElsePosition 保存 else 关键字位置；没有 else 时为零值。
	ElsePosition lexer.Position
	// EndPosition 保存关闭 if 语句的 end 关键字位置。
	EndPosition lexer.Position
}

// Pos 返回 if 语句起始位置。
//
// 返回值指向 if 关键字。
func (statement *IfStatement) Pos() lexer.Position {
	// if 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 IfStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *IfStatement) statementNode() {
	// 空实现用于接口标记。
}

// WhileStatement 表示 Lua while 循环语句。
//
// Condition 保存循环条件，Body 保存循环体 block。
type WhileStatement struct {
	// Condition 保存 while 条件表达式。
	Condition Expression
	// Body 保存循环体 block。
	Body *Block
	// Position 保存 while 关键字位置。
	Position lexer.Position
	// DoPosition 保存 do 关键字位置，供条件检查指令标注 debug 行号。
	DoPosition lexer.Position
	// EndPosition 保存关闭 while 语句的 end 关键字位置。
	EndPosition lexer.Position
}

// Pos 返回 while 语句起始位置。
//
// 返回值指向 while 关键字。
func (statement *WhileStatement) Pos() lexer.Position {
	// while 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 WhileStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *WhileStatement) statementNode() {
	// 空实现用于接口标记。
}

// RepeatUntilStatement 表示 Lua repeat-until 循环语句。
//
// Body 保存循环体 block，Condition 保存 until 后的退出条件。
type RepeatUntilStatement struct {
	// Body 保存 repeat 循环体。
	Body *Block
	// Condition 保存 until 条件表达式。
	Condition Expression
	// Position 保存 repeat 关键字位置。
	Position lexer.Position
	// UntilPosition 保存 until 关键字位置，供后置条件和回跳指令标注 debug 行号。
	UntilPosition lexer.Position
}

// Pos 返回 repeat 语句起始位置。
//
// 返回值指向 repeat 关键字。
func (statement *RepeatUntilStatement) Pos() lexer.Position {
	// repeat 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 RepeatUntilStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *RepeatUntilStatement) statementNode() {
	// 空实现用于接口标记。
}

// NumericForStatement 表示 Lua 数值 for 循环语句。
//
// 语法为 `for name = init, limit[, step] do block end`；Step 为空表示使用 Lua 默认步长 1。
type NumericForStatement struct {
	// Name 保存循环变量名。
	Name string
	// Init 保存初始值表达式。
	Init Expression
	// Limit 保存边界值表达式。
	Limit Expression
	// Step 保存可选步长表达式。
	Step Expression
	// Body 保存循环体 block。
	Body *Block
	// Position 保存 for 关键字位置。
	Position lexer.Position
	// EndPosition 保存关闭 numeric for 的 end 关键字位置。
	EndPosition lexer.Position
}

// Pos 返回 numeric for 语句起始位置。
//
// 返回值指向 for 关键字。
func (statement *NumericForStatement) Pos() lexer.Position {
	// numeric for 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 NumericForStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *NumericForStatement) statementNode() {
	// 空实现用于接口标记。
}

// GenericForStatement 表示 Lua generic for 循环语句。
//
// 语法为 `for name-list in exp-list do block end`。
type GenericForStatement struct {
	// Names 保存迭代变量名列表。
	Names []string
	// Iterators 保存 in 后的表达式列表。
	Iterators []Expression
	// Body 保存循环体 block。
	Body *Block
	// Position 保存 for 关键字位置。
	Position lexer.Position
	// EndPosition 保存关闭 generic for 的 end 关键字位置。
	EndPosition lexer.Position
}

// Pos 返回 generic for 语句起始位置。
//
// 返回值指向 for 关键字。
func (statement *GenericForStatement) Pos() lexer.Position {
	// generic for 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 GenericForStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *GenericForStatement) statementNode() {
	// 空实现用于接口标记。
}

// BreakStatement 表示 Lua break 语句。
//
// 当前阶段只解析语法节点；是否位于循环内会在作用域/控制流校验阶段处理。
type BreakStatement struct {
	// Position 保存 break 关键字位置。
	Position lexer.Position
}

// Pos 返回 break 语句起始位置。
//
// 返回值指向 break 关键字。
func (statement *BreakStatement) Pos() lexer.Position {
	// break 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 BreakStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *BreakStatement) statementNode() {
	// 空实现用于接口标记。
}

// GotoStatement 表示 Lua goto 语句。
//
// Label 保存跳转目标标签名；合法性和作用域穿越检查后续单独实现。
type GotoStatement struct {
	// Label 保存跳转目标标签名。
	Label string
	// Position 保存 goto 关键字位置。
	Position lexer.Position
}

// Pos 返回 goto 语句起始位置。
//
// 返回值指向 goto 关键字。
func (statement *GotoStatement) Pos() lexer.Position {
	// goto 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 GotoStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *GotoStatement) statementNode() {
	// 空实现用于接口标记。
}

// LabelStatement 表示 Lua label 语句。
//
// 语法为 `::name::`；label 合法性和重复检查后续单独实现。
type LabelStatement struct {
	// Name 保存标签名。
	Name string
	// Position 保存第一个 `::` 位置。
	Position lexer.Position
}

// Pos 返回 label 语句起始位置。
//
// 返回值指向第一个 `::` 操作符。
func (statement *LabelStatement) Pos() lexer.Position {
	// label 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 LabelStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *LabelStatement) statementNode() {
	// 空实现用于接口标记。
}

// ReturnStatement 表示 Lua return 语句。
//
// Values 保存返回表达式列表；空列表表示 `return` 不返回任何值。
type ReturnStatement struct {
	// Values 保存 return 后的表达式列表。
	Values []Expression
	// inlineValues 保存最常见的单返回表达式，避免为 Values 单独分配底层数组。
	inlineValues [1]Expression
	// Position 保存 return 关键字位置。
	Position lexer.Position
}

// Pos 返回 return 语句起始位置。
//
// 返回值指向 return 关键字。
func (statement *ReturnStatement) Pos() lexer.Position {
	// return 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 ReturnStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *ReturnStatement) statementNode() {
	// 空实现用于接口标记。
}

// Expression 表示 Lua 表达式节点。
//
// expressionNode 是内部标记方法，避免语句误实现 Expression。
type Expression interface {
	Node
	// expressionNode 标记当前节点是表达式。
	expressionNode()
}

// NameExpression 表示名称表达式。
//
// 当前阶段它既可表示赋值左侧变量，也可表示右侧读取表达式。
type NameExpression struct {
	// Name 保存标识符文本。
	Name string
	// Position 保存标识符位置。
	Position lexer.Position
}

// Pos 返回名称表达式起始位置。
//
// 返回值指向标识符 token。
func (expression *NameExpression) Pos() lexer.Position {
	// 名称表达式位置就是标识符位置。
	return expression.Position
}

// expressionNode 标记 NameExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *NameExpression) expressionNode() {
	// 空实现用于接口标记。
}

// LiteralExpression 表示基础字面量表达式。
//
// Kind 复用 lexer token 类型；Value 保存源码文本或解码后字符串。
type LiteralExpression struct {
	// Kind 保存字面量 token 类型。
	Kind lexer.TokenKind
	// Value 保存字面量值文本。
	Value string
	// Number 保存数字字面量详情，仅 Kind 为 TokenNumber 时有效。
	Number lexer.NumberLiteral
	// Position 保存字面量位置。
	Position lexer.Position
}

// Pos 返回字面量表达式起始位置。
//
// 返回值指向对应字面量 token。
func (expression *LiteralExpression) Pos() lexer.Position {
	// 字面量表达式位置就是 token 位置。
	return expression.Position
}

// expressionNode 标记 LiteralExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *LiteralExpression) expressionNode() {
	// 空实现用于接口标记。
}

// VarargExpression 表示 Lua `...` 表达式。
//
// 是否允许 vararg 会在函数作用域检查阶段处理。
type VarargExpression struct {
	// Position 保存 `...` 操作符位置。
	Position lexer.Position
}

// Pos 返回 vararg 表达式起始位置。
//
// 返回值指向 `...` 操作符。
func (expression *VarargExpression) Pos() lexer.Position {
	// vararg 表达式位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 VarargExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *VarargExpression) expressionNode() {
	// 空实现用于接口标记。
}

// TableRecordField 表示 table constructor 中的名称键值字段。
//
// Name 保存 `name = value` 中的字符串 key，Value 保存对应值表达式。
type TableRecordField struct {
	// Name 保存记录字段名称。
	Name string
	// Value 保存记录字段值表达式。
	Value Expression
	// Position 保存字段名位置。
	Position lexer.Position
}

// TableIndexField 表示 table constructor 中的 `[key] = value` 字段。
//
// Key 保存方括号内的键表达式，Value 保存等号右侧的值表达式。
type TableIndexField struct {
	// Key 保存动态 key 表达式。
	Key Expression
	// Value 保存字段值表达式。
	Value Expression
	// Position 保存 `[` 操作符位置。
	Position lexer.Position
}

// TableConstructorExpression 表示 Lua table constructor。
//
// Fields 保存数组风格字段，RecordFields 保存 `name = value` 风格记录字段，IndexFields 保存
// `[key] = value` 动态键字段。
type TableConstructorExpression struct {
	// Fields 保存数组风格字段表达式列表。
	Fields []Expression
	// RecordFields 保存名称键值字段列表。
	RecordFields []TableRecordField
	// IndexFields 保存动态键字段列表。
	IndexFields []TableIndexField
	// Position 保存 `{` 操作符位置。
	Position lexer.Position
}

// Pos 返回 table constructor 起始位置。
//
// 返回值指向 `{` 操作符。
func (expression *TableConstructorExpression) Pos() lexer.Position {
	// table constructor 位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 TableConstructorExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *TableConstructorExpression) expressionNode() {
	// 空实现用于接口标记。
}

// PrefixExpression 表示括号包裹的 prefix expression。
//
// 当前阶段只支持 `(exp)` 这一种 prefix 形态；函数调用和字段访问会在后续 TODO 中扩展。
type PrefixExpression struct {
	// Inner 保存括号内表达式。
	Inner Expression
	// Position 保存 `(` 操作符位置。
	Position lexer.Position
}

// Pos 返回 prefix expression 起始位置。
//
// 返回值指向 `(` 操作符。
func (expression *PrefixExpression) Pos() lexer.Position {
	// prefix expression 位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 PrefixExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *PrefixExpression) expressionNode() {
	// 空实现用于接口标记。
}

// FieldAccessExpression 表示 Lua 点号字段访问表达式。
//
// Receiver 保存点号左侧表达式，Field 保存字段名；后续 codegen 会将其映射为 GETTABLE/GETTABUP 读取。
type FieldAccessExpression struct {
	// Receiver 保存被访问的表或 userdata 表达式。
	Receiver Expression
	// Field 保存点号右侧字段名。
	Field string
	// Position 保存接收者表达式起始位置。
	Position lexer.Position
}

// Pos 返回字段访问表达式起始位置。
//
// 返回值指向接收者表达式的起始位置。
func (expression *FieldAccessExpression) Pos() lexer.Position {
	// 字段访问位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 FieldAccessExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *FieldAccessExpression) expressionNode() {
	// 空实现用于接口标记。
}

// IndexExpression 表示 Lua 方括号索引表达式。
//
// Receiver 保存被索引对象，Index 保存方括号内表达式；后续 codegen 会将其映射为通用表访问。
type IndexExpression struct {
	// Receiver 保存被索引的表或 userdata 表达式。
	Receiver Expression
	// Index 保存方括号内的索引表达式。
	Index Expression
	// Position 保存接收者表达式起始位置。
	Position lexer.Position
}

// Pos 返回索引表达式起始位置。
//
// 返回值指向接收者表达式的起始位置。
func (expression *IndexExpression) Pos() lexer.Position {
	// 索引表达式位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 IndexExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *IndexExpression) expressionNode() {
	// 空实现用于接口标记。
}

// FunctionCallExpression 表示 Lua 函数调用表达式。
//
// Function 保存被调用表达式，Arguments 保存调用参数；返回值数量语义会在 codegen 阶段处理。
type FunctionCallExpression struct {
	// Function 保存被调用的函数表达式。
	Function Expression
	// Arguments 保存调用参数表达式列表。
	Arguments []Expression
	// Position 保存调用起始位置。
	Position lexer.Position
}

// Pos 返回函数调用表达式起始位置。
//
// 返回值指向被调用表达式的起始位置。
func (expression *FunctionCallExpression) Pos() lexer.Position {
	// 函数调用位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 FunctionCallExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *FunctionCallExpression) expressionNode() {
	// 空实现用于接口标记。
}

// MethodCallExpression 表示 Lua method call 表达式。
//
// Receiver 保存冒号左侧表达式，Method 保存方法名，Arguments 保存显式参数；隐式 self 会在 codegen 阶段补入。
type MethodCallExpression struct {
	// Receiver 保存方法接收者表达式。
	Receiver Expression
	// Method 保存冒号后的方法名。
	Method string
	// Arguments 保存显式调用参数表达式列表。
	Arguments []Expression
	// Position 保存接收者表达式起始位置。
	Position lexer.Position
}

// Pos 返回 method call 表达式起始位置。
//
// 返回值指向接收者表达式的起始位置。
func (expression *MethodCallExpression) Pos() lexer.Position {
	// method call 位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 MethodCallExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *MethodCallExpression) expressionNode() {
	// 空实现用于接口标记。
}

// UnaryExpression 表示 Lua 一元表达式。
//
// Operator 保存 `not`、`#`、`-` 或 `~`；Operand 保存右侧操作数。
type UnaryExpression struct {
	// Operator 保存一元操作符文本。
	Operator string
	// Operand 保存一元操作数表达式。
	Operand Expression
	// Position 保存操作符位置。
	Position lexer.Position
}

// Pos 返回一元表达式起始位置。
//
// 返回值指向一元操作符位置。
func (expression *UnaryExpression) Pos() lexer.Position {
	// 一元表达式位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 UnaryExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *UnaryExpression) expressionNode() {
	// 空实现用于接口标记。
}

// BinaryExpression 表示 Lua 二元表达式。
//
// Operator 保存二元操作符文本；Left 和 Right 保存按 Lua 5.3 优先级归约后的左右表达式。
type BinaryExpression struct {
	// Operator 保存二元操作符文本。
	Operator string
	// Left 保存左侧表达式。
	Left Expression
	// Right 保存右侧表达式。
	Right Expression
	// Position 保存操作符位置。
	Position lexer.Position
}

// Pos 返回二元表达式起始位置。
//
// 返回值指向二元操作符位置，便于后续错误和 debug 映射。
func (expression *BinaryExpression) Pos() lexer.Position {
	// 二元表达式位置在构造时固定。
	return expression.Position
}

// expressionNode 标记 BinaryExpression 是表达式节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (expression *BinaryExpression) expressionNode() {
	// 空实现用于接口标记。
}
