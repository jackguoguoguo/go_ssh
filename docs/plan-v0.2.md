# sshtool v0.2 实施方案：终端可用性与安全兜底

- 版本：v0.2（基于当前 `dev`，基线提交 `f4614e8`）
- 日期：2026-09-17
- 状态：**评审已通过（按修订版执行）**

---

## 1. 目标

在「连得上、看得见、改得了」已经成立的基础上，补掉**每天都会撞到**的四类缺口：

1. **安全兜底**：消除中间人风险敞口，主机指纹可追溯、可落盘。
2. **文本可取**：终端里的输出能被选中、复制、粘贴，不再依赖关闭鼠标后的系统选择。
3. **信息可找**：回滚缓冲可被搜索定位，翻屏找报错不用靠肉眼。
4. **批量可管**：一条命令可同时下发到多台已连接主机，并带危险命令拦截。

非目标（本版不做）：见第 7 节路线图。

## 2. 现状与问题

| 编号 | 现状 | 问题 |
|---|---|---|
| a | `internal/remotessh/auth.go:103-115`：known_hosts 存在则用，缺失则 `InsecureIgnoreHostKey()` | 首次连接不校验指纹；指纹变更不报错 |
| b | README「已知限制」：开启鼠标后系统文本选择/复制不可用 | 输出无法复制 |
| c | `internal/vt/terminal.go` 只暴露 `Render()` | 5000 行 scrollback 无法检索 |
| d | `actions.go` 的 `runCommand` 只发给当前会话 | 批量运维缺失 |

## 3. 范围

| 编号 | 功能 | 主要落点 |
|---|---|---|
| F1 | known_hosts 指纹确认 + 变更拒绝 | `internal/remotessh/trust.go`（新增）、`auth.go`、`session.go`/`manager.go`、`internal/ui/model.go`+`dialogs.go` |
| F2 | 终端文本选择 + 复制 + 粘贴 | `internal/vt/terminal.go`、`internal/ui/selection.go`（新增）、`theme.go`、`keys.go`、`main.go` |
| F3 | 回滚缓冲搜索 | `internal/vt/terminal.go`、`internal/ui/find.go`（新增）、`panels.go` |
| F4 | 多会话广播命令 + 危险命令确认 | `internal/ui/actions.go`、`keys.go`、`panels.go` |

---

## 8. 评审记录（2026-09-17 评审会）

三位评审人独立评审了初版方案，结论均为「不通过（有条件通过）」。共收到 24 条意见，采纳 18 条，部分采纳 4 条，不采纳 2 条。

### 8.1 采纳并已并入设计（必须修改项）

| 来源 | 意见 | 修订后的做法 |
|---|---|---|
| 安全 | 后台 goroutine 阻塞在指纹询问上会泄漏/死锁（`ClientConfig.Timeout` 只限 TCP，关会话也取消不了握手） | **取消阻塞式 asker**。HostKeyCallback 遇到未知/变更主机直接返回哨兵错误 `*HostKeyError`，经 `fail()` 转成新事件 `EventNeedHostKey`，由 UI 主线程弹框；用户同意后 UI 写 known_hosts 再 `Reopen`。不存在阻塞与超时 |
| 安全 | 禁止 `close(AskCh)`（会 send on closed channel 崩溃） | 已无此通道，问题消失 |
| 安全 | 单对话框会被并发询问覆盖 | UI 侧维护 `hostKeyQueue` FIFO 队列，逐个弹出，按请求处理完再弹下一个 |
| 安全 | `Ctrl+Q`/`Ctrl+H` 直接 `m.dlg=nil` 会绕过 `onCancel` | 改为调用 `closeDialog()` 统一走取消回调 |
| 安全 | `RunOnce`（密钥推送，协程内）无交互能力 | 走非交互 strict 分支：未知主机直接拒绝并给出可操作提示，**不再回退 `InsecureIgnoreHostKey`** |
| 安全 | known_hosts 落盘需原子性 | `trust.go` 内包级 `sync.Mutex` + `O_APPEND` 单次写入完整 `knownhosts.Line()` + `Sync`；写入前重读判重；目录 0700、文件 0600 |
| 安全 | 安全默认值 | 见 8.2 第 1 条（改为常量严格校验） |
| 安全 | `risky()` 正则误伤 `echo a > f`、`cat reboot.log`，漏报 `rm -rf /*`、`dd of=/dev/sda` | 改为**结构化判定**：按 `; && \|\| \|` 分段 → 剥 `sudo`/`env` → 取命令首词 → 按命令分派目标检查（见 F4 步骤 4）。命中只弹确认、列主机，不静默拦截 |
| 安全 | 广播时应提示目标主机 | 广播前展示将发往哪些主机，并在确认框明示「sudo 口令会同时发到全部主机」 |
| 架构 | 视口行 → 缓冲行换算：`idx = (history.len() - scrollOffset) + y`，且 `histRing.get` 必须取模 | 所有换算集中到 `terminal.go` 的 `viewTopLocked()` 一个函数里，UI 不自行算 |
| 架构 | `histRing` 环形写满后索引整体左移，`[]int` 形式的命中会漂移失效 | `Find` 返回 `[]Hit{Line, Text}`，跳转前校验 `LineText(Line) == Text`，不一致就重跑 Find |
| 架构 | `\x1b[7m` 字符串后处理不可行：`ansiCut` 行首就是 `\x1b[0m`，反显会被立刻清掉，且区间内颜色丢失 | **选区渲染下沉到 vt**：`Terminal.SetSelection()` 存视口坐标，`renderLine` 命中格时 `a.Reverse = true`（`Attr.Reverse` 已存在于 `color.go:38`，SGR 输出 `;7`）。宽度天然不变，也绕开 `padVisible`/`boxStyle` 的宽度约束 |
| 架构 | `ScrollBy` 是相对量且两次加锁不原子 | 新增 `ScrollToLine(idx int) bool`，锁内计算并居中 |
| 架构 | 取文本/搜索要一次性持锁 | `LineText`/`Text`/`Find` 单次加锁内完成；`Find` 在锁内先把行拼成字符串快照，避免长期占锁阻塞 `Write`/`Render` |
| 交互 | `Ctrl+A` 与命令行行首（`keys.go:261`）及 GNU screen 前缀冲突；`Ctrl+F` 是 less/vim 翻页与 readline forward-char；`Alt+N/P` 是 bash M-n/M-p | 键位改为：广播 **Alt+A**、搜索 **Alt+/**、上一处/下一处 **Alt+[ / Alt+]**、选择模式 **Alt+S**（无冲突） |
| 交互 | 锚点会随输出/环形溢出漂移 | 进入选择即**冻结视口**（`frozenTop`），复制或退出才恢复；`switchTo`、会话关闭、`resize` 三处强制复位选择态 |
| 交互 | 状态叠加打架 | 新增 `resetTermModes()`，在 `setFocus`/`switchTo`/会话事件/`resize` 统一归零；搜索不复用 `filtering`，用独立 `searchMode` |
| 交互 | 指纹确认框只认 Enter、`y` 键太危险 | 指纹框 `okLabel` 明确为「信任并写入 known_hosts」，取消即拒绝 |

### 8.2 部分采纳 / 调整

1. **取消 `Settings.StrictHostKey` 字段**：`internal/store/types.go` 头部明确声明「结构体与常量为冻结契约，不得修改」，无法给 `Settings` 加字段。改为：**始终严格校验 + 交互式确认**；逃生舱用环境变量 `SSHTOOL_INSECURE_HOST_KEYS=1`（仅对无法交互的场景如密钥推送之外的场景生效），并在 README 说明。这样既满足「安全默认 true」，又不破坏契约。
2. **剪贴板依赖**：评审建议 OSC52 为主、砍掉 `atotto/clipboard`。采纳「不新增依赖」，但也不引入 `go-osc52`（其 API 在 v2.0.1 下需额外确认），改为**手写 OSC 52 序列**（base64 + `\x1b]52;c;…\x1b\\`，约 5 行）；终端不支持时回退写入 `~/.sshtool/clipboard.txt` 并在状态栏给出路径。
3. **会话关闭/Resize 复位**：采纳，但只复位选择态与搜索高亮，保留搜索关键字以便继续查找。
4. **文件命名**：采纳评审建议，`select.go`/`search.go` 改名为 `selection.go`/`find.go`，并只放状态与纯逻辑，按键与渲染仍留在 `keys.go`/`panels.go`。

### 8.3 不采纳（含理由）

1. **历史命令脱敏**：属于独立议题，且会影响「历史复用」这一核心价值；本期不做，记录进路线图。
2. **`go mod tidy` 批量转正 `// indirect` 依赖**：与本期功能无关，且会改动 go.mod/go.sum 的既有标注，留到发布前单独处理。

### 8.4 评审结论

三位评审人提出的必须修改项已全部并入修订版，**方案通过，可进入任务拆解与实现**。

---

## 9. 任务清单（输入 → 输出 → 验收）

| # | 任务 | 输入 | 输出 | 验收标准 |
|---|---|---|---|---|
| T1 | known_hosts 指纹校验与落盘 | `auth.go`/`session.go`/`manager.go` 现状；`golang.org/x/crypto/ssh/knownhosts` | 新增 `internal/remotessh/trust.go`；`HostKeyError` 哨兵错误；`EventNeedHostKey`；`Session.Secret()`；`trust_test.go` | ① 未知主机连接 → 返回 `HostKeyError` 而非静默通过 ② 指纹变更 → 明确报错且拒绝连接 ③ `TrustHost` 写入的条目可被 `knownhosts.New` 再次验证通过 ④ 并发 8 goroutine 写同一文件不产生残缺行 ⑤ `go test ./internal/remotessh` 通过 |
| T2 | UI 指纹确认与重连 | T1 的事件；`dialogs.go` 的 `newConfirmDialog`；`model.go` 事件分发 | `hostKeyQueue` FIFO；确认对话框（含指纹/地址/风险说明）；同意后落盘+`Reopen` 复用已存 secret | ① 事件到达即弹框，取消则维持错误态 ② 确认后 known_hosts 新增一行且会话重连成功 ③ 多路询问不丢单（队列逐个弹出） ④ `Ctrl+Q`/`Ctrl+H` 关闭对话框走取消语义 |
| T3 | vt 文本/检索/跳转 API | `terminal.go` 的 `histRing`/`screen`/`scrollOffset`；`color.go` 的 `Attr.Reverse` | `LineText`、`Text`、`Find`（返回 `[]Hit`）、`ScrollToLine`、`SetHighlight`；`vt` 单元测试 | ① `Text` 跨行/宽字符/越界裁剪正确 ② `Find` 命中按缓冲顺序升序，覆盖 history+screen ③ `ScrollToLine` 后目标行进入视口 ④ 命中行渲染带反显且 `ansiWidth` 不变 |
| T4 | 终端选择 + 复制 + 粘贴 | T3 的 `Text`；`Attr.Reverse`；OSC 52 | `vt.SetSelection/ClearSelection/FreezeScroll`；`internal/ui/selection.go`；`Alt+S` 与鼠标拖选；复制出口与文件兜底；`selection_test.go` | ① `Alt+S` 进入后按键不透传远端 ② 选择期间新输出到达，选区内容不变 ③ 复制出口写出正确 OSC52 序列（用 spy 验证） ④ 会话关闭/resize/切会话自动复位 ⑤ 退出后恢复跟随最新输出 |
| T5 | 回滚缓冲搜索 | T3 的 `Find`/`ScrollToLine`/`SetHighlight` | `internal/ui/find.go`；`Alt+/` 打开、`Alt+[`/`Alt+]` 跳转；状态栏 `n/N`；命中行反显 | ① 输入关键字后跳到最近命中并居中 ② `Alt+]` 到末尾后回到第一条（环绕） ③ 缓冲变化导致命中漂移时能自愈（重跑 Find） ④ `Esc` 清除高亮 ⑤ 状态栏显示 `3/17` |
| T6 | 广播命令 + 危险命令确认 | `actions.go` 的 `runCommand`；会话状态机 | `broadcast` 状态 + `Alt+A`；`risky()` 结构化判定；确认对话框；提示符/状态栏 `[广播]` | ① 广播时每个已连接会话各收到一次命令 ② 危险命令弹出确认，取消则不发送 ③ `echo a > f` 不被误拦，`rm -rf /` 被拦 ④ 历史只记一条 ⑤ 无会话时 `Alt+A` 给出提示而非静默失败 |
| T7 | 文档、帮助与全量回归 | 全部实现；README 现状 | README 增补四节 + 快捷键表 + 鼠标表 + 已知限制修订；`openHelp` 增补条目；构建产物 | ① `go build ./...`、`go vet ./...`、`go test ./...` 全绿 ② `build.ps1` 产出可运行二进制并 `-v` 可查版本 ③ README 与实现一致（键位、行为、限制） |

---

## 7. 后续路线图（本期不做）

1. 历史命令脱敏（密码/令牌不入库）
2. SFTP 双向传输（上传/下载/进度/重命名/删除/chmod）
3. 心跳保活与断线自动重连
4. 会话日志落盘与回放
5. `~/.ssh/config` 导入导出 + 分组树折叠
6. 主题切换（`settings.theme` 目前未生效）
7. ssh-agent 集成、私钥口令缓存
8. 本地 shell 标签
9. 端口转发、ProxyJump

## 6. 风险与缓解

| 编号 | 风险 | 缓解 |
|---|---|---|
| R1 | 首次连接弹出指纹框，Windows 无 `~/.ssh` 的用户每个主机都要确认一次 | 与 OpenSSH 行为一致；一次确认永久生效（落盘 known_hosts） |
| R2 | 重连需重新输入密码 | `Session` 内存保存 secret，重连复用；未存密码时才走 `EventNeedSecret` |
| R3 | 环形缓冲溢出导致命中/选区漂移 | `Find` 结果带行文本校验；选区冻结 + 溢出时 `frozenTop` 递减 |
| R4 | `WithMouseAllMotion` 事件量上升 | 非拖选状态直接 return；保留 `Alt+S` 纯键盘路径 |
| R5 | 终端不支持 OSC 52 | 回退写 `~/.sshtool/clipboard.txt` 并在状态栏给出路径 |
| R6 | 广播误操作影响面大 | 危险命令确认 + `[广播]` 常驻标识 + 随时 `Alt+A` 退出 |
