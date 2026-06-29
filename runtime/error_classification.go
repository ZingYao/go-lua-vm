package runtime

import "errors"

const (
	// ErrorClassRuntime 表示 Lua 运行期错误分类。
	ErrorClassRuntime ErrorClass = "runtime"
	// ErrorClassResourceLimit 表示资源限制错误分类。
	ErrorClassResourceLimit ErrorClass = "resource_limit"
	// ErrorClassOther 表示当前 runtime 包不能识别的错误分类。
	ErrorClassOther ErrorClass = "other"
)

// ErrorClass 表示 runtime 层可识别的错误分类。
//
// 该分类服务 Go 嵌入 API、CLI 退出码和后续 debug traceback 拼接；syntax error 分类位于
// compiler/parser 包，避免 runtime 反向依赖 compiler。
type ErrorClass string

// ClassifyError 返回 runtime 层可识别的错误分类。
//
// err 可以是 nil、RuntimeError、ResourceLimitError 或其他 Go error。nil 和未知错误返回
// ErrorClassOther；调用方若需要 parser syntax 分类，应在 parser 包内使用对应 helper。
func ClassifyError(err error) ErrorClass {
	if IsResourceLimitError(err) {
		// 资源限制错误比普通 runtime error 更具体，优先返回资源限制分类。
		return ErrorClassResourceLimit
	}
	if IsRuntimeError(err) {
		// RuntimeError 或可转换为 Lua error object 的错误属于运行期错误。
		return ErrorClassRuntime
	}

	// nil 或未知错误在 runtime 层不做更细分类。
	return ErrorClassOther
}

// IsRuntimeError 判断错误是否属于 Lua 运行期错误。
//
// err 链中包含 RuntimeError、ErrLuaError 或 ErrExpectedCallable 时返回 true。资源限制错误
// 由 IsResourceLimitError 单独识别，避免调用方丢失更具体分类。
func IsRuntimeError(err error) bool {
	if err == nil {
		// nil 错误不属于任何失败分类。
		return false
	}
	if IsResourceLimitError(err) {
		// 资源限制错误单独归类，不混入普通 runtime error。
		return false
	}

	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		// RuntimeError 是 Lua 运行期错误的主要包装类型。
		return true
	}
	if errors.Is(err, ErrLuaError) || errors.Is(err, ErrExpectedCallable) {
		// 直接暴露的 Lua error 哨兵也应被识别为运行期错误。
		return true
	}

	// 其他 Go error 需要先通过 LuaErrorFromGo 转换，当前不直接归类为 runtime。
	return false
}

// IsResourceLimitError 判断错误链中是否包含资源限制错误。
//
// err 链中包含 ResourceLimitError 时返回 true；调用方可继续使用 errors.As 取出 Kind、
// Limit 和 Actual 等结构化字段。
func IsResourceLimitError(err error) bool {
	if err == nil {
		// nil 错误不属于资源限制。
		return false
	}

	var resourceErr *ResourceLimitError
	if errors.As(err, &resourceErr) {
		// ResourceLimitError 支持 errors.As，便于保留具体限制类型。
		return true
	}

	// 错误链中没有资源限制结构。
	return false
}
