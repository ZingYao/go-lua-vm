# GLua 语言扩展

面向 `go-lua-vm` / `glua` 的 VS Code 编辑器扩展，提供 GLua/Lua 编码、模块导航、文档提示、格式化和 DAP 调试连接能力。

## 概述

GLua 语言扩展面向日常开发场景设计：写 Lua/glua 文件时能获得语法高亮、扩展语法诊断、作用域补全、`require` 模块成员补全、冒号方法跳转和悬停文档；配置 `glua` 可执行文件后，可以在 VS Code 内快速运行或 Debug 当前文件。

## 功能

- 支持 `.lua` 和 `.glua` 文件的语法高亮。
- 支持 glua 扩展语法诊断。
- 提供内置函数和自定义函数的悬停文档。
- 支持通过 Cmd/Ctrl 单击跳转到局部函数定义和内置函数文档。
- 支持 glua 扩展语法格式化。
- 提供内置函数和自定义函数补全。
- 提供 `glua.event`、JSON/YAML/XML/TOML 序列化、codec、hash、regex、UUID、ZIP 和 schema API 的补全、定义跳转及中英文悬停文档。
- 支持用户定义的内置函数/函数签名 JSON。
- 支持多语言函数文档。
- 提供当前 `.lua` 或 `.glua` 文件的快速运行和调试命令。
- 提供原生 VS Code DAP 调试连接，DAP 地址由扩展管理。

## 许可证

该扩展当前标记为 `UNLICENSED`。VSIX 中包含记录此发布限制的 `LICENSE` 文件；除非项目所有者另行发布许可证授权，否则不得公开再分发。

## 设置

```json
{
  "glua.syntax": "extended",
  "glua.docLanguage": "auto",
  "glua.executable": "/path/to/glua",
  "glua.gluacExecutable": "/path/to/gluac",
  "glua.useRemoteDap": false,
  "glua.builtinDocs": [
    ".vscode/glua-builtin-docs.json"
  ]
}
```

`glua.syntax` 控制语法支持范围：

- `extended`：启用 glua 扩展，目前包括 `switch`、`continue`、`const` 和 Event 能力。
- `lua53`：只启用与 Lua 5.3 兼容的语法。
- `switch`、`continue`：启用指定扩展。
- 支持逗号分隔的配置值，例如 `switch,continue`。

`glua.json`、`glua.yaml` 和 `glua.xml` 属于运行时扩展方法，不依赖语法糖开关。完整数据规则参见仓库中的 `docs/glua-serialization.md`。

`glua.docLanguage` 控制悬停提示、补全文档和内置 stub 的语言：

- `auto`：跟随 VS Code 界面语言。
- `en`, `zh-CN`, `ja-JP`, `ko`, `fr-FR`, etc.

扩展会把检测到的语言写入 `输出 -> glua Language Server`：

```text
[glua-lsp] activate: vscode.env.language=zh-cn; glua.docLanguage=auto; requested doc language=zh-cn; resolved doc language=zh-CN; builtin docs=1
```

请使用日志中的 `resolved doc language` 值作为自定义 JSON 的语言键。

## 创建自定义函数文档

打开命令面板并执行：

```text
glua: Open Builtin Signature JSON
```

该命令可以创建或打开项目级、全局级 JSON 文件。项目级文件保存在：

```text
<project_root>/.vscode/glua-builtin-docs.json
```

项目级文件通常更便于审查并随仓库共享。

VS Code 设置页面不会为字符串或数组配置显示原生文件选择控件。如需选择文件，请在命令面板中使用以下命令：

```text
GLua: Select glua executable
GLua: Select gluac executable
GLua: Select builtin docs JSON
```

也可以手动创建文件，并在 `glua.builtinDocs` 中引用它。

## 自定义 JSON 格式

推荐格式如下：

```json
{
  "functions": {
    "print": {
      "signature": {
        "en": "print(...)",
        "zh-CN": "print(...)",
        "ja-JP": "print(...)"
      },
      "description": {
        "en": "Prints values to standard output.",
        "zh-CN": "将值输出到标准输出。",
        "ja-JP": "値を標準出力へ出力します。"
      },
      "params": {
        "en": [
          "...: values to print"
        ],
        "zh-CN": [
          "...: 要输出的值"
        ],
        "ja-JP": [
          "...: 出力する値"
        ]
      },
      "returns": {
        "en": "returns: no return values.",
        "zh-CN": "返回：无返回值。",
        "ja-JP": "戻り値はありません。"
      },
      "example": {
        "en": "print('hello')",
        "zh-CN": "print('你好')",
        "ja-JP": "print('こんにちは')"
      }
    }
  }
}
```

函数名可以是全局名称，也可以是带库名称的限定名称。

`example` 为可选字段。它与 `description`、`params` 和 `returns` 一样支持按语言设置内容；存在时会显示在悬停和补全文档中。

```json
{
  "functions": {
    "myGlobalFunc": {
      "signature": {
        "en": "myGlobalFunc(value)"
      },
      "description": {
        "en": "Runs a custom global function."
      },
      "params": {
        "en": [
          "value: input value"
        ]
      },
      "returns": {
        "en": "returns: result value."
      },
      "example": {
        "en": "local result = myGlobalFunc(value)"
      }
    },
    "string.slug": {
      "signature": {
        "en": "string.slug(s)",
        "zh-CN": "string.slug(s)"
      },
      "description": {
        "en": "Converts a string to a URL slug.",
        "zh-CN": "将字符串转换为 URL slug。"
      },
      "params": {
        "en": [
          "s: source string"
        ],
        "zh-CN": [
          "s: 源字符串"
        ]
      },
      "returns": {
        "en": "returns: slug string.",
        "zh-CN": "返回：slug 字符串。"
      },
      "example": {
        "en": "local value = string.slug('Hello World')",
        "zh-CN": "local value = string.slug('Hello World')"
      }
    }
  }
}
```

配置该文件后，补全和悬停提示中会包含 `myGlobalFunc` 与 `string.slug`。

## 覆盖内置文档

自定义 JSON 中的函数名与默认内置函数冲突时，以自定义 JSON 为准。

例如，以下配置会覆盖内置的 `string.match` 文档：

```json
{
  "functions": {
    "string.match": {
      "description": {
        "en": "Project-specific string match behavior.",
        "zh-CN": "项目自定义的 string.match 行为说明。"
      },
      "params": {
        "en": [
          "s: project string",
          "pattern: project pattern"
        ],
        "zh-CN": [
          "s: 项目字符串",
          "pattern: 项目模式"
        ]
      },
      "returns": {
        "en": "returns: project-specific match values.",
        "zh-CN": "返回：项目自定义匹配值。"
      },
      "example": {
        "en": "local a, b = string.match('a,b', '(.-),(.*)')",
        "zh-CN": "local a, b = string.match('a,b', '(.-),(.*)')"
      }
    }
  }
}
```

只需提供需要覆盖的字段；缺失字段会回退到默认内置文档。

## 多语言规则

语言键使用标准语言标签：

- `en`
- `zh-CN`
- `ja-JP`
- `ko`
- `fr-FR`
- `pt-BR`

`zh-cn`、`zh_CN` 和 `zh-hans` 会统一规范为 `zh-CN`，`ja-jp` 等标签会规范为 `ja-JP`。

如果缺少所选语言，glua-lsp 会先回退到 `en`，再回退到该条目中其他可用语言。

## 单语言简写

如果文件只面向一种语言，可以使用文件级 `locale`：

```json
{
  "locale": "ja-JP",
  "functions": {
    "print": {
      "signature": "print(...)",
      "description": "値を標準出力へ出力します。",
      "params": [
        "...: 出力する値"
      ],
      "returns": "戻り値はありません。",
      "example": "print('こんにちは')"
    }
  }
}
```

该格式会在内部规范为多语言结构。

## 重新加载变更

编辑 `glua.builtinDocs` 或内置 JSON 文件后，如果悬停或补全没有立即刷新，请重新加载 VS Code 窗口。

执行：

```text
Developer: Reload Window
```

## 运行与调试

扩展提供名为 `glua` 的原生 VS Code 调试器类型。
设置 `glua.executable` 后，可以通过编辑器上下文菜单或命令面板执行：

```text
GLua: Run current file
GLua: Debug current file
```

`Run current file` 会在文件所属工作区或目录中执行 `glua <current-file>`。
`Debug current file` 会启动 `glua` 调试会话。DAP 地址由扩展内部管理，不作为用户可编辑设置公开。

将 `glua.useRemoteDap` 设为 `true` 后，`Debug current file` 会连接到
`glua.dapHost` / `glua.dapPort`，而不是启动已配置的可执行文件。

也可以创建最小化的 `.vscode/launch.json` 配置：

```json
{
  "type": "glua",
  "request": "attach",
  "name": "Attach to GLua DAP"
}
```

如果希望启动下拉列表中的配置使用远程 DAP，请添加 `useRemoteDap`：

```json
{
  "type": "glua",
  "request": "launch",
  "name": "Debug via remote GLua DAP",
  "program": "${file}",
  "useRemoteDap": true,
  "host": "${config:glua.dapHost}",
  "port": "${config:glua.dapPort}"
}
```

如果调试失败：

- 检查 `glua.executable` 是否指向预期的可执行文件。
- 检查使用的 GLua 运行时是否已启用 DAP 支持。
- 打开 `输出 -> glua Language Server` 查看失败原因。
