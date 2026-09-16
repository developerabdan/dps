// Package table turns containers into a width-fitted plain table. It is the
// path used whenever stdout is not a terminal, so it emits no colour unless
// told to and never depends on the TUI.
package table

// TruncMode says which end of a value is expendable.
type TruncMode int

const (
	// TruncTail keeps the head: the default for values that read left to right.
	TruncTail TruncMode = iota
	// TruncMid keeps both ends, for names whose prefix and suffix both matter.
	TruncMid
	// TruncHead keeps the tail, for images where the tag is the point.
	TruncHead
)

const ellipsis = '…'

// Truncate shortens s to w display cells. It counts runes, not bytes: the
// glyphs dps prints (●, ✗, ◐, →) are multibyte, and byte slicing would both
// miscount the width and split them into garbage.
func Truncate(s string, w int, mode TruncMode) string {
	r := []rune(s)
	if w <= 0 {
		return ""
	}
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return string(ellipsis)
	}

	switch mode {
	case TruncHead:
		return string(ellipsis) + string(r[len(r)-(w-1):])
	case TruncMid:
		head := (w - 1 + 1) / 2
		tail := w - 1 - head
		out := make([]rune, 0, w)
		out = append(out, r[:head]...)
		out = append(out, ellipsis)
		if tail > 0 {
			out = append(out, r[len(r)-tail:]...)
		}
		return string(out)
	default:
		return string(r[:w-1]) + string(ellipsis)
	}
}

// Pad right-pads s to w cells, counting runes.
func Pad(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	out := make([]byte, 0, len(s)+(w-n))
	out = append(out, s...)
	for i := 0; i < w-n; i++ {
		out = append(out, ' ')
	}
	return string(out)
}

// Width reports the display width of s in cells.
func Width(s string) int { return len([]rune(s)) }
