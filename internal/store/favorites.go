package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GetFavorites 返回收藏命令的副本：按使用次数降序，其次名称升序。
func (s *Store) GetFavorites() []FavoriteCmd {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return s.favoritesLocked()
}

// ListFavorites 是 GetFavorites 的别名。
func (s *Store) ListFavorites() []FavoriteCmd { return s.GetFavorites() }

// Favorite 按 ID 查找收藏命令。
func (s *Store) Favorite(id string) (FavoriteCmd, bool) {
	storeMu.RLock()
	defer storeMu.RUnlock()
	for _, f := range s.Favorites {
		if f.ID == id {
			return f, true
		}
	}
	return FavoriteCmd{}, false
}

// AddFavorite 新增一条收藏命令并返回填充后的副本。
func (s *Store) AddFavorite(f FavoriteCmd) FavoriteCmd {
	storeMu.Lock()
	defer storeMu.Unlock()

	if f.ID == "" {
		f.ID = newID()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	if f.UseCount < 0 {
		f.UseCount = 0
	}
	s.Favorites = append(s.Favorites, f)
	return f
}

// UpdateFavorite 按 ID 整体替换收藏命令；不存在时返回错误。
func (s *Store) UpdateFavorite(f FavoriteCmd) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	for i := range s.Favorites {
		if s.Favorites[i].ID == f.ID {
			if f.CreatedAt.IsZero() {
				f.CreatedAt = s.Favorites[i].CreatedAt
			}
			s.Favorites[i] = f
			return nil
		}
	}
	return errors.New("store: 收藏命令不存在: " + f.ID)
}

// RemoveFavorite 按 ID 删除收藏命令；不存在时返回错误。
func (s *Store) RemoveFavorite(id string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	for i := range s.Favorites {
		if s.Favorites[i].ID == id {
			s.Favorites = append(s.Favorites[:i], s.Favorites[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("store: 收藏命令 %q 不存在", id)
}

// TouchFavorite 递增收藏命令的使用次数；ID 不存在时静默忽略。
func (s *Store) TouchFavorite(id string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	for i := range s.Favorites {
		if s.Favorites[i].ID == id {
			s.Favorites[i].UseCount++
			return
		}
	}
}

func (s *Store) favoritesLocked() []FavoriteCmd {
	out := make([]FavoriteCmd, len(s.Favorites))
	copy(out, s.Favorites)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UseCount != out[j].UseCount {
			return out[i].UseCount > out[j].UseCount
		}
		ni, nj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if ni != nj {
			return ni < nj
		}
		return out[i].ID < out[j].ID
	})
	return out
}
