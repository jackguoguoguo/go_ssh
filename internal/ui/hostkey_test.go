package ui

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	sshx "golang.org/x/crypto/ssh"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// newTestModel 构造一个可用于按键/状态断言的 Model。
func newTestModel(t *testing.T) (*Model, *store.Store, *remotessh.Manager) {
	t.Helper()
	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "config.json"))
	st.AddConnection(store.Connection{
		Name: "c1", Host: "h1", Port: 22, User: "u",
		AuthType: store.AuthPassword, Password: "p",
	})
	mgr := remotessh.NewManager()
	m := New(st, mgr)
	m.width, m.height = 120, 30
	return &m, st, mgr
}

func testPubKey(t *testing.T) sshx.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := sshx.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestHostKeyPromptQueueAndStrictConfirm(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	t.Setenv(remotessh.KnownHostsEnv, kh)

	m, st, _ := newTestModel(t)
	conn := st.GetConnections()[0]

	k1 := testPubKey(t)
	k2 := testPubKey(t)

	m.enqueueHostKey(conn.ID, &remotessh.HostKeyError{
		Addr: "h1:22", Type: "ssh-ed25519", Fingerprint: "SHA256:aaa", Key: k1,
	})
	if m.dlg == nil {
		t.Fatal("收到指纹事件后应立即弹框")
	}
	if m.dlg.kind != dlgConfirm {
		t.Errorf("对话框类型 = %d，期望 dlgConfirm", m.dlg.kind)
	}
	if !m.dlg.strictConfirm {
		t.Error("指纹确认框必须是 strictConfirm（只认 Enter）")
	}
	if !strings.Contains(m.dlg.message, "SHA256:aaa") {
		t.Errorf("确认框应展示指纹，实际: %s", m.dlg.message)
	}

	// 第二台主机同时询问：应排队而不是覆盖
	m.enqueueHostKey(conn.ID, &remotessh.HostKeyError{
		Addr: "h2:22", Type: "ssh-ed25519", Fingerprint: "SHA256:bbb", Key: k2,
	})
	if len(m.hostKeyQueue) != 2 {
		t.Fatalf("队列长度 = %d，期望 2（并发询问不能丢单）", len(m.hostKeyQueue))
	}

	// 顺手敲 y 不应通过
	m.handleDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.dlg == nil {
		t.Fatal("strictConfirm 的确认框不应被 y 键确认")
	}

	// Enter 确认：写入 known_hosts 并推进队列
	m.handleDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.hostKeyQueue) != 1 {
		t.Fatalf("确认后队列应出队，实际 %d", len(m.hostKeyQueue))
	}
	if m.dlg == nil {
		t.Fatal("确认后应继续弹出下一个询问")
	}
	if !strings.Contains(m.dlg.message, "SHA256:bbb") {
		t.Errorf("第二个确认框内容不对: %s", m.dlg.message)
	}

	data, err := os.ReadFile(kh)
	if err != nil {
		t.Fatalf("known_hosts 未创建: %v", err)
	}
	if !strings.Contains(string(data), base64.StdEncoding.EncodeToString(k1.Marshal())) {
		t.Errorf("确认后应写入第一台主机的公钥，实际内容:\n%s", data)
	}
}

func TestHostKeyPromptCancelAdvancesQueue(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	t.Setenv(remotessh.KnownHostsEnv, kh)

	m, st, _ := newTestModel(t)
	conn := st.GetConnections()[0]

	m.enqueueHostKey(conn.ID, &remotessh.HostKeyError{Addr: "h1:22", Fingerprint: "SHA256:x", Key: testPubKey(t)})
	m.enqueueHostKey(conn.ID, &remotessh.HostKeyError{Addr: "h2:22", Fingerprint: "SHA256:y", Key: testPubKey(t)})

	// Esc 取消第一个
	m.handleDialogKey(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.hostKeyQueue) != 1 {
		t.Fatalf("取消后队列应出队，实际 %d", len(m.hostKeyQueue))
	}
	if m.dlg == nil {
		t.Fatal("取消后应继续弹出下一个询问")
	}
	if _, err := os.Stat(kh); err == nil {
		t.Error("取消时不应写入 known_hosts")
	}

	// 再取消一个：队列清空
	m.handleDialogKey(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.hostKeyQueue) != 0 {
		t.Fatalf("队列应清空，实际 %d", len(m.hostKeyQueue))
	}
	if m.dlg != nil {
		t.Error("队列清空后不应残留对话框")
	}
}

func TestChangedHostKeyHasNoTrustButton(t *testing.T) {
	t.Setenv(remotessh.KnownHostsEnv, filepath.Join(t.TempDir(), "known_hosts"))

	m, st, _ := newTestModel(t)
	conn := st.GetConnections()[0]

	m.enqueueHostKey(conn.ID, &remotessh.HostKeyError{
		Addr: "h1:22", Fingerprint: "SHA256:z", Changed: true, Key: testPubKey(t),
	})
	if m.dlg == nil {
		t.Fatal("指纹变更也应提示用户")
	}
	if strings.Contains(m.dlg.okLabel, "信任") {
		t.Errorf("指纹变更不应提供信任按钮，实际 okLabel=%q", m.dlg.okLabel)
	}
	if !strings.Contains(m.dlg.message, "ssh-keygen -R") {
		t.Error("指纹变更应给出 ssh-keygen -R 的处理建议")
	}
}
