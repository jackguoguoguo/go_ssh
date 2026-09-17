// Package ssh 封装 SSH 会话的生命周期管理：认证、PTY、多会话注册与事件通知。
//
// 每个 Session 内部持有一个 vt.Terminal 屏幕缓冲区：远端输出在后台协程中被
// 直接写入该缓冲区，UI 只需要读取并渲染，从而避免大块数据在主线程拷贝。
package remotessh

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	sshx "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshtool/internal/store"
)

// ErrNeedPassphrase 表示私钥被加密，需要用户提供私钥口令。
var ErrNeedPassphrase = errors.New("ssh: 私钥已加密，需要 passphrase")

// ErrNeedPassword 表示服务端要求密码但本地没有可用密码。
var ErrNeedPassword = errors.New("ssh: 需要密码")

// DefaultDialTimeout 建立 TCP 与 SSH 握手的超时时间。
const DefaultDialTimeout = 12 * time.Second

// addrOf 根据连接配置拼出 host:port。
func addrOf(conn store.Connection) string {
	port := conn.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(conn.Host, itoa(port))
}

// userOf 返回登录用户名，缺省为 root。
func userOf(conn store.Connection) string {
	if conn.User != "" {
		return conn.User
	}
	return "root"
}

// authMethods 根据连接配置构造认证方式列表。
// secret 的含义取决于配置：密码认证时为登录密码；私钥认证时为私钥口令（可为空）。
func authMethods(conn store.Connection, secret string) ([]sshx.AuthMethod, error) {
	if conn.AuthType == store.AuthKey {
		keyPath := conn.KeyPath
		if keyPath == "" {
			home, _ := os.UserHomeDir()
			keyPath = filepath.Join(home, ".ssh", "id_rsa")
		}
		keyPath = expandHome(keyPath)

		pem, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("读取私钥失败: %w", err)
		}

		var signer sshx.Signer
		if secret != "" {
			signer, err = sshx.ParsePrivateKeyWithPassphrase(pem, []byte(secret))
			if err != nil {
				return nil, fmt.Errorf("解析私钥失败（口令可能不正确）: %w", err)
			}
		} else {
			signer, err = sshx.ParsePrivateKey(pem)
			if err != nil {
				// 绝大多数加密私钥在这里会失败，交由上层提示用户输入口令后重试。
				return nil, ErrNeedPassphrase
			}
		}
		return []sshx.AuthMethod{sshx.PublicKeys(signer)}, nil
	}

	// 密码认证：同时挂上 keyboard-interactive，兼容只支持交互式认证的服务端。
	methods := make([]sshx.AuthMethod, 0, 2)
	if secret != "" {
		methods = append(methods, sshx.Password(secret))
		methods = append(methods, sshx.KeyboardInteractive(
			func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = secret
				}
				return answers, nil
			},
		))
	} else {
		methods = append(methods, sshx.KeyboardInteractive(
			func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				return nil, ErrNeedPassword
			},
		))
	}
	return methods, nil
}

// hostKeyCallback 严格校验主机指纹：以 ~/.ssh/known_hosts 为准（文件缺失时创建）。
//
// 握手发生在后台协程里，无法就地弹窗询问，因此这里不阻塞等待用户决策，
// 而是把「未知主机 / 指纹变更」统一转成 *HostKeyError 返回：
//   - 交互会话：由 Session.fail 转成 EventNeedHostKey，UI 弹确认框，
//     用户同意后调用 TrustHost 落盘再重连；
//   - 非交互调用（RunOnce，例如批量推送公钥）：直接失败，绝不静默放通。
//
// 逃生舱：设置 SSHTOOL_INSECURE_HOST_KEYS=1 可退回不校验（仅供批量脚本）。
func hostKeyCallback() sshx.HostKeyCallback {
	return func(hostname string, remote net.Addr, key sshx.PublicKey) error {
		if insecureHostKeys() {
			return nil
		}
		path, err := knownHostsPath()
		if err != nil {
			return err
		}
		if err := ensureKnownHostsFile(path); err != nil {
			return err
		}
		cb, err := knownhosts.New(path)
		if err != nil {
			return fmt.Errorf("解析 known_hosts 失败: %w", err)
		}
		if err := cb(hostname, remote, key); err != nil {
			var ke *knownhosts.KeyError
			if errors.As(err, &ke) {
				return &HostKeyError{
					Addr:        hostname,
					Type:        key.Type(),
					Fingerprint: sshx.FingerprintSHA256(key),
					Changed:     len(ke.Want) > 0,
					Want:        ke.Want,
					Key:         key,
				}
			}
			return err
		}
		return nil
	}
}

// expandHome 把路径开头的 ~ 展开为用户目录。
func expandHome(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if len(p) == 1 {
		return home
	}
	return filepath.Join(home, p[2:])
}
