//go:build native_modules && windows

package native

import (
	"errors"
	"strings"
	"testing"

	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// TestWindowsDynamicLibraryLookupSymbol 验证 Windows native loader 能打开系统 DLL 并解析公开符号。
func TestWindowsDynamicLibraryLookupSymbol(t *testing.T) {
	// 使用 Windows 稳定系统 DLL 做最小 smoke，避免依赖项目 fixture 提前存在。
	filename, symbol := windowsSmokeLibrary()
	library, err := openDynamicLibrary(filename)
	if err != nil {
		// 系统 DLL 无法打开说明 LoadLibraryW 封装不可用，测试必须失败暴露。
		t.Fatalf("openDynamicLibrary(%q) failed: %v", filename, err)
	}
	defer func() {
		// 失败路径也要释放句柄，避免同一进程后续测试受到影响。
		if err := library.close(); err != nil {
			// 关闭失败说明平台 loader 状态异常。
			t.Fatalf("close dynamic library failed: %v", err)
		}
	}()

	address, err := library.lookupSymbol(symbol)
	if err != nil {
		// 系统 DLL 公开符号无法解析说明 GetProcAddress 封装不可用。
		t.Fatalf("lookupSymbol(%q) failed: %v", symbol, err)
	}
	if address == nil {
		// lookupSymbol 成功时必须返回非 nil 地址，后续 shim 才能包装调用。
		t.Fatalf("lookupSymbol(%q) returned nil address", symbol)
	}
}

// TestWindowsDynamicLibraryMissingSymbol 验证缺失符号会按 package.loadlib init 分类返回。
func TestWindowsDynamicLibraryMissingSymbol(t *testing.T) {
	// 缺失符号测试复用系统 DLL，只验证 GetProcAddress 错误分类，不依赖 C 模块 fixture。
	filename, _ := windowsSmokeLibrary()
	library, err := openDynamicLibrary(filename)
	if err != nil {
		// 系统 DLL 无法打开时无法继续验证缺失符号分类。
		t.Fatalf("openDynamicLibrary(%q) failed: %v", filename, err)
	}
	defer func() {
		// 测试结束必须释放 DLL 句柄。
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

// TestWindowsLoaderReportsShimBoundary 验证 Loader 解析到符号后仍明确报告 shim 尚未实现。
func TestWindowsLoaderReportsShimBoundary(t *testing.T) {
	// 直接使用系统 DLL 符号证明 loader 已进入 LoadLibraryW/GetProcAddress 成功路径。
	filename, symbol := windowsSmokeLibrary()
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

// windowsSmokeLibrary 返回 Windows 平台可用于 LoadLibraryW/GetProcAddress smoke 的系统 DLL 和符号。
func windowsSmokeLibrary() (string, string) {
	// kernel32.dll 是 Windows 稳定系统 DLL，GetCurrentProcess 是公开导出符号。
	return "kernel32.dll", "GetCurrentProcess"
}
