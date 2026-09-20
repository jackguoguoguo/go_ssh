package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
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

// fsLoadedMsg 目录加载完成。note 为操作成功后的提示（如「已下载…」）。
type fsLoadedMsg struct {
	path    string
	entries []remotessh.Entry
	err     error
	note    string
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
	if isLocalShell(s) {
		return m.setMsg("本地 shell 没有远端文件浏览器（Ctrl+O 仅用于 SSH 会话）")
	}
	fs, err := m.mgr.FS(s.TabID())
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
	// 目录加载 / 文件操作进行中：忽略按键，避免竞态。
	if d.fsLoading {
		return
	}
	// 参数输入态：所有按键用于编辑 fsInput（重命名 / 权限 / 新建目录 / 上传 / 下载）。
	if d.fsOp != "" {
		m.fsInputKey(d, msg)
		return
	}
	// 删除二次确认态
	if d.fsConfirmDel {
		switch msg.Type {
		case tea.KeyEnter, tea.KeyRunes:
			if msg.Type == tea.KeyEnter || (len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y')) {
				d.fsConfirmDel = false
				d.fsLoading = true
				m.afterCmd = m.fsRemoveCmd(d, d.fsInputPath, d.fsInputIsDir)
				return
			}
			d.fsConfirmDel = false
			d.fsInputPath = ""
			return
		case tea.KeyEsc:
			d.fsConfirmDel = false
			d.fsInputPath = ""
			return
		}
		return
	}

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
		case d.fsFilter == "" && (r == 'u'):
			m.fsNavigate(d, parentPath(d.fsPath))
			return
		case d.fsFilter == "" && (r == '~'):
			if wd, err := d.fs.Getwd(); err == nil {
				m.fsNavigate(d, wd)
			}
			return
		// ---- 文件维护操作（仅无过滤时触发，避免与过滤输入冲突）----
		case d.fsFilter == "" && (r == 'n'): // 新建目录
			m.fsStartOp(d, "mkdir", "新建目录：", "", false)
			return
		case d.fsFilter == "" && (r == 'r'): // 重命名
			if e, ok := d.currentFsEntry(); ok {
				m.fsStartOp(d, "rename", "重命名为：", e.Name, e.IsDir)
			}
			return
		case d.fsFilter == "" && (r == 'm'): // 改权限
			if e, ok := d.currentFsEntry(); ok {
				m.fsStartOp(d, "chmod", "权限(八进制，如 644)：", "", e.IsDir)
			}
			return
		case d.fsFilter == "" && (r == 'd'): // 下载到本地（目录则递归）
			if e, ok := d.currentFsEntry(); ok {
				m.fsStartOp(d, "download", "下载到本地路径：", e.Name, e.IsDir)
			}
			return
		case d.fsFilter == "" && (r == 'U'): // 上传本地文件到当前目录
			m.fsStartOp(d, "upload", "上传本地文件：", "", false)
			return
		case d.fsFilter == "" && (r == 'D'): // 删除（二次确认）
			if e, ok := d.currentFsEntry(); ok {
				d.fsConfirmDel = true
				d.fsInputPath = joinPath(d.fsPath, e.Name)
				d.fsInputIsDir = e.IsDir
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

// fsStartOp 进入某文件操作的参数输入态。
func (m *Model) fsStartOp(d *dlg, op, label, prefill string, isDir bool) {
	d.fsOp = op
	d.fsInputLabel = label
	d.fsInput = prefill
	d.fsInputIsDir = isDir
	if e, ok := d.currentFsEntry(); ok {
		d.fsInputPath = joinPath(d.fsPath, e.Name)
	} else {
		d.fsInputPath = ""
	}
}

// fsInputKey 处理参数输入态的按键（仅 esc/enter/backspace/可打印字符）。
func (m *Model) fsInputKey(d *dlg, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEsc:
		d.fsOp, d.fsInput, d.fsInputLabel = "", "", ""
	case tea.KeyEnter:
		m.fsSubmitOp(d)
	case tea.KeyBackspace:
		if len(d.fsInput) > 0 {
			d.fsInput = d.fsInput[:len(d.fsInput)-1]
		}
	default:
		if len(msg.Runes) == 1 && msg.Runes[0] >= 0x20 {
			d.fsInput += string(msg.Runes[0])
		}
	}
}

// fsSubmitOp 提交参数输入态，根据 fsOp 执行对应文件操作。
func (m *Model) fsSubmitOp(d *dlg) {
	op := d.fsOp
	input := strings.TrimSpace(d.fsInput)
	d.fsOp, d.fsInput, d.fsInputLabel = "", "", ""
	if input == "" {
		return
	}
	switch op {
	case "mkdir":
		d.fsLoading = true
		m.afterCmd = m.fsMkdirCmd(d, joinPath(d.fsPath, input))
	case "rename":
		if d.fsInputPath == "" {
			return
		}
		newp := joinPath(d.fsPath, input)
		d.fsLoading = true
		m.afterCmd = m.fsRenameCmd(d, d.fsInputPath, newp)
	case "chmod":
		mode, err := parseOctal(input)
		if err != nil {
			m.setMsg("权限格式错误：应为八进制数字，如 644")
			return
		}
		if d.fsInputPath == "" {
			return
		}
		d.fsLoading = true
		m.afterCmd = m.fsChmodCmd(d, d.fsInputPath, mode)
	case "download":
		if d.fsInputPath == "" {
			return
		}
		local := input
		if !filepath.IsAbs(local) {
			local = filepath.Join(".", local) // 相对当前目录
		}
		d.fsLoading = true
		if d.fsInputIsDir {
			// 目录：递归下载整棵子树到本地目录
			m.afterCmd = m.fsDownloadTreeCmd(d, d.fsInputPath, local)
		} else {
			m.afterCmd = m.fsDownloadCmd(d, d.fsInputPath, local)
		}
	case "upload":
		local := input
		if !filepath.IsAbs(local) {
			local = filepath.Join(".", local)
		}
		d.fsLoading = true
		if fi, err := os.Stat(local); err == nil && fi.IsDir() {
			// 目录：递归上传整棵子树到当前远端目录下的同名目录
			remote := joinPath(d.fsPath, filepath.Base(filepath.Clean(local)))
			m.afterCmd = m.fsUploadTreeCmd(d, local, remote)
		} else {
			remote := joinPath(d.fsPath, filepath.Base(local))
			m.afterCmd = m.fsUploadCmd(d, local, remote)
		}
	}
}

// parseOctal 解析八进制权限字符串（如 "644"、"0755"），返回低 12 位模式。
func parseOctal(s string) (os.FileMode, error) {
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil || v > 0o7777 {
		return 0, err
	}
	return os.FileMode(v), nil
}

// ---- 各操作的异步命令：执行后重新加载当前目录 ----

func (m *Model) fsMkdirCmd(d *dlg, p string) tea.Cmd {
	return func() tea.Msg {
		if err := d.fs.Mkdir(p); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("新建目录失败：%w", err)}
		}
		return fsReload(d)
	}
}

func (m *Model) fsRenameCmd(d *dlg, oldp, newp string) tea.Cmd {
	return func() tea.Msg {
		if err := d.fs.Rename(oldp, newp); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("重命名失败：%w", err)}
		}
		return fsReload(d)
	}
}

func (m *Model) fsChmodCmd(d *dlg, p string, mode os.FileMode) tea.Cmd {
	return func() tea.Msg {
		if err := d.fs.Chmod(p, mode); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("改权限失败：%w", err)}
		}
		return fsReload(d)
	}
}

func (m *Model) fsRemoveCmd(d *dlg, p string, isDir bool) tea.Cmd {
	return func() tea.Msg {
		if err := d.fs.RemoveAll(p); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("删除失败：%w", err)}
		}
		return fsReload(d)
	}
}

func (m *Model) fsDownloadCmd(d *dlg, remote, local string) tea.Cmd {
	return func() tea.Msg {
		data, err := d.fs.ReadFile(remote)
		if err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("读取远端失败：%w", err)}
		}
		if err := os.WriteFile(local, data, 0o644); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("写入本地失败：%w", err)}
		}
		return fsLoadedMsg{path: d.fsPath, note: "已下载到 " + local}
	}
}

func (m *Model) fsUploadCmd(d *dlg, local, remote string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(local)
		if err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("读取本地失败：%w", err)}
		}
		if err := d.fs.WriteFile(remote, data); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("上传失败：%w", err)}
		}
		return fsLoadedMsg{path: d.fsPath, note: "已上传 " + local}
	}
}

// fsReload 重新列出当前目录（供文件操作命令回调）。
func fsReload(d *dlg) tea.Msg {
	entries, err := d.fs.List(d.fsPath)
	return fsLoadedMsg{path: d.fsPath, entries: entries, err: err}
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
