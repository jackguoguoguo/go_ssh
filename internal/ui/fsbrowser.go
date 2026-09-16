package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/remotessh"
)

// fsEvent 由编辑监听协程发出，用于把「本地改动已回传」的结果送回主循环。
type fsEvent struct {
	path string
	err  string
}

// fsLoadedMsg 目录加载完成。
type fsLoadedMsg struct {
	path    string
	entries []remotessh.Entry
	err     error
}

// fsSavedMsg 一次自动回传完成。
type fsSavedMsg struct {
	path string
	err  string
}

// openFileBrowser 打开远端文件浏览器，基于当前活动会话的 SFTP 通道。
func (m *Model) openFileBrowser() tea.Cmd {
	s := m.activeSession()
	if s == nil {
		return m.setMsg("请先连接一台服务器")
	}
	fs, err := m.mgr.FS(s.ID)
	if err != nil {
		return m.setMsg("打开 SFTP 失败：" + err.Error())
	}
	start := m.remoteCwd()
	if start == "" {
		if wd, e := fs.Getwd(); e == nil {
			start = wd
		} else {
			start = "/"
		}
	}
	m.dlg = &dlg{
		kind:      dlgFile,
		title:     "远端文件浏览器（SFTP）",
		fs:        fs,
		fsPath:    start,
		fsLoading: true,
		fsDone:    make(chan struct{}),
	}
	return loadDirCmd(fs, start)
}

// loadDirCmd 异步读取远端目录。
func loadDirCmd(fs *remotessh.FSClient, p string) tea.Cmd {
	return func() tea.Msg {
		entries, err := fs.List(p)
		return fsLoadedMsg{path: p, entries: entries, err: err}
	}
}

// waitFsEvent 阻塞等待一次编辑回传事件。
func (m *Model) waitFsEvent() tea.Cmd {
	return func() tea.Msg {
		ev := <-m.fsEvents
		return fsSavedMsg{path: ev.path, err: ev.err}
	}
}

// visibleFs 返回按过滤条件筛选后的目录条目（保持 d.fsEntries 的顺序）。
func visibleFs(d *dlg) []remotessh.Entry {
	q := strings.ToLower(strings.TrimSpace(d.fsFilter))
	if q == "" {
		return d.fsEntries
	}
	out := make([]remotessh.Entry, 0, len(d.fsEntries))
	for _, e := range d.fsEntries {
		if strings.Contains(strings.ToLower(e.Name), q) {
			out = append(out, e)
		}
	}
	return out
}

// currentFsEntry 返回当前光标指向的条目（考虑过滤）。
func (d *dlg) currentFsEntry() (remotessh.Entry, bool) {
	vis := visibleFs(d)
	if d.fsCursor < 0 || d.fsCursor >= len(vis) {
		return remotessh.Entry{}, false
	}
	return vis[d.fsCursor], true
}

// fileKey 处理文件浏览器内的按键。
func (m *Model) fileKey(d *dlg, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyUp:
		if d.fsCursor > 0 {
			d.fsCursor--
			m.clampFsScroll(d)
		}
		return
	case tea.KeyDown:
		d.fsCursor++
		m.clampFsScroll(d)
		return
	case tea.KeyPgUp:
		d.fsCursor -= 8
		m.clampFsScroll(d)
		return
	case tea.KeyPgDown:
		d.fsCursor += 8
		m.clampFsScroll(d)
		return
	case tea.KeyEnter:
		e, ok := d.currentFsEntry()
		if !ok {
			return
		}
		if e.IsDir {
			m.fsNavigate(d, joinPath(d.fsPath, e.Name))
		} else {
			m.openFileAction(d, e, false)
		}
		return
	case tea.KeyEsc:
		if d.fsFilter != "" {
			d.fsFilter = ""
			d.fsCursor = 0
			return
		}
		m.closeDialog()
		return
	case tea.KeyBackspace:
		if d.fsFilter != "" {
			runes := []rune(d.fsFilter)
			d.fsFilter = string(runes[:len(runes)-1])
			d.fsCursor = 0
			return
		}
		// 无过滤时，Backspace 视为「上一级目录」
		m.fsNavigate(d, parentPath(d.fsPath))
		return
	}

	if len(msg.Runes) == 1 {
		r := msg.Runes[0]
		switch {
		case d.fsFilter == "" && (r == 'e' || r == 'E'):
			// 编辑：下载到本地临时文件，用本地工具打开，改动自动回传
			if e, ok := d.currentFsEntry(); ok && !e.IsDir {
				m.openFileAction(d, e, true)
			}
			return
		case d.fsFilter == "" && (r == 'u' || r == 'U'):
			m.fsNavigate(d, parentPath(d.fsPath))
			return
		case d.fsFilter == "" && (r == '~'):
			if wd, err := d.fs.Getwd(); err == nil {
				m.fsNavigate(d, wd)
			}
			return
		case r >= 0x20:
			d.fsFilter += string(r)
			d.fsCursor = 0
			d.fsScroll = 0
			return
		}
	}
}

// fsNavigate 跳转到某目录并异步加载。
func (m *Model) fsNavigate(d *dlg, p string) {
	if p == d.fsPath && !d.fsLoading {
		// 已经在加载或相同路径：重新拉取以确保最新
	}
	d.fsPath = p
	d.fsLoading = true
	d.fsCursor = 0
	d.fsScroll = 0
	m.afterCmd = loadDirCmd(d.fs, p)
}

// openFileAction 下载远端文件到本地临时目录，并用本地默认程序打开（编辑模式还会自动回传）。
func (m *Model) openFileAction(d *dlg, e remotessh.Entry, edit bool) {
	remote := joinPath(d.fsPath, e.Name)
	data, err := d.fs.ReadFile(remote)
	if err != nil {
		m.setMsg("读取失败：" + err.Error())
		return
	}
	tmp := filepath.Join(os.TempDir(), "sshtool-"+sanitizePath(remote))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		m.setMsg("写入临时文件失败：" + err.Error())
		return
	}
	if err := openWithLocalTool(tmp); err != nil {
		m.setMsg("打开失败：" + err.Error())
		return
	}
	if edit {
		m.setMsg("已下载并打开（编辑后自动回传）：" + remote)
		go m.watchAndUpload(d.fs, remote, tmp, d.fsDone)
		m.afterCmd = m.waitFsEvent()
	} else {
		m.setMsg("已下载并打开：" + remote)
	}
}

// watchAndUpload 轮询临时文件，本地发生改动时回传到远端。
func (m *Model) watchAndUpload(fs *remotessh.FSClient, remote, tmp string, done <-chan struct{}) {
	last := fileModTime(tmp)
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			mt := fileModTime(tmp)
			if mt.IsZero() || !mt.After(last) {
				continue
			}
			last = mt
			data, err := os.ReadFile(tmp)
			if err != nil {
				continue
			}
			if err := fs.WriteFile(remote, data); err != nil {
				m.fsEvents <- fsEvent{path: remote, err: err.Error()}
			} else {
				m.fsEvents <- fsEvent{path: remote, err: ""}
			}
		}
	}
}

// openWithLocalTool 调用操作系统默认程序打开文件。
func openWithLocalTool(p string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", p).Start()
	case "darwin":
		return exec.Command("open", p).Start()
	default:
		return exec.Command("xdg-open", p).Start()
	}
}

// clampFsScroll 保证光标落在可见范围内，并按需平移滚动窗口。
func (m *Model) clampFsScroll(d *dlg) {
	vis := visibleFs(d)
	if d.fsCursor < 0 {
		d.fsCursor = 0
	}
	if d.fsCursor >= len(vis) {
		d.fsCursor = len(vis) - 1
		if d.fsCursor < 0 {
			d.fsCursor = 0
		}
	}
	// 简单滚动：让光标尽量可见（假设单屏 ~20 行即可，无需精确）
	if d.fsCursor < d.fsScroll {
		d.fsScroll = d.fsCursor
	}
	if d.fsCursor >= d.fsScroll+20 {
		d.fsScroll = d.fsCursor - 19
	}
}

// padRune 把字符串按显示宽度裁剪/补齐到 n 个 rune（超长加 …）。
func padRune(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		if n <= 1 {
			return string(runes[:n])
		}
		return string(runes[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(runes))
}

// humanSize 把字节数格式化为可读字符串。
func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}

// sanitizePath 把远端路径转成文件名安全的后缀。
func sanitizePath(p string) string {
	p = strings.ReplaceAll(p, string(os.PathSeparator), "_")
	return strings.Map(func(r rune) rune {
		switch r {
		case ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, p)
}

// joinPath 拼接远端路径（支持 ~ 家目录）。
func joinPath(base, name string) string {
	if base == "~" {
		return "~/" + name
	}
	if strings.HasPrefix(base, "~/") {
		return base + "/" + name
	}
	return path.Join(base, name)
}

// parentPath 返回远端路径的上一级。
func parentPath(p string) string {
	if p == "~" || p == "/" {
		return p
	}
	if strings.HasPrefix(p, "~/") {
		rest := p[2:]
		if i := strings.LastIndex(rest, "/"); i >= 0 {
			if i == 0 {
				return "~"
			}
			return "~/" + rest[:i]
		}
		return "~"
	}
	dir := path.Dir(p)
	if dir == "." || dir == "" {
		return "/"
	}
	return dir
}

// fileModTime 返回文件最后修改时间。
func fileModTime(p string) time.Time {
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
