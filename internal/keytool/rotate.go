package keytool

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// RotateOptions 一轮密钥轮换的参数。
type RotateOptions struct {
	Conn        store.Connection // 目标主机
	Secret      string           // 当前（旧）凭据，用于分发与移除
	NewPublic   string           // 新公钥行
	NewKeyPath  string           // 新私钥路径（用于验证新密钥能否登录）
	NewKeyPass  string           // 新私钥口令（可为空）
	OldPublic   string           // 旧公钥行（验证成功后移除）
	RemotePath  string           // 远端 authorized_keys 路径
	Timeout     time.Duration
	KeepOld     bool // true=验证成功后仍保留旧密钥（不移除）
}

// RotateResult 单台主机的轮换结果。
type RotateResult struct {
	Name     string   // 连接名
	Host     string   // user@host:port
	Path     string
	Phases   []string // 各阶段结果，便于排障
	Appended bool     // 新密钥是否新写入（false=早已存在）
	Verified bool     // 新密钥是否验证可登录
	Removed  bool     // 旧密钥是否已移除
	Err      string   // 非空表示失败（失败时会保留旧密钥，避免把自己锁在门外）
}

// Rotate 对单台主机执行「分发新密钥 → 验证新密钥可登录 → 移除旧密钥」。
// 任一阶段失败即中止并保留旧密钥，绝不静默删除——这是轮换最重要的安全属性。
func Rotate(o RotateOptions) RotateResult {
	res := RotateResult{
		Name: connName(o.Conn),
		Host: fmt.Sprintf("%s@%s:%d", userDisplay(o.Conn), o.Conn.Host, portDisplay(o.Conn)),
		Path: strings.TrimSpace(o.RemotePath),
	}
	if res.Path == "" {
		res.Path = DefaultRemotePath
	}
	if strings.TrimSpace(o.NewPublic) == "" {
		res.Err = "新公钥为空"
		return res
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// ① 分发新密钥
	push := Push(PushOptions{Conn: o.Conn, Secret: o.Secret, PublicKey: o.NewPublic, RemotePath: o.RemotePath, Timeout: timeout})
	if push.Err != "" {
		res.Err = "分发新密钥失败：" + push.Err
		res.Phases = append(res.Phases, "① 分发新密钥 ✗ "+push.Err)
		return res
	}
	res.Appended = push.Appended
	res.Phases = append(res.Phases, fmt.Sprintf("① 分发新密钥 ✓（%s，远端共 %d 条）", phaseWord(push.Appended), push.Total))

	// ② 用新密钥验证能否登录（失败则保留旧密钥）
	verifyConn := o.Conn
	verifyConn.AuthType = store.AuthKey
	verifyConn.KeyPath = o.NewKeyPath
	verifyConn.AskPassphrase = false
	_, _, err := remotessh.RunOnce(verifyConn, o.NewKeyPass, "true", timeout)
	if err != nil {
		res.Err = "新密钥验证登录失败（已保留旧密钥）：" + err.Error()
		res.Phases = append(res.Phases, "② 验证新密钥 ✗ "+err.Error())
		return res
	}
	res.Verified = true
	res.Phases = append(res.Phases, "② 验证新密钥 ✓ 可用新私钥登录")

	// ③ 移除旧密钥（可选保留）
	if o.KeepOld || strings.TrimSpace(o.OldPublic) == "" {
		res.Phases = append(res.Phases, "③ 移除旧密钥 — 已按要求保留")
		return res
	}
	removed, total, err := RemoveAuthorizedKey(o.Conn, o.Secret, o.OldPublic, o.RemotePath, timeout)
	if err != nil {
		res.Err = "移除旧密钥失败：" + err.Error()
		res.Phases = append(res.Phases, "③ 移除旧密钥 ✗ "+err.Error())
		return res
	}
	res.Removed = removed
	res.Phases = append(res.Phases, fmt.Sprintf("③ 移除旧密钥 ✓（远端剩余 %d 条）", total))
	return res
}

// RotateAll 对多台主机并发执行轮换（按名称排序返回）。
func RotateAll(conns []store.Connection, o RotateOptions) []RotateResult {
	results := make([]RotateResult, len(conns))
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			oc := o
			oc.Conn = conns[i]
			// 每台主机沿用自己已保存的凭据（除非显式给了统一 secret）。
			if o.Secret == "" || secretMissingLike(conns[i]) {
				oc.Secret = secretFor(conns[i])
			}
			results[i] = Rotate(oc)
		}(i)
	}
	wg.Wait()
	return results
}

func secretMissingLike(c store.Connection) bool {
	if c.AuthType == store.AuthKey {
		return c.AskPassphrase
	}
	return c.Password == "" || c.AskPassword
}

// RemoveAuthorizedKey 从远端 authorized_keys 中移除匹配「算法+密钥体」的行。
// 返回是否真的删掉了内容，以及剩余条目数。
func RemoveAuthorizedKey(conn store.Connection, secret, publicKey, remotePath string, timeout time.Duration) (removed bool, total int, err error) {
	body := KeyBody(publicKey)
	if strings.TrimSpace(body) == "" {
		return false, 0, fmt.Errorf("无法从公钥解析出密钥体")
	}
	p := normalizeRemotePath(remotePath)
	cmd := strings.Join([]string{
		`set -e`,
		`P="` + p + `"`,
		`[ -f "$P" ] || { echo COUNT=0; exit 0; }`,
		`B=` + shellQuote(body),
		`BEFORE=$(wc -l < "$P" | tr -d ' ')`,
		// 只删除「算法+密钥体」完全匹配的行，不动其它条目
		`grep -vF -- "$B" "$P" > "$P.sshtool.tmp" || true`,
		`cat "$P.sshtool.tmp" > "$P"`,
		`rm -f "$P.sshtool.tmp"`,
		`chmod 600 "$P"`,
		`AFTER=$(grep -c . "$P" || true)`,
		`if [ "$BEFORE" != "$AFTER" ]; then echo REMOVED=1; else echo REMOVED=0; fi`,
		`printf 'COUNT=%s\n' "$AFTER"`,
		"",
	}, "\n")

	out, errOut, rerr := remotessh.RunOnce(conn, secret, cmd, timeout)
	if rerr != nil {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = rerr.Error()
		}
		return false, 0, fmt.Errorf("%s", msg)
	}
	return strings.Contains(out, "REMOVED=1"), parseCount(out), nil
}

func phaseWord(appended bool) string {
	if appended {
		return "新写入"
	}
	return "已存在"
}

// RotateSummary 汇总一批主机的轮换结果。
func RotateSummary(results []RotateResult) (ok, failed int) {
	for _, r := range results {
		if r.Err != "" {
			failed++
		} else {
			ok++
		}
	}
	return ok, failed
}
