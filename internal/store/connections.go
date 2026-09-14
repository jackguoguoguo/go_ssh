package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// 注意：types.go 中 Store 已经有同名字段 Connections，Go 不允许同名字段与方法
// 共存，因此这里的读取器命名为 GetConnections（别名 ListConnections）。

// GetConnections 返回连接列表的副本：收藏优先，其余按名称升序（不区分大小写）。
func (s *Store) GetConnections() []Connection {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return s.connectionsLocked()
}

// ListConnections 是 GetConnections 的别名。
func (s *Store) ListConnections() []Connection { return s.GetConnections() }

// Connection 按 ID 查找连接。
func (s *Store) Connection(id string) (Connection, bool) {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return s.connectionLocked(id)
}

// AddConnection 新增一条连接并返回填充默认值后的副本。
// 自动生成 ID、创建时间；端口非法时使用 22；认证方式为空时使用密码认证。
func (s *Store) AddConnection(c Connection) Connection {
	storeMu.Lock()
	defer storeMu.Unlock()

	if c.ID == "" {
		c.ID = newID()
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.Port <= 0 {
		c.Port = DefaultSSHPort
	}
	if string(c.AuthType) == "" {
		c.AuthType = AuthPassword
	}
	if c.LastUsedAt.IsZero() {
		c.LastUsedAt = c.CreatedAt
	}
	s.Connections = append(s.Connections, c)
	return c
}

// UpdateConnection 按 ID 整体替换一条连接；不存在时返回错误。
func (s *Store) UpdateConnection(c Connection) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	return s.updateConnectionLocked(c)
}

// RemoveConnection 按 ID 删除连接；不存在时返回错误。
func (s *Store) RemoveConnection(id string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	for i := range s.Connections {
		if s.Connections[i].ID == id {
			s.Connections = append(s.Connections[:i], s.Connections[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("store: 连接 %q 不存在", id)
}

// TouchConnection 更新连接的最后使用时间；ID 不存在时静默忽略。
func (s *Store) TouchConnection(id string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	for i := range s.Connections {
		if s.Connections[i].ID == id {
			s.Connections[i].LastUsedAt = time.Now()
			return
		}
	}
}

func (s *Store) connectionsLocked() []Connection {
	out := make([]Connection, len(s.Connections))
	copy(out, s.Connections)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Favorite != out[j].Favorite {
			return out[i].Favorite
		}
		ni, nj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if ni != nj {
			return ni < nj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Store) connectionLocked(id string) (Connection, bool) {
	for _, c := range s.Connections {
		if c.ID == id {
			return c, true
		}
	}
	return Connection{}, false
}

func (s *Store) updateConnectionLocked(c Connection) error {
	for i := range s.Connections {
		if s.Connections[i].ID == c.ID {
			if c.CreatedAt.IsZero() {
				c.CreatedAt = s.Connections[i].CreatedAt
			}
			if c.Port <= 0 {
				c.Port = DefaultSSHPort
			}
			if string(c.AuthType) == "" {
				c.AuthType = AuthPassword
			}
			s.Connections[i] = c
			return nil
		}
	}
	return errors.New("store: 连接不存在: " + c.ID)
}
