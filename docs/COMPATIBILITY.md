# Lua 5.3 兼容性说明

本文记录本项目与 Lua 5.3 官方行为的兼容目标、已知差异和验收方式。迁移基线锁定为 `lua-5.3.6`，源码参考副本位于 `third_party/lua-5.3.6/`。

## 基线

- Lua 版本：`lua-5.3.6`。
- 源码形态：Lua 官方 C 源码。
- Go 实现：纯 Go，无 CGO，不接入 Lua C API。
- CLI 主产物：`glua`。
- 字节码工具产物：`gluac`。

用户早期提到的 “Cpp 源码” 在本项目中按 Lua 官方 C 源码理解。若后续提供独立 C++ 仓库，应新增迁移映射文档，不覆盖当前官方源码基线。

## 必须兼容

- Lua 5.3 基础语法、词法和表达式优先级。
- Lua 5.3 opcode 编号、编码格式和 RK 语义。
- Lua 5.3 值模型：nil、boolean、integer、number、string、table、function、userdata、thread。
- Lua 5.3 table 基础行为、元方法、长度和迭代语义。
- Lua 5.3 函数调用、尾调用、闭包、upvalue、vararg、协程和错误恢复。
- Lua 5.3 标准库：base、coroutine、table、string、utf8、math、io、os、package、debug。
- Lua 5.3 Debug API：hook、traceback、局部变量、upvalue 和 registry。
- `lua` CLI 参数与调用方式：无参数、`-v`、`-E`、`-i`、`-e stat`、`-l mod`、`--`、`-`、脚本文件、脚本参数、stdin 管道、组合顺序和错误参数。
- `lua` REPL 行为：启动 banner、主提示符、续行提示符、表达式输入、语句输入、多行补全、错误恢复、行编辑、Ctrl-C、EOF、`os.exit()` 和分行 local 语义。
- `lua` CLI 错误输出：语法错误、运行时错误、traceback、`os.exit`、主线程 `coroutine.yield`、脚本文件路径、stdin chunk name 和 `-e` chunk name。
- `luac` 参数与调用方式：无参数、`-v`、`-l`、`-l -l`、`-o name`、`-p`、`-s`、`--`、`-`、单文件、多文件、错误参数和默认输出 `luac.out`。

## 官方可执行文件兼容状态

当前 `glua`/`gluac` 的发布口径如下：

- `glua` 已按官方 `lua` 语义覆盖 `-e`、`-i`、`-l`、`-v`、`-E`、`--`、`-`、脚本参数、stdin、REPL、错误输出、`os.exit()` 和 Ctrl-C 相关路径。
- `glua -l` 永远按官方 `lua` 的 `require` 语义处理，不作为反汇编入口。
- `gluac` 已按官方 `luac` 语义覆盖 `-l`、`-l -l`、`-o`、`-p`、`-s`、`-v`、`--`、单文件、多文件和默认 `luac.out` 输出。
- `gluac` 多输入文件会生成顶层 wrapper chunk，按输入顺序依次创建并调用每个子 chunk，且共享 `_ENV`。
- 项目扩展参数必须使用项目命名空间：`glua` 使用 `--glua-*`，`gluac` 使用 `--gluac-*`，`gluals` 使用 `--gluals-*`。
- 旧的无项目前缀扩展参数，例如 `--syntax`、`--list-bytecode`、`--format`、`--opcode-trace`，不作为成功路径保留。

官方可执行文件兼容矩阵与 release 阻塞验收清单见 `docs/CLI_COMPATIBILITY.md`。

## 允许差异

- Go map 无序遍历不会直接暴露；本项目测试可使用稳定顺序，但不承诺与 C Lua hash 内部顺序一致。
- 第一阶段不追求与 C Lua 性能一致，优先保证可读性和正确性。
- binary chunk 首版只承诺本项目 load/dump roundtrip 和 Lua 5.3 语义字段一致，跨端序、跨字长完全互通需单独验收。
- CLI 入口的错误文本以官方可执行文件 golden 和自动对比脚本为准；Go API 的结构化错误会保留更丰富的列号、源码片段或 cause 信息，不要求与官方 CLI 逐字符一致。
- `io`、`os`、`package` 标准库在宿主权限、路径和平台差异上允许有 Go 运行时约束。
- `package.loadlib` 默认不内置动态 C 库打开逻辑；无 CGO 约束下未注入 loader 时返回明确不支持。宿主可通过 `lua.Options.PackageDynamicLibraryLoader`、`stdlib/package` 环境注入或覆盖 `package.loadlib` 接入自己的动态库加载层。
- 默认跨平台构建不依赖 C 头文件、系统动态库或 Lua C API 开发包；`require` 不直接加载普通 Lua C 模块。普通 Lua C 模块需要 `lua_State*` 和 Lua C ABI 兼容层，该能力不属于首版默认纯 Go 构建承诺。

## Release 阻塞项

首个 release 前，以下检查必须全部通过，否则不能宣称 `glua`/`gluac` 官方可执行文件兼容完成：

- `CGO_ENABLED=0 go test ./...`
- `./scripts/check-go-gates.sh`
- `git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'` 无未跟踪 Go 文件。
- `./scripts/compare-official-executables.sh` 对比官方 `lua`/`luac` 与本项目 `glua`/`gluac` 的 stdout、stderr、退出码、输出文件和 chunk 可加载性。
- REPL 行编辑、Ctrl-C、EOF 和 `os.exit()` 使用伪终端或等价交互测试验收。
- 若本机官方工具名不是 `lua`/`luac`，必须显式设置 `LUA_BIN`、`LUAC_BIN`、`GLUA_BIN`、`GLUAC_BIN` 后再运行对比脚本。

## 验收方式

- Go 单测覆盖每个 runtime、bytecode、compiler 和 stdlib 单元。
- Lua golden 脚本覆盖 stdout、stderr 和退出码。
- 官方 Lua 5.3 测试套件作为兼容验收来源。
- `glua` 与官方 `lua` 对同一脚本输出做差异对比。
- `gluac` 与官方 `luac` 对关键参数行为做差异对比。
- binary chunk 使用 roundtrip、反汇编和 Proto 字段一致性测试。
- `glua`/`gluac` 官方可执行文件参数、REPL、错误输出和扩展参数边界以 `docs/CLI_COMPATIBILITY.md` 为 release 阻塞清单。
- 新增或修改 CLI 行为后执行 `scripts/compare-official-executables.sh`，对比官方 `lua`/`luac` 与本项目 `glua`/`gluac` 的 stdout、stderr、退出码、输出文件和 chunk 可加载性。

## 差异记录模板

新增差异必须按以下格式追加：

```text
### 差异标题

- 状态：临时 / 长期 / 已修复
- 官方行为：
- 当前行为：
- 影响范围：
- 回收计划：
```

当前无已确认长期差异。
