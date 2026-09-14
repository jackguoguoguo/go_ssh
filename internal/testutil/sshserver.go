// Package testutil 提供测试用的进程内 SSH 服务端。
package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"testing"

	sshx "golang.org/x/crypto/ssh"
)

// SSHServer 是一个仅用于测试的 SSH 服务端。
type SSHServer struct {
	Host string
	Port int
	ln   net.Listener
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

	s := &SSHServer{ln: ln}
	addr := ln.Addr().(*net.TCPAddr)
	s.Host, s.Port = addr.IP.String(), addr.Port

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

func (s *SSHServer) serve(nc net.Conn, cfg *sshx.ServerConfig) {
	sc, chans, reqs, err := sshx.NewServerConn(nc, cfg)
	if err != nil {
		_ = nc.Close()
		return
	}
	defer sc.Close()
	go sshx.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
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
				default:
					_ = req.Reply(false, nil)
				}
			}
		}()
	}
}
