package ui

import (
	"os"
	"path/filepath"
	"testing"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

func newGroupedModel(t *testing.T) *Model {
	t.Helper()
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{Name: "a", Host: "h1", Group: "prod"})
	st.AddConnection(store.Connection{Name: "b", Host: "h2", Group: "prod"})
	st.AddConnection(store.Connection{Name: "c", Host: "h3", Group: "dev"})
	st.AddConnection(store.Connection{Name: "d", Host: "h4"}) // 未分组
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	m.width, m.height = 120, 30
	return &m
}

func TestConnRowsGrouping(t *testing.T) {
	m := newGroupedModel(t)
	// 期望顺序：dev 组头、c、prod 组头、a、b、未分组 d
	want := []struct {
		isGroup bool
		group   string
		name    string
	}{
		{true, "dev", ""},
		{false, "dev", "c"},
		{true, "prod", ""},
		{false, "prod", "a"},
		{false, "prod", "b"},
		{false, "", "d"},
	}
	if len(m.connRows) != len(want) {
		t.Fatalf("显示行数应为 %d，得到 %d：%+v", len(want), len(m.connRows), m.connRows)
	}
	for i, w := range want {
		row := m.connRows[i]
		if row.isGroup != w.isGroup || row.group != w.group {
			t.Fatalf("第 %d 行不匹配：got %+v want %+v", i, row, w)
		}
		if !w.isGroup {
			if m.connList[row.idx].Name != w.name {
				t.Fatalf("第 %d 行连接应为 %s，得到 %s", i, w.name, m.connList[row.idx].Name)
			}
		}
	}
	if n := m.countOf(focusConn); n != len(want) {
		t.Fatalf("countOf(focusConn) 应为 %d，得到 %d", len(want), n)
	}
}

func TestConnGroupCollapse(t *testing.T) {
	m := newGroupedModel(t)
	m.toggleGroupAt("prod")
	// prod 收起后只剩：dev 头、c、prod 头、未分组 d
	groups := []string{}
	conns := []string{}
	for _, row := range m.connRows {
		if row.isGroup {
			groups = append(groups, row.group)
		} else {
			conns = append(conns, m.connList[row.idx].Name)
		}
	}
	if len(m.connRows) != 4 {
		t.Fatalf("收起 prod 后应剩 4 行，得到 %d：%+v", len(m.connRows), m.connRows)
	}
	if len(conns) != 2 || conns[0] != "c" || conns[1] != "d" {
		t.Fatalf("收起后可见连接应为 c、d，得到 %v", conns)
	}
	// 展开恢复
	m.toggleGroupAt("prod")
	if len(m.connRows) != 6 {
		t.Fatalf("展开后应恢复 6 行，得到 %d", len(m.connRows))
	}
}

func TestSelectedConnSkipsGroupHeader(t *testing.T) {
	m := newGroupedModel(t)
	m.connSel = 0 // dev 组头
	if _, ok := m.selectedConn(); ok {
		t.Fatal("选中分组头时不应返回连接")
	}
	m.connSel = 1 // c
	if c, ok := m.selectedConn(); !ok || c.Name != "c" {
		t.Fatalf("应选中连接 c，得到 %+v ok=%v", c, ok)
	}
}

func TestMetaImportExportSSHConfig(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ssh_config_in")
	if err := os.WriteFile(src, []byte("Host imp\n    HostName 1.2.3.4\n    User u\n    Port 2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	m.width, m.height = 120, 30

	// import 元命令
	if !m.runMetaCommand("import " + src) {
		t.Fatal("import 应被识别为元命令")
	}
	var found *store.Connection
	for _, c := range st.GetConnections() {
		if c.Name == "imp" {
			cc := c
			found = &cc
		}
	}
	if found == nil || found.Host != "1.2.3.4" || found.Port != 2222 {
		t.Fatalf("导入的连接不正确：%+v", found)
	}
	if found.Group != store.SSHConfigGroup {
		t.Fatalf("导入连接应归入 %s，得到 %q", store.SSHConfigGroup, found.Group)
	}

	// export 元命令
	out := filepath.Join(dir, "ssh_config_out")
	if !m.runMetaCommand("export ssh " + out) {
		t.Fatal("export 应被识别为元命令")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("导出文件不可读：%v", err)
	}
	if len(store.ParseSSHConfig(data)) == 0 {
		t.Fatalf("导出内容无法被重新解析：\n%s", data)
	}

	// 非元命令不应被拦截
	if m.runMetaCommand("ls -la") {
		t.Fatal("普通命令不应被当作元命令")
	}
}
