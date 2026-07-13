package playground

import (
	"bytes"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// workspaceFilesystem 将浏览器提交的工作区快照暴露为只读 fs.FS。
type workspaceFilesystem struct {
	files map[string][]byte
}

// newWorkspaceFilesystem 创建规范化的只读工作区文件系统。
//
// files 的 key 必须是工作区相对路径；非法、绝对或包含父目录穿越的路径会被忽略。返回 nil 表示没有可读文件。
func newWorkspaceFilesystem(files map[string]string) fs.FS {
	// 先排序原始路径，确保规范化重名文件的覆盖顺序稳定。
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	contents := make(map[string][]byte, len(paths))
	for _, filePath := range paths {
		normalizedPath, err := runtime.CleanVirtualPath(filePath)
		if err != nil {
			// 非法路径不能进入 VFS，避免脚本通过快照键逃逸工作区。
			continue
		}
		contents[normalizedPath] = []byte(files[filePath])
	}
	if len(contents) == 0 {
		// 空快照不安装 VFS，保持单文件 Playground 的原有沙箱语义。
		return nil
	}
	return &workspaceFilesystem{files: contents}
}

// Open 打开一个工作区快照文件。
//
// name 必须是 fs.ValidPath 格式的文件路径；目录和不存在的文件返回 fs.PathError。
func (filesystem *workspaceFilesystem) Open(name string) (fs.File, error) {
	// fs.FS 调用方可能直接传入非法路径，入口必须独立校验。
	if filesystem == nil || !fs.ValidPath(name) || name == "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	content, ok := filesystem.files[name]
	if !ok {
		// 未命中文件保留 fs.ErrNotExist 语义，供 io.open 返回 nil/message/code。
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	// 复制内容避免调用方持有的 reader 与文件系统底层切片共享可变位置。
	contentCopy := append([]byte(nil), content...)
	return &workspaceFile{name: path.Base(name), content: contentCopy, reader: bytes.NewReader(contentCopy)}, nil
}

// workspaceFile 实现只读工作区文件的 fs.File 接口。
type workspaceFile struct {
	name    string
	content []byte
	reader  *bytes.Reader
	closed  bool
}

// Stat 返回只读普通文件元信息。
//
// 文件关闭后返回 fs.ErrClosed；成功结果的大小等于快照字节长度，修改时间固定为零值。
func (file *workspaceFile) Stat() (fs.FileInfo, error) {
	// nil 或已关闭文件不再提供元信息。
	if file == nil || file.closed {
		return nil, fs.ErrClosed
	}
	return workspaceFileInfo{name: file.name, size: int64(len(file.content))}, nil
}

// Read 从当前偏移读取工作区文件内容。
//
// target 可以为空；文件关闭后返回 fs.ErrClosed，其余语义委托 bytes.Reader。
func (file *workspaceFile) Read(target []byte) (int, error) {
	// nil 或已关闭文件不能继续读取。
	if file == nil || file.closed {
		return 0, fs.ErrClosed
	}
	return file.reader.Read(target)
}

// Close 关闭工作区文件句柄。
//
// Close 幂等且不修改底层快照；关闭后 Read 和 Stat 返回 fs.ErrClosed。
func (file *workspaceFile) Close() error {
	// 重复关闭保持成功，匹配只读内存句柄的安全清理语义。
	if file == nil || file.closed {
		return nil
	}
	file.closed = true
	return nil
}

// workspaceFileInfo 保存工作区快照文件的稳定元信息。
type workspaceFileInfo struct {
	name string
	size int64
}

// Name 返回文件基础名称。
func (info workspaceFileInfo) Name() string {
	// 文件系统路径已在 Open 时裁剪为基础名称。
	return info.name
}

// Size 返回文件字节长度。
func (info workspaceFileInfo) Size() int64 {
	// 快照创建后内容不可变，因此大小保持稳定。
	return info.size
}

// Mode 返回只读普通文件权限。
func (info workspaceFileInfo) Mode() fs.FileMode {
	// VFS 不允许写入，使用 0444 普通文件模式。
	return 0o444
}

// ModTime 返回确定性的零值修改时间。
func (info workspaceFileInfo) ModTime() time.Time {
	// 浏览器快照协议当前不传输修改时间，避免伪造宿主时间。
	return time.Time{}
}

// IsDir 报告当前条目不是目录。
func (info workspaceFileInfo) IsDir() bool {
	// workspaceFilesystem 只为具体文件创建句柄。
	return false
}

// Sys 返回 nil，因为浏览器快照没有宿主系统元数据。
func (info workspaceFileInfo) Sys() any {
	// 不泄露本地目录句柄或平台相关信息。
	return nil
}
