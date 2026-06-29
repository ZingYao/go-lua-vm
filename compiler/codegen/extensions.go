package codegen

import "github.com/zing/go-lua-vm/compiler/parser"

// extensionStatementCompiler 表示一个可选语法扩展的语句编译入口。
type extensionStatementCompiler func(*generator, parser.Statement) (bool, error)

// extensionStatementCompilers 保存当前构建产物编译进来的扩展语句编译器。
var extensionStatementCompilers []extensionStatementCompiler

// registerExtensionStatementCompiler 注册一个扩展语句编译器。
func registerExtensionStatementCompiler(compiler extensionStatementCompiler) {
	// 扩展文件在 init 阶段注册，核心 codegen 不直接引用具体扩展类型。
	extensionStatementCompilers = append(extensionStatementCompilers, compiler)
}

// compileExtensionStatement 尝试按已编译扩展编译当前语句。
func (generator *generator) compileExtensionStatement(statement parser.Statement) (bool, error) {
	for _, compile := range extensionStatementCompilers {
		// 扩展编译器自行判断语句类型，命中时返回 handled=true。
		handled, err := compile(generator, statement)
		if handled || err != nil {
			return handled, err
		}
	}

	// 没有任何扩展处理当前语句。
	return false, nil
}

// extensionFunctionChecker 表示一个可选语法扩展的函数子树检测入口。
type extensionFunctionChecker func(parser.Statement) (bool, bool)

// extensionFunctionCheckers 保存当前构建产物编译进来的扩展函数子树检测器。
var extensionFunctionCheckers []extensionFunctionChecker

// registerExtensionFunctionChecker 注册一个扩展函数子树检测器。
func registerExtensionFunctionChecker(checker extensionFunctionChecker) {
	// 扩展文件在 init 阶段注册，核心 codegen 不直接引用具体扩展类型。
	extensionFunctionCheckers = append(extensionFunctionCheckers, checker)
}

// extensionStatementContainsFunction 尝试按已编译扩展检查当前语句是否包含匿名函数。
func extensionStatementContainsFunction(statement parser.Statement) (bool, bool) {
	for _, checker := range extensionFunctionCheckers {
		// 扩展检测器自行判断语句类型，命中时返回 handled=true。
		contains, handled := checker(statement)
		if handled {
			return contains, true
		}
	}

	// 没有任何扩展处理当前语句。
	return false, false
}
