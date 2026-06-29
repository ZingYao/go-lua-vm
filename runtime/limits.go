package runtime

import (
	"fmt"

	"github.com/zing/go-lua-vm/extensions"
)

const (
	// DefaultMaxStackDepth 对齐 Lua 5.3 在 32 位以上 int 环境下的 LUAI_MAXSTACK 默认值。
	DefaultMaxStackDepth = 1000000
	// DefaultMaxCallDepth 为 Lua 调用帧提供默认预算，预留主 chunk、标准库和 debug 包装帧余量。
	DefaultMaxCallDepth = 1000
)

// Options 描述 Lua State 的资源限制配置。
//
// 零值会被 NormalizeOptions 转换为项目默认值；MaxAllocationBudget 为 0 表示不限制分配预算。
// 宿主访问权限默认关闭，嵌入方必须显式开启后标准库才可访问环境变量、文件系统或进程。
type Options struct {
	// MaxStackDepth 限制 Lua 栈最大槽位数量。
	MaxStackDepth int
	// MaxCallDepth 限制嵌套调用深度。
	MaxCallDepth int
	// MaxAllocationBudget 限制运行期可分配预算，单位暂定为字节。
	MaxAllocationBudget int64
	// AllowHostFilesystem 表示 io/os 标准库是否允许访问宿主文件系统。
	AllowHostFilesystem bool
	// AllowEnvironment 表示 os.getenv 和 package 初始化是否允许读取宿主环境变量。
	AllowEnvironment bool
	// AllowProcess 表示 io.popen 和 os.execute 是否允许启动宿主进程。
	AllowProcess bool
	// SyntaxExtensions 保存源码编译阶段启用的可选语法扩展集合。
	SyntaxExtensions extensions.SyntaxSet
	// SyntaxExtensionsSet 表示调用方是否显式设置过 SyntaxExtensions。
	SyntaxExtensionsSet bool
}

// NormalizeOptions 规范化 State 资源限制选项。
//
// 入参 options 可以是零值；返回值会填充默认栈深度、默认调用深度，并保留用户设置的分配预算。
func NormalizeOptions(options Options) Options {
	// 栈深度未设置时使用 Lua 5.3 默认上限。
	if options.MaxStackDepth <= 0 {
		options.MaxStackDepth = DefaultMaxStackDepth
	}
	// 调用深度未设置时使用项目默认调用帧预算。
	if options.MaxCallDepth <= 0 {
		options.MaxCallDepth = DefaultMaxCallDepth
	}
	// 负分配预算没有业务意义，归一化为 0 表示不限制。
	if options.MaxAllocationBudget < 0 {
		options.MaxAllocationBudget = 0
	}
	if !options.SyntaxExtensionsSet {
		// 未显式配置语法时，默认启用当前构建产物包含的扩展。
		options.SyntaxExtensions = extensions.Default()
	} else {
		// 显式配置时裁剪未编译扩展，保证 runtime 选项不会绕过 build tag。
		options.SyntaxExtensions &= extensions.Compiled()
	}

	// 返回已经填充默认值的选项。
	return options
}

// WithSyntaxExtensions 返回启用指定语法集合后的 Options 副本。
//
// syntax 会裁剪到当前构建产物已编译集合；该方法适合嵌入方从 DefaultOptions 开始切换
// lua53、extended 或自定义扩展组合。
func (options Options) WithSyntaxExtensions(syntax extensions.SyntaxSet) Options {
	// 显式标记语法配置，允许调用方传入 0 表示关闭所有扩展。
	options.SyntaxExtensions = syntax & extensions.Compiled()
	options.SyntaxExtensionsSet = true
	return options
}

// WithoutSyntaxExtensions 返回关闭指定语法扩展后的 Options 副本。
//
// 未显式配置过语法集合时先使用默认集合，再移除 disabled 指定的扩展。
func (options Options) WithoutSyntaxExtensions(disabled extensions.SyntaxSet) Options {
	// 先规范化，确保零值 options 也有默认语法集合可供裁剪。
	normalized := NormalizeOptions(options)
	normalized.SyntaxExtensions = normalized.SyntaxExtensions.Without(disabled)
	normalized.SyntaxExtensionsSet = true
	return normalized
}

// ResourceLimitKind 表示触发的资源限制类型。
//
// VM、栈、GC 和 bridge 层都应使用该分类构造 ResourceLimitError。
type ResourceLimitKind string

const (
	// ResourceLimitStack 表示 Lua 栈深度超过限制。
	ResourceLimitStack ResourceLimitKind = "stack"
	// ResourceLimitCall 表示调用深度超过限制。
	ResourceLimitCall ResourceLimitKind = "call"
	// ResourceLimitAllocation 表示分配预算超过限制。
	ResourceLimitAllocation ResourceLimitKind = "allocation"
)

// ResourceLimitError 表示 Lua VM 运行中触发资源限制。
//
// 该错误可通过 errors.As 识别，Message 提供接近 Lua 错误文本的说明。
type ResourceLimitError struct {
	// Kind 标识触发的资源限制类别。
	Kind ResourceLimitKind
	// Limit 是配置上限。
	Limit int64
	// Actual 是实际请求或使用量。
	Actual int64
	// Message 是面向调用方的错误说明。
	Message string
}

// Error 返回资源限制错误文本。
//
// 当 Message 非空时优先返回 Message，否则返回包含 kind、actual 和 limit 的通用说明。
func (err *ResourceLimitError) Error() string {
	// 自定义消息通常用于对齐 Lua 的 "stack overflow" 等错误文本。
	if err.Message != "" {
		return err.Message
	}

	// 没有自定义消息时返回结构化通用文本。
	return fmt.Sprintf("%s resource limit exceeded: actual=%d limit=%d", err.Kind, err.Actual, err.Limit)
}

// CheckStackDepth 检查给定栈深度是否超过配置限制。
//
// currentDepth 表示请求后的栈槽位数量；超过 MaxStackDepth 时返回 ResourceLimitError。
func (options Options) CheckStackDepth(currentDepth int) error {
	// 先规范化选项，确保零值 options 也有默认限制。
	normalized := NormalizeOptions(options)
	if currentDepth > normalized.MaxStackDepth {
		// 超过栈上限时返回接近 Lua 语义的 stack overflow。
		return &ResourceLimitError{Kind: ResourceLimitStack, Limit: int64(normalized.MaxStackDepth), Actual: int64(currentDepth), Message: "stack overflow"}
	}

	// 未超过限制时返回 nil。
	return nil
}

// CheckCallDepth 检查给定调用深度是否超过配置限制。
//
// currentDepth 表示请求后的调用深度；超过 MaxCallDepth 时返回 ResourceLimitError。
func (options Options) CheckCallDepth(currentDepth int) error {
	// 先规范化选项，确保零值 options 也有默认限制。
	normalized := NormalizeOptions(options)
	if currentDepth > normalized.MaxCallDepth {
		// 超过调用深度时返回接近 Lua 语义的 C stack overflow。
		return &ResourceLimitError{Kind: ResourceLimitCall, Limit: int64(normalized.MaxCallDepth), Actual: int64(currentDepth), Message: "C stack overflow"}
	}

	// 未超过限制时返回 nil。
	return nil
}

// CheckAllocationBudget 检查给定分配量是否超过配置预算。
//
// usedBytes 表示请求后的累计分配量；MaxAllocationBudget 为 0 时表示不限制。
func (options Options) CheckAllocationBudget(usedBytes int64) error {
	// 先规范化选项，确保负预算被转换为不限制。
	normalized := NormalizeOptions(options)
	if normalized.MaxAllocationBudget == 0 {
		// 预算为 0 表示不限制，直接通过。
		return nil
	}
	if usedBytes > normalized.MaxAllocationBudget {
		// 超过分配预算时返回资源限制错误，后续 GC/allocator 可据此中断执行。
		return &ResourceLimitError{Kind: ResourceLimitAllocation, Limit: normalized.MaxAllocationBudget, Actual: usedBytes, Message: "allocation budget exceeded"}
	}

	// 未超过限制时返回 nil。
	return nil
}
