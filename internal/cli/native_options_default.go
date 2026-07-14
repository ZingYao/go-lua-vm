//go:build !cgo

package cli

import "github.com/ZingYao/go-lua-vm/lua"

// applyNativeModuleOptions 在默认构建下保持 CLI 选项不变。
func applyNativeModuleOptions(options lua.Options) lua.Options {
	// 默认 CGO-free 构建不能启用动态库 loader，直接返回调用方已有选项。
	return options
}
