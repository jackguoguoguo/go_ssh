# sshtool 会话快照与恢复

- 关联版本：v0.4 扩展批次（推荐扩展 P1-3）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

自动重连解决的是「连接断了能恢复」，但不解决「**工具重启后标签布局全丢**」。
本功能在退出时记录打开的会话，下次启动询问是否复原——等价于 tmux-resurrect 的会话布局恢复。

## 2. 设计要点

### 2.1 快照存储（`internal/store/sessions.go`）
- 独立文件 `~/.sshtool/sessions.json`（与 `config.json` 同目录），不污染冻结契约 `Store` 结构。
- `SessionSnapshot{open_ids[], active_id, saved_at}`：只记录 **SSH 会话**的连接 ID（按标签顺序）与活动会话；
  本地 shell 无法跨进程复原，故不记录。
- `SaveSessions` 原子写入（临时文件 + rename）；`LoadSessions` 在文件缺失时返回零值、不报错。

### 2.2 退出时保存（`internal/ui/model.go` + `session_snapshot.go`）
- `tea.QuitMsg` 分支：先 `saveSessionSnapshot()`（收集 `m.sessions` 中非本地 shell 的 `TabID()`），
  再 `fwd.StopAll()` 清理端口转发，最后退出。
- 无论 Ctrl+Q 还是其它退出路径，最终都会经过 `QuitMsg`，保证快照被保存。

### 2.3 启动时复原（`restorePrompt`）
- `New()` 末尾调用 `restorePrompt()`：读取快照，若其中有仍存在的连接，弹出确认框询问是否复原。
- **避免连环弹窗**：只复原「可免询问直连」的会话（`restorableSecretAvailable`）：
  - 密码认证：已保存密码且未勾选「每次询问」；
  - 私钥认证：未勾选「每次询问口令」。
  需要输入密码/口令的会话被**跳过**，并在确认框中说明数量，用户可手动连接。
- 确认后按快照顺序 `connectConn` 逐个打开（保持标签顺序），并把快照中的活动会话设为当前。
- 逃生舱：`SSHTOOL_NO_RESTORE=1` 时完全不弹复原询问。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| S1 | 快照持久化（原子写 / 缺省容错） | `internal/store/sessions.go` |
| S2 | UI：保存 / 复原询问 / 过滤需口令的会话 | `internal/ui/session_snapshot.go` |
| S3 | 钩子：退出保存、启动复原 | `internal/ui/model.go` |
| S4 | 文档 | `README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 快照保存/加载往返（ID 顺序、活动会话、时间） | `store/sessions_test.go::TestSessionSnapshotRoundTrip` 通过 |
| T2 | 两个真实会话 → 快照按标签顺序记录且活动会话正确 | `ui/session_snapshot_test.go::TestSaveSessionSnapshot` 通过 |
| T3 | 需要密码的会话在复原时被跳过并在提示中说明 | `TestRestorePromptSkipsSecretNeeding` 通过 |
| T4 | `SSHTOOL_NO_RESTORE=1` 时不弹复原询问 | `TestRestorePromptDisabledByEnv` 通过 |
| T5 | 确认后真实重连会话 | `TestRestorePromptReconnects` 通过 |
| T6 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 本地 shell 标签**不复原**（每次都是新 shell）。
- 需交互输入口令的会话不复原（避免启动时连环弹窗），需手动连接。
- 不带滚动缓冲/命令历史恢复——只恢复「打开了哪些会话」这一布局信息。
