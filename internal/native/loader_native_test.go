//go:build native_modules

package native

import (
	"errors"
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
	packagelib "github.com/ZingYao/go-lua-vm/stdlib/package"
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

// TestNativeLoaderDiagnosticBeforeOpenModes 验证 native loader 顶层诊断模式的返回形态。
func TestNativeLoaderDiagnosticBeforeOpenModes(t *testing.T) {
	// 诊断模式只应命中指定文件名片段，避免影响其它 native 模块加载。
	t.Setenv("GLUA_NATIVE_LOADER_DIAGNOSTIC_MATCH", "glua_loader_diag_target")
	loader := Loader()

	t.Setenv("GLUA_NATIVE_LOADER_DIAGNOSTIC", "dynamic-error")
	_, err := loader("/tmp/glua_loader_diag_target.so", "luaopen_diag")
	if err == nil {
		// DynamicLibraryError 诊断模式必须通过 error 返回。
		t.Fatalf("dynamic-error diagnostic returned nil error")
	}
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(err, &dynamicErr) {
		// DynamicLibraryError 诊断模式必须保留可分类错误类型。
		t.Fatalf("dynamic-error diagnostic error = %T, want DynamicLibraryError", err)
	}
	if dynamicErr.Category != "open" {
		// 顶层诊断模拟打开阶段失败，分类必须为 open。
		t.Fatalf("dynamic-error diagnostic category = %q, want open", dynamicErr.Category)
	}

	t.Setenv("GLUA_NATIVE_LOADER_DIAGNOSTIC", "plain-error")
	_, err = loader("/tmp/glua_loader_diag_target.so", "luaopen_diag")
	if err == nil {
		// 普通错误诊断模式必须通过 error 返回。
		t.Fatalf("plain-error diagnostic returned nil error")
	}
	if errors.As(err, &dynamicErr) {
		// 普通错误诊断模式用于覆盖 dynamicLibraryFailure 的默认 open 分类路径。
		t.Fatalf("plain-error diagnostic unexpectedly matched DynamicLibraryError: %v", err)
	}
	if !strings.Contains(err.Error(), "plain-error") {
		// 普通错误文本必须携带模式名，便于脚本确认命中路径。
		t.Fatalf("plain-error diagnostic message = %q", err.Error())
	}

	t.Setenv("GLUA_NATIVE_LOADER_DIAGNOSTIC", "noncallable")
	result, err := loader("/tmp/glua_loader_diag_target.so", "luaopen_diag")
	if err != nil {
		// noncallable 模式必须返回 nil error，交给 package.loadlib 自行构造 init 失败。
		t.Fatalf("noncallable diagnostic error = %v, want nil", err)
	}
	if result.Kind != runtime.KindString {
		// 返回非 callable 字符串可稳定触发 package.loadlib 的不可调用分支。
		t.Fatalf("noncallable diagnostic result kind = %v, want string", result.Kind)
	}
}
