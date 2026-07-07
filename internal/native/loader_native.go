//go:build native_modules

package native

import (
	"fmt"
	"os"
	"strings"

	"github.com/ZingYao/go-lua-vm/runtime"
	packagelib "github.com/ZingYao/go-lua-vm/stdlib/package"
)

// Loader 返回 native_modules 构建下的无状态原生动态库 loader。
//
// 该入口只验证动态库和符号可解析；真实 luaopen_* 调用需要使用 LoaderForState 绑定当前
// runtime.State，避免 C 模块拿到错误的 lua_State* handle。
func Loader() func(filename string, symbol string) (runtime.Value, error) {
	// native_modules 构建下返回非 nil loader，便于 CLI 或嵌入方写入 PackageDynamicLibraryLoader。
	return func(filename string, symbol string) (runtime.Value, error) {
		if err := validateDynamicLoaderRequest(filename, symbol); err != nil {
			// 文件名或符号缺失属于调用方传参错误，按打开失败分类返回给 package.loadlib。
			return runtime.NilValue(), err
		}
		if loaderResult, err, ok := loaderDiagnosticBeforeOpenResult(filename); ok {
			// 诊断模式在平台动态库调用之前返回，用于隔离 Go loader 回调错误传播。
			return loaderResult, err
		}
		err := resolveDynamicSymbol(filename, symbol)
		if err != nil {
			// 平台动态库或符号解析失败时保留底层兼容分类，交给 package.loadlib 转三返回。
			return runtime.NilValue(), err
		}
		// 当前阶段只证明动态库和符号可解析；Lua C API shim 尚未实现，不能伪装成可调用 loader。
		return runtime.NilValue(), packagelib.DynamicLibraryError{
			Category: "init",
			Message:  "native module loader resolved symbol but Lua C API shim is not implemented yet",
		}
	}
}

// LoaderForState 返回绑定指定 State 的原生动态库 loader。
//
// state 必须是当前 package 库所属 State。返回的 loader 会打开动态库、解析 luaopen_*，并把入口
// 包装成 Lua 可调用 Go closure；动态库句柄和 lua_State* opaque handle 会随 closure 常驻。
func LoaderForState(state *runtime.State) func(filename string, symbol string) (runtime.Value, error) {
	// state-aware loader 由 lua.Options.PackageDynamicLibraryLoaderForState 调用，必须保留当前 VM 上下文。
	return func(filename string, symbol string) (runtime.Value, error) {
		if err := validateDynamicLoaderRequest(filename, symbol); err != nil {
			// 参数错误保持 open 分类，与无状态 Loader 一致。
			return runtime.NilValue(), err
		}
		if loaderResult, err, ok := loaderDiagnosticBeforeOpenResult(filename); ok {
			// 诊断模式在平台动态库调用之前返回，用于隔离 Go loader 回调错误传播。
			return loaderResult, err
		}
		symbolAddress, closeLibrary, err := loadDynamicSymbol(filename, symbol)
		if err != nil {
			// 打开或符号解析错误直接透传，保持 package.loadlib 三返回分类。
			return runtime.NilValue(), err
		}
		if loaderResult, err, ok := loaderDiagnosticAfterOpenResult(filename, closeLibrary); ok {
			// 诊断模式已处理库句柄生命周期，直接返回人工结果。
			return loaderResult, err
		}
		handle, err := newNativeStateHandle(state)
		if err != nil {
			// 无有效 State 时不能返回 callable；已打开动态库要立即关闭。
			if closeLibrary != nil {
				// 关闭失败不能掩盖 State 失效这一初始化主因。
				_ = closeLibrary()
			}
			return runtime.NilValue(), packagelib.DynamicLibraryError{
				Category: "init",
				Message:  fmt.Sprintf("native module loader requires an active State: %v", err),
			}
		}
		loader := runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			// closeLibrary 被 closure 捕获以保持动态库句柄存活；当前阶段不做卸载。
			_ = closeLibrary
			return nativeLuaCallCFunction(handle.pointer(), symbolAddress, nil, args...)
		})
		return runtime.ReferenceValue(runtime.KindGoClosure, loader), nil
	}
}

// validateDynamicLoaderRequest 校验 native 动态库 loader 入参。
func validateDynamicLoaderRequest(filename string, symbol string) error {
	// package.loadlib 和 C searcher 都必须传入明确文件名和 luaopen_* 符号名。
	if filename == "" || symbol == "" {
		// 缺失文件名或符号属于打开阶段前置错误。
		return packagelib.DynamicLibraryError{
			Category: "open",
			Message:  fmt.Sprintf("native module loader requires filename and symbol, got filename=%q symbol=%q", filename, symbol),
		}
	}
	return nil
}

// loaderDiagnosticBeforeOpenResult 返回 native loader 顶层诊断结果。
//
// 该入口只供 LPeg/loadlib 边界定位脚本使用，不属于公开 API；未设置环境变量时不改变生产路径。
func loaderDiagnosticBeforeOpenResult(filename string) (runtime.Value, error, bool) {
	// 诊断模式必须命中文件名片段，避免影响前置 require("lpeg") 或其它正常模块加载。
	diagnosticMode := nativeLoaderDiagnosticMode()
	if diagnosticMode == "" || !nativeLoaderDiagnosticApplies(filename) {
		// 未启用或未命中时继续真实 native loader 路径。
		return runtime.NilValue(), nil, false
	}
	switch diagnosticMode {
	case "dynamic-error":
		// DynamicLibraryError 覆盖宿主 loader 已分类错误返回。
		return runtime.NilValue(), diagnosticLoaderDynamicError(filename, diagnosticMode), true
	case "plain-error":
		// 普通 error 覆盖 dynamicLibraryFailure 默认 open 分类路径。
		return runtime.NilValue(), fmt.Errorf("diagnostic native loader plain error at %s for %q", diagnosticMode, filename), true
	case "noncallable":
		// nil 错误但返回不可调用值，覆盖 package.loadlib 自行构造 init 失败的路径。
		return runtime.StringValue("diagnostic native loader noncallable result"), nil, true
	default:
		// 未知诊断模式不拦截，避免拼写错误破坏正常 loader 行为。
		return runtime.NilValue(), nil, false
	}
}

// loaderDiagnosticAfterOpenResult 返回成功打开和解析符号后的人工诊断错误。
//
// closeLibrary 必须是当前动态库句柄的关闭函数；诊断模式命中时本函数负责关闭，避免资源泄漏。
func loaderDiagnosticAfterOpenResult(filename string, closeLibrary func() error) (runtime.Value, error, bool) {
	// after-open 诊断用于确认成功动态库路径之后返回 open 错误是否同样污染 LPeg 状态。
	diagnosticMode := nativeLoaderDiagnosticMode()
	if diagnosticMode != "after-open-dynamic-error" || !nativeLoaderDiagnosticApplies(filename) {
		// 未启用该模式时继续真实 luaopen_* callable 包装。
		return runtime.NilValue(), nil, false
	}
	if closeLibrary != nil {
		// 诊断提前返回时必须关闭已打开库句柄。
		_ = closeLibrary()
	}
	return runtime.NilValue(), diagnosticLoaderDynamicError(filename, diagnosticMode), true
}

// nativeLoaderDiagnosticMode 返回 native loader 顶层错误传播诊断模式。
func nativeLoaderDiagnosticMode() string {
	// 每次 loader 回调时读取环境变量，便于同一 glua 二进制执行多个独立探针。
	return os.Getenv("GLUA_NATIVE_LOADER_DIAGNOSTIC")
}

// nativeLoaderDiagnosticApplies 判断当前文件是否命中 native loader 顶层诊断范围。
//
// GLUA_NATIVE_LOADER_DIAGNOSTIC_MATCH 为空时诊断模式影响全部 loader 请求；非空时只匹配包含该片段的路径。
func nativeLoaderDiagnosticApplies(filename string) bool {
	// 文件片段为空时允许手工调试直接覆盖所有 native loader 请求。
	filenameFragment := os.Getenv("GLUA_NATIVE_LOADER_DIAGNOSTIC_MATCH")
	if filenameFragment == "" {
		// 空匹配条件表示调用者明确要让诊断模式覆盖全部请求。
		return true
	}
	return strings.Contains(filename, filenameFragment)
}

// diagnosticLoaderDynamicError 构造 native loader 顶层诊断用动态库错误。
func diagnosticLoaderDynamicError(filename string, diagnosticMode string) error {
	// 诊断错误仍使用 open 分类，除非后续模式专门覆盖其它分类。
	return packagelib.DynamicLibraryError{
		Category: "open",
		Message:  fmt.Sprintf("diagnostic native loader dynamic error at %s for %q", diagnosticMode, filename),
	}
}
