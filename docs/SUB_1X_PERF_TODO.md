# glua 全面低于 1.0x 性能 TODO

## 分支与目标

- 目标分支：`quanquan/feature/glua-sub-1x-perf`
- 起点 commit：`fd2edb4`
- 目标：默认官方 Lua 5.3.6 对比 benchmark 中所有用例稳定低于 `1.00x`。
- 倍率语义：`本项目/官方 Lua 5.3.6`，低于 `1.00x` 表示本项目更快。

## 0. 基线

- [x] 从 `master` 创建 `quanquan/feature/glua-sub-1x-perf`。
- [x] 重建 `bin/glua` 和 `bin/gluac`。
- [x] 确认官方 `lua` / `luac` 为 5.3.6。
- [x] 运行默认完整 benchmark 单轮，记录中英文用例名和初始倍率。
- [ ] 运行默认完整 benchmark 三轮，确认哪些 `> 1.00x` 是稳定差距。

初始单轮基线：

| English case | 中文名称 | 初始倍率 | 当前优先级 |
| --- | --- | ---: | --- |
| `compile_3000_functions` | 编译3000个函数 | 1.24x | P0 |
| `recursion` | 递归 | 1.08x | P1 |
| `string_concat` | 字符串拼接 | 1.05x | P1 |
| `function_call` | 函数调用 | 1.04x | P1 |
| `arith_mix_loop` | 混合算术循环 | 1.01x | P2 |
| `table_rw` | 表读写 | 0.90x | 观察 |
| `closure_upvalue` | 闭包 upvalue | 0.88x | 观察 |
| `arith_chain_temp` | 算术临时链 | 0.77x | 观察 |
| `arith_add_loop` | 整数累加循环 | 0.65x | 观察 |
| `stdlib_math_string` | 标准库数学与字符串 | 0.59x | 观察 |

## 1. `compile_3000_functions` / 编译3000个函数

- [ ] 跑三轮完整 benchmark，确认该项稳定 `> 1.00x`。
- [ ] 跑 `BenchmarkCompileSource3000Functions` 五轮和 CPU/memory profile。
- [ ] 设计 compile-only streaming 简单函数声明路径，明确普通 parser、semantic、debug、`luac -l -l`、
  错误位置和非目标回退边界。
- [ ] 先补 guard 测试：普通 parser 仍返回完整 AST、target Proto/list 与普通路径一致、非目标形态回退、
  语法错误位置一致。
- [ ] 实现最小 prototype。
- [ ] 五轮 micro wall-clock 稳定下降至少 `5%`，且 B/op 不高于当前约 `3.50 MB`。
- [ ] 重建 CLI 并跑完整 benchmark 三轮；未稳定低于 `1.00x` 时必须记录原因或回退。

## 2. `recursion` / 递归

- [ ] 仅在完整 benchmark 三轮稳定高于 `1.00x` 且接近或超过 `1.08x` 时进入。
- [ ] 跑 `BenchmarkPreparedRecursion` 五轮和 CPU/memory profile。
- [ ] 补 guard：闭包返回、闭包传参、闭包存表、闭包身份比较、`debug.getupvalue`、`debug.setupvalue`、
  `debug.upvalueid`、`debug.upvaluejoin`、错误 traceback、line/call hook、pcall/error、coroutine/yield。
- [ ] 实现非逃逸 local function descriptor prototype。
- [ ] prepared 路径去掉当前 `2 allocs/op`，并且 wall-clock 稳定下降。
- [ ] 完整 benchmark 三轮稳定低于 `1.00x`，否则回退或记录证伪。

## 3. `string_concat` / 字符串拼接

- [ ] 跑官方 8000 次拼接 fixture 的 DoString/prepared micro 和 profile。
- [ ] 复核 `__concat` 元方法、debug 可见性、yield、错误路径和 materialize 边界。
- [ ] 只有语义可证明时，设计更窄 builder/materialize 切口。
- [ ] 完整 benchmark 三轮稳定低于 `1.00x`，否则不保留生产改动。

## 4. `function_call` / 函数调用

- [ ] 完整 benchmark 三轮稳定 `> 1.00x` 后进入。
- [ ] 跑 `BenchmarkPreparedFunctionCallOfficial` 五轮和 CPU profile。
- [ ] 评估 leaf add-return batch guard 冻结：闭包寄存器、函数 Proto、参数寄存器、结果寄存器。
- [ ] 补 guard：函数值替换、upvalue/env 变化、hook 打开、yield/continuation、错误 PC、traceback、context 取消。
- [ ] 完整 benchmark 三轮稳定低于 `1.00x`，否则回退或记录证伪。

## 5. `arith_mix_loop` / 混合算术循环

- [ ] 仅做完整 benchmark 三轮复核。
- [ ] 只有稳定 `> 1.00x` 且 profile 指向新的单一主因时才重新打开。

## 6. 全局验证

每个生产 Go 改动必须执行：

```bash
gopls check <changed-go-files>
gofmt -w <changed-go-files>
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

涉及 CLI、bytecode、VM、stdlib、compiler 或官方兼容行为时还必须执行：

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
./scripts/compare-cli-golden.sh
./scripts/compare-official-executables.sh
./scripts/run-official-tests.sh
```

## 7. 每轮报告格式

每轮完成后必须报告：

- 优化点或证伪点。
- 语义依据。
- benchmark 变化。
- 优化程度：从 `X.x` 到 `Y.x`、下降百分比，或未改善原因。
- 测试结果。
- commit hash。

最终完成后必须输出整体 benchmark 对比表，包含英文用例名、中文名称、官方中位数、本项目中位数、
本项目/官方倍率、相对初始基线改善幅度、是否低于 `1.00x`。
