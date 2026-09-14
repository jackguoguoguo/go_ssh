package vt

import (
	"strings"
	"unicode/utf8"
)

// 本文件实现 ANSI 转义序列解析。
// 关键要求：Write 可能被按任意边界切分，未完成的序列 / UTF-8 字节缓存在 t.pending 中，
// 下次 Write 先拼接再解析；不认识的序列一律安全吞掉，绝不把转义字符当作普通字符打印。

const (
	maxCSIParams = 512   // CSI 参数区最大长度，超过则放弃该序列
	maxStringLen = 65536 // OSC/DCS 等字符串最大长度，超过则放弃
)

// Write 解析 ANSI 流并更新屏幕，始终返回 len(p), nil。
func (t *Terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(p), t.feed(p)
}

// feed 解析一段字节（调用方需持有锁）。
func (t *Terminal) feed(p []byte) error {
	buf := p
	if len(t.pending) > 0 {
		buf = make([]byte, 0, len(t.pending)+len(p))
		buf = append(buf, t.pending...)
		buf = append(buf, p...)
		t.pending = nil
	}
	i := 0
	for i < len(buf) {
		b := buf[i]
		switch {
		case b == 0x1b:
			n, ok := t.parseEscape(buf, i)
			if !ok { // 序列不完整，缓存剩余字节
				t.pending = append([]byte(nil), buf[i:]...)
				return nil
			}
			if n <= 0 {
				i++
				continue
			}
			i += n
		case b < 0x20 || b == 0x7f:
			t.control(b)
			i++
		default:
			r, size, incomplete := decodeRune(buf[i:])
			if incomplete { // UTF-8 被切分
				t.pending = append([]byte(nil), buf[i:]...)
				return nil
			}
			if size <= 0 {
				size = 1
			}
			t.put(r)
			i += size
		}
	}
	t.pending = nil
	return nil
}

// decodeRune 解码一个 rune；incomplete 表示字节流在 rune 中间被截断。
func decodeRune(b []byte) (r rune, size int, incomplete bool) {
	if b[0] < 0x80 {
		return rune(b[0]), 1, false
	}
	need := utf8SeqLen(b[0])
	if need == 0 {
		return utf8.RuneError, 1, false // 非法起始字节
	}
	if len(b) >= need {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size <= 1 {
			return utf8.RuneError, 1, false
		}
		return r, size, false
	}
	// 长度不足：校验已有字节是否合法前缀
	for k := 1; k < len(b); k++ {
		if b[k]&0xC0 != 0x80 {
			return utf8.RuneError, 1, false
		}
	}
	return 0, 0, true
}

// utf8SeqLen 返回 UTF-8 首字节所需的总长度，非法首字节返回 0。
func utf8SeqLen(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 0
}

// control 处理 C0 控制字符。
func (t *Terminal) control(b byte) {
	switch b {
	case 0x07: // BEL：忽略
	case 0x08: // BS：左移
		if t.cx >= t.cols {
			t.cx = t.cols - 1
		} else if t.cx > 0 {
			t.cx--
		}
	case 0x09: // HT：跳到下一个 8 的倍数
		t.cx = (t.cx/8 + 1) * 8
		if t.cx > t.cols-1 {
			t.cx = t.cols - 1
		}
	case 0x0a, 0x0b, 0x0c: // LF / VT / FF：下移一行
		t.lineFeed()
	case 0x0d: // CR：回到行首
		t.cx = 0
	}
}

// parseEscape 解析从 buf[i]（必须是 0x1b）开始的一个 ESC 序列。
// 返回消费的字节数与是否解析完成；未完成时调用方应缓存 buf[i:]。
func (t *Terminal) parseEscape(buf []byte, i int) (int, bool) {
	if i+1 >= len(buf) {
		return 0, false
	}
	switch c := buf[i+1]; c {
	case '[':
		j := i + 2
		for j < len(buf) {
			b := buf[j]
			if b >= 0x40 && b <= 0x7e { // 终止字节
				t.csi(buf[i+2:j], b)
				return j - i + 1, true
			}
			if b >= 0x20 && b <= 0x3f { // 参数字节 / 中间字节 / 私有前缀
				if j-(i+2) > maxCSIParams {
					return j - i, true // 过长，放弃
				}
				j++
				continue
			}
			// 序列中出现控制字符：放弃本序列，从该控制字符继续解析
			return j - i, true
		}
		return 0, false
	case ']': // OSC
		return t.parseString(buf, i, true)
	case 'P', 'X', '^', '_': // DCS / SOS / PM / APC：吞掉内容
		return t.parseString(buf, i, false)
	case '(', ')', '*', '+', '-', '.', '/': // 字符集指定：忽略
		if i+2 >= len(buf) {
			return 0, false
		}
		return 3, true
	case '#':
		if i+2 >= len(buf) {
			return 0, false
		}
		if buf[i+2] == '8' {
			t.decaln()
		}
		return 3, true
	case '7', 's': // DECSC
		t.saveCursor()
		return 2, true
	case '8', 'u': // DECRC
		t.restoreCursor()
		return 2, true
	case 'D': // IND
		t.lineFeed()
		return 2, true
	case 'E': // NEL
		t.cx = 0
		t.lineFeed()
		return 2, true
	case 'M': // RI
		t.reverseIndex()
		return 2, true
	case 'c': // RIS
		t.resetLocked()
		return 2, true
	case '=', '>': // 键盘模式：忽略
		return 2, true
	default:
		if c < 0x20 { // ESC ESC：从第二个 ESC 重新开始
			return 1, true
		}
		return 2, true // 未知 ESC 序列：安全吞掉
	}
}

// parseString 解析 OSC / DCS 等字符串序列，以 BEL 或 ST(ESC \) 结束。
func (t *Terminal) parseString(buf []byte, i int, isOSC bool) (int, bool) {
	j := i + 2
	for j < len(buf) {
		switch b := buf[j]; {
		case b == 0x07: // BEL
			if isOSC {
				t.osc(string(buf[i+2 : j]))
			}
			return j - i + 1, true
		case b == 0x9c: // 8 位 ST
			if isOSC {
				t.osc(string(buf[i+2 : j]))
			}
			return j - i + 1, true
		case b == 0x1b:
			if j+1 >= len(buf) {
				return 0, false
			}
			if buf[j+1] == '\\' { // ST
				if isOSC {
					t.osc(string(buf[i+2 : j]))
				}
				return j - i + 2, true
			}
			// ESC 后不是 '\'：结束本串，从 ESC 重新解析
			return j - i, true
		case j-(i+2) > maxStringLen:
			return j - i, true
		}
		j++
	}
	return 0, false
}

// osc 处理 OSC 内容（仅保留标题）。
func (t *Terminal) osc(s string) {
	idx := strings.IndexByte(s, ';')
	if idx <= 0 {
		return
	}
	switch s[:idx] {
	case "0", "1", "2":
		t.title = s[idx+1:]
	}
}

// csi 分发一个 CSI 序列。raw 为 ESC[ 与终止字节之间的内容，final 为终止字节。
func (t *Terminal) csi(raw []byte, final byte) {
	s := string(raw)
	var priv byte
	if len(s) > 0 && (s[0] == '?' || s[0] == '>' || s[0] == '<' || s[0] == '=') {
		priv = s[0]
		s = s[1:]
	}
	var params []int
	if len(s) > 0 {
		params = parseParams(s)
	}
	p0 := paramAt(params, 0, 1)
	switch final {
	case 'A': // CUU
		t.cy = clamp(t.cy-p0, 0, t.rows-1)
	case 'B': // CUD
		t.cy = clamp(t.cy+p0, 0, t.rows-1)
	case 'C': // CUF
		t.cx = clamp(t.cx+p0, 0, t.cols-1)
	case 'D': // CUB
		t.cx = clamp(t.cx-p0, 0, t.cols-1)
	case 'E': // CNL
		t.cy = clamp(t.cy+p0, 0, t.rows-1)
		t.cx = 0
	case 'F': // CPL
		t.cy = clamp(t.cy-p0, 0, t.rows-1)
		t.cx = 0
	case 'G', '`': // CHA
		t.cx = clamp(p0-1, 0, t.cols-1)
	case 'd': // VPA
		t.cy = clamp(p0-1, 0, t.rows-1)
	case 'H', 'f': // CUP
		t.cy = clamp(paramAt(params, 0, 1)-1, 0, t.rows-1)
		t.cx = clamp(paramAt(params, 1, 1)-1, 0, t.cols-1)
	case 'J': // ED
		t.eraseInDisplay(paramAt(params, 0, 0))
	case 'K': // EL
		t.eraseInLine(paramAt(params, 0, 0))
	case 'L': // IL
		t.insertLines(p0)
	case 'M': // DL
		t.deleteLines(p0)
	case 'P': // DCH
		t.deleteChars(p0)
	case '@': // ICH
		t.insertChars(p0)
	case 'X': // ECH
		t.eraseChars(p0)
	case 'S': // SU
		t.scrollUp(p0)
	case 'T': // SD
		t.scrollDown(p0)
	case 'r': // DECSTBM
		t.setScrollRegion(paramAt(params, 0, 1), paramAt(params, 1, t.rows))
	case 'm': // SGR
		if priv == 0 {
			t.sgr(params)
		}
	case 'h', 'l': // SM / RM
		if priv == '?' {
			t.setModes(params, final == 'h')
		}
	case 's': // 保存光标
		if priv == 0 {
			t.saveCursor()
		}
	case 'u': // 恢复光标
		if priv == 0 {
			t.restoreCursor()
		}
	}
	// 其余（DSR n、未知序列等）一律忽略
}

// setScrollRegion 设置滚动区（1-based，含两端），设置后光标移到左上角。
func (t *Terminal) setScrollRegion(top, bottom int) {
	if top < 1 {
		top = 1
	}
	if bottom > t.rows {
		bottom = t.rows
	}
	if bottom < 1 {
		bottom = 1
	}
	if top > t.rows {
		top = t.rows
	}
	if top >= bottom { // 非法区间：复位为整屏
		top, bottom = 1, t.rows
	}
	t.top = top - 1
	t.bottom = bottom
	t.cx, t.cy = 0, 0
}

// setModes 处理私有模式（?h / ?l）。
func (t *Terminal) setModes(params []int, enable bool) {
	for _, v := range params {
		switch v {
		case 7: // 自动换行
			t.autowrap = enable
		case 25: // 光标显隐
			t.cursorVisible = enable
		case 47, 1047, 1049: // alt screen
			t.switchAlt(enable)
		}
	}
}

// sgr 解析 SGR 参数。
func (t *Terminal) sgr(params []int) {
	if len(params) == 0 {
		t.attr = DefaultAttr
		return
	}
	a := t.attr
	for i := 0; i < len(params); i++ {
		v := params[i]
		if v < 0 {
			v = 0
		}
		switch {
		case v == 0:
			a = DefaultAttr
		case v == 1:
			a.Bold = true
		case v == 2:
			a.Dim = true
		case v == 3:
			a.Italic = true
		case v == 4:
			a.Underline = true
		case v == 5:
			a.Blink = true
		case v == 7:
			a.Reverse = true
		case v == 8:
			a.Hidden = true
		case v == 22:
			a.Bold, a.Dim = false, false
		case v == 23:
			a.Italic = false
		case v == 24:
			a.Underline = false
		case v == 25:
			a.Blink = false
		case v == 27:
			a.Reverse = false
		case v == 28:
			a.Hidden = false
		case v >= 30 && v <= 37:
			a.FG = Color(v - 30)
		case v == 38:
			c, next := parseExtColor(params, i)
			a.FG = c
			i = next
		case v == 39:
			a.FG = DefaultColor
		case v >= 40 && v <= 47:
			a.BG = Color(v - 40)
		case v == 48:
			c, next := parseExtColor(params, i)
			a.BG = c
			i = next
		case v == 49:
			a.BG = DefaultColor
		case v >= 90 && v <= 97:
			a.FG = Color(v - 90 + 8)
		case v >= 100 && v <= 107:
			a.BG = Color(v - 100 + 8)
		}
	}
	t.attr = a
}

// parseExtColor 解析 38/48 之后的扩展颜色，返回新下标。
func parseExtColor(params []int, i int) (Color, int) {
	if i+1 >= len(params) {
		return DefaultColor, i
	}
	switch params[i+1] {
	case 5: // 256 色
		if i+2 < len(params) && params[i+2] >= 0 && params[i+2] <= 255 {
			return Color(params[i+2]), i + 2
		}
		return DefaultColor, i + 1
	case 2: // TrueColor
		if i+4 < len(params) {
			r, g, b := params[i+2], params[i+3], params[i+4]
			if r < 0 {
				r = 0
			}
			if g < 0 {
				g = 0
			}
			if b < 0 {
				b = 0
			}
			return RGBColor(uint8(clamp(r, 0, 255)), uint8(clamp(g, 0, 255)), uint8(clamp(b, 0, 255))), i + 4
		}
		return DefaultColor, len(params)
	}
	return DefaultColor, i
}

// parseParams 解析 CSI 参数：';' 与 ':' 都作为参数分隔符，缺省为 -1。
func parseParams(s string) []int {
	out := make([]int, 0, 4)
	cur := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v := int(c - '0')
			if cur < 0 {
				cur = 0
			}
			cur = cur*10 + v
			if cur > 1<<24 {
				cur = 1 << 24
			}
		case c == ';' || c == ':':
			out = append(out, cur)
			cur = -1
		default:
			// 中间字节等：忽略
		}
	}
	out = append(out, cur)
	return out
}

// paramAt 取第 i 个参数，缺省或非法时返回 def。
func paramAt(params []int, i, def int) int {
	if i < len(params) && params[i] >= 0 {
		return params[i]
	}
	return def
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
