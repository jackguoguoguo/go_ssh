package portfwd

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	sshx "golang.org/x/crypto/ssh"

	"sshtool/internal/testutil"
)

// echoServer 启动一个回显 TCP 服务，返回其监听地址与关闭函数。
func echoServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

// dialTestServer 直连测试 SSH 服务端，返回已认证的 *sshx.Client。
func dialTestServer(t *testing.T, s *testutil.SSHServer) *sshx.Client {
	t.Helper()
	cfg := &sshx.ClientConfig{
		User:            "test",
		Auth:            []sshx.AuthMethod{sshx.Password("pass")},
		HostKeyCallback: sshx.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := sshx.Dial("tcp", net.JoinHostPort(s.Host, fmt.Sprintf("%d", s.Port)), cfg)
	if err != nil {
		t.Fatalf("连接测试服务端失败: %v", err)
	}
	return client
}

func TestLocalForwardRoundTrip(t *testing.T) {
	s := testutil.StartSSHServer(t)
	client := dialTestServer(t, s)
	defer client.Close()

	target, stop := echoServer(t)
	defer stop()

	m := New()
	f, err := m.StartLocal(client, "127.0.0.1:0", target)
	if err != nil {
		t.Fatalf("启动本地转发失败: %v", err)
	}
	defer m.Stop(f.ID)

	if len(m.List()) != 1 {
		t.Fatalf("应存在 1 条转发，实际 %d", len(m.List()))
	}

	// 连接到本地监听端口，数据应经 SSH 隧道抵达远端回显服务并原样返回。
	lc, err := net.Dial("tcp", f.Listen)
	if err != nil {
		t.Fatalf("连接本地转发监听失败: %v", err)
	}
	defer lc.Close()
	if _, err := lc.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	lc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(lc, buf); err != nil {
		t.Fatalf("未收到回显: %v", err)
	}
	if string(buf) != "PING" {
		t.Fatalf("回显内容错误：%q", buf)
	}
}

func TestRemoteForwardRoundTrip(t *testing.T) {
	s := testutil.StartSSHServer(t)
	client := dialTestServer(t, s)
	defer client.Close()

	target, stop := echoServer(t)
	defer stop()

	m := New()
	// 在 SSH 服务端监听一个固定端口，目标回连本机回显服务。
	remoteListen := "127.0.0.1:18701"
	f, err := m.StartRemote(client, remoteListen, target)
	if err != nil {
		t.Fatalf("启动远端转发失败: %v", err)
	}
	defer m.Stop(f.ID)

	if !strings.HasSuffix(f.Listen, ":18701") {
		t.Fatalf("远端转发监听端口应为 18701，实际 %s", f.Listen)
	}

	// 从本机（即 SSH 服务端所在机器）连接远端监听端口，数据应回连到本机回显服务。
	rc, err := net.DialTimeout("tcp", remoteListen, 5*time.Second)
	if err != nil {
		t.Fatalf("连接远端转发监听失败: %v", err)
	}
	defer rc.Close()
	if _, err := rc.Write([]byte("PONG")); err != nil {
		t.Fatal(err)
	}
	rc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatalf("未收到回显: %v", err)
	}
	if string(buf) != "PONG" {
		t.Fatalf("回显内容错误：%q", buf)
	}
}

func TestStopAndList(t *testing.T) {
	s := testutil.StartSSHServer(t)
	client := dialTestServer(t, s)
	defer client.Close()

	target, stop := echoServer(t)
	defer stop()

	m := New()
	f1, _ := m.StartLocal(client, "127.0.0.1:0", target)
	f2, _ := m.StartLocal(client, "127.0.0.1:0", target)

	if got := len(m.List()); got != 2 {
		t.Fatalf("应存在 2 条转发，实际 %d", got)
	}
	if err := m.Stop(f1.ID); err != nil {
		t.Fatalf("停止转发失败: %v", err)
	}
	if got := len(m.List()); got != 1 {
		t.Fatalf("停止后应剩 1 条，实际 %d", got)
	}
	if err := m.Stop("NOPE"); err == nil {
		t.Fatal("停止不存在的转发应报错")
	}
	_ = f2
	m.StopAll()
	if got := len(m.List()); got != 0 {
		t.Fatalf("StopAll 后应无转发，实际 %d", got)
	}
}
