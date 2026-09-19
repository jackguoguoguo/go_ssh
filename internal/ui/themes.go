package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"sshtool/internal/store"
)

// palette 是一套完整的配色方案。所有 UI 颜色都取自这里，便于整体切换主题。
type palette struct {
	Bg      lipgloss.Color
	Panel   lipgloss.Color
	Fg      lipgloss.Color
	Dim     lipgloss.Color
	Accent  lipgloss.Color
	Accent2 lipgloss.Color
	OK      lipgloss.Color
	Warn    lipgloss.Color
	Err     lipgloss.Color
	SelFg   lipgloss.Color // 选中项反色后的文字色（落在 Accent 背景上）
	Border  lipgloss.Color
}

// themes 内置主题表。键名即 Settings.Theme 的取值。
var themes = map[string]palette{
	"dark": {
		Bg:      lipgloss.Color("#16161e"),
		Panel:   lipgloss.Color("#1f2335"),
		Fg:      lipgloss.Color("#c0caf5"),
		Dim:     lipgloss.Color("#565f89"),
		Accent:  lipgloss.Color("#7aa2f7"),
		Accent2: lipgloss.Color("#bb9af7"),
		OK:      lipgloss.Color("#9ece6a"),
		Warn:    lipgloss.Color("#e0af68"),
		Err:     lipgloss.Color("#f7768e"),
		SelFg:   lipgloss.Color("#16161e"),
		Border:  lipgloss.Color("#292e42"),
	},
	"light": {
		Bg:      lipgloss.Color("#e1e2e7"),
		Panel:   lipgloss.Color("#d5d6e0"),
		Fg:      lipgloss.Color("#343b5c"),
		Dim:     lipgloss.Color("#7982a9"),
		Accent:  lipgloss.Color("#34548a"),
		Accent2: lipgloss.Color("#7a3e9d"),
		OK:      lipgloss.Color("#287a3b"),
		Warn:    lipgloss.Color("#9a6b1f"),
		Err:     lipgloss.Color("#b3304a"),
		SelFg:   lipgloss.Color("#ffffff"),
		Border:  lipgloss.Color("#b8b9c5"),
	},
}

// currentTheme 记录当前生效的主题名。
var currentTheme = store.DefaultTheme

// applyTheme 把指定主题应用到全局配色与共享样式；未知名称回退到默认主题。
// 注意：主题为进程级全局状态，本工具为单实例 UI，渲染时统一读取这些变量。
func applyTheme(name string) {
	p, ok := themes[name]
	if !ok {
		p = themes[store.DefaultTheme]
		name = store.DefaultTheme
	}
	cBg = p.Bg
	cPanel = p.Panel
	cFg = p.Fg
	cDim = p.Dim
	cAccent = p.Accent
	cAccent2 = p.Accent2
	cOK = p.OK
	cWarn = p.Warn
	cErr = p.Err
	cSelFg = p.SelFg
	cBorder = p.Border

	// 重建依赖配色的共享样式。
	styleBase = lipgloss.NewStyle().Foreground(cFg)
	styleTitle = lipgloss.NewStyle().Foreground(cAccent2).Bold(true)
	styleDim = lipgloss.NewStyle().Foreground(cDim)
	styleHeader = lipgloss.NewStyle().Foreground(cFg).Background(cPanel)
	styleStatusBar = lipgloss.NewStyle().Foreground(cDim).Background(cPanel)
	styleHint = lipgloss.NewStyle().Foreground(cDim)

	currentTheme = name
}

// CurrentTheme 返回当前生效主题名。
func CurrentTheme() string { return currentTheme }

// themeNames 返回所有可用主题名（升序），用于提示信息。
func themeNames() []string {
	names := make([]string, 0, len(themes))
	for n := range themes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// applyThemeByName 切换主题并持久化到设置（命令行 `theme <name>` 调用）。
func (m *Model) applyThemeByName(name string) {
	name = strings.TrimSpace(name)
	if _, ok := themes[name]; !ok {
		m.setMsg("未知主题：" + name + "（可选：" + strings.Join(themeNames(), " / ") + "）")
		return
	}
	applyTheme(name)
	m.st.UpdateSettings(func(st *store.Settings) { st.Theme = name })
	_ = m.st.Save()
	m.setMsg("已切换主题：" + name)
}
