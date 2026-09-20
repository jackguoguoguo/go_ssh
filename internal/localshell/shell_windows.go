//go:build windows
// +build windows

package localshell

import (
	"context"
	"os"

	"github.com/UserExistsError/conpty"
)

// openPTY 用 Windows ConPTY 启动本地 shell，返回设备、等待函数与错误。
// 系统不支持 ConPTY（Windows 10 1809 以前）时返回 conpty.ErrConPtyUnsupported。
func openPTY(cols, rows int) (ptyDevice, func() error, error) {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	if !conpty.IsConPtyAvailable() {
		return nil, nil, conpty.ErrConPtyUnsupported
	}

	cpty, err := conpty.Start(shell,
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyEnv(append(os.Environ(), "TERM=xterm-256color")),
	)
	if err != nil {
		return nil, nil, err
	}
	wait := func() error {
		_, err := cpty.Wait(context.Background())
		return err
	}
	return cpty, wait, nil
}
