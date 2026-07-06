//go:build native_modules && !(linux || darwin)

package native

import packagelib "github.com/zing/go-lua-vm/stdlib/package"

// resolveDynamicSymbol 在尚未实现平台动态加载器时返回明确失败分类。
//
// Windows 的 LoadLibraryW/GetProcAddress 会在后续切口实现；当前保留 native 构建可编译。
func resolveDynamicSymbol(filename string, symbol string) error {
	// 非 Unix 平台当前没有动态库实现，直接返回 absent 以保持 package.searchers 诊断清晰。
	return packagelib.DynamicLibraryError{
		Category: "absent",
		Message:  "native module dynamic loader is not implemented on this platform yet",
	}
}
