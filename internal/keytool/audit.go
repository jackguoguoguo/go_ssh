package keytool

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"sshtool/internal/store"
)

// AuditResult 单台主机的公钥盘点结果。
type AuditResult struct {
	Name  string // 连接名
	Host  string // user@host:port
	Path  string // 远端 authorized_keys 路径
	Total int    // 远端条目数

	// 本机密钥中，已在该主机授权的（指纹）
	Authorized []string
	// 本机密钥中，尚未授权的（指纹）——「哪台还没授权」
	Missing []string
	// 远端存在但本机没有对应密钥的条目——「哪台有已废弃的旧密钥」
	Stale []KeyInfo

	Err string // 非空表示拉取失败
}

// Audit 并发拉取各主机上的 authorized_keys，与本机密钥比对：
//   - Authorized：本机密钥里已授权的；
//   - Missing：本机有、但该主机没有的（需要推送）；
//   - Stale：远端有、但本机没有的（疑似废弃 / 他人密钥）。
//
// 比较按「算法 + 密钥体」（KeyBody），忽略注释，与推送时的去重口径一致。
func Audit(conns []store.Connection, localKeys []KeyInfo, remotePath string, timeout time.Duration) []AuditResult {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	results := make([]AuditResult, len(conns))
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := conns[i]
			res := AuditResult{
				Name: connName(c),
				Host: fmt.Sprintf("%s@%s:%d", userDisplay(c), c.Host, portDisplay(c)),
				Path: strings.TrimSpace(remotePath),
			}
			if res.Path == "" {
				res.Path = DefaultRemotePath
			}
			content, err := SnapshotRemoteAuthorizedKeys(c, secretFor(c), res.Path, timeout)
			if err != nil {
				res.Err = err.Error()
				results[i] = res
				return
			}
			remote := parseAuthorizedKeys(content)
			res.Total = len(remote)
			res.Authorized, res.Missing, res.Stale = classify(remote, localKeys)
			results[i] = res
		}(i)
	}
	wg.Wait()
	return results
}

// secretFor 取出连接已保存的凭据（与批量执行一致）。
func secretFor(c store.Connection) string {
	if c.AuthType == store.AuthKey {
		return c.KeyPassphrase
	}
	return c.Password
}

// parseAuthorizedKeys 把 authorized_keys 内容解析为条目（跳过空行与注释行）。
func parseAuthorizedKeys(content string) []KeyInfo {
	var out []KeyInfo
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		info, err := ParseLine(line)
		if err != nil {
			continue // 无法解析的行（选项前缀等）跳过，不影响其余条目
		}
		out = append(out, info)
	}
	return out
}

// classify 把远端条目与本机密钥按「算法+密钥体」比对，分出已授权 / 缺失 / 废弃。
func classify(remote, local []KeyInfo) (authorized, missing []string, stale []KeyInfo) {
	remoteSet := map[string]bool{}
	for _, r := range remote {
		remoteSet[KeyBody(r.PublicKey)] = true
	}
	localSet := map[string]bool{}
	for _, l := range local {
		localSet[KeyBody(l.PublicKey)] = true
	}

	for _, l := range local {
		if remoteSet[KeyBody(l.PublicKey)] {
			authorized = append(authorized, l.Fingerprint)
		} else {
			missing = append(missing, l.Fingerprint)
		}
	}
	for _, r := range remote {
		if !localSet[KeyBody(r.PublicKey)] {
			stale = append(stale, r)
		}
	}
	return authorized, missing, stale
}

// AuditSummary 汇总一批主机的盘点结果。
type AuditSummary struct {
	Hosts     int // 成功拉取的主机数
	Failed    int // 拉取失败的主机数
	Fully     int // 本机全部密钥都已授权的主机数
	NeedPush  int // 至少缺一把密钥的主机数
	StaleHost int // 至少含一条废弃密钥的主机数
}

// Summarize 统计盘点结果。
func Summarize(results []AuditResult) AuditSummary {
	var s AuditSummary
	for _, r := range results {
		if r.Err != "" {
			s.Failed++
			continue
		}
		s.Hosts++
		if len(r.Missing) == 0 {
			s.Fully++
		} else {
			s.NeedPush++
		}
		if len(r.Stale) > 0 {
			s.StaleHost++
		}
	}
	return s
}
