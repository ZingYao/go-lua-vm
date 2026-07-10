package lua

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestGluaZIPDecompressRejectsUnsafeArchives 验证解压入口拒绝外部恶意 ZIP。
//
// 测试构造路径穿越、重复名称和超限文件三类归档；gluaZIPDecompress 必须返回 Lua error，不能
// 生成部分结果 table 或覆盖已有字段。
func TestGluaZIPDecompressRejectsUnsafeArchives(t *testing.T) {
	// 表驱动构造不同 central directory 和内容组合。
	testCases := []struct {
		name       string
		fileNames  []string
		contents   []string
		maxFileLen int64
	}{
		{name: "path traversal", fileNames: []string{"../escape.txt"}, contents: []string{"bad"}},
		{name: "duplicate name", fileNames: []string{"same.txt", "same.txt"}, contents: []string{"a", "b"}},
		{name: "file limit", fileNames: []string{"large.txt"}, contents: []string{"0123456789"}, maxFileLen: 1},
	}
	for _, testCase := range testCases {
		// 每个用例独立构造完整 ZIP，避免 writer 状态交叉污染。
		t.Run(testCase.name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			for index, fileName := range testCase.fileNames {
				// Create 使用标准 deflate/store 元数据，恶意点只来自名称或预算。
				fileWriter, err := writer.Create(fileName)
				if err != nil {
					// 测试 fixture 构造失败应立即终止。
					t.Fatalf("create ZIP entry failed: %v", err)
				}
				if _, err := fileWriter.Write([]byte(testCase.contents[index])); err != nil {
					// fixture 内容写入失败应立即终止。
					t.Fatalf("write ZIP entry failed: %v", err)
				}
			}
			if err := writer.Close(); err != nil {
				// central directory 构造失败应立即终止。
				t.Fatalf("close ZIP fixture failed: %v", err)
			}
			arguments := []runtime.Value{runtime.StringValue(archive.String())}
			if testCase.maxFileLen > 0 {
				// 单文件限制用 options table 显式收紧。
				options := runtime.NewTable()
				options.RawSetString("maxFileBytes", runtime.IntegerValue(testCase.maxFileLen))
				arguments = append(arguments, runtime.ReferenceValue(runtime.KindTable, options))
			}
			if _, err := gluaZIPDecompress(arguments...); err == nil {
				// 恶意或超限归档必须整体失败。
				t.Fatalf("unsafe ZIP archive was accepted")
			}
		})
	}
}

// TestDoStringGluaUtilityExtensions 验证序列化补强和全部通用扩展公共 API。
//
// 测试不接收外部输入；必须覆盖结构形状、资源限制、TOML、XML 类型映射、codec、hash、regex、
// UUID、ZIP 和 schema，任一 Lua 断言失败都输出错误对象。
func TestDoStringGluaUtilityExtensions(t *testing.T) {
	// 创建完整标准库 State，确保 assert、pcall、type 和 math.type 可用于验收。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 扩展命名空间由 OpenLibs 注册。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	source := `
-- 空数组和空对象必须稳定保持结构形状。
local emptyArray = glua.array()
local emptyObject = glua.object()
assert(glua.json.encode(emptyArray) == "[]")
assert(glua.json.encode(emptyObject) == "{}")
assert(glua.json.encode(glua.json.decode("[]")) == "[]")
assert(glua.json.encode(glua.json.decode("{}")) == "{}")

-- 共用资源预算和数字策略必须生效。
assert(not pcall(glua.json.encode, { nested = {} }, { maxDepth = 1 }))
assert(not pcall(glua.json.encode, "toolong", { maxOutputBytes = 2 }))
assert(not pcall(glua.json.decode, "{}", { maxInputBytes = 1 }))
assert(glua.json.decode("123", { numberMode = "string" }) == "123")
assert(math.type(glua.json.decode("123", { numberMode = "number" })) == "float")
assert(not pcall(glua.json.decode, "1.5", { numberMode = "integer" }))

-- TOML 支持对象、数组和日期时间文本，null 明确拒绝。
local tomlText = glua.toml.encode({ title = "zing", values = { 1, 2 } })
local tomlValue = glua.toml.decode(tomlText)
assert(tomlValue.title == "zing" and tomlValue.values[2] == 2)
local tomlDate = glua.toml.decode("created = 2026-07-10T12:30:00Z\n")
assert(tomlDate.created == "2026-07-10T12:30:00Z")
assert(not pcall(glua.toml.encode, { missing = glua.null }))

-- XML namespace、CDATA 和 typed 类型标记必须可用。
local xmlText = glua.xml.encode({
  code = "001",
  missing = glua.null,
  values = glua.array(),
}, { root = "document", namespace = "urn:glua", typed = true, pretty = true })
assert(xmlText:find('xmlns="urn:glua"', 1, true))
local xmlValue = glua.xml.decode(xmlText, { typed = true })
assert(xmlValue._namespace == "urn:glua")
assert(xmlValue.code == "001" and xmlValue.missing == glua.null)
assert(glua.json.encode(xmlValue.values) == "[]")
local cdata = glua.xml.encode({ _cdata = "<zing>&data" }, { root = "content" })
assert(cdata:find("<![CDATA[<zing>&data]]>", 1, true))
assert(glua.xml.decode(cdata) == "<zing>&data")

-- codec 支持二进制安全的 Base64/Hex 和查询参数 URL 编解码。
assert(glua.codec.base64Encode("zing") == "emluZw==")
assert(glua.codec.base64Decode("emluZw==") == "zing")
assert(glua.codec.hexEncode("zing") == "7a696e67")
assert(glua.codec.hexDecode("7a696e67") == "zing")
assert(glua.codec.urlEncode("a b+c") == "a+b%2Bc")
assert(glua.codec.urlDecode("a+b%2Bc") == "a b+c")

-- hash 输出固定小写十六进制，并提供兼容算法和 HMAC。
assert(glua.hash.md5("zing") == "2f5117cb8211933814c3da646e0e4dde")
assert(glua.hash.sha1("zing") == "20a1c567ff655e597dc680f8cc0d1dc2462e06bf")
assert(glua.hash.sha256("zing") == "bd2e37ad80b24655fc1887ac05c7ca75f5e7eac58294ef3b17996492a09c3004")
assert(glua.hash.sha512("zing") == "9eb7851e4bb9d46f61267233c6eaef7d2996ba2fe0feb9cea402bc05b55e4750bb69a7b169b31c3d9dd3c72e92d42fb3ce55c2b7041222b8e32842668f995c13")
assert(glua.hash.hmac("sha256", "key", "data") == "5031fe3d989c6d1537a013fa6e739da23463fdaec3b70137d828e36ace221bd0")

-- regex 使用 RE2 和 Lua 1-based 字节索引。
assert(glua.regex.match("^z.*g$", "zing"))
local first, last = glua.regex.find("z.", "zing")
assert(first == 1 and last == 2)
local matches = glua.regex.findAll("([a-z]+)([0-9]+)", "ab12 cd34")
assert(#matches == 2 and matches[1].groups[1] == "ab" and matches[2].groups[2] == "34")
assert(glua.regex.replace("[0-9]+", "a1b2", "#", 1) == "a#b2")
local parts = glua.regex.split(",+", "a,b,,c")
assert(#parts == 3 and parts[3] == "c")

-- UUID v4 必须合法，parse 支持无连字符文本并输出 canonical 小写格式。
local identifier = glua.uuid.v4()
assert(glua.uuid.validate(identifier))
assert(identifier:sub(15, 15) == "4")
assert(glua.uuid.parse("550E8400E29B41D4A716446655440000") == "550e8400-e29b-41d4-a716-446655440000")
assert(glua.uuid.parse("bad") == nil)

-- ZIP 只操作内存 table，输出稳定并拒绝危险路径和过小预算。
local files = { ["a.txt"] = "A", ["dir/b.bin"] = "B\0C" }
local archive = glua.zip.compress(files)
assert(archive == glua.zip.compress(files))
local restored = glua.zip.decompress(archive)
assert(restored["a.txt"] == "A" and restored["dir/b.bin"] == "B\0C")
assert(not pcall(glua.zip.compress, { ["../escape"] = "bad" }))
assert(not pcall(glua.zip.decompress, archive, { maxTotalBytes = 1 }))

-- 轻量 schema 支持对象、必填字段、数组、字符串和数值约束。
local schema = {
  type = "object",
  required = { "name", "scores" },
  additionalProperties = false,
  properties = {
    name = { type = "string", minLength = 2, pattern = "^[a-z]+$" },
    scores = { type = "array", minItems = 1, items = { type = "integer", minimum = 0 } },
  },
}
local valid = { name = "zing", scores = { 1, 2 } }
assert(glua.schema.validate(valid, schema))
assert(glua.schema.assert(valid, schema) == valid)
local ok, message, path = glua.schema.validate({ name = "Z", scores = { -1 } }, schema)
assert(not ok and type(message) == "string" and path == "$.name")
local extraOK, _, extraPath = glua.schema.validate({ name = "zing", scores = { 1 }, extra = true }, schema)
assert(not extraOK and extraPath == "$.extra")
local emptyOK, emptyMessage, emptyPath = glua.schema.validate({ name = "zing", scores = {} }, schema)
assert(not emptyOK and type(emptyMessage) == "string" and emptyPath == "$.scores", tostring(emptyOK) .. ":" .. tostring(emptyMessage) .. ":" .. tostring(emptyPath))
local assertOK = pcall(glua.schema.assert, { name = "zing", scores = {} }, schema)
assert(not assertOK)
assert(not pcall(glua.schema.validate, valid, { type = "unknown" }))
`
	if err := DoString(state, source); err != nil {
		// 任一扩展行为失败都输出 Lua 错误对象。
		t.Fatalf("GLua utility extensions failed: %v object=%s", err, runtime.ErrorObject(err).DebugString())
	}
}
