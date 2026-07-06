//go:build !native_modules

package native

import "testing"

// TestDefaultLoaderDisabled 验证默认构建不会注入原生动态库 loader。
func TestDefaultLoaderDisabled(t *testing.T) {
	// 默认构建必须保持 CGO-free，Loader 返回 nil 表示 package.loadlib 继续走禁用策略。
	if loader := Loader(); loader != nil {
		t.Fatalf("default Loader() returned non-nil loader")
	}
}
