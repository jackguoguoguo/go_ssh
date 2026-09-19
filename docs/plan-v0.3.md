# sshtool v0.3 实施方案：可靠性（心跳保活 + 断线自动重连）

- 版本：v0.3（基于当前 `dev`，基线提交 `ed01e5d`）
- 日期：2026-09-19
- 状态：**已实现并验证（待评审/提交）**

---

## 1. 目标

解决运维场景中最痛的「连着连着就断了」问题：

1. **快速发现断线**：周期性发送 SSH 保活探测，而非干等传输层 FIN/超时（后者在网络静默丢包时可能要几分钟才被发现）。
2. **自动恢复**：传输层断开后自动按指数退避重连，复用内存中已保存的密码 / 私钥口令，无需用户重新输入。
3. **不打扰用户**：用户主动 `exit` 或关闭会话**不**触发重连；重连期间状态栏给出明确提示而非静默丢输出。

非目标（本期不做，见路线图）：SFTP 双向传输、会话日志落盘、ssh/config 导入导出、断线前的本地输入缓冲重放。

## 2. 现状与问题

| 编号 | 现状 | 问题 |
|---|---|---|
| a | `session.go` 的 Wait 协程：shell 退出或传输错误都置 `StateClosed` 并广播 `EventDisconnected` | 网络抖动后无任何自动恢复，用户需手动 `Ctrl+R` |
| b | 仅依赖 `sess.Wait()` 返回来判断存活 | 静默掉线时 `Wait` 可能长时间不返回，发现延迟高 |
| c | 重连必须重新询问密码（v0.2 虽在内存保存 `secret`，但未用于自动重连） | 半夜掉线无人值守时无法自愈 |

## 3. 范围与落点

| 编号 | 功能 | 主要落点 |
|---|---|---|
| R1 | 保活探测协程 | `internal/remotessh/session.go`（`keepalive`） |
| R2 | 传输层断开 → 自动重连（指数退避、复用 secret、可中断） | `internal/remotessh/session.go`（`reconnect`、`connectReal` 重构） |
| R3 | 新状态 `StateReconnecting` 与新事件 `EventReconnecting/Reconnected/ReconnectFailed` | `session.go` / `manager.go` / `internal/ui/model.go`（状态栏与提示） |
| R4 | 可调参数（保活间隔 / 最大次数 / 退避基数） | 环境变量 `SSHTOOL_KEEPALIVE_SECS` / `SSHTOOL_RECONNECT_MAX` / `SSHTOOL_RECONNECT_BASE` |

## 4. 设计要点

### 4.1 存活判定区分「退出」与「掉线」
- `sess.Wait()` 返回 `nil` → shell 正常退出（用户 `exit`）→ 置 `StateClosed`，广播 `EventDisconnected`，**不重连**。
- `sess.Wait()` 返回非 `nil` → 传输层断开 → 触发 `reconnect()`。
- 保活探测连续 2 次失败 → 关闭底层 `client` → `Wait` 报错 → 同样进入 `reconnect()`。

### 4.2 reconnect 状态机
- 入口加 `reconnecting` 守卫，避免并发重连循环叠加。
- 退避：`base × 2^(N-2)` 秒，封顶 30s；首次立即尝试。
- 退避等待可被 `s.cancel`（`Close()` 时关闭）中断。
- 某次 `connect` 返回 `*HostKeyError`（指纹变更）→ 不再盲目重试，广播 `EventNeedHostKey` 交还 UI 决策。
- 达最大次数 → 置 `StateError` 并广播 `EventReconnectFailed`，提示 `Ctrl+R` 手动重连。

### 4.3 复用现有能力
- `secret` 字段（v0.2 已存于内存）直接用于重连，无需重新询问。
- `connect` 抽出 `connectImpl` 可替换点（默认 `connectReal`），便于单测注入故障序列。
- 重连成功保持原有 `term` 缓冲区，历史输出不丢。

### 4.4 安全默认值
- 保活默认开（30s）；如需关闭设 `SSHTOOL_KEEPALIVE_SECS=0`。
- 重连复用 secret 但**不落盘**，与 v0.2「口令仅内存」契约一致。

## 5. 任务清单（验收）

| # | 任务 | 验收 |
|---|---|---|
| T1 | `connectReal` 重构为同步、可注入；新增 `keepalive` / `reconnect` / `setErr` | `go vet` / `go build` 干净 |
| T2 | 新状态与事件 + UI 状态栏/提示接入 | 重连中显示「重连中」，成功提示「已恢复连接」，失败提示手动重连 |
| T3 | 单元测试覆盖：前 N 次失败第 N+1 次成功 / Close 中断 / 达上限放弃 | `reconnect_test.go` 三个用例全绿 |
| T4 | 测试服务端回复 keepalive，使保活路径可真实走通 | `testutil/sshserver.go` 回复 `keepalive@openssh.com` |
| T5 | 全量回归（remotessh / store / keytool / vt / ui） | 全部 `ok` |

## 6. 风险与缓解

| 编号 | 风险 | 缓解 |
|---|---|---|
| R1 | 服务端不回复 keepalive 导致误判掉线 | OpenSSH 默认回复；测试服务端已补回复；`SSHTOOL_KEEPALIVE_SECS=0` 可关闭 |
| R2 | 重连风暴（服务端持续不可达） | 指数退避封顶 30s + 最大次数上限 |
| R3 | 重连循环叠加 | `reconnecting` 守卫 + `cancel` 通道统一中断 |
| R4 | 用户 `exit` 被误重连 | 仅 `Wait` 返回错误才重连，正常退出不触发 |
