package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

var (
	// ErrHostFilesystemDisabled 表示脚本路径访问被宿主权限策略拒绝。
	ErrHostFilesystemDisabled = errors.New("host filesystem access is disabled")
)

// CleanVirtualPath 将 Lua 路径清洗为 Go fs.FS 可接受的相对路径。
//
// filename 必须是非空路径；允许开头 `./` 并清洗重复分隔符；拒绝绝对路径、NUL 字节与 `..`
// 穿越段。返回值只用于只读 VFS 访问，错误文本面向 loadfile、require 和 io.open 展示。
func CleanVirtualPath(filename string) (string, error) {
	// 先拒绝空路径，避免 path.Clean 把空值变成当前目录。
	if filename == "" {
		return "", fmt.Errorf("virtual filesystem path is empty")
	}
	if strings.ContainsRune(filename, 0) {
		// NUL 字节不能作为 Go fs.FS 路径，直接拒绝避免路径截断歧义。
		return "", fmt.Errorf("virtual filesystem path is invalid: %s", filename)
	}
	if strings.HasPrefix(filename, "/") {
		// VFS 路径必须限制在 fs.FS 根内，绝对路径视为越界访问。
		return "", fmt.Errorf("virtual filesystem path escapes root: %s", filename)
	}
	for _, element := range strings.Split(filename, "/") {
		// 任一原始路径段为 .. 都表示尝试穿越根目录，即使 Clean 后能归约也必须拒绝。
		if element == ".." {
			return "", fmt.Errorf("virtual filesystem path escapes root: %s", filename)
		}
	}

	cleanedPath := path.Clean(filename)
	if cleanedPath == "." {
		// 当前目录不是可加载文件，避免后续把目录当 chunk 读取。
		return "", fmt.Errorf("virtual filesystem path is invalid: %s", filename)
	}
	if !fs.ValidPath(cleanedPath) {
		// Go fs.FS 只接受 slash 分隔的相对路径。
		return "", fmt.Errorf("virtual filesystem path is invalid: %s", filename)
	}
	return cleanedPath, nil
}

// ReadVirtualFile 从只读虚拟文件系统读取文件内容。
//
// options.VirtualFilesystem 为 nil 时返回 ErrHostFilesystemDisabled 作为不可用占位；filename 会先
// 按 CleanVirtualPath 清洗。返回的错误保留 fs.ErrNotExist 语义，便于宿主文件系统兜底判断。
func ReadVirtualFile(options Options, filename string) ([]byte, error) {
	// 没有配置 VFS 时调用方应决定是否尝试宿主文件系统。
	if options.VirtualFilesystem == nil {
		return nil, ErrHostFilesystemDisabled
	}
	cleanedPath, err := CleanVirtualPath(filename)
	if err != nil {
		// 路径非法时不能传给 fs.FS，避免实现差异造成绕过。
		return nil, err
	}
	return fs.ReadFile(options.VirtualFilesystem, cleanedPath)
}

// OpenVirtualFile 从只读虚拟文件系统打开文件。
//
// 返回文件可供 io.open/io.lines 构造只读 file userdata；调用方负责 Close。路径非法或文件缺失
// 会返回可展示错误，不会回退到宿主文件系统。
func OpenVirtualFile(options Options, filename string) (fs.File, error) {
	// 没有配置 VFS 时调用方应决定是否尝试宿主文件系统。
	if options.VirtualFilesystem == nil {
		return nil, ErrHostFilesystemDisabled
	}
	cleanedPath, err := CleanVirtualPath(filename)
	if err != nil {
		// 路径非法时不能传给 fs.FS，避免实现差异造成绕过。
		return nil, err
	}
	return options.VirtualFilesystem.Open(cleanedPath)
}

// ReadFileWithOptions 按 Options 的 VFS、宿主权限和优先级读取文件。
//
// 默认优先读取 VirtualFilesystem；设置 PreferHostFilesystem 且 AllowHostFilesystem 为 true 时先读
// 宿主路径。VFS 未命中可在宿主授权时兜底；宿主未授权且 VFS 也不可用时返回禁用错误。
func ReadFileWithOptions(options Options, filename string) ([]byte, error) {
	// 宿主优先模式仅在宿主文件系统显式授权后生效。
	if options.PreferHostFilesystem && options.AllowHostFilesystem {
		if sourceBytes, err := os.ReadFile(filename); err == nil {
			// 宿主路径命中时直接返回，保持优先级承诺。
			return sourceBytes, nil
		}
	}

	if options.VirtualFilesystem != nil {
		sourceBytes, err := ReadVirtualFile(options, filename)
		if err == nil {
			// VFS 命中时直接返回，默认优先级下不会再读宿主路径。
			return sourceBytes, nil
		}
		if !options.AllowHostFilesystem {
			// 宿主未授权时 VFS 错误就是最终错误，包含路径穿越或文件缺失文本。
			return nil, err
		}
	}

	if options.AllowHostFilesystem {
		// 宿主授权后作为 VFS 未命中或未配置时的兜底读取路径。
		return os.ReadFile(filename)
	}
	return nil, ErrHostFilesystemDisabled
}

// CanReadFileWithOptions 按 Options 判断候选路径是否可读。
//
// 该函数服务 package.searchpath/require 的候选命中判断；VFS 和宿主路径都只尝试只读打开，目录、
// 缺失、无权限和非法 VFS 路径都会被视为未命中。
func CanReadFileWithOptions(options Options, filename string) bool {
	// 空路径没有可读文件语义，直接拒绝。
	if filename == "" {
		return false
	}
	if options.PreferHostFilesystem && options.AllowHostFilesystem && canReadHostFile(filename) {
		// 宿主优先时先使用宿主可读性判断。
		return true
	}
	if options.VirtualFilesystem != nil && canReadVirtualFile(options, filename) {
		// 默认优先级下 VFS 可读即命中。
		return true
	}
	if options.AllowHostFilesystem {
		// VFS 未命中后，宿主授权时允许宿主文件兜底。
		return canReadHostFile(filename)
	}
	return false
}

// canReadVirtualFile 判断 VFS 路径是否能作为普通文件打开。
func canReadVirtualFile(options Options, filename string) bool {
	// 通过 Open 检查候选文件，目录实现通常也可打开，后续 Stat 需要排除目录。
	file, err := OpenVirtualFile(options, filename)
	if err != nil {
		// 不存在、非法或无权限都按 package 未命中处理。
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		// 无法取得元信息时仍视为不可读文件。
		return false
	}
	if info.IsDir() {
		// 目录不是 Lua chunk 文件。
		return false
	}
	return true
}

// canReadHostFile 判断宿主路径是否能作为普通文件打开。
func canReadHostFile(filename string) bool {
	// 先读取元信息排除目录与明显不存在路径。
	info, err := os.Stat(filename)
	if err != nil {
		// 不存在或无权限读取元信息都按未命中处理。
		return false
	}
	if info.IsDir() {
		// 目录不是 Lua chunk 文件。
		return false
	}
	file, err := os.Open(filename)
	if err != nil {
		// 元信息存在但不可打开时仍视为未命中。
		return false
	}
	if closeErr := file.Close(); closeErr != nil {
		// 能打开即表示候选可读，关闭失败不改变搜索命中。
		return true
	}
	return true
}
