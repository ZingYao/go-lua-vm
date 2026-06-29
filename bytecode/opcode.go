// Package bytecode 提供 Lua 5.3 字节码指令、函数原型和二进制 chunk 编解码能力。
//
// 本包的 opcode 编号、字段宽度和字段位置对齐 Lua 5.3.6 的 lopcodes.h/lopcodes.c。
// 后续 VM、compiler/codegen、反汇编和 binary chunk load/dump 都应复用这里的定义，
// 避免多个模块重复维护指令布局。
package bytecode

// Instruction 表示 Lua 5.3 VM 的单条 32 位指令。
//
// Lua 5.3 官方实现把 Instruction 视为无符号整数，并在低 6 位存放 opcode。
// Go 实现固定使用 uint32，确保字段移位和掩码运算不受平台 int 宽度影响。
type Instruction uint32

// OpCode 表示 Lua 5.3 的虚拟机操作码。
//
// 枚举顺序必须严格对齐 Lua 5.3.6 lopcodes.h 中的 OpCode 定义，因为 binary chunk
// 保存的是数字 opcode，顺序变化会直接破坏兼容性。
type OpCode uint8

// OpMode 表示 Lua 5.3 指令的基础编码格式。
//
// 该枚举对齐 lopcodes.h 中的 enum OpMode，用于指导反汇编、chunk 校验和
// compiler/codegen 选择对应的字段编码方式。
type OpMode uint8

const (
	// ModeABC 表示指令按 opcode、A、B、C 四段字段编码。
	ModeABC OpMode = iota
	// ModeABx 表示指令按 opcode、A、Bx 三段字段编码。
	ModeABx
	// ModeAsBx 表示指令按 opcode、A、sBx 三段字段编码。
	ModeAsBx
	// ModeAx 表示指令按 opcode、Ax 两段字段编码。
	ModeAx
)

// OpArgMask 表示 Lua 5.3 opcode 元数据中的参数使用方式。
//
// 该枚举对齐 lopcodes.h 中的 enum OpArgMask，用于描述 B/C 字段是未使用、
// 普通参数、寄存器参数，还是 RK 常量/寄存器混合参数。
type OpArgMask uint8

const (
	// OpArgN 表示参数未被当前 opcode 使用。
	OpArgN OpArgMask = iota
	// OpArgU 表示参数被 opcode 使用，但不限定为寄存器或常量。
	OpArgU
	// OpArgR 表示参数是寄存器索引或跳转偏移。
	OpArgR
	// OpArgK 表示参数是 RK 编码，可表示寄存器或常量表索引。
	OpArgK
)

const (
	// OpMove 对齐 OP_MOVE，语义为 R(A) := R(B)。
	OpMove OpCode = iota
	// OpLoadK 对齐 OP_LOADK，语义为 R(A) := Kst(Bx)。
	OpLoadK
	// OpLoadKX 对齐 OP_LOADKX，语义为 R(A) := Kst(extra arg)。
	OpLoadKX
	// OpLoadBool 对齐 OP_LOADBOOL，语义为 R(A) := bool(B)，并在 C 非 0 时跳过下一条指令。
	OpLoadBool
	// OpLoadNil 对齐 OP_LOADNIL，语义为 R(A)..R(A+B) := nil。
	OpLoadNil
	// OpGetUpval 对齐 OP_GETUPVAL，语义为 R(A) := UpValue[B]。
	OpGetUpval
	// OpGetTabUp 对齐 OP_GETTABUP，语义为 R(A) := UpValue[B][RK(C)]。
	OpGetTabUp
	// OpGetTable 对齐 OP_GETTABLE，语义为 R(A) := R(B)[RK(C)]。
	OpGetTable
	// OpSetTabUp 对齐 OP_SETTABUP，语义为 UpValue[A][RK(B)] := RK(C)。
	OpSetTabUp
	// OpSetupVal 对齐 OP_SETUPVAL，语义为 UpValue[B] := R(A)。
	OpSetupVal
	// OpSetTable 对齐 OP_SETTABLE，语义为 R(A)[RK(B)] := RK(C)。
	OpSetTable
	// OpNewTable 对齐 OP_NEWTABLE，语义为 R(A) := {}。
	OpNewTable
	// OpSelf 对齐 OP_SELF，语义为 R(A+1) := R(B); R(A) := R(B)[RK(C)]。
	OpSelf
	// OpAdd 对齐 OP_ADD，语义为 R(A) := RK(B) + RK(C)。
	OpAdd
	// OpSub 对齐 OP_SUB，语义为 R(A) := RK(B) - RK(C)。
	OpSub
	// OpMul 对齐 OP_MUL，语义为 R(A) := RK(B) * RK(C)。
	OpMul
	// OpMod 对齐 OP_MOD，语义为 R(A) := RK(B) % RK(C)。
	OpMod
	// OpPow 对齐 OP_POW，语义为 R(A) := RK(B) ^ RK(C)。
	OpPow
	// OpDiv 对齐 OP_DIV，语义为 R(A) := RK(B) / RK(C)。
	OpDiv
	// OpIDiv 对齐 OP_IDIV，语义为 R(A) := RK(B) // RK(C)。
	OpIDiv
	// OpBAnd 对齐 OP_BAND，语义为 R(A) := RK(B) & RK(C)。
	OpBAnd
	// OpBOr 对齐 OP_BOR，语义为 R(A) := RK(B) | RK(C)。
	OpBOr
	// OpBXor 对齐 OP_BXOR，语义为 R(A) := RK(B) ~ RK(C)。
	OpBXor
	// OpShl 对齐 OP_SHL，语义为 R(A) := RK(B) << RK(C)。
	OpShl
	// OpShr 对齐 OP_SHR，语义为 R(A) := RK(B) >> RK(C)。
	OpShr
	// OpUnm 对齐 OP_UNM，语义为 R(A) := -R(B)。
	OpUnm
	// OpBNot 对齐 OP_BNOT，语义为 R(A) := ~R(B)。
	OpBNot
	// OpNot 对齐 OP_NOT，语义为 R(A) := not R(B)。
	OpNot
	// OpLen 对齐 OP_LEN，语义为 R(A) := #R(B)。
	OpLen
	// OpConcat 对齐 OP_CONCAT，语义为 R(A) := R(B).. ... ..R(C)。
	OpConcat
	// OpJmp 对齐 OP_JMP，语义为 pc += sBx，并按 A 关闭 upvalue。
	OpJmp
	// OpEq 对齐 OP_EQ，语义为条件不匹配 A 时跳过下一条指令。
	OpEq
	// OpLt 对齐 OP_LT，语义为条件不匹配 A 时跳过下一条指令。
	OpLt
	// OpLe 对齐 OP_LE，语义为条件不匹配 A 时跳过下一条指令。
	OpLe
	// OpTest 对齐 OP_TEST，语义为 R(A) 与 C 的布尔期望不匹配时跳过下一条指令。
	OpTest
	// OpTestSet 对齐 OP_TESTSET，语义为条件匹配时赋值，否则跳过下一条指令。
	OpTestSet
	// OpCall 对齐 OP_CALL，语义为普通函数调用。
	OpCall
	// OpTailCall 对齐 OP_TAILCALL，语义为尾调用。
	OpTailCall
	// OpReturn 对齐 OP_RETURN，语义为返回寄存器区间。
	OpReturn
	// OpForLoop 对齐 OP_FORLOOP，语义为数值 for 循环步进与跳转。
	OpForLoop
	// OpForPrep 对齐 OP_FORPREP，语义为数值 for 循环初始化与跳转。
	OpForPrep
	// OpTForCall 对齐 OP_TFORCALL，语义为泛型 for 迭代器调用。
	OpTForCall
	// OpTForLoop 对齐 OP_TFORLOOP，语义为泛型 for 结果判空与跳转。
	OpTForLoop
	// OpSetList 对齐 OP_SETLIST，语义为批量写入 table 数组区。
	OpSetList
	// OpClosure 对齐 OP_CLOSURE，语义为 R(A) := closure(KPROTO[Bx])。
	OpClosure
	// OpVararg 对齐 OP_VARARG，语义为读取 vararg 到寄存器区间。
	OpVararg
	// OpExtraArg 对齐 OP_EXTRAARG，为前一条 LOADKX 或 SETLIST 提供扩展参数。
	OpExtraArg
)

const (
	// SizeC 是 C 操作数字段宽度。
	SizeC = 9
	// SizeB 是 B 操作数字段宽度。
	SizeB = 9
	// SizeBx 是 Bx 操作数字段宽度，由 B 与 C 组合而成。
	SizeBx = SizeC + SizeB
	// SizeA 是 A 操作数字段宽度。
	SizeA = 8
	// SizeAx 是 Ax 操作数字段宽度，由 A、B、C 组合而成。
	SizeAx = SizeC + SizeB + SizeA
	// SizeOp 是 opcode 字段宽度。
	SizeOp = 6
)

const (
	// PosOp 是 opcode 字段起始位。
	PosOp = 0
	// PosA 是 A 字段起始位。
	PosA = PosOp + SizeOp
	// PosC 是 C 字段起始位。
	PosC = PosA + SizeA
	// PosB 是 B 字段起始位。
	PosB = PosC + SizeC
	// PosBx 是 Bx 字段起始位。
	PosBx = PosC
	// PosAx 是 Ax 字段起始位。
	PosAx = PosA
)

const (
	// MaxArgA 是 A 字段最大值。
	MaxArgA = 1<<SizeA - 1
	// MaxArgB 是 B 字段最大值。
	MaxArgB = 1<<SizeB - 1
	// MaxArgC 是 C 字段最大值。
	MaxArgC = 1<<SizeC - 1
	// MaxArgBx 是 Bx 字段最大值。
	MaxArgBx = 1<<SizeBx - 1
	// MaxArgSBx 是 sBx 偏移量最大绝对值，Lua 使用 excess-K 表示有符号参数。
	MaxArgSBx = MaxArgBx >> 1
	// MaxArgAx 是 Ax 字段最大值。
	MaxArgAx = 1<<SizeAx - 1
	// BitRK 是 RK 操作数的常量标记位，置 1 表示常量表索引。
	BitRK = 1 << (SizeB - 1)
	// MaxIndexRK 是 RK 可直接编码的最大常量索引。
	MaxIndexRK = BitRK - 1
	// NoReg 是 Lua 5.3 使用的无效寄存器标记，必须能放入 A 字段。
	NoReg = MaxArgA
	// NumOpCodes 是 Lua 5.3.6 opcode 数量。
	NumOpCodes = int(OpExtraArg) + 1
)

var opCodeNames = [...]string{
	"MOVE", "LOADK", "LOADKX", "LOADBOOL", "LOADNIL", "GETUPVAL",
	"GETTABUP", "GETTABLE", "SETTABUP", "SETUPVAL", "SETTABLE", "NEWTABLE",
	"SELF", "ADD", "SUB", "MUL", "MOD", "POW", "DIV", "IDIV", "BAND",
	"BOR", "BXOR", "SHL", "SHR", "UNM", "BNOT", "NOT", "LEN", "CONCAT",
	"JMP", "EQ", "LT", "LE", "TEST", "TESTSET", "CALL", "TAILCALL",
	"RETURN", "FORLOOP", "FORPREP", "TFORCALL", "TFORLOOP", "SETLIST",
	"CLOSURE", "VARARG", "EXTRAARG",
}

var opModes = [...]uint8{
	opMode(0, 1, OpArgR, OpArgN, ModeABC),  // MOVE
	opMode(0, 1, OpArgK, OpArgN, ModeABx),  // LOADK
	opMode(0, 1, OpArgN, OpArgN, ModeABx),  // LOADKX
	opMode(0, 1, OpArgU, OpArgU, ModeABC),  // LOADBOOL
	opMode(0, 1, OpArgU, OpArgN, ModeABC),  // LOADNIL
	opMode(0, 1, OpArgU, OpArgN, ModeABC),  // GETUPVAL
	opMode(0, 1, OpArgU, OpArgK, ModeABC),  // GETTABUP
	opMode(0, 1, OpArgR, OpArgK, ModeABC),  // GETTABLE
	opMode(0, 0, OpArgK, OpArgK, ModeABC),  // SETTABUP
	opMode(0, 0, OpArgU, OpArgN, ModeABC),  // SETUPVAL
	opMode(0, 0, OpArgK, OpArgK, ModeABC),  // SETTABLE
	opMode(0, 1, OpArgU, OpArgU, ModeABC),  // NEWTABLE
	opMode(0, 1, OpArgR, OpArgK, ModeABC),  // SELF
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // ADD
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // SUB
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // MUL
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // MOD
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // POW
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // DIV
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // IDIV
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // BAND
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // BOR
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // BXOR
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // SHL
	opMode(0, 1, OpArgK, OpArgK, ModeABC),  // SHR
	opMode(0, 1, OpArgR, OpArgN, ModeABC),  // UNM
	opMode(0, 1, OpArgR, OpArgN, ModeABC),  // BNOT
	opMode(0, 1, OpArgR, OpArgN, ModeABC),  // NOT
	opMode(0, 1, OpArgR, OpArgN, ModeABC),  // LEN
	opMode(0, 1, OpArgR, OpArgR, ModeABC),  // CONCAT
	opMode(0, 0, OpArgR, OpArgN, ModeAsBx), // JMP
	opMode(1, 0, OpArgK, OpArgK, ModeABC),  // EQ
	opMode(1, 0, OpArgK, OpArgK, ModeABC),  // LT
	opMode(1, 0, OpArgK, OpArgK, ModeABC),  // LE
	opMode(1, 0, OpArgN, OpArgU, ModeABC),  // TEST
	opMode(1, 1, OpArgR, OpArgU, ModeABC),  // TESTSET
	opMode(0, 1, OpArgU, OpArgU, ModeABC),  // CALL
	opMode(0, 1, OpArgU, OpArgU, ModeABC),  // TAILCALL
	opMode(0, 0, OpArgU, OpArgN, ModeABC),  // RETURN
	opMode(0, 1, OpArgR, OpArgN, ModeAsBx), // FORLOOP
	opMode(0, 1, OpArgR, OpArgN, ModeAsBx), // FORPREP
	opMode(0, 0, OpArgN, OpArgU, ModeABC),  // TFORCALL
	opMode(0, 1, OpArgR, OpArgN, ModeAsBx), // TFORLOOP
	opMode(0, 0, OpArgU, OpArgU, ModeABC),  // SETLIST
	opMode(0, 1, OpArgU, OpArgN, ModeABx),  // CLOSURE
	opMode(0, 1, OpArgU, OpArgN, ModeABC),  // VARARG
	opMode(0, 0, OpArgU, OpArgU, ModeAx),   // EXTRAARG
}

// OpCode 返回指令低 6 位存放的 Lua 5.3 opcode。
//
// 本方法不校验 opcode 是否小于 NumOpCodes，调用方在加载不可信 binary chunk 时
// 应在 chunk 校验阶段单独检查 opcode 合法性。
func (instruction Instruction) OpCode() OpCode {
	// 直接按 Lua 5.3 字段布局取低 6 位，保持与 GET_OPCODE 宏一致。
	return OpCode((uint32(instruction) >> PosOp) & mask(SizeOp))
}

// A 返回指令的 A 操作数字段。
//
// A 字段宽度固定为 8 位，通常表示目标寄存器或关闭 upvalue 的寄存器边界。
func (instruction Instruction) A() int {
	// 直接取 A 字段，调用方根据 opcode 决定该字段的业务语义。
	return int((uint32(instruction) >> PosA) & mask(SizeA))
}

// B 返回指令的 B 操作数字段。
//
// B 字段宽度固定为 9 位，可能表示寄存器、常量 RK、无符号数量或跳转辅助参数。
func (instruction Instruction) B() int {
	// 直接取 B 字段，保持与 Lua 5.3 GETARG_B 宏一致。
	return int((uint32(instruction) >> PosB) & mask(SizeB))
}

// C 返回指令的 C 操作数字段。
//
// C 字段宽度固定为 9 位，可能表示寄存器、常量 RK、无符号数量或布尔标记。
func (instruction Instruction) C() int {
	// 直接取 C 字段，保持与 Lua 5.3 GETARG_C 宏一致。
	return int((uint32(instruction) >> PosC) & mask(SizeC))
}

// Bx 返回由 B 与 C 拼接成的无符号 Bx 字段。
//
// Bx 字段用于常量表索引或子 Proto 索引，宽度为 18 位。
func (instruction Instruction) Bx() int {
	// 从 C 起始位读取 18 位，保持与 Lua 5.3 GETARG_Bx 宏一致。
	return int((uint32(instruction) >> PosBx) & mask(SizeBx))
}

// SBx 返回由 Bx 解码得到的有符号 sBx 字段。
//
// Lua 5.3 使用 excess-K 表示有符号跳转偏移，真实值等于 Bx - MaxArgSBx。
func (instruction Instruction) SBx() int {
	// 将无符号 Bx 转回有符号偏移，供 JMP、FORLOOP、FORPREP、TFORLOOP 使用。
	return instruction.Bx() - MaxArgSBx
}

// Ax 返回由 A、B、C 拼接成的无符号 Ax 字段。
//
// Ax 字段用于 EXTRAARG，宽度为 26 位。
func (instruction Instruction) Ax() int {
	// 从 A 起始位读取 26 位，保持与 Lua 5.3 GETARG_Ax 宏一致。
	return int((uint32(instruction) >> PosAx) & mask(SizeAx))
}

// Name 返回 opcode 的 Lua 5.3 官方名称。
//
// opcode 越界时返回 "UNKNOWN"，便于 binary chunk 校验或反汇编错误展示，不在此处 panic。
func (opCode OpCode) Name() string {
	// 越界 opcode 可能来自损坏 chunk，返回 UNKNOWN 让上层产生结构化错误。
	if int(opCode) >= len(opCodeNames) {
		return "UNKNOWN"
	}

	// 合法 opcode 直接返回与 lopcodes.c 中 luaP_opnames 对齐的名称。
	return opCodeNames[opCode]
}

// Valid 判断 opcode 是否属于 Lua 5.3.6 定义范围。
//
// binary chunk loader 读取不可信字节码时应先调用本方法，避免后续按非法 opcode
// 查询模式表或执行 VM 分派。
func (opCode OpCode) Valid() bool {
	// 只接受名称表覆盖的 opcode，名称表长度与 opmodes 长度由单测保持一致。
	return int(opCode) < len(opCodeNames)
}

// Mode 返回 opcode 的基础指令格式。
//
// opcode 非法时返回 ModeABC 作为零值占位；调用方需要通过 Valid 区分该返回值是否可信。
func (opCode OpCode) Mode() OpMode {
	// 非法 opcode 不能访问 opmodes 表，返回零值避免 chunk 校验阶段 panic。
	if !opCode.Valid() {
		return ModeABC
	}

	// 合法 opcode 低 2 位保存基础指令格式，保持与 getOpMode 宏一致。
	return OpMode(opModes[opCode] & 3)
}

// BMode 返回 opcode 的 B 参数使用方式。
//
// 该元数据对齐 Lua 5.3 luaP_opmodes，可用于 codegen、chunk 校验和反汇编输出。
func (opCode OpCode) BMode() OpArgMask {
	// 非法 opcode 不能访问 opmodes 表，返回未使用参数作为保守占位。
	if !opCode.Valid() {
		return OpArgN
	}

	// B 参数模式位于 opmode 字节的第 4-5 位，保持与 getBMode 宏一致。
	return OpArgMask((opModes[opCode] >> 4) & 3)
}

// CMode 返回 opcode 的 C 参数使用方式。
//
// 该元数据对齐 Lua 5.3 luaP_opmodes，可用于识别 C 字段是否为 RK 参数。
func (opCode OpCode) CMode() OpArgMask {
	// 非法 opcode 不能访问 opmodes 表，返回未使用参数作为保守占位。
	if !opCode.Valid() {
		return OpArgN
	}

	// C 参数模式位于 opmode 字节的第 2-3 位，保持与 getCMode 宏一致。
	return OpArgMask((opModes[opCode] >> 2) & 3)
}

// SetsA 判断 opcode 是否会设置 A 寄存器。
//
// Lua 5.3 使用 opmode bit 6 表示该指令会写入 A 字段指向的寄存器。
func (opCode OpCode) SetsA() bool {
	// 非法 opcode 不应被视为会写寄存器，避免校验阶段产生错误副作用。
	if !opCode.Valid() {
		return false
	}

	// bit 6 为 1 表示会写 A 寄存器，保持与 testAMode 宏一致。
	return opModes[opCode]&(1<<6) != 0
}

// IsTest 判断 opcode 是否是测试指令。
//
// Lua 5.3 使用 opmode bit 7 标记测试指令，测试指令后续通常要求下一条是跳转。
func (opCode OpCode) IsTest() bool {
	// 非法 opcode 不应被视为测试指令，避免 chunk 校验误判控制流结构。
	if !opCode.Valid() {
		return false
	}

	// bit 7 为 1 表示测试指令，保持与 testTMode 宏一致。
	return opModes[opCode]&(1<<7) != 0
}

// CreateABC 按 iABC 格式创建 Lua 5.3 指令。
//
// 入参必须已经满足对应字段宽度，函数只做位布局编码，不做范围校验；编译器和
// chunk loader 应在更靠近错误上下文的位置生成诊断。
func CreateABC(opCode OpCode, a int, b int, c int) Instruction {
	// 按 opcode、A、B、C 的固定位置组合字段，保持与 CREATE_ABC 宏一致。
	return Instruction(uint32(opCode)<<PosOp | uint32(a)<<PosA | uint32(b)<<PosB | uint32(c)<<PosC)
}

// CreateABx 按 iABx 格式创建 Lua 5.3 指令。
//
// Bx 会占用 B 与 C 的连续 18 位，适用于 LOADK、CLOSURE 等指令。
func CreateABx(opCode OpCode, a int, bx int) Instruction {
	// 按 opcode、A、Bx 的固定位置组合字段，保持与 CREATE_ABx 宏一致。
	return Instruction(uint32(opCode)<<PosOp | uint32(a)<<PosA | uint32(bx)<<PosBx)
}

// CreateAsBx 按 iAsBx 格式创建 Lua 5.3 指令。
//
// sBx 使用 Lua 5.3 excess-K 表示法编码，适用于 JMP 和 for 循环跳转。
func CreateAsBx(opCode OpCode, a int, sbx int) Instruction {
	// 将有符号偏移转换为 Bx 存储形式，保持与 SETARG_sBx 宏一致。
	return CreateABx(opCode, a, sbx+MaxArgSBx)
}

// CreateAx 按 iAx 格式创建 Lua 5.3 指令。
//
// Ax 会占用 A、B、C 的连续 26 位，当前主要用于 EXTRAARG。
func CreateAx(opCode OpCode, ax int) Instruction {
	// 按 opcode 与 Ax 的固定位置组合字段，保持与 CREATE_Ax 宏一致。
	return Instruction(uint32(opCode)<<PosOp | uint32(ax)<<PosAx)
}

// IsK 判断 RK 操作数是否表示常量表索引。
//
// Lua 5.3 在 9 位 RK 字段最高位写入 BitRK，1 表示常量表，0 表示寄存器。
func IsK(rk int) bool {
	// 只检查 RK 最高标记位，调用方负责确保 rk 来源于 B/C 操作数字段。
	return rk&BitRK != 0
}

// IndexK 返回 RK 操作数去掉常量标记后的索引。
//
// 当 RK 表示常量时返回常量表索引；当 RK 表示寄存器时返回寄存器索引本身。
func IndexK(rk int) int {
	// 清除 BitRK 标记位，保持与 Lua 5.3 INDEXK 宏一致。
	return rk &^ BitRK
}

// RKAsK 将常量表索引编码为 RK 操作数。
//
// 入参必须不超过 MaxIndexRK，否则会覆盖 RK 字段宽度之外的语义位。
func RKAsK(index int) int {
	// 设置 BitRK 标记位，保持与 Lua 5.3 RKASK 宏一致。
	return index | BitRK
}

// mask 返回从低位开始的 n 位掩码。
//
// n 必须小于 32；本包仅用它处理 Lua 5.3 固定字段宽度，不接受外部输入。
func mask(bitCount uint) uint32 {
	// 使用 uint32 全 1 右移生成低位掩码，避免依赖平台 int 宽度。
	return ^uint32(0) >> (32 - bitCount)
}

// opMode 按 Lua 5.3 的 opmode 字节布局编码 opcode 元数据。
//
// 参数 test 与 setA 使用 0/1 数字，是为了直接对齐 lopcodes.c 中的 opmode 宏；
// 调用点均为静态表初始化，不接受运行时外部输入。
func opMode(test int, setA int, bMode OpArgMask, cMode OpArgMask, mode OpMode) uint8 {
	// 按 bits 0-1、2-3、4-5、6、7 的布局组合，保持与 C 宏 opmode 一致。
	return uint8(test<<7) | uint8(setA<<6) | uint8(bMode<<4) | uint8(cMode<<2) | uint8(mode)
}
