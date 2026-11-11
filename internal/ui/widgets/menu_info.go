package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of the info line of a menu (Extras.dc.html .minfo): 12 px text with 8 px above, 6
// below and 10 at the sides, 8 between name and detail; the progress bar is 4 px high (radius
// 2) with 2 px above and 8 below.
const (
	menuInfoPadTop      float32 = 8
	menuInfoPadBottom   float32 = 6
	menuInfoPadH        float32 = 10
	menuInfoGap         float32 = 8
	menuInfoMinName     float32 = 40 // least width of the name before it ends in "…"
	menuBarHeight       float32 = 4
	menuBarRadius       float32 = 2
	menuBarMarginTop    float32 = 2
	menuBarMarginBottom float32 = 8
)

// MenuInfo is the line above the items of a MenuPopup (Extras.dc.html, Media menu): a name in
// semibold primary text at the left, a detail in secondary text at the right, and optionally a
// thin progress bar in the accent color below them. It is no item: the keyboard skips it and
// taps on it do nothing. A long name ends in "…"; the line never widens the menu for it.
type MenuInfo struct {
	widget.BaseWidget
	name, detail string
	progress     float32
	bar          bool
}

// NewMenuInfo returns an info line without a progress bar.
func NewMenuInfo(name, detail string) *MenuInfo {
	i := &MenuInfo{name: name, detail: detail}
	i.ExtendBaseWidget(i)
	return i
}

// Name returns the name.
func (i *MenuInfo) Name() string { return i.name }

// SetName changes the name.
func (i *MenuInfo) SetName(s string) {
	if i.name != s {
		i.name = s
		i.Refresh()
	}
}

// Detail returns the detail.
func (i *MenuInfo) Detail() string { return i.detail }

// SetDetail changes the detail ("" for none).
func (i *MenuInfo) SetDetail(s string) {
	if i.detail != s {
		i.detail = s
		i.Refresh()
	}
}

// SetProgress shows the progress bar filled to v (0 to 1).
func (i *MenuInfo) SetProgress(v float32) {
	v = min(max(v, 0), 1)
	if !i.bar || i.progress != v {
		i.bar, i.progress = true, v
		i.Refresh()
	}
}

// Progress returns the value of the bar and whether it is shown.
func (i *MenuInfo) Progress() (float32, bool) { return i.progress, i.bar }

func (i *MenuInfo) CreateRenderer() fyne.WidgetRenderer {
	size := style.Size(style.SizeNameSmallText)
	r := &menuInfoRenderer{i: i,
		name:   newText("", size, nil, true),
		detail: newText("", size, nil, false),
		track:  &canvas.Rectangle{CornerRadius: menuBarRadius},
		fill:   &canvas.Rectangle{CornerRadius: menuBarRadius},
	}
	r.Refresh()
	return r
}

type menuInfoRenderer struct {
	i            *MenuInfo
	name, detail *canvas.Text
	track, fill  *canvas.Rectangle
}

func (r *menuInfoRenderer) textHeight() float32 {
	return fyne.MeasureText("Mg", r.name.TextSize, r.name.TextStyle).Height
}

func (r *menuInfoRenderer) MinSize() fyne.Size {
	dw := float32(0)
	if r.i.detail != "" {
		dw = fyne.MeasureText(r.i.detail, r.detail.TextSize, r.detail.TextStyle).Width + menuInfoGap
	}
	h := menuInfoPadTop + r.textHeight() + menuInfoPadBottom
	if r.i.bar {
		h += menuBarMarginTop + menuBarHeight + menuBarMarginBottom - menuInfoPadBottom
	}
	return fyne.NewSize(2*menuInfoPadH+menuInfoMinName+dw, h)
}

func (r *menuInfoRenderer) Layout(size fyne.Size) {
	th := r.textHeight()
	ds := r.detail.MinSize()
	right := size.Width - menuInfoPadH
	r.detail.Move(fyne.NewPos(right-ds.Width, menuInfoPadTop))
	r.detail.Resize(ds)
	nameWidth := right - menuInfoPadH
	if r.i.detail != "" {
		nameWidth -= ds.Width + menuInfoGap
	}
	fitText(r.name, r.i.name, nameWidth)
	r.name.Move(fyne.NewPos(menuInfoPadH, menuInfoPadTop))
	r.name.Resize(fyne.NewSize(max(0, nameWidth), th))
	y := menuInfoPadTop + th + menuBarMarginTop
	w := max(0, size.Width-2*menuInfoPadH)
	r.track.Move(fyne.NewPos(menuInfoPadH, y))
	r.track.Resize(fyne.NewSize(w, menuBarHeight))
	r.fill.Move(fyne.NewPos(menuInfoPadH, y))
	r.fill.Resize(fyne.NewSize(w*r.i.progress, menuBarHeight))
}

func (r *menuInfoRenderer) Refresh() {
	r.name.Color = style.Color(theme.ColorNameForeground)
	r.detail.Color = style.Color(style.ColorNameTextSecondary)
	r.detail.Text = r.i.detail
	r.track.FillColor = style.Color(style.ColorNameGroupDivider)
	r.fill.FillColor = style.Color(theme.ColorNamePrimary)
	setVisible(r.track, r.i.bar)
	setVisible(r.fill, r.i.bar)
	r.Layout(r.i.Size())
	for _, o := range r.Objects() {
		o.Refresh()
	}
}

func (r *menuInfoRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill, r.name, r.detail}
}

func (r *menuInfoRenderer) Destroy() {}
