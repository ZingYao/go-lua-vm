package lua

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	// defaultGluaZipMaxEntries 限制单个 ZIP 的文件条目数量。
	defaultGluaZipMaxEntries = 1024
	// defaultGluaZipMaxFileBytes 限制单个 ZIP 文件的解压后大小。
	defaultGluaZipMaxFileBytes = 16 << 20
	// defaultGluaZipMaxTotalBytes 限制 ZIP 全部文件的解压后总大小。
	defaultGluaZipMaxTotalBytes = 64 << 20
	// defaultGluaZipMaxArchiveBytes 限制 ZIP 二进制本身大小。
	defaultGluaZipMaxArchiveBytes = 64 << 20
)

// gluaZipOptions 保存内存 ZIP 编解码的资源和压缩配置。
type gluaZipOptions struct {
	// maxEntries 是文件条目数量上限。
	maxEntries int
	// maxFileBytes 是单文件原始内容上限。
	maxFileBytes int
	// maxTotalBytes 是所有原始内容总上限。
	maxTotalBytes int
	// maxArchiveBytes 是 ZIP 二进制上限。
	maxArchiveBytes int
	// method 是 deflate 或 store。
	method string
	// level 是 flate 压缩级别。
	level int
}

// registerGluaUtilityGlobals 注册 codec、hash、regex、uuid 和 zip 命名空间。
//
// state 必须已打开全局环境；函数没有返回值。宿主占用非 table 的 glua 全局时跳过注册，所有
// 子 API 使用纯 Go 标准库且不访问宿主文件系统。
func registerGluaUtilityGlobals(state *State) {
	// 无效或关闭 State 没有注册目标。
	if state == nil || state.Globals() == nil {
		// 直接跳过，不覆盖其他 OpenLibs 错误。
		return
	}
	gluaTable := gluaNamespaceTable(state.Globals())
	if gluaTable == nil {
		// 非 table glua 全局属于宿主，保持原值。
		return
	}
	codecTable := runtime.NewTable()
	codecTable.RawSetString("base64Encode", gluaGoFunction(gluaCodecBase64Encode))
	codecTable.RawSetString("base64Decode", gluaGoFunction(gluaCodecBase64Decode))
	codecTable.RawSetString("hexEncode", gluaGoFunction(gluaCodecHexEncode))
	codecTable.RawSetString("hexDecode", gluaGoFunction(gluaCodecHexDecode))
	codecTable.RawSetString("urlEncode", gluaGoFunction(gluaCodecURLEncode))
	codecTable.RawSetString("urlDecode", gluaGoFunction(gluaCodecURLDecode))
	gluaTable.RawSetString("codec", runtime.ReferenceValue(runtime.KindTable, codecTable))

	hashTable := runtime.NewTable()
	hashTable.RawSetString("md5", gluaGoFunction(gluaHashMD5))
	hashTable.RawSetString("sha1", gluaGoFunction(gluaHashSHA1))
	hashTable.RawSetString("sha256", gluaGoFunction(gluaHashSHA256))
	hashTable.RawSetString("sha512", gluaGoFunction(gluaHashSHA512))
	hashTable.RawSetString("hmac", gluaGoFunction(gluaHashHMAC))
	gluaTable.RawSetString("hash", runtime.ReferenceValue(runtime.KindTable, hashTable))

	regexTable := runtime.NewTable()
	regexTable.RawSetString("match", gluaGoFunction(gluaRegexMatch))
	regexTable.RawSetString("find", gluaGoFunction(gluaRegexFind))
	regexTable.RawSetString("findAll", gluaGoFunction(gluaRegexFindAll))
	regexTable.RawSetString("replace", gluaGoFunction(gluaRegexReplace))
	regexTable.RawSetString("split", gluaGoFunction(gluaRegexSplit))
	gluaTable.RawSetString("regex", runtime.ReferenceValue(runtime.KindTable, regexTable))

	uuidTable := runtime.NewTable()
	uuidTable.RawSetString("v4", gluaGoFunction(gluaUUIDV4))
	uuidTable.RawSetString("validate", gluaGoFunction(gluaUUIDValidate))
	uuidTable.RawSetString("parse", gluaGoFunction(gluaUUIDParse))
	gluaTable.RawSetString("uuid", runtime.ReferenceValue(runtime.KindTable, uuidTable))

	zipTable := runtime.NewTable()
	zipTable.RawSetString("compress", gluaGoFunction(gluaZIPCompress))
	zipTable.RawSetString("decompress", gluaGoFunction(gluaZIPDecompress))
	gluaTable.RawSetString("zip", runtime.ReferenceValue(runtime.KindTable, zipTable))

	schemaTable := runtime.NewTable()
	schemaTable.RawSetString("validate", gluaGoFunction(gluaSchemaValidate))
	schemaTable.RawSetString("assert", gluaGoFunction(gluaSchemaAssert))
	gluaTable.RawSetString("schema", runtime.ReferenceValue(runtime.KindTable, schemaTable))
}

// gluaGoFunction 把 Go 多返回函数包装为 runtime Value。
//
// function 必须非 nil；返回 KindGoClosure 值供 namespace table 注册。nil 函数仍会被包装，调用方
// 只在静态注册表中使用该辅助函数。
func gluaGoFunction(function runtime.GoResultsFunction) runtime.Value {
	// 统一构造 Go closure，减少注册代码重复。
	return runtime.ReferenceValue(runtime.KindGoClosure, function)
}

// gluaCodecBase64Encode 编码 Base64 文本。
//
// args 为 data string 和可选 urlSafe boolean；返回带填充的标准或 URL-safe Base64 string。
func gluaCodecBase64Encode(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析二进制字符串和可选 URL-safe 开关。
	data, flag, err := gluaStringAndOptionalBoolean("glua.codec.base64Encode", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	encoding := base64.StdEncoding
	if flag {
		// URL-safe 模式使用 - 和 _ 字符表。
		encoding = base64.URLEncoding
	}
	return []runtime.Value{runtime.StringValue(encoding.EncodeToString([]byte(data)))}, nil
}

// gluaCodecBase64Decode 解码 Base64 文本。
//
// args 为 text string 和可选 urlSafe boolean；返回原始二进制 Lua string，非法编码返回 Lua error。
func gluaCodecBase64Decode(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析文本和字符表开关。
	text, flag, err := gluaStringAndOptionalBoolean("glua.codec.base64Decode", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	encoding := base64.StdEncoding
	if flag {
		// URL-safe 模式使用 URL 字符表。
		encoding = base64.URLEncoding
	}
	decoded, err := encoding.DecodeString(text)
	if err != nil {
		// 非法字符或填充错误通过 Lua error 返回。
		return nil, gluaSerializationError("glua.codec.base64Decode: " + err.Error())
	}
	return []runtime.Value{runtime.StringValue(string(decoded))}, nil
}

// gluaCodecHexEncode 编码小写十六进制文本。
//
// args 必须只有一个二进制 string；返回长度为输入两倍的小写 hex string。
func gluaCodecHexEncode(args ...runtime.Value) ([]runtime.Value, error) {
	// Hex 编码严格要求一个字符串。
	data, err := gluaSingleString("glua.codec.hexEncode", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(hex.EncodeToString([]byte(data)))}, nil
}

// gluaCodecHexDecode 解码十六进制文本。
//
// args 必须只有一个 string；大小写 hex 都接受，奇数长度或非法字符返回 Lua error。
func gluaCodecHexDecode(args ...runtime.Value) ([]runtime.Value, error) {
	// Hex 解码严格要求一个字符串。
	text, err := gluaSingleString("glua.codec.hexDecode", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	decoded, err := hex.DecodeString(text)
	if err != nil {
		// 非法 hex 返回 Lua error。
		return nil, gluaSerializationError("glua.codec.hexDecode: " + err.Error())
	}
	return []runtime.Value{runtime.StringValue(string(decoded))}, nil
}

// gluaCodecURLEncode 使用 application/x-www-form-urlencoded 规则编码文本。
//
// args 必须只有一个 string；返回 QueryEscape 结果，空格编码为加号。
func gluaCodecURLEncode(args ...runtime.Value) ([]runtime.Value, error) {
	// URL 编码严格要求一个字符串。
	text, err := gluaSingleString("glua.codec.urlEncode", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(url.QueryEscape(text))}, nil
}

// gluaCodecURLDecode 解码 application/x-www-form-urlencoded 文本。
//
// args 必须只有一个 string；返回解码文本，非法百分号转义返回 Lua error。
func gluaCodecURLDecode(args ...runtime.Value) ([]runtime.Value, error) {
	// URL 解码严格要求一个字符串。
	text, err := gluaSingleString("glua.codec.urlDecode", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	decoded, err := url.QueryUnescape(text)
	if err != nil {
		// 非法 URL 转义通过 Lua error 返回。
		return nil, gluaSerializationError("glua.codec.urlDecode: " + err.Error())
	}
	return []runtime.Value{runtime.StringValue(decoded)}, nil
}

// gluaHashMD5 返回输入的 MD5 小写十六进制摘要。
//
// args 必须只有一个二进制 string；MD5 仅用于兼容校验，不应作为密码或安全签名算法。
func gluaHashMD5(args ...runtime.Value) ([]runtime.Value, error) {
	// 复用通用摘要入口。
	return gluaHashDigest("glua.hash.md5", md5.New, args)
}

// gluaHashSHA1 返回输入的 SHA-1 小写十六进制摘要。
//
// args 必须只有一个二进制 string；SHA-1 仅用于兼容校验，不应作为新安全签名算法。
func gluaHashSHA1(args ...runtime.Value) ([]runtime.Value, error) {
	// 复用通用摘要入口。
	return gluaHashDigest("glua.hash.sha1", sha1.New, args)
}

// gluaHashSHA256 返回输入的 SHA-256 小写十六进制摘要。
//
// args 必须只有一个二进制 string；返回 64 字符 hex string。
func gluaHashSHA256(args ...runtime.Value) ([]runtime.Value, error) {
	// 复用通用摘要入口。
	return gluaHashDigest("glua.hash.sha256", sha256.New, args)
}

// gluaHashSHA512 返回输入的 SHA-512 小写十六进制摘要。
//
// args 必须只有一个二进制 string；返回 128 字符 hex string。
func gluaHashSHA512(args ...runtime.Value) ([]runtime.Value, error) {
	// 复用通用摘要入口。
	return gluaHashDigest("glua.hash.sha512", sha512.New, args)
}

// gluaHashDigest 执行一个固定摘要算法。
//
// apiName 用于错误消息，constructor 创建 hash.Hash，args 必须是单 string；返回小写 hex 摘要。
func gluaHashDigest(apiName string, constructor func() hash.Hash, args []runtime.Value) ([]runtime.Value, error) {
	// 摘要输入是任意二进制 Lua string。
	data, err := gluaSingleString(apiName, args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	digest := constructor()
	_, _ = digest.Write([]byte(data))
	return []runtime.Value{runtime.StringValue(hex.EncodeToString(digest.Sum(nil)))}, nil
}

// gluaHashHMAC 计算指定 SHA 算法的 HMAC。
//
// args 必须为 algorithm、key、data 三个 string；algorithm 支持 md5、sha1、sha256、sha512，返回
// 小写 hex。MD5/SHA-1 只为协议兼容提供，新设计应选 sha256 或 sha512。
func gluaHashHMAC(args ...runtime.Value) ([]runtime.Value, error) {
	// HMAC 需要算法、密钥和数据三个字符串。
	if len(args) != 3 || args[0].Kind != runtime.KindString || args[1].Kind != runtime.KindString || args[2].Kind != runtime.KindString {
		// 参数形态错误直接返回。
		return nil, gluaSerializationError("glua.hash.hmac expects algorithm, key, and data strings")
	}
	var constructor func() hash.Hash
	switch strings.ToLower(args[0].String) {
	case "md5":
		// 兼容旧协议的 MD5 HMAC。
		constructor = md5.New
	case "sha1":
		// 兼容旧协议的 SHA-1 HMAC。
		constructor = sha1.New
	case "sha256":
		// 推荐的 SHA-256 HMAC。
		constructor = sha256.New
	case "sha512":
		// 高强度 SHA-512 HMAC。
		constructor = sha512.New
	default:
		// 未知算法不能回退。
		return nil, gluaSerializationError("glua.hash.hmac algorithm must be md5, sha1, sha256, or sha512")
	}
	digest := hmac.New(constructor, []byte(args[1].String))
	_, _ = digest.Write([]byte(args[2].String))
	return []runtime.Value{runtime.StringValue(hex.EncodeToString(digest.Sum(nil)))}, nil
}

// gluaRegexMatch 判断 RE2 正则是否匹配文本任意位置。
//
// args 必须为 pattern 和 text string；返回 boolean，非法 pattern 返回 Lua error。
func gluaRegexMatch(args ...runtime.Value) ([]runtime.Value, error) {
	// 编译正则并读取文本。
	expression, text, err := gluaRegexArgs("glua.regex.match", args, 2)
	if err != nil {
		// 参数或 pattern 错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.BooleanValue(expression.MatchString(text))}, nil
}

// gluaRegexFind 返回第一个 RE2 匹配的 Lua 字节范围。
//
// args 为 pattern、text 和可选 init integer；init 使用 Lua 1-based 字节位置。匹配时返回 start、end，
// 未匹配返回 nil；非法范围或 pattern 返回 Lua error。
func gluaRegexFind(args ...runtime.Value) ([]runtime.Value, error) {
	// find 接受二或三个参数。
	if len(args) < 2 || len(args) > 3 {
		// 参数数量错误直接返回。
		return nil, gluaSerializationError("glua.regex.find expects pattern, text, and optional init")
	}
	expression, text, err := gluaRegexArgs("glua.regex.find", args[:2], 2)
	if err != nil {
		// 参数或 pattern 错误直接返回。
		return nil, err
	}
	startOffset := 0
	if len(args) == 3 {
		// init 必须是 1..len+1 的 integer。
		if args[2].Kind != runtime.KindInteger || args[2].Integer < 1 || args[2].Integer > int64(len(text)+1) {
			// 越界位置拒绝，避免切片 panic。
			return nil, gluaSerializationError("glua.regex.find init must be an integer between 1 and text length + 1")
		}
		startOffset = int(args[2].Integer - 1)
	}
	match := expression.FindStringIndex(text[startOffset:])
	if match == nil {
		// 未匹配时返回单个 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	start := startOffset + match[0] + 1
	end := startOffset + match[1]
	return []runtime.Value{runtime.IntegerValue(int64(start)), runtime.IntegerValue(int64(end))}, nil
}

// gluaRegexFindAll 返回所有匹配及捕获组。
//
// args 为 pattern、text 和可选 limit integer；limit=-1 或缺失表示全部，0 返回空数组。每个结果包含
// text、start、end、groups，索引使用 1-based 字节位置。
func gluaRegexFindAll(args ...runtime.Value) ([]runtime.Value, error) {
	// findAll 接受二或三个参数。
	if len(args) < 2 || len(args) > 3 {
		// 参数数量错误直接返回。
		return nil, gluaSerializationError("glua.regex.findAll expects pattern, text, and optional limit")
	}
	expression, text, err := gluaRegexArgs("glua.regex.findAll", args[:2], 2)
	if err != nil {
		// 参数或 pattern 错误直接返回。
		return nil, err
	}
	limit, err := gluaOptionalLimit("glua.regex.findAll", args, 2, -1)
	if err != nil {
		// limit 错误直接返回。
		return nil, err
	}
	indexes := expression.FindAllStringSubmatchIndex(text, limit)
	result := runtime.NewTable()
	result.SetStructuredShape(runtime.TableShapeArray)
	for matchIndex, indexSet := range indexes {
		// 每个匹配生成带捕获组的对象。
		entry := runtime.NewTable()
		entry.SetStructuredShape(runtime.TableShapeObject)
		entry.RawSetString("text", runtime.StringValue(text[indexSet[0]:indexSet[1]]))
		entry.RawSetString("start", runtime.IntegerValue(int64(indexSet[0]+1)))
		entry.RawSetString("end", runtime.IntegerValue(int64(indexSet[1])))
		groups := runtime.NewTable()
		groups.SetStructuredShape(runtime.TableShapeArray)
		for groupIndex := 2; groupIndex < len(indexSet); groupIndex += 2 {
			// 未匹配的可选捕获组使用 glua.null 保留槽位。
			if indexSet[groupIndex] < 0 {
				// null 哨兵保持数组连续。
				groups.RawSetInteger(int64(groupIndex/2), runtime.ReferenceValue(runtime.KindTable, gluaSerializationNull))
			} else {
				// 已匹配组写入原始字节子串。
				groups.RawSetInteger(int64(groupIndex/2), runtime.StringValue(text[indexSet[groupIndex]:indexSet[groupIndex+1]]))
			}
		}
		entry.RawSetString("groups", runtime.ReferenceValue(runtime.KindTable, groups))
		result.RawSetInteger(int64(matchIndex+1), runtime.ReferenceValue(runtime.KindTable, entry))
	}
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, result)}, nil
}

// gluaRegexReplace 使用 RE2 replacement 语法替换匹配。
//
// args 为 pattern、text、replacement 和可选 limit；replacement 支持 `$1`/`${name}`，limit=-1
// 表示全部、0 表示不替换。返回替换后的 string。
func gluaRegexReplace(args ...runtime.Value) ([]runtime.Value, error) {
	// replace 接受三或四个参数，前三个必须是 string。
	if len(args) < 3 || len(args) > 4 || args[0].Kind != runtime.KindString || args[1].Kind != runtime.KindString || args[2].Kind != runtime.KindString {
		// 参数形态错误直接返回。
		return nil, gluaSerializationError("glua.regex.replace expects pattern, text, replacement, and optional limit")
	}
	expression, err := regexp.Compile(args[0].String)
	if err != nil {
		// 非法 RE2 pattern 返回 Lua error。
		return nil, gluaSerializationError("glua.regex.replace: " + err.Error())
	}
	limit, err := gluaOptionalLimit("glua.regex.replace", args, 3, -1)
	if err != nil {
		// limit 错误直接返回。
		return nil, err
	}
	if limit == 0 {
		// 零限制保持原文本。
		return []runtime.Value{runtime.StringValue(args[1].String)}, nil
	}
	replaced := gluaRegexReplaceLimit(expression, args[1].String, args[2].String, limit)
	return []runtime.Value{runtime.StringValue(replaced)}, nil
}

// gluaRegexSplit 按 RE2 pattern 分割文本。
//
// args 为 pattern、text 和可选 limit；返回显式数组 table。limit=-1 表示全部，0 返回空数组，
// 正数表示最多结果数量；非法 pattern 返回 Lua error。
func gluaRegexSplit(args ...runtime.Value) ([]runtime.Value, error) {
	// split 接受二或三个参数。
	if len(args) < 2 || len(args) > 3 {
		// 参数数量错误直接返回。
		return nil, gluaSerializationError("glua.regex.split expects pattern, text, and optional limit")
	}
	expression, text, err := gluaRegexArgs("glua.regex.split", args[:2], 2)
	if err != nil {
		// 参数或 pattern 错误直接返回。
		return nil, err
	}
	limit, err := gluaOptionalLimit("glua.regex.split", args, 2, -1)
	if err != nil {
		// limit 错误直接返回。
		return nil, err
	}
	parts := expression.Split(text, limit)
	result := runtime.NewTable()
	result.SetStructuredShape(runtime.TableShapeArray)
	for index, part := range parts {
		// 分割结果按 1-based 顺序写入。
		result.RawSetInteger(int64(index+1), runtime.StringValue(part))
	}
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, result)}, nil
}

// gluaUUIDV4 使用 crypto/rand 生成 RFC 4122 UUID v4。
//
// args 必须为空；返回小写 canonical UUID string，系统随机源失败时返回 Lua error。
func gluaUUIDV4(args ...runtime.Value) ([]runtime.Value, error) {
	// UUID v4 不接受参数。
	if len(args) != 0 {
		// 多余参数返回错误。
		return nil, gluaSerializationError("glua.uuid.v4 expects no arguments")
	}
	identifier := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, identifier); err != nil {
		// 系统安全随机源失败时不能回退伪随机。
		return nil, gluaSerializationError("glua.uuid.v4: " + err.Error())
	}
	identifier[6] = (identifier[6] & 0x0f) | 0x40
	identifier[8] = (identifier[8] & 0x3f) | 0x80
	return []runtime.Value{runtime.StringValue(formatGluaUUID(identifier))}, nil
}

// gluaUUIDValidate 判断字符串是否为合法 UUID 文本。
//
// args 必须只有一个 string；接受 canonical 36 字符或无连字符 32 字符格式，返回 boolean。
func gluaUUIDValidate(args ...runtime.Value) ([]runtime.Value, error) {
	// validate 严格要求一个字符串。
	text, err := gluaSingleString("glua.uuid.validate", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	_, ok := parseGluaUUID(text)
	return []runtime.Value{runtime.BooleanValue(ok)}, nil
}

// gluaUUIDParse 规范化 UUID 文本。
//
// args 必须只有一个 string；合法时返回小写 canonical UUID，非法时返回 nil，不抛语法错误。
func gluaUUIDParse(args ...runtime.Value) ([]runtime.Value, error) {
	// parse 严格要求一个字符串。
	text, err := gluaSingleString("glua.uuid.parse", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	identifier, ok := parseGluaUUID(text)
	if !ok {
		// 非法 UUID 文本返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.StringValue(formatGluaUUID(identifier))}, nil
}

// gluaZIPCompress 把文件名到二进制字符串的 table 压缩为 ZIP。
//
// args 包含 entries object 和可选 options；返回 ZIP 二进制 Lua string。只允许安全相对路径，
// 默认 deflate；条目数、单文件、总输入和归档输出均受限制。
func gluaZIPCompress(args ...runtime.Value) ([]runtime.Value, error) {
	// ZIP 压缩接受 entries 和可选 options。
	if len(args) < 1 || len(args) > 2 || args[0].Kind != runtime.KindTable {
		// 参数形态错误直接返回。
		return nil, gluaSerializationError("glua.zip.compress expects entries table and optional options")
	}
	options, err := parseGluaZipOptions(args, 1, "glua.zip.compress")
	if err != nil {
		// options 错误直接返回。
		return nil, err
	}
	entriesTable, _ := args[0].Ref.(*runtime.Table)
	entries, totalBytes, err := collectGluaZipEntries(entriesTable, options)
	if err != nil {
		// 文件名、类型或预算错误直接返回。
		return nil, err
	}
	if totalBytes > options.maxTotalBytes {
		// 防御性重复检查总输入上限。
		return nil, gluaSerializationError("glua.zip.compress input exceeds maxTotalBytes")
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	if options.method == "deflate" {
		// 自定义 deflate level 只作用于当前 writer。
		writer.RegisterCompressor(zip.Deflate, func(destination io.Writer) (io.WriteCloser, error) {
			// flate writer 构造错误上传给 archive/zip。
			return flate.NewWriter(destination, options.level)
		})
	}
	for _, entry := range entries {
		// 以排序后的稳定顺序写入文件。
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if options.method == "store" {
			// store 模式不压缩内容。
			header.Method = zip.Store
		}
		header.SetMode(0o600)
		header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
		fileWriter, err := writer.CreateHeader(header)
		if err != nil {
			// header 写入失败时关闭 writer 并返回。
			_ = writer.Close()
			return nil, gluaSerializationError("glua.zip.compress: " + err.Error())
		}
		if _, err := fileWriter.Write([]byte(entry.content)); err != nil {
			// 内容写入失败时关闭 writer 并返回。
			_ = writer.Close()
			return nil, gluaSerializationError("glua.zip.compress: " + err.Error())
		}
	}
	if err := writer.Close(); err != nil {
		// central directory 写入失败返回错误。
		return nil, gluaSerializationError("glua.zip.compress: " + err.Error())
	}
	if output.Len() > options.maxArchiveBytes {
		// 最终 ZIP 超限时不返回二进制。
		return nil, gluaSerializationError("glua.zip.compress output exceeds maxArchiveBytes")
	}
	return []runtime.Value{runtime.StringValue(output.String())}, nil
}

// gluaZIPDecompress 把 ZIP 二进制解压为文件名到二进制字符串的对象 table。
//
// args 包含 ZIP string 和可选 options；返回显式对象。目录条目忽略，危险路径、重复名称、加密或
// 超过条目/单文件/总大小限制的归档返回 Lua error。
func gluaZIPDecompress(args ...runtime.Value) ([]runtime.Value, error) {
	// ZIP 解压接受二进制 string 和可选 options。
	if len(args) < 1 || len(args) > 2 || args[0].Kind != runtime.KindString {
		// 参数形态错误直接返回。
		return nil, gluaSerializationError("glua.zip.decompress expects archive string and optional options")
	}
	options, err := parseGluaZipOptions(args, 1, "glua.zip.decompress")
	if err != nil {
		// options 错误直接返回。
		return nil, err
	}
	archive := []byte(args[0].String)
	if len(archive) > options.maxArchiveBytes {
		// 归档本身超限时不进入 ZIP parser。
		return nil, gluaSerializationError("glua.zip.decompress input exceeds maxArchiveBytes")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		// 非法 ZIP 返回 Lua error。
		return nil, gluaSerializationError("glua.zip.decompress: " + err.Error())
	}
	if len(reader.File) > options.maxEntries {
		// central directory 条目数超限。
		return nil, gluaSerializationError("glua.zip.decompress archive exceeds maxEntries")
	}
	result := runtime.NewTable()
	result.SetStructuredShape(runtime.TableShapeObject)
	seen := make(map[string]bool, len(reader.File))
	totalBytes := 0
	for _, file := range reader.File {
		// 目录条目不产生 Lua 字段。
		if file.FileInfo().IsDir() {
			// 安全目录可跳过，危险目录路径仍必须拒绝。
			directoryName := strings.TrimSuffix(file.Name, "/")
			if err := validateGluaZipName(directoryName); err != nil {
				// 路径穿越目录不能被静默忽略。
				return nil, gluaSerializationError("glua.zip.decompress: " + err.Error())
			}
			continue
		}
		if err := validateGluaZipName(file.Name); err != nil {
			// 路径穿越或非法名称拒绝整个归档。
			return nil, gluaSerializationError("glua.zip.decompress: " + err.Error())
		}
		if seen[file.Name] {
			// 重复文件名会覆盖 table 字段，必须明确拒绝。
			return nil, gluaSerializationError("glua.zip.decompress: duplicate file name " + file.Name)
		}
		seen[file.Name] = true
		if file.Flags&0x1 != 0 {
			// 加密 ZIP 不受 Go 标准库支持，且不能在 API 中请求密码。
			return nil, gluaSerializationError("glua.zip.decompress encrypted entries are not supported: " + file.Name)
		}
		if file.UncompressedSize64 > uint64(options.maxFileBytes) {
			// header 声明的单文件大小已超限。
			return nil, gluaSerializationError("glua.zip.decompress file exceeds maxFileBytes: " + file.Name)
		}
		fileReader, err := file.Open()
		if err != nil {
			// 加密或损坏条目无法打开。
			return nil, gluaSerializationError("glua.zip.decompress: " + err.Error())
		}
		content, readErr := io.ReadAll(io.LimitReader(fileReader, int64(options.maxFileBytes)+1))
		closeErr := fileReader.Close()
		if readErr != nil {
			// CRC 或流读取错误拒绝归档。
			return nil, gluaSerializationError("glua.zip.decompress: " + readErr.Error())
		}
		if closeErr != nil {
			// 条目关闭错误拒绝归档。
			return nil, gluaSerializationError("glua.zip.decompress: " + closeErr.Error())
		}
		if len(content) > options.maxFileBytes {
			// 实际流超过单文件限制，防止伪造 header。
			return nil, gluaSerializationError("glua.zip.decompress file exceeds maxFileBytes: " + file.Name)
		}
		totalBytes += len(content)
		if totalBytes > options.maxTotalBytes {
			// 累计解压大小超限，防止 zip bomb。
			return nil, gluaSerializationError("glua.zip.decompress output exceeds maxTotalBytes")
		}
		result.RawSetString(file.Name, runtime.StringValue(string(content)))
	}
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, result)}, nil
}

// gluaStringAndOptionalBoolean 解析 string 和可选 boolean 参数。
//
// apiName 用于错误消息；args 必须包含一个 string，可选第二个 boolean。返回字符串、开关和错误。
func gluaStringAndOptionalBoolean(apiName string, args []runtime.Value) (string, bool, error) {
	// 只接受一或两个参数。
	if len(args) < 1 || len(args) > 2 || args[0].Kind != runtime.KindString || (len(args) == 2 && args[1].Kind != runtime.KindBoolean) {
		// 参数形态错误直接返回。
		return "", false, gluaSerializationError(apiName + " expects string and optional boolean")
	}
	flag := false
	if len(args) == 2 {
		// 第二参数存在时读取 boolean。
		flag = args[1].Bool
	}
	return args[0].String, flag, nil
}

// gluaSingleString 解析严格单 string 参数。
//
// apiName 用于错误消息；成功返回原始二进制字符串，失败返回 Lua error。
func gluaSingleString(apiName string, args []runtime.Value) (string, error) {
	// 单字符串 API 不接受多余参数。
	if len(args) != 1 || args[0].Kind != runtime.KindString {
		// 参数错误直接返回。
		return "", gluaSerializationError(apiName + " expects one string")
	}
	return args[0].String, nil
}

// gluaRegexArgs 编译 pattern 并返回 text。
//
// apiName 用于错误消息，args 必须包含 expected 个参数且前两个为 string；返回 RE2 表达式和文本。
func gluaRegexArgs(apiName string, args []runtime.Value, expected int) (*regexp.Regexp, string, error) {
	// 当前正则入口都要求 pattern 和 text 两个字符串。
	if len(args) != expected || expected < 2 || args[0].Kind != runtime.KindString || args[1].Kind != runtime.KindString {
		// 参数形态错误直接返回。
		return nil, "", gluaSerializationError(apiName + " expects pattern and text strings")
	}
	expression, err := regexp.Compile(args[0].String)
	if err != nil {
		// Go regexp 使用 RE2 语义，非法表达式返回 Lua error。
		return nil, "", gluaSerializationError(apiName + ": " + err.Error())
	}
	return expression, args[1].String, nil
}

// gluaOptionalLimit 解析指定位置的可选 limit。
//
// apiName 用于错误消息；args 长度不超过 index 时返回 fallback。limit 必须为 -1 或非负 integer，
// 其他负数和非整数返回 Lua error。
func gluaOptionalLimit(apiName string, args []runtime.Value, index int, fallback int) (int, error) {
	// 缺失 limit 使用调用方默认值。
	if len(args) <= index {
		// 默认通常为 -1，表示处理全部匹配。
		return fallback, nil
	}
	value := args[index]
	if value.Kind != runtime.KindInteger || value.Integer < -1 || uint64(value.Integer+1) > uint64(^uint(0)>>1) {
		// 非法 limit 不能传给 regexp API。
		return 0, gluaSerializationError(apiName + " limit must be -1 or a non-negative integer")
	}
	return int(value.Integer), nil
}

// gluaRegexReplaceLimit 执行有限次正则替换。
//
// expression 必须非 nil；text 和 replacement 使用 Go RE2 ExpandString 语义，limit=-1 表示全部。
// 返回替换文本；调用方已校验 limit，因此函数不返回错误。
func gluaRegexReplaceLimit(expression *regexp.Regexp, text string, replacement string, limit int) string {
	// 获取所需数量的匹配及捕获组索引。
	indexes := expression.FindAllStringSubmatchIndex(text, limit)
	if len(indexes) == 0 {
		// 没有匹配时直接返回原文本。
		return text
	}
	result := make([]byte, 0, len(text))
	previousEnd := 0
	for _, indexSet := range indexes {
		// 先复制匹配前原文，再按捕获组展开 replacement。
		result = append(result, text[previousEnd:indexSet[0]]...)
		result = expression.ExpandString(result, replacement, text, indexSet)
		previousEnd = indexSet[1]
	}
	result = append(result, text[previousEnd:]...)
	return string(result)
}

// parseGluaUUID 解析 UUID 的 32 字符或 canonical 文本。
//
// text 可含标准四个连字符；返回 16 字节和 true 表示语法合法，其他形式返回 false。
func parseGluaUUID(text string) ([]byte, bool) {
	// canonical 格式必须在固定位置含连字符。
	if len(text) == 36 {
		// 连字符位置不正确时拒绝。
		if text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
			// 非 canonical 分隔布局无效。
			return nil, false
		}
		text = strings.ReplaceAll(text, "-", "")
	}
	if len(text) != 32 {
		// 无连字符文本必须恰好 32 个 hex 字符。
		return nil, false
	}
	identifier, err := hex.DecodeString(text)
	if err != nil || len(identifier) != 16 {
		// 非 hex 或长度异常无效。
		return nil, false
	}
	return identifier, true
}

// formatGluaUUID 把 16 字节 UUID 格式化为小写 canonical 文本。
//
// identifier 必须至少 16 字节；调用方只传入生成或解析结果。长度不足时返回空字符串。
func formatGluaUUID(identifier []byte) string {
	// 防御长度不足，避免切片 panic。
	if len(identifier) < 16 {
		// 无效输入没有 canonical 表示。
		return ""
	}
	encoded := hex.EncodeToString(identifier[:16])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// parseGluaZipOptions 解析 ZIP options table。
//
// args 可在 optionIndex 位置包含 table；返回带默认限制的配置。支持 maxEntries、maxFileBytes、
// maxTotalBytes、maxArchiveBytes、method 和 level，非法字段返回 Lua error。
func parseGluaZipOptions(args []runtime.Value, optionIndex int, apiName string) (gluaZipOptions, error) {
	// 默认使用 deflate 默认级别和安全资源边界。
	options := gluaZipOptions{
		maxEntries:      defaultGluaZipMaxEntries,
		maxFileBytes:    defaultGluaZipMaxFileBytes,
		maxTotalBytes:   defaultGluaZipMaxTotalBytes,
		maxArchiveBytes: defaultGluaZipMaxArchiveBytes,
		method:          "deflate",
		level:           flate.DefaultCompression,
	}
	if len(args) <= optionIndex {
		// 未提供 options 时返回默认值。
		return options, nil
	}
	if args[optionIndex].Kind != runtime.KindTable {
		// options 必须是 table。
		return options, gluaSerializationError(apiName + " options must be table")
	}
	table, _ := args[optionIndex].Ref.(*runtime.Table)
	if table == nil {
		// 损坏 table 引用不能读取。
		return options, gluaSerializationError(apiName + " options must be valid table")
	}
	var err error
	options.maxEntries, err = gluaPositiveIntegerOption(table, "maxEntries", options.maxEntries, apiName)
	if err != nil {
		// 条目上限错误直接返回。
		return options, err
	}
	options.maxFileBytes, err = gluaPositiveIntegerOption(table, "maxFileBytes", options.maxFileBytes, apiName)
	if err != nil {
		// 单文件上限错误直接返回。
		return options, err
	}
	options.maxTotalBytes, err = gluaPositiveIntegerOption(table, "maxTotalBytes", options.maxTotalBytes, apiName)
	if err != nil {
		// 总大小上限错误直接返回。
		return options, err
	}
	options.maxArchiveBytes, err = gluaPositiveIntegerOption(table, "maxArchiveBytes", options.maxArchiveBytes, apiName)
	if err != nil {
		// 归档上限错误直接返回。
		return options, err
	}
	if method := table.RawGetString("method"); !method.IsNil() {
		// method 必须是 deflate 或 store。
		if method.Kind != runtime.KindString || (method.String != "deflate" && method.String != "store") {
			// 未知压缩方法拒绝。
			return options, gluaSerializationError(apiName + " options.method must be deflate or store")
		}
		options.method = method.String
	}
	if level := table.RawGetString("level"); !level.IsNil() {
		// level 只接受 flate 支持的 -2..9。
		if level.Kind != runtime.KindInteger || level.Integer < flate.HuffmanOnly || level.Integer > flate.BestCompression {
			// 非法级别拒绝。
			return options, gluaSerializationError(apiName + " options.level must be between -2 and 9")
		}
		options.level = int(level.Integer)
	}
	return options, nil
}

// gluaZipEntry 保存一个待写入 ZIP 的文件。
type gluaZipEntry struct {
	// name 是经过安全校验的相对路径。
	name string
	// content 是原始二进制 Lua string。
	content string
}

// collectGluaZipEntries 从 Lua object table 收集并排序 ZIP 文件。
//
// table 必须非 nil；options 提供资源限制。返回稳定名称顺序的条目、总字节数和错误，键和值必须
// 都是 string，危险路径和预算超限返回 Lua error。
func collectGluaZipEntries(table *runtime.Table, options gluaZipOptions) ([]gluaZipEntry, int, error) {
	// nil table 无法遍历。
	if table == nil {
		// 损坏引用返回参数错误。
		return nil, 0, gluaSerializationError("glua.zip.compress expects valid entries table")
	}
	entries := make([]gluaZipEntry, 0)
	totalBytes := 0
	key := runtime.NilValue()
	for {
		// RawNext 读取文件名和值，不触发元方法。
		nextKey, nextValue, ok, err := table.RawNext(key)
		if err != nil {
			// 并发修改或非法迭代状态返回错误。
			return nil, 0, gluaSerializationError("glua.zip.compress: " + err.Error())
		}
		if !ok {
			// 条目收集完成。
			break
		}
		if nextKey.Kind != runtime.KindString || nextValue.Kind != runtime.KindString {
			// ZIP entries 只接受 filename -> binary string。
			return nil, 0, gluaSerializationError("glua.zip.compress entries must map string names to string contents")
		}
		if err := validateGluaZipName(nextKey.String); err != nil {
			// 危险路径拒绝。
			return nil, 0, gluaSerializationError("glua.zip.compress: " + err.Error())
		}
		if len(nextValue.String) > options.maxFileBytes {
			// 单文件输入超限。
			return nil, 0, gluaSerializationError("glua.zip.compress file exceeds maxFileBytes: " + nextKey.String)
		}
		totalBytes += len(nextValue.String)
		if totalBytes > options.maxTotalBytes {
			// 累计输入超限。
			return nil, 0, gluaSerializationError("glua.zip.compress input exceeds maxTotalBytes")
		}
		entries = append(entries, gluaZipEntry{name: nextKey.String, content: nextValue.String})
		if len(entries) > options.maxEntries {
			// 条目数量超限。
			return nil, 0, gluaSerializationError("glua.zip.compress entries exceed maxEntries")
		}
		key = nextKey
	}
	sort.Slice(entries, func(left int, right int) bool {
		// 名称排序保证相同输入生成稳定 central directory 顺序。
		return entries[left].name < entries[right].name
	})
	return entries, totalBytes, nil
}

// validateGluaZipName 校验 ZIP 内文件名不会产生路径穿越。
//
// name 必须是使用正斜杠的非空相对清理路径，不允许绝对路径、反斜杠、NUL、`.`、`..` 或目录尾斜杠。
func validateGluaZipName(name string) error {
	// 空名称、NUL 和 Windows 分隔符拒绝。
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") {
		// 返回不安全名称错误。
		return fmt.Errorf("unsafe ZIP file name %q", name)
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		// 只接受文件相对路径，不接受绝对路径或目录条目。
		return fmt.Errorf("unsafe ZIP file name %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != name {
		// 清理后变化或逃逸根目录都拒绝。
		return fmt.Errorf("unsafe ZIP file name %q", name)
	}
	return nil
}
