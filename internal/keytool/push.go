package keytool

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// PushOptions 把公钥写入远端 authorized_keys 的参数。
type PushOptions struct {
	Conn       store.Connection // 目标主机（端口 / 用户名 / 认证方式都取自它）
	Secret     string           // 密码或私钥口令
	PublicKey  string           // 要写入的一整行公钥
	RemotePath string           // 远端 authorized_keys 路径，留空用默认值
	Timeout    time.Duration    // 留空为 30s
}

// PushResult 单台主机的推送结果。
type PushResult struct {
	Name     string // 连接名
	Host     string // user@host:port
	Path     string
	Appended bool   // true=新写入，false=早已存在（已去重跳过）
	Total    int    // 远端 authorized_keys 现有条目数
	Message  string // 远端脚本的原始输出，便于排障
	Err      string // 非空表示失败
}

// Push 把公钥追加到远端 authorized_keys：
// 自动创建目录（700）、修正文件权限（600）、按「算法+密钥体」去重、
// 并在需要时补一个换行再追加，最后尝试 chown 给登录用户。
func Push(o PushOptions) PushResult {
	res := PushResult{
		Name: connName(o.Conn),
		Host: fmt.Sprintf("%s@%s:%d", userDisplay(o.Conn), o.Conn.Host, portDisplay(o.Conn)),
		Path: strings.TrimSpace(o.RemotePath),
	}
	if res.Path == "" {
		res.Path = DefaultRemotePath
	}

	key := strings.TrimSpace(o.PublicKey)
	if key == "" {
		res.Err = "公钥内容为空"
		return res
	}

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	cmd := InstallCommand(key, o.RemotePath)
	out, errOut, err := remotessh.RunOnce(o.Conn, o.Secret, cmd, timeout)
	res.Message = strings.TrimSpace(out)
	if err != nil {
		res.Err = err.Error()
		if msg := strings.TrimSpace(errOut); msg != "" && res.Message == "" {
			res.Message = msg
		}
		if res.Message != "" {
			res.Message += "\n"
		}
		res.Message += "stderr: " + strings.TrimSpace(errOut)
		return res
	}

	res.Appended = strings.Contains(out, "APPENDED")
	res.Total = parseCount(out)
	return res
}

// InstallCommand 生成幂等的远端安装脚本。
// 之所以用一段 shell 而不是一堆 scp/sftp 调用：一次连接搞定权限、去重与追加，
// 既不依赖远端是否有 scp，也天然规避多次写入产生的竞态。
func InstallCommand(publicKey, remotePath string) string {
	p := normalizeRemotePath(remotePath)
	k := shellQuote(publicKey)
	return strings.Join([]string{
		"set -e",
		`P="` + p + `"`,
		`D=$(dirname "$P")`,
		`[ -d "$D" ] || mkdir -p "$D"`,
		`chmod 700 "$D"`,
		`touch "$P"`,
		`chmod 600 "$P"`,
		`K=` + k,
		// 用位置参数拆分出「算法 + 密钥体」，不依赖远端是否安装 awk
		`set -- $K`,
		`B="$1 $2"`,
		`if grep -qF -- "$B" "$P" 2>/dev/null; then`,
		`  echo EXISTS`,
		`else`,
		`  if [ -s "$P" ] && [ -n "$(tail -c 1 "$P")" ]; then printf '\n' >> "$P"; fi`,
		`  printf '%s\n' "$K" >> "$P"`,
		`  echo APPENDED`,
		`fi`,
		`chmod 600 "$P"`,
		`u=$(id -un 2>/dev/null || echo "")`,
		`[ -n "$u" ] && chown "$u" "$P" "$D" 2>/dev/null || true`,
		`printf 'COUNT=%s\n' "$(wc -l < "$P" | tr -d ' ')"`,
		"",
	}, "\n")
}

// shellQuote 用单引号包裹，并把内部的单引号安全转义。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// parseCount 从脚本输出里解析 COUNT=n。
func parseCount(out string) int {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "COUNT=") {
			n, err := strconv.Atoi(strings.TrimSpace(line[6:]))
			if err == nil {
				return n
			}
		}
	}
	return 0
}

func connName(c store.Connection) string {
	if c.Name != "" {
		return c.Name
	}
	return c.Host
}

func userDisplay(c store.Connection) string {
	if c.User != "" {
		return c.User
	}
	return "root"
}

func portDisplay(c store.Connection) int {
	if c.Port > 0 {
		return c.Port
	}
	return 22
}
