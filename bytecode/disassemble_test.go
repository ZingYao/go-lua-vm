package bytecode

import (
	"strings"
	"testing"
)

// TestDisassembleProto 验证 Proto 反汇编输出包含关键调试信息。
//
// 该测试不要求完整匹配大段文本，只锁定格式中后续调试依赖的稳定片段。
func TestDisassembleProto(t *testing.T) {
	proto := NewProto("@debug.lua")
	proto.LineDefined = 1
	proto.LastLineDefined = 3
	proto.NumParams = 1
	proto.MaxStackSize = 3
	proto.Code = []Instruction{
		CreateABx(OpLoadK, 0, 0),
		CreateABC(OpAdd, 1, 0, RKAsK(1)),
		CreateABC(OpReturn, 1, 2, 0),
	}
	proto.Constants = []Constant{IntegerConstant(7), NumberConstant(1.5)}
	proto.Upvalues = []UpvalueDesc{{Name: "_ENV", InStack: true, Index: 0}}
	proto.LocalVars = []LocalVar{{Name: "x", StartPC: 0, EndPC: 3}}
	proto.LineInfo = []int{1, 2, 3}

	output := DisassembleProto(proto)
	expectedFragments := []string{
		"main <@debug.lua:1,3> params=1 vararg=false maxstack=3",
		"[0000] line=1 LOADK",
		"[0001] line=2 ADD       A=1 B=R(0) C=K(1)",
		"constants (2):",
		"[0] integer(7)",
		"[1] number(1.5)",
		"upvalues (1):",
		"name=\"_ENV\" instack=true index=0",
		"locals (1):",
		"name=\"x\" pc=[0,3)",
	}

	for _, expectedFragment := range expectedFragments {
		// 每个稳定片段都必须出现在输出中，避免反汇编格式意外退化。
		if !strings.Contains(output, expectedFragment) {
			t.Fatalf("disassembly missing %q in:\n%s", expectedFragment, output)
		}
	}
}

// TestDisassembleProtoChildren 验证反汇编会递归输出子 Proto。
//
// 子 Proto 输出是后续 CLOSURE 和嵌套函数调试定位的基础。
func TestDisassembleProtoChildren(t *testing.T) {
	child := NewProto("@child.lua")
	child.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}

	parent := NewProto("@parent.lua")
	parent.Protos = []*Proto{child}

	output := DisassembleProto(parent)
	if !strings.Contains(output, "child[0] <@child.lua:0,0>") {
		// 子 Proto 标签缺失会让嵌套函数反汇编难以定位。
		t.Fatalf("child proto disassembly missing in:\n%s", output)
	}
}
