# Benchmark 基线

本文记录当前纯 Go Lua 5.3 VM 的首个 runtime benchmark 基线，用于后续优化、回归和发布前对比。

## 执行环境

- 日期：2026-06-27
- 命令：`CGO_ENABLED=0 go test ./runtime -run=^$ -bench=. -benchtime=100ms`
- OS/Arch：`darwin/arm64`
- CPU：`Apple M1 Max`
- Go：项目 `go.mod` 声明 `go 1.26` 与 `toolchain go1.26.4`
- CGO：关闭

## 结果

```text
BenchmarkVMDispatch-10                         37104798      3.120 ns/op       0 B/op   0 allocs/op
BenchmarkTableReadWrite/raw_set_integer-10     23011464      5.130 ns/op       0 B/op   0 allocs/op
BenchmarkTableReadWrite/raw_get_integer-10     24501250      4.926 ns/op       0 B/op   0 allocs/op
BenchmarkGoFunctionCall-10                      2480064     49.25 ns/op      128 B/op   2 allocs/op
BenchmarkStringConcat-10                        3321932     35.90 ns/op       16 B/op   1 allocs/op
BenchmarkGoLuaCallback-10                        509391    268.5 ns/op       492 B/op   5 allocs/op
```

## 使用说明

- 该基线只覆盖 runtime 当前已有 benchmark，不代表完整 Lua 5.3 官方测试性能。
- 后续修改 VM dispatch、Table、字符串、Go/Lua 回调和 bridge 层时，应复跑同一命令并记录差异。
- 若硬件、Go toolchain、`benchtime` 或 CGO 设置变化，不能直接与本基线做精确比较。
