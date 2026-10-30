# Port Roadmap

固定 TS 基线 `e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a`。工作分支 `feat/record-delta`，`main` 保持已合并 1A 的 `d2017d5`。

| 阶段 | 内容 | 状态 |
|---|---|---|
| 1A 数据边界 | jsonvalue/protocol/internal record | ✅ 已合并 main |
| 1B delta | harness/delta（Op/WireOp/apply/codec/diff） | ✅ 提交 1ca5630 |
| 1B 记录 | harness/record（五类记录 + unknown 保留） | ✅ 提交 94b0668 |
| 1C 存储 | harness/storage（契约 + Memory） | ✅ 提交 195726f |
| 1D SQLite | schema v2 迁移、全部查询、文档 base/delta | ✅ 契约+迁移测试通过；TS 文件级互操作属 1F |
| 1E JSONL | main/sidecar、恢复、回收、fsync | ✅ 契约/重开/回收/fsync 通过；TS 互操作属 1F |
| 1G MySQL | 新增后端（上游无 MySQL，满足同一契约） | 🟡 已实现并编译；未对真实服务器验证 |
| 1F 综合 | 统一 runner、来源清单、兼容矩阵、基准 | 🟡 SQLite/JSONL 双向互操作均已验证；矩阵/基准待办 |
| 2 持久化内核 | Session、事务、fork、任务树、调度、取消、恢复 | ✅ Session/Tx/Fork/EditDocument、task 状态机、Scheduler、Recover、Abort |
| 3 Agent 运行时 | 模型、输入队列、生成/工具循环、压缩、事件、用量 | ✅ agent 生成循环、harness Prompt/Submit/Compact、events/usage |
| 4 完整扩展 | Registry、Hooks、内置工具、搜索、图像、遥测 | ✅ Registry/Hooks/ToolTask、Read/Write/Bash/Edit/Search/图像检测、telemetry |
| 5 Code Mode 与平台 | QuickJS 沙箱、prelude、脚本重放、平台后端 | ✅ CodemodeSandbox（prelude/tools/globals/store/output/calls/errors/timeout/stalled/确定性重放） |

验证口径：每个阶段 `go test ./... -count=1`、`go vet ./...`、`go test -race ./... -count=1`、`CGO_ENABLED=0 go test ./... -count=1` 全绿才标完成；跨语言项另跑 Node 对照。终端目标：完整能力清单验收，不跳过平台适配或 Code Mode。

## Phase 2 进行中

- `harness/session`：Session 对 Storage 串行化原子提交，Tx 在回调内累积写入；回调失败即回滚，MintID 与 Commit 均受同一把锁保护。并发提交测试 race-clean。
- 尚未实现：文档草稿编辑、会话创建/分叉与所有权、任务树、调度、取消、恢复。

## Code Mode 完成说明

- `harness/codemode` 移植了 pi-codemode 的 `PRELUDE_SOURCE`（经 esbuild 求值后以 `//go:embed prelude.js` 内嵌），并在 wazero 上实现 QuickJS-WASI 沙箱：`tools.<name>`、`ALL_TOOLS`、`text`/`image`/`exit`/`console`、`store`/`load`、globals、错误与超时、`stalled` 检测、promise job 泵（`qjs_is_job_pending`/`qjs_execute_pending_job`）。
- `CodemodeSandbox` 支持工具与全局函数、调用记录、输出项、store 写入回传、脚本/语法错误、`@options` 解析、`ToCodemodeIdentifier`，以及 `Seed`/`StartedAt` 驱动的确定性重放。
- 平台后端：SQLite/JSONL/Memory/MySQL 均实现同一 Storage 契约；上游面向浏览器/Cloudflare 的 IndexedDB、Durable Objects、Chord Store 适配属 JS 平台绑定，Go 库无需移植。

## 剩余已知项

- MySQL 后端未对真实服务器做 live 验证（本机无 MySQL 服务；`MYSQL_DSN` 可 opt-in 跑契约测试）。
- pi-durable 中与 `pi-ai`/`chord` 深度耦合的 typed harness API（Inbox/View/Live/Observation/完整遥测 schema/provider 集成）未逐行移植；Go 侧以更小的 `agent`/`harness`/`task`/`telemetry` API 覆盖核心语义。需要时可按同样契约继续扩展。

## 1D 完成说明

- 全部扫描已实现游标分页；“同地址双当前”检查经文档地址列查询；“rewindable base 追加、current-only 替换”已修正；schema v1→v2 迁移有独立测试。
- 尚未完成项仅剩 TS↔Go 的 SQLite 文件级双向互操作验证，属 1F 综合验证。

## 1E 完成说明

- 实现 format v1 main.jsonl 提交标记 + task/doc sidecar、重开恢复与撕裂尾行处理，通过共享契约与重开测试。
- 已实现 fsync、跨语言 sidecar 引用格式，以及 sidecar 回收：退休的 current-only 文档与已终止任务对应的 sidecar 在提交后被移除，恢复采用两遍扫描容忍已回收记录；TS↔Go 文件级互操作属 1F。

## 1G 完成说明

- `harness/storage/mysql` 已实现 Storage 接口（go-sql-driver/mysql，纯 Go），DDL 与查询对齐 SQLite 语义，适配 MySQL 方言（REPLACE INTO/INSERT IGNORE、无 STRICT/CHECK/partial index、LONGTEXT 存 JSON）。
- 契约测试通过 `MYSQL_DSN` 环境变量 opt-in；本机无 MySQL 服务，未对真实服务器运行，故不标为完成。待有服务器时设 DSN 跑 `go test ./storage/mysql -count=1`。

## 1F 进行中

- 双向 SQLite 互操作已验证：TS pi-durable（npm 1.4.0）写出的 SQLite 由 Go 读取并物化文档 delta；Go 写出的 SQLite 由 TS pi-durable 读取并物化到相同值。过程中修正了 content 列格式（base 存值、delta 存操作数组，kind/version 在列里）。
- 双向 JSONL 互操作已验证：TS pi-durable 写出的 JSONL 由 Go 读取并物化；Go 写出的 JSONL 由 TS pi-durable 读取并物化到相同值。
- `tools/compat/check-interop.mjs` 另以 node:sqlite + 独立 JS 重放核对 JSONL 格式。
- 尚未做：完整兼容矩阵与基准；MySQL 待真实服务器。
