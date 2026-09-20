//go:build !windows
// +build !windows

package localshell

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// unixPTY 包装 creack/pty 的主设备，补上 Resize。
type unixPTY struct{ f *os.File }

func (u *unixPTY) Read(p []byte) (int, error)  { return u.f.Read(p) }
func (u *unixPTY) Write(p []byte) (int, error) { return u.f.Write(p) }
func (u *unixPTY) Close() error                { return u.f.Close() }

func (u *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(u.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// openPTY 在本机打开 PTY 并启动交互式 shell，返回设备、等待函数与错误。
func openPTY(cols, rows int) (ptyDevice, func() error, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-i")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, nil, err
	}
	return &unixPTY{f: f}, cmd.Wait, nil
}
