package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/keytool"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestRotateWizardStartsWithForm 验证 ⑦ 打开生成新密钥表单。
func TestRotateWizardStartsWithForm(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openRotateWizard()
	if m.dlg == nil || m.dlg.kind != dlgForm {
		t.Fatalf("应打开生成表单，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.fields) != 4 {
		t.Fatalf("应有算法/注释/路径/口令四个字段，实际 %d", len(m.dlg.fields))
	}
	if m.rotate == nil {
		t.Fatal("应初始化轮换状态")
	}
}

// TestRotatePickOldExcludesNewKey 验证选旧密钥时排除刚生成的新密钥。
func TestRotatePickOldExcludesNewKey(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirAll(t, sshDir)

	oldKey, err := keytool.Generate(keytool.GenOptions{Alg: keytool.Ed25519, Comment: "old", Path: filepath.Join(sshDir, "id_old")})
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := keytool.Generate(keytool.GenOptions{Alg: keytool.Ed25519, Comment: "new", Path: filepath.Join(sshDir, "id_new")})
	if err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openRotateWizard()
	m.openRotatePickOld(newKey)
	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应打开选旧密钥，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.items) != 1 {
		t.Fatalf("应只列出旧密钥（排除新密钥），实际 %d", len(m.dlg.items))
	}
	if !strings.Contains(m.dlg.items[0].Desc, "old") {
		t.Fatalf("列表项应是旧密钥：%+v", m.dlg.items[0])
	}
	_ = oldKey
}

// TestRotateConfirmAndRun 验证确认步骤与后台命令派生。
func TestRotateConfirmAndRun(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirAll(t, sshDir)
	newKey, _ := keytool.Generate(keytool.GenOptions{Alg: keytool.Ed25519, Comment: "new", Path: filepath.Join(sshDir, "id_new")})
	oldKey, _ := keytool.Generate(keytool.GenOptions{Alg: keytool.Ed25519, Comment: "old", Path: filepath.Join(sshDir, "id_old")})

	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	c := st.AddConnection(store.Connection{Name: "web-01", Host: "1.1.1.1", Port: 22, User: "root", AuthType: store.AuthPassword, Password: "pw"})
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openRotateWizard()
	m.openRotatePickOld(newKey)
	m.openRotatePickTargets(oldKey)
	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应打开选主机，实际 kind=%v", m.dlg.kind)
	}
	m.dlg.onPick(&m, []int{0})

	if m.dlg == nil || m.dlg.kind != dlgForm {
		t.Fatalf("应打开确认表单，实际 kind=%v", m.dlg.kind)
	}
	m.dlg.onOK(&m, []string{"true"})
	if m.afterCmd == nil {
		t.Fatal("确认后应派生轮换命令")
	}
	if len(m.rotate.targets) != 1 || m.rotate.targets[0].ID != c.ID {
		t.Fatalf("目标主机未正确记录：%+v", m.rotate.targets)
	}
}

// TestShowRotateResult 验证轮换结果页展示各阶段。
func TestShowRotateResult(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.showRotateResult(rotateDoneMsg{results: []keytool.RotateResult{
		{Name: "ok-01", Host: "root@1.1.1.1:22", Verified: true, Removed: true,
			Phases: []string{"① 分发新密钥 ✓", "② 验证新密钥 ✓", "③ 移除旧密钥 ✓"}},
		{Name: "bad-01", Host: "root@2.2.2.2:22", Err: "分发新密钥失败：认证失败",
			Phases: []string{"① 分发新密钥 ✗ 认证失败"}},
	}})

	if m.dlg == nil || m.dlg.kind != dlgText {
		t.Fatalf("应展示结果页，实际 kind=%v", m.dlg.kind)
	}
	var joined string
	for _, line := range m.dlg.body {
		joined += line + "\n"
	}
	if !strings.Contains(joined, "1 台成功 · 1 台失败") {
		t.Fatalf("汇总应正确：\n%s", joined)
	}
	if !strings.Contains(joined, "验证通过") || !strings.Contains(joined, "旧密钥已移除") {
		t.Fatalf("应展示验证与移除状态：\n%s", joined)
	}
	for _, want := range []string{"① 分发新密钥 ✓", "③ 移除旧密钥 ✓", "认证失败"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("结果页应含 %q：\n%s", want, joined)
		}
	}
	if !strings.Contains(m.msg, "1 成功 / 1 失败") {
		t.Fatalf("状态栏应报告，实际：%q", m.msg)
	}
}
