package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/keytool"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestBackupMenuOpensBackupForm 验证 ⑤ → ① 打开备份表单（含加密口令字段）。
func TestBackupMenuOpensBackupForm(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openBackupMenu()
	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应打开子菜单，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.items) != 2 {
		t.Fatalf("菜单应有备份/恢复两项，实际 %d", len(m.dlg.items))
	}
	m.dlg.onPick(&m, []int{0})
	if m.dlg == nil || m.dlg.kind != dlgForm {
		t.Fatalf("应打开备份表单，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.fields) != 1 || !m.dlg.fields[0].secret {
		t.Fatalf("应有 1 个口令字段且为密文输入：%+v", m.dlg.fields)
	}
}

// TestBackupFormEmptyPassphraseRejected 验证空口令不派生命令。
func TestBackupFormEmptyPassphraseRejected(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openBackupForm()
	m.dlg.onOK(&m, []string{"   "})
	if m.afterCmd != nil {
		t.Fatal("空口令不应派生备份命令")
	}
	if !strings.Contains(m.msg, "口令") {
		t.Fatalf("应提示需要口令，实际：%q", m.msg)
	}
}

// TestBackupFlowCreatesArchive 端到端：表单 → 加密归档 → 结果对话框。
func TestBackupFlowCreatesArchive(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirAll(t, sshDir)
	if _, err := keytool.Generate(keytool.GenOptions{
		Alg: keytool.Ed25519, Comment: "bk@test", Path: filepath.Join(sshDir, "id_ed25519"),
	}); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openBackupForm()
	m.dlg.onOK(&m, []string{"pw123"})
	if m.afterCmd == nil {
		t.Fatal("应派生备份命令")
	}

	msg := m.afterCmd()
	bm, ok := msg.(backupDoneMsg)
	if !ok {
		t.Fatalf("消息类型错误：%T", msg)
	}
	if bm.err != "" {
		t.Fatalf("备份失败：%s", bm.err)
	}
	if bm.path == "" || !strings.HasSuffix(bm.path, ".enc") {
		t.Fatalf("应生成 .enc 归档：%q", bm.path)
	}

	m.showBackupResult(bm)
	if m.dlg == nil || m.dlg.kind != dlgText || m.dlg.title != "备份完成" {
		t.Fatalf("应展示备份完成，实际 %+v", m.dlg)
	}
}

// TestRestoreFlowRoundTrip 端到端：备份 → 列出 → 恢复（改名模式）。
func TestRestoreFlowRoundTrip(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirAll(t, sshDir)
	info, err := keytool.Generate(keytool.GenOptions{
		Alg: keytool.Ed25519, Comment: "rs@test", Path: filepath.Join(sshDir, "id_ed25519"),
	})
	if err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	// 先备份（派生后台命令后需真正执行）
	m.openBackupForm()
	m.dlg.onOK(&m, []string{"pw"})
	if m.afterCmd == nil {
		t.Fatal("应派生备份命令")
	}
	if bm := m.afterCmd().(backupDoneMsg); bm.err != "" {
		t.Fatalf("备份失败：%s", bm.err)
	}

	// 列出备份并选第一个
	m.openRestorePick()
	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应列出备份，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.items) == 0 {
		t.Fatal("应至少有一个备份")
	}
	m.dlg.onPick(&m, []int{0})
	if m.dlg == nil || m.dlg.kind != dlgForm {
		t.Fatalf("应打开恢复表单，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.fields) != 2 {
		t.Fatalf("恢复表单应有口令与同名策略两字段，实际 %d", len(m.dlg.fields))
	}

	m.dlg.onOK(&m, []string{"pw", "rename"})
	if m.afterCmd == nil {
		t.Fatal("应派生恢复命令")
	}
	rm := m.afterCmd().(restoreDoneMsg)
	if rm.err != "" {
		t.Fatalf("恢复失败：%s", rm.err)
	}
	if len(rm.written) == 0 {
		t.Fatal("恢复应写入文件")
	}

	m.showRestoreResult(rm)
	if m.dlg == nil || m.dlg.title != "恢复完成" {
		t.Fatalf("应展示恢复完成，实际 %+v", m.dlg)
	}

	// 恢复后的公钥指纹应与原密钥一致
	got, err := keytool.ReadPublic(filepath.Join(sshDir, "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != info.Fingerprint {
		t.Fatalf("恢复后指纹不符：%s != %s", got.Fingerprint, info.Fingerprint)
	}
}

// TestRestoreNoBackupsPrompts 验证无备份时给出提示而非报错。
func TestRestoreNoBackupsPrompts(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openRestorePick()
	if m.dlg == nil || m.dlg.kind != dlgText {
		t.Fatalf("无备份时应提示，实际 kind=%v", m.dlg.kind)
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}
