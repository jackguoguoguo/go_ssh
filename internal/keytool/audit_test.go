package keytool

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

func TestParseAuthorizedKeysSkipsComments(t *testing.T) {
	dir := t.TempDir()
	k1, err := Generate(GenOptions{Alg: Ed25519, Comment: "user@a", Path: filepath.Join(dir, "id_a")})
	if err != nil {
		t.Fatal(err)
	}
	k2, err := Generate(GenOptions{Alg: Ed25519, Comment: "user@b", Path: filepath.Join(dir, "id_b")})
	if err != nil {
		t.Fatal(err)
	}
	content := "# 注释行\n\n" + k1.PublicKey + "\n" + k2.PublicKey + "\n"
	got := parseAuthorizedKeys(content)
	if len(got) != 2 {
		t.Fatalf("应解析 2 条（跳过注释与空行），实际 %d", len(got))
	}
	if got[0].Comment != "user@a" || got[1].Comment != "user@b" {
		t.Fatalf("注释解析错误：%+v", got)
	}
}

func TestClassifyAuthorizedMissingStale(t *testing.T) {
	// 构造本机两把密钥
	dir := t.TempDir()
	k1, err := Generate(GenOptions{Alg: Ed25519, Comment: "local1", Path: filepath.Join(dir, "id_a")})
	if err != nil {
		t.Fatal(err)
	}
	k2, err := Generate(GenOptions{Alg: Ed25519, Comment: "local2", Path: filepath.Join(dir, "id_b")})
	if err != nil {
		t.Fatal(err)
	}
	local := []KeyInfo{k1, k2}

	// 远端只授权了 k1，另有一条本机没有的密钥（废弃）
	other := k2.PublicKey
	_ = other
	remote := parseAuthorizedKeys(k1.PublicKey + "\n" + mustThirdKey(t, dir) + "\n")

	authorized, missing, stale := classify(remote, local)
	if len(authorized) != 1 || authorized[0] != k1.Fingerprint {
		t.Fatalf("已授权应只含 k1：%v", authorized)
	}
	if len(missing) != 1 || missing[0] != k2.Fingerprint {
		t.Fatalf("缺失应为 k2：%v", missing)
	}
	if len(stale) != 1 {
		t.Fatalf("应识别 1 条废弃条目，实际 %d", len(stale))
	}
}

// mustThirdKey 生成一把「本机没有」的第三把密钥（代表远端上的废弃条目）。
func mustThirdKey(t *testing.T, dir string) string {
	t.Helper()
	k, err := Generate(GenOptions{Alg: Ed25519, Comment: "old@remote", Path: filepath.Join(dir, "id_old")})
	if err != nil {
		t.Fatal(err)
	}
	return k.PublicKey
}

func TestAuditAgainstServer(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirs(t, sshDir)

	// 本机密钥
	local, err := Generate(GenOptions{Alg: Ed25519, Comment: "audit@test", Path: filepath.Join(sshDir, "id_ed25519")})
	if err != nil {
		t.Fatal(err)
	}

	srv := testutil.StartSSHServer(t)
	conn := store.Connection{
		Name: "srv", Host: srv.Host, Port: srv.Port, User: "test",
		AuthType: store.AuthPassword, Password: "pass",
	}

	// 先推一次 → 该主机应已授权
	res := Push(PushOptions{Conn: conn, Secret: "pass", PublicKey: local.PublicKey, RemotePath: "~/.ssh/authorized_keys"})
	if res.Err != "" {
		t.Fatalf("推送失败：%s", res.Err)
	}

	got := Audit([]store.Connection{conn}, []KeyInfo{local}, "~/.ssh/authorized_keys", 10*time.Second)
	if len(got) != 1 {
		t.Fatalf("应返回 1 条结果，实际 %d", len(got))
	}
	r := got[0]
	if r.Err != "" {
		t.Fatalf("盘点失败：%s", r.Err)
	}
	if r.Total < 1 {
		t.Fatalf("远端条目数应 >= 1，实际 %d", r.Total)
	}
	if len(r.Authorized) != 1 || r.Authorized[0] != local.Fingerprint {
		t.Fatalf("已授权应含本机密钥：%+v", r.Authorized)
	}
	if len(r.Missing) != 0 {
		t.Fatalf("推送后不应有缺失：%v", r.Missing)
	}

	sum := Summarize(got)
	if sum.Hosts != 1 || sum.Failed != 0 || sum.Fully != 1 {
		t.Fatalf("汇总错误：%+v", sum)
	}
}

func TestAuditUnreachableHost(t *testing.T) {
	conn := store.Connection{Name: "down", Host: "127.0.0.1", Port: 1, User: "x", AuthType: store.AuthPassword, Password: "y"}
	got := Audit([]store.Connection{conn}, nil, "~/.ssh/authorized_keys", 3*time.Second)
	if len(got) != 1 || got[0].Err == "" {
		t.Fatalf("不可达主机应记录错误：%+v", got)
	}
	if s := Summarize(got); s.Failed != 1 {
		t.Fatalf("失败数应为 1：%+v", s)
	}
}

func mkdirs(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}
