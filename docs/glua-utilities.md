# GLua 通用扩展

完整 `OpenLibs` 会在 `glua` 下注册 codec、hash、regex、uuid、zip 和 schema。全部实现保持纯 Go，不依赖 CGO。

## Codec

```lua
local base64 = glua.codec.base64Encode("zing")
assert(glua.codec.base64Decode(base64) == "zing")

local hex = glua.codec.hexEncode("zing")
assert(glua.codec.hexDecode(hex) == "zing")

local query = glua.codec.urlEncode("a b+c")
assert(glua.codec.urlDecode(query) == "a b+c")
```

API：

- `glua.codec.base64Encode(data [, urlSafe])`
- `glua.codec.base64Decode(text [, urlSafe])`
- `glua.codec.hexEncode(data)`
- `glua.codec.hexDecode(text)`
- `glua.codec.urlEncode(text)`
- `glua.codec.urlDecode(text)`

Base64 默认使用带填充标准字符表；`urlSafe = true` 使用带填充 URL-safe 字符表。Lua string 可以包含任意二进制字节，因此编解码不会经过 UTF-8 转换。

## Hash

```lua
local digest = glua.hash.sha256("payload")
local signature = glua.hash.hmac("sha256", "secret", "payload")
```

API：

- `glua.hash.md5(data)`
- `glua.hash.sha1(data)`
- `glua.hash.sha256(data)`
- `glua.hash.sha512(data)`
- `glua.hash.hmac(algorithm, key, data)`

所有结果都是小写十六进制。HMAC 支持 `md5`、`sha1`、`sha256`、`sha512`。MD5 和 SHA-1 仅用于兼容旧协议或非安全校验，新设计应使用 SHA-256 或 SHA-512；密码存储不应直接使用这些快速摘要算法。

## Regex

`glua.regex` 使用 Go RE2，运行时间可预测，不支持回溯、反向引用和 lookbehind。

```lua
assert(glua.regex.match("^z.*g$", "zing"))

local first, last = glua.regex.find("z.", "zing")
assert(first == 1 and last == 2)

local matches = glua.regex.findAll("([a-z]+)([0-9]+)", "ab12 cd34")
local replaced = glua.regex.replace("[0-9]+", "a1b2", "#", 1)
local parts = glua.regex.split(",+", "a,b,,c")
```

API：

- `glua.regex.match(pattern, text)`
- `glua.regex.find(pattern, text [, init])`
- `glua.regex.findAll(pattern, text [, limit])`
- `glua.regex.replace(pattern, text, replacement [, limit])`
- `glua.regex.split(pattern, text [, limit])`

位置使用 Lua 1-based 字节索引。`findAll` 每项包含 `text`、`start`、`end`、`groups`；未匹配的可选捕获组使用 `glua.null`。

## UUID

```lua
local identifier = glua.uuid.v4()
assert(glua.uuid.validate(identifier))

local canonical = glua.uuid.parse("550E8400E29B41D4A716446655440000")
assert(canonical == "550e8400-e29b-41d4-a716-446655440000")
```

- `glua.uuid.v4()` 使用系统加密安全随机源生成 RFC 4122 UUID v4。
- `glua.uuid.validate(text)` 接受 canonical 36 字符或无连字符 32 字符格式。
- `glua.uuid.parse(text)` 返回小写 canonical 文本，非法时返回 `nil`。

## ZIP

ZIP API 只处理内存中的文件映射，不读取或写入宿主文件系统：

```lua
local archive = glua.zip.compress({
  ["config/main.toml"] = "author = \"zing\"",
  ["assets/raw.bin"] = "\0\1\2",
})

local files = glua.zip.decompress(archive)
assert(files["assets/raw.bin"] == "\0\1\2")
```

API：

- `glua.zip.compress(entries [, options])`
- `glua.zip.decompress(archive [, options])`

options：

- `method = "deflate" | "store"`，默认 `deflate`。
- `level = -2..9`，默认使用 Go deflate 默认级别。
- `maxEntries = 1024`。
- `maxFileBytes = 16777216`，单文件 16 MiB。
- `maxTotalBytes = 67108864`，全部原始内容 64 MiB。
- `maxArchiveBytes = 67108864`，ZIP 二进制 64 MiB。

实现会拒绝绝对路径、反斜杠、NUL、`..` 路径穿越、非规范路径、重复文件名、伪造大小和超限解压内容。目录条目不出现在返回 table 中。

## Schema

轻量 schema 使用 Lua table 表达，不读取远程引用，也不执行脚本回调：

```lua
local schema = {
  type = "object",
  required = { "name", "scores" },
  additionalProperties = false,
  properties = {
    name = {
      type = "string",
      minLength = 2,
      pattern = "^[a-z]+$",
    },
    scores = {
      type = "array",
      minItems = 1,
      items = { type = "integer", minimum = 0 },
    },
  },
}

local ok, message, path = glua.schema.validate(value, schema)
local validated = glua.schema.assert(value, schema)
```

支持字段：

- `type`：`any`、`nil`、`null`、`boolean`、`integer`、`number`、`string`、`table`、`array`、`object`、`function`。
- `nullable`：允许 Lua nil 或 `glua.null` 提前通过。
- `enum`：候选值 table，使用 Lua raw equality。
- 字符串：`minLength`、`maxLength`、`pattern`，长度按 Unicode code point，pattern 使用 RE2。
- 数字：`minimum`、`maximum`。
- 数组：`minItems`、`maxItems`、`items`。
- 对象：`required`、`properties`、`additionalProperties`。

`validate` 成功返回 `true`；数据不匹配返回 `false, message, path`；schema 本身非法时抛出错误。`assert` 成功返回原值，失败时抛出带 `$` 路径的 Lua error。校验固定限制为 128 层和 100000 个节点。

## 回归脚本

构建 `bin/glua` 后执行：

```bash
./scripts/test-glua-utilities.sh
```

也可通过 `GLUA_BIN=/path/to/glua` 验证其他构建产物。
