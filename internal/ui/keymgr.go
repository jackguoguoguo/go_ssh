package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/keytool"
	"sshtool/internal/store"
)

// ---------- 入口 ----------

// openKeyManager 打开 SSH 密钥管理入口（Ctrl+G）。
func (m *Model) openKeyManager() tea.Cmd {
	keys, err := keytool.ListLocal(keytool.DefaultDir())
	if err != nil {
		return m.setMsg("扫描本机密钥失败：" + err.Error())
	}

	m.dlg = &dlg{
		kind:  dlgPick,
		title: "SSH 密钥管理（本机密钥目录：" + shortHome(keytool.DefaultDir()) + "）",
		items: []pickItem{
			{Label: "① 生成新密钥", Desc: "ed25519 / ECDSA / RSA，可设注释与私钥口令"},
			{Label: "② 推送公钥到服务器", Desc: "写入远端 authorized_keys，幂等去重、自动修权限"},
			{Label: "③ 查看本机公钥", Desc: fmt.Sprintf("已发现 %d 个公钥", len(keys))},
			{Label: "④ 标签与备注", Desc: "给本机密钥打标签（#prod 等）与备忘，供推送时辨认"},
			{Label: "⑤ 备份与恢复", Desc: "加密归档 ~/.ssh 密钥与 config，或从备份还原"},
			{Label: "⑥ 远端公钥盘点", Desc: "拉取各主机 authorized_keys，与本机密钥比对（缺失 / 废弃）"},
			{Label: "⑦ 密钥轮换", Desc: "生成新密钥 → 分发 → 验证登录 → 移除旧密钥"},
		},
		onPick: func(m *Model, picked []int) {
			switch picked[0] {
			case 0:
				m.openGenKeyDialog()
			case 1:
				m.openPushPickKey()
			case 2:
				m.showLocalKeys()
			case 3:
				m.openPickKeyForTags()
			case 4:
				m.openBackupMenu()
			case 5:
				m.openAuditPickTargets()
			case 6:
				m.openRotateWizard()
			}
		},
	}
	return nil
}

// ---------- 查看本机公钥 ----------

func (m *Model) showLocalKeys() {
	keys, _ := keytool.ListLocal(keytool.DefaultDir())
	if len(keys) == 0 {
		m.dlg = &dlg{
			kind:  dlgText,
			title: "本机公钥",
			body:  []string{"", "  未找到任何公钥。可先用「生成新密钥」创建一对。"},
		}
		return
	}

	ix := loadKeyTags()
	var body []string
	for i, k := range keys {
		priv := "（无对应私钥）"
		if k.HasPrivate {
			priv = "已配对私钥"
		}
		tags := ix.TagsOf(k.Fingerprint)
		comment := k.Comment
		if comment == "" {
			comment = "（无）"
		}
		body = append(body,
			fmt.Sprintf("  %d. %s", i+1, filepath.Base(k.PublicPath)),
			fmt.Sprintf("     算法  %s", k.Alg),
			fmt.Sprintf("     指纹  %s", k.Fingerprint),
			fmt.Sprintf("     标签  %s", tagsForDisplay(tags)),
			fmt.Sprintf("     注释  %s · %s", comment, priv),
			fmt.Sprintf("     公钥  %s", k.PublicPath),
			"",
		)
	}
	body = append(body, "  提示：选②可把其中任意一个推到服务器的 authorized_keys；选④可给密钥打标签。")
	m.dlg = &dlg{kind: dlgText, title: "本机公钥", body: body}
}

// ---------- 生成密钥 ----------

// openGenKeyDialog 打开密钥生成表单。
func (m *Model) openGenKeyDialog() {
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   "生成新密钥",
		okLabel: "生成",
		fields: []dlgField{
			{label: "算法", value: []rune(keytool.AlgOptions[0]), options: keytool.AlgOptions, hint: keytool.AlgDesc(keytool.Ed25519)},
			{label: "注释", value: []rune(keytool.DefaultComment()), hint: "通常填 user@host，便于区分用途"},
			{label: "私钥路径", value: []rune(shortHome(keytool.SuggestPath(keytool.Ed25519)))},
			{label: "私钥口令", value: nil, secret: true, hint: "留空表示不加密（推荐至少为空时不放服务器）"},
		},
		onOK: func(m *Model, v []string) {
			if len(v) < 4 {
				return
			}
			alg := keytool.Alg(v[0])
			opts := keytool.GenOptions{
				Alg:        alg,
				Comment:    v[1],
				Path:       keytool.ExpandHome(v[2]),
				Passphrase: v[3],
			}
			// 算法切换后如果用户没改过路径，跟着换默认名
			if strings.TrimSpace(v[2]) == "" {
				opts.Path = keytool.SuggestPath(alg)
			}
			m.dlg = &dlg{
				kind:  dlgText,
				title: "正在生成密钥…",
				body:  []string{"", "  " + string(alg) + " 正在生成，RSA 可能需要几秒到十几秒…"},
			}
			m.afterCmd = generateKeyCmd(opts)
		},
	}
}

// generateKeyCmd 在后台生成密钥，避免阻塞 UI。
func generateKeyCmd(o keytool.GenOptions) tea.Cmd {
	return func() tea.Msg {
		info, err := keytool.Generate(o)
		return genDoneMsg{info: info, err: err}
	}
}

// showGenResult 展示生成结果（含可直接复制的公钥内容）。
func (m *Model) showGenResult(info keytool.KeyInfo, err error) {
	if err != nil {
		m.dlg = &dlg{
			kind:  dlgText,
			title: "生成失败",
			body:  append([]string{""}, wrapText("  "+err.Error(), max(30, m.width-16))...),
		}
		return
	}

	var body []string
	body = append(body, "")
	body = append(body, "  私钥  "+shortHome(info.PrivatePath))
	body = append(body, "  公钥  "+shortHome(info.PublicPath))
	body = append(body, "  算法  "+info.Alg)
	body = append(body, "  指纹  "+info.Fingerprint)
	body = append(body, "")
	body = append(body, "  公钥内容（可整行复制进 authorized_keys）：")
	width := max(24, minInt(m.width-16, 68))
	for _, line := range wrapText(info.PublicKey, width) {
		body = append(body, "    "+line)
	}
	body = append(body, "")
	body = append(body, "  下一步：按 Ctrl+G → ② 把它推到服务器上。")

	m.dlg = &dlg{kind: dlgText, title: "密钥已生成", body: body}
	_ = m.setMsg("密钥已生成：" + filepath.Base(info.PublicPath))
}

// ---------- 推送公钥 ----------

// openPushPickKey 选择要推送的本机公钥。
func (m *Model) openPushPickKey() {
	keys, err := keytool.ListLocal(keytool.DefaultDir())
	if err != nil {
		m.setMsg("扫描本机密钥失败：" + err.Error())
		return
	}

	ix := loadKeyTags()
	items := make([]pickItem, 0, len(keys)+1)
	for _, k := range keys {
		desc := k.Alg + "  " + fallback(k.Comment, "无注释")
		if tags := tagString(ix.TagsOf(k.Fingerprint)); tags != "" {
			desc += "  " + tags
		}
		if k.HasPrivate {
			desc += "  · 已配对私钥"
		}
		items = append(items, pickItem{Label: filepath.Base(k.PublicPath), Desc: desc})
	}
	items = append(items, pickItem{Label: "手动输入公钥路径", Desc: "不在默认目录时使用"})

	m.dlg = &dlg{
		kind:  dlgPick,
		title: "选择要推送的本机公钥",
		items: items,
		onPick: func(m *Model, picked []int) {
			idx := picked[0]
			if idx >= len(keys) {
				m.openPushManualPath()
				return
			}
			m.openPushPickTargets(keys[idx])
		},
	}
}

// openPushManualPath 手动填入公钥路径。
func (m *Model) openPushManualPath() {
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   "手动指定公钥",
		okLabel: "下一步",
		fields: []dlgField{
			{label: "公钥路径", value: []rune("~/.ssh/id_ed25519.pub"), hint: "支持 ~ 开头的路径"},
		},
		onOK: func(m *Model, v []string) {
			if len(v) < 1 {
				return
			}
			info, err := keytool.ReadPublic(v[0])
			if err != nil {
				m.dlg = &dlg{kind: dlgText, title: "读取失败", body: append([]string{""}, wrapText("  "+err.Error(), max(30, m.width-16))...)}
				return
			}
			m.openPushPickTargets(info)
		},
	}
}

// openPushPickTargets 多选目标服务器（顺带实现批量分发）。
func (m *Model) openPushPickTargets(key keytool.KeyInfo) {
	conns := m.st.GetConnections()
	if len(conns) == 0 {
		m.dlg = &dlg{kind: dlgText, title: "没有可用的服务器", body: []string{"", "  请先用 Ctrl+N 至少新建一条连接。"}}
		return
	}

	keyTags := tagString(loadKeyTags().TagsOf(key.Fingerprint))
	items := make([]pickItem, 0, len(conns))
	needsSecret := false
	for _, c := range conns {
		user := c.User
		if user == "" {
			user = "root"
		}
		desc := fmt.Sprintf("%s@%s:%d", user, c.Host, portOr22(c))
		// 连接的分组作为「标签」维度展示，便于用过滤框按 #分组 快速筛选目标。
		if c.Group != "" {
			desc += "  #" + c.Group
		}
		items = append(items, pickItem{Label: c.Name, Desc: desc})
		if secretMissing(c) {
			needsSecret = true
		}
	}

	title := "选择目标服务器（可多选，空格勾选 / a 全选）"
	if keyTags != "" {
		title = "选择目标服务器 · 密钥标签 " + keyTags + "（可多选，空格勾选 / a 全选）"
	}
	// 按密钥标签预勾选「分组命中」的目标，实现「把带 #prod 的密钥推给所有 prod 分组主机」。
	checked := map[int]bool{}
	for _, i := range connsMatchingTags(conns, loadKeyTags().TagsOf(key.Fingerprint)) {
		checked[i] = true
	}
	if len(checked) > 0 {
		title = fmt.Sprintf("%s · 已按标签预选 %d 台", title, len(checked))
	}
	m.dlg = &dlg{
		kind:    dlgPick,
		title:   title,
		items:   items,
		multi:   true,
		checked: checked,
		onPick: func(m *Model, picked []int) {
			selected := make([]store.Connection, 0, len(picked))
			for _, i := range picked {
				selected = append(selected, conns[i])
			}
			if needsSecret {
				// 批量场景下常为同一套口令，统一问一次即可
				m.dlg = newSecretDialog("批量推送需要口令",
					fmt.Sprintf("选中的 %d 台主机中有需要密码/私钥口令的，请输入（其余主机沿用已保存凭据）", len(selected)),
					func(m *Model, v []string) {
						pw := ""
						if len(v) > 0 {
							pw = v[0]
						}
						m.openPushRemotePath(key, selected, pw)
					})
				return
			}
			m.openPushRemotePath(key, selected, "")
		},
	}
}

// openPushRemotePath 确认远端 authorized_keys 路径。
func (m *Model) openPushRemotePath(key keytool.KeyInfo, conns []store.Connection, secret string) {
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   fmt.Sprintf("推送到 %d 台主机", len(conns)),
		okLabel: "开始推送",
		message: "公钥：" + fallback(key.Comment, filepath.Base(key.PublicPath)) + "\n指纹：" + key.Fingerprint,
		fields: []dlgField{
			{label: "远端路径", value: []rune(keytool.DefaultRemotePath), hint: "留空用默认值；支持自定义 authorized_keys 位置"},
		},
		onOK: func(m *Model, v []string) {
			path := keytool.DefaultRemotePath
			if len(v) > 0 && strings.TrimSpace(v[0]) != "" {
				path = strings.TrimSpace(v[0])
			}
			m.dlg = &dlg{
				kind:  dlgText,
				title: "正在推送…",
				body:  []string{"", fmt.Sprintf("  正在向 %d 台主机写入公钥，请稍候…", len(conns))},
			}
			m.afterCmd = pushKeysCmd(key, conns, path, secret)
		},
	}
}

// endpointOf 计算连接的目标端点，用于去重。
func endpointOf(c store.Connection) string {
	user := c.User
	if user == "" {
		user = "root"
	}
	return fmt.Sprintf("%s@%s:%d", user, c.Host, portOr22(c))
}

// dedupeTargets 按端点去重：多条连接指向同一台主机时只推送一次。
// 这既避免无意义的重复连接，也消除了「并发写同一个 authorized_keys 产生重复行」的竞态。
func dedupeTargets(conns []store.Connection) []store.Connection {
	seen := make(map[string]bool, len(conns))
	out := make([]store.Connection, 0, len(conns))
	for _, c := range conns {
		k := endpointOf(c)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

// pushKeysCmd 并发向多台主机分发公钥。
func pushKeysCmd(key keytool.KeyInfo, conns []store.Connection, remotePath, sharedSecret string) tea.Cmd {
	targets := dedupeTargets(conns)
	return func() tea.Msg {
		conns := targets
		results := make([]keytool.PushResult, len(conns))
		var wg sync.WaitGroup
		for i := range conns {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := conns[i]
				secret := sharedSecret
				if !secretMissing(c) {
					secret = secretOf(c)
				}
				// 快照式保险：先回传远端 authorized_keys 存档，再写入。
				snapshotRemoteKeys(c, secret, remotePath)
				results[i] = keytool.Push(keytool.PushOptions{
					Conn:       c,
					Secret:     secret,
					PublicKey:  key.PublicKey,
					RemotePath: remotePath,
					Timeout:    30 * time.Second,
				})
			}(i)
		}
		wg.Wait()
		sort.SliceStable(results, func(a, b int) bool { return results[a].Name < results[b].Name })
		return pushDoneMsg{results: results, remotePath: remotePath}
	}
}

// showPushResult 展示批量推送结果汇总。
func (m *Model) showPushResult(results []keytool.PushResult, remotePath string) {
	var body []string
	body = append(body, "")
	body = append(body, "  远端路径  "+remotePath)

	ok, dup, fail := 0, 0, 0
	for _, r := range results {
		switch {
		case r.Err != "":
			fail++
		case r.Appended:
			ok++
		default:
			dup++
		}
	}
	body = append(body, fmt.Sprintf("  汇总      %d 台成功写入 · %d 台已存在跳过 · %d 台失败", ok, dup, fail))
	body = append(body, "")

	for _, r := range results {
		status := "✓ 已写入"
		switch {
		case r.Err != "":
			status = "✗ 失败"
		case !r.Appended:
			status = "· 已存在"
		}
		body = append(body, fmt.Sprintf("  %s  %-16s %s", status, r.Name, r.Host))
		if r.Err != "" {
			for _, line := range wrapText("原因: "+r.Err, max(30, m.width-20)) {
				body = append(body, "           "+line)
			}
			continue
		}
		body = append(body, fmt.Sprintf("            当前 authorized_keys 共 %d 条", r.Total))
	}
	body = append(body, "")
	body = append(body, "  提示：结果里「失败」多为认证失败或网络不通；同一公钥重复推送不会写入多行。")

	m.dlg = &dlg{kind: dlgText, title: "推送结果", body: body}

	if fail > 0 {
		_ = m.setMsg(fmt.Sprintf("推送完成：%d 成功 / %d 失败", ok+dup, fail))
	} else {
		_ = m.setMsg(fmt.Sprintf("推送完成：%d 台写入，%d 台已存在", ok, dup))
	}
}

// ---------- 工具 ----------

// secretMissing 判断连接是否缺少可直接使用的凭据。
func secretMissing(c store.Connection) bool {
	if c.AuthType == store.AuthKey {
		return c.AskPassphrase
	}
	return c.Password == "" || c.AskPassword
}

// secretOf 取出连接已保存的凭据。
func secretOf(c store.Connection) string {
	if c.AuthType == store.AuthKey {
		return c.KeyPassphrase
	}
	return c.Password
}

func portOr22(c store.Connection) int {
	if c.Port > 0 {
		return c.Port
	}
	return store.DefaultSSHPort
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// shortHome 把用户家目录替换成 ~，让长路径在窄面板里也能看清。
func shortHome(p string) string {
	home := keytool.DefaultDir()
	parent := filepath.Dir(home)
	if parent == "" || parent == "." {
		return p
	}
	if strings.HasPrefix(p, parent) {
		return "~" + strings.TrimPrefix(p, parent)
	}
	return p
}
