package remotessh

import (
	"fmt"
	"sync"
	"time"

	"sshtool/internal/store"
)

// BatchResult 单台主机的命令执行结果。
type BatchResult struct {
	Conn   store.Connection // 目标连接
	Name   string           // 展示名（连接名，缺省 user@host）
	Host   string           // user@host:port
	Stdout string
	Stderr string
	Code   int  // 退出码；-1 表示未执行成功（连不上 / 超时）
	Err    string // 非空表示失败
}

// Batch 在目标主机上并发执行同一条命令并汇总结果。
// 复用 RunOnce 的「一次性连接」通道：不申请 PTY、不占用交互会话、执行完立即断开。
func Batch(conns []store.Connection, cmd string, timeout time.Duration) []BatchResult {
	results := make([]BatchResult, len(conns))
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := conns[i]
			res := BatchResult{
				Conn: c,
				Name: batchName(c),
				Host: batchHost(c),
				Code: -1,
			}
			out, errOut, err := RunOnce(c, secretForBatch(c), cmd, timeout)
			res.Stdout = out
			res.Stderr = errOut
			if err != nil {
				res.Err = err.Error()
			} else {
				res.Code = 0 // RunOnce 成功即命令已执行完毕
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	return results
}

// secretForBatch 取出连接已保存的凭据（密码 / 私钥口令）。
// 与 UI 的 secretOf 语义一致，但这里在 remotessh 层自持，便于无 UI 场景（CLI）复用。
func secretForBatch(c store.Connection) string {
	if c.AuthType == store.AuthKey {
		return c.KeyPassphrase
	}
	return c.Password
}

func batchName(c store.Connection) string {
	if c.Name != "" {
		return c.Name
	}
	return batchHost(c)
}

func batchHost(c store.Connection) string {
	user := c.User
	if user == "" {
		user = "root"
	}
	port := c.Port
	if port <= 0 {
		port = store.DefaultSSHPort
	}
	return fmt.Sprintf("%s@%s:%d", user, c.Host, port)
}

// BatchFailed 统计批量的成功 / 失败数。
func BatchFailed(results []BatchResult) int {
	n := 0
	for _, r := range results {
		if r.Err != "" {
			n++
		}
	}
	return n
}