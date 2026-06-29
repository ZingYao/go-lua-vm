package bridge

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"

	"github.com/zing/go-lua-vm/lua"
	luaRuntime "github.com/zing/go-lua-vm/runtime"
)

var (
	// ErrReflectUnsupported 表示 reflection 自动绑定遇到当前不支持的 Go 类型或签名。
	ErrReflectUnsupported = errors.New("unsupported reflect binding")
	// ErrReflectConversion 表示 Lua 值与 Go 类型之间无法按 reflection 绑定规则转换。
	ErrReflectConversion = errors.New("reflect conversion failed")
)

var (
	// errorInterfaceType 缓存 Go error 接口类型，用于识别函数返回值中的错误语义。
	errorInterfaceType = reflect.TypeOf((*error)(nil)).Elem()
	// luaValueType 缓存 lua.Value 类型，用于零拷贝传递显式 Lua 值。
	luaValueType = reflect.TypeOf(lua.Value{})
)

// ReflectOptions 描述 reflection 自动绑定的可见性与转换策略。
//
// TagName 为空时默认读取 `lua` tag；tag 支持 `lua:"name"` 重命名、`lua:"-"` 跳过和
// `lua:"name,readonly"` 禁止写入。当前策略只暴露导出字段和导出方法，不扫描未导出成员。
type ReflectOptions struct {
	// TagName 指定字段重命名使用的 struct tag 名称。
	TagName string
}

// BindReflectFunction 把普通 Go 函数自动包装为 Lua callable。
//
// state 可为 nil，但非 nil 时会在调用前检查 context 取消；fn 必须是 Go 函数。参数按 Lua 值转换到
// Go 入参，返回值按顺序转换为 Lua 多返回值；任一非 nil error 返回值会中止并映射为 Lua error。
func BindReflectFunction(state *lua.State, fn any) (lua.Value, error) {
	// 创建绑定器复用统一转换规则，函数级绑定不需要额外选项。
	binder := newReflectBinder(state, ReflectOptions{})
	functionValue, err := binder.bindFunction(fn)
	if err != nil {
		// 函数签名不合法时返回构造错误，调用方不会得到半初始化 closure。
		return lua.Value{Kind: lua.KindNil}, err
	}
	return functionValue, nil
}

// RegisterReflectFunction 把普通 Go 函数自动包装并注册为 Lua 全局函数。
//
// state 必须非 nil；name 是全局变量名；fn 必须是函数。注册后 Lua 侧调用该函数会走 reflection
// 参数和返回值转换，并复用 bridge 的 error 与 panic 边界。
func RegisterReflectFunction(state *lua.State, name string, fn any) error {
	if state == nil {
		// nil State 没有全局环境，无法注册反射函数。
		return lua.ErrNilState
	}
	functionValue, err := BindReflectFunction(state, fn)
	if err != nil {
		// 函数包装失败时不写入全局环境。
		return err
	}
	return lua.SetGlobal(state, name, functionValue)
}

// BindReflectStruct 把 Go struct 或 struct 指针自动绑定为 Lua 对象代理。
//
// state 必须非 nil；object 必须是非 nil struct 或 struct 指针。导出字段会暴露为属性，导出方法会
// 暴露为 Lua callable；不可导出字段不会被扫描，nil receiver 会被拒绝为绑定错误。
func BindReflectStruct(state *lua.State, object any, options ...ReflectOptions) (lua.Value, error) {
	if state == nil {
		// nil State 无法注册 userdata 生命周期，必须提前拒绝。
		return lua.Value{Kind: lua.KindNil}, lua.ErrNilState
	}
	binder := newReflectBinder(state, firstReflectOptions(options))
	objectValue := reflect.ValueOf(object)
	if !objectValue.IsValid() {
		// 无效 object 没有可代理 identity。
		return lua.Value{Kind: lua.KindNil}, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "nil reflect object"})
	}
	if isNilReflectValue(objectValue) {
		// nil 指针 receiver 会导致字段或方法反射调用边界不稳定，统一拒绝。
		return lua.Value{Kind: lua.KindNil}, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "nil reflect object"})
	}
	return binder.bindStruct(objectValue)
}

// reflectBinder 保存一次 reflection 自动绑定的转换上下文。
//
// seen 用于缓存已经代理过的 Go 指针对象，避免 self 引用或循环引用在字段/返回值转换时无限递归。
type reflectBinder struct {
	// state 是当前绑定关联的 Lua State。
	state *lua.State
	// options 保存 tag 和可见性配置。
	options ReflectOptions
	// seen 保存 Go 指针地址到 Lua 代理值的缓存。
	seen map[uintptr]lua.Value
}

// newReflectBinder 创建 reflection 绑定器。
//
// state 可为 nil，仅函数包装时允许；options 会补齐默认 tag 名称。
func newReflectBinder(state *lua.State, options ReflectOptions) *reflectBinder {
	if options.TagName == "" {
		// 空 tag 名称使用 bridge 默认的 lua tag。
		options.TagName = "lua"
	}
	return &reflectBinder{
		state:   state,
		options: options,
		seen:    make(map[uintptr]lua.Value),
	}
}

// bindFunction 把 reflect 函数值包装为 Lua Go closure。
//
// fn 必须是函数；返回的 Lua 值可直接写入全局环境、table 或作为其他反射返回值继续传递。
func (binder *reflectBinder) bindFunction(fn any) (lua.Value, error) {
	functionValue := reflect.ValueOf(fn)
	if !functionValue.IsValid() || functionValue.Kind() != reflect.Func {
		// 非函数值没有可调用目标。
		return lua.Value{Kind: lua.KindNil}, fmt.Errorf("%w: function expected", ErrReflectUnsupported)
	}
	if functionValue.IsNil() {
		// nil 函数值不能调用，提前拒绝避免运行期 panic。
		return lua.Value{Kind: lua.KindNil}, lua.ErrExpectedCallable
	}
	luaFunction := lua.Function(func(args ...lua.Value) (results []lua.Value, err error) {
		defer func() {
			// 反射调用是 Go/Lua 边界，panic 必须转为 Lua error 而不能穿透宿主。
			if recovered := recover(); recovered != nil {
				results = nil
				err = RecoverPanic(recovered)
			}
		}()
		if binder.state != nil {
			// 有 State 时先遵守 context 取消语义，避免取消后继续进入宿主函数。
			if checkErr := binder.state.CheckContext(); checkErr != nil {
				return nil, checkErr
			}
		}
		return binder.callReflectFunction(functionValue, args)
	})
	return luaRuntime.ReferenceValue(luaRuntime.KindGoClosure, luaRuntime.GoResultsFunction(luaFunction)), nil
}

// callReflectFunction 执行一次反射函数调用。
//
// functionValue 必须是有效函数；args 是 Lua 侧实参。返回值按 Go 返回值顺序转换，error 返回值只作为
// 错误通道，不进入 Lua 多返回值。
func (binder *reflectBinder) callReflectFunction(functionValue reflect.Value, args []lua.Value) ([]lua.Value, error) {
	functionType := functionValue.Type()
	if len(args) != functionType.NumIn() {
		// 当前自动绑定使用严格 arity，避免缺参被误转为 Go 零值造成业务歧义。
		return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: fmt.Sprintf("argument count mismatch: got %d, want %d", len(args), functionType.NumIn())})
	}

	// 按函数签名逐个转换参数，保持 Lua 参数顺序。
	callArgs := make([]reflect.Value, 0, functionType.NumIn())
	for index := 0; index < functionType.NumIn(); index++ {
		convertedArg, err := binder.luaToReflect(args[index], functionType.In(index))
		if err != nil {
			// 任一参数转换失败都会中止调用，避免宿主函数收到错误类型。
			return nil, ErrorFromGo(fmt.Errorf("argument %d: %w", index+1, err))
		}
		callArgs = append(callArgs, convertedArg)
	}

	// 通过 reflect.Call 进入宿主函数；panic 已在外层边界恢复。
	goResults := functionValue.Call(callArgs)
	luaResults := make([]lua.Value, 0, len(goResults))
	for index, result := range goResults {
		if result.Type().Implements(errorInterfaceType) {
			// error 返回值作为错误语义处理，nil error 不暴露给 Lua。
			if !result.IsNil() {
				return nil, ErrorFromGo(result.Interface().(error))
			}
			continue
		}
		convertedResult, err := binder.reflectToLua(result)
		if err != nil {
			// 返回值转换失败说明宿主函数返回了不可暴露类型。
			return nil, ErrorFromGo(fmt.Errorf("return %d: %w", index+1, err))
		}
		luaResults = append(luaResults, convertedResult)
	}
	return luaResults, nil
}

// bindStruct 把 struct 或 struct 指针转换为显式 ObjectBinding 并复用现有代理。
//
// objectValue 必须是非 nil 值；方法和字段扫描会按 Go 可见性以及 lua tag 生成稳定绑定表。
func (binder *reflectBinder) bindStruct(objectValue reflect.Value) (lua.Value, error) {
	if objectValue.Kind() == reflect.Interface {
		// interface 包装值先展开，避免把接口本身误判为不可绑定对象。
		objectValue = objectValue.Elem()
	}
	if objectValue.Kind() != reflect.Struct && !(objectValue.Kind() == reflect.Pointer && objectValue.Type().Elem().Kind() == reflect.Struct) {
		// 只允许 struct 和 struct 指针进入对象代理路径。
		return lua.Value{Kind: lua.KindNil}, fmt.Errorf("%w: struct or struct pointer expected", ErrReflectUnsupported)
	}
	if isNilReflectValue(objectValue) {
		// nil 指针没有可访问字段和稳定 receiver。
		return lua.Value{Kind: lua.KindNil}, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "nil reflect object"})
	}
	if cachedValue, ok := binder.cachedStructValue(objectValue); ok {
		// 命中缓存时直接返回已有代理，打断循环引用。
		return cachedValue, nil
	}

	// 生成显式绑定表后委托 BindStruct，保持 Lua 侧元表和 userdata 策略一致。
	binding, err := binder.objectBindingFromStruct(objectValue)
	if err != nil {
		// 字段或方法扫描失败时不创建半初始化代理。
		return lua.Value{Kind: lua.KindNil}, err
	}
	proxyValue, err := BindStruct(binder.state, binding)
	if err != nil {
		// 底层显式代理创建失败时直接返回。
		return lua.Value{Kind: lua.KindNil}, err
	}
	binder.cacheStructValue(objectValue, proxyValue)
	return proxyValue, nil
}

// objectBindingFromStruct 从反射类型生成 ObjectBinding。
//
// objectValue 可为 struct 或 struct 指针；返回的 binding 只包含导出字段、导出方法和 tag 允许的成员。
func (binder *reflectBinder) objectBindingFromStruct(objectValue reflect.Value) (ObjectBinding, error) {
	objectType := objectValue.Type()
	structValue := objectValue
	if objectValue.Kind() == reflect.Pointer {
		// 指针对象的字段来自 Elem，方法仍使用指针类型以包含 pointer receiver。
		structValue = objectValue.Elem()
	}
	structType := structValue.Type()
	binding := ObjectBinding{
		Name:    structType.Name(),
		Object:  objectValue.Interface(),
		Methods: make(map[string]ObjectMethod),
		Getters: make(map[string]PropertyGetter),
		Setters: make(map[string]PropertySetter),
	}

	// 使用 VisibleFields 覆盖嵌入字段提升规则，保持 Go 语言选择器语义。
	for _, field := range reflect.VisibleFields(structType) {
		fieldInfo, ok := binder.reflectFieldInfo(field)
		if !ok {
			// tag 跳过、未导出或不可见字段不进入 Lua 暴露面。
			continue
		}
		binder.attachReflectField(&binding, objectValue, field, fieldInfo)
	}
	for index := 0; index < objectType.NumMethod(); index++ {
		method := objectType.Method(index)
		if method.PkgPath != "" {
			// reflect.NumMethod 通常只返回导出方法；这里保留防御检查，避免未导出方法外泄。
			continue
		}
		methodName := method.Name
		binding.Methods[methodName] = binder.wrapReflectMethod(objectValue, method)
	}
	return binding, nil
}

// reflectFieldInfo 保存一个可暴露字段的 Lua 名称和权限。
type reflectFieldInfo struct {
	// name 是 Lua 侧属性名。
	name string
	// readonly 表示字段只允许读取，不生成 setter。
	readonly bool
}

// reflectFieldInfo 按 tag 和导出规则判断字段是否允许暴露。
//
// field 必须来自 reflect.VisibleFields；返回 ok=false 表示字段应跳过。
func (binder *reflectBinder) reflectFieldInfo(field reflect.StructField) (reflectFieldInfo, bool) {
	if !field.IsExported() {
		// 不可导出字段永远不暴露，避免破坏 Go 包封装和权限边界。
		return reflectFieldInfo{}, false
	}
	tagName, readonly, skip := parseReflectLuaTag(field.Tag.Get(binder.options.TagName))
	if skip {
		// tag 显式要求忽略字段。
		return reflectFieldInfo{}, false
	}
	fieldName := field.Name
	if tagName != "" {
		// tag 重命名优先于 Go 字段名。
		fieldName = tagName
	}
	return reflectFieldInfo{name: fieldName, readonly: readonly}, true
}

// attachReflectField 把一个导出字段接入 ObjectBinding getter/setter。
//
// binding 必须非 nil；objectValue 是原始 struct 或指针，field 保存 VisibleFields 的索引路径。
func (binder *reflectBinder) attachReflectField(binding *ObjectBinding, objectValue reflect.Value, field reflect.StructField, fieldInfo reflectFieldInfo) {
	binding.Getters[fieldInfo.name] = func(object any) (lua.Value, error) {
		// 每次读取都从当前对象取字段，确保写入后的 Go 状态可被观察。
		currentObjectValue := reflect.ValueOf(object)
		fieldValue, err := reflectFieldByIndex(currentObjectValue, field.Index)
		if err != nil {
			// nil 嵌入指针或损坏对象会返回 Lua error。
			return lua.Value{Kind: lua.KindNil}, ErrorFromGo(err)
		}
		return binder.reflectToLua(fieldValue)
	}
	if fieldInfo.readonly {
		// readonly 字段只生成 getter，不允许 Lua 写回。
		return
	}
	if !reflectFieldCanSet(objectValue, field.Index) {
		// 非指针对象或不可设置字段没有稳定写回位置，保持只读。
		return
	}
	binding.Setters[fieldInfo.name] = func(object any, value lua.Value) error {
		// setter 在写入时重新定位字段，避免持有过期 reflect.Value。
		currentObjectValue := reflect.ValueOf(object)
		fieldValue, err := reflectFieldByIndex(currentObjectValue, field.Index)
		if err != nil {
			// 字段路径不可达时返回错误，避免静默丢失写入。
			return ErrorFromGo(err)
		}
		convertedValue, err := binder.luaToReflect(value, fieldValue.Type())
		if err != nil {
			// Lua 值不能转换成字段类型时返回明确错误。
			return ErrorFromGo(err)
		}
		fieldValue.Set(convertedValue)
		return nil
	}
}

// wrapReflectMethod 把反射方法包装为 ObjectMethod。
//
// objectValue 是绑定 receiver；method 必须是导出方法。Lua 冒号调用传入的 self 已由 ObjectProxy 移除。
func (binder *reflectBinder) wrapReflectMethod(objectValue reflect.Value, method reflect.Method) ObjectMethod {
	return func(context *ObjectContext) error {
		// 从绑定对象重新解析 receiver，避免闭包捕获的 reflect.Value 因复制产生不可预期状态。
		receiverValue := reflect.ValueOf(context.Object())
		if !receiverValue.IsValid() || isNilReflectValue(receiverValue) {
			// nil receiver 不能安全调用反射方法。
			return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "nil reflect receiver"})
		}
		methodValue := receiverValue.MethodByName(method.Name)
		if !methodValue.IsValid() {
			// receiver 不包含目标方法时说明绑定表与对象类型不一致。
			return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "reflect method not found: " + method.Name})
		}
		results, err := binder.callReflectFunction(methodValue, context.Args())
		if err != nil {
			// 方法调用错误按 ObjectMethod 语义上抛给代理。
			return err
		}
		for _, result := range results {
			// ObjectContext 通过 PushValue 保留多返回值顺序。
			context.PushValue(result)
		}
		return nil
	}
}

// luaToReflect 把 Lua 值转换为目标 Go 类型。
//
// targetType 是函数入参或字段类型；转换失败返回带 ErrReflectConversion 的错误。
func (binder *reflectBinder) luaToReflect(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	if targetType == luaValueType {
		// 显式要求 lua.Value 时原样传递，便于宿主接管复杂转换。
		return reflect.ValueOf(value), nil
	}
	if value.IsNil() {
		// nil 只能转成 Go 可 nil 类型，否则会丢失必填参数语义。
		return nilReflectValue(targetType)
	}
	switch targetType.Kind() {
	case reflect.Bool:
		// bool 入参采用 Lua 条件真值语义，匹配 lua_toboolean。
		return reflect.ValueOf(value.Truthy()).Convert(targetType), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// 整数类型必须能从 Lua integer/可整数化 number 无损转换。
		return luaToSignedInteger(value, targetType)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// 无符号整数不接受负数，且必须在目标位宽范围内。
		return luaToUnsignedInteger(value, targetType)
	case reflect.Float32, reflect.Float64:
		// 浮点类型接受 Lua integer 和 number。
		return luaToFloat(value, targetType)
	case reflect.String:
		// string 入参复用 Lua tostring 基础语义，保留 number 到 string 的兼容转换。
		return luaToString(value, targetType)
	case reflect.Slice:
		// []byte 使用 Lua string 字节内容转换，其他 slice 当前不自动从 table 解包。
		return luaToByteSlice(value, targetType)
	case reflect.Pointer, reflect.Struct:
		// struct 或指针参数支持从 bridge 代理 table 取回原始 Go 对象。
		return luaToObject(value, targetType)
	case reflect.Interface:
		// 空接口传入 Lua 原始值；非空接口尝试从对象代理中取 Go 对象。
		return luaToInterface(value, targetType)
	default:
		// 其他类型需要后续显式设计，避免自动绑定过度扩散。
		return reflect.Value{}, fmt.Errorf("%w: cannot convert Lua %s to %s", ErrReflectConversion, value.DebugString(), targetType.String())
	}
}

// reflectToLua 把 Go 反射值转换为 Lua 值。
//
// value 可以是函数返回值或字段值；struct 和 struct 指针会转换为 reflection 对象代理。
func (binder *reflectBinder) reflectToLua(value reflect.Value) (lua.Value, error) {
	if !value.IsValid() {
		// 无效反射值按 Lua nil 处理。
		return lua.Value{Kind: lua.KindNil}, nil
	}
	if value.Type() == luaValueType {
		// lua.Value 返回值原样透传。
		return value.Interface().(lua.Value), nil
	}
	if value.Kind() == reflect.Interface {
		// interface 返回值先展开动态值；nil interface 返回 Lua nil。
		if value.IsNil() {
			// nil interface 没有动态负载。
			return lua.Value{Kind: lua.KindNil}, nil
		}
		return binder.reflectToLua(value.Elem())
	}
	if isNilReflectValue(value) {
		// nil 指针、slice、map、func 在 Lua 侧表示为 nil。
		return lua.Value{Kind: lua.KindNil}, nil
	}
	switch value.Kind() {
	case reflect.Bool:
		// bool 返回 Lua boolean。
		return lua.Value{Kind: lua.KindBoolean, Bool: value.Bool()}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// 有符号整数返回 Lua integer。
		return lua.Value{Kind: lua.KindInteger, Integer: value.Int()}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// 无符号整数必须可放入 Lua integer。
		return reflectUnsignedToLua(value)
	case reflect.Float32, reflect.Float64:
		// 浮点返回 Lua number。
		return lua.Value{Kind: lua.KindNumber, Number: value.Convert(reflect.TypeOf(float64(0))).Float()}, nil
	case reflect.String:
		// string 返回 Lua string。
		return lua.Value{Kind: lua.KindString, String: value.String()}, nil
	case reflect.Slice:
		// []byte 返回 Lua string，其他 slice 暂不自动展开为 table。
		return reflectSliceToLua(value)
	case reflect.Func:
		// 函数返回值继续包装为 Lua callable。
		return binder.bindFunction(value.Interface())
	case reflect.Struct, reflect.Pointer:
		// struct 和 struct 指针返回对象代理，以保持 Go identity 和懒加载字段。
		return binder.bindStruct(value)
	default:
		// 未支持类型返回明确错误，避免隐式泄露 Go 内部结构。
		return lua.Value{Kind: lua.KindNil}, fmt.Errorf("%w: cannot convert %s to Lua", ErrReflectUnsupported, value.Type().String())
	}
}

// cachedStructValue 尝试按 Go 指针 identity 读取已创建代理。
//
// objectValue 可以是 struct 或 struct 指针；非可寻址 struct 当前不缓存。
func (binder *reflectBinder) cachedStructValue(objectValue reflect.Value) (lua.Value, bool) {
	pointerKey, ok := reflectPointerKey(objectValue)
	if !ok {
		// 无法获得稳定指针的值不参与循环引用缓存。
		return lua.Value{Kind: lua.KindNil}, false
	}
	cachedValue, ok := binder.seen[pointerKey]
	return cachedValue, ok
}

// cacheStructValue 按 Go 指针 identity 保存代理值。
//
// objectValue 可以是 struct 或 struct 指针；无法获得稳定指针时忽略缓存。
func (binder *reflectBinder) cacheStructValue(objectValue reflect.Value, proxyValue lua.Value) {
	pointerKey, ok := reflectPointerKey(objectValue)
	if !ok {
		// 无稳定指针时不能安全缓存。
		return
	}
	binder.seen[pointerKey] = proxyValue
}

// firstReflectOptions 返回可选配置中的第一项。
//
// 未传配置时返回零值，由 newReflectBinder 统一补默认值。
func firstReflectOptions(options []ReflectOptions) ReflectOptions {
	if len(options) == 0 {
		// 没有显式配置时使用默认 tag 规则。
		return ReflectOptions{}
	}
	return options[0]
}

// parseReflectLuaTag 解析 reflection 字段 tag。
//
// tag 支持空值、`-`、`name`、`name,readonly` 和 `,readonly`；未知选项保留向前兼容并忽略。
func parseReflectLuaTag(tag string) (name string, readonly bool, skip bool) {
	if tag == "-" {
		// "-" 表示字段完全不暴露。
		return "", false, true
	}
	if tag == "" {
		// 空 tag 使用 Go 字段名，权限默认可写。
		return "", false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, option := range parts[1:] {
		if option == "readonly" {
			// readonly 只影响 setter 生成，不影响 getter。
			readonly = true
		}
	}
	return name, readonly, false
}

// reflectFieldByIndex 按 VisibleFields 的索引路径读取字段。
//
// objectValue 可以是 struct 或 struct 指针；嵌入 nil 指针会返回错误而不是 panic。
func reflectFieldByIndex(objectValue reflect.Value, index []int) (reflect.Value, error) {
	if objectValue.Kind() == reflect.Pointer {
		// 顶层指针先解引用后再按字段路径查找。
		if objectValue.IsNil() {
			// nil 顶层对象没有字段可读。
			return reflect.Value{}, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "nil reflect object"})
		}
		objectValue = objectValue.Elem()
	}
	for depth, fieldIndex := range index {
		if objectValue.Kind() == reflect.Pointer {
			// 嵌入指针需要在进入下一层前解引用。
			if objectValue.IsNil() {
				// nil 嵌入字段会让 promoted 字段不可达。
				return reflect.Value{}, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "nil embedded field"})
			}
			objectValue = objectValue.Elem()
		}
		if objectValue.Kind() != reflect.Struct {
			// 字段路径中途不是 struct，说明反射索引已失效。
			return reflect.Value{}, fmt.Errorf("%w: invalid field path at depth %d", ErrReflectUnsupported, depth)
		}
		objectValue = objectValue.Field(fieldIndex)
	}
	return objectValue, nil
}

// reflectFieldCanSet 判断字段当前是否可写。
//
// objectValue 是绑定时的原始对象；index 是 VisibleFields 字段路径。
func reflectFieldCanSet(objectValue reflect.Value, index []int) bool {
	fieldValue, err := reflectFieldByIndex(objectValue, index)
	if err != nil {
		// 字段不可达时不能生成 setter。
		return false
	}
	return fieldValue.CanSet()
}

// reflectPointerKey 返回对象用于循环引用缓存的指针 key。
//
// 指针对象直接使用 Pointer；可寻址 struct 使用 Addr。返回 false 表示没有稳定 identity。
func reflectPointerKey(value reflect.Value) (uintptr, bool) {
	if value.Kind() == reflect.Interface {
		// interface 先展开动态值再取 key。
		if value.IsNil() {
			// nil interface 没有 identity。
			return 0, false
		}
		return reflectPointerKey(value.Elem())
	}
	if value.Kind() == reflect.Pointer {
		// nil 指针没有可缓存对象。
		if value.IsNil() {
			return 0, false
		}
		return value.Pointer(), true
	}
	if value.Kind() == reflect.Struct && value.CanAddr() {
		// 可寻址 struct 使用地址作为 identity。
		return value.Addr().Pointer(), true
	}
	return 0, false
}

// isNilReflectValue 判断反射值是否是可 nil 类型且为 nil。
//
// 非可 nil 类型固定返回 false，避免对普通值调用 IsNil panic。
func isNilReflectValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		// 这些 Kind 支持 IsNil，可直接判断 nil 状态。
		return value.IsNil()
	default:
		// 非可 nil 类型永远不是 nil。
		return false
	}
}

// nilReflectValue 把 Lua nil 转换为 Go 目标类型零值。
//
// 只有可 nil 类型接受 nil；其他目标类型返回转换错误。
func nilReflectValue(targetType reflect.Type) (reflect.Value, error) {
	switch targetType.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		// 可 nil 类型使用目标类型零值表示 nil。
		return reflect.Zero(targetType), nil
	default:
		// 非可 nil 类型不能从 Lua nil 自动填充。
		return reflect.Value{}, fmt.Errorf("%w: nil cannot convert to %s", ErrReflectConversion, targetType.String())
	}
}

// luaToSignedInteger 把 Lua number 转成目标有符号整数。
func luaToSignedInteger(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	integerValue, ok := value.ToInteger()
	if !ok {
		// 非整数 Lua 值不能转换为 Go 整数。
		return reflect.Value{}, fmt.Errorf("%w: integer expected", ErrReflectConversion)
	}
	if targetType.Bits() < 64 {
		// 小整数类型需要显式检查范围。
		minValue := -(int64(1) << (targetType.Bits() - 1))
		maxValue := int64(1)<<(targetType.Bits()-1) - 1
		if integerValue < minValue || integerValue > maxValue {
			// 超出目标位宽时拒绝转换。
			return reflect.Value{}, fmt.Errorf("%w: integer out of range for %s", ErrReflectConversion, targetType.String())
		}
	}
	return reflect.ValueOf(integerValue).Convert(targetType), nil
}

// luaToUnsignedInteger 把 Lua number 转成目标无符号整数。
func luaToUnsignedInteger(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	integerValue, ok := value.ToInteger()
	if !ok {
		// 非整数 Lua 值不能转换为 Go 无符号整数。
		return reflect.Value{}, fmt.Errorf("%w: integer expected", ErrReflectConversion)
	}
	if integerValue < 0 {
		// 负数不能转成无符号整数。
		return reflect.Value{}, fmt.Errorf("%w: negative integer for %s", ErrReflectConversion, targetType.String())
	}
	unsignedValue := uint64(integerValue)
	if targetType.Bits() < 64 && unsignedValue > uint64(1<<targetType.Bits()-1) {
		// 超出目标无符号位宽时拒绝转换。
		return reflect.Value{}, fmt.Errorf("%w: unsigned integer out of range for %s", ErrReflectConversion, targetType.String())
	}
	return reflect.ValueOf(unsignedValue).Convert(targetType), nil
}

// luaToFloat 把 Lua number 转成目标浮点类型。
func luaToFloat(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	numberValue, ok := value.ToNumber()
	if !ok {
		// 非 number Lua 值不能转换为 Go 浮点数。
		return reflect.Value{}, fmt.Errorf("%w: number expected", ErrReflectConversion)
	}
	if targetType.Kind() == reflect.Float32 && (numberValue > math.MaxFloat32 || numberValue < -math.MaxFloat32) {
		// float32 需要避免溢出为 Inf。
		return reflect.Value{}, fmt.Errorf("%w: number out of range for float32", ErrReflectConversion)
	}
	return reflect.ValueOf(numberValue).Convert(targetType), nil
}

// luaToString 把 Lua 值转成 Go string。
func luaToString(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	stringValue, err := luaRuntime.ToString(value)
	if err != nil {
		// Lua 基础 tostring 失败时返回转换错误。
		return reflect.Value{}, fmt.Errorf("%w: %v", ErrReflectConversion, err)
	}
	return reflect.ValueOf(stringValue.String).Convert(targetType), nil
}

// luaToByteSlice 把 Lua string 转成 []byte。
func luaToByteSlice(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	if targetType.Elem().Kind() != reflect.Uint8 {
		// 只有 []byte 有明确的 Lua string 转换语义。
		return reflect.Value{}, fmt.Errorf("%w: unsupported slice %s", ErrReflectConversion, targetType.String())
	}
	if value.Kind != lua.KindString {
		// []byte 入参必须来自 Lua string。
		return reflect.Value{}, fmt.Errorf("%w: string expected for []byte", ErrReflectConversion)
	}
	return reflect.ValueOf([]byte(value.String)).Convert(targetType), nil
}

// luaToObject 从 Lua 代理表取回原始 Go 对象并转换到目标类型。
func luaToObject(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	object, ok := objectFromLuaValue(value)
	if !ok {
		// 非 bridge 代理不能转换为 Go struct 或指针。
		return reflect.Value{}, fmt.Errorf("%w: reflect object proxy expected", ErrReflectConversion)
	}
	objectValue := reflect.ValueOf(object)
	if objectValue.Type().AssignableTo(targetType) {
		// 原始对象类型可直接赋值给目标类型。
		return objectValue, nil
	}
	if objectValue.Type().ConvertibleTo(targetType) {
		// Go 允许的显式转换也可安全执行。
		return objectValue.Convert(targetType), nil
	}
	return reflect.Value{}, fmt.Errorf("%w: object %s cannot convert to %s", ErrReflectConversion, objectValue.Type().String(), targetType.String())
}

// luaToInterface 把 Lua 值转换为目标 interface。
func luaToInterface(value lua.Value, targetType reflect.Type) (reflect.Value, error) {
	if targetType.NumMethod() == 0 {
		// 空接口保留 Lua 原始值，调用方可自行分派。
		return reflect.ValueOf(value), nil
	}
	object, ok := objectFromLuaValue(value)
	if !ok {
		// 非空接口需要从对象代理恢复 Go object。
		return reflect.Value{}, fmt.Errorf("%w: interface object proxy expected", ErrReflectConversion)
	}
	objectValue := reflect.ValueOf(object)
	if objectValue.Type().AssignableTo(targetType) {
		// 原始对象实现目标接口时可直接传入。
		return objectValue, nil
	}
	return reflect.Value{}, fmt.Errorf("%w: object %s does not implement %s", ErrReflectConversion, objectValue.Type().String(), targetType.String())
}

// objectFromLuaValue 从 bridge 代理 table 中读取原始 Go 对象。
func objectFromLuaValue(value lua.Value) (any, bool) {
	if value.Kind != lua.KindTable {
		// 只有对象代理 table 携带隐藏 userdata 字段。
		return nil, false
	}
	tableObject, ok := value.Ref.(*luaRuntime.Table)
	if !ok || tableObject == nil {
		// table 引用损坏时不能恢复对象。
		return nil, false
	}
	userdataValue := tableObject.RawGetString("__userdata")
	if userdataValue.Kind != lua.KindUserdata {
		// 非对象代理没有隐藏 userdata。
		return nil, false
	}
	userdata, ok := userdataValue.Ref.(*luaRuntime.Userdata)
	if !ok || userdata == nil {
		// userdata 引用损坏时不能恢复对象。
		return nil, false
	}
	proxy, ok := userdata.Data.(*ObjectProxy)
	if !ok || proxy == nil {
		// 只有 ObjectProxy 负载才代表 bridge 对象代理。
		return nil, false
	}
	return proxy.Object(), true
}

// reflectUnsignedToLua 把 Go 无符号整数转换为 Lua integer。
func reflectUnsignedToLua(value reflect.Value) (lua.Value, error) {
	unsignedValue := value.Uint()
	if unsignedValue > math.MaxInt64 {
		// Lua integer 当前使用 int64，超出范围时拒绝隐式转换。
		return lua.Value{Kind: lua.KindNil}, fmt.Errorf("%w: unsigned integer out of Lua range", ErrReflectConversion)
	}
	return lua.Value{Kind: lua.KindInteger, Integer: int64(unsignedValue)}, nil
}

// reflectSliceToLua 把 Go slice 转换为 Lua 值。
func reflectSliceToLua(value reflect.Value) (lua.Value, error) {
	if value.Type().Elem().Kind() == reflect.Uint8 {
		// []byte 按 Lua 二进制字符串返回。
		return lua.Value{Kind: lua.KindString, String: string(value.Bytes())}, nil
	}
	return lua.Value{Kind: lua.KindNil}, fmt.Errorf("%w: unsupported slice %s", ErrReflectUnsupported, value.Type().String())
}

// reflectFunctionName 返回函数值的调试名称。
//
// 当前只用于未来错误信息扩展；无法解析时返回空字符串。
func reflectFunctionName(functionValue reflect.Value) string {
	if functionValue.Kind() != reflect.Func || functionValue.IsNil() {
		// 非函数或 nil 函数没有可用调试名称。
		return ""
	}
	function := runtime.FuncForPC(functionValue.Pointer())
	if function == nil {
		// runtime 无法解析 PC 时返回空名称。
		return ""
	}
	return function.Name()
}
