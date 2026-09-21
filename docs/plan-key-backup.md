# sshtool 密钥备份与恢复

- 关联版本：v0.4（README「推荐补上的三件事」①）
- 日期：2026-09-21
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

误删 `~/.ssh` 或重装系统后，若只靠密钥登录会**彻底失去服务器访问权**。本功能提供：

1. **加密备份**：打包 `id_*`（私钥+公钥）、`config`、`known_hosts` 为 tar.gz，用口令派生密钥整体加密归档。
2. **校验恢复**：解密后按 manifest 校验每个公钥指纹，可选覆盖或改名导入。
3. **推送前快照**（快照式保险）：每次推送公钥前回传远端 `authorized_keys` 存档，误删也能回滚。

## 2. 设计要点

### 2.1 归档与加密（`internal/keytool/backup.go`）
- 归档内容：`id_*` 私钥与其 `.pub`、`config`、`known_hosts`（**只收 `id_*`**，避免把无关文件混进备份）。
- 归档格式：`tar.gz`，内含 `manifest.json`（`created_at / ssh_dir / files[] / keys[]`，
  其中 `keys[]` 记录每把密钥的 `alg / fingerprint / comment / 路径`）。
- 加密：**scrypt**（N=32768, r=8, p=1, 32 字节）派生密钥 + **AES-256-GCM**。
  文件布局：`magic("SSKB") + version(1) + salt(32) + nonce(12) + ciphertext`。
  未加密的私钥压缩包放在磁盘上是明显的安全回退，因此**强制要求口令**（空口令直接报错）。
- 落盘：`~/.sshtool/backups/ssh-keys-<时间戳>.enc`，权限 `0600`，目录 `0700`。

### 2.2 恢复与校验
- `ReadManifest`：解密并读取 manifest（口令错误 → 明确的「解密失败」）。
- `Restore`：`RestoreOverwrite`（覆盖）或 `RestoreRename`（同名自动加 `.restored-<时间戳>` 后缀）。
- 写入后 `verifyRestored` 按 manifest **逐把校验指纹**，不一致即报错——防止恢复出损坏/被篡改的密钥。

### 2.3 推送前远端快照
- `SnapshotRemoteAuthorizedKeys`：经 `RunOnce` 执行 `cat <远端路径>` 取回内容。
- `SaveRemoteSnapshot`：存到 `~/.sshtool/backups/remote/<主机>/<时间戳>.authorized_keys`。
- 在 UI 的 `pushKeysCmd` 里对每台目标「先快照、后推送」；快照失败**不阻断**推送（它只是保险）。

### 2.4 UI（`internal/ui/keybackup.go`）
- `Ctrl+G` 新增「⑤ 备份与恢复」→ 子菜单：① 备份（填加密口令）② 恢复（选备份 → 口令 + 覆盖/改名）。
- 均为后台命令（`backupDoneMsg` / `restoreDoneMsg`）→ 结果对话框；状态栏同步摘要。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| B1 | 归档 / 加解密 / manifest / 列表 | `internal/keytool/backup.go` |
| B2 | 指纹校验与两种恢复模式 | 同上 |
| B3 | 推送前远端快照 | 同上 + `internal/ui/keymgr.go` |
| B4 | UI 备份与恢复流程 | `internal/ui/keybackup.go`、`model.go` |
| B5 | 帮助与文档 | `internal/ui/dialogs.go`、`README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 备份→错误口令解密失败→正确口令读出 manifest（含两把密钥指纹）→恢复后指纹一致 | `keytool/backup_test.go::TestBackupAndRestoreRoundTrip` 通过 |
| T2 | 空口令不允许备份 | `TestBackupRequiresPassphrase` 通过 |
| T3 | 空目录备份报错 | `TestBackupEmptyDir` 通过 |
| T4 | 改名模式不覆盖同名文件 | `TestRestoreRenameAvoidsOverwrite` 通过 |
| T5 | 列出备份（时间倒序、大小） | `TestListBackups` 通过 |
| T6 | 远端快照落盘到 remote 子目录且内容一致 | `TestSaveRemoteSnapshot` 通过 |
| T7 | UI：菜单→备份表单（口令密文字段） | `ui/keybackup_test.go::TestBackupMenuOpensBackupForm` 通过 |
| T8 | UI：空口令不派生命令 | `TestBackupFormEmptyPassphraseRejected` 通过 |
| T9 | UI 端到端备份出 `.enc` 归档 | `TestBackupFlowCreatesArchive` 通过 |
| T10 | UI 端到端恢复并校验指纹一致 | `TestRestoreFlowRoundTrip` 通过 |
| T11 | 无备份时提示而非报错 | `TestRestoreNoBackupsPrompts` 通过 |
| T12 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 口令**无找回机制**（设计如此），请另行保管；口令丢失则备份不可恢复。
- 每次推送多一次 SSH 往返用于快照；可通过设置关闭（当前默认开启，优先安全）。
- 远端快照按时间戳累积，不会自动清理旧快照。
