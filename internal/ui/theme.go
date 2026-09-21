package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// go-runewidth 在 Windows 上按 GetConsoleOutputCP() 决定是否启用「东亚歧义宽度」：
// 代码页 932/936/949/950 会把 ← → · ● … 这些歧义字符算成 2 列。而 lipgloss 内部走
// x/ansi，同一批字符恒算 1 列 —— 两边口径不一致，面板边框就会与内容错位。
// 这里统一钉死为「窄」，与 lipgloss 对齐；终端侧也必须按 1 列渲染
// （Windows Terminal + Cascadia Mono 默认如此）。
func init() {
	runewidth.DefaultCondition.EastAsianWidth = false
}

// 配色（tokyo-night 风格，运行时由 applyTheme 按主题覆盖）。
var (
	cBg      = lipgloss.Color("#16161e")
	cPanel   = lipgloss.Color("#1f2335")
	cFg      = lipgloss.Color("#c0caf5")
	cDim     = lipgloss.Color("#565f89")
	cAccent  = lipgloss.Color("#7aa2f7")
	cAccent2 = lipgloss.Color("#bb9af7")
	cOK      = lipgloss.Color("#9ece6a")
	cWarn    = lipgloss.Color("#e0af68")
	cErr     = lipgloss.Color("#f7768e")
	cSelFg   = lipgloss.Color("#16161e")
	cBorder  = lipgloss.Color("#292e42")
)

// 常用样式。
var (
	styleBase = lipgloss.NewStyle().Foreground(cFg)

	styleTitle = lipgloss.NewStyle().
			Foreground(cAccent2).
			Bold(true)

	styleDim = lipgloss.NewStyle().Foreground(cDim)

	styleHeader = lipgloss.NewStyle().
			Foreground(cFg).
			Background(cPanel)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(cDim).
			Background(cPanel)

	styleHint = lipgloss.NewStyle().Foreground(cDim)
)

// boxStyle 生成一个面板边框样式。
func boxStyle(w, h int, active bool) lipgloss.Style {
	border := lipgloss.RoundedBorder()
	bc := cBorder
	if active {
		bc = cAccent
	}
	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(bc).
		Width(w).
		Height(h).
		MaxWidth(w).
		MaxHeight(h)
}

// renderItem 渲染一行列表项，selected 表示是否为当前选中项。
func renderItem(w int, selected bool, plain string, accent string) string {
	text := plain
	if accent != "" {
		text = plain + " " + accent
	}
	text = cutPlain(text, w)

	if selected {
		// 选中项整行反色：内部不再嵌套颜色，避免与高亮背景冲突。
		return lipgloss.NewStyle().
			Background(cAccent).
			Foreground(cSelFg).
			Bold(true).
			Width(w).
			MaxWidth(w).
			Render(text + strings.Repeat(" ", max(0, w-runewidth.StringWidth(text))))
	}

	s := styleBase.Render(plain)
	if accent != "" {
		s += " " + styleDim.Render(accent)
	}
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(truncateVisible(s, w))
}

// cutPlain 按显示宽度裁剪纯文本（不含 ANSI）。
func cutPlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	runes := []rune(s)
	width := 0
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		rw := runewidth.RuneWidth(r)
		if width+rw > w {
			break
		}
		out = append(out, r)
		width += rw
	}
	return string(out)
}

// truncateVisible 按显示宽度裁剪含 ANSI 的字符串。
func truncateVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansiCut(s, 0, w)
}

// padVisible 用空格把含 ANSI 的字符串补齐到显示宽度 w。
func padVisible(s string, w int) string {
	got := ansiWidth(s)
	if got >= w {
		return s
	}
	return s + strings.Repeat(" ", w-got)
}

// ansiWidth 计算含 SGR 转义序列的字符串的显示宽度。
// 终端渲染结果里只会出现 SGR（\x1b[...m），因此按 'm' 截断是安全的。
func ansiWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == 0x1b {
			if j := strings.IndexByte(s[i:], 'm'); j >= 0 {
				i += j + 1
				continue
			}
			i += size
			continue
		}
		if r == 0 {
			i += size
			continue
		}
		i += size
		w += runewidth.RuneWidth(r)
	}
	return w
}

// ansiCut 截取显示列区间 [a, b) 的内容并保留其中的 SGR 序列。
func ansiCut(s string, a, b int) string {
	if b <= a {
		return ""
	}
	var sb strings.Builder
	col := 0
	cur := ""
	wroteStyle := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == 0x1b {
			if j := strings.IndexByte(s[i:], 'm'); j >= 0 {
				seq := s[i : i+j+1]
				i += j + 1
				cur = seq
				continue
			}
			i += size
			continue
		}
		if r == 0 {
			i += size
			continue
		}
		i += size
		rw := runewidth.RuneWidth(r)
		if col+rw > a && col < b {
			if !wroteStyle {
				sb.WriteString(cur)
				wroteStyle = true
			}
			if col >= a && col+rw <= b {
				sb.WriteRune(r)
			} else {
				// 与边界部分重叠的宽字符用空格替代，避免破坏网格。
				sb.WriteString(strings.Repeat(" ", rw))
			}
		}
		col += rw
	}
	return sb.String()
}

// highlightCell 把第 x 列（显示列）反显，用于绘制终端光标。
func highlightCell(s string, x, w int) string {
	if x < 0 || x >= w {
		return s
	}
	left := ansiCut(s, 0, x)
	cell := ansiCut(s, x, x+1)
	right := ansiCut(s, x+1, w)
	if cell == "" {
		cell = " "
	}
	return left + "\x1b[7m" + cell + "\x1b[0m" + right
}
