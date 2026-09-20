package remotessh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sshx "golang.org/x/crypto/ssh"

	"sshtool/internal/store"
)

// writeTestKey 在 path 生成 ed25519 私钥；passphrase 非空时加密保存。
func writeTestKey(t *testing.T, path, passphrase string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = sshx.MarshalPrivateKey(priv, "sshtool-test")
	} else {
		block, err = sshx.MarshalPrivateKeyWithPassphrase(priv, "sshtool-test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveKeyPath(t *testing.T) {
	def := ResolveKeyPath(store.Connection{})
	if !strings.Contains(def, "id_rsa") {
		t.Fatalf("缺省私钥路径应含 id_rsa，得到 %q", def)
	}
	if strings.Contains(def, "~") {
		t.Fatalf("缺省路径不应残留 ~：%q", def)
	}
	if got := ResolveKeyPath(store.Connection{KeyPath: "/a/b/k"}); got != "/a/b/k" {
		t.Fatalf("显式路径应原样返回，得到 %q", got)
	}
	if got := ResolveKeyPath(store.Connection{KeyPath: "~/x/k"}); strings.Contains(got, "~") {
		t.Fatalf("~ 应被展开，得到 %q", got)
	}
}

func TestAgentUnavailableWhenNoSock(t *testing.T) {
	resetAgentForTest()
	t.Setenv("SSH_AUTH_SOCK", "")
	if AgentAvailable() {
		t.Fatal("未设置 SSH_AUTH_SOCK 时 agent 不应可用")
	}
	if agentAuthMethod() != nil {
		t.Fatal("无 agent 时不应返回认证方式")
	}
}

func TestAgentUnavailableWhenDialFails(t *testing.T) {
	resetAgentForTest()
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "missing.sock"))
	if AgentAvailable() {
		t.Fatal("连接 agent 失败时不应可用")
	}
	if agentAuthMethod() != nil {
		t.Fatal("连接失败时不应返回认证方式")
	}
}

func TestAuthMethodsKeyWithoutAgent(t *testing.T) {
	resetAgentForTest()
	t.Setenv("SSH_AUTH_SOCK", "")
	p := filepath.Join(t.TempDir(), "k")
	writeTestKey(t, p, "")
	methods, err := authMethods(store.Connection{AuthType: store.AuthKey, KeyPath: p}, "")
	if err != nil {
		t.Fatalf("未加密私钥应直接可用：%v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("无 agent 时应只有 1 个认证方式（私钥），得到 %d", len(methods))
	}
}

func TestAuthMethodsEncryptedKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ek")
	writeTestKey(t, p, "pw")
	c := store.Connection{AuthType: store.AuthKey, KeyPath: p}

	if _, err := authMethods(c, ""); err != ErrNeedPassphrase {
		t.Fatalf("加密私钥无口令时应返回 ErrNeedPassphrase，得到 %v", err)
	}
	if _, err := authMethods(c, "pw"); err != nil {
		t.Fatalf("正确口令应解析成功：%v", err)
	}
	if _, err := authMethods(c, "wrong"); err == nil {
		t.Fatal("错误口令应报错")
	}
}
