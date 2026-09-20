package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

func TestHandleForwardNoActiveSession(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)

	// 无参数：应给出用法提示。
	m.handleForward(nil)
	if !strings.Contains(m.msg, "用法") {
		t.Fatalf("无参数应提示用法，实际：%q", m.msg)
	}

	// 无活动 SSH 会话：应提示需要已连接的 SSH 会话。
	m.handleForward([]string{"L", "8080", "example.com:80"})
	if !strings.Contains(m.msg, "SSH 会话") {
		t.Fatalf("无 SSH 会话应提示，实际：%q", m.msg)
	}

	// 未知转发类型：应提示用 L/R。
	m.handleForward([]string{"X", "8080", "x:80"})
	if !strings.Contains(m.msg, "L") && !strings.Contains(m.msg, "R") {
		t.Fatalf("未知类型应提示 L/R，实际：%q", m.msg)
	}
}
