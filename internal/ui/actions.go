package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/remotessh"
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
	// 切会话后终端缓冲区换了，选择/搜索状态必须复位
	m.resetTermModes()
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

// runCommand 把整条命令发往会话并记录历史。
// 广播模式（Alt+A）下发往所有已连接会话，否则只发当前会话。
func (m *Model) runCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	// 元命令（无需已连接会话）：theme <name> 切换主题。
	if lcmd := strings.ToLower(cmd); strings.HasPrefix(lcmd, "theme ") {
		m.applyThemeByName(strings.TrimSpace(cmd[len("theme "):]))
		return
	}
	s := m.activeSession()
	if s == nil {
		m.setMsg("尚未连接到服务器")
		return
	}
	if m.broadcast {
		targets := m.connectedSessions()
		if len(targets) == 0 {
			m.setMsg("没有已连接的会话，无法广播")
			return
		}
		m.dispatch(cmd, targets)
		return
	}
	if s.State() != remotessh.StateConnected {
		m.setMsg("会话当前不可写（" + s.State().String() + "），可按 Ctrl+R 重连")
		return
	}
	m.dispatch(cmd, []*remotessh.Session{s})
}

// connectedSessions 返回所有可写会话。
func (m *Model) connectedSessions() []*remotessh.Session {
	out := make([]*remotessh.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.State() == remotessh.StateConnected {
			out = append(out, s)
		}
	}
	return out
}

// broadcastTargets 返回广播会覆盖的会话数（供状态栏显示）。
func (m *Model) broadcastTargets() int { return len(m.connectedSessions()) }

// toggleBroadcast 切换广播模式（Alt+A）。
func (m *Model) toggleBroadcast() tea.Cmd {
	if m.broadcast {
		m.broadcast = false
		return m.setMsg("已退出广播模式")
	}
	n := m.broadcastTargets()
	if n == 0 {
		return m.setMsg("没有已连接的会话，无法进入广播模式")
	}
	m.broadcast = true
	return m.setMsg(fmt.Sprintf("广播模式：命令将同时发往 %d 台主机（Alt+A 退出）", n))
}

// dispatch 先做危险命令检查，再真正下发。
func (m *Model) dispatch(cmd string, targets []*remotessh.Session) {
	reason := risky(cmd)
	if reason == "" {
		m.deliver(cmd, targets)
		return
	}
	var names []string
	for _, s := range targets {
		names = append(names, s.Label())
	}
	msg := "命令被判定为高风险操作：" + reason + "\n命令：" + cmd
	if len(targets) > 1 {
		msg += "\n将同时发往 " + fmt.Sprintf("%d", len(targets)) + " 台主机：\n  " + strings.Join(names, "、")
		msg += "\n\n注意：sudo 口令会同时发往全部主机，并留在各自的 shell 历史里。"
	}
	targetsCopy := targets
	d := newConfirmDialog("危险命令确认", msg, func(m *Model, _ []string) {
		m.deliver(cmd, targetsCopy)
	})
	d.strictConfirm = true // 只认 Enter，避免顺手敲 y
	d.onCancel = func(m *Model) { m.setMsg("已取消，命令未发送") }
	m.dlg = d
}

// deliver 真正把命令写进目标会话的 stdin。
func (m *Model) deliver(cmd string, targets []*remotessh.Session) {
	for _, s := range targets {
		_ = s.Write([]byte(cmd + "\r"))
	}
	if len(targets) == 1 {
		m.trackCwd(cmd)
		m.st.AddHistory(cmd, targets[0].Conn.Host)
	} else {
		// 广播只记一条历史，避免把同一条命令刷进历史面板 N 次
		m.st.AddHistory(cmd, fmt.Sprintf("广播(%d 台)", len(targets)))
	}
	_ = m.st.Save()
	m.refresh()
	if len(targets) > 1 {
		m.setMsg(fmt.Sprintf("已发往 %d 台主机：%s", len(targets), cmd))
	}
}

// ---------- 危险命令判定 ----------

// risky 判断命令是否属于「误执行代价极高」的操作，返回命中原因；安全则返回 ""。
//
// 不用简单正则的原因：正则既会误伤（echo a > f、cat reboot.log 都会被拦），
// 又会漏判（rm -rf /*、dd of=/dev/sda、chmod -R 777 / 都绕得过去）。
// 这里按 shell 语义分段，再按「命令 + 参数 + 目标」判定。
func risky(cmd string) string {
	for _, seg := range splitShell(cmd) {
		if reason := riskyRedirect(seg); reason != "" {
			return reason
		}
		fields := stripPrefixes(strings.Fields(seg))
		if len(fields) == 0 {
			continue
		}
		prog := fields[0]
		args := fields[1:]
		switch prog {
		case "rm":
			if hasFlag(args, "r", "R", "recursive") && targetsCritical(args) {
				return "递归删除根目录或系统目录"
			}
		case "chmod", "chown":
			if hasFlag(args, "R") && targetsCritical(args) {
				return "递归修改根目录/系统目录的权限或属主"
			}
		case "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.vfat", "mkswap":
			return "格式化文件系统"
		case "dd":
			for _, a := range args {
				if strings.HasPrefix(a, "of=/dev/") {
					return "dd 直接写块设备"
				}
			}
		case "shutdown", "reboot", "poweroff", "halt":
			return "关机或重启主机"
		case "init":
			if len(args) > 0 && (args[0] == "0" || args[0] == "6") {
				return "关机或重启主机"
			}
		case "systemctl":
			if len(args) > 0 && (args[0] == "stop" || args[0] == "disable" || args[0] == "mask") {
				return "停止 / 禁用系统服务"
			}
		case "pkill", "killall":
			return "按名字批量杀进程"
		case "iptables", "ip6tables", "nft":
			return "修改防火墙规则"
		case "userdel", "groupdel":
			return "删除用户 / 用户组"
		}
	}
	return ""
}

// riskyRedirect 检查重定向目标是否指向块设备或系统目录。
// 只拦「覆盖块设备 / 系统目录」这类不可逆写入，echo a > f 不受影响。
func riskyRedirect(seg string) string {
	for _, op := range []string{">", ">>", ">|"} {
		idx := strings.Index(seg, op)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(seg[idx+len(op):])
		if rest == "" {
			continue
		}
		fields := strings.Fields(rest)
		target := fields[0]
		if strings.HasPrefix(target, "/dev/sd") || strings.HasPrefix(target, "/dev/nvme") ||
			strings.HasPrefix(target, "/dev/vd") || strings.HasPrefix(target, "/dev/xvd") {
			return "重定向覆盖块设备"
		}
		if targetsCritical([]string{target}) {
			return "重定向覆盖系统文件"
		}
	}
	return ""
}

// splitShell 按 ; && || | 把命令行切成单条命令。
func splitShell(cmd string) []string {
	repl := strings.NewReplacer("&&", "\x00", "||", "\x00", ";", "\x00", "|", "\x00")
	out := strings.Split(repl.Replace(cmd), "\x00")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}

// stripPrefixes 剥掉 sudo / env / nice 等前缀及其选项，露出真正的命令。
func stripPrefixes(fields []string) []string {
	for len(fields) > 0 {
		switch fields[0] {
		case "sudo", "nice", "nohup", "command", "exec", "time":
			fields = fields[1:]
			for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
				fields = fields[1:]
			}
		case "env":
			fields = fields[1:]
			for len(fields) > 0 && strings.Contains(fields[0], "=") {
				fields = fields[1:]
			}
		default:
			return fields
		}
	}
	return fields
}

// hasFlag 判断参数里是否带指定开关（支持 -rf 这种合并写法）。
func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "--" {
			continue
		}
		body := strings.TrimLeft(a, "-")
		for _, n := range names {
			if len(n) == 1 {
				if strings.Contains(body, n) {
					return true
				}
			} else if body == n {
				return true
			}
		}
	}
	return false
}

// targetsCritical 判断参数里是否出现根目录 / 系统目录 / 家目录。
func targetsCritical(args []string) bool {
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		switch a {
		case "/", "/*", "~", "$HOME", "${HOME}", "/root", "/home":
			return true
		}
		for _, p := range []string{"/etc", "/usr", "/var", "/boot", "/bin", "/sbin", "/lib", "/opt", "/sys", "/proc", "/dev"} {
			if a == p || strings.HasPrefix(a, p+"/") {
				return true
			}
		}
	}
	return false
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
