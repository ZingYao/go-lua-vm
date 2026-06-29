// Package lua 提供 go-lua-vm 的对外嵌入 API 边界。
//
// 本包后续只暴露稳定的 Lua 5.3 VM 嵌入能力，包括 State 生命周期、
// 脚本加载、函数调用、标准库加载、Go 函数注册以及 Go/Lua 双向回调。
// 内部运行时细节必须保留在 runtime、bytecode、compiler、stdlib、debug
// 和 bridge 等包中，避免调用方依赖尚未稳定的实现结构。
package lua
