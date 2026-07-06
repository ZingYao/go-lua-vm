//go:build native_modules

package native

import (
	"errors"
	"strings"
	"testing"

	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// TestNativeLoaderRejectsEmptyArguments 验证 native_modules 构建下 loader 入口存在且校验参数。
func TestNativeLoaderRejectsEmptyArguments(t *testing.T) {
	// native_modules 构建需要暴露非 nil loader，后续平台实现会替换当前骨架失败。
	loader := Loader()
	if loader == nil {
		t.Fatalf("native Loader() = nil, want skeleton loader")
	}

	_, err := loader("", "")
	if err == nil {
		t.Fatalf("native loader should reject empty filename and symbol")
	}
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(err, &dynamicErr) {
		t.Fatalf("native skeleton error = %T, want DynamicLibraryError", err)
	}
	if dynamicErr.Category != "open" {
		t.Fatalf("native skeleton category = %q, want open", dynamicErr.Category)
	}
	if !strings.Contains(dynamicErr.Message, "requires filename and symbol") {
		t.Fatalf("native skeleton message = %q, want argument validation", dynamicErr.Message)
	}
}
