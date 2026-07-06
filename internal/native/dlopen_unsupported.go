//go:build native_modules && !(linux || darwin || windows)

package native

import packagelib "github.com/zing/go-lua-vm/stdlib/package"
import "unsafe"

// resolveDynamicSymbol 在非目标平台动态加载器尚未实现时返回明确失败分类。
//
// Linux、macOS、Windows 是当前目标平台；其他平台保留 native 构建可编译。
func resolveDynamicSymbol(filename string, symbol string) error {
	// 非目标平台当前没有动态库实现，直接返回 absent 以保持 package.searchers 诊断清晰。
	return packagelib.DynamicLibraryError{
		Category: "absent",
		Message:  "native module dynamic loader is not implemented on this platform yet",
	}
}

// loadDynamicSymbol 在非目标平台返回明确失败分类。
func loadDynamicSymbol(filename string, symbol string) (unsafe.Pointer, func() error, error) {
	// 非目标平台当前没有动态库实现，直接复用解析错误。
	return nil, nil, resolveDynamicSymbol(filename, symbol)
}
