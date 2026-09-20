package ui

import (
	"sshtool/internal/localshell"
	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/vt"
)

// tabSession 是标签栏里一个「会话」的抽象：既可以是 SSH 会话，也可以是本地 shell。
// 两种会话的能力并不完全一致（例如 SFTP、广播、远端 cwd 上报只属于 SSH），
// 需要 SSH 专属能力的调用点请先用 sshSessionOf 取回具体类型。
type tabSession interface {
	TabID() string
	Label() string
	ConnInfo() store.Connection
	State() remotessh.State
	Term() *vt.Terminal
	Write(p []byte) error
	Err() error
	Cols() int
	Rows() int
	Logging() bool
	Secret() string
	Resize(cols, rows int)
	Close()
}

// sshSessionOf 若该标签是 SSH 会话则返回具体类型，否则返回 nil。
func sshSessionOf(s tabSession) *remotessh.Session {
	if s == nil {
		return nil
	}
	if rs, ok := s.(*remotessh.Session); ok {
		return rs
	}
	return nil
}

// isLocalShell 判断标签是否为本地 shell。
func isLocalShell(s tabSession) bool {
	_, ok := s.(*localshell.Session)
	return ok
}
