package parser

import "errors"

// IsSyntaxError 判断错误是否属于 Lua 语法或语义解析错误。
//
// err 链中包含 ParseError 或 ParseErrorList 时返回 true。该 helper 位于 parser 包内，
// 避免 runtime 包为了 syntax 分类反向依赖 compiler/parser。
func IsSyntaxError(err error) bool {
	if err == nil {
		// nil 错误不属于语法错误。
		return false
	}

	var parseError ParseError
	if errors.As(err, &parseError) {
		// 单个 ParseError 表示语法或语义解析失败。
		return true
	}

	var parseErrors ParseErrorList
	if errors.As(err, &parseErrors) {
		// ParseErrorList 表示语义校验阶段聚合出的多个解析错误。
		return true
	}

	// 其他错误不由 parser 包归类为 syntax error。
	return false
}
