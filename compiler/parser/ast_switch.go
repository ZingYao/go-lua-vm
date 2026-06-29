//go:build !lua53 && (with_switch || with_all || (!with_switch && !with_continue && !with_all))

package parser

import "github.com/zing/go-lua-vm/compiler/lexer"

// SwitchCase 表示扩展 switch 语法中的一个 case 分支。
//
// Values 保存一个或多个匹配表达式；Body 保存匹配后执行的语句块。
type SwitchCase struct {
	// Values 保存 case 后的匹配表达式列表。
	Values []Expression
	// Body 保存 case 命中后执行的 block。
	Body *Block
	// Position 保存 case 上下文关键字位置。
	Position lexer.Position
}

// SwitchStatement 表示扩展语法 switch 语句。
//
// Expression 只求值一次；Cases 按顺序匹配；DefaultBlock 为空表示没有 default 分支。
type SwitchStatement struct {
	// Expression 保存 switch 主表达式。
	Expression Expression
	// Cases 保存按源码顺序排列的 case 分支。
	Cases []SwitchCase
	// DefaultBlock 保存可选 default 分支。
	DefaultBlock *Block
	// Position 保存 switch 关键字位置。
	Position lexer.Position
	// DefaultPosition 保存 default 上下文关键字位置；无 default 时为零值。
	DefaultPosition lexer.Position
	// EndPosition 保存关闭 switch 的 end 关键字位置。
	EndPosition lexer.Position
}

// Pos 返回 switch 语句起始位置。
//
// 返回值指向 switch 关键字。
func (statement *SwitchStatement) Pos() lexer.Position {
	// switch 语句位置在构造时固定。
	return statement.Position
}

// statementNode 标记 SwitchStatement 是语句节点。
//
// 该方法没有运行时逻辑，仅用于 Go 类型系统区分 AST 节点类别。
func (statement *SwitchStatement) statementNode() {
	// 空实现用于接口标记。
}
