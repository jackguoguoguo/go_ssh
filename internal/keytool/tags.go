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

// ParseTagQuery 解析组合查询串：
//   - `,` 分隔的是「或」关系（满足其中一组即可）；
//   - 一组内以空格分隔（可带 `+` / `#` 前缀）表示「与」关系（全部满足）。
//
// 例：`prod` → 含 prod；`prod +ci` → 同时含 prod 与 ci；`prod,staging` → 含其一。
func ParseTagQuery(query string) [][]string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	var groups [][]string
	for _, part := range strings.Split(query, ",") {
		var and []string
		for _, tok := range strings.Fields(part) {
			tok = strings.TrimSpace(strings.TrimPrefix(tok, "+"))
			tok = NormalizeTag(tok)
			if tok == "" {
				continue
			}
			and = append(and, tok)
		}
		groups = append(groups, and)
	}
	return groups
}

// MatchTagQuery 判断 have 是否满足组合查询：任一「或」组内全部命中即算匹配。
// 空查询恒为匹配（等价于不过滤）。
func MatchTagQuery(have []string, query string) bool {
	groups := ParseTagQuery(query)
	if len(groups) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, h := range NormalizeTags(have) {
		set[h] = true
	}
	for _, and := range groups {
		ok := len(and) > 0
		for _, t := range and {
			if !set[t] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}