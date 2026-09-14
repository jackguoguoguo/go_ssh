package remotessh

import (
	"fmt"
	"strings"
	"time"

	sshx "golang.org/x/crypto/ssh"

	"sshtool/internal/store"
)

// RunOnce 建立一次短连接执行单条命令，不申请 PTY，执行完立即断开。
// 适用于「把公钥写进 authorized_keys」这类一次性运维操作。
//
// timeout 同时作用于 TCP/SSH 握手与整条命令的执行（握手段为 DefaultDialTimeout）。
func RunOnce(conn store.Connection, secret, cmd string, timeout time.Duration) (stdout, stderr string, err error) {
	methods, err := authMethods(conn, secret)
	if err != nil {
		return "", "", err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	cfg := &sshx.ClientConfig{
		User:            userOf(conn),
		Auth:            methods,
		HostKeyCallback: hostKeyCallback(),
		Timeout:         DefaultDialTimeout,
	}

	client, err := sshx.Dial("tcp", addrOf(conn), cfg)
	if err != nil {
		return "", "", err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer sess.Close()

	var out, errb strings.Builder
	sess.Stdout = &out
	sess.Stderr = &errb

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case err = <-done:
	case <-time.After(timeout):
		_ = sess.Signal(sshx.SIGKILL)
		return out.String(), errb.String(), fmt.Errorf("命令执行超时（%s）", timeout)
	}

	return out.String(), errb.String(), err
}
