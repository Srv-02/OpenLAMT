package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// newText returns a canvas.Text in the given design size and color.
func newText(s string, size float32, c color.Color, bold bool) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Bold: bold}
	return t
}

// ellipsize shortens s with "…" so that it fits width at the given size and style.
func ellipsize(s string, size float32, st fyne.TextStyle, width float32) string {
	if width <= 0 {
		return ""
	}
	if fyne.MeasureText(s, size, st).Width <= width {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r) // largest n with r[:n]+"…" fitting
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fyne.MeasureText(string(r[:mid])+"…", size, st).Width <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return "…"
	}
	return string(r[:lo]) + "…"
}

// fitText sets t to full, shortened to width.
func fitText(t *canvas.Text, full string, width float32) {
	short := ellipsize(full, t.TextSize, t.TextStyle, width)
	if t.Text != short {
		t.Text = short
		t.Refresh()
	}
}
