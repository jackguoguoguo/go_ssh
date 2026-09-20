package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultSSHConfigPath 返回 ~/.ssh/config；取不到主目录时退化为相对路径。
func defaultSSHConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ssh", "config")
	}
	return filepath.Join(".ssh", "config")
}

// defaultExportPath 返回默认导出路径 ~/.ssh/config.sshtool。
func defaultExportPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ssh", "config.sshtool")
	}
	return "config.sshtool"
}

// runMetaCommand 处理命令行里的元命令（无需已连接会话）；返回是否已处理。
// 支持：theme <name> / import [ssh] [path] / export [ssh] [path] / shell
// / forward L|R <监听> <目标> / forward stop <id> / forwards。
func (m *Model) runMetaCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "theme":
		if len(fields) < 2 {
			m.setMsg("用法：theme <名称>（可选：" + strings.Join(themeNames(), " / ") + "）")
			return true
		}
		m.applyThemeByName(fields[1])
		return true
	case "import":
		m.handleImportSSHConfig(metaArg(fields[1:]))
		return true
	case "export":
		m.handleExportSSHConfig(metaArg(fields[1:]))
		return true
	case "shell":
		_ = m.openLocalShell()
		return true
	case "forward", "forwards":
		m.handleForward(fields[1:])
		return true
	}
	return false
}

// metaArg 兼容 `import ssh /path` 与 `import /path` 两种写法。
func metaArg(rest []string) string {
	if len(rest) == 0 {
		return ""
	}
	if strings.EqualFold(rest[0], "ssh") {
		return strings.Join(rest[1:], " ")
	}
	return strings.Join(rest, " ")
}

// handleImportSSHConfig 从 ssh_config 导入连接。path 为空时使用 ~/.ssh/config。
func (m *Model) handleImportSSHConfig(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultSSHConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.setMsg("读取失败：" + err.Error())
		return
	}
	added, skipped := m.st.ImportSSHConfig(data)
	if added > 0 {
		if err := m.st.Save(); err != nil {
			m.setMsg("保存失败：" + err.Error())
			return
		}
		m.refresh()
	}
	m.setMsg(fmt.Sprintf("已从 %s 导入 %d 条连接（跳过重复 %d 条）", path, added, skipped))
}

// handleExportSSHConfig 把当前连接导出为 ssh_config。path 为空时写入 ~/.ssh/config.sshtool。
// 只会写到独立文件，绝不覆盖真实的 ~/.ssh/config。
func (m *Model) handleExportSSHConfig(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultExportPath()
	}
	if filepath.Clean(path) == filepath.Clean(defaultSSHConfigPath()) {
		m.setMsg("为避免覆盖真实 ~/.ssh/config，请指定其它导出路径")
		return
	}
	text := m.st.ExportSSHConfig()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			m.setMsg("创建目录失败：" + err.Error())
			return
		}
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		m.setMsg("写入失败：" + err.Error())
		return
	}
	m.setMsg("已导出连接（ssh_config 格式）到 " + path)
}
