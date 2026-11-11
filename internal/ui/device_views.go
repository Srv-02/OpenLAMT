package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Views, Media and Manage of the device window (Main.dc.html, Console.dc.html, Media.dc.html):
// the KVM|Console switch shows one of two stages, both sessions may stay open; Media opens its
// popover through onMedia; Manage toggles the device management sidebar right of the stage.

// Segments of the view switch.
const (
	viewKVM     = 0
	viewConsole = 1
)

// manageWidth is the width of the device management sidebar (Main.dc.html), without its 1 px
// left border.
const manageWidth float32 = 400

// buildViewControls creates the view switch and the Media and Manage buttons. Called once from
// NewDeviceWindow before buildKVM, whose setKVMControls already sets their states.
func (d *DeviceWindow) buildViewControls() {
	d.viewSwitch = widgets.NewSegmented(widgets.SegmentedToolbar, []widgets.SegmentOption{
		{Icon: icons.KVM, Label: "KVM"},
		{Icon: icons.Console, Label: lang.L("Console")},
	}, func(i int) { d.setConsoleView(i == viewConsole) })
	d.viewSwitch.OnReselected = d.viewReselected
	d.mediaBtn = widgets.NewToolbarButton(icons.Media, lang.L("Media"), func() {
		if d.onMedia != nil {
			d.onMedia()
		}
	})
	d.manageBtn = widgets.NewToolbarButton(icons.Manage, lang.L("Manage"), func() { d.setManageOpen(!d.manageOpen()) })
}

// buildStage returns the stack of the KVM stage (letterbox, view, empty state) and the Console
// stage (dark background, consoleStage); only one of them is visible.
func (d *DeviceWindow) buildStage() fyne.CanvasObject {
	letterbox := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "")
	d.kvmStage = container.NewStack(letterbox, d.kvmArea, d.buildEmptyState())
	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameConsole, "")
	d.consoleStage = container.NewStack()
	d.consolePane = container.NewStack(bg, d.consoleStage)
	d.consolePane.Hide()
	return container.NewStack(d.kvmStage, d.consolePane)
}

// buildManagePane returns the sidebar right of the stage: a 1 px line on its left and the slot
// of manageWidth on the panel background. It starts closed.
func (d *DeviceWindow) buildManagePane() fyne.CanvasObject {
	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameSidePanel, "")
	d.manageSlot = container.NewStack()
	panel := container.New(&fixedWidthStack{width: manageWidth}, bg, d.manageSlot)
	d.manageEdge = vline(style.ColorNameSeparatorDark)
	d.managePane = container.New(layout.NewCustomPaddedHBoxLayout(0), d.manageEdge, panel)
	d.managePane.Hide()
	return d.managePane
}

// consoleActive reports whether the Console view is shown.
func (d *DeviceWindow) consoleActive() bool { return d.consoleView }

// setConsoleView shows the Console view (on) or the KVM view. Sessions stay as they are: a
// running KVM session goes on behind the Console view. The hidden KVM view gives up the focus
// (FocusLost releases held keys); back in the KVM view it gets it again.
func (d *DeviceWindow) setConsoleView(on bool) {
	if on == d.consoleView {
		return
	}
	d.consoleView = on
	if on {
		d.viewSwitch.SetSelected(viewConsole)
		if d.qualityPopup != nil {
			d.qualityPopup.Dismiss()
		}
		d.kvmStage.Hide()
		d.consolePane.Show()
		if d.win.Canvas().Focused() == d.view {
			d.win.Canvas().Unfocus()
		}
	} else {
		d.viewSwitch.SetSelected(viewKVM)
		d.blurConsole()
		d.consolePane.Hide()
		d.kvmStage.Show()
	}
	d.setKVMControls()
	if !on {
		d.focusView()
		return
	}
	if d.onConsoleSelected != nil {
		d.onConsoleSelected()
	}
}

// viewReselected handles a tap on the selected segment: KVM connects or disconnects KVM like the
// former KVM button (locked while the device is off and nothing runs), Console calls
// onConsoleReselected.
func (d *DeviceWindow) viewReselected(i int) {
	if i == viewConsole {
		if d.onConsoleReselected != nil {
			d.onConsoleReselected()
		}
		return
	}
	if d.kvmToggleEnabled() {
		d.toggleKVM()
	}
}

// kvmOn reports whether a KVM session runs or connects (the former toggled KVM button).
func (d *DeviceWindow) kvmOn() bool {
	return (d.kvmClient != nil || d.kvmReconnecting || d.kvmBusy) && !d.aborted
}

// kvmToggleEnabled reports whether the selected KVM segment may connect or disconnect: an open
// session can always be closed and a connect cancelled; a new connect needs a device that is not
// read as off and a live session.
func (d *DeviceWindow) kvmToggleEnabled() bool {
	return !d.aborted && (d.kvmClient != nil || d.kvmReconnecting || d.kvmBusy || !d.poweredOff())
}

// focusView gives the keyboard to the KVM view while a session runs and the view is shown (KVM
// view or full screen); the Console view keeps its own focus.
func (d *DeviceWindow) focusView() {
	if d.kvmClient != nil && (d.fullScreen || !d.consoleView) {
		d.win.Canvas().Focus(d.view)
	}
}

// toolbarCtrlAltDel is Ctrl+Alt+Del of the toolbar: the console hook in the Console view, the
// KVM session otherwise. The full screen bar always sends over KVM (ctrlAltDel).
func (d *DeviceWindow) toolbarCtrlAltDel() {
	if d.consoleView {
		if d.consoleCtrlAltDel != nil {
			d.consoleCtrlAltDel()
		}
		return
	}
	d.ctrlAltDel()
}

// toolbarTypeClipboard is Type Clipboard of the toolbar: the console hook in the Console view,
// unless a KVM typing job runs (the button then reads "Stop typing" and stops it).
func (d *DeviceWindow) toolbarTypeClipboard() {
	if d.consoleView && d.typing == nil {
		if d.consoleTypeClipboard != nil {
			d.consoleTypeClipboard()
		}
		return
	}
	d.typeClipboard()
}

// manageOpen reports whether the device management sidebar is shown.
func (d *DeviceWindow) manageOpen() bool { return d.managePane.Visible() }

// setManageOpen opens or closes the sidebar and toggles the Manage button. The first opening
// fills the slot from manageContent (nil leaves it empty). Full screen replaces the whole
// content, so the sidebar disappears with it and returns as it was.
func (d *DeviceWindow) setManageOpen(open bool) {
	if open == d.manageOpen() {
		return
	}
	if open && !d.manageBuilt {
		d.manageBuilt = true
		if d.manageContent != nil {
			if c := d.manageContent(); c != nil {
				d.manageSlot.Objects = []fyne.CanvasObject{c}
				d.manageEdge.Hide() // the content draws its own left line (manager.Sidebar)
			}
		}
	}
	if open {
		d.managePane.Show()
	} else {
		d.managePane.Hide()
	}
	d.manageBtn.SetActive(open)
	d.body.Refresh()
}

// setMediaStatus shows text ("ISO mounted · 18.2 MB/s") in the accent color before TLS in the
// status bar; empty hides it.
func (d *DeviceWindow) setMediaStatus(text string) { d.setStatusExtra(d.mediaLine, text) }

// setConsoleStatus shows text ("Console connected") after the KVM state in the status bar;
// empty hides it.
func (d *DeviceWindow) setConsoleStatus(text string) { d.setStatusExtra(d.consoleLine, text) }

func (d *DeviceWindow) setStatusExtra(t *ellipsisText, text string) {
	t.SetText(text)
	if text == "" {
		t.Hide()
	} else {
		t.Show()
	}
	d.relayoutStatus()
}

// vline is a 1 px vertical line.
func vline(c fyne.ThemeColorName) fyne.CanvasObject {
	r := style.BindRect(canvas.NewRectangle(nil), c, "")
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

// manageSideLayout gives objs[0] (the stages) the width left of objs[1] (the sidebar), which keeps
// its minimum width while visible; no gap between them.
type manageSideLayout struct{}

func (manageSideLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	s := objs[0].MinSize()
	if objs[1].Visible() {
		m := objs[1].MinSize()
		s = fyne.NewSize(s.Width+m.Width, max(s.Height, m.Height))
	}
	return s
}

func (manageSideLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var sw float32
	if objs[1].Visible() {
		sw = min(objs[1].MinSize().Width, size.Width)
		objs[1].Move(fyne.NewPos(size.Width-sw, 0))
		objs[1].Resize(fyne.NewSize(sw, size.Height))
	}
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(size.Width-sw, size.Height))
}

// fixedWidthStack stacks its objects at a fixed width and the full height.
type fixedWidthStack struct{ width float32 }

func (l *fixedWidthStack) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		if o.Visible() {
			h = max(h, o.MinSize().Height)
		}
	}
	return fyne.NewSize(l.width, h)
}

func (l *fixedWidthStack) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

// ensureManager returns the device management of this window and creates it at the first call
// (the first opening of the sidebar). It reads nothing itself: Sidebar starts the first read.
func (d *DeviceWindow) ensureManager() *manager {
	if d.manager != nil {
		return d.manager
	}
	m := newManager(d.app, d.win, d.sess, d.name, manageDeps{
		host:       d.sess.Config().Host,
		status:     func() (amt.Status, bool) { return d.lastStatus, d.statusKnown },
		loginStore: d.loginStore,
		onError:    func(err error) { d.onError(err) },
	})
	// The pop-out symbol: the sidebar closes (Manage inactive), then the window opens.
	m.SetOnPopOut(func() { d.setManageOpen(false) })
	d.manager = m
	if d.onManager != nil {
		d.onManager(m)
	}
	return m
}

// closeManager ends device management with the window or the session: its own window closes and
// later results are dropped.
func (d *DeviceWindow) closeManager() {
	if d.manager != nil {
		d.manager.Close()
	}
}
