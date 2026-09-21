package keytool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempHome 把主目录指向临时目录，使备份目录与默认 SSH 目录都落在沙箱内。
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// 造两把密钥 + config + known_hosts
	k1, err := Generate(GenOptions{Alg: Ed25519, Comment: "one@test", Path: filepath.Join(sshDir, "id_ed25519")})
	if err != nil {
		t.Fatal(err)
	}
	k2, err := Generate(GenOptions{Alg: Ed25519, Comment: "two@test", Path: filepath.Join(sshDir, "id_ed25519_two")})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host demo\n"), 0o600)
	_ = os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte("demo ssh-ed25519 AAAA\n"), 0o600)

	// 备份（需口令）
	archive, err := Backup(sshDir, "s3cret")
	if err != nil {
		t.Fatalf("备份失败：%v", err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("备份文件未生成：%v", err)
	}
	if !strings.HasSuffix(archive, ".enc") {
		t.Fatalf("备份文件后缀应为 .enc：%s", archive)
	}

	// 口令错误 → 读取 manifest 失败
	if _, err := ReadManifest(archive, "wrong"); err == nil {
		t.Fatal("错误口令应解密失败")
	}

	// 正确口令 → manifest 含两把密钥的指纹
	man, err := ReadManifest(archive, "s3cret")
	if err != nil {
		t.Fatalf("读取 manifest 失败：%v", err)
	}
	if len(man.Keys) != 2 {
		t.Fatalf("manifest 应含 2 把密钥，实际 %d", len(man.Keys))
	}
	fps := map[string]bool{}
	for _, k := range man.Keys {
		fps[k.Fingerprint] = true
	}
	if !fps[k1.Fingerprint] || !fps[k2.Fingerprint] {
		t.Fatalf("manifest 指纹与生成的密钥不符：%+v", man.Keys)
	}
	for _, want := range []string{"config", "known_hosts", "id_ed25519", "id_ed25519.pub"} {
		found := false
		for _, f := range man.Files {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("manifest 文件列表缺少 %s：%v", want, man.Files)
		}
	}

	// 恢复到另一个目录（覆盖模式）
	back := filepath.Join(home, "restored")
	written, err := Restore(archive, "s3cret", back, RestoreOverwrite)
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if len(written) == 0 {
		t.Fatal("恢复应写入文件")
	}
	got, err := ReadPublic(filepath.Join(back, "id_ed25519.pub"))
	if err != nil {
		t.Fatalf("恢复后的公钥不可读：%v", err)
	}
	if got.Fingerprint != k1.Fingerprint {
		t.Fatalf("恢复后指纹不符：%s != %s", got.Fingerprint, k1.Fingerprint)
	}
	if _, err := os.Stat(filepath.Join(back, "config")); err != nil {
		t.Fatalf("config 未恢复：%v", err)
	}
}

func TestBackupRequiresPassphrase(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(sshDir, 0o700)
	if _, err := Backup(sshDir, ""); err == nil {
		t.Fatal("无口令应报错")
	}
}

func TestBackupEmptyDir(t *testing.T) {
	home := withTempHome(t)
	empty := filepath.Join(home, "empty-ssh")
	_ = os.MkdirAll(empty, 0o700)
	if _, err := Backup(empty, "pw"); err == nil {
		t.Fatal("空目录备份应报错")
	}
}

func TestRestoreRenameAvoidsOverwrite(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(sshDir, 0o700)
	if _, err := Generate(GenOptions{Alg: Ed25519, Comment: "r@test", Path: filepath.Join(sshDir, "id_ed25519")}); err != nil {
		t.Fatal(err)
	}
	archive, err := Backup(sshDir, "pw")
	if err != nil {
		t.Fatal(err)
	}
	// 恢复回同一目录且用改名模式：原文件不应被覆盖
	written, err := Restore(archive, "pw", sshDir, RestoreRename)
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	renamed := false
	for _, w := range written {
		if strings.Contains(w, ".restored-") {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("改名模式应产生 .restored- 后缀文件，实际 %v", written)
	}
}

func TestListBackups(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(sshDir, 0o700)
	if _, err := Generate(GenOptions{Alg: Ed25519, Path: filepath.Join(sshDir, "id_ed25519")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(sshDir, "pw"); err != nil {
		t.Fatal(err)
	}
	list, err := ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("应列出 1 个备份，实际 %d", len(list))
	}
	if list[0].Size <= 0 {
		t.Fatal("备份大小应大于 0")
	}
}

func TestSaveRemoteSnapshot(t *testing.T) {
	withTempHome(t)
	p, err := SaveRemoteSnapshot("root@1.2.3.4:22", "ssh-ed25519 AAAA demo\n")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ssh-ed25519 AAAA demo\n" {
		t.Fatalf("快照内容不符：%q", data)
	}
	if !strings.Contains(filepath.ToSlash(p), "remote") {
		t.Fatalf("快照应落在 remote 子目录：%s", p)
	}
}
