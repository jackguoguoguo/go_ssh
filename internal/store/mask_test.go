package store

import "testing"

func TestMaskSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"password= eq", "mysql -u root password=secret123", "mysql -u root password=***"},
		{"passwd= eq", "useradd admin passwd=hunter2", "useradd admin passwd=***"},
		{"token= eq", "export TOKEN=abc.def.ghi", "export TOKEN=***"},
		{"aws secret key", "aws configure set aws_secret_access_key=AKIA123", "aws configure set aws_secret_access_key=***"},
		{"api_key quoted", `curl -d 'api_key="sk_live_xxx"'`, `curl -d 'api_key=***'`},
		{"long flag", "tool --password s3cr3t", "tool --password ***"},
		{"long flag token", "cli --token bearer.xxx", "cli --token ***"},
		{"bearer", "curl -H 'Authorization: Bearer eyJhbGciOi'", "curl -H 'Authorization: Bearer ***'"},
		{"basic", "curl -u user:pass --basic Basic dXNlcg==", "curl -u user:pass --basic Basic ***"},
		{"ssh port untouched", "ssh -p 2222 host", "ssh -p 2222 host"},
		{"pwd command untouched", "pwd && ls", "pwd && ls"},
		{"passwd command untouched", "passwd alice", "passwd alice"},
		{"no secret", "uptime", "uptime"},
		{"secret in value only", "echo password=topsecret", "echo password=***"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MaskSecrets(c.in); got != c.want {
				t.Fatalf("MaskSecrets(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMaskSecretsDisabledByEnv(t *testing.T) {
	t.Setenv("SSHTOOL_NO_HISTORY_MASK", "1")
	if got := MaskSecrets("password=secret"); got != "password=secret" {
		t.Fatalf("关闭脱敏时应为原值，得到 %q", got)
	}
}

func TestAddHistoryMasksCommand(t *testing.T) {
	s := New(t.TempDir() + "/cfg.json")
	e := s.AddHistory("mysql -uroot -ppassword=secret123", "10.0.0.5")
	if e.Cmd != "mysql -uroot -ppassword=***" {
		t.Fatalf("入库命令应脱敏，得到 %q", e.Cmd)
	}
	// 再次以不同口令写入应去重为同一条（cmd 相同），证明按脱敏后比较。
	e2 := s.AddHistory("mysql -uroot -ppassword=DIFFERENT", "10.0.0.5")
	if e2.ID != e.ID {
		t.Fatal("脱敏后命令相同应视为重复历史")
	}
	// 未脱敏的正常命令不受影响
	e3 := s.AddHistory("ls -la /var/log", "10.0.0.5")
	if e3.Cmd != "ls -la /var/log" {
		t.Fatalf("普通命令不应被改动，得到 %q", e3.Cmd)
	}
}
