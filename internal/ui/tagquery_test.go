package ui

import (
	"path/filepath"
	"testing"

	"sshtool/internal/keytool"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestKeyPickerTagQueryFilter 验证密钥选择器按标签组合查询过滤（与 / 或）。
func TestKeyPickerTagQueryFilter(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirAll(t, sshDir)

	k1, _ := keytool.Generate(keytool.GenOptions{Alg: keytool.Ed25519, Comment: "prod-ci", Path: filepath.Join(sshDir, "id_a")})
	k2, _ := keytool.Generate(keytool.GenOptions{Alg: keytool.Ed25519, Comment: "staging", Path: filepath.Join(sshDir, "id_b")})

	ix := loadKeyTags()
	ix.Set(k1.Fingerprint, k1.PublicPath, []string{"prod", "ci"}, "")
	ix.Set(k2.Fingerprint, k2.PublicPath, []string{"staging"}, "")
	if err := saveKeyTags(ix); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openPickKeyForTags()
	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应打开密钥选择器，实际 kind=%v", m.dlg.kind)
	}
	if m.dlg.filterIdxFn == nil {
		t.Fatal("密钥选择器应接入标签过滤")
	}

	// 空查询 → 全部可见
	m.dlg.filter = ""
	if got := len(m.visiblePick(m.dlg)); got != 2 {
		t.Fatalf("空查询应可见 2 项，实际 %d", got)
	}
	// 「与」：prod +ci → 只 k1
	m.dlg.filter = "prod +ci"
	if got := len(m.visiblePick(m.dlg)); got != 1 {
		t.Fatalf("prod +ci 应只剩 1 项，实际 %d", got)
	}
	// 「或」：prod,staging → 两项都命中
	m.dlg.filter = "prod,staging"
	if got := len(m.visiblePick(m.dlg)); got != 2 {
		t.Fatalf("prod,staging 应可见 2 项，实际 %d", got)
	}
	// 不存在的标签 → 无匹配
	m.dlg.filter = "nosuch"
	if got := len(m.visiblePick(m.dlg)); got != 0 {
		t.Fatalf("不存在的标签应无匹配，实际 %d", got)
	}
}

// TestSubstringFilterStillWorks 验证未接入自定义过滤的选择器仍按子串过滤。
func TestSubstringFilterStillWorks(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	st.AddConnection(store.Connection{Name: "web-01", Group: "prod", Host: "1.1.1.1", Port: 22, User: "root"})
	st.AddConnection(store.Connection{Name: "db-01", Group: "db", Host: "2.2.2.2", Port: 22, User: "root"})
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	m.openAuditPickTargets()
	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应打开选择器，实际 kind=%v", m.dlg.kind)
	}
	if m.dlg.filterIdxFn != nil {
		t.Fatal("主机选择器不应接入标签过滤")
	}
	m.dlg.filter = "web"
	if got := len(m.visiblePick(m.dlg)); got != 1 {
		t.Fatalf("子串过滤应剩 1 项，实际 %d", got)
	}
	m.dlg.filter = ""
	if got := len(m.visiblePick(m.dlg)); got != 2 {
		t.Fatalf("空过滤应可见 2 项，实际 %d", got)
	}
}
