package ui

import (
	"strings"
	"testing"
	"time"
)

// TestRisky 用真实运维命令验证危险命令判定：既不误伤普通命令，也不漏报高危写法。
func TestRisky(t *testing.T) {
	cases := []struct {
		cmd  string
		want string // 期望的风险原因（空表示安全，不应拦截）
	}{
		{"", ""},
		{"echo a > f", ""},
		{"cat reboot.log", ""},
		{"ls /", ""},
		{"history", ""},
		{"grep -R pattern /etc", ""},
		{"echo hi | wall", ""},          // wall 不在名单，不应被拦
		{"chmod 600 id_rsa", ""},        // 非递归、非根目录
		{"systemctl restart nginx", ""}, // 仅 stop/disable/mask 被拦
		{"rm -rf /tmp/x", ""},           // 临时目录不是关键目标
		{"rm -rf /", "递归删除根目录或系统目录"},
		{"rm -rf /*", "递归删除根目录或系统目录"},
		{"rm -rf $HOME", "递归删除根目录或系统目录"},
		{"sudo rm -rf /", "递归删除根目录或系统目录"},      // sudo 前缀应被剥掉
		{"env FOO=1 rm -rf /", "递归删除根目录或系统目录"}, // env 前缀应被剥掉
		{"chmod -R 777 /", "递归修改根目录/系统目录的权限或属主"},
		{"chown -R root /", "递归修改根目录/系统目录的权限或属主"},
		{"dd if=/dev/zero of=/dev/sda", "dd 直接写块设备"},
		{"shutdown -h now", "关机或重启主机"},
		{"reboot", "关机或重启主机"},
		{"init 0", "关机或重启主机"},
		{"systemctl stop nginx", "停止 / 禁用系统服务"},
		{"pkill sshd", "按名字批量杀进程"},
		{"iptables -F", "修改防火墙规则"},
		{"mkfs.ext4 /dev/sdb1", "格式化文件系统"},
		{"echo x > /dev/sda", "重定向覆盖块设备"},
		{"echo x > /etc/passwd", "重定向覆盖系统文件"},
	}
	for _, c := range cases {
		got := risky(c.cmd)
		if c.want == "" {
			if got != "" {
				t.Errorf("risky(%q) 不应被拦截，实际判定为：%s", c.cmd, got)
			}
		} else {
			if got == "" {
				t.Errorf("risky(%q) 应被拦截（期望含 %q），却放行了", c.cmd, c.want)
			} else if !strings.Contains(got, c.want) {
				t.Errorf("risky(%q) 原因=%q，期望含 %q", c.cmd, got, c.want)
			}
		}
	}
}

// TestBroadcastDeliversToAll 验证广播模式把命令发往所有已连接会话。
func TestBroadcastDeliversToAll(t *testing.T) {
	m, st, _ := newTestModel(t)
	s1 := connectTestSession(t, m, st)
	s2 := connectTestSession(t, m, st)

	m.activeID = s1.ID
	m.broadcast = true
	m.runCommand("uptime")

	waitTermText(t, s1, "echo:uptime", 5*time.Second)
	waitTermText(t, s2, "echo:uptime", 5*time.Second)

	hist := m.st.GetHistory()
	if len(hist) == 0 {
		t.Fatal("广播命令应写入历史")
	}
	last := hist[len(hist)-1]
	if last.Cmd != "uptime" {
		t.Errorf("历史命令 = %q，期望 uptime", last.Cmd)
	}
	if !strings.Contains(last.Host, "广播") {
		t.Errorf("广播历史 Host 标注应含「广播」，实际 %q", last.Host)
	}
}

// TestBroadcastToggleNeedsSessions 验证无连接时无法进入广播模式。
func TestBroadcastToggleNeedsSessions(t *testing.T) {
	m, st, _ := newTestModel(t)
	_, _, _ = m, st, st // 未连接任何会话
	cmd := m.toggleBroadcast()
	if cmd == nil {
		t.Fatal("无连接时应返回一条提示命令")
	}
	if m.broadcast {
		t.Error("无连接时不应进入广播模式")
	}
}
