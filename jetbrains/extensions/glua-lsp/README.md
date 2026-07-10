# JetBrains IDE 的 GLua 语言扩展

这是面向 `go-lua-vm` / `glua` 的 JetBrains IntelliJ Platform 插件，提供 GLua/Lua 编码、模块导航、文档提示、格式化和 DAP 调试连接能力。

## 概述

GLua 语言扩展覆盖日常开发中的核心链路：`.lua` / `.glua` 文件识别、语法高亮、扩展语法诊断、作用域补全、`require` 模块成员补全、冒号方法跳转、快速文档、自定义函数文档和 GLua DAP Debug/Attach。

该扩展采用纯 IntelliJ Platform 插件实现，可以运行在基于 IntelliJ Platform 构建且版本处于插件兼容范围内的 JetBrains IDE 中，包括 IntelliJ IDEA、GoLand、WebStorm、PyCharm、PhpStorm、RubyMine、CLion、DataGrip、Rider 和 Android Studio。

## 功能

- 支持 `.glua` 和 `.lua` 文件类型。
- 支持 Lua 与 glua 扩展语法高亮。
- 支持 `switch`、`case`、`default` 和 `continue` 等扩展语法诊断。
- 提供默认函数和自定义函数补全。
- 提供默认函数和自定义函数快速文档。
- 提供 `glua.event`、JSON/YAML/XML/TOML 序列化、codec、hash、regex、UUID、ZIP、path 和 schema API 的补全、定义跳转及中英文快速文档。
- 支持通过 Cmd/Ctrl 单击跳转到局部定义和内置函数文档。
- 提供 `Code -> Format GLua File` 操作。
- 支持多语言内置函数/函数文档。
- 支持通过用户 JSON 文件扩展或覆盖函数签名。
- 提供当前 `.lua` 或 `.glua` 文件的快速运行和调试操作。
- 提供原生 JetBrains DAP 支持，DAP 地址由插件管理。

## 设置

打开：

```text
Settings / Preferences -> GLua
```

可用设置：

- `Documentation language`：`auto`、`en`、`zh-CN`。
- `glua executable`：快速运行/调试功能使用的 `glua` 可执行文件路径。
- `gluac executable`：编译辅助功能使用的 `gluac` 可执行文件路径。
- `Builtin docs JSON files`：每行填写一个 JSON 文件路径。

`glua.json`、`glua.yaml` 和 `glua.xml` 由完整运行时的 `OpenLibs` 注册，不依赖 Event 开关；插件内置目录会为这些命名空间提供成员补全和定义目标。

`auto` 使用 IDE/JVM 的默认区域设置，并将其规范为语言标签。例如，`zh-cn` 会转换为 `zh-CN`，`ja-jp` 会转换为 `ja-JP`。

插件会把实际使用的内置文档语言写入 IDE 日志：

```text
glua builtin docs locale=zh-CN, entries=56, files=1
```

请使用该 locale 值作为自定义 JSON 的语言键。

## 配置自定义方法签名

JetBrains 使用独立的插件设置，与 VS Code 配置互不影响。

在以下位置添加自定义方法签名 JSON 文件：

```text
Settings / Preferences -> GLua -> Builtin docs JSON files
```

每行填写一个文件路径。

推荐的项目级路径：

```text
./glua-builtin-docs.json
```

使用相对路径时，会从 IDE 工作目录开始解析。团队使用时，建议使用绝对路径，或从文件上下文菜单复制项目路径。

## 自定义 JSON 格式

JetBrains 插件与 VS Code 扩展使用相同的 JSON 结构：

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

函数名可以是全局函数，也可以是带库名称的限定函数。

`example` 为可选字段。它与 `description`、`params` 和 `returns` 一样支持按语言设置内容；存在时会显示在文档中。

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

把 JSON 文件加入设置后，补全和文档中会包含这些函数。

## 运行与调试

设置 `glua executable` 后，打开 `.lua` 或 `.glua` 文件，并使用编辑器上下文菜单或 Tools 菜单：

```text
Run Current GLua File
Debug Current GLua File
```

`Run Current GLua File` 会启动 `glua <current-file>`。
`Debug Current GLua File` 会为当前文件创建并选中 GLua Debug 配置。DAP 地址由插件内部管理，不显示为可编辑的主机/端口字段。

也可以创建原生 JetBrains 运行/调试配置：

```text
Run -> Edit Configurations -> Add New Configuration -> GLua DAP Attach
```

该配置只要求填写 `glua` 可执行文件和程序文件。

如果调试失败：

- 检查配置的 `glua` 可执行文件是否正确。
- 检查使用的 GLua 运行时是否已启用 DAP 支持。
- 打开运行/调试控制台查看失败原因。

## 覆盖默认内置文档

如果自定义 JSON 定义了与默认内置目录相同的函数，以自定义 JSON 为准。

示例：

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

只会覆盖自定义 JSON 中存在的字段；缺失字段会回退到默认内置文档。

## 单语言简写

对于只面向一种语言的文件，可以使用文件级 `locale`：

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

插件会在内部将该格式规范为多语言结构。

## 构建

IDE 同步和插件构建应使用 JDK 21 作为 Gradle JVM。

在 JetBrains IDE 中打开：

```text
Settings / Preferences -> Build, Execution, Deployment -> Build Tools -> Gradle -> Gradle JVM
```

选择 JDK 21 安装目录。在使用 Homebrew 的 macOS 上，常见路径为：

```text
/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home
/usr/local/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home
```

如果尚未安装 JDK 21：

```bash
brew install openjdk@21
```

Java 26 可能导致 Gradle 9.0.0 的 IDE 同步失败，因为 IDE 支持的 Gradle JVM 上限低于 Java 26。某些环境中的命令行 wrapper 仍可能在较新 Java 上运行，但该插件支持的本地基线是 JDK 21。

在当前目录中运行测试：

```bash
./gradlew --no-daemon --no-configuration-cache test
```

构建可安装的插件 zip：

```bash
./gradlew --no-daemon --no-configuration-cache buildPlugin
```

插件包输出到：

```text
build/distributions/glua-jetbrains-0.0.1.zip
```
