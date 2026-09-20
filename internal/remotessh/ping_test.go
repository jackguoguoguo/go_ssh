package remotessh

import (
	"testing"
	"time"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// TestPingOK 验证正常连接的巡检状态为可达，且记录耗时。
func TestPingOK(t *testing.T) {
	s := testutil.StartSSHServer(t)
	conns := []store.Connection{
		{Name: "web", Host: s.Host, Port: s.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"},
	}
	results := Ping(conns, 10*time.Second)
	if len(results) != 1 {
		t.Fatalf("应返回 1 条结果，实际 %d", len(results))
	}
	r := results[0]
	if r.Status != PingOK {
		t.Fatalf("状态应为可达，实际 %v（detail=%q）", r.Status.Text(), r.Detail)
	}
	// Latency 为 time.Duration，无直接比较接口；非 0 即代表记录了耗时（到达可达值）。
	if r.Latency == 0*time.Second {
		t.Fatalf("耗时不应为 0：%v", r.Latency)
	}
}

// TestPingAuthFail 验证错误密码 → 认证失败。
func TestPingAuthFail(t *testing.T) {
	s := testutil.StartSSHServer(t)
	conns := []store.Connection{
		{Name: "bad", Host: s.Host, Port: s.Port, User: "test", AuthType: store.AuthPassword, Password: "wrong"},
	}
	results := Ping(conns, 5*time.Second)
	if len(results) != 1 {
		t.Fatalf("应返回 1 条结果，实际 %d", len(results))
	}
	r := results[0]
	if r.Status != PingAuthFail {
		t.Fatalf("错误密码应判为认证失败，实际 %v（detail=%q）", r.Status.Text(), r.Detail)
	}
	if r.Detail == "" {
		t.Fatal("认证失败应附细节")
	}
}

// TestPingUnreachable 验证无监听端口 → 不可达。
func TestPingUnreachable(t *testing.T) {
	conns := []store.Connection{
		{Name: "down", Host: "127.0.0.1", Port: 1, User: "test", AuthType: store.AuthPassword, Password: "pass"},
	}
	results := Ping(conns, 5*time.Second)
	if len(results) != 1 {
		t.Fatalf("应返回 1 条结果，实际 %d", len(results))
	}
	r := results[0]
	if r.Status != PingUnreachable {
		t.Fatalf("无监听应判为不可达，实际 %v（detail=%q）", r.Status.Text(), r.Detail)
	}
}

// TestPingCount 验证统计函数。
func TestPingCount(t *testing.T) {
	results := []PingResult{
		{Status: PingOK},
		{Status: PingOK},
		{Status: PingAuthFail},
		{Status: PingUnreachable},
		{Status: PingTimeout},
	}
	ok, af, un, to := PingCount(results)
	if ok != 2 || af != 1 || un != 1 || to != 1 {
		t.Fatalf("统计出错：%d/%d/%d/%d", ok, af, un, to)
	}
}