//go:build darwin || linux || freebsd || openbsd || netbsd

package cli

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// makeRawTerminal 将终端输入切换为 raw mode 并返回恢复函数。
//
// file 必须是终端文件描述符；当前实现通过系统 stty 保存并恢复终端状态，不引入 CGO 或额外
// Go module。返回 false 表示当前输入不支持 raw mode，调用方应回退到普通行读取。
func makeRawTerminal(file *os.File) (func(), bool) {
	if file == nil {
		// nil 文件没有可操作的终端描述符。
		return func() {}, false
	}
	saveCommand := exec.Command("stty", "-g")
	saveCommand.Stdin = file
	saveCommand.Stderr = io.Discard
	savedStateBytes, err := saveCommand.Output()
	if err != nil || len(savedStateBytes) == 0 {
		// stty 无法读取状态时说明不是可控终端，保持普通读取。
		return func() {}, false
	}
	savedState := strings.TrimSpace(string(savedStateBytes))
	rawCommand := exec.Command("stty", "-icanon", "-echo", "-isig", "min", "1", "time", "0")
	rawCommand.Stdin = file
	rawCommand.Stdout = io.Discard
	rawCommand.Stderr = io.Discard
	if err := rawCommand.Run(); err != nil {
		// raw mode 切换失败时不启用行编辑。
		return func() {}, false
	}
	return func() {
		// 恢复终端状态失败时无法向用户可靠报告，只能尽力执行。
		restoreCommand := exec.Command("stty", savedState)
		restoreCommand.Stdin = file
		restoreCommand.Stdout = io.Discard
		restoreCommand.Stderr = io.Discard
		_ = restoreCommand.Run()
	}, true
}
