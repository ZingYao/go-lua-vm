// Package bridge 提供 Go 与 Lua 双向桥接的第一阶段能力。
//
// 本包承载 Go 回调包装、参数读取、返回值收集、错误映射和 panic 恢复；稳定入口后续可继续
// 由 lua 包重导出，但内部实现先在 bridge 包内保持独立。
package bridge

import (
	"sort"
	"strings"

	"github.com/ZingYao/go-lua-vm/lua"
	"github.com/ZingYao/go-lua-vm/runtime"
)

// Function 表示桥接层推荐的 Go 回调签名。
//
// 入参 context 提供 State、实参读取和返回值压入能力。返回 error 时，桥接层会转换为 Lua
// runtime error；发生 panic 时，桥接层会 recover 并转换为 RuntimeError。
type Function func(context *Context) error

// ObjectMethod 表示 Go 对象方法的桥接签名。
//
// 入参 context 提供绑定对象、Lua 实参读取和返回值压入能力；返回 error 时按普通 bridge error
// 映射为 Lua error object。
type ObjectMethod func(context *ObjectContext) error

// PropertyGetter 表示 Go 对象属性读取函数。
//
// object 是 ObjectBinding.Object；返回值会作为 Lua 属性读取结果。
type PropertyGetter func(object any) (lua.Value, error)

// PropertySetter 表示 Go 对象属性写入函数。
//
// object 是 ObjectBinding.Object；value 是 Lua 侧写入值。
type PropertySetter func(object any, value lua.Value) error

// ObjectFinalizer 表示 Go 对象代理在 State.Close 阶段的关闭回调。
//
// object 是 ObjectBinding.Object；返回 error 或 panic 都会被 runtime userdata 关闭流程隔离，不会
// 阻断其他 userdata 的关闭。该语义用于释放宿主资源，不等同于 Lua `__gc` 可见回调。
type ObjectFinalizer func(object any) error

// YieldPolicy 表示 Go/Lua 跨边界 yield 支持策略。
//
// 当前阶段尚未实现 coroutine 穿越 Go 回调的恢复协议，因此默认策略是禁止跨边界 yield。
type YieldPolicy string

const (
	// YieldForbidden 表示当前桥接边界不允许 Lua coroutine yield 穿越。
	YieldForbidden YieldPolicy = "forbidden"
	// YieldAllowed 表示后续可恢复调用帧具备后允许 yield 穿越。
	YieldAllowed YieldPolicy = "allowed"
)

// Callable 表示可由 Go 保存并调用的 Lua 函数值。
//
// State 是函数调用所依赖的 Lua State；Value 是 KindGoClosure 或 KindLuaClosure。当前阶段
// KindGoClosure 可执行，KindLuaClosure 会由 lua.Call 返回 ErrExecutionUnavailable。
type Callable struct {
	// state 是函数调用所依赖的 Lua State。
	state *lua.State
	// value 是保存下来的 Lua 函数值。
	value lua.Value
}

// ObjectBinding 描述一个显式 Go 对象绑定。
//
// Object 是被代理的 Go 对象；Methods、Getters、Setters 必须显式列出可见成员，避免默认反射暴露
// 不该进入 Lua 的方法或字段。
type ObjectBinding struct {
	// Name 是对象代理的调试名称，可用于后续 stub 和错误信息。
	Name string
	// Object 是被 Lua 代理持有的 Go 对象。
	Object any
	// Methods 保存允许 Lua 调用的方法。
	Methods map[string]ObjectMethod
	// Getters 保存允许 Lua 读取的属性。
	Getters map[string]PropertyGetter
	// Setters 保存允许 Lua 写入的属性。
	Setters map[string]PropertySetter
	// Finalizer 保存 State.Close 阶段执行的对象关闭回调；nil 表示对象无显式关闭资源。
	Finalizer ObjectFinalizer
}

// ObjectProxy 表示 Go 对象的 Lua 代理。
//
// 代理使用 userdata 保存 Go identity，同时用 table 和 metatable 提供 Lua 侧属性与方法访问。
type ObjectProxy struct {
	// binding 保存显式绑定配置。
	binding ObjectBinding
	// userdata 保存 Go 对象 identity。
	userdata *runtime.Userdata
	// table 保存 Lua 侧代理表。
	table *runtime.Table
}

// ObjectContext 表示一次 Go 对象方法调用上下文。
//
// Context 提供普通 bridge 参数和返回值能力；Object 返回当前绑定的 Go 对象。
type ObjectContext struct {
	// Context 复用普通 Go 回调的参数读取和返回值压入能力。
	*Context
	// object 是当前绑定的 Go 对象。
	object any
}

// ModuleBinding 描述一个 Go 实现的 Lua 模块绑定。
//
// Name 是 Lua 模块名；Functions 和 Objects 必须显式列出要暴露到 Lua 的 API，避免隐式反射扩散。
type ModuleBinding struct {
	// Name 是 Lua 侧模块名，同时用于 package.loaded 和全局环境写入。
	Name string
	// Fields 保存模块 table 上的普通可变字段。
	Fields map[string]any
	// Constants 保存模块 table 上只读常量；Lua 侧写入同名字段会返回错误。
	Constants map[string]any
	// Variables 保存模块 table 上可变变量；Lua 侧可直接覆盖。
	Variables map[string]any
	// Functions 保存模块级 Go 函数。
	Functions map[string]Function
	// Tables 保存模块级嵌套 table。
	Tables map[string]TableBinding
	// Objects 保存模块级 Go 对象代理。
	Objects map[string]ObjectBinding
	// ReadOnly 表示模块 table 是否整体只读；启用后 Lua 侧不能写入任何字段。
	ReadOnly bool
}

// TableBinding 描述一个由 Go 构造并暴露给 Lua 的 table。
//
// Fields、Variables、Functions、Tables 和 Objects 会组成 Lua table 字段；Constants 通过元表保护
// 为只读字段；ReadOnly=true 时所有字段都存放在内部 backing table，Lua 写入会统一失败。
type TableBinding struct {
	// Name 是 table 的调试名称，用于只读错误文本。
	Name string
	// Fields 保存普通可变字段。
	Fields map[string]any
	// Constants 保存只读常量字段。
	Constants map[string]any
	// Variables 保存可变变量字段。
	Variables map[string]any
	// Functions 保存 table 上的 Go 方法或函数字段。
	Functions map[string]Function
	// Tables 保存嵌套 table 字段。
	Tables map[string]TableBinding
	// Objects 保存嵌套对象代理字段。
	Objects map[string]ObjectBinding
	// Metatable 保存宿主提供的元表字段；保留字段会与只读/常量保护元方法合并。
	Metatable map[string]any
	// ReadOnly 表示 Lua 侧不能写入该 table 的任何字段。
	ReadOnly bool
}

// Context 表示一次 Go bridge 调用的上下文。
//
// State 返回当前 Lua State；Args/Arg/To 系列用于读取 Lua 实参；Push 系列用于按顺序压入
// Lua 返回值。Context 不拥有 State 生命周期，调用结束后不应长期持有。
type Context struct {
	// state 是当前调用绑定的 Lua State。
	state *lua.State
	// args 保存 Lua 传入 Go 回调的实参快照。
	args []lua.Value
	// results 保存 Go 回调压入的 Lua 返回值。
	results []lua.Value
}

// NewContext 创建一次桥接调用上下文。
//
// state 可以为 nil，仅表示该上下文不能检查 State 生命周期；args 会被复制，避免调用方后续修改
// 原切片影响桥接调用。
func NewContext(state *lua.State, args ...lua.Value) *Context {
	// 复制实参快照，保证 Context 对外只暴露稳定调用入参。
	copiedArgs := append([]lua.Value(nil), args...)
	return &Context{state: state, args: copiedArgs}
}

// State 返回当前桥接调用绑定的 Lua State。
//
// 返回 nil 表示该 Context 由测试或纯转换路径创建，无法执行依赖 State 的检查。
func (context *Context) State() *lua.State {
	// 直接返回 State 引用，调用方不得关闭不属于自己的 State。
	return context.state
}

// Args 返回 Lua 调用传入 Go 回调的实参快照。
//
// 返回切片是副本，调用方修改该切片不会影响 Context 内部参数。
func (context *Context) Args() []lua.Value {
	// 复制参数切片，避免外部修改破坏后续 Arg/To 读取语义。
	return append([]lua.Value(nil), context.args...)
}

// Arg 按 Lua 1-based 参数索引读取实参。
//
// index 从 1 开始；小于 1 或超过参数数量时返回 Lua nil，便于 Go 回调按 Lua 缺省参数语义处理。
func (context *Context) Arg(index int) lua.Value {
	if index < 1 {
		// Lua 参数索引从 1 开始，非法索引按缺省 nil 处理。
		return lua.Value{Kind: lua.KindNil}
	}
	if index > len(context.args) {
		// 缺失参数在 Lua 调用语义中表现为 nil。
		return lua.Value{Kind: lua.KindNil}
	}

	// 参数切片是 0-based，Lua 索引需要减一。
	return context.args[index-1]
}

// ToBoolean 按 Lua 条件判断语义读取指定参数。
//
// index 使用 Lua 1-based 参数索引；nil 和 false 返回 false，其他值返回 true。
func (context *Context) ToBoolean(index int) (bool, bool) {
	// Arg 已经处理越界为 nil，因此 bool 转换总是成功。
	return context.Arg(index).Truthy(), true
}

// ToInteger 按 Lua 5.3 number-to-integer 规则读取指定参数。
//
// index 使用 Lua 1-based 参数索引；integer 直接返回，有限且无小数的 float number 可转换。
func (context *Context) ToInteger(index int) (int64, bool) {
	// 复用 lua.Value 的整数转换语义，保持 bridge 与 VM 一致。
	return context.Arg(index).ToInteger()
}

// ToNumber 按 Lua 5.3 number 语义读取指定参数。
//
// index 使用 Lua 1-based 参数索引；integer 会转换为 float64，float number 直接返回。
func (context *Context) ToNumber(index int) (float64, bool) {
	// 复用 lua.Value 的 number 转换语义，保持 bridge 与 VM 一致。
	return context.Arg(index).ToNumber()
}

// ToString 按 Lua 基础 tostring 语义读取指定参数。
//
// index 使用 Lua 1-based 参数索引；转换失败时返回 ok=false 和原始错误。
func (context *Context) ToString(index int) (string, bool, error) {
	// 使用临时 State 栈没有必要，直接复用 runtime.ToString 的基础转换。
	stringValue, err := runtime.ToString(context.Arg(index))
	if err != nil {
		// tostring 元方法或转换失败时返回错误，调用方可继续映射为 Lua error。
		return "", false, err
	}
	return stringValue.String, true, nil
}

// ToCallable 把指定参数读取为可由 Go 保存和调用的 Lua 函数。
//
// index 使用 Lua 1-based 参数索引；参数必须是 Go closure 或 Lua closure。当前阶段 Go closure
// 可执行，Lua closure 会在调用时返回 lua.ErrExecutionUnavailable。
func (context *Context) ToCallable(index int) (Callable, bool, error) {
	// 从参数快照读取函数值，保持 Lua 缺省参数按 nil 处理。
	callable, err := FromValue(context.state, context.Arg(index))
	if err != nil {
		// 参数不是函数或 State 无效时返回转换失败。
		return Callable{}, false, err
	}
	return callable, true, nil
}

// PushValue 压入一个 Lua 返回值。
//
// value 可以是任意 Lua 值；返回值会按压入顺序返回给 lua.Call 调用方。
func (context *Context) PushValue(value lua.Value) {
	// 追加到返回值列表，保持 Go 回调多返回值顺序。
	context.results = append(context.results, value)
}

// PushNil 压入 Lua nil 返回值。
//
// 该 helper 用于 Go 回调显式返回 nil。
func (context *Context) PushNil() {
	// nil 返回值没有负载，直接构造 KindNil。
	context.PushValue(lua.Value{Kind: lua.KindNil})
}

// PushBoolean 压入 Lua boolean 返回值。
//
// value 为 true/false 时分别对应 Lua true/false。
func (context *Context) PushBoolean(value bool) {
	// boolean 返回值只设置 Kind 和 Bool 负载。
	context.PushValue(lua.Value{Kind: lua.KindBoolean, Bool: value})
}

// PushInteger 压入 Lua integer 返回值。
//
// value 使用 int64 表达 Lua 5.3 默认整数语义。
func (context *Context) PushInteger(value int64) {
	// integer 返回值保留精确整数，不经过浮点转换。
	context.PushValue(lua.Value{Kind: lua.KindInteger, Integer: value})
}

// PushNumber 压入 Lua float number 返回值。
//
// value 使用 float64 表达 Lua 5.3 默认 lua_Number 语义。
func (context *Context) PushNumber(value float64) {
	// number 返回值保存浮点负载。
	context.PushValue(lua.Value{Kind: lua.KindNumber, Number: value})
}

// PushString 压入 Lua string 返回值。
//
// value 按字节字符串保存，允许包含任意二进制内容。
func (context *Context) PushString(value string) {
	// string 返回值保存 Go 字符串负载。
	context.PushValue(lua.Value{Kind: lua.KindString, String: value})
}

// Results 返回 Go 回调已经压入的 Lua 返回值。
//
// 返回切片是副本，调用方修改该切片不会影响 Context 内部返回值。
func (context *Context) Results() []lua.Value {
	// 复制返回值切片，避免外部修改破坏桥接结果。
	return append([]lua.Value(nil), context.results...)
}

// Call 调用一个已保存的 Lua callable。
//
// callable 可以来自 FromValue、ToCallable 或全局环境；返回值会原样返回给 Go 回调，由调用方决定
// 是否继续 Push 到当前 Context。
func (context *Context) Call(callable Callable, args ...lua.Value) ([]lua.Value, error) {
	// 直接复用 Callable.Call，集中处理 State 和函数类型检查。
	return callable.Call(args...)
}

// CallGlobal 调用当前 State 全局环境中的函数。
//
// name 是全局函数名；args 是 Lua 实参。该 helper 用于 Go 回调内部形成 Go -> Lua -> Go 链路。
func (context *Context) CallGlobal(name string, args ...lua.Value) ([]lua.Value, error) {
	// 复用包级 CallGlobal，确保全局函数读取和错误语义一致。
	return CallGlobal(context.state, name, args...)
}

// CallTableMethod 调用当前 State 中 table 的字段方法。
//
// tableValue 必须是 Lua table；methodName 是 raw string 字段名。调用时自动注入 self。
func (context *Context) CallTableMethod(tableValue lua.Value, methodName string, args ...lua.Value) ([]lua.Value, error) {
	// 复用包级 CallTableMethod，确保 self 注入和错误语义一致。
	return CallTableMethod(context.state, tableValue, methodName, args...)
}

// YieldPolicy 返回当前桥接调用的 yield 策略。
//
// 现阶段统一禁止跨 Go/Lua 边界 yield；后续 coroutine 恢复协议接入后可在 Context 中携带策略。
func (context *Context) YieldPolicy() YieldPolicy {
	// 当前 bridge 没有可恢复 Go 调用帧，必须禁止 yield 穿越。
	return YieldForbidden
}

// Wrap 把 bridge.Function 包装为 lua.Function。
//
// state 用于 context 取消检查和后续跨边界调用；fn 不能为空。包装后的函数会 recover panic，
// 并把 Go error 转换为 Lua RuntimeError。
func Wrap(state *lua.State, fn Function) lua.Function {
	// 返回 lua.Function 以复用 lua.Register 和 lua.Call 的 Go closure 调用路径。
	return func(args ...lua.Value) (results []lua.Value, err error) {
		defer func() {
			// recover 必须位于 Go/Lua 边界，避免 panic 穿透到宿主调用栈。
			if recovered := recover(); recovered != nil {
				err = RecoverPanic(recovered)
				results = nil
			}
		}()
		if fn == nil {
			// nil 回调没有可执行目标，按不可调用错误映射为 Lua runtime error。
			return nil, ErrorFromGo(lua.ErrExpectedCallable)
		}
		if state != nil {
			// 有 State 时先检查 context，避免取消后继续执行 Go 回调副作用。
			if checkErr := state.CheckContext(); checkErr != nil {
				return nil, checkErr
			}
		}

		context := NewContext(state, args...)
		if callErr := fn(context); callErr != nil {
			// Go 回调返回的错误统一映射为 Lua error 对象。
			return nil, ErrorFromGo(callErr)
		}
		return context.Results(), nil
	}
}

// Register 把 bridge.Function 注册为 Lua 全局函数。
//
// state 必须非 nil；name 是全局变量名；fn 不能为空。注册后的函数可通过 lua.GetGlobal 读取并由
// lua.Call 调用，也可供后续 Lua VM 调用路径复用。
func Register(state *lua.State, name string, fn Function) error {
	if state == nil {
		// nil State 没有全局环境，无法注册桥接函数。
		return lua.ErrNilState
	}
	if fn == nil {
		// nil 回调没有可调用目标，返回明确 callable 错误。
		return lua.ErrExpectedCallable
	}
	return lua.Register(state, name, Wrap(state, fn))
}

// RegisterFunction 把 bridge.Function 注册到 Lua 全局环境。
//
// 该入口是统一封装 API 的命名化别名；state 必须非 nil，name 必须非空，fn 必须非 nil。错误语义
// 与 Register 保持一致，注册后的函数可被 Lua 或 Go 侧 CallGlobal 调用。
func RegisterFunction(state *lua.State, name string, fn Function) error {
	if name == "" {
		// 空名称无法形成稳定全局变量，提前返回 Lua error。
		return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "function name is empty"})
	}

	// 复用旧 Register，保持既有全局注册行为和错误链不变。
	return Register(state, name, fn)
}

// RegisterModule 把一组 Go API 注册为 Lua 模块表。
//
// state 必须非 nil；module.Name 必须非空。成功后模块表会写入全局环境，并在 package.loaded
// 可用时同步写入缓存，使 Lua 侧 `require(module.Name)` 可直接返回同一模块表。
func RegisterModule(state *lua.State, module ModuleBinding) (lua.Value, error) {
	if state == nil {
		// nil State 没有全局环境和 package.loaded，无法注册模块。
		return lua.Value{Kind: lua.KindNil}, lua.ErrNilState
	}
	if module.Name == "" {
		// 空模块名无法被全局环境或 package.loaded 稳定索引。
		return lua.Value{Kind: lua.KindNil}, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "module name is empty"})
	}

	moduleValue, err := BuildModule(state, module)
	if err != nil {
		// 模块 table 构造失败时不写入全局或 package.loaded，避免半初始化模块泄漏。
		return lua.Value{Kind: lua.KindNil}, err
	}

	if err := lua.SetGlobal(state, module.Name, moduleValue); err != nil {
		// 全局写入失败时返回错误，调用方可决定是否回滚。
		return lua.Value{Kind: lua.KindNil}, err
	}
	if loadedTable := packageLoadedTable(state); loadedTable != nil {
		// package.loaded 可用时同步写入，使 require 返回同一模块实例。
		loadedTable.RawSetString(module.Name, moduleValue)
	}
	return moduleValue, nil
}

// RegisterModulePreload 把 Go 模块注册到 package.preload。
//
// state 必须非 nil 且已打开 package 标准库；module.Name 必须非空。注册后 Lua 侧
// `require(module.Name)` 会通过 package.searchers[1] 调用 loader，loader 构造模块 table 并由
// require 写入 package.loaded。该路径不立即写全局环境，便于按需加载。
func RegisterModulePreload(state *lua.State, module ModuleBinding) error {
	if state == nil {
		// nil State 没有 package 表，无法注册 preload。
		return lua.ErrNilState
	}
	if module.Name == "" {
		// 空模块名无法被 require 稳定索引。
		return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "module name is empty"})
	}
	preloadTable := packagePreloadTable(state)
	if preloadTable == nil {
		// package.preload 不可用说明 package 标准库尚未打开或被 Lua 侧破坏。
		return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "package.preload is not available"})
	}

	moduleSnapshot := copyModuleBinding(module)
	preloadTable.RawSetString(module.Name, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 每次 require 首次命中时构造模块表，随后 require 会写入 package.loaded 缓存。
		moduleValue, err := BuildModule(state, moduleSnapshot)
		if err != nil {
			// 构造失败应作为 require loader 错误传播。
			return nil, err
		}
		return []runtime.Value{moduleValue}, nil
	})))
	return nil
}

// BuildModule 根据 ModuleBinding 构造 Lua 模块 table。
//
// state 必须非 nil；module.Name 只用于错误文本和嵌套对象生命周期注册。返回值是 table；该函数
// 不写全局环境、package.loaded 或 package.preload，调用方可自行决定注册位置。
func BuildModule(state *lua.State, module ModuleBinding) (lua.Value, error) {
	if state == nil {
		// nil State 无法包装 Go 函数或注册对象 userdata。
		return lua.Value{Kind: lua.KindNil}, lua.ErrNilState
	}

	// 模块本质也是 table binding，复用同一构造路径保证常量、变量和只读策略一致。
	return BuildTable(state, TableBinding{
		Name:      module.Name,
		Fields:    module.Fields,
		Constants: module.Constants,
		Variables: module.Variables,
		Functions: module.Functions,
		Tables:    module.Tables,
		Objects:   module.Objects,
		ReadOnly:  module.ReadOnly,
	})
}

// BuildTable 根据 TableBinding 构造 Lua table。
//
// state 必须非 nil；Fields/Variables 是可变字段，Constants 是只读字段，Functions 会包装为 Go
// closure，Tables 和 Objects 会递归构造。ReadOnly=true 时 Lua 侧所有写入都返回错误。
func BuildTable(state *lua.State, binding TableBinding) (lua.Value, error) {
	if state == nil {
		// nil State 无法包装 Go 函数或注册对象 userdata。
		return lua.Value{Kind: lua.KindNil}, lua.ErrNilState
	}

	targetTable := runtime.NewTable()
	storageTable := targetTable
	if binding.ReadOnly {
		// 只读 table 使用 backing storage，避免 Lua 写入已有 raw key 时绕过 __newindex。
		storageTable = runtime.NewTable()
	}
	constantTable := storageTable
	if !binding.ReadOnly && len(binding.Constants) > 0 {
		// 非整体只读 table 的常量必须放入独立 storage，避免已有 raw key 覆盖绕过 __newindex。
		constantTable = runtime.NewTable()
	}

	if err := fillTableStorage(state, storageTable, binding, binding.ReadOnly); err != nil {
		// 任一字段转换失败都停止构造，避免返回部分可见 table。
		return lua.Value{Kind: lua.KindNil}, err
	}
	if !binding.ReadOnly {
		// 非整体只读 table 的常量通过 __index 暴露，不直接写入公开 table。
		if err := writeAnyFields(state, constantTable, binding.Constants); err != nil {
			// 常量 storage 构造失败时不返回半初始化 table。
			return lua.Value{Kind: lua.KindNil}, err
		}
	}
	if err := installTableMetatable(state, targetTable, storageTable, constantTable, binding); err != nil {
		// 元表安装失败说明宿主提供的元表字段无法转换。
		return lua.Value{Kind: lua.KindNil}, err
	}
	return runtime.ReferenceValue(runtime.KindTable, targetTable), nil
}

// ValueOf 把常见 Go 值转换为 Lua Value。
//
// state 用于包装 bridge.Function；value 支持 nil、bool、string、整数、浮点、lua.Value、
// *runtime.Table、*runtime.Userdata、Function 和 runtime.GoResultsFunction。无法转换时返回 Lua error。
func ValueOf(state *lua.State, value any) (lua.Value, error) {
	switch typedValue := value.(type) {
	case nil:
		// nil 对应 Lua nil。
		return runtime.NilValue(), nil
	case lua.Value:
		// 调用方已提供 Lua Value 时直接复用。
		return typedValue, nil
	case bool:
		// Go bool 映射为 Lua boolean。
		return runtime.BooleanValue(typedValue), nil
	case string:
		// Go string 按 Lua 字节字符串保存。
		return runtime.StringValue(typedValue), nil
	case int:
		// Go int 按当前平台值转换为 Lua integer。
		return runtime.IntegerValue(int64(typedValue)), nil
	case int8:
		// Go int8 转换为 Lua integer。
		return runtime.IntegerValue(int64(typedValue)), nil
	case int16:
		// Go int16 转换为 Lua integer。
		return runtime.IntegerValue(int64(typedValue)), nil
	case int32:
		// Go int32 转换为 Lua integer。
		return runtime.IntegerValue(int64(typedValue)), nil
	case int64:
		// Go int64 与 Lua integer 语义一致。
		return runtime.IntegerValue(typedValue), nil
	case uint:
		// Go uint 超出 int64 时不能安全表达为 Lua integer。
		if uint64(typedValue) > uint64(^uint64(0)>>1) {
			// 超出 Lua integer 范围时提前失败，避免符号回绕。
			return lua.Value{Kind: lua.KindNil}, lua.RaiseError(runtime.StringValue("uint value overflows lua integer"))
		}
		return runtime.IntegerValue(int64(typedValue)), nil
	case uint8:
		// Go uint8 总能安全转换为 Lua integer。
		return runtime.IntegerValue(int64(typedValue)), nil
	case uint16:
		// Go uint16 总能安全转换为 Lua integer。
		return runtime.IntegerValue(int64(typedValue)), nil
	case uint32:
		// Go uint32 总能安全转换为 Lua integer。
		return runtime.IntegerValue(int64(typedValue)), nil
	case uint64:
		// Go uint64 超出 int64 时不能安全表达为 Lua integer。
		if typedValue > uint64(^uint64(0)>>1) {
			// 超出 Lua integer 范围时提前失败，避免符号回绕。
			return lua.Value{Kind: lua.KindNil}, lua.RaiseError(runtime.StringValue("uint64 value overflows lua integer"))
		}
		return runtime.IntegerValue(int64(typedValue)), nil
	case float32:
		// Go float32 扩展为 Lua number。
		return runtime.NumberValue(float64(typedValue)), nil
	case float64:
		// Go float64 与 Lua number 语义一致。
		return runtime.NumberValue(typedValue), nil
	case Function:
		// bridge.Function 需要绑定 State 才能获得 context、panic 和错误传播语义。
		if state == nil {
			// nil State 无法包装 bridge.Function。
			return lua.Value{Kind: lua.KindNil}, lua.ErrNilState
		}
		return runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Wrap(state, typedValue))), nil
	case runtime.GoResultsFunction:
		// runtime.GoResultsFunction 已符合 Lua Go closure 调用约定。
		if typedValue == nil {
			// nil 函数不能作为 Lua callable。
			return lua.Value{Kind: lua.KindNil}, lua.ErrExpectedCallable
		}
		return runtime.ReferenceValue(runtime.KindGoClosure, typedValue), nil
	case *runtime.Table:
		// runtime.Table 指针按 Lua table 引用暴露。
		if typedValue == nil {
			// nil table 指针没有可暴露对象。
			return lua.Value{Kind: lua.KindNil}, lua.RaiseError(runtime.StringValue("nil table value"))
		}
		return runtime.ReferenceValue(runtime.KindTable, typedValue), nil
	case *runtime.Userdata:
		// runtime.Userdata 指针按 Lua userdata 引用暴露。
		if typedValue == nil {
			// nil userdata 指针没有可暴露对象。
			return lua.Value{Kind: lua.KindNil}, lua.RaiseError(runtime.StringValue("nil userdata value"))
		}
		if state != nil {
			// 有 State 时纳入关闭路径；重复注册由 runtime 去重。
			if err := state.RegisterUserdata(typedValue); err != nil {
				// userdata 注册失败时不能返回未纳入关闭路径的值。
				return lua.Value{Kind: lua.KindNil}, err
			}
		}
		return typedValue.Value(), nil
	default:
		// 未列入的 Go 类型必须显式转换或通过 ObjectBinding 暴露，避免隐式反射扩散。
		return lua.Value{Kind: lua.KindNil}, lua.RaiseError(runtime.StringValue("unsupported bridge value type"))
	}
}

// GenerateLuaStub 根据 Go 模块绑定生成 Lua 代理代码。
//
// stub 不是 Go 源码反编译结果，而是 Lua 侧可读的代理层；函数和对象方法通过
// `__go_bridge_call` 占位入口转发到宿主 bridge。
func GenerateLuaStub(module ModuleBinding) (string, error) {
	if module.Name == "" {
		// 空模块名无法生成稳定的 require 代理注释。
		return "", lua.RaiseError(lua.Value{Kind: lua.KindString, String: "module name is empty"})
	}

	// 使用 strings.Builder 逐段输出，保证 stub 可读且顺序稳定。
	var builder strings.Builder
	builder.WriteString("-- Code generated by go-lua-vm bridge. DO NOT EDIT.\n")
	builder.WriteString("-- Module: ")
	builder.WriteString(module.Name)
	builder.WriteString("\n\n")
	builder.WriteString("local M = {}\n\n")

	for _, functionName := range sortedFunctionNames(module.Functions) {
		// 模块函数通过统一 bridge call 占位入口转发。
		builder.WriteString("function M.")
		builder.WriteString(luaIdentifier(functionName))
		builder.WriteString("(...)\n")
		builder.WriteString("  return __go_bridge_call(\"")
		builder.WriteString(luaStringContent(module.Name + "." + functionName))
		builder.WriteString("\", ...)\n")
		builder.WriteString("end\n\n")
	}
	for _, objectName := range sortedObjectNames(module.Objects) {
		// 对象 stub 使用 table 表示，属性和方法都转发到 bridge 占位入口。
		objectBinding := module.Objects[objectName]
		builder.WriteString("M.")
		builder.WriteString(luaIdentifier(objectName))
		builder.WriteString(" = setmetatable({}, {\n")
		builder.WriteString("  __index = function(_, key)\n")
		builder.WriteString("    return __go_bridge_property(\"")
		builder.WriteString(luaStringContent(module.Name + "." + objectName))
		builder.WriteString("\", key)\n")
		builder.WriteString("  end,\n")
		builder.WriteString("  __newindex = function(_, key, value)\n")
		builder.WriteString("    return __go_bridge_set_property(\"")
		builder.WriteString(luaStringContent(module.Name + "." + objectName))
		builder.WriteString("\", key, value)\n")
		builder.WriteString("  end,\n")
		builder.WriteString("})\n")
		for _, methodName := range sortedObjectMethodNames(objectBinding.Methods) {
			// 对象方法使用冒号形式保留 Lua self 调用风格。
			builder.WriteString("function M.")
			builder.WriteString(luaIdentifier(objectName))
			builder.WriteString(":")
			builder.WriteString(luaIdentifier(methodName))
			builder.WriteString("(...)\n")
			builder.WriteString("  return __go_bridge_call(\"")
			builder.WriteString(luaStringContent(module.Name + "." + objectName + ":" + methodName))
			builder.WriteString("\", self, ...)\n")
			builder.WriteString("end\n")
		}
		builder.WriteString("\n")
	}
	builder.WriteString("return M\n")
	return builder.String(), nil
}

// ErrorFromGo 把 Go error 映射为 Lua RuntimeError。
//
// err 为 nil 时返回 nil；已有 RuntimeError 或资源限制错误保持错误链；普通 Go error 会转换为
// Lua string error object，并保留原始错误供 errors.Is/errors.As 判断。
func ErrorFromGo(err error) error {
	// runtime.LuaErrorFromGo 已经实现统一错误对象包装。
	return runtime.LuaErrorFromGo(err)
}

// RecoverPanic 把 Go panic 值映射为 Lua RuntimeError。
//
// recovered 必须来自 recover；nil 表示没有 panic 并返回 nil。返回错误链携带 panic 文本，Lua
// error object 保存 panic 的字符串形式。
func RecoverPanic(recovered any) error {
	// runtime.PanicToError 已经实现 panic 到 RuntimeError 的兼容转换。
	return runtime.PanicToError(recovered)
}

// Object 返回当前对象方法绑定的 Go 对象。
//
// 返回值可能是任意 Go 指针或值；调用方需要按 ObjectBinding.Object 的实际类型自行断言。
func (context *ObjectContext) Object() any {
	// 只暴露绑定对象引用，不允许从 Context 修改绑定表本身。
	return context.object
}

// BindStruct 把显式 Go 对象绑定为 Lua 代理表。
//
// state 必须非 nil；binding.Object 必须非 nil；Methods、Getters、Setters 只暴露显式声明成员。
// 返回值是 Lua table，内部通过隐藏 userdata 保留 Go object identity，并通过元表转发访问。
func BindStruct(state *lua.State, binding ObjectBinding) (lua.Value, error) {
	if state == nil {
		// nil State 无法注册 userdata 生命周期，必须提前拒绝。
		return lua.Value{Kind: lua.KindNil}, lua.ErrNilState
	}
	if binding.Object == nil {
		// nil 对象没有可代理 identity，返回 Lua error 便于宿主识别绑定错误。
		return lua.Value{Kind: lua.KindNil}, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "nil object binding"})
	}

	// 创建代理并把 userdata 纳入 State 生命周期管理。
	proxy := newObjectProxy(binding)
	if err := state.RegisterUserdata(proxy.userdata); err != nil {
		// userdata 注册失败时不返回半初始化代理，避免关闭路径遗漏。
		return lua.Value{Kind: lua.KindNil}, err
	}
	return proxy.Value(), nil
}

// newObjectProxy 创建 Go 对象的 Lua table/userdata 双载体代理。
//
// binding 会被复制，避免调用方后续修改 map 影响 Lua 侧已暴露成员；返回代理尚未注册到 State。
func newObjectProxy(binding ObjectBinding) *ObjectProxy {
	// 先复制绑定配置，确保代理持有稳定的显式成员快照。
	proxy := &ObjectProxy{binding: copyObjectBinding(binding)}
	if proxy.binding.Finalizer != nil {
		// 有显式关闭回调时，userdata 在 State.Close 阶段负责触发对象生命周期关闭。
		proxy.userdata = runtime.NewUserdataWithFinalizer(proxy, func(payload any) error {
			payloadProxy, ok := payload.(*ObjectProxy)
			if !ok || payloadProxy == nil {
				// payload 异常时没有可关闭对象，直接跳过避免关闭路径 panic。
				return nil
			}
			return payloadProxy.binding.Finalizer(payloadProxy.binding.Object)
		})
	} else {
		// 无关闭回调时只需要 userdata identity，不注册额外资源释放语义。
		proxy.userdata = runtime.NewUserdata(proxy)
	}
	proxy.table = runtime.NewTable()
	proxy.table.RawSetString("__userdata", proxy.userdata.Value())

	// 元表通过 Go closure 实现 __index/__newindex，保持属性和方法策略集中在代理对象。
	metatable := runtime.NewTable()
	metatable.RawSetString("__index", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(proxy.index)))
	metatable.RawSetString("__newindex", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(proxy.newIndex)))
	proxy.table.SetMetatable(metatable)
	return proxy
}

// Value 返回代理对 Lua 侧公开的 table 值。
//
// 该 table 挂载 __index 和 __newindex 元方法，Lua 侧通常只需要持有此值。
func (proxy *ObjectProxy) Value() lua.Value {
	if proxy == nil || proxy.table == nil {
		// nil 代理没有 Lua 侧对象，返回 nil 避免调用方 panic。
		return lua.Value{Kind: lua.KindNil}
	}
	return runtime.ReferenceValue(runtime.KindTable, proxy.table)
}

// UserdataValue 返回代理内部保存 Go identity 的 userdata 值。
//
// 该值主要供测试、调试和后续 stub/GC 逻辑使用，普通 Lua 代码不应依赖隐藏字段名。
func (proxy *ObjectProxy) UserdataValue() lua.Value {
	if proxy == nil || proxy.userdata == nil {
		// nil 代理没有 userdata identity，返回 nil。
		return lua.Value{Kind: lua.KindNil}
	}
	return proxy.userdata.Value()
}

// Object 返回代理绑定的原始 Go 对象。
//
// 返回值保持 ObjectBinding.Object 的原始引用，用于 Go 侧回查 identity。
func (proxy *ObjectProxy) Object() any {
	if proxy == nil {
		// nil 代理没有可返回对象。
		return nil
	}
	return proxy.binding.Object
}

// GetProperty 按显式 getter 策略读取代理属性。
//
// name 是 Lua 侧 string key；返回 found=false 表示没有对应属性，错误表示 getter 执行失败。
func (proxy *ObjectProxy) GetProperty(name string) (lua.Value, bool, error) {
	if proxy == nil {
		// nil 代理没有属性表，按未命中处理。
		return lua.Value{Kind: lua.KindNil}, false, nil
	}
	getter, ok := proxy.binding.Getters[name]
	if !ok {
		// 未声明 getter 的属性不能被读取。
		return lua.Value{Kind: lua.KindNil}, false, nil
	}
	value, err := getter(proxy.binding.Object)
	if err != nil {
		// getter 错误向 Lua 侧传播为运行期错误。
		return lua.Value{Kind: lua.KindNil}, true, ErrorFromGo(err)
	}
	return value, true, nil
}

// SetProperty 按显式 setter 策略写入代理属性。
//
// name 是 Lua 侧 string key；found=false 表示没有对应 setter，错误表示 setter 执行失败。
func (proxy *ObjectProxy) SetProperty(name string, value lua.Value) (bool, error) {
	if proxy == nil {
		// nil 代理没有属性表，按未命中处理。
		return false, nil
	}
	setter, ok := proxy.binding.Setters[name]
	if !ok {
		// 未声明 setter 的属性不能被写入。
		return false, nil
	}
	if err := setter(proxy.binding.Object, value); err != nil {
		// setter 错误向 Lua 侧传播为运行期错误。
		return true, ErrorFromGo(err)
	}
	return true, nil
}

// MethodValue 返回显式绑定方法对应的 Lua Go closure。
//
// name 是 Lua 侧 string key；found=false 表示没有对应方法。
func (proxy *ObjectProxy) MethodValue(name string) (lua.Value, bool) {
	if proxy == nil {
		// nil 代理没有方法表，按未命中处理。
		return lua.Value{Kind: lua.KindNil}, false
	}
	method, ok := proxy.binding.Methods[name]
	if !ok {
		// 未声明方法不能被 Lua 调用。
		return lua.Value{Kind: lua.KindNil}, false
	}
	return runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(proxy.wrapObjectMethod(method))), true
}

// index 实现代理 table 的 __index 元方法。
//
// Lua 5.3 以 `(self, key)` 调用 function 形式 __index；方法优先于 getter，未命中返回 nil。
func (proxy *ObjectProxy) index(args ...lua.Value) ([]lua.Value, error) {
	if len(args) < 2 || args[1].Kind != lua.KindString {
		// 缺少 string key 时按 Lua 未命中处理，返回 nil。
		return []lua.Value{{Kind: lua.KindNil}}, nil
	}

	// 方法优先返回 closure，支持 `object:method(...)` 的 self 调用形式。
	key := args[1].String
	if methodValue, ok := proxy.MethodValue(key); ok {
		// 命中方法时返回可调用 Go closure。
		return []lua.Value{methodValue}, nil
	}
	propertyValue, ok, err := proxy.GetProperty(key)
	if err != nil {
		// getter 执行失败时中断读取路径。
		return nil, err
	}
	if ok {
		// 命中属性时返回 getter 结果。
		return []lua.Value{propertyValue}, nil
	}
	return []lua.Value{{Kind: lua.KindNil}}, nil
}

// newIndex 实现代理 table 的 __newindex 元方法。
//
// Lua 5.3 以 `(self, key, value)` 调用 function 形式 __newindex；只允许写入显式 setter。
func (proxy *ObjectProxy) newIndex(args ...lua.Value) ([]lua.Value, error) {
	if len(args) < 3 || args[1].Kind != lua.KindString {
		// 缺少 string key 或 value 时返回明确错误，避免静默丢失写入。
		return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "string property key expected"})
	}

	// 只把声明过 setter 的属性写回 Go 对象。
	found, err := proxy.SetProperty(args[1].String, args[2])
	if err != nil {
		// setter 执行失败时向上传播错误。
		return nil, err
	}
	if !found {
		// 未声明 setter 的属性保持只读/不可见，避免 Lua 侧扩展代理表污染 Go 对象语义。
		return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "unknown writable property: " + args[1].String})
	}
	return nil, nil
}

// wrapObjectMethod 把显式对象方法包装为 Lua Go closure。
//
// method 不能为空；Lua 冒号调用会传入 self，包装器会移除首个代理 self 后再交给 ObjectContext。
func (proxy *ObjectProxy) wrapObjectMethod(method ObjectMethod) runtime.GoResultsFunction {
	// 返回 Go closure，使 runtime.Table 的 __index 结果可直接通过 lua.Call 执行。
	return func(args ...lua.Value) (results []lua.Value, err error) {
		defer func() {
			// 对象方法是 Go/Lua 边界，同样需要 recover panic。
			if recovered := recover(); recovered != nil {
				results = nil
				err = RecoverPanic(recovered)
			}
		}()
		if method == nil {
			// nil 方法没有可执行目标，按不可调用错误映射。
			return nil, ErrorFromGo(lua.ErrExpectedCallable)
		}

		// Lua 冒号调用会把代理 table 作为第一个参数，Go 方法不需要重复看到 self。
		methodArgs := args
		if len(methodArgs) > 0 && methodArgs[0].Kind == lua.KindTable && proxy != nil && methodArgs[0].Ref == proxy.table {
			// 命中当前代理 self 时移除首参，剩余参数才是业务参数。
			methodArgs = methodArgs[1:]
		}
		context := &ObjectContext{
			Context: NewContext(nil, methodArgs...),
			object:  proxy.Object(),
		}
		if callErr := method(context); callErr != nil {
			// 对象方法返回错误时统一映射为 Lua error。
			return nil, ErrorFromGo(callErr)
		}
		return context.Results(), nil
	}
}

// copyObjectBinding 复制对象绑定配置。
//
// Methods、Getters、Setters 的 map 会复制一层；函数值和 Object 引用保持原始语义。
func copyObjectBinding(binding ObjectBinding) ObjectBinding {
	// 复制基础字段，后续分别复制显式成员 map。
	copiedBinding := ObjectBinding{
		Name:      binding.Name,
		Object:    binding.Object,
		Finalizer: binding.Finalizer,
	}
	if binding.Methods != nil {
		// 复制方法表，避免调用方后续增删影响代理。
		copiedBinding.Methods = make(map[string]ObjectMethod, len(binding.Methods))
		for name, method := range binding.Methods {
			// 方法名和值按原样复制。
			copiedBinding.Methods[name] = method
		}
	}
	if binding.Getters != nil {
		// 复制 getter 表，避免调用方后续增删影响代理。
		copiedBinding.Getters = make(map[string]PropertyGetter, len(binding.Getters))
		for name, getter := range binding.Getters {
			// 属性名和值按原样复制。
			copiedBinding.Getters[name] = getter
		}
	}
	if binding.Setters != nil {
		// 复制 setter 表，避免调用方后续增删影响代理。
		copiedBinding.Setters = make(map[string]PropertySetter, len(binding.Setters))
		for name, setter := range binding.Setters {
			// 属性名和值按原样复制。
			copiedBinding.Setters[name] = setter
		}
	}
	return copiedBinding
}

// copyModuleBinding 复制模块绑定配置。
//
// map 字段会复制一层；函数、对象引用和字段值保持原始语义，用于 package.preload 延迟构造时避免
// 调用方后续增删字段影响已注册 loader。
func copyModuleBinding(binding ModuleBinding) ModuleBinding {
	// 复制基础字段，后续分别复制显式成员 map。
	return ModuleBinding{
		Name:      binding.Name,
		Fields:    copyAnyMap(binding.Fields),
		Constants: copyAnyMap(binding.Constants),
		Variables: copyAnyMap(binding.Variables),
		Functions: copyFunctionMap(binding.Functions),
		Tables:    copyTableBindingMap(binding.Tables),
		Objects:   copyObjectBindingMap(binding.Objects),
		ReadOnly:  binding.ReadOnly,
	}
}

// copyTableBinding 复制 table 绑定配置。
//
// 嵌套 table 和对象绑定会继续复制一层，避免延迟构造或递归构造时读取被调用方改动的 map。
func copyTableBinding(binding TableBinding) TableBinding {
	// 复制基础字段，后续分别复制显式成员 map。
	return TableBinding{
		Name:      binding.Name,
		Fields:    copyAnyMap(binding.Fields),
		Constants: copyAnyMap(binding.Constants),
		Variables: copyAnyMap(binding.Variables),
		Functions: copyFunctionMap(binding.Functions),
		Tables:    copyTableBindingMap(binding.Tables),
		Objects:   copyObjectBindingMap(binding.Objects),
		Metatable: copyAnyMap(binding.Metatable),
		ReadOnly:  binding.ReadOnly,
	}
}

// copyAnyMap 复制 string 到 any 的字段 map。
//
// value 本身保持引用语义；该 helper 只隔离 map 结构的后续增删。
func copyAnyMap(values map[string]any) map[string]any {
	if values == nil {
		// nil map 保持 nil，避免无意义分配。
		return nil
	}
	copiedValues := make(map[string]any, len(values))
	for name, value := range values {
		// 字段值按原样复制。
		copiedValues[name] = value
	}
	return copiedValues
}

// copyFunctionMap 复制模块或 table 函数 map。
//
// 函数值保持原始引用语义；该 helper 只隔离 map 结构的后续增删。
func copyFunctionMap(functions map[string]Function) map[string]Function {
	if functions == nil {
		// nil map 保持 nil，避免无意义分配。
		return nil
	}
	copiedFunctions := make(map[string]Function, len(functions))
	for name, function := range functions {
		// 函数值按原样复制。
		copiedFunctions[name] = function
	}
	return copiedFunctions
}

// copyTableBindingMap 复制嵌套 table 绑定 map。
//
// 每个 TableBinding 递归复制，避免 package.preload 延迟 loader 观察到调用方后续 map 改动。
func copyTableBindingMap(tables map[string]TableBinding) map[string]TableBinding {
	if tables == nil {
		// nil map 保持 nil，避免无意义分配。
		return nil
	}
	copiedTables := make(map[string]TableBinding, len(tables))
	for name, tableBinding := range tables {
		// 嵌套 table 需要递归复制。
		copiedTables[name] = copyTableBinding(tableBinding)
	}
	return copiedTables
}

// copyObjectBindingMap 复制对象绑定 map。
//
// 每个 ObjectBinding 复制一层显式成员 map；Object 引用本身保持原始 identity。
func copyObjectBindingMap(objects map[string]ObjectBinding) map[string]ObjectBinding {
	if objects == nil {
		// nil map 保持 nil，避免无意义分配。
		return nil
	}
	copiedObjects := make(map[string]ObjectBinding, len(objects))
	for name, objectBinding := range objects {
		// 对象绑定需要复制成员 map，避免调用方后续增删影响代理。
		copiedObjects[name] = copyObjectBinding(objectBinding)
	}
	return copiedObjects
}

// FromValue 把 Lua 函数值保存为 Go callable。
//
// state 必须非 nil；value 必须是 Lua closure 或 Go closure。返回的 Callable 可跨 Go 代码保存，
// 后续通过 Call 重新进入 lua.Call。
func FromValue(state *lua.State, value lua.Value) (Callable, error) {
	if state == nil {
		// nil State 无法作为后续调用上下文。
		return Callable{}, lua.ErrNilState
	}
	switch value.Kind {
	case lua.KindGoClosure, lua.KindLuaClosure:
		// Go closure 当前可执行；Lua closure 保存后会在 Call 阶段返回执行器未接入错误。
		return Callable{state: state, value: value}, nil
	default:
		// 非函数值不能保存为 callable。
		return Callable{}, lua.ErrExpectedCallable
	}
}

// Call 调用已保存的 Lua 函数值。
//
// args 是 Lua 实参列表；返回值按 Lua 多返回值顺序排列。Go closure 当前可执行，Lua closure 在
// Proto 执行器接入前返回 lua.ErrExecutionUnavailable。
func (callable Callable) Call(args ...lua.Value) ([]lua.Value, error) {
	if callable.state == nil {
		// 缺少 State 时无法建立调用上下文。
		return nil, lua.ErrNilState
	}
	if callable.value.Kind != lua.KindGoClosure && callable.value.Kind != lua.KindLuaClosure {
		// Callable 被零值或非法值构造时，按不可调用错误返回。
		return nil, lua.ErrExpectedCallable
	}
	return lua.Call(callable.state, callable.value, args...)
}

// Value 返回 Callable 保存的原始 Lua 函数值。
//
// 调用方可用该值重新写入全局环境或表字段；返回值是 Value 副本。
func (callable Callable) Value() lua.Value {
	// Value 是结构体值，直接返回不会泄露可变内部状态。
	return callable.value
}

// CallGlobal 调用全局环境中的 Lua 函数。
//
// state 必须非 nil；name 是全局变量名；args 是 Lua 实参列表。全局值必须是函数，否则返回
// lua.ErrExpectedCallable。
func CallGlobal(state *lua.State, name string, args ...lua.Value) ([]lua.Value, error) {
	if state == nil {
		// nil State 没有全局环境，无法读取函数。
		return nil, lua.ErrNilState
	}
	functionValue, err := lua.GetGlobal(state, name)
	if err != nil {
		// 读取全局函数失败时直接返回底层错误。
		return nil, err
	}
	callable, err := FromValue(state, functionValue)
	if err != nil {
		// 全局值不是函数时返回不可调用错误。
		return nil, err
	}
	return callable.Call(args...)
}

// CallTableMethod 调用 table 上的 string 字段方法。
//
// state 必须非 nil；tableValue 必须是 Lua table；methodName 是 raw string 字段名。调用时会把
// tableValue 作为第一个参数传入，模拟 Lua 冒号调用 `table:method(...)` 的 self 语义。
func CallTableMethod(state *lua.State, tableValue lua.Value, methodName string, args ...lua.Value) ([]lua.Value, error) {
	if state == nil {
		// nil State 无法建立方法调用上下文。
		return nil, lua.ErrNilState
	}
	tableObject, err := tableFromValue(tableValue)
	if err != nil {
		// 非 table 值不能执行 table method 调用。
		return nil, err
	}
	methodValue := tableObject.RawGetString(methodName)
	callable, err := FromValue(state, methodValue)
	if err != nil {
		// 字段不存在或不是函数时返回不可调用错误。
		return nil, err
	}
	methodArgs := append([]lua.Value{tableValue}, args...)
	return callable.Call(methodArgs...)
}

// tableFromValue 从 Lua Value 中提取 runtime.Table。
//
// value 必须是 KindTable 且 Ref 保存 *runtime.Table；否则返回 Lua 运行期错误，供 table method
// 调用路径统一向上传播。
func tableFromValue(value lua.Value) (*runtime.Table, error) {
	if value.Kind != lua.KindTable {
		// 非 table 值不能按 table method 读取字段。
		return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "table expected"})
	}
	tableObject, ok := value.Ref.(*runtime.Table)
	if !ok || tableObject == nil {
		// table 引用负载损坏时返回明确错误，避免 panic。
		return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "invalid table reference"})
	}
	return tableObject, nil
}

// fillTableStorage 把 TableBinding 的显式成员写入目标 storage table。
//
// storage 必须非 nil；该函数只做 raw 写入，不安装元表。includeConstants 控制常量是否写入同一
// storage：整体只读 table 需要写入，普通 table 则必须把常量放在独立 storage。
func fillTableStorage(state *lua.State, storage *runtime.Table, binding TableBinding, includeConstants bool) error {
	if storage == nil {
		// storage 缺失说明构造流程损坏，返回明确 Lua error。
		return lua.RaiseError(runtime.StringValue("nil table storage"))
	}

	if err := writeAnyFields(state, storage, binding.Fields); err != nil {
		// 普通字段转换失败时停止构造。
		return err
	}
	if err := writeAnyFields(state, storage, binding.Variables); err != nil {
		// 变量字段转换失败时停止构造。
		return err
	}
	if includeConstants {
		// 整体只读 table 的公开 table 没有 raw 字段，因此常量可以安全放入 backing storage。
		if err := writeAnyFields(state, storage, binding.Constants); err != nil {
			// 常量字段转换失败时停止构造。
			return err
		}
	}
	for _, functionName := range sortedFunctionNames(binding.Functions) {
		// 每个函数都通过 Wrap 捕获当前 State，确保 context 取消和 panic/error 映射一致。
		storage.RawSetString(functionName, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Wrap(state, binding.Functions[functionName]))))
	}
	for _, tableName := range sortedTableNames(binding.Tables) {
		// 嵌套 table 递归构造，继承统一字段、常量和只读策略。
		tableValue, err := BuildTable(state, binding.Tables[tableName])
		if err != nil {
			// 任一嵌套 table 构造失败时停止构造。
			return err
		}
		storage.RawSetString(tableName, tableValue)
	}
	for _, objectName := range sortedObjectNames(binding.Objects) {
		// 对象绑定复用 BindStruct，确保 userdata 生命周期和元方法策略一致。
		objectValue, err := BindStruct(state, binding.Objects[objectName])
		if err != nil {
			// 任一对象绑定失败时停止注册，避免返回半初始化 table。
			return err
		}
		storage.RawSetString(objectName, objectValue)
	}
	return nil
}

// writeAnyFields 把 map 字段按稳定 key 顺序转换并写入 table。
//
// fields 的 value 支持 ValueOf 认可的类型；nil map 表示无字段。
func writeAnyFields(state *lua.State, table *runtime.Table, fields map[string]any) error {
	for _, fieldName := range sortedAnyNames(fields) {
		// 字段值先转换为 Lua Value，再写入 raw table。
		fieldValue, err := ValueOf(state, fields[fieldName])
		if err != nil {
			// 转换失败时返回底层错误，调用方负责附加上下文。
			return err
		}
		table.RawSetString(fieldName, fieldValue)
	}
	return nil
}

// installTableMetatable 为构造出的 table 合并宿主元表、常量保护和只读保护。
//
// target 是 Lua 侧公开 table；storage 是只读 backing storage；constants 是常量 backing storage。
// ReadOnly=true 时 target 与 storage 不同，所有读取通过 __index 到 storage，所有写入被拒绝。
func installTableMetatable(state *lua.State, target *runtime.Table, storage *runtime.Table, constants *runtime.Table, binding TableBinding) error {
	if target == nil || storage == nil || constants == nil {
		// 缺少 table 说明构造流程损坏，返回明确错误。
		return lua.RaiseError(runtime.StringValue("nil table metatable target"))
	}

	metatable := runtime.NewTable()
	if err := writeAnyFields(state, metatable, binding.Metatable); err != nil {
		// 宿主元表字段转换失败时停止构造。
		return err
	}
	originalIndex := metatable.RawGetString("__index")
	originalNewIndex := metatable.RawGetString("__newindex")
	if binding.ReadOnly || len(binding.Constants) > 0 {
		// 只读或常量 table 需要通过元方法拦截普通 Lua 读写。
		metatable.RawSetString("__index", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			return tableIndexWithStorage(state, storage, constants, binding, originalIndex, args...)
		})))
		metatable.RawSetString("__newindex", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
			return tableNewIndexWithGuards(state, binding, originalNewIndex, args...)
		})))
		metatable.RawSetString("__metatable", runtime.StringValue("protected bridge table"))
	}
	if len(binding.Metatable) > 0 || metatableHasFields(metatable) {
		// 只有存在宿主元表字段或保护元方法时才安装元表。
		target.SetMetatable(metatable)
	}
	return nil
}

// tableIndexWithStorage 实现只读 table 和常量字段的 __index。
//
// storage 保存只读 table 的真实字段；constants 保存常量字段；originalIndex 是宿主提供的原始
// __index，未命中 bridge storage 时会作为 fallback 使用。
func tableIndexWithStorage(state *lua.State, storage *runtime.Table, constants *runtime.Table, binding TableBinding, originalIndex lua.Value, args ...lua.Value) ([]lua.Value, error) {
	if len(args) < 2 {
		// 缺少 key 时按 Lua 未命中读取处理。
		return []lua.Value{runtime.NilValue()}, nil
	}

	constantValue, err := constants.RawGet(args[1])
	if err != nil {
		// 常量 storage 读取失败通常来自非法 key，直接传播。
		return nil, err
	}
	if !constantValue.IsNil() {
		// 命中常量 storage 时返回字段值。
		return []lua.Value{constantValue}, nil
	}
	if binding.ReadOnly {
		// 整体只读 table 的所有字段都存放在 backing storage 中。
		storedValue, err := storage.RawGet(args[1])
		if err != nil {
			// storage 读取失败通常来自非法 key，直接传播。
			return nil, err
		}
		if !storedValue.IsNil() {
			// 命中 storage 时返回字段值。
			return []lua.Value{storedValue}, nil
		}
	}
	// 未命中 bridge storage 时继续走宿主 fallback。
	return callIndexFallback(state, originalIndex, args...)
}

// tableNewIndexWithGuards 实现只读 table 和常量字段的 __newindex。
//
// binding.ReadOnly 为 true 时所有写入失败；否则仅 Constants 中声明的 string key 被拒绝，其余写入
// 交给宿主原始 __newindex 或 raw 写入 receiver table。
func tableNewIndexWithGuards(state *lua.State, binding TableBinding, originalNewIndex lua.Value, args ...lua.Value) ([]lua.Value, error) {
	if len(args) < 3 {
		// __newindex 缺少 key/value 时返回明确错误，避免静默忽略写入。
		return nil, lua.RaiseError(runtime.StringValue("table write expects self, key and value"))
	}
	if binding.ReadOnly {
		// 只读 table 拒绝所有 Lua 侧写入。
		return nil, lua.RaiseError(runtime.StringValue(readOnlyTableMessage(binding.Name)))
	}
	if args[1].Kind == lua.KindString {
		// string key 才能匹配 Constants 声明的只读字段。
		if _, ok := binding.Constants[args[1].String]; ok {
			// 常量字段拒绝覆盖，变量字段仍允许普通写入。
			return nil, lua.RaiseError(runtime.StringValue("cannot modify constant: " + args[1].String))
		}
	}
	if !originalNewIndex.IsNil() {
		// 宿主提供 __newindex 时优先保持宿主元方法语义。
		return callNewIndexFallback(state, originalNewIndex, args...)
	}

	receiverTable, err := tableFromValue(args[0])
	if err != nil {
		// receiver 不是 table 时无法 raw 写入。
		return nil, err
	}
	if err := receiverTable.RawSet(args[1], args[2]); err != nil {
		// raw 写入失败时向 Lua 侧传播 table key 错误。
		return nil, err
	}
	return nil, nil
}

// callIndexFallback 执行宿主提供的 __index fallback。
//
// fallback 可以是 nil、table 或 Go/Lua closure；closure 会按 `(self, key)` 参数执行。
func callIndexFallback(state *lua.State, fallback lua.Value, args ...lua.Value) ([]lua.Value, error) {
	if fallback.IsNil() {
		// 没有 fallback 时按 Lua 未命中返回 nil。
		return []lua.Value{runtime.NilValue()}, nil
	}
	if fallback.Kind == lua.KindTable {
		// table fallback 按 raw get 读取 key。
		fallbackTable, err := tableFromValue(fallback)
		if err != nil {
			// fallback table 引用损坏时传播错误。
			return nil, err
		}
		if len(args) < 2 {
			// 缺少 key 时 fallback 也只能返回 nil。
			return []lua.Value{runtime.NilValue()}, nil
		}
		value, err := fallbackTable.RawGet(args[1])
		if err != nil {
			// fallback table raw get 失败时传播错误。
			return nil, err
		}
		return []lua.Value{value}, nil
	}
	if fallback.Kind == lua.KindGoClosure || fallback.Kind == lua.KindLuaClosure {
		// 函数 fallback 按 Lua __index 调用约定执行。
		return lua.Call(state, fallback, args...)
	}

	// 不支持的 fallback 类型按未命中处理，避免破坏已有 table 构造。
	return []lua.Value{runtime.NilValue()}, nil
}

// callNewIndexFallback 执行宿主提供的 __newindex fallback。
//
// fallback 可以是 table 或 Go/Lua closure；table fallback 使用 raw set，closure 按 `(self,key,value)` 执行。
func callNewIndexFallback(state *lua.State, fallback lua.Value, args ...lua.Value) ([]lua.Value, error) {
	if fallback.Kind == lua.KindTable {
		// table fallback 按 raw set 写入 key/value。
		fallbackTable, err := tableFromValue(fallback)
		if err != nil {
			// fallback table 引用损坏时传播错误。
			return nil, err
		}
		if err := fallbackTable.RawSet(args[1], args[2]); err != nil {
			// raw set 失败时传播 key 错误。
			return nil, err
		}
		return nil, nil
	}
	if fallback.Kind == lua.KindGoClosure || fallback.Kind == lua.KindLuaClosure {
		// 函数 fallback 按 Lua __newindex 调用约定执行。
		return lua.Call(state, fallback, args...)
	}
	return nil, lua.RaiseError(runtime.StringValue("unsupported __newindex fallback"))
}

// metatableHasFields 判断元表是否需要安装。
//
// 当前 Table 没有公开长度 API，这里通过检查常用保护字段和宿主传入 map 是否为空来决定。
func metatableHasFields(metatable *runtime.Table) bool {
	if metatable == nil {
		// nil 元表没有字段。
		return false
	}
	if !metatable.RawGetString("__index").IsNil() {
		// 存在 __index 时需要安装元表。
		return true
	}
	if !metatable.RawGetString("__newindex").IsNil() {
		// 存在 __newindex 时需要安装元表。
		return true
	}
	if !metatable.RawGetString("__metatable").IsNil() {
		// 存在 __metatable 保护字段时需要安装元表。
		return true
	}
	return false
}

// readOnlyTableMessage 返回只读 table 写入错误文本。
//
// name 为空时使用通用 table 名称，避免错误信息出现空对象名。
func readOnlyTableMessage(name string) string {
	if name == "" {
		// 无调试名称时返回通用错误。
		return "cannot modify read-only table"
	}
	return "cannot modify read-only table: " + name
}

// packageLoadedTable 从 State 中读取 package.loaded 表。
//
// package 标准库未打开或 package.loaded 类型不匹配时返回 nil；调用方应把它视为可选加速路径。
func packageLoadedTable(state *lua.State) *runtime.Table {
	if state == nil {
		// nil State 没有全局环境，无法读取 package.loaded。
		return nil
	}
	packageValue, err := lua.GetGlobal(state, "package")
	if err != nil {
		// 全局读取错误表示当前状态不可用，按无 package 处理。
		return nil
	}
	packageTable, err := tableFromValue(packageValue)
	if err != nil {
		// package 不是 table 时说明标准库未打开或被覆盖。
		return nil
	}
	loadedValue := packageTable.RawGetString("loaded")
	loadedTable, err := tableFromValue(loadedValue)
	if err != nil {
		// package.loaded 不是 table 时不能安全写入模块缓存。
		return nil
	}
	return loadedTable
}

// packagePreloadTable 从 State 中读取 package.preload 表。
//
// package 标准库未打开或 package.preload 类型不匹配时返回 nil；RegisterModulePreload 使用该 helper
// 与 package.loaded 的可选写入策略保持一致。
func packagePreloadTable(state *lua.State) *runtime.Table {
	if state == nil {
		// nil State 没有全局环境，无法读取 package.preload。
		return nil
	}
	packageValue, err := lua.GetGlobal(state, "package")
	if err != nil {
		// 全局读取错误表示当前状态不可用，按无 package 处理。
		return nil
	}
	packageTable, err := tableFromValue(packageValue)
	if err != nil {
		// package 不是 table 时说明标准库未打开或被覆盖。
		return nil
	}
	preloadValue := packageTable.RawGetString("preload")
	preloadTable, err := tableFromValue(preloadValue)
	if err != nil {
		// package.preload 不是 table 时不能安全写入预加载模块。
		return nil
	}
	return preloadTable
}

// sortedFunctionNames 返回模块函数名的稳定排序。
//
// nil map 返回空切片；排序只影响注册和 stub 输出顺序，不改变函数语义。
func sortedFunctionNames(functions map[string]Function) []string {
	// 预分配名称切片，避免输出顺序依赖 Go map 遍历。
	names := make([]string, 0, len(functions))
	for name := range functions {
		// 收集显式声明函数名。
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedObjectNames 返回模块对象名的稳定排序。
//
// nil map 返回空切片；排序只影响注册和 stub 输出顺序，不改变对象语义。
func sortedObjectNames(objects map[string]ObjectBinding) []string {
	// 预分配名称切片，避免输出顺序依赖 Go map 遍历。
	names := make([]string, 0, len(objects))
	for name := range objects {
		// 收集显式声明对象名。
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedTableNames 返回嵌套 table 名称的稳定排序。
//
// nil map 返回空切片；排序只影响注册和测试输出稳定性，不改变 table 语义。
func sortedTableNames(tables map[string]TableBinding) []string {
	// 预分配名称切片，避免输出顺序依赖 Go map 遍历。
	names := make([]string, 0, len(tables))
	for name := range tables {
		// 收集显式声明 table 名。
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedAnyNames 返回 any 字段 map 的稳定 key 顺序。
//
// nil map 返回空切片；该 helper 用于字段、变量、常量和元表字段写入。
func sortedAnyNames(fields map[string]any) []string {
	// 预分配名称切片，避免输出顺序依赖 Go map 遍历。
	names := make([]string, 0, len(fields))
	for name := range fields {
		// 收集显式声明字段名。
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedObjectMethodNames 返回对象方法名的稳定排序。
//
// nil map 返回空切片；排序只影响 stub 输出顺序，不改变方法调用语义。
func sortedObjectMethodNames(methods map[string]ObjectMethod) []string {
	// 预分配名称切片，避免输出顺序依赖 Go map 遍历。
	names := make([]string, 0, len(methods))
	for name := range methods {
		// 收集显式声明方法名。
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// luaIdentifier 把 Go 侧 API 名转换为 Lua stub 中可用的标识符。
//
// 合法 Lua 标识符保持原样；非法字符替换为下划线。真实桥接调用名仍保留原始名称。
func luaIdentifier(name string) string {
	if name == "" {
		// 空名称无法作为 Lua 字段标识符，使用下划线占位。
		return "_"
	}

	// 逐字符构造标识符，避免生成 Lua 语法非法的 stub。
	var builder strings.Builder
	for index, char := range name {
		if isLuaIdentifierChar(index, char) {
			// 合法标识符字符保留原样。
			builder.WriteRune(char)
			continue
		}
		// 非法字符替换为下划线，stub 中的转发 key 仍保留原始名称。
		builder.WriteByte('_')
	}
	return builder.String()
}

// isLuaIdentifierChar 判断字符能否出现在 Lua 标识符中。
//
// index 为 0 时只能接受字母或下划线；后续位置允许数字。
func isLuaIdentifierChar(index int, char rune) bool {
	if char == '_' || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') {
		// 字母和下划线在任意位置都合法。
		return true
	}
	if index > 0 && char >= '0' && char <= '9' {
		// 数字只能出现在非首字符位置。
		return true
	}
	return false
}

// luaStringContent 转义 Lua 双引号字符串内容。
//
// 返回值不包含外层引号；当前只生成 ASCII 控制转义，满足 bridge stub 的模块名和 API 名输出。
func luaStringContent(value string) string {
	// 使用 strings.Builder 避免多次字符串拼接。
	var builder strings.Builder
	for _, char := range value {
		switch char {
		case '\\':
			// 反斜杠必须转义，避免吞掉后续字符。
			builder.WriteString("\\\\")
		case '"':
			// 双引号必须转义，避免提前结束字符串。
			builder.WriteString("\\\"")
		case '\n':
			// 换行转义为 \n，保持单行字符串。
			builder.WriteString("\\n")
		case '\r':
			// 回车转义为 \r，保持跨平台文本稳定。
			builder.WriteString("\\r")
		case '\t':
			// 制表符转义为 \t，避免格式不可见。
			builder.WriteString("\\t")
		default:
			// 普通字符原样写入。
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
