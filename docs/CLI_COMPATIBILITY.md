# glua/gluac 官方可执行文件兼容矩阵

本文定义 `glua`/`gluac` 对齐 Lua 5.3.6 官方 `lua`/`luac` 可执行文件的验收口径。核心原则是：官方短参数、调用顺序、stdin、REPL、错误输出和退出码必须优先兼容；项目扩展只能使用不冲突的长参数或显式扩展模式。

## 总体规则

- `glua` 对齐官方 `lua` 的调用方式，扩展能力不得改变 `-e`、`-i`、`-l`、`-v`、`-E`、`--`、`-` 的官方语义。
- `gluac` 对齐官方 `luac` 的调用方式，扩展能力不得改变 `-l`、`-o`、`-p`、`-s`、`-v`、`--`、`-` 的官方语义。
- `-v` 必须保持调用行为兼容，但输出内容应标明本项目产物，例如 `glua`/`gluac`、项目版本和 Lua 5.3.6 兼容说明，不伪装成官方 `lua`/`luac`。
- 对比验收以 Lua 5.3.6 官方工具为基线，至少比较 stdout、stderr、退出码、输出文件是否存在和 binary chunk 可加载性。

## lua CLI 参数矩阵

| 场景 | 官方调用 | `glua` 期望 | 验收方式 |
| --- | --- | --- | --- |
| 无参数，TTY | `lua` | 打印项目 banner 后进入 REPL | 伪终端交互测试 |
| 无参数，stdin 管道 | `printf 'print(1)' \| lua` | 执行 stdin chunk，不进入交互提示符 | stdout/stderr/exit code golden |
| 版本 | `lua -v` | 输出 `glua`/项目版本和 Lua 5.3.6 兼容说明，退出码为 0 | 版本输出语义检查 |
| 屏蔽环境变量 | `lua -E ...` | 不读取 Lua 环境变量，参数顺序保持官方语义 | 环境变量夹具 |
| 强制交互 | `lua -i script.lua` | 执行脚本后进入 REPL | 伪终端交互测试 |
| 执行片段 | `lua -e 'print(1)'` | 按传入顺序执行片段 | stdout/stderr/exit code golden |
| 多个片段 | `lua -e 'a=1' -e 'print(a)'` | 多个 `-e` 按出现顺序执行并共享全局环境 | stdout/stderr/exit code golden |
| 加载模块 | `lua -l mod` | 等价 `require("mod")`，不得解释成反汇编 | 模块路径夹具 |
| 停止解析 | `lua -- -x.lua` | `--` 后第一个参数作为脚本或脚本参数处理 | argv golden |
| stdin 脚本 | `lua -` | 从 stdin 读取脚本，chunk 名称对齐官方 | stdout/stderr/exit code golden |
| 脚本文件 | `lua script.lua a b` | 执行脚本，`arg` 表和退出码对齐官方 | argv golden |
| 参数顺序 | `lua -E -e '...' -l mod script.lua` | option 按官方顺序执行，脚本与参数边界对齐 | 组合夹具 |
| 错误参数 | `lua -Z` | stderr 和非 0 退出码对齐官方语义 | stderr/exit code golden |

## lua REPL 矩阵

| 场景 | 官方行为基线 | `glua` 期望 | 验收方式 |
| --- | --- | --- | --- |
| 启动 banner | 打印 Lua 版本、版权与提示 | 打印 `glua`/项目版本，并明确 Lua 5.3.6 兼容 | 伪终端输出检查 |
| 主提示符 | `> ` | 输出后提示符仍从列首开始，不因多余空格后移 | 伪终端输出检查 |
| 续行提示符 | `>> ` | 未完成语句进入续行提示符 | 多行输入夹具 |
| 表达式输入 | `=1+2` 输出 `3` | 支持表达式快捷输入 | REPL golden |
| 语句输入 | `print(1)` 输出 `1` | 支持普通 Lua 语句 | REPL golden |
| 多行补全 | `function f()` 后续行 | 编译完整 chunk 后执行 | REPL golden |
| 错误恢复 | 输入运行时错误后继续提示 | 打印错误并恢复下一次输入 | REPL golden |
| 光标左右 | 终端行编辑可在行内移动 | 支持左右移动和行内插入 | 伪终端按键测试 |
| Ctrl-C | 中断当前输入或执行 | 与官方语义对齐，非交互稳定返回非 0 | 信号测试 |
| EOF | Ctrl-D 或 stdin EOF 退出 | 正常退出交互循环 | 伪终端 EOF 测试 |
| `os.exit()` | 退出进程 | REPL 中立即退出并返回指定退出码 | 伪终端/进程测试 |
| 分行 local | `local a='hello'` 下一行不可见 | 错误文本和 traceback 对齐官方 | REPL golden |

## lua 错误输出矩阵

| 场景 | 示例 | 必须对齐项 |
| --- | --- | --- |
| 语法错误 | `lua -e 'if'` | chunk 名称、行号、错误类别、退出码 |
| 运行时错误 | `lua -e 'error("x")'` | stderr、traceback、退出码 |
| stdin chunk | `printf 'error("x")' \| lua -` | stdin chunk 名称 |
| 文件 chunk | `lua bad.lua` | 文件路径显示和行号 |
| `-e` chunk | `lua -e 'error("x")'` | `-e` chunk 名称 |
| `os.exit` | `lua -e 'os.exit(7)'` | 退出码和输出为空 |
| 主线程 yield | `lua -e 'coroutine.yield()'` | 错误类别和 traceback |

## luac 参数矩阵

| 场景 | 官方调用 | `gluac` 期望 | 验收方式 |
| --- | --- | --- | --- |
| 无参数 | `luac` | 按官方方式处理 stdin 或用法错误，以实测官方 5.3.6 为准 | stdout/stderr/exit code golden |
| 版本 | `luac -v` | 输出 `gluac`/项目版本和 Lua 5.3.6 兼容说明，退出码为 0 | 版本输出语义检查 |
| 反汇编 | `luac -l script.lua` | 输出反汇编，退出码对齐官方 | stdout/stderr/exit code golden |
| 详细反汇编 | `luac -l -l script.lua` | 输出详细反汇编 | stdout/stderr/exit code golden |
| 指定输出 | `luac -o out.luac script.lua` | 写出 binary chunk | 输出文件存在和可加载性 |
| 只检查 | `luac -p script.lua` | 只编译检查，不生成默认输出 | 输出文件检查 |
| 剥离调试 | `luac -s -o out.luac script.lua` | 输出 stripped chunk | binary chunk 可加载性 |
| 停止解析 | `luac -- -x.lua` | `--` 后文件名不再按 option 解析 | 文件名夹具 |
| stdin 输入 | `luac -` | 从 stdin 编译，chunk 名称对齐官方 | stdout/stderr/exit code golden |
| 单文件 | `luac script.lua` | 生成默认 `luac.out` | 输出文件存在和可加载性 |
| 多文件 | `luac a.lua b.lua` | 按官方组合多个 chunk 的语义处理 | 输出文件存在和可加载性 |
| 错误参数 | `luac -Z` | stderr 和非 0 退出码对齐官方语义 | stderr/exit code golden |

## 扩展参数命名空间

- 官方短参数不得复用为项目扩展。`glua -l` 永远是 `require`，不是反汇编。
- `glua` 项目扩展使用 `--glua-*` 命名空间，例如 `--glua-syntax`、`--glua-disable-syntax`、`--glua-list-bytecode`、`--glua-format`。
- `gluac` 项目扩展使用 `--gluac-*` 命名空间，例如 `--gluac-syntax`、`--gluac-disable-syntax`、`--gluac-opcode-trace`、`--gluac-step-trace`、`--gluac-minimal-disassembly`。
- `gluals` 项目扩展使用 `--gluals-*` 命名空间，例如 `--gluals-syntax`。
- 旧的无项目名前缀扩展参数不得作为成功路径保留，避免用户把项目扩展误认为官方 `lua`/`luac` 参数。
- `gluac` 的调试输出可以扩展项目命名空间长参数，但不能改变官方 `-l`、`-p`、`-s`、`-o` 语义。

## 自动验收

新增或修改 CLI 行为后至少执行：

```bash
CGO_ENABLED=0 go test ./internal/cli ./internal/luac
./scripts/compare-official-executables.sh
```

当本机官方工具名不是 `lua`/`luac` 时，显式指定：

```bash
LUA_BIN=/path/to/lua5.3 LUAC_BIN=/path/to/luac5.3 \
GLUA_BIN=./bin/glua GLUAC_BIN=./bin/gluac \
./scripts/compare-official-executables.sh
```

交互式 REPL 行编辑、Ctrl-C 和 EOF 必须使用伪终端或人工验收补充，不能只依赖普通 stdin 管道。
