package lexer

import "testing"

// FuzzLexerNextToken 验证 lexer 在任意输入上持续推进并最终到达 EOF 或非法 token。
//
// fuzz 入口只检查词法器的健壮性：输入可以是任意字节串，NextToken 不应 panic，也不应卡在同一位置。
func FuzzLexerNextToken(f *testing.F) {
	// 种子覆盖空输入、基础表达式、注释、长字符串和不完整字符串等典型 Lua 5.3 词法边界。
	for _, seed := range []string{
		"",
		"local a = 1 + 2",
		"-- comment\nreturn 'ok'",
		"[=[long\nstring]=]",
		"'unterminated",
		"\x00\xff",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// 每次 fuzz 用例都从新 lexer 开始，避免跨用例共享位置状态。
		lexer := New(input)
		for tokenIndex := 0; tokenIndex < len(input)+8; tokenIndex++ {
			// token 数上限与输入长度绑定，用于发现未推进导致的潜在死循环。
			token := lexer.NextToken()
			if token.Kind == TokenEOF {
				// 到达 EOF 表示该输入扫描完成。
				return
			}
			if token.Kind == TokenIllegal {
				// 非法 token 是词法错误的受控表达，返回即可避免 fuzz 把合法错误当成失败。
				return
			}
		}

		// 超过上限仍未结束说明 lexer 可能没有消费输入。
		t.Fatalf("lexer did not finish within bounded token count")
	})
}
