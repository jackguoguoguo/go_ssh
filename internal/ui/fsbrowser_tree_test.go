package ui

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

// TestFsTreeUploadDownloadRoundTrip 通过进程内 SSH 服务端验证目录递归上传/下载（保留子目录结构）。
func TestFsTreeUploadDownloadRoundTrip(t *testing.T) {
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

	// 构造本地目录树：root.txt / a/x.txt / a/b/y.txt
	local := t.TempDir()
	mustMkdir(t, filepath.Join(local, "a", "b"))
	mustWrite(t, filepath.Join(local, "root.txt"), "root")
	mustWrite(t, filepath.Join(local, "a", "x.txt"), "x")
	mustWrite(t, filepath.Join(local, "a", "b", "y.txt"), "y")

	d := &dlg{kind: dlgFile, fs: fs, fsPath: wd}
	remoteDir := path.Join(wd, "tree")

	// 递归上传
	um := m.fsUploadTreeCmd(d, local, remoteDir)()
	if lm, ok := um.(fsLoadedMsg); !ok || lm.err != nil {
		t.Fatalf("递归上传失败：%+v", um)
	}

	// 远端应存在嵌套文件
	if got, err := fs.ReadFile(path.Join(remoteDir, "a", "b", "y.txt")); err != nil || string(got) != "y" {
		t.Fatalf("远端嵌套文件校验失败：%q err=%v", got, err)
	}
	if got, err := fs.ReadFile(path.Join(remoteDir, "root.txt")); err != nil || string(got) != "root" {
		t.Fatalf("远端根文件校验失败：%q err=%v", got, err)
	}

	// 递归下载回另一个本地目录
	back := t.TempDir()
	dm := m.fsDownloadTreeCmd(d, remoteDir, back)()
	if lm, ok := dm.(fsLoadedMsg); !ok || lm.err != nil {
		t.Fatalf("递归下载失败：%+v", dm)
	}
	for rel, want := range map[string]string{
		"root.txt":  "root",
		"a/x.txt":   "x",
		"a/b/y.txt": "y",
	} {
		data, err := os.ReadFile(filepath.Join(back, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("回读 %s 失败：%v", rel, err)
		}
		if string(data) != want {
			t.Fatalf("回读 %s = %q，期望 %q", rel, data, want)
		}
	}

	// 清理远端
	if err := fs.RemoveAll(remoteDir); err != nil {
		t.Fatalf("清理远端目录失败：%v", err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}