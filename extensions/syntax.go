// Package extensions 保存 go-lua-vm 的可选语法扩展注册表。
//
// 该包只定义轻量位图和参数解析，不依赖 parser、codegen 或 runtime 细节，便于命令行、
// Go 嵌入 API 和编译阶段共用同一套开关。
package extensions

import (
	"fmt"
	"sort"
	"strings"
)

// SyntaxSet 表示一组可选语法扩展。
//
// 每一位对应一个扩展能力，parser 热路径只需要按位检查，避免运行时 map 查询开销。
type SyntaxSet uint64

const (
	// SyntaxContinue 表示启用 continue 语句语法糖。
	SyntaxContinue SyntaxSet = 1 << iota
	// SyntaxSwitch 表示启用 switch/case/default 语句语法糖。
	SyntaxSwitch
)

const (
	// SyntaxNameContinue 是 continue 扩展的用户可见名称。
	SyntaxNameContinue = "continue"
	// SyntaxNameSwitch 是 switch 扩展的用户可见名称。
	SyntaxNameSwitch = "switch"
)

// None 返回不启用任何扩展的 Lua 5.3 兼容语法集合。
func None() SyntaxSet {
	// 空集合代表纯 Lua 5.3 语法，不包含项目扩展。
	return 0
}

// Default 返回当前构建产物默认启用的语法扩展集合。
func Default() SyntaxSet {
	// 默认集合只包含当前二进制已经编译进来的扩展。
	return Compiled()
}

// Has 判断集合是否包含指定扩展。
func (set SyntaxSet) Has(feature SyntaxSet) bool {
	// 使用按位与判断功能位，feature 允许传入单个或多个扩展位。
	return set&feature == feature
}

// Without 返回移除指定扩展后的集合。
func (set SyntaxSet) Without(feature SyntaxSet) SyntaxSet {
	// 按位清除功能位，不影响集合中的其他扩展。
	return set &^ feature
}

// With 返回加入指定扩展后的集合。
func (set SyntaxSet) With(feature SyntaxSet) SyntaxSet {
	// 按位加入功能位，不影响集合中的其他扩展。
	return set | feature
}

// Names 返回集合中已启用扩展的稳定名称列表。
func (set SyntaxSet) Names() []string {
	// 按固定顺序输出，避免文档、测试和 CLI 信息受 map 顺序影响。
	names := make([]string, 0, 2)
	if set.Has(SyntaxContinue) {
		// continue 位启用时展示对应名称。
		names = append(names, SyntaxNameContinue)
	}
	if set.Has(SyntaxSwitch) {
		// switch 位启用时展示对应名称。
		names = append(names, SyntaxNameSwitch)
	}
	return names
}

// ParseSyntaxSet 解析用户传入的语法扩展集合。
//
// text 支持 lua53、extended、all，以及逗号分隔的扩展名称。返回集合会自动裁剪到当前二进制
// 已编译的扩展范围；请求未编译的扩展会返回错误。
func ParseSyntaxSet(text string) (SyntaxSet, error) {
	// 空字符串按默认扩展集合处理，便于 CLI 等号参数复用。
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == "extended" || trimmed == "all" {
		// extended/all 表示启用当前构建内所有扩展。
		return Default(), nil
	}
	if trimmed == "lua53" || trimmed == "none" {
		// lua53/none 表示关闭所有扩展，恢复 Lua 5.3 关键字集合。
		return None(), nil
	}

	var set SyntaxSet
	parts := strings.Split(trimmed, ",")
	for _, part := range parts {
		// 每个名称独立解析，允许用户写 --syntax=continue,switch。
		name := strings.TrimSpace(part)
		if name == "" {
			// 空分段通常来自多余逗号，直接拒绝以避免静默忽略拼写错误。
			return 0, fmt.Errorf("empty syntax extension name")
		}
		feature, err := Lookup(name)
		if err != nil {
			// 未知名称直接返回，调用方负责展示错误。
			return 0, err
		}
		set = set.With(feature)
	}
	if unavailable := set.Without(Compiled()); unavailable != 0 {
		// 用户请求了当前 build tag 未编译的扩展，必须明确报错。
		return 0, fmt.Errorf("syntax extension not compiled: %s", strings.Join(unavailable.Names(), ","))
	}
	return set, nil
}

// ParseDisabledSyntaxSet 解析需要关闭的语法扩展名称列表。
//
// text 必须是逗号分隔扩展名；返回集合用于从默认或显式 syntax 集合中清除指定扩展。
func ParseDisabledSyntaxSet(text string) (SyntaxSet, error) {
	// 空值表示不额外关闭任何扩展。
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		// 无禁用项时返回空集合。
		return None(), nil
	}
	var set SyntaxSet
	parts := strings.Split(trimmed, ",")
	for _, part := range parts {
		// 禁用列表只接受具体扩展名，不接受 all/lua53 这类模式别名。
		name := strings.TrimSpace(part)
		if name == "" {
			// 空名称说明参数存在多余逗号，直接提示用户修正。
			return 0, fmt.Errorf("empty syntax extension name")
		}
		feature, err := Lookup(name)
		if err != nil {
			// 未知扩展不能静默跳过。
			return 0, err
		}
		set = set.With(feature)
	}
	return set, nil
}

// Lookup 按用户可见名称查找单个语法扩展位。
func Lookup(name string) (SyntaxSet, error) {
	// 先规范化大小写，CLI 参数保持大小写不敏感。
	switch strings.ToLower(strings.TrimSpace(name)) {
	case SyntaxNameContinue:
		// continue 名称对应 continue 语句扩展。
		return SyntaxContinue, nil
	case SyntaxNameSwitch:
		// switch 名称对应 switch 语句扩展。
		return SyntaxSwitch, nil
	default:
		// 未知扩展返回带可用名称的错误，便于 CLI 直接展示。
		return 0, fmt.Errorf("unknown syntax extension %q, available: %s", name, strings.Join(AvailableNames(), ","))
	}
}

// AvailableNames 返回当前构建产物已编译扩展的稳定名称列表。
func AvailableNames() []string {
	// 复用 Names 并排序，保证即使未来扩展表扩大也输出稳定。
	names := Compiled().Names()
	sort.Strings(names)
	return names
}
