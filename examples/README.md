# 示例

本目录按使用方式提供可直接运行的示例。默认构建和全部示例均使用 `CGO_ENABLED=0`，不需要系统 Lua、C 编译器或 Native 模块。

| 示例 | 说明 | 运行命令 |
| --- | --- | --- |
| [`embed`](embed) | 创建 State、注册 Go 函数并从 Go 调用 | `CGO_ENABLED=0 go run ./examples/embed` |
| [`bridge_module`](bridge_module) | 通过 `package.preload` 向 Lua 暴露 Go 模块、常量、变量、函数和只读子表 | `CGO_ENABLED=0 go run ./examples/bridge_module` |
| [`event_bridge`](event_bridge) | Go 注册 Event，Lua/Go 触发，并演示静音、替换回调、异步队列、配置更新和删除 | `CGO_ENABLED=0 go run ./examples/event_bridge` |
| [`extensions`](extensions) | 在 Lua 中使用序列化、Codec、Hash、Regex、UUID、ZIP、Schema 和 Path | 见目录内 README |

单个 `lua.State` 必须由宿主串行使用。Event 的异步调用只进入 State 安全点队列，不会创建后台 goroutine。

示例属于项目自有代码，适用仓库根目录的非商业许可证。商业集成前需要取得单独付费商业授权。
