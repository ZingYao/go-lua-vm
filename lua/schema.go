package lua

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"unicode/utf8"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// gluaSchemaFailure 保存一次数据或 schema 校验失败。
type gluaSchemaFailure struct {
	// path 是失败数据的 `$` 起始路径。
	path string
	// message 是不含路径前缀的失败原因。
	message string
	// invalidSchema 表示错误来自 schema 定义，而不是业务值不匹配。
	invalidSchema bool
}

// gluaSchemaContext 保存单次递归校验的预算和循环检测状态。
type gluaSchemaContext struct {
	// nodes 是已经检查的数据节点数。
	nodes int
	// maxNodes 是固定安全节点上限。
	maxNodes int
	// maxDepth 是固定安全嵌套上限。
	maxDepth int
	// visiting 保存当前递归路径上的 value/schema table 对。
	visiting map[gluaSchemaVisit]bool
}

// gluaSchemaVisit 标识一个数据 table 与 schema table 组合。
type gluaSchemaVisit struct {
	// value 是当前被校验 table。
	value *runtime.Table
	// schema 是应用到该值的 schema table。
	schema *runtime.Table
}

// gluaSchemaValidate 校验 Lua 值是否满足轻量 schema。
//
// args 必须为 value 和 schema table；成功返回 true，数据不匹配返回 false、message、path，schema
// 本身非法或超过安全预算时抛出 Lua error。
func gluaSchemaValidate(args ...runtime.Value) ([]runtime.Value, error) {
	// validate 必须接收值和 schema table。
	if len(args) != 2 || args[1].Kind != runtime.KindTable {
		// schema 类型错误属于 API 使用错误。
		return nil, gluaSerializationError("glua.schema.validate expects value and schema table")
	}
	schema, _ := args[1].Ref.(*runtime.Table)
	if schema == nil {
		// 损坏 schema 引用不能校验。
		return nil, gluaSerializationError("glua.schema.validate expects valid schema table")
	}
	failure := validateGluaSchema(args[0], schema)
	if failure == nil {
		// 完整满足 schema 时只返回 true。
		return []runtime.Value{runtime.BooleanValue(true)}, nil
	}
	if failure.invalidSchema {
		// schema 定义错误不能伪装成业务校验失败。
		return nil, gluaSerializationError(fmt.Sprintf("invalid schema at %s: %s", failure.path, failure.message))
	}
	return []runtime.Value{
		runtime.BooleanValue(false),
		runtime.StringValue(failure.message),
		runtime.StringValue(failure.path),
	}, nil
}

// gluaSchemaAssert 断言 Lua 值满足轻量 schema。
//
// args 必须为 value 和 schema table；成功返回原 value，失败抛出带数据路径的 Lua error，便于在
// 配置加载入口直接中止执行。
func gluaSchemaAssert(args ...runtime.Value) ([]runtime.Value, error) {
	// assert 与 validate 使用相同参数形态。
	if len(args) != 2 || args[1].Kind != runtime.KindTable {
		// schema 类型错误直接返回。
		return nil, gluaSerializationError("glua.schema.assert expects value and schema table")
	}
	schema, _ := args[1].Ref.(*runtime.Table)
	if schema == nil {
		// 损坏 schema 引用不能校验。
		return nil, gluaSerializationError("glua.schema.assert expects valid schema table")
	}
	failure := validateGluaSchema(args[0], schema)
	if failure != nil {
		// schema 错误与数据错误都通过 assert 抛出，但保留不同前缀。
		prefix := "schema validation failed"
		if failure.invalidSchema {
			// 定义错误使用 invalid schema 前缀。
			prefix = "invalid schema"
		}
		return nil, gluaSerializationError(fmt.Sprintf("%s at %s: %s", prefix, failure.path, failure.message))
	}
	return []runtime.Value{args[0]}, nil
}

// validateGluaSchema 使用固定安全预算校验值。
//
// value 是任意 Lua 值，schema 必须非 nil；返回 nil 表示成功，否则返回首个确定性失败。
func validateGluaSchema(value runtime.Value, schema *runtime.Table) *gluaSchemaFailure {
	// 每次调用使用独立上下文，避免并发 State 共享计数。
	context := &gluaSchemaContext{
		maxNodes: 100000,
		maxDepth: 128,
		visiting: make(map[gluaSchemaVisit]bool),
	}
	return context.validate(value, schema, "$", 1)
}

// validate 递归校验一个值和 schema。
//
// value 和 schema 是当前节点，path 是数据路径，depth 从 1 开始；返回首个失败或 nil。
func (context *gluaSchemaContext) validate(value runtime.Value, schema *runtime.Table, path string, depth int) *gluaSchemaFailure {
	// 先执行固定资源预算，防止恶意 schema/value 产生深递归。
	context.nodes++
	if context.nodes > context.maxNodes {
		// 节点超限属于 schema 执行错误。
		return &gluaSchemaFailure{path: path, message: "validation exceeds maxNodes", invalidSchema: true}
	}
	if depth > context.maxDepth {
		// 深度超限属于 schema 执行错误。
		return &gluaSchemaFailure{path: path, message: "validation exceeds maxDepth", invalidSchema: true}
	}
	if nullable := schema.RawGetString("nullable"); !nullable.IsNil() {
		// nullable 必须是 boolean。
		if nullable.Kind != runtime.KindBoolean {
			// 字段类型错误属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "nullable must be boolean", invalidSchema: true}
		}
		if nullable.Bool && (value.IsNil() || gluaValueIsNull(value)) {
			// nullable 值提前通过其他约束。
			return nil
		}
	}
	if failure := validateGluaSchemaType(value, schema, path); failure != nil {
		// 类型不匹配或 type 定义错误直接返回。
		return failure
	}
	if failure := validateGluaSchemaEnum(value, schema, path); failure != nil {
		// enum 不匹配或定义错误直接返回。
		return failure
	}
	switch value.Kind {
	case runtime.KindString:
		// 字符串应用长度和 pattern 约束。
		return validateGluaSchemaString(value.String, schema, path)
	case runtime.KindInteger, runtime.KindNumber:
		// 数字应用 minimum/maximum 约束。
		return validateGluaSchemaNumber(value, schema, path)
	case runtime.KindTable:
		// null 哨兵没有 table 子结构约束。
		if gluaValueIsNull(value) {
			// null 类型已经在 type/nullable 阶段处理。
			return nil
		}
		table, _ := value.Ref.(*runtime.Table)
		if table == nil {
			// 损坏 table 值按数据失败报告。
			return &gluaSchemaFailure{path: path, message: "invalid table value"}
		}
		visit := gluaSchemaVisit{value: table, schema: schema}
		if context.visiting[visit] {
			// 同一 value/schema 对再次出现表示递归环。
			return &gluaSchemaFailure{path: path, message: "cyclic value is not supported"}
		}
		context.visiting[visit] = true
		defer delete(context.visiting, visit)
		shape, shapeFailure := gluaSchemaTableShape(table, path)
		if shapeFailure != nil {
			// 混合或稀疏形状不能继续校验。
			return shapeFailure
		}
		if shape == runtime.TableShapeArray {
			// 数组应用 items 和数量约束。
			return context.validateArray(table, schema, path, depth)
		}
		// 对象应用 properties、required 和 additionalProperties。
		return context.validateObject(table, schema, path, depth)
	default:
		// 其他标量没有附加约束。
		return nil
	}
}

// validateGluaSchemaType 校验 schema.type。
//
// type 缺失或 any 表示不限制；支持 nil、null、boolean、integer、number、string、table、array、
// object、function。返回 nil 表示匹配，非法类型名属于 schema 错误。
func validateGluaSchemaType(value runtime.Value, schema *runtime.Table, path string) *gluaSchemaFailure {
	// 缺失 type 默认 any。
	typeValue := schema.RawGetString("type")
	if typeValue.IsNil() {
		// 不限制基础类型。
		return nil
	}
	if typeValue.Kind != runtime.KindString {
		// type 字段必须是字符串。
		return &gluaSchemaFailure{path: path, message: "type must be string", invalidSchema: true}
	}
	typeName := typeValue.String
	matched := false
	switch typeName {
	case "any":
		// any 接受所有值。
		matched = true
	case "nil":
		// nil 只接受 Lua nil，不接受 glua.null。
		matched = value.IsNil()
	case "null":
		// null 只接受 glua.null。
		matched = gluaValueIsNull(value)
	case "boolean":
		// boolean 接受 true/false。
		matched = value.Kind == runtime.KindBoolean
	case "integer":
		// integer 不接受浮点 number，即使数值无小数。
		matched = value.Kind == runtime.KindInteger
	case "number":
		// number 接受 Lua integer 和 float。
		matched = value.Kind == runtime.KindInteger || value.Kind == runtime.KindNumber
	case "string":
		// string 接受任意二进制 Lua string。
		matched = value.Kind == runtime.KindString
	case "table":
		// table 不把 glua.null 视为业务 table。
		matched = value.Kind == runtime.KindTable && !gluaValueIsNull(value)
	case "array", "object":
		// 数组/对象需要进一步判断 table 形状。
		if value.Kind == runtime.KindTable && !gluaValueIsNull(value) {
			// 仅有效 table 引用可判断形状。
			table, _ := value.Ref.(*runtime.Table)
			shape, failure := gluaSchemaTableShape(table, path)
			if failure != nil {
				// 形状错误就是类型不匹配原因。
				return failure
			}
			matched = (typeName == "array" && shape == runtime.TableShapeArray) ||
				(typeName == "object" && shape == runtime.TableShapeObject)
		}
	case "function":
		// function 接受 Lua 和 Go closure。
		matched = value.Kind == runtime.KindLuaClosure || value.Kind == runtime.KindGoClosure
	default:
		// 未知 type 名属于非法 schema。
		return &gluaSchemaFailure{path: path, message: "unsupported type " + typeName, invalidSchema: true}
	}
	if !matched {
		// 数据类型不符合 schema。
		return &gluaSchemaFailure{path: path, message: "expected " + typeName + ", got " + gluaSchemaValueType(value)}
	}
	return nil
}

// validateGluaSchemaEnum 校验可选 enum table。
//
// enum 缺失表示不限制；存在时必须是非空 table，使用 Lua RawEqual 比较候选值。返回 nil 表示匹配。
func validateGluaSchemaEnum(value runtime.Value, schema *runtime.Table, path string) *gluaSchemaFailure {
	// 缺失 enum 不限制值。
	enumValue := schema.RawGetString("enum")
	if enumValue.IsNil() {
		// 跳过枚举检查。
		return nil
	}
	if enumValue.Kind != runtime.KindTable {
		// enum 必须是 table。
		return &gluaSchemaFailure{path: path, message: "enum must be table", invalidSchema: true}
	}
	enumTable, _ := enumValue.Ref.(*runtime.Table)
	if enumTable == nil {
		// 损坏 enum 引用属于非法 schema。
		return &gluaSchemaFailure{path: path, message: "enum must be valid table", invalidSchema: true}
	}
	key := runtime.NilValue()
	count := 0
	for {
		// enum 使用 value 部分，支持数组和映射写法。
		nextKey, candidate, ok, err := enumTable.RawNext(key)
		if err != nil {
			// 迭代失败属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "enum iteration failed", invalidSchema: true}
		}
		if !ok {
			// 枚举遍历完成。
			break
		}
		count++
		if value.RawEqual(candidate) {
			// 命中任一候选即通过。
			return nil
		}
		key = nextKey
	}
	if count == 0 {
		// 空 enum 永远失败且通常是定义错误。
		return &gluaSchemaFailure{path: path, message: "enum must not be empty", invalidSchema: true}
	}
	return &gluaSchemaFailure{path: path, message: "value is not in enum"}
}

// validateGluaSchemaString 校验字符串长度和 RE2 pattern。
//
// text 是业务字符串，schema 可包含 minLength、maxLength、pattern；长度按 Unicode code point 计算。
func validateGluaSchemaString(text string, schema *runtime.Table, path string) *gluaSchemaFailure {
	// 长度按 rune 计数，非法 UTF-8 字节按 RuneError 单元计数。
	length := utf8.RuneCountInString(text)
	minimum, present, failure := gluaSchemaNonNegativeInteger(schema, "minLength", path)
	if failure != nil {
		// 定义错误直接返回。
		return failure
	}
	if present && length < minimum {
		// 字符串短于下限。
		return &gluaSchemaFailure{path: path, message: fmt.Sprintf("string length must be at least %d", minimum)}
	}
	maximum, present, failure := gluaSchemaNonNegativeInteger(schema, "maxLength", path)
	if failure != nil {
		// 定义错误直接返回。
		return failure
	}
	if present && length > maximum {
		// 字符串长于上限。
		return &gluaSchemaFailure{path: path, message: fmt.Sprintf("string length must be at most %d", maximum)}
	}
	pattern := schema.RawGetString("pattern")
	if !pattern.IsNil() {
		// pattern 必须是合法 RE2 string。
		if pattern.Kind != runtime.KindString {
			// 非 string pattern 属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "pattern must be string", invalidSchema: true}
		}
		expression, err := regexp.Compile(pattern.String)
		if err != nil {
			// 非法 RE2 属于 schema 错误。
			return &gluaSchemaFailure{path: path, message: "invalid pattern: " + err.Error(), invalidSchema: true}
		}
		if !expression.MatchString(text) {
			// 文本不匹配 pattern。
			return &gluaSchemaFailure{path: path, message: "string does not match pattern"}
		}
	}
	return nil
}

// validateGluaSchemaNumber 校验 minimum 和 maximum。
//
// value 必须是 integer 或 number；schema 边界也必须是 number。比较使用 float64，int64 对接近边界
// 的精确性由 Lua 数字双模型约束。
func validateGluaSchemaNumber(value runtime.Value, schema *runtime.Table, path string) *gluaSchemaFailure {
	// 统一转换为 float64 进行跨 integer/number 比较。
	number, _ := value.ToNumber()
	minimum := schema.RawGetString("minimum")
	if !minimum.IsNil() {
		// minimum 必须是 Lua number。
		minimumNumber, ok := minimum.ToNumber()
		if !ok || math.IsNaN(minimumNumber) || math.IsInf(minimumNumber, 0) {
			// 非数字或 NaN 边界属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "minimum must be a finite number", invalidSchema: true}
		}
		if number < minimumNumber {
			// 数据小于下限。
			return &gluaSchemaFailure{path: path, message: fmt.Sprintf("number must be at least %g", minimumNumber)}
		}
	}
	maximum := schema.RawGetString("maximum")
	if !maximum.IsNil() {
		// maximum 必须是 Lua number。
		maximumNumber, ok := maximum.ToNumber()
		if !ok || math.IsNaN(maximumNumber) || math.IsInf(maximumNumber, 0) {
			// 非数字或 NaN 边界属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "maximum must be a finite number", invalidSchema: true}
		}
		if number > maximumNumber {
			// 数据大于上限。
			return &gluaSchemaFailure{path: path, message: fmt.Sprintf("number must be at most %g", maximumNumber)}
		}
	}
	return nil
}

// validateArray 校验数组数量和 items schema。
//
// table 已确认是连续数组；schema 可包含 minItems、maxItems 和 items table。返回首个失败或 nil。
func (context *gluaSchemaContext) validateArray(table *runtime.Table, schema *runtime.Table, path string, depth int) *gluaSchemaFailure {
	// 数组长度使用稳定 Lua 边界。
	length := int(table.Len())
	minimum, present, failure := gluaSchemaNonNegativeInteger(schema, "minItems", path)
	if failure != nil {
		// 定义错误直接返回。
		return failure
	}
	if present && length < minimum {
		// 数组短于下限。
		return &gluaSchemaFailure{path: path, message: fmt.Sprintf("array length must be at least %d", minimum)}
	}
	maximum, present, failure := gluaSchemaNonNegativeInteger(schema, "maxItems", path)
	if failure != nil {
		// 定义错误直接返回。
		return failure
	}
	if present && length > maximum {
		// 数组长于上限。
		return &gluaSchemaFailure{path: path, message: fmt.Sprintf("array length must be at most %d", maximum)}
	}
	itemsValue := schema.RawGetString("items")
	if itemsValue.IsNil() {
		// 未定义 items 时不校验元素。
		return nil
	}
	if itemsValue.Kind != runtime.KindTable {
		// items 必须是 schema table。
		return &gluaSchemaFailure{path: path, message: "items must be schema table", invalidSchema: true}
	}
	itemsSchema, _ := itemsValue.Ref.(*runtime.Table)
	if itemsSchema == nil {
		// 损坏 items 引用属于非法 schema。
		return &gluaSchemaFailure{path: path, message: "items must be valid schema table", invalidSchema: true}
	}
	for index := 1; index <= length; index++ {
		// 每个连续元素应用同一 items schema。
		if failure := context.validate(table.RawGetInteger(int64(index)), itemsSchema, fmt.Sprintf("%s[%d]", path, index), depth+1); failure != nil {
			// 首个元素失败直接返回。
			return failure
		}
	}
	return nil
}

// validateObject 校验 required、properties 和 additionalProperties。
//
// table 已确认是字符串键对象；schema 可定义 required 数组、properties 对象和 boolean
// additionalProperties。返回首个稳定键顺序失败或 nil。
func (context *gluaSchemaContext) validateObject(table *runtime.Table, schema *runtime.Table, path string, depth int) *gluaSchemaFailure {
	required, failure := gluaSchemaRequired(schema, path)
	if failure != nil {
		// required 定义错误直接返回。
		return failure
	}
	for _, key := range required {
		// nil 表示字段缺失；glua.null 仍算已提供。
		if table.RawGetString(key).IsNil() {
			// 缺少必填字段返回数据失败。
			return &gluaSchemaFailure{path: path + "." + key, message: "required property is missing"}
		}
	}
	propertiesValue := schema.RawGetString("properties")
	var properties *runtime.Table
	if !propertiesValue.IsNil() {
		// properties 必须是 table。
		if propertiesValue.Kind != runtime.KindTable {
			// 非 table 属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "properties must be table", invalidSchema: true}
		}
		properties, _ = propertiesValue.Ref.(*runtime.Table)
		if properties == nil {
			// 损坏 properties 引用属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "properties must be valid table", invalidSchema: true}
		}
	}
	additionalAllowed := true
	if additional := schema.RawGetString("additionalProperties"); !additional.IsNil() {
		// additionalProperties 必须是 boolean。
		if additional.Kind != runtime.KindBoolean {
			// 其他类型属于非法 schema。
			return &gluaSchemaFailure{path: path, message: "additionalProperties must be boolean", invalidSchema: true}
		}
		additionalAllowed = additional.Bool
	}
	keys, failure := gluaSchemaObjectKeys(table, path)
	if failure != nil {
		// 对象键错误直接返回。
		return failure
	}
	for _, key := range keys {
		// 按排序键应用 property schema 或额外字段策略。
		propertySchemaValue := runtime.NilValue()
		if properties != nil {
			// 从 properties 查找同名 schema。
			propertySchemaValue = properties.RawGetString(key)
		}
		if propertySchemaValue.IsNil() {
			// 未定义字段按 additionalProperties 处理。
			if !additionalAllowed {
				// 禁止额外字段时返回数据失败。
				return &gluaSchemaFailure{path: path + "." + key, message: "additional property is not allowed"}
			}
			continue
		}
		if propertySchemaValue.Kind != runtime.KindTable {
			// property 定义必须是 schema table。
			return &gluaSchemaFailure{path: path + "." + key, message: "property schema must be table", invalidSchema: true}
		}
		propertySchema, _ := propertySchemaValue.Ref.(*runtime.Table)
		if propertySchema == nil {
			// 损坏 property schema 属于定义错误。
			return &gluaSchemaFailure{path: path + "." + key, message: "property schema must be valid table", invalidSchema: true}
		}
		if failure := context.validate(table.RawGetString(key), propertySchema, path+"."+key, depth+1); failure != nil {
			// 首个字段失败直接返回。
			return failure
		}
	}
	return nil
}

// gluaSchemaTableShape 判断 table 是数组还是对象。
//
// table 可带显式 TableShape；自动模式下连续正整数键是数组，字符串键是对象，空 table 是对象。
// 混合、稀疏和其他键返回数据失败。
func gluaSchemaTableShape(table *runtime.Table, path string) (runtime.TableShape, *gluaSchemaFailure) {
	// nil table 无法判断形状。
	if table == nil {
		// 损坏值按数据失败。
		return runtime.TableShapeAuto, &gluaSchemaFailure{path: path, message: "invalid table value"}
	}
	hasInteger := false
	hasString := false
	integerKeys := make(map[int64]bool)
	key := runtime.NilValue()
	for {
		// RawNext 收集键形状。
		nextKey, _, ok, err := table.RawNext(key)
		if err != nil {
			// 迭代失败按数据失败。
			return runtime.TableShapeAuto, &gluaSchemaFailure{path: path, message: "table iteration failed"}
		}
		if !ok {
			// 键遍历完成。
			break
		}
		switch nextKey.Kind {
		case runtime.KindInteger:
			// 数组索引必须为正整数。
			if nextKey.Integer <= 0 {
				// 非正整数键不属于支持的结构对象。
				return runtime.TableShapeAuto, &gluaSchemaFailure{path: path, message: "array index must be positive"}
			}
			hasInteger = true
			integerKeys[nextKey.Integer] = true
		case runtime.KindString:
			// 字符串键表示对象字段。
			hasString = true
		default:
			// 其他键没有结构化 schema 语义。
			return runtime.TableShapeAuto, &gluaSchemaFailure{path: path, message: "object keys must be strings"}
		}
		key = nextKey
	}
	if hasInteger && hasString {
		// 混合键无法同时是数组和对象。
		return runtime.TableShapeAuto, &gluaSchemaFailure{path: path, message: "mixed table keys are not supported"}
	}
	shape := table.StructuredShape()
	if shape == runtime.TableShapeArray && hasString {
		// 显式数组包含对象键时失败。
		return shape, &gluaSchemaFailure{path: path, message: "array contains string keys"}
	}
	if shape == runtime.TableShapeObject && hasInteger {
		// 显式对象包含数组键时失败。
		return shape, &gluaSchemaFailure{path: path, message: "object contains integer keys"}
	}
	if hasInteger || shape == runtime.TableShapeArray {
		// 数组键必须连续 1..n。
		for index := 1; index <= len(integerKeys); index++ {
			// 缺少任一索引表示稀疏数组。
			if !integerKeys[int64(index)] {
				// 稀疏数组不受轻量 schema 支持。
				return runtime.TableShapeArray, &gluaSchemaFailure{path: path, message: "sparse arrays are not supported"}
			}
		}
		return runtime.TableShapeArray, nil
	}
	return runtime.TableShapeObject, nil
}

// gluaSchemaRequired 解析 required 字符串数组。
//
// schema 可不含 required；存在时必须是连续字符串数组，返回原顺序列表。定义错误返回 failure。
func gluaSchemaRequired(schema *runtime.Table, path string) ([]string, *gluaSchemaFailure) {
	// 缺失 required 返回空列表。
	value := schema.RawGetString("required")
	if value.IsNil() {
		// 没有必填字段。
		return nil, nil
	}
	if value.Kind != runtime.KindTable {
		// required 必须是 table。
		return nil, &gluaSchemaFailure{path: path, message: "required must be string array", invalidSchema: true}
	}
	table, _ := value.Ref.(*runtime.Table)
	shape, failure := gluaSchemaTableShape(table, path)
	if failure != nil || shape != runtime.TableShapeArray {
		// required 形状必须是数组。
		return nil, &gluaSchemaFailure{path: path, message: "required must be string array", invalidSchema: true}
	}
	length := int(table.Len())
	result := make([]string, 0, length)
	seen := make(map[string]bool, length)
	for index := 1; index <= length; index++ {
		// 每个 required 元素必须是非空唯一字符串。
		item := table.RawGetInteger(int64(index))
		if item.Kind != runtime.KindString || item.String == "" || seen[item.String] {
			// 空值、重复值或非字符串属于非法 schema。
			return nil, &gluaSchemaFailure{path: path, message: "required entries must be unique non-empty strings", invalidSchema: true}
		}
		seen[item.String] = true
		result = append(result, item.String)
	}
	return result, nil
}

// gluaSchemaObjectKeys 返回对象的稳定排序字符串键。
//
// table 必须是对象；返回按字典序排序的键。非法键或迭代失败返回数据 failure。
func gluaSchemaObjectKeys(table *runtime.Table, path string) ([]string, *gluaSchemaFailure) {
	// 收集对象字符串键。
	keys := make([]string, 0)
	key := runtime.NilValue()
	for {
		// RawNext 遍历所有字段。
		nextKey, _, ok, err := table.RawNext(key)
		if err != nil {
			// 迭代失败返回数据错误。
			return nil, &gluaSchemaFailure{path: path, message: "table iteration failed"}
		}
		if !ok {
			// 收集完成。
			break
		}
		if nextKey.Kind != runtime.KindString {
			// 对象键必须是字符串。
			return nil, &gluaSchemaFailure{path: path, message: "object keys must be strings"}
		}
		keys = append(keys, nextKey.String)
		key = nextKey
	}
	sort.Strings(keys)
	return keys, nil
}

// gluaSchemaNonNegativeInteger 读取可选非负整数约束。
//
// schema 和 name 指定字段；返回值、是否存在和 failure。非 integer 或负数属于非法 schema。
func gluaSchemaNonNegativeInteger(schema *runtime.Table, name string, path string) (int, bool, *gluaSchemaFailure) {
	// 缺失字段表示不限制。
	value := schema.RawGetString(name)
	if value.IsNil() {
		// 返回不存在。
		return 0, false, nil
	}
	if value.Kind != runtime.KindInteger || value.Integer < 0 || uint64(value.Integer) > uint64(^uint(0)>>1) {
		// 非负平台 int 是唯一合法形态。
		return 0, true, &gluaSchemaFailure{path: path, message: name + " must be a non-negative integer", invalidSchema: true}
	}
	return int(value.Integer), true, nil
}

// gluaValueIsNull 判断值是否是 glua.null 单例。
//
// value 可为任意 Lua 值；返回 true 仅表示 table 引用与只读 null 哨兵相同。
func gluaValueIsNull(value runtime.Value) bool {
	// nil 与 glua.null 语义不同。
	return value.Kind == runtime.KindTable && value.Ref == gluaSerializationNull
}

// gluaSchemaValueType 返回 schema 错误使用的稳定类型名。
//
// value 可为任意 Lua 值；glua.null 返回 null，显式或自动 table 返回 array/object，其他值使用
// LuaTypeName。
func gluaSchemaValueType(value runtime.Value) string {
	// null 哨兵优先识别。
	if gluaValueIsNull(value) {
		// 与普通 table 区分。
		return "null"
	}
	if value.Kind == runtime.KindTable {
		// 尝试判断结构化形状。
		table, _ := value.Ref.(*runtime.Table)
		shape, failure := gluaSchemaTableShape(table, "$")
		if failure == nil && shape == runtime.TableShapeArray {
			// 连续数组返回 array。
			return "array"
		}
		if failure == nil {
			// 其他合法 table 返回 object。
			return "object"
		}
	}
	return runtime.LuaTypeName(value)
}
