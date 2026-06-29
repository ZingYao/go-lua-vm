# Luac 兼容工具设计

本文记录本项目对 Lua 5.3 `luac` 能力的实现目标。该能力必须使用纯 Go 实现，不调用官方 C 版 `luac`，也不通过 CGO 接入 Lua C API。

## 交付形态

首选交付两个入口：

- `glua`：对齐官方 `lua`，负责执行脚本、REPL、`-e`、`-l`、`-i`、`-v` 等运行入口。
- `gluac`：对齐官方 `luac`，负责源码编译、binary chunk 输出、反汇编和 debug dump。

为了方便调试，`glua` 可以提供内部调试参数查看 chunk 反汇编，但必须避开官方 `lua -l <module>` 语义。`glua -l` 永远按官方 `lua` 的 `require` 入口处理，反汇编类扩展只能使用 `--glua-list-bytecode` 等项目命名空间长参数。

## 首版范围

`gluac` 首版目标：

- 支持读取 Lua 源码文件并编译为 Lua 5.3 Proto。
- 支持输出 Lua 5.3 binary chunk。
- 支持读取 binary chunk 并反汇编。
- 支持输出 Proto debug dump，展示常量表、upvalue、line info 和 local var。
- 支持 `-o <file>` 指定输出文件。
- 支持 `-p` 只解析/编译检查，不输出 chunk。
- 支持 `-s` 去除 debug 信息。
- 支持 `-l` 与 `-l -l` 反汇编入口。
- 支持 `--` 停止参数解析。
- 支持单文件和多文件输入；多文件会组合为共享 `_ENV` 的 wrapper chunk。
- 支持默认输出 `luac.out`。
- 支持项目调试扩展参数 `--gluac-syntax`、`--gluac-disable-syntax`、`--gluac-opcode-trace`、`--gluac-step-trace`、`--gluac-minimal-disassembly`。

暂不承诺：

- 与官方 `luac` 所有文本输出逐字符一致。
- 跨架构 binary chunk 完全可互换。
- 兼容 Lua 5.4 或 LuaJIT chunk。
- 默认纯 Go 构建中直接加载普通 Lua C 模块。
- 无项目前缀的扩展参数；`--syntax`、`--opcode-trace` 等旧形式必须迁移到 `--gluac-*`。

## 依赖顺序

`gluac` 可用性依赖以下模块完成：

1. `bytecode`：opcode、Proto、chunk load/dump、反汇编。
2. `compiler/lexer`：Lua 5.3 词法分析。
3. `compiler/parser`：Lua 5.3 语法分析和 AST。
4. `compiler/codegen`：AST 到 Proto 的指令生成。
5. `internal/luac`：参数解析、文件输入输出、错误打印与退出码。

当前 `bytecode` 底座已完成 chunk load/dump、roundtrip 和反汇编，后续 `gluac` 重点依赖 compiler 三层实现。

## 参数兼容策略

`gluac` 参数应优先对齐 Lua 5.3 `luac`：

- `-l`：列出反汇编。
- `-l -l`：输出更详细反汇编。
- `-o <file>`：写入输出文件。
- `-p`：只解析/编译检查。
- `-s`：剥离 debug 信息。
- `-v`：输出版本。

遇到未支持参数时，应返回明确错误和非 0 退出码，不静默忽略。

完整官方可执行文件兼容矩阵见 `docs/CLI_COMPATIBILITY.md`。其中 `gluac` 的 release 阻塞项包括：无参数、`-v`、`-l`、`-l -l`、`-o`、`-p`、`-s`、`--`、`-`、单文件、多文件、错误参数和默认输出 `luac.out` 的 stdout、stderr、退出码与输出文件行为。

## 扩展参数

`gluac` 扩展能力必须使用 `--gluac-*` 命名空间：

- `--gluac-syntax <mode>` 或 `--gluac-syntax=<mode>`：选择源码编译语法集合。
- `--gluac-disable-syntax <names>` 或 `--gluac-disable-syntax=<names>`：从最终语法集合中关闭指定扩展。
- `--gluac-opcode-trace`：输出静态 opcode trace。
- `--gluac-step-trace`：输出 VM step trace 预览。
- `--gluac-minimal-disassembly`：输出测试失败定位用的最小反汇编。

无项目前缀的旧扩展参数不属于兼容接口；官方 `luac` 参数空间只保留给 Lua 5.3.6 行为。

## 测试策略

- 使用手写 Proto 覆盖 binary chunk dump/load roundtrip。
- 使用 Lua 源码 golden 覆盖 parser/codegen 后的反汇编输出。
- 使用官方 Lua 5.3 `luac` 输出作为行为参考，但不把 C 工具接入构建链路。
- CLI 层覆盖 stdout、stderr、退出码和输出文件内容。
