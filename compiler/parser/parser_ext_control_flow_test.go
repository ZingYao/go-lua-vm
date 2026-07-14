package parser

import (
	"strings"
	"testing"
)

// TestParserContinueSwitchStatements 验证扩展 continue 与 switch/case/default 语法。
//
// case/default 在 switch 内按上下文关键字解析；普通 Lua 代码中 case 仍可作为变量名使用。
func TestParserContinueSwitchStatements(t *testing.T) {
	parser := New("local case = 1 local out = 0 while case < 4 do case = case + 1 continue end switch case do case 1, 2 out = 5 default out = 6 end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// continue 和 switch 扩展语法应能完成解析。
		t.Fatalf("parse continue/switch failed: %v", err)
	}
	if len(chunk.Block.Statements) != 4 {
		// 顶层应包含两个 local、while 和 switch 四条语句。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}
	whileStatement, ok := chunk.Block.Statements[2].(*WhileStatement)
	if !ok {
		// 第三条语句应为 while。
		t.Fatalf("third statement should be while")
	}
	if _, ok := whileStatement.Body.Statements[1].(*ContinueStatement); !ok {
		// while body 中应解析出 continue。
		t.Fatalf("while body should contain continue")
	}
	switchStatement, ok := chunk.Block.Statements[3].(*SwitchStatement)
	if !ok {
		// 第四条语句应为 switch。
		t.Fatalf("fourth statement should be switch")
	}
	if len(switchStatement.Cases) != 1 || len(switchStatement.Cases[0].Values) != 2 || switchStatement.DefaultBlock == nil {
		// switch 应保留多值 case 和 default block。
		t.Fatalf("unexpected switch statement=%+v", switchStatement)
	}
}

// TestParserRejectsDuplicateSwitchCaseValue 验证同一个 switch 内重复 case 值会报错。
func TestParserRejectsDuplicateSwitchCaseValue(t *testing.T) {
	parser := New("switch 1 do\ncase 1, 2\nprint('x')\ncase 2\nprint('y')\nend\n")

	_, err := parser.ParseChunk()
	if err == nil {
		// 重复 case 值会让后续分支不可达，必须在 parser 语义阶段报错。
		t.Fatalf("parse should reject duplicate switch case value")
	}
	if !strings.Contains(err.Error(), "duplicate switch case value") {
		// 错误应明确提示重复 case 值。
		t.Fatalf("error = %v", err)
	}
}
