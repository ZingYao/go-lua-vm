// Package main 提供 glua language server 可执行程序入口。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zing/go-lua-vm/extensions"
	"github.com/zing/go-lua-vm/internal/lsp"
)

// main 启动 glua language server。
//
// 默认通过 stdin/stdout 使用 LSP Content-Length JSON-RPC；可选 `--gluals-syntax` 用于指定语法扩展集合。
func main() {
	// 解析少量启动参数后进入 LSP 主循环。
	syntax, err := parseArgs(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := lsp.New(os.Stdin, os.Stdout, syntax)
	if err := server.Run(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseArgs 解析 gluals 启动参数。
//
// args 不包含程序名；当前支持 `--gluals-syntax value` 和 `--gluals-syntax=value`，默认使用当前构建默认扩展。
func parseArgs(args []string) (extensions.SyntaxSet, error) {
	// 默认启用当前构建产物包含的扩展，让 IDE 能识别 switch/continue 语法糖。
	syntax := extensions.Default()
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--gluals-syntax" {
			// 独立 --gluals-syntax 必须消费下一个参数。
			index++
			if index >= len(args) {
				return 0, fmt.Errorf("option --gluals-syntax requires an argument")
			}
			parsedSyntax, err := extensions.ParseSyntaxSet(args[index])
			if err != nil {
				return 0, err
			}
			syntax = parsedSyntax
			continue
		}
		if len(argument) > len("--gluals-syntax=") && argument[:len("--gluals-syntax=")] == "--gluals-syntax=" {
			// 等号形式便于 IDE 配置。
			parsedSyntax, err := extensions.ParseSyntaxSet(argument[len("--gluals-syntax="):])
			if err != nil {
				return 0, err
			}
			syntax = parsedSyntax
			continue
		}
		return 0, fmt.Errorf("unrecognized option %q", argument)
	}
	return syntax, nil
}
