//go:build native_modules && (linux || darwin)

package native

/*
#cgo linux LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

static void* glua_dlopen(const char* filename) {
	return dlopen(filename, RTLD_NOW | RTLD_LOCAL);
}

static void* glua_dlsym(void* handle, const char* symbol) {
	return dlsym(handle, symbol);
}

static int glua_dlclose(void* handle) {
	return dlclose(handle);
}

static const char* glua_dlerror(void) {
	return dlerror();
}
*/
import "C"

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// dynamicLibrary 保存 Unix 平台 dlopen 返回的动态库句柄。
type dynamicLibrary struct {
	// handle 保存 C 动态加载器返回的不透明句柄。
	handle unsafe.Pointer
	// filename 保存原始库路径，用于错误信息回传给 package.loadlib。
	filename string
}

// openDynamicLibrary 在 Linux/macOS native_modules 构建下打开动态库。
//
// filename 必须是非空路径或系统动态加载器可解析的库名。成功时返回的句柄必须由调用方 close。
func openDynamicLibrary(filename string) (*dynamicLibrary, error) {
	// 入口先校验路径，避免向 dlopen 传递空字符串后得到平台相关诊断。
	if filename == "" {
		// 空路径属于打开阶段错误，package.loadlib 应返回 open 分类。
		return nil, packagelib.DynamicLibraryError{Category: "open", Message: "dynamic library filename is empty"}
	}
	diagnosticMode := dynamicOpenDiagnosticMode()
	if diagnosticMode == "before-cstring" && dynamicOpenDiagnosticApplies(filename) {
		// 诊断模式在任何 C 分配和 loader 调用之前返回，用于隔离 package.loadlib/native Go 错误路径。
		return nil, diagnosticDynamicOpenError(filename, diagnosticMode)
	}
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))
	if diagnosticMode == "after-cstring" && dynamicOpenDiagnosticApplies(filename) {
		// 诊断模式只覆盖 C.CString/C.free 生命周期，不进入 dlerror 或 dlopen。
		return nil, diagnosticDynamicOpenError(filename, diagnosticMode)
	}

	clearDynamicLibraryError()
	if diagnosticMode == "after-clear" && dynamicOpenDiagnosticApplies(filename) {
		// 诊断模式覆盖 dlerror 清理调用，但仍不触发真实 dlopen。
		return nil, diagnosticDynamicOpenError(filename, diagnosticMode)
	}
	handle := C.glua_dlopen(cFilename)
	if diagnosticMode == "after-dlopen-no-dlerror" && dynamicOpenDiagnosticApplies(filename) {
		// 诊断模式触发真实 dlopen，但故意不读取 dlerror，避免把错误字符串读取混入定位结果。
		if handle != nil {
			// 异常成功时立即关闭句柄，避免诊断模式泄漏平台 loader 资源。
			_ = C.glua_dlclose(handle)
		}
		return nil, diagnosticDynamicOpenError(filename, diagnosticMode)
	}
	if handle == nil {
		// dlopen 返回 nil 表示库不存在、格式不匹配或依赖缺失，统一归类为 open。
		return nil, packagelib.DynamicLibraryError{
			Category: "open",
			Message:  fmt.Sprintf("failed to open dynamic library %q: %s", filename, lastDynamicLibraryError()),
		}
	}
	return &dynamicLibrary{handle: handle, filename: filename}, nil
}

// lookupSymbol 在已打开的动态库中查找符号地址。
//
// symbol 必须是非空 C 符号名。返回的地址只用于本阶段解析验证，后续 C API shim 会负责调用边界。
func (library *dynamicLibrary) lookupSymbol(symbol string) (unsafe.Pointer, error) {
	// 符号查找前先确认句柄仍可用，避免对已关闭库执行 dlsym。
	if library == nil || library.handle == nil {
		// 无可用句柄属于打开阶段错误，调用方需要重新打开库。
		return nil, packagelib.DynamicLibraryError{Category: "open", Message: "dynamic library handle is closed"}
	}
	if symbol == "" {
		// 空符号名属于初始化阶段错误，因为动态库已打开但入口不可定位。
		return nil, packagelib.DynamicLibraryError{Category: "init", Message: "dynamic library symbol is empty"}
	}
	cSymbol := C.CString(symbol)
	defer C.free(unsafe.Pointer(cSymbol))

	clearDynamicLibraryError()
	address := C.glua_dlsym(library.handle, cSymbol)
	if address == nil {
		// dlsym 返回 nil 表示 luaopen_* 入口不存在或不可见，归类为 init。
		return nil, packagelib.DynamicLibraryError{
			Category: "init",
			Message:  fmt.Sprintf("failed to resolve symbol %q in %q: %s", symbol, library.filename, lastDynamicLibraryError()),
		}
	}
	return address, nil
}

// close 关闭 Unix 平台动态库句柄。
//
// close 可重复调用；首次成功后会清空句柄，后续调用保持 no-op。
func (library *dynamicLibrary) close() error {
	// nil 或已关闭句柄视为 no-op，方便测试和失败路径 defer 清理。
	if library == nil || library.handle == nil {
		// 没有活跃句柄时无需向 dlclose 传参。
		return nil
	}
	handle := library.handle
	library.handle = nil

	clearDynamicLibraryError()
	if C.glua_dlclose(handle) != 0 {
		// dlclose 失败通常表示平台 loader 状态异常，按 open 分类返回给调用方。
		return packagelib.DynamicLibraryError{
			Category: "open",
			Message:  fmt.Sprintf("failed to close dynamic library %q: %s", library.filename, lastDynamicLibraryError()),
		}
	}
	return nil
}

// resolveDynamicSymbol 打开动态库并验证指定符号可解析。
//
// 当前阶段只做解析验证，不调用符号地址；后续 Lua C API shim 落地后再把地址包装为 Lua loader。
func resolveDynamicSymbol(filename string, symbol string) error {
	// 解析过程复用 loadDynamicSymbol，并在验证完成后关闭句柄。
	_, closeLibrary, err := loadDynamicSymbol(filename, symbol)
	if err != nil {
		// 打开或符号解析失败直接返回，保留兼容分类。
		return err
	}
	if closeLibrary == nil {
		// 理论上可解析符号必须带有关闭函数；缺失时按打开阶段错误暴露。
		return packagelib.DynamicLibraryError{Category: "open", Message: "dynamic library close function is missing"}
	}
	if err := closeLibrary(); err != nil {
		// 显式关闭失败需要返回，避免测试遗漏平台 loader 异常。
		return err
	}
	return nil
}

// loadDynamicSymbol 打开动态库并返回指定符号地址，调用方负责关闭库句柄。
func loadDynamicSymbol(filename string, symbol string) (unsafe.Pointer, func() error, error) {
	// 解析过程先打开动态库，再查找 luaopen_* 符号；成功后句柄必须保持到调用结束。
	library, err := openDynamicLibrary(filename)
	if err != nil {
		// 打开失败直接返回，保留 open 分类。
		return nil, nil, err
	}
	address, err := library.lookupSymbol(symbol)
	if err != nil {
		// 符号解析失败时立即关闭库，避免泄漏句柄。
		_ = library.close()
		return nil, nil, err
	}
	return address, library.close, nil
}

// clearDynamicLibraryError 清空 dlerror 的线程本地错误状态。
func clearDynamicLibraryError() {
	// POSIX 语义要求调用 dlerror 读取并清空旧错误。
	_ = C.glua_dlerror()
}

// lastDynamicLibraryError 返回最近一次动态加载器错误文本。
func lastDynamicLibraryError() string {
	// dlerror 返回 nil 时说明平台未提供更详细错误，使用稳定兜底文本。
	message := C.glua_dlerror()
	if message == nil {
		// 无底层错误文本时保持可读错误，避免 package.loadlib 返回空字符串。
		return "no dynamic loader error details"
	}
	return C.GoString(message)
}

// dynamicOpenDiagnosticMode 返回 Unix native loader 的诊断阶段开关。
//
// 该环境变量只供 LPeg/loadlib 边界定位脚本使用，不属于公开 API；未设置时生产路径完全保持原样。
func dynamicOpenDiagnosticMode() string {
	// 每次打开库时读取环境变量，便于同一二进制在不同探针进程中切换诊断阶段。
	return os.Getenv("GLUA_NATIVE_DLOPEN_DIAGNOSTIC")
}

// dynamicOpenDiagnosticApplies 判断当前文件是否命中 Unix native loader 诊断范围。
//
// GLUA_NATIVE_DLOPEN_DIAGNOSTIC_MATCH 为空时诊断模式影响全部打开请求；非空时只匹配包含该片段的路径。
func dynamicOpenDiagnosticApplies(filename string) bool {
	// 未启用匹配条件时保留旧诊断行为，方便手工直接拦截任意 dlopen。
	filenameFragment := os.Getenv("GLUA_NATIVE_DLOPEN_DIAGNOSTIC_MATCH")
	if filenameFragment == "" {
		// 空匹配条件表示调用者明确要让诊断模式覆盖所有动态库打开。
		return true
	}
	return strings.Contains(filename, filenameFragment)
}

// diagnosticDynamicOpenError 构造 package.loadlib 兼容的诊断 open 错误。
//
// filename 必须是原始动态库路径；mode 记录中断阶段，供脚本输出和 TODO 证据定位。
func diagnosticDynamicOpenError(filename string, mode string) error {
	// 诊断错误仍返回 open 分类，确保 package.loadlib 三返回形态与真实 dlopen 失败一致。
	return packagelib.DynamicLibraryError{
		Category: "open",
		Message:  fmt.Sprintf("diagnostic dynamic library open failure at %s for %q", mode, filename),
	}
}
