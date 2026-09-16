package remotessh

import (
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"time"

	"github.com/pkg/sftp"
)

// FSClient 封装一个远端 SFTP 连接，提供面向文件浏览器的常用操作。
type FSClient struct {
	c *sftp.Client
}

// FS 为指定会话建立一个 SFTP 通道（subsystem "sftp"）。
func (m *Manager) FS(id string) (*FSClient, error) {
	s, ok := m.Get(id)
	if !ok {
		return nil, fmt.Errorf("会话不存在")
	}
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("会话尚未就绪")
	}
	c, err := sftp.NewClient(client)
	if err != nil {
		return nil, err
	}
	return &FSClient{c: c}, nil
}

// Close 关闭 SFTP 通道。
func (c *FSClient) Close() error { return c.c.Close() }

// Entry 描述远端的一个文件或目录。
type Entry struct {
	Name    string
	IsDir   bool
	Size    int64
	Mode    string
	ModTime time.Time
}

func toEntry(fi os.FileInfo) Entry {
	return Entry{
		Name:    fi.Name(),
		IsDir:   fi.IsDir(),
		Size:    fi.Size(),
		Mode:    fi.Mode().String(),
		ModTime: fi.ModTime(),
	}
}

// List 列出目录内容（不含 . 和 ..），目录排在前面，其余按名称升序。
func (c *FSClient) List(dir string) ([]Entry, error) {
	infos, err := c.c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		out = append(out, toEntry(fi))
	}
	sortEntries(out)
	return out, nil
}

// ReadFile 读取整个文件内容。
func (c *FSClient) ReadFile(p string) ([]byte, error) {
	f, err := c.c.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// WriteFile 覆盖写入文件（自动创建父目录）。
func (c *FSClient) WriteFile(p string, data []byte) error {
	if dir := path.Dir(p); dir != "" && dir != "." && dir != "/" {
		_ = c.c.MkdirAll(dir)
	}
	f, err := c.c.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// Mkdir 创建目录（递归）。
func (c *FSClient) Mkdir(p string) error { return c.c.MkdirAll(p) }

// Remove 删除文件或空目录。
func (c *FSClient) Remove(p string) error { return c.c.Remove(p) }

// Rename 重命名 / 移动。
func (c *FSClient) Rename(oldp, newp string) error { return c.c.Rename(oldp, newp) }

// Getwd 返回 SFTP 当前工作目录（通常是登录用户的家目录）。
func (c *FSClient) Getwd() (string, error) { return c.c.Getwd() }

func sortEntries(e []Entry) {
	sort.SliceStable(e, func(i, j int) bool {
		if e[i].IsDir != e[j].IsDir {
			return e[i].IsDir
		}
		return e[i].Name < e[j].Name
	})
}
