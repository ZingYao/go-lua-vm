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
	if proto.Source != "@main.lua" || len(proto.Constants) != 0 || len(proto.Code) != 0 || len(proto.Protos) != 0 {
		t.Fatalf("new proto mismatch: source=%q constants=%d code=%d protos=%d", proto.Source, len(proto.Constants), len(proto.Code), len(proto.Protos))
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
