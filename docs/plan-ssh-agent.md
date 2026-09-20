# sshtool ssh-agent 集成 + 私钥口令缓存

- 关联版本：v0.3 可靠性批次补充（路线图第 7 项）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

- 用户常把私钥 `ssh-add` 到 agent；此前本工具只认磁盘上的私钥文件，agent 里的密钥用不上，
  且被加密的私钥每次连接都要重新输入口令（重连多台主机时尤其烦）。
- 目标：认证时**自动附带 ssh-agent 的公钥**；**私钥口令在进程内缓存**，同一私钥只问一次。

## 2. 设计要点

### 2.1 ssh-agent
- `SSH_AUTH_SOCK` 存在时惰性 `net.Dial("unix", sock)` 建立 agent 客户端，并以
  `sshx.PublicKeysCallback(ag.Signers)` 作为**附加认证方式**。
- 进程内**共享同一个 agent 连接**：agent 返回的 signer 在签名时仍需回访该连接，
  共享一份即可满足所有会话与重连，生命周期与进程一致。
- 挂载位置：私钥认证时排在私钥之后；密码认证时排在最后兜底。无论如何，agent 不可用就
  **静默跳过**，不影响原有认证路径。
- 导出 `AgentAvailable()` 便于后续做状态提示。

### 2.2 私钥口令缓存
- `Model.passCache map[string]string`，键为 **`remotessh.ResolveKeyPath(conn)`**（含默认 `~/.ssh/id_rsa`
  与 `~` 展开），只缓存 `AuthType == AuthKey` 且非空口令。
- 命中缓存时直接连接；未命中且连接设为「每次询问口令」时才弹窗。
- 用户在弹窗输入口令后写入缓存（连接成功的口令自然留下；口令错误会再次提示并覆盖）。
- 顺带抽出 `ResolveKeyPath` 供缓存键与 `authMethods` 共用，避免两处各算一遍。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| A1 | ssh-agent 客户端（共享连接）+ `AgentAvailable` + `agentAuthMethod` | `internal/remotessh/auth.go` |
| A2 | `authMethods` 附带 agent；抽出 `ResolveKeyPath` | `internal/remotessh/auth.go` |
| A3 | `Model.passCache` + `cachePassphrase` | `internal/ui/model.go` |
| A4 | 连接流程命中缓存免询问、弹窗后写入缓存 | `internal/ui/actions.go`、`internal/ui/model.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 缺省路径含 id_rsa、显式路径原样、`~` 展开 | `auth_test.go::TestResolveKeyPath` 通过 |
| T2 | `SSH_AUTH_SOCK` 未设置 / 连接失败时 agent 不可用且不产生认证方式 | `TestAgentUnavailableWhenNoSock`、`TestAgentUnavailableWhenDialFails` 通过 |
| T3 | 无 agent 时私钥认证只有 1 个认证方式 | `TestAuthMethodsKeyWithoutAgent` 通过 |
| T4 | 加密私钥：无口令 → `ErrNeedPassphrase`；正确口令成功；错误口令报错 | `TestAuthMethodsEncryptedKey` 通过 |
| T5 | 口令按私钥路径缓存；密码认证 / 空口令不缓存 | `passcache_test.go::TestCachePassphrase` 通过 |
| T6 | 「每次询问口令」命中缓存时不再弹窗 | `TestConnectReusesCachedPassphrase` 通过 |
| T7 | `go build`/`go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制
- **Windows 的 OpenSSH agent 走命名管道**（非 unix socket），当前实现不可用，会安全退化为不使用
  agent；Linux/macOS 正常。后续可引入 `winio` 支持。
- agent 连接进程内共享且随进程退出关闭（不显式关闭）。
- 口令缓存仅存于内存，退出即失效（不落盘，符合口令不落地的安全约定）；缓存了错误口令会
  在下一次连接失败后由 `EventNeedSecret` 重新询问并覆盖。
