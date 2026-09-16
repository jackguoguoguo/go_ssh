package ui

import (
	"path"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

func TestFilePathHelpers(t *testing.T) {
	if got := joinPath("~", "a.txt"); got != "~/a.txt" {
		t.Fatalf("joinPath(~,a.txt)=%q", got)
	}
	if got := joinPath("/home/u", "a.txt"); got != "/home/u/a.txt" {
		t.Fatalf("joinPath=/home/u,a.txt=%q", got)
	}
	if got := parentPath("~/a/b"); got != "~/a" {
		// ~/a/b 的上一级应为 ~/a
		t.Fatalf("parentPath(~/a/b)=%q", got)
	}
	if got := parentPath("~/a"); got != "~" {
		t.Fatalf("parentPath(~/a)=%q", got)
	}
	if got := parentPath("/a/b/c"); got != "/a/b" {
		t.Fatalf("parentPath(/a/b/c)=%q", got)
	}
	if got := parentPath("~"); got != "~" {
		t.Fatalf("parentPath(~)=%q", got)
	}

	if got := resolvePath("", "/abs"); got != "/abs" {
		t.Fatalf("resolvePath empty=/abs=%q", got)
	}
	if got := resolvePath("/a/b", "c"); got != "/a/b/c" {
		t.Fatalf("resolvePath /a/b + c=%q", got)
	}
	if got := resolvePath("/a/b", ".."); got != "/a" {
		t.Fatalf("resolvePath /a/b + ..=%q", got)
	}
	if got := resolvePath("~", "sub"); got != "~/sub" {
		t.Fatalf("resolvePath ~ + sub=%q", got)
	}

	if got := humanSize(512); got != "512" {
		t.Fatalf("humanSize(512)=%q", got)
	}
	if got := humanSize(1536); got != "1.5K" {
		t.Fatalf("humanSize(1536)=%q", got)
	}
	if got := humanSize(1024 * 1024 * 3); got != "3.0M" {
		t.Fatalf("humanSize(3M)=%q", got)
	}

	if got := padRune("abc", 5); got != "abc  " {
		t.Fatalf("padRune=%q", got)
	}
	if got := padRune("abcdef", 4); got != "abc…" {
		t.Fatalf("padRune truncate=%q", got)
	}

	d := &dlg{fsEntries: []remotessh.Entry{
		{Name: "apple"}, {Name: "banana"}, {Name: "cherry"},
	}, fsFilter: "an"}
	vis := visibleFs(d)
	if len(vis) != 1 || vis[0].Name != "banana" {
		t.Fatalf("visibleFs 过滤失败: %+v", vis)
	}
}

func TestOpenFileBrowserFlow(t *testing.T) {
	srv := testutil.StartSSHServer(t)

	st := store.New(path.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{
		Name: "fb", Host: srv.Host, Port: srv.Port, User: "test",
		AuthType: store.AuthPassword, Password: "pass",
	})

	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})

	// 连接
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if s := m.activeSession(); s != nil && s.State() == remotessh.StateConnected {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if m.activeSession() == nil {
		t.Fatal("未建立会话")
	}

	// 打开文件浏览器（Ctrl+O 返回异步加载命令）
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.dlg == nil || m.dlg.kind != dlgFile {
		t.Fatalf("Ctrl+O 未打开文件浏览器: %+v", m.dlg)
	}
	if cmd == nil {
		t.Fatal("打开文件浏览器未返回加载命令")
	}
	loaded := cmd()
	// 把加载结果喂回 Update
	m.Update(loaded)
	if m.dlg == nil || m.dlg.kind != dlgFile {
		t.Fatal("加载后对话框被关闭")
	}
	if m.dlg.fsLoading {
		t.Fatal("加载完成后仍处于 loading 状态")
	}
	if m.dlg.fsPath == "" {
		t.Fatal("文件浏览器未设置当前路径")
	}

	// Esc 关闭并释放 SFTP 资源
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dlg != nil {
		t.Fatal("Esc 未关闭文件浏览器")
	}
}
