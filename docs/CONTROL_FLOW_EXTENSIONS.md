# 控制流语法扩展设计

本文档记录本项目在 Lua 5.3 基线之上新增 `continue` 与 `switch/case/default` 的设计。该扩展只改 lexer/parser/codegen 层，不新增 VM opcode，最终仍生成标准 Lua 5.3 VM 指令。

## continue

`continue` 用于跳过当前循环剩余语句，继续最近一层循环的下一轮。

```lua
for i = 1, 10 do
    if i % 2 == 0 then
        continue
    end
    print(i)
end
```

支持范围：

- `while ... do ... end`
- `repeat ... until ...`
- numeric `for`
- generic `for`

编译策略：

- 不新增 `OP_CONTINUE`。
- `continue` 编译为 `JMP`，目标由最近一层循环决定。
- `while` 跳到条件检查位置。
- `repeat-until` 跳到 `until` 条件求值位置。
- numeric `for` 跳到 `FORLOOP`。
- generic `for` 跳到 `TFORCALL`。

语义约束：

- 循环外出现 `continue` 必须报错。
- 嵌套循环中 `continue` 只作用于最近一层循环。
- `continue` 跳出当前 block 剩余语句时必须正确关闭离开的局部变量和 upvalue。
- `repeat-until` 的条件表达式仍可访问循环体 local，不能因 `continue` 提前破坏该作用域。

## switch/case/default

`switch` 用于表达多分支值匹配。默认不支持 fallthrough。

```lua
switch value do
case 1 then
    print("one")
case 2, 3 then
    print("two or three")
default then
    print("other")
end
```

语义约束：

- `switch` 表达式只求值一次。
- `case` 表达式按源码顺序求值。
- 单个 `case` 可包含多个逗号分隔表达式，任一表达式与 switch 值相等即匹配。
- 匹配使用 Lua `==` 语义。
- `default` 最多出现一次，第一阶段要求放在最后。
- 每个 `case/default` 块不贯穿执行；块结束后自动跳到 `switch` 结束位置。
- `break` 仍表示跳出最近循环，不表示跳出 `switch`。
- 循环内的 `switch` 中可以使用 `continue`，继续最近一层循环。
- `case/default` 不加入全局保留字表，普通 Lua 代码中仍可作为变量名；但在 `switch` 分支块的语句起点会被当作分支边界，不能无歧义地写成 `case = ...` 或 `default = ...`。

编译策略：

- 不新增 `OP_SWITCH`、`OP_CASE` 或 `OP_CONTINUE`。
- `switch` 表达式先写入一个临时寄存器。
- 每个 `case` 生成一组 `EQ/JMP` 匹配检查。
- 未命中当前 case 时跳到下一 case 检查或 default。
- 命中 case 执行对应 block，block 结束后跳到 switch 结束位置。
- `default` 作为所有 case 都未命中时的兜底 block。

## 测试验收

- parser 能解析 `continue`、`switch`、多值 `case` 和 `default`。
- codegen 不新增 opcode，仅产生现有跳转和比较指令。
- runtime 覆盖四种循环中的 `continue`。
- runtime 覆盖 `switch` 首个 case、后续 case、多值 case、default 和未匹配无 default。
- runtime 覆盖 loop 内 switch + continue。
- parser 拒绝循环外 `continue`、重复 `default`、非末尾 `default`。
