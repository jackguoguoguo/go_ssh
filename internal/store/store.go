package store

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// storeMu 是包级全局读写锁，保护所有 Store 实例的读写。
//
// 为什么不是 Store 内嵌 sync.RWMutex：types.go 是冻结契约，Store 的结构体
// 定义不能新增字段（同一包内也不允许重复定义同一类型），而带 mutex 的结构体
// 又必须始终以指针方式使用、不能被值拷贝。本项目同时只会存在一个 Store 实例，
// 因此使用包级全局锁既安全又足够，且避免了在多个 package 文件里拆分结构体。
//
// 约定：所有导出方法自行加锁；包名中以 locked 结尾的内部辅助函数假定调用方
// 已经持有 storeMu，本身不再加锁，避免重入死锁。
var storeMu sync.RWMutex

// New 返回一个以 path 为存储路径、使用默认设置的空 Store。
func New(path string) *Store {
	s := &Store{
		Connections: make([]Connection, 0),
		Favorites:   make([]FavoriteCmd, 0),
		History:     make([]HistoryEntry, 0),
		Settings:    defaultSettings(),
		path:        path,
	}
	return s
}

// DefaultPath 返回默认配置文件路径：用户主目录下的 .sshtool/config.json。
// 获取主目录失败时退化为相对路径 ".sshtool/config.json"。
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(DirName, ConfigName)
	}
	return filepath.Join(home, DirName, ConfigName)
}

// Load 从默认路径加载配置。
func Load() (*Store, error) {
	return LoadFrom(DefaultPath())
}

// LoadFrom 从 path 加载配置。
//
// 文件不存在时返回带默认设置的新 Store，不返回错误，也不会创建文件。
// 文件损坏或无法解析时，尝试读取同目录下的 config.json.bak；备份也失败时，
// 返回一个非 nil 的默认 Store，同时返回错误供调用方提示用户。
func LoadFrom(path string) (*Store, error) {
	s := New(path)

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if errors.Is(readErr, fs.ErrNotExist) {
			return s, nil
		}
		// 读不到主文件（权限等），尝试走备份分支。
	} else if err := json.Unmarshal(data, s); err == nil {
		normalizeLoaded(s)
		return s, nil
	} else {
		readErr = err
	}

	// 主文件不可用，尝试备份。
	bakData, bakErr := os.ReadFile(filepath.Join(filepath.Dir(path), BackupName))
	if bakErr != nil {
		if readErr == nil {
			readErr = bakErr
		}
		return New(path), readErr
	}
	bak := New(path)
	if err := json.Unmarshal(bakData, bak); err != nil {
		return New(path), fmt.Errorf("配置文件已损坏，备份文件同样无法解析: %w", err)
	}
	normalizeLoaded(bak)
	return bak, nil
}

// Path 返回与该 Store 关联的配置文件路径。
func (s *Store) Path() string {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return s.path
}

// Save 以原子方式把 Store 写入 Path() 对应的文件。
//
// 流程：写入同目录临时文件 → fsync → 把当前 config.json 复制为 config.json.bak
// → rename 覆盖 config.json。目录不存在时自动创建（0750），文件权限 0600
// （Windows 上 Chmod 只能切换只读位，属尽力而为）。
func (s *Store) Save() error {
	path := s.Path()
	if path == "" {
		return errors.New("store: 未指定配置文件路径")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	storeMu.RLock()
	data, err := json.MarshalIndent(s, "", "  ")
	storeMu.RUnlock()
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ConfigName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 出错时清理临时文件

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// 备份当前配置；备份失败不阻断写入。
	if old, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(filepath.Join(dir, BackupName), old, 0o600)
	}

	return os.Rename(tmpName, path)
}

// defaultSettings 返回补齐后的默认设置。
func defaultSettings() Settings {
	return Settings{
		HistoryLimit: DefaultHistoryLimit,
		Scrollback:   DefaultScrollback,
		Theme:        DefaultTheme,
	}
}

// normalizeLoaded 补齐加载后 Store 中的零值字段。
func normalizeLoaded(s *Store) {
	if s.Connections == nil {
		s.Connections = make([]Connection, 0)
	}
	if s.Favorites == nil {
		s.Favorites = make([]FavoriteCmd, 0)
	}
	if s.History == nil {
		s.History = make([]HistoryEntry, 0)
	}
	// 文件里的时间可能乱序，统一整理为「最新在前」，保证 History()/AddHistory 的语义一致。
	sortByTimeDesc(s.History)
	st := s.Settings.withDefaults()
	s.Settings = st
}

// withDefaults 返回补齐默认值后的 Settings 副本。
func (st Settings) withDefaults() Settings {
	if st.HistoryLimit <= 0 {
		st.HistoryLimit = DefaultHistoryLimit
	}
	if st.Scrollback <= 0 {
		st.Scrollback = DefaultScrollback
	}
	if st.Theme == "" {
		st.Theme = DefaultTheme
	}
	return st
}

// newID 生成一个随机的短 ID（8 字节随机数 hex 编码）。
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 正常情况下不会失败，兜底用纳秒时间戳保证不重复。
		binary.LittleEndian.PutUint64(b[:], uint64(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
}
