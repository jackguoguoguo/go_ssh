package remotessh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sshx "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshtool/internal/store"
)

// useTempKnownHosts 把 known_hosts 指向临时文件，返回清理函数。
func useTempKnownHosts(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "known_hosts")
	orig := knownHostsPath
	knownHostsPath = func() (string, error) { return p, nil }
	t.Cleanup(func() { knownHostsPath = orig })
	return p
}

// newTestHostKey 生成一个随机的 ed25519 主机公钥。
func newTestHostKey(t *testing.T) sshx.PublicKey {
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

func testAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22} }

func TestHostKeyCallbackUnknownHost(t *testing.T) {
	useTempKnownHosts(t)

	key := newTestHostKey(t)
	err := hostKeyCallback()("example.test:22", testAddr(), key)

	var hke *HostKeyError
	if !errors.As(err, &hke) {
		t.Fatalf("未知主机应返回 *HostKeyError，实际: %v", err)
	}
	if hke.Changed {
		t.Error("首次连接不应标记为指纹变更")
	}
	if !strings.HasPrefix(hke.Fingerprint, "SHA256:") {
		t.Errorf("指纹格式不对: %q", hke.Fingerprint)
	}
	if hke.Key == nil {
		t.Error("HostKeyError 应带上主机公钥，供确认后落盘")
	}
	if strings.Contains(err.Error(), "ssh-keygen") {
		t.Errorf("未知主机的错误不应提示 ssh-keygen -R，实际: %v", err)
	}
}

func TestHostKeyCallbackTrustThenRejectChange(t *testing.T) {
	useTempKnownHosts(t)

	key := newTestHostKey(t)
	if err := TrustHost("example.test:22", key); err != nil {
		t.Fatalf("TrustHost 失败: %v", err)
	}

	// 已信任：同一把钥匙应直接通过
	if err := hostKeyCallback()("example.test:22", testAddr(), key); err != nil {
		t.Fatalf("已信任的主机不应报错: %v", err)
	}

	// 换钥匙：必须硬拒绝且标记 Changed
	other := newTestHostKey(t)
	err := hostKeyCallback()("example.test:22", testAddr(), other)
	var hke *HostKeyError
	if !errors.As(err, &hke) {
		t.Fatalf("指纹变更应返回 *HostKeyError，实际: %v", err)
	}
	if !hke.Changed {
		t.Error("指纹变更应标记 Changed=true")
	}
	if len(hke.Want) == 0 {
		t.Error("Changed 时应带上 known_hosts 中的旧记录")
	}
	if !strings.Contains(err.Error(), "ssh-keygen -R") {
		t.Errorf("指纹变更的错误应给出处理建议，实际: %v", err)
	}
}

func TestTrustHostIdempotent(t *testing.T) {
	p := useTempKnownHosts(t)
	key := newTestHostKey(t)

	for i := 0; i < 3; i++ {
		if err := TrustHost("example.test:22", key); err != nil {
			t.Fatalf("第 %d 次 TrustHost 失败: %v", i+1, err)
		}
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if lines != 1 {
		t.Errorf("重复信任应幂等，实际写入 %d 行:\n%s", lines, data)
	}
}

func TestTrustHostConcurrent(t *testing.T) {
	p := useTempKnownHosts(t)

	const n = 8
	type job struct {
		addr string
		key  sshx.PublicKey
	}
	jobs := make([]job, 0, n)
	for i := 0; i < n; i++ {
		jobs = append(jobs, job{addr: "host" + string(rune('a'+i)) + ".test:2222", key: newTestHostKey(t)})
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			errs <- TrustHost(j.addr, j.key)
		}(j)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发写入失败: %v", err)
		}
	}

	// 写出的文件必须能被 knownhosts 解析，且每行都是完整的条目
	if _, err := knownhosts.New(p); err != nil {
		t.Fatalf("并发写入产生了无法解析的 known_hosts: %v", err)
	}
	data, _ := os.ReadFile(p)
	got := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		got++
		// 每行都应形如 "[host]:2222 ssh-ed25519 AAAA..."
		f := strings.Fields(l)
		if len(f) < 3 || !strings.HasPrefix(f[0], "[") || !strings.HasPrefix(f[2], "AAAA") {
			t.Errorf("条目格式异常: %q", l)
		}
	}
	if got != n {
		t.Errorf("期望 %d 行，实际 %d 行", n, got)
	}
}

func TestHostKeyErrorBecomesNeedHostKeyEvent(t *testing.T) {
	events := make(chan Event, 4)
	s := newSession(store.Connection{ID: "c1", Host: "h", Port: 22}, 80, 24, events, nil, 100)
	s.fail(&HostKeyError{Addr: "h:22", Fingerprint: "SHA256:xxx", Key: newTestHostKey(t)})

	select {
	case ev := <-events:
		if ev.Kind != EventNeedHostKey {
			t.Errorf("期望 EventNeedHostKey，实际 %v", ev.Kind)
		}
	default:
		t.Fatal("未投递事件")
	}
}

func TestInsecureEnvBypassesCheck(t *testing.T) {
	useTempKnownHosts(t)
	t.Setenv(insecureHostKeysEnv, "1")

	if err := hostKeyCallback()("example.test:22", testAddr(), newTestHostKey(t)); err != nil {
		t.Errorf("设置逃生舱环境变量后应跳过校验，实际: %v", err)
	}
}
