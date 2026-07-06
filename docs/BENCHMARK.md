# Benchmark 最终结果

本文只保留当前 `glua` / `gluac` 对官方 Lua 5.3.6 的最终 benchmark 结果。完整优化过程、每个用例如何拉近差距、与官方 Lua C 实现不同的地方，以及 guard 测试索引见 `docs/PERFORMANCE_CLOSURE_REPORT.md`。

倍率语义：`本项目/官方 Lua 5.3.6`。低于 `1.00x` 表示本项目快于官方，高于 `1.00x` 表示本项目仍慢于官方。

## 数据口径

- 最终代码基线：`c322b5d`
- 对比脚本：`scripts/benchmark-official.sh`
- 官方工具：Lua 5.3.6 `lua` / `luac`
- 本项目工具：`./bin/glua` / `./bin/gluac`
- 构建要求：`CGO_ENABLED=0`
- 统计方式：默认完整 benchmark 三轮，取三轮中位数。

## 最终对比表

| English key | 中文名称 | 官方三轮中位数 | 本项目三轮中位数 | 本项目/官方 | 状态 |
| --- | --- | ---: | ---: | ---: | --- |
| `recursion` | 递归 | 0.003505s | 0.003695s | 1.054x | 剩余项，语义门禁证伪 |
| `table_rw` | 表读写 | 0.006929s | 0.006035s | 0.871x | 低于 1.00x |
| `closure_upvalue` | 闭包 upvalue | 0.007892s | 0.006725s | 0.852x | 低于 1.00x |
| `arith_mix_loop` | 混合算术循环 | 0.011024s | 0.009175s | 0.832x | 低于 1.00x |
| `string_concat` | 字符串拼接 | 0.004590s | 0.003778s | 0.823x | 低于 1.00x |
| `function_call` | 函数调用 | 0.006610s | 0.005256s | 0.795x | 低于 1.00x |
| `compile_3000_functions` | 编译3000个函数 | 0.005015s | 0.003879s | 0.773x | 低于 1.00x |
| `arith_chain_temp` | 算术临时链 | 0.012395s | 0.009627s | 0.777x | 低于 1.00x |
| `arith_add_loop` | 整数累加循环 | 0.007571s | 0.004865s | 0.643x | 低于 1.00x |
| `stdlib_math_string` | 标准库数学与字符串 | 0.018876s | 0.010797s | 0.572x | 低于 1.00x |

最终只有 `recursion` 高于 `1.00x`。该项已经通过 profile 证明 prepared 路径为 `0 B/op`、`0 allocs/op`，剩余差距来自执行框架、VM step 和自递归 fast path 的固定 CPU；继续压缩需要整段递归折叠或调用帧绕过，会破坏 Debug、coroutine/yield、traceback、错误 PC 和调用帧可见性，因此按语义门禁不进入生产实现。

## 复现命令

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
GLUA_BIN=./bin/glua \
GLUAC_BIN=./bin/gluac \
./scripts/benchmark-official.sh
```

## 兼容门禁

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
./scripts/compare-cli-golden.sh
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
./scripts/compare-official-executables.sh
LUA_BIN=/Users/zing/.local/lua/5.3.6/bin/lua \
LUAC_BIN=/Users/zing/.local/lua/5.3.6/bin/luac \
./scripts/run-official-tests.sh
```
