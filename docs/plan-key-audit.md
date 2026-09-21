# sshtool 远端公钥盘点

- 关联版本：v0.4（README「其它值得做的」最高优先级项）
- 日期：2026-09-21
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

密钥分发后最难回答的问题是「到底哪台授权了、哪台没有」。本功能并发拉取各主机的
`authorized_keys`，与本机密钥比对，直接回答：

- **哪台还没授权**（本机有该密钥、远端没有 → 需要推送）
- **哪台有已废弃的旧密钥**（远端有、本机无对应私钥 → 疑似废弃或他人密钥）

## 2. 设计要点

### 2.1 审计内核（`internal/keytool/audit.go`）
- `Audit(conns, localKeys, remotePath, timeout)`：并发（WaitGroup）对每台主机
  拉取远端 `authorized_keys` 内容并解析为条目，然后分类。
- `parseAuthorizedKeys`：按行解析，跳过空行与 `#` 注释行；无法解析的行（如带选项前缀）跳过，不影响其余条目。
- `classify`：按 **`KeyBody`（算法 + 密钥体）** 比较，**忽略注释**——与推送时的去重口径完全一致
  （换注释不算新密钥，也不会误判为缺失）。
- `AuditResult`：`Total / Authorized(指纹) / Missing(指纹) / Stale(条目) / Err`。
- `Summarize`：统计已全授权 / 待推送 / 含废弃 / 失败台数。

### 2.2 修复的真实 bug
拉取远端文件用 `cat <路径>`，而 `normalizeRemotePath` 会把 `~` 展开成 `$HOME`；
原先 `SnapshotRemoteAuthorizedKeys` 用**单引号**包裹路径，导致 `$HOME` 不被 shell 展开，
盘点**永远**报「No such file or directory」（由 `TestAuditAgainstServer` 暴露）。
改为 `shellDoubleQuote`（双引号，转义内部双引号但保留 `$` 以允许展开）。

### 2.3 UI（`internal/ui/keyaudit.go`）
- `Ctrl+G` 新增「⑥ 远端公钥盘点」→ 多选目标（复用 `dlgPick` 多选，`#分组` 可过滤）→ 后台执行。
- `auditCmd` 并发盘点；结果按名称排序后由 `showAuditResult` 展示：
  汇总行（已全授权 / 待推送 / 含废弃 / 失败）+ 逐台（远端条目数、已授权数、废弃条目及其注释）。
- 无本机公钥时直接提示先生成，不做无意义的比对。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| A1 | 并发审计内核 + 分类 + 汇总 | `internal/keytool/audit.go` |
| A2 | 远端读取引号修复（$HOME 展开） | `internal/keytool/backup.go` |
| A3 | UI 目标选择 / 后台盘点 / 结果页 | `internal/ui/keyaudit.go`、`keymgr.go`、`model.go` |
| A4 | 帮助与文档 | `internal/ui/dialogs.go`、`README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 解析跳过注释与空行，注释正确 | `keytool/audit_test.go::TestParseAuthorizedKeysSkipsComments` 通过 |
| T2 | 按密钥体分类出已授权 / 缺失 / 废弃 | `TestClassifyAuthorizedMissingStale` 通过 |
| T3 | 真机端到端：推送后盘点显示已授权且无缺失（**发现并修复 $HOME 引号 bug**） | `TestAuditAgainstServer` 通过 |
| T4 | 不可达主机记录错误并计入失败 | `TestAuditUnreachableHost` 通过 |
| T5 | UI 无连接时提示 | `ui/keyaudit_test.go::TestAuditNoConnectionsPrompts` 通过 |
| T6 | UI 打开多选目标选择器 | `TestAuditPickerShowsTargets` 通过 |
| T7 | 结果页汇总与逐台展示（含废弃条目注释、失败原因） | `TestShowAuditResultSummarizes` 通过 |
| T8 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 无法解析的条目（带 `from="..."` 等选项前缀）会被跳过，不计入废弃。
- 「废弃」是**按本机私钥判断**的：远端上属于他人/其他用途的密钥也会显示为废弃，需人工确认后再删。
- 盘点只读，不做任何修改；如需移除废弃条目，可用下一步的「密钥轮换」或手动处理。
