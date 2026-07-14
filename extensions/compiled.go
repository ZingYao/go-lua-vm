package extensions

// Compiled 返回统一主线始终包含的语法扩展集合。
func Compiled() SyntaxSet {
	// 统一主线固定编译全部已实现扩展，运行时仍可通过 syntax 选项关闭。
	return compiledContinue | compiledSwitch | compiledConst
}
