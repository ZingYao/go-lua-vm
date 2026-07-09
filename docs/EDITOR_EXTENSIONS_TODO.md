# GLua 编辑器扩展优化 TODO

本文跟踪 VS Code 与 JetBrains 两个 GLua 编辑器扩展的优化任务。每轮自动推进必须先读取 `docs/EDITOR_EXTENSIONS_PLAN.md` 和本文，并且只做一个可验证小切口。

## 当前状态

- 分支：`codex/editor-extension-followups`
- 当前重点：共享 golden cases 与 module/require 工作流。
- 已知环境提醒：JetBrains CLI Gradle 测试已可运行；IDE 同步若选择 Java 26 作为 Gradle JVM，仍可能与 Gradle 9.0.0 不兼容，建议 IDE Gradle JVM 使用 JDK 21。
- 当前工作区继承了上一轮扩展修复改动，需在提交前统一复核 diff。

## 总目标

- [x] VS Code 与 JetBrains 扩展核心诊断行为由共享 golden 覆盖。
- [x] 两个扩展支持常见 Lua 单文件模块导出场景：非 local 模块表赋值、后续成员赋值、顶层 `return`。
- [x] 两个扩展对 `require(...)` 的文件解析、跳转和诊断有一致策略。
- [x] 两个扩展的发布包可重复构建，关键打包 warning 有明确处理策略。
- [x] 顶层脚本可以一键跑编辑器扩展门禁。

## 第一阶段：共享 diagnostics golden

- [x] 新增共享 diagnostics golden 文件：`tests/editor/golden-diagnostics.json`。
- [x] VS Code 测试通过 LSP stdio 跑共享 diagnostics golden。
- [x] JetBrains 新增 JUnit golden 测试入口。
- [x] 覆盖 `M = {}; return M` 不误报。
- [x] 覆盖 `value = ...; return value` 不误报。
- [x] 覆盖 `local cjson = require("cjson"); return cjson` 不误报。
- [x] 覆盖 table constructor 字段不会误声明为全局。
- [x] 执行 JetBrains `./gradlew --no-daemon --no-configuration-cache test`。
- [x] 把 diagnostics golden 接入一个顶层脚本，避免手动分别进入两个扩展目录。

## 第二阶段：require/module 工作流

- [x] 设计 `require("foo.bar")` 到文件候选的规则，明确 `.lua`、`init.lua`、工作区根和 `package.path` 的优先级。
- [x] VS Code 支持 `require(...)` 跳转到 Lua 文件。
- [x] JetBrains 支持 `require(...)` 跳转到 Lua 文件。
- [x] 共享 golden 覆盖存在模块、缺失模块和 native C 模块三类路径。
- [x] 对 `package.cpath` 命中的 native 模块只给提示，不强制要求 Lua 源文件存在。

## 第三阶段：作用域和符号表

- [x] 评估轻量 AST 或 block scope 模型，给出实现边界。
- [x] 区分 local/global、function 参数、for 变量和 upvalue。
- [x] 修正 table field、method definition、index assignment 与普通变量声明的边界。
- [x] completion、definition 和 diagnostics 复用同一份符号信息。
- [x] 为顶层 `return` 与模块导出添加更多 golden。

## 第三阶段 A：智能补全与冒号方法导出

- [x] 设计并落地共享 completion golden，覆盖 numeric for、generic for、当前文件符号和 require 模块成员。
- [x] VS Code：补全 `for i = x, x do` 循环体内的 `i`。
- [x] VS Code：补全 `for a, d in pairs(xxx) do` 循环体内的 `a`、`d`。
- [x] JetBrains：补全 numeric for 循环变量。
- [x] JetBrains：补全 generic for 循环变量。
- [x] 第一轮 shared completion golden 覆盖当前文件符号、numeric for、generic for 和函数参数；require 模块成员 golden 后续单独补。
- [x] VS Code：实现 `moduleExportSnapshot(filePath)`，统一导出成员扫描。
- [x] JetBrains：实现对齐的 `ModuleExportSnapshot` / `ExportedMember` 查询入口。
- [x] VS Code：`module.` 补全目标模块的点成员、table literal 函数字段和 bracket 函数字段。
- [x] JetBrains：`module.` 补全目标模块的点成员、table literal 函数字段和 bracket 函数字段。
- [x] VS Code：`module:` 优先补全 `function M:foo()` 冒号方法。
- [x] JetBrains：`module:` 优先补全 `function M:foo()` 冒号方法。
- [x] VS Code：`tools:ccc()` 跳转到 `function M:ccc()`。
- [x] JetBrains：`tools:ccc()` 跳转到 `function M:ccc()`。
- [x] VS Code：`tools.ccc()` 允许跳转到 `function M:ccc()`。
- [x] JetBrains：`tools.ccc()` 允许跳转到 `function M:ccc()`。
- [x] VS Code：hover/completion detail 展示导出成员签名和定义前注释。
- [x] JetBrains：quick documentation/completion tail text 展示导出成员签名和定义前注释。
- [x] 增加 guard case：非返回 table 的成员不跨文件暴露。
- [x] 增加 guard case：`{ ccc = true }` 不作为函数成员补全。
- [x] 执行 VS Code 验证：`npm test && npm run build`。
- [x] 执行 JetBrains 验证：`./gradlew test buildPlugin`。
- [x] 第二轮 shared completion golden 覆盖 require 模块点成员、冒号成员、table literal 函数字段和 bracket 函数字段；VS Code 已消费这些用例，JetBrains 模块成员补全后续接入。
- [x] VS Code：先落地 `moduleExportMembers(...)` 统一扫描入口，供 completion 复用；正式 `ModuleExportSnapshot` 对象化留待后续收敛。
- [x] 第三轮 JetBrains 接入 shared completion golden 的 module-member 用例，并通过 `ExportedMember` 列表入口驱动 `module.` / `module:` 补全；正式 `ModuleExportSnapshot` 对象化留待后续收敛。
- [x] 第四轮 shared require-definition golden 覆盖 `tools:colonCall()` 和 `tools.colonCall(...)` 跳到 `function M:colonCall()`，VS Code 与 JetBrains 均通过；method 的 hover/detail 展示仍留待后续。
- [x] 第五轮 shared completion golden 增加非返回 table 与 table literal 非函数字段 guard；现有 VS Code 与 JetBrains 导出扫描均已通过验证，无需核心代码变更。
- [x] 终结轮：VS Code 与 JetBrains 收敛 `ModuleExportSnapshot` / `ExportedMember`，补齐 signature、documentation、detail，并由 shared completion golden 验证 completion detail 和定义前注释展示。
- [x] 追踪轮：在导出成员定义名上执行跳转时反查调用方；单调用方直接跳转，多调用方由 IDE 原生列表选择，无调用方提示“没有找到调用方”；VS Code 与 JetBrains 共享 require-definition golden 验证。
- [x] 追踪轮：方法/函数补全插入带括号和形参的 snippet/template，支持 Tab 逐个修改参数；formatter 忽略单行和 Lua 长注释内的块关键字；switch 回车按 `switch ... do` / `case` / `default` 实际语法糖展开。
- [x] 追踪轮：设置页文案支持中英切换，移除用户可编辑 DAP host/port；配置 `glua` 后支持当前 `.lua` / `.glua` 文件快速运行和 Debug 入口。

## 第四阶段：发布与 CI

- [x] VS Code 处理 LICENSE warning：确认项目授权策略后补许可证文件或记录发布约束。
- [x] VS Code 处理 bundle warning：评估是否用 esbuild 打包 server/client。
- [x] JetBrains 明确 Gradle JVM 使用 JDK 21，文档写入本地配置路径。
- [x] JetBrains 执行 `buildPlugin` 并检查插件 zip。
- [x] 增加顶层 `scripts/check-editor-extensions.sh`。

## 第五阶段：Debug 与生态 docs

- [x] VS Code DAP attach 增加连接失败提示。
- [x] JetBrains DAP attach 增加连接失败提示。
- [x] 增加“启动 glua 并自动 attach”的设计说明。
- [x] runtime/dap：实现可被嵌入方复用的 DAP server，CLI 只通过 `--glua-dap-listen=host:port` 调用该服务并输出 ready 标记。
- [x] runtime/dap：DAP server 支持 initialize、launch/attach、setBreakpoints、configurationDone、threads、stackTrace、scopes、variables、continue、next、stepIn、stepOut、disconnect 的基础协议响应。
- [x] glua VM：接入指令级 DebugObserver，支持源码行断点命中、stopped 事件、最小 stackTrace 和 continue 继续。
- [ ] glua VM：补齐 step over / step in / step out 的真实暂停语义。
- [ ] glua VM：补齐完整调用栈、scope 和 locals/upvalues 变量读取。
- [x] VS Code：Debug current file / launch 配置启动 `glua --glua-dap-listen=127.0.0.1:0 <file>`，等待 ready 后 attach。
- [x] JetBrains：Debug Current GLua File 启动 `glua --glua-dap-listen=127.0.0.1:0 <file>`，等待 ready 后 attach。
- [x] VS Code / JetBrains：Debug 失败时显示命令、工作目录、监听地址、退出码和 stderr/stdout 尾部。
- [x] 为 `cjson`、`lpeg`、`socket` 生成可选 docs/stub。
  - [x] `cjson` 基础 docs/stub：`encode`、`decode`、`new`、`null`、`cjson.safe`。
  - [x] `lpeg` 基础 docs/stub：`match`、`P`、`R`、`S`、`V`、`C`、`Cp`、`Cs`、`Ct`、`locale`、`setmaxstack`、`type`。
  - [x] `socket` 基础 docs/stub：`bind`、`connect`、`tcp`、`udp`、`select`、`sleep`、`gettime`、`protect`、`try`、`newtry`、`dns.*`、`socket.core`。

## 每轮交付要求

- 输出本轮实现点或证伪点。
- 输出涉及文件。
- 输出 VS Code 验证结果。
- 输出 JetBrains 验证结果；若因 JDK 21 缺失阻塞，明确恢复条件。
- 输出剩余 TODO。
- 不满足全部 TODO 和双扩展验证闭环前，不删除或停止自动化任务。
