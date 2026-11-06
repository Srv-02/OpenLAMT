package widgets

import (
	"image/color"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Link is the text link of the About and settings dialogs. widget.Hyperlink knows one theme
// color and no symbol, but the design colors each link on its own (blue with a hover color,
// white in the credits and the footer) and puts a symbol next to some. A tap runs OnTapped, or
// opens URL in the browser.
type Link struct {
	widget.BaseWidget

	Text string
	URL  *url.URL
	// OnTapped, if set, replaces opening URL.
	OnTapped func()

	// Color names the text color token (default "foreground"). HoverColor, if set, replaces it
	// under the pointer; Hover colors only where the design shows them. Tokens, not colors,
	// so the link follows a change of the theme variant.
	Color, HoverColor fyne.ThemeColorName
	TextSize          float32 // default 13
	Bold              bool

	// Icon is shown next to the text (after it, or before with IconLeading); HoverIcon
	// replaces it under the pointer. IconSize defaults to 14, IconGap to 5.
	Icon, HoverIcon fyne.Resource
	IconSize        float32
	IconGap         float32
	IconLeading     bool

	// Height and PadH make the link a flat button (28 px, 10 px side padding in the footer
	// of the settings dialog); 0 sizes it to its content.
	Height, PadH float32

	hovered, focused bool
}

var (
	_ fyne.Tappable      = (*Link)(nil)
	_ fyne.Focusable     = (*Link)(nil)
	_ desktop.Hoverable  = (*Link)(nil)
	_ desktop.Cursorable = (*Link)(nil)
)

// NewLink returns a link with text that opens u.
func NewLink(text string, u *url.URL) *Link {
	l := &Link{Text: text, URL: u}
	l.ExtendBaseWidget(l)
	return l
}

func (l *Link) Tapped(*fyne.PointEvent) {
	switch {
	case l.OnTapped != nil:
		l.OnTapped()
	case l.URL != nil:
		if a := fyne.CurrentApp(); a != nil {
			if err := a.OpenURL(l.URL); err != nil {
				fyne.LogError("open link", err)
			}
		}
	}
}

func (l *Link) FocusGained() { l.focused = true; l.Refresh() }
func (l *Link) FocusLost()   { l.focused = false; l.Refresh() }
func (l *Link) TypedRune(r rune) {
	if r == ' ' {
		l.Tapped(nil)
	}
}
func (l *Link) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyReturn || e.Name == fyne.KeyEnter {
		l.Tapped(nil)
	}
}

func (l *Link) MouseIn(*desktop.MouseEvent)    { l.hovered = true; l.Refresh() }
func (l *Link) MouseMoved(*desktop.MouseEvent) {}
func (l *Link) MouseOut()                      { l.hovered = false; l.Refresh() }
func (l *Link) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (l *Link) textSize() float32 {
	if l.TextSize != 0 {
		return l.TextSize
	}
	return style.Size("text")
}

func (l *Link) iconSize() float32 {
	if l.IconSize != 0 {
		return l.IconSize
	}
	return 14
}

func (l *Link) iconGap() float32 {
	if l.IconGap != 0 {
		return l.IconGap
	}
	return 5
}

func (l *Link) CreateRenderer() fyne.WidgetRenderer {
	r := &linkRenderer{l: l, text: newText("", l.textSize(), nil, false), icon: NewSymbol(nil, 14),
		focus: &canvas.Rectangle{CornerRadius: 4, StrokeWidth: 2, FillColor: color.Transparent}}
	r.Refresh()
	return r
}

type linkRenderer struct {
	l     *Link
	text  *canvas.Text
	icon  *Symbol
	focus *canvas.Rectangle // ring while the link has the keyboard focus
}

func (r *linkRenderer) style() fyne.TextStyle { return fyne.TextStyle{Bold: r.l.Bold} }

func (r *linkRenderer) MinSize() fyne.Size {
	l := r.l
	ts := fyne.MeasureText(l.Text, l.textSize(), r.style())
	w := ts.Width + 2*l.PadH
	if l.Icon != nil {
		w += l.iconGap() + l.iconSize()
	}
	return fyne.NewSize(w, max(l.Height, ts.Height, l.iconSize()))
}

func (r *linkRenderer) Layout(size fyne.Size) {
	l := r.l
	r.focus.Move(fyne.NewPos(0, 0))
	r.focus.Resize(size)
	ts := fyne.MeasureText(l.Text, l.textSize(), r.style())
	x := l.PadH
	iconX := float32(-1)
	if l.Icon != nil && l.IconLeading {
		iconX = x
		x += l.iconSize() + l.iconGap()
	}
	r.text.Move(fyne.NewPos(x, (size.Height-ts.Height)/2))
	r.text.Resize(ts)
	if l.Icon != nil && !l.IconLeading {
		iconX = x + ts.Width + l.iconGap()
	}
	r.icon.Move(fyne.NewPos(iconX, (size.Height-l.iconSize())/2))
	r.icon.Resize(fyne.NewSquareSize(l.iconSize()))
}

func (r *linkRenderer) Refresh() {
	l := r.l
	c := style.Color("foreground")
	if l.Color != "" {
		c = style.Color(l.Color)
	}
	icon := l.Icon
	if l.hovered {
		if l.HoverColor != "" {
			c = style.Color(l.HoverColor)
		}
		if l.HoverIcon != nil {
			icon = l.HoverIcon
		}
	}
	r.text.Text, r.text.Color, r.text.TextSize, r.text.TextStyle = l.Text, c, l.textSize(), r.style()
	r.text.Refresh()
	r.focus.StrokeColor = style.Color("focus")
	r.focus.Hidden = !l.focused
	r.focus.Refresh()
	r.icon.min = fyne.NewSquareSize(l.iconSize())
	r.icon.SetResource(icon)
	setVisible(r.icon, icon != nil)
	r.Layout(l.Size())
	canvas.Refresh(l)
}

func (r *linkRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.text, r.icon, r.focus}
}
func (r *linkRenderer) Destroy() {}
