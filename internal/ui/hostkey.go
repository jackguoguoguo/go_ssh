package ui

import (
	"errors"
	"strings"

	"sshtool/internal/remotessh"
)

// hostKeyAsk 是一次待用户确认的主机指纹询问。
//
// 握手在后台协程里完成，无法就地弹窗，因此 remotessh 把「未知主机 / 指纹变更」
// 转成 EventNeedHostKey 事件；UI 收到后入队并逐个弹框。
// 队列存在的意义：多个会话可能同时首次连接不同主机，而对话框是单例的，
// 不排队会直接覆盖掉前一次询问。
type hostKeyAsk struct {
	sessionID string
	err       *remotessh.HostKeyError
}

// enqueueHostKey 收到指纹事件后入队并尝试弹框。
func (m *Model) enqueueHostKey(sessionID string, err error) {
	var hke *remotessh.HostKeyError
	if !errors.As(err, &hke) {
		return
	}
	m.hostKeyQueue = append(m.hostKeyQueue, hostKeyAsk{sessionID: sessionID, err: hke})
	m.showNextHostKeyPrompt()
}

// showNextHostKeyPrompt 在对话框空闲时弹出队首的询问。
func (m *Model) showNextHostKeyPrompt() {
	if m.dlg != nil || len(m.hostKeyQueue) == 0 {
		return
	}
	ask := m.hostKeyQueue[0]
	hke := ask.err

	who := hke.Addr
	if c, ok := m.st.Connection(ask.sessionID); ok && c.Host != "" {
		who = c.User + "@" + c.Host + " (" + hke.Addr + ")"
	}

	d := newConfirmDialog("主机指纹确认", hostKeyMessage(hke, who), func(m *Model, _ []string) {
		m.trustAndReopen(ask)
		m.finishHostKeyPrompt()
	})
	d.strictConfirm = true
	if hke.Changed {
		// 指纹变更一律不提供「覆盖」按钮：程序无法区分主机重装与中间人攻击，
		// 这个决定只能由人来做（先 ssh-keygen -R 再重连）。
		d.okLabel = "关闭"
		d.onOK = func(m *Model, _ []string) {
			m.setMsg("已拒绝连接 " + hke.Addr + "：指纹与 known_hosts 不一致")
		}
	} else {
		d.okLabel = "信任并写入 known_hosts"
	}
	d.onCancel = func(m *Model) {
		m.setMsg("未信任 " + hke.Addr + "，连接已中止")
		m.finishHostKeyPrompt()
	}
	m.dlg = d
}

// finishHostKeyPrompt 弹出队首并继续处理下一个（无则什么都不做）。
func (m *Model) finishHostKeyPrompt() {
	if len(m.hostKeyQueue) == 0 {
		return
	}
	m.hostKeyQueue = m.hostKeyQueue[1:]
	m.showNextHostKeyPrompt()
}

// trustAndReopen 写入 known_hosts 后用原口令重连，避免用户重复输入密码。
func (m *Model) trustAndReopen(ask hostKeyAsk) {
	if err := remotessh.TrustHost(ask.err.Addr, ask.err.Key); err != nil {
		m.setMsg("写入 known_hosts 失败：" + err.Error())
		return
	}
	c, ok := m.st.Connection(ask.sessionID)
	if !ok {
		m.setMsg("已信任 " + ask.err.Addr + "，但找不到对应连接，请手动重连")
		return
	}
	secret := ""
	if s, _ := m.mgr.Get(ask.sessionID); s != nil {
		secret = s.Secret()
	}
	l := m.computeLayout()
	ns := m.mgr.Reopen(c, secret, l.termCol, l.termRow, m.st.GetSettings().Scrollback)
	m.activeID = ns.ID
	m.refreshSessions()
	m.setMsg("已信任 " + ask.err.Addr + "，正在重连")
}

// hostKeyMessage 生成指纹确认/拒绝的说明文案。
func hostKeyMessage(hke *remotessh.HostKeyError, who string) string {
	var b strings.Builder
	if hke.Changed {
		b.WriteString("拒绝连接 " + who + "：本次指纹与 known_hosts 中已保存的记录不一致。\n")
		b.WriteString("算法 " + hke.Type + "\n")
		b.WriteString("本次指纹 " + hke.Fingerprint + "\n\n")
		b.WriteString("指纹变化既可能是主机重装，也可能是中间人攻击，程序无法区分，因此不会自动覆盖。\n")
		b.WriteString("若你确认主机确实换过密钥，请先执行：\n")
		b.WriteString("  ssh-keygen -R " + sshKeygenHost(hke.Addr) + "\n")
		b.WriteString("再重新连接。")
		return b.String()
	}
	b.WriteString("无法确认 " + who + " 的身份：known_hosts 中没有该主机的记录。\n")
	b.WriteString("算法 " + hke.Type + "\n")
	b.WriteString("指纹 " + hke.Fingerprint + "\n\n")
	b.WriteString("请与服务器管理员提供的指纹核对一致后再信任。\n")
	b.WriteString("信任后会写入 ~/.ssh/known_hosts，以后连接同一主机不再询问。")
	return b.String()
}

// sshKeygenHost 去掉端口并去掉 IPv6 的方括号，用于 ssh-keygen -R 提示。
func sshKeygenHost(addr string) string {
	s := addr
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return s[1:i]
		}
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		return s[:i]
	}
	return s
}
