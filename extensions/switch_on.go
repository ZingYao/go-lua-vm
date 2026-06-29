//go:build !lua53 && (with_switch || with_all || (!with_switch && !with_continue && !with_all))

package extensions

// compiledSwitch 表示当前构建包含 switch 扩展。
const compiledSwitch SyntaxSet = SyntaxSwitch
