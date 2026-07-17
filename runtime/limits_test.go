package runtime

import (
	"errors"
	"testing"
)

// TestNormalizeOptions 验证资源限制选项零值会填充默认值。
//
// 默认栈深度和调用深度对齐 Lua 5.3 默认配置，负分配预算会归一为不限制，宿主能力全部开放。
func TestNormalizeOptions(t *testing.T) {
	options := NormalizeOptions(Options{MaxAllocationBudget: -1})

	// 零值栈深度必须填充默认 LUAI_MAXSTACK。
	if options.MaxStackDepth != DefaultMaxStackDepth {
		t.Fatalf("max stack depth mismatch: got %d", options.MaxStackDepth)
	}
	// 零值调用深度必须填充默认 LUAI_MAXCCALLS。
	if options.MaxCallDepth != DefaultMaxCallDepth {
		t.Fatalf("max call depth mismatch: got %d", options.MaxCallDepth)
	}
	// 负预算必须归一为 0，表示不限制。
	if options.MaxAllocationBudget != 0 {
		t.Fatalf("allocation budget mismatch: got %d", options.MaxAllocationBudget)
	}
	if !options.AllowHostFilesystem || !options.AllowEnvironment || !options.AllowProcess {
		// 默认配置必须开放文件系统、环境变量和进程能力。
		t.Fatalf("host access should default to enabled: %#v", options)
	}
}

// TestNormalizeOptionsAlwaysEnablesHostAccess 验证显式 false 不再恢复已移除的宿主访问安全策略。
//
// 三个 Allow 字段仅保留源码兼容，所有 State 规范化后都必须允许访问对应宿主能力。
func TestNormalizeOptionsAlwaysEnablesHostAccess(t *testing.T) {
	// 使用三个字段的零值模拟旧调用方显式或隐式关闭权限的配置。
	options := NormalizeOptions(Options{
		AllowHostFilesystem: false,
		AllowEnvironment:    false,
		AllowProcess:        false,
	})

	if !options.AllowHostFilesystem || !options.AllowEnvironment || !options.AllowProcess {
		// 任一能力仍关闭都表示旧安全策略尚未完整移除。
		t.Fatalf("normalized host access should always be enabled: %#v", options)
	}
}

// TestResourceLimitChecks 验证资源限制检查会返回可识别错误。
//
// 后续 VM、栈和 allocator 可以使用 errors.As 识别 ResourceLimitError。
func TestResourceLimitChecks(t *testing.T) {
	options := Options{MaxStackDepth: 2, MaxCallDepth: 1, MaxAllocationBudget: 10}

	// 栈深度超过限制时必须返回 stack overflow。
	if err := options.CheckStackDepth(3); err == nil || err.Error() != "stack overflow" {
		t.Fatalf("stack limit error mismatch: %v", err)
	}
	// 调用深度超过限制时必须返回 C stack overflow。
	if err := options.CheckCallDepth(2); err == nil || err.Error() != "C stack overflow" {
		t.Fatalf("call limit error mismatch: %v", err)
	}
	// 分配量超过预算时必须返回 allocation budget exceeded。
	if err := options.CheckAllocationBudget(11); err == nil || err.Error() != "allocation budget exceeded" {
		t.Fatalf("allocation limit error mismatch: %v", err)
	}

	var resourceLimitError *ResourceLimitError
	if !errors.As(options.CheckAllocationBudget(11), &resourceLimitError) || resourceLimitError.Kind != ResourceLimitAllocation {
		// 资源限制错误必须支持 errors.As 分类识别。
		t.Fatalf("resource limit classification mismatch: %#v", resourceLimitError)
	}
}

// TestResourceLimitChecksPass 验证未超过限制时不会返回错误。
//
// 分配预算为 0 表示不限制。
func TestResourceLimitChecksPass(t *testing.T) {
	options := Options{MaxStackDepth: 2, MaxCallDepth: 2, MaxAllocationBudget: 0}

	// 栈深度等于限制仍然允许。
	if err := options.CheckStackDepth(2); err != nil {
		t.Fatalf("stack depth should pass: %v", err)
	}
	// 调用深度等于限制仍然允许。
	if err := options.CheckCallDepth(2); err != nil {
		t.Fatalf("call depth should pass: %v", err)
	}
	// 预算为 0 时不限制分配量。
	if err := options.CheckAllocationBudget(1 << 40); err != nil {
		t.Fatalf("unlimited allocation should pass: %v", err)
	}
}

// TestNewStateWithOptions 验证 State 会保存规范化后的资源限制配置。
//
// 该配置后续会被栈、调用帧和分配器复用。
func TestNewStateWithOptions(t *testing.T) {
	state := NewStateWithOptions(Options{MaxStackDepth: 8, MaxCallDepth: 4, MaxAllocationBudget: 1024, AllowHostFilesystem: true, AllowEnvironment: true, AllowProcess: true})
	options := state.Options()

	// State 必须保留调用方传入的资源限制配置。
	if options.MaxStackDepth != 8 || options.MaxCallDepth != 4 || options.MaxAllocationBudget != 1024 {
		t.Fatalf("state options mismatch: %#v", options)
	}
	if !options.AllowHostFilesystem || !options.AllowEnvironment || !options.AllowProcess {
		// 显式宿主访问授权必须随 State 保存，供标准库注册时读取。
		t.Fatalf("state host access options mismatch: %#v", options)
	}
}
