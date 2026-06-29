package runtime

import (
	"errors"
	"fmt"
)

var (
	// ErrNilUserdata 表示传入的 userdata 为空。
	ErrNilUserdata = errors.New("nil userdata")
	// ErrNilStateForUserdata 表示尝试在 nil state 上管理 userdata。
	ErrNilStateForUserdata = errors.New("nil state for userdata")
	// ErrNonUserdata 表示尝试注册了非 userdata 值。
	ErrNonUserdata = errors.New("value is not userdata")
)

// UserdataFinalizer 表示显式 userdata 关闭回调。
//
// finalizer 只在 State.Close 阶段执行，用户可以在回调中释放外部资源。
// 回调返回 error 仍会被 State.Close 兜底隔离，不会阻塞后续 userdata close。
type UserdataFinalizer func(payload any) error

// Userdata 表示纯 Go 侧持有的用户数据对象。
//
// 当前阶段维护显式 finalizer 生命周期、user value 与 raw 元表引用；元表用于 file 等
// userdata 通过 `__index` 暴露方法，类型级共享元表后续再按标准库需要扩展。
type Userdata struct {
	// Data 保存用户数据承载的外部对象。
	Data any
	// UserValue 保存 Lua 5.3 userdata 关联的 user value；零值等价于 Lua nil。
	UserValue Value
	// metatable 保存当前 userdata 的 raw 元表；nil 表示未设置元表。
	metatable *Table
	// finalizer 保存显式关闭回调；空表示无外部资源需要显式回收。
	finalizer UserdataFinalizer
	// closed 标记 userdata finalizer 是否已执行过。
	closed bool
}

// NewUserdata 创建基础 userdata 对象。
//
// data 可为空；如需在 State 关闭时释放资源，请使用 NewUserdataWithFinalizer。
func NewUserdata(data any) *Userdata {
	// 构造基础对象，默认无 finalizer。
	return &Userdata{Data: data}
}

// NewUserdataWithFinalizer 创建携带关闭语义的 userdata 对象。
//
// payload 允许承载任意外部资源引用；finalizer 在 State.Close 触发。
func NewUserdataWithFinalizer(data any, finalizer UserdataFinalizer) *Userdata {
	// 返回值直接保留外部对象与回调，后续可通过 State.Close 执行释放逻辑。
	return &Userdata{
		Data:      data,
		finalizer: finalizer,
	}
}

// Value 生成对应 Lua 栈值。
//
// nil 用户数据对象不允许进入栈；这里返回 lua nil，便于调用方统一空值处理。
func (userdata *Userdata) Value() Value {
	if userdata == nil {
		// nil userdata 无可追踪实例，返回 lua nil 避免 panic。
		return NilValue()
	}

	// 用 KindUserdata 与引用身份保留对象 identity 语义。
	return ReferenceValue(KindUserdata, userdata)
}

// SetFinalizer 覆盖用户数据 finalizer。
//
// nil userdata 不允许设置 finalizer，空 finalizer 表示不回收。
func (userdata *Userdata) SetFinalizer(finalizer UserdataFinalizer) error {
	if userdata == nil {
		// nil userdata 无法更新回调，返回明确错误。
		return ErrNilUserdata
	}

	// 覆盖 finalizer 支持在创建后延迟绑定回收语义。
	userdata.finalizer = finalizer
	return nil
}

// SetMetatable 设置 userdata 的 raw 元表。
//
// metatable 为 nil 时表示移除元表；nil userdata 无法保存元表，会返回 ErrNilUserdata。
// 该方法不处理 `__metatable` 保护字段，供 VM 与标准库内部构造 userdata 时使用。
func (userdata *Userdata) SetMetatable(metatable *Table) error {
	if userdata == nil {
		// nil userdata 没有可写入对象，返回明确错误。
		return ErrNilUserdata
	}

	// 直接替换 raw 元表，公开保护语义后续由 API 层包装。
	userdata.metatable = metatable
	return nil
}

// GetMetatable 返回 userdata 的 raw 元表。
//
// 返回 nil 表示当前 userdata 未设置元表；nil userdata 也返回 nil，便于元方法查找路径
// 把损坏或缺失对象统一视为未命中。
func (userdata *Userdata) GetMetatable() *Table {
	if userdata == nil {
		// nil userdata 没有 raw 元表。
		return nil
	}

	// 返回内部 raw 元表引用，调用方不得直接暴露给受保护 API。
	return userdata.metatable
}

// RegisterUserdata 注册 userdata 到 State，建立显式关闭路径。
//
// 注册不是“拥有”语义，允许重复调用，重复注册仅保留一次，避免 finalizer 重复执行。
func (state *State) RegisterUserdata(userdata *Userdata) error {
	if state == nil {
		// nil State 无法维护关闭路径。
		return ErrNilStateForUserdata
	}
	if state.closed {
		// 已关闭状态不再接收新注册，避免泄漏路径错位。
		return ErrClosedState
	}
	if userdata == nil {
		// nil userdata 不具备生命周期。
		return ErrNilUserdata
	}

	// 避免重复注册导致 finalizer 多次执行，重复添加会成为只读引用。
	for index := range state.userdatas {
		if state.userdatas[index] == userdata {
			// 已注册对象再次进入不改变关闭序列。
			return nil
		}
	}
	state.userdatas = append(state.userdatas, userdata)
	return nil
}

// RegisterValueUserdata 从 Value 提取 userdata 引用并注册。
//
// 通过 Value 进行桥接，避免外部直接持有 State.userdata 列表。
func (state *State) RegisterValueUserdata(value Value) error {
	if value.Kind != KindUserdata {
		// 只有 userdata reference 才能进入明确关闭策略。
		return ErrNonUserdata
	}

	// KindUserdata 必须使用 *Userdata 构建，避免引用类型错配。
	userdata, ok := value.Ref.(*Userdata)
	if !ok {
		// 值类型错配通常是错误构造路径，可回退为 nil 用户数据错误。
		return fmt.Errorf("%w: expected *Userdata", ErrNonUserdata)
	}

	return state.RegisterUserdata(userdata)
}

// closeUserdatas 统一执行注册 userdata 的 finalizer。
//
// close 阶段不传播错误，兼容 Lua C API 只做用户资源回收，避免关闭路径阻塞。
func (state *State) closeUserdatas() {
	if state == nil {
		// nil state 不执行关闭路径。
		return
	}
	for index := len(state.userdatas) - 1; index >= 0; index-- {
		// 逆序执行更接近栈式资源释放直觉，减少嵌套依赖影响。
		userdata := state.userdatas[index]
		if userdata == nil || userdata.closed {
			// nil 或已关闭 userdata 直接跳过，保持幂等性。
			continue
		}

		if userdata.finalizer != nil {
			func() {
				// 回调执行前后都要保证 state 关闭流程不会因异常中断。
				defer func() {
					// 用户回调 panic 不能中断 State 关闭，不再继续传播。
					_ = recover()
				}()
				_ = userdata.finalizer(userdata.Data)
			}()
		}
		userdata.closed = true
	}
}
