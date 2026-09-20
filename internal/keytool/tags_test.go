package keytool

import (
	"reflect"
	"testing"
)

func TestParseTags(t *testing.T) {
	cases := []struct {
		comment string
		want    []string
	}{
		{"deploy@CI #prod #ci #readonly", []string{"prod", "ci", "readonly"}},
		{"user@host #Prod", []string{"prod"}},           // 大小写归一
		{"no tags here", nil},                           // 无标签
		{"a #prod#ci", []string{"prod", "ci"}},          // 连写
		{"a #prod,ci", []string{"prod", "ci"}},          // 逗号
		{"a #prod #prod", []string{"prod"}},             // 去重
		{"#leading #trailing", []string{"leading", "trailing"}},
	}
	for _, c := range cases {
		got := ParseTags(c.comment)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseTags(%q) = %v, want %v", c.comment, got, c.want)
		}
	}
}

func TestBaseCommentAndFormat(t *testing.T) {
	base := BaseComment("deploy@CI #prod #ci")
	if base != "deploy@CI" {
		t.Fatalf("BaseComment = %q, want deploy@CI", base)
	}
	got := FormatComment(base, []string{"PROD", "#ci", " ci ", ""})
	if got != "deploy@CI #prod #ci" {
		t.Fatalf("FormatComment = %q, want %q", got, "deploy@CI #prod #ci")
	}
	// 主体为空时也不应留下前导空格
	if got := FormatComment("", []string{"prod"}); got != "#prod" {
		t.Fatalf("FormatComment 空主体 = %q，期望 #prod", got)
	}
}

func TestMatchTags(t *testing.T) {
	have := []string{"prod", "ci"}
	if !MatchTags(have, nil, false) {
		t.Error("空 want 应视为匹配")
	}
	if !MatchTags(have, []string{"prod"}, true) {
		t.Error("包含 prod 应匹配（与）")
	}
	if MatchTags(have, []string{"prod", "db"}, true) {
		t.Error("缺少 db 时不应匹配（与）")
	}
	if !MatchTags(have, []string{"db", "ci"}, false) {
		t.Error("含 ci 应匹配（或）")
	}
	if MatchTags(have, []string{"db", "staging"}, false) {
		t.Error("都不含时不应匹配（或）")
	}
	if !HasTag(have, "#prod") {
		t.Error("HasTag 应忽略 # 前缀")
	}
}

func TestTagIndexLoadSaveAndSet(t *testing.T) {
	path := t.TempDir() + "/key-tags.json"

	ix, err := LoadTagIndex(path)
	if err != nil {
		t.Fatalf("加载空索引失败：%v", err)
	}
	if len(ix.Keys) != 0 {
		t.Fatalf("空索引应为空，实际 %d", len(ix.Keys))
	}

	ix.Set("SHA256:abc", "/home/u/.ssh/id_ed25519", []string{"PROD", "#ci"}, "部署用")
	if err := ix.Save(path); err != nil {
		t.Fatalf("保存失败：%v", err)
	}

	got, err := LoadTagIndex(path)
	if err != nil {
		t.Fatalf("重新加载失败：%v", err)
	}
	tags := got.TagsOf("SHA256:abc")
	if len(tags) != 2 || tags[0] != "prod" || tags[1] != "ci" {
		t.Fatalf("标签回读 = %v，期望 [prod ci]", tags)
	}
	if k, _ := got.Get("SHA256:abc"); k.Notes != "部署用" {
		t.Fatalf("备注回读 = %q", k.Notes)
	}

	// 更新既有记录不应产生重复条目
	got.Set("SHA256:abc", "/home/u/.ssh/id_ed25519", []string{"db"}, "改备注")
	if len(got.Keys) != 1 {
		t.Fatalf("更新后应仍为 1 条，实际 %d", len(got.Keys))
	}
	if tags := got.TagsOf("SHA256:abc"); len(tags) != 1 || tags[0] != "db" {
		t.Fatalf("更新后标签 = %v，期望 [db]", tags)
	}

	// 删除
	got.Remove("SHA256:abc")
	if len(got.Keys) != 0 {
		t.Fatalf("删除后应为空，实际 %d", len(got.Keys))
	}
}

func TestTagIndexPrune(t *testing.T) {
	ix := &TagIndex{}
	ix.Set("SHA256:keep", "", []string{"prod"}, "")
	ix.Set("SHA256:gone", "", []string{"old"}, "")

	ix.Prune([]KeyInfo{{Fingerprint: "SHA256:keep"}})
	if len(ix.Keys) != 1 || ix.Keys[0].Fingerprint != "SHA256:keep" {
		t.Fatalf("Prune 结果错误：%+v", ix.Keys)
	}
}