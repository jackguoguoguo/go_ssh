// Package localshell 提供「本地 shell 标签」：在本机打开一个真正的 PTY 跑 shell，
// 与 SSH 会话并列展示在同一个标签栏里。
//
// 与 SSH 会话不同，本地 shell 不走网络：输入写入 PTY，输出经终端模拟器渲染。
// resize 会同步 PTY 窗口尺寸，因此 vim / top 这类全屏程序也能正常工作。
// PTY 后端按平台拆分：Unix 用 creack/pty，Windows 用 ConPTY（见 shell_unix.go /
// shell_windows.go）。
package localshell

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/user"
	"sync"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/vt"
)

// ptyDevice 抽象一个伪终端句柄：读写即与 shell 交互，Resize 同步窗口尺寸。
type ptyDevice interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Close() error
}

// Session 一个本地 shell 会话。
type Session struct {
	id     string
	conn   store.Connection
	term   *vt.Terminal
	dev    ptyDevice
	cols   int
	rows   int
	mu     sync.Mutex
	err    error
	closed bool
	notify func(id string) // 有输出时通知 UI 重绘
}

// Open 启动本地 shell。cols/rows 为初始窗口尺寸，notify 用于输出通知（可为 nil）。
func Open(cols, rows, scrollback int, notify func(id string)) (*Session, error) {
	if cols < 8 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	if scrollback <= 0 {
		scrollback = 5000
	}

	dev, wait, err := openPTY(cols, rows)
	if err != nil {
		return nil, err
	}

	s := &Session{
		id:   newID(),
		term: vt.New(cols, rows, scrollback),
		dev:  dev,
		cols: cols,
		rows: rows,
		conn: store.Connection{
			Name: "本地 shell",
			Host: "localhost",
			User: currentUser(),
		},
		notify: notify,
	}
	s.conn.ID = s.id

	go s.readLoop()
	go s.waitLoop(wait)
	return s, nil
}

// readLoop 把 PTY 输出写入终端缓冲区并通知 UI。
func (s *Session) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.dev.Read(buf)
		if n > 0 {
			s.mu.Lock()
			_, _ = s.term.Write(buf[:n])
			s.mu.Unlock()
			if s.notify != nil {
				s.notify(s.id)
			}
		}
		if err != nil {
			return
		}
	}
}

// waitLoop 等待进程退出并标记会话结束。
func (s *Session) waitLoop(wait func() error) {
	err := wait()
	s.mu.Lock()
	s.closed = true
	if err != nil {
		s.err = err
	}
	s.mu.Unlock()
	if s.notify != nil {
		s.notify(s.id)
	}
}

// ---------- 与 remotessh.Session 对齐的接口 ----------

func (s *Session) TabID() string              { return s.id }
func (s *Session) Label() string              { return "本地 shell" }
func (s *Session) ConnInfo() store.Connection { return s.conn }
func (s *Session) Logging() bool              { return false }
func (s *Session) Secret() string             { return "" }
func (s *Session) Term() *vt.Terminal         { return s.term }
func (s *Session) Cols() int                  { return s.cols }
func (s *Session) Rows() int                  { return s.rows }

// State 会话状态：进程结束后为已关闭，否则视为已连接。
func (s *Session) State() remotessh.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return remotessh.StateClosed
	}
	return remotessh.StateConnected
}

func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Write 把输入写入 PTY（等价于用户敲键）。
func (s *Session) Write(p []byte) error {
	if s.dev == nil {
		return os.ErrClosed
	}
	_, err := s.dev.Write(p)
	return err
}

// Resize 同步 PTY 窗口尺寸与终端缓冲区大小。
func (s *Session) Resize(cols, rows int) {
	if cols < 1 || rows < 1 || s.dev == nil {
		return
	}
	_ = s.dev.Resize(cols, rows)
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.term.Resize(cols, rows)
	s.mu.Unlock()
}

// Close 终止本地 shell 进程并释放 PTY。
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	if s.dev != nil {
		_ = s.dev.Close()
	}
}

// currentUser 返回本机用户名（取不到时退化为环境变量）。
func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// newID 生成会话 ID。
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "local"
	}
	return "local-" + hex.EncodeToString(b[:])
}
