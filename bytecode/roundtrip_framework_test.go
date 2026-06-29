package bytecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBytecodeRoundTripFrameworkGolden 验证 bytecode roundtrip 测试框架说明与 golden 保持一致。
//
// 该测试不替代实际 roundtrip 测试；它固定当前框架覆盖面，便于后续扩展时同步更新迁移基线。
func TestBytecodeRoundTripFrameworkGolden(t *testing.T) {
	// 框架说明列出本轮已经纳入 roundtrip 的入口和字段覆盖范围。
	actualText := strings.Join([]string{
		"bytecode roundtrip framework:",
		"- DumpBinaryChunk + LoadBinaryChunk",
		"- DumpProto + LoadProto",
		"- header, constants, instructions, upvalues, child protos, line info, local vars",
	}, "\n")

	goldenBytes, err := os.ReadFile(filepath.Join("..", "tests", "golden", "bytecode_roundtrip.txt"))
	if err != nil {
		// golden 文件缺失说明 bytecode 回归资产不完整。
		t.Fatalf("read bytecode framework golden failed: %v", err)
	}

	expectedText := strings.TrimRight(string(goldenBytes), "\n")
	if actualText != expectedText {
		// 框架覆盖说明变化时必须同步更新 golden，避免 TODO 状态和测试实际不一致。
		t.Fatalf("bytecode framework golden mismatch:\n got:\n%s\nwant:\n%s", actualText, expectedText)
	}
}
