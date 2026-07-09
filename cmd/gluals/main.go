// Package main 提供 glua language server 可执行程序入口。
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ZingYao/go-lua-vm/extensions"
	"github.com/ZingYao/go-lua-vm/internal/buildinfo"
	"github.com/ZingYao/go-lua-vm/internal/lsp"
)

// main 启动 glua language server。
//
// 默认通过 stdin/stdout 使用 LSP Content-Length JSON-RPC；可选 `--gluals-syntax` 用于指定语法扩展集合。
func main() {
	// 解析少量启动参数后进入 LSP 主循环。
	options, err := parseArgs(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if options.Help {
		// 帮助模式只输出说明，不启动 LSP Content-Length 循环。
		_, _ = fmt.Fprint(os.Stdout, helpText())
		return
	}
	server := lsp.New(os.Stdin, os.Stdout, options.Syntax)
	if err := server.Run(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// options 保存 gluals 启动参数解析结果。
type options struct {
	// Syntax 保存 LSP 解析和诊断使用的语法扩展集合。
	Syntax extensions.SyntaxSet
	// Help 表示是否输出帮助文本。
	Help bool
}

// parseArgs 解析 gluals 启动参数。
//
// args 不包含程序名；当前支持 `--gluals-syntax value` 和 `--gluals-syntax=value`，默认使用当前构建默认扩展。
func parseArgs(args []string) (options, error) {
	// 默认启用当前构建产物包含的扩展，让 IDE 能识别 switch/continue 语法糖。
	parsedOptions := options{Syntax: extensions.Default()}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "-h" || argument == "--help" {
			// -h/--help 输出帮助文本，方便确认 gluals 构建能力。
			parsedOptions.Help = true
			continue
		}
		if argument == "--gluals-syntax" {
			// 独立 --gluals-syntax 必须消费下一个参数。
			index++
			if index >= len(args) {
				return options{}, fmt.Errorf("option --gluals-syntax requires an argument")
			}
			parsedSyntax, err := extensions.ParseSyntaxSet(args[index])
			if err != nil {
				return options{}, err
			}
			parsedOptions.Syntax = parsedSyntax
			continue
		}
		if len(argument) > len("--gluals-syntax=") && argument[:len("--gluals-syntax=")] == "--gluals-syntax=" {
			// 等号形式便于 IDE 配置。
			parsedSyntax, err := extensions.ParseSyntaxSet(argument[len("--gluals-syntax="):])
			if err != nil {
				return options{}, err
			}
			parsedOptions.Syntax = parsedSyntax
			continue
		}
		return options{}, fmt.Errorf("unrecognized option %q", argument)
	}
	return parsedOptions, nil
}

// helpText 返回 gluals 命令帮助文本。
func helpText() string {
	// LSP 可执行文件也展示构建能力，便于 IDE 侧确认语言服务器语法模式来源。
	var builder strings.Builder
	builder.WriteString("GLua language server\n\n")
	builder.WriteString("Usage: gluals [options]\n\n")
	builder.WriteString("Options:\n")
	builder.WriteString("  -h, --help               show this help\n")
	builder.WriteString("  --gluals-syntax value    select syntax mode: lua53, extended, all, continue,switch,const\n\n")
	builder.WriteString(buildinfo.FeatureText(false))
	return builder.String()
}
