package vt

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// stripANSI 去掉字符串中的 ANSI 转义序列，用于宽度与内容断言。
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
		case ']':
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					j++
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		default:
			i += 2
		}
	}
	return b.String()
}

// text 返回去掉转义并裁掉右侧填充空格的行内容。
func text(s string) string { return strings.TrimRight(stripANSI(s), " ") }

// screen 返回当前屏幕（去掉转义、裁掉右侧空格）。
func screen(term *Terminal) []string {
	lines := term.Render()
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = text(l)
	}
	return out
}

func checkScreen(t *testing.T, term *Terminal, want ...string) {
	t.Helper()
	got := screen(term)
	if len(got) != len(want) {
		t.Fatalf("行数 = %d, 期望 %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 行 = %q, 期望 %q", i, got[i], want[i])
		}
	}
}

// checkWidth 校验每一行渲染后的可打印宽度都等于 Cols()。
func checkWidth(t *testing.T, term *Terminal) {
	t.Helper()
	for i, l := range term.Render() {
		if w := runewidth.StringWidth(stripANSI(l)); w != term.Cols() {
			t.Errorf("第 %d 行宽度 = %d, 期望 %d (%q)", i, w, term.Cols(), l)
		}
	}
}

func TestNewClamps(t *testing.T) {
	term := New(0, -3, -1)
	if term.Cols() != 1 || term.Rows() != 1 {
		t.Fatalf("cols/rows = %d/%d, 期望 1/1", term.Cols(), term.Rows())
	}
	if got := len(term.Render()); got != 1 {
		t.Fatalf("Render 行数 = %d", got)
	}
}

func TestWriteAndNewline(t *testing.T) {
	term := New(10, 3, 10)
	in := []byte("hello\r\nworld")
	n, err := term.Write(in)
	if n != len(in) || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	checkScreen(t, term, "hello", "world", "")
	checkWidth(t, term)
}

func TestCarriageReturn(t *testing.T) {
	term := New(10, 1, 0)
	term.Write([]byte("abcdef\rXY"))
	checkScreen(t, term, "XYcdef")
}

func TestTabAndBackspace(t *testing.T) {
	term := New(20, 1, 0)
	term.Write([]byte("a\tb"))
	checkScreen(t, term, "a       b")
	checkWidth(t, term)

	term = New(10, 1, 0)
	term.Write([]byte("abc\b\bX"))
	checkScreen(t, term, "aXc")
}

func TestBELAndOtherControlsIgnored(t *testing.T) {
	term := New(10, 1, 0)
	term.Write([]byte("a\x07b\x00c\x0ed"))
	checkScreen(t, term, "abcd")
}

func TestCursorPositionAndErase(t *testing.T) {
	term := New(10, 3, 0)
	term.Write([]byte("aaa\r\nbbb\r\nccc"))
	term.Write([]byte("\x1b[2;2H")) // 光标到第 2 行第 2 列
	term.Write([]byte("\x1b[K"))    // 清光标到行尾
	term.Write([]byte("\x1b[0J"))   // 清光标到屏尾
	checkScreen(t, term, "aaa", "b", "")
	checkWidth(t, term)

	term = New(5, 2, 0)
	term.Write([]byte("hello\r\nworld"))
	term.Write([]byte("\x1b[2J"))
	checkScreen(t, term, "", "")

	term = New(5, 1, 0)
	term.Write([]byte("abcde"))
	term.Write([]byte("\x1b[1;3H"))
	term.Write([]byte("\x1b[1K")) // 清行首到光标（含光标格，共 3 格）
	checkScreen(t, term, "   de")

	term = New(5, 2, 0)
	term.Write([]byte("abc"))
	term.Write([]byte("\x1b[9d")) // VPA：行号越界应被 clamp 到最后一行
	term.Write([]byte("\x1b[4`")) // CHA：到第 4 列
	term.Write([]byte("Z"))
	checkScreen(t, term, "abc", "   Z")
}

func TestED3ClearsScrollback(t *testing.T) {
	term := New(5, 2, 10)
	term.Write([]byte("1\r\n2\r\n3\r\n4\r\n5"))
	if term.ScrollbackLen() != 3 {
		t.Fatalf("ScrollbackLen = %d, 期望 3", term.ScrollbackLen())
	}
	term.Write([]byte("\x1b[3J"))
	if term.ScrollbackLen() != 0 {
		t.Fatalf("清屏后 ScrollbackLen = %d", term.ScrollbackLen())
	}
	checkScreen(t, term, "", "")
}

func TestSGRBasic(t *testing.T) {
	term := New(10, 1, 0)
	term.Write([]byte("\x1b[1;33;44mA"))
	if term.attr.FG != 3 || term.attr.BG != 4 || !term.attr.Bold {
		t.Fatalf("attr = %+v, 期望 FG=3 BG=4 Bold", term.attr)
	}
	line := term.Render()[0]
	if !strings.HasPrefix(line, "\x1b[0m\x1b[0;1;33;44mA") {
		t.Fatalf("渲染结果 = %q", line)
	}
	if !strings.HasSuffix(line, "\x1b[0m") {
		t.Fatalf("行尾未重置: %q", line)
	}
	term.Write([]byte("\x1b[0mB"))
	if term.attr != DefaultAttr {
		t.Fatalf("复位后 attr = %+v", term.attr)
	}
}

func TestSGROffSwitches(t *testing.T) {
	term := New(5, 1, 0)
	term.Write([]byte("\x1b[1;2;3;4;5;7;8m"))
	term.Write([]byte("\x1b[22;23;24;25;27;28m"))
	if term.attr != DefaultAttr {
		t.Fatalf("全部关闭后 attr = %+v, 期望默认属性", term.attr)
	}
}

func TestSGRColors(t *testing.T) {
	term := New(5, 1, 0)
	term.Write([]byte("\x1b[38;5;196m"))
	if term.attr.FG != 196 {
		t.Fatalf("256 色 FG = %d, 期望 196", term.attr.FG)
	}
	term.Write([]byte("\x1b[48;2;10;20;30m"))
	if term.attr.BG != RGBColor(10, 20, 30) {
		t.Fatalf("TrueColor BG = %#x, 期望 %#x", term.attr.BG, RGBColor(10, 20, 30))
	}
	term.Write([]byte("\x1b[39;49m"))
	if term.attr.FG != DefaultColor || term.attr.BG != DefaultColor {
		t.Fatalf("39/49 后 FG=%d BG=%d", term.attr.FG, term.attr.BG)
	}
	term.Write([]byte("\x1b[91;105m"))
	if term.attr.FG != 9 || term.attr.BG != 13 {
		t.Fatalf("亮色 FG=%d BG=%d, 期望 9/13", term.attr.FG, term.attr.BG)
	}
	term.Write([]byte("\x1b[38:2:1:2:3m")) // 冒号分隔的 TrueColor
	if term.attr.FG != RGBColor(1, 2, 3) {
		t.Fatalf("冒号形式 TrueColor FG = %#x", term.attr.FG)
	}
}

func TestAttrSGROutput(t *testing.T) {
	cases := []struct {
		attr Attr
		want string
	}{
		{DefaultAttr, "\x1b[0m"},
		{Attr{FG: DefaultColor, BG: DefaultColor, Bold: true}, "\x1b[0;1m"},
		{Attr{FG: 1, BG: DefaultColor}, "\x1b[0;31m"},
		{Attr{FG: DefaultColor, BG: 4}, "\x1b[0;44m"},
		{Attr{FG: 196, BG: DefaultColor}, "\x1b[0;38;5;196m"},
		{Attr{FG: DefaultColor, BG: RGBColor(1, 2, 3)}, "\x1b[0;48;2;1;2;3m"},
		{Attr{FG: 9, BG: DefaultColor, Bold: true, Underline: true}, "\x1b[0;1;4;91m"},
	}
	for _, c := range cases {
		if got := c.attr.SGR(); got != c.want {
			t.Errorf("SGR() = %q, 期望 %q", got, c.want)
		}
	}
}

func TestHiddenRendersAsSpace(t *testing.T) {
	term := New(6, 1, 0)
	term.Write([]byte("\x1b[8mABC"))
	checkScreen(t, term, "")
	checkWidth(t, term)
}

func TestDECSTBMAndReverseIndex(t *testing.T) {
	term := New(4, 5, 0)
	term.Write([]byte("1\r\n2\r\n3\r\n4\r\n5"))
	term.Write([]byte("\x1b[2;4r")) // 滚动区 2..4
	term.Write([]byte("\x1b[4;1H"))
	term.Write([]byte("\x1b[1S")) // 区内上滚 1 行
	checkScreen(t, term, "1", "3", "4", "", "5")

	term = New(4, 5, 0)
	term.Write([]byte("1\r\n2\r\n3\r\n4\r\n5"))
	term.Write([]byte("\x1b[2;4r"))
	term.Write([]byte("\x1b[2;1H"))
	term.Write([]byte("\x1bM")) // RI：滚动区顶部反向换行
	checkScreen(t, term, "1", "", "2", "3", "5")
}

func TestScrollDownAndInsertDeleteLines(t *testing.T) {
	term := New(4, 3, 0)
	term.Write([]byte("1\r\n2\r\n3"))
	term.Write([]byte("\x1b[2T")) // 整屏下滚 2 行
	checkScreen(t, term, "", "", "1")

	term = New(4, 3, 0)
	term.Write([]byte("1\r\n2\r\n3"))
	term.Write([]byte("\x1b[1;1H"))
	term.Write([]byte("\x1b[1L")) // 插入一行
	checkScreen(t, term, "", "1", "2")
	term.Write([]byte("\x1b[2;1H"))
	term.Write([]byte("\x1b[1M")) // 删除一行
	checkScreen(t, term, "", "2", "")
}

func TestInsertDeleteEraseChars(t *testing.T) {
	term := New(5, 1, 0)
	term.Write([]byte("abcde\x1b[1;3H\x1b[2P")) // DCH
	checkScreen(t, term, "abe")

	term = New(5, 1, 0)
	term.Write([]byte("abcde\x1b[1;3H\x1b[2@")) // ICH：右侧溢出的字符被丢弃
	checkScreen(t, term, "ab  c")

	term = New(5, 1, 0)
	term.Write([]byte("abcde\x1b[1;3H\x1b[2X")) // ECH
	checkScreen(t, term, "ab  e")
}

func TestSplitEscapeSequence(t *testing.T) {
	term := New(10, 2, 0)
	for _, part := range []string{"\x1b[", "1;33", ";44m", "A"} {
		if _, err := term.Write([]byte(part)); err != nil {
			t.Fatalf("Write(%q) 出错: %v", part, err)
		}
	}
	if term.attr.FG != 3 || term.attr.BG != 4 || !term.attr.Bold {
		t.Fatalf("拼接后 attr = %+v", term.attr)
	}
	if !strings.HasPrefix(term.Render()[0], "\x1b[0m\x1b[0;1;33;44mA") {
		t.Fatalf("渲染结果 = %q", term.Render()[0])
	}

	// 未完成的序列不得输出可见字符
	term2 := New(10, 1, 0)
	term2.Write([]byte("\x1b["))
	checkScreen(t, term2, "")
	term2.Write([]byte("\x1b"))
	checkScreen(t, term2, "")
	term2.Write([]byte("]0;half"))
	checkScreen(t, term2, "")
	term2.Write([]byte(" title\x07ok"))
	checkScreen(t, term2, "ok")
	if term2.Title() != "half title" {
		t.Fatalf("title = %q", term2.Title())
	}
}

func TestSplitUTF8(t *testing.T) {
	term := New(10, 1, 0)
	b := []byte("a你b\x1b[31mc")
	for i := range b { // 逐字节写入，覆盖被切分的 UTF-8
		if _, err := term.Write(b[i : i+1]); err != nil {
			t.Fatalf("Write 出错: %v", err)
		}
	}
	checkScreen(t, term, "a你bc")
	for _, l := range term.Render() {
		if strings.ContainsRune(l, utf8.RuneError) {
			t.Fatalf("输出含 U+FFFD: %q", l)
		}
	}
	checkWidth(t, term)
}

func TestTruncatedUTF8NotGarbled(t *testing.T) {
	term := New(10, 1, 0)
	b := []byte("你好")
	term.Write(b[:2]) // 只写前 2 字节
	checkScreen(t, term, "")
	term.Write(b[2:])
	checkScreen(t, term, "你好")
}

func TestWideChars(t *testing.T) {
	term := New(6, 1, 0)
	term.Write([]byte("你好"))
	if term.screen[0].R != '你' || term.screen[0].W != 2 {
		t.Fatalf("cell[0] = %+v", term.screen[0])
	}
	if term.screen[1].R != 0 || term.screen[1].W != 0 {
		t.Fatalf("占位格 cell[1] = %+v", term.screen[1])
	}
	if got := stripANSI(term.Render()[0]); got != "你好  " {
		t.Fatalf("渲染 = %q", got)
	}
	checkWidth(t, term)

	// 覆盖宽字符左半：右半占位格要清成空格
	term.Write([]byte("\x1b[1;1HX"))
	if term.screen[1].R != ' ' || term.screen[1].W != 1 {
		t.Fatalf("覆盖左半后占位格 = %+v", term.screen[1])
	}

	// 覆盖宽字符右半：左半要清成空格
	term = New(6, 1, 0)
	term.Write([]byte("你好\x1b[1;2HY"))
	if term.screen[0].R != ' ' {
		t.Fatalf("覆盖右半后左半 = %+v", term.screen[0])
	}
	checkScreen(t, term, " Y好")
	checkWidth(t, term)
}

func TestWideCharWrap(t *testing.T) {
	term := New(4, 2, 0)
	term.Write([]byte("ab你c"))
	checkScreen(t, term, "ab你", "c")
	checkWidth(t, term)

	// 只剩 1 格时先换行再写
	term = New(3, 2, 0)
	term.Write([]byte("ab你"))
	checkScreen(t, term, "ab", "你")
	checkWidth(t, term)
}

func TestAutowrapOff(t *testing.T) {
	term := New(3, 2, 0)
	term.Write([]byte("\x1b[?7l"))
	term.Write([]byte("abcd"))
	// 关闭自动换行后超出的字符覆盖最后一格
	checkScreen(t, term, "abd", "")
}

func TestResize(t *testing.T) {
	term := New(20, 5, 10)
	term.Write([]byte("hello\r\nworld"))
	term.Resize(10, 3)
	if term.Cols() != 10 || term.Rows() != 3 {
		t.Fatalf("resize 后 = %dx%d", term.Cols(), term.Rows())
	}
	if got := len(term.Render()); got != 3 {
		t.Fatalf("Render 行数 = %d", got)
	}
	checkScreen(t, term, "hello", "world", "")
	checkWidth(t, term)

	// 行数减少：被挤出屏幕的行按顺序进入 scrollback
	term = New(5, 4, 10)
	term.Write([]byte("1\r\n2\r\n3\r\n4"))
	term.Resize(5, 2)
	checkScreen(t, term, "1", "2")
	if term.ScrollbackLen() != 2 {
		t.Fatalf("ScrollbackLen = %d, 期望 2", term.ScrollbackLen())
	}
	checkWidth(t, term)
}

func TestRenderWidth(t *testing.T) {
	term := New(15, 4, 5)
	term.Write([]byte("abc\r\n"))
	term.Write([]byte("\x1b[31mdefghijklmnopqrstuvwxyz\r\n"))
	term.Write([]byte("\x1b[0m中文测试\r\n"))
	term.Write([]byte("\x1b[10;5H\x1b[7mX\x1b[0m"))
	checkWidth(t, term)
	term.ScrollBy(-1)
	checkWidth(t, term)
	term.ScrollBy(-100)
	checkWidth(t, term)
	term.ScrollToBottom()
	checkWidth(t, term)
}

func TestScrollback(t *testing.T) {
	term := New(5, 2, 10)
	term.Write([]byte("1\r\n2\r\n3\r\n4\r\n5"))
	checkScreen(t, term, "4", "5")
	if term.ScrollbackLen() != 3 {
		t.Fatalf("ScrollbackLen = %d, 期望 3", term.ScrollbackLen())
	}
	if !term.AtBottom() || term.ScrollOffset() != 0 {
		t.Fatal("应处于底部")
	}
	term.ScrollBy(-2)
	if term.ScrollOffset() != 2 || term.AtBottom() {
		t.Fatalf("ScrollOffset = %d", term.ScrollOffset())
	}
	checkScreen(t, term, "2", "3")
	term.ScrollBy(-100) // 到顶 clamp
	if term.ScrollOffset() != 3 {
		t.Fatalf("ScrollOffset = %d, 期望 3", term.ScrollOffset())
	}
	checkScreen(t, term, "1", "2")
	term.ScrollBy(1)
	checkScreen(t, term, "2", "3")
	term.ScrollToBottom()
	checkScreen(t, term, "4", "5")
	term.ScrollBy(5) // 到底 clamp
	if term.ScrollOffset() != 0 {
		t.Fatalf("ScrollOffset = %d, 期望 0", term.ScrollOffset())
	}
}

func TestScrollbackLimit(t *testing.T) {
	term := New(5, 2, 3)
	for i := 0; i < 20; i++ {
		term.Write([]byte("x\r\n"))
	}
	if term.ScrollbackLen() != 3 {
		t.Fatalf("ScrollbackLen = %d, 期望 3", term.ScrollbackLen())
	}
	term.ScrollBy(-3)
	checkScreen(t, term, "x", "x")
}

func TestUnknownSequencesSwallowed(t *testing.T) {
	term := New(20, 2, 0)
	term.Write([]byte("\x1b[?2004h"))    // 未知私有模式
	term.Write([]byte("\x1b[>4;2m"))     // 私有前缀 + SGR
	term.Write([]byte("\x1b[!p"))        // 带中间字节
	term.Write([]byte("\x1b[6n"))        // DSR
	term.Write([]byte("\x1b[99999999X")) // 超大参数
	term.Write([]byte("\x1bZ"))          // 未知 ESC 序列
	term.Write([]byte("\x1b(B"))         // 字符集指定
	term.Write([]byte("\x1bP+q544e\x1b\\"))
	term.Write([]byte("ok"))
	checkScreen(t, term, "ok", "")
	for _, l := range term.Render() {
		if strings.Contains(stripANSI(l), "\x1b") {
			t.Fatalf("转义字符被打印: %q", l)
		}
	}
}

func TestOSC(t *testing.T) {
	term := New(20, 1, 0)
	term.Write([]byte("\x1b]0;my title\x07abc"))
	checkScreen(t, term, "abc")
	if term.Title() != "my title" {
		t.Fatalf("title = %q", term.Title())
	}
	term.Write([]byte("\x1b]2;t2\x1b\\z"))
	checkScreen(t, term, "abcz")
	if term.Title() != "t2" {
		t.Fatalf("title = %q", term.Title())
	}
}

func TestSaveRestoreCursor(t *testing.T) {
	term := New(6, 3, 0)
	term.Write([]byte("\x1b[31m\x1b[3;4H"))
	term.Write([]byte("\x1b7")) // 保存光标
	term.Write([]byte("\x1b[1;1H\x1b[0m"))
	term.Write([]byte("X"))
	term.Write([]byte("\x1b8")) // 恢复光标
	x, y, _ := term.Cursor()
	if x != 3 || y != 2 {
		t.Fatalf("恢复后光标 = %d,%d, 期望 3,2", x, y)
	}
	if term.attr.FG != 1 {
		t.Fatalf("恢复后 FG = %d, 期望 1", term.attr.FG)
	}
}

func TestCursorAPI(t *testing.T) {
	term := New(10, 3, 0)
	if x, y, v := term.Cursor(); x != 0 || y != 0 || !v {
		t.Fatalf("初始光标 = %d,%d,%v", x, y, v)
	}
	term.Write([]byte("ab"))
	if x, _, _ := term.Cursor(); x != 2 {
		t.Fatalf("写入 2 字符后 x = %d", x)
	}
	term.Write([]byte("01234567"))
	if x, y, _ := term.Cursor(); x != 10 || y != 0 {
		t.Fatalf("写满一行后光标 = %d,%d, 期望 10,0", x, y)
	}
	term.Write([]byte("\x1b[?25l"))
	if _, _, v := term.Cursor(); v {
		t.Fatal("?25l 后光标应不可见")
	}
	term.Write([]byte("\x1b[?25h"))
	if _, _, v := term.Cursor(); !v {
		t.Fatal("?25h 后光标应可见")
	}
}

func TestAltScreen(t *testing.T) {
	term := New(5, 2, 5)
	term.Write([]byte("main"))
	term.Write([]byte("\x1b[?1049h"))
	checkScreen(t, term, "", "")
	term.Write([]byte("alt"))
	checkScreen(t, term, "alt", "")
	term.Write([]byte("\x1b[?1049l"))
	checkScreen(t, term, "main", "")
}

func TestReset(t *testing.T) {
	term := New(5, 2, 10)
	term.Write([]byte("abc\r\n\x1b[31mdef\r\nghi"))
	term.Reset()
	checkScreen(t, term, "", "")
	if term.attr != DefaultAttr {
		t.Fatalf("Reset 后 attr = %+v", term.attr)
	}
	if x, y, _ := term.Cursor(); x != 0 || y != 0 {
		t.Fatalf("Reset 后光标 = %d,%d", x, y)
	}
	if term.ScrollbackLen() != 0 {
		t.Fatalf("Reset 后 ScrollbackLen = %d", term.ScrollbackLen())
	}
}

func TestESCcRIS(t *testing.T) {
	term := New(5, 2, 10)
	term.Write([]byte("abc\x1b[31m"))
	term.Write([]byte("\x1bc"))
	checkScreen(t, term, "", "")
	if term.attr != DefaultAttr {
		t.Fatalf("RIS 后 attr = %+v", term.attr)
	}
}

func TestNELAndIND(t *testing.T) {
	term := New(4, 3, 0)
	term.Write([]byte("ab\x1bEcd")) // NEL：回车 + 换行
	checkScreen(t, term, "ab", "cd", "")

	term = New(4, 3, 0)
	term.Write([]byte("\x1b[3;1H\x1bD")) // IND：末行继续下移触发上滚
	checkScreen(t, term, "", "", "")
}

func TestConcurrentWriteRender(t *testing.T) {
	term := New(40, 10, 20)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				term.Write([]byte("hello \x1b[31mworld\x1b[0m 中文\r\n"))
				term.Render()
			}
		}()
	}
	wg.Wait()
	checkWidth(t, term)
}
