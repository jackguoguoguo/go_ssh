# sshtool SFTP 目录递归传输

- 关联版本：v0.4 扩展批次（推荐扩展 P1-4）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

`Ctrl+O` 文件浏览器此前只支持**单文件**上传/下载，备份目录、同步配置文件时需逐个操作。
本功能让 `d`（下载）与 `U`（上传）支持**目录递归传输**：整棵子树一次到位，保留子目录结构。

## 2. 设计要点

### 2.1 远端遍历（`internal/remotessh/sftp.go`）
- 新增 `Walk(root, fn)`：封装 `sftp.Client.Walk` 的 `Step()` API，回调收到远端 POSIX 路径与是否为目录。
- 新增 `Stat(p)`：供上层判断远端路径属性。

### 2.2 UI 递归命令（`internal/ui/fsbrowser_tree.go`）
- **递归下载** `fsDownloadTreeCmd(remoteDir, localDir)`：
  - `FSClient.Walk` 前序遍历远端；相对路径用 `strings.TrimPrefix` 计算（远端为 POSIX 分隔，`path` 包无 Rel），
    再经 `filepath.FromSlash` 映射为本地路径——Windows 上自动落为 `\`。
  - 目录 `os.MkdirAll`、文件 `ReadFile → WriteFile`；返回时重新 `List` 当前目录并带 `note`（避免列表被 `entries=nil` 清空）。
- **递归上传** `fsUploadTreeCmd(localDir, remoteDir)`：
  - `filepath.Walk` 遍历本地；目录 `FSClient.Mkdir`（递归建）、文件 `ReadFile → WriteFile`（远端自动建父目录）。
  - 远端相对路径用 `path.Join(remoteDir, filepath.ToSlash(rel))` 统一为 POSIX。

### 2.3 交互改造（`fsbrowser.go`）
- `d` 对**文件或目录**都可用（目录自动进入递归下载，`fsInputIsDir=true` 区分）；
- `U` 上传时若本地路径是目录（`os.Stat` 判断）自动走递归上传，远端目标为当前目录下的同名目录；
- 底部提示与帮助文本同步更新（「目录会递归传输」）。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| F1 | 远端遍历 Walk / Stat | `internal/remotessh/sftp.go` |
| F2 | 递归下载 / 上传命令 | `internal/ui/fsbrowser_tree.go` |
| F3 | 交互分流（文件 vs 目录） | `internal/ui/fsbrowser.go` |
| F4 | 帮助与文档 | `internal/ui/dialogs.go`、`README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 本地三级目录树（含嵌套）递归上传 → 远端结构一致 | `ui/fsbrowser_tree_test.go::TestFsTreeUploadDownloadRoundTrip` 通过 |
| T2 | 递归下载回另一本地目录 → 三个文件内容逐一比对一致 | 同上 通过 |
| T3 | 目录按 `d` 进入递归下载态（`fsInputIsDir=true`）、文件仍为普通下载 | `ui/fsops_test.go::TestFsUploadDownloadEnter` 通过 |
| T4 | `go build` / `go vet` 干净；UI 全量回归通过 | 通过 |

## 5. 已知限制与后续
- 文件内容整体读入内存（沿用既有 `ReadFile/WriteFile` 行为）；超大文件（GB 级）建议先用命令行工具，后续可为树传输引入流式拷贝。
- 递归传输为同步阻塞命令（无进度条）；目录极大时 UI 会停在「加载中」。后续可在树传输中上报进度事件。
- 不做增量/差异比较：同名覆盖、已存在不跳过（与单文件上传语义一致）。
