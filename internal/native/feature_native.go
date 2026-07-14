//go:build cgo

package native

// Enabled 返回当前构建是否启用了 CGO 动态模块能力。
func Enabled() bool {
	// CGO 构建会注入 package.loadlib 和 C searcher 使用的动态库 loader。
	return true
}
