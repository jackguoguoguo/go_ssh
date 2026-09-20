# sshtool 多主机批量执行

- 关联版本：v0.4 扩展批次（推荐扩展 P0-1）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

运维最常用的场景是「在 N 台主机上跑同一条命令并对比结果」。现有交互模式（逐个连接、广播）无法收集并汇总输出。
本功能提供：

- **多目标选择**：从保存的连接里多选（空格勾选 / 关键字过滤），或直接以 `batch <关键词>` 过滤匹配连接。
- **并发执行**：对每台目标建立一次性 SSH 连接执行同一命令，不占用交互会话、不申请 PTY，执行完即断开。
- **结果汇总**：按主机列出 stdout / 退出码，失败（认证失败 / 网络不通 / 超时）单独标注，结果页一目了然。

## 2. 设计要点

### 2.1 执行内核（`internal/remotessh/batch.go`）
- `Batch(conns, cmd, timeout) []BatchResult`：并发对每台目标调用已有的 `RunOnce`（一次连接、无 PTY、执行完断开），
  收集 `Stdout / Stderr / Code / Err`。`results[i]` 与 `conns[i]` 下标一一对应，UI 层负责排序展示。
- `BatchResult` 自描述：`Name`（连接名）、`Host`（user@host:port）、`Code`（-1 表示未执行成功）、`Err`（失败原因）。
- `secretForBatch`：复用连接已保存的密码 / 私钥口令（与 UI `secretOf` 语义一致，但放在 remotessh 层便于无 UI 复用——是后续 CLI 的基础）。
- `BatchFailed`：统计失败数。

### 2.2 UI 接入（`internal/ui/batch.go`）
- 命令行元命令 `batch`（`runMetaCommand`）：
  - 无参数：打开**多选目标对话框**（复用 `dlgPick.multi`，与密钥推送一致）。
  - 带参数：把参数当作连接过滤词（名称/主机/用户/分组），直接进入命令输入对话框，跳过选择。
- 命令输入对话框：预填上一次命令（`m.lastBatchCmd`）便于重复执行；回车提交。
- `runBatch`：危险命令（`risky` 判定）先二次确认（与广播一致，`strictConfirm`）。
- `launchBatch`：记录历史（标注「批量(N 台)」）→ 弹出「正在执行…」对话框 → `m.afterCmd = batchCmd(...)`。
- `batchCmd` 后台并发执行（复用 `sync.WaitGroup`）→ 返回 `batchDoneMsg`。
- `showBatchResult`：结果对话框（`dlgText`）——汇总「X 台成功 · Y 台失败」+ 逐台输出裁剪展示
  （每台最多 10 行、每行 100 字符，防止海量输出撑爆界面）；状态栏给出摘要。

### 2.3 安全
- 危险命令（`rm -rf /` 等）在批量上下文同样二次确认。
- 批量执行**不会**自动降级：认证失败即记为失败，绝不静默跳过。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| E1 | 批量执行内核 | `internal/remotessh/batch.go` |
| E2 | UI：元命令 / 目标选择 / 命令输入 / 后台执行 / 结果页 | `internal/ui/batch.go` |
| E3 | 元命令分发与消息处理 | `internal/ui/sshconfig.go`、`internal/ui/model.go`（`batchDoneMsg`） |
| E4 | 帮助与文档 | `internal/ui/dialogs.go`、`README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 两台测试服务器并发执行 `echo hello-batch`，均收集到输出且 Code=0 | `remotessh/batch_test.go::TestBatchExecutesAcrossHosts` 通过 |
| T2 | 错误密码连接 → 结果为失败且无输出、Code=-1 | `TestBatchFailureCaptured` 通过 |
| T3 | 不可达主机（127.0.0.1:1）→ 快速返回错误而非挂死 | `TestBatchUnreachableTimeout` 通过 |
| T4 | `batch` 无连接 → 提示，不弹对话框 | `ui/batch_test.go::TestBatchMetaNoConnections` 通过 |
| T5 | `batch web` 过滤 → 直接进入命令输入对话框且标题显示台数 | `TestBatchMetaWithConnections` 通过 |
| T6 | 命令输入预填上次命令 | `TestBatchCmdInputPrefilled` 通过 |
| T7 | 结果对话框汇总统计与状态栏摘要正确 | `TestShowBatchResult` 通过 |
| T8 | 长输出裁剪有界 | `TestTrimmedOutput` 通过 |
| T9 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 单条命令超时默认 60s；`RunOnce` 不经过 ProxyJump（与既有 `RunOnce` 行为一致）。
- 结果页目前为**只读展示**，未支持「把结果导出到文件 / 复制某台完整输出」——后续可加导出（也即 README 中建议的 CLI 子命令的雏形）。
- 本实现是推荐扩展 P0-1，执行内核可被后续「健康巡检（P0-2）」「headless CLI（P2-6）」直接复用。