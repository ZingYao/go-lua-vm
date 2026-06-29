package runtime

import (
	"errors"
	"testing"
)

// TestRuntimeErrorClassification 验证 runtime error 分类能识别 RuntimeError 和 Lua error 哨兵。
//
// 分类结果后续会服务 Go 嵌入 API 与 CLI 错误出口，必须支持 errors.Is/errors.As 包装链。
func TestRuntimeErrorClassification(t *testing.T) {
	runtimeErr := NewRuntimeError(StringValue("boom"), errors.New("cause"))
	if !IsRuntimeError(runtimeErr) {
		// RuntimeError 必须被识别为运行期错误。
		t.Fatalf("runtime error should be classified")
	}
	if ClassifyError(runtimeErr) != ErrorClassRuntime {
		// RuntimeError 的分类应为 runtime。
		t.Fatalf("runtime error class mismatch: %s", ClassifyError(runtimeErr))
	}

	luaErr := RaiseError(StringValue("lua"))
	if !IsRuntimeError(luaErr) {
		// RaiseError 构造的错误链必须被识别为运行期错误。
		t.Fatalf("lua error should be classified as runtime")
	}
	if ClassifyError(ErrExpectedCallable) != ErrorClassRuntime {
		// 直接暴露的 callable 错误哨兵属于运行期错误。
		t.Fatalf("callable error class mismatch: %s", ClassifyError(ErrExpectedCallable))
	}
}

// TestResourceLimitErrorClassification 验证资源限制错误会优先归类为 resource_limit。
//
// 资源限制错误是 runtime error 的特殊子类，分类时必须保留更具体的资源限制类型。
func TestResourceLimitErrorClassification(t *testing.T) {
	err := Options{MaxStackDepth: 1}.CheckStackDepth(2)
	if !IsResourceLimitError(err) {
		// ResourceLimitError 必须被资源限制 helper 识别。
		t.Fatalf("resource limit should be classified")
	}
	if IsRuntimeError(err) {
		// 资源限制错误不应被普通 runtime 分类吞掉。
		t.Fatalf("resource limit should not be plain runtime")
	}
	if ClassifyError(err) != ErrorClassResourceLimit {
		// ClassifyError 必须优先返回 resource_limit。
		t.Fatalf("resource limit class mismatch: %s", ClassifyError(err))
	}
}

// TestUnknownErrorClassification 验证 nil 和未知 Go error 不被 runtime 层误分类。
//
// 普通 Go error 需要先通过 LuaErrorFromGo 明确转换，避免宿主内部错误被静默当成 Lua 错误。
func TestUnknownErrorClassification(t *testing.T) {
	if ClassifyError(nil) != ErrorClassOther {
		// nil 错误应返回 other，表示没有失败分类。
		t.Fatalf("nil error class mismatch: %s", ClassifyError(nil))
	}
	if ClassifyError(errors.New("plain")) != ErrorClassOther {
		// 未转换的普通 Go error 不应自动归类为 runtime。
		t.Fatalf("plain error class mismatch: %s", ClassifyError(errors.New("plain")))
	}
}
