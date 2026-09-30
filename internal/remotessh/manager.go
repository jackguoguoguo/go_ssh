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

// NotifyOutput 供非 SSH 会话（如本地 shell）投递输出通知，复用同一条重绘通道。
// 通道满时丢弃，与 SSH 会话的行为一致（不阻塞输出读取）。
func (m *Manager) NotifyOutput(id string) {
	select {
	case m.output <- id:
	default:
	}
}

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

// Open 打开一个会话：若该连接已有会话则直接返回既有会话（already=true），
// 从而「点已连接的连接项」会聚焦已有 tab 而非新建。
// cols/rows 为初始终端尺寸，secret 为密码或私钥口令。
func (m *Manager) Open(conn store.Connection, secret string, cols, rows, scrollback int) (s *Session, already bool) {
	if exist, ok := m.GetByConn(conn.ID); ok {
		return exist, true
	}
	return m.spawn(conn, secret, cols, rows, scrollback), false
}

// OpenNew 总是为同一连接创建一个全新的会话，无视是否已有会话，
// 用于「复制当前 tab / 同一连接多 tab」。
func (m *Manager) OpenNew(conn store.Connection, secret string, cols, rows, scrollback int) *Session {
	return m.spawn(conn, secret, cols, rows, scrollback)
}

// spawn 创建会话、登记到管理器并启动后台拨号，返回该会话。
func (m *Manager) spawn(conn store.Connection, secret string, cols, rows, scrollback int) *Session {
	s := newSession(conn, cols, rows, m.events, m.output, scrollback)
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.order = append(m.order, s.ID)
	m.mu.Unlock()

	go s.dial(secret)
	return s
}

// GetByConn 返回该连接当前任意一个会话（用于「聚焦已有 tab」）。
func (m *Manager) GetByConn(connID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, id := range m.order {
		if s, ok := m.sessions[id]; ok && s.Conn.ID == connID {
			return s, true
		}
	}
	return nil, false
}

// Reopen 关闭指定会话（按会话 ID）并以原口令重连，返回新会话。
// 用于密码 / 主机指纹确认后的重试，只影响这一个会话，不波及其余同连接 tab。
func (m *Manager) Reopen(oldSessionID string, secret string, cols, rows, scrollback int) *Session {
	m.mu.Lock()
	old, ok := m.sessions[oldSessionID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	conn := old.Conn
	m.Close(oldSessionID)
	return m.spawn(conn, secret, cols, rows, scrollback)
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
