package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "config.json"))

	// 文件不存在时加载返回零值，不报错
	snap, err := st.LoadSessions()
	if err != nil {
		t.Fatalf("空快照加载失败：%v", err)
	}
	if len(snap.OpenIDs) != 0 {
		t.Fatalf("空快照应为空，实际 %v", snap.OpenIDs)
	}

	in := SessionSnapshot{OpenIDs: []string{"a", "b"}, ActiveID: "b"}
	if err := st.SaveSessions(in); err != nil {
		t.Fatalf("保存快照失败：%v", err)
	}

	// 落在与配置同目录
	if _, err := os.Stat(filepath.Join(dir, SessionsName)); err != nil {
		t.Fatalf("快照文件未生成：%v", err)
	}

	out, err := st.LoadSessions()
	if err != nil {
		t.Fatalf("加载快照失败：%v", err)
	}
	if len(out.OpenIDs) != 2 || out.OpenIDs[0] != "a" || out.OpenIDs[1] != "b" {
		t.Fatalf("OpenIDs 回读错误：%v", out.OpenIDs)
	}
	if out.ActiveID != "b" {
		t.Fatalf("ActiveID 回读错误：%q", out.ActiveID)
	}
	if out.SavedAt.IsZero() {
		t.Fatal("SavedAt 应被填充")
	}
}