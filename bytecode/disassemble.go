package bytecode

import (
	"fmt"
	"strings"
)

// DisassembleProto 返回 Proto 的可读反汇编文本。
//
// 输出包含函数头、指令列表、常量表、upvalue、局部变量和子 Proto。该格式用于项目内
// 调试与测试失败定位，不承诺与官方 luac -l 文本完全一致。
func DisassembleProto(proto *Proto) string {
	// 使用 strings.Builder 累积文本，避免大量小字符串拼接。
	var builder strings.Builder
	writeProtoDisassembly(&builder, proto, 0, "main")
	return builder.String()
}

// writeProtoDisassembly 递归写入一个 Proto 的反汇编信息。
//
// depth 控制缩进层级，label 用于标识当前原型在父结构中的位置。
func writeProtoDisassembly(builder *strings.Builder, proto *Proto, depth int, label string) {
	// 先输出当前 Proto 的摘要，再输出指令和辅助表。
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(builder, "%s%s <%s:%d,%d> params=%d vararg=%v maxstack=%d\n", indent, label, proto.Source, proto.LineDefined, proto.LastLineDefined, proto.NumParams, proto.IsVararg, proto.MaxStackSize)

	for pc, instruction := range proto.Code {
		// 每条指令按 pc 顺序输出，行号缺失时使用 "-"。
		line := "-"
		if pc < len(proto.LineInfo) {
			// lineinfo 存在对应 pc 时，显示源码行号。
			line = fmt.Sprintf("%d", proto.LineInfo[pc])
		}
		fmt.Fprintf(builder, "%s  [%04d] line=%s %-9s %s\n", indent, pc, line, instruction.OpCode().Name(), formatInstructionOperands(instruction))
	}

	if len(proto.Constants) > 0 {
		// 常量表存在时输出，便于 LOADK 和 RK 参数定位。
		fmt.Fprintf(builder, "%s  constants (%d):\n", indent, len(proto.Constants))
		for constantIndex, constant := range proto.Constants {
			fmt.Fprintf(builder, "%s    [%d] %s\n", indent, constantIndex, formatConstant(constant))
		}
	}

	if len(proto.Upvalues) > 0 {
		// upvalue 表存在时输出捕获来源、索引和 Debug 名称。
		fmt.Fprintf(builder, "%s  upvalues (%d):\n", indent, len(proto.Upvalues))
		for upvalueIndex, upvalue := range proto.Upvalues {
			fmt.Fprintf(builder, "%s    [%d] name=%q instack=%v index=%d\n", indent, upvalueIndex, upvalue.Name, upvalue.InStack, upvalue.Index)
		}
	}

	if len(proto.LocalVars) > 0 {
		// 局部变量表存在时输出生命周期范围，便于 debug.getlocal 定位。
		fmt.Fprintf(builder, "%s  locals (%d):\n", indent, len(proto.LocalVars))
		for localVarIndex, localVar := range proto.LocalVars {
			fmt.Fprintf(builder, "%s    [%d] name=%q pc=[%d,%d)\n", indent, localVarIndex, localVar.Name, localVar.StartPC, localVar.EndPC)
		}
	}

	for childIndex, child := range proto.Protos {
		// 子 Proto 按 CLOSURE Bx 索引顺序递归输出。
		writeProtoDisassembly(builder, child, depth+1, fmt.Sprintf("child[%d]", childIndex))
	}
}

// formatInstructionOperands 按 opcode 模式格式化指令操作数。
//
// 返回值只描述原始字段，不解释运行时语义；运行时语义由 VM 层负责。
func formatInstructionOperands(instruction Instruction) string {
	// 根据 opcode 的基础模式选择字段展示方式，保持与 Lua 5.3 opmode 元数据一致。
	opCode := instruction.OpCode()
	switch opCode.Mode() {
	case ModeABC:
		// iABC 指令展示 A、B、C 三个字段。
		return fmt.Sprintf("A=%d B=%s C=%s", instruction.A(), formatArg(instruction.B(), opCode.BMode()), formatArg(instruction.C(), opCode.CMode()))
	case ModeABx:
		// iABx 指令展示 A 和无符号 Bx。
		return fmt.Sprintf("A=%d Bx=%d", instruction.A(), instruction.Bx())
	case ModeAsBx:
		// iAsBx 指令展示 A 和有符号 sBx。
		return fmt.Sprintf("A=%d sBx=%d", instruction.A(), instruction.SBx())
	case ModeAx:
		// iAx 指令展示 Ax。
		return fmt.Sprintf("Ax=%d", instruction.Ax())
	default:
		// 未知模式理论上不会出现，保留原始字段便于排查损坏元数据。
		return fmt.Sprintf("A=%d B=%d C=%d", instruction.A(), instruction.B(), instruction.C())
	}
}

// formatArg 格式化单个 B/C 操作数字段。
//
// RK 参数会显示寄存器或常量索引，其他参数直接显示数字值。
func formatArg(value int, argMask OpArgMask) string {
	// RK 参数需要根据 BitRK 区分寄存器和常量表索引。
	if argMask == OpArgK {
		// 常量参数显示为 K(index)，便于和常量表对应。
		if IsK(value) {
			return fmt.Sprintf("K(%d)", IndexK(value))
		}
		// 寄存器参数显示为 R(index)，便于和 VM 寄存器窗口对应。
		return fmt.Sprintf("R(%d)", value)
	}

	// 非 RK 参数直接显示原始数值。
	return fmt.Sprintf("%d", value)
}

// formatConstant 格式化 Proto 常量表中的单个常量。
//
// 返回值用于反汇编文本，必须稳定以支持 golden 测试。
func formatConstant(constant Constant) string {
	// 根据常量类型输出 Lua 语义下的值。
	switch constant.Kind {
	case ConstantNil:
		// nil 常量没有额外负载。
		return "nil"
	case ConstantBoolean:
		// boolean 常量直接输出 true 或 false。
		return fmt.Sprintf("boolean(%v)", constant.Bool)
	case ConstantInteger:
		// integer 常量保持十进制整数输出。
		return fmt.Sprintf("integer(%d)", constant.Integer)
	case ConstantNumber:
		// number 常量使用 %g 保持紧凑稳定输出。
		return fmt.Sprintf("number(%g)", constant.Number)
	case ConstantString:
		// string 常量使用 %q 暴露转义字节，便于调试二进制字符串。
		return fmt.Sprintf("string(%q)", constant.String)
	default:
		// 未知常量类型输出 kind 编号，便于定位损坏数据。
		return fmt.Sprintf("unknown(kind=%d)", constant.Kind)
	}
}
