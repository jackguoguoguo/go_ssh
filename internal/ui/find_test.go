package ui

import (
	"strings"
	"testing"
)

// TestSearchFindsAndNavigates 验证搜索定位到最后一条命中，并支持环绕跳转。
func TestSearchFindsAndNavigates(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("alpha\r\nbeta\r\nalpha again\r\n"))

	m.setInput("alpha")
	m.runSearch()
	if len(m.hits) != 2 {
		t.Fatalf("命中数 = %d，期望 2", len(m.hits))
	}
	if m.hitIdx != len(m.hits)-1 {
		t.Fatalf("默认应定位到最后一条命中，实际 %d", m.hitIdx)
	}
	// 向后跳转应环绕到第一条
	m.gotoHit(1)
	if m.hitIdx != 0 {
		t.Fatalf("环绕跳转后应回到第 0 条，实际 %d", m.hitIdx)
	}
	// 再向后回到最后一条
	m.gotoHit(1)
	if m.hitIdx != len(m.hits)-1 {
		t.Fatalf("再跳转应回到最后一条，实际 %d", m.hitIdx)
	}
	// 向前跳转应环绕到最后一条
	m.gotoHit(-1)
	if m.hitIdx != 0 {
		t.Fatalf("向前环绕应回到第 0 条，实际 %d", m.hitIdx)
	}
}

// TestSearchStatusShowsCount 验证状态栏展示「命中 x/y」提示。
func TestSearchStatusShowsCount(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("alpha\r\nbeta\r\nalpha again\r\n"))

	m.setInput("alpha")
	m.runSearch()
	m.gotoHit(1) // 回到第 0 条，状态应显示 1/2

	l := m.computeLayout()
	status := m.renderStatus(l)
	if !strings.Contains(status, "命中 1/2") {
		t.Errorf("状态栏应显示命中计数，实际: %q", status)
	}
}

// TestSearchStaleHitReruns 验证缓冲变化导致命中失效时，跳转会重新搜索而非崩溃。
func TestSearchStaleHitReruns(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("alpha\r\nbeta\r\nalpha again\r\n"))

	m.setInput("alpha")
	m.runSearch()
	// 灌入大量新输出，让旧命中行被移出或覆盖
	for i := 0; i < 30; i++ {
		s.Term().Write([]byte("fresh content line\r\n"))
	}
	// 旧的命中行文本已不存在，gotoHit 应自动重搜，不 panic
	m.gotoHit(1)
	// 重搜后要么命中 0 条被清空，要么命中新内容；无论哪种都不应保留失效行
	for _, h := range m.hits {
		if s.Term().LineText(h.Line) != h.Text {
			t.Errorf("gotoHit 后不应保留失效命中: line=%d text=%q", h.Line, h.Text)
		}
	}
}

// TestSearchEmptyQueryClears 验证空关键字清空命中。
func TestSearchEmptyQueryClears(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("alpha\r\nbeta\r\n"))

	m.setInput("beta")
	m.runSearch()
	if len(m.hits) != 1 {
		t.Fatalf("搜索前应有 1 条命中，实际 %d", len(m.hits))
	}
	m.setInput("")
	m.runSearch()
	if len(m.hits) != 0 || m.hitIdx != -1 {
		t.Error("空关键字应清空命中")
	}
	if s.Term().SelectionActive() {
		t.Error("这里没进入选择模式，不应有选区；签证里 SelectionActive 仅用于搜索高亮清理")
	}
}
