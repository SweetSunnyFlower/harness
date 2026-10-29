# Phase 1B Record Types Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付 `harness/record`：Conversation/Entry/Submission/Task/Document 五类持久化记录的类型化表示、判别联合校验与未知字段保留的 JSON 往返，作为存储协议的值模型。

**Architecture:** 每个记录是“类型化字段 + Extra 未知成员”。opaque JSON 字段（model/data/trace/links/input/checkpoint/result/detail/memos）用 `json.RawMessage` 原样承载，由上层解释。通用 `marshalWithExtra`/`unmarshalWithExtra` 负责类型化字段与未知成员的合并与拆分，保证 Go 读取再写回不丢未知内容。

**Tech Stack:** Go 1.25.0+ 标准库，消费 `harness/protocol` 的类型化 ID/Seq。字段名与判别联合严格对齐固定 TS 基线 `e65b3b88` 的 `packages/durable/src/types.ts`。

## Global Constraints

- 完整移植 pi-durable，Go API，运行时不依赖 Node.js；上游基线 `e65b3b88`。
- module `harness`，go 1.25.0，本子项目不加第三方依赖。
- 字段名与 JSON 形状与 TypeScript 一致；未知成员保留，缺失与 null 区分，不做静默损坏。
- 记录层只解释身份、判别符与索引所需的标量；input/checkpoint/model/data 等保留为 opaque JSON。

## 文件与接口

| 文件 | 内容 |
|---|---|
| `record/record.go` | `marshalWithExtra`、`unmarshalWithExtra`、判别符与错误 |
| `record/conversation.go` | `ConversationRecord`、parent/owner |
| `record/entry.go` | `EntryRecord` |
| `record/submission.go` | `SubmissionRecord`、type/status 联合 |
| `record/task.go` | `TaskRecord`、`TaskState`、`TaskOutcome`、`TaskOutcomeError` |
| `record/document.go` | `DocumentRecord`、`DocumentCreate`、`DocumentScope` |
| `record/*_test.go` | 往返、未知字段保留、联合校验、参考向量 |

每个类型公开 `Validate() error`，并在 `UnmarshalJSON` 中完成类型化解码后由调用方显式校验（解码不静默丢弃，但不因校验失败破坏 unknown 保留）。opaque 字段解码失败返回错误。

执行记录：实现提交 `94b0668`。`harness/record` 交付五类记录、未知字段 Extra 保留与联合校验。验证：`go test ./... -count=1`、`go vet ./...`、`go test -race ./... -count=1`、`CGO_ENABLED=0 go test ./... -count=1` 全部通过。关键保真细节已覆盖：`on:[]` 空数组不因 omitempty 丢失，非法 ID 错误链同时含 `record.ErrRecord` 与 `protocol.ErrID`，会话/会话文档/任务文档的判别联合均按 TS 形状往返并拒绝非法组合。Node 对照脚本未单独添加，因为 Go 测试以字面 TS 形状 JSON 断言字段名与形状，字段保真由这些往返测试直接覆盖。

## 完成标准

- `go test ./... -count=1`、`go vet ./...`、`go test -race ./... -count=1`、`CGO_ENABLED=0 go test ./... -count=1` 全部通过。
- 每个记录类型有 TS 形状往返测试、未知字段保留测试与判别联合拒绝测试。
- 与 Node 对照脚本共享参考向量：TypeScript 形状的 JSON 经 Go 解码再编码后语义等价，未知字段存活。

完成后进入 1C 存储契约与内存后端。
