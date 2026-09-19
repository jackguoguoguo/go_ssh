package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// GetHistory 返回历史命令的副本，按时间倒序（最新在前）。
//
// 内部切片始终以「最新在前」的顺序保存（新增时前插，加载时整理一次），
// 因此这里直接拷贝即可，无需每次排序：时间戳相同时也能稳定保持插入顺序。
func (s *Store) GetHistory() []HistoryEntry {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return s.historyLocked()
}

// ListHistory 是 GetHistory 的别名。
func (s *Store) ListHistory() []HistoryEntry { return s.GetHistory() }

// AddHistory 记录一条历史命令。
//
// cmd 会去掉首尾空白，为空则不记录并返回零值；与最新一条（cmd 与 host 均相同）
// 重复时只更新时间，不追加新条目；条数超过 Settings.HistoryLimit（<=0 时用
// DefaultHistoryLimit）时裁剪最旧的记录。
func (s *Store) AddHistory(cmd, host string) HistoryEntry {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return HistoryEntry{}
	}
	// 入库前脱敏：口令 / 令牌等敏感值以 *** 遮蔽，避免明文落盘。
	cmd = MaskSecrets(cmd)

	storeMu.Lock()
	defer storeMu.Unlock()

	now := time.Now()
	if len(s.History) > 0 {
		last := &s.History[0]
		if last.Cmd == cmd && last.Host == host {
			last.At = now
			return *last
		}
	}

	e := HistoryEntry{ID: newID(), Cmd: cmd, Host: host, At: now}
	s.History = append([]HistoryEntry{e}, s.History...)
	s.trimHistoryLocked()
	return e
}

// SearchHistory 按关键字搜索历史命令，大小写不敏感地匹配命令或主机；
// 空关键字返回全部记录（时间倒序）。
func (s *Store) SearchHistory(keyword string) []HistoryEntry {
	storeMu.RLock()
	defer storeMu.RUnlock()

	all := s.historyLocked()
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return all
	}
	lower := strings.ToLower(keyword)
	out := make([]HistoryEntry, 0, len(all))
	for _, h := range all {
		if strings.Contains(strings.ToLower(h.Cmd), lower) ||
			strings.Contains(strings.ToLower(h.Host), lower) {
			out = append(out, h)
		}
	}
	return out
}

// ClearHistory 清空全部历史命令。
func (s *Store) ClearHistory() {
	storeMu.Lock()
	defer storeMu.Unlock()
	s.History = make([]HistoryEntry, 0)
}

// RemoveHistory 按 ID 删除历史记录；不存在时返回错误。
func (s *Store) RemoveHistory(id string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	for i := range s.History {
		if s.History[i].ID == id {
			s.History = append(s.History[:i], s.History[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("store: 历史记录 %q 不存在", id)
}

func (s *Store) historyLocked() []HistoryEntry {
	out := make([]HistoryEntry, len(s.History))
	copy(out, s.History)
	return out
}

// sortByTimeDesc 就地整理为时间倒序；时间相同时保持原有顺序（稳定排序），
// 加载历史文件后调用一次即可。
func sortByTimeDesc(entries []HistoryEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].At.After(entries[j].At)
	})
}

// trimHistoryLocked 把历史裁剪到 Settings.HistoryLimit 条以内（假定已按时间倒序）。
func (s *Store) trimHistoryLocked() {
	limit := s.Settings.HistoryLimit
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if len(s.History) > limit {
		s.History = s.History[:limit]
	}
}
