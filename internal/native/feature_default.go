//go:build !native_modules

package native

// Enabled 返回当前构建是否启用了 native_modules 动态模块能力。
func Enabled() bool {
	// 默认构建不加载本机动态库，返回 false 供 CLI 帮助和版本信息展示。
	return false
}
