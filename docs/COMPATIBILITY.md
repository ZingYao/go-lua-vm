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
- `lua` CLI 常用参数：`-e`、`-i`、`-l`、`-v` 和脚本参数。
- `luac` 常用参数：`-l`、`-o`、`-p`、`-s`、`-v`。

## 允许差异

- Go map 无序遍历不会直接暴露；本项目测试可使用稳定顺序，但不承诺与 C Lua hash 内部顺序一致。
- 第一阶段不追求与 C Lua 性能一致，优先保证可读性和正确性。
- binary chunk 首版只承诺本项目 load/dump roundtrip 和 Lua 5.3 语义字段一致，跨端序、跨字长完全互通需单独验收。
- 错误文本首版不要求逐字符匹配官方 Lua，但错误类别、行号和 traceback 必须可对齐。
- `io`、`os`、`package` 标准库在宿主权限、路径和平台差异上允许有 Go 运行时约束。
- `package.loadlib` 默认不内置动态 C 库打开逻辑；无 CGO 约束下未注入 loader 时返回明确不支持。宿主可通过 `lua.Options.PackageDynamicLibraryLoader`、`stdlib/package` 环境注入或覆盖 `package.loadlib` 接入自己的动态库加载层。
- 默认跨平台构建不依赖 C 头文件、系统动态库或 Lua C API 开发包；`require` 不直接加载普通 Lua C 模块。普通 Lua C 模块需要 `lua_State*` 和 Lua C ABI 兼容层，该能力不属于首版默认纯 Go 构建承诺。

## 验收方式

- Go 单测覆盖每个 runtime、bytecode、compiler 和 stdlib 单元。
- Lua golden 脚本覆盖 stdout、stderr 和退出码。
- 官方 Lua 5.3 测试套件作为兼容验收来源。
- `glua` 与官方 `lua` 对同一脚本输出做差异对比。
- `gluac` 与官方 `luac` 对关键参数行为做差异对比。
- binary chunk 使用 roundtrip、反汇编和 Proto 字段一致性测试。

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
