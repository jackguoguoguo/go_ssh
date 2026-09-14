package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/keytool"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

func testModelUI(t *testing.T, st *store.Store) Model {
	t.Helper()
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 34})
	return m
}

// runCmd 执行一个 tea.Cmd，并把 BatchMsg 展开成扁平的消息列表。
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestKeyManagerOpensAndCloses(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	m := testModelUI(t, st)

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if !strings.Contains(m.View(), "SSH 密钥管理") {
		t.Fatalf("Ctrl+G 未打开密钥管理:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(m.View(), "SSH 密钥管理") {
		t.Fatal("Esc 未关闭密钥管理")
	}
}

func TestKeyManagerGenerateFlow(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	m := testModelUI(t, st)

	// 菜单第一项就是「生成新密钥」
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	for _, want := range []string{"生成新密钥", "算法", "注释", "私钥路径", "私钥口令"} {
		if !strings.Contains(view, want) {
			t.Fatalf("生成表单缺少 %q:\n%s", want, view)
		}
	}

	// 切到第二个字段（注释）确认 Tab 可用
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.dlg == nil || m.dlg.focus != 1 {
		t.Fatalf("Tab 未切换字段，focus=%v", m.dlg.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
}

func TestKeyManagerGenerateActuallyCreatesKey(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	m := testModelUI(t, st)

	outDir := t.TempDir()

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// 把「私钥路径」改成临时目录下的路径：Tab 两次到路径字段后重填
	d := m.dlg
	pathField := 2
	d.focus = pathField
	d.fields[pathField].value = nil
	d.fields[pathField].pos = 0
	target := filepath.Join(outDir, "id_ed25519")
	for _, r := range target {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	var done genDoneMsg
	found := false
	for _, msg := range runCmd(cmd) {
		if g, ok := msg.(genDoneMsg); ok {
			done, found = g, true
		}
	}
	if !found {
		t.Fatal("确认生成后没有派生生成命令")
	}
	if done.err != nil {
		t.Fatalf("生成失败: %v", done.err)
	}
	if _, err := os.Stat(done.info.PrivatePath); err != nil {
		t.Fatalf("私钥未落盘: %v", err)
	}
	if _, err := os.Stat(done.info.PublicPath); err != nil {
		t.Fatalf("公钥未落盘: %v", err)
	}

	m.Update(done)
	view := m.View()
	for _, want := range []string{"密钥已生成", "指纹", done.info.Fingerprint} {
		if !strings.Contains(view, want) {
			t.Fatalf("结果页缺少 %q:\n%s", want, view)
		}
	}
}

func TestKeyManagerViewLocalKeys(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	m := testModelUI(t, st)

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.View(), "本机公钥") {
		t.Fatalf("未进入本机公钥页面:\n%s", m.View())
	}
}

func TestBatchPushAgainstServer(t *testing.T) {
	if testutil.POSIXShell() == "" {
		t.Skip("未找到 POSIX shell，跳过推送集成测试")
	}
	// 起两台独立的测试服务端，各自拥有独立的 HOME（也就有独立的 authorized_keys），
	// 这样才能真正覆盖「一次分发到多台主机」的场景。
	srvA := testutil.StartSSHServer(t)
	srvB := testutil.StartSSHServer(t)

	info, err := keytool.Generate(keytool.GenOptions{
		Alg: keytool.Ed25519, Comment: "ui@test",
		Path: filepath.Join(t.TempDir(), "id_ed25519"),
	})
	if err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{Name: "alpha", Host: srvA.Host, Port: srvA.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"})
	st.AddConnection(store.Connection{Name: "beta", Host: srvB.Host, Port: srvB.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"})

	conns := st.GetConnections()
	cmd := pushKeysCmd(info, conns, "~/.ssh/authorized_keys", "pass")
	msg := cmd()
	done, ok := msg.(pushDoneMsg)
	if !ok {
		t.Fatalf("非预期消息: %T", msg)
	}
	if len(done.results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(done.results))
	}
	for _, r := range done.results {
		if r.Err != "" {
			t.Fatalf("%s 推送失败: %s | %s", r.Name, r.Err, r.Message)
		}
		if !r.Appended {
			t.Fatalf("%s 首次推送应写入新密钥", r.Name)
		}
	}

	// 两台主机各自都应恰好有一行，且内容正确
	for _, srv := range []*testutil.SSHServer{srvA, srvB} {
		data, err := os.ReadFile(filepath.Join(srv.Home, ".ssh", "authorized_keys"))
		if err != nil {
			t.Fatalf("authorized_keys 未创建: %v", err)
		}
		if strings.TrimSpace(string(data)) != info.PublicKey {
			t.Fatalf("公钥内容不符:\n%s", data)
		}
	}

	m := testModelUI(t, st)
	m.Update(done)
	view := m.View()
	if !strings.Contains(view, "推送结果") || !strings.Contains(view, "2 台成功写入") {
		t.Fatalf("推送结果页异常:\n%s", view)
	}

	// 再推一次，两台都应识别为「已存在」
	msg2 := pushKeysCmd(info, conns, "~/.ssh/authorized_keys", "pass")()
	done2 := msg2.(pushDoneMsg)
	for _, r := range done2.results {
		if r.Appended || r.Err != "" {
			t.Fatalf("%s 重复推送应被去重: %+v", r.Name, r)
		}
	}
	m.Update(done2)
	if !strings.Contains(m.View(), "2 台已存在跳过") {
		t.Fatalf("去重结果未体现:\n%s", m.View())
	}
}

// TestPushDeduplicatesSameEndpoint 多条连接指向同一台主机时只应推送一次，
// 避免并发写入同一个 authorized_keys 产生重复行。
func TestPushDeduplicatesSameEndpoint(t *testing.T) {
	if testutil.POSIXShell() == "" {
		t.Skip("未找到 POSIX shell，跳过推送集成测试")
	}
	srv := testutil.StartSSHServer(t)

	info, err := keytool.Generate(keytool.GenOptions{
		Alg: keytool.Ed25519, Comment: "dup@test",
		Path: filepath.Join(t.TempDir(), "id_ed25519"),
	})
	if err != nil {
		t.Fatal(err)
	}

	base := store.Connection{Host: srv.Host, Port: srv.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"}
	a := base
	a.Name = "别名一"
	b := base
	b.Name = "别名二"
	c := base
	c.Name = "别名三" // 同一端点的第三个别名

	if got := len(dedupeTargets([]store.Connection{a, b, c})); got != 1 {
		t.Fatalf("去重后目标数 = %d，期望 1", got)
	}

	done := pushKeysCmd(info, []store.Connection{a, b, c}, "~/.ssh/authorized_keys", "pass")().(pushDoneMsg)
	if len(done.results) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(done.results))
	}
	if done.results[0].Err != "" {
		t.Fatalf("推送失败: %s", done.results[0].Err)
	}

	data, err := os.ReadFile(filepath.Join(srv.Home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; n != 1 {
		t.Fatalf("authorized_keys 有 %d 行，期望 1:\n%s", n, data)
	}
}

func TestKeyHelpers(t *testing.T) {
	if got := fallback("", "alt"); got != "alt" {
		t.Fatalf("fallback = %q", got)
	}
	if got := fallback(" x ", "alt"); got != " x " {
		t.Fatalf("fallback = %q", got)
	}
	if got := portOr22(store.Connection{}); got != 22 {
		t.Fatalf("portOr22 = %d", got)
	}
	if got := portOr22(store.Connection{Port: 2222}); got != 2222 {
		t.Fatalf("portOr22 = %d", got)
	}
	// 没有保存凭据的连接应被识别为「需要口令」
	if !secretMissing(store.Connection{AuthType: store.AuthPassword}) {
		t.Fatal("空密码连接应判定为缺少凭据")
	}
	if secretMissing(store.Connection{AuthType: store.AuthPassword, Password: "p"}) {
		t.Fatal("已保存密码不应判定为缺少凭据")
	}
	if !secretMissing(store.Connection{AuthType: store.AuthPassword, Password: "p", AskPassword: true}) {
		t.Fatal("AskPassword 应判定为缺少凭据")
	}
	if got := secretOf(store.Connection{AuthType: store.AuthKey, KeyPassphrase: "kp"}); got != "kp" {
		t.Fatalf("secretOf = %q", got)
	}
}

func TestPushUnreachableHostFailsFast(t *testing.T) {
	// 端口无服务时应快速返回错误，而不是永久阻塞
	c := store.Connection{Name: "down", Host: "127.0.0.1", Port: 1, User: "x", AuthType: store.AuthPassword, Password: "y"}
	start := time.Now()
	r := keytool.Push(keytool.PushOptions{Conn: c, Secret: "y", PublicKey: "ssh-ed25519 AAAA ignored", Timeout: 15 * time.Second})
	if r.Err == "" {
		t.Fatal("不可达主机应返回失败")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("不可达主机耗时过长: %s", elapsed)
	}
}
