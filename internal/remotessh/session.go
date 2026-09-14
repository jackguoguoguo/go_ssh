package remotessh

import (
	"io"
	"sync"
	"time"

	sshx "golang.org/x/crypto/ssh"

	"sshtool/internal/store"
	"sshtool/internal/vt"
)

// State 表示会话生命周期状态。
type State int

const (
	StateConnecting State = iota // 正在建立连接
	StateConnected               // 已连接
	StateClosed                  // 已关闭（用户主动断开）
	StateError                   // 连接失败或异常断开
)

// String 返回状态的可读描述。
func (s State) String() string {
	switch s {
	case StateConnecting:
		return "连接中"
	case StateConnected:
		return "已连接"
	case StateClosed:
		return "已关闭"
	case StateError:
		return "异常"
	}
	return "未知"
}

// 输出批量聚合的时间窗口：窗口内的输出合并后一次性写入终端缓冲区，
// 避免高频输出把 UI 主线程打满。
const batchWindow = 8 * time.Millisecond

// Session 表示一个已建立的（或正在建立的）远程会话。
type Session struct {
	ID   string           // 会话唯一 ID
	Conn store.Connection // 对应的连接配置快照

	term *vt.Terminal // 屏幕缓冲区（内部自带锁）

	mu      sync.Mutex
	state   State
	err     error
	client  *sshx.Client
	session *sshx.Session
	stdin   io.WriteCloser
	cols    int
	rows    int
	closed  bool

	outCh  chan<- string // 全局输出通知通道（非阻塞，满了就丢弃）
	events chan<- Event  // 全局事件通道
}

// newSession 创建一个处于 StateConnecting 的会话。
func newSession(conn store.Connection, cols, rows int, events chan<- Event, outCh chan<- string, scrollback int) *Session {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	if scrollback < 0 {
		scrollback = store.DefaultScrollback
	}
	return &Session{
		ID:     conn.ID,
		Conn:   conn,
		term:   vt.New(cols, rows, scrollback),
		state:  StateConnecting,
		cols:   cols,
		rows:   rows,
		outCh:  outCh,
		events: events,
	}
}

// Term 返回该会话的终端缓冲区。
func (s *Session) Term() *vt.Terminal { return s.term }

// State 返回当前状态。
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Err 返回失败原因（仅在 StateError 时有意义）。
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Cols / Rows 返回当前终端尺寸。
func (s *Session) Cols() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols
}

func (s *Session) Rows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows
}

// Label 返回用于标签页展示的名字。
func (s *Session) Label() string {
	if s.Conn.Name != "" {
		return s.Conn.Name
	}
	if s.Conn.User != "" {
		return s.Conn.User + "@" + s.Conn.Host
	}
	return s.Conn.Host
}

// Write 把本地输入发送到远端 stdin。
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	w := s.stdin
	s.mu.Unlock()
	if w == nil {
		return io.ErrClosedPipe
	}
	_, err := w.Write(p)
	return err
}

// Resize 同步终端尺寸到缓冲区与远端 PTY。
func (s *Session) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	sess := s.session
	s.mu.Unlock()

	s.term.Resize(cols, rows)
	if sess != nil {
		_ = sess.WindowChange(rows, cols)
	}
}

// Close 关闭会话并释放资源，可重复调用。
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	prev := s.state
	s.state = StateClosed
	stdin := s.stdin
	sess := s.session
	client := s.client
	s.stdin, s.session, s.client = nil, nil, nil
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if sess != nil {
		_ = sess.Close()
	}
	if client != nil {
		_ = client.Close()
	}
	if prev != StateClosed {
		s.emit(Event{SessionID: s.ID, Kind: EventDisconnected})
	}
}

// dial 在后台协程中完成建连、申请 PTY 并启动 shell。
func (s *Session) dial(secret string) {
	addr := addrOf(s.Conn)
	user := userOf(s.Conn)

	methods, err := authMethods(s.Conn, secret)
	if err != nil {
		s.fail(err)
		return
	}

	cfg := &sshx.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: hostKeyCallback(),
		Timeout:         DefaultDialTimeout,
	}

	client, err := sshx.Dial("tcp", addr, cfg)
	if err != nil {
		s.fail(err)
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = client.Close()
		return
	}
	s.client = client
	s.mu.Unlock()

	sess, err := client.NewSession()
	if err != nil {
		s.fail(err)
		return
	}

	modes := sshx.TerminalModes{
		sshx.ECHO:          1,
		sshx.TTY_OP_ISPEED: 14400,
		sshx.TTY_OP_OSPEED: 14400,
	}
	cols, rows := s.Cols(), s.Rows()
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sess.Close()
		s.fail(err)
		return
	}

	pr, pw := io.Pipe()
	sess.Stdout = pw
	sess.Stderr = pw

	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		s.fail(err)
		return
	}

	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		s.fail(err)
		return
	}

	s.mu.Lock()
	s.session = sess
	s.stdin = stdin
	s.state = StateConnected
	s.err = nil
	s.mu.Unlock()

	go s.readLoop(pr)
	go func() {
		_ = sess.Wait()
		_ = pw.Close()
		s.mu.Lock()
		already := s.closed || s.state != StateConnected
		s.mu.Unlock()
		if !already {
			// Wait 正常返回意味着远端 shell 退出（可能是用户执行了 exit）。
			s.mu.Lock()
			if s.state == StateConnected {
				s.state = StateClosed
			}
			s.mu.Unlock()
			s.emit(Event{SessionID: s.ID, Kind: EventDisconnected})
		}
	}()

	s.emit(Event{SessionID: s.ID, Kind: EventConnected})

	if s.Conn.StartupCmd != "" {
		time.Sleep(300 * time.Millisecond)
		_ = s.Write([]byte(s.Conn.StartupCmd + "\n"))
	}
}

// readLoop 读取远端输出，按 batchWindow 聚合成批后写入终端缓冲区。
func (s *Session) readLoop(r io.Reader) {
	ch := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				p := make([]byte, n)
				copy(p, buf[:n])
				ch <- p
			}
			if err != nil {
				close(ch)
				return
			}
		}
	}()

	tick := time.NewTicker(batchWindow)
	defer tick.Stop()

	var pending []byte
	flush := func() {
		if len(pending) == 0 {
			return
		}
		_, _ = s.term.Write(pending)
		pending = pending[:0]
		s.signal()
	}

	for {
		select {
		case p, ok := <-ch:
			if !ok {
				flush()
				return
			}
			pending = append(pending, p...)
			if len(pending) >= 128*1024 {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

// signal 非阻塞地通知 UI 有新输出。
func (s *Session) signal() {
	if s.outCh == nil {
		return
	}
	select {
	case s.outCh <- s.ID:
	default:
	}
}

func (s *Session) emit(e Event) {
	if s.events == nil {
		return
	}
	select {
	case s.events <- e:
	default:
	}
}

func (s *Session) fail(err error) {
	s.mu.Lock()
	if !s.closed {
		s.state = StateError
		s.err = err
	}
	s.mu.Unlock()
	if s.client != nil {
		_ = s.client.Close()
	}
	if err == ErrNeedPassphrase || err == ErrNeedPassword {
		s.emit(Event{SessionID: s.ID, Kind: EventNeedSecret, Err: err})
		return
	}
	s.emit(Event{SessionID: s.ID, Kind: EventError, Err: err})
}

// itoa 是 strconv.Itoa 的轻量替代，避免额外依赖。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
