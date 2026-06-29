package packagelib

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestOpenRegistersPackageAndRequire 验证 Open 注册 package 表和 require 全局函数。
//
// 该用例覆盖 package.config、package.cpath、package.loaded、package.path、package.preload、
// package.searchers、package.searchpath 与 package.loadlib 的注册形态。
func TestOpenRegistersPackageAndRequire(t *testing.T) {
	// 测试先创建独立 State，避免污染其他标准库测试。
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// Open 不应在有效 State 上失败。
		t.Fatalf("Open failed: %v", err)
	}

	requireValue := state.GetGlobal("require")
	if requireValue.Kind != runtime.KindGoClosure {
		// require 必须注册为 Go closure。
		t.Fatalf("require kind = %v, want Go closure", requireValue.Kind)
	}
	packageValue := state.GetGlobal("package")
	if packageValue.Kind != runtime.KindTable {
		// package 全局必须是 table。
		t.Fatalf("package kind = %v, want table", packageValue.Kind)
	}
	packageTable := packageValue.Ref.(*runtime.Table)
	if packageTable.RawGetString("config").String != DefaultConfig {
		// package.config 文本必须稳定。
		t.Fatalf("package.config mismatch")
	}
	if packageTable.RawGetString("path").String != DefaultPath {
		// package.path 默认模板必须稳定。
		t.Fatalf("package.path mismatch")
	}
	if packageTable.RawGetString("cpath").String != DefaultCPath {
		// package.cpath 保留兼容搜索模板，但实际 C loader 仍由无 CGO 策略禁用。
		t.Fatalf("package.cpath mismatch")
	}
	if packageTable.RawGetString("loaded").Kind != runtime.KindTable {
		// package.loaded 必须是 table。
		t.Fatalf("package.loaded kind = %v, want table", packageTable.RawGetString("loaded").Kind)
	}
	if packageTable.RawGetString("preload").Kind != runtime.KindTable {
		// package.preload 必须是 table。
		t.Fatalf("package.preload kind = %v, want table", packageTable.RawGetString("preload").Kind)
	}
	if packageTable.RawGetString("searchers").Kind != runtime.KindTable {
		// package.searchers 必须是 table。
		t.Fatalf("package.searchers kind = %v, want table", packageTable.RawGetString("searchers").Kind)
	}
	if packageTable.RawGetString("searchpath").Kind != runtime.KindGoClosure {
		// package.searchpath 必须是 Go closure。
		t.Fatalf("package.searchpath kind = %v, want Go closure", packageTable.RawGetString("searchpath").Kind)
	}
	if packageTable.RawGetString("loadlib").Kind != runtime.KindGoClosure {
		// package.loadlib 必须是 Go closure。
		t.Fatalf("package.loadlib kind = %v, want Go closure", packageTable.RawGetString("loadlib").Kind)
	}
}

// TestEnvironmentPathVariables 验证 package.path/package.cpath 启动环境变量。
//
// Lua 5.3 使用版本专用变量优先于通用变量，并用 `;;` 表示插入默认路径。
func TestEnvironmentPathVariables(t *testing.T) {
	// 设置通用 path 变量时 package.path 应使用环境值。
	t.Setenv("LUA_PATH", "x")
	environment := NewEnvironment()
	if got := environment.Table().RawGetString("path").String; got != "x" {
		// LUA_PATH 必须覆盖默认 package.path。
		t.Fatalf("package.path = %q, want x", got)
	}

	// 版本专用变量优先于通用变量。
	t.Setenv("LUA_PATH_5_3", "y")
	environment = NewEnvironment()
	if got := environment.Table().RawGetString("path").String; got != "y" {
		// LUA_PATH_5_3 必须优先于 LUA_PATH。
		t.Fatalf("package.path versioned = %q, want y", got)
	}

	// cpath 使用同样的版本专用优先级。
	t.Setenv("LUA_CPATH", "cx")
	t.Setenv("LUA_CPATH_5_3", "cy")
	environment = NewEnvironment()
	if got := environment.Table().RawGetString("cpath").String; got != "cy" {
		// LUA_CPATH_5_3 必须优先于 LUA_CPATH。
		t.Fatalf("package.cpath versioned = %q, want cy", got)
	}

	// `;;` 必须展开为包含默认路径的模板。
	if got := expandDefaultPath("a;;b", DefaultPath); got != "a;"+DefaultPath+";b" {
		// 默认路径展开需要保留左右分隔符。
		t.Fatalf("expanded path = %q", got)
	}
}

// TestConfigLines 验证 package.config 的 5 行结构。
//
// Lua 5.3 package.config 需要包含目录分隔符、路径分隔符、模板占位符、执行目录标记和忽略标记。
func TestConfigLines(t *testing.T) {
	// 拆分 package.config 并校验固定 Unix 风格策略。
	lines := ConfigLines()
	if len(lines) != 5 {
		// package.config 必须有 5 行。
		t.Fatalf("config line count = %d, want 5", len(lines))
	}
	if lines[0] != "/" || lines[1] != ";" || lines[2] != "?" || lines[3] != "!" || lines[4] != "-" {
		// 每一行都必须匹配当前平台策略。
		t.Fatalf("config lines = %#v", lines)
	}
}

// TestRequireReturnsLoadedModule 验证 require 命中 package.loaded 时直接返回缓存。
//
// 这覆盖 require 的第一阶段可用路径，后续 searchers 接入后仍应保留该快速路径。
func TestRequireReturnsLoadedModule(t *testing.T) {
	// 构造独立 package 环境并写入 loaded 缓存。
	environment := NewEnvironment()
	module := runtime.NewTable()
	environment.Loaded().RawSetString("demo", runtime.ReferenceValue(runtime.KindTable, module))

	values, err := environment.Require(runtime.StringValue("demo"))
	if err != nil {
		// loaded 命中不应失败。
		t.Fatalf("Require loaded failed: %v", err)
	}
	if len(values) != 1 || values[0].Ref != module {
		// require 必须返回 loaded 中保存的模块值。
		t.Fatalf("Require loaded result = %#v", values)
	}
}

// TestRequireUsesPreloadSearcher 验证 require 可通过 package.searchers 命中预加载模块。
//
// 该用例覆盖 package.preload、package.searchers 与 require loader 执行的第一阶段闭环。
func TestRequireUsesPreloadSearcher(t *testing.T) {
	// 预加载 loader 返回一个字符串模块值，模拟 Go 侧注册纯 Lua 模块代理。
	environment := NewEnvironment()
	if err := environment.RegisterPreload("demo", runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// loader 必须收到 require 传入的模块名。
		if len(args) != 1 || args[0].String != "demo" {
			// 参数不符合预期时让测试直接失败。
			t.Fatalf("loader args = %#v", args)
		}
		return []runtime.Value{runtime.StringValue("loaded-demo")}, nil
	})); err != nil {
		// 合法 preload loader 注册不应失败。
		t.Fatalf("RegisterPreload failed: %v", err)
	}

	values, err := environment.Require(runtime.StringValue("demo"))
	if err != nil {
		// preload 命中不应失败。
		t.Fatalf("Require preload failed: %v", err)
	}
	if len(values) != 1 || values[0].String != "loaded-demo" {
		// require 必须返回 loader 的模块值。
		t.Fatalf("Require preload result = %#v", values)
	}
	if got := environment.Loaded().RawGetString("demo"); got.String != "loaded-demo" {
		// loader 结果必须写入 package.loaded 缓存。
		t.Fatalf("package.loaded.demo = %#v", got)
	}
}

// TestRequireUsesInjectedLoaderCallerForLuaPreload 验证 require 可通过宿主调用器执行 Lua closure loader。
//
// stdlib/package 不直接依赖 lua 包；Lua closure 执行由 lua 包通过 SetLoaderCaller 注入。
func TestRequireUsesInjectedLoaderCallerForLuaPreload(t *testing.T) {
	environment := NewEnvironment()
	loaderValue := runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{})
	environment.Preload().RawSetString("demo", loaderValue)
	environment.SetLoaderCaller(func(loader runtime.Value, args ...runtime.Value) ([]runtime.Value, error) {
		if loader.Kind != runtime.KindLuaClosure || len(args) != 1 || args[0].String != "demo" {
			// 调用器必须收到 Lua closure 和模块名。
			t.Fatalf("loader=%#v args=%#v", loader, args)
		}
		return []runtime.Value{runtime.StringValue("lua-preload")}, nil
	})

	values, err := environment.Require(runtime.StringValue("demo"))
	if err != nil {
		// 注入调用器后 Lua closure preload 不应失败。
		t.Fatalf("Require lua preload failed: %v", err)
	}
	if len(values) != 1 || values[0].String != "lua-preload" {
		// require 必须返回注入调用器的模块值。
		t.Fatalf("Require lua preload result = %#v", values)
	}
}

// TestRequireLuaPreloadWithoutInjectedCallerFails 验证默认 package 环境不静默执行 Lua closure。
//
// 默认调用器只支持 Go closure，避免 stdlib/package 直接依赖 lua 执行循环。
func TestRequireLuaPreloadWithoutInjectedCallerFails(t *testing.T) {
	environment := NewEnvironment()
	environment.Preload().RawSetString("demo", runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{}))

	_, err := environment.Require(runtime.StringValue("demo"))
	if !errors.Is(err, runtime.ErrExpectedCallable) {
		// 没有注入调用器时 Lua closure loader 仍不可直接调用。
		t.Fatalf("Require lua preload without caller error = %v, want ErrExpectedCallable", err)
	}
}

// TestRequirePreloadLoaderNilResultStoresTrue 验证预加载 loader 不返回模块值时写入 true。
//
// Lua 5.3 require 对 loader 返回 nil 的模块会写入 package.loaded[name]=true，避免重复加载。
func TestRequirePreloadLoaderNilResultStoresTrue(t *testing.T) {
	// 注册一个不返回值的 preload loader。
	environment := NewEnvironment()
	if err := environment.RegisterPreload("empty", runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// loader 成功但不返回模块值，触发 require 的 true 缓存路径。
		return nil, nil
	})); err != nil {
		// 合法 preload loader 注册不应失败。
		t.Fatalf("RegisterPreload empty failed: %v", err)
	}

	values, err := environment.Require(runtime.StringValue("empty"))
	if err != nil {
		// preload loader 成功执行不应失败。
		t.Fatalf("Require empty preload failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindBoolean || !values[0].Bool {
		// require 必须返回 true 并写入 loaded 缓存。
		t.Fatalf("Require empty preload result = %#v", values)
	}
	if got := environment.Loaded().RawGetString("empty"); got.Kind != runtime.KindBoolean || !got.Bool {
		// package.loaded 必须保存 true。
		t.Fatalf("package.loaded.empty = %#v", got)
	}
}

// TestRegisterGoModuleUsesPreloadDesign 验证 Go 模块 loader 设计复用 package.preload。
//
// 该设计不新增非 Lua 5.3 标准 searcher，而是让 Go 扩展模块通过标准 require 流程加载。
func TestRegisterGoModuleUsesPreloadDesign(t *testing.T) {
	// 注册 Go 实现的模块 loader。
	environment := NewEnvironment()
	if err := environment.RegisterGoModule("go.demo", runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// Go 模块 loader 返回一个 table 模块。
		module := runtime.NewTable()
		module.RawSetString("name", runtime.StringValue(args[0].String))
		return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, module)}, nil
	})); err != nil {
		// 合法 Go 模块 loader 注册不应失败。
		t.Fatalf("RegisterGoModule failed: %v", err)
	}
	if got := environment.Preload().RawGetString("go.demo"); got.Kind != runtime.KindGoClosure {
		// Go 模块 loader 必须落入 package.preload。
		t.Fatalf("package.preload.go.demo = %#v", got)
	}

	values, err := environment.Require(runtime.StringValue("go.demo"))
	if err != nil {
		// require 应通过 preload searcher 加载 Go 模块。
		t.Fatalf("Require go module failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindTable {
		// Go 模块 loader 必须返回 table 模块。
		t.Fatalf("Require go module result = %#v", values)
	}
}

// TestRegisterPreloadRejectsInvalidInput 验证预加载模块注册的输入约束。
//
// 注册入口需要在 Go 侧提前拒绝空模块名和 nil loader，避免 require 阶段才暴露损坏状态。
func TestRegisterPreloadRejectsInvalidInput(t *testing.T) {
	// 构造独立环境并覆盖非法输入。
	environment := NewEnvironment()
	if err := environment.RegisterPreload("", runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 该 loader 不应被注册或调用。
		return nil, nil
	})); err == nil {
		// 空模块名必须被拒绝。
		t.Fatalf("RegisterPreload empty module succeeded")
	}
	if err := environment.RegisterPreload("bad", nil); err == nil {
		// nil loader 必须被拒绝。
		t.Fatalf("RegisterPreload nil loader succeeded")
	}
}

// TestRequireErrorsWhenModuleMissing 验证 require 未命中时返回 Lua error。
//
// 未命中 loaded 和 searchers 时应给出包含候选路径与 C loader 策略的 module not found 错误。
func TestRequireErrorsWhenModuleMissing(t *testing.T) {
	// 空环境中 require 任意模块都应失败。
	environment := NewEnvironment()
	_, err := environment.Require(runtime.StringValue("missing"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 未找到模块必须是 Lua error。
		t.Fatalf("Require missing error = %v, want Lua error", err)
	}
	errorObject := runtime.ErrorObject(err)
	if errorObject.Kind != runtime.KindString || !strings.Contains(errorObject.String, "module 'missing' not found") || !strings.Contains(errorObject.String, "no file './missing.lua'") {
		// 错误文本必须包含模块名和当前 Lua 文件候选路径。
		t.Fatalf("Require missing object = %#v", errorObject)
	}
	if _, err := environment.Require(runtime.IntegerValue(1)); !errors.Is(err, runtime.ErrLuaError) {
		// 非 string 模块名必须是 Lua 参数错误。
		t.Fatalf("Require argument error = %v, want Lua error", err)
	}
}

// TestRequireErrorsWhenPackagePathIsNotString 验证 require 对损坏 package.path 的错误语义。
//
// Lua 5.3 要求 package.path 被改成非 string 时直接抛出包含 package.path 的错误，而不是回退默认路径。
func TestRequireErrorsWhenPackagePathIsNotString(t *testing.T) {
	environment := NewEnvironment()
	environment.Table().RawSetString("path", runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()))

	_, err := environment.Require(runtime.StringValue("no-such-file"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 非 string package.path 必须通过 Lua error 暴露。
		t.Fatalf("Require bad package.path error = %v, want Lua error", err)
	}
	errorObject := runtime.ErrorObject(err)
	if errorObject.Kind != runtime.KindString || !strings.Contains(errorObject.String, "package.path") {
		// 错误文本必须包含 package.path，供官方 attrib.lua 断言。
		t.Fatalf("Require bad package.path object = %#v", errorObject)
	}
}

// TestRequireErrorsWhenSearchersIsNotTable 验证 require 会观察运行期 package.searchers 类型。
//
// Lua 5.3 允许用户改写 package.searchers；改成非 table 后 require 必须抛出明确错误。
func TestRequireErrorsWhenSearchersIsNotTable(t *testing.T) {
	environment := NewEnvironment()
	environment.Table().RawSetString("searchers", runtime.IntegerValue(3))

	_, err := environment.Require(runtime.StringValue("demo"))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 非 table package.searchers 必须通过 Lua error 暴露。
		t.Fatalf("Require bad searchers error = %v, want Lua error", err)
	}
	errorObject := runtime.ErrorObject(err)
	if errorObject.Kind != runtime.KindString || !strings.Contains(errorObject.String, "must be a table") {
		// 错误文本必须包含官方断言需要的 must be a table。
		t.Fatalf("Require bad searchers object = %#v", errorObject)
	}
}

// TestLoadLibDisabled 验证内置 package.loadlib 的无 CGO 策略。
//
// 合法参数下返回 nil 和错误文本，表示默认 C 动态库加载器未内置；宿主程序可覆盖该函数。
func TestLoadLibDisabled(t *testing.T) {
	// 构造独立环境并调用 loadlib。
	environment := NewEnvironment()
	values, err := environment.LoadLib(runtime.StringValue("libdemo.so"), runtime.StringValue("luaopen_demo"))
	if err != nil {
		// 合法参数不应抛出 Lua error，而应返回 nil,error 文本。
		t.Fatalf("LoadLib failed: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[1].Kind != runtime.KindString || values[2].String != "absent" {
		// loadlib 禁用策略必须返回 nil、错误文本和 absent 分类。
		t.Fatalf("LoadLib result = %#v", values)
	}
	if values[1].String != CLoadingPolicy() {
		// loadlib 错误文本必须与集中策略一致。
		t.Fatalf("LoadLib policy = %q, want %q", values[1].String, CLoadingPolicy())
	}
	if _, err := environment.LoadLib(runtime.IntegerValue(1), runtime.StringValue("sym")); !errors.Is(err, runtime.ErrLuaError) {
		// 参数错误仍应是 Lua error。
		t.Fatalf("LoadLib argument error = %v, want Lua error", err)
	}
}

// TestLoadLibUsesDynamicLibraryLoader 验证 package.loadlib 可通过宿主 loader 返回 Lua 可调用函数。
//
// 默认构建仍不引入 CGO；该用例用纯 Go closure 模拟宿主已经完成动态库打开和符号解析。
func TestLoadLibUsesDynamicLibraryLoader(t *testing.T) {
	// 构造带宿主动态库 loader 的 package 环境。
	callCount := 0
	environment := NewEnvironmentWithLoaders(nil, func(filename string, symbol string) (runtime.Value, error) {
		if filename != "libdemo.dylib" || symbol != "luaopen_demo" {
			// 参数不符合预期时返回带 init 分类的兼容错误。
			return runtime.NilValue(), DynamicLibraryError{Category: "init", Message: "unexpected dynamic library request"}
		}
		callCount++
		loader := runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			// 模拟 luaopen_demo 返回模块值。
			return []runtime.Value{runtime.StringValue("dynamic-demo")}, nil
		})
		return runtime.ReferenceValue(runtime.KindGoClosure, loader), nil
	})

	values, err := environment.LoadLib(runtime.StringValue("libdemo.dylib"), runtime.StringValue("luaopen_demo"))
	if err != nil {
		// 宿主 loader 成功时 loadlib 不应返回 Go error。
		t.Fatalf("LoadLib dynamic loader failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindGoClosure {
		// 成功路径必须只返回 Lua callable。
		t.Fatalf("LoadLib dynamic values = %#v", values)
	}
	results, err := callGoResults(values[0])
	if err != nil {
		// 返回的 loader 必须可执行。
		t.Fatalf("dynamic loader callable failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "dynamic-demo" || callCount != 1 {
		// loader 结果和调用次数必须稳定。
		t.Fatalf("dynamic loader results=%#v callCount=%d", results, callCount)
	}
}

// TestLoadLibKeepsTripleReturnForDynamicLoaderFailure 验证宿主 loader 失败时保持兼容三返回。
//
// DynamicLibraryError 允许宿主明确 absent/open/init 分类；普通 error 会被归类为 open。
func TestLoadLibKeepsTripleReturnForDynamicLoaderFailure(t *testing.T) {
	environment := NewEnvironmentWithLoaders(nil, func(filename string, symbol string) (runtime.Value, error) {
		// 模拟符号存在但初始化失败。
		return runtime.NilValue(), DynamicLibraryError{Category: "init", Message: "missing symbol " + symbol}
	})

	values, err := environment.LoadLib(runtime.StringValue("libdemo.so"), runtime.StringValue("luaopen_demo"))
	if err != nil {
		// 动态库加载失败应通过返回值表达，不应抛出 Lua error。
		t.Fatalf("LoadLib dynamic failure returned error: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[1].String != "missing symbol luaopen_demo" || values[2].String != "init" {
		// 失败路径必须保持 nil,error,where 三返回。
		t.Fatalf("LoadLib dynamic failure values = %#v", values)
	}
}

// TestCLoadingPolicyDocumentsUnsupportedDynamicLibraries 验证内置 C 动态库 loader 的固定策略。
//
// 本项目默认构建纯 Go 且禁用 CGO，因此默认 loadlib、C searcher 和 C root searcher 都必须明确不支持。
func TestCLoadingPolicyDocumentsUnsupportedDynamicLibraries(t *testing.T) {
	// 内置 C 动态库支持状态必须固定为 false。
	if CLoadingSupported() {
		// 启用默认内置 C loader 会破坏默认跨系统编译边界。
		t.Fatalf("CLoadingSupported = true, want false")
	}
	if !strings.Contains(CLoadingPolicy(), "CGO-free") {
		// 策略文本必须说明无 CGO 约束。
		t.Fatalf("CLoadingPolicy = %q", CLoadingPolicy())
	}

	environment := NewEnvironment()
	cResults, cErr := environment.CSearcher(runtime.StringValue("demo"))
	if cErr != nil {
		// C searcher 合法模块名不应抛出 Lua error。
		t.Fatalf("CSearcher failed: %v", cErr)
	}
	rootResults, rootErr := environment.CRootSearcher(runtime.StringValue("demo.sub"))
	if rootErr != nil {
		// C root searcher 合法模块名不应抛出 Lua error。
		t.Fatalf("CRootSearcher failed: %v", rootErr)
	}
	if len(cResults) != 1 || !strings.Contains(cResults[0].String, CLoadingPolicy()) {
		// C searcher 必须返回集中策略文本。
		t.Fatalf("CSearcher result = %#v", cResults)
	}
	if len(rootResults) != 1 || !strings.Contains(rootResults[0].String, CLoadingPolicy()) {
		// C root searcher 必须返回集中策略文本。
		t.Fatalf("CRootSearcher result = %#v", rootResults)
	}
}

// TestDefaultCPathDocumentsPlatformCandidates 验证动态库默认候选和 Windows .lib 边界。
//
// Linux/macOS 默认候选包含 .so 与 .dylib；Windows 只把 .dll 作为运行期加载候选，并明确 .lib
// 是链接期/import library，不是 require 运行期路径。
func TestDefaultCPathDocumentsPlatformCandidates(t *testing.T) {
	// Unix 默认 cpath 必须同时覆盖 .so 和 .dylib。
	unixCPath := DefaultCPathForGOOS("linux")
	if !strings.Contains(unixCPath, "?.so") || !strings.Contains(unixCPath, "?.dylib") {
		// Linux/macOS 候选必须包含两类常见动态库后缀。
		t.Fatalf("unix cpath = %q", unixCPath)
	}
	darwinCPath := DefaultCPathForGOOS("darwin")
	if !strings.Contains(darwinCPath, "?.so") || !strings.Contains(darwinCPath, "?.dylib") {
		// macOS 也必须保留 .so 兼容候选和 .dylib 原生候选。
		t.Fatalf("darwin cpath = %q", darwinCPath)
	}
	windowsCPath := DefaultCPathForGOOS("windows")
	if !strings.Contains(windowsCPath, "?.dll") || strings.Contains(windowsCPath, "?.lib") {
		// Windows 运行期 require 只搜索 .dll，不搜索 .lib。
		t.Fatalf("windows cpath = %q", windowsCPath)
	}
	windowsNote := DynamicLibraryPlatformNote("windows")
	if !strings.Contains(windowsNote, ".dll") || !strings.Contains(windowsNote, ".lib/import library") {
		// Windows 诊断必须明确运行期和链接期边界。
		t.Fatalf("windows note = %q", windowsNote)
	}
}

// TestSearchersAreRegistered 验证 package.searchers 的四个 Lua 5.3 标准搜索器槽位。
//
// 当前四个槽位分别对应 preload、Lua 文件、C 动态库和 C root 动态库搜索策略。
func TestSearchersAreRegistered(t *testing.T) {
	// 新建环境后 searchers 应立即可用。
	environment := NewEnvironment()
	for index := int64(1); index <= 4; index++ {
		// 每个标准 searcher 都必须是 Go closure。
		if got := environment.Searchers().RawGetInteger(index); got.Kind != runtime.KindGoClosure {
			t.Fatalf("searcher[%d] kind = %v, want Go closure", index, got.Kind)
		}
	}
	if got := environment.Searchers().RawGetInteger(5); !got.IsNil() {
		// 第五个槽位当前未注册，保持 Lua 数组结束语义。
		t.Fatalf("searcher[5] = %#v, want nil", got)
	}
}

// TestSearchPathFindsReadableFile 验证 package.searchpath 会返回首个可读候选文件。
//
// 命中文件时返回文件名；未命中时返回 nil 和包含候选路径的错误文本。
func TestSearchPathFindsReadableFile(t *testing.T) {
	// 写入临时 Lua 文件，验证 searchpath 按模板顺序查找可读文件。
	environment := NewEnvironment()
	tempDir := t.TempDir()
	modulePath := filepath.Join(tempDir, "a", "b.lua")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o700); err != nil {
		// 测试目录创建不应失败。
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(modulePath, []byte("return true\n"), 0o600); err != nil {
		// 测试文件写入不应失败。
		t.Fatalf("WriteFile failed: %v", err)
	}
	values, err := environment.SearchPath(
		runtime.StringValue("a.b"),
		runtime.StringValue(filepath.Join(tempDir, "missing", "?.lua")+";"+filepath.Join(tempDir, "?.lua")),
	)
	if err != nil {
		// 合法参数不应抛出 Lua error。
		t.Fatalf("SearchPath failed: %v", err)
	}
	if len(values) != 1 || values[0].String != modulePath {
		// 命中文件时必须只返回文件路径。
		t.Fatalf("SearchPath result = %#v", values)
	}

	missingValues, err := environment.SearchPath(
		runtime.StringValue("a.c"),
		runtime.StringValue(filepath.Join(tempDir, "?.lua")),
	)
	if err != nil {
		// 合法未命中也不应抛出 Lua error。
		t.Fatalf("SearchPath missing failed: %v", err)
	}
	if len(missingValues) != 2 || !missingValues[0].IsNil() || missingValues[1].Kind != runtime.KindString {
		// 未命中时必须返回 nil 和错误文本。
		t.Fatalf("SearchPath missing result = %#v", missingValues)
	}
	if !strings.Contains(missingValues[1].String, filepath.Join(tempDir, "a", "c.lua")) {
		// 错误文本必须包含替换后的所有候选路径。
		t.Fatalf("SearchPath error text = %q", missingValues[1].String)
	}
}

// TestCSearcherReportsCPathCandidates 验证禁用 C loader 时仍报告 cpath 候选路径。
//
// Lua 5.3 require 的缺失模块错误会汇总 package.path 与 package.cpath；即使本项目禁用
// CGO，也必须把 cpath 展开结果放入诊断文本，供官方 attrib.lua 校验。
func TestCSearcherReportsCPathCandidates(t *testing.T) {
	environment := NewEnvironment()
	environment.Table().RawSetString("cpath", runtime.StringValue("./?.so"))

	values, err := environment.CSearcher(runtime.StringValue("missing.mod"))
	if err != nil {
		// 合法模块名不应让 C searcher 抛出 Lua error。
		t.Fatalf("CSearcher failed: %v", err)
	}
	if len(values) != 1 || values[0].Kind != runtime.KindString {
		// C searcher 未命中时必须返回单个诊断字符串。
		t.Fatalf("CSearcher values = %#v", values)
	}
	if !strings.Contains(values[0].String, "./missing/mod.so") {
		// 诊断文本必须包含 cpath 展开候选。
		t.Fatalf("CSearcher message missing cpath candidate: %q", values[0].String)
	}
	if !strings.Contains(values[0].String, "C loader disabled") {
		// 诊断文本仍需明确当前纯 Go 禁用动态 C loader。
		t.Fatalf("CSearcher message missing disabled policy: %q", values[0].String)
	}
}

// TestCSearcherUsesDynamicLibraryLoader 验证 C searcher 会按 package.cpath 候选调用宿主 loader。
//
// searcher 需要生成 luaopen_* 符号，返回 loader 和命中文件名 loader data，供 require 后续执行。
func TestCSearcherUsesDynamicLibraryLoader(t *testing.T) {
	tempDir := t.TempDir()
	modulePath := filepath.Join(tempDir, "demo", "mod.dylib")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o700); err != nil {
		// 测试目录创建不应失败。
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(modulePath, []byte("not a real dylib\n"), 0o600); err != nil {
		// 候选文件只用于验证路径展开，宿主 loader 不会真实打开。
		t.Fatalf("WriteFile failed: %v", err)
	}

	var requests []string
	environment := NewEnvironmentWithLoaders(nil, func(filename string, symbol string) (runtime.Value, error) {
		// 记录 searcher 传入的候选和符号。
		requests = append(requests, filename+":"+symbol)
		if filename != modulePath || symbol != "luaopen_demo_mod" {
			// 非命中候选按 open 失败处理，searcher 应继续尝试。
			return runtime.NilValue(), DynamicLibraryError{Category: "open", Message: "candidate not found"}
		}
		loader := runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			// 模拟动态库模块入口返回模块值。
			return []runtime.Value{runtime.StringValue("loaded from " + filename)}, nil
		})
		return runtime.ReferenceValue(runtime.KindGoClosure, loader), nil
	})
	environment.Table().RawSetString("cpath", runtime.StringValue(filepath.Join(tempDir, "missing", "?.so")+";"+filepath.Join(tempDir, "?.dylib")))

	values, err := environment.CSearcher(runtime.StringValue("demo.mod"))
	if err != nil {
		// 合法模块名不应失败。
		t.Fatalf("CSearcher dynamic failed: %v", err)
	}
	if len(values) != 2 || values[0].Kind != runtime.KindGoClosure || values[1].String != modulePath {
		// searcher 成功路径必须返回 loader 和命中文件名。
		t.Fatalf("CSearcher dynamic values = %#v", values)
	}
	results, err := callGoResults(values[0], runtime.StringValue("demo.mod"), values[1])
	if err != nil {
		// 返回的 loader 必须可执行。
		t.Fatalf("CSearcher loader failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "loaded from "+modulePath {
		// loader 结果必须来自命中候选。
		t.Fatalf("CSearcher loader results = %#v", results)
	}
	if len(requests) != 2 || requests[1] != modulePath+":luaopen_demo_mod" {
		// searcher 必须按 cpath 顺序尝试候选并生成稳定符号。
		t.Fatalf("dynamic requests = %#v", requests)
	}
}

// TestCRootSearcherUsesRootCandidateAndFullSymbol 验证 C root searcher 的候选和符号规则。
//
// 对 demo.mod，候选路径使用 demo，符号仍使用完整模块名 luaopen_demo_mod。
func TestCRootSearcherUsesRootCandidateAndFullSymbol(t *testing.T) {
	tempDir := t.TempDir()
	rootPath := filepath.Join(tempDir, "demo.so")
	if err := os.WriteFile(rootPath, []byte("not a real so\n"), 0o600); err != nil {
		// 测试文件写入不应失败。
		t.Fatalf("WriteFile failed: %v", err)
	}

	environment := NewEnvironmentWithLoaders(nil, func(filename string, symbol string) (runtime.Value, error) {
		if filename != rootPath || symbol != "luaopen_demo_mod" {
			// root searcher 必须用根模块找文件，用完整模块名找符号。
			return runtime.NilValue(), DynamicLibraryError{Category: "open", Message: "unexpected root request"}
		}
		loader := runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			return []runtime.Value{runtime.StringValue(symbol)}, nil
		})
		return runtime.ReferenceValue(runtime.KindGoClosure, loader), nil
	})
	environment.Table().RawSetString("cpath", runtime.StringValue(filepath.Join(tempDir, "?.so")))

	values, err := environment.CRootSearcher(runtime.StringValue("demo.mod"))
	if err != nil {
		// 合法模块名不应失败。
		t.Fatalf("CRootSearcher dynamic failed: %v", err)
	}
	if len(values) != 2 || values[0].Kind != runtime.KindGoClosure || values[1].String != rootPath {
		// C root searcher 成功路径必须返回 root 文件名。
		t.Fatalf("CRootSearcher dynamic values = %#v", values)
	}
}

// TestLuaSearcherReturnsFileLoader 验证 Lua 文件 searcher 命中文件后返回 loader。
//
// loader 由宿主注入，package 包只负责文件查找和 require searcher 协议。
func TestLuaSearcherReturnsFileLoader(t *testing.T) {
	// 写入临时 Lua 文件，并让注入 loader 返回该路径供断言。
	tempDir := t.TempDir()
	modulePath := filepath.Join(tempDir, "demo", "mod.lua")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o700); err != nil {
		// 测试目录创建不应失败。
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(modulePath, []byte("return true\n"), 0o600); err != nil {
		// 测试文件写入不应失败。
		t.Fatalf("WriteFile failed: %v", err)
	}
	environment := NewEnvironmentWithLuaFileLoader(func(filename string) runtime.GoResultsFunction {
		// loader 捕获命中文件名，模拟 lua 包后续执行文件。
		return func(args ...runtime.Value) ([]runtime.Value, error) {
			return []runtime.Value{runtime.StringValue(filename)}, nil
		}
	})
	environment.Table().RawSetString("path", runtime.StringValue(filepath.Join(tempDir, "?.lua")))

	values, err := environment.LuaSearcher(runtime.StringValue("demo.mod"))
	if err != nil {
		// 合法模块名不应抛出 Lua error。
		t.Fatalf("LuaSearcher failed: %v", err)
	}
	if len(values) != 2 || values[0].Kind != runtime.KindGoClosure || values[1].String != modulePath {
		// 命中文件时 searcher 必须返回 loader 和文件名 loader data。
		t.Fatalf("LuaSearcher result = %#v", values)
	}
	loaderResults, err := callGoResults(values[0], runtime.StringValue("demo.mod"), values[1])
	if err != nil {
		// loader 执行不应失败。
		t.Fatalf("loader failed: %v", err)
	}
	if len(loaderResults) != 1 || loaderResults[0].String != modulePath {
		// loader 必须收到 searcher 命中的文件名。
		t.Fatalf("loader results = %#v", loaderResults)
	}
}

// TestRequirePassesLuaSearcherLoaderData 验证 require 会把 Lua 文件 searcher 的 loader data 传给 loader。
//
// 官方 attrib.lua 依赖 Lua 文件 chunk 通过 `...` 看到模块名和命中文件名。
func TestRequirePassesLuaSearcherLoaderData(t *testing.T) {
	tempDir := t.TempDir()
	modulePath := filepath.Join(tempDir, "demo.lua")
	if err := os.WriteFile(modulePath, []byte("return true\n"), 0o600); err != nil {
		// 测试文件写入不应失败。
		t.Fatalf("WriteFile failed: %v", err)
	}
	environment := NewEnvironmentWithLuaFileLoader(func(filename string) runtime.GoResultsFunction {
		// loader 捕获 searcher 命中的文件名，并校验 require 透传的参数。
		return func(args ...runtime.Value) ([]runtime.Value, error) {
			if len(args) != 2 || args[0].String != "demo" || args[1].String != filename {
				// loader 必须收到模块名和文件名。
				t.Fatalf("loader args = %#v filename=%q", args, filename)
			}
			return []runtime.Value{runtime.StringValue(args[0].String + ":" + args[1].String)}, nil
		}
	})
	environment.Table().RawSetString("path", runtime.StringValue(filepath.Join(tempDir, "?.lua")))

	values, err := environment.Require(runtime.StringValue("demo"))
	if err != nil {
		// Lua 文件 loader 命中不应失败。
		t.Fatalf("Require lua file failed: %v", err)
	}
	if len(values) != 1 || values[0].String != "demo:"+modulePath {
		// require 必须返回 loader 的模块值。
		t.Fatalf("Require lua file result = %#v", values)
	}
}
