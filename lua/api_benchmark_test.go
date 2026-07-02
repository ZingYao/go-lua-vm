package lua

import "testing"

// BenchmarkDoStringArithAddLoop 度量完整 Lua VM 路径下的整数累加 numeric for 热循环。
func BenchmarkDoStringArithAddLoop(b *testing.B) {
	source := `
local sum = 0
for i = 1, 100000 do
  sum = sum + i
end
return sum
	`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖源码编译、加载和执行的端到端路径。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}

// BenchmarkDoStringArithChainTemp 度量完整 Lua VM 路径下的左结合自二元链热循环。
func BenchmarkDoStringArithChainTemp(b *testing.B) {
	source := `
local sum = 0
for i = 1, 100000 do
  sum = sum + i * 3 - 7
end
return sum
	`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖源码编译、加载和执行的端到端路径。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}

// BenchmarkDoStringArithChainTempOfficial 度量官方完整 benchmark 同规模的左结合自二元链热循环。
func BenchmarkDoStringArithChainTempOfficial(b *testing.B) {
	source := `
local sum = 0
for i = 1, 1000000 do
  sum = sum + i * 3 - 7
end
return sum
	`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖源码编译、加载和执行的端到端路径，并对齐官方脚本循环规模。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}

// BenchmarkDoStringTableReadWrite 度量完整 Lua VM 路径下的连续整数 table 写入和读取。
func BenchmarkDoStringTableReadWrite(b *testing.B) {
	source := `
local t = {}
for i = 1, 20000 do
  t[i] = i
end
local sum = 0
for i = 1, 20000 do
  sum = sum + t[i]
end
return sum
	`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖源码编译、加载和执行的端到端路径。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}

// BenchmarkDoStringStringConcat 度量完整 Lua VM 路径下的循环字符串拼接。
func BenchmarkDoStringStringConcat(b *testing.B) {
	source := `
local s = ""
for i = 1, 2000 do
  s = s .. "x"
end
return #s
`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖源码编译、加载和执行的端到端路径。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}

// BenchmarkDoStringStdlibMathString 度量标准库 math/string 混合调用热路径。
func BenchmarkDoStringStdlibMathString(b *testing.B) {
	source := `
local sum = 0
for i = 1, 80000 do
  sum = sum + math.floor(math.sqrt(i)) + #string.format('%d', i)
end
return sum
`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖标准库注册、源码编译、加载和执行的端到端路径。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}

// BenchmarkDoStringFunctionCall 度量完整 Lua VM 路径下的 Lua 函数调用循环。
func BenchmarkDoStringFunctionCall(b *testing.B) {
	source := `
local function add(a, b)
  return a + b
end
local sum = 0
for i = 1, 5000 do
  sum = sum + add(i, 1)
end
return sum
`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖源码编译、加载和执行的端到端路径。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}

// BenchmarkDoStringRecursion 度量完整 Lua VM 路径下的递归 Lua closure 调用。
func BenchmarkDoStringRecursion(b *testing.B) {
	source := `
local function fib(n)
  if n < 2 then return n end
  return fib(n - 1) + fib(n - 2)
end
local sum = 0
for i = 1, 16 do
  sum = sum + fib(15)
end
return sum
`
	b.ReportAllocs()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建独立 State，覆盖源码编译、加载和执行的端到端路径。
		state := NewState()
		if err := OpenLibs(state); err != nil {
			state.Close()
			b.Fatalf("OpenLibs failed: %v", err)
		}
		if err := DoString(state, source); err != nil {
			state.Close()
			b.Fatalf("DoString failed: %v", err)
		}
		state.Close()
	}
}
