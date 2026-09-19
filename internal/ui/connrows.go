package ui

import (
	"sort"

	"sshtool/internal/store"
)

// connRow 是「保存的连接」面板的一行显示项：分组头或一条连接。
// m.connSel / m.connScroll 均以此为索引单位。
type connRow struct {
	group   string // 分组名（分组头行自身；连接行为其所属分组）
	isGroup bool   // 是否为分组头
	idx     int    // 连接行：m.connList 下标；分组头：-1
}

// rebuildConnRows 依据 m.connList 与收起状态重建显示行。
// 命名分组按字母序在前，未分组条目在最后；收起的分组只显示分组头。
func (m *Model) rebuildConnRows() {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	order := []string{}
	groups := map[string][]int{}
	for i, c := range m.connList {
		if _, ok := groups[c.Group]; !ok {
			order = append(order, c.Group)
		}
		groups[c.Group] = append(groups[c.Group], i)
	}
	// 命名分组按字母序在前，未分组("")排最后。
	sort.SliceStable(order, func(a, b int) bool {
		ga, gb := order[a], order[b]
		if (ga == "") != (gb == "") {
			return gb == ""
		}
		return ga < gb
	})

	rows := make([]connRow, 0, len(m.connList)+len(order))
	for _, g := range order {
		if g != "" {
			rows = append(rows, connRow{group: g, isGroup: true, idx: -1})
			if m.collapsed[g] {
				continue
			}
		}
		for _, i := range groups[g] {
			rows = append(rows, connRow{group: g, idx: i})
		}
	}
	m.connRows = rows

	if m.connSel >= len(m.connRows) {
		m.connSel = len(m.connRows) - 1
	}
	if m.connSel < 0 {
		m.connSel = 0
	}
}

// selectedConn 返回当前选中的连接；选中分组头或列表为空时返回 false。
func (m *Model) selectedConn() (store.Connection, bool) {
	if m.connSel < 0 || m.connSel >= len(m.connRows) {
		return store.Connection{}, false
	}
	row := m.connRows[m.connSel]
	if row.isGroup || row.idx < 0 || row.idx >= len(m.connList) {
		return store.Connection{}, false
	}
	return m.connList[row.idx], true
}

// toggleGroupAt 收放指定分组并重建显示行。
func (m *Model) toggleGroupAt(name string) {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	m.collapsed[name] = !m.collapsed[name]
	m.rebuildConnRows()
}
