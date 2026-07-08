package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/bytecode"
)

// TestVMOpcodeUnitCoverageMatrix 验证每条 Lua 5.3 opcode 都有对应单元测试覆盖说明。
//
// 该测试不是替代行为测试，而是防止后续新增或重排 opcode 时遗漏 VM 单测映射；map 值记录
// 覆盖该 opcode 的测试函数名或测试组名。
func TestVMOpcodeUnitCoverageMatrix(t *testing.T) {
	// coverageByOpcode 逐条列出 Lua 5.3 opcode 与当前行为测试的对应关系。
	coverageByOpcode := map[bytecode.OpCode]string{
		bytecode.OpMove:     "TestVMMoveCopiesRegister/TestVMMovePreservesReferenceIdentity/TestVMMoveOutOfRange",
		bytecode.OpLoadK:    "TestVMLoadKLoadsConstants/TestVMLoadKCopiesConstantSlice/TestVMLoadKOutOfRange",
		bytecode.OpLoadKX:   "TestVMLoadKXWithExtraArg/TestVMLoadKXRequiresExtraArg",
		bytecode.OpLoadBool: "TestVMLoadBool",
		bytecode.OpLoadNil:  "TestVMLoadNil/TestVMLoadNilOutOfRange",
		bytecode.OpGetUpval: "TestVMGetAndSetUpvalue/TestVMUpvalueOutOfRange",
		bytecode.OpGetTabUp: "TestVMGetTabUpAndSetTabUp/TestVMGetTabUpAndSetTabUpErrors",
		bytecode.OpGetTable: "TestVMGetTable/TestVMTableInstructionErrors",
		bytecode.OpSetTabUp: "TestVMGetTabUpAndSetTabUp/TestVMGetTabUpAndSetTabUpErrors",
		bytecode.OpSetupVal: "TestVMGetAndSetUpvalue/TestVMUpvalueOutOfRange",
		bytecode.OpSetTable: "TestVMSetTable/TestVMTableInstructionErrors",
		bytecode.OpNewTable: "TestVMNewTable",
		bytecode.OpSelf:     "TestVMSelf/TestVMSelfErrors",
		bytecode.OpAdd:      "TestVMBinaryArithmeticInstructions/TestVMBinaryArithmeticErrors",
		bytecode.OpSub:      "TestVMBinaryArithmeticInstructions",
		bytecode.OpMul:      "TestVMBinaryArithmeticInstructions",
		bytecode.OpMod:      "TestVMBinaryArithmeticInstructions/TestVMBinaryArithmeticErrors",
		bytecode.OpPow:      "TestVMBinaryArithmeticInstructions",
		bytecode.OpDiv:      "TestVMBinaryArithmeticInstructions",
		bytecode.OpIDiv:     "TestVMBinaryArithmeticInstructions/TestVMBinaryArithmeticErrors",
		bytecode.OpBAnd:     "TestVMBinaryBitwiseInstructions/TestVMBinaryBitwiseErrors",
		bytecode.OpBOr:      "TestVMBinaryBitwiseInstructions",
		bytecode.OpBXor:     "TestVMBinaryBitwiseInstructions",
		bytecode.OpShl:      "TestVMBinaryBitwiseInstructions",
		bytecode.OpShr:      "TestVMBinaryBitwiseInstructions",
		bytecode.OpUnm:      "TestVMUnaryInstructions/TestVMUnaryErrors",
		bytecode.OpBNot:     "TestVMUnaryInstructions/TestVMUnaryErrors",
		bytecode.OpNot:      "TestVMUnaryInstructions",
		bytecode.OpLen:      "TestVMLength/TestVMLengthErrors",
		bytecode.OpConcat:   "TestVMConcat/TestVMConcatErrors",
		bytecode.OpJmp:      "TestVMJump",
		bytecode.OpEq:       "TestVMComparisonInstructions",
		bytecode.OpLt:       "TestVMComparisonInstructions/TestVMComparisonErrors",
		bytecode.OpLe:       "TestVMComparisonInstructions",
		bytecode.OpTest:     "TestVMTestAndTestSet",
		bytecode.OpTestSet:  "TestVMTestAndTestSet",
		bytecode.OpCall:     "TestVMCallTailCallAndTForCall",
		bytecode.OpTailCall: "TestVMCallTailCallAndTForCall",
		bytecode.OpReturn:   "TestVMReturn",
		bytecode.OpForLoop:  "TestVMForPrepAndForLoop",
		bytecode.OpForPrep:  "TestVMForPrepAndForLoop",
		bytecode.OpTForCall: "TestVMCallTailCallAndTForCall",
		bytecode.OpTForLoop: "TestVMTForLoop",
		bytecode.OpSetList:  "TestVMSetList",
		bytecode.OpClosure:  "TestVMClosureAndVararg",
		bytecode.OpVararg:   "TestVMClosureAndVararg",
		bytecode.OpExtraArg: "TestVMLoadKXWithExtraArg/TestVMUnexpectedExtraArg/TestVMSetList",
	}

	if len(coverageByOpcode) != bytecode.NumOpCodes {
		// 覆盖矩阵数量不等于 opcode 数量时，说明存在重复或遗漏。
		t.Fatalf("opcode coverage count mismatch: got=%d want=%d", len(coverageByOpcode), bytecode.NumOpCodes)
	}
	for opIndex := 0; opIndex < bytecode.NumOpCodes; opIndex++ {
		// 按 opcode 数字顺序检查，避免只依赖 map 长度掩盖遗漏项。
		opCode := bytecode.OpCode(opIndex)
		testNames, ok := coverageByOpcode[opCode]
		if !ok {
			// 任一 opcode 没有覆盖说明都必须失败。
			t.Fatalf("opcode %s has no unit coverage mapping", opCode.Name())
		}
		if strings.TrimSpace(testNames) == "" {
			// 覆盖说明为空没有可维护价值，必须失败。
			t.Fatalf("opcode %s has empty unit coverage mapping", opCode.Name())
		}
	}
}

// TestVMOpcodeCombinationGolden 验证多条 VM opcode 组合执行后的稳定状态。
//
// 该 golden 用例用手写指令串覆盖常量加载、算术、table 写读、拼接、长度和比较跳转标记，
// 用于捕捉单条 opcode 测试不容易暴露的组合状态回归。
func TestVMOpcodeCombinationGolden(t *testing.T) {
	vm := NewVMWithConstants(8, []bytecode.Constant{
		bytecode.IntegerConstant(2),
		bytecode.IntegerConstant(3),
		bytecode.StringConstant("sum"),
		bytecode.StringConstant("="),
	})

	instructions := []bytecode.Instruction{
		bytecode.CreateABx(bytecode.OpLoadK, 0, 0),
		bytecode.CreateABx(bytecode.OpLoadK, 1, 1),
		bytecode.CreateABC(bytecode.OpAdd, 2, 0, 1),
		bytecode.CreateABC(bytecode.OpNewTable, 3, 0, 0),
		bytecode.CreateABC(bytecode.OpSetTable, 3, bytecode.RKAsK(2), 2),
		bytecode.CreateABC(bytecode.OpGetTable, 4, 3, bytecode.RKAsK(2)),
		bytecode.CreateABx(bytecode.OpLoadK, 5, 3),
		bytecode.CreateABC(bytecode.OpMove, 6, 4, 0),
		bytecode.CreateABC(bytecode.OpConcat, 6, 5, 6),
		bytecode.CreateABC(bytecode.OpLen, 7, 6, 0),
		bytecode.CreateABC(bytecode.OpEq, 1, 4, 2),
	}
	for instructionIndex, instruction := range instructions {
		// 逐条执行手写指令串，任何一步失败都带上指令下标便于定位。
		if err := vm.Step(instruction); err != nil {
			t.Fatalf("step %d failed: %v", instructionIndex, err)
		}
	}

	got := vmGoldenState(vm)
	goldenPath := filepath.Join("..", "tests", "golden", "vm_opcode_sequence.golden")
	expectedBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		// golden 文件缺失或不可读时，测试资产不完整。
		t.Fatalf("read golden failed: %v", err)
	}
	expected := strings.TrimSpace(strings.ReplaceAll(string(expectedBytes), "\r\n", "\n"))
	if got != expected {
		// 组合执行状态必须与 golden 完全一致。
		t.Fatalf("golden mismatch:\n got:\n%s\nwant:\n%s", got, expected)
	}
}

// vmGoldenState 返回组合 opcode golden 需要比对的 VM 状态文本。
//
// 返回内容必须稳定，不包含 Go 指针地址或 map 遍历顺序。
func vmGoldenState(vm *VM) string {
	// 逐个读取固定寄存器，输出 DebugString 形式以保持文本稳定。
	var builder strings.Builder
	for registerIndex := 0; registerIndex <= 7; registerIndex++ {
		// Register 越界在该测试中不应发生，若发生也输出 nil 便于定位。
		value, ok := vm.Register(registerIndex)
		if !ok {
			value = NilValue()
		}
		builder.WriteString("R")
		builder.WriteString(strconv.Itoa(registerIndex))
		builder.WriteString("=")
		builder.WriteString(value.DebugString())
		builder.WriteString("\n")
	}
	builder.WriteString("skipNext=")
	if vm.SkipNext() {
		// skipNext 为 true 时输出 true。
		builder.WriteString("true")
	} else {
		// skipNext 为 false 时输出 false。
		builder.WriteString("false")
	}
	return strings.TrimSpace(builder.String())
}
