//go:build cgo

package cli

import (
	"github.com/ZingYao/go-lua-vm/internal/native"
	"github.com/ZingYao/go-lua-vm/lua"
)

// applyNativeModuleOptions 在 CGO 构建下为 CLI 注入 Lua C 模块 loader。
func applyNativeModuleOptions(options lua.Options) lua.Options {
	// 状态感知 loader 绑定当前 State，确保 luaopen_* 和后续 C function 访问同一个 VM 栈。
	if options.PackageDynamicLibraryLoaderForState == nil {
		// CLI 不暴露自定义 loader 配置时使用内置 native loader。
		options.PackageDynamicLibraryLoaderForState = native.LoaderForState
	}
	if options.PackageDynamicLibraryLoader == nil {
		// 无状态 loader 保留给 package.loadlib 的直接解析边界和诊断路径。
		options.PackageDynamicLibraryLoader = native.Loader()
	}
	return options
}
