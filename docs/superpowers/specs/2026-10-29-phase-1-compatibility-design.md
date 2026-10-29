# 第一阶段：兼容基础详细边界

日期：2026-10-29。依据已批准的 [总设计](2026-10-29-go-durable-harness-design.md)。

此文档展开总设计第一阶段，不改变功能范围。第一阶段由六个依次衔接的子项目组成；当前完整可执行计划覆盖 1A。后续子项目在其接口设计完成后分别编写执行计划，不以本文件的交付顺序代替实现步骤。

## 1. 子项目与依赖

| 子项目 | 可独立验收的产物 | 前置条件 | 退出条件 |
|---|---|---|---|
| 1A 数据边界 | 无损 JSON、UTF-16 字符串、安全整数 ID、保留未知字段的记录封装 | Go 工具链 | Go 测试与 JavaScript 语义夹具均通过 |
| 1B 记录与增量协议 | 完整记录类型、Op/WireOp、JSON 对象编辑、增量应用与编码 | 1A | 上游增量操作参考测试及记录序列化往返通过 |
| 1C 存储契约与内存实现 | 完整 Storage API、Memory、历史物化、分页与共享契约测试 | 1B | 原子提交、隔离、所有扫描与文档读取契约通过 |
| 1D SQLite | 相同 schema、迁移和索引的 SQLite 后端 | 1C | 契约、迁移、重开、查询计划与 TS 双向读写通过 |
| 1E JSONL | 主日志、sidecar、故障恢复、回收与文件系统适配 | 1C | 契约、文件故障注入与 TS 双向读写通过 |
| 1F 综合验证 | 统一后端运行器、来源清单、兼容矩阵及基准 | 1D、1E | 总设计第一阶段出口通过，并记录所有差异 |

任务调度和未完成 Agent 任务的接续属于总设计第二、三阶段。第一阶段只能承诺记录、文档与存储操作的兼容，不把读到 sleeping 记录等同于已能唤醒任务。

## 2. 基线与工具链

- 上游提交固定为 `e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a`。
- 本地 Go 工具链已检测为 `go1.25.6 darwin/arm64`，Node 为 `v22.23.4`。
- 1A 采用 `go 1.25.0`，只使用标准库。Node 仅用于开发参考测试，不加入 Go 运行依赖。
- 采用本地 module 名 `harness`；这是本仓库当前有效的导入路径，不预设用户的远程仓库。发布前单独确认远程路径并机械调整导入。
- 1D 的数据库驱动在其详细计划中锁定，必须满足 Go 运行不依赖 Node、SQLite STRICT/JSON/WAL 能力及基线 schema；数据库驱动不进入协议和内存实现包。

## 3. 1A 的确定接口

第一子项目使用三层：

1. `jsonvalue`：不可变 JSON 文本值，语法检查、有限数字检查、类型查询、ASCII 协议字段提取、UTF-16 字符串转换。
2. `protocol`：区分记录种类的 ID 和 Seq，范围限制及 JSON 编码。
3. `internal/record`：内部 Envelope，提取公共 ID，同时完整保留未知内容。它不等于最终的 ConversationRecord 或 TaskRecord。

`jsonvalue.Value` 用私有字符串保留有效 JSON 文本，零值表示 JSON null；Bytes 返回新分配的切片。输入、输出切片的修改均不能改变已保存的值。压缩空白允许，但不经 `map[string]any` 重编码整个对象。

Parse 接受一个 UTF-8 JSON 值，拒绝空输入、尾随第二个值、损坏语法、原始无效 UTF-8，以及转换为 IEEE-754 后非有限的数字。保留数字词法文本；Number 返回 JavaScript number 对应的 float64 解释。一般数据里的有限数字可以超出安全整数范围，ID 不可以。

ASCII 字段查询只用于已知协议键，键名大小写敏感。以 json.RawMessage 读取成员，返回 `(Value, found, error)`，区分缺失和显式 null。它不作为任意 Unicode 用户键的通用编辑器；任意键的对象模型属于 1B。

JavaScript 字符串包括合法的孤立 UTF-16 代理项。StringUnits 返回 UTF-16 code units 的新切片，StringFromUnits 通过 JSON Unicode 转义构造值，不经 Go rune 把孤立代理项替换为 U+FFFD。

类型化 ID 使用泛型 `ID[K]` 与封闭的种类别名。记录 ID 和提交 Seq 的有效范围为 `[1, 9007199254740991]`。root conversation 固定为 1。0 是未初始化状态，不能成功编码为一个有效 ID。可选 ID 通过字段缺失表示，不用 0 替代缺失。

JSON 数字形式的 `1`、`1.0`、`1e0` 具有相同 ID 语义；字符串 `"1"`、null、布尔值、负数、小数、0 和不安全整数拒绝。Unmarshal 失败时保留接收者原值。

Envelope 只检查对象形状和公共 ID，保留未知 kind/version/state 字段。完整记录必填项与联合状态检查由 1B 完成；不能调用 Envelope 就宣称记录语义已有效。

## 4. 后续记录及增量的约束

1B 的记录结构来源于上游 `packages/durable/src/types.ts`，至少包括：

| 记录 | 公共和特有字段 |
|---|---|
| Conversation | id、parent(conversationId/at)、owner(conversationId/taskId) |
| Entry | id、conversationId、kind、model、data、head、edits、byTaskId |
| Submission | id、conversationId、requestId、trace、type、status，以及状态允许的 entry/answer/reason/detail |
| Task | 基线完整 TaskRecord 字段、所有权、定义版本、input、state、abort/background 与生命周期字段 |
| Document | 基线完整 DocumentRecord/DocumentCreate、scope、kind、key、history/fork、创建与退休序号 |

保留未知字段的原则延续 Envelope；结构化修改已知字段不能丢弃其他内容。不把 Go 的缺省空值直接写成上游不允许的字段。

Chord 的七种 decoded 操作全部覆盖：r/s/d/a/t/p/m。特别注意 `t` 表示 `current.slice(n)`，按 UTF-16 code units 移除前缀，而非把字符串截到长度 n；数组 delete 会移除元素并保持稠密。WireOp 另支持路径字典 `#`、路径 ID 和省略重复路径。decoded Op 与 WireOp 的校验不能混用。

不接受不安全路径、无效排列或未知操作并静默跳过。对象属性名、数组下标、缺失属性和 null 各有不同语义。跨版本操作或数据无法应用时保留证据并报错，不当作空文档。

## 5. Storage API 的边界

1C 的接口完整对应固定规范第 10 节，不以一个通用 Scan 替代不同查询的约束。Go 签名使用 context.Context 在首位、显式错误、类型化 ID、可选结果和带游标 Page。

接口必须包括：Commit、MintID、Conversation/ScanConversations、全局与会话可见 Entry 查询、FindLatestHeadMarker、ScanEntries、Task/ScanTasks、Submission/ScanSubmissions/SubmissionByRequest、FindDocument/Document/ScanDocuments、Close。

Commit 接收 writes 与同步 adopt 回调，回调不得调用 Storage 或抛出错误；成功的批次仅采纳一次。调用方 Session 串行化提交，但后端仍保证批次原子性。ID 分配跨记录种类共用一个命名空间。

Write 联合体完整包含 conversation、entry、task、submission、document.create/copy/change/retire。copy 读取提交前已存在的源文档；内容写入先于同批退休，退休和同地址新建遵守生命周期边界。

统一查询测试覆盖排序、游标顺序不匹配、祖先继承上界、当前/历史物化、地址更替、版本边界和所有权过滤。存储语义与调度语义分离：写入合法的任务记录不等于后端负责执行任务。

## 6. SQLite 与 JSONL 的明确协议

SQLite 元数据表包括 durable_schema、durable_metadata；next_id 为 TEXT，next_seq 为整数。业务表及索引名称来自基线 schema.ts，不自创替代布局。支持前缀校验；从版本 1 升到 2 在一个事务中重建 tasks CHECK 约束以支持 sleeping，并保留记录和索引。拒绝高于版本 2 的数据库，迁移失败回滚。

SQLite 不通过“启动时加载全表到 Memory，关闭时全量写回”模拟数据库：文档 base+tail、任务筛选、分页与历史访问使用对应索引。

JSONL format 为 1，main.jsonl 保存完整提交标记；doc-ID.jsonl 与 task-ID.jsonl 保存带 seq/ordinal 的记录。写 sidecar 后写 main marker，再采纳内存状态。打开时只采用已确认记录，处理撕裂尾行和未确认尾部，缺失必要确认数据报损坏。

JSONL 回收在授权提交之后执行；临时替换、rename、已打开句柄失效及 fsync 模式遵守基线。不得用“每次提交重写整个目录”替代这一协议。普通进程崩溃与断电保证分别验证。

## 7. 兼容矩阵与验收层级

1A 的 JavaScript 对照仅验证 JSON 数字、字段存在性和字符串 code unit 语义，不是完整 pi-durable 存储互操作测试。

1D/1E 必须运行固定上游实现，产生真实 SQLite/JSONL 数据，并覆盖 TS→Go→TS 与 Go→TS→Go。比较公共状态、未知字段、历史、版本与合法后续提交；不要求 JSON 空白或对象键顺序逐字节相同。

1F 矩阵至少区分以下状态：计划中、已实现未验证、通过、发现差异、环境未具备。每个通过项目附命令、基线、测试宿主和输出摘要。源代码编译通过不能替代契约测试，只有 fixture 能读不能替代双向修改测试。

1A 的完成只允许进入 1B；三后端完整兼容必须等 1F 出口，不把其余子项目遗漏为可选项。
