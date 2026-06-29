package utf8lib

import (
	"errors"
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestOpenRegistersUTF8Library 验证 Open 会注册 utf8 库和本阶段支持的函数。
//
// 测试通过全局表读取库对象，确认函数以 Go closure 暴露，charpattern 以字符串常量暴露。
func TestOpenRegistersUTF8Library(t *testing.T) {
	// 测试先创建新的 State，避免污染其他标准库注册用例。
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// Open 失败表示 utf8 标准库无法作为全局库暴露。
		t.Fatalf("Open failed: %v", err)
	}

	libraryValue := state.GetGlobal("utf8")
	if libraryValue.Kind != runtime.KindTable {
		// utf8 全局变量必须指向库表。
		t.Fatalf("global utf8 kind mismatch: %v", libraryValue.DebugString())
	}
	library, ok := libraryValue.Ref.(*runtime.Table)
	if !ok || library == nil {
		// KindTable 的引用负载必须是 runtime.Table。
		t.Fatalf("global utf8 payload mismatch: %#v", libraryValue.Ref)
	}

	for _, name := range []string{"char", "codes", "codepoint", "len", "offset"} {
		// 每个本阶段函数都应作为 Go closure 注册在库表上。
		functionValue := library.RawGetString(name)
		if functionValue.Kind != runtime.KindGoClosure {
			// 缺失或类型错误都会导致 VM CALL 无法进入标准库函数。
			t.Fatalf("utf8.%s kind mismatch: %v", name, functionValue.DebugString())
		}
	}

	patternValue := library.RawGetString("charpattern")
	if patternValue.Kind != runtime.KindString || patternValue.String != CharPattern {
		// charpattern 必须注册为 Lua pattern 字符串常量。
		t.Fatalf("utf8.charpattern mismatch: %v", patternValue.DebugString())
	}
}

// TestCharEncodesCodePoints 验证 utf8.char 会把码点编码为 UTF-8 字符串。
//
// 多个 integer 码点按参数顺序拼接；无参数时返回空字符串。
func TestCharEncodesCodePoints(t *testing.T) {
	// ASCII 与中文码点应按 UTF-8 编码后拼接。
	results, err := Char(runtime.IntegerValue(0x41), runtime.IntegerValue(0x4E2D))
	if err != nil {
		// 合法码点编码不应失败。
		t.Fatalf("Char failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "A中" {
		// char 结果必须是拼接后的 UTF-8 字符串。
		t.Fatalf("Char result mismatch: %#v", results)
	}

	results, err = Char()
	if err != nil {
		// 无参数 char 不应失败。
		t.Fatalf("Char empty failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "" {
		// 无参数 char 必须返回空字符串。
		t.Fatalf("Char empty result mismatch: %#v", results)
	}
}

// TestCodesReturnsIteratorTriplet 验证 utf8.codes 的迭代三元组。
//
// iterator 每次返回下一个字符的 1-based 字节位置和 Unicode 码点；遍历结束返回空结果。
func TestCodesReturnsIteratorTriplet(t *testing.T) {
	// codes 返回 iterator、原始字符串和初始控制变量。
	results, err := Codes(runtime.StringValue("A中"))
	if err != nil {
		// 合法字符串 codes 调用不应失败。
		t.Fatalf("Codes failed: %v", err)
	}
	if len(results) != 3 || results[0].Kind != runtime.KindGoClosure || results[1].String != "A中" || results[2].Integer != 0 {
		// codes 必须返回 Lua generic for 可使用的三元组。
		t.Fatalf("Codes triplet mismatch: %#v", results)
	}

	iterator, ok := results[0].Ref.(runtime.GoResultsFunction)
	if !ok {
		// iterator 的 Ref 必须是 runtime.GoResultsFunction。
		t.Fatalf("Codes iterator payload mismatch: %#v", results[0].Ref)
	}

	first, err := iterator(results[1], results[2])
	if err != nil {
		// 第一次迭代不应失败。
		t.Fatalf("Codes first iteration failed: %v", err)
	}
	if len(first) != 2 || first[0].Integer != 1 || first[1].Integer != 0x41 {
		// 第一个字符 A 位于第 1 个字节。
		t.Fatalf("Codes first result mismatch: %#v", first)
	}

	second, err := iterator(results[1], first[0])
	if err != nil {
		// 第二次迭代不应失败。
		t.Fatalf("Codes second iteration failed: %v", err)
	}
	if len(second) != 2 || second[0].Integer != 2 || second[1].Integer != 0x4E2D {
		// 第二个字符 中 位于第 2 个字节。
		t.Fatalf("Codes second result mismatch: %#v", second)
	}

	done, err := iterator(results[1], second[0])
	if err != nil {
		// 结束迭代不应失败。
		t.Fatalf("Codes final iteration failed: %v", err)
	}
	if len(done) != 0 {
		// 结束迭代必须返回空结果。
		t.Fatalf("Codes final result mismatch: %#v", done)
	}
}

// TestCodePointReturnsCodePoints 验证 utf8.codepoint 的范围读取语义。
//
// 默认读取第一个字符；显式范围按 1-based 字节位置返回所有起始位置落入范围的码点。
func TestCodePointReturnsCodePoints(t *testing.T) {
	// 默认范围只返回第一个字符码点。
	results, err := CodePoint(runtime.StringValue("A中"))
	if err != nil {
		// 合法 codepoint 调用不应失败。
		t.Fatalf("CodePoint default failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 0x41 {
		// 默认 codepoint 应返回首字符 A。
		t.Fatalf("CodePoint default result mismatch: %#v", results)
	}

	results, err = CodePoint(runtime.StringValue("A中"), runtime.IntegerValue(1), runtime.IntegerValue(2))
	if err != nil {
		// 合法范围 codepoint 调用不应失败。
		t.Fatalf("CodePoint range failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 0x41 || results[1].Integer != 0x4E2D {
		// 范围 [1,2] 的起始字节覆盖 A 和 中。
		t.Fatalf("CodePoint range result mismatch: %#v", results)
	}
}

// TestLenCountsUTF8CharactersAndReportsInvalid 验证 utf8.len 的计数与非法字节返回。
//
// 成功时返回字符数量；遇到非法 UTF-8 时返回 nil 和首个非法字节位置。
func TestLenCountsUTF8CharactersAndReportsInvalid(t *testing.T) {
	// 混合 ASCII 和中文字符应统计为两个 UTF-8 字符。
	results, err := Len(runtime.StringValue("A中"))
	if err != nil {
		// 合法 len 调用不应失败。
		t.Fatalf("Len failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 2 {
		// len 必须返回 UTF-8 字符数量。
		t.Fatalf("Len result mismatch: %#v", results)
	}

	results, err = Len(runtime.StringValue("A\xff"))
	if err != nil {
		// 非法 UTF-8 不应抛出 Go error，而应返回 nil 和位置。
		t.Fatalf("Len invalid failed: %v", err)
	}
	if len(results) != 2 || results[0].Kind != runtime.KindNil || results[1].Kind != runtime.KindInteger || results[1].Integer != 2 {
		// 非法字节位于第 2 个字节。
		t.Fatalf("Len invalid result mismatch: %#v", results)
	}
}

// TestOffsetFindsUTF8BytePositions 验证 utf8.offset 的正向、反向和包含字节定位。
//
// 字符串 `A中B` 的字符起始字节分别是 1、2、5；测试覆盖 Lua 1-based 字节位置语义。
func TestOffsetFindsUTF8BytePositions(t *testing.T) {
	// 正向查找第二个字符应返回字节位置 2。
	results, err := Offset(runtime.StringValue("A中B"), runtime.IntegerValue(2))
	if err != nil {
		// 合法正向 offset 调用不应失败。
		t.Fatalf("Offset forward failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 2 {
		// 第二个字符 中 起始于第 2 个字节。
		t.Fatalf("Offset forward result mismatch: %#v", results)
	}

	results, err = Offset(runtime.StringValue("A中B"), runtime.IntegerValue(-1))
	if err != nil {
		// 合法反向 offset 调用不应失败。
		t.Fatalf("Offset backward failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 5 {
		// 倒数第一个字符 B 起始于第 5 个字节。
		t.Fatalf("Offset backward result mismatch: %#v", results)
	}

	results, err = Offset(runtime.StringValue("A中B"), runtime.IntegerValue(0), runtime.IntegerValue(3))
	if err != nil {
		// n==0 的包含字节定位不应失败。
		t.Fatalf("Offset containing failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 2 {
		// 第 3 个字节位于字符 中 内，其起始位置是 2。
		t.Fatalf("Offset containing result mismatch: %#v", results)
	}

	results, err = Offset(runtime.StringValue(""), runtime.IntegerValue(0))
	if err != nil {
		// 空字符串 n==0 不应失败。
		t.Fatalf("Offset empty containing failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 1 {
		// Lua 5.3 对空字符串的唯一边界返回 1。
		t.Fatalf("Offset empty containing result mismatch: %#v", results)
	}

	results, err = Offset(runtime.StringValue("A"), runtime.IntegerValue(2))
	if err != nil {
		// 查找到尾后哨兵位置时不应失败。
		t.Fatalf("Offset single tail sentinel failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 2 {
		// Lua 5.3 允许从首字符继续查找尾后位置。
		t.Fatalf("Offset single tail sentinel result mismatch: %#v", results)
	}

	results, err = Offset(runtime.StringValue("A"), runtime.IntegerValue(3))
	if err != nil {
		// 字符数量明显不足时不应失败。
		t.Fatalf("Offset missing failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindNil {
		// 超过尾后哨兵位置时必须返回 nil。
		t.Fatalf("Offset missing result mismatch: %#v", results)
	}

	results, err = Offset(runtime.StringValue("abc"), runtime.IntegerValue(2), runtime.IntegerValue(3))
	if err != nil {
		// 从最后一个字符起点查找下一个字符不应失败。
		t.Fatalf("Offset tail sentinel failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindInteger || results[0].Integer != 4 {
		// Lua 5.3 在该场景返回尾后位置 #s+1。
		t.Fatalf("Offset tail sentinel result mismatch: %#v", results)
	}
}

// TestUTF8InvalidBoundaries 验证 UTF-8 标准库对非法字节边界的处理。
//
// len 返回 nil 和位置；codepoint、codes iterator、offset 对非法编码返回 Lua error。
func TestUTF8InvalidBoundaries(t *testing.T) {
	// 截断的三字节序列应在 utf8.len 中报告首个非法字节。
	results, err := Len(runtime.StringValue("A\xe4"))
	if err != nil {
		// len 非法输入应返回 Lua 值而不是 Go error。
		t.Fatalf("Len truncated failed: %v", err)
	}
	if len(results) != 2 || results[0].Kind != runtime.KindNil || results[1].Integer != 2 {
		// 截断序列从第 2 个字节开始非法。
		t.Fatalf("Len truncated result mismatch: %#v", results)
	}

	_, err = CodePoint(runtime.StringValue("\xff"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// codepoint 遇到非法起始字节必须返回 Lua error。
		t.Fatalf("CodePoint invalid error mismatch: %v", err)
	}

	codesResults, err := Codes(runtime.StringValue("\xff"))
	if err != nil {
		// 创建 codes 迭代器不应提前扫描字符串。
		t.Fatalf("Codes invalid setup failed: %v", err)
	}
	iterator, ok := codesResults[0].Ref.(runtime.GoResultsFunction)
	if !ok {
		// iterator 的 Ref 必须是 runtime.GoResultsFunction。
		t.Fatalf("Codes invalid iterator payload mismatch: %#v", codesResults[0].Ref)
	}
	_, err = iterator(codesResults[1], codesResults[2])
	if !errors.Is(err, runtime.ErrLuaError) {
		// codes iterator 遇到非法起始字节必须返回 Lua error。
		t.Fatalf("Codes invalid iteration error mismatch: %v", err)
	}

	_, err = Offset(runtime.StringValue("A中"), runtime.IntegerValue(1), runtime.IntegerValue(3))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 正向 offset 起点不能是 continuation byte。
		t.Fatalf("Offset continuation error mismatch: %v", err)
	}
}

// TestUTF8ArgumentErrors 验证 utf8 标准库参数错误以 Lua error 传播。
//
// 非 string、非 integer 和非法码点都必须返回 runtime.ErrLuaError 链路。
func TestUTF8ArgumentErrors(t *testing.T) {
	// char 参数必须是合法 integer 码点。
	_, err := Char(runtime.IntegerValue(0xD800))
	if !errors.Is(err, runtime.ErrLuaError) {
		// surrogate 码点必须以 Lua error 传播。
		t.Fatalf("Char surrogate error mismatch: %v", err)
	}

	_, err = CodePoint(runtime.IntegerValue(1))
	if !errors.Is(err, runtime.ErrLuaError) {
		// codepoint 第一个参数必须是 string。
		t.Fatalf("CodePoint argument error mismatch: %v", err)
	}

	_, err = Offset(runtime.StringValue("abc"), runtime.StringValue("bad"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// offset 第二个参数必须是 integer。
		t.Fatalf("Offset argument error mismatch: %v", err)
	}
}
