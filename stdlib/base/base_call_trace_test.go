//go:build call_trace

package base

import (
	"strconv"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/bytecode"
	"github.com/zing/go-lua-vm/compiler/codegen"
	"github.com/zing/go-lua-vm/compiler/parser"
	"github.com/zing/go-lua-vm/runtime"
)

// TestBaseLuaCallTraceFixedResultPairs 输出固定单返回 CALL 的运行期寄存器窗口。
//
// 该测试仅在显式 `-tags call_trace` 下启用，用于定位 native_modules 真实模块暴露出的
// CALL 写回、实参槽残留和后续局部槽布局差异；默认测试不运行它，避免把当前诊断状态固化为生产语义。
func TestBaseLuaCallTraceFixedResultPairs(t *testing.T) {
	// 逐个追踪已由 LPeg probe 证明存在 good/bad 差异的最小对。
	for _, testCase := range []struct {
		name   string
		source string
	}{
		{
			name: "bad-select-one-string",
			source: `
local count = select("#", "alpha")
if count ~= 1 then
  error("unexpected one-string select count")
end
local skipped = error
local payload = 17
`,
		},
		{
			name: "bad-select-two-numbers",
			source: `
local count = select("#", 17, 25)
if count ~= 2 then
  error("unexpected numeric select count")
end
local skipped = error
local payload = 17
`,
		},
		{
			name: "good-select-two-booleans",
			source: `
local count = select("#", true, false)
if count ~= 2 then
  error("unexpected boolean select count")
end
local skipped = error
local payload = 17
`,
		},
		{
			name: "bad-rawequal-strings-false",
			source: `
local count = rawequal("alpha", "beta")
if count ~= false then
  error("unexpected rawequal false result")
end
local skipped = error
local payload = 17
`,
		},
		{
			name: "good-rawequal-strings-true",
			source: `
local count = rawequal("alpha", "alpha")
if count ~= true then
  error("unexpected rawequal true result")
end
local skipped = error
local payload = 17
`,
		},
		{
			name: "good-rawequal-numbers-false",
			source: `
local count = rawequal(17, 25)
if count ~= false then
  error("unexpected numeric rawequal false result")
end
local skipped = error
local payload = 17
`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// 运行同包 VM trace 并通过 -v 输出每个 CALL 写回后的寄存器窗口。
			for _, event := range traceBaseLuaCallEvents(t, testCase.source) {
				t.Log(event)
			}
		})
	}
}

// TestBaseLuaCallTraceCalleeRegisterLifetimes 输出不同调用来源的 callee/local 生命周期。
//
// 该测试专门服务于评估固定返回 CALL 后能否清理非结果实参槽：若 CALL A 命中仍存活的
// local callee，则运行期清理会破坏 Lua 5.3 语义；若 codegen 总是把 callee 搬到临时槽，
// 后续才可以继续论证清理调用临时区的安全边界。
func TestBaseLuaCallTraceCalleeRegisterLifetimes(t *testing.T) {
	// 覆盖全局函数、local Go 函数、local Lua 函数、字段调用和方法调用的 CALL 布局。
	for _, testCase := range []struct {
		name   string
		source string
	}{
		{
			name: "global-go-callee",
			source: `
local count = select("#", "alpha")
local payload = 17
`,
		},
		{
			name: "local-go-callee-preserved",
			source: `
local f = select
local count = f("#", "alpha")
local preserved = f
local payload = 17
`,
		},
		{
			name: "local-lua-callee-preserved",
			source: `
local function f()
  return 1
end
local count = f()
local preserved = f
local payload = 17
`,
		},
		{
			name: "field-lua-callee-preserved",
			source: `
local t = { f = function() return 1 end }
local count = t.f()
local preserved = t
local payload = 17
`,
		},
		{
			name: "method-lua-callee-preserved",
			source: `
local t = { f = function(self) return 1 end }
local count = t:f()
local preserved = t
local payload = 17
`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// 运行同包 VM trace 并通过 -v 输出每个 CALL 写回后的活动 local 与寄存器窗口。
			for _, event := range traceBaseLuaCallEvents(t, testCase.source) {
				t.Log(event)
			}
		})
	}
}

// traceBaseLuaCallEvents 编译并单步执行 Lua chunk，返回每个 CALL 写回后的诊断行。
//
// source 必须只依赖 base.Open 注册的全局函数；该 helper 只用于 call_trace 测试，不参与生产执行路径。
func traceBaseLuaCallEvents(t *testing.T, source string) []string {
	// 准备 State 与 base 全局函数，确保 GETTABUP _ENV 能读取 select/rawequal/error。
	t.Helper()
	state := runtime.NewState()
	if err := Open(state); err != nil {
		t.Fatalf("open base failed: %v", err)
	}
	defer state.Close()

	proto := compileBaseCallTraceProto(t, source)
	upvalues := loadedClosureUpvalues(state, proto, nil)
	registerCount := baseLuaClosureRegisterCount(proto, 0, 0)
	vm := runtime.NewVMWithBorrowedPrototypeData(registerCount, proto.Constants, upvalues, proto.Protos, nil)
	vm.BindPrototype(proto)
	vm.BindUpvalueCells(loadedClosureUpvalueCells(upvalues))

	events := make([]string, 0, 8)
	pc := 0
	for pc >= 0 && pc < len(proto.Code) {
		// 单步执行时同步 PC，保持 local 生命周期与正式执行器一致。
		vm.SetCurrentPC(pc)
		instruction := proto.Code[pc]
		if err := vm.Step(instruction); err != nil {
			t.Fatalf("step pc=%d %s failed: %v", pc, instruction.OpCode().Name(), err)
		}
		if callRequest := vm.LastCallRequest(); callRequest != nil {
			// CALL 写回前后记录参数、结果和寄存器窗口，便于比较 non-result 参数槽是否保留。
			arguments, err := baseLuaCallArguments(vm, callRequest)
			if err != nil {
				t.Fatalf("arguments pc=%d failed: %v", pc, err)
			}
			functionValue, ok := vm.Register(callRequest.FunctionIndex)
			if !ok {
				t.Fatalf("function register pc=%d missing", pc)
			}
			results, err := callProtectedWithStateNamed(state, functionValue, "", "", arguments...)
			if err != nil {
				t.Fatalf("call pc=%d failed: %v", pc, err)
			}
			if err := writeBaseLuaCallResults(vm, proto, pc, callRequest, results); err != nil {
				t.Fatalf("write results pc=%d failed: %v", pc, err)
			}
			if runtime.CanClearFixedCallArgumentTemporaries(functionValue, callRequest) {
				// trace 输出必须对齐 base 执行器真实的普通 Go 回调临时槽清理语义。
				finishBaseLuaFixedCallResults(vm, proto, pc, callRequest)
			}
			events = append(events, formatBaseCallTraceEvent(pc, proto, instruction, callRequest, arguments, results, vm.RegistersSnapshot()))
		}
		if returnValues := vm.ReturnValues(); returnValues != nil {
			// 测试片段均为主 chunk，RETURN 后停止。
			break
		}
		pc++
		if vm.SkipNext() {
			// TEST/EQ 组合会请求跳过下一条指令。
			pc++
		}
		pc += vm.PCOffset()
	}
	return events
}

// compileBaseCallTraceProto 编译诊断源码为 Proto。
//
// source 是完整 Lua chunk；返回的 Proto 会绑定到 `_ENV` upvalue 后由 trace helper 执行。
func compileBaseCallTraceProto(t *testing.T, source string) *bytecode.Proto {
	// 解析和编译保持与 load/base 执行路径一致，避免手写字节码引入额外变量。
	t.Helper()
	chunk, err := parser.New(source).ParseChunk()
	if err != nil {
		t.Fatalf("parse trace source failed: %v", err)
	}
	proto, err := codegen.CompileChunk(chunk, "base-call-trace")
	if err != nil {
		t.Fatalf("compile trace source failed: %v", err)
	}
	return proto
}

// formatBaseCallTraceEvent 格式化单次 CALL trace。
//
// 输出包含 pc、CALL 的 A/B/C、解码后的实参/返回数量、实参快照、返回结果、活动 local 和写回后全部寄存器。
func formatBaseCallTraceEvent(pc int, proto *bytecode.Proto, instruction bytecode.Instruction, request *runtime.CallRequest, arguments []runtime.Value, results []runtime.Value, registers []runtime.Value) string {
	// 使用紧凑文本输出，便于在自动任务日志和 TODO 中摘录关键差异。
	return strings.Join([]string{
		"pc=" + traceInt(pc),
		"op=" + instruction.OpCode().Name(),
		"abc=" + traceInt(instruction.A()) + "/" + traceInt(instruction.B()) + "/" + traceInt(instruction.C()),
		"call=" + traceInt(request.FunctionIndex) + "/" + traceInt(request.ArgumentCount) + "/" + traceInt(request.ReturnCount),
		"args=[" + traceValues(arguments) + "]",
		"results=[" + traceValues(results) + "]",
		"locals=[" + traceActiveLocals(proto, pc+1, registers) + "]",
		"registers=[" + traceValues(registers) + "]",
	}, " ")
}

// traceActiveLocals 格式化指定 pc 后仍活动的局部变量。
//
// pc 通常取 CALL 后的下一条指令位置，用于观察返回写回后哪些寄存器已经成为活动 local。
func traceActiveLocals(proto *bytecode.Proto, pc int, registers []runtime.Value) string {
	// 缺少 Proto 时没有可靠的 local 生命周期信息。
	if proto == nil {
		return ""
	}
	parts := make([]string, 0, len(proto.LocalVars))
	for _, localVar := range proto.LocalVars {
		// 只输出当前 pc 可见且寄存器落在窗口内的 local。
		if !localVar.ActiveAt(pc) || localVar.Name == "" || localVar.Register < 0 || localVar.Register >= len(registers) {
			continue
		}
		parts = append(parts, localVar.Name+"@"+traceInt(localVar.Register)+"="+registers[localVar.Register].DebugString())
	}
	return strings.Join(parts, ",")
}

// traceValues 格式化一组 runtime.Value。
//
// DebugString 会暴露值类型和标量内容，足够区分本轮关注的 string、integer、boolean 与 Go closure。
func traceValues(values []runtime.Value) string {
	// 按寄存器顺序输出，避免 map 或 table 遍历带来不稳定。
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, value.DebugString())
	}
	return strings.Join(parts, ",")
}

// traceInt 返回十进制整数文本。
//
// 避免在诊断测试里引入 fmt，只为固定格式拼接服务。
func traceInt(value int) string {
	// strconv.Itoa 的语义稳定，输出只用于诊断日志。
	return strconv.Itoa(value)
}
