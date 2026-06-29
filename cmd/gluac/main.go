// Package main 提供 gluac 可执行程序入口。
//
// 该入口保持极薄，只负责连接 OS 参数、标准流和退出码；具体 luac 兼容语义由 internal/luac 包维护。
package main

import (
	"os"

	"github.com/zing/go-lua-vm/internal/luac"
)

// main 启动 gluac 命令行程序。
//
// 参数来自 os.Args[1:]；stdout/stderr 直接连接当前进程；退出码由 luac.Main 返回。
func main() {
	// main 不承载业务逻辑，只把系统边界转交给 luac 包。
	exitCode := luac.Main(os.Args[1:], luac.Streams{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	os.Exit(exitCode)
}
