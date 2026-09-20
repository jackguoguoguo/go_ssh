package ui

import (
	"path/filepath"
	"testing"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

func TestCachePassphrase(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)

	c := store.Connection{AuthType: store.AuthKey, KeyPath: "/tmp/k"}
	m.cachePassphrase(c, "pw")
	key := remotessh.ResolveKeyPath(c)
	if m.passCache[key] != "pw" {
		t.Fatalf("私钥口令应被缓存：%+v", m.passCache)
	}

	// 密码认证不进入口令缓存
	m.cachePassphrase(store.Connection{AuthType: store.AuthPassword}, "secret")
	if len(m.passCache) != 1 {
		t.Fatalf("密码认证不应缓存：%+v", m.passCache)
	}
	// 空口令不缓存
	m.cachePassphrase(store.Connection{AuthType: store.AuthKey, KeyPath: "/tmp/k2"}, "")
	if len(m.passCache) != 1 {
		t.Fatalf("空口令不应缓存：%+v", m.passCache)
	}
}

// TestConnectReusesCachedPassphrase 验证「每次询问口令」且有缓存时不再弹窗。
func TestConnectReusesCachedPassphrase(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	mgr := remotessh.NewManager()
	t.Cleanup(mgr.CloseAll)
	m := New(st, mgr)
	m.width, m.height = 120, 30

	c := store.Connection{
		Name: "k", Host: "192.0.2.9", User: "root",
		AuthType: store.AuthKey, KeyPath: "/tmp/k", AskPassphrase: true,
	}
	m.cachePassphrase(c, "cached-pw")

	m.connectConn(c)
	if m.dlg != nil {
		t.Fatalf("命中口令缓存时不应弹出口令对话框：%+v", m.dlg)
	}
}
