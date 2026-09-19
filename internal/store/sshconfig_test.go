package store

import (
	"path/filepath"
	"testing"
)

const sampleSSHConfig = `
# 全局
Host *
    ServerAliveInterval 30

Host web prod
    HostName 10.0.0.1
    User deploy
    Port 2222

Host db
    HostName db.internal
    User root
    IdentityFile ~/.ssh/id_ed25519
`

func TestParseSSHConfig(t *testing.T) {
	conns := ParseSSHConfig([]byte(sampleSSHConfig))
	// 期望：web、prod、db 三条（Host * 被跳过）
	if len(conns) != 3 {
		t.Fatalf("应解析出 3 条，得到 %d：%+v", len(conns), conns)
	}
	byName := map[string]Connection{}
	for _, c := range conns {
		byName[c.Name] = c
	}
	if byName["web"].Host != "10.0.0.1" || byName["web"].User != "deploy" || byName["web"].Port != 2222 {
		t.Fatalf("web 解析错误：%+v", byName["web"])
	}
	if byName["web"].AuthType != AuthPassword || !byName["web"].AskPassword {
		t.Fatalf("web 应为密码认证且连接时询问：%+v", byName["web"])
	}
	if byName["prod"].Host != "10.0.0.1" || byName["prod"].Port != 2222 {
		t.Fatalf("prod 应与 web 共享设置：%+v", byName["prod"])
	}
	if byName["db"].AuthType != AuthKey || byName["db"].KeyPath == "" {
		t.Fatalf("db 应为私钥认证：%+v", byName["db"])
	}
	if byName["db"].Group != SSHConfigGroup {
		t.Fatalf("导入条目应归入 %s，得到 %q", SSHConfigGroup, byName["db"].Group)
	}
}

func TestImportSSHConfigDedup(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "cfg.json"))
	added, skipped := s.ImportSSHConfig([]byte(sampleSSHConfig))
	if added != 3 || skipped != 0 {
		t.Fatalf("首次导入应为 3/0，得到 %d/%d", added, skipped)
	}
	added2, skipped2 := s.ImportSSHConfig([]byte(sampleSSHConfig))
	if added2 != 0 || skipped2 != 3 {
		t.Fatalf("重复导入应为 0/3，得到 %d/%d", added2, skipped2)
	}
}

func TestExportSSHConfigRoundTrip(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "cfg.json"))
	s.AddConnection(Connection{Name: "web", Host: "10.0.0.1", User: "deploy", Port: 2222, AuthType: AuthPassword})
	s.AddConnection(Connection{Name: "db", Host: "db.internal", User: "root", Port: 22, AuthType: AuthKey, KeyPath: "/home/u/.ssh/id_ed25519"})

	text := s.ExportSSHConfig()
	reparsed := ParseSSHConfig([]byte(text))
	if len(reparsed) != 2 {
		t.Fatalf("导出后应能重新解析出 2 条，得到 %d\n%s", len(reparsed), text)
	}
	byName := map[string]Connection{}
	for _, c := range reparsed {
		byName[c.Name] = c
	}
	if byName["web"].Host != "10.0.0.1" || byName["web"].Port != 2222 || byName["web"].User != "deploy" {
		t.Fatalf("web 往返不一致：%+v", byName["web"])
	}
	if byName["db"].AuthType != AuthKey || byName["db"].KeyPath != "/home/u/.ssh/id_ed25519" {
		t.Fatalf("db 往返不一致：%+v", byName["db"])
	}
}
