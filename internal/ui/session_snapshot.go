package ui

import (
	"fmt"
	"os"
	"strings"

	"sshtool/internal/store"
)

// restoreDisabled 允许用环境变量关闭启动时自动复原会话：SSHTOOL_NO_RESTORE=1。
func restoreDisabled() bool {
	v := os.Getenv("SSHTOOL_NO_RESTORE")
	return v == "1" || strings.EqualFold(v, "true")
}

// saveSessionSnapshot 记录当前打开的 SSH 会话（按标签顺序）与活动会话。
// 本地 shell 无法跨进程复原，故不记录。
func (m *Model) saveSessionSnapshot() {
	ids := make([]string, 0, len(m.sessions))
	for _, s := range m.sessions {
		if isLocalShell(s) {
			continue
		}
		ids = append(ids, s.TabID())
	}
	snap := store.SessionSnapshot{OpenIDs: ids}
	// 活动会话若也是 SSH 会话则一并记录（本地 shell 作为活动会话时不记录）。
	for _, s := range m.sessions {
		if s.TabID() == m.activeID && !isLocalShell(s) {
			snap.ActiveID = m.activeID
			break
		}
	}
	_ = m.st.SaveSessions(snap)
}

// restorableSecretAvailable 判断连接能否在启动复原时「免询问」地直连：
//   - 密码认证：已保存密码且未勾选「每次询问」；
//   - 私钥认证：未勾选「每次询问口令」（未加密私钥可静默，加密私钥即使需要口令也交由后续提示）。
func restorableSecretAvailable(c store.Connection) bool {
	if c.AuthType == store.AuthKey {
		return !c.AskPassphrase
	}
	return c.Password != "" && !c.AskPassword
}

// restorePrompt 在启动时若存在会话快照，询问是否复原（仅复原能免询问直连的会话）。
func (m *Model) restorePrompt() {
	if restoreDisabled() {
		return
	}
	snap, err := m.st.LoadSessions()
	if err != nil || len(snap.OpenIDs) == 0 {
		return
	}
	byID := map[string]store.Connection{}
	for _, c := range m.st.GetConnections() {
		byID[c.ID] = c
	}

	var restorable []store.Connection
	skipped := 0
	for _, id := range snap.OpenIDs {
		c, ok := byID[id]
		if !ok {
			continue // 连接已被删除
		}
		if !restorableSecretAvailable(c) {
			skipped++
			continue
		}
		restorable = append(restorable, c)
	}
	if len(restorable) == 0 {
		return
	}

	names := make([]string, 0, len(restorable))
	for _, c := range restorable {
		names = append(names, fallback(c.Name, c.Host))
	}
	msg := fmt.Sprintf("上次退出时有 %d 个会话，是否恢复？\n  %s", len(restorable), strings.Join(names, "、"))
	if skipped > 0 {
		msg += fmt.Sprintf("\n（另有 %d 个需要输入密码/口令的会话已跳过，可手动连接）", skipped)
	}
	targets := restorable
	active := snap.ActiveID
	m.dlg = newConfirmDialog("恢复上次会话", msg, func(m *Model, _ []string) {
		for _, c := range targets {
			m.connectConn(c)
		}
		if active != "" {
			if s, ok := m.mgr.Get(active); ok {
				m.activeID = s.ID
				m.refreshSessions()
			}
		}
		_ = m.setMsg(fmt.Sprintf("已恢复 %d 个会话", len(targets)))
	})
	// 复原是「锦上添花」，默认不强制 Enter，允许 y。
	m.dlg.strictConfirm = false
}