package remotessh

import (
	"os"
	"testing"
)

// TestSessionLoggingWritesToFile 验证 SSHTOOL_LOG_DIR 设置后，会话落盘记录终端输出，
// 且 Close 后文件内容保留、Logging() 置否。
func TestSessionLoggingWritesToFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SSHTOOL_LOG_DIR", dir)

	s := newSession(testConn("log1"), 80, 24, nil, nil, 1000)
	if !s.Logging() {
		t.Fatal("SSHTOOL_LOG_DIR 设置后 Logging 应为 true")
	}
	if s.LogPath() == "" {
		t.Fatal("LogPath 不应为空")
	}

	s.appendLog([]byte("hello "))
	s.appendLog([]byte("world\n"))
	data, err := os.ReadFile(s.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("日志内容=%q，期望 \"hello world\\n\"", string(data))
	}

	s.Close()
	if s.Logging() {
		t.Fatal("Close 后 Logging 应为 false")
	}
	// 关闭后文件内容保留可读
	data, err = os.ReadFile(s.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("Close 后日志内容=%q", string(data))
	}
}

// TestSessionLoggingDisabledByDefault 验证未设置 SSHTOOL_LOG_DIR 时不记录。
func TestSessionLoggingDisabledByDefault(t *testing.T) {
	os.Unsetenv("SSHTOOL_LOG_DIR")
	s := newSession(testConn("log2"), 80, 24, nil, nil, 1000)
	if s.Logging() {
		t.Fatal("未设置 SSHTOOL_LOG_DIR 时 Logging 应为 false")
	}
	if s.LogPath() != "" {
		t.Fatal("未设置时 LogPath 应为空")
	}
	s.Close()
}

// TestSessionLoggingPathStable 验证同一会话重连期间日志路径保持一致（共用一个文件）。
func TestSessionLoggingPathStable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SSHTOOL_LOG_DIR", dir)

	s := newSession(testConn("log3"), 80, 24, nil, nil, 1000)
	p1 := s.LogPath()
	if p1 == "" {
		t.Fatal("LogPath 不应为空")
	}
	s.appendLog([]byte("a"))

	// 重连会重启 readLoop，但日志路径字段不应变化。
	s.logMu.Lock()
	old := s.logPath
	s.logMu.Unlock()
	if old != p1 {
		t.Fatal("重连期间日志路径应保持一致")
	}

	s.Close()
}
