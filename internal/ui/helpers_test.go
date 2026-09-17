package ui

import (
	"testing"
	"time"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// connectTestSession 起一个进程内 SSH 服务端并把 Model 连上去，返回已连接的会话。
// 终端类交互（选择、搜索、广播）都需要真实的 Session，无法用假对象替代。
func connectTestSession(t *testing.T, m *Model, st *store.Store) *remotessh.Session {
	t.Helper()
	srv := testutil.StartSSHServer(t)
	c := st.AddConnection(store.Connection{
		Name: "srv", Host: srv.Host, Port: srv.Port, User: "test",
		AuthType: store.AuthPassword, Password: "pass",
	})
	s, _ := m.mgr.Open(c, "pass", 40, 8, 100)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == remotessh.StateConnected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.State() != remotessh.StateConnected {
		t.Fatalf("测试会话未连接成功：state=%v err=%v", s.State(), s.Err())
	}
	m.width, m.height = 120, 30
	m.activeID = s.ID
	m.refreshSessions()
	return s
}

// waitTermText 轮询等待终端缓冲区出现指定文本。
func waitTermText(t *testing.T, s *remotessh.Session, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if containsPlain(s.Term().Text(0, 0, 200, 30), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("终端未出现 %q", want)
}

func containsPlain(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexPlain(haystack, needle) >= 0)
}

func indexPlain(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
