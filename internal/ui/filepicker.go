package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// errNoFilePicker 表示当前系统找不到可用的文件选择器（如 Linux 未装 zenity/kdialog）。
var errNoFilePicker = errors.New("当前系统没有可用的文件选择器")

// filePickedMsg 本地文件选择器的返回结果。
// path 为空且 err 为 nil 表示用户取消了选择（静默回到输入态）。
type filePickedMsg struct {
	path string
	err  error
}

// localFilePickerCmd 异步打开当前系统的原生文件选择对话框（不阻塞 TUI）。
func localFilePickerCmd() tea.Cmd {
	return func() tea.Msg {
		p, err := pickLocalFile()
		return filePickedMsg{path: p, err: err}
	}
}

// pickLocalFile 按操作系统调起原生文件选择对话框，返回用户选中的本地路径。
// 用户取消时返回 ("", nil)；没有可用选择器或调起失败时返回带「请直接输入本地路径」提示的错误。
func pickLocalFile() (string, error) {
	cmd, ok := filePickerCommand(runtime.GOOS, exec.LookPath)
	if !ok {
		return "", fmt.Errorf("%w（请直接输入本地路径）", errNoFilePicker)
	}
	hidePickerWindow(cmd)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()

	if p := parsePickerOutput(outBuf.String()); p != "" {
		return p, nil
	}
	if err == nil {
		return "", nil // 正常退出但无输出：用户取消
	}
	if pickerCanceled(errBuf.String()) {
		return "", nil
	}
	if msg := strings.TrimSpace(errBuf.String()); msg != "" {
		return "", fmt.Errorf("无法打开文件选择器：%s（请直接输入本地路径）", firstLine(msg))
	}
	return "", fmt.Errorf("无法打开文件选择器：%w（请直接输入本地路径）", err)
}

// filePickerCommand 构造各系统原生的文件选择命令；该系统没有可用选择器时返回 (nil, false)。
// 约定：命令把选中路径写到 stdout（单行）；用户取消时无输出、退出码非 0。
// lookPath 参数注入可执行文件查找，便于测试。
func filePickerCommand(goos string, lookPath func(string) (string, error)) (*exec.Cmd, bool) {
	const title = "选择要上传的文件"
	switch goos {
	case "windows":
		// WinForms OpenFileDialog：即便宿主是终端（mintty/WT），也能弹出真实 GUI 对话框。
		// 先把输出编码钉成 UTF-8，避免中文路径经 OEM 代码页转出乱码。
		// $ow 是一个从不显示的置顶宿主窗体：ShowDialog($ow) 让对话框归它所有，
		// 确保对话框永远盖在全屏终端之上（否则可能被压在后面，看起来像「没打开」）。
		script := `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms | Out-Null
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$ow = New-Object System.Windows.Forms.Form
$ow.TopMost = $true
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = '` + title + `'
if ($d.ShowDialog($ow) -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName }`
		return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-NoLogo", "-STA", "-Command", script), true
	case "darwin":
		return exec.Command("osascript", "-e", `POSIX path of (choose file with prompt "`+title+`")`), true
	case "linux", "freebsd", "openbsd", "netbsd":
		if p, err := lookPath("zenity"); err == nil {
			return exec.Command(p, "--file-selection", "--title="+title), true
		}
		if p, err := lookPath("kdialog"); err == nil {
			return exec.Command(p, "--getopenfilename", ".", "--title", title), true
		}
		return nil, false
	default:
		return nil, false
	}
}

// pickerCanceled 根据子进程 stderr 判断「用户取消选择」（而非真的打不开）。
//   - zenity/kdialog 取消：退出码 1、无输出、无报错 → stderr 为空；
//   - osascript 取消：stderr 含 "User canceled. (-128)"。
func pickerCanceled(stderr string) bool {
	if strings.TrimSpace(stderr) == "" {
		return true
	}
	s := strings.ToLower(stderr)
	return strings.Contains(s, "user canceled") || strings.Contains(s, "(-128)")
}

// parsePickerOutput 取 stdout 中第一个非空行并去掉首尾引号/空白，作为选中路径。
func parsePickerOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, `"'`)
		if line != "" {
			return line
		}
	}
	return ""
}

// firstLine 取多行文本的第一行，用于把子进程报错压缩成一行提示。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
