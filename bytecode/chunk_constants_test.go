package bytecode

import (
	"bytes"
	"errors"
	"testing"
)

// TestLoadConstants 验证 Lua 5.3 常量表可以按顺序读取。
//
// 该测试覆盖 nil、boolean、integer、number 和 string 五类 Proto 常量。
func TestLoadConstants(t *testing.T) {
	expectedConstants := []Constant{
		NilConstant(),
		BooleanConstant(true),
		IntegerConstant(123456789),
		NumberConstant(12.5),
		StringConstant("hello"),
	}

	constants, err := LoadConstants(bytes.NewReader(AppendConstants(nil, expectedConstants)))
	if err != nil {
		// 合法常量表不应返回错误。
		t.Fatalf("load constants failed: %v", err)
	}
	if len(constants) != len(expectedConstants) {
		// 常量数量必须和 chunk 中声明的一致。
		t.Fatalf("constant count mismatch: got %d want %d", len(constants), len(expectedConstants))
	}

	for constantIndex := range expectedConstants {
		// 每个常量必须保持类型和值，避免 LOADK 后续读取错误。
		if constants[constantIndex] != expectedConstants[constantIndex] {
			t.Fatalf("constant[%d] mismatch: got %#v want %#v", constantIndex, constants[constantIndex], expectedConstants[constantIndex])
		}
	}
}

// TestLoadConstantsRejectsUnknownTag 验证未知常量 tag 会被拒绝。
//
// 损坏 chunk 或其他 Lua 版本 chunk 可能包含未知 tag，loader 必须返回分类错误。
func TestLoadConstantsRejectsUnknownTag(t *testing.T) {
	chunk := appendInt(nil, 1)
	chunk = append(chunk, 0xfe)

	_, err := LoadConstants(bytes.NewReader(chunk))
	if !errors.Is(err, ErrInvalidChunkData) {
		// 错误必须包装 ErrInvalidChunkData，便于上层区分 header 与 body 损坏。
		t.Fatalf("expected invalid chunk data error, got %v", err)
	}
}

// TestLoadConstantsRejectsTruncatedString 验证截断字符串常量会被拒绝。
//
// 字符串长度字段与实际负载不一致时，不能返回半截字符串。
func TestLoadConstantsRejectsTruncatedString(t *testing.T) {
	chunk := appendInt(nil, 1)
	chunk = append(chunk, luaTypeShortString, 6, 'h', 'e')

	_, err := LoadConstants(bytes.NewReader(chunk))
	if !errors.Is(err, ErrInvalidChunkData) {
		// 截断字符串必须被归类为 chunk body 错误。
		t.Fatalf("expected invalid chunk data error, got %v", err)
	}
}

// TestLoadConstantsLongString 验证 0xff size_t 长度编码的字符串可读取。
//
// Lua 5.3 对长字符串使用 0xff 标记并追加 size_t 长度，本测试覆盖该分支。
func TestLoadConstantsLongString(t *testing.T) {
	longString := string(make([]byte, int(longStringSizeMarker)))
	chunk := appendInt(nil, 1)
	chunk = append(chunk, luaTypeLongString)
	chunk = appendLuaString(chunk, longString)

	constants, err := LoadConstants(bytes.NewReader(chunk))
	if err != nil {
		// 合法长字符串常量不应返回错误。
		t.Fatalf("load long string constant failed: %v", err)
	}
	if len(constants) != 1 || constants[0].Kind != ConstantString || len(constants[0].String) != len(longString) {
		// 长字符串长度必须完整保留。
		t.Fatalf("long string constant mismatch: constants=%#v", constants)
	}
}
