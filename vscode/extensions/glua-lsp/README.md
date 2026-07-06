# glua Language Server

VS Code extension for `go-lua-vm` / `glua`. It provides syntax highlighting, diagnostics, hover docs, go to definition, formatting, and completion for Lua plus glua syntax extensions such as `switch` and `continue`.

## Features

- Syntax highlighting for `.lua` and `.glua`
- Diagnostics for glua extended syntax
- Hover documentation for builtin and custom functions
- Cmd/Ctrl click go to definition for local functions and builtin docs
- Formatting for glua syntax extensions
- Completion for builtin and custom functions
- User-defined builtin/function signature JSON
- Multi-language function documentation
- Native VS Code debug attach to a running GLua DAP server over TCP

## Settings

```json
{
  "glua.syntax": "extended",
  "glua.docLanguage": "auto",
  "glua.builtinDocs": [
    ".vscode/glua-builtin-docs.json"
  ],
  "glua.debug.host": "127.0.0.1",
  "glua.debug.port": 5678
}
```

`glua.syntax` controls syntax support:

- `extended`: enable glua extensions, currently including `switch` and `continue`
- `lua53`: Lua 5.3 compatible syntax only
- `switch`, `continue`: enable selected extensions
- comma-separated values are supported, for example `switch,continue`

`glua.docLanguage` controls hover, completion documentation, and builtin stub language:

- `auto`: follow VS Code UI language
- `en`, `zh-CN`, `ja-JP`, `ko`, `fr-FR`, etc.

The extension writes the detected language to `Output -> glua Language Server`:

```text
[glua-lsp] activate: vscode.env.language=zh-cn; glua.docLanguage=auto; requested doc language=zh-cn; resolved doc language=zh-CN; builtin docs=1
```

Use the `resolved doc language` value as the language key in your custom JSON.

## Create Custom Function Docs

Open the Command Palette and run:

```text
glua: Open Builtin Signature JSON
```

The command can create or open a project-level or global-level JSON file. Project-level files are stored at:

```text
<project_root>/.vscode/glua-builtin-docs.json
```

Project files are usually easier to review and share with the repository.

You can also create the file manually and reference it from `glua.builtinDocs`.

## Custom JSON Format

The recommended format is:

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

Function names can be global names or qualified library names:

`example` is optional. Like `description`, `params`, and `returns`, it supports language-keyed values and is shown in hover/completion docs when present.

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

After configuring this file, completion and hover will include `myGlobalFunc` and `string.slug`.

## Override Builtin Docs

When a custom JSON function name conflicts with a default builtin, the custom JSON wins.

For example, this overrides the built-in `string.match` documentation:

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

You only need to provide fields you want to override. Missing fields fall back to the default builtin docs.

## Multi-language Rules

Language keys use standard language tags:

- `en`
- `zh-CN`
- `ja-JP`
- `ko`
- `fr-FR`
- `pt-BR`

`zh-cn`, `zh_CN`, and `zh-hans` are normalized to `zh-CN`. Tags such as `ja-jp` are normalized to `ja-JP`.

If the selected language is missing, glua-lsp falls back to `en`, then to another available language in the entry.

## Single-language Shortcut

For a file that only targets one language, you can use a file-level `locale`:

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

This is normalized internally to the multi-language shape.

## Reloading Changes

After editing `glua.builtinDocs` or a builtin JSON file, reload the VS Code window if hover/completion does not refresh immediately.

Use:

```text
Developer: Reload Window
```

## Debug Attach

The extension contributes a native VS Code debugger type named `glua`.
It attaches the VS Code Debug UI to an already running GLua Debug Adapter Protocol server over TCP.

Create `.vscode/launch.json` with the Command Palette:

```text
GLua: Create DAP attach configuration
```

To attach once without editing `launch.json`, run:

```text
GLua: Attach to DAP server
```

The command prompts for `host` and `port`, then starts a native VS Code debug session immediately.

Or add the configuration manually:

```json
{
  "type": "glua",
  "request": "attach",
  "name": "Attach to GLua DAP",
  "host": "127.0.0.1",
  "port": 5678
}
```

`host` and `port` must point to a process that speaks DAP. The editor extension does not start the GLua VM debugger by itself.
