# sshtool 非交互子命令（exec / ping）

- 关联版本：v0.4 扩展批次（推荐扩展 P2-6）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

把 TUI 内的批量执行与巡检内核直接暴露为命令行子命令，让 sshtool 进入脚本与 CI 场景：

```sh
sshtool exec  [-g 关键词] [--timeout N] [-f text|json|plain] <命令...>
sshtool ping  [-g 关键词] [--timeout N] [-f text|json]
```

## 2. 设计要点

### 2.1 内核复用（不重写）
- `exec` → `remotessh.Batch`（P0-1 的并发执行内核，复用 `RunOnce` 一次连接行为）。
- `ping` → `remotessh.Ping`（P0-2 的巡检内核，区分可达/认证失败/不可达/超时）。
- 目标过滤复用 `store.FilterConnections`（本次从 UI 抽出为共享实现，UI 委托调用，保证语义一致）。

### 2.2 输出与退出码
- **text**（默认）：逐台 `✓/✗ 名称 地址 (exit=N)` + 缩进的 stdout；`ping` 为逐台状态行 + 汇总。
- **json**：**白名单字段**（name/host/code/stdout/stderr/err 与 name/host/status/detail/latency_ms）。
  **绝不序列化 `Connection`**——其中含明文密码字段（`password`），这是安全红线（有测试把关）。
- **plain**（仅 exec）：只打印成功主机的 stdout，单台不加节分隔，便于管道给 `grep`/`jq` 等。
- 退出码：`0` 全部成功；`1` 存在失败或无匹配目标；`2` 用法错误。

### 2.3 安全
- 指纹校验与 TUI 完全一致（`~/.ssh/known_hosts`）；未信任的主机会直接失败，杜绝「脚本静默信任新指纹」。
- 脚本批量场景可用既有逃生舱 `SSHTOOL_INSECURE_HOST_KEYS=1` 显式退回不校验。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| C1 | exec / ping 子命令与输出格式 | `internal/cli/cli.go` |
| C2 | 共享连接过滤 | `internal/store/filter.go`（UI 委托） |
| C3 | main 分发与帮助 | `main.go` |
| C4 | 文档 | `README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 两台服务器执行成功，退出码 0，text 汇总正确 | `cli/cli_test.go::TestExecAllSuccess` 通过 |
| T2 | `-g` 过滤命中目标；错误密码 → 退出码 1 并标注失败主机 | `TestExecGroupFilterAndFail` 通过 |
| T3 | JSON 输出小写字段名且**不含凭据** | `TestExecJSONFormat` 通过 |
| T4 | plain 模式只输出 stdout | `TestExecPlainOnlyStdout` 通过 |
| T5 | 无命令 → 2；无匹配目标 → 1 | `TestExecUsageErrors` 通过 |
| T6 | ping 区分可达/认证失败/不可达，退出码 1 | `TestPingTextAndExitCode` 通过 |
| T7 | 全部可达 → 退出码 0 | `TestPingAllReachable` 通过 |
| T8 | 无连接 → 1 | `TestPingNoTargets` 通过 |
| T9 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 需要交互输入口令的连接在 CLI 下无法使用（凭据必须已保存，或由 agent/未加密私钥提供）——与 headless 语义一致。
- 无并发上限调节（默认全并发）；目标极多时可考虑 `-j` 限并发。
- 结果不落盘（stdout 交给调用方重定向）；需要审计可外接 `SSHTOOL_LOG_DIR` 的 TUI 侧日志。
