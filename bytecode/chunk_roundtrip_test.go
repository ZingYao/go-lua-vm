package bytecode

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestDumpAndLoadBinaryChunkRoundTrip 验证完整 binary chunk 可以 dump 后再 load。
//
// 该测试覆盖 header、顶层 upvalue 数量、Proto body 和 Debug 信息的组合 roundtrip。
func TestDumpAndLoadBinaryChunkRoundTrip(t *testing.T) {
	proto := roundTripProtoFixture()

	loadedProto, err := LoadBinaryChunk(bytes.NewReader(DumpBinaryChunk(proto)))
	if err != nil {
		// dump 生成的合法 chunk 必须能被当前 loader 读回。
		t.Fatalf("load dumped binary chunk failed: %v", err)
	}
	if !equalProto(loadedProto, proto) {
		// roundtrip 后 Proto 必须保持结构和值一致。
		t.Fatalf("roundtrip proto mismatch:\n got: %#v\nwant: %#v", loadedProto, proto)
	}
}

// TestDumpAndLoadProtoRoundTrip 验证单独 Proto body 可以 dump 后再 load。
//
// 该测试覆盖不含 header 的工具级 Proto 编码入口。
func TestDumpAndLoadProtoRoundTrip(t *testing.T) {
	proto := roundTripProtoFixture()

	loadedProto, err := LoadProto(bytes.NewReader(DumpProto(nil, proto)))
	if err != nil {
		// dump 生成的合法 Proto body 必须能被当前 loader 读回。
		t.Fatalf("load dumped proto failed: %v", err)
	}
	if !equalProto(loadedProto, proto) {
		// roundtrip 后 Proto 必须保持结构和值一致。
		t.Fatalf("proto body roundtrip mismatch:\n got: %#v\nwant: %#v", loadedProto, proto)
	}
}

// TestLoadBinaryChunkRejectsTopLevelUpvalueMismatch 验证顶层 upvalue 数量不一致会被拒绝。
//
// Lua 5.3 dump 会在 header 后额外写入顶层闭包 upvalue 数量，Go loader 显式校验一致性。
func TestLoadBinaryChunkRejectsTopLevelUpvalueMismatch(t *testing.T) {
	proto := roundTripProtoFixture()
	chunk := DumpBinaryChunk(proto)
	chunk[len(AppendChunkHeader(nil))] = byte(len(proto.Upvalues) + 1)

	_, err := LoadBinaryChunk(bytes.NewReader(chunk))
	if !errors.Is(err, ErrInvalidChunkData) {
		// 数量不一致属于 chunk body 错误。
		t.Fatalf("expected invalid chunk data error, got %v", err)
	}
}

// TestLoadBinaryChunkTruncatedErrorsMentionTruncated 验证所有截断前缀都返回 truncated 错误。
//
// 官方 calls.lua 会逐字节截断 string.dump 结果并断言错误文本包含 truncated；header、
// 顶层 upvalue 数量和 Proto body 任一位置短读都必须保留该兼容片段。
func TestLoadBinaryChunkTruncatedErrorsMentionTruncated(t *testing.T) {
	chunk := DumpBinaryChunk(roundTripProtoFixture())

	for chunkSize := 1; chunkSize < len(chunk); chunkSize++ {
		_, err := LoadBinaryChunk(bytes.NewReader(chunk[:chunkSize]))
		if err == nil {
			// 截断前缀不应被误判为合法 binary chunk。
			t.Fatalf("prefix size %d loaded successfully", chunkSize)
		}
		if !errors.Is(err, ErrInvalidChunkHeader) && !errors.Is(err, ErrInvalidChunkData) {
			// 截断错误仍应归类为 chunk header 或 body 错误，供上层 load 转换。
			t.Fatalf("prefix size %d error = %v, want chunk error", chunkSize, err)
		}
		if !strings.Contains(err.Error(), "truncated") {
			// Lua 5.3 官方测试依赖该文本片段识别短读 binary chunk。
			t.Fatalf("prefix size %d error = %q, want truncated", chunkSize, err.Error())
		}
	}
}

// TestLoadBinaryChunkFillsMissingTopSource 验证 stripped chunk 缺失 source 时使用官方占位。
//
// Lua 5.3 对 `string.dump(fn, true)` 再 `load` 的函数，debug.getinfo(...).source 应返回 `=?`；
// 子 Proto 的空 source 也要继承该占位，避免顶层显示为空字符串。
func TestLoadBinaryChunkFillsMissingTopSource(t *testing.T) {
	proto := NewProto("")
	proto.MaxStackSize = 2
	proto.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}
	child := NewProto("")
	child.MaxStackSize = 2
	child.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}
	proto.Protos = []*Proto{child}

	loadedProto, err := LoadBinaryChunk(bytes.NewReader(DumpBinaryChunk(proto)))
	if err != nil {
		// 缺失 source 不应影响 binary chunk 加载。
		t.Fatalf("load stripped-source chunk failed: %v", err)
	}
	if loadedProto.Source != "=?" {
		// 顶层缺失 source 必须填充官方占位。
		t.Fatalf("top source = %q, want =?", loadedProto.Source)
	}
	if len(loadedProto.Protos) != 1 || loadedProto.Protos[0].Source != "=?" {
		// 子函数缺失 source 必须继承顶层占位。
		t.Fatalf("child source mismatch: %#v", loadedProto.Protos)
	}
}

// roundTripProtoFixture 创建覆盖主要 Proto 字段的测试原型。
//
// 该 fixture 避免多个 roundtrip 测试重复构造复杂 Proto，同时保持字段覆盖稳定。
func roundTripProtoFixture() *Proto {
	// 构造一个带子函数、常量、upvalue、lineinfo 和 locvar 的 Proto。
	child := NewProto("@roundtrip.lua")
	child.LineDefined = 8
	child.LastLineDefined = 9
	child.MaxStackSize = 2
	child.Code = []Instruction{CreateABC(OpReturn, 0, 1, 0)}
	child.Constants = []Constant{BooleanConstant(false)}
	child.LineInfo = []int{9}

	proto := NewProto("@roundtrip.lua")
	proto.LineDefined = 1
	proto.LastLineDefined = 12
	proto.NumParams = 1
	proto.IsVararg = true
	proto.MaxStackSize = 4
	proto.Code = []Instruction{CreateABx(OpLoadK, 0, 0), CreateABC(OpReturn, 0, 2, 0)}
	proto.Constants = []Constant{StringConstant("value"), NumberConstant(1.25), IntegerConstant(-3)}
	proto.Upvalues = []UpvalueDesc{{Name: "_ENV", InStack: true, Index: 0}}
	proto.Protos = []*Proto{child}
	proto.LineInfo = []int{2, 3}
	proto.LocalVars = []LocalVar{{Name: "arg", StartPC: 0, EndPC: 2}}
	return proto
}

// equalProto 比较两个 Proto 的结构内容是否一致。
//
// 子 Proto 使用递归内容比较，而不是比较指针地址，符合 load/dump roundtrip 的验收语义。
func equalProto(left *Proto, right *Proto) bool {
	// nil 情况先处理，避免后续字段访问 panic。
	if left == nil || right == nil {
		return left == right
	}
	if left.NumParams != right.NumParams || left.IsVararg != right.IsVararg || left.MaxStackSize != right.MaxStackSize || left.LineDefined != right.LineDefined || left.LastLineDefined != right.LastLineDefined || left.Source != right.Source {
		// 基础元数据不一致时，两个 Proto 内容不相等。
		return false
	}
	if !equalConstants(left.Constants, right.Constants) || !equalInstructions(left.Code, right.Code) || !equalIntSlice(left.LineInfo, right.LineInfo) || !equalLocalVars(left.LocalVars, right.LocalVars) || !equalUpvalues(left.Upvalues, right.Upvalues) {
		// 任一平铺数组不一致时，两个 Proto 内容不相等。
		return false
	}
	if len(left.Protos) != len(right.Protos) {
		// 子 Proto 数量不一致时，闭包结构不相等。
		return false
	}
	for protoIndex := range left.Protos {
		if !equalProto(left.Protos[protoIndex], right.Protos[protoIndex]) {
			// 任一子 Proto 内容不一致时，父 Proto 不相等。
			return false
		}
	}

	// 所有字段和子结构都一致。
	return true
}

// equalConstants 比较常量表内容是否一致。
//
// 常量结构只包含可比较字段，逐项比较可以避免引入反射。
func equalConstants(left []Constant, right []Constant) bool {
	// 长度不一致时，常量表不相等。
	if len(left) != len(right) {
		return false
	}
	for constantIndex := range left {
		if left[constantIndex] != right[constantIndex] {
			// 任一常量类型或值不同，常量表不相等。
			return false
		}
	}
	return true
}

// equalInstructions 比较指令数组内容是否一致。
//
// 指令数组按 pc 顺序逐项比较。
func equalInstructions(left []Instruction, right []Instruction) bool {
	// 长度不一致时，指令数组不相等。
	if len(left) != len(right) {
		return false
	}
	for instructionIndex := range left {
		if left[instructionIndex] != right[instructionIndex] {
			// 任一指令字不同，指令数组不相等。
			return false
		}
	}
	return true
}

// equalIntSlice 比较 int 切片内容是否一致。
//
// 当前用于 lineinfo roundtrip 校验。
func equalIntSlice(left []int, right []int) bool {
	// 长度不一致时，切片不相等。
	if len(left) != len(right) {
		return false
	}
	for valueIndex := range left {
		if left[valueIndex] != right[valueIndex] {
			// 任一元素不同，切片不相等。
			return false
		}
	}
	return true
}

// equalLocalVars 比较局部变量调试表内容是否一致。
//
// 局部变量结构只包含可比较字段，逐项比较即可。
func equalLocalVars(left []LocalVar, right []LocalVar) bool {
	// 长度不一致时，局部变量表不相等。
	if len(left) != len(right) {
		return false
	}
	for localVarIndex := range left {
		if left[localVarIndex] != right[localVarIndex] {
			// 任一局部变量名称或生命周期不同，局部变量表不相等。
			return false
		}
	}
	return true
}

// equalUpvalues 比较 upvalue 描述内容是否一致。
//
// upvalue 描述包含名称、捕获来源和索引，逐项比较即可。
func equalUpvalues(left []UpvalueDesc, right []UpvalueDesc) bool {
	// 长度不一致时，upvalue 表不相等。
	if len(left) != len(right) {
		return false
	}
	for upvalueIndex := range left {
		if left[upvalueIndex] != right[upvalueIndex] {
			// 任一 upvalue 描述不同，upvalue 表不相等。
			return false
		}
	}
	return true
}
