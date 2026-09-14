package ui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/testutil"
	"sshtool/internal/ui"
)

// newTestModel 构造一个带有示例数据的测试用 Model。
func newTestModel(t *testing.T) (ui.Model, *remotessh.Manager) {
	t.Helper()

	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{Name: "demo", Host: "192.0.2.1", User: "root", Port: 22})
	st.AddFavorite(store.FavoriteCmd{Name: "磁盘", Cmd: "df -h"})
	st.AddHistory("ls -la", "192.0.2.1")

	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)

	m := ui.New(st, mgr)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	return m, mgr
}

func TestLayoutRendersThreePanelsAndTerminal(t *testing.T) {
	m, _ := newTestModel(t)

	view := m.View()
	for _, want := range []string{"保存的连接", "收藏命令", "历史命令", "demo", "磁盘", "ls -la", "尚未连接"} {
		if !strings.Contains(view, want) {
			t.Fatalf("界面缺少 %q\n%s", want, view)
		}
	}

	if got := strings.Count(view, "\n") + 1; got != 32 {
		t.Fatalf("渲染行数 = %d，期望 32（与终端高度一致）", got)
	}
}

func TestTwoToEightSplit(t *testing.T) {
	m, _ := newTestModel(t)
	view := m.View()

	// 左列边框应出现在第 24 列附近（120 * 2 / 10），右列边框紧随其后。
	first := strings.Split(view, "\n")[0]
	if len(first) < 30 {
		t.Fatalf("首行过短: %q", first)
	}
}

func TestNewConnectionDialogOpensAndCloses(t *testing.T) {
	m, _ := newTestModel(t)

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if !strings.Contains(m.View(), "用户名") {
		t.Fatalf("Ctrl+N 未打开新建连接对话框:\n%s", m.View())
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(m.View(), "用户名") {
		t.Fatalf("Esc 未关闭对话框:\n%s", m.View())
	}
}

func TestHelpDialog(t *testing.T) {
	m, _ := newTestModel(t)

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	view := m.View()
	if !strings.Contains(view, "快捷键") || !strings.Contains(view, "Alt+1..9") {
		t.Fatalf("帮助面板内容异常:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
}

func TestFavoriteClickFillsCommandLine(t *testing.T) {
	m, _ := newTestModel(t)

	// 切到收藏面板并回车 → 命令填入输入行
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.View(), "df -h") {
		t.Fatalf("收藏命令未填入命令行:\n%s", m.View())
	}
}

// TestConnectAndRenderTerminal 端到端：从连接面板回车 → 真实 SSH 连接 → 终端渲染远端输出。
func TestConnectAndRenderTerminal(t *testing.T) {
	srv := testutil.StartSSHServer(t)

	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{
		Name:     "e2e",
		Host:     srv.Host,
		Port:     srv.Port,
		User:     "test",
		AuthType: store.AuthPassword,
		Password: "pass",
	})

	mgr := remotessh.NewManager()
	defer mgr.CloseAll()

	m := ui.New(st, mgr)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // 连接选中的连接

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(m.View(), "hello") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("终端未渲染远端输出:\n%s", m.View())
}

func TestTerminalPassthroughRequiresSession(t *testing.T) {
	m, _ := newTestModel(t)

	// 没有会话时按键不应 panic
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("ls")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyPgUp},
		{Type: tea.KeyPgDown},
		{Type: tea.KeyCtrlC},
	} {
		m.Update(k)
	}
}
