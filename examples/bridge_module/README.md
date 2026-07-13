# Go 模块桥接示例

该示例模拟第三方 Go 宿主通过公开 `bridge` 包注册延迟加载模块。Lua 使用 `require("host.config")` 获取模块，并验证：

- `VERSION` 是不可覆盖的只读常量。
- `enabled` 是 Lua 可修改的普通变量。
- `greet` 由 Go 实现并返回 Lua string。
- `paths` 是整体只读的嵌套 table。

运行：

~~~bash
CGO_ENABLED=0 go run ./examples/bridge_module
~~~

`RegisterModulePreload` 要求先调用 `lua.OpenLibs` 打开 `package` 标准库。模块在第一次 `require` 时构造，之后由 `package.loaded` 缓存。
