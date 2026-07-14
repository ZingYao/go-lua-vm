package runtime

// gluaEventsCompiled 表示当前构建产物包含 glua 自定义事件能力。
func gluaEventsCompiled() bool {
	// 统一主线始终包含事件能力。
	return true
}

// GluaEventsCompiled 返回当前构建产物是否包含 glua 自定义事件能力。
func GluaEventsCompiled() bool {
	// 对外只暴露只读能力查询，不改变 State 配置。
	return gluaEventsCompiled()
}
