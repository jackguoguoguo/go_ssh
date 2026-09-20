package keytool

import "strings"

// TagPrefix 标签前缀。标签写在公钥注释里，如 `deploy@CI #prod #ci`。
const TagPrefix = "#"

// NormalizeTag 规范化单个标签：去掉前缀与首尾空白，转小写。
func NormalizeTag(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, TagPrefix)
	return strings.ToLower(strings.TrimSpace(t))
}

// NormalizeTags 规范化并去重一组标签（保序，跳过空值）。
func NormalizeTags(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		n := NormalizeTag(t)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// ParseTags 从公钥注释中提取 `#tag` 形式的标签。
// 支持以空格分隔或直接用 `#` 连接（如 `#prod#ci`），也支持逗号分隔。
func ParseTags(comment string) []string {
	var raw []string
	for _, field := range strings.Fields(comment) {
		if !strings.Contains(field, TagPrefix) {
			continue
		}
		// 一个字段里可能含多个标签：以 '#' 与 ',' 切分。
		for _, part := range strings.FieldsFunc(field, func(r rune) bool {
			return r == '#' || r == ','
		}) {
			raw = append(raw, part)
		}
	}
	return NormalizeTags(raw)
}

// BaseComment 返回去除标签后的注释主体（用于重新拼装，避免标签重复累积）。
func BaseComment(comment string) string {
	var keep []string
	for _, field := range strings.Fields(comment) {
		if strings.Contains(field, TagPrefix) {
			continue
		}
		keep = append(keep, field)
	}
	return strings.Join(keep, " ")
}

// FormatComment 把注释主体与标签重新拼成规范形式：`base #t1 #t2`。
func FormatComment(base string, tags []string) string {
	base = strings.TrimSpace(base)
	tags = NormalizeTags(tags)
	var b strings.Builder
	b.WriteString(base)
	for _, t := range tags {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(TagPrefix + t)
	}
	return b.String()
}

// MatchTags 判断 have 是否满足 want。
//   - want 为空：视为匹配（无过滤条件）。
//   - matchAll=true：have 需包含 want 的每一个（与）；
//   - matchAll=false：have 包含 want 中任意一个即可（或）。
func MatchTags(have, want []string, matchAll bool) bool {
	need := NormalizeTags(want)
	if len(need) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, h := range NormalizeTags(have) {
		set[h] = true
	}
	if matchAll {
		for _, w := range need {
			if !set[w] {
				return false
			}
		}
		return true
	}
	for _, w := range need {
		if set[w] {
			return true
		}
	}
	return false
}

// HasTag 报告 have 中是否含某个标签。
func HasTag(have []string, tag string) bool {
	return MatchTags(have, []string{tag}, true)
}