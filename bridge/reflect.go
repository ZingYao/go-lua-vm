package bridge

import (
	stdcontext "context"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/zing/go-lua-vm/lua"
	"github.com/zing/go-lua-vm/runtime"
)

var (
	// reflectErrorType 保存 error 接口类型，用于识别最后一个错误返回值。
	reflectErrorType = reflect.TypeOf((*error)(nil)).Elem()
	// reflectContextType 保存 context.Context 接口类型，用于注入当前 State 的上下文。
	reflectContextType = reflect.TypeOf((*stdcontext.Context)(nil)).Elem()
	// reflectStateType 保存 *lua.State 类型，用于把当前 State 注入反射函数。
	reflectStateType = reflect.TypeOf((*lua.State)(nil))
	// reflectLuaValueType 保存 lua.Value 类型，用于原样传递 Lua 值。
	reflectLuaValueType = reflect.TypeOf(lua.Value{})
	// reflectRuntimeTableType 保存 *runtime.Table 类型，用于允许反射函数直接返回 table。
	reflectRuntimeTableType = reflect.TypeOf((*runtime.Table)(nil))
	// reflectRuntimeUserdataType 保存 *runtime.Userdata 类型，用于允许反射函数直接返回 userdata。
	reflectRuntimeUserdataType = reflect.TypeOf((*runtime.Userdata)(nil))
	// reflectBridgeFunctionType 保存 bridge.Function 类型，用于允许反射函数返回可调用闭包。
	reflectBridgeFunctionType = reflect.TypeOf(Function(nil))
	// reflectRuntimeGoResultsFunctionType 保存 runtime.GoResultsFunction 类型，用于允许反射函数返回底层 Go closure。
	reflectRuntimeGoResultsFunctionType = reflect.TypeOf(runtime.GoResultsFunction(nil))
)

// ReflectFunction 把 Go 函数自动包装为 bridge.Function。
//
// fn 必须是非 nil 函数；参数支持 bool、整数、浮点、string、lua.Value、*lua.State 和
// context.Context；返回值支持 ValueOf 可转换类型，最后一个 error 返回值会映射为 Lua error。
// 调用期 panic 会恢复为 Lua runtime error，避免穿透宿主调用栈。
func ReflectFunction(fn any) (Function, error) {
	// 先在构造期完成签名检查，避免 Lua 调用时才暴露不可支持签名。
	functionValue := reflect.ValueOf(fn)
	if !functionValue.IsValid() || functionValue.Kind() != reflect.Func || functionValue.IsNil() {
		// 非函数或 nil 函数没有可绑定目标。
		return nil, lua.RaiseError(runtime.StringValue("reflection binding requires a non-nil function"))
	}
	functionType := functionValue.Type()
	for index := 0; index < functionType.NumIn(); index++ {
		// 每个参数都必须有明确 Lua 到 Go 转换规则。
		if !reflectInputSupported(functionType.In(index)) {
			return nil, lua.RaiseError(runtime.StringValue(fmt.Sprintf("unsupported reflection argument type at #%d: %s", index+1, functionType.In(index))))
		}
	}
	for index := 0; index < functionType.NumOut(); index++ {
		// 返回值允许最后一位 error；其他位置必须可转换为 Lua 值。
		if reflectOutputIsError(functionType, index) {
			continue
		}
		if !reflectOutputSupported(functionType.Out(index)) {
			return nil, lua.RaiseError(runtime.StringValue(fmt.Sprintf("unsupported reflection return type at #%d: %s", index+1, functionType.Out(index))))
		}
	}

	return func(context *Context) (err error) {
		defer func() {
			// 反射调用是 Go/Lua 边界的一部分，panic 必须恢复为 Lua error。
			if recovered := recover(); recovered != nil {
				err = RecoverPanic(recovered)
			}
		}()
		inputs := make([]reflect.Value, 0, functionType.NumIn())
		luaArgumentIndex := 1
		for index := 0; index < functionType.NumIn(); index++ {
			// 按函数签名逐项转换 Lua 参数。
			inputType := functionType.In(index)
			input, convertErr := reflectInputValue(context, luaArgumentIndex, inputType)
			if convertErr != nil {
				return convertErr
			}
			inputs = append(inputs, input)
			if reflectInputConsumesLuaArgument(inputType) {
				// context.Context 和 *lua.State 是注入参数，不消耗 Lua 实参。
				luaArgumentIndex++
			}
		}
		outputs := functionValue.Call(inputs)
		for index, output := range outputs {
			// 最后一位 error 非 nil 时作为 Lua 调用错误返回，nil 时不产生正常返回值。
			if reflectOutputIsError(functionType, index) {
				if output.IsNil() {
					continue
				}
				return output.Interface().(error)
			}
			luaValue, convertErr := ValueOf(context.State(), output.Interface())
			if convertErr != nil {
				return convertErr
			}
			context.PushValue(luaValue)
		}
		return nil
	}, nil
}

// reflectInputConsumesLuaArgument 判断 Go 参数是否需要从 Lua 实参列表读取。
func reflectInputConsumesLuaArgument(inputType reflect.Type) bool {
	// 注入参数由 bridge 上下文提供，不推进 Lua 参数索引。
	return inputType != reflectContextType && inputType != reflectStateType
}

// ReflectedFunctions 把函数 map 自动包装为 TableBinding/ModuleBinding 可直接使用的函数表。
//
// functions 的 key 是 Lua 侧可见名称；value 必须是非 nil Go 函数。返回 map 中每个函数都已经
// 完成签名检查，任一函数不支持时整体返回错误。
func ReflectedFunctions(functions map[string]any) (map[string]Function, error) {
	if functions == nil {
		// nil map 表示没有函数需要绑定。
		return nil, nil
	}
	reflectedFunctions := make(map[string]Function, len(functions))
	for name, function := range functions {
		// 空名称无法稳定写入 Lua table，提前拒绝。
		if name == "" {
			return nil, lua.RaiseError(runtime.StringValue("reflected function name is empty"))
		}
		reflectedFunction, err := ReflectFunction(function)
		if err != nil {
			return nil, err
		}
		reflectedFunctions[name] = reflectedFunction
	}
	return reflectedFunctions, nil
}

// ReflectStruct 把 Go struct 或 *struct 自动扫描为 ObjectBinding。
//
// object 必须是非 nil struct 或 struct 指针；导出字段会按 lowerCamel 或 `glua` tag 暴露为 Lua
// 属性，导出方法会按 lowerCamel 暴露为 Lua callable。匿名嵌入 struct 字段会被展开；重复 Lua
// 名称、未知 tag 选项和不支持的字段类型会在构造期返回错误，避免运行期隐式暴露不稳定行为。
func ReflectStruct(object any) (ObjectBinding, error) {
	// 先解析根对象类型，构造期拒绝 nil 和非 struct 输入。
	objectValue := reflect.ValueOf(object)
	structType, writable, err := reflectStructType(objectValue)
	if err != nil {
		// 输入对象不满足 struct 绑定要求时直接返回错误。
		return ObjectBinding{}, err
	}

	// 通过 map 收集字段和方法，保持与显式 ObjectBinding 的成员边界一致。
	binding := ObjectBinding{
		Name:    structType.Name(),
		Object:  object,
		Methods: map[string]ObjectMethod{},
		Getters: map[string]PropertyGetter{},
		Setters: map[string]PropertySetter{},
	}
	if err := reflectCollectStructFields(structType, nil, writable, binding.Getters, binding.Setters); err != nil {
		// 字段扫描错误说明绑定配置不稳定，不能返回半成品。
		return ObjectBinding{}, err
	}
	if err := reflectCollectStructMethods(objectValue, binding.Methods, binding.Getters); err != nil {
		// 方法扫描错误说明与字段命名冲突或签名不支持。
		return ObjectBinding{}, err
	}
	return binding, nil
}

// reflectStructType 解析可绑定 struct 类型和是否可写字段。
func reflectStructType(objectValue reflect.Value) (reflect.Type, bool, error) {
	if !objectValue.IsValid() {
		// nil interface 没有可扫描的 Go 类型。
		return nil, false, lua.RaiseError(runtime.StringValue("reflection struct binding requires a non-nil struct"))
	}
	if objectValue.Kind() == reflect.Ptr {
		// nil 指针没有可代理 identity，必须在绑定期拒绝。
		if objectValue.IsNil() {
			return nil, false, lua.RaiseError(runtime.StringValue("reflection struct binding requires a non-nil struct pointer"))
		}
		if objectValue.Type().Elem().Kind() != reflect.Struct {
			// 只有 struct 指针支持对象字段和方法扫描。
			return nil, false, lua.RaiseError(runtime.StringValue("reflection struct binding requires a struct or struct pointer"))
		}
		return objectValue.Type().Elem(), true, nil
	}
	if objectValue.Kind() != reflect.Struct {
		// 非 struct 值不会自动暴露，避免反射扩散。
		return nil, false, lua.RaiseError(runtime.StringValue("reflection struct binding requires a struct or struct pointer"))
	}
	return objectValue.Type(), false, nil
}

// reflectCollectStructFields 收集导出字段 getter/setter。
func reflectCollectStructFields(structType reflect.Type, prefix []int, writable bool, getters map[string]PropertyGetter, setters map[string]PropertySetter) error {
	for index := 0; index < structType.NumField(); index++ {
		// 每个字段先处理可见性和 tag，再决定是否递归展开。
		field := structType.Field(index)
		if field.PkgPath != "" {
			// 未导出字段不进入 Lua 侧，避免破坏 Go 封装边界。
			continue
		}
		fieldIndex := append(append([]int(nil), prefix...), index)
		if reflectFieldIsEmbeddedStruct(field) {
			// 匿名嵌入 struct 按 Lua 侧平铺字段处理。
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				// 嵌入指针字段按其元素 struct 展开，读取时再检查 nil。
				embeddedType = embeddedType.Elem()
			}
			if err := reflectCollectStructFields(embeddedType, fieldIndex, writable, getters, setters); err != nil {
				return err
			}
			continue
		}
		name, readonly, ignored, err := reflectStructFieldName(field)
		if err != nil {
			// tag 解析失败时返回配置错误，避免静默忽略。
			return err
		}
		if ignored {
			// 显式忽略字段不暴露到 Lua。
			continue
		}
		if !reflectStructFieldSupported(field.Type) {
			// 不支持的字段类型不做隐式深拷贝或递归代理。
			return lua.RaiseError(runtime.StringValue(fmt.Sprintf("unsupported reflected struct field %s type: %s", field.Name, field.Type)))
		}
		if _, exists := getters[name]; exists {
			// 字段 Lua 名称重复会导致访问歧义，构造期必须拒绝。
			return lua.RaiseError(runtime.StringValue("duplicate reflected struct member: " + name))
		}
		getters[name] = reflectStructFieldGetter(fieldIndex)
		if writable && !readonly && reflectStructFieldWritable(field.Type) {
			// 只有 struct 指针、非 readonly 且支持 Lua 入参转换的字段才生成 setter。
			setters[name] = reflectStructFieldSetter(fieldIndex, field.Type)
		}
	}
	return nil
}

// reflectCollectStructMethods 收集导出方法并转换为 ObjectMethod。
func reflectCollectStructMethods(objectValue reflect.Value, methods map[string]ObjectMethod, getters map[string]PropertyGetter) error {
	objectType := objectValue.Type()
	for index := 0; index < objectType.NumMethod(); index++ {
		// reflect.NumMethod 只返回导出方法，符合 Lua 侧可见性策略。
		method := objectType.Method(index)
		luaName := reflectLowerCamel(method.Name)
		if _, exists := getters[luaName]; exists {
			// 方法与字段同名时访问语义不明确，构造期拒绝。
			return lua.RaiseError(runtime.StringValue("duplicate reflected struct member: " + luaName))
		}
		if _, exists := methods[luaName]; exists {
			// Go 方法集理论上不会重复，这里防御后续命名策略变化。
			return lua.RaiseError(runtime.StringValue("duplicate reflected struct method: " + luaName))
		}
		methodValue := objectValue.Method(index)
		reflectedFunction, err := ReflectFunction(methodValue.Interface())
		if err != nil {
			// 方法签名不支持时暴露具体方法名，便于宿主修正绑定对象。
			return lua.RaiseError(runtime.StringValue(fmt.Sprintf("unsupported reflected struct method %s: %v", method.Name, err)))
		}
		methods[luaName] = func(reflectedFunction Function) ObjectMethod {
			// 闭包捕获每个方法自己的反射函数，避免循环变量复用。
			return func(context *ObjectContext) error {
				if context == nil {
					// nil ObjectContext 无法读取方法参数。
					return lua.RaiseError(runtime.StringValue("nil reflected object method context"))
				}
				return reflectedFunction(context.Context)
			}
		}(reflectedFunction)
	}
	return nil
}

// reflectFieldIsEmbeddedStruct 判断字段是否是可展开的匿名嵌入 struct。
func reflectFieldIsEmbeddedStruct(field reflect.StructField) bool {
	if !field.Anonymous {
		// 非匿名字段不做嵌入展开。
		return false
	}
	if field.Tag.Get("glua") == "-" {
		// 显式忽略的匿名字段不展开。
		return false
	}
	fieldType := field.Type
	if fieldType.Kind() == reflect.Ptr {
		// 指针嵌入字段只在元素是 struct 时展开。
		fieldType = fieldType.Elem()
	}
	return fieldType.Kind() == reflect.Struct
}

// reflectStructFieldName 解析字段 Lua 名称、只读标记和忽略标记。
func reflectStructFieldName(field reflect.StructField) (string, bool, bool, error) {
	tagValue := field.Tag.Get("glua")
	if tagValue == "-" {
		// glua:"-" 表示字段完全不暴露。
		return "", false, true, nil
	}
	luaName := reflectLowerCamel(field.Name)
	readonly := false
	if tagValue != "" {
		// tag 第一段是 Lua 名称，后续选项目前只支持 readonly。
		parts := strings.Split(tagValue, ",")
		if parts[0] != "" {
			luaName = parts[0]
		}
		for _, option := range parts[1:] {
			// 空 option 兼容尾随逗号，不改变语义。
			if option == "" {
				continue
			}
			if option != "readonly" {
				return "", false, false, lua.RaiseError(runtime.StringValue(fmt.Sprintf("unknown glua tag option %q on field %s", option, field.Name)))
			}
			readonly = true
		}
	}
	if luaName == "" {
		// 空名称无法稳定写入 Lua table。
		return "", false, false, lua.RaiseError(runtime.StringValue("empty reflected struct field name: " + field.Name))
	}
	return luaName, readonly, false, nil
}

// reflectStructFieldSupported 判断字段读取是否有稳定 Lua 转换规则。
func reflectStructFieldSupported(fieldType reflect.Type) bool {
	if fieldType == reflectLuaValueType || fieldType == reflectRuntimeTableType || fieldType == reflectRuntimeUserdataType {
		// lua.Value/table/userdata 可作为只读字段返回。
		return true
	}
	switch fieldType.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		// 基础标量字段支持读写转换。
		return true
	default:
		// struct、slice、map、普通指针等类型需要显式 ObjectBinding。
		return false
	}
}

// reflectStructFieldWritable 判断字段写入是否有稳定 Lua 到 Go 转换规则。
func reflectStructFieldWritable(fieldType reflect.Type) bool {
	// 字段写入复用反射函数入参转换，避免维护两套 Lua 到 Go 标量规则。
	return reflectInputSupported(fieldType)
}

// reflectStructFieldGetter 构造字段 getter。
func reflectStructFieldGetter(index []int) PropertyGetter {
	copiedIndex := append([]int(nil), index...)
	return func(object any) (lua.Value, error) {
		// 每次读取都从当前对象取字段，保证 Go 侧修改能被 Lua 观察。
		fieldValue, err := reflectStructFieldValue(object, copiedIndex)
		if err != nil {
			return lua.Value{Kind: lua.KindNil}, err
		}
		if !fieldValue.CanInterface() {
			// 理论上只收集导出字段，这里防御嵌入路径导致的不可见字段。
			return lua.Value{Kind: lua.KindNil}, lua.RaiseError(runtime.StringValue("reflected field is not interfaceable"))
		}
		return ValueOf(nil, fieldValue.Interface())
	}
}

// reflectStructFieldSetter 构造字段 setter。
func reflectStructFieldSetter(index []int, fieldType reflect.Type) PropertySetter {
	copiedIndex := append([]int(nil), index...)
	return func(object any, value lua.Value) error {
		// setter 从 Lua 值转换为字段类型，并写回原始 Go 对象。
		fieldValue, err := reflectStructFieldValue(object, copiedIndex)
		if err != nil {
			return err
		}
		if !fieldValue.CanSet() {
			// 非指针 struct 或不可寻址字段不能写入。
			return lua.RaiseError(runtime.StringValue("reflected field is read-only"))
		}
		converted, err := reflectInputValue(NewContext(nil, value), 1, fieldType)
		if err != nil {
			return err
		}
		fieldValue.Set(converted)
		return nil
	}
}

// reflectStructFieldValue 按字段索引链读取当前对象字段。
func reflectStructFieldValue(object any, index []int) (reflect.Value, error) {
	current := reflect.ValueOf(object)
	if !current.IsValid() {
		// nil object 无法读取字段。
		return reflect.Value{}, lua.RaiseError(runtime.StringValue("nil reflected struct object"))
	}
	if current.Kind() == reflect.Ptr {
		// 读取 struct 指针前必须解引用并检查 nil。
		if current.IsNil() {
			return reflect.Value{}, lua.RaiseError(runtime.StringValue("nil reflected struct pointer"))
		}
		current = current.Elem()
	}
	for _, fieldIndex := range index {
		// 嵌入指针字段可能出现在索引链中，每一步都需要防御 nil。
		if current.Kind() == reflect.Ptr {
			if current.IsNil() {
				return reflect.Value{}, lua.RaiseError(runtime.StringValue("nil embedded reflected struct pointer"))
			}
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct {
			// 索引链只能穿过 struct。
			return reflect.Value{}, lua.RaiseError(runtime.StringValue("invalid reflected struct field path"))
		}
		current = current.Field(fieldIndex)
	}
	return current, nil
}

// reflectLowerCamel 把 Go 导出标识符转换为 Lua 侧默认 lowerCamel 名称。
func reflectLowerCamel(name string) string {
	if name == "" {
		// 空名称保持空，由调用方决定是否报错。
		return ""
	}
	runes := []rune(name)
	upperPrefix := 0
	for upperPrefix < len(runes) && unicode.IsUpper(runes[upperPrefix]) {
		// 统计开头连续大写字母，用于处理 HTTPClient 这类首字母缩写。
		upperPrefix++
	}
	if upperPrefix > 1 && upperPrefix < len(runes) && unicode.IsLower(runes[upperPrefix]) {
		// 最后一个大写字母属于后续单词首字母，例如 HTTPClient 中的 C。
		upperPrefix--
	}
	if upperPrefix == 0 {
		// 已经是非大写开头时不改名。
		return name
	}
	for index := 0; index < upperPrefix; index++ {
		// 只降低前缀大写字母，保留后续缩写语义。
		runes[index] = unicode.ToLower(runes[index])
	}
	return string(runes)
}

// reflectInputSupported 判断 Go 参数类型是否有稳定 Lua 转换规则。
func reflectInputSupported(inputType reflect.Type) bool {
	if inputType == reflectContextType || inputType == reflectStateType || inputType == reflectLuaValueType {
		// 特殊参数由 bridge 上下文直接注入或透传。
		return true
	}
	switch inputType.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		// 基础标量有明确 Lua 值转换规则。
		return true
	default:
		// 其他类型第一阶段不支持，避免隐式深拷贝 table/struct。
		return false
	}
}

// reflectOutputSupported 判断 Go 返回值类型是否可通过 ValueOf 转为 Lua 值。
func reflectOutputSupported(outputType reflect.Type) bool {
	if outputType == reflectLuaValueType || outputType == reflectRuntimeTableType || outputType == reflectRuntimeUserdataType ||
		outputType == reflectBridgeFunctionType || outputType == reflectRuntimeGoResultsFunctionType {
		// lua.Value 返回值可原样传递。
		return true
	}
	switch outputType.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		// ValueOf 已支持这些基础标量。
		return true
	default:
		// 其他返回类型需要显式 binding 或后续 struct 自动绑定。
		return false
	}
}

// reflectOutputIsError 判断指定返回值是否为最后一位 error。
func reflectOutputIsError(functionType reflect.Type, index int) bool {
	// 只有最后一个返回值为 error 接口时才按错误语义处理。
	return index == functionType.NumOut()-1 && functionType.Out(index) == reflectErrorType
}

// reflectInputValue 把第 index 个 Lua 参数转换为 Go 反射值。
func reflectInputValue(context *Context, index int, inputType reflect.Type) (reflect.Value, error) {
	if inputType == reflectContextType {
		// context.Context 参数注入当前 State 绑定的 context；缺失 State 时退回 Background。
		if context != nil && context.State() != nil && context.State().Context() != nil {
			return reflect.ValueOf(context.State().Context()), nil
		}
		return reflect.ValueOf(stdcontext.Background()), nil
	}
	if inputType == reflectStateType {
		// *lua.State 参数直接注入当前调用 State，可为 nil。
		if context == nil {
			return reflect.Zero(inputType), nil
		}
		return reflect.ValueOf(context.State()), nil
	}
	if context == nil {
		// 普通 Lua 参数转换需要 Context，缺失时返回稳定错误。
		return reflect.Value{}, lua.RaiseError(runtime.StringValue("nil reflection call context"))
	}
	argument := context.Arg(index)
	if inputType == reflectLuaValueType {
		// lua.Value 参数保留原始 Lua 值。
		return reflect.ValueOf(argument), nil
	}
	switch inputType.Kind() {
	case reflect.Bool:
		// bool 参数使用 Lua truthiness。
		return reflect.ValueOf(argument.Truthy()).Convert(inputType), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// 有符号整数必须按 Lua 5.3 整数转换成功。
		integerValue, ok := argument.ToInteger()
		if !ok {
			return reflect.Value{}, lua.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d (%s expected)", index, inputType)))
		}
		converted := reflect.New(inputType).Elem()
		if converted.OverflowInt(integerValue) {
			return reflect.Value{}, lua.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d (%s overflow)", index, inputType)))
		}
		converted.SetInt(integerValue)
		return converted, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// 无符号整数不接受负数，避免回绕。
		integerValue, ok := argument.ToInteger()
		if !ok || integerValue < 0 {
			return reflect.Value{}, lua.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d (%s expected)", index, inputType)))
		}
		converted := reflect.New(inputType).Elem()
		unsignedValue := uint64(integerValue)
		if converted.OverflowUint(unsignedValue) {
			return reflect.Value{}, lua.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d (%s overflow)", index, inputType)))
		}
		converted.SetUint(unsignedValue)
		return converted, nil
	case reflect.Float32, reflect.Float64:
		// 浮点数接受 Lua integer 和 number。
		numberValue, ok := argument.ToNumber()
		if !ok {
			return reflect.Value{}, lua.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d (%s expected)", index, inputType)))
		}
		converted := reflect.New(inputType).Elem()
		if converted.OverflowFloat(numberValue) {
			return reflect.Value{}, lua.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d (%s overflow)", index, inputType)))
		}
		converted.SetFloat(numberValue)
		return converted, nil
	case reflect.String:
		// string 参数使用 Lua 基础字符串转换规则。
		stringValue, err := runtime.ToString(argument)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(stringValue.String).Convert(inputType), nil
	default:
		// 构造期已检查签名，这里只防御后续代码维护遗漏。
		return reflect.Value{}, lua.RaiseError(runtime.StringValue("unsupported reflection argument type"))
	}
}
