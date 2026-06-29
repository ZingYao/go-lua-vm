package parser

import (
	"errors"
	"fmt"
	"testing"

	"github.com/zing/go-lua-vm/compiler/lexer"
)

// TestIsSyntaxError 验证 parser 包能识别单个和聚合解析错误。
//
// 语法错误分类位于 parser 包内，后续 CLI 或 lua 包可以在捕获编译错误时用该 helper
// 区分 syntax error 与 runtime/resource error。
func TestIsSyntaxError(t *testing.T) {
	parseError := ParseError{Position: lexer.Position{Line: 1, Column: 2}, Message: "unexpected token"}
	if !IsSyntaxError(parseError) {
		// 单个 ParseError 必须归类为 syntax error。
		t.Fatalf("parse error should be syntax error")
	}
	if !IsSyntaxError(fmt.Errorf("wrapped: %w", parseError)) {
		// errors.As 包装链中的 ParseError 也必须可识别。
		t.Fatalf("wrapped parse error should be syntax error")
	}

	parseErrors := ParseErrorList{parseError}
	if !IsSyntaxError(parseErrors) {
		// ParseErrorList 必须归类为 syntax error。
		t.Fatalf("parse error list should be syntax error")
	}
	if IsSyntaxError(errors.New("plain")) {
		// 普通 Go error 不应误判为 syntax error。
		t.Fatalf("plain error should not be syntax error")
	}
}
