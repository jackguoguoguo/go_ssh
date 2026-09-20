package remotessh

import (
	"strings"
	"sync"
	"time"

	"sshtool/internal/store"
)

// PingStatus 巡检状态。
type PingStatus int

const (
	PingOK      PingStatus = iota // 可达且认证成功
	PingAuthFail             // 可达但认证失败
	PingUnreachable          // 网络不可达 / 连接被拒 / 握手超时
	PingTimeout              // 命令执行超时
)

// PingResult 单台主机的巡检结果。
type PingResult struct {
	Conn    store.Connection
	Name    string
	Host    string
	Status  PingStatus
	Detail  string
	Latency time.Duration // 认证成功时 T+认证耗时；其它状态为 0
}

// StatusText 返回巡检状态的可读描述。
func (s PingStatus) Text() string {
	switch s {
	case PingOK:
		return "可达"
	case PingAuthFail:
		return "认证失败"
	case PingUnreachable:
		return "不可达"
	case PingTimeout:
		return "超时"
	}
	return "未知"
}

// Ping 并发巡检一批连接：对每台执行无害命令 `true` 并计时，区分可达/认证失败/不可达/超时。
// 认证失败可通过握手错误消息分辨（"unable to authenticate" / "permission denied" 等）。
func Ping(conns []store.Connection, timeout time.Duration) []PingResult {
	results := make([]PingResult, len(conns))
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := conns[i]
			res := PingResult{
				Conn: c,
				Name: batchName(c),
				Host: batchHost(c),
			}
			start := time.Now()
			_, _, err := RunOnce(c, secretForBatch(c), "true", timeout)
			if err != nil {
				msg := err.Error()
				if strings.Contains(msg, "unable to authenticate") ||
					strings.Contains(msg, "authentication") ||
					strings.Contains(msg, "permission denied") ||
					strings.Contains(msg, "认证失败") ||
					strings.Contains(msg, "口令") && strings.Contains(msg, "私钥") {
					res.Status = PingAuthFail
				} else if strings.Contains(msg, "超时") {
					res.Status = PingTimeout
				} else {
					res.Status = PingUnreachable
				}
				res.Detail = msg
			} else {
				res.Status = PingOK
				res.Latency = time.Now().Sub(start)
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	return results
}

// PingCount 按状态统计（返回 [ok, authFail, unreachable, timeout]）。
func PingCount(results []PingResult) (int, int, int, int) {
	ok, af, un, to := 0, 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case PingOK:
			ok++
		case PingAuthFail:
			af++
		case PingUnreachable:
			un++
		case PingTimeout:
			to++
		}
	}
	return ok, af, un, to
}