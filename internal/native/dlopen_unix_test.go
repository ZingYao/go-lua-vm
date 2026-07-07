//go:build native_modules && (linux || darwin)

package native

import (
	"errors"
	goruntime "runtime"
	"strings"
	"testing"

	packagelib "github.com/ZingYao/go-lua-vm/stdlib/package"
)

// TestUnixDynamicLibraryLookupSymbol 验证 Linux/macOS native loader 能打开系统库并解析公开符号。
func TestUnixDynamicLibraryLookupSymbol(t *testing.T) {
	// 使用平台稳定系统库做最小 smoke，避免依赖项目 fixture 提前存在。
	filename, symbol := unixSmokeLibrary()
	library, err := openDynamicLibrary(filename)
	if err != nil {
		// 系统库无法打开说明 dlopen 封装不可用，测试必须失败暴露。
		t.Fatalf("openDynamicLibrary(%q) failed: %v", filename, err)
	}
	defer func() {
		// 失败路径也要关闭句柄，避免同一进程后续测试受到影响。
		if err := library.close(); err != nil {
			// 关闭失败说明平台 loader 状态异常。
			t.Fatalf("close dynamic library failed: %v", err)
		}
	}()

	address, err := library.lookupSymbol(symbol)
	if err != nil {
		// 系统库公开符号无法解析说明 dlsym 封装不可用。
		t.Fatalf("lookupSymbol(%q) failed: %v", symbol, err)
	}
	if address == nil {
		// lookupSymbol 成功时必须返回非 nil 地址，后续 shim 才能包装调用。
		t.Fatalf("lookupSymbol(%q) returned nil address", symbol)
	}
}

// TestUnixDynamicLibraryMissingSymbol 验证缺失符号会按 package.loadlib init 分类返回。
func TestUnixDynamicLibraryMissingSymbol(t *testing.T) {
	// 缺失符号测试复用系统库，只验证 dlsym 错误分类，不依赖 C 模块 fixture。
	filename, _ := unixSmokeLibrary()
	library, err := openDynamicLibrary(filename)
	if err != nil {
		// 系统库无法打开时无法继续验证缺失符号分类。
		t.Fatalf("openDynamicLibrary(%q) failed: %v", filename, err)
	}
	defer func() {
		// 测试结束必须关闭库句柄。
		if err := library.close(); err != nil {
			// close 失败需要暴露，避免动态加载器资源泄露。
			t.Fatalf("close dynamic library failed: %v", err)
		}
	}()

	_, err = library.lookupSymbol("glua_missing_symbol_for_native_loader_test")
	if err == nil {
		// 缺失符号不应伪装成功。
		t.Fatalf("lookupSymbol for missing symbol returned nil error")
	}
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(err, &dynamicErr) {
		// 错误必须保留 package.loadlib 可识别的分类类型。
		t.Fatalf("missing symbol error = %T, want DynamicLibraryError", err)
	}
	if dynamicErr.Category != "init" {
		// Lua 5.3 loadlib 对入口初始化失败使用 init 分类。
		t.Fatalf("missing symbol category = %q, want init", dynamicErr.Category)
	}
}

// TestUnixLoaderReportsShimBoundary 验证 Loader 解析到符号后仍明确报告 shim 尚未实现。
func TestUnixLoaderReportsShimBoundary(t *testing.T) {
	// 直接使用系统库符号证明 loader 已进入 dlopen/dlsym 成功路径。
	filename, symbol := unixSmokeLibrary()
	_, err := Loader()(filename, symbol)
	if err == nil {
		// 当前阶段还没有 Lua C API shim，不能返回可调用 Lua loader。
		t.Fatalf("Loader(%q, %q) returned nil error before shim exists", filename, symbol)
	}
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(err, &dynamicErr) {
		// 错误必须保留动态库兼容分类。
		t.Fatalf("loader shim boundary error = %T, want DynamicLibraryError", err)
	}
	if dynamicErr.Category != "init" {
		// 符号已解析但不能初始化 Lua loader，应归类为 init。
		t.Fatalf("loader shim boundary category = %q, want init", dynamicErr.Category)
	}
	if !strings.Contains(dynamicErr.Message, "shim is not implemented") {
		// 错误文本必须明确当前阻塞点在 C API shim，而不是动态库解析。
		t.Fatalf("loader shim boundary message = %q", dynamicErr.Message)
	}
}

// TestUnixDynamicLibraryDiagnosticOpenMode 验证 LPeg 定位用诊断开关只拦截匹配路径。
func TestUnixDynamicLibraryDiagnosticOpenMode(t *testing.T) {
	// 诊断模式只应拦截包含匹配片段的目标文件，避免误伤前置 require("lpeg")。
	t.Setenv("GLUA_NATIVE_DLOPEN_DIAGNOSTIC", "before-cstring")
	t.Setenv("GLUA_NATIVE_DLOPEN_DIAGNOSTIC_MATCH", "glua_diag_target")

	_, err := openDynamicLibrary("/tmp/glua_diag_target_missing.so")
	if err == nil {
		// 命中匹配片段时必须强制返回诊断错误，而不是进入真实 dlopen。
		t.Fatalf("diagnostic open returned nil error")
	}
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(err, &dynamicErr) {
		// 诊断错误必须保留 package.loadlib 可识别的动态库错误类型。
		t.Fatalf("diagnostic open error = %T, want DynamicLibraryError", err)
	}
	if dynamicErr.Category != "open" {
		// 诊断模式模拟打开阶段失败，因此分类必须是 open。
		t.Fatalf("diagnostic open category = %q, want open", dynamicErr.Category)
	}
	if !strings.Contains(dynamicErr.Message, "before-cstring") {
		// 错误文本必须携带阶段名，便于探针脚本确认拦截位置。
		t.Fatalf("diagnostic open message = %q", dynamicErr.Message)
	}

	filename, _ := unixSmokeLibrary()
	library, err := openDynamicLibrary(filename)
	if err != nil {
		// 非匹配路径必须继续进入真实 dlopen，避免诊断开关污染 LPeg 模块加载。
		t.Fatalf("openDynamicLibrary(%q) with non-matching diagnostic filter failed: %v", filename, err)
	}
	defer func() {
		// 成功打开的系统库必须关闭，避免影响同进程后续 native 测试。
		if err := library.close(); err != nil {
			// close 失败说明诊断开关之外的平台 loader 状态异常。
			t.Fatalf("close dynamic library failed: %v", err)
		}
	}()
}

// unixSmokeLibrary 返回当前 Unix 平台上可用于 dlopen/dlsym smoke 的系统库和符号。
func unixSmokeLibrary() (string, string) {
	// 根据平台选择稳定的 C 运行时库入口。
	switch goruntime.GOOS {
	case "darwin":
		// macOS 的 libSystem 暴露 malloc，路径在 dyld shared cache 下仍可 dlopen。
		return "/usr/lib/libSystem.B.dylib", "malloc"
	default:
		// Linux 使用 libc.so.6 和 malloc，覆盖主流 glibc 环境。
		return "libc.so.6", "malloc"
	}
}
