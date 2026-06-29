package bytecode

import (
	"errors"
	"testing"
)

// TestValidateChunkHeader 验证 Lua 5.3 官方 chunk 头部可以被成功校验。
//
// 该测试覆盖签名、版本、格式、LUAC_DATA、类型尺寸、整数哨兵和浮点哨兵。
func TestValidateChunkHeader(t *testing.T) {
	header, err := ValidateChunkHeader(AppendChunkHeader(nil))
	if err != nil {
		// 合法头部不应返回错误，否则后续 binary chunk loader 无法启动。
		t.Fatalf("validate chunk header failed: %v", err)
	}

	// 校验返回元数据是否保留关键字段，供后续 loader 使用。
	if header.Version != ChunkVersion || header.Format != ChunkFormat || header.InstructionSize != ChunkInstructionSize {
		t.Fatalf("header metadata mismatch: version=%d format=%d instructionSize=%d", header.Version, header.Format, header.InstructionSize)
	}
}

// TestValidateChunkHeaderRejectsBadSignature 验证非法签名会被拒绝。
//
// 签名错误通常表示输入是源码文本或损坏数据，loader 必须停止读取。
func TestValidateChunkHeaderRejectsBadSignature(t *testing.T) {
	chunk := AppendChunkHeader(nil)
	chunk[0] = 0

	_, err := ValidateChunkHeader(chunk)
	if !errors.Is(err, ErrInvalidChunkHeader) {
		// 错误必须包装 ErrInvalidChunkHeader，方便 CLI 和 API 做分类处理。
		t.Fatalf("expected invalid header error, got %v", err)
	}
}

// TestValidateChunkHeaderRejectsBadVersion 验证非 Lua 5.3 版本会被拒绝。
//
// Lua 5.3 binary chunk 与其他版本不保证 opcode 和 Proto 布局兼容。
func TestValidateChunkHeaderRejectsBadVersion(t *testing.T) {
	chunk := AppendChunkHeader(nil)
	chunk[len(ChunkSignature)] = 0x54

	_, err := ValidateChunkHeader(chunk)
	if !errors.Is(err, ErrInvalidChunkHeader) {
		// 版本不匹配必须被归类为无效 chunk header。
		t.Fatalf("expected invalid header error, got %v", err)
	}
}

// TestValidateChunkHeaderRejectsTruncatedInput 验证截断头部会被拒绝。
//
// 该测试确保 reader 数据不足时不会误判为合法 chunk。
func TestValidateChunkHeaderRejectsTruncatedInput(t *testing.T) {
	chunk := AppendChunkHeader(nil)

	_, err := ValidateChunkHeader(chunk[:len(chunk)-1])
	if !errors.Is(err, ErrInvalidChunkHeader) {
		// 截断输入必须返回可分类的 chunk header 错误。
		t.Fatalf("expected invalid header error, got %v", err)
	}
}
