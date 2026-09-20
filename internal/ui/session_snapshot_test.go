package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// TestSaveSessionSnapshot 验证打开的 SSH 会话被记录（按标签顺序）且活动会话被保存。
func TestSaveSessionSnapshot(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30
	pm := &m

	s1 := connectTestSession(t, pm, st)
	s2 := connectTestSession(t, pm, st)
	pm.activeID = s2.ID
	pm.saveSessionSnapshot()

	snap, err := st.LoadSessions()
	if err != nil {
		t.Fatalf("加载快照失败：%v", err)
	}
	if len(snap.OpenIDs) != 2 || snap.OpenIDs[0] != s1.ID || snap.OpenIDs[1] != s2.ID {
		t.Fatalf("快照应记录两个会话且顺序正确，实际 %v", snap.OpenIDs)
	}
	if snap.ActiveID != s2.ID {
		t.Fatalf("活动会话应为 %s，实际 %q", s2.ID, snap.ActiveID)
	}
}

// TestRestorePromptSkipsSecretNeeding 验证需要询问密码的会话在复原时被跳过。
func TestRestorePromptSkipsSecretNeeding(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	c1 := st.AddConnection(store.Connection{Name: "ok", Host: "h1", Port: 22, User: "root", AuthType: store.AuthPassword, Password: "pw"})
	c2 := st.AddConnection(store.Connection{Name: "ask", Host: "h2", Port: 22, User: "root", AuthType: store.AuthPassword, AskPassword: true})
	if err := st.SaveSessions(store.SessionSnapshot{OpenIDs: []string{c1.ID, c2.ID}}); err != nil {
		t.Fatal(err)
	}

	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr) // New 会触发复原询问

	if m.dlg == nil || m.dlg.title != "恢复上次会话" {
		t.Fatalf("应弹出复原确认，实际 %+v", m.dlg)
	}
	if !strings.Contains(m.dlg.message, "1 个会话") {
		t.Fatalf("应只提示 1 个可复原会话，实际：%q", m.dlg.message)
	}
	if !strings.Contains(m.dlg.message, "跳过") {
		t.Fatalf("应说明有会话被跳过，实际：%q", m.dlg.message)
	}
}

// TestRestorePromptDisabledByEnv 验证 SSHTOOL_NO_RESTORE=1 时不弹复原询问。
func TestRestorePromptDisabledByEnv(t *testing.T) {
	t.Setenv("SSHTOOL_NO_RESTORE", "1")
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	c := st.AddConnection(store.Connection{Name: "ok", Host: "h1", Port: 22, User: "root", AuthType: store.AuthPassword, Password: "pw"})
	_ = st.SaveSessions(store.SessionSnapshot{OpenIDs: []string{c.ID}})

	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	if m.dlg != nil {
		t.Fatalf("设置了 SSHTOOL_NO_RESTORE 时不应弹复原询问，实际 %+v", m.dlg)
	}
}

// TestRestorePromptReconnects 验证确认后真实重连会话。
func TestRestorePromptReconnects(t *testing.T) {
	srv := testutil.StartSSHServer(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	c := st.AddConnection(store.Connection{
		Name: "srv", Host: srv.Host, Port: srv.Port, User: "test",
		AuthType: store.AuthPassword, Password: "pass",
	})
	if err := st.SaveSessions(store.SessionSnapshot{OpenIDs: []string{c.ID}, ActiveID: c.ID}); err != nil {
		t.Fatal(err)
	}

	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30
	if m.dlg == nil {
		t.Fatal("应弹出复原确认")
	}

	// 确认复原
	m.dlg.onOK(&m, nil)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := mgr.Get(c.ID); ok && s.State() == remotessh.StateConnected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("复原后会话未连接成功")
}