package keytool

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sshx "golang.org/x/crypto/ssh"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

func TestGenerateAllAlgorithms(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "nested", "id_test")

	for _, alg := range []Alg{Ed25519, ECDSA256, RSA2048} {
		t.Run(string(alg), func(t *testing.T) {
			info, err := Generate(GenOptions{
				Alg:     alg,
				Comment: "sshtool@test",
				Path:    keyPath + "-" + string(alg),
			})
			if err != nil {
				t.Fatalf("生成失败: %v", err)
			}
			if info.Comment != "sshtool@test" {
				t.Fatalf("注释 = %q", info.Comment)
			}
			if !strings.HasPrefix(info.Fingerprint, "SHA256:") {
				t.Fatalf("指纹格式异常: %q", info.Fingerprint)
			}
			switch alg {
			case Ed25519:
				if info.Alg != "ssh-ed25519" {
					t.Fatalf("算法 = %q", info.Alg)
				}
			case ECDSA256:
				if !strings.HasPrefix(info.Alg, "ecdsa-sha2-nistp256") {
					t.Fatalf("算法 = %q", info.Alg)
				}
			case RSA2048:
				if info.Alg != "ssh-rsa" {
					t.Fatalf("算法 = %q", info.Alg)
				}
			}

			// 私钥应能被 ssh 库解析回来，长度罩得上也应该是合法的 OpenSSH 格式
			data, err := os.ReadFile(info.PrivatePath)
			if err != nil {
				t.Fatal(err)
			}
			block, _ := pem.Decode(data)
			if block == nil {
				t.Fatal("私钥不是 PEM 格式")
			}
			if strings.Contains(block.Type, "ENCRYPTED") {
				t.Fatal("未设置口令却产出了加密私钥")
			}
			if _, err := sshx.ParsePrivateKey(data); err != nil {
				t.Fatalf("私钥无法解析: %v", err)
			}

			// 公钥文件内容应与返回值一致
			pubData, err := os.ReadFile(info.PublicPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(pubData)) != info.PublicKey {
				t.Fatalf("公钥文件内容与返回值不一致")
			}

			// 私钥权限必须收紧（Windows 上 Go 无法设置 POSIX 权限位，跳过该断言）
			if runtime.GOOS != "windows" {
				if st, err := os.Stat(info.PrivatePath); err == nil && st.Mode().Perm() != 0o600 {
					t.Fatalf("私钥权限 = %v，期望 0600", st.Mode().Perm())
				}
			}
		})
	}
}

func TestGenerateKeyTypesAreUsable(t *testing.T) {
	dir := t.TempDir()

	info, err := Generate(GenOptions{Alg: Ed25519, Path: filepath.Join(dir, "id_ed25519")})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(info.PrivatePath)
	if _, err := sshx.ParsePrivateKey(data); err != nil {
		t.Fatalf("ed25519 私钥不可用: %v", err)
	}

	// 带口令的私钥应该无法直接解析（必须先解密）
	enc, err := Generate(GenOptions{Alg: Ed25519, Path: filepath.Join(dir, "id_enc"), Passphrase: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(enc.PrivatePath)
	if _, err := sshx.ParsePrivateKey(data); err == nil {
		t.Fatal("加密私钥竟然无需口令就能解析")
	}
	if _, err := sshx.ParsePrivateKeyWithPassphrase(data, []byte("pw")); err != nil {
		t.Fatalf("正确口令却解析失败: %v", err)
	}
}

func TestGenerateRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "id_ed25519")
	if _, err := Generate(GenOptions{Alg: Ed25519, Path: p}); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(GenOptions{Alg: Ed25519, Path: p}); err == nil {
		t.Fatal("未勾选覆盖时应拒绝覆盖已有文件")
	}
	if _, err := Generate(GenOptions{Alg: Ed25519, Path: p, Overwrite: true}); err != nil {
		t.Fatalf("勾选覆盖后仍失败: %v", err)
	}
}

func TestReadPublicAndListLocal(t *testing.T) {
	dir := t.TempDir()
	gen, err := Generate(GenOptions{Alg: Ed25519, Comment: "list@test", Path: filepath.Join(dir, "id_a")})
	if err != nil {
		t.Fatal(err)
	}
	// 制造一个无法解析的干扰文件
	_ = os.WriteFile(filepath.Join(dir, "known_hosts.pub"), []byte("garbage\n"), 0o644)

	keys, err := ListLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("扫描到 %d 个密钥，期望 1（干扰文件应被忽略）", len(keys))
	}
	if keys[0].Fingerprint != gen.Fingerprint {
		t.Fatalf("指纹不一致")
	}
	if !keys[0].HasPrivate || keys[0].PrivatePath != gen.PrivatePath {
		t.Fatalf("未关联到私钥: %+v", keys[0])
	}
}

func TestListLocalMissingDirReturnsEmpty(t *testing.T) {
	keys, err := ListLocal(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("目录不存在不应报错: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("期望空列表，得到 %d", len(keys))
	}
}

func TestKeyBodyIgnoresComment(t *testing.T) {
	a := "ssh-ed25519 AAAAsig user@host"
	b := "ssh-ed25519 AAAAsig another@comment"
	if KeyBody(a) != KeyBody(b) {
		t.Fatal("去重比较必须忽略注释")
	}
}

func TestShellQuoteAndRemotePath(t *testing.T) {
	if got := shellQuote("ssh-ed25519 AAA"); got != "'ssh-ed25519 AAA'" {
		t.Fatalf("shellQuote = %q", got)
	}
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("shellQuote 未正确转义单引号: %q", got)
	}
	if got := normalizeRemotePath(""); got != "$HOME/.ssh/authorized_keys" {
		t.Fatalf("默认路径 = %q", got)
	}
	if got := normalizeRemotePath("~/.ssh/ak"); got != "$HOME/.ssh/ak" {
		t.Fatalf("~ 路径 = %q", got)
	}
	if got := normalizeRemotePath("/etc/ssh/ak"); got != "/etc/ssh/ak" {
		t.Fatalf("绝对路径 = %q", got)
	}
	if got := normalizeRemotePath("custom/ak"); got != "$HOME/custom/ak" {
		t.Fatalf("相对路径 = %q", got)
	}
}

func TestInstallCommandShape(t *testing.T) {
	cmd := InstallCommand("ssh-ed25519 AAAA alice@pc", "~/.ssh/authorized_keys")
	for _, want := range []string{`P="$HOME/.ssh/authorized_keys"`, `set -- $K`, `B="$1 $2"`, "COUNT="} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("安装脚本缺少 %q:\n%s", want, cmd)
		}
	}
	if !strings.Contains(cmd, "'ssh-ed25519 AAAA alice@pc'") {
		t.Fatalf("公钥未被安全引用:\n%s", cmd)
	}
}

func TestParseCount(t *testing.T) {
	if got := parseCount("APPENDED\nCOUNT=12\n"); got != 12 {
		t.Fatalf("parseCount = %d", got)
	}
	if got := parseCount("EXISTS\n"); got != 0 {
		t.Fatalf("parseCount = %d，期望 0", got)
	}
}

// TestPushToServer 端到端：把公钥推到进程内 SSH 服务端的真实 authorized_keys 文件，
// 覆盖「新建写入 → 去重跳过 → 权限修正」三条路径。
func TestPushToServer(t *testing.T) {
	if testutil.POSIXShell() == "" {
		t.Skip("未找到 POSIX shell，跳过需要 exec 的集成测试")
	}

	srv := testutil.StartSSHServer(t)

	// 服务端执行命令时用的是自己的 HOME（srv.Home），远端文件就落在那里
	home := srv.Home
	target := filepath.Join(home, ".ssh", "authorized_keys")

	info, err := Generate(GenOptions{Alg: Ed25519, Comment: "push@test", Path: filepath.Join(home, ".ssh", "id_ed25519")})
	if err != nil {
		t.Fatal(err)
	}
	conn := store.Connection{
		ID: "h1", Name: "host1", Host: srv.Host, Port: srv.Port,
		User: "test", AuthType: store.AuthPassword, Password: "pass",
	}

	res := Push(PushOptions{Conn: conn, Secret: "pass", PublicKey: info.PublicKey, RemotePath: "~/.ssh/authorized_keys", Timeout: 20 * time.Second})
	if res.Err != "" {
		t.Fatalf("首次推送失败: %s | %s", res.Err, res.Message)
	}
	if !res.Appended {
		t.Fatalf("首次推送应写入新密钥: %+v", res)
	}
	if res.Total != 1 {
		t.Fatalf("远端条目数 = %d，期望 1", res.Total)
	}

	// 第二次推送必须被去重跳过，且不能产生重复行
	res2 := Push(PushOptions{Conn: conn, Secret: "pass", PublicKey: info.PublicKey, RemotePath: "~/.ssh/authorized_keys"})
	if res2.Err != "" {
		t.Fatalf("二次推送失败: %s", res2.Err)
	}
	if res2.Appended {
		t.Fatal("重复推送不应再次追加")
	}
	if res2.Total != 1 {
		t.Fatalf("去重后条目数 = %d，期望 1", res2.Total)
	}

	// 文件内容与权限
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("authorized_keys 未创建: %v", err)
	}
	if strings.TrimSpace(string(data)) != info.PublicKey {
		t.Fatalf("文件内容不符:\n%s", data)
	}
	// Windows 下 POSIX 权限位不可靠，仅在类 Unix 环境断言
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(target)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("authorized_keys 权限 = %v，期望 0600", st.Mode().Perm())
		}
		dirStat, _ := os.Stat(filepath.Dir(target))
		if dirStat.Mode().Perm() != 0o700 {
			t.Fatalf(".ssh 目录权限 = %v，期望 0700", dirStat.Mode().Perm())
		}
	}

	// 换个注释再推同一把密钥，仍应识别为重复（去重按「算法+密钥体」，不看注释）
	body := KeyBody(info.PublicKey)
	recyled := body + " changed-comment"
	res3 := Push(PushOptions{Conn: conn, Secret: "pass", PublicKey: recyled, RemotePath: "~/.ssh/authorized_keys"})
	if res3.Err != "" {
		t.Fatalf("换注释推送失败: %s", res3.Err)
	}
	if res3.Appended {
		t.Fatal("同一把密钥换注释后不应再次追加")
	}
	if res3.Total != 1 {
		t.Fatalf("换注释推送后条目数 = %d，期望 1", res3.Total)
	}
}

func TestPushBadPassword(t *testing.T) {
	if testutil.POSIXShell() == "" {
		t.Skip("未找到 POSIX shell，跳过需要 exec 的集成测试")
	}
	srv := testutil.StartSSHServer(t)
	conn := store.Connection{ID: "h2", Host: srv.Host, Port: srv.Port, User: "test", AuthType: store.AuthPassword}

	res := Push(PushOptions{Conn: conn, Secret: "bad", PublicKey: "ssh-ed25519 AAAA ignored"})
	if res.Err == "" {
		t.Fatal("错误密码应返回失败")
	}
}

// 保证三种私钥类型都被覆盖到（避免编译器优化掉未使用的分支）。
var _ = []any{(*ed25519.PrivateKey)(nil), (*rsa.PrivateKey)(nil), (*ecdsa.PrivateKey)(nil)}
