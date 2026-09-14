package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), DirName, ConfigName)
}

// toJSON 把任意值序列化为字符串用于比较。
// time.Time 内部的单调时钟读数不会落盘，因此序列化后再比较可以避免误判。
func toJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return string(data)
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("USERPROFILE", filepath.Join("home", "jack"))
	// Windows 与 Unix 均依赖各自环境变量，这里同时设置。
	t.Setenv("HOME", filepath.Join("home", "jack"))
	want := filepath.Join("home", "jack", DirName, ConfigName)
	if got := DefaultPath(); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}

	t.Setenv("USERPROFILE", "")
	t.Setenv("HOME", "")
	if got := DefaultPath(); got != filepath.Join(DirName, ConfigName) {
		t.Fatalf("退化路径 = %q, want %q", got, filepath.Join(DirName, ConfigName))
	}
}

func TestLoadFromMissingFile(t *testing.T) {
	p := tempPath(t)
	s, err := LoadFrom(p)
	if err != nil {
		t.Fatalf("缺少文件时不应报错: %v", err)
	}
	if s == nil {
		t.Fatal("LoadFrom 返回了 nil Store")
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadFrom 不应创建文件, stat err = %v", err)
	}
	if st := s.GetSettings(); st.HistoryLimit != DefaultHistoryLimit ||
		st.Scrollback != DefaultScrollback || st.Theme != DefaultTheme {
		t.Fatalf("默认设置未补齐: %+v", st)
	}
	if s.Path() != p {
		t.Fatalf("Path() = %q, want %q", s.Path(), p)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := tempPath(t)
	s := New(p)
	c := s.AddConnection(Connection{Name: "web", Host: "10.0.0.1", User: "root"})
	s.AddFavorite(FavoriteCmd{Name: "df", Cmd: "df -h"})
	s.AddHistory("uptime", "10.0.0.1")
	s.TouchConnection(c.ID)
	s.UpdateSettings(func(st *Settings) { st.Theme = "light"; st.ConfirmQuit = true })

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("Save 后文件应存在: %v", err)
	}

	got, err := LoadFrom(p)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if gotJSON, wantJSON := toJSON(t, got.GetConnections()), toJSON(t, s.GetConnections()); gotJSON != wantJSON {
		t.Fatalf("连接不一致:\n got %s\nwant %s", gotJSON, wantJSON)
	}
	if gotJSON, wantJSON := toJSON(t, got.GetFavorites()), toJSON(t, s.GetFavorites()); gotJSON != wantJSON {
		t.Fatalf("收藏不一致:\n got %s\nwant %s", gotJSON, wantJSON)
	}
	if gotJSON, wantJSON := toJSON(t, got.GetHistory()), toJSON(t, s.GetHistory()); gotJSON != wantJSON {
		t.Fatalf("历史不一致:\n got %s\nwant %s", gotJSON, wantJSON)
	}
	if !reflect.DeepEqual(got.GetSettings(), s.GetSettings()) {
		t.Fatalf("设置不一致: got %+v want %+v", got.GetSettings(), s.GetSettings())
	}

	// 第二次 Save 应生成备份文件。
	if err := got.Save(); err != nil {
		t.Fatalf("再次 Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(p), BackupName)); err != nil {
		t.Fatalf("备份文件应存在: %v", err)
	}
}

func TestLoadFromCorruptFallsBackToBackup(t *testing.T) {
	p := tempPath(t)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	// 先写出一份合法配置并保存，得到备份。
	s := New(p)
	s.AddConnection(Connection{Name: "backup-host", Host: "b.example.com"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadFrom(p)
	if err != nil {
		t.Fatalf("应从备份恢复且不报错: %v", err)
	}
	conns := got.GetConnections()
	if len(conns) != 1 || conns[0].Name != "backup-host" {
		t.Fatalf("未从备份恢复: %+v", conns)
	}
}

func TestLoadFromCorruptNoBackup(t *testing.T) {
	p := tempPath(t)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not-json-at-all"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadFrom(p)
	if err == nil {
		t.Fatal("主文件与备份均不可用时应返回错误")
	}
	if got == nil {
		t.Fatal("返回错误时 Store 也不能为 nil")
	}
	if st := got.GetSettings(); st.Theme != DefaultTheme {
		t.Fatalf("兜底 Store 应带默认设置: %+v", st)
	}
}

func TestAddConnectionDefaults(t *testing.T) {
	s := New(tempPath(t))
	c := s.AddConnection(Connection{Name: "minimal", Host: "h"})

	if c.ID == "" {
		t.Fatal("应自动生成 ID")
	}
	if c.Port != DefaultSSHPort {
		t.Fatalf("Port = %d, want %d", c.Port, DefaultSSHPort)
	}
	if c.AuthType != AuthPassword {
		t.Fatalf("AuthType = %q, want %q", c.AuthType, AuthPassword)
	}
	if c.CreatedAt.IsZero() {
		t.Fatal("CreatedAt 应填充")
	}

	// 显式值不应被覆盖。
	given := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	c2 := s.AddConnection(Connection{Name: "full", Host: "h", Port: 2222, AuthType: AuthKey, CreatedAt: given})
	if c2.Port != 2222 || c2.AuthType != AuthKey || !c2.CreatedAt.Equal(given) {
		t.Fatalf("显式字段被覆盖: %+v", c2)
	}
	if c2.ID == c.ID {
		t.Fatal("ID 不应重复")
	}

	got, ok := s.Connection(c.ID)
	if !ok || got.Name != "minimal" {
		t.Fatalf("Connection() 查找失败: %+v ok=%v", got, ok)
	}
	if _, ok := s.Connection("nope"); ok {
		t.Fatal("不存在的 ID 应返回 false")
	}

	// 返回值是副本，修改不应影响内部状态。
	c.Name = "changed"
	if again, _ := s.Connection(c.ID); again.Name != "minimal" {
		t.Fatal("返回的连接疑似未拷贝")
	}
}

func TestUpdateRemoveErrors(t *testing.T) {
	s := New(tempPath(t))
	c := s.AddConnection(Connection{Name: "a", Host: "h"})

	c.Note = "updated"
	if err := s.UpdateConnection(c); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}
	if err := s.UpdateConnection(Connection{ID: "missing"}); err == nil {
		t.Fatal("UpdateConnection 缺失 ID 应报错")
	}
	if err := s.RemoveConnection(c.ID); err != nil {
		t.Fatalf("RemoveConnection: %v", err)
	}
	if err := s.RemoveConnection(c.ID); err == nil {
		t.Fatal("RemoveConnection 缺失 ID 应报错")
	}

	f := s.AddFavorite(FavoriteCmd{Name: "n", Cmd: "ls"})
	if err := s.UpdateFavorite(FavoriteCmd{ID: "missing"}); err == nil {
		t.Fatal("UpdateFavorite 缺失 ID 应报错")
	}
	if err := s.RemoveFavorite(f.ID); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	if err := s.RemoveFavorite(f.ID); err == nil {
		t.Fatal("RemoveFavorite 缺失 ID 应报错")
	}
	if err := s.RemoveHistory("missing"); err == nil {
		t.Fatal("RemoveHistory 缺失 ID 应报错")
	}
}

func TestGetConnectionsSorting(t *testing.T) {
	s := New(tempPath(t))
	s.AddConnection(Connection{Name: "beta", Host: "h"})
	s.AddConnection(Connection{Name: "Alpha", Host: "h"})
	s.AddConnection(Connection{Name: "zeta", Host: "h", Favorite: true})

	names := make([]string, 0, 3)
	for _, c := range s.GetConnections() {
		names = append(names, c.Name)
	}
	want := []string{"zeta", "Alpha", "beta"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("排序结果 = %v, want %v", names, want)
	}
}

func TestFavoritesSorting(t *testing.T) {
	s := New(tempPath(t))
	a := s.AddFavorite(FavoriteCmd{Name: "bbb", Cmd: "1", UseCount: 1})
	b := s.AddFavorite(FavoriteCmd{Name: "aaa", Cmd: "2", UseCount: 5})
	c := s.AddFavorite(FavoriteCmd{Name: "ccc", Cmd: "3", UseCount: 5})

	s.TouchFavorite(a.ID)
	got := s.GetFavorites()
	if got[0].ID != b.ID || got[1].ID != c.ID || got[2].ID != a.ID {
		t.Fatalf("收藏排序错误: %+v", got)
	}
	f, ok := s.Favorite(b.ID)
	if !ok || f.UseCount != 5 {
		t.Fatalf("Favorite() 查找失败: %+v ok=%v", f, ok)
	}
}

func TestAddHistoryDedupAndTrim(t *testing.T) {
	s := New(tempPath(t))

	if e := s.AddHistory("   ", "h"); e.ID != "" || e.Cmd != "" {
		t.Fatal("空白命令不应记录")
	}
	if len(s.GetHistory()) != 0 {
		t.Fatal("空白命令不该进入历史")
	}

	first := s.AddHistory(" ls -la ", "host-a")
	if first.Cmd != "ls -la" {
		t.Fatalf("命令应去除首尾空白, got %q", first.Cmd)
	}
	if first.ID == "" {
		t.Fatal("历史条目应带 ID")
	}
	s.AddHistory("pwd", "host-a")
	if n := len(s.GetHistory()); n != 2 {
		t.Fatalf("应有 2 条历史, got %d", n)
	}

	// 与最新一条完全相同：不追加，只更新时间。
	time.Sleep(time.Millisecond)
	dup := s.AddHistory("pwd", "host-a")
	if n := len(s.GetHistory()); n != 2 {
		t.Fatalf("重复命令不应追加, got %d 条", n)
	}
	if dup.ID != "" && !dup.At.After(first.At) {
		t.Fatalf("重复命令应刷新时间: %+v", dup)
	}
	// 不同 host 的同名命令属于不同条目。
	s.AddHistory("pwd", "host-b")
	if n := len(s.GetHistory()); n != 3 {
		t.Fatalf("不同 host 应算不同条目, got %d", n)
	}

	// 裁剪：limit = 2。
	s.UpdateSettings(func(st *Settings) { st.HistoryLimit = 2 })
	s.AddHistory("whoami", "host-a")
	hist := s.GetHistory()
	if len(hist) != 2 {
		t.Fatalf("裁剪后应为 2 条, got %d", len(hist))
	}
	if hist[0].Cmd != "whoami" {
		t.Fatalf("最新一条应在最前: %+v", hist)
	}

	// limit <= 0 时使用 DefaultHistoryLimit，不裁剪。
	s.UpdateSettings(func(st *Settings) { st.HistoryLimit = 0 })
	s.AddHistory("tail -f /var/log/app.log", "host-a")
	if n := len(s.GetHistory()); n != 3 {
		t.Fatalf("limit<=0 应回退为默认值, got %d 条", n)
	}

	s.ClearHistory()
	if n := len(s.GetHistory()); n != 0 {
		t.Fatalf("ClearHistory 后应为空, got %d", n)
	}
}

func TestSearchHistory(t *testing.T) {
	s := New(tempPath(t))
	s.AddHistory("docker ps", "prod-01")
	s.AddHistory("Docker logs -f app", "prod-02")
	s.AddHistory("kubectl get pods", "K8S-Prod")

	all := s.SearchHistory("")
	if len(all) != 3 {
		t.Fatalf("空关键字应返回全部, got %d", len(all))
	}
	if all[0].Cmd != "kubectl get pods" {
		t.Fatalf("时间倒序错误: %+v", all)
	}

	got := s.SearchHistory("docker")
	if len(got) != 2 {
		t.Fatalf("大小写不敏感匹配失败: %+v", got)
	}
	got = s.SearchHistory("K8S")
	if len(got) != 1 || got[0].Host != "K8S-Prod" {
		t.Fatalf("host 匹配失败: %+v", got)
	}
	if got := s.SearchHistory("nonexistent"); len(got) != 0 {
		t.Fatalf("无匹配应为空, got %+v", got)
	}
}

func TestGetSettingsDefaults(t *testing.T) {
	s := New(tempPath(t))
	s.Settings = Settings{} // 直接置零模拟旧配置
	st := s.GetSettings()
	if st.HistoryLimit != DefaultHistoryLimit || st.Scrollback != DefaultScrollback || st.Theme != DefaultTheme {
		t.Fatalf("默认值未补齐: %+v", st)
	}

	// 返回的是副本。
	st.Theme = "hacked"
	if again := s.GetSettings(); again.Theme != DefaultTheme {
		t.Fatal("GetSettings 返回的不是副本")
	}

	s.UpdateSettings(func(p *Settings) { p.Theme = "light" })
	if s.GetSettings().Theme != "light" {
		t.Fatal("UpdateSettings 未生效")
	}
	s.UpdateSettings(nil) // 不应 panic
}

func TestConcurrentAccess(t *testing.T) {
	p := tempPath(t)
	s := New(p)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := s.AddConnection(Connection{Name: "host", Host: "h"})
			s.AddFavorite(FavoriteCmd{Name: "cmd", Cmd: "ls"})
			s.AddHistory("uptime", "h")
			s.UpdateConnection(c)
			s.TouchConnection(c.ID)
			_ = s.GetConnections()
			_ = s.GetFavorites()
			_ = s.GetHistory()
			_ = s.SearchHistory("up")
			_ = s.GetSettings()
			s.UpdateSettings(func(st *Settings) { st.Scrollback = i })
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if err := s.Save(); err != nil {
				t.Errorf("并发 Save: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if n := len(s.GetConnections()); n != 8 {
		t.Fatalf("应有 8 条连接, got %d", n)
	}
	if _, err := LoadFrom(p); err != nil {
		t.Fatalf("并发写后的文件应可正常解析: %v", err)
	}
}

func TestSaveCreatesDirectoryAndKeepsJSONShape(t *testing.T) {
	p := tempPath(t) // 目录尚不存在
	s := New(p)
	s.AddConnection(Connection{Name: "x", Host: "h"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save 应自动创建目录: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Fatal("配置文件不应是目录")
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("保存的内容不是合法 JSON: %v", err)
	}
	for _, key := range []string{"connections", "favorites", "history", "settings"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("缺少字段 %q", key)
		}
	}
}
