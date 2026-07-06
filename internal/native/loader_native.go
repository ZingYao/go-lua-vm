//go:build native_modules

package native

import (
	"fmt"

	"github.com/zing/go-lua-vm/runtime"
	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// Loader 返回 native_modules 构建下的原生动态库 loader。
//
// 当前阶段只建立稳定入口和错误分类；后续平台实现会在此入口下接入 dlopen/dlsym 或
// LoadLibrary/GetProcAddress，并返回 luaopen_* 对应的 Lua 可调用 loader。
func Loader() func(filename string, symbol string) (runtime.Value, error) {
	// native_modules 构建下返回非 nil loader，便于 CLI 或嵌入方写入 PackageDynamicLibraryLoader。
	return func(filename string, symbol string) (runtime.Value, error) {
		if filename == "" || symbol == "" {
			// 文件名或符号缺失属于调用方传参错误，按打开失败分类返回给 package.loadlib。
			return runtime.NilValue(), packagelib.DynamicLibraryError{
				Category: "open",
				Message:  fmt.Sprintf("native module loader requires filename and symbol, got filename=%q symbol=%q", filename, symbol),
			}
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
