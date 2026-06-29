package bytecode

import "testing"

// TestOpCodeNames 验证 Lua 5.3 opcode 名称表与枚举数量保持一致。
//
// 该测试避免后续新增或调整 opcode 时破坏 binary chunk 编号与反汇编输出。
func TestOpCodeNames(t *testing.T) {
	// 先校验数量，确保名称表覆盖所有 Lua 5.3.6 opcode。
	if len(opCodeNames) != NumOpCodes {
		t.Fatalf("opcode name count mismatch: got %d want %d", len(opCodeNames), NumOpCodes)
	}

	// 校验首尾名称，快速锁定枚举顺序是否整体偏移。
	if OpMove.Name() != "MOVE" || OpExtraArg.Name() != "EXTRAARG" {
		t.Fatalf("opcode boundary names mismatch: first=%s last=%s", OpMove.Name(), OpExtraArg.Name())
	}

	// 越界 opcode 来自损坏 chunk 时应返回 UNKNOWN，而不是造成 panic。
	if OpCode(255).Name() != "UNKNOWN" {
		t.Fatalf("invalid opcode name mismatch: got %s", OpCode(255).Name())
	}
}

// TestOpCodeModes 验证 Lua 5.3 opcode 模式表与关键指令语义对齐。
//
// 该测试覆盖指令格式、B/C 参数类型、A 寄存器写入标记和测试指令标记。
func TestOpCodeModes(t *testing.T) {
	// 模式表必须覆盖所有 opcode，避免后续 VM 或反汇编查询越界。
	if len(opModes) != NumOpCodes {
		t.Fatalf("opcode mode count mismatch: got %d want %d", len(opModes), NumOpCodes)
	}

	// LOADK 必须是 iABx，写 A，B 参数表示常量索引，C 参数未使用。
	if OpLoadK.Mode() != ModeABx || !OpLoadK.SetsA() || OpLoadK.BMode() != OpArgK || OpLoadK.CMode() != OpArgN {
		t.Fatalf("LOADK mode mismatch: mode=%d setA=%v b=%d c=%d", OpLoadK.Mode(), OpLoadK.SetsA(), OpLoadK.BMode(), OpLoadK.CMode())
	}

	// JMP 必须是 iAsBx，不写 A，B 参数表示跳转偏移，C 参数未使用。
	if OpJmp.Mode() != ModeAsBx || OpJmp.SetsA() || OpJmp.BMode() != OpArgR || OpJmp.CMode() != OpArgN {
		t.Fatalf("JMP mode mismatch: mode=%d setA=%v b=%d c=%d", OpJmp.Mode(), OpJmp.SetsA(), OpJmp.BMode(), OpJmp.CMode())
	}

	// EQ 必须被标记为测试指令，且 B/C 都是 RK 参数。
	if !OpEq.IsTest() || OpEq.BMode() != OpArgK || OpEq.CMode() != OpArgK {
		t.Fatalf("EQ mode mismatch: test=%v b=%d c=%d", OpEq.IsTest(), OpEq.BMode(), OpEq.CMode())
	}

	// EXTRAARG 必须是 iAx，承载扩展参数。
	if OpExtraArg.Mode() != ModeAx || OpExtraArg.BMode() != OpArgU || OpExtraArg.CMode() != OpArgU {
		t.Fatalf("EXTRAARG mode mismatch: mode=%d b=%d c=%d", OpExtraArg.Mode(), OpExtraArg.BMode(), OpExtraArg.CMode())
	}
}

// TestInvalidOpCodeMetadata 验证非法 opcode 的元数据查询不会 panic。
//
// chunk loader 读取损坏字节码时会先遇到非法 opcode，元数据查询必须给出保守占位。
func TestInvalidOpCodeMetadata(t *testing.T) {
	invalidOpCode := OpCode(255)

	// 非法 opcode 必须被识别为无效，避免执行层继续分派。
	if invalidOpCode.Valid() {
		t.Fatalf("invalid opcode should not be valid")
	}

	// 非法 opcode 的查询方法必须返回保守零值，不允许 panic。
	if invalidOpCode.Mode() != ModeABC || invalidOpCode.BMode() != OpArgN || invalidOpCode.CMode() != OpArgN || invalidOpCode.SetsA() || invalidOpCode.IsTest() {
		t.Fatalf("invalid opcode metadata mismatch")
	}
}

// TestCreateABCAndDecode 验证 iABC 指令编码与字段解码。
//
// 该测试覆盖 opcode、A、B、C 四个字段，确保字段位置对齐 Lua 5.3 lopcodes.h。
func TestCreateABCAndDecode(t *testing.T) {
	instruction := CreateABC(OpAdd, 7, 258, 129)

	// opcode 字段必须回读为创建时的操作码。
	if instruction.OpCode() != OpAdd {
		t.Fatalf("opcode mismatch: got %v want %v", instruction.OpCode(), OpAdd)
	}

	// A/B/C 字段必须分别回读，证明三个字段没有互相覆盖。
	if instruction.A() != 7 || instruction.B() != 258 || instruction.C() != 129 {
		t.Fatalf("ABC mismatch: got A=%d B=%d C=%d", instruction.A(), instruction.B(), instruction.C())
	}
}

// TestCreateABxAndDecode 验证 iABx 指令编码与 Bx 解码。
//
// LOADK 和 CLOSURE 依赖 Bx 保存常量或子 Proto 索引，字段错误会直接导致脚本加载错误。
func TestCreateABxAndDecode(t *testing.T) {
	instruction := CreateABx(OpLoadK, 3, 12345)

	// opcode、A、Bx 必须按 iABx 格式回读。
	if instruction.OpCode() != OpLoadK || instruction.A() != 3 || instruction.Bx() != 12345 {
		t.Fatalf("ABx mismatch: op=%s A=%d Bx=%d", instruction.OpCode().Name(), instruction.A(), instruction.Bx())
	}
}

// TestCreateAsBxAndDecode 验证 iAsBx 指令的 excess-K 有符号偏移编码。
//
// JMP 和循环指令依赖 sBx 表示前后跳转，偏移错误会破坏控制流。
func TestCreateAsBxAndDecode(t *testing.T) {
	instruction := CreateAsBx(OpJmp, 1, -42)

	// sBx 必须能从 excess-K 表示法还原为原始有符号偏移。
	if instruction.OpCode() != OpJmp || instruction.A() != 1 || instruction.SBx() != -42 {
		t.Fatalf("AsBx mismatch: op=%s A=%d sBx=%d", instruction.OpCode().Name(), instruction.A(), instruction.SBx())
	}
}

// TestCreateAxAndDecode 验证 iAx 指令编码与 Ax 解码。
//
// EXTRAARG 使用 Ax 承载扩展常量索引，必须保留完整 26 位宽度。
func TestCreateAxAndDecode(t *testing.T) {
	instruction := CreateAx(OpExtraArg, 0x1fffff)

	// Ax 必须按 26 位字段回读，opcode 必须保留在低 6 位。
	if instruction.OpCode() != OpExtraArg || instruction.Ax() != 0x1fffff {
		t.Fatalf("Ax mismatch: op=%s Ax=%d", instruction.OpCode().Name(), instruction.Ax())
	}
}

// TestRKHelpers 验证 RK 常量标记、索引清理和常量编码。
//
// RK 编码会被大量算术、比较、表访问指令复用，必须先固定基础语义。
func TestRKHelpers(t *testing.T) {
	constantRK := RKAsK(17)

	// 常量 RK 必须设置 BitRK，并能还原原始常量索引。
	if !IsK(constantRK) || IndexK(constantRK) != 17 {
		t.Fatalf("constant RK mismatch: rk=%d isK=%v index=%d", constantRK, IsK(constantRK), IndexK(constantRK))
	}

	// 寄存器 RK 不应设置 BitRK，IndexK 应保持寄存器索引不变。
	if IsK(17) || IndexK(17) != 17 {
		t.Fatalf("register RK mismatch: isK=%v index=%d", IsK(17), IndexK(17))
	}
}
