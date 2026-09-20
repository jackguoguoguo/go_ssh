package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestPingMetaNoConnections 验证 `ping` 无连接时提示、不弹对话框。
func TestPingMetaNoConnections(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)

	if !m.runMetaCommand("ping") {
		t.Fatal("ping 应被识别为元命令")
	}
	if m.dlg != nil {
		t.Fatalf("无连接时不应弹对话框，实际 kind=%v", m.dlg.kind)
	}
	if !strings.Contains(m.msg, "没有匹配") {
		t.Fatalf("应提示没有匹配的连接，实际：%q", m.msg)
	}
}

// TestPingMetaStartsCheck 验证有连接时 `ping` 直接进入巡检（进度对话框 + afterCmd）。
func TestPingMetaStartsCheck(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	st.AddConnection(store.Connection{Name: "web-01", Host: "1.2.3.4", Port: 22, User: "root", AuthType: store.AuthPassword})
	m := New(st, mgr)
	m.width, m.height = 120, 30

	if !m.runMetaCommand("ping") {
		t.Fatal("ping 应被识别为元命令")
	}
	if m.dlg == nil || m.dlg.kind != dlgText {
		t.Fatalf("应进入进度对话框，实际 kind=%v", m.dlg.kind)
	}
	if m.afterCmd == nil {
		t.Fatal("应派生存后台巡检命令")
	}
}

// TestShowPingResult 验证巡检结果对话框。
func TestShowPingResult(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.showPingResult(pingDoneMsg{
		results: []remotessh.PingResult{
			{Name: "web-01", Host: "root@1.2.3.4:22", Status: remotessh.PingOK},
			{Name: "db-01", Host: "root@5.6.7.8:22", Status: remotessh.PingAuthFail, Detail: "user has no permission"},
			{Name: "bak-01", Host: "root@9.9.9.9:22", Status: remotessh.PingUnreachable, Detail: "connection refused"},
		},
	})

	if m.dlg == nil || m.dlg.kind != dlgText {
		t.Fatalf("结果应以 dlgText 展示，实际 kind=%v", m.dlg.kind)
	}
	var joined string
	for _, line := range m.dlg.body {
		joined += line + "\n"
	}
	if !strings.Contains(joined, "1 台可达 · 1 认证失败 · 1 不可达") {
		t.Fatalf("汇总统计应正确，实际：\n%q", joined)
	}
	for _, name := range []string{"web-01", "db-01", "bak-01"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("结果页应列出 %s，实际：\n%q", name, joined)
		}
	}
	if !strings.Contains(joined, "认证失败") || !strings.Contains(joined, "不可达") {
		t.Fatalf("状态文本应出现，实际：\n%q", joined)
	}
	if !strings.Contains(m.msg, "1 可达 / 2 异常") {
		t.Fatalf("状态栏应报告异常数，实际：%q", m.msg)
	}

	// 全部可达
	m.showPingResult(pingDoneMsg{
		results: []remotessh.PingResult{
			{Name: "web-01", Host: "root@1.2.3.4:22", Status: remotessh.PingOK},
		},
	})
	if !strings.Contains(m.msg, "全部可达") {
		t.Fatalf("全可达时状态栏应报告，实际：%q", m.msg)
	}
}