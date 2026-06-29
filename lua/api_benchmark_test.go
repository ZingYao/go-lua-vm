package lua

import "testing"

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
