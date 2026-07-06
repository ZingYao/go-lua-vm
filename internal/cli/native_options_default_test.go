//go:build !native_modules

package cli

import (
	"testing"

	"github.com/zing/go-lua-vm/lua"
)

// TestApplyNativeModuleOptionsDefaultNoop 验证默认构建不启用 native loader。
func TestApplyNativeModuleOptionsDefaultNoop(t *testing.T) {
	// 默认 CGO-free 构建必须保持 package.loadlib 禁用策略，不能注入动态库 loader。
	options := applyNativeModuleOptions(lua.DefaultOptions())
	if options.PackageDynamicLibraryLoader != nil {
		// 默认构建出现无状态 loader 会改变 package.loadlib 行为。
		t.Fatalf("default PackageDynamicLibraryLoader is enabled")
	}
	if options.PackageDynamicLibraryLoaderForState != nil {
		// 默认构建出现状态感知 loader 会让 require 尝试打开本机动态库。
		t.Fatalf("default PackageDynamicLibraryLoaderForState is enabled")
	}
}
