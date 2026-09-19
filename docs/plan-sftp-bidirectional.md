# sshtool SFTP 双向传输

- 关联版本：v0.3 可靠性批次补充（路线图第 2 项）
- 日期：2026-09-19
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

`Ctrl+O` 文件浏览器此前只能「浏览 / 打开 / 编辑」，缺少运维最常用的文件维护能力。
本功能补齐**双向传输与目录维护**：重命名、改权限、新建目录、下载到本地、上传本地文件、删除（目录递归），
全部基于既有 SFTP 通道，不依赖 `scp`/`sftp` 命令行。

## 2. 设计要点

- **后端（`FSClient`）**：
  - 新增 `Chmod(p, mode)`：透传 SFTP 的 chmod。
  - 新增 `RemoveAll(p)`：文件直接删；目录先 `ReadDir` 递归清空子项再 `RemoveDirectory`
    （pkg/sftp 的 `Walk` 是前序，无法直接后序删除，故自行递归）。
- **前端（文件浏览器）**：引入「参数输入态」与「删除确认态」，避免为每个操作弹新对话框而丢失浏览上下文：
  - `dlg` 增加 `fsOp`/`fsInput`/`fsInputLabel`/`fsInputPath`/`fsInputIsDir`/`fsConfirmDel`。
  - `fileKey` 顶部优先处理输入态 / 确认态；`fsInputKey` 负责编辑（Esc 取消、Enter 提交、Backspace、可打印字符）。
  - `fsSubmitOp` 按 `fsOp` 分派：`mkdir` / `rename` / `chmod` / `download` / `upload`。
  - 每个操作封装为异步 `tea.Cmd`（执行后重新列出当前目录，回传 `fsLoadedMsg`），避免阻塞 UI；
    `fsLoadedMsg` 新增 `note` 字段用于成功提示（如「已下载到 …」）。
  - 操作进行中置 `fsLoading`，`fileKey` 顶部忽略按键，规避竞态。
- **按键分配**（仅在「无过滤」时触发，避免与文件名过滤冲突）：
  `r` 重命名、`m` 改权限、`n` 新建目录、`d` 下载、`U` 上传、`D` 删除；
  `u` 仍为上一级（改为仅小写，把 `U` 让给上传）。
- **安全性**：删除**二次确认**（`y`/`Enter` 确认，其它取消）；权限输入按八进制校验，非法输入不派生命令。
- 因 `dlg.fs` 是具体 `*FSClient`，输入/确认状态机可在无网络下单测；真实传输用进程内 SSH+SFTP 服务端做集成测试。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| S1 | `FSClient.Chmod` / `FSClient.RemoveAll`（递归） | `internal/remotessh/sftp.go` |
| S2 | `dlg` 操作态字段 + 渲染输入/确认行 + 帮助文本 | `internal/ui/dialogs.go` |
| S3 | 操作按键、输入态、`fsSubmitOp` 与各异步命令 | `internal/ui/fsbrowser.go` |
| S4 | `fsLoadedMsg.note` 成功提示 | `internal/ui/model.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 重命名输入态预填当前名、追加字符、回车派生命令 | `fsops_test.go::TestFsRenameFlow` 通过 |
| T2 | 新建目录空输入不派生命令 | `TestFsMkdirEmptyIsNoop` 通过 |
| T3 | 非法权限（`abc`）被拒且退出输入态 | `TestFsChmodInvalidRejected` 通过 |
| T4 | 删除需二次确认；非 `y` 取消；`y` 派生命令 | `TestFsDeleteConfirm` 通过 |
| T5 | `U` 进入上传态、`Esc` 取消；目录不可下载、文件可下载 | `TestFsUploadDownloadEnter` 通过 |
| T6 | `parseOctal` 合法/非法解析 | `TestParseOctal` 通过 |
| T7 | 真机 SFTP：Mkdir/WriteFile/ReadFile/Chmod/List/RemoveAll 递归删除 | `TestFsTransferRoundTrip` 通过 |
| T8 | `go build`/`go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制
- 上传/下载为整体读写（`ReadFile`/`WriteFile`），无分片与进度条，超大文件会占用较多内存（后续可加分块 + 进度）。
- 下载默认相对**本地当前工作目录**；不支持本地文件选择器（需手动输入路径）。
- 上传同名文件直接覆盖，无覆盖确认。
- `m` 改权限在 Windows 本地无意义（权限语义由远端决定），仅对远端文件生效。
