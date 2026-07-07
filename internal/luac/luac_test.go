package luac

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/compiler/parser"
	"github.com/ZingYao/go-lua-vm/extensions"
	"github.com/ZingYao/go-lua-vm/lua"
	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestParseArgs 验证 gluac 参数解析覆盖 luac 兼容选项和开发 trace 选项。
func TestParseArgs(t *testing.T) {
	// 表驱动覆盖单输入、输出路径、重复 -l 和 trace 标记。
	tests := []struct {
		name string
		args []string
		want Options
	}{
		{
			name: "compile output",
			args: []string{"-o", "out.luac", "input.lua"},
			want: Options{InputPath: "input.lua", InputPaths: []string{"input.lua"}, OutputPath: "out.luac"},
		},
		{
			name: "verbose list",
			args: []string{"-l", "-l", "input.luac"},
			want: Options{InputPath: "input.luac", InputPaths: []string{"input.luac"}, OutputPath: DefaultOutputPath, ListLevel: 2},
		},
		{
			name: "developer trace",
			args: []string{"--gluac-opcode-trace", "--gluac-step-trace", "--gluac-minimal-disassembly", "input.lua"},
			want: Options{InputPath: "input.lua", InputPaths: []string{"input.lua"}, OutputPath: DefaultOutputPath, OpcodeTrace: true, StepTrace: true, MinimalDisassembly: true},
		},
		{
			name: "multiple inputs",
			args: []string{"one.lua", "two.lua"},
			want: Options{InputPath: "one.lua", InputPaths: []string{"one.lua", "two.lua"}, OutputPath: DefaultOutputPath},
		},
	}
	for _, test := range tests {
		// 每个用例独立解析，避免状态互相污染。
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseArgs(test.args)
			if err != nil {
				// 参数应合法，错误代表解析器回归。
				t.Fatalf("ParseArgs error: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				// Options 必须逐字段稳定，便于 CLI 行为可预测。
				t.Fatalf("Options mismatch: got=%+v want=%+v", got, test.want)
			}
		})
	}
}

// TestParseArgsSyntaxOptions 验证 gluac 语法扩展参数解析。
func TestParseArgsSyntaxOptions(t *testing.T) {
	if extensions.Compiled().Has(extensions.SyntaxContinue | extensions.SyntaxSwitch) {
		// 只有当前构建包含两个扩展时，才验证 extended 后局部关闭 switch 的正向路径。
		options, err := ParseArgs([]string{"--gluac-syntax=extended", "--gluac-disable-syntax", "switch", "input.lua"})
		if err != nil {
			// 合法语法扩展参数不应失败。
			t.Fatalf("ParseArgs syntax failed: %v", err)
		}
		if !options.SyntaxExtensionsSet || !options.SyntaxExtensions.Has(extensions.SyntaxContinue) || !options.SyntaxExtensions.Has(extensions.SyntaxSwitch) {
			// extended 必须映射到当前构建内所有扩展。
			t.Fatalf("syntax extensions = %#v set=%v", options.SyntaxExtensions, options.SyntaxExtensionsSet)
		}
		if finalSyntax := syntaxForOptions(options); finalSyntax.Has(extensions.SyntaxSwitch) || !finalSyntax.Has(extensions.SyntaxContinue) {
			// 最终集合应只保留 continue。
			t.Fatalf("final syntax = %#v", finalSyntax)
		}
	}

	options, err := ParseArgs([]string{"--gluac-syntax", "lua53", "input.lua"})
	if err != nil {
		// lua53 是合法语法模式。
		t.Fatalf("ParseArgs lua53 failed: %v", err)
	}
	if syntaxForOptions(options) != extensions.None() {
		// lua53 模式必须关闭全部扩展。
		t.Fatalf("lua53 syntax = %#v", syntaxForOptions(options))
	}
}

// TestCompileSourceWithSyntaxDisablesExtensions 验证 gluac 源码编译入口遵守语法开关。
func TestCompileSourceWithSyntaxDisablesExtensions(t *testing.T) {
	if _, err := CompileSourceWithSyntax("local continue = 1\nreturn continue\n", "@lua53.lua", extensions.None()); err != nil {
		// 关闭扩展后同名变量仍是合法 Lua 5.3 代码。
		t.Fatalf("CompileSourceWithSyntax lua53 identifiers failed: %v", err)
	}
	if _, err := CompileSourceWithSyntax("while true do continue end\n", "@lua53.lua", extensions.None()); err == nil {
		// 关闭扩展后 continue 语句必须被 parser 拒绝。
		t.Fatalf("CompileSourceWithSyntax should reject disabled continue")
	}
}

// TestCompileSourceSimpleFunctionBodyPrototypeShape 固定简单函数体紧凑编译候选必须保持的 Proto 形态。
func TestCompileSourceSimpleFunctionBodyPrototypeShape(t *testing.T) {
	// 编译官方 compile_3000_functions 同构的单函数样例，锁定未来 fast path 的 bytecode/debug 输出边界。
	proto, err := CompileSource("function f0(x) return x + 7 end\nreturn f0(1)\n", "@simple-function.lua")
	if err != nil {
		// 合法样例编译失败说明 parser/codegen 入口发生回归。
		t.Fatalf("CompileSource simple function failed: %v", err)
	}
	if len(proto.Protos) != 1 {
		// 顶层 function 声明必须生成一个子 Proto。
		t.Fatalf("unexpected child proto count=%d", len(proto.Protos))
	}

	child := proto.Protos[0]
	if child.NumParams != 1 || child.IsVararg || child.MaxStackSize != 2 {
		// 简单函数体只有单参数、非 vararg，并且只需要参数槽与返回槽两个寄存器。
		t.Fatalf("unexpected child header params=%d vararg=%v maxstack=%d", child.NumParams, child.IsVararg, child.MaxStackSize)
	}
	if child.LineDefined != 1 || child.LastLineDefined != 1 {
		// debug.getinfo 依赖函数定义起止行，紧凑路径不能丢失源码行范围。
		t.Fatalf("unexpected child line range=%d,%d", child.LineDefined, child.LastLineDefined)
	}
	if len(child.Constants) != 1 || child.Constants[0].Kind != bytecode.ConstantInteger || child.Constants[0].Integer != 7 {
		// return x + 7 必须只保留整数常量 7。
		t.Fatalf("unexpected child constants=%+v", child.Constants)
	}
	if len(child.Code) != 2 {
		// 目标形态当前应只生成 ADD 和 RETURN 两条指令。
		t.Fatalf("unexpected child code length=%d code=%v", len(child.Code), child.Code)
	}
	assertInstructionFields(t, child.Code[0], bytecode.OpAdd, 1, 0, bytecode.BitRK)
	assertInstructionFields(t, child.Code[1], bytecode.OpReturn, 1, 2, 0)
	if len(child.LineInfo) != 2 || child.LineInfo[0] != 1 || child.LineInfo[1] != 1 {
		// 每条子函数指令都必须映射到函数定义行，供 debug hook 和反汇编展示。
		t.Fatalf("unexpected child lineinfo=%v", child.LineInfo)
	}
	if len(child.LocalVars) != 1 || child.LocalVars[0].Name != "x" || child.LocalVars[0].StartPC != 0 || child.LocalVars[0].EndPC != 2 {
		// 参数 x 的局部变量生命周期必须覆盖完整函数体指令区间。
		t.Fatalf("unexpected child locals=%+v", child.LocalVars)
	}
	if len(child.Upvalues) != 0 {
		// 目标函数体不读取外层变量，不能产生 upvalue。
		t.Fatalf("unexpected child upvalues=%+v", child.Upvalues)
	}
}

// TestCompileSourceSimpleFunctionBodyDebugAndListGuards 固定简单函数体紧凑候选的 debug 与列表输出。
func TestCompileSourceSimpleFunctionBodyDebugAndListGuards(t *testing.T) {
	// 使用 luac 编译入口生成 binary chunk，确保后续 guard 覆盖的是 gluac 产物而非 DoString 直编译。
	source := `function f0(x) return x + 7 end
local info = debug.getinfo(f0, "Slu")
assert(info.what == "Lua", info.what)
assert(info.linedefined == 1, info.linedefined)
assert(info.lastlinedefined == 1, info.lastlinedefined)
assert(info.nparams == 1, info.nparams)
assert(info.isvararg == false, tostring(info.isvararg))
assert(debug.getlocal(f0, 1) == "x")
assert(not debug.getlocal(f0, 2))
simpleResult = f0(35)
assert(simpleResult == 42, simpleResult)
`
	chunk, err := DumpSource(source, "@simple-function-debug.lua", false)
	if err != nil {
		// 编译失败说明简单函数体 guard 样例不再合法。
		t.Fatalf("DumpSource simple function debug failed: %v", err)
	}

	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "simple-function-debug.luac")
	if err := os.WriteFile(chunkPath, chunk, 0o644); err != nil {
		// 测试 chunk 必须可写入临时目录。
		t.Fatalf("write simple function chunk: %v", err)
	}

	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// debug.getinfo 与 debug.getlocal 依赖标准库已注册。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := lua.DoFile(state, chunkPath); err != nil {
		// 执行失败时说明 luac 产物的 debug metadata 或函数执行语义已偏离。
		t.Fatalf("DoFile simple function debug chunk failed: %v", err)
	}
	result, err := lua.GetGlobal(state, "simpleResult")
	if err != nil {
		// 全局读取不应失败。
		t.Fatalf("GetGlobal simpleResult failed: %v", err)
	}
	if result.Kind != runtime.KindInteger || result.Integer != 42 {
		// 简单函数仍必须按普通 Lua 语义返回参数加整数常量。
		t.Fatalf("simpleResult = %#v, want integer 42", result)
	}

	var stdout bytes.Buffer
	if err := Run([]string{"-l", "-l", chunkPath}, Streams{Stdout: &stdout}); err != nil {
		// 刚生成的 binary chunk 必须可被 gluac -l -l 读取。
		t.Fatalf("Run list simple function chunk failed: %v", err)
	}
	listOutput := stdout.String()
	expectedFragments := []string{
		"child[0] <@simple-function-debug.lua:1,1> params=1 vararg=false maxstack=2",
		"[0000] line=1 ADD       A=1 B=R(0) C=K(0)",
		"[0001] line=1 RETURN",
		"locals (1):",
		"name=\"x\" pc=[0,2)",
		"debug child[0] source=\"@simple-function-debug.lua\" lines=1..1 code=2 constants=1 children=0",
		"lineinfo=[1 1]",
		"local[0] name=\"x\" pc=[0,2)",
	}
	for _, expectedFragment := range expectedFragments {
		// 每个片段都对应 compact fast path 必须保持的 debug 或 luac -l -l 可见字段。
		if !strings.Contains(listOutput, expectedFragment) {
			// 缺少任一片段都说明列表输出或 debug metadata 已经漂移。
			t.Fatalf("list output missing %q in:\n%s", expectedFragment, listOutput)
		}
	}
}

// TestCompileSourceSimpleFunctionBodySignatureFallbackGuards 固定非目标函数签名与命名形态必须回退普通路径。
func TestCompileSourceSimpleFunctionBodySignatureFallbackGuards(t *testing.T) {
	// 表驱动覆盖 compactSimpleFunctionBody 第一批禁止进入的函数头和函数名形态。
	tests := []struct {
		name   string
		source string
		check  func(t *testing.T, proto *bytecode.Proto, child *bytecode.Proto)
	}{
		{
			name: "table field function name",
			source: `local t = {}
function t.f(x) return x + 7 end
return t.f(1)
`,
			check: func(t *testing.T, proto *bytecode.Proto, child *bytecode.Proto) {
				// table 字段函数定义必须保留顶层 SETTABLE，而不是被误当成普通全局 function 声明。
				if !containsInstructionOp(proto, bytecode.OpSetTable) {
					t.Fatalf("table field function should assign through SETTABLE, code=%v", proto.Code)
				}
				if child.NumParams != 1 || child.IsVararg || len(child.LocalVars) != 1 || child.LocalVars[0].Name != "x" {
					t.Fatalf("table field child header/locals changed: params=%d vararg=%v locals=%+v", child.NumParams, child.IsVararg, child.LocalVars)
				}
			},
		},
		{
			name: "method receiver",
			source: `local t = {}
function t:f(x) return x + 7 end
return t:f(1)
`,
			check: func(t *testing.T, proto *bytecode.Proto, child *bytecode.Proto) {
				// method 定义会注入 self 参数，紧凑路径不能按单参数函数体处理。
				if child.NumParams != 2 || child.IsVararg || len(child.LocalVars) < 2 {
					t.Fatalf("method child header/locals changed: params=%d vararg=%v locals=%+v", child.NumParams, child.IsVararg, child.LocalVars)
				}
				if child.LocalVars[0].Name != "self" || child.LocalVars[1].Name != "x" {
					t.Fatalf("method locals should keep self/x: %+v", child.LocalVars)
				}
				assertInstructionFields(t, child.Code[0], bytecode.OpAdd, 2, 1, bytecode.BitRK)
				if !containsInstructionOp(proto, bytecode.OpSelf) {
					t.Fatalf("method call should keep SELF opcode, code=%v", proto.Code)
				}
			},
		},
		{
			name: "vararg parameter",
			source: `function f0(x, ...) return x + 7 end
return f0(1)
`,
			check: func(t *testing.T, proto *bytecode.Proto, child *bytecode.Proto) {
				// vararg 标记必须保留，compact 候选只允许非 vararg。
				if child.NumParams != 1 || !child.IsVararg {
					t.Fatalf("vararg child header changed: params=%d vararg=%v", child.NumParams, child.IsVararg)
				}
			},
		},
		{
			name: "multiple parameters",
			source: `function f0(x, y) return x + 7 end
return f0(1, 2)
`,
			check: func(t *testing.T, proto *bytecode.Proto, child *bytecode.Proto) {
				// 多参数函数必须保留两个形参和额外返回临时寄存器。
				if child.NumParams != 2 || child.IsVararg || child.MaxStackSize != 3 || len(child.LocalVars) < 2 {
					t.Fatalf("multi-param child header/locals changed: params=%d vararg=%v maxstack=%d locals=%+v", child.NumParams, child.IsVararg, child.MaxStackSize, child.LocalVars)
				}
				if child.LocalVars[0].Name != "x" || child.LocalVars[1].Name != "y" {
					t.Fatalf("multi-param locals should keep x/y: %+v", child.LocalVars)
				}
				assertInstructionFields(t, child.Code[0], bytecode.OpAdd, 2, 0, bytecode.BitRK)
			},
		},
		{
			name: "local upvalue operand",
			source: `local y = 7
function f0(x) return x + y end
return f0(1)
`,
			check: func(t *testing.T, proto *bytecode.Proto, child *bytecode.Proto) {
				// 读取外层 local y 必须保留 upvalue 捕获和 GETUPVAL 指令。
				if len(child.Upvalues) != 1 || child.Upvalues[0].Name != "y" {
					t.Fatalf("upvalue child should capture y: %+v", child.Upvalues)
				}
				if !containsInstructionOp(child, bytecode.OpGetUpval) {
					t.Fatalf("upvalue child should read GETUPVAL, code=%v", child.Code)
				}
			},
		},
		{
			name: "nested closure body",
			source: `function f0(x)
  local function g() return x + 7 end
  return g()
end
return f0(1)
`,
			check: func(t *testing.T, proto *bytecode.Proto, child *bytecode.Proto) {
				// 嵌套函数体必须保留子 Proto、CLOSURE 和尾调用，不能折叠成直接 ADD/RETURN。
				if len(child.Protos) != 1 {
					t.Fatalf("nested body should keep child proto: children=%d", len(child.Protos))
				}
				if !containsInstructionOp(child, bytecode.OpClosure) || !containsInstructionOp(child, bytecode.OpTailCall) {
					t.Fatalf("nested body should keep CLOSURE/TAILCALL, code=%v", child.Code)
				}
			},
		},
	}

	for _, test := range tests {
		// 每个非目标样例都必须独立编译并保留自身普通路径特征。
		t.Run(test.name, func(t *testing.T) {
			proto, err := CompileSource(test.source, "@simple-function-fallback-"+test.name+".lua")
			if err != nil {
				// 合法非目标样例必须仍能编译。
				t.Fatalf("CompileSource fallback sample failed: %v", err)
			}
			if len(proto.Protos) == 0 {
				// 每个样例都至少定义一个函数，用于验证子 Proto 普通形态。
				t.Fatalf("missing child proto")
			}
			test.check(t, proto, proto.Protos[0])
		})
	}
}

// TestCompileSourceSimpleFunctionBodyExpressionFallbackGuards 固定复杂表达式必须回退普通路径。
func TestCompileSourceSimpleFunctionBodyExpressionFallbackGuards(t *testing.T) {
	// 表驱动覆盖 compactSimpleFunctionBody 禁止直接识别的表达式形态。
	tests := []struct {
		name   string
		source string
		check  func(t *testing.T, child *bytecode.Proto)
	}{
		{
			name: "function call operand",
			source: `function f0(x) return x + tonumber("7") end
return f0(1)
`,
			check: func(t *testing.T, child *bytecode.Proto) {
				// 函数调用操作数必须保留 CALL，不能折叠成常量整数加法。
				if !containsInstructionOp(child, bytecode.OpCall) {
					t.Fatalf("call operand should keep CALL, code=%v", child.Code)
				}
			},
		},
		{
			name: "field access operand",
			source: `local t = {y = 7}
function f0(x) return x + t.y end
return f0(1)
`,
			check: func(t *testing.T, child *bytecode.Proto) {
				// 字段访问必须保留 table 读取和外层 table upvalue 捕获。
				if !containsInstructionOp(child, bytecode.OpGetTable) || !containsInstructionOp(child, bytecode.OpGetUpval) {
					t.Fatalf("field operand should keep GETUPVAL/GETTABLE, code=%v upvalues=%+v", child.Code, child.Upvalues)
				}
				if len(child.Upvalues) != 1 || child.Upvalues[0].Name != "t" {
					t.Fatalf("field operand should capture t: %+v", child.Upvalues)
				}
			},
		},
		{
			name: "index access operand",
			source: `local t = {7}
function f0(x) return x + t[1] end
return f0(1)
`,
			check: func(t *testing.T, child *bytecode.Proto) {
				// 索引访问必须保留 table 读取，不能把下标读取当成整数 literal。
				if !containsInstructionOp(child, bytecode.OpGetTable) {
					t.Fatalf("index operand should keep GETTABLE, code=%v", child.Code)
				}
				if len(child.Upvalues) != 1 || child.Upvalues[0].Name != "t" {
					t.Fatalf("index operand should capture t: %+v", child.Upvalues)
				}
			},
		},
		{
			name: "concat expression",
			source: `function f0(x) return x .. "7" end
return f0(1)
`,
			check: func(t *testing.T, child *bytecode.Proto) {
				// 拼接表达式必须保留 CONCAT，不能进入整数 ADD 紧凑路径。
				if !containsInstructionOp(child, bytecode.OpConcat) {
					t.Fatalf("concat expression should keep CONCAT, code=%v", child.Code)
				}
			},
		},
		{
			name: "power expression",
			source: `function f0(x) return x ^ 2 end
return f0(2)
`,
			check: func(t *testing.T, child *bytecode.Proto) {
				// 幂运算不是目标整数加法形态，必须保留 POW。
				if !containsInstructionOp(child, bytecode.OpPow) {
					t.Fatalf("power expression should keep POW, code=%v", child.Code)
				}
			},
		},
		{
			name: "number literal",
			source: `function f0(x) return x + 7.5 end
return f0(1)
`,
			check: func(t *testing.T, child *bytecode.Proto) {
				// 非 integer 常量不能进入只接受整数 literal 的 compact summary。
				if len(child.Constants) != 1 || child.Constants[0].Kind != bytecode.ConstantNumber || child.Constants[0].Number != 7.5 {
					t.Fatalf("number literal should remain number constant: %+v", child.Constants)
				}
			},
		},
	}

	for _, test := range tests {
		// 每个复杂表达式样例都必须独立编译并保留普通 codegen 特征。
		t.Run(test.name, func(t *testing.T) {
			proto, err := CompileSource(test.source, "@simple-function-expression-"+test.name+".lua")
			if err != nil {
				// 合法表达式样例必须仍能编译。
				t.Fatalf("CompileSource expression fallback sample failed: %v", err)
			}
			if len(proto.Protos) != 1 {
				// 每个样例都只定义一个待检查函数。
				t.Fatalf("unexpected child proto count=%d", len(proto.Protos))
			}
			test.check(t, proto.Protos[0])
		})
	}
}

// TestCompileSourceSimpleFunctionBodyControlFlowAndErrorFallbackGuards 固定控制流与语法错误必须回退普通路径。
func TestCompileSourceSimpleFunctionBodyControlFlowAndErrorFallbackGuards(t *testing.T) {
	// goto/label 会改变函数体控制流，compactSimpleFunctionBody 必须保留普通 parser/codegen 的跳转指令。
	proto, err := CompileSource(`function f0(x)
  goto done
  x = x + 1
  ::done::
  return x + 7
end
return f0(1)
`, "@simple-function-control-flow.lua")
	if err != nil {
		// 合法控制流样例必须仍能编译，失败说明普通 fallback 路径或 goto 语义回归。
		t.Fatalf("CompileSource control-flow fallback sample failed: %v", err)
	}
	if len(proto.Protos) != 1 {
		// 样例只定义一个待检查函数，子 Proto 数量漂移会影响后续断言语义。
		t.Fatalf("unexpected child proto count=%d", len(proto.Protos))
	}
	child := proto.Protos[0]
	if !containsInstructionOp(child, bytecode.OpJmp) {
		// goto 必须保留 JMP，紧凑路径不能误把函数体折叠成 ADD/RETURN。
		t.Fatalf("goto/label fallback should keep JMP, code=%v", child.Code)
	}
	if len(child.Code) <= 2 {
		// 带控制流的函数体不能退化为目标形态的两条指令。
		t.Fatalf("control-flow fallback should keep ordinary code, code=%v", child.Code)
	}

	_, err = CompileSource("function f0(x) return x + end\n", "@simple-function-syntax-error.lua")
	if err == nil {
		// 非法简单函数体必须返回 parser 错误，不能被试探解析吞掉或改写成合法 AST。
		t.Fatalf("CompileSource should reject incomplete expression")
	}
	var parseError parser.ParseError
	if !errors.As(err, &parseError) {
		// 语法错误必须保留结构化 ParseError，便于 CLI/load 路径继续格式化行列和 near token。
		t.Fatalf("expected parser.ParseError, got %T: %v", err, err)
	}
	if parseError.Position.Line != 1 || parseError.Position.Column == 0 || !strings.Contains(parseError.Message, "expression") {
		// 错误位置和消息必须仍指向失败表达式，防止试探解析 reset 后丢失 token 位置。
		t.Fatalf("unexpected parse error position/message: %+v", parseError)
	}
}

// TestCompileSourceSimpleFunctionBodyNonTargetKeepsOrdinaryShape 固定非目标函数体必须回退普通 AST/codegen。
func TestCompileSourceSimpleFunctionBodyNonTargetKeepsOrdinaryShape(t *testing.T) {
	// 引入局部变量 y，使函数体不再满足 `return <param> + <integer>` 紧凑候选形态。
	proto, err := CompileSource("function f0(x) local y = x return y + 7 end\nreturn f0(1)\n", "@non-target-function.lua")
	if err != nil {
		// 合法非目标样例仍必须正常编译。
		t.Fatalf("CompileSource non-target function failed: %v", err)
	}
	if len(proto.Protos) != 1 {
		// 顶层 function 声明必须生成一个子 Proto。
		t.Fatalf("unexpected child proto count=%d", len(proto.Protos))
	}

	child := proto.Protos[0]
	if child.NumParams != 1 || child.IsVararg || child.MaxStackSize != 3 {
		// 局部变量 y 需要额外寄存器，不能被误压成目标紧凑函数体的两个寄存器形态。
		t.Fatalf("unexpected child header params=%d vararg=%v maxstack=%d", child.NumParams, child.IsVararg, child.MaxStackSize)
	}
	if len(child.Code) != 3 {
		// 非目标函数体当前应保留 MOVE、ADD、RETURN 三条普通 codegen 指令。
		t.Fatalf("unexpected child code length=%d code=%v", len(child.Code), child.Code)
	}
	assertInstructionFields(t, child.Code[0], bytecode.OpMove, 1, 0, 0)
	assertInstructionFields(t, child.Code[1], bytecode.OpAdd, 2, 1, bytecode.BitRK)
	assertInstructionFields(t, child.Code[2], bytecode.OpReturn, 2, 2, 0)
	if len(child.LocalVars) != 2 || child.LocalVars[0].Name != "x" || child.LocalVars[1].Name != "y" {
		// 普通路径必须保留参数 x 和局部 y 的 debug local 记录。
		t.Fatalf("unexpected child locals=%+v", child.LocalVars)
	}
	if child.LocalVars[0].StartPC != 0 || child.LocalVars[0].EndPC != 3 || child.LocalVars[1].StartPC != 1 || child.LocalVars[1].EndPC != 3 {
		// 局部变量生命周期必须覆盖当前普通 codegen 指令区间。
		t.Fatalf("unexpected child local ranges=%+v", child.LocalVars)
	}
}

// TestCompileSourceMultipleSimpleFunctionsKeepDebugShape 固定多个顶层简单函数声明的 child Proto 与调试输出。
//
// 未来 compile-only streaming 路径若跳过 FunctionStatement AST 构造，也必须生成与普通 codegen 等价的
// 子 Proto 顺序、行号、local debug 记录和反汇编可见 opcode。
func TestCompileSourceMultipleSimpleFunctionsKeepDebugShape(t *testing.T) {
	source := "function f0(x) return x + 7 end\nfunction f1(y) return y + 9 end\nreturn f0(1) + f1(2)\n"
	proto, err := CompileSource(source, "@multi-simple-functions.lua")
	if err != nil {
		// 合法多函数样例必须可编译。
		t.Fatalf("CompileSource multi simple functions failed: %v", err)
	}
	if len(proto.Protos) != 2 {
		// 两个顶层 function 声明必须按源码顺序生成两个 child Proto。
		t.Fatalf("unexpected child proto count=%d", len(proto.Protos))
	}
	if len(proto.Code) != 12 {
		// 父 Proto 必须保留两次全局函数写入、两次全局读取调用和最终返回，streaming 不能只生成 child。
		t.Fatalf("unexpected parent code length=%d code=%v", len(proto.Code), proto.Code)
	}
	if !reflect.DeepEqual(proto.LineInfo, []int{1, 1, 2, 2, 3, 3, 3, 3, 3, 3, 3, 3}) {
		// 父 Proto 行号必须保留 function 定义行与最终 return 行，供错误 PC、traceback 和 luac 列表输出使用。
		t.Fatalf("unexpected parent lineinfo=%v", proto.LineInfo)
	}
	if len(proto.Upvalues) != 1 || proto.Upvalues[0].Name != "_ENV" || !proto.Upvalues[0].InStack || proto.Upvalues[0].Index != 0 {
		// 顶层 function 声明写入全局表，必须继续捕获 `_ENV` upvalue。
		t.Fatalf("unexpected parent upvalues=%+v", proto.Upvalues)
	}
	if len(proto.Constants) != 4 ||
		proto.Constants[0].Kind != bytecode.ConstantString || proto.Constants[0].String != "f0" ||
		proto.Constants[1].Kind != bytecode.ConstantString || proto.Constants[1].String != "f1" ||
		proto.Constants[2].Kind != bytecode.ConstantInteger || proto.Constants[2].Integer != 1 ||
		proto.Constants[3].Kind != bytecode.ConstantInteger || proto.Constants[3].Integer != 2 {
		// 父 Proto 常量顺序决定 SETTABUP/GETTABUP 和 final return 调用参数，streaming 不得重排。
		t.Fatalf("unexpected parent constants=%+v", proto.Constants)
	}
	assertInstructionABxFields(t, proto.Code[0], bytecode.OpClosure, 0, 0)
	assertInstructionFields(t, proto.Code[1], bytecode.OpSetTabUp, 0, bytecode.BitRK, 0)
	assertInstructionABxFields(t, proto.Code[2], bytecode.OpClosure, 0, 1)
	assertInstructionFields(t, proto.Code[3], bytecode.OpSetTabUp, 0, bytecode.BitRK+1, 0)
	assertInstructionFields(t, proto.Code[4], bytecode.OpGetTabUp, 0, 0, bytecode.BitRK)
	assertInstructionABxFields(t, proto.Code[5], bytecode.OpLoadK, 1, 2)
	assertInstructionFields(t, proto.Code[6], bytecode.OpCall, 0, 2, 2)
	assertInstructionFields(t, proto.Code[7], bytecode.OpGetTabUp, 1, 0, bytecode.BitRK+1)
	assertInstructionABxFields(t, proto.Code[8], bytecode.OpLoadK, 2, 3)
	assertInstructionFields(t, proto.Code[9], bytecode.OpCall, 1, 2, 2)
	assertInstructionFields(t, proto.Code[10], bytecode.OpAdd, 0, 0, 1)
	assertInstructionFields(t, proto.Code[11], bytecode.OpReturn, 0, 2, 0)

	tests := []struct {
		name       string
		childIndex int
		line       int
		paramName  string
		integer    int64
	}{
		{name: "first", childIndex: 0, line: 1, paramName: "x", integer: 7},
		{name: "second", childIndex: 1, line: 2, paramName: "y", integer: 9},
	}
	for _, test := range tests {
		// 每个 child Proto 独立校验，确保 streaming 不交换函数顺序或复用错误行号。
		t.Run(test.name, func(t *testing.T) {
			child := proto.Protos[test.childIndex]
			if child.LineDefined != test.line || child.LastLineDefined != test.line {
				// debug.getinfo 和 luac -l -l 依赖函数定义行范围。
				t.Fatalf("unexpected child line range=%d,%d want line=%d", child.LineDefined, child.LastLineDefined, test.line)
			}
			if len(child.LineInfo) != 2 || child.LineInfo[0] != test.line || child.LineInfo[1] != test.line {
				// ADD 与 RETURN 都必须映射回对应源码行。
				t.Fatalf("unexpected child lineinfo=%v want line=%d", child.LineInfo, test.line)
			}
			if len(child.Constants) != 1 || child.Constants[0].Kind != bytecode.ConstantInteger || child.Constants[0].Integer != test.integer {
				// 每个 child 只应保留自己的 integer 常量。
				t.Fatalf("unexpected child constants=%+v want integer=%d", child.Constants, test.integer)
			}
			if len(child.LocalVars) != 1 || child.LocalVars[0].Name != test.paramName || child.LocalVars[0].StartPC != 0 || child.LocalVars[0].EndPC != 2 {
				// 参数 local 记录必须保留，供 debug.getlocal 和详细反汇编使用。
				t.Fatalf("unexpected child locals=%+v want param=%q", child.LocalVars, test.paramName)
			}
			if len(child.Code) != 2 {
				// 目标形态仍应生成 ADD 和 RETURN 两条指令。
				t.Fatalf("unexpected child code=%v", child.Code)
			}
			assertInstructionFields(t, child.Code[0], bytecode.OpAdd, 1, 0, bytecode.BitRK)
			assertInstructionFields(t, child.Code[1], bytecode.OpReturn, 1, 2, 0)
		})
	}

	debugDump := DebugDumpProto(proto)
	for _, want := range []string{
		`debug child[0] source="@multi-simple-functions.lua" lines=1..1 code=2 constants=1 children=0`,
		`debug child[1] source="@multi-simple-functions.lua" lines=2..2 code=2 constants=1 children=0`,
		`local[0] name="x" pc=[0,2)`,
		`local[0] name="y" pc=[0,2)`,
	} {
		// 详细调试 dump 是 `luac -l -l` 的稳定代理，必须保留 child 行号和 local 名称。
		if !strings.Contains(debugDump, want) {
			t.Fatalf("debug dump missing %q:\n%s", want, debugDump)
		}
	}

	minimal := MinimalDisassemblyProto(proto)
	for _, want := range []string{
		"child[0] code=2",
		"child[1] code=2",
		"[0000] line=1 ADD",
		"[0001] line=1 RETURN",
		"[0000] line=2 ADD",
		"[0001] line=2 RETURN",
	} {
		// 最小反汇编必须继续展示两段 child 指令和各自 lineinfo。
		if !strings.Contains(minimal, want) {
			t.Fatalf("minimal disassembly missing %q:\n%s", want, minimal)
		}
	}
}

// containsInstructionOp 判断 Proto 指令区是否包含指定 opcode。
func containsInstructionOp(proto *bytecode.Proto, opCode bytecode.OpCode) bool {
	// 线性扫描足够测试断言使用，保持辅助函数无额外索引状态。
	for _, instruction := range proto.Code {
		// 命中目标 opcode 时立即返回，便于调用方表达普通路径特征。
		if instruction.OpCode() == opCode {
			return true
		}
	}
	return false
}

// assertInstructionFields 校验 Lua 5.3 ABC 指令的 opcode 与 A/B/C 字段。
func assertInstructionFields(t *testing.T, instruction bytecode.Instruction, opCode bytecode.OpCode, a int, b int, c int) {
	// 标记 helper 后，失败行会指向调用方测试断言。
	t.Helper()
	if instruction.OpCode() != opCode || instruction.A() != a || instruction.B() != b || instruction.C() != c {
		// 指令字段不匹配时输出完整字段，便于判断是 opcode 还是寄存器/RK 编码回归。
		t.Fatalf("instruction mismatch got=%s A=%d B=%d C=%d want=%s A=%d B=%d C=%d", instruction.OpCode().Name(), instruction.A(), instruction.B(), instruction.C(), opCode.Name(), a, b, c)
	}
}

// assertInstructionABxFields 校验 Lua 5.3 ABx 指令的 opcode、A 与 Bx 字段。
func assertInstructionABxFields(t *testing.T, instruction bytecode.Instruction, opCode bytecode.OpCode, a int, bx int) {
	// 标记 helper 后，失败行会指向调用方测试断言。
	t.Helper()
	if instruction.OpCode() != opCode || instruction.A() != a || instruction.Bx() != bx {
		// 指令字段不匹配时输出完整字段，便于定位 child Proto 或常量索引重排。
		t.Fatalf("instruction mismatch got=%s A=%d Bx=%d want=%s A=%d Bx=%d", instruction.OpCode().Name(), instruction.A(), instruction.Bx(), opCode.Name(), a, bx)
	}
}

// BenchmarkCompileSource3000Functions 度量 gluac 兼容编译入口处理大量顶层函数定义的成本。
func BenchmarkCompileSource3000Functions(b *testing.B) {
	// 使用官方 benchmark 同形态源码，覆盖 parser、semantic analyzer 与 codegen 的编译期路径。
	source := buildCompile3000FunctionsSource()
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮重新编译完整源码，避免复用 AST 或 Proto 掩盖编译期分配。
		if _, err := CompileSource(source, "@compile_3000_functions.lua"); err != nil {
			// 编译失败说明 benchmark fixture 不再是合法 Lua 源码。
			b.Fatalf("CompileSource failed: %v", err)
		}
	}
}

// buildCompile3000FunctionsSource 构造与官方完整 benchmark 对齐的大量函数定义源码。
func buildCompile3000FunctionsSource() string {
	// 预估容量减少 benchmark fixture 构造本身的无关扩容成本。
	var builder strings.Builder
	builder.Grow(128 * 3000)
	for functionIndex := 0; functionIndex < 3000; functionIndex++ {
		// 每个函数体保持同一结构，便于稳定覆盖 parser/codegen 的重复定义成本。
		fmt.Fprintf(&builder, "function f%d(x) return x + %d end\n", functionIndex, functionIndex)
	}
	builder.WriteString("return f2999(1)\n")
	return builder.String()
}

// TestParseArgsRejectsInvalidInput 验证参数错误会在读取文件前暴露。
func TestParseArgsRejectsInvalidInput(t *testing.T) {
	// 每个参数组合都应返回明确错误。
	invalidArgs := [][]string{
		{"-o"},
		{"--unknown", "input.lua"},
		{"--syntax", "lua53", "input.lua"},
		{"--disable-syntax", "switch", "input.lua"},
		{"--opcode-trace", "input.lua"},
		{"--step-trace", "input.lua"},
		{"--minimal-disassembly", "input.lua"},
		{},
	}
	for _, args := range invalidArgs {
		// 参数解析失败即可，不需要断言完整错误文案。
		if _, err := ParseArgs(args); err == nil {
			// 没有错误会导致 CLI 后续行为歧义。
			t.Fatalf("ParseArgs(%v) succeeded unexpectedly", args)
		}
	}
}

// TestRunCombinesMultipleInputFiles 验证 gluac 多输入文件会生成可执行 wrapper chunk。
func TestRunCombinesMultipleInputFiles(t *testing.T) {
	// 使用两个文件共享全局变量，验证 wrapper 顺序执行且子 chunk 捕获同一个 _ENV。
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "one.lua")
	secondPath := filepath.Join(dir, "two.lua")
	outputPath := filepath.Join(dir, "combined.luac")
	if err := os.WriteFile(firstPath, []byte("x = 41\n"), 0o644); err != nil {
		// 第一个输入文件必须可写。
		t.Fatalf("write first source: %v", err)
	}
	if err := os.WriteFile(secondPath, []byte("x = x + 1\n"), 0o644); err != nil {
		// 第二个输入文件必须可写。
		t.Fatalf("write second source: %v", err)
	}
	if err := Run([]string{"-o", outputPath, firstPath, secondPath}, Streams{}); err != nil {
		// 多输入文件应按官方 luac 语义合并，不再拒绝。
		t.Fatalf("Run multiple input compile error: %v", err)
	}
	outputBytes, err := os.ReadFile(outputPath)
	if err != nil {
		// 输出文件必须存在。
		t.Fatalf("read combined chunk: %v", err)
	}
	combinedProto, err := bytecode.LoadBinaryChunk(bytes.NewReader(outputBytes))
	if err != nil {
		// 组合 chunk 必须仍是合法 Lua 5.3 binary chunk。
		t.Fatalf("load combined chunk: %v", err)
	}
	if len(combinedProto.Protos) != 2 || len(combinedProto.Code) != 5 {
		// wrapper 应包含两个子 Proto，并按 CLOSURE/CALL/CLOSURE/CALL/RETURN 形态执行。
		t.Fatalf("combined shape code=%d children=%d", len(combinedProto.Code), len(combinedProto.Protos))
	}

	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if err := lua.DoFile(state, outputPath); err != nil {
		// glua 必须能加载并执行 gluac 生成的组合 chunk。
		t.Fatalf("DoFile combined chunk failed: %v", err)
	}
	value, err := lua.GetGlobal(state, "x")
	if err != nil {
		// 全局读取不应失败。
		t.Fatalf("GetGlobal x failed: %v", err)
	}
	if value.Kind != runtime.KindInteger || value.Integer != 42 {
		// 两个输入文件必须按顺序共享环境执行。
		t.Fatalf("global x = %#v, want integer 42", value)
	}
}

// TestDumpSourceAndDisassembleChunk 验证源码可编译为 binary chunk 且可反汇编。
func TestDumpSourceAndDisassembleChunk(t *testing.T) {
	// 使用当前 codegen 已支持的最小源码，避免跨越 VM 执行依赖。
	chunk, err := DumpSource("local x = 1\nreturn x\n", "@unit.lua", false)
	if err != nil {
		// 编译失败时附加最小反汇编帮助定位。
		t.Fatalf("DumpSource error: %v\n%s", err, MinimalDisassembly("local x = 1\nreturn x\n", "@unit.lua"))
	}
	if !bytes.HasPrefix(chunk, []byte(bytecode.ChunkSignature)) {
		// binary chunk 必须以 Lua 5.3 签名开头。
		t.Fatalf("chunk signature mismatch: % x", chunk[:4])
	}
	text, err := DisassembleChunk(chunk)
	if err != nil {
		// 刚生成的 chunk 必须能被 loader 读取。
		t.Fatalf("DisassembleChunk error: %v", err)
	}
	if !strings.Contains(text, "LOADK") || !strings.Contains(text, "RETURN") {
		// 反汇编至少应包含当前源码对应的核心指令。
		t.Fatalf("disassembly missing opcodes:\n%s", text)
	}
}

// TestDebugTraceAndMinimalDisassembly 验证 debug dump、opcode trace、step trace 和最小反汇编。
func TestDebugTraceAndMinimalDisassembly(t *testing.T) {
	// 先编译 Proto，后续所有调试文本都复用同一产物。
	proto, err := CompileSource("local x = 1\nreturn x\n", "@trace.lua")
	if err != nil {
		// 编译失败说明测试源码或 codegen 出现回归。
		t.Fatalf("CompileSource error: %v", err)
	}
	debugText := DebugDumpProto(proto)
	if !strings.Contains(debugText, "lineinfo=") || !strings.Contains(debugText, "local[0]") {
		// debug dump 必须暴露 lineinfo 和 local var，满足 Proto 调试需求。
		t.Fatalf("debug dump missing details:\n%s", debugText)
	}
	opcodeTrace := OpcodeTraceProto(proto)
	if !strings.Contains(opcodeTrace, "opcode-trace main") || !strings.Contains(opcodeTrace, "op=LOADK") {
		// opcode trace 必须按 pc 输出 opcode 名称。
		t.Fatalf("opcode trace missing details:\n%s", opcodeTrace)
	}
	stepTrace := StepTraceProto(proto)
	if !strings.Contains(stepTrace, "step-trace main") || !strings.Contains(stepTrace, "A=") {
		// step trace 必须包含寄存器字段，便于对照 VM Step。
		t.Fatalf("step trace missing details:\n%s", stepTrace)
	}
	minimal := MinimalDisassemblyProto(proto)
	if strings.Contains(minimal, "constants") || !strings.Contains(minimal, "RETURN") {
		// 最小反汇编应保持短文本，但仍包含 opcode。
		t.Fatalf("minimal disassembly mismatch:\n%s", minimal)
	}
}

// TestStripDebugKeepsExecutableShape 验证 -s 只剥离调试信息不破坏字节码结构。
func TestStripDebugKeepsExecutableShape(t *testing.T) {
	// 编译带局部变量的源码，确保 strip 前存在调试表。
	proto, err := CompileSource("local x = 1\nreturn x\n", "@strip.lua")
	if err != nil {
		// 编译失败说明测试源码或 codegen 出现回归。
		t.Fatalf("CompileSource error: %v", err)
	}
	stripped := StripDebug(proto)
	if len(stripped.Code) != len(proto.Code) || len(stripped.Constants) != len(proto.Constants) {
		// strip 不应改变指令和常量，否则 binary chunk 语义会变化。
		t.Fatalf("stripped shape mismatch: code=%d/%d constants=%d/%d", len(stripped.Code), len(proto.Code), len(stripped.Constants), len(proto.Constants))
	}
	if len(stripped.LineInfo) != 0 || len(stripped.LocalVars) != len(proto.LocalVars) || stripped.LocalVars[0].Name != "" {
		// lineinfo 应被剥离；local 生命周期保留但名称清空，供 stripped chunk 枚举 temporary local。
		t.Fatalf("debug tables not stripped: lineinfo=%v locals=%v", stripped.LineInfo, stripped.LocalVars)
	}
	if len(proto.LocalVars) == 0 {
		// 原始 Proto 的已实现调试表不能被 StripDebug 修改。
		t.Fatalf("original proto was modified: lineinfo=%v locals=%v", proto.LineInfo, proto.LocalVars)
	}
}

// TestRunCompileAndList 验证 cmd 级 Run 可写出 chunk 并读取 chunk 反汇编。
func TestRunCompileAndList(t *testing.T) {
	// 使用临时目录隔离输入输出文件。
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "input.lua")
	outputPath := filepath.Join(dir, "out.luac")
	if err := os.WriteFile(sourcePath, []byte("local x = 1\nreturn x\n"), 0o644); err != nil {
		// 测试夹具写入失败时无法继续。
		t.Fatalf("write source: %v", err)
	}
	if err := Run([]string{"-o", outputPath, sourcePath}, Streams{}); err != nil {
		// 正常编译写出不应失败。
		t.Fatalf("Run compile error: %v", err)
	}
	outputBytes, err := os.ReadFile(outputPath)
	if err != nil {
		// 输出文件必须存在。
		t.Fatalf("read output: %v", err)
	}
	if !bytes.HasPrefix(outputBytes, []byte(bytecode.ChunkSignature)) {
		// 输出内容必须是 binary chunk。
		t.Fatalf("output is not a chunk: % x", outputBytes[:4])
	}

	var stdout bytes.Buffer
	if err := Run([]string{"-l", "-l", outputPath}, Streams{Stdout: &stdout}); err != nil {
		// 反汇编刚生成的 chunk 不应失败。
		t.Fatalf("Run list error: %v", err)
	}
	if !strings.Contains(stdout.String(), "LOADK") || !strings.Contains(stdout.String(), "debug main") {
		// -l -l 必须包含反汇编和 debug dump。
		t.Fatalf("list output missing details:\n%s", stdout.String())
	}
}

// TestAttachMinimalDisassemblyOnFailure 验证测试失败时才输出最小反汇编。
func TestAttachMinimalDisassemblyOnFailure(t *testing.T) {
	// 成功 reporter 不应产生日志。
	passingReporter := &fakeFailureReporter{}
	AttachMinimalDisassemblyOnFailure(passingReporter, "local x = 1\nreturn x\n", "@pass.lua")
	passingReporter.runCleanups()
	if passingReporter.logs.Len() != 0 {
		// 成功测试不应输出最小反汇编，避免污染正常日志。
		t.Fatalf("passing reporter logs = %q", passingReporter.logs.String())
	}

	// 失败 reporter 应在 cleanup 阶段输出最小反汇编。
	failingReporter := &fakeFailureReporter{failed: true}
	AttachMinimalDisassemblyOnFailure(failingReporter, "local x = 1\nreturn x\n", "@fail.lua")
	failingReporter.runCleanups()
	if !strings.Contains(failingReporter.logs.String(), "minimal lua disassembly") || !strings.Contains(failingReporter.logs.String(), "RETURN") {
		// 失败测试必须携带短反汇编，帮助定位 codegen 或 VM 断言。
		t.Fatalf("failing reporter logs = %q", failingReporter.logs.String())
	}
}

// TestAttachMinimalProtoDisassemblyOnFailure 验证测试失败时可直接输出 Proto 最小反汇编。
func TestAttachMinimalProtoDisassemblyOnFailure(t *testing.T) {
	// 先编译 Proto，模拟 codegen/VM 测试中已有 Proto 的场景。
	proto, err := CompileSource("local x = 1\nreturn x\n", "@proto.lua")
	if err != nil {
		// 编译失败说明测试源码或 codegen 出现回归。
		t.Fatalf("CompileSource error: %v", err)
	}
	reporter := &fakeFailureReporter{failed: true}
	AttachMinimalProtoDisassemblyOnFailure(reporter, proto)
	reporter.runCleanups()
	if !strings.Contains(reporter.logs.String(), "minimal lua proto disassembly") || !strings.Contains(reporter.logs.String(), "LOADK") {
		// Proto helper 必须输出最小指令列表。
		t.Fatalf("proto reporter logs = %q", reporter.logs.String())
	}
}

// fakeFailureReporter 模拟 testing.TB 的失败、cleanup 和日志能力。
type fakeFailureReporter struct {
	// failed 表示测试最终是否失败。
	failed bool
	// cleanups 保存注册的 cleanup 回调。
	cleanups []func()
	// logs 保存 Logf 输出。
	logs bytes.Buffer
}

// Failed 返回当前 fake 测试是否失败。
func (reporter *fakeFailureReporter) Failed() bool {
	// 直接返回预置状态，测试用例可精确控制 cleanup 输出分支。
	return reporter.failed
}

// Cleanup 注册 cleanup 回调。
func (reporter *fakeFailureReporter) Cleanup(cleanup func()) {
	// cleanup 按注册顺序保存，runCleanups 统一执行。
	reporter.cleanups = append(reporter.cleanups, cleanup)
}

// Logf 记录格式化测试日志。
func (reporter *fakeFailureReporter) Logf(format string, args ...any) {
	// 追加换行模拟 testing.TB 日志块的可读性。
	_, _ = fmt.Fprintf(&reporter.logs, format+"\n", args...)
}

// runCleanups 执行所有注册的 cleanup 回调。
func (reporter *fakeFailureReporter) runCleanups() {
	for _, cleanup := range reporter.cleanups {
		// cleanup 按注册顺序执行，满足本测试的简单语义。
		cleanup()
	}
}
