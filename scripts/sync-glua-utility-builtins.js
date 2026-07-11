#!/usr/bin/env node

const fs = require("fs");
const path = require("path");

// 从仓库脚本目录定位两套扩展目录，确保任意工作目录执行结果一致。
const repositoryRoot = path.resolve(__dirname, "..");
const vscodeCatalogPath = path.join(repositoryRoot, "vscode", "extensions", "glua-lsp", "server", "builtin-functions.json");
const jetbrainsCatalogPath = path.join(repositoryRoot, "jetbrains", "extensions", "glua-lsp", "src", "main", "resources", "builtin-functions.json");

// entry 构造语言服务目录使用的双语条目。
function entry(signature, zhDescription, enDescription, zhParams = [], enParams = [], zhReturns = "返回：无返回值。", enReturns = "returns: no return values.", example = signature) {
  return {
    signature: { en: signature, "zh-CN": signature },
    returns: { en: enReturns, "zh-CN": zhReturns },
    params: { en: enParams, "zh-CN": zhParams },
    description: { en: enDescription, "zh-CN": zhDescription },
    example: { en: example, "zh-CN": example },
  };
}

// constant 构造命名空间或只读常量条目。
function constant(name, zhDescription, enDescription, example = name) {
  return entry(name, zhDescription, enDescription, [], [], "值：GLua 扩展对象。", "value: GLua extension object.", example);
}

// definitions 是通用扩展 API 的唯一编辑器文档来源。
const definitions = {
  "glua.json.encode": entry("glua.json.encode(value [, prettyOrOptions])", "编码 JSON；第二参数兼容 boolean pretty，也可传入包含 pretty、maxDepth、maxNodes、maxOutputBytes 的 options。循环、稀疏和混合键 table 会抛出错误。", "Encodes JSON. The second argument accepts the compatible boolean pretty flag or an options table with limits. Cyclic, sparse, mixed-key tables raise an error.", ["value：可序列化值", "prettyOrOptions（可选）：boolean 或 options table"], ["value: serializable value", "prettyOrOptions (optional): boolean or options table"], "返回：JSON 字符串。", "returns: JSON string."),
  "glua.json.decode": entry("glua.json.decode(text [, options])", "解码单个 JSON 值；支持输入/结构预算和 auto、integer、number、string 数字策略。", "Decodes one JSON value with input/structure budgets and auto, integer, number, or string number modes.", ["text：JSON 字符串", "options（可选）：资源限制和 numberMode"], ["text: JSON string", "options (optional): limits and numberMode"], "返回：Lua 值。", "returns: Lua value."),
  "glua.yaml.encode": entry("glua.yaml.encode(value [, options])", "编码单个 YAML 文档并执行深度、节点和输出限制。", "Encodes one YAML document with depth, node, and output limits.", ["value：可序列化值", "options（可选）：资源限制"], ["value: serializable value", "options (optional): limits"], "返回：YAML 字符串。", "returns: YAML string."),
  "glua.yaml.decode": entry("glua.yaml.decode(text [, options])", "解码单个 YAML 文档；支持输入/结构预算和 numberMode。", "Decodes one YAML document with input/structure budgets and numberMode.", ["text：YAML 字符串", "options（可选）：资源限制和 numberMode"], ["text: YAML string", "options (optional): limits and numberMode"], "返回：Lua 值。", "returns: Lua value."),
  "glua.xml.encode": entry("glua.xml.encode(value [, options])", "编码 XML；支持 root、pretty、namespace、typed、CDATA 和统一资源限制。_attr、_text、_cdata、_namespace 是保留映射字段。", "Encodes XML with root, pretty, namespace, typed markers, CDATA, and shared limits. _attr, _text, _cdata, and _namespace are reserved mapping fields.", ["value：可序列化值", "options（可选）：XML 与资源配置"], ["value: serializable value", "options (optional): XML and limit settings"], "返回：XML 字符串。", "returns: XML string."),
  "glua.xml.decode": entry("glua.xml.decode(text [, options])", "解码 XML；纯 item 子节点和重复同名节点恢复为数组，typed=true 识别 _glua_type 无损类型标记，namespace URI 写入 _namespace。", "Decodes XML; item-only and repeated children become arrays, typed=true reads lossless _glua_type markers, and namespace URIs are exposed as _namespace.", ["text：XML 字符串", "options（可选）：inferTypes、typed 和资源限制"], ["text: XML string", "options (optional): inferTypes, typed, and limits"], "返回：Lua 值。", "returns: Lua value."),
  "glua.array": entry(
    "glua.array([table])",
    "创建空 table 或把已有 table 标记为结构化数组，不修改字段和业务元表；用于稳定编码空数组。",
    "Creates an empty table or marks an existing table as a structured array without changing fields or its metatable.",
    ["table（可选）：需要原地标记的 table"],
    ["table (optional): table to mark in place"],
    "返回：新建或原始 table。",
    "returns: the new or original table.",
    "local values = glua.array()"
  ),
  "glua.object": entry(
    "glua.object([table])",
    "创建空 table 或把已有 table 标记为结构化对象，不修改字段和业务元表；用于稳定编码空对象。",
    "Creates an empty table or marks an existing table as a structured object without changing fields or its metatable.",
    ["table（可选）：需要原地标记的 table"],
    ["table (optional): table to mark in place"],
    "返回：新建或原始 table。",
    "returns: the new or original table.",
    "local object = glua.object()"
  ),
  "glua.toml": constant("glua.toml", "纯 Go TOML 编解码命名空间。", "Pure-Go TOML codec namespace."),
  "glua.toml.encode": entry(
    "glua.toml.encode(value [, options])",
    "编码 TOML 文本，使用统一的深度、节点和输出限制；TOML 不支持 glua.null。",
    "Encodes TOML with shared depth, node, and output limits. TOML does not support glua.null.",
    ["value：对象 table", "options（可选）：资源限制"],
    ["value: object table", "options (optional): resource limits"],
    "返回：TOML 字符串。",
    "returns: TOML string.",
    "local text = glua.toml.encode({ author = 'zing' })"
  ),
  "glua.toml.decode": entry(
    "glua.toml.decode(text [, options])",
    "解码 TOML 文本；日期时间恢复为规范字符串，数字遵守 numberMode。",
    "Decodes TOML; date/time values become canonical strings and numbers follow numberMode.",
    ["text：TOML 字符串", "options（可选）：资源限制和 numberMode"],
    ["text: TOML string", "options (optional): limits and numberMode"],
    "返回：对象 table。",
    "returns: object table.",
    "local value = glua.toml.decode('author = \"zing\"')"
  ),
  "glua.codec": constant("glua.codec", "Base64、Hex 和 URL 编解码命名空间。", "Base64, Hex, and URL codec namespace."),
  "glua.codec.base64Encode": entry("glua.codec.base64Encode(data [, urlSafe])", "把二进制 Lua string 编码为带填充 Base64；urlSafe=true 使用 URL 字符表。", "Encodes a binary Lua string as padded Base64; urlSafe selects the URL alphabet.", ["data：二进制字符串", "urlSafe（可选）：boolean"], ["data: binary string", "urlSafe (optional): boolean"], "返回：Base64 字符串。", "returns: Base64 string.", "glua.codec.base64Encode('zing')"),
  "glua.codec.base64Decode": entry("glua.codec.base64Decode(text [, urlSafe])", "解码带填充 Base64，非法文本抛出错误。", "Decodes padded Base64 and raises on malformed input.", ["text：Base64 字符串", "urlSafe（可选）：boolean"], ["text: Base64 string", "urlSafe (optional): boolean"], "返回：二进制字符串。", "returns: binary string.", "glua.codec.base64Decode('emluZw==')"),
  "glua.codec.hexEncode": entry("glua.codec.hexEncode(data)", "把二进制字符串编码为小写十六进制。", "Encodes a binary string as lowercase hexadecimal.", ["data：二进制字符串"], ["data: binary string"], "返回：十六进制字符串。", "returns: hexadecimal string."),
  "glua.codec.hexDecode": entry("glua.codec.hexDecode(text)", "解码大小写十六进制，奇数长度或非法字符抛出错误。", "Decodes hexadecimal and raises on odd length or invalid characters.", ["text：十六进制字符串"], ["text: hexadecimal string"], "返回：二进制字符串。", "returns: binary string."),
  "glua.codec.urlEncode": entry("glua.codec.urlEncode(text)", "按查询参数规则编码文本，空格编码为加号。", "Query-escapes text and encodes spaces as plus signs.", ["text：字符串"], ["text: string"], "返回：URL 编码字符串。", "returns: URL-encoded string."),
  "glua.codec.urlDecode": entry("glua.codec.urlDecode(text)", "解码查询参数文本，非法百分号转义抛出错误。", "Decodes query-escaped text and raises on invalid percent escapes.", ["text：URL 编码字符串"], ["text: URL-encoded string"], "返回：解码字符串。", "returns: decoded string."),
  "glua.codec.base64EncodeFile": entry("glua.codec.base64EncodeFile(file [, urlSafe])", "从可读 io file 当前位置读取到 EOF 并编码为 Base64；不 seek 也不关闭文件。", "Reads a readable io file from its current position to EOF and encodes Base64 without seeking or closing it.", ["file：io.open 返回的可读 file", "urlSafe（可选）：boolean"], ["file: readable file returned by io.open", "urlSafe (optional): boolean"], "返回：Base64 字符串。", "returns: Base64 string.", "glua.codec.base64EncodeFile(assert(io.open('data.bin', 'rb')))"),
  "glua.codec.base64DecodeFile": entry("glua.codec.base64DecodeFile(file [, urlSafe])", "从可读 io file 当前位置读取 Base64 文本并解码；不 seek 也不关闭文件。", "Reads Base64 text from a readable io file at its current position and decodes it without seeking or closing it.", ["file：io.open 返回的可读 file", "urlSafe（可选）：boolean"], ["file: readable file returned by io.open", "urlSafe (optional): boolean"], "返回：二进制字符串。", "returns: binary string."),
  "glua.codec.hexEncodeFile": entry("glua.codec.hexEncodeFile(file)", "从可读 io file 当前位置读取到 EOF 并编码为小写十六进制；不 seek 也不关闭文件。", "Reads a readable io file from its current position to EOF and encodes lowercase hexadecimal without seeking or closing it.", ["file：io.open 返回的可读 file"], ["file: readable file returned by io.open"], "返回：十六进制字符串。", "returns: hexadecimal string."),
  "glua.codec.hexDecodeFile": entry("glua.codec.hexDecodeFile(file)", "从可读 io file 当前位置读取并解码十六进制；不 seek 也不关闭文件。", "Reads and decodes hexadecimal from a readable io file at its current position without seeking or closing it.", ["file：io.open 返回的可读 file"], ["file: readable file returned by io.open"], "返回：二进制字符串。", "returns: binary string."),
  "glua.hash": constant("glua.hash", "MD5、SHA 和 HMAC 摘要命名空间；MD5/SHA-1 仅用于旧协议兼容。", "MD5, SHA, and HMAC namespace. MD5/SHA-1 are provided only for legacy compatibility."),
  "glua.hash.md5": entry("glua.hash.md5(data)", "返回 MD5 小写十六进制摘要，仅用于兼容校验。", "Returns a lowercase MD5 hex digest for compatibility only.", ["data：二进制字符串"], ["data: binary string"], "返回：32 字符摘要。", "returns: 32-character digest."),
  "glua.hash.sha1": entry("glua.hash.sha1(data)", "返回 SHA-1 小写十六进制摘要，仅用于兼容校验。", "Returns a lowercase SHA-1 hex digest for compatibility only.", ["data：二进制字符串"], ["data: binary string"], "返回：40 字符摘要。", "returns: 40-character digest."),
  "glua.hash.sha256": entry("glua.hash.sha256(data)", "返回 SHA-256 小写十六进制摘要。", "Returns a lowercase SHA-256 hex digest.", ["data：二进制字符串"], ["data: binary string"], "返回：64 字符摘要。", "returns: 64-character digest."),
  "glua.hash.sha512": entry("glua.hash.sha512(data)", "返回 SHA-512 小写十六进制摘要。", "Returns a lowercase SHA-512 hex digest.", ["data：二进制字符串"], ["data: binary string"], "返回：128 字符摘要。", "returns: 128-character digest."),
  "glua.hash.hmac": entry("glua.hash.hmac(algorithm, key, data)", "计算 HMAC，algorithm 支持 md5、sha1、sha256、sha512；新设计推荐 sha256 或 sha512。", "Computes HMAC using md5, sha1, sha256, or sha512; prefer sha256/sha512 for new designs.", ["algorithm：算法名", "key：二进制密钥", "data：二进制数据"], ["algorithm: digest name", "key: binary key", "data: binary data"], "返回：小写十六进制 HMAC。", "returns: lowercase hexadecimal HMAC."),
  "glua.hash.md5File": entry("glua.hash.md5File(file)", "从可读 io file 当前位置流式计算 MD5，仅用于兼容；不 seek 也不关闭文件。", "Streams MD5 from a readable io file at its current position for compatibility only, without seeking or closing it.", ["file：io.open 返回的可读 file"], ["file: readable file returned by io.open"], "返回：32 字符摘要。", "returns: 32-character digest."),
  "glua.hash.sha1File": entry("glua.hash.sha1File(file)", "从可读 io file 当前位置流式计算 SHA-1，仅用于兼容；不 seek 也不关闭文件。", "Streams SHA-1 from a readable io file at its current position for compatibility only, without seeking or closing it.", ["file：io.open 返回的可读 file"], ["file: readable file returned by io.open"], "返回：40 字符摘要。", "returns: 40-character digest."),
  "glua.hash.sha256File": entry("glua.hash.sha256File(file)", "从可读 io file 当前位置流式计算 SHA-256；不 seek 也不关闭文件。", "Streams SHA-256 from a readable io file at its current position without seeking or closing it.", ["file：io.open 返回的可读 file"], ["file: readable file returned by io.open"], "返回：64 字符摘要。", "returns: 64-character digest.", "glua.hash.sha256File(assert(io.open('data.bin', 'rb')))"),
  "glua.hash.sha512File": entry("glua.hash.sha512File(file)", "从可读 io file 当前位置流式计算 SHA-512；不 seek 也不关闭文件。", "Streams SHA-512 from a readable io file at its current position without seeking or closing it.", ["file：io.open 返回的可读 file"], ["file: readable file returned by io.open"], "返回：128 字符摘要。", "returns: 128-character digest."),
  "glua.hash.hmacFile": entry("glua.hash.hmacFile(algorithm, key, file)", "从可读 io file 当前位置流式计算 HMAC；不 seek 也不关闭文件。", "Streams HMAC from a readable io file at its current position without seeking or closing it.", ["algorithm：算法名", "key：二进制密钥", "file：io.open 返回的可读 file"], ["algorithm: digest name", "key: binary key", "file: readable file returned by io.open"], "返回：小写十六进制 HMAC。", "returns: lowercase hexadecimal HMAC."),
  "glua.regex": constant("glua.regex", "基于 Go RE2 的安全正则命名空间，不支持回溯和反向引用。", "Safe regular-expression namespace backed by Go RE2; backtracking and backreferences are unsupported."),
  "glua.regex.match": entry("glua.regex.match(pattern, text)", "判断 RE2 pattern 是否匹配文本任意位置。", "Reports whether an RE2 pattern matches anywhere in text.", ["pattern：RE2 表达式", "text：文本"], ["pattern: RE2 expression", "text: text"], "返回：boolean。", "returns: boolean."),
  "glua.regex.find": entry("glua.regex.find(pattern, text [, init])", "返回首个匹配的 1-based 字节 start/end；未匹配返回 nil。", "Returns 1-based byte start/end for the first match, or nil.", ["pattern：RE2 表达式", "text：文本", "init（可选）：起始字节位置"], ["pattern: RE2 expression", "text: text", "init (optional): starting byte position"], "返回：start、end 或 nil。", "returns: start, end, or nil."),
  "glua.regex.findAll": entry("glua.regex.findAll(pattern, text [, limit])", "返回匹配对象数组，每项包含 text、start、end、groups。", "Returns match objects containing text, start, end, and groups.", ["pattern：RE2 表达式", "text：文本", "limit（可选）：-1 或非负整数"], ["pattern: RE2 expression", "text: text", "limit (optional): -1 or non-negative integer"], "返回：匹配数组。", "returns: match array."),
  "glua.regex.replace": entry("glua.regex.replace(pattern, text, replacement [, limit])", "按 RE2 `$1`/`${name}` 语法替换匹配，可限制替换次数。", "Replaces matches using RE2 `$1`/`${name}` expansion with an optional limit.", ["pattern：RE2 表达式", "text：文本", "replacement：替换模板", "limit（可选）：次数"], ["pattern: RE2 expression", "text: text", "replacement: template", "limit (optional): count"], "返回：替换文本。", "returns: replaced text."),
  "glua.regex.split": entry("glua.regex.split(pattern, text [, limit])", "按 RE2 pattern 分割文本并返回显式数组。", "Splits text by an RE2 pattern and returns an explicit array.", ["pattern：RE2 表达式", "text：文本", "limit（可选）：结果数量"], ["pattern: RE2 expression", "text: text", "limit (optional): result count"], "返回：字符串数组。", "returns: string array."),
  "glua.uuid": constant("glua.uuid", "UUID v4 生成、校验和规范化命名空间。", "UUID v4 generation, validation, and normalization namespace."),
  "glua.uuid.v4": entry("glua.uuid.v4()", "使用系统安全随机源生成 RFC 4122 UUID v4。", "Generates an RFC 4122 UUID v4 using the system cryptographic random source.", [], [], "返回：canonical UUID 字符串。", "returns: canonical UUID string."),
  "glua.uuid.validate": entry("glua.uuid.validate(text)", "校验 36 字符 canonical 或 32 字符无连字符 UUID。", "Validates canonical 36-character or compact 32-character UUID text.", ["text：UUID 文本"], ["text: UUID text"], "返回：boolean。", "returns: boolean."),
  "glua.uuid.parse": entry("glua.uuid.parse(text)", "把合法 UUID 规范为小写 canonical 文本，非法时返回 nil。", "Normalizes valid UUID text to lowercase canonical form, or returns nil.", ["text：UUID 文本"], ["text: UUID text"], "返回：UUID 字符串或 nil。", "returns: UUID string or nil."),
  "glua.zip": constant("glua.zip", "受资源限制的内存 ZIP 压缩/解压命名空间，不访问宿主文件系统。", "Resource-limited in-memory ZIP namespace with no host filesystem access."),
  "glua.zip.compress": entry("glua.zip.compress(entries [, options])", "把文件名到二进制字符串的对象压缩为 ZIP；拒绝危险路径并限制条目、文件、总输入和归档大小。", "Compresses a filename-to-binary-string object into ZIP, rejecting unsafe paths and enforcing size limits.", ["entries：文件映射 table", "options（可选）：method、level 和资源限制"], ["entries: file map table", "options (optional): method, level, and limits"], "返回：ZIP 二进制字符串。", "returns: ZIP binary string."),
  "glua.zip.decompress": entry("glua.zip.decompress(archive [, options])", "把 ZIP 二进制解压为文件映射；拒绝路径穿越、重复名称和超限内容。", "Decompresses ZIP into a file map, rejecting traversal, duplicate names, and oversized content.", ["archive：ZIP 二进制字符串", "options（可选）：资源限制"], ["archive: ZIP binary string", "options (optional): limits"], "返回：文件映射 table。", "returns: file map table."),
  "glua.path": constant("glua.path", "只进行宿主平台词法路径运算且不访问文件系统的命名空间。", "Host-platform lexical path operations with no filesystem access."),
  "glua.path.join": entry("glua.path.join(...)", "连接并清理任意数量的路径片段。", "Joins and cleans any number of path elements.", ["...：路径字符串"], ["...: path strings"], "返回：平台格式路径。", "returns: platform path."),
  "glua.path.clean": entry("glua.path.clean(path)", "返回最短等价词法路径，不检查目标。", "Returns the shortest equivalent lexical path without checking the target.", ["path：路径字符串"], ["path: path string"], "返回：清理后的路径。", "returns: cleaned path."),
  "glua.path.base": entry("glua.path.base(path)", "返回最后一个路径元素。", "Returns the last path element.", ["path：路径字符串"], ["path: path string"], "返回：文件名或末尾元素。", "returns: final path element."),
  "glua.path.dir": entry("glua.path.dir(path)", "返回路径的目录部分。", "Returns the directory portion of a path.", ["path：路径字符串"], ["path: path string"], "返回：目录路径。", "returns: directory path."),
  "glua.path.ext": entry("glua.path.ext(path)", "返回包含前导点的扩展名。", "Returns the final extension including its leading dot.", ["path：路径字符串"], ["path: path string"], "返回：扩展名或空字符串。", "returns: extension or empty string."),
  "glua.path.isAbs": entry("glua.path.isAbs(path)", "按宿主平台规则判断绝对路径。", "Reports whether a path is absolute on the host platform.", ["path：路径字符串"], ["path: path string"], "返回：boolean。", "returns: boolean."),
  "glua.path.rel": entry("glua.path.rel(base, target)", "计算 target 相对于 base 的词法路径，无法跨卷表达时抛错。", "Computes target relative to base and raises when the result cannot be represented across volumes.", ["base：基础路径", "target：目标路径"], ["base: base path", "target: target path"], "返回：相对路径。", "returns: relative path."),
  "glua.path.split": entry("glua.path.split(path)", "拆分目录和最后一个元素，目录保留尾部分隔符。", "Splits a path into directory and final element, retaining the directory separator.", ["path：路径字符串"], ["path: path string"], "返回：dir、file。", "returns: dir, file."),
  "glua.path.volume": entry("glua.path.volume(path)", "返回 Windows 驱动器或 UNC 卷名，其他平台通常为空。", "Returns a Windows drive or UNC volume name and usually empty text elsewhere.", ["path：路径字符串"], ["path: path string"], "返回：卷名字符串。", "returns: volume name string."),
  "glua.path.toSlash": entry("glua.path.toSlash(path)", "把宿主平台分隔符转换为正斜杠。", "Converts host separators to forward slashes.", ["path：路径字符串"], ["path: path string"], "返回：转换后的路径。", "returns: converted path."),
  "glua.path.fromSlash": entry("glua.path.fromSlash(path)", "把正斜杠转换为宿主平台分隔符。", "Converts forward slashes to host separators.", ["path：路径字符串"], ["path: path string"], "返回：转换后的路径。", "returns: converted path."),
  "glua.path.match": entry("glua.path.match(pattern, name)", "按宿主平台路径模式匹配名称，非法模式抛错。", "Matches a name using host path-pattern rules and raises on malformed patterns.", ["pattern：路径模式", "name：待匹配名称"], ["pattern: path pattern", "name: name to match"], "返回：boolean。", "returns: boolean."),
  "glua.path.separator": constant("glua.path.separator", "当前宿主平台目录分隔符。", "Current host directory separator."),
  "glua.path.listSeparator": constant("glua.path.listSeparator", "当前宿主平台路径列表分隔符。", "Current host path-list separator."),
  "glua.schema": constant("glua.schema", "确定性的轻量 table schema 校验命名空间，不支持远程引用。", "Deterministic lightweight table-schema namespace without remote references."),
  "glua.schema.validate": entry("glua.schema.validate(value, schema)", "校验 type、enum、properties、required、additionalProperties、items、长度、范围和 RE2 pattern。数据失败返回 false、message、path，非法 schema 抛错。", "Validates type, enum, properties, required, additionalProperties, items, lengths, ranges, and RE2 patterns. Data mismatches return false/message/path; invalid schemas raise.", ["value：待校验值", "schema：轻量 schema table"], ["value: value to validate", "schema: lightweight schema table"], "返回：true，或 false、message、path。", "returns: true, or false, message, path."),
  "glua.schema.assert": entry("glua.schema.assert(value, schema)", "校验成功返回原值，失败抛出带 `$` 数据路径的 Lua error。", "Returns the original value on success and raises a Lua error with a `$` data path on failure.", ["value：待校验值", "schema：轻量 schema table"], ["value: value to validate", "schema: lightweight schema table"], "返回：原 value。", "returns: original value."),
};

// 读取 VSCode 主目录，合并定义后用同一内容覆盖两套扩展资源。
const catalog = JSON.parse(fs.readFileSync(vscodeCatalogPath, "utf8"));
catalog.functions = catalog.functions || {};
Object.assign(catalog.functions, definitions);

// 事件可靠性配置与运行时一起维护，避免两套编辑器继续展示旧选项。
const syncEvent = catalog.functions["glua.event.setProgress"];
if (syncEvent) {
  syncEvent.params.en[1] = "callback: function(ctx); ctx includes eventId, traceId, optional parentEventId, timing, payload, and listener reliability metadata";
  syncEvent.params["zh-CN"][1] = "callback：function(ctx)；ctx 包含 eventId、traceId、可选 parentEventId、时间、负载和监听器可靠性元数据";
  syncEvent.params.en[2] = "config (optional): filters, once/maxCalls, priority/group, throttleMs, sampleRate, onError, and mutable";
  syncEvent.params["zh-CN"][2] = "config（可选）：过滤器、once/maxCalls、priority/group、throttleMs、sampleRate、onError 和 mutable";
  const reliabilityEn = " Reliability options cap executions, apply deterministic sampling or leading-edge throttling, and choose propagate, ignore, mute, or remove error handling.";
  const reliabilityZh = " 可靠性配置可限制执行次数、执行确定性采样或前沿节流，并选择传播、忽略、静音或删除监听器的错误处置。";
  while (syncEvent.description.en.endsWith(reliabilityEn)) {
    // 先移除旧生成后缀，保证脚本重复执行得到相同目录。
    syncEvent.description.en = syncEvent.description.en.slice(0, -reliabilityEn.length);
  }
  while (syncEvent.description["zh-CN"].endsWith(reliabilityZh)) {
    // 中文说明使用相同幂等规则。
    syncEvent.description["zh-CN"] = syncEvent.description["zh-CN"].slice(0, -reliabilityZh.length);
  }
  syncEvent.description.en += reliabilityEn;
  syncEvent.description["zh-CN"] += reliabilityZh;
}

const asyncEvent = catalog.functions["glua.event.setProgressAsync"];
if (asyncEvent) {
  asyncEvent.params.en[2] = "config (optional): sync listener options plus debounceMs, queueLimit, and overflow";
  asyncEvent.params["zh-CN"][2] = "config（可选）：同步监听器选项，以及 debounceMs、queueLimit 和 overflow";
  asyncEvent.description.en = "Registers an asynchronous listener with the same source scope and preset timing as setProgress. Callbacks run only at VM safe points or flush(). debounceMs coalesces a time window to the latest context without a background goroutine; flush() forces pending debounced tasks. Queue limits provide backpressure, and onError supports propagate, ignore, mute, or remove.";
  asyncEvent.description["zh-CN"] = "注册与 setProgress 作用域和预设时机相同的异步监听器。回调只在 VM 安全点或 flush() 中执行；debounceMs 不启动后台 goroutine，而是在时间窗内只保留最新上下文，flush() 会强制执行待处理防抖任务。队列上限提供背压，onError 支持 propagate、ignore、mute 或 remove。";
}

const output = JSON.stringify(catalog, null, 2) + "\n";
fs.writeFileSync(vscodeCatalogPath, output);
fs.writeFileSync(jetbrainsCatalogPath, output);

process.stdout.write(`synced ${Object.keys(definitions).length} GLua utility builtin entries\n`);
