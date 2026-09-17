package vt

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// feed 向终端写入若干行（CRLF 分隔）。
func feed(t *testing.T, term *Terminal, lines ...string) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\r\n")
	}
	if _, err := term.Write([]byte(b.String())); err != nil {
		t.Fatalf("写入终端失败: %v", err)
	}
}

func TestLineTextAndBufferLen(t *testing.T) {
	term := New(20, 5, 10)
	feed(t, term, "aaa", "bbb", "ccc")

	if got := term.BufferLen(); got != 5 {
		t.Fatalf("BufferLen = %d，期望 5（3 行内容 + 2 行空屏）", got)
	}
	if got := term.LineText(0); got != "aaa" {
		t.Errorf("LineText(0) = %q，期望 aaa", got)
	}
	if got := term.LineText(2); got != "ccc" {
		t.Errorf("LineText(2) = %q，期望 ccc", got)
	}
	// 越界应返回空串而不是 panic
	if got := term.LineText(999); got != "" {
		t.Errorf("越界 LineText = %q，期望空串", got)
	}
}

func TestTextRectAndWideChars(t *testing.T) {
	term := New(20, 5, 10)
	feed(t, term, "hello world", "中文abc")

	if got := term.Text(0, 0, 4, 0); got != "hello" {
		t.Errorf("Text(0,0,4,0) = %q，期望 hello", got)
	}
	if got := term.Text(6, 0, 10, 0); got != "world" {
		t.Errorf("Text(6,0,10,0) = %q，期望 world", got)
	}
	// 跨行：行之间用 \n 连接
	if got := term.Text(0, 0, 10, 1); got != "hello world\n中文abc" {
		t.Errorf("跨行取文本 = %q", got)
	}
	// 宽字符按 2 列计：取第 0~1 列（中的两列）应得到「中」
	if got := term.Text(0, 1, 1, 1); got != "中" {
		t.Errorf("宽字符取文本 = %q，期望 中", got)
	}
	// 起止点反序应自动规范化
	if got := term.Text(4, 0, 0, 0); got != "hello" {
		t.Errorf("反序坐标应规范化，实际 %q", got)
	}
	// 越界裁剪：不应 panic，且只取到存在的部分
	if got := term.Text(0, 4, 100, 9); got != "" {
		t.Errorf("越界行应返回空串，实际 %q", got)
	}
}

func TestFindAcrossScrollback(t *testing.T) {
	term := New(20, 5, 20)
	feed(t, term, "alpha", "beta", "gamma", "alpha again", "delta", "epsilon", "zeta", "alpha end")

	hits := term.Find("alpha", false)
	if len(hits) != 3 {
		t.Fatalf("命中数 = %d，期望 3（含回滚缓冲中的行）", len(hits))
	}
	// 命中必须按缓冲区行号升序，且带上命中时的行文本
	if hits[0].Line >= hits[1].Line || hits[1].Line >= hits[2].Line {
		t.Errorf("命中未按行号升序: %+v", hits)
	}
	if hits[0].Text != "alpha" || hits[2].Text != "alpha end" {
		t.Errorf("命中行文本不对: %+v", hits)
	}
	// 行号应能反查出同样的文本
	for _, h := range hits {
		if got := term.LineText(h.Line); got != h.Text {
			t.Errorf("行号 %d 的文本 = %q，命中记录为 %q", h.Line, got, h.Text)
		}
	}

	if term.Find("", false) != nil {
		t.Error("空关键字不应产生命中")
	}
	if len(term.Find("ALPHA", false)) != 0 {
		t.Error("默认应区分大小写")
	}
	if len(term.Find("ALPHA", true)) != 3 {
		t.Error("ignoreCase=true 时应命中 3 处")
	}
}

func TestScrollToLine(t *testing.T) {
	term := New(10, 5, 50)
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "line"+string(rune('A'+i%26))+itoaForVT(i))
	}
	feed(t, term, lines...)

	// 滚到第 3 行：它应出现在视口内，且不再跟随底部
	if ok := term.ScrollToLine(3); !ok {
		t.Fatal("ScrollToLine(3) 应返回 true")
	}
	if term.ScrollOffset() == 0 {
		t.Error("滚动后不应仍处于底部")
	}
	joined := strings.Join(term.Render(), "\n")
	if !strings.Contains(joined, term.LineText(3)) {
		t.Errorf("目标行未进入视口，视口内容:\n%s", joined)
	}
	if term.ScrollToLine(-1) {
		t.Error("越界行号应返回 false")
	}
}

func TestHighlightRendersReverseVideo(t *testing.T) {
	term := New(10, 3, 10)
	// 只写两行（终端 3 行），保证不发生滚动：缓冲区行号 == 屏幕行号
	feed(t, term, "one", "two")

	term.SetHighlight(1)
	rows := term.Render()
	// 命中行应带反显(7)与加粗(1)，且宽度不受影响
	if !strings.Contains(rows[1], ";1") || !strings.Contains(rows[1], ";7") {
		t.Errorf("命中行应反显加粗，实际: %q", rows[1])
	}
	if strings.Contains(rows[0], ";7") {
		t.Errorf("非命中行不应反显: %q", rows[0])
	}
	for i, r := range rows {
		if w := ansiWidthForTest(r); w != 10 {
			t.Errorf("第 %d 行宽度 = %d，期望 10（高亮不应改变宽度）", i, w)
		}
	}

	term.SetHighlight(-1)
	if strings.Contains(term.Render()[1], ";7") {
		t.Error("SetHighlight(-1) 应取消高亮")
	}
}

// ansiWidthForTest 计算去掉 SGR 后的显示宽度。
func ansiWidthForTest(s string) int {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if j := strings.IndexByte(s[i:], 'm'); j >= 0 {
				i += j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return runewidth.StringWidth(b.String())
}

func itoaForVT(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
