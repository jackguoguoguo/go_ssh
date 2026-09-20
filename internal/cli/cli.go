// Package cli 提供 sshtool 的非交互（headless）子命令：
//
//	sshtool exec  [-g 关键词] [--timeout N] [-f text|json|plain] <命令...>
//	sshtool ping  [-g 关键词] [--timeout N] [-f text|json]
//
// 复用 TUI 的批量执行 / 健康巡检内核，目标连接从配置文件读取，
// 退出码：0=全部成功，1=存在失败，2=用法错误。适合脚本与 CI。
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// 退出码。
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

// pickTargets 按 -g 过滤目标连接；无匹配时返回 nil。
func pickTargets(st *store.Store, group string) []store.Connection {
	return store.FilterConnections(st.GetConnections(), group)
}

// execJSON 是 exec 的 JSON 输出结构（白名单字段，绝不包含连接凭据）。
type execJSON struct {
	Name   string `json:"name"`
	Host   string `json:"host"`
	Code   int    `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr,omitempty"`
	Err    string `json:"err,omitempty"`
}

// pingJSON 是 ping 的 JSON 输出结构（白名单字段，绝不包含连接凭据）。
type pingJSON struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
}

// Exec 实现 `sshtool exec`：对匹配的连接并发执行命令并汇总结果。
func Exec(st *store.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	group := fs.String("g", "", "按名称/主机/用户/分组过滤目标连接（缺省全部）")
	timeout := fs.Int("timeout", 60, "单台执行超时（秒）")
	format := fs.String("f", "text", "输出格式：text | json | plain（仅 stdout）")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cmd := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if cmd == "" {
		fmt.Fprintln(stderr, "用法: sshtool exec [-g 关键词] [--timeout N] [-f text|json|plain] <命令...>")
		return ExitUsage
	}
	targets := pickTargets(st, *group)
	if len(targets) == 0 {
		fmt.Fprintln(stderr, "没有匹配的连接")
		return ExitFail
	}

	results := remotessh.Batch(targets, cmd, time.Duration(*timeout)*time.Second)
	fail := 0
	for _, r := range results {
		if r.Err != "" {
			fail++
		}
	}

	switch strings.ToLower(*format) {
	case "json":
		// 只输出白名单字段：绝不序列化 Connection（内含明文密码）。
		dtos := make([]execJSON, 0, len(results))
		for _, r := range results {
			dtos = append(dtos, execJSON{
				Name: r.Name, Host: r.Host, Code: r.Code,
				Stdout: r.Stdout, Stderr: r.Stderr, Err: r.Err,
			})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(dtos); err != nil {
			fmt.Fprintln(stderr, "输出失败：", err)
			return ExitFail
		}
	case "plain":
		// 仅输出成功主机的 stdout：单台时无分隔，多台时以主机名为节。
		for _, r := range results {
			if r.Err != "" {
				continue
			}
			if len(results) > 1 {
				fmt.Fprintf(stdout, "--- %s ---\n", r.Name)
			}
			fmt.Fprint(stdout, r.Stdout)
		}
	default:
		for _, r := range results {
			if r.Err != "" {
				fmt.Fprintf(stdout, "✗ %s  %s\n    %s\n", r.Name, r.Host, r.Err)
				continue
			}
			fmt.Fprintf(stdout, "✓ %s  %s  (exit=%d)\n", r.Name, r.Host, r.Code)
			if s := strings.TrimRight(r.Stdout, "\n"); s != "" {
				fmt.Fprintln(stdout, indent(s))
			}
		}
		fmt.Fprintf(stdout, "汇总: %d 成功 · %d 失败  $ %s\n", len(results)-fail, fail, cmd)
	}

	if fail > 0 {
		return ExitFail
	}
	return ExitOK
}

// Ping 实现 `sshtool ping`：对匹配的连接并发做连通性 + 认证检查。
func Ping(st *store.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	fs.SetOutput(stderr)
	group := fs.String("g", "", "按名称/主机/用户/分组过滤目标连接（缺省全部）")
	timeout := fs.Int("timeout", 10, "单台检查超时（秒）")
	format := fs.String("f", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	targets := pickTargets(st, *group)
	if len(targets) == 0 {
		fmt.Fprintln(stderr, "没有匹配的连接")
		return ExitFail
	}

	results := remotessh.Ping(targets, time.Duration(*timeout)*time.Second)
	ok, af, un, to := remotessh.PingCount(results)
	bad := len(results) - ok

	switch strings.ToLower(*format) {
	case "json":
		dtos := make([]pingJSON, 0, len(results))
		for _, r := range results {
			dtos = append(dtos, pingJSON{
				Name: r.Name, Host: r.Host, Status: r.Status.Text(),
				Detail: r.Detail, LatencyMs: r.Latency.Milliseconds(),
			})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(dtos); err != nil {
			fmt.Fprintln(stderr, "输出失败：", err)
			return ExitFail
		}
	default:
		for _, r := range results {
			lat := ""
			if r.Status == remotessh.PingOK {
				lat = fmt.Sprintf("  %dms", int(r.Latency/time.Millisecond))
			}
			fmt.Fprintf(stdout, "%-6s %-16s %-24s %s%s\n", r.Status.Text(), r.Name, r.Host, r.Detail, lat)
		}
		fmt.Fprintf(stdout, "汇总: %d 可达 · %d 异常（认证失败 %d · 不可达 %d · 超时 %d）\n", ok, bad, af, un, to)
	}

	if bad > 0 {
		return ExitFail
	}
	return ExitOK
}

// indent 给多行文本加四空格缩进，便于结果对齐。
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}