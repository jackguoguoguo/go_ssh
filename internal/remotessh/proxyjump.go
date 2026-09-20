package remotessh

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	sshx "golang.org/x/crypto/ssh"

	"sshtool/internal/store"
)

// ProxyJump 通过环境变量 SSHTOOL_PROXY_JUMP 配置（格式 [user@]host[:port]）。
//
// 之所以走环境变量而不是给 store.Connection 加字段：types.go 是冻结契约，
// 不得修改结构体字段。跳板机通常是整机统一的出口，用环境变量配置已足够。
const (
	EnvProxyJump           = "SSHTOOL_PROXY_JUMP"
	EnvProxyJumpPassword   = "SSHTOOL_PROXY_JUMP_PASSWORD"
	EnvProxyJumpPassphrase = "SSHTOOL_PROXY_JUMP_PASSPHRASE"
)

// proxyJumpSpec 解析跳板机配置；未配置或格式非法时 ok=false。
func proxyJumpSpec() (juser, jhost string, jport int, ok bool) {
	v := strings.TrimSpace(os.Getenv(EnvProxyJump))
	if v == "" {
		return "", "", 0, false
	}
	hostport := v
	if i := strings.LastIndex(v, "@"); i >= 0 {
		juser, hostport = v[:i], v[i+1:]
	}
	var portStr string
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		jhost, portStr = hostport[:i], hostport[i+1:]
	} else {
		jhost = hostport
	}
	jport = store.DefaultSSHPort
	if portStr != "" {
		if n, err := strconv.Atoi(portStr); err == nil && n > 0 {
			jport = n
		}
	}
	if jhost == "" {
		return "", "", 0, false
	}
	return juser, jhost, jport, true
}

// ProxyJumpEnabled 报告当前是否配置了跳板机。
func ProxyJumpEnabled() bool {
	_, _, _, ok := proxyJumpSpec()
	return ok
}

// jumpAuthMethods 构造跳板机的认证方式：ssh-agent → 默认私钥（可带口令）→ 环境变量里的密码。
// 跳板机通常只接受公钥，故优先 agent 与默认私钥。
func jumpAuthMethods() []sshx.AuthMethod {
	var methods []sshx.AuthMethod
	if am := agentAuthMethod(); am != nil {
		methods = append(methods, am)
	}
	if home, err := os.UserHomeDir(); err == nil {
		passphrase := os.Getenv(EnvProxyJumpPassphrase)
		for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
			pem, err := os.ReadFile(filepath.Join(home, ".ssh", name))
			if err != nil {
				continue
			}
			signer, err := sshx.ParsePrivateKey(pem)
			if err != nil && passphrase != "" {
				signer, err = sshx.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
			}
			if err == nil {
				methods = append(methods, sshx.PublicKeys(signer))
			}
		}
	}
	if pw := os.Getenv(EnvProxyJumpPassword); pw != "" {
		methods = append(methods, sshx.Password(pw))
	}
	return methods
}

// dialClient 建立到 addr 的 SSH 客户端。配置了跳板机时先连跳板机再在其通道上握手，
// 并一并返回跳板机客户端（调用方需在其不再需要时关闭）。
func dialClient(addr string, cfg *sshx.ClientConfig) (*sshx.Client, *sshx.Client, error) {
	juser, jhost, jport, ok := proxyJumpSpec()
	if !ok {
		client, err := sshx.Dial("tcp", addr, cfg)
		return client, nil, err
	}
	return dialViaJump(juser, jhost, jport, addr, cfg)
}

// dialViaJump 经跳板机建立到 addr 的 SSH 连接。
func dialViaJump(juser, jhost string, jport int, addr string, cfg *sshx.ClientConfig) (*sshx.Client, *sshx.Client, error) {
	if juser == "" {
		if u, err := user.Current(); err == nil && u.Username != "" {
			juser = u.Username
		}
	}
	jumpCfg := &sshx.ClientConfig{
		User:            juser,
		Auth:            jumpAuthMethods(),
		HostKeyCallback: hostKeyCallback(),
		Timeout:         DefaultDialTimeout,
	}
	jump, err := sshx.Dial("tcp", net.JoinHostPort(jhost, itoa(jport)), jumpCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("连接跳板机 %s 失败: %w", jhost, err)
	}

	conn, err := jump.Dial("tcp", addr)
	if err != nil {
		_ = jump.Close()
		return nil, nil, fmt.Errorf("经跳板机转发到 %s 失败: %w", addr, err)
	}
	ncc, chans, reqs, err := sshx.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = jump.Close()
		return nil, nil, fmt.Errorf("经跳板机握手失败: %w", err)
	}
	// 注意：jump 不能在此时关闭——目标连接建立在它的通道之上。
	return sshx.NewClient(ncc, chans, reqs), jump, nil
}
