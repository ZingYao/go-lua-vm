# 语法糖

GLua 在 Lua 5.3 基线上提供 `continue`、`switch/case/default` 和 `const`。这些扩展发生在 lexer、parser 和 codegen 层，不新增 VM opcode。

## 开关

默认构建会编译全部语法糖。需要精确控制时，可以使用 Go build tag：

~~~bash
# 完全关闭扩展语法
go build -tags lua53 -o bin/glua-lua53 ./cmd/glua

# 只编译指定语法糖
go build -tags with_continue -o bin/glua-continue ./cmd/glua
go build -tags with_switch -o bin/glua-switch ./cmd/glua
go build -tags with_const -o bin/glua-const ./cmd/glua

# 显式编译全部扩展
go build -tags with_all -o bin/glua ./cmd/glua
~~~

已编译的能力仍可在运行时选择：

~~~bash
glua --glua-syntax=lua53 script.lua
glua --glua-syntax=extended script.glua
glua --glua-syntax=continue,switch,const script.glua
glua --glua-disable-syntax=switch script.glua
~~~

`gluac` 使用对应的 `--gluac-syntax` 和 `--gluac-disable-syntax`。

## continue

`continue` 跳过最近一层循环当前轮剩余语句。

~~~glua
for value = 1, 10 do
  if value % 2 == 0 then
    continue
  end
  print(value)
end
~~~

支持 `while`、`repeat-until`、数值 `for` 和泛型 `for`。编译器会生成现有 `JMP`、`FORLOOP` 或 `TFORCALL` 路径，并正确关闭离开作用域的 upvalue。

## switch / case / default

`switch` 表达式只求值一次，每个 `case` 可以包含多个候选值，默认不贯穿执行：

~~~glua
local status = 201

switch status do
case 200
  print("ok")
case 201, 202
  print("accepted")
case 400, 404
  print("client error")
default
  print("other")
end
~~~

规则：

- 匹配使用 Lua `==` 语义。
- `case` 按源码顺序求值。
- `default` 最多一个且位于最后。
- 分支后不需要 `break`，命中后自动离开 `switch`。
- 循环中的 `switch` 可以使用 `continue` 继续最近循环。

## const

`const` 声明当前词法作用域内的只读绑定，必须立即初始化：

~~~glua
const apiVersion = "v1"
const minPort, maxPort = 1024, 65535

print(apiVersion, minPort, maxPort)

-- 编译错误：不能重新赋值
-- apiVersion = "v2"
~~~

`const a, b = 1, 2` 与多变量 `local` 初始化保持相同的值补齐和截断规则，但生成的绑定不可再次赋值，也不能被内层函数作为可写 upvalue 修改。

### 导出模块常量

词法 `const` 不会自动成为模块字段。模块通过 `_glua_const` 表声明只读导出：

~~~glua
-- protocol.glua
local protocol = {
  mutableRetries = 3,
}

protocol._glua_const = {
  VERSION = "1.0.0",
  DEFAULT_PORT = 8080,
}

return protocol
~~~

调用方直接从模块根表读取：

~~~glua
local protocol = require("protocol")

print(protocol.VERSION)
print(protocol.DEFAULT_PORT)

protocol.mutableRetries = 5

-- 运行时错误，PLS/编辑器静态扫描也会报告：
-- protocol.VERSION = "2.0.0"
~~~

`_glua_const` 中的字段会投影到返回 table 根级并变为只读；普通字段仍可修改。不要同时定义同名普通字段和常量字段。

## 编辑器支持

VSCode 与 JetBrains 扩展支持：

- `.glua` / `.lua` 扩展语法高亮。
- 格式化。
- `const` 重复赋值和 `_glua_const` 只读写入诊断。
- `switch/case/default`、`continue` 的语法扫描。
- 模块常量、变量、函数的补全与定义跳转。

完整控制流语义和编译策略见 [控制流扩展设计](CONTROL_FLOW_EXTENSIONS.md)。
