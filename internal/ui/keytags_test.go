package ui

import (
	"path/filepath"
	"testing"

	"sshtool/internal/keytool"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// withTempHome 把用户主目录指向临时目录，使 ~/.sshtool 下的文件落在沙箱内，返回该目录。
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestConnsMatchingTags(t *testing.T) {
	conns := []store.Connection{
		{Name: "a", Group: "prod"},
		{Name: "b", Group: "staging"},
		{Name: "c", Group: "prod"},
		{Name: "d", Group: ""},
	}
	got := connsMatchingTags(conns, []string{"#prod"})
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("按 prod 匹配应返回下标 [0 2]，实际 %v", got)
	}
	if connsMatchingTags(conns, nil) != nil {
		t.Fatal("空标签应返回 nil")
	}
}

func TestOpenEditKeyTagsSaves(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)
	m.width, m.height = 120, 30

	key := keytool.KeyInfo{
		Alg:         "ssh-ed25519",
		PublicPath:  "/tmp/id_ed25519.pub",
		PublicKey:   "ssh-ed25519 AAAA test",
		Fingerprint: "SHA256:TESTFP",
	}
	m.openEditKeyTags(key)
	if m.dlg == nil || m.dlg.kind != dlgForm {
		t.Fatalf("应打开表单，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.fields) != 2 {
		t.Fatalf("应有标签与备注两个字段，实际 %d", len(m.dlg.fields))
	}

	// 提交 #prod #ci 与备注
	m.dlg.onOK(&m, []string{"#prod #ci", "CI 部署专用"})

	ix := loadKeyTags()
	tags := ix.TagsOf("SHA256:TESTFP")
	if len(tags) != 2 || tags[0] != "prod" || tags[1] != "ci" {
		t.Fatalf("标签未持久化，实际 %v", tags)
	}
	if k, _ := ix.Get("SHA256:TESTFP"); k.Notes != "CI 部署专用" {
		t.Fatalf("备注未持久化，实际 %q", k.Notes)
	}
	if k, _ := ix.Get("SHA256:TESTFP"); k.Path != "/tmp/id_ed25519.pub" {
		t.Fatalf("路径未记录，实际 %q", k.Path)
	}
}

func TestPushTargetsPrecheckedByKeyTag(t *testing.T) {
	withTempHome(t)
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	st.AddConnection(store.Connection{Name: "web-01", Group: "prod", Host: "1.1.1.1", Port: 22, User: "root", AuthType: store.AuthPassword})
	st.AddConnection(store.Connection{Name: "db-01", Group: "prod", Host: "2.2.2.2", Port: 22, User: "root", AuthType: store.AuthPassword})
	st.AddConnection(store.Connection{Name: "lab-01", Group: "lab", Host: "3.3.3.3", Port: 22, User: "root", AuthType: store.AuthPassword})
	m := New(st, mgr)
	m.width, m.height = 120, 30

	// 先给密钥打上 #prod 标签
	ix := loadKeyTags()
	ix.Set("SHA256:FP2", "/tmp/id_ed25519.pub", []string{"prod"}, "")
	if err := saveKeyTags(ix); err != nil {
		t.Fatal(err)
	}

	key := keytool.KeyInfo{
		Alg:         "ssh-ed25519",
		PublicPath:  "/tmp/id_ed25519.pub",
		Fingerprint: "SHA256:FP2",
	}
	m.openPushPickTargets(key)

	if m.dlg == nil || m.dlg.kind != dlgPick {
		t.Fatalf("应打开选择器，实际 kind=%v", m.dlg.kind)
	}
	if len(m.dlg.items) != 3 {
		t.Fatalf("应列出 3 台，实际 %d", len(m.dlg.items))
	}

	// 预勾选应恰好命中「分组=prod」的连接（按 store 返回顺序对应下标）。
	conns := st.GetConnections()
	wantChecked := 0
	for i, c := range conns {
		isProd := c.Group == "prod"
		if m.dlg.checked[i] != isProd {
			t.Fatalf("连接 %s(group=%s) 勾选状态 = %v，期望 %v", c.Name, c.Group, m.dlg.checked[i], isProd)
		}
		if isProd {
			wantChecked++
		}
	}
	if wantChecked != 2 {
		t.Fatalf("应有 2 台 prod 主机，实际 %d", wantChecked)
	}
	if len(m.dlg.checked) != wantChecked {
		t.Fatalf("预勾选数量 = %d，期望 %d", len(m.dlg.checked), wantChecked)
	}
}