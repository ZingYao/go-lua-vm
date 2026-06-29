//go:build lua53 || (!with_continue && !with_all && with_switch)

package extensions

// compiledContinue 表示当前构建不包含 continue 扩展。
const compiledContinue SyntaxSet = 0
