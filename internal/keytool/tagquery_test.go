package keytool

import "testing"

func TestParseTagQuery(t *testing.T) {
	cases := []struct {
		q    string
		want int // 组的数量
	}{
		{"", 0},
		{"prod", 1},
		{"prod +ci", 1},
		{"prod,staging", 2},
		{"#prod + #ci , staging", 2},
	}
	for _, c := range cases {
		got := ParseTagQuery(c.q)
		if len(got) != c.want {
			t.Errorf("ParseTagQuery(%q) 组数 = %d，期望 %d（%+v）", c.q, len(got), c.want, got)
		}
	}
	// 组内为「与」，且去掉 # 与 +
	g := ParseTagQuery("#prod + #ci")
	if len(g) != 1 || len(g[0]) != 2 || g[0][0] != "prod" || g[0][1] != "ci" {
		t.Fatalf("与组解析错误：%+v", g)
	}
}

func TestMatchTagQuery(t *testing.T) {
	have := []string{"prod", "ci"}

	if !MatchTagQuery(have, "") {
		t.Error("空查询应匹配")
	}
	if !MatchTagQuery(have, "prod") {
		t.Error("单个标签应匹配")
	}
	if MatchTagQuery(have, "db") {
		t.Error("不存在的标签不应匹配")
	}
	// 与：两个都满足
	if !MatchTagQuery(have, "prod +ci") {
		t.Error("prod +ci 应匹配（与）")
	}
	if MatchTagQuery(have, "prod +db") {
		t.Error("prod +db 不应匹配（缺 db）")
	}
	// 或：满足其一
	if !MatchTagQuery(have, "db,ci") {
		t.Error("db,ci 应匹配（或，命中 ci）")
	}
	if MatchTagQuery(have, "db,staging") {
		t.Error("db,staging 不应匹配（都不含）")
	}
	// 大小写与 # 前缀不敏感
	if !MatchTagQuery(have, "#PROD") {
		t.Error("标签大小写与 # 前缀应不敏感")
	}
}
