package lua

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	// defaultGluaSerializationMaxDepth 限制单次结构遍历的最大嵌套层数。
	defaultGluaSerializationMaxDepth = 128
	// defaultGluaSerializationMaxNodes 限制单次结构遍历的最大节点数。
	defaultGluaSerializationMaxNodes = 100000
	// defaultGluaSerializationMaxInputBytes 限制单次解码输入大小。
	defaultGluaSerializationMaxInputBytes = 16 << 20
	// defaultGluaSerializationMaxOutputBytes 限制单次编码输出大小。
	defaultGluaSerializationMaxOutputBytes = 16 << 20
)

// gluaSerializationOptions 保存文本序列化 API 共用的资源和数字策略。
type gluaSerializationOptions struct {
	// maxDepth 是 table/数组/对象最大嵌套层数。
	maxDepth int
	// maxNodes 是标量和容器总节点上限。
	maxNodes int
	// maxInputBytes 是解码文本的字节上限。
	maxInputBytes int
	// maxOutputBytes 是编码文本的字节上限。
	maxOutputBytes int
	// numberMode 控制解码数字恢复为 auto、integer、number 或 string。
	numberMode string
}

// defaultGluaSerializationOptions 返回单次编解码的安全默认配置。
//
// 返回值是独立副本，调用方可按 options table 覆盖；默认值兼顾普通配置文件和拒绝异常大输入。
func defaultGluaSerializationOptions() gluaSerializationOptions {
	// 所有格式共享同一套默认资源边界。
	return gluaSerializationOptions{
		maxDepth:       defaultGluaSerializationMaxDepth,
		maxNodes:       defaultGluaSerializationMaxNodes,
		maxInputBytes:  defaultGluaSerializationMaxInputBytes,
		maxOutputBytes: defaultGluaSerializationMaxOutputBytes,
		numberMode:     "auto",
	}
}

// parseGluaSerializationOptions 解析共用 options table。
//
// value 可为 nil 或 table；apiName 用于错误消息。返回填充默认值后的配置；资源上限必须为正整数，
// numberMode 必须是 auto、integer、number 或 string，错误通过 Lua error 返回。
func parseGluaSerializationOptions(value runtime.Value, apiName string) (gluaSerializationOptions, error) {
	// 从安全默认值开始，只覆盖调用方显式字段。
	options := defaultGluaSerializationOptions()
	if value.IsNil() {
		// 未提供 options 时直接返回默认值。
		return options, nil
	}
	if value.Kind != runtime.KindTable {
		// 共用 options 必须是 table。
		return options, gluaSerializationError(apiName + " options must be table")
	}
	table, _ := value.Ref.(*runtime.Table)
	if table == nil {
		// 损坏 table 引用不能读取配置。
		return options, gluaSerializationError(apiName + " options must be valid table")
	}
	var err error
	options.maxDepth, err = gluaPositiveIntegerOption(table, "maxDepth", options.maxDepth, apiName)
	if err != nil {
		// 深度配置错误直接返回。
		return options, err
	}
	options.maxNodes, err = gluaPositiveIntegerOption(table, "maxNodes", options.maxNodes, apiName)
	if err != nil {
		// 节点配置错误直接返回。
		return options, err
	}
	options.maxInputBytes, err = gluaPositiveIntegerOption(table, "maxInputBytes", options.maxInputBytes, apiName)
	if err != nil {
		// 输入配置错误直接返回。
		return options, err
	}
	options.maxOutputBytes, err = gluaPositiveIntegerOption(table, "maxOutputBytes", options.maxOutputBytes, apiName)
	if err != nil {
		// 输出配置错误直接返回。
		return options, err
	}
	if numberMode := table.RawGetString("numberMode"); !numberMode.IsNil() {
		// numberMode 必须是受支持的字符串枚举。
		if numberMode.Kind != runtime.KindString ||
			(numberMode.String != "auto" && numberMode.String != "integer" && numberMode.String != "number" && numberMode.String != "string") {
			// 未知模式不能静默回退。
			return options, gluaSerializationError(apiName + " options.numberMode must be auto, integer, number, or string")
		}
		options.numberMode = numberMode.String
	}
	return options, nil
}

// gluaPositiveIntegerOption 读取一个可选正整数配置。
//
// table 必须非 nil；name 是字段名，fallback 是默认值。字段缺失返回 fallback，非正整数返回带
// apiName 的 Lua error，成功值必须能安全转换为当前平台 int。
func gluaPositiveIntegerOption(table *runtime.Table, name string, fallback int, apiName string) (int, error) {
	// 缺失字段使用默认值。
	value := table.RawGetString(name)
	if value.IsNil() {
		// 调用方未覆盖该限制。
		return fallback, nil
	}
	if value.Kind != runtime.KindInteger || value.Integer <= 0 || uint64(value.Integer) > uint64(^uint(0)>>1) {
		// 非正整数或超出平台 int 范围都拒绝。
		return 0, gluaSerializationError(fmt.Sprintf("%s options.%s must be a positive integer", apiName, name))
	}
	return int(value.Integer), nil
}

// checkGluaSerializationInput 检查解码输入字节上限。
//
// apiName 和 text 用于错误消息与计数；options 必须已解析。成功返回 nil，超限返回 Lua error，
// 不会截断输入或尝试部分解析。
func checkGluaSerializationInput(apiName string, text string, options gluaSerializationOptions) error {
	// 按 Lua string 的原始字节长度检查，中文和二进制内容都不按 rune 低估。
	if len(text) > options.maxInputBytes {
		// 超限输入在进入解析器前拒绝，避免先分配大型语法树。
		return gluaSerializationError(fmt.Sprintf("%s input exceeds maxInputBytes (%d)", apiName, options.maxInputBytes))
	}
	return nil
}

// checkGluaSerializationOutput 检查编码结果字节上限。
//
// apiName 和 output 用于错误消息与计数；成功返回 nil，超限返回 Lua error。输出已在内存中生成，
// 该检查保证结果不会继续暴露给 Lua；流式限制由后续 writer API 扩展。
func checkGluaSerializationOutput(apiName string, output []byte, options gluaSerializationOptions) error {
	// 输出上限按最终编码字节数判断。
	if len(output) > options.maxOutputBytes {
		// 超限结果不返回给脚本。
		return gluaSerializationError(fmt.Sprintf("%s output exceeds maxOutputBytes (%d)", apiName, options.maxOutputBytes))
	}
	return nil
}

// validateGluaSerializationValue 在编码前检查 Lua 值的深度、节点数和循环引用。
//
// value 是待编码根值；options 提供预算。返回 nil 表示预算内且无循环，错误包含路径。该检查不
// 判断数组/对象键合法性，形状错误仍由正式转换路径报告。
func validateGluaSerializationValue(value runtime.Value, options gluaSerializationOptions) error {
	// 独立递归栈和节点计数只服务本次调用，支持多个 State 并发编码。
	visiting := make(map[*runtime.Table]bool)
	nodes := 0
	rawBytes := 0
	return validateGluaSerializationValueAt(value, options, visiting, &nodes, &rawBytes, 1, "$")
}

// validateGluaSerializationValueAt 递归执行 Lua 值预算检查。
//
// visiting 保存当前 table 路径，nodes 和 rawBytes 是共享预算，depth 从 1 开始，path 用于错误定位。
// 返回错误表示超过预算、循环引用或 table 迭代失败。
func validateGluaSerializationValueAt(value runtime.Value, options gluaSerializationOptions, visiting map[*runtime.Table]bool, nodes *int, rawBytes *int, depth int, path string) error {
	// 每个标量和容器都计为一个节点。
	(*nodes)++
	if *nodes > options.maxNodes {
		// 节点超限时停止遍历。
		return fmt.Errorf("serialization exceeds maxNodes (%d) at %s", options.maxNodes, path)
	}
	if depth > options.maxDepth {
		// 深度超限时停止递归。
		return fmt.Errorf("serialization exceeds maxDepth (%d) at %s", options.maxDepth, path)
	}
	if value.Kind == runtime.KindString {
		// 编码前累计原始字符串，避免超大值先进入 Marshal 分配。
		*rawBytes += len(value.String)
		if *rawBytes > options.maxOutputBytes {
			// 原始文本已经超过输出上限，任何文本编码都不应继续。
			return fmt.Errorf("serialization raw string data exceeds maxOutputBytes (%d) at %s", options.maxOutputBytes, path)
		}
	}
	if value.Kind != runtime.KindTable || value.Ref == gluaSerializationNull {
		// 标量和 null 哨兵不再递归。
		return nil
	}
	table, _ := value.Ref.(*runtime.Table)
	if table == nil {
		// 损坏引用由正式转换报告类型错误。
		return nil
	}
	if visiting[table] {
		// 当前路径重复 table 表示循环引用。
		return fmt.Errorf("serialization detected cyclic table at %s", path)
	}
	visiting[table] = true
	defer delete(visiting, table)
	key := runtime.NilValue()
	for {
		// RawNext 遍历每个可见值，不触发元方法。
		nextKey, nextValue, ok, err := table.RawNext(key)
		if err != nil {
			// 并发修改或非法迭代状态不能继续预算检查。
			return fmt.Errorf("serialization failed to iterate %s: %w", path, err)
		}
		if !ok {
			// 当前 table 遍历完成。
			break
		}
		childPath := path + ".?"
		if nextKey.Kind == runtime.KindString {
			// 字符串键使用字段路径。
			*rawBytes += len(nextKey.String)
			if *rawBytes > options.maxOutputBytes {
				// 对象键总字节也计入编码前预算。
				return fmt.Errorf("serialization raw string data exceeds maxOutputBytes (%d) at %s", options.maxOutputBytes, path)
			}
			childPath = path + "." + nextKey.String
		} else if nextKey.Kind == runtime.KindInteger {
			// 整数键使用数组路径。
			childPath = fmt.Sprintf("%s[%d]", path, nextKey.Integer)
		}
		if err := validateGluaSerializationValueAt(nextValue, options, visiting, nodes, rawBytes, depth+1, childPath); err != nil {
			// 子节点预算错误直接上传。
			return err
		}
		key = nextKey
	}
	return nil
}

// validateGluaStructuredValue 检查解码中间树的深度和节点总数。
//
// value 必须由受控解析器产生；options 提供预算。返回错误表示超限或出现未知结构类型，确保转换
// Lua table 前不会构造超过调用方预算的数据图。
func validateGluaStructuredValue(value any, options gluaSerializationOptions) error {
	// 从根节点开始统计。
	nodes := 0
	return validateGluaStructuredValueAt(value, options, &nodes, 1, "$")
}

// validateGluaStructuredValueAt 递归检查一个中间树节点。
//
// nodes 是共享计数，depth 从 1 开始，path 用于错误定位；返回错误表示预算超限或节点类型不受控。
func validateGluaStructuredValueAt(value any, options gluaSerializationOptions, nodes *int, depth int, path string) error {
	// 当前节点先计数再检查预算。
	(*nodes)++
	if *nodes > options.maxNodes {
		// 节点总数超过限制。
		return fmt.Errorf("serialization exceeds maxNodes (%d) at %s", options.maxNodes, path)
	}
	if depth > options.maxDepth {
		// 嵌套深度超过限制。
		return fmt.Errorf("serialization exceeds maxDepth (%d) at %s", options.maxDepth, path)
	}
	switch typed := value.(type) {
	case nil, bool, int, int64, uint64, float64, json.Number, string:
		// 标量节点无需继续递归。
		return nil
	case []any:
		// 数组逐项检查。
		for index, item := range typed {
			// 子项路径使用 1-based 展示以匹配 Lua。
			if err := validateGluaStructuredValueAt(item, options, nodes, depth+1, fmt.Sprintf("%s[%d]", path, index+1)); err != nil {
				// 子项超限直接上传。
				return err
			}
		}
		return nil
	case map[string]any:
		// 对象逐字段检查。
		for key, item := range typed {
			// 子字段路径附加 key。
			if err := validateGluaStructuredValueAt(item, options, nodes, depth+1, path+"."+key); err != nil {
				// 子字段超限直接上传。
				return err
			}
		}
		return nil
	default:
		// 未知节点类型不能进入 Lua 转换。
		return fmt.Errorf("unsupported decoded value type %T at %s", value, path)
	}
}

// gluaNumberValue 按 numberMode 把中间树数字转换为 Lua 值。
//
// value 允许 int64、uint64、float64 或数字文本；options.numberMode 决定返回 integer、number 或
// string。auto 尽量保留整数；无法满足 integer 模式或超出范围时返回错误。
func gluaNumberValue(value any, options gluaSerializationOptions) (runtime.Value, error) {
	// 先规范化为文本、整数或浮点分支。
	switch typed := value.(type) {
	case int:
		// 平台 int 统一交给 int64 分支。
		return gluaNumberValue(int64(typed), options)
	case int64:
		// string 模式保留十进制文本。
		if options.numberMode == "string" {
			// 整数精确转字符串。
			return runtime.StringValue(strconv.FormatInt(typed, 10)), nil
		}
		if options.numberMode == "number" {
			// 调用方显式接受 float64 表示。
			return runtime.NumberValue(float64(typed)), nil
		}
		return runtime.IntegerValue(typed), nil
	case uint64:
		// string 模式可保留全部无符号范围。
		if options.numberMode == "string" {
			// 无符号整数精确转字符串。
			return runtime.StringValue(strconv.FormatUint(typed, 10)), nil
		}
		if typed > math.MaxInt64 {
			// integer/auto 不能表示超范围无符号整数；number 模式允许显式精度折中。
			if options.numberMode == "number" {
				// 调用方明确要求 Lua number。
				return runtime.NumberValue(float64(typed)), nil
			}
			return runtime.NilValue(), fmt.Errorf("integer %d exceeds Lua integer range", typed)
		}
		return gluaNumberValue(int64(typed), options)
	case float64:
		// 非有限浮点在所有模式下拒绝。
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			// 文本格式无法稳定表达非有限值。
			return runtime.NilValue(), fmt.Errorf("non-finite number is not supported")
		}
		if options.numberMode == "string" {
			// 使用可往返的最短浮点文本。
			return runtime.StringValue(strconv.FormatFloat(typed, 'g', -1, 64)), nil
		}
		if options.numberMode == "integer" {
			// integer 模式要求有限、无小数且处于 int64 范围。
			if typed < math.MinInt64 || typed > math.MaxInt64 || math.Trunc(typed) != typed {
				// 不能精确恢复为 Lua integer。
				return runtime.NilValue(), fmt.Errorf("number %g is not a Lua integer", typed)
			}
			return runtime.IntegerValue(int64(typed)), nil
		}
		return runtime.NumberValue(typed), nil
	case string:
		// JSON number 文本根据模式解析。
		if options.numberMode == "string" {
			// 原始数字文本直接保留。
			return runtime.StringValue(typed), nil
		}
		if options.numberMode != "number" && !containsFloatMarker(typed) {
			// auto/integer 的十进制整数必须在 int64 范围内。
			integer, err := strconv.ParseInt(typed, 10, 64)
			if err != nil {
				// 超范围整数不能静默变成 float。
				return runtime.NilValue(), fmt.Errorf("integer %q exceeds Lua integer range", typed)
			}
			return runtime.IntegerValue(integer), nil
		}
		number, err := strconv.ParseFloat(typed, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			// 非法或超范围数字文本返回错误。
			return runtime.NilValue(), fmt.Errorf("number %q exceeds Lua number range", typed)
		}
		return gluaNumberValue(number, options)
	default:
		// 调用方传入了非数字中间节点。
		return runtime.NilValue(), fmt.Errorf("unsupported number type %T", value)
	}
}

// containsFloatMarker 判断数字文本是否包含小数点或指数标记。
//
// text 是 JSON/TOML 数字原文；返回 true 表示应按浮点路径解析。
func containsFloatMarker(text string) bool {
	// 手工扫描避免为热路径引入额外正则。
	for _, character := range text {
		// 小数点和 e/E 都表示非纯十进制 integer 语法。
		if character == '.' || character == 'e' || character == 'E' {
			// 命中任一标记即可返回。
			return true
		}
	}
	return false
}
