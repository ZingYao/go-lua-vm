//go:build !lua53 && (with_continue || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package extensions

// compiledContinue 表示当前构建包含 continue 扩展。
const compiledContinue SyntaxSet = SyntaxContinue
