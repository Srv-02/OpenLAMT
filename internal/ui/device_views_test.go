package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// tapKVM taps the KVM segment of the view switch: in the KVM view it connects or disconnects
// KVM like the former KVM button, in the Console view it only switches the view.
func tapKVM(d *DeviceWindow) { d.viewSwitch.Tap(viewKVM) }

// viewsOnly keeps the work that the Console view and the Manage sidebar start in the background
// out of tests of the views alone: no console connect (console_test.go tests it) and no results
// or clock of device management (device_manage_test.go). That work runs on its own goroutines,
// where the test driver runs fyne.Do inline, so it would lay out the SOL question or the
// hardware page while the test goroutine goes on with the window. Fyne keeps the theme of every
// ThemeOverride layout in one global stack, so two layouts at once can read a nil theme and
// panic; the full screen content holds ThemeOverrides since it shows both stages.
func viewsOnly(d *DeviceWindow) {
	d.onConsoleSelected = nil
	d.onManager = func(m *manager) {
		m.tickEvery = 0
		m.later = func(func()) {} // the read results are dropped
	}
}

func newOnDevice(t *testing.T) *DeviceWindow {
	t.Helper()
	englishUI(t)
	a := test.NewTempApp(t)
	dev := &session.Fake{St: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}, FP: fp}
	return newDevice(t, a, dev, true).d
}

// TestToolbarOrder: Power | [KVM | Console] · Media · Quality | Ctrl+Alt+Del · Type Clipboard ·
// Keyboard | Manage · Full Screen (Main.dc.html).
func TestToolbarOrder(t *testing.T) {
	d := newOnDevice(t)
	objs := d.toolbarItems.Objects
	isDivider := func(o fyne.CanvasObject) bool {
		_, ok := o.(*fyne.Container)
		return ok
	}
	want := []fyne.CanvasObject{d.powerBtn, nil, d.viewSwitch, d.mediaBtn, d.quality, nil,
		d.cadBtn, d.clipBtn, d.keyboard, nil, d.manageBtn, d.fullBtn}
	if len(objs) != len(want) {
		t.Fatalf("%d toolbar items, want %d", len(objs), len(want))
	}
	for i, w := range want {
		if w == nil && !isDivider(objs[i]) || w != nil && objs[i] != w {
			t.Fatalf("item %d is %T", i, objs[i])
		}
	}
	if d.mediaBtn.Caption() != "Media" || d.manageBtn.Caption() != "Manage" {
		t.Fatalf("captions %q %q", d.mediaBtn.Caption(), d.manageBtn.Caption())
	}
	if d.viewSwitch.Selected() != viewKVM || d.consoleActive() {
		t.Fatal("the KVM view must be selected at start")
	}
	if d.mediaBtn.Disabled() || d.manageBtn.Disabled() || d.manageBtn.Active() {
		t.Fatal("Media and Manage must be enabled, Manage not toggled")
	}
	// Media calls its hook (media.go opens the Media menu there).
	media := 0
	d.onMedia = func() { media++ }
	test.Tap(d.mediaBtn)
	if media != 1 {
		t.Fatalf("Media hook called %d times", media)
	}
}

// TestConsoleViewControls: the Console view greys out Quality, and Type Clipboard while no
// console session runs; Ctrl+Alt+Del works there without a KVM session and calls the console
// hook. Back in the KVM view the KVM states return, and a tap on the inactive segment never
// connects.
func TestConsoleViewControls(t *testing.T) {
	d := newOnDevice(t)
	selected, reselected, cad, clip := 0, 0, 0, 0
	d.onConsoleSelected = func() { selected++ }
	d.onConsoleReselected = func() { reselected++ }
	d.consoleCtrlAltDel = func() { cad++ }
	d.consoleTypeClipboard = func() { clip++ }

	d.viewSwitch.Tap(viewConsole)
	if !d.consoleActive() || selected != 1 || d.viewSwitch.Selected() != viewConsole {
		t.Fatalf("console view: active %v, hook %d", d.consoleActive(), selected)
	}
	if !d.consolePane.Visible() || d.kvmStage.Visible() {
		t.Fatal("console stage not shown")
	}
	if !d.quality.Disabled() {
		t.Fatal("Quality must be disabled in the Console view")
	}
	if d.cadBtn.Disabled() || d.mediaBtn.Disabled() || d.powerBtn.Disabled() ||
		d.manageBtn.Disabled() || d.keyboard.Disabled() || d.viewSwitch.Disabled() {
		t.Fatal("Console view: only Quality, Type Clipboard and Full Screen may be disabled")
	}
	if !d.clipBtn.Disabled() || !d.barClip.Disabled() {
		t.Fatal("Type Clipboard must be disabled without a console session")
	}
	if !d.fullBtn.Disabled() {
		t.Fatal("Full Screen needs the session of the shown view")
	}
	test.Tap(d.cadBtn)
	test.Tap(d.clipBtn)
	if cad != 1 || clip != 0 {
		t.Fatalf("console hooks: Ctrl+Alt+Del %d, Type Clipboard %d", cad, clip)
	}
	d.typing = &typeJob{} // a KVM typing job: the button reads "Stop typing" and must stay usable
	d.setKVMControls()
	if d.clipBtn.Disabled() || d.barClip.Disabled() {
		t.Fatal("Stop typing disabled in the Console view")
	}
	d.typing = nil
	d.setKVMControls()
	d.viewSwitch.Tap(viewConsole) // the selected segment
	if reselected != 1 || selected != 1 {
		t.Fatalf("reselect: %d, selected %d", reselected, selected)
	}

	tapKVM(d) // inactive segment: view only
	if d.consoleActive() || d.consolePane.Visible() || !d.kvmStage.Visible() || d.kvmBusy || d.kvmOn() {
		t.Fatalf("KVM view: console %v busy %v", d.consoleActive(), d.kvmBusy)
	}
	if d.quality.Disabled() || !d.cadBtn.Disabled() || !d.clipBtn.Disabled() {
		t.Fatal("KVM view states not restored")
	}
	test.Tap(d.cadBtn)
	if cad != 1 {
		t.Fatal("the console hook must not run in the KVM view")
	}
}

// TestKVMSegmentReselectAndViews: the selected KVM segment connects and disconnects KVM; the
// session stays open in the Console view, which disables Quality and leaves the focus alone.
func TestKVMSegmentReselectAndViews(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	viewsOnly(h.d)
	h.connect(t, true) // taps the selected KVM segment
	if !h.d.kvmOn() || h.d.win.Canvas().Focused() != h.d.view {
		t.Fatal("not connected or view not focused")
	}
	h.d.viewSwitch.Tap(viewConsole)
	if h.d.con.busy {
		t.Fatal("a console connect runs beside the test goroutine")
	}
	if h.d.kvmClient == nil || h.d.kvmLine.Text != "KVM connected" {
		t.Fatal("KVM must stay connected in the Console view")
	}
	if !h.d.quality.Disabled() || h.d.cadBtn.Disabled() || !h.d.fullBtn.Disabled() {
		t.Fatal("Console view controls while KVM runs (Full Screen needs a console session)")
	}
	if h.d.win.Canvas().Focused() == h.d.view {
		t.Fatal("the hidden KVM view keeps the focus")
	}
	tapKVM(h.d) // back, still connected
	if h.d.kvmClient == nil || h.d.quality.Disabled() || h.d.win.Canvas().Focused() != h.d.view {
		t.Fatal("KVM view not restored")
	}

	// Full screen hides the open sidebar and brings it back.
	h.d.setManageOpen(true)
	test.Tap(h.d.fullBtn)
	found := false
	walkAbs(h.d.win.Content(), fyne.Position{}, func(o fyne.CanvasObject, _ fyne.Position) { found = found || o == h.d.manageSlot })
	if found {
		t.Fatal("sidebar shown in full screen")
	}
	test.Tap(h.d.exitFullBtn)
	if h.d.win.Content() != h.d.content || !h.d.manageOpen() || !h.d.managePane.Visible() {
		t.Fatal("sidebar not back after full screen")
	}

	h.disconnect(t) // taps the selected KVM segment again
	if h.d.kvmClient != nil || h.d.kvmOn() {
		t.Fatal("still connected")
	}
}

// TestManageSidebar: Manage toggles a 400 px slot right of the stage; its content is built once.
func TestManageSidebar(t *testing.T) {
	d := newOnDevice(t)
	if d.manageOpen() || d.managePane.Visible() {
		t.Fatal("the sidebar must start closed")
	}
	built := 0
	content := widget.NewLabel("sidebar")
	d.manageContent = func() fyne.CanvasObject { built++; return content }

	test.Tap(d.manageBtn)
	if !d.manageOpen() || !d.managePane.Visible() || !d.manageBtn.Active() || built != 1 {
		t.Fatalf("open: %v active %v built %d", d.manageOpen(), d.manageBtn.Active(), built)
	}
	if len(d.manageSlot.Objects) != 1 || d.manageSlot.Objects[0] != content {
		t.Fatal("content not in the slot")
	}
	if w := d.manageSlot.Size().Width; w != manageWidth || manageWidth != 400 {
		t.Fatalf("slot width %v", w)
	}
	if d.kvmStage.Size().Width != d.content.Size().Width-manageWidth || d.manageEdge.Visible() {
		t.Fatalf("stage %v, window %v", d.kvmStage.Size().Width, d.content.Size().Width)
	}

	test.Tap(d.manageBtn)
	if d.manageOpen() || d.managePane.Visible() || d.manageBtn.Active() {
		t.Fatal("sidebar not closed")
	}
	if d.kvmStage.Size().Width != d.content.Size().Width {
		t.Fatalf("stage %v after close", d.kvmStage.Size().Width)
	}
	d.setManageOpen(true)
	if built != 1 {
		t.Fatalf("content built %d times", built)
	}
}

// TestStatusBarExtras: "ISO mounted · …" (accent) before TLS, "Console connected" after the
// KVM state, each shown only with text.
func TestStatusBarExtras(t *testing.T) {
	d := newOnDevice(t)
	if d.mediaLine.Visible() || d.consoleLine.Visible() {
		t.Fatal("extras shown without text")
	}
	d.setMediaStatus("ISO mounted · 18.2 MB/s")
	d.setConsoleStatus("Console connected")
	if !d.mediaLine.Visible() || !d.consoleLine.Visible() || d.mediaLine.Text != "ISO mounted · 18.2 MB/s" || d.consoleLine.Text != "Console connected" {
		t.Fatal("extras not shown")
	}
	if d.mediaLine.color != style.ColorNameAccentText {
		t.Fatalf("media status color %s", d.mediaLine.color)
	}
	tls := d.statusRow.Objects[2]
	x := func(o fyne.CanvasObject) float32 { return o.Position().X }
	if !(x(d.version) < x(d.mediaLine) && x(d.mediaLine) < x(tls) && x(tls) < x(d.kvmLine) && x(d.kvmLine) < x(d.consoleLine)) {
		t.Fatalf("order: media %v tls %v kvm %v console %v", x(d.mediaLine), x(tls), x(d.kvmLine), x(d.consoleLine))
	}
	right := d.consoleLine.Position().X + d.consoleLine.Size().Width
	if right != d.statusRow.Size().Width || d.kvmLine.Size().Width != d.kvmLine.natural() {
		t.Fatalf("console line ends at %v of %v", right, d.statusRow.Size().Width)
	}
	d.setMediaStatus("")
	d.setConsoleStatus("")
	if d.mediaLine.Visible() || d.consoleLine.Visible() {
		t.Fatal("extras shown after clearing")
	}
	if right := d.kvmLine.Position().X + d.kvmLine.Size().Width; right != d.statusRow.Size().Width {
		t.Fatalf("KVM text ends at %v of %v", right, d.statusRow.Size().Width)
	}
}

// TestStatusBarShortensOnlyKVM: in a narrow bar only the KVM text is shortened.
func TestStatusBarShortensOnlyKVM(t *testing.T) {
	d := newOnDevice(t)
	d.setMediaStatus("ISO mounted · 18.2 MB/s")
	d.setConsoleStatus("Console connected")
	l := &statusLayout{gap: statusGap, shrink: statusShrink}
	objs := d.statusRow.Objects
	l.Layout(objs, fyne.NewSize(l.MinSize(objs).Width+20, style.StatusBarHeight))
	if d.kvmLine.Size().Width != 20 || d.mediaLine.Size().Width != d.mediaLine.natural() || d.consoleLine.Size().Width != d.consoleLine.natural() {
		t.Fatalf("kvm %v media %v console %v", d.kvmLine.Size().Width, d.mediaLine.Size().Width, d.consoleLine.Size().Width)
	}
}

// TestConsoleStageIsDark: the Console stage has the dark console background in both variants
// (Console.dc.html #0B0D10).
func TestConsoleStageIsDark(t *testing.T) {
	for _, v := range []fyne.ThemeVariant{theme.VariantLight, theme.VariantDark} {
		if got := style.ColorFor(style.ColorNameConsole, v); got != style.ColorFor(style.ColorNameConsole, theme.VariantLight) {
			t.Fatalf("variant %d: %v", v, got)
		}
	}
	r, g, b, _ := style.ColorFor(style.ColorNameConsole, theme.VariantLight).RGBA()
	if r>>8 != 0x0B || g>>8 != 0x0D || b>>8 != 0x10 {
		t.Fatalf("console background %02X%02X%02X", r>>8, g>>8, b>>8)
	}
	d := newOnDevice(t)
	if d.consolePane.Visible() || d.consoleStage == nil || len(d.consoleStage.Objects) != 0 {
		t.Fatal("console stage must start hidden and empty")
	}
}

// TestViewSwitchLockedWhileOff: while the device is off the KVM segment does not connect (as
// the old KVM button), but the view still switches.
func TestViewSwitchLockedWhileOff(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	dev := &session.Fake{St: amt.Status{PowerState: models.PowerStateOffSoft}, FP: fp}
	d := newDevice(t, a, dev, true).d
	if d.kvmToggleEnabled() {
		t.Fatal("KVM toggle must be locked while the device is off")
	}
	tapKVM(d)
	if d.kvmBusy {
		t.Fatal("reselect connected a device that is off")
	}
	viewsOnly(d)
	d.viewSwitch.Tap(viewConsole) // the view still switches
	if !d.consoleActive() {
		t.Fatal("view switch locked while off")
	}
}
