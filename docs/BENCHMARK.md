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

## 官方 Lua 5.3.6 CLI 对比

### 执行环境

- 日期：2026-06-29
- OS/Arch：`darwin/arm64`
- macOS：`26.5.1`
- CPU：`Apple M1 Max`
- Go：`go version go1.26.4 darwin/arm64`
- CGO：本项目 `glua` / `gluac` 构建时关闭，命令为 `CGO_ENABLED=0 go build -o bin/glua ./cmd/glua` 与 `CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac`
- 官方 Lua：从 `https://www.lua.org/ftp/lua-5.3.6.tar.gz` 下载完整发布包到 `/tmp`，SHA256 为 `fc5fd69bb8736323f026672b1b7235da613d7177e72558893a0bdcd320466d60`
- 官方 Lua 构建：`make macosx MYCFLAGS='-DLUA_COMPAT_5_2'`
- 说明：仓库内 `third_party/lua-5.3.6/` 当前参考副本缺少 `luac.c`，因此 `luac` 对比使用官方完整发布包构建产物。

### 方法

- 每个用例先各自 warmup 一次，再交替执行官方工具与本项目工具各 5 次。
- 统计 wall-clock elapsed time 的中位数。
- `lua` 对比执行同一份临时 Lua 脚本，并校验 stdout 一致。
- `luac` 对比编译同一份 2500 个全局函数定义的 Lua 源码，并校验两侧均成功写出 chunk。

### 结果

| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_loop` | 0.036815s | 0.923068s | 25.07x |
| `table_rw` | 0.014469s | 0.332654s | 22.99x |
| `function_call` | 0.027137s | 0.965794s | 35.59x |
| `string_concat` | 0.011507s | 1.064175s | 92.48x |
| `compile_2500_global_functions` | 0.007467s | 0.019272s | 2.58x |

### 结论

- 当前 `glua` 已以兼容性验收为第一目标，解释执行性能明显慢于官方 C Lua；算术循环、表读写和 Lua 函数调用约慢 23x 到 36x。
- 字符串连续拼接差距最大，约慢 92x，后续优化应优先检查 `CONCAT` 指令、字符串分配、短字符串驻留和 Lua 字符串 builder 路径。
- `gluac` 编译速度与官方 `luac` 的差距相对较小，当前临时源码编译约慢 2.6x，说明 lexer/parser/codegen 的首轮性能风险低于 VM 执行热路径。
- 该结果是单机、短脚本、wall-clock 基准，不作为发布性能承诺；后续优化需要补充更稳定的 benchmark harness，并分别跟踪 VM 指令分发、表、字符串、函数调用和 binary chunk 编解码。
