package ui

import (
	"strings"
	"testing"
	"time"
)

// TestSelectModeKeyboard 验证键盘选择：进入 → 定起点 → 移动 → 复制。
func TestSelectModeKeyboard(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("hello world\r\nsecond line\r\n"))

	m.toggleSelectMode()
	if !m.selMode {
		t.Fatal("Alt+S 后应在选择模式")
	}
	// 手动把选区定在首行，避免依赖光标位置的隐式约定
	m.selCX, m.selCY, m.selAX, m.selAY = 0, 0, 0, 0
	m.toggleSelectAnchor()
	if !m.selOn {
		t.Fatal("Space 后应已按下选区起点")
	}
	m.selectMove(4, 0) // 向右 5 列 → 覆盖 "hello"
	if m.selCX != 4 {
		t.Fatalf("选择光标 X 应移动到 4，实际 %d", m.selCX)
	}
	_ = m.copySelection()
	if m.lastCopy != "hello" {
		t.Fatalf("复制结果 = %q，期望 hello", m.lastCopy)
	}
	// 退出选择模式应解除 vt 侧选区（解冻）
	_ = m.exitSelectMode(false)
	if s.Term().SelectionActive() {
		t.Error("退出选择模式后 vt 选区应被清除（视口恢复跟随）")
	}
}

// TestSelectionFreezeUnderNewOutput 验证：远端持续输出时，已选中的内容不会被顶走。
func TestSelectionFreezeUnderNewOutput(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("hello world\r\n"))

	m.selMode = true
	m.selCX, m.selCY, m.selAX, m.selAY = 0, 0, 0, 0
	m.toggleSelectAnchor() // selOn=true → 冻结视口

	// 灌入足够多行，让首行被推入回滚缓冲
	for i := 0; i < 9; i++ {
		s.Term().Write([]byte("padding-line\r\n"))
	}
	if got := s.Term().Text(0, 0, 4, 0); got != "hello" {
		t.Fatalf("持续输出后选区内容应仍为 hello，实际 %q", got)
	}
}

// TestMouseSelectCopiesOnRelease 验证鼠标「按下-拖动-松开」自动复制。
func TestMouseSelectCopiesOnRelease(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("hello world\r\n"))

	m.startMouseSelect(0, 0)
	m.updateMouseSelect(4, 0)
	cmd := m.finishMouseSelect()
	if cmd == nil {
		t.Fatal("拖选松开后应产生复制命令")
	}
	if m.lastCopy != "hello" {
		t.Fatalf("鼠标拖选复制结果 = %q，期望 hello", m.lastCopy)
	}
	if m.selMode {
		t.Error("松开后应选择模式退出")
	}
}

// TestMouseClickDoesNotCopy 验证纯单击（无拖动）只切焦点、不弹「选区是空的」。
func TestMouseClickDoesNotCopy(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("hello world\r\n"))

	m.startMouseSelect(0, 0)
	cmd := m.finishMouseSelect() // 未按住拖动
	if cmd != nil {
		t.Error("纯单击不应触发复制命令")
	}
	if m.selMode {
		t.Error("纯单击不应停留在选择模式")
	}
}

// TestSwitchResetsSelection 验证切会话会复位选择状态（坐标会失配）。
func TestSwitchResetsSelection(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("hello world\r\n"))

	m.toggleSelectMode()
	m.startMouseSelect(0, 0)
	m.updateMouseSelect(2, 0)
	m.switchTo(0)
	if m.selMode || m.selOn {
		t.Error("切会话后选择状态应被复位")
	}
}

// TestResizeResetsSelection 验证窗口尺寸变化会复位选择状态。
func TestResizeResetsSelection(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("hello world\r\n"))

	m.startMouseSelect(0, 0)
	m.updateMouseSelect(2, 0)
	m.width, m.height = 100, 40
	m.applyTermSize()
	if m.selMode || m.selOn {
		t.Error("窗口尺寸变化后选择状态应被复位")
	}
}

// TestSelectionCursorRendered 验证选择模式下会在光标处高亮一格。
func TestSelectionCursorRendered(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)
	s.Term().Write([]byte("hello world\r\nsecond line\r\n"))

	m.selMode = true
	m.selCX, m.selCY = 2, 0 // 落在 "l" 上
	l := m.computeLayout()
	if !strings.Contains(m.renderTerminal(l), "\x1b[7m") {
		t.Errorf("选择光标所在单元格应反显（含 \\x1b[7m）")
	}
}

// TestPasteUsesLastCopy 验证粘贴走内存里最近一次复制内容。
func TestPasteUsesLastCopy(t *testing.T) {
	m, st, _ := newTestModel(t)
	s := connectTestSession(t, m, st)

	m.lastCopy = "uptime"
	m.pasteLastCopy()
	waitTermText(t, s, "echo:uptime", 3*time.Second)
}
