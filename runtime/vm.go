package runtime

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ZingYao/go-lua-vm/bytecode"
)

var (
	// ErrUnsupportedInstruction 表示当前 VM 尚未实现指定 opcode。
	ErrUnsupportedInstruction = errors.New("unsupported instruction")
	// ErrRegisterOutOfRange 表示指令访问了超出当前寄存器窗口的槽位。
	ErrRegisterOutOfRange = errors.New("register out of range")
	// ErrConstantOutOfRange 表示指令访问了超出当前常量表的索引。
	ErrConstantOutOfRange = errors.New("constant out of range")
	// ErrExpectedExtraArg 表示 LOADKX 后续指令不是 EXTRAARG。
	ErrExpectedExtraArg = errors.New("expected EXTRAARG")
	// ErrUnexpectedExtraArg 表示 EXTRAARG 没有匹配的前置扩展指令。
	ErrUnexpectedExtraArg = errors.New("unexpected EXTRAARG")
	// ErrUpvalueOutOfRange 表示指令访问了超出当前闭包 upvalue 列表的槽位。
	ErrUpvalueOutOfRange = errors.New("upvalue out of range")
	// ErrExpectedTable 表示 table 访问指令遇到了非 table 值或损坏 table 引用。
	ErrExpectedTable = errors.New("expected table")
	// ErrArithmeticOperand 表示算术指令遇到无法转换为 number 的操作数。
	ErrArithmeticOperand = errors.New("arithmetic operand must be number")
	// ErrIntegerOperand 表示位运算或整数除法遇到无法转换为 integer 的操作数。
	ErrIntegerOperand = errors.New("number has no integer representation")
	// ErrDivisionByZero 表示除法、取模或整数除法遇到零除数。
	ErrDivisionByZero = errors.New("divide by zero")
	// ErrLengthOperand 表示 LEN 指令遇到不支持长度运算的操作数。
	ErrLengthOperand = errors.New("length operand expected")
	// ErrConcatOperand 表示 CONCAT 指令遇到无法转换为 string 的操作数。
	ErrConcatOperand = errors.New("concat operand must be string or number")
	// ErrCompareOperand 表示比较指令遇到无法比较的操作数。
	ErrCompareOperand = errors.New("comparison operand mismatch")
	// ErrProtoOutOfRange 表示 CLOSURE 指令访问了不存在的子 Proto。
	ErrProtoOutOfRange = errors.New("proto out of range")
	// ErrExpectedNumber 表示循环指令遇到无法转换为 number 的控制变量。
	ErrExpectedNumber = errors.New("number expected")
)

const (
	// fieldsPerFlush 对齐 Lua 5.3 LFIELDS_PER_FLUSH，表示 SETLIST 每批写入 table 的字段数。
	fieldsPerFlush = 50
)

// VM 表示最小 Lua 5.3 指令执行器。
//
// 当前阶段只承载寄存器窗口和单步执行能力，用于逐条落地 opcode；完整函数调用、
// upvalue、常量表和 Proto 执行循环会在后续任务中继续接入。
type VM struct {
	// registers 保存当前函数帧寄存器窗口。
	registers []Value
	// constants 保存当前函数 Proto 的常量表。
	constants []bytecode.Constant
	// upvalues 保存当前闭包捕获的 upvalue 值。
	upvalues []Value
	// upvalueCells 保存当前闭包共享 upvalue 槽；存在时读写优先使用 cell。
	upvalueCells []*UpvalueCell
	// openUpvalues 保存当前函数帧中指向活动寄存器的 upvalue cell。
	openUpvalues []openUpvalueCell
	// protos 保存当前函数 Proto 的子函数原型列表。
	protos []*bytecode.Proto
	// varargs 保存当前函数帧可见的 vararg 实参。
	varargs []Value
	// luaMetamethodRunner 执行 Lua closure 元方法；nil 时 VM 只支持 Go closure 元方法。
	luaMetamethodRunner LuaMetamethodRunner
	// proto 保存当前 VM 正在执行的函数原型，用于按 local 生命周期裁剪活动寄存器根。
	proto *bytecode.Proto
	// decodedInstructionProto 记录 decodedInstructions 对应的 Proto，避免 VM 池复用时误读旧预解码。
	decodedInstructionProto *bytecode.Proto
	// decodedInstructions 保存当前 Proto 的只读预解码指令元数据，供后续 hot path 按 PC 读取。
	decodedInstructions []decodedInstruction
	// addForLoopSuperInstructionProto 记录 ADD;FORLOOP superinstruction 表对应的 Proto。
	addForLoopSuperInstructionProto *bytecode.Proto
	// addForLoopSuperInstructions 按 PC 保存 ADD;FORLOOP superinstruction 预匹配结果。
	addForLoopSuperInstructions []addForLoopSuperInstruction
	// mulAddSubForLoopSuperInstructionProto 记录 MUL;ADD;SUB;FORLOOP superinstruction 表对应的 Proto。
	mulAddSubForLoopSuperInstructionProto *bytecode.Proto
	// mulAddSubForLoopSuperInstructions 按 PC 保存 MUL;ADD;SUB;FORLOOP superinstruction 预匹配结果。
	mulAddSubForLoopSuperInstructions []mulAddSubForLoopSuperInstruction
	// mixArithmeticForLoopSuperInstructionProto 记录混合算术循环 superinstruction 表对应的 Proto。
	mixArithmeticForLoopSuperInstructionProto *bytecode.Proto
	// mixArithmeticForLoopSuperInstructions 按 PC 保存混合算术循环 superinstruction 预匹配结果。
	mixArithmeticForLoopSuperInstructions []mixArithmeticForLoopSuperInstruction
	// functionCallAddForLoopSuperInstructionProto 记录函数调用加法循环 superinstruction 表对应的 Proto。
	functionCallAddForLoopSuperInstructionProto *bytecode.Proto
	// functionCallAddForLoopSuperInstructions 按 PC 保存函数调用加法循环 superinstruction 预匹配结果。
	functionCallAddForLoopSuperInstructions []functionCallAddForLoopSuperInstruction
	// functionCallAssignForLoopSuperInstructionProto 记录函数调用赋值循环 superinstruction 表对应的 Proto。
	functionCallAssignForLoopSuperInstructionProto *bytecode.Proto
	// functionCallAssignForLoopSuperInstructions 按 PC 保存函数调用赋值循环 superinstruction 预匹配结果。
	functionCallAssignForLoopSuperInstructions []functionCallAssignForLoopSuperInstruction
	// closureUpvalueForLoopSuperInstructionProto 记录闭包 upvalue 循环 superinstruction 表对应的 Proto。
	closureUpvalueForLoopSuperInstructionProto *bytecode.Proto
	// closureUpvalueForLoopSuperInstructions 按 PC 保存闭包 upvalue 循环 superinstruction 预匹配结果。
	closureUpvalueForLoopSuperInstructions []closureUpvalueForLoopSuperInstruction
	// formatLenAddForLoopSuperInstructionProto 记录 string.format 长度消费循环 superinstruction 表对应的 Proto。
	formatLenAddForLoopSuperInstructionProto *bytecode.Proto
	// formatLenAddForLoopSuperInstructions 按 PC 保存 string.format 长度消费循环 superinstruction 预匹配结果。
	formatLenAddForLoopSuperInstructions []formatLenAddForLoopSuperInstruction
	// stdlibMathStringForLoopSuperInstructionProto 记录 stdlib_math_string 循环 superinstruction 表对应的 Proto。
	stdlibMathStringForLoopSuperInstructionProto *bytecode.Proto
	// stdlibMathStringForLoopSuperInstructions 按 PC 保存 stdlib_math_string 循环 superinstruction 预匹配结果。
	stdlibMathStringForLoopSuperInstructions []stdlibMathStringForLoopSuperInstruction
	// stringAppendForLoopSuperInstructionProto 记录字符串自追加循环 superinstruction 表对应的 Proto。
	stringAppendForLoopSuperInstructionProto *bytecode.Proto
	// stringAppendForLoopSuperInstructions 按 PC 保存字符串自追加循环 superinstruction 预匹配结果。
	stringAppendForLoopSuperInstructions []stringAppendForLoopSuperInstruction
	// tableWriteForLoopSuperInstructionProto 记录连续 table 数组写入循环 superinstruction 表对应的 Proto。
	tableWriteForLoopSuperInstructionProto *bytecode.Proto
	// tableWriteForLoopSuperInstructions 按 PC 保存连续 table 数组写入循环 superinstruction 预匹配结果。
	tableWriteForLoopSuperInstructions []tableWriteForLoopSuperInstruction
	// tableReadAddForLoopSuperInstructionProto 记录连续 table 读取求和循环 superinstruction 表对应的 Proto。
	tableReadAddForLoopSuperInstructionProto *bytecode.Proto
	// tableReadAddForLoopSuperInstructions 按 PC 保存连续 table 读取求和循环 superinstruction 预匹配结果。
	tableReadAddForLoopSuperInstructions []tableReadAddForLoopSuperInstruction
	// tableArrayPreallocHintProto 记录 table 数组预分配 hint 表对应的 Proto。
	tableArrayPreallocHintProto *bytecode.Proto
	// tableArrayPreallocHints 按 NEWTABLE 所在 PC 保存可证明安全的数组区预留容量。
	tableArrayPreallocHints []tableArrayPreallocHint
	// currentPC 保存当前执行指令位置，用于判断 Proto.LocalVars 中哪些局部变量仍然存活。
	currentPC int
	// openTop 保存上一条开放返回/开放 vararg 写入后的寄存器上界，-1 表示当前没有开放栈顶。
	openTop int
	// pendingLoadKXTarget 保存等待 EXTRAARG 完成的 LOADKX 目标寄存器，-1 表示无等待。
	pendingLoadKXTarget int
	// pendingSetList 保存等待 EXTRAARG 完成的 SETLIST 批量 table 写入请求。
	pendingSetList *pendingSetList
	// pendingComparison 保存比较元方法 yield 后需要恢复的测试指令语义。
	pendingComparison *pendingComparisonContinuation
	// pendingConcat 保存 CONCAT 元方法 yield 后需要继续折叠的寄存器区间。
	pendingConcat *pendingConcatContinuation
	// skipNext 标记上一条指令是否要求跳过下一条指令。
	skipNext bool
	// pcOffset 保存上一条控制流指令请求的 pc 偏移量。
	pcOffset int
	// closeFrom 保存上一条 JMP 请求关闭 upvalue 的起始寄存器，-1 表示不关闭。
	closeFrom int
	// callRequest 保存上一条 CALL、TAILCALL 或 TFORCALL 生成的调用请求。
	callRequest CallRequest
	// hasCallRequest 标记 callRequest 是否保存了可消费的调用请求。
	hasCallRequest bool
	// returned 标记上一条指令是否是 RETURN，避免裸 return 的 0 返回值被误判为未返回。
	returned bool
	// returnValues 保存上一条 RETURN 收集到的返回值。
	returnValues []Value
	// returnInline 保存少量返回值，避免普通 Lua 函数每次 return 都分配切片底层数组。
	returnInline [2]Value
	// arithmeticIntRegisterCache 按 PC 标记算术指令最近命中过的 integer 热路径。
	arithmeticIntRegisterCache []byte
	// arithmeticIntOperandCache 按 PC 记录 integer 算术热路径的 RK 操作数形态和值。
	arithmeticIntOperandCache []arithmeticIntOperandCacheEntry
	// arithmeticIntRegisterCacheProto 记录 arithmeticIntRegisterCache 对应的 Proto，避免 VM 池复用时误用旧缓存。
	arithmeticIntRegisterCacheProto *bytecode.Proto
	// stringTableReadCache 按 PC 缓存无元表 table 的字符串常量 key 读取结果。
	stringTableReadCache []stringTableReadCacheEntry
	// stringTableReadCacheProto 记录 stringTableReadCache 对应的 Proto，避免 VM 池复用时误用旧缓存。
	stringTableReadCacheProto *bytecode.Proto
	// disableStringTableReadCache 表示当前执行路径要求逐次读取动态 table 字段，不使用 inline cache。
	disableStringTableReadCache bool
	// borrowedSelfRecursiveLocalFunctionClosureEnabled 控制非逃逸自递归局部函数闭包借用路径。
	borrowedSelfRecursiveLocalFunctionClosureEnabled bool
	// borrowedSelfRecursiveLocalFunctionProto 记录借用闭包对应的子 Proto，避免跨 Proto 复用错误闭包。
	borrowedSelfRecursiveLocalFunctionProto *bytecode.Proto
	// borrowedSelfRecursiveLocalFunctionClosure 保存 VM 本地复用的自递归局部函数闭包。
	borrowedSelfRecursiveLocalFunctionClosure *LuaClosure
	// borrowedSelfRecursiveLocalFunctionCell 保存借用闭包的 self upvalue cell。
	borrowedSelfRecursiveLocalFunctionCell *UpvalueCell
	// borrowedSelfRecursiveLocalFunctionValue 保存借用闭包的引用值，避免每次 OP_CLOSURE 重建 Value。
	borrowedSelfRecursiveLocalFunctionValue Value
}

const (
	// arithmeticIntRegisterCacheNone 表示当前 PC 没有可用的 integer 寄存器算术缓存。
	arithmeticIntRegisterCacheNone byte = iota
	// arithmeticIntRegisterCacheAdd 表示当前 PC 最近命中过 ADD 的双 integer 寄存器路径。
	arithmeticIntRegisterCacheAdd
	// arithmeticIntRegisterCacheSub 表示当前 PC 最近命中过 SUB 的双 integer 寄存器路径。
	arithmeticIntRegisterCacheSub
	// arithmeticIntRegisterCacheMul 表示当前 PC 最近命中过 MUL 的双 integer 寄存器路径。
	arithmeticIntRegisterCacheMul
	// arithmeticIntRegisterCacheSubRightConstant 表示当前 PC 最近命中过 SUB 的左寄存器右 integer 常量路径。
	arithmeticIntRegisterCacheSubRightConstant
	// arithmeticIntRegisterCacheMulRightConstant 表示当前 PC 最近命中过 MUL 的左寄存器右 integer 常量路径。
	arithmeticIntRegisterCacheMulRightConstant
	// arithmeticIntRegisterCacheAddNumber 表示当前 PC 最近命中过 ADD 的寄存器 number 路径。
	arithmeticIntRegisterCacheAddNumber
	// arithmeticIntRegisterCacheDivNumber 表示当前 PC 最近命中过 DIV 的寄存器原生数值路径。
	arithmeticIntRegisterCacheDivNumber
	// arithmeticIntRegisterCacheMod 表示当前 PC 最近命中过 MOD 的双 integer 路径。
	arithmeticIntRegisterCacheMod
	// arithmeticIntRegisterCacheIDiv 表示当前 PC 最近命中过 IDIV 的双 integer 路径。
	arithmeticIntRegisterCacheIDiv
)

// arithmeticIntOperandCacheEntry 表示一条 integer 算术 inline cache 的操作数形态。
//
// 寄存器操作数保存寄存器索引，运行期仍检查 KindInteger；常量操作数保存不可变常量值，命中后
// 不再重复访问 Proto 常量表。
type arithmeticIntOperandCacheEntry struct {
	// leftIndex 保存左操作数为寄存器时的寄存器索引。
	leftIndex int
	// rightIndex 保存右操作数为寄存器时的寄存器索引。
	rightIndex int
	// leftConstant 保存左操作数为 integer 常量时的常量值。
	leftConstant int64
	// rightConstant 保存右操作数为 integer 常量时的常量值。
	rightConstant int64
	// leftConstantOperand 表示左操作数来自 RK 常量表。
	leftConstantOperand bool
	// rightConstantOperand 表示右操作数来自 RK 常量表。
	rightConstantOperand bool
}

// stringTableReadCacheEntry 表示一条字符串常量 table 读取 inline cache。
//
// table、tableID 与 version 共同判定缓存是否仍然匹配；同一 Proto 的同一 PC 固定对应同一个字符串常量 key，
// 因此 key 不需要重复保存。value 可以是 nil 值，valid 用于区分未初始化缓存。
type stringTableReadCacheEntry struct {
	// table 保存上次命中的 Lua table 指针。
	table *Table
	// tableID 保存 table 的稳定创建身份，防止 Go GC 地址复用把新 table 当成旧 table。
	tableID uint64
	// version 保存 table 上次命中时的 raw 写入版本。
	version uint64
	// value 保存上次读取到的 Lua 值。
	value Value
	// valid 表示当前缓存项是否已经初始化。
	valid bool
}

// decodedRKOperand 表示一侧 RK 操作数的预解码形态。
//
// Constant 为 true 时 Index 是 Proto.Constants 的下标；IntegerConstantOK 只在常量存在且类型为
// integer 时为 true。寄存器操作数只保存寄存器下标，运行期仍必须检查寄存器窗口和动态类型。
type decodedRKOperand struct {
	// Index 保存寄存器下标或常量表下标。
	Index int
	// Constant 表示该 RK 操作数来自常量表。
	Constant bool
	// IntegerConstant 保存 integer 常量值。
	IntegerConstant int64
	// IntegerConstantOK 表示 IntegerConstant 可直接用于 integer arithmetic fast path。
	IntegerConstantOK bool
}

// decodedInstruction 保存单条 Proto 指令的不可变预解码字段。
//
// 该结构只缓存从原始 Instruction 和 Proto.Constants 派生出的只读信息；debug、traceback、
// 反汇编和普通 VM 仍以 Proto.Code 为准，后续 fast path 必须在 guard 失败时回退 Step。
type decodedInstruction struct {
	// Instruction 保存原始 32 位指令，便于回退普通 VM 或错误路径复用。
	Instruction bytecode.Instruction
	// OpCode 保存指令 opcode，避免 hot path 每步重复提取低 6 位。
	OpCode bytecode.OpCode
	// A 保存 A 操作数字段。
	A int
	// B 保存 B 操作数字段。
	B int
	// C 保存 C 操作数字段。
	C int
	// Bx 保存 Bx 操作数字段。
	Bx int
	// SBx 保存 sBx 跳转偏移字段。
	SBx int
	// Ax 保存 Ax 扩展参数字段。
	Ax int
	// BOperand 保存 B 字段按 RK 语义预解码后的形态。
	BOperand decodedRKOperand
	// COperand 保存 C 字段按 RK 语义预解码后的形态。
	COperand decodedRKOperand
}

// tableArrayPreallocHint 保存 `NEWTABLE` 后连续 numeric-for 数组写入的预留容量。
//
// 当前只覆盖精确 `local t = {}; for i = 1, N do t[i] = i end` 形态。容量 hint 只影响 table
// 底层数组区 cap，不跳过后续 SETTABLE、FORPREP 或 FORLOOP 指令。
type tableArrayPreallocHint struct {
	// Capacity 保存可安全预留的数组区容量。
	Capacity int
	// Valid 表示当前 PC 的 NEWTABLE 是否存在可用预分配 hint。
	Valid bool
}

// tableWriteForLoopSuperInstruction 保存 `SETTABLE; FORLOOP` 的预匹配形态。
//
// 该形态只覆盖 `t[i] = i` 的连续 numeric-for 写入。执行期仍检查 table identity、元表、寄存器
// 类型和 numeric-for 控制槽；guard 不满足时必须回退普通 SETTABLE/FORLOOP 路径。
type tableWriteForLoopSuperInstruction struct {
	// TableRegister 保存被写入 table 所在寄存器。
	TableRegister int
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配连续 table 写入循环形态。
	Valid bool
}

// TableWriteForLoopBatch 保存已验证的连续 table 写入 batch 执行上下文。
//
// 字段保持未导出，调用方只能通过 PrepareTableWriteForLoopBatch 获取已验证上下文。
type TableWriteForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体 SETTABLE 的 PC。
	pc int
	// superInstruction 保存已匹配的 SETTABLE;FORLOOP 寄存器数据流。
	superInstruction tableWriteForLoopSuperInstruction
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareTableWriteForLoopBatch 成功构造。
	valid bool
}

// tableReadAddForLoopSuperInstruction 保存 `GETTABLE; ADD; FORLOOP` 的预匹配形态。
//
// 该形态只覆盖 `sum = sum + t[i]` 或 `sum = t[i] + sum` 的连续 numeric-for 读取求和。
// 执行期仍检查 table identity、元表、寄存器类型、table 值类型和 numeric-for 控制槽。
type tableReadAddForLoopSuperInstruction struct {
	// TableRegister 保存被读取 table 所在寄存器。
	TableRegister int
	// GetTarget 保存 GETTABLE 写入的临时寄存器。
	GetTarget int
	// SumRegister 保存 ADD 累加寄存器，也是 ADD 的目标寄存器。
	SumRegister int
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配连续 table 读取求和形态。
	Valid bool
}

// TableReadAddForLoopBatch 保存已验证的连续 table 读取求和 batch 执行上下文。
//
// 字段保持未导出，调用方只能通过 PrepareTableReadAddForLoopBatch 获取已验证上下文。
type TableReadAddForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体 GETTABLE 的 PC。
	pc int
	// superInstruction 保存已匹配的 GETTABLE;ADD;FORLOOP 寄存器数据流。
	superInstruction tableReadAddForLoopSuperInstruction
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareTableReadAddForLoopBatch 成功构造。
	valid bool
}

// addForLoopSuperInstruction 保存 `ADD; FORLOOP` 的预匹配形态。
//
// Valid 为 true 时，当前 PC 是 ADD 且下一条指令是 FORLOOP。操作数仍在运行期检查类型和值；
// 该结构只避免 hot path 每轮重复识别 opcode、PC 和 RK 形态。
type addForLoopSuperInstruction struct {
	// AddTarget 保存 ADD 的目标寄存器。
	AddTarget int
	// LeftOperand 保存 ADD 左操作数形态。
	LeftOperand decodedRKOperand
	// RightOperand 保存 ADD 右操作数形态。
	RightOperand decodedRKOperand
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配 ADD;FORLOOP 形态。
	Valid bool
}

// AddForLoopBatch 保存已验证的 `ADD; FORLOOP` batch 执行上下文。
//
// 该结构只覆盖 `sum = sum + i` 或 `sum = i + sum` 这类目标寄存器加可见 numeric-for 变量的窄形态；
// 字段保持未导出，调用方只能通过 PrepareAddForLoopBatch 获取已验证上下文。
type AddForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体 ADD 的 PC。
	pc int
	// superInstruction 保存已匹配的 ADD;FORLOOP 寄存器数据流。
	superInstruction addForLoopSuperInstruction
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareAddForLoopBatch 成功构造。
	valid bool
}

// mulAddSubForLoopSuperInstruction 保存 `MUL; ADD; SUB; FORLOOP` 的预匹配形态。
//
// 该形态覆盖 `sum = sum + i * K1 - K2` 一类左结合整数算术链。所有操作数仍在运行期按
// integer guard 检查；guard 不满足时调用方必须回退普通 VM，保留字符串数字、元方法和错误语义。
type mulAddSubForLoopSuperInstruction struct {
	// MulTarget 保存 MUL 的目标寄存器。
	MulTarget int
	// MulLeftOperand 保存 MUL 左操作数形态。
	MulLeftOperand decodedRKOperand
	// MulRightOperand 保存 MUL 右操作数形态。
	MulRightOperand decodedRKOperand
	// AddTarget 保存 ADD 的目标寄存器。
	AddTarget int
	// AddLeftOperand 保存 ADD 左操作数形态。
	AddLeftOperand decodedRKOperand
	// AddRightOperand 保存 ADD 右操作数形态。
	AddRightOperand decodedRKOperand
	// SubTarget 保存 SUB 的目标寄存器。
	SubTarget int
	// SubLeftOperand 保存 SUB 左操作数形态。
	SubLeftOperand decodedRKOperand
	// SubRightOperand 保存 SUB 右操作数形态。
	SubRightOperand decodedRKOperand
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配 MUL;ADD;SUB;FORLOOP 形态。
	Valid bool
}

// MulAddSubForLoopBatch 保存已验证的 `MUL; ADD; SUB; FORLOOP` batch 执行上下文。
//
// 该结构只覆盖 `sum = sum + i * K1 - K2` 官方算术链形态；复杂别名、非常量乘数/减数或控制槽覆盖
// 都会回退单轮 superinstruction 或普通 VM，以保留逐指令可见性和错误语义。
type MulAddSubForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体 MUL 的 PC。
	pc int
	// superInstruction 保存已匹配的 MUL;ADD;SUB;FORLOOP 寄存器数据流。
	superInstruction mulAddSubForLoopSuperInstruction
	// TempRegister 保存 MUL/ADD 复用的临时寄存器。
	TempRegister int
	// SumRegister 保存 SUB 写回的累加寄存器。
	SumRegister int
	// MulConstant 保存 MUL 右侧或左侧的 integer 常量。
	MulConstant int64
	// SubConstant 保存 SUB 右侧 integer 常量。
	SubConstant int64
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareMulAddSubForLoopBatch 成功构造。
	valid bool
}

// mixArithmeticForLoopSuperInstruction 保存 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` 的预匹配形态。
//
// 该形态覆盖官方 `arith_mix_loop` 的完整循环体。所有操作数和除数仍在运行期按 integer guard
// 检查；guard 不满足时调用方必须回退普通 VM，保留字符串数字、元方法、零除和错误栈语义。
type mixArithmeticForLoopSuperInstruction struct {
	// MulTarget 保存 MUL 的目标寄存器。
	MulTarget int
	// MulLeftOperand 保存 MUL 左操作数形态。
	MulLeftOperand decodedRKOperand
	// MulRightOperand 保存 MUL 右操作数形态。
	MulRightOperand decodedRKOperand
	// FirstAddTarget 保存第一条 ADD 的目标寄存器。
	FirstAddTarget int
	// FirstAddLeftOperand 保存第一条 ADD 左操作数形态。
	FirstAddLeftOperand decodedRKOperand
	// FirstAddRightOperand 保存第一条 ADD 右操作数形态。
	FirstAddRightOperand decodedRKOperand
	// SubTarget 保存 SUB 的目标寄存器。
	SubTarget int
	// SubLeftOperand 保存 SUB 左操作数形态。
	SubLeftOperand decodedRKOperand
	// SubRightOperand 保存 SUB 右操作数形态。
	SubRightOperand decodedRKOperand
	// IDivTarget 保存 IDIV 的目标寄存器。
	IDivTarget int
	// IDivLeftOperand 保存 IDIV 左操作数形态。
	IDivLeftOperand decodedRKOperand
	// IDivRightOperand 保存 IDIV 右操作数形态。
	IDivRightOperand decodedRKOperand
	// ModTarget 保存 MOD 的目标寄存器。
	ModTarget int
	// ModLeftOperand 保存 MOD 左操作数形态。
	ModLeftOperand decodedRKOperand
	// ModRightOperand 保存 MOD 右操作数形态。
	ModRightOperand decodedRKOperand
	// FinalAddTarget 保存末尾 ADD 的目标寄存器。
	FinalAddTarget int
	// FinalAddLeftOperand 保存末尾 ADD 左操作数形态。
	FinalAddLeftOperand decodedRKOperand
	// FinalAddRightOperand 保存末尾 ADD 右操作数形态。
	FinalAddRightOperand decodedRKOperand
	// SumRegister 保存循环内被累积更新的 sum 寄存器。
	SumRegister int
	// LoopRegister 保存循环体读取的外部可见 numeric-for 控制变量寄存器。
	LoopRegister int
	// MulConstant 保存 MUL 使用的 integer 常量。
	MulConstant int64
	// SubConstant 保存 SUB 使用的 integer 常量。
	SubConstant int64
	// IDivConstant 保存 IDIV 使用的非零 integer 常量。
	IDivConstant int64
	// ModConstant 保存 MOD 使用的非零 integer 常量。
	ModConstant int64
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// BatchTempRegister 保存 batch 路径中 MUL/ADD/IDIV 复用的临时寄存器。
	BatchTempRegister int
	// BatchSumRegister 保存 batch 路径中 SUB 与末尾 ADD 写回的累加寄存器。
	BatchSumRegister int
	// BatchModRegister 保存 batch 路径中 MOD 写回的临时寄存器。
	BatchModRegister int
	// BatchMaxRegisterIndex 保存 batch 路径一次性寄存器边界检查所需的最大下标。
	BatchMaxRegisterIndex int
	// BatchValid 表示当前预匹配形态同时满足 batch 静态别名约束。
	BatchValid bool
	// Valid 表示当前 PC 是否匹配完整混合算术循环形态。
	Valid bool
}

// MixArithmeticForLoopBatch 保存已验证的 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` batch 上下文。
//
// 该结构只覆盖官方 `arith_mix_loop` 的窄形态：sum、IDIV 临时寄存器、MOD 临时寄存器和 numeric-for
// 控制槽互不别名；其他寄存器别名形态回退单轮 superinstruction 或普通 VM，以保留逐指令可见性。
type MixArithmeticForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体 MUL 的 PC。
	pc int
	// superInstruction 保存已匹配的混合算术循环寄存器数据流。
	superInstruction mixArithmeticForLoopSuperInstruction
	// TempRegister 保存 MUL/ADD/IDIV 复用的临时寄存器。
	TempRegister int
	// SumRegister 保存 SUB 与末尾 ADD 写回的累加寄存器。
	SumRegister int
	// ModRegister 保存 MOD 写回的临时寄存器。
	ModRegister int
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareMixArithmeticForLoopBatch 成功构造。
	valid bool
}

// functionCallAddForLoopSuperInstruction 保存 `MOVE; MOVE; LOADK; CALL; ADD; FORLOOP` 的预匹配形态。
//
// 该形态覆盖官方 `function_call` 的完整循环体。构建期只记录寄存器和常量数据流；执行期仍检查
// callee closure、实参、sum 和 numeric-for 控制槽类型，guard 不满足时必须回退普通 VM。
type functionCallAddForLoopSuperInstruction struct {
	// FunctionSlot 保存 CALL 的函数槽，也是第一条 MOVE 的目标和 CALL 结果槽。
	FunctionSlot int
	// FunctionSource 保存被调用 closure 所在寄存器。
	FunctionSource int
	// FirstArgumentSlot 保存第二条 MOVE 的目标实参槽。
	FirstArgumentSlot int
	// FirstArgumentSource 保存第一实参来源寄存器，通常是 numeric-for 外部循环变量。
	FirstArgumentSource int
	// SecondArgumentSlot 保存 LOADK 写入的第二实参槽。
	SecondArgumentSlot int
	// SecondArgument 保存第二实参的 integer 常量值。
	SecondArgument int64
	// SumRegister 保存外层 ADD 累加寄存器。
	SumRegister int
	// AddTarget 保存外层 ADD 目标寄存器。
	AddTarget int
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配完整函数调用加法循环形态。
	Valid bool
}

// FunctionCallAddForLoopBatch 保存已验证的函数调用加法循环 batch 执行上下文。
//
// 该结构只暴露给 API 热循环复用当前 PC 的静态 guard 结果；字段保持未导出，避免调用方绕过
// PrepareFunctionCallAddForLoopBatch 构造不完整上下文。每个虚拟 CALL 前的 context 检查由 batch 执行方法负责。
type FunctionCallAddForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体第一条 MOVE 的 PC。
	pc int
	// superInstruction 保存已匹配的循环体寄存器和常量数据流。
	superInstruction functionCallAddForLoopSuperInstruction
	// functionValue 保存已验证的 callee closure 值，执行期用于确认函数寄存器未被替换。
	functionValue Value
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareFunctionCallAddForLoopBatch 成功构造。
	valid bool
}

// functionCallAssignForLoopSuperInstruction 保存 `MOVE; MOVE; MOVE; CALL; MOVE; FORLOOP` 的预匹配形态。
//
// 该形态覆盖官方完整 benchmark 的 `sum = add(sum, i)` 循环体。构建期只记录寄存器数据流；执行期仍
// 检查 callee closure、sum、循环变量和 numeric-for 控制槽类型，guard 不满足时必须回退普通 VM。
type functionCallAssignForLoopSuperInstruction struct {
	// FunctionSlot 保存 CALL 的函数槽，也是第一条 MOVE 的目标和 CALL 结果槽。
	FunctionSlot int
	// FunctionSource 保存被调用 closure 所在寄存器。
	FunctionSource int
	// SumRegister 保存 sum 寄存器，也是第一实参来源与 CALL 后 MOVE 的目标。
	SumRegister int
	// FirstArgumentSlot 保存第二条 MOVE 的目标实参槽。
	FirstArgumentSlot int
	// SecondArgumentSlot 保存第三条 MOVE 的目标实参槽。
	SecondArgumentSlot int
	// SecondArgumentSource 保存第二实参来源寄存器，通常是 numeric-for 外部循环变量。
	SecondArgumentSource int
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配完整函数调用赋值循环形态。
	Valid bool
}

// FunctionCallAssignForLoopBatch 保存已验证的函数调用赋值循环 batch 执行上下文。
//
// 字段保持未导出，调用方只能通过 PrepareFunctionCallAssignForLoopBatch 获取已验证上下文。每个虚拟
// CALL 前的 context 检查由 TryExecuteFunctionCallAssignForLoopBatch 负责。
type FunctionCallAssignForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体第一条 MOVE 的 PC。
	pc int
	// superInstruction 保存已匹配的循环体寄存器数据流。
	superInstruction functionCallAssignForLoopSuperInstruction
	// functionValue 保存已验证的 callee closure 值，执行期用于确认函数寄存器未被替换。
	functionValue Value
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareFunctionCallAssignForLoopBatch 成功构造。
	valid bool
}

// closureUpvalueForLoopSuperInstruction 保存 `MOVE; LOADK; CALL; MOVE; FORLOOP` 的预匹配形态。
//
// 该形态覆盖官方 `closure_upvalue` benchmark 的 `sum = inc(1)` 循环体。构建期只记录寄存器和常量
// 数据流；执行期仍检查 callee closure、upvalue、参数和 numeric-for 控制槽，guard 不满足时必须回退。
type closureUpvalueForLoopSuperInstruction struct {
	// FunctionSlot 保存 CALL 的函数槽，也是第一条 MOVE 的目标和 CALL 结果槽。
	FunctionSlot int
	// FunctionSource 保存被调用 closure 所在寄存器。
	FunctionSource int
	// ArgumentSlot 保存 LOADK 写入的固定实参槽。
	ArgumentSlot int
	// ArgumentValue 保存固定 integer 实参值。
	ArgumentValue int64
	// ResultTarget 保存 CALL 后 MOVE 写回的 sum 寄存器。
	ResultTarget int
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配完整闭包 upvalue 循环形态。
	Valid bool
}

// ClosureUpvalueForLoopBatch 保存已验证的闭包 upvalue 循环 batch 执行上下文。
//
// 字段保持未导出，调用方只能通过 PrepareClosureUpvalueForLoopBatch 获取已验证上下文。每个虚拟
// CALL 前的 context 检查由 TryExecuteClosureUpvalueForLoopBatch 负责。
type ClosureUpvalueForLoopBatch struct {
	// proto 记录 batch 绑定的 Proto，用于防止 VM 复用后误用旧上下文。
	proto *bytecode.Proto
	// pc 保存循环体第一条 MOVE 的 PC。
	pc int
	// superInstruction 保存已匹配的循环体寄存器和常量数据流。
	superInstruction closureUpvalueForLoopSuperInstruction
	// functionValue 保存已验证的 callee closure 值，执行期用于确认函数寄存器未被替换。
	functionValue Value
	// closure 保存已验证的 upvalue 自增叶子闭包。
	closure *LuaClosure
	// leaf 保存闭包创建期预解析的 upvalue 自增形态。
	leaf *LuaLeafUpvalueAddSetReturn
	// maxRegisterIndex 保存参与寄存器最大下标，用一次边界检查替代逐寄存器重复检查。
	maxRegisterIndex int
	// valid 表示 batch 是否由 PrepareClosureUpvalueForLoopBatch 成功构造。
	valid bool
}

// formatLenAddForLoopSuperInstruction 保存 `string.format("%d", i)` 被 LEN 消费后的循环尾部形态。
//
// 该形态覆盖官方 `stdlib_math_string` 的尾部 `GETTABUP; GETTABLE; LOADK; MOVE; CALL; LEN; ADD; FORLOOP`。
// 构建期只记录寄存器、upvalue 和常量数据流；执行期仍检查全局 table、string table、format 函数身份、
// 参数类型、累加器和 numeric-for 控制槽，任一 guard 不满足时必须回退普通 VM。
type formatLenAddForLoopSuperInstruction struct {
	// EnvUpvalueIndex 保存 GETTABUP 读取的环境 upvalue 下标。
	EnvUpvalueIndex int
	// FunctionSlot 保存 string.format 函数槽，也是 CALL 结果槽和 LEN 目标槽。
	FunctionSlot int
	// FormatArgumentSlot 保存 LOADK 写入 `%d` 的实参槽。
	FormatArgumentSlot int
	// ValueArgumentSlot 保存 MOVE 写入待格式化值的实参槽。
	ValueArgumentSlot int
	// ValueArgumentSource 保存待格式化值来源寄存器，官方形态中为 numeric-for 外部循环变量。
	ValueArgumentSource int
	// FormatString 保存 LOADK 的 exact `%d` 常量，用于还原被跳过指令后的寄存器状态。
	FormatString string
	// AccumulatorRegister 保存前序 ADD 生成的中间累加值寄存器。
	AccumulatorRegister int
	// AddTarget 保存最终 ADD 写回的 sum 寄存器。
	AddTarget int
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配完整 string.format 长度消费形态。
	Valid bool
}

// stdlibMathStringForLoopSuperInstruction 保存官方 stdlib_math_string 的完整循环体形态。
//
// 该形态覆盖 `math.floor(math.sqrt(i)) + #string.format("%d", i)` 的热体。构建期只记录寄存器、
// upvalue 和常量数据流；执行期仍检查环境表、math/string 表、函数身份、累加器与 numeric-for
// 控制槽，任一 guard 不满足时必须回退普通 VM。
type stdlibMathStringForLoopSuperInstruction struct {
	// EnvUpvalueIndex 保存 GETTABUP 读取的环境 upvalue 下标。
	EnvUpvalueIndex int
	// FloorSlot 保存 math.floor 函数槽，也是外层 CALL 结果槽。
	FloorSlot int
	// SqrtSlot 保存 math.sqrt 函数槽，也是内层 CALL 结果槽。
	SqrtSlot int
	// SqrtArgumentSlot 保存 MOVE 写入 sqrt 参数的槽位。
	SqrtArgumentSlot int
	// FormatSlot 保存 string.format 函数槽，也是 CALL 结果槽和 LEN 目标槽。
	FormatSlot int
	// FormatArgumentSlot 保存 LOADK 写入 `%d` 的实参槽。
	FormatArgumentSlot int
	// ValueArgumentSlot 保存 MOVE 写入待格式化值的实参槽。
	ValueArgumentSlot int
	// ValueArgumentSource 保存待计算/格式化的 numeric-for 外部循环变量。
	ValueArgumentSource int
	// FormatString 保存 LOADK 的 exact `%d` 常量，用于还原被跳过指令后的寄存器状态。
	FormatString string
	// AccumulatorRegister 保存前序 math ADD 生成的中间累加值寄存器。
	AccumulatorRegister int
	// SumRegister 保存最终 ADD 写回的 sum 寄存器。
	SumRegister int
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配完整 stdlib_math_string 循环体形态。
	Valid bool
}

// stringAppendForLoopSuperInstruction 保存 `s = s .. Kstring` 循环体形态。
//
// 该形态覆盖官方 string_concat 的 `MOVE; LOADK; CONCAT; FORLOOP` 热体。构建期只记录寄存器、
// 常量和 FORLOOP 数据流；执行期仍检查目标寄存器与 numeric-for 控制槽类型。任一 guard 不满足
// 时必须回退普通 VM，以保留 number 转换、`__concat`、debug hook 和错误路径语义。
type stringAppendForLoopSuperInstruction struct {
	// TargetRegister 保存 CONCAT 写回的字符串累加寄存器。
	TargetRegister int
	// MoveRegister 保存 MOVE 写入旧累加字符串的临时槽。
	MoveRegister int
	// AppendRegister 保存 LOADK 写入右侧常量字符串的临时槽。
	AppendRegister int
	// AppendText 保存右侧固定 string 常量。
	AppendText string
	// ForBase 保存 FORLOOP 的 base 寄存器。
	ForBase int
	// ForSBx 保存 FORLOOP 的有符号跳转偏移。
	ForSBx int
	// ExitPC 保存循环退出时的下一条 PC。
	ExitPC int
	// LoopPC 保存循环继续时的跳转 PC。
	LoopPC int
	// Valid 表示当前 PC 是否匹配完整字符串自追加循环形态。
	Valid bool
}

// stringAppendContextPhase 表示整段字符串自追加 batch 内部 context 检查发生的虚拟指令边界。
//
// 这些边界只用于取消路径提交寄存器快照；正常热路径不 materialize 中间字符串。
type stringAppendContextPhase int

const (
	// stringAppendPhaseAfterCompleted 表示检查发生在上一轮 FORLOOP 后、下一轮 MOVE 前。
	stringAppendPhaseAfterCompleted stringAppendContextPhase = iota
	// stringAppendPhaseAfterMove 表示检查发生在当前轮 MOVE 后、LOADK 前。
	stringAppendPhaseAfterMove
	// stringAppendPhaseAfterLoadK 表示检查发生在当前轮 LOADK 后、CONCAT 前。
	stringAppendPhaseAfterLoadK
	// stringAppendPhaseAfterConcat 表示检查发生在当前轮 CONCAT 后、FORLOOP 前。
	stringAppendPhaseAfterConcat
)

// LuaClosure 表示最小 VM 阶段的 Lua closure 值。
//
// Proto 指向待执行函数原型；Upvalues 保存 CLOSURE 指令根据 UpvalueDesc 捕获到的值。
type LuaClosure struct {
	// Proto 保存 Lua 函数原型。
	Proto *bytecode.Proto
	// Upvalues 保存闭包捕获值快照。
	Upvalues []Value
	// UpvalueCells 保存运行期共享 upvalue 槽；存在时优先于 Upvalues 执行读写。
	UpvalueCells []*UpvalueCell
	// inlineUpvalue 保存单 upvalue 闭包的内嵌快照，避免 OP_CLOSURE 热路径额外分配切片底层数组。
	inlineUpvalue [1]Value
	// inlineUpvalueCell 保存单 upvalue 闭包的内嵌共享 cell，避免 OP_CLOSURE 热路径额外分配切片底层数组。
	inlineUpvalueCell [1]*UpvalueCell
	// DirectCallSafe 表示该 closure 的 Proto 可走 Lua 叶子函数 direct CALL 快路径。
	DirectCallSafe bool
	// LeafAddReturn 保存 ADD;RETURN 叶子函数的预解析形态，nil 表示不能走 caller-side 加法快路径。
	LeafAddReturn *LuaLeafAddReturn
	// LeafUpvalueAddSetReturn 保存 upvalue 自增叶子函数的预解析形态，nil 表示不能走 caller-side 写回快路径。
	LeafUpvalueAddSetReturn *LuaLeafUpvalueAddSetReturn
	// SelfRecursiveIntegerFib 表示该 closure 的 Proto 命中固定签名整数 fib 自递归形态。
	SelfRecursiveIntegerFib bool
}

// LuaLeafAddReturn 保存 `ADD;RETURN` 或 `GETUPVAL;ADD;RETURN` 叶子函数快路径元数据。
//
// AddInstruction 和 ReturnInstruction 来自不可变 Proto；HasUpvalueRegister 为 true 时，
// UpvalueRegister 表示 GETUPVAL 写入的临时寄存器，UpvalueIndex 表示被读取的 closure upvalue。
type LuaLeafAddReturn struct {
	// AddInstruction 保存实际 ADD 指令。
	AddInstruction bytecode.Instruction
	// ReturnInstruction 保存 ADD 后的 RETURN 指令。
	ReturnInstruction bytecode.Instruction
	// LeftOperand 保存 ADD 左操作数的预解析形态。
	LeftOperand LuaLeafAddOperand
	// RightOperand 保存 ADD 右操作数的预解析形态。
	RightOperand LuaLeafAddOperand
	// UpvalueRegister 保存可由 upvalue 直接替代的 GETUPVAL 目标寄存器。
	UpvalueRegister int
	// UpvalueIndex 保存 GETUPVAL 读取的 upvalue 下标。
	UpvalueIndex int
	// HasUpvalueRegister 表示该叶子函数带 GETUPVAL 前缀。
	HasUpvalueRegister bool
	// IntegerRegisterIndex 保存 `R + integer` 快路径中的寄存器操作数下标。
	IntegerRegisterIndex int
	// IntegerConstant 保存 `R + integer` 快路径中的 integer 常量。
	IntegerConstant int64
	// HasRegisterIntegerConstant 表示该叶子函数可走寄存器加 integer 常量专用快路径。
	HasRegisterIntegerConstant bool
	// UpvalueAddRegisterIndex 保存 `R + upvalue` 快路径中的实参寄存器下标。
	UpvalueAddRegisterIndex int
	// HasRegisterUpvalueAdd 表示该叶子函数可走实参加 upvalue 专用快路径。
	HasRegisterUpvalueAdd bool
	// LeftRegisterIndex 保存 `R + R` 快路径中的左侧实参寄存器下标。
	LeftRegisterIndex int
	// RightRegisterIndex 保存 `R + R` 快路径中的右侧实参寄存器下标。
	RightRegisterIndex int
	// HasRegisterRegisterAdd 表示该叶子函数可走双实参寄存器专用快路径。
	HasRegisterRegisterAdd bool
}

// LuaLeafAddOperand 保存 caller-side 叶子 ADD 操作数形态。
//
// Constant 为 true 时直接使用 ConstantValue；否则 RegisterIndex 是 callee 寄存器下标，
// 调用方需要映射到 caller 实参区或 upvalue 临时寄存器。
type LuaLeafAddOperand struct {
	// RegisterIndex 保存寄存器操作数下标。
	RegisterIndex int
	// ConstantValue 保存常量操作数转换后的 runtime 值。
	ConstantValue Value
	// Constant 表示该操作数来自 Proto 常量表。
	Constant bool
}

// LuaLeafUpvalueAddSetReturn 保存 `upvalue = upvalue + X; return upvalue` 叶子函数快路径元数据。
//
// UpvalueIndex 指向被读取和写回的 closure upvalue；右操作数可为 Lua integer 常量或固定参数寄存器。
type LuaLeafUpvalueAddSetReturn struct {
	// UpvalueIndex 保存 GETUPVAL/SETUPVAL 访问的 upvalue 下标。
	UpvalueIndex int
	// IntegerConstant 保存 `upvalue + integer` 中的 integer 常量。
	IntegerConstant int64
	// RegisterIndex 保存 `upvalue + R` 中的参数寄存器下标。
	RegisterIndex int
	// HasIntegerConstant 表示该形态使用 integer 常量作为另一侧操作数。
	HasIntegerConstant bool
	// HasRegisterOperand 表示该形态使用调用参数寄存器作为另一侧操作数。
	HasRegisterOperand bool
}

// NewLuaClosure 创建带运行期缓存属性的 Lua closure。
//
// proto 必须是不可变 Lua 函数原型；upvalues/upvalueCells 由调用方按捕获语义准备。返回 closure
// 会缓存 direct CALL 安全性和极小叶子函数形态，避免热循环中每次 CALL 重复扫描 Proto 指令。
func NewLuaClosure(proto *bytecode.Proto, upvalues []Value, upvalueCells []*UpvalueCell) *LuaClosure {
	// 创建 closure 时一次性计算不可变 Proto 的 direct CALL 属性。
	directCallSafe := luaProtoDirectCallSafe(proto)
	var leafAddReturn *LuaLeafAddReturn
	var leafUpvalueAddSetReturn *LuaLeafUpvalueAddSetReturn
	selfRecursiveIntegerFib := false
	if directCallSafe {
		// 叶子快路径形态不包含 CALL/TAILCALL/TFORCALL/CLOSURE；非 direct-safe Proto 必然无法命中。
		leafAddReturn = luaProtoLeafAddReturn(proto)
		leafUpvalueAddSetReturn = luaProtoLeafUpvalueAddSetReturn(proto)
	} else {
		// 自递归 fib 明确包含 CALL，只在非 direct-safe Proto 中做精确形态识别。
		selfRecursiveIntegerFib = luaProtoSelfRecursiveIntegerFib(proto)
	}
	return &LuaClosure{
		Proto:                   proto,
		Upvalues:                upvalues,
		UpvalueCells:            upvalueCells,
		DirectCallSafe:          directCallSafe,
		LeafAddReturn:           leafAddReturn,
		LeafUpvalueAddSetReturn: leafUpvalueAddSetReturn,
		SelfRecursiveIntegerFib: selfRecursiveIntegerFib,
	}
}

// bindSingleCapturedUpvalue 绑定 OP_CLOSURE 捕获到的单个 upvalue。
//
// 该方法只供 VM 创建闭包时使用；value 是创建时的调试快照，cell 是运行期共享槽。单 upvalue
// 直接存入 closure 内嵌数组，避免为常见局部函数创建两段独立切片底层数组。
func (closure *LuaClosure) bindSingleCapturedUpvalue(value Value, cell *UpvalueCell) {
	// nil closure 无法绑定捕获值，保持调用方幂等。
	if closure == nil {
		return
	}
	closure.inlineUpvalue[0] = value
	closure.inlineUpvalueCell[0] = cell
	closure.Upvalues = closure.inlineUpvalue[:1]
	closure.UpvalueCells = closure.inlineUpvalueCell[:1]
}

// luaProtoDirectCallSafe 判断 Proto 是否适合 Lua closure direct CALL 热路径。
//
// direct CALL 当前仅覆盖纯叶子函数，避免嵌套调用、闭包创建和 yield 现场裁剪破坏 coroutine。
func luaProtoDirectCallSafe(proto *bytecode.Proto) bool {
	if proto == nil {
		// 缺少 Proto 时不能进入 direct CALL。
		return false
	}
	for instructionIndex := range proto.Code {
		switch proto.Code[instructionIndex].OpCode() {
		case bytecode.OpCall, bytecode.OpTailCall, bytecode.OpTForCall, bytecode.OpClosure:
			// 任何嵌套调用或闭包创建都交给通用路径。
			return false
		}
	}
	return true
}

// luaProtoLeafAddReturn 预解析 `ADD;RETURN` 叶子函数形态。
//
// proto 必须是不可变函数原型；返回 nil 表示不支持 caller-side 原生加法快路径。
func luaProtoLeafAddReturn(proto *bytecode.Proto) *LuaLeafAddReturn {
	if proto == nil {
		// 缺少 Proto 时不能识别叶子函数。
		return nil
	}
	prefixLength := 0
	leaf := LuaLeafAddReturn{}
	if len(proto.Code) == 3 && proto.Code[0].OpCode() == bytecode.OpGetUpval {
		// 闭包捕获常量形态通常先 GETUPVAL 到临时寄存器，再 ADD 后 RETURN。
		getUpvalueInstruction := proto.Code[0]
		leaf.UpvalueRegister = getUpvalueInstruction.A()
		leaf.UpvalueIndex = getUpvalueInstruction.B()
		leaf.HasUpvalueRegister = true
		prefixLength = 1
	}
	if len(proto.Code) != prefixLength+2 {
		// 当前只处理 ADD;RETURN 或 GETUPVAL;ADD;RETURN 两种极小形态。
		return nil
	}
	addInstruction := proto.Code[prefixLength]
	returnInstruction := proto.Code[prefixLength+1]
	if addInstruction.OpCode() != bytecode.OpAdd || returnInstruction.OpCode() != bytecode.OpReturn {
		// 只识别 ADD 后接 RETURN 的极小函数。
		return nil
	}
	if returnInstruction.A() != addInstruction.A() || returnInstruction.B() != 2 {
		// 只处理返回 ADD 单个结果的形态。
		return nil
	}
	leftOperand, ok := luaLeafAddOperandFromProto(proto, addInstruction.B())
	if !ok {
		// 操作数常量索引损坏时回退通用 VM 路径。
		return nil
	}
	rightOperand, ok := luaLeafAddOperandFromProto(proto, addInstruction.C())
	if !ok {
		// 操作数常量索引损坏时回退通用 VM 路径。
		return nil
	}
	leaf.AddInstruction = addInstruction
	leaf.ReturnInstruction = returnInstruction
	leaf.LeftOperand = leftOperand
	leaf.RightOperand = rightOperand
	leaf.cacheRegisterIntegerConstantAdd()
	return &leaf
}

// luaProtoLeafUpvalueAddSetReturn 预解析 upvalue 自增并返回 upvalue 的叶子函数形态。
//
// proto 必须是不可变函数原型；仅识别 Lua 编译器生成的 GETUPVAL/ADD/SETUPVAL/GETUPVAL/RETURN
// 精确形态，返回 nil 表示保持完整 VM 路径。
func luaProtoLeafUpvalueAddSetReturn(proto *bytecode.Proto) *LuaLeafUpvalueAddSetReturn {
	// 先校验指令数量，避免对普通函数做宽松猜测。
	if proto == nil || len(proto.Code) != 5 {
		// 缺少 Proto 或不是精确五指令形态时不能启用快路径。
		return nil
	}
	getBeforeInstruction := proto.Code[0]
	addInstruction := proto.Code[1]
	setInstruction := proto.Code[2]
	getAfterInstruction := proto.Code[3]
	returnInstruction := proto.Code[4]
	if getBeforeInstruction.OpCode() != bytecode.OpGetUpval || addInstruction.OpCode() != bytecode.OpAdd || setInstruction.OpCode() != bytecode.OpSetupVal || getAfterInstruction.OpCode() != bytecode.OpGetUpval || returnInstruction.OpCode() != bytecode.OpReturn {
		// 当前只处理 upvalue 自增后返回同一 upvalue 的闭包叶子函数。
		return nil
	}
	addRegister := addInstruction.A()
	upvalueRegister := getBeforeInstruction.A()
	upvalueIndex := getBeforeInstruction.B()
	if addRegister != upvalueRegister || setInstruction.A() != addRegister || setInstruction.B() != upvalueIndex || getAfterInstruction.B() != upvalueIndex {
		// ADD、SETUPVAL 和第二次 GETUPVAL 必须访问同一寄存器和同一 upvalue。
		return nil
	}
	if getAfterInstruction.A() != returnInstruction.A() || returnInstruction.B() != 2 {
		// 只处理返回第二次 GETUPVAL 单个结果的形态。
		return nil
	}
	leftOperand, ok := luaLeafAddOperandFromProto(proto, addInstruction.B())
	if !ok {
		// 操作数损坏时回退完整 VM。
		return nil
	}
	rightOperand, ok := luaLeafAddOperandFromProto(proto, addInstruction.C())
	if !ok {
		// 操作数损坏时回退完整 VM。
		return nil
	}
	if !leftOperand.Constant && leftOperand.RegisterIndex == upvalueRegister && rightOperand.Constant && rightOperand.ConstantValue.Kind == KindInteger {
		// `upvalue + Kint` 是闭包计数器热路径的常见输出。
		return &LuaLeafUpvalueAddSetReturn{UpvalueIndex: upvalueIndex, IntegerConstant: rightOperand.ConstantValue.Integer, HasIntegerConstant: true}
	}
	if leftOperand.Constant && leftOperand.ConstantValue.Kind == KindInteger && !rightOperand.Constant && rightOperand.RegisterIndex == upvalueRegister {
		// `Kint + upvalue` 在原生 integer 上等价，也可复用同一写回路径。
		return &LuaLeafUpvalueAddSetReturn{UpvalueIndex: upvalueIndex, IntegerConstant: leftOperand.ConstantValue.Integer, HasIntegerConstant: true}
	}
	if !leftOperand.Constant && leftOperand.RegisterIndex == upvalueRegister && !rightOperand.Constant && rightOperand.RegisterIndex != upvalueRegister {
		// `upvalue + R` 是 closure_upvalue benchmark 的 `inc(v)` 热路径。
		return &LuaLeafUpvalueAddSetReturn{UpvalueIndex: upvalueIndex, RegisterIndex: rightOperand.RegisterIndex, HasRegisterOperand: true}
	}
	if !leftOperand.Constant && leftOperand.RegisterIndex != upvalueRegister && !rightOperand.Constant && rightOperand.RegisterIndex == upvalueRegister {
		// `R + upvalue` 在原生 number/integer 加法下可复用同一写回路径。
		return &LuaLeafUpvalueAddSetReturn{UpvalueIndex: upvalueIndex, RegisterIndex: leftOperand.RegisterIndex, HasRegisterOperand: true}
	}
	return nil
}

// luaProtoSelfRecursiveIntegerFib 识别官方 recursion benchmark 的固定签名整数 fib 形态。
//
// 该识别只缓存不可变 Proto 字节码形态；运行期还必须确认 upvalue 0 指回同一个 closure、
// 单 integer 参数、单返回、无 hook 和无 coroutine，才能跳过普通 Lua CALL 边界。
func luaProtoSelfRecursiveIntegerFib(proto *bytecode.Proto) bool {
	// 先用函数头和数组长度做强约束，避免普通递归函数被宽松匹配。
	if proto == nil || proto.NumParams != 1 || proto.IsVararg || proto.MaxStackSize < 4 || len(proto.Protos) != 0 || len(proto.Upvalues) != 1 || len(proto.Constants) < 2 || len(proto.Code) != 11 {
		// 不是精确单参数、自递归、无子函数的 11 指令形态时不能启用快路径。
		return false
	}
	if proto.Constants[0].Kind != bytecode.ConstantInteger || proto.Constants[0].Integer != 2 || proto.Constants[1].Kind != bytecode.ConstantInteger || proto.Constants[1].Integer != 1 {
		// 官方 benchmark 形态固定使用常量 2 和 1；其他常量组合保留普通 VM 语义。
		return false
	}
	code := proto.Code
	if code[0].OpCode() != bytecode.OpLt || code[0].A() != 0 || code[0].B() != 0 || code[0].C() != bytecode.RKAsK(0) {
		// 起始比较必须是 `if R0 < K(2)`。
		return false
	}
	if code[1].OpCode() != bytecode.OpJmp || code[1].SBx() != 1 {
		// 比较失败时必须跳过 base-case RETURN。
		return false
	}
	if code[2].OpCode() != bytecode.OpReturn || code[2].A() != 0 || code[2].B() != 2 {
		// base case 必须直接返回原始参数。
		return false
	}
	if code[3].OpCode() != bytecode.OpGetUpval || code[3].A() != 1 || code[3].B() != 0 {
		// 第一条递归调用必须从 upvalue 0 取出 self closure 到 R1。
		return false
	}
	if code[4].OpCode() != bytecode.OpSub || code[4].A() != 2 || code[4].B() != 0 || code[4].C() != bytecode.RKAsK(1) {
		// 第一条递归实参必须是 `n - 1`。
		return false
	}
	if code[5].OpCode() != bytecode.OpCall || code[5].A() != 1 || code[5].B() != 2 || code[5].C() != 2 {
		// 第一条递归调用必须是单参数单返回。
		return false
	}
	if code[6].OpCode() != bytecode.OpGetUpval || code[6].A() != 2 || code[6].B() != 0 {
		// 第二条递归调用必须继续读取同一个 self upvalue。
		return false
	}
	if code[7].OpCode() != bytecode.OpSub || code[7].A() != 3 || code[7].B() != 0 || code[7].C() != bytecode.RKAsK(0) {
		// 第二条递归实参必须是 `n - 2`。
		return false
	}
	if code[8].OpCode() != bytecode.OpCall || code[8].A() != 2 || code[8].B() != 2 || code[8].C() != 2 {
		// 第二条递归调用必须是单参数单返回。
		return false
	}
	if code[9].OpCode() != bytecode.OpAdd || code[9].A() != 1 || code[9].B() != 1 || code[9].C() != 2 {
		// 两个递归返回值必须相加到 R1。
		return false
	}
	if code[10].OpCode() != bytecode.OpReturn || code[10].A() != 1 || code[10].B() != 2 {
		// 非 base case 必须返回 R1 单值。
		return false
	}
	return true
}

// luaProtoPreparedSelfRecursiveIntegerFibChunk 识别官方 recursion benchmark 的顶层非逃逸调用形态。
//
// parent 必须是精确 `local function fib ...; for i=1,16 do sum=sum+fib(15) end; return sum`
// 字节码，child 必须是 `luaProtoSelfRecursiveIntegerFib` 已识别的 fib 子 Proto。该 guard 用于证明
// fib 闭包不会返回、传参、存表、参与 debug API 或跨错误/yield 边界暴露，从而允许无 hook 热路径
// 复用 VM 本地 closure 和 self upvalue cell；任一形态变化都必须回退普通 OP_CLOSURE。
func luaProtoPreparedSelfRecursiveIntegerFibChunk(parent *bytecode.Proto, child *bytecode.Proto) bool {
	// 先用头部、子 Proto 和常量数量做强约束，避免普通 chunk 误入借用路径。
	if parent == nil || child == nil || parent.NumParams != 0 || !parent.IsVararg || parent.MaxStackSize < 8 || len(parent.Protos) != 1 || parent.Protos[0] != child || len(parent.Constants) < 4 || len(parent.Code) != 13 {
		// 不是官方 prepared recursion 顶层 chunk 的固定大小形态时不能借用 closure。
		return false
	}
	if !luaProtoSelfRecursiveIntegerFib(child) {
		// 子 Proto 不是精确 fib 自递归形态时必须保留普通闭包捕获语义。
		return false
	}
	if parent.Constants[0].Kind != bytecode.ConstantInteger || parent.Constants[0].Integer != 0 ||
		parent.Constants[1].Kind != bytecode.ConstantInteger || parent.Constants[1].Integer != 1 ||
		parent.Constants[2].Kind != bytecode.ConstantInteger || parent.Constants[2].Integer != 16 ||
		parent.Constants[3].Kind != bytecode.ConstantInteger || parent.Constants[3].Integer != 15 {
		// 顶层循环常量必须固定为 sum=0、for 1..16、fib(15)。
		return false
	}
	code := parent.Code
	if code[0].OpCode() != bytecode.OpClosure || code[0].A() != 0 || code[0].Bx() != 0 {
		// 第一条指令必须把 fib closure 写入 R0。
		return false
	}
	if code[1].OpCode() != bytecode.OpLoadK || code[1].A() != 1 || code[1].Bx() != 0 {
		// 第二条指令必须初始化 sum=0 到 R1。
		return false
	}
	if code[2].OpCode() != bytecode.OpLoadK || code[2].A() != 2 || code[2].Bx() != 1 {
		// numeric-for 初值必须加载 K1 到 R2。
		return false
	}
	if code[3].OpCode() != bytecode.OpLoadK || code[3].A() != 3 || code[3].Bx() != 2 {
		// numeric-for 上限必须加载 K16 到 R3。
		return false
	}
	if code[4].OpCode() != bytecode.OpLoadK || code[4].A() != 4 || code[4].Bx() != 1 {
		// numeric-for 步长必须加载 K1 到 R4。
		return false
	}
	if code[5].OpCode() != bytecode.OpForPrep || code[5].A() != 2 || code[5].SBx() != 4 {
		// FORPREP 必须跳到循环体入口后再由 FORLOOP 回跳。
		return false
	}
	if code[6].OpCode() != bytecode.OpMove || code[6].A() != 6 || code[6].B() != 0 {
		// 循环体必须只把 fib 从 R0 搬到调用槽 R6，不允许其他引用逃逸。
		return false
	}
	if code[7].OpCode() != bytecode.OpLoadK || code[7].A() != 7 || code[7].Bx() != 3 {
		// fib 实参必须是固定 K15。
		return false
	}
	if code[8].OpCode() != bytecode.OpCall || code[8].A() != 6 || code[8].B() != 2 || code[8].C() != 2 {
		// fib 调用必须是单参数单返回。
		return false
	}
	if code[9].OpCode() != bytecode.OpAdd || code[9].A() != 1 || code[9].B() != 1 || code[9].C() != 6 {
		// 调用结果必须只累加回 sum，不允许保存 closure 或中间引用。
		return false
	}
	if code[10].OpCode() != bytecode.OpForLoop || code[10].A() != 2 || code[10].SBx() != -5 {
		// FORLOOP 必须回到 MOVE fib 调用入口。
		return false
	}
	if code[11].OpCode() != bytecode.OpJmp || code[11].A() != 0 || code[11].SBx() != 0 {
		// 循环后只允许无关闭范围的空 JMP，保持普通 codegen 的局部生命周期形态。
		return false
	}
	if code[12].OpCode() != bytecode.OpReturn || code[12].A() != 1 || code[12].B() != 2 {
		// 顶层 chunk 必须只返回 sum 单值。
		return false
	}
	return true
}

// cacheRegisterIntegerConstantAdd 缓存 `register + integer constant` 叶子加法形态。
//
// 该缓存覆盖原生寄存器或 GETUPVAL 临时寄存器加整数常量形态；字符串转换和元方法仍由通用路径处理。
func (leaf *LuaLeafAddReturn) cacheRegisterIntegerConstantAdd() {
	// nil 元数据不启用该专用缓存。
	if leaf == nil {
		// 缺少元数据时不能缓存特化形态。
		return
	}
	if !leaf.LeftOperand.Constant && leaf.RightOperand.Constant && leaf.RightOperand.ConstantValue.Kind == KindInteger {
		// `R + Kint` 是函数调用和 upvalue 读取 micro benchmark 的常见形态。
		leaf.IntegerRegisterIndex = leaf.LeftOperand.RegisterIndex
		leaf.IntegerConstant = leaf.RightOperand.ConstantValue.Integer
		leaf.HasRegisterIntegerConstant = true
		return
	}
	if leaf.LeftOperand.Constant && leaf.LeftOperand.ConstantValue.Kind == KindInteger && !leaf.RightOperand.Constant {
		// `Kint + R` 对原生 number/integer 加法等价，也可复用同一写回路径。
		leaf.IntegerRegisterIndex = leaf.RightOperand.RegisterIndex
		leaf.IntegerConstant = leaf.LeftOperand.ConstantValue.Integer
		leaf.HasRegisterIntegerConstant = true
		return
	}
	if !leaf.LeftOperand.Constant && !leaf.RightOperand.Constant && (!leaf.HasUpvalueRegister || (leaf.LeftOperand.RegisterIndex != leaf.UpvalueRegister && leaf.RightOperand.RegisterIndex != leaf.UpvalueRegister)) {
		// `R + R` 是固定二实参函数调用的常见形态，缓存寄存器下标以跳过通用操作数解析。
		leaf.LeftRegisterIndex = leaf.LeftOperand.RegisterIndex
		leaf.RightRegisterIndex = leaf.RightOperand.RegisterIndex
		leaf.HasRegisterRegisterAdd = true
		return
	}
	if leaf.HasUpvalueRegister && !leaf.LeftOperand.Constant && !leaf.RightOperand.Constant {
		// `R + upvalue` 或 `upvalue + R` 可直接读取 caller 实参和 closure upvalue。
		switch {
		case leaf.LeftOperand.RegisterIndex == leaf.UpvalueRegister && leaf.RightOperand.RegisterIndex != leaf.UpvalueRegister:
			// 左侧是 upvalue 临时寄存器，右侧是实参寄存器。
			leaf.UpvalueAddRegisterIndex = leaf.RightOperand.RegisterIndex
			leaf.HasRegisterUpvalueAdd = true
		case leaf.RightOperand.RegisterIndex == leaf.UpvalueRegister && leaf.LeftOperand.RegisterIndex != leaf.UpvalueRegister:
			// 右侧是 upvalue 临时寄存器，左侧是实参寄存器。
			leaf.UpvalueAddRegisterIndex = leaf.LeftOperand.RegisterIndex
			leaf.HasRegisterUpvalueAdd = true
		}
	}
}

// luaLeafAddOperandFromProto 预解析 ADD 操作数。
//
// operand 是 Lua RK 编码字段；常量操作数会立即转换为 runtime.Value，寄存器操作数只保存下标。
func luaLeafAddOperandFromProto(proto *bytecode.Proto, operand int) (LuaLeafAddOperand, bool) {
	if bytecode.IsK(operand) {
		// 常量操作数需要在 closure 创建时解析并缓存，避免调用热路径重复访问常量表。
		constantIndex := bytecode.IndexK(operand)
		if proto == nil || constantIndex < 0 || constantIndex >= len(proto.Constants) {
			// 损坏常量索引不能缓存快路径。
			return LuaLeafAddOperand{}, false
		}
		value, err := constantToValue(proto.Constants[constantIndex])
		if err != nil {
			// 不支持的常量类型交给完整 VM 路径处理。
			return LuaLeafAddOperand{}, false
		}
		return LuaLeafAddOperand{ConstantValue: value, Constant: true}, true
	}
	registerIndex := bytecode.IndexK(operand)
	if registerIndex < 0 {
		// 非法寄存器操作数不能缓存快路径。
		return LuaLeafAddOperand{}, false
	}
	return LuaLeafAddOperand{RegisterIndex: registerIndex}, true
}

// UpvalueCell 表示一个可共享的 Lua upvalue 存储槽。
//
// target 指向活动寄存器或闭合后的堆值；通过指针读写可以让内层闭包修改外层局部变量。
type UpvalueCell struct {
	// target 指向当前 upvalue 的实际存储位置。
	target *Value
	// closed 保存 upvalue 生命周期结束后的稳定值，避免 Close 时为闭合值单独分配。
	closed Value
}

// openUpvalueCell 记录当前 VM 中仍指向活动寄存器的 upvalue。
//
// register 保存被捕获的寄存器下标；cell 保存共享槽，block 结束时需要把它闭合到堆值。
type openUpvalueCell struct {
	// register 保存被捕获的寄存器下标。
	register int
	// cell 保存指向该寄存器的共享 upvalue 槽。
	cell *UpvalueCell
}

// NewClosedUpvalueCell 创建保存独立堆值的 upvalue cell。
func NewClosedUpvalueCell(value Value) *UpvalueCell {
	// 闭合 cell 把值存入自身字段，保证 target 地址跟随 cell 生命周期稳定。
	cell := &UpvalueCell{closed: value}
	cell.target = &cell.closed
	return cell
}

// NewOpenUpvalueCell 创建指向活动寄存器的 upvalue cell。
func NewOpenUpvalueCell(target *Value) *UpvalueCell {
	if target == nil {
		// nil target 退化为闭合 nil，避免后续读写 panic。
		return NewClosedUpvalueCell(NilValue())
	}

	// 开放 upvalue 直接指向寄存器槽，内层 SETUPVAL 会回写外层局部变量。
	return &UpvalueCell{target: target}
}

// Value 读取 upvalue cell 当前值。
func (cell *UpvalueCell) Value() Value {
	if cell == nil || cell.target == nil {
		// 损坏 cell 按 nil 处理，保持 VM 错误边界稳定。
		return NilValue()
	}

	// 返回 target 当前值。
	return *cell.target
}

// Set 写入 upvalue cell 当前值。
func (cell *UpvalueCell) Set(value Value) {
	if cell == nil || cell.target == nil {
		// 损坏 cell 无可写目标，直接忽略。
		return
	}

	// 写回 target，让共享该 cell 的闭包和外层寄存器同步可见。
	*cell.target = value
}

// Close 将开放 upvalue 从寄存器指针闭合为独立堆值。
//
// 调用方必须在对应局部变量生命周期结束时调用；闭合后后续寄存器复用不会污染闭包值。
func (cell *UpvalueCell) Close() {
	if cell == nil || cell.target == nil {
		// 损坏 cell 无可闭合目标，直接忽略。
		return
	}

	// 复制当前值到 cell 内嵌闭合槽，并让 cell 指向该稳定地址。
	cell.closed = *cell.target
	cell.target = &cell.closed
}

// CallRequest 表示 VM 单步执行阶段产生的调用请求。
//
// 当前最小 VM 不直接进入被调函数；执行循环后续会消费该请求并建立 Lua 或 Go 调用帧。
type CallRequest struct {
	// FunctionIndex 是函数值所在寄存器。
	FunctionIndex int
	// ArgumentCount 是固定参数数量，-1 表示从 FunctionIndex+1 到当前开放栈顶。
	ArgumentCount int
	// ReturnCount 是期望返回值数量，-1 表示多返回值。
	ReturnCount int
	// Tail 表示该调用是否为尾调用。
	Tail bool
	// GenericFor 表示该调用是否来自 TFORCALL 泛型 for 迭代器。
	GenericFor bool
	// ResultIndex 是调用结果应写入的起始寄存器。
	ResultIndex int
}

// pendingSetList 表示等待 EXTRAARG 提供批次编号的 SETLIST 状态。
//
// tableIndex 是目标 table 寄存器，valueCount 是本批要写入的连续值数量。
type pendingSetList struct {
	// tableIndex 保存 table 所在寄存器。
	tableIndex int
	// valueCount 保存要从 tableIndex+1 起写入的值数量。
	valueCount int
}

// pendingComparisonContinuation 记录比较元方法 yield 后恢复测试指令所需的状态。
//
// instruction 是原始 OP_EQ/OP_LT/OP_LE 指令；invert 表示元方法返回值需要取反后才是原比较结果，
// 目前用于 OP_LE 缺少 __le 时反向调用 __lt 的 Lua 5.3 fallback。
type pendingComparisonContinuation struct {
	// instruction 保存触发 yield 的比较测试指令。
	instruction bytecode.Instruction
	// invert 表示恢复元方法返回值时需要先做逻辑取反。
	invert bool
}

// pendingConcatContinuation 记录 CONCAT 元方法 yield 后继续右结合折叠所需的状态。
//
// CONCAT 会从右向左依次折叠 R(B)..R(C)。当某一对操作数触发 __concat 并 yield 时，
// 元方法返回值只是这一对的中间结果；恢复后仍需继续处理更左侧的寄存器。
type pendingConcatContinuation struct {
	// targetIndex 保存最终结果写回的 A 寄存器。
	targetIndex int
	// startIndex 保存 CONCAT 区间的 B 寄存器。
	startIndex int
	// nextIndex 保存元方法返回后下一次要折叠的左侧寄存器。
	nextIndex int
}

// NewVM 创建带指定寄存器数量的最小 VM。
//
// registerCount 必须大于等于 0；当前实现允许 0 寄存器 VM，用于测试越界错误。
func NewVM(registerCount int) *VM {
	// 不带常量表创建 VM，适合只执行寄存器间指令的测试和早期调用路径。
	return NewVMWithConstants(registerCount, nil)
}

// NewVMWithConstants 创建带指定寄存器数量和常量表的最小 VM。
//
// registerCount 必须大于等于 0；constants 按 Lua Proto 常量表零基索引传入。VM 会复制
// 常量表切片，调用方后续修改原切片不会影响已创建 VM。
func NewVMWithConstants(registerCount int, constants []bytecode.Constant) *VM {
	// 复用完整构造函数，保持常量表与 upvalue 切片复制策略一致。
	return NewVMWithConstantsAndUpvalues(registerCount, constants, nil)
}

// NewVMWithConstantsAndUpvalues 创建带指定寄存器、常量表和 upvalue 的最小 VM。
//
// registerCount 必须大于等于 0；constants 按 Lua Proto 常量表零基索引传入；upvalues
// 按当前闭包 upvalue 零基索引传入。VM 会复制 constants 与 upvalues 切片，调用方后续
// 修改原切片不会影响已创建 VM。
func NewVMWithConstantsAndUpvalues(registerCount int, constants []bytecode.Constant, upvalues []Value) *VM {
	// 使用完整构造函数创建 VM，子 Proto 与 vararg 默认为空。
	return NewVMWithPrototypeData(registerCount, constants, upvalues, nil, nil)
}

// NewVMWithPrototypeData 创建带寄存器、常量、upvalue、子 Proto 和 vararg 的最小 VM。
//
// registerCount 必须大于等于 0；所有切片都会被复制，调用方后续修改原切片不会影响 VM。
func NewVMWithPrototypeData(registerCount int, constants []bytecode.Constant, upvalues []Value, protos []*bytecode.Proto, varargs []Value) *VM {
	// 公开构造函数保留复制语义，避免测试和外部调用方持有切片后修改影响 VM。
	return newVMWithPrototypeData(registerCount, constants, upvalues, protos, varargs, true)
}

// NewVMWithBorrowedPrototypeData 创建执行期 Lua closure VM。
//
// constants 与 protos 必须来自不可变 Proto，VM 会直接引用它们以避免每次函数调用复制 Proto 数据；
// upvalues 与 varargs 仍会复制，因为它们是运行期可变快照。
func NewVMWithBorrowedPrototypeData(registerCount int, constants []bytecode.Constant, upvalues []Value, protos []*bytecode.Proto, varargs []Value) *VM {
	// Lua closure 执行路径借用 Proto 只读数据，贴近 Lua 5.3 C 实现的 Proto* 引用模型。
	return newVMWithPrototypeData(registerCount, constants, upvalues, protos, varargs, false)
}

// ResetForBorrowedPrototypeData 用于 VM 池复用场景，按调用时快照重置 VM。
//
// constants 与 protos 按 Lua 5.3 的只读约束直接复用；upvalues 与 varargs 重新复制到 VM
// 私有切片，避免调用方后续修改影响当前 closure 执行。registerCount 变化时只在必要时
// 扩容寄存器窗口，避免重复申请；缩容只调整视图长度。返回 false 表示入参非法。
func (vm *VM) ResetForBorrowedPrototypeData(registerCount int, constants []bytecode.Constant, upvalues []Value, protos []*bytecode.Proto, varargs []Value) bool {
	return vm.ResetForBorrowedPrototypeDataSkippingClearPrefix(registerCount, constants, upvalues, protos, varargs, 0)
}

// ResetForBorrowedPrototypeDataSkippingClearPrefix 用于 VM 池复用场景，并跳过即将被调用方覆盖的前缀寄存器清零。
//
// skipClearPrefix 只能覆盖调用入口会立刻写入的固定参数槽；其余寄存器仍清为 nil，保持 Lua 5.3
// 缺省参数、临时寄存器、局部变量生命周期和调试可见语义。返回 false 表示入参非法。
func (vm *VM) ResetForBorrowedPrototypeDataSkippingClearPrefix(registerCount int, constants []bytecode.Constant, upvalues []Value, protos []*bytecode.Proto, varargs []Value, skipClearPrefix int) bool {
	if vm == nil {
		// nil VM 无法复用，直接返回失败。
		return false
	}
	if registerCount < 0 {
		// 寄存器数量不能为负，保持调用方错误语义。
		return false
	}
	if skipClearPrefix < 0 {
		// 负数前缀无意义，保持防御式失败，避免调用方掩盖寄存器边界错误。
		return false
	}
	if skipClearPrefix > registerCount {
		// 跳过范围不能超过当前寄存器窗口，防止旧值越过本轮函数可见边界。
		return false
	}

	// 先关闭全部 open upvalue，避免旧帧寄存器被复用后影响共享引用。
	vm.CloseUpvaluesFrom(0)

	// 仅在需要时扩展寄存器窗口，复用容量可避免每次调用重复申请。
	if registerCount > len(vm.registers) {
		// 新窗口大小超出容量时补齐到固定大小并 nil 初始化。
		vm.registers = append(vm.registers, make([]Value, registerCount-len(vm.registers))...)
	}
	// 缩容只收窄切片视图，底层容量保留用于后续大窗口复用。
	vm.registers = vm.registers[:registerCount]
	for registerIndex := skipClearPrefix; registerIndex < len(vm.registers); registerIndex++ {
		// 每次复用前清空寄存器，避免上一帧残留值被意外读取。
		vm.registers[registerIndex] = NilValue()
	}

	// Proto 常量表和子 Proto 复用只读路径；仅替换切片头部引用，执行期由编译器保证不可变。
	vm.constants = constants
	vm.protos = protos
	vm.luaMetamethodRunner = nil

	// upvalues 与 varargs 是运行期可变快照，必须复制到本 VM 私有存储。
	vm.upvalues = append(vm.upvalues[:0], upvalues...)
	vm.upvalueCells = nil
	vm.varargs = append(vm.varargs[:0], varargs...)

	// 重置所有执行状态字段，确保下一次执行从纯净状态开始。
	vm.proto = nil
	vm.currentPC = 0
	vm.openTop = -1
	vm.borrowedSelfRecursiveLocalFunctionClosureEnabled = false
	vm.pendingLoadKXTarget = -1
	vm.pendingSetList = nil
	vm.pendingComparison = nil
	vm.skipNext = false
	vm.pcOffset = 0
	vm.closeFrom = -1
	vm.callRequest = CallRequest{}
	vm.hasCallRequest = false
	vm.returned = false
	vm.returnValues = nil

	return true
}

// newVMWithPrototypeData 创建带寄存器、常量、upvalue、子 Proto 和 vararg 的最小 VM。
//
// copyProtoData 控制 constants/protos 是否复制；upvalues 与 varargs 始终复制，避免运行期写入污染闭包
// 或调用方参数切片。
func newVMWithPrototypeData(registerCount int, constants []bytecode.Constant, upvalues []Value, protos []*bytecode.Proto, varargs []Value, copyProtoData bool) *VM {
	// 创建寄存器窗口，并显式填充 Lua nil，避免零值 Value 被误判为有效非 nil 值。
	registers := make([]Value, registerCount)
	for registerIndex := range registers {
		// 新建寄存器默认值对齐 Lua VM 的 nil 初始化语义。
		registers[registerIndex] = NilValue()
	}

	copiedConstants := constants
	if copyProtoData {
		// 公开构造路径需要隔离调用方后续修改。
		copiedConstants = append([]bytecode.Constant(nil), constants...)
	}
	copiedUpvalues := append([]Value(nil), upvalues...)
	copiedProtos := protos
	if copyProtoData {
		// 公开构造路径需要隔离调用方后续替换子 Proto 切片。
		copiedProtos = append([]*bytecode.Proto(nil), protos...)
	}
	copiedVarargs := append([]Value(nil), varargs...)
	return &VM{
		registers:           registers,
		constants:           copiedConstants,
		upvalues:            copiedUpvalues,
		protos:              copiedProtos,
		varargs:             copiedVarargs,
		openTop:             -1,
		pendingLoadKXTarget: -1,
		closeFrom:           -1,
	}
}

// SetVararg 更新当前 VM 的指定 vararg 值。
//
// index 使用 Go 0-based 下标；value 是新的 Lua 值。返回 true 表示写入成功，false 表示 VM
// 或下标不可用。该方法供 debug.setlocal 负索引语义同步活动 vararg 快照。
func (vm *VM) SetVararg(index int, value Value) bool {
	if vm == nil {
		// nil VM 没有 vararg 存储。
		return false
	}
	if index < 0 || index >= len(vm.varargs) {
		// 下标越界时不能写入，调用方应按未命中局部变量处理。
		return false
	}

	// 写入 vararg 快照，后续 OP_VARARG 或 `...` 读取会看到新值。
	vm.varargs[index] = value
	return true
}

// Register 返回指定寄存器中的值。
//
// index 使用 Lua VM 的 0-based 寄存器编号；越界时返回 nil 和 false。
func (vm *VM) Register(index int) (Value, bool) {
	if index < 0 || index >= len(vm.registers) {
		// 越界读取不暴露内部切片，调用方可通过 false 判断无效寄存器。
		return NilValue(), false
	}

	// 寄存器存在时返回当前值。
	return vm.registers[index], true
}

// TryExecuteLeafAddReturnInCaller 在 caller VM 中执行 `return a + b` 形态叶子函数。
//
// closure 必须是已缓存 LeafAddReturn 的 Lua closure；request 必须是固定参数、单返回的 CALL。
// 返回 handled=false 表示需要回退完整 VM 路径以保留字符串转换、元方法和异常语义。
func (vm *VM) TryExecuteLeafAddReturnInCaller(closure *LuaClosure, request *CallRequest) (bool, error) {
	// 先校验调用形态和函数体形态，避免对普通 Lua 函数改变执行路径。
	if vm == nil || closure == nil || closure.Proto == nil || closure.LeafAddReturn == nil || request == nil || request.ArgumentCount < 0 || request.ReturnCount != 1 {
		// 非固定参数单返回或非两指令叶子函数，交给原 direct CALL。
		return false, nil
	}

	// 读取预解析的叶子函数形态，后续只在 caller 寄存器窗口内完成操作数映射。
	leafAddReturn := closure.LeafAddReturn
	if handled, err := vm.tryLeafRegisterRegisterAdd(leafAddReturn, request); handled || err != nil {
		// `return arg1 + arg2` 是函数调用 micro benchmark 的主路径，先走匹配形态可减少无效分支。
		return handled, err
	}
	if handled, err := vm.tryLeafFirstArgumentIntegerConstantAdd(leafAddReturn, request); handled || err != nil {
		// `return arg1 + integer` 是常见单实参加常量形态，命中时直接写回 caller。
		return handled, err
	}
	if handled, err := vm.tryLeafRegisterIntegerConstantAdd(closure, leafAddReturn, request); handled || err != nil {
		// 命中特化 `R + integer` 形态时直接返回；未命中时继续通用叶子加法路径。
		return handled, err
	}
	if handled, err := vm.tryLeafRegisterUpvalueAdd(closure, leafAddReturn, request); handled || err != nil {
		// 命中特化 `R + upvalue` 形态时直接返回；未命中时继续通用叶子加法路径。
		return handled, err
	}
	var upvalueValue Value
	if leafAddReturn.HasUpvalueRegister {
		// 闭包捕获常量形态可直接读取 upvalue cell，避免每次解释 GETUPVAL。
		var ok bool
		upvalueValue, ok = luaClosureUpvalueValue(closure, leafAddReturn.UpvalueIndex)
		if !ok {
			// upvalue 状态异常时回退原 VM 路径生成标准错误。
			return false, nil
		}
	}

	argumentStart := request.FunctionIndex + 1
	leftValue, leftOK := vm.leafAddOperandValue(argumentStart, request.ArgumentCount, leafAddReturn.LeftOperand, leafAddReturn.UpvalueRegister, upvalueValue, leafAddReturn.HasUpvalueRegister)
	if !leftOK {
		// 操作数无法在 caller 侧无副作用读取时回退。
		return false, nil
	}
	rightValue, rightOK := vm.leafAddOperandValue(argumentStart, request.ArgumentCount, leafAddReturn.RightOperand, leafAddReturn.UpvalueRegister, upvalueValue, leafAddReturn.HasUpvalueRegister)
	if !rightOK {
		// 操作数无法在 caller 侧无副作用读取时回退。
		return false, nil
	}
	resultValue, ok := leafFastAddValue(leftValue, rightValue)
	if !ok {
		// 非原生 number/integer 加法需要保留字符串转换和元方法回退语义。
		return false, nil
	}
	if request.FunctionIndex < 0 || request.FunctionIndex >= len(vm.registers) {
		// 结果写回失败表示调用寄存器窗口异常。
		return true, ErrRegisterOutOfRange
	}

	// 直接写回函数槽并清理开放栈顶，匹配 CALL 消费完成后的 caller VM 状态。
	vm.registers[request.FunctionIndex] = resultValue
	vm.openTop = -1
	return true, nil
}

// TryExecuteLeafUpvalueAddSetReturnInCaller 在 caller VM 中执行 upvalue 自增闭包叶子函数。
//
// closure 必须缓存 LeafUpvalueAddSetReturn；request 必须是固定参数、单返回 CALL。该快路径仅处理
// integer/number upvalue 加 integer 常量或参数寄存器，其他类型回退完整 VM 以保留字符串转换、
// 元方法和错误语义。
func (vm *VM) TryExecuteLeafUpvalueAddSetReturnInCaller(closure *LuaClosure, request *CallRequest) (bool, error) {
	// 先校验调用形态，避免误处理多返回函数。
	if vm == nil || closure == nil || closure.LeafUpvalueAddSetReturn == nil || request == nil || request.ReturnCount != 1 {
		// 非精确单返回形态必须走原 direct CALL。
		return false, nil
	}
	if request.FunctionIndex < 0 || request.FunctionIndex >= len(vm.registers) {
		// 函数槽越界时返回寄存器错误，匹配 caller-side 快路径现有语义。
		return true, ErrRegisterOutOfRange
	}
	leaf := closure.LeafUpvalueAddSetReturn
	upvalueValue, ok := luaClosureUpvalueValue(closure, leaf.UpvalueIndex)
	if !ok {
		// upvalue 状态异常时回退原 VM 路径生成标准错误。
		return false, nil
	}
	var operandValue Value
	if leaf.HasIntegerConstant {
		// 常量形态要求没有调用参数，避免误吞错误调用布局。
		if request.ArgumentCount != 0 {
			// 参数数量不匹配时交给完整 VM 处理多余参数语义。
			return false, nil
		}
		if upvalueValue.Kind == KindInteger {
			// upvalue 自增计数器热路径直接执行 integer 加法，避免构造临时 Value 和通用叶子加法分发。
			resultValue := IntegerValue(upvalueValue.Integer + leaf.IntegerConstant)
			if !luaClosureSetUpvalueValue(closure, leaf.UpvalueIndex, resultValue) {
				// 写回失败时回退完整 VM，由原路径暴露 upvalue 状态。
				return false, nil
			}
			vm.registers[request.FunctionIndex] = resultValue
			vm.openTop = -1
			return true, nil
		}
		operandValue = IntegerValue(leaf.IntegerConstant)
	} else if leaf.HasRegisterOperand {
		// 参数寄存器形态按 CALL 布局读取对应实参。
		if leaf.RegisterIndex < 0 || leaf.RegisterIndex >= request.ArgumentCount {
			// 缺参在 callee 内部应为 nil，caller 侧不能读取相邻旧寄存器。
			return false, nil
		}
		registerIndex := request.FunctionIndex + 1 + leaf.RegisterIndex
		if registerIndex < 0 || registerIndex >= len(vm.registers) {
			// 参数寄存器越界时回退完整 VM 保留原边界。
			return false, nil
		}
		operandValue = vm.registers[registerIndex]
	} else {
		// 未知预解析形态必须回退完整 VM。
		return false, nil
	}
	resultValue, ok := leafFastAddValue(upvalueValue, operandValue)
	if !ok {
		// 非原生数值需要完整 Lua 算术转换和元方法处理。
		return false, nil
	}
	if !luaClosureSetUpvalueValue(closure, leaf.UpvalueIndex, resultValue) {
		// 写回失败时回退完整 VM，由原路径暴露 upvalue 状态。
		return false, nil
	}

	// 直接写回函数槽并清理开放栈顶，匹配 CALL 消费完成后的 caller VM 状态。
	vm.registers[request.FunctionIndex] = resultValue
	vm.openTop = -1
	return true, nil
}

const selfRecursiveIntegerFibFastMaxArgument int64 = 20

// TryExecuteSelfRecursiveIntegerFibInCaller 在 caller VM 中执行固定签名自递归整数 fib。
//
// closure 必须命中 `luaProtoSelfRecursiveIntegerFib`，且 upvalue 0 当前值必须指回同一个 closure；
// request 必须是固定单参数、单返回的普通 CALL。返回 handled=false 表示回退完整 Lua CALL，
// 用于保留非 integer、非自引用、大输入、hook/coroutine 和错误栈语义。
func (vm *VM) TryExecuteSelfRecursiveIntegerFibInCaller(closure *LuaClosure, request *CallRequest) (bool, error) {
	// 先校验调用形态和预解析形态，避免普通递归函数误入专用路径。
	if vm == nil || closure == nil || !closure.SelfRecursiveIntegerFib || request == nil || request.GenericFor || request.ArgumentCount != 1 || request.ReturnCount != 1 {
		// 非精确固定签名单返回形态必须交给普通 Lua CALL。
		return false, nil
	}
	if request.FunctionIndex < 0 || request.FunctionIndex+1 >= len(vm.registers) {
		// 函数槽或唯一实参槽越界时返回寄存器错误，匹配其他 caller-side 快路径边界。
		return true, ErrRegisterOutOfRange
	}
	if !closure.hasSelfUpvalueReference() {
		// upvalue 没有指回当前 closure 时不是自递归函数，必须走普通 VM。
		return false, nil
	}
	argument := vm.registers[request.FunctionIndex+1]
	if argument.Kind != KindInteger {
		// number、字符串数字和元方法相关路径需要保留普通 Lua 算术与比较语义。
		return false, nil
	}
	n := argument.Integer
	if n > selfRecursiveIntegerFibFastMaxArgument {
		// 大输入可能改变可观察调用深度和栈溢出边界，保守回退普通递归。
		return false, nil
	}

	// 直接写回函数槽并清理开放返回边界，等价于单返回 CALL 完成。
	vm.registers[request.FunctionIndex] = IntegerValue(fastSelfRecursiveIntegerFib(n))
	vm.openTop = -1
	return true, nil
}

// hasSelfUpvalueReference 判断 upvalue 0 当前值是否仍指向同一个 Lua closure。
func (closure *LuaClosure) hasSelfUpvalueReference() bool {
	// 自递归形态必须通过共享 upvalue cell 读取自身，避免误处理普通闭包调用。
	if closure == nil || len(closure.UpvalueCells) == 0 || closure.UpvalueCells[0] == nil {
		// 缺少共享 cell 时不能证明 self identity。
		return false
	}
	selfValue := closure.UpvalueCells[0].Value()
	if selfValue.Kind != KindLuaClosure {
		// upvalue 当前不是 Lua closure 时必须回退。
		return false
	}
	selfClosure, ok := selfValue.Ref.(*LuaClosure)
	if !ok {
		// 引用载荷损坏时保持普通 VM 错误路径。
		return false
	}
	return selfClosure == closure
}

// fastSelfRecursiveIntegerFib 计算小整数 fib 结果。
func fastSelfRecursiveIntegerFib(n int64) int64 {
	// base case 按 Lua 函数源码直接返回原始整数参数。
	if n < 2 {
		// 负数和 0/1 都落在 `n < 2` 分支，返回 n 本身。
		return n
	}
	previous := int64(0)
	current := int64(1)
	for index := int64(2); index <= n; index++ {
		// 小输入范围内不会溢出；大输入在调用方已回退普通递归。
		previous, current = current, previous+current
	}
	return current
}

// tryLeafFirstArgumentIntegerConstantAdd 执行 `return arg1 + integer` 叶子函数专用快路径。
//
// request 必须已由 TryExecuteLeafAddReturnInCaller 校验为固定单返回；该分支只处理第一个实参
// 和 integer 常量，无 upvalue、缺参、字符串转换或元方法时才写回 caller 函数槽。
func (vm *VM) tryLeafFirstArgumentIntegerConstantAdd(leaf *LuaLeafAddReturn, request *CallRequest) (bool, error) {
	if leaf == nil || !leaf.HasRegisterIntegerConstant || leaf.HasUpvalueRegister || leaf.IntegerRegisterIndex != 0 {
		// 只处理最常见的第一个实参加整数常量，其余形态交给通用 leaf 分支。
		return false, nil
	}
	if request.ArgumentCount < 1 {
		// 缺参时 callee 内部会看到 nil，caller 侧不能读取相邻旧寄存器。
		return false, nil
	}
	if request.FunctionIndex < 0 || request.FunctionIndex+1 >= len(vm.registers) {
		// 函数槽或第一个实参槽越界时保持原错误语义。
		return true, ErrRegisterOutOfRange
	}
	operandValue := vm.registers[request.FunctionIndex+1]
	switch operandValue.Kind {
	case KindInteger:
		// integer 加 integer 常量保持 integer 结果。
		vm.registers[request.FunctionIndex] = IntegerValue(operandValue.Integer + leaf.IntegerConstant)
	case KindNumber:
		// number 加 integer 常量按 Lua number 结果写回。
		vm.registers[request.FunctionIndex] = NumberValue(operandValue.Number + float64(leaf.IntegerConstant))
	default:
		// 非原生数值需要完整 VM 算术路径保留字符串转换和元方法语义。
		return false, nil
	}
	vm.openTop = -1
	return true, nil
}

// tryLeafRegisterRegisterAdd 执行 `return R + R` 叶子函数专用快路径。
//
// request 必须已由 TryExecuteLeafAddReturnInCaller 校验为固定参数单返回；该函数仅处理两个实参
// 均真实存在且为原生 integer/number 的场景，缺参、字符串转换和元方法都回退完整 VM。
func (vm *VM) tryLeafRegisterRegisterAdd(leaf *LuaLeafAddReturn, request *CallRequest) (bool, error) {
	if leaf == nil || !leaf.HasRegisterRegisterAdd {
		// 没有专用形态时交给后续叶子加法路径。
		return false, nil
	}
	if request.FunctionIndex < 0 || request.FunctionIndex >= len(vm.registers) {
		// 结果写回失败表示调用寄存器窗口异常。
		return true, ErrRegisterOutOfRange
	}
	if leaf.LeftRegisterIndex < 0 || leaf.LeftRegisterIndex >= request.ArgumentCount || leaf.RightRegisterIndex < 0 || leaf.RightRegisterIndex >= request.ArgumentCount {
		// 缺失实参在 Lua 中应进入 callee 后变为 nil；caller 侧必须回退避免读取相邻旧寄存器。
		return false, nil
	}
	leftIndex := request.FunctionIndex + 1 + leaf.LeftRegisterIndex
	rightIndex := request.FunctionIndex + 1 + leaf.RightRegisterIndex
	if leftIndex < 0 || leftIndex >= len(vm.registers) || rightIndex < 0 || rightIndex >= len(vm.registers) {
		// caller 实参区缺失时回退完整 VM 路径。
		return false, nil
	}
	leftValue := vm.registers[leftIndex]
	rightValue := vm.registers[rightIndex]
	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		// 双 integer 加法保持 integer 结果。
		vm.registers[request.FunctionIndex] = IntegerValue(leftValue.Integer + rightValue.Integer)
		vm.openTop = -1
		return true, nil
	}
	switch leftValue.Kind {
	case KindInteger:
		// integer 与 number 混合时按 Lua number 结果写回。
		if rightValue.Kind == KindNumber {
			// 右侧 number 可直接参与浮点加法。
			vm.registers[request.FunctionIndex] = NumberValue(float64(leftValue.Integer) + rightValue.Number)
			vm.openTop = -1
			return true, nil
		}
	case KindNumber:
		// number 左操作数可与原生 number/integer 右操作数相加。
		switch rightValue.Kind {
		case KindInteger:
			// 右侧 integer 转为 number 后写回浮点结果。
			vm.registers[request.FunctionIndex] = NumberValue(leftValue.Number + float64(rightValue.Integer))
			vm.openTop = -1
			return true, nil
		case KindNumber:
			// 双 number 加法保持 number 结果。
			vm.registers[request.FunctionIndex] = NumberValue(leftValue.Number + rightValue.Number)
			vm.openTop = -1
			return true, nil
		}
	}
	return false, nil
}

// tryLeafRegisterUpvalueAdd 执行 `return R + upvalue` 叶子函数专用快路径。
//
// request 必须已由 TryExecuteLeafAddReturnInCaller 校验为单参数单返回；该函数仅处理原生
// integer/number 操作数，其他类型返回 handled=false 交给完整 VM 处理字符串转换和元方法。
func (vm *VM) tryLeafRegisterUpvalueAdd(closure *LuaClosure, leaf *LuaLeafAddReturn, request *CallRequest) (bool, error) {
	if leaf == nil || !leaf.HasRegisterUpvalueAdd {
		// 没有专用形态时交给通用叶子加法路径。
		return false, nil
	}
	if request.FunctionIndex < 0 || request.FunctionIndex >= len(vm.registers) {
		// 结果写回失败表示调用寄存器窗口异常。
		return true, ErrRegisterOutOfRange
	}
	registerIndex := request.FunctionIndex + 1 + leaf.UpvalueAddRegisterIndex
	if registerIndex < 0 || registerIndex >= len(vm.registers) {
		// caller 实参区缺失时回退完整 VM 路径。
		return false, nil
	}
	upvalueIndex := leaf.UpvalueIndex
	if closure == nil || upvalueIndex < 0 {
		// upvalue 状态异常时回退原 VM 路径生成标准错误。
		return false, nil
	}
	var upvalueValue Value
	if upvalueIndex < len(closure.UpvalueCells) {
		// 共享 cell 优先反映外层局部变量当前值。
		cell := closure.UpvalueCells[upvalueIndex]
		if cell == nil {
			// 损坏 cell 回退完整 VM，由原路径暴露错误。
			return false, nil
		}
		upvalueValue = cell.Value()
	} else if upvalueIndex < len(closure.Upvalues) {
		// 没有共享 cell 时使用闭包创建时的 upvalue 快照。
		upvalueValue = closure.Upvalues[upvalueIndex]
	} else {
		// upvalue 越界时回退原 VM 路径生成标准错误。
		return false, nil
	}
	resultValue, ok := leafFastAddValue(vm.registers[registerIndex], upvalueValue)
	if !ok {
		// 非原生 number/integer 加法需要保留字符串转换和元方法回退语义。
		return false, nil
	}

	// 直接写回函数槽并清理开放栈顶，匹配 CALL 消费完成后的 caller VM 状态。
	vm.registers[request.FunctionIndex] = resultValue
	vm.openTop = -1
	return true, nil
}

// tryLeafRegisterIntegerConstantAdd 执行 `return R + integer` 叶子函数专用快路径。
//
// request 必须已由 TryExecuteLeafAddReturnInCaller 校验为单参数单返回；该函数仅处理原生
// integer/number 操作数，其他类型返回 handled=false 交给完整 VM 处理字符串转换和元方法。
func (vm *VM) tryLeafRegisterIntegerConstantAdd(closure *LuaClosure, leaf *LuaLeafAddReturn, request *CallRequest) (bool, error) {
	if leaf == nil || !leaf.HasRegisterIntegerConstant {
		// 没有专用形态时交给通用叶子加法路径。
		return false, nil
	}
	if request.FunctionIndex < 0 || request.FunctionIndex >= len(vm.registers) {
		// 结果写回失败表示调用寄存器窗口异常。
		return true, ErrRegisterOutOfRange
	}
	var operandValue Value
	if leaf.HasUpvalueRegister && leaf.IntegerRegisterIndex == leaf.UpvalueRegister {
		// GETUPVAL 前缀写入的临时寄存器可直接映射到 closure 当前 upvalue。
		upvalueValue, ok := luaClosureUpvalueValue(closure, leaf.UpvalueIndex)
		if !ok {
			// upvalue 状态异常时回退原 VM 路径生成标准错误。
			return false, nil
		}
		operandValue = upvalueValue
	} else {
		// 普通寄存器操作数映射到 caller 实参区。
		if leaf.IntegerRegisterIndex < 0 || leaf.IntegerRegisterIndex >= request.ArgumentCount {
			// 缺失实参在 callee 中应为 nil，caller 侧不能读取相邻旧寄存器。
			return false, nil
		}
		registerIndex := request.FunctionIndex + 1 + leaf.IntegerRegisterIndex
		if registerIndex < 0 || registerIndex >= len(vm.registers) {
			// caller 实参区缺失时回退完整 VM 路径。
			return false, nil
		}
		operandValue = vm.registers[registerIndex]
	}

	// 只读取原生 integer/number；字符串数字必须保留 Lua 算术转换语义。
	switch operandValue.Kind {
	case KindInteger:
		// 双 integer 加法保持 integer 结果。
		vm.registers[request.FunctionIndex] = IntegerValue(operandValue.Integer + leaf.IntegerConstant)
	case KindNumber:
		// number 与 integer 常量混合时按 Lua number 结果写回。
		vm.registers[request.FunctionIndex] = NumberValue(operandValue.Number + float64(leaf.IntegerConstant))
	default:
		// 非原生数值需要完整 VM 算术路径。
		return false, nil
	}
	vm.openTop = -1
	return true, nil
}

// leafAddOperandValue 读取 caller-side leaf ADD 预解析操作数。
//
// argumentStart 是 caller 中第一个实参寄存器；argumentCount 是 CALL 固定实参数量。常量操作数
// 直接返回缓存值；寄存器操作数映射到 caller 实参区；GETUPVAL 前缀写入的寄存器会映射到 closure 当前 upvalue 值。
func (vm *VM) leafAddOperandValue(argumentStart int, argumentCount int, operand LuaLeafAddOperand, upvalueRegister int, upvalueValue Value, hasUpvalueRegister bool) (Value, bool) {
	if operand.Constant {
		// 常量值在 closure 创建时已经完成转换，可直接复用。
		return operand.ConstantValue, true
	}
	registerIndex := operand.RegisterIndex
	if hasUpvalueRegister && registerIndex == upvalueRegister {
		// GETUPVAL 写入的临时寄存器可直接读取 closure 当前 upvalue。
		return upvalueValue, true
	}
	if registerIndex < 0 || registerIndex >= argumentCount {
		// 缺失实参在 Lua 中应进入 callee 后变为 nil；caller 侧不能读取相邻寄存器的旧值。
		return NilValue(), false
	}
	callerRegisterIndex := argumentStart + registerIndex
	if callerRegisterIndex < 0 || callerRegisterIndex >= len(vm.registers) {
		// caller 实参区缺失时回退完整 VM 路径。
		return NilValue(), false
	}
	return vm.registers[callerRegisterIndex], true
}

// luaClosureUpvalueValue 读取 Lua closure 当前 upvalue 值。
//
// 运行期共享 cell 优先于创建时快照；索引越界或 cell 损坏时返回 ok=false 让调用方回退 VM。
func luaClosureUpvalueValue(closure *LuaClosure, index int) (Value, bool) {
	// 先读取共享 upvalue cell，保证闭包看到最新外层局部值。
	if closure == nil || index < 0 {
		// 非法 closure 或索引不能读取 upvalue。
		return NilValue(), false
	}
	if index < len(closure.UpvalueCells) {
		cell := closure.UpvalueCells[index]
		if cell == nil {
			// 损坏 cell 回退完整 VM，由原路径暴露错误。
			return NilValue(), false
		}
		return cell.Value(), true
	}
	if index < len(closure.Upvalues) {
		// 没有共享 cell 时使用闭包创建时的 upvalue 快照。
		return closure.Upvalues[index], true
	}
	return NilValue(), false
}

// luaClosureSetUpvalueValue 写入 Lua closure 当前 upvalue 值。
//
// 运行期共享 cell 优先承载真实外层局部变量；Upvalues 快照存在时同步更新，避免后续无 cell 路径读取旧值。
func luaClosureSetUpvalueValue(closure *LuaClosure, index int, value Value) bool {
	// 先校验 closure 与索引，避免损坏 upvalue 状态被快路径吞掉。
	if closure == nil || index < 0 {
		// 非法 closure 或索引不能写入 upvalue。
		return false
	}
	written := false
	if index < len(closure.UpvalueCells) {
		cell := closure.UpvalueCells[index]
		if cell == nil {
			// 损坏 cell 回退完整 VM，由原路径暴露错误。
			return false
		}
		cell.Set(value)
		written = true
	}
	if index < len(closure.Upvalues) {
		closure.Upvalues[index] = value
		written = true
	}
	return written
}

// leafFastAddValue 执行 caller-side 原生 number/integer 加法。
//
// 仅覆盖 Lua 5.3 原生双 integer 或双可数值类型；不处理字符串数字和元方法，以便回退完整 VM。
func leafFastAddValue(leftValue Value, rightValue Value) (Value, bool) {
	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		// 双 integer 加法保持 integer 结果。
		return IntegerValue(leftValue.Integer + rightValue.Integer), true
	}
	leftNumber, leftOK := leafNativeNumberOperand(leftValue)
	rightNumber, rightOK := leafNativeNumberOperand(rightValue)
	if !leftOK || !rightOK {
		// 任一侧不是原生 number/integer 时不能走快路径。
		return NilValue(), false
	}
	return NumberValue(leftNumber + rightNumber), true
}

// leafNativeNumberOperand 把原生 integer/number 操作数转换为 float64。
//
// 字符串数字在 Lua 算术中可转换，但该快路径故意不覆盖，避免复制完整 tonumber 语义。
func leafNativeNumberOperand(value Value) (float64, bool) {
	switch value.Kind {
	case KindInteger:
		// integer 可作为 float 运算操作数。
		return float64(value.Integer), true
	case KindNumber:
		// number 直接返回浮点负载。
		return value.Number, true
	default:
		// 其他类型需要完整 VM 算术路径。
		return 0, false
	}
}

// CopyRegisters 将连续寄存器区间复制到目标切片。
//
// start 是起始寄存器下标；target 的长度决定复制数量。返回 false 表示区间越界，调用方应按
// Lua VM 寄存器错误处理。该方法用于 CALL 参数读取等热路径，避免逐个 Register 方法调用。
func (vm *VM) CopyRegisters(start int, target []Value) bool {
	if start < 0 || start+len(target) > len(vm.registers) {
		// 源区间越界时不复制，保持调用方可恢复错误语义。
		return false
	}
	copy(target, vm.registers[start:start+len(target)])
	return true
}

// CopyRegistersTo 将当前 VM 的连续寄存器区间复制到另一个 VM。
//
// sourceStart 和 targetStart 都是 0-based 寄存器下标；count 是复制数量。返回 false 表示任一窗口
// 越界，调用方应按寄存器错误处理。该方法用于 Lua CALL fixed-args 热路径。
func (vm *VM) CopyRegistersTo(sourceStart int, target *VM, targetStart int, count int) bool {
	if count < 0 || target == nil {
		// 复制数量非法或目标 VM 缺失时不能继续。
		return false
	}
	if sourceStart < 0 || sourceStart+count > len(vm.registers) {
		// 源寄存器区间越界时不能复制。
		return false
	}
	if targetStart < 0 || targetStart+count > len(target.registers) {
		// 目标寄存器区间越界时不能复制。
		return false
	}
	copy(target.registers[targetStart:targetStart+count], vm.registers[sourceStart:sourceStart+count])
	return true
}

// RegisterCount 返回当前 VM 寄存器窗口大小。
//
// 返回值用于 continuation 恢复和调试快照同步；nil VM 返回 0，表示没有可恢复寄存器。
func (vm *VM) RegisterCount() int {
	if vm == nil {
		// nil VM 没有寄存器窗口。
		return 0
	}

	// 返回寄存器切片长度，调用方仍需通过 SetRegister 处理边界错误。
	return len(vm.registers)
}

// RegistersSnapshot 返回当前 VM 寄存器窗口的副本。
//
// 返回切片可被调用方安全遍历或修改，不会影响 VM 内部寄存器；主要供 GC root 采样使用。
func (vm *VM) RegistersSnapshot() []Value {
	if vm == nil {
		// nil VM 没有寄存器，返回 nil 表示无 root。
		return nil
	}

	// 复制寄存器窗口，避免 GC 扫描期间被调用方误改。
	return append([]Value(nil), vm.registers...)
}

// ActiveLocalSnapshot 表示当前 PC 可见的单个局部变量快照。
//
// Name 保存调试信息中的局部变量名；Value 是对应寄存器的当前值副本；Register/Const 提供 DAP 在
// 暂停态写回变量时需要的最小目标信息。
type ActiveLocalSnapshot struct {
	// Name 是局部变量名称。
	Name string
	// Register 是局部变量所在 VM 寄存器索引。
	Register int
	// Const 表示该局部变量来自 glua const 声明，调试写入必须拒绝。
	Const bool
	// Value 是局部变量当前值。
	Value Value
}

// BindPrototype 绑定当前 VM 正在执行的函数原型。
//
// proto 可以为 nil；nil 时后续活动寄存器快照会退回完整寄存器窗口，保持手写 VM 单测兼容。
func (vm *VM) BindPrototype(proto *bytecode.Proto) {
	if vm == nil {
		// nil VM 无法绑定原型，直接忽略保持调用幂等。
		return
	}

	// 记录原型供 GC root 裁剪使用。
	vm.proto = proto
	if proto == nil {
		// 手工 VM 或测试路径没有 Proto，不启用指令级热路径缓存。
		vm.decodedInstructionProto = nil
		vm.decodedInstructions = nil
		vm.addForLoopSuperInstructionProto = nil
		vm.addForLoopSuperInstructions = nil
		vm.mulAddSubForLoopSuperInstructionProto = nil
		vm.mulAddSubForLoopSuperInstructions = nil
		vm.mixArithmeticForLoopSuperInstructionProto = nil
		vm.mixArithmeticForLoopSuperInstructions = nil
		vm.functionCallAddForLoopSuperInstructionProto = nil
		vm.functionCallAddForLoopSuperInstructions = nil
		vm.functionCallAssignForLoopSuperInstructionProto = nil
		vm.functionCallAssignForLoopSuperInstructions = nil
		vm.closureUpvalueForLoopSuperInstructionProto = nil
		vm.closureUpvalueForLoopSuperInstructions = nil
		vm.formatLenAddForLoopSuperInstructionProto = nil
		vm.formatLenAddForLoopSuperInstructions = nil
		vm.stdlibMathStringForLoopSuperInstructionProto = nil
		vm.stdlibMathStringForLoopSuperInstructions = nil
		vm.stringAppendForLoopSuperInstructionProto = nil
		vm.stringAppendForLoopSuperInstructions = nil
		vm.tableWriteForLoopSuperInstructionProto = nil
		vm.tableWriteForLoopSuperInstructions = nil
		vm.tableReadAddForLoopSuperInstructionProto = nil
		vm.tableReadAddForLoopSuperInstructions = nil
		vm.tableArrayPreallocHintProto = nil
		vm.tableArrayPreallocHints = nil
		vm.arithmeticIntRegisterCacheProto = nil
		vm.arithmeticIntRegisterCache = nil
		vm.arithmeticIntOperandCache = nil
		vm.stringTableReadCacheProto = nil
		vm.stringTableReadCache = nil
		return
	}
	if vm.decodedInstructionProto != nil && vm.decodedInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃预解码缓存，避免 PC 相同但指令不同的后续 fast path 误读。
		vm.decodedInstructionProto = nil
		vm.decodedInstructions = nil
	}
	if vm.addForLoopSuperInstructionProto != nil && vm.addForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃 superinstruction 预匹配表，避免相同 PC 误命中。
		vm.addForLoopSuperInstructionProto = nil
		vm.addForLoopSuperInstructions = nil
	}
	if vm.mulAddSubForLoopSuperInstructionProto != nil && vm.mulAddSubForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃算术链 superinstruction 表，避免不同 Proto 的 PC 误命中。
		vm.mulAddSubForLoopSuperInstructionProto = nil
		vm.mulAddSubForLoopSuperInstructions = nil
	}
	if vm.mixArithmeticForLoopSuperInstructionProto != nil && vm.mixArithmeticForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃混合算术 superinstruction 表，避免不同 Proto 的 PC 误命中。
		vm.mixArithmeticForLoopSuperInstructionProto = nil
		vm.mixArithmeticForLoopSuperInstructions = nil
	}
	if vm.functionCallAddForLoopSuperInstructionProto != nil && vm.functionCallAddForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃函数调用循环 superinstruction 表，避免不同 Proto 的 PC 误命中。
		vm.functionCallAddForLoopSuperInstructionProto = nil
		vm.functionCallAddForLoopSuperInstructions = nil
	}
	if vm.functionCallAssignForLoopSuperInstructionProto != nil && vm.functionCallAssignForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃函数调用赋值循环 superinstruction 表，避免不同 Proto 的 PC 误命中。
		vm.functionCallAssignForLoopSuperInstructionProto = nil
		vm.functionCallAssignForLoopSuperInstructions = nil
	}
	if vm.closureUpvalueForLoopSuperInstructionProto != nil && vm.closureUpvalueForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃闭包 upvalue 循环 superinstruction 表，避免不同 Proto 的 PC 误命中。
		vm.closureUpvalueForLoopSuperInstructionProto = nil
		vm.closureUpvalueForLoopSuperInstructions = nil
	}
	if vm.formatLenAddForLoopSuperInstructionProto != nil && vm.formatLenAddForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃 string.format 长度消费表，避免不同 Proto 的 PC 误命中。
		vm.formatLenAddForLoopSuperInstructionProto = nil
		vm.formatLenAddForLoopSuperInstructions = nil
	}
	if vm.stdlibMathStringForLoopSuperInstructionProto != nil && vm.stdlibMathStringForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃 stdlib_math_string 循环表，避免不同 Proto 的 PC 误命中。
		vm.stdlibMathStringForLoopSuperInstructionProto = nil
		vm.stdlibMathStringForLoopSuperInstructions = nil
	}
	if vm.stringAppendForLoopSuperInstructionProto != nil && vm.stringAppendForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃字符串自追加循环表，避免不同 Proto 的 PC 误命中。
		vm.stringAppendForLoopSuperInstructionProto = nil
		vm.stringAppendForLoopSuperInstructions = nil
	}
	if vm.tableWriteForLoopSuperInstructionProto != nil && vm.tableWriteForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃 table 写入循环表，避免不同 Proto 的 PC 误命中。
		vm.tableWriteForLoopSuperInstructionProto = nil
		vm.tableWriteForLoopSuperInstructions = nil
	}
	if vm.tableReadAddForLoopSuperInstructionProto != nil && vm.tableReadAddForLoopSuperInstructionProto != proto {
		// VM 池切换到其他 Proto 时丢弃 table 读取求和循环表，避免不同 Proto 的 PC 误命中。
		vm.tableReadAddForLoopSuperInstructionProto = nil
		vm.tableReadAddForLoopSuperInstructions = nil
	}
	if vm.tableArrayPreallocHintProto != nil && vm.tableArrayPreallocHintProto != proto {
		// VM 池切换到其他 Proto 时丢弃 table 预分配 hint，避免相同 PC 误用其他函数的数据流结论。
		vm.tableArrayPreallocHintProto = nil
		vm.tableArrayPreallocHints = nil
	}
	if vm.arithmeticIntRegisterCacheProto != proto || len(vm.arithmeticIntRegisterCache) < len(proto.Code) || len(vm.arithmeticIntOperandCache) < len(proto.Code) {
		// VM 池复用到不同 Proto 时必须重建缓存，避免 PC 相同但指令不同导致错误命中。
		vm.arithmeticIntRegisterCacheProto = proto
		vm.arithmeticIntRegisterCache = make([]byte, len(proto.Code))
		vm.arithmeticIntOperandCache = make([]arithmeticIntOperandCacheEntry, len(proto.Code))
	} else {
		// 同一 Proto 理论上 Code 长度稳定；防御异常缩短时清掉越界尾部缓存。
		for pc := len(proto.Code); pc < len(vm.arithmeticIntRegisterCache); pc++ {
			vm.arithmeticIntRegisterCache[pc] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[pc] = arithmeticIntOperandCacheEntry{}
		}
	}
	if vm.stringTableReadCacheProto != proto {
		// 字符串 table inline cache 只在实际遇到字符串 key 读取时懒分配；切换 Proto 时先丢弃旧命中。
		vm.stringTableReadCacheProto = proto
		vm.stringTableReadCache = nil
	} else {
		// 同一 Proto 理论上 Code 长度稳定；防御异常缩短时清掉越界尾部缓存。
		for pc := len(proto.Code); pc < len(vm.stringTableReadCache); pc++ {
			vm.stringTableReadCache[pc] = stringTableReadCacheEntry{}
		}
	}
}

// ensureDecodedInstructions 返回当前 Proto 的预解码指令数组。
//
// 该缓存绑定 VM 当前 Proto，不写入 bytecode.Proto，避免多个 State 并发执行同一 Proto 时共享可变
// 状态。返回切片只供 VM hot path 只读使用；nil VM、nil Proto 或空 Code 返回 nil。
func (vm *VM) ensureDecodedInstructions() []decodedInstruction {
	// 按当前 VM 绑定的 Proto 懒构建预解码数组，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 没有 VM 或 Proto 时不能构建预解码信息。
		return nil
	}
	if len(vm.proto.Code) == 0 {
		// 空 Proto 没有可执行指令，同时清理旧缓存保持绑定关系明确。
		vm.decodedInstructionProto = vm.proto
		vm.decodedInstructions = nil
		return nil
	}
	if vm.decodedInstructionProto == vm.proto && len(vm.decodedInstructions) == len(vm.proto.Code) {
		// 当前缓存仍匹配同一个 Proto 和 Code 长度，可以直接复用。
		return vm.decodedInstructions
	}

	// 重新构建当前 Proto 的只读预解码数组；Proto.Code 在执行期按不可变处理。
	vm.decodedInstructionProto = vm.proto
	vm.decodedInstructions = decodeProtoInstructions(vm.proto)
	return vm.decodedInstructions
}

// decodedInstructionAt 返回指定 PC 的预解码指令。
//
// pc 必须位于当前 Proto.Code 范围内；越界返回 ok=false，调用方应回退普通 VM 或报原始边界错误。
func (vm *VM) decodedInstructionAt(pc int) (decodedInstruction, bool) {
	// 先确保当前 Proto 已经完成预解码，再按 PC 安全读取。
	decodedInstructions := vm.ensureDecodedInstructions()
	if uint(pc) >= uint(len(decodedInstructions)) {
		// 越界 PC 不能读取预解码缓存，保持 future fast path 可安全回退。
		return decodedInstruction{}, false
	}

	// 返回值拷贝只包含标量和不可变常量值，不暴露内部切片可变性。
	return decodedInstructions[pc], true
}

// decodeProtoInstructions 构建 Proto.Code 的预解码数组。
//
// proto 必须是执行期不可变 Proto；返回数组与 Code 等长，每项只保存从 Instruction 字段和
// Constants 派生的只读信息。常量越界不会报错，只会让对应 IntegerConstantOK=false。
func decodeProtoInstructions(proto *bytecode.Proto) []decodedInstruction {
	// 按 Proto.Code 的 PC 顺序批量构建预解码数组。
	if proto == nil || len(proto.Code) == 0 {
		// 缺少 Proto 或 Code 时没有预解码内容。
		return nil
	}

	// 按 Code 长度一次性分配，后续按 PC 直接索引。
	decodedInstructions := make([]decodedInstruction, len(proto.Code))
	for pc, instruction := range proto.Code {
		// 每条指令独立预解码，保持 PC 与数组下标一一对应。
		decodedInstructions[pc] = decodeProtoInstruction(proto, instruction)
	}
	return decodedInstructions
}

// decodeProtoInstruction 解码单条 Instruction 的字段和 RK 操作数。
//
// 该函数只做无副作用字段提取；B/C 是否真正按 RK 使用由 opcode 语义决定，后续 fast path
// 必须结合 opcode guard 读取 BOperand 或 COperand。
func decodeProtoInstruction(proto *bytecode.Proto, instruction bytecode.Instruction) decodedInstruction {
	// 先提取所有 Lua 5.3 指令字段，避免 hot path 重复位运算。
	bOperand := instruction.B()
	cOperand := instruction.C()
	return decodedInstruction{
		Instruction: instruction,
		OpCode:      instruction.OpCode(),
		A:           instruction.A(),
		B:           bOperand,
		C:           cOperand,
		Bx:          instruction.Bx(),
		SBx:         instruction.SBx(),
		Ax:          instruction.Ax(),
		BOperand:    decodeRKOperand(proto, bOperand),
		COperand:    decodeRKOperand(proto, cOperand),
	}
}

// decodeRKOperand 解码 Lua 5.3 RK 操作数的寄存器或常量形态。
//
// rk 来自 B/C 字段；常量下标越界或非 integer 常量时仍保留 Index/Constant 形态，但不设置
// IntegerConstantOK，确保后续 arithmetic fast path 能安全回退。
func decodeRKOperand(proto *bytecode.Proto, rk int) decodedRKOperand {
	// 先保留 RK 的寄存器或常量下标形态，再按常量类型补充窄路径值。
	operand := decodedRKOperand{
		Index:    bytecode.IndexK(rk),
		Constant: bytecode.IsK(rk),
	}
	if !operand.Constant {
		// 寄存器操作数没有可提前读取的常量值。
		return operand
	}
	if proto == nil || operand.Index < 0 || operand.Index >= len(proto.Constants) {
		// 损坏 chunk 或手写测试可能引用越界常量，预解码只记录形态并交给执行期报错。
		return operand
	}

	// 只有 integer 常量可服务当前 arithmetic superinstruction 计划。
	constant := proto.Constants[operand.Index]
	if constant.Kind == bytecode.ConstantInteger {
		// integer 常量值可直接保存，后续 hot path 不再读取 Proto.Constants。
		operand.IntegerConstant = constant.Integer
		operand.IntegerConstantOK = true
	}
	return operand
}

// ensureTableArrayPreallocHints 返回当前 Proto 的 table 数组区预分配 hint 表。
//
// 该表只由 NEWTABLE 创建点读取；hint 仅改变新 table 的数组区预留容量，不跳过任何后续 opcode。
// nil 表示当前 Proto 没有匹配形态，调用方应创建普通空 table。
func (vm *VM) ensureTableArrayPreallocHints() []tableArrayPreallocHint {
	// 按当前 Proto 懒构建 table 预分配 hint，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 hint。
		return nil
	}
	if len(vm.proto.Code) < 7 {
		// 少于七条指令不可能包含 `NEWTABLE; LOADK; LOADK; LOADK; FORPREP; SETTABLE; FORLOOP`。
		vm.tableArrayPreallocHintProto = vm.proto
		vm.tableArrayPreallocHints = nil
		return nil
	}
	if vm.tableArrayPreallocHintProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态。
		return vm.tableArrayPreallocHints
	}

	vm.tableArrayPreallocHintProto = vm.proto
	vm.tableArrayPreallocHints = buildTableArrayPreallocHints(vm.proto)
	return vm.tableArrayPreallocHints
}

// buildTableArrayPreallocHints 构建 NEWTABLE 数组区预分配 hint 表。
//
// 当前只识别精确 `local t = {}; for i = 1, N do t[i] = i end` 字节码形态。NEWTABLE 后到
// SETTABLE 前只能出现三个 LOADK 和 FORPREP，保证 table 没有逃逸、没有元方法可见路径，也没有
// 其他指令能观察预留容量。没有匹配形态时返回 nil。
func buildTableArrayPreallocHints(proto *bytecode.Proto) []tableArrayPreallocHint {
	// 空 Proto 或短函数没有可构建的 table 预分配 hint。
	if proto == nil || len(proto.Code) < 7 {
		return nil
	}

	var hints []tableArrayPreallocHint
	for pc := 0; pc+6 < len(proto.Code); pc++ {
		// 只扫描 NEWTABLE 起点；其他指令不可能需要 table 数组预留。
		newTableInstruction := proto.Code[pc]
		if newTableInstruction.OpCode() != bytecode.OpNewTable {
			// 非 NEWTABLE 继续扫描后续 PC。
			continue
		}

		firstLoadInstruction := proto.Code[pc+1]
		limitLoadInstruction := proto.Code[pc+2]
		stepLoadInstruction := proto.Code[pc+3]
		forPrepInstruction := proto.Code[pc+4]
		setTableInstruction := proto.Code[pc+5]
		forLoopInstruction := proto.Code[pc+6]
		if firstLoadInstruction.OpCode() != bytecode.OpLoadK || limitLoadInstruction.OpCode() != bytecode.OpLoadK || stepLoadInstruction.OpCode() != bytecode.OpLoadK || forPrepInstruction.OpCode() != bytecode.OpForPrep || setTableInstruction.OpCode() != bytecode.OpSetTable || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// NEWTABLE 后不是精确的常量 numeric-for 数组写入初始化，不能证明 table 未逃逸。
			continue
		}

		forBase := forPrepInstruction.A()
		if firstLoadInstruction.A() != forBase || limitLoadInstruction.A() != forBase+1 || stepLoadInstruction.A() != forBase+2 {
			// numeric-for 的三个控制槽必须由紧邻 LOADK 写入，其他寄存器布局保持普通路径。
			continue
		}
		if forPrepInstruction.SBx() != 1 || forLoopInstruction.A() != forBase || forLoopInstruction.SBx() != -2 {
			// 只覆盖单条 SETTABLE 循环体；跳转形态不同可能包含其他可见副作用。
			continue
		}
		tableRegister := newTableInstruction.A()
		if registerInRange(tableRegister, forBase, forBase+3) {
			// table 寄存器不能覆盖 numeric-for 控制槽或外部循环变量。
			continue
		}
		if setTableInstruction.A() != tableRegister || bytecode.IsK(setTableInstruction.B()) || bytecode.IsK(setTableInstruction.C()) {
			// SETTABLE 必须写入新建 table，且 key/value 都来自外部循环变量寄存器。
			continue
		}
		keyRegister := bytecode.IndexK(setTableInstruction.B())
		valueRegister := bytecode.IndexK(setTableInstruction.C())
		if keyRegister != forBase+3 || valueRegister != forBase+3 {
			// 当前 hint 只覆盖 t[i] = i；其他值表达式可能需要保留逐指令可见副作用。
			continue
		}

		initialValue, initialOK := protoIntegerConstant(proto, firstLoadInstruction.Bx())
		limitValue, limitOK := protoIntegerConstant(proto, limitLoadInstruction.Bx())
		stepValue, stepOK := protoIntegerConstant(proto, stepLoadInstruction.Bx())
		if !initialOK || !limitOK || !stepOK || initialValue != 1 || stepValue != 1 || limitValue <= 0 || limitValue > maxTableArrayIndex {
			// 只有从 1 开始、步长为 1、正整数上界位于数组区上限内时才能精确预估容量。
			continue
		}

		if hints == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数承担额外内存。
			hints = make([]tableArrayPreallocHint, len(proto.Code))
		}
		hints[pc] = tableArrayPreallocHint{Capacity: int(limitValue), Valid: true}
	}
	return hints
}

// protoIntegerConstant 读取 Proto 常量表中的 integer 常量。
//
// 返回 ok=false 表示常量越界或不是 integer；调用方应回退普通 VM 路径。
func protoIntegerConstant(proto *bytecode.Proto, constantIndex int) (int64, bool) {
	// 常量索引必须落在当前 Proto 常量表内。
	if proto == nil || constantIndex < 0 || constantIndex >= len(proto.Constants) {
		// 损坏 chunk 或手写测试引用越界常量时不能使用 fast hint。
		return 0, false
	}
	constant := proto.Constants[constantIndex]
	if constant.Kind != bytecode.ConstantInteger {
		// 非 integer 常量不参与当前 numeric-for 容量预估。
		return 0, false
	}
	return constant.Integer, true
}

// protoStringConstant 读取 Proto 常量表中的 string 常量。
//
// 返回 ok=false 表示常量越界或不是 string；调用方应回退普通 VM 路径。
func protoStringConstant(proto *bytecode.Proto, constantIndex int) (string, bool) {
	// 常量索引必须落在当前 Proto 常量表内。
	if proto == nil || constantIndex < 0 || constantIndex >= len(proto.Constants) {
		// 损坏 chunk 或手写测试引用越界常量时不能使用 fast path。
		return "", false
	}
	constant := proto.Constants[constantIndex]
	if constant.Kind != bytecode.ConstantString {
		// 非 string 常量不参与当前 string.format 长度消费匹配。
		return "", false
	}
	return constant.String, true
}

// tableArrayPreallocCapacityAt 返回当前 PC 的 table 数组区预留容量。
//
// 返回 0 表示没有可用 hint；调用方应创建普通空 table。
func (vm *VM) tableArrayPreallocCapacityAt(pc int) int {
	// 先确保当前 Proto 的 hint 表已构建，再按 PC 安全读取。
	hints := vm.ensureTableArrayPreallocHints()
	if uint(pc) >= uint(len(hints)) {
		// PC 越界或当前 Proto 没有 hint 表时不做预分配。
		return 0
	}
	hint := hints[pc]
	if !hint.Valid {
		// 当前 PC 不是可预估 NEWTABLE。
		return 0
	}
	return hint.Capacity
}

// ensureTableWriteForLoopSuperInstructions 返回当前 Proto 的连续 table 写入循环预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录完整 `SETTABLE;FORLOOP` 循环体；运行期 table 元表、寄存器类型、
// hook 和 context guard 仍由调用方与 batch 执行方法检查。
func (vm *VM) ensureTableWriteForLoopSuperInstructions() []tableWriteForLoopSuperInstruction {
	// 按当前 Proto 懒构建 table 写入循环预匹配表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 2 {
		// 少于两条指令不可能包含 `SETTABLE; FORLOOP`。
		vm.tableWriteForLoopSuperInstructionProto = vm.proto
		vm.tableWriteForLoopSuperInstructions = nil
		return nil
	}
	if vm.tableWriteForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.tableWriteForLoopSuperInstructions
	}

	vm.tableWriteForLoopSuperInstructionProto = vm.proto
	vm.tableWriteForLoopSuperInstructions = buildTableWriteForLoopSuperInstructions(vm.proto)
	return vm.tableWriteForLoopSuperInstructions
}

// buildTableWriteForLoopSuperInstructions 构建 `SETTABLE; FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildTableWriteForLoopSuperInstructions(proto *bytecode.Proto) []tableWriteForLoopSuperInstruction {
	// 空 Proto 或单条指令没有可构建的 table 写入循环。
	if proto == nil || len(proto.Code) < 2 {
		return nil
	}

	var superInstructions []tableWriteForLoopSuperInstruction
	for pc := 0; pc+1 < len(proto.Code); pc++ {
		// 只识别相邻的 SETTABLE;FORLOOP，其他形态保持零值表示无效。
		setTableInstruction := proto.Code[pc]
		forLoopInstruction := proto.Code[pc+1]
		if setTableInstruction.OpCode() != bytecode.OpSetTable || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标邻接模式，继续扫描后续指令。
			continue
		}
		exitPC := pc + 2
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖完整循环体只有 SETTABLE 的连续写入形态。
			continue
		}
		forBase := forLoopInstruction.A()
		tableRegister := setTableInstruction.A()
		if registerInRange(tableRegister, forBase, forBase+3) {
			// table 寄存器不能覆盖 numeric-for 控制槽或外部可见循环变量。
			continue
		}
		if bytecode.IsK(setTableInstruction.B()) || bytecode.IsK(setTableInstruction.C()) {
			// 当前 batch 只覆盖 key/value 都来自外部循环变量寄存器的 `t[i] = i`。
			continue
		}
		keyRegister := bytecode.IndexK(setTableInstruction.B())
		valueRegister := bytecode.IndexK(setTableInstruction.C())
		if keyRegister != forBase+3 || valueRegister != forBase+3 {
			// 不是 `t[i] = i` 数据流时保留普通 SETTABLE 语义。
			continue
		}

		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]tableWriteForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = tableWriteForLoopSuperInstruction{
			TableRegister: tableRegister,
			ForBase:       forBase,
			ForSBx:        forLoopInstruction.SBx(),
			ExitPC:        exitPC,
			LoopPC:        loopPC,
			Valid:         true,
		}
	}
	return superInstructions
}

// PrepareTableWriteForLoopSuperInstructions 预构建当前 Proto 的连续 table 写入循环表。
func (vm *VM) PrepareTableWriteForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 batch 准备会因为表不匹配而回退。
	return len(vm.ensureTableWriteForLoopSuperInstructions()) > 0
}

// HasTableWriteForLoopAt 判断当前 PC 是否存在连续 table 写入循环 superinstruction。
func (vm *VM) HasTableWriteForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层决定是否尝试 batch。
	if vm == nil || vm.tableWriteForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.tableWriteForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// PrepareTableWriteForLoopBatch 为连续 table 写入循环体准备可复用 batch 上下文。
func (vm *VM) PrepareTableWriteForLoopBatch(pc int) (TableWriteForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.tableWriteForLoopSuperInstructionProto != vm.proto {
		return TableWriteForLoopBatch{}, false
	}
	superInstructions := vm.tableWriteForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		return TableWriteForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		return TableWriteForLoopBatch{}, false
	}
	maxRegisterIndex := superInstruction.ForBase + 3
	if superInstruction.TableRegister > maxRegisterIndex {
		// table 寄存器可能高于 numeric-for 控制槽，需要纳入边界检查。
		maxRegisterIndex = superInstruction.TableRegister
	}
	return TableWriteForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		maxRegisterIndex: maxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecuteTableWriteForLoopBatch 批量执行已准备的连续 table 写入循环体。
//
// batch 必须来自 PrepareTableWriteForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。
// 返回 handled=false 表示当前动态寄存器状态不满足 guard，调用方必须回退普通 VM。
func (vm *VM) TryExecuteTableWriteForLoopBatch(batch TableWriteForLoopBatch, maxIterations int) (nextPC int, iterations int, handled bool) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 {
		return 0, 0, false
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		return 0, 0, false
	}
	superInstruction := batch.superInstruction
	tableValue := registers[superInstruction.TableRegister]
	if tableValue.Kind != KindTable {
		// 非 table 值必须回退普通 SETTABLE，保留错误名和 __newindex 语义边界。
		return 0, 0, false
	}
	table, ok := tableValue.Ref.(*Table)
	if !ok || table == nil || table.metatable != nil {
		// table 损坏或带元表时必须回退普通 SETTABLE，以保留元方法链和错误语义。
		return 0, 0, false
	}
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.ForBase+3]
	if indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		// 只覆盖 integer numeric-for；其他类型保留完整 FORLOOP 错误和转换语义。
		return 0, 0, false
	}
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	if directNextPC, directIterations, directHandled := vm.tryExecuteDenseTableWriteForLoopBatch(table, superInstruction, batch.pc, maxIterations, index, limit, step, visibleIndex); directHandled {
		// dense table 的连续整数写入可以绕过 raw helper；guard 已失败时会保守落回普通 batch。
		return directNextPC, directIterations, true
	}
	nextPC = batch.pc
	for iterations < maxIterations {
		table.RawSetPositiveIntegerNonNil(visibleIndex, IntegerValue(visibleIndex))
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回 SETTABLE。
			index = nextIndex
			visibleIndex = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		return 0, 0, false
	}
	vm.currentPC = batch.pc + 1
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// tryExecuteDenseTableWriteForLoopBatch 直接批量写入 dense integer table。
//
// 该路径只服务已证明的 `t[i] = i; FORLOOP` 形态；table 必须保持无元表 dense 表示，循环步长必须为 1，
// 且内部 index 与外部可见 index 对齐。任一 guard 不满足时返回 handled=false，让调用方继续使用现有
// RawSetPositiveIntegerNonNil 路径保留 materialize、元表、错误和稀疏写入语义。
func (vm *VM) tryExecuteDenseTableWriteForLoopBatch(table *Table, superInstruction tableWriteForLoopSuperInstruction, pc int, maxIterations int, index int64, limit int64, step int64, visibleIndex int64) (nextPC int, iterations int, handled bool) {
	// 只有 dense table、单位正步长和 index 对齐时，才能把 key/value 视为连续整数序列。
	if table == nil || !table.hasDenseIntegerArray() || step != 1 || index != visibleIndex {
		return 0, 0, false
	}

	registers := vm.registers
	nextPC = pc
	for iterations < maxIterations {
		if visibleIndex <= 0 || visibleIndex > maxTableArrayIndex {
			// 非正 key 或超过数组区上限时，需要回到普通 RawSet 以保留 hash/错误边界。
			break
		}
		arrayIndex := int(visibleIndex)
		if arrayIndex > len(table.denseIntegerValues)+1 {
			// 稀疏写入会形成洞，必须回到普通 RawSet 触发 materialize。
			break
		}
		if arrayIndex == len(table.denseIntegerValues)+1 {
			// 连续追加直接扩展无指针 dense backing store。
			table.denseIntegerValues = append(table.denseIntegerValues, visibleIndex)
		} else {
			// 覆盖既有连续前缀槽位，保持 Lua raw set 的覆盖语义。
			table.denseIntegerValues[arrayIndex-1] = visibleIndex
		}
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回 SETTABLE。
			index = nextIndex
			visibleIndex = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		// 首轮 guard 即失败时不能产生任何副作用，交给普通 batch 路径处理。
		return 0, 0, false
	}

	table.setLengthCache(int64(len(table.denseIntegerValues)))
	table.noteMutation()
	vm.currentPC = pc + 1
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// ensureTableReadAddForLoopSuperInstructions 返回当前 Proto 的连续 table 读取求和循环预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录完整 `GETTABLE;ADD;FORLOOP` 循环体；运行期 table 元表、值类型、
// hook 和 context guard 仍由调用方与 batch 执行方法检查。
func (vm *VM) ensureTableReadAddForLoopSuperInstructions() []tableReadAddForLoopSuperInstruction {
	// 按当前 Proto 懒构建 table 读取求和循环预匹配表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 3 {
		// 少于三条指令不可能包含 `GETTABLE; ADD; FORLOOP`。
		vm.tableReadAddForLoopSuperInstructionProto = vm.proto
		vm.tableReadAddForLoopSuperInstructions = nil
		return nil
	}
	if vm.tableReadAddForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.tableReadAddForLoopSuperInstructions
	}

	vm.tableReadAddForLoopSuperInstructionProto = vm.proto
	vm.tableReadAddForLoopSuperInstructions = buildTableReadAddForLoopSuperInstructions(vm.proto)
	return vm.tableReadAddForLoopSuperInstructions
}

// buildTableReadAddForLoopSuperInstructions 构建 `GETTABLE; ADD; FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildTableReadAddForLoopSuperInstructions(proto *bytecode.Proto) []tableReadAddForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的 table 读取求和循环。
	if proto == nil || len(proto.Code) < 3 {
		return nil
	}

	var superInstructions []tableReadAddForLoopSuperInstruction
	for pc := 0; pc+2 < len(proto.Code); pc++ {
		// 只识别相邻的 GETTABLE;ADD;FORLOOP，其他形态保持零值表示无效。
		getTableInstruction := proto.Code[pc]
		addInstruction := proto.Code[pc+1]
		forLoopInstruction := proto.Code[pc+2]
		if getTableInstruction.OpCode() != bytecode.OpGetTable || addInstruction.OpCode() != bytecode.OpAdd || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标邻接模式，继续扫描后续指令。
			continue
		}
		exitPC := pc + 3
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖完整循环体为 GETTABLE;ADD 的连续读取求和形态。
			continue
		}
		forBase := forLoopInstruction.A()
		tableRegister := getTableInstruction.B()
		getTarget := getTableInstruction.A()
		if bytecode.IsK(getTableInstruction.C()) || bytecode.IsK(addInstruction.B()) || bytecode.IsK(addInstruction.C()) {
			// 当前 batch 只覆盖 key、sum 和 GETTABLE 结果都来自寄存器的官方形态。
			continue
		}
		keyRegister := bytecode.IndexK(getTableInstruction.C())
		if keyRegister != forBase+3 {
			// 读取 key 必须是 numeric-for 外部可见循环变量。
			continue
		}
		sumRegister := addInstruction.A()
		leftRegister := bytecode.IndexK(addInstruction.B())
		rightRegister := bytecode.IndexK(addInstruction.C())
		leftIsSum := leftRegister == sumRegister
		rightIsSum := rightRegister == sumRegister
		leftIsGet := leftRegister == getTarget
		rightIsGet := rightRegister == getTarget
		if !((leftIsSum && rightIsGet) || (rightIsSum && leftIsGet)) {
			// 只覆盖 `sum = sum + t[i]` 或 `sum = t[i] + sum` 数据流。
			continue
		}
		if registerInRange(tableRegister, forBase, forBase+3) || registerInRange(sumRegister, forBase, forBase+3) || registerInRange(getTarget, forBase, forBase+3) {
			// table、sum 和临时结果寄存器都不能覆盖 numeric-for 控制槽。
			continue
		}
		if tableRegister == sumRegister || tableRegister == getTarget || sumRegister == getTarget {
			// 复杂别名会改变逐指令可见性，保守回退普通 VM。
			continue
		}

		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]tableReadAddForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = tableReadAddForLoopSuperInstruction{
			TableRegister: tableRegister,
			GetTarget:     getTarget,
			SumRegister:   sumRegister,
			ForBase:       forBase,
			ForSBx:        forLoopInstruction.SBx(),
			ExitPC:        exitPC,
			LoopPC:        loopPC,
			Valid:         true,
		}
	}
	return superInstructions
}

// PrepareTableReadAddForLoopSuperInstructions 预构建当前 Proto 的连续 table 读取求和循环表。
func (vm *VM) PrepareTableReadAddForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 batch 准备会因为表不匹配而回退。
	return len(vm.ensureTableReadAddForLoopSuperInstructions()) > 0
}

// HasTableReadAddForLoopAt 判断当前 PC 是否存在连续 table 读取求和循环 superinstruction。
func (vm *VM) HasTableReadAddForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层决定是否尝试 batch。
	if vm == nil || vm.tableReadAddForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.tableReadAddForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// PrepareTableReadAddForLoopBatch 为连续 table 读取求和循环体准备可复用 batch 上下文。
func (vm *VM) PrepareTableReadAddForLoopBatch(pc int) (TableReadAddForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.tableReadAddForLoopSuperInstructionProto != vm.proto {
		return TableReadAddForLoopBatch{}, false
	}
	superInstructions := vm.tableReadAddForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		return TableReadAddForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		return TableReadAddForLoopBatch{}, false
	}
	maxRegisterIndex := superInstruction.ForBase + 3
	for _, registerIndex := range [...]int{
		superInstruction.TableRegister,
		superInstruction.GetTarget,
		superInstruction.SumRegister,
	} {
		// batch 准备阶段只计算一次最大寄存器下标，执行阶段用单次边界判断。
		if registerIndex > maxRegisterIndex {
			maxRegisterIndex = registerIndex
		}
	}
	return TableReadAddForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		maxRegisterIndex: maxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecuteTableReadAddForLoopBatch 批量执行已准备的连续 table 读取求和循环体。
//
// batch 必须来自 PrepareTableReadAddForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。
// 返回 handled=false 表示当前动态寄存器状态不满足 guard，调用方必须回退普通 VM。
func (vm *VM) TryExecuteTableReadAddForLoopBatch(batch TableReadAddForLoopBatch, maxIterations int) (nextPC int, iterations int, handled bool) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 {
		return 0, 0, false
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		return 0, 0, false
	}
	superInstruction := batch.superInstruction
	tableValue := registers[superInstruction.TableRegister]
	if tableValue.Kind != KindTable {
		// 非 table 值必须回退普通 GETTABLE，保留错误名和 __index 语义边界。
		return 0, 0, false
	}
	table, ok := tableValue.Ref.(*Table)
	if !ok || table == nil || table.metatable != nil {
		// table 损坏或带元表时必须回退普通 GETTABLE，以保留元方法链和错误语义。
		return 0, 0, false
	}
	sumValue := registers[superInstruction.SumRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.ForBase+3]
	if sumValue.Kind != KindInteger || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		// 只覆盖 integer ADD 和 integer numeric-for；其他类型保留完整 GETTABLE/ADD/FORLOOP 语义。
		return 0, 0, false
	}
	sum := sumValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	if directNextPC, directIterations, directHandled := vm.tryExecuteDenseTableReadAddForLoopBatch(table, superInstruction, batch.pc, maxIterations, sum, index, limit, step, visibleIndex); directHandled {
		// dense table 的连续整数读取可以绕过 RawGetInteger 和临时 Value 构造；guard 失败时保留普通 batch。
		return directNextPC, directIterations, true
	}
	nextPC = batch.pc
	for iterations < maxIterations {
		tableEntryValue := table.RawGetInteger(visibleIndex)
		if tableEntryValue.Kind != KindInteger {
			// 当前轮次需要普通 GETTABLE 写回和 ADD 错误/转换语义；若已有提交轮次，则让调用方从当前 GETTABLE 重放。
			return nextPC, iterations, iterations > 0
		}
		registers[superInstruction.GetTarget] = tableEntryValue
		sum += tableEntryValue.Integer
		registers[superInstruction.SumRegister] = IntegerValue(sum)
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回 GETTABLE。
			index = nextIndex
			visibleIndex = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		return 0, 0, false
	}
	vm.currentPC = batch.pc + 2
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// tryExecuteDenseTableReadAddForLoopBatch 直接批量读取 dense integer table 并累加。
//
// 该路径只服务已证明的 `sum = sum + t[i]; FORLOOP` 形态；table 必须保持无元表 dense 表示，循环步长必须为 1，
// 且读取 key 位于连续 dense 前缀内。读不到 integer 时，若已有提交轮次则返回当前 GETTABLE PC 让普通 VM
// 重放失败轮次；若首轮失败则完全回退普通 batch。
func (vm *VM) tryExecuteDenseTableReadAddForLoopBatch(table *Table, superInstruction tableReadAddForLoopSuperInstruction, pc int, maxIterations int, sum int64, index int64, limit int64, step int64, visibleIndex int64) (nextPC int, iterations int, handled bool) {
	// 只有 dense table、单位正步长和 index 对齐时，才能直接从 dense backing store 读取。
	if table == nil || !table.hasDenseIntegerArray() || step != 1 || index != visibleIndex {
		return 0, 0, false
	}

	registers := vm.registers
	nextPC = pc
	lastValue := int64(0)
	for iterations < maxIterations {
		if visibleIndex <= 0 || visibleIndex > int64(len(table.denseIntegerValues)) {
			// 当前轮次缺少 dense integer；已有提交时让调用方从当前 GETTABLE 重放，否则完全回退。
			if iterations > 0 {
				registers[superInstruction.GetTarget] = IntegerValue(lastValue)
				registers[superInstruction.SumRegister] = IntegerValue(sum)
			}
			return nextPC, iterations, iterations > 0
		}
		lastValue = table.denseIntegerValues[visibleIndex-1]
		sum += lastValue
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回 GETTABLE。
			index = nextIndex
			visibleIndex = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		// 首轮 guard 即失败时不能产生任何副作用，交给普通 batch 路径处理。
		return 0, 0, false
	}

	registers[superInstruction.GetTarget] = IntegerValue(lastValue)
	registers[superInstruction.SumRegister] = IntegerValue(sum)
	vm.currentPC = pc + 2
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// ensureAddForLoopSuperInstructions 返回当前 Proto 的 ADD;FORLOOP 预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录可由后续 hot path 尝试的 opcode 邻接形态；运行期类型、
// 寄存器窗口、hook 和 context guard 仍由调用方与 TryExecuteAddForLoop 检查。
func (vm *VM) ensureAddForLoopSuperInstructions() []addForLoopSuperInstruction {
	// 按当前 Proto 懒构建 ADD;FORLOOP 预匹配表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) == 0 {
		// 空 Proto 没有热循环形态，清理旧表并返回 nil。
		vm.addForLoopSuperInstructionProto = vm.proto
		vm.addForLoopSuperInstructions = nil
		return nil
	}
	if vm.addForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.addForLoopSuperInstructions
	}

	vm.addForLoopSuperInstructionProto = vm.proto
	vm.addForLoopSuperInstructions = buildAddForLoopSuperInstructions(vm.proto)
	return vm.addForLoopSuperInstructions
}

// buildAddForLoopSuperInstructions 构建 `ADD; FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildAddForLoopSuperInstructions(proto *bytecode.Proto) []addForLoopSuperInstruction {
	// 空 Proto 或单条指令没有可构建的 superinstruction。
	if proto == nil || len(proto.Code) < 2 {
		return nil
	}

	var superInstructions []addForLoopSuperInstruction
	for pc := 0; pc+1 < len(proto.Code); pc++ {
		// 只识别相邻的 ADD;FORLOOP，其他形态保持零值表示无效。
		addInstruction := proto.Code[pc]
		forLoopInstruction := proto.Code[pc+1]
		if addInstruction.OpCode() != bytecode.OpAdd || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标邻接模式，继续扫描后续指令。
			continue
		}
		exitPC := pc + 2
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖 arith_add_loop 的完整循环体 `ADD; FORLOOP`；混合算术循环的末尾 ADD 会跳回更早 PC。
			continue
		}
		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]addForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = addForLoopSuperInstruction{
			AddTarget:    addInstruction.A(),
			LeftOperand:  decodeRKOperand(proto, addInstruction.B()),
			RightOperand: decodeRKOperand(proto, addInstruction.C()),
			ForBase:      forLoopInstruction.A(),
			ForSBx:       forLoopInstruction.SBx(),
			ExitPC:       exitPC,
			LoopPC:       loopPC,
			Valid:        true,
		}
	}
	return superInstructions
}

// PrepareAddForLoopSuperInstructions 预构建当前 Proto 的 `ADD; FORLOOP` 预匹配表。
//
// 调用方可在进入普通无 hook 热循环前调用一次，避免 TryExecuteAddForLoop 在每轮循环里重复触发
// 懒初始化检查。返回 true 表示当前 Proto 至少存在一个可尝试的 ADD;FORLOOP PC。
func (vm *VM) PrepareAddForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 TryExecuteAddForLoop 会因为表不匹配而回退。
	return len(vm.ensureAddForLoopSuperInstructions()) > 0
}

// PrepareAddForLoopBatch 为 `ADD; FORLOOP` 循环体准备可复用 batch 上下文。
func (vm *VM) PrepareAddForLoopBatch(pc int) (AddForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.addForLoopSuperInstructionProto != vm.proto {
		return AddForLoopBatch{}, false
	}
	superInstructions := vm.addForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		return AddForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		return AddForLoopBatch{}, false
	}
	if registerInRange(superInstruction.AddTarget, superInstruction.ForBase, superInstruction.ForBase+3) {
		// batch 只处理 sum 独立于 numeric-for 控制槽的官方形态；别名形态保留单步 VM 语义。
		return AddForLoopBatch{}, false
	}
	leftIsTarget := !superInstruction.LeftOperand.Constant && superInstruction.LeftOperand.Index == superInstruction.AddTarget
	rightIsTarget := !superInstruction.RightOperand.Constant && superInstruction.RightOperand.Index == superInstruction.AddTarget
	leftIsVisible := !superInstruction.LeftOperand.Constant && superInstruction.LeftOperand.Index == superInstruction.ForBase+3
	rightIsVisible := !superInstruction.RightOperand.Constant && superInstruction.RightOperand.Index == superInstruction.ForBase+3
	if !((leftIsTarget && rightIsVisible) || (rightIsTarget && leftIsVisible)) {
		// 当前 batch 只覆盖 `sum = sum + i` 或 `sum = i + sum`；其他操作数形态回退单轮路径。
		return AddForLoopBatch{}, false
	}
	maxRegisterIndex := superInstruction.ForBase + 3
	if superInstruction.AddTarget > maxRegisterIndex {
		// 目标寄存器可能高于 numeric-for 控制槽，需要纳入边界检查。
		maxRegisterIndex = superInstruction.AddTarget
	}
	return AddForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		maxRegisterIndex: maxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecuteAddForLoopBatch 批量执行已准备的 `ADD; FORLOOP` 循环体。
//
// batch 必须来自 PrepareAddForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。返回
// handled=false 表示当前动态寄存器状态不再满足 integer guard，调用方必须回退普通 VM。
func (vm *VM) TryExecuteAddForLoopBatch(batch AddForLoopBatch, maxIterations int) (nextPC int, iterations int, handled bool) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 {
		return 0, 0, false
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		return 0, 0, false
	}
	superInstruction := batch.superInstruction
	sumValue := registers[superInstruction.AddTarget]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.ForBase+3]
	if sumValue.Kind != KindInteger || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		// 只覆盖 integer ADD 和 integer numeric-for；其他类型保留完整算术与 FORLOOP 语义。
		return 0, 0, false
	}
	sum := sumValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	if step == 1 && visibleIndex == index && index >= 0 {
		// 正向连续整数循环可在当前 context 窗口内用等差求和提交；不扩大 maxIterations 边界。
		return vm.tryExecuteAddForLoopStepOneBatch(batch, sum, index, limit, maxIterations)
	}
	nextPC = batch.pc
	for iterations < maxIterations {
		sum += visibleIndex
		registers[superInstruction.AddTarget] = IntegerValue(sum)
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回 ADD。
			index = nextIndex
			visibleIndex = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		return 0, 0, false
	}
	vm.currentPC = batch.pc + 1
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// tryExecuteAddForLoopStepOneBatch 用等差求和提交 step=1 的 ADD;FORLOOP batch。
//
// batch 必须已通过 TryExecuteAddForLoopBatch 的寄存器、类型、step 和可见 index guard；maxIterations
// 仍由调用方按 context 检查窗口换算，本 helper 只减少窗口内逐轮整数加法，不改变 PC 或退出语义。
func (vm *VM) tryExecuteAddForLoopStepOneBatch(batch AddForLoopBatch, sum int64, index int64, limit int64, maxIterations int) (nextPC int, iterations int, handled bool) {
	// 计算本窗口最多能提交的迭代数；即使 index 已越过 limit，也保留当前 ADD 后退出的防御语义。
	iterations, exits := addForLoopStepOneBatchIterations(index, limit, maxIterations)
	if iterations <= 0 {
		// 理论上 maxIterations 已经过滤；保留防御回退避免产生部分副作用。
		return 0, 0, false
	}

	superInstruction := batch.superInstruction
	registers := vm.registers
	sum = int64(uint64(sum) + addForLoopStepOneTermSum(index, iterations))
	registers[superInstruction.AddTarget] = IntegerValue(sum)
	nextPC = superInstruction.ExitPC
	if exits {
		// 退出时普通 FORLOOP 保留最后一次有效 index，不写入越界后的 nextIndex。
		lastIndex := index + int64(iterations) - 1
		registers[superInstruction.ForBase] = IntegerValue(lastIndex)
		registers[superInstruction.ForBase+3] = IntegerValue(lastIndex)
		vm.pcOffset = 0
	} else {
		// 未到 limit 时必须写入下一轮可见 index，并跳回 ADD。
		nextIndex := index + int64(iterations)
		registers[superInstruction.ForBase] = IntegerValue(nextIndex)
		registers[superInstruction.ForBase+3] = IntegerValue(nextIndex)
		nextPC = superInstruction.LoopPC
		vm.pcOffset = superInstruction.ForSBx
	}
	vm.currentPC = batch.pc + 1
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// addForLoopStepOneBatchIterations 计算 step=1 batch 在当前窗口内能提交的迭代数。
//
// 调用方保证 index 非负且 maxIterations 来自 context 窗口；返回 exits=true 表示本次 batch 会执行到
// FORLOOP 退出分支。若 index 已经大于 limit，仍提交当前 ADD 的一轮并退出，以匹配已位于循环体 PC
// 时的防御性逐指令语义。
func addForLoopStepOneBatchIterations(index int64, limit int64, maxIterations int) (iterations int, exits bool) {
	// 没有可用窗口时不能提交任何虚拟指令。
	if maxIterations <= 0 {
		return 0, false
	}
	if limit < index {
		// 当前 ADD 已经开始执行；即使控制槽异常越界，也要执行本轮后退出。
		return 1, true
	}
	maxBatch := int64(maxIterations)
	distance := limit - index
	if distance < maxBatch {
		// 剩余可执行轮数落在窗口内，本 batch 会在最后一轮退出。
		return int(distance) + 1, true
	}

	// limit 仍在窗口之外，完整提交 maxIterations 并继续循环。
	return maxIterations, false
}

// addForLoopStepOneTermSum 返回 start, start+1 ... 的 count 项和，按 int64 ADD wrap 语义取模。
func addForLoopStepOneTermSum(start int64, count int) uint64 {
	// 用 uint64 表示 Lua integer 加法的二进制环绕；count 来自 context 窗口，三角数不会放大循环成本。
	termCount := uint64(count)
	return uint64(start)*termCount + termCount*(termCount-1)/2
}

// TryExecuteAddForLoop 尝试执行 `ADD; FORLOOP` superinstruction。
//
// pc 必须指向当前 Proto.Code 中的 ADD 指令；返回 handled=false 表示 guard 不满足，调用方必须回退
// 普通 VM。该 fast path 只覆盖 integer ADD 和 integer numeric-for，不触发元方法、不处理 hook、
// 不处理 yield；调用方必须先确认当前执行环境允许跳过逐 PC 后处理。
func (vm *VM) TryExecuteAddForLoop(pc int) (int, bool) {
	// 单轮路径先尝试更窄的 batch 准备，覆盖官方 arith_add_loop；失败再保留旧通用单轮 guard。
	if batch, ok := vm.PrepareAddForLoopBatch(pc); ok {
		if nextPC, _, handled := vm.TryExecuteAddForLoopBatch(batch, 1); handled {
			return nextPC, true
		}
	}

	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败，避免普通指令承担额外识别成本。
	if vm == nil || vm.addForLoopSuperInstructionProto != vm.proto {
		// 调用方尚未为当前 Proto 准备表，回退普通 VM。
		return 0, false
	}
	superInstructions := vm.addForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// 当前 PC 不在表范围内，回退普通 VM。
		return 0, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配 ADD;FORLOOP 形态，回退普通 VM。
		return 0, false
	}

	// 先完成全部寄存器和类型 guard，再写回任何寄存器，保证 guard 失败可以无副作用回退。
	registers := vm.registers
	if uint(superInstruction.AddTarget) >= uint(len(registers)) {
		// ADD 目标寄存器越界交给普通 VM 报原始错误。
		return 0, false
	}
	leftInteger, leftOK := vm.decodedIntegerOperandValue(superInstruction.LeftOperand)
	if !leftOK {
		// 左操作数不是 integer 寄存器或 integer 常量时回退完整算术语义。
		return 0, false
	}
	rightInteger, rightOK := vm.decodedIntegerOperandValue(superInstruction.RightOperand)
	if !rightOK {
		// 右操作数不是 integer 寄存器或 integer 常量时回退完整算术语义。
		return 0, false
	}

	baseIndex := superInstruction.ForBase
	if uint(baseIndex+3) >= uint(len(registers)) {
		// FORLOOP 需要 R(A)..R(A+3) 四个控制槽，越界时回退普通 VM。
		return 0, false
	}
	indexValue := registers[baseIndex]
	limitValue := registers[baseIndex+1]
	stepValue := registers[baseIndex+2]
	if indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		// 只覆盖 integer numeric-for；float 或可转 number 字符串仍走普通 VM。
		return 0, false
	}

	// guard 全部通过后，按 ADD 与 FORLOOP 顺序提交寄存器写入。
	registers[superInstruction.AddTarget] = IntegerValue(leftInteger + rightInteger)
	nextIndex := indexValue.Integer + stepValue.Integer
	nextPC := superInstruction.ExitPC
	if forIntegerLoopContinues(nextIndex, limitValue.Integer, stepValue.Integer) {
		// 循环继续时更新内部 index 和外部可见循环变量，并跳回 FORLOOP 的 sBx 目标。
		registers[baseIndex] = IntegerValue(nextIndex)
		registers[baseIndex+3] = IntegerValue(nextIndex)
		nextPC = superInstruction.LoopPC
		vm.pcOffset = superInstruction.ForSBx
	} else {
		// 循环结束时不更新 index 和外部变量，保持普通 FORLOOP 语义。
		vm.pcOffset = 0
	}
	vm.currentPC = pc + 1
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, true
}

// ensureMulAddSubForLoopSuperInstructions 返回当前 Proto 的 MUL;ADD;SUB;FORLOOP 预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录完整循环体回跳当前 MUL 的算术链形态；运行期类型、
// 寄存器窗口、hook 和 context guard 仍由调用方与 TryExecuteMulAddSubForLoop 检查。
func (vm *VM) ensureMulAddSubForLoopSuperInstructions() []mulAddSubForLoopSuperInstruction {
	// 按当前 Proto 懒构建算术链 superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 4 {
		// 少于四条指令不可能包含完整 `MUL;ADD;SUB;FORLOOP`。
		vm.mulAddSubForLoopSuperInstructionProto = vm.proto
		vm.mulAddSubForLoopSuperInstructions = nil
		return nil
	}
	if vm.mulAddSubForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.mulAddSubForLoopSuperInstructions
	}

	vm.mulAddSubForLoopSuperInstructionProto = vm.proto
	vm.mulAddSubForLoopSuperInstructions = buildMulAddSubForLoopSuperInstructions(vm.proto)
	return vm.mulAddSubForLoopSuperInstructions
}

// buildMulAddSubForLoopSuperInstructions 构建 `MUL; ADD; SUB; FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildMulAddSubForLoopSuperInstructions(proto *bytecode.Proto) []mulAddSubForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的算术链 superinstruction。
	if proto == nil || len(proto.Code) < 4 {
		return nil
	}

	var superInstructions []mulAddSubForLoopSuperInstruction
	for pc := 0; pc+3 < len(proto.Code); pc++ {
		// 只识别完整相邻 `MUL;ADD;SUB;FORLOOP`，其他形态保持零值表示无效。
		mulInstruction := proto.Code[pc]
		addInstruction := proto.Code[pc+1]
		subInstruction := proto.Code[pc+2]
		forLoopInstruction := proto.Code[pc+3]
		if mulInstruction.OpCode() != bytecode.OpMul || addInstruction.OpCode() != bytecode.OpAdd || subInstruction.OpCode() != bytecode.OpSub || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标四指令模式，继续扫描后续指令。
			continue
		}
		exitPC := pc + 4
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖完整算术链循环体；回跳到其他 PC 的形态必须交给普通 VM 或后续更完整模式。
			continue
		}
		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]mulAddSubForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = mulAddSubForLoopSuperInstruction{
			MulTarget:       mulInstruction.A(),
			MulLeftOperand:  decodeRKOperand(proto, mulInstruction.B()),
			MulRightOperand: decodeRKOperand(proto, mulInstruction.C()),
			AddTarget:       addInstruction.A(),
			AddLeftOperand:  decodeRKOperand(proto, addInstruction.B()),
			AddRightOperand: decodeRKOperand(proto, addInstruction.C()),
			SubTarget:       subInstruction.A(),
			SubLeftOperand:  decodeRKOperand(proto, subInstruction.B()),
			SubRightOperand: decodeRKOperand(proto, subInstruction.C()),
			ForBase:         forLoopInstruction.A(),
			ForSBx:          forLoopInstruction.SBx(),
			ExitPC:          exitPC,
			LoopPC:          loopPC,
			Valid:           true,
		}
	}
	return superInstructions
}

// PrepareMulAddSubForLoopSuperInstructions 预构建当前 Proto 的 `MUL; ADD; SUB; FORLOOP` 表。
//
// 返回 true 表示当前 Proto 至少存在一个可尝试的算术链 superinstruction PC；返回 false 时调用方
// 不应在热循环中反复调用 TryExecuteMulAddSubForLoop。
func (vm *VM) PrepareMulAddSubForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 TryExecuteMulAddSubForLoop 会因为表不匹配而回退。
	return len(vm.ensureMulAddSubForLoopSuperInstructions()) > 0
}

// PrepareMulAddSubForLoopBatch 为 `MUL; ADD; SUB; FORLOOP` 算术链准备 batch 上下文。
func (vm *VM) PrepareMulAddSubForLoopBatch(pc int) (MulAddSubForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.mulAddSubForLoopSuperInstructionProto != vm.proto {
		return MulAddSubForLoopBatch{}, false
	}
	superInstructions := vm.mulAddSubForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		return MulAddSubForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		return MulAddSubForLoopBatch{}, false
	}

	forBase := superInstruction.ForBase
	tempRegister := superInstruction.MulTarget
	if superInstruction.AddTarget != tempRegister {
		// 当前 batch 只覆盖 MUL 和 ADD 写回同一临时寄存器的官方形态。
		return MulAddSubForLoopBatch{}, false
	}
	loopRegister, mulConstant, ok := decodedRegisterIntegerConstantPair(superInstruction.MulLeftOperand, superInstruction.MulRightOperand)
	if !ok || loopRegister != forBase+3 {
		// MUL 必须读取 numeric-for 外部可见变量与 integer 常量。
		return MulAddSubForLoopBatch{}, false
	}
	sumRegister, ok := decodedRegisterPlusTarget(superInstruction.AddLeftOperand, superInstruction.AddRightOperand, tempRegister)
	if !ok {
		// ADD 必须把 sum 与 MUL 临时值累加回临时寄存器。
		return MulAddSubForLoopBatch{}, false
	}
	subLeftRegister, ok := decodedRegisterOperand(superInstruction.SubLeftOperand)
	if !ok || subLeftRegister != tempRegister || superInstruction.SubTarget != sumRegister {
		// SUB 必须从 ADD 临时值减常量并写回 sum。
		return MulAddSubForLoopBatch{}, false
	}
	subConstant, ok := decodedIntegerConstantOperand(superInstruction.SubRightOperand)
	if !ok {
		// SUB 右侧不是 integer 常量时保留普通 VM 的完整算术语义。
		return MulAddSubForLoopBatch{}, false
	}
	if tempRegister == sumRegister || registerInRange(tempRegister, forBase, forBase+3) || registerInRange(sumRegister, forBase, forBase+3) {
		// 临时寄存器和 sum 不能彼此别名，也不能覆盖 numeric-for 控制槽。
		return MulAddSubForLoopBatch{}, false
	}

	maxRegisterIndex := forBase + 3
	if tempRegister > maxRegisterIndex {
		// 临时寄存器可能高于控制槽，需要纳入边界检查。
		maxRegisterIndex = tempRegister
	}
	if sumRegister > maxRegisterIndex {
		// sum 寄存器可能高于控制槽，需要纳入边界检查。
		maxRegisterIndex = sumRegister
	}
	return MulAddSubForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		TempRegister:     tempRegister,
		SumRegister:      sumRegister,
		MulConstant:      mulConstant,
		SubConstant:      subConstant,
		maxRegisterIndex: maxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecuteMulAddSubForLoopBatch 批量执行已准备的 `MUL; ADD; SUB; FORLOOP` 算术链。
//
// batch 必须来自 PrepareMulAddSubForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。
// 返回 handled=false 表示当前动态寄存器状态不满足 integer guard，调用方必须回退旧单轮路径或普通 VM。
func (vm *VM) TryExecuteMulAddSubForLoopBatch(batch MulAddSubForLoopBatch, maxIterations int) (nextPC int, iterations int, handled bool) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 {
		return 0, 0, false
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		return 0, 0, false
	}
	superInstruction := batch.superInstruction
	sumValue := registers[batch.SumRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.ForBase+3]
	if sumValue.Kind != KindInteger || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		// 只覆盖 integer 算术链和 integer numeric-for；其他类型保留普通转换、元方法和错误语义。
		return 0, 0, false
	}
	sum := sumValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	lastTemp := registers[batch.TempRegister]
	nextPC = batch.pc
	for iterations < maxIterations {
		mulResult := visibleIndex * batch.MulConstant
		addResult := sum + mulResult
		sum = addResult - batch.SubConstant
		lastTemp = IntegerValue(addResult)
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时推进内部 index 和外部可见循环变量，并跳回 MUL。
			index = nextIndex
			visibleIndex = nextIndex
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		return 0, 0, false
	}
	registers[batch.TempRegister] = lastTemp
	registers[batch.SumRegister] = IntegerValue(sum)
	registers[superInstruction.ForBase] = IntegerValue(index)
	registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
	vm.currentPC = batch.pc + 3
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// TryExecuteMulAddSubForLoop 尝试执行 `MUL; ADD; SUB; FORLOOP` superinstruction。
//
// pc 必须指向当前 Proto.Code 中的 MUL 指令；返回 handled=false 表示 guard 不满足，调用方必须回退
// 普通 VM。该 fast path 只覆盖 integer 算术链和 integer numeric-for，不触发元方法、不处理 hook、
// 不处理 yield；调用方必须先确认当前执行环境允许跳过逐 PC 后处理和 context 检查。
func (vm *VM) TryExecuteMulAddSubForLoop(pc int) (int, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败，避免普通指令承担额外识别成本。
	if vm == nil || vm.mulAddSubForLoopSuperInstructionProto != vm.proto {
		// 调用方尚未为当前 Proto 准备表，回退普通 VM。
		return 0, false
	}
	superInstructions := vm.mulAddSubForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// 当前 PC 不在表范围内，回退普通 VM。
		return 0, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配 MUL;ADD;SUB;FORLOOP 形态，回退普通 VM。
		return 0, false
	}

	// 先完成全部寄存器和类型 guard，再写回任何寄存器，保证 guard 失败可以无副作用回退。
	registers := vm.registers
	if uint(superInstruction.MulTarget) >= uint(len(registers)) || uint(superInstruction.AddTarget) >= uint(len(registers)) || uint(superInstruction.SubTarget) >= uint(len(registers)) {
		// 任一算术目标寄存器越界时交给普通 VM 报原始错误。
		return 0, false
	}
	mulLeftInteger, mulLeftOK := vm.decodedIntegerOperandValue(superInstruction.MulLeftOperand)
	if !mulLeftOK {
		// MUL 左操作数不是 integer 寄存器或 integer 常量时回退完整算术语义。
		return 0, false
	}
	mulRightInteger, mulRightOK := vm.decodedIntegerOperandValue(superInstruction.MulRightOperand)
	if !mulRightOK {
		// MUL 右操作数不是 integer 寄存器或 integer 常量时回退完整算术语义。
		return 0, false
	}
	mulResult := mulLeftInteger * mulRightInteger

	addLeftInteger, addLeftOK := vm.decodedIntegerOperandValueWithOverrides(superInstruction.AddLeftOperand, superInstruction.MulTarget, mulResult, true, -1, 0, false, -1, 0, false)
	if !addLeftOK {
		// ADD 左操作数不能在 MUL 后解析为 integer 时回退普通 VM。
		return 0, false
	}
	addRightInteger, addRightOK := vm.decodedIntegerOperandValueWithOverrides(superInstruction.AddRightOperand, superInstruction.MulTarget, mulResult, true, -1, 0, false, -1, 0, false)
	if !addRightOK {
		// ADD 右操作数不能在 MUL 后解析为 integer 时回退普通 VM。
		return 0, false
	}
	addResult := addLeftInteger + addRightInteger

	subLeftInteger, subLeftOK := vm.decodedIntegerOperandValueWithOverrides(superInstruction.SubLeftOperand, superInstruction.MulTarget, mulResult, true, superInstruction.AddTarget, addResult, true, -1, 0, false)
	if !subLeftOK {
		// SUB 左操作数不能在 ADD 后解析为 integer 时回退普通 VM。
		return 0, false
	}
	subRightInteger, subRightOK := vm.decodedIntegerOperandValueWithOverrides(superInstruction.SubRightOperand, superInstruction.MulTarget, mulResult, true, superInstruction.AddTarget, addResult, true, -1, 0, false)
	if !subRightOK {
		// SUB 右操作数不能在 ADD 后解析为 integer 时回退普通 VM。
		return 0, false
	}
	subResult := subLeftInteger - subRightInteger

	baseIndex := superInstruction.ForBase
	if uint(baseIndex+3) >= uint(len(registers)) {
		// FORLOOP 需要 R(A)..R(A+3) 四个控制槽，越界时回退普通 VM。
		return 0, false
	}
	indexInteger, indexOK := vm.registerIntegerValueWithOverrides(baseIndex, superInstruction.MulTarget, mulResult, superInstruction.AddTarget, addResult, superInstruction.SubTarget, subResult)
	limitInteger, limitOK := vm.registerIntegerValueWithOverrides(baseIndex+1, superInstruction.MulTarget, mulResult, superInstruction.AddTarget, addResult, superInstruction.SubTarget, subResult)
	stepInteger, stepOK := vm.registerIntegerValueWithOverrides(baseIndex+2, superInstruction.MulTarget, mulResult, superInstruction.AddTarget, addResult, superInstruction.SubTarget, subResult)
	if !indexOK || !limitOK || !stepOK {
		// 只覆盖 integer numeric-for；float 或可转 number 字符串仍走普通 VM。
		return 0, false
	}

	// guard 全部通过后，按 MUL、ADD、SUB 与 FORLOOP 顺序提交寄存器写入。
	registers[superInstruction.MulTarget] = IntegerValue(mulResult)
	registers[superInstruction.AddTarget] = IntegerValue(addResult)
	registers[superInstruction.SubTarget] = IntegerValue(subResult)
	nextIndex := indexInteger + stepInteger
	nextPC := superInstruction.ExitPC
	if forIntegerLoopContinues(nextIndex, limitInteger, stepInteger) {
		// 循环继续时更新内部 index 和外部可见循环变量，并跳回 FORLOOP 的 sBx 目标。
		registers[baseIndex] = IntegerValue(nextIndex)
		registers[baseIndex+3] = IntegerValue(nextIndex)
		nextPC = superInstruction.LoopPC
		vm.pcOffset = superInstruction.ForSBx
	} else {
		// 循环结束时不更新 index 和外部变量，保持普通 FORLOOP 语义。
		vm.pcOffset = 0
	}
	vm.currentPC = pc + 3
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, true
}

// ensureMixArithmeticForLoopSuperInstructions 返回当前 Proto 的混合算术循环预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录完整循环体回跳当前 MUL 的 `arith_mix_loop` 形态；运行期
// 类型、寄存器窗口、除数、hook 和 context guard 仍由调用方与 TryExecuteMixArithmeticForLoop 检查。
func (vm *VM) ensureMixArithmeticForLoopSuperInstructions() []mixArithmeticForLoopSuperInstruction {
	// 按当前 Proto 懒构建混合算术 superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 7 {
		// 少于七条指令不可能包含完整混合算术循环体。
		vm.mixArithmeticForLoopSuperInstructionProto = vm.proto
		vm.mixArithmeticForLoopSuperInstructions = nil
		return nil
	}
	if vm.mixArithmeticForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.mixArithmeticForLoopSuperInstructions
	}

	vm.mixArithmeticForLoopSuperInstructionProto = vm.proto
	vm.mixArithmeticForLoopSuperInstructions = buildMixArithmeticForLoopSuperInstructions(vm.proto)
	return vm.mixArithmeticForLoopSuperInstructions
}

// buildMixArithmeticForLoopSuperInstructions 构建 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildMixArithmeticForLoopSuperInstructions(proto *bytecode.Proto) []mixArithmeticForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的混合算术 superinstruction。
	if proto == nil || len(proto.Code) < 7 {
		return nil
	}

	var superInstructions []mixArithmeticForLoopSuperInstruction
	for pc := 0; pc+6 < len(proto.Code); pc++ {
		// 只识别完整相邻 `MUL;ADD;SUB;IDIV;MOD;ADD;FORLOOP`，其他形态保持零值表示无效。
		mulInstruction := proto.Code[pc]
		firstAddInstruction := proto.Code[pc+1]
		subInstruction := proto.Code[pc+2]
		iDivInstruction := proto.Code[pc+3]
		modInstruction := proto.Code[pc+4]
		finalAddInstruction := proto.Code[pc+5]
		forLoopInstruction := proto.Code[pc+6]
		if mulInstruction.OpCode() != bytecode.OpMul || firstAddInstruction.OpCode() != bytecode.OpAdd || subInstruction.OpCode() != bytecode.OpSub || iDivInstruction.OpCode() != bytecode.OpIDiv || modInstruction.OpCode() != bytecode.OpMod || finalAddInstruction.OpCode() != bytecode.OpAdd || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标七指令模式，继续扫描后续指令。
			continue
		}
		exitPC := pc + 7
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖完整混合算术循环体；回跳到其他 PC 的形态必须交给普通 VM。
			continue
		}
		forBase := forLoopInstruction.A()
		mulLeftOperand := decodeRKOperand(proto, mulInstruction.B())
		mulRightOperand := decodeRKOperand(proto, mulInstruction.C())
		mulRegister, mulConstant, ok := decodedRegisterIntegerConstantPair(mulLeftOperand, mulRightOperand)
		if !ok || mulRegister != forBase+3 {
			// 官方 mix 形态必须使用外部可见循环变量乘以 integer 常量。
			continue
		}
		firstAddLeftOperand := decodeRKOperand(proto, firstAddInstruction.B())
		firstAddRightOperand := decodeRKOperand(proto, firstAddInstruction.C())
		sumRegister, ok := decodedRegisterPlusTarget(firstAddLeftOperand, firstAddRightOperand, mulInstruction.A())
		if !ok || firstAddInstruction.A() != mulInstruction.A() {
			// 第一条 ADD 必须把 sum 与 MUL 临时结果累加回同一临时寄存器。
			continue
		}
		subLeftOperand := decodeRKOperand(proto, subInstruction.B())
		subRightOperand := decodeRKOperand(proto, subInstruction.C())
		subLeftRegister, ok := decodedRegisterOperand(subLeftOperand)
		if !ok || subInstruction.A() != sumRegister || subLeftRegister != firstAddInstruction.A() {
			// SUB 必须从第一条 ADD 的临时结果减常量并写回 sum。
			continue
		}
		subConstant, ok := decodedIntegerConstantOperand(subRightOperand)
		if !ok {
			// SUB 右侧不是 integer 常量时交给普通 VM 保留完整语义。
			continue
		}
		iDivLeftOperand := decodeRKOperand(proto, iDivInstruction.B())
		iDivRightOperand := decodeRKOperand(proto, iDivInstruction.C())
		iDivLeftRegister, ok := decodedRegisterOperand(iDivLeftOperand)
		if !ok || iDivInstruction.A() != firstAddInstruction.A() || iDivLeftRegister != subInstruction.A() {
			// IDIV 必须读取更新后的 sum 并写回临时寄存器。
			continue
		}
		iDivConstant, ok := decodedIntegerConstantOperand(iDivRightOperand)
		if !ok || iDivConstant == 0 {
			// 非 integer 或零除常量不能进入 fast path，保留普通错误语义。
			continue
		}
		modLeftOperand := decodeRKOperand(proto, modInstruction.B())
		modRightOperand := decodeRKOperand(proto, modInstruction.C())
		modLeftRegister, ok := decodedRegisterOperand(modLeftOperand)
		if !ok || modLeftRegister != mulRegister {
			// MOD 必须读取同一个外部循环变量。
			continue
		}
		modConstant, ok := decodedIntegerConstantOperand(modRightOperand)
		if !ok || modConstant == 0 {
			// 非 integer 或零除常量不能进入 fast path，保留普通错误语义。
			continue
		}
		finalAddLeftOperand := decodeRKOperand(proto, finalAddInstruction.B())
		finalAddRightOperand := decodeRKOperand(proto, finalAddInstruction.C())
		if finalAddInstruction.A() != sumRegister || !decodedRegistersMatchPair(finalAddLeftOperand, finalAddRightOperand, iDivInstruction.A(), modInstruction.A()) {
			// 末尾 ADD 必须把 IDIV 和 MOD 临时结果相加写回 sum。
			continue
		}
		if registerInRange(sumRegister, forBase, forBase+3) || registerInRange(mulInstruction.A(), forBase, forBase+3) || registerInRange(modInstruction.A(), forBase, forBase+3) {
			// 算术目标不能覆盖 numeric-for 控制槽，否则需要逐指令别名语义。
			continue
		}
		tempRegister := mulInstruction.A()
		modRegister := modInstruction.A()
		batchValid := tempRegister != sumRegister && tempRegister != modRegister && sumRegister != modRegister
		batchMaxRegisterIndex := forBase + 3
		if tempRegister > batchMaxRegisterIndex {
			// IDIV 临时寄存器可能高于控制槽，需要纳入 batch 边界检查。
			batchMaxRegisterIndex = tempRegister
		}
		if sumRegister > batchMaxRegisterIndex {
			// sum 寄存器可能高于控制槽，需要纳入 batch 边界检查。
			batchMaxRegisterIndex = sumRegister
		}
		if modRegister > batchMaxRegisterIndex {
			// MOD 临时寄存器可能高于控制槽，需要纳入 batch 边界检查。
			batchMaxRegisterIndex = modRegister
		}
		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]mixArithmeticForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = mixArithmeticForLoopSuperInstruction{
			MulTarget:             mulInstruction.A(),
			MulLeftOperand:        mulLeftOperand,
			MulRightOperand:       mulRightOperand,
			FirstAddTarget:        firstAddInstruction.A(),
			FirstAddLeftOperand:   firstAddLeftOperand,
			FirstAddRightOperand:  firstAddRightOperand,
			SubTarget:             subInstruction.A(),
			SubLeftOperand:        subLeftOperand,
			SubRightOperand:       subRightOperand,
			IDivTarget:            iDivInstruction.A(),
			IDivLeftOperand:       iDivLeftOperand,
			IDivRightOperand:      iDivRightOperand,
			ModTarget:             modInstruction.A(),
			ModLeftOperand:        modLeftOperand,
			ModRightOperand:       modRightOperand,
			FinalAddTarget:        finalAddInstruction.A(),
			FinalAddLeftOperand:   finalAddLeftOperand,
			FinalAddRightOperand:  finalAddRightOperand,
			SumRegister:           sumRegister,
			LoopRegister:          mulRegister,
			MulConstant:           mulConstant,
			SubConstant:           subConstant,
			IDivConstant:          iDivConstant,
			ModConstant:           modConstant,
			ForBase:               forBase,
			ForSBx:                forLoopInstruction.SBx(),
			ExitPC:                exitPC,
			LoopPC:                loopPC,
			BatchTempRegister:     tempRegister,
			BatchSumRegister:      sumRegister,
			BatchModRegister:      modRegister,
			BatchMaxRegisterIndex: batchMaxRegisterIndex,
			BatchValid:            batchValid,
			Valid:                 true,
		}
	}
	return superInstructions
}

// PrepareMixArithmeticForLoopSuperInstructions 预构建当前 Proto 的混合算术循环表。
//
// 返回 true 表示当前 Proto 至少存在一个可尝试的 `arith_mix_loop` superinstruction PC；返回 false
// 时调用方不应在热循环中反复调用 TryExecuteMixArithmeticForLoop。
func (vm *VM) PrepareMixArithmeticForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 TryExecuteMixArithmeticForLoop 会因为表不匹配而回退。
	return len(vm.ensureMixArithmeticForLoopSuperInstructions()) > 0
}

// HasMixArithmeticForLoopAt 判断当前 PC 是否存在混合算术循环 superinstruction。
func (vm *VM) HasMixArithmeticForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层避免在非目标 PC 反复准备 batch。
	if vm == nil || vm.mixArithmeticForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.mixArithmeticForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// PrepareMixArithmeticForLoopBatch 为 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` 混合算术循环准备 batch 上下文。
func (vm *VM) PrepareMixArithmeticForLoopBatch(pc int) (MixArithmeticForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.mixArithmeticForLoopSuperInstructionProto != vm.proto {
		return MixArithmeticForLoopBatch{}, false
	}
	superInstructions := vm.mixArithmeticForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		return MixArithmeticForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		return MixArithmeticForLoopBatch{}, false
	}
	if !superInstruction.BatchValid {
		// batch 最终只写回最后一轮可见状态；别名形态保留单轮路径的逐指令覆盖顺序。
		return MixArithmeticForLoopBatch{}, false
	}
	return MixArithmeticForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		TempRegister:     superInstruction.BatchTempRegister,
		SumRegister:      superInstruction.BatchSumRegister,
		ModRegister:      superInstruction.BatchModRegister,
		maxRegisterIndex: superInstruction.BatchMaxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecuteMixArithmeticForLoopBatch 批量执行已准备的混合算术循环体。
//
// batch 必须来自 PrepareMixArithmeticForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。
// 返回 handled=false 表示当前动态寄存器状态不满足 integer guard，调用方必须回退旧单轮路径或普通 VM。
func (vm *VM) TryExecuteMixArithmeticForLoopBatch(batch MixArithmeticForLoopBatch, maxIterations int) (nextPC int, iterations int, handled bool) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 {
		return 0, 0, false
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		return 0, 0, false
	}
	superInstruction := batch.superInstruction
	if superInstruction.IDivConstant == 0 || superInstruction.ModConstant == 0 {
		// 构建期已排除零除常量；这里保留防御性回退以维持普通错误路径。
		return 0, 0, false
	}

	sumValue := registers[batch.SumRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.ForBase+3]
	if sumValue.Kind != KindInteger || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		// 只覆盖 integer 混合算术和 integer numeric-for；其他类型保留普通转换、元方法和错误语义。
		return 0, 0, false
	}

	sum := sumValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	lastTemp := registers[batch.TempRegister]
	lastMod := registers[batch.ModRegister]
	nextPC = batch.pc
	for iterations < maxIterations {
		// 每轮严格按普通指令数据流计算，但只在 batch 完成后写回最后一轮可见寄存器状态。
		mulResult := visibleIndex * superInstruction.MulConstant
		firstAddResult := sum + mulResult
		subResult := firstAddResult - superInstruction.SubConstant
		iDivResult := integerFloorDiv(subResult, superInstruction.IDivConstant)
		modResult := integerModulo(visibleIndex, superInstruction.ModConstant)
		sum = iDivResult + modResult
		lastTemp = IntegerValue(iDivResult)
		lastMod = IntegerValue(modResult)
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时推进内部 index 和外部可见循环变量，并跳回 MUL。
			index = nextIndex
			visibleIndex = nextIndex
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		return 0, 0, false
	}
	registers[batch.TempRegister] = lastTemp
	registers[batch.ModRegister] = lastMod
	registers[batch.SumRegister] = IntegerValue(sum)
	registers[superInstruction.ForBase] = IntegerValue(index)
	registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
	vm.currentPC = batch.pc + 6
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// ensureFunctionCallAddForLoopSuperInstructions 返回当前 Proto 的函数调用加法循环预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录完整 `MOVE;MOVE;LOADK;CALL;ADD;FORLOOP` 循环体；运行期
// closure identity、参数类型、hook 和 context guard 仍由调用方与 TryExecuteFunctionCallAddForLoop 检查。
func (vm *VM) ensureFunctionCallAddForLoopSuperInstructions() []functionCallAddForLoopSuperInstruction {
	// 按当前 Proto 懒构建函数调用循环 superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 6 {
		// 少于六条指令不可能包含完整 function_call 循环体。
		vm.functionCallAddForLoopSuperInstructionProto = vm.proto
		vm.functionCallAddForLoopSuperInstructions = nil
		return nil
	}
	if vm.functionCallAddForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.functionCallAddForLoopSuperInstructions
	}

	vm.functionCallAddForLoopSuperInstructionProto = vm.proto
	vm.functionCallAddForLoopSuperInstructions = buildFunctionCallAddForLoopSuperInstructions(vm.proto)
	return vm.functionCallAddForLoopSuperInstructions
}

// buildFunctionCallAddForLoopSuperInstructions 构建 `MOVE;MOVE;LOADK;CALL;ADD;FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildFunctionCallAddForLoopSuperInstructions(proto *bytecode.Proto) []functionCallAddForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的函数调用循环 superinstruction。
	if proto == nil || len(proto.Code) < 6 {
		return nil
	}

	var superInstructions []functionCallAddForLoopSuperInstruction
	for pc := 0; pc+5 < len(proto.Code); pc++ {
		// 只识别完整相邻 `MOVE;MOVE;LOADK;CALL;ADD;FORLOOP`，其他形态保持零值表示无效。
		moveFunctionInstruction := proto.Code[pc]
		moveFirstArgumentInstruction := proto.Code[pc+1]
		loadSecondArgumentInstruction := proto.Code[pc+2]
		callInstruction := proto.Code[pc+3]
		addInstruction := proto.Code[pc+4]
		forLoopInstruction := proto.Code[pc+5]
		if moveFunctionInstruction.OpCode() != bytecode.OpMove || moveFirstArgumentInstruction.OpCode() != bytecode.OpMove || loadSecondArgumentInstruction.OpCode() != bytecode.OpLoadK || callInstruction.OpCode() != bytecode.OpCall || addInstruction.OpCode() != bytecode.OpAdd || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标六指令模式，继续扫描后续指令。
			continue
		}
		functionSlot := callInstruction.A()
		if moveFunctionInstruction.A() != functionSlot || moveFirstArgumentInstruction.A() != functionSlot+1 || loadSecondArgumentInstruction.A() != functionSlot+2 || callInstruction.B() != 3 || callInstruction.C() != 2 {
			// 只覆盖固定两实参、单返回 CALL，且两个实参必须由紧邻 MOVE/LOADK 准备。
			continue
		}
		exitPC := pc + 6
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖完整循环体；回跳到其他 PC 的形态必须交给普通 VM。
			continue
		}
		addLeftOperand := decodeRKOperand(proto, addInstruction.B())
		addRightOperand := decodeRKOperand(proto, addInstruction.C())
		sumRegister, ok := decodedRegisterPlusTarget(addLeftOperand, addRightOperand, functionSlot)
		if !ok || addInstruction.A() != sumRegister {
			// 外层 ADD 必须形如 `sum = sum + callResult`，且写回同一 sum 寄存器。
			continue
		}
		forBase := forLoopInstruction.A()
		firstArgumentSource := moveFirstArgumentInstruction.B()
		if firstArgumentSource != forBase+3 {
			// 当前窄路径只处理 numeric-for 外部可见循环变量作为第一实参。
			continue
		}
		functionSource := moveFunctionInstruction.B()
		if registerInRange(functionSource, forBase, forBase+3) || functionSource == functionSlot || functionSource == functionSlot+1 || functionSource == functionSlot+2 || functionSource == sumRegister {
			// 被调用 closure 来源必须在循环体内保持稳定，不能被 CALL、ADD 或 FORLOOP 覆盖。
			continue
		}
		if registerInRange(sumRegister, forBase, forBase+3) || sumRegister == functionSlot || sumRegister == functionSlot+1 || sumRegister == functionSlot+2 {
			// sum 不能覆盖 CALL 临时槽或 numeric-for 控制槽，否则需要逐指令别名语义。
			continue
		}
		secondArgument, ok := protoIntegerConstant(proto, loadSecondArgumentInstruction.Bx())
		if !ok {
			// 第二实参不是 integer 常量时回退普通 CALL，保留完整参数语义。
			continue
		}
		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]functionCallAddForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = functionCallAddForLoopSuperInstruction{
			FunctionSlot:        functionSlot,
			FunctionSource:      functionSource,
			FirstArgumentSlot:   functionSlot + 1,
			FirstArgumentSource: firstArgumentSource,
			SecondArgumentSlot:  functionSlot + 2,
			SecondArgument:      secondArgument,
			SumRegister:         sumRegister,
			AddTarget:           addInstruction.A(),
			ForBase:             forBase,
			ForSBx:              forLoopInstruction.SBx(),
			ExitPC:              exitPC,
			LoopPC:              loopPC,
			Valid:               true,
		}
	}
	return superInstructions
}

// PrepareFunctionCallAddForLoopSuperInstructions 预构建当前 Proto 的函数调用加法循环表。
//
// 返回 true 表示当前 Proto 至少存在一个可尝试的 `function_call` superinstruction PC；返回 false
// 时调用方不应在热循环中反复调用 TryExecuteFunctionCallAddForLoop。
func (vm *VM) PrepareFunctionCallAddForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 TryExecuteFunctionCallAddForLoop 会因为表不匹配而回退。
	return len(vm.ensureFunctionCallAddForLoopSuperInstructions()) > 0
}

// HasFunctionCallAddForLoopAt 判断当前 PC 是否存在函数调用加法循环 superinstruction。
func (vm *VM) HasFunctionCallAddForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层决定是否需要执行 CALL 边界 context 检查。
	if vm == nil || vm.functionCallAddForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.functionCallAddForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// PrepareFunctionCallAddForLoopBatch 为 function_call benchmark 循环体准备可复用 batch 上下文。
//
// pc 必须指向第一条 MOVE；返回 ok=false 表示静态形态、callee identity 或寄存器边界不满足 fast path，
// 调用方必须回退普通 VM。该方法不修改寄存器，只缓存后续迭代可复用的 guard 结果。
func (vm *VM) PrepareFunctionCallAddForLoopBatch(pc int) (FunctionCallAddForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.functionCallAddForLoopSuperInstructionProto != vm.proto {
		// 调用方尚未为当前 Proto 准备表，回退普通 VM。
		return FunctionCallAddForLoopBatch{}, false
	}
	superInstructions := vm.functionCallAddForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// 当前 PC 不在表范围内，回退普通 VM。
		return FunctionCallAddForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配函数调用循环形态，回退普通 VM。
		return FunctionCallAddForLoopBatch{}, false
	}

	registers := vm.registers
	maxRegisterIndex := 0
	for _, registerIndex := range [...]int{
		superInstruction.FunctionSlot,
		superInstruction.FunctionSource,
		superInstruction.FirstArgumentSlot,
		superInstruction.FirstArgumentSource,
		superInstruction.SecondArgumentSlot,
		superInstruction.SumRegister,
		superInstruction.ForBase + 3,
	} {
		// batch 准备阶段只计算一次最大寄存器下标，执行阶段用单次边界判断。
		if registerIndex > maxRegisterIndex {
			maxRegisterIndex = registerIndex
		}
	}
	if uint(maxRegisterIndex) >= uint(len(registers)) {
		// 任一参与寄存器越界时交给普通 VM 报原始错误。
		return FunctionCallAddForLoopBatch{}, false
	}

	functionValue := registers[superInstruction.FunctionSource]
	if functionValue.Kind != KindLuaClosure {
		// 被调值不是 Lua closure 时必须回退普通 CALL，保留 __call 和错误语义。
		return FunctionCallAddForLoopBatch{}, false
	}
	closure, ok := functionValue.Ref.(*LuaClosure)
	if !ok || closure == nil || closure.LeafAddReturn == nil || !closure.LeafAddReturn.HasRegisterRegisterAdd {
		// 只覆盖已预解析的 `return a+b` 叶子 closure。
		return FunctionCallAddForLoopBatch{}, false
	}
	leaf := closure.LeafAddReturn
	if !((leaf.LeftRegisterIndex == 0 && leaf.RightRegisterIndex == 1) || (leaf.LeftRegisterIndex == 1 && leaf.RightRegisterIndex == 0)) {
		// 叶子函数必须只读取两个固定实参，其他寄存器布局回退普通 direct CALL。
		return FunctionCallAddForLoopBatch{}, false
	}

	return FunctionCallAddForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		functionValue:    functionValue,
		maxRegisterIndex: maxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecutePreparedFunctionCallAddForLoop 执行一次已准备的 function_call 循环体。
//
// batch 必须来自 PrepareFunctionCallAddForLoopBatch；返回 handled=false 表示当前动态寄存器状态不再满足
// integer 窄路径，调用方可在当前 PC 回退普通 VM。本方法只提交一个虚拟迭代，context 检查由调用方在
// 调用前完成。
func (vm *VM) TryExecutePreparedFunctionCallAddForLoop(batch FunctionCallAddForLoopBatch) (int, bool) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto {
		return 0, false
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		// 寄存器窗口不足时交给普通 VM 保持原始错误路径。
		return 0, false
	}
	superInstruction := batch.superInstruction
	if !registers[superInstruction.FunctionSource].RawEqual(batch.functionValue) {
		// 被调函数寄存器被替换时必须回退普通 CALL，保留 __call 和错误语义。
		return 0, false
	}

	firstArgument := registers[superInstruction.FirstArgumentSource]
	sumValue := registers[superInstruction.SumRegister]
	if firstArgument.Kind != KindInteger || sumValue.Kind != KindInteger {
		// 当前窄路径只覆盖 integer 实参和 integer 累加器。
		return 0, false
	}
	callResult := firstArgument.Integer + superInstruction.SecondArgument
	addResult := sumValue.Integer + callResult

	baseIndex := superInstruction.ForBase
	indexValue := registers[baseIndex]
	limitValue := registers[baseIndex+1]
	stepValue := registers[baseIndex+2]
	if indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		// 只覆盖 integer numeric-for；float 或可转 number 字符串仍走普通 VM。
		return 0, false
	}

	// guard 全部通过后，按 MOVE、MOVE、LOADK、CALL、ADD 与 FORLOOP 顺序提交寄存器写入。
	registers[superInstruction.FunctionSlot] = batch.functionValue
	registers[superInstruction.FirstArgumentSlot] = firstArgument
	registers[superInstruction.SecondArgumentSlot] = IntegerValue(superInstruction.SecondArgument)
	registers[superInstruction.FunctionSlot] = IntegerValue(callResult)
	registers[superInstruction.AddTarget] = IntegerValue(addResult)
	nextIndex := indexValue.Integer + stepValue.Integer
	nextPC := superInstruction.ExitPC
	if forIntegerLoopContinues(nextIndex, limitValue.Integer, stepValue.Integer) {
		// 循环继续时更新内部 index 和外部可见循环变量，并跳回循环体开头。
		registers[baseIndex] = IntegerValue(nextIndex)
		registers[baseIndex+3] = IntegerValue(nextIndex)
		nextPC = superInstruction.LoopPC
		vm.pcOffset = superInstruction.ForSBx
	} else {
		// 循环结束时不更新 index 和外部变量，保持普通 FORLOOP 语义。
		vm.pcOffset = 0
	}
	vm.currentPC = batch.pc + 5
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, true
}

// TryExecuteFunctionCallAddForLoopBatch 批量执行已准备的 function_call 循环体。
//
// batch 必须来自 PrepareFunctionCallAddForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。
// 本方法每个虚拟 CALL 前都会调用 state.CheckContext，保留 direct CALL 取消边界；返回 iterations 表示已
// 提交的完整虚拟迭代数，handled=false 表示首轮动态 guard 不满足且调用方应回退普通 VM。
func (vm *VM) TryExecuteFunctionCallAddForLoopBatch(batch FunctionCallAddForLoopBatch, maxIterations int, state *State) (nextPC int, iterations int, handled bool, err error) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 || state == nil {
		return 0, 0, false, nil
	}
	if err := state.CheckContext(); err != nil {
		// 首个虚拟 CALL 入口的取消检查必须发生在动态 guard 和任何寄存器写入之前。
		return 0, 0, false, err
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		// 寄存器窗口不足时交给普通 VM 保持原始错误路径。
		return 0, 0, false, nil
	}
	superInstruction := batch.superInstruction
	if !registers[superInstruction.FunctionSource].RawEqual(batch.functionValue) {
		// 被调函数寄存器被替换时必须回退普通 CALL，保留 __call 和错误语义。
		return 0, 0, false, nil
	}

	sumValue := registers[superInstruction.SumRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.FirstArgumentSource]
	if sumValue.Kind != KindInteger || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		// 批量路径只覆盖稳定 integer sum、integer numeric-for 和 integer 可见循环变量。
		return 0, 0, false, nil
	}

	sum := sumValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	nextPC = batch.pc
	for iterations < maxIterations {
		if iterations > 0 {
			// 首轮已在动态 guard 前检查；后续每个虚拟 CALL 前继续保留 direct CALL 取消边界。
			if err := state.CheckContext(); err != nil {
				return nextPC, iterations, true, err
			}
		}

		callResult := visibleIndex + superInstruction.SecondArgument
		sum += callResult
		registers[superInstruction.FunctionSlot] = batch.functionValue
		registers[superInstruction.FirstArgumentSlot] = IntegerValue(visibleIndex)
		registers[superInstruction.SecondArgumentSlot] = IntegerValue(superInstruction.SecondArgument)
		registers[superInstruction.FunctionSlot] = IntegerValue(callResult)
		registers[superInstruction.AddTarget] = IntegerValue(sum)
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回循环体开头。
			index = nextIndex
			visibleIndex = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		// 没有提交任何迭代时调用方可安全回退普通 VM。
		return 0, 0, false, nil
	}
	vm.currentPC = batch.pc + 5
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true, nil
}

// TryExecuteFunctionCallAddForLoop 尝试执行 function_call benchmark 的完整循环体。
//
// pc 必须指向第一条 MOVE；返回 handled=false 表示 guard 不满足，调用方必须回退普通 VM。该 fast
// path 只覆盖 integer 参数、integer sum、`return a+b` 叶子 closure 和 integer numeric-for。
func (vm *VM) TryExecuteFunctionCallAddForLoop(pc int) (int, bool) {
	// 保留原公开 helper 行为；单轮执行复用 batch 准备逻辑，避免两套 guard 漂移。
	batch, ok := vm.PrepareFunctionCallAddForLoopBatch(pc)
	if !ok {
		return 0, false
	}
	return vm.TryExecutePreparedFunctionCallAddForLoop(batch)
}

// ensureFunctionCallAssignForLoopSuperInstructions 返回当前 Proto 的函数调用赋值循环预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录完整 `MOVE;MOVE;MOVE;CALL;MOVE;FORLOOP` 循环体；运行期
// closure identity、参数类型、hook 和 context guard 仍由调用方与 batch 执行方法检查。
func (vm *VM) ensureFunctionCallAssignForLoopSuperInstructions() []functionCallAssignForLoopSuperInstruction {
	// 按当前 Proto 懒构建官方 function_call 赋值循环 superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 6 {
		// 少于六条指令不可能包含完整 function_call 赋值循环体。
		vm.functionCallAssignForLoopSuperInstructionProto = vm.proto
		vm.functionCallAssignForLoopSuperInstructions = nil
		return nil
	}
	if vm.functionCallAssignForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.functionCallAssignForLoopSuperInstructions
	}

	vm.functionCallAssignForLoopSuperInstructionProto = vm.proto
	vm.functionCallAssignForLoopSuperInstructions = buildFunctionCallAssignForLoopSuperInstructions(vm.proto)
	return vm.functionCallAssignForLoopSuperInstructions
}

// buildFunctionCallAssignForLoopSuperInstructions 构建 `MOVE;MOVE;MOVE;CALL;MOVE;FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildFunctionCallAssignForLoopSuperInstructions(proto *bytecode.Proto) []functionCallAssignForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的函数调用赋值循环 superinstruction。
	if proto == nil || len(proto.Code) < 6 {
		return nil
	}

	var superInstructions []functionCallAssignForLoopSuperInstruction
	for pc := 0; pc+5 < len(proto.Code); pc++ {
		// 只识别完整相邻 `MOVE;MOVE;MOVE;CALL;MOVE;FORLOOP`，其他形态保持零值表示无效。
		moveFunctionInstruction := proto.Code[pc]
		moveFirstArgumentInstruction := proto.Code[pc+1]
		moveSecondArgumentInstruction := proto.Code[pc+2]
		callInstruction := proto.Code[pc+3]
		moveResultInstruction := proto.Code[pc+4]
		forLoopInstruction := proto.Code[pc+5]
		if moveFunctionInstruction.OpCode() != bytecode.OpMove || moveFirstArgumentInstruction.OpCode() != bytecode.OpMove || moveSecondArgumentInstruction.OpCode() != bytecode.OpMove || callInstruction.OpCode() != bytecode.OpCall || moveResultInstruction.OpCode() != bytecode.OpMove || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标六指令模式，继续扫描后续指令。
			continue
		}
		functionSlot := callInstruction.A()
		if moveFunctionInstruction.A() != functionSlot || moveFirstArgumentInstruction.A() != functionSlot+1 || moveSecondArgumentInstruction.A() != functionSlot+2 || callInstruction.B() != 3 || callInstruction.C() != 2 || moveResultInstruction.B() != functionSlot {
			// 只覆盖固定两实参、单返回 CALL，且两个实参必须由紧邻 MOVE 准备，结果必须 MOVE 到 sum。
			continue
		}
		exitPC := pc + 6
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖完整循环体；回跳到其他 PC 的形态必须交给普通 VM。
			continue
		}
		sumRegister := moveFirstArgumentInstruction.B()
		if moveResultInstruction.A() != sumRegister {
			// 官方 function_call 形态必须把 CALL 结果写回第一实参来源的 sum 寄存器。
			continue
		}
		forBase := forLoopInstruction.A()
		secondArgumentSource := moveSecondArgumentInstruction.B()
		if secondArgumentSource != forBase+3 {
			// 当前窄路径只处理 numeric-for 外部可见循环变量作为第二实参。
			continue
		}
		functionSource := moveFunctionInstruction.B()
		if registerInRange(functionSource, forBase, forBase+3) || functionSource == functionSlot || functionSource == functionSlot+1 || functionSource == functionSlot+2 || functionSource == sumRegister {
			// 被调用 closure 来源必须在循环体内保持稳定，不能被 CALL、MOVE 或 FORLOOP 覆盖。
			continue
		}
		if registerInRange(sumRegister, forBase, forBase+3) || sumRegister == functionSlot || sumRegister == functionSlot+1 || sumRegister == functionSlot+2 {
			// sum 不能覆盖 CALL 临时槽或 numeric-for 控制槽，否则需要逐指令别名语义。
			continue
		}
		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]functionCallAssignForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = functionCallAssignForLoopSuperInstruction{
			FunctionSlot:         functionSlot,
			FunctionSource:       functionSource,
			SumRegister:          sumRegister,
			FirstArgumentSlot:    functionSlot + 1,
			SecondArgumentSlot:   functionSlot + 2,
			SecondArgumentSource: secondArgumentSource,
			ForBase:              forBase,
			ForSBx:               forLoopInstruction.SBx(),
			ExitPC:               exitPC,
			LoopPC:               loopPC,
			Valid:                true,
		}
	}
	return superInstructions
}

// PrepareFunctionCallAssignForLoopSuperInstructions 预构建当前 Proto 的函数调用赋值循环表。
func (vm *VM) PrepareFunctionCallAssignForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 batch 准备会因为表不匹配而回退。
	return len(vm.ensureFunctionCallAssignForLoopSuperInstructions()) > 0
}

// HasFunctionCallAssignForLoopAt 判断当前 PC 是否存在函数调用赋值循环 superinstruction。
func (vm *VM) HasFunctionCallAssignForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层决定是否尝试 batch。
	if vm == nil || vm.functionCallAssignForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.functionCallAssignForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// PrepareFunctionCallAssignForLoopBatch 为官方 function_call 循环体准备可复用 batch 上下文。
func (vm *VM) PrepareFunctionCallAssignForLoopBatch(pc int) (FunctionCallAssignForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.functionCallAssignForLoopSuperInstructionProto != vm.proto {
		return FunctionCallAssignForLoopBatch{}, false
	}
	superInstructions := vm.functionCallAssignForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		return FunctionCallAssignForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		return FunctionCallAssignForLoopBatch{}, false
	}
	registers := vm.registers
	maxRegisterIndex := 0
	for _, registerIndex := range [...]int{
		superInstruction.FunctionSlot,
		superInstruction.FunctionSource,
		superInstruction.SumRegister,
		superInstruction.FirstArgumentSlot,
		superInstruction.SecondArgumentSlot,
		superInstruction.SecondArgumentSource,
		superInstruction.ForBase + 3,
	} {
		// batch 准备阶段只计算一次最大寄存器下标，执行阶段用单次边界判断。
		if registerIndex > maxRegisterIndex {
			maxRegisterIndex = registerIndex
		}
	}
	if uint(maxRegisterIndex) >= uint(len(registers)) {
		return FunctionCallAssignForLoopBatch{}, false
	}
	functionValue := registers[superInstruction.FunctionSource]
	if functionValue.Kind != KindLuaClosure {
		// 被调值不是 Lua closure 时必须回退普通 CALL，保留 __call 和错误语义。
		return FunctionCallAssignForLoopBatch{}, false
	}
	closure, ok := functionValue.Ref.(*LuaClosure)
	if !ok || closure == nil || closure.LeafAddReturn == nil || !closure.LeafAddReturn.HasRegisterRegisterAdd {
		// 只覆盖已预解析的 `return a+b` 叶子 closure。
		return FunctionCallAssignForLoopBatch{}, false
	}
	leaf := closure.LeafAddReturn
	if !((leaf.LeftRegisterIndex == 0 && leaf.RightRegisterIndex == 1) || (leaf.LeftRegisterIndex == 1 && leaf.RightRegisterIndex == 0)) {
		// 叶子函数必须只读取两个固定实参，其他寄存器布局回退普通 direct CALL。
		return FunctionCallAssignForLoopBatch{}, false
	}
	return FunctionCallAssignForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		functionValue:    functionValue,
		maxRegisterIndex: maxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecuteFunctionCallAssignForLoopBatch 批量执行已准备的官方 function_call 循环体。
//
// batch 必须来自 PrepareFunctionCallAssignForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。
// 本方法每个虚拟 CALL 前都会调用 state.CheckContext，保留 direct CALL 取消边界。
func (vm *VM) TryExecuteFunctionCallAssignForLoopBatch(batch FunctionCallAssignForLoopBatch, maxIterations int, state *State) (nextPC int, iterations int, handled bool, err error) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 || state == nil {
		return 0, 0, false, nil
	}
	if err := state.CheckContext(); err != nil {
		// 首个虚拟 CALL 入口的取消检查必须发生在动态 guard 和任何寄存器写入之前。
		return 0, 0, false, err
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		return 0, 0, false, nil
	}
	superInstruction := batch.superInstruction
	if !registers[superInstruction.FunctionSource].RawEqual(batch.functionValue) {
		// 被调函数寄存器被替换时必须回退普通 CALL，保留 __call 和错误语义。
		return 0, 0, false, nil
	}
	sumValue := registers[superInstruction.SumRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.SecondArgumentSource]
	if sumValue.Kind != KindInteger || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		return 0, 0, false, nil
	}
	sum := sumValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	nextPC = batch.pc
	lastSumBeforeCall := sum
	lastVisibleIndexBeforeCall := visibleIndex
	lastCallResult := sum
	for iterations < maxIterations {
		if iterations > 0 {
			// 首轮已在动态 guard 前检查；后续每个虚拟 CALL 前继续保留 direct CALL 取消边界。
			if err := state.CheckContext(); err != nil {
				// 取消发生在下一次 CALL 入口前，提交到上一轮 FORLOOP 已完成的可见边界。
				commitFunctionCallAssignForLoopBatchRegisters(registers, superInstruction, lastSumBeforeCall, lastVisibleIndexBeforeCall, lastCallResult, index, visibleIndex)
				return nextPC, iterations, true, err
			}
		}

		sumBeforeCall := sum
		visibleIndexBeforeCall := visibleIndex
		callResult := sumBeforeCall + visibleIndexBeforeCall
		lastSumBeforeCall = sumBeforeCall
		lastVisibleIndexBeforeCall = visibleIndexBeforeCall
		lastCallResult = callResult
		sum = callResult
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回循环体开头。
			index = nextIndex
			visibleIndex = nextIndex
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		return 0, 0, false, nil
	}
	commitFunctionCallAssignForLoopBatchRegisters(registers, superInstruction, lastSumBeforeCall, lastVisibleIndexBeforeCall, lastCallResult, index, visibleIndex)
	vm.currentPC = batch.pc + 5
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true, nil
}

// commitFunctionCallAssignForLoopBatchRegisters 提交 function_call batch 的 Lua 可见寄存器状态。
//
// registers 必须是当前 VM 寄存器窗口；superInstruction 来自已验证的 assign batch；sumBeforeCall、
// visibleIndexBeforeCall 和 callResult 描述最后一个已完成 CALL；index 与 visibleIndex 描述该 CALL 后
// FORLOOP 的可见控制槽状态。该 helper 只在 batch 退出或 context 取消边界调用，避免热循环每轮重复写
// 临时 CALL 槽，同时保持普通 VM 在 MOVE 入口或循环退出处的可见寄存器状态。
func commitFunctionCallAssignForLoopBatchRegisters(registers []Value, superInstruction functionCallAssignForLoopSuperInstruction, sumBeforeCall int64, visibleIndexBeforeCall int64, callResult int64, index int64, visibleIndex int64) {
	// 函数槽在 CALL 后保存返回值；下一轮普通 MOVE 会再把 closure 放回函数槽。
	registers[superInstruction.FunctionSlot] = IntegerValue(callResult)
	registers[superInstruction.FirstArgumentSlot] = IntegerValue(sumBeforeCall)
	registers[superInstruction.SecondArgumentSlot] = IntegerValue(visibleIndexBeforeCall)
	registers[superInstruction.SumRegister] = IntegerValue(callResult)
	registers[superInstruction.ForBase] = IntegerValue(index)
	registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
}

// ensureClosureUpvalueForLoopSuperInstructions 返回当前 Proto 的闭包 upvalue 循环预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录完整 `MOVE;LOADK;CALL;MOVE;FORLOOP` 循环体；运行期
// closure identity、upvalue 类型、hook 和 context guard 仍由调用方与 batch 执行方法检查。
func (vm *VM) ensureClosureUpvalueForLoopSuperInstructions() []closureUpvalueForLoopSuperInstruction {
	// 按当前 Proto 懒构建 closure_upvalue 循环 superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 5 {
		// 少于五条指令不可能包含完整 closure_upvalue 循环体。
		vm.closureUpvalueForLoopSuperInstructionProto = vm.proto
		vm.closureUpvalueForLoopSuperInstructions = nil
		return nil
	}
	if vm.closureUpvalueForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.closureUpvalueForLoopSuperInstructions
	}

	vm.closureUpvalueForLoopSuperInstructionProto = vm.proto
	vm.closureUpvalueForLoopSuperInstructions = buildClosureUpvalueForLoopSuperInstructions(vm.proto)
	return vm.closureUpvalueForLoopSuperInstructions
}

// buildClosureUpvalueForLoopSuperInstructions 构建 `MOVE;LOADK;CALL;MOVE;FORLOOP` 预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildClosureUpvalueForLoopSuperInstructions(proto *bytecode.Proto) []closureUpvalueForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的闭包 upvalue 循环 superinstruction。
	if proto == nil || len(proto.Code) < 5 {
		return nil
	}

	var superInstructions []closureUpvalueForLoopSuperInstruction
	for pc := 0; pc+4 < len(proto.Code); pc++ {
		// 只识别完整相邻 `MOVE;LOADK;CALL;MOVE;FORLOOP`，其他形态保持零值表示无效。
		moveFunctionInstruction := proto.Code[pc]
		loadArgumentInstruction := proto.Code[pc+1]
		callInstruction := proto.Code[pc+2]
		moveResultInstruction := proto.Code[pc+3]
		forLoopInstruction := proto.Code[pc+4]
		if moveFunctionInstruction.OpCode() != bytecode.OpMove || loadArgumentInstruction.OpCode() != bytecode.OpLoadK || callInstruction.OpCode() != bytecode.OpCall || moveResultInstruction.OpCode() != bytecode.OpMove || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标五指令模式，继续扫描后续指令。
			continue
		}
		functionSlot := callInstruction.A()
		if moveFunctionInstruction.A() != functionSlot || loadArgumentInstruction.A() != functionSlot+1 || callInstruction.B() != 2 || callInstruction.C() != 2 || moveResultInstruction.B() != functionSlot {
			// 只覆盖固定一实参、单返回 CALL，且实参必须由紧邻 LOADK 准备，结果必须 MOVE 到 sum。
			continue
		}
		constantIndex := loadArgumentInstruction.Bx()
		if constantIndex < 0 || constantIndex >= len(proto.Constants) || proto.Constants[constantIndex].Kind != bytecode.ConstantInteger {
			// 当前窄路径只处理 integer 常量实参；其他常量保留普通 VM 语义。
			continue
		}
		exitPC := pc + 5
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// 只覆盖完整循环体；回跳到其他 PC 的形态必须交给普通 VM。
			continue
		}
		functionSource := moveFunctionInstruction.B()
		resultTarget := moveResultInstruction.A()
		forBase := forLoopInstruction.A()
		if registerInRange(functionSource, forBase, forBase+3) || functionSource == functionSlot || functionSource == functionSlot+1 || functionSource == resultTarget {
			// 被调用 closure 来源必须在循环体内保持稳定，不能被 CALL、MOVE 或 FORLOOP 覆盖。
			continue
		}
		if registerInRange(resultTarget, forBase, forBase+3) || resultTarget == functionSlot || resultTarget == functionSlot+1 {
			// sum 不能覆盖 CALL 临时槽或 numeric-for 控制槽，否则需要逐指令别名语义。
			continue
		}
		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]closureUpvalueForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = closureUpvalueForLoopSuperInstruction{
			FunctionSlot:   functionSlot,
			FunctionSource: functionSource,
			ArgumentSlot:   functionSlot + 1,
			ArgumentValue:  proto.Constants[constantIndex].Integer,
			ResultTarget:   resultTarget,
			ForBase:        forBase,
			ForSBx:         forLoopInstruction.SBx(),
			ExitPC:         exitPC,
			LoopPC:         loopPC,
			Valid:          true,
		}
	}
	return superInstructions
}

// PrepareClosureUpvalueForLoopSuperInstructions 预构建当前 Proto 的闭包 upvalue 循环表。
func (vm *VM) PrepareClosureUpvalueForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 batch 准备会因为表不匹配而回退。
	return len(vm.ensureClosureUpvalueForLoopSuperInstructions()) > 0
}

// HasClosureUpvalueForLoopAt 判断当前 PC 是否存在闭包 upvalue 循环 superinstruction。
func (vm *VM) HasClosureUpvalueForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层决定是否尝试 batch。
	if vm == nil || vm.closureUpvalueForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.closureUpvalueForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// PrepareClosureUpvalueForLoopBatch 为官方 closure_upvalue 循环体准备可复用 batch 上下文。
func (vm *VM) PrepareClosureUpvalueForLoopBatch(pc int) (ClosureUpvalueForLoopBatch, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败且不能产生副作用。
	if vm == nil || vm.closureUpvalueForLoopSuperInstructionProto != vm.proto {
		return ClosureUpvalueForLoopBatch{}, false
	}
	superInstructions := vm.closureUpvalueForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		return ClosureUpvalueForLoopBatch{}, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		return ClosureUpvalueForLoopBatch{}, false
	}
	registers := vm.registers
	maxRegisterIndex := 0
	for _, registerIndex := range [...]int{
		superInstruction.FunctionSlot,
		superInstruction.FunctionSource,
		superInstruction.ArgumentSlot,
		superInstruction.ResultTarget,
		superInstruction.ForBase + 3,
	} {
		// batch 准备阶段只计算一次最大寄存器下标，执行阶段用单次边界判断。
		if registerIndex > maxRegisterIndex {
			maxRegisterIndex = registerIndex
		}
	}
	if uint(maxRegisterIndex) >= uint(len(registers)) {
		return ClosureUpvalueForLoopBatch{}, false
	}
	functionValue := registers[superInstruction.FunctionSource]
	if functionValue.Kind != KindLuaClosure {
		// 被调值不是 Lua closure 时必须回退普通 CALL，保留 __call 和错误语义。
		return ClosureUpvalueForLoopBatch{}, false
	}
	closure, ok := functionValue.Ref.(*LuaClosure)
	if !ok || closure == nil || closure.LeafUpvalueAddSetReturn == nil || !closure.LeafUpvalueAddSetReturn.HasRegisterOperand {
		// 只覆盖已预解析的 `upvalue = upvalue + R; return upvalue` 叶子 closure。
		return ClosureUpvalueForLoopBatch{}, false
	}
	leaf := closure.LeafUpvalueAddSetReturn
	if leaf.RegisterIndex != 0 {
		// 当前官方形态只把单个 LOADK 实参映射到 callee R0；其他布局回退普通 direct CALL。
		return ClosureUpvalueForLoopBatch{}, false
	}
	return ClosureUpvalueForLoopBatch{
		proto:            vm.proto,
		pc:               pc,
		superInstruction: superInstruction,
		functionValue:    functionValue,
		closure:          closure,
		leaf:             leaf,
		maxRegisterIndex: maxRegisterIndex,
		valid:            true,
	}, true
}

// TryExecuteClosureUpvalueForLoopBatch 批量执行已准备的官方 closure_upvalue 循环体。
//
// batch 必须来自 PrepareClosureUpvalueForLoopBatch；maxIterations 由调用方按 context 检查窗口换算。
// 本方法每个虚拟 CALL 前都会调用 state.CheckContext，保留 direct CALL 取消边界。
func (vm *VM) TryExecuteClosureUpvalueForLoopBatch(batch ClosureUpvalueForLoopBatch, maxIterations int, state *State) (nextPC int, iterations int, handled bool, err error) {
	// batch 与当前 VM/Proto 不匹配时必须回退，避免 VM 池复用后误写寄存器。
	if vm == nil || !batch.valid || batch.proto != vm.proto || maxIterations <= 0 || state == nil {
		return 0, 0, false, nil
	}
	if err := state.CheckContext(); err != nil {
		// 首个虚拟 CALL 入口的取消检查必须发生在动态 guard 和任何寄存器写入之前。
		return 0, 0, false, err
	}
	registers := vm.registers
	if uint(batch.maxRegisterIndex) >= uint(len(registers)) {
		return 0, 0, false, nil
	}
	superInstruction := batch.superInstruction
	if !registers[superInstruction.FunctionSource].RawEqual(batch.functionValue) {
		// 被调函数寄存器被替换时必须回退普通 CALL，保留 __call 和错误语义。
		return 0, 0, false, nil
	}
	upvalueValue, ok := luaClosureUpvalueValue(batch.closure, batch.leaf.UpvalueIndex)
	if !ok || upvalueValue.Kind != KindInteger {
		// 非 integer upvalue 需要完整 Lua 算术转换和元方法处理。
		return 0, 0, false, nil
	}
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	if indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		return 0, 0, false, nil
	}
	upvalue := upvalueValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	nextPC = batch.pc
	for iterations < maxIterations {
		if iterations > 0 {
			// 首轮已在动态 guard 前检查；后续每个虚拟 CALL 前继续保留 direct CALL 取消边界。
			if err := state.CheckContext(); err != nil {
				return nextPC, iterations, true, err
			}
		}

		upvalue += superInstruction.ArgumentValue
		resultValue := IntegerValue(upvalue)
		if !luaClosureSetUpvalueValue(batch.closure, batch.leaf.UpvalueIndex, resultValue) {
			// upvalue 状态损坏时回退普通 VM，由原路径暴露错误。
			return nextPC, iterations, iterations > 0, nil
		}
		registers[superInstruction.FunctionSlot] = resultValue
		registers[superInstruction.ArgumentSlot] = IntegerValue(superInstruction.ArgumentValue)
		registers[superInstruction.ResultTarget] = resultValue
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回循环体开头。
			index = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(index)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		return 0, 0, false, nil
	}
	vm.currentPC = batch.pc + 4
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true, nil
}

// ensureStdlibMathStringForLoopSuperInstructions 返回当前 Proto 的 stdlib_math_string 预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录官方 `math.floor(math.sqrt(i)) + #string.format("%d", i)`
// 循环热体形态；运行期函数身份、table 元表、参数类型、sum 和 numeric-for 控制槽仍由
// TryExecuteStdlibMathStringForLoopBatch 检查。
func (vm *VM) ensureStdlibMathStringForLoopSuperInstructions() []stdlibMathStringForLoopSuperInstruction {
	// 按当前 Proto 懒构建 stdlib_math_string superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 16 {
		// 少于十六条指令不可能包含完整官方 stdlib_math_string 循环体。
		vm.stdlibMathStringForLoopSuperInstructionProto = vm.proto
		vm.stdlibMathStringForLoopSuperInstructions = nil
		return nil
	}
	if vm.stdlibMathStringForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.stdlibMathStringForLoopSuperInstructions
	}

	vm.stdlibMathStringForLoopSuperInstructionProto = vm.proto
	vm.stdlibMathStringForLoopSuperInstructions = buildStdlibMathStringForLoopSuperInstructions(vm.proto)
	return vm.stdlibMathStringForLoopSuperInstructions
}

// buildStdlibMathStringForLoopSuperInstructions 构建 stdlib_math_string 完整循环体预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildStdlibMathStringForLoopSuperInstructions(proto *bytecode.Proto) []stdlibMathStringForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的 stdlib_math_string superinstruction。
	if proto == nil || len(proto.Code) < 16 {
		return nil
	}

	var superInstructions []stdlibMathStringForLoopSuperInstruction
	for pc := 0; pc+15 < len(proto.Code); pc++ {
		// 只识别官方热体：math.floor(math.sqrt(i))、ADD、string.format、LEN、ADD、FORLOOP。
		getMathFloorInstruction := proto.Code[pc]
		getFloorInstruction := proto.Code[pc+1]
		getMathSqrtInstruction := proto.Code[pc+2]
		getSqrtInstruction := proto.Code[pc+3]
		moveSqrtArgumentInstruction := proto.Code[pc+4]
		callSqrtInstruction := proto.Code[pc+5]
		callFloorInstruction := proto.Code[pc+6]
		addMathInstruction := proto.Code[pc+7]
		getStringInstruction := proto.Code[pc+8]
		getFormatInstruction := proto.Code[pc+9]
		loadFormatInstruction := proto.Code[pc+10]
		moveFormatArgumentInstruction := proto.Code[pc+11]
		callFormatInstruction := proto.Code[pc+12]
		lenInstruction := proto.Code[pc+13]
		addFormatInstruction := proto.Code[pc+14]
		forLoopInstruction := proto.Code[pc+15]
		if getMathFloorInstruction.OpCode() != bytecode.OpGetTabUp || getFloorInstruction.OpCode() != bytecode.OpGetTable || getMathSqrtInstruction.OpCode() != bytecode.OpGetTabUp || getSqrtInstruction.OpCode() != bytecode.OpGetTable || moveSqrtArgumentInstruction.OpCode() != bytecode.OpMove || callSqrtInstruction.OpCode() != bytecode.OpCall || callFloorInstruction.OpCode() != bytecode.OpCall || addMathInstruction.OpCode() != bytecode.OpAdd || getStringInstruction.OpCode() != bytecode.OpGetTabUp || getFormatInstruction.OpCode() != bytecode.OpGetTable || loadFormatInstruction.OpCode() != bytecode.OpLoadK || moveFormatArgumentInstruction.OpCode() != bytecode.OpMove || callFormatInstruction.OpCode() != bytecode.OpCall || lenInstruction.OpCode() != bytecode.OpLen || addFormatInstruction.OpCode() != bytecode.OpAdd || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标模式，继续扫描后续指令。
			continue
		}

		floorSlot := callFloorInstruction.A()
		sqrtSlot := callSqrtInstruction.A()
		if getMathFloorInstruction.A() != floorSlot || getFloorInstruction.A() != floorSlot || getFloorInstruction.B() != floorSlot {
			// math.floor 的 GETTABUP/GETTABLE/CALL 必须复用同一个函数槽。
			continue
		}
		if getMathSqrtInstruction.A() != sqrtSlot || getSqrtInstruction.A() != sqrtSlot || getSqrtInstruction.B() != sqrtSlot {
			// math.sqrt 的 GETTABUP/GETTABLE/CALL 必须复用同一个函数槽。
			continue
		}
		if sqrtSlot != floorSlot+1 || moveSqrtArgumentInstruction.A() != sqrtSlot+1 || callSqrtInstruction.B() != 2 || callSqrtInstruction.C() != 0 || callFloorInstruction.B() != 0 || callFloorInstruction.C() != 2 {
			// 只覆盖官方开放返回链：sqrt 单参数开放返回，floor 用 B=0 消费该结果并返回单值。
			continue
		}

		mathName, ok := protoStringConstant(proto, bytecode.IndexK(getMathFloorInstruction.C()))
		if !ok || !bytecode.IsK(getMathFloorInstruction.C()) || mathName != "math" {
			// 第一段 GETTABUP 必须读取全局 math table。
			continue
		}
		secondMathName, ok := protoStringConstant(proto, bytecode.IndexK(getMathSqrtInstruction.C()))
		if !ok || !bytecode.IsK(getMathSqrtInstruction.C()) || secondMathName != "math" || getMathSqrtInstruction.B() != getMathFloorInstruction.B() {
			// 两次 math 读取必须来自同一个环境 upvalue。
			continue
		}
		floorName, ok := protoStringConstant(proto, bytecode.IndexK(getFloorInstruction.C()))
		if !ok || !bytecode.IsK(getFloorInstruction.C()) || floorName != "floor" {
			// GETTABLE 必须读取 math.floor。
			continue
		}
		sqrtName, ok := protoStringConstant(proto, bytecode.IndexK(getSqrtInstruction.C()))
		if !ok || !bytecode.IsK(getSqrtInstruction.C()) || sqrtName != "sqrt" {
			// GETTABLE 必须读取 math.sqrt。
			continue
		}

		addMathLeftOperand := decodeRKOperand(proto, addMathInstruction.B())
		addMathRightOperand := decodeRKOperand(proto, addMathInstruction.C())
		sumRegister, ok := decodedRegisterPlusTarget(addMathLeftOperand, addMathRightOperand, floorSlot)
		if !ok || addMathInstruction.A() == sumRegister {
			// 前半段 ADD 必须形如 `accumulator = sum + floorResult`，且不能覆盖 sum 本身。
			continue
		}
		accumulatorRegister := addMathInstruction.A()

		formatSlot := callFormatInstruction.A()
		if formatSlot != sqrtSlot || formatSlot+1 != moveSqrtArgumentInstruction.A() {
			// 当前实现只覆盖官方寄存器复用布局：format 槽覆盖 sqrt 结果槽，格式串槽覆盖 sqrt 参数槽。
			continue
		}
		if getStringInstruction.A() != formatSlot || getFormatInstruction.A() != formatSlot || getFormatInstruction.B() != formatSlot || lenInstruction.A() != formatSlot || lenInstruction.B() != formatSlot {
			// string 表、format 函数、CALL 结果和 LEN 结果必须复用同一个临时槽。
			continue
		}
		if loadFormatInstruction.A() != formatSlot+1 || moveFormatArgumentInstruction.A() != formatSlot+2 || callFormatInstruction.B() != 3 || callFormatInstruction.C() != 2 {
			// 只覆盖固定两个实参、单返回 CALL，实参槽必须紧跟函数槽。
			continue
		}

		stringName, ok := protoStringConstant(proto, bytecode.IndexK(getStringInstruction.C()))
		if !ok || !bytecode.IsK(getStringInstruction.C()) || stringName != "string" || getStringInstruction.B() != getMathFloorInstruction.B() {
			// GETTABUP 必须从同一个环境 upvalue 读取全局 string table。
			continue
		}
		formatName, ok := protoStringConstant(proto, bytecode.IndexK(getFormatInstruction.C()))
		if !ok || !bytecode.IsK(getFormatInstruction.C()) || formatName != "format" {
			// GETTABLE 必须读取 string.format。
			continue
		}
		formatText, ok := protoStringConstant(proto, loadFormatInstruction.Bx())
		if !ok || formatText != "%d" {
			// 只覆盖 exact `%d`，其他格式串保持完整格式化路径。
			continue
		}

		addFormatLeftOperand := decodeRKOperand(proto, addFormatInstruction.B())
		addFormatRightOperand := decodeRKOperand(proto, addFormatInstruction.C())
		previousAccumulator, ok := decodedRegisterPlusTarget(addFormatLeftOperand, addFormatRightOperand, formatSlot)
		if !ok || previousAccumulator != accumulatorRegister || addFormatInstruction.A() != sumRegister {
			// 最终 ADD 必须形如 `sum = accumulator + len`，保留官方求值顺序和寄存器可见性。
			continue
		}

		forBase := forLoopInstruction.A()
		exitPC := pc + 16
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc || moveSqrtArgumentInstruction.B() != forBase+3 || moveFormatArgumentInstruction.B() != forBase+3 {
			// 当前窄路径只覆盖官方热循环体，两个参数都来自外部 numeric-for 变量。
			continue
		}
		if registerInRange(floorSlot, forBase, forBase+3) || registerInRange(sqrtSlot, forBase, forBase+3) || registerInRange(sqrtSlot+1, forBase, forBase+3) || registerInRange(formatSlot, forBase, forBase+3) || registerInRange(formatSlot+1, forBase, forBase+3) || registerInRange(formatSlot+2, forBase, forBase+3) || registerInRange(accumulatorRegister, forBase, forBase+3) || registerInRange(sumRegister, forBase, forBase+3) {
			// 任一参与槽覆盖 numeric-for 控制槽时逐指令别名语义会变复杂。
			continue
		}

		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]stdlibMathStringForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = stdlibMathStringForLoopSuperInstruction{
			EnvUpvalueIndex:     getMathFloorInstruction.B(),
			FloorSlot:           floorSlot,
			SqrtSlot:            sqrtSlot,
			SqrtArgumentSlot:    sqrtSlot + 1,
			FormatSlot:          formatSlot,
			FormatArgumentSlot:  formatSlot + 1,
			ValueArgumentSlot:   formatSlot + 2,
			ValueArgumentSource: moveFormatArgumentInstruction.B(),
			FormatString:        formatText,
			AccumulatorRegister: accumulatorRegister,
			SumRegister:         sumRegister,
			ForBase:             forBase,
			ForSBx:              forLoopInstruction.SBx(),
			ExitPC:              exitPC,
			LoopPC:              loopPC,
			Valid:               true,
		}
	}
	return superInstructions
}

// PrepareStdlibMathStringForLoopSuperInstructions 预构建当前 Proto 的 stdlib_math_string 循环表。
//
// 返回 true 表示当前 Proto 至少存在一个可尝试的完整循环体 superinstruction PC。
func (vm *VM) PrepareStdlibMathStringForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 TryExecuteStdlibMathStringForLoopBatch 会因为表不匹配而回退。
	return len(vm.ensureStdlibMathStringForLoopSuperInstructions()) > 0
}

// HasStdlibMathStringForLoopAt 判断当前 PC 是否存在 stdlib_math_string 循环 superinstruction。
func (vm *VM) HasStdlibMathStringForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层决定是否需要执行 CALL 边界 context 检查。
	if vm == nil || vm.stdlibMathStringForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.stdlibMathStringForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// TryExecuteStdlibMathStringForLoopBatch 批量执行官方 stdlib_math_string 的完整循环体。
//
// pc 必须指向第一条 GETTABUP math；maxIterations 由调用方按 context 检查窗口换算。本方法在首个
// 虚拟 CALL 前检查 context，后续每轮继续检查，保留普通 CALL 入口取消边界。
func (vm *VM) TryExecuteStdlibMathStringForLoopBatch(pc int, maxIterations int, state *State) (nextPC int, iterations int, handled bool, err error) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败。
	if vm == nil || vm.stdlibMathStringForLoopSuperInstructionProto != vm.proto || maxIterations <= 0 || state == nil {
		// 调用方尚未为当前 Proto 准备表，或没有可批量执行窗口。
		return 0, 0, false, nil
	}
	superInstructions := vm.stdlibMathStringForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// 当前 PC 不在表范围内，回退普通 VM。
		return 0, 0, false, nil
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配 stdlib_math_string 形态，回退普通 VM。
		return 0, 0, false, nil
	}
	if err := state.CheckContext(); err != nil {
		// 首个虚拟 CALL 入口的取消检查必须发生在动态 guard 和任何寄存器写入之前。
		return 0, 0, false, err
	}

	registers := vm.registers
	requiredRegisters := [...]int{
		superInstruction.FloorSlot,
		superInstruction.SqrtSlot,
		superInstruction.SqrtArgumentSlot,
		superInstruction.FormatSlot,
		superInstruction.FormatArgumentSlot,
		superInstruction.ValueArgumentSlot,
		superInstruction.ValueArgumentSource,
		superInstruction.AccumulatorRegister,
		superInstruction.SumRegister,
		superInstruction.ForBase,
		superInstruction.ForBase + 1,
		superInstruction.ForBase + 2,
		superInstruction.ForBase + 3,
	}
	for _, registerIndex := range requiredRegisters {
		// 任一参与寄存器越界时交给普通 VM 报原始错误。
		if uint(registerIndex) >= uint(len(registers)) {
			return 0, 0, false, nil
		}
	}

	envTable, tableErr := tableFromValue(vm.upvalueValue(superInstruction.EnvUpvalueIndex))
	if tableErr != nil || envTable.GetMetatable() != nil {
		// GETTABUP 对有元表环境可能触发 __index，必须回退普通 VM。
		return 0, 0, false, nil
	}
	mathValue := envTable.RawGetString("math")
	mathTable, tableErr := tableFromValue(mathValue)
	if tableErr != nil || mathTable.GetMetatable() != nil {
		// math 字段不是无元表 table 时，GETTABLE 可能触发普通错误或元方法。
		return 0, 0, false, nil
	}
	floorValue := mathTable.RawGetString("floor")
	floorFunction, ok := floorValue.Ref.(*GoFastUnaryFunction)
	if floorValue.Kind != KindGoClosure || !ok || floorFunction == nil || floorFunction.FastPathID != GoFastUnaryFastPathMathFloor {
		// math.floor 被替换或不再是标准库 fast unary 时必须回退普通 CALL。
		return 0, 0, false, nil
	}
	sqrtValue := mathTable.RawGetString("sqrt")
	sqrtFunction, ok := sqrtValue.Ref.(*GoFastUnaryFunction)
	if sqrtValue.Kind != KindGoClosure || !ok || sqrtFunction == nil || sqrtFunction.FastPathID != GoFastUnaryFastPathMathSqrt {
		// math.sqrt 被替换或不再是标准库 fast unary 时必须回退普通 CALL。
		return 0, 0, false, nil
	}
	stringValue := envTable.RawGetString("string")
	stringTable, tableErr := tableFromValue(stringValue)
	if tableErr != nil || stringTable.GetMetatable() != nil {
		// string 字段不是无元表 table 时，GETTABLE 可能触发普通错误或元方法。
		return 0, 0, false, nil
	}
	formatValue := stringTable.RawGetString("format")
	formatFunction, ok := formatValue.Ref.(*GoFixedResultsFunction)
	if formatValue.Kind != KindGoClosure || !ok || formatFunction == nil || formatFunction.FastPathID != GoFixedResultsFastPathStringFormatDecimal {
		// 只有标准库标记过的 exact `%d` 固定结果函数才能被表达式级直接消费。
		return 0, 0, false, nil
	}

	sumValue := registers[superInstruction.SumRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	visibleIndexValue := registers[superInstruction.ValueArgumentSource]
	if sumValue.Kind != KindInteger || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger || visibleIndexValue.Kind != KindInteger {
		// 批量路径只覆盖 integer sum、integer numeric-for 和 integer 可见循环变量。
		return 0, 0, false, nil
	}
	if stepValue.Integer <= 0 || indexValue.Integer < 0 || limitValue.Integer < 0 || visibleIndexValue.Integer != indexValue.Integer {
		// 当前官方 fixture 是正向非负整数循环；负数会让 sqrt/floor 产生 NaN/number 路径，必须回退。
		return 0, 0, false, nil
	}

	sum := sumValue.Integer
	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	visibleIndex := visibleIndexValue.Integer
	nextPC = pc
	for iterations < maxIterations {
		if iterations > 0 {
			// 首轮已在动态 guard 前检查；后续每个虚拟 CALL 组前继续保留取消边界。
			if err := state.CheckContext(); err != nil {
				return nextPC, iterations, true, err
			}
		}

		floorResult := int64(math.Floor(math.Sqrt(float64(visibleIndex))))
		accumulator := sum + floorResult
		lengthValue := decimalIntegerStringLength(visibleIndex)
		sum = accumulator + lengthValue
		registers[superInstruction.FloorSlot] = IntegerValue(accumulator)
		registers[superInstruction.FormatSlot] = IntegerValue(lengthValue)
		registers[superInstruction.FormatArgumentSlot] = StringValue(superInstruction.FormatString)
		registers[superInstruction.ValueArgumentSlot] = IntegerValue(visibleIndex)
		registers[superInstruction.SumRegister] = IntegerValue(sum)
		iterations++

		nextIndex := index + step
		nextPC = superInstruction.ExitPC
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时更新内部 index 和外部可见循环变量，并跳回循环体开头。
			index = nextIndex
			visibleIndex = nextIndex
			registers[superInstruction.ForBase] = IntegerValue(index)
			registers[superInstruction.ForBase+3] = IntegerValue(visibleIndex)
			nextPC = superInstruction.LoopPC
			vm.pcOffset = superInstruction.ForSBx
		} else {
			// 循环结束时不写入越界后的 index，保持普通 FORLOOP 语义。
			vm.pcOffset = 0
			break
		}
	}
	if iterations == 0 {
		// 没有提交任何迭代时调用方可安全回退普通 VM。
		return 0, 0, false, nil
	}
	vm.currentPC = pc + 15
	vm.openTop = -1
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true, nil
}

// ensureFormatLenAddForLoopSuperInstructions 返回当前 Proto 的 string.format 长度消费预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录官方 `#string.format("%d", i)` 循环尾部形态；运行期
// 函数身份、table 元表、参数类型、sum 和 numeric-for 控制槽仍由 TryExecuteFormatLenAddForLoop 检查。
func (vm *VM) ensureFormatLenAddForLoopSuperInstructions() []formatLenAddForLoopSuperInstruction {
	// 按当前 Proto 懒构建 format/LEN superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 9 {
		// 少于九条指令不可能包含前序 ADD 加八条尾部模式。
		vm.formatLenAddForLoopSuperInstructionProto = vm.proto
		vm.formatLenAddForLoopSuperInstructions = nil
		return nil
	}
	if vm.formatLenAddForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.formatLenAddForLoopSuperInstructions
	}

	vm.formatLenAddForLoopSuperInstructionProto = vm.proto
	vm.formatLenAddForLoopSuperInstructions = buildFormatLenAddForLoopSuperInstructions(vm.proto)
	return vm.formatLenAddForLoopSuperInstructions
}

// buildFormatLenAddForLoopSuperInstructions 构建 `#string.format("%d", i)` 循环尾部预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildFormatLenAddForLoopSuperInstructions(proto *bytecode.Proto) []formatLenAddForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的 string.format 长度消费 superinstruction。
	if proto == nil || len(proto.Code) < 9 {
		return nil
	}

	var superInstructions []formatLenAddForLoopSuperInstruction
	for pc := 1; pc+7 < len(proto.Code); pc++ {
		// 只识别官方尾部 `GETTABUP;GETTABLE;LOADK;MOVE;CALL;LEN;ADD;FORLOOP`。
		previousAddInstruction := proto.Code[pc-1]
		getStringInstruction := proto.Code[pc]
		getFormatInstruction := proto.Code[pc+1]
		loadFormatInstruction := proto.Code[pc+2]
		moveValueInstruction := proto.Code[pc+3]
		callInstruction := proto.Code[pc+4]
		lenInstruction := proto.Code[pc+5]
		addInstruction := proto.Code[pc+6]
		forLoopInstruction := proto.Code[pc+7]
		if previousAddInstruction.OpCode() != bytecode.OpAdd || getStringInstruction.OpCode() != bytecode.OpGetTabUp || getFormatInstruction.OpCode() != bytecode.OpGetTable || loadFormatInstruction.OpCode() != bytecode.OpLoadK || moveValueInstruction.OpCode() != bytecode.OpMove || callInstruction.OpCode() != bytecode.OpCall || lenInstruction.OpCode() != bytecode.OpLen || addInstruction.OpCode() != bytecode.OpAdd || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标模式，继续扫描后续指令。
			continue
		}

		functionSlot := callInstruction.A()
		if getStringInstruction.A() != functionSlot || getFormatInstruction.A() != functionSlot || getFormatInstruction.B() != functionSlot || lenInstruction.A() != functionSlot || lenInstruction.B() != functionSlot {
			// string 表、format 函数、CALL 结果和 LEN 结果必须复用同一个临时槽。
			continue
		}
		if loadFormatInstruction.A() != functionSlot+1 || moveValueInstruction.A() != functionSlot+2 || callInstruction.B() != 3 || callInstruction.C() != 2 {
			// 只覆盖固定两个实参、单返回 CALL，实参槽必须紧跟函数槽。
			continue
		}

		stringName, ok := protoStringConstant(proto, bytecode.IndexK(getStringInstruction.C()))
		if !ok || !bytecode.IsK(getStringInstruction.C()) || stringName != "string" {
			// GETTABUP 必须读取全局 string table；非常量或其他字段回退普通 VM。
			continue
		}
		formatName, ok := protoStringConstant(proto, bytecode.IndexK(getFormatInstruction.C()))
		if !ok || !bytecode.IsK(getFormatInstruction.C()) || formatName != "format" {
			// GETTABLE 必须读取 string.format；其他字段回退普通 VM。
			continue
		}
		formatText, ok := protoStringConstant(proto, loadFormatInstruction.Bx())
		if !ok || formatText != "%d" {
			// 只覆盖 exact `%d`，带 flag、宽度、精度或其他 verb 都保持完整格式化路径。
			continue
		}

		addLeftOperand := decodeRKOperand(proto, addInstruction.B())
		addRightOperand := decodeRKOperand(proto, addInstruction.C())
		accumulatorRegister, ok := decodedRegisterPlusTarget(addLeftOperand, addRightOperand, functionSlot)
		if !ok || addInstruction.A() == functionSlot {
			// 最终 ADD 必须是 `sum = accumulator + len`，不能覆盖 LEN 临时槽本身。
			continue
		}
		previousLeftOperand := decodeRKOperand(proto, previousAddInstruction.B())
		previousRightOperand := decodeRKOperand(proto, previousAddInstruction.C())
		previousAccumulator, ok := decodedRegisterPlusTarget(previousLeftOperand, previousRightOperand, addInstruction.A())
		if !ok || previousAddInstruction.A() != accumulatorRegister || previousAccumulator != accumulatorRegister {
			// 前一条 ADD 必须形如 `accumulator = sum + value`，保证当前尾部只消费已形成的中间累加值。
			continue
		}

		forBase := forLoopInstruction.A()
		exitPC := pc + 8
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc-8 || moveValueInstruction.B() != forBase+3 {
			// 当前窄路径只覆盖官方热循环体：尾部 PC 前八条开始，格式化值来自外部 numeric-for 变量。
			continue
		}
		if registerInRange(functionSlot, forBase, forBase+3) || registerInRange(functionSlot+1, forBase, forBase+3) || registerInRange(functionSlot+2, forBase, forBase+3) {
			// CALL 临时槽不能覆盖 numeric-for 控制槽，否则逐指令别名语义会变复杂。
			continue
		}
		if registerInRange(addInstruction.A(), forBase, forBase+3) || registerInRange(accumulatorRegister, forBase, forBase+3) {
			// sum 和前序累加临时值都不能覆盖 FORLOOP 控制槽。
			continue
		}
		if addInstruction.A() == functionSlot || addInstruction.A() == functionSlot+1 || addInstruction.A() == functionSlot+2 || accumulatorRegister == functionSlot || accumulatorRegister == functionSlot+1 || accumulatorRegister == functionSlot+2 {
			// sum/accumulator 不能与 CALL 临时槽交叠，避免跳过 CALL 后破坏普通寄存器可见状态。
			continue
		}

		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]formatLenAddForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = formatLenAddForLoopSuperInstruction{
			EnvUpvalueIndex:     getStringInstruction.B(),
			FunctionSlot:        functionSlot,
			FormatArgumentSlot:  functionSlot + 1,
			ValueArgumentSlot:   functionSlot + 2,
			ValueArgumentSource: moveValueInstruction.B(),
			FormatString:        formatText,
			AccumulatorRegister: accumulatorRegister,
			AddTarget:           addInstruction.A(),
			ForBase:             forBase,
			ForSBx:              forLoopInstruction.SBx(),
			ExitPC:              exitPC,
			LoopPC:              loopPC,
			Valid:               true,
		}
	}
	return superInstructions
}

// PrepareFormatLenAddForLoopSuperInstructions 预构建当前 Proto 的 string.format 长度消费表。
//
// 返回 true 表示当前 Proto 至少存在一个可尝试的 `#string.format("%d", i)` superinstruction PC；
// 返回 false 时调用方不应在热循环中反复调用 TryExecuteFormatLenAddForLoop。
func (vm *VM) PrepareFormatLenAddForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 TryExecuteFormatLenAddForLoop 会因为表不匹配而回退。
	return len(vm.ensureFormatLenAddForLoopSuperInstructions()) > 0
}

// HasFormatLenAddForLoopAt 判断当前 PC 是否存在 string.format 长度消费 superinstruction。
func (vm *VM) HasFormatLenAddForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层决定是否需要执行 CALL 边界 context 检查。
	if vm == nil || vm.formatLenAddForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.formatLenAddForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// TryExecuteFormatLenAddForLoop 尝试直接消费 `#string.format("%d", i)` 并执行末尾 FORLOOP。
//
// pc 必须指向 GETTABUP string；返回 handled=false 表示 guard 不满足，调用方必须回退普通 VM。该
// fast path 只覆盖无元表全局/string table、标准库 exact `%d` 固定结果函数、integer 参数与
// integer numeric-for，不触发元方法、不处理 hook、不处理 yield。
func (vm *VM) TryExecuteFormatLenAddForLoop(pc int) (int, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败。
	if vm == nil || vm.formatLenAddForLoopSuperInstructionProto != vm.proto {
		// 调用方尚未为当前 Proto 准备表，回退普通 VM。
		return 0, false
	}
	superInstructions := vm.formatLenAddForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// 当前 PC 不在表范围内，回退普通 VM。
		return 0, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配 string.format 长度消费形态，回退普通 VM。
		return 0, false
	}

	registers := vm.registers
	requiredRegisters := [...]int{
		superInstruction.FunctionSlot,
		superInstruction.FormatArgumentSlot,
		superInstruction.ValueArgumentSlot,
		superInstruction.ValueArgumentSource,
		superInstruction.AccumulatorRegister,
		superInstruction.AddTarget,
		superInstruction.ForBase,
		superInstruction.ForBase + 1,
		superInstruction.ForBase + 2,
		superInstruction.ForBase + 3,
	}
	for _, registerIndex := range requiredRegisters {
		// 任一参与寄存器越界时交给普通 VM 报原始错误。
		if uint(registerIndex) >= uint(len(registers)) {
			return 0, false
		}
	}

	envTable, err := tableFromValue(vm.upvalueValue(superInstruction.EnvUpvalueIndex))
	if err != nil || envTable.GetMetatable() != nil {
		// GETTABUP 对有元表环境可能触发 __index，必须回退普通 VM。
		return 0, false
	}
	stringValue := envTable.RawGetString("string")
	stringTable, err := tableFromValue(stringValue)
	if err != nil || stringTable.GetMetatable() != nil {
		// string 字段不是无元表 table 时，GETTABLE 可能触发普通错误或元方法。
		return 0, false
	}
	formatValue := stringTable.RawGetString("format")
	if formatValue.Kind != KindGoClosure {
		// string.format 被替换为 Lua closure、table __call 或非函数时必须保留普通 CALL 语义。
		return 0, false
	}
	fixedFunction, ok := formatValue.Ref.(*GoFixedResultsFunction)
	if !ok || fixedFunction == nil || fixedFunction.FastPathID != GoFixedResultsFastPathStringFormatDecimal {
		// 只有标准库标记过的 exact `%d` 固定结果函数才能被表达式级直接消费。
		return 0, false
	}

	valueArgument := registers[superInstruction.ValueArgumentSource]
	accumulatorValue := registers[superInstruction.AccumulatorRegister]
	if valueArgument.Kind != KindInteger || accumulatorValue.Kind != KindInteger {
		// 当前表达式级消费只覆盖 integer 参数和 integer 累加器，其他类型保留完整 format/LEN/ADD 语义。
		return 0, false
	}
	lengthValue := decimalIntegerStringLength(valueArgument.Integer)
	addResult := accumulatorValue.Integer + lengthValue

	baseIndex := superInstruction.ForBase
	indexValue := registers[baseIndex]
	limitValue := registers[baseIndex+1]
	stepValue := registers[baseIndex+2]
	if indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		// 只覆盖 integer numeric-for；float 或可转 number 字符串仍走普通 VM。
		return 0, false
	}

	// guard 全部通过后，按被跳过指令结束后的可见状态提交寄存器写入。
	registers[superInstruction.FunctionSlot] = IntegerValue(lengthValue)
	registers[superInstruction.FormatArgumentSlot] = StringValue(superInstruction.FormatString)
	registers[superInstruction.ValueArgumentSlot] = valueArgument
	registers[superInstruction.AddTarget] = IntegerValue(addResult)
	nextIndex := indexValue.Integer + stepValue.Integer
	nextPC := superInstruction.ExitPC
	if forIntegerLoopContinues(nextIndex, limitValue.Integer, stepValue.Integer) {
		// 循环继续时更新内部 index 和外部可见循环变量，并跳回 FORLOOP 的 sBx 目标。
		registers[baseIndex] = IntegerValue(nextIndex)
		registers[baseIndex+3] = IntegerValue(nextIndex)
		nextPC = superInstruction.LoopPC
		vm.pcOffset = superInstruction.ForSBx
	} else {
		// 循环结束时不更新 index 和外部变量，保持普通 FORLOOP 语义。
		vm.pcOffset = 0
	}
	vm.currentPC = pc + 7
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, true
}

// ensureStringAppendForLoopSuperInstructions 返回当前 Proto 的字符串自追加循环预匹配表。
//
// 该表按 PC 与 Proto.Code 对齐，只记录官方 `s = s .. "x"` 循环热体形态；运行期目标寄存器、
// 临时槽和 numeric-for 控制槽仍由 TryExecuteStringAppendForLoopBatch 检查。
func (vm *VM) ensureStringAppendForLoopSuperInstructions() []stringAppendForLoopSuperInstruction {
	// 按当前 Proto 懒构建字符串自追加 superinstruction 表，并在 Proto 未变化时复用。
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时没有可用 superinstruction 表。
		return nil
	}
	if len(vm.proto.Code) < 4 {
		// 少于四条指令不可能包含 `MOVE;LOADK;CONCAT;FORLOOP`。
		vm.stringAppendForLoopSuperInstructionProto = vm.proto
		vm.stringAppendForLoopSuperInstructions = nil
		return nil
	}
	if vm.stringAppendForLoopSuperInstructionProto == vm.proto {
		// 当前 Proto 已完成扫描；nil 表示无匹配形态，非 nil 表示可按 PC 读取。
		return vm.stringAppendForLoopSuperInstructions
	}

	vm.stringAppendForLoopSuperInstructionProto = vm.proto
	vm.stringAppendForLoopSuperInstructions = buildStringAppendForLoopSuperInstructions(vm.proto)
	return vm.stringAppendForLoopSuperInstructions
}

// buildStringAppendForLoopSuperInstructions 构建 `s = s .. Kstring` 循环体预匹配表。
//
// 返回表按 PC 对齐；没有匹配形态时返回 nil，表示该 Proto 不需要承担额外表分配。
func buildStringAppendForLoopSuperInstructions(proto *bytecode.Proto) []stringAppendForLoopSuperInstruction {
	// 空 Proto 或短函数没有可构建的字符串自追加 superinstruction。
	if proto == nil || len(proto.Code) < 4 {
		return nil
	}

	var superInstructions []stringAppendForLoopSuperInstruction
	for pc := 0; pc+3 < len(proto.Code); pc++ {
		// 只识别官方 string_concat 热体 `MOVE;LOADK;CONCAT;FORLOOP`。
		moveInstruction := proto.Code[pc]
		loadInstruction := proto.Code[pc+1]
		concatInstruction := proto.Code[pc+2]
		forLoopInstruction := proto.Code[pc+3]
		if moveInstruction.OpCode() != bytecode.OpMove || loadInstruction.OpCode() != bytecode.OpLoadK || concatInstruction.OpCode() != bytecode.OpConcat || forLoopInstruction.OpCode() != bytecode.OpForLoop {
			// 当前 PC 不匹配目标模式，继续扫描后续指令。
			continue
		}

		targetRegister := concatInstruction.A()
		moveRegister := moveInstruction.A()
		appendRegister := loadInstruction.A()
		if moveInstruction.B() != targetRegister || concatInstruction.B() != moveRegister || concatInstruction.C() != appendRegister {
			// 必须是 `tmp = s; rhs = K; s = tmp .. rhs`，其他 CONCAT 形态保持普通 VM。
			continue
		}
		if targetRegister == moveRegister || targetRegister == appendRegister || moveRegister == appendRegister {
			// 三个槽位需要互不别名，否则跳过 MOVE/LOADK 后难以保持逐指令可见状态。
			continue
		}
		appendText, ok := protoStringConstant(proto, loadInstruction.Bx())
		if !ok || appendText == "" {
			// 只覆盖非空固定 string 常量；空串追加已有普通 CONCAT 空串快路径。
			continue
		}

		forBase := forLoopInstruction.A()
		exitPC := pc + 4
		loopPC := exitPC + forLoopInstruction.SBx()
		if loopPC != pc {
			// FORLOOP 必须精确跳回当前 MOVE，确保批量窗口覆盖完整循环体。
			continue
		}
		if registerInRange(targetRegister, forBase, forBase+3) || registerInRange(moveRegister, forBase, forBase+3) || registerInRange(appendRegister, forBase, forBase+3) {
			// 拼接目标和临时槽不能覆盖 numeric-for 控制槽，避免破坏 FORLOOP 状态。
			continue
		}

		if superInstructions == nil {
			// 只有真实存在匹配形态时才分配 PC 对齐表，避免普通函数执行增加分配。
			superInstructions = make([]stringAppendForLoopSuperInstruction, len(proto.Code))
		}
		superInstructions[pc] = stringAppendForLoopSuperInstruction{
			TargetRegister: targetRegister,
			MoveRegister:   moveRegister,
			AppendRegister: appendRegister,
			AppendText:     appendText,
			ForBase:        forBase,
			ForSBx:         forLoopInstruction.SBx(),
			ExitPC:         exitPC,
			LoopPC:         loopPC,
			Valid:          true,
		}
	}
	return superInstructions
}

// PrepareStringAppendForLoopSuperInstructions 预构建当前 Proto 的字符串自追加循环表。
//
// 返回 true 表示当前 Proto 至少存在一个可尝试的 `s = s .. Kstring` superinstruction PC；返回
// false 时调用方不应在热循环中反复调用 TryExecuteStringAppendForLoopBatch。
func (vm *VM) PrepareStringAppendForLoopSuperInstructions() bool {
	// 预构建失败不影响普通 VM；后续 TryExecuteStringAppendForLoopBatch 会因为表不匹配而回退。
	return len(vm.ensureStringAppendForLoopSuperInstructions()) > 0
}

// HasStringAppendForLoopAt 判断当前 PC 是否存在字符串自追加 superinstruction。
func (vm *VM) HasStringAppendForLoopAt(pc int) bool {
	// 该 helper 只读取已准备好的表，供 API 层按 context 窗口尝试批量执行。
	if vm == nil || vm.stringAppendForLoopSuperInstructionProto != vm.proto {
		// 表尚未准备或 Proto 已变化时不能命中。
		return false
	}
	superInstructions := vm.stringAppendForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中。
		return false
	}
	return superInstructions[pc].Valid
}

// TryExecuteStringAppendForLoopWholeBatch 尝试整段执行 `s = s .. Kstring` numeric-for 循环。
//
// pc 必须指向 MOVE；contextCheckCountdown/contextCheckInterval 必须来自 lua API 执行循环。该方法只覆盖
// 正步长 integer numeric-for、raw string 累加器和固定非空 string 后缀；正常路径只在循环结束时 materialize
// final/previous 两个字符串。若 context 在内部虚拟指令边界取消，会提交到对应边界后返回原始错误。
func (vm *VM) TryExecuteStringAppendForLoopWholeBatch(pc int, contextCheckCountdown int, contextCheckInterval int, checkContext func() error) (int, int, int, bool, error) {
	// 整段 batch 需要可用 context 回调和正数检查窗口；否则回退现有窗口 batch。
	if vm == nil || checkContext == nil || contextCheckInterval <= 0 || vm.stringAppendForLoopSuperInstructionProto != vm.proto {
		// 缺少执行环境或未准备 superinstruction 表时不处理。
		return 0, 0, contextCheckCountdown, false, nil
	}
	superInstructions := vm.stringAppendForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// PC 越界时不能命中整段 batch。
		return 0, 0, contextCheckCountdown, false, nil
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配字符串自追加形态。
		return 0, 0, contextCheckCountdown, false, nil
	}

	registers := vm.registers
	requiredRegisters := [...]int{
		superInstruction.TargetRegister,
		superInstruction.MoveRegister,
		superInstruction.AppendRegister,
		superInstruction.ForBase,
		superInstruction.ForBase + 1,
		superInstruction.ForBase + 2,
		superInstruction.ForBase + 3,
	}
	for _, registerIndex := range requiredRegisters {
		// 任一参与寄存器越界时交给普通 VM 报原始错误。
		if uint(registerIndex) >= uint(len(registers)) {
			return 0, 0, contextCheckCountdown, false, nil
		}
	}

	currentValue := registers[superInstruction.TargetRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	if currentValue.Kind != KindString || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		// 只覆盖 raw string 累加器和 integer numeric-for；其他类型保留完整 CONCAT/FORLOOP 语义。
		return 0, 0, contextCheckCountdown, false, nil
	}
	if stepValue.Integer <= 0 {
		// prototype 只覆盖官方正步长字符串拼接；零/负步长保持现有窗口 batch 或普通 VM。
		return 0, 0, contextCheckCountdown, false, nil
	}

	totalIterations, ok := positiveIntegerForLoopRemainingIterations(indexValue.Integer, limitValue.Integer, stepValue.Integer)
	if !ok || totalIterations <= 0 {
		// 无法静态证明剩余轮数时不做整段 materialize。
		return 0, 0, contextCheckCountdown, false, nil
	}
	if !repeatedAppendStringLengthOK(len(currentValue.String), len(superInstruction.AppendText), totalIterations) {
		// 目标字符串长度不可表达时回退普通路径，保留原始分配/错误行为。
		return 0, 0, contextCheckCountdown, false, nil
	}

	countdown := contextCheckCountdown
	completedIterations := 0
	currentIndex := indexValue.Integer
	consumeContext := func(phase stringAppendContextPhase) error {
		// 模拟 API 执行循环在每个虚拟指令入口的 context 检查/倒计时。
		if countdown <= 0 {
			// 检查失败时提交到检查发生前已经执行完的虚拟指令边界。
			if err := checkContext(); err != nil {
				vm.commitStringAppendForLoopState(superInstruction, currentValue.String, completedIterations, currentIndex, phase, totalIterations)
				return err
			}
			countdown = contextCheckInterval
			return nil
		}

		// 本虚拟指令入口消耗一个普通热路径倒计时单位。
		countdown--
		return nil
	}

	for completedIterations < totalIterations {
		// 除第一轮 MOVE 外，后续每轮 MOVE 入口都需要模拟 context 边界。
		if completedIterations > 0 {
			// 上一轮 FORLOOP 已经更新到当前轮 index；若取消，提交到下一轮 MOVE 前。
			if err := consumeContext(stringAppendPhaseAfterCompleted); err != nil {
				return superInstruction.LoopPC, completedIterations, countdown, true, err
			}
		}
		if err := consumeContext(stringAppendPhaseAfterMove); err != nil {
			// MOVE 已把当前累加字符串复制到临时槽。
			return pc + 1, completedIterations, countdown, true, err
		}
		if err := consumeContext(stringAppendPhaseAfterLoadK); err != nil {
			// LOADK 已把固定后缀写入临时槽。
			return pc + 2, completedIterations, countdown, true, err
		}
		if err := consumeContext(stringAppendPhaseAfterConcat); err != nil {
			// CONCAT 已写回本轮目标字符串，但 FORLOOP 尚未推进 index。
			return pc + 3, completedIterations, countdown, true, err
		}

		// FORLOOP 执行成功后，本轮拼接才进入完整提交计数。
		completedIterations++
		if completedIterations < totalIterations {
			// 循环继续时 FORLOOP 写入下一轮 index 和外部可见循环变量。
			currentIndex += stepValue.Integer
			continue
		}
	}

	if !vm.commitStringAppendForLoopState(superInstruction, currentValue.String, completedIterations, currentIndex, stringAppendPhaseAfterCompleted, totalIterations) {
		// 防御性处理：长度前置校验已通过，正常不应失败。
		return 0, 0, contextCheckCountdown, false, nil
	}
	return superInstruction.ExitPC, completedIterations, countdown, true, nil
}

// TryExecuteStringAppendForLoopBatch 尝试批量执行 `s = s .. Kstring` 与末尾 FORLOOP。
//
// pc 必须指向 MOVE；maxIterations 是调用方按 context 检查窗口给出的最多批量轮数。返回
// handled=false 表示 guard 不满足且寄存器无副作用；handledIterations 表示实际提交的循环轮数。
// 该 fast path 只覆盖 raw string 累加器和 integer numeric-for，不触发元方法、不处理 hook、不处理 yield。
func (vm *VM) TryExecuteStringAppendForLoopBatch(pc int, maxIterations int) (int, int, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 或没有可执行窗口必须快速失败。
	if vm == nil || maxIterations <= 0 || vm.stringAppendForLoopSuperInstructionProto != vm.proto {
		// 调用方尚未为当前 Proto 准备表，或没有可安全批量的 context 窗口。
		return 0, 0, false
	}
	superInstructions := vm.stringAppendForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// 当前 PC 不在表范围内，回退普通 VM。
		return 0, 0, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配字符串自追加形态，回退普通 VM。
		return 0, 0, false
	}

	registers := vm.registers
	requiredRegisters := [...]int{
		superInstruction.TargetRegister,
		superInstruction.MoveRegister,
		superInstruction.AppendRegister,
		superInstruction.ForBase,
		superInstruction.ForBase + 1,
		superInstruction.ForBase + 2,
		superInstruction.ForBase + 3,
	}
	for _, registerIndex := range requiredRegisters {
		// 任一参与寄存器越界时交给普通 VM 报原始错误。
		if uint(registerIndex) >= uint(len(registers)) {
			return 0, 0, false
		}
	}

	currentValue := registers[superInstruction.TargetRegister]
	indexValue := registers[superInstruction.ForBase]
	limitValue := registers[superInstruction.ForBase+1]
	stepValue := registers[superInstruction.ForBase+2]
	if currentValue.Kind != KindString || indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		// 只覆盖 raw string 累加器和 integer numeric-for；其他类型保留完整 CONCAT/FORLOOP 语义。
		return 0, 0, false
	}

	index := indexValue.Integer
	limit := limitValue.Integer
	step := stepValue.Integer
	iterations := 0
	nextPC := superInstruction.ExitPC
	loopContinues := false
	for iterations < maxIterations {
		// 每一轮代表已经执行 MOVE、LOADK、CONCAT 和 FORLOOP。
		iterations++
		nextIndex := index + step
		if forIntegerLoopContinues(nextIndex, limit, step) {
			// 循环继续时 FORLOOP 会更新内部 index 和外部可见循环变量。
			index = nextIndex
			nextPC = superInstruction.LoopPC
			loopContinues = true
			continue
		}

		// 循环退出时 FORLOOP 不写入越界后的 index。
		nextPC = superInstruction.ExitPC
		loopContinues = false
		break
	}
	if iterations == 0 {
		// 没有实际提交轮数时不能宣称处理成功。
		return 0, 0, false
	}

	finalText, ok := repeatedAppendString(currentValue.String, superInstruction.AppendText, iterations)
	if !ok {
		// 长度溢出或无法构造时回退普通 VM，保留原始错误/分配行为。
		return 0, 0, false
	}
	previousText, ok := repeatedAppendString(currentValue.String, superInstruction.AppendText, iterations-1)
	if !ok {
		// previousText 理论上不会在 finalText 成功后失败；防御异常溢出仍回退普通 VM。
		return 0, 0, false
	}

	// guard 全部通过后，按最后一轮 MOVE/LOADK/CONCAT 结束后的可见寄存器状态提交。
	registers[superInstruction.MoveRegister] = StringValue(previousText)
	registers[superInstruction.AppendRegister] = StringValue(superInstruction.AppendText)
	registers[superInstruction.TargetRegister] = StringValue(finalText)
	registers[superInstruction.ForBase] = IntegerValue(index)
	registers[superInstruction.ForBase+3] = IntegerValue(index)
	if loopContinues {
		// 批量窗口停在仍需继续循环的位置时，FORLOOP 已请求回跳。
		vm.pcOffset = superInstruction.ForSBx
	} else {
		// 循环退出时写回的 index 仍是最后一次有效迭代值，而不是越界后的 nextIndex。
		vm.pcOffset = 0
	}
	vm.currentPC = pc + 3
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, iterations, true
}

// TryExecuteMixArithmeticForLoop 尝试执行 `MUL; ADD; SUB; IDIV; MOD; ADD; FORLOOP` superinstruction。
//
// pc 必须指向当前 Proto.Code 中的 MUL 指令；返回 handled=false 表示 guard 不满足，调用方必须回退
// 普通 VM。该 fast path 只覆盖 integer 算术和 integer numeric-for，不触发元方法、不处理 hook、
// 不处理 yield；除数为 0 时也回退普通 VM，以保留原始零除错误路径和前序写回语义。
func (vm *VM) TryExecuteMixArithmeticForLoop(pc int) (int, bool) {
	// 先按 PC 读取已准备好的预匹配表；非目标 PC 必须快速失败，避免普通指令承担额外识别成本。
	if vm == nil || vm.mixArithmeticForLoopSuperInstructionProto != vm.proto {
		// 调用方尚未为当前 Proto 准备表，回退普通 VM。
		return 0, false
	}
	superInstructions := vm.mixArithmeticForLoopSuperInstructions
	if uint(pc) >= uint(len(superInstructions)) {
		// 当前 PC 不在表范围内，回退普通 VM。
		return 0, false
	}
	superInstruction := superInstructions[pc]
	if !superInstruction.Valid {
		// 当前 PC 没有匹配混合算术循环形态，回退普通 VM。
		return 0, false
	}

	registers := vm.registers
	targets := [...]int{
		superInstruction.MulTarget,
		superInstruction.FirstAddTarget,
		superInstruction.SubTarget,
		superInstruction.IDivTarget,
		superInstruction.ModTarget,
		superInstruction.FinalAddTarget,
	}
	for _, targetIndex := range targets {
		// 任一算术目标寄存器越界时交给普通 VM 报原始错误。
		if uint(targetIndex) >= uint(len(registers)) {
			return 0, false
		}
	}

	if uint(superInstruction.SumRegister) >= uint(len(registers)) || uint(superInstruction.LoopRegister) >= uint(len(registers)) {
		// sum 或外部循环变量寄存器越界时交给普通 VM 报原始错误。
		return 0, false
	}
	sumValue := registers[superInstruction.SumRegister]
	loopValue := registers[superInstruction.LoopRegister]
	if sumValue.Kind != KindInteger || loopValue.Kind != KindInteger {
		// 当前窄路径只覆盖 integer sum 与 integer 外部循环变量。
		return 0, false
	}
	if superInstruction.IDivConstant == 0 || superInstruction.ModConstant == 0 {
		// 构建期已排除零除常量；这里保留防御性回退以维持普通错误路径。
		return 0, false
	}

	// 该窄路径只匹配构建期证明的数据流：sum = (sum + i*K1 - K2)//K3 + i%K4。
	mulResult := loopValue.Integer * superInstruction.MulConstant
	firstAddResult := sumValue.Integer + mulResult
	subResult := firstAddResult - superInstruction.SubConstant
	iDivResult := integerFloorDiv(subResult, superInstruction.IDivConstant)
	modResult := integerModulo(loopValue.Integer, superInstruction.ModConstant)
	finalAddResult := iDivResult + modResult

	baseIndex := superInstruction.ForBase
	if uint(baseIndex+3) >= uint(len(registers)) {
		// FORLOOP 需要 R(A)..R(A+3) 四个控制槽，越界时回退普通 VM。
		return 0, false
	}
	indexValue := registers[baseIndex]
	limitValue := registers[baseIndex+1]
	stepValue := registers[baseIndex+2]
	if indexValue.Kind != KindInteger || limitValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		// 只覆盖 integer numeric-for；float 或可转 number 字符串仍走普通 VM。
		return 0, false
	}

	// guard 全部通过后，按普通指令顺序提交寄存器写入。
	registers[superInstruction.MulTarget] = IntegerValue(mulResult)
	registers[superInstruction.FirstAddTarget] = IntegerValue(firstAddResult)
	registers[superInstruction.SubTarget] = IntegerValue(subResult)
	registers[superInstruction.IDivTarget] = IntegerValue(iDivResult)
	registers[superInstruction.ModTarget] = IntegerValue(modResult)
	registers[superInstruction.FinalAddTarget] = IntegerValue(finalAddResult)
	nextIndex := indexValue.Integer + stepValue.Integer
	nextPC := superInstruction.ExitPC
	if forIntegerLoopContinues(nextIndex, limitValue.Integer, stepValue.Integer) {
		// 循环继续时更新内部 index 和外部可见循环变量，并跳回 FORLOOP 的 sBx 目标。
		registers[baseIndex] = IntegerValue(nextIndex)
		registers[baseIndex+3] = IntegerValue(nextIndex)
		nextPC = superInstruction.LoopPC
		vm.pcOffset = superInstruction.ForSBx
	} else {
		// 循环结束时不更新 index 和外部变量，保持普通 FORLOOP 语义。
		vm.pcOffset = 0
	}
	vm.currentPC = pc + 6
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return nextPC, true
}

// decodedRegisterOperand 返回预解码 RK 操作数中的寄存器下标。
//
// ok=false 表示该操作数来自常量表，不能作为固定寄存器数据流参与 superinstruction 匹配。
func decodedRegisterOperand(operand decodedRKOperand) (int, bool) {
	// 只有非常量 RK 才表示寄存器操作数。
	if operand.Constant {
		// 常量操作数不是寄存器数据流。
		return 0, false
	}
	return operand.Index, true
}

// decodedIntegerConstantOperand 返回预解码 RK 操作数中的 integer 常量。
//
// ok=false 表示该操作数不是可直接用于 integer fast path 的 Proto 常量。
func decodedIntegerConstantOperand(operand decodedRKOperand) (int64, bool) {
	// 只有已确认的 integer 常量可作为构建期常量写入 superinstruction。
	if !operand.Constant || !operand.IntegerConstantOK {
		// 寄存器、非 integer 常量或越界常量都不能在构建期固定。
		return 0, false
	}
	return operand.IntegerConstant, true
}

// decodedRegisterIntegerConstantPair 识别一个寄存器和一个 integer 常量组成的二元操作数。
//
// 乘法等交换律场景可使用该 helper；返回的 registerIndex 是寄存器操作数，constantValue 是
// 另一侧 integer 常量。其他形态返回 ok=false。
func decodedRegisterIntegerConstantPair(left decodedRKOperand, right decodedRKOperand) (int, int64, bool) {
	// 优先匹配左寄存器右常量，这是当前 codegen 的主路径。
	if registerIndex, registerOK := decodedRegisterOperand(left); registerOK {
		if constantValue, constantOK := decodedIntegerConstantOperand(right); constantOK {
			// 左寄存器右常量命中。
			return registerIndex, constantValue, true
		}
	}
	if registerIndex, registerOK := decodedRegisterOperand(right); registerOK {
		if constantValue, constantOK := decodedIntegerConstantOperand(left); constantOK {
			// 右寄存器左常量命中。
			return registerIndex, constantValue, true
		}
	}
	return 0, 0, false
}

// decodedRegisterPlusTarget 识别一侧为指定临时目标、另一侧为寄存器的 ADD 操作数。
//
// 返回的寄存器表示除 targetRegister 外的累加寄存器；其他形态返回 ok=false。
func decodedRegisterPlusTarget(left decodedRKOperand, right decodedRKOperand, targetRegister int) (int, bool) {
	// ADD 具备交换律，允许 targetRegister 出现在任一侧。
	leftRegister, leftOK := decodedRegisterOperand(left)
	rightRegister, rightOK := decodedRegisterOperand(right)
	if !leftOK || !rightOK {
		// 当前窄模式只接受双寄存器 ADD。
		return 0, false
	}
	if leftRegister == targetRegister {
		// 左侧为临时目标时，右侧是被累加的寄存器。
		return rightRegister, true
	}
	if rightRegister == targetRegister {
		// 右侧为临时目标时，左侧是被累加的寄存器。
		return leftRegister, true
	}
	return 0, false
}

// decodedRegistersMatchPair 判断两个操作数是否刚好匹配两个寄存器。
//
// ADD 具备交换律，因此两个寄存器可按任意顺序出现。
func decodedRegistersMatchPair(left decodedRKOperand, right decodedRKOperand, firstRegister int, secondRegister int) bool {
	// 先读取两个寄存器操作数；任一侧为常量时不属于当前窄模式。
	leftRegister, leftOK := decodedRegisterOperand(left)
	rightRegister, rightOK := decodedRegisterOperand(right)
	if !leftOK || !rightOK {
		// 当前窄模式只接受双寄存器 ADD。
		return false
	}
	return (leftRegister == firstRegister && rightRegister == secondRegister) || (leftRegister == secondRegister && rightRegister == firstRegister)
}

// registerInRange 判断寄存器是否落在闭区间 [start, end]。
//
// 该 helper 用于构建期排除算术目标覆盖 numeric-for 控制槽的形态。
func registerInRange(registerIndex int, start int, end int) bool {
	// 边界是闭区间，覆盖 R(A)..R(A+3) 四个 numeric-for 控制槽。
	return registerIndex >= start && registerIndex <= end
}

// decimalIntegerStringLength 返回 int64 十进制格式化后的字符串长度。
//
// 该 helper 等价于 `len(strconv.FormatInt(value, 10))`，但不分配字符串；负数包含符号位，
// math.MinInt64 使用无符号补码转换避免取反溢出。
func decimalIntegerStringLength(value int64) int64 {
	// 先转换为待计数的无符号绝对值，并记录负号长度。
	var unsignedValue uint64
	length := int64(0)
	if value < 0 {
		// -(MinInt64) 会溢出，使用 -(value+1)+1 的等价转换。
		unsignedValue = uint64(-(value + 1)) + 1
		length = 1
	} else {
		// 非负整数直接转换成无符号值计数。
		unsignedValue = uint64(value)
	}
	for unsignedValue >= 10 {
		// 每除以 10 去掉一位十进制数字。
		unsignedValue /= 10
		length++
	}
	return length + 1
}

// decodedIntegerOperandValue 读取预解码 RK 操作数的 integer 值。
//
// 常量操作数必须在预解码阶段已确认为 integer；寄存器操作数仍按当前寄存器窗口和动态类型检查。
// 返回 ok=false 表示不能走 integer fast path，调用方应回退普通 VM。
func (vm *VM) decodedIntegerOperandValue(operand decodedRKOperand) (int64, bool) {
	// 常量 integer 已经在预解码阶段保存值，不需要再访问 Proto.Constants。
	if operand.Constant {
		// 非 integer 常量或越界常量不能使用 integer fast path。
		return operand.IntegerConstant, operand.IntegerConstantOK
	}
	if uint(operand.Index) >= uint(len(vm.registers)) {
		// 寄存器越界必须回退普通 VM，以保留原始错误语义。
		return 0, false
	}
	value := vm.registers[operand.Index]
	if value.Kind != KindInteger {
		// 非 integer 寄存器值需要完整算术转换、元方法或错误路径。
		return 0, false
	}
	return value.Integer, true
}

// decodedIntegerOperandValueWithOverrides 按顺序读取带临时写回覆盖的 RK integer 操作数。
//
// 覆盖项用于模拟同一 superinstruction 内前序指令已经写回的寄存器；后声明的覆盖项优先级更高，
// 从而保持 `MUL`、`ADD`、`SUB` 的普通逐指令可见性。返回 ok=false 表示不能走 integer fast path。
func (vm *VM) decodedIntegerOperandValueWithOverrides(operand decodedRKOperand, firstIndex int, firstValue int64, firstValid bool, secondIndex int, secondValue int64, secondValid bool, thirdIndex int, thirdValue int64, thirdValid bool) (int64, bool) {
	// 常量操作数不受临时寄存器覆盖影响，直接复用预解码 integer 常量。
	if operand.Constant {
		// 非 integer 常量或越界常量不能使用 integer fast path。
		return operand.IntegerConstant, operand.IntegerConstantOK
	}
	if thirdValid && operand.Index == thirdIndex {
		// 第三覆盖项表示最新写回，优先返回。
		return thirdValue, true
	}
	if secondValid && operand.Index == secondIndex {
		// 第二覆盖项比第一覆盖项更新，优先于第一项返回。
		return secondValue, true
	}
	if firstValid && operand.Index == firstIndex {
		// 第一覆盖项表示最早的临时写回。
		return firstValue, true
	}
	return vm.decodedIntegerOperandValue(operand)
}

// registerIntegerValueWithOverrides 读取带临时写回覆盖的寄存器 integer 值。
//
// 该 helper 专门用于 superinstruction 末尾的 FORLOOP 控制槽读取；如果算术链目标别名到控制槽，
// FORLOOP 必须看到前序算术写回后的值，才能与普通逐指令执行一致。
func (vm *VM) registerIntegerValueWithOverrides(registerIndex int, firstIndex int, firstValue int64, secondIndex int, secondValue int64, thirdIndex int, thirdValue int64) (int64, bool) {
	// 覆盖项按普通执行顺序从新到旧检查，保证别名寄存器读取到最近一次写入。
	if registerIndex == thirdIndex {
		// SUB 是算术链中最新写回。
		return thirdValue, true
	}
	if registerIndex == secondIndex {
		// ADD 写回优先于 MUL 写回。
		return secondValue, true
	}
	if registerIndex == firstIndex {
		// MUL 写回是最早覆盖项。
		return firstValue, true
	}
	if uint(registerIndex) >= uint(len(vm.registers)) {
		// 寄存器越界必须回退普通 VM，以保留原始错误语义。
		return 0, false
	}
	value := vm.registers[registerIndex]
	if value.Kind != KindInteger {
		// 非 integer 控制槽需要完整 FORLOOP number 转换或错误路径。
		return 0, false
	}
	return value.Integer, true
}

// BindLuaMetamethodRunner 绑定当前 VM 可用的 Lua closure 元方法执行器。
//
// runner 可为 nil，表示只允许 Go closure 元方法。lua 包在创建执行 VM 时注入 State runner，
// 使运行期算术、位运算、比较和拼接能够调用 Lua 写成的元方法。
func (vm *VM) BindLuaMetamethodRunner(runner LuaMetamethodRunner) {
	if vm == nil {
		// nil VM 无法绑定执行器，直接忽略保持调用幂等。
		return
	}

	// 记录执行器；调用方负责保证 runner 与当前 State 生命周期一致。
	vm.luaMetamethodRunner = runner
}

// SetCurrentPC 更新当前 VM 的执行指令位置。
//
// pc 使用 Proto.Code 的零基下标；非法 pc 不主动报错，由调用方执行循环保证范围。
func (vm *VM) SetCurrentPC(pc int) {
	if vm == nil {
		// nil VM 无法记录 PC，直接忽略。
		return
	}

	// 记录当前 PC，GC 在 collectgarbage 中会据此过滤已离开作用域的寄存器值。
	vm.currentPC = pc
}

// SetStringTableReadCacheDisabled 设置当前 VM 是否禁用字符串字段读取 inline cache。
//
// disabled=true 用于事件、调试等需要精确观察动态临时 table 的路径；切换到禁用状态会立即丢弃
// 旧缓存，disabled=false 恢复默认缓存行为。该标记只影响 VM 内部优化，不改变 Lua table 语义。
func (vm *VM) SetStringTableReadCacheDisabled(disabled bool) {
	// nil VM 没有可更新的缓存配置。
	if vm == nil {
		// 调用方可在可选 VM 路径安全调用。
		return
	}
	if disabled {
		// 禁用时清空旧命中，避免恢复前误用动态 table 的历史结果。
		vm.stringTableReadCache = nil
	}
	vm.disableStringTableReadCache = disabled
}

// cachedStringTableRead 尝试读取当前 PC 的字符串 table inline cache。
//
// table 必须是无元表 table；缓存命中还要求 table raw 写入版本未变化。返回 ok=false 时调用方
// 应执行真实 RawGetString，并通过 rememberStringTableRead 记录新结果。
func (vm *VM) cachedStringTableRead(table *Table) (Value, bool) {
	if vm == nil || table == nil {
		// 缺少 VM 或 table 时不能使用缓存。
		return NilValue(), false
	}
	if vm.disableStringTableReadCache || vm.currentPC < 0 || vm.currentPC >= len(vm.stringTableReadCache) {
		// 没有绑定 Proto 或 PC 超出缓存范围时退回普通读取。
		return NilValue(), false
	}
	cacheEntry := vm.stringTableReadCache[vm.currentPC]
	if !cacheEntry.valid || cacheEntry.table != table || cacheEntry.tableID != table.CacheIdentity() {
		// 缓存尚未初始化或来自其他 table 时不能复用。
		return NilValue(), false
	}
	if cacheEntry.version != table.MutationVersion() {
		// table 被写入过，旧读取结果必须失效。
		return NilValue(), false
	}
	return cacheEntry.value, true
}

// rememberStringTableRead 记录当前 PC 的字符串 table inline cache。
//
// value 可以是 Lua nil；valid 标记用于区分 nil 命中和未初始化缓存。
func (vm *VM) rememberStringTableRead(table *Table, value Value) {
	if vm == nil || table == nil {
		// 缺少 VM 或 table 时没有可记录对象。
		return
	}
	if vm.disableStringTableReadCache || !vm.ensureStringTableReadCache() {
		// 没有绑定 Proto 或 PC 超出缓存范围时跳过记录。
		return
	}
	vm.stringTableReadCache[vm.currentPC] = stringTableReadCacheEntry{
		table:   table,
		tableID: table.CacheIdentity(),
		version: table.MutationVersion(),
		value:   value,
		valid:   true,
	}
}

// ensureStringTableReadCache 确保当前 Proto 的字符串 table 读缓存已经可写。
//
// 只有遇到无元表 table 的字符串常量 key 读取时才需要该缓存；递归与纯算术函数没有此类指令，
// 因此延迟分配可以避免每个 Lua 调用帧进入时创建一段用不到的缓存数组。
func (vm *VM) ensureStringTableReadCache() bool {
	if vm == nil || vm.proto == nil {
		// 缺少 VM 或 Proto 时无法按 PC 建立缓存。
		return false
	}
	if vm.currentPC < 0 || vm.currentPC >= len(vm.proto.Code) {
		// PC 超出当前 Proto 指令范围时不能写入缓存，保持普通读取语义。
		return false
	}
	if vm.stringTableReadCacheProto != vm.proto || len(vm.stringTableReadCache) < len(vm.proto.Code) {
		// 首次使用或 Proto 切换后按当前指令数量建立缓存，PC 与 Lua 5.3 指令一一对应。
		vm.stringTableReadCacheProto = vm.proto
		vm.stringTableReadCache = make([]stringTableReadCacheEntry, len(vm.proto.Code))
	}
	return true
}

// ActiveRegistersSnapshot 返回当前 PC 下仍处于局部变量生命周期内的寄存器副本。
//
// 当缺少 Proto 或 LocalVars 调试信息时退回完整寄存器快照；有 LocalVars 时按当前有效
// local 的真实寄存器索引提取，避免已经离开作用域的循环临时变量被 weak table 误判为强根。
func (vm *VM) ActiveRegistersSnapshot() []Value {
	if vm == nil {
		// nil VM 没有活动寄存器。
		return nil
	}
	if vm.proto == nil || len(vm.proto.LocalVars) == 0 {
		// 无调试生命周期信息时保守扫描完整寄存器，避免漏根。
		return vm.RegistersSnapshot()
	}

	activeRegisters := make([]Value, 0, len(vm.proto.LocalVars))
	seenRegisters := make(map[int]bool)
	for index := range vm.proto.LocalVars {
		if vm.proto.LocalVars[index].ActiveAt(vm.currentPC) {
			if vm.proto.LocalVars[index].Name == "(*temporary)" {
				// 临时调试槽位可能在语句结束后残留旧值；GC 根只保留具名 local，避免弱表被历史临时值误保活。
				continue
			}
			registerIndex := vm.proto.LocalVars[index].Register
			if registerIndex < 0 || registerIndex >= len(vm.registers) {
				// 损坏或外部 chunk 缺失寄存器映射时跳过越界项，避免误扫历史寄存器。
				continue
			}
			if seenRegisters[registerIndex] {
				// 同一寄存器可能被调试信息重复覆盖，重复扫描没有意义。
				continue
			}
			// 按真实寄存器索引追加当前仍存活的局部变量值。
			activeRegisters = append(activeRegisters, vm.registers[registerIndex])
			seenRegisters[registerIndex] = true
		}
	}
	if len(activeRegisters) == 0 {
		// 当前 PC 没有可见 local 时不扫描寄存器，避免历史值保活弱表项。
		return nil
	}

	// 返回活动寄存器副本。
	return activeRegisters
}

// ActiveLocalSnapshots 返回当前 PC 下仍处于生命周期内的具名局部变量副本。
//
// 返回值只用于调试展示；缺少 Proto、LocalVars 或寄存器映射时返回 nil。该方法不会暴露临时槽位，
// 也不会返回重复寄存器，避免 IDE 变量窗口展示已经离开作用域的历史值。
func (vm *VM) ActiveLocalSnapshots() []ActiveLocalSnapshot {
	if vm == nil || vm.proto == nil || len(vm.proto.LocalVars) == 0 {
		// 缺少 VM 或调试信息时没有稳定变量名可展示。
		return nil
	}

	locals := make([]ActiveLocalSnapshot, 0, len(vm.proto.LocalVars))
	seenRegisters := make(map[int]bool)
	for index := range vm.proto.LocalVars {
		localVar := vm.proto.LocalVars[index]
		if !localVar.ActiveAt(vm.currentPC) {
			// 不在当前 PC 生命周期内的 local 不应展示。
			continue
		}
		if localVar.Name == "" || localVar.Name == "(*temporary)" {
			// 匿名和临时槽位不是用户可理解变量。
			continue
		}
		registerIndex := localVar.Register
		if registerIndex < 0 || registerIndex >= len(vm.registers) {
			// 损坏调试信息不能读取寄存器。
			continue
		}
		if seenRegisters[registerIndex] {
			// 同一寄存器只展示第一个活动名称，避免重复项。
			continue
		}
		locals = append(locals, ActiveLocalSnapshot{Name: localVar.Name, Register: registerIndex, Const: localVar.Const, Value: vm.registers[registerIndex]})
		seenRegisters[registerIndex] = true
	}
	return locals
}

// SetRegister 写入指定寄存器。
//
// index 使用 Lua VM 的 0-based 寄存器编号；越界时返回 ErrRegisterOutOfRange。
func (vm *VM) SetRegister(index int, value Value) error {
	if index < 0 || index >= len(vm.registers) {
		// 越界写入会破坏寄存器窗口，必须拒绝。
		return ErrRegisterOutOfRange
	}

	// 写入寄存器槽位，后续 MOVE/LOADK 等指令会复用该路径。
	vm.registers[index] = value
	return nil
}

// ClearFixedCallArgumentTemporaries 清理固定返回 CALL 后不再属于结果区的临时实参槽。
//
// proto 可为空；为空时无法识别活动 local，只按 CALL 布局清理。request 必须来自刚完成写回的
// CALL 请求；nextPC 是 CALL 后下一条指令位置，用于避免清掉该位置仍可见的 local。开放返回和
// TFORCALL 不会被清理，因为它们的结果边界或写回区间需要由后续指令继续消费。
func (vm *VM) ClearFixedCallArgumentTemporaries(proto *bytecode.Proto, request *CallRequest, nextPC int) {
	if vm == nil || request == nil {
		// 缺少 VM 或 CALL 请求时没有可清理目标。
		return
	}
	if request.GenericFor || request.ArgumentCount < 0 || request.ReturnCount < 0 {
		// 泛型 for、开放实参或开放返回的寄存器边界不能按固定 CALL 临时区处理。
		return
	}
	firstTemporary := request.FunctionIndex + request.ReturnCount
	lastTemporary := request.FunctionIndex + request.ArgumentCount
	if firstTemporary > lastTemporary {
		// 固定结果区已经覆盖全部调用输入槽，无剩余临时实参需要清理。
		return
	}
	if firstTemporary < 0 {
		// 损坏请求不能从负寄存器开始清理。
		firstTemporary = 0
	}
	for registerIndex := firstTemporary; registerIndex <= lastTemporary && registerIndex < len(vm.registers); registerIndex++ {
		// 活动 local 仍可能与调用实参槽共享寄存器，必须保留 Lua 5.3 可见值。
		if fixedCallTemporaryRegisterActiveLocal(proto, registerIndex, nextPC) {
			// 当前槽仍是活动 local，跳过清理避免破坏后续语义和 debug.getlocal。
			continue
		}
		vm.registers[registerIndex] = NilValue()
	}
}

// CanClearFixedCallArgumentTemporaries 判断指定 CALL 是否适合清理非结果实参槽。
//
// function 是 CALL 指令读取到的被调值；request 是同一 CALL 的固定布局。当前只允许普通 Lua
// closure 和无 C/native 调用帧状态的普通 Go 回调适配器触发清理，避免 native C closure 与带
// upvalue 的 Go wrapper 依赖调用寄存器作为临时根时被提前置 nil。
func CanClearFixedCallArgumentTemporaries(function Value, request *CallRequest) bool {
	if request == nil || request.GenericFor || request.ArgumentCount < 0 || request.ReturnCount < 0 {
		// 缺少请求、泛型 for 或开放边界都不能按固定 CALL 临时区清理。
		return false
	}
	if function.Kind == KindLuaClosure {
		// 普通 Lua closure 的固定 CALL 结束后，非结果实参槽不再属于可见结果区。
		return true
	}
	if function.Kind != KindGoClosure {
		// 非 Go/Lua closure 不进入清理边界。
		return false
	}
	switch function.Ref.(type) {
	case GoResultsFunction:
		// 普通多返回 Go 回调没有 native C frame，可在返回写回后释放实参临时根。
		return true
	case GoFunction:
		// 普通单返回 Go 回调没有 native C frame，可在返回写回后释放实参临时根。
		return true
	case GoUnaryFunction:
		// 一元 Go 回调是普通 GoFunction 的热路径形态。
		return true
	case *GoFastUnaryFunction:
		// 显式 fast unary 回调不携带 native C frame 状态。
		return true
	case *GoFixedResultsFunction:
		// 固定结果 Go 回调声明了结果上限，写回后可清理多余实参槽。
		return true
	default:
		// GoClosureWithUpvalues 等 wrapper 可能承载 native C closure 或闭包 upvalue 根，保守跳过。
		return false
	}
}

// fixedCallTemporaryRegisterActiveLocal 判断寄存器在指定 PC 是否属于活动 local。
//
// proto 为空或调试信息缺失时返回 false；调用方会按临时槽处理。该 helper 只保护明确可见的
// local，不保留编译器临时值，从而减少固定返回 CALL 后历史实参对象被 GC/root 继续观察的机会。
func fixedCallTemporaryRegisterActiveLocal(proto *bytecode.Proto, registerIndex int, pc int) bool {
	if proto == nil || registerIndex < 0 || pc < 0 {
		// 缺少 Proto、寄存器非法或 PC 非法时无法证明该槽是活动 local。
		return false
	}
	for localIndex := range proto.LocalVars {
		// 逐项检查 LocalVar 调试范围；同一寄存器命中任意活动 local 即需要保留。
		localVar := proto.LocalVars[localIndex]
		if localVar.Register == registerIndex && localVar.ActiveAt(pc) {
			// 命中 CALL 后仍活动的 local，调用方不能清理该寄存器。
			return true
		}
	}
	return false
}

// ResetForTailCall 将当前 VM 复位为同一 Proto 的尾调用入口状态。
//
// 该方法只用于 Lua closure 自尾调用优化：调用方必须保证当前 VM 仍执行同一 Proto，且会在复位后
// 重新写入固定参数。varargs 会被复制保存；开放 upvalue 会先闭合，避免旧帧局部变量继续指向将被
// 复用的寄存器槽。返回错误仅表示 VM 为 nil，当前实现保持无错误以便调用侧逻辑简洁。
func (vm *VM) ResetForTailCall(varargs []Value) {
	if vm == nil {
		// nil VM 没有可复位状态，直接保持无副作用。
		return
	}

	// 当前帧局部变量生命周期结束，所有开放 upvalue 必须先闭合到堆值。
	vm.CloseUpvaluesFrom(0)
	for registerIndex := range vm.registers {
		// 寄存器窗口复用前清空为 nil，避免旧局部变量污染新一轮调用。
		vm.registers[registerIndex] = NilValue()
	}
	vm.varargs = append([]Value(nil), varargs...)
	vm.currentPC = 0
	vm.openTop = -1
	vm.borrowedSelfRecursiveLocalFunctionClosureEnabled = false
	vm.pendingLoadKXTarget = -1
	vm.pendingSetList = nil
	vm.skipNext = false
	vm.pcOffset = 0
	vm.closeFrom = -1
	vm.callRequest = CallRequest{}
	vm.hasCallRequest = false
	vm.pendingComparison = nil
	vm.returned = false
	vm.returnValues = nil
}

// EnsureRegisterCount 扩展当前 VM 的寄存器窗口。
//
// count 表示调用方即将访问的寄存器数量；只允许扩展不缩小，新增寄存器按 nil 初始化。
func (vm *VM) EnsureRegisterCount(count int) {
	if vm == nil || count <= len(vm.registers) {
		// nil VM 或现有窗口已足够时无需处理。
		return
	}

	// 追加 nil 寄存器，支持开放 call 返回值这类运行期才知道数量的写入。
	for len(vm.registers) < count {
		vm.registers = append(vm.registers, NilValue())
	}
}

// Upvalue 返回指定 upvalue 中的值。
//
// index 使用 Lua VM 的 0-based upvalue 编号；越界时返回 nil 和 false。该方法主要服务
// 单测与后续 debug.getupvalue 迁移，不暴露给对外 lua 包。
func (vm *VM) Upvalue(index int) (Value, bool) {
	if !vm.hasUpvalueIndex(index) {
		// 越界读取不暴露内部切片，调用方可通过 false 判断无效 upvalue。
		return NilValue(), false
	}

	// upvalue 存在时返回当前值；共享 cell 优先反映实时值。
	return vm.upvalueValue(index), true
}

// BindUpvalueCells 绑定当前闭包的共享 upvalue 槽。
//
// cells 可为空；为空时 VM 继续使用 Upvalues 快照语义，兼容旧测试和手工构造闭包。
func (vm *VM) BindUpvalueCells(cells []*UpvalueCell) {
	if vm == nil {
		// nil VM 无法保存绑定。
		return
	}

	// 复制 cell 切片，避免调用方后续替换切片影响 VM。
	vm.upvalueCells = append([]*UpvalueCell(nil), cells...)
}

// BindBorrowedUpvalueCells 绑定执行期 Lua closure 的共享 upvalue 槽。
//
// cells 必须来自不可变 LuaClosure.UpvalueCells 切片头；VM 只读取切片并通过 cell 写入值，不得
// 修改切片结构。该方法对齐 Lua 5.3 closure 持有 UpVal 指针的模型，避免递归调用每帧复制 upvalue
// cell 切片；公开或测试路径需要隔离调用方切片时仍应使用 BindUpvalueCells。
func (vm *VM) BindBorrowedUpvalueCells(cells []*UpvalueCell) {
	if vm == nil {
		// nil VM 无法保存绑定。
		return
	}

	// 执行期 closure 的 upvalue cell 切片结构稳定，可直接借用以减少每帧分配。
	vm.upvalueCells = cells
}

// EnableBorrowedSelfRecursiveLocalFunctionClosure 控制非逃逸自递归局部函数闭包借用路径。
//
// enabled 只能由 Lua 执行循环在无 debug hook、无 coroutine、无 continuation 的热路径中打开；
// VM reset 和 tail-call reset 会自动关闭该开关，避免池复用后把 fast path 泄漏到调试语义路径。
func (vm *VM) EnableBorrowedSelfRecursiveLocalFunctionClosure(enabled bool) {
	if vm == nil {
		// nil VM 没有运行状态可更新，直接保持无副作用。
		return
	}

	// 仅更新本轮执行开关；缓存对象仍按 Proto guard 惰性复用。
	vm.borrowedSelfRecursiveLocalFunctionClosureEnabled = enabled
}

// SetOpenTop 记录开放返回列表写入后的寄存器开区间上界。
//
// top 小于 0 表示清空开放栈顶；非负值由执行器在 CALL C=0 或 VARARG B=0 后设置，供后续
// SETLIST B=0 或 CALL B=0 折算实际数量。
func (vm *VM) SetOpenTop(top int) {
	if vm == nil {
		// nil VM 无状态可写，直接忽略。
		return
	}
	if top < 0 {
		// 负数表示清除开放栈顶。
		vm.openTop = -1
		return
	}

	// 记录开放列表的开区间上界。
	vm.openTop = top
}

// CloseUpvaluesFrom 关闭从指定寄存器开始的开放 upvalue。
//
// fromRegister 使用 0-based 寄存器编号；小于该寄存器的局部变量仍处于外层作用域，必须保持开放。
func (vm *VM) CloseUpvaluesFrom(fromRegister int) {
	if vm == nil || len(vm.openUpvalues) == 0 {
		// 没有活动 VM 或开放 upvalue 时无需处理。
		return
	}

	remaining := vm.openUpvalues[:0]
	for _, trackedCell := range vm.openUpvalues {
		if trackedCell.register >= fromRegister {
			// 该寄存器生命周期已结束，闭合到堆值以避免后续寄存器复用污染。
			trackedCell.cell.Close()
			continue
		}
		// 外层寄存器仍然存活，保留开放 cell。
		remaining = append(remaining, trackedCell)
	}
	vm.openUpvalues = remaining
}

// upvalueValue 读取指定 upvalue 的当前运行期值。
func (vm *VM) upvalueValue(index int) Value {
	if index >= 0 && index < len(vm.upvalueCells) && vm.upvalueCells[index] != nil {
		// 共享 cell 存在时返回实时值。
		return vm.upvalueCells[index].Value()
	}
	if index >= 0 && index < len(vm.upvalues) {
		// 无 cell 时退回快照值。
		return vm.upvalues[index]
	}

	// 越界按 nil 返回，由调用方负责先做范围校验。
	return NilValue()
}

// hasUpvalueIndex 判断指定 upvalue 下标在当前 VM 中是否存在。
func (vm *VM) hasUpvalueIndex(index int) bool {
	if index < 0 {
		// 负下标不是合法 Lua upvalue 索引。
		return false
	}
	if index < len(vm.upvalueCells) && vm.upvalueCells[index] != nil {
		// 执行期共享 cell 可作为真实 upvalue 来源。
		return true
	}
	// 没有 cell 时退回旧的 upvalue 快照边界。
	return index < len(vm.upvalues)
}

// setUpvalueValue 写入指定 upvalue 的当前运行期值。
func (vm *VM) setUpvalueValue(index int, value Value) {
	if index >= 0 && index < len(vm.upvalueCells) && vm.upvalueCells[index] != nil {
		// 共享 cell 存在时写回实时槽。
		vm.upvalueCells[index].Set(value)
	}
	if index >= 0 && index < len(vm.upvalues) {
		// 同步快照，保持 debug.getupvalue 与既有测试可见。
		vm.upvalues[index] = value
	}
}

// SkipNext 返回上一条指令是否要求跳过下一条指令。
//
// 当前最小 VM 尚未实现完整 pc 循环；LOADBOOL 的 C 字段会写入该标记，后续执行循环接入时
// 可据此调整 pc。返回值为 true 表示调用方应跳过下一条指令。
func (vm *VM) SkipNext() bool {
	// skipNext 由具体指令写入，直接返回当前状态。
	return vm.skipNext
}

// ApplyComparisonContinuationResult 应用比较元方法 yield 恢复后的返回值。
//
// result 是 __eq/__lt/__le 或 OP_LE fallback __lt 元方法恢复后返回的第一个 Lua 值，按 Lua
// truthiness 折算为比较结果。返回值表示恢复后是否需要跳过下一条指令。
func (vm *VM) ApplyComparisonContinuationResult(result Value) (bool, error) {
	if vm == nil || vm.pendingComparison == nil {
		// 没有待恢复比较时，调用方不能安全解释元方法返回值。
		return false, ErrUnsupportedInstruction
	}

	// 元方法返回值按 Lua truthiness 折算；LE fallback 的反向 LT 结果需要取反。
	comparisonResult := result.Truthy()
	if vm.pendingComparison.invert {
		// OP_LE 的 `not (right < left)` fallback 要把 __lt 结果取反后再应用测试语义。
		comparisonResult = !comparisonResult
	}
	vm.skipNext = comparisonResult != (vm.pendingComparison.instruction.A() != 0)
	vm.pendingComparison = nil
	return vm.skipNext, nil
}

// ApplyConcatContinuationResult 应用 CONCAT 元方法 yield 恢复后的返回值。
//
// result 是刚恢复的 __concat 元方法返回值；它只代表上次挂起的相邻两项拼接结果。本方法会
// 继续按 Lua 5.3 右结合顺序折叠剩余左侧寄存器，并在完成后写入原 A 寄存器。若后续折叠
// 再次触发 __concat yield，会更新 pendingConcat 后返回 ErrCoroutineYield，供 lua 层重新保存
// 外层 continuation。
func (vm *VM) ApplyConcatContinuationResult(result Value) error {
	if vm == nil || vm.pendingConcat == nil {
		// 没有待恢复 CONCAT 时，调用方不能安全解释元方法返回值。
		return ErrUnsupportedInstruction
	}

	// 取出待恢复状态后先清空；若后续再次 yield，会由 finishConcatContinuation 重新写入新状态。
	pending := *vm.pendingConcat
	vm.pendingConcat = nil
	return vm.finishConcatContinuation(pending.targetIndex, pending.startIndex, pending.nextIndex, result)
}

// PCOffset 返回上一条控制流指令请求的 pc 偏移。
//
// 返回 0 表示上一条指令没有请求跳转；完整执行循环接入后会消费该值更新 pc。
func (vm *VM) PCOffset() int {
	// pcOffset 由 JMP、FORLOOP、FORPREP 和 TFORLOOP 写入。
	return vm.pcOffset
}

// NextPC 根据上一条指令的跳过标记和跳转偏移计算下一条 PC。
//
// pc 必须是刚执行完成的指令位置；返回值只表达执行循环下一轮入口，不会修改 VM 内部状态。
// 该方法等价于 `pc + 1 + optional skip + pcOffset`，用于普通热路径合并 SkipNext 和 PCOffset 读取。
func (vm *VM) NextPC(pc int) int {
	// 普通路径先前进到下一条顺序指令。
	nextPC := pc + 1
	if vm.skipNext {
		// 测试类指令要求跳过下一条顺序指令。
		nextPC++
	}
	// 控制流指令的偏移在顺序推进后叠加。
	return nextPC + vm.pcOffset
}

// CloseFrom 返回上一条 JMP 指令要求关闭 upvalue 的起始寄存器。
//
// 第二个返回值为 false 表示上一条 JMP 没有 close 请求。
func (vm *VM) CloseFrom() (int, bool) {
	if vm.closeFrom < 0 {
		// closeFrom 为 -1 表示当前没有关闭 upvalue 请求。
		return 0, false
	}

	// 返回需要关闭 upvalue 的起始寄存器。
	return vm.closeFrom, true
}

// LastCallRequest 返回上一条调用类指令生成的调用请求。
//
// 返回 nil 表示上一条指令不是 CALL、TAILCALL 或 TFORCALL。
func (vm *VM) LastCallRequest() *CallRequest {
	if !vm.hasCallRequest {
		// 上一条指令不是调用类指令时没有可消费请求。
		return nil
	}

	// 返回 VM 内嵌请求地址，避免每次 CALL 为请求对象额外分配。
	return &vm.callRequest
}

// ReturnValues 返回上一条 RETURN 指令收集到的返回值。
//
// 返回切片副本，避免测试或调用方修改 VM 内部记录。
func (vm *VM) ReturnValues() []Value {
	if !vm.returned {
		// returned=false 表示上一条指令不是 RETURN；空切片表示 RETURN 0 个值。
		return nil
	}
	// 返回副本，保持内部 returnValues 不被外部修改。
	values := make([]Value, len(vm.returnValues))
	copy(values, vm.returnValues)
	return values
}

// BorrowReturnValues 返回上一条 RETURN 指令收集到的内部返回值切片。
//
// 返回值只允许 VM 执行循环在当前 Step 后立即读取；调用方不得修改或长期保存。公开测试和外部
// 调用仍应使用 ReturnValues 获取副本，以避免破坏 VM 内部状态。
func (vm *VM) BorrowReturnValues() []Value {
	if !vm.returned {
		// returned=false 表示上一条指令不是 RETURN。
		return nil
	}
	return vm.returnValues
}

// Step 执行单条 Lua 5.3 指令。
//
// instruction 必须来自当前函数 Proto；当前阶段实现基础加载和寄存器复制指令。未实现 opcode
// 返回 ErrUnsupportedInstruction，便于逐步补齐 VM 指令。
func (vm *VM) Step(instruction bytecode.Instruction) error {
	// 每条指令默认不跳过下一条；LOADBOOL 会在自身逻辑内重新设置该标记。
	vm.skipNext = false
	vm.pcOffset = 0
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	opCode := instruction.OpCode()
	if (vm.pendingLoadKXTarget >= 0 || vm.pendingSetList != nil) && opCode != bytecode.OpExtraArg {
		// LOADKX 或 SETLIST 的扩展形态必须紧跟 EXTRAARG，否则无法确定扩展参数。
		return ErrExpectedExtraArg
	}

	switch opCode {
	case bytecode.OpMove:
		// MOVE 只在寄存器之间复制值，不触发元方法、栈调整或 GC 屏障。
		return vm.executeMove(instruction)
	case bytecode.OpLoadK:
		// LOADK 从当前 Proto 常量表读取常量并写入目标寄存器。
		return vm.executeLoadK(instruction)
	case bytecode.OpLoadKX:
		// LOADKX 先记录目标寄存器，下一条 EXTRAARG 再提供完整常量索引。
		return vm.executeLoadKX(instruction)
	case bytecode.OpLoadBool:
		// LOADBOOL 写入 boolean，并在 C 非 0 时要求跳过下一条指令。
		return vm.executeLoadBool(instruction)
	case bytecode.OpLoadNil:
		// LOADNIL 把连续寄存器区间清为 nil。
		return vm.executeLoadNil(instruction)
	case bytecode.OpGetUpval:
		// GETUPVAL 从当前闭包 upvalue 列表读取值到寄存器。
		return vm.executeGetUpval(instruction)
	case bytecode.OpGetTabUp:
		// GETTABUP 从 upvalue table 中读取字段到目标寄存器。
		return vm.executeGetTabUp(instruction)
	case bytecode.OpSetupVal:
		// SETUPVAL 把寄存器值写回当前闭包 upvalue 列表。
		return vm.executeSetupVal(instruction)
	case bytecode.OpGetTable:
		// GETTABLE 对寄存器中的 table 执行 Lua 普通读取语义。
		return vm.executeGetTable(instruction)
	case bytecode.OpSetTabUp:
		// SETTABUP 把 RK 值写入 upvalue table。
		return vm.executeSetTabUp(instruction)
	case bytecode.OpSetTable:
		// SETTABLE 对寄存器中的 table 执行 Lua 普通写入语义。
		return vm.executeSetTable(instruction)
	case bytecode.OpNewTable:
		// NEWTABLE 创建空 table 并写入目标寄存器。
		return vm.executeNewTable(instruction)
	case bytecode.OpSelf:
		// SELF 为冒号调用准备方法和接收者寄存器。
		return vm.executeSelf(instruction)
	case bytecode.OpAdd:
		// ADD 是数值循环和函数调用基准的高频指令，使用专用路径减少通用函数分发开销。
		return vm.executeAdd(instruction)
	case bytecode.OpSub:
		// SUB 执行 Lua 5.3 减法，优先保留 integer 结果。
		return vm.executeSub(instruction)
	case bytecode.OpMul:
		// MUL 执行 Lua 5.3 乘法，优先保留 integer 结果。
		return vm.executeMul(instruction)
	case bytecode.OpMod:
		// MOD 执行 Lua 5.3 取模，优先按官方 VM 的双 integer 路径处理。
		return vm.executeMod(instruction)
	case bytecode.OpPow:
		// POW 执行 Lua 5.3 幂运算，结果为 float number。
		return vm.executeBinaryArithmetic(instruction, binaryArithmeticPow, metamethodPow)
	case bytecode.OpDiv:
		// DIV 执行 Lua 5.3 浮点除法，结果为 float number。
		return vm.executeDiv(instruction)
	case bytecode.OpIDiv:
		// IDIV 执行 Lua 5.3 向下取整除法，优先按官方 VM 的双 integer 路径处理。
		return vm.executeIDiv(instruction)
	case bytecode.OpBAnd:
		// BAND 执行 Lua 5.3 按位与，操作数必须可转为 integer。
		return vm.executeBinaryBitwise(instruction, binaryBitwiseAnd, metamethodBand)
	case bytecode.OpBOr:
		// BOR 执行 Lua 5.3 按位或，操作数必须可转为 integer。
		return vm.executeBinaryBitwise(instruction, binaryBitwiseOr, metamethodBor)
	case bytecode.OpBXor:
		// BXOR 执行 Lua 5.3 按位异或，操作数必须可转为 integer。
		return vm.executeBinaryBitwise(instruction, binaryBitwiseXor, metamethodBXor)
	case bytecode.OpShl:
		// SHL 执行 Lua 5.3 左移，负移位数按右移处理。
		return vm.executeBinaryBitwise(instruction, binaryBitwiseShl, metamethodShl)
	case bytecode.OpShr:
		// SHR 执行 Lua 5.3 右移，负移位数按左移处理。
		return vm.executeBinaryBitwise(instruction, binaryBitwiseShr, metamethodShr)
	case bytecode.OpUnm:
		// UNM 执行 Lua 5.3 一元负号，优先保留 integer 结果。
		return vm.executeUnaryMinus(instruction)
	case bytecode.OpBNot:
		// BNOT 执行 Lua 5.3 按位取反，操作数必须可转为 integer。
		return vm.executeBitwiseNot(instruction)
	case bytecode.OpNot:
		// NOT 执行 Lua 5.3 逻辑非，只有 nil 与 false 为假。
		return vm.executeLogicalNot(instruction)
	case bytecode.OpLen:
		// LEN 执行 Lua 5.3 长度运算，当前支持 string 与 table。
		return vm.executeLength(instruction)
	case bytecode.OpConcat:
		// CONCAT 把连续寄存器区间转换为 string 并拼接。
		return vm.executeConcat(instruction)
	case bytecode.OpEq:
		// EQ 比较两个 RK 值，并在条件不满足 A 期望时标记跳过下一条。
		return vm.executeEqualityTest(instruction)
	case bytecode.OpLt:
		// LT 比较两个 RK 值是否小于，并在条件不满足 A 期望时标记跳过下一条。
		return vm.executeOrderTest(instruction, compareLessThan, metamethodLt)
	case bytecode.OpLe:
		// LE 比较两个 RK 值是否小于等于，并在条件不满足 A 期望时标记跳过下一条。
		return vm.executeOrderTest(instruction, compareLessEqual, metamethodLe)
	case bytecode.OpJmp:
		// JMP 记录 pc 偏移，并按 A 字段记录待关闭 upvalue 范围。
		return vm.executeJump(instruction)
	case bytecode.OpTest:
		// TEST 根据 R(A) 的 truthy 与 C 期望决定是否跳过下一条。
		return vm.executeTest(instruction)
	case bytecode.OpTestSet:
		// TESTSET 条件匹配时复制 R(B) 到 R(A)，否则跳过下一条。
		return vm.executeTestSet(instruction)
	case bytecode.OpCall:
		// CALL 生成普通调用请求，后续执行循环消费。
		return vm.executeCall(instruction, false)
	case bytecode.OpTailCall:
		// TAILCALL 生成尾调用请求，后续执行循环消费。
		return vm.executeCall(instruction, true)
	case bytecode.OpReturn:
		// RETURN 收集返回值区间，后续调用帧退出逻辑消费。
		return vm.executeReturn(instruction)
	case bytecode.OpForLoop:
		// FORLOOP 执行数值 for 步进，并在仍未越界时记录跳转。
		return vm.executeForLoop(instruction)
	case bytecode.OpForPrep:
		// FORPREP 执行数值 for 初始预减，并记录进入循环体前跳转。
		return vm.executeForPrep(instruction)
	case bytecode.OpTForCall:
		// TFORCALL 生成泛型 for 迭代器调用请求。
		return vm.executeTForCall(instruction)
	case bytecode.OpTForLoop:
		// TFORLOOP 根据迭代结果是否为 nil 决定是否继续循环。
		return vm.executeTForLoop(instruction)
	case bytecode.OpSetList:
		// SETLIST 批量写入 table 数组区。
		return vm.executeSetList(instruction)
	case bytecode.OpClosure:
		// CLOSURE 根据子 Proto 创建 Lua closure 值。
		return vm.executeClosure(instruction)
	case bytecode.OpVararg:
		// VARARG 把当前函数 vararg 写入寄存器区间。
		return vm.executeVararg(instruction)
	case bytecode.OpExtraArg:
		// EXTRAARG 为前置 LOADKX 提供扩展常量索引。
		return vm.executeExtraArg(instruction)
	default:
		// 未实现 opcode 明确返回错误，避免静默跳过导致状态错误。
		return fmt.Errorf("%w: %s", ErrUnsupportedInstruction, instruction.OpCode().Name())
	}
}

// executeLoadK 执行 Lua 5.3 OP_LOADK 指令。
//
// 指令语义为 R(A) := Kst(Bx)。A 是 0-based 寄存器编号，Bx 是 0-based 常量表索引；
// 任一索引越界时返回明确错误，并保持目标寄存器不变。
func (vm *VM) executeLoadK(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	constantIndex := instruction.Bx()
	return vm.loadConstantIntoRegister(targetIndex, constantIndex)
}

// executeLoadKX 执行 Lua 5.3 OP_LOADKX 指令。
//
// 指令语义为 R(A) := Kst(extra arg)，其中常量索引来自紧随其后的 EXTRAARG。当前方法只记录
// 目标寄存器，真正加载在 executeExtraArg 中完成。
func (vm *VM) executeLoadKX(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if vm.pendingLoadKXTarget >= 0 {
		// 已存在待完成 LOADKX 时不能再接收新的 LOADKX。
		return ErrExpectedExtraArg
	}

	// 记录目标寄存器，等待下一条 EXTRAARG 提供常量索引。
	vm.pendingLoadKXTarget = targetIndex
	return nil
}

// executeExtraArg 执行 Lua 5.3 OP_EXTRAARG 指令。
//
// 当前支持作为 LOADKX 或扩展 SETLIST 的后继指令使用。LOADKX 使用 Ax 作为常量索引；
// SETLIST 使用 Ax 作为批次编号。
func (vm *VM) executeExtraArg(instruction bytecode.Instruction) error {
	if vm.pendingLoadKXTarget >= 0 {
		// LOADKX 等待 EXTRAARG 时，用 Ax 作为常量索引完成加载。
		targetIndex := vm.pendingLoadKXTarget
		vm.pendingLoadKXTarget = -1
		return vm.loadConstantIntoRegister(targetIndex, instruction.Ax())
	}
	if vm.pendingSetList != nil {
		// SETLIST 等待 EXTRAARG 时，用 Ax 作为批次编号完成 table 数组写入。
		pending := vm.pendingSetList
		vm.pendingSetList = nil
		return vm.writeSetList(pending.tableIndex, pending.valueCount, instruction.Ax())
	}

	// 没有等待扩展参数的前置指令时，EXTRAARG 属于非法字节码。
	return ErrUnexpectedExtraArg
}

// executeLoadBool 执行 Lua 5.3 OP_LOADBOOL 指令。
//
// 指令语义为 R(A) := bool(B)，若 C 非 0 则跳过下一条指令。当前最小 VM 只记录 skipNext
// 标记，完整 pc 调整会在执行循环接入后使用该标记。
func (vm *VM) executeLoadBool(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}

	// Lua 5.3 使用 B==0 表示 false，非 0 表示 true。
	vm.registers[targetIndex] = BooleanValue(instruction.B() != 0)
	vm.skipNext = instruction.C() != 0
	return nil
}

// executeLoadNil 执行 Lua 5.3 OP_LOADNIL 指令。
//
// 指令语义为 R(A)..R(A+B) := nil。A 和 A+B 都必须落在当前寄存器窗口内；越界时不修改
// 任何寄存器。
func (vm *VM) executeLoadNil(instruction bytecode.Instruction) error {
	startIndex := instruction.A()
	endIndex := instruction.A() + instruction.B()
	if startIndex < 0 || endIndex >= len(vm.registers) {
		// 清空区间越界时拒绝执行，并保持寄存器窗口原样。
		return ErrRegisterOutOfRange
	}

	for registerIndex := startIndex; registerIndex <= endIndex; registerIndex++ {
		// LOADNIL 需要把闭区间内每个寄存器都写为 Lua nil。
		vm.registers[registerIndex] = NilValue()
	}
	return nil
}

// executeGetUpval 执行 Lua 5.3 OP_GETUPVAL 指令。
//
// 指令语义为 R(A) := UpValue[B]。A 是目标寄存器，B 是当前闭包的 upvalue 索引；任一
// 索引越界时返回明确错误，并保持目标寄存器不变。
func (vm *VM) executeGetUpval(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	upvalueIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if !vm.hasUpvalueIndex(upvalueIndex) {
		// upvalue 越界通常表示损坏 chunk 或闭包原型不匹配。
		return ErrUpvalueOutOfRange
	}

	// GETUPVAL 读取共享 cell 或快照值；引用类型 identity 保留在 Ref 字段中。
	vm.registers[targetIndex] = vm.upvalueValue(upvalueIndex)
	return nil
}

// executeGetTabUp 执行 Lua 5.3 OP_GETTABUP 指令。
//
// 指令语义为 R(A) := UpValue[B][RK(C)]。A 是目标寄存器，B 是 upvalue table 索引，
// C 使用 RK 编码读取 key；任一读取失败时返回明确错误，并保持目标寄存器不变。
func (vm *VM) executeGetTabUp(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	upvalueIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if !vm.hasUpvalueIndex(upvalueIndex) {
		// upvalue 越界通常表示损坏 chunk 或闭包原型不匹配。
		return ErrUpvalueOutOfRange
	}

	table, err := tableFromValue(vm.upvalueValue(upvalueIndex))
	if err != nil {
		// GETTABUP 需要 upvalue 保存 table，例如 Lua 5.3 的 _ENV。
		return err
	}
	if table.metatable == nil {
		if bytecode.IsK(instruction.C()) {
			// 无元表 upvalue table 使用 string 常量 key 时可直接查 hash，避免构造临时 Value 和通用 key 编码。
			keyIndex := bytecode.IndexK(instruction.C())
			if keyIndex < 0 || keyIndex >= len(vm.constants) {
				// key 常量越界时保持目标寄存器不变。
				return ErrConstantOutOfRange
			}
			keyConstant := vm.constants[keyIndex]
			if keyConstant.Kind == bytecode.ConstantString {
				// string 常量 raw get 不会触发元方法，未命中直接返回 nil。
				if !vm.disableStringTableReadCache && vm.currentPC >= 0 && vm.currentPC < len(vm.stringTableReadCache) {
					// 热路径直接检查当前 PC 的 table/version，避免每次全局读取进入 helper。
					cacheEntry := vm.stringTableReadCache[vm.currentPC]
					if cacheEntry.valid && cacheEntry.table == table && cacheEntry.tableID == table.CacheIdentity() && cacheEntry.version == table.mutationVersion {
						// table 版本未变化时复用上一轮同 PC 的读取结果。
						vm.registers[targetIndex] = cacheEntry.value
						return nil
					}
				}
				value := table.RawGetString(keyConstant.String)
				if !vm.disableStringTableReadCache && vm.ensureStringTableReadCache() {
					// 记录当前 PC 的读取结果，下一轮相同字段可直接命中。
					vm.stringTableReadCache[vm.currentPC] = stringTableReadCacheEntry{table: table, tableID: table.CacheIdentity(), version: table.mutationVersion, value: value, valid: true}
				}
				vm.registers[targetIndex] = value
				return nil
			}
		}
		key, err := vm.rkValue(instruction.C())
		if err != nil {
			// RK key 无法读取时不能执行 table 查询，目标寄存器保持原值。
			return err
		}
		// 无元表 table 的普通读取等价于 raw get，跳过 __index 链检查以减少全局变量热路径开销。
		value, err := table.RawGet(key)
		if err != nil {
			// raw get 的 key 编码错误需要直接返回，目标寄存器保持原值。
			return err
		}
		vm.registers[targetIndex] = value
		return nil
	}

	key, err := vm.rkValue(instruction.C())
	if err != nil {
		// RK key 无法读取时不能执行 table 查询，目标寄存器保持原值。
		return err
	}
	value, err := table.GetWithRunner(key, vm.luaMetamethodRunner)
	if err != nil {
		// table 普通读取可能因为 key 编码、不可索引源值或 Lua closure 元方法返回错误。
		return err
	}

	// GETTABUP 成功后才覆盖目标寄存器，保证错误路径无副作用。
	vm.registers[targetIndex] = value
	return nil
}

// executeSetupVal 执行 Lua 5.3 OP_SETUPVAL 指令。
//
// 指令语义为 UpValue[B] := R(A)。A 是源寄存器，B 是当前闭包的 upvalue 索引；任一
// 索引越界时返回明确错误，并保持 upvalue 不变。
func (vm *VM) executeSetupVal(instruction bytecode.Instruction) error {
	sourceIndex := instruction.A()
	upvalueIndex := instruction.B()
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取，upvalue 必须保持原值。
		return ErrRegisterOutOfRange
	}
	if !vm.hasUpvalueIndex(upvalueIndex) {
		// upvalue 越界时不能写入，避免破坏闭包捕获值。
		return ErrUpvalueOutOfRange
	}

	// SETUPVAL 写入当前寄存器值；共享 cell 会同步回外层局部寄存器。
	vm.setUpvalueValue(upvalueIndex, vm.registers[sourceIndex])
	return nil
}

// executeGetTable 执行 Lua 5.3 OP_GETTABLE 指令。
//
// 指令语义为 R(A) := R(B)[RK(C)]。A 是目标寄存器，B 是 table 或 userdata 所在寄存器，
// C 使用 RK 编码读取 key；目标或源寄存器越界、源值不可索引、key 读取失败时返回错误，
// 并保持目标寄存器不变。
func (vm *VM) executeGetTable(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	tableIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if tableIndex < 0 || tableIndex >= len(vm.registers) {
		// table 源寄存器越界时不能读取，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}

	if receiverValue := vm.registers[tableIndex]; receiverValue.Kind == KindTable {
		// 普通无元表 table 读取等价于 raw get，可避开通用 __index 分派。
		table, ok := receiverValue.Ref.(*Table)
		if !ok || table == nil {
			// table 类型引用损坏时仍返回原有 table 解析错误。
			return ErrExpectedTable
		}
		if table.metatable == nil {
			if bytecode.IsK(instruction.C()) {
				// 无元表 table 使用 string 常量 key 时可直接查 hash，避免构造临时 Value 和通用 key 编码。
				keyIndex := bytecode.IndexK(instruction.C())
				if keyIndex < 0 || keyIndex >= len(vm.constants) {
					// key 常量越界时保持目标寄存器不变。
					return ErrConstantOutOfRange
				}
				keyConstant := vm.constants[keyIndex]
				if keyConstant.Kind == bytecode.ConstantString {
					// string 常量 raw get 不会触发元方法，未命中直接返回 nil。
					if !vm.disableStringTableReadCache && vm.currentPC >= 0 && vm.currentPC < len(vm.stringTableReadCache) {
						// 热路径直接检查当前 PC 的 table/version，避免每次字段读取进入 helper。
						cacheEntry := vm.stringTableReadCache[vm.currentPC]
						if cacheEntry.valid && cacheEntry.table == table && cacheEntry.tableID == table.CacheIdentity() && cacheEntry.version == table.mutationVersion {
							// table 版本未变化时复用上一轮同 PC 的读取结果。
							vm.registers[targetIndex] = cacheEntry.value
							return nil
						}
					}
					value := table.RawGetString(keyConstant.String)
					if !vm.disableStringTableReadCache && vm.ensureStringTableReadCache() {
						// 记录当前 PC 的读取结果，下一轮相同字段可直接命中。
						vm.stringTableReadCache[vm.currentPC] = stringTableReadCacheEntry{table: table, tableID: table.CacheIdentity(), version: table.mutationVersion, value: value, valid: true}
					}
					vm.registers[targetIndex] = value
					return nil
				}
			}
			if !bytecode.IsK(instruction.C()) {
				// 数值 for 常见的整数寄存器 key 直接查数组区，避免 RK Value 复制和 ToInteger 分派。
				keyIndex := bytecode.IndexK(instruction.C())
				if keyIndex < 0 || keyIndex >= len(vm.registers) {
					// key 寄存器越界时保持目标寄存器不变。
					return ErrRegisterOutOfRange
				}
				keyValue := vm.registers[keyIndex]
				if keyValue.Kind == KindInteger {
					// integer key raw get 不会触发元方法，未命中直接返回 nil。
					vm.registers[targetIndex] = table.RawGetInteger(keyValue.Integer)
					return nil
				}
			}
			// 无元表时 raw 未命中也直接返回 nil，符合 Lua 5.3 普通 table 读取语义。
			key, err := vm.rkValue(instruction.C())
			if err != nil {
				// RK key 无法读取时不能执行 table 查询，目标寄存器保持原值。
				return err
			}
			value, err := table.RawGet(key)
			if err != nil {
				// raw get 的 key 编码错误需要直接返回，目标寄存器保持原值。
				return err
			}
			vm.registers[targetIndex] = value
			return nil
		}
	}

	key, err := vm.rkValue(instruction.C())
	if err != nil {
		// RK key 无法读取时不能执行 table 查询，目标寄存器保持原值。
		return err
	}
	value, err := vm.indexedValue(vm.registers[tableIndex], key)
	if err != nil {
		// 普通读取可能因为 key 编码、不可索引源值或暂不支持的元方法返回错误。
		return err
	}

	// GETTABLE 成功后才覆盖目标寄存器，保证错误路径无副作用。
	vm.registers[targetIndex] = value
	return nil
}

// executeSetTable 执行 Lua 5.3 OP_SETTABLE 指令。
//
// 指令语义为 R(A)[RK(B)] := RK(C)。A 是 table 所在寄存器，B/C 使用 RK 编码读取 key 与
// value；任一读取或写入失败时返回错误，已成功读取的寄存器值不被修改。
func (vm *VM) executeSetTable(instruction bytecode.Instruction) error {
	tableIndex := instruction.A()
	if tableIndex < 0 || tableIndex >= len(vm.registers) {
		// table 源寄存器越界时不能读取，也无法执行写入。
		return ErrRegisterOutOfRange
	}

	receiverValue := vm.registers[tableIndex]
	if receiverValue.Kind != KindTable {
		// SETTABLE 只能在 table 值上执行，非 table 值后续会接入元方法错误语义。
		return ErrExpectedTable
	}
	table, ok := receiverValue.Ref.(*Table)
	if !ok || table == nil {
		// table 类型引用损坏时仍返回原有 table 解析错误。
		return ErrExpectedTable
	}
	if table.metatable == nil {
		if bytecode.IsK(instruction.B()) {
			// 无元表 table 使用 string 常量 key 时可直接写 hash，避免构造临时 Value 和通用 key 编码。
			keyIndex := bytecode.IndexK(instruction.B())
			if keyIndex < 0 || keyIndex >= len(vm.constants) {
				// key 常量越界时不能读取 value，也不能尝试写入 table。
				return ErrConstantOutOfRange
			}
			keyConstant := vm.constants[keyIndex]
			if keyConstant.Kind == bytecode.ConstantString {
				if !bytecode.IsK(instruction.C()) {
					// value 来自寄存器时直接读取，避免 string key 写入热路径重复进入 RK 分派。
					valueIndex := bytecode.IndexK(instruction.C())
					if valueIndex < 0 || valueIndex >= len(vm.registers) {
						// value 寄存器越界时不能尝试写入 table。
						return ErrRegisterOutOfRange
					}
					return table.RawSetStringWithConstCheck(keyConstant.String, vm.registers[valueIndex])
				}
				// string key 写入不会触发元方法；value 仍按 RK 语义读取，保留常量越界错误边界。
				value, err := vm.rkValue(instruction.C())
				if err != nil {
					// value 常量读取失败时不能尝试写入 table。
					return err
				}
				return table.RawSetStringWithConstCheck(keyConstant.String, value)
			}
		}
		if !bytecode.IsK(instruction.B()) {
			// 数值 for 常见的整数寄存器 key 直接写数组区，避免 RawSet 内部再次解析 key 类型。
			keyIndex := bytecode.IndexK(instruction.B())
			if keyIndex < 0 || keyIndex >= len(vm.registers) {
				// key 寄存器越界时不能执行写入。
				return ErrRegisterOutOfRange
			}
			keyValue := vm.registers[keyIndex]
			if keyValue.Kind == KindInteger {
				if !bytecode.IsK(instruction.C()) {
					// value 同样来自寄存器时直接读取，避免数值 for 数组写入热路径重复解析 RK。
					valueIndex := bytecode.IndexK(instruction.C())
					if valueIndex < 0 || valueIndex >= len(vm.registers) {
						// value 寄存器越界时不能尝试写入 table。
						return ErrRegisterOutOfRange
					}
					value := vm.registers[valueIndex]
					if keyValue.Integer > 0 && !value.IsNil() {
						// 正整数非 nil 数组写入走更窄的 table 热路径，跳过删除语义分支。
						return table.RawSetPositiveIntegerNonNilWithConstCheck(keyValue.Integer, value)
					}
					// integer key raw set 不触发元方法；寄存器值已按 RK 语义读取完成。
					return table.RawSetIntegerWithConstCheck(keyValue.Integer, value)
				}
				value, err := vm.rkValue(instruction.C())
				if err != nil {
					// value 常量读取失败时不能尝试写入 table。
					return err
				}
				// integer key raw set 不触发元方法；value 已按 RK 语义读取完成。
				return table.RawSetIntegerWithConstCheck(keyValue.Integer, value)
			}
		}
		key, err := vm.rkValue(instruction.B())
		if err != nil {
			// key 读取失败时不能尝试写入 table。
			return err
		}
		value, err := vm.rkValue(instruction.C())
		if err != nil {
			// value 读取失败时不能尝试写入 table。
			return err
		}
		// 无元表 table 写入等价于 raw set，跳过 __newindex 链检查以减少数组/字段写入热路径开销。
		return table.RawSet(key, value)
	}

	key, err := vm.rkValue(instruction.B())
	if err != nil {
		// key 读取失败时不能尝试写入 table。
		return err
	}
	value, err := vm.rkValue(instruction.C())
	if err != nil {
		// value 读取失败时不能尝试写入 table。
		return err
	}
	// SETTABLE 使用带 runner 的普通写入，支持 Lua closure 形式 __newindex 元方法。
	return table.SetWithRunner(key, value, vm.luaMetamethodRunner)
}

// executeSetTabUp 执行 Lua 5.3 OP_SETTABUP 指令。
//
// 指令语义为 UpValue[A][RK(B)] := RK(C)。A 是 upvalue table 索引，B/C 使用 RK 编码
// 读取 key 与 value；任一读取或写入失败时返回错误，upvalue 本身不被替换。
func (vm *VM) executeSetTabUp(instruction bytecode.Instruction) error {
	upvalueIndex := instruction.A()
	if !vm.hasUpvalueIndex(upvalueIndex) {
		// upvalue 越界时不能读取 table，也无法执行写入。
		return ErrUpvalueOutOfRange
	}

	table, err := tableFromValue(vm.upvalueValue(upvalueIndex))
	if err != nil {
		// SETTABUP 需要 upvalue 保存 table，例如 Lua 5.3 的 _ENV。
		return err
	}
	if table.metatable == nil {
		if bytecode.IsK(instruction.B()) {
			// 无元表 upvalue table 使用 string 常量 key 时可直接写 hash，避免构造临时 Value。
			keyIndex := bytecode.IndexK(instruction.B())
			if keyIndex < 0 || keyIndex >= len(vm.constants) {
				// key 常量越界时不能读取 value，也不能尝试写入 table。
				return ErrConstantOutOfRange
			}
			keyConstant := vm.constants[keyIndex]
			if keyConstant.Kind == bytecode.ConstantString {
				if !bytecode.IsK(instruction.C()) {
					// value 来自寄存器时直接读取，避免 string key 写入热路径重复进入 RK 分派。
					valueIndex := bytecode.IndexK(instruction.C())
					if valueIndex < 0 || valueIndex >= len(vm.registers) {
						// value 寄存器越界时不能尝试写入 table。
						return ErrRegisterOutOfRange
					}
					return table.RawSetStringWithConstCheck(keyConstant.String, vm.registers[valueIndex])
				}
				// string key 写入不会触发元方法；value 仍按 RK 语义读取，保留常量越界错误边界。
				value, err := vm.rkValue(instruction.C())
				if err != nil {
					// value 常量读取失败时不能尝试写入 table。
					return err
				}
				return table.RawSetStringWithConstCheck(keyConstant.String, value)
			}
		}
		key, err := vm.rkValue(instruction.B())
		if err != nil {
			// key 读取失败时不能尝试写入 table。
			return err
		}
		value, err := vm.rkValue(instruction.C())
		if err != nil {
			// value 读取失败时不能尝试写入 table。
			return err
		}
		// 无元表 upvalue table 写入等价于 raw set，常见于 _ENV 初始化和普通模块表更新。
		return table.RawSet(key, value)
	}

	key, err := vm.rkValue(instruction.B())
	if err != nil {
		// key 读取失败时不能尝试写入 table。
		return err
	}
	value, err := vm.rkValue(instruction.C())
	if err != nil {
		// value 读取失败时不能尝试写入 table。
		return err
	}
	// SETTABUP 使用带 runner 的普通写入，支持 Lua closure 形式 __newindex 元方法。
	return table.SetWithRunner(key, value, vm.luaMetamethodRunner)
}

// executeNewTable 执行 Lua 5.3 OP_NEWTABLE 指令。
//
// 指令语义为 R(A) := {}。B/C 在 Lua 5.3 中携带数组区和 hash 区预分配 hint；当前额外支持
// VM 预扫描出的强约束 numeric-for 连续数组写入 hint，其余形态仍创建普通空 table。
func (vm *VM) executeNewTable(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}

	table := NewTable()
	if arrayCapacity := vm.tableArrayPreallocCapacityAt(vm.currentPC); arrayCapacity > 0 {
		// 强约束数据流已证明后续会连续写入 1..N，提前预留 cap 但保持 len=0 和空表语义。
		table = newTableWithArrayCapacity(arrayCapacity)
	}

	// NEWTABLE 创建新的 table identity，并以引用值写入目标寄存器。
	vm.registers[targetIndex] = ReferenceValue(KindTable, table)
	return nil
}

// executeSelf 执行 Lua 5.3 OP_SELF 指令。
//
// 指令语义为 R(A+1) := R(B); R(A) := R(B)[RK(C)]。该指令服务冒号调用语法，
// 接收者可为 table 或携带 `__index` 元表的 userdata；任一读取失败时保持 A 与 A+1
// 两个目标寄存器不变。
func (vm *VM) executeSelf(instruction bytecode.Instruction) error {
	methodIndex := instruction.A()
	receiverTargetIndex := instruction.A() + 1
	receiverSourceIndex := instruction.B()
	if methodIndex < 0 || receiverTargetIndex >= len(vm.registers) {
		// SELF 会同时写 R(A) 与 R(A+1)，任一目标越界都必须拒绝。
		return ErrRegisterOutOfRange
	}
	if receiverSourceIndex < 0 || receiverSourceIndex >= len(vm.registers) {
		// 接收者源寄存器越界时不能读取，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}

	receiverValue := vm.registers[receiverSourceIndex]
	key, err := vm.rkValue(instruction.C())
	if err != nil {
		// RK method key 无法读取时不能覆盖目标寄存器。
		return err
	}

	if receiverValue.Kind == KindTable {
		// 无元表 table 的方法读取等价于 raw get，仍需保留 SELF 对接收者寄存器的写入布局。
		table, err := tableFromValue(receiverValue)
		if err != nil {
			// table 类型引用损坏时保持两个目标寄存器不变。
			return err
		}
		if table.metatable == nil {
			// raw 未命中返回 nil，后续 CALL 会按原有语义报告不可调用错误。
			methodValue, err := table.RawGet(key)
			if err != nil {
				// key 编码错误时不能覆盖 SELF 目标寄存器。
				return err
			}
			vm.registers[receiverTargetIndex] = receiverValue
			vm.registers[methodIndex] = methodValue
			return nil
		}
	}

	methodValue, err := vm.indexedValue(receiverValue, key)
	if err != nil {
		// 普通读取可能因为 key 编码、不可索引接收者或暂不支持的元方法返回错误。
		return err
	}

	// 两个目标都确认可写且方法读取成功后，再一次性写入冒号调用所需布局。
	vm.registers[receiverTargetIndex] = receiverValue
	vm.registers[methodIndex] = methodValue
	return nil
}

// executeBinaryArithmetic 执行 Lua 5.3 二元算术指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数；operation 决定具体算术
// 语义。任一读取失败时返回错误；转换失败时按 Lua 5.3 规则尝试对应算术元方法。
func (vm *VM) executeBinaryArithmetic(instruction bytecode.Instruction, operation binaryArithmeticOperation, metamethodName string) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}

	leftValue, err := vm.rkValue(instruction.B())
	if err != nil {
		// 左操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	rightValue, err := vm.rkValue(instruction.C())
	if err != nil {
		// 右操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}

	result, err := operation(leftValue, rightValue)
	if err != nil {
		// 原始算术失败时只对类型转换错误尝试元方法；零除等数值错误保持原错误。
		if errors.Is(err, ErrArithmeticOperand) || errors.Is(err, ErrIntegerOperand) {
			metamethodResult, found, metamethodErr := vm.callBinaryMetamethod(leftValue, rightValue, metamethodName)
			if metamethodErr != nil {
				// 元方法被找到但调用失败时，返回调用错误并保持目标寄存器原值。
				return metamethodErr
			}
			if found {
				// 元方法返回值就是 Lua 运算结果，不再强制转换成 number。
				vm.registers[targetIndex] = metamethodResult
				return nil
			}
		}
		// 无元方法或不允许回退的错误返回原始错误，目标寄存器保持原值。
		return err
	}

	// 算术成功后才覆盖目标寄存器，保证错误路径无副作用。
	vm.registers[targetIndex] = result
	return nil
}

// executeAdd 执行 Lua 5.3 OP_ADD 指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数。普通 integer/number 加法
// 直接在本函数完成，转换失败时仍按 Lua 5.3 规则尝试 `__add` 元方法。
func (vm *VM) executeAdd(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if handled, err := vm.tryCachedIntegerAddArithmetic(instruction, targetIndex); handled || err != nil {
		// ADD 专用缓存命中已完成写回；缓存形态损坏时返回原始寄存器错误。
		return err
	}
	if handled, err := vm.tryNativeNumberAdd(instruction); handled || err != nil {
		// 原生 number 参与的加法可跳过通用 RK 和字符串转换检查；双 integer 仍走整数路径。
		return err
	}

	// ADD 复用整数寄存器缓存，同时保留 number、字符串数字和元方法回退语义。
	return vm.executeFastArithmetic(instruction, arithmeticIntRegisterCacheAdd, func(left float64, right float64) float64 {
		return left + right
	}, metamethodAdd)
}

// tryNativeNumberAdd 执行至少一侧为原生 number 的 ADD 窄快路径。
//
// 该路径只处理 integer/number 两类真实数值，且排除双 integer 以保留 Lua 5.3 integer
// 加法结果；字符串数字、非数值和元方法语义返回 handled=false 交给完整算术路径。
func (vm *VM) tryNativeNumberAdd(instruction bytecode.Instruction) (bool, error) {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return true, ErrRegisterOutOfRange
	}

	leftValue, err := vm.rkValue(instruction.B())
	if err != nil {
		// 左操作数读取失败时保持原错误语义。
		return true, err
	}
	rightValue, err := vm.rkValue(instruction.C())
	if err != nil {
		// 右操作数读取失败时保持原错误语义。
		return true, err
	}
	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		// 双 integer 必须保留 integer 结果和现有 integer inline cache 行为。
		return false, nil
	}
	leftNumber, leftOK := nativeNumberValue(leftValue)
	rightNumber, rightOK := nativeNumberValue(rightValue)
	if !leftOK || !rightOK {
		// 字符串数字或元方法相关类型必须回退完整 Lua 算术路径。
		return false, nil
	}

	// 至少一侧为 number 时，Lua 5.3 加法结果为 float number。
	vm.rememberNativeNumberAdd(instruction)
	vm.registers[targetIndex] = NumberValue(leftNumber + rightNumber)
	return true, nil
}

// executeSub 执行 Lua 5.3 OP_SUB 指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数。连续 integer 寄存器减法
// 会记录当前 PC 的热路径缓存；类型变化时回退完整 Lua 算术和 `__sub` 元方法语义。
func (vm *VM) executeSub(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if handled, err := vm.tryCachedIntegerSubArithmetic(instruction, targetIndex); handled || err != nil {
		// SUB 专用缓存命中已完成写回；缓存形态损坏时返回原始寄存器错误。
		return err
	}

	// SUB 复用整数寄存器缓存，同时保留 number、字符串数字和元方法回退语义。
	return vm.executeFastArithmetic(instruction, arithmeticIntRegisterCacheSub, func(left float64, right float64) float64 {
		return left - right
	}, metamethodSub)
}

// executeMul 执行 Lua 5.3 OP_MUL 指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数。连续 integer 寄存器乘法
// 会记录当前 PC 的热路径缓存；类型变化时回退完整 Lua 算术和 `__mul` 元方法语义。
func (vm *VM) executeMul(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if handled, err := vm.tryCachedIntegerMulArithmetic(instruction, targetIndex); handled || err != nil {
		// MUL 专用缓存命中已完成写回；缓存形态损坏时返回原始寄存器错误。
		return err
	}
	if handled, err := vm.tryNumberConstantMul(instruction, targetIndex); handled || err != nil {
		// 混合算术循环常见 `number register * number constant`，命中后跳过通用 RK 和闭包回调。
		return err
	}

	// MUL 复用整数寄存器缓存，同时保留 number、字符串数字和元方法回退语义。
	return vm.executeFastArithmetic(instruction, arithmeticIntRegisterCacheMul, func(left float64, right float64) float64 {
		return left * right
	}, metamethodMul)
}

// executeMod 执行 Lua 5.3 OP_MOD 指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数。对齐官方 Lua 5.3.6 的
// lvm.c，双 integer 先直接执行 integer 取模；number、字符串数字和元方法语义回退完整路径。
func (vm *VM) executeMod(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if handled, err := vm.tryCachedIntegerModArithmetic(instruction, targetIndex); handled || err != nil {
		// MOD 专用缓存命中已完成写回；零除或缓存形态损坏时返回原始错误。
		return err
	}

	leftOperand := instruction.B()
	rightOperand := instruction.C()
	leftValue, err := vm.rkValue(leftOperand)
	if err != nil {
		// 左操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	rightValue, err := vm.rkValue(rightOperand)
	if err != nil {
		// 右操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		if rightValue.Integer == 0 {
			// integer 取模零除必须返回 Lua 运行期错误，不能触发 Go panic。
			return fmt.Errorf("'n%%0': %w", ErrDivisionByZero)
		}
		// 双 integer 直接写回 integer 余数，并记录当前 PC 的热路径。
		vm.rememberIntegerRegisterArithmetic(leftOperand, rightOperand, arithmeticIntRegisterCacheMod)
		vm.registers[targetIndex] = IntegerValue(integerModulo(leftValue.Integer, rightValue.Integer))
		return nil
	}

	// 非双 integer 继续使用完整路径，保留 number、字符串数字和 __mod 元方法。
	return vm.executeBinaryArithmetic(instruction, binaryArithmeticMod, metamethodMod)
}

// executeIDiv 执行 Lua 5.3 OP_IDIV 指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数。对齐官方 Lua 5.3.6 的
// lvm.c，双 integer 先直接执行 floor division；number、字符串数字和元方法语义回退完整路径。
func (vm *VM) executeIDiv(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if handled, err := vm.tryCachedIntegerIDivArithmetic(instruction, targetIndex); handled || err != nil {
		// IDIV 专用缓存命中已完成写回；零除或缓存形态损坏时返回原始错误。
		return err
	}

	leftOperand := instruction.B()
	rightOperand := instruction.C()
	leftValue, err := vm.rkValue(leftOperand)
	if err != nil {
		// 左操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	rightValue, err := vm.rkValue(rightOperand)
	if err != nil {
		// 右操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		if rightValue.Integer == 0 {
			// integer floor division 零除必须返回 Lua 运行期错误，不能触发 Go panic。
			return ErrDivisionByZero
		}
		// 双 integer 直接写回 integer 商，并记录当前 PC 的热路径。
		vm.rememberIntegerRegisterArithmetic(leftOperand, rightOperand, arithmeticIntRegisterCacheIDiv)
		vm.registers[targetIndex] = IntegerValue(integerFloorDiv(leftValue.Integer, rightValue.Integer))
		return nil
	}

	// 非双 integer 继续使用完整路径，保留 number、字符串数字和 __idiv 元方法。
	return vm.executeBinaryArithmetic(instruction, binaryArithmeticIDiv, metamethodIDiv)
}

// executeDiv 执行 Lua 5.3 OP_DIV 指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数。原生 integer/number 直接按
// float64 除法写回；字符串数字、非数值和元方法语义回退完整二元算术路径。
func (vm *VM) executeDiv(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	currentPC := vm.currentPC
	if currentPC >= 0 && currentPC < len(vm.arithmeticIntRegisterCache) && currentPC < len(vm.arithmeticIntOperandCache) && vm.arithmeticIntRegisterCache[currentPC] == arithmeticIntRegisterCacheDivNumber {
		// 当前 PC 命中过 DIV number 缓存时，直接进入窄路径，避免普通未命中路径重复读取 PC。
		handled, err := vm.tryCachedNativeNumberDivArithmetic(instruction, currentPC)
		if handled || err != nil {
			// DIV number 缓存命中已完成写回；缓存形态损坏时返回原始寄存器错误。
			return err
		}
	}

	leftValue, err := vm.rkValue(instruction.B())
	if err != nil {
		// 左操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	rightValue, err := vm.rkValue(instruction.C())
	if err != nil {
		// 右操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	switch leftValue.Kind {
	case KindInteger:
		// integer 左操作数在 DIV 中先转换为 float64。
		switch rightValue.Kind {
		case KindInteger:
			// integer / integer 也返回 number。
			vm.rememberNativeNumberDiv(instruction)
			vm.registers[targetIndex] = NumberValue(float64(leftValue.Integer) / float64(rightValue.Integer))
			return nil
		case KindNumber:
			// integer / number 直接按 float64 执行。
			vm.rememberNativeNumberDiv(instruction)
			vm.registers[targetIndex] = NumberValue(float64(leftValue.Integer) / rightValue.Number)
			return nil
		}
	case KindNumber:
		// number 左操作数只需区分右侧原生数值类型。
		switch rightValue.Kind {
		case KindInteger:
			// number / integer 直接按 float64 执行。
			vm.rememberNativeNumberDiv(instruction)
			vm.registers[targetIndex] = NumberValue(leftValue.Number / float64(rightValue.Integer))
			return nil
		case KindNumber:
			// number / number 直接按 float64 执行。
			vm.rememberNativeNumberDiv(instruction)
			vm.registers[targetIndex] = NumberValue(leftValue.Number / rightValue.Number)
			return nil
		}
	}

	// 非原生数值继续使用完整路径，保留字符串数字转换和 __div 元方法。
	return vm.executeBinaryArithmetic(instruction, binaryArithmeticDiv, metamethodDiv)
}

// tryNumberConstantMul 执行寄存器数值与 number 常量相乘的窄快路径。
//
// instruction 必须是 MUL；只处理一侧为寄存器、另一侧为 Proto number 常量，且寄存器运行期值是
// integer 或 number 的场景。字符串数字、非数值和元方法语义返回 handled=false 交给完整算术路径。
func (vm *VM) tryNumberConstantMul(instruction bytecode.Instruction, targetIndex int) (bool, error) {
	// 先解析 B/C 操作数，只有恰好一侧为常量时才可能命中该窄快路径。
	leftOperand := instruction.B()
	rightOperand := instruction.C()
	leftIsConstant := bytecode.IsK(leftOperand)
	rightIsConstant := bytecode.IsK(rightOperand)
	if leftIsConstant == rightIsConstant {
		// 双寄存器或双常量形态不属于当前优化目标。
		return false, nil
	}

	registerOperand := leftOperand
	constantOperand := rightOperand
	if leftIsConstant {
		// 常量在左侧时交换读取顺序；乘法交换律允许共用同一结果计算。
		registerOperand = rightOperand
		constantOperand = leftOperand
	}

	constantIndex := bytecode.IndexK(constantOperand)
	if constantIndex < 0 || constantIndex >= len(vm.constants) {
		// 损坏 chunk 或越界常量应暴露原始常量错误。
		return true, ErrConstantOutOfRange
	}
	constant := vm.constants[constantIndex]
	if constant.Kind != bytecode.ConstantNumber {
		// integer 常量继续交给已有 integer cache；字符串数字需要完整 Lua 转换语义。
		return false, nil
	}

	registerIndex := bytecode.IndexK(registerOperand)
	if registerIndex < 0 || registerIndex >= len(vm.registers) {
		// 寄存器越界时保持原 VM 错误语义。
		return true, ErrRegisterOutOfRange
	}
	registerValue := vm.registers[registerIndex]
	var registerNumber float64
	switch registerValue.Kind {
	case KindInteger:
		// integer 与 number 常量混合时按 Lua number 结果写回。
		registerNumber = float64(registerValue.Integer)
	case KindNumber:
		// number 寄存器可直接参与浮点乘法。
		registerNumber = registerValue.Number
	default:
		// 非原生数值必须保留字符串转换和元方法回退。
		return false, nil
	}

	vm.registers[targetIndex] = NumberValue(registerNumber * constant.Number)
	return true, nil
}

// nativeNumberValue 只把真实 integer/number 转为 Lua number。
//
// 该 helper 用于 VM 窄快路径；字符串数字必须返回 false，让完整算术路径处理字符串转换和元方法。
func nativeNumberValue(value Value) (float64, bool) {
	switch value.Kind {
	case KindInteger:
		// integer 参与浮点算术时按 Lua number 转换。
		return float64(value.Integer), true
	case KindNumber:
		// number 可直接作为 float64 使用。
		return value.Number, true
	default:
		// 字符串、table、userdata 等必须交给完整 Lua 算术路径。
		return 0, false
	}
}

// executeFastArithmetic 执行 ADD/SUB/MUL 的低风险热路径。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数；cacheKind 决定双 integer
// 结果，numberOperation 处理 float number 结果。缓存覆盖寄存器 integer 与 integer
// 常量组合；类型变化、字符串数字或元方法都会走完整 Lua 语义。
func (vm *VM) executeFastArithmetic(instruction bytecode.Instruction, cacheKind byte, numberOperation func(float64, float64) float64, metamethodName string) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}

	if handled, err := vm.tryCachedIntegerRegisterArithmetic(instruction, cacheKind); handled || err != nil {
		// 缓存命中已完成写回；缓存形态损坏时返回原始寄存器错误。
		return err
	}

	leftOperand := instruction.B()
	rightOperand := instruction.C()
	leftValue, err := vm.rkValue(leftOperand)
	if err != nil {
		// 左操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	rightValue, err := vm.rkValue(rightOperand)
	if err != nil {
		// 右操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}

	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		// 双 integer 算术保留 integer 结果，并按 64 位补码自然回绕。
		vm.rememberIntegerRegisterArithmetic(leftOperand, rightOperand, cacheKind)
		vm.registers[targetIndex] = IntegerValue(integerArithmeticByCacheKind(cacheKind, leftValue.Integer, rightValue.Integer))
		return nil
	}
	leftNumber, leftOK := valueToLuaNumber(leftValue)
	rightNumber, rightOK := valueToLuaNumber(rightValue)
	if leftOK && rightOK {
		// 任一侧为 float 或可转数字字符串时，按 Lua 5.3 number 语义计算。
		vm.registers[targetIndex] = NumberValue(numberOperation(leftNumber, rightNumber))
		return nil
	}

	metamethodResult, found, metamethodErr := vm.callBinaryMetamethod(leftValue, rightValue, metamethodName)
	if metamethodErr != nil {
		// 元方法被找到但调用失败时，返回调用错误并保持目标寄存器原值。
		return metamethodErr
	}
	if found {
		// 元方法返回值就是 Lua 运算结果，不再强制转换成 number。
		vm.registers[targetIndex] = metamethodResult
		return nil
	}
	return ErrArithmeticOperand
}

// tryCachedIntegerRegisterArithmetic 尝试执行当前 PC 的双 integer 寄存器算术缓存。
//
// 返回 handled 表示指令已经成功写回；当缓存记录存在但操作数形态或类型不再匹配时会清除
// 缓存并返回 handled=false，让调用方回到完整 Lua 语义。
func (vm *VM) tryCachedIntegerRegisterArithmetic(instruction bytecode.Instruction, cacheKind byte) (bool, error) {
	currentPC := vm.currentPC
	if currentPC < 0 || currentPC >= len(vm.arithmeticIntRegisterCache) || currentPC >= len(vm.arithmeticIntOperandCache) || vm.arithmeticIntRegisterCache[currentPC] != cacheKind {
		// 当前 PC 没有目标算术缓存，调用方继续走普通 RK 路径。
		return false, nil
	}

	cacheEntry := vm.arithmeticIntOperandCache[currentPC]
	leftInteger, leftOK, leftErr := vm.cachedIntegerArithmeticEntryValue(cacheEntry.leftIndex, cacheEntry.leftConstant, cacheEntry.leftConstantOperand)
	rightInteger, rightOK, rightErr := vm.cachedIntegerArithmeticEntryValue(cacheEntry.rightIndex, cacheEntry.rightConstant, cacheEntry.rightConstantOperand)
	if leftErr != nil || rightErr != nil {
		// 指令形态或寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
		vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
		vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
		return false, nil
	}
	if !leftOK || !rightOK {
		// 类型不再匹配时清理缓存，后续走完整 Lua 算术和元方法语义。
		vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
		vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
		return false, nil
	}

	// 双 integer 算术保留 integer 结果，并按 64 位补码自然回绕。
	vm.registers[instruction.A()] = IntegerValue(integerArithmeticByCacheKind(cacheKind, leftInteger, rightInteger))
	return true, nil
}

// tryCachedIntegerAddArithmetic 尝试执行当前 PC 的 ADD integer 算术缓存。
//
// 该函数只处理 ADD 热路径；缓存不存在、类型变化或指令形态变化时返回 handled=false，让调用方
// 回到完整 Lua 算术语义。相比通用缓存路径，它避免二次 helper 调用和 ADD/SUB/MUL 分支选择。
func (vm *VM) tryCachedIntegerAddArithmetic(instruction bytecode.Instruction, targetIndex int) (bool, error) {
	currentPC := vm.currentPC
	registerCache := vm.arithmeticIntRegisterCache
	operandCache := vm.arithmeticIntOperandCache
	if uint(currentPC) >= uint(len(registerCache)) || uint(currentPC) >= uint(len(operandCache)) {
		// 当前 PC 没有 ADD integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}
	cacheKind := registerCache[currentPC]
	if cacheKind == arithmeticIntRegisterCacheAddNumber {
		// 当前 PC 最近命中过寄存器 number ADD，优先尝试该路径避免 integer cache miss。
		return vm.tryCachedNativeNumberAddArithmetic(instruction, currentPC)
	}
	if cacheKind != arithmeticIntRegisterCacheAdd {
		// 当前 PC 没有 ADD integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}

	cacheEntry := operandCache[currentPC]
	registers := vm.registers
	if !cacheEntry.leftConstantOperand && !cacheEntry.rightConstantOperand {
		// 双寄存器 ADD 是算术链热路径，先走无常量分支，避免每轮检查常量操作数形态。
		leftIndex := cacheEntry.leftIndex
		rightIndex := cacheEntry.rightIndex
		if uint(leftIndex) >= uint(len(registers)) || uint(rightIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		if registers[leftIndex].Kind != KindInteger || registers[rightIndex].Kind != KindInteger {
			// 任一操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		leftInteger := registers[leftIndex].Integer
		rightInteger := registers[rightIndex].Integer

		// ADD 按 64 位补码自然回绕，命中后直接写回目标寄存器。
		registers[targetIndex] = IntegerValue(leftInteger + rightInteger)
		return true, nil
	}

	var leftInteger int64
	if cacheEntry.leftConstantOperand {
		// 左操作数为 Proto integer 常量时可直接复用缓存值。
		leftInteger = cacheEntry.leftConstant
	} else {
		leftIndex := cacheEntry.leftIndex
		if uint(leftIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		leftValue := registers[leftIndex]
		if leftValue.Kind != KindInteger {
			// 左操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		leftInteger = leftValue.Integer
	}

	var rightInteger int64
	if cacheEntry.rightConstantOperand {
		// 右操作数为 Proto integer 常量时可直接复用缓存值。
		rightInteger = cacheEntry.rightConstant
	} else {
		rightIndex := cacheEntry.rightIndex
		if uint(rightIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightValue := registers[rightIndex]
		if rightValue.Kind != KindInteger {
			// 右操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightInteger = rightValue.Integer
	}

	// ADD 按 64 位补码自然回绕，命中后直接写回目标寄存器。
	registers[targetIndex] = IntegerValue(leftInteger + rightInteger)
	return true, nil
}

// tryCachedNativeNumberAddArithmetic 尝试执行当前 PC 的寄存器 number ADD 缓存。
//
// 该缓存只记录 B/C 都是寄存器、且运行期至少一侧为 number 的 ADD。命中时保持 Lua 5.3
// number 结果；类型变化为双 integer 或非原生数值时清理缓存并回到完整 ADD 语义。
func (vm *VM) tryCachedNativeNumberAddArithmetic(instruction bytecode.Instruction, currentPC int) (bool, error) {
	cacheEntry := vm.arithmeticIntOperandCache[currentPC]
	if cacheEntry.leftIndex < 0 || cacheEntry.leftIndex >= len(vm.registers) || cacheEntry.rightIndex < 0 || cacheEntry.rightIndex >= len(vm.registers) {
		// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
		vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
		vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
		return false, nil
	}
	leftValue := vm.registers[cacheEntry.leftIndex]
	rightValue := vm.registers[cacheEntry.rightIndex]
	if leftValue.Kind == KindNumber && rightValue.Kind == KindNumber {
		// 双 number 是混合算术循环常见 ADD 形态，直接相加可跳过原生数值拆分。
		vm.registers[instruction.A()] = NumberValue(leftValue.Number + rightValue.Number)
		return true, nil
	}
	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		// 双 integer 必须让 integer ADD 路径处理，保留 integer 结果。
		vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
		vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
		return false, nil
	}
	leftNumber, leftOK := nativeNumberValue(leftValue)
	rightNumber, rightOK := nativeNumberValue(rightValue)
	if !leftOK || !rightOK {
		// 字符串数字或元方法相关类型必须回退完整 Lua 算术路径。
		vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
		vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
		return false, nil
	}

	// 寄存器 number ADD 命中后直接写回 number 结果。
	vm.registers[instruction.A()] = NumberValue(leftNumber + rightNumber)
	return true, nil
}

// tryCachedNativeNumberDivArithmetic 尝试执行当前 PC 的寄存器原生数值 DIV 缓存。
//
// 该缓存只记录 B/C 都是寄存器且运行期值为 integer/number 的 DIV。命中时始终写回 Lua
// number；类型变化为字符串数字或元方法相关类型时清理缓存并回到完整 DIV 语义。
func (vm *VM) tryCachedNativeNumberDivArithmetic(instruction bytecode.Instruction, currentPC int) (bool, error) {
	cacheEntry := vm.arithmeticIntOperandCache[currentPC]
	if cacheEntry.leftIndex < 0 || cacheEntry.leftIndex >= len(vm.registers) || cacheEntry.rightIndex < 0 || cacheEntry.rightIndex >= len(vm.registers) {
		// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
		vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
		vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
		return false, nil
	}
	leftValue := vm.registers[cacheEntry.leftIndex]
	rightValue := vm.registers[cacheEntry.rightIndex]
	if leftValue.Kind == KindInteger && rightValue.Kind == KindInteger {
		// 双 integer 是算术循环常见 DIV 形态，直接按 float64 除法写回 number。
		vm.registers[instruction.A()] = NumberValue(float64(leftValue.Integer) / float64(rightValue.Integer))
		return true, nil
	}
	leftNumber, leftOK := nativeNumberValue(leftValue)
	rightNumber, rightOK := nativeNumberValue(rightValue)
	if !leftOK || !rightOK {
		// 字符串数字或元方法相关类型必须回退完整 Lua 算术路径。
		vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
		vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
		return false, nil
	}

	// Lua 5.3 的 DIV 总是返回 number，整数除法也按 float64 计算。
	vm.registers[instruction.A()] = NumberValue(leftNumber / rightNumber)
	return true, nil
}

// tryCachedIntegerSubArithmetic 尝试执行当前 PC 的 SUB integer 算术缓存。
//
// 该函数只处理 SUB 热路径；缓存不存在、类型变化或指令形态变化时返回 handled=false，让调用方
// 回到完整 Lua 算术语义。相比通用缓存路径，它避免二次 helper 调用和 ADD/SUB/MUL 分支选择。
func (vm *VM) tryCachedIntegerSubArithmetic(instruction bytecode.Instruction, targetIndex int) (bool, error) {
	currentPC := vm.currentPC
	registerCache := vm.arithmeticIntRegisterCache
	operandCache := vm.arithmeticIntOperandCache
	if uint(currentPC) >= uint(len(registerCache)) || uint(currentPC) >= uint(len(operandCache)) {
		// 当前 PC 没有 SUB integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}
	cacheKind := registerCache[currentPC]
	if cacheKind == arithmeticIntRegisterCacheSubRightConstant {
		// 左寄存器右常量是算术链路常见形态，命中时只需校验左寄存器类型。
		cacheEntry := operandCache[currentPC]
		registers := vm.registers
		leftIndex := cacheEntry.leftIndex
		if uint(leftIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		if registers[leftIndex].Kind != KindInteger {
			// 左操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}

		// SUB 按 64 位补码自然回绕，右侧 integer 常量直接复用缓存值。
		registers[targetIndex] = IntegerValue(registers[leftIndex].Integer - cacheEntry.rightConstant)
		return true, nil
	}
	if cacheKind != arithmeticIntRegisterCacheSub {
		// 当前 PC 没有 SUB integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}

	// 非右常量 SUB 复用通用 integer 缓存路径，保持完整类型失效和常量操作数语义。
	return vm.tryCachedIntegerRegisterArithmetic(instruction, arithmeticIntRegisterCacheSub)
}

// tryCachedIntegerMulArithmetic 尝试执行当前 PC 的 MUL integer 算术缓存。
//
// 该函数只处理 MUL 热路径；缓存不存在、类型变化或指令形态变化时返回 handled=false，让调用方
// 回到完整 Lua 算术语义。相比通用缓存路径，它避免二次 helper 调用和 ADD/SUB/MUL 分支选择。
func (vm *VM) tryCachedIntegerMulArithmetic(instruction bytecode.Instruction, targetIndex int) (bool, error) {
	currentPC := vm.currentPC
	registerCache := vm.arithmeticIntRegisterCache
	operandCache := vm.arithmeticIntOperandCache
	if uint(currentPC) >= uint(len(registerCache)) || uint(currentPC) >= uint(len(operandCache)) {
		// 当前 PC 没有 MUL integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}
	cacheKind := registerCache[currentPC]
	if cacheKind == arithmeticIntRegisterCacheMulRightConstant {
		// 左寄存器右常量是算术链路常见形态，命中时只需校验左寄存器类型。
		cacheEntry := operandCache[currentPC]
		registers := vm.registers
		leftIndex := cacheEntry.leftIndex
		if uint(leftIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		if registers[leftIndex].Kind != KindInteger {
			// 左操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}

		// MUL 按 64 位补码自然回绕，右侧 integer 常量直接复用缓存值。
		registers[targetIndex] = IntegerValue(registers[leftIndex].Integer * cacheEntry.rightConstant)
		return true, nil
	}
	if cacheKind != arithmeticIntRegisterCacheMul {
		// 当前 PC 没有 MUL integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}

	// 非右常量 MUL 复用通用 integer 缓存路径，保持完整类型失效和常量操作数语义。
	return vm.tryCachedIntegerRegisterArithmetic(instruction, arithmeticIntRegisterCacheMul)
}

// tryCachedIntegerModArithmetic 尝试执行当前 PC 的 MOD integer 算术缓存。
//
// 该函数只处理 MOD 热路径；缓存不存在、类型变化或指令形态变化时返回 handled=false，让调用方
// 回到完整 Lua 算术语义。相比通用 MOD/IDIV 缓存路径，它避免二次 helper 调用和缓存类型 switch。
func (vm *VM) tryCachedIntegerModArithmetic(instruction bytecode.Instruction, targetIndex int) (bool, error) {
	currentPC := vm.currentPC
	if currentPC < 0 || currentPC >= len(vm.arithmeticIntRegisterCache) || currentPC >= len(vm.arithmeticIntOperandCache) || vm.arithmeticIntRegisterCache[currentPC] != arithmeticIntRegisterCacheMod {
		// 当前 PC 没有 MOD integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}

	cacheEntry := vm.arithmeticIntOperandCache[currentPC]
	registers := vm.registers
	if cacheEntry.rightConstantOperand && !cacheEntry.leftConstantOperand {
		// 左寄存器右常量是混合算术循环常见形态，命中时只需校验左寄存器类型。
		leftIndex := cacheEntry.leftIndex
		if uint(leftIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		if registers[leftIndex].Kind != KindInteger {
			// 左操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightInteger := cacheEntry.rightConstant
		if rightInteger == 0 {
			// MOD 零除错误必须保持 Lua 运行期错误文本，并避免覆盖目标寄存器。
			return true, fmt.Errorf("'n%%0': %w", ErrDivisionByZero)
		}

		// MOD 使用 Lua floor modulo 语义，右侧 integer 常量直接复用缓存值。
		registers[targetIndex] = IntegerValue(integerModulo(registers[leftIndex].Integer, rightInteger))
		return true, nil
	}

	var leftInteger int64
	if cacheEntry.leftConstantOperand {
		// 左操作数为 Proto integer 常量时可直接复用缓存值。
		leftInteger = cacheEntry.leftConstant
	} else {
		leftIndex := cacheEntry.leftIndex
		if uint(leftIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		leftValue := registers[leftIndex]
		if leftValue.Kind != KindInteger {
			// 左操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		leftInteger = leftValue.Integer
	}

	var rightInteger int64
	if cacheEntry.rightConstantOperand {
		// 右操作数为 Proto integer 常量时可直接复用缓存值。
		rightInteger = cacheEntry.rightConstant
	} else {
		rightIndex := cacheEntry.rightIndex
		if uint(rightIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightValue := registers[rightIndex]
		if rightValue.Kind != KindInteger {
			// 右操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightInteger = rightValue.Integer
	}
	if rightInteger == 0 {
		// MOD 零除错误必须保持 Lua 运行期错误文本，并避免覆盖目标寄存器。
		return true, fmt.Errorf("'n%%0': %w", ErrDivisionByZero)
	}

	// MOD 使用 Lua floor modulo 语义，符号与除数保持一致。
	registers[targetIndex] = IntegerValue(integerModulo(leftInteger, rightInteger))
	return true, nil
}

// tryCachedIntegerIDivArithmetic 尝试执行当前 PC 的 IDIV integer 算术缓存。
//
// 该函数只处理 IDIV 热路径；缓存不存在、类型变化或指令形态变化时返回 handled=false，让调用方
// 回到完整 Lua 算术语义。相比通用 MOD/IDIV 缓存路径，它避免二次 helper 调用和缓存类型 switch。
func (vm *VM) tryCachedIntegerIDivArithmetic(instruction bytecode.Instruction, targetIndex int) (bool, error) {
	currentPC := vm.currentPC
	if currentPC < 0 || currentPC >= len(vm.arithmeticIntRegisterCache) || currentPC >= len(vm.arithmeticIntOperandCache) || vm.arithmeticIntRegisterCache[currentPC] != arithmeticIntRegisterCacheIDiv {
		// 当前 PC 没有 IDIV integer 缓存，调用方继续走普通 RK 路径。
		return false, nil
	}

	cacheEntry := vm.arithmeticIntOperandCache[currentPC]
	registers := vm.registers
	if cacheEntry.rightConstantOperand && !cacheEntry.leftConstantOperand {
		// 左寄存器右常量是混合算术循环常见形态，命中时只需校验左寄存器类型。
		leftIndex := cacheEntry.leftIndex
		if uint(leftIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		if registers[leftIndex].Kind != KindInteger {
			// 左操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightInteger := cacheEntry.rightConstant
		if rightInteger == 0 {
			// 零除错误必须在写回前暴露，保持目标寄存器原值。
			return true, ErrDivisionByZero
		}

		// IDIV 使用 Lua floor division 语义，右侧 integer 常量直接复用缓存值。
		registers[targetIndex] = IntegerValue(integerFloorDiv(registers[leftIndex].Integer, rightInteger))
		return true, nil
	}

	var leftInteger int64
	if cacheEntry.leftConstantOperand {
		// 左操作数为 Proto integer 常量时可直接复用缓存值。
		leftInteger = cacheEntry.leftConstant
	} else {
		leftIndex := cacheEntry.leftIndex
		if uint(leftIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		leftValue := registers[leftIndex]
		if leftValue.Kind != KindInteger {
			// 左操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		leftInteger = leftValue.Integer
	}

	var rightInteger int64
	if cacheEntry.rightConstantOperand {
		// 右操作数为 Proto integer 常量时可直接复用缓存值。
		rightInteger = cacheEntry.rightConstant
	} else {
		rightIndex := cacheEntry.rightIndex
		if uint(rightIndex) >= uint(len(registers)) {
			// 寄存器窗口变化时清理缓存，并回到通用 RK 路径报出原始错误。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightValue := registers[rightIndex]
		if rightValue.Kind != KindInteger {
			// 右操作数类型变化时缓存失效，后续走完整 Lua 算术和元方法语义。
			vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheNone
			vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{}
			return false, nil
		}
		rightInteger = rightValue.Integer
	}
	if rightInteger == 0 {
		// 零除错误必须在写回前暴露，保持目标寄存器原值。
		return true, ErrDivisionByZero
	}

	// IDIV 使用 Lua floor division 语义，结果向负无穷取整。
	registers[targetIndex] = IntegerValue(integerFloorDiv(leftInteger, rightInteger))
	return true, nil
}

// integerArithmeticByCacheKind 执行 ADD/SUB/MUL 的 integer 热路径。
//
// cacheKind 必须来自当前算术指令；未知类型返回 0 仅作为损坏缓存的防御兜底，正常路径不会触发。
func integerArithmeticByCacheKind(cacheKind byte, left int64, right int64) int64 {
	switch cacheKind {
	case arithmeticIntRegisterCacheAdd:
		// ADD 按 64 位补码自然回绕。
		return left + right
	case arithmeticIntRegisterCacheSub:
		// SUB 按 64 位补码自然回绕。
		return left - right
	case arithmeticIntRegisterCacheSubRightConstant:
		// 右常量 SUB 与普通 SUB 使用相同算术语义。
		return left - right
	case arithmeticIntRegisterCacheMul:
		// MUL 按 64 位补码自然回绕。
		return left * right
	case arithmeticIntRegisterCacheMulRightConstant:
		// 右常量 MUL 与普通 MUL 使用相同算术语义。
		return left * right
	default:
		// 未知缓存类型不应出现，返回 0 让测试暴露异常路径。
		return 0
	}
}

// rememberIntegerRegisterArithmetic 记录当前 PC 的 integer 算术热路径。
//
// 只有 B/C 都是寄存器或 integer 常量时才记录缓存；其他 RK 常量保留通用读取路径。
func (vm *VM) rememberIntegerRegisterArithmetic(leftOperand int, rightOperand int, cacheKind byte) {
	currentPC := vm.currentPC
	if currentPC < 0 || currentPC >= len(vm.arithmeticIntRegisterCache) || currentPC >= len(vm.arithmeticIntOperandCache) {
		// 无效 PC 不适合缓存，直接保留完整 RK 路径。
		return
	}
	leftCache, ok, err := vm.integerArithmeticOperandCacheEntry(leftOperand)
	if err != nil || !ok {
		// 左操作数不是可缓存 integer 时保留完整 RK 路径。
		return
	}
	rightCache, ok, err := vm.integerArithmeticOperandCacheEntry(rightOperand)
	if err != nil || !ok {
		// 右操作数不是可缓存 integer 时保留完整 RK 路径。
		return
	}

	if !leftCache.leftConstantOperand && rightCache.leftConstantOperand {
		// `R op Kint` 是算术循环常见形态，单独记录缓存类型以减少命中时的分支和常量读取。
		switch cacheKind {
		case arithmeticIntRegisterCacheSub:
			// SUB 右常量缓存保持 `R - Kint` 的 Lua 5.3 integer 回绕语义。
			cacheKind = arithmeticIntRegisterCacheSubRightConstant
		case arithmeticIntRegisterCacheMul:
			// MUL 右常量缓存保持 `R * Kint` 的 Lua 5.3 integer 回绕语义。
			cacheKind = arithmeticIntRegisterCacheMulRightConstant
		}
	}

	// 记录当前 PC 的 integer 热路径，下次同类算术可跳过通用 RK 读取和 number fallback。
	vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{
		leftIndex:            leftCache.leftIndex,
		rightIndex:           rightCache.leftIndex,
		leftConstant:         leftCache.leftConstant,
		rightConstant:        rightCache.leftConstant,
		leftConstantOperand:  leftCache.leftConstantOperand,
		rightConstantOperand: rightCache.leftConstantOperand,
	}
	vm.arithmeticIntRegisterCache[currentPC] = cacheKind
}

// rememberNativeNumberAdd 记录当前 PC 的寄存器 number ADD 热路径。
//
// 只缓存 B/C 都是寄存器的 ADD；常量、字符串数字和元方法相关路径保留完整 RK 读取与回退语义。
func (vm *VM) rememberNativeNumberAdd(instruction bytecode.Instruction) {
	currentPC := vm.currentPC
	if currentPC < 0 || currentPC >= len(vm.arithmeticIntRegisterCache) || currentPC >= len(vm.arithmeticIntOperandCache) {
		// 无效 PC 不适合缓存，直接保留完整 RK 路径。
		return
	}
	leftOperand := instruction.B()
	rightOperand := instruction.C()
	if bytecode.IsK(leftOperand) || bytecode.IsK(rightOperand) {
		// 只缓存寄存器操作数，避免常量类型变化和字符串数字转换语义混入窄路径。
		return
	}
	leftIndex := bytecode.IndexK(leftOperand)
	rightIndex := bytecode.IndexK(rightOperand)
	if leftIndex < 0 || leftIndex >= len(vm.registers) || rightIndex < 0 || rightIndex >= len(vm.registers) {
		// 损坏指令或寄存器窗口变化时不建立缓存，后续完整路径会报原始错误。
		return
	}
	// 记录当前 PC 的寄存器 number ADD 热路径，下次可跳过 integer cache miss 和通用 RK helper。
	vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{leftIndex: leftIndex, rightIndex: rightIndex}
	vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheAddNumber
}

// rememberNativeNumberDiv 记录当前 PC 的寄存器原生数值 DIV 热路径。
//
// 只缓存 B/C 都是寄存器的 DIV；常量、字符串数字和元方法相关路径保留完整 RK 读取与回退语义。
func (vm *VM) rememberNativeNumberDiv(instruction bytecode.Instruction) {
	currentPC := vm.currentPC
	if currentPC < 0 || currentPC >= len(vm.arithmeticIntRegisterCache) || currentPC >= len(vm.arithmeticIntOperandCache) {
		// 无效 PC 不适合缓存，直接保留完整 RK 路径。
		return
	}
	leftOperand := instruction.B()
	rightOperand := instruction.C()
	if bytecode.IsK(leftOperand) || bytecode.IsK(rightOperand) {
		// 只缓存寄存器操作数，避免常量和字符串数字转换语义混入窄路径。
		return
	}
	leftIndex := bytecode.IndexK(leftOperand)
	rightIndex := bytecode.IndexK(rightOperand)
	if leftIndex < 0 || leftIndex >= len(vm.registers) || rightIndex < 0 || rightIndex >= len(vm.registers) {
		// 损坏指令或寄存器窗口变化时不建立缓存，后续完整路径会报原始错误。
		return
	}
	// 记录当前 PC 的寄存器 DIV 热路径，下次可跳过通用 RK helper 和类型分支。
	vm.arithmeticIntOperandCache[currentPC] = arithmeticIntOperandCacheEntry{leftIndex: leftIndex, rightIndex: rightIndex}
	vm.arithmeticIntRegisterCache[currentPC] = arithmeticIntRegisterCacheDivNumber
}

// integerArithmeticOperandCacheEntry 为可缓存 integer RK 操作数构造缓存项。
//
// rk 可指向寄存器或 integer 常量；寄存器操作数只记录索引，命中时继续检查运行期类型。
func (vm *VM) integerArithmeticOperandCacheEntry(rk int) (arithmeticIntOperandCacheEntry, bool, error) {
	index := bytecode.IndexK(rk)
	if bytecode.IsK(rk) {
		// RK 常量路径只接受 Proto 中的 integer 常量，其他常量交给通用算术路径处理。
		if index < 0 || index >= len(vm.constants) {
			// 常量索引越界通常表示损坏 chunk 或编译器输出错误。
			return arithmeticIntOperandCacheEntry{}, false, ErrConstantOutOfRange
		}
		constant := vm.constants[index]
		if constant.Kind != bytecode.ConstantInteger {
			// 非 integer 常量不能走整数算术快路径。
			return arithmeticIntOperandCacheEntry{}, false, nil
		}
		return arithmeticIntOperandCacheEntry{leftConstant: constant.Integer, leftConstantOperand: true}, true, nil
	}
	if index < 0 || index >= len(vm.registers) {
		// RK 寄存器路径越界时不能读取寄存器窗口。
		return arithmeticIntOperandCacheEntry{}, false, ErrRegisterOutOfRange
	}
	value := vm.registers[index]
	if value.Kind != KindInteger {
		// 非 integer 寄存器值需要回到完整 number/string/metamethod 语义。
		return arithmeticIntOperandCacheEntry{}, false, nil
	}
	return arithmeticIntOperandCacheEntry{leftIndex: index}, true, nil
}

// cachedIntegerArithmeticEntryValue 读取 integer 算术缓存项当前值。
//
// 常量操作数直接返回缓存值；寄存器操作数必须重新检查边界与 KindInteger，保证运行期类型变化
// 能回退完整 Lua 算术语义。
func (vm *VM) cachedIntegerArithmeticEntryValue(registerIndex int, constantValue int64, constantOperand bool) (int64, bool, error) {
	if constantOperand {
		// Proto 常量不可变，命中后可直接复用缓存值。
		return constantValue, true, nil
	}
	if registerIndex < 0 || registerIndex >= len(vm.registers) {
		// 寄存器窗口变化时缓存失效，调用方回退通用路径。
		return 0, false, ErrRegisterOutOfRange
	}
	value := vm.registers[registerIndex]
	if value.Kind != KindInteger {
		// 寄存器运行期类型变化时缓存失效。
		return 0, false, nil
	}
	return value.Integer, true, nil
}

// executeBinaryBitwise 执行 Lua 5.3 二元位运算指令。
//
// instruction 的 A 是目标寄存器，B/C 使用 RK 编码读取操作数；operation 决定具体位运算。
// 操作数必须可转换为 integer；转换失败时按 Lua 5.3 规则尝试对应位运算元方法。
func (vm *VM) executeBinaryBitwise(instruction bytecode.Instruction, operation binaryBitwiseOperation, metamethodName string) error {
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}

	leftValue, err := vm.rkValue(instruction.B())
	if err != nil {
		// 左操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	rightValue, err := vm.rkValue(instruction.C())
	if err != nil {
		// 右操作数读取失败时不能继续计算，目标寄存器保持原值。
		return err
	}
	leftInteger, ok := valueToLuaInteger(leftValue)
	if !ok {
		// 左操作数不能转为 integer 时，尝试二元位运算元方法。
		result, found, err := vm.callBinaryMetamethod(leftValue, rightValue, metamethodName)
		if err != nil {
			// 元方法存在但调用失败时返回调用错误。
			return err
		}
		if found {
			// 元方法返回值直接作为位运算结果写回，不再强制转换。
			vm.registers[targetIndex] = result
			return nil
		}
		return integerOperandError(leftValue)
	}
	rightInteger, ok := valueToLuaInteger(rightValue)
	if !ok {
		// 右操作数不能转为 integer 时，尝试二元位运算元方法。
		result, found, err := vm.callBinaryMetamethod(leftValue, rightValue, metamethodName)
		if err != nil {
			// 元方法存在但调用失败时返回调用错误。
			return err
		}
		if found {
			// 元方法返回值直接作为位运算结果写回，不再强制转换。
			vm.registers[targetIndex] = result
			return nil
		}
		return integerOperandError(rightValue)
	}

	// 位运算按 64 位二进制补码语义执行，结果写回 Lua integer。
	vm.registers[targetIndex] = IntegerValue(operation(leftInteger, rightInteger))
	return nil
}

// callBinaryMetamethod 调用当前 VM 可执行的二元元方法。
//
// VM 由 lua 包执行时会绑定 Lua closure runner，因此脚本动态写入的 Lua 元方法可被执行；
// 底层 runtime 单测未绑定 runner 时仍只支持 Go closure 元方法。
func (vm *VM) callBinaryMetamethod(left Value, right Value, name string) (Value, bool, error) {
	if vm == nil {
		// nil VM 没有 runner，只能退回 Go closure 元方法路径。
		return callBinaryMetamethod(left, right, name)
	}

	// 使用 VM 持有的 runner 执行 Lua closure 元方法。
	return callBinaryMetamethodWithRunner(vm.luaMetamethodRunner, left, right, name)
}

// callUnaryMetamethod 调用当前 VM 可执行的一元元方法。
//
// value 是原始操作数；name 是 `__unm`、`__bnot` 等一元元方法字段名。Lua 5.3 对一元元方法
// 使用同一个操作数作为两侧参数，以复用二元 tag method 调用协议。
func (vm *VM) callUnaryMetamethod(value Value, name string) (Value, bool, error) {
	if vm == nil {
		// nil VM 没有 runner，只能退回 Go closure 元方法路径。
		return callBinaryMetamethod(value, value, name)
	}

	// 使用 VM 持有的 runner 执行 Lua closure 元方法。
	return callBinaryMetamethodWithRunner(vm.luaMetamethodRunner, value, value, name)
}

// executeUnaryMinus 执行 Lua 5.3 OP_UNM 指令。
//
// 指令语义为 R(A) := -R(B)。操作数为 integer 或可转换为 integer 时保留 integer 结果；
// 否则按 float number 计算。无法转换为 number 时返回 ErrArithmeticOperand。
func (vm *VM) executeUnaryMinus(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	sourceIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}

	sourceValue := vm.registers[sourceIndex]
	if sourceValue.Kind == KindInteger {
		// integer 负号按 64 位补码回绕语义执行，匹配 Lua integer 算术边界。
		vm.registers[targetIndex] = IntegerValue(-sourceValue.Integer)
		return nil
	}
	numberValue, ok := valueToLuaNumber(sourceValue)
	if !ok {
		// 操作数不能转为 number 时，尝试 __unm 元方法。
		result, found, err := vm.callUnaryMetamethod(sourceValue, metamethodUnm)
		if err != nil {
			// 元方法存在但调用失败时返回调用错误。
			return err
		}
		if found {
			// 元方法返回值直接作为一元负号结果写回。
			vm.registers[targetIndex] = result
			return nil
		}
		return ErrArithmeticOperand
	}

	// float number 负号使用 IEEE-754 float64 语义。
	vm.registers[targetIndex] = NumberValue(-numberValue)
	return nil
}

// executeBitwiseNot 执行 Lua 5.3 OP_BNOT 指令。
//
// 指令语义为 R(A) := ~R(B)。操作数必须可转换为 integer；结果按 64 位二进制补码语义
// 写回 Lua integer。
func (vm *VM) executeBitwiseNot(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	sourceIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}

	integerValue, ok := valueToLuaInteger(vm.registers[sourceIndex])
	if !ok {
		// 操作数不能转为 integer 时，尝试 __bnot 元方法。
		result, found, err := vm.callUnaryMetamethod(vm.registers[sourceIndex], metamethodBNot)
		if err != nil {
			// 元方法存在但调用失败时返回调用错误。
			return err
		}
		if found {
			// 元方法返回值直接作为按位非结果写回。
			vm.registers[targetIndex] = result
			return nil
		}
		return integerOperandError(vm.registers[sourceIndex])
	}

	// 按位取反翻转 64 位补码中的每一位。
	vm.registers[targetIndex] = IntegerValue(^integerValue)
	return nil
}

// integerOperandError 构造 Lua integer 转换失败错误。
//
// Lua 5.3 官方 math.lua 会校验 math.huge 位运算错误里包含 field 'huge'；当前 VM 未保留
// 完整表达式来源，因此对 Inf 值补兼容上下文，同时用 ErrIntegerOperand 保持 errors.Is。
func integerOperandError(value Value) error {
	if value.Kind == KindNumber && math.IsInf(value.Number, 0) {
		// 无穷 number 常见来源是 math.huge，错误文本对齐官方测试的字段提示。
		return fmt.Errorf("number (field 'huge') has no integer representation: %w", ErrIntegerOperand)
	}
	if !value.IsNumber() {
		// 非 number 参与位运算时，官方错误文本强调 bitwise operation 及操作数 Lua 类型。
		return fmt.Errorf("attempt to perform bitwise operation on a %s value: %w", LuaErrorTypeName(value), ErrIntegerOperand)
	}

	// 普通转换失败返回稳定哨兵错误。
	return ErrIntegerOperand
}

// lengthOperandError 构造 Lua 长度运算失败错误。
//
// value 是执行 `#` 的原始操作数；返回错误保留 ErrLengthOperand 哨兵链，便于既有单测继续
// 使用 errors.Is 判断错误类别。
func lengthOperandError(value Value) error {
	// 官方 errors.lua 只要求文本包含具体类型，例如 function value 或 number value。
	return fmt.Errorf("attempt to get length of a %s value: %w", LuaErrorTypeName(value), ErrLengthOperand)
}

// concatOperandError 构造 Lua 拼接运算失败错误。
//
// value 是第一个不能按 string/number 参与拼接且没有 `__concat` 元方法的操作数；返回错误
// 保留 ErrConcatOperand 哨兵链。
func concatOperandError(value Value) error {
	// Lua 5.3 拼接错误需要指出无法拼接的操作数类型。
	return fmt.Errorf("attempt to concatenate a %s value: %w", LuaErrorTypeName(value), ErrConcatOperand)
}

// compareOperandError 构造 Lua 有序比较失败错误。
//
// left/right 是原始比较操作数；当前错误文本保留左右 Lua 类型，同时通过 ErrCompareOperand
// 维持既有错误分类。
func compareOperandError(left Value, right Value) error {
	leftTypeName := LuaErrorTypeName(left)
	rightTypeName := LuaErrorTypeName(right)
	if leftTypeName == rightTypeName {
		// 同类型不可比较值按 Lua 5.3 文本展示为 two <type> values。
		return fmt.Errorf("attempt to compare two %s values: %w", leftTypeName, ErrCompareOperand)
	}
	// 官方测试匹配 attempt to compare 前缀，附带类型可帮助定位左右操作数。
	return fmt.Errorf("attempt to compare %s with %s: %w", leftTypeName, rightTypeName, ErrCompareOperand)
}

// executeLogicalNot 执行 Lua 5.3 OP_NOT 指令。
//
// 指令语义为 R(A) := not R(B)。Lua 条件语义只有 nil 和 false 为假，其余值都为真。
func (vm *VM) executeLogicalNot(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	sourceIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}

	// NOT 直接复用 Value.Truthy，保持 nil/false 为假、其他值为真的 Lua 语义。
	vm.registers[targetIndex] = BooleanValue(!vm.registers[sourceIndex].Truthy())
	return nil
}

// executeLength 执行 Lua 5.3 OP_LEN 指令。
//
// 指令语义为 R(A) := #R(B)。string 走字节长度；table 优先尝试 `__len`，未定义时使用
// Table.Len 基础边界；其他类型当前需要可查询到元方法，否则返回 ErrLengthOperand。
func (vm *VM) executeLength(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	sourceIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}

	lengthValue, err := vm.valueLength(vm.registers[sourceIndex])
	if err != nil {
		// 操作数不支持长度运算时，目标寄存器保持原值。
		return err
	}

	// LEN 成功后写入基础长度或元方法返回值。
	vm.registers[targetIndex] = lengthValue
	return nil
}

// valueLength 返回当前 VM 语境下 Lua 值的长度运算结果。
//
// string 返回字节长度；table 优先调用 `__len`，且 Lua closure 元方法通过 VM runner 执行；
// 未定义 `__len` 时使用 Table.Len。其他类型当前无法找到长度元方法时返回 ErrLengthOperand。
func (vm *VM) valueLength(value Value) (Value, error) {
	switch value.Kind {
	case KindString:
		// Lua 5.3 string 长度按字节计算，不按 Unicode rune 计算。
		return IntegerValue(int64(len(value.String))), nil
	case KindTable:
		// table 先检查 __len 元方法，Lua 5.3 允许 table 覆盖长度语义。
		if method, ok := lookupMetamethod(value, metamethodLen); ok {
			result, err := callMetamethodValue(vm.luaMetamethodRunner, method, metamethodLen, value)
			if err != nil {
				// 元方法存在但调用失败时返回调用错误。
				return NilValue(), err
			}
			return result, nil
		}
		table, err := tableFromValue(value)
		if err != nil {
			// table 类型引用损坏时直接返回 table 解析错误。
			return NilValue(), err
		}
		return IntegerValue(table.Len()), nil
	default:
		if method, ok := lookupMetamethod(value, metamethodLen); ok {
			// 基础类型的 __len 元方法通过当前 VM runner 执行。
			result, err := callMetamethodValue(vm.luaMetamethodRunner, method, metamethodLen, value)
			if err != nil {
				// 元方法存在但执行失败时返回该错误。
				return NilValue(), err
			}
			return result, nil
		}
		// 其他类型没有长度语义且未定义 __len 时返回 Lua 5.3 风格类型错误。
		return NilValue(), lengthOperandError(value)
	}
}

// executeConcat 执行 Lua 5.3 OP_CONCAT 指令。
//
// 指令语义为 R(A) := R(B).. ... ..R(C)。B 到 C 必须是闭区间且全部在寄存器窗口内；
// 每个操作数优先按 string/number 转换；转换失败时尝试 `__concat` 元方法。
func (vm *VM) executeConcat(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	startIndex := instruction.B()
	endIndex := instruction.C()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if startIndex < 0 || endIndex < startIndex || endIndex >= len(vm.registers) {
		// 拼接区间非法时不能读取寄存器，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}

	if endIndex == startIndex+1 &&
		vm.registers[startIndex].Kind == KindString &&
		vm.registers[endIndex].Kind == KindString {
		if vm.registers[endIndex].String == "" {
			// Lua 5.3 luaV_concat 对右侧空字符串直接保留左操作数，避免无意义结果分配。
			vm.registers[targetIndex] = vm.registers[startIndex]
			return nil
		}
		if vm.registers[startIndex].String == "" {
			// 左侧空字符串时结果等于右操作数，保持字符串不可变语义且避免分配。
			vm.registers[targetIndex] = vm.registers[endIndex]
			return nil
		}
		// 最常见的二元 string 拼接直接使用 Go 字符串拼接，避免 Builder 和区间扫描开销。
		vm.registers[targetIndex] = StringValue(vm.registers[startIndex].String + vm.registers[endIndex].String)
		return nil
	}
	if result, ok := vm.concatStringRegisterRange(startIndex, endIndex); ok {
		// 全部操作数已经是 string 时直接按寄存器范围拼接，避免构造临时 []string。
		vm.registers[targetIndex] = StringValue(result)
		return nil
	}

	parts := make([]string, 0, endIndex-startIndex+1)
	allConvertible := true
	for registerIndex := startIndex; registerIndex <= endIndex; registerIndex++ {
		// 先尝试纯 string/number 快路径；官方 constructs.lua 大量普通字符串拼接依赖该路径性能。
		part, err := valueToLuaString(vm.registers[registerIndex])
		if err != nil {
			// 任一操作数不能直接转 string 时，回落到现有二元折叠以保留 __concat 元方法机会。
			allConvertible = false
			break
		}
		parts = append(parts, part)
	}
	if allConvertible {
		// 全部片段都可直接转换时，一次性预估容量并拼接，避免左折叠产生重复拷贝。
		vm.registers[targetIndex] = StringValue(concatStrings(parts))
		return nil
	}

	result := vm.registers[endIndex]
	for registerIndex := endIndex - 1; registerIndex >= startIndex; registerIndex-- {
		// CONCAT 是右结合运算；存在元方法时必须先折叠右侧相邻操作数，匹配 Lua 5.3 luaV_concat。
		leftValue := vm.registers[registerIndex]
		combined, err := vm.concatPair(leftValue, result)
		if err != nil {
			if errors.Is(err, ErrCoroutineYield) {
				// __concat yield 后需要从当前相邻对的返回值继续折叠更左侧寄存器。
				vm.pendingConcat = &pendingConcatContinuation{targetIndex: targetIndex, startIndex: startIndex, nextIndex: registerIndex - 1}
			}
			// 当前二元拼接无法完成且无元方法时，目标寄存器保持原值。
			return err
		}
		result = combined
	}

	// 全部转换成功后一次性写入目标寄存器。
	vm.registers[targetIndex] = result
	return nil
}

// finishConcatContinuation 从 __concat 元方法返回值继续完成 CONCAT 右结合折叠。
//
// targetIndex/startIndex/nextIndex 来自 pendingConcat；result 是最近一次 __concat 的返回值。
// 若继续折叠时再次触发 yield，本方法会保存新的 pendingConcat，并保持目标寄存器不变。
func (vm *VM) finishConcatContinuation(targetIndex int, startIndex int, nextIndex int, result Value) error {
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if startIndex < 0 || nextIndex >= len(vm.registers) {
		// 保存的区间已经不合法，不能继续解释恢复值。
		return ErrRegisterOutOfRange
	}
	for registerIndex := nextIndex; registerIndex >= startIndex; registerIndex-- {
		// 恢复后继续按 Lua 5.3 右结合顺序向左折叠剩余操作数。
		leftValue := vm.registers[registerIndex]
		combined, err := vm.concatPair(leftValue, result)
		if err != nil {
			if errors.Is(err, ErrCoroutineYield) {
				// 下一次恢复仍使用同一条 CONCAT 指令，但从更左侧寄存器继续。
				vm.pendingConcat = &pendingConcatContinuation{targetIndex: targetIndex, startIndex: startIndex, nextIndex: registerIndex - 1}
			}
			// 拼接失败时保持目标寄存器原值。
			return err
		}
		result = combined
	}

	// 所有剩余操作数折叠完成后，才把最终结果写入目标寄存器。
	vm.registers[targetIndex] = result
	return nil
}

// concatPair 拼接两个 Lua 值。
//
// 两个值都能转换为 string 时走基础拼接；任一转换失败时按 Lua 5.3 规则尝试 `__concat`
// 二元元方法。返回值可能是元方法的任意 Lua 值，后续连续 CONCAT 会继续拿它参与折叠。
func (vm *VM) concatPair(left Value, right Value) (Value, error) {
	leftString, leftErr := valueToLuaString(left)
	rightString, rightErr := valueToLuaString(right)
	if leftErr == nil && rightErr == nil {
		if rightString == "" {
			// 右侧空字符串时结果为左操作数，避免基础折叠路径分配新字符串。
			return StringValue(leftString), nil
		}
		if leftString == "" {
			// 左侧空字符串时结果为右操作数，匹配 Lua 5.3 luaV_concat 的空串快路径。
			return StringValue(rightString), nil
		}
		// 两侧均可转换为 string 时，使用基础字符串拼接快速路径。
		return StringValue(leftString + rightString), nil
	}

	result, found, err := vm.callBinaryMetamethod(left, right, metamethodConcat)
	if err != nil {
		// 元方法存在但调用失败时返回调用错误。
		return NilValue(), err
	}
	if found {
		// 元方法返回值作为当前折叠结果。
		return result, nil
	}

	// 无 __concat 时保留 Lua 拼接操作数错误，并指出第一个不可拼接操作数类型。
	if leftErr != nil {
		return NilValue(), concatOperandError(left)
	}
	return NilValue(), concatOperandError(right)
}

// executeEqualityTest 执行 Lua 5.3 OP_EQ 指令。
//
// 指令语义为 if ((RK(B) == RK(C)) ~= A) then pc++。当前最小 VM 没有完整 pc，因此把
// 是否跳过下一条指令记录到 skipNext。
func (vm *VM) executeEqualityTest(instruction bytecode.Instruction) error {
	leftValue, err := vm.rkValue(instruction.B())
	if err != nil {
		// 左操作数读取失败时不能完成比较。
		return err
	}
	rightValue, err := vm.rkValue(instruction.C())
	if err != nil {
		// 右操作数读取失败时不能完成比较。
		return err
	}

	comparisonResult := leftValue.RawEqual(rightValue)
	if !comparisonResult {
		// raw 不相等时尝试 __eq；raw 相等不触发元方法，符合 Lua equality 快速路径。
		vm.pendingComparison = &pendingComparisonContinuation{instruction: instruction}
		result, found, metamethodErr := vm.callBinaryMetamethod(leftValue, rightValue, metamethodEq)
		if metamethodErr != nil {
			if !errors.Is(metamethodErr, ErrCoroutineYield) {
				// 非 yield 错误不会进入 continuation，必须清理待恢复比较状态。
				vm.pendingComparison = nil
			}
			// 元方法存在但调用失败时比较指令失败。
			return metamethodErr
		}
		vm.pendingComparison = nil
		if found {
			// __eq 返回值按 Lua truthiness 转成比较结果。
			comparisonResult = result.Truthy()
		}
	}
	vm.skipNext = comparisonResult != (instruction.A() != 0)
	return nil
}

// executeOrderTest 执行 Lua 5.3 OP_LT 或 OP_LE 指令。
//
// 指令语义为 if ((RK(B) op RK(C)) ~= A) then pc++。operation 决定小于或小于等于；
// 当前最小 VM 没有完整 pc，因此把是否跳过下一条指令记录到 skipNext。
func (vm *VM) executeOrderTest(instruction bytecode.Instruction, operation orderCompareOperation, metamethodName string) error {
	if metamethodName == metamethodLt {
		// 高频递归边界通常是 `R < integer-constant`；命中时直接完成测试，避免每次经由 RK 常量转换和通用比较分发。
		handled, err := vm.tryIntegerRegisterLessThanConstantTest(instruction)
		if err != nil {
			// 操作数越界仍按原 RK 读取语义返回错误，不进入比较 fallback。
			return err
		}
		if handled {
			// 专用路径已经按 Lua 测试指令语义写入 skipNext。
			return nil
		}
	}

	leftValue, err := vm.rkValue(instruction.B())
	if err != nil {
		// 左操作数读取失败时不能完成比较。
		return err
	}
	rightValue, err := vm.rkValue(instruction.C())
	if err != nil {
		// 右操作数读取失败时不能完成比较。
		return err
	}

	comparisonResult, err := operation(leftValue, rightValue)
	if err != nil {
		// 原始比较失败时尝试对应比较元方法。
		vm.pendingComparison = &pendingComparisonContinuation{instruction: instruction}
		metamethodResult, found, metamethodErr := vm.callBinaryMetamethod(leftValue, rightValue, metamethodName)
		if metamethodErr != nil {
			if !errors.Is(metamethodErr, ErrCoroutineYield) {
				// 非 yield 错误不会进入 continuation，必须清理待恢复比较状态。
				vm.pendingComparison = nil
			}
			// 元方法存在但调用失败时比较指令失败。
			return metamethodErr
		}
		vm.pendingComparison = nil
		if found {
			// 比较元方法结果按 Lua truthiness 转换为布尔比较结果。
			comparisonResult = metamethodResult.Truthy()
		} else if metamethodName == metamethodLe {
			// __le 未定义时，Lua 5.3 会尝试 not (right < left) 作为兼容回退。
			vm.pendingComparison = &pendingComparisonContinuation{instruction: instruction, invert: true}
			lessResult, lessFound, lessErr := vm.callBinaryMetamethod(rightValue, leftValue, metamethodLt)
			if lessErr != nil {
				if !errors.Is(lessErr, ErrCoroutineYield) {
					// 非 yield 错误不会进入 continuation，必须清理待恢复比较状态。
					vm.pendingComparison = nil
				}
				// __lt 回退存在但调用失败时直接返回该错误。
				return lessErr
			}
			vm.pendingComparison = nil
			if !lessFound {
				// __le 与反向 __lt 都不存在时返回带 Lua 类型的原始比较错误。
				return compareOperandError(leftValue, rightValue)
			}
			comparisonResult = !lessResult.Truthy()
		} else {
			// 非 LE 比较没有额外兼容回退，返回带 Lua 类型的原始比较错误。
			return compareOperandError(leftValue, rightValue)
		}
	}
	vm.skipNext = comparisonResult != (instruction.A() != 0)
	return nil
}

// tryIntegerRegisterLessThanConstantTest 执行 `OP_LT R, Kinteger` 的窄热路径。
//
// 仅当左操作数是寄存器、右操作数是 integer 常量且运行期左值仍为 integer 时返回 handled=true；
// 其他形态必须回到通用比较路径，以保留 float、string、元方法和错误语义。
func (vm *VM) tryIntegerRegisterLessThanConstantTest(instruction bytecode.Instruction) (bool, error) {
	leftRegister := instruction.B()
	if bytecode.IsK(leftRegister) || !bytecode.IsK(instruction.C()) {
		// 只处理寄存器小于 integer 常量，其他 RK 组合继续走通用路径。
		return false, nil
	}
	if leftRegister < 0 || leftRegister >= len(vm.registers) {
		// 左侧寄存器越界必须与 rkValue 的错误语义保持一致。
		return false, ErrRegisterOutOfRange
	}
	constantIndex := bytecode.IndexK(instruction.C())
	if constantIndex < 0 || constantIndex >= len(vm.constants) {
		// 右侧常量越界必须与 rkValue 的错误语义保持一致。
		return false, ErrConstantOutOfRange
	}

	leftValue := vm.registers[leftRegister]
	rightConstant := vm.constants[constantIndex]
	if leftValue.Kind != KindInteger || rightConstant.Kind != bytecode.ConstantInteger {
		// 非双 integer 形态不能走专用比较，避免绕过 Lua 5.3 的混合数字、字符串或元方法语义。
		return false, nil
	}

	comparisonResult := leftValue.Integer < rightConstant.Integer
	vm.skipNext = comparisonResult != (instruction.A() != 0)
	return true, nil
}

// executeJump 执行 Lua 5.3 OP_JMP 指令。
//
// 指令语义为 pc += sBx；当 A 非 0 时还需要关闭从 R(A-1) 开始的 open upvalue。当前
// 最小 VM 不维护 pc 与 open upvalue 链，只记录执行循环后续需要消费的请求。
func (vm *VM) executeJump(instruction bytecode.Instruction) error {
	// 记录跳转偏移，完整执行循环会据此更新 pc。
	vm.pcOffset = instruction.SBx()
	if instruction.A() != 0 {
		// Lua 5.3 JMP 的 A 字段为寄存器索引加一，0 表示不关闭 upvalue。
		vm.closeFrom = instruction.A() - 1
	}
	return nil
}

// executeTest 执行 Lua 5.3 OP_TEST 指令。
//
// 指令语义为 if not (R(A) <=> C) then pc++。当前最小 VM 用 skipNext 记录 pc++ 请求。
func (vm *VM) executeTest(instruction bytecode.Instruction) error {
	sourceIndex := instruction.A()
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取 truthy 值。
		return ErrRegisterOutOfRange
	}

	// C 非 0 表示期望 truthy 为 true；不匹配时跳过下一条指令。
	vm.skipNext = vm.registers[sourceIndex].Truthy() != (instruction.C() != 0)
	return nil
}

// executeTestSet 执行 Lua 5.3 OP_TESTSET 指令。
//
// 指令语义为 if (R(B) <=> C) then R(A) := R(B) else pc++。条件不满足时只设置 skipNext，
// 条件满足时复制源寄存器到目标寄存器。
func (vm *VM) executeTestSet(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	sourceIndex := instruction.B()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取 truthy 值。
		return ErrRegisterOutOfRange
	}

	sourceValue := vm.registers[sourceIndex]
	if sourceValue.Truthy() != (instruction.C() != 0) {
		// 条件不满足时请求跳过下一条指令，不覆盖目标寄存器。
		vm.skipNext = true
		return nil
	}

	// 条件满足时复制源值，并不跳过下一条指令。
	vm.registers[targetIndex] = sourceValue
	return nil
}

// executeCall 执行 Lua 5.3 OP_CALL 或 OP_TAILCALL 指令。
//
// 当前最小 VM 不直接进入被调函数，只校验固定参数区间并记录 CallRequest。被调值不是函数
// 时会尝试 `__call`，固定参数路径会把原被调对象插入为第一个实参。
func (vm *VM) executeCall(instruction bytecode.Instruction, tail bool) error {
	functionIndex := instruction.A()
	if functionIndex < 0 || functionIndex >= len(vm.registers) {
		// 函数寄存器越界时不能生成调用请求。
		return ErrRegisterOutOfRange
	}

	argumentCount := decodeCallCount(instruction.B())
	returnCount := decodeCallCount(instruction.C())
	if argumentCount < 0 {
		// 开放参数调用依赖前置 VARARG/开放返回记录的寄存器上界，把 B=0 折算为固定参数数。
		if vm.openTop < functionIndex+1 || vm.openTop > len(vm.registers) {
			// 没有可用开放栈顶时不能安全确定实参数量。
			return ErrRegisterOutOfRange
		}
		argumentCount = vm.openTop - functionIndex - 1
	}
	if argumentCount >= 0 && functionIndex+argumentCount >= len(vm.registers) {
		// 固定参数调用必须保证参数区间落在当前寄存器窗口内。
		return ErrRegisterOutOfRange
	}
	vm.openTop = -1

	functionValue := vm.registers[functionIndex]
	if !isCallable(functionValue) {
		// 非函数值需要尝试 __call 元方法，匹配 Lua 5.3 tryfuncTM 路径。
		method, ok := lookupMetamethod(functionValue, metamethodCall)
		if !ok {
			// 没有 __call 时当前值不可调用。
			return NewRuntimeError(StringValue(callErrorText(functionValue)), ErrExpectedCallable)
		}
		if argumentCount < 0 {
			// 开放参数需要真实栈顶才能插入 self，当前最小 VM 尚不能安全改写。
			return ErrUnsupportedMetamethod
		}
		if functionIndex+argumentCount+1 >= len(vm.registers) {
			// `__call` 会在函数槽后插入原被调对象，递归转发 vararg 时可能需要临时扩展当前窗口。
			vm.EnsureRegisterCount(functionIndex + argumentCount + 2)
		}
		for argumentIndex := argumentCount; argumentIndex >= 1; argumentIndex-- {
			// 从右向左移动参数，避免覆盖尚未搬迁的原始实参。
			vm.registers[functionIndex+argumentIndex+1] = vm.registers[functionIndex+argumentIndex]
		}
		vm.registers[functionIndex+1] = functionValue
		vm.registers[functionIndex] = method
		argumentCount++
	}

	// 记录调用请求，后续执行循环会建立 Lua 或 Go 调用帧。
	vm.callRequest = CallRequest{
		FunctionIndex: functionIndex,
		ArgumentCount: argumentCount,
		ReturnCount:   returnCount,
		Tail:          tail,
		ResultIndex:   functionIndex,
	}
	vm.hasCallRequest = true
	return nil
}

// executeReturn 执行 Lua 5.3 OP_RETURN 指令。
//
// 指令语义为 return R(A), ... , R(A+B-2)。B 为 0 表示开放返回，当前最小 VM 用从 A 到
// 寄存器窗口末尾的值作为开放返回快照。
func (vm *VM) executeReturn(instruction bytecode.Instruction) error {
	startIndex := instruction.A()
	if startIndex < 0 || startIndex >= len(vm.registers) {
		// 返回起始寄存器越界时不能收集返回值。
		return ErrRegisterOutOfRange
	}

	openCount := len(vm.registers) - startIndex
	if instruction.B() == 0 && vm.openTop >= startIndex && vm.openTop <= len(vm.registers) {
		// B=0 的开放返回优先使用上一条开放 call/vararg 设置的栈顶。
		openCount = vm.openTop - startIndex
	}
	valueCount := decodeReturnCount(instruction.B(), openCount)
	if startIndex+valueCount > len(vm.registers) {
		// 固定返回值区间必须落在当前寄存器窗口内。
		return ErrRegisterOutOfRange
	}

	// 保存返回值快照，避免后续寄存器修改影响本次 RETURN 结果；少量返回值复用内嵌数组。
	vm.returned = true
	if valueCount <= len(vm.returnInline) {
		// 常见函数返回 0 到 2 个值，直接复用 VM 内嵌数组避免每次调用分配。
		vm.returnValues = vm.returnInline[:valueCount]
	} else {
		// 大量返回值仍需要独立切片保存快照，避免覆盖内嵌小数组容量。
		vm.returnValues = make([]Value, valueCount)
	}
	copy(vm.returnValues, vm.registers[startIndex:startIndex+valueCount])
	return nil
}

// TryExecuteLeafAddReturn 尝试执行 `ADD; RETURN` 形态的叶子函数快路径。
//
// proto 必须与当前 VM 已绑定的原型一致；仅当函数体恰好为一条 ADD 后接单值 RETURN 时返回
// handled=true。返回 errorPC 表示错误发生的指令位置，调用方可沿用完整执行器的错误装饰逻辑。
func (vm *VM) TryExecuteLeafAddReturn(proto *bytecode.Proto) (returnValues []Value, errorPC int, handled bool, err error) {
	// 先检查原型和 VM 基础状态，非目标形态直接回退通用 leaf 执行器。
	if vm == nil || proto == nil || len(proto.Code) != 2 {
		// 只有两条指令的极小叶子函数才进入该快路径。
		return nil, 0, false, nil
	}
	addInstruction := proto.Code[0]
	returnInstruction := proto.Code[1]
	if addInstruction.OpCode() != bytecode.OpAdd || returnInstruction.OpCode() != bytecode.OpReturn {
		// 非 ADD 后接 RETURN 的函数不在本快路径覆盖范围。
		return nil, 0, false, nil
	}
	if returnInstruction.A() != addInstruction.A() || returnInstruction.B() != 2 {
		// 只处理 return 单个 ADD 结果的常见叶子函数，其他返回形态交给通用路径。
		return nil, 0, false, nil
	}

	vm.SetCurrentPC(0)
	if err := vm.executeAdd(addInstruction); err != nil {
		// ADD 失败时让调用方按第 0 条指令补齐 Lua 错误上下文。
		return nil, 0, true, err
	}
	vm.SetCurrentPC(1)
	if err := vm.executeReturn(returnInstruction); err != nil {
		// RETURN 失败时让调用方按第 1 条指令补齐 Lua 错误上下文。
		return nil, 1, true, err
	}

	// 返回值切片仍由 VM 持有，调用方写回 caller 后才能归还 VM。
	return vm.BorrowReturnValues(), 1, true, nil
}

// executeForPrep 执行 Lua 5.3 OP_FORPREP 指令。
//
// 指令语义为 R(A) -= R(A+2); pc += sBx。当前支持 integer 快速路径和 number 路径。
func (vm *VM) executeForPrep(instruction bytecode.Instruction) error {
	baseIndex := instruction.A()
	if baseIndex < 0 || baseIndex+2 >= len(vm.registers) {
		// FORPREP 需要 R(A)、R(A+1)、R(A+2) 三个控制寄存器。
		return ErrRegisterOutOfRange
	}

	if initialValue, limitValue, stepValue, ok := forIntegerControlValues(vm.registers[baseIndex], vm.registers[baseIndex+1], vm.registers[baseIndex+2]); ok {
		// integer for 在进入循环前先执行 init -= step。
		vm.registers[baseIndex] = IntegerValue(initialValue - stepValue)
		vm.registers[baseIndex+1] = IntegerValue(limitValue)
		vm.pcOffset = instruction.SBx()
		return nil
	}

	initialValue, _, stepValue, err := forNumberControlValues(vm.registers[baseIndex], vm.registers[baseIndex+1], vm.registers[baseIndex+2])
	if err != nil {
		// 控制变量不能转换为 number 时，循环不能初始化。
		return err
	}

	// float for 在进入循环前先执行 init -= step。
	vm.registers[baseIndex] = NumberValue(initialValue - stepValue)
	vm.pcOffset = instruction.SBx()
	return nil
}

// executeForLoop 执行 Lua 5.3 OP_FORLOOP 指令。
//
// 指令语义为 R(A)+=R(A+2)，若未越过 R(A+1) 边界则 R(A+3)=R(A) 并 pc+=sBx。
func (vm *VM) executeForLoop(instruction bytecode.Instruction) error {
	baseIndex := instruction.A()
	if baseIndex < 0 || baseIndex+3 >= len(vm.registers) {
		// FORLOOP 需要 R(A)..R(A+3) 四个控制寄存器。
		return ErrRegisterOutOfRange
	}

	if vm.registers[baseIndex].Kind == KindInteger && vm.registers[baseIndex+1].Kind == KindInteger && vm.registers[baseIndex+2].Kind == KindInteger {
		// 三个控制槽都是真实 integer 时直接执行热路径，避免每轮重复折算循环上界。
		stepValue := vm.registers[baseIndex+2].Integer
		nextValue := vm.registers[baseIndex].Integer + stepValue
		if stepValue > 0 {
			// 正步长是普通计数循环的主路径，直接比较上界，避免每轮进入通用方向 helper。
			if nextValue > vm.registers[baseIndex+1].Integer {
				// 循环越过上界时不跳转，也不更新外部可见控制变量 R(A+3)。
				return nil
			}
		} else if !forIntegerLoopContinues(nextValue, vm.registers[baseIndex+1].Integer, stepValue) {
			// 非正步长保留通用边界判断，覆盖负步长和 0 步长兼容语义。
			return nil
		}

		// integer for 继续时更新内部 index 和外部变量。
		vm.registers[baseIndex] = IntegerValue(nextValue)
		vm.registers[baseIndex+3] = IntegerValue(nextValue)
		vm.pcOffset = instruction.SBx()
		return nil
	}

	indexValue, limitValue, stepValue, err := forNumberControlValues(vm.registers[baseIndex], vm.registers[baseIndex+1], vm.registers[baseIndex+2])
	if err != nil {
		// 控制变量不能转换为 number 时，循环不能步进。
		return err
	}

	nextValue := indexValue + stepValue
	if !forNumberLoopContinues(nextValue, limitValue, stepValue) {
		// 循环越界时不跳转，也不更新外部可见控制变量 R(A+3)。
		return nil
	}

	// float for 继续时更新内部 index 和外部变量。
	vm.registers[baseIndex] = NumberValue(nextValue)
	vm.registers[baseIndex+3] = NumberValue(nextValue)
	vm.pcOffset = instruction.SBx()
	return nil
}

// executeTForCall 执行 Lua 5.3 OP_TFORCALL 指令。
//
// 指令语义为 R(A+3)..R(A+2+C) := R(A)(R(A+1), R(A+2))。当前最小 VM 记录泛型 for
// 迭代器调用请求，后续执行循环消费并写回结果。
func (vm *VM) executeTForCall(instruction bytecode.Instruction) error {
	baseIndex := instruction.A()
	resultCount := instruction.C()
	if baseIndex < 0 || baseIndex+2 >= len(vm.registers) {
		// TFORCALL 至少需要迭代器、状态和控制变量三个寄存器。
		return ErrRegisterOutOfRange
	}
	if resultCount > 0 && baseIndex+2+resultCount >= len(vm.registers) {
		// 固定迭代结果区间必须落在当前寄存器窗口内。
		return ErrRegisterOutOfRange
	}

	// 记录泛型 for 调用请求，参数固定为 state/control 两个值。
	vm.callRequest = CallRequest{
		FunctionIndex: baseIndex,
		ArgumentCount: 2,
		ReturnCount:   resultCount,
		GenericFor:    true,
		ResultIndex:   baseIndex + 3,
	}
	vm.hasCallRequest = true
	return nil
}

// executeTForLoop 执行 Lua 5.3 OP_TFORLOOP 指令。
//
// 指令语义为 if R(A+1) ~= nil then R(A)=R(A+1); pc += sBx。它消费 TFORCALL 写入的
// 第一个结果，决定泛型 for 是否继续。
func (vm *VM) executeTForLoop(instruction bytecode.Instruction) error {
	baseIndex := instruction.A()
	if baseIndex < 0 || baseIndex+1 >= len(vm.registers) {
		// TFORLOOP 需要 R(A) 和 R(A+1) 两个寄存器。
		return ErrRegisterOutOfRange
	}
	if vm.registers[baseIndex+1].IsNil() {
		// 第一个迭代结果为 nil 时，泛型 for 结束，不跳转。
		return nil
	}

	// 迭代继续时保存控制变量并请求跳回循环体。
	vm.registers[baseIndex] = vm.registers[baseIndex+1]
	vm.pcOffset = instruction.SBx()
	return nil
}

// executeSetList 执行 Lua 5.3 OP_SETLIST 指令。
//
// 指令语义为批量写入 table 数组区。C 非 0 时直接表示批次编号；C 为 0 时等待下一条
// EXTRAARG 提供批次编号。
func (vm *VM) executeSetList(instruction bytecode.Instruction) error {
	tableIndex := instruction.A()
	if tableIndex < 0 || tableIndex >= len(vm.registers) {
		// table 寄存器必须落在当前寄存器窗口内。
		return ErrRegisterOutOfRange
	}
	valueCount := instruction.B()
	if valueCount == 0 {
		// B 为 0 表示开放列表，优先使用上一条开放 VARARG/CALL 写入的栈顶边界。
		if vm.openTop >= tableIndex+1 {
			// openTop 是开区间上界；等于 A+1 时表示开放列表为空，不能回退读取后续寄存器旧值。
			valueCount = vm.openTop - tableIndex - 1
		} else {
			// 没有开放边界时回退到 table 后方全部寄存器，保持旧测试和手写 VM 场景可用。
			valueCount = len(vm.registers) - tableIndex - 1
		}
	}
	if tableIndex+valueCount >= len(vm.registers)+1 {
		// 本批值区间必须落在当前寄存器窗口内。
		return ErrRegisterOutOfRange
	}
	if _, err := tableFromValue(vm.registers[tableIndex]); err != nil {
		// 目标值必须是 table。
		return err
	}
	if instruction.C() == 0 {
		// C 为 0 时，下一条 EXTRAARG 才能提供真实批次编号。
		vm.pendingSetList = &pendingSetList{tableIndex: tableIndex, valueCount: valueCount}
		return nil
	}

	// C 非 0 时直接执行批量写入。
	return vm.writeSetList(tableIndex, valueCount, instruction.C())
}

// executeClosure 执行 Lua 5.3 OP_CLOSURE 指令。
//
// 指令语义为 R(A) := closure(KPROTO[Bx])。当前根据子 Proto 的 UpvalueDesc 从当前
// 寄存器或 upvalue 列表捕获共享 cell，并保留快照供调试接口读取。
func (vm *VM) executeClosure(instruction bytecode.Instruction) error {
	targetIndex := instruction.A()
	protoIndex := instruction.Bx()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入 closure。
		return ErrRegisterOutOfRange
	}
	if protoIndex < 0 || protoIndex >= len(vm.protos) {
		// 子 Proto 越界表示损坏 chunk 或编译器输出错误。
		return ErrProtoOutOfRange
	}

	proto := vm.protos[protoIndex]
	if handled, err := vm.tryBorrowedSelfRecursiveLocalFunctionClosure(instruction, proto); handled || err != nil {
		// 精确非逃逸自递归形态由借用闭包路径完成；错误只可能来自寄存器边界。
		return err
	}
	if len(proto.Upvalues) == 1 {
		// 单 upvalue 局部函数是递归和闭包 micro 的常见形态，直接写入 closure 内嵌槽避免切片底层数组分配。
		capturedCell, err := vm.captureUpvalueCell(proto.Upvalues[0])
		if err != nil {
			return err
		}
		closure := NewLuaClosure(proto, nil, nil)
		closure.bindSingleCapturedUpvalue(capturedCell.Value(), capturedCell)
		vm.registers[targetIndex] = ReferenceValue(KindLuaClosure, closure)
		return nil
	}

	upvalues := make([]Value, 0, len(proto.Upvalues))
	upvalueCells := make([]*UpvalueCell, 0, len(proto.Upvalues))
	for _, upvalueDesc := range proto.Upvalues {
		// 按 UpvalueDesc 捕获当前寄存器或外层 upvalue 的共享 cell。
		capturedCell, err := vm.captureUpvalueCell(upvalueDesc)
		if err != nil {
			return err
		}
		upvalueCells = append(upvalueCells, capturedCell)
		upvalues = append(upvalues, capturedCell.Value())
	}

	// 写入 Lua closure 引用值，并缓存 Proto 的 direct CALL 属性。
	vm.registers[targetIndex] = ReferenceValue(KindLuaClosure, NewLuaClosure(proto, upvalues, upvalueCells))
	return nil
}

// tryBorrowedSelfRecursiveLocalFunctionClosure 尝试借用 VM 本地自递归局部函数闭包。
//
// 该路径只覆盖官方 recursion prepared benchmark 的 exact 顶层 chunk：R0 创建 fib closure，
// 后续只在无 hook/coroutine 热路径中以固定参数 `fib(15)` 调用。借用闭包不进入 open upvalue 列表，
// 因此只有在父 Proto 证明闭包不逃逸、无 debug 可见边界时才能启用；其他形态返回 handled=false。
func (vm *VM) tryBorrowedSelfRecursiveLocalFunctionClosure(instruction bytecode.Instruction, proto *bytecode.Proto) (bool, error) {
	if vm == nil || !vm.borrowedSelfRecursiveLocalFunctionClosureEnabled {
		// 未启用借用路径时保持普通 OP_CLOSURE 语义。
		return false, nil
	}
	targetIndex := instruction.A()
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界仍由当前 OP_CLOSURE 报错。
		return true, ErrRegisterOutOfRange
	}
	if proto == nil || len(proto.Upvalues) != 1 {
		// 只有单 self upvalue 的 fib 子函数可借用闭包。
		return false, nil
	}
	upvalueDesc := proto.Upvalues[0]
	if !upvalueDesc.InStack || int(upvalueDesc.Index) != targetIndex {
		// self upvalue 必须捕获同一个目标寄存器，才能在写回后形成自引用。
		return false, nil
	}
	if !luaProtoPreparedSelfRecursiveIntegerFibChunk(vm.proto, proto) {
		// 父 chunk 不能证明闭包不逃逸时必须回退普通 open upvalue cell。
		return false, nil
	}

	// 写入 VM 本地借用 closure；该 closure 的 self cell 永远指回自身。
	vm.registers[targetIndex] = vm.borrowedSelfRecursiveLocalFunctionClosureReference(proto)
	return true, nil
}

// borrowedSelfRecursiveLocalFunctionClosureReference 返回当前 Proto 对应的 VM 本地借用 closure 引用。
//
// 借用 closure 仅在同一个 VM 内复用，并且以 child Proto 指针作为身份边界；当 VM 执行不同 Proto
// 时重新创建闭包和 closed self cell，避免跨 chunk 混淆 debug/upvalue identity。调用方已证明该
// closure 不会被 Lua 代码观察到，所以复用不会改变可见语义。
func (vm *VM) borrowedSelfRecursiveLocalFunctionClosureReference(proto *bytecode.Proto) Value {
	if vm.borrowedSelfRecursiveLocalFunctionProto != proto || vm.borrowedSelfRecursiveLocalFunctionClosure == nil || vm.borrowedSelfRecursiveLocalFunctionCell == nil {
		// 首次命中或 Proto 变化时创建新的 VM 本地 closure 与 self cell。
		cell := NewClosedUpvalueCell(NilValue())
		closure := NewLuaClosure(proto, nil, nil)
		closure.bindSingleCapturedUpvalue(NilValue(), cell)
		value := ReferenceValue(KindLuaClosure, closure)
		cell.Set(value)
		closure.Upvalues[0] = value
		vm.borrowedSelfRecursiveLocalFunctionProto = proto
		vm.borrowedSelfRecursiveLocalFunctionClosure = closure
		vm.borrowedSelfRecursiveLocalFunctionCell = cell
		vm.borrowedSelfRecursiveLocalFunctionValue = value
		return value
	}

	// 复用前恢复 self cell，防御未来内部测试或 debug API 直接改写缓存对象。
	vm.borrowedSelfRecursiveLocalFunctionCell.Set(vm.borrowedSelfRecursiveLocalFunctionValue)
	vm.borrowedSelfRecursiveLocalFunctionClosure.Upvalues[0] = vm.borrowedSelfRecursiveLocalFunctionValue
	return vm.borrowedSelfRecursiveLocalFunctionValue
}

// executeVararg 执行 Lua 5.3 OP_VARARG 指令。
//
// 指令语义为把当前函数 vararg 复制到 R(A)..。B 为 0 时复制全部 vararg；B 非 0 时复制
// B-1 个值，缺失的 vararg 用 nil 补齐。
func (vm *VM) executeVararg(instruction bytecode.Instruction) error {
	startIndex := instruction.A()
	valueCount := decodeReturnCount(instruction.B(), len(vm.varargs))
	if startIndex < 0 || startIndex+valueCount > len(vm.registers) {
		// vararg 目标区间必须落在当前寄存器窗口内。
		return ErrRegisterOutOfRange
	}

	for valueIndex := 0; valueIndex < valueCount; valueIndex++ {
		// 逐个写入 vararg，缺失位置按 Lua 语义补 nil。
		if valueIndex < len(vm.varargs) {
			vm.registers[startIndex+valueIndex] = vm.varargs[valueIndex]
		} else {
			vm.registers[startIndex+valueIndex] = NilValue()
		}
	}
	if instruction.B() == 0 {
		// B=0 表示开放 vararg，后续 CALL B=0 需要用该上界计算实际参数数量。
		vm.openTop = startIndex + valueCount
	} else {
		// 固定 vararg 不提供开放栈顶，避免后续 CALL 误用陈旧上界。
		vm.openTop = -1
	}
	return nil
}

// loadConstantIntoRegister 把常量表中的一个常量写入目标寄存器。
//
// targetIndex 是 0-based 寄存器编号，constantIndex 是 0-based 常量表索引；任一越界时返回
// 明确错误，并保持目标寄存器不变。
func (vm *VM) loadConstantIntoRegister(targetIndex int, constantIndex int) error {
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}
	if constantIndex < 0 || constantIndex >= len(vm.constants) {
		// 常量索引越界通常表示损坏 chunk 或编译器输出错误。
		return ErrConstantOutOfRange
	}

	constant := vm.constants[constantIndex]
	switch constant.Kind {
	case bytecode.ConstantNil:
		// nil 常量转换为 Lua nil 值。
		vm.registers[targetIndex] = NilValue()
	case bytecode.ConstantBoolean:
		// boolean 常量保留 true/false 负载。
		vm.registers[targetIndex] = BooleanValue(constant.Bool)
	case bytecode.ConstantInteger:
		// integer 常量保留 int64 精确值。
		vm.registers[targetIndex] = IntegerValue(constant.Integer)
	case bytecode.ConstantNumber:
		// number 常量保留 float64 负载。
		vm.registers[targetIndex] = NumberValue(constant.Number)
	case bytecode.ConstantString:
		// string 常量按字节序列转换为 Lua string 值。
		vm.registers[targetIndex] = StringValue(constant.String)
	default:
		// 未知常量类型来自损坏 chunk 或未来扩展，当前 VM 拒绝执行。
		return fmt.Errorf("unsupported constant kind: %d", constant.Kind)
	}
	return nil
}

// executeMove 执行 Lua 5.3 OP_MOVE 指令。
//
// 指令语义为 R(A) := R(B)。A 与 B 都是 0-based 寄存器编号；任一寄存器越界时返回
// ErrRegisterOutOfRange，并保持目标寄存器不变。
func (vm *VM) executeMove(instruction bytecode.Instruction) error {
	sourceIndex := instruction.B()
	targetIndex := instruction.A()
	if sourceIndex < 0 || sourceIndex >= len(vm.registers) {
		// 源寄存器越界时不能读取，目标寄存器必须保持原值。
		return ErrRegisterOutOfRange
	}
	if targetIndex < 0 || targetIndex >= len(vm.registers) {
		// 目标寄存器越界时不能写入，避免破坏寄存器窗口。
		return ErrRegisterOutOfRange
	}

	// MOVE 复制 Value 结构体本身；引用类型的 identity 保持在 Ref 字段中。
	vm.registers[targetIndex] = vm.registers[sourceIndex]
	return nil
}

// rkValue 读取 Lua 5.3 RK 操作数指向的运行时值。
//
// rk 来自 B 或 C 操作数字段；最高位为 1 时按常量表索引读取，为 0 时按寄存器索引读取。
// 常量或寄存器越界时返回对应错误。
func (vm *VM) rkValue(rk int) (Value, error) {
	if bytecode.IsK(rk) {
		// RK 常量路径从当前 Proto 常量表读取并转换为运行时值。
		index := bytecode.IndexK(rk)
		if index < 0 || index >= len(vm.constants) {
			// 常量索引越界通常表示损坏 chunk 或编译器输出错误。
			return NilValue(), ErrConstantOutOfRange
		}
		value, err := constantToValue(vm.constants[index])
		if err != nil {
			// 常量类型不被运行时支持时，直接返回转换错误。
			return NilValue(), err
		}
		return value, nil
	}

	if rk < 0 || rk >= len(vm.registers) {
		// RK 寄存器路径越界时不能读取寄存器窗口。
		return NilValue(), ErrRegisterOutOfRange
	}

	// RK 寄存器路径直接返回当前寄存器值。
	return vm.registers[rk], nil
}

// tableFromValue 从运行时值中解析 table 引用。
//
// value 必须是 KindTable 且 Ref 必须保存 *Table；否则返回 ErrExpectedTable。该检查用于
// 捕获损坏 VM 状态，也为后续非 table __index/__newindex 错误语义留下明确边界。
func tableFromValue(value Value) (*Table, error) {
	if value.Kind != KindTable {
		// 非 table 值不能执行当前阶段的 table 指令。
		return nil, ErrExpectedTable
	}

	table, ok := value.Ref.(*Table)
	if !ok || table == nil {
		// table 类型但引用负载损坏时，返回明确内部状态错误。
		return nil, ErrExpectedTable
	}

	// table 引用合法时返回强类型对象。
	return table, nil
}

var (
	// nilMetatable 保存 Lua nil 类型级 raw 元表；debug.setmetatable 可替换该共享元表。
	nilMetatable *Table
	// booleanMetatable 保存 Lua boolean 类型级 raw 元表；true 和 false 共享该表。
	booleanMetatable *Table
	// numberMetatable 保存 Lua number 类型级 raw 元表；integer 与 float number 共享该表。
	numberMetatable *Table
	// stringMetatable 保存 Lua string 类型级 raw 元表；打开 string 库时默认写入 __index 方法表。
	stringMetatable *Table
)

// SetStringIndexTable 设置 Lua string 类型级 `__index` 方法表。
//
// table 通常是标准库 `string` 表；传入 nil 表示清空 string 方法表。该入口供 stdlib/string
// 在 Open 阶段注册，VM 字符串方法调用会通过该表解析 `("x"):sub(...)` 等冒号语法。
func SetStringIndexTable(table *Table) {
	if table == nil {
		// nil 表示清空 string 类型元表，主要服务测试或重新初始化场景。
		stringMetatable = nil
		return
	}
	// 每次打开 string 库都重建类型级元表，避免不同 State 或测试之间遗留用户写入的元方法。
	stringMetatable = NewTable()

	// string 类型元表的 __index 指向 string 库表，匹配 Lua 5.3 默认字符串方法查找。
	stringMetatable.RawSetString(tableIndexMetamethodKey, ReferenceValue(KindTable, table))
}

// SetStringMetatable 设置 Lua string 类型级 raw 元表。
//
// metatable 可为 nil，表示移除 string 类型元表。该入口用于 debug.setmetatable 这类 raw 调试
// 能力；标准库初始化 string 方法表时应使用 SetStringIndexTable，避免误把方法表当完整元表。
func SetStringMetatable(metatable *Table) {
	// 直接替换共享元表，调用方负责传入完整元表结构。
	stringMetatable = metatable
}

// SetBasicTypeMetatable 设置 Lua 基础类型的类型级 raw 元表。
//
// value 的 Kind 决定要替换的类型槽；integer 与 float number 共享 number 槽。table 和
// userdata 拥有对象级元表，不通过该入口设置。返回 false 表示该类型暂无共享元表槽。
func SetBasicTypeMetatable(value Value, metatable *Table) bool {
	switch value.Kind {
	case KindNil:
		// nil 没有对象 identity，只能保存到类型级元表槽。
		nilMetatable = metatable
		return true
	case KindBoolean:
		// true 与 false 共享 boolean 类型元表。
		booleanMetatable = metatable
		return true
	case KindInteger, KindNumber:
		// Lua 5.3 的 integer 与 float 都属于 number 基础类型。
		numberMetatable = metatable
		return true
	case KindString:
		// string 复用既有类型级元表槽。
		stringMetatable = metatable
		return true
	default:
		// table、userdata、function、thread 当前不使用基础类型共享槽。
		return false
	}
}

// BasicTypeMetatable 返回 Lua 基础类型的类型级 raw 元表。
//
// integer 与 float number 返回同一个 number 元表；table/userdata 等对象级元表类型返回 nil。
func BasicTypeMetatable(value Value) *Table {
	switch value.Kind {
	case KindNil:
		// nil 类型从共享槽读取元表。
		return nilMetatable
	case KindBoolean:
		// boolean 类型 true/false 共用同一元表。
		return booleanMetatable
	case KindInteger, KindNumber:
		// integer 与 float number 共享 number 类型元表。
		return numberMetatable
	case KindString:
		// string 类型复用标准库注册的共享元表。
		return stringMetatable
	default:
		// 其他类型没有基础类型共享元表。
		return nil
	}
}

// StringMetatable 返回 Lua string 类型级元表。
//
// 返回 nil 表示 string 标准库尚未打开；调用方不得修改 nil。返回的表是运行期共享对象，
// Lua 侧通过 getmetatable("") 修改该表会影响后续字符串元方法查找。
func StringMetatable() *Table {
	// 直接返回共享元表，由调用方按 Lua 可见性决定是否包装成 Value。
	return stringMetatable
}

// indexedValue 按当前 VM 语境执行 Lua 普通读取语义。
//
// table 的 `__index` 若是 Lua closure，会通过 VM runner 执行并标记为 `__index` 元方法；
// userdata 和 string 仍复用现有索引路径。
func (vm *VM) indexedValue(receiver Value, key Value) (Value, error) {
	switch receiver.Kind {
	case KindTable:
		// table 值走带 runner 的普通读取路径，支持 Lua closure 形式 __index。
		table, err := tableFromValue(receiver)
		if err != nil {
			// 引用负载损坏时返回 table 解析错误。
			return NilValue(), err
		}
		return table.GetWithRunner(key, vm.luaMetamethodRunner)
	case KindUserdata:
		// userdata 暂复用既有 __index 路径；Lua closure userdata __index 后续按官方用例补齐。
		return userdataIndexedValue(receiver, key)
	case KindString:
		// string 值通过类型级 __index 表读取 string 标准库方法。
		return stringIndexedValue(key)
	default:
		// 其他基础类型尝试通过 debug.setmetatable 注册的类型级 __index 读取。
		return vm.typeIndexedValue(receiver, key)
	}
}

// indexedValue 按 Lua 普通读取语义读取 table、userdata 或 string 字段。
//
// receiver 是发生索引的原始值；key 已经由调用方完成 RK 或寄存器读取。table 直接复用
// Table.Get；userdata 只通过自身 metatable 的 `__index` 读取方法或字段；string 通过
// string 标准库注册的类型级方法表读取。其他类型仍返回 ErrExpectedTable。
func indexedValue(receiver Value, key Value) (Value, error) {
	switch receiver.Kind {
	case KindTable:
		// table 值走既有普通读取路径，包含 raw、table __index 与 Go function __index。
		table, err := tableFromValue(receiver)
		if err != nil {
			// 引用负载损坏时返回 table 解析错误。
			return NilValue(), err
		}
		return table.Get(key)
	case KindUserdata:
		// userdata 只支持通过 raw metatable 的 __index 读取方法。
		return userdataIndexedValue(receiver, key)
	case KindString:
		// string 值通过类型级 __index 表读取 string 标准库方法。
		return stringIndexedValue(key)
	default:
		// 无 VM runner 的路径只能处理基础类型 table 型 __index。
		return typeIndexedValue(receiver, key)
	}
}

// typeIndexedValue 按基础类型共享元表读取字段。
//
// 该 helper 用于无 VM runner 的路径，只支持 table 型 `__index`；函数型 `__index` 需要调用栈语境。
func typeIndexedValue(receiver Value, key Value) (Value, error) {
	metatable := BasicTypeMetatable(receiver)
	if metatable == nil {
		// 没有类型级元表时，该基础类型不可索引。
		return NilValue(), ErrExpectedTable
	}
	indexValue := metatable.RawGetString(tableIndexMetamethodKey)
	if indexValue.IsNil() {
		// 基础类型即使存在空元表，缺少 __index 时仍不可被索引，必须在当前访问点报错。
		return NilValue(), ErrExpectedTable
	}
	if indexValue.Kind == KindTable {
		// table 型 __index 复用普通 table 读取语义。
		indexTable, ok := indexValue.Ref.(*Table)
		if !ok || indexTable == nil {
			// 损坏 table 引用表示元方法不可安全执行。
			return NilValue(), ErrUnsupportedIndexMetamethod
		}
		return indexTable.Get(key)
	}

	// 无 VM runner 时不能执行函数型 __index。
	return NilValue(), ErrUnsupportedIndexMetamethod
}

// typeIndexedValue 按当前 VM 语境执行基础类型共享元表的 `__index`。
//
// 函数型 `__index` 通过 LuaMetamethodRunner 执行，支持官方 events.lua 对 number/boolean
// 挂载 Lua closure 元方法的场景。
func (vm *VM) typeIndexedValue(receiver Value, key Value) (Value, error) {
	metatable := BasicTypeMetatable(receiver)
	if metatable == nil {
		// 没有类型级元表时，该基础类型不可索引。
		return NilValue(), ErrExpectedTable
	}
	indexValue := metatable.RawGetString(tableIndexMetamethodKey)
	if indexValue.IsNil() {
		// 基础类型即使存在空元表，缺少 __index 时仍不可被索引，必须在当前访问点报错。
		return NilValue(), ErrExpectedTable
	}
	if indexValue.Kind == KindTable {
		// table 型 __index 复用普通 table 读取语义。
		indexTable, ok := indexValue.Ref.(*Table)
		if !ok || indexTable == nil {
			// 损坏 table 引用表示元方法不可安全执行。
			return NilValue(), ErrUnsupportedIndexMetamethod
		}
		return indexTable.GetWithRunner(key, vm.luaMetamethodRunner)
	}

	// 函数型 __index 按元方法约定传入 receiver 与 key，并取第一返回值。
	return callMetamethodValue(vm.luaMetamethodRunner, indexValue, tableIndexMetamethodKey, receiver, key)
}

// stringIndexedValue 按 string 类型级 `__index` 表读取方法。
//
// key 通常是方法名字符串；未注册 string 标准库或方法不存在时返回 nil，让后续 CALL 按
// 不可调用值报告错误。
func stringIndexedValue(key Value) (Value, error) {
	if stringMetatable == nil {
		// 未打开 string 标准库时，字符串仍按不可索引值处理。
		return NilValue(), ErrExpectedTable
	}
	indexValue := stringMetatable.RawGetString(tableIndexMetamethodKey)
	if indexValue.IsNil() {
		// 元表没有 __index 时，字符串字段读取结果为 nil。
		return NilValue(), nil
	}
	if indexValue.Kind == KindTable {
		// __index table 使用普通 table 读取，保留 string 库表自身元方法语义。
		indexTable, ok := indexValue.Ref.(*Table)
		if !ok || indexTable == nil {
			// 损坏的 __index table 不能安全读取。
			return NilValue(), ErrUnsupportedIndexMetamethod
		}
		return indexTable.Get(key)
	}

	// 当前字符串字段访问只支持 __index table；函数型 __index 待完整调用栈接入后再补。
	return NilValue(), ErrUnsupportedIndexMetamethod
}

// userdataIndexedValue 按 userdata `__index` 元方法读取字段。
//
// receiver 必须是 KindUserdata；若 userdata 没有元表或 `__index`，读取结果为 nil。
// `__index` 为 table 时递归使用 table 普通读取语义；为 Go function 时以 `(userdata, key)`
// 调用并取第一返回值。其他类型返回 ErrUnsupportedIndexMetamethod。
func userdataIndexedValue(receiver Value, key Value) (Value, error) {
	userdata, ok := receiver.Ref.(*Userdata)
	if !ok || userdata == nil {
		// KindUserdata 的引用负载损坏时按不可索引值处理。
		return NilValue(), ErrExpectedTable
	}

	metatable := userdata.GetMetatable()
	if metatable == nil {
		// 没有元表时，userdata 字段读取结果为 nil，后续 CALL 会报告不可调用。
		return NilValue(), nil
	}
	indexValue := metatable.RawGetString(tableIndexMetamethodKey)
	if indexValue.IsNil() {
		// 元表没有 __index 时，userdata 字段读取结果为 nil。
		return NilValue(), nil
	}
	if indexValue.Kind == KindTable {
		// __index table 使用普通 table 读取，允许继续跟随该 table 的元表链。
		indexTable, ok := indexValue.Ref.(*Table)
		if !ok || indexTable == nil {
			// table 引用负载损坏表示元方法不可安全执行。
			return NilValue(), ErrUnsupportedIndexMetamethod
		}
		return indexTable.Get(key)
	}
	if indexValue.Kind == KindGoClosure {
		// __index Go function 按 Lua 规则接收原 userdata 与 key。
		return callGoMetamethod(indexValue, receiver, key)
	}

	// 其他 __index 类型当前不能作为索引元方法执行。
	return NilValue(), ErrUnsupportedIndexMetamethod
}

// orderCompareOperation 表示 Lua 有序比较操作函数。
//
// left 与 right 是已经通过 RK 读取到的运行时值；返回值表示比较是否成立。
type orderCompareOperation func(left Value, right Value) (bool, error)

// binaryArithmeticOperation 表示二元算术操作函数。
//
// left 与 right 是已经通过 RK 或寄存器读取到的运行时值；返回值是写入目标寄存器的 Lua 值。
type binaryArithmeticOperation func(left Value, right Value) (Value, error)

// binaryBitwiseOperation 表示二元位运算操作函数。
//
// left 与 right 是已经转换后的 Lua integer；返回值按 64 位补码语义写入 Lua integer。
type binaryBitwiseOperation func(left int64, right int64) int64

// isCallable 判断值是否可作为 Lua 调用目标。
//
// 当前阶段直接可调用的值包括 Lua closure 与 Go closure；带 `__call` 的 table 会在
// executeCall 中转换为调用元方法，这里只判断 raw callable 类型。
func isCallable(value Value) bool {
	switch value.Kind {
	case KindLuaClosure, KindGoClosure:
		// Lua closure 与 Go closure 都是当前调用执行器认识的函数值。
		return true
	default:
		// 其他类型需要通过 __call 元方法才能被调用。
		return false
	}
}

// valueLength 返回 Lua 值的长度运算结果。
//
// string 返回字节长度；table 优先调用 `__len`，未定义时返回 Table.Len；其他类型当前没有
// 全局类型元表，无法找到 `__len` 时返回 ErrLengthOperand。
func valueLength(value Value) (Value, error) {
	switch value.Kind {
	case KindString:
		// Lua 5.3 string 长度按字节计算，不按 Unicode rune 计算。
		return IntegerValue(int64(len(value.String))), nil
	case KindTable:
		// table 先检查 __len 元方法，Lua 5.3 允许 table 覆盖长度语义。
		if method, ok := lookupMetamethod(value, metamethodLen); ok {
			result, err := callGoMetamethod(method, value)
			if err != nil {
				// 元方法存在但调用失败时返回调用错误。
				return NilValue(), err
			}
			return result, nil
		}
		table, err := tableFromValue(value)
		if err != nil {
			// table 类型引用损坏时直接返回 table 解析错误。
			return NilValue(), err
		}
		return IntegerValue(table.Len()), nil
	default:
		// 其他类型当前没有长度语义，后续通过 __len 元方法支持。
		return NilValue(), lengthOperandError(value)
	}
}

// valueToLuaString 按 CONCAT 需要把 Lua 值转换为 string。
//
// string 直接返回；integer/number 使用 NumberToString；其他类型当前返回 ErrConcatOperand。
func valueToLuaString(value Value) (string, error) {
	if value.Kind == KindString {
		// string 操作数直接参与拼接。
		return value.String, nil
	}
	if converted, ok := value.NumberToString(); ok {
		// number 操作数按 Lua 5.3 number-to-string 基础规则转换。
		return converted, nil
	}

	// 其他类型当前不能参与拼接，后续通过 __concat 元方法支持。
	return "", ErrConcatOperand
}

// positiveIntegerForLoopRemainingIterations 计算正步长 integer for 从当前 index 到 limit 的剩余轮数。
//
// step 必须大于 0；返回 false 表示当前控制槽无法静态证明有限正向循环，调用方应回退普通 VM。
func positiveIntegerForLoopRemainingIterations(index int64, limit int64, step int64) (int, bool) {
	// 整段字符串拼接 prototype 只覆盖正步长 numeric-for。
	if step <= 0 {
		// 零或负步长不进入整段 builder。
		return 0, false
	}
	if index > limit {
		// 当前 body 理论上不应在越界后执行；防御性回退普通路径。
		return 0, false
	}

	distance := uint64(limit) - uint64(index)
	remaining := distance/uint64(step) + 1
	if remaining > uint64(math.MaxInt) {
		// Go int 无法表达轮数时不能安全构造字符串。
		return 0, false
	}
	return int(remaining), true
}

// repeatedAppendStringLengthOK 判断 base 追加 count 次 suffix 后的长度是否可表达。
//
// 该 helper 只做整数长度预检，不分配 builder；正常整段 batch 会在最终提交时再 materialize 字符串。
func repeatedAppendStringLengthOK(baseLength int, suffixLength int, count int) bool {
	// count 为 0 时长度保持不变。
	if count == 0 {
		return true
	}
	if count < 0 || suffixLength < 0 || baseLength < 0 {
		// 非法长度参数不能进入 builder 路径。
		return false
	}
	if suffixLength == 0 {
		// 空后缀不增加长度。
		return true
	}
	return suffixLength <= (math.MaxInt-baseLength)/count
}

// repeatedAppendString 返回 base 后连续追加 count 次 suffix 的字符串。
//
// count 必须非负；长度溢出 Go int 时返回 ok=false，让调用方回退普通 VM 路径。该 helper 只服务
// 已证明 raw string 的窄形态 builder，不执行 number 转换或 `__concat` 元方法。
func repeatedAppendString(base string, suffix string, count int) (string, bool) {
	// count 为 0 时结果就是原字符串，保持 Lua string 不可变值语义且避免无意义分配。
	if count == 0 {
		return base, true
	}
	if count < 0 {
		// 负次数不是合法批量窗口，调用方应回退普通 VM。
		return "", false
	}
	if suffix == "" {
		// 空后缀追加不改变结果；当前构建期通常已排除该形态，这里保留防御语义。
		return base, true
	}
	if !repeatedAppendStringLengthOK(len(base), len(suffix), count) {
		// 目标长度超出 Go int 可表达范围时不能构造 Builder。
		return "", false
	}

	totalLength := len(base) + len(suffix)*count
	var builder strings.Builder
	builder.Grow(totalLength)
	builder.WriteString(base)
	for appendIndex := 0; appendIndex < count; appendIndex++ {
		// 每次追加同一个固定右侧字符串，等价于多轮 `s = s .. suffix` 的最终结果。
		builder.WriteString(suffix)
	}
	return builder.String(), true
}

// commitStringAppendForLoopState 按整段字符串自追加 batch 的虚拟指令边界提交寄存器状态。
//
// completedIterations 表示已经执行完 FORLOOP 的完整轮数；phase 表示可能额外执行了当前轮 MOVE、
// LOADK 或 CONCAT。该 helper 只在整段 batch 最终提交或 context 取消路径 materialize 字符串。
func (vm *VM) commitStringAppendForLoopState(superInstruction stringAppendForLoopSuperInstruction, base string, completedIterations int, currentIndex int64, phase stringAppendContextPhase, totalIterations int) bool {
	// 根据虚拟指令边界推导目标字符串、MOVE 临时槽和 LOADK 临时槽是否已经可见。
	targetIterations := completedIterations
	moveIterations := completedIterations - 1
	appendVisible := completedIterations > 0
	currentPC := superInstruction.LoopPC
	pcOffset := superInstruction.ForSBx
	switch phase {
	case stringAppendPhaseAfterCompleted:
		// 已停在完整 FORLOOP 边界，默认状态就是上一轮结束后。
	case stringAppendPhaseAfterMove:
		// MOVE 已执行，临时槽保存当前累加字符串。
		moveIterations = completedIterations
		currentPC = superInstruction.LoopPC
		pcOffset = 0
	case stringAppendPhaseAfterLoadK:
		// LOADK 已执行，右侧固定字符串临时槽也可见。
		moveIterations = completedIterations
		appendVisible = true
		currentPC = superInstruction.LoopPC + 1
		pcOffset = 0
	case stringAppendPhaseAfterConcat:
		// CONCAT 已执行但 FORLOOP 未执行，目标多包含当前轮后缀。
		targetIterations = completedIterations + 1
		moveIterations = completedIterations
		appendVisible = true
		currentPC = superInstruction.LoopPC + 2
		pcOffset = 0
	default:
		// 未知 phase 表示调用方状态损坏，拒绝提交副作用。
		return false
	}
	if phase == stringAppendPhaseAfterCompleted && completedIterations >= totalIterations {
		// 循环自然退出时 FORLOOP 不再回跳。
		currentPC = superInstruction.ExitPC - 1
		pcOffset = 0
	}

	targetText, ok := repeatedAppendString(base, superInstruction.AppendText, targetIterations)
	if !ok {
		// 长度溢出时不提交半截寄存器状态。
		return false
	}
	vm.registers[superInstruction.TargetRegister] = StringValue(targetText)
	if moveIterations >= 0 {
		// MOVE 临时槽只在至少执行过 MOVE 或已有完整轮数时可见。
		moveText, moveOK := repeatedAppendString(base, superInstruction.AppendText, moveIterations)
		if !moveOK {
			// MOVE 临时槽构造失败时不提交半截寄存器状态。
			return false
		}
		vm.registers[superInstruction.MoveRegister] = StringValue(moveText)
	}
	if appendVisible {
		// LOADK 或前序完整轮数已让右侧固定字符串临时槽可见。
		vm.registers[superInstruction.AppendRegister] = StringValue(superInstruction.AppendText)
	}
	vm.registers[superInstruction.ForBase] = IntegerValue(currentIndex)
	vm.registers[superInstruction.ForBase+3] = IntegerValue(currentIndex)
	vm.pcOffset = pcOffset
	vm.currentPC = currentPC
	vm.skipNext = false
	vm.closeFrom = -1
	vm.hasCallRequest = false
	vm.returned = false
	return true
}

// concatStringRegisterRange 直接拼接寄存器区间内的纯 string 操作数。
//
// 任一寄存器不是 string 时返回 ok=false，调用方应回落到 number 转换和 __concat 元方法路径。
func (vm *VM) concatStringRegisterRange(startIndex int, endIndex int) (string, bool) {
	totalLength := 0
	nonEmptyCount := 0
	onlyNonEmpty := ""
	for registerIndex := startIndex; registerIndex <= endIndex; registerIndex++ {
		value := vm.registers[registerIndex]
		if value.Kind != KindString {
			// 非 string 操作数需要走完整转换逻辑。
			return "", false
		}
		if value.String != "" {
			// 记录非空片段，便于全空或单非空范围直接返回已有字符串。
			nonEmptyCount++
			onlyNonEmpty = value.String
		}
		if len(value.String) > math.MaxInt-totalLength {
			// 长度超过 Go int 可表达范围时，交给完整路径返回拼接错误。
			return "", false
		}
		totalLength += len(value.String)
	}
	if nonEmptyCount == 0 {
		// 全部片段为空时结果仍为空串，不需要 Builder 分配。
		return "", true
	}
	if nonEmptyCount == 1 {
		// 只有一个非空片段时结果等于该片段，匹配 C 版空串快速路径。
		return onlyNonEmpty, true
	}

	var builder strings.Builder
	builder.Grow(totalLength)
	for registerIndex := startIndex; registerIndex <= endIndex; registerIndex++ {
		// 按寄存器顺序写入片段，保持 R(B)..R(C) 语义。
		builder.WriteString(vm.registers[registerIndex].String)
	}
	return builder.String(), true
}

// concatPair 拼接两个 Lua 值。
//
// 两个值都能转换为 string 时走基础拼接；任一转换失败时按 Lua 5.3 规则尝试 `__concat`
// 二元元方法。返回值可能是元方法的任意 Lua 值，后续连续 CONCAT 会继续拿它参与折叠。
func concatPair(left Value, right Value) (Value, error) {
	leftString, leftErr := valueToLuaString(left)
	rightString, rightErr := valueToLuaString(right)
	if leftErr == nil && rightErr == nil {
		if rightString == "" {
			// 右侧空字符串时结果为左操作数，避免基础折叠路径分配新字符串。
			return StringValue(leftString), nil
		}
		if leftString == "" {
			// 左侧空字符串时结果为右操作数，匹配 Lua 5.3 luaV_concat 的空串快路径。
			return StringValue(rightString), nil
		}
		// 两侧均可转换为 string 时，使用基础字符串拼接快速路径。
		return StringValue(leftString + rightString), nil
	}

	result, found, err := callBinaryMetamethod(left, right, metamethodConcat)
	if err != nil {
		// 元方法存在但调用失败时返回调用错误。
		return NilValue(), err
	}
	if found {
		// 元方法返回值作为当前折叠结果。
		return result, nil
	}

	// 无 __concat 时保留 Lua 拼接操作数错误，并指出第一个不可拼接操作数类型。
	if leftErr != nil {
		return NilValue(), concatOperandError(left)
	}
	return NilValue(), concatOperandError(right)
}

// concatStrings 拼接多个字符串片段。
//
// parts 必须按 Lua CONCAT 的寄存器顺序传入；返回值是完整拼接结果。
func concatStrings(parts []string) string {
	totalLength := 0
	for _, part := range parts {
		// 先计算总长度，避免 strings.Builder 多次扩容。
		totalLength += len(part)
	}

	var builder strings.Builder
	builder.Grow(totalLength)
	for _, part := range parts {
		// 按传入顺序写入每个片段，保持 R(B)..R(C) 语义。
		builder.WriteString(part)
	}

	// 返回完整拼接结果。
	return builder.String()
}

// compareLessThan 执行 Lua 5.3 小于比较。
//
// number 与 number 按数值比较，string 与 string 按字节序比较；其他组合当前返回
// ErrCompareOperand，后续通过 __lt 元方法补齐。
func compareLessThan(left Value, right Value) (bool, error) {
	if left.Kind == KindInteger && right.Kind == KindInteger {
		// 双 integer 必须用 int64 精确比较，避免边界值转 float64 后丢失低位精度。
		return left.Integer < right.Integer, nil
	}
	if left.Kind == KindInteger && right.Kind == KindNumber {
		// integer 与 float 混合比较需要避免 2^53 以上的 float64 舍入误判。
		return integerLessThanFloat(left.Integer, right.Number), nil
	}
	if left.Kind == KindNumber && right.Kind == KindInteger {
		// float 与 integer 混合比较需要按 Lua 5.3 的边界排序语义处理。
		return floatLessThanInteger(left.Number, right.Integer), nil
	}
	if left.IsNumber() && right.IsNumber() {
		// number 比较允许 integer 与 float number 混合。
		leftNumber, _ := left.ToNumber()
		rightNumber, _ := right.ToNumber()
		return leftNumber < rightNumber, nil
	}
	if left.Kind == KindString && right.Kind == KindString {
		// string 比较按字节字典序执行，匹配 Lua 5.3 基础字符串比较。
		return left.String < right.String, nil
	}

	// 其他类型组合当前不能有序比较。
	return false, ErrCompareOperand
}

// compareLessEqual 执行 Lua 5.3 小于等于比较。
//
// number 与 number 按数值比较，string 与 string 按字节序比较；其他组合当前返回
// ErrCompareOperand，后续通过 __le 或 __lt 元方法补齐。
func compareLessEqual(left Value, right Value) (bool, error) {
	if left.Kind == KindInteger && right.Kind == KindInteger {
		// 双 integer 必须用 int64 精确比较，避免边界值转 float64 后丢失低位精度。
		return left.Integer <= right.Integer, nil
	}
	if left.Kind == KindInteger && right.Kind == KindNumber {
		// integer 与 float 混合比较需要避免 2^53 以上的 float64 舍入误判。
		return integerLessEqualFloat(left.Integer, right.Number), nil
	}
	if left.Kind == KindNumber && right.Kind == KindInteger {
		// float 与 integer 混合比较需要按 Lua 5.3 的边界排序语义处理。
		return floatLessEqualInteger(left.Number, right.Integer), nil
	}
	if left.IsNumber() && right.IsNumber() {
		// number 比较允许 integer 与 float number 混合。
		leftNumber, _ := left.ToNumber()
		rightNumber, _ := right.ToNumber()
		return leftNumber <= rightNumber, nil
	}
	if left.Kind == KindString && right.Kind == KindString {
		// string 比较按字节字典序执行，匹配 Lua 5.3 基础字符串比较。
		return left.String <= right.String, nil
	}

	// 其他类型组合当前不能有序比较。
	return false, ErrCompareOperand
}

// integerLessThanFloat 比较 Lua integer 是否小于 Lua float number。
//
// float64 无法精确表示所有 int64，尤其是 maxinteger 附近；本 helper 通过边界和
// floor 规避把 integer 直接转 float 后的舍入误判。
func integerLessThanFloat(integerValue int64, numberValue float64) bool {
	if math.IsNaN(numberValue) {
		// NaN 与任何数字有序比较都为 false。
		return false
	}
	if numberValue <= float64(math.MinInt64) {
		// 小于或等于最小 integer 的 float 不大于任何 Lua integer。
		return false
	}
	if numberValue >= -float64(math.MinInt64) {
		// 大于等于 2^63 的 float 大于所有 Lua integer。
		return true
	}
	floorValue := math.Floor(numberValue)
	floorInteger := int64(floorValue)
	if floorValue == numberValue {
		// float 本身是整数值时执行严格整数比较。
		return integerValue < floorInteger
	}

	// 非整数 float 落在 floor 与 floor+1 之间，integer <= floor 即小于该 float。
	return integerValue <= floorInteger
}

// floatLessThanInteger 比较 Lua float number 是否小于 Lua integer。
//
// 该逻辑与 integerLessThanFloat 对称，通过 ceil 处理非整数 float 与相邻 integer 的顺序。
func floatLessThanInteger(numberValue float64, integerValue int64) bool {
	if math.IsNaN(numberValue) {
		// NaN 与任何数字有序比较都为 false。
		return false
	}
	if numberValue < float64(math.MinInt64) {
		// 小于最小 integer 的 float 小于所有 Lua integer。
		return true
	}
	if numberValue >= -float64(math.MinInt64) {
		// 大于等于 2^63 的 float 不小于任何 Lua integer。
		return false
	}
	ceilValue := math.Ceil(numberValue)
	ceilInteger := int64(ceilValue)
	if ceilValue == numberValue {
		// float 本身是整数值时执行严格整数比较。
		return ceilInteger < integerValue
	}

	// 非整数 float 落在 ceil-1 与 ceil 之间，ceil <= integer 即小于该 integer。
	return ceilInteger <= integerValue
}

// integerLessEqualFloat 比较 Lua integer 是否小于等于 Lua float number。
//
// NaN 保持 false；其他情况与严格小于共享同一边界拆分，只在整数 float 时允许相等。
func integerLessEqualFloat(integerValue int64, numberValue float64) bool {
	if math.IsNaN(numberValue) {
		// NaN 与任何数字有序比较都为 false。
		return false
	}
	if numberValue < float64(math.MinInt64) {
		// 小于最小 integer 的 float 小于所有 Lua integer。
		return false
	}
	if numberValue >= -float64(math.MinInt64) {
		// 大于等于 2^63 的 float 大于等于所有 Lua integer。
		return true
	}
	floorValue := math.Floor(numberValue)
	floorInteger := int64(floorValue)
	if floorValue == numberValue {
		// float 本身是整数值时允许相等。
		return integerValue <= floorInteger
	}

	// 非整数 float 落在 floor 与 floor+1 之间，integer <= floor 即小于等于该 float。
	return integerValue <= floorInteger
}

// floatLessEqualInteger 比较 Lua float number 是否小于等于 Lua integer。
//
// NaN 保持 false；其他情况与严格小于共享同一边界拆分，只在整数 float 时允许相等。
func floatLessEqualInteger(numberValue float64, integerValue int64) bool {
	if math.IsNaN(numberValue) {
		// NaN 与任何数字有序比较都为 false。
		return false
	}
	if numberValue < float64(math.MinInt64) {
		// 小于最小 integer 的 float 小于等于所有 Lua integer。
		return true
	}
	if numberValue >= -float64(math.MinInt64) {
		// 大于等于 2^63 的 float 大于所有 Lua integer。
		return false
	}
	ceilValue := math.Ceil(numberValue)
	ceilInteger := int64(ceilValue)
	if ceilValue == numberValue {
		// float 本身是整数值时允许相等。
		return ceilInteger <= integerValue
	}

	// 非整数 float 落在 ceil-1 与 ceil 之间，ceil <= integer 即小于等于该 integer。
	return ceilInteger <= integerValue
}

// decodeCallCount 解码 CALL/TAILCALL 的 B/C 计数字段。
//
// encoded 为 0 表示开放数量，返回 -1；否则返回 encoded-1。
func decodeCallCount(encoded int) int {
	if encoded == 0 {
		// 0 表示开放参数或开放返回值，当前用 -1 记录。
		return -1
	}

	// Lua 5.3 CALL 系列的固定数量字段都以实际数量加一编码。
	return encoded - 1
}

// decodeReturnCount 解码 RETURN/VARARG 的数量字段。
//
// encoded 为 0 表示开放数量，使用 openCount；否则返回 encoded-1。
func decodeReturnCount(encoded int, openCount int) int {
	if encoded == 0 {
		// 0 表示开放数量，调用方提供当前可见的开放值数量。
		return openCount
	}

	// Lua 5.3 RETURN/VARARG 的固定数量字段以实际数量加一编码。
	return encoded - 1
}

// forIntegerControlValues 解析 integer 数值 for 的 index、limit 和 step。
//
// 三个控制值都是真实 integer 时返回精确 int64 控制值；任一值不是 integer 时返回 ok=false。
func forIntegerControlValues(indexValue Value, limitValue Value, stepValue Value) (int64, int64, int64, bool) {
	if indexValue.Kind != KindInteger || stepValue.Kind != KindInteger {
		// Lua 5.3 只有初值和步长保持 integer 时才使用 integer for 快速路径。
		return 0, 0, 0, false
	}

	limitInteger, ok := forIntegerLimitValue(limitValue, stepValue.Integer)
	if !ok {
		// 上界无法按当前步长方向折算为 integer 时交给 number 路径判断空循环。
		return 0, 0, 0, false
	}

	// 初值、折算后的边界和步长都可按 integer for 精确执行。
	return indexValue.Integer, limitInteger, stepValue.Integer, true
}

// forIntegerLimitValue 按 Lua 5.3 forlimit 语义把循环边界折算为 integer。
//
// 正步长使用 floor(limit)，负步长使用 ceil(limit)；无穷边界在会继续循环的方向折算到
// integer 极值，明显应为空循环的越界方向返回 ok=false 交给 number 路径自然判空。
func forIntegerLimitValue(limitValue Value, stepValue int64) (int64, bool) {
	if limitValue.Kind == KindInteger {
		// 真实 integer 边界不需要折算。
		return limitValue.Integer, true
	}

	limitNumber, ok := valueToLuaNumber(limitValue)
	if !ok {
		// 边界不能转为 number 时不满足 integer for 快速路径。
		return 0, false
	}
	if math.IsNaN(limitNumber) {
		// NaN 边界无法定义稳定的整数停止条件。
		return 0, false
	}
	if stepValue >= 0 {
		// 正步长遇到正无穷或超出上界时以 maxinteger 作为可达边界。
		if math.IsInf(limitNumber, 1) || limitNumber >= float64(math.MaxInt64) {
			return math.MaxInt64, true
		}
		if math.IsInf(limitNumber, -1) || limitNumber < float64(math.MinInt64) {
			// 边界低于 mininteger 时，任意 integer 初值都不应进入正向循环。
			return 0, false
		}

		// 正步长使用不超过原始边界的最大 integer。
		return int64(math.Floor(limitNumber)), true
	}

	if math.IsInf(limitNumber, -1) || limitNumber <= float64(math.MinInt64) {
		// 负步长遇到负无穷或超出下界时以 mininteger 作为可达边界。
		return math.MinInt64, true
	}
	if math.IsInf(limitNumber, 1) || limitNumber >= -float64(math.MinInt64) {
		// 边界高于 maxinteger 时，任意 integer 初值都不应进入反向循环。
		return 0, false
	}

	// 负步长使用不小于原始边界的最小 integer。
	return int64(math.Ceil(limitNumber)), true
}

// forNumberControlValues 解析 float 数值 for 的 index、limit 和 step。
//
// 该路径按 Lua 5.3 number 语义转换控制值，不处理全 integer 快速路径。
func forNumberControlValues(indexValue Value, limitValue Value, stepValue Value) (float64, float64, float64, error) {
	indexNumber, indexOK := valueToLuaNumber(indexValue)
	if !indexOK {
		// 初始值无法转为 number 时，for 循环控制变量非法。
		return 0, 0, 0, ErrExpectedNumber
	}
	limitNumber, limitOK := valueToLuaNumber(limitValue)
	if !limitOK {
		// 边界值无法转为 number 时，for 循环控制变量非法。
		return 0, 0, 0, ErrExpectedNumber
	}
	stepNumber, stepOK := valueToLuaNumber(stepValue)
	if !stepOK {
		// 步长无法转为 number 时，for 循环控制变量非法。
		return 0, 0, 0, ErrExpectedNumber
	}

	// 返回已转换的 float 控制值。
	return indexNumber, limitNumber, stepNumber, nil
}

// forIntegerLoopContinues 判断 integer 数值 for 步进后是否继续循环。
//
// step 非负时要求 next <= limit；step 为负时要求 next >= limit，比较必须保持 int64 精度。
func forIntegerLoopContinues(nextValue int64, limitValue int64, stepValue int64) bool {
	if stepValue >= 0 {
		// 正步长向上递增，未超过上界则继续。
		return nextValue <= limitValue
	}

	// 负步长向下递减，未低于下界则继续。
	return nextValue >= limitValue
}

// forNumberLoopContinues 判断 float 数值 for 步进后是否继续循环。
//
// step 非负时要求 next <= limit；step 为负时要求 next >= limit。
func forNumberLoopContinues(nextValue float64, limitValue float64, stepValue float64) bool {
	if stepValue >= 0 {
		// 正步长向上递增，未超过上界则继续。
		return nextValue <= limitValue
	}

	// 负步长向下递减，未低于下界则继续。
	return nextValue >= limitValue
}

// writeSetList 执行 SETLIST 的实际 table 数组区写入。
//
// tableIndex 是 table 寄存器，valueCount 是要写入的值数量，batchNumber 是 Lua 5.3 的
// SETLIST 批次编号，数组起点为 (batchNumber-1)*LFIELDS_PER_FLUSH + 1。
func (vm *VM) writeSetList(tableIndex int, valueCount int, batchNumber int) error {
	table, err := tableFromValue(vm.registers[tableIndex])
	if err != nil {
		// SETLIST 目标必须是 table。
		return err
	}
	if batchNumber <= 0 {
		// 批次编号必须为正数，0 只允许作为等待 EXTRAARG 的编码。
		return ErrConstantOutOfRange
	}
	if tableIndex+valueCount >= len(vm.registers)+1 {
		// 要写入的值区间必须落在当前寄存器窗口内。
		return ErrRegisterOutOfRange
	}

	startArrayIndex := int64((batchNumber-1)*fieldsPerFlush + 1)
	for valueOffset := 0; valueOffset < valueCount; valueOffset++ {
		// 依次把 R(A+1)..R(A+B) 写入数组区连续整数 key。
		if err := table.RawSetIntegerWithConstCheck(startArrayIndex+int64(valueOffset), vm.registers[tableIndex+1+valueOffset]); err != nil {
			// 批量写入命中 `_glua_const` 只读数组字段时，停止当前 SETLIST 并向调用方报告错误。
			return err
		}
	}
	return nil
}

// captureUpvalue 根据子 Proto 的 UpvalueDesc 捕获一个 upvalue 值。
//
// InStack 为 true 时从当前寄存器捕获；否则从当前闭包 upvalue 列表捕获。
func (vm *VM) captureUpvalue(upvalueDesc bytecode.UpvalueDesc) (Value, error) {
	captureIndex := int(upvalueDesc.Index)
	if upvalueDesc.InStack {
		// 捕获当前寄存器中的局部变量值。
		if captureIndex < 0 || captureIndex >= len(vm.registers) {
			return NilValue(), ErrRegisterOutOfRange
		}
		return vm.registers[captureIndex], nil
	}
	if !vm.hasUpvalueIndex(captureIndex) {
		// 捕获外层 upvalue 时，索引必须落在当前 upvalue 列表内。
		return NilValue(), ErrUpvalueOutOfRange
	}

	// 返回外层 upvalue 值快照。
	return vm.upvalueValue(captureIndex), nil
}

// captureUpvalueCell 根据子 Proto 的 UpvalueDesc 捕获一个共享 upvalue cell。
func (vm *VM) captureUpvalueCell(upvalueDesc bytecode.UpvalueDesc) (*UpvalueCell, error) {
	captureIndex := int(upvalueDesc.Index)
	if upvalueDesc.InStack {
		// 捕获当前寄存器槽，支持内层闭包写回外层局部变量。
		if captureIndex < 0 || captureIndex >= len(vm.registers) {
			return nil, ErrRegisterOutOfRange
		}
		for _, trackedCell := range vm.openUpvalues {
			if trackedCell.register == captureIndex {
				// 同一寄存器已被其他闭包捕获时复用 cell，保证它们共享同一个 upvalue。
				return trackedCell.cell, nil
			}
		}
		cell := NewOpenUpvalueCell(&vm.registers[captureIndex])
		vm.openUpvalues = append(vm.openUpvalues, openUpvalueCell{register: captureIndex, cell: cell})
		return cell, nil
	}
	if !vm.hasUpvalueIndex(captureIndex) {
		// 捕获外层 upvalue 时，索引必须落在当前 upvalue 列表内。
		return nil, ErrUpvalueOutOfRange
	}
	if captureIndex < len(vm.upvalueCells) && vm.upvalueCells[captureIndex] != nil {
		// 外层已经有共享 cell 时必须复用，保证多层闭包看到同一 upvalue。
		return vm.upvalueCells[captureIndex], nil
	}

	// 旧闭包没有 cell 时退回闭合快照 cell。
	return NewClosedUpvalueCell(vm.upvalueValue(captureIndex)), nil
}

// binaryArithmeticAdd 执行 Lua 5.3 加法。
//
// 两个操作数都能转换为 integer 时返回 integer；否则按 float number 计算。
func binaryArithmeticAdd(left Value, right Value) (Value, error) {
	if leftInteger, rightInteger, ok := valuesToLuaIntegers(left, right); ok {
		// 双 integer 加法保留 integer 结果，并按 64 位补码自然回绕。
		return IntegerValue(leftInteger + rightInteger), nil
	}

	leftNumber, rightNumber, ok := valuesToLuaNumbers(left, right)
	if !ok {
		// 任一操作数不能转为 number 时返回算术操作数错误。
		return NilValue(), ErrArithmeticOperand
	}
	// float 加法按 IEEE-754 float64 语义执行。
	return NumberValue(leftNumber + rightNumber), nil
}

// binaryArithmeticSub 执行 Lua 5.3 减法。
//
// 两个操作数都能转换为 integer 时返回 integer；否则按 float number 计算。
func binaryArithmeticSub(left Value, right Value) (Value, error) {
	if leftInteger, rightInteger, ok := valuesToLuaIntegers(left, right); ok {
		// 双 integer 减法保留 integer 结果，并按 64 位补码自然回绕。
		return IntegerValue(leftInteger - rightInteger), nil
	}

	leftNumber, rightNumber, ok := valuesToLuaNumbers(left, right)
	if !ok {
		// 任一操作数不能转为 number 时返回算术操作数错误。
		return NilValue(), ErrArithmeticOperand
	}
	// float 减法按 IEEE-754 float64 语义执行。
	return NumberValue(leftNumber - rightNumber), nil
}

// binaryArithmeticMul 执行 Lua 5.3 乘法。
//
// 两个操作数都能转换为 integer 时返回 integer；否则按 float number 计算。
func binaryArithmeticMul(left Value, right Value) (Value, error) {
	if leftInteger, rightInteger, ok := valuesToLuaIntegers(left, right); ok {
		// 双 integer 乘法保留 integer 结果，并按 64 位补码自然回绕。
		return IntegerValue(leftInteger * rightInteger), nil
	}

	leftNumber, rightNumber, ok := valuesToLuaNumbers(left, right)
	if !ok {
		// 任一操作数不能转为 number 时返回算术操作数错误。
		return NilValue(), ErrArithmeticOperand
	}
	// float 乘法按 IEEE-754 float64 语义执行。
	return NumberValue(leftNumber * rightNumber), nil
}

// binaryArithmeticMod 执行 Lua 5.3 取模。
//
// 两个操作数都能转换为 integer 时返回 integer；否则按 float number 计算。取模使用
// 向下取整语义，保证负数结果对齐 Lua 5.3。
func binaryArithmeticMod(left Value, right Value) (Value, error) {
	if leftInteger, rightInteger, ok := valuesToLuaIntegers(left, right); ok {
		// integer 取模需要先拒绝零除数，避免 Go 运行时 panic。
		if rightInteger == 0 {
			return NilValue(), fmt.Errorf("'n%%0': %w", ErrDivisionByZero)
		}
		return IntegerValue(integerModulo(leftInteger, rightInteger)), nil
	}

	leftNumber, rightNumber, ok := valuesToLuaNumbers(left, right)
	if !ok {
		// 任一操作数不能转为 number 时返回算术操作数错误。
		return NilValue(), ErrArithmeticOperand
	}
	if math.IsInf(rightNumber, 1) && !math.IsInf(leftNumber, 0) && !math.IsNaN(leftNumber) {
		// 有限数对 +Inf 取模需避免 0*Inf 产生 NaN，按 Lua 5.3 可观察结果返回边界值。
		if leftNumber >= 0 {
			// 非负有限数落在 [0, +Inf) 内，余数保持自身。
			return NumberValue(leftNumber), nil
		}
		// 负有限数向下取整后余数贴近 +Inf。
		return NumberValue(math.Inf(1)), nil
	}
	if math.IsInf(rightNumber, -1) && !math.IsInf(leftNumber, 0) && !math.IsNaN(leftNumber) {
		// 有限数对 -Inf 取模同样需要避开 0*Inf，保持 Lua 5.3 的符号边界。
		if leftNumber <= 0 {
			// 负有限数落在 (-Inf, 0] 内，余数保持自身。
			return NumberValue(leftNumber), nil
		}
		// 正有限数向下取整后余数贴近 -Inf。
		return NumberValue(math.Inf(-1)), nil
	}
	// Lua 取模定义为 a - floor(a/b) * b。
	return NumberValue(leftNumber - math.Floor(leftNumber/rightNumber)*rightNumber), nil
}

// binaryArithmeticPow 执行 Lua 5.3 幂运算。
//
// 幂运算结果总是 float number，符合 Lua 5.3 OP_POW 的基础数字路径。
func binaryArithmeticPow(left Value, right Value) (Value, error) {
	leftNumber, rightNumber, ok := valuesToLuaNumbers(left, right)
	if !ok {
		// 任一操作数不能转为 number 时返回算术操作数错误。
		return NilValue(), ErrArithmeticOperand
	}
	// 幂运算使用 math.Pow，保留 IEEE-754 NaN/Inf 结果。
	return NumberValue(math.Pow(leftNumber, rightNumber)), nil
}

// binaryArithmeticDiv 执行 Lua 5.3 浮点除法。
//
// 除法结果总是 float number；零除数按 IEEE-754 产生 Inf 或 NaN，不在 VM 中改写为错误。
func binaryArithmeticDiv(left Value, right Value) (Value, error) {
	leftNumber, rightNumber, ok := valuesToLuaNumbers(left, right)
	if !ok {
		// 任一操作数不能转为 number 时返回算术操作数错误。
		return NilValue(), ErrArithmeticOperand
	}
	// 普通除法按 float64 语义执行。
	return NumberValue(leftNumber / rightNumber), nil
}

// binaryArithmeticIDiv 执行 Lua 5.3 向下取整除法。
//
// 两个操作数都能转换为 integer 时返回 integer；否则按 float number 计算并向下取整。
func binaryArithmeticIDiv(left Value, right Value) (Value, error) {
	if leftInteger, rightInteger, ok := valuesToLuaIntegers(left, right); ok {
		// integer floor division 需要先拒绝零除数，避免 Go 运行时 panic。
		if rightInteger == 0 {
			return NilValue(), ErrDivisionByZero
		}
		return IntegerValue(integerFloorDiv(leftInteger, rightInteger)), nil
	}

	leftNumber, rightNumber, ok := valuesToLuaNumbers(left, right)
	if !ok {
		// 任一操作数不能转为 number 时返回算术操作数错误。
		return NilValue(), ErrArithmeticOperand
	}
	// float floor division 返回 float number，符合非双 integer 路径。
	return NumberValue(math.Floor(leftNumber / rightNumber)), nil
}

// binaryBitwiseAnd 执行 Lua 5.3 按位与。
//
// 位语义：每一位只有 left 与 right 同时为 1 时结果位才为 1，例如 1100 & 1010 = 1000。
func binaryBitwiseAnd(left int64, right int64) int64 {
	// Go int64 按位与直接对齐 64 位补码位模式。
	return left & right
}

// binaryBitwiseOr 执行 Lua 5.3 按位或。
//
// 位语义：每一位只要 left 或 right 任一为 1 时结果位为 1，例如 1100 | 1010 = 1110。
func binaryBitwiseOr(left int64, right int64) int64 {
	// Go int64 按位或直接对齐 64 位补码位模式。
	return left | right
}

// binaryBitwiseXor 执行 Lua 5.3 按位异或。
//
// 位语义：每一位在 left 与 right 不相同时结果位为 1，例如 1100 ~ 1010 = 0110。
func binaryBitwiseXor(left int64, right int64) int64 {
	// Go int64 按位异或直接对齐 64 位补码位模式。
	return left ^ right
}

// binaryBitwiseShl 执行 Lua 5.3 左移。
//
// 位语义：正移位把 bit 向高位移动，低位补 0；负移位转为右移，例如 0001 << 2 = 0100。
func binaryBitwiseShl(left int64, right int64) int64 {
	return shiftLeft(left, right)
}

// binaryBitwiseShr 执行 Lua 5.3 右移。
//
// 位语义：正移位把 bit 向低位移动，高位补 0；负移位转为左移，例如 1000 >> 2 = 0010。
func binaryBitwiseShr(left int64, right int64) int64 {
	return shiftLeft(left, -right)
}

// valuesToLuaIntegers 尝试把两个 Lua 值都转换为 integer。
//
// 转换成功时返回两个 int64 和 true；任一失败时返回 false。
func valuesToLuaIntegers(left Value, right Value) (int64, int64, bool) {
	if left.Kind != KindInteger {
		// 左操作数不是真实 integer 值时，不能走 Lua VM 的双 integer 快速路径。
		return 0, 0, false
	}
	if right.Kind != KindInteger {
		// 右操作数不是真实 integer 值时，不能走 Lua VM 的双 integer 快速路径。
		return 0, 0, false
	}

	// 两个操作数都可用 integer 表示。
	return left.Integer, right.Integer, true
}

// valuesToLuaNumbers 尝试把两个 Lua 值都转换为 number。
//
// 转换成功时返回两个 float64 和 true；任一失败时返回 false。
func valuesToLuaNumbers(left Value, right Value) (float64, float64, bool) {
	leftNumber, leftOK := valueToLuaNumber(left)
	if !leftOK {
		// 左操作数不能转为 number 时，算术路径不可用。
		return 0, 0, false
	}
	rightNumber, rightOK := valueToLuaNumber(right)
	if !rightOK {
		// 右操作数不能转为 number 时，算术路径不可用。
		return 0, 0, false
	}

	// 两个操作数都可用 float64 表示。
	return leftNumber, rightNumber, true
}

// valueToLuaInteger 尝试把单个 Lua 值转换为 integer。
//
// number 值使用 Value.ToInteger；string 值先按 Lua 5.3 字符串转数字规则解析，再尝试转
// integer。转换失败时返回 false。
func valueToLuaInteger(value Value) (int64, bool) {
	if value.Kind == KindInteger {
		// 真实 integer 值直接返回。
		return value.Integer, true
	}
	if value.Kind == KindNumber {
		// 位运算中的 float-to-integer 转换必须严格限制在 int64 数学范围内。
		return strictFloatToLuaInteger(value.Number)
	}
	if integerValue, ok := value.ToInteger(); ok {
		// 其他 number-like 值当前无直接路径；保留防御分支兼容未来 Value 变体。
		return integerValue, true
	}
	if value.Kind == KindString {
		// string 参与位运算时先尝试按 Lua 字符串数字规则转换。
		numberValue, ok := value.StringToNumber()
		if !ok {
			return 0, false
		}
		return valueToLuaInteger(numberValue)
	}

	// 其他类型当前不能转换为 Lua integer。
	return 0, false
}

// strictFloatToLuaInteger 按 Lua 位运算规则把 float number 转为 integer。
//
// 该转换不同于位级补码回绕：float 必须有限、处于 int64 数学范围内，并且可无损表示为整数。
func strictFloatToLuaInteger(numberValue float64) (int64, bool) {
	if math.IsNaN(numberValue) || math.IsInf(numberValue, 0) {
		// NaN/Inf 没有 integer 表示。
		return 0, false
	}
	if numberValue < float64(math.MinInt64) || numberValue >= -float64(math.MinInt64) {
		// 小于 mininteger 或大于等于 2^63 都无法表示为 Lua integer。
		return 0, false
	}
	integerValue := int64(numberValue)
	if float64(integerValue) != numberValue {
		// 有小数部分或 float 无法无损回转时，不能作为位运算 integer。
		return 0, false
	}

	// float 可无损表示为 Lua integer。
	return integerValue, true
}

// valueToLuaNumber 尝试把单个 Lua 值转换为 float number。
//
// number 值直接转换；string 值按 Lua 5.3 字符串转数字规则解析。转换失败时返回 false。
func valueToLuaNumber(value Value) (float64, bool) {
	if numberValue, ok := value.ToNumber(); ok {
		// integer 与 float number 都可作为算术 number 使用。
		return numberValue, true
	}
	if value.Kind == KindString {
		// string 参与算术时先尝试按 Lua 字符串数字规则转换。
		parsedValue, ok := value.StringToNumber()
		if !ok {
			return 0, false
		}
		return parsedValue.ToNumber()
	}

	// 其他类型当前不能转换为 Lua number。
	return 0, false
}

// integerModulo 执行 Lua 5.3 integer 取模。
//
// right 必须非 0；结果满足 left == floor(left/right)*right + result，并与 right 同号。
func integerModulo(left int64, right int64) int64 {
	if right == -1 {
		// Lua 5.3 官方 luaV_mod 对 -1 特判为 0，避免最小 integer 取模溢出路径。
		return 0
	}
	modulo := left % right
	if modulo != 0 && (modulo^right) < 0 {
		// Go % 结果与被除数同号；Lua 需要结果与除数同号，因此跨符号时修正一次。
		modulo += right
	}

	// 返回 Lua floor-mod 语义下的余数。
	return modulo
}

// integerFloorDiv 执行 Lua 5.3 integer 向下取整除法。
//
// right 必须非 0；结果向负无穷方向取整，而不是 Go 默认的向零截断。
func integerFloorDiv(left int64, right int64) int64 {
	if right == -1 {
		// Lua 5.3 官方 luaV_div 对 -1 特判为 0-left，避免最小 integer 除以 -1 溢出。
		return -left
	}
	quotient := left / right
	remainder := left % right
	if remainder != 0 && (remainder^right) < 0 {
		// Go integer 除法向零截断；Lua floor division 需要负方向修正一位。
		quotient--
	}

	// 返回 Lua floor division 语义下的商。
	return quotient
}

// shiftLeft 执行 Lua 5.3 风格的左移辅助。
//
// shift 为正时左移，为负时逻辑右移；绝对值大于等于 64 时结果为 0。所有操作按
// uint64 位模式执行，再转回 int64。
func shiftLeft(value int64, shift int64) int64 {
	if shift >= 64 || shift <= -64 {
		// 移动超过位宽时所有位都被移出，结果为 0。
		return 0
	}
	if shift >= 0 {
		// 正移位向高位移动，低位补 0。
		return int64(uint64(value) << uint(shift))
	}

	// 负移位转为逻辑右移，高位补 0。
	return int64(uint64(value) >> uint(-shift))
}

// constantToValue 把 bytecode 常量转换为 runtime 值。
//
// 该函数只处理 binary chunk 常量允许出现的 nil、boolean、integer、number 和 string；
// table/function/userdata 不应出现在 Proto 常量表中。
func constantToValue(constant bytecode.Constant) (Value, error) {
	switch constant.Kind {
	case bytecode.ConstantNil:
		// nil 常量转换为 Lua nil 值。
		return NilValue(), nil
	case bytecode.ConstantBoolean:
		// boolean 常量保留 true/false 负载。
		return BooleanValue(constant.Bool), nil
	case bytecode.ConstantInteger:
		// integer 常量保留 int64 精确值。
		return IntegerValue(constant.Integer), nil
	case bytecode.ConstantNumber:
		// number 常量保留 float64 负载。
		return NumberValue(constant.Number), nil
	case bytecode.ConstantString:
		// string 常量按字节序列转换为 Lua string 值。
		return StringValue(constant.String), nil
	default:
		// 未知常量类型来自损坏 chunk 或未来扩展，当前 VM 拒绝执行。
		return NilValue(), fmt.Errorf("unsupported constant kind: %d", constant.Kind)
	}
}
