package bytecode

import (
	"bytes"
	"testing"
)

// FuzzLoadBinaryChunk 验证 binary chunk loader 对任意字节输入保持可恢复错误语义。
//
// fuzz 入口覆盖 lundump 对照迁移中的头部、常量表、Proto 嵌套和截断输入边界。
func FuzzLoadBinaryChunk(f *testing.F) {
	// 种子覆盖空输入、只有头部、有效 roundtrip chunk 和截断签名。
	for _, seed := range [][]byte{
		nil,
		AppendChunkHeader(nil),
		DumpBinaryChunk(roundTripProtoFixture()),
		[]byte(ChunkSignature[:2]),
		[]byte("\x1bLua\x53\x00"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		// LoadBinaryChunk 必须通过 error 表达无效 chunk，不能因损坏输入 panic。
		if _, err := LoadBinaryChunk(bytes.NewReader(input)); err != nil {
			// 任意损坏或不完整 chunk 返回错误都是预期行为。
			return
		}
	})
}
