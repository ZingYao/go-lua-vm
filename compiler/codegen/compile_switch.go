package codegen

import (
	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/compiler/parser"
)

func init() {
	// 注册 switch 扩展语句编译与函数子树检测入口。
	registerExtensionStatementCompiler(compileSwitchExtensionStatement)
	registerExtensionFunctionChecker(switchStatementContainsFunction)
}

// compileSwitchExtensionStatement 尝试编译 switch 扩展语句。
func compileSwitchExtensionStatement(generator *generator, statement parser.Statement) (bool, error) {
	typedStatement, ok := statement.(*parser.SwitchStatement)
	if !ok {
		// 当前语句不是 switch 扩展节点。
		return false, nil
	}
	return true, generator.compileSwitchStatement(typedStatement)
}

// compileSwitchStatement 编译扩展 switch/case/default 语句。
//
// switch 主表达式只求值一次；每个 case 通过 EQ/JMP 测试匹配，命中分支执行后跳到 switch 结束。
func (generator *generator) compileSwitchStatement(statement *parser.SwitchStatement) error {
	switchRegister := generator.allocateRegister()
	if err := generator.compileExpressionTo(statement.Expression, switchRegister); err != nil {
		// 主表达式编译失败时释放临时寄存器并返回。
		generator.releaseRegister(switchRegister)
		return err
	}

	var endJumpPCs []int
	for caseIndex := range statement.Cases {
		// 每个 case 按源码顺序生成匹配检查，未命中则落到下一分支检查。
		switchCase := statement.Cases[caseIndex]
		var matchJumpPCs []int
		for _, valueExpression := range switchCase.Values {
			caseRegister := generator.allocateRegister()
			if err := generator.compileExpressionTo(valueExpression, caseRegister); err != nil {
				// case 表达式编译失败时释放寄存器并返回。
				generator.releaseRegister(caseRegister)
				generator.releaseRegister(switchRegister)
				return err
			}
			if err := generator.withSourceLine(switchCase.Position, func() error {
				// EQ A=1 时匹配成功会执行下一条 JMP；匹配失败会跳过该 JMP 继续检查后续 case 值。
				generator.emitABC(bytecode.OpEq, 1, switchRegister, caseRegister)
				return nil
			}); err != nil {
				// 当前闭包只生成指令，不预期返回错误；保留分支便于未来扩展。
				generator.releaseRegister(caseRegister)
				generator.releaseRegister(switchRegister)
				return err
			}
			matchJumpPCs = append(matchJumpPCs, generator.emitJump(0))
			generator.releaseRegister(caseRegister)
		}
		nextCaseJumpPC := generator.emitJump(0)
		bodyStartPC := len(generator.proto.Code)
		for _, matchJumpPC := range matchJumpPCs {
			// 所有命中的 case 值都跳到同一个 case body 入口。
			generator.patchJump(matchJumpPC, bodyStartPC)
		}
		if err := generator.compileScopedBlock(switchCase.Body); err != nil {
			// case body 编译失败时释放 switch 临时寄存器并返回。
			generator.releaseRegister(switchRegister)
			return err
		}
		endJumpPCs = append(endJumpPCs, generator.emitJump(0))
		generator.patchJump(nextCaseJumpPC, len(generator.proto.Code))
	}

	if statement.DefaultBlock != nil {
		// default 位于所有 case 未命中路径之后，无需额外匹配检查。
		if err := generator.compileScopedBlock(statement.DefaultBlock); err != nil {
			// default body 编译失败时释放 switch 临时寄存器并返回。
			generator.releaseRegister(switchRegister)
			return err
		}
	}
	for _, endJumpPC := range endJumpPCs {
		// 所有已执行 case 分支统一跳过后续分支到 switch 结束位置。
		generator.patchJump(endJumpPC, len(generator.proto.Code))
	}
	generator.releaseRegister(switchRegister)

	// switch 语句编译完成。
	return nil
}

// switchStatementContainsFunction 检查 switch 扩展语句是否包含匿名函数。
func switchStatementContainsFunction(statement parser.Statement) (bool, bool) {
	typedStatement, ok := statement.(*parser.SwitchStatement)
	if !ok {
		// 当前语句不是 switch 扩展节点。
		return false, false
	}
	if expressionContainsFunction(typedStatement.Expression) {
		// switch 主表达式中的匿名函数需要保守处理。
		return true, true
	}
	for _, switchCase := range typedStatement.Cases {
		for _, value := range switchCase.Values {
			if expressionContainsFunction(value) {
				// case 匹配表达式中的匿名函数需要保守处理。
				return true, true
			}
		}
		if blockContainsFunction(switchCase.Body) {
			// case body 中出现函数时返回 true。
			return true, true
		}
	}
	return blockContainsFunction(typedStatement.DefaultBlock), true
}
