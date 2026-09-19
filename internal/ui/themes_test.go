package ui

import (
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"sshtool/internal/remotessh"
	"sshtool/internal/store"
)

func TestApplyThemeSwitchesPalette(t *testing.T) {
	applyTheme("light")
	if CurrentTheme() != "light" {
		t.Fatalf("当前主题应为 light，得到 %q", CurrentTheme())
	}
	if cFg != lipgloss.Color("#343b5c") {
		t.Fatalf("light 主题前景色未生效：%v", cFg)
	}
	if cBorder != lipgloss.Color("#b8b9c5") {
		t.Fatalf("light 主题边框色未生效：%v", cBorder)
	}
	// 共享样式应随主题重建
	if styleBase.GetForeground() != lipgloss.Color("#343b5c") {
		t.Fatal("styleBase 未随主题重建")
	}
}

func TestApplyThemeUnknownFallsBack(t *testing.T) {
	applyTheme("does-not-exist")
	if CurrentTheme() != "dark" {
		t.Fatalf("未知主题应回退到 dark，得到 %q", CurrentTheme())
	}
}

func TestThemeNames(t *testing.T) {
	names := themeNames()
	if len(names) < 2 {
		t.Fatalf("至少应内置 dark/light 两套主题，得到 %v", names)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	if !seen["dark"] || !seen["light"] {
		t.Fatalf("主题列表应含 dark 与 light：%v", names)
	}
}

func TestApplyThemeByNamePersists(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "cfg.json"))
	mgr := remotessh.NewManager()
	defer mgr.CloseAll()
	m := New(st, mgr)

	m.applyThemeByName("light")
	if CurrentTheme() != "light" {
		t.Fatalf("切换后当前主题应为 light，得到 %q", CurrentTheme())
	}
	if st.GetSettings().Theme != "light" {
		t.Fatalf("主题应持久化到设置，得到 %q", st.GetSettings().Theme)
	}

	// 未知主题不切换，保持当前主题（不应降级）
	m.applyThemeByName("does-not-exist")
	if CurrentTheme() != "light" {
		t.Fatalf("未知主题应保持当前主题 light，得到 %q", CurrentTheme())
	}
}
