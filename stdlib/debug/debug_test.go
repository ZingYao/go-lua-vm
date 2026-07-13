package debuglib

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestOpenRegistersDebugLibrary 验证 Open 注册 debug 标准库表。
//
// 该用例覆盖当前已实现 debug 函数的注册形态。
func TestOpenRegistersDebugLibrary(t *testing.T) {
	// 测试先创建独立 State，避免污染其他标准库测试。
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// Open 不应在有效 State 上失败。
		t.Fatalf("Open failed: %v", err)
	}

	debugValue := state.GetGlobal("debug")
	if debugValue.Kind != runtime.KindTable {
		// debug 全局必须是 table。
		t.Fatalf("debug kind = %v, want table", debugValue.Kind)
	}
	debugTable := debugValue.Ref.(*runtime.Table)
	for _, functionName := range []string{"debug", "gethook", "getinfo", "getlocal", "getmetatable", "getregistry", "getupvalue", "getuservalue", "sethook", "setlocal", "setmetatable", "setupvalue", "setuservalue", "traceback", "upvalueid", "upvaluejoin"} {
		// 每个已实现函数都必须注册为 Go closure。
		if got := debugTable.RawGetString(functionName); got.Kind != runtime.KindGoClosure {
			t.Fatalf("debug.%s kind = %v, want Go closure", functionName, got.Kind)
		}
	}
}

// TestDebugDebugPromptExecutesStderrWrite 验证 debug.debug 的最小交互调试器。
//
// 调试器必须输出官方提示符，执行 `io.stderr:write(...)`，并在读取 `cont` 后恢复外层程序。
func TestDebugDebugPromptExecutesStderrWrite(t *testing.T) {
	// 构造独立 debug 环境并注入测试输入输出，避免阻塞真实终端。
	environment := NewEnvironment(runtime.NewState())
	environment.debugInput = strings.NewReader("io.stderr:write(1000)\ncont\n")
	var output bytes.Buffer
	environment.debugOutput = &output
	values, err := environment.Debug()
	if err != nil {
		// 支持范围内的调试命令不应失败。
		t.Fatalf("Debug failed: %v", err)
	}
	if len(values) != 0 {
		// debug.debug 恢复执行时不返回 Lua 值。
		t.Fatalf("Debug values = %#v, want none", values)
	}
	if output.String() != "lua_debug> 1000lua_debug> " {
		// 提示符和 io.stderr:write 输出必须匹配官方 main.lua 断言。
		t.Fatalf("debug output = %q", output.String())
	}
}

// TestGetHookDefault 验证 debug.gethook 在未设置 hook 时的默认三元组。
//
// Lua 新 State 默认没有 hook，返回 nil、空 mask 和 0 count。
func TestGetHookDefault(t *testing.T) {
	// 构造独立环境并读取默认 hook。
	environment := NewEnvironment(runtime.NewState())
	values, err := environment.GetHook()
	if err != nil {
		// 默认读取不应失败。
		t.Fatalf("GetHook failed: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[1].String != "" || values[2].Integer != 0 {
		// gethook 默认值必须稳定。
		t.Fatalf("GetHook result = %#v", values)
	}
}

// TestGetInfoReadsCallFrameSnapshot 验证 debug.getinfo 读取调用帧快照。
//
// 当前阶段没有源码行号和局部变量信息，但必须返回基础帧类型、函数和占位字段。
func TestGetInfoReadsCallFrameSnapshot(t *testing.T) {
	// 构造包含一个 Go 调用帧的 State。
	state := runtime.NewState()
	function := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 该函数不需要被调用，只作为帧内函数值。
		return nil, nil
	}))
	if err := state.PushCallFrame(runtime.NewGoCallFrame(function, 1, 0)); err != nil {
		// 测试前置帧必须压入成功。
		t.Fatalf("PushCallFrame failed: %v", err)
	}

	environment := NewEnvironment(state)
	values, err := environment.GetInfo(runtime.IntegerValue(1))
	if err != nil {
		// 有效层级读取不应失败。
		t.Fatalf("GetInfo failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindTable {
		// getinfo 命中帧时必须返回 table。
		t.Fatalf("GetInfo result = %#v", values)
	}
	info := values[0].Ref.(*runtime.Table)
	if info.RawGetString("what").String != "C" {
		// Go 调用帧对外必须按 Lua C frame 展示。
		t.Fatalf("getinfo.what = %#v", info.RawGetString("what"))
	}
	funcValue := info.RawGetString("func")
	if funcValue.Kind != runtime.KindGoClosure || funcValue.Ref == nil {
		// func 字段必须保留当前帧函数值类型。
		t.Fatalf("getinfo.func = %#v", info.RawGetString("func"))
	}
	if info.RawGetString("currentline").Integer != DefaultCurrentLine || !info.RawGetString("isvararg").Bool {
		// Go/C 帧没有源码行，但 debug.getinfo("u") 应按 Lua 5.3 报告为 vararg。
		t.Fatalf("getinfo placeholders mismatch")
	}

	values, err = environment.GetInfo(runtime.IntegerValue(2))
	if err != nil {
		// 越界层级读取不应失败。
		t.Fatalf("GetInfo out-of-range failed: %v", err)
	}
	if len(values) != 1 || !values[0].IsNil() {
		// 越界层级按 Lua 语义返回 nil。
		t.Fatalf("GetInfo out-of-range = %#v", values)
	}
}

// TestGetInfoReadsLuaProtoDebugMetadata 验证 debug.getinfo 读取 Lua Proto 调试信息。
//
// Lua 调用帧携带 Proto 和 tail call 标记时，getinfo 必须返回行号、参数、vararg 和尾调用信息。
func TestGetInfoReadsLuaProtoDebugMetadata(t *testing.T) {
	// 构造带源码行号和函数定义范围的 Lua closure。
	state := runtime.NewState()
	closure := &runtime.LuaClosure{
		Proto: &bytecode.Proto{
			Source:          "@chunk.lua",
			LineDefined:     5,
			LastLineDefined: 9,
			NumParams:       2,
			IsVararg:        true,
			LineInfo:        []int{10, 11},
			Upvalues:        []bytecode.UpvalueDesc{{Name: "env"}},
		},
		Upvalues: []runtime.Value{runtime.StringValue("up")},
	}
	frame := runtime.NewLuaCallFrame(runtime.ReferenceValue(runtime.KindLuaClosure, closure), 1, -1)
	frame.CurrentPC = 1
	frame.TailCall = true
	if err := state.PushCallFrame(frame); err != nil {
		// 测试前置帧必须压入成功。
		t.Fatalf("PushCallFrame failed: %v", err)
	}

	environment := NewEnvironment(state)
	values, err := environment.GetInfo(runtime.IntegerValue(1))
	if err != nil {
		// 有效 Lua 帧读取不应失败。
		t.Fatalf("GetInfo lua metadata failed: %v", err)
	}
	info := values[0].Ref.(*runtime.Table)
	if info.RawGetString("source").String != "@chunk.lua" || info.RawGetString("currentline").Integer != 11 {
		// source 和 currentline 必须来自 Proto 调试表。
		t.Fatalf("getinfo source/line mismatch: source=%#v line=%#v", info.RawGetString("source"), info.RawGetString("currentline"))
	}
	if info.RawGetString("linedefined").Integer != 5 || info.RawGetString("lastlinedefined").Integer != 9 {
		// 函数定义行范围必须来自 Proto。
		t.Fatalf("getinfo defined lines mismatch")
	}
	if info.RawGetString("nparams").Integer != 2 || !info.RawGetString("isvararg").Bool || !info.RawGetString("istailcall").Bool || info.RawGetString("nups").Integer != 1 {
		// 参数、vararg、tailcall 与 upvalue 数必须稳定返回。
		t.Fatalf("getinfo function metadata mismatch")
	}
}

// TestGetInfoReadsFunctionValueMetadata 验证 debug.getinfo 可直接读取函数值。
//
// Lua 5.3 允许第一个参数传函数；Go closure 应显示为 C，[C]，Lua closure 应读取 Proto 元数据。
func TestGetInfoReadsFunctionValueMetadata(t *testing.T) {
	environment := NewEnvironment(runtime.NewState())
	goFunction := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 测试函数不会被调用，仅作为 debug.getinfo 的对象。
		return nil, nil
	}))
	goValues, err := environment.GetInfo(goFunction)
	if err != nil {
		// Go closure 元信息读取不应失败。
		t.Fatalf("GetInfo go function failed: %v", err)
	}
	goInfo := goValues[0].Ref.(*runtime.Table)
	if goInfo.RawGetString("what").String != "C" || goInfo.RawGetString("short_src").String != "[C]" {
		// Go closure 应按 Lua C function 元信息返回。
		t.Fatalf("go function getinfo mismatch: what=%#v short=%#v", goInfo.RawGetString("what"), goInfo.RawGetString("short_src"))
	}
	if !goInfo.RawGetString("isvararg").Bool || goInfo.RawGetString("nparams").Integer != 0 || goInfo.RawGetString("nups").Integer != 0 {
		// Go closure 对齐 Lua C function，debug.getinfo("u") 应报告 vararg、0 参数、0 upvalue。
		t.Fatalf("go function getinfo u mismatch: isvararg=%#v nparams=%#v nups=%#v", goInfo.RawGetString("isvararg"), goInfo.RawGetString("nparams"), goInfo.RawGetString("nups"))
	}

	luaClosure := runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{
		Proto: &bytecode.Proto{
			Source:          `function f () end`,
			LineDefined:     19,
			LastLineDefined: 29,
			NumParams:       3,
			LineInfo:        []int{20, 21, 29},
		},
	})
	luaValues, err := environment.GetInfo(luaClosure, runtime.StringValue("SfL"))
	if err != nil {
		// Lua closure 元信息读取不应失败。
		t.Fatalf("GetInfo lua function failed: %v", err)
	}
	luaInfo := luaValues[0].Ref.(*runtime.Table)
	if luaInfo.RawGetString("what").String != "Lua" || luaInfo.RawGetString("linedefined").Integer != 19 || luaInfo.RawGetString("nparams").Integer != 3 {
		// Lua closure 必须读取 Proto 的函数元信息。
		t.Fatalf("lua function getinfo mismatch")
	}
	if luaInfo.RawGetString("short_src").String != `[string "function f () end"]` {
		// 字符串 chunk 的 short_src 必须按 [string "..."] 展示。
		t.Fatalf("lua function short_src mismatch: %#v", luaInfo.RawGetString("short_src"))
	}
	activeLines := luaInfo.RawGetString("activelines").Ref.(*runtime.Table)
	if !activeLines.RawGetInteger(20).Bool || !activeLines.RawGetInteger(29).Bool || !activeLines.RawGetInteger(19).IsNil() {
		// activelines 只包含 LineInfo 中真实出现的行。
		t.Fatalf("lua function activelines mismatch")
	}

	newlineClosure := runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{Proto: &bytecode.Proto{Source: "\nfunction f () end"}})
	newlineValues, err := environment.GetInfo(newlineClosure)
	if err != nil {
		// 首行为空的源码名也必须能生成 debug 信息。
		t.Fatalf("GetInfo newline source failed: %v", err)
	}
	newlineInfo := newlineValues[0].Ref.(*runtime.Table)
	if newlineInfo.RawGetString("short_src").String != `[string "..."]` {
		// Lua 5.3 对首行为空的字符串 chunk 使用省略号展示。
		t.Fatalf("newline source short_src mismatch: %#v", newlineInfo.RawGetString("short_src"))
	}

	emptyNameClosure := runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{Proto: &bytecode.Proto{Source: ""}})
	emptyNameValues, err := environment.GetInfo(emptyNameClosure)
	if err != nil {
		// 显式空 chunk name 也必须能生成 debug 信息。
		t.Fatalf("GetInfo empty source failed: %v", err)
	}
	emptyNameInfo := emptyNameValues[0].Ref.(*runtime.Table)
	if emptyNameInfo.RawGetString("source").String != "" || emptyNameInfo.RawGetString("short_src").String != `[string ""]` {
		// 空 source 是有效 chunk name，不能回退到函数值 DebugString。
		t.Fatalf("empty source debug info mismatch: source=%#v short=%#v", emptyNameInfo.RawGetString("source"), emptyNameInfo.RawGetString("short_src"))
	}
}

// TestGetInfoRejectsInvalidOption 验证 debug.getinfo 会拒绝非法 what 选项。
func TestGetInfoRejectsInvalidOption(t *testing.T) {
	environment := NewEnvironment(runtime.NewState())
	function := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 测试函数不会被调用。
		return nil, nil
	}))

	_, err := environment.GetInfo(function, runtime.StringValue("X"))
	if err == nil {
		// 非法选项必须返回 Lua 参数错误。
		t.Fatalf("GetInfo invalid option should fail")
	}
}

// TestGetInfoReadsDebugNameMetadata 验证 debug.getinfo 读取调用点名称。
//
// Lua 5.3 的 `debug.getinfo(level, "n")` 需要返回 name 和 namewhat；调用帧已经携带
// 推断结果时，debug 标准库必须原样暴露。
func TestGetInfoReadsDebugNameMetadata(t *testing.T) {
	// 构造带 name/namewhat 的 Lua 调用帧。
	state := runtime.NewState()
	closure := &runtime.LuaClosure{Proto: &bytecode.Proto{}}
	frame := runtime.NewLuaCallFrame(runtime.ReferenceValue(runtime.KindLuaClosure, closure), 1, -1)
	frame.Name = "F"
	frame.NameWhat = "global"
	if err := state.PushCallFrame(frame); err != nil {
		// 测试前置帧必须压入成功。
		t.Fatalf("PushCallFrame failed: %v", err)
	}

	environment := NewEnvironment(state)
	values, err := environment.GetInfo(runtime.IntegerValue(1))
	if err != nil {
		// 有效 Lua 帧读取不应失败。
		t.Fatalf("GetInfo name metadata failed: %v", err)
	}
	info := values[0].Ref.(*runtime.Table)
	if info.RawGetString("name").String != "F" || info.RawGetString("namewhat").String != "global" {
		// name/namewhat 必须按调用帧元数据返回。
		t.Fatalf("getinfo name metadata mismatch: name=%#v namewhat=%#v", info.RawGetString("name"), info.RawGetString("namewhat"))
	}
}

// TestGetLocalReturnsNilUntilLocalMetadataExists 验证 debug.getlocal 的阶段性空结果。
//
// runtime 尚未保存局部变量可见范围，因此合法 level/index 返回 nil。
func TestGetLocalReturnsNilUntilLocalMetadataExists(t *testing.T) {
	// 构造独立环境并查询第一个局部变量。
	environment := NewEnvironment(runtime.NewState())
	values, err := environment.GetLocal(runtime.IntegerValue(1), runtime.IntegerValue(1))
	if err != nil {
		// 合法参数不应失败。
		t.Fatalf("GetLocal failed: %v", err)
	}
	if len(values) != 1 || !values[0].IsNil() {
		// 当前阶段必须返回 nil。
		t.Fatalf("GetLocal result = %#v", values)
	}
	if _, err := environment.GetLocal(runtime.StringValue("bad"), runtime.IntegerValue(1)); !errors.Is(err, runtime.ErrLuaError) {
		// 非整数 level 必须是 Lua 参数错误。
		t.Fatalf("GetLocal argument error = %v, want Lua error", err)
	}
}

// TestGetLocalReadsActiveLocalRange 验证 debug.getlocal 按局部变量可见范围读取。
//
// 当前帧的 Proto.LocalVars 决定可见局部变量顺序，局部变量值从 State 栈帧窗口读取。
func TestGetLocalReadsActiveLocalRange(t *testing.T) {
	// 构造 pc=2 时可见 a、b，不可见 gone 的 Lua 帧。
	state := runtime.NewState()
	for _, value := range []runtime.Value{runtime.StringValue("A"), runtime.StringValue("B")} {
		// 压入局部变量窗口值，Base=1 时依次对应 active local。
		if err := state.Push(value); err != nil {
			t.Fatalf("Push local failed: %v", err)
		}
	}
	closure := &runtime.LuaClosure{Proto: &bytecode.Proto{
		LocalVars: []bytecode.LocalVar{
			{Name: "gone", StartPC: 0, EndPC: 1},
			{Name: "a", StartPC: 0, EndPC: 4},
			{Name: "b", StartPC: 2, EndPC: 4},
		},
	}}
	frame := runtime.NewLuaCallFrame(runtime.ReferenceValue(runtime.KindLuaClosure, closure), 1, 0)
	frame.CurrentPC = 2
	if err := state.PushCallFrame(frame); err != nil {
		// 测试前置帧必须压入成功。
		t.Fatalf("PushCallFrame failed: %v", err)
	}

	environment := NewEnvironment(state)
	first, err := environment.GetLocal(runtime.IntegerValue(1), runtime.IntegerValue(1))
	if err != nil {
		// 读取第一个活跃局部变量不应失败。
		t.Fatalf("GetLocal first failed: %v", err)
	}
	second, err := environment.GetLocal(runtime.IntegerValue(1), runtime.IntegerValue(2))
	if err != nil {
		// 读取第二个活跃局部变量不应失败。
		t.Fatalf("GetLocal second failed: %v", err)
	}
	missing, err := environment.GetLocal(runtime.IntegerValue(1), runtime.IntegerValue(3))
	if err != nil {
		// 越界读取不应失败。
		t.Fatalf("GetLocal missing failed: %v", err)
	}
	if first[0].String != "a" || first[1].String != "A" || second[0].String != "b" || second[1].String != "B" {
		// 活跃局部变量必须按 LocalVars 中的可见顺序返回名称和值。
		t.Fatalf("GetLocal active locals first=%#v second=%#v", first, second)
	}
	if len(missing) != 1 || !missing[0].IsNil() {
		// 没有第三个活跃局部变量时返回 nil。
		t.Fatalf("GetLocal missing = %#v", missing)
	}
}

// TestGetLocalReadsVarargDebugValues 验证 debug.getlocal 负索引读取 vararg。
//
// Lua 5.3 使用负数局部变量索引读取 vararg，本项目通过 CallFrame.Varargs 暴露阶段性快照。
func TestGetLocalReadsVarargDebugValues(t *testing.T) {
	// 构造带两个 vararg 值的 Lua 调用帧。
	state := runtime.NewState()
	frame := runtime.NewLuaCallFrame(runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{}), 1, 0)
	frame.Varargs = &runtime.VarargSnapshot{Values: []runtime.Value{runtime.StringValue("v1"), runtime.StringValue("v2")}}
	if err := state.PushCallFrame(frame); err != nil {
		// 测试前置帧必须压入成功。
		t.Fatalf("PushCallFrame failed: %v", err)
	}

	environment := NewEnvironment(state)
	values, err := environment.GetLocal(runtime.IntegerValue(1), runtime.IntegerValue(-2))
	if err != nil {
		// 读取第二个 vararg 不应失败。
		t.Fatalf("GetLocal vararg failed: %v", err)
	}
	if len(values) != 2 || values[0].String != "(*vararg)" || values[1].String != "v2" {
		// 负索引 -2 必须返回第二个 vararg。
		t.Fatalf("GetLocal vararg = %#v", values)
	}
	missing, err := environment.GetLocal(runtime.IntegerValue(1), runtime.IntegerValue(-3))
	if err != nil {
		// 越界 vararg 读取不应失败。
		t.Fatalf("GetLocal missing vararg failed: %v", err)
	}
	if len(missing) != 1 || !missing[0].IsNil() {
		// 缺失 vararg 必须返回 nil。
		t.Fatalf("GetLocal missing vararg = %#v", missing)
	}
}

// TestGetLocalReadsLevelZeroTemporaries 验证 level 0 可读取 debug API 自身临时参数。
//
// Lua 5.3 官方 db.lua 使用 debug.getlocal(0, n) 检查 C 函数临时槽位；Go 实现需要把
// debug.getlocal 的实参按 `(*temporary)` 暴露出来。
func TestGetLocalReadsLevelZeroTemporaries(t *testing.T) {
	// 构造 debug 环境，不需要真实调用栈即可验证 level 0 语义。
	environment := NewEnvironment(runtime.NewState())
	first, err := environment.GetLocal(runtime.IntegerValue(0), runtime.IntegerValue(1))
	if err != nil {
		// level 0 第一临时槽位读取不应失败。
		t.Fatalf("GetLocal level0 first failed: %v", err)
	}
	second, err := environment.GetLocal(runtime.IntegerValue(0), runtime.IntegerValue(2))
	if err != nil {
		// level 0 第二临时槽位读取不应失败。
		t.Fatalf("GetLocal level0 second failed: %v", err)
	}
	missing, err := environment.GetLocal(runtime.IntegerValue(0), runtime.IntegerValue(3))
	if err != nil {
		// 超出临时槽位时应返回 nil 而不是 bad level。
		t.Fatalf("GetLocal level0 missing failed: %v", err)
	}
	zero, err := environment.GetLocal(runtime.IntegerValue(0), runtime.IntegerValue(0))
	if err != nil {
		// 0 索引不应命中，也不应报错。
		t.Fatalf("GetLocal level0 zero failed: %v", err)
	}
	if first[0].String != "(*temporary)" || first[1].Integer != 0 || second[0].String != "(*temporary)" || second[1].Integer != 2 {
		// 前两个临时槽位分别对应 debug.getlocal 的 level 与 index 实参。
		t.Fatalf("level0 temporaries first=%#v second=%#v", first, second)
	}
	if len(missing) != 1 || !missing[0].IsNil() || len(zero) != 1 || !zero[0].IsNil() {
		// 未命中临时槽位必须返回单个 nil。
		t.Fatalf("level0 missing=%#v zero=%#v", missing, zero)
	}
}

// TestGetLocalAcceptsIntegerFloatIndex 验证 debug local 参数接受整数值 float。
//
// 官方 db.lua 的 numeric for 可能把 local index 以 float number 传入；debug 库应按 Lua 5.3
// 规则接受有限整数值 float。
func TestGetLocalAcceptsIntegerFloatIndex(t *testing.T) {
	// 使用 level 0 temporary 语义验证 float index 会被转换为整数 2。
	environment := NewEnvironment(runtime.NewState())
	values, err := environment.GetLocal(runtime.NumberValue(0), runtime.NumberValue(2))
	if err != nil {
		// 可转整数的 float level/index 不应报参数错误。
		t.Fatalf("GetLocal float integer index failed: %v", err)
	}
	if len(values) != 2 || values[0].String != "(*temporary)" || values[1].Number != 2 {
		// 第二个 temporary 应返回原始 index 参数值 2.0。
		t.Fatalf("GetLocal float integer values = %#v", values)
	}
	if _, err := environment.GetLocal(runtime.IntegerValue(0), runtime.NumberValue(1.5)); !errors.Is(err, runtime.ErrLuaError) {
		// 非整数 float 仍必须报 Lua 参数错误。
		t.Fatalf("GetLocal fractional index error = %v, want Lua error", err)
	}
}

// TestGetLocalReadsFunctionParameterNames 验证 debug.getlocal 的函数形参名查询重载。
//
// Lua 5.3 允许 debug.getlocal(function, index) 读取固定形参名，也允许带 thread 的三参数形式；
// Go/C closure 没有 Lua 形参调试表，应返回 nil。
func TestGetLocalReadsFunctionParameterNames(t *testing.T) {
	// 构造携带两个固定形参和一个普通局部变量的 Lua closure。
	closure := &runtime.LuaClosure{Proto: &bytecode.Proto{
		NumParams: 2,
		LocalVars: []bytecode.LocalVar{
			{Name: "a", Register: 0, StartPC: 0, EndPC: 4},
			{Name: "b", Register: 1, StartPC: 0, EndPC: 4},
			{Name: "localOnly", Register: 2, StartPC: 1, EndPC: 4},
		},
	}}
	function := runtime.ReferenceValue(runtime.KindLuaClosure, closure)
	state := runtime.NewState()
	thread := state.NewThread(function)
	environment := NewEnvironment(state)

	first, err := environment.GetLocal(function, runtime.IntegerValue(1))
	if err != nil {
		// 函数形参查询不应失败。
		t.Fatalf("GetLocal function first failed: %v", err)
	}
	second, err := environment.GetLocal(runtime.ReferenceValue(runtime.KindThread, thread), function, runtime.IntegerValue(2))
	if err != nil {
		// 带 thread 的函数形参查询也不应失败。
		t.Fatalf("GetLocal thread function second failed: %v", err)
	}
	missing, err := environment.GetLocal(function, runtime.IntegerValue(3))
	if err != nil {
		// 超出固定形参数量时返回 nil，不应报错。
		t.Fatalf("GetLocal function missing failed: %v", err)
	}
	goFunction, err := environment.GetLocal(runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// Go closure 只用于确认 C 函数语义，不会被实际调用。
		return nil, nil
	})), runtime.IntegerValue(1))
	if err != nil {
		// Go closure 查询形参也不应失败。
		t.Fatalf("GetLocal Go closure failed: %v", err)
	}
	if len(first) != 1 || first[0].String != "a" || len(second) != 1 || second[0].String != "b" {
		// 固定形参名必须按声明顺序返回。
		t.Fatalf("GetLocal parameter names first=%#v second=%#v", first, second)
	}
	if len(missing) != 1 || !missing[0].IsNil() || len(goFunction) != 1 || !goFunction[0].IsNil() {
		// 普通局部变量和 Go closure 都不能作为函数形参结果返回。
		t.Fatalf("GetLocal missing/goFunction = %#v / %#v", missing, goFunction)
	}
}

// TestGetRegistryReturnsStateRegistry 验证 debug.getregistry 返回当前 State registry。
//
// registry 是 debug 库允许访问的内部表，必须保持与 State.Registry 相同 identity。
func TestGetRegistryReturnsStateRegistry(t *testing.T) {
	// 构造独立环境并读取 registry。
	state := runtime.NewState()
	environment := NewEnvironment(state)
	values, err := environment.GetRegistry()
	if err != nil {
		// registry 读取不应失败。
		t.Fatalf("GetRegistry failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindTable || values[0].Ref != state.Registry() {
		// getregistry 必须返回 State registry 本体。
		t.Fatalf("GetRegistry result = %#v", values)
	}
}

// TestGetUpvalueReadsLuaClosureSnapshot 验证 debug.getupvalue 读取 Lua closure upvalue。
//
// 当前 closure 保存 upvalue 快照；命中时应返回 Proto 中的 upvalue 名称和捕获值。
func TestGetUpvalueReadsLuaClosureSnapshot(t *testing.T) {
	// 构造携带一个命名 upvalue 的 Lua closure。
	closure := &runtime.LuaClosure{
		Proto: &bytecode.Proto{
			Upvalues: []bytecode.UpvalueDesc{{Name: "x"}},
		},
		Upvalues: []runtime.Value{runtime.StringValue("captured")},
	}
	values, err := GetUpvalue(runtime.ReferenceValue(runtime.KindLuaClosure, closure), runtime.IntegerValue(1))
	if err != nil {
		// 合法 upvalue 读取不应失败。
		t.Fatalf("GetUpvalue failed: %v", err)
	}
	if len(values) != 2 || values[0].String != "x" || values[1].String != "captured" {
		// getupvalue 必须返回名称和值。
		t.Fatalf("GetUpvalue result = %#v", values)
	}

	values, err = GetUpvalue(runtime.ReferenceValue(runtime.KindLuaClosure, closure), runtime.IntegerValue(2))
	if err != nil {
		// 越界 upvalue 读取不应失败。
		t.Fatalf("GetUpvalue out-of-range failed: %v", err)
	}
	if len(values) != 1 || !values[0].IsNil() {
		// 越界 upvalue 必须返回 nil。
		t.Fatalf("GetUpvalue out-of-range = %#v", values)
	}
}

// TestGetUserValueReturnsNilUntilUserValueExists 验证 debug.getuservalue 的阶段性空结果。
//
// runtime.Userdata 当前未保存 Lua user value，因此合法 userdata 返回 nil。
func TestGetUserValueReturnsNilUntilUserValueExists(t *testing.T) {
	// 构造 userdata 并读取 user value。
	userdata := runtime.NewUserdata("payload")
	values, err := GetUserValue(userdata.Value())
	if err != nil {
		// 合法 userdata 读取不应失败。
		t.Fatalf("GetUserValue failed: %v", err)
	}
	if len(values) != 1 || !values[0].IsNil() {
		// 当前阶段 user value 必须为空。
		t.Fatalf("GetUserValue result = %#v", values)
	}
}

// TestSetUserValueStoresUserValue 验证 debug.setuservalue 写入 userdata user value。
//
// 写入后 debug.getuservalue 必须能读回同一个 Lua 值。
func TestSetUserValueStoresUserValue(t *testing.T) {
	// 构造 userdata 并写入 user value。
	userdata := runtime.NewUserdata("payload")
	userdataValue := userdata.Value()
	results, err := SetUserValue(userdataValue, runtime.StringValue("user"))
	if err != nil {
		// 合法 userdata 写入不应失败。
		t.Fatalf("SetUserValue failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(userdataValue) {
		// setuservalue 必须返回原 userdata。
		t.Fatalf("SetUserValue result = %#v", results)
	}
	values, err := GetUserValue(userdataValue)
	if err != nil {
		// getuservalue 读取不应失败。
		t.Fatalf("GetUserValue after set failed: %v", err)
	}
	if len(values) != 1 || values[0].String != "user" {
		// getuservalue 必须读回写入值。
		t.Fatalf("GetUserValue after set = %#v", values)
	}
}

// TestSetUserValueRejectsLightUserdata 验证 debug.setuservalue 对 light userdata 的错误文本。
//
// 当前 upvalueid 用 string surrogate 表示 light userdata；官方 errors.lua 要求错误文本包含
// `light userdata`，不能只说 `userdata expected`。
func TestSetUserValueRejectsLightUserdata(t *testing.T) {
	// 通过 upvalueid 生成 light userdata surrogate。
	closure := &runtime.LuaClosure{Upvalues: []runtime.Value{runtime.StringValue("a")}}
	identity, err := UpvalueID(runtime.ReferenceValue(runtime.KindLuaClosure, closure), runtime.IntegerValue(1))
	if err != nil {
		// 合法 upvalueid 不应失败。
		t.Fatalf("UpvalueID failed: %v", err)
	}
	_, err = SetUserValue(identity[0], runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()))
	if err == nil {
		// light userdata 不能设置 user value。
		t.Fatalf("SetUserValue unexpectedly accepted light userdata")
	}
	if message := runtime.ErrorObject(err).String; !strings.Contains(message, "light userdata") {
		// 错误文本必须包含官方 errors.lua 匹配片段。
		t.Fatalf("SetUserValue light userdata error = %q", message)
	}
}

// TestSetHookUpdatesGetHookState 验证 debug.sethook 和 debug.gethook 的状态互通。
//
// 当前阶段只保存 hook 状态，不触发 VM 事件；gethook 必须能读回 sethook 写入的值。
func TestSetHookUpdatesGetHookState(t *testing.T) {
	// 构造 hook 函数并写入环境。
	environment := NewEnvironment(runtime.NewState())
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// hook 不在本阶段执行。
		return nil, nil
	}))
	if values, err := environment.SetHook(hook, runtime.StringValue("crl"), runtime.IntegerValue(7)); err != nil || len(values) != 0 {
		// sethook 写入状态不应返回值或错误。
		t.Fatalf("SetHook failed: values=%#v err=%v", values, err)
	}
	values, err := environment.GetHook()
	if err != nil {
		// gethook 读取不应失败。
		t.Fatalf("GetHook after set failed: %v", err)
	}
	if len(values) != 3 || values[0].Kind != runtime.KindGoClosure || values[1].String != "crl" || values[2].Integer != 7 {
		// gethook 必须读回 hook、mask 和 count。
		t.Fatalf("GetHook after set = %#v", values)
	}

	if values, err := environment.SetHook(runtime.NilValue()); err != nil || len(values) != 0 {
		// nil hook 清除状态不应返回值或错误。
		t.Fatalf("SetHook clear failed: values=%#v err=%v", values, err)
	}
	values, err = environment.GetHook()
	if err != nil {
		// 清除后读取不应失败。
		t.Fatalf("GetHook after clear failed: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[1].String != "" || values[2].Integer != 0 {
		// 清除后必须恢复默认 hook 状态。
		t.Fatalf("GetHook after clear = %#v", values)
	}
}

// TestSetHookThreadOverloadIsolatesCoroutineHook 验证 debug.sethook/gethook 的 thread 重载。
//
// 官方 db.lua 会对挂起协程设置 hook，并要求 debug.gethook(co) 能读回 hook，而主线程
// debug.gethook() 仍保持未设置状态。
func TestSetHookThreadOverloadIsolatesCoroutineHook(t *testing.T) {
	// 构造独立 State、debug 环境和目标协程。
	state := runtime.NewState()
	environment := NewEnvironment(state)
	thread := state.NewThread(runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{}))
	threadValue := runtime.ReferenceValue(runtime.KindThread, thread)
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// hook 不在本测试中执行，只验证状态隔离。
		return nil, nil
	}))

	if values, err := environment.SetHook(threadValue, hook, runtime.StringValue("lcr"), runtime.IntegerValue(3)); err != nil || len(values) != 0 {
		// thread 重载写入不应返回值或错误。
		t.Fatalf("SetHook thread overload failed: values=%#v err=%v", values, err)
	}
	threadHook, err := environment.GetHook(threadValue)
	if err != nil {
		// 读取协程 hook 不应失败。
		t.Fatalf("GetHook thread overload failed: %v", err)
	}
	if len(threadHook) != 3 || !threadHook[0].RawEqual(hook) || threadHook[1].String != "lcr" || threadHook[2].Integer != 3 {
		// 协程 hook 必须读回 sethook 写入的三元组。
		t.Fatalf("GetHook thread overload = %#v", threadHook)
	}
	mainHook, err := environment.GetHook()
	if err != nil {
		// 读取默认 hook 不应失败。
		t.Fatalf("GetHook main failed: %v", err)
	}
	if len(mainHook) != 3 || !mainHook[0].IsNil() || mainHook[1].String != "" || mainHook[2].Integer != 0 {
		// 对协程设置 hook 不得污染主线程默认 hook。
		t.Fatalf("GetHook main after thread set = %#v", mainHook)
	}

	if values, err := environment.SetHook(threadValue, runtime.NilValue()); err != nil || len(values) != 0 {
		// thread nil hook 只清除目标协程 hook。
		t.Fatalf("SetHook thread clear failed: values=%#v err=%v", values, err)
	}
	threadHook, err = environment.GetHook(threadValue)
	if err != nil {
		// 清除后读取协程 hook 不应失败。
		t.Fatalf("GetHook thread after clear failed: %v", err)
	}
	if len(threadHook) != 3 || !threadHook[0].IsNil() || threadHook[1].String != "" || threadHook[2].Integer != 0 {
		// 清除后协程 hook 必须恢复默认三元组。
		t.Fatalf("GetHook thread after clear = %#v", threadHook)
	}
}

// TestSetHookThreadActiveCountTracksReplacement 验证协程 hook 活跃计数缓存不会因重复设置漂移。
//
// HasActiveHook 依赖该缓存跳过无 hook 热路径；重复设置同一协程、设置空 mask/count 和清除 hook
// 都必须保持计数与真实可触发 hook 状态一致。
func TestSetHookThreadActiveCountTracksReplacement(t *testing.T) {
	// 构造独立 State、debug 环境和目标协程。
	state := runtime.NewState()
	environment := NewEnvironment(state)
	thread := state.NewThread(runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{}))
	threadValue := runtime.ReferenceValue(runtime.KindThread, thread)
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// hook 不在本测试中执行，只验证状态缓存。
		return nil, nil
	}))

	if _, err := environment.SetHook(threadValue, hook, runtime.StringValue("c"), runtime.IntegerValue(0)); err != nil {
		// 首次设置活跃协程 hook 不应失败。
		t.Fatalf("SetHook active thread hook failed: %v", err)
	}
	if environment.activeThreadHookCount != 1 {
		// 一个可触发协程 hook 应计为 1。
		t.Fatalf("activeThreadHookCount after set = %d, want 1", environment.activeThreadHookCount)
	}
	if _, err := environment.SetHook(threadValue, hook, runtime.StringValue("r"), runtime.IntegerValue(0)); err != nil {
		// 替换同一协程 hook 不应失败。
		t.Fatalf("SetHook replacement failed: %v", err)
	}
	if environment.activeThreadHookCount != 1 {
		// 替换同一协程 hook 不能重复增加活跃计数。
		t.Fatalf("activeThreadHookCount after replace = %d, want 1", environment.activeThreadHookCount)
	}
	if _, err := environment.SetHook(threadValue, hook, runtime.StringValue(""), runtime.IntegerValue(0)); err != nil {
		// 空 mask/count 的 hook 可被 gethook 读回，但不会触发 VM hook。
		t.Fatalf("SetHook inactive replacement failed: %v", err)
	}
	if environment.activeThreadHookCount != 0 {
		// 空 mask/count 不应继续保留活跃计数。
		t.Fatalf("activeThreadHookCount after inactive replace = %d, want 0", environment.activeThreadHookCount)
	}
	if _, err := environment.SetHook(threadValue, runtime.NilValue()); err != nil {
		// 清除协程 hook 不应失败。
		t.Fatalf("SetHook clear failed: %v", err)
	}
	if environment.activeThreadHookCount != 0 {
		// 清除后活跃计数必须保持为 0。
		t.Fatalf("activeThreadHookCount after clear = %d, want 0", environment.activeThreadHookCount)
	}
}

// TestSetHookAcceptsIntegerFloatCount 验证 debug.sethook 的 count 参数接受整数值 float。
//
// 官方 db.lua 使用 `2^24 - 1` 作为 count 上界；该表达式可能以 float number 传入。
func TestSetHookAcceptsIntegerFloatCount(t *testing.T) {
	// 构造 hook 函数并使用 float 整数值作为 count。
	environment := NewEnvironment(runtime.NewState())
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// hook 不在本测试中执行。
		return nil, nil
	}))
	if values, err := environment.SetHook(hook, runtime.StringValue(""), runtime.NumberValue(16777215)); err != nil || len(values) != 0 {
		// 可转整数的 float count 不应报错。
		t.Fatalf("SetHook float count failed: values=%#v err=%v", values, err)
	}
	values, err := environment.GetHook()
	if err != nil {
		// gethook 读取不应失败。
		t.Fatalf("GetHook float count failed: %v", err)
	}
	if len(values) != 3 || values[2].Integer != 16777215 {
		// count 必须按整数值保存。
		t.Fatalf("GetHook float count = %#v", values)
	}
	if _, err := environment.SetHook(hook, runtime.StringValue(""), runtime.NumberValue(1.5)); !errors.Is(err, runtime.ErrLuaError) {
		// 非整数 float count 仍必须报 Lua 参数错误。
		t.Fatalf("SetHook fractional count err=%v, want Lua error", err)
	}
}

// TestSetLocalReturnsNilUntilLocalMetadataExists 验证 debug.setlocal 的阶段性空结果。
//
// runtime 尚未保存可写局部变量窗口，因此合法 level/index/value 返回 nil。
func TestSetLocalReturnsNilUntilLocalMetadataExists(t *testing.T) {
	// 构造独立环境并尝试设置局部变量。
	environment := NewEnvironment(runtime.NewState())
	values, err := environment.SetLocal(runtime.IntegerValue(1), runtime.IntegerValue(1), runtime.StringValue("value"))
	if err != nil {
		// 合法参数不应失败。
		t.Fatalf("SetLocal failed: %v", err)
	}
	if len(values) != 1 || !values[0].IsNil() {
		// 当前阶段必须返回 nil。
		t.Fatalf("SetLocal result = %#v", values)
	}
	if _, err := environment.SetLocal(runtime.IntegerValue(1), runtime.StringValue("bad"), runtime.StringValue("value")); !errors.Is(err, runtime.ErrLuaError) {
		// 非整数 local index 必须是 Lua 参数错误。
		t.Fatalf("SetLocal argument error = %v, want Lua error", err)
	}
}

// TestSetMetatableWritesRawMetatable 验证 debug.setmetatable raw 写入元表。
//
// debug 版本必须绕过 __metatable 保护字段，直接替换 table raw 元表。
func TestSetMetatableWritesRawMetatable(t *testing.T) {
	// 构造带受保护元表的 table。
	table := runtime.NewTable()
	protected := runtime.NewTable()
	protected.RawSetString("__metatable", runtime.StringValue("locked"))
	table.SetMetatable(protected)
	tableValue := runtime.ReferenceValue(runtime.KindTable, table)
	nextMetatable := runtime.NewTable()
	nextMetatableValue := runtime.ReferenceValue(runtime.KindTable, nextMetatable)

	values, err := SetMetatable(tableValue, nextMetatableValue)
	if err != nil {
		// debug.setmetatable 应允许替换受保护元表。
		t.Fatalf("SetMetatable failed: %v", err)
	}
	if len(values) != 1 || !values[0].RawEqual(tableValue) || table.GetMetatable() != nextMetatable {
		// setmetatable 必须返回原 table 并完成 raw 写入。
		t.Fatalf("SetMetatable result=%#v metatable=%#v", values, table.GetMetatable())
	}
}

// TestSetMetatableWritesBasicTypeMetatable 验证 debug.setmetatable 可写入基础类型共享元表。
//
// Lua 5.3 允许通过 debug 库给 number、boolean 等基础类型设置类型级元表；integer 与 float
// number 必须共享同一个元表，true 与 false 也必须共享同一个 boolean 元表。
func TestSetMetatableWritesBasicTypeMetatable(t *testing.T) {
	defer runtime.SetBasicTypeMetatable(runtime.IntegerValue(0), nil)
	defer runtime.SetBasicTypeMetatable(runtime.BooleanValue(false), nil)

	numberMetatable := runtime.NewTable()
	values, err := SetMetatable(runtime.IntegerValue(10), runtime.ReferenceValue(runtime.KindTable, numberMetatable))
	if err != nil {
		// number 类型级元表写入必须成功。
		t.Fatalf("SetMetatable number failed: %v", err)
	}
	if len(values) != 1 || !values[0].RawEqual(runtime.IntegerValue(10)) {
		// debug.setmetatable 必须返回原始 value。
		t.Fatalf("SetMetatable number result=%#v", values)
	}
	if got := runtime.BasicTypeMetatable(runtime.NumberValue(1.5)); got != numberMetatable {
		// integer 与 float number 必须共享同一个 number 元表。
		t.Fatalf("number metatable mismatch: %p", got)
	}

	booleanMetatable := runtime.NewTable()
	if _, err := SetMetatable(runtime.BooleanValue(true), runtime.ReferenceValue(runtime.KindTable, booleanMetatable)); err != nil {
		// boolean 类型级元表写入必须成功。
		t.Fatalf("SetMetatable boolean failed: %v", err)
	}
	if got := runtime.BasicTypeMetatable(runtime.BooleanValue(false)); got != booleanMetatable {
		// true 与 false 必须共享同一个 boolean 元表。
		t.Fatalf("boolean metatable mismatch: %p", got)
	}
	if _, err := SetMetatable(runtime.BooleanValue(false), runtime.NilValue()); err != nil {
		// nil 第二参数必须移除对应类型级元表。
		t.Fatalf("clear boolean metatable failed: %v", err)
	}
	if got := runtime.BasicTypeMetatable(runtime.BooleanValue(true)); got != nil {
		// 移除 false 的元表也应影响 true。
		t.Fatalf("boolean metatable should be nil: %p", got)
	}
}

// TestSetUpvalueWritesLuaClosureSnapshot 验证 debug.setupvalue 写入 Lua closure upvalue。
//
// 写入后 debug.getupvalue 必须返回新的 upvalue 值。
func TestSetUpvalueWritesLuaClosureSnapshot(t *testing.T) {
	// 构造携带一个命名 upvalue 的 Lua closure。
	closure := &runtime.LuaClosure{
		Proto: &bytecode.Proto{
			Upvalues: []bytecode.UpvalueDesc{{Name: "x"}},
		},
		Upvalues: []runtime.Value{runtime.StringValue("old")},
	}
	closureValue := runtime.ReferenceValue(runtime.KindLuaClosure, closure)
	values, err := SetUpvalue(closureValue, runtime.IntegerValue(1), runtime.StringValue("new"))
	if err != nil {
		// 合法 setupvalue 不应失败。
		t.Fatalf("SetUpvalue failed: %v", err)
	}
	if len(values) != 1 || values[0].String != "x" || closure.Upvalues[0].String != "new" {
		// setupvalue 必须返回 upvalue 名称并写入新值。
		t.Fatalf("SetUpvalue result=%#v upvalue=%#v", values, closure.Upvalues[0])
	}
}

// TestTracebackFormatsStateFrames 验证 debug.traceback 使用 State 调用帧生成文本。
//
// 当前阶段复用 runtime.Traceback，输出必须包含消息和调用帧类型。
func TestTracebackFormatsStateFrames(t *testing.T) {
	// 构造带一个 Go 帧的 State。
	state := runtime.NewState()
	if err := state.PushCallFrame(runtime.NewGoCallFrame(runtime.ReferenceValue(runtime.KindGoClosure, "go-fn"), 1, 0)); err != nil {
		// 测试前置帧必须压入成功。
		t.Fatalf("PushCallFrame failed: %v", err)
	}
	environment := NewEnvironment(state)
	values, err := environment.Traceback(runtime.StringValue("boom"))
	if err != nil {
		// traceback 格式化不应失败。
		t.Fatalf("Traceback failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindString {
		// traceback 必须返回 string。
		t.Fatalf("Traceback result = %#v", values)
	}
	if values[0].String == "" || !containsAll(values[0].String, []string{"boom", "stack traceback", "[go]"}) {
		// traceback 文本必须包含消息和帧类型。
		t.Fatalf("Traceback text = %q", values[0].String)
	}
}

// TestTracebackReturnsNonStringMessage 验证 debug.traceback 对非字符串 message 原样返回。
//
// 官方 db.lua 要求 debug.traceback(print) 和 debug.traceback(print, level) 都直接返回 print 函数。
func TestTracebackReturnsNonStringMessage(t *testing.T) {
	// 构造非 string 的 Go closure message。
	environment := NewEnvironment(runtime.NewState())
	message := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// message 函数不在本测试中执行。
		return nil, nil
	}))
	values, err := environment.Traceback(message, runtime.IntegerValue(4))
	if err != nil {
		// 非 string message 不应报错。
		t.Fatalf("Traceback non-string failed: %v", err)
	}
	if len(values) != 1 || !values[0].RawEqual(message) {
		// 返回值必须是原始 message 对象。
		t.Fatalf("Traceback non-string = %#v", values)
	}
}

// TestTracebackThreadOverloadReturnsString 验证 debug.traceback 的 thread 重载解析。
//
// 官方 db.lua 会调用 debug.traceback(co, nil, level) 并交给 string.gmatch；首参为 thread 时不能按
// 非字符串 message 原样返回 thread。
func TestTracebackThreadOverloadReturnsString(t *testing.T) {
	// 构造主 State 与一个协程 thread 值。
	state := runtime.NewState()
	thread := state.NewThread(runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 协程函数不在本测试中执行。
		return nil, nil
	})))
	environment := NewEnvironment(state)

	values, err := environment.Traceback(runtime.ReferenceValue(runtime.KindThread, thread), runtime.NilValue(), runtime.IntegerValue(1))
	if err != nil {
		// thread 重载解析不应失败。
		t.Fatalf("Traceback thread overload failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindString || !strings.Contains(values[0].String, "stack traceback") {
		// thread 重载必须返回 traceback 字符串，供 string.gmatch 消费。
		t.Fatalf("Traceback thread overload = %#v", values)
	}
}

// TestTrimCoroutineResumeBoundary 验证 thread traceback 会裁掉 resume 外层主线程帧。
//
// coroutine.yield 保存的 State 栈包含 yield、Lua 帧、resume 和主线程调用者；官方 thread
// traceback 只展示目标协程内部栈。
func TestTrimCoroutineResumeBoundary(t *testing.T) {
	// 构造包含 resume 边界的帧序列。
	frames := []runtime.CallFrame{
		{Name: "yield", Kind: runtime.CallFrameKindGo},
		{Kind: runtime.CallFrameKindLua},
		{Name: "resume", Kind: runtime.CallFrameKindGo},
		{Kind: runtime.CallFrameKindLua},
	}
	trimmedFrames := trimCoroutineResumeBoundary(frames)
	if len(trimmedFrames) != 2 || trimmedFrames[0].Name != "yield" || trimmedFrames[1].Kind != runtime.CallFrameKindLua {
		// resume 及其外层帧必须被裁掉。
		t.Fatalf("trimCoroutineResumeBoundary = %#v", trimmedFrames)
	}
}

// TestUpvalueIDReturnsStableIdentifier 验证 debug.upvalueid 返回稳定身份。
//
// runtime 当前没有 lightuserdata，本阶段用 string 标识同一 closure 同一 upvalue。
func TestUpvalueIDReturnsStableIdentifier(t *testing.T) {
	// 构造带两个 upvalue 的 Lua closure。
	closure := &runtime.LuaClosure{Upvalues: []runtime.Value{runtime.StringValue("a"), runtime.StringValue("b")}}
	closureValue := runtime.ReferenceValue(runtime.KindLuaClosure, closure)
	first, err := UpvalueID(closureValue, runtime.IntegerValue(1))
	if err != nil {
		// 合法 upvalueid 不应失败。
		t.Fatalf("UpvalueID first failed: %v", err)
	}
	second, err := UpvalueID(closureValue, runtime.IntegerValue(1))
	if err != nil {
		// 重复读取同一 upvalue id 不应失败。
		t.Fatalf("UpvalueID second failed: %v", err)
	}
	other, err := UpvalueID(closureValue, runtime.IntegerValue(2))
	if err != nil {
		// 读取另一个 upvalue id 不应失败。
		t.Fatalf("UpvalueID other failed: %v", err)
	}
	if len(first) != 1 || len(second) != 1 || len(other) != 1 || first[0].String == "" || first[0].String != second[0].String || first[0].String == other[0].String {
		// 同一 upvalue id 必须稳定，不同 upvalue id 必须不同。
		t.Fatalf("UpvalueID first=%#v second=%#v other=%#v", first, second, other)
	}
}

// TestUpvalueIDRejectsInvalidIndex 验证 debug.upvalueid 对越界 upvalue 抛错。
//
// Lua 5.3 中 upvalueid 不是 getupvalue，非法索引必须作为参数错误暴露，官方 closure.lua
// 使用 pcall(debug.upvalueid, f, n) 校验该行为。
func TestUpvalueIDRejectsInvalidIndex(t *testing.T) {
	closure := &runtime.LuaClosure{Upvalues: []runtime.Value{runtime.StringValue("a")}}
	closureValue := runtime.ReferenceValue(runtime.KindLuaClosure, closure)
	if _, err := UpvalueID(closureValue, runtime.IntegerValue(2)); err == nil {
		// 越界 upvalue index 必须返回错误。
		t.Fatalf("UpvalueID invalid index succeeded")
	}
	if _, err := UpvalueID(closureValue, runtime.IntegerValue(0)); err == nil {
		// 非正 upvalue index 必须返回错误。
		t.Fatalf("UpvalueID non-positive index succeeded")
	}
}

// TestUpvalueIDSupportsGoClosureWithUpvalues 验证带元数据的 Go closure 可返回 upvalue id。
//
// string.gmatch 返回的迭代器是 GoClosureWithUpvalues，官方 closure.lua 要求 upvalueid 对
// 这类显式暴露 upvalue 的 Go closure 返回非 nil 标识。
func TestUpvalueIDSupportsGoClosureWithUpvalues(t *testing.T) {
	closure := &runtime.GoClosureWithUpvalues{
		Function: runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			return nil, nil
		}),
		Upvalues: []runtime.Value{runtime.StringValue("source")},
	}
	closureValue := runtime.ReferenceValue(runtime.KindGoClosure, closure)
	values, err := UpvalueID(closureValue, runtime.IntegerValue(1))
	if err != nil {
		// 合法 Go closure upvalue id 不应失败。
		t.Fatalf("UpvalueID Go closure failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindString || values[0].String == "" {
		// 返回值必须是可比较的非空标识。
		t.Fatalf("UpvalueID Go closure result=%#v", values)
	}
}

// TestUpvalueJoinCopiesLegacySourceSnapshot 验证 debug.upvaluejoin 对旧快照闭包的兼容 fallback。
//
// 没有 UpvalueCells 的手工旧闭包无法建立共享 identity，因此只复制来源当前值。
func TestUpvalueJoinCopiesLegacySourceSnapshot(t *testing.T) {
	// 构造目标和来源 Lua closure。
	target := &runtime.LuaClosure{Upvalues: []runtime.Value{runtime.StringValue("target")}}
	source := &runtime.LuaClosure{Upvalues: []runtime.Value{runtime.StringValue("source")}}

	values, err := UpvalueJoin(
		runtime.ReferenceValue(runtime.KindLuaClosure, target),
		runtime.IntegerValue(1),
		runtime.ReferenceValue(runtime.KindLuaClosure, source),
		runtime.IntegerValue(1),
	)
	if err != nil {
		// 合法 upvaluejoin 不应失败。
		t.Fatalf("UpvalueJoin failed: %v", err)
	}
	if len(values) != 0 || target.Upvalues[0].String != "source" {
		// upvaluejoin 必须不返回值，并把目标 upvalue 替换为来源值。
		t.Fatalf("UpvalueJoin result=%#v target=%#v", values, target.Upvalues[0])
	}
}

// TestUpvalueJoinSharesSourceCell 验证 debug.upvaluejoin 会让目标和来源闭包共享真实 upvalue cell。
//
// join 后通过 debug.setupvalue 修改来源值，目标必须立即观察到变化，且两者 upvalueid 必须一致。
func TestUpvalueJoinSharesSourceCell(t *testing.T) {
	// 为目标和来源分别构造独立闭合 cell，避免测试依赖 VM 寄存器生命周期。
	targetCell := runtime.NewClosedUpvalueCell(runtime.StringValue("target"))
	sourceCell := runtime.NewClosedUpvalueCell(runtime.StringValue("source"))
	target := &runtime.LuaClosure{
		Upvalues:     []runtime.Value{runtime.StringValue("target")},
		UpvalueCells: []*runtime.UpvalueCell{targetCell},
	}
	source := &runtime.LuaClosure{
		Upvalues:     []runtime.Value{runtime.StringValue("source")},
		UpvalueCells: []*runtime.UpvalueCell{sourceCell},
	}
	targetValue := runtime.ReferenceValue(runtime.KindLuaClosure, target)
	sourceValue := runtime.ReferenceValue(runtime.KindLuaClosure, source)
	if _, err := UpvalueJoin(targetValue, runtime.IntegerValue(1), sourceValue, runtime.IntegerValue(1)); err != nil {
		// 合法 Lua closure 和 upvalue 下标必须完成共享绑定。
		t.Fatalf("UpvalueJoin failed: %v", err)
	}
	if target.UpvalueCells[0] != sourceCell {
		// 目标必须直接引用来源 cell，而不是只复制当前值。
		t.Fatalf("target cell=%p source cell=%p", target.UpvalueCells[0], sourceCell)
	}
	if _, err := SetUpvalue(sourceValue, runtime.IntegerValue(1), runtime.StringValue("updated")); err != nil {
		// 来源 setupvalue 必须写入共享 cell。
		t.Fatalf("SetUpvalue failed: %v", err)
	}
	values, err := GetUpvalue(targetValue, runtime.IntegerValue(1))
	if err != nil || len(values) != 2 || values[1].String != "updated" {
		// 目标闭包应立即读取来源写入的新值。
		t.Fatalf("GetUpvalue target values=%#v err=%v", values, err)
	}
	targetID, targetErr := UpvalueID(targetValue, runtime.IntegerValue(1))
	sourceID, sourceErr := UpvalueID(sourceValue, runtime.IntegerValue(1))
	if targetErr != nil || sourceErr != nil || len(targetID) != 1 || len(sourceID) != 1 || targetID[0].String != sourceID[0].String {
		// 共享 cell 的 debug.upvalueid 必须相同。
		t.Fatalf("upvalue ids target=%#v source=%#v errors=%v/%v", targetID, sourceID, targetErr, sourceErr)
	}
}

// TestHookDispatchCallReturnLine 验证 call、return 和 line hook 显式派发。
//
// 该测试固定 debug 环境的底层显式派发顺序；VM 自动触发路径由 lua/api_test.go 的脚本回归覆盖。
func TestHookDispatchCallReturnLine(t *testing.T) {
	// 记录 hook 事件和行号，便于断言派发顺序。
	environment := NewEnvironment(runtime.NewState())
	events := make([]string, 0, 3)
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// hook 参数为事件名和行号。
		events = append(events, args[0].String)
		if args[0].String == HookEventLine && args[1].Integer != 42 {
			// line hook 必须携带当前行号。
			t.Fatalf("line hook line = %d, want 42", args[1].Integer)
		}
		return nil, nil
	}))
	if _, err := environment.SetHook(hook, runtime.StringValue("crl"), runtime.IntegerValue(0)); err != nil {
		// sethook 必须成功。
		t.Fatalf("SetHook failed: %v", err)
	}
	if err := environment.TriggerCallHook(); err != nil {
		// call hook 派发不应失败。
		t.Fatalf("TriggerCallHook failed: %v", err)
	}
	if err := environment.TriggerReturnHook(); err != nil {
		// return hook 派发不应失败。
		t.Fatalf("TriggerReturnHook failed: %v", err)
	}
	if err := environment.TriggerLineHook(42); err != nil {
		// line hook 派发不应失败。
		t.Fatalf("TriggerLineHook failed: %v", err)
	}
	if strings.Join(events, ",") != "call,return,line" {
		// hook 派发顺序必须与触发顺序一致。
		t.Fatalf("hook events = %#v", events)
	}
}

// TestHookDispatchLuaRunnerForCallReturn 验证 call/return hook 可通过 VM runner 执行 Lua closure。
//
// debug 环境自身无法直接执行 Lua closure；VM 接入点必须为 call/return 提供 runner，才能覆盖
// 官方 db.lua 中 Lua 函数作为 hook 的场景。
func TestHookDispatchLuaRunnerForCallReturn(t *testing.T) {
	// 使用 Lua closure 类型占位，确保 dispatch 走 runner 而不是 Go 直接调用路径。
	environment := NewEnvironment(runtime.NewState())
	hook := runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{})
	events := make([]string, 0, 2)
	if _, err := environment.SetHook(hook, runtime.StringValue("cr"), runtime.IntegerValue(0)); err != nil {
		// sethook 必须接受 Lua closure。
		t.Fatalf("SetHook failed: %v", err)
	}
	runner := func(receivedHook runtime.Value, event string, hookLine int64) error {
		// runner 应拿到原 hook 值、事件名和 call/return 的 0 行号。
		if receivedHook != hook {
			// hook 值必须保持原样传递给 VM。
			t.Fatalf("hook = %#v, want %#v", receivedHook, hook)
		}
		if hookLine != 0 {
			// call/return hook 不携带源码行号。
			t.Fatalf("hook line = %d, want 0", hookLine)
		}
		events = append(events, event)
		return nil
	}
	if err := environment.TriggerCallHookWithRunner(runner); err != nil {
		// call runner 派发不应失败。
		t.Fatalf("TriggerCallHookWithRunner failed: %v", err)
	}
	if err := environment.TriggerReturnHookWithRunner(runner); err != nil {
		// return runner 派发不应失败。
		t.Fatalf("TriggerReturnHookWithRunner failed: %v", err)
	}
	if strings.Join(events, ",") != "call,return" {
		// runner 必须按触发顺序收到 call 与 return。
		t.Fatalf("runner events = %#v", events)
	}
}

// TestCountHookInterval 验证 count hook 按指令间隔触发。
//
// count hook 不依赖 mask 字符，只要 count 为正数并达到间隔就触发。
func TestCountHookInterval(t *testing.T) {
	// 使用 count=2，触发三次后应只执行一次 hook。
	environment := NewEnvironment(runtime.NewState())
	triggered := 0
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// count hook 参数事件名必须为 count。
		if args[0].String != HookEventCount {
			// 非 count 事件说明派发错误。
			t.Fatalf("count hook event = %s", args[0].String)
		}
		triggered++
		return nil, nil
	}))
	if _, err := environment.SetHook(hook, runtime.StringValue(""), runtime.IntegerValue(2)); err != nil {
		// sethook 必须成功。
		t.Fatalf("SetHook count failed: %v", err)
	}
	for index := 0; index < 3; index++ {
		// 连续模拟三条指令检查点。
		if err := environment.TriggerCountHook(); err != nil {
			t.Fatalf("TriggerCountHook #%d failed: %v", index, err)
		}
	}
	if triggered != 1 {
		// count=2 时三次检查只应触发一次。
		t.Fatalf("count hook triggered = %d, want 1", triggered)
	}
}

// TestHookReentryIsIgnored 验证 hook 重入保护。
//
// hook 执行期间再次触发 hook 时必须被忽略，避免递归进入同一个 debug hook。
func TestHookReentryIsIgnored(t *testing.T) {
	// hook 内部再次触发 call hook，最终执行次数仍应为 1。
	environment := NewEnvironment(runtime.NewState())
	triggered := 0
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 第一次进入 hook 时模拟重入触发。
		triggered++
		if err := environment.TriggerCallHook(); err != nil {
			// 重入触发不应失败。
			t.Fatalf("nested TriggerCallHook failed: %v", err)
		}
		return nil, nil
	}))
	if _, err := environment.SetHook(hook, runtime.StringValue("c"), runtime.IntegerValue(0)); err != nil {
		// sethook 必须成功。
		t.Fatalf("SetHook failed: %v", err)
	}
	if err := environment.TriggerCallHook(); err != nil {
		// 外层 call hook 不应失败。
		t.Fatalf("TriggerCallHook failed: %v", err)
	}
	if triggered != 1 {
		// 重入保护必须阻止第二次执行 hook。
		t.Fatalf("hook triggered = %d, want 1", triggered)
	}
}

// TestHookErrorPropagates 验证 hook 中错误会向触发点传播。
//
// VM 后续接入 hook 时依赖该行为把 hook 错误转换为运行时错误路径。
func TestHookErrorPropagates(t *testing.T) {
	// 构造稳定 sentinel 错误用于 errors.Is 断言。
	environment := NewEnvironment(runtime.NewState())
	sentinel := errors.New("hook failed")
	hook := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// hook 直接返回 sentinel，触发点应原样传播。
		return nil, sentinel
	}))
	if _, err := environment.SetHook(hook, runtime.StringValue("l"), runtime.IntegerValue(0)); err != nil {
		// sethook 必须成功。
		t.Fatalf("SetHook failed: %v", err)
	}
	if err := environment.TriggerLineHook(12); !errors.Is(err, sentinel) {
		// hook 错误必须保留错误链，便于上层识别。
		t.Fatalf("TriggerLineHook error = %v, want sentinel", err)
	}
}

// TestGetMetatableReadsRawMetatable 验证 debug.getmetatable 忽略 __metatable 保护字段。
//
// 这区别于 base.getmetatable，debug 库必须返回 raw 元表以便调试和诊断。
func TestGetMetatableReadsRawMetatable(t *testing.T) {
	// 构造带受保护元表的 table。
	table := runtime.NewTable()
	metatable := runtime.NewTable()
	metatable.RawSetString("__metatable", runtime.StringValue("locked"))
	table.SetMetatable(metatable)

	values, err := GetMetatable(runtime.ReferenceValue(runtime.KindTable, table))
	if err != nil {
		// raw 元表读取不应失败。
		t.Fatalf("GetMetatable failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindTable || values[0].Ref != metatable {
		// debug.getmetatable 必须返回 raw 元表，而不是 __metatable 字段。
		t.Fatalf("GetMetatable result = %#v", values)
	}

	values, err = GetMetatable(runtime.StringValue("not-table"))
	if err != nil {
		// 非 table 读取不应失败。
		t.Fatalf("GetMetatable non-table failed: %v", err)
	}
	if len(values) != 1 {
		// 非 table 读取仍必须返回单个结果。
		t.Fatalf("GetMetatable non-table result count = %#v", values)
	}
}

// containsAll 判断文本是否包含所有片段。
//
// text 是待检查文本；parts 是必须出现的片段列表。全部出现时返回 true。
func containsAll(text string, parts []string) bool {
	// 逐个片段检查，遇到缺失即可提前返回 false。
	for _, part := range parts {
		if !strings.Contains(text, part) {
			// 缺少任意片段都表示匹配失败。
			return false
		}
	}
	return true
}
