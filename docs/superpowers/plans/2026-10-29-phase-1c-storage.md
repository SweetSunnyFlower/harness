# Phase 1C Storage Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付 `harness/storage`：Storage 契约接口、Memory 参考后端与统一后端契约测试，作为 SQLite/JSONL（1D/1E）的共同验收基线。

**Architecture:** `Storage` 以 `context.Context` 在前、显式错误、类型化 ID/record/delta 类型定义原子提交、ID 分配、分页扫描、条目可见性、head 标记、提交去重与文档物化。Memory 忠实移植固定 TS 基线 `packages/durable/src/storage/memory.ts`，返回值和保留写入均深拷贝以模拟序列化后端的所有权边界。

**Tech Stack:** Go 1.25.0+ 标准库；消费 `harness/protocol`、`harness/record`、`harness/delta`。

## Global Constraints

- 完整移植 pi-durable，Go API，运行时不依赖 Node.js；上游基线 `e65b3b88`。
- module `harness`，go 1.25.0，不加第三方依赖。
- 字段名、排序、游标、fork 可见性与文档历史语义与 TypeScript 一致；缺失与 null 区分，未知内容不丢。
- Storage 不执行任务、不调度；只持久化记录与文档，非法请求返回 `StorageRequestError` 而非终止 Session（该区分由 1F/后续 Session 层处理）。

## 文件与接口

| 文件 | 内容 |
|---|---|
| `storage/storage.go` | Cursor/Page/ScanOrder、查询、DocumentContent/StoredDocument/Address/Point、StorageWrite、Storage 接口、StorageRequestError |
| `storage/content.go` | DocumentContent 的 JSON 编解码（base value 与 decoded op 列表） |
| `storage/memory.go` | Memory 参考实现 |
| `storage/memory_test.go` | 后端契约测试：原子提交、ID 命名空间、扫描/游标、条目可见性与 head、请求去重、文档生命周期/物化/复制/回收 |

## 完成标准

- `go test ./... -count=1`、`go vet ./...`、`go test -race ./... -count=1`、`CGO_ENABLED=0 go test ./... -count=1` 全部通过。
- 契约测试覆盖 spec §10 与 §11.1 Memory 的全部语义；同一测试函数以共享 runner 形式编写，供 1D/1E 后端复用。
- 文档物化用 `delta.ApplyImmutableBatches`；current-only 与 rewindable 的历史/退休/回收行为与 TS 对齐。

完成后进入 1D SQLite 与 1E JSONL 后端。
