package playground

import (
	"io/fs"
	"testing"
)

// TestWorkspaceFilesystemReadAndTraversal 验证浏览器快照文件可读且非法路径不会进入 VFS。
func TestWorkspaceFilesystemReadAndTraversal(t *testing.T) {
	// 同时提供合法文件、父目录穿越和绝对路径，只有合法文件应可访问。
	filesystem := newWorkspaceFilesystem(map[string]string{
		"docs/readme.txt": "hello",
		"../secret.txt":   "secret",
		"/absolute.txt":   "absolute",
	})
	if filesystem == nil {
		// 至少一个合法文件时必须创建 VFS。
		t.Fatal("newWorkspaceFilesystem returned nil")
	}
	content, err := fs.ReadFile(filesystem, "docs/readme.txt")
	if err != nil || string(content) != "hello" {
		// 合法相对文件应保持完整字节内容。
		t.Fatalf("ReadFile content=%q err=%v", content, err)
	}
	if _, err := fs.ReadFile(filesystem, "secret.txt"); err == nil {
		// 父目录穿越键不能被清洗成根目录文件。
		t.Fatal("traversal file unexpectedly readable")
	}
	if _, err := fs.ReadFile(filesystem, "absolute.txt"); err == nil {
		// 绝对路径键不能被裁剪成工作区文件。
		t.Fatal("absolute file unexpectedly readable")
	}
}

// TestWorkspaceFileClose 验证只读内存文件关闭后的行为。
func TestWorkspaceFileClose(t *testing.T) {
	// 关闭句柄后 Read 和 Stat 都应返回 fs.ErrClosed，重复关闭保持幂等。
	filesystem := newWorkspaceFilesystem(map[string]string{"value.txt": "value"})
	file, err := filesystem.Open("value.txt")
	if err != nil {
		// 合法文件必须可打开。
		t.Fatalf("Open failed: %v", err)
	}
	if err := file.Close(); err != nil {
		// 第一次关闭应成功。
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := file.Read(make([]byte, 1)); err != fs.ErrClosed {
		// 关闭后读取必须返回标准关闭错误。
		t.Fatalf("Read closed error = %v, want fs.ErrClosed", err)
	}
	if _, err := file.Stat(); err != fs.ErrClosed {
		// 关闭后 Stat 必须返回标准关闭错误。
		t.Fatalf("Stat closed error = %v, want fs.ErrClosed", err)
	}
	if err := file.Close(); err != nil {
		// 重复关闭不应制造额外错误。
		t.Fatalf("second Close failed: %v", err)
	}
}
