package ui

import (
	"os"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// shotScale is the screen scale of the screenshot tests: $OPENLAMT_SHOT_SCALE, default 1.
func shotScale() float32 {
	if f, err := strconv.ParseFloat(os.Getenv("OPENLAMT_SHOT_SCALE"), 32); err == nil && f > 0 {
		return float32(f)
	}
	return 1
}

// scaleWindow sets the screenshot scale on a test window.
func scaleWindow(w fyne.Window) {
	if c, ok := w.Canvas().(test.WindowlessCanvas); ok {
		c.SetScale(shotScale())
	}
}
