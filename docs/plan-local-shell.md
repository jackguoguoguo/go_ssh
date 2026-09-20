# sshtool 本地 shell 标签

- 关联版本：v0.3 可靠性批次补充（路线图第 8 项）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

除了连远端，运维也常要在**本机**跑命令。此前本工具只有 SSH 会话标签，本机操作得另开终端。
本功能新增「本地 shell 标签」：在本机开一个**真实 PTY** 的 shell，与 SSH 会话并列在同一标签栏，
共用同一套终端模拟器、选择/搜索、窗口尺寸同步等能力。

## 2. 设计要点

### 2.1 引入标签抽象（本次最大的结构改动）
原先 `Model.sessions` 硬编码为 `[]*remotessh.Session`，无法容纳非 SSH 会话。改为接口：

```go
type tabSession interface {
    TabID() string
    Label() string
    ConnInfo() store.Connection
    State() remotessh.State
    Term() *vt.Terminal
    Write(p []byte) error
    Err() error
    Cols() int
    Rows() int
    Logging() bool
    Secret() string
    Resize(cols, rows int)
    Close()
}
```

- `remotessh.Session` 的 `ID` / `Conn` 是导出字段，不能再定义同名方法，故新增取值器
  `TabID()` / `ConnInfo()`（见 `remotessh/tabsession.go`）。
- `sshSessionOf()` / `isLocalShell()` 用于在需要 SSH 专属能力时取回具体类型。
- 由此统一了标签栏渲染、切换、关闭、resize 的路径；SSH 专属功能做显式降级：
  - **广播**：本地 shell 不计入（把命令广播到本机风险过高）。
  - **Ctrl+O 文件浏览器 / Ctrl+L 回放**：本地 shell 直接提示不支持，不再静默失败。
  - 头部信息：本地 shell 不显示端口（避免出现 `:0`）。

### 2.2 本地 shell 实现（`internal/localshell`）
- 抽象 `ptyDevice{Read, Write, Resize, Close}`，按平台拆分后端：
  - **Unix**：`creack/pty` 的 `StartWithSize` + `pty.Setsize`（shell 取 `$SHELL`，默认 `/bin/bash -i`）。
  - **Windows**：ConPTY（`UserExistsError/conpty`），shell 取 `COMSPEC`（默认 `cmd.exe`）。
- 输出经 `vt.Terminal` 渲染；`Resize` 同时更新 PTY 窗口尺寸与终端缓冲区（全屏程序因此可用）。
- 输出通知复用 SSH 的同一条重绘通道：新增 `Manager.NotifyOutput(id)`，
  本地 shell 产生输出即触发 UI 重绘，无需另起轮询。
- 打开方式：命令行元命令 `shell`（与 `theme` / `import` / `export` 一致，无需新快捷键）。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| L1 | 标签接口与类型判定 | `internal/ui/tab.go` |
| L2 | `Session.TabID/ConnInfo` | `internal/remotessh/tabsession.go` |
| L3 | `Manager.NotifyOutput` | `internal/remotessh/manager.go` |
| L4 | 会话公共逻辑 | `internal/localshell/shell.go` |
| L5 | PTY 后端（Unix / Windows） | `internal/localshell/shell_unix.go`、`shell_windows.go` |
| L6 | Model 改为 `[]tabSession` + `locals`；resize 同步本地 shell | `internal/ui/model.go` |
| L7 | 打开/关闭/重连、广播排除、SFTP 与回放降级 | `internal/ui/actions.go`、`fsbrowser.go`、`replay.go`、`panels.go` |
| L8 | 元命令 `shell` | `internal/ui/sshconfig.go`、`internal/ui/actions.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 打开本地 shell 后状态为已连接、标签名与连接信息正确 | `shell_test.go::TestSessionRunsCommand` 通过 |
| T2 | 写入命令后终端缓冲区出现回显（证明是真实 PTY 而非裸管道） | 通过（Windows ConPTY 实测） |
| T3 | 关闭后状态为已关闭、再写入报错 | `TestSessionClose` 通过 |
| T4 | 元命令 `shell` 打开标签并置为活动；关闭后不残留 | `localshell_test.go::TestOpenLocalShellTab` 通过 |
| T5 | 本地 shell 不计入广播目标 | `TestLocalShellNotInBroadcast` 通过 |
| T6 | `go build`/`go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制
- Windows 需要 **Windows 10 1809+**（ConPTY 可用）；更旧系统会明确报 `ErrConPtyUnsupported` 而不是静默失败。
- 本地 shell 无 SSH 专属能力：不参与广播、无 SFTP 文件浏览器、无会话日志回放、无远端 cwd 上报
  （状态栏的 `pwd:` 对本地 shell 无意义）。
- 关闭标签会直接杀掉 shell 进程树（ConPTY 关闭即终止子进程），未做「确认后再关」。
- 依赖新增两个模块：`github.com/creack/pty`（Unix）与 `github.com/UserExistsError/conpty`（Windows）。
