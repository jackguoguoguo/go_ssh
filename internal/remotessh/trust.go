package remotessh

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sshx "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// insecureHostKeysEnv 是关闭主机指纹校验的逃生舱环境变量。
// 仅用于无法交互的场景（例如批量脚本），正常交互使用请勿开启：
// 一旦开启，中间人可以完全冒充目标主机。
const insecureHostKeysEnv = "SSHTOOL_INSECURE_HOST_KEYS"

// KnownHostsEnv 允许用环境变量指定 known_hosts 的路径。
// 正常使用时不需要；测试与绿色版（便携）场景下可以用来指向另一份文件。
const KnownHostsEnv = "SSHTOOL_KNOWN_HOSTS"

// HostKeyError 表示主机指纹校验未通过。
//
// 它与「连不上」这类错误的区别在于：它是**可决策**的。
// 未知主机（Changed=false）可以经用户确认后写入 known_hosts 并重试；
// 指纹变更（Changed=true）一律硬拒绝，必须由用户手动移除旧记录，
// 因为指纹变化既可能是主机重装，也可能是中间人攻击，程序无法区分。
type HostKeyError struct {
	Addr        string                // 形如 host:port
	Type        string                // 密钥算法，如 ssh-ed25519
	Fingerprint string                // SHA256 指纹
	Changed     bool                  // true 表示与 known_hosts 中已有记录冲突
	Want        []knownhosts.KnownKey // Changed 时已知的记录（knownhosts.KeyError.Want）
	Key         sshx.PublicKey        // 本次握手拿到的主机公钥，用于确认后落盘
}

// Error 实现 error。
func (e *HostKeyError) Error() string {
	if e.Changed {
		return fmt.Sprintf("主机 %s 的指纹与 known_hosts 中的记录不一致（本次为 %s %s）。"+
			"若确认主机已重装或更换密钥，请先执行 ssh-keygen -R %s 再重连",
			e.Addr, e.Type, e.Fingerprint, hostOfAddr(e.Addr))
	}
	return fmt.Sprintf("未知主机 %s（%s 指纹 %s），需确认后才会写入 known_hosts",
		e.Addr, e.Type, e.Fingerprint)
}

// hostOfAddr 去掉端口，用于 ssh-keygen -R 提示。
func hostOfAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i > 0 && !strings.Contains(addr[i:], "]") {
		return addr[:i]
	}
	return addr
}

// knownHostsPath 返回（必要时创建）known_hosts 的路径。
// 定义为变量以便测试替换。
var knownHostsPath = defaultKnownHostsPath

func defaultKnownHostsPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv(KnownHostsEnv)); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("无法定位用户主目录，known_hosts 不可用")
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建 %s 失败: %w", dir, err)
	}
	p := filepath.Join(dir, "known_hosts")
	if err := ensureKnownHostsFile(p); err != nil {
		return "", err
	}
	return p, nil
}

// ensureKnownHostsFile 保证文件存在：knownhosts.New 在文件缺失时会直接报错，
// 而「首次使用、还没有 known_hosts」恰恰是最常见的场景。
func ensureKnownHostsFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("访问 known_hosts 失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建 known_hosts 失败: %w", err)
	}
	return f.Close()
}

// insecureHostKeys 是否通过环境变量关闭了指纹校验。
func insecureHostKeys() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(insecureHostKeysEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// trustMu 串行化对 known_hosts 的写入：多个会话可能同时首次连接不同主机，
// 虽然 O_APPEND 的单次写入通常不会交错，但配合「读-判重-写」后就必须互斥，
// 否则两个协程会各写一份重复条目。
var trustMu sync.Mutex

// TrustHost 把 addr 的主机公钥写入 known_hosts（幂等）。
// addr 形如 host:port，内部按 OpenSSH 规则规范化（22 端口省略、其它写成 [host]:port）。
func TrustHost(addr string, key sshx.PublicKey) error {
	if key == nil {
		return errors.New("trust: 缺少主机公钥")
	}
	path, err := knownHostsPath()
	if err != nil {
		return err
	}
	return trustHostToFile(path, addr, key)
}

// trustHostToFile 是 TrustHost 的可测试实现。
func trustHostToFile(path, addr string, key sshx.PublicKey) error {
	trustMu.Lock()
	defer trustMu.Unlock()

	norm := knownhosts.Normalize(addr)
	blob := base64.StdEncoding.EncodeToString(key.Marshal())

	// 读-判重-写：已存在同主机同密钥时直接返回，保证幂等。
	if old, err := os.ReadFile(path); err == nil {
		for _, l := range strings.Split(string(old), "\n") {
			if hasTrustedKey(l, norm, blob) {
				return nil
			}
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("写入 known_hosts 失败: %w", err)
	}
	line := knownhosts.Line([]string{norm}, key)
	if _, err := f.WriteString(line + "\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入 known_hosts 失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// hasTrustedKey 判断 known_hosts 的一行是否为「主机 norm 已信任密钥 blob」。
func hasTrustedKey(line, norm, blob string) bool {
	l := strings.TrimSpace(line)
	if l == "" || strings.HasPrefix(l, "#") {
		return false
	}
	fields := strings.Fields(l)
	if len(fields) < 2 {
		return false
	}
	// 跳过 @cert-authority / @revoked 之类的标记列
	i := 0
	if strings.HasPrefix(fields[0], "@") {
		i = 1
	}
	if i >= len(fields) {
		return false
	}
	if !hostListMatches(fields[i], norm) {
		return false
	}
	for _, f := range fields[i+1:] {
		if f == blob {
			return true
		}
	}
	return false
}

// hostListMatches 判断 known_hosts 的主机模式列（逗号分隔，可含通配符）是否覆盖 norm。
func hostListMatches(pattern, norm string) bool {
	for _, p := range strings.Split(pattern, ",") {
		if p == norm {
			return true
		}
		if strings.Contains(p, "*") || strings.Contains(p, "?") {
			if ok, _ := filepath.Match(p, norm); ok {
				return true
			}
		}
	}
	return false
}
