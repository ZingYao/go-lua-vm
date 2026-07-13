package playground

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/runtime"
)

const (
	debugContinue = "continue"
	debugStepInto = "stepInto"
	debugStepOver = "stepOver"
	debugStepOut  = "stepOut"
)

// Debugger 实现文档 Playground 使用的行级断点和步进控制。
type Debugger struct {
	mu              sync.Mutex
	emit            Sink
	breakpoints     map[int]bool
	commands        chan string
	pauseRequested  bool
	pauseAtEntry    bool
	stepMode        string
	stepDepth       int
	lastSource      string
	lastLine        int
	skipBreakSource string
	skipBreakLine   int
}

// NewDebugger 创建尚未启用的调试观察器。
func NewDebugger(emit Sink) *Debugger {
	// commands 保留一个待处理命令，避免连续点击泄漏到后续暂停点。
	return &Debugger{emit: emit, breakpoints: make(map[int]bool), commands: make(chan string, 1)}
}

// Reset 清理一次运行留下的步进状态。
//
// pauseAtEntry 为 true 时下一条可见源码行以 entry 原因暂停；断点集合会保留。
func (debugger *Debugger) Reset(pauseAtEntry bool) {
	// 调试状态更新需要与浏览器控制回调并发安全。
	debugger.mu.Lock()
	defer debugger.mu.Unlock()
	debugger.pauseRequested = false
	debugger.pauseAtEntry = pauseAtEntry
	debugger.stepMode = ""
	debugger.stepDepth = 0
	debugger.lastSource = ""
	debugger.lastLine = 0
	debugger.skipBreakSource = ""
	debugger.skipBreakLine = 0
	for {
		select {
		case <-debugger.commands:
			// 丢弃上一轮暂停遗留的控制命令。
			continue
		default:
			// channel 已排空即可完成重置。
			return
		}
	}
}

// SetBreakpoints 替换当前源码断点集合。
func (debugger *Debugger) SetBreakpoints(lines []int) {
	// 使用替换语义，保证 UI 取消断点后立即生效。
	debugger.mu.Lock()
	defer debugger.mu.Unlock()
	debugger.breakpoints = make(map[int]bool, len(lines))
	for _, line := range lines {
		if line <= 0 {
			// 非正行号不是可执行源码行，直接忽略。
			continue
		}
		debugger.breakpoints[line] = true
	}
}

// Command 提交一条调试控制命令。
func (debugger *Debugger) Command(command string) error {
	// pause 不需要当前已经停止，设置标记后在下一条可见行生效。
	if command == "pause" {
		debugger.mu.Lock()
		debugger.pauseRequested = true
		debugger.mu.Unlock()
		return nil
	}
	if command != debugContinue && command != debugStepInto && command != debugStepOver && command != debugStepOut {
		// 未知命令不得改变暂停状态。
		return fmt.Errorf("unsupported playground debug command: %s", command)
	}
	select {
	case debugger.commands <- command:
		// 暂停中的 BeforeInstruction 会消费命令并恢复执行。
		return nil
	default:
		// 连续点击只保留已有命令，避免 channel 堆积到下一暂停点。
		return errors.New("playground debug command is already pending")
	}
}

// BeforeInstruction 在每条 Lua 指令前处理断点、暂停和步进。
func (debugger *Debugger) BeforeInstruction(state *runtime.State, vm *runtime.VM, proto *bytecode.Proto, pc int) error {
	// 无效执行上下文没有可映射的用户源码行。
	if state == nil || vm == nil || proto == nil || pc < 0 || pc >= len(proto.LineInfo) {
		return nil
	}
	line := proto.LineInfo[pc]
	if line <= 0 {
		// 编译器内部指令没有正行号，不参与用户调试。
		return nil
	}
	source := strings.TrimPrefix(proto.Source, "@")
	if source == "" {
		// 缺少源码名时使用 Playground 固定名称，保证 UI 可展示。
		source = filepath.Base(playgroundChunkName)
	}
	depth := state.CallDepth()
	reason := debugger.stopReason(source, line, depth)
	if reason == "" {
		// 当前指令未满足任何暂停条件。
		return nil
	}
	locals := debuggerVariables(vm.ActiveLocalSnapshots())
	if debugger.emit != nil {
		// 暂停事件携带当前行、深度和局部变量快照。
		debugger.emit(Event{Type: "paused", Source: source, Line: line, Reason: reason, Depth: depth, Locals: locals})
	}
	command := <-debugger.commands
	debugger.resume(command, source, line, depth)
	return nil
}

// stopReason 判断当前源码位置是否需要暂停。
func (debugger *Debugger) stopReason(source string, line int, depth int) string {
	// 所有暂停状态在同一锁内判断和消费，避免 UI 命令竞态。
	debugger.mu.Lock()
	defer debugger.mu.Unlock()
	lineChanged := debugger.lastSource != source || debugger.lastLine != line
	if lineChanged {
		// 离开上一行后允许未来再次命中该行断点。
		debugger.lastSource = source
		debugger.lastLine = line
		if debugger.skipBreakSource != source || debugger.skipBreakLine != line {
			debugger.skipBreakSource = ""
			debugger.skipBreakLine = 0
		}
	}
	if debugger.pauseAtEntry {
		// Debug 启动始终先在首条可见行暂停一次。
		debugger.pauseAtEntry = false
		return "entry"
	}
	if debugger.pauseRequested && lineChanged {
		// 用户暂停在下一条不同的可见源码行生效。
		debugger.pauseRequested = false
		return "pause"
	}
	if debugger.breakpoints[line] && (debugger.skipBreakSource != source || debugger.skipBreakLine != line) {
		// 命中断点后暂停；恢复时会暂时跳过当前行的重复指令。
		return "breakpoint"
	}
	if !lineChanged {
		// 行级单步不在同一源码行的后续指令重复暂停。
		return ""
	}
	switch debugger.stepMode {
	case debugStepInto:
		// 单步进入在任意深度的下一可见行暂停。
		return "step"
	case debugStepOver:
		if depth <= debugger.stepDepth {
			// 单步跳过忽略更深调用帧，到原深度或更浅位置暂停。
			return "step"
		}
	case debugStepOut:
		if depth < debugger.stepDepth {
			// 单步跳出只在离开当前调用深度后暂停。
			return "step"
		}
	default:
		// 未设置步进模式时继续运行到断点或显式暂停。
		return ""
	}
	return ""
}

// resume 应用暂停点收到的继续或步进命令。
func (debugger *Debugger) resume(command string, source string, line int, depth int) {
	// 记录恢复行，防止同一行多条指令立即再次命中断点。
	debugger.mu.Lock()
	defer debugger.mu.Unlock()
	debugger.skipBreakSource = source
	debugger.skipBreakLine = line
	debugger.stepMode = ""
	debugger.stepDepth = depth
	if command == debugStepInto || command == debugStepOver || command == debugStepOut {
		// 步进模式从当前暂停深度开始计算。
		debugger.stepMode = command
	}
}

// debuggerVariables 将 VM 局部变量快照转换为浏览器可序列化结构。
func debuggerVariables(locals []runtime.ActiveLocalSnapshot) []Variable {
	// 空局部变量保持 nil，减少暂停事件负载。
	if len(locals) == 0 {
		return nil
	}
	variables := make([]Variable, 0, len(locals))
	for _, local := range locals {
		// table 最多展开两层并限制总项数，避免循环引用和超大对象阻塞 UI。
		variable := variableFromValue(local.Name, local.Value, 0, make(map[*runtime.Table]bool))
		variable.Const = local.Const
		variables = append(variables, variable)
	}
	return variables
}

// variableFromValue 把 Lua 值转换为有限深度变量树。
func variableFromValue(name string, value runtime.Value, depth int, visited map[*runtime.Table]bool) Variable {
	// 基础展示始终包含名称、类型和稳定值文本。
	variable := Variable{Name: name, Type: valueTypeName(value), Value: value.DebugString()}
	if value.Kind != runtime.KindTable || depth >= 2 {
		// 非 table 或到达深度上限时不再展开。
		return variable
	}
	table, ok := value.Ref.(*runtime.Table)
	if !ok || table == nil || visited[table] {
		// 损坏或循环 table 只展示引用摘要。
		return variable
	}
	visited[table] = true
	defer delete(visited, table)
	key := runtime.NilValue()
	for count := 0; count < 100; count++ {
		// RawNext 提供稳定 Lua raw 迭代，不触发用户元方法。
		nextKey, nextValue, hasNext, err := table.RawNext(key)
		if err != nil || !hasNext {
			// 迭代结束或异常时停止展开，调试展示不得影响执行。
			break
		}
		variable.Children = append(variable.Children, variableFromValue(debuggerKeyName(nextKey), nextValue, depth+1, visited))
		key = nextKey
	}
	return variable
}

// debuggerKeyName 返回 table 键在变量树中的名称。
func debuggerKeyName(value runtime.Value) string {
	// string 键直接展示内容，其他键使用稳定调试文本。
	if value.Kind == runtime.KindString {
		return value.String
	}
	return value.DebugString()
}

// valueTypeName 返回 Lua 基础类型名称。
func valueTypeName(value runtime.Value) string {
	// integer 与 number 都属于 Lua number，但调试面板保留 integer 细分。
	switch value.Kind {
	case runtime.KindNil:
		return "nil"
	case runtime.KindBoolean:
		return "boolean"
	case runtime.KindInteger:
		return "integer"
	case runtime.KindNumber:
		return "number"
	case runtime.KindString:
		return "string"
	case runtime.KindTable:
		return "table"
	case runtime.KindLuaClosure, runtime.KindGoClosure:
		return "function"
	case runtime.KindUserdata:
		return "userdata"
	case runtime.KindThread:
		return "thread"
	default:
		return fmt.Sprintf("kind-%d", value.Kind)
	}
}
