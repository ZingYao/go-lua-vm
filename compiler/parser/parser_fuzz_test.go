package parser

import "testing"

// FuzzParserParseChunk 验证 parser 在任意输入上以错误返回而不是 panic 终止。
//
// fuzz 入口覆盖 Lua 5.3 chunk 解析边界；当前阶段只要求 ParseChunk 对非法输入保持可恢复错误语义。
func FuzzParserParseChunk(f *testing.F) {
	// 种子覆盖空 chunk、local、函数、控制流、goto/label 和不完整语句。
	for _, seed := range []string{
		"",
		"local a = 1",
		"function f(a, ...) return a, ... end",
		"if true then a = 1 else a = 2 end",
		"goto done ::done::",
		"function",
		"\x00\xff",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// 每个 fuzz 用例都创建独立 parser，避免 token 前瞻状态跨用例污染。
		parser := New(input)
		if _, err := parser.ParseChunk(); err != nil {
			// 非法源码返回错误是预期行为，fuzz 只关注 panic 和不可恢复崩溃。
			return
		}
	})
}
