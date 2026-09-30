package vt

import (
	"fmt"
	"strings"
	"testing"
)

// feedLines 写入 n 行可区分文本，便于判断视口停在哪一段。
func feedLines(t *Terminal, n int) {
	for i := 1; i <= n; i++ {
		line := fmt.Sprintf("L%03d:%s\n", i, strings.Repeat(".", 10))
		_, _ = t.Write([]byte(line))
	}
}

// TestClickAtBottomDoesNotJumpToTop 复现「在底部点击终端 → 视口是否跳到顶部」。
//
// onMouse 点击输出区时调 startMouseSelect → pushSelection → SetSelection，
// 传入的是视口坐标（左上角 0,0）。我们用 SetSelection(0,0, 5,0) 模拟「点在视口顶行」。
// 期望：冻结后视口仍停在最底部（显示最新行），ScrollOffset 仍为 0。
func TestClickAtBottomDoesNotJumpToTop(t *testing.T) {
	tm := New(80, 24, 2000)
	feedLines(tm, 100) // 100 行输出，rows=24 → 76 行进入回滚缓冲

	if !tm.AtBottom() {
		t.Fatalf("前置条件失败：喂完数据后未处于底部 (ScrollOffset=%d)", tm.ScrollOffset())
	}

	// 模拟点击：视口坐标 (col=0,row=0) ~ (col=5,row=0)
	tm.SetSelection(0, 0, 5, 0)

	// 冻结后必须仍停在底部
	if off := tm.ScrollOffset(); off != 0 {
		t.Fatalf("点击后视口偏移=%d，期望 0（仍在底部）。说明点击把视口顶到了历史里", off)
	}

	lines := tm.Render()
	if len(lines) == 0 {
		t.Fatal("Render 返回空")
	}
	joined := strings.Join(lines, "\n")
	// 在底部：可见区应是最新内容（含高行号 L100），绝不能是最旧的 L001。
	if strings.Contains(joined, "L001") {
		t.Fatalf("点击后视口居然显示了最旧行 L001，说明视口被钉到了顶部：\n%s", joined)
	}
	if !strings.Contains(joined, "L100") {
		t.Fatalf("点击后视口未显示最新行 L100，视口位置不对：\n%s", joined)
	}
	t.Logf("点击后视口含最新行 L100 且不含 L001，确认停在底部")
}

// TestClickWhileScrolledHoldsPosition 复现「滚到历史里再点击 → 是否跳顶」。
func TestClickWhileScrolledHoldsPosition(t *testing.T) {
	tm := New(80, 24, 2000)
	feedLines(tm, 100)

	// 向上回滚 30 行（看历史中段）
	tm.ScrollBy(-30)
	if tm.AtBottom() {
		t.Fatal("前置条件失败：ScrollBy(-30) 后应不在底部")
	}
	before := tm.ScrollOffset() // 期望 30

	// 在视口顶行点击
	tm.SetSelection(0, 0, 5, 0)

	after := tm.ScrollOffset()
	if after != before {
		t.Fatalf("点击后 ScrollOffset %d != 点击前 %d，说明点击改动了视口位置", after, before)
	}
	t.Logf("滚动后点击：ScrollOffset 保持 %d（位置未跳）", after)
}
