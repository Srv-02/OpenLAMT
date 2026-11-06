package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// managerButton is a flat button of the manager window (ManagerA.html): the 34 x 28 icon
// buttons of the detail toolbar (Edit, Duplicate, Delete, Settings) and the full-width
// "New Connection" row of the sidebar. Transparent until hovered; active marks a toggled
// button (Edit while editing); disabled buttons draw the symbol in the disabled color.
type managerButton struct {
	widget.DisableableWidget

	OnTapped func()

	icon     icons.Name
	iconSize float32
	text     string
	size     fyne.Size // minimum size; the width grows with the text
	active   bool
	hovered  bool
	focused  bool
}

var (
	_ fyne.Tappable     = (*managerButton)(nil)
	_ fyne.Focusable    = (*managerButton)(nil)
	_ desktop.Hoverable = (*managerButton)(nil)
)

// newManagerIconButton returns a 34 x 28 button with an 18 px symbol.
func newManagerIconButton(icon icons.Name, fn func()) *managerButton {
	b := &managerButton{OnTapped: fn, icon: icon, iconSize: 18, size: fyne.NewSize(style.IconButtonWidth, style.IconButtonHeight)}
	b.ExtendBaseWidget(b)
	return b
}

// newManagerTextButton returns a 32 high row button with a 16 px symbol and a text.
func newManagerTextButton(icon icons.Name, text string, fn func()) *managerButton {
	b := &managerButton{OnTapped: fn, icon: icon, iconSize: 16, text: text, size: fyne.NewSize(0, style.NewConnectionRowHeight)}
	b.ExtendBaseWidget(b)
	return b
}

func (b *managerButton) SetActive(on bool) { b.active = on; b.Refresh() }

func (b *managerButton) Tapped(*fyne.PointEvent) {
	if b.Disabled() || b.OnTapped == nil {
		return
	}
	b.OnTapped()
}

func (b *managerButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.Refresh() }
func (b *managerButton) MouseMoved(*desktop.MouseEvent) {}
func (b *managerButton) MouseOut()                      { b.hovered = false; b.Refresh() }
func (b *managerButton) FocusGained()                   { b.focused = true; b.Refresh() }
func (b *managerButton) FocusLost()                     { b.focused = false; b.Refresh() }
func (b *managerButton) TypedRune(r rune) {
	if r == ' ' {
		b.Tapped(nil)
	}
}
func (b *managerButton) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyReturn || e.Name == fyne.KeyEnter {
		b.Tapped(nil)
	}
}

func (b *managerButton) CreateRenderer() fyne.WidgetRenderer {
	r := &managerButtonRenderer{b: b,
		bg:   &canvas.Rectangle{CornerRadius: style.IconButtonRadius},
		icon: widgets.NewSymbol(nil, 0),
		text: canvas.NewText(b.text, style.Color(style.ColorNameIconMuted)),
	}
	if b.text != "" {
		r.bg.CornerRadius = style.SidebarRowRadius
		r.text.TextStyle.Bold = true // design: weight 500; Inter SemiBold is the closest bundled face
	}
	r.Refresh()
	return r
}

type managerButtonRenderer struct {
	b    *managerButton
	bg   *canvas.Rectangle
	icon *widgets.Symbol
	text *canvas.Text
}

const managerButtonPad float32 = 10 // left padding and gap of the text button

func (r *managerButtonRenderer) MinSize() fyne.Size {
	s := r.b.size
	if r.b.text != "" {
		s.Width = max(s.Width, 2*managerButtonPad+r.b.iconSize+8+r.text.MinSize().Width)
	}
	return s
}

func (r *managerButtonRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	is := r.b.iconSize
	if r.b.text == "" {
		r.icon.Move(fyne.NewPos((size.Width-is)/2, (size.Height-is)/2))
	} else {
		r.icon.Move(fyne.NewPos(managerButtonPad, (size.Height-is)/2))
		ts := r.text.MinSize()
		r.text.Move(fyne.NewPos(managerButtonPad+is+8, (size.Height-ts.Height)/2))
		r.text.Resize(ts)
	}
	r.icon.Resize(fyne.NewSquareSize(is))
}

func (r *managerButtonRenderer) Refresh() {
	b := r.b
	var bg color.Color = color.Transparent
	variant := icons.Muted
	switch {
	case b.Disabled():
		variant = icons.Disabled
	case b.active:
		bg = style.Color("pressed")
		variant = icons.Accent
	case b.hovered || b.focused:
		bg = style.Color("hover")
	}
	r.bg.FillColor = bg
	r.bg.Refresh()
	r.icon.SetResource(icons.Get(b.icon, variant))
	r.text.Text = b.text
	r.text.TextSize = style.Size("text")
	r.text.Color = style.Color(style.ColorNameIconMuted)
	if b.Disabled() {
		r.text.Color = style.Color("disabled")
	}
	r.text.Refresh()
	r.Layout(b.Size())
}

func (r *managerButtonRenderer) Objects() []fyne.CanvasObject {
	if r.b.text == "" {
		return []fyne.CanvasObject{r.bg, r.icon}
	}
	return []fyne.CanvasObject{r.bg, r.icon, r.text}
}

func (r *managerButtonRenderer) Destroy() {}

// searchTheme is the theme of the sidebar search entry: no border and a smaller inner
// padding, so the entry fits the 30 px field of the design.
type searchTheme struct{ style.CardTheme }

func (t searchTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameInnerPadding {
		return 4
	}
	return t.CardTheme.Size(name)
}

// newSearchField wraps entry into the rounded search field of the sidebar (30 high, radius 8,
// rgba(255,255,255,0.07), 15 px search symbol).
func newSearchField(entry *widget.Entry) fyne.CanvasObject {
	entry.SetPlaceHolder(lang.L("Search"))
	bg := style.BindRect(&canvas.Rectangle{CornerRadius: style.SearchFieldRadius}, style.ColorNameSearchField, "")
	bg.SetMinSize(fyne.NewSize(0, style.SearchFieldHeight))
	icon := widgets.NewSymbol(icons.Get(icons.Search, icons.Secondary), 15)
	lead := container.New(layout.NewCustomPaddedLayout(0, 0, 8, 0), container.NewCenter(icon))
	// The clear button gets its own right slot (Entry.ActionItem would reserve the space
	// even when hidden). It shows only while the field has text.
	clear := newManagerIconButton(icons.Clear, nil)
	clear.iconSize = 15
	clear.size = fyne.NewSize(24, 24)
	clear.OnTapped = func() {
		entry.SetText("")
		if c := fyne.CurrentApp().Driver().CanvasForObject(entry); c != nil {
			c.Focus(entry)
		}
	}
	clear.Hidden = entry.Text == ""
	prev := entry.OnChanged
	entry.OnChanged = func(text string) {
		if text == "" {
			clear.Hide()
		} else {
			clear.Show()
		}
		if prev != nil {
			prev(text)
		}
	}
	right := container.New(layout.NewCustomPaddedLayout(0, 0, 0, 4), container.NewCenter(clear))
	return container.NewStack(bg, container.NewBorder(nil, nil, lead, right, container.NewThemeOverride(entry, searchTheme{})))
}

// sectionLabel is the "Favorites" heading of the sidebar and the card headings of the detail
// pane: 11 px semibold, #98989D.
func sectionLabel(text string) *canvas.Text {
	t := style.BindText(canvas.NewText(text, nil), style.ColorNameSectionLabel)
	t.TextSize = style.Size(style.SizeNameSectionLabel)
	t.TextStyle.Bold = true
	return t
}

// hairline is a 1 px line in the color token c (sidebar edge, toolbar and sidebar footer borders).
func hairline(c fyne.ThemeColorName) *canvas.Rectangle {
	r := style.BindRect(canvas.NewRectangle(nil), c, "")
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}
