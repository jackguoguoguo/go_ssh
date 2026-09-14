# sshtool

一个用 Go 写的 **SSH 多会话终端 TUI**：左侧管理连接/收藏/历史，右侧是真实 PTY 终端，顶部标签栏在多台服务器之间切换。鼠标可点，快捷键可盲操。

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ●1 web-01 ×  ●2 db-01 ×   ＋ 新连接      Alt+数字切换 · Ctrl+N新建 · …     │  ← 已连接服务器标签栏
├────────────────┬───────────────────────────────────────────────────────────┤
│ ① 保存的连接    │                                                           │
│   ● web-01     │   root@web-01:~# df -h                                     │
│   ● db-01      │   Filesystem      Size  Used Avail Use% Mounted on          │
│   ○ test-02    │   /dev/sda1        40G   12G   26G  32% /                   │
├────────────────┤                                                           │
│ ② 收藏命令      │   root@web-01:~# █                                         │
│   磁盘  df -h   │                                                           │
│   日志  tail -f │                                                           │
├────────────────┤                                                           │
│ ③ 历史命令      │                                                           │
│   systemctl …  │                                                           │
│   docker ps    │                                                           │
└────────────────┴───────────────────────────────────────────────────────────┘
 Tab 切换焦点 · Enter 执行 · / 过滤 · Ctrl+X 命令行 · Ctrl+W 关闭 · PgUp 回滚
```

## 特性

- **2:8 双列布局**，左列三块面板（保存的连接 / 收藏命令 / 历史命令）
- **多会话并行存活**：切换标签只是切换渲染目标，后台会话继续接收输出
- **真实 PTY**：`xterm-256color`，支持 `top` / `vim` 这类全屏程序、ANSI 256 色与 TrueColor、滚动回溯
- **两种输入模式**：直通模式（按键直达远端，远端回显）+ 命令行模式（本地编辑，回车整行发送并记录历史）
- **鼠标全可点**：标签切换 / 关闭、列表项选中、双击执行、滚轮滚动、对话框字段与按钮
- **配置持久化**：连接、收藏、历史落盘到 `~/.sshtool/config.json`（原子写 + `.bak` 备份）

## 构建与运行

需要 Go 1.21+（本项目在 Go 1.26 上开发）。

```bash
git clone https://github.com/jackguoguoguo/go_ssh.git
cd go_ssh

# 方式一：一键打包（推荐）
powershell -ExecutionPolicy Bypass -File .\build.ps1 -Version v0.1.0   # Windows
make build VERSION=v0.1.0                                              # Linux / macOS

# 方式二：直接 go build
go build -o sshtool .     # Windows: go build -o sshtool.exe .
./sshtool

# 方式三：不打包直接跑
go run .
```

打包脚本参数：

| 命令 | 说明 |
|---|---|
| `.\build.ps1` | 构建当前平台 → `dist/sshtool.exe` |
| `.\build.ps1 -All` | 交叉构建 `windows-amd64` / `linux-amd64` / `darwin-arm64` |
| `.\build.ps1 -Version v1.2.3` | 注入版本号（`sshtool -v` 可查） |
| `.\build.ps1 -Run` | 构建完成后立即启动 |
| `make build-all` | 等价的 Makefile 交叉构建目标 |

产物采用 `-trimpath -ldflags "-s -w"` 构建，体积约 6 MB。

其他命令：

```bash
go test ./...     # 运行测试（含进程内 SSH 服务端的端到端测试）
go vet ./...      # 静态检查
gofmt -w .        # 格式化
sshtool -v        # 查看版本与配置文件路径
```

> 建议在支持 256 色的终端中运行（Windows Terminal / iTerm2 / GNOME Terminal 等）。

## 快捷键

| 快捷键 | 作用 |
|---|---|
| `Alt+1..9` / `Alt+0` | 切换到第 N 个会话 / 最后一个会话 |
| `Alt+←` / `Alt+→` | 上一个 / 下一个会话 |
| `Ctrl+N` | 新建连接 |
| `Ctrl+E` / `Ctrl+D` | 编辑 / 删除选中的连接（连接面板） |
| `Enter` | 连接面板=连接；收藏/历史=填入命令行；双击同效 |
| `Ctrl+W` | 关闭当前会话 |
| `Ctrl+R` | 重连当前会话 |
| `Ctrl+B` / `Ctrl+K` | 跳到「收藏命令」/「历史命令」面板（后者直接进入过滤） |
| `Ctrl+X` | 进入命令行编辑模式（`Esc` 退出，`Enter` 发送） |
| `Ctrl+P` | 把命令行内容加入收藏 |
| `Tab` / `Shift+Tab` | 焦点在 连接 → 收藏 → 历史 → 终端 之间循环 |
| `↑` `↓` `j` `k` | 移动列表选择 |
| `/` | 过滤当前面板（`Esc` 清空并退出过滤） |
| `PgUp` / `PgDn` / 滚轮 | 终端回滚；`End` 回到最新输出 |
| `Ctrl+C` | 终端直通模式下发送给远端（SIGINT），不是退出 |
| `Ctrl+H` / `F1` | 帮助 |
| `Ctrl+Q` | 退出（二次确认，会断开所有会话） |

> `Ctrl+N/W/R/B/K/P/X/E/D/H/Q` 属于本工具的功能键，不会发送给远端；其余按键（含 `Ctrl+C`、`Ctrl+Z`、功能键、方向键）在直通模式下原样透传。

## 鼠标

| 操作 | 效果 |
|---|---|
| 点击顶部标签 | 切换到该会话 |
| 点击标签上的 `×` | 关闭该会话 |
| 单击列表项 | 选中（收藏/历史会同时填入命令行） |
| 双击列表项 | 执行（连接=连接，命令=填入命令行） |
| 滚轮 | 左侧滚动列表，右侧滚动终端 |
| 点击终端下方输入行 | 进入命令行编辑模式 |
| 对话框 | 点击字段切换焦点，点击 `确定` / `取消` 提交 |

## 配置

配置文件默认位于 `~/.sshtool/config.json`（Windows：`%USERPROFILE%\.sshtool\config.json`），首次运行自动生成。

```jsonc
{
  "connections": [{
    "id": "…", "name": "web-01", "group": "生产",
    "host": "10.0.0.1", "port": 22, "user": "root",
    "auth_type": "password",       // password | key
    "password": "",                // 留空则连接时询问
    "ask_password": true,          // 每次连接都问密码（推荐，避免明文落盘）
    "key_path": "~/.ssh/id_rsa",   // auth_type=key 时生效
    "key_passphrase": "", "ask_passphrase": false,
    "startup_cmd": "", "note": "", "favorite": false
  }],
  "favorites":  [{ "id": "…", "name": "磁盘", "cmd": "df -h", "group": "" }],
  "history":    [{ "id": "…", "cmd": "docker ps", "host": "10.0.0.1", "at": "…" }],
  "settings":   { "history_limit": 1000, "scrollback": 5000, "theme": "dark" }
}
```

主机指纹校验优先使用 `~/.ssh/known_hosts`；该文件不存在时退化为不校验（会在代码里标注 TODO）。

## 代码结构

```
main.go                  入口
internal/store/          数据模型 + JSON 持久化（原子写、历史裁剪、并发安全）
internal/vt/             ANSI 终端模拟器：CSI/OSC 解析、256 色、宽字符、滚动区、scrollback
internal/remotessh/      会话生命周期：认证、PTY、window-change、输出批量聚合、事件通知
internal/ui/             bubbletea 界面：布局、焦点状态机、快捷键、鼠标、对话框
internal/testutil/       测试用进程内 SSH 服务端
```

数据流：

```
远端 PTY ──▶ remotessh.Session 读协程（8ms 批量聚合）──▶ vt.Terminal 屏幕缓冲 ──▶ UI 渲染
UI 按键 ───▶ remotessh.Session.Write ────────────────────────────────▶ 远端 stdin
```

## 开源许可

[MIT License](LICENSE) © 2026 jackguoguoguo

可以随意使用、修改和分发（包括商业用途），唯一要求是在副本中保留版权与许可声明。软件按「原样」提供，不含任何担保。

## 已知限制

- 开启鼠标后系统终端的文本选择/复制不可用（bubbletea 的通用限制）；可用命令行 + `Ctrl+P` 收藏来复用命令
- 不支持 SFTP、端口转发、ProxyJump 跳板机（数据结构已预留 `group` 等字段，跳板机未实现）
- 终端模拟实现的是常用 CSI 子集（见 `internal/vt`），极少数冷门序列会被安全忽略
- 直通模式下不记录历史命令（避免把程序交互输出误当成命令），历史只在命令行模式发送时记录
