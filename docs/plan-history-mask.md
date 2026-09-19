# sshtool 历史命令脱敏

- 关联版本：v0.3 可靠性批次补充（路线图第 1 项）
- 日期：2026-09-19
- 状态：**已实现并验证（待提交）**

---

## 1. 目标

历史命令面板 / 自动补全会记住用户跑过的命令，但运维命令常带明文口令或令牌
（`mysql -ppassword=...`、`export TOKEN=...`、`curl -H 'Authorization: Bearer ...'`），
明文落盘等于把凭证写进 `~/.sshtool` 的 JSON，存在泄露风险。本功能在**入库前**对命令中的
敏感值做脱敏，既保留命令语义（仍看得出在传密码），又避免凭证明文留存。

## 2. 设计要点

- 脱敏发生在 `Store.AddHistory` 入库前；去重比较也基于脱敏后的命令（不同口令的同一条命令视为重复，
  避免历史被相近命令刷屏）。
- 采用「关键字 / 选项 + 值」的保守模式，只遮蔽**值**，保留键名与命令结构：
  - `key=value`：`password=` / `passwd=` / `token=` / `secret=` / `api_key=` / `access_token=` /
    `aws_secret_access_key=` 等（含大小写、下划线、连字符变体）。
  - 长选项：`--password VALUE` / `--token VALUE` / `--secret VALUE` / `--authorization VALUE` 等。
  - 凭证头：`Bearer <token>` / `Basic <token>`。
- **逃生开关**：因 `Settings` 为冻结契约（见 plan-v0.2 §8.2.1，不得加字段），用环境变量
  `SSHTOOL_NO_HISTORY_MASK=1` 整体关闭脱敏，仅用于无法脱离敏感参数的特殊场景。
- **不误伤**：`ssh -p 22`（短选项 `-p` 是端口而非密码）、`pwd`、`passwd` 命令等保持原样；
  认证方案词（Bearer/Basic）按大写匹配，避免 `--basic` 之类标志被误当 scheme。

## 3. 落点

| 编号 | 功能 | 主要文件 |
|---|---|---|
| M1 | `MaskSecrets` 正则脱敏 + 环境变量逃生 | `internal/store/mask.go` |
| M2 | `AddHistory` 入库前调用脱敏 | `internal/store/history.go` |

## 4. 验收

| # | 用例 | 结果 |
|---|---|---|
| T1 | `password=`/`token=`/`aws_secret_access_key=` 等被遮蔽为 `***` | `mask_test.go::TestMaskSecrets` 通过 |
| T2 | `--password VALUE` / `Bearer <t>` / `Basic <t>` 值被遮蔽 | 通过 |
| T3 | `ssh -p 22`、`pwd`、`passwd`、普通命令不受影响 | 通过 |
| T4 | `SSHTOOL_NO_HISTORY_MASK=1` 关闭脱敏 | 通过 |
| T5 | `AddHistory` 入库命令已脱敏；不同口令按脱敏后去重 | `mask_test.go::TestAddHistoryMasksCommand` 通过 |
| T6 | store 全量回归 | `ok` |

## 5. 已知限制
- 仅遮蔽值的第一个 token；带空格的引号值（`"a b"`）会部分残留。
- 短选项 `-p`（ssh 端口 / mysql 密码歧义）不处理，避免误伤 `ssh -p`。
- `curl -u user:pass` 形式的用户口令不在覆盖范围内（不同工具形态过多，留待后续按需补充）。
- 小写 `bearer`/`basic` 不处理（约定大写）。
