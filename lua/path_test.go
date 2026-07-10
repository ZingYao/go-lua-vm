package lua

import (
	"path/filepath"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestGluaPathNamespace 验证 glua.path 的纯路径运算、平台常量和错误语义。
//
// 测试不创建任何文件；输入只覆盖词法路径，保证结果不依赖工作目录内容。
func TestGluaPathNamespace(t *testing.T) {
	// 通过公开 Lua API 执行脚本，覆盖命名空间注册和多返回值。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 命名空间注册失败时无法继续验证路径 API。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	separator := string(filepath.Separator)
	state.Globals().RawSetString("_TEST_SEPARATOR", runtime.StringValue(separator))
	script := `
local path = glua.path
assert(type(path) == "table")
assert(path.separator == _TEST_SEPARATOR)
assert(type(path.listSeparator) == "string")
assert(path.join("root", "folder", "file.txt") == "root" .. path.separator .. "folder" .. path.separator .. "file.txt")
assert(path.clean("root" .. path.separator .. "." .. path.separator .. "folder" .. path.separator .. ".." .. path.separator .. "file.txt") == "root" .. path.separator .. "file.txt")
local joined = path.join("root", "folder", "file.txt")
assert(path.base(joined) == "file.txt")
assert(path.dir(joined) == path.join("root", "folder"))
assert(path.ext("archive.tar.gz") == ".gz")
assert(path.isAbs("relative/file") == false)
assert(path.rel("root/folder", "root/file.txt") == ".." .. path.separator .. "file.txt")
local directory, fileName = path.split(joined)
assert(directory == path.join("root", "folder") .. path.separator and fileName == "file.txt")
assert(type(path.volume("root/file.txt")) == "string")
assert(path.toSlash(path.fromSlash("root/folder/file.txt")) == "root/folder/file.txt")
assert(path.match("*.txt", "file.txt"))
assert(not path.match("*.txt", "file.lua"))
local ok = pcall(path.match, "[", "file")
assert(not ok)
local typeOK = pcall(path.join, "root", 1)
assert(not typeOK)
`
	if err := DoString(state, script); err != nil {
		// 执行错误说明路径 API 与公开约定不一致。
		t.Fatalf("run glua.path test: %v object=%s", err, runtime.ErrorObject(err).DebugString())
	}
}
