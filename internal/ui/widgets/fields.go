package widgets

import (
	"image/color"
	"strings"

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
)

// fieldTheme is the theme of an entry inside a framed field (filter field, password field):
// no border of its own, the frame is the border, and a small inner padding so the entry fits
// the field height of the design.
type fieldTheme struct{ style.CardTheme }

func (t fieldTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameInnerPadding {
		return 4
	}
	return t.CardTheme.Size(name)
}

// FieldEntry is the entry of a FilterField or PasswordField. Every key goes to the field's
// KeyFilter first (a dialog closes on Esc); keys it does not handle reach the entry.
type FieldEntry struct {
	widget.Entry
	filter func() func(*fyne.KeyEvent) bool
}

func newFieldEntry() *FieldEntry {
	e := &FieldEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *FieldEntry) TypedKey(k *fyne.KeyEvent) {
	if e.filter != nil {
		if f := e.filter(); f != nil && f(k) {
			return
		}
	}
	e.Entry.TypedKey(k)
}

// Geometry of the filter field (WindowB.dc.html): 200 x 30, radius 8, 1 px border, 10 px
// side padding, a 14 px magnifier with stroke 2, and the round clear button: a 16 px circle with
// a 9 px white cross of stroke 4.
const (
	filterWidth     float32 = 200
	filterHeight    float32 = 30
	filterRadius    float32 = 8
	filterPadH      float32 = 10
	filterIconSize  float32 = 14
	clearButtonSize float32 = 16
	clearCrossSize  float32 = 9
	clearCrossWidth float32 = 4 // on the 24 px grid of the cross
)

// FilterField is the filter of the log pages: a magnifier, an entry with the placeholder
// "Filter" and a round clear button that shows only while the field has text.
type FilterField struct {
	widget.BaseWidget

	// Entry is the text entry; set its OnChanged through the field's OnChanged.
	Entry *FieldEntry
	// OnChanged is called with the text after every change, also after the clear button.
	OnChanged func(string)
	// KeyFilter, if set, sees every key typed into the entry first; true means handled.
	KeyFilter func(*fyne.KeyEvent) bool

	clear   *clearButton
	content fyne.CanvasObject
}

// NewFilterField returns an empty filter field.
func NewFilterField(changed func(string)) *FilterField {
	f := &FilterField{Entry: newFieldEntry(), OnChanged: changed}
	f.Entry.filter = func() func(*fyne.KeyEvent) bool { return f.KeyFilter }
	f.Entry.SetPlaceHolder(lang.L("Filter"))
	f.clear = newClearButton(func() {
		f.Entry.SetText("")
		if c := fyne.CurrentApp().Driver().CanvasForObject(f.Entry); c != nil {
			c.Focus(f.Entry)
		}
	})
	f.clear.Hide()
	f.Entry.OnChanged = func(s string) {
		f.syncClear()
		if f.OnChanged != nil {
			f.OnChanged(s)
		}
	}
	bg := style.BindRect(&canvas.Rectangle{CornerRadius: filterRadius, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder)
	bg.SetMinSize(fyne.NewSize(filterWidth, filterHeight))
	icon := NewSymbol(icons.GetStroke(icons.Search, icons.Secondary, 2), filterIconSize)
	lead := container.New(layout.NewCustomPaddedLayout(0, 0, filterPadH, 0), container.NewCenter(icon))
	right := container.New(layout.NewCustomPaddedLayout(0, 0, 0, filterPadH-3), container.NewCenter(f.clear))
	entry := container.New(middleLayout{}, container.NewThemeOverride(f.Entry, fieldTheme{}))
	f.content = container.NewStack(bg, container.NewBorder(nil, nil, lead, right, entry))
	f.ExtendBaseWidget(f)
	return f
}

// Text returns the filter text.
func (f *FilterField) Text() string { return f.Entry.Text }

// SetText sets the filter text.
func (f *FilterField) SetText(s string) {
	f.Entry.SetText(s)
	f.syncClear()
}

func (f *FilterField) syncClear() { setVisible(f.clear, f.Entry.Text != "") }

func (f *FilterField) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(f.content)
}

// middleLayout gives its objects the full width and their own height, in the vertical middle:
// Fyne draws an entry's text at the top of the entry, so a taller entry looks too high.
type middleLayout struct{}

func (middleLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		h := min(size.Height, o.MinSize().Height)
		o.Move(fyne.NewPos(0, (size.Height-h)/2))
		o.Resize(fyne.NewSize(size.Width, h))
	}
}

func (middleLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		s = s.Max(o.MinSize())
	}
	return s
}

// clearButton is the round gray clear button of the filter field.
type clearButton struct {
	widget.BaseWidget
	OnTapped func()
}

var (
	_ fyne.Tappable      = (*clearButton)(nil)
	_ desktop.Cursorable = (*clearButton)(nil)
)

func newClearButton(tapped func()) *clearButton {
	b := &clearButton{OnTapped: tapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *clearButton) Tapped(*fyne.PointEvent) {
	if b.OnTapped != nil {
		b.OnTapped()
	}
}
func (b *clearButton) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (b *clearButton) CreateRenderer() fyne.WidgetRenderer {
	stroke := clearCrossWidth * clearCrossSize / 24
	r := &clearButtonRenderer{circle: &canvas.Circle{},
		l1: &canvas.Line{StrokeWidth: stroke}, l2: &canvas.Line{StrokeWidth: stroke}}
	r.Refresh()
	return r
}

type clearButtonRenderer struct {
	circle *canvas.Circle
	l1, l2 *canvas.Line
}

func (r *clearButtonRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(clearButtonSize) }

func (r *clearButtonRenderer) Layout(size fyne.Size) {
	x0, y0 := (size.Width-clearButtonSize)/2, (size.Height-clearButtonSize)/2
	r.circle.Move(fyne.NewPos(x0, y0))
	r.circle.Resize(fyne.NewSquareSize(clearButtonSize))
	// the cross of the mockup: M6 6l12 12M18 6L6 18 on a 24 grid, drawn 9 px wide
	o := (clearButtonSize - clearCrossSize) / 2
	a, b := o+6*clearCrossSize/24, o+18*clearCrossSize/24
	r.l1.Position1, r.l1.Position2 = fyne.NewPos(x0+a, y0+a), fyne.NewPos(x0+b, y0+b)
	r.l2.Position1, r.l2.Position2 = fyne.NewPos(x0+b, y0+a), fyne.NewPos(x0+a, y0+b)
}

func (r *clearButtonRenderer) Refresh() {
	r.circle.FillColor = style.Color(style.ColorNameFilterClear)
	r.circle.Refresh()
	for _, l := range []*canvas.Line{r.l1, r.l2} {
		l.StrokeColor = style.Color(style.ColorNameWhite)
		l.Refresh()
	}
}

func (r *clearButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.circle, r.l1, r.l2}
}
func (r *clearButtonRenderer) Destroy() {}

// Geometry of the password fields (Forms.dc.html, Danger.dc.html .pw/.eye): radius 7, 1 px
// border, 9 px left and 2 px right padding, a 28 px eye button (radius 6) with a 16 px symbol.
// The read-only display of a generated password ("New password not saved") uses 14 px
// monospace text, 10 px left and 4 px right padding and a 30 px button with an 18 px symbol.
const (
	pwRadius     float32 = 7
	pwPadL       float32 = 9
	pwPadR       float32 = 2
	pwEye        float32 = 28
	pwEyeIcon    float32 = 16
	pwShowPadL   float32 = 10
	pwShowPadR   float32 = 4
	pwShowEye    float32 = 30
	pwShowIcon   float32 = 18
	pwShowText   float32 = 14
	pwShowPadV   float32 = 8
	pwMaskRune           = "•"
	eyeButtonRad float32 = 6
)

// PasswordField is a password entry, hidden by default, with an eye button inside the field
// on the right that shows and hides the text. NewPasswordDisplay builds the read-only variant
// for a password the user has to copy (monospace, no entry).
type PasswordField struct {
	widget.BaseWidget

	// Entry is the text entry; nil for the read-only display.
	Entry *FieldEntry
	// KeyFilter, if set, sees every key typed into the entry first; true means handled.
	KeyFilter func(*fyne.KeyEvent) bool

	eye      *eyeButton
	text     *canvas.Text // read-only display
	value    string       // read-only display
	revealed bool
	content  fyne.CanvasObject
}

// NewPasswordField returns an empty, hidden password entry. Set OnChanged and OnSubmitted on
// its Entry.
func NewPasswordField() *PasswordField {
	p := &PasswordField{Entry: newFieldEntry()}
	p.Entry.filter = func() func(*fyne.KeyEvent) bool { return p.KeyFilter }
	p.Entry.Password = true
	p.Entry.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	p.eye = newEyeButton(pwEye, pwEyeIcon, p.toggle)
	p.Entry.ActionItem = p.eye
	bg := style.BindRect(&canvas.Rectangle{CornerRadius: pwRadius, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder)
	inner := container.New(layout.NewCustomPaddedLayout(0, 0, pwPadL-4, pwPadR), container.NewThemeOverride(p.Entry, fieldTheme{}))
	p.content = container.NewStack(bg, inner)
	p.ExtendBaseWidget(p)
	return p
}

// NewPasswordDisplay returns the read-only, hidden display of password.
func NewPasswordDisplay(password string) *PasswordField {
	p := &PasswordField{value: password}
	p.text = canvas.NewText("", style.Color("foreground"))
	p.text.TextSize = pwShowText
	p.text.TextStyle = fyne.TextStyle{Monospace: true}
	p.eye = newEyeButton(pwShowEye, pwShowIcon, p.toggle)
	bg := style.BindRect(&canvas.Rectangle{CornerRadius: pwRadius, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder)
	row := container.NewBorder(nil, nil, nil, container.NewCenter(p.eye),
		container.New(layout.NewCustomPaddedLayout(pwShowPadV, pwShowPadV, 0, 0), p.text))
	p.content = container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(0, 0, pwShowPadL, pwShowPadR), row))
	p.sync()
	p.ExtendBaseWidget(p)
	return p
}

// Text returns the password.
func (p *PasswordField) Text() string {
	if p.Entry != nil {
		return p.Entry.Text
	}
	return p.value
}

// SetText sets the password.
func (p *PasswordField) SetText(s string) {
	if p.Entry != nil {
		p.Entry.SetText(s)
		return
	}
	p.value = s
	p.sync()
}

// Revealed reports whether the password is shown in clear text.
func (p *PasswordField) Revealed() bool { return p.revealed }

// SetRevealed shows (true) or hides the password.
func (p *PasswordField) SetRevealed(on bool) {
	p.revealed = on
	p.sync()
}

func (p *PasswordField) toggle() { p.SetRevealed(!p.revealed) }

func (p *PasswordField) sync() {
	if p.Entry != nil {
		p.Entry.Password = !p.revealed
		p.Entry.Refresh()
		return
	}
	p.text.Text = p.value
	if !p.revealed {
		p.text.Text = strings.Repeat(pwMaskRune, len([]rune(p.value)))
	}
	p.text.Color = style.Color("foreground")
	p.text.Refresh()
}

func (p *PasswordField) Refresh() {
	if p.text != nil {
		p.text.Color = style.Color("foreground")
	}
	p.BaseWidget.Refresh()
}

func (p *PasswordField) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(p.content)
}

// eyeButton is the transparent show/hide button of a password field.
type eyeButton struct {
	widget.BaseWidget
	OnTapped func()

	size    float32
	icon    *Symbol
	hovered bool
}

var (
	_ fyne.Tappable      = (*eyeButton)(nil)
	_ desktop.Hoverable  = (*eyeButton)(nil)
	_ desktop.Cursorable = (*eyeButton)(nil)
)

func newEyeButton(size, iconSize float32, tapped func()) *eyeButton {
	b := &eyeButton{OnTapped: tapped, size: size, icon: NewSymbol(icons.Get(icons.Eye, icons.Secondary), iconSize)}
	b.ExtendBaseWidget(b)
	return b
}

func (b *eyeButton) Tapped(*fyne.PointEvent) {
	if b.OnTapped != nil {
		b.OnTapped()
	}
}
func (b *eyeButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.Refresh() }
func (b *eyeButton) MouseMoved(*desktop.MouseEvent) {}
func (b *eyeButton) MouseOut()                      { b.hovered = false; b.Refresh() }
func (b *eyeButton) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (b *eyeButton) MinSize() fyne.Size             { return fyne.NewSquareSize(b.size) }

func (b *eyeButton) CreateRenderer() fyne.WidgetRenderer {
	r := &eyeButtonRenderer{b: b, bg: &canvas.Rectangle{CornerRadius: eyeButtonRad}}
	r.Refresh()
	return r
}

type eyeButtonRenderer struct {
	b  *eyeButton
	bg *canvas.Rectangle
}

func (r *eyeButtonRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(r.b.size) }
func (r *eyeButtonRenderer) Layout(size fyne.Size) {
	r.bg.Move(fyne.NewPos(0, 0))
	r.bg.Resize(size)
	is := r.b.icon.MinSize()
	r.b.icon.Move(fyne.NewPos((size.Width-is.Width)/2, (size.Height-is.Height)/2))
	r.b.icon.Resize(is)
}
func (r *eyeButtonRenderer) Refresh() {
	r.bg.FillColor = color.Transparent
	if r.b.hovered {
		r.bg.FillColor = style.Color("hover")
	}
	r.bg.Refresh()
	r.b.icon.Refresh()
}
func (r *eyeButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.b.icon}
}
func (r *eyeButtonRenderer) Destroy() {}
