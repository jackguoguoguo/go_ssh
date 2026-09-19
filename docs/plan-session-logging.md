# sshtool 会话日志落盘与回放

- 关联版本：v0.3 可靠性批次（在「心跳保活 + 断线自动重连」之后）
- 日期：2026-09-19
- 状态：**已实现并验证（待评审/提交）**

---

## 1. 目标

运维与审计场景经常需要「把这次连上去干了什么留个底」：

1. **落盘**：每个会话的终端输出实时写入磁盘，断线重连后仍然是一个连续文件。
2. **回放**：随时按 `Ctrl+L` 把已落盘的日志重新喂给终端模拟器，只读地重看整段会话。
3. **零侵入**：未设置环境变量时完全无感知；设置即用，不污染 `--config` 的 JSON 契约（Settings 结构体已冻结，偏好走环境变量）。

## 2. 设计要点

### 2.1 记录什么、不记录什么
- 只记录**终端输出字节流**（远端回显 + 命令输出 + ANSI 转义），这正是 `readLoop` 写进 `vt` 缓冲区的同一批字节。
- 因为远端默认回显输入，所以命令本身会随回显进入日志；**密码等无回显输入不会落盘**（更安全）。
- 采用「输出流」而非「输入流 + 输出流」分离，语义等同于 Unix `script`/`typescript`，回放即忠实还原屏幕。

### 2.2 落盘位置与生命周期
- 环境变量 `SSHTOOL_LOG_DIR` 非空时启用；为空则不记录。
- 每个会话在创建时（`newSession`）建立文件：`<LOG_DIR>/<user@host_port>/<sessionID>_<YYYYMMDD-HHMMSS>.log`。
- 同一会话断线重连（reconnect 重启 `readLoop`）共用同一文件，追加写入，保证连续性。
- `Close()` 时关闭文件句柄；写入尽力而为，磁盘满等错误被忽略，不阻塞主流程。
- 并发安全：`logFile` 由独立的 `logMu` 保护，`readLoop` 追加与 `Close` 关闭互不踩踏。

### 2.3 回放视图
- `Ctrl+L`：当前会话未记录时给出提示；已记录则从磁盘读取日志，新建一个 `vt.Terminal` 喂入并 `ScrollToBottom`，进入全屏「回放」模态。
- 模态内 `↑↓`/`PgUp`/`PgDn` 滚动、`End` 回最新、`Esc`/`Q`/`Ctrl+Q` 退出；复用既有 `renderTerminal` 同款渲染逻辑。
- 回放是快照（打开瞬间读盘），不随后续实时输出变化。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| L1 | 会话日志字段、`startLogging`、`Logging()`/`LogPath()`、`appendLog`，`readLoop` 追加 | `internal/remotessh/session.go` |
| L2 | 状态栏「●记录」指示 | `internal/ui/panels.go` |
| L3 | `Ctrl+L` 打开回放模态 + `handleReplayKey` + `renderReplay` | `internal/ui/keys.go`、`internal/ui/replay.go` |
| L4 | 帮助文本与状态栏提示补 `Ctrl+L` | `internal/ui/dialogs.go`、`internal/ui/panels.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | `SSHTOOL_LOG_DIR` 设置后 `Logging()==true` 且输出写入文件；`Close` 后内容保留 | `session_log_test.go` 通过 |
| T2 | 未设置时 `Logging()==false`、`LogPath()==""` | 通过 |
| T3 | 真实会话产生输出后日志文件含该内容；`Ctrl+L` 打开回放且回放含该内容；`Esc` 退出 | `replay_test.go::TestReplayOpensForLoggingSession` 通过 |
| T4 | 未记录时 `Ctrl+L` 不打开回放，仅提示 | `replay_test.go::TestReplayWithoutLoggingShowsHint` 通过 |
| T5 | 全量回归（remotessh / store / keytool / vt / ui） | 全部 `ok` |

## 5. 已知限制
- 回放为「打开瞬间」的快照，不会随实时输出滚动更新；如需跟随，可后续改为增量重放。
- 日志按字节原样写入，包含 ANSI；如需纯文本审计，可后续加「剥离转义」的导出。
- 未做日志轮转/大小上限，长时间会话文件可能较大（后续可用环境变量控制上限）。
