package remotessh_test

import (
	"strings"
	"testing"
	"time"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

func TestSFTPRoundTrip(t *testing.T) {
	srv := testutil.StartSSHServer(t)

	m := remotessh.NewManager()
	defer m.CloseAll()

	conn := store.Connection{
		Name:     "sftp-e2e",
		Host:     srv.Host,
		Port:     srv.Port,
		User:     "test",
		AuthType: store.AuthPassword,
		Password: "pass",
	}
	s, _ := m.Open(conn, "pass", 80, 24, store.DefaultScrollback)

	// 等待连接就绪
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == remotessh.StateConnected {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s.State() != remotessh.StateConnected {
		t.Fatalf("会话未连接，状态 = %v", s.State())
	}

	fs, err := m.FS(s.ID)
	if err != nil {
		t.Fatalf("打开 SFTP 失败: %v", err)
	}
	defer fs.Close()

	wd, err := fs.Getwd()
	if err != nil {
		t.Fatalf("Getwd 失败: %v", err)
	}
	if !strings.HasPrefix(wd, "/") {
		t.Fatalf("Getwd 应返回绝对路径，得到 %q", wd)
	}

	content := []byte("hello sftp\n")
	remote := wd + "/sftp-test.txt"
	if err := fs.WriteFile(remote, content); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	got, err := fs.ReadFile(remote)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("内容不一致: got %q want %q", got, content)
	}

	entries, err := fs.List(wd)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "sftp-test.txt" && !e.IsDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("List 结果中未找到 sftp-test.txt: %+v", entries)
	}

	if err := fs.Rename(remote, wd+"/sftp-renamed.txt"); err != nil {
		t.Fatalf("Rename 失败: %v", err)
	}
	if err := fs.Remove(wd + "/sftp-renamed.txt"); err != nil {
		t.Fatalf("Remove 失败: %v", err)
	}
	// 删除后再次读取应失败
	if _, err := fs.ReadFile(wd + "/sftp-renamed.txt"); err == nil {
		t.Fatal("Remove 后文件仍可被读取，删除似乎未生效")
	}
}
