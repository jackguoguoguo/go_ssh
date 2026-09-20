package store

import "strings"

// FilterConnections 按关键词过滤连接：匹配名称 / 主机 / 用户 / 分组（大小写不敏感）。
// q 为空时原样返回。UI 面板过滤与 CLI 子命令共用同一语义。
func FilterConnections(in []Connection, q string) []Connection {
	if q == "" {
		return in
	}
	q = strings.ToLower(q)
	out := make([]Connection, 0, len(in))
	for _, c := range in {
		if strings.Contains(strings.ToLower(c.Name), q) ||
			strings.Contains(strings.ToLower(c.Host), q) ||
			strings.Contains(strings.ToLower(c.User), q) ||
			strings.Contains(strings.ToLower(c.Group), q) {
			out = append(out, c)
		}
	}
	return out
}
