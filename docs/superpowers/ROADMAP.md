# Port Roadmap

固定 TS 基线 `e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a`。工作分支 `feat/record-delta`，`main` 保持已合并 1A 的 `d2017d5`。

| 阶段 | 内容 | 状态 |
|---|---|---|
| 1A 数据边界 | jsonvalue/protocol/internal record | ✅ 已合并 main |
| 1B delta | harness/delta（Op/WireOp/apply/codec/diff） | ✅ 提交 1ca5630 |
| 1B 记录 | harness/record（五类记录 + unknown 保留） | ✅ 提交 94b0668 |
| 1C 存储 | harness/storage（契约 + Memory） | ✅ 提交 195726f |
| 1D SQLite | schema v2 迁移、全部查询、文档 base/delta | 🔨 进行中 |
| 1E JSONL | main/sidecar、恢复、回收、fsync | 待办 |
| 1G MySQL | 新增后端（上游无 MySQL，满足同一契约） | 待办 |
| 1F 综合 | 统一 runner、来源清单、兼容矩阵、基准 | 待办 |
| 2 持久化内核 | Session、事务、fork、任务树、调度、取消、恢复 | 待办 |
| 3 Agent 运行时 | 模型、输入队列、生成/工具循环、压缩、事件、用量 | 待办 |
| 4 完整扩展 | Registry、Hooks、热更新、嵌套工具、内置工具、搜索、图像、遥测 | 待办 |
| 5 Code Mode 与平台 | Go WASM、脚本重放、平台后端 | 待办 |

验证口径：每个阶段 `go test ./... -count=1`、`go vet ./...`、`go test -race ./... -count=1`、`CGO_ENABLED=0 go test ./... -count=1` 全绿才标完成；跨语言项另跑 Node 对照。终端目标：完整能力清单验收，不跳过平台适配或 Code Mode。
