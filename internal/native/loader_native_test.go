//go:build native_modules

package native

import (
	"errors"
	"strings"
	"testing"

	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// TestNativeLoaderSkeleton 验证 native_modules 构建下 loader 入口已存在且保持明确失败分类。
func TestNativeLoaderSkeleton(t *testing.T) {
	// native_modules 构建需要暴露非 nil loader，后续平台实现会替换当前骨架失败。
	loader := Loader()
	if loader == nil {
		t.Fatalf("native Loader() = nil, want skeleton loader")
	}

	_, err := loader("demo.so", "luaopen_demo")
	if err == nil {
		t.Fatalf("native skeleton loader should fail before platform loader is implemented")
	}
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(err, &dynamicErr) {
		t.Fatalf("native skeleton error = %T, want DynamicLibraryError", err)
	}
	if dynamicErr.Category != "absent" {
		t.Fatalf("native skeleton category = %q, want absent", dynamicErr.Category)
	}
	if !strings.Contains(dynamicErr.Message, "not implemented") {
		t.Fatalf("native skeleton message = %q, want not implemented", dynamicErr.Message)
	}
}
