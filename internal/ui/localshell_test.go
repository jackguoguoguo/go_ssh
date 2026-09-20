package ui

import (
	"path/filepath"
	"testing"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestOpenLocalShellTab 验证「本地 shell」标签能打开、成为活动标签并能关闭。
func TestOpenLocalShellTab(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	m.width, m.height = 120, 30

	if !m.runMetaCommand("shell") {
		t.Fatal("shell 应被识别为元命令")
	}
	if len(m.locals) == 0 {
		t.Skipf("本机无法打开 PTY（%s），跳过", m.msg)
	}
	if len(m.locals) != 1 {
		t.Fatalf("应打开 1 个本地 shell，得到 %d", len(m.locals))
	}
	if m.activeID != m.locals[0].TabID() {
		t.Fatal("新标签应成为活动标签")
	}
	m.refreshSessions()
	if len(m.sessions) != 1 {
		t.Fatalf("标签栏应有 1 个会话，得到 %d", len(m.sessions))
	}
	if !isLocalShell(m.activeSession()) {
		t.Fatal("活动会话应为本地 shell")
	}

	// 关闭后不应残留
	m.closeActiveSession()
	if len(m.locals) != 0 {
		t.Fatalf("关闭后不应残留本地 shell，得到 %d", len(m.locals))
	}
}

// TestLocalShellNotInBroadcast 验证本地 shell 不参与广播（避免把命令误发到本机）。
func TestLocalShellNotInBroadcast(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	m.width, m.height = 120, 30

	if !m.runMetaCommand("shell") {
		t.Fatal("shell 应被识别为元命令")
	}
	if len(m.locals) == 0 {
		t.Skipf("本机无法打开 PTY（%s），跳过", m.msg)
	}
	if n := len(m.connectedSessions()); n != 0 {
		t.Fatalf("本地 shell 不应计入可广播会话，得到 %d", n)
	}
	m.locals[0].Close()
}
