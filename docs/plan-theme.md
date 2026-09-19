# sshtool 主题切换

- 关联版本：v0.3 可靠性批次补充（路线图第 6 项）
- 日期：2026-09-19
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

`Settings.Theme` 字段早已存在（默认 `"dark"`），但 UI 配色是硬编码的包级变量，该字段从未被读取，
等于「占着位置不干活」。本功能让它真正生效：提供 `dark` / `light` 两套配色，可由配置文件切换，
也可在终端内即时切换并落盘。

## 2. 设计要点

- 配色收敛到一套 `palette` 结构体（背景 / 面板 / 前景 / 弱化 / 主强调 / 次强调 / 成功 / 警告 / 错误 /
  反色文字 / 边框），由 `applyTheme(name)` 一次性赋值给全局颜色变量（`cBg`/`cPanel`/...）并重建共享样式
  （`styleBase`/`styleTitle`/...）。`boxStyle` 与对话框的硬编码 `#1f2335`/`#292e42` 也改为引用主题变量，
  保证切换后整体一致。
- 主题为**进程级全局状态**：本工具是单实例 TUI，所有渲染统一读取这些全局变量，无需在 `Model` 上挂字段。
- 接入点：
  - 启动时 `ui.New` 按 `st.GetSettings().Theme` 调用 `applyTheme`（未知值回退 `dark`）。
  - 命令行模式（Ctrl+X）支持元命令 `theme <name>`：即时切换、写回 `Settings.Theme` 并 `Save`；
    未知名称仅提示可用主题、保持当前主题不降级。
- 受 `Settings` 冻结契约约束，未新增任何配置字段，复用既有 `Theme` 字段。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| T1 | `palette` / `themes` 表 + `applyTheme` / `CurrentTheme` / `themeNames` | `internal/ui/themes.go` |
| T2 | 颜色变量与共享样式改为由 `applyTheme` 驱动；`boxStyle`、对话框接入主题变量 | `internal/ui/theme.go`、`internal/ui/dialogs.go` |
| T3 | `New` 按设置应用主题 | `internal/ui/model.go` |
| T4 | 命令行 `theme <name>` 元命令 | `internal/ui/actions.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | `applyTheme("light")` 改变全局配色与 `CurrentTheme`；共享样式随主题重建 | `themes_test.go::TestApplyThemeSwitchesPalette` 通过 |
| T2 | 未知主题名回退 `dark` | `TestApplyThemeUnknownFallsBack` 通过 |
| T3 | `themeNames` 至少含 `dark`/`light` | `TestThemeNames` 通过 |
| T4 | `applyThemeByName("light")` 生效并持久化到设置；未知名保持当前主题 | `TestApplyThemeByNamePersists` 通过 |
| T5 | `go build`/`go vet` 干净；UI 全量回归 PASS | 通过 |

## 5. 已知限制
- 仅内置 `dark` / `light` 两套；如需更多主题，往 `themes` 表加一项即可（无需改其它代码）。
- 主题为单实例全局状态，多窗口并发场景不适用（本工具无此形态）。
- 切换主题不会重绘已存在的对话框模板（对话框在打开时按当时主题渲染，重新打开即更新）。
