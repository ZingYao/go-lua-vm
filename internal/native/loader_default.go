//go:build !cgo

package native

import "github.com/ZingYao/go-lua-vm/runtime"

// Loader 返回默认构建下的原生动态库 loader。
//
// 默认构建必须保持纯 Go 与无 CGO；返回 nil 表示不向 package 标准库注入动态库加载能力，
// package.loadlib 与 C searcher 会继续使用现有禁用说明。
func Loader() func(filename string, symbol string) (runtime.Value, error) {
	// 默认构建不启用 native 模块，避免无意加载本机机器码。
	return nil
}
