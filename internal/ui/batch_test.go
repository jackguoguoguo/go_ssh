package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestBatchMetaNoConnections 验证 `batch` 元命令在无连接时只提示、不弹对话框。
func TestBatchMetaNoConnections(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)

	if !m.runMetaCommand("batch") {
		t.Fatal("batch 应被识别为元命令")
	}
	if m.dlg != nil {
		t.Fatalf("无连接时不应弹对话框，实际 kind=%v", m.dlg.kind)
	}
	if !strings.Contains(m.msg, "没有匹配") {
		t.Fatalf("应提示没有匹配的连接，实际：%q", m.msg)
	}
}

func TestBatchMetaWithConnections(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	st.AddConnection(store.Connection{Name: "web-01", Host: "1.2.3.4", Port: 22, User: "root", AuthType: store.AuthPassword})
	st.AddConnection(store.Connection{Name: "db-01", Host: "5.6.7.8", Port: 22, User: "root", AuthType: store.AuthPassword})
	m := New(st, mgr)

	m.runMetaCommand("batch web")
	if m.dlg == nil || m.dlg.kind != dlgForm {
		t.Fatalf("带过滤词应直接进入命令输入对话框，实际 kind=%v", m.dlg.kind)
	}
	// 标题应只提到匹配的一台
	if !strings.Contains(m.dlg.title, "1 台") {
		t.Fatalf("标题应显示匹配台数，实际：%q", m.dlg.title)
	}
}

// TestBatchCmdInputPrefilled 验证上一次命令被预填（便于重复执行）。
func TestBatchCmdInputPrefilled(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	conn := store.Connection{Name: "web-01", Host: "1.2.3.4", Port: 22, User: "root", AuthType: store.AuthPassword}
	st.AddConnection(conn)
	m := New(st, mgr)

	m.lastBatchCmd = "uptime"
	m.openBatchCmdInput([]store.Connection{conn})
	if m.dlg == nil || m.dlg.kind != dlgForm {
		t.Fatalf("应进入命令输入对话框，实际 kind=%v", m.dlg.kind)
	}
	v := string(m.dlg.fields[0].value)
	if v != "uptime" {
		t.Fatalf("命令输入应预填上次命令，实际 %q", v)
	}
}

// TestShowBatchResult 验证结果对话框的展示与状态栏消息。
func TestShowBatchResult(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.showBatchResult(batchDoneMsg{
		cmd: "df -h",
		results: []remotessh.BatchResult{
			{Name: "web-01", Host: "root@1.2.3.4:22", Stdout: "Filesystem      Size  Used\n/dev/sda1        40G   12G", Code: 0},
			{Name: "db-01", Host: "root@5.6.7.8:22", Err: "认证失败", Code: -1},
		},
	})

	if m.dlg == nil || m.dlg.kind != dlgText {
		t.Fatalf("结果应以 dlgText 展示，实际 kind=%v", m.dlg.kind)
	}
	var joined string
	for _, line := range m.dlg.body {
		joined += line + "\n"
	}
	if !strings.Contains(joined, " df -h ") && !strings.Contains(joined, "$ df -h") {
		t.Fatalf("结果页应含命令，实际：\n%q", joined)
	}
	if !strings.Contains(joined, "1 台成功 · 1 台失败") {
		t.Fatalf("汇总统计应正确，实际：\n%q", joined)
	}
	if !strings.Contains(joined, "web-01") || !strings.Contains(joined, "db-01") {
		t.Fatalf("结果页应列出两台主机，实际：\n%q", joined)
	}
	if !strings.Contains(m.msg, "1 成功 / 1 失败") {
		t.Fatalf("状态栏应报告失败数，实际：%q", m.msg)
	}

	// 无失败时的状态栏
	m.showBatchResult(batchDoneMsg{
		cmd: "uptime",
		results: []remotessh.BatchResult{
			{Name: "web-01", Host: "root@1.2.3.4:22", Stdout: " 12:01:00 up 1 day", Code: 0},
		},
	})
	if !strings.Contains(m.msg, "1 台全部成功") {
		t.Fatalf("全成功时状态栏应报告，实际：%q", m.msg)
	}
}

// TestTrimmedOutput 验证长输出被裁剪为有界行数。
func TestTrimmedOutput(t *testing.T) {
	var long string
	for i := 0; i < 50; i++ {
		long += "line-" + fmt.Sprintf("%d", i) + "\n"
	}
	lines := trimmedOutput(long)
	if len(lines) > 12 {
		t.Fatalf("裁剪后应不超过 12 行，实际 %d", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "截断") {
		t.Fatalf("裁剪标记应出现，实际最后一行：%q", lines[len(lines)-1])
	}
}