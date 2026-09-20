# sshtool 连接健康巡检

- 关联版本：v0.4 扩展批次（推荐扩展 P0-2）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

运维常问「我这一批机器里，哪几台还活着？」本功能一键并发巡检全部（或过滤后的）连接，区分故障类型：

- **可达**：TCP 可连、SSH 认证成功；
- **认证失败**：网络通但凭据不对（密码错 / 密钥未授权 / 私钥口令缺失）；
- **不可达**：连接被拒 / 网络不通；
- **超时**：握手或命令执行超时。

只做「连通 + 认证」检查，**不发任何有副作用的命令**。

## 2. 设计要点

### 2.1 巡检内核（`internal/remotessh/ping.go`）
- `Ping(conns, timeout) []PingResult`：并发对每台执行无害命令 `true`（复用 `RunOnce`，无 PTY、一次连接），
  通过返回错误消息分类：
  - `unable to authenticate` / `permission denied` / `认证失败` → **认证失败**；
  - `超时` → **超时**；
  - 其它握手/拨号错误 → **不可达**。
- `PingResult`：`Name / Host / Status / Detail / Latency`；`PingStatus.Text()` 给出可读文本。
- `PingCount`：返回 `[可达, 认证失败, 不可达, 超时]` 计数。
- 复用 P0-1 的 `batchName / batchHost / secretForBatch`，避免重复实现。

### 2.2 UI 接入（`internal/ui/ping.go`）
- 元命令 `ping [过滤词...]`：
  - 无过滤词：对**全部**保存的连接巡检；
  - 带过滤词：仅对「名称/主机/用户/分组」匹配的连接巡检。
  - 与 `batch` 不同，`ping` **不弹选择框**（一键全量巡检才是主场景），`batch` 才需要多选。
- `runPing`：弹「正在巡检…」对话框 → `m.afterCmd = pingCmd(...)`。
- `pingCmd`：后台并发巡检 → `pingDoneMsg`。
- `showPingResult`：结果按「状态优先（可达在前）+ 名称」排序，逐台显示状态、地址、耗时与失败原因；
  状态栏给出汇总（`X 可达 / Y 异常（认证失败 a · 不可达 b · 超时 c）`）。

### 2.3 安全
- 巡检命令为 `true`，无副作用、不写历史、不污染交互会话。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| P1 | 巡检内核（分类 + 统计） | `internal/remotessh/ping.go` |
| P2 | UI：元命令 / 进度 / 结果页 | `internal/ui/ping.go` |
| P3 | 元命令分发与消息处理 | `internal/ui/sshconfig.go`、`internal/ui/model.go`（`pingDoneMsg`） |
| P4 | 帮助与文档 | `internal/ui/dialogs.go`、`README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 正常连接 → 可达且记录耗时 | `remotessh/ping_test.go::TestPingOK` 通过 |
| T2 | 错误密码 → 认证失败并附细节 | `TestPingAuthFail` 通过 |
| T3 | 无监听端口 → 不可达 | `TestPingUnreachable` 通过 |
| T4 | 统计函数正确 | `TestPingCount` 通过 |
| T5 | `ping` 无连接 → 提示、不弹框 | `ui/ping_test.go::TestPingMetaNoConnections` 通过 |
| T6 | `ping` 有连接 → 进度对话框 + 派生命令 | `TestPingMetaStartsCheck` 通过 |
| T7 | 结果页汇总统计与状态栏摘要正确 | `TestShowPingResult` 通过 |
| T8 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 巡检命令固定为 `true`（POSIX）；若远端为 Windows/受限 shell，可能失败并被判为不可达——后续可按平台自适应或用更基础的探测。
- `ping` 默认对全部连接巡检；如需部分，用 `ping <关键词>` 过滤。
- 与批量执行共用执行内核，后续 CLI（P2-6）可直接暴露 `sshtool ping`。
