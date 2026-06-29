package bytecode

import (
	"bytes"
	"errors"
	"testing"
)

// TestLoadProto 验证 Lua 5.3 函数原型可以按 LoadFunction 顺序完整读取。
//
// 该测试覆盖基础元数据、指令数组、常量表、upvalue、子 Proto、行号和局部变量调试信息。
func TestLoadProto(t *testing.T) {
	child := NewProto("")
	child.LineDefined = 10
	child.LastLineDefined = 11
	child.NumParams = 1
	child.MaxStackSize = 2
	child.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}

	proto := NewProto("@main.lua")
	proto.LineDefined = 1
	proto.LastLineDefined = 20
	proto.NumParams = 2
	proto.IsVararg = true
	proto.MaxStackSize = 5
	proto.Code = []Instruction{CreateABx(OpLoadK, 0, 0), CreateABC(OpReturn, 0, 2, 0)}
	proto.Constants = []Constant{StringConstant("hello"), IntegerConstant(7)}
	proto.Upvalues = []UpvalueDesc{{Name: "_ENV", InStack: true, Index: 0}}
	proto.Protos = []*Proto{child}
	proto.LineInfo = []int{2, 3}
	proto.LocalVars = []LocalVar{{Name: "x", StartPC: 0, EndPC: 2}}

	loadedProto, err := LoadProto(bytes.NewReader(AppendProto(nil, proto)))
	if err != nil {
		// 合法 Proto 不应返回错误。
		t.Fatalf("load proto failed: %v", err)
	}

	// 基础函数元数据必须完整保留。
	if loadedProto.Source != "@main.lua" || loadedProto.LineDefined != 1 || loadedProto.LastLineDefined != 20 || loadedProto.NumParams != 2 || !loadedProto.IsVararg || loadedProto.MaxStackSize != 5 {
		t.Fatalf("proto metadata mismatch: %#v", loadedProto)
	}

	// 指令、常量和 Debug 表必须按原始顺序读取。
	if len(loadedProto.Code) != 2 || loadedProto.Code[0].OpCode() != OpLoadK || len(loadedProto.Constants) != 2 || loadedProto.LineInfo[1] != 3 {
		t.Fatalf("proto arrays mismatch: code=%#v constants=%#v lineinfo=%#v", loadedProto.Code, loadedProto.Constants, loadedProto.LineInfo)
	}

	// upvalue 捕获信息和 Debug 名称必须在 LoadDebug 后合并到同一结构。
	if len(loadedProto.Upvalues) != 1 || loadedProto.Upvalues[0].Name != "_ENV" || !loadedProto.Upvalues[0].InStack || loadedProto.Upvalues[0].Index != 0 {
		t.Fatalf("upvalue mismatch: %#v", loadedProto.Upvalues)
	}

	// 子 Proto source 为空时必须继承父 source，保持 Lua 5.3 LoadFunction 语义。
	if len(loadedProto.Protos) != 1 || loadedProto.Protos[0].Source != "@main.lua" {
		t.Fatalf("child proto source mismatch: %#v", loadedProto.Protos)
	}

	// 局部变量生命周期必须完整保留。
	if len(loadedProto.LocalVars) != 1 || !loadedProto.LocalVars[0].ActiveAt(1) {
		t.Fatalf("local var mismatch: %#v", loadedProto.LocalVars)
	}
}

// TestLoadProtoRejectsUpvalueNameOverflow 验证 Debug 段不会越界回填 upvalue 名称。
//
// 官方 C 实现假设数据可信；Go loader 对损坏 chunk 显式返回错误，避免 slice 越界。
func TestLoadProtoRejectsUpvalueNameOverflow(t *testing.T) {
	proto := NewProto("@main.lua")
	proto.Upvalues = []UpvalueDesc{}

	chunk := appendNullableLuaString(nil, proto.Source)
	chunk = appendInt(chunk, 0)
	chunk = appendInt(chunk, 0)
	chunk = append(chunk, 0, 0, 2)
	chunk = appendInstructions(chunk, nil)
	chunk = AppendConstants(chunk, nil)
	chunk = appendUpvalues(chunk, nil)
	chunk = appendChildProtos(chunk, nil, proto.Source)
	chunk = appendInt(chunk, 0)
	chunk = appendInt(chunk, 0)
	chunk = appendInt(chunk, 1)
	chunk = appendNullableLuaString(chunk, "extra")

	_, err := LoadProto(bytes.NewReader(chunk))
	if !errors.Is(err, ErrInvalidChunkData) {
		// upvalue 名称数量超过 upvalue 数量时必须返回 chunk data 错误。
		t.Fatalf("expected invalid chunk data error, got %v", err)
	}
}

// TestLoadProtoRebuildsLocalRegisters 验证 binary chunk 读回后会按生命周期重建 local 寄存器。
//
// Lua 5.3 locvar 不存寄存器号；前一个 local 失效后，后续 local 应复用低位槽，确保
// debug.getinfo("n") 在 dump/load 后能从 CALL 指令寄存器反查到局部函数名。
func TestLoadProtoRebuildsLocalRegisters(t *testing.T) {
	proto := NewProto("@locals.lua")
	proto.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}
	proto.MaxStackSize = 3
	proto.LocalVars = []LocalVar{
		{Name: "old", Register: 0, StartPC: 0, EndPC: 1},
		{Name: "a", Register: 0, StartPC: 1, EndPC: 4},
		{Name: "b", Register: 1, StartPC: 1, EndPC: 4},
		{Name: "next", Register: 0, StartPC: 4, EndPC: 6},
	}

	loadedProto, err := LoadProto(bytes.NewReader(AppendProto(nil, proto)))
	if err != nil {
		// 合法 Proto 不应返回错误。
		t.Fatalf("load proto failed: %v", err)
	}
	if len(loadedProto.LocalVars) != 4 {
		// 局部变量数量必须保持不变。
		t.Fatalf("local vars mismatch: %#v", loadedProto.LocalVars)
	}
	if loadedProto.LocalVars[0].Register != 0 ||
		loadedProto.LocalVars[1].Register != 0 ||
		loadedProto.LocalVars[2].Register != 1 ||
		loadedProto.LocalVars[3].Register != 0 {
		// 生命周期不重叠的 local 必须复用低位寄存器，重叠 local 必须占用后续槽。
		t.Fatalf("rebuilt registers mismatch: %#v", loadedProto.LocalVars)
	}
}

// TestLoadProtoRebuildsLocalFunctionRegistersFromClosure 验证 local function 会从 CLOSURE 指令恢复真实寄存器。
//
// Lua 5.3 locvar 不保存寄存器号；官方测试 db.lua 在 dump/load 后依赖 debug.getinfo("n")
// 从 CALL 寄存器反查 local function 名称。若 local function 被低槽重建，会把 foo 误判为 aux。
func TestLoadProtoRebuildsLocalFunctionRegistersFromClosure(t *testing.T) {
	// 构造一个拥有高寄存器 local function 的 Proto，模拟同作用域已有多个长期存活 local 后再声明函数。
	childFoo := NewProto("@locals.lua")
	childFoo.MaxStackSize = 2
	childFoo.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}
	childAux := NewProto("@locals.lua")
	childAux.MaxStackSize = 2
	childAux.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}

	proto := NewProto("@locals.lua")
	proto.Code = make([]Instruction, 13)
	proto.Code[10] = CreateABx(OpClosure, 16, 0)
	proto.Code[11] = CreateABx(OpClosure, 17, 1)
	proto.Code[12] = CreateABC(OpReturn, 0, 1, 0)
	proto.MaxStackSize = 20
	proto.Protos = []*Proto{childFoo, childAux}
	proto.LocalVars = []LocalVar{
		{Name: "debug", Register: 0, StartPC: 0, EndPC: 13},
		{Name: "state", Register: 1, StartPC: 0, EndPC: 13},
		{Name: "foo", Register: 16, StartPC: 10, EndPC: 12},
		{Name: "aux", Register: 17, StartPC: 11, EndPC: 12},
	}

	loadedProto, err := LoadProto(bytes.NewReader(AppendProto(nil, proto)))
	if err != nil {
		// 合法 Proto 不应返回错误。
		t.Fatalf("load proto failed: %v", err)
	}
	if len(loadedProto.LocalVars) != 4 {
		// 局部变量数量必须保持不变。
		t.Fatalf("local vars mismatch: %#v", loadedProto.LocalVars)
	}
	if loadedProto.LocalVars[2].Register != 16 || loadedProto.LocalVars[3].Register != 17 {
		// local function 必须优先使用 CLOSURE A 的真实高寄存器，避免 dump/load 后 Debug 名称错配。
		t.Fatalf("local function registers mismatch: %#v", loadedProto.LocalVars)
	}
}

// TestLoadProtoRebuildsAssignedClosureRegisterFromPreviousInstruction 验证赋值式局部闭包会从 StartPC 前一条恢复寄存器。
//
// `local f = function()` 的 locvar 生命周期可能从 CLOSURE 后一条开始；若只看 StartPC，
// dump/load 后会把该闭包降级到低槽，导致调用 `f()` 时 debug.getinfo(2).name 误判。
func TestLoadProtoRebuildsAssignedClosureRegisterFromPreviousInstruction(t *testing.T) {
	// 构造 StartPC-1 为 CLOSURE 的局部闭包，覆盖官方 db.lua 早期 repeat 块中的 local f 形态。
	child := NewProto("@locals.lua")
	child.MaxStackSize = 2
	child.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}

	proto := NewProto("@locals.lua")
	proto.Code = make([]Instruction, 8)
	proto.Code[5] = CreateABx(OpClosure, 9, 0)
	proto.Code[6] = CreateABC(OpMove, 10, 9, 0)
	proto.Code[7] = CreateABC(OpReturn, 0, 1, 0)
	proto.MaxStackSize = 12
	proto.Protos = []*Proto{child}
	proto.LocalVars = []LocalVar{
		{Name: "g", Register: 0, StartPC: 0, EndPC: 8},
		{Name: "f", Register: 9, StartPC: 6, EndPC: 8},
	}

	loadedProto, err := LoadProto(bytes.NewReader(AppendProto(nil, proto)))
	if err != nil {
		// 合法 Proto 不应返回错误。
		t.Fatalf("load proto failed: %v", err)
	}
	if loadedProto.LocalVars[1].Register != 9 {
		// 赋值式局部闭包必须从 StartPC-1 的 CLOSURE A 恢复真实寄存器。
		t.Fatalf("assigned closure register mismatch: %#v", loadedProto.LocalVars)
	}
}

// TestLoadProtoRejectsTruncatedCode 验证截断指令数组会被拒绝。
//
// 指令数量字段声明了更多指令但数据不足时，loader 不能返回半成品 Proto。
func TestLoadProtoRejectsTruncatedCode(t *testing.T) {
	chunk := appendNullableLuaString(nil, "@main.lua")
	chunk = appendInt(chunk, 0)
	chunk = appendInt(chunk, 0)
	chunk = append(chunk, 0, 0, 2)
	chunk = appendInt(chunk, 1)
	chunk = append(chunk, 0x01, 0x02)

	_, err := LoadProto(bytes.NewReader(chunk))
	if !errors.Is(err, ErrInvalidChunkData) {
		// 截断 code 必须被归类为 chunk data 错误。
		t.Fatalf("expected invalid chunk data error, got %v", err)
	}
}
