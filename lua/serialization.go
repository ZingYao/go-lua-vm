package lua

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ZingYao/go-lua-vm/runtime"
	"gopkg.in/yaml.v3"
)

var gluaSerializationNull = func() *runtime.Table {
	// 单例空值哨兵保持引用身份稳定，并禁止 Lua 侧写入字段。
	nullTable := runtime.NewTable()
	nullTable.Freeze()
	return nullTable
}()

// gluaXMLNode 保存 XML 解码阶段的通用元素树。
type gluaXMLNode struct {
	// name 保存元素完整名称，当前 Lua 映射使用 Local 部分作为 table 键。
	name xml.Name
	// attributes 保存元素属性，解码后映射到 `_attr` table。
	attributes []xml.Attr
	// children 保存直接子元素，重复名称会恢复为数组。
	children []*gluaXMLNode
	// text 保存元素内字符数据。
	text string
}

// gluaXMLOptions 保存 XML 编解码可选行为。
type gluaXMLOptions struct {
	// serialization 保存共用资源限制和数字策略。
	serialization gluaSerializationOptions
	// root 保存编码根元素名，默认 root。
	root string
	// pretty 表示编码时是否使用两空格缩进。
	pretty bool
	// inferTypes 表示解码叶节点时是否推断 boolean 和 number。
	inferTypes bool
	// namespace 保存编码根元素使用的默认 XML namespace URI。
	namespace string
	// typed 表示是否通过 `_glua_type` 属性无损保留标量和空容器类型。
	typed bool
}

// registerGluaSerializationGlobals 注册 glua.json、glua.yaml 和 glua.xml 命名空间。
//
// state 必须已打开 base 全局环境；函数没有返回值，宿主把 glua 占用为非 table 时保持原值并
// 跳过注册。所有编解码错误通过 Lua error 返回，不静默丢弃不支持的值。
func registerGluaSerializationGlobals(state *State) {
	// 序列化扩展依赖可写全局表，关闭或未初始化 State 直接跳过。
	if state == nil || state.Globals() == nil {
		// 无效 State 没有注册目标。
		return
	}
	gluaTable := gluaNamespaceTable(state.Globals())
	if gluaTable == nil {
		// 宿主占用了非 table 的 glua 全局值，不覆盖已有语义。
		return
	}
	nullValue := runtime.ReferenceValue(runtime.KindTable, gluaSerializationNull)
	gluaTable.RawSetString("null", nullValue)
	gluaTable.RawSetString("array", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 创建或标记一个显式结构化数组。
		return markGluaStructuredTable(runtime.TableShapeArray, "glua.array", args...)
	})))
	gluaTable.RawSetString("object", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 创建或标记一个显式结构化对象。
		return markGluaStructuredTable(runtime.TableShapeObject, "glua.object", args...)
	})))

	jsonTable := runtime.NewTable()
	jsonTable.RawSetString("null", nullValue)
	jsonTable.RawSetString("encode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaJSONEncode)))
	jsonTable.RawSetString("decode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaJSONDecode)))
	gluaTable.RawSetString("json", runtime.ReferenceValue(runtime.KindTable, jsonTable))

	yamlTable := runtime.NewTable()
	yamlTable.RawSetString("null", nullValue)
	yamlTable.RawSetString("encode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaYAMLEncode)))
	yamlTable.RawSetString("decode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaYAMLDecode)))
	gluaTable.RawSetString("yaml", runtime.ReferenceValue(runtime.KindTable, yamlTable))

	xmlTable := runtime.NewTable()
	xmlTable.RawSetString("null", nullValue)
	xmlTable.RawSetString("encode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaXMLEncode)))
	xmlTable.RawSetString("decode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaXMLDecode)))
	gluaTable.RawSetString("xml", runtime.ReferenceValue(runtime.KindTable, xmlTable))

	tomlTable := runtime.NewTable()
	tomlTable.RawSetString("encode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaTOMLEncode)))
	tomlTable.RawSetString("decode", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(gluaTOMLDecode)))
	gluaTable.RawSetString("toml", runtime.ReferenceValue(runtime.KindTable, tomlTable))
}

// markGluaStructuredTable 创建或标记一个显式数组/对象 table。
//
// shape 必须是 Array 或 Object；functionName 用于错误消息。args 允许为空或只包含一个 table，
// 返回同一个 table 值；参数错误通过 Lua error 返回，业务元表和字段保持不变。
func markGluaStructuredTable(shape runtime.TableShape, functionName string, args ...runtime.Value) ([]runtime.Value, error) {
	// 只允许零参数新建或单 table 参数原地标记。
	if len(args) > 1 || (len(args) == 1 && args[0].Kind != runtime.KindTable) {
		// 其他参数形态不能形成结构化 table。
		return nil, gluaSerializationError(functionName + " expects optional table")
	}
	var table *runtime.Table
	var result runtime.Value
	if len(args) == 0 {
		// 无参数时创建空 table。
		table = runtime.NewTable()
		result = runtime.ReferenceValue(runtime.KindTable, table)
	} else {
		// 单参数时保留原 table 引用。
		table, _ = args[0].Ref.(*runtime.Table)
		result = args[0]
	}
	if table == nil {
		// 损坏的 table 引用不能标记。
		return nil, gluaSerializationError(functionName + " expects valid table")
	}
	table.SetStructuredShape(shape)
	return []runtime.Value{result}, nil
}

// gluaJSONEncode 把 Lua 值编码为 JSON 文本。
//
// args 必须包含一个值，可选第二个 boolean 控制两空格格式化；返回单个 JSON string。循环 table、
// 混合键、非字符串对象键、函数、线程和 userdata 会返回 Lua error。
func gluaJSONEncode(args ...runtime.Value) ([]runtime.Value, error) {
	// JSON 编码只接受 value 和可选 pretty。
	if len(args) < 1 || len(args) > 2 {
		// 参数数量错误时返回稳定的 Lua 侧诊断。
		return nil, gluaSerializationError("glua.json.encode expects value and optional pretty")
	}
	pretty := false
	options := defaultGluaSerializationOptions()
	if len(args) == 2 {
		// 兼容原 boolean pretty 参数，同时允许 options table。
		if args[1].Kind == runtime.KindBoolean {
			// boolean 只控制格式化，其他限制使用默认值。
			pretty = args[1].Bool
		} else if args[1].Kind == runtime.KindTable {
			// table 同时提供 pretty 和共用资源限制。
			var err error
			options, err = parseGluaSerializationOptions(args[1], "glua.json.encode")
			if err != nil {
				// 共用 options 错误直接返回。
				return nil, err
			}
			optionsTable, _ := args[1].Ref.(*runtime.Table)
			prettyValue := optionsTable.RawGetString("pretty")
			if !prettyValue.IsNil() {
				// pretty 字段必须是 boolean。
				if prettyValue.Kind != runtime.KindBoolean {
					// 非 boolean 格式化字段返回错误。
					return nil, gluaSerializationError("glua.json.encode options.pretty must be boolean")
				}
				pretty = prettyValue.Bool
			}
		} else {
			// 其他类型既不是兼容 pretty 也不是 options。
			return nil, gluaSerializationError("bad argument #2 to 'glua.json.encode' (boolean or table expected)")
		}
	}
	if err := validateGluaSerializationValue(args[0], options); err != nil {
		// 在正式转换前执行深度、节点和循环预算检查。
		return nil, gluaSerializationError("glua.json.encode: " + err.Error())
	}
	nativeValue, err := gluaValueToStructured(args[0], make(map[*runtime.Table]bool), "$")
	if err != nil {
		// Lua 值转换失败时保留具体路径。
		return nil, gluaSerializationError(err.Error())
	}
	var encoded []byte
	if pretty {
		// 格式化模式使用稳定的两空格缩进。
		encoded, err = json.MarshalIndent(nativeValue, "", "  ")
	} else {
		// 默认输出紧凑 JSON。
		encoded, err = json.Marshal(nativeValue)
	}
	if err != nil {
		// 标准库编码错误转为 Lua error。
		return nil, gluaSerializationError("glua.json.encode: " + err.Error())
	}
	if err := checkGluaSerializationOutput("glua.json.encode", encoded, options); err != nil {
		// 超限结果不返回给 Lua。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(string(encoded))}, nil
}

// gluaJSONDecode 把单个 JSON 文本解码为 Lua 值。
//
// args 必须只有一个 string；返回 table 或标量。JSON null 使用只读 `glua.null` 哨兵，整数尽量
// 保留为 Lua integer；语法错误、尾随非空白内容和超范围数字返回 Lua error。
func gluaJSONDecode(args ...runtime.Value) ([]runtime.Value, error) {
	// JSON 解码严格要求一个字符串参数。
	if len(args) < 1 || len(args) > 2 || args[0].Kind != runtime.KindString {
		// 参数形态错误直接拒绝。
		return nil, gluaSerializationError("glua.json.decode expects string and optional options")
	}
	optionValue := runtime.NilValue()
	if len(args) == 2 {
		// 第二参数交给共用 options 解析。
		optionValue = args[1]
	}
	options, err := parseGluaSerializationOptions(optionValue, "glua.json.decode")
	if err != nil {
		// options 错误直接返回。
		return nil, err
	}
	if err := checkGluaSerializationInput("glua.json.decode", args[0].String, options); err != nil {
		// 超限输入不进入 JSON parser。
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(args[0].String))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		// 首个 JSON 值解析失败时返回原始语法位置。
		return nil, gluaSerializationError("glua.json.decode: " + err.Error())
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		// 第二个值或非法尾随内容不属于单文档 JSON。
		if err == nil {
			// 成功解析第二个值说明输入包含多个 JSON 文档。
			return nil, gluaSerializationError("glua.json.decode: multiple JSON values")
		}
		return nil, gluaSerializationError("glua.json.decode: " + err.Error())
	}
	if err := validateGluaStructuredValue(decoded, options); err != nil {
		// 解析后的树必须满足深度和节点预算。
		return nil, gluaSerializationError("glua.json.decode: " + err.Error())
	}
	value, err := gluaStructuredToValueWithOptions(decoded, options)
	if err != nil {
		// 数字范围或结构类型不受支持时返回明确错误。
		return nil, gluaSerializationError("glua.json.decode: " + err.Error())
	}
	return []runtime.Value{value}, nil
}

// gluaYAMLEncode 把 Lua 值编码为单文档 YAML 文本。
//
// args 必须只有一个值；返回带结尾换行的 YAML string。table 规则与 JSON 一致，不支持循环引用、
// 混合键和 Lua 可调用对象，错误通过 Lua error 返回。
func gluaYAMLEncode(args ...runtime.Value) ([]runtime.Value, error) {
	// YAML 编码只接收一个待编码值。
	if len(args) < 1 || len(args) > 2 {
		// YAML 允许 value 和可选 options。
		return nil, gluaSerializationError("glua.yaml.encode expects value and optional options")
	}
	optionValue := runtime.NilValue()
	if len(args) == 2 {
		// 第二参数提供共用资源限制。
		optionValue = args[1]
	}
	options, err := parseGluaSerializationOptions(optionValue, "glua.yaml.encode")
	if err != nil {
		// options 错误直接返回。
		return nil, err
	}
	if err := validateGluaSerializationValue(args[0], options); err != nil {
		// 在 YAML 转换前检查结构预算。
		return nil, gluaSerializationError("glua.yaml.encode: " + err.Error())
	}
	nativeValue, err := gluaValueToStructured(args[0], make(map[*runtime.Table]bool), "$")
	if err != nil {
		// 结构转换错误保留 table 路径。
		return nil, gluaSerializationError(err.Error())
	}
	encoded, err := yaml.Marshal(nativeValue)
	if err != nil {
		// YAML 库错误统一转成 Lua error。
		return nil, gluaSerializationError("glua.yaml.encode: " + err.Error())
	}
	if err := checkGluaSerializationOutput("glua.yaml.encode", encoded, options); err != nil {
		// 超限 YAML 不返回给 Lua。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(string(encoded))}, nil
}

// gluaYAMLDecode 把单文档 YAML 文本解码为 Lua 值。
//
// args 必须只有一个 string；返回 table 或标量，YAML null 使用 `glua.null`。只允许字符串 map key，
// alias 循环、多个文档和无法表示的时间戳等值返回 Lua error。
func gluaYAMLDecode(args ...runtime.Value) ([]runtime.Value, error) {
	// YAML 解码严格要求一个字符串。
	if len(args) < 1 || len(args) > 2 || args[0].Kind != runtime.KindString {
		// 参数错误不进入 YAML 解析器。
		return nil, gluaSerializationError("glua.yaml.decode expects string and optional options")
	}
	optionValue := runtime.NilValue()
	if len(args) == 2 {
		// 第二参数提供共用限制和数字策略。
		optionValue = args[1]
	}
	options, err := parseGluaSerializationOptions(optionValue, "glua.yaml.decode")
	if err != nil {
		// options 错误直接返回。
		return nil, err
	}
	if err := checkGluaSerializationInput("glua.yaml.decode", args[0].String, options); err != nil {
		// 超限输入不进入 YAML parser。
		return nil, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(args[0].String))
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		// 首个 YAML 文档语法错误直接返回。
		return nil, gluaSerializationError("glua.yaml.decode: " + err.Error())
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		// YAML API 只接受单文档，避免静默忽略后续文档。
		if err == nil {
			// 成功解析第二个文档时返回明确错误。
			return nil, gluaSerializationError("glua.yaml.decode: multiple YAML documents")
		}
		return nil, gluaSerializationError("glua.yaml.decode: " + err.Error())
	}
	normalized, err := normalizeYAMLValue(decoded, make(map[any]bool))
	if err != nil {
		// YAML 特有 map key 或 alias 循环错误转成 Lua error。
		return nil, gluaSerializationError("glua.yaml.decode: " + err.Error())
	}
	if err := validateGluaStructuredValue(normalized, options); err != nil {
		// 中间树必须满足深度和节点预算。
		return nil, gluaSerializationError("glua.yaml.decode: " + err.Error())
	}
	value, err := gluaStructuredToValueWithOptions(normalized, options)
	if err != nil {
		// 结构转换失败时返回明确类型错误。
		return nil, gluaSerializationError("glua.yaml.decode: " + err.Error())
	}
	return []runtime.Value{value}, nil
}

// gluaXMLEncode 把 Lua 值编码为 XML 文本。
//
// args 包含一个值和可选 options table；options.root 默认 root，options.pretty 控制缩进。对象键映射
// 为元素，数组映射为 item，`_attr` 映射属性，`_text` 映射文本；非法 XML 名称和循环 table 返回错误。
func gluaXMLEncode(args ...runtime.Value) ([]runtime.Value, error) {
	// XML 编码接受 value 和可选配置表。
	if len(args) < 1 || len(args) > 2 {
		// 参数数量错误时不进行结构转换。
		return nil, gluaSerializationError("glua.xml.encode expects value and optional options")
	}
	options, err := parseGluaXMLOptions(args, false)
	if err != nil {
		// 配置类型或字段错误直接返回。
		return nil, err
	}
	if err := validateGluaSerializationValue(args[0], options.serialization); err != nil {
		// XML 转换前检查深度、节点和循环预算。
		return nil, gluaSerializationError("glua.xml.encode: " + err.Error())
	}
	nativeValue, err := gluaValueToStructured(args[0], make(map[*runtime.Table]bool), "$")
	if err != nil {
		// table 结构错误保留路径。
		return nil, gluaSerializationError(err.Error())
	}
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	if options.pretty {
		// pretty 使用两空格层级缩进。
		encoder.Indent("", "  ")
	}
	if err := encodeGluaXMLValueWithOptions(encoder, xml.Name{Space: options.namespace, Local: options.root}, nativeValue, options); err != nil {
		// XML token 构造或名称错误通过 Lua error 返回。
		return nil, gluaSerializationError("glua.xml.encode: " + err.Error())
	}
	if err := encoder.Flush(); err != nil {
		// 写入缓冲区失败时返回编码错误。
		return nil, gluaSerializationError("glua.xml.encode: " + err.Error())
	}
	if err := checkGluaSerializationOutput("glua.xml.encode", output.Bytes(), options.serialization); err != nil {
		// 超限 XML 不返回给 Lua。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(output.String())}, nil
}

// gluaXMLDecode 把单根 XML 文本解码为 Lua 值。
//
// args 包含一个 XML string 和可选 options table；options.inferTypes 默认 true。属性写入 `_attr`，
// 文本写入 `_text`，纯 item 子节点恢复数组，重复同名节点恢复数组；格式错误返回 Lua error。
func gluaXMLDecode(args ...runtime.Value) ([]runtime.Value, error) {
	// XML 解码至少需要一个字符串。
	if len(args) < 1 || len(args) > 2 || args[0].Kind != runtime.KindString {
		// 参数错误不进入 token 解析器。
		return nil, gluaSerializationError("glua.xml.decode expects string and optional options")
	}
	options, err := parseGluaXMLOptions(args, true)
	if err != nil {
		// 配置错误按 Lua error 返回。
		return nil, err
	}
	if err := checkGluaSerializationInput("glua.xml.decode", args[0].String, options.serialization); err != nil {
		// 超限输入不进入 XML parser。
		return nil, err
	}
	decoder := xml.NewDecoder(strings.NewReader(args[0].String))
	root, err := decodeGluaXMLDocument(decoder)
	if err != nil {
		// XML 语法、多个根节点和尾随内容错误都不能忽略。
		return nil, gluaSerializationError("glua.xml.decode: " + err.Error())
	}
	nativeValue, err := gluaXMLNodeValue(root, options.inferTypes, options.typed)
	if err != nil {
		// XML 映射冲突返回明确错误。
		return nil, gluaSerializationError("glua.xml.decode: " + err.Error())
	}
	if err := validateGluaStructuredValue(nativeValue, options.serialization); err != nil {
		// XML 映射树必须满足资源预算。
		return nil, gluaSerializationError("glua.xml.decode: " + err.Error())
	}
	value, err := gluaStructuredToValueWithOptions(nativeValue, options.serialization)
	if err != nil {
		// 结构到 Lua table 的转换错误继续按 XML API 报告。
		return nil, gluaSerializationError("glua.xml.decode: " + err.Error())
	}
	return []runtime.Value{value}, nil
}

// gluaValueToStructured 把 Lua 值转换为 JSON/YAML/XML 共用的 Go 结构树。
//
// value 可为 nil、boolean、integer、有限 number、string、glua.null 或 table；visiting 记录当前递归
// 路径以拒绝循环，path 用于错误定位。返回值只包含 nil、标量、[]any 和 map[string]any。
func gluaValueToStructured(value runtime.Value, visiting map[*runtime.Table]bool, path string) (any, error) {
	// 按 Lua 基础类型转换，不触发元方法。
	switch value.Kind {
	case runtime.KindNil:
		// 顶层 Lua nil 与 glua.null 都编码为格式空值。
		return nil, nil
	case runtime.KindBoolean:
		// boolean 可被三种格式直接表示。
		return value.Bool, nil
	case runtime.KindInteger:
		// integer 保持 int64 精度。
		return value.Integer, nil
	case runtime.KindNumber:
		// JSON 和 YAML 不接受 NaN/Inf 的跨格式稳定表示。
		if math.IsNaN(value.Number) || math.IsInf(value.Number, 0) {
			// 非有限数值会导致各格式行为不一致，因此明确拒绝。
			return nil, fmt.Errorf("serialization does not support non-finite number at %s", path)
		}
		return value.Number, nil
	case runtime.KindString:
		// Lua string 按 UTF-8 文本交给具体编码器。
		return value.String, nil
	case runtime.KindTable:
		// table 需要区分 null 哨兵、数组和对象。
		table, ok := value.Ref.(*runtime.Table)
		if !ok || table == nil {
			// 损坏 table 引用不能编码。
			return nil, fmt.Errorf("serialization encountered invalid table at %s", path)
		}
		if table == gluaSerializationNull {
			// 只读哨兵在目标格式中恢复为 null。
			return nil, nil
		}
		return gluaTableToStructured(table, visiting, path)
	default:
		// closure、userdata 和 thread 没有通用数据表示。
		return nil, fmt.Errorf("serialization does not support %s at %s", runtime.LuaTypeName(value), path)
	}
}

// gluaTableToStructured 把 Lua table 转换为连续数组或字符串键对象。
//
// table 必须非 nil；空 table 默认对象，连续 1..n integer key 转数组，纯 string key 转对象。
// 混合键、稀疏数组、其他键类型和递归环返回带路径错误。
func gluaTableToStructured(table *runtime.Table, visiting map[*runtime.Table]bool, path string) (any, error) {
	// 当前递归栈已包含 table 时说明存在循环引用。
	if visiting[table] {
		// 循环结构无法映射到树形文本格式。
		return nil, fmt.Errorf("serialization detected cyclic table at %s", path)
	}
	visiting[table] = true
	defer delete(visiting, table)

	integerValues := make(map[int64]runtime.Value)
	objectValues := make(map[string]runtime.Value)
	hasIntegerKeys := false
	hasStringKeys := false
	currentKey := runtime.NilValue()
	for {
		// RawNext 只读取 table 自身字段，不触发 __pairs 或 __index。
		nextKey, nextValue, ok, err := table.RawNext(currentKey)
		if err != nil {
			// 非法迭代状态说明 table 在转换期间被并发修改。
			return nil, fmt.Errorf("serialization failed to iterate %s: %w", path, err)
		}
		if !ok {
			// table 遍历完成后进入形状判断。
			break
		}
		switch nextKey.Kind {
		case runtime.KindInteger:
			// 数组键必须是正整数。
			if nextKey.Integer <= 0 {
				// 零和负整数不能映射为数组索引。
				return nil, fmt.Errorf("serialization requires positive array index at %s", path)
			}
			hasIntegerKeys = true
			integerValues[nextKey.Integer] = nextValue
		case runtime.KindString:
			// 字符串键参与对象映射。
			hasStringKeys = true
			objectValues[nextKey.String] = nextValue
		default:
			// boolean、number 和引用键无法形成稳定文本对象键。
			return nil, fmt.Errorf("serialization requires string object keys at %s", path)
		}
		currentKey = nextKey
	}
	if hasIntegerKeys && hasStringKeys {
		// 同时存在数组键与对象键时拒绝猜测目标形状。
		return nil, fmt.Errorf("serialization does not support mixed table keys at %s", path)
	}
	shape := table.StructuredShape()
	if shape == runtime.TableShapeArray && hasStringKeys {
		// 显式数组不能包含对象字段。
		return nil, fmt.Errorf("serialization array contains string keys at %s", path)
	}
	if shape == runtime.TableShapeObject && hasIntegerKeys {
		// 显式对象不能包含数组索引。
		return nil, fmt.Errorf("serialization object contains integer keys at %s", path)
	}
	if shape == runtime.TableShapeArray && !hasIntegerKeys {
		// 空显式数组必须编码为 []，不能回退空对象。
		return []any{}, nil
	}
	if hasIntegerKeys {
		// 连续 1..n 才是可序列化数组。
		arrayValues := make([]any, len(integerValues))
		for index := 1; index <= len(integerValues); index++ {
			luaValue, ok := integerValues[int64(index)]
			if !ok {
				// 缺少中间索引表示稀疏数组，不能静默补 null。
				return nil, fmt.Errorf("serialization does not support sparse array at %s", path)
			}
			converted, err := gluaValueToStructured(luaValue, visiting, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				// 子元素错误保留其精确路径。
				return nil, err
			}
			arrayValues[index-1] = converted
		}
		return arrayValues, nil
	}
	// 空 table 与纯字符串键 table 都按对象编码。
	object := make(map[string]any, len(objectValues))
	for key, luaValue := range objectValues {
		// 每个对象值递归转换并追加字段路径。
		converted, err := gluaValueToStructured(luaValue, visiting, path+"."+key)
		if err != nil {
			// 子字段错误直接上传。
			return nil, err
		}
		object[key] = converted
	}
	return object, nil
}

// gluaStructuredToValue 把通用 Go 结构树转换为 Lua 值。
//
// value 只允许 nil、boolean、整数、有限浮点、json.Number、string、[]any 与 string-key map；
// nil 转为 glua.null，数组从索引 1 写入，错误表示解码器产生了无法映射的类型。
func gluaStructuredToValue(value any) (runtime.Value, error) {
	// 旧内部入口使用默认数字策略，保持已有调用行为。
	return gluaStructuredToValueWithOptions(value, defaultGluaSerializationOptions())
}

// gluaStructuredToValueWithOptions 按指定数字策略把通用 Go 结构树转换为 Lua 值。
//
// value 只允许受控标量、[]any 与 map[string]any；options.numberMode 控制数字类型。nil 转为
// glua.null，数组和对象会设置显式 TableShape；不支持类型或数字溢出返回错误。
func gluaStructuredToValueWithOptions(value any, options gluaSerializationOptions) (runtime.Value, error) {
	// 按中间树节点类型构造 Lua 值。
	switch typed := value.(type) {
	case nil:
		// 格式 null 使用稳定只读哨兵，避免 table 字段被 Lua nil 删除。
		return runtime.ReferenceValue(runtime.KindTable, gluaSerializationNull), nil
	case bool:
		// boolean 直接映射。
		return runtime.BooleanValue(typed), nil
	case int:
		// YAML 平台 int 交给共用数字策略。
		return gluaNumberValue(typed, options)
	case int64:
		// int64 交给共用数字策略。
		return gluaNumberValue(typed, options)
	case uint64:
		// 无符号整数交给共用范围和模式处理。
		return gluaNumberValue(typed, options)
	case float64:
		// 浮点交给共用有限值和模式处理。
		return gluaNumberValue(typed, options)
	case json.Number:
		// JSON number 原文交给共用数字策略，string 模式可保留精确文本。
		return gluaNumberValue(string(typed), options)
	case string:
		// 文本直接映射为 Lua string。
		return runtime.StringValue(typed), nil
	case []any:
		// 数组按 Lua 1-based index 构造 table。
		table := runtime.NewTable()
		table.SetStructuredShape(runtime.TableShapeArray)
		for index, item := range typed {
			// 每个元素递归转换；null 哨兵可占据数组槽位。
			converted, err := gluaStructuredToValueWithOptions(item, options)
			if err != nil {
				// 子元素错误直接上传。
				return runtime.NilValue(), err
			}
			table.RawSetInteger(int64(index+1), converted)
		}
		return runtime.ReferenceValue(runtime.KindTable, table), nil
	case map[string]any:
		// 对象按字符串 key 构造 Lua table。
		table := runtime.NewTable()
		table.SetStructuredShape(runtime.TableShapeObject)
		for key, item := range typed {
			// 每个字段递归转换。
			converted, err := gluaStructuredToValueWithOptions(item, options)
			if err != nil {
				// 子字段错误直接上传。
				return runtime.NilValue(), err
			}
			table.RawSetString(key, converted)
		}
		return runtime.ReferenceValue(runtime.KindTable, table), nil
	default:
		// 任何其他 Go 类型都不属于受控中间树。
		return runtime.NilValue(), fmt.Errorf("unsupported decoded value type %T", value)
	}
}

// normalizeYAMLValue 把 YAML 解码器产生的 map[interface{}]interface{} 等节点规范为共用结构树。
//
// value 可为 YAML 标量、sequence 或 map；visiting 防止 alias 形成递归容器。仅字符串 map key 被接受，
// 时间值和自定义 tagged Go 类型返回错误，避免产生格式间不一致。
func normalizeYAMLValue(value any, visiting map[any]bool) (any, error) {
	// YAML v3 默认会生成以下标量和容器类型。
	switch typed := value.(type) {
	case nil, bool, int, int64, uint64, float64, string:
		// 共用转换层可直接处理标准标量。
		return typed, nil
	case []any:
		// sequence 逐项规范化。
		result := make([]any, len(typed))
		for index, item := range typed {
			// 递归规范化嵌套节点。
			normalized, err := normalizeYAMLValue(item, visiting)
			if err != nil {
				// 子节点错误直接上传。
				return nil, err
			}
			result[index] = normalized
		}
		return result, nil
	case map[string]any:
		// 已经是字符串键 map 时复制并递归规范化值。
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			// 字段值递归规范化。
			normalized, err := normalizeYAMLValue(item, visiting)
			if err != nil {
				// 子字段错误直接上传。
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	case map[any]any:
		// YAML 允许非字符串 key，但 glua table 对象映射明确拒绝它们。
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			// 每个 key 必须是 string。
			stringKey, ok := key.(string)
			if !ok {
				// 非字符串键没有稳定 JSON/XML 对应语义。
				return nil, fmt.Errorf("mapping key must be string, got %T", key)
			}
			normalized, err := normalizeYAMLValue(item, visiting)
			if err != nil {
				// 子字段错误直接上传。
				return nil, err
			}
			result[stringKey] = normalized
		}
		return result, nil
	default:
		// 时间戳等隐式类型不自动字符串化，避免数据类型悄然变化。
		return nil, fmt.Errorf("unsupported YAML value type %T", value)
	}
}

// parseGluaXMLOptions 解析 XML 编解码 options table。
//
// args 已通过调用入口完成数量检查；decode 为 true 时读取 inferTypes，编码时读取 root/pretty。
// 返回默认 root、紧凑编码和启用类型推断；字段类型错误返回 Lua error。
func parseGluaXMLOptions(args []runtime.Value, decode bool) (gluaXMLOptions, error) {
	// 默认根名和类型推断保持简单调用可用。
	options := gluaXMLOptions{root: "root", inferTypes: true, serialization: defaultGluaSerializationOptions()}
	if len(args) < 2 {
		// 没有 options 时直接使用默认值。
		return options, nil
	}
	if args[1].Kind != runtime.KindTable {
		// 第二参数必须是 table。
		return options, gluaSerializationError("bad argument #2 to 'glua.xml' (table expected)")
	}
	optionsTable, _ := args[1].Ref.(*runtime.Table)
	if optionsTable == nil {
		// 损坏 table 引用不能读取配置。
		return options, gluaSerializationError("bad argument #2 to 'glua.xml' (table expected)")
	}
	var err error
	options.serialization, err = parseGluaSerializationOptions(args[1], "glua.xml")
	if err != nil {
		// 共用资源或数字配置错误直接返回。
		return options, err
	}
	typed := optionsTable.RawGetString("typed")
	if !typed.IsNil() {
		// typed 必须是 boolean，编码和解码共用。
		if typed.Kind != runtime.KindBoolean {
			// 非 boolean 类型标记配置返回错误。
			return options, gluaSerializationError("glua.xml options.typed must be boolean")
		}
		options.typed = typed.Bool
	}
	if decode {
		// 解码仅使用 inferTypes，root 对解析没有约束作用。
		inferTypes := optionsTable.RawGetString("inferTypes")
		if !inferTypes.IsNil() {
			// inferTypes 必须是 boolean。
			if inferTypes.Kind != runtime.KindBoolean {
				// 非 boolean 配置返回错误。
				return options, gluaSerializationError("glua.xml options.inferTypes must be boolean")
			}
			options.inferTypes = inferTypes.Bool
		}
		return options, nil
	}
	namespace := optionsTable.RawGetString("namespace")
	if !namespace.IsNil() {
		// namespace 必须是 URI string；空字符串表示无 namespace。
		if namespace.Kind != runtime.KindString {
			// 非字符串 namespace 返回错误。
			return options, gluaSerializationError("glua.xml options.namespace must be string")
		}
		options.namespace = namespace.String
	}
	root := optionsTable.RawGetString("root")
	if !root.IsNil() {
		// root 必须是非空字符串且符合简化 XML 名称规则。
		if root.Kind != runtime.KindString || !validGluaXMLName(root.String) {
			// 非法根名可能生成不可解析 XML，提前拒绝。
			return options, gluaSerializationError("glua.xml options.root must be a valid XML element name")
		}
		options.root = root.String
	}
	pretty := optionsTable.RawGetString("pretty")
	if !pretty.IsNil() {
		// pretty 必须是 boolean。
		if pretty.Kind != runtime.KindBoolean {
			// 非 boolean 格式化配置返回错误。
			return options, gluaSerializationError("glua.xml options.pretty must be boolean")
		}
		options.pretty = pretty.Bool
	}
	return options, nil
}

// validGluaXMLName 判断字符串是否可直接用作无命名空间 XML 元素或属性名。
//
// name 必须非空，首字符允许字母或下划线，后续允许字母、数字、点、连字符和下划线；返回 true
// 表示当前映射无需转义名称。该规则是 XML Name 的保守 ASCII 子集。
func validGluaXMLName(name string) bool {
	// 空名称不能形成 XML token。
	if name == "" {
		// 明确拒绝空字符串。
		return false
	}
	for index, character := range name {
		// 首字符规则比后续字符更严格。
		if index == 0 {
			// ASCII 字母和下划线可作为首字符。
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_') {
				// 其他首字符拒绝。
				return false
			}
			continue
		}
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.') {
			// 不在保守 ASCII Name 子集内的字符拒绝。
			return false
		}
	}
	return true
}

// encodeGluaXMLValue 把一个结构树节点写成指定名称的 XML 元素。
//
// encoder 和 name 必须有效；value 可为标量、数组或对象。对象的 `_attr` 必须是标量 map，`_text`
// 必须是标量；其他字段名必须符合 XML 名称规则。返回错误表示结构无法映射或 token 写入失败。
func encodeGluaXMLValue(encoder *xml.Encoder, name xml.Name, value any) error {
	// 旧内部入口使用默认 XML options，保持现有测试与调用行为。
	return encodeGluaXMLValueWithOptions(encoder, name, value, gluaXMLOptions{inferTypes: true, serialization: defaultGluaSerializationOptions()})
}

// encodeGluaXMLValueWithOptions 按 XML options 写入指定名称的结构树节点。
//
// options 控制 namespace、CDATA 保留字段和 `_glua_type` 类型标记；其他映射规则与
// encodeGluaXMLValue 相同，返回错误表示名称、保留字段或 token 写入失败。
func encodeGluaXMLValueWithOptions(encoder *xml.Encoder, name xml.Name, value any, options gluaXMLOptions) error {
	// 元素名称在写入前验证，避免 encoding/xml 生成异常 token。
	if encoder == nil || !validGluaXMLName(name.Local) {
		// nil encoder 或非法名称不能继续。
		return fmt.Errorf("invalid XML element name %q", name.Local)
	}
	start := xml.StartElement{Name: name}
	object, isObject := value.(map[string]any)
	if isObject {
		// `_namespace` 可为当前元素覆盖继承的 namespace URI。
		if rawNamespace, ok := object["_namespace"]; ok {
			// namespace 必须是字符串。
			namespace, ok := rawNamespace.(string)
			if !ok {
				// 非字符串 namespace 无法写入 xml.Name.Space。
				return fmt.Errorf("%s._namespace must be a string", name.Local)
			}
			start.Name.Space = namespace
			name.Space = namespace
		}
		// `_attr` 字段在开始 token 前提取为属性。
		if rawAttributes, ok := object["_attr"]; ok {
			// 属性容器必须是字符串键对象。
			attributes, ok := rawAttributes.(map[string]any)
			if !ok {
				// 其他形态无法映射属性。
				return fmt.Errorf("%s._attr must be an object", name.Local)
			}
			attributeNames := make([]string, 0, len(attributes))
			for attributeName := range attributes {
				// 先收集属性名，排序后保证 XML 输出可复现。
				attributeNames = append(attributeNames, attributeName)
			}
			sort.Strings(attributeNames)
			for _, attributeName := range attributeNames {
				// 属性名和标量值都必须可表示。
				attributeValue := attributes[attributeName]
				if !validGluaXMLName(attributeName) {
					// 非法属性名拒绝编码。
					return fmt.Errorf("invalid XML attribute name %q", attributeName)
				}
				text, err := gluaXMLScalarText(attributeValue)
				if err != nil {
					// table 或数组不能作为属性值。
					return fmt.Errorf("attribute %s: %w", attributeName, err)
				}
				start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: attributeName}, Value: text})
			}
		}
	}
	if options.typed {
		// 类型标记使用保留属性，解决空元素、数字文本和 null 的歧义。
		for _, attribute := range start.Attr {
			// 用户属性不能覆盖保留类型字段。
			if attribute.Name.Local == "_glua_type" {
				// 冲突时明确报错。
				return fmt.Errorf("%s._attr._glua_type is reserved when typed=true", name.Local)
			}
		}
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "_glua_type"}, Value: gluaXMLStructuredType(value)})
	}
	if err := encoder.EncodeToken(start); err != nil {
		// 开始 token 写入失败直接返回。
		return err
	}
	if err := encodeGluaXMLContent(encoder, name, value, options); err != nil {
		// 内容映射失败时停止编码。
		return err
	}
	if err := encoder.EncodeToken(xml.EndElement{Name: name}); err != nil {
		// 结束 token 写入失败直接返回。
		return err
	}
	return nil
}

// encodeGluaXMLContent 写入 XML 元素内部文本和子元素。
//
// value 可为标量、数组或对象；数组子元素统一命名 item，对象跳过 `_attr` 并把 `_text` 写为文本。
// 返回错误表示字段名非法、保留字段类型错误或编码器写入失败。
func encodeGluaXMLContent(encoder *xml.Encoder, parent xml.Name, value any, options gluaXMLOptions) error {
	// 按结构节点类型写内容。
	switch typed := value.(type) {
	case map[string]any:
		if _, hasText := typed["_text"]; hasText {
			// `_text` 与 `_cdata` 同时存在时语义冲突。
			if _, hasCDATA := typed["_cdata"]; hasCDATA {
				// 调用方必须二选一。
				return fmt.Errorf("%s cannot contain both _text and _cdata", parent.Local)
			}
		}
		// `_text` 先于普通子元素写入。
		if rawText, ok := typed["_text"]; ok {
			// 文本字段必须是标量。
			text, err := gluaXMLScalarText(rawText)
			if err != nil {
				// 复杂值不能写为字符数据。
				return fmt.Errorf("%s._text: %w", parent.Local, err)
			}
			if err := encoder.EncodeToken(xml.CharData([]byte(text))); err != nil {
				// 字符数据写入失败直接返回。
				return err
			}
		}
		if rawCDATA, ok := typed["_cdata"]; ok {
			// CDATA 必须是字符串，且内容不能关闭 CDATA section。
			cdata, ok := rawCDATA.(string)
			if !ok {
				// 非字符串 CDATA 拒绝。
				return fmt.Errorf("%s._cdata must be a string", parent.Local)
			}
			if strings.Contains(cdata, "]]>") {
				// 结束标记会破坏 XML 文档。
				return fmt.Errorf("%s._cdata must not contain ]]>", parent.Local)
			}
			if err := encoder.EncodeToken(xml.Directive([]byte("[CDATA[" + cdata + "]]"))); err != nil {
				// Directive 写入失败直接返回。
				return err
			}
		}
		fieldNames := make([]string, 0, len(typed))
		for fieldName := range typed {
			// 保留字段不进入普通子元素排序列表。
			if fieldName == "_attr" || fieldName == "_text" || fieldName == "_cdata" || fieldName == "_namespace" {
				// 保留字段已在当前元素层处理。
				continue
			}
			fieldNames = append(fieldNames, fieldName)
		}
		sort.Strings(fieldNames)
		for _, fieldName := range fieldNames {
			// 保留字段不再生成子元素。
			childValue := typed[fieldName]
			if !validGluaXMLName(fieldName) {
				// 对象键必须能直接作为 XML 元素名。
				return fmt.Errorf("invalid XML element name %q", fieldName)
			}
			if err := encodeGluaXMLValueWithOptions(encoder, xml.Name{Space: parent.Space, Local: fieldName}, childValue, options); err != nil {
				// 子元素编码错误直接上传。
				return err
			}
		}
		return nil
	case []any:
		// 数组元素统一使用 item 名称。
		for _, item := range typed {
			// 每个 item 保持原值递归结构。
			if err := encodeGluaXMLValueWithOptions(encoder, xml.Name{Space: parent.Space, Local: "item"}, item, options); err != nil {
				// 子项编码错误直接上传。
				return err
			}
		}
		return nil
	default:
		// 标量直接写字符数据。
		text, err := gluaXMLScalarText(value)
		if err != nil {
			// 未知复杂类型拒绝编码。
			return err
		}
		if text == "" {
			// 空字符串和 null 使用空元素表示。
			return nil
		}
		return encoder.EncodeToken(xml.CharData([]byte(text)))
	}
}

// gluaXMLStructuredType 返回中间树节点的稳定 XML 类型标记。
//
// value 允许受控结构树节点；返回 null、boolean、integer、number、string、array 或 object，未知
// 类型返回 string 作为防御默认值，正式编码仍会在内容阶段拒绝复杂未知值。
func gluaXMLStructuredType(value any) string {
	// 按 Go 中间节点类型映射。
	switch value.(type) {
	case nil:
		// nil 映射 null。
		return "null"
	case bool:
		// boolean 保留布尔类型。
		return "boolean"
	case int, int64, uint64:
		// 整数类型统一标记 integer。
		return "integer"
	case float64:
		// float64 标记 number。
		return "number"
	case []any:
		// sequence 标记 array。
		return "array"
	case map[string]any:
		// mapping 标记 object。
		return "object"
	default:
		// 普通文本和未知标量按 string。
		return "string"
	}
}

// gluaXMLScalarText 把结构树标量转换为 XML 字符数据。
//
// value 允许 nil、boolean、整数、float64 和 string；返回文本及 nil error。nil 与空字符串都返回
// 空文本，复杂值返回错误；XML 本身无法区分这两种值，文档需提示该限制。
func gluaXMLScalarText(value any) (string, error) {
	// 按标量类型生成稳定文本。
	switch typed := value.(type) {
	case nil:
		// XML 空元素表示 null/空文本的共同形态。
		return "", nil
	case bool:
		// boolean 使用小写标准文本。
		return strconv.FormatBool(typed), nil
	case int:
		// 平台 int 使用十进制。
		return strconv.Itoa(typed), nil
	case int64:
		// Lua integer 使用十进制且不丢精度。
		return strconv.FormatInt(typed, 10), nil
	case uint64:
		// YAML 无符号整数使用十进制。
		return strconv.FormatUint(typed, 10), nil
	case float64:
		// 浮点使用最短可往返表示。
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	case string:
		// 字符串原样交给 encoding/xml 转义。
		return typed, nil
	default:
		// 数组和对象不能直接成为文本或属性。
		return "", fmt.Errorf("XML scalar expected, got %T", value)
	}
}

// decodeGluaXMLDocument 读取恰好一个 XML 根元素。
//
// decoder 必须非 nil；返回根节点。声明、指令和外围空白会忽略，多个根元素、根外非空字符和空文档
// 返回错误，保证输入不会被部分消费后静默成功。
func decodeGluaXMLDocument(decoder *xml.Decoder) (*gluaXMLNode, error) {
	// 逐 token 查找唯一根元素。
	var root *gluaXMLNode
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			// 输入结束后校验是否读取到根。
			break
		}
		if err != nil {
			// XML 语法错误直接返回。
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			// 只允许一个根元素。
			if root != nil {
				// 第二个根元素违反 XML 单根约束。
				return nil, fmt.Errorf("multiple root elements")
			}
			root, err = decodeGluaXMLNode(decoder, typed)
			if err != nil {
				// 子树解析错误直接返回。
				return nil, err
			}
		case xml.CharData:
			// 根元素外只允许空白字符。
			if strings.TrimSpace(string(typed)) != "" {
				// 非空字符数据意味着无根文本或尾随垃圾。
				return nil, fmt.Errorf("character data outside root element")
			}
		default:
			// 注释、指令和 directive 不影响数据映射。
		}
	}
	if root == nil {
		// 空文档没有可解码值。
		return nil, fmt.Errorf("empty XML document")
	}
	return root, nil
}

// decodeGluaXMLNode 递归读取一个已经开始的 XML 元素。
//
// decoder 和 start 必须有效；返回包含属性、文本和子元素的节点。遇到不匹配结束元素或语法错误
// 返回错误，注释与处理指令忽略。
func decodeGluaXMLNode(decoder *xml.Decoder, start xml.StartElement) (*gluaXMLNode, error) {
	// 复制属性切片，避免后续 token 缓冲复用影响节点。
	node := &gluaXMLNode{name: start.Name, attributes: append([]xml.Attr(nil), start.Attr...)}
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			// EOF 在元素未闭合时也是语法错误。
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			// 子元素递归解析并保持出现顺序。
			child, err := decodeGluaXMLNode(decoder, typed)
			if err != nil {
				// 子树错误直接上传。
				return nil, err
			}
			node.children = append(node.children, child)
		case xml.EndElement:
			// encoding/xml 已验证嵌套，这里确认当前元素结束。
			if typed.Name != start.Name {
				// 名称不匹配时返回明确错误。
				return nil, fmt.Errorf("unexpected closing element %s", typed.Name.Local)
			}
			node.text = text.String()
			return node, nil
		case xml.CharData:
			// 保留元素内文本，映射阶段再决定是否 trim 和推断类型。
			text.Write([]byte(typed))
		default:
			// 注释和指令不进入 Lua 数据。
		}
	}
}

// gluaXMLNodeValue 把 XML 元素节点映射为共用结构树。
//
// node 必须非 nil；inferTypes 控制无类型标记文本的推断，typed 表示识别 `_glua_type`。纯 item
// 子元素映射数组，namespace 写入 `_namespace`，属性和混合文本写入 `_attr`/`_text`。
func gluaXMLNodeValue(node *gluaXMLNode, inferTypes bool, typed bool) (any, error) {
	// nil 节点表示内部解析错误。
	if node == nil {
		// 不生成无来源的空值。
		return nil, fmt.Errorf("invalid XML node")
	}
	trimmedText := strings.TrimSpace(node.text)
	typeMarker := ""
	visibleAttributes := make([]xml.Attr, 0, len(node.attributes))
	for _, attribute := range node.attributes {
		// namespace 声明由 xml.Name.Space 表达，不作为业务 `_attr` 暴露。
		if attribute.Name.Space == "xmlns" || (attribute.Name.Space == "" && attribute.Name.Local == "xmlns") {
			// 跳过默认或带前缀的 xmlns 声明。
			continue
		}
		// typed 模式把保留属性用于类型恢复，不暴露到 `_attr`。
		if typed && attribute.Name.Local == "_glua_type" {
			// 重复类型属性由 XML parser 或后续覆盖语义拒绝。
			if typeMarker != "" {
				// 同一元素只能有一个类型标记。
				return nil, fmt.Errorf("duplicate _glua_type attribute")
			}
			typeMarker = attribute.Value
			continue
		}
		visibleAttributes = append(visibleAttributes, attribute)
	}
	if typed && typeMarker != "" && typeMarker != "array" && typeMarker != "object" {
		// 标量类型标记必须出现在叶节点。
		if len(node.children) > 0 || len(visibleAttributes) > 0 {
			// 标量不能同时承载子元素或业务属性。
			return nil, fmt.Errorf("typed scalar element must not contain child elements or attributes")
		}
		return gluaXMLTypedTextValue(trimmedText, typeMarker)
	}
	if len(node.children) == 0 && len(visibleAttributes) == 0 && node.name.Space == "" && typeMarker == "" {
		// 无属性叶节点直接返回文本标量。
		return gluaXMLTextValue(trimmedText, inferTypes), nil
	}
	allItems := len(node.children) > 0
	for _, child := range node.children {
		// 只有全部名为 item 才恢复为数组。
		if child.name.Local != "item" {
			// 任一普通名称都会切换为对象映射。
			allItems = false
			break
		}
	}
	if typeMarker == "array" && !allItems && len(node.children) > 0 {
		// 显式数组只允许 item 子元素。
		return nil, fmt.Errorf("typed array element may only contain item children")
	}
	if (allItems && typeMarker != "object" && len(visibleAttributes) == 0 && trimmedText == "") || typeMarker == "array" {
		// 纯 item 容器恢复数组。
		items := make([]any, 0, len(node.children))
		for _, child := range node.children {
			// 每个 item 递归转换。
			value, err := gluaXMLNodeValue(child, inferTypes, typed)
			if err != nil {
				// 子项错误直接上传。
				return nil, err
			}
			items = append(items, value)
		}
		return items, nil
	}
	result := make(map[string]any)
	if node.name.Space != "" {
		// namespace URI 使用保留字段保留，便于再次编码。
		result["_namespace"] = node.name.Space
	}
	if len(visibleAttributes) > 0 {
		// 属性恢复到 `_attr` 对象，并按 inferTypes 处理值。
		attributes := make(map[string]any, len(visibleAttributes))
		for _, attribute := range visibleAttributes {
			// 同名属性由 XML 解析器保证合法，Local 作为 Lua key。
			attributes[attribute.Name.Local] = gluaXMLTextValue(attribute.Value, inferTypes)
		}
		result["_attr"] = attributes
	}
	if trimmedText != "" {
		// 包含子元素或属性时文本写入 `_text`。
		result["_text"] = gluaXMLTextValue(trimmedText, inferTypes)
	}
	for _, child := range node.children {
		// 子元素按名称聚合，重复名称恢复为数组。
		childValue, err := gluaXMLNodeValue(child, inferTypes, typed)
		if err != nil {
			// 子节点错误直接上传。
			return nil, err
		}
		existing, exists := result[child.name.Local]
		if !exists {
			// 首个同名子节点直接保存标量或对象。
			result[child.name.Local] = childValue
			continue
		}
		if list, ok := existing.([]any); ok {
			// 已经聚合为数组时继续追加。
			result[child.name.Local] = append(list, childValue)
		} else {
			// 第二个同名子节点把前值升级为数组。
			result[child.name.Local] = []any{existing, childValue}
		}
	}
	return result, nil
}

// gluaXMLTypedTextValue 按 `_glua_type` 恢复叶节点标量。
//
// text 是元素文本，typeMarker 支持 null、boolean、integer、number、string；返回精确中间值。
// 未知类型、非法数字或非法布尔文本返回错误。
func gluaXMLTypedTextValue(text string, typeMarker string) (any, error) {
	// 类型标记优先于 inferTypes，保证可逆。
	switch typeMarker {
	case "null":
		// null 必须使用空元素。
		if text != "" {
			// 非空 null 表达冲突。
			return nil, fmt.Errorf("typed null element must be empty")
		}
		return nil, nil
	case "boolean":
		// boolean 只接受标准 true/false。
		if text == "true" {
			// 恢复 true。
			return true, nil
		}
		if text == "false" {
			// 恢复 false。
			return false, nil
		}
		return nil, fmt.Errorf("invalid typed boolean %q", text)
	case "integer":
		// integer 必须处于 int64 范围。
		integer, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			// 非法或超范围整数返回错误。
			return nil, fmt.Errorf("invalid typed integer %q", text)
		}
		return integer, nil
	case "number":
		// number 必须是有限 float64。
		number, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			// 非法或非有限数字返回错误。
			return nil, fmt.Errorf("invalid typed number %q", text)
		}
		return number, nil
	case "string":
		// string 原样保留文本。
		return text, nil
	default:
		// array/object 在容器路径处理，其他值未知。
		return nil, fmt.Errorf("unsupported _glua_type %q", typeMarker)
	}
}

// gluaXMLTextValue 按 XML 解码选项把文本转换为标量。
//
// text 是已经按调用点决定是否 trim 的字符数据；inferTypes 为 false 时始终返回 string，为 true
// 时依次识别 true/false、十进制 integer 和有限 float，其他内容保持 string。
func gluaXMLTextValue(text string, inferTypes bool) any {
	// 禁用推断时完整保留文本类型。
	if !inferTypes {
		// 调用方明确要求所有叶节点为字符串。
		return text
	}
	if text == "true" {
		// 标准小写 true 恢复 boolean。
		return true
	}
	if text == "false" {
		// 标准小写 false 恢复 boolean。
		return false
	}
	if integer, err := strconv.ParseInt(text, 10, 64); err == nil && text != "" {
		// 完整十进制文本且处于 int64 范围时恢复 integer。
		return integer
	}
	if number, err := strconv.ParseFloat(text, 64); err == nil && text != "" && !math.IsNaN(number) && !math.IsInf(number, 0) {
		// 其余有限数值恢复 float number。
		return number
	}
	return text
}

// gluaSerializationError 把序列化错误消息包装成 Lua 可捕获错误。
//
// message 应包含 API 名或结构路径；返回 error 可由 pcall/xpcall 捕获，错误对象为 Lua string。
func gluaSerializationError(message string) error {
	// 所有序列化入口统一使用字符串错误对象。
	return runtime.RaiseError(runtime.StringValue(message))
}
