// sshtool：一个终端里的 SSH 多会话管理工具。
//
// 布局：左侧 20%（保存的连接 / 收藏命令 / 历史命令），右侧 80%（终端）。
// 快捷键见 Ctrl+H，鼠标可直接点击操作。
package main

import (
	"fmt"
	"os"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/cli"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/ui"
)

// version 由构建脚本通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version", "version":
			fmt.Printf("sshtool %s (%s/%s, go %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
			fmt.Printf("配置文件: %s\n", store.DefaultPath())
			return
		case "exec", "ping":
			// 非交互子命令：复用 TUI 的批量执行 / 巡检内核，供脚本与 CI 使用。
			st, err := store.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "警告：加载配置失败，将使用空配置。", err)
			}
			if st == nil {
				st = store.New(store.DefaultPath())
			}
			var code int
			if os.Args[1] == "exec" {
				code = cli.Exec(st, os.Args[2:], os.Stdout, os.Stderr)
			} else {
				code = cli.Ping(st, os.Args[2:], os.Stdout, os.Stderr)
			}
			os.Exit(code)
		case "-h", "--help":
			fmt.Println("用法:")
			fmt.Println("  sshtool                      启动交互式 TUI（按 Ctrl+H 查看快捷键）")
			fmt.Println("  sshtool exec  [选项] <命令>  对多台主机批量执行命令（headless）")
			fmt.Println("  sshtool ping  [选项]         对多台主机做连通性巡检（headless）")
			fmt.Println("  sshtool -v | --version       查看版本与配置文件路径")
			fmt.Println("exec/ping 选项: -g 关键词（过滤目标） · --timeout 秒 · -f text|json|plain")
			return
		}
	}

	st, err := store.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "警告：加载配置失败，将使用空配置。", err)
	}
	if st == nil {
		st = store.New(store.DefaultPath())
	}

	mgr := remotessh.NewManager()
	defer mgr.CloseAll()

	model := ui.New(st, mgr)
	p := tea.NewProgram(&model,
		tea.WithAltScreen(),
		// all-motion 用于支持「按下-拖动-松开」的终端拖选；
		// 非拖选状态的 motion 事件在 keys.onMouse 里被直接丢弃，不会有额外开销。
		tea.WithMouseAllMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "运行出错：", err)
		_ = st.Save()
		os.Exit(1)
	}

	// 退出时落盘，保存历史命令与连接变更。
	if err := st.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "警告：保存配置失败。", err)
	}
}
