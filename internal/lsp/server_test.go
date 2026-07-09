package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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
