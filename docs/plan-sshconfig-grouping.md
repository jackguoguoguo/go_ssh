# sshtool ~/.ssh/config 导入导出 + 分组树折叠

- 关联版本：v0.3 可靠性批次补充（路线图第 5 项）
- 日期：2026-09-19
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

连接多了以后，扁平列表难以管理。本功能：
1. 让「保存的连接」面板按 `Connection.Group` **分组显示**，分组头可**折叠/展开**；
2. 打通与 `~/.ssh/config` 的**互操作**：一键导入现有主机，导出为 ssh_config 片段。

`Connection` 早已有 `Group` 字段（冻结契约），此前只是没被用于展示。

## 2. 设计要点

### 2.1 分组显示（引入「显示行」模型）
- 新增 `connRow{group, isGroup, idx}` 与 `m.connRows`：`isGroup` 为分组头行，否则 `idx` 指向 `m.connList`。
- `m.connSel` / `m.connScroll` 的索引单位由 `connList` 改为 `connRows`；`countOf(focusConn)` 随之改为
  `len(connRows)`，因此键盘移动、鼠标点击、滚轮、`ensureVisible` 全部自动适配，无需逐个改。
- 命中连接的位置改为 `selectedConn()`（选中分组头时返回 false），`activate` 对分组头执行**折叠切换**、
  对连接执行连接；`edit/deleteSelectedConn` 同样改用 `selectedConn()`。
- 命名分组按字母序在前（未分组置末）；收起的分组只渲染分组头（带 `▸`/`▾` 与条目数）。
- 鼠标双击分组头即展开/收起（复用 `activate`）。

### 2.2 ssh_config 互操作
- `store` 层新增纯函数 `ParseSSHConfig`：解析 `Host`/`HostName`/`User`/`Port`/`IdentityFile`，
  跳过含通配符的 `Host`，一个 `Host` 多别名展开为多条；无 `HostName` 以别名为主机，无 `Port` 默认 22；
  有 `IdentityFile` 视为私钥认证，否则视为密码认证并在连接时询问（口令不落盘）。
- `Store.ImportSSHConfig`：按**连接名**去重后合并（保留不同别名的语义），返回新增/跳过数。
- `Store.ExportSSHConfig`：渲染为 ssh_config（按分组加注释），供导出。
- UI 以**命令行元命令**暴露（无需已连接会话）：`import [ssh] [path]`、`export [ssh] [path]`；
  默认路径分别为 `~/.ssh/config` 与 `~/.ssh/config.sshtool`。导出到默认真实 `~/.ssh/config` 会被拒绝，
  避免误覆盖。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| G1 | `ParseSSHConfig` / `ImportSSHConfig` / `ExportSSHConfig` | `internal/store/sshconfig.go` |
| G2 | `connRow` / `rebuildConnRows` / `selectedConn` / `toggleGroupAt` | `internal/ui/connrows.go` |
| G3 | `Model` 增加 `connRows`/`collapsed`；`refresh` 重建显示行 | `internal/ui/model.go` |
| G4 | `countOf`/`activate`/`edit/deleteSelectedConn` 改用显示行模型 | `internal/ui/actions.go` |
| G5 | 连接面板渲染分组头 + 缩进连接项 | `internal/ui/panels.go` |
| G6 | 元命令 `import`/`export` 与处理函数 | `internal/ui/sshconfig.go`、`internal/ui/actions.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | ssh_config 解析：多主机、多别名、通配符跳过、私钥/密码判定 | `store/sshconfig_test.go::TestParseSSHConfig` 通过 |
| T2 | 导入按名称去重（3/0 后 0/3） | `TestImportSSHConfigDedup` 通过 |
| T3 | 导出后重新解析往返一致 | `TestExportSSHConfigRoundTrip` 通过 |
| T4 | 分组显示行顺序（命名分组字母序、未分组在末） | `ui/connrows_test.go::TestConnRowsGrouping` 通过 |
| T5 | 折叠/展开分组改变显示行 | `TestConnGroupCollapse` 通过 |
| T6 | 选中分组头不返回连接 | `TestSelectedConnSkipsGroupHeader` 通过 |
| T7 | 元命令 import/export 生效、普通命令不误拦截 | `TestMetaImportExportSSHConfig` 通过 |
| T8 | `go build`/`go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制
- 导入不解析 `Include`、`ProxyJump`、`ProxyCommand`（后者尚未实现）。
- 分组仅一层，不支持嵌套；分组名来自连接编辑对话框的「分组」字段。
- 导入默认按名称去重：与已有同名连接冲突时跳过。
- 导出为独立文件，不自动合并进真实 `~/.ssh/config`（安全考虑）。
