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
		case "-h", "--help":
			fmt.Println("用法: sshtool [-v|--version]")
			fmt.Println("终端内 SSH 多会话管理工具，启动后按 Ctrl+H 查看快捷键。")
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
		tea.WithMouseCellMotion(),
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
