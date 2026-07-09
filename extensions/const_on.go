//go:build !lua53 && (with_const || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package extensions

// compiledConst 表示当前构建包含 const 扩展。
const compiledConst SyntaxSet = SyntaxConst
