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

// pingDoneMsg 巡检完成的通知消息。
type pingDoneMsg struct {
	results []remotessh.PingResult
}

// ---------- 入口 ----------

// pingMeta 处理命令行 `ping [过滤词...]` 元命令：
//   - 无过滤词：打开多选目标对话框；
//   - 带过滤词：直接对「名称/主机/用户/分组匹配的连接」巡检。
func (m *Model) pingMeta(args []string) bool {
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
	m.runPing(conns)
	return true
}

// runPing 启动后台巡检并展示进度对话框。
func (m *Model) runPing(conns []store.Connection) {
	m.dlg = &dlg{
		kind:  dlgText,
		title: fmt.Sprintf("正在巡检 %d 台主机…", len(conns)),
		body:  []string{"", "  正在逐台建立连接并认证，请稍候…"},
	}
	m.afterCmd = pingCmd(conns)
}

// pingCmd 并发巡检并返回结果消息。
func pingCmd(conns []store.Connection) tea.Cmd {
	return func() tea.Msg {
		results := remotessh.Ping(conns, 10*time.Second)
		sort.SliceStable(results, func(a, b int) bool {
			// 按状态（可达最优）+ 名称排序
			sa, sb := results[a].Status, results[b].Status
			if sa != sb {
				return sa < sb
			}
			return results[a].Name < results[b].Name
		})
		return pingDoneMsg{results: results}
	}
}

// ---------- 结果展示 ----------

// showPingResult 展示巡检汇总。
func (m *Model) showPingResult(msg pingDoneMsg) {
	ok, af, un, to := remotessh.PingCount(msg.results)

	var body []string
	body = append(body, "")
	body = append(body, fmt.Sprintf("  汇总      %d 台可达 · %d 认证失败 · %d 不可达 · %d 超时", ok, af, un, to))
	body = append(body, "")

	icons := map[remotessh.PingStatus]string{
		remotessh.PingOK:         "✓ ",
		remotessh.PingAuthFail:   "✗ ",
		remotessh.PingUnreachable: "✗ ",
		remotessh.PingTimeout:    "? ",
	}
	for _, r := range msg.results {
		icon, _ := icons[r.Status]
		lat := ""
		if r.Status == remotessh.PingOK {
			lat = fmt.Sprintf("  %dms", int(r.Latency / time.Millisecond))
		}
		body = append(body, fmt.Sprintf("  %s%-16s %-12s %s%s", icon, r.Name, r.Status.Text(), r.Host, lat))
		if r.Detail != "" {
			for _, line := range wrapText(r.Detail, max(30, m.width-22)) {
				body = append(body, "      "+line)
			}
		}
	}
	body = append(body, "")
	body = append(body, "  Esc / Enter 关闭")

	m.dlg = &dlg{kind: dlgText, title: "连接健康巡检结果", body: body}

	total := len(msg.results)
	if ok == total {
		_ = m.setMsg(fmt.Sprintf("巡检完成：%d 台全部可达", total))
	} else {
		_ = m.setMsg(fmt.Sprintf("巡检完成：%d 可达 / %d 异常（认证失败 %d · 不可达 %d · 超时 %d）", ok, total-ok, af, un, to))
	}
}