package remotessh

import (
	"fmt"
	"testing"
	"time"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// TestProxyJumpConnects 验证配置 SSHTOOL_PROXY_JUMP 后，所有连接经跳板机建立。
func TestProxyJumpConnects(t *testing.T) {
	s := testutil.StartSSHServer(t)
	t.Setenv(EnvProxyJump, fmt.Sprintf("test@%s:%d", s.Host, s.Port))
	// 跳板机用环境变量里的密码认证（测试服务端接受 test/pass）。
	t.Setenv(EnvProxyJumpPassword, "pass")

	mgr := NewManager()
	defer mgr.CloseAll()

	conn := store.Connection{
		ID:       "t1",
		Name:     "via-jump",
		Host:     s.Host,
		Port:     s.Port,
		User:     "test",
		AuthType: store.AuthPassword,
		Password: "pass",
	}
	if _, already := mgr.Open(conn, "pass", 80, 24, 5000); already {
		t.Fatal("不应报告已存在")
	}
	sess, ok := mgr.Get("t1")
	if !ok {
		t.Fatal("连接后应能取到会话")
	}
	if !waitConnected(sess) {
		t.Fatalf("状态应为已连接，实际 %v (err=%v)", sess.State(), sess.Err())
	}
	if sess.Client() == nil {
		t.Fatal("连接成功但 Client() 为空（端口转发将不可用）")
	}
	mgr.CloseAll()
}

// TestProxyJumpDisabledConnectsDirectly 验证未配置跳板机时直连照常工作。
func TestProxyJumpDisabledConnectsDirectly(t *testing.T) {
	t.Setenv(EnvProxyJump, "")
	s := testutil.StartSSHServer(t)
	mgr := NewManager()
	defer mgr.CloseAll()

	conn := store.Connection{
		ID:       "t2",
		Name:     "direct",
		Host:     s.Host,
		Port:     s.Port,
		User:     "test",
		AuthType: store.AuthPassword,
		Password: "pass",
	}
	mgr.Open(conn, "pass", 80, 24, 5000)
	sess, _ := mgr.Get("t2")
	if !waitConnected(sess) {
		t.Fatalf("状态应为已连接，实际 %v (err=%v)", sess.State(), sess.Err())
	}
}

// waitConnected 轮询会话状态直到已连接或超时（连接为异步建立）。
func waitConnected(s *Session) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == StateConnected {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
