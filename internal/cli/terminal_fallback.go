//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd

package cli

import "os"

// makeRawTerminal 在暂未适配的平台上保持普通行读取。
//
// file 参数保留给平台实现使用；返回 false 表示调用方应回退到 Scanner 路径。
func makeRawTerminal(file *os.File) (func(), bool) {
	// 当前平台没有 raw mode 适配，保持可编译和基础 REPL 可用。
	return func() {}, false
}
