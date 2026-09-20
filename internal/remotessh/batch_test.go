package remotessh

import (
	"strings"
	"testing"
	"time"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// TestBatchExecutesAcrossHosts 验证 Batch 在多个测试服务器上并发执行并收集输出。
func TestBatchExecutesAcrossHosts(t *testing.T) {
	s1 := testutil.StartSSHServer(t)
	s2 := testutil.StartSSHServer(t)

	conns := []store.Connection{
		{Name: "host-a", Host: s1.Host, Port: s1.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"},
		{Name: "host-b", Host: s2.Host, Port: s2.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"},
	}

	results := Batch(conns, "echo hello-batch", 10*time.Second)
	if len(results) != 2 {
		t.Fatalf("应返回 2 条结果，实际 %d", len(results))
	}
	for _, r := range results {
		if r.Err != "" {
			t.Fatalf("%s 执行失败: %s", r.Name, r.Err)
		}
		if r.Code != 0 {
			t.Fatalf("%s 退出码 = %d，期望 0", r.Name, r.Code)
		}
		if !strings.Contains(r.Stdout, "hello-batch") {
			t.Fatalf("%s 输出缺少 hello-batch，实际 %q", r.Name, r.Stdout)
		}
	}
}

// TestBatchFailureCaptured 验证认证失败的连接被记录为带 Err 的结果。
func TestBatchFailureCaptured(t *testing.T) {
	s := testutil.StartSSHServer(t)

	conns := []store.Connection{
		// 错误密码 → 认证失败
		{Name: "bad-auth", Host: s.Host, Port: s.Port, User: "test", AuthType: store.AuthPassword, Password: "wrong"},
	}

	results := Batch(conns, "echo hi", 5*time.Second)
	if len(results) != 1 {
		t.Fatalf("应返回 1 条结果，实际 %d", len(results))
	}
	r := results[0]
	if r.Err == "" {
		t.Fatal("认证失败应记录 Err，实际为空")
	}
	if r.Stdout != "" || r.Code != -1 {
		t.Fatalf("失败结果应无输出且 Code=-1（stdout=%q code=%d）", r.Stdout, r.Code)
	}
}

// TestBatchUnreachableTimeout 验证不可达主机在超时后返回错误（而非挂死）。
func TestBatchUnreachableTimeout(t *testing.T) {
	conns := []store.Connection{
		// 127.0.0.1:1 几乎必然拒绝连接（无监听），应快速失败而非超时
		{Name: "nope", Host: "127.0.0.1", Port: 1, User: "test", AuthType: store.AuthPassword, Password: "pass"},
	}

	results := Batch(conns, "echo hi", 3*time.Second)
	if len(results) != 1 {
		t.Fatalf("应返回 1 条结果，实际 %d", len(results))
	}
	if r := results[0]; r.Err == "" {
		t.Fatal("不可达主机应记录 Err，实际为空")
	}
}