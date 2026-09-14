package keytool

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultDir 返回本机默认的 SSH 目录（~/.ssh）。
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".ssh"
	}
	return filepath.Join(home, ".ssh")
}

// DefaultRemotePath 远端 authorized_keys 的默认路径。
const DefaultRemotePath = "~/.ssh/authorized_keys"

// DefaultComment 生成 user@host 形式的默认注释。
func DefaultComment() string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	switch {
	case user != "" && host != "":
		return user + "@" + host
	case user != "":
		return user
	case host != "":
		return host
	}
	return "sshtool"
}

// ExpandHome 把路径开头的 ~ 展开为 HOME，便于直接传给 os 函数。
func ExpandHome(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if len(p) == 1 {
		return home
	}
	return filepath.Join(home, p[2:])
}

// normalizeRemotePath 把远端路径改成「远端 shell 能正确展开」的形式：
// ~ 开头的路径替换成 $HOME，相对路径也挂到 $HOME 下（登录后的工作目录通常就是家目录，
// 但显式化更稳）。这样脚本里可以统一用双引号包裹，无需依赖波浪号展开。
func normalizeRemotePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return strings.Replace(DefaultRemotePath, "~", "$HOME", 1)
	}
	if strings.HasPrefix(p, "~") {
		return "$HOME" + p[1:]
	}
	if !strings.HasPrefix(p, "/") {
		return "$HOME/" + p
	}
	return p
}

// ListLocal 扫描目录下的全部 *.pub，返回密钥列表（按修改时间倒序）。
func ListLocal(dir string) ([]KeyInfo, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []KeyInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		info, err := ReadPublic(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // 跳过 known_hosts 之类无法解析的文件
		}
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	for i := range out {
		if out[i].ModTime.IsZero() {
			out[i].ModTime = time.Now()
		}
	}
	return out, nil
}
