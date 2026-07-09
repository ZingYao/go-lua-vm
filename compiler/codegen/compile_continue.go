//go:build !lua53 && (with_continue || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package codegen

import (
	"fmt"

	"github.com/ZingYao/go-lua-vm/compiler/parser"
)

func init() {
	// 注册 continue 扩展语句编译入口。
	registerExtensionStatementCompiler(compileContinueExtensionStatement)
}

// compileContinueExtensionStatement 尝试编译 continue 扩展语句。
func compileContinueExtensionStatement(generator *generator, statement parser.Statement) (bool, error) {
	if _, ok := statement.(*parser.ContinueStatement); !ok {
		// 当前语句不是 continue 扩展节点。
		return false, nil
	}
	return true, generator.compileContinueStatement()
}

// compileContinueStatement 编译扩展 continue 语句。
//
// continue 只能出现在循环体内；当前生成一条待回填 JMP，由各循环在续迭代位置确定后统一回填。
func (generator *generator) compileContinueStatement() error {
	if len(generator.continueJumps) == 0 {
		// parser 语义通常会拦截循环外 continue；这里保留防御式错误。
		return fmt.Errorf("codegen continue outside loop")
	}

	continuePC := generator.emitJump(generator.currentLoopCloseRegister())
	lastLoopIndex := len(generator.continueJumps) - 1
	generator.continueJumps[lastLoopIndex] = append(generator.continueJumps[lastLoopIndex], continuePC)
	return nil
}
