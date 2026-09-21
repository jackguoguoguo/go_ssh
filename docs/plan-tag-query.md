# sshtool 标签组合查询

- 关联版本：v0.4（README「基于标签的密钥分类查询」② 的剩余部分）
- 日期：2026-09-21
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

标签索引落地后，还差「怎么用标签筛」。本功能给密钥选择器加上组合查询：

- `#prod` → 含 prod 标签的密钥
- `prod +ci` → **同时**含 prod 与 ci（与）
- `prod,staging` → 含 prod **或** staging 即可（或）

## 2. 设计要点

### 2.1 查询语法（`internal/keytool/tags.go`）
- `ParseTagQuery(query) [][]string`：先按 `,` 切出「或」组，组内按空白切词（可带 `+` / `#` 前缀）作为「与」条件。
- `MatchTagQuery(have, query) bool`：任一或组内全部命中即匹配；**空查询恒为匹配**（等价于不过滤）。
- 标签比较统一走 `NormalizeTags`（小写、去 `#`），大小写与前缀不敏感。

### 2.2 选择器扩展（`internal/ui/dialogs.go`）
- `dlg` 新增可选钩子 `filterIdxFn func(idx int, query string) bool`。
- `visiblePick` 中：`filterIdxFn` 非空时改用它按下标过滤，否则沿用原有的
  「Label/Desc 子串匹配」——**不影响任何既有选择器**（主机、连接等仍按子串过滤）。
- 之所以按「下标」而非「条目文本」过滤：标签存在索引里、不体现在展示文本中，
  需要拿到下标才能反查对应密钥的标签。

### 2.3 接入点
- `openPickKeyForTags`（④ 编辑标签）：按标签过滤候选密钥。
- `openPushPickKey`（② 推送公钥）：按标签过滤；「手动输入公钥路径」始终可见（兜底入口）。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| Q1 | 查询解析与匹配 | `internal/keytool/tags.go` |
| Q2 | 选择器自定义过滤钩子 | `internal/ui/dialogs.go` |
| Q3 | 两个密钥选择器接入 | `internal/ui/keytags.go`、`keymgr.go` |
| Q4 | 文档 | `README.md` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | 解析组数（空/单/与/或/混合带 `#`+） | `keytool/tagquery_test.go::TestParseTagQuery` 通过 |
| T2 | 与/或/不存在/大小写前缀不敏感 | `TestMatchTagQuery` 通过 |
| T3 | 密钥选择器：空查询全显示、`prod +ci` 仅 1 项、`prod,staging` 2 项、不存在标签 0 项 | `ui/tagquery_test.go::TestKeyPickerTagQueryFilter` 通过 |
| T4 | 未接入钩子的选择器仍按子串过滤（回归保护） | `TestSubstringFilterStillWorks` 通过 |
| T5 | `go build` / `go vet` 干净；全包回归通过 | 通过 |

## 5. 已知限制与后续
- 语法只支持一层「或」内嵌「与」，不支持括号嵌套（当前场景足够）。
- 查询框同时仍是「其他文本过滤」的位置：接入钩子的选择器不再按文件名子串过滤，
  只按标签过滤（因为标签不体现在文本里）。
