# GLua 扩展方法示例

先构建纯 Go CLI：

~~~bash
CGO_ENABLED=0 go build -o ./bin/glua ./cmd/glua
~~~

运行序列化示例：

~~~bash
./bin/glua ./examples/extensions/serialization.glua
~~~

运行通用工具示例：

~~~bash
./bin/glua ./examples/extensions/utilities.glua
~~~

这些 API 由 `lua.OpenLibs` 注册到全局 `glua` table。序列化与通用工具保持纯 Go，不访问网络；`glua.path` 只做词法路径运算，`glua.zip` 只处理内存中的文件映射。
