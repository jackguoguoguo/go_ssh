package remotessh

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// waitEvent 等待事件通道上出现指定类型的事件。
func waitEvent(t *testing.T, mgr *Manager, want EventKind, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-mgr.Events():
			if ev.Kind == want {
				return ev
			}
			if ev.Kind == EventError || ev.Kind == EventNeedSecret {
				t.Fatalf("收到错误事件: kind=%d err=%v", ev.Kind, ev.Err)
			}
		case <-deadline:
			t.Fatalf("等待事件 kind=%d 超时", want)
		}
	}
}

// waitForText 轮询等待终端缓冲区出现指定文本。
func waitForText(t *testing.T, mgr *Manager, s *Session, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		joined := strings.Join(s.Term().Render(), "\n")
		if strings.Contains(stripANSIForTest(joined), want) {
			return
		}
		select {
		case <-mgr.Output():
		case <-deadline:
			t.Fatalf("终端未出现 %q，当前内容:\n%q", want, joined)
		}
	}
}

func TestOpenWritesToTerminal(t *testing.T) {
	srv := testutil.StartSSHServer(t)
	mgr := NewManager()
	defer mgr.CloseAll()

	conn := store.Connection{
		ID:       "conn-1",
		Name:     "测试机",
		Host:     srv.Host,
		Port:     srv.Port,
		User:     "test",
		AuthType: store.AuthPassword,
		Password: "pass",
	}

	s, already := mgr.Open(conn, "pass", 40, 8, 100)
	if already {
		t.Fatal("首次打开不应返回已存在的会话")
	}
	if s.ID != conn.ID {
		t.Fatalf("会话 ID = %q，期望 %q", s.ID, conn.ID)
	}

	waitEvent(t, mgr, EventConnected, 10*time.Second)
	if s.State() != StateConnected {
		t.Fatalf("状态 = %v，期望 StateConnected", s.State())
	}

	waitForText(t, mgr, s, "hello", 5*time.Second)

	// 同一连接再次 Open 应复用会话
	if _, again := mgr.Open(conn, "pass", 40, 8, 100); !again {
		t.Fatal("重复 Open 应返回已有会话")
	}
	if mgr.Len() != 1 {
		t.Fatalf("会话数 = %d，期望 1", mgr.Len())
	}

	// 写入 stdin 应被服务端回显
	if err := s.Write([]byte("hi\n")); err != nil {
		t.Fatalf("写入 stdin 失败: %v", err)
	}
	waitForText(t, mgr, s, "echo:hi", 5*time.Second)

	// Resize 应同步缓冲区尺寸
	s.Resize(30, 6)
	if s.Term().Cols() != 30 || s.Term().Rows() != 6 {
		t.Fatalf("resize 后尺寸 = %dx%d，期望 30x6", s.Term().Cols(), s.Term().Rows())
	}

	// Close 后应从管理器移除并变为已关闭状态
	mgr.Close(conn.ID)
	if mgr.Len() != 0 {
		t.Fatalf("关闭后会话数 = %d，期望 0", mgr.Len())
	}
	if s.State() != StateClosed {
		t.Fatalf("关闭后状态 = %v，期望 StateClosed", s.State())
	}
}

// TestUnknownHostNeedsTrustThenReconnect 覆盖「陌生主机 → 询问 → 信任 → 重连」全链路。
func TestUnknownHostNeedsTrustThenReconnect(t *testing.T) {
	srv := testutil.StartSSHServer(t)

	// StartSSHServer 会自动信任自己（否则既有测试全部连不上），
	// 这里把记录清空，模拟首次连接一台陌生主机。
	p := os.Getenv(KnownHostsEnv)
	if p == "" {
		t.Fatal("测试 known_hosts 未设置")
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	defer mgr.CloseAll()

	conn := store.Connection{
		ID: "trust-1", Host: srv.Host, Port: srv.Port,
		User: "test", AuthType: store.AuthPassword, Password: "pass",
	}
	first, _ := mgr.Open(conn, "pass", 40, 8, 100)

	ev := waitEvent(t, mgr, EventNeedHostKey, 10*time.Second)
	var hke *HostKeyError
	if !errors.As(ev.Err, &hke) {
		t.Fatalf("期望 *HostKeyError，实际 %v", ev.Err)
	}
	if hke.Changed {
		t.Error("清空记录后应视为未知主机，而非指纹变更")
	}
	if first.State() != StateError {
		t.Errorf("未确认指纹时状态应为 StateError，实际 %v", first.State())
	}

	// 模拟用户在对话框里点「信任并写入 known_hosts」
	if err := TrustHost(net.JoinHostPort(srv.Host, fmt.Sprintf("%d", srv.Port)), hke.Key); err != nil {
		t.Fatalf("TrustHost 失败: %v", err)
	}
	// 重连应复用已保存的口令，不再弹密码框
	second := mgr.Reopen(conn, first.Secret(), 40, 8, 100)
	waitEvent(t, mgr, EventConnected, 10*time.Second)
	if second.State() != StateConnected {
		t.Fatalf("信任后重连应成功，实际状态 %v（err=%v）", second.State(), second.Err())
	}
	if second.Secret() != "pass" {
		t.Errorf("重连应复用原口令，实际 %q", second.Secret())
	}
}

func TestOpenBadPasswordReportsError(t *testing.T) {
	srv := testutil.StartSSHServer(t)
	mgr := NewManager()
	defer mgr.CloseAll()

	conn := store.Connection{ID: "bad", Host: srv.Host, Port: srv.Port, User: "test", AuthType: store.AuthPassword}
	mgr.Open(conn, "wrong", 40, 8, 100)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-mgr.Events():
			if ev.Kind == EventError {
				if s, ok := mgr.Get(conn.ID); ok && s.State() != StateError {
					t.Fatalf("失败后状态 = %v，期望 StateError", s.State())
				}
				return
			}
		case <-deadline:
			t.Fatal("未收到连接失败事件")
		}
	}
}

// stripANSIForTest 去掉 SGR 序列，便于断言文本内容。
func stripANSIForTest(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if j := strings.IndexByte(s[i:], 'm'); j >= 0 {
				i += j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
