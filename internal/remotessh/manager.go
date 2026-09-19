package remotessh

import (
	"sync"

	"sshtool/internal/store"
)

// EventKind 事件类型。
type EventKind int

const (
	EventConnected    EventKind = iota // 建连成功
	EventDisconnected                  // 连接断开（shell 正常退出）
	EventError                         // 出错
	EventNeedSecret                    // 需要密码 / 私钥口令
	EventNeedHostKey                   // 主机指纹需要确认（未知主机）
	EventReconnecting                  // 传输层断开，正在自动重连
	EventReconnected                   // 自动重连成功
	EventReconnectFailed               // 自动重连失败（已达最大次数）
)

// Event 会话事件，由 Manager 统一投递给 UI。
type Event struct {
	SessionID string
	Kind      EventKind
	Err       error
	Attempt   int // EventReconnecting 时表示第几次重连尝试
}

// Manager 管理全部存活的会话。
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	order    []string
	events   chan Event
	output   chan string // 输出通知：携带产生输出的会话 ID
}

// NewManager 创建一个会话管理器。
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		events:   make(chan Event, 64),
		output:   make(chan string, 256),
	}
}

// Events 返回全局事件通道，UI 应持续消费。
func (m *Manager) Events() <-chan Event { return m.events }

// Output 返回全局输出通知通道，UI 消费后触发重绘。
func (m *Manager) Output() <-chan string { return m.output }

// Get 按连接 ID 取会话。
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// List 按打开顺序返回全部会话。
func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.order))
	for _, id := range m.order {
		if s, ok := m.sessions[id]; ok {
			out = append(out, s)
		}
	}
	return out
}

// Len 返回会话数量。
func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// Open 打开一个会话。若该连接已有会话则直接返回既有会话（already=true）。
// cols/rows 为初始终端尺寸，secret 为密码或私钥口令。
func (m *Manager) Open(conn store.Connection, secret string, cols, rows, scrollback int) (s *Session, already bool) {
	m.mu.Lock()
	if exist, ok := m.sessions[conn.ID]; ok {
		m.mu.Unlock()
		return exist, true
	}
	s = newSession(conn, cols, rows, m.events, m.output, scrollback)
	m.sessions[conn.ID] = s
	m.order = append(m.order, conn.ID)
	m.mu.Unlock()

	go s.dial(secret)
	return s, false
}

// Reopen 关闭并重连一个已存在的会话（用于失败重试）。
func (m *Manager) Reopen(conn store.Connection, secret string, cols, rows, scrollback int) *Session {
	m.Close(conn.ID)

	s := newSession(conn, cols, rows, m.events, m.output, scrollback)

	m.mu.Lock()
	m.sessions[conn.ID] = s
	found := false
	for _, id := range m.order {
		if id == conn.ID {
			found = true
			break
		}
	}
	if !found {
		m.order = append(m.order, conn.ID)
	}
	m.mu.Unlock()

	go s.dial(secret)
	return s
}

// Close 关闭并移除会话。
func (m *Manager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		for i, x := range m.order {
			if x == id {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
	}
	m.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

// CloseAll 关闭全部会话。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}

// ResizeAll 把新尺寸广播给全部存活会话。
func (m *Manager) ResizeAll(cols, rows int) {
	for _, s := range m.List() {
		s.Resize(cols, rows)
	}
}

// Touch 更新连接的最近使用时间（由 UI 在连接成功后调用）。
func Touch(st *store.Store, id string) {
	if st == nil {
		return
	}
	st.TouchConnection(id)
	_ = st.Save()
}
