//go:build windows

package ui

import (
	"os/exec"
	"syscall"
)

// hidePickerWindow 抑制 PowerShell 子进程的黑色控制台窗口闪现：
// HideWindow 走 STARTF_USESHOWWINDOW(SW_HIDE)，CREATE_NO_WINDOW(0x08000000) 彻底不分配新控制台。
func hidePickerWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
