package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/keytool"
	"sshtool/internal/store"
)

// rotateState 一轮轮换向导的中间状态。
type rotateState struct {
	newKey  keytool.KeyInfo
	oldKey  keytool.KeyInfo
	targets []store.Connection
	keepOld bool
}

// rotateGenDoneMsg 新密钥生成完成。
type rotateGenDoneMsg struct {
	info keytool.KeyInfo
	err  string
}

// rotateDoneMsg 轮换执行完成。
type rotateDoneMsg struct {
	results []keytool.RotateResult
}

// ---------- ① 生成新密钥 ----------

func (m *Model) openRotateWizard() {
	m.rotate = &rotateState{}
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   "① 生成新密钥（轮换用）",
		okLabel: "生成并继续",
		fields: []dlgField{
			{label: "算法", value: []rune(string(keytool.Ed25519)), options: []string{string(keytool.Ed25519), string(keytool.ECDSA256), string(keytool.RSA2048)}, hint: "← → 切换"},
			{label: "注释", value: []rune(keytool.DefaultComment() + "-new")},
			{label: "保存路径", value: []rune("~/.ssh/id_ed25519_new"), hint: "私钥路径，公钥自动加 .pub"},
			{label: "私钥口令", secret: true, hint: "可留空；留空时验证阶段直接免口令登录"},
		},
		onOK: func(m *Model, v []string) {
			alg := keytool.Alg(strings.TrimSpace(v[0]))
			comment := strings.TrimSpace(v[1])
			p := strings.TrimSpace(v[2])
			pass := ""
			if len(v) > 3 {
				pass = v[3]
			}
			if p == "" {
				p = keytool.SuggestPath(alg)
			}
			m.dlg = &dlg{kind: dlgText, title: "正在生成新密钥…", body: []string{"", "  正在生成，RSA 可能需要几秒…"}}
			m.afterCmd = generateRotateKeyCmd(keytool.GenOptions{Alg: alg, Comment: comment, Path: p, Passphrase: pass})
		},
	}
}

func generateRotateKeyCmd(o keytool.GenOptions) tea.Cmd {
	return func() tea.Msg {
		info, err := keytool.Generate(o)
		if err != nil {
			return rotateGenDoneMsg{err: err.Error()}
		}
		return rotateGenDoneMsg{info: info}
	}
}

// ---------- ② 选择要替换的旧密钥 ----------

func (m *Model) openRotatePickOld(newKey keytool.KeyInfo) {
	if m.rotate == nil {
		m.rotate = &rotateState{}
	}
	m.rotate.newKey = newKey

	keys, err := keytool.ListLocal(keytool.DefaultDir())
	if err != nil {
		m.setMsg("扫描本机密钥失败：" + err.Error())
		return
	}
	var items []pickItem
	var filtered []keytool.KeyInfo
	for _, k := range keys {
		if k.Fingerprint == newKey.Fingerprint {
			continue // 跳过刚生成的新密钥
		}
		filtered = append(filtered, k)
		items = append(items, pickItem{
			Label: filepath.Base(k.PublicPath),
			Desc:  k.Alg + "  " + fallback(k.Comment, "无注释"),
		})
	}
	if len(items) == 0 {
		m.dlg = &dlg{kind: dlgText, title: "没有可替换的旧密钥",
			body: []string{"", "  本机只有刚生成的这一把密钥，无需轮换。"}}
		return
	}
	m.dlg = &dlg{
		kind:  dlgPick,
		title: "② 选择要被替换的旧密钥",
		items: items,
		onPick: func(m *Model, picked []int) {
			if len(picked) == 0 {
				return
			}
			m.openRotatePickTargets(filtered[picked[0]])
		},
	}
}

// ---------- ③ 选择目标主机 ----------

func (m *Model) openRotatePickTargets(oldKey keytool.KeyInfo) {
	m.rotate.oldKey = oldKey
	conns := m.st.GetConnections()
	if len(conns) == 0 {
		m.dlg = &dlg{kind: dlgText, title: "没有可用的服务器", body: []string{"", "  请先用 Ctrl+N 至少新建一条连接。"}}
		return
	}
	items := make([]pickItem, 0, len(conns))
	for _, c := range conns {
		desc := fmt.Sprintf("%s@%s:%d", fallback(c.User, "root"), c.Host, portOr22(c))
		items = append(items, pickItem{Label: fallback(c.Name, c.Host), Desc: desc})
	}
	m.dlg = &dlg{
		kind:    dlgPick,
		title:   "③ 选择要轮换的主机（空格多选 / a 全选）",
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
			m.openRotateConfirm(targets)
		},
	}
}

// ---------- ④ 确认并执行 ----------

func (m *Model) openRotateConfirm(targets []store.Connection) {
	if len(targets) == 0 {
		m.setMsg("未选择任何主机")
		return
	}
	m.rotate.targets = targets
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   fmt.Sprintf("④ 确认轮换（%d 台主机）", len(targets)),
		okLabel: "开始轮换",
		message: "新密钥 " + filepath.Base(m.rotate.newKey.PublicPath) + "\n旧密钥 " + filepath.Base(m.rotate.oldKey.PublicPath) +
			"\n\n流程：分发新密钥 → 用新私钥验证可登录 → 再移除旧密钥。\n任一主机验证失败将保留其旧密钥，不会把自己锁在门外。",
		fields: []dlgField{
			{label: "验证后移除旧密钥", value: []rune("true"), options: []string{"true", "false"}, hint: "← → 切换；false=只加不删"},
		},
		onOK: func(m *Model, v []string) {
			keep := false
			if len(v) > 0 && strings.EqualFold(strings.TrimSpace(v[0]), "false") {
				keep = true
			}
			st := m.rotate
			st.keepOld = keep
			m.runRotate(st)
		},
	}
}

func (m *Model) runRotate(st *rotateState) {
	m.dlg = &dlg{
		kind:  dlgText,
		title: fmt.Sprintf("正在轮换 %d 台主机…", len(st.targets)),
		body:  []string{"", "  正在分发新密钥、验证登录并移除旧密钥，请稍候…"},
	}
	newKey, oldKey, targets, keep := st.newKey, st.oldKey, st.targets, st.keepOld
	m.afterCmd = func() tea.Msg {
		results := keytool.RotateAll(targets, keytool.RotateOptions{
			NewPublic:  newKey.PublicKey,
			NewKeyPath: newKey.PrivatePath,
			OldPublic:  oldKey.PublicKey,
			KeepOld:    keep,
			Timeout:    30 * time.Second,
		})
		sort.SliceStable(results, func(a, b int) bool { return results[a].Name < results[b].Name })
		return rotateDoneMsg{results: results}
	}
}

// ---------- 结果 ----------

func (m *Model) showRotateResult(msg rotateDoneMsg) {
	ok, failed := keytool.RotateSummary(msg.results)
	var body []string
	body = append(body, "")
	body = append(body, fmt.Sprintf("  汇总      %d 台成功 · %d 台失败", ok, failed))
	body = append(body, "")
	for _, r := range msg.results {
		if r.Err != "" {
			body = append(body, fmt.Sprintf("  ✗ %s  %s", r.Name, r.Host))
			for _, line := range wrapText("    "+r.Err, max(30, m.width-20)) {
				body = append(body, line)
			}
		} else {
			body = append(body, fmt.Sprintf("  ✓ %s  %s  验证%s · 旧密钥%s",
				r.Name, r.Host, boolCN(r.Verified, "通过", "未通过"), boolCN(r.Removed, "已移除", "已保留")))
		}
		for _, p := range r.Phases {
			body = append(body, "      "+p)
		}
	}
	body = append(body, "")
	body = append(body, "  提示：失败的主机旧密钥仍在，可修复后重新轮换。")
	m.dlg = &dlg{kind: dlgText, title: "密钥轮换结果", body: body}
	if failed > 0 {
		_ = m.setMsg(fmt.Sprintf("轮换完成：%d 成功 / %d 失败", ok, failed))
	} else {
		_ = m.setMsg(fmt.Sprintf("轮换完成：%d 台全部成功", ok))
	}
	m.rotate = nil
}

func boolCN(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}
