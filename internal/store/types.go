// Package store 定义 sshtool 的持久化数据模型。
//
// 本文件中的结构体与常量为「冻结契约」：包名、字段名、JSON tag、常量值
// 在并行开发期间不得修改，其他包一律基于此契约实现。
package store

import "time"

// AuthType 认证方式。
type AuthType string

const (
	AuthPassword AuthType = "password" // 密码认证
	AuthKey      AuthType = "key"      // 私钥认证
)

// Connection 一条保存的 SSH 连接。
type Connection struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Group         string    `json:"group"`
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	User          string    `json:"user"`
	AuthType      AuthType  `json:"auth_type"`
	Password      string    `json:"password,omitempty"`       // AskPassword 为 true 时不落盘
	AskPassword   bool      `json:"ask_password"`             // 每次连接时询问密码
	KeyPath       string    `json:"key_path,omitempty"`       // 私钥路径
	KeyPassphrase string    `json:"key_passphrase,omitempty"` // 私钥口令
	AskPassphrase bool      `json:"ask_passphrase"`           // 每次连接时询问私钥口令
	StartupCmd    string    `json:"startup_cmd,omitempty"`    // 连接后自动执行
	Note          string    `json:"note,omitempty"`
	Favorite      bool      `json:"favorite"`
	LastUsedAt    time.Time `json:"last_used_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// FavoriteCmd 收藏的命令。
type FavoriteCmd struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Cmd       string    `json:"cmd"`
	Group     string    `json:"group"`
	UseCount  int       `json:"use_count"`
	CreatedAt time.Time `json:"created_at"`
}

// HistoryEntry 一条历史命令。
type HistoryEntry struct {
	ID   string    `json:"id"`
	Cmd  string    `json:"cmd"`
	Host string    `json:"host"`
	At   time.Time `json:"at"`
}

// Settings 全局设置。
type Settings struct {
	HistoryLimit int    `json:"history_limit"` // 历史命令最大条数，默认 1000
	Scrollback   int    `json:"scrollback"`    // 终端回滚缓冲行数，默认 5000
	Theme        string `json:"theme"`         // 主题名，默认 "dark"
	ConfirmQuit  bool   `json:"confirm_quit"`  // 退出前确认
	MouseEnabled bool   `json:"mouse_enabled"` // 是否启用鼠标
}

// Store 是全部持久化数据的根。
type Store struct {
	Connections []Connection   `json:"connections"`
	Favorites   []FavoriteCmd  `json:"favorites"`
	History     []HistoryEntry `json:"history"`
	Settings    Settings       `json:"settings"`

	path string // 配置文件路径
}

// 默认值常量。
const (
	DefaultHistoryLimit = 1000
	DefaultScrollback   = 5000
	DefaultTheme        = "dark"
	DefaultSSHPort      = 22
	DirName             = ".sshtool"
	ConfigName          = "config.json"
	BackupName          = "config.json.bak"
)
