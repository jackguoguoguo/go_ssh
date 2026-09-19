package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/store"
)

// ---------- 键盘 ----------

// onKey 统一入口：全局快捷键 → 对话框 → 面板 / 终端。
func (m *Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 回放视图优先：仅处理滚动与退出，其它一律拦截。
	if m.replay != nil {
		return m.handleReplayKey(msg)
	}

	// 全局：退出
	if msg.Type == tea.KeyCtrlQ {
		if m.dlg != nil {
			// 走 closeDialog 以触发 onCancel：指纹确认框依赖取消回调推进队列。
			m.closeDialog()
			return m, nil
		}
		m.dlg = newConfirmDialog("退出 sshtool", "将断开全部会话，确定退出吗？", func(m *Model, _ []string) {
			m.quitting = true
		})
		return m, nil
	}

	// 全局：Alt+数字切换会话
	if msg.Alt && len(msg.Runes) == 1 {
		r := msg.Runes[0]
		if r >= '1' && r <= '9' {
			m.switchTo(int(r - '1'))
			return m, m.armOutput()
		}
		if r == '0' {
			m.switchTo(len(m.sessions) - 1)
			return m, m.armOutput()
		}
	}
	if msg.Alt && (msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight) {
		cur := m.activeIndex()
		if cur < 0 {
			cur = 0
		}
		if msg.Type == tea.KeyLeft {
			m.switchTo(cur - 1)
		} else {
			m.switchTo(cur + 1)
		}
		return m, m.armOutput()
	}

	// 全局：Alt+字母功能键。
	// 键位刻意避开了被远端高频占用的组合：
	//   Ctrl+A（bash 行首 / GNU screen 前缀）、Ctrl+F（less、vim 翻页、readline）、
	//   Alt+N/Alt+P（bash 的 M-n/M-p 历史搜索），所以这里统一用 Alt+S/A///[/]。
	if msg.Alt && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case 's':
			return m, m.toggleSelectMode()
		case 'a':
			return m, m.toggleBroadcast()
		case '/':
			return m, m.openSearch()
		case '[':
			return m, m.gotoHit(-1)
		case ']':
			return m, m.gotoHit(1)
		}
	}

	// 全局：帮助
	if msg.Type == tea.KeyCtrlH || msg.Type == tea.KeyF1 {
		if m.dlg != nil {
			m.closeDialog()
			return m, nil
		}
		m.openHelp()
		return m, nil
	}

	// 对话框优先
	if m.dlg != nil {
		m.handleDialogKey(msg)
		if m.quitting {
			return m, tea.Quit
		}
		// 对话框回调可能派生出异步命令（生成密钥 / 推送公钥）
		cmd := m.takeAfterCmd()
		return m, tea.Batch(cmd, m.armOutput())
	}

	// 选择模式：按键全部用于移动选区，一律不透传给远端
	if m.selMode {
		return m, tea.Batch(m.selectKey(msg), m.armOutput())
	}

	// 命令行编辑模式下，除少数功能键外全部交给输入行处理，
	// 避免 Ctrl+W（删除单词）等编辑键被全局快捷键抢走。
	if (m.inputMode || m.searchMode) && m.focus == focusTerm {
		switch msg.Type {
		case tea.KeyCtrlP, tea.KeyCtrlX, tea.KeyCtrlH, tea.KeyTab, tea.KeyShiftTab, tea.KeyEsc:
			// 继续走全局逻辑
		default:
			return m.termKey(msg)
		}
	}

	switch msg.Type {
	case tea.KeyCtrlN:
		return m, m.openConnDialog(store.Connection{Port: store.DefaultSSHPort, User: "root"})
	case tea.KeyCtrlW:
		m.closeActiveSession()
		return m, m.armOutput()
	case tea.KeyCtrlR:
		m.reconnectActive()
		return m, m.armOutput()
	case tea.KeyCtrlB:
		m.setFocus(focusFav)
		return m, nil
	case tea.KeyCtrlK:
		m.setFocus(focusHist)
		m.filtering = true
		return m, nil
	case tea.KeyCtrlP:
		return m, m.openFavDialog()
	case tea.KeyCtrlX:
		m.focus = focusTerm
		m.inputMode = true
		return m, nil
	case tea.KeyCtrlG:
		return m, m.openKeyManager()
	case tea.KeyCtrlO:
		return m, m.openFileBrowser()
	case tea.KeyCtrlL:
		return m, m.openReplay()
	case tea.KeyTab, tea.KeyShiftTab:
		step := 1
		if msg.Type == tea.KeyShiftTab {
			step = -1
		}
		m.setFocus((m.focus + step + 4) % 4)
		return m, nil
	}

	if m.focus == focusTerm {
		return m.termKey(msg)
	}
	return m.panelKey(msg)
}

// setFocus 切换焦点并重置过滤态。
func (m *Model) setFocus(f int) {
	m.focus = f
	m.filtering = false
	if f != focusTerm {
		m.inputMode = false
	}
}

// panelKey 处理左侧三个面板的按键。
func (m *Model) panelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := m.focus
	n := m.countOf(i)

	// 过滤输入
	if m.filtering {
		switch msg.Type {
		case tea.KeyEsc:
			m.filtering = false
			m.setQuery(i, "")
			m.refresh()
			return m, nil
		case tea.KeyBackspace:
			q := m.queryOf(i)
			if len(q) == 0 {
				m.filtering = false
			} else {
				m.setQuery(i, q[:len(q)-1])
			}
			m.refresh()
			return m, nil
		case tea.KeyEnter:
			m.filtering = false
			return m.activate(i)
		}
		if len(msg.Runes) > 0 && msg.Runes[0] >= 0x20 {
			m.setQuery(i, m.queryOf(i)+string(msg.Runes))
			m.refresh()
			return m, nil
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyUp:
		m.moveSel(i, -1)
		return m, nil
	case tea.KeyDown:
		m.moveSel(i, 1)
		return m, nil
	case tea.KeyPgUp:
		m.moveSel(i, -m.panelRows(i))
		return m, nil
	case tea.KeyPgDown:
		m.moveSel(i, m.panelRows(i))
		return m, nil
	case tea.KeyHome:
		m.moveSel(i, -1000000)
		return m, nil
	case tea.KeyEnd:
		m.moveSel(i, 1000000)
		return m, nil
	case tea.KeyEnter:
		return m.activate(i)
	case tea.KeyEsc:
		m.inputMode = false
		m.focus = focusTerm
		return m, nil
	}

	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case '/':
			m.filtering = true
			return m, nil
		case 'j':
			m.moveSel(i, 1)
			return m, nil
		case 'k':
			m.moveSel(i, -1)
			return m, nil
		}
	}

	if i == focusConn {
		switch msg.Type {
		case tea.KeyCtrlE:
			return m, m.editSelectedConn()
		case tea.KeyCtrlD:
			return m, m.deleteSelectedConn()
		}
	}
	_ = n
	return m, nil
}

// termKey 处理终端区域的按键。
func (m *Model) termKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 命令行编辑模式 / 搜索输入模式：两者共用输入行，但提交行为不同
	if m.inputMode || m.searchMode {
		switch msg.Type {
		case tea.KeyEsc:
			if m.searchMode {
				// 退出搜索并清除高亮（关键字保留，便于再次进入继续查找）
				m.searchMode = false
				m.input = nil
				m.inputPos = 0
				m.clearHits()
				return m, nil
			}
			m.inputMode = false
			return m, nil
		case tea.KeyEnter:
			if m.searchMode {
				return m, tea.Batch(m.runSearch(), m.armOutput())
			}
			cmd := strings.TrimSpace(string(m.input))
			m.input = nil
			m.inputPos = 0
			m.inputMode = false
			if cmd != "" {
				m.runCommand(cmd)
			}
			return m, m.armOutput()
		case tea.KeyBackspace:
			if m.inputPos > 0 {
				m.input = append(m.input[:m.inputPos-1], m.input[m.inputPos:]...)
				m.inputPos--
			}
			return m, nil
		case tea.KeyDelete:
			if m.inputPos < len(m.input) {
				m.input = append(m.input[:m.inputPos], m.input[m.inputPos+1:]...)
			}
			return m, nil
		case tea.KeyLeft:
			if m.inputPos > 0 {
				m.inputPos--
			}
			return m, nil
		case tea.KeyRight:
			if m.inputPos < len(m.input) {
				m.inputPos++
			}
			return m, nil
		case tea.KeyHome, tea.KeyCtrlA:
			m.inputPos = 0
			return m, nil
		case tea.KeyEnd, tea.KeyCtrlE:
			m.inputPos = len(m.input)
			return m, nil
		case tea.KeyCtrlU:
			m.input = nil
			m.inputPos = 0
			return m, nil
		case tea.KeyCtrlW:
			// 删除前一个单词
			p := m.inputPos
			for p > 0 && m.input[p-1] == ' ' {
				p--
			}
			for p > 0 && m.input[p-1] != ' ' {
				p--
			}
			m.input = append(m.input[:p], m.input[m.inputPos:]...)
			m.inputPos = p
			return m, nil
		case tea.KeyUp:
			if m.searchMode {
				return m, nil // 搜索输入不走命令历史
			}
			m.fillFromHistory(-1)
			return m, nil
		case tea.KeyDown:
			if m.searchMode {
				return m, nil
			}
			m.fillFromHistory(1)
			return m, nil
		}
		if len(msg.Runes) > 0 && msg.Runes[0] >= 0x20 {
			r := append([]rune{}, msg.Runes...)
			m.input = append(m.input[:m.inputPos], append(r, m.input[m.inputPos:]...)...)
			m.inputPos += len(r)
			return m, nil
		}
		return m, nil
	}

	// 直通模式：本地只拦截回滚键，其余全部发往远端
	switch msg.Type {
	case tea.KeyPgUp:
		m.scrollTerm(-m.termRows())
		return m, nil
	case tea.KeyPgDown:
		m.scrollTerm(m.termRows())
		return m, nil
	case tea.KeyEnd:
		if s := m.activeSession(); s != nil {
			s.Term().ScrollToBottom()
		}
		return m, nil
	case tea.KeyEsc:
		m.focus = focusConn
		return m, nil
	}

	data := keyToBytes(msg)
	if len(data) > 0 {
		m.sendRaw(data)
	}
	return m, m.armOutput()
}

// fillFromHistory 在命令行中回溯历史命令（dir=-1 上一条，dir=1 下一条）。
func (m *Model) fillFromHistory(dir int) {
	if len(m.histList) == 0 {
		return
	}
	cur := string(m.input)
	idx := -1
	for i, h := range m.histList {
		if h.Cmd == cur {
			idx = i
			break
		}
	}
	if idx < 0 {
		if dir < 0 {
			m.setInput(m.histList[0].Cmd)
		}
		return
	}
	next := idx - dir
	if next < 0 {
		next = 0
	}
	if next >= len(m.histList) {
		m.setInput("")
		return
	}
	m.setInput(m.histList[next].Cmd)
}

// keyToBytes 把按键转换为发往远端 PTY 的字节序列。
func keyToBytes(msg tea.KeyMsg) []byte {
	alt := ""
	if msg.Alt {
		alt = "\x1b"
	}

	switch msg.Type {
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	}

	// Ctrl 组合键：用 String() 反解控制字符
	s := msg.String()
	if strings.HasPrefix(s, "ctrl+") {
		rest := s[5:]
		special := map[string]byte{
			"@": 0, "space": 0, "[": 27, "\\": 28, "]": 29, "^": 30, "_": 31, "?": 127,
		}
		if v, ok := special[rest]; ok {
			return []byte{v}
		}
		if len(rest) == 1 && rest[0] >= 'a' && rest[0] <= 'z' {
			return []byte{rest[0] - 'a' + 1}
		}
		return nil
	}

	if len(msg.Runes) > 0 {
		if msg.Runes[0] < 0x20 {
			return []byte{byte(msg.Runes[0])}
		}
		return []byte(alt + string(msg.Runes))
	}
	if alt != "" {
		return []byte(alt)
	}
	return nil
}

// ---------- 鼠标 ----------

// onMouse 处理鼠标事件（点击 + 滚轮）。
func (m *Model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x, y := msg.X, msg.Y
	l := m.computeLayout()

	if m.dlg != nil {
		if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.dialogMouse(x, y)
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.wheel(x, y, -3)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.wheel(x, y, 3)
		return m, nil
	}

	// 拖选中的移动事件：终端区域才处理
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionMotion {
		if m.selDrag {
			if col, row, ok := m.termCellAt(x, y); ok {
				m.updateMouseSelect(col, row)
			}
		}
		// 非拖选状态直接丢弃：开启 all-motion 后事件量很大
		return m, nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease {
		if m.selDrag {
			return m, m.finishMouseSelect()
		}
		return m, nil
	}

	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}

	// 头部标签栏
	if y == l.header.y {
		for _, tb := range m.tabBounds {
			if x < tb.x0 || x >= tb.x1 {
				continue
			}
			if tb.newConn {
				return m, m.openConnDialog(store.Connection{Port: store.DefaultSSHPort, User: "root"})
			}
			if !tb.newConn && x >= tb.cx0 && x < tb.cx1 {
				m.mgr.Close(tb.id)
				m.refreshSessions()
				return m, m.armOutput()
			}
			m.activeID = tb.id
			m.focus = focusTerm
			return m, m.armOutput()
		}
		return m, nil
	}

	// 左侧面板
	if x < l.leftW {
		for i := range l.panels {
			r := l.panels[i]
			if y < r.y || y >= r.y+r.h {
				continue
			}
			m.setFocus(i)
			idx := m.scrollOf(i) + (y - (r.y + 2)) // 边框 1 行 + 标题 1 行
			n := m.countOf(i)
			if idx < 0 {
				idx = 0
			}
			if n > 0 && idx >= n {
				idx = n - 1
			}
			if n > 0 {
				m.setSel(i, idx)
				m.ensureVisible(i)
			}
			key := fmt.Sprintf("%d:%d", i, idx)
			now := time.Now()
			if m.lastClickKey == key && now.Sub(m.lastClickAt) < 450*time.Millisecond {
				m.lastClickKey = ""
				return m.activate(i)
			}
			m.lastClickKey = key
			m.lastClickAt = now
			return m, nil
		}
		return m, nil
	}

	// 右侧终端
	m.focus = focusTerm
	inputY := l.term.y + 1 + l.termRow
	if y == inputY {
		m.inputMode = true
		return m, nil
	}
	if y < inputY {
		m.inputMode = false
	}
	// 输出区按下左键 = 开始拖选（松开时若拖出过区域则自动复制）
	if col, row, ok := m.termCellAt(x, y); ok {
		m.startMouseSelect(col, row)
	}
	return m, nil
}

// termCellAt 把屏幕坐标换算成终端视口内的列/行；不在输出区时返回 false。
func (m *Model) termCellAt(x, y int) (col, row int, ok bool) {
	l := m.computeLayout()
	col = x - (l.term.x + 1)
	row = y - (l.term.y + 1)
	if col < 0 || col >= l.termCol || row < 0 || row >= l.termRow {
		return 0, 0, false
	}
	return col, row, true
}

// dialogMouse 处理对话框内的点击。
func (m *Model) dialogMouse(x, y int) (tea.Model, tea.Cmd) {
	hit := m.dlgHit
	if !hit.valid {
		return m, nil
	}
	if y == hit.btnY {
		if x >= hit.okX0 && x < hit.okX1 {
			d := m.dlg
			values := collectValues(d)
			fn := d.onOK
			m.dlg = nil
			if fn != nil {
				fn(m, values)
			}
			if m.quitting {
				return m, tea.Quit
			}
			return m, m.armOutput()
		}
		if x >= hit.cancelX0 && x < hit.cancelX1 {
			m.closeDialog()
			return m, nil
		}
		return m, nil
	}
	for i, fy := range hit.fieldY {
		if y >= fy-1 && y <= fy+1 && i < len(m.dlg.fields) {
			m.dlg.focus = i
			return m, nil
		}
	}
	for i, iy := range hit.itemY {
		if y < iy || y > iy+1 {
			continue
		}
		if m.dlg.cursor == i {
			m.commitPick(m.dlg) // 再次点击当前项 = 确认
			return m, nil
		}
		m.dlg.cursor = i
		return m, nil
	}
	return m, nil
}

// wheel 处理滚轮：左侧滚动列表，右侧滚动终端。
func (m *Model) wheel(x, y, delta int) {
	l := m.computeLayout()
	if x < l.leftW {
		for i := range l.panels {
			r := l.panels[i]
			if y >= r.y && y < r.y+r.h {
				m.setFocus(i)
				m.moveSel(i, sign(delta))
				return
			}
		}
		if m.focus != focusTerm {
			m.moveSel(m.focus, sign(delta))
		}
		return
	}
	m.scrollTerm(delta)
}

func sign(v int) int {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}

// scrollTerm 滚动终端回滚缓冲。
func (m *Model) scrollTerm(delta int) {
	s := m.activeSession()
	if s == nil {
		return
	}
	s.Term().ScrollBy(delta)
}

// termRows 返回终端可见行数。
func (m *Model) termRows() int {
	l := m.computeLayout()
	r := l.termRow
	if r < 1 {
		r = 1
	}
	return r
}
