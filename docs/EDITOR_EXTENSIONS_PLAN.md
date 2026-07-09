# GLua 编辑器扩展优化计划

本文定义 VS Code 与 JetBrains 两个 GLua 编辑器扩展的后续优化路线。目标是在不牺牲现有轻量实现的前提下，让两个扩展共享行为基线、减少诊断误报，并逐步补齐模块解析、调试、发布和 CI 验证能力。

## 目标

- VS Code 与 JetBrains 扩展在核心语言能力上保持一致。
- 建立共享 golden cases，覆盖 diagnostics、completion、hover、definition 和 formatting 的主要路径。
- 支持更实用的 Lua 模块工作流，包括 `require(...)` 文件解析、顶层 `return` 导出和 `package.path` / `package.cpath` 相关提示。
- 减少 token scan 带来的作用域误报，逐步形成可维护的简化 AST 或 block scope 模型。
- 补齐扩展发布前的本地和 CI 验证入口，确保 VSIX 与 JetBrains plugin zip 可重复构建。
- 让 GLua DAP attach 体验更顺滑，包括配置生成、错误提示和后续 launch 自动化。

## 非目标

- 不在短期内让 JetBrains 插件直接运行 VS Code 的 JS language server。该方案会引入 Node 发现、进程生命周期、日志、版本锁定和打包复杂度，短期收益低于共享 golden 与统一后端演进。
- 不在本阶段替换整个扩展架构为完整 Go LSP backend。长期可以引入 `gluals` 作为统一后端，但需要等行为基线和协议面稳定后再做。
- 不把编辑器扩展修复和 Go VM/native_modules 功能修复混在同一小切口里。

## 当前基础

- VS Code 扩展位于 `vscode/extensions/glua-lsp`。
- JetBrains 扩展位于 `jetbrains/extensions/glua-lsp`。
- 两边已有语法高亮、诊断、hover/docs、completion、definition、format 和 DAP attach 能力。
- 当前已开始引入共享诊断 golden：`tests/editor/golden-diagnostics.json`。
- VS Code 可用 Node 测试跑共享 golden；JetBrains 侧需要 JDK 21 才能跑 Gradle/JUnit 验证。

## 分阶段路线

### Phase 1：共享 golden 基线

目标：先用共享 golden 固定两个扩展的核心行为，避免修一边坏一边。

- diagnostics golden：语法扩展、作用域、module return、table field、require binding。
- completion golden：全局函数、库函数、模块成员、用户自定义 docs。
- hover golden：builtin docs、custom docs、多语言 fallback。
- definition golden：local definition、builtin docs pseudo target、require target。
- formatting golden：switch/case、continue、函数、table constructor 的主要形态。

### Phase 2：模块解析与 require 工作流

目标：让常见 Lua 单文件模块和目录模块体验自然。

- 解析 `require("foo")`、`require("foo.bar")` 到候选 `.lua` / `init.lua` 文件。
- 根据 `package.path` 与工作区根推导搜索路径，默认支持 Lua 5.3 常见模板。
- 支持 `package.cpath` 的 native 模块提示，不把 C 模块误判成 Lua 源文件。
- 顶层 `return M` / `return value` 不误报语法错误。
- 对不存在的 Lua 模块给出诊断或弱提示，避免影响动态模块场景。

#### require 解析规则

两个扩展应共享同一套候选规则，先在 golden 中固定，再分别实现：

- 只解析静态字符串参数：`require("foo")`、`require('foo.bar')`。动态表达式不诊断、不跳转。
- 模块名中的 `.` 映射为路径分隔符：`foo.bar` 对应 `foo/bar`。
- 默认 Lua 源文件候选顺序：
  - `${workspaceRoot}/foo/bar.lua`
  - `${workspaceRoot}/foo/bar/init.lua`
  - `${workspaceRoot}/lua/foo/bar.lua`
  - `${workspaceRoot}/lua/foo/bar/init.lua`
  - `${workspaceRoot}/src/foo/bar.lua`
  - `${workspaceRoot}/src/foo/bar/init.lua`
- 若当前文件所在目录存在相对候选，也应优先纳入：`./foo/bar.lua`、`./foo/bar/init.lua`。
- 后续读取 `package.path` 时，只支持静态赋值或静态拼接中的模板片段，例如 `package.path = "./?.lua;./?/init.lua;" .. package.path`。
- 诊断策略分级：
  - 找到 Lua 源文件：definition 跳转到文件，diagnostics 不报错。
  - 仅可能命中 native C 模块：不跳转到 Lua 文件，不报缺失错误，hover 提示来自 `package.cpath` / native 模块说明。
  - 找不到任何静态候选：默认给 warning 级弱诊断，允许用户通过设置关闭。
- `package.cpath` 只参与 native 模块提示，不参与 Lua 源文件跳转。
- golden 用例至少覆盖：存在文件、目录 `init.lua`、相对文件、缺失文件、native-only 模块和动态 require。

### Phase 3：作用域与符号模型

目标：从纯 token scan 逐步升级到可维护的 block scope。

- 区分 local/global、function 参数、for 变量、upvalue 与 table field。
- 正确处理顶层 return、do/if/for/while/repeat/function block。
- 避免把 table constructor 字段、对象属性赋值误当变量声明。
- 为 completion 和 definition 复用同一套符号表。

#### 轻量符号模型边界

短期不引入完整 Lua parser。两端先共享同一套轻量 token + block stack 模型，把高频误报压住，再根据 golden 扩展能力：

- 符号来源：
  - Lua 标准全局和 GLua 内建函数。
  - `local name`、`local a, b = ...`、`local function name(...)`。
  - 普通顶层或语句级简单赋值：`name = value`、`a, b = ...`，用于兼容 Lua 模块常见的非 local 导出写法。
  - 函数参数，包括 `function f(a)`、`function M.f(a)` 和 `local f = function(a)`。
  - `for i = ... do` 和 `for k, v in ... do` 的循环变量。
- 明确不作为新符号：
  - table constructor 字段：`{ exported = true }`。
  - table/object 成员赋值：`M.name = value`、`obj[name] = value`。
  - method 名和属性名：`function M:run(arg)` 中 `run` 不进入普通变量集合，`arg` 进入函数参数集合。
- block scope 策略：
  - 第一版诊断允许“文件级可见集合”近似 upvalue，优先减少模块导出误报。
  - 后续引入 block stack 后，再把 `do/if/for/while/repeat/function` 的局部变量生命周期收窄。
  - completion、definition、diagnostics 后续复用同一份符号快照，避免三套 token scan 规则分叉。

### Phase 3A：智能补全与模块成员索引

目标：使用“同步路径”同时增强 VS Code 与 JetBrains 扩展，让两个编辑器在变量补全、模块成员补全、冒号方法定义和跨文件跳转上保持一致。

#### 能力目标

- 作用域变量补全：
  - `for i = x, x do ... end` 的循环体内补全 `i`。
  - `for a, d in pairs(xxx) do ... end` 的循环体内补全 `a`、`d`。
  - 函数参数、`local` 变量、`local function`、简单赋值变量继续进入当前文件补全集。
  - 第一版允许使用“前文可见符号”近似作用域；后续再通过 block stack 收窄局部变量生命周期。
- 模块成员补全：
  - `local tools = require("module")` 后，输入 `tools.` 列出目标模块导出的点调用成员。
  - 输入 `tools:` 列出目标模块导出的冒号方法成员。
  - 点调用也可以列出冒号方法，但补全详情必须标明该成员定义为 method，以免用户误解 `self` 语义。
  - 补全项应尽量携带签名、注释文档、定义文件路径和调用风格。
- 冒号方法跳转与 hover：
  - `function aaa:ccc() ... end` 必须被识别为导出成员 `ccc`。
  - 调用侧 `tools:ccc()` 应跳转到 `function aaa:ccc()` 的 `ccc`。
  - 调用侧 `tools.ccc()` 也允许跳到同一成员，但 hover/detail 需要提示这是冒号方法。
  - 定义侧 hover 和文档提取继续支持 `-- description:` / `-- param:` / `-- return:` 注释块。

#### 模块导出索引

两个扩展应构建同一概念的 `ModuleExportSnapshot`，输入为目标 Lua 文件文本，输出为导出成员列表。

需要识别的导出 table：

- 顶层或局部变量返回：`return M`、`return aaa`。
- require 绑定别名只用于调用侧，不要求目标文件使用同名 table。
- 第一版不解析 `return { ... }` 匿名表；如果需要支持，单独加 golden 后再扩展。

需要识别的成员定义：

- 点函数声明：`function M.foo(a, b) ... end`。
- 冒号函数声明：`function M:foo(a, b) ... end`。
- 点赋值函数：`M.foo = function(a, b) ... end`。
- bracket 赋值函数：`M["foo"] = function(a, b) ... end`、`M['foo'] = function(a, b) ... end`。
- table literal 字段函数：`M = { foo = function(...) ... end }`。
- table literal bracket 字段函数：`M = { ["foo"] = function(...) ... end }`、`M = { ['foo'] = function(...) ... end }`。

每个导出成员至少记录：

- `name`：成员名，例如 `ccc`。
- `range`：定义位置，用于跳转。
- `kind`：`function`、`method`、`field`，第一版主要覆盖 `function` 和 `method`。
- `callStyle`：`.` 或 `:`，用于 `module.` / `module:` 补全过滤。
- `signature`：从函数参数列表生成，例如 `ccc(name, times)`；冒号方法可展示为 `ccc(self, ...)` 或 `ccc(...) method`，展示策略两端保持一致。
- `documentation`：定义前连续注释块转成 markdown/html。
- `sourcePath`：目标文件路径，用于 detail 或排查日志。

#### Completion 行为约定

- `module.`：
  - 优先列出 `callStyle = "."` 的导出成员。
  - 同时列出冒号方法成员，但 detail 中标记 `method`，避免隐藏可访问成员。
  - 内置库成员继续保留现有逻辑，例如 `string.`、`table.`。
- `module:`：
  - 优先列出 `callStyle = ":"` 的导出成员。
  - 对已知 receiver 类型继续保留内置类型方法逻辑。
- 普通全局位置：
  - 列出当前文件符号、内置函数、自定义 docs 中的函数名。
  - 不把 table constructor 字段作为普通全局补全。
- 插入文本：
  - 第一版只插入成员名，避免错误生成参数。
  - 后续可用 snippet 插入参数占位，但必须有测试覆盖。

#### Definition / Hover 行为约定

- `tools.ccc` 和 `tools:ccc` 都应能通过 require binding 找到目标文件导出成员。
- 对 `function aaa:ccc()`，definition range 应落在 `ccc` 标识符，不落在 `aaa` 或整行。
- hover 读取定义前注释块：

```lua
-- description: print many times
-- param: name string user name
-- param: times number repeat count
function aaa:ccc(name, times)
end
```

- 若模块文件存在但成员不存在，completion 可为空；definition 返回 null，不给强诊断。

#### 测试矩阵

共享 golden 应覆盖：

- numeric for：`for i = 1, n do` 内补全 `i`。
- generic for：`for a, d in pairs(xxx) do` 内补全 `a`、`d`。
- require + 点成员：`tools.timesPrint` 跳到 `M.timesPrint = function`。
- require + 冒号成员：`tools:ccc()` 跳到 `function M:ccc()`。
- require + 点调用冒号定义：`tools.ccc()` 也能跳到 `function M:ccc()`。
- table literal 点字段：`M = { ccc = function() end }` 后 `tools.ccc` 可补全和跳转。
- table literal bracket 字段：`M = { ['ccc'] = function() end }` 后 `tools.ccc` 可补全和跳转。
- non-goal guard：`{ ccc = true }` 不作为函数补全；非返回 table 的局部表成员不跨文件暴露。

#### 实现策略

- VS Code：
  - 在 `server/index.js` 中把当前 `exportedMemberDefinition` 拆成 `moduleExportSnapshot(filePath)`。
  - `requiredMemberTarget`、hover、completion 复用该 snapshot，避免重复扫描目标文件。
  - LSP 测试扩展 `golden-require-definition` 或新增 `golden-completion`。
- JetBrains：
  - 在 `GluaRequireSupport` 中建立对齐的 `ModuleExportSnapshot` / `ExportedMember` 概念。
  - `GluaGotoDeclarationHandler`、`GluaDocumentationProvider`、`GluaCompletionContributor` 通过同一入口查询 require 模块导出。
  - JUnit 覆盖 formatter、definition 和 completion 名称集合。
- 两端同步：
  - 先把 shared golden 写清楚，再分别实现。
  - 每次新增一种导出语法，必须同时更新 VS Code 和 JetBrains 测试。
  - 保持窄解析，不为了补全引入完整 Lua parser。

### Phase 4：发布和 CI 闭环

目标：让两个扩展可以可靠打包和验收。

- VS Code：`npm test`、`vsce package`、`.vscodeignore`、bundle 或明确保留 node_modules 的策略。
- JetBrains：JDK 21 toolchain、`gradlew test`、`buildPlugin`、插件 zip 产物检查。
- 顶层脚本统一跑两个扩展的 golden 和打包 smoke。
- 文档记录本机 JDK/Node 前置条件。

### Phase 5：Debug 与生态文档

目标：提升真实使用体验。

- GLua DAP attach 配置检测和错误提示。
- 实现 `glua` runtime/CLI 内置 DAP server，然后支持编辑器启动 `glua` 并自动 attach。
- 为 `cjson`、`lpeg`、`socket` 等验收过的真实模块提供可选 docs/stub。
- 对 native_modules 下的 `package.cpath` / `package.loadlib` 行为提供更清晰 hover。

#### 当前 DAP 状态校准

截至当前轮次，`runtime/dap` 已提供可复用的最小 DAP server；`glua` CLI 只是在收到 `--glua-dap-listen=host:port` 时创建并启动该 server。该 server 会输出 ready 标记，支持基础 DAP 握手、断点设置、源码行断点暂停、`stopped` 事件、最小 `stackTrace` 和 `continue`。VS Code 与 JetBrains 扩展已改为启动 `glua --glua-dap-listen=127.0.0.1:0 <file>`，等待 ready 后 attach 到真实端口，并在启动失败时展示命令、工作目录、监听地址、退出码和输出尾部。单步、完整调用栈、scope 和变量面板仍是后续实现项。

#### glua runtime DAP server 目标

第一阶段实现最小可调试闭环，默认不影响普通 `glua script.lua` 执行：

- `runtime/dap` 暴露可复用 server，嵌入方可以直接创建并写入 `runtime.Options.DebugObserver`；也可以按自身需求实现自己的 `runtime.DebugObserver` 和 DAP 服务。
- CLI 新增显式参数：`--glua-dap-listen=127.0.0.1:5678`，只在用户或编辑器显式传入时启用，并且只负责调用 `runtime/dap`。
- 监听地址默认要求 loopback；普通用户设置页不暴露 host/port。
- DAP server 先覆盖基础协议：`initialize`、`launch`/`attach`、`setBreakpoints`、`configurationDone`、`threads`、`stackTrace`、`scopes`、`variables`、`continue`、`next`、`stepIn`、`stepOut`、`disconnect`。
- VM 层已先接入指令级 DebugObserver 实现源码行断点暂停；后续单步可继续复用同一观察点，step in/out 再结合 call/return hook 与调用帧深度。
- 进程启动后需要输出明确 ready 标记，供扩展无侵入等待；如果 DAP 协议只允许单连接，扩展不得用 TCP 探测占用真实连接。
- 失败必须展示启动命令、工作目录、监听地址、退出码和 stderr 尾部。

#### 启动 glua 并自动 attach 的设计边界

该能力的目标是让用户从编辑器内直接启动脚本，并在 GLua DAP 服务就绪后自动进入 attach 调试会话。短期只固定设计约束，不在扩展里硬编码尚未稳定的 `glua` 调试启动参数。

- 共享配置模型：
  - `glua` 可执行文件路径，默认从 PATH 查找。
  - 被调试 Lua 脚本、脚本参数、工作目录和环境变量。
  - DAP 地址由扩展内部管理，默认只使用本地回环地址；不把 host/port 暴露给普通用户填写。
  - 是否在调试结束时终止子进程，默认终止由扩展启动的进程，手动 attach 的进程不接管生命周期。
- 启动流程：
  - 扩展先创建 `glua` 子进程，并把内部调试地址通过稳定后的 CLI 参数或环境变量传入。
  - 优先依赖 `glua` 输出明确的 DAP ready 标记；没有 ready 标记时，允许使用有超时的 TCP 就绪探测。
  - 如果未来 DAP 服务只允许单连接，TCP 探测必须改为非侵入式 ready 信号，避免探测连接占用真实调试会话。
  - 就绪后复用现有 attach 配置进入调试会话，attach 失败时展示内部地址、错误原因和恢复建议提示。
- 失败与清理：
  - 启动失败需要展示命令、工作目录、内部 DAP 地址、退出码和 stderr 尾部，避免只显示通用连接失败。
  - 启动超时需要提示用户检查 `glua` 是否启用 DAP、端口是否被占用、防火墙或 VPN 是否拦截本地连接。
  - 用户停止调试或 attach 失败后，应关闭由扩展启动的子进程，避免残留后台进程和端口占用。
  - 若用户选择 detached 模式，扩展只结束调试会话，不终止外部进程。
- 编辑器实现路径：
  - VS Code：新增 launch 型 debug 配置或命令，使用子进程管理器启动 `glua`，ready 后通过 `DebugAdapterServer` attach；失败提示复用现有 DAP attach 文案。
  - JetBrains：新增 run configuration，`ProcessHandler` 管理 `glua` 进程和控制台输出，ready 后复用 DAP attach 入口；失败提示复用当前 `GluaDapAttachProcessHandler` 的诊断文案。
- 验证策略：
  - 增加 fake `glua` fixture：可模拟端口就绪、启动超时、提前退出和 stderr 输出。
  - VS Code 覆盖启动成功、attach 返回 false、启动超时和进程清理。
  - JetBrains 覆盖 ready 后 attach、连接失败提示和子进程终止。
  - 打包 smoke 继续通过 `scripts/check-editor-extensions.sh` 验证。

## 验证策略

- 每轮只做一个可验证小切口。
- 修改 VS Code 扩展后至少执行：

```bash
cd vscode/extensions/glua-lsp
npm test
npx --yes @vscode/vsce package --out /tmp/glua-lsp-vscode-test.vsix
```

- 修改 JetBrains 扩展后至少执行：

```bash
cd jetbrains/extensions/glua-lsp
./gradlew --no-daemon --no-configuration-cache test
```

- 修改共享 JSON 后必须执行 JSON parse 校验。
- 修改 Go 文件时回到项目 Go 门禁：gopls、gofmt、相关 Go test、脚本门禁和未跟踪 Go 文件检查。
