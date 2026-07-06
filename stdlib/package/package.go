// Package packagelib 实现 Lua 5.3 package 标准库的第一阶段能力。
//
// 本包当前提供 package 表、require 的 loaded/searchers 路径、package.config/path/cpath/loaded、
// package.preload、package.searchers、package.searchpath、Lua 文件 loader 和 package.loadlib 的
// 内置无 CGO 策略。默认 require 会报告 C 模块候选，但没有宿主 loader 时不自动打开动态库。
package packagelib

import (
	"errors"
	"fmt"
	"os"
	goRuntime "runtime"
	"strings"

	"github.com/zing/go-lua-vm/runtime"
)

const (
	// DefaultConfig 是 Lua 5.3 package.config 在 Unix 风格平台下的基础文本。
	DefaultConfig = "/\n;\n?\n!\n-\n"
	// DefaultPath 是当前纯 Go package.path 默认模板。
	DefaultPath = "./?.glua;./?.lua;./?/init.glua;./?/init.lua"
	// DefaultCPath 是 Unix 风格平台下的 Lua 5.3 默认 C 模块搜索模板。
	//
	// 默认 CGO-free 构建仍不会打开动态库；该模板只用于保持 package.cpath 和 require 诊断与
	// Lua 5.3 CLI 接近，实际加载能力由 PackageDynamicLibraryLoader 显式接入。
	DefaultCPath = "/usr/local/lib/lua/5.3/?.so;/usr/local/lib/lua/5.3/loadall.so;./?.so"
	// DefaultWindowsCPath 是 Windows 风格平台下的 Lua 5.3 默认 C 模块搜索模板。
	//
	// 默认 CGO-free 构建仍不会打开动态库；该模板只用于 package.cpath 兼容展示和候选诊断。
	DefaultWindowsCPath = ".\\?.dll;.\\?53.dll;C:\\Program Files\\Lua\\5.3\\?.dll;C:\\Program Files\\Lua\\5.3\\?53.dll;C:\\Program Files\\Lua\\5.3\\clibs\\?.dll;C:\\Program Files\\Lua\\5.3\\clibs\\?53.dll;C:\\Program Files\\Lua\\5.3\\loadall.dll;C:\\Program Files\\Lua\\5.3\\clibs\\loadall.dll"
	// CLoadingPolicyText 说明当前项目默认 Lua C 动态库 loader 的固定策略。
	CLoadingPolicyText = "built-in dynamic C library loading is disabled in the default CGO-free build to keep cross-system builds simple; embedding programs may register their own loader"
	// defaultPathSeparator 是 package.path 多模板之间的分隔符。
	defaultPathSeparator = ";"
	// defaultTemplateMark 是 package.path 中用于替换模块路径的占位符。
	defaultTemplateMark = "?"
	// defaultModuleSeparator 是 Lua 模块名中层级分隔符。
	defaultModuleSeparator = "."
	// defaultDirectorySeparator 是当前 Unix 风格平台下的目录分隔符。
	defaultDirectorySeparator = "/"
)

// Environment 保存单个 State 对应的 package 标准库运行环境。
//
// 由于当前 Go closure 调用签名不携带 State，require/loadlib 通过闭包捕获该结构访问
// package.loaded、package.preload 和 package.searchers 等表；后续 VM 调用上下文完善后可再
// 下沉到 State registry。
type Environment struct {
	// table 保存全局 package 表。
	table *runtime.Table
	// loaded 保存 package.loaded 模块缓存。
	loaded *runtime.Table
	// preload 保存 package.preload 预加载 loader。
	preload *runtime.Table
	// searchers 保存 require 使用的模块搜索器列表。
	searchers *runtime.Table
	// luaFileLoader 根据已命中的 Lua 文件路径生成 loader。
	luaFileLoader LuaFileLoader
	// loaderCaller 执行 require 找到的 loader，宿主可用它接入 Lua closure 调用。
	loaderCaller LoaderCaller
	// dynamicLibraryLoader 保存 package.loadlib 和 C searcher 使用的宿主动态库接入点。
	dynamicLibraryLoader DynamicLibraryLoader
	// options 保存当前 State 的文件系统与环境变量权限策略。
	options runtime.Options
}

// LuaFileLoader 表示 Lua 文件 searcher 命中文件后生成 loader 的回调。
//
// filename 是 package.path 展开后已确认可读的路径；返回的 Go closure 必须在 require 调用时
// 加载并执行该文件，返回值按 Lua 5.3 require 规则写入 package.loaded。
type LuaFileLoader func(filename string) runtime.GoResultsFunction

// LoaderCaller 表示 require 执行 loader 的回调。
//
// loader 可以是 Go closure 或 Lua closure；args 是 require 按 Lua 5.3 传入的模块名和可选
// loader data。默认实现只支持 Go closure，lua 包会注入可执行 Lua closure 的实现。
type LoaderCaller func(loader runtime.Value, args ...runtime.Value) ([]runtime.Value, error)

// DynamicLibraryLoader 表示宿主提供的可选动态库 loader。
//
// filename 是 package.loadlib 参数或 package.cpath 命中的候选文件；symbol 是 Lua 5.3 规则生成的
// luaopen_* 符号。返回值必须是 Lua 可调用函数；错误会转换为 package.loadlib 的 nil,error,where
// 三返回而不是破坏默认 CGO-free 构建。
type DynamicLibraryLoader func(filename string, symbol string) (runtime.Value, error)

// DynamicLibraryError 表示动态库 loader 的兼容失败分类。
//
// Category 对齐 package.loadlib 第三个返回值，常见值包括 absent、open 和 init；Message 是第二个
// 返回值。宿主返回普通 error 时会被归类为 open。
type DynamicLibraryError struct {
	// Category 保存 package.loadlib 第三个返回值分类。
	Category string
	// Message 保存面向 Lua 的动态库加载错误文本。
	Message string
}

// Error 返回动态库加载错误文本。
//
// Message 为空时回退到 CLoadingPolicy，避免 package.loadlib 返回空错误字符串。
func (err DynamicLibraryError) Error() string {
	// 空 Message 使用集中策略文本兜底。
	if err.Message == "" {
		return CLoadingPolicy()
	}
	return err.Message
}

// Open 将 Lua 5.3 package 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后全局 `package` 字段指向库表，并注册 require 全局函数。
// 当前 require 支持 package.loaded 缓存命中与 package.searchers 中 Go loader 的阶段性加载。
func Open(state *runtime.State) error {
	// 默认注册不提供 Lua 文件执行器，适合底层 stdlib 单测和纯 package 表构造。
	return OpenWithLuaFileLoader(state, nil)
}

// OpenWithLuaFileLoader 将 package 标准库注册到 State，并接入 Lua 文件 loader。
//
// state 必须非 nil 且未关闭；luaFileLoader 为 nil 时，Lua 文件 searcher 只报告候选文件未命中，
// 不执行源码文件。非 nil 时，searcher 会先按 package.path 查找可读文件，再返回 loader。
func OpenWithLuaFileLoader(state *runtime.State, luaFileLoader LuaFileLoader) error {
	// 注册入口先校验 State 生命周期，避免向关闭后的全局表写入库函数。
	if state == nil {
		// nil State 没有 globals，调用方需要先创建 runtime.State。
		return fmt.Errorf("package library unavailable: %w", runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("package library unavailable: %w", runtime.ErrClosedState)
	}

	environment := NewEnvironmentWithOptions(luaFileLoader, state.Options())
	// require 作为全局函数注册，符合 Lua 5.3 基础库可见性。
	state.SetGlobal("require", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.Require)))
	state.SetGlobal("package", runtime.ReferenceValue(runtime.KindTable, environment.table))
	return nil
}

// NewEnvironment 创建 package 标准库运行环境。
//
// 返回环境包含 package 表、loaded/preload/searchers 表、path/cpath/config 常量和 loadlib
// 函数。默认 searchers 注册 preload、Lua/GLua 文件、C 和 C root 搜索器；C 搜索器在无宿主
// loader 时只返回候选诊断，不打开动态库。
func NewEnvironment() *Environment {
	// 默认环境不接入 Lua 文件执行器，调用方可使用 NewEnvironmentWithLuaFileLoader 覆盖。
	return NewEnvironmentWithLuaFileLoader(nil)
}

// NewEnvironmentWithLuaFileLoader 创建带 Lua 文件 loader 扩展点的 package 标准库运行环境。
//
// luaFileLoader 可以为 nil；nil 时 package.searchpath 仍会访问文件系统检查候选文件，但
// LuaSearcher 不会返回可执行 loader，避免底层包自行依赖 lua API。
func NewEnvironmentWithLuaFileLoader(luaFileLoader LuaFileLoader) *Environment {
	// 无 State 的环境保持历史测试行为，允许宿主文件系统参与 package.searchpath。
	return NewEnvironmentWithOptions(luaFileLoader, runtime.Options{AllowHostFilesystem: true, AllowEnvironment: true})
}

// NewEnvironmentWithLoaders 创建带 Lua 文件和动态库 loader 扩展点的 package 标准库运行环境。
//
// luaFileLoader 可以为 nil；dynamicLibraryLoader 只供 package.loadlib 使用，默认 require 不注册
// C searcher 和 C root searcher。
func NewEnvironmentWithLoaders(luaFileLoader LuaFileLoader, dynamicLibraryLoader DynamicLibraryLoader) *Environment {
	// 兼容旧入口：在历史测试权限基础上接入动态库 loader。
	return NewEnvironmentWithOptions(luaFileLoader, runtime.Options{
		AllowHostFilesystem:         true,
		AllowEnvironment:            true,
		PackageDynamicLibraryLoader: dynamicLibraryLoader,
	})
}

// NewEnvironmentWithOptions 创建带 Lua 文件 loader 与权限策略的 package 标准库运行环境。
//
// luaFileLoader 可以为 nil；options 控制 package.path 环境变量读取、Lua 文件候选可读性检查和
// VFS/宿主文件系统优先级，并可通过 PackageDynamicLibraryLoader 接入可选动态库 loader。
func NewEnvironmentWithOptions(luaFileLoader LuaFileLoader, options runtime.Options) *Environment {
	// 初始化 package 表、loaded 缓存表和 searcher 相关表。
	packageTable := runtime.NewTable()
	loadedTable := runtime.NewTable()
	preloadTable := runtime.NewTable()
	searchersTable := runtime.NewTable()
	environment := &Environment{
		table:                packageTable,
		loaded:               loadedTable,
		preload:              preloadTable,
		searchers:            searchersTable,
		luaFileLoader:        luaFileLoader,
		loaderCaller:         callGoResults,
		dynamicLibraryLoader: DynamicLibraryLoader(options.PackageDynamicLibraryLoader),
		options:              runtime.NormalizeOptions(options),
	}
	packageTable.RawSetString("config", runtime.StringValue(DefaultConfig))
	packageTable.RawSetString("cpath", runtime.StringValue(resolveConfiguredPath(environment.options, "LUA_CPATH_5_3", "LUA_CPATH", DefaultCPathForGOOS(goRuntime.GOOS))))
	packageTable.RawSetString("loaded", runtime.ReferenceValue(runtime.KindTable, loadedTable))
	packageTable.RawSetString("loadlib", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.LoadLib)))
	packageTable.RawSetString("path", runtime.StringValue(resolveConfiguredPath(environment.options, "LUA_PATH_5_3", "LUA_PATH", DefaultPath)))
	packageTable.RawSetString("preload", runtime.ReferenceValue(runtime.KindTable, preloadTable))
	packageTable.RawSetString("searchers", runtime.ReferenceValue(runtime.KindTable, searchersTable))
	packageTable.RawSetString("searchpath", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.SearchPath)))
	searchersTable.RawSetInteger(1, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.PreloadSearcher)))
	searchersTable.RawSetInteger(2, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.LuaSearcher)))
	searchersTable.RawSetInteger(3, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.CSearcher)))
	searchersTable.RawSetInteger(4, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(environment.CRootSearcher)))
	return environment
}

// resolveConfiguredPath 按 State 权限解析 Lua 5.3 package.path/package.cpath。
//
// AllowEnvironment 为 false 时直接使用默认路径，避免嵌入模式泄漏宿主环境变量；授权后才读取
// LUA_PATH/LUA_CPATH 等环境变量并展开默认路径标记。
func resolveConfiguredPath(options runtime.Options, versionedName string, genericName string, defaultValue string) string {
	// 环境变量访问需要宿主显式授权。
	if !options.AllowEnvironment {
		return defaultValue
	}
	return resolveEnvironmentPath(versionedName, genericName, defaultValue)
}

// SetLoaderCaller 设置 require 执行 loader 的回调。
//
// caller 为 nil 时恢复默认 Go closure 调用器；非 nil caller 可让宿主接入 Lua closure loader。
func (environment *Environment) SetLoaderCaller(caller LoaderCaller) {
	if environment == nil {
		// nil 环境没有可设置的调用器。
		return
	}
	if caller == nil {
		// nil caller 恢复默认 Go closure 行为。
		environment.loaderCaller = callGoResults
		return
	}
	environment.loaderCaller = caller
}

// SetDynamicLibraryLoader 设置 package.loadlib 和 C searcher 使用的动态库 loader。
//
// loader 为 nil 时恢复默认 CGO-free 禁用行为；非 nil loader 只保存回调，不会主动打开任何动态库。
func (environment *Environment) SetDynamicLibraryLoader(loader DynamicLibraryLoader) {
	// nil 环境没有可设置的动态库 loader。
	if environment == nil {
		return
	}
	environment.dynamicLibraryLoader = loader
}

// resolveEnvironmentPath 解析 Lua 5.3 package.path/package.cpath 的环境变量覆盖。
//
// versionedName 优先于 genericName；任一变量存在时使用其原始值，并把 `;;` 替换为嵌入默认
// 路径的形式，兼容 Lua 5.3 对 LUA_PATH 与 LUA_CPATH 的启动期处理。
func resolveEnvironmentPath(versionedName string, genericName string, defaultValue string) string {
	if value, ok := os.LookupEnv(versionedName); ok {
		// 版本专用变量优先级最高，例如 LUA_PATH_5_3。
		return expandDefaultPath(value, defaultValue)
	}
	if value, ok := os.LookupEnv(genericName); ok {
		// 通用变量在版本专用变量不存在时生效。
		return expandDefaultPath(value, defaultValue)
	}
	return defaultValue
}

// expandDefaultPath 展开 package 路径中的默认路径标记。
//
// Lua 5.3 使用 `;;` 表示在该位置插入默认路径，并保留两侧路径分隔符；没有默认标记时
// 直接返回环境变量原值。
func expandDefaultPath(path string, defaultValue string) string {
	if !strings.Contains(path, ";;") {
		// 没有默认路径标记时保持环境变量原值。
		return path
	}
	return strings.ReplaceAll(path, ";;", defaultPathSeparator+defaultValue+defaultPathSeparator)
}

// Table 返回当前 package 表。
//
// 返回值用于测试和后续标准库组合注册；调用方不得把 nil Environment 用作有效库环境。
func (environment *Environment) Table() *runtime.Table {
	// nil 环境没有 package 表。
	if environment == nil {
		return nil
	}
	return environment.table
}

// Loaded 返回当前 package.loaded 表。
//
// 返回值用于测试、预加载标准库和后续 require searcher 写回模块缓存。
func (environment *Environment) Loaded() *runtime.Table {
	// nil 环境没有 loaded 表。
	if environment == nil {
		return nil
	}
	return environment.loaded
}

// Preload 返回当前 package.preload 表。
//
// 返回值用于测试和 Go 侧预注册 Lua 模块 loader；nil Environment 返回 nil。
func (environment *Environment) Preload() *runtime.Table {
	// nil 环境没有 preload 表。
	if environment == nil {
		return nil
	}
	return environment.preload
}

// Searchers 返回当前 package.searchers 表。
//
// 返回值用于测试和后续扩展模块搜索策略；nil Environment 返回 nil。
func (environment *Environment) Searchers() *runtime.Table {
	// nil 环境没有 searchers 表。
	if environment == nil {
		return nil
	}
	return environment.searchers
}

// RegisterPreload 注册 package.preload 中的预加载模块 loader。
//
// moduleName 必须是非空模块名；loader 必须非 nil。注册后的 loader 会被 require 的
// package.searchers[1] 命中并执行，返回值按 Lua 5.3 require 规则写入 package.loaded。
func (environment *Environment) RegisterPreload(moduleName string, loader runtime.GoResultsFunction) error {
	// 注册前先确认 package 环境和 preload 表可用。
	if environment == nil || environment.preload == nil {
		// 环境缺失通常表示标准库未正确初始化。
		return fmt.Errorf("package library is not initialized")
	}
	if moduleName == "" {
		// 空模块名无法被 require 稳定索引。
		return fmt.Errorf("module name is empty")
	}
	if loader == nil {
		// nil loader 被调用时会变成不可调用错误，因此注册阶段直接拒绝。
		return fmt.Errorf("preload loader is nil")
	}

	environment.preload.RawSetString(moduleName, runtime.ReferenceValue(runtime.KindGoClosure, loader))
	return nil
}

// RegisterGoModule 注册 Go 实现的 Lua 模块 loader。
//
// 本项目的 Go 模块 loader 设计为 package.preload 的纯 Go 扩展：Go 侧把模块 loader 注册到
// preload 表，Lua 侧仍通过标准 require 搜索顺序加载，不增加非 Lua 5.3 标准 searcher 槽位。
func (environment *Environment) RegisterGoModule(moduleName string, loader runtime.GoResultsFunction) error {
	// Go 模块 loader 复用 preload 机制，保持 require 行为和 package.searchers 兼容 Lua 5.3。
	return environment.RegisterPreload(moduleName, loader)
}

// CLoadingSupported 返回当前 package 库是否内置支持 C 动态库 loader。
//
// 本项目默认构建禁止 CGO，并且内置 package 库不接入 Lua C API，因此该方法固定返回 false。
// 嵌入方可以在自己的宿主程序中注册自定义 loader 或覆盖 package.loadlib，这不改变默认内置状态。
func CLoadingSupported() bool {
	// 默认无 CGO 策略是跨系统编译边界，不能按运行时环境隐式切换。
	return false
}

// CLoadingPolicy 返回当前 package 库的 C 动态库 loader 策略说明。
//
// 返回文本用于测试、文档和错误消息，说明内置 loadlib、C searcher 与 C root searcher 均不启用。
func CLoadingPolicy() string {
	// 策略文本集中维护，避免多个 loader 分支产生不一致说明。
	return CLoadingPolicyText
}

// DefaultCPathForGOOS 返回指定平台的默认动态库搜索模板。
//
// goos 必须使用 Go 的 GOOS 名称；Windows 只返回 .dll 运行期候选，.lib/import library 属于链接期
// 产物，不作为 require 的运行期加载路径。Linux/macOS 默认包含 .so 与 .dylib 候选，便于宿主跨
// Unix 系统复用 package.cpath。
func DefaultCPathForGOOS(goos string) string {
	// Windows 运行期动态库加载只使用 .dll 候选。
	if goos == "windows" {
		return DefaultWindowsCPath
	}
	return DefaultCPath
}

// DynamicLibraryPlatformNote 返回动态库 loader 的平台边界说明。
//
// goos 必须使用 Go 的 GOOS 名称；返回文本用于 C searcher 诊断和测试，明确默认构建不自动打开
// 动态库，以及 Windows 下 .dll 与 .lib/import library 的边界。
func DynamicLibraryPlatformNote(goos string) string {
	// Windows 需要明确 .dll 运行期加载和 .lib 链接期/import library 的支持边界。
	if goos == "windows" {
		return "Windows dynamic loader candidates use .dll for runtime loading; .lib/import library files are link-time artifacts and are not runtime require candidates"
	}
	// Unix 风格平台保留 .so 与 .dylib 候选，具体打开方式由宿主 loader 决定。
	return "Unix dynamic loader candidates include .so and .dylib; actual loading is enabled only when the embedding program registers a loader"
}

// Require 实现 Lua 5.3 `require` 的阶段性语义。
//
// 第一个参数必须是模块名 string。当前先查询 package.loaded；命中 truthy 值时直接返回。
// 未命中时按 package.searchers 查找 Go loader；Lua 文件 loader 和 C loader 当前返回明确禁用文本。
func (environment *Environment) Require(args ...runtime.Value) ([]runtime.Value, error) {
	// require 首先解析模块名。
	moduleName, err := stringArgument(args, 1, "require")
	if err != nil {
		// 模块名不是 string 时返回 Lua 参数错误。
		return nil, err
	}
	if environment == nil || environment.loaded == nil {
		// 环境缺失通常表示标准库未正确打开。
		return nil, runtime.RaiseError(runtime.StringValue("package library is not initialized"))
	}

	loadedValue := environment.loaded.RawGetString(moduleName)
	if loadedValue.Truthy() {
		// loaded 中的 truthy 值表示模块已经加载，直接返回缓存。
		return []runtime.Value{loadedValue}, nil
	}

	loader, loaderData, searchMessages, err := environment.findLoader(moduleName)
	if err != nil {
		// searcher 执行期间出现 Lua 错误时，require 直接传播该错误。
		return nil, err
	}
	if loader.IsNil() {
		// 未找到 loader 时合并所有 searcher 的错误文本，便于定位模块解析路径。
		return nil, runtime.RaiseError(runtime.StringValue(moduleNotFoundMessage(moduleName, searchMessages)))
	}

	loaderArgs := []runtime.Value{runtime.StringValue(moduleName)}
	if !loaderData.IsNil() {
		// Lua 5.3 会把 searcher 返回的第二个值作为 loader data 传给 loader。
		loaderArgs = append(loaderArgs, loaderData)
	}
	loaderCaller := environment.loaderCaller
	if loaderCaller == nil {
		// 兼容手工构造 Environment 时漏设调用器的情况。
		loaderCaller = callGoResults
	}
	results, err := loaderCaller(loader, loaderArgs...)
	if err != nil {
		// loader 运行错误需要作为 require 的 Lua 错误向外传播。
		return nil, err
	}

	loadedValue = environment.loaded.RawGetString(moduleName)
	if loadedValue.Truthy() {
		// loader 自行写入 package.loaded 时，以写入值为最终模块值。
		return []runtime.Value{loadedValue}, nil
	}
	if len(results) > 0 && !results[0].IsNil() {
		// loader 返回非 nil 模块值时写入 loaded 缓存。
		environment.loaded.RawSetString(moduleName, results[0])
		return []runtime.Value{results[0]}, nil
	}

	// loader 未返回模块值时 Lua 5.3 会把 package.loaded[name] 置为 true。
	loadedValue = runtime.BooleanValue(true)
	environment.loaded.RawSetString(moduleName, loadedValue)
	return []runtime.Value{loadedValue}, nil
}

// LoadLib 实现 Lua 5.3 `package.loadlib` 的无 CGO 策略。
//
// filename 和 symbol 参数都必须是 string。当前默认构建不绑定外部动态库，内置 loadlib
// 返回 nil 与错误文本；宿主程序可通过覆盖 package.loadlib 提供自定义实现。
func (environment *Environment) LoadLib(args ...runtime.Value) ([]runtime.Value, error) {
	// filename 必须是 string。
	filename, err := stringArgument(args, 1, "loadlib")
	if err != nil {
		// 第一个参数错误直接返回。
		return nil, err
	}
	symbol, err := stringArgument(args, 2, "loadlib")
	if err != nil {
		// 第二个参数错误直接返回。
		return nil, err
	}
	if loadLibDiagnosticMode() == "before-loader-fixed" && loadLibDiagnosticApplies(filename) {
		// 诊断模式在调用宿主 loader 前直接返回三返回，用于隔离 LoadLib 固定失败分支。
		return diagnosticLoadLibFailure(filename, "before-loader-fixed"), nil
	}
	loader, loadErr := environment.loadDynamicLibrary(filename, symbol)
	if loadLibDiagnosticMode() == "after-loader-fixed" && loadLibDiagnosticApplies(filename) {
		// 诊断模式已调用宿主 loader，但跳过 dynamicLibraryFailure，直接返回固定三返回。
		return diagnosticLoadLibFailure(filename, "after-loader-fixed"), nil
	}
	if loadErr == nil && (loader.Kind == runtime.KindGoClosure || loader.Kind == runtime.KindLuaClosure) {
		// 宿主 loader 返回 Lua 可调用函数时，package.loadlib 成功。
		return []runtime.Value{loader}, nil
	}
	if loadErr == nil {
		// nil 错误但返回值不可调用时视为符号初始化失败，避免 require 后续调用崩溃。
		loadErr = DynamicLibraryError{Category: "init", Message: "dynamic library loader did not return a callable Lua function"}
	}
	message, category := dynamicLibraryFailure(loadErr)
	return []runtime.Value{
		runtime.NilValue(),
		runtime.StringValue(message),
		runtime.StringValue(category),
	}, nil
}

// loadLibDiagnosticMode 返回 package.loadlib 失败分支诊断模式。
//
// 该环境变量只供 LPeg/native loader 边界定位脚本使用，不属于公开 API；未设置时不改变默认语义。
func loadLibDiagnosticMode() string {
	// 每次 loadlib 调用时读取环境变量，便于同一个 glua 二进制执行不同诊断脚本。
	return os.Getenv("GLUA_PACKAGE_LOADLIB_DIAGNOSTIC")
}

// loadLibDiagnosticApplies 判断当前 filename 是否命中 package.loadlib 诊断范围。
//
// GLUA_PACKAGE_LOADLIB_DIAGNOSTIC_MATCH 为空时诊断模式影响全部 loadlib 请求；非空时只匹配包含该片段的路径。
func loadLibDiagnosticApplies(filename string) bool {
	// 文件片段为空时允许手工调试直接覆盖所有 loadlib 请求。
	filenameFragment := os.Getenv("GLUA_PACKAGE_LOADLIB_DIAGNOSTIC_MATCH")
	if filenameFragment == "" {
		// 空匹配条件表示调用者明确要让诊断模式覆盖全部请求。
		return true
	}
	return strings.Contains(filename, filenameFragment)
}

// diagnosticLoadLibFailure 构造 package.loadlib 兼容的固定诊断三返回。
func diagnosticLoadLibFailure(filename string, mode string) []runtime.Value {
	// 诊断返回保持 nil,message,"open" 形态，方便与真实 native loader 失败路径对照。
	return []runtime.Value{
		runtime.NilValue(),
		runtime.StringValue(fmt.Sprintf("diagnostic package.loadlib failure at %s for %q", mode, filename)),
		runtime.StringValue("open"),
	}
}

// SearchPath 实现 Lua 5.3 `package.searchpath` 的模板解析与文件查找语义。
//
// name 和 path 必须是 string；sep 与 rep 可选且默认分别为 `.` 与 `/`。找到可读文件时返回
// 文件名；全部未命中时返回 nil 和候选路径错误文本。
func (environment *Environment) SearchPath(args ...runtime.Value) ([]runtime.Value, error) {
	// package.searchpath 先解析模块名和路径模板。
	moduleName, err := stringArgument(args, 1, "searchpath")
	if err != nil {
		// 模块名错误直接返回 Lua 参数错误。
		return nil, err
	}
	path, err := stringArgument(args, 2, "searchpath")
	if err != nil {
		// 路径模板错误直接返回 Lua 参数错误。
		return nil, err
	}

	separator := defaultModuleSeparator
	if len(args) >= 3 && !args[2].IsNil() {
		// 第三个参数存在且非 nil 时覆盖模块名分隔符。
		separator, err = stringArgument(args, 3, "searchpath")
		if err != nil {
			// sep 类型错误直接返回 Lua 参数错误。
			return nil, err
		}
	}

	replacement := defaultDirectorySeparator
	if len(args) >= 4 && !args[3].IsNil() {
		// 第四个参数存在且非 nil 时覆盖路径替换串。
		replacement, err = stringArgument(args, 4, "searchpath")
		if err != nil {
			// rep 类型错误直接返回 Lua 参数错误。
			return nil, err
		}
	}

	candidates := expandSearchPath(moduleName, path, separator, replacement)
	if filename, ok := firstReadableFileWithOptions(environment.options, candidates); ok {
		// 命中第一个可读候选文件时直接返回文件名。
		return []runtime.Value{runtime.StringValue(filename)}, nil
	}
	return []runtime.Value{runtime.NilValue(), runtime.StringValue(searchPathError(candidates))}, nil
}

// PreloadSearcher 实现 package.searchers[1] 的 package.preload 搜索器。
//
// 第一个参数必须是模块名 string；命中 package.preload[name] 时返回 loader，否则返回错误文本。
func (environment *Environment) PreloadSearcher(args ...runtime.Value) ([]runtime.Value, error) {
	// preload searcher 先解析模块名。
	moduleName, err := stringArgument(args, 1, "preload searcher")
	if err != nil {
		// 模块名错误直接返回 Lua 参数错误。
		return nil, err
	}
	if environment == nil || environment.preload == nil {
		// 环境缺失时返回普通搜索失败文本，由 require 汇总。
		return []runtime.Value{runtime.StringValue(fmt.Sprintf("\n\tno field package.preload['%s']", moduleName))}, nil
	}

	loader := environment.preload.RawGetString(moduleName)
	if loader.Kind == runtime.KindGoClosure || loader.Kind == runtime.KindLuaClosure {
		// 预加载表命中 Go 或 Lua loader 时交给 require 执行。
		return []runtime.Value{loader}, nil
	}
	return []runtime.Value{runtime.StringValue(fmt.Sprintf("\n\tno field package.preload['%s']", moduleName))}, nil
}

// LuaSearcher 实现 package.searchers[2] 的 Lua 文件搜索器。
//
// searcher 会按 package.path 查找第一个可读文件；命中且已接入 luaFileLoader 时返回 loader，
// 否则返回 Lua 5.3 风格的未命中错误文本。
func (environment *Environment) LuaSearcher(args ...runtime.Value) ([]runtime.Value, error) {
	// Lua 文件 searcher 先解析模块名。
	moduleName, err := stringArgument(args, 1, "Lua searcher")
	if err != nil {
		// 模块名错误直接返回 Lua 参数错误。
		return nil, err
	}

	if environment == nil || environment.table == nil {
		// 环境缺失时不能读取 package.path，按 Lua 错误直接中断 require。
		return nil, runtime.RaiseError(runtime.StringValue("package.path must be a string"))
	}
	pathValue := environment.table.RawGetString("path")
	if pathValue.Kind != runtime.KindString {
		// package.path 被用户改成非 string 时，官方 require 会报错而不是回退默认路径。
		return nil, runtime.RaiseError(runtime.StringValue("package.path must be a string"))
	}
	path := pathValue.String

	candidates := expandSearchPath(moduleName, path, defaultModuleSeparator, defaultDirectorySeparator)
	filename, ok := firstReadableFileWithOptions(environment.options, candidates)
	if ok && environment.luaFileLoader != nil {
		// 文件存在且执行器可用时返回 loader，require 会负责执行并缓存模块结果。
		loader := environment.luaFileLoader(filename)
		if loader != nil {
			// loader 非 nil 表示宿主可执行该 Lua 文件。
			return []runtime.Value{runtime.ReferenceValue(runtime.KindGoClosure, loader), runtime.StringValue(filename)}, nil
		}
	}
	return []runtime.Value{runtime.StringValue(searchPathError(candidates))}, nil
}

// CSearcher 实现 package.searchers[3] 的 C 动态库搜索器。
//
// 默认 CGO-free 构建没有内置 loader；宿主注册动态库 loader 后，会按 package.cpath 命中候选并
// 返回 luaopen_* 符号对应的 Lua 可调用 loader。
func (environment *Environment) CSearcher(args ...runtime.Value) ([]runtime.Value, error) {
	// C searcher 只需要校验模块名，保证参数错误与 Lua searcher 一致。
	moduleName, err := stringArgument(args, 1, "C searcher")
	if err != nil {
		// 模块名错误直接返回 Lua 参数错误。
		return nil, err
	}
	candidates := environment.cpathCandidates(moduleName)
	return environment.searchDynamicLibrary(moduleName, moduleName, candidates, "C loader")
}

// CRootSearcher 实现 package.searchers[4] 的 C root 动态库搜索器。
//
// 默认 CGO-free 构建没有内置 loader；对 a.b 这类模块名，root searcher 按 a 展开 cpath 候选，
// 但仍使用完整模块名生成 luaopen_a_b 符号。
func (environment *Environment) CRootSearcher(args ...runtime.Value) ([]runtime.Value, error) {
	// C root searcher 只需要校验模块名，保证参数错误与 Lua searcher 一致。
	moduleName, err := stringArgument(args, 1, "C root searcher")
	if err != nil {
		// 模块名错误直接返回 Lua 参数错误。
		return nil, err
	}
	rootName := cRootModuleName(moduleName)
	if rootName == "" {
		// 没有 root 模块时保留 searcher 未命中文本，由 require 继续汇总。
		return []runtime.Value{runtime.StringValue(fmt.Sprintf("\n\tno C root module for '%s'", moduleName))}, nil
	}
	candidates := environment.cpathCandidates(rootName)
	return environment.searchDynamicLibrary(moduleName, rootName, candidates, "C root loader")
}

// loadDynamicLibrary 调用宿主注册的动态库 loader。
//
// filename 和 symbol 必须来自 package.loadlib 参数或 C searcher 候选；无宿主 loader 时返回 absent
// 分类错误，保持默认 CGO-free 构建的兼容三返回语义。
func (environment *Environment) loadDynamicLibrary(filename string, symbol string) (runtime.Value, error) {
	// nil 环境或 nil loader 都表示默认构建未启用动态库加载。
	if environment == nil || environment.dynamicLibraryLoader == nil {
		return runtime.NilValue(), DynamicLibraryError{Category: "absent", Message: CLoadingPolicy()}
	}
	loader, err := environment.dynamicLibraryLoader(filename, symbol)
	if err != nil {
		// 宿主 loader 错误转交给上层统一转换为 loadlib 三返回。
		return runtime.NilValue(), err
	}
	return loader, nil
}

// searchDynamicLibrary 按 package.cpath 候选查找并加载动态库 loader。
//
// moduleName 用于生成 luaopen_* 符号；searchName 是 cpath 展开使用的模块名。返回 loader 时第二
// 返回值为命中文件名，未命中时返回可被 require 汇总的诊断文本。
func (environment *Environment) searchDynamicLibrary(moduleName string, searchName string, candidates []string, loaderKind string) ([]runtime.Value, error) {
	symbol := dynamicLibrarySymbol(moduleName)
	if environment == nil || environment.dynamicLibraryLoader == nil {
		// 默认 CGO-free 构建不尝试打开候选文件，只报告候选和平台边界。
		return []runtime.Value{runtime.StringValue(dynamicLibrarySearchMessage(moduleName, searchName, candidates, loaderKind, DynamicLibraryError{Category: "absent", Message: CLoadingPolicy()}))}, nil
	}
	var lastLoadErr error
	for _, candidate := range candidates {
		// C searcher 按 package.cpath 候选顺序逐个交给宿主 loader。
		loader, err := environment.loadDynamicLibrary(candidate, symbol)
		if err != nil {
			// 单个候选失败时继续尝试后续候选，最终诊断会汇总候选与最后错误。
			lastLoadErr = err
			continue
		}
		if loader.Kind == runtime.KindGoClosure || loader.Kind == runtime.KindLuaClosure {
			// 命中可调用 loader 时返回 loader 与文件名 loader data。
			return []runtime.Value{loader, runtime.StringValue(candidate)}, nil
		}
		// 宿主 loader 成功返回但不可调用，记录 init 分类并继续尝试后续候选。
		lastLoadErr = DynamicLibraryError{Category: "init", Message: "dynamic library loader did not return a callable Lua function"}
	}
	if lastLoadErr == nil {
		// 没有候选或没有 loader 结果时按打开失败给出稳定诊断。
		lastLoadErr = DynamicLibraryError{Category: "open", Message: "dynamic library loader did not return a callable Lua function"}
	}
	message := dynamicLibrarySearchMessage(moduleName, searchName, candidates, loaderKind, lastLoadErr)
	return []runtime.Value{runtime.StringValue(message)}, nil
}

// dynamicLibrarySearchMessage 构造 C searcher 的未命中诊断文本。
//
// moduleName 是 require 模块名；searchName 是 cpath 展开名；failure 表示当前 loader 失败分类。
// 返回文本包含候选路径、平台说明和 loadlib 兼容错误信息。
func dynamicLibrarySearchMessage(moduleName string, searchName string, candidates []string, loaderKind string, failure error) string {
	message := searchPathError(candidates)
	failureMessage, category := dynamicLibraryFailure(failure)
	status := "failed"
	if category == "absent" {
		// absent 表示默认构建未启用动态库 loader，保留旧诊断里的 disabled 关键词。
		status = "disabled"
	}
	message += fmt.Sprintf("\n\t%s %s for module '%s' using '%s' (%s): %s", loaderKind, status, moduleName, searchName, category, failureMessage)
	message += "\n\t" + DynamicLibraryPlatformNote(goRuntime.GOOS)
	return message
}

// dynamicLibraryFailure 把宿主 loader 错误转换为 package.loadlib 兼容三返回中的文本和分类。
//
// nil 错误表示成功路径，不应调用该函数；普通 error 默认归类为 open，DynamicLibraryError 可覆盖
// absent/open/init 等 Lua 5.3 兼容分类。
func dynamicLibraryFailure(err error) (string, string) {
	// nil 错误没有失败信息，使用策略文本兜底。
	if err == nil {
		return CLoadingPolicy(), "absent"
	}
	var dynamicErr DynamicLibraryError
	if errors.As(err, &dynamicErr) {
		// 宿主显式分类时使用其 Message 和 Category。
		category := dynamicErr.Category
		if category == "" {
			// 空分类按打开失败处理，避免第三返回值为空。
			category = "open"
		}
		return dynamicErr.Error(), category
	}
	return err.Error(), "open"
}

// dynamicLibrarySymbol 按 Lua 5.3 C loader 规则生成 luaopen_* 符号名。
//
// moduleName 是 require 模块名；点号和连字符会映射为下划线，保持 Go 侧可测试的稳定符号。
func dynamicLibrarySymbol(moduleName string) string {
	// luaopen_ 前缀是 Lua 5.3 C 模块入口约定。
	replacer := strings.NewReplacer(".", "_", "-", "_")
	return "luaopen_" + replacer.Replace(moduleName)
}

// cRootModuleName 返回 C root searcher 使用的根模块名。
//
// moduleName 必须是 require 模块名；没有点号时返回空字符串，表示 C root searcher 不适用。
func cRootModuleName(moduleName string) string {
	dotIndex := strings.Index(moduleName, ".")
	if dotIndex <= 0 {
		// 没有 root 前缀时不能生成 C root 候选。
		return ""
	}
	return moduleName[:dotIndex]
}

// cpathCandidates 按当前 package.cpath 展开 C loader 候选路径。
//
// moduleName 是 require 传入的模块名；package.cpath 非 string 时返回空候选，调用方仍会
// 附加纯 Go 禁用说明，避免错误文本误导为可动态加载。
func (environment *Environment) cpathCandidates(moduleName string) []string {
	if environment == nil || environment.table == nil {
		// 环境缺失时没有 package.cpath 可读。
		return nil
	}
	cpathValue := environment.table.RawGetString("cpath")
	if cpathValue.Kind != runtime.KindString {
		// 非 string cpath 不参与候选展开。
		return nil
	}

	// C 模块搜索使用 package.cpath，并按点号模块名映射为目录分隔符。
	return expandSearchPath(moduleName, cpathValue.String, defaultModuleSeparator, defaultDirectorySeparator)
}

// findLoader 遍历 package.searchers 并返回第一个 Go loader。
//
// moduleName 必须是已解析的模块名。返回 loader 为 nil 表示未找到；loaderData 保存 searcher
// 返回的第二个结果，后续 require 会把它作为 loader 的第二个参数。
func (environment *Environment) findLoader(moduleName string) (runtime.Value, runtime.Value, []string, error) {
	searchers := environment.searchers
	if environment.table != nil {
		// require 必须观察用户对 package.searchers 的运行时改写。
		searchersValue := environment.table.RawGetString("searchers")
		if searchersValue.Kind != runtime.KindTable {
			// Lua 5.3 在 package.searchers 不是 table 时直接抛错。
			return runtime.NilValue(), runtime.NilValue(), nil, runtime.RaiseError(runtime.StringValue("package.searchers must be a table"))
		}
		tableValue, ok := searchersValue.Ref.(*runtime.Table)
		if !ok || tableValue == nil {
			// 损坏的 table 引用同样视为 searchers 不可用。
			return runtime.NilValue(), runtime.NilValue(), nil, runtime.RaiseError(runtime.StringValue("package.searchers must be a table"))
		}
		searchers = tableValue
	}
	// searcher 列表缺失时无法继续查找。
	if searchers == nil {
		return runtime.NilValue(), runtime.NilValue(), nil, nil
	}

	messages := make([]string, 0, 4)
	for index := int64(1); ; index++ {
		// package.searchers 使用 Lua 1-based 数组语义。
		searcher := searchers.RawGetInteger(index)
		if searcher.IsNil() {
			// 遇到 nil searcher 表示列表结束。
			break
		}

		results, err := callGoResults(searcher, runtime.StringValue(moduleName))
		if err != nil {
			// searcher 自身错误需要立即中止 require。
			return runtime.NilValue(), runtime.NilValue(), messages, err
		}
		if len(results) == 0 {
			// 空返回值表示未命中且无额外错误文本。
			continue
		}
		if results[0].Kind == runtime.KindGoClosure || results[0].Kind == runtime.KindLuaClosure {
			// 找到 loader 后立即返回，符合 Lua 5.3 searcher 顺序。
			loaderData := runtime.NilValue()
			if len(results) > 1 {
				// searcher 第二返回值是 loader data，例如 Lua 文件名。
				loaderData = results[1]
			}
			return results[0], loaderData, messages, nil
		}
		if results[0].Kind == runtime.KindString {
			// searcher 返回字符串时作为未命中的诊断文本。
			messages = append(messages, results[0].String)
		}
	}

	// 所有 searcher 都未返回 loader。
	return runtime.NilValue(), runtime.NilValue(), messages, nil
}

// callGoResults 调用当前 runtime 支持的 Go closure。
//
// function 必须是 KindGoClosure，Ref 支持 GoResultsFunction 和 GoFunction；其他类型返回
// Lua runtime callable 错误。该 helper 只服务 package searcher/loader 阶段性执行路径。
func callGoResults(function runtime.Value, args ...runtime.Value) ([]runtime.Value, error) {
	// 只有 Go closure 可以在当前阶段直接执行。
	if function.Kind != runtime.KindGoClosure {
		return nil, runtime.ErrExpectedCallable
	}

	switch goFunction := function.Ref.(type) {
	case runtime.GoResultsFunction:
		// GoResultsFunction 保留多返回值语义。
		if goFunction == nil {
			// nil 函数负载表示不可调用。
			return nil, runtime.ErrExpectedCallable
		}
		return goFunction(args...)
	case runtime.GoFunction:
		// GoFunction 单返回值需要适配为多返回值列表。
		if goFunction == nil {
			// nil 函数负载表示不可调用。
			return nil, runtime.ErrExpectedCallable
		}
		result, err := goFunction(args...)
		if err != nil {
			// GoFunction 错误直接交给调用方处理。
			return nil, err
		}
		return []runtime.Value{result}, nil
	default:
		// 未知闭包负载表示 callable 损坏。
		return nil, runtime.ErrExpectedCallable
	}
}

// expandSearchPath 根据模块名和 package.path 模板生成候选路径。
//
// moduleName 是 Lua 模块名；path 是以 `;` 分隔的模板列表；separator 和 replacement 控制
// 模块名层级替换。返回值保留模板顺序，便于错误文本稳定。
func expandSearchPath(moduleName string, path string, separator string, replacement string) []string {
	// 按 Lua 5.3 语义把模块名分隔符替换成目录分隔符。
	replacedName := moduleName
	if separator != "" {
		// 空 separator 无法被 strings.ReplaceAll 处理，非空时才替换。
		replacedName = strings.ReplaceAll(moduleName, separator, replacement)
	}

	templates := strings.Split(path, defaultPathSeparator)
	candidates := make([]string, 0, len(templates))
	for _, template := range templates {
		// 空模板没有可搜索路径，跳过避免生成误导候选。
		if template == "" {
			continue
		}
		candidates = append(candidates, strings.ReplaceAll(template, defaultTemplateMark, replacedName))
	}
	return candidates
}

// firstReadableFile 返回候选列表中的第一个普通可读文件。
//
// candidates 必须按 package.path 顺序传入；目录、缺失路径和无法打开路径都会被视为未命中。
func firstReadableFile(candidates []string) (string, bool) {
	// 无权限参数版本保持历史行为：允许宿主文件系统参与检查。
	return firstReadableFileWithOptions(runtime.Options{AllowHostFilesystem: true}, candidates)
}

// firstReadableFileWithOptions 返回候选列表中第一个按权限策略可读的普通文件。
//
// candidates 必须按 package.path 顺序传入；VFS 与宿主优先级由 options 控制。
func firstReadableFileWithOptions(options runtime.Options, candidates []string) (string, bool) {
	for _, candidate := range candidates {
		// 空路径没有可读文件语义，直接跳过。
		if candidate == "" {
			continue
		}
		if runtime.CanReadFileWithOptions(options, candidate) {
			// VFS 或宿主文件系统命中可读文件时返回候选原始名称，供错误文本和 chunk name 使用。
			return candidate, true
		}
	}

	// 所有候选都不可读。
	return "", false
}

// searchPathError 构造 package.searchpath 和 Lua 文件 loader 的错误文本。
//
// candidates 必须按搜索顺序传入；返回文本包含每个未命中的候选文件。
func searchPathError(candidates []string) string {
	// 无候选路径时给出明确文本。
	if len(candidates) == 0 {
		return "\n\tno file candidates"
	}

	builder := strings.Builder{}
	for _, candidate := range candidates {
		// 每个候选路径单独成行，接近 Lua 5.3 require 的错误排版。
		builder.WriteString("\n\tno file '")
		builder.WriteString(candidate)
		builder.WriteString("'")
	}
	return builder.String()
}

// moduleNotFoundMessage 构造 require 未找到模块时的 Lua error 文本。
//
// moduleName 是已解析模块名；messages 是 searcher 返回的错误文本，允许为空。
func moduleNotFoundMessage(moduleName string, messages []string) string {
	// 先写入 Lua 5.3 风格的模块未找到标题。
	builder := strings.Builder{}
	builder.WriteString("module '")
	builder.WriteString(moduleName)
	builder.WriteString("' not found:")
	for _, message := range messages {
		// searcher 文本已经带有换行缩进，直接拼接以保持可读性。
		builder.WriteString(message)
	}
	return builder.String()
}

// stringArgument 读取指定位置的 string 参数。
//
// position 使用 Lua 1-based 参数序号；参数缺失或不是 string 时返回 Lua 参数错误。
func stringArgument(args []runtime.Value, position int, functionName string) (string, error) {
	// 参数缺失时无法取得 string。
	if position <= 0 || position > len(args) {
		// Lua 标准库把缺失参数报告为 string expected。
		return "", badArgument(functionName, position, "string expected")
	}
	value := args[position-1]
	if value.Kind != runtime.KindString {
		// 非 string 参数返回 Lua 参数错误。
		return "", badArgument(functionName, position, "string expected")
	}
	return value.String, nil
}

// badArgument 构造 Lua 标准库参数错误。
//
// functionName 不包含库名前缀；position 使用 Lua 1-based 参数序号；detail 写入括号内的
// 具体约束说明。返回错误可被 errors.Is(err, runtime.ErrLuaError) 识别。
func badArgument(functionName string, position int, detail string) error {
	// 参数错误统一走 Lua error 对象，便于 pcall/xpcall 后续复用。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (%s)", position, functionName, detail)))
}

// ConfigLines 返回 package.config 的分行结果。
//
// 该 helper 用于测试配置文本结构，也便于后续平台策略扩展时集中校验。
func ConfigLines() []string {
	// TrimSuffix 去掉最后一个换行，避免 Split 产生尾部空字段。
	return strings.Split(strings.TrimSuffix(DefaultConfig, "\n"), "\n")
}
