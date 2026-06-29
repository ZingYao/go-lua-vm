//go:build lua53 || (!with_switch && !with_all && with_continue)

package extensions

// compiledSwitch 表示当前构建不包含 switch 扩展。
const compiledSwitch SyntaxSet = 0
