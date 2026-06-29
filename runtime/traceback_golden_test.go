package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTracebackMatchesGolden 验证 runtime traceback 输出与 golden 保持一致。
//
// 当前阶段 traceback 只包含错误消息、固定标题和调用帧类型；后续接入源码行号时需要同步更新 golden。
func TestTracebackMatchesGolden(t *testing.T) {
	// 构造一组固定调用帧，保证 traceback 文本顺序稳定。
	frames := []CallFrame{
		NewGoCallFrame(ReferenceValue(KindGoClosure, "outer"), 1, 0),
		NewLuaCallFrame(ReferenceValue(KindLuaClosure, "inner"), 1, 0),
	}

	actualText := Traceback("boom", frames)
	goldenBytes, err := os.ReadFile(filepath.Join("..", "tests", "golden", "runtime_traceback.golden"))
	if err != nil {
		// golden 文件缺失会让 traceback 兼容测试失去基线。
		t.Fatalf("read traceback golden failed: %v", err)
	}

	expectedText := strings.TrimRight(string(goldenBytes), "\n")
	actualText = strings.TrimRight(actualText, "\n")
	if actualText != expectedText {
		// traceback 文本变化必须经过迁移语义确认后更新 golden。
		t.Fatalf("traceback golden mismatch:\n got:\n%s\nwant:\n%s", actualText, expectedText)
	}
}
