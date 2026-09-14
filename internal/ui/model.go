package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// 焦点枚举：连接 → 收藏 → 历史 → 终端。
const (
	focusConn = iota
	focusFav
	focusHist
	focusTerm
)

// configPathHint 在帮助面板里展示配置文件位置。
var configPathHint string

// Model 是应用的根 bubbletea Model。
type Model struct {
	st  *store.Store
	mgr *remotessh.Manager

	width  int
	height int

	focus     int
	inputMode bool // true 表示终端下方的命令行处于编辑状态

	sessions []*remotessh.Session
	activeID string

	connList []store.Connection
	favList  []store.FavoriteCmd
	histList []store.HistoryEntry

	connSel, favSel, histSel       int
	connScroll, favScroll          int
	histScroll                     int
	connQuery, favQuery, histQuery string
	filtering                      bool

	input    []rune
	inputPos int

	tabBounds []tabBound
	dlg       *dlg
	dlgHit    dlgHit

	msg        string
	msgSeq     int
	outPending bool

	lastClickAt  time.Time
	lastClickKey string

	quitting bool
}

// New 创建根 Model。
func New(st *store.Store, mgr *remotessh.Manager) Model {
	configPathHint = st.Path()
	m := Model{st: st, mgr: mgr, focus: focusConn}
	m.refresh()
	return m
}

// ---------- 消息 ----------

type outputTick struct{ id string }

type sessionEvent struct{ ev remotessh.Event }

type clearMsg struct{ seq int }

// waitEvent 等待下一个会话事件。
func waitEvent(mgr *remotessh.Manager) tea.Cmd {
	ch := mgr.Events()
	return func() tea.Msg { return sessionEvent{ev: <-ch} }
}

// waitOutput 等待任意会话产生新输出。使用全局通道避免会话切换导致的协程泄漏。
func waitOutput(mgr *remotessh.Manager) tea.Cmd {
	ch := mgr.Output()
	return func() tea.Msg { return outputTick{id: <-ch} }
}

// Init 实现 tea.Model。
func (m *Model) Init() tea.Cmd {
	return tea.Batch(waitEvent(m.mgr), m.armOutput())
}

// armOutput 为当前活动会话注册一个输出等待命令。
func (m *Model) armOutput() tea.Cmd {
	if m.outPending {
		return nil
	}
	s := m.activeSession()
	if s == nil {
		return nil
	}
	m.outPending = true
	return waitOutput(m.mgr)
}

// ---------- 数据同步 ----------

// refresh 重新从 store 与 manager 拉取数据并修正选中项。
func (m *Model) refresh() {
	m.refreshSessions()

	q := m.connQuery
	m.connList = filterConnections(m.st.GetConnections(), q)
	if m.connSel >= len(m.connList) {
		m.connSel = len(m.connList) - 1
	}
	if m.connSel < 0 {
		m.connSel = 0
	}

	m.favList = filterFavorites(m.st.GetFavorites(), m.favQuery)
	if m.favSel >= len(m.favList) {
		m.favSel = len(m.favList) - 1
	}
	if m.favSel < 0 {
		m.favSel = 0
	}

	if m.histQuery == "" {
		m.histList = m.st.GetHistory()
	} else {
		m.histList = m.st.SearchHistory(m.histQuery)
	}
	if m.histSel >= len(m.histList) {
		m.histSel = len(m.histList) - 1
	}
	if m.histSel < 0 {
		m.histSel = 0
	}
}

// refreshSessions 同步会话列表并保证 activeID 有效。
func (m *Model) refreshSessions() {
	m.sessions = m.mgr.List()
	found := false
	for _, s := range m.sessions {
		if s.ID == m.activeID {
			found = true
			break
		}
	}
	if !found {
		if len(m.sessions) > 0 {
			m.activeID = m.sessions[len(m.sessions)-1].ID
		} else {
			m.activeID = ""
		}
	}
}

// activeSession 返回当前活动会话，可能为 nil。
func (m *Model) activeSession() *remotessh.Session {
	if m.activeID == "" {
		return nil
	}
	s, ok := m.mgr.Get(m.activeID)
	if !ok {
		return nil
	}
	return s
}

// activeIndex 返回当前活动会话在标签栏中的下标。
func (m *Model) activeIndex() int {
	for i, s := range m.sessions {
		if s.ID == m.activeID {
			return i
		}
	}
	return -1
}

// ---------- Update ----------

// Update 实现 tea.Model。
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applyTermSize()
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)

	case tea.MouseMsg:
		return m.onMouse(msg)

	case sessionEvent:
		m.onSessionEvent(msg.ev)
		return m, tea.Batch(waitEvent(m.mgr), m.armOutput())

	case outputTick:
		m.outPending = false
		return m, m.armOutput()

	case clearMsg:
		if msg.seq == m.msgSeq {
			m.msg = ""
		}
		return m, nil
	}
	return m, nil
}

// onSessionEvent 处理会话状态变化。
func (m *Model) onSessionEvent(ev remotessh.Event) {
	switch ev.Kind {
	case remotessh.EventConnected:
		m.refresh()
		remotessh.Touch(m.st, ev.SessionID)
	case remotessh.EventNeedSecret:
		if c, ok := m.st.Connection(ev.SessionID); ok {
			title := "需要密码"
			if c.AuthType == store.AuthKey {
				title = "需要私钥口令"
			}
			conn := c
			m.dlg = newSecretDialog(title, conn.User+"@"+conn.Host+" 需要认证信息", func(m *Model, values []string) {
				secret := ""
				if len(values) > 0 {
					secret = values[0]
				}
				l := m.computeLayout()
				s := m.mgr.Reopen(conn, secret, l.termCol, l.termRow, m.st.GetSettings().Scrollback)
				m.activeID = s.ID
				m.refreshSessions()
			})
		}
	case remotessh.EventError:
		if ev.Err != nil {
			m.setMsg("连接失败：" + ev.Err.Error())
		}
	case remotessh.EventDisconnected:
		m.refreshSessions()
	}
	m.refreshSessions()
}

// applyTermSize 把布局算出的终端尺寸广播给所有会话。
func (m *Model) applyTermSize() {
	l := m.computeLayout()
	m.mgr.ResizeAll(l.termCol, l.termRow)
}

// setMsg 设置状态栏提示，并在 5 秒后自动清除。
func (m *Model) setMsg(s string) tea.Cmd {
	m.msgSeq++
	m.msg = s
	seq := m.msgSeq
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearMsg{seq: seq} })
}

// ---------- View ----------

// View 实现 tea.Model。
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "初始化中…"
	}
	if m.dlg != nil {
		return m.renderDialog()
	}

	l := m.computeLayout()
	header := m.renderHeader(l)
	left := lipgloss.JoinVertical(lipgloss.Left,
		m.renderConnPanel(l.panels[0]),
		m.renderFavPanel(l.panels[1]),
		m.renderHistPanel(l.panels[2]),
	)
	right := m.renderTerminal(l)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	status := m.renderStatus(l)

	return header + "\n" + body + "\n" + status
}

// ---------- 过滤 ----------

func filterConnections(in []store.Connection, q string) []store.Connection {
	if q == "" {
		return in
	}
	q = strings.ToLower(q)
	out := make([]store.Connection, 0, len(in))
	for _, c := range in {
		if strings.Contains(strings.ToLower(c.Name), q) ||
			strings.Contains(strings.ToLower(c.Host), q) ||
			strings.Contains(strings.ToLower(c.User), q) ||
			strings.Contains(strings.ToLower(c.Group), q) {
			out = append(out, c)
		}
	}
	return out
}

func filterFavorites(in []store.FavoriteCmd, q string) []store.FavoriteCmd {
	if q == "" {
		return in
	}
	q = strings.ToLower(q)
	out := make([]store.FavoriteCmd, 0, len(in))
	for _, f := range in {
		if strings.Contains(strings.ToLower(f.Name), q) ||
			strings.Contains(strings.ToLower(f.Cmd), q) ||
			strings.Contains(strings.ToLower(f.Group), q) {
			out = append(out, f)
		}
	}
	return out
}
