package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"sshtool/internal/remotessh"
)

// rect 描述屏幕上的一个矩形区域。
type rect struct{ x, y, w, h int }

// layout 存放一次布局计算的结果，渲染与鼠标命中测试共用。
type layout struct {
	header  rect
	panels  [3]rect
	term    rect
	status  rect
	body    rect
	leftW   int
	rightW  int
	termCol int // 终端内容列数
	termRow int // 终端输出行数
}

// tabBound 描述头部一个标签的点击区域。
type tabBound struct {
	id      string
	x0, x1  int
	cx0     int // 关闭按钮起点
	cx1     int // 关闭按钮终点（不含）
	newConn bool
}

const (
	headerH = 2
	statusH = 1
	minLeft = 24
	maxLeft = 44
)

// computeLayout 按 2:8 划分左右两列，左列按 4:3:3 划分三个面板。
func (m *Model) computeLayout() layout {
	var l layout
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}

	l.header = rect{0, 0, w, headerH}
	l.status = rect{0, h - 1, w, statusH}

	bodyH := h - headerH - statusH
	if bodyH < 9 {
		bodyH = 9
	}
	l.body = rect{0, headerH, w, bodyH}

	leftW := w * 2 / 10
	if leftW < minLeft {
		leftW = minLeft
	}
	if leftW > maxLeft {
		leftW = maxLeft
	}
	if leftW > w-24 {
		leftW = w - 24
	}
	if leftW < 12 {
		leftW = 12
	}
	l.leftW = leftW
	l.rightW = w - leftW

	hs := splitHeights(bodyH, []int{4, 3, 3}, 6)
	y := headerH
	for i := 0; i < 3; i++ {
		l.panels[i] = rect{0, y, leftW, hs[i]}
		y += hs[i]
	}

	l.term = rect{leftW, headerH, l.rightW, bodyH}
	l.termCol = l.rightW - 2
	l.termRow = bodyH - 3
	if l.termCol < 8 {
		l.termCol = 8
	}
	if l.termRow < 1 {
		l.termRow = 1
	}
	return l
}

// splitHeights 按权重把 total 行分配给 n 个区块，并保证每个区块不小于 minH。
func splitHeights(total int, weights []int, minH int) []int {
	n := len(weights)
	out := make([]int, n)
	if total < n*minH {
		base := total / n
		for i := range out {
			out[i] = base
		}
		out[0] += total - base*n
		for i := range out {
			if out[i] < 1 {
				out[i] = 1
			}
		}
		return out
	}
	sum := 0
	for _, x := range weights {
		sum += x
	}
	assigned := 0
	for i := 0; i < n-1; i++ {
		v := total * weights[i] / sum
		if v < minH {
			v = minH
		}
		out[i] = v
		assigned += v
	}
	out[n-1] = total - assigned
	if out[n-1] < minH {
		diff := minH - out[n-1]
		out[n-1] = minH
		for i := 0; i < n-1 && diff > 0; i++ {
			take := diff
			if out[i]-minH < take {
				take = out[i] - minH
			}
			if take > 0 {
				out[i] -= take
				diff -= take
			}
		}
	}
	return out
}

// ---------- Header ----------

// buildTabs 生成头部标签栏，并记录每个标签的点击区域。
func (m *Model) buildTabs(w int) (string, []tabBound) {
	m.tabBounds = m.tabBounds[:0]

	var sb strings.Builder
	x := 1

	add := func(text string, bound tabBound) {
		tw := lipgloss.Width(text)
		if x+tw > w-2 {
			return
		}
		sb.WriteString(text)
		bound.x0 = x
		bound.x1 = x + tw
		x = bound.x1
		m.tabBounds = append(m.tabBounds, bound)
	}

	for i, s := range m.sessions {
		idx := i + 1
		label := s.Label()
		if len([]rune(label)) > 14 {
			label = string([]rune(label)[:13]) + "…"
		}
		num := fmt.Sprintf("%d", idx%10)

		dotStyle := lipgloss.NewStyle()
		switch s.State() {
		case remotessh.StateConnected:
			dotStyle = dotStyle.Foreground(cOK)
		case remotessh.StateConnecting:
			dotStyle = dotStyle.Foreground(cWarn)
		case remotessh.StateError:
			dotStyle = dotStyle.Foreground(cErr)
		default:
			dotStyle = dotStyle.Foreground(cDim)
		}
		dot := dotStyle.Render("●")

		body := num + " " + label
		if s.ID == m.activeID {
			body = lipgloss.NewStyle().Background(cAccent).Foreground(cSelFg).Bold(true).Render(body)
		} else {
			body = lipgloss.NewStyle().Foreground(cFg).Render(body)
		}
		closeBtn := lipgloss.NewStyle().Foreground(cDim).Render(" × ")

		bodyW := lipgloss.Width(dot) + lipgloss.Width(body)
		whole := " " + dot + body + closeBtn
		add(whole, tabBound{
			id:  s.ID,
			cx0: x + 1 + bodyW,
			cx1: x + 1 + bodyW + lipgloss.Width(closeBtn) - 1,
		})
	}

	add(lipgloss.NewStyle().Foreground(cAccent2).Bold(true).Render(" ＋ 新连接 "), tabBound{newConn: true})

	line := sb.String()
	hint := styleDim.Render(" Alt+数字切换 · Ctrl+N新建 · Ctrl+H帮助 · Ctrl+Q退出 ")
	tw, hw := lipgloss.Width(line), lipgloss.Width(hint)
	if tw+hw < w {
		line += strings.Repeat(" ", w-tw-hw) + hint
	} else {
		line += strings.Repeat(" ", max(0, w-tw))
	}
	return line, m.tabBounds
}

// renderHeader 渲染头部两行：标签栏 + 当前会话信息。
func (m *Model) renderHeader(l layout) string {
	tabLine, _ := m.buildTabs(l.header.w)

	var info string
	if s := m.activeSession(); s != nil {
		stateStyle := lipgloss.NewStyle().Foreground(cOK)
		switch s.State() {
		case remotessh.StateConnecting:
			stateStyle = stateStyle.Foreground(cWarn)
		case remotessh.StateClosed:
			stateStyle = stateStyle.Foreground(cDim)
		case remotessh.StateError:
			stateStyle = stateStyle.Foreground(cErr)
		}
		c := s.Conn
		info = "  " +
			lipgloss.NewStyle().Foreground(cAccent).Render(c.User+"@"+c.Host) +
			styleDim.Render(fmt.Sprintf(":%d", c.Port)) +
			styleDim.Render("  ·  ") +
			stateStyle.Render(s.State().String())
		if err := s.Err(); err != nil {
			info += styleDim.Render("  ·  ") + lipgloss.NewStyle().Foreground(cErr).Render(err.Error())
		}
		info += styleDim.Render(fmt.Sprintf("  ·  %dx%d", s.Cols(), s.Rows()))
	} else {
		info = "  尚未连接任何服务器 —— 点击左侧「保存的连接」，或按 Ctrl+N 新建连接"
	}

	return styleHeader.Render(padVisible(tabLine, l.header.w)) + "\n" +
		styleHeader.Render(padVisible(info, l.header.w))
}

// ---------- 左侧面板 ----------

// renderPanel 渲染一个通用列表面板：第一行是标题，其余为列表项。
func (m *Model) renderPanel(r rect, title string, active bool, items []string, accents []string, sel, scroll int) string {
	contentW := r.w - 2
	contentH := r.h - 2
	if contentW < 1 || contentH < 1 {
		return strings.Repeat(" ", max(1, r.w))
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render(truncateVisible(title, contentW)))

	rows := contentH - 1
	for i := 0; i < rows; i++ {
		b.WriteString("\n")
		idx := scroll + i
		if idx >= len(items) {
			continue
		}
		acc := ""
		if idx < len(accents) {
			acc = accents[idx]
		}
		b.WriteString(renderItem(contentW, idx == sel, items[idx], acc))
	}

	return boxStyle(r.w, r.h, active).Render(b.String())
}

// renderConnPanel 渲染「保存的连接」面板。
func (m *Model) renderConnPanel(r rect) string {
	title := fmt.Sprintf("① 保存的连接 (%d)", len(m.connList))
	if m.connQuery != "" {
		title += " /" + m.connQuery
	}
	items := make([]string, 0, len(m.connList))
	accents := make([]string, 0, len(m.connList))
	for _, c := range m.connList {
		name := c.Name
		if name == "" {
			name = c.Host
		}
		prefix := "  "
		if _, ok := m.mgr.Get(c.ID); ok {
			prefix = "● "
		}
		items = append(items, prefix+name)
		accents = append(accents, c.User+"@"+c.Host)
	}
	if len(items) == 0 {
		items = append(items, "  (空) 按 Ctrl+N 新建")
		accents = append(accents, "")
	}
	return m.renderPanel(r, title, m.focus == focusConn, items, accents, m.connSel, m.connScroll)
}

// renderFavPanel 渲染「收藏的命令」面板。
func (m *Model) renderFavPanel(r rect) string {
	title := fmt.Sprintf("② 收藏命令 (%d)", len(m.favList))
	if m.favQuery != "" {
		title += " /" + m.favQuery
	}
	items := make([]string, 0, len(m.favList))
	accents := make([]string, 0, len(m.favList))
	for _, f := range m.favList {
		label := f.Name
		if label == "" {
			label = f.Cmd
		}
		items = append(items, "  "+label)
		accents = append(accents, f.Group)
	}
	if len(items) == 0 {
		items = append(items, "  (空) Ctrl+P 添加收藏")
		accents = append(accents, "")
	}
	return m.renderPanel(r, title, m.focus == focusFav, items, accents, m.favSel, m.favScroll)
}

// renderHistPanel 渲染「历史命令」面板。
func (m *Model) renderHistPanel(r rect) string {
	title := fmt.Sprintf("③ 历史命令 (%d)", len(m.histList))
	if m.histQuery != "" {
		title += " /" + m.histQuery
	}
	items := make([]string, 0, len(m.histList))
	accents := make([]string, 0, len(m.histList))
	for _, h := range m.histList {
		items = append(items, "  "+h.Cmd)
		accents = append(accents, h.At.Format("01-02 15:04"))
	}
	if len(items) == 0 {
		items = append(items, "  (空)")
		accents = append(accents, "")
	}
	return m.renderPanel(r, title, m.focus == focusHist, items, accents, m.histSel, m.histScroll)
}

// ---------- 终端 ----------

// renderTerminal 渲染右侧终端面板（输出区 + 命令行输入行）。
func (m *Model) renderTerminal(l layout) string {
	r := l.term
	contentW := l.termCol
	outRows := l.termRow
	if contentW < 1 || outRows < 1 {
		return ""
	}

	lines := make([]string, 0, outRows+1)

	if s := m.activeSession(); s == nil {
		hints := []string{
			"",
			"   尚未连接到任何服务器",
			"",
			"   · 点击左侧「保存的连接」或按 Enter 连接",
			"   · Ctrl+N 新建连接",
			"   · Ctrl+H 查看全部快捷键",
		}
		for i := 0; i < outRows; i++ {
			if i < len(hints) {
				lines = append(lines, styleDim.Render(padVisible(hints[i], contentW)))
			} else {
				lines = append(lines, strings.Repeat(" ", contentW))
			}
		}
	} else {
		term := s.Term()
		rows := term.Render()

		start := 0
		if len(rows) > outRows {
			start = len(rows) - outRows
		}
		for i := 0; i < outRows; i++ {
			idx := start + i
			if idx >= 0 && idx < len(rows) {
				lines = append(lines, padVisible(rows[idx], contentW))
			} else {
				lines = append(lines, strings.Repeat(" ", contentW))
			}
		}

		// 光标：终端获得焦点且处于直通模式时绘制
		if m.focus == focusTerm && !m.inputMode && s.State() == remotessh.StateConnected {
			if cx, cy, vis := term.Cursor(); vis && cy >= 0 && cy < len(lines) && cx >= 0 && cx < contentW {
				lines[cy] = highlightCell(lines[cy], cx, contentW)
			}
		}
	}

	// 命令行输入行
	promptStyle := lipgloss.NewStyle().Foreground(cDim)
	promptText := "> "
	if m.inputMode {
		promptStyle = lipgloss.NewStyle().Foreground(cOK).Bold(true)
		promptText = "❯ "
	}
	line := padVisible(promptStyle.Render(promptText)+string(m.input), contentW)
	if m.inputMode {
		col := lipgloss.Width(promptText) + ansiWidth(string(m.input[:minInt(m.inputPos, len(m.input))]))
		if col >= contentW {
			col = contentW - 1
		}
		line = highlightCell(line, col, contentW)
	}
	lines = append(lines, line)

	return boxStyle(r.w, r.h, m.focus == focusTerm).Render(strings.Join(lines, "\n"))
}

// ---------- 状态栏 ----------

// renderStatus 渲染底部状态栏。
func (m *Model) renderStatus(l layout) string {
	var left string
	if m.msg != "" {
		left = " " + m.msg
		left = lipgloss.NewStyle().Foreground(cWarn).Render(left)
	} else {
		left = " Tab 切换焦点 · Enter 执行 · / 过滤 · Ctrl+X 命令行 · Ctrl+W 关闭 · PgUp/PgDn 回滚 · Ctrl+H 帮助"
		left = styleHint.Render(left)
	}
	right := ""
	if s := m.activeSession(); s != nil && s.Term().ScrollOffset() > 0 {
		right = lipgloss.NewStyle().Foreground(cAccent2).Render(fmt.Sprintf("回滚 %d 行 · End 返回最新 ", s.Term().ScrollOffset()))
	}
	lr := max(0, l.status.w-lipgloss.Width(right))
	return styleStatusBar.Render(padVisible(left, lr) + right)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
