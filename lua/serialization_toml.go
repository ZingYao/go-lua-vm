package lua

import (
	"fmt"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
	"github.com/pelletier/go-toml/v2"
)

// gluaTOMLEncode 把 Lua 对象编码为 TOML 文本。
//
// args 包含一个对象 table 和可选共用 options；返回 TOML string。TOML 不支持 null，包含
// glua.null、循环引用、混合键或非字符串对象键时返回 Lua error。
func gluaTOMLEncode(args ...runtime.Value) ([]runtime.Value, error) {
	// TOML 编码接受 value 和可选 options。
	if len(args) < 1 || len(args) > 2 {
		// 参数数量错误时不进入转换。
		return nil, gluaSerializationError("glua.toml.encode expects value and optional options")
	}
	optionValue := runtime.NilValue()
	if len(args) == 2 {
		// 第二参数提供共用资源限制。
		optionValue = args[1]
	}
	options, err := parseGluaSerializationOptions(optionValue, "glua.toml.encode")
	if err != nil {
		// options 错误直接返回。
		return nil, err
	}
	if err := validateGluaSerializationValue(args[0], options); err != nil {
		// 在结构转换前检查深度、节点和循环预算。
		return nil, gluaSerializationError("glua.toml.encode: " + err.Error())
	}
	nativeValue, err := gluaValueToStructured(args[0], make(map[*runtime.Table]bool), "$")
	if err != nil {
		// table 形状或值类型错误直接返回。
		return nil, gluaSerializationError("glua.toml.encode: " + err.Error())
	}
	if containsGluaStructuredNull(nativeValue) {
		// TOML 规范没有 null，拒绝产生含义不明确的空值。
		return nil, gluaSerializationError("glua.toml.encode: TOML does not support null")
	}
	encoded, err := toml.Marshal(nativeValue)
	if err != nil {
		// TOML 库错误统一转为 Lua error。
		return nil, gluaSerializationError("glua.toml.encode: " + err.Error())
	}
	if err := checkGluaSerializationOutput("glua.toml.encode", encoded, options); err != nil {
		// 超限结果不返回给 Lua。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(string(encoded))}, nil
}

// gluaTOMLDecode 把 TOML 文本解码为 Lua 对象。
//
// args 包含一个 TOML string 和可选 options；返回对象 table。日期时间值按 TOML 原始文本形式
// 恢复为 Lua string，数字遵守 numberMode；语法错误或资源超限返回 Lua error。
func gluaTOMLDecode(args ...runtime.Value) ([]runtime.Value, error) {
	// TOML 解码接受 text 和可选 options。
	if len(args) < 1 || len(args) > 2 || args[0].Kind != runtime.KindString {
		// 参数错误不进入 TOML parser。
		return nil, gluaSerializationError("glua.toml.decode expects string and optional options")
	}
	optionValue := runtime.NilValue()
	if len(args) == 2 {
		// 第二参数提供资源和数字策略。
		optionValue = args[1]
	}
	options, err := parseGluaSerializationOptions(optionValue, "glua.toml.decode")
	if err != nil {
		// options 错误直接返回。
		return nil, err
	}
	if err := checkGluaSerializationInput("glua.toml.decode", args[0].String, options); err != nil {
		// 超限输入不进入 parser。
		return nil, err
	}
	decoded := make(map[string]any)
	if err := toml.Unmarshal([]byte(args[0].String), &decoded); err != nil {
		// TOML 语法和类型错误转为 Lua error。
		return nil, gluaSerializationError("glua.toml.decode: " + err.Error())
	}
	normalized, err := normalizeTOMLValue(decoded)
	if err != nil {
		// 未知 TOML 节点类型不能静默字符串化。
		return nil, gluaSerializationError("glua.toml.decode: " + err.Error())
	}
	if err := validateGluaStructuredValue(normalized, options); err != nil {
		// 中间树必须满足资源预算。
		return nil, gluaSerializationError("glua.toml.decode: " + err.Error())
	}
	value, err := gluaStructuredToValueWithOptions(normalized, options)
	if err != nil {
		// 数字范围或结构错误转为 TOML API 错误。
		return nil, gluaSerializationError("glua.toml.decode: " + err.Error())
	}
	return []runtime.Value{value}, nil
}

// containsGluaStructuredNull 判断通用结构树中是否包含 nil/null。
//
// value 允许标量、数组和对象；返回 true 表示 TOML 无法表达该节点。函数只遍历已经过预算检查
// 的无环结构，不需要额外循环检测。
func containsGluaStructuredNull(value any) bool {
	// 按节点类型递归查找 nil。
	switch typed := value.(type) {
	case nil:
		// nil 就是格式 null。
		return true
	case []any:
		// 数组任一元素包含 null 即返回 true。
		for _, item := range typed {
			// 递归检查数组元素。
			if containsGluaStructuredNull(item) {
				// 找到后提前结束。
				return true
			}
		}
	case map[string]any:
		// 对象任一字段包含 null 即返回 true。
		for _, item := range typed {
			// 递归检查字段值。
			if containsGluaStructuredNull(item) {
				// 找到后提前结束。
				return true
			}
		}
	}
	return false
}

// normalizeTOMLValue 把 TOML 解码节点规范为共用结构树。
//
// value 可为基础标量、数组、对象及 TOML 日期时间类型；日期时间恢复为规范字符串。返回错误表示
// 库产生了当前 GLua 映射未定义的类型。
func normalizeTOMLValue(value any) (any, error) {
	// 按 TOML v2 解码器的已知输出类型规范化。
	switch typed := value.(type) {
	case nil, bool, int, int64, uint64, float64, string:
		// 基础标量可直接进入共用转换层。
		return typed, nil
	case time.Time:
		// 带时区日期时间使用 RFC3339Nano 保留偏移和精度。
		return typed.Format(time.RFC3339Nano), nil
	case toml.LocalDate:
		// 本地日期保留 TOML 规范文本。
		return typed.String(), nil
	case toml.LocalTime:
		// 本地时间保留 TOML 规范文本。
		return typed.String(), nil
	case toml.LocalDateTime:
		// 本地日期时间保留 TOML 规范文本。
		return typed.String(), nil
	case []any:
		// 数组逐项规范化。
		result := make([]any, len(typed))
		for index, item := range typed {
			// 递归处理嵌套数组或对象。
			normalized, err := normalizeTOMLValue(item)
			if err != nil {
				// 子节点错误直接上传。
				return nil, err
			}
			result[index] = normalized
		}
		return result, nil
	case map[string]any:
		// 对象逐字段规范化。
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			// 字段值递归处理。
			normalized, err := normalizeTOMLValue(item)
			if err != nil {
				// 子字段错误直接上传。
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	default:
		// 未定义类型必须显式扩展映射后才能支持。
		return nil, fmt.Errorf("unsupported TOML value type %T", value)
	}
}
