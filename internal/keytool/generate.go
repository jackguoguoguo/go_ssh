// Package sshkey 提供 SSH 密钥的生成、本机管理与公钥分发能力。
//
// 与 internal/remotessh 的分工：本包只关心「密钥本身」，
// 远程执行通过 remotessh.RunOnce 完成，不申请 PTY。
package keytool

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sshx "golang.org/x/crypto/ssh"
)

// Alg 密钥算法。
type Alg string

const (
	Ed25519  Alg = "ed25519"
	ECDSA256 Alg = "ecdsa-256"
	RSA2048  Alg = "rsa-2048"
	RSA4096  Alg = "rsa-4096"
)

// AlgOptions 供 UI 展示与切换的算法列表（按推荐顺序）。
var AlgOptions = []string{string(Ed25519), string(ECDSA256), string(RSA4096), string(RSA2048)}

// AlgDesc 算法的人话说明与生成耗时提示。
func AlgDesc(a Alg) string {
	switch a {
	case Ed25519:
		return "推荐：最短、最快、最安全，新服务器普遍支持"
	case ECDSA256:
		return "NIST P-256，兼容性好于 ed25519"
	case RSA4096:
		return "兼容性最好，但生成较慢（约 1-10 秒）"
	case RSA2048:
		return "旧设备兜底，仅在不支持以上算法时使用"
	}
	return ""
}

// GenOptions 生成密钥的参数。
type GenOptions struct {
	Alg        Alg    // 算法
	Comment    string // 注释，通常填 user@host
	Path       string // 私钥保存路径，公钥自动加 .pub
	Passphrase string // 私钥口令，可为空
	Overwrite  bool   // 是否允许覆盖已存在的文件
}

// KeyInfo 生成或读取到的密钥信息。
type KeyInfo struct {
	Alg         string
	PrivatePath string
	PublicPath  string
	PublicKey   string // authorized_keys 里的一整行
	Comment     string
	Fingerprint string
	HasPrivate  bool
	ModTime     time.Time
}

// SuggestPath 返回某种算法的默认私钥路径（~/.ssh/id_<alg>）。
func SuggestPath(alg Alg) string {
	name := "id_ed25519"
	switch alg {
	case ECDSA256:
		name = "id_ecdsa"
	case RSA2048, RSA4096:
		name = "id_rsa"
	}
	return filepath.Join(DefaultDir(), name)
}

// generate 产出私钥；第二个返回值用于 sshx.MarshalPrivateKey（要求指针类型）。
func generate(alg Alg) (sshx.Signer, any, error) {
	switch alg {
	case Ed25519:
		_, priv, err := ed25519.GenerateKey(cryptorand.Reader)
		if err != nil {
			return nil, nil, err
		}
		signer, err := sshx.NewSignerFromKey(priv)
		return signer, &priv, err

	case ECDSA256:
		priv, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
		if err != nil {
			return nil, nil, err
		}
		signer, err := sshx.NewSignerFromKey(priv)
		return signer, priv, err

	case RSA2048, RSA4096:
		bits := 2048
		if alg == RSA4096 {
			bits = 4096
		}
		priv, err := rsa.GenerateKey(cryptorand.Reader, bits)
		if err != nil {
			return nil, nil, err
		}
		signer, err := sshx.NewSignerFromKey(priv)
		return signer, priv, err
	}
	return nil, nil, fmt.Errorf("sshkey: 不支持的算法 %q", alg)
}

// Generate 生成一对密钥并落盘，返回结果信息（含公钥内容与指纹）。
func Generate(o GenOptions) (KeyInfo, error) {
	if o.Alg == "" {
		o.Alg = Ed25519
	}
	privPath := ExpandHome(o.Path)
	if privPath == "" {
		privPath = SuggestPath(o.Alg)
	}
	pubPath := privPath + ".pub"
	dir := filepath.Dir(privPath)

	if !o.Overwrite {
		for _, p := range []string{privPath, pubPath} {
			if _, err := os.Stat(p); err == nil {
				return KeyInfo{}, fmt.Errorf("文件已存在：%s（勾选覆盖或换一个路径）", p)
			}
		}
	}

	signer, marshalable, err := generate(o.Alg)
	if err != nil {
		return KeyInfo{}, err
	}

	var block *pem.Block
	if o.Passphrase != "" {
		block, err = sshx.MarshalPrivateKeyWithPassphrase(marshalable, o.Comment, []byte(o.Passphrase))
	} else {
		block, err = sshx.MarshalPrivateKey(marshalable, o.Comment)
	}
	if err != nil {
		return KeyInfo{}, fmt.Errorf("序列化私钥失败: %w", err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return KeyInfo{}, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return KeyInfo{}, fmt.Errorf("写入私钥失败: %w", err)
	}

	// MarshalAuthorizedKey 只输出「算法 + 公钥体」，注释要自己拼上。
	pub := strings.TrimSpace(string(sshx.MarshalAuthorizedKey(signer.PublicKey())))
	if c := strings.TrimSpace(o.Comment); c != "" {
		pub += " " + c
	}
	if err := os.WriteFile(pubPath, []byte(pub+"\n"), 0o644); err != nil {
		return KeyInfo{}, fmt.Errorf("写入公钥失败: %w", err)
	}
	// WriteFile 只在「新建」时应用 mode；覆盖已有文件时会沿用旧权限，
	// 因此这里再显式收紧一次（Windows 上 Chmod 能力有限，属尽力而为）。
	_ = os.Chmod(privPath, 0o600)
	_ = os.Chmod(pubPath, 0o644)

	return KeyInfo{
		Alg:         signer.PublicKey().Type(), // 线上名字，如 ssh-ed25519 / ecdsa-sha2-nistp256
		PrivatePath: privPath,
		PublicPath:  pubPath,
		PublicKey:   pub,
		Comment:     commentOf(pub),
		Fingerprint: sshx.FingerprintSHA256(signer.PublicKey()),
		HasPrivate:  true,
		ModTime:     time.Now(),
	}, nil
}

// ReadPublic 读取一个公钥文件（或已保存的公钥行）并补全元信息。
func ReadPublic(pubPath string) (KeyInfo, error) {
	pubPath = ExpandHome(pubPath)
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return KeyInfo{}, err
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return KeyInfo{}, errors.New("sshkey: 公钥文件为空")
	}
	info, err := ParseLine(line)
	if err != nil {
		return KeyInfo{}, err
	}
	info.PublicPath = pubPath
	priv := strings.TrimSuffix(pubPath, ".pub")
	if st, err := os.Stat(priv); err == nil {
		info.PrivatePath = priv
		info.HasPrivate = true
		info.ModTime = st.ModTime()
	}
	return info, nil
}

// ParseLine 解析一行 authorized_keys / .pub 内容。
func ParseLine(line string) (KeyInfo, error) {
	pub, comment, _, _, err := sshx.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return KeyInfo{}, fmt.Errorf("解析公钥失败: %w", err)
	}
	return KeyInfo{
		Alg:         pub.Type(),
		PublicKey:   strings.TrimSpace(line),
		Comment:     comment,
		Fingerprint: sshx.FingerprintSHA256(pub),
	}, nil
}

// commentOf 从一行 authorized_keys 里取注释部分（第 3 列及之后）。
func commentOf(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return ""
	}
	return strings.Join(fields[2:], " ")
}

// KeyBody 取用于去重比较的密钥主体（算法 + base64 部分，忽略注释）。
func KeyBody(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return strings.TrimSpace(line)
	}
	return fields[0] + " " + fields[1]
}
