package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/store"
)

// ---------- 面板通用操作 ----------

func (m *Model) panelRows(i int) int {
	l := m.computeLayout()
	rows := l.panels[i].h - 3 // 上下边框 + 标题行
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (m *Model) countOf(i int) int {
	switch i {
	case focusConn:
		return len(m.connList)
	case focusFav:
		return len(m.favList)
	case focusHist:
		return len(m.histList)
	}
	return 0
}

func (m *Model) selOf(i int) int {
	switch i {
	case focusConn:
		return m.connSel
	case focusFav:
		return m.favSel
	case focusHist:
		return m.histSel
	}
	return 0
}

func (m *Model) setSel(i, v int) {
	switch i {
	case focusConn:
		m.connSel = v
	case focusFav:
		m.favSel = v
	case focusHist:
		m.histSel = v
	}
}

func (m *Model) scrollOf(i int) int {
	switch i {
	case focusConn:
		return m.connScroll
	case focusFav:
		return m.favScroll
	case focusHist:
		return m.histScroll
	}
	return 0
}

func (m *Model) setScroll(i, v int) {
	if v < 0 {
		v = 0
	}
	switch i {
	case focusConn:
		m.connScroll = v
	case focusFav:
		m.favScroll = v
	case focusHist:
		m.histScroll = v
	}
}

func (m *Model) queryOf(i int) string {
	switch i {
	case focusConn:
		return m.connQuery
	case focusFav:
		return m.favQuery
	case focusHist:
		return m.histQuery
	}
	return ""
}

func (m *Model) setQuery(i int, q string) {
	switch i {
	case focusConn:
		m.connQuery = q
	case focusFav:
		m.favQuery = q
	case focusHist:
		m.histQuery = q
	}
}

// moveSel 移动选中项并保证其在可视区域内。
func (m *Model) moveSel(i, delta int) {
	n := m.countOf(i)
	if n == 0 {
		return
	}
	v := m.selOf(i) + delta
	if v < 0 {
		v = 0
	}
	if v >= n {
		v = n - 1
	}
	m.setSel(i, v)
	m.ensureVisible(i)
}

// ensureVisible 调整滚动偏移，使选中项可见。
func (m *Model) ensureVisible(i int) {
	rows := m.panelRows(i)
	sel := m.selOf(i)
	scroll := m.scrollOf(i)
	if sel < scroll {
		m.setScroll(i, sel)
		return
	}
	if sel >= scroll+rows {
		m.setScroll(i, sel-rows+1)
	}
	// 列表变短时回退
	if n := m.countOf(i); scroll > 0 && scroll+rows > n {
		v := n - rows
		if v < 0 {
			v = 0
		}
		m.setScroll(i, v)
	}
}

// activate 执行当前面板选中项的默认动作。
func (m *Model) activate(i int) (tea.Model, tea.Cmd) {
	n := m.countOf(i)
	if n == 0 {
		return m, nil
	}
	sel := m.selOf(i)
	if sel < 0 || sel >= n {
		return m, nil
	}

	switch i {
	case focusConn:
		c := m.connList[sel]
		return m, m.connectConn(c)
	case focusFav:
		f := m.favList[sel]
		m.st.TouchFavorite(f.ID)
		_ = m.st.Save()
		m.setInput(f.Cmd)
		m.focus = focusTerm
		m.inputMode = true
		return m, nil
	case focusHist:
		h := m.histList[sel]
		m.setInput(h.Cmd)
		m.focus = focusTerm
		m.inputMode = true
		return m, nil
	}
	return m, nil
}

// setInput 设置命令行内容并把光标移到末尾。
func (m *Model) setInput(s string) {
	m.input = []rune(s)
	m.inputPos = len(m.input)
}

// ---------- 会话操作 ----------

func (m *Model) switchTo(i int) {
	if len(m.sessions) == 0 {
		return
	}
	if i < 0 {
		i = len(m.sessions) - 1
	}
	if i >= len(m.sessions) {
		i = 0
	}
	m.activeID = m.sessions[i].ID
	m.focus = focusTerm
	m.inputMode = false
}

func (m *Model) sendRaw(data []byte) {
	s := m.activeSession()
	if s == nil {
		return
	}
	_ = s.Write(data)
}

// runCommand 把整条命令发往当前会话并记录历史。
func (m *Model) runCommand(cmd string) {
	s := m.activeSession()
	if s == nil {
		m.setMsg("尚未连接到服务器")
		return
	}
	if s.State() != 1 {
		m.setMsg("会话当前不可写（" + s.State().String() + "），可按 Ctrl+R 重连")
		return
	}
	_ = s.Write([]byte(cmd + "\r"))
	m.trackCwd(cmd)
	m.st.AddHistory(cmd, s.Conn.Host)
	_ = m.st.Save()
	m.refresh()
}

func (m *Model) closeActiveSession() {
	if m.activeID == "" {
		return
	}
	m.mgr.Close(m.activeID)
	m.refreshSessions()
	m.setMsg("会话已关闭")
}

func (m *Model) reconnectActive() {
	s := m.activeSession()
	if s == nil {
		m.setMsg("没有可重连的会话")
		return
	}
	c := s.Conn
	m.mgr.Close(c.ID)
	m.refreshSessions()
	m.connectConn(c)
}

// connectConn 连接（或激活）一个连接配置。
func (m *Model) connectConn(c store.Connection) tea.Cmd {
	if s, ok := m.mgr.Get(c.ID); ok {
		m.activeID = s.ID
		m.focus = focusTerm
		m.refreshSessions()
		return nil
	}

	var secret string
	needAsk := false
	if c.AuthType == store.AuthKey {
		secret = c.KeyPassphrase
		if c.AskPassphrase {
			needAsk = true
			secret = ""
		}
	} else {
		secret = c.Password
		if secret == "" || c.AskPassword {
			needAsk = true
			secret = ""
		}
	}

	if needAsk {
		title := "需要密码"
		if c.AuthType == store.AuthKey {
			title = "需要私钥口令"
		}
		conn := c
		m.dlg = newSecretDialog(title, conn.User+"@"+conn.Host, func(m *Model, v []string) {
			pw := ""
			if len(v) > 0 {
				pw = v[0]
			}
			m.doOpen(conn, pw)
		})
		return nil
	}

	m.doOpen(c, secret)
	return nil
}

// doOpen 打开会话并切换到它。
func (m *Model) doOpen(c store.Connection, secret string) {
	l := m.computeLayout()
	scrollback := m.st.GetSettings().Scrollback
	s, _ := m.mgr.Open(c, secret, l.termCol, l.termRow, scrollback)
	m.activeID = s.ID
	m.focus = focusTerm
	m.inputMode = false
	m.refreshSessions()
}

// ---------- 对话框动作 ----------

// openConnDialog 打开连接编辑表单（ID 为空表示新建）。
func (m *Model) openConnDialog(c store.Connection) tea.Cmd {
	title := "新建连接"
	if c.ID != "" {
		title = "编辑连接"
	}
	origin := c
	m.dlg = newConnDialog(title, c, func(m *Model, v []string) {
		if len(v) < 13 {
			return
		}
		nc := store.Connection{
			ID:            origin.ID,
			Name:          v[0],
			Group:         v[1],
			Host:          strings.TrimSpace(v[2]),
			Port:          atoi(v[3]),
			User:          v[4],
			AuthType:      store.AuthType(v[5]),
			Password:      v[6],
			AskPassword:   parseBool(v[7]),
			KeyPath:       v[8],
			KeyPassphrase: v[9],
			AskPassphrase: parseBool(v[10]),
			StartupCmd:    v[11],
			Note:          v[12],
			CreatedAt:     origin.CreatedAt,
		}
		if nc.Port == 0 {
			nc.Port = store.DefaultSSHPort
		}
		if nc.Host == "" {
			m.setMsg("主机不能为空")
			return
		}
		if nc.AuthType == store.AuthKey {
			nc.Password = ""
		}
		if origin.ID == "" {
			nc = m.st.AddConnection(nc)
		} else {
			if err := m.st.UpdateConnection(nc); err != nil {
				m.setMsg(err.Error())
				return
			}
		}
		if err := m.st.Save(); err != nil {
			m.setMsg("保存失败：" + err.Error())
			return
		}
		m.filtering = false
		m.connQuery = ""
		m.refresh()
		for i, x := range m.connList {
			if x.ID == nc.ID {
				m.connSel = i
				m.ensureVisible(focusConn)
				break
			}
		}
		m.connectConn(nc)
	})
	return nil
}

func (m *Model) editSelectedConn() tea.Cmd {
	if len(m.connList) == 0 {
		return nil
	}
	c := m.connList[m.connSel]
	return m.openConnDialog(c)
}

func (m *Model) deleteSelectedConn() tea.Cmd {
	if len(m.connList) == 0 {
		return nil
	}
	c := m.connList[m.connSel]
	m.dlg = newConfirmDialog("删除连接", "确定删除「"+c.Name+"」("+c.Host+")？已打开的会话会一并关闭。", func(m *Model, _ []string) {
		m.mgr.Close(c.ID)
		if err := m.st.RemoveConnection(c.ID); err != nil {
			m.setMsg(err.Error())
			return
		}
		_ = m.st.Save()
		m.refresh()
		m.setMsg("已删除连接")
	})
	return nil
}

// openFavDialog 把当前命令行内容加入收藏。
func (m *Model) openFavDialog() tea.Cmd {
	cmd := strings.TrimSpace(string(m.input))
	if cmd == "" {
		m.setMsg("命令行是空的，先输入命令再按 Ctrl+P")
		return nil
	}
	m.dlg = newFavDialog("添加收藏命令", store.FavoriteCmd{Cmd: cmd, Name: cmd}, func(m *Model, v []string) {
		if len(v) < 3 {
			return
		}
		if strings.TrimSpace(v[1]) == "" {
			m.setMsg("命令不能为空")
			return
		}
		f := m.st.AddFavorite(store.FavoriteCmd{Name: v[0], Cmd: v[1], Group: v[2]})
		_ = m.st.Save()
		m.refresh()
		for i, x := range m.favList {
			if x.ID == f.ID {
				m.favSel = i
				m.ensureVisible(focusFav)
				break
			}
		}
		m.setMsg("已加入收藏")
	})
	return nil
}

func (m *Model) openHelp() {
	m.dlg = &dlg{
		kind:    dlgHelp,
		title:   "快捷键",
		body:    helpBody(),
		okLabel: "关闭",
		onOK:    nil,
	}
}
