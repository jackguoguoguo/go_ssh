package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// TestWidthEnginesAgree 钉死「两套宽度引擎口径一致」这个不变式。
//
// 本进程内存在两套互不相干的宽度实现：lipgloss 走 charmbracelet/x/ansi，歧义字符
// （← → · ● … 等）恒算 1 列；theme.go 的 ansiWidth / cutPlain / ansiCut 走 go-runewidth，
// 后者在 Windows 上会被 GetConsoleOutputCP() 翻成 2 列。只要有差值，排版宽度与实际
// 补齐宽度就对不上，面板边框错位。详见 theme.go 里的 init。
func TestWidthEnginesAgree(t *testing.T) {
	if runewidth.DefaultCondition.EastAsianWidth {
		t.Fatal("EastAsianWidth 应为 false：置为 true 会与 lipgloss 的窄口径冲突")
	}

	cases := []string{
		"  Alt+← / Alt+→   上一个 / 下一个会话",
		"  ↑↓ 移动 · 空格选中/取消 · a 全选 · Enter 确定 · Esc 返回",
		"  Tab 焦点 · Enter 执行 · / 过滤 · Ctrl+X 命令行 · Ctrl+G 密钥",
		"回滚 128 行 · End 返回最新 ",
	}
	for _, s := range cases {
		want := lipgloss.Width(s)
		if got := runewidth.StringWidth(s); got != want {
			t.Errorf("go-runewidth 与 lipgloss 不一致: %d != %d (%q)", got, want, s)
		}
		if got := ansiWidth(s); got != want {
			t.Errorf("ansiWidth 与 lipgloss 不一致: %d != %d (%q)", got, want, s)
		}
	}
}

// TestCutPlainHonoursWidth cutPlain 的结果必须真的放得进给定宽度，否则行会溢出。
func TestCutPlainHonoursWidth(t *testing.T) {
	const s = "  Alt+← / Alt+→   上一个 / 下一个会话"
	for w := 1; w <= 40; w++ {
		got := cutPlain(s, w)
		if cw := lipgloss.Width(got); cw > w {
			t.Errorf("cutPlain(%q, %d) = %q，宽度 %d 超过 %d", s, w, got, cw, w)
		}
	}
}
