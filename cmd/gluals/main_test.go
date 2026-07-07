package main

import (
	"testing"

	"github.com/ZingYao/go-lua-vm/extensions"
)

// TestParseArgsGlualsSyntax 验证 gluals 使用独立项目命名空间参数控制语法扩展。
func TestParseArgsGlualsSyntax(t *testing.T) {
	// 使用等号形式覆盖 IDE 配置常见写法。
	syntax, err := parseArgs([]string{"--gluals-syntax=lua53"})
	if err != nil {
		// 合法 gluals 语法参数不应失败。
		t.Fatalf("parseArgs gluals syntax failed: %v", err)
	}
	if syntax != extensions.None() {
		// lua53 模式必须关闭全部扩展。
		t.Fatalf("syntax = %#v, want none", syntax)
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
