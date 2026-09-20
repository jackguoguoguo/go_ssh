// Package testutil 提供测试用的进程内 SSH 服务端。
package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	sshx "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// POSIXShell 返回一个可用的 POSIX shell 路径；不可用时返回空串。
//
// 注意：Windows 上有两个「假 bash」必须排除——
//   - C:\Windows\system32\bash.exe  WSL 启动存根，不会按 POSIX 语义执行 -c
//   - %LOCALAPPDATA%\Microsoft\WindowsApps\bash.exe  Microsoft Store 别名存根
//
// 它们会让依赖 exec 的集成测试得出错误结论，因此这里优先选用 Git for Windows 自带的 bash。
func POSIXShell() string {
	candidates := []string{
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\bin\sh.exe`,
		`C:\cygwin64\bin\bash.exe`,
	}
	if runtime.GOOS != "windows" {
		candidates = []string{"/bin/bash", "/bin/sh"}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, c := range []string{"bash", "sh"} {
		p, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		if runtime.GOOS == "windows" && isWindowsStub(p) {
			continue
		}
		return p
	}
	return ""
}

// isWindowsStub 判断是否为 WSL / Store 的 bash 存根。
func isWindowsStub(p string) bool {
	dir := strings.ToLower(filepath.Dir(p))
	return strings.Contains(dir, `windows\system32`) || strings.Contains(dir, "windowsapps")
}

// SSHServer 是一个仅用于测试的 SSH 服务端。
type SSHServer struct {
	Host string
	Port int
	// Home 是该服务端执行命令时使用的工作家目录。
	// 每个服务端实例各有一份，这样「推送到多台主机」的测试能各自拥有独立的
	// authorized_keys，不会因为共享 HOME 而互相覆盖。
	Home string
	ln   net.Listener
	pub  sshx.PublicKey // 主机公钥，供「信任本机测试服务端」使用
}

// StartSSHServer 启动一个进程内 SSH 服务端。
// 接受用户名 test / 密码 pass；shell 启动后输出一行带颜色的文本，并回显收到的输入。
func StartSSHServer(t *testing.T) *SSHServer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sshx.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &sshx.ServerConfig{
		PasswordCallback: func(c sshx.ConnMetadata, pass []byte) (*sshx.Permissions, error) {
			if c.User() == "test" && string(pass) == "pass" {
				return nil, nil
			}
			return nil, fmt.Errorf("认证失败")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	s := &SSHServer{ln: ln, Home: t.TempDir(), pub: signer.PublicKey()}
	addr := ln.Addr().(*net.TCPAddr)
	s.Host, s.Port = addr.IP.String(), addr.Port

	// 让测试也走真实的指纹校验路径：把本服务端的主机公钥写进测试专用的 known_hosts，
	// 而不是让 remotessh 退化成不校验。
	trustSelf(t, net.JoinHostPort(s.Host, fmt.Sprintf("%d", s.Port)), s.pub)

	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(nc, cfg)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

// trustSelf 把本服务端的主机公钥写入测试专用的 known_hosts。
//
// 环境变量 SSHTOOL_KNOWN_HOSTS 与 remotessh.KnownHostsEnv 对应（这里不能引 remotessh 包，
// 因为 remotessh 的内部测试要引 testutil，会形成循环依赖）。这样测试走的是真实指纹校验路径，
// 又不会污染开发机的 ~/.ssh/known_hosts。
func trustSelf(t *testing.T, addr string, key sshx.PublicKey) {
	t.Helper()
	p := os.Getenv("SSHTOOL_KNOWN_HOSTS")
	if p == "" {
		p = filepath.Join(t.TempDir(), "known_hosts")
		t.Setenv("SSHTOOL_KNOWN_HOSTS", p)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("写入测试 known_hosts 失败: %v", err)
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, key)
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("写入测试 known_hosts 失败: %v", err)
	}
}

// pathSep 用于拼接 PATH：Windows 用分号，其余平台用冒号。
var pathSep = func() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}()

// runRemoteCmd 在服务端执行一条命令并返回退出码；无可用 shell 时返回 127。
func (s *SSHServer) runRemoteCmd(ch sshx.Channel, cmdStr string) int {
	shell := POSIXShell()
	if shell == "" || cmdStr == "" {
		return 127
	}
	c := exec.Command(shell, "-c", cmdStr)
	c.Stdout = ch
	c.Stderr = ch.Stderr()
	// 非登录式 shell 不会加载 profile，PATH 会沿用宿主机环境变量，
	// 导致 dirname/grep/chmod 等 coreutils 找不到。这里补一段 POSIX 路径。
	c.Env = append(os.Environ(),
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin"+pathSep+os.Getenv("PATH"),
		"HOME="+s.Home,
	)
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

func (s *SSHServer) serve(nc net.Conn, cfg *sshx.ServerConfig) {
	sc, chans, reqs, err := sshx.NewServerConn(nc, cfg)
	if err != nil {
		_ = nc.Close()
		return
	}
	defer sc.Close()
	// 回应在握手阶段协商的全局请求：OpenSSH 保活探测需要应答，否则客户端会判定连接已死。
	go func() {
		for r := range reqs {
			switch r.Type {
			case "keepalive@openssh.com":
				_ = r.Reply(true, nil)
			case "tcpip-forward":
				// 远端端口转发：解析 bind 地址/端口后在服务端监听，连接经 forwarded-tcpip 通道回传客户端。
				if addr, port, ok := parseTCPForward(r.Payload); ok {
					_ = r.Reply(true, nil)
					go s.serveTCPForward(sc, addr, port)
				} else {
					_ = r.Reply(false, nil)
				}
			case "cancel-tcpip-forward":
				_ = r.Reply(true, nil)
			default:
				_ = r.Reply(false, nil)
			}
		}
	}()

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			// 支持 direct-tcpip（本地端口转发用）：解析目标后实际拨号并桥接。
			if newCh.ChannelType() == "direct-tcpip" {
				go s.serveDirectTCP(newCh)
				continue
			}
			_ = newCh.Reject(sshx.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range chReqs {
				switch req.Type {
				case "pty-req":
					_ = req.Reply(true, nil)
				case "shell":
					_ = req.Reply(true, nil)
					_, _ = ch.Write([]byte("\x1b[32mhello\x1b[0m\r\n$ "))
					go func() {
						buf := make([]byte, 256)
						for {
							n, err := ch.Read(buf)
							if n > 0 {
								_, _ = ch.Write([]byte("echo:"))
								_, _ = ch.Write(buf[:n])
							}
							if err != nil {
								return
							}
						}
					}()
				case "window-change":
					_ = req.Reply(true, nil)
				case "exec":
					_ = req.Reply(true, nil)
					// payload 前 4 字节是命令字符串长度
					cmdStr := ""
					if len(req.Payload) > 4 {
						cmdStr = string(req.Payload[4:])
					}
					code := s.runRemoteCmd(ch, cmdStr)
					_, _ = ch.SendRequest("exit-status", false, sshx.Marshal(struct{ Status uint32 }{uint32(code)}))
					_ = ch.Close()
				case "subsystem":
					// payload 前 4 字节是子系统名长度
					name := ""
					if len(req.Payload) > 4 {
						name = string(req.Payload[4:])
					}
					if name == "sftp" {
						_ = req.Reply(true, nil)
						go func() {
							srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.Home))
							if err != nil {
								_ = ch.Close()
								return
							}
							defer srv.Close()
							_ = srv.Serve()
							_ = ch.Close()
						}()
					} else {
						_ = req.Reply(false, nil)
					}
				default:
					_ = req.Reply(false, nil)
				}
				}
				}()
				}
				}

				// serveDirectTCP 处理 direct-tcpip 通道（本地端口转发的「隧道出口」）：
// 解析目标地址后从服务端侧真实拨号，并双向桥接。
// 载荷含 4 个字段（RFC 4254）：目标 host、目标 port、发起方地址、发起方端口。
func (s *SSHServer) serveDirectTCP(newCh sshx.NewChannel) {
	var req struct {
		Host     string
		Port     uint32
		OrigAddr string
		OrigPort uint32
	}
	if err := sshx.Unmarshal(newCh.ExtraData(), &req); err != nil {
		_ = newCh.Reject(sshx.ConnectionFailed, "cannot parse direct-tcpip: "+err.Error())
		return
	}
	ch, chReqs, err := newCh.Accept()
	if err != nil {
		return
	}
	go sshx.DiscardRequests(chReqs)

	target := net.JoinHostPort(req.Host, fmt.Sprintf("%d", req.Port))
	rc, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_, _ = ch.SendRequest("exit-status", false, sshx.Marshal(struct{ Status uint32 }{1}))
		_ = ch.Close()
		return
	}
	go func() {
		defer ch.Close()
		defer rc.Close()
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(ch, rc); done <- struct{}{} }()
		go func() { _, _ = io.Copy(rc, ch); done <- struct{}{} }()
		<-done
	}()
}

// parseTCPForward 解析 tcpip-forward 请求载荷：string(bindAddr) + uint32(bindPort)。
				func parseTCPForward(payload []byte) (addr string, port uint32, ok bool) {
				if len(payload) < 4 {
				return "", 0, false
				}
				addrLen := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
				if len(payload) < 4+addrLen+4 {
				return "", 0, false
				}
				addr = string(payload[4 : 4+addrLen])
				port = uint32(payload[4+addrLen])<<24 | uint32(payload[4+addrLen+1])<<16 | uint32(payload[4+addrLen+2])<<8 | uint32(payload[4+addrLen+3])
				return addr, port, true
				}

				// serveTCPForward 在服务端监听 bindAddr:bindPort，每个连接经 forwarded-tcpip 通道回传客户端。
				func (s *SSHServer) serveTCPForward(sc sshx.Conn, bindAddr string, bindPort uint32) {
				ln, err := net.Listen("tcp", net.JoinHostPort(bindAddr, fmt.Sprintf("%d", bindPort)))
				if err != nil {
				return
				}
				defer ln.Close()
				for {
				conn, err := ln.Accept()
				if err != nil {
				return
				}
				// payload：connected addr/port 用 bind 信息匹配；originator 需为合法 IP 与 1..65535 的端口，
				// 否则客户端 parseTCPAddr 会直接拒绝通道。
				payload := sshx.Marshal(struct {
					ConnectAddr string
					ConnectPort uint32
					OrigAddr    string
					OrigPort    uint32
				}{bindAddr, bindPort, "127.0.0.1", 54321})
				ch, chReqs, err := sc.OpenChannel("forwarded-tcpip", payload)
				if err != nil {
				_ = conn.Close()
				continue
				}
				go sshx.DiscardRequests(chReqs)
				go func() {
				defer conn.Close()
				defer ch.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(ch, conn); done <- struct{}{} }()
				go func() { _, _ = io.Copy(conn, ch); done <- struct{}{} }()
				<-done
				}()
				}
				}
