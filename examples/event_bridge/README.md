# Go 与 Lua Event 交互示例

该示例模拟仓库外第三方 Go 程序：宿主打开标准库，通过公开 `lua` 包注册 Event，Lua 使用 `glua.json` 生成负载并触发 Go callback。随后宿主演示静音/恢复监听器、替换 callback、异步触发与显式 flush、更新类型化配置、查询统计和删除监听器。

~~~bash
CGO_ENABLED=0 go run ./examples/event_bridge
~~~

单个 `lua.State` 不应被多个 goroutine 同时执行。`CallProgressEventAsync` 只把任务加入 State 队列，不会创建后台 goroutine；任务在 VM 安全点或 `FlushProgressEvents` 中执行。

`ProgressEventScopeRuntime` 是默认范围，监听当前 State 涉及的全部 Lua source；改用 `ProgressEventScopeFile` 时只匹配注册时传入的 source。

示例使用 `ChunkProgressEventSource` 和 `FileProgressEventSource` 构造稳定 Source，并通过类型化 `ListProgressEvents` 读取监听器、队列和 State 预算统计。高级调用方仍可使用 `ListProgressEventsRaw` 读取原始 Lua table。

本示例属于项目自有代码，适用仓库根目录的非商业许可证。商业集成前需要取得单独付费商业授权。
