package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestReplayOpensForLoggingSession 验证：开启 SSHTOOL_LOG_DIR 后，会话把终端输出落盘，
// 按 Ctrl+L 能从磁盘日志打开回放视图且内容一致；按 Esc 可退出。
func TestReplayOpensForLoggingSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SSHTOOL_LOG_DIR", dir)

	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	mm := &m
	mm.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	s := connectTestSession(t, mm, st)
	if !s.Logging() {
		t.Fatal("会话应处于记录状态")
	}
	path := s.LogPath()

	// 通过真实会话产生输出，并等待其落盘到日志文件（回放的数据源是磁盘文件）。
	s.Write([]byte("echo REPLAY_MARKER\r"))
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "REPLAY_MARKER") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "REPLAY_MARKER") {
		t.Fatalf("日志文件未包含 REPLAY_MARKER：\n%s", string(data))
	}

	mm.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if mm.replay == nil {
		t.Fatalf("Ctrl+L 未打开回放视图；提示=%q", mm.msg)
	}
	view := mm.renderReplay()
	if !strings.Contains(view, "REPLAY_MARKER") {
		t.Fatalf("回放内容缺少 REPLAY_MARKER：\n%s", view)
	}

	mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.replay != nil {
		t.Fatal("Esc 未关闭回放视图")
	}
}

// TestReplayWithoutLoggingShowsHint 验证：未开启日志时按 Ctrl+L 不打开回放，仅给出提示。
func TestReplayWithoutLoggingShowsHint(t *testing.T) {
	t.Setenv("SSHTOOL_LOG_DIR", "")

	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	mm := &m
	mm.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	_ = connectTestSession(t, mm, st)

	mm.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if mm.replay != nil {
		t.Fatal("未开启日志时不应打开回放视图")
	}
	if !strings.Contains(mm.msg, "未记录日志") {
		t.Fatalf("应提示未记录日志，得到 %q", mm.msg)
	}
}
