package style

import (
	"sync"
	"weak"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// Canvas primitives (rectangle, text, circle) are not widgets, so Fyne does not ask them for
// their colors again when the theme variant changes. BindRect, BindText and BindCircle name the
// design token a primitive is drawn in; SetVariant then recolors every bound primitive that is
// still alive, wherever it sits (in a window, in a cached popup that is not shown, in a dialog
// that is not open yet). The registry holds the primitives weakly, so it keeps none alive.
//
// A primitive whose color a widget sets itself on every Refresh (hover, selection, disabled)
// is not bound: the widget reads Color in its renderer and follows the variant that way.

type rectBind struct {
	obj          weak.Pointer[canvas.Rectangle]
	fill, stroke fyne.ThemeColorName
}

type textBind struct {
	obj   weak.Pointer[canvas.Text]
	color fyne.ThemeColorName
}

type circleBind struct {
	obj          weak.Pointer[canvas.Circle]
	fill, stroke fyne.ThemeColorName
}

var (
	bindMu  sync.Mutex
	rects   []rectBind
	texts   []textBind
	circles []circleBind
	// pruneAt is the registry size that triggers the next sweep of dead entries: twice the
	// size after the last sweep, so the sweeps cost O(1) per bind on average.
	pruneAt = minPrune
)

const minPrune = 64

// BindRect colors r with the tokens fill and stroke (empty: leave that color alone) in the
// current variant and on every variant change. It returns r.
func BindRect(r *canvas.Rectangle, fill, stroke fyne.ThemeColorName) *canvas.Rectangle {
	b := rectBind{weak.Make(r), fill, stroke}
	b.apply(r)
	bindMu.Lock()
	rects = append(rects, b)
	pruneLocked()
	bindMu.Unlock()
	return r
}

// BindText colors t with the token c; see BindRect.
func BindText(t *canvas.Text, c fyne.ThemeColorName) *canvas.Text {
	b := textBind{weak.Make(t), c}
	t.Color = Color(c)
	bindMu.Lock()
	texts = append(texts, b)
	pruneLocked()
	bindMu.Unlock()
	return t
}

// BindCircle colors c with the tokens fill and stroke; see BindRect.
func BindCircle(c *canvas.Circle, fill, stroke fyne.ThemeColorName) *canvas.Circle {
	b := circleBind{weak.Make(c), fill, stroke}
	b.apply(c)
	bindMu.Lock()
	circles = append(circles, b)
	pruneLocked()
	bindMu.Unlock()
	return c
}

func (b rectBind) apply(r *canvas.Rectangle) {
	if b.fill != "" {
		r.FillColor = Color(b.fill)
	}
	if b.stroke != "" {
		r.StrokeColor = Color(b.stroke)
	}
}

func (b circleBind) apply(c *canvas.Circle) {
	if b.fill != "" {
		c.FillColor = Color(b.fill)
	}
	if b.stroke != "" {
		c.StrokeColor = Color(b.stroke)
	}
}

// recolorBound recolors the bound primitives for the current variant and drops the ones the
// garbage collector took.
func recolorBound() {
	bindMu.Lock()
	defer bindMu.Unlock()
	sweepLocked(true)
}

// pruneLocked drops the dead entries once the registry has doubled since the last sweep, so
// primitives that are bound again and again (a status line on every poll) cannot make it grow
// without bound. Live entries are never removed.
func pruneLocked() {
	if len(rects)+len(texts)+len(circles) >= pruneAt {
		sweepLocked(false)
	}
}

// sweepLocked removes the dead entries; with recolor it also applies the current variant to
// the live ones.
func sweepLocked(recolor bool) {
	rects = keep(rects, func(b rectBind) bool {
		r := b.obj.Value()
		if r != nil && recolor {
			b.apply(r)
		}
		return r != nil
	})
	texts = keep(texts, func(b textBind) bool {
		t := b.obj.Value()
		if t != nil && recolor {
			t.Color = Color(b.color)
		}
		return t != nil
	})
	circles = keep(circles, func(b circleBind) bool {
		c := b.obj.Value()
		if c != nil && recolor {
			b.apply(c)
		}
		return c != nil
	})
	pruneAt = max(minPrune, 2*(len(rects)+len(texts)+len(circles)))
}

func keep[T any](s []T, alive func(T) bool) []T {
	out := s[:0]
	for _, b := range s {
		if alive(b) {
			out = append(out, b)
		}
	}
	clear(s[len(out):])
	return out
}

// boundCount is the number of registered primitives, for tests.
func boundCount() int {
	bindMu.Lock()
	defer bindMu.Unlock()
	return len(rects) + len(texts) + len(circles)
}
