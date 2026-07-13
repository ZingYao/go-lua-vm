package lua

import (
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// FuzzGluaStructuredDecoders 验证 JSON、YAML、XML 和 TOML 解码器对任意文本不发生 panic。
func FuzzGluaStructuredDecoders(fuzzer *testing.F) {
	// 固定合法、边界和损坏输入种子，后续 fuzz 只检查安全终止和错误返回。
	fuzzer.Add(byte(0), `{"ready":true}`)
	fuzzer.Add(byte(1), "ready: true\n")
	fuzzer.Add(byte(2), `<root><ready>true</ready></root>`)
	fuzzer.Add(byte(3), "ready = true\n")
	fuzzer.Add(byte(0), `{"nested":[[[null]]]}`)
	fuzzer.Add(byte(2), `<root><broken></root>`)
	fuzzer.Fuzz(func(t *testing.T, decoder byte, text string) {
		// 限制输入大小，避免 fuzz 基础设施绕过公开解码预算造成无意义资源消耗。
		if len(text) > 64<<10 {
			// 超出审计输入上限时跳过本轮。
			t.Skip()
		}
		arguments := []runtime.Value{runtime.StringValue(text)}
		switch decoder % 4 {
		case 0:
			// JSON 解码错误属于预期结果，只要求不 panic。
			_, _ = gluaJSONDecode(arguments...)
		case 1:
			// YAML 解码错误属于预期结果，只要求不 panic。
			_, _ = gluaYAMLDecode(arguments...)
		case 2:
			// XML 解码错误属于预期结果，只要求不 panic。
			_, _ = gluaXMLDecode(arguments...)
		default:
			// TOML 解码错误属于预期结果，只要求不 panic。
			_, _ = gluaTOMLDecode(arguments...)
		}
	})
}

// FuzzGluaZIPDecompress 验证任意 ZIP 字节输入受预算约束并安全返回。
func FuzzGluaZIPDecompress(fuzzer *testing.F) {
	// 空内容、普通文本和最小 ZIP 头覆盖常见解析入口。
	fuzzer.Add([]byte{})
	fuzzer.Add([]byte("not-a-zip"))
	fuzzer.Add([]byte{'P', 'K', 3, 4})
	fuzzer.Fuzz(func(t *testing.T, archive []byte) {
		// 限制 archive 种子大小，解压后大小仍由正式代码预算控制。
		if len(archive) > 256<<10 {
			// 超出审计输入上限时跳过本轮。
			t.Skip()
		}
		_, _ = gluaZIPDecompress(runtime.StringValue(string(archive)))
	})
}
