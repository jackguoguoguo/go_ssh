package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/keytool"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestAuditNoConnectionsPrompts 验证无连接时盘点给出提示。
func TestAuditNoConnectionsPrompts(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openAuditPickTargets()
	if m.dlg == nil || m.dlg.kind != dlgText {
		t.Fatalf("无连接时应提示，实际 kind=%v", m.dlg.kind)
	}
}

// TestAuditPickerShowsTargets 验证有连接时打开多选目标选择器。
func TestAuditPickerShowsTargets(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	st.AddConnection(store.Connection{Name: "web-01", Group: "prod", Host: "1.1.1.1", Port: 22, User: "root", AuthType: store.AuthPassword})
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openAuditPickTargets()
	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应打开选择器，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.items) != 1 || !m.dlg.multi {
		t.Fatalf("应列出 1 台且为多选：items=%d multi=%v", len(m.dlg.items), m.dlg.multi)
	}
}

// TestShowAuditResultSummarizes 验证盘点结果页的汇总与逐台展示。
func TestShowAuditResultSummarizes(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.showAuditResult(auditDoneMsg{
		path:  "~/.ssh/authorized_keys",
		local: []keytool.KeyInfo{{Fingerprint: "SHA256:me"}},
		results: []keytool.AuditResult{
			{Name: "ok-01", Host: "root@1.1.1.1:22", Total: 1, Authorized: []string{"SHA256:me"}},
			{Name: "need-01", Host: "root@2.2.2.2:22", Total: 2, Missing: []string{"SHA256:me"},
				Stale: []keytool.KeyInfo{{Alg: "ssh-ed25519", Comment: "old@key"}}},
			{Name: "bad-01", Host: "root@3.3.3.3:22", Err: "认证失败"},
		},
	})

	if m.dlg == nil || m.dlg.kind != dlgText {
		t.Fatalf("应展示结果页，实际 kind=%v", m.dlg.kind)
	}
	var joined string
	for _, line := range m.dlg.body {
		joined += line + "\n"
	}
	if !strings.Contains(joined, "已全授权 1 台 · 待推送 1 台 · 含废弃条目 1 台 · 失败 1 台") {
		t.Fatalf("汇总统计应正确：\n%s", joined)
	}
	for _, want := range []string{"ok-01", "need-01", "bad-01", "old@key", "认证失败"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("结果页应含 %q：\n%s", want, joined)
		}
	}
	if !strings.Contains(m.msg, "1 台待推送") {
		t.Fatalf("状态栏应报告待推送数，实际：%q", m.msg)
	}
}
