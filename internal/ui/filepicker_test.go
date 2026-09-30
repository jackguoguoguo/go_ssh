package ui

import (
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeLook 构造一个只在 names 里查找成功的 exec.LookPath 替身。
func fakeLook(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		for _, want := range names {
			if n == want {
				return "/usr/bin/" + n, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

// TestFilePickerCommandByOS 验证各系统的文件选择命令构造：选对程序、带上关键参数、
// 无可用选择器的系统返回 false（调用方回退手动输入）。
func TestFilePickerCommandByOS(t *testing.T) {
	cmd, ok := filePickerCommand("windows", fakeLook())
	if !ok || !strings.Contains(cmd.Path, "powershell") {
		t.Fatalf("windows 应构造 powershell 命令：ok=%v cmd=%v", ok, cmd)
	}
	if !containsArg(cmd.Args, "-STA") {
		t.Fatal("WinForms 对话框需要 -STA，缺少该参数")
	}
	if !containsArg(cmd.Args, "-NonInteractive") {
		t.Fatal("应带 -NonInteractive 防止挂起等输入")
	}

	cmd, ok = filePickerCommand("darwin", fakeLook())
	if !ok || !strings.Contains(cmd.Path, "osascript") {
		t.Fatalf("darwin 应构造 osascript 命令：ok=%v cmd=%v", ok, cmd)
	}

	cmd, ok = filePickerCommand("linux", fakeLook("zenity"))
	if !ok || !strings.Contains(cmd.Path, "zenity") {
		t.Fatalf("linux 装有 zenity 时应优先 zenity：ok=%v cmd=%v", ok, cmd)
	}

	cmd, ok = filePickerCommand("linux", fakeLook("kdialog"))
	if !ok || !strings.Contains(cmd.Path, "kdialog") {
		t.Fatalf("linux 只有 kdialog 时应回退 kdialog：ok=%v cmd=%v", ok, cmd)
	}

	if c, ok := filePickerCommand("linux", fakeLook()); ok || c != nil {
		t.Fatal("linux 无 zenity/kdialog 应返回 (nil, false)")
	}
	if c, ok := filePickerCommand("plan9", fakeLook("zenity")); ok || c != nil {
		t.Fatal("未知系统应返回 (nil, false)")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestParsePickerOutput 验证选中路径解析：去空白与引号、取首个非空行、空输出得空串。
func TestParsePickerOutput(t *testing.T) {
	cases := []struct{ in, want string }{
		{" /home/u/a.txt\n", "/home/u/a.txt"},
		{"\"C:\\x y.txt\"\n", `C:\x y.txt`},
		{"\n\n  \n", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := parsePickerOutput(c.in); got != c.want {
			t.Fatalf("parsePickerOutput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPickerCanceled 验证取消分类：空 stderr（zenity/kdialog）与 osascript 的
// "User canceled. (-128)" 都算取消；真实报错不算。
func TestPickerCanceled(t *testing.T) {
	if !pickerCanceled("") {
		t.Fatal("空 stderr 应视为用户取消")
	}
	if !pickerCanceled("execution error: User canceled. (-128)") {
		t.Fatal("osascript 取消应视为用户取消")
	}
	if pickerCanceled("Gtk-WARNING **: cannot open display") {
		t.Fatal("无图形环境是真实失败，不应视为取消")
	}
}

// TestUploadCtrlFOpensPicker 验证：上传态 Ctrl+F 派生文件选择器命令并进入「打开中」反馈态；
// 打开期间按键全部吞掉（连按不重入、Esc 不退出）；结果到达后状态复位。
// 注意不用 Ctrl+P —— 该键已留给命令行发送命令。
func TestUploadCtrlFOpensPicker(t *testing.T) {
	m, d := newFsTestModel(t)
	m.fileKey(d, fsTestKey('U'))
	if d.fsOp != "upload" {
		t.Fatalf("应进入上传态：%q", d.fsOp)
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.afterCmd == nil {
		t.Fatal("上传态 Ctrl+F 应派生文件选择器命令")
	}
	if !d.fsPicking || d.fsInputLabel != "正在打开系统文件选择器..." {
		t.Fatalf("应进入打开中反馈态：picking=%v label=%q", d.fsPicking, d.fsInputLabel)
	}
	m.takeAfterCmd()

	// 打开期间：连按 Ctrl+F 不重复派生
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.afterCmd != nil {
		t.Fatal("选择器打开期间不应重复派生命令")
	}
	// Esc 被吞掉，不退出输入态
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEsc})
	if d.fsOp != "upload" {
		t.Fatalf("选择器打开期间 Esc 不应退出输入态：%q", d.fsOp)
	}

	// 结果到达：状态复位、提示恢复、路径填入
	m.Update(filePickedMsg{path: "/home/u/a.txt"})
	if d.fsPicking {
		t.Fatal("结果到达后应退出打开中状态")
	}
	if d.fsInputLabel != fsUploadLabel {
		t.Fatalf("提示文案应恢复：%q", d.fsInputLabel)
	}
	if d.fsInput != "/home/u/a.txt" || d.fsOp != "upload" {
		t.Fatalf("路径应填入且保持输入态：input=%q op=%q", d.fsInput, d.fsOp)
	}

	// 其它操作（重命名）不应响应 Ctrl+F
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEsc})
	d.fsCursor = 0
	m.fileKey(d, fsTestKey('r'))
	if d.fsOp != "rename" {
		t.Fatalf("应进入重命名态：%q", d.fsOp)
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.afterCmd != nil {
		t.Fatal("重命名态 Ctrl+F 不应派生命令")
	}
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyEsc})

	// 下载态不应响应
	m.fileKey(d, fsTestKey('d'))
	m.fileKey(d, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.afterCmd != nil {
		t.Fatal("下载态 Ctrl+F 不应派生命令")
	}
}

// TestFilePickedMsgFillsUploadInput 验证选择结果处理：选中路径填进上传输入框；
// 取消静默；出错提示但不打断输入态；失败路径同样复位 fsPicking。
func TestFilePickedMsgFillsUploadInput(t *testing.T) {
	m, d := newFsTestModel(t)
	m.fileKey(d, fsTestKey('U'))

	m.Update(filePickedMsg{path: "/home/u/a.txt"})
	if d.fsInput != "/home/u/a.txt" {
		t.Fatalf("选中路径应填入输入框：%q", d.fsInput)
	}
	if d.fsOp != "upload" {
		t.Fatal("填充路径后应仍在输入态，等用户回车确认")
	}

	m.Update(filePickedMsg{}) // 用户取消
	if d.fsInput != "/home/u/a.txt" || d.fsOp != "upload" {
		t.Fatal("取消不应改动输入框或退出输入态")
	}

	d.fsPicking = true
	d.fsInputLabel = "正在打开系统文件选择器..."
	m.Update(filePickedMsg{err: errNoFilePicker})
	if d.fsOp != "upload" {
		t.Fatal("选择器失败应保留输入态，回退手动输入")
	}
	if d.fsPicking || d.fsInputLabel != fsUploadLabel {
		t.Fatalf("失败后应复位打开中状态：picking=%v label=%q", d.fsPicking, d.fsInputLabel)
	}
}
