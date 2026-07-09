//go:build !lua53 && (with_continue || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package parser

import (
	"strings"
	"testing"
)

// TestParserRejectsContinueOutsideLoop 验证循环外 continue 会被语义阶段拒绝。
func TestParserRejectsContinueOutsideLoop(t *testing.T) {
	parser := New("continue")

	if _, err := parser.ParseChunk(); err == nil || !strings.Contains(err.Error(), "continue outside loop") {
		// 循环外 continue 没有续迭代目标，必须返回明确错误。
		t.Fatalf("expected continue outside loop error, got %v", err)
	}
}
