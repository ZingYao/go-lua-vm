package main

import (
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/extensions"
)

// TestParseArgsGlualsSyntax 验证 gluals 使用独立项目命名空间参数控制语法扩展。
func TestParseArgsGlualsSyntax(t *testing.T) {
	// 使用等号形式覆盖 IDE 配置常见写法。
	options, err := parseArgs([]string{"--gluals-syntax=lua53"})
	if err != nil {
		// 合法 gluals 语法参数不应失败。
		t.Fatalf("parseArgs gluals syntax failed: %v", err)
	}
	if options.Syntax != extensions.None() {
		// lua53 模式必须关闭全部扩展。
		t.Fatalf("syntax = %#v, want none", options.Syntax)
	}
}

// TestParseArgsGlualsHelp 验证 gluals 支持帮助参数。
func TestParseArgsGlualsHelp(t *testing.T) {
	// 帮助参数不应改变默认语法集合，也不应启动 LSP 服务。
	options, err := parseArgs([]string{"--help"})
	if err != nil {
		// 合法帮助参数不应失败。
		t.Fatalf("parseArgs help failed: %v", err)
	}
	if !options.Help {
		// Help 标记必须传回 main，才能提前输出帮助并退出。
		t.Fatalf("help should be true")
	}
	if !strings.Contains(helpText(), "Usage: gluals") || !strings.Contains(helpText(), "GLua build features:") {
		// 帮助文本必须能说明入口和构建能力。
		t.Fatalf("help text missing required sections")
	}
}

// TestParseArgsRejectsLegacySyntaxFlag 验证 gluals 不再接受无命名空间的扩展参数。
func TestParseArgsRejectsLegacySyntaxFlag(t *testing.T) {
	// 旧 --syntax 参数容易与其他工具语义混淆，必须拒绝。
	if _, err := parseArgs([]string{"--syntax=lua53"}); err == nil {
		// 未失败说明扩展参数又回退到了无项目前缀命名空间。
		t.Fatalf("parseArgs should reject legacy --syntax")
	}
}
