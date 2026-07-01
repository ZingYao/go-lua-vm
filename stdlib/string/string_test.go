package stringlib

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/bytecode"
	"github.com/zing/go-lua-vm/runtime"
)

// TestOpenRegistersStringLibrary 验证 Open 会注册 string 库和本阶段支持的函数。
//
// 测试通过全局表读取库对象，确认每个函数都以 Go closure 暴露，后续 VM CALL 可进入标准库。
func TestOpenRegistersStringLibrary(t *testing.T) {
	// 测试先创建新的 State，避免污染其他标准库注册用例。
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// Open 失败表示 string 标准库无法作为全局库暴露。
		t.Fatalf("Open failed: %v", err)
	}

	libraryValue := state.GetGlobal("string")
	if libraryValue.Kind != runtime.KindTable {
		// string 全局变量必须指向库表。
		t.Fatalf("global string kind mismatch: %v", libraryValue.DebugString())
	}
	library, ok := libraryValue.Ref.(*runtime.Table)
	if !ok || library == nil {
		// KindTable 的引用负载必须是 runtime.Table。
		t.Fatalf("global string payload mismatch: %#v", libraryValue.Ref)
	}

	for _, name := range []string{"byte", "char", "dump", "find", "format", "gmatch", "gsub", "len", "lower", "match", "pack", "packsize", "rep", "reverse", "sub", "unpack", "upper"} {
		// 每个本阶段函数都应作为 Go closure 注册在库表上。
		functionValue := library.RawGetString(name)
		if functionValue.Kind != runtime.KindGoClosure {
			// 缺失或类型错误都会导致 VM CALL 无法进入标准库函数。
			t.Fatalf("string.%s kind mismatch: %v", name, functionValue.DebugString())
		}
	}
}

// TestByteReturnsRange 验证 string.byte 的 1-based 与负索引区间语义。
//
// Lua 字符串按字节处理，返回值数量等于裁剪后的区间长度。
func TestByteReturnsRange(t *testing.T) {
	// 传入负起点和显式终点，验证索引换算后返回最后两个字节。
	results, err := Byte(runtime.StringValue("abc"), runtime.IntegerValue(-2), runtime.IntegerValue(3))
	if err != nil {
		// 合法 byte 调用不应失败。
		t.Fatalf("Byte failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != int64('b') || results[1].Integer != int64('c') {
		// 返回值必须按原始字节顺序排列。
		t.Fatalf("Byte results mismatch: %#v", results)
	}
}

// TestByteFixedFastPath 验证 string.byte 单字节固定返回快路径。
//
// 快路径只覆盖返回 0 或 1 个值的调用形态；多字节范围必须回退完整 Byte，避免截断多返回值。
func TestByteFixedFastPath(t *testing.T) {
	// 单字节范围应直接写入结果槽。
	var dst [1]runtime.Value
	resultCount, handled, err := ByteFixed4(dst[:], runtime.StringValue("abc"), runtime.IntegerValue(2), runtime.IntegerValue(2), runtime.NilValue(), 3)
	if err != nil {
		// 合法单字节 byte 快路径不应失败。
		t.Fatalf("ByteFixed4 failed: %v", err)
	}
	if !handled || resultCount != 1 || dst[0].Kind != runtime.KindInteger || dst[0].Integer != int64('b') {
		// 快路径必须返回第二个字节的整数值。
		t.Fatalf("ByteFixed4 result mismatch: handled=%v count=%d dst=%#v", handled, resultCount, dst[0])
	}

	resultCount, handled, err = ByteFixed4(dst[:], runtime.StringValue("abc"), runtime.IntegerValue(4), runtime.IntegerValue(4), runtime.NilValue(), 3)
	if err != nil {
		// 越界空区间不应失败。
		t.Fatalf("ByteFixed4 empty range failed: %v", err)
	}
	if !handled || resultCount != 0 {
		// 空区间按 Lua 5.3 语义返回零个结果。
		t.Fatalf("ByteFixed4 empty mismatch: handled=%v count=%d", handled, resultCount)
	}

	resultCount, handled, err = ByteFixed4(dst[:], runtime.StringValue("abc"), runtime.IntegerValue(1), runtime.IntegerValue(2), runtime.NilValue(), 3)
	if err != nil {
		// 多字节范围应回退而不是失败。
		t.Fatalf("ByteFixed4 multi range failed: %v", err)
	}
	if handled || resultCount != 0 {
		// 多返回值范围不能由单槽快路径处理。
		t.Fatalf("ByteFixed4 multi range should fallback: handled=%v count=%d", handled, resultCount)
	}
}

// TestByteFixedSingleFastPath 验证 string.byte 单返回无槽位快路径。
//
// 单返回入口必须与 ByteFixed4 保持相同的返回数量、错误和多返回回退语义。
func TestByteFixedSingleFastPath(t *testing.T) {
	// 单字节范围应直接返回字节整数值。
	result, resultCount, handled, err := ByteFixed4Single(runtime.StringValue("abc"), runtime.IntegerValue(2), runtime.IntegerValue(2), runtime.NilValue(), 3)
	if err != nil {
		// 合法单字节 byte 快路径不应失败。
		t.Fatalf("ByteFixed4Single failed: %v", err)
	}
	if !handled || resultCount != 1 || result.Kind != runtime.KindInteger || result.Integer != int64('b') {
		// 快路径必须返回第二个字节的整数值。
		t.Fatalf("ByteFixed4Single result mismatch: handled=%v count=%d result=%#v", handled, resultCount, result)
	}

	result, resultCount, handled, err = ByteFixed4Single(runtime.StringValue("abc"), runtime.IntegerValue(4), runtime.IntegerValue(4), runtime.NilValue(), 3)
	if err != nil {
		// 越界空区间不应失败。
		t.Fatalf("ByteFixed4Single empty range failed: %v", err)
	}
	if !handled || resultCount != 0 || !result.IsNil() {
		// 空区间按 Lua 5.3 语义返回零个结果，固定单返回调用层再补 nil。
		t.Fatalf("ByteFixed4Single empty mismatch: handled=%v count=%d result=%#v", handled, resultCount, result)
	}

	_, resultCount, handled, err = ByteFixed4Single(runtime.StringValue("abc"), runtime.IntegerValue(1), runtime.IntegerValue(2), runtime.NilValue(), 3)
	if err != nil {
		// 多字节范围应回退而不是失败。
		t.Fatalf("ByteFixed4Single multi range failed: %v", err)
	}
	if handled || resultCount != 0 {
		// 多返回值范围不能由单返回快路径处理。
		t.Fatalf("ByteFixed4Single multi range should fallback: handled=%v count=%d", handled, resultCount)
	}
}

// TestCharBuildsBytesAndRejectsOutOfRange 验证 string.char 构造字节串和越界错误。
//
// Lua 5.3 的 string.char 参数必须落在 0..255，不能被 Go byte 截断。
func TestCharBuildsBytesAndRejectsOutOfRange(t *testing.T) {
	// 合法字节应按顺序构造字符串。
	results, err := Char(runtime.IntegerValue(65), runtime.IntegerValue(0), runtime.IntegerValue(255))
	if err != nil {
		// 合法 char 调用不应失败。
		t.Fatalf("Char failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "A\x00\xff" {
		// 返回字符串必须保留原始字节内容。
		t.Fatalf("Char result mismatch: %#v", results)
	}

	_, err = Char(runtime.IntegerValue(256))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 越界参数必须以 Lua error 形式传播。
		t.Fatalf("Char out-of-range error mismatch: %v", err)
	}
}

// TestDumpReturnsBinaryChunk 验证 string.dump 会导出 Lua closure 的 binary chunk。
//
// 测试使用 bytecode loader 回读 dump 结果，确认输出是当前项目支持的 Lua 5.3 chunk。
func TestDumpReturnsBinaryChunk(t *testing.T) {
	// 构造最小 Proto，作为 Lua closure 的可序列化主体。
	proto := bytecode.NewProto("@dump.lua")
	proto.MaxStackSize = 2
	closure := &runtime.LuaClosure{Proto: proto}

	results, err := Dump(runtime.ReferenceValue(runtime.KindLuaClosure, closure))
	if err != nil {
		// Lua closure dump 不应失败。
		t.Fatalf("Dump failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString {
		// dump 必须返回 binary chunk 字符串。
		t.Fatalf("Dump result mismatch: %#v", results)
	}

	loadedProto, err := bytecode.LoadBinaryChunk(bytes.NewReader([]byte(results[0].String)))
	if err != nil {
		// dump 结果必须能被当前 binary chunk loader 读回。
		t.Fatalf("Load dumped chunk failed: %v", err)
	}
	if loadedProto.Source != proto.Source || loadedProto.MaxStackSize != proto.MaxStackSize {
		// 回读后的关键 Proto 元数据必须保持一致。
		t.Fatalf("dumped proto mismatch: %#v", loadedProto)
	}
}

// TestDumpStripDebugReturnsStrippedBinaryChunk 验证 string.dump 的 strip 参数。
//
// 第二参数为 true 时必须剥离 source、lineinfo、local var 和 upvalue 名称，同时不能修改原 closure。
func TestDumpStripDebugReturnsStrippedBinaryChunk(t *testing.T) {
	// 构造带调试信息的 Proto，便于确认 strip 后的 binary chunk 内容。
	proto := bytecode.NewProto("@dump.lua")
	proto.MaxStackSize = 2
	proto.LineInfo = []int{4}
	proto.LocalVars = []bytecode.LocalVar{{Name: "x", Register: 0, StartPC: 0, EndPC: 1}}
	proto.Upvalues = []bytecode.UpvalueDesc{{Name: "_ENV", InStack: true, Index: 0}}
	closure := &runtime.LuaClosure{Proto: proto}

	results, err := Dump(runtime.ReferenceValue(runtime.KindLuaClosure, closure), runtime.BooleanValue(true))
	if err != nil {
		// Lua closure stripped dump 不应失败。
		t.Fatalf("Dump strip failed: %v", err)
	}
	loadedProto, err := bytecode.LoadBinaryChunk(bytes.NewReader([]byte(results[0].String)))
	if err != nil {
		// stripped dump 结果必须仍是合法 binary chunk。
		t.Fatalf("Load stripped dumped chunk failed: %v", err)
	}
	if loadedProto.Source != "=?" || len(loadedProto.LineInfo) != 0 || loadedProto.LocalVars[0].Name != "" || loadedProto.Upvalues[0].Name != "" {
		// stripped binary chunk 读回后只保留官方 source 占位，不应保留行号和调试名称。
		t.Fatalf("stripped dumped proto still has debug info: %#v", loadedProto)
	}
	if proto.Source == "" || len(proto.LineInfo) == 0 || len(proto.LocalVars) == 0 || proto.Upvalues[0].Name == "" {
		// string.dump(fn, true) 不能污染正在使用的原始 closure Proto。
		t.Fatalf("source proto mutated by dump strip: %#v", proto)
	}
}

// TestFindReturnsLiteralAndPatternRange 验证 string.find 的 literal 与 pattern 查找范围。
//
// plain=true 时按 literal 文本查找；plain 省略时按 Lua pattern 查找并返回 1-based 闭区间。
func TestFindReturnsLiteralAndPatternRange(t *testing.T) {
	// 从第 3 个字节开始查找，应该命中第二个 "ab"。
	results, err := Find(runtime.StringValue("ab-ab"), runtime.StringValue("ab"), runtime.IntegerValue(3), runtime.BooleanValue(true))
	if err != nil {
		// 合法 literal 查找不应失败。
		t.Fatalf("Find failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 4 || results[1].Integer != 5 {
		// 返回位置必须是 Lua 1-based 闭区间。
		t.Fatalf("Find results mismatch: %#v", results)
	}

	results, err = Find(runtime.StringValue("abc"), runtime.StringValue("z"))
	if err != nil {
		// 未命中不应返回错误。
		t.Fatalf("Find miss failed: %v", err)
	}
	if len(results) != 1 || !results[0].IsNil() {
		// 未命中必须返回单个 nil。
		t.Fatalf("Find miss result mismatch: %#v", results)
	}

	results, err = Find(runtime.StringValue("aloaloalo"), runtime.StringValue("alo.*alo.*alo"))
	if err != nil {
		// 合法 pattern 查找不应失败。
		t.Fatalf("Find pattern failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 1 || results[1].Integer != 9 {
		// pattern 模式应匹配整个字符串。
		t.Fatalf("Find pattern result mismatch: %#v", results)
	}

	bigSource := runtime.StringValue(strings.Repeat("a", 300000))
	results, err = Find(bigSource, runtime.StringValue("^a*.?$"))
	if err != nil {
		// 官方 big strings 锚定 pattern 不应失败。
		t.Fatalf("Find big anchored greedy failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 1 || results[1].Integer != 300000 {
		// `^a*.?$` 应覆盖完整大字符串。
		t.Fatalf("Find big anchored greedy mismatch: %#v", results)
	}
	results, err = Find(bigSource, runtime.StringValue("^a*.?b$"))
	if err != nil {
		// 未命中的大字符串 pattern 不应进入长时间回溯或报错。
		t.Fatalf("Find big anchored miss failed: %v", err)
	}
	if len(results) != 1 || !results[0].IsNil() {
		// 全部为 a 的大字符串不应匹配需要尾部 b 的 pattern。
		t.Fatalf("Find big anchored miss mismatch: %#v", results)
	}
	results, err = Find(bigSource, runtime.StringValue("^a-.?$"))
	if err != nil {
		// 非贪婪版本也应线性完成。
		t.Fatalf("Find big anchored nongreedy failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 1 || results[1].Integer != 300000 {
		// `^a-.?$` 也应覆盖完整大字符串。
		t.Fatalf("Find big anchored nongreedy mismatch: %#v", results)
	}
}

// TestFindFixed4CoversLiteralFastPath 验证 string.find 四实参固定返回快路径。
//
// plain=true 与普通 literal pattern 都可直接写入结果槽；复杂 Lua pattern 必须回退完整 Find。
func TestFindFixed4CoversLiteralFastPath(t *testing.T) {
	// 快路径使用栈上结果槽模拟 VM 固定返回调用。
	var slots [2]runtime.Value
	resultCount, handled, err := FindFixed4(slots[:], runtime.StringValue("ab-ab"), runtime.StringValue("ab"), runtime.IntegerValue(3), runtime.BooleanValue(true), 4)
	if err != nil {
		// 合法 literal 查找不应失败。
		t.Fatalf("FindFixed4 literal failed: %v", err)
	}
	if !handled || resultCount != 2 || slots[0].Integer != 4 || slots[1].Integer != 5 {
		// plain=true 必须直接返回第二次命中的 1-based 闭区间。
		t.Fatalf("FindFixed4 literal mismatch: handled=%v count=%d slots=%#v", handled, resultCount, slots)
	}

	resultCount, handled, err = FindFixed4(slots[:], runtime.StringValue("abc"), runtime.StringValue("z"), runtime.NilValue(), runtime.NilValue(), 2)
	if err != nil {
		// 未命中 literal 查找不应失败。
		t.Fatalf("FindFixed4 miss failed: %v", err)
	}
	if !handled || resultCount != 1 || !slots[0].IsNil() {
		// 未命中应返回单个 nil。
		t.Fatalf("FindFixed4 miss mismatch: handled=%v count=%d slots=%#v", handled, resultCount, slots)
	}

	resultCount, handled, err = FindFixed4(slots[:], runtime.StringValue("aloaloalo"), runtime.StringValue("alo.*alo.*alo"), runtime.NilValue(), runtime.NilValue(), 2)
	if err != nil {
		// 复杂 pattern 不应在快路径报错。
		t.Fatalf("FindFixed4 pattern failed before fallback: %v", err)
	}
	if handled || resultCount != 0 {
		// 含 magic 的 pattern 必须回退完整引擎，避免截断 capture 等变长语义。
		t.Fatalf("FindFixed4 pattern should fallback: handled=%v count=%d", handled, resultCount)
	}
}

// TestFormatSupportsBasicVerbs 验证 string.format 当前支持的基础格式项。
//
// 用例覆盖字符串、整数、浮点、quote、字面百分号，以及官方测试入口依赖的浮点精度格式。
func TestFormatSupportsBasicVerbs(t *testing.T) {
	// 构造覆盖本阶段支持格式符的格式串。
	results, err := Format(
		runtime.StringValue("%s:%d:%f:%g:%.0f:%q:%%"),
		runtime.StringValue("lua"),
		runtime.IntegerValue(53),
		runtime.NumberValue(1.5),
		runtime.NumberValue(2.25),
		runtime.NumberValue(12.4),
		runtime.StringValue("a\nb"),
	)
	if err != nil {
		// 合法格式化不应失败。
		t.Fatalf("Format failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "lua:53:1.500000:2.25:12:\"a\\\nb\":%" {
		// 输出必须稳定反映当前支持的格式语义。
		t.Fatalf("Format result mismatch: %#v", results)
	}
	stringIntegerResults, err := Format(runtime.StringValue("%d"), runtime.StringValue("123"))
	if err != nil {
		// Lua 5.3 string.format 的整数格式应接受可转换为整数的字符串。
		t.Fatalf("Format string integer failed: %v", err)
	}
	if len(stringIntegerResults) != 1 || stringIntegerResults[0].String != "123" {
		// 数字字符串必须按整数格式输出。
		t.Fatalf("Format string integer result mismatch: %#v", stringIntegerResults)
	}
}

// TestLenLowerRepReverseAndSub 验证本轮新增的基础 string 函数组合行为。
//
// 用例覆盖字节长度、ASCII 小写、带分隔符重复、字节反转和负索引截取。
func TestLenLowerRepReverseAndSub(t *testing.T) {
	// len 按字节计数，中文字符会占多个字节。
	lenResults, err := Len(runtime.StringValue("a好"))
	if err != nil {
		// 合法 len 调用不应失败。
		t.Fatalf("Len failed: %v", err)
	}
	if len(lenResults) != 1 || lenResults[0].Kind != runtime.KindInteger || lenResults[0].Integer != 4 {
		// "a好" 的 UTF-8 字节长度为 4。
		t.Fatalf("Len result mismatch: %#v", lenResults)
	}

	// lower 只转换 ASCII 字节，非 ASCII 字节保持原样。
	lowerResults, err := Lower(runtime.StringValue("Lua-好"))
	if err != nil {
		// 合法 lower 调用不应失败。
		t.Fatalf("Lower failed: %v", err)
	}
	if len(lowerResults) != 1 || lowerResults[0].String != "lua-好" {
		// ASCII 大写应变小写，中文字节序列不应变化。
		t.Fatalf("Lower result mismatch: %#v", lowerResults)
	}

	// rep 支持可选分隔符。
	repResults, err := Rep(runtime.StringValue("go"), runtime.IntegerValue(3), runtime.StringValue(":"))
	if err != nil {
		// 合法 rep 调用不应失败。
		t.Fatalf("Rep failed: %v", err)
	}
	if len(repResults) != 1 || repResults[0].String != "go:go:go" {
		// 重复片段之间必须插入 separator。
		t.Fatalf("Rep result mismatch: %#v", repResults)
	}

	// reverse 按字节反转，不按 rune 重排。
	reverseResults, err := Reverse(runtime.StringValue("abc"))
	if err != nil {
		// 合法 reverse 调用不应失败。
		t.Fatalf("Reverse failed: %v", err)
	}
	if len(reverseResults) != 1 || reverseResults[0].String != "cba" {
		// ASCII 字节反转结果应为 cba。
		t.Fatalf("Reverse result mismatch: %#v", reverseResults)
	}

	// sub 支持负索引和默认尾部。
	subResults, err := Sub(runtime.StringValue("abcdef"), runtime.IntegerValue(-3))
	if err != nil {
		// 合法 sub 调用不应失败。
		t.Fatalf("Sub failed: %v", err)
	}
	if len(subResults) != 1 || subResults[0].String != "def" {
		// -3 到默认 -1 应截取最后三个字节。
		t.Fatalf("Sub result mismatch: %#v", subResults)
	}
}

// TestSubFixedFastPath 验证 string.sub 固定单返回快路径。
//
// 快路径必须保留负索引、显式终点和空区间返回空字符串的 Lua 5.3 语义。
func TestSubFixedFastPath(t *testing.T) {
	// 负索引范围应与 Sub 完整路径返回一致。
	var dst [1]runtime.Value
	resultCount, handled, err := SubFixed4(dst[:], runtime.StringValue("abcdef"), runtime.IntegerValue(-3), runtime.IntegerValue(-1), runtime.NilValue(), 3)
	if err != nil {
		// 合法 sub 快路径不应失败。
		t.Fatalf("SubFixed4 failed: %v", err)
	}
	if !handled || resultCount != 1 || dst[0].Kind != runtime.KindString || dst[0].String != "def" {
		// 负索引应截取后三个字节。
		t.Fatalf("SubFixed4 result mismatch: handled=%v count=%d dst=%#v", handled, resultCount, dst[0])
	}

	resultCount, handled, err = SubFixed4(dst[:], runtime.StringValue("abc"), runtime.IntegerValue(3), runtime.IntegerValue(1), runtime.NilValue(), 3)
	if err != nil {
		// 空区间不应失败。
		t.Fatalf("SubFixed4 empty failed: %v", err)
	}
	if !handled || resultCount != 1 || dst[0].Kind != runtime.KindString || dst[0].String != "" {
		// Lua string.sub 空区间返回空字符串。
		t.Fatalf("SubFixed4 empty mismatch: handled=%v count=%d dst=%#v", handled, resultCount, dst[0])
	}
}

// TestSubFixedSingleFastPath 验证 string.sub 固定单返回无槽位快路径。
//
// 单返回入口必须保留负索引、显式终点和空区间返回空字符串的 Lua 5.3 语义。
func TestSubFixedSingleFastPath(t *testing.T) {
	// 负索引范围应与 SubFixed4 完整固定入口返回一致。
	result, resultCount, handled, err := SubFixed4Single(runtime.StringValue("abcdef"), runtime.IntegerValue(-3), runtime.IntegerValue(-1), runtime.NilValue(), 3)
	if err != nil {
		// 合法 sub 快路径不应失败。
		t.Fatalf("SubFixed4Single failed: %v", err)
	}
	if !handled || resultCount != 1 || result.Kind != runtime.KindString || result.String != "def" {
		// 负索引应截取后三个字节。
		t.Fatalf("SubFixed4Single result mismatch: handled=%v count=%d result=%#v", handled, resultCount, result)
	}

	result, resultCount, handled, err = SubFixed4Single(runtime.StringValue("abc"), runtime.IntegerValue(3), runtime.IntegerValue(1), runtime.NilValue(), 3)
	if err != nil {
		// 空区间不应失败。
		t.Fatalf("SubFixed4Single empty failed: %v", err)
	}
	if !handled || resultCount != 1 || result.Kind != runtime.KindString || result.String != "" {
		// Lua string.sub 空区间返回空字符串。
		t.Fatalf("SubFixed4Single empty mismatch: handled=%v count=%d result=%#v", handled, resultCount, result)
	}
}

// TestIntegerArgumentReportsRepresentationFailure 验证 string 库整数参数的 number 转换错误文本。
//
// Lua 5.3 官方 errors.lua 会匹配 `has no integer representation`，不能只返回
// `integer expected`，否则无法区分类型错误和 number 无法整数化。
func TestIntegerArgumentReportsRepresentationFailure(t *testing.T) {
	_, err := Sub(runtime.StringValue("a"), runtime.NumberValue(math.Pow(2, 100)))
	if err == nil {
		// 超出 integer 表示范围的起始位置必须报错。
		t.Fatalf("Sub unexpectedly accepted huge number")
	}
	if message := runtime.ErrorObject(err).String; !strings.Contains(message, "has no integer representation") {
		// 错误文本必须包含官方 errors.lua 匹配片段。
		t.Fatalf("Sub huge number error = %q", message)
	}

	_, err = Rep(runtime.StringValue("a"), runtime.NumberValue(3.3))
	if err == nil {
		// 非整数 float 重复次数必须报错。
		t.Fatalf("Rep unexpectedly accepted fractional number")
	}
	if message := runtime.ErrorObject(err).String; !strings.Contains(message, "has no integer representation") {
		// 错误文本必须包含官方 errors.lua 匹配片段。
		t.Fatalf("Rep fractional number error = %q", message)
	}
}

// TestFormatQuoteUsesLuaEscapes 验证 string.format("%q") 使用 Lua 转义规则。
//
// 官方 strings.lua 要求换行输出为反斜杠加真实换行，NUL 输出为 `\0`，且 quote 结果可被
// load 回读。
func TestFormatQuoteUsesLuaEscapes(t *testing.T) {
	// 覆盖官方 strings.lua 第 153 行的组合格式。
	source := "\"\xe1lo\"\n\\"
	results, err := Format(runtime.StringValue("%q%s"), runtime.StringValue(source), runtime.StringValue(source))
	if err != nil {
		// 合法 quote 格式化不应失败。
		t.Fatalf("Format quote failed: %v", err)
	}
	want := "\"\\\"\xe1lo\\\"\\\n\\\\\"\"\xe1lo\"\n\\"
	if len(results) != 1 || results[0].String != want {
		// quote 输出必须精确匹配 Lua 5.3 的转义文本。
		t.Fatalf("Format quote result = %q want %q", results[0].String, want)
	}

	// 单独 NUL 必须使用短 \0 转义。
	nulResults, err := Format(runtime.StringValue("%q"), runtime.StringValue("\x00"))
	if err != nil {
		// 合法 NUL quote 不应失败。
		t.Fatalf("Format nul quote failed: %v", err)
	}
	if len(nulResults) != 1 || nulResults[0].String != "\"\\0\"" {
		// 官方 strings.lua 断言 string.format("%q", "\0") == [["\0"]]。
		t.Fatalf("Format nul quote result = %q", nulResults[0].String)
	}

	// number 必须输出可回读的数字字面量，而不是带引号的字符串。
	numberResults, err := Format(runtime.StringValue("%q"), runtime.IntegerValue(123))
	if err != nil {
		// 合法 number quote 不应失败。
		t.Fatalf("Format number quote failed: %v", err)
	}
	if len(numberResults) != 1 || numberResults[0].String != "123" {
		// 数字字面量必须保留为裸数字。
		t.Fatalf("Format number quote result = %q", numberResults[0].String)
	}

	// nil/boolean 也必须输出可回读的基础字面量。
	for _, testCase := range []struct {
		value runtime.Value
		want  string
	}{
		{value: runtime.NilValue(), want: "nil"},
		{value: runtime.BooleanValue(true), want: "true"},
		{value: runtime.BooleanValue(false), want: "false"},
	} {
		literalResults, literalErr := Format(runtime.StringValue("%q"), testCase.value)
		if literalErr != nil {
			// 合法基础字面量 quote 不应失败。
			t.Fatalf("Format basic literal quote failed: %v", literalErr)
		}
		if len(literalResults) != 1 || literalResults[0].String != testCase.want {
			// 基础字面量必须输出裸文本。
			t.Fatalf("Format basic literal quote result = %q want %q", literalResults[0].String, testCase.want)
		}
	}

	// math.mininteger 使用十六进制字面量，避免十进制正操作数超过 int64 后无法回读。
	minIntegerResults, err := Format(runtime.StringValue("%q"), runtime.IntegerValue(-9223372036854775808))
	if err != nil {
		// 合法 mininteger quote 不应失败。
		t.Fatalf("Format mininteger quote failed: %v", err)
	}
	if len(minIntegerResults) != 1 || minIntegerResults[0].String != "-0x8000000000000000" {
		// mininteger 必须输出当前 parser 可回读的字面量。
		t.Fatalf("Format mininteger quote result = %q", minIntegerResults[0].String)
	}

	// 0xff 字节需要十进制转义，保证 load 回读不受源码文本解码影响。
	ffResults, err := Format(runtime.StringValue("%q"), runtime.StringValue("\xff"))
	if err != nil {
		// 合法 0xff quote 不应失败。
		t.Fatalf("Format 0xff quote failed: %v", err)
	}
	if len(ffResults) != 1 || ffResults[0].String != "\"\\255\"" {
		// 0xff 必须输出 \255 转义。
		t.Fatalf("Format 0xff quote result = %q", ffResults[0].String)
	}

	// table 没有 Lua 字面量表示，必须返回 no literal 错误。
	_, err = Format(runtime.StringValue("%q"), runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()))
	if !errors.Is(err, runtime.ErrLuaError) || !strings.Contains(runtime.ErrorObject(err).String, "no literal") {
		// 官方 strings.lua 通过 checkerror("no literal", string.format, "%q", {}) 验证该错误。
		t.Fatalf("Format table quote error = %v", err)
	}
}

// TestFormatSupportsIntegerAndCharVerbs 验证 string.format 的整数与字符格式族。
//
// 官方 strings.lua 依赖 `%c/%x/%X/%o/%u`，其中 `%c` 必须输出单字节字符串，十六进制和
// 无符号格式必须按 64 位补码视图处理。
func TestFormatSupportsIntegerAndCharVerbs(t *testing.T) {
	// %c 输出原始字节，不能按 UTF-8 rune 扩展。
	charResults, err := Format(runtime.StringValue("\x00%c\x00%c%x\x00"), runtime.IntegerValue(0xe4), runtime.IntegerValue('b'), runtime.IntegerValue(140))
	if err != nil {
		// 合法字符和十六进制格式不应失败。
		t.Fatalf("Format char/integer failed: %v", err)
	}
	if len(charResults) != 1 || charResults[0].String != "\x00\xe4\x00b8c\x00" {
		// 输出必须保留单字节 0xe4 和小写十六进制 8c。
		t.Fatalf("Format char/integer result = %#v", charResults)
	}

	// 无符号格式按 uint64 视图输出。
	unsignedResults, err := Format(runtime.StringValue("%x %X %o %u"), runtime.IntegerValue(-1), runtime.IntegerValue(0x8f), runtime.IntegerValue(0xABCD), runtime.IntegerValue(-1))
	if err != nil {
		// 合法无符号格式不应失败。
		t.Fatalf("Format unsigned failed: %v", err)
	}
	if len(unsignedResults) != 1 || unsignedResults[0].String != "ffffffffffffffff 8F 125715 18446744073709551615" {
		// 十六进制、八进制和无符号十进制必须匹配补码视图。
		t.Fatalf("Format unsigned result = %#v", unsignedResults)
	}
}

// TestFormatSupportsHexFloatVerbs 验证 string.format 的十六进制浮点格式。
//
// Lua 5.3 官方 strings.lua 使用 `%a/%A` 验证浮点数可按 C99 十六进制指数形式往返。
func TestFormatSupportsHexFloatVerbs(t *testing.T) {
	// 小写 %a 需要输出 0x 前缀和 p 指数。
	lowerResults, err := Format(runtime.StringValue("%a"), runtime.NumberValue(1.5))
	if err != nil {
		// 合法十六进制浮点格式不应失败。
		t.Fatalf("Format hex float failed: %v", err)
	}
	if len(lowerResults) != 1 || lowerResults[0].String != "0x1.8p+0" {
		// Go fmt 的 %x 输出正好匹配 Lua/C99 十六进制浮点形式。
		t.Fatalf("Format %%a result = %#v", lowerResults)
	}

	// 大写 %A 需要输出 0X 前缀和 P 指数，并支持修饰符。
	upperResults, err := Format(runtime.StringValue("%+.2A"), runtime.IntegerValue(12))
	if err != nil {
		// 整数可转换为 number 后按十六进制浮点格式化。
		t.Fatalf("Format upper hex float failed: %v", err)
	}
	if len(upperResults) != 1 || upperResults[0].String != "+0X1.80P+3" {
		// 符号、精度和大写指数必须保持稳定。
		t.Fatalf("Format %%A result = %#v", upperResults)
	}

	// inf、-inf 和 nan 按 Lua 官方测试期望的大小写文本输出。
	specialResults, err := Format(runtime.StringValue("%a %A %a"), runtime.NumberValue(math.Inf(1)), runtime.NumberValue(math.Inf(-1)), runtime.NumberValue(math.NaN()))
	if err != nil {
		// 特殊浮点值格式化不应失败。
		t.Fatalf("Format special hex float failed: %v", err)
	}
	if len(specialResults) != 1 || specialResults[0].String != "inf -INF nan" {
		// 特殊值文本必须匹配 Lua 5.3 strings.lua 的大小写和正号规则。
		t.Fatalf("Format special hex float result = %#v", specialResults)
	}
}

// TestFormatRejectsModifiedStringWithZeros 验证带修饰的 %s 拒绝 NUL 字符串。
//
// Lua 5.3 官方 strings.lua 要求 `string.format("%10s", "\0")` 报 contains zeros，避免
// C printf 对 NUL 截断产生不可移植结果。
func TestFormatRejectsModifiedStringWithZeros(t *testing.T) {
	// 不带修饰的 %s 允许原样输出 NUL 字节。
	plainResults, err := Format(runtime.StringValue("%s"), runtime.StringValue("\x00"))
	if err != nil {
		// plain %s 不应失败。
		t.Fatalf("Format plain zero string failed: %v", err)
	}
	if len(plainResults) != 1 || plainResults[0].String != "\x00" {
		// plain %s 必须保留 NUL。
		t.Fatalf("Format plain zero string result = %#v", plainResults)
	}

	// 带宽度的 %s 必须拒绝 NUL 字符串。
	_, err = Format(runtime.StringValue("%10s"), runtime.StringValue("\x00"))
	if !errors.Is(err, runtime.ErrLuaError) || !strings.Contains(runtime.ErrorObject(err).String, "contains zeros") {
		// 错误对象必须包含官方测试检查的文本。
		t.Fatalf("Format modified zero string error = %v", err)
	}
}

// TestFormatRejectsOfficialErrorCases 验证 Lua 5.3 strings.lua 覆盖的 format 错误文本。
//
// 官方用例依赖错误对象包含 too long、repeated flags 和 no value，不能只返回通用参数错误。
func TestFormatRejectsOfficialErrorCases(t *testing.T) {
	tests := []struct {
		// name 描述当前错误场景。
		name string
		// format 是传入 string.format 的格式串。
		format string
		// wantSubstring 是错误对象中必须包含的 Lua 兼容文本。
		wantSubstring string
		// args 是格式串后续实参。
		args []runtime.Value
	}{
		{name: "large width", format: "%100.3d", wantSubstring: "too long", args: []runtime.Value{runtime.IntegerValue(10)}},
		{name: "large precision", format: "%1.100d", wantSubstring: "too long", args: []runtime.Value{runtime.IntegerValue(10)}},
		{name: "repeated zero flag", format: "%000d", wantSubstring: "repeated flags", args: []runtime.Value{runtime.IntegerValue(10)}},
		{name: "missing value", format: "%d %d", wantSubstring: "no value", args: []runtime.Value{runtime.IntegerValue(10)}},
	}
	for _, testCase := range tests {
		// 每个场景独立调用 Format，避免错误对象互相污染。
		_, err := Format(append([]runtime.Value{runtime.StringValue(testCase.format)}, testCase.args...)...)
		if err == nil {
			// 这些官方错误场景都必须失败。
			t.Fatalf("%s: expected error", testCase.name)
		}
		errorText := runtime.ErrorObject(err).String
		if !strings.Contains(errorText, testCase.wantSubstring) {
			// 错误文本必须包含官方 checkerror 匹配的关键片段。
			t.Fatalf("%s: error %q missing %q", testCase.name, errorText, testCase.wantSubstring)
		}
	}
}

// TestRepAndSubEdgeCases 验证 string.rep 与 string.sub 的边界语义。
//
// 用例覆盖非正重复次数和裁剪后为空区间，确保返回空字符串而不是 nil。
func TestRepAndSubEdgeCases(t *testing.T) {
	// 非正重复次数返回空字符串。
	repResults, err := Rep(runtime.StringValue("x"), runtime.IntegerValue(0))
	if err != nil {
		// repeat count 为 0 不应报错。
		t.Fatalf("Rep zero failed: %v", err)
	}
	if len(repResults) != 1 || repResults[0].String != "" {
		// rep 0 次必须返回空字符串。
		t.Fatalf("Rep zero result mismatch: %#v", repResults)
	}

	// 起点大于终点时返回空字符串。
	subResults, err := Sub(runtime.StringValue("abc"), runtime.IntegerValue(3), runtime.IntegerValue(1))
	if err != nil {
		// 空区间 sub 不应报错。
		t.Fatalf("Sub empty failed: %v", err)
	}
	if len(subResults) != 1 || subResults[0].String != "" {
		// 空区间必须返回空字符串。
		t.Fatalf("Sub empty result mismatch: %#v", subResults)
	}
}

// TestRepRejectsTooLargeResult 验证 string.rep 拒绝超大结果。
//
// Lua 5.3 官方 strings.lua 会用超过 2GB 的重复结果检查错误文本；实现必须在分配前返回
// 包含 too large 的 Lua error，避免宿主进程触发大内存分配。
func TestRepRejectsTooLargeResult(t *testing.T) {
	// 普通源字符串重复过多时必须返回 Lua error。
	_, err := Rep(runtime.StringValue("aa"), runtime.IntegerValue(1<<30))
	if !errors.Is(err, runtime.ErrLuaError) || !strings.Contains(runtime.ErrorObject(err).String, "too large") {
		// 错误链和文本都要满足官方 checkerror 的 string.find 检查。
		t.Fatalf("Rep large result error = %v", err)
	}

	// 即使源字符串为空，分隔符也会贡献结果长度，仍需要提前拒绝。
	_, err = Rep(runtime.StringValue("a"), runtime.IntegerValue(1<<30), runtime.StringValue(","))
	if !errors.Is(err, runtime.ErrLuaError) || !strings.Contains(runtime.ErrorObject(err).String, "too large") {
		// 分隔符导致的超大结果同样必须报 too large。
		t.Fatalf("Rep large separator result error = %v", err)
	}
}

// TestUpperConvertsASCIIBytes 验证 string.upper 的 ASCII 字节转换语义。
//
// 非 ASCII 字节必须原样保留，避免 Unicode 大小写映射改变 Lua 字节字符串。
func TestUpperConvertsASCIIBytes(t *testing.T) {
	// 构造包含 ASCII 小写和非 ASCII 字符的字符串。
	results, err := Upper(runtime.StringValue("lua-好"))
	if err != nil {
		// 合法 upper 调用不应失败。
		t.Fatalf("Upper failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "LUA-好" {
		// ASCII 字母应变大写，中文字节序列不应变化。
		t.Fatalf("Upper result mismatch: %#v", results)
	}
}

// TestPackPackSizeAndUnpackRoundTrip 验证 string.pack/unpack 的基础格式往返。
//
// 用例覆盖端序标记、整数、浮点、固定字符串、零结尾字符串、长度前缀字符串和 padding。
func TestPackPackSizeAndUnpackRoundTrip(t *testing.T) {
	// 固定宽度格式的 packsize 应只统计确定长度字段。
	sizeResults, err := PackSize(runtime.StringValue("<i2I4fdc3x"))
	if err != nil {
		// 固定宽度格式不应返回 packsize 错误。
		t.Fatalf("PackSize failed: %v", err)
	}
	if len(sizeResults) != 1 || sizeResults[0].Kind != runtime.KindInteger || sizeResults[0].Integer != 22 {
		// i2 + I4 + f + d + c3 + x = 22。
		t.Fatalf("PackSize result mismatch: %#v", sizeResults)
	}

	packResults, err := Pack(
		runtime.StringValue("<i2I4fdc3xz s1"),
		runtime.IntegerValue(-2),
		runtime.IntegerValue(258),
		runtime.NumberValue(1.5),
		runtime.NumberValue(2.25),
		runtime.StringValue("go"),
		runtime.StringValue("end"),
		runtime.StringValue("lua"),
	)
	if err != nil {
		// 合法格式与参数不应打包失败。
		t.Fatalf("Pack failed: %v", err)
	}
	if len(packResults) != 1 || packResults[0].Kind != runtime.KindString {
		// pack 必须返回二进制字符串。
		t.Fatalf("Pack result mismatch: %#v", packResults)
	}

	unpackResults, err := Unpack(runtime.StringValue("<i2I4fdc3xz s1"), packResults[0])
	if err != nil {
		// pack 产物应能被同一格式 unpack。
		t.Fatalf("Unpack failed: %v", err)
	}
	if len(unpackResults) != 8 {
		// 7 个值加下一位置，共 8 个返回值。
		t.Fatalf("Unpack result length mismatch: %d", len(unpackResults))
	}
	if unpackResults[0].Integer != -2 || unpackResults[1].Integer != 258 {
		// 整数往返必须保持值。
		t.Fatalf("Unpack integer mismatch: %#v", unpackResults[:2])
	}
	if unpackResults[2].Kind != runtime.KindNumber || unpackResults[2].Number != 1.5 {
		// float32 往返到 Lua number 后应保持测试值。
		t.Fatalf("Unpack float mismatch: %v", unpackResults[2].DebugString())
	}
	if unpackResults[3].Kind != runtime.KindNumber || unpackResults[3].Number != 2.25 {
		// float64 往返到 Lua number 后应保持测试值。
		t.Fatalf("Unpack double mismatch: %v", unpackResults[3].DebugString())
	}
	if unpackResults[4].String != "go\x00" || unpackResults[5].String != "end" || unpackResults[6].String != "lua" {
		// 固定字符串、零结尾字符串和长度前缀字符串都必须按格式恢复。
		t.Fatalf("Unpack string mismatch: %#v", unpackResults[4:7])
	}
	if unpackResults[7].Kind != runtime.KindInteger || unpackResults[7].Integer != int64(len(packResults[0].String)+1) {
		// 最后返回值必须是下一次读取的 Lua 1-based 位置。
		t.Fatalf("Unpack next position mismatch: %v", unpackResults[7].DebugString())
	}
}

// TestPackNumberOptionRoundTrip 验证 string.pack 的 n 格式支持 Lua number。
//
// 官方 calls.lua 构造 binary chunk header 时会调用 string.packsize("n") 和 string.pack("n", ...)，
// n 在当前纯 Go 实现中按 8 字节 float64 编码。
func TestPackNumberOptionRoundTrip(t *testing.T) {
	sizeResults, err := PackSize(runtime.StringValue("n"))
	if err != nil {
		// n 是固定宽度 Lua number，packsize 不应失败。
		t.Fatalf("PackSize n failed: %v", err)
	}
	if len(sizeResults) != 1 || sizeResults[0].Kind != runtime.KindInteger || sizeResults[0].Integer != 8 {
		// 当前 Lua number 使用 float64，因此 n 大小为 8。
		t.Fatalf("PackSize n mismatch: %#v", sizeResults)
	}

	packResults, err := Pack(runtime.StringValue("<n"), runtime.NumberValue(3.5))
	if err != nil {
		// n 应接受 number 参数。
		t.Fatalf("Pack n failed: %v", err)
	}
	unpackResults, err := Unpack(runtime.StringValue("<n"), packResults[0])
	if err != nil {
		// pack 的 n 产物应可用同格式解包。
		t.Fatalf("Unpack n failed: %v", err)
	}
	if len(unpackResults) != 2 || unpackResults[0].Kind != runtime.KindNumber || unpackResults[0].Number != 3.5 {
		// 解包结果第一项是 number，第二项是下一位置。
		t.Fatalf("Unpack n mismatch: %#v", unpackResults)
	}
}

// TestPackSizeRejectsVariableLength 验证 string.packsize 拒绝可变长度格式。
//
// Lua 5.3 对 z 和 sN 这类依赖实际字符串内容的格式无法静态计算大小。
func TestPackSizeRejectsVariableLength(t *testing.T) {
	// z 是零结尾字符串，长度依赖实参内容。
	_, err := PackSize(runtime.StringValue("z"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 可变长度格式必须以 Lua error 形式传播。
		t.Fatalf("PackSize variable error mismatch: %v", err)
	}
}

// TestMatchPatternClassesAndQuantifiers 验证 string.match 的字符类和量词。
//
// 用例覆盖 `%a`、`%d`、`+`、`-` 和 `?` 的第一阶段 pattern 引擎行为。
func TestMatchPatternClassesAndQuantifiers(t *testing.T) {
	// 捕获单词和数字，验证字符类与贪婪量词。
	results, err := Match(runtime.StringValue("item-123;"), runtime.StringValue("(%a+)%-(%d+)"))
	if err != nil {
		// 合法 pattern 不应失败。
		t.Fatalf("Match failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "item" || results[1].String != "123" {
		// capture 应按左括号顺序返回。
		t.Fatalf("Match capture result mismatch: %#v", results)
	}

	// 非贪婪量词应选择满足后续 pattern 的最短匹配。
	results, err = Match(runtime.StringValue("a123b456b"), runtime.StringValue("a(.-)b"))
	if err != nil {
		// 合法非贪婪 pattern 不应失败。
		t.Fatalf("Match non-greedy failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "123" {
		// `.-` 应在第一个 b 处停止。
		t.Fatalf("Match non-greedy result mismatch: %#v", results)
	}

	// 字符集开头的 ] 是集合成员，不能被误判为闭合符。
	results, err = Match(runtime.StringValue("]]]\xe1b"), runtime.StringValue("[^]]"))
	if err != nil {
		// 合法取反字符集不应失败。
		t.Fatalf("Match set with leading bracket failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "\xe1" {
		// `[^]]` 应跳过连续右括号并命中第一个非右括号字节。
		t.Fatalf("Match set with leading bracket mismatch: %#v", results)
	}

	// 字符集内非字符类转义应按字面字符处理。
	for _, literal := range []string{"-", "[", "]", "^", "a", "b"} {
		results, err = Match(runtime.StringValue(literal), runtime.StringValue("[%^%[%-a%]%-b]"))
		if err != nil {
			// 合法复杂字符集不应失败。
			t.Fatalf("Match escaped set literal %q failed: %v", literal, err)
		}
		if len(results) != 1 || results[0].String != literal {
			// 每个官方期望字符都必须能被该集合匹配。
			t.Fatalf("Match escaped set literal %q mismatch: %#v", literal, results)
		}
	}

	// Back reference 应匹配前面捕获到的文本，空 capture 应返回 1-based 位置。
	results, err = Match(runtime.StringValue("alo alx 123 b\x00o b\x00o"), runtime.StringValue("()(..*) %2()"))
	if err != nil {
		// 合法 back reference pattern 不应失败。
		t.Fatalf("Match back reference failed: %v", err)
	}
	if len(results) != 3 || !results[0].RawEqual(runtime.IntegerValue(13)) || results[1].String != "b\x00o" || !results[2].RawEqual(runtime.IntegerValue(20)) {
		// 位置捕获与 back reference 文本 capture 必须按 Lua 5.3 顺序返回。
		t.Fatalf("Match back reference results mismatch: %#v", results)
	}

	// 嵌套 capture 的返回顺序必须按左括号出现顺序，而不是闭合顺序。
	results, err = Match(runtime.StringValue("\xe1lo alo"), runtime.StringValue("^(((.).).* (%w*))$"))
	if err != nil {
		// 合法嵌套 capture pattern 不应失败。
		t.Fatalf("Match nested captures failed: %v", err)
	}
	if len(results) != 4 || results[0].String != "\xe1lo alo" || results[1].String != "\xe1l" || results[2].String != "\xe1" || results[3].String != "alo" {
		// 返回顺序应为外层到内层，再到后续 capture。
		t.Fatalf("Match nested captures results mismatch: %#v", results)
	}
}

// TestMatchBalancedAndFrontier 验证 `%bxy` balanced 和 `%f[]` frontier pattern。
//
// 用例覆盖括号平衡匹配，以及从非单词到单词的零宽边界。
func TestMatchBalancedAndFrontier(t *testing.T) {
	// balanced pattern 应包含嵌套括号。
	results, err := Match(runtime.StringValue("x(a(b)c)y"), runtime.StringValue("%b()"))
	if err != nil {
		// 合法 balanced pattern 不应失败。
		t.Fatalf("Match balanced failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "(a(b)c)" {
		// 完整匹配应覆盖最外层平衡括号。
		t.Fatalf("Match balanced result mismatch: %#v", results)
	}
	results, err = Match(runtime.StringValue("alo 'oi' alo"), runtime.StringValue("%b''"))
	if err != nil {
		// 同字符分隔符的 balanced pattern 不应失败。
		t.Fatalf("Match same-delimiter balanced failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "'oi'" {
		// `%b''` 应从第一个单引号匹配到下一个单引号。
		t.Fatalf("Match same-delimiter balanced result mismatch: %#v", results)
	}

	// frontier pattern 不消耗字符，但要求当前位置进入指定集合。
	results, err = Match(runtime.StringValue("..lua"), runtime.StringValue("%f[%a](%a+)"))
	if err != nil {
		// 合法 frontier pattern 不应失败。
		t.Fatalf("Match frontier failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "lua" {
		// frontier 后的 capture 应返回单词。
		t.Fatalf("Match frontier result mismatch: %#v", results)
	}

	results, err = Find(runtime.StringValue("function"), runtime.StringValue("%f[^\x01-\xff]"))
	if err != nil {
		// 取反全集的 frontier 不应失败。
		t.Fatalf("Find frontier negated byte range failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 9 || results[1].Integer != 8 {
		// 字符串末尾外侧按 NUL 判断，位置应为尾后一位的空匹配。
		t.Fatalf("Find frontier negated byte range mismatch: %#v", results)
	}

	results, err = Find(runtime.StringValue("aba"), runtime.StringValue("%f[%z]"))
	if err != nil {
		// `%z` frontier 不应失败。
		t.Fatalf("Find frontier zero failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 4 || results[1].Integer != 3 {
		// `%f[%z]` 只能在尾后 NUL 边界命中。
		t.Fatalf("Find frontier zero mismatch: %#v", results)
	}

	results, err = Match(runtime.StringValue(strings.Repeat("a", 80)), runtime.StringValue(strings.Repeat(".?", 80)))
	if err != nil {
		// 官方小规模复杂 pattern 应正常匹配。
		t.Fatalf("Match bounded complex pattern failed: %v", err)
	}
	if len(results) != 1 || results[0].String != strings.Repeat("a", 80) {
		// `.?` 重复 80 次应覆盖完整 80 字节字符串。
		t.Fatalf("Match bounded complex pattern result mismatch: %#v", results)
	}

	_, err = Match(runtime.StringValue("a"), runtime.StringValue(strings.Repeat(".?", maxPatternBytes/2+1)))
	if complexErr := runtime.ErrorObject(err); err == nil || complexErr.Kind != runtime.KindString || !strings.Contains(complexErr.String, "too complex") {
		// 超长 pattern 必须快速失败，避免官方压力用例长期回溯。
		t.Fatalf("Match too complex error mismatch: %v", err)
	}
}

// TestMatchReturnsNilOnMiss 验证 string.match 未命中时返回单个 nil。
//
// 该语义与 string.find 未命中保持一致，便于 Lua 侧条件判断。
func TestMatchReturnsNilOnMiss(t *testing.T) {
	// pattern 无法命中源字符串。
	results, err := Match(runtime.StringValue("abc"), runtime.StringValue("%d+"))
	if err != nil {
		// 未命中不是错误。
		t.Fatalf("Match miss failed: %v", err)
	}
	if len(results) != 1 || !results[0].IsNil() {
		// 未命中必须返回单个 nil。
		t.Fatalf("Match miss result mismatch: %#v", results)
	}
}

// TestFindMalformedFrontierReportsMissing 验证 `%f` 缺少字符集时的错误文本。
//
// Lua 5.3 官方测试要求该错误包含 `missing`，用于区分普通 malformed pattern。
func TestFindMalformedFrontierReportsMissing(t *testing.T) {
	// `%f` 后没有 `[` 时必须返回 Lua pattern 错误。
	_, err := Find(runtime.StringValue("a"), runtime.StringValue("%f"))
	errorObject := runtime.ErrorObject(err)
	if err == nil || errorObject.Kind != runtime.KindString || !strings.Contains(errorObject.String, "missing") {
		// 错误文本需要保留 missing 关键字。
		t.Fatalf("Find malformed frontier error mismatch: %v", err)
	}
}

// TestGMatchIteratesCaptures 验证 string.gmatch 返回可重复调用的 iterator。
//
// 用例覆盖 capture 返回值和迭代结束时的零返回值。
func TestGMatchIteratesCaptures(t *testing.T) {
	// gmatch 返回一个 Go closure iterator。
	results, err := GMatch(runtime.StringValue("a=1 b=22"), runtime.StringValue("(%a)=(%d+)"))
	if err != nil {
		// 合法 gmatch 不应失败。
		t.Fatalf("GMatch failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindGoClosure {
		// gmatch 必须返回 iterator 函数。
		t.Fatalf("GMatch iterator mismatch: %#v", results)
	}

	iteratorClosure, ok := results[0].Ref.(*runtime.GoClosureWithUpvalues)
	if !ok || iteratorClosure == nil || iteratorClosure.Function == nil {
		// iterator 必须是当前运行期可执行且带 debug upvalue 元数据的 Go closure。
		t.Fatalf("GMatch iterator payload mismatch: %#v", results[0].Ref)
	}
	first, err := iteratorClosure.Function()
	if err != nil {
		// 第一次迭代不应失败。
		t.Fatalf("GMatch first failed: %v", err)
	}
	if len(first) != 2 || first[0].String != "a" || first[1].String != "1" {
		// 第一次应返回 a 和 1 两个 capture。
		t.Fatalf("GMatch first result mismatch: %#v", first)
	}
	second, err := iteratorClosure.Function()
	if err != nil {
		// 第二次迭代不应失败。
		t.Fatalf("GMatch second failed: %v", err)
	}
	if len(second) != 2 || second[0].String != "b" || second[1].String != "22" {
		// 第二次应返回 b 和 22 两个 capture。
		t.Fatalf("GMatch second result mismatch: %#v", second)
	}
	done, err := iteratorClosure.Function()
	if err != nil {
		// 结束迭代不应失败。
		t.Fatalf("GMatch done failed: %v", err)
	}
	if len(done) != 0 {
		// 无更多匹配时返回零个结果。
		t.Fatalf("GMatch done result mismatch: %#v", done)
	}
}

// TestGSubStringReplacement 验证 string.gsub 的字符串替换模板。
//
// 用例覆盖 `%0`、`%1` 展开和替换次数返回。
func TestGSubStringReplacement(t *testing.T) {
	// 使用 capture 重排 key/value 格式。
	results, err := GSub(runtime.StringValue("a=1 b=22"), runtime.StringValue("(%a)=(%d+)"), runtime.StringValue("%1:%2"))
	if err != nil {
		// 合法 gsub 不应失败。
		t.Fatalf("GSub failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "a:1 b:22" || results[1].Integer != 2 {
		// 替换结果和替换次数都必须返回。
		t.Fatalf("GSub result mismatch: %#v", results)
	}

	limited, err := GSub(runtime.StringValue("aa aa"), runtime.StringValue("aa"), runtime.StringValue("[%0]"), runtime.IntegerValue(1))
	if err != nil {
		// 限制次数的 gsub 不应失败。
		t.Fatalf("GSub limited failed: %v", err)
	}
	if len(limited) != 2 || limited[0].String != "[aa] aa" || limited[1].Integer != 1 {
		// n=1 时只替换第一次命中。
		t.Fatalf("GSub limited result mismatch: %#v", limited)
	}

	implicitCapture, err := GSub(runtime.StringValue("abc"), runtime.StringValue("%w"), runtime.StringValue("%1%0"))
	if err != nil {
		// 没有显式 capture 时，Lua gsub 把完整匹配视为第一个 capture。
		t.Fatalf("GSub implicit capture failed: %v", err)
	}
	if len(implicitCapture) != 2 || implicitCapture[0].String != "aabbcc" || implicitCapture[1].Integer != 3 {
		// `%1%0` 应把每个完整匹配重复两次。
		t.Fatalf("GSub implicit capture result mismatch: %#v", implicitCapture)
	}

	emptyMatch, err := GSub(runtime.StringValue("a b cd"), runtime.StringValue(" *"), runtime.StringValue("-"))
	if err != nil {
		// nullable pattern 的 gsub 不应失败。
		t.Fatalf("GSub empty match failed: %v", err)
	}
	if len(emptyMatch) != 2 || emptyMatch[0].String != "-a-b-c-d-" {
		// Lua 5.3.3 之后，非空匹配结束位置不能立即重复接受空匹配。
		t.Fatalf("GSub empty match result mismatch: %#v", emptyMatch)
	}

	balancedQuote, err := GSub(runtime.StringValue("alo 'oi' alo"), runtime.StringValue("%b''"), runtime.StringValue("\""))
	if err != nil {
		// balanced pattern 作为 gsub 查找模式不应失败。
		t.Fatalf("GSub balanced quote failed: %v", err)
	}
	if len(balancedQuote) != 2 || balancedQuote[0].String != "alo \" alo" || balancedQuote[1].Integer != 1 {
		// `%b''` 应替换整段单引号内容。
		t.Fatalf("GSub balanced quote result mismatch: %#v", balancedQuote)
	}

	_, err = GSub(runtime.StringValue("alo"), runtime.StringValue("."), runtime.StringValue("%2"))
	if captureErr := runtime.ErrorObject(err); err == nil || captureErr.Kind != runtime.KindString || !strings.Contains(captureErr.String, "invalid capture index %2") {
		// replacement 中引用不存在的 capture 时，错误文本应包含具体 `%n`。
		t.Fatalf("GSub invalid replacement capture error mismatch: %v", err)
	}

	_, err = GSub(runtime.StringValue("alo"), runtime.StringValue("(%0)"), runtime.StringValue("a"))
	if captureErr := runtime.ErrorObject(err); err == nil || captureErr.Kind != runtime.KindString || !strings.Contains(captureErr.String, "invalid capture index %0") {
		// pattern 中 `%0` 不是合法 back reference。
		t.Fatalf("GSub invalid pattern capture zero error mismatch: %v", err)
	}

	_, err = GSub(runtime.StringValue("alo"), runtime.StringValue("(%1)"), runtime.StringValue("a"))
	if captureErr := runtime.ErrorObject(err); err == nil || captureErr.Kind != runtime.KindString || !strings.Contains(captureErr.String, "invalid capture index %1") {
		// pattern 中向前引用尚未完成的 capture 时应报告具体序号。
		t.Fatalf("GSub invalid pattern capture one error mismatch: %v", err)
	}
}

// TestGSubTableReplacement 验证 string.gsub 的 table 替换语义。
//
// 有 capture 时使用第一 capture 作为 key，table 未命中时保留原匹配文本。
func TestGSubTableReplacement(t *testing.T) {
	// 构造替换表，将 foo/bar 映射为大写。
	replacements := runtime.NewTable()
	replacements.RawSetString("foo", runtime.StringValue("FOO"))
	replacements.RawSetString("bar", runtime.StringValue("BAR"))

	results, err := GSub(
		runtime.StringValue("foo baz bar"),
		runtime.StringValue("(%a+)"),
		runtime.ReferenceValue(runtime.KindTable, replacements),
	)
	if err != nil {
		// table 替换不应失败。
		t.Fatalf("GSub table failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "FOO baz BAR" || results[1].Integer != 3 {
		// baz 未命中替换表，应保留原匹配文本；替换次数仍统计匹配次数。
		t.Fatalf("GSub table result mismatch: %#v", results)
	}

	falseReplacement := runtime.NewTable()
	falseReplacement.RawSetString("al", runtime.StringValue("AA"))
	falseReplacement.RawSetString("o", runtime.BooleanValue(false))
	results, err = GSub(runtime.StringValue("alo alo"), runtime.StringValue("((.)(.?))"), runtime.ReferenceValue(runtime.KindTable, falseReplacement))
	if err != nil {
		// table replacement 中 false 不应作为非法替换值。
		t.Fatalf("GSub false table replacement failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "AAo AAo" || results[1].Integer != 4 {
		// false replacement 应保留原匹配文本，命中次数仍正常统计。
		t.Fatalf("GSub false table replacement result mismatch: %#v", results)
	}

	positionReplacement := runtime.NewTable()
	positionReplacement.RawSetInteger(1, runtime.StringValue("x"))
	positionReplacement.RawSetInteger(2, runtime.StringValue("yy"))
	positionReplacement.RawSetInteger(3, runtime.StringValue("zzz"))
	results, err = GSub(runtime.StringValue("alo alo"), runtime.StringValue("()."), runtime.ReferenceValue(runtime.KindTable, positionReplacement))
	if err != nil {
		// 位置捕获作为 table key 时不应失败。
		t.Fatalf("GSub position table replacement failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "xyyzzz alo" {
		// `()` 返回 integer 位置，必须用数字 key 命中数组区。
		t.Fatalf("GSub position table replacement result mismatch: %#v", results)
	}

	indexedReplacement := runtime.NewTable()
	indexMetatable := runtime.NewTable()
	indexMetatable.RawSetString("__index", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoFunction(func(args ...runtime.Value) (runtime.Value, error) {
		// Go closure 形式 `__index` 接收原 table 和 key，返回动态替换文本。
		if len(args) != 2 || args[1].Kind != runtime.KindString {
			// 参数不符合 table lookup 约定时返回 nil，让测试暴露错误结果。
			return runtime.NilValue(), nil
		}
		return runtime.StringValue(strings.ToUpper(args[1].String)), nil
	})))
	indexedReplacement.SetMetatable(indexMetatable)
	results, err = GSub(runtime.StringValue("a alo b hi"), runtime.StringValue("%w%w+"), runtime.ReferenceValue(runtime.KindTable, indexedReplacement))
	if err != nil {
		// table replacement 应触发 `__index` 普通读取。
		t.Fatalf("GSub indexed table replacement failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "a ALO b HI" {
		// 未命中 raw key 时应使用 `__index` 生成替换文本。
		t.Fatalf("GSub indexed table replacement result mismatch: %#v", results)
	}

	invalid := runtime.NewTable()
	invalid.RawSetString("a", runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()))
	_, err = GSub(runtime.StringValue("alo"), runtime.StringValue("."), runtime.ReferenceValue(runtime.KindTable, invalid))
	errorObject := runtime.ErrorObject(err)
	if err == nil || errorObject.Kind != runtime.KindString || !strings.Contains(errorObject.String, "invalid replacement value (a table)") {
		// table replacement 产生非字符串值时，错误文本必须包含非法值类型。
		t.Fatalf("GSub invalid table replacement error mismatch: %v", err)
	}
}

// TestGSubGoFunctionReplacement 验证 string.gsub 的 Go closure 替换函数语义。
//
// Go closure 会收到 capture 列表；首个返回值为 string 时作为替换文本，nil/false 保留原文。
func TestGSubGoFunctionReplacement(t *testing.T) {
	// 构造等价 string.upper 的替换函数，用于覆盖官方 gc.lua 的 gsub 调用形态。
	upper := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// 替换函数应收到第一个 capture。
		if len(values) != 1 || values[0].Kind != runtime.KindString {
			// 参数形态异常时返回 Lua error，便于测试失败定位。
			return nil, runtime.RaiseError(runtime.StringValue("bad replacement args"))
		}
		return []runtime.Value{runtime.StringValue(strings.ToUpper(values[0].String))}, nil
	})
	results, err := GSub(
		runtime.StringValue("a12 b34"),
		runtime.StringValue("(%d%d*)"),
		runtime.ReferenceValue(runtime.KindGoClosure, upper),
	)
	if err != nil {
		// Go closure 替换不应失败。
		t.Fatalf("GSub Go closure failed: %v", err)
	}
	if len(results) != 2 || results[0].String != "a12 b34" || results[1].Integer != 2 {
		// 数字 upper 后文本不变，但应统计两次匹配。
		t.Fatalf("GSub Go closure result mismatch: %#v", results)
	}

	keepOriginal := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// 返回 false 应保留原匹配文本。
		return []runtime.Value{runtime.BooleanValue(false)}, nil
	})
	kept, err := GSub(
		runtime.StringValue("aa bb"),
		runtime.StringValue("(%a+)"),
		runtime.ReferenceValue(runtime.KindGoClosure, keepOriginal),
	)
	if err != nil {
		// false 替换结果不应失败。
		t.Fatalf("GSub false replacement failed: %v", err)
	}
	if len(kept) != 2 || kept[0].String != "aa bb" || kept[1].Integer != 2 {
		// false 返回值应保留原文但仍统计匹配次数。
		t.Fatalf("GSub false replacement result mismatch: %#v", kept)
	}
}

// TestPackAlignmentAndWideIntegers 验证 string.pack 对齐和 1..16 字节整数兼容。
//
// 用例覆盖官方 tpack.lua 依赖的 `!n`、`Xop`、16 字节符号扩展和负起始位置语义。
func TestPackAlignmentAndWideIntegers(t *testing.T) {
	// `Xh` 和 `Xi8` 只产生对齐填充，不消耗额外参数。
	packed, err := Pack(
		runtime.StringValue(">!8 b Xh i4 i8 c1 Xi8"),
		runtime.IntegerValue(-12),
		runtime.IntegerValue(100),
		runtime.IntegerValue(200),
		runtime.StringValue("\xEC"),
	)
	if err != nil {
		// 合法对齐格式不应失败。
		t.Fatalf("Pack aligned failed: %v", err)
	}
	want := "\xf4" + "\x00\x00\x00" + "\x00\x00\x00\x64" + "\x00\x00\x00\x00\x00\x00\x00\xc8" + "\xec" + "\x00\x00\x00\x00\x00\x00\x00"
	if len(packed) != 1 || packed[0].String != want {
		// 对齐填充位置必须与 Lua 5.3 tpack.lua 断言一致。
		t.Fatalf("Pack aligned result = %q want %q", packed[0].String, want)
	}

	sizeResults, err := PackSize(runtime.StringValue("!16 xXi16"))
	if err != nil {
		// 固定宽度对齐格式应可计算 packsize。
		t.Fatalf("PackSize X i16 failed: %v", err)
	}
	if len(sizeResults) != 1 || sizeResults[0].Integer != 16 {
		// x 后对齐到 16 字节边界，总长应为 16。
		t.Fatalf("PackSize X i16 result mismatch: %#v", sizeResults)
	}

	unpacked, err := Unpack(runtime.StringValue("<i16"), runtime.StringValue("\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff"))
	if err != nil {
		// 全 0xff 是合法 -1 的 16 字节符号扩展。
		t.Fatalf("Unpack i16 sign-extended failed: %v", err)
	}
	if len(unpacked) != 2 || unpacked[0].Integer != -1 || unpacked[1].Integer != 17 {
		// 返回值必须包含解包整数和下一读取位置。
		t.Fatalf("Unpack i16 sign-extended mismatch: %#v", unpacked)
	}

	_, err = Unpack(runtime.StringValue("i16"), runtime.StringValue("\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03"))
	if !errors.Is(err, runtime.ErrLuaError) || !strings.Contains(runtime.ErrorObject(err).String, "16-byte integer") {
		// 非法高位扩展必须暴露官方测试匹配的宽度错误。
		t.Fatalf("Unpack i16 invalid extension error mismatch: %v", err)
	}
}
