package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/store"
	"sshtool/internal/testutil"
)

// newTestStore 建一个含两台测试服务器的 store（web-01 正常凭据；db-01 错误密码）。
func newTestStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	s1 := testutil.StartSSHServer(t)
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{Name: "web-01", Group: "prod", Host: s1.Host, Port: s1.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"})
	return st, func() {}
}

func TestExecAllSuccess(t *testing.T) {
	st, _ := newTestStore(t)
	var out, errb bytes.Buffer
	code := Exec(st, []string{"echo", "hello-cli"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（stderr=%q）", code, errb.String())
	}
	if !strings.Contains(out.String(), "web-01") || !strings.Contains(out.String(), "hello-cli") {
		t.Fatalf("输出应含主机名与命令输出：\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 成功 · 0 失败") {
		t.Fatalf("汇总应正确：\n%s", out.String())
	}
}

func TestExecGroupFilterAndFail(t *testing.T) {
	s := testutil.StartSSHServer(t)
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{Name: "web-01", Group: "prod", Host: s.Host, Port: s.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"})
	st.AddConnection(store.Connection{Name: "db-01", Group: "db", Host: s.Host, Port: s.Port, User: "test", AuthType: store.AuthPassword, Password: "wrong"})

	// -g db 只命中 db-01（错误密码 → 失败退出码 1）
	var out, errb bytes.Buffer
	code := Exec(st, []string{"-g", "db", "echo", "x"}, &out, &errb)
	if code != ExitFail {
		t.Fatalf("认证失败应返回 1，实际 %d", code)
	}
	if strings.Contains(out.String(), "web-01") {
		t.Fatalf("-g 过滤后不应包含 web-01：\n%s", out.String())
	}
	if !strings.Contains(out.String(), "✗ db-01") {
		t.Fatalf("应标注失败主机：\n%s", out.String())
	}

	// -g prod 只命中 web-01 → 成功
	out.Reset()
	code = Exec(st, []string{"-g", "prod", "echo", "ok"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("prod 组应全部成功，退出码 %d", code)
	}
	if strings.Contains(out.String(), "db-01") {
		t.Fatalf("prod 组不应包含 db-01：\n%s", out.String())
	}
}

func TestExecJSONFormat(t *testing.T) {
	st, _ := newTestStore(t)
	var out, errb bytes.Buffer
	code := Exec(st, []string{"-f", "json", "echo", "j"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if !strings.Contains(out.String(), `"name"`) || !strings.Contains(out.String(), `"stdout"`) {
		t.Fatalf("JSON 输出应含小写字段名：\n%s", out.String())
	}
	// 安全：JSON 输出绝不能携带连接凭据。
	if strings.Contains(out.String(), `"pass"`) || strings.Contains(out.String(), `"password"`) {
		t.Fatalf("JSON 输出泄露了凭据：\n%s", out.String())
	}
}

func TestExecPlainOnlyStdout(t *testing.T) {
	st, _ := newTestStore(t)
	var out, errb bytes.Buffer
	code := Exec(st, []string{"-f", "plain", "echo", "plain-out"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if strings.Contains(out.String(), "✓") || strings.Contains(out.String(), "汇总") {
		t.Fatalf("plain 模式不应含修饰符号：\n%s", out.String())
	}
	if !strings.Contains(out.String(), "plain-out") {
		t.Fatalf("plain 模式应输出命令结果：\n%s", out.String())
	}
}

func TestExecUsageErrors(t *testing.T) {
	st, _ := newTestStore(t)
	var out, errb bytes.Buffer

	if code := Exec(st, nil, &out, &errb); code != ExitUsage {
		t.Fatalf("无命令应返回 2，实际 %d", code)
	}
	if code := Exec(st, []string{"-g", "no-such", "echo", "x"}, &out, &errb); code != ExitFail {
		t.Fatalf("无匹配目标应返回 1，实际 %d", code)
	}
}

func TestPingTextAndExitCode(t *testing.T) {
	s := testutil.StartSSHServer(t)
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	st.AddConnection(store.Connection{Name: "web-01", Group: "prod", Host: s.Host, Port: s.Port, User: "test", AuthType: store.AuthPassword, Password: "pass"})
	st.AddConnection(store.Connection{Name: "bad-01", Group: "prod", Host: s.Host, Port: s.Port, User: "test", AuthType: store.AuthPassword, Password: "wrong"})
	st.AddConnection(store.Connection{Name: "down-01", Group: "prod", Host: "127.0.0.1", Port: 1, User: "test", AuthType: store.AuthPassword, Password: "pass"})

	var out, errb bytes.Buffer
	code := Ping(st, []string{"-g", "prod"}, &out, &errb)
	if code != ExitFail {
		t.Fatalf("存在异常主机应返回 1，实际 %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "可达") || !strings.Contains(text, "认证失败") || !strings.Contains(text, "不可达") {
		t.Fatalf("巡检输出应区分状态：\n%s", text)
	}
	if !strings.Contains(text, "1 可达 · 2 异常") {
		t.Fatalf("汇总应正确：\n%s", text)
	}
}

func TestPingAllReachable(t *testing.T) {
	st, _ := newTestStore(t)
	var out, errb bytes.Buffer
	code := Ping(st, nil, &out, &errb)
	if code != ExitOK {
		t.Fatalf("全部可达应返回 0，实际 %d（%s）", code, out.String())
	}
	if !strings.Contains(out.String(), "1 可达 · 0 异常") {
		t.Fatalf("汇总应正确：\n%s", out.String())
	}
}

func TestPingNoTargets(t *testing.T) {
	st := store.New(filepath.Join(t.TempDir(), "config.json"))
	var out, errb bytes.Buffer
	if code := Ping(st, nil, &out, &errb); code != ExitFail {
		t.Fatalf("无连接应返回 1，实际 %d", code)
	}
}
