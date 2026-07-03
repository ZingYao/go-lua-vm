package bytecode

import "testing"

// TestConstantConstructors 验证常量构造函数保留 Lua 5.3 常量类型和值。
//
// 常量表是 LOADK、binary chunk 和 compiler/codegen 的共同边界，类型和值必须稳定。
func TestConstantConstructors(t *testing.T) {
	// nil 常量只应设置类型标记。
	if constant := NilConstant(); constant.Kind != ConstantNil {
		t.Fatalf("nil constant kind mismatch: got %d", constant.Kind)
	}

	// boolean 常量必须保留 false 值，不能与零值 nil 混淆。
	if constant := BooleanConstant(false); constant.Kind != ConstantBoolean || constant.Bool {
		t.Fatalf("boolean constant mismatch: kind=%d value=%v", constant.Kind, constant.Bool)
	}

	// integer 常量必须保留 int64 精度。
	if constant := IntegerConstant(1<<40 + 7); constant.Kind != ConstantInteger || constant.Integer != 1<<40+7 {
		t.Fatalf("integer constant mismatch: kind=%d value=%d", constant.Kind, constant.Integer)
	}

	// number 常量必须保留浮点值。
	if constant := NumberConstant(3.5); constant.Kind != ConstantNumber || constant.Number != 3.5 {
		t.Fatalf("number constant mismatch: kind=%d value=%f", constant.Kind, constant.Number)
	}

	// string 常量必须按字节保存字符串内容。
	if constant := StringConstant("lua\x00vm"); constant.Kind != ConstantString || constant.String != "lua\x00vm" {
		t.Fatalf("string constant mismatch: kind=%d value=%q", constant.Kind, constant.String)
	}
}

// TestLocalVarActiveAt 验证局部变量调试范围采用半开区间。
//
// Lua 5.3 LocVar 的 endpc 表示变量死亡的第一条指令，因此 endpc 本身不可见。
func TestLocalVarActiveAt(t *testing.T) {
	localVar := LocalVar{Name: "x", StartPC: 2, EndPC: 5}

	// StartPC 前变量不可见。
	if localVar.ActiveAt(1) {
		t.Fatalf("local var should be inactive before StartPC")
	}

	// StartPC 和区间内部变量可见。
	if !localVar.ActiveAt(2) || !localVar.ActiveAt(4) {
		t.Fatalf("local var should be active inside [StartPC, EndPC)")
	}

	// EndPC 表示死亡位置，变量不可见。
	if localVar.ActiveAt(5) {
		t.Fatalf("local var should be inactive at EndPC")
	}
}

// TestStripDebugClonesAndClearsDebugFields 验证 StripDebug 深拷贝并剥离调试字段。
//
// strip 必须保留执行所需的 code、constant、child proto 与 upvalue 捕获位置，同时清空
// source、lineinfo、local var 和 upvalue 名称，供 string.dump(fn, true) 与 luac -s 共用。
func TestStripDebugClonesAndClearsDebugFields(t *testing.T) {
	// 构造包含完整调试信息和子 Proto 的原型。
	proto := NewProto("@main.lua")
	proto.LineDefined = 1
	proto.LastLineDefined = 9
	proto.Constants = []Constant{StringConstant("k")}
	proto.Code = []Instruction{CreateABx(OpLoadK, 0, 0)}
	proto.LineInfo = []int{3}
	proto.LocalVars = []LocalVar{{Name: "x", Register: 0, StartPC: 0, EndPC: 1}}
	proto.Upvalues = []UpvalueDesc{{Name: "_ENV", InStack: true, Index: 0}}
	child := NewProto("@child.lua")
	child.LineInfo = []int{7}
	child.Upvalues = []UpvalueDesc{{Name: "x", InStack: true, Index: 0}}
	proto.Protos = []*Proto{child}

	stripped := StripDebug(proto)
	if stripped == nil || stripped == proto || stripped.Protos[0] == child {
		// StripDebug 必须返回独立深拷贝，避免污染原始 Proto。
		t.Fatalf("strip should deep clone proto")
	}
	if stripped.Source != "" || len(stripped.LineInfo) != 0 || stripped.LocalVars[0].Name != "" || stripped.Upvalues[0].Name != "" {
		// 调试字段必须被剥离。
		t.Fatalf("debug fields not stripped: %#v", stripped)
	}
	if stripped.LineDefined != proto.LineDefined || stripped.LastLineDefined != proto.LastLineDefined || len(stripped.Code) != 1 || len(stripped.Constants) != 1 {
		// 执行语义和函数定义行号必须保留。
		t.Fatalf("runtime fields changed by strip: %#v", stripped)
	}
	if stripped.Protos[0].Source != "" || len(stripped.Protos[0].LineInfo) != 0 || stripped.Protos[0].Upvalues[0].Name != "" {
		// 子 Proto 也必须递归剥离调试信息。
		t.Fatalf("child debug fields not stripped: %#v", stripped.Protos[0])
	}
	if proto.Source == "" || len(proto.LineInfo) == 0 || proto.Upvalues[0].Name == "" || child.Source == "" {
		// 原始 Proto 不得被 strip 修改。
		t.Fatalf("source proto mutated: proto=%#v child=%#v", proto, child)
	}
}

// TestProtoAppendHelpers 验证 Proto 追加常量、指令和子原型时返回 Lua 兼容索引。
//
// 这些索引会被 LOADK、CLOSURE 和跳转回填复用，必须保持零基数组下标语义。
func TestProtoAppendHelpers(t *testing.T) {
	proto := NewProto("@main.lua")

	// 新建 Proto 必须记录 source，并保持切片为空。
	if proto.Source != "@main.lua" || len(proto.Constants) != 0 || proto.Code != nil || len(proto.Protos) != 0 || proto.LineInfo != nil {
		t.Fatalf("new proto mismatch: source=%q constants=%d code=%v protos=%d line=%v", proto.Source, len(proto.Constants), proto.Code, len(proto.Protos), proto.LineInfo)
	}

	// 常量追加应返回零基常量表索引。
	if index := proto.AddConstant(StringConstant("hello")); index != 0 {
		t.Fatalf("first constant index mismatch: got %d", index)
	}

	// 指令追加应返回当前 pc。
	if pc := proto.AddInstruction(CreateABx(OpLoadK, 0, 0)); pc != 0 {
		t.Fatalf("first instruction pc mismatch: got %d", pc)
	}

	child := NewProto("@child.lua")

	// 子原型追加应返回零基子函数索引。
	if index := proto.AddChild(child); index != 0 || proto.Protos[0] != child {
		t.Fatalf("child proto mismatch: index=%d childStored=%v", index, proto.Protos[0] == child)
	}
}

// TestProtoPrepareInlineCodeLineInfo 验证短函数指令和行号表 opt-in 内嵌槽。
//
// NewProto 默认仍保持 nil 切片；只有 codegen 显式准备容量时，短 Code/LineInfo 才复用 Proto 内嵌槽。
func TestProtoPrepareInlineCodeLineInfo(t *testing.T) {
	proto := NewProto("@inline.lua")
	if proto.Code != nil || proto.LineInfo != nil {
		// 默认 nil 切片语义是 chunk loader 和手写 Proto 的兼容边界。
		t.Fatalf("new proto should keep nil slices: code=%v line=%v", proto.Code, proto.LineInfo)
	}

	proto.PrepareInlineCodeLineInfo(2)
	if len(proto.Code) != 0 || len(proto.LineInfo) != 0 || cap(proto.Code) != 2 || cap(proto.LineInfo) != 2 {
		// opt-in 后只预留容量，不能产生可见指令或行号。
		t.Fatalf("unexpected prepared slices: code len/cap=%d/%d line len/cap=%d/%d", len(proto.Code), cap(proto.Code), len(proto.LineInfo), cap(proto.LineInfo))
	}

	first := CreateABx(OpLoadK, 0, 0)
	second := CreateABC(OpReturn, 0, 1, 0)
	third := CreateABC(OpMove, 1, 0, 0)
	if pc := proto.AddInstruction(first); pc != 0 {
		// 第一条指令仍从 pc 0 开始。
		t.Fatalf("first pc mismatch: %d", pc)
	}
	proto.LineInfo = append(proto.LineInfo, 11)
	if pc := proto.AddInstruction(second); pc != 1 {
		// 第二条指令仍按顺序追加。
		t.Fatalf("second pc mismatch: %d", pc)
	}
	proto.LineInfo = append(proto.LineInfo, 12)
	if pc := proto.AddInstruction(third); pc != 2 {
		// 第三条指令会触发普通 slice 扩容，但 pc 语义不变。
		t.Fatalf("third pc mismatch: %d", pc)
	}
	proto.LineInfo = append(proto.LineInfo, 13)
	if len(proto.Code) != 3 || proto.Code[0] != first || proto.Code[1] != second || proto.Code[2] != third {
		// 扩容后必须保留短槽中的前两条指令顺序。
		t.Fatalf("code order changed: %+v", proto.Code)
	}
	if len(proto.LineInfo) != 3 || proto.LineInfo[0] != 11 || proto.LineInfo[1] != 12 || proto.LineInfo[2] != 13 {
		// 行号表必须与指令扩容保持同序。
		t.Fatalf("line info order changed: %+v", proto.LineInfo)
	}

	stripped := StripDebug(proto)
	if len(stripped.Code) != 3 || len(stripped.LineInfo) != 0 {
		// StripDebug 必须复制执行字节码并剥离行号表。
		t.Fatalf("unexpected stripped proto: code=%+v line=%+v", stripped.Code, stripped.LineInfo)
	}
	stripped.Code[0] = CreateABC(OpReturn, 0, 1, 0)
	if proto.Code[0] != first {
		// StripDebug 返回值不得与原 Proto 共享 Code 底层数组。
		t.Fatalf("stripped code shares storage with source")
	}
}
