//go:build native_modules && windows

package native

import (
	"fmt"
	"syscall"
	"unsafe"

	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// dynamicLibrary 保存 Windows 平台 LoadLibraryW 返回的动态库句柄。
type dynamicLibrary struct {
	// dll 保存标准库封装的 Windows DLL 句柄。
	dll *syscall.DLL
	// filename 保存原始库路径，用于错误信息回传给 package.loadlib。
	filename string
}

// openDynamicLibrary 在 Windows native_modules 构建下打开动态库。
//
// filename 必须是非空路径或 Windows 动态加载器可解析的 DLL 名称。成功时返回的句柄必须由调用方 close。
func openDynamicLibrary(filename string) (*dynamicLibrary, error) {
	// 入口先校验路径，避免把空字符串交给 LoadLibraryW 后得到平台相关诊断。
	if filename == "" {
		// 空路径属于打开阶段错误，package.loadlib 应返回 open 分类。
		return nil, packagelib.DynamicLibraryError{Category: "open", Message: "dynamic library filename is empty"}
	}
	dll, err := syscall.LoadDLL(filename)
	if err != nil {
		// LoadDLL 失败表示 DLL 缺失、格式不匹配或依赖缺失，统一归类为 open。
		return nil, packagelib.DynamicLibraryError{
			Category: "open",
			Message:  fmt.Sprintf("failed to open dynamic library %q: %v", filename, err),
		}
	}
	return &dynamicLibrary{dll: dll, filename: filename}, nil
}

// lookupSymbol 在已打开的 Windows DLL 中查找符号地址。
//
// symbol 必须是非空导出符号名。返回的地址只用于本阶段解析验证，后续 C API shim 会负责调用边界。
func (library *dynamicLibrary) lookupSymbol(symbol string) (unsafe.Pointer, error) {
	// 符号查找前先确认句柄仍可用，避免对已释放 DLL 执行 GetProcAddress。
	if library == nil || library.dll == nil {
		// 无可用句柄属于打开阶段错误，调用方需要重新打开 DLL。
		return nil, packagelib.DynamicLibraryError{Category: "open", Message: "dynamic library handle is closed"}
	}
	if symbol == "" {
		// 空符号名属于初始化阶段错误，因为动态库已打开但入口不可定位。
		return nil, packagelib.DynamicLibraryError{Category: "init", Message: "dynamic library symbol is empty"}
	}
	proc, err := library.dll.FindProc(symbol)
	if err != nil {
		// GetProcAddress 失败表示 luaopen_* 入口不存在或不可见，归类为 init。
		return nil, packagelib.DynamicLibraryError{
			Category: "init",
			Message:  fmt.Sprintf("failed to resolve symbol %q in %q: %v", symbol, library.filename, err),
		}
	}
	return unsafe.Pointer(proc.Addr()), nil
}

// close 关闭 Windows 动态库句柄。
//
// close 可重复调用；首次成功后会清空句柄，后续调用保持 no-op。
func (library *dynamicLibrary) close() error {
	// nil 或已释放 DLL 视为 no-op，方便测试和失败路径 defer 清理。
	if library == nil || library.dll == nil {
		// 没有活跃 DLL 句柄时无需调用 FreeLibrary。
		return nil
	}
	dll := library.dll
	library.dll = nil
	if err := dll.Release(); err != nil {
		// FreeLibrary 失败通常表示平台 loader 状态异常，按 open 分类返回给调用方。
		return packagelib.DynamicLibraryError{
			Category: "open",
			Message:  fmt.Sprintf("failed to close dynamic library %q: %v", library.filename, err),
		}
	}
	return nil
}

// resolveDynamicSymbol 打开 Windows DLL 并验证指定符号可解析。
//
// 当前阶段只做解析验证，不调用符号地址；后续 Lua C API shim 落地后再把地址包装为 Lua loader。
func resolveDynamicSymbol(filename string, symbol string) error {
	// 解析过程先打开 DLL，再查找 luaopen_* 符号，最后释放句柄。
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
