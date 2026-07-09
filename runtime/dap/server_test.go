package dap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestServerInitializeThreadsAndDisconnect 验证最小 DAP server 能完成 IDE 基础握手。
//
// 该测试只覆盖协议骨架，不声明断点、单步和变量已经接入 VM。
func TestServerInitializeThreadsAndDisconnect(t *testing.T) {
	// 使用 127.0.0.1:0 让系统分配空闲端口，避免测试之间端口冲突。
	server, err := NewServer("127.0.0.1:0")
	if err != nil {
		// 合法 loopback 地址不应创建失败。
		t.Fatalf("NewServer failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx, nil); err != nil {
		// 监听失败说明本地 TCP 环境不可用。
		t.Fatalf("Start failed: %v", err)
	}
	defer server.Close()
	connection, err := net.DialTimeout("tcp", server.ActualAddress, time.Second)
	if err != nil {
		// ready 后必须可以连接。
		t.Fatalf("dial DAP server failed: %v", err)
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	writeRequest(t, connection, 1, "initialize")
	initializeResponse := readProtocolMessage(t, reader)
	if initializeResponse["type"] != "response" || initializeResponse["command"] != "initialize" || initializeResponse["success"] != true {
		// initialize 必须成功响应。
		t.Fatalf("initialize response = %#v", initializeResponse)
	}
	initializedEvent := readProtocolMessage(t, reader)
	if initializedEvent["type"] != "event" || initializedEvent["event"] != "initialized" {
		// initialize 后必须通知客户端可以配置断点。
		t.Fatalf("initialized event = %#v", initializedEvent)
	}
	writeRequest(t, connection, 2, "threads")
	threadsResponse := readProtocolMessage(t, reader)
	if threadsResponse["type"] != "response" || threadsResponse["command"] != "threads" || threadsResponse["success"] != true {
		// threads 必须成功响应。
		t.Fatalf("threads response = %#v", threadsResponse)
	}
	writeRequest(t, connection, 3, "disconnect")
	disconnectResponse := readProtocolMessage(t, reader)
	if disconnectResponse["type"] != "response" || disconnectResponse["command"] != "disconnect" || disconnectResponse["success"] != true {
		// disconnect 必须成功响应，便于 IDE 关闭会话。
		t.Fatalf("disconnect response = %#v", disconnectResponse)
	}
}

// TestServerBreakpointStopsAndContinues 验证 DAP 断点能暂停 VM 并通过 continue 继续。
//
// 该测试直接调用 DebugObserver 入口，锁定 DAP server 与 VM 行事件之间的最小集成契约。
func TestServerBreakpointStopsAndContinues(t *testing.T) {
	// 启动本机 DAP server 并完成 initialize 握手。
	server, connection, reader := startTestServerAndConnect(t)
	defer server.Close()
	defer connection.Close()
	writeRequest(t, connection, 1, "initialize")
	_ = readProtocolMessage(t, reader)
	_ = readProtocolMessage(t, reader)

	arguments := map[string]any{
		"source": map[string]any{"path": "main.glua"},
		"breakpoints": []map[string]any{
			{"line": 7},
		},
	}
	writeRequestWithArguments(t, connection, 2, "setBreakpoints", arguments)
	breakpointResponse := readProtocolMessage(t, reader)
	if breakpointResponse["type"] != "response" || breakpointResponse["command"] != "setBreakpoints" || breakpointResponse["success"] != true {
		// 设置断点必须成功响应。
		t.Fatalf("setBreakpoints response = %#v", breakpointResponse)
	}

	state := runtime.NewState()
	defer state.Close()
	vm := runtime.NewVM(1)
	proto := &bytecode.Proto{Source: "@main.glua", LineInfo: []int{7}, Code: []bytecode.Instruction{0}}
	stopped := make(chan error, 1)
	go func() {
		// BeforeInstruction 应命中断点并阻塞，直到 continue。
		stopped <- server.BeforeInstruction(state, vm, proto, 0)
	}()

	stoppedEvent := readProtocolMessage(t, reader)
	if stoppedEvent["type"] != "event" || stoppedEvent["event"] != "stopped" {
		// 命中断点必须发送 stopped 事件。
		t.Fatalf("stopped event = %#v", stoppedEvent)
	}
	writeRequest(t, connection, 3, "stackTrace")
	stackResponse := readProtocolMessage(t, reader)
	if stackResponse["type"] != "response" || stackResponse["command"] != "stackTrace" || stackResponse["success"] != true {
		// 暂停后必须能读取栈帧响应。
		t.Fatalf("stackTrace response = %#v", stackResponse)
	}
	writeRequest(t, connection, 4, "continue")
	continueResponse := readProtocolMessage(t, reader)
	if continueResponse["type"] != "response" || continueResponse["command"] != "continue" || continueResponse["success"] != true {
		// continue 必须释放 VM。
		t.Fatalf("continue response = %#v", continueResponse)
	}
	select {
	case err := <-stopped:
		if err != nil {
			// continue 后观察器不应返回错误。
			t.Fatalf("BeforeInstruction returned %v", err)
		}
	case <-time.After(time.Second):
		// 未释放说明 VM 会被 Debug 会话卡死。
		t.Fatalf("BeforeInstruction did not resume after continue")
	}
}

// TestServerWaitForConfigurationDone 验证 CLI 可等待 IDE 完成断点配置后再执行脚本。
func TestServerWaitForConfigurationDone(t *testing.T) {
	// 启动 DAP server 并建立客户端连接。
	server, connection, reader := startTestServerAndConnect(t)
	defer server.Close()
	defer connection.Close()

	var output bytes.Buffer
	waiting := make(chan error, 1)
	go func() {
		// 等待必须阻塞到客户端发送 configurationDone。
		waiting <- server.WaitForConfigurationDone(context.Background(), time.Second, &output)
	}()

	select {
	case err := <-waiting:
		// configurationDone 前不应提前释放脚本执行。
		t.Fatalf("WaitForConfigurationDone returned before configurationDone: %v", err)
	case <-time.After(20 * time.Millisecond):
		// 短暂等待后仍阻塞，说明 CLI 不会抢跑脚本。
	}

	writeRequest(t, connection, 1, "initialize")
	_ = readProtocolMessage(t, reader)
	_ = readProtocolMessage(t, reader)
	writeRequest(t, connection, 2, "configurationDone")
	configurationDoneResponse := readProtocolMessage(t, reader)
	if configurationDoneResponse["type"] != "response" || configurationDoneResponse["command"] != "configurationDone" || configurationDoneResponse["success"] != true {
		// configurationDone 必须成功响应，才能释放 CLI。
		t.Fatalf("configurationDone response = %#v", configurationDoneResponse)
	}

	select {
	case err := <-waiting:
		if err != nil {
			// 客户端配置完成后等待应成功结束。
			t.Fatalf("WaitForConfigurationDone returned %v", err)
		}
	case <-time.After(time.Second):
		// 未释放说明 glua Debug 会一直卡在启动阶段。
		t.Fatalf("WaitForConfigurationDone did not return after configurationDone")
	}
	for _, want := range []string{"waiting for client configuration", "starting script"} {
		// 等待进度必须写入输出，便于 IDE Debug 控制台展示。
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output = %q, missing %q", output.String(), want)
		}
	}
}

// startTestServerAndConnect 启动测试 DAP server 并建立 TCP 连接。
func startTestServerAndConnect(t *testing.T) (*Server, net.Conn, *bufio.Reader) {
	t.Helper()
	server, err := NewServer("127.0.0.1:0")
	if err != nil {
		// 合法 loopback 地址不应创建失败。
		t.Fatalf("NewServer failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := server.Start(ctx, nil); err != nil {
		// 监听失败说明本地 TCP 环境不可用。
		t.Fatalf("Start failed: %v", err)
	}
	connection, err := net.DialTimeout("tcp", server.ActualAddress, time.Second)
	if err != nil {
		// ready 后必须可以连接。
		t.Fatalf("dial DAP server failed: %v", err)
	}
	return server, connection, bufio.NewReader(connection)
}

// writeRequest 写出一个最小 DAP request。
//
// connection 必须是已连接的 TCP 连接；seq 和 command 用于构造 request。
func writeRequest(t *testing.T, connection net.Conn, seq int, command string) {
	t.Helper()
	writeRequestWithArguments(t, connection, seq, command, nil)
}

// writeRequestWithArguments 写出带 arguments 的 DAP request。
//
// arguments 为 nil 时省略该字段；测试用它覆盖 setBreakpoints 等有参数命令。
func writeRequestWithArguments(t *testing.T, connection net.Conn, seq int, command string, arguments any) {
	t.Helper()
	request := map[string]any{
		"seq":     seq,
		"type":    "request",
		"command": command,
	}
	if arguments != nil {
		// 有参数命令必须写入 arguments 字段。
		request["arguments"] = arguments
	}
	payload, err := json.Marshal(request)
	if err != nil {
		// 测试构造的 request 必须可编码。
		t.Fatalf("marshal request failed: %v", err)
	}
	if _, err := fmt.Fprintf(connection, "Content-Length: %d\r\n\r\n%s", len(payload), payload); err != nil {
		// 写入失败说明测试连接不可用。
		t.Fatalf("write request failed: %v", err)
	}
}

// readProtocolMessage 读取一个 DAP JSON 消息并转为 map。
//
// reader 必须指向 DAP Content-Length 帧流；测试只关心少量顶层字段。
func readProtocolMessage(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	message, err := readMessage(reader)
	if err != nil {
		// server 必须返回完整 DAP 帧。
		t.Fatalf("read message failed: %v", err)
	}
	payload, err := json.Marshal(message)
	if err != nil {
		// protocolMessage 必须可重新编码。
		t.Fatalf("remarshal message failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		// 编码后的消息必须可解析为通用 map。
		t.Fatalf("unmarshal message failed: %v", err)
	}
	return result
}
