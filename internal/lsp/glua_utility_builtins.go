package lsp

// gluaUtilityFallbackBuiltins 保存 gluals 未加载扩展 JSON 时仍可用的通用扩展签名。
var gluaUtilityFallbackBuiltins = map[string]builtinFunctionDoc{
	"glua.array":                  gluaUtilityBuiltin("glua.array([table])", "创建或标记结构化数组。", "table (optional): table", "returns: table."),
	"glua.object":                 gluaUtilityBuiltin("glua.object([table])", "创建或标记结构化对象。", "table (optional): table", "returns: table."),
	"glua.toml.encode":            gluaUtilityBuiltin("glua.toml.encode(value [, options])", "编码 TOML 文本。", "value: object table", "returns: TOML string."),
	"glua.toml.decode":            gluaUtilityBuiltin("glua.toml.decode(text [, options])", "解码 TOML 文本。", "text: TOML string", "returns: object table."),
	"glua.codec.base64Encode":     gluaUtilityBuiltin("glua.codec.base64Encode(data [, urlSafe])", "编码 Base64。", "data: binary string", "returns: Base64 string."),
	"glua.codec.base64Decode":     gluaUtilityBuiltin("glua.codec.base64Decode(text [, urlSafe])", "解码 Base64。", "text: Base64 string", "returns: binary string."),
	"glua.codec.hexEncode":        gluaUtilityBuiltin("glua.codec.hexEncode(data)", "编码小写十六进制。", "data: binary string", "returns: hex string."),
	"glua.codec.hexDecode":        gluaUtilityBuiltin("glua.codec.hexDecode(text)", "解码十六进制。", "text: hex string", "returns: binary string."),
	"glua.codec.urlEncode":        gluaUtilityBuiltin("glua.codec.urlEncode(text)", "编码 URL 查询参数文本。", "text: string", "returns: encoded string."),
	"glua.codec.urlDecode":        gluaUtilityBuiltin("glua.codec.urlDecode(text)", "解码 URL 查询参数文本。", "text: encoded string", "returns: decoded string."),
	"glua.codec.base64EncodeFile": gluaUtilityBuiltin("glua.codec.base64EncodeFile(file [, urlSafe])", "从 io file 当前位置读取并编码 Base64。", "file: readable io file", "returns: Base64 string."),
	"glua.codec.base64DecodeFile": gluaUtilityBuiltin("glua.codec.base64DecodeFile(file [, urlSafe])", "从 io file 当前位置读取并解码 Base64。", "file: readable io file", "returns: binary string."),
	"glua.codec.hexEncodeFile":    gluaUtilityBuiltin("glua.codec.hexEncodeFile(file)", "从 io file 当前位置读取并编码小写十六进制。", "file: readable io file", "returns: hex string."),
	"glua.codec.hexDecodeFile":    gluaUtilityBuiltin("glua.codec.hexDecodeFile(file)", "从 io file 当前位置读取并解码十六进制。", "file: readable io file", "returns: binary string."),
	"glua.hash.md5":               gluaUtilityBuiltin("glua.hash.md5(data)", "计算 MD5 小写十六进制摘要，仅用于兼容。", "data: binary string", "returns: hex digest."),
	"glua.hash.sha1":              gluaUtilityBuiltin("glua.hash.sha1(data)", "计算 SHA-1 小写十六进制摘要，仅用于兼容。", "data: binary string", "returns: hex digest."),
	"glua.hash.sha256":            gluaUtilityBuiltin("glua.hash.sha256(data)", "计算 SHA-256 小写十六进制摘要。", "data: binary string", "returns: hex digest."),
	"glua.hash.sha512":            gluaUtilityBuiltin("glua.hash.sha512(data)", "计算 SHA-512 小写十六进制摘要。", "data: binary string", "returns: hex digest."),
	"glua.hash.hmac":              gluaUtilityBuiltin("glua.hash.hmac(algorithm, key, data)", "计算 MD5/SHA 系列 HMAC。", "algorithm: digest name", "returns: hex HMAC."),
	"glua.hash.md5File":           gluaUtilityBuiltin("glua.hash.md5File(file)", "从 io file 当前位置流式计算 MD5，仅用于兼容。", "file: readable io file", "returns: hex digest."),
	"glua.hash.sha1File":          gluaUtilityBuiltin("glua.hash.sha1File(file)", "从 io file 当前位置流式计算 SHA-1，仅用于兼容。", "file: readable io file", "returns: hex digest."),
	"glua.hash.sha256File":        gluaUtilityBuiltin("glua.hash.sha256File(file)", "从 io file 当前位置流式计算 SHA-256。", "file: readable io file", "returns: hex digest."),
	"glua.hash.sha512File":        gluaUtilityBuiltin("glua.hash.sha512File(file)", "从 io file 当前位置流式计算 SHA-512。", "file: readable io file", "returns: hex digest."),
	"glua.hash.hmacFile":          gluaUtilityBuiltin("glua.hash.hmacFile(algorithm, key, file)", "从 io file 当前位置流式计算 HMAC。", "algorithm: digest name", "returns: hex HMAC."),
	"glua.regex.match":            gluaUtilityBuiltin("glua.regex.match(pattern, text)", "判断 RE2 正则是否匹配。", "pattern: RE2 expression", "returns: boolean."),
	"glua.regex.find":             gluaUtilityBuiltin("glua.regex.find(pattern, text [, init])", "返回首个匹配的 Lua 字节范围。", "pattern: RE2 expression", "returns: start, end, or nil."),
	"glua.regex.findAll":          gluaUtilityBuiltin("glua.regex.findAll(pattern, text [, limit])", "返回匹配和捕获组数组。", "pattern: RE2 expression", "returns: match array."),
	"glua.regex.replace":          gluaUtilityBuiltin("glua.regex.replace(pattern, text, replacement [, limit])", "按 RE2 模板替换匹配。", "pattern: RE2 expression", "returns: replaced text."),
	"glua.regex.split":            gluaUtilityBuiltin("glua.regex.split(pattern, text [, limit])", "按 RE2 正则分割文本。", "pattern: RE2 expression", "returns: string array."),
	"glua.uuid.v4":                gluaUtilityBuiltin("glua.uuid.v4()", "生成安全随机 UUID v4。", "", "returns: UUID string."),
	"glua.uuid.validate":          gluaUtilityBuiltin("glua.uuid.validate(text)", "校验 UUID 文本。", "text: UUID string", "returns: boolean."),
	"glua.uuid.parse":             gluaUtilityBuiltin("glua.uuid.parse(text)", "规范化 UUID 文本。", "text: UUID string", "returns: UUID string or nil."),
	"glua.zip.compress":           gluaUtilityBuiltin("glua.zip.compress(entries [, options])", "在内存中压缩安全文件映射。", "entries: file map", "returns: ZIP binary string."),
	"glua.zip.decompress":         gluaUtilityBuiltin("glua.zip.decompress(archive [, options])", "在资源限制内解压 ZIP。", "archive: ZIP binary string", "returns: file map."),
	"glua.path.join":              gluaUtilityBuiltin("glua.path.join(...)", "连接并清理路径片段。", "...: path strings", "returns: platform path."),
	"glua.path.clean":             gluaUtilityBuiltin("glua.path.clean(path)", "清理词法路径。", "path: path string", "returns: cleaned path."),
	"glua.path.base":              gluaUtilityBuiltin("glua.path.base(path)", "返回最后一个路径元素。", "path: path string", "returns: final element."),
	"glua.path.dir":               gluaUtilityBuiltin("glua.path.dir(path)", "返回目录部分。", "path: path string", "returns: directory path."),
	"glua.path.ext":               gluaUtilityBuiltin("glua.path.ext(path)", "返回扩展名。", "path: path string", "returns: extension."),
	"glua.path.isAbs":             gluaUtilityBuiltin("glua.path.isAbs(path)", "判断宿主平台绝对路径。", "path: path string", "returns: boolean."),
	"glua.path.rel":               gluaUtilityBuiltin("glua.path.rel(base, target)", "计算相对路径。", "base: base path", "returns: relative path."),
	"glua.path.split":             gluaUtilityBuiltin("glua.path.split(path)", "拆分目录和文件名。", "path: path string", "returns: dir, file."),
	"glua.path.volume":            gluaUtilityBuiltin("glua.path.volume(path)", "返回平台卷名。", "path: path string", "returns: volume name."),
	"glua.path.toSlash":           gluaUtilityBuiltin("glua.path.toSlash(path)", "转换为正斜杠路径。", "path: path string", "returns: converted path."),
	"glua.path.fromSlash":         gluaUtilityBuiltin("glua.path.fromSlash(path)", "转换为平台分隔符路径。", "path: path string", "returns: converted path."),
	"glua.path.match":             gluaUtilityBuiltin("glua.path.match(pattern, name)", "匹配平台路径模式。", "pattern: path pattern", "returns: boolean."),
	"glua.schema.validate":        gluaUtilityBuiltin("glua.schema.validate(value, schema)", "校验轻量 table schema。", "value: value", "returns: true, or false, message, path."),
	"glua.schema.assert":          gluaUtilityBuiltin("glua.schema.assert(value, schema)", "断言值满足轻量 schema。", "value: value", "returns: original value."),
}

// gluaUtilityBuiltin 构造一个简洁的语言服务 fallback 文档。
//
// signature、description 和 returns 必须非空；parameter 为空表示无参数说明。返回值供包初始化时
// 合并到 builtinFunctionDocs，正式扩展 JSON 可继续覆盖这些条目。
func gluaUtilityBuiltin(signature string, description string, parameter string, returns string) builtinFunctionDoc {
	// 单参数说明转换为切片，无参数时保持空列表。
	parameters := []string{}
	if parameter != "" {
		// fallback 只保留最关键的首个参数。
		parameters = append(parameters, parameter)
	}
	return builtinFunctionDoc{
		Signature:   signature,
		Returns:     returns,
		Parameters:  parameters,
		Description: description,
	}
}

// init 把通用扩展 fallback 和命名空间加入全局 builtin 目录。
func init() {
	// JSON 目录加载前先提供完整签名，加载后由双语条目覆盖。
	for name, document := range gluaUtilityFallbackBuiltins {
		// 同名项使用通用扩展定义。
		builtinFunctionDocs[name] = document
	}
	for _, name := range []string{
		"glua.toml", "glua.codec", "glua.hash", "glua.regex", "glua.uuid", "glua.zip", "glua.path", "glua.path.separator", "glua.path.listSeparator", "glua.schema",
	} {
		// 命名空间作为 constant 参与成员补全和定义跳转。
		appendBuiltinConstant(name)
	}
}
