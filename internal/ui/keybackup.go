package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/keytool"
	"sshtool/internal/store"
)

// backupDoneMsg / restoreDoneMsg 备份与恢复的完成通知。
type backupDoneMsg struct {
	path string
	err  string
}

type restoreDoneMsg struct {
	archive string
	written []string
	err     string
}

// ---------- 入口 ----------

// openBackupMenu 打开「密钥备份与恢复」子菜单（Ctrl+G → ⑤）。
func (m *Model) openBackupMenu() {
	m.dlg = &dlg{
		kind:  dlgPick,
		title: "密钥备份与恢复",
		items: []pickItem{
			{Label: "① 备份本机密钥", Desc: "id_*（私钥+公钥）/ config / known_hosts → 加密归档"},
			{Label: "② 从备份恢复", Desc: "解密归档并还原，可选覆盖或改名导入"},
		},
		onPick: func(m *Model, picked []int) {
			if len(picked) == 0 {
				return
			}
			switch picked[0] {
			case 0:
				m.openBackupForm()
			case 1:
				m.openRestorePick()
			}
		},
	}
}

// ---------- 备份 ----------

func (m *Model) openBackupForm() {
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   "备份本机密钥",
		okLabel: "开始备份",
		message: "将打包 ~/.ssh 下的 id_*（私钥与公钥）、config、known_hosts，\n用口令派生的密钥（scrypt + AES-GCM）加密后存入 ~/.sshtool/backups/",
		fields: []dlgField{
			{label: "加密口令", secret: true, hint: "请牢记：恢复时必须输入同一口令"},
		},
		onOK: func(m *Model, v []string) {
			pw := ""
			if len(v) > 0 {
				pw = strings.TrimSpace(v[0])
			}
			if pw == "" {
				m.setMsg("备份需要加密口令")
				return
			}
			m.dlg = &dlg{kind: dlgText, title: "正在备份…", body: []string{"", "  正在打包并加密，请稍候…"}}
			m.afterCmd = backupKeysCmd(pw)
		},
	}
}

func backupKeysCmd(pw string) tea.Cmd {
	return func() tea.Msg {
		path, err := keytool.Backup(keytool.DefaultDir(), pw)
		if err != nil {
			return backupDoneMsg{err: err.Error()}
		}
		return backupDoneMsg{path: path}
	}
}

func (m *Model) showBackupResult(msg backupDoneMsg) {
	if msg.err != "" {
		m.dlg = &dlg{kind: dlgText, title: "备份失败",
			body: append([]string{""}, wrapText("  "+msg.err, max(30, m.width-16))...)}
		_ = m.setMsg("备份失败：" + msg.err)
		return
	}
	body := []string{
		"",
		"  已生成加密备份：",
		"  " + msg.path,
		"",
		"  提示：恢复时输入同一口令；请把备份与口令分开保管。",
	}
	m.dlg = &dlg{kind: dlgText, title: "备份完成", body: body}
	_ = m.setMsg("备份完成：" + filepath.Base(msg.path))
}

// ---------- 恢复 ----------

func (m *Model) openRestorePick() {
	list, err := keytool.ListBackups()
	if err != nil || len(list) == 0 {
		m.dlg = &dlg{kind: dlgText, title: "没有可用的备份",
			body: []string{"", "  尚未创建过备份。可先选「① 备份本机密钥」。"}}
		return
	}
	items := make([]pickItem, 0, len(list))
	for _, b := range list {
		items = append(items, pickItem{
			Label: b.When.Format("2006-01-02 15:04:05"),
			Desc:  fmt.Sprintf("%.1f KB", float64(b.Size)/1024),
		})
	}
	m.dlg = &dlg{
		kind:  dlgPick,
		title: "选择要恢复的备份",
		items: items,
		onPick: func(m *Model, picked []int) {
			if len(picked) == 0 {
				return
			}
			m.openRestoreForm(list[picked[0]])
		},
	}
}

func (m *Model) openRestoreForm(b keytool.BackupInfo) {
	m.dlg = &dlg{
		kind:    dlgForm,
		title:   "恢复备份 · " + b.When.Format("2006-01-02 15:04"),
		okLabel: "开始恢复",
		message: "备份文件：" + b.Path,
		fields: []dlgField{
			{label: "解密口令", secret: true},
			{label: "同名文件", value: []rune("rename"), options: []string{"rename", "overwrite"}, hint: "← → 切换：rename=改名导入，overwrite=覆盖"},
		},
		onOK: func(m *Model, v []string) {
			pw := ""
			if len(v) > 0 {
				pw = strings.TrimSpace(v[0])
			}
			if pw == "" {
				m.setMsg("需要解密口令")
				return
			}
			mode := keytool.RestoreRename
			if len(v) > 1 && strings.EqualFold(strings.TrimSpace(v[1]), "overwrite") {
				mode = keytool.RestoreOverwrite
			}
			m.dlg = &dlg{kind: dlgText, title: "正在恢复…", body: []string{"", "  正在解密并校验指纹，请稍候…"}}
			m.afterCmd = restoreKeysCmd(b.Path, pw, mode)
		},
	}
}

func restoreKeysCmd(archive, pw string, mode keytool.RestoreMode) tea.Cmd {
	return func() tea.Msg {
		written, err := keytool.Restore(archive, pw, keytool.DefaultDir(), mode)
		if err != nil {
			return restoreDoneMsg{archive: archive, err: err.Error()}
		}
		return restoreDoneMsg{archive: archive, written: written}
	}
}

func (m *Model) showRestoreResult(msg restoreDoneMsg) {
	if msg.err != "" {
		m.dlg = &dlg{kind: dlgText, title: "恢复失败",
			body: append([]string{""}, wrapText("  "+msg.err, max(30, m.width-16))...)}
		_ = m.setMsg("恢复失败：" + msg.err)
		return
	}
	body := []string{"", "  已还原到 ~/.ssh："}
	for _, w := range msg.written {
		body = append(body, "    "+w)
	}
	body = append(body, "")
	body = append(body, "  提示：恢复后请确认远端 authorized_keys 仍包含对应公钥（可用 Ctrl+G ②重新推送）。")
	m.dlg = &dlg{kind: dlgText, title: "恢复完成", body: body}
	_ = m.setMsg(fmt.Sprintf("恢复完成：%d 个文件", len(msg.written)))
}

// ---------- 推送前的远端快照（快照式保险）----------

// snapshotRemoteKeys 在推送前回传远端 authorized_keys 存档，便于误删后回滚。
// 失败不影响推送本身（快照只是保险）。
func snapshotRemoteKeys(c store.Connection, secret, remotePath string) {
	out, err := keytool.SnapshotRemoteAuthorizedKeys(c, secret, remotePath, 20*time.Second)
	if err != nil {
		return
	}
	_, _ = keytool.SaveRemoteSnapshot(fmt.Sprintf("%s@%s:%d", c.User, c.Host, c.Port), out)
}
