package runtime

import (
	"errors"
	"fmt"
	goRuntime "runtime"
	"testing"
	"weak"
)

// TestUserdataValueNil 与 Value 交互验证。
//
// 该测试确认 nil userdata 使用统一的 nil 值返回策略，并保证不触发 panic。
func TestUserdataValueNil(t *testing.T) {
	var userdata *Userdata
	if got := userdata.Value(); !got.IsNil() {
		// nil userdata 预期返回 lua nil，便于上层统一空值处理。
		t.Fatalf("nil userdata value should be nil, got %#v", got)
	}
}

// TestNewUserdataWithFinalizer 与 SetFinalizer 验证。
//
// 覆盖带 finalizer 构造与后续替换回调的合法路径。
func TestNewUserdataWithFinalizer(t *testing.T) {
	first := 0
	second := 0
	userdata := NewUserdataWithFinalizer("payload", func(payload any) error {
		if got := payload.(string); got != "payload" {
			t.Fatalf("payload mismatch: %q", got)
		}
		first++
		return nil
	})

	if err := userdata.SetFinalizer(func(payload any) error {
		if got := payload.(string); got != "payload" {
			t.Fatalf("payload mismatch after reset: %q", got)
		}
		second++
		return nil
	}); err != nil {
		// 覆盖非 nil userdata 的 finalizer 重设路径。
		t.Fatalf("set finalizer failed: %v", err)
	}

	state := NewState()
	if err := state.RegisterUserdata(userdata); err != nil {
		// 注册后在 Close 时会触发回调，前置路径本身必须可用。
		t.Fatalf("register userdata failed: %v", err)
	}
	state.Close()

	if first != 0 {
		// 重新设置 finalizer 后旧回调不得触发。
		t.Fatalf("should execute new finalizer only")
	}
	if second != 1 {
		// 新回调必须在关闭路径执行一次。
		t.Fatalf("new finalizer should execute once, got %d", second)
	}
}

// TestSetFinalizerNilData 验证 nil userdata 设置 finalizer 报错。
//
// 显式错误语义用于与其他对象生命周期方法保持一致。
func TestSetFinalizerNilData(t *testing.T) {
	var userdata *Userdata
	if err := userdata.SetFinalizer(func(any) error { return nil }); !errors.Is(err, ErrNilUserdata) {
		// nil 用户数据无可写入对象，应该返回 ErrNilUserdata。
		t.Fatalf("set finalizer expected ErrNilUserdata, got %v", err)
	}
}

// TestUserdataMetatable 验证 userdata raw 元表的设置与读取。
//
// file userdata 和 Go 对象代理依赖该元表承载 `__index` 方法表；nil userdata 必须返回明确错误。
func TestUserdataMetatable(t *testing.T) {
	userdata := NewUserdata("payload")
	metatable := NewTable()
	if err := userdata.SetMetatable(metatable); err != nil {
		// 非 nil userdata 设置元表必须成功。
		t.Fatalf("set userdata metatable failed: %v", err)
	}
	if got := userdata.GetMetatable(); got != metatable {
		// 读取到的 raw 元表必须与写入对象保持同一 identity。
		t.Fatalf("userdata metatable mismatch: got=%p want=%p", got, metatable)
	}
	if err := userdata.SetMetatable(nil); err != nil {
		// nil 元表表示移除元表，必须允许。
		t.Fatalf("clear userdata metatable failed: %v", err)
	}
	if got := userdata.GetMetatable(); got != nil {
		// 移除后 raw 元表必须为空。
		t.Fatalf("userdata metatable should be nil after clear: %p", got)
	}

	var nilUserdata *Userdata
	if err := nilUserdata.SetMetatable(metatable); !errors.Is(err, ErrNilUserdata) {
		// nil userdata 不能保存元表。
		t.Fatalf("nil userdata metatable error = %v, want ErrNilUserdata", err)
	}
	if got := nilUserdata.GetMetatable(); got != nil {
		// nil userdata 读取元表应保持 nil，方便元方法查找按未命中处理。
		t.Fatalf("nil userdata metatable should be nil: %p", got)
	}
}

// TestRegisterUserdata 与 closeUserdatas 验证关闭顺序、去重和错误隔离。
//
// 关闭顺序采用注册逆序，finalizer panic 或 error 不能阻塞后续清理。
func TestRegisterUserdataAndCloseOrder(t *testing.T) {
	order := make([]string, 0, 3)
	state := NewState()

	first := NewUserdataWithFinalizer("first", func(payload any) error {
		order = append(order, payload.(string))
		return nil
	})
	second := NewUserdataWithFinalizer("second", func(payload any) error {
		order = append(order, payload.(string))
		return fmt.Errorf("explicit error")
	})
	third := NewUserdataWithFinalizer("third", func(payload any) error {
		order = append(order, payload.(string))
		panic("panic in finalizer")
	})

	// 重复注册应去重，避免 finalizer 重入执行。
	if err := state.RegisterUserdata(first); err != nil {
		t.Fatalf("register first userdata failed: %v", err)
	}
	if err := state.RegisterUserdata(first); err != nil {
		t.Fatalf("register duplicate userdata should succeed: %v", err)
	}
	if err := state.RegisterUserdata(second); err != nil {
		t.Fatalf("register second userdata failed: %v", err)
	}
	if err := state.RegisterUserdata(third); err != nil {
		t.Fatalf("register third userdata failed: %v", err)
	}

	state.Close()

	// 关闭顺序应为注册逆序：third -> second -> first。
	if len(order) != 3 {
		t.Fatalf("finalizer invocation count mismatch, want 3 got %d", len(order))
	}
	if order[0] != "third" || order[1] != "second" || order[2] != "first" {
		t.Fatalf("finalizer order mismatch: %#v", order)
	}

	// 同一对象多次 Close 不应再次触发 finalizer。
	order = order[:0]
	state.Close()
	if len(order) != 0 {
		t.Fatalf("closed state should be idempotent, finalizers should not repeat")
	}
}

// TestRegisterUserdataDoesNotRetainOrdinaryPayload 验证 State 不强持有无 finalizer 的 userdata。
//
// payload 模拟截图等大对象；注册完成且宿主引用离开作用域后，即使 State 仍保持打开，Go GC 也应
// 能回收 payload。测试使用弱引用判定对象可达性，不依赖 RSS、睡眠或关闭 State。
func TestRegisterUserdataDoesNotRetainOrdinaryPayload(t *testing.T) {
	// 创建保持打开的 State，并在独立函数中构造短生命周期 payload，避免测试栈变量延长其寿命。
	state := NewState()
	defer state.Close()
	payloadReference := registerOrdinaryUserdataPayload(t, state)

	for attempt := 0; attempt < 20 && payloadReference.Value() != nil; attempt++ {
		// 连续触发诊断 GC 并让出调度，等待弱引用反映不可达状态；不引入时间抖动判断。
		goRuntime.GC()
		goRuntime.Gosched()
	}
	if payloadReference.Value() != nil {
		// State 打开期间仍可达说明注册路径依旧保留了普通 userdata 或其 payload。
		t.Fatalf("ordinary userdata payload should be collectible while state remains open")
	}
}

// registerOrdinaryUserdataPayload 构造并注册普通 userdata，返回 payload 弱引用供调用方验证。
//
// state 必须有效且未关闭；注册错误会直接终止测试。函数返回后不保留 payload 或 userdata 强引用。
func registerOrdinaryUserdataPayload(t *testing.T, state *State) weak.Pointer[[]byte] {
	// 分配足够大的独立 payload，避免 tiny allocator 合批影响弱引用清理判定。
	payload := make([]byte, 12*1024*1024)
	payloadPointer := &payload
	payloadReference := weak.Make(payloadPointer)
	userdata := NewUserdata(payloadPointer)
	if err := state.RegisterUserdata(userdata); err != nil {
		// 有效普通 userdata 注册失败时无法继续验证生命周期。
		t.Fatalf("register ordinary userdata failed: %v", err)
	}
	goRuntime.KeepAlive(userdata)
	return payloadReference
}

// TestRegisterValueUserdata 验证从 Value 注册 userdata 的边界。
//
// 覆盖 nil State、类型不符和引用类型损坏三类典型错误。
func TestRegisterValueUserdata(t *testing.T) {
	state := NewState()
	userdata := NewUserdata("x")
	value := userdata.Value()

	if err := state.RegisterValueUserdata(value); err != nil {
		t.Fatalf("register userdata value failed: %v", err)
	}

	if err := state.RegisterValueUserdata(StringValue("x")); !errors.Is(err, ErrNonUserdata) {
		// 非 userdata 值禁止进入 userdata 生命周期路径。
		t.Fatalf("register non-userdata should return ErrNonUserdata, got %v", err)
	}

	wrongType := ReferenceValue(KindUserdata, "not-userdata-obj")
	err := state.RegisterValueUserdata(wrongType)
	if err == nil {
		t.Fatalf("register broken userdata value should return error")
	}

	closed := NewState()
	closed.Close()
	err = closed.RegisterUserdata(userdata)
	if !errors.Is(err, ErrClosedState) {
		t.Fatalf("register on closed state should return ErrClosedState, got %v", err)
	}

	var nilState *State
	err = nilState.RegisterUserdata(userdata)
	if !errors.Is(err, ErrNilStateForUserdata) {
		t.Fatalf("register on nil state should return ErrNilStateForUserdata, got %v", err)
	}
}
