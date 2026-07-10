# 效率对比

GLua 的性能目标是在保留 Lua 5.3.6 语义、Debug、coroutine、traceback 和错误位置的前提下，降低编译器、VM、标准库与 CLI 的执行成本。

## 最终收敛结果

仓库基线 `c322b5d` 的默认完整 Benchmark 使用同一台机器上的官方 Lua 5.3.6 与纯 Go `glua/gluac` 各运行三轮并取中位数。倍率为“GLua / 官方 Lua”，低于 `1.00x` 表示 GLua 更快。

<div class="glua-metrics">
  <div class="glua-metric"><strong>低于官方</strong><span class="value">9 / 10</span>默认完整测试中九个用例低于 1.00x。</div>
  <div class="glua-metric"><strong>最佳倍率</strong><span class="value">0.572x</span>标准库数学与字符串组合用例。</div>
  <div class="glua-metric"><strong>整数循环</strong><span class="value">0.643x</span>整数累加循环用例。</div>
  <div class="glua-metric"><strong>剩余项</strong><span class="value">1.054x</span>递归保持完整调用帧语义后的倍率。</div>
</div>

| 用例 | 官方 Lua 5.3.6 | GLua | GLua / 官方 |
| --- | ---: | ---: | ---: |
| 整数累加循环 | 0.007571s | 0.004865s | 0.643x |
| 混合算术循环 | 0.011024s | 0.009175s | 0.832x |
| 表读写 | 0.006929s | 0.006035s | 0.871x |
| 函数调用 | 0.006610s | 0.005256s | 0.795x |
| 字符串拼接 | 0.004590s | 0.003778s | 0.823x |
| 闭包 upvalue | 0.007892s | 0.006725s | 0.852x |
| 数学与字符串标准库 | 0.018876s | 0.010797s | 0.572x |
| 递归 | 0.003505s | 0.003695s | 1.054x |
| 编译 3000 个函数 | 0.005015s | 0.003879s | 0.773x |

完整十项数据、原始口径和优化边界见 [Benchmark 最终结果](BENCHMARK.md) 与 [性能收敛报告](PERFORMANCE_CLOSURE_REPORT.md)。

## 多平台复核

2026-07-08 的平台复核使用各平台独立同机比较，不能把不同机器的绝对耗时横向混合：

| 平台 | 结论摘要 |
| --- | --- |
| macOS arm64 | 十项中七项低于官方，范围约 0.65x 到 1.11x。 |
| Linux arm64 | 十项全部低于官方，范围约 0.34x 到 0.88x。 |
| Android arm64 | 十项全部低于官方，范围约 0.56x 到 0.90x。 |
| Windows 11 amd64 | CLI 冷启动会放大 Go runtime 固定成本；进程内摊销后多数运行用例约 0.71x 到 1.26x。 |

Windows 的短进程冷启动与 VM 热路径是两类指标。判断运行时退化时，应优先使用进程内 Benchmark 或 Go Benchmark，而不是把每次重新启动 CLI 的耗时直接归因到 VM。

## 复现

准备官方 Lua 5.3.6 的 `lua` 和 `luac`，然后执行：

~~~bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac

LUA_BIN=/path/to/lua-5.3.6 \
LUAC_BIN=/path/to/luac-5.3.6 \
GLUA_BIN=./bin/glua \
GLUAC_BIN=./bin/gluac \
./scripts/benchmark-official.sh
~~~

Windows 进程内摊销测试：

~~~bash
LUA_BIN=/path/to/lua-5.3.6 \
GLUA_BIN=./bin/glua \
./scripts/benchmark-official-amortized.sh
~~~

## 解读原则

- 只比较同一机器、同一系统、同一电源模式下的官方 Lua 和 GLua。
- 先运行兼容门禁，再采集性能；不能用语义缺失换取数字。
- Wall-clock 结果会受调度器、杀毒软件、文件缓存和温控影响。
- Native 模块构建与默认纯 Go Benchmark 是不同能力，现有主表使用 `CGO_ENABLED=0`。
