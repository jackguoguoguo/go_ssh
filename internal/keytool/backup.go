package keytool

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// 备份文件格式：magic(4) + version(1) + salt(32) + nonce(12) + ciphertext。
var (
	backupMagic   = []byte("SSKB")
	backupVersion = byte(1)
)

// scrypt 参数（Go 官方建议的量级，兼顾安全性与耗时）。
const (
	scryptN      = 1 << 15 // 32768
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
)

// BackupManifest 描述一次备份的内容，随归档一起加密保存。
type BackupManifest struct {
	CreatedAt time.Time      `json:"created_at"`
	SSHDir    string         `json:"ssh_dir"`
	Files     []string       `json:"files"`
	Keys      []BackupKeyRef `json:"keys,omitempty"`
}

// BackupKeyRef 备份里一把密钥的元数据（用于恢复时校验指纹）。
type BackupKeyRef struct {
	Alg         string `json:"alg"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment,omitempty"`
	PrivatePath string `json:"private_path,omitempty"`
	PublicPath  string `json:"public_path,omitempty"`
}

// BackupDir 返回备份目录：~/.sshtool/backups。
func BackupDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".sshtool", "backups")
	}
	return filepath.Join(home, ".sshtool", "backups")
}

// Backup 把 sshDir 下的 id_*（私钥 + .pub）、config、known_hosts 打成加密归档。
// 归档落盘到 ~/.sshtool/backups/ssh-keys-<时间戳>.enc，返回其路径。
//
// 未加密的私钥压缩包放在磁盘上是明显的安全回退，因此整体用口令派生密钥加密
// （scrypt + AES-256-GCM）。
func Backup(sshDir, passphrase string) (string, error) {
	if sshDir == "" {
		sshDir = DefaultDir()
	}
	if passphrase == "" {
		return "", errors.New("备份需要口令（用于加密私钥，请牢记）")
	}

	files, err := collectBackupFiles(sshDir)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("%s 下没有可备份的密钥或配置文件", sshDir)
	}

	man := BackupManifest{CreatedAt: time.Now(), SSHDir: sshDir, Files: make([]string, 0, len(files))}
	for _, f := range files {
		man.Files = append(man.Files, filepath.Base(f))
	}
	man.Keys = manifestKeys(sshDir, files)

	var buf bytes.Buffer
	if err := writeTarGz(&buf, sshDir, files, man); err != nil {
		return "", err
	}

	blob, err := encrypt(buf.Bytes(), passphrase)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(BackupDir(), 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(BackupDir(), "ssh-keys-"+time.Now().Format("20060102-150405")+".enc")
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// collectBackupFiles 收集需要备份的文件：id_*（私钥与其 .pub）、config、known_hosts。
// 只收 id_*，避免把无关文件混进备份。
func collectBackupFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("目录不存在：%s", dir)
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "id_") || name == "config" || name == "known_hosts" {
			out = append(out, filepath.Join(dir, name))
		}
	}
	sort.Strings(out)
	return out, nil
}

// manifestKeys 为每个 .pub 文件生成元数据（算法 / 指纹 / 注释）。
func manifestKeys(dir string, files []string) []BackupKeyRef {
	var refs []BackupKeyRef
	for _, f := range files {
		if !strings.HasSuffix(f, ".pub") {
			continue
		}
		info, err := ReadPublic(f)
		if err != nil {
			continue
		}
		refs = append(refs, BackupKeyRef{
			Alg:         info.Alg,
			Fingerprint: info.Fingerprint,
			Comment:     info.Comment,
			PrivatePath: strings.TrimSuffix(f, ".pub"),
			PublicPath:  f,
		})
	}
	return refs
}

// writeTarGz 把文件与 manifest.json 写入 tar.gz。
func writeTarGz(w io.Writer, baseDir string, files []string, man BackupManifest) error {
	manData, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	if err := tw.WriteHeader(&tar.Header{
		Name:    "manifest.json",
		Mode:    0o600,
		Size:    int64(len(manData)),
		ModTime: man.CreatedAt,
	}); err != nil {
		return err
	}
	if _, err := tw.Write(manData); err != nil {
		return err
	}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		fi, err := os.Stat(f)
		if err != nil {
			return err
		}
		mode := int64(0o600)
		if strings.HasSuffix(f, ".pub") {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    filepath.Base(f),
			Mode:    mode,
			Size:    int64(len(data)),
			ModTime: fi.ModTime(),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// encrypt 用 scrypt 派生的密钥做 AES-256-GCM 加密。
func encrypt(plain []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(backupMagic)+1+len(salt)+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, backupMagic...)
	out = append(out, backupVersion)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plain, nil)
	return out, nil
}

// decrypt 解密备份文件；口令错误或文件损坏时返回错误。
func decrypt(blob []byte, passphrase string) ([]byte, error) {
	minLen := len(backupMagic) + 1 + 32 + 12
	if len(blob) < minLen+gcmTagSize {
		return nil, errors.New("备份文件过短或已损坏")
	}
	if !bytes.Equal(blob[:len(backupMagic)], backupMagic) {
		return nil, errors.New("不是有效的 sshtool 备份文件")
	}
	ver := blob[len(backupMagic)]
	if ver != backupVersion {
		return nil, fmt.Errorf("备份版本不支持：%d", ver)
	}
	off := len(backupMagic) + 1
	salt := blob[off : off+32]
	off += 32

	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := blob[off : off+gcm.NonceSize()]
	off += gcm.NonceSize()
	return gcm.Open(nil, nonce, blob[off:], nil)
}

// AES-GCM 标准 tag 长度。
const gcmTagSize = 16

// BackupInfo 一个备份文件的概要（列出备份用，无需口令）。
type BackupInfo struct {
	Path string
	Size int64
	When time.Time
}

// ListBackups 列出备份目录中的全部备份（按时间倒序）。
func ListBackups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(BackupDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".enc") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{
			Path: filepath.Join(BackupDir(), e.Name()),
			Size: fi.Size(),
			When: fi.ModTime(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out, nil
}

// ReadManifest 解密备份并读取其中的 manifest（用于恢复前校验指纹）。
func ReadManifest(archive, passphrase string) (BackupManifest, error) {
	blob, err := os.ReadFile(archive)
	if err != nil {
		return BackupManifest{}, err
	}
	plain, err := decrypt(blob, passphrase)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("解密失败（口令错误或文件损坏）：%w", err)
	}
	files, man, err := readTarGz(plain)
	if err != nil {
		return BackupManifest{}, err
	}
	man.Files = nil
	for _, f := range files {
		if f.Name == "manifest.json" {
			continue
		}
		man.Files = append(man.Files, f.Name)
	}
	return man, nil
}

// tarEntry 归档中的一个条目。
type tarEntry struct {
	Name string
	Data []byte
	Mode int64
}

// readTarGz 解析 tar.gz，返回其中的条目。
func readTarGz(data []byte) ([]tarEntry, BackupManifest, error) {
	var man BackupManifest
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, man, fmt.Errorf("解压失败：%w", err)
	}
	defer gr.Close()

	var out []tarEntry
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, man, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return nil, man, err
		}
		if filepath.Base(h.Name) == "manifest.json" {
			_ = json.Unmarshal(buf, &man)
			continue
		}
		out = append(out, tarEntry{Name: filepath.Base(h.Name), Data: buf, Mode: h.Mode})
	}
	return out, man, nil
}

// RestoreMode 恢复时遇到同名文件的处理策略。
type RestoreMode int

const (
	RestoreOverwrite RestoreMode = iota // 覆盖同名文件
	RestoreRename                       // 改名导入（加 .restored-<时间戳> 后缀）
)

// Restore 解密备份并把文件还原到 sshDir。
// 返回被写入的文件名列表；恢复前会按 manifest 校验每个 .pub 的指纹。
func Restore(archive, passphrase, sshDir string, mode RestoreMode) ([]string, error) {
	if sshDir == "" {
		sshDir = DefaultDir()
	}
	blob, err := os.ReadFile(archive)
	if err != nil {
		return nil, err
	}
	plain, err := decrypt(blob, passphrase)
	if err != nil {
		return nil, fmt.Errorf("解密失败（口令错误或文件损坏）：%w", err)
	}
	entries, man, err := readTarGz(plain)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("备份内容为空")
	}

	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, err
	}

	stamp := time.Now().Format("20060102-150405")
	var written []string
	for _, e := range entries {
		name := e.Name
		if mode == RestoreRename {
			if _, err := os.Stat(filepath.Join(sshDir, name)); err == nil {
				name += ".restored-" + stamp
			}
		}
		perm := os.FileMode(0o600)
		if strings.HasSuffix(name, ".pub") {
			perm = 0o644
		}
		if err := os.WriteFile(filepath.Join(sshDir, name), e.Data, perm); err != nil {
			return written, err
		}
		written = append(written, name)
	}

	// 指纹校验：恢复后的 .pub 指纹应与 manifest 一致。
	if errs := verifyRestored(sshDir, man); len(errs) > 0 {
		return written, errors.New(strings.Join(errs, "；"))
	}
	return written, nil
}

// verifyRestored 按 manifest 校验已恢复的公钥指纹。
func verifyRestored(sshDir string, man BackupManifest) []string {
	var errs []string
	for _, ref := range man.Keys {
		if ref.PublicPath == "" {
			continue
		}
		p := filepath.Join(sshDir, filepath.Base(ref.PublicPath))
		info, err := ReadPublic(p)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s 校验失败：%v", filepath.Base(p), err))
			continue
		}
		if info.Fingerprint != ref.Fingerprint {
			errs = append(errs, fmt.Sprintf("%s 指纹不符（期望 %s，实际 %s）", filepath.Base(p), ref.Fingerprint, info.Fingerprint))
		}
	}
	return errs
}

// ---------- 推送前的远端快照（快照式保险） ----------

// SnapshotRemoteAuthorizedKeys 读取远端 authorized_keys 的内容。
func SnapshotRemoteAuthorizedKeys(conn store.Connection, secret, remotePath string, timeout time.Duration) (string, error) {
	p := strings.TrimSpace(remotePath)
	if p == "" {
		p = DefaultRemotePath
	}
	out, errOut, err := remotessh.RunOnce(conn, secret, "cat "+shellQuote(normalizeRemotePath(p)), timeout)
	if err != nil {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("读取远端 %s 失败：%s", p, msg)
	}
	return out, nil
}

// SaveRemoteSnapshot 把远端 authorized_keys 内容存进备份目录，便于误删后回滚。
// 落盘到 ~/.sshtool/backups/remote/<sanitized host>/<时间戳>.authorized_keys。
func SaveRemoteSnapshot(host, content string) (string, error) {
	dir := filepath.Join(BackupDir(), "remote", sanitizeForPath(host))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+".authorized_keys")
	if !strings.HasSuffix(content, "\n") && content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// sanitizeForPath 把主机标识里的非法字符替换为下划线。
func sanitizeForPath(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ', '\t':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
