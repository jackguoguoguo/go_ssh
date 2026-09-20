package localshell

import (
	"strings"
	"testing"
	"time"
)

// TestSessionRunsCommand 验证本地 shell 真的通过 PTY 跑起来了：写入一条命令，
// 输出应出现在终端缓冲区里（含回显，说明是真实终端而非裸管道）。
func TestSessionRunsCommand(t *testing.T) {
	s, err := Open(80, 24, 1000, nil)
	if err != nil {
		t.Skipf("本机无法打开 PTY，跳过：%v", err)
	}
	defer s.Close()

	if s.State().String() != "已连接" {
		t.Fatalf("打开后应为已连接，得到 %v", s.State())
	}
	if s.Label() != "本地 shell" {
		t.Fatalf("标签名不对：%q", s.Label())
	}
	if s.ConnInfo().Host != "localhost" {
		t.Fatalf("连接信息不对：%+v", s.ConnInfo())
	}

	marker := "sshtool-local-ok"
	if err := s.Write([]byte("echo " + marker + "\r")); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Term().Text(0, 0, 200, 40), marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("本地 shell 未回显命令输出：%q", s.Term().Text(0, 0, 200, 40))
}

// TestSessionClose 验证关闭后状态变为已关闭。
func TestSessionClose(t *testing.T) {
	s, err := Open(40, 10, 500, nil)
	if err != nil {
		t.Skipf("本机无法打开 PTY，跳过：%v", err)
	}
	s.Close()
	if s.State().String() != "已关闭" {
		t.Fatalf("关闭后应为已关闭，得到 %v", s.State())
	}
	if err := s.Write([]byte("x")); err == nil {
		t.Fatal("关闭后写入应报错")
	}
}
