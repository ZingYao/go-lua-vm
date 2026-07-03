package bytecode

// ConstantKind 表示 Lua 5.3 Proto 常量表中的常量类型。
//
// 该类型是 Go 侧对 TValue 常量子集的安全表达，只覆盖 binary chunk 常量表允许
// 持久化的 nil、boolean、integer、number 和 string，运行期 table/function/userdata
// 不应作为 Proto 常量直接存放在这里。
type ConstantKind uint8

const (
	// ConstantNil 表示 Lua nil 常量。
	ConstantNil ConstantKind = iota
	// ConstantBoolean 表示 Lua boolean 常量。
	ConstantBoolean
	// ConstantInteger 表示 Lua 5.3 integer 常量。
	ConstantInteger
	// ConstantNumber 表示 Lua 5.3 float number 常量。
	ConstantNumber
	// ConstantString 表示 Lua string 常量。
	ConstantString
)

// Constant 表示 Lua 5.3 函数原型常量表中的一个常量。
//
// 字段采用分类型存储，避免 interface{} 在早期 bytecode 层泄露不稳定运行时值系统；
// 调用方必须先读取 Kind，再访问对应字段。
type Constant struct {
	// Kind 标识当前常量的 Lua 类型。
	Kind ConstantKind
	// Bool 保存 ConstantBoolean 对应的布尔值。
	Bool bool
	// Integer 保存 ConstantInteger 对应的整数值。
	Integer int64
	// Number 保存 ConstantNumber 对应的浮点值。
	Number float64
	// String 保存 ConstantString 对应的字符串字节序列。
	String string
}

// NilConstant 创建 Lua nil 常量。
//
// nil 常量没有附加值，返回值只设置 Kind 字段。
func NilConstant() Constant {
	// nil 常量只需要类型标记，其他字段保持零值即可。
	return Constant{Kind: ConstantNil}
}

// BooleanConstant 创建 Lua boolean 常量。
//
// 入参 value 直接对应 Lua 层 true 或 false。
func BooleanConstant(value bool) Constant {
	// boolean 常量保存布尔值，并通过 Kind 区分零值 false 与非 boolean 常量。
	return Constant{Kind: ConstantBoolean, Bool: value}
}

// IntegerConstant 创建 Lua integer 常量。
//
// 入参 value 使用 int64 表示 Lua 5.3 的整数语义，后续运行时会按项目配置映射到 Lua Integer。
func IntegerConstant(value int64) Constant {
	// integer 常量保存精确整数，不经过 float64 转换，避免精度损失。
	return Constant{Kind: ConstantInteger, Integer: value}
}

// NumberConstant 创建 Lua number 浮点常量。
//
// 入参 value 使用 float64 表示默认 Lua 5.3 lua_Number 语义。
func NumberConstant(value float64) Constant {
	// number 常量保存浮点值，整数常量必须使用 IntegerConstant 单独表达。
	return Constant{Kind: ConstantNumber, Number: value}
}

// StringConstant 创建 Lua string 常量。
//
// 入参 value 按 Lua 字符串字节序列保存，允许包含 UTF-8 或任意二进制字节。
func StringConstant(value string) Constant {
	// string 常量保存 Go 字符串；Lua 字符串长度语义后续按字节计算。
	return Constant{Kind: ConstantString, String: value}
}

// UpvalueDesc 描述 Lua 函数原型中的一个 upvalue。
//
// 该结构对齐 Lua 5.3 Upvaldesc，用于编译器输出和 Debug 信息读取；运行期 open/closed
// upvalue 的实际值生命周期由 runtime 包管理。
type UpvalueDesc struct {
	// Name 是 upvalue 的调试名称，空字符串表示源码或 chunk 未提供名称。
	Name string
	// InStack 表示该 upvalue 是否捕获自外层函数栈寄存器。
	InStack bool
	// Index 表示捕获位置；InStack 为 true 时是寄存器索引，否则是外层 upvalue 索引。
	Index uint8
}

// LocalVar 描述 Lua 函数原型中的一个局部变量调试范围。
//
// StartPC 是变量开始可见的第一条指令位置，EndPC 是变量失效的第一条指令位置。
type LocalVar struct {
	// Name 是局部变量名称，空字符串表示调试信息缺失或匿名内部变量。
	Name string
	// Register 是当前 Go 编译器分配的寄存器索引；官方 binary chunk 不携带该字段，加载 chunk 时保持零值。
	Register int
	// StartPC 是变量开始生效的指令位置。
	StartPC int
	// EndPC 是变量停止生效的指令位置。
	EndPC int
}

// ActiveAt 判断局部变量在指定 pc 处是否可见。
//
// 入参 pc 是当前执行指令的下标；返回 true 表示 pc 落在 [StartPC, EndPC) 范围内。
func (localVar LocalVar) ActiveAt(pc int) bool {
	// pc 必须大于等于 StartPC 且小于 EndPC，保持 Lua 5.3 LocVar 的半开区间语义。
	return pc >= localVar.StartPC && pc < localVar.EndPC
}

// Proto 表示 Lua 5.3 函数原型。
//
// 该结构对齐 lobject.h 中 Proto 的稳定语义，但使用 Go slice 表达动态数组；
// closure cache、GC 链表和运行期闭包对象不放在 bytecode 层。
type Proto struct {
	// NumParams 是固定参数个数。
	NumParams uint8
	// IsVararg 表示函数是否接收可变参数。
	IsVararg bool
	// MaxStackSize 是该函数需要的最大寄存器数量。
	MaxStackSize uint8
	// LineDefined 是函数定义起始行号，缺失时为 0。
	LineDefined int
	// LastLineDefined 是函数定义结束行号，缺失时为 0。
	LastLineDefined int
	// Source 是调试用源码名称或 chunk 名称。
	Source string
	// Constants 是函数常量表，对齐 Proto.k。
	Constants []Constant
	// Code 是函数指令数组，对齐 Proto.code。
	Code []Instruction
	// Protos 是嵌套函数原型数组，对齐 Proto.p。
	Protos []*Proto
	// LineInfo 把每条指令映射到源码行号，对齐 Proto.lineinfo。
	LineInfo []int
	// LocalVars 保存局部变量调试信息，对齐 Proto.locvars。
	LocalVars []LocalVar
	// Upvalues 保存 upvalue 调试与捕获信息，对齐 Proto.upvalues。
	Upvalues []UpvalueDesc
	// inlineCode 保存 codegen 最常见的短函数指令槽，仅在显式 opt-in 后作为 Code 的底层数组。
	inlineCode [2]Instruction
	// inlineLineInfo 保存 codegen 最常见的短函数行号槽，仅在显式 opt-in 后作为 LineInfo 的底层数组。
	inlineLineInfo [2]int
	// inlineConstants 保存 codegen 最常见的单常量槽，仅在显式 opt-in 后作为 Constants 的底层数组。
	inlineConstants [1]Constant
}

// NewProto 创建一个空 Lua 函数原型。
//
// 返回的 Proto 只初始化 Source 字段，其他切片保持 nil，便于 chunk loader 和 compiler
// 按实际内容逐步追加，避免不必要分配。
func NewProto(source string) *Proto {
	// 新原型只绑定源码名称，其他字段由编译器或 chunk loader 后续填充。
	return &Proto{Source: source}
}

// PrepareInlineCodeLineInfo 为 codegen 短函数准备指令和行号表容量。
//
// capacity 表示调用方预期的最小指令数；该方法必须在写入 Code 或 LineInfo 前调用。容量不超过
// 内嵌短槽时复用 Proto 自身存储，超过短槽时退回普通切片分配。已有内容时保持原切片不变，避免误调用
// 丢失 binary chunk loader 或手写 Proto 已经追加的字节码。
func (proto *Proto) PrepareInlineCodeLineInfo(capacity int) {
	if proto == nil || capacity <= 0 {
		// nil 或无效容量没有可准备对象，保持调用方状态不变。
		return
	}
	if len(proto.Code) != 0 || len(proto.LineInfo) != 0 {
		// 已有指令或行号时不能重绑定底层数组，否则会丢失已经构造的字节码。
		return
	}
	if capacity <= len(proto.inlineCode) {
		// 短函数使用 Proto 内嵌槽，减少每个子函数两段小切片底层数组分配。
		proto.Code = proto.inlineCode[:0]
		proto.LineInfo = proto.inlineLineInfo[:0]
		return
	}

	// 超过短槽容量时按调用方请求预留普通切片，保持扩展函数的追加性能。
	proto.Code = make([]Instruction, 0, capacity)
	proto.LineInfo = make([]int, 0, capacity)
}

// PrepareInlineConstants 为 codegen 单常量函数准备常量表容量。
//
// capacity 表示调用方预期的最小常量数；该方法必须在写入 Constants 前调用。容量不超过内嵌短槽时
// 复用 Proto 自身存储，超过短槽时退回普通切片分配。已有内容时保持原切片不变，避免误调用丢失
// binary chunk loader 或手写 Proto 已经追加的常量表。
func (proto *Proto) PrepareInlineConstants(capacity int) {
	if proto == nil || capacity <= 0 {
		// nil 或无效容量没有可准备对象，保持调用方状态不变。
		return
	}
	if len(proto.Constants) != 0 {
		// 已有常量时不能重绑定底层数组，否则会丢失已经构造的常量表。
		return
	}
	if capacity <= len(proto.inlineConstants) {
		// 单常量函数使用 Proto 内嵌槽，减少每个子函数一段小切片底层数组分配。
		proto.Constants = proto.inlineConstants[:0]
		return
	}

	// 超过短槽容量时按调用方请求预留普通切片，保持多常量函数的追加性能。
	proto.Constants = make([]Constant, 0, capacity)
}

// StripDebug 深拷贝 Proto 并剥离 Lua 5.3 调试信息。
//
// proto 必须非 nil；返回值会保留 code、constant、child proto、函数行定义、local 生命周期和
// upvalue 捕获位置，但清空 source、lineinfo、local 名称和 upvalue 名称。该语义服务
// `string.dump(fn, true)` 与 `luac -s`，并且不会修改调用方持有的原始 Proto。
func StripDebug(proto *Proto) *Proto {
	if proto == nil {
		// nil Proto 没有可剥离内容，保持 nil 便于递归调用方处理损坏输入。
		return nil
	}

	// 深拷贝 Proto 主体，避免 strip 操作污染正在执行的 closure 调试信息。
	clone := *proto
	clone.Source = ""
	clone.Constants = append([]Constant(nil), proto.Constants...)
	clone.Code = append([]Instruction(nil), proto.Code...)
	clone.LineInfo = nil
	clone.LocalVars = append([]LocalVar(nil), proto.LocalVars...)
	for index := range clone.LocalVars {
		// 保留寄存器生命周期用于 stripped chunk 的 temporary local 枚举，但清空源码名称。
		clone.LocalVars[index].Name = ""
	}
	clone.Upvalues = append([]UpvalueDesc(nil), proto.Upvalues...)
	for index := range clone.Upvalues {
		// strip 只清空调试名称，保留 InStack 和 Index 捕获语义。
		clone.Upvalues[index].Name = ""
	}
	clone.Protos = make([]*Proto, 0, len(proto.Protos))
	for _, child := range proto.Protos {
		// 子 Proto 递归剥离，保持 CLOSURE 索引结构不变。
		clone.Protos = append(clone.Protos, StripDebug(child))
	}
	return &clone
}

// AddConstant 追加一个常量并返回常量表索引。
//
// 返回值可直接用于 LOADK 的 Bx 字段；调用方仍需检查是否超过 MaxArgBx 或 RK 限制。
func (proto *Proto) AddConstant(constant Constant) int {
	// 追加常量后返回最后一个元素下标，保持 Lua 常量表零基索引语义。
	proto.Constants = append(proto.Constants, constant)
	return len(proto.Constants) - 1
}

// AddInstruction 追加一条指令并返回指令 pc。
//
// 返回值用于跳转回填、行号表同步和局部变量生命周期记录。
func (proto *Proto) AddInstruction(instruction Instruction) int {
	// 追加指令后返回当前指令位置，Lua VM 的 pc 以指令数组下标为基础。
	proto.Code = append(proto.Code, instruction)
	return len(proto.Code) - 1
}

// AddChild 追加一个嵌套函数原型并返回子原型索引。
//
// 返回值可用于 CLOSURE 指令的 Bx 字段，对齐 Lua 5.3 Proto.p 数组索引。
func (proto *Proto) AddChild(child *Proto) int {
	// 追加子原型后返回其索引，编译器用该索引生成 CLOSURE 指令。
	proto.Protos = append(proto.Protos, child)
	return len(proto.Protos) - 1
}
