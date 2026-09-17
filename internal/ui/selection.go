package ui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/store"
)

// 本文件只放「终端文本选择」的状态与纯逻辑；
// 按键分发在 keys.go，渲染在 panels.go。

// clipboardMsg 是复制命令的结果。
type clipboardMsg struct {
	ok   bool   // 是否写进了系统剪贴板（OSC 52）
	path string // 回退落盘的路径
	err  string
}

// ---------- 模式进出 ----------

// toggleSelectMode 进入或退出选择模式（Alt+S）。
func (m *Model) toggleSelectMode() tea.Cmd {
	if m.selMode {
		return m.exitSelectMode(false)
	}
	s := m.activeSession()
	if s == nil {
		m.setMsg("没有可选择的会话，先连接一台服务器")
		return nil
	}
	m.searchMode = false
	m.inputMode = false
	m.selMode = true
	m.selOn = false
	m.focus = focusTerm

	// 从远端光标处开始，选起来最顺手
	l := m.computeLayout()
	cx, cy, _ := s.Term().Cursor()
	m.selCX, m.selCY = clampInt(cx, 0, l.termCol-1), clampInt(cy, 0, l.termRow-1)
	m.selAX, m.selAY = m.selCX, m.selCY
	return nil
}

// exitSelectMode 退出选择模式；copy 为 true 时先复制当前选区。
func (m *Model) exitSelectMode(copy bool) tea.Cmd {
	if !m.selMode {
		return nil
	}
	var cmd tea.Cmd
	if copy {
		cmd = m.copySelection()
	}
	m.selMode = false
	m.selOn = false
	m.selDrag = false
	if s := m.activeSession(); s != nil {
		s.Term().ClearSelection()
	}
	return cmd
}

// ---------- 键盘操作 ----------

// selectKey 处理选择模式下的按键：这一时期按键一律不透传给远端。
func (m *Model) selectKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		return m.exitSelectMode(false)
	case tea.KeyEnter:
		return m.exitSelectMode(true)
	case tea.KeyUp:
		return m.selectMove(0, -1)
	case tea.KeyDown:
		return m.selectMove(0, 1)
	case tea.KeyLeft:
		return m.selectMove(-1, 0)
	case tea.KeyRight:
		return m.selectMove(1, 0)
	case tea.KeyHome:
		m.selCX = 0
		return m.selectMoved()
	case tea.KeyEnd:
		l := m.computeLayout()
		m.selCX = l.termCol - 1
		return m.selectMoved()
	case tea.KeyPgUp:
		if s := m.activeSession(); s != nil {
			s.Term().ScrollBy(-m.termRows())
		}
		return nil
	case tea.KeyPgDown:
		if s := m.activeSession(); s != nil {
			s.Term().ScrollBy(m.termRows())
		}
		return nil
	}

	if len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case ' ':
			m.toggleSelectAnchor()
			return nil
		case 'v':
			m.toggleSelectAnchor()
			return nil
		case 'y':
			return m.exitSelectMode(true)
		case 'p':
			m.pasteLastCopy()
			return nil
		case 'j':
			return m.selectMove(0, 1)
		case 'k':
			return m.selectMove(0, -1)
		case 'h':
			return m.selectMove(-1, 0)
		case 'l':
			return m.selectMove(1, 0)
		}
	}
	return nil
}

// selectMove 移动选择光标；未按下起点时起点跟随光标。
func (m *Model) selectMove(dx, dy int) tea.Cmd {
	l := m.computeLayout()
	m.selCX = clampInt(m.selCX+dx, 0, l.termCol-1)
	m.selCY = clampInt(m.selCY+dy, 0, l.termRow-1)
	return m.selectMoved()
}

// selectMoved 光标变动后同步起点与 vt 侧的选区。
func (m *Model) selectMoved() tea.Cmd {
	if !m.selOn {
		m.selAX, m.selAY = m.selCX, m.selCY
	}
	m.pushSelection()
	return nil
}

// toggleSelectAnchor 按下/松开选区起点（Space 或 v）。
func (m *Model) toggleSelectAnchor() {
	if m.selOn {
		m.selOn = false
		if s := m.activeSession(); s != nil {
			s.Term().ClearSelection()
		}
		return
	}
	m.selOn = true
	m.selAX, m.selAY = m.selCX, m.selCY
	m.pushSelection()
}

// pushSelection 把当前选区同步给终端缓冲区（由它负责反显渲染）。
func (m *Model) pushSelection() {
	if !m.selOn {
		return
	}
	s := m.activeSession()
	if s == nil {
		return
	}
	s.Term().SetSelection(m.selAX, m.selAY, m.selCX, m.selCY)
}

// ---------- 复制 / 粘贴 ----------

// copySelection 复制当前选区。
func (m *Model) copySelection() tea.Cmd {
	s := m.activeSession()
	if s == nil || !m.selOn {
		m.setMsg("还没有选中内容：Space 按下起点，方向键移动，Enter 复制")
		return nil
	}
	text := s.Term().Text(m.selAX, m.selAY, m.selCX, m.selCY)
	if strings.TrimSpace(text) == "" {
		m.setMsg("选区是空的")
		return nil
	}
	m.lastCopy = text
	return copyClipboardCmd(text)
}

// copyClipboardCmd 通过 OSC 52 写入系统剪贴板；失败时回退到文件，保证内容不丢。
func copyClipboardCmd(s string) tea.Cmd {
	return func() tea.Msg {
		seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\x1b\\"
		if _, err := os.Stdout.WriteString(seq); err == nil {
			return clipboardMsg{ok: true}
		}
		dir := ""
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, store.DirName)
		} else {
			dir = store.DirName
		}
		_ = os.MkdirAll(dir, 0o750)
		p := filepath.Join(dir, "clipboard.txt")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			return clipboardMsg{err: err.Error()}
		}
		return clipboardMsg{path: p}
	}
}

// pasteLastCopy 把最近一次复制的内容发往远端 stdin。
//
// OSC 52 只能写不能读，读系统剪贴板需要平台原生 API，这里退而求其次：
// 粘贴「本次会话里复制过的内容」，覆盖最常用的「复制命令 → 粘贴执行」场景。
func (m *Model) pasteLastCopy() {
	if m.lastCopy == "" {
		m.setMsg("还没有复制过内容（OSC 52 只能写剪贴板，读不到系统剪贴板）")
		return
	}
	s := m.activeSession()
	if s == nil {
		return
	}
	text := m.lastCopy
	if !strings.HasSuffix(text, "\n") {
		text += "\r"
	}
	_ = s.Write([]byte(text))
}

// ---------- 鼠标拖选 ----------

// startMouseSelect 在终端区域按下左键：开始拖选。
func (m *Model) startMouseSelect(col, row int) {
	if m.activeSession() == nil {
		return
	}
	m.searchMode = false
	m.inputMode = false
	m.selMode = true
	m.selOn = true
	m.selDrag = true
	m.selAX, m.selAY = col, row
	m.selCX, m.selCY = col, row
	m.pushSelection()
}

// updateMouseSelect 拖动中更新选区终点。
func (m *Model) updateMouseSelect(col, row int) {
	if !m.selDrag {
		return
	}
	m.selCX, m.selCY = col, row
	m.pushSelection()
}

// finishMouseSelect 松开左键：拖出过区域就复制，纯单击则只是切焦点。
func (m *Model) finishMouseSelect() tea.Cmd {
	if !m.selDrag {
		return nil
	}
	m.selDrag = false
	if m.selAX == m.selCX && m.selAY == m.selCY {
		// 没拖动 = 普通点击，不该弹「选区是空的」
		return m.exitSelectMode(false)
	}
	return m.exitSelectMode(true)
}

// ---------- 复位 ----------

// resetTermModes 复位终端侧的临时状态（选择 / 搜索高亮）。
// 切换会话、窗口尺寸变化、会话关闭时必须调用，否则坐标会失配。
func (m *Model) resetTermModes() {
	m.selMode = false
	m.selOn = false
	m.selDrag = false
	m.hits = nil
	m.hitIdx = -1
	if s := m.activeSession(); s != nil {
		s.Term().ClearSelection()
		s.Term().SetHighlight(-1)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
