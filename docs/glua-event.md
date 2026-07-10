# GLua 事件

`glua.event` 提供文件作用域的执行观察能力。在某个 Lua 文件中注册的事件只对该源文件生效。

```lua
local event = glua.event

local id = event.setProgress(event.events.progress_function_call, function(ctx)
  print(ctx.timestamp, ctx.event)
end, {
  whitelist = { "work" },
})
```

## API

- `glua.event.setProgress(event, callback [, config])`：同步注册事件，返回事件 ID。
- `glua.event.setProgressAsync(event, callback [, config])`：注册异步事件，回调会进入队列并在 VM 安全点执行，返回事件 ID。
- `glua.event.callProgress(event [, payload])`：在当前文件中同步触发自定义事件。
- `glua.event.callProgressAsync(event [, payload])`：将自定义事件加入异步队列。
- `glua.event.setMuted(id, muted)`：临时静音或重新启用指定事件。
- `glua.event.setCallback(id, callback)`：替换监听器回调，保留原事件 ID、配置和统计。
- `glua.event.remove(id)`：删除指定事件，返回该事件是否存在。
- `glua.event.setConfig(id, config)`：替换函数事件的过滤配置。
- `glua.event.getConfig(id)`：返回事件当前的配置表；事件不存在时返回 `nil`。
- `glua.event.get(id)`：返回监听器回调、配置、状态和累计统计快照。
- `glua.event.clear([event])`：清理当前文件全部监听器或指定事件监听器，返回删除数量。
- `glua.event.setGroupMuted(group, muted)`：批量静音或启用当前文件中的监听器分组。
- `glua.event.removeGroup(group)`：删除当前文件指定分组，返回删除数量。
- `glua.event.flush()`：立即消费当前 State 的异步事件队列，返回执行数量。
- `glua.event.stats()`：返回当前文件监听器和异步队列统计。
- `glua.event.eventList()`：返回当前源码文件已经注册的事件及监听器统计。

每个回调都会收到一个上下文表，其中包含 `event`、`kind`、`timestamp`（Unix 毫秒时间戳）、`sequence`、`eventId`、`traceId`、可选 `parentEventId`、`depth`、`payload`、`source`、`line`、`id`、`config`、`group`、`priority` 和可靠性配置。函数进度事件还会提供 `args`、`results`、`error`、`durationNs` 和调用元数据。

回调通过事件 ID 读取配置，因此调用 `setConfig` 后总能获取最新配置：

```lua
event.setProgress(event.events.progress_function_call, function(ctx)
  local config = event.getConfig(ctx.id)
  if config then
    print(config.whitelist[1])
  end
end, { whitelist = { "work" } })
```

## 预设事件

- `progress_line`：执行到新的源码行。
- `progress_start`、`progress_end`、`progress_error`、`progress_exit`：源文件生命周期事件。
- `progress_function_call`、`progress_function_return`、`progress_function_error`、`progress_function_exit`：当前源文件中的 Lua 闭包调用生命周期事件。

`progress_function_*` 支持 `whitelist` 和 `blacklist`。黑名单的优先级高于白名单。名单元素可以使用以下形式：

- 名称字符串：`{ "work", "render" }`，兼容原有按调试名称匹配的行为。
- 函数变量：`{ moduleA.run }`，按 Lua closure identity 精确匹配，可区分跨文件同名函数。
- 映射表：`{ work = true }` 或 `{ [moduleA.run] = true }`，值为 `false` 时忽略该项。

函数变量由事件配置表作为 GC 强引用持有，内部按 Lua closure 对象引用身份精确比较，不向脚本暴露宿主内存地址。

```lua
local moduleA = require("module_a")
local moduleB = require("module_b")

local id = event.setProgress(event.events.progress_function_call, callback, {
  whitelist = { moduleA.run },
  blacklist = { moduleB.run },
})
```

## 监听器配置

第三个参数和 `setConfig` 支持以下字段：

- `whitelist`、`blacklist`：函数名称或函数引用过滤器。
- `once`：触发一次后自动删除，等价于 `maxCalls = 1`。
- `maxCalls`：最多实际执行次数，`0` 表示不限制；达到上限后自动删除。
- `priority`：整数，越大越先执行；相同优先级保持注册顺序。
- `group`：监听器分组名称。
- `queueLimit`：该异步监听器最大待处理任务数，`0` 表示不限制。
- `overflow`：队列溢出策略，可选 `drop_oldest`、`drop_newest`、`error`。
- `onError`：同步和异步回调错误策略，可选 `propagate`、`ignore`、`mute`、`remove`；`throw` 和 `continue` 分别是前两者的别名。
- `mutable`：是否允许上下文保留业务 table 的可变引用，默认 `false`。
- `throttleMs`：前沿节流窗口，窗口内重复触发会被抑制，`0` 表示关闭。
- `debounceMs`：异步防抖窗口，只允许用于 `setProgressAsync`；窗口内只保留最新上下文。
- `sampleRate`：`0..1` 的确定性累加采样率，默认 `1`。

默认情况下，`locals`、`upvalues`、`calleeUpvalues`、`args`、`results` 和其中嵌套的 table 都会递归复制并冻结。设置 `mutable = true` 后，回调可以直接修改原始 table，应只在明确需要拦截器行为时使用。

```lua
local id = event.setProgressAsync("task.done", callback, {
  once = true,
  priority = 10,
  group = "trace",
  queueLimit = 100,
  overflow = "drop_oldest",
  onError = "ignore",
  maxCalls = 10,
  throttleMs = 100,
  debounceMs = 50,
  sampleRate = 0.5,
})

event.setCallback(id, replacementCallback)
```

## 监听器列表

`event.eventList()` 只统计调用它的当前源码文件，返回结构如下：

```lua
{
  source = "@/path/to/main.glua",
  totalEvents = 2,
  totalListeners = 3,
  activeListeners = 2,
  mutedListeners = 1,
  syncListeners = 2,
  asyncListeners = 1,
  queuedTasks = 0,
  droppedTasks = 0,
  callbackErrors = 0,
  suppressedEvents = 0,
  debouncedTasks = 0,
  sequence = 42,
  traceSequence = 1,
  events = {
    {
      event = "progress.function_call",
      listeners = 2,
      active = 1,
      muted = 1,
      sync = 2,
      async = 0,
      dispatchCount = 12,
      errorCount = 0,
      droppedCount = 0,
      suppressedCount = 3,
      totalDurationNs = 1000,
    },
  },
}
```

`events` 按事件名稳定排序。静音监听器仍计入 `listeners`，但不计入 `active`；删除监听器后，下一次调用会立即反映最新统计。`stats()` 当前返回与 `eventList()` 相同的结构。

## 回调规则

同步回调可以通过抛出错误中止执行。异步回调会先进入队列，不会在发出事件的指令位置立即执行；异步回调默认在后续 VM 安全点传播错误。`onError = "ignore"` 记录后继续，`mute` 会保留并静音监听器，`remove` 会删除监听器和待处理任务。事件回调不会递归触发新的事件回调。

每个回调上下文还包含 `eventId`、`traceId` 和可选 `parentEventId`。`eventId` 与 `sequence` 相同并在 State 内单调递增；同一代码执行链共享 `traceId`；存在较浅调用层事件时，`parentEventId` 指向最近父事件。监听器专属上下文同时包含 `maxCalls`、`throttleMs`、`debounceMs` 和 `sampleRate`。

`event.get(id)` 除原有状态外会返回 `remainingCalls`、`dispatchCount`、`errorCount`、`droppedCount`、`suppressedCount`、`throttledCount`、`sampledOutCount`、`debouncedCount`、`totalDurationNs` 和 `averageDurationNs`。无限监听器的 `remainingCalls` 为 `-1`。

防抖不启动 goroutine，也不会从后台线程重入 VM。到期任务在后续 VM 安全点执行；`event.flush()` 会忽略剩余等待时间，立即执行队列中的最新防抖任务。

普通 `pcall`、`xpcall` 及其跨 `coroutine.yield` continuation 都会保留函数 call/return/error/exit 生命周期；错误即使最终被保护调用捕获，`progress_function_error` 仍会先触发。

`State.Close()` 会删除该 State 的事件注册表，并清空监听器、配置根引用和待处理异步任务。直接调用 `state.Close()` 与包级 `lua.Close(state)` 使用同一清理路径，重复关闭不会再次执行回调或保留全局索引。

## 独立回归脚本

使用当前 CLI 产物运行事件测试套件：

```bash
./scripts/test-glua-events.sh
```

可以通过 `GLUA_BIN` 验证其他构建产物，例如 `GLUA_BIN=/path/to/glua ./scripts/test-glua-events.sh`。
该套件覆盖预设事件名、源码行观察、自定义同步与异步事件、函数调用/返回/错误/退出观察、跨文件同名函数引用过滤、监听器治理、回调替换、优先级、once、maxCalls、节流、异步防抖、确定性采样、错误处置、调用链、分组、队列策略、只读上下文、`pcall` 错误观察、统计、配置修改、静音、删除、时间戳，以及文件错误/退出生命周期回调。
