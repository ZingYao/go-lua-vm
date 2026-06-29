package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/extensions"
	"github.com/zing/go-lua-vm/lua"
	"github.com/zing/go-lua-vm/runtime"
)

// TestParseArgs 验证 glua 第一阶段参数解析。
//
// 解析器必须支持独立和紧凑形式的 -e/-l，并能识别 -i 标记。
func TestParseArgs(t *testing.T) {
	// 构造包含两种 -e/-l 形式和 -i 的参数列表。
	options, err := ParseArgs([]string{"-e", "print(1)", "-eprint(2)", "-l", "moda", "-lmodb", "-v", "-E", "-i"})
	if err != nil {
		// 合法参数不应解析失败。
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if len(options.Expressions) != 2 || options.Expressions[0] != "print(1)" || options.Expressions[1] != "print(2)" {
		// -e 参数必须按出现顺序保存。
		t.Fatalf("expressions = %#v", options.Expressions)
	}
	if len(options.Libraries) != 2 || options.Libraries[0] != "moda" || options.Libraries[1] != "modb" {
		// -l 参数必须按出现顺序保存。
		t.Fatalf("libraries = %#v", options.Libraries)
	}
	if !options.Interactive {
		// -i 必须设置交互模式标记。
		t.Fatalf("interactive should be true")
	}
	if !options.Version {
		// -v 必须设置版本输出标记。
		t.Fatalf("version should be true")
	}
	if !options.IgnoreEnvironment {
		// -E 必须设置忽略环境变量标记。
		t.Fatalf("ignore environment should be true")
	}
}

// TestParseArgsSyntaxOptions 验证 glua 语法扩展参数解析。
//
// --syntax 设置基础集合，--disable-syntax 在基础集合上移除指定扩展。
func TestParseArgsSyntaxOptions(t *testing.T) {
	if extensions.Compiled().Has(extensions.SyntaxContinue | extensions.SyntaxSwitch) {
		// 只有当前构建包含两个扩展时，才验证 extended 后局部关闭 switch 的正向路径。
		options, err := ParseArgs([]string{"--syntax=extended", "--disable-syntax", "switch", "-e", "while true do continue end"})
		if err != nil {
			// 合法语法扩展参数不应失败。
			t.Fatalf("ParseArgs syntax failed: %v", err)
		}
		if !options.SyntaxExtensionsSet || !options.SyntaxExtensions.Has(extensions.SyntaxContinue) || !options.SyntaxExtensions.Has(extensions.SyntaxSwitch) {
			// extended 必须映射到当前构建内所有扩展。
			t.Fatalf("syntax extensions = %#v set=%v", options.SyntaxExtensions, options.SyntaxExtensionsSet)
		}
		if options.DisabledSyntaxExtensions != extensions.SyntaxSwitch {
			// disable-syntax 必须记录待关闭的 switch 位。
			t.Fatalf("disabled syntax = %#v", options.DisabledSyntaxExtensions)
		}
		if finalSyntax := syntaxForOptions(options); finalSyntax.Has(extensions.SyntaxSwitch) || !finalSyntax.Has(extensions.SyntaxContinue) {
			// 最终集合应只保留 continue。
			t.Fatalf("final syntax = %#v", finalSyntax)
		}
	}

	options, err := ParseArgs([]string{"--syntax", "lua53", "-e", "local continue = 1"})
	if err != nil {
		// lua53 模式是合法语法模式。
		t.Fatalf("ParseArgs lua53 failed: %v", err)
	}
	if syntaxForOptions(options) != extensions.None() {
		// lua53 模式必须关闭全部扩展。
		t.Fatalf("lua53 syntax = %#v", syntaxForOptions(options))
	}
}

// TestParseArgsRejectsInvalidInput 验证参数解析错误。
//
// 缺少 -e/-l 入参和未知选项必须返回明确错误。
func TestParseArgsRejectsInvalidInput(t *testing.T) {
	testCases := [][]string{
		{"-e"},
		{"-l"},
		{"-unknown"},
	}
	for _, testCase := range testCases {
		// 每个非法参数组合都必须失败。
		if _, err := ParseArgs(testCase); err == nil {
			// 未返回错误会让 CLI 后续误以为该能力已经实现。
			t.Fatalf("ParseArgs(%#v) should fail", testCase)
		}
	}
}

// TestParseArgsScriptAndStopOptions 验证脚本路径、脚本参数和 -- 终止选项。
//
// 第一个非选项参数或 -- 后第一个参数必须成为脚本路径，其余参数必须进入 ScriptArgs。
func TestParseArgsScriptAndStopOptions(t *testing.T) {
	// 普通脚本路径后续参数进入 ScriptArgs。
	options, err := ParseArgs([]string{"script.lua", "a", "b"})
	if err != nil {
		// 合法脚本参数不应失败。
		t.Fatalf("ParseArgs script failed: %v", err)
	}
	if options.ScriptPath != "script.lua" || len(options.ScriptArgs) != 2 || options.ScriptArgs[0] != "a" || options.ScriptArgs[1] != "b" {
		// 脚本路径和参数必须保持顺序。
		t.Fatalf("script options = %#v", options)
	}

	// -- 后即使是 - 开头也必须按脚本路径处理。
	options, err = ParseArgs([]string{"--", "-file.lua", "x"})
	if err != nil {
		// -- 终止选项后不应解析 -file.lua 为未知选项。
		t.Fatalf("ParseArgs -- failed: %v", err)
	}
	if options.ScriptPath != "-file.lua" || len(options.ScriptArgs) != 1 || options.ScriptArgs[0] != "x" {
		// -- 后脚本路径和参数必须保留原样。
		t.Fatalf("stop options = %#v", options)
	}

	// 单独 - 表示 stdin 脚本。
	options, err = ParseArgs([]string{"-", "stdinArg"})
	if err != nil {
		// stdin 脚本参数不应失败。
		t.Fatalf("ParseArgs stdin failed: %v", err)
	}
	if options.ScriptPath != "-" || len(options.ScriptArgs) != 1 || options.ScriptArgs[0] != "stdinArg" {
		// stdin 路径和参数必须保存。
		t.Fatalf("stdin options = %#v", options)
	}
}

// TestParseArgsListBytecodeConflict 验证 glua 可选反汇编模式不会抢占官方 -l 语义。
//
// `--list-bytecode` 可以单独使用，但不能与官方 `-l` 模块加载、`-e`、脚本执行或 `-i` 混用。
func TestParseArgsListBytecodeConflict(t *testing.T) {
	// 单独使用长选项应记录反汇编路径。
	options, err := ParseArgs([]string{"--list-bytecode", "script.lua"})
	if err != nil {
		// 合法反汇编参数不应失败。
		t.Fatalf("ParseArgs list bytecode failed: %v", err)
	}
	if options.ListBytecodePath != "script.lua" || len(options.Libraries) != 0 {
		// --list-bytecode 必须独立保存路径，且不写入官方 -l 模块列表。
		t.Fatalf("list bytecode options = %#v", options)
	}

	conflicts := [][]string{
		{"--list-bytecode", "script.lua", "-l", "mod"},
		{"--list-bytecode", "script.lua", "-e", "return 1"},
		{"--list-bytecode", "script.lua", "-i"},
		{"--list-bytecode", "script.lua", "--", "run.lua"},
	}
	for _, args := range conflicts {
		// 每组冲突参数都必须在解析阶段失败。
		if _, err := ParseArgs(args); err == nil {
			// 未失败会导致 glua 同时承担运行和反汇编语义。
			t.Fatalf("ParseArgs(%#v) should reject list-bytecode conflict", args)
		}
	}
}

// TestRunListBytecode 验证 glua 长选项可输出源码反汇编。
//
// 该能力使用 `--list-bytecode`，明确避开官方 `lua -l <module>` 参数。
func TestRunListBytecode(t *testing.T) {
	// 写入最小源码文件作为反汇编输入。
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "script.lua")
	if err := os.WriteFile(scriptPath, []byte("local x = 1\nreturn x\n"), 0o600); err != nil {
		// 测试脚本写入不应失败。
		t.Fatalf("WriteFile failed: %v", err)
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"--list-bytecode", scriptPath}, Streams{Stdout: &stdout}); err != nil {
		// 反汇编源码不应失败。
		t.Fatalf("Run list bytecode failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "LOADK") || !strings.Contains(stdout.String(), "RETURN") {
		// 输出必须包含源码对应的核心指令。
		t.Fatalf("list bytecode output = %q", stdout.String())
	}
}

// TestRunListBytecodeHonorsSyntaxOptions 验证 glua 反汇编源码输入遵守语法开关。
func TestRunListBytecodeHonorsSyntaxOptions(t *testing.T) {
	// 写入使用扩展语法的源码文件。
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "script.lua")
	if err := os.WriteFile(scriptPath, []byte("while true do continue end\n"), 0o600); err != nil {
		// 测试脚本写入不应失败。
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := Run(context.Background(), []string{"--syntax=lua53", "--list-bytecode", scriptPath}, Streams{}); err == nil {
		// lua53 模式下 continue 语法糖应被拒绝。
		t.Fatalf("Run list bytecode should reject disabled continue")
	}
}

// TestLoadLibrariesUsesRequireCache 验证 -l 模块加载通过 require 路径。
//
// 当前测试预先写入 package.loaded，require 应直接返回缓存模块，避免依赖 Lua 文件 loader。
func TestLoadLibrariesUsesRequireCache(t *testing.T) {
	// 创建 State 并打开标准库，确保 package.loaded 和 require 可用。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	packageValue, err := lua.GetGlobal(state, "package")
	if err != nil {
		// package 全局表必须存在。
		t.Fatalf("GetGlobal package failed: %v", err)
	}
	packageTable := packageValue.Ref.(*runtime.Table)
	loadedValue := packageTable.RawGetString("loaded")
	loadedTable := loadedValue.Ref.(*runtime.Table)
	moduleTable := runtime.NewTable()
	moduleTable.RawSetString("name", runtime.StringValue("demo"))
	loadedTable.RawSetString("demo", runtime.ReferenceValue(runtime.KindTable, moduleTable))

	if err := loadLibraries(state, []string{"demo"}); err != nil {
		// 已缓存模块通过 -l 加载不应失败。
		t.Fatalf("loadLibraries failed: %v", err)
	}
	globalValue, err := lua.GetGlobal(state, "demo")
	if err != nil {
		// 全局读取不应失败。
		t.Fatalf("GetGlobal demo failed: %v", err)
	}
	if globalValue.Kind != runtime.KindTable || globalValue.Ref != moduleTable {
		// -l 必须把 require 返回值写入同名全局，兼容 Lua 5.3 CLI。
		t.Fatalf("global demo = %#v, want module table", globalValue)
	}
	if err := loadLibraries(state, []string{""}); err == nil {
		// 空模块名必须失败，避免 require 空 key。
		t.Fatalf("loadLibraries empty module should fail")
	}
}

// TestExecuteExpressionsRunsChunks 验证 -e 片段执行路径。
//
// 合法 Lua 片段应完成解析、codegen 和最小 VM 执行。
func TestExecuteExpressionsRunsChunks(t *testing.T) {
	// 创建 State 并执行最小表达式片段。
	state := lua.NewState()
	defer state.Close()
	err := executeExpressions(state, []string{"return 1"})
	if err != nil {
		// 最小 return chunk 应执行成功。
		t.Fatalf("executeExpressions error = %v", err)
	}
	if err := executeExpressions(nil, []string{"return 1"}); !errors.Is(err, lua.ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("executeExpressions nil state error = %v", err)
	}
}

// TestExecuteScriptFileAndStdinRun 验证脚本文件和 stdin 执行路径。
//
// 脚本应完成读取、编译和最小 VM 执行。
func TestExecuteScriptFileAndStdinRun(t *testing.T) {
	// 创建临时 Lua 文件，验证普通脚本路径走 DoFile。
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "script.lua")
	if err := os.WriteFile(scriptPath, []byte("first = ...\n"), 0o600); err != nil {
		// 测试脚本写入不应失败。
		t.Fatalf("WriteFile failed: %v", err)
	}
	state := lua.NewState()
	defer state.Close()
	err := executeScript(state, scriptPath, []string{"arg1"}, nil)
	if err != nil {
		// 文件脚本应执行成功。
		t.Fatalf("executeScript file error = %v", err)
	}
	firstValue, err := lua.GetGlobal(state, "first")
	if err != nil {
		// first 全局读取不应失败。
		t.Fatalf("GetGlobal first failed: %v", err)
	}
	if firstValue.Kind != runtime.KindString || firstValue.String != "arg1" {
		// 脚本参数必须作为 chunk vararg 传入。
		t.Fatalf("first = %#v, want arg1", firstValue)
	}

	// stdin 路径读取 reader 内容并执行。
	stdinState := lua.NewState()
	defer stdinState.Close()
	err = executeScript(stdinState, "-", nil, strings.NewReader("return 2\n"))
	if err != nil {
		// stdin 脚本应执行成功。
		t.Fatalf("executeScript stdin error = %v", err)
	}

	// stdin chunk 的 vararg 必须读取当前 arg 表，保留 -e 对 arg 的修改。
	mutatedArgState := lua.NewState()
	defer mutatedArgState.Close()
	if err := setArgTable(mutatedArgState, "-", nil, nil); err != nil {
		// stdin arg 表写入不应失败。
		t.Fatalf("setArgTable stdin failed: %v", err)
	}
	if err := lua.DoString(mutatedArgState, "arg[1] = 100"); err != nil {
		// 模拟 -e 修改 arg[1]。
		t.Fatalf("mutate arg failed: %v", err)
	}
	err = executeScript(mutatedArgState, "-", nil, strings.NewReader("first = ...\n"))
	if err != nil {
		// stdin 脚本应读取修改后的 arg。
		t.Fatalf("executeScript mutated stdin error = %v", err)
	}
	mutatedFirst, err := lua.GetGlobal(mutatedArgState, "first")
	if err != nil {
		// first 全局读取不应失败。
		t.Fatalf("GetGlobal mutated first failed: %v", err)
	}
	if mutatedFirst.Kind != runtime.KindInteger || mutatedFirst.Integer != 100 {
		// stdin chunk 的 ... 应来自当前 arg[1]。
		t.Fatalf("mutated first = %#v, want 100", mutatedFirst)
	}

	// arg 被启动片段改成非 table 时，脚本执行必须失败。
	badArgState := lua.NewState()
	defer badArgState.Close()
	if err := setArgTable(badArgState, "-", nil, nil); err != nil {
		// stdin arg 表写入不应失败。
		t.Fatalf("setArgTable bad arg failed: %v", err)
	}
	if err := lua.DoString(badArgState, "arg = 1"); err != nil {
		// 模拟 -e 损坏 arg。
		t.Fatalf("replace arg failed: %v", err)
	}
	if err := executeScript(badArgState, "-", nil, strings.NewReader("")); err == nil {
		// 非 table arg 必须阻止脚本继续执行。
		t.Fatalf("executeScript should reject non-table arg")
	}
}

// TestRunExecutesImplicitStdinPipe 验证无脚本参数时会执行管道 stdin。
//
// 官方 Lua 在 `echo "chunk" | lua` 场景会把 stdin 当作脚本；这里用非法 chunk 断言
// Run 确实尝试读取并编译 stdin，而不是静默空操作。
func TestRunExecutesImplicitStdinPipe(t *testing.T) {
	// 非 os.File reader 被视为管道输入，应触发隐式 stdin 执行。
	err := Run(context.Background(), nil, Streams{Stdin: strings.NewReader("local =\n")})
	if err == nil {
		// 若没有错误，说明 stdin 没有被执行或语法错误未传播。
		t.Fatalf("Run implicit stdin should return syntax error")
	}
	if !strings.Contains(err.Error(), "syntax error") {
		// 隐式 stdin 执行非法源码时应暴露 parser 语法错误。
		t.Fatalf("Run implicit stdin error = %v, want syntax error", err)
	}
}

// TestExecuteStartupInit 验证 LUA_INIT 启动脚本执行。
//
// 启动脚本需要能读取已经建立的 arg 表；版本专用 LUA_INIT_5_3 优先于通用 LUA_INIT。
func TestExecuteStartupInit(t *testing.T) {
	// 创建 State 并写入脚本参数，模拟 Run 中 LUA_INIT 前的启动顺序。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := setArgTable(state, "script.lua", []string{"3.2"}, nil); err != nil {
		// arg 表写入不应失败。
		t.Fatalf("setArgTable failed: %v", err)
	}

	t.Setenv("LUA_INIT", "X=tonumber(arg[1])")
	if err := executeStartupInit(state); err != nil {
		// LUA_INIT 源码片段应可执行。
		t.Fatalf("executeStartupInit failed: %v", err)
	}
	xValue, err := lua.GetGlobal(state, "X")
	if err != nil {
		// 全局 X 应可读取。
		t.Fatalf("GetGlobal X failed: %v", err)
	}
	if xValue.Kind != runtime.KindNumber || xValue.Number != 3.2 {
		// LUA_INIT 必须能读取 arg[1] 并写入 X。
		t.Fatalf("X = %#v, want 3.2", xValue)
	}

	t.Setenv("LUA_INIT_5_3", "X=10")
	if err := executeStartupInit(state); err != nil {
		// 版本专用初始化片段应可执行。
		t.Fatalf("executeStartupInit versioned failed: %v", err)
	}
	xValue, err = lua.GetGlobal(state, "X")
	if err != nil {
		// 版本专用执行后仍应能读取 X。
		t.Fatalf("GetGlobal versioned X failed: %v", err)
	}
	if xValue.Kind != runtime.KindInteger || xValue.Integer != 10 {
		// LUA_INIT_5_3 必须优先于 LUA_INIT。
		t.Fatalf("versioned X = %#v, want 10", xValue)
	}

	initFile := filepath.Join(t.TempDir(), "init.lua")
	if err := os.WriteFile(initFile, []byte("Y=11\n"), 0o600); err != nil {
		// 测试初始化文件必须可写。
		t.Fatalf("WriteFile init failed: %v", err)
	}
	t.Setenv("LUA_INIT_5_3", "@"+initFile)
	if err := executeStartupInit(state); err != nil {
		// @file 形式应按 Lua 文件执行。
		t.Fatalf("executeStartupInit file failed: %v", err)
	}
	yValue, err := lua.GetGlobal(state, "Y")
	if err != nil {
		// 文件初始化后应能读取 Y。
		t.Fatalf("GetGlobal Y failed: %v", err)
	}
	if yValue.Kind != runtime.KindInteger || yValue.Integer != 11 {
		// @file 初始化必须写入全局 Y。
		t.Fatalf("Y = %#v, want 11", yValue)
	}

	t.Setenv("LUA_INIT_5_3", "error('msg')")
	err = executeStartupInit(state)
	if err == nil || !strings.Contains(err.Error(), "LUA_INIT:1: msg") {
		// 字符串形式初始化错误必须带官方 CLI 兼容前缀。
		t.Fatalf("executeStartupInit error = %v, want LUA_INIT prefix", err)
	}
}

// TestRunIgnoreEnvironmentOption 验证 -E 忽略 Lua 启动环境变量。
//
// -E 必须同时屏蔽 LUA_INIT 和 package path/cpath 环境变量，避免宿主环境影响脚本启动。
func TestRunIgnoreEnvironmentOption(t *testing.T) {
	// 设置会破坏启动的环境变量；-E 生效时这些变量都应被忽略。
	t.Setenv("LUA_INIT", "error('should ignore')")
	t.Setenv("LUA_PATH", "xxx")
	t.Setenv("LUA_CPATH", "xxx")

	scriptPath := filepath.Join(t.TempDir(), "script.lua")
	script := "assert(not string.find(package.path, 'xxx', 1, true)); assert(not string.find(package.cpath, 'xxx', 1, true))\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		// 测试脚本写入不应失败。
		t.Fatalf("WriteFile script failed: %v", err)
	}
	if err := Run(context.Background(), []string{"-E", scriptPath}, Streams{}); err != nil {
		// -E 应屏蔽环境变量，使脚本正常执行。
		t.Fatalf("Run -E failed: %v", err)
	}
}

// TestShouldExecuteImplicitStdin 验证 stdin 隐式执行判定边界。
//
// helper 必须只在无显式脚本且输入可读时启用，避免 -e、-l、-i 或脚本文件场景误读 stdin。
func TestShouldExecuteImplicitStdin(t *testing.T) {
	// 无脚本且 reader 非 nil 时可执行隐式 stdin。
	if !shouldExecuteImplicitStdin(Options{}, strings.NewReader("print(1)")) {
		// 管道 reader 应被视为可执行输入。
		t.Fatalf("implicit stdin should be enabled for reader")
	}
	for _, options := range []Options{
		{ScriptPath: "script.lua"},
		{Interactive: true},
		{Expressions: []string{"print(1)"}},
		{Libraries: []string{"mod"}},
	} {
		// 任一显式执行模式都不应隐式读取 stdin。
		if shouldExecuteImplicitStdin(options, strings.NewReader("print(1)")) {
			// 误启用会改变官方选项的执行边界。
			t.Fatalf("implicit stdin should be disabled for options %#v", options)
		}
	}
	if shouldExecuteImplicitStdin(Options{}, nil) {
		// nil stdin 不能读取，也不能隐式执行。
		t.Fatalf("implicit stdin should be disabled for nil stdin")
	}
}

// TestIsIncompleteREPLSourceIgnoresCommentEquals 验证 REPL 续行判断忽略短注释内容。
//
// 官方交互测试包含 `(6*2-6) -- ===`；注释中的等号不能被误判成赋值缺少右侧表达式。
func TestIsIncompleteREPLSourceIgnoresCommentEquals(t *testing.T) {
	if isIncompleteREPLSource("(6*2-6) -- ===") {
		// 注释尾部的等号不应触发续行。
		t.Fatalf("comment equals should not be incomplete")
	}
	if !isIncompleteREPLSource("a =") {
		// 普通赋值缺少右侧表达式仍应触发续行。
		t.Fatalf("assignment should be incomplete")
	}
	if isIncompleteREPLSource("local =") {
		// 非法 local 语句应交给 parser 报错，不能被续行吞掉。
		t.Fatalf("invalid local should not be treated as incomplete")
	}
}

// TestSetArgTable 验证脚本参数 arg 表。
//
// arg[0] 必须保存脚本路径，arg[1..n] 必须按顺序保存脚本参数，负索引保存脚本名前启动参数。
func TestSetArgTable(t *testing.T) {
	// 创建 State 并写入 arg 表。
	state := lua.NewState()
	defer state.Close()
	if err := setArgTable(state, "script.lua", []string{"a", "b"}, []string{"-e ", "--"}); err != nil {
		// 有效脚本参数写入不应失败。
		t.Fatalf("setArgTable failed: %v", err)
	}
	argValue, err := lua.GetGlobal(state, "arg")
	if err != nil {
		// arg 全局读取不应失败。
		t.Fatalf("GetGlobal arg failed: %v", err)
	}
	argTable := argValue.Ref.(*runtime.Table)
	if value := argTable.RawGetInteger(-1); value.Kind != runtime.KindString || value.String != "--" {
		// arg[-1] 必须是最靠近脚本名的启动参数。
		t.Fatalf("arg[-1] = %#v", value)
	}
	if value := argTable.RawGetInteger(-2); value.Kind != runtime.KindString || value.String != "-e " {
		// arg[-2] 必须继续向前保存启动参数。
		t.Fatalf("arg[-2] = %#v", value)
	}
	if value := argTable.RawGetInteger(-3); value.Kind != runtime.KindString || value.String == "" {
		// arg[-3] 必须保留解释器路径，官方 main.lua 会用它重启当前 lua。
		t.Fatalf("arg[-3] = %#v", value)
	}
	if value := argTable.RawGetInteger(0); value.String != "script.lua" {
		// arg[0] 必须是脚本路径。
		t.Fatalf("arg[0] = %#v", value)
	}
	if value := argTable.RawGetInteger(1); value.String != "a" {
		// arg[1] 必须是第一个脚本参数。
		t.Fatalf("arg[1] = %#v", value)
	}
	if value := argTable.RawGetInteger(2); value.String != "b" {
		// arg[2] 必须是第二个脚本参数。
		t.Fatalf("arg[2] = %#v", value)
	}
}

// TestMainInteractiveMode 验证 -i 第一阶段控制流。
//
// 当前 -i 进入 REPL 读取 stdin；合法输入应完成执行且不写 stderr。
func TestMainInteractiveMode(t *testing.T) {
	// 使用 bytes.Buffer 捕获 stdout/stderr，避免测试污染真实终端。
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main(context.Background(), []string{"-i"}, Streams{
		Stdin:  strings.NewReader("return 1\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if exitCode != ExitOK {
		// -i 占位模式不应返回失败退出码。
		t.Fatalf("exit code = %d stderr=%q", exitCode, stderr.String())
	}
	if stdout.String() != "> > " {
		// REPL 必须输出每次读取前的主提示符。
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		// 合法输入成功执行后不应写入 stderr。
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestREPLMultilineCompletion 验证 REPL 多行补全。
//
// function/end 跨行输入时，第一行后必须输出续行提示符，直到块关闭才执行。
func TestREPLMultilineCompletion(t *testing.T) {
	// 创建 State 并运行包含 function/end 的多行 REPL 输入。
	state := lua.NewState()
	defer state.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runREPL(state, Streams{
		Stdin:  strings.NewReader("function f()\nend\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		// 正常 EOF 不应让 REPL 返回错误。
		t.Fatalf("runREPL failed: %v", err)
	}
	if stdout.String() != "> >> > " {
		// 第一行未闭合块应触发续行提示符。
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		// 多行 chunk 补全后应成功执行，不再报告阶段性执行错误。
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestREPLErrorRecovery 验证 REPL 错误恢复。
//
// 语法错误写入 stderr 后，后续输入仍应继续读取和执行。
func TestREPLErrorRecovery(t *testing.T) {
	// 第一行是语法错误，第二行是可编译 chunk。
	state := lua.NewState()
	defer state.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runREPL(state, Streams{
		Stdin:  strings.NewReader("local =\nreturn 1\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		// REPL 应从 chunk 错误中恢复，不返回错误。
		t.Fatalf("runREPL failed: %v", err)
	}
	if stdout.String() != "> > > " {
		// 两条输入加 EOF 前提示符都应写入 stdout。
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "syntax error") {
		// 第一条语法错误必须写入 stderr。
		t.Fatalf("stderr missing syntax error: %q", stderr.String())
	}
}

// TestREPLExpressionPrintFailure 验证 REPL 表达式回显依赖当前全局 print。
//
// 官方 Lua 在交互模式下会通过全局 print 输出表达式结果；print 被改成非函数时必须把
// `error calling 'print'` 写入 stderr，并继续保持 REPL 错误恢复语义。
func TestREPLExpressionPrintFailure(t *testing.T) {
	// 打开标准库后故意破坏 print，模拟官方 main.lua 中的交互模式回归用例。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := lua.DoString(state, "print=nil"); err != nil {
		// 破坏 print 的启动片段不应失败。
		t.Fatalf("disable print failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runREPL(state, Streams{
		Stdin:  strings.NewReader("10\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		// REPL 应捕获单条输入错误并继续到 EOF。
		t.Fatalf("runREPL failed: %v", err)
	}
	if stdout.String() != "> > " {
		// REPL 仍然必须正常输出主提示符。
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "error calling 'print'") {
		// print 不可调用时错误文本必须匹配官方测试检查。
		t.Fatalf("stderr = %q, want print failure", stderr.String())
	}
}

// TestREPLPromptShortcutAndContinuation 验证官方交互模式依赖的提示符与输入补全。
//
// `_PROMPT/_PROMPT2` 为空时不应输出提示符；`a =` 后换行应继续补全；`=a` 必须按表达式回显。
func TestREPLPromptShortcutAndContinuation(t *testing.T) {
	// 使用自定义 print 捕获 REPL 表达式回显，避免测试依赖真实 os.Stdout。
	state := lua.NewState()
	defer state.Close()
	state.SetGlobal("_PROMPT", runtime.StringValue(""))
	state.SetGlobal("_PROMPT2", runtime.StringValue(""))
	var printed []runtime.Value
	if err := lua.Register(state, "print", func(args ...runtime.Value) ([]runtime.Value, error) {
		// 捕获所有 print 参数，便于断言 =a 的回显结果。
		printed = append(printed, args...)
		return nil, nil
	}); err != nil {
		// 注册测试 print 不应失败。
		t.Fatalf("Register print failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runREPL(state, Streams{
		Stdin:  strings.NewReader("a =\n10\n=a\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		// 补全赋值和快捷回显不应让 REPL 失败。
		t.Fatalf("runREPL failed: %v", err)
	}
	if stdout.Len() != 0 {
		// 空提示符配置下 stdout 不应出现默认提示符。
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		// 合法跨行赋值和 =a 快捷表达式不应报错。
		t.Fatalf("stderr = %q", stderr.String())
	}
	if len(printed) != 1 || printed[0].Kind != runtime.KindInteger || printed[0].Integer != 10 {
		// =a 必须回显赋值后的整数 10。
		t.Fatalf("printed = %#v, want 10", printed)
	}
}

// TestREPLExpressionCommentEcho 验证 REPL 对带短注释表达式的回显。
//
// 官方 main.lua 使用 `(6*2-6) -- ===` 检查交互解释器；短注释不能让表达式结果退化为 nil。
func TestREPLExpressionCommentEcho(t *testing.T) {
	// 注册自定义 print 捕获表达式结果。
	state := lua.NewState()
	defer state.Close()
	var printed []runtime.Value
	if err := lua.Register(state, "print", func(args ...runtime.Value) ([]runtime.Value, error) {
		// 捕获 print 参数用于断言。
		printed = append(printed, args...)
		return nil, nil
	}); err != nil {
		// 注册测试 print 不应失败。
		t.Fatalf("Register print failed: %v", err)
	}
	if err := executeREPLChunk(state, "(6*2-6) -- ==="); err != nil {
		// 带短注释表达式应按表达式路径成功执行。
		t.Fatalf("executeREPLChunk failed: %v", err)
	}
	if len(printed) != 1 || printed[0].Kind != runtime.KindInteger || printed[0].Integer != 6 {
		// 表达式结果必须是整数 6。
		t.Fatalf("printed = %#v, want 6", printed)
	}
}

// TestREPLLongStringContinuation 验证 REPL 会等待长字符串闭合。
//
// 官方 main.lua 会在交互模式输入 `a = [[...]]`，随后用 `=a` 回显完整多行字符串。
func TestREPLLongStringContinuation(t *testing.T) {
	// 使用自定义 print 捕获长字符串回显结果。
	state := lua.NewState()
	defer state.Close()
	state.SetGlobal("_PROMPT", runtime.StringValue(""))
	state.SetGlobal("_PROMPT2", runtime.StringValue(""))
	var printed []runtime.Value
	if err := lua.Register(state, "print", func(args ...runtime.Value) ([]runtime.Value, error) {
		// 捕获 print 参数用于断言。
		printed = append(printed, args...)
		return nil, nil
	}); err != nil {
		// 注册测试 print 不应失败。
		t.Fatalf("Register print failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runREPL(state, Streams{
		Stdin:  strings.NewReader("a = [[b\nc\nd\ne]]\n=a\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		// 长字符串补全不应让 REPL 失败。
		t.Fatalf("runREPL failed: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		// 空提示符下不应输出提示符，合法输入也不应输出错误。
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(printed) != 1 || printed[0].Kind != runtime.KindString || printed[0].String != "b\nc\nd\ne" {
		// =a 必须回显完整长字符串内容。
		t.Fatalf("printed = %#v, want long string", printed)
	}
}

// TestREPLRunsWhitespaceSplitChunk 验证 REPL 能执行官方 main.lua 的跨换行 chunk。
//
// 官方测试会把一个函数定义和 return 调用中的空格全部替换为换行；REPL 必须累计到 chunk 完整后
// 再执行，并回显显式 return 的返回值。
func TestREPLRunsWhitespaceSplitChunk(t *testing.T) {
	// 打开标准库并覆盖 print，保留 assert/tostring 等基础函数。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	state.SetGlobal("_PROMPT", runtime.StringValue(""))
	state.SetGlobal("_PROMPT2", runtime.StringValue(""))
	var printed [][]runtime.Value
	if err := lua.Register(state, "print", func(args ...runtime.Value) ([]runtime.Value, error) {
		// 每次回显单独保存，便于验证多返回值。
		printed = append(printed, append([]runtime.Value(nil), args...))
		return nil, nil
	}); err != nil {
		// 注册测试 print 不应失败。
		t.Fatalf("Register print failed: %v", err)
	}
	source := ` --
function f ( x )
  local a = [[
xuxu
]]
  local b = "\
xuxu\n"
  if x == 11 then return 1 + 12 , 2 + 20 end
  return x + 1
end
return( f( 100 ) )
assert( a == b )
do return f( 11 ) end`
	source = strings.ReplaceAll(source, " ", "\n\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runREPL(state, Streams{Stdin: strings.NewReader(source), Stdout: &stdout, Stderr: &stderr}); err != nil {
		// 官方同构输入不应让 REPL 失败。
		t.Fatalf("runREPL failed: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		// 空提示符下不应输出提示符，合法输入也不应报错。
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(printed) != 2 {
		// 两个显式 return chunk 都应回显。
		t.Fatalf("printed calls = %#v", printed)
	}
	if len(printed[0]) != 1 || printed[0][0].Kind != runtime.KindInteger || printed[0][0].Integer != 101 {
		// return(f(100)) 应回显 101。
		t.Fatalf("first printed = %#v", printed[0])
	}
	if len(printed[1]) != 2 || printed[1][0].Integer != 13 || printed[1][1].Integer != 22 {
		// do return f(11) end 应回显两个返回值。
		t.Fatalf("second printed = %#v", printed[1])
	}
}

// TestExitCodeForError 验证 CLI 错误退出码映射。
//
// 当前阶段 nil 返回 0，语法、执行器未接入和普通错误都返回 1。
func TestExitCodeForError(t *testing.T) {
	if exitCode := exitCodeForError(nil); exitCode != ExitOK {
		// nil 错误必须映射成功退出。
		t.Fatalf("nil exit code = %d", exitCode)
	}
	if exitCode := exitCodeForError(lua.ErrExecutionUnavailable); exitCode != ExitFailure {
		// 执行器未接入属于运行失败。
		t.Fatalf("execution unavailable exit code = %d", exitCode)
	}
	if exitCode := exitCodeForError(errors.New("plain")); exitCode != ExitFailure {
		// 普通错误也应映射失败退出。
		t.Fatalf("plain error exit code = %d", exitCode)
	}
}

// TestMainMapsOSExitWithoutStderr 验证脚本中的 os.exit 会映射为真实 CLI 退出码。
//
// 官方 main.lua 要求 os.exit(0,true) 成功且不输出错误文本，非零退出也只返回状态码。
func TestMainMapsOSExitWithoutStderr(t *testing.T) {
	tests := []struct {
		name string
		code string
		want int
	}{
		{name: "zero", code: "os.exit(0, true)", want: ExitOK},
		{name: "true", code: "os.exit(true, true)", want: ExitOK},
		{name: "false", code: "os.exit(false, true)", want: ExitFailure},
		{name: "integer", code: "os.exit(7, true)", want: 7},
	}
	for _, tt := range tests {
		// 每个子用例独立捕获 stderr，确保 os.exit 不走普通错误打印。
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			exitCode := Main(context.Background(), []string{"-e", tt.code}, Streams{Stderr: &stderr})
			if exitCode != tt.want {
				// os.exit 的错误对象必须被 CLI 转为指定进程退出码。
				t.Fatalf("exit code = %d, want %d stderr=%q", exitCode, tt.want, stderr.String())
			}
			if stderr.Len() != 0 {
				// 官方 CLI 对 os.exit 不输出错误文本。
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

// TestLuaErrorTextUsesToStringMetamethod 验证 CLI 会按 tostring 语义展示 Lua error object。
//
// 官方 main.lua 会 error(table)，并依赖 table 的 `__tostring` 元方法生成命令行 stderr 文本。
func TestLuaErrorTextUsesToStringMetamethod(t *testing.T) {
	// 构造带 __tostring 的 table 作为 Lua error object。
	errorTable := runtime.NewTable()
	metatable := runtime.NewTable()
	metatable.RawSetString("__tostring", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回稳定字符串，模拟 Lua 侧 __tostring。
		return []runtime.Value{runtime.StringValue("converted")}, nil
	})))
	errorTable.SetMetatable(metatable)
	err := lua.RaiseError(runtime.ReferenceValue(runtime.KindTable, errorTable))
	if got := luaErrorText(err); got != "converted" {
		// CLI 必须优先展示 __tostring 的返回值。
		t.Fatalf("luaErrorText = %q, want converted", got)
	}
	err = lua.RaiseError(runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()))
	if got := luaErrorText(err); got != "error object is a table value" {
		// 没有 __tostring 的 table error object 必须使用 Lua 5.3 CLI 固定文本。
		t.Fatalf("luaErrorText table = %q", got)
	}
}

// TestExecuteScriptNormalizesLuaErrorObject 验证脚本错误对象会在 State 存活时转换为字符串。
//
// Lua closure 形式的 `__tostring` 需要通过当前 State 调用；这覆盖官方 main.lua 的
// `error(table)` 命令行诊断路径。
func TestExecuteScriptNormalizesLuaErrorObject(t *testing.T) {
	// 创建 State 并打开基础库，确保 setmetatable 与 error 可用。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	script := `
local value = {}
setmetatable(value, {__tostring = function()
  return "converted"
end})
error(value)
`
	err := executeScript(state, "-", nil, strings.NewReader(script))
	if err == nil {
		// error(value) 必须传播运行期错误。
		t.Fatalf("executeScript should fail")
	}
	if got := luaErrorText(err); got != "converted" {
		// CLI 错误文本必须来自 Lua closure __tostring。
		t.Fatalf("luaErrorText = %q, want converted", got)
	}
}

// TestMainFormatsTableErrorObjectDebugLevel 验证 CLI 错误对象 tostring 的 debug 层级。
//
// 官方 main.lua 在 table `__tostring` 中读取 `debug.getinfo(4).currentline`，期望该层级指向
// 原脚本 `error(m)` 所在行；若 CLI 合成错误处理帧数量不对，会得到 -1 或错误行号。
func TestMainFormatsTableErrorObjectDebugLevel(t *testing.T) {
	// 临时脚本的第 6 行必须是 error(m)，与官方 main.lua 断言一致。
	scriptPath := filepath.Join(t.TempDir(), "error_object.lua")
	source := `debug = require "debug"
m = {x=0}
setmetatable(m, {__tostring = function(x)
  return tostring(debug.getinfo(4).currentline + x.x)
end})
error(m)
`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		// 测试夹具写入失败时无法验证 CLI 路径。
		t.Fatalf("write script failed: %v", err)
	}

	var stderr bytes.Buffer
	exitCode := Main(context.Background(), []string{scriptPath}, Streams{Stderr: &stderr})
	if exitCode != ExitFailure {
		// error(m) 必须映射为普通运行失败。
		t.Fatalf("exit code = %d stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), ": 6\n") {
		// stderr 需要包含 __tostring 根据原脚本行号生成的文本。
		t.Fatalf("stderr = %q, want line 6", stderr.String())
	}
}

// TestMainVersionOutput 验证 -v 版本输出。
//
// -v 必须写 stdout，且没有其他任务时成功退出。
func TestMainVersionOutput(t *testing.T) {
	// 使用 stdout buffer 捕获版本文本。
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main(context.Background(), []string{"-v"}, Streams{Stdout: &stdout, Stderr: &stderr})
	if exitCode != ExitOK {
		// 版本输出不应失败。
		t.Fatalf("exit code = %d stderr=%q", exitCode, stderr.String())
	}
	if stdout.String() != VersionText+"\n" {
		// 版本输出必须稳定。
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		// 成功路径不应写 stderr。
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestMainReturnsFailureOnParseError 验证 CLI 参数错误退出码。
//
// 未知选项应写入 stderr 并返回 ExitFailure。
func TestMainReturnsFailureOnParseError(t *testing.T) {
	// 使用 stderr buffer 捕获错误文本。
	var stderr bytes.Buffer
	exitCode := Main(context.Background(), []string{"-unknown"}, Streams{Stderr: &stderr})
	if exitCode != ExitFailure {
		// 参数错误必须返回失败退出码。
		t.Fatalf("exit code = %d", exitCode)
	}
	if stderr.Len() == 0 {
		// 失败路径必须输出错误信息。
		t.Fatalf("stderr should not be empty")
	}
}

// TestMainInvalidOptionMessagesMatchLua 验证官方测试依赖的参数错误关键文本。
//
// Lua 5.3 main.lua 使用 string.find 匹配这些 stderr 子串，文本漂移会导致官方测试失败。
func TestMainInvalidOptionMessagesMatchLua(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"-h"}, want: "unrecognized option '-h'"},
		{args: []string{"---"}, want: "unrecognized option '---'"},
		{args: []string{"-e"}, want: "'-e' needs argument"},
		{args: []string{"-e", "a"}, want: "syntax error"},
		{args: []string{"-l"}, want: "'-l' needs argument"},
	}
	for _, tt := range tests {
		// 每个参数组合独立捕获 stderr，避免错误文本互相污染。
		var stderr bytes.Buffer
		exitCode := Main(context.Background(), tt.args, Streams{Stderr: &stderr})
		if exitCode == ExitOK {
			// 非法参数或非法表达式必须失败退出。
			t.Fatalf("Main(%#v) exit code = %d, want failure", tt.args, exitCode)
		}
		if !strings.Contains(stderr.String(), tt.want) {
			// stderr 必须包含官方测试匹配的关键子串。
			t.Fatalf("Main(%#v) stderr = %q, want contains %q", tt.args, stderr.String(), tt.want)
		}
	}
}

// TestMainScriptSyntaxErrorPrintsCompactSourceMessage 验证脚本语法错误直接输出源码定位主消息。
//
// CLI 不再额外拼接程序名和 parser 内部 `parse error` 文本；错误本身携带 path:line:column。
func TestMainScriptSyntaxErrorPrintsCompactSourceMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.lua")
	if err := os.WriteFile(path, []byte("local a = 1\n\ne"), 0o600); err != nil {
		// 测试夹具必须可写入。
		t.Fatalf("write fixture failed: %v", err)
	}

	var stderr bytes.Buffer
	exitCode := Main(context.Background(), []string{path}, Streams{Stderr: &stderr})
	if exitCode != ExitFailure {
		// 脚本语法错误必须失败退出。
		t.Fatalf("exit code = %d, want failure", exitCode)
	}
	want := path + ":3:2: syntax error near <eof>\n" +
		"   3 | e\n" +
		`       |  ^ expected assignment operator "=" or function call arguments` + "\n"
	if stderr.String() != want {
		// stderr 必须只包含可直接展示的主错误。
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}
