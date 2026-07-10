# GLua 序列化

`glua.json`、`glua.yaml`、`glua.xml` 和 `glua.toml` 提供纯 Go 编解码能力，不依赖 CGO，也不受 Event 开关影响。调用 `OpenLibs` 后即可使用。

## 公共数据规则

四种格式共用以下 Lua 值转换规则：

- `nil`、`boolean`、integer、有限 number 和 string 可以直接编码。
- 连续的正整数键 `1..n` 组成数组。
- 只有字符串键的 table 组成对象。
- 空 table 默认按对象编码。
- 循环引用、稀疏数组、混合整数/字符串键以及其他键类型会抛出错误。
- function、userdata 和 thread 没有通用文本表示，编码时会抛出错误。
- NaN、正无穷和负无穷会抛出错误，避免三种格式产生不一致结果。

完整运行时还提供 TOML，因此当前共有 JSON、YAML、XML、TOML 四种文本格式。

## 结构形状

空 Lua table 无法自动判断应编码为数组还是对象。使用以下方法显式标记，并且不会修改业务元表：

```lua
local array = glua.array()
local object = glua.object()

assert(glua.json.encode(array) == "[]")
assert(glua.json.encode(object) == "{}")

local existing = {}
assert(glua.array(existing) == existing)
```

解码得到的数组和对象会自动携带结构标记，因此空容器再次编码时不会改变形状。

## 资源限制

四种格式共用以下 options，默认值适用于普通配置文件：

- `maxDepth = 128`：最大嵌套层数。
- `maxNodes = 100000`：标量和容器节点总数。
- `maxInputBytes = 16777216`：解码输入上限，16 MiB。
- `maxOutputBytes = 16777216`：编码输出上限，16 MiB。
- `numberMode = "auto"`：解码数字策略，可选 `auto`、`integer`、`number`、`string`。

每次调用都可独立设置，不使用进程级可变配置：

```lua
local value = glua.json.decode(text, {
  maxInputBytes = 1024 * 1024,
  maxDepth = 32,
  numberMode = "string",
})
```

Lua 的 `nil` 会删除 table 字段，也无法占据数组槽位。为保留 JSON/YAML 的 `null`，运行时提供只读单例 `glua.null`：

```lua
local value = glua.json.decode('{"items":[1,null,3]}')
assert(value.items[2] == glua.null)
assert(glua.json.null == glua.null)
assert(glua.yaml.null == glua.null)
assert(glua.xml.null == glua.null)
```

## JSON

```lua
local compact = glua.json.encode({ name = "zing", enabled = true })
local pretty = glua.json.encode({ name = "zing" }, true)
local value = glua.json.decode(compact)
```

- `glua.json.encode(value [, prettyOrOptions])`：返回 JSON 字符串；第二参数兼容原 boolean，也可传 `{ pretty = true, ... }`。
- `glua.json.decode(text [, options])`：严格解码一个 JSON 值；尾随第二个值或非法内容会抛出错误。
- 范围允许时，未带小数和指数的 JSON number 恢复为 Lua integer；其他数值恢复为 Lua number。
- JSON `null` 恢复为 `glua.null`。

## YAML

```lua
local text = glua.yaml.encode({ name = "zing", values = { 1, 2 } })
local value = glua.yaml.decode(text)
```

- `glua.yaml.encode(value [, options])`：编码一个 YAML 文档并返回字符串。
- `glua.yaml.decode(text [, options])`：只接受一个 YAML 文档，多个 `---` 文档会抛出错误。
- YAML mapping 的键必须是字符串；非字符串键不会自动转换。
- YAML `null` 恢复为 `glua.null`。

## XML

XML 不能天然无损表达 Lua table，因此 GLua 使用固定映射：

- 默认根元素为 `<root>`，可通过 `options.root` 修改。
- 对象键映射为同名子元素。
- 数组元素映射为 `<item>`。
- `_attr` table 映射为当前元素的属性。
- `_text` 映射为当前元素的字符数据。
- `_cdata` string 映射为 CDATA，不能与 `_text` 同时出现，也不能包含 `]]>`。
- `_namespace` string 为当前元素设置 namespace URI；`options.namespace` 设置根元素默认 namespace。
- 只包含 `<item>` 的子元素集合恢复为数组。
- 重复同名子元素恢复为数组。

```lua
local text = glua.xml.encode({
  user = {
    _attr = { id = 7 },
    name = "zing",
  },
  values = { 1, 2 },
}, {
  root = "document",
  pretty = true,
})

local value = glua.xml.decode(text)
assert(value.user._attr.id == 7)
assert(value.values[2] == 2)
```

API：

- `glua.xml.encode(value [, options])`
  - `options.root`：根元素名，默认 `root`。
  - `options.pretty`：是否使用两空格缩进，默认 `false`。
  - `options.namespace`：根元素默认 namespace URI。
  - `options.typed`：是否写入 `_glua_type` 属性保留 null、字符串、数字和空容器类型。
- `glua.xml.decode(text [, options])`
  - `options.inferTypes`：是否把 `true`、`false`、integer 和 number 文本恢复为对应 Lua 类型，默认 `true`。
  - `options.typed`：是否识别 `_glua_type` 类型标记。

XML 元素名和属性名当前接受保守的 ASCII XML Name 子集：首字符为字母或下划线，后续可包含字母、数字、下划线、连字符和点。非法名称会抛出错误。

XML 空元素无法区分空字符串与 null；默认解码为空字符串。文本 `"001"` 在类型推断开启时会恢复为 integer `1`，需要保持原文本时关闭推断：

```lua
local value = glua.xml.decode("<root><code>001</code></root>", {
  inferTypes = false,
})
assert(value.code == "001")
```

## TOML

```lua
local text = glua.toml.encode({
  author = "zing",
  values = { 1, 2 },
})

local value = glua.toml.decode(text)
assert(value.author == "zing")
```

- `glua.toml.encode(value [, options])`：编码对象 table；TOML 没有 null，遇到 `glua.null` 会抛出错误。
- `glua.toml.decode(text [, options])`：日期、时间和日期时间恢复为规范字符串，数字遵守 `numberMode`。

## 错误处理

所有错误都使用 Lua error，可通过 `pcall` 或 `xpcall` 捕获：

```lua
local cycle = {}
cycle.self = cycle

local ok, err = pcall(glua.json.encode, cycle)
assert(not ok)
print(err)
```

错误消息会包含 API 名或 table 路径，便于定位循环引用、非法键和不支持的值。

## 独立回归脚本

先构建 `bin/glua`，然后执行：

```bash
./scripts/test-glua-serialization.sh
```

也可以通过 `GLUA_BIN=/path/to/glua` 验证其他构建产物。
