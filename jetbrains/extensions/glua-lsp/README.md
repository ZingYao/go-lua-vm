# glua Language Support for JetBrains IDEs

This is the JetBrains IntelliJ Platform plugin for `go-lua-vm` / `glua`.

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
- Native JetBrains DAP attach configuration for running GLua Debug Adapter Protocol servers
- GLua DAP attach host/port settings and a helper action for sharing attach JSON

## Settings

Open:

```text
Settings / Preferences -> GLua
```

Available settings:

- `Doc language tag`: `auto`, `en`, `zh-CN`, `ja-JP`, `ko`, `fr-FR`, etc.
- `Builtin docs JSON files`: one JSON file path per line
- `DAP attach host`: GLua DAP server IP address or host name
- `DAP attach port`: GLua DAP server TCP port, from 1 to 65535

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

## Debug Attach

Create a native JetBrains Run/Debug configuration:

```text
Run -> Edit Configurations -> Add New Configuration -> GLua DAP Attach
```

Set:

- `DAP attach host`: GLua DAP server IP address or host name
- `DAP attach port`: GLua DAP server TCP port

Use the Debug action to start a JetBrains DAP session backed by the configured TCP server. The GLua VM debugger process must already be listening on that address.

You can also store default attach values in:

```text
Settings / Preferences -> GLua
```

New `GLua DAP Attach` configurations use these saved defaults.

For sharing or copying the attach payload, run:

```text
Tools -> Copy GLua DAP Attach Config
```

The action copies this JSON shape to the clipboard:

```json
{
  "type": "glua",
  "request": "attach",
  "name": "Attach to GLua DAP",
  "host": "127.0.0.1",
  "port": 5678
}
```

The JSON shape matches the VS Code attach configuration.

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

From this directory:

```bash
gradle buildPlugin
```
