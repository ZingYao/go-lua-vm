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
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))

	clearDynamicLibraryError()
	handle := C.glua_dlopen(cFilename)
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
	// 解析过程先打开动态库，再查找 luaopen_* 符号，最后关闭句柄。
	library, err := openDynamicLibrary(filename)
	if err != nil {
		// 打开失败直接返回，保留 open 分类。
		return err
	}
	defer func() {
		// defer 只做兜底清理；显式 close 的错误在下方返回，避免掩盖符号解析错误。
		_ = library.close()
	}()
	if _, err := library.lookupSymbol(symbol); err != nil {
		// 符号缺失直接返回，保留 init 分类。
		return err
	}
	if err := library.close(); err != nil {
		// 显式关闭失败需要返回，避免测试遗漏平台 loader 异常。
		return err
	}
	return nil
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
