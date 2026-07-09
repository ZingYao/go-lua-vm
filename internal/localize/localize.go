// Package localize 提供命令行输出使用的轻量语言选择。
package localize

import (
	"os"
	"strings"
)

// Chinese 返回当前 CLI 输出是否应使用中文。
func Chinese() bool {
	// GLUA_LANG 优先级最高，方便测试、脚本和用户临时覆盖系统语言。
	for _, name := range []string{"GLUA_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
		if value == "" {
			// 空环境变量不能表达语言偏好，继续看下一个。
			continue
		}
		if strings.HasPrefix(value, "zh") {
			// zh、zh_CN、zh-CN、zh-Hans 等都走中文输出。
			return true
		}
		if strings.HasPrefix(value, "en") || value == "c" || value == "posix" {
			// 明确英文或 POSIX locale 时保持英文输出。
			return false
		}
	}
	return false
}

// Text 根据当前语言偏好返回英文或中文文本。
func Text(en string, zh string) string {
	// 中文环境返回中文，否则返回英文。
	if Chinese() {
		return zh
	}
	return en
}
