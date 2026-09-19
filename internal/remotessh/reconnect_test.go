package remotessh

import (
	"errors"
	"testing"
	"time"

	"sshtool/internal/store"
)

func testConn(id string) store.Connection {
	return store.Connection{ID: id, Host: "127.0.0.1", Port: 22, User: "test"}
}

// TestAutoReconnectRetriesThenSucceeds 验证传输层断开后，前两次 connect 失败、第三次成功时，
// 会话最终回到已连接态并广播 EventReconnected。
func TestAutoReconnectRetriesThenSucceeds(t *testing.T) {
	t.Setenv("SSHTOOL_RECONNECT_BASE", "1") // 退避 1s/2s，整体 < 5s
	mgr := NewManager()
	s := newSession(testConn("r1"), 80, 24, mgr.events, mgr.output, 1000)

	calls := 0
	old := connectImpl
	connectImpl = func(sx *Session, secret string) error {
		calls++
		sx.mu.Lock()
		defer sx.mu.Unlock()
		if calls < 3 {
			sx.state = StateError
			sx.err = errors.New("drop")
			return errors.New("drop")
		}
		sx.state = StateConnected
		return nil
	}
	defer func() { connectImpl = old }()

	go s.reconnect()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-mgr.Events():
			switch ev.Kind {
			case EventReconnected:
				if calls != 3 {
					t.Fatalf("重连成功但 connect 调用次数=%d，期望 3", calls)
				}
				if s.State() != StateConnected {
					t.Fatalf("重连后状态=%v，期望 Connected", s.State())
				}
				return
			case EventError, EventReconnectFailed:
				t.Fatalf("不应出现失败事件: %+v", ev)
			}
		case <-deadline:
			t.Fatalf("等待 EventReconnected 超时，calls=%d", calls)
		}
	}
}

// TestAutoReconnectAbortsOnClose 验证用户主动关闭会话时，正在进行的重连退避会立即中断，
// 会话回到已关闭态而非错误地变为已连接。
func TestAutoReconnectAbortsOnClose(t *testing.T) {
	t.Setenv("SSHTOOL_RECONNECT_BASE", "1")
	mgr := NewManager()
	s := newSession(testConn("r2"), 80, 24, mgr.events, mgr.output, 1000)

	old := connectImpl
	connectImpl = func(sx *Session, secret string) error {
		sx.mu.Lock()
		sx.state = StateError
		sx.mu.Unlock()
		return errors.New("always drop")
	}
	defer func() { connectImpl = old }()

	done := make(chan struct{})
	go func() {
		s.reconnect()
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	s.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close 后 reconnect 未退出")
	}
	if s.State() != StateClosed {
		t.Fatalf("Close 后状态=%v，期望 Closed", s.State())
	}
}

// TestReconnectGivesUpAfterMax 验证达到最大重试次数后停止，并广播 EventReconnectFailed。
func TestReconnectGivesUpAfterMax(t *testing.T) {
	t.Setenv("SSHTOOL_RECONNECT_BASE", "1")
	t.Setenv("SSHTOOL_RECONNECT_MAX", "3")
	mgr := NewManager()
	s := newSession(testConn("r3"), 80, 24, mgr.events, mgr.output, 1000)

	old := connectImpl
	connectImpl = func(sx *Session, secret string) error {
		sx.mu.Lock()
		sx.state = StateError
		sx.mu.Unlock()
		return errors.New("always drop")
	}
	defer func() { connectImpl = old }()

	go s.reconnect()

	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-mgr.Events():
			if ev.Kind == EventReconnectFailed {
				if s.State() != StateError {
					t.Fatalf("放弃后状态=%v，期望 Error", s.State())
				}
				return
			}
			if ev.Kind == EventReconnected {
				t.Fatal("不应重连成功")
			}
		case <-deadline:
			t.Fatal("等待 EventReconnectFailed 超时")
		}
	}
}
