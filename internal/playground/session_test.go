package playground

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestSessionCapturesStandardStreams 验证 print、io.write 和 io.stderr 会按流发送到浏览器事件。
func TestSessionCapturesStandardStreams(t *testing.T) {
	// 收集单次同步执行产生的事件，便于分别断言 stdout 与 stderr。
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	err := session.Run(context.Background(), `print("hello"); io.write("world"); io.stderr:write("warn")`, false)
	if err != nil {
		// 基础输出脚本必须执行成功。
		t.Fatalf("Run failed: %v", err)
	}
	stdout := eventText(events, "stdout")
	stderr := eventText(events, "stderr")
	if !strings.Contains(stdout, "hello\n") || !strings.Contains(stdout, "world") {
		// print 与 io.write 都应进入 stdout。
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "warn" {
		// io.stderr 必须独立进入 stderr。
		t.Fatalf("stderr = %q, want warn", stderr)
	}
}

// TestSessionRunsWorkspaceModules 验证浏览器虚拟文件系统模块可通过 require 执行。
func TestSessionRunsWorkspaceModules(t *testing.T) {
	// 入口与模块均只来自内存映射，测试不得访问宿主文件系统。
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	files := map[string]string{
		"main.glua":      "local tools = require(\"lib.tools\")\nprint(tools.greet(\"GLua\"))\n",
		"lib/tools.glua": "local module = {}\nfunction module.greet(name) return \"hello \" .. name end\nreturn module\n",
	}
	err := session.RunWorkspace(context.Background(), files["main.glua"], "main.glua", files, false)
	if err != nil {
		// require 命中虚拟模块后入口应成功完成。
		t.Fatalf("RunWorkspace failed: %v", err)
	}
	if stdout := eventText(events, "stdout"); stdout != "hello GLua\n" {
		// 模块返回表和函数调用结果必须写入浏览器 stdout。
		t.Fatalf("stdout = %q, want hello GLua", stdout)
	}
}

// TestSessionIgnoresUnrequiredInvalidWorkspaceModule 验证无关坏文件不会阻塞入口执行。
func TestSessionIgnoresUnrequiredInvalidWorkspaceModule(t *testing.T) {
	// 模拟大型本地目录中存在只供其他工具使用、当前入口没有 require 的不兼容 Lua 夹具。
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	files := map[string]string{
		"main.glua":                             `print("entry ok")`,
		"tests/native_modules/fixtures/bad.lua": `local @ invalid`,
	}
	if err := session.RunWorkspace(context.Background(), files["main.glua"], "main.glua", files, false); err != nil {
		// 未被 require 的文件必须保持惰性，不能在启动阶段预编译。
		t.Fatalf("RunWorkspace unrelated invalid module failed: %v", err)
	}
	if stdout := eventText(events, "stdout"); stdout != "entry ok\n" {
		// 入口脚本应独立完成并输出结果。
		t.Fatalf("stdout = %q, want entry ok", stdout)
	}
}

// TestSessionReportsRequiredInvalidWorkspaceModule 验证实际 require 坏模块时仍返回具体错误。
func TestSessionReportsRequiredInvalidWorkspaceModule(t *testing.T) {
	// 只有入口显式引用模块后，VFS loader 才应编译并报告语法位置。
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	files := map[string]string{
		"main.glua": `require("bad")`,
		"bad.glua":  `local @ invalid`,
	}
	err := session.RunWorkspace(context.Background(), files["main.glua"], "main.glua", files, false)
	if err == nil {
		// 被引用坏模块必须终止未保护的 require。
		t.Fatal("RunWorkspace required invalid module unexpectedly succeeded")
	}
	stderr := eventText(events, "stderr")
	if !strings.Contains(stderr, "bad.glua:1") || !strings.Contains(stderr, "syntax error") {
		// 错误必须指向真正被引用的模块，而不是笼统 workspace compile 失败。
		t.Fatalf("stderr = %q, want bad.glua syntax location", stderr)
	}
}

// TestSessionWorkspaceVirtualFilesystem 验证 io.open 可读取浏览器工作区中的非 Lua 文件。
func TestSessionWorkspaceVirtualFilesystem(t *testing.T) {
	// Markdown 文件只存在于内存快照，成功读取可证明没有回退宿主文件系统。
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	files := map[string]string{
		"main.glua": "local file = assert(io.open(\"./TODO.md\", \"r\"))\nprint(file:read(\"*a\"))\n",
		"TODO.md":   "workspace todo",
	}
	if err := session.RunWorkspace(context.Background(), files["main.glua"], "main.glua", files, false); err != nil {
		// 只读 io.open 命中 VFS 后入口应执行成功。
		t.Fatalf("RunWorkspace VFS failed: %v", err)
	}
	if stdout := eventText(events, "stdout"); stdout != "workspace todo\n" {
		// io.open 返回的 file userdata 必须读取完整快照内容。
		t.Fatalf("stdout = %q, want workspace todo", stdout)
	}
}

// TestSessionWorkspaceVirtualFilesystemHostFallback 验证缺失文件错误与写模式宿主回退语义。
func TestSessionWorkspaceVirtualFilesystemHostFallback(t *testing.T) {
	// 切换到独立临时目录，避免默认开放的宿主写入污染仓库。
	t.Chdir(t.TempDir())
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	source := `
local missing, missingError = io.open("missing.txt", "r")
print(missing == nil, missingError)
local writeFile, writeError = io.open("TODO.md", "w")
print(writeFile ~= nil, writeError)
assert(writeFile:write("host fallback"))
assert(writeFile:close())
assert(os.remove("TODO.md"))
`
	files := map[string]string{"main.glua": source, "TODO.md": "read only"}
	if err := session.RunWorkspace(context.Background(), source, "main.glua", files, false); err != nil {
		// 可预期文件错误由 Lua 脚本处理，不应终止整个会话。
		t.Fatalf("RunWorkspace handled VFS errors failed: %v", err)
	}
	stdout := eventText(events, "stdout")
	if !strings.Contains(stdout, "true\topen missing.txt") || !strings.Contains(stdout, "true\tnil") {
		// 页面必须同时看到缺失路径错误和宿主写入成功结果。
		t.Fatalf("stdout = %q, want missing error and host write success", stdout)
	}
}

// TestSessionReportsLuaErrorObjectAndTraceback 验证 Playground stderr 不再退化为 lua error。
func TestSessionReportsLuaErrorObjectAndTraceback(t *testing.T) {
	// 主入口主动抛错，事件应包含原始对象、源码位置和 traceback 标题。
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	err := session.RunWorkspace(context.Background(), `error("boom")`, "main.glua", map[string]string{"main.glua": `error("stale")`}, false)
	if err == nil {
		// 主动 error 必须终止未保护的入口调用。
		t.Fatal("RunWorkspace error unexpectedly succeeded")
	}
	stderr := eventText(events, "stderr")
	if !strings.Contains(stderr, "boom") || !strings.Contains(stderr, "main.glua:1") || !strings.Contains(stderr, "stack traceback:") {
		// 缺少任一信息都会让浏览器错误无法定位。
		t.Fatalf("stderr = %q, want object, source line and traceback", stderr)
	}
	if strings.TrimSpace(stderr) == "lua error" {
		// 回归保护：禁止重新只输出错误分类名称。
		t.Fatalf("stderr remained generic: %q", stderr)
	}
}

// TestSessionBreakpointAfterWorkspaceRead 验证成功读取 VFS 后仍能命中后续断点。
func TestSessionBreakpointAfterWorkspaceRead(t *testing.T) {
	// 调试入口先暂停第一行，继续后应在第二行用户断点暂停。
	events := make(chan Event, 32)
	session := NewSession(func(event Event) {
		events <- event
	})
	session.SetBreakpoints([]int{2})
	source := "local file = assert(io.open(\"TODO.md\", \"r\"))\nprint(file:read(\"*a\"))\n"
	files := map[string]string{"main.glua": source, "TODO.md": "breakpoint content"}
	done := make(chan error, 1)
	go func() {
		// Debug 会阻塞等待页面命令，测试通过 channel 驱动继续。
		done <- session.RunWorkspace(context.Background(), source, "main.glua", files, true)
	}()
	breakpoint := waitForEvent(t, events, "paused")
	if breakpoint.Reason != "breakpoint" || breakpoint.Line != 2 || breakpoint.Source != "main.glua" {
		// VFS 读取成功后第二行断点必须命中。
		t.Fatalf("breakpoint = %#v, want main.glua:2 breakpoint", breakpoint)
	}
	if err := session.DebugCommand(debugContinue); err != nil {
		// 继续断点后脚本应完成。
		t.Fatalf("continue breakpoint failed: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			// 完成路径不应产生读取或调试错误。
			t.Fatalf("RunWorkspace debug failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		// 超时表示断点恢复或 VFS 读取阻塞。
		t.Fatal("workspace breakpoint session timed out")
	}
}

// TestSessionWorkspaceRejectsTraversalModule 验证非法工作区路径不会注册到 package.preload。
func TestSessionWorkspaceRejectsTraversalModule(t *testing.T) {
	// 父目录文件即使出现在宿主映射中也不能被 require 命中。
	session := NewSession(nil)
	files := map[string]string{"../secret.glua": "return { value = 1 }"}
	err := session.RunWorkspace(context.Background(), "require(\"secret\")", "main.glua", files, false)
	if err == nil {
		// 非法路径被加载意味着虚拟文件系统边界失效。
		t.Fatal("RunWorkspace traversal module unexpectedly succeeded")
	}
}

// TestDebuggerStepDepthModes 验证跳过和跳出会按调用深度选择下一暂停行。
func TestDebuggerStepDepthModes(t *testing.T) {
	// 单步跳过应忽略更深调用帧，并回到原深度后暂停。
	debugger := NewDebugger(nil)
	debugger.Reset(false)
	debugger.lastSource = "playground.lua"
	debugger.lastLine = 1
	debugger.resume(debugStepOver, "playground.lua", 1, 3)
	if reason := debugger.stopReason("playground.lua", 2, 4); reason != "" {
		// 更深调用帧不应触发 stepOver。
		t.Fatalf("stepOver deeper reason = %q", reason)
	}
	if reason := debugger.stopReason("playground.lua", 3, 3); reason != "step" {
		// 回到原调用深度的下一行应暂停。
		t.Fatalf("stepOver same-depth reason = %q, want step", reason)
	}

	debugger.Reset(false)
	debugger.lastSource = "playground.lua"
	debugger.lastLine = 4
	debugger.resume(debugStepOut, "playground.lua", 4, 3)
	if reason := debugger.stopReason("playground.lua", 5, 3); reason != "" {
		// 仍在当前调用深度时不应触发 stepOut。
		t.Fatalf("stepOut same-depth reason = %q", reason)
	}
	if reason := debugger.stopReason("playground.lua", 6, 2); reason != "step" {
		// 离开当前调用深度后应暂停。
		t.Fatalf("stepOut shallower reason = %q, want step", reason)
	}
}

// TestDebuggerVariablesExpandTables 验证局部 table 会转换为有限深度变量树。
func TestDebuggerVariablesExpandTables(t *testing.T) {
	// 构造包含嵌套 table 和循环引用的局部值，确保展开不会无限递归。
	root := runtime.NewTable()
	child := runtime.NewTable()
	rootValue := runtime.ReferenceValue(runtime.KindTable, root)
	childValue := runtime.ReferenceValue(runtime.KindTable, child)
	root.RawSetString("answer", runtime.IntegerValue(42))
	root.RawSetString("child", childValue)
	child.RawSetString("root", rootValue)
	variables := debuggerVariables([]runtime.ActiveLocalSnapshot{{Name: "config", Const: true, Value: rootValue}})
	if len(variables) != 1 || variables[0].Name != "config" || !variables[0].Const {
		// 顶层局部名称和 const 属性必须保留。
		t.Fatalf("variables = %#v", variables)
	}
	if len(variables[0].Children) != 2 {
		// root 的两个字段都应出现在变量树中。
		t.Fatalf("children = %#v", variables[0].Children)
	}
	if variables[0].Children[0].Name == "" || variables[0].Children[1].Name == "" {
		// table 子项必须有可展示键名。
		t.Fatalf("child names = %#v", variables[0].Children)
	}
}

// TestSessionSupportsInteractiveInput 验证脚本等待 io.read 时可由宿主异步提交输入。
func TestSessionSupportsInteractiveInput(t *testing.T) {
	// 使用 goroutine 模拟 Worker 中运行脚本，测试线程随后发送用户输入。
	events := make(chan Event, 32)
	session := NewSession(func(event Event) {
		events <- event
	})
	done := make(chan error, 1)
	go func() {
		// io.read 默认读取一行并由 print 回显。
		done <- session.Run(context.Background(), `local name = io.read(); print("hello", name)`, false)
	}()
	session.SendInput("GLua")
	select {
	case err := <-done:
		if err != nil {
			// 提交输入后脚本应正常完成。
			t.Fatalf("Run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		// 超时表示阻塞输入没有被唤醒。
		t.Fatal("interactive input timed out")
	}
	close(events)
	collected := make([]Event, 0)
	for event := range events {
		// 汇总缓冲事件用于输出断言。
		collected = append(collected, event)
	}
	if stdout := eventText(collected, "stdout"); !strings.Contains(stdout, "hello\tGLua\n") {
		// print 应输出用户输入内容。
		t.Fatalf("stdout = %q", stdout)
	}
}

// TestSessionDebugPauseAndStep 验证 Debug 断点暂停、局部变量和单步控制。
func TestSessionDebugPauseAndStep(t *testing.T) {
	// 事件 channel 允许测试与暂停中的执行 goroutine交互。
	events := make(chan Event, 32)
	session := NewSession(func(event Event) {
		events <- event
	})
	session.SetBreakpoints([]int{1})
	done := make(chan error, 1)
	go func() {
		// 多行脚本提供至少两个行级暂停点。
		done <- session.Run(context.Background(), "local value = 1\nvalue = value + 1\nprint(value)\n", true)
	}()
	breakpoint := waitForEvent(t, events, "paused")
	if breakpoint.Reason != "breakpoint" || breakpoint.Line != 1 {
		// Debug 应直接运行到用户设置的首行断点。
		t.Fatalf("breakpoint event = %#v", breakpoint)
	}
	if err := session.DebugCommand(debugStepInto); err != nil {
		// 暂停点应接受单步进入命令。
		t.Fatalf("step command failed: %v", err)
	}
	step := waitForEvent(t, events, "paused")
	if step.Reason != "step" || step.Line <= breakpoint.Line {
		// 单步应前进到后续源码行。
		t.Fatalf("step event = %#v", step)
	}
	if err := session.DebugCommand(debugContinue); err != nil {
		// 继续命令应释放当前暂停点。
		t.Fatalf("continue command failed: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			// 继续后脚本应执行完成。
			t.Fatalf("Run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		// 调试恢复不能永久阻塞。
		t.Fatal("debug session timed out")
	}
}

// TestSessionDebugWithoutBreakpointsRunsToCompletion 验证 Debug 默认不会停在入口首行。
func TestSessionDebugWithoutBreakpointsRunsToCompletion(t *testing.T) {
	// 无断点脚本应自动执行完成，只产生 running、output 和 finished 事件。
	events := make([]Event, 0)
	session := NewSession(func(event Event) {
		events = append(events, event)
	})
	if err := session.Run(context.Background(), "print('auto')", true); err != nil {
		// Debug 自动执行路径不应产生错误。
		t.Fatalf("Run debug without breakpoints failed: %v", err)
	}
	for _, event := range events {
		if event.Type == "paused" {
			// 未设置断点时禁止恢复旧的 entry 暂停行为。
			t.Fatalf("unexpected paused event: %#v", event)
		}
	}
	if stdout := eventText(events, "stdout"); stdout != "auto\n" {
		// 自动执行仍须保留正常 stdout。
		t.Fatalf("stdout = %q, want auto", stdout)
	}
}

// eventText 合并指定标准流的输出事件文本。
func eventText(events []Event, stream string) string {
	// 使用 Builder 保持事件到达顺序。
	var builder strings.Builder
	for _, event := range events {
		if event.Stream != stream {
			// 其他流和状态事件不参与合并。
			continue
		}
		builder.WriteString(event.Text)
	}
	return builder.String()
}

// waitForEvent 等待指定类型事件或测试超时。
func waitForEvent(t *testing.T, events <-chan Event, eventType string) Event {
	// 辅助函数失败时归属调用测试。
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == eventType {
				// 找到目标事件后返回完整负载。
				return event
			}
		case <-deadline:
			// 超时通常表示执行或调试 channel 没有正确唤醒。
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}
