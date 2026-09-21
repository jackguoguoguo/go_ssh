# sshtool 密钥轮换

- 关联版本：v0.4（README「其它值得做的」）
- 日期：2026-09-21
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

密钥需要定期轮换（人员变动、疑似泄露、合规要求）。手工流程繁琐且危险——
最容易踩的坑是「新密钥没生效就把旧密钥删了，结果把自己锁在门外」。

本功能把流程自动化：**生成新密钥 → 批量分发 → 用新私钥验证确实能登录 → 才移除旧密钥**。

## 2. 设计要点

### 2.1 内核（`internal/keytool/rotate.go`）
- `Rotate(o)` 单台主机三阶段：
  1. **分发**：复用 `Push`（幂等去重、自动修权限）。
  2. **验证**：构造一个 `AuthType=key`、指向**新私钥**的连接，用 `RunOnce` 执行 `true`。
     只有真的能用新私钥登录才继续——这是整个流程的安全闸门。
  3. **移除**：`RemoveAuthorizedKey` 按「算法+密钥体」`grep -vF` 精确删除旧行，
     经临时文件中转后覆写，保留其它条目与 `600` 权限。
- **失败即中止并保留旧密钥**：任一阶段出错都返回 `Err` 且不删除旧密钥，错误信息明确写「已保留旧密钥」。
- `KeepOld` 选项：只加不删（适合先灰度、再单独清理）。
- `RotateAll`：并发对多台执行；每台沿用自己已保存的凭据。
- `RotateResult.Phases` 记录每阶段结果，便于结果页排障与审计。

### 2.2 UI 向导（`internal/ui/keyrotate.go`）
`Ctrl+G` → ⑦ 密钥轮换，四步：
1. 生成新密钥（算法 / 注释 / 路径 / 口令，后台生成避免阻塞）；
2. 选择要被替换的旧密钥（自动排除刚生成的新密钥）；
3. 多选目标主机；
4. 确认（可选「验证后是否移除旧密钥」）→ 后台执行 → 结果页（逐台列出三阶段）。

### 2.3 测试基础设施改动
测试用 SSH 服务端原先**只支持密码认证**，导致「用私钥登录」这条路径无法验证
（表现为 `attempted methods [none]`）。已为 `testutil.SSHServer` 增加
`PublicKeyCallback`（接受任意公钥），使轮换的验证阶段可被真实端到端测试。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| R1 | 轮换内核（分发/验证/移除）与并发 | `internal/keytool/rotate.go` |
| R2 | 远端精确删除旧密钥 | 同上（`RemoveAuthorizedKey`） |
| R3 | UI 四步向导与结果页 | `internal/ui/keyrotate.go`、`keymgr.go`、`model.go` |
| R4 | 测试服务端支持公钥认证 | `internal/testutil/sshserver.go` |
| R5 | 文档 | `README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 真机完整轮换：分发 → 验证 → 移除，盘点确认只剩新密钥、旧密钥已不在 | `keytool/rotate_test.go::TestRotateFullCycle` 通过 |
| T2 | **验证失败时保留旧密钥**（安全闸门），旧密钥仍在远端 | `TestRotateKeepsOldOnVerifyFailure` 通过 |
| T3 | `KeepOld=true` 时只加不删 | `TestRotateKeepOldOption` 通过 |
| T4 | 空新公钥被拒绝 | `TestRotateEmptyNewKey` 通过 |
| T5 | 移除不存在的条目不报错且报告未移除 | `TestRemoveAuthorizedKeyByIdempotent` 通过 |
| T6 | 成功/失败计数 | `TestRotateSummaryCounts` 通过 |
| T7 | UI 向导首步为生成表单 | `ui/keyrotate_test.go::TestRotateWizardStartsWithForm` 通过 |
| T8 | 选旧密钥时排除新密钥 | `TestRotatePickOldExcludesNewKey` 通过 |
| T9 | 确认后派生命令且目标正确 | `TestRotateConfirmAndRun` 通过 |
| T10 | 结果页展示三阶段与汇总 | `TestShowRotateResult` 通过 |
| T11 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 验证阶段要求目标主机允许**公钥登录**（密码-only 的主机会验证失败并保留旧密钥，属预期保护行为）。
- 移除按「算法+密钥体」精确匹配，若同一把密钥在远端写了多行（不同注释）会一并删除（符合轮换语义）。
- 轮换不改动本机 `~/.ssh/config` 里对旧私钥的引用，也不更新连接的 `key_path`，需用户自行切换。
