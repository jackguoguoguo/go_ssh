package keytool

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// TestRotateFullCycle 真机端到端：分发新密钥 → 验证新私钥可登录 → 移除旧密钥。
func TestRotateFullCycle(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirs(t, sshDir)

	// 旧密钥（不带口令，便于验证阶段用私钥登录）
	oldKey, err := Generate(GenOptions{Alg: Ed25519, Comment: "old@test", Path: filepath.Join(sshDir, "id_old")})
	if err != nil {
		t.Fatal(err)
	}
	// 新密钥
	newKey, err := Generate(GenOptions{Alg: Ed25519, Comment: "new@test", Path: filepath.Join(sshDir, "id_new")})
	if err != nil {
		t.Fatal(err)
	}

	srv := testutil.StartSSHServer(t)
	conn := store.Connection{
		Name: "srv", Host: srv.Host, Port: srv.Port, User: "test",
		AuthType: store.AuthPassword, Password: "pass",
	}

	// 先让旧密钥获得授权（模拟现状）
	if r := Push(PushOptions{Conn: conn, Secret: "pass", PublicKey: oldKey.PublicKey}); r.Err != "" {
		t.Fatalf("预置旧密钥失败：%s", r.Err)
	}

	// 轮换：新密钥分发 → 验证 → 移除旧
	res := Rotate(RotateOptions{
		Conn:       conn,
		Secret:     "pass",
		NewPublic:  newKey.PublicKey,
		NewKeyPath: newKey.PrivatePath,
		OldPublic:  oldKey.PublicKey,
		Timeout:    15 * time.Second,
	})
	if res.Err != "" {
		t.Fatalf("轮换失败：%s（阶段：%v）", res.Err, res.Phases)
	}
	if !res.Verified {
		t.Fatal("新密钥应验证通过")
	}
	if !res.Removed {
		t.Fatal("旧密钥应被移除")
	}
	if len(res.Phases) != 3 {
		t.Fatalf("应有三个阶段记录，实际 %v", res.Phases)
	}

	// 远端应只剩新密钥
	audit := Audit([]store.Connection{conn}, []KeyInfo{newKey, oldKey}, "~/.ssh/authorized_keys", 10*time.Second)
	if audit[0].Err != "" {
		t.Fatalf("盘点失败：%s", audit[0].Err)
	}
	if len(audit[0].Authorized) != 1 || audit[0].Authorized[0] != newKey.Fingerprint {
		t.Fatalf("轮换后应只有新密钥被授权：%+v", audit[0].Authorized)
	}
	if len(audit[0].Missing) != 1 || audit[0].Missing[0] != oldKey.Fingerprint {
		t.Fatalf("旧密钥应已不在远端：%+v", audit[0].Missing)
	}
}

// TestRotateKeepsOldOnVerifyFailure 验证新密钥不可用时保留旧密钥（不把自己锁在门外）。
func TestRotateKeepsOldOnVerifyFailure(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirs(t, sshDir)

	oldKey, err := Generate(GenOptions{Alg: Ed25519, Comment: "old@test", Path: filepath.Join(sshDir, "id_old")})
	if err != nil {
		t.Fatal(err)
	}
	// 一把「新」密钥，但私钥路径故意指向不存在的文件 → 验证必然失败
	newKey, err := Generate(GenOptions{Alg: Ed25519, Comment: "new@test", Path: filepath.Join(sshDir, "id_new")})
	if err != nil {
		t.Fatal(err)
	}

	srv := testutil.StartSSHServer(t)
	conn := store.Connection{
		Name: "srv", Host: srv.Host, Port: srv.Port, User: "test",
		AuthType: store.AuthPassword, Password: "pass",
	}
	if r := Push(PushOptions{Conn: conn, Secret: "pass", PublicKey: oldKey.PublicKey}); r.Err != "" {
		t.Fatalf("预置旧密钥失败：%s", r.Err)
	}

	res := Rotate(RotateOptions{
		Conn:       conn,
		Secret:     "pass",
		NewPublic:  newKey.PublicKey,
		NewKeyPath: filepath.Join(sshDir, "no_such_private_key"), // 故意
		OldPublic:  oldKey.PublicKey,
		Timeout:    10 * time.Second,
	})
	if res.Err == "" {
		t.Fatal("验证失败应返回错误")
	}
	if res.Removed {
		t.Fatal("验证失败时绝不能移除旧密钥")
	}
	if !strings.Contains(res.Err, "保留旧密钥") {
		t.Fatalf("错误信息应说明保留了旧密钥：%q", res.Err)
	}

	// 旧密钥仍在远端
	audit := Audit([]store.Connection{conn}, []KeyInfo{oldKey}, "~/.ssh/authorized_keys", 10*time.Second)
	if audit[0].Err != "" || len(audit[0].Authorized) != 1 {
		t.Fatalf("旧密钥应仍在远端：%+v", audit[0])
	}
}

// TestRotateKeepOldOption 验证 KeepOld=true 时跳过移除。
func TestRotateKeepOldOption(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirs(t, sshDir)
	oldKey, _ := Generate(GenOptions{Alg: Ed25519, Comment: "old", Path: filepath.Join(sshDir, "id_old")})
	newKey, _ := Generate(GenOptions{Alg: Ed25519, Comment: "new", Path: filepath.Join(sshDir, "id_new")})

	srv := testutil.StartSSHServer(t)
	conn := store.Connection{Name: "srv", Host: srv.Host, Port: srv.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"}
	if r := Push(PushOptions{Conn: conn, Secret: "pass", PublicKey: oldKey.PublicKey}); r.Err != "" {
		t.Fatal(r.Err)
	}

	res := Rotate(RotateOptions{
		Conn: conn, Secret: "pass",
		NewPublic: newKey.PublicKey, NewKeyPath: newKey.PrivatePath,
		OldPublic: oldKey.PublicKey, KeepOld: true, Timeout: 15 * time.Second,
	})
	if res.Err != "" {
		t.Fatalf("轮换失败：%s（%v）", res.Err, res.Phases)
	}
	if res.Removed {
		t.Fatal("KeepOld 时不应移除旧密钥")
	}
}

// TestRotateEmptyNewKey 验证空公钥被拒绝。
func TestRotateEmptyNewKey(t *testing.T) {
	res := Rotate(RotateOptions{Conn: store.Connection{Name: "x", Host: "h"}, NewPublic: "  "})
	if res.Err == "" {
		t.Fatal("空新公钥应报错")
	}
}

// TestRemoveAuthorizedKeyByIdempotent 验证移除不存在的条目不报错且 REMOVED=0。
func TestRemoveAuthorizedKeyByIdempotent(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	mkdirs(t, sshDir)
	k, _ := Generate(GenOptions{Alg: Ed25519, Comment: "r", Path: filepath.Join(sshDir, "id_r")})

	srv := testutil.StartSSHServer(t)
	conn := store.Connection{Name: "srv", Host: srv.Host, Port: srv.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"}

	// 从未推送过 → 移除应是 no-op
	removed, _, err := RemoveAuthorizedKey(conn, "pass", k.PublicKey, "~/.ssh/authorized_keys", 10*time.Second)
	if err != nil {
		t.Fatalf("移除不存在的条目不应报错：%v", err)
	}
	if removed {
		t.Fatal("未推送过的密钥不应报告已移除")
	}
}

func TestRotateSummaryCounts(t *testing.T) {
	ok, failed := RotateSummary([]RotateResult{
		{Name: "a"},
		{Name: "b", Err: "x"},
		{Name: "c", Err: "y"},
	})
	if ok != 1 || failed != 2 {
		t.Fatalf("统计错误：ok=%d failed=%d", ok, failed)
	}
}
