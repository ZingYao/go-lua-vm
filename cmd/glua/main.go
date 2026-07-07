// Package main 提供 glua 可执行程序入口。
//
// 该入口保持极薄，只负责连接 OS 参数、标准流和退出码；具体 CLI 语义由 internal/cli 包维护。
package main

import (
	"context"
	"os"

	"github.com/ZingYao/go-lua-vm/internal/cli"
)

// main 启动 glua 命令行程序。
//
// 参数来自 os.Args[1:]；标准流直接使用当前进程的 stdin/stdout/stderr；退出码由 cli.Main 返回。
func main() {
	// main 不承载业务逻辑，只把系统边界转交给 CLI 包。
	exitCode := cli.Main(context.Background(), os.Args[1:], cli.Streams{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	os.Exit(exitCode)
}
