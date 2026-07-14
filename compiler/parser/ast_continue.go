package parser

import "github.com/ZingYao/go-lua-vm/compiler/lexer"

// ContinueStatement 表示扩展语法 continue 语句。
//
// continue 只能出现在循环体内；codegen 会把它降级为跳往最近循环续迭代位置的 JMP。
type ContinueStatement struct {
	// Position 保存 continue 关键字位置。
	Position lexer.Position
}

// Pos 返回 continue 语句起始位置。
//
// 返回值指向 continue 关键字。
func (statement *ContinueStatement) Pos() lexer.Position {
	// continue 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 ContinueStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *ContinueStatement) statementNode() {
	// 空实现用于接口标记。
}
