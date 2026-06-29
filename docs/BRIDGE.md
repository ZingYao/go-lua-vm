# Go/Lua Bridge 设计

本文记录 Go 与 Lua 双向回调、Go 对象代理和 Lua stub 生成的设计。Bridge 位于 `bridge` 包，稳定入口由 `lua` 包暴露。

## 目标

- 允许 Go 注册函数给 Lua 调用。
- 允许 Go 调用 Lua 函数并读取返回值。
- 允许 Go 函数内部回调 Lua，支持 Go -> Lua -> Go 和 Lua -> Go -> Lua 链路。
- 允许 Go 对象以 userdata 或 table 代理形式暴露给 Lua。
- 允许根据 Go 注册信息生成 Lua stub/代理代码。
- 保持纯 Go，无 CGO，不依赖 Lua C API。

## Go 函数注册

```go
type GoFunction func(context.Context, *lua.State) (int, error)
```

注册流程：

1. `lua.State.Register` 接收名称和 Go 函数。
2. bridge 包创建 Go closure 值并写入全局环境或目标 table。
3. Lua 调用该 closure 时，runtime 建立 Go 调用帧。
4. Go 函数从 Lua 栈读取参数，压入返回值。
5. Go 函数返回的错误转换为 Lua error。

Go panic 必须在边界 recover，并包装为可追踪的 Lua runtime error。

## Go 调 Lua

Go 调 Lua 需要支持：

- 从全局环境读取函数。
- 从 table 字段读取函数。
- 直接调用栈上的函数。
- 传入基础类型、slice、map、struct 代理和 userdata。
- 指定期望返回值数量。
- 在 context 取消时中断 VM。

调用必须使用 protected call，不能让 Lua error 穿透为 Go panic。

## 类型转换

基础转换：

- Go `nil` -> Lua nil。
- Go `bool` -> Lua boolean。
- Go 整数 -> Lua integer。
- Go 浮点 -> Lua number。
- Go `string` / `[]byte` -> Lua string。
- Go `map` / `slice` 可显式转换为 Lua table。
- Go struct 默认通过代理暴露，不自动深拷贝。

不做隐式反射绑定作为默认行为。自动绑定需要显式调用 `bridge.BindReflectFunction`、`bridge.RegisterReflectFunction` 或 `bridge.BindReflectStruct`，并记录可见方法、字段、错误策略和性能风险。

## Reflection 自动绑定

reflection 自动绑定是显式启用能力，不会替代 `ObjectBinding` 的可审计显式绑定路径：

- `bridge.BindReflectFunction` 将 Go 函数包装为 Lua callable，支持 bool、整数、浮点、string、`[]byte`、`lua.Value`、对象代理参数和对应返回值转换。
- 函数和方法返回的非 nil `error` 会转换为 Lua error object；nil `error` 不进入 Lua 多返回值。
- Go panic 会在 bridge 边界 recover，并按现有 panic-to-error 规则转换为 Lua runtime error。
- `bridge.BindReflectStruct` 只接受非 nil struct 或 struct 指针；nil receiver 会在绑定阶段拒绝。
- 字段只暴露导出字段，支持嵌入字段提升；不可导出字段不会生成 getter 或 setter。
- 字段 tag 默认读取 `lua`：`lua:"name"` 重命名，`lua:"-"` 跳过，`lua:"name,readonly"` 只读。
- struct 指针可写回可设置字段；非指针 struct 或不可设置字段按只读处理。
- 方法只暴露导出方法；struct 指针绑定可同时暴露值 receiver 与指针 receiver 方法。
- struct、struct 指针和方法返回对象会转换为 Lua 对象代理，并用 Go 指针 identity 缓存代理，避免 self 引用和循环引用造成递归展开。
- 反射转换只覆盖明确支持的基础类型和对象代理；slice 当前只支持 `[]byte` 与 Lua string 互转，map/table 深拷贝不作为自动绑定默认能力。

## 对象代理

对象代理使用 table 或 userdata 表示：

- 默认推荐使用显式 `bridge.ObjectBinding`；需要快速暴露 Go struct/function 时可显式启用 reflection 自动绑定。
- 对 Lua 侧公开的值是 table，隐藏字段 `__userdata` 保存 `*runtime.Userdata`，userdata 负载指回 `*bridge.ObjectProxy`。
- `__index` 按方法优先、getter 次之的顺序转发 string key；未命中返回 Lua nil。
- `__newindex` 只允许写入显式 setter，未声明 setter 的属性返回 Lua error，避免 Lua 侧污染代理表。
- 对象方法签名为 `bridge.ObjectMethod`，通过 `ObjectContext.Object()` 读取绑定对象，通过普通 `Context` helper 读取参数和压入返回值。
- `__gc` 暂不承诺等同 C Lua userdata 析构语义，Go 生命周期以 Go GC 为准；State 关闭阶段只执行已注册 userdata 的显式 finalizer。
- 代理对象必须保留 Go identity，避免 Lua table 拷贝破坏状态。

## Lua stub 生成

stub 生成不是把 Go 源码翻译为 Lua 源码，而是根据注册信息生成 Lua 侧代理模块：

```lua
local M = {}

function M.goFunc(...)
  return __go_bridge_call("goFunc", ...)
end

return M
```

生成内容包括：

- 模块名。
- 函数名。
- 参数和返回值注释。
- 错误语义说明。
- 对象代理 metatable。

## Go 模块注册示例

Go 侧通过显式 `ModuleBinding` 暴露模块，不做反射自动导出：

```go
state := lua.NewState()
defer state.Close()
_ = lua.OpenLibs(state)

counter := &Counter{Value: 1}
moduleValue, err := bridge.RegisterModule(state, bridge.ModuleBinding{
    Name: "gomod",
    Functions: map[string]bridge.Function{
        "add": func(ctx *bridge.Context) error {
            left, _ := ctx.ToInteger(1)
            right, _ := ctx.ToInteger(2)
            ctx.PushInteger(left + right)
            return nil
        },
    },
    Objects: map[string]bridge.ObjectBinding{
        "counter": {
            Object: counter,
            Methods: map[string]bridge.ObjectMethod{
                "read": func(ctx *bridge.ObjectContext) error {
                    ctx.PushInteger(ctx.Object().(*Counter).Value)
                    return nil
                },
            },
        },
    },
})
```

注册成功后：

- 全局 `gomod` 指向模块表。
- `package.loaded.gomod` 在 package 库可用时指向同一个模块表。
- Lua 侧 `require("gomod")` 返回同一模块实例。

对应 Lua stub 示例：

```lua
local M = {}

function M.add(...)
  return __go_bridge_call("gomod.add", ...)
end

M.counter = setmetatable({}, {
  __index = function(_, key)
    return __go_bridge_property("gomod.counter", key)
  end,
  __newindex = function(_, key, value)
    return __go_bridge_set_property("gomod.counter", key, value)
  end,
})

function M.counter:read(...)
  return __go_bridge_call("gomod.counter:read", self, ...)
end

return M
```

## 错误与 yield 边界

- Go error 转为 Lua error object。
- Lua error 转为 Go error，并保留 traceback。
- Go panic 在 bridge 边界转换为 Lua runtime error。
- 当前第一阶段默认 `YieldForbidden`：Lua coroutine yield 不允许穿越 Go 调用边界。
- 允许 yield 的边界必须等完整 VM 调用帧、coroutine resume/yield 恢复协议和 Go 回调续体都接入后再开启。
- 不允许 yield 的边界必须返回明确错误，禁止静默吞掉 yield 或把 yield 伪装成普通 return。

## 测试策略

- Lua 调 Go 成功路径。
- Lua 调 Go 返回错误。
- Go 调 Lua 成功路径。
- Go 调 Lua 遇到 Lua error。
- Go -> Lua -> Go 嵌套调用。
- Lua -> Go -> Lua 嵌套调用。
- context 取消中断回调链。
- stub 生成 golden 对比。
