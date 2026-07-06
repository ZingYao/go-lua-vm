//go:build native_modules

package native

import (
	"fmt"

	"github.com/zing/go-lua-vm/runtime"
	packagelib "github.com/zing/go-lua-vm/stdlib/package"
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
		symbolAddress, closeLibrary, err := loadDynamicSymbol(filename, symbol)
		if err != nil {
			// 打开或符号解析错误直接透传，保持 package.loadlib 三返回分类。
			return runtime.NilValue(), err
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
