# sshtool 密钥标签索引

- 关联版本：v0.4 扩展批次（推荐扩展 P1-5）
- 日期：2026-09-20
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

密钥一多就难以分辨「这把是干什么的」。本功能给本机密钥打**标签**（`#prod` / `#ci` / `#readonly` 等）与**备注**，
并在推送公钥时**按标签自动预选**目标主机，打通「把 `#prod` 的密钥推给所有 `prod` 分组主机」。

## 2. 设计要点

### 2.1 标签语法（`internal/keytool/tags.go`）
标签写在公钥注释里，形如 `deploy@CI #prod #ci #readonly`：
- `ParseTags(comment)`：从注释提取标签，支持空格分隔、`#prod#ci` 连写、逗号分隔；统一小写、去重、保序。
- `BaseComment(comment)`：去除标签后的注释主体。
- `FormatComment(base, tags)`：重新拼成规范形式（`base #t1 #t2`）。
- `MatchTags(have, want, matchAll)`：与（全部满足）/ 或（任一满足）匹配；`want` 为空视为匹配。

### 2.2 持久化索引（`internal/keytool/tagindex.go`）
- 存储于 `~/.sshtool/key-tags.json`，**按指纹索引**——与文件路径解耦，改名/移动不影响标签。
- `KeyTag{fingerprint, path, tags[], notes, updated_at}`；原子写入（临时文件 + rename）。
- API：`LoadTagIndex / Save / Get / TagsOf / Set / Remove / Prune`。
- `Prune(keys)` 可清理索引中已不存在的密钥指纹。

> 为什么用独立索引而不是只写在注释里？注释会被 `ssh-keygen -c` 等工具重写、也随文件移动而丢；
> 独立索引与公钥文件解耦、支持备注，且 `ParseTags` 仍能读取注释中的标签作为兼容来源。

### 2.3 UI（`internal/ui/keytags.go` + `keymgr.go`）
- `Ctrl+G` 密钥管理新增「④ 标签与备注」：选密钥 → 表单填标签（空格分隔、支持 `#`）与备注 → 存入索引。
- 「③ 查看本机公钥」增列每把密钥的标签。
- 推送公钥（②）的目标选择器：
  - 每台主机的**分组**以 `#分组` 展示，可用过滤框按分组/标签快速筛选；
  - 标题显示当前密钥的标签；
  - **按密钥标签预勾选**分组命中（`connsMatchingTags`）的目标，用户可直接确认或调整。

> 连接侧：`store.Connection` 是**冻结契约**，不能新增 `tags` 字段，故复用已有的 `Group` 作为连接的「标签」维度。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| K1 | 标签语法（解析 / 格式化 / 匹配） | `internal/keytool/tags.go` |
| K2 | 持久化标签索引（原子写） | `internal/keytool/tagindex.go` |
| K3 | UI：标签编辑 / 公钥列表展示 / 推送预勾选 | `internal/ui/keytags.go`、`keymgr.go` |
| K4 | 帮助与文档 | `internal/ui/dialogs.go`、`README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 注释标签解析（空格/连写/逗号/去重/大小写归一） | `keytool/tags_test.go::TestParseTags` 通过 |
| T2 | 注释主体与标签重排 | `TestBaseCommentAndFormat` 通过 |
| T3 | 与 / 或 / 空前缀匹配语义 | `TestMatchTags` 通过 |
| T4 | 索引加载/保存/更新/删除/备注/路径 | `TestTagIndexLoadSaveAndSet` 通过 |
| T5 | `Prune` 清理失效指纹 | `TestTagIndexPrune` 通过 |
| T6 | 编辑标签表单 → 持久化标签与备注 | `ui/keytags_test.go::TestOpenEditKeyTagsSaves` 通过 |
| T7 | 分组命中标签的连接下标计算 | `TestConnsMatchingTags` 通过 |
| T8 | 推送目标按密钥标签预勾选（prod 命中、lab 不命中） | `TestPushTargetsPrecheckedByKeyTag` 通过 |
| T9 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 标签索引为**独立文件**，不随 `config.json` 备份——后续「密钥备份/恢复」应一并打包。
- 连接的标签维度复用 `Group`（一个连接只有一个分组）；如需多标签需解除 `Connection` 冻结契约。
- 组合查询（`prod +ci`）可在过滤框里用关键字逐步缩小，暂未实现语法化表达式。
