# GLua 语言扩展 for JetBrains IDEs

这是面向 `go-lua-vm` / `glua` 的 JetBrains IntelliJ Platform 插件，提供 GLua/Lua 编码、模块导航、文档提示、格式化和 DAP 调试连接能力。

## Overview

GLua 语言扩展覆盖日常开发中的核心链路：`.lua` / `.glua` 文件识别、语法高亮、扩展语法诊断、作用域补全、`require` 模块成员补全、冒号方法跳转、快速文档、自定义函数文档和 GLua DAP Debug/Attach。

It is implemented as a pure IntelliJ Platform plugin, so it can run in JetBrains IDEs built on the IntelliJ Platform, including IntelliJ IDEA, GoLand, WebStorm, PyCharm, PhpStorm, RubyMine, CLion, DataGrip, Rider, and Android Studio versions compatible with the plugin build range.

## Features

- `.glua` and `.lua` file type support
- Syntax highlighting for Lua and glua syntax extensions
- Diagnostics for extended syntax such as `switch`, `case`, `default`, and `continue`
- Completion for default and custom functions
- Quick documentation for default and custom functions
- Cmd/Ctrl click navigation for local definitions and builtin function docs
- `Code -> Format GLua File` action
- Multi-language builtin/function documentation
- User JSON files that extend or override function signatures
- Quick run/debug actions for the current `.lua` or `.glua` file
- Native JetBrains DAP support with the DAP address managed by the plugin

## Settings

Open:

```text
Settings / Preferences -> GLua
```

Available settings:

- `Documentation language`: `auto`, `en`, `zh-CN`
- `glua executable`: path to the `glua` executable used by quick run/debug helpers
- `gluac executable`: path to the `gluac` executable used by compile helpers
- `Builtin docs JSON files`: one JSON file path per line

`auto` uses the IDE/JVM default locale and normalizes it to a language tag. For example, `zh-cn` becomes `zh-CN`, and `ja-jp` becomes `ja-JP`.

The plugin writes the effective builtin docs language to the IDE log:

```text
glua builtin docs locale=zh-CN, entries=56, files=1
```

Use this locale value as the language key in your custom JSON.

## Where To Configure Custom Method Signatures

JetBrains uses its own plugin settings, separate from VS Code.

Add custom method signature JSON files here:

```text
Settings / Preferences -> GLua -> Builtin docs JSON files
```

Put one file path per line.

Recommended project-level path:

```text
./glua-builtin-docs.json
```

If a relative path is used, resolve it from the IDE working directory. For stable team usage, prefer an absolute path or a project path copied from the file context menu.

## Custom JSON Format

The JetBrains plugin uses the same JSON shape as the VS Code extension:

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

Function names can be global functions or qualified library functions:

`example` is optional. Like `description`, `params`, and `returns`, it supports language-keyed values and is shown in documentation when present.

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

After adding the JSON file to settings, completion and documentation include these functions.

## Run And Debug

After setting `glua executable`, open a `.lua` or `.glua` file and use the editor context menu or Tools menu:

```text
Run Current GLua File
Debug Current GLua File
```

`Run Current GLua File` starts `glua <current-file>`.
`Debug Current GLua File` creates and selects a GLua Debug configuration for the current file. The DAP address is managed internally by the plugin and is not shown as an editable host/port field.

You can also create a native JetBrains Run/Debug configuration:

```text
Run -> Edit Configurations -> Add New Configuration -> GLua DAP Attach
```

The configuration asks for the `glua` executable and program file only.

If Debug fails:

- Check that the configured `glua` executable is correct.
- Check that the GLua runtime you use has DAP support enabled.
- Open the Run/Debug console to see the failure reason.

## Override Default Builtins

If your JSON defines the same function as the default builtin catalog, your JSON wins.

Example:

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

Only fields present in custom JSON are overridden. Missing fields fall back to default builtin docs.

## Single-language Shortcut

For one-language files, use file-level `locale`:

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

The plugin normalizes this into the multi-language structure internally.

## Build

Use JDK 21 as the Gradle JVM for IDE sync and plugin builds.

In JetBrains IDEs, open:

```text
Settings / Preferences -> Build, Execution, Deployment -> Build Tools -> Gradle -> Gradle JVM
```

Select a JDK 21 installation. On macOS with Homebrew, common paths are:

```text
/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home
/usr/local/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home
```

If JDK 21 is missing:

```bash
brew install openjdk@21
```

Java 26 can fail IDE sync with Gradle 9.0.0 because the IDE-supported Gradle JVM ceiling is lower than Java 26. The command-line wrapper may still run on newer Java in some environments, but JDK 21 is the supported local baseline for this plugin.

From this directory, run tests:

```bash
./gradlew --no-daemon --no-configuration-cache test
```

Build the installable plugin zip:

```bash
./gradlew --no-daemon --no-configuration-cache buildPlugin
```

The plugin package is written to:

```text
build/distributions/glua-jetbrains-0.0.1.zip
```
