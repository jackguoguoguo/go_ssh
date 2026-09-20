package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// batchDoneMsg 批量执行完成的通知消息。
type batchDoneMsg struct {
	cmd     string
	results []remotessh.BatchResult
}

// ---------- 入口 ----------

// batchMeta 处理命令行 `batch [过滤词...]` 元命令：
//   - 无过滤词：打开多选目标对话框；
//   - 带过滤词：跳过选择，直接对「名称/主机/用户/分组匹配的连接」执行后续命令对话框。
func (m *Model) batchMeta(args []string) bool {
	q := strings.Join(args, " ")
	conns := filterConnections(m.st.GetConnections(), q)
	if len(conns) == 0 {
		suffix := ""
		if q != "" {
			suffix = "（" + q + "）"
		}
		m.setMsg("没有匹配的连接" + suffix)
		return true
	}
	m.openBatchCmdInput(conns)
	return true
}

// openBatchRun 打开「多主机批量执行」的目标选择对话框。
// 复用 dlgPick 多选能力（与密钥推送的目标选择一致）。
func (m *Model) openBatchRun() {
	conns := m.st.GetConnections()
	if len(conns) == 0 {
		m.dlg = &dlg{kind: dlgText, title: "没有可用的服务器", body: []string{"", "  请先用 Ctrl+N 至少新建一条连接。"}}
		return
	}
	items := make([]pickItem, 0, len(conns))
	for _, c := range conns {
		items = append(items, pickItem{Label: c.Name, Desc: batchHostDesc(c)})
	}
	m.dlg = &dlg{
		kind:  dlgPick,
		title: fmt.Sprintf("批量执行（%d 台可选 · 空格多选 · 回车执行）", len(items)),
		items: items,
		multi: true,
		onCancel: func(m *Model) { m.batchTargets = nil },
		onPick: func(m *Model, picked []int) {
			pickedConns := make([]store.Connection, 0, len(picked))
			for _, idx := range picked {
				if idx >= 0 && idx < len(conns) {
					pickedConns = append(pickedConns, conns[idx])
				}
			}
			m.openBatchCmdInput(pickedConns)
		},
	}
}

// openBatchCmdInput 收集要执行的命令。
func (m *Model) openBatchCmdInput(targets []store.Connection) {
	names := make([]string, 0, len(targets))
	for _, c := range targets {
		names = append(names, c.Name)
	}
	m.batchTargets = targets
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   fmt.Sprintf("批量执行命令 → %d 台", len(targets)),
		okLabel: "执行",
		fields: []dlgField{
			{label: "命令", value: []rune(m.lastBatchCmd), hint: "回车执行，将同时发往这 %d 台主机"},
		},
		onCancel: func(m *Model) { m.batchTargets = nil },
		onOK: func(m *Model, values []string) {
			cmd := strings.TrimSpace(values[0])
			if cmd == "" {
				return
			}
			targets := m.batchTargets
			m.batchTargets = nil
			m.lastBatchCmd = cmd
			m.runBatch(targets, cmd)
		},
	}
}

// runBatch 对目标并发执行命令；危险命令先二次确认。
func (m *Model) runBatch(targets []store.Connection, cmd string) {
	if len(targets) == 0 {
		m.setMsg("未选择任何主机")
		return
	}
	reason := risky(cmd)
	if reason != "" {
		m.dlg = newConfirmDialog(
			"危险命令确认",
			"命令被判定为高风险操作："+reason+"\n命令："+cmd+
				fmt.Sprintf("\n将同时在 %d 台主机上执行，请再次确认。", len(targets)),
			func(m *Model, _ []string) { m.launchBatch(targets, cmd) },
		)
		m.dlg.strictConfirm = true
		return
	}
	m.launchBatch(targets, cmd)
}

// launchBatch 记录历史并启动后台执行。
func (m *Model) launchBatch(targets []store.Connection, cmd string) {
	m.st.AddHistory(cmd, fmt.Sprintf("批量(%d 台)", len(targets)))
	_ = m.st.Save()
	m.refresh()

	m.dlg = &dlg{
		kind:  dlgText,
		title: fmt.Sprintf("正在批量执行（%d 台）…", len(targets)),
		body:  []string{"", "  正在分发命令并收集输出，请稍候…"},
	}
	m.afterCmd = batchCmd(targets, cmd)
}

// batchCmd 并发执行并返回结果消息。
func batchCmd(targets []store.Connection, cmd string) tea.Cmd {
	return func() tea.Msg {
		results := remotessh.Batch(targets, cmd, 60*time.Second)
		sort.SliceStable(results, func(a, b int) bool {
			return results[a].Name < results[b].Name
		})
		return batchDoneMsg{cmd: cmd, results: results}
	}
}

// ---------- 结果展示 ----------

// showBatchResult 展示批量执行汇总结果。
func (m *Model) showBatchResult(msg batchDoneMsg) {
	var body []string
	body = append(body, "")
	body = append(body, "  $ "+msg.cmd)

	fail := 0
	for _, r := range msg.results {
		if r.Err != "" {
			fail++
		}
	}
	body = append(body, fmt.Sprintf("  汇总      %d 台成功 · %d 台失败", len(msg.results)-fail, fail))
	body = append(body, "")

	for _, r := range msg.results {
		status := "✓"
		if r.Err != "" {
			status = "✗"
		}
		body = append(body, fmt.Sprintf("  %s  %s", status, r.Name))
		body = append(body, "      "+r.Host)
		if r.Err != "" {
			for _, line := range wrapText("原因: "+r.Err, max(30, m.width-20)) {
				body = append(body, "      "+line)
			}
			continue
		}
		lines := trimmedOutput(r.Stdout)
		if len(lines) == 0 && strings.TrimSpace(r.Stderr) != "" {
			body = append(body, "      [stderr]")
			lines = trimmedOutput(r.Stderr)
		}
		for _, line := range lines {
			body = append(body, "      "+line)
		}
	}
	body = append(body, "")
	body = append(body, "  Esc / Enter 关闭")

	m.dlg = &dlg{kind: dlgText, title: "批量执行结果", body: body}
	if fail > 0 {
		_ = m.setMsg(fmt.Sprintf("批量执行完成：%d 成功 / %d 失败（%s）", len(msg.results)-fail, fail, msg.cmd))
	} else {
		_ = m.setMsg(fmt.Sprintf("批量执行完成：%d 台全部成功（%s）", len(msg.results), msg.cmd))
	}
}

// trimmedOutput 把输出裁剪为最多 10 行、每行 100 字符的展示文本。
func trimmedOutput(s string) []string {
	const maxLines = 10
	const maxLen = 100
	all := strings.Split(s, "\n")
	n := 0
	var out []string
	for _, line := range all {
		line = strings.TrimRight(line, "\r")
		if len(line) > maxLen {
			line = line[:maxLen] + "…"
		}
		out = append(out, line)
		n++
		if n >= maxLines {
			if len(all) > n {
				out = append(out, fmt.Sprintf("      …（共 %d 行，已截断）", len(all)))
			}
			break
		}
	}
	return out
}

func batchHostDesc(c store.Connection) string {
	user := c.User
	if user == "" {
		user = "root"
	}
	port := c.Port
	if port <= 0 {
		port = store.DefaultSSHPort
	}
	return fmt.Sprintf("%s@%s:%d", user, c.Host, port)
}