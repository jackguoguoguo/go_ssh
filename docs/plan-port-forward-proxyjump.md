# sshtool 端口转发与跳板机（ProxyJump）

- 关联版本：v0.3 可靠性批次补充（路线图第 7 项）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

运维常需要在本机与远端之间打通隧道，或在跳板机后面访问内网主机。本功能补齐两类能力：

1. **端口转发**：本地转发（`-L`，把远端/内网服务映射到本机端口）与远端转发（`-R`，把本机服务暴露给远端）。
2. **跳板机（ProxyJump）**：设置环境变量后，所有 SSH 连接都先经跳板机建立，无需改每条连接配置。

## 2. 设计要点

### 2.1 端口转发（`internal/portfwd`）
独立的 `Manager` 管理本进程内所有转发规则，互不干扰：

- `StartLocal(client, listen, target)`：本机监听 `listen`，每个入站连接经 `client.Dial("tcp", target)` 隧道到目标；
  `client` 即当前 SSH 会话的 `*sshx.Client`（`remotessh.Session.Client()` 暴露）。
- `StartRemote(client, listen, target)`：调用 `client.Listen("tcp", listen)` 在 **SSH 服务端** 监听，
  入站连接经隧道回连本机 `target`。
- `Stop / List / StopAll`：停止、列出、全部停止（退出时调用 `StopAll` 释放端口）。
- 双向 `bridge` 拷贝，任一端关闭即断开两侧。

转发参数由调用方显式给出，**不写入 `store.Connection`**——后者是冻结契约，不能加字段。

### 2.2 跳板机（`internal/remotessh/proxyjump.go`）
- 通过环境变量 `SSHTOOL_PROXY_JUMP=[user@]host[:port]` 配置（格式同 ssh `-J`）。
  - `SSHTOOL_PROXY_JUMP_PASSWORD` / `SSHTOOL_PROXY_JUMP_PASSPHRASE` 提供跳板机密码 / 私钥口令。
- `dialClient` 改为：配置了跳板机时先 `sshx.Dial` 跳板机，再在其通道上 `jump.Dial("tcp", target)` 并完成 `NewClientConn`，
  得到目标连接；目标连接建立在跳板机通道之上，因此 `Session` 额外持有 `jump *sshx.Client`，**必须在目标关闭之后才关闭**。
- 跳板机认证：ssh-agent → 默认私钥（可带口令）→ 环境变量密码，与直连逻辑一致。
- 未配置时 `dialClient` 退化为直连，行为与改动前完全一致。

### 2.3 UI 接入
- `Model` 新增 `fwd *portfwd.Manager`（在 `tea.QuitMsg` 时 `StopAll` 清理端口）。
- 命令行元命令新增 `forward` / `forwards`：
  - `forward L <监听> <目标>` / `forward R <监听> <目标>`：取当前活动标签对应的 SSH 会话 `Client()` 启动转发；
    本地 shell 或无活动 SSH 会话时给出提示。
  - `forward stop <id>` / `forwards`：停止与查看。
- 监听地址规范化：纯端口补 `localhost:`，保留 `0.0.0.0` 等。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| F1 | 转发管理器（本地/远端/停止/列出） | `internal/portfwd/forward.go` |
| F2 | 跳板机拨号与生命周期 | `internal/remotessh/proxyjump.go`、`session.go`（`jump` 字段、`dialClient`、`Client()` 取值器） |
| F3 | UI 接入：Manager、元命令、退出清理 | `internal/ui/model.go`、`portfwd.go`、`sshconfig.go`、`dialogs.go`（帮助） |
| F4 | 测试辅助：测试服务器支持 direct-tcpip / tcpip-forward | `internal/testutil/sshserver.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 本地转发往返：经 SSH 隧道把数据送到远端回显服务并原样返回 | `forward_test.go::TestLocalForwardRoundTrip` 通过 |
| T2 | 远端转发往返：服务端监听端口，连接经隧道回连本机回显服务 | `TestRemoteForwardRoundTrip` 通过 |
| T3 | Stop / List / StopAll 生命周期正确 | `TestStopAndList` 通过 |
| T4 | 配置 `SSHTOOL_PROXY_JUMP` 后连接经跳板机建立且 `Client()` 非空 | `proxyjump_test.go::TestProxyJumpConnects` 通过 |
| T5 | 未配置跳板机时直连照常 | `TestProxyJumpDisabledConnectsDirectly` 通过 |
| T6 | 无活动 SSH 会话时 `forward` 给出提示 | `ui/portfwd_test.go::TestHandleForwardNoActiveSession` 通过 |
| T7 | `go build`/`go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制
- 转发规则**不持久化**：重启进程后需重新 `forward`（避免触碰 `store.Connection` 冻结契约）。如需持久，建议未来引入独立的转发配置文件。
- 转发随 SSH 会话生命周期：会话断开重连后，原转发监听会失效（持有的是旧 `Client`）。需手动重建。
- `forward` 需要当前标签是已连接的 SSH 会话；**本地 shell 不支持转发**。
- 跳板机配置为全局（影响所有连接），目前不支持按连接单独指定。
- 测试服务器新增 `direct-tcpip` / `tcpip-forward` 处理能力，仅用于单元测试，不影响生产代码路径。
