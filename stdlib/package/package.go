// Package packagelib 实现 Lua 5.3 package 标准库的第一阶段能力。
//
// 本包当前提供 package 表、require 的 loaded/searchers 路径、package.config/path/cpath/loaded、
// package.preload、package.searchers、package.searchpath、Lua 文件 loader 和 package.loadlib 的
// 内置无 CGO 策略。该策略用于保持默认构建易跨系统编译；宿主程序仍可自行注册纯 Go、
// 自带 CGO 或系统动态库适配的 loader 覆盖默认行为。
package packagelib

import (
	"fmt"
	"os"
	"strings"

	"github.com/zing/go-lua-vm/runtime"
)

const (
	// DefaultConfig 是 Lua 5.3 package.config 在 Unix 风格平台下的基础文本。
	DefaultConfig = "/\n;\n?\n!\n-\n"
	// DefaultPath 是当前纯 Go package.path 默认模板。
	DefaultPath = "./?.lua;./?/init.lua"
	// DefaultCPath 是当前无 CGO 策略下保留的 Lua 5.3 兼容 C 模块搜索模板。
	DefaultCPath = "./?.so;./lua/?.so"
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
// 函数。该函数不访问宿主文件系统，也不启用 C 动态库。
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

// NewEnvironmentWithOptions 创建带 Lua 文件 loader 与权限策略的 package 标准库运行环境。
//
// luaFileLoader 可以为 nil；options 控制 package.path 环境变量读取、Lua 文件候选可读性检查和
// VFS/宿主文件系统优先级。该函数不启用 C 动态库加载。
func NewEnvironmentWithOptions(luaFileLoader LuaFileLoader, options runtime.Options) *Environment {
	// 初始化 package 表、loaded 缓存表和 searcher 相关表。
	packageTable := runtime.NewTable()
	loadedTable := runtime.NewTable()
	preloadTable := runtime.NewTable()
	searchersTable := runtime.NewTable()
	environment := &Environment{
		table:         packageTable,
		loaded:        loadedTable,
		preload:       preloadTable,
		searchers:     searchersTable,
		luaFileLoader: luaFileLoader,
		loaderCaller:  callGoResults,
		options:       runtime.NormalizeOptions(options),
	}
	packageTable.RawSetString("config", runtime.StringValue(DefaultConfig))
	packageTable.RawSetString("cpath", runtime.StringValue(resolveConfiguredPath(environment.options, "LUA_CPATH_5_3", "LUA_CPATH", DefaultCPath)))
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
	if _, err := stringArgument(args, 1, "loadlib"); err != nil {
		// 第一个参数错误直接返回。
		return nil, err
	}
	if _, err := stringArgument(args, 2, "loadlib"); err != nil {
		// 第二个参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{
		runtime.NilValue(),
		runtime.StringValue(CLoadingPolicy()),
		runtime.StringValue("absent"),
	}, nil
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
// 本项目禁止 CGO，因此该搜索器始终返回禁用文本，不尝试读取 package.cpath。
func (environment *Environment) CSearcher(args ...runtime.Value) ([]runtime.Value, error) {
	// C searcher 只需要校验模块名，保证参数错误与 Lua searcher 一致。
	moduleName, err := stringArgument(args, 1, "C searcher")
	if err != nil {
		// 模块名错误直接返回 Lua 参数错误。
		return nil, err
	}
	candidates := environment.cpathCandidates(moduleName)
	message := searchPathError(candidates)
	message += fmt.Sprintf("\n\tC loader disabled for module '%s': %s", moduleName, CLoadingPolicy())
	return []runtime.Value{runtime.StringValue(message)}, nil
}

// CRootSearcher 实现 package.searchers[4] 的 C root 动态库搜索器。
//
// 本项目禁止 CGO，因此该搜索器始终返回禁用文本，保留 Lua 5.3 searchers 形态。
func (environment *Environment) CRootSearcher(args ...runtime.Value) ([]runtime.Value, error) {
	// C root searcher 只需要校验模块名，保证参数错误与 Lua searcher 一致。
	moduleName, err := stringArgument(args, 1, "C root searcher")
	if err != nil {
		// 模块名错误直接返回 Lua 参数错误。
		return nil, err
	}
	candidates := environment.cpathCandidates(moduleName)
	message := searchPathError(candidates)
	message += fmt.Sprintf("\n\tC root loader disabled for module '%s': %s", moduleName, CLoadingPolicy())
	return []runtime.Value{runtime.StringValue(message)}, nil
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
