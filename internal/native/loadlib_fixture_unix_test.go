//go:build native_modules && (linux || darwin)

package native

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	glualua "github.com/zing/go-lua-vm/lua"
	luaruntime "github.com/zing/go-lua-vm/runtime"
	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// TestUnixPackageLoadLibResolvesNativeFixture 验证 package.loadlib 可解析真实 Lua C 模块入口。
func TestUnixPackageLoadLibResolvesNativeFixture(t *testing.T) {
	// 构建使用 Lua 5.3 public C API 的 smoke 模块 fixture。
	fixturePath := buildUnixNativeFixture(t)
	environment := packagelib.NewEnvironmentWithLoaders(nil, Loader())

	values, err := environment.LoadLib(
		luaruntime.StringValue(fixturePath),
		luaruntime.StringValue("luaopen_glua_native_smoke"),
	)
	if err != nil {
		// 合法 loadlib 参数和 native loader 失败边界必须通过三返回表达，不应抛 Go error。
		t.Fatalf("LoadLib returned error: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[2].String != "init" {
		// 当前尚未实现 C API shim，因此解析成功后应停在 init 分类，而不是返回 callable。
		t.Fatalf("LoadLib fixture values = %#v, want nil,message,init", values)
	}
	if !strings.Contains(values[1].String, "shim is not implemented") {
		// 错误文本必须明确已经越过动态库解析，阻塞点是 Lua C API shim。
		t.Fatalf("LoadLib fixture message = %q, want shim boundary", values[1].String)
	}
}

// TestUnixPackageLoadLibReturnsCallableNativeFixture 验证 state-aware loader 可调用 luaopen_*。
func TestUnixPackageLoadLibReturnsCallableNativeFixture(t *testing.T) {
	// fixture 的 luaopen_* 会通过 luaL_newlib 返回包含 C function 的模块 table。
	fixturePath := buildUnixNativeFixture(t)
	state := luaruntime.NewState()
	defer state.Close()
	environment := packagelib.NewEnvironmentWithLoaders(nil, LoaderForState(state))

	values, err := environment.LoadLib(
		luaruntime.StringValue(fixturePath),
		luaruntime.StringValue("luaopen_glua_native_smoke"),
	)
	if err != nil {
		// 合法 loadlib 参数和 native loader 成功路径不应返回 Go error。
		t.Fatalf("LoadLib state-aware fixture returned error: %v", err)
	}
	if len(values) != 1 || values[0].Kind != luaruntime.KindGoClosure {
		// state-aware loader 成功时必须只返回可调用 Lua loader。
		t.Fatalf("LoadLib state-aware fixture values = %#v, want callable", values)
	}
	loader, ok := values[0].Ref.(luaruntime.GoResultsFunction)
	if !ok || loader == nil {
		// 当前 native luaopen_* 通过 GoResultsFunction 进入 VM 调用通道。
		t.Fatalf("LoadLib state-aware fixture payload = %#v, want GoResultsFunction", values[0].Ref)
	}
	results, err := loader(luaruntime.StringValue("glua_native_smoke"), luaruntime.StringValue(fixturePath))
	if err != nil {
		// fixture luaopen_* 只使用当前已实现的 public C API，调用不应失败。
		t.Fatalf("state-aware native loader call failed: %v", err)
	}
	if len(results) != 1 {
		// luaopen_* 必须返回一个模块 table。
		t.Fatalf("state-aware native loader results = %#v, want one module table", results)
	}
	assertNativeSmokeModule(t, results[0])
	if got := state.StackTop(); got != 0 {
		// loader 调用期间压入的 require 参数必须恢复干净。
		t.Fatalf("state-aware native loader stack top = %d, want 0", got)
	}
}

// TestUnixRequireLoadsNativeFixtureThroughCPath 验证 Lua 侧 require 可通过 package.cpath 加载原生模块。
func TestUnixRequireLoadsNativeFixtureThroughCPath(t *testing.T) {
	// 构建真实 Lua C 动态库，并让标准 package.searchers[3] 通过 cpath 命中它。
	fixturePath := buildUnixNativeFixture(t)
	cpathPattern := filepath.Join(filepath.Dir(fixturePath), "?"+dynamicLibraryExtension())
	missingLuaPattern := filepath.Join(t.TempDir(), "missing", "?.lua")
	state := glualua.NewStateWithOptions(glualua.Options{
		AllowHostFilesystem: true,
		PackageDynamicLibraryLoaderForState: func(loaderState *luaruntime.State) func(filename string, symbol string) (luaruntime.Value, error) {
			// native loader 必须绑定当前 State，确保 luaopen_* 与后续 C function 看到同一个 VM 栈。
			return LoaderForState(loaderState)
		},
	})
	defer state.Close()
	if err := glualua.OpenLibs(state); err != nil {
		// require 依赖 package/base 标准库完成注册。
		t.Fatalf("OpenLibs native require failed: %v", err)
	}

	source := loadNativeSmokeLuaFixture(t, cpathPattern, missingLuaPattern)
	if err := glualua.DoString(state, source); err != nil {
		// Lua 侧 require、模块函数调用和 require 缓存都必须成功。
		t.Fatalf("native require script failed: %v", err)
	}
}

// TestUnixRequireCapturesNativeFixtureOpenError 验证 pcall(require) 可捕获 C module 初始化错误。
func TestUnixRequireCapturesNativeFixtureOpenError(t *testing.T) {
	// 构建文件名与模块名匹配的动态库，确保 require 走标准 cpath 和 luaopen_* 符号生成规则。
	fixturePath := buildUnixNativeFixtureModule(t, "glua_native_failopen")
	cpathPattern := filepath.Join(filepath.Dir(fixturePath), "?"+dynamicLibraryExtension())
	missingLuaPattern := filepath.Join(t.TempDir(), "missing", "?.lua")
	state := glualua.NewStateWithOptions(glualua.Options{
		AllowHostFilesystem: true,
		PackageDynamicLibraryLoaderForState: func(loaderState *luaruntime.State) func(filename string, symbol string) (luaruntime.Value, error) {
			// 初始化失败仍必须绑定当前 State，否则 luaL_error 无法转换为当前 VM 的 runtime error。
			return LoaderForState(loaderState)
		},
	})
	defer state.Close()
	if err := glualua.OpenLibs(state); err != nil {
		// require 和 pcall 依赖 base/package 标准库。
		t.Fatalf("OpenLibs native failopen require failed: %v", err)
	}

	source := `
package.path = ` + nativeLuaStringLiteral(missingLuaPattern) + `
package.cpath = ` + nativeLuaStringLiteral(cpathPattern) + `
local ok, message = pcall(require, "glua_native_failopen")
assert(ok == false, "require unexpectedly succeeded")
assert(string.find(message, "native open failure", 1, true), message)
assert(package.loaded["glua_native_failopen"] == nil)
`
	if err := glualua.DoString(state, source); err != nil {
		// pcall(require) 必须捕获 luaopen_* 中的 luaL_error，而不是让 chunk 失败。
		t.Fatalf("native failopen require script failed: %v", err)
	}
}

// assertNativeSmokeModule 验证 fixture 模块 table 及其中 C function 的调用语义。
func assertNativeSmokeModule(t *testing.T, value luaruntime.Value) {
	// 先确认 luaopen_* 的返回值是 table，再逐个调用由 luaL_newlib 注册的函数。
	t.Helper()
	table := nativeSmokeModuleTable(t, value)

	add := nativeSmokeFunction(t, table, "add")
	addResults, err := add(luaruntime.IntegerValue(20), luaruntime.IntegerValue(22))
	if err != nil {
		// add 只读取两个 integer 参数并返回一个 integer，不应触发错误。
		t.Fatalf("native smoke add failed: %v", err)
	}
	if len(addResults) != 1 || !addResults[0].RawEqual(luaruntime.IntegerValue(42)) {
		// add 的结果可证明 luaL_checkinteger 与 lua_pushinteger 已贯通。
		t.Fatalf("native smoke add results = %#v, want 42", addResults)
	}

	echo := nativeSmokeFunction(t, table, "echo")
	echoResults, err := echo(luaruntime.StringValue("hello"))
	if err != nil {
		// echo 只读取 string 参数并原样返回，不应触发错误。
		t.Fatalf("native smoke echo failed: %v", err)
	}
	if len(echoResults) != 1 || !echoResults[0].RawEqual(luaruntime.StringValue("hello")) {
		// echo 的结果可证明 luaL_checklstring 与 lua_pushlstring 已贯通。
		t.Fatalf("native smoke echo results = %#v, want hello", echoResults)
	}

	multi := nativeSmokeFunction(t, table, "multi")
	multiResults, err := multi()
	if err != nil {
		// multi 不读取参数，只返回三值，不应触发错误。
		t.Fatalf("native smoke multi failed: %v", err)
	}
	if len(multiResults) != 3 ||
		!multiResults[0].RawEqual(luaruntime.IntegerValue(1)) ||
		!multiResults[1].RawEqual(luaruntime.StringValue("two")) ||
		!multiResults[2].RawEqual(luaruntime.BooleanValue(true)) {
		// multi 的结果可证明 C function 多返回值能回到 Go VM。
		t.Fatalf("native smoke multi results = %#v, want 1,two,true", multiResults)
	}

	fail := nativeSmokeFunction(t, table, "fail")
	_, failErr := fail(luaruntime.StringValue("boom"))
	if failErr == nil {
		// fail 使用 luaL_error，必须转换为 Go VM runtime error 而不是伪装成功。
		t.Fatalf("native smoke fail returned nil error")
	}
	if !errors.Is(failErr, luaruntime.ErrLuaError) {
		// 错误链需要保留 Lua error 分类，供 pcall/xpcall 捕获。
		t.Fatalf("native smoke fail error = %v, want lua error classification", failErr)
	}

	raise := nativeSmokeFunction(t, table, "raise")
	_, raiseErr := raise()
	if raiseErr == nil {
		// raise 使用 lua_error，必须把栈顶对象作为 Lua error object 传回 VM。
		t.Fatalf("native smoke raise returned nil error")
	}
	var runtimeErr *luaruntime.RuntimeError
	if !errors.As(raiseErr, &runtimeErr) || !runtimeErr.Object.RawEqual(luaruntime.StringValue("native lua_error object")) {
		// Go 侧直接调用 C function wrapper 时也应能读取原始 Lua error object。
		t.Fatalf("native smoke raise error = %#v, want native lua_error object", raiseErr)
	}
}

// nativeSmokeModuleTable 从 fixture 模块返回值读取 table 引用。
func nativeSmokeModuleTable(t *testing.T, value luaruntime.Value) *luaruntime.Table {
	// 模块入口必须返回 table；其他类型说明 luaopen_* 结果搬运错误。
	t.Helper()
	table, ok := value.Ref.(*luaruntime.Table)
	if value.Kind != luaruntime.KindTable || !ok || table == nil {
		// 这里直接失败，避免后续字段断言产生误导。
		t.Fatalf("native smoke module = %#v, want table", value)
	}
	return table
}

// nativeSmokeFunction 从 fixture 模块 table 读取指定 C function。
func nativeSmokeFunction(t *testing.T, table *luaruntime.Table, name string) luaruntime.GoResultsFunction {
	// luaL_newlib 应把 luaL_Reg 中的函数注册成当前 VM 可调用的 Go closure。
	t.Helper()
	value := table.RawGetString(name)
	function, ok := value.Ref.(luaruntime.GoResultsFunction)
	if value.Kind != luaruntime.KindGoClosure || !ok || function == nil {
		// 缺失函数表示 luaL_setfuncs 或 C function wrapper 退化。
		t.Fatalf("native smoke function %s = %#v, want GoResultsFunction", name, value)
	}
	return function
}

// nativeLuaStringLiteral 返回可嵌入测试 Lua 源码的字符串字面量。
func nativeLuaStringLiteral(text string) string {
	// 测试路径只需要覆盖常见转义字符，避免 package.path/cpath 中的引号或反斜杠破坏源码。
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + replacer.Replace(text) + `"`
}

// loadNativeSmokeLuaFixture 读取仓库内固定的 Lua require smoke 脚本并注入测试路径。
func loadNativeSmokeLuaFixture(t *testing.T, cpathPattern string, missingLuaPattern string) string {
	// 使用独立 Lua fixture 文件，确保 Go 测试、CLI smoke 和跨平台脚本复用同一份验收逻辑。
	t.Helper()
	sourcePath := filepath.Join(repoRootFromTest(t), "tests", "native_modules", "fixtures", "glua_native_smoke.lua")
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		// fixture Lua 脚本必须固定在仓库内，否则 CLI 级 native 验收无法复用当前 require 覆盖面。
		t.Fatalf("read native Lua fixture source failed: %v", err)
	}

	replacer := strings.NewReplacer(
		"@GLUA_NATIVE_PACKAGE_PATH@", nativeLuaStringLiteral(missingLuaPattern),
		"@GLUA_NATIVE_PACKAGE_CPATH@", nativeLuaStringLiteral(cpathPattern),
	)
	return replacer.Replace(string(sourceBytes))
}

// buildUnixNativeFixture 构建导出 luaopen_glua_native_smoke 的 Lua C 动态库。
func buildUnixNativeFixture(t *testing.T) string {
	// 默认构建 smoke 模块文件名，供现有 loadlib 和 require 成功路径复用。
	t.Helper()
	return buildUnixNativeFixtureModule(t, "glua_native_smoke")
}

// buildUnixNativeFixtureModule 构建指定模块文件名的 Lua C 动态库。
func buildUnixNativeFixtureModule(t *testing.T, outputModule string) string {
	// native_modules 测试依赖 C 编译器；若环境没有可用编译器则明确跳过。
	t.Helper()
	cc := os.Getenv("CC")
	if cc == "" {
		// 未显式指定 CC 时使用系统默认 cc。
		cc = "cc"
	}
	if _, err := exec.LookPath(cc); err != nil {
		// 没有编译器无法构建 fixture，属于环境缺失而不是 Go 逻辑失败。
		t.Skipf("C compiler %q not found: %v", cc, err)
	}

	tempDir := t.TempDir()
	repoRoot := repoRootFromTest(t)
	sourcePath := filepath.Join(repoRoot, "tests", "native_modules", "fixtures", "glua_native_smoke.c")
	if _, err := os.Stat(sourcePath); err != nil {
		// fixture C 源码必须固定在仓库内，否则 native 模块验收不再自包含。
		t.Fatalf("native fixture source missing: %v", err)
	}

	includeDir := filepath.Join(repoRoot, "native", "lua53", "include")
	outputPath := filepath.Join(tempDir, outputModule+dynamicLibraryExtension())
	args := nativeFixtureCompileArgs(includeDir, outputPath, sourcePath)
	command := exec.Command(cc, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		// fixture 编译失败需要暴露完整命令和输出，便于区分编译器差异与头文件路径错误。
		t.Fatalf("compile native fixture failed: %v\n%s %s\n%s", err, cc, strings.Join(args, " "), string(output))
	}
	return outputPath
}

// repoRootFromTest 返回当前测试文件所在仓库根目录。
func repoRootFromTest(t *testing.T) string {
	// 使用 runtime.Caller 定位源码文件，避免依赖 go test 的当前工作目录。
	t.Helper()
	_, filename, _, ok := goruntime.Caller(0)
	if !ok {
		// 无法定位测试文件时无法稳定找到 native/lua53/include。
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

// nativeFixtureCompileArgs 返回当前 Unix 平台构建动态库 fixture 的编译参数。
func nativeFixtureCompileArgs(includeDir string, outputPath string, sourcePath string) []string {
	// 根据平台选择动态库输出参数，fixture 的 lua_* 符号由当前测试进程中的 native shim 满足。
	args := []string{"-I", includeDir, "-o", outputPath}
	switch goruntime.GOOS {
	case "darwin":
		// macOS Lua C 模块通常通过 dynamic_lookup 在宿主进程解析 lua_* / luaL_* 符号。
		args = append(args, "-dynamiclib", "-undefined", "dynamic_lookup")
	default:
		// Linux 使用 shared + fPIC，未解析的 lua_* / luaL_* 符号在 dlopen 时绑定到宿主导出符号。
		args = append(args, "-shared", "-fPIC")
	}
	return append(args, sourcePath)
}

// dynamicLibraryExtension 返回当前 Unix 平台测试 fixture 的动态库后缀。
func dynamicLibraryExtension() string {
	// 后缀与平台 loader 预期一致，后续 cpath fixture 可复用。
	if goruntime.GOOS == "darwin" {
		// macOS 首轮 fixture 使用 dylib；后续单独覆盖 so 后缀候选。
		return ".dylib"
	}
	return ".so"
}

// TestUnixPackageLoadLibMissingFixtureSymbol 验证真实 fixture 缺失符号仍保持 init 分类。
func TestUnixPackageLoadLibMissingFixtureSymbol(t *testing.T) {
	// 使用真实动态库验证 package.loadlib 的错误三返回，而不只测试底层 dlsym。
	fixturePath := buildUnixNativeFixture(t)
	environment := packagelib.NewEnvironmentWithLoaders(nil, Loader())

	values, err := environment.LoadLib(
		luaruntime.StringValue(fixturePath),
		luaruntime.StringValue("luaopen_glua_native_missing"),
	)
	if err != nil {
		// 动态库符号缺失必须通过 loadlib 三返回表达。
		t.Fatalf("LoadLib missing symbol returned error: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[2].String != "init" {
		// 缺失 luaopen_* 符号属于初始化失败分类。
		t.Fatalf("LoadLib missing symbol values = %#v, want nil,message,init", values)
	}
	if !strings.Contains(values[1].String, "luaopen_glua_native_missing") {
		// 错误文本应包含缺失符号名，方便 require 诊断。
		t.Fatalf("LoadLib missing symbol message = %q", values[1].String)
	}
	_, loaderErr := Loader()(fixturePath, "luaopen_glua_native_missing")
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(loaderErr, &dynamicErr) {
		// 直接 loader 路径同样要保持 DynamicLibraryError 分类，避免 package.loadlib 只能归类 open。
		t.Fatalf("direct Loader missing symbol did not return DynamicLibraryError")
	}
	if dynamicErr.Category != "init" {
		// 直接 loader 与 package.loadlib 分类必须一致。
		t.Fatalf("direct Loader missing symbol category = %q, want init", dynamicErr.Category)
	}
}

// TestUnixNativeLuaPushCClosureCallsResolvedFixture 验证解析出的 C 函数可包装为 Go closure。
func TestUnixNativeLuaPushCClosureCallsResolvedFixture(t *testing.T) {
	// fixture 的 luaopen_* 入口是符合 lua_CFunction ABI 的真实动态库符号，并返回模块 table。
	fixturePath := buildUnixNativeFixture(t)
	library, err := openDynamicLibrary(fixturePath)
	if err != nil {
		// fixture 已构建成功，打开失败说明动态库 loader 退化。
		t.Fatalf("open native fixture failed: %v", err)
	}
	defer func() {
		if closeErr := library.close(); closeErr != nil {
			// 动态库关闭失败需要暴露，避免隐藏句柄生命周期问题。
			t.Fatalf("close native fixture failed: %v", closeErr)
		}
	}()
	symbol, err := library.lookupSymbol("luaopen_glua_native_smoke")
	if err != nil {
		// 符号缺失会使 C function wrapper 无法验收。
		t.Fatalf("lookup native fixture symbol failed: %v", err)
	}

	state := luaruntime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C function wrapper。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushCClosure(luaState, symbol, 0)
	closureValue := state.ValueAt(-1)
	if closureValue.Kind != luaruntime.KindGoClosure {
		// C function 必须被包装成当前 VM 可调用的 Go closure。
		t.Fatalf("native C closure value = %#v, want Go closure", closureValue)
	}
	closure, ok := closureValue.Ref.(luaruntime.GoResultsFunction)
	if !ok || closure == nil {
		// 当前 wrapper 使用 GoResultsFunction 承接多返回值语义。
		t.Fatalf("native C closure payload = %#v, want GoResultsFunction", closureValue.Ref)
	}
	if _, err := state.Pop(); err != nil {
		// 直接调用已保存的 Go closure 前先清空测试栈，保持 C API 正索引从实参 1 开始。
		t.Fatalf("pop native C closure value failed: %v", err)
	}
	results, err := closure(luaruntime.IntegerValue(1), luaruntime.StringValue("arg"))
	if err != nil {
		// fixture 只使用当前已实现的 C API，调用不应产生错误。
		t.Fatalf("native C closure call failed: %v", err)
	}
	if len(results) != 1 {
		// fixture luaopen_* 返回一个模块 table。
		t.Fatalf("native C closure results = %#v, want one module table", results)
	}
	assertNativeSmokeModule(t, results[0])
	if got := nativeLuaStackTop(luaState); got != 0 {
		// 调用期间临时压入的参数必须在返回后恢复，不污染调用方栈。
		t.Fatalf("native C closure top after call = %d, want 0", got)
	}
}

// TestDynamicLibraryExtensionDocumentsFixtureSuffix 验证 fixture 后缀选择保持平台可读。
func TestDynamicLibraryExtensionDocumentsFixtureSuffix(t *testing.T) {
	// 该测试锁定后缀策略，避免后续 cpath fixture 与构建脚本产生分歧。
	extension := dynamicLibraryExtension()
	if extension != ".so" && extension != ".dylib" {
		// Unix 平台只应出现 .so 或 .dylib。
		t.Fatalf("dynamic library extension = %q", extension)
	}
	if goruntime.GOOS == "darwin" && extension != ".dylib" {
		// macOS 首轮 fixture 使用 dylib。
		t.Fatalf("darwin extension = %q, want .dylib", extension)
	}
	if goruntime.GOOS != "darwin" && extension != ".so" {
		// Linux 首轮 fixture 使用 so。
		t.Fatalf("%s extension = %q, want .so", goruntime.GOOS, extension)
	}
}
