// Package buildinfo 汇总当前二进制编译进来的 GLua 扩展能力。
package buildinfo

import (
	"fmt"
	"strings"

	"github.com/ZingYao/go-lua-vm/extensions"
	"github.com/ZingYao/go-lua-vm/internal/localize"
	"github.com/ZingYao/go-lua-vm/internal/native"
	"github.com/ZingYao/go-lua-vm/runtime"
)

// FeatureText 返回适合 CLI 版本、帮助和启动横幅展示的能力摘要。
func FeatureText(includeDAP bool) string {
	// 使用 strings.Builder 逐行组装，避免多个命令维护不一致的功能描述。
	var builder strings.Builder
	builder.WriteString(localize.Text("GLua build features:\n", "GLua 构建能力：\n"))
	builder.WriteString(fmt.Sprintf(localize.Text("  syntax sugar: %s\n", "  语法糖：%s\n"), syntaxFeatureText()))
	builder.WriteString(fmt.Sprintf(localize.Text("  glua events: %s\n", "  glua events：%s\n"), eventFeatureText()))
	builder.WriteString(fmt.Sprintf(localize.Text("  lua c modules/native_module: %s\n", "  Lua C 模块/native_module：%s\n"), nativeFeatureText()))
	builder.WriteString("  package.loadlib: ")
	if native.Enabled() {
		// native_modules 构建下 package.loadlib 会接入真实动态库 loader。
		builder.WriteString(localize.Text("enabled by native_modules\n", "已通过 native_modules 启用\n"))
	} else {
		// 默认构建保持纯 Go 策略，package.loadlib 仍返回禁用说明。
		builder.WriteString(localize.Text("disabled in this build\n", "当前构建未启用\n"))
	}
	builder.WriteString(localize.Text("  dap server: ", "  DAP 服务："))
	if includeDAP {
		// glua 解释器可通过 --glua-dap-listen 启动内置 DAP server。
		builder.WriteString(localize.Text("enabled (--glua-dap-listen host:port)\n", "已启用（--glua-dap-listen host:port）\n"))
	} else {
		// gluac 只负责编译和反汇编，不承载运行时调试 server。
		builder.WriteString(localize.Text("not applicable for this command\n", "当前命令不适用\n"))
	}
	return builder.String()
}

// eventFeatureText 返回 glua 自定义事件能力的可读状态。
func eventFeatureText() string {
	// event 能力由 runtime 包的 build tags 决定，这里只读取最终编译状态。
	if runtime.GluaEventsCompiled() {
		return localize.Text("enabled", "已启用")
	}
	return localize.Text("disabled", "未启用")
}

// syntaxFeatureText 返回当前构建包含的语法糖名称列表。
func syntaxFeatureText() string {
	// 语法扩展由 extensions 包的 build tags 决定，这里只读取最终编译集合。
	names := extensions.Compiled().Names()
	if len(names) == 0 {
		// lua53 或关闭全部扩展时明确展示没有语法糖。
		return localize.Text("none", "无")
	}
	return strings.Join(names, ",")
}

// nativeFeatureText 返回 native 模块能力的可读状态。
func nativeFeatureText() string {
	// native.Enabled 由 build tag 文件提供，避免 CLI 输出与真实构建能力漂移。
	if native.Enabled() {
		// 开启时说明可以加载 Lua C API 风格动态模块。
		return localize.Text("enabled", "已启用")
	}
	return localize.Text("disabled", "未启用")
}
