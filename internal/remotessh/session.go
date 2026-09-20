package remotessh

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	StateReconnecting            // 连接断开后正在自动重连
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
	case StateReconnecting:
		return "重连中"
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

// 自动重连与心跳的可调参数（均可用环境变量覆盖，便于测试与特殊网络环境）。
const (
	defaultKeepaliveSecs = 30
	defaultReconnectMax  = 5
	defaultReconnectBase = 2 * time.Second
	reconnectBackoffMax  = 30 * time.Second
)

func keepaliveSecs() int { return intFromEnv("SSHTOOL_KEEPALIVE_SECS", defaultKeepaliveSecs) }
func reconnectMax() int  { return intFromEnv("SSHTOOL_RECONNECT_MAX", defaultReconnectMax) }

// reconnectBackoff 返回第 attempt 次重试前的等待时长（指数退避，封顶 reconnectBackoffMax）。
func reconnectBackoff(attempt int) time.Duration {
	if attempt < 2 {
		return 0
	}
	d := time.Duration(intFromEnv("SSHTOOL_RECONNECT_BASE", int(defaultReconnectBase/time.Second))) * time.Second
	for i := 2; i < attempt; i++ {
		d *= 2
		if d >= reconnectBackoffMax {
			return reconnectBackoffMax
		}
	}
	if d > reconnectBackoffMax {
		return reconnectBackoffMax
	}
	return d
}

// intFromEnv 读取正整数型环境变量，非法或缺失时回退默认值。
func intFromEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// Session 表示一个已建立的（或正在建立的）远程会话。
type Session struct {
	ID   string           // 会话唯一 ID
	Conn store.Connection // 对应的连接配置快照

	term *vt.Terminal // 屏幕缓冲区（内部自带锁）

	mu      sync.Mutex
	state   State
	err     error
	client  *sshx.Client
	jump    *sshx.Client // ProxyJump 跳板机的客户端；目标连接建立在其通道之上，需与之同生命周期
	session *sshx.Session
	stdin   io.WriteCloser
	cols    int
	rows    int
	closed  bool
	secret  string // 本次连接使用的密码 / 私钥口令，仅内存保存，用于重连时免重复询问

	// 自动重连相关
	reconnecting bool
	cancelOnce   sync.Once
	cancel       chan struct{} // 关闭后中断重连退避等待

	// 会话日志（落盘回放）：SSHTOOL_LOG_DIR 设置后启用，记录终端输出字节流。
	logMu   sync.Mutex
	logFile *os.File
	logPath string

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
	s := &Session{
		ID:     conn.ID,
		Conn:   conn,
		term:   vt.New(cols, rows, scrollback),
		state:  StateConnecting,
		cols:   cols,
		rows:   rows,
		cancel: make(chan struct{}),
		outCh:  outCh,
		events: events,
	}
	s.startLogging()
	return s
}

// logDir 返回会话日志目录（SSHTOOL_LOG_DIR）；为空表示不记录。
func logDir() string { return os.Getenv("SSHTOOL_LOG_DIR") }

// startLogging 在 SSHTOOL_LOG_DIR 设置时为本会话创建日志文件（每次 Open/重连共用同一文件）。
func (s *Session) startLogging() {
	dir := logDir()
	if dir == "" {
		return
	}
	sub := sanitizeName(s.Conn.User + "@" + s.Conn.Host + "_" + itoa(s.Conn.Port))
	if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
		return
	}
	ts := time.Now().Format("20060102-150405")
	name := sanitizeName(s.ID) + "_" + ts + ".log"
	path := filepath.Join(dir, sub, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return // 记录失败不阻塞主流程
	}
	s.logMu.Lock()
	s.logPath = path
	s.logFile = f
	s.logMu.Unlock()
}

// sanitizeName 把文件名中的非法字符替换为下划线。
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ', '\t':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Logging 表示本会话是否正在落盘记录。
func (s *Session) Logging() bool {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	return s.logFile != nil
}

// LogPath 返回日志文件路径（未启用时为空）。
func (s *Session) LogPath() string {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	return s.logPath
}

// appendLog 把一批终端输出字节追加到日志文件（尽力而为，忽略错误）。
func (s *Session) appendLog(p []byte) {
	if len(p) == 0 {
		return
	}
	s.logMu.Lock()
	f := s.logFile
	s.logMu.Unlock()
	if f == nil {
		return
	}
	_, _ = f.Write(p)
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
	jump := s.jump
	s.stdin, s.session, s.client, s.jump = nil, nil, nil, nil
	s.mu.Unlock()

	s.cancelOnce.Do(func() { close(s.cancel) })

	s.logMu.Lock()
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	s.logMu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if sess != nil {
		_ = sess.Close()
	}
	if client != nil {
		_ = client.Close()
	}
	// 目标连接建立在跳板机的通道之上，必须在目标关闭之后再关跳板机。
	if jump != nil {
		_ = jump.Close()
	}
	if prev != StateClosed {
		s.emit(Event{SessionID: s.ID, Kind: EventDisconnected})
	}
}

// Client 返回底层 SSH 客户端，供端口转发等场景复用已建立的连接；未就绪时返回 nil。
func (s *Session) Client() *sshx.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// Secret 返回本次连接使用的密码 / 私钥口令（仅内存，不落盘），供重连复用。
func (s *Session) Secret() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secret
}

// connectImpl 是实际建连逻辑的可替换点，便于测试注入故障序列。
var connectImpl func(*Session, string) error

func init() {
	connectImpl = connectReal
}

// connect 完成一次建连（同步）。成功返回 nil 并置 StateConnected；失败返回错误并置 StateError。
// 它不主动发事件：初始建连由 dial 包裹后转发给 fail()，自动重连由 reconnect 自行决定事件。
func (s *Session) connect(secret string) error { return connectImpl(s, secret) }

// dial 在后台协程中完成建连、申请 PTY 并启动 shell。
func (s *Session) dial(secret string) {
	go func() {
		if err := s.connect(secret); err != nil {
			s.fail(err)
		}
	}()
}

// setErr 在连接失败时记录错误并置异常态，关闭已建立的底层连接。
func (s *Session) setErr(err error) error {
	s.mu.Lock()
	if !s.closed {
		s.state = StateError
		s.err = err
	}
	cli := s.client
	s.mu.Unlock()
	if cli != nil {
		_ = cli.Close()
	}
	return err
}

// connectReal 真实的建连实现：TCP 握手 → SSH 认证 → PTY → shell，并启动输出/心跳/等待协程。
func connectReal(s *Session, secret string) error {
	addr := addrOf(s.Conn)
	user := userOf(s.Conn)

	s.mu.Lock()
	s.secret = secret
	s.mu.Unlock()

	methods, err := authMethods(s.Conn, secret)
	if err != nil {
		return s.setErr(err)
	}

	cfg := &sshx.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: hostKeyCallback(),
		Timeout:         DefaultDialTimeout,
	}

	// 配置了 SSHTOOL_PROXY_JUMP 时经跳板机建立；jump 可能非空，需随会话一起关闭。
	client, jump, err := dialClient(addr, cfg)
	if err != nil {
		return s.setErr(err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = client.Close()
		if jump != nil {
			_ = jump.Close()
		}
		return nil // 已被关闭，视为正常退出，不报错
	}
	s.client = client
	s.jump = jump
	s.mu.Unlock()

	sess, err := client.NewSession()
	if err != nil {
		return s.setErr(err)
	}

	modes := sshx.TerminalModes{
		sshx.ECHO:          1,
		sshx.TTY_OP_ISPEED: 14400,
		sshx.TTY_OP_OSPEED: 14400,
	}
	cols, rows := s.Cols(), s.Rows()
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sess.Close()
		return s.setErr(err)
	}

	pr, pw := io.Pipe()
	sess.Stdout = pw
	sess.Stderr = pw

	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		return s.setErr(err)
	}

	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		return s.setErr(err)
	}

	s.mu.Lock()
	s.session = sess
	s.stdin = stdin
	s.state = StateConnected
	s.err = nil
	s.mu.Unlock()

	go s.readLoop(pr)
	go s.keepalive()
	go func() {
		werr := sess.Wait()
		_ = pw.Close()
		s.mu.Lock()
		already := s.closed || s.state != StateConnected
		s.mu.Unlock()
		if already {
			return
		}
		if werr != nil {
			// 传输层断开（网络抖动 / 服务端重启），自动重连。
			s.reconnect()
			return
		}
		// shell 正常退出（如用户执行 exit）：不重连。
		s.mu.Lock()
		if s.state == StateConnected {
			s.state = StateClosed
		}
		s.mu.Unlock()
		s.emit(Event{SessionID: s.ID, Kind: EventDisconnected})
	}()

	s.emit(Event{SessionID: s.ID, Kind: EventConnected})

	// 让远端 shell 每次刷新提示符时通过 OSC 标题上报当前工作目录（前缀 SSHTPWD:）。
	// 终端模拟器会把 OSC 标题吞掉不显示，UI 读取标题即可静默获得远端 cwd，
	// 无需轮询命令、也不污染屏幕。bash 支持 PROMPT_COMMAND；sh 仅设置了无害的环境变量。
	s.injectCwdSync()

	if s.Conn.StartupCmd != "" {
		time.Sleep(300 * time.Millisecond)
		_ = s.Write([]byte(s.Conn.StartupCmd + "\n"))
	}
	return nil
}

// keepalive 周期性发送 SSH 保活探测，连续失败则关闭底层连接以触发自动重连。
// SSHTOOL_KEEPALIVE_SECS=0 可关闭（默认 30 秒）。
func (s *Session) keepalive() {
	secs := keepaliveSecs()
	if secs <= 0 {
		return
	}
	tick := time.NewTicker(time.Duration(secs) * time.Second)
	defer tick.Stop()

	const maxMiss = 2
	miss := 0
	for {
		select {
		case <-s.cancel:
			return
		case <-tick.C:
		}
		s.mu.Lock()
		cli, closed := s.client, s.closed
		s.mu.Unlock()
		if closed || cli == nil {
			return
		}
		// OpenSSH 保活探测：wantReply=true，服务端应回 should-be-replied。
		if _, _, err := cli.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			if miss++; miss >= maxMiss {
				_ = cli.Close() // 关闭后 Wait 报错 → reconnect
				return
			}
			continue
		}
		miss = 0
	}
}

// reconnect 在传输层断开后按指数退避自动重连，复用内存中的 secret。
// 用户主动 Close 会中断退避；超过最大次数则置异常态并提示手动重连。
func (s *Session) reconnect() {
	s.mu.Lock()
	if s.closed || s.reconnecting {
		s.mu.Unlock()
		return
	}
	s.reconnecting = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.reconnecting = false
		s.mu.Unlock()
	}()

	maxA := reconnectMax()
	for attempt := 1; attempt <= maxA; attempt++ {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.state = StateReconnecting
		s.mu.Unlock()
		s.emit(Event{SessionID: s.ID, Kind: EventReconnecting, Attempt: attempt})

		// 首次尝试立即进行，之后指数退避（可被 Close 中断）。
		if attempt > 1 {
			select {
			case <-time.After(reconnectBackoff(attempt)):
			case <-s.cancel:
				return
			}
		}
		if s.closed {
			return
		}

		err := s.connect(s.secret)
		if err == nil && s.State() == StateConnected {
			s.emit(Event{SessionID: s.ID, Kind: EventReconnected})
			return
		}

		// 指纹变更等需要用户决策的错误：交给 UI，不再盲目重试。
		var hke *HostKeyError
		if errors.As(err, &hke) {
			s.mu.Lock()
			if !s.closed {
				s.state = StateError
				s.err = err
			}
			s.mu.Unlock()
			s.emit(Event{SessionID: s.ID, Kind: EventNeedHostKey, Err: err})
			return
		}
	}
	s.mu.Lock()
	if !s.closed {
		s.state = StateError
		s.err = errors.New("自动重连失败，请按 Ctrl+R 手动重连")
	}
	s.mu.Unlock()
	s.emit(Event{SessionID: s.ID, Kind: EventReconnectFailed})
}

// injectCwdSync 让远端 shell 每次刷新提示符时通过 OSC 标题上报当前工作目录。
// 标题格式为 `SSHTPWD:<cwd>`，由终端模拟器静默捕获，供 UI 读取。
func (s *Session) injectCwdSync() {
	// 原始字符串：反斜杠保持字面量，交给远端 printf 解释（\033=ESC，\\=单反斜杠=OSC 终结 ST）。
	cmd := `PROMPT_COMMAND='printf "\033]0;SSHTPWD:%s\033\\" "$(pwd)"'`
	_ = s.Write([]byte(cmd + "\n"))
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
		s.appendLog(pending)
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
	var hke *HostKeyError
	if errors.As(err, &hke) {
		// 指纹问题不是「连不上」，而是「要不要信」：交给 UI 询问用户，
		// 非交互调用方（RunOnce）则表现为一次明确的失败。
		s.emit(Event{SessionID: s.ID, Kind: EventNeedHostKey, Err: err})
		return
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
