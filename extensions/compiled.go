package extensions

// Compiled 返回当前构建产物已经编译进来的语法扩展集合。
func Compiled() SyntaxSet {
	// 各扩展位由 build tag 文件决定，默认构建包含当前已实现扩展。
	return compiledContinue | compiledSwitch
}
