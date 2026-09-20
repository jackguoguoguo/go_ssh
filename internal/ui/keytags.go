package ui

import (
	"path/filepath"
	"strings"

	"sshtool/internal/keytool"
	"sshtool/internal/store"
)

// ---------- 标签索引读写 ----------

// loadKeyTags 读取密钥标签索引；失败时返回空索引（不影响主流程）。
func loadKeyTags() *keytool.TagIndex {
	ix, err := keytool.LoadTagIndex(keytool.DefaultTagIndexPath())
	if err != nil || ix == nil {
		return &keytool.TagIndex{}
	}
	return ix
}

// saveKeyTags 保存标签索引。
func saveKeyTags(ix *keytool.TagIndex) error {
	return ix.Save(keytool.DefaultTagIndexPath())
}

// tagString 把标签格式化为 `#a #b` 形式；无标签时给占位。
func tagString(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tags))
	for _, t := range tags {
		parts = append(parts, "#"+t)
	}
	return strings.Join(parts, " ")
}

// tagsForDisplay 返回展示用标签串（空时给「未打标签」）。
func tagsForDisplay(tags []string) string {
	if s := tagString(tags); s != "" {
		return s
	}
	return "未打标签"
}

// ---------- 编辑标签与备注 ----------

// openPickKeyForTags 选择要编辑标签的本机公钥。
func (m *Model) openPickKeyForTags() {
	keys, err := keytool.ListLocal(keytool.DefaultDir())
	if err != nil {
		m.setMsg("扫描本机密钥失败：" + err.Error())
		return
	}
	if len(keys) == 0 {
		m.dlg = &dlg{kind: dlgText, title: "本机公钥", body: []string{"", "  未找到任何公钥。可先用「生成新密钥」创建一对。"}}
		return
	}
	ix := loadKeyTags()
	items := make([]pickItem, 0, len(keys))
	for _, k := range keys {
		items = append(items, pickItem{
			Label: filepath.Base(k.PublicPath),
			Desc:  k.Alg + "  " + tagsForDisplay(ix.TagsOf(k.Fingerprint)),
		})
	}
	m.dlg = &dlg{
		kind:  dlgPick,
		title: "选择要编辑标签的密钥",
		items: items,
		onPick: func(m *Model, picked []int) {
			if len(picked) == 0 {
				return
			}
			m.openEditKeyTags(keys[picked[0]])
		},
	}
}

// openEditKeyTags 编辑某密钥的标签与备注（写入 ~/.sshtool/key-tags.json）。
func (m *Model) openEditKeyTags(key keytool.KeyInfo) {
	ix := loadKeyTags()
	cur, _ := ix.Get(key.Fingerprint)

	m.dlg = &dlg{
		kind:    dlgForm,
		title:   "编辑标签与备注 · " + filepath.Base(key.PublicPath),
		okLabel: "保存",
		message: "指纹：" + key.Fingerprint,
		fields: []dlgField{
			{label: "标签", value: []rune(tagString(cur.Tags)), hint: "空格分隔，如 #prod #ci；留空清除"},
			{label: "备注", value: []rune(cur.Notes), hint: "备忘用途，如「CI 部署专用」"},
		},
		onOK: func(m *Model, v []string) {
			var tags []string
			var notes string
			if len(v) > 0 {
				tags = keytool.ParseTags(v[0])
			}
			if len(v) > 1 {
				notes = strings.TrimSpace(v[1])
			}
			idx := loadKeyTags()
			idx.Set(key.Fingerprint, key.PublicPath, tags, notes)
			if err := saveKeyTags(idx); err != nil {
				m.setMsg("保存标签失败：" + err.Error())
				return
			}
			if s := tagString(tags); s != "" {
				m.setMsg("已保存标签：" + s)
			} else {
				m.setMsg("已清除该密钥的标签")
			}
		},
	}
}

// connsMatchingTags 返回「分组命中任一标签」的连接下标，用于按密钥标签预勾选目标。
// 连接的分组字段在这里充当标签维度（Connection 是冻结契约，不能新增 tags 字段）。
func connsMatchingTags(conns []store.Connection, tags []string) []int {
	want := keytool.NormalizeTags(tags)
	if len(want) == 0 {
		return nil
	}
	var out []int
	for i, c := range conns {
		if keytool.MatchTags([]string{c.Group}, want, false) {
			out = append(out, i)
		}
	}
	return out
}