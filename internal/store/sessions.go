package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// SessionSnapshot 记录退出时的会话布局，用于下次启动复原。
// 只保存 SSH 会话的连接 ID（按标签顺序）与当前活动会话；本地 shell 无法复原故不记录。
type SessionSnapshot struct {
	OpenIDs  []string  `json:"open_ids"`
	ActiveID string    `json:"active_id,omitempty"`
	SavedAt  time.Time `json:"saved_at"`
}

// SessionsName 会话快照文件名（与配置文件同目录）。
const SessionsName = "sessions.json"

// SessionsPath 返回会话快照文件路径（与配置文件同目录）。
func (s *Store) SessionsPath() string {
	p := s.Path()
	if p == "" {
		return SessionsName
	}
	return filepath.Join(filepath.Dir(p), SessionsName)
}

// SaveSessions 原子写入会话快照。
func (s *Store) SaveSessions(snap SessionSnapshot) error {
	path := s.SessionsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	snap.SavedAt = time.Now()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadSessions 读取会话快照；文件不存在时返回零值快照（不报错）。
func (s *Store) LoadSessions() (SessionSnapshot, error) {
	data, err := os.ReadFile(s.SessionsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return SessionSnapshot{}, nil
		}
		return SessionSnapshot{}, err
	}
	var snap SessionSnapshot
	if len(data) > 0 {
		if err := json.Unmarshal(data, &snap); err != nil {
			return SessionSnapshot{}, err
		}
	}
	return snap, nil
}