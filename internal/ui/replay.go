package ui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"sshtool/internal/vt"
)

// replayModal 是「会话回放」全屏视图：把已落盘的终端日志重新喂给一个 vt 终端并只读渲染。
type replayModal struct {
	term    *vt.Terminal
	path    string
	cols    int
	rows    int
	loadErr string
}

// openReplay 打开当前活动会话的日志回放；未开启记录时给出提示。
func (m *Model) openReplay() tea.Cmd {
	s := m.activeSession()
	if s == nil {
		m.setMsg("当前没有活动的会话")
		return nil
	}
	if isLocalShell(s) {
		m.setMsg("本地 shell 不支持会话日志回放（Ctrl+L 仅用于 SSH 会话）")
		return nil
	}
	if !s.Logging() {
		m.setMsg("当前会话未记录日志：设置环境变量 SSHTOOL_LOG_DIR 指向目录后重连即开启记录")
		return nil
	}
	rs := sshSessionOf(s)
	if rs == nil {
		return nil
	}
	path := rs.LogPath()
	data, err := os.ReadFile(path)
	if err != nil {
		m.setMsg("读取回放日志失败：" + err.Error())
		return nil
	}
	l := m.computeLayout()
	cols, rows := l.termCol, l.termRow
	if cols < 8 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	term := vt.New(cols, rows, 500000)
	term.Write(data)
	term.ScrollToBottom()
	m.replay = &replayModal{term: term, path: path, cols: cols, rows: rows}
	return nil
}

// handleReplayKey 处理回放视图内的按键：滚动与退出。
func (m *Model) handleReplayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.replay
	if r == nil {
		return m, nil
	}
	switch {
	case msg.Type == tea.KeyEsc, msg.Type == tea.KeyCtrlQ:
		m.replay = nil
	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q'):
		m.replay = nil
	case msg.Type == tea.KeyUp:
		r.term.ScrollBy(-1)
	case msg.Type == tea.KeyDown:
		r.term.ScrollBy(1)
	case msg.Type == tea.KeyPgUp:
		r.term.ScrollBy(-r.rows)
	case msg.Type == tea.KeyPgDown:
		r.term.ScrollBy(r.rows)
	case msg.Type == tea.KeyEnd:
		r.term.ScrollToBottom()
	case msg.Type == tea.KeyHome:
		r.term.ScrollToLine(0)
	}
	return m, nil
}

// renderReplay 渲染回放全屏视图。
func (m *Model) renderReplay() string {
	r := m.replay
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}
	outRows := h - 4 // 标题 1 + 底部提示 1，留白在边框内
	if outRows < 1 {
		outRows = 1
	}

	lines := make([]string, 0, outRows)
	if r.loadErr != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(cErr).Render(padVisible(r.loadErr, contentW)))
		for i := 1; i < outRows; i++ {
			lines = append(lines, strings.Repeat(" ", contentW))
		}
	} else {
		rows := r.term.Render()
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
	}

	title := styleTitle.Render(" 会话回放（只读）  " + r.path)
	hint := styleDim.Render(" ↑↓/PgUp/PgDn 滚动 · End 回到最新 · Esc/Q 退出 ")
	inner := strings.Join(lines, "\n")
	return boxStyle(w, h, true).Render(padVisible(title, w) + "\n" + inner + "\n" + padVisible(hint, w))
}
