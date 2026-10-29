# Port Roadmap

固定 TS 基线 `e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a`。工作分支 `feat/record-delta`，`main` 保持已合并 1A 的 `d2017d5`。

| 阶段 | 内容 | 状态 |
|---|---|---|
| 1A 数据边界 | jsonvalue/protocol/internal record | ✅ 已合并 main |
| 1B delta | harness/delta（Op/WireOp/apply/codec/diff） | ✅ 提交 1ca5630 |
| 1B 记录 | harness/record（五类记录 + unknown 保留） | ✅ 提交 94b0668 |
| 1C 存储 | harness/storage（契约 + Memory） | ✅ 提交 195726f |
| 1D SQLite | schema v2 迁移、全部查询、文档 base/delta | ✅ 契约+迁移测试通过；TS 文件级互操作属 1F |
| 1E JSONL | main/sidecar、恢复、回收、fsync | 🟡 契约+重开通过；回收/fsync 未实现 |
| 1G MySQL | 新增后端（上游无 MySQL，满足同一契约） | 🟡 已实现并编译；未对真实服务器验证 |
| 1F 综合 | 统一 runner、来源清单、兼容矩阵、基准 | 待办 |
| 2 持久化内核 | Session、事务、fork、任务树、调度、取消、恢复 | 待办 |
| 3 Agent 运行时 | 模型、输入队列、生成/工具循环、压缩、事件、用量 | 待办 |
| 4 完整扩展 | Registry、Hooks、热更新、嵌套工具、内置工具、搜索、图像、遥测 | 待办 |
| 5 Code Mode 与平台 | Go WASM、脚本重放、平台后端 | 待办 |

验证口径：每个阶段 `go test ./... -count=1`、`go vet ./...`、`go test -race ./... -count=1`、`CGO_ENABLED=0 go test ./... -count=1` 全绿才标完成；跨语言项另跑 Node 对照。终端目标：完整能力清单验收，不跳过平台适配或 Code Mode。

## 1D 完成说明

- 全部扫描已实现游标分页；“同地址双当前”检查经文档地址列查询；“rewindable base 追加、current-only 替换”已修正；schema v1→v2 迁移有独立测试。
- 尚未完成项仅剩 TS↔Go 的 SQLite 文件级双向互操作验证，属 1F 综合验证。

## 1E 完成说明

- 实现 format v1 main.jsonl 提交标记 + task/doc sidecar、重开恢复与撕裂尾行处理，通过共享契约与重开测试。
- 已实现 fsync 模式（受影响 sidecar 先落盘再写 main 标记，含重开测试）；尚未实现 sidecar 回收（reclaim）；TS↔Go 文件级互操作属 1F。

## 1G 完成说明

- `harness/storage/mysql` 已实现 Storage 接口（go-sql-driver/mysql，纯 Go），DDL 与查询对齐 SQLite 语义，适配 MySQL 方言（REPLACE INTO/INSERT IGNORE、无 STRICT/CHECK/partial index、LONGTEXT 存 JSON）。
- 契约测试通过 `MYSQL_DSN` 环境变量 opt-in；本机无 MySQL 服务，未对真实服务器运行，故不标为完成。待有服务器时设 DSN 跑 `go test ./storage/mysql -count=1`。
