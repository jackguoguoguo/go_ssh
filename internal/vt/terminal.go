// Package vt 实现一个轻量 ANSI 终端屏幕模拟器：
// 把远端 PTY 的原始字节流喂给 Write，用 Render 取出带颜色转义、宽度严格等于 Cols() 的行。
package vt

import (
	"strings"
	"sync"

	"github.com/mattn/go-runewidth"
)

// Cell 表示屏幕上的一个字符格。
type Cell struct {
	R    rune // 字符；宽字符右侧的占位格为 0
	W    int  // 显示宽度：1 或 2；占位格为 0
	Attr Attr
}

// 默认空格（默认属性）。
var blankCell = Cell{R: ' ', W: 1, Attr: DefaultAttr}

// cursorState 保存/恢复光标用。
type cursorState struct {
	x, y int
	attr Attr
}

// screenState 用于 alt screen 切换时保存现场。
type screenState struct {
	cells         []Cell
	x, y          int
	attr          Attr
	top, bottom   int
	autowrap      bool
	cursorVisible bool
}

// histRing 是回滚缓冲的环形实现，i=0 表示最旧的一行。
type histRing struct {
	buf   [][]Cell
	start int
}

func (h *histRing) len() int { return len(h.buf) }

func (h *histRing) get(i int) []Cell { return h.buf[(h.start+i)%len(h.buf)] }

func (h *histRing) push(line []Cell, limit int) {
	if limit <= 0 {
		return
	}
	if len(h.buf) < limit {
		h.buf = append(h.buf, line)
		return
	}
	h.buf[h.start] = line
	h.start = (h.start + 1) % len(h.buf)
}

func (h *histRing) reset() {
	h.buf = h.buf[:0]
	h.start = 0
}

// Terminal 是一个终端屏幕模拟器。Write / Resize / Render 等对同一实例并发安全。
type Terminal struct {
	cols int
	rows int
	// scrollbackLimit 是回滚缓冲的最大行数。
	scrollbackLimit int

	screen  []Cell // 一维切片，cells[y*cols+x]
	history histRing

	attr Attr
	cx   int
	cy   int

	saved    cursorState
	altState screenState
	altOn    bool

	top    int // 滚动区上界（含）
	bottom int // 滚动区下界（不含）

	autowrap      bool
	cursorVisible bool

	title string

	scrollOffset int

	pending []byte // 未完成的转义序列或 UTF-8 字节

	mu sync.Mutex
}

// New 创建一个 cols x rows 的终端，scrollback 为回滚缓冲行数。
func New(cols, rows int, scrollback int) *Terminal {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if scrollback < 0 {
		scrollback = 0
	}
	t := &Terminal{
		cols:            cols,
		rows:            rows,
		scrollbackLimit: scrollback,
		screen:          make([]Cell, cols*rows),
		attr:            DefaultAttr,
		autowrap:        true,
		cursorVisible:   true,
		top:             0,
		bottom:          rows,
	}
	t.clearScreen()
	return t
}

// Cols 返回列数。
func (t *Terminal) Cols() int { return t.cols }

// Rows 返回行数。
func (t *Terminal) Rows() int { return t.rows }

// ScrollbackLen 返回当前回滚缓冲中的行数。
func (t *Terminal) ScrollbackLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.history.len()
}

// Cursor 返回光标位置（0-based 屏幕坐标）。位于行末待换行时 x 等于 Cols()。
func (t *Terminal) Cursor() (x, y int, visible bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	x = t.cx
	if x < 0 {
		x = 0
	}
	if x > t.cols {
		x = t.cols
	}
	y = t.cy
	if y < 0 {
		y = 0
	}
	if y > t.rows-1 {
		y = t.rows - 1
	}
	return x, y, t.cursorVisible
}

// ScrollBy 回滚：dy < 0 向上查看历史，dy > 0 向下；到底/到顶自动 clamp。
func (t *Terminal) ScrollBy(dy int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset -= dy
	max := t.history.len()
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	if t.scrollOffset > max {
		t.scrollOffset = max
	}
}

// ScrollOffset 返回距底部的行数，0 表示正在跟随最新输出。
func (t *Terminal) ScrollOffset() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollOffset
}

// ScrollToBottom 回到最新输出。
func (t *Terminal) ScrollToBottom() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset = 0
}

// AtBottom 是否正在跟随最新输出。
func (t *Terminal) AtBottom() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollOffset == 0
}

// Title 返回最近一次 OSC 0/1/2 设置的标题（可能为空）。
func (t *Terminal) Title() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.title
}

// Reset 全清：回到默认属性、光标归位、清空 scrollback。
func (t *Terminal) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resetLocked()
}

// resetLocked 与 Reset 相同，但调用方需已持有锁。
func (t *Terminal) resetLocked() {
	t.attr = DefaultAttr
	t.cx, t.cy = 0, 0
	t.top, t.bottom = 0, t.rows
	t.autowrap = true
	t.cursorVisible = true
	t.scrollOffset = 0
	t.history.reset()
	t.saved = cursorState{}
	t.altState = screenState{}
	t.altOn = false
	t.title = ""
	t.pending = nil
	t.clearScreen()
}

// Resize 改变终端大小：保留左上角区域；行数减少时被挤出屏幕的行推入 scrollback。
func (t *Terminal) Resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols == t.cols && rows == t.rows {
		return
	}
	oldCols, oldRows := t.cols, t.rows
	old := t.screen

	// 行数减少：被挤出屏幕的行（原屏幕底部）按顺序进入 scrollback。
	if rows < oldRows {
		for y := rows; y < oldRows; y++ {
			row := old[y*oldCols : (y+1)*oldCols]
			t.pushHistory(row)
		}
	}

	t.screen = make([]Cell, cols*rows)
	for i := range t.screen {
		t.screen[i] = blankCell
	}
	t.cols, t.rows = cols, rows
	n := oldCols
	if n > cols {
		n = cols
	}
	m := oldRows
	if m > rows {
		m = rows
	}
	for y := 0; y < m; y++ {
		copy(t.screen[y*cols:y*cols+n], old[y*oldCols:y*oldCols+n])
	}
	// 变窄后可能把一个宽字符切成一半：清掉孤立的占位格。
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			c := t.screen[y*cols+x]
			if c.W == 2 && (x+1 >= cols || t.screen[y*cols+x+1].R != 0) {
				t.screen[y*cols+x] = blankCell
			}
		}
	}
	t.top, t.bottom = 0, rows
	if t.cx > cols {
		t.cx = cols
	}
	if t.cx < 0 {
		t.cx = 0
	}
	if t.cy > rows-1 {
		t.cy = rows - 1
	}
	if t.cy < 0 {
		t.cy = 0
	}
	t.scrollOffset = 0
}

// Render 返回 Rows() 行字符串，每行含 ANSI 转义，可打印宽度严格等于 Cols()。
// 不绘制光标。
func (t *Terminal) Render() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, t.rows)
	maxTop := t.history.len()
	top := maxTop - t.scrollOffset
	if top < 0 {
		top = 0
	}
	if top > maxTop {
		top = maxTop
	}
	for y := 0; y < t.rows; y++ {
		idx := top + y
		var line []Cell
		switch {
		case idx < maxTop:
			line = t.history.get(idx)
		default:
			sy := idx - maxTop
			if sy >= 0 && sy < t.rows {
				line = t.screen[sy*t.cols : (sy+1)*t.cols]
			}
		}
		out[y] = t.renderLine(line)
	}
	return out
}

// renderLine 把一行（长度可能不等于 Cols()）渲染成宽度严格等于 Cols() 的字符串。
func (t *Terminal) renderLine(line []Cell) string {
	var b strings.Builder
	b.WriteString("\x1b[0m")
	cur := DefaultAttr
	for x := 0; x < t.cols; {
		c := blankCell
		if x < len(line) {
			c = line[x]
		}
		if c.Attr != cur {
			b.WriteString(c.Attr.SGR())
			cur = c.Attr
		}
		switch {
		case c.R == 0 || c.Attr.Hidden:
			// 宽字符占位格 / 隐藏字符：渲染为空格
			b.WriteByte(' ')
			x++
		case c.W == 2 && x+1 < t.cols:
			b.WriteRune(c.R)
			x += 2
		case c.W == 2:
			// 宽字符被挤到最后一格（理论上不该发生）：退化为空格
			b.WriteByte(' ')
			x++
		default:
			b.WriteRune(c.R)
			x++
		}
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// ---------- 内部：屏幕操作（调用方需持有锁） ----------

// blank 返回用当前属性填充的空格（BCE 语义）。
func (t *Terminal) blank() Cell { return Cell{R: ' ', W: 1, Attr: t.attr} }

// clearScreen 用当前属性清屏。
func (t *Terminal) clearScreen() {
	c := t.blank()
	for i := range t.screen {
		t.screen[i] = c
	}
}

// pushHistory 把一行推入回滚缓冲（会拷贝）。
func (t *Terminal) pushHistory(src []Cell) {
	if t.scrollbackLimit <= 0 {
		return
	}
	cp := make([]Cell, len(src))
	copy(cp, src)
	t.history.push(cp, t.scrollbackLimit)
}

// put 在当前光标处写入一个字符。
func (t *Terminal) put(r rune) {
	w := runewidth.RuneWidth(r)
	if w <= 0 {
		// 组合字符等零宽字符：忽略
		return
	}
	if t.cx >= t.cols { // 行末待换行状态
		if !t.autowrap {
			t.cx = t.cols - 1
		} else {
			t.lineFeed()
			t.cx = 0 // 隐式换行：回到行首
		}
	}
	if w == 2 && t.cx+2 > t.cols {
		if !t.autowrap {
			return
		}
		t.lineFeed()
		t.cx = 0
		if t.cx+2 > t.cols {
			return // 一列宽的屏幕放不下宽字符
		}
	}
	// 覆盖宽字符的任意一半时，另一半也要清成空格
	if t.cx > 0 {
		prev := t.screen[t.cy*t.cols+t.cx-1]
		if prev.W == 2 && t.screen[t.cy*t.cols+t.cx].R == 0 {
			t.screen[t.cy*t.cols+t.cx-1] = t.blank()
		}
	}
	cur := t.screen[t.cy*t.cols+t.cx]
	if cur.W == 2 && t.cx+1 < t.cols {
		t.screen[t.cy*t.cols+t.cx+1] = t.blank()
	}
	t.screen[t.cy*t.cols+t.cx] = Cell{R: r, W: w, Attr: t.attr}
	if w == 2 {
		t.screen[t.cy*t.cols+t.cx+1] = Cell{R: 0, W: 0, Attr: t.attr}
	}
	t.cx += w
}

// lineFeed 下移一行（LF/VT/FF/IND）。
func (t *Terminal) lineFeed() {
	if t.cy >= t.top && t.cy < t.bottom {
		if t.cy == t.bottom-1 {
			t.scrollUp(1)
		} else {
			t.cy++
		}
		return
	}
	if t.cy < t.rows-1 {
		t.cy++
	}
}

// reverseIndex 上移一行（RI），到滚动区顶部时下滚。
func (t *Terminal) reverseIndex() {
	if t.cy == t.top {
		t.scrollDown(1)
		return
	}
	if t.cy > 0 {
		t.cy--
	}
}

// scrollUp 在滚动区内上滚 n 行。
func (t *Terminal) scrollUp(n int) {
	top, bot := t.top, t.bottom
	if n <= 0 || bot <= top {
		return
	}
	if n > bot-top {
		n = bot - top
	}
	for k := 0; k < n; k++ {
		if top == 0 {
			t.pushHistory(t.screen[0:t.cols])
		}
		copy(t.screen[top*t.cols:(bot-1)*t.cols], t.screen[(top+1)*t.cols:bot*t.cols])
		for x := 0; x < t.cols; x++ {
			t.screen[(bot-1)*t.cols+x] = t.blank()
		}
	}
}

// scrollDown 在滚动区内下滚 n 行。
func (t *Terminal) scrollDown(n int) {
	top, bot := t.top, t.bottom
	if n <= 0 || bot <= top {
		return
	}
	if n > bot-top {
		n = bot - top
	}
	for k := 0; k < n; k++ {
		copy(t.screen[(top+1)*t.cols:bot*t.cols], t.screen[top*t.cols:(bot-1)*t.cols])
		for x := 0; x < t.cols; x++ {
			t.screen[top*t.cols+x] = t.blank()
		}
	}
}

// eraseCell 用当前属性擦除一格。
func (t *Terminal) eraseCell(x, y int) {
	t.screen[y*t.cols+x] = t.blank()
}

func (t *Terminal) eraseInLine(mode int) {
	y := t.cy
	if y < 0 || y >= t.rows {
		return
	}
	switch mode {
	case 0:
		for x := t.cx; x < t.cols; x++ {
			t.eraseCell(x, y)
		}
	case 1:
		end := t.cx + 1
		if end > t.cols {
			end = t.cols
		}
		for x := 0; x < end; x++ {
			t.eraseCell(x, y)
		}
	case 2:
		for x := 0; x < t.cols; x++ {
			t.eraseCell(x, y)
		}
	}
}

func (t *Terminal) eraseInDisplay(mode int) {
	switch mode {
	case 0:
		for x := t.cx; x < t.cols; x++ {
			t.eraseCell(x, t.cy)
		}
		for y := t.cy + 1; y < t.rows; y++ {
			for x := 0; x < t.cols; x++ {
				t.eraseCell(x, y)
			}
		}
	case 1:
		for y := 0; y < t.cy; y++ {
			for x := 0; x < t.cols; x++ {
				t.eraseCell(x, y)
			}
		}
		end := t.cx + 1
		if end > t.cols {
			end = t.cols
		}
		for x := 0; x < end; x++ {
			t.eraseCell(x, t.cy)
		}
	case 2:
		t.clearScreen()
	case 3:
		t.clearScreen()
		t.history.reset()
		t.scrollOffset = 0
	}
}

func (t *Terminal) insertLines(n int) {
	if t.cy < t.top || t.cy >= t.bottom || n <= 0 {
		return
	}
	if n > t.bottom-t.cy {
		n = t.bottom - t.cy
	}
	copy(t.screen[(t.cy+n)*t.cols:t.bottom*t.cols], t.screen[t.cy*t.cols:(t.bottom-n)*t.cols])
	for y := t.cy; y < t.cy+n; y++ {
		for x := 0; x < t.cols; x++ {
			t.screen[y*t.cols+x] = t.blank()
		}
	}
}

func (t *Terminal) deleteLines(n int) {
	if t.cy < t.top || t.cy >= t.bottom || n <= 0 {
		return
	}
	if n > t.bottom-t.cy {
		n = t.bottom - t.cy
	}
	copy(t.screen[t.cy*t.cols:(t.bottom-n)*t.cols], t.screen[(t.cy+n)*t.cols:t.bottom*t.cols])
	for y := t.bottom - n; y < t.bottom; y++ {
		for x := 0; x < t.cols; x++ {
			t.screen[y*t.cols+x] = t.blank()
		}
	}
}

func (t *Terminal) deleteChars(n int) {
	row := t.cy * t.cols
	if n <= 0 || t.cx >= t.cols {
		return
	}
	if n > t.cols-t.cx {
		n = t.cols - t.cx
	}
	copy(t.screen[row+t.cx:row+t.cols-n], t.screen[row+t.cx+n:row+t.cols])
	for x := t.cols - n; x < t.cols; x++ {
		t.screen[row+x] = t.blank()
	}
}

func (t *Terminal) insertChars(n int) {
	row := t.cy * t.cols
	if n <= 0 || t.cx >= t.cols {
		return
	}
	if n > t.cols-t.cx {
		n = t.cols - t.cx
	}
	copy(t.screen[row+t.cx+n:row+t.cols], t.screen[row+t.cx:row+t.cols-n])
	for x := t.cx; x < t.cx+n; x++ {
		t.screen[row+x] = t.blank()
	}
}

func (t *Terminal) eraseChars(n int) {
	row := t.cy * t.cols
	if n <= 0 || t.cx >= t.cols {
		return
	}
	if n > t.cols-t.cx {
		n = t.cols - t.cx
	}
	for x := t.cx; x < t.cx+n; x++ {
		t.screen[row+x] = t.blank()
	}
}

// saveCursor 保存光标（DECSC）。
func (t *Terminal) saveCursor() {
	t.saved = cursorState{x: t.cx, y: t.cy, attr: t.attr}
}

// restoreCursor 恢复光标（DECRC）。
func (t *Terminal) restoreCursor() {
	s := t.saved
	t.cx, t.cy = s.x, s.y
	t.attr = s.attr
	if t.cx < 0 {
		t.cx = 0
	}
	if t.cx > t.cols {
		t.cx = t.cols
	}
	if t.cy < 0 {
		t.cy = 0
	}
	if t.cy > t.rows-1 {
		t.cy = t.rows - 1
	}
}

// switchAlt 进入/退出 alt screen：进入时清屏并保存现场，退出时恢复。
func (t *Terminal) switchAlt(on bool) {
	if on == t.altOn {
		return
	}
	if on {
		t.altState = screenState{
			cells:         append([]Cell(nil), t.screen...),
			x:             t.cx,
			y:             t.cy,
			attr:          t.attr,
			top:           t.top,
			bottom:        t.bottom,
			autowrap:      t.autowrap,
			cursorVisible: t.cursorVisible,
		}
		t.clearScreen()
		t.cx, t.cy = 0, 0
		t.top, t.bottom = 0, t.rows
		t.altOn = true
		return
	}
	s := t.altState
	if len(s.cells) == len(t.screen) {
		copy(t.screen, s.cells)
	} else {
		t.clearScreen()
	}
	t.cx, t.cy = s.x, s.y
	t.attr = s.attr
	t.top, t.bottom = 0, t.rows
	if s.bottom > s.top && s.bottom <= t.rows {
		t.top, t.bottom = s.top, s.bottom
	}
	t.autowrap = s.autowrap
	t.cursorVisible = s.cursorVisible
	t.altOn = false
	t.scrollOffset = 0
}

// decaln 用 'E' 填满屏幕（DEC 校准图案，ESC # 8）。
func (t *Terminal) decaln() {
	c := Cell{R: 'E', W: 1, Attr: t.attr}
	for i := range t.screen {
		t.screen[i] = c
	}
}
