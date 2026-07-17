package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/compiler/lexer"
	"github.com/ZingYao/go-lua-vm/extensions"
)

// TestAnalyzeDiagnosticsAcceptsExtensions 验证默认扩展语法不会产生标准 Lua 误报。
func TestAnalyzeDiagnosticsAcceptsExtensions(t *testing.T) {
	if !extensions.Default().Has(extensions.SyntaxContinue | extensions.SyntaxSwitch) {
		// lua53 构建不包含扩展语法，扩展诊断用例在该模式下不执行。
		t.Skip("syntax extensions are not compiled")
	}
	source := "while true do\nswitch 1 do\ncase 1\ncontinue\nend\nend\n"
	diagnostics := analyzeDiagnostics(source, extensions.Default())
	if len(diagnostics) != 0 {
		// switch/continue 默认扩展开启时应被 glua parser 接受。
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
}

// TestAnalyzeDiagnosticsAcceptsMemberFunctionCall 验证点号成员调用不会被误判为缺少赋值等号。
func TestAnalyzeDiagnosticsAcceptsMemberFunctionCall(t *testing.T) {
	// console.log 与普通 table 成员调用都是合法 Lua 函数调用语句。
	diagnostics := analyzeDiagnostics("local name = 'zing'\nprint(\"AutoGo IDEA remote engine acceptance\")\nprint('zing')\nconsole.log('log')\nconsole.info('info')\nconsole.debug('debug')\nconsole.warn('warn')\nconsole.error('error')\n\n", extensions.Default())
	if len(diagnostics) != 0 {
		// 合法调用不得在 EOF 处产生 expected operator "=" 等伪诊断。
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
}

// TestAnalyzeDiagnosticsRejectsDuplicateSwitchCaseValue 验证 PLS 会诊断 switch 内重复 case 值。
func TestAnalyzeDiagnosticsRejectsDuplicateSwitchCaseValue(t *testing.T) {
	if !extensions.Default().Has(extensions.SyntaxSwitch) {
		// lua53 构建不包含 switch 扩展，扩展诊断用例在该模式下不执行。
		t.Skip("switch syntax extension is not compiled")
	}
	source := "switch 1 do\ncase 1, 2\nprint('x')\ncase 2\nprint('y')\nend\n"
	diagnostics := analyzeDiagnostics(source, extensions.Default())
	if len(diagnostics) == 0 {
		// 重复 case 值必须通过 PLS 展示为错误。
		t.Fatalf("diagnostics should not be empty")
	}
	if !strings.Contains(diagnostics[0].Message, "duplicate switch case value") {
		// 诊断消息应直接说明重复 case 值。
		t.Fatalf("diagnostic message = %q", diagnostics[0].Message)
	}
}

// TestAnalyzeDiagnosticsRejectsDisabledExtensions 验证关闭扩展后会产生诊断。
func TestAnalyzeDiagnosticsRejectsDisabledExtensions(t *testing.T) {
	diagnostics := analyzeDiagnostics("while true do continue end\n", extensions.None())
	if len(diagnostics) == 0 {
		// lua53 模式下 continue 语法糖必须被诊断。
		t.Fatalf("diagnostics should not be empty")
	}
	if !strings.Contains(diagnostics[0].Message, "syntax error") && !strings.Contains(diagnostics[0].Message, "expected") {
		// 诊断消息应能指向语法错误。
		t.Fatalf("diagnostic message = %q", diagnostics[0].Message)
	}
}

// TestAnalyzeDiagnosticsRejectsDisabledConst 验证关闭 const 语法糖后会产生诊断。
func TestAnalyzeDiagnosticsRejectsDisabledConst(t *testing.T) {
	diagnostics := analyzeDiagnostics("const answer = 42\n", extensions.None())
	if len(diagnostics) == 0 {
		// lua53 模式下 const 语法糖必须被诊断。
		t.Fatalf("diagnostics should not be empty")
	}
	if !strings.Contains(diagnostics[0].Message, "syntax error") && !strings.Contains(diagnostics[0].Message, "expected") {
		// 诊断消息应能指向语法错误。
		t.Fatalf("diagnostic message = %q", diagnostics[0].Message)
	}
}

// TestAnalyzeDiagnosticsRejectsConstAssignment 验证 PLS 会诊断 const 语义赋值错误。
func TestAnalyzeDiagnosticsRejectsConstAssignment(t *testing.T) {
	if !extensions.Default().Has(extensions.SyntaxConst) {
		// 当前构建不包含 const 扩展时，正向 const 语义用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// const 重新赋值属于 codegen 语义错误，不能只依赖 parser 诊断。
	diagnostics := analyzeDiagnostics("const answer = 42\nanswer = 7\n", extensions.Default())
	if len(diagnostics) == 0 {
		// const 覆盖必须通过 PLS 展示为错误。
		t.Fatalf("diagnostics should not be empty")
	}
	if diagnostics[0].Range.Start.Line != 1 || !strings.Contains(diagnostics[0].Message, "cannot assign to const binding 'answer'") {
		// 诊断应定位到赋值行，并说明 const 覆盖。
		t.Fatalf("diagnostic = %#v", diagnostics[0])
	}
}

// TestAnalyzeDiagnosticsRejectsConstTopLevelLocalShadow 验证 PLS 会诊断顶层 local 遮蔽全局 const。
func TestAnalyzeDiagnosticsRejectsConstTopLevelLocalShadow(t *testing.T) {
	if !extensions.Default().Has(extensions.SyntaxConst) {
		// 当前构建不包含 const 扩展时，正向 const 语义用例不执行。
		t.Skip("const syntax extension is not compiled")
	}
	// 顶层 local 同名声明会按 ROOT 重新定义处理，必须走 codegen 语义错误。
	diagnostics := analyzeDiagnostics("const answer = 42\nlocal answer = 7\n", extensions.Default())
	if len(diagnostics) == 0 {
		// const 顶层遮蔽必须通过 PLS 展示为错误。
		t.Fatalf("diagnostics should not be empty")
	}
	if diagnostics[0].Range.Start.Line != 1 || !strings.Contains(diagnostics[0].Message, "cannot assign to const binding 'answer'") {
		// 诊断应定位到 local 遮蔽行，并说明 const 覆盖。
		t.Fatalf("diagnostic = %#v", diagnostics[0])
	}
}

// TestServerInitializeAndFormatting 验证 LSP 初始化、打开文档和格式化请求。
func TestServerInitializeAndFormatting(t *testing.T) {
	if !extensions.Default().Has(extensions.SyntaxSwitch) {
		// lua53 构建不包含 switch 扩展，扩展语法格式化用例在该模式下不执行。
		t.Skip("switch syntax extension is not compiled")
	}
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"initializationOptions":{"syntax":"extended"}}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///test.glua","text":"local a={1,2}\nswitch a[1] do\ncase 1\nprint('x')\nend\n"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":"file:///test.glua"}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	err := server.Run(context.Background())
	if err != nil {
		// 完整请求序列应正常处理并在 exit 后退出。
		t.Fatalf("Run failed: %v", err)
	}

	messages := decodeFrames(t, output.Bytes())
	if len(messages) < 4 {
		// initialize 响应、publishDiagnostics、formatting 响应、shutdown 响应都应写出。
		t.Fatalf("messages = %#v", messages)
	}
	foundDiagnostics := false
	for _, message := range messages {
		if message["method"] != "textDocument/publishDiagnostics" {
			continue
		}
		foundDiagnostics = true
		params, ok := message["params"].(map[string]any)
		if !ok {
			t.Fatalf("publishDiagnostics params = %#v", message["params"])
		}
		if diagnostics, ok := params["diagnostics"].([]any); !ok || diagnostics == nil {
			t.Fatalf("publishDiagnostics diagnostics must be a JSON array, got %#v", params["diagnostics"])
		}
	}
	if !foundDiagnostics {
		t.Fatal("publishDiagnostics notification was not emitted")
	}
	formatResponse := findResponse(t, messages, float64(2))
	resultBytes, err := json.Marshal(formatResponse["result"])
	if err != nil {
		// 测试读取响应结果必须可编码。
		t.Fatalf("marshal result failed: %v", err)
	}
	if !strings.Contains(string(resultBytes), "local a = {1, 2}") || !strings.Contains(string(resultBytes), "case 1") {
		// formatting 响应应包含格式化后的全文替换 edit。
		t.Fatalf("format result = %s", resultBytes)
	}
}

// TestServerDefinitionAndSemanticTokens 验证点击跳转和语义高亮请求。
func TestServerDefinitionAndSemanticTokens(t *testing.T) {
	source := "local value = 1\nprint(value)\n"
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///jump.glua","text":"`+strings.ReplaceAll(source, "\n", `\n`)+`"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///jump.glua"},"position":{"line":1,"character":7}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/semanticTokens/full","params":{"textDocument":{"uri":"file:///jump.glua"}}}`) +
		frame(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		// 完整请求序列应正常处理并退出。
		t.Fatalf("Run failed: %v", err)
	}

	messages := decodeFrames(t, output.Bytes())
	definitionResponse := findResponse(t, messages, float64(2))
	definitionBytes, err := json.Marshal(definitionResponse["result"])
	if err != nil {
		// 测试读取 definition 结果必须可编码。
		t.Fatalf("marshal definition failed: %v", err)
	}
	if !strings.Contains(string(definitionBytes), `"line":0`) || !strings.Contains(string(definitionBytes), `"character":6`) {
		// value 的定义应跳回第一行 local value 的名称位置。
		t.Fatalf("definition result = %s", definitionBytes)
	}

	tokensResponse := findResponse(t, messages, float64(3))
	tokenBytes, err := json.Marshal(tokensResponse["result"])
	if err != nil {
		// 测试读取 semantic token 结果必须可编码。
		t.Fatalf("marshal semantic tokens failed: %v", err)
	}
	if !strings.Contains(string(tokenBytes), `"data"`) || strings.Contains(string(tokenBytes), `"data":[]`) {
		// semanticTokens/full 应返回非空 token 数据。
		t.Fatalf("semantic tokens result = %s", tokenBytes)
	}
}

// TestServerEmptySuccessResponsesContainNullResult 验证空 hover、definition 和 shutdown 响应仍符合 JSON-RPC。
func TestServerEmptySuccessResponsesContainNullResult(t *testing.T) {
	// 对不存在符号发起请求，三个成功响应都必须显式返回 result:null。
	source := "local value = 1\n"
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///empty-result.glua","text":"`+strings.ReplaceAll(source, "\n", `\n`)+`"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///empty-result.glua"},"position":{"line":0,"character":20}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///empty-result.glua"},"position":{"line":0,"character":20}}}`) +
		frame(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		// 空成功响应不得导致服务退出异常。
		t.Fatalf("Run failed: %v", err)
	}

	messages := decodeFrames(t, output.Bytes())
	for _, responseID := range []float64{2, 3, 4} {
		// 每个目标响应都必须存在 result 键且值为 nil。
		response := findResponse(t, messages, responseID)
		result, hasResult := response["result"]
		if !hasResult || result != nil {
			// 缺少 result 会被 VS Code LanguageClient 判定为畸形响应。
			t.Fatalf("response id=%v result=%#v hasResult=%v response=%#v", responseID, result, hasResult, response)
		}
		if _, hasError := response["error"]; hasError {
			// 成功响应不能同时携带 error。
			t.Fatalf("response id=%v unexpectedly contains error: %#v", responseID, response)
		}
	}
}

// TestServerErrorResponseOmitsResult 验证 JSON-RPC 错误响应不会同时携带 result。
func TestServerErrorResponseOmitsResult(t *testing.T) {
	// 未知请求应返回 method not found，并且只包含 error。
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"unknown/method"}`)
	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		// 输入在 EOF 结束时属于正常服务退出。
		t.Fatalf("Run failed: %v", err)
	}
	messages := decodeFrames(t, output.Bytes())
	response := findResponse(t, messages, float64(1))
	if _, hasResult := response["result"]; hasResult {
		// 失败响应出现 result 会违反 JSON-RPC 二选一约束。
		t.Fatalf("error response unexpectedly contains result: %#v", response)
	}
	if _, hasError := response["error"]; !hasError {
		// 未知方法必须返回结构化 error。
		t.Fatalf("error response missing error: %#v", response)
	}
}

// TestScanTokensUsesRawUTF16Ranges 验证语义高亮使用原始字符串边界和 UTF-16 列坐标。
func TestScanTokensUsesRawUTF16Ranges(t *testing.T) {
	// 同时覆盖 ASCII 字符串、中文字符串、emoji 后续 token 和多行长字符串。
	source := "local ascii = \"test.custom.async\"\n" +
		"local chinese = \"中文\"\n" +
		"local emoji = \"😀\"; print(emoji)\n" +
		"local long = [[first\nsecond]]\n"
	tokens := scanTokens(source)
	matchedASCII := false
	matchedChinese := false
	matchedPrint := false
	matchedLong := false
	for _, token := range tokens {
		switch {
		case token.Kind == lexer.TokenString && token.Text == "test.custom.async":
			// 引号必须包含在 semantic token 内，避免最后一个字符回退到 TextMate 颜色。
			matchedASCII = true
			if token.Range.Start != (lspPosition{Line: 0, Character: 14}) || token.Range.End != (lspPosition{Line: 0, Character: 33}) {
				// ASCII 原始字面量长度应为十九个 UTF-16 code unit。
				t.Fatalf("ASCII string range = %#v", token.Range)
			}
		case token.Kind == lexer.TokenString && token.Text == "中文":
			// 中文 BMP 字符各占一个 UTF-16 code unit。
			matchedChinese = true
			if token.Range.Start != (lspPosition{Line: 1, Character: 16}) || token.Range.End != (lspPosition{Line: 1, Character: 20}) {
				// 中文字符串范围必须同时覆盖两侧引号。
				t.Fatalf("Chinese string range = %#v", token.Range)
			}
		case token.Kind == lexer.TokenIdentifier && token.Text == "print":
			// emoji 占两个 UTF-16 code unit，因此其后的 print 从第 20 列开始。
			matchedPrint = true
			if token.Range.Start != (lspPosition{Line: 2, Character: 20}) {
				// 起始列错误会让后续整行语义高亮发生偏移。
				t.Fatalf("print range = %#v", token.Range)
			}
		case token.Kind == lexer.TokenString && token.Text == "first\nsecond":
			// 多行长字符串必须保留真实跨行范围，供 semantic token 层跳过。
			matchedLong = true
			if token.Range.Start.Line != 3 || token.Range.End.Line != 4 {
				// 长字符串错误地压成单行会生成非法 LSP semantic token。
				t.Fatalf("long string range = %#v", token.Range)
			}
		}
	}
	if !matchedASCII || !matchedChinese || !matchedPrint || !matchedLong {
		// 任一目标 token 缺失都表示回归样本没有真正覆盖对应路径。
		t.Fatalf("matched ascii=%v chinese=%v print=%v long=%v", matchedASCII, matchedChinese, matchedPrint, matchedLong)
	}
}

func TestServerBuiltinDefinitionAndHover(t *testing.T) {
	source := "print(1)\n"
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///builtin.glua","text":"`+strings.ReplaceAll(source, "\n", `\n`)+`"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///builtin.glua"},"position":{"line":0,"character":0}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///builtin.glua"},"position":{"line":0,"character":0}}}`) +
		frame(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`)

	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	messages := decodeFrames(t, output.Bytes())
	definitionResponse := findResponse(t, messages, float64(2))
	definitionBytes, err := json.Marshal(definitionResponse["result"])
	if err != nil {
		t.Fatalf("marshal definition failed: %v", err)
	}
	if !strings.Contains(string(definitionBytes), `glua-builtin:///print.lua`) {
		t.Fatalf("definition result = %s", definitionBytes)
	}

	hoverResponse := findResponse(t, messages, float64(3))
	hoverBytes, err := json.Marshal(hoverResponse["result"])
	if err != nil {
		t.Fatalf("marshal hover failed: %v", err)
	}
	if !strings.Contains(string(hoverBytes), "print(...)") || !strings.Contains(string(hoverBytes), "**Parameters**") {
		t.Fatalf("hover result = %s", hoverBytes)
	}
}

// TestServerBuiltinConstantCompletionAndDefinition 验证事件常量由 Go LSP 统一分类和跳转。
func TestServerBuiltinConstantCompletionAndDefinition(t *testing.T) {
	// 使用真实 LSP 帧覆盖 capability、补全类型、hover 和常量定义 URI。
	source := "local event = glua.event.events.progress_function_error\nglua.event.events.progress_function_"
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///constant.glua","text":"`+strings.ReplaceAll(source, "\n", `\n`)+`"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"file:///constant.glua"},"position":{"line":1,"character":36}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///constant.glua"},"position":{"line":0,"character":45}}}`) +
		frame(`{"jsonrpc":"2.0","id":4,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///constant.glua"},"position":{"line":0,"character":45}}}`) +
		frame(`{"jsonrpc":"2.0","id":5,"method":"shutdown"}`)

	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		// 完整请求序列必须正常完成。
		t.Fatalf("Run failed: %v", err)
	}

	messages := decodeFrames(t, output.Bytes())
	initializeBytes, err := json.Marshal(findResponse(t, messages, float64(1))["result"])
	if err != nil {
		// 初始化结果必须可编码后检查 capability。
		t.Fatalf("marshal initialize failed: %v", err)
	}
	if !strings.Contains(string(initializeBytes), `"completionProvider"`) {
		// 客户端依赖 capability 决定是否向 Go 服务请求补全。
		t.Fatalf("initialize result = %s", initializeBytes)
	}

	completionBytes, err := json.Marshal(findResponse(t, messages, float64(2))["result"])
	if err != nil {
		// 补全结果必须是合法 JSON。
		t.Fatalf("marshal completion failed: %v", err)
	}
	if !strings.Contains(string(completionBytes), `"label":"progress_function_error"`) || !strings.Contains(string(completionBytes), `"kind":21`) {
		// 事件成员必须以 Constant 类型返回，不能退化为 Function。
		t.Fatalf("completion result = %s", completionBytes)
	}

	definitionBytes, err := json.Marshal(findResponse(t, messages, float64(3))["result"])
	if err != nil {
		// 定义结果必须是合法 JSON。
		t.Fatalf("marshal definition failed: %v", err)
	}
	if !strings.Contains(string(definitionBytes), `glua-builtin:///constant/glua.event.events.progress_function_error.lua`) {
		// 常量定义 URI 必须明确区分于函数虚拟文档。
		t.Fatalf("definition result = %s", definitionBytes)
	}

	hoverBytes, err := json.Marshal(findResponse(t, messages, float64(4))["result"])
	if err != nil {
		// hover 结果必须是合法 JSON。
		t.Fatalf("marshal hover failed: %v", err)
	}
	if !strings.Contains(string(hoverBytes), "glua.event.events.progress_function_error") || strings.Contains(string(hoverBytes), "**Parameters**") {
		// 常量 hover 展示值语义且不能伪造参数列表。
		t.Fatalf("hover result = %s", hoverBytes)
	}
}

// TestServerBuiltinAliasDefinitionHoverAndCompletion 验证内置命名空间局部别名可跳转、悬停和补全。
func TestServerBuiltinAliasDefinitionHoverAndCompletion(t *testing.T) {
	// 覆盖截图中的 event 别名和进一步派生的 events 链式别名。
	source := "local event = glua.event\n" +
		"local events = event.events\n" +
		"assert(event.events.progress_end == 'progress.end')\n" +
		"assert(events.progress_end == 'progress.end')\n" +
		"events.progress_\n"
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///alias.glua","text":"`+strings.ReplaceAll(source, "\n", `\n`)+`"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///alias.glua"},"position":{"line":2,"character":24}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///alias.glua"},"position":{"line":3,"character":17}}}`) +
		frame(`{"jsonrpc":"2.0","id":4,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///alias.glua"},"position":{"line":2,"character":24}}}`) +
		frame(`{"jsonrpc":"2.0","id":5,"method":"textDocument/completion","params":{"textDocument":{"uri":"file:///alias.glua"},"position":{"line":4,"character":16}}}`) +
		frame(`{"jsonrpc":"2.0","id":6,"method":"shutdown"}`)
	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		// 别名请求序列必须完整处理。
		t.Fatalf("Run failed: %v", err)
	}
	messages := decodeFrames(t, output.Bytes())
	for _, responseID := range []float64{2, 3} {
		// 一级和二级别名都必须跳到同一个内置常量虚拟文档。
		definitionBytes, err := json.Marshal(findResponse(t, messages, responseID)["result"])
		if err != nil {
			// definition 结果必须可编码。
			t.Fatalf("marshal definition id=%v failed: %v", responseID, err)
		}
		if !strings.Contains(string(definitionBytes), `glua-builtin:///constant/glua.event.events.progress_end.lua`) {
			// 未展开别名会返回 nil，无法在 VS Code 中点击跳转。
			t.Fatalf("definition id=%v result = %s", responseID, definitionBytes)
		}
	}
	hoverBytes, err := json.Marshal(findResponse(t, messages, float64(4))["result"])
	if err != nil {
		// hover 结果必须可编码。
		t.Fatalf("marshal hover failed: %v", err)
	}
	if !strings.Contains(string(hoverBytes), "glua.event.events.progress_end") {
		// hover 应展示展开后的完整内置名称。
		t.Fatalf("hover result = %s", hoverBytes)
	}
	completionBytes, err := json.Marshal(findResponse(t, messages, float64(5))["result"])
	if err != nil {
		// completion 结果必须可编码。
		t.Fatalf("marshal completion failed: %v", err)
	}
	if !strings.Contains(string(completionBytes), `"label":"progress_end"`) {
		// 链式别名后的成员前缀必须获得内置常量补全。
		t.Fatalf("completion result = %s", completionBytes)
	}
}

// TestResolveBuiltinQualifiedAliasRejectsUnknownShadow 验证未知右值会清除旧内置别名。
func TestResolveBuiltinQualifiedAliasRejectsUnknownShadow(t *testing.T) {
	// 最近的同名 local 声明覆盖旧别名后，不得继续跳到 glua 内置文档。
	source := "local event = glua.event\nlocal event = custom\nevent.events.progress_end\n"
	resolved := resolveBuiltinQualifiedAlias(source, "event.events.progress_end", lspPosition{Line: 2, Character: 20})
	if resolved != "event.events.progress_end" {
		// 错误保留旧别名会让普通用户对象跳到内置常量。
		t.Fatalf("resolved shadow alias = %q", resolved)
	}
}

// TestServerSourceConstCompletionAndDefinition 验证源码 const 的补全类型和定义位置。
func TestServerSourceConstCompletionAndDefinition(t *testing.T) {
	// 在同一文档声明并使用 const，覆盖 Go 侧文件符号模型。
	source := "const answer = 42\nprint(answer)\nans"
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///source-const.glua","text":"`+strings.ReplaceAll(source, "\n", `\n`)+`"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"file:///source-const.glua"},"position":{"line":2,"character":3}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///source-const.glua"},"position":{"line":1,"character":8}}}`) +
		frame(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`)

	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		// const 请求序列必须正常完成。
		t.Fatalf("Run failed: %v", err)
	}
	messages := decodeFrames(t, output.Bytes())
	completionBytes, err := json.Marshal(findResponse(t, messages, float64(2))["result"])
	if err != nil {
		// 补全结果必须可编码。
		t.Fatalf("marshal completion failed: %v", err)
	}
	if !strings.Contains(string(completionBytes), `"label":"answer"`) || !strings.Contains(string(completionBytes), `"kind":21`) {
		// 源码 const 必须保持 Constant 类型。
		t.Fatalf("completion result = %s", completionBytes)
	}
	definitionBytes, err := json.Marshal(findResponse(t, messages, float64(3))["result"])
	if err != nil {
		// 定义结果必须可编码。
		t.Fatalf("marshal definition failed: %v", err)
	}
	if !strings.Contains(string(definitionBytes), `"character":6`) || !strings.Contains(string(definitionBytes), `"line":0`) {
		// answer 应跳回 const 声明名称。
		t.Fatalf("definition result = %s", definitionBytes)
	}
}

// TestServerRequireModuleCompletionAndDefinition 验证 Go LSP 解析 require 模块及返回表成员。
func TestServerRequireModuleCompletionAndDefinition(t *testing.T) {
	// 建立与编辑器 golden 相同结构的临时工作区，避免测试依赖真实仓库文件。
	workspace := t.TempDir()
	modulePath := filepath.Join(workspace, "app", "module.lua")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o755); err != nil {
		// 临时目录创建失败时测试不能继续。
		t.Fatalf("MkdirAll failed: %v", err)
	}
	moduleSource := "local M = {}\nM.timesPrint = function(name) end\nfunction M:colonCall(value) end\nM.enabled = true\nreturn M\n"
	if err := os.WriteFile(modulePath, []byte(moduleSource), 0o644); err != nil {
		// 模块 fixture 写入失败时测试不能继续。
		t.Fatalf("WriteFile failed: %v", err)
	}
	callerPath := filepath.Join(workspace, "app", "caller.lua")
	callerURI := fileURIFromPath(callerPath)
	server := New(strings.NewReader(""), &bytes.Buffer{}, extensions.Default())
	server.workspaceRoots = []string{workspace}
	callerSource := "local tools = require('module')\ntools.timesPrint('ok')\ntools.\ntools:"

	moduleLocation, ok := server.requiredModuleLocation(callerSource, callerURI, lspPosition{Line: 0, Character: 23})
	if !ok || moduleLocation.URI != fileURIFromPath(modulePath) {
		// require 字符串必须跳转到同目录模块文件。
		t.Fatalf("module location = %#v, ok = %t", moduleLocation, ok)
	}
	memberLocation, ok := server.requiredMemberLocation(callerSource, callerURI, lspPosition{Line: 1, Character: 7})
	if !ok || memberLocation.URI != fileURIFromPath(modulePath) || memberLocation.Range.Start.Line != 1 {
		// 返回表函数成员必须跳转到模块内赋值定义。
		t.Fatalf("member location = %#v, ok = %t", memberLocation, ok)
	}
	items := server.moduleCompletionItems(callerSource, callerURI, lspPosition{Line: 2, Character: 6}, "tools.")
	itemBytes, err := json.Marshal(items)
	if err != nil {
		// 补全候选必须可编码。
		t.Fatalf("marshal completion items failed: %v", err)
	}
	if !strings.Contains(string(itemBytes), `"label":"timesPrint"`) || !strings.Contains(string(itemBytes), `"label":"enabled"`) {
		// 点号成员补全必须包含函数和普通字段。
		t.Fatalf("module completion = %s", itemBytes)
	}
	if strings.Contains(string(itemBytes), `"label":"colonCall"`) {
		// 冒号方法不应出现在点号补全里。
		t.Fatalf("dot completion unexpectedly includes colonCall: %s", itemBytes)
	}
	colonItems := server.moduleCompletionItems(callerSource, callerURI, lspPosition{Line: 3, Character: 6}, "tools:")
	colonBytes, err := json.Marshal(colonItems)
	if err != nil {
		// 冒号补全候选必须可编码。
		t.Fatalf("marshal colon completion items failed: %v", err)
	}
	if !strings.Contains(string(colonBytes), `"label":"colonCall"`) || strings.Contains(string(colonBytes), `"label":"timesPrint"`) {
		// 冒号补全只返回冒号方法。
		t.Fatalf("colon completion = %s", colonBytes)
	}
}

// TestWorkspaceRootPaths 验证多工作区 URI 会转换并去重。
func TestWorkspaceRootPaths(t *testing.T) {
	// 使用 file URI 覆盖 rootUri 与 workspaceFolders 的兼容路径。
	first := t.TempDir()
	second := t.TempDir()
	paths := workspaceRootPaths(initializeParams{
		RootURI: fileURIFromPath(first),
		WorkspaceFolders: []workspaceFolder{
			{URI: fileURIFromPath(first)},
			{URI: fileURIFromPath(second)},
		},
	})
	if len(paths) != 2 || paths[0] != filepath.Clean(first) || paths[1] != filepath.Clean(second) {
		// workspace roots 必须按 folders 顺序去重。
		t.Fatalf("workspace roots = %#v", paths)
	}
	if _, err := url.Parse(fileURIFromPath(first)); err != nil {
		// URI 工具函数必须返回可解析的 URI。
		t.Fatalf("invalid file URI: %v", err)
	}
}

// TestLoadBuiltinCatalogFiles 验证插件 builtin JSON 会统一进入 Go 补全模型。
func TestLoadBuiltinCatalogFiles(t *testing.T) {
	// 写入最小 catalog，覆盖函数和常量两种 signature 分类。
	path := filepath.Join(t.TempDir(), "builtin.json")
	content := `{"functions":{"pkg.call":{"signature":{"en":"pkg.call(value)"},"returns":{"en":"returns: nil"},"params":{"en":["value: any"]},"description":{"en":"calls value"}},"pkg.flag":{"signature":{"en":"pkg.flag"},"returns":{"en":"value: flag"},"params":{"en":[]},"description":{"en":"constant flag"}}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		// fixture 写入失败时测试不能继续。
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := LoadBuiltinCatalogFiles([]string{path}); err != nil {
		// 有效 catalog 必须成功加载。
		t.Fatalf("LoadBuiltinCatalogFiles failed: %v", err)
	}
	items := builtinCompletionItems("pkg.")
	itemBytes, err := json.Marshal(items)
	if err != nil {
		// catalog 补全结果必须可编码。
		t.Fatalf("marshal completion items failed: %v", err)
	}
	if !strings.Contains(string(itemBytes), `"label":"call"`) || !strings.Contains(string(itemBytes), `"insertText":"call(${1:value})"`) || !strings.Contains(string(itemBytes), `"insertTextFormat":2`) || !strings.Contains(string(itemBytes), `"label":"flag"`) || !strings.Contains(string(itemBytes), `"kind":21`) {
		// 无参数签名条目必须作为 Constant 返回。
		t.Fatalf("catalog completion = %s", itemBytes)
	}
}

// TestCompletionSnippetCaretPlacement 验证有参和无参函数的 Tab 接受光标语义。
func TestCompletionSnippetCaretPlacement(t *testing.T) {
	// 有参候选选中首个参数，无参候选插入完整括号。
	if got := completionSnippet("log", "console.log(...values)"); got != "log(${1:...values})" {
		t.Fatalf("parameter snippet = %q", got)
	}
	if got := completionSnippet("clear", "console.clear()"); got != "clear()" {
		t.Fatalf("zero parameter snippet = %q", got)
	}
}

// TestConsoleBuiltinCatalogDocumentsLevels 验证 Console 日志方法提供完整 Hover 文档。
func TestConsoleBuiltinCatalogDocumentsLevels(t *testing.T) {
	previousLocale := activeBuiltinCatalogLocale
	defer func() { applyBuiltinCatalogLocale(previousLocale) }()
	catalogPath := filepath.Join("..", "..", "vscode", "extensions", "glua-lsp", "server", "builtin-functions.json")
	if err := LoadBuiltinCatalogFiles([]string{catalogPath}); err != nil {
		t.Fatalf("LoadBuiltinCatalogFiles failed: %v", err)
	}
	applyBuiltinCatalogLocale("zh-CN")
	info, ok := builtinFunctionInfo("console.info")
	if !ok {
		t.Fatal("console.info missing from builtin catalog")
	}
	hover := formatBuiltinHover("console.info", info)
	if !strings.Contains(hover, "青绿色") || !strings.Contains(hover, "console.info(...values)") || !strings.Contains(hover, "参数") {
		t.Fatalf("console.info hover incomplete: %s", hover)
	}
}

// TestServerBuiltinHoverFollowsResolvedLocale 验证初始化和配置变更会切换 Hover 语言。
func TestServerBuiltinHoverFollowsResolvedLocale(t *testing.T) {
	// 使用独立条目避免覆盖其他内置函数，并在结束时恢复全局目录语言。
	const builtinName = "docs.localized"
	previousLocale := activeBuiltinCatalogLocale
	previousEntry, hadEntry := builtinCatalogEntries[builtinName]
	previousDoc, hadDoc := builtinFunctionDocs[builtinName]
	t.Cleanup(func() {
		// 清理测试条目并恢复执行前语言，避免影响后续用例。
		if hadEntry {
			builtinCatalogEntries[builtinName] = previousEntry
		} else {
			delete(builtinCatalogEntries, builtinName)
		}
		if hadDoc {
			builtinFunctionDocs[builtinName] = previousDoc
		} else {
			delete(builtinFunctionDocs, builtinName)
		}
		applyBuiltinCatalogLocale(previousLocale)
	})
	path := filepath.Join(t.TempDir(), "localized.json")
	content := `{"functions":{"docs.localized":{"signature":{"en":"docs.localized()","zh-CN":"docs.localized()"},"returns":{"en":"returns: id","zh-CN":"返回：ID。"},"params":{"en":[],"zh-CN":[]},"description":{"en":"English trigger timing.","zh-CN":"中文触发时机说明。"},"example":{"en":"docs.localized() -- English example","zh-CN":"docs.localized() -- 中文示例"}}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		// fixture 写入失败时无法验证目录本地化。
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := LoadBuiltinCatalogFiles([]string{path}); err != nil {
		// 双语目录必须成功加载。
		t.Fatalf("LoadBuiltinCatalogFiles failed: %v", err)
	}
	source := "docs.localized()\n"
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"initializationOptions":{"locale":"auto","resolvedLocale":"zh-CN"}}}`) +
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///localized.glua","text":"`+strings.ReplaceAll(source, "\n", `\n`)+`"}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///localized.glua"},"position":{"line":0,"character":6}}}`) +
		frame(`{"jsonrpc":"2.0","method":"workspace/didChangeConfiguration","params":{"settings":{"glua":{"docLanguage":"en","resolvedDocLanguage":"en"}}}}`) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///localized.glua"},"position":{"line":0,"character":6}}}`) +
		frame(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	var output bytes.Buffer
	server := New(strings.NewReader(input), &output, extensions.Default())
	if err := server.Run(context.Background()); err != nil {
		// 完整语言切换请求序列必须正常退出。
		t.Fatalf("Run failed: %v", err)
	}
	messages := decodeFrames(t, output.Bytes())
	zhBytes, err := json.Marshal(findResponse(t, messages, float64(2))["result"])
	if err != nil {
		// 中文 Hover 必须可编码。
		t.Fatalf("marshal zh hover failed: %v", err)
	}
	if !strings.Contains(string(zhBytes), "中文触发时机说明") || !strings.Contains(string(zhBytes), "**说明**") || !strings.Contains(string(zhBytes), "中文示例") {
		// 中文界面必须同时本地化内容、区块标题和示例。
		t.Fatalf("zh hover = %s", zhBytes)
	}
	enBytes, err := json.Marshal(findResponse(t, messages, float64(3))["result"])
	if err != nil {
		// 英文 Hover 必须可编码。
		t.Fatalf("marshal en hover failed: %v", err)
	}
	if !strings.Contains(string(enBytes), "English trigger timing") || !strings.Contains(string(enBytes), "**Description**") || !strings.Contains(string(enBytes), "English example") {
		// 动态切到英文后必须刷新全部 Hover 文本。
		t.Fatalf("en hover = %s", enBytes)
	}
}

// TestEventBuiltinCatalogDescribesTriggerTiming 验证事件公开文档包含准确触发时机和中文区块。
func TestEventBuiltinCatalogDescribesTriggerTiming(t *testing.T) {
	// 加载扩展实际随包目录，防止测试 fixture 与发布文档发生漂移。
	previousLocale := activeBuiltinCatalogLocale
	t.Cleanup(func() {
		// 测试结束恢复原文档语言。
		applyBuiltinCatalogLocale(previousLocale)
	})
	catalogPath := filepath.Join("..", "..", "vscode", "extensions", "glua-lsp", "server", "builtin-functions.json")
	if err := LoadBuiltinCatalogFiles([]string{catalogPath}); err != nil {
		// 发布目录必须能被 Go PLS 正常加载。
		t.Fatalf("LoadBuiltinCatalogFiles failed: %v", err)
	}
	applyBuiltinCatalogLocale("zh-CN")
	info, ok := builtinFunctionInfo("glua.event.setProgress")
	if !ok {
		// setProgress 必须存在于内置目录。
		t.Fatalf("setProgress builtin is missing")
	}
	hover := formatBuiltinHover("glua.event.setProgress", info)
	for _, expected := range []string{"预设事件的触发时机", "progress.function_error 在函数错误被 pcall/xpcall 捕获前触发", "maxCalls", "确定性采样", "静音", "**说明**", "**参数**", "**返回值**", "**示例**"} {
		// 关键触发语义和本地化标题缺一不可。
		if !strings.Contains(hover, expected) {
			t.Fatalf("setProgress hover missing %q: %s", expected, hover)
		}
	}
}

// TestPathBuiltinCatalog 验证 glua.path 的补全目录和中文安全边界说明。
func TestPathBuiltinCatalog(t *testing.T) {
	// 加载实际发布目录，确保 VSCode 与 JetBrains 共同获得 path 文档。
	previousLocale := activeBuiltinCatalogLocale
	t.Cleanup(func() {
		// 测试结束恢复原文档语言。
		applyBuiltinCatalogLocale(previousLocale)
	})
	catalogPath := filepath.Join("..", "..", "vscode", "extensions", "glua-lsp", "server", "builtin-functions.json")
	if err := LoadBuiltinCatalogFiles([]string{catalogPath}); err != nil {
		// 发布目录必须保持合法 JSON。
		t.Fatalf("LoadBuiltinCatalogFiles failed: %v", err)
	}
	applyBuiltinCatalogLocale("zh-CN")
	info, ok := builtinFunctionInfo("glua.path.rel")
	if !ok {
		// rel 必须参与补全、定义和 Hover。
		t.Fatalf("glua.path.rel builtin is missing")
	}
	hover := formatBuiltinHover("glua.path.rel", info)
	for _, expected := range []string{"glua.path.rel(base, target)", "词法路径", "跨卷", "**说明**", "**参数**", "**返回值**"} {
		// 签名、纯词法语义和失败边界必须完整展示。
		if !strings.Contains(hover, expected) {
			// 任一缺失都会让编辑器说明与运行时不一致。
			t.Fatalf("glua.path.rel hover missing %q: %s", expected, hover)
		}
	}
}

// TestSerializationBuiltinCatalog 验证序列化 API 的中英文文档和签名来自发布目录。
func TestSerializationBuiltinCatalog(t *testing.T) {
	// 加载 VSCode 与 JetBrains 共用的实际目录，避免 fallback 掩盖发布文件遗漏。
	previousLocale := activeBuiltinCatalogLocale
	t.Cleanup(func() {
		// 测试结束恢复原文档语言。
		applyBuiltinCatalogLocale(previousLocale)
	})
	catalogPath := filepath.Join("..", "..", "vscode", "extensions", "glua-lsp", "server", "builtin-functions.json")
	if err := LoadBuiltinCatalogFiles([]string{catalogPath}); err != nil {
		// 发布目录必须保持合法 JSON。
		t.Fatalf("LoadBuiltinCatalogFiles failed: %v", err)
	}
	applyBuiltinCatalogLocale("zh-CN")
	info, ok := builtinFunctionInfo("glua.xml.decode")
	if !ok {
		// XML decode 必须参与补全、定义和 Hover。
		t.Fatalf("glua.xml.decode builtin is missing")
	}
	hover := formatBuiltinHover("glua.xml.decode", info)
	for _, expected := range []string{"inferTypes", "纯 item 子节点", "**说明**", "**参数**", "**返回值**", "**示例**"} {
		// XML 映射和类型推断语义必须在中文 Hover 中可见。
		if !strings.Contains(hover, expected) {
			t.Fatalf("glua.xml.decode hover missing %q: %s", expected, hover)
		}
	}
	applyBuiltinCatalogLocale("en")
	jsonInfo, ok := builtinFunctionInfo("glua.json.encode")
	if !ok || !strings.Contains(formatBuiltinHover("glua.json.encode", jsonInfo), "Cyclic, sparse, mixed-key tables") {
		// 英文目录也必须包含主要错误边界。
		t.Fatalf("glua.json.encode English hover is incomplete")
	}
}

// TestUtilityBuiltinCatalog 验证通用扩展目录包含安全说明和中文 Hover 区块。
func TestUtilityBuiltinCatalog(t *testing.T) {
	// 加载 VSCode 与 JetBrains 共用的发布目录。
	previousLocale := activeBuiltinCatalogLocale
	t.Cleanup(func() {
		// 测试结束恢复原文档语言。
		applyBuiltinCatalogLocale(previousLocale)
	})
	catalogPath := filepath.Join("..", "..", "vscode", "extensions", "glua-lsp", "server", "builtin-functions.json")
	if err := LoadBuiltinCatalogFiles([]string{catalogPath}); err != nil {
		// 发布目录必须能被 Go PLS 加载。
		t.Fatalf("LoadBuiltinCatalogFiles failed: %v", err)
	}
	applyBuiltinCatalogLocale("zh-CN")
	testCases := map[string][]string{
		"glua.hash.md5":        {"仅用于兼容", "**说明**", "**参数**", "**返回值**"},
		"glua.zip.decompress":  {"路径穿越", "重复名称", "超限内容"},
		"glua.schema.validate": {"false、message、path", "非法 schema 抛错"},
	}
	for name, expectedTexts := range testCases {
		// 每个关键 API 必须存在并包含安全/返回协议说明。
		info, ok := builtinFunctionInfo(name)
		if !ok {
			// 缺少条目会破坏补全和定义跳转。
			t.Fatalf("%s builtin is missing", name)
		}
		hover := formatBuiltinHover(name, info)
		for _, expected := range expectedTexts {
			// 关键文本缺失时输出完整 Hover。
			if !strings.Contains(hover, expected) {
				t.Fatalf("%s hover missing %q: %s", name, expected, hover)
			}
		}
	}
}

// frame 构造一条 LSP Content-Length 消息。
func frame(body string) string {
	// LSP 帧使用字节长度而不是 rune 长度。
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// decodeFrames 解码测试输出中的所有 LSP 帧。
func decodeFrames(t *testing.T, output []byte) []map[string]any {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(output))
	messages := make([]map[string]any, 0, 4)
	for {
		if _, err := reader.Peek(1); err != nil {
			break
		}
		body, err := readMessage(reader)
		if err != nil {
			// server 输出必须是合法 LSP 帧。
			t.Fatalf("read frame failed: %v", err)
		}
		var message map[string]any
		if err := json.Unmarshal(body, &message); err != nil {
			// 每个帧 body 必须是合法 JSON。
			t.Fatalf("decode frame failed: %v", err)
		}
		messages = append(messages, message)
	}
	return messages
}

// findResponse 查找指定 ID 的 JSON-RPC 响应。
func findResponse(t *testing.T, messages []map[string]any, id float64) map[string]any {
	t.Helper()
	for _, message := range messages {
		if message["id"] == id {
			// 命中指定响应。
			return message
		}
	}
	t.Fatalf("response id %v not found in %#v", id, messages)
	return nil
}
