package ui

import (
	"path"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

func fsTestKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func newFsTestModel(t *testing.T) (*Model, *dlg) {
	t.Helper()
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	d := &dlg{
		kind:      dlgFile,
		fsPath:    "/home/u",
		fsEntries: []remotessh.Entry{{Name: "a.txt"}, {Name: "d", IsDir: true}},
	}
	m.dlg = d
	return &m, d
}

func TestParseOctal(t *testing.T) {
	if m, err := parseOctal("644"); err != nil || m != 0o644 {
		t.Fatalf("644 解析错误：%v %v", m, err)
	}
	if m, err := parseOctal("0755"); err != nil || m != 0o755 {
		t.Fatalf("0755 解析错误：%v %v", m, err)
	}
	for _, bad := range []string{"abc", "888", "", "99999"} {
		if _, err := parseOctal(bad); err == nil {
			t.Fatalf("非法权限 %q 应报错", bad)
		}
	}
}

func TestFsRenameFlow(t *testing.T) {
	m, d := newFsTestModel(t)
	m.fileKey(d, fsTestKey('r'))
	if d.fsOp != "rename" || d.fsInput != "a.txt" || d.fsInputPath != "/home/u/a.txt" {
		t.Fatalf("重命名输入态不对：%+v", d)
	}
	// 追加字符再回车提交
	m.fileKey(d, fsTestKey('2'))
	if d.fsInput != "a.txt2" {
		t.Fatalf("输入拼接错误：%q", d.fsInput)
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEnter})
	if d.fsOp != "" || m.afterCmd == nil || !d.fsLoading {
		t.Fatalf("提交后应进入加载态并派生命令：op=%q after=%v loading=%v", d.fsOp, m.afterCmd, d.fsLoading)
	}
}

func TestFsMkdirEmptyIsNoop(t *testing.T) {
	m, d := newFsTestModel(t)
	m.fileKey(d, fsTestKey('n'))
	if d.fsOp != "mkdir" {
		t.Fatalf("应进入新建目录态：%q", d.fsOp)
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEnter}) // 空输入
	if m.afterCmd != nil || d.fsLoading {
		t.Fatal("空输入不应派生命令")
	}
}

func TestFsChmodInvalidRejected(t *testing.T) {
	m, d := newFsTestModel(t)
	m.fileKey(d, fsTestKey('m'))
	for _, r := range "abc" {
		m.fileKey(d, fsTestKey(r))
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEnter})
	if m.afterCmd != nil || d.fsLoading {
		t.Fatal("非法权限不应派生命令")
	}
	if d.fsOp != "" {
		t.Fatal("非法输入后应退出输入态")
	}
}

func TestFsDeleteConfirm(t *testing.T) {
	m, d := newFsTestModel(t)
	m.fileKey(d, fsTestKey('D'))
	if !d.fsConfirmDel || d.fsInputPath != "/home/u/a.txt" {
		t.Fatalf("应进入删除确认：%+v", d)
	}
	// 非 y 键取消
	m.fileKey(d, fsTestKey('n'))
	if d.fsConfirmDel || m.afterCmd != nil {
		t.Fatal("非 y 键应取消删除")
	}
	// 再次触发并确认
	m.fileKey(d, fsTestKey('D'))
	m.fileKey(d, fsTestKey('y'))
	if d.fsConfirmDel || m.afterCmd == nil || !d.fsLoading {
		t.Fatal("确认删除应派生命令并进入加载态")
	}
}

func TestFsUploadDownloadEnter(t *testing.T) {
	m, d := newFsTestModel(t)
	m.fileKey(d, fsTestKey('U'))
	if d.fsOp != "upload" {
		t.Fatalf("应进入上传态：%q", d.fsOp)
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEsc})
	if d.fsOp != "" {
		t.Fatal("Esc 应退出输入态")
	}

	d.fsCursor = 1 // 目录
	m.fileKey(d, fsTestKey('d'))
	if d.fsOp != "download" || !d.fsInputIsDir || d.fsInput != "d" {
		t.Fatalf("目录应进入递归下载态：%+v", d)
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEsc})
	d.fsCursor = 0
	m.fileKey(d, fsTestKey('d'))
	if d.fsOp != "download" || d.fsInput != "a.txt" || d.fsInputIsDir {
		t.Fatalf("文件应进入下载态：%+v", d)
	}
}

// TestFsTransferRoundTrip 通过进程内 SSH 服务端验证 SFTP 双向传输与递归删除。
func TestFsTransferRoundTrip(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)

	s := connectTestSession(t, &m, st)
	fs, err := mgr.FS(s.ID)
	if err != nil {
		t.Fatalf("建立 SFTP 通道失败：%v", err)
	}
	defer fs.Close()

	wd, err := fs.Getwd()
	if err != nil {
		t.Fatalf("Getwd 失败：%v", err)
	}
	dir := path.Join(wd, "subops")
	if err := fs.Mkdir(dir); err != nil {
		t.Fatalf("Mkdir 失败：%v", err)
	}
	file := path.Join(dir, "a.txt")
	payload := []byte("hello sftp")
	if err := fs.WriteFile(file, payload); err != nil {
		t.Fatalf("WriteFile（上传）失败：%v", err)
	}
	got, err := fs.ReadFile(file)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("ReadFile（下载）不一致：%q err=%v", got, err)
	}
	if err := fs.Chmod(file, 0o644); err != nil {
		t.Fatalf("Chmod 失败：%v", err)
	}
	entries, err := fs.List(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List 结果异常：%v err=%v", entries, err)
	}
	// 递归删除目录
	if err := fs.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll 失败：%v", err)
	}
	if _, err := fs.List(dir); err == nil {
		t.Fatal("目录删除后 List 应报错")
	}
}
