package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// enabledPower lists the Power menu entries that can be tapped.
func enabledPower(d *DeviceWindow) []amt.PowerAction {
	var on []amt.PowerAction
	for _, act := range powerOrder {
		if !d.powerItems[act].Disabled {
			on = append(on, act)
		}
	}
	return on
}

func sameActions(a, b []amt.PowerAction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The Power menu offers only what applies to the power state; an unknown state offers all.
// Shutdown additionally needs Off - Soft Graceful (12) in the device's power states, Boot from
// ISO a mounted ISO (none here; media_test.go).
func TestPowerMenuFollowsPowerState(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	d := h.d
	for _, states := range [][]power.PowerState{{5, 8, 2, 10, 11}, {2, 5, 8, 10, 11, 12}} {
		graceful := slices.Contains(states, power.PowerOffSoftGraceful)
		all := slices.DeleteFunc(slices.Clone(powerOrder), func(a amt.PowerAction) bool {
			return a == amt.BootToISO || a == amt.PowerShutdown && !graceful
		})
		on := []amt.PowerAction{amt.PowerOff, amt.PowerReset, amt.PowerCycle, amt.BootToBIOS}
		if graceful {
			on = []amt.PowerAction{amt.PowerOff, amt.PowerReset, amt.PowerCycle, amt.BootToBIOS, amt.PowerShutdown}
		}
		cases := []struct {
			st   models.PowerState
			want []amt.PowerAction
			dot  widgets.DotState
			text string
		}{
			{models.PowerStateOn, on, widgets.DotOn, "Powered on"},
			{models.PowerStateOffHard, []amt.PowerAction{amt.PowerOn, amt.BootToBIOS}, widgets.DotOff, "Powered off"},
			{models.PowerStateOffSoft, []amt.PowerAction{amt.PowerOn, amt.BootToBIOS}, widgets.DotOff, "Powered off"},
			{models.PowerStateSleepDeep, all, widgets.DotAsleep, "Sleeping"},
			{models.PowerStateHibernate, all, widgets.DotAsleep, "Hibernating"},
		}
		for _, c := range cases {
			d.apply(amt.Status{PowerState: c.st, PowerStates: states}, nil)
			if got := enabledPower(d); !sameActions(got, c.want) {
				t.Errorf("%v %v: enabled %v, want %v", states, c.st, got, c.want)
			}
			if d.headDot.State() != c.dot || d.powerDot.State() != c.dot || d.power.Text != c.text {
				t.Errorf("%v: dot %v/%v text %q", c.st, d.headDot.State(), d.powerDot.State(), d.power.Text)
			}
		}
		d.apply(amt.Status{}, errors.New("read failed")) // the power states read before still apply
		if got := enabledPower(d); !sameActions(got, all) || d.headDot.State() != widgets.DotUnknown {
			t.Errorf("%v unknown state: enabled %v dot %v", states, got, d.headDot.State())
		}
	}
	// A locked entry does nothing, even if a stale menu still shows it.
	d.apply(amt.Status{PowerState: models.PowerStateOn}, nil)
	d.confirm = func(amt.PowerAction, func()) { t.Fatal("confirm for a locked entry") }
	d.run(amt.PowerOn)
	if len(h.dev.Powered()) != 0 {
		t.Fatal("locked action ran")
	}
}

// Opening the Power menu reads the state once more; the open menu follows the answer.
func TestPowerMenuReadsStateOnOpen(t *testing.T) {
	englishUI(t)
	dev := &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOffHard}}
	h := newDevice(t, test.NewTempApp(t), dev, true)
	// Poller not started yet: the menu shows the last known state (off) while the read waits.
	test.Tap(h.d.powerBtn)
	if h.d.powerPopup == nil {
		t.Fatal("menu not open")
	}
	if !h.d.powerItems[amt.PowerOff].Disabled || h.d.powerItems[amt.PowerOn].Disabled {
		t.Fatal("menu did not start with the last known state")
	}
	h.d.powerPopup.Dismiss()

	// Another window with a running poller: opening the menu requests one read at once. (The
	// first window's request is still pending; its poller never runs.)
	h = newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOffHard}}, true)
	h.show(t)
	calls := h.dev.Calls()
	test.Tap(h.d.powerBtn)
	recv(t, h.applied) // the read the menu requested; without it the next one is 10 s away
	if h.dev.Calls() != calls+1 || h.d.powerPopup == nil {
		t.Fatalf("calls %d, want %d", h.dev.Calls(), calls+1)
	}
	h.d.apply(amt.Status{PowerState: models.PowerStateOn}, nil) // as if switched on meanwhile
	if h.d.powerItems[amt.PowerOff].Disabled || !h.d.powerItems[amt.PowerOn].Disabled {
		t.Fatal("open menu did not follow the new state")
	}
	h.d.powerPopup.Dismiss()
	if h.d.powerPopup != nil {
		t.Fatal("menu still registered after dismiss")
	}
}

// Every destructive action asks with its own title, text and button; Cancel and Esc stop it,
// only the action button runs it.
func TestPowerConfirmDialog(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	a.Settings().SetTheme(style.Theme{})
	w := a.NewWindow("x")
	w.Resize(fyne.NewSize(600, 400))
	t.Cleanup(w.Close)
	cases := map[amt.PowerAction][3]string{
		amt.PowerReset:    {"Reset Büro?", "The machine restarts immediately without shutting down", "Reset"},
		amt.PowerCycle:    {"Power cycle Büro?", "switched off and on again immediately", "Power Cycle"},
		amt.PowerOff:      {"Power off Büro?", "The machine is switched off immediately", "Power Off"},
		amt.PowerShutdown: {"Shut down Büro?", "The operating system is shut down in an orderly way", "Shut Down"},
		amt.BootToBIOS:    {"Restart Büro into BIOS setup?", "opens the BIOS setup", "Restart"},
	}
	for act, want := range cases {
		title, msg, button := confirmText(act, "Büro")
		if title != want[0] || !strings.Contains(msg, want[1]) || button != want[2] {
			t.Errorf("%s: %q %q %q", act, title, msg, button)
		}
	}

	ran := 0
	show := func() *widget.PopUp { return showPowerConfirm(w, amt.PowerOff, "Büro", func() { ran++ }) }
	buttons := func(p *widget.PopUp) (cancel, action *escButton) {
		bs := panelButtons(p.Content)
		if len(bs) != 2 {
			t.Fatalf("%d buttons", len(bs))
		}
		return bs[0], bs[1]
	}

	p := show()
	cancel, action := buttons(p)
	if cancel == nil || action == nil || action.Text != "Power Off" || cancel.Text != lang.L("Cancel") {
		t.Fatal("dialog buttons missing")
	}
	if w.Canvas().Focused() != cancel {
		t.Fatal("Cancel is not the focused default")
	}
	cancel.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if p.Visible() || ran != 0 {
		t.Fatalf("Esc on Cancel: visible=%v ran=%d", p.Visible(), ran)
	}

	p = show()
	w.Canvas().Unfocus()
	w.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape}) // Esc with nothing focused
	if p.Visible() || ran != 0 || w.Canvas().OnTypedKey() != nil {
		t.Fatalf("Esc on canvas: visible=%v ran=%d", p.Visible(), ran)
	}

	p = show()
	cancel, _ = buttons(p)
	test.Tap(cancel)
	if p.Visible() || ran != 0 {
		t.Fatal("Cancel ran the action")
	}

	p = show()
	_, action = buttons(p)
	test.Tap(action)
	test.Tap(action) // a second tap on the hidden dialog does nothing
	if p.Visible() || ran != 1 {
		t.Fatalf("action: visible=%v ran=%d", p.Visible(), ran)
	}
}

// The real confirmation path from the Power menu: the dialog opens and runs the action.
func TestPowerMenuOpensConfirmDialog(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn}}, true)
	h.d.confirm = h.d.confirmDialog
	h.show(t)
	h.tapPower(t, amt.PowerReset)
	if len(h.dev.Powered()) != 0 {
		t.Fatal("ran before the confirmation")
	}
	focused, ok := h.d.win.Canvas().Focused().(*escButton)
	if !ok {
		t.Fatalf("focus on %T", h.d.win.Canvas().Focused())
	}
	focused.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if len(h.dev.Powered()) != 0 {
		t.Fatal("Esc ran the action")
	}
}

func TestQualityMenuSavesPerFavorite(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	store := &fakeLayouts{fav: &favorites.Favorite{Host: "device.example.com", Username: "admin"}}
	h.d.layouts = store
	saved := make(chan struct{}, 4)
	h.d.onQualitySaved = func() { saved <- struct{}{} }

	if h.d.quality.Disabled() || !h.d.qualityItems[kvm.QualityHigh].Checked || h.d.qualityItems[kvm.QualityLow].Checked {
		t.Fatal("High must be the checked default")
	}

	// Locked while a connect is pending: the menu does not open and nothing changes.
	h.d.kvmBusy = true
	h.d.setKVMControls()
	if !h.d.quality.Disabled() {
		t.Fatal("quality enabled while connecting")
	}
	h.d.showQualityMenu()
	h.d.setQuality(kvm.QualityLow)
	if h.d.qualityPopup != nil || h.d.kvmQuality != kvm.QualityHigh || len(store.saved) != 0 {
		t.Fatal("quality changed while connecting")
	}
	h.d.kvmBusy = false
	h.d.setKVMControls()
	h.connect(t, true) // takes the fake server's only (High) frame
	h.disconnect(t)

	test.Tap(h.d.quality)
	if h.d.qualityPopup == nil {
		t.Fatal("Quality menu not open")
	}
	tapMenuItem(t, h.d.qualityPopup, h.d.qualityMenu, h.d.qualityItems[kvm.QualityLow])
	recv(t, saved)
	if h.d.kvmQuality != kvm.QualityLow || !h.d.qualityItems[kvm.QualityLow].Checked || store.fav.Quality != favorites.QualityLow {
		t.Fatalf("quality %v stored %q", h.d.kvmQuality, store.fav.Quality)
	}
	if len(h.errs) != 0 {
		t.Fatal("error on save")
	}
	h.connect(t, false) // the next connection uses it
	h.disconnect(t)
	if q := h.dev.Qualities(); len(q) != 2 || q[0] != kvm.QualityHigh || q[1] != kvm.QualityLow {
		t.Fatalf("connected with %v", q)
	}

	// A device that is not a favorite keeps the choice for the window only, silently.
	store.fav = nil
	test.Tap(h.d.quality)
	tapMenuItem(t, h.d.qualityPopup, h.d.qualityMenu, h.d.qualityItems[kvm.QualityHigh])
	recv(t, saved)
	if h.d.kvmQuality != kvm.QualityHigh || len(h.errs) != 0 {
		t.Fatalf("not a favorite: %v errs %d", h.d.kvmQuality, len(h.errs))
	}

	// A failed write is reported.
	store.fav = &favorites.Favorite{}
	store.setErr = errors.New("disk full")
	h.d.setQuality(kvm.QualityLow)
	recv(t, saved)
	if err := <-h.errs; err == nil || err.Error() != "disk full" {
		t.Fatalf("err %v", err)
	}
}

func TestQualityReadFromFavoriteOnOpen(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	for _, c := range []struct {
		fav  *favorites.Favorite
		want kvm.Quality
	}{
		{&favorites.Favorite{Quality: favorites.QualityLow}, kvm.QualityLow},
		{&favorites.Favorite{}, kvm.QualityHigh}, // old favorite without the field
		{nil, kvm.QualityHigh},                   // not a favorite
	} {
		h := newDevice(t, a, &session.Fake{FP: fp}, true)
		loaded := make(chan struct{}, 1)
		h.d.onQualityLoaded = func() { loaded <- struct{}{} }
		h.d.layouts = &fakeLayouts{fav: c.fav}
		h.d.loadQuality() // as Show does; without the poller, whose reads run beside it in tests
		recv(t, loaded)
		if h.d.kvmQuality != c.want || h.d.quality.Caption() != "Quality" {
			t.Errorf("fav %+v: quality %v", c.fav, h.d.kvmQuality)
		}
		if want := qualityBadge(c.want); !h.d.qualityItems[c.want].Checked || want == "" {
			t.Errorf("fav %+v: item not checked", c.fav)
		}
	}

	// A choice made before the favorite was read wins.
	h := newDevice(t, a, &session.Fake{FP: fp}, true)
	block := make(chan struct{})
	h.d.layouts = &fakeLayouts{fav: &favorites.Favorite{Quality: favorites.QualityLow}, block: block}
	loaded := make(chan struct{}, 1)
	h.d.onQualityLoaded = func() { loaded <- struct{}{} }
	h.d.loadQuality()
	h.d.qualityChosen = true // as setQuality does; its own write would block on the fake as well
	close(block)
	recv(t, loaded)
	if h.d.kvmQuality != kvm.QualityHigh {
		t.Fatal("favorite overrode the user's choice")
	}
}

// The empty state connects KVM, offers Cancel while connecting and follows the power state;
// the status bar names the KVM state.
func TestEmptyStateAndStatusBar(t *testing.T) {
	dev := &sessionFake{Consent: true, ConsentCode: "123456"}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	d := h.d
	if !d.emptyState.Visible() || d.emptyBtn.Text != "Connect KVM" || d.kvmLine.Text != "KVM not connected" {
		t.Fatalf("empty %v %q %q", d.emptyState.Visible(), d.emptyBtn.Text, d.kvmLine.Text)
	}
	test.Tap(d.emptyBtn)
	answer := <-h.asked
	if d.emptyBtn.Text != lang.L("Cancel") || d.kvmLine.Text != "Connecting KVM…" || !d.kvmOn() {
		t.Fatalf("connecting: %q %q", d.emptyBtn.Text, d.kvmLine.Text)
	}
	answer("123456", true)
	recv(t, h.changed)
	recv(t, h.frames)
	if d.emptyState.Visible() || d.kvmLine.Text != "KVM connected" || !d.kvmOn() {
		t.Fatalf("connected: empty=%v line=%q", d.emptyState.Visible(), d.kvmLine.Text)
	}
	h.disconnect(t)
	if !d.emptyState.Visible() || d.kvmLine.Text != "KVM session closed." || d.kvmOn() {
		t.Fatalf("closed: empty=%v line=%q", d.emptyState.Visible(), d.kvmLine.Text)
	}

	test.Tap(d.emptyBtn) // Cancel while the consent dialog waits
	<-h.asked
	test.Tap(d.emptyBtn)
	recv(t, h.changed)
	if d.kvmClient != nil || d.emptyBtn.Text != "Connect KVM" || len(h.errs) != 0 {
		t.Fatalf("cancel: %q", d.emptyBtn.Text)
	}

	for st, want := range map[models.PowerState]string{
		models.PowerStateOn:        "Büro is powered on.",
		models.PowerStateOffSoft:   "Büro is powered off.",
		models.PowerStateSleepDeep: "Büro is asleep.",
		models.PowerState(99):      "Connect KVM to see the screen of Büro",
	} {
		d.apply(amt.Status{PowerState: st}, nil)
		if !strings.HasPrefix(d.emptyText.String(), want) {
			t.Errorf("%v: %q", st, d.emptyText.String())
		}
	}
}

// While the device is off there is nothing to connect to (issue #120): the empty state hides
// "Connect KVM" and says the device is off, the KVM toggle is grayed out. Any other state,
// including an unknown one, keeps both. A session that is still open when the device is read
// as off keeps the toggle, so it can be closed.
func TestKVMLockedWhileOff(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	d := h.d
	for _, st := range []models.PowerState{models.PowerStateOffSoft, models.PowerStateOffHard} {
		d.apply(amt.Status{PowerState: st}, nil)
		if !d.emptyState.Visible() || d.emptyBtnBox.Visible() || d.kvmToggleEnabled() {
			t.Fatalf("%v: empty %v, button %v, KVM disabled %v", st, d.emptyState.Visible(), d.emptyBtnBox.Visible(), !d.kvmToggleEnabled())
		}
		if got := d.emptyText.String(); got != "Büro is powered off. Power it on to connect KVM." {
			t.Errorf("%v: %q", st, got)
		}
	}
	tapKVM(d)
	if d.kvmBusy || d.kvmClient != nil {
		t.Fatal("KVM connects while the device is off")
	}
	for _, st := range []models.PowerState{models.PowerStateOn, models.PowerStateSleepDeep, models.PowerStateHibernate, models.PowerState(99)} {
		d.apply(amt.Status{PowerState: st}, nil)
		if !d.emptyBtnBox.Visible() || !d.kvmToggleEnabled() {
			t.Fatalf("%v: button %v, KVM disabled %v", st, d.emptyBtnBox.Visible(), !d.kvmToggleEnabled())
		}
	}
	d.apply(amt.Status{PowerState: models.PowerStateOffSoft}, nil)
	d.apply(amt.Status{}, errors.New("read failed")) // unknown again: connecting is possible
	if !d.emptyBtnBox.Visible() || !d.kvmToggleEnabled() {
		t.Fatal("unknown state locks KVM")
	}

	d.apply(amt.Status{PowerState: models.PowerStateOn}, nil)
	h.connect(t, true)
	d.apply(amt.Status{PowerState: models.PowerStateOffSoft}, nil)
	if !d.kvmToggleEnabled() || !d.kvmOn() {
		t.Fatal("open session cannot be closed while the device reads as off")
	}
	h.disconnect(t)
	if !d.emptyState.Visible() || d.emptyBtnBox.Visible() || d.kvmToggleEnabled() {
		t.Fatal("after closing: KVM not locked while off")
	}
}

func TestEllipsisText(t *testing.T) {
	test.NewTempApp(t)
	l := newEllipsisText("A rather long device name", 13, "foreground", false)
	l.shrink = true
	full := l.natural()
	l.Resize(fyne.NewSize(full/2, 20))
	r := test.TempWidgetRenderer(t, l).(*ellipsisRenderer)
	r.Layout(l.Size())
	if !strings.HasSuffix(r.txt.Text, "…") || l.MinSize().Width != 0 {
		t.Fatalf("%q min %v", r.txt.Text, l.MinSize())
	}
	l.Resize(fyne.NewSize(full+1, 20))
	r.Layout(l.Size())
	if r.txt.Text != l.Text {
		t.Fatalf("%q", r.txt.Text)
	}
}

// The status dot sits at the far left of the toolbar, directly before name and host, and not on
// the Power button.
func TestToolbarDotLeftOfNameAndNotOnPowerButton(t *testing.T) {
	englishUI(t)
	d := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn}}, true).d
	d.win.Resize(fyne.NewSize(1120, 732))
	d.apply(amt.Status{PowerState: models.PowerStateOn}, nil)
	if d.powerBtn.Dot() != widgets.DotNone {
		t.Fatalf("Power button still shows a dot: %v", d.powerBtn.Dot())
	}
	if d.headDot.State() != widgets.DotOn {
		t.Fatalf("toolbar dot %v", d.headDot.State())
	}
	abs := func(o fyne.CanvasObject) fyne.Position {
		return fyne.CurrentApp().Driver().AbsolutePositionForObject(o)
	}
	dot, title, host := abs(d.headDot), abs(d.title), abs(d.host)
	if dot.X >= title.X || dot.X >= host.X || dot.X != toolbarPadLeft {
		t.Fatalf("dot x %v title x %v host x %v", dot.X, title.X, host.X)
	}
	if gap := title.X - (dot.X + d.headDot.Size().Width); gap != dotNameGap || title.X != host.X {
		t.Fatalf("gap %v, title x %v host x %v", gap, title.X, host.X)
	}
}
