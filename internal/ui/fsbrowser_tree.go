package ui

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// fsDownloadTreeCmd 递归下载远端目录到本地目录（保留子目录结构）。
func (m *Model) fsDownloadTreeCmd(d *dlg, remoteDir, localDir string) tea.Cmd {
	return func() tea.Msg {
		files := 0
		// 先确保根目录存在，再按相对路径逐项落盘。
		if err := os.MkdirAll(localDir, 0o755); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("创建本地目录失败：%w", err)}
		}
		err := d.fs.Walk(remoteDir, func(p string, isDir bool) error {
			// 远端路径为 POSIX 形式，用前缀裁剪得到相对路径（path 包无 Rel）。
			rel := strings.TrimPrefix(strings.TrimPrefix(p, remoteDir), "/")
			target := filepath.Join(localDir, filepath.FromSlash(rel))
			if isDir {
				return os.MkdirAll(target, 0o755)
			}
			data, err := d.fs.ReadFile(p)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return err
			}
			files++
			return nil
		})
		if err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("递归下载失败：%w", err)}
		}
		entries, lerr := d.fs.List(d.fsPath)
		return fsLoadedMsg{path: d.fsPath, entries: entries, err: lerr,
			note: fmt.Sprintf("已下载目录（%d 个文件）到 %s", files, localDir)}
	}
}

// fsUploadTreeCmd 递归上传本地目录到远端目录（保留子目录结构）。
func (m *Model) fsUploadTreeCmd(d *dlg, localDir, remoteDir string) tea.Cmd {
	return func() tea.Msg {
		files := 0
		if err := d.fs.Mkdir(remoteDir); err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("创建远端目录失败：%w", err)}
		}
		err := filepath.Walk(localDir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(localDir, p)
			if err != nil {
				return err
			}
			remote := path.Join(remoteDir, filepath.ToSlash(rel))
			if info.IsDir() {
				return d.fs.Mkdir(remote)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err := d.fs.WriteFile(remote, data); err != nil {
				return err
			}
			files++
			return nil
		})
		if err != nil {
			return fsLoadedMsg{path: d.fsPath, err: fmt.Errorf("递归上传失败：%w", err)}
		}
		entries, lerr := d.fs.List(d.fsPath)
		return fsLoadedMsg{path: d.fsPath, entries: entries, err: lerr,
			note: fmt.Sprintf("已上传目录（%d 个文件）到 %s", files, remoteDir)}
	}
}