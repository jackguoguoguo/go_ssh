package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// 本文件只放「回滚缓冲搜索」的状态与纯逻辑；
// 按键分发在 keys.go，渲染在 panels.go。

// openSearch 进入搜索输入状态（Alt+/）。
func (m *Model) openSearch() tea.Cmd {
	if m.activeSession() == nil {
		m.setMsg("没有可搜索的会话，先连接一台服务器")
		return nil
	}
	m.selMode = false
	m.inputMode = false
	m.searchMode = true
	m.setInput(m.searchQuery) // 保留上次关键字，便于继续查找
	m.focus = focusTerm
	return nil
}

// clearHits 清除命中与高亮（关键字本身保留）。
func (m *Model) clearHits() {
	m.hits = nil
	m.hitIdx = -1
	if s := m.activeSession(); s != nil {
		s.Term().SetHighlight(-1)
	}
}

// runSearch 执行搜索：默认定位到最后一条命中（离当前输出最近的一条）。
func (m *Model) runSearch() tea.Cmd {
	q := strings.TrimSpace(string(m.input))
	m.searchMode = false
	m.input = nil
	m.inputPos = 0

	s := m.activeSession()
	if s == nil || q == "" {
		m.clearHits()
		return nil
	}
	m.searchQuery = q
	m.hits = s.Term().Find(q, true)
	if len(m.hits) == 0 {
		m.clearHits()
		m.setMsg("没有找到：" + q)
		return nil
	}
	m.hitIdx = len(m.hits) - 1
	return m.gotoHit(0)
}

// gotoHit 跳到第 delta 条命中（相对当前），支持环绕。
func (m *Model) gotoHit(delta int) tea.Cmd {
	s := m.activeSession()
	if s == nil {
		return nil
	}
	if len(m.hits) == 0 {
		m.setMsg("还没有搜索（Alt+/ 输入关键字）")
		return nil
	}
	term := s.Term()

	idx := m.hitIdx + delta
	if idx < 0 {
		idx = len(m.hits) - 1
	}
	if idx >= len(m.hits) {
		idx = 0
	}

	// 环形缓冲写满后行号会整体左移，命中记录可能已经失效。
	// 用命中时保存的行文本校验，失效就重新搜索并回到最接近的位置。
	h := m.hits[idx]
	if term.LineText(h.Line) != h.Text {
		m.hits = term.Find(m.searchQuery, true)
		if len(m.hits) == 0 {
			m.clearHits()
			m.setMsg("终端内容已变化，之前的命中失效，请重新搜索")
			return nil
		}
		idx = 0
		for i, x := range m.hits {
			if x.Line >= h.Line {
				idx = i
				break
			}
		}
	}

	m.hitIdx = idx
	term.ScrollToLine(m.hits[idx].Line)
	term.SetHighlight(m.hits[idx].Line)
	return nil
}
