package ui

import (
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"sshtool/internal/keytool"
	"sshtool/internal/localshell"
	"sshtool/internal/portfwd"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/vt"
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

	sessions []tabSession // 标签栏：SSH 会话 + 本地 shell，顺序见 rebuildTabs
	locals   []*localshell.Session
	activeID string

	connList []store.Connection
	favList  []store.FavoriteCmd
	histList []store.HistoryEntry

	// 连接的显示行（含分组头）；connSel / connScroll 以此为索引。
	connRows  []connRow
	collapsed map[string]bool // 分组收起状态

	// passCache 进程内私钥口令缓存：键为私钥路径，避免同一私钥重复询问。
	passCache map[string]string

	// fwd 管理所有端口转发，与 SSH 会话解耦（转发随会话生命周期，但归总在本进程内）。
	fwd *portfwd.Manager

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

	replay *replayModal // 会话回放（Ctrl+L）全屏视图

	msg        string
	msgSeq     int
	outPending bool
	afterCmd   tea.Cmd // 对话框回调派生的异步命令

	cwd      string       // 远端当前工作目录（命令行 cd 解析得到的兜底值）
	fsEvents chan fsEvent // 文件编辑自动回传的事件通道

	hostKeyQueue []hostKeyAsk // 待确认的主机指纹询问（对话框是单例，必须排队）

	// 终端文本选择（Alt+S）：坐标为终端视口坐标，左上角 0,0
	selMode  bool
	selOn    bool // 是否已按下起点
	selCX    int  // 光标位置
	selCY    int
	selAX    int // 选区起点
	selAY    int
	selDrag  bool   // 鼠标正在拖选
	lastCopy string // 最近一次复制的内容（OSC52 只能写不能读，粘贴用它兜底）

	// 回滚缓冲搜索（Alt+/）
	searchMode  bool
	searchQuery string
	hits        []vt.Hit
	hitIdx      int

	broadcast bool // 广播模式：命令行发往所有已连接会话

	// 批量执行（多主机）：目标连接与记忆的上次命令。
	batchTargets []store.Connection
	lastBatchCmd string

	// rotate 密钥轮换向导的中间状态（生成新密钥 → 选旧密钥 → 选主机 → 执行）。
	rotate *rotateState

	lastClickAt  time.Time
	lastClickKey string

	quitting bool
}

// New 创建根 Model。
func New(st *store.Store, mgr *remotessh.Manager) Model {
	configPathHint = st.Path()
	// 按设置应用主题（Settings.Theme 此前未生效，现接入）。
	applyTheme(st.GetSettings().Theme)
	m := Model{st: st, mgr: mgr, focus: focusConn, fsEvents: make(chan fsEvent, 16), passCache: map[string]string{}, fwd: portfwd.New()}
	m.refresh()
	// 若上次退出时有会话快照，询问是否复原（可用 SSHTOOL_NO_RESTORE=1 关闭）。
	m.restorePrompt()
	return m
}

// cachePassphrase 记住某私钥的口令（仅私钥认证、非空时），供后续连接复用以免重复询问。
func (m *Model) cachePassphrase(c store.Connection, secret string) {
	if c.AuthType != store.AuthKey || secret == "" {
		return
	}
	if m.passCache == nil {
		m.passCache = map[string]string{}
	}
	m.passCache[remotessh.ResolveKeyPath(c)] = secret
}

// ---------- 消息 ----------

type outputTick struct{ id string }

type sessionEvent struct{ ev remotessh.Event }

type clearMsg struct{ seq int }

// genDoneMsg 本地密钥生成完成。
type genDoneMsg struct {
	info keytool.KeyInfo
	err  error
}

// pushDoneMsg 批量推送公钥完成。
type pushDoneMsg struct {
	results    []keytool.PushResult
	remotePath string
}

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
	m.rebuildConnRows() // 依据分组与收起状态重建显示行，并修正 connSel

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
// rebuildTabs 重建标签列表：先 SSH 会话（按打开顺序），再本地 shell。
func (m *Model) rebuildTabs() {
	m.sessions = m.sessions[:0]
	for _, s := range m.mgr.List() {
		m.sessions = append(m.sessions, s)
	}
	for _, ls := range m.locals {
		m.sessions = append(m.sessions, ls)
	}
}

func (m *Model) refreshSessions() {
	m.rebuildTabs()
	found := false
	for _, s := range m.sessions {
		if s.TabID() == m.activeID {
			found = true
			break
		}
	}
	if !found {
		if len(m.sessions) > 0 {
			m.activeID = m.sessions[len(m.sessions)-1].TabID()
		} else {
			m.activeID = ""
		}
	}
}

// activeSession 返回当前活动会话，可能为 nil。
func (m *Model) activeSession() tabSession {
	if m.activeID == "" {
		return nil
	}
	for _, s := range m.sessions {
		if s.TabID() == m.activeID {
			return s
		}
	}
	return nil
}

// activeIndex 返回当前活动会话在标签栏中的下标。
func (m *Model) activeIndex() int {
	for i, s := range m.sessions {
		if s.TabID() == m.activeID {
			return i
		}
	}
	return -1
}

// cwdMarker 是远端 shell 通过 OSC 标题上报当前目录时使用的前缀。
const cwdMarker = "SSHTPWD:"

// remoteCwd 读取当前活动会话的远端工作目录。
// 优先使用终端标题里由 PROMPT_COMMAND 静默上报的值（前缀 SSHTPWD:），
// 取不到时回退到本机根据命令行 cd 解析得到的值。
func (m *Model) remoteCwd() string {
	s := m.activeSession()
	if s == nil {
		return ""
	}
	title := s.Term().Title()
	if strings.HasPrefix(title, cwdMarker) {
		return strings.TrimSpace(title[len(cwdMarker):])
	}
	return m.cwd
}

// trackCwd 在用户通过命令行执行命令后，尝试解析 cd 类命令以更新兜底 cwd。
func (m *Model) trackCwd(cmd string) {
	c := strings.TrimSpace(cmd)
	if c == "" {
		return
	}
	fields := strings.Fields(c)
	if len(fields) == 0 || fields[0] != "cd" {
		return
	}
	arg := ""
	if len(fields) > 1 {
		arg = fields[1]
	}
	m.cwd = resolvePath(m.cwd, arg)
}

// resolvePath 结合当前目录解析一个 cd 参数（支持绝对路径、~、.、..）。
func resolvePath(base, arg string) string {
	if arg == "" || arg == "~" {
		return "~"
	}
	if arg == "-" {
		return base // 简化：不回溯旧目录
	}
	if strings.HasPrefix(arg, "~/") {
		return "~" + arg[1:]
	}
	if arg == "." {
		return base
	}
	if !strings.HasPrefix(arg, "/") && !strings.HasPrefix(arg, "~") {
		if base == "" || base == "~" {
			arg = "~/" + arg
		} else if strings.HasSuffix(base, "/") {
			arg = base + arg
		} else {
			arg = base + "/" + arg
		}
	}
	parts := strings.Split(arg, "/")
	stack := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p {
		case "", ".":
			// 跳过
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, p)
		}
	}
	if len(stack) == 0 {
		return "/"
	}
	if stack[0] == "~" {
		return "~/" + strings.Join(stack[1:], "/")
	}
	return "/" + strings.Join(stack, "/")
}

// ---------- Update ----------

// Update 实现 tea.Model。
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applyTermSize()
		return m, nil

	case tea.QuitMsg:
		// 退出前记录会话布局（供下次启动复原），并清理所有端口转发监听。
		m.saveSessionSnapshot()
		if m.fwd != nil {
			m.fwd.StopAll()
		}
		return m, tea.Quit

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

	case genDoneMsg:
		m.showGenResult(msg.info, msg.err)
		return m, nil

	case pushDoneMsg:
		m.showPushResult(msg.results, msg.remotePath)
		return m, nil

	case batchDoneMsg:
		m.showBatchResult(msg)
		return m, nil

	case pingDoneMsg:
		m.showPingResult(msg)
		return m, nil

	case backupDoneMsg:
		m.showBackupResult(msg)
		return m, nil

	case restoreDoneMsg:
		m.showRestoreResult(msg)
		return m, nil

	case auditDoneMsg:
		m.showAuditResult(msg)
		return m, nil

	case rotateGenDoneMsg:
		if msg.err != "" {
			m.dlg = &dlg{kind: dlgText, title: "生成失败",
				body: append([]string{""}, wrapText("  "+msg.err, max(30, m.width-16))...)}
			_ = m.setMsg("生成新密钥失败：" + msg.err)
			return m, nil
		}
		m.openRotatePickOld(msg.info)
		return m, nil

	case rotateDoneMsg:
		m.showRotateResult(msg)
		return m, nil

	case fsLoadedMsg:
		if m.dlg != nil && m.dlg.kind == dlgFile {
			d := m.dlg
			d.fsPath = msg.path
			if msg.err != nil {
				d.fsMsg = "读取目录失败：" + msg.err.Error()
				d.fsLoading = false
			} else {
				d.fsEntries = msg.entries
				d.fsCursor = 0
				d.fsScroll = 0
				d.fsLoading = false
				d.fsMsg = ""
				if msg.note != "" {
					m.setMsg(msg.note)
				}
			}
		}
		return m, nil

	case fsSavedMsg:
		if msg.err != "" {
			m.setMsg("上传失败：" + msg.err)
		} else {
			m.setMsg("已保存并回传：" + msg.path)
		}
		if m.dlg != nil && m.dlg.kind == dlgFile {
			return m, m.waitFsEvent()
		}
		return m, nil

	case clearMsg:
		if msg.seq == m.msgSeq {
			m.msg = ""
		}
		return m, nil

	case clipboardMsg:
		switch {
		case msg.ok:
			return m, m.setMsg("已复制到系统剪贴板")
		case msg.path != "":
			return m, m.setMsg("终端不支持剪贴板，已保存到 " + msg.path)
		default:
			return m, m.setMsg("复制失败：" + msg.err)
		}
	}
	return m, nil
}

// takeAfterCmd 取出并清空对话框回调派生的命令。
func (m *Model) takeAfterCmd() tea.Cmd {
	c := m.afterCmd
	m.afterCmd = nil
	return c
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
				m.cachePassphrase(conn, secret)
				l := m.computeLayout()
				s := m.mgr.Reopen(conn, secret, l.termCol, l.termRow, m.st.GetSettings().Scrollback)
				m.activeID = s.ID
				m.refreshSessions()
			})
		}
	case remotessh.EventNeedHostKey:
		// 未知主机 / 指纹变更：交给用户决策，队列保证并发询问不丢单。
		m.enqueueHostKey(ev.SessionID, ev.Err)
	case remotessh.EventError:
		// 指纹变更等无法决策的错误已经在 EventNeedHostKey 里提示过，避免重复刷屏。
		if ev.Err != nil {
			var hke *remotessh.HostKeyError
			if !errors.As(ev.Err, &hke) {
				m.setMsg("连接失败：" + ev.Err.Error())
			}
		}
	case remotessh.EventDisconnected:
		m.resetTermModes()
		m.refreshSessions()
	case remotessh.EventReconnecting:
		m.refreshSessions()
	case remotessh.EventReconnected:
		m.setMsg("已恢复连接")
		m.refreshSessions()
	case remotessh.EventReconnectFailed:
		m.setMsg("自动重连失败，按 Ctrl+R 手动重连")
		m.refreshSessions()
	}
	m.refreshSessions()
}

// applyTermSize 把布局算出的终端尺寸广播给所有会话。
func (m *Model) applyTermSize() {
	l := m.computeLayout()
	// 尺寸变了，选区坐标与搜索高亮都失效，先复位再广播尺寸
	m.resetTermModes()
	m.mgr.ResizeAll(l.termCol, l.termRow)
	for _, ls := range m.locals {
		ls.Resize(l.termCol, l.termRow)
	}
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
	if m.replay != nil {
		return m.renderReplay()
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

// filterConnections 连接过滤委托给 store.FilterConnections，保证 UI 与 CLI 子命令语义一致。
func filterConnections(in []store.Connection, q string) []store.Connection {
	return store.FilterConnections(in, q)
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
