# 扩展方法总览

`OpenLibs` 会在全局 `glua` table 下注册扩展能力。除 `glua.event` 可以通过 `--glua-disable-events` 关闭外，序列化与通用工具不受 Event 开关影响。

## 命名空间

| 命名空间 | 能力 |
| --- | --- |
| `glua.event` | 文件与函数生命周期、自定义事件、异步队列和监听器治理 |
| `glua.json` | JSON 编解码 |
| `glua.yaml` | YAML 编解码 |
| `glua.xml` | XML 编解码、namespace、CDATA 和类型标记 |
| `glua.toml` | TOML 编解码 |
| `glua.codec` | Base64、Hex、URL 编解码 |
| `glua.hash` | MD5、SHA 与 HMAC |
| `glua.regex` | 基于 RE2 的正则 |
| `glua.uuid` | UUID v4、校验与规范化 |
| `glua.zip` | 受资源限制的内存 ZIP |
| `glua.schema` | 轻量结构校验 |
| `glua.path` | 不访问文件系统的纯词法路径运算 |

## Event

### 预设事件

- 文件：`progress_start`、`progress_line`、`progress_end`、`progress_error`、`progress_exit`。
- 函数：`progress_function_call`、`progress_function_return`、`progress_function_error`、`progress_function_exit`。

### 方法

| 方法 | 说明 |
| --- | --- |
| `setProgress(event, callback [, config])` | 注册同步监听器，默认覆盖当前 State 全部 Lua source，返回事件 ID |
| `setProgressAsync(event, callback [, config])` | 注册安全点异步监听器 |
| `callProgress(event [, payload])` | 同步触发当前文件自定义事件 |
| `callProgressAsync(event [, payload])` | 异步触发当前文件自定义事件 |
| `setMuted(id, muted)` | 静音或恢复监听器 |
| `setCallback(id, callback)` | 替换回调并保留 ID、配置和统计 |
| `setConfig(id, config)` | 替换过滤与可靠性配置 |
| `getConfig(id)` | 读取配置 |
| `get(id)` | 读取监听器状态和统计 |
| `remove(id)` | 删除监听器 |
| `clear([event])` | 清理当前文件监听器 |
| `setGroupMuted(group, muted)` | 批量静音分组 |
| `removeGroup(group)` | 删除分组 |
| `flush()` | 立即消费异步和防抖任务 |
| `eventList()` / `stats()` | 查询对当前来源生效的监听器、丢弃/拒绝任务与队列统计 |

### Sample

~~~glua
local event = glua.event

local listenerID = event.setProgressAsync(
  event.events.progress_function_call,
  function(ctx)
    print(ctx.traceId, ctx.eventId, ctx.calleeName, ctx.line)
  end,
  {
    whitelist = { "processOrder" },
    maxCalls = 100,
    priority = 10,
    throttleMs = 25,
    debounceMs = 10,
    sampleRate = 0.5,
    queueLimit = 32,
    overflow = "drop_oldest",
    onError = "mute",
    group = "trace",
    scope = "runtime",
  }
)

event.setCallback(listenerID, function(ctx)
  print("new callback", ctx.event)
end)

local summary = event.stats()
print(summary.totalListeners, summary.suppressedEvents)
~~~

Event 回调默认接收只读快照，不会直接修改业务 table。详细触发时机、上下文字段和错误策略见 [Event 文档](glua-event.md)。

`scope` 默认为 `runtime`；设置为 `file` 时只匹配注册来源。Go 宿主可使用 `lua.SetProgressEvent`、`lua.CallProgressEvent`、`lua.CallProgressEventAsync` 和 `lua.FlushProgressEvents` 与 Lua 双向触发，完整示例见 [Event 文档](glua-event.md#从-go-与-lua-交互)。

Go 宿主还可以通过 `RemoveProgressEvent`、`SetProgressEventMuted`、`SetProgressEventCallback`、`SetProgressEventOptions`、`GetProgressEvent`、类型化 `ListProgressEvents`、原始 `ListProgressEventsRaw`、`ClearProgressEvents`、`SetProgressEventGroupMuted` 和 `RemoveProgressEventGroup` 完成监听器治理。`FileProgressEventSource`、`ChunkProgressEventSource` 和 `NormalizeProgressEventSource` 用于构造与 `Proto.Source` 一致的来源。单个 State 必须串行使用；异步 Event 是安全点队列，不是后台 goroutine。

State 默认限制 4096 个监听器、65536 个排队任务和单次 drain 4096 个任务，可通过 `lua.Options.MaxGluaEventListeners`、`MaxGluaEventQueuedTasks`、`MaxGluaEventTasksPerDrain` 调整。Go 侧参数、配置冲突和预算错误均提供稳定 sentinel 与结构化错误类型。

## 序列化

### 方法

- `glua.json.encode(value [, prettyOrOptions])`
- `glua.json.decode(text [, options])`
- `glua.yaml.encode(value [, options])`
- `glua.yaml.decode(text [, options])`
- `glua.xml.encode(value [, options])`
- `glua.xml.decode(text [, options])`
- `glua.toml.encode(value [, options])`
- `glua.toml.decode(text [, options])`
- `glua.array([table])`：标记显式数组。
- `glua.object([table])`：标记显式对象。
- `glua.null`：跨格式 null 值。

### Sample

~~~lua
local payload = {
  name = "GLua",
  tags = glua.array({ "lua53", "go" }),
  metadata = glua.object(),
  optional = glua.null,
}

local text = glua.json.encode(payload, {
  pretty = true,
  maxDepth = 32,
  maxOutputBytes = 1024 * 1024,
})

local restored = glua.json.decode(text, {
  numberMode = "auto",
})

print(restored.name, restored.tags[1])
~~~

四种格式共享深度、节点、输入和输出预算。完整结构映射见 [序列化文档](glua-serialization.md)。

## Codec 与 Hash

~~~lua
local encoded = glua.codec.base64Encode("binary\0data")
local decoded = glua.codec.base64Decode(encoded)

local digest = glua.hash.sha256(decoded)
local signature = glua.hash.hmac("sha256", "secret", decoded)
~~~

方法：

- Codec：`base64Encode`、`base64Decode`、`hexEncode`、`hexDecode`、`urlEncode`、`urlDecode`，以及接受可读 `io.open` 对象的 `base64EncodeFile`、`base64DecodeFile`、`hexEncodeFile`、`hexDecodeFile`。
- Hash：`md5`、`sha1`、`sha256`、`sha512`、`hmac`，以及从文件当前位置流式计算的 `md5File`、`sha1File`、`sha256File`、`sha512File`、`hmacFile`。

MD5 和 SHA-1 只用于旧协议兼容，不应用于密码存储或新签名设计。

## Regex

~~~lua
local first, last = glua.regex.find("order-[0-9]+", "id=order-42")
local matches = glua.regex.findAll("([a-z]+)=([0-9]+)", "a=1 b=2")
local replaced = glua.regex.replace("[0-9]+", "order-42", "100")
~~~

方法：`match`、`find`、`findAll`、`replace`、`split`。实现使用 Go RE2，不支持反向引用和回溯型表达式；位置使用 Lua 1-based 字节索引。

## UUID

~~~lua
local id = glua.uuid.v4()
assert(glua.uuid.validate(id))
print(glua.uuid.parse(id))
~~~

方法：`v4`、`validate`、`parse`。

## ZIP

ZIP API 只处理内存 table，不读取或写入宿主文件系统：

~~~lua
local archive = glua.zip.compress({
  ["config/app.json"] = '{"ready":true}',
  ["assets/raw.bin"] = "\0\1\2",
}, {
  method = "deflate",
  maxTotalBytes = 16 * 1024 * 1024,
})

local files = glua.zip.decompress(archive)
print(files["config/app.json"])
~~~

方法：`compress`、`decompress`。实现拒绝绝对路径、`..` 穿越、重复名称和超限内容。

## Schema

~~~lua
local schema = {
  type = "object",
  required = { "name" },
  additionalProperties = false,
  properties = {
    name = { type = "string", minLength = 2 },
    port = { type = "integer", minimum = 1, maximum = 65535 },
  },
}

local ok, message, path = glua.schema.validate({
  name = "api",
  port = 8080,
}, schema)

assert(ok, tostring(path) .. ": " .. tostring(message))
~~~

方法：`validate`、`assert`。Schema 不支持远程引用，也不会执行脚本回调。

## Path

`glua.path` 使用宿主平台路径规则，但不访问文件系统：

~~~lua
local config = glua.path.join("config", "profiles", "dev.toml")
local directory, fileName = glua.path.split(config)

print(directory, fileName)
print(glua.path.ext(fileName))
print(glua.path.rel("config", config))
~~~

方法：

- `join`、`clean`、`base`、`dir`、`ext`、`isAbs`。
- `rel`、`split`、`volume`、`toSlash`、`fromSlash`、`match`。
- 常量：`separator`、`listSeparator`。

完整参数、资源预算与安全边界见 [通用工具文档](glua-utilities.md)。
