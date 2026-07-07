package base

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/runtime"
	stringlib "github.com/ZingYao/go-lua-vm/stdlib/string"
)

// TestOpenRegistersGlobalsAndVersion 验证 base.Open 注册 `_G` 与 `_VERSION`。
//
// 该用例覆盖本轮 `_G`、`_VERSION` TODO，并确认全局表自引用和版本文本稳定。
func TestOpenRegistersGlobalsAndVersion(t *testing.T) {
	// 创建独立 State，避免全局表注册结果被其他测试污染。
	state := runtime.NewState()

	if err := Open(state); err != nil {
		// 正常 State 必须能注册 base 标准库。
		t.Fatalf("open base failed: %v", err)
	}
	globalValue := state.GetGlobal("_G")
	if globalValue.Kind != runtime.KindTable || globalValue.Ref != state.Globals() {
		// `_G` 必须指回当前 State 的全局表。
		t.Fatalf("_G mismatch: %#v", globalValue)
	}
	versionValue := state.GetGlobal("_VERSION")
	if !versionValue.RawEqual(runtime.StringValue(VersionText)) {
		// `_VERSION` 文本必须保持 Lua 5.3 兼容。
		t.Fatalf("_VERSION mismatch: %#v", versionValue)
	}
}

// TestOpenRegistersBaseFunctions 验证 base.Open 注册本轮 base 函数。
//
// assert、collectgarbage、dofile、error、getmetatable、ipairs、load、loadfile、next、pairs、
// pcall、print、rawequal、rawget、rawlen、rawset、select、setmetatable、tonumber、tostring、
// type 和 xpcall 必须作为 Go closure 写入全局表。
func TestOpenRegistersBaseFunctions(t *testing.T) {
	// 初始化并打开 base 库。
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// 注册失败会导致后续函数检查没有意义。
		t.Fatalf("open base failed: %v", err)
	}

	for _, functionName := range []string{"assert", "collectgarbage", "dofile", "error", "getmetatable", "ipairs", "load", "loadfile", "next", "pairs", "pcall", "print", "rawequal", "rawget", "rawlen", "rawset", "select", "setmetatable", "tonumber", "tostring", "type", "xpcall"} {
		// 逐个检查函数槽位是否是 Go closure。
		value := state.GetGlobal(functionName)
		if value.Kind != runtime.KindGoClosure {
			// base 函数必须以 Go closure 形式暴露。
			t.Fatalf("%s should be go closure, got %#v", functionName, value)
		}
	}
}

// TestPairsAndIPairsArgumentErrors 验证 pairs/ipairs 缺参错误文本。
//
// 官方 nextvar.lua 使用 pcall 检查错误中包含 bad argument，不能泄露 runtime 内部错误文本。
func TestPairsAndIPairsArgumentErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		call func(...runtime.Value) ([]runtime.Value, error)
	}{
		{name: "pairs", call: Pairs},
		{name: "ipairs", call: IPairs},
	} {
		_, err := testCase.call()
		if !errors.Is(err, runtime.ErrLuaError) || !strings.Contains(runtime.ErrorObject(err).String, "bad argument") {
			// 缺少 table 参数时必须以 Lua 参数错误暴露。
			t.Fatalf("%s missing arg error mismatch: %v", testCase.name, err)
		}
	}
}

// TestToNumberToStringAndTypeSemantics 验证 tonumber、tostring 和 type 基础语义。
//
// tonumber 覆盖普通转换和 base 转换；tostring 复用 runtime 文本；type 合并 integer/number 和 closure 类型名。
func TestToNumberToStringAndTypeSemantics(t *testing.T) {
	numberResults, err := ToNumber(runtime.StringValue("0x10"))
	if err != nil {
		// 普通 tonumber 不应返回 Go error。
		t.Fatalf("tonumber hex failed: %v", err)
	}
	if len(numberResults) != 1 || !numberResults[0].RawEqual(runtime.IntegerValue(16)) {
		// 十六进制字符串应转换为 integer。
		t.Fatalf("tonumber hex mismatch: %#v", numberResults)
	}

	baseResults, err := ToNumber(runtime.StringValue("ff"), runtime.IntegerValue(16))
	if err != nil {
		// 带 base 的 tonumber 不应返回 Go error。
		t.Fatalf("tonumber base failed: %v", err)
	}
	if len(baseResults) != 1 || !baseResults[0].RawEqual(runtime.IntegerValue(255)) {
		// base=16 时 ff 应转换为 255。
		t.Fatalf("tonumber base mismatch: %#v", baseResults)
	}
	if _, err := ToNumber(); err == nil {
		// Lua 5.3 要求 tonumber 缺少第一个参数时报错。
		t.Fatalf("tonumber without arguments should fail")
	}

	stringResults, err := ToString(runtime.BooleanValue(true))
	if err != nil {
		// boolean tostring 不应失败。
		t.Fatalf("tostring failed: %v", err)
	}
	if len(stringResults) != 1 || !stringResults[0].RawEqual(runtime.StringValue("true")) {
		// true 的 tostring 文本必须为 true。
		t.Fatalf("tostring mismatch: %#v", stringResults)
	}
	if _, err := ToString(); err == nil {
		// Lua 5.3 要求 tostring 缺少第一个参数时报错。
		t.Fatalf("tostring without arguments should fail")
	}

	typeResults, err := Type(runtime.IntegerValue(53))
	if err != nil {
		// type 不应失败。
		t.Fatalf("type failed: %v", err)
	}
	if len(typeResults) != 1 || !typeResults[0].RawEqual(runtime.StringValue("number")) {
		// integer 在 Lua type 中应显示为 number。
		t.Fatalf("type mismatch: %#v", typeResults)
	}

	if _, err := Type(); err == nil {
		// Lua 5.3 的 type 缺参必须抛错，供 pcall(type) 返回 false。
		t.Fatalf("expected type without arguments to fail")
	}
}

// TestXPCallCapturesAndHandlesError 验证 xpcall 的成功、错误处理和 handler 失败语义。
//
// 当前阶段 xpcall 执行 Go closure，被调函数失败后用 handler 返回值替换错误对象。
func TestXPCallCapturesAndHandlesError(t *testing.T) {
	successFunction := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 成功函数返回固定值。
		return []runtime.Value{runtime.StringValue("ok")}, nil
	}))
	handlerFunction := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// handler 返回收到的错误对象。
		return []runtime.Value{args[0]}, nil
	}))
	successResults, err := XPCall(successFunction, handlerFunction)
	if err != nil {
		// xpcall 成功路径不应返回 Go error。
		t.Fatalf("xpcall success failed: %v", err)
	}
	if len(successResults) != 2 || !successResults[0].RawEqual(runtime.BooleanValue(true)) || !successResults[1].RawEqual(runtime.StringValue("ok")) {
		// 成功布局必须是 true 后接返回值。
		t.Fatalf("xpcall success mismatch: %#v", successResults)
	}

	errorFunction := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 被调函数抛出 Lua error。
		return nil, runtime.RaiseError(runtime.StringValue("boom"))
	}))
	errorResults, err := XPCall(errorFunction, handlerFunction)
	if err != nil {
		// xpcall 错误路径不应继续上抛。
		t.Fatalf("xpcall error returned go error: %v", err)
	}
	if len(errorResults) != 2 || !errorResults[0].RawEqual(runtime.BooleanValue(false)) || !errorResults[1].RawEqual(runtime.StringValue("boom")) {
		// handler 应收到并返回原始错误对象。
		t.Fatalf("xpcall error mismatch: %#v", errorResults)
	}
}

// TestRawGetLenSetSemantics 验证 rawget/rawlen/rawset 的 table 和 string 基础语义。
//
// raw 操作不能触发元方法；rawlen 对 string 返回字节长度，对 table 返回基础边界长度。
func TestRawGetLenSetSemantics(t *testing.T) {
	// 构造 table 并通过 rawset 写入字段。
	table := runtime.NewTable()
	tableValue := runtime.ReferenceValue(runtime.KindTable, table)
	setResults, err := RawSet(tableValue, runtime.StringValue("name"), runtime.StringValue("lua"))
	if err != nil {
		// rawset 对合法 table/key/value 不应失败。
		t.Fatalf("rawset failed: %v", err)
	}
	if len(setResults) != 1 || !setResults[0].RawEqual(tableValue) {
		// rawset 必须返回原 table。
		t.Fatalf("rawset results mismatch: %#v", setResults)
	}

	getResults, err := RawGet(tableValue, runtime.StringValue("name"))
	if err != nil {
		// rawget 对合法 table/key 不应失败。
		t.Fatalf("rawget failed: %v", err)
	}
	if len(getResults) != 1 || !getResults[0].RawEqual(runtime.StringValue("lua")) {
		// rawget 必须返回刚写入的值。
		t.Fatalf("rawget results mismatch: %#v", getResults)
	}

	table.RawSetInteger(1, runtime.StringValue("first"))
	lengthResults, err := RawLen(tableValue)
	if err != nil {
		// rawlen table 不应失败。
		t.Fatalf("rawlen table failed: %v", err)
	}
	if len(lengthResults) != 1 || !lengthResults[0].RawEqual(runtime.IntegerValue(1)) {
		// table rawlen 必须返回基础长度边界。
		t.Fatalf("rawlen table mismatch: %#v", lengthResults)
	}

	stringLengthResults, err := RawLen(runtime.StringValue("好"))
	if err != nil {
		// rawlen string 不应失败。
		t.Fatalf("rawlen string failed: %v", err)
	}
	if len(stringLengthResults) != 1 || !stringLengthResults[0].RawEqual(runtime.IntegerValue(3)) {
		// string rawlen 必须按字节计算 UTF-8 长度。
		t.Fatalf("rawlen string mismatch: %#v", stringLengthResults)
	}
}

// TestSelectSemantics 验证 select 的计数、正索引和负索引。
//
// `#` 返回参数数量；正索引从前选取，负索引从尾部选取。
func TestSelectSemantics(t *testing.T) {
	countResults, err := Select(runtime.StringValue("#"), runtime.StringValue("a"), runtime.StringValue("b"))
	if err != nil {
		// select("#", ...) 不应失败。
		t.Fatalf("select count failed: %v", err)
	}
	if len(countResults) != 1 || !countResults[0].RawEqual(runtime.IntegerValue(2)) {
		// 计数结果必须是剩余参数数量。
		t.Fatalf("select count mismatch: %#v", countResults)
	}

	sliceResults, err := Select(runtime.IntegerValue(2), runtime.StringValue("a"), runtime.StringValue("b"), runtime.StringValue("c"))
	if err != nil {
		// 正索引在范围内不应失败。
		t.Fatalf("select positive failed: %v", err)
	}
	if len(sliceResults) != 2 || !sliceResults[0].RawEqual(runtime.StringValue("b")) || !sliceResults[1].RawEqual(runtime.StringValue("c")) {
		// select(2, a, b, c) 应返回 b, c。
		t.Fatalf("select positive mismatch: %#v", sliceResults)
	}

	tailResults, err := Select(runtime.IntegerValue(-1), runtime.StringValue("a"), runtime.StringValue("b"))
	if err != nil {
		// 负索引在范围内不应失败。
		t.Fatalf("select negative failed: %v", err)
	}
	if len(tailResults) != 1 || !tailResults[0].RawEqual(runtime.StringValue("b")) {
		// select(-1, a, b) 应返回最后一个参数。
		t.Fatalf("select negative mismatch: %#v", tailResults)
	}
}

// TestSetMetatableSemantics 验证 setmetatable 设置、移除和保护边界。
//
// 第二参数为 table 时设置元表，为 nil 时移除元表；受保护元表不允许覆盖。
func TestSetMetatableSemantics(t *testing.T) {
	// 创建目标 table 和元表。
	table := runtime.NewTable()
	metatable := runtime.NewTable()
	tableValue := runtime.ReferenceValue(runtime.KindTable, table)
	metatableValue := runtime.ReferenceValue(runtime.KindTable, metatable)

	results, err := SetMetatable(tableValue, metatableValue)
	if err != nil {
		// 未保护 table 设置元表不应失败。
		t.Fatalf("setmetatable failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(tableValue) || table.GetMetatable() != metatable {
		// setmetatable 必须返回原 table 并写入元表。
		t.Fatalf("setmetatable results mismatch: results=%#v metatable=%#v", results, table.GetMetatable())
	}

	protected := runtime.NewTable()
	protected.RawSetString("__metatable", runtime.StringValue("locked"))
	table.SetMetatable(protected)
	_, err = SetMetatable(tableValue, runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()))
	if !errors.Is(err, runtime.ErrProtectedMetatable) {
		// 受保护元表必须拒绝覆盖。
		t.Fatalf("setmetatable protected mismatch: %v", err)
	}
}

// TestNextAndPairsSemantics 验证 next 和 pairs 返回 raw 迭代结果。
//
// next 应直接返回第一组 key/value；pairs 应返回 iterator、state、nil 初始 key 三元组。
func TestNextAndPairsSemantics(t *testing.T) {
	// 构造包含一个字段的 table，保证迭代结果稳定。
	table := runtime.NewTable()
	table.RawSetString("name", runtime.StringValue("lua"))
	tableValue := runtime.ReferenceValue(runtime.KindTable, table)

	nextResults, err := Next(tableValue)
	if err != nil {
		// next 对合法 table 不应失败。
		t.Fatalf("next failed: %v", err)
	}
	if len(nextResults) != 2 || !nextResults[0].RawEqual(runtime.StringValue("name")) || !nextResults[1].RawEqual(runtime.StringValue("lua")) {
		// next 必须返回第一组 key/value。
		t.Fatalf("next results mismatch: %#v", nextResults)
	}

	pairsResults, err := Pairs(tableValue)
	if err != nil {
		// pairs 对合法 table 不应失败。
		t.Fatalf("pairs failed: %v", err)
	}
	if len(pairsResults) != 3 || pairsResults[0].Kind != runtime.KindGoClosure || !pairsResults[1].RawEqual(tableValue) || !pairsResults[2].IsNil() {
		// pairs 三元组布局必须是 iterator、state、nil。
		t.Fatalf("pairs results mismatch: %#v", pairsResults)
	}
}

// TestOpenPairsGenericForKeepsTableKeys 验证 pairs 在泛型 for 中保留 table key。
//
// 官方 gc.lua 的 clearing tables 段使用 table 作为 key，TFORCALL 必须把 next 的 key/value
// 写入迭代变量区，不能覆盖 iterator/state/control。
func TestOpenPairsGenericForKeepsTableKeys(t *testing.T) {
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// base.Open 不应在有效 State 上失败。
		t.Fatalf("Open failed: %v", err)
	}
	loaded, err := loadWithState(state, runtime.StringValue("local a = {}; a[{}] = 7; local sum = 0; for k, v in pairs(a) do if type(k) ~= 'table' then error('bad key') end; sum = sum + v end; return sum"))
	if err != nil {
		// 合法 pairs 泛型 for chunk 加载不应失败。
		t.Fatalf("load pairs generic for failed: %v", err)
	}
	results, err := callProtectedWithState(state, loaded[0])
	if err != nil {
		// pairs 泛型 for 执行不应失败。
		t.Fatalf("execute pairs generic for failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(runtime.IntegerValue(7)) {
		// table key 应进入 k，value 应进入 v 并完成求和。
		t.Fatalf("pairs generic for results mismatch: %#v", results)
	}
}

// TestPCallCapturesGoClosure 验证 pcall 捕获 Go closure 成功和失败。
//
// 成功返回 true 后接返回值；失败返回 false 和 Lua error object，不继续上抛。
func TestPCallCapturesGoClosure(t *testing.T) {
	successFunction := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 成功函数返回第一个参数。
		return []runtime.Value{args[0]}, nil
	}))
	successResults, err := PCall(successFunction, runtime.StringValue("ok"))
	if err != nil {
		// pcall 自身不应上抛成功函数错误。
		t.Fatalf("pcall success failed: %v", err)
	}
	if len(successResults) != 2 || !successResults[0].RawEqual(runtime.BooleanValue(true)) || !successResults[1].RawEqual(runtime.StringValue("ok")) {
		// 成功布局必须是 true 后接函数返回值。
		t.Fatalf("pcall success results mismatch: %#v", successResults)
	}

	errorFunction := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 失败函数抛出 Lua error object。
		return nil, runtime.RaiseError(runtime.StringValue("bad"))
	}))
	errorResults, err := PCall(errorFunction)
	if err != nil {
		// pcall 捕获错误后不应返回 Go error。
		t.Fatalf("pcall error returned go error: %v", err)
	}
	if len(errorResults) != 2 || !errorResults[0].RawEqual(runtime.BooleanValue(false)) || !errorResults[1].RawEqual(runtime.StringValue("bad")) {
		// 失败布局必须是 false 后接错误对象。
		t.Fatalf("pcall error results mismatch: %#v", errorResults)
	}
}

// TestOpenPCallCapturesLuaClosure 验证注册到 State 的 pcall/xpcall 可执行 Lua closure。
//
// base.Open 捕获 State 后，pcall/xpcall 必须通过 VM 执行 Lua closure，并保持成功和错误返回布局。
func TestOpenPCallCapturesLuaClosure(t *testing.T) {
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// base.Open 不应在有效 State 上失败。
		t.Fatalf("Open failed: %v", err)
	}
	loaded, err := loadWithState(state, runtime.StringValue("return 9"))
	if err != nil {
		// 合法 chunk 加载不应失败。
		t.Fatalf("loadWithState return chunk failed: %v", err)
	}
	pcallValue := state.GetGlobal("pcall")
	pcallFunction := pcallValue.Ref.(runtime.GoResultsFunction)
	successResults, err := pcallFunction(loaded[0])
	if err != nil {
		// pcall 执行 Lua closure 不应上抛成功路径错误。
		t.Fatalf("pcall lua closure failed: %v", err)
	}
	if len(successResults) != 2 || !successResults[0].RawEqual(runtime.BooleanValue(true)) || !successResults[1].RawEqual(runtime.IntegerValue(9)) {
		// 成功布局必须是 true 后接 Lua closure 返回值。
		t.Fatalf("pcall lua closure results mismatch: %#v", successResults)
	}

	errorLoaded, err := loadWithState(state, runtime.StringValue("error('bad')"))
	if err != nil {
		// 合法 error chunk 加载不应失败。
		t.Fatalf("loadWithState error chunk failed: %v", err)
	}
	xpcallValue := state.GetGlobal("xpcall")
	xpcallFunction := xpcallValue.Ref.(runtime.GoResultsFunction)
	handler := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// handler 返回稳定文本，验证 xpcall 调用错误处理函数。
		return []runtime.Value{runtime.StringValue("handled:" + args[0].String)}, nil
	}))
	errorResults, err := xpcallFunction(errorLoaded[0], handler)
	if err != nil {
		// xpcall 捕获 Lua closure 错误后不应上抛。
		t.Fatalf("xpcall lua closure failed: %v", err)
	}
	if len(errorResults) != 2 || !errorResults[0].RawEqual(runtime.BooleanValue(false)) || !errorResults[1].RawEqual(runtime.StringValue("handled:error('bad'):1: bad")) {
		// 错误布局必须是 false 后接 handler 返回值。
		t.Fatalf("xpcall lua closure results mismatch: %#v", errorResults)
	}
}

// TestOpenPCallLuaClosureErrorDoesNotLeakFrames 验证 Lua closure 错误不会泄漏调用帧。
//
// 官方 gc.lua 会大量执行 pcall(load(...))，失败 chunk 若泄漏帧会在后续步骤触发 C stack overflow。
func TestOpenPCallLuaClosureErrorDoesNotLeakFrames(t *testing.T) {
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// base.Open 不应在有效 State 上失败。
		t.Fatalf("Open failed: %v", err)
	}
	loaded, err := loadWithState(state, runtime.StringValue("error('bad')"))
	if err != nil {
		// 合法 error chunk 加载不应失败。
		t.Fatalf("loadWithState error chunk failed: %v", err)
	}
	pcallValue := state.GetGlobal("pcall")
	pcallFunction := pcallValue.Ref.(runtime.GoResultsFunction)
	for index := 0; index < 5; index++ {
		// 重复捕获错误，验证每次执行结束都会回收 Lua 调用帧。
		results, callErr := pcallFunction(loaded[0])
		if callErr != nil {
			// pcall 捕获错误后不应上抛 Go error。
			t.Fatalf("pcall lua closure error failed: %v", callErr)
		}
		if len(results) != 2 || !results[0].RawEqual(runtime.BooleanValue(false)) {
			// 错误布局必须是 false 后接错误对象。
			t.Fatalf("pcall lua closure error results mismatch: %#v", results)
		}
		if state.CallDepth() != 0 {
			// 错误路径不得泄漏调用帧。
			t.Fatalf("call depth leaked after pcall #%d: %d", index, state.CallDepth())
		}
	}
}

// TestOpenPCallCapturesInterrupt 验证 pcall 能捕获宿主请求的 Lua 级中断。
//
// Ctrl-C 进入 VM 后应表现为一次 `interrupted!` Lua error，pcall 捕获后后续检查点继续可用。
func TestOpenPCallCapturesInterrupt(t *testing.T) {
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// base.Open 不应在有效 State 上失败。
		t.Fatalf("Open failed: %v", err)
	}
	loaded, err := loadWithState(state, runtime.StringValue("while true do end"))
	if err != nil {
		// 合法循环 chunk 加载不应失败。
		t.Fatalf("loadWithState loop chunk failed: %v", err)
	}
	pcallValue := state.GetGlobal("pcall")
	pcallFunction := pcallValue.Ref.(runtime.GoResultsFunction)
	state.RequestInterrupt()
	results, err := pcallFunction(loaded[0])
	if err != nil {
		// pcall 捕获中断后不应继续上抛 Go error。
		t.Fatalf("pcall interrupt failed: %v", err)
	}
	if len(results) != 2 || !results[0].RawEqual(runtime.BooleanValue(false)) || !results[1].RawEqual(runtime.StringValue("interrupted!")) {
		// 中断错误布局必须是 false 后接 interrupted! 错误对象。
		t.Fatalf("pcall interrupt results mismatch: %#v", results)
	}
	if err := state.CheckContext(); err != nil {
		// 中断已由 pcall 内部 VM 检查点消费，后续脚本应能继续。
		t.Fatalf("interrupt should be consumed after pcall, got %v", err)
	}
}

// TestPrintToWritesTabSeparatedValues 验证 print 输出转换和分隔符。
//
// print 使用 tostring 转换参数，以 tab 分隔并追加换行。
func TestPrintToWritesTabSeparatedValues(t *testing.T) {
	// 使用 bytes.Buffer 避免测试污染标准输出。
	var buffer bytes.Buffer
	if _, err := PrintTo(&buffer, runtime.StringValue("lua"), runtime.IntegerValue(53), runtime.BooleanValue(true)); err != nil {
		// 合法参数必须能输出成功。
		t.Fatalf("print failed: %v", err)
	}
	if got := buffer.String(); got != "lua\t53\ttrue\n" {
		// 输出文本必须与 Lua print 分隔规则一致。
		t.Fatalf("print output mismatch: %q", got)
	}
}

// TestRawEqualSemantics 验证 rawequal 不触发元方法并返回 boolean。
//
// integer 与相同 integer 相等；不同类型或值不相等。
func TestRawEqualSemantics(t *testing.T) {
	equalResults, err := RawEqual(runtime.IntegerValue(1), runtime.IntegerValue(1))
	if err != nil {
		// rawequal 不应返回错误。
		t.Fatalf("rawequal equal failed: %v", err)
	}
	if len(equalResults) != 1 || !equalResults[0].RawEqual(runtime.BooleanValue(true)) {
		// 相同 integer 必须 raw 相等。
		t.Fatalf("rawequal equal mismatch: %#v", equalResults)
	}

	notEqualResults, err := RawEqual(runtime.StringValue("1"), runtime.IntegerValue(1))
	if err != nil {
		// rawequal 不应返回错误。
		t.Fatalf("rawequal not equal failed: %v", err)
	}
	if len(notEqualResults) != 1 || !notEqualResults[0].RawEqual(runtime.BooleanValue(false)) {
		// string 与 integer 不应 raw 相等。
		t.Fatalf("rawequal not equal mismatch: %#v", notEqualResults)
	}
}

// TestErrorRaisesObject 验证 base error 会保留 Lua error object。
//
// 第一个参数必须作为错误对象进入 RuntimeError 链；未传参数时错误对象为 nil。
func TestErrorRaisesObject(t *testing.T) {
	_, err := Error(runtime.StringValue("boom"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// error 调用必须被 runtime 识别为 Lua error。
		t.Fatalf("error classification mismatch: %v", err)
	}
	if !runtime.ErrorObject(err).RawEqual(runtime.StringValue("boom")) {
		// 第一个参数必须原样成为错误对象。
		t.Fatalf("error object mismatch: %#v", runtime.ErrorObject(err))
	}
}

// TestGetMetatableSemantics 验证 getmetatable 返回公开元表值。
//
// 未保护元表返回真实 table，存在 `__metatable` 时返回保护字段。
func TestGetMetatableSemantics(t *testing.T) {
	// 构造带受保护元表的 table。
	table := runtime.NewTable()
	metatable := runtime.NewTable()
	metatable.RawSetString("__metatable", runtime.StringValue("locked"))
	table.SetMetatable(metatable)

	results, err := GetMetatable(runtime.ReferenceValue(runtime.KindTable, table))
	if err != nil {
		// getmetatable 不应对普通 table 返回错误。
		t.Fatalf("getmetatable failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(runtime.StringValue("locked")) {
		// 受保护元表必须隐藏真实 table。
		t.Fatalf("getmetatable protected mismatch: %#v", results)
	}

	results, err = GetMetatable(runtime.StringValue("not-table"))
	if err != nil {
		// 非 table 值当前没有元表，不应报错。
		t.Fatalf("getmetatable non table failed: %v", err)
	}
	if len(results) != 1 || !results[0].IsNil() {
		// 当前阶段非 table 类型返回 nil。
		t.Fatalf("getmetatable non table mismatch: %#v", results)
	}
}

// TestIPairsReturnsIteratorTriple 验证 ipairs 返回 raw integer 迭代三元组。
//
// 返回值应为 iterator、原 table 和 0 初始索引，迭代器执行后返回第一个 index/value。
func TestIPairsReturnsIteratorTriple(t *testing.T) {
	// 构造数组区含第一项的 table。
	table := runtime.NewTable()
	table.RawSetInteger(1, runtime.StringValue("first"))
	tableValue := runtime.ReferenceValue(runtime.KindTable, table)

	results, err := IPairs(tableValue)
	if err != nil {
		// table 参数必须可生成 ipairs 三元组。
		t.Fatalf("ipairs failed: %v", err)
	}
	if len(results) != 3 || results[0].Kind != runtime.KindGoClosure || !results[1].RawEqual(tableValue) || !results[2].RawEqual(runtime.IntegerValue(0)) {
		// ipairs 三元组布局必须稳定。
		t.Fatalf("ipairs triple mismatch: %#v", results)
	}
}

// TestAssertSemantics 验证 Lua assert 的成功透传与失败错误对象。
//
// 成功时返回全部参数；失败时抛出 Lua error，并保留第二个参数作为错误对象。
func TestAssertSemantics(t *testing.T) {
	// truthy 参数应按原顺序透传。
	results, err := Assert(runtime.BooleanValue(true), runtime.StringValue("ok"))
	if err != nil {
		// assert(true, ...) 不应返回错误。
		t.Fatalf("assert success failed: %v", err)
	}
	if len(results) != 2 || !results[1].RawEqual(runtime.StringValue("ok")) {
		// 成功路径必须返回所有传入参数。
		t.Fatalf("assert success results mismatch: %#v", results)
	}

	_, err = Assert(runtime.BooleanValue(false), runtime.StringValue("bad"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// assert(false, msg) 必须转为 Lua error。
		t.Fatalf("assert error classification mismatch: %v", err)
	}
	if !runtime.ErrorObject(err).RawEqual(runtime.StringValue("bad")) {
		// 第二个参数必须成为错误对象。
		t.Fatalf("assert error object mismatch: %#v", runtime.ErrorObject(err))
	}
}

// TestCollectGarbageCommands 验证 collectgarbage 第一阶段命令集合。
//
// 当前实现不模拟 Lua 增量 GC，只保证命令解析、root 计数、控制状态和错误边界稳定。
func TestCollectGarbageCommands(t *testing.T) {
	// 构造带一个全局值的 State，确保 count 至少能观察到 root。
	state := runtime.NewState()
	state.SetGlobal("answer", runtime.IntegerValue(42))

	countResults, err := CollectGarbage(state, runtime.StringValue("count"))
	if err != nil {
		// count 命令必须成功。
		t.Fatalf("collectgarbage count failed: %v", err)
	}
	if len(countResults) != 1 || countResults[0].Kind != runtime.KindInteger || countResults[0].Integer <= 0 {
		// count 返回当前 root 样本数量，必须是正整数。
		t.Fatalf("collectgarbage count mismatch: %#v", countResults)
	}

	runningResults, err := CollectGarbage(state, runtime.StringValue("isrunning"))
	if err != nil {
		// isrunning 命令必须成功。
		t.Fatalf("collectgarbage isrunning failed: %v", err)
	}
	if len(runningResults) != 1 || !runningResults[0].RawEqual(runtime.BooleanValue(true)) {
		// 当前阶段 GC 固定处于运行状态。
		t.Fatalf("collectgarbage isrunning mismatch: %#v", runningResults)
	}

	stopResults, err := CollectGarbage(state, runtime.StringValue("stop"))
	if err != nil {
		// stop 命令必须成功。
		t.Fatalf("collectgarbage stop failed: %v", err)
	}
	if len(stopResults) != 0 {
		// stop 按 Lua 5.3 不返回旧状态。
		t.Fatalf("collectgarbage stop should return no values: %#v", stopResults)
	}
	runningResults, err = CollectGarbage(state, runtime.StringValue("isrunning"))
	if err != nil {
		// stop 后继续查询 isrunning 必须成功。
		t.Fatalf("collectgarbage isrunning after stop failed: %v", err)
	}
	if len(runningResults) != 1 || !runningResults[0].RawEqual(runtime.BooleanValue(false)) {
		// stop 后 Lua 视角自动 GC 必须显示为未运行。
		t.Fatalf("collectgarbage stopped isrunning mismatch: %#v", runningResults)
	}

	restartResults, err := CollectGarbage(state, runtime.StringValue("restart"))
	if err != nil {
		// restart 命令必须成功。
		t.Fatalf("collectgarbage restart failed: %v", err)
	}
	if len(restartResults) != 0 {
		// restart 按 Lua 5.3 不返回旧状态。
		t.Fatalf("collectgarbage restart should return no values: %#v", restartResults)
	}
	runningResults, err = CollectGarbage(state, runtime.StringValue("isrunning"))
	if err != nil {
		// restart 后继续查询 isrunning 必须成功。
		t.Fatalf("collectgarbage isrunning after restart failed: %v", err)
	}
	if len(runningResults) != 1 || !runningResults[0].RawEqual(runtime.BooleanValue(true)) {
		// restart 后 Lua 视角自动 GC 必须显示为运行中。
		t.Fatalf("collectgarbage restarted isrunning mismatch: %#v", runningResults)
	}

	stepResults, err := CollectGarbage(state, runtime.StringValue("step"), runtime.IntegerValue(20000))
	if err != nil {
		// step 命令必须接受工作量参数。
		t.Fatalf("collectgarbage step failed: %v", err)
	}
	if len(stepResults) != 1 || !stepResults[0].RawEqual(runtime.BooleanValue(true)) {
		// 当前占位增量步骤必须返回布尔完成状态。
		t.Fatalf("collectgarbage step mismatch: %#v", stepResults)
	}

	pauseResults, err := CollectGarbage(state, runtime.StringValue("setpause"), runtime.IntegerValue(180))
	if err != nil {
		// setpause 命令必须成功。
		t.Fatalf("collectgarbage setpause failed: %v", err)
	}
	if len(pauseResults) != 1 || !pauseResults[0].RawEqual(runtime.IntegerValue(200)) {
		// 默认 pause 为 Lua 常见 200，setpause 返回旧值。
		t.Fatalf("collectgarbage setpause old value mismatch: %#v", pauseResults)
	}
	pauseResults, err = CollectGarbage(state, runtime.StringValue("setpause"), runtime.IntegerValue(220))
	if err != nil {
		// 第二次 setpause 命令必须成功。
		t.Fatalf("collectgarbage setpause second failed: %v", err)
	}
	if len(pauseResults) != 1 || !pauseResults[0].RawEqual(runtime.IntegerValue(180)) {
		// 第二次 setpause 应返回上次写入的新值。
		t.Fatalf("collectgarbage setpause previous value mismatch: %#v", pauseResults)
	}

	stepMulResults, err := CollectGarbage(state, runtime.StringValue("setstepmul"), runtime.IntegerValue(190))
	if err != nil {
		// setstepmul 命令必须成功。
		t.Fatalf("collectgarbage setstepmul failed: %v", err)
	}
	if len(stepMulResults) != 1 || !stepMulResults[0].RawEqual(runtime.IntegerValue(200)) {
		// 默认 step multiplier 为 Lua 常见 200，setstepmul 返回旧值。
		t.Fatalf("collectgarbage setstepmul old value mismatch: %#v", stepMulResults)
	}

	_, err = CollectGarbage(state, runtime.StringValue("unknown"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 未知命令必须返回 Lua error。
		t.Fatalf("collectgarbage invalid option mismatch: %v", err)
	}

	_, err = CollectGarbage(state, runtime.StringValue("step"), runtime.StringValue("bad"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// step 的非数值参数必须返回 Lua error。
		t.Fatalf("collectgarbage step argument error mismatch: %v", err)
	}
}

// TestDofileReadBoundary 验证 dofile 文件读取边界。
//
// 当前 dofile 已完成参数校验和文件读取；执行链路未接入时返回明确的阶段性错误。
func TestDofileReadBoundary(t *testing.T) {
	// 创建临时 Lua 文件，避免依赖仓库外部路径。
	path := filepath.Join(t.TempDir(), "chunk.lua")
	if err := os.WriteFile(path, []byte("return 1\n"), 0o600); err != nil {
		// 测试文件必须可写入。
		t.Fatalf("write temp chunk failed: %v", err)
	}

	_, err := Dofile(runtime.StringValue(path))
	if !errors.Is(err, ErrDofileExecutionUnsupported) {
		// 文件读取成功后必须返回当前阶段的执行未接入错误。
		t.Fatalf("dofile unsupported error mismatch: %v", err)
	}

	_, err = Dofile(runtime.IntegerValue(1))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 非 string 文件名必须按 Lua 参数错误返回。
		t.Fatalf("dofile argument error mismatch: %v", err)
	}
}

// TestOpenDofileExecutesFileChunk 验证 Open 注册的全局 dofile 会执行文件 chunk。
//
// 全局 dofile 必须捕获当前 State，复用 loadfile 绑定的 `_ENV`，并把文件 chunk 的多返回值返回给
// 调用方；加载失败才抛 Lua error，而不是保留阶段性未接入错误。
func TestOpenDofileExecutesFileChunk(t *testing.T) {
	state := runtime.NewStateWithOptions(runtime.Options{AllowHostFilesystem: true})
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 dofile。
		t.Fatalf("open base failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "dofile_exec.lua")
	if err := os.WriteFile(path, []byte("return _VERSION, 7\n"), 0o600); err != nil {
		// 测试文件必须可写。
		t.Fatalf("write dofile chunk failed: %v", err)
	}

	dofileValue := state.GetGlobal("dofile")
	dofileFunction, ok := dofileValue.Ref.(*runtime.GoClosureWithUpvalues)
	if dofileValue.Kind != runtime.KindGoClosure || !ok {
		// Open 注册的 dofile 必须是捕获 State 的可 yield Go closure。
		t.Fatalf("dofile global mismatch: %#v", dofileValue)
	}
	results, err := dofileFunction.Function(runtime.StringValue(path))
	if err != nil {
		// dofile 成功读取与执行文件时不应返回错误。
		t.Fatalf("dofile execute failed: %v", err)
	}
	if len(results) != 2 || !results[0].RawEqual(runtime.StringValue(VersionText)) || !results[1].RawEqual(runtime.IntegerValue(7)) {
		// 文件 chunk 的多返回值必须原样传回调用方。
		t.Fatalf("dofile results mismatch: %#v", results)
	}
}

// TestOpenDofileExecutesGMatchIterator 验证 dofile 内可调用带 upvalue 元数据的 Go closure。
//
// string.gmatch 返回 *runtime.GoClosureWithUpvalues；官方 strings.lua 会在 dofile 执行路径中把
// 该 iterator 作为 local 再调用，base 内部执行器必须识别这种 Go closure 负载。
func TestOpenDofileExecutesGMatchIterator(t *testing.T) {
	state := runtime.NewStateWithOptions(runtime.Options{AllowHostFilesystem: true})
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 dofile。
		t.Fatalf("open base failed: %v", err)
	}
	if err := stringlib.Open(state); err != nil {
		// gmatch 来自 string 标准库，测试前必须注册。
		t.Fatalf("open string failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "dofile_gmatch.lua")
	if err := os.WriteFile(path, []byte("local f = string.gmatch('1 2', '%d+')\nreturn f()\n"), 0o600); err != nil {
		// 测试文件必须可写。
		t.Fatalf("write dofile gmatch chunk failed: %v", err)
	}

	dofileValue := state.GetGlobal("dofile")
	dofileFunction, ok := dofileValue.Ref.(*runtime.GoClosureWithUpvalues)
	if dofileValue.Kind != runtime.KindGoClosure || !ok {
		// Open 注册的 dofile 必须是捕获 State 的可 yield Go closure。
		t.Fatalf("dofile global mismatch: %#v", dofileValue)
	}
	results, err := dofileFunction.Function(runtime.StringValue(path))
	if err != nil {
		// iterator local 调用必须在 dofile 的 base 执行器中成功。
		t.Fatalf("dofile gmatch failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(runtime.StringValue("1")) {
		// gmatch iterator 首次调用应返回第一段匹配文本。
		t.Fatalf("dofile gmatch results mismatch: %#v", results)
	}
}

// TestLoadCompilesStringChunk 验证 load 将字符串 chunk 编译成 Lua closure。
//
// 当前阶段只验证 closure 和 Proto.Source，不执行字节码。
func TestLoadCompilesStringChunk(t *testing.T) {
	results, err := Load(runtime.StringValue("local a = 1 return a"), runtime.StringValue("sample"))
	if err != nil {
		// load 的加载错误应通过返回值表达，不应返回 Go error。
		t.Fatalf("load returned go error: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindLuaClosure {
		// 成功 load 必须返回 Lua closure。
		t.Fatalf("load results mismatch: %#v", results)
	}
	closure, ok := results[0].Ref.(*runtime.LuaClosure)
	if !ok || closure.Proto == nil || closure.Proto.Source != "sample" {
		// closure 必须携带编译后的 Proto 和 source 名称。
		t.Fatalf("load closure mismatch: %#v", results[0].Ref)
	}

	defaultNameResults, err := Load(runtime.StringValue("function f () end"))
	if err != nil {
		// 默认 chunk name 路径也不应返回 Go error。
		t.Fatalf("load default name returned go error: %v", err)
	}
	defaultNameClosure, ok := defaultNameResults[0].Ref.(*runtime.LuaClosure)
	if !ok || defaultNameClosure.Proto == nil || defaultNameClosure.Proto.Source != "function f () end" {
		// Lua 5.3 的 load(string) 默认使用源码字符串作为 chunk name。
		t.Fatalf("load default source mismatch: %#v", defaultNameResults[0].Ref)
	}

	errorResults, err := Load(runtime.StringValue("return ("))
	if err != nil {
		// 编译失败同样不应返回 Go error。
		t.Fatalf("load invalid returned go error: %v", err)
	}
	if len(errorResults) != 2 || !errorResults[0].IsNil() || errorResults[1].Kind != runtime.KindString {
		// load 失败应返回 nil 和错误文本。
		t.Fatalf("load invalid results mismatch: %#v", errorResults)
	}
}

// TestLoadSyntaxErrorUsesLuaMessageShape 验证 load 失败文本使用 Lua 5.3 语法错误格式。
//
// 官方 errors.lua 会匹配 `[string "..."]:line: ... near token`，而 parser 内部仍保留
// `parse error at line:column` 给 Go 层诊断；load 需要在边界处转换文本。
func TestLoadSyntaxErrorUsesLuaMessageShape(t *testing.T) {
	// 多行 table constructor 缺少关闭括号时应报告 EOF token 和第 3 行。
	results, err := Load(runtime.StringValue("local a = {4\n\n"))
	if err != nil {
		// load 的语法错误通过返回值表达。
		t.Fatalf("load eof syntax returned go error: %v", err)
	}
	if len(results) != 2 || !results[0].IsNil() || results[1].Kind != runtime.KindString {
		// 失败结果必须是 nil/message。
		t.Fatalf("load eof syntax results mismatch: %#v", results)
	}
	message := results[1].String
	if !strings.Contains(message, `[string "local a = {4"]:3:`) || !strings.Contains(message, "near <eof>") {
		// 官方 errors.lua 的 checksyntax 依赖 source、行号和 near <eof>。
		t.Fatalf("load eof syntax message = %q", message)
	}

	// 非法语句起始符按 Lua 5.3 展示 unexpected symbol near token。
	results, err = Load(runtime.StringValue("syntax error"))
	if err != nil {
		// load 的语法错误通过返回值表达。
		t.Fatalf("load keyword syntax returned go error: %v", err)
	}
	message = results[1].String
	if !strings.Contains(message, `[string "syntax error"]:1:`) || !strings.Contains(message, `near 'error'`) {
		// parser 会把 `syntax` 当作待赋值名称，错误 token 是后续的 `error`。
		t.Fatalf("load keyword syntax message = %q", message)
	}

	// 长字符串作为非法 chunk 时，near 片段必须保留源码长字符串定界符。
	results, err = Load(runtime.StringValue("[[a]]"))
	if err != nil {
		// load 的语法错误通过返回值表达。
		t.Fatalf("load long string syntax returned go error: %v", err)
	}
	message = results[1].String
	if !strings.Contains(message, `near '[[a]]'`) {
		// 官方 errors.lua 对长字符串 token 使用源码字面量匹配。
		t.Fatalf("load long string syntax message = %q", message)
	}

	// 短字符串作为非法 chunk 时，near 片段必须保留源码引号。
	results, err = Load(runtime.StringValue("'aa'"))
	if err != nil {
		// load 的语法错误通过返回值表达。
		t.Fatalf("load short string syntax returned go error: %v", err)
	}
	message = results[1].String
	if !strings.Contains(message, `near ''aa''`) {
		// 官方 errors.lua 对短字符串 token 使用外层 near 引号包裹源码字面量。
		t.Fatalf("load short string syntax message = %q", message)
	}

	// 不可打印字符按官方 errors.lua 要求展示为十进制转义 token。
	for _, testCase := range []struct {
		name string
		src  string
		near string
	}{
		{name: "control char inside name", src: "a\001a = 1", near: `near '<\1>'`},
		{name: "invalid first byte", src: string([]byte{255}) + "a = 1", near: `near '<\255>'`},
	} {
		results, err = Load(runtime.StringValue(testCase.src))
		if err != nil {
			// load 的非法 token 错误仍应通过返回值表达。
			t.Fatalf("%s: load illegal token returned go error: %v", testCase.name, err)
		}
		if len(results) != 2 || !results[0].IsNil() || results[1].Kind != runtime.KindString {
			// 失败结果必须保持 nil/message。
			t.Fatalf("%s: load illegal token results mismatch: %#v", testCase.name, results)
		}
		if message = results[1].String; !strings.Contains(message, testCase.near) {
			// Lua 官方 token 测试使用 near 后的十进制转义片段匹配。
			t.Fatalf("%s: load illegal token message = %q, want %q", testCase.name, message, testCase.near)
		}
	}

	// 长 source 名称摘要不能超过 Lua 5.3 LUA_IDSIZE-1。
	longSource := strings.Repeat("x", 80)
	results, err = Load(runtime.StringValue("x"), runtime.StringValue(longSource))
	if err != nil {
		// load 的语法错误通过返回值表达。
		t.Fatalf("load long source returned go error: %v", err)
	}
	sourcePrefix := strings.SplitN(results[1].String, ":", 2)[0]
	if len(sourcePrefix) > 59 {
		// 官方 errors.lua 对 source info 长度有上限断言。
		t.Fatalf("source prefix too long: len=%d text=%q", len(sourcePrefix), sourcePrefix)
	}
}

// TestLoadSyntaxLimitReportsTooManyCLevels 验证 parser 过深语法层级使用 Lua 5.3 错误文本。
//
// 官方 errors.lua 的 syntax limits 小节用 190 层作为可接受边界、201 层作为失败边界；
// load 需要返回 nil/message，且 message 包含 `too many C levels`。
func TestLoadSyntaxLimitReportsTooManyCLevels(t *testing.T) {
	const maxCLevel = 200
	for _, testCase := range []struct {
		name  string
		init  string
		rep   string
		close string
		repc  string
	}{
		{name: "assignment list", init: "local a; a", rep: ",a", close: "= 1", repc: ",1"},
		{name: "table constructor", init: "local a; a=", rep: "{", close: "0", repc: "}"},
		{name: "parentheses", init: "local a; a=", rep: "(", close: "2", repc: ")"},
		{name: "call arguments", init: "local a; ", rep: "a(", close: "2", repc: ")"},
		{name: "do blocks", init: "", rep: "do ", close: "", repc: " end"},
		{name: "while blocks", init: "", rep: "while a do ", close: "", repc: " end"},
		{name: "if blocks", init: "local a; ", rep: "if a then else ", close: "", repc: " end"},
		{name: "function blocks", init: "", rep: "function foo () ", close: "", repc: " end"},
		{name: "concat expression", init: "local a; a=", rep: "a..", close: "a", repc: ""},
		{name: "pow expression", init: "local a; a=", rep: "a^", close: "a", repc: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			acceptedSource := testCase.init + strings.Repeat(testCase.rep, maxCLevel-10) + testCase.close + strings.Repeat(testCase.repc, maxCLevel-10)
			results, err := Load(runtime.StringValue(acceptedSource))
			if err != nil {
				// load 的 parser 结果必须通过返回值表达，不应上抛 Go error。
				t.Fatalf("accepted syntax returned go error: %v", err)
			}
			if len(results) == 0 || results[0].IsNil() {
				// 190 层仍应被 parser 接受，避免上限过低。
				t.Fatalf("accepted syntax failed: %#v", results)
			}

			rejectedSource := testCase.init + strings.Repeat(testCase.rep, maxCLevel+1)
			results, err = Load(runtime.StringValue(rejectedSource))
			if err != nil {
				// 过深语法也必须按 load 语义返回 nil/message。
				t.Fatalf("rejected syntax returned go error: %v", err)
			}
			if len(results) != 2 || !results[0].IsNil() || results[1].Kind != runtime.KindString {
				// 失败结果必须是 nil/message。
				t.Fatalf("rejected syntax results mismatch: %#v", results)
			}
			if !strings.Contains(results[1].String, "too many C levels") {
				// 官方 errors.lua 的 checkmessage 只要求包含该片段。
				t.Fatalf("rejected syntax message = %q", results[1].String)
			}
		})
	}
}

// TestLoadTooManyRegistersReportsCompileLimit 验证超长调用实参列表返回 Lua 5.3 寄存器错误。
//
// 官方 errors.lua 在 syntax limits 后继续检查 `f(x, ... x)` 超过寄存器预算时包含
// `too many registers`，该错误必须来自编译期而不是运行时寄存器回绕。
func TestLoadTooManyRegistersReportsCompileLimit(t *testing.T) {
	source := "a = f(x" + strings.Repeat(",x", 260) + ")"
	results, err := Load(runtime.StringValue(source))
	if err != nil {
		// load 的编译限制错误通过返回值表达。
		t.Fatalf("load register limit returned go error: %v", err)
	}
	if len(results) != 2 || !results[0].IsNil() || results[1].Kind != runtime.KindString {
		// 失败结果必须是 nil/message。
		t.Fatalf("load register limit results mismatch: %#v", results)
	}
	if !strings.Contains(results[1].String, "too many registers") {
		// 官方 errors.lua 的 checkmessage 只要求包含该片段。
		t.Fatalf("load register limit message = %q", results[1].String)
	}
}

// TestLoadFunctionVariableLimitsUseLuaMessages 验证局部变量和 upvalue 数量上限错误文本。
//
// 官方 errors.lua 在 syntax limits 末尾分别匹配 `line 5` + `too many upvalues`，以及
// `line 2` + `too many local variables`。
func TestLoadFunctionVariableLimitsUseLuaMessages(t *testing.T) {
	upvalueSource := "local function fooA ()\n  local "
	for index := 1; index <= 127; index++ {
		// 构造第一组外层局部变量。
		upvalueSource += fmt.Sprintf("a%d, ", index)
	}
	upvalueSource += "b,c\nlocal function fooB ()\n  local "
	for index := 1; index <= 127; index++ {
		// 构造第二组外层局部变量。
		upvalueSource += fmt.Sprintf("b%d, ", index)
	}
	upvalueSource += "b\nfunction fooC () return b+c"
	for index := 1; index <= 127; index++ {
		// fooC 会捕获超过 255 个 upvalue。
		upvalueSource += fmt.Sprintf("+a%d+b%d", index, index)
	}
	upvalueSource += "\nend  end end"

	results, err := Load(runtime.StringValue(upvalueSource))
	if err != nil {
		// load 的 upvalue 限制错误通过返回值表达。
		t.Fatalf("load upvalue limit returned go error: %v", err)
	}
	if len(results) != 2 || !results[0].IsNil() || results[1].Kind != runtime.KindString {
		// 失败结果必须是 nil/message。
		t.Fatalf("load upvalue limit results mismatch: %#v", results)
	}
	if message := results[1].String; !strings.Contains(message, "line 5") || !strings.Contains(message, "too many upvalues") {
		// 官方 errors.lua 同时匹配行号和错误片段。
		t.Fatalf("load upvalue limit message = %q", message)
	}

	localSource := "\nfunction foo ()\n  local "
	for index := 1; index <= 300; index++ {
		// 构造超过 Lua 5.3 上限的局部变量声明。
		localSource += fmt.Sprintf("a%d, ", index)
	}
	localSource += "b\n"
	results, err = Load(runtime.StringValue(localSource))
	if err != nil {
		// load 的局部变量限制错误通过返回值表达。
		t.Fatalf("load local limit returned go error: %v", err)
	}
	if len(results) != 2 || !results[0].IsNil() || results[1].Kind != runtime.KindString {
		// 失败结果必须是 nil/message。
		t.Fatalf("load local limit results mismatch: %#v", results)
	}
	if message := results[1].String; !strings.Contains(message, "line 2") || !strings.Contains(message, "too many local variables") {
		// 官方 errors.lua 同时匹配行号和错误片段。
		t.Fatalf("load local limit message = %q", message)
	}
}

// TestLoadAcceptsBinaryChunkString 验证 load 可读取 string.dump 产生的 binary chunk。
//
// 官方测试会执行 `local b = string.dump(f); f = assert(load(b))`，因此 load 必须按签名识别
// Lua 5.3 预编译 chunk，并返回绑定当前全局环境的 Lua closure。
func TestLoadAcceptsBinaryChunkString(t *testing.T) {
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 load。
		t.Fatalf("open base failed: %v", err)
	}
	proto := bytecode.NewProto("@binary-load.lua")
	proto.MaxStackSize = 2
	proto.Upvalues = []bytecode.UpvalueDesc{{}}
	chunkBytes := bytecode.DumpBinaryChunk(proto)

	results, err := loadWithState(state, runtime.StringValue(string(chunkBytes)))
	if err != nil {
		// load 的加载错误应通过返回值表达，不应返回 Go error。
		t.Fatalf("load binary returned go error: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindLuaClosure {
		// 成功加载 binary chunk 必须返回 Lua closure。
		t.Fatalf("load binary results mismatch: %#v", results)
	}
	closure, ok := results[0].Ref.(*runtime.LuaClosure)
	if !ok || closure.Proto == nil || closure.Proto.Source != proto.Source {
		// binary chunk 的 Proto 元数据必须被 loader 保留下来。
		t.Fatalf("load binary closure mismatch: %#v", results[0].Ref)
	}
	if len(closure.Upvalues) != 1 || closure.Upvalues[0].Kind != runtime.KindTable || closure.Upvalues[0].Ref != state.Globals() {
		// binary chunk 缺少 upvalue 调试名时，第一个 upvalue 也必须按 _ENV 绑定。
		t.Fatalf("load binary _ENV mismatch: %#v", closure.Upvalues)
	}
}

// TestLoadTruncatedBinaryChunkReturnsTruncatedError 验证 load 对截断 binary chunk 返回 truncated。
//
// Lua 5.3 只要输入以 ESC 开头就进入预编译 chunk 读取路径；短读必须通过 nil,errorText 返回，
// 且错误文本包含 truncated，供官方 calls.lua 的逐前缀断言匹配。
func TestLoadTruncatedBinaryChunkReturnsTruncatedError(t *testing.T) {
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 load。
		t.Fatalf("open base failed: %v", err)
	}
	proto := bytecode.NewProto("@truncated-binary.lua")
	proto.MaxStackSize = 2
	proto.Code = []bytecode.Instruction{bytecode.CreateABC(bytecode.OpReturn, 0, 1, 0)}
	chunkBytes := bytecode.DumpBinaryChunk(proto)

	for chunkSize := 1; chunkSize < len(chunkBytes); chunkSize++ {
		results, err := loadWithState(state, runtime.StringValue(string(chunkBytes[:chunkSize])))
		if err != nil {
			// load 的 binary chunk 短读错误应通过 Lua 返回值表达。
			t.Fatalf("prefix size %d returned go error: %v", chunkSize, err)
		}
		if len(results) != 2 || !results[0].IsNil() || results[1].Kind != runtime.KindString {
			// 失败路径必须返回 nil 和错误文本，不能返回 Go error 或 closure。
			t.Fatalf("prefix size %d results mismatch: %#v", chunkSize, results)
		}
		if !strings.Contains(results[1].String, "truncated") {
			// 官方 calls.lua 使用 string.find(msg, "truncated") 验收该语义。
			t.Fatalf("prefix size %d error = %q, want truncated", chunkSize, results[1].String)
		}
	}
}

// TestLoadFileCompilesFileChunk 验证 loadfile 读取文件并编译成 Lua closure。
//
// 文件读取失败或编译失败通过 nil,errorText 返回，成功路径返回 closure。
func TestLoadFileCompilesFileChunk(t *testing.T) {
	// 写入可编译的临时 Lua chunk。
	path := filepath.Join(t.TempDir(), "loadfile.lua")
	if err := os.WriteFile(path, []byte("local a = 1 return a\n"), 0o600); err != nil {
		// 测试文件必须可写。
		t.Fatalf("write loadfile chunk failed: %v", err)
	}

	results, err := LoadFile(runtime.StringValue(path))
	if err != nil {
		// loadfile 的加载错误通过返回值表达，不应返回 Go error。
		t.Fatalf("loadfile returned go error: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindLuaClosure {
		// 成功 loadfile 必须返回 Lua closure。
		t.Fatalf("loadfile results mismatch: %#v", results)
	}

	missingResults, err := LoadFile(runtime.StringValue(filepath.Join(t.TempDir(), "missing.lua")))
	if err != nil {
		// 文件缺失应通过返回值表达。
		t.Fatalf("loadfile missing returned go error: %v", err)
	}
	if len(missingResults) != 2 || !missingResults[0].IsNil() || missingResults[1].Kind != runtime.KindString {
		// 文件缺失返回 nil 和错误文本。
		t.Fatalf("loadfile missing results mismatch: %#v", missingResults)
	}
}

// TestLoadFileAcceptsBinaryChunk 验证 loadfile 可读取 Lua 5.3 binary chunk 文件。
//
// loadfile 与 load 共享 chunk 识别逻辑；文件内容以 binary 签名开头时不应走源码 parser。
func TestLoadFileAcceptsBinaryChunk(t *testing.T) {
	state := runtime.NewStateWithOptions(runtime.Options{AllowHostFilesystem: true})
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 loadfile。
		t.Fatalf("open base failed: %v", err)
	}
	proto := bytecode.NewProto("@binary-loadfile.lua")
	proto.MaxStackSize = 2
	proto.Upvalues = []bytecode.UpvalueDesc{{Name: "_ENV"}}
	path := filepath.Join(t.TempDir(), "chunk.luac")
	if err := os.WriteFile(path, bytecode.DumpBinaryChunk(proto), 0o600); err != nil {
		// 测试 binary chunk 文件必须可写。
		t.Fatalf("write binary chunk failed: %v", err)
	}

	results, err := loadFileWithState(state, runtime.StringValue(path))
	if err != nil {
		// loadfile 的加载错误应通过返回值表达，不应返回 Go error。
		t.Fatalf("loadfile binary returned go error: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindLuaClosure {
		// 成功加载 binary chunk 文件必须返回 Lua closure。
		t.Fatalf("loadfile binary results mismatch: %#v", results)
	}
	closure, ok := results[0].Ref.(*runtime.LuaClosure)
	if !ok || closure.Proto == nil || closure.Proto.Source != proto.Source {
		// 文件路径不应覆盖 binary chunk 内保存的 Source。
		t.Fatalf("loadfile binary closure mismatch: %#v", results[0].Ref)
	}
}

// TestLoadFileAcceptsBinaryChunkAfterInitialComment 验证文件首行注释后可加载 binary chunk。
//
// Lua 5.3 files.lua 会把 `#comment\0\n` 与 string.dump 结果拼成文件；loadfile 必须跳过该
// 文件头注释，再从 ESC 签名开始读取 binary chunk。
func TestLoadFileAcceptsBinaryChunkAfterInitialComment(t *testing.T) {
	state := runtime.NewStateWithOptions(runtime.Options{AllowHostFilesystem: true})
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 loadfile。
		t.Fatalf("open base failed: %v", err)
	}
	proto := bytecode.NewProto("@commented-binary-loadfile.lua")
	proto.MaxStackSize = 2
	proto.Upvalues = []bytecode.UpvalueDesc{{Name: "_ENV"}}
	path := filepath.Join(t.TempDir(), "commented.luac")
	fileBytes := append([]byte("#this is a comment for a binary file\x00\n"), bytecode.DumpBinaryChunk(proto)...)
	if err := os.WriteFile(path, fileBytes, 0o600); err != nil {
		// 测试带首行注释的 binary chunk 文件必须可写。
		t.Fatalf("write commented binary chunk failed: %v", err)
	}

	results, err := loadFileWithState(state, runtime.StringValue(path))
	if err != nil {
		// loadfile 的加载错误应通过返回值表达，不应返回 Go error。
		t.Fatalf("loadfile commented binary returned go error: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindLuaClosure {
		// 成功加载带注释 binary chunk 文件必须返回 Lua closure。
		t.Fatalf("loadfile commented binary results mismatch: %#v", results)
	}
	closure, ok := results[0].Ref.(*runtime.LuaClosure)
	if !ok || closure.Proto == nil || closure.Proto.Source != proto.Source {
		// 文件头注释不应覆盖 binary chunk 内保存的 Source。
		t.Fatalf("loadfile commented binary closure mismatch: %#v", results[0].Ref)
	}
}

// TestLoadFileModeAndEnvironment 验证 loadfile 的 mode 校验与 env 绑定。
//
// 第二参数 mode 控制 text/binary 接受范围；第三参数 env 必须原样绑定到顶层 `_ENV` upvalue。
func TestLoadFileModeAndEnvironment(t *testing.T) {
	state := runtime.NewStateWithOptions(runtime.Options{AllowHostFilesystem: true})
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 loadfile。
		t.Fatalf("open base failed: %v", err)
	}
	dir := t.TempDir()
	textPath := filepath.Join(dir, "chunk.lua")
	if err := os.WriteFile(textPath, []byte("return _ENV"), 0o600); err != nil {
		// 文本 chunk 必须可写入临时文件。
		t.Fatalf("write text chunk failed: %v", err)
	}
	textRejected, err := loadFileWithState(state, runtime.StringValue(textPath), runtime.StringValue("b"))
	if err != nil {
		// mode 校验错误应通过返回值表达，不应返回 Go error。
		t.Fatalf("loadfile text mode returned go error: %v", err)
	}
	if len(textRejected) != 2 || !textRejected[0].IsNil() || textRejected[1].Kind != runtime.KindString || !strings.Contains(textRejected[1].String, "text chunk") {
		// binary-only 模式必须拒绝文本 chunk。
		t.Fatalf("loadfile text mode rejection mismatch: %#v", textRejected)
	}

	envTable := runtime.NewTable()
	loaded, err := loadFileWithState(state, runtime.StringValue(textPath), runtime.StringValue("t"), runtime.ReferenceValue(runtime.KindTable, envTable))
	if err != nil {
		// 成功加载文本 chunk 不应返回 Go error。
		t.Fatalf("loadfile env returned go error: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Kind != runtime.KindLuaClosure {
		// loadfile 成功必须返回 Lua closure。
		t.Fatalf("loadfile env results mismatch: %#v", loaded)
	}
	closure, ok := loaded[0].Ref.(*runtime.LuaClosure)
	if !ok || len(closure.Upvalues) == 0 || closure.Upvalues[0].Kind != runtime.KindTable || closure.Upvalues[0].Ref != envTable {
		// 显式 env 必须绑定为顶层 `_ENV`。
		t.Fatalf("loadfile env upvalue mismatch: %#v", loaded[0].Ref)
	}

	binaryPath := filepath.Join(dir, "chunk.luac")
	proto := bytecode.NewProto("@mode-binary.lua")
	proto.MaxStackSize = 2
	proto.Upvalues = []bytecode.UpvalueDesc{{Name: "_ENV"}}
	if err := os.WriteFile(binaryPath, bytecode.DumpBinaryChunk(proto), 0o600); err != nil {
		// binary chunk 必须可写入临时文件。
		t.Fatalf("write binary chunk failed: %v", err)
	}
	binaryRejected, err := loadFileWithState(state, runtime.StringValue(binaryPath), runtime.StringValue("t"))
	if err != nil {
		// mode 校验错误应通过返回值表达，不应返回 Go error。
		t.Fatalf("loadfile binary mode returned go error: %v", err)
	}
	if len(binaryRejected) != 2 || !binaryRejected[0].IsNil() || binaryRejected[1].Kind != runtime.KindString || !strings.Contains(binaryRejected[1].String, "binary chunk") {
		// text-only 模式必须拒绝 binary chunk。
		t.Fatalf("loadfile binary mode rejection mismatch: %#v", binaryRejected)
	}
}

// TestOpenLoadFileBindsGlobalEnvironment 验证全局 loadfile 会绑定当前 State 的 _ENV。
//
// 官方测试通过 `assert(loadfile(n))()` 执行文件；返回 closure 必须携带 globals upvalue，
// 否则文件内访问 print、os、io、package 等全局库会在运行期失败。
func TestOpenLoadFileBindsGlobalEnvironment(t *testing.T) {
	state := runtime.NewStateWithOptions(runtime.Options{AllowHostFilesystem: true})
	if err := Open(state); err != nil {
		// base.Open 必须能注册捕获 State 的 loadfile。
		t.Fatalf("open base failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "loadfile_env.lua")
	if err := os.WriteFile(path, []byte("return _VERSION\n"), 0o600); err != nil {
		// 测试文件必须可写。
		t.Fatalf("write loadfile env chunk failed: %v", err)
	}

	loadfileValue := state.GetGlobal("loadfile")
	loadfileFunction, ok := loadfileValue.Ref.(runtime.GoResultsFunction)
	if loadfileValue.Kind != runtime.KindGoClosure || !ok {
		// Open 注册的 loadfile 必须是 GoResultsFunction。
		t.Fatalf("loadfile global mismatch: %#v", loadfileValue)
	}
	results, err := loadfileFunction(runtime.StringValue(path))
	if err != nil {
		// loadfile 加载错误应通过返回值表达，不应返回 Go error。
		t.Fatalf("loadfile global returned go error: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindLuaClosure {
		// 成功路径必须返回 Lua closure。
		t.Fatalf("loadfile global results mismatch: %#v", results)
	}
	closure, ok := results[0].Ref.(*runtime.LuaClosure)
	if !ok || len(closure.Upvalues) == 0 {
		// closure 必须包含顶层 _ENV upvalue。
		t.Fatalf("loadfile closure upvalues missing: %#v", results[0].Ref)
	}
	if closure.Upvalues[0].Kind != runtime.KindTable || closure.Upvalues[0].Ref != state.Globals() {
		// _ENV 必须绑定到当前 State globals。
		t.Fatalf("loadfile _ENV upvalue mismatch: %#v", closure.Upvalues)
	}
}
