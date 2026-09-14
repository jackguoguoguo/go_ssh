package vt

import "strconv"

// Color 表示终端颜色：
//   - c < 0            默认色（不输出 SGR 参数）
//   - 0..255           256 色索引
//   - >= 0x01000000    TrueColor，RGB 分别为 (c>>16)&0xFF、(c>>8)&0xFF、c&0xFF
type Color int32

// DefaultColor 表示终端默认颜色（不输出 SGR）。
const DefaultColor Color = -1

// trueColorFlag 是 TrueColor 的标记位。
const trueColorFlag Color = 0x01000000

// RGBColor 由 r/g/b 构造一个 TrueColor。
func RGBColor(r, g, b uint8) Color {
	return trueColorFlag | Color(r)<<16 | Color(g)<<8 | Color(b)
}

// isTrue 是否为 TrueColor。
func (c Color) isTrue() bool { return c >= trueColorFlag }

// rgb 返回 TrueColor 的三个分量。
func (c Color) rgb() (r, g, b uint8) {
	return uint8((c >> 16) & 0xFF), uint8((c >> 8) & 0xFF), uint8(c & 0xFF)
}

// Attr 是一组字符属性（前景/背景色 + 若干开关）。
type Attr struct {
	FG, BG    Color
	Bold      bool
	Dim       bool
	Italic    bool
	Underline bool
	Blink     bool
	Reverse   bool
	Hidden    bool
}

// DefaultAttr 是「默认属性」：前景/背景都是默认色，无任何开关。
// 注意：Attr 的零值 FG/BG 为 0（黑色），渲染出来是黑底黑字，
// 因此初始化单元格时请使用 DefaultAttr。
var DefaultAttr = Attr{FG: DefaultColor, BG: DefaultColor}

// SGR 返回把终端从默认属性切换到 a 所需的 ANSI 串，形如 "\x1b[0;1;31;44m"。
// 若 a 就是默认属性，返回 "\x1b[0m"。
func (a Attr) SGR() string {
	if a == DefaultAttr {
		return "\x1b[0m"
	}
	var b []byte
	b = append(b, '\x1b', '[', '0')
	if a.Bold {
		b = append(b, ';', '1')
	}
	if a.Dim {
		b = append(b, ';', '2')
	}
	if a.Italic {
		b = append(b, ';', '3')
	}
	if a.Underline {
		b = append(b, ';', '4')
	}
	if a.Blink {
		b = append(b, ';', '5')
	}
	if a.Reverse {
		b = append(b, ';', '7')
	}
	if a.Hidden {
		b = append(b, ';', '8')
	}
	b = appendColor(b, a.FG, 30)
	b = appendColor(b, a.BG, 40)
	b = append(b, 'm')
	return string(b)
}

// appendColor 把颜色参数追加到 b，base 为 30（前景）或 40（背景）。
func appendColor(b []byte, c Color, base int) []byte {
	switch {
	case c < 0:
		return b
	case c.isTrue(): // 38;2;r;g;b / 48;2;r;g;b
		r, g, bl := c.rgb()
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(base)+8, 10)
		b = append(b, ';', '2', ';')
		b = strconv.AppendInt(b, int64(r), 10)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(g), 10)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(bl), 10)
	case c < 8: // 基础色：30-37 / 40-47
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(base)+int64(c), 10)
	case c < 16: // 高亮色：90-97 / 100-107
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(base)+60+int64(c-8), 10)
	default: // 256 色：38;5;n / 48;5;n
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(base)+8, 10)
		b = append(b, ';', '5', ';')
		b = strconv.AppendInt(b, int64(c), 10)
	}
	return b
}
