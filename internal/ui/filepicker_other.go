//go:build !windows

package ui

import "os/exec"

// hidePickerWindow 非 Windows 无控制台窗口问题，无需处理。
func hidePickerWindow(cmd *exec.Cmd) {}
