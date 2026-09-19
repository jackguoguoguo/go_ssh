package store

import (
	"os"
	"regexp"
)

// MaskSecrets 在把命令写入历史前，对其中的密码 / 令牌等敏感值做脱敏处理。
//
// 设计原则：
//   - 只遮蔽「值」，保留命令语义与可执行性（例如 `sshpass -p ***` 仍能看出在传密码）。
//   - 采用「关键字 / 选项 + 值」的保守匹配，宁可少遮也不误伤常见命令（如 `ssh -p22`
//     的短选项 `-p` 是端口而非密码，因此只对 `--password` 这类长选项做处理）。
//   - 环境变量 SSHTOOL_NO_HISTORY_MASK=1 可整体关闭脱敏（仅用于无法脱离敏感参数的特殊场景）。
//
// 覆盖的常见形态：
//   - key=value：password= / passwd= / pwd= / token= / secret= / api_key= / access_token=
//     / aws_secret_access_key= 等（含大小写、下划线、连字符变体）
//   - 长选项：--password VALUE / --token VALUE / --secret VALUE / --authorization VALUE 等
//   - 凭证头：Authorization: Bearer xxx / Basic xxx
//
// 已知限制：仅遮蔽「值」的第一个 token；带空格的引号值（"a b"）会部分残留，
// 短选项 -p（ssh 端口 / mysql 密码歧义）不处理，避免误伤。
const maskRepl = "***"

// 关键字 = 值。KEY 允许前后带字母数字/下划线/点/连字符，并需包含敏感子串。
// 前缀用贪婪匹配以便尽量保留完整键名（如 aws_secret_access_key=）。
var reKeyValue = regexp.MustCompile(`(?i)(([A-Za-z0-9_.\-]*(?:password|passwd|pwd|token|secret|api[_-]?key|access[_-]?token|access[_-]?key|secret[_-]?key|private[_-]?key|client[_-]?secret|session[_-]?token|credential|authorization)[A-Za-z0-9_.\-]*)\s*=\s*)("[^"]*"|'[^']*'|\S+)`)

// 长选项 值（--password VALUE 等）。
var reLongFlag = regexp.MustCompile(`(?i)((?:(?:--)(?:password|pass|token|secret|api[-_]?key|access[-_]?token|private[-_]?key|client[-_]?secret|session[-_]?token|authorization|credential))\s+)("[^"]*"|'[^']*'|\S+)`)

// 凭证头 Bearer / Basic 后面的值（认证方案词约定大写，故大小写敏感以避免误伤 --basic 等标志）。
// group1=方案词，group2=空白，group3=凭据值。
var reAuthHeader = regexp.MustCompile(`(Bearer|Basic)(\s+)([A-Za-z0-9._~+/=+\-]+)`)

// MaskSecrets 返回脱敏后的命令；cmd 为空或关闭脱敏时原样返回。
func MaskSecrets(cmd string) string {
	if cmd == "" || os.Getenv("SSHTOOL_NO_HISTORY_MASK") == "1" {
		return cmd
	}
	s := reKeyValue.ReplaceAllString(cmd, "${1}"+maskRepl)
	s = reLongFlag.ReplaceAllString(s, "${1}"+maskRepl)
	s = reAuthHeader.ReplaceAllString(s, "${1}${2}"+maskRepl)
	return s
}
