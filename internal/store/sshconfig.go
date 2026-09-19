package store

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHConfigGroup 是导入的 ~/.ssh/config 连接默认归属的分组名。
const SSHConfigGroup = "ssh_config"

// ParseSSHConfig 解析 ssh_config 文本，返回其中的主机条目。
//
// 支持的关键字：Host / HostName / User / Port / IdentityFile。
// 含通配符（* 或 ?）的 Host 模式会被跳过；一个 Host 写多个别名会展开成多条连接。
// 无 HostName 时以别名作为主机；无 Port 时默认 22；有 IdentityFile 视为私钥认证，
// 否则视为密码认证并要求连接时输入密码（不落盘）。
func ParseSSHConfig(data []byte) []Connection {
	type block struct {
		aliases []string
		host    string
		user    string
		port    int
		keyPath string
	}
	var blocks []block
	var cur *block

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 去掉行尾注释（简单处理：以空白+# 起始处截断）
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		key, val := splitDirective(line)
		switch strings.ToLower(key) {
		case "host":
			blocks = append(blocks, block{aliases: strings.Fields(val)})
			cur = &blocks[len(blocks)-1]
		default:
			if cur == nil {
				continue
			}
			switch strings.ToLower(key) {
			case "hostname":
				cur.host = unquote(val)
			case "user":
				cur.user = unquote(val)
			case "port":
				if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
					cur.port = n
				}
			case "identityfile":
				cur.keyPath = expandHome(unquote(val))
			}
		}
	}

	var out []Connection
	for _, b := range blocks {
		if len(b.aliases) == 0 {
			continue
		}
		for _, alias := range b.aliases {
			if alias == "" || strings.ContainsAny(alias, "*?") {
				continue
			}
			c := Connection{
				Name:    alias,
				Group:   SSHConfigGroup,
				Host:    b.host,
				User:    b.user,
				Port:    b.port,
				AuthType: AuthPassword,
			}
			if c.Host == "" {
				c.Host = alias
			}
			if c.Port == 0 {
				c.Port = DefaultSSHPort
			}
			if b.keyPath != "" {
				c.AuthType = AuthKey
				c.KeyPath = b.keyPath
			} else {
				c.AskPassword = true
			}
			out = append(out, c)
		}
	}
	return out
}

// splitDirective 把一行拆成「关键字 值」；无值时空串。
func splitDirective(line string) (key, val string) {
	line = strings.TrimSpace(line)
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i+1:])
}

// unquote 去掉成对的引号。
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// expandHome 把开头的 ~ 展开为主目录。
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// ImportSSHConfig 解析并合并 ssh_config：按名称去重（保留不同别名的语义），返回新增数与跳过数。
func (s *Store) ImportSSHConfig(data []byte) (added, skipped int) {
	existing := map[string]bool{}
	storeMu.RLock()
	for _, c := range s.Connections {
		existing[c.Name] = true
	}
	storeMu.RUnlock()

	for _, c := range ParseSSHConfig(data) {
		if existing[c.Name] {
			skipped++
			continue
		}
		s.AddConnection(c)
		existing[c.Name] = true
		added++
	}
	return added, skipped
}

// ExportSSHConfig 把当前连接渲染为 ssh_config 文本（按分组加注释，便于阅读）。
func (s *Store) ExportSSHConfig() string {
	storeMu.RLock()
	conns := append([]Connection(nil), s.Connections...)
	storeMu.RUnlock()

	var b strings.Builder
	b.WriteString("# 由 sshtool 导出\n\n")
	lastGroup := "\x00"
	for _, c := range conns {
		if c.Group != lastGroup {
			b.WriteString("\n# " + groupLabel(c.Group) + "\n")
			lastGroup = c.Group
		}
		name := c.Name
		if name == "" {
			name = c.Host
		}
		b.WriteString("Host " + sanitizeHostAlias(name) + "\n")
		b.WriteString("    HostName " + c.Host + "\n")
		if c.User != "" {
			b.WriteString("    User " + c.User + "\n")
		}
		port := c.Port
		if port == 0 {
			port = DefaultSSHPort
		}
		b.WriteString("    Port " + strconv.Itoa(port) + "\n")
		if c.AuthType == AuthKey && c.KeyPath != "" {
			b.WriteString("    IdentityFile " + c.KeyPath + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func groupLabel(g string) string {
	if g == "" {
		return "(未分组)"
	}
	return g
}

// sanitizeHostAlias 去掉别名中的空白，避免写坏 ssh_config。
func sanitizeHostAlias(name string) string {
	return strings.Join(strings.Fields(name), "_")
}
