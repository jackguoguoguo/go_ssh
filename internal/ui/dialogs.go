package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"sshtool/internal/store"
)

// 对话框类型。
const (
	dlgForm    = iota + 1 // 表单（连接编辑 / 收藏编辑）
	dlgSecret             // 密码 / 私钥口令
	dlgConfirm            // 确认
	dlgHelp               // 帮助
)

// dlgField 表单字段。
type dlgField struct {
	label   string
	value   []rune
	pos     int
	secret  bool
	options []string // 非空时该字段为可切换选项
	hint    string
}

// dlg 描述一个模态对话框。
type dlg struct {
	kind     int
	title    string
	fields   []dlgField
	focus    int
	message  string
	body     []string // 帮助等非表单内容
	okLabel  string
	onOK     func(m *Model, values []string)
	onCancel func(m *Model)
}

// dlgHit 记录对话框内可点击区域的屏幕坐标。
type dlgHit struct {
	box      rect
	fieldY   []int // 每个字段所在的屏幕行
	btnY     int
	okX0     int
	okX1     int
	cancelX0 int
	cancelX1 int
	valid    bool
}

// ---------- 构造 ----------

// newConnDialog 生成连接编辑表单。
func newConnDialog(title string, c store.Connection, onOK func(m *Model, values []string)) *dlg {
	if c.Port == 0 {
		c.Port = store.DefaultSSHPort
	}
	if c.AuthType == "" {
		c.AuthType = store.AuthPassword
	}
	if c.User == "" {
		c.User = "root"
	}

	d := &dlg{
		kind:    dlgForm,
		title:   title,
		okLabel: "保存并连接",
		onOK:    onOK,
	}
	d.fields = []dlgField{
		{label: "名称", value: []rune(c.Name), hint: "显示名，留空则使用主机"},
		{label: "分组", value: []rune(c.Group)},
		{label: "主机", value: []rune(c.Host), hint: "IP 或域名"},
		{label: "端口", value: []rune(itoa(c.Port))},
		{label: "用户名", value: []rune(c.User)},
		{label: "认证方式", value: []rune(string(c.AuthType)), options: []string{string(store.AuthPassword), string(store.AuthKey)}, hint: "← → 或空格切换"},
		{label: "密码", value: []rune(c.Password), secret: true, hint: "留空则连接时询问"},
		{label: "每次询问密码", value: []rune(boolStr(c.AskPassword)), options: []string{"false", "true"}},
		{label: "私钥路径", value: []rune(c.KeyPath), hint: "如 ~/.ssh/id_rsa"},
		{label: "私钥口令", value: []rune(c.KeyPassphrase), secret: true},
		{label: "每次询问口令", value: []rune(boolStr(c.AskPassphrase)), options: []string{"false", "true"}},
		{label: "启动命令", value: []rune(c.StartupCmd), hint: "连接后自动执行"},
		{label: "备注", value: []rune(c.Note)},
	}
	for i := range d.fields {
		d.fields[i].pos = len(d.fields[i].value)
	}
	return d
}

// newSecretDialog 生成密码输入对话框。
func newSecretDialog(title, message string, onOK func(m *Model, values []string)) *dlg {
	return &dlg{
		kind:    dlgSecret,
		title:   title,
		message: message,
		okLabel: "确定",
		fields:  []dlgField{{label: "口令", value: nil, secret: true}},
		onOK:    onOK,
	}
}

// newConfirmDialog 生成确认对话框。
func newConfirmDialog(title, message string, onOK func(m *Model, values []string)) *dlg {
	return &dlg{
		kind:    dlgConfirm,
		title:   title,
		message: message,
		okLabel: "确定",
		onOK:    onOK,
	}
}

// newFavDialog 生成收藏命令表单。
func newFavDialog(title string, f store.FavoriteCmd, onOK func(m *Model, values []string)) *dlg {
	return &dlg{
		kind:    dlgForm,
		title:   title,
		okLabel: "保存",
		fields: []dlgField{
			{label: "名称", value: []rune(f.Name)},
			{label: "命令", value: []rune(f.Cmd), hint: "点击面板项会填入命令行，回车执行"},
			{label: "分组", value: []rune(f.Group)},
		},
		onOK: onOK,
	}
}

// ---------- 渲染 ----------

// renderDialog 渲染当前对话框（整屏居中覆盖）。
func (m *Model) renderDialog() string {
	d := m.dlg
	if d == nil {
		return ""
	}

	hit := dlgHit{}
	var lines []string

	if d.title != "" {
		lines = append(lines, styleTitle.Render(d.title))
		lines = append(lines, "")
	}
	if d.message != "" {
		width := minInt(60, max(20, m.width-16))
		for _, line := range wrapText(d.message, width) {
			lines = append(lines, lipgloss.NewStyle().Foreground(cFg).Render(line))
		}
		lines = append(lines, "")
	}

	switch d.kind {
	case dlgHelp:
		for _, line := range d.body {
			lines = append(lines, lipgloss.NewStyle().Foreground(cFg).Render(line))
		}
	case dlgForm, dlgSecret:
		for i, f := range d.fields {
			active := i == d.focus
			if f.label != "" {
				ls := styleDim
				if active {
					ls = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
				}
				_ = ls
			}
			hit.fieldY = append(hit.fieldY, len(lines))

			labelStyle := styleDim
			if active {
				labelStyle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
			}
			labelText := ""
			if f.label != "" {
				labelText = labelStyle.Render(padVisible(f.label, 14)) + " "
			}

			shown := string(f.value)
			if f.secret {
				shown = strings.Repeat("*", len(f.value))
			}
			if len(f.options) > 0 {
				shown = string(f.value) + "  ‹← → 切换›"
			}
			width := minInt(44, max(16, m.width-34))
			if len([]rune(shown)) > width-1 {
				shown = string([]rune(shown)[len([]rune(shown))-(width-1):])
			}
			fieldStyle := lipgloss.NewStyle().
				Foreground(cFg).
				Width(width).
				MaxWidth(width).
				Background(lipgloss.Color("#1f2335"))
			if active {
				fieldStyle = fieldStyle.Background(lipgloss.Color("#292e42"))
			}
			row := labelText + fieldStyle.Render(shown)
			if f.hint != "" && active {
				row += "  " + styleDim.Render(f.hint)
			}
			lines = append(lines, row)
		}
	}

	lines = append(lines, "")

	okLabel := d.okLabel
	if okLabel == "" {
		okLabel = "确定"
	}
	okBtn := lipgloss.NewStyle().
		Background(cOK).Foreground(cSelFg).Bold(true).
		Padding(0, 2).Render(okLabel)
	cancelBtn := lipgloss.NewStyle().
		Background(lipgloss.Color("#292e42")).Foreground(cFg).
		Padding(0, 2).Render("取消")

	hit.btnY = len(lines)
	const btnPrefix = 2
	hit.okX0 = btnPrefix
	hit.okX1 = hit.okX0 + lipgloss.Width(okBtn)
	hit.cancelX0 = hit.okX1 + 2
	hit.cancelX1 = hit.cancelX0 + lipgloss.Width(cancelBtn)
	lines = append(lines, strings.Repeat(" ", btnPrefix)+okBtn+"  "+cancelBtn)
	lines = append(lines, "")
	lines = append(lines, styleDim.Render("Tab/↑↓ 切换字段 · Enter 确定 · Esc 取消 · 支持鼠标点击"))

	content := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cAccent2).
		Padding(1, 2).
		Background(cBg).
		Render(content)

	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	x0 := (m.width - bw) / 2
	y0 := (m.height - bh) / 2
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	hit.box = rect{x: x0, y: y0, w: bw, h: bh}
	for i := range hit.fieldY {
		hit.fieldY[i] += y0 + 2 // 边框 + padding
	}
	hit.btnY += y0 + 2
	hit.okX0 += x0 + 2
	hit.okX1 += x0 + 2
	hit.cancelX0 += x0 + 2
	hit.cancelX1 += x0 + 2
	hit.valid = true
	m.dlgHit = hit

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// ---------- 键盘 ----------

// handleDialogKey 处理对话框内的按键，返回 true 表示已消费。
func (m *Model) handleDialogKey(msg tea.KeyMsg) bool {
	d := m.dlg
	if d == nil {
		return false
	}

	// Alt+数字等全局快捷键在对话框中仍然生效，交给调用方先行处理。

	if msg.Type == tea.KeyEsc {
		m.closeDialog()
		return true
	}

	switch d.kind {
	case dlgHelp:
		m.dlg = nil
		return true

	case dlgConfirm:
		if msg.Type == tea.KeyEnter || (len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y')) {
			fn := d.onOK
			m.dlg = nil
			if fn != nil {
				fn(m, nil)
			}
			return true
		}
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'n' || msg.Runes[0] == 'N') {
			m.dlg = nil
			return true
		}
		return true

	case dlgSecret, dlgForm:
		f := &d.fields[d.focus]
		switch msg.Type {
		case tea.KeyTab, tea.KeyDown:
			d.focus = (d.focus + 1) % len(d.fields)
			return true
		case tea.KeyShiftTab, tea.KeyUp:
			d.focus = (d.focus + len(d.fields) - 1) % len(d.fields)
			return true
		case tea.KeyEnter:
			values := collectValues(d)
			fn := d.onOK
			m.dlg = nil
			if fn != nil {
				fn(m, values)
			}
			return true
		case tea.KeyLeft, tea.KeyRight, tea.KeySpace:
			if len(f.options) > 0 {
				cur := 0
				for i, o := range f.options {
					if o == string(f.value) {
						cur = i
					}
				}
				if msg.Type == tea.KeyLeft {
					cur = (cur + len(f.options) - 1) % len(f.options)
				} else {
					cur = (cur + 1) % len(f.options)
				}
				f.value = []rune(f.options[cur])
				f.pos = len(f.value)
				return true
			}
		case tea.KeyBackspace:
			if f.pos > 0 {
				f.value = append(f.value[:f.pos-1], f.value[f.pos:]...)
				f.pos--
			}
			return true
		case tea.KeyDelete:
			if f.pos < len(f.value) {
				f.value = append(f.value[:f.pos], f.value[f.pos+1:]...)
			}
			return true
		case tea.KeyHome:
			f.pos = 0
			return true
		case tea.KeyEnd:
			f.pos = len(f.value)
			return true
		case tea.KeyCtrlU:
			f.value = nil
			f.pos = 0
			return true
		}
		if len(msg.Runes) > 0 && msg.Runes[0] >= 0x20 {
			runes := append([]rune{}, msg.Runes...)
			f.value = append(f.value[:f.pos], append(runes, f.value[f.pos:]...)...)
			f.pos += len(runes)
			return true
		}
		return true
	}
	return true
}

// closeDialog 关闭对话框并触发取消回调。
func (m *Model) closeDialog() {
	d := m.dlg
	m.dlg = nil
	if d != nil && d.onCancel != nil {
		d.onCancel(m)
	}
}

func collectValues(d *dlg) []string {
	out := make([]string, len(d.fields))
	for i, f := range d.fields {
		out[i] = strings.TrimSpace(string(f.value))
	}
	return out
}

// ---------- 帮助内容 ----------

func helpBody() []string {
	return []string{
		"  Alt+1..9        切换到第 N 个会话",
		"  Alt+← / Alt+→   上一个 / 下一个会话",
		"  Ctrl+N          新建连接",
		"  Ctrl+E          编辑选中的连接",
		"  Ctrl+D          删除选中的连接",
		"  Ctrl+W          关闭当前会话",
		"  Ctrl+R          重连当前会话",
		"  Ctrl+B          跳到「收藏命令」面板",
		"  Ctrl+K          跳到「历史命令」面板并过滤",
		"  Ctrl+P          把输入行内容加入收藏",
		"  Ctrl+X          进入命令行（可编辑后回车执行）",
		"  Tab / Shift+Tab 切换焦点（连接→收藏→历史→终端）",
		"  Enter           连接面板=连接；收藏/历史=填入并立即执行",
		"  /               过滤当前面板；Esc 取消过滤",
		"  ↑ ↓             移动选择（鼠标滚轮同样可用）",
		"  PgUp / PgDn     终端回滚；End 回到最新输出",
		"  Ctrl+C          终端直通模式下发送给远端（SIGINT）",
		"  Ctrl+H / F1     显示本帮助",
		"  Ctrl+Q          退出（二次确认）",
		"",
		"  鼠标：点击标签切换会话 · 点击标签 × 关闭会话 · 点击列表项选中",
		"        双击列表项（或回车）执行 · 滚轮滚动列表与终端",
		"",
		"  配置文件：" + configPathHint,
	}
}

// ---------- 工具 ----------

func wrapText(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		runes := []rune(para)
		for len(runes) > width {
			out = append(out, string(runes[:width]))
			runes = runes[width:]
		}
		out = append(out, string(runes))
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func atoi(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		return -n
	}
	return n
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func parseBool(s string) bool {
	s = strings.TrimSpace(s)
	return s == "true" || s == "1" || s == "y" || s == "yes"
}
