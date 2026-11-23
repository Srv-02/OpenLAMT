package ui

import (
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Scale menu of the device window (issue #26, gesamtdesign V23): Fit to Window (the default),
// Actual Size and fixed steps, Zoom In and Zoom Out, and Reverse Mouse Wheel. The scale applies
// to this window only and is not stored; full screen always fits. Reverse Mouse Wheel is stored
// with the favorite. The shortcuts work while the focus is not in the remote image (kvmView
// swallows shortcuts).

// scaleSteps are the fixed scales of the menu and of Zoom In and Zoom Out; 1 is Actual Size.
var scaleSteps = []float32{0.5, 0.75, 1, 1.5, 2}

// scaleState is the Scale menu of the device window; Fyne thread only.
type scaleState struct {
	btn     *widgets.ToolbarButton
	menu    *fyne.Menu
	popup   *widgets.MenuPopup
	choice  float32 // 0: Fit to Window
	fit     *fyne.MenuItem
	steps   map[float32]*fyne.MenuItem // Actual Size and the percentages
	zoomIn  *fyne.MenuItem
	zoomOut *fyne.MenuItem
	reverse *fyne.MenuItem
	chosen  bool // the user set Reverse Mouse Wheel before the favorite was read
	write   latestWriter
}

// Shortcuts of the menu, with the Ctrl the menu shows on every platform.
var (
	shortcutFit     = &desktop.CustomShortcut{KeyName: fyne.Key0, Modifier: fyne.KeyModifierControl}
	shortcutActual  = &desktop.CustomShortcut{KeyName: fyne.Key1, Modifier: fyne.KeyModifierControl}
	shortcutZoomIn  = &desktop.CustomShortcut{KeyName: fyne.KeyPlus, Modifier: fyne.KeyModifierControl}
	shortcutZoomOut = &desktop.CustomShortcut{KeyName: fyne.KeyMinus, Modifier: fyne.KeyModifierControl}
)

func scaleLabel(s float32) string {
	switch s {
	case 0:
		return lang.L("Fit to Window")
	case 1:
		return lang.L("Actual Size (100%)")
	}
	return percent(s)
}

func percent(s float32) string { return strconv.Itoa(int(s*100+0.5)) + "%" }

// buildScaleMenu creates the Scale button, its menu and the window shortcuts.
func (d *DeviceWindow) buildScaleMenu() {
	s := &d.scale
	s.btn = widgets.NewToolbarMenuButton(icons.Zoom, lang.L("Scale"), d.showScaleMenu)
	s.fit = fyne.NewMenuItem(scaleLabel(0), func() { d.setScale(0) })
	s.fit.Shortcut = shortcutFit
	s.steps = map[float32]*fyne.MenuItem{}
	items := []*fyne.MenuItem{s.fit}
	for _, st := range []float32{1, 0, 0.5, 0.75, 1.5, 2} {
		if st == 0 {
			items = append(items, fyne.NewMenuItemSeparator())
			continue
		}
		it := fyne.NewMenuItem(scaleLabel(st), func() { d.setScale(st) })
		if st == 1 {
			it.Shortcut = shortcutActual
		}
		s.steps[st] = it
		items = append(items, it)
	}
	s.zoomIn = fyne.NewMenuItem(lang.L("Zoom In"), func() { d.zoom(1) })
	s.zoomIn.Shortcut = shortcutZoomIn
	s.zoomOut = fyne.NewMenuItem(lang.L("Zoom Out"), func() { d.zoom(-1) })
	s.zoomOut.Shortcut = shortcutZoomOut
	s.reverse = fyne.NewMenuItem(lang.L("Reverse Mouse Wheel"), func() { d.setReverseWheel(!d.view.reverseWheel) })
	items = append(items, fyne.NewMenuItemSeparator(), s.zoomIn, s.zoomOut, fyne.NewMenuItemSeparator(), s.reverse)
	s.menu = fyne.NewMenu("", items...)

	c := d.win.Canvas()
	c.AddShortcut(shortcutFit, func(fyne.Shortcut) { d.scaleShortcut(func() { d.setScale(0) }) })
	c.AddShortcut(shortcutActual, func(fyne.Shortcut) { d.scaleShortcut(func() { d.setScale(1) }) })
	// "+" is Shift+= on many layouts, and its own key (KeyPlus) on others and on the keypad.
	for _, sc := range []*desktop.CustomShortcut{shortcutZoomIn,
		{KeyName: fyne.KeyPlus, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift},
		{KeyName: fyne.KeyEqual, Modifier: fyne.KeyModifierControl},
		{KeyName: fyne.KeyEqual, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}} {
		c.AddShortcut(sc, func(fyne.Shortcut) { d.scaleShortcut(func() { d.zoom(1) }) })
	}
	c.AddShortcut(shortcutZoomOut, func(fyne.Shortcut) { d.scaleShortcut(func() { d.zoom(-1) }) })
	d.refreshScaleItems()
}

// scaleEnabled reports whether the Scale menu applies: in the KVM view of the window.
func (d *DeviceWindow) scaleEnabled() bool {
	return !d.consoleView && !d.aborted && !d.fullScreen
}

// scaleShortcut runs a shortcut of the menu when the menu could be used.
func (d *DeviceWindow) scaleShortcut(run func()) {
	if d.scaleEnabled() && d.scale.popup == nil {
		run()
	}
}

func (d *DeviceWindow) showScaleMenu() {
	if d.scale.popup != nil || !d.scaleEnabled() {
		return
	}
	d.refreshScaleItems()
	d.scale.popup = d.showMenu(d.scale.btn, d.scale.menu, func() { d.scale.popup = nil })
}

// setScale shows the remote screen at s (0: Fit to Window) in the window; full screen keeps
// fitting and shows s again when it ends.
func (d *DeviceWindow) setScale(s float32) {
	d.scale.choice = s
	if !d.fullScreen {
		d.view.setScale(s)
	}
	d.refreshScaleItems()
}

// shownScale is the scale the image has now, in device pixels per framebuffer pixel: the
// choice, or for Fit to Window the scale fitting gives (1 without an image).
func (d *DeviceWindow) shownScale() float32 {
	if d.scale.choice > 0 {
		return d.scale.choice
	}
	w, h := d.view.screenSize()
	area := d.kvmScroll.Size()
	if w <= 0 || h <= 0 || area.Width <= 0 || area.Height <= 0 {
		return 1
	}
	_, size := fitRect(area, w, h)
	s := size.Width / float32(w)
	if c := d.win.Canvas(); c != nil && c.Scale() > 0 {
		s *= c.Scale()
	}
	return s
}

// nextStep returns the next step above (dir > 0) or below the shown scale, 0 if there is none.
func (d *DeviceWindow) nextStep(dir int) float32 {
	cur := d.shownScale()
	const eps = 0.001
	if dir > 0 {
		for _, s := range scaleSteps {
			if s > cur+eps {
				return s
			}
		}
		return 0
	}
	for i := len(scaleSteps) - 1; i >= 0; i-- {
		if scaleSteps[i] < cur-eps {
			return scaleSteps[i]
		}
	}
	return 0
}

// zoom is Zoom In (dir 1) or Zoom Out (dir -1): the next fixed step from the scale shown now.
func (d *DeviceWindow) zoom(dir int) {
	if s := d.nextStep(dir); s > 0 {
		d.setScale(s)
	}
}

// refreshScaleItems sets the check marks and locks Zoom In and Zoom Out at the ends.
func (d *DeviceWindow) refreshScaleItems() {
	s := &d.scale
	s.fit.Checked = s.choice == 0
	for st, it := range s.steps {
		it.Checked = st == s.choice
	}
	s.zoomIn.Disabled = d.nextStep(1) == 0
	s.zoomOut.Disabled = d.nextStep(-1) == 0
	s.reverse.Checked = d.view.reverseWheel
	if s.popup != nil {
		s.popup.Refresh()
	}
}

// setReverseWheel is the user's choice of Reverse Mouse Wheel: it applies at once and is stored
// with the favorite; a device that is not a favorite keeps it for this window only.
func (d *DeviceWindow) setReverseWheel(on bool) {
	d.view.reverseWheel, d.scale.chosen = on, true
	d.refreshScaleItems()
	ticket := d.scale.write.ticket()
	d.saveFavorite(func(s favoriteStore, host, user string) error {
		return d.scale.write.write(ticket, func() error { return s.SetReverseWheel(host, user, on) })
	})
}
