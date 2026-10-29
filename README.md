# Go Durable Harness

面向 pi-durable 完整能力移植的 Go 包，运行时不依赖 Node.js。
上游参考提交为 e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a。

当前交付为阶段 1A 数据边界：

- jsonvalue：不可变 JSON、缺失字段与 null 的区分、UTF-16 字符串往返。
- protocol：类型化 ID/Seq 与 JavaScript 安全整数限制。
- internal/record：保留未知字段和状态的内部记录封装。

此阶段尚未实现 Storage、持久化任务或 Agent 执行；不能用于恢复
pi-durable 会话。完整交付顺序见 docs/superpowers/specs。

## 使用

本地 module 名为 harness，要求 Go 1.25.0 或更新版本。

```go
package main

import (
	"fmt"

	"harness/jsonvalue"
)

func main() {
	value, err := jsonvalue.Parse([]byte(`{"text":"\ud800"}`))
	if err != nil {
		panic(err)
	}
	text, found, err := value.FieldASCII("text")
	if err != nil || !found {
		panic("text missing")
	}
	units, err := text.StringUnits()
	if err != nil {
		panic(err)
	}
	fmt.Printf("%04x\n", units)
}
```

FieldASCII 只用于已知 ASCII 协议键。Envelope 只检查对象和 ID，
不代表任务记录的所有状态字段已经有效。

## 验证

```sh
go test ./... -count=1
go vet ./...
CGO_ENABLED=0 go test ./... -count=1
node tools/compat/check-json.mjs
```

最后一条命令是开发期 JavaScript 语义对照，需要 Node.js 22.19+。
Go 包的运行和 Go 单元测试均不调用 Node.js。
数据语义夹具通过不等于 SQLite/JSONL 已完成跨语言互操作。
