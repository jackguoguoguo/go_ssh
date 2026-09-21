package ui

import (
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/keytool"
	"sshtool/internal/store"
)

// auditDoneMsg 远端公钥盘点完成。
type auditDoneMsg struct {
	results []keytool.AuditResult
	local   []keytool.KeyInfo
	path    string
}

// ---------- 入口 ----------

// openAuditPickTargets 选择要盘点的目标主机（复用多选选择器）。
func (m *Model) openAuditPickTargets() {
	conns := m.st.GetConnections()
	if len(conns) == 0 {
		m.dlg = &dlg{kind: dlgText, title: "没有可用的服务器", body: []string{"", "  请先用 Ctrl+N 至少新建一条连接。"}}
		return
	}
	items := make([]pickItem, 0, len(conns))
	for _, c := range conns {
		desc := fmt.Sprintf("%s@%s:%d", fallback(c.User, "root"), c.Host, portOr22(c))
		if c.Group != "" {
			desc += "  #" + c.Group
		}
		items = append(items, pickItem{Label: fallback(c.Name, c.Host), Desc: desc})
	}
	m.dlg = &dlg{
		kind:    dlgPick,
		title:   "选择要盘点的主机（空格多选 / a 全选）",
		items:   items,
		multi:   true,
		checked: map[int]bool{},
		onPick: func(m *Model, picked []int) {
			var targets []store.Connection
			for _, i := range picked {
				if i >= 0 && i < len(conns) {
					targets = append(targets, conns[i])
				}
			}
			m.runAudit(targets)
		},
	}
}

// runAudit 后台并发拉取各主机的 authorized_keys 并与本机密钥比对。
func (m *Model) runAudit(targets []store.Connection) {
	if len(targets) == 0 {
		m.setMsg("未选择任何主机")
		return
	}
	local, _ := keytool.ListLocal(keytool.DefaultDir())
	if len(local) == 0 {
		m.setMsg("本机没有公钥，无法比对（可先用 Ctrl+G ①生成）")
		return
	}
	m.dlg = &dlg{
		kind:  dlgText,
		title: fmt.Sprintf("正在盘点 %d 台主机…", len(targets)),
		body:  []string{"", "  正在拉取各主机的 authorized_keys 并与本机密钥比对…"},
	}
	m.afterCmd = auditCmd(targets, local, keytool.DefaultRemotePath)
}

func auditCmd(targets []store.Connection, local []keytool.KeyInfo, path string) tea.Cmd {
	return func() tea.Msg {
		results := keytool.Audit(targets, local, path, 20*time.Second)
		sort.SliceStable(results, func(a, b int) bool { return results[a].Name < results[b].Name })
		return auditDoneMsg{results: results, local: local, path: path}
	}
}

// ---------- 结果展示 ----------

func (m *Model) showAuditResult(msg auditDoneMsg) {
	sum := keytool.Summarize(msg.results)

	var body []string
	body = append(body, "")
	body = append(body, "  本机公钥  "+fmt.Sprintf("%d 把", len(msg.local)))
	body = append(body, "  盘点路径  "+msg.path)
	body = append(body, fmt.Sprintf("  汇总      已全授权 %d 台 · 待推送 %d 台 · 含废弃条目 %d 台 · 失败 %d 台",
		sum.Fully, sum.NeedPush, sum.StaleHost, sum.Failed))
	body = append(body, "")

	for _, r := range msg.results {
		if r.Err != "" {
			body = append(body, fmt.Sprintf("  ✗ %s  %s", r.Name, r.Host))
			for _, line := range wrapText("    拉取失败："+r.Err, max(30, m.width-20)) {
				body = append(body, line)
			}
			continue
		}
		status := "✓ 已全授权"
		if len(r.Missing) > 0 {
			status = fmt.Sprintf("· 缺 %d 把", len(r.Missing))
		}
		body = append(body, fmt.Sprintf("  %-10s %-16s 远端共 %d 条 · 已授权 %d · 废弃 %d",
			status, r.Name, r.Total, len(r.Authorized), len(r.Stale)))
		if len(r.Stale) > 0 {
			for _, s := range r.Stale {
				who := s.Comment
				if who == "" {
					who = "（无注释）"
				}
				body = append(body, fmt.Sprintf("               废弃: %s  %s", s.Alg, who))
			}
		}
	}
	body = append(body, "")
	body = append(body, "  提示：「缺 N 把」的主机可用 Ctrl+G ②推送公钥；「废弃」指远端有、本机没有对应私钥的条目。")
	body = append(body, "  Esc / Enter 关闭")

	m.dlg = &dlg{kind: dlgText, title: "远端公钥盘点结果", body: body}
	if sum.Failed > 0 || sum.NeedPush > 0 {
		_ = m.setMsg(fmt.Sprintf("盘点完成：%d 台待推送 · %d 台含废弃 · %d 台失败", sum.NeedPush, sum.StaleHost, sum.Failed))
	} else {
		_ = m.setMsg("盘点完成：本机密钥已在全部目标主机授权")
	}
}
