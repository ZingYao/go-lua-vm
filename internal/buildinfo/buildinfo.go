// Package buildinfo 汇总当前二进制编译进来的 GLua 扩展能力。
package buildinfo

import (
	"fmt"
	"strings"

	"github.com/ZingYao/go-lua-vm/extensions"
	"github.com/ZingYao/go-lua-vm/internal/native"
)

// FeatureText 返回适合 CLI 版本、帮助和启动横幅展示的能力摘要。
func FeatureText(includeDAP bool) string {
	// 使用 strings.Builder 逐行组装，避免多个命令维护不一致的功能描述。
	var builder strings.Builder
	builder.WriteString("GLua build features:\n")
	builder.WriteString(fmt.Sprintf("  syntax sugar: %s\n", syntaxFeatureText()))
	builder.WriteString(fmt.Sprintf("  lua c modules/native_module: %s\n", nativeFeatureText()))
	builder.WriteString("  package.loadlib: ")
	if native.Enabled() {
		// native_modules 构建下 package.loadlib 会接入真实动态库 loader。
		builder.WriteString("enabled by native_modules\n")
	} else {
		// 默认构建保持纯 Go 策略，package.loadlib 仍返回禁用说明。
		builder.WriteString("disabled in this build\n")
	}
	builder.WriteString("  dap server: ")
	if includeDAP {
		// glua 解释器可通过 --glua-dap-listen 启动内置 DAP server。
		builder.WriteString("enabled (--glua-dap-listen host:port)\n")
	} else {
		// gluac 只负责编译和反汇编，不承载运行时调试 server。
		builder.WriteString("not applicable for this command\n")
	}
	return builder.String()
}

// syntaxFeatureText 返回当前构建包含的语法糖名称列表。
func syntaxFeatureText() string {
	// 语法扩展由 extensions 包的 build tags 决定，这里只读取最终编译集合。
	names := extensions.Compiled().Names()
	if len(names) == 0 {
		// lua53 或关闭全部扩展时明确展示没有语法糖。
		return "none"
	}
	return strings.Join(names, ",")
}

// nativeFeatureText 返回 native 模块能力的可读状态。
func nativeFeatureText() string {
	// native.Enabled 由 build tag 文件提供，避免 CLI 输出与真实构建能力漂移。
	if native.Enabled() {
		// 开启时说明可以加载 Lua C API 风格动态模块。
		return "enabled"
	}
	return "disabled"
}
