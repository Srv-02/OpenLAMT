package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/ider/idertest"
	"github.com/Srv-02/openlamt/internal/mkiso"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// mediaHarness is a device window whose fake mounts on an idertest server. The statistics
// ticker is manual (tick) and the clock of the read rate is h.clock. Asynchronous steps end
// with an event (media.go onEvent); tests touch the window only after waiting for one.
type mediaHarness struct {
	*deviceHarness
	m      *mediaPanel
	srv    *idertest.Server
	events chan string
	peers  chan *idertest.Peer
	asked  chan func(string, bool)
	bootGo chan struct{} // lets the session call of the checkbox run
	tick   func()
	clock  time.Time
}

func newMediaDevice(t *testing.T, dev *session.Fake) *mediaHarness {
	t.Helper()
	englishUI(t)
	cache := t.TempDir() // mkiso.BuildTemp writes below os.UserCacheDir
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	dev.FP, dev.IDERAddr, dev.IDERUser, dev.IDERPassword = fp, srv.Addr, "admin", "pw"
	dev.Cfg.Settings.Features.IDER = true
	h := &mediaHarness{deviceHarness: newDevice(t, test.NewTempApp(t), dev, true), srv: srv,
		events: make(chan string, 256), bootGo: make(chan struct{}), peers: make(chan *idertest.Peer, 4), asked: make(chan func(string, bool), 4),
		clock: time.Now()}
	h.m = h.d.media
	h.m.onEvent = func(e string) { h.events <- e }
	h.m.ticker = func(each func()) func() { h.tick = each; return func() { h.tick = nil } }
	h.m.now = func() time.Time { return h.clock }
	h.m.beforeBoot = func() { <-h.bootGo }
	h.d.askConsent = func(answer func(string, bool)) func() { h.asked <- answer; return func() {} }
	return h
}

// wait reads events until want arrived.
func (h *mediaHarness) wait(t *testing.T, want string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-h.events:
			if e == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q event (phase %d, errors %d)", want, h.m.phase, len(h.errs))
		}
	}
}

// serve lets the fake device accept the next IDE-R session.
func (h *mediaHarness) serve(t *testing.T) {
	go func() { h.peers <- h.srv.AcceptSession(t) }()
}

// openPopover taps Media unless the popover is open.
func (h *mediaHarness) openPopover(t *testing.T) {
	t.Helper()
	if h.m.pop == nil {
		test.Tap(h.d.mediaBtn)
	}
	if h.m.pop == nil {
		t.Fatal("popover not open")
	}
}

// mountISO mounts the file at path through "Mount ISO file…" and returns the device side.
func (h *mediaHarness) mountISO(t *testing.T, path string) *idertest.Peer {
	t.Helper()
	h.m.pickISO = func(chosen func(string)) { chosen(path) }
	h.serve(t)
	h.openPopover(t)
	test.Tap(h.m.ui.iso)
	h.wait(t, "opened")
	if h.m.phase != mediaMounted {
		t.Fatalf("not mounted: phase %d, errors %d", h.m.phase, len(h.errs))
	}
	return <-h.peers
}

// tapCheck taps "Boot from this ISO on the next start" and waits for the session call. The
// call waits until the tap has returned: the test driver runs fyne.Do at once, and Check
// refreshes itself after OnChanged.
func (h *mediaHarness) tapCheck(t *testing.T) {
	t.Helper()
	test.Tap(h.m.ui.check)
	h.bootGo <- struct{}{}
	h.wait(t, "boot")
}

// question returns the buttons of the dialog on top of the window, failing unless its title is title.
func (h *mediaHarness) question(t *testing.T, title string) []*escButton {
	t.Helper()
	top := h.d.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatalf("no dialog %q", title)
	}
	findLabel(t, top, title)
	return panelButtons(top)
}

// labelText is the text of the first label containing part in the dialog on top, or "".
func labelText(w fyne.Window, part string) string {
	var got string
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if l, ok := o.(interface{ String() string }); ok && got == "" && strings.Contains(l.String(), part) {
			got = l.String()
		}
	})
	return got
}

func writeISO(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// sourceFolder is a small folder for a folder ISO (not bootable): two files of 64 KB.
func sourceFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "firmware-tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(dir, n), make([]byte, 64<<10), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func cachedISOs(t *testing.T) []string {
	t.Helper()
	cd, err := mkiso.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	m, _ := filepath.Glob(filepath.Join(cd, "*.iso"))
	return m
}

// TestMediaPopoverNothingMounted: the Media button opens the popover below it and stays active
// while it is open; Esc and a tap outside close it, a tap on the card does not.
func TestMediaPopoverNothingMounted(t *testing.T) {
	h := newMediaDevice(t, &session.Fake{})
	h.openPopover(t)
	if !h.d.mediaBtn.Active() || h.m.pop.width != mediaPopWidth {
		t.Fatalf("active %v width %v", h.d.mediaBtn.Active(), h.m.pop.width)
	}
	if h.m.ui.title.Text != "Media" || h.m.ui.iso.Text != "Mount ISO file…" || h.m.ui.folder.Text != "Mount folder as ISO…" ||
		!strings.HasPrefix(h.m.ui.hint.String(), "The device sees a read-only CD drive.") {
		t.Fatalf("texts %q %q %q %q", h.m.ui.title.Text, h.m.ui.iso.Text, h.m.ui.folder.Text, h.m.ui.hint.String())
	}
	if h.m.ui.iso.Icon() == nil || h.m.ui.folder.Icon() == nil || !h.m.ui.iso.Leading {
		t.Fatal("buttons need their symbols at the left")
	}
	// Media.dc.html .btn: white with a border, regular text
	if m := h.m.ui.iso.Metrics; m.Neutral != style.ColorNameField || m.NeutralBorder != style.ColorNameControlBorder || !m.Regular {
		t.Fatalf("button metrics %+v", *m)
	}
	btn := fyne.CurrentApp().Driver().AbsolutePositionForObject(h.d.mediaBtn)
	if card := h.m.pop.card.Position(); card.Y <= btn.Y || card.X > btn.X {
		t.Fatalf("card at %v, button at %v: card must hang below the button", card, btn)
	}
	inside := h.m.pop.card.Position().Add(fyne.NewPos(2, 2))
	h.m.pop.Tapped(&fyne.PointEvent{Position: inside})
	if h.m.pop == nil {
		t.Fatal("a tap on the card closed the popover")
	}
	h.m.pop.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if h.m.pop != nil || h.d.mediaBtn.Active() {
		t.Fatal("Esc did not close the popover")
	}
	h.openPopover(t)
	h.m.pop.Tapped(&fyne.PointEvent{Position: fyne.NewPos(1, 700)})
	if h.m.pop != nil || h.d.mediaBtn.Active() || len(h.d.win.Canvas().Overlays().List()) != 0 {
		t.Fatal("a tap outside did not close the popover")
	}
}

// TestMediaMountISOShowsStatistics: an ISO file is mounted (IDE-R on, StartNow); the popover,
// the status bar and Boot from ISO follow; the statistics come from the client once a second.
func TestMediaMountISOShowsStatistics(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	if !h.d.powerItems[amt.BootToISO].Disabled {
		t.Fatal("Boot from ISO enabled without an ISO")
	}
	p := h.mountISO(t, writeISO(t, "alpine-standard.iso", 1<<20))
	if st := dev.IDERStarts(); len(st) != 1 || st[0] != ider.StartNow {
		t.Fatalf("starts %v", st)
	}
	if h.m.pop == nil || h.m.pop.width != mediaPopWidthMounted || h.m.ui.title.Text != "alpine-standard.iso" || h.m.ui.size.Text != "1 MB" {
		t.Fatalf("mounted popover: title %q size %q", h.m.ui.title.Text, h.m.ui.size.Text)
	}
	if h.m.ui.check.Checked || h.m.ui.unmount.Text != "Unmount" || h.m.ui.restart.Text != "Restart from ISO…" || h.m.ui.restart.Disabled() {
		t.Fatalf("controls: check %v %q %q", h.m.ui.check.Checked, h.m.ui.unmount.Text, h.m.ui.restart.Text)
	}
	if !h.d.mediaLine.Visible() || h.d.mediaLine.Text != "ISO mounted · 0.0 MB/s" {
		t.Fatalf("status %q", h.d.mediaLine.Text)
	}
	if s := h.m.ui.stats.String(); !strings.HasPrefix(s, "Read 0 KB · 0.0 MB/s · connected ") || strings.Contains(s, "last read") {
		t.Fatalf("stats %q", s)
	}
	if h.d.powerItems[amt.BootToISO].Disabled {
		t.Fatal("Boot from ISO disabled while an ISO is mounted")
	}

	// The host reads the whole image (512 sectors) within one second.
	read10 := []byte{0x28, 0, 0, 0, 0, 0, 0, 0x02, 0, 0, 0, 0}
	p.Written(t, read10, 0x10, 0)
	for {
		if m := p.Read(t); m.Cmd == 0x54 && m.Attr == 3 {
			break
		}
	}
	c := h.m.mount.c
	waitFor(t, func() bool { return c.Stats().Reads == 1 })
	h.clock = h.clock.Add(time.Second)
	h.tick()
	if h.d.mediaLine.Text != "ISO mounted · 1.0 MB/s" {
		t.Fatalf("status %q", h.d.mediaLine.Text)
	}
	if s := h.m.ui.stats.String(); !strings.HasPrefix(s, "Read 1 MB · 1.0 MB/s · last read ") {
		t.Fatalf("stats %q", s)
	}
	// Without reads the rate falls to zero once the window has passed.
	for range 6 {
		h.clock = h.clock.Add(time.Second)
		h.tick()
	}
	if h.d.mediaLine.Text != "ISO mounted · 0.0 MB/s" {
		t.Fatalf("status after idle %q", h.d.mediaLine.Text)
	}
	if len(h.errs) != 0 {
		t.Fatalf("errors: %v", <-h.errs)
	}
}

// TestMediaTurnOnRedirection: with IDE-R off the window asks first; Cancel deletes the built
// image without mounting, Turn On mounts (OpenIDER turns IDE-R on).
func TestMediaTurnOnRedirection(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	dev.Cfg.Settings.Features.IDER = false
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }

	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	h.wait(t, "settings")
	bs := h.question(t, "Turn on media redirection?")
	if texts(bs) != lang.L("Cancel")+"|Turn On" ||
		labelText(h.d.win, "Media redirection (IDE-R) is turned off on “Büro”. Turn it on to mount ISO files and folders on this device?") == "" {
		t.Fatalf("buttons %q", texts(bs))
	}
	if len(cachedISOs(t)) != 1 || h.m.phase != mediaPreparing {
		t.Fatalf("built image %v, phase %d", cachedISOs(t), h.m.phase)
	}
	// The popover shows the finished build with its file count.
	if h.m.ui.progress.String() != "100 % · 131 KB of 131 KB · 2 files" || h.m.ui.bar.value != 1 ||
		h.m.ui.title.Text != "Preparing “firmware-tools”" {
		t.Fatalf("progress %q bar %v title %q", h.m.ui.progress.String(), h.m.ui.bar.value, h.m.ui.title.Text)
	}
	test.Tap(bs[0])
	if h.m.phase != mediaIdle || len(cachedISOs(t)) != 0 || len(dev.IDERStarts()) != 0 {
		t.Fatalf("Cancel: phase %d, images %v, OpenIDER calls %d", h.m.phase, cachedISOs(t), len(dev.IDERStarts()))
	}

	h.serve(t)
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	h.wait(t, "settings")
	test.Tap(h.question(t, "Turn on media redirection?")[1])
	h.wait(t, "opened")
	<-h.peers
	if h.m.phase != mediaMounted || h.m.ui.title.Text != "firmware-tools" || len(dev.IDERStarts()) != 1 {
		t.Fatalf("Turn On: phase %d title %q", h.m.phase, h.m.ui.title.Text)
	}
	if len(h.errs) != 0 {
		t.Fatalf("errors: %v", <-h.errs)
	}
}

// TestMediaOpenErrorClosesImage: a failed OpenIDER is reported and the image it did not take
// over is closed (a built one is deleted); a cancelled consent stays silent.
func TestMediaOpenErrorClosesImage(t *testing.T) {
	boom := errors.New("boom")
	dev := &session.Fake{IDERErr: boom}
	h := newMediaDevice(t, dev)
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	h.wait(t, "opened")
	if err := <-h.errs; !errors.Is(err, boom) || h.m.phase != mediaIdle || len(cachedISOs(t)) != 0 || h.d.mediaLine.Visible() {
		t.Fatalf("err %v phase %d images %v", err, h.m.phase, cachedISOs(t))
	}

	dev.IDERErr = nil
	dev.Consent, dev.ConsentCode = true, "123456"
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	(<-h.asked)("", false)
	h.wait(t, "opened")
	if h.m.phase != mediaIdle || len(h.errs) != 0 || len(cachedISOs(t)) != 0 {
		t.Fatalf("cancelled consent: phase %d errors %d images %v", h.m.phase, len(h.errs), cachedISOs(t))
	}
	h.serve(t)
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	(<-h.asked)("123456", true)
	h.wait(t, "opened")
	<-h.peers
	if h.m.phase != mediaMounted {
		t.Fatalf("consent given: phase %d", h.m.phase)
	}
}

// TestMediaFolderBuildCancel: the folder ISO is built with progress; Cancel stops the build and
// removes the partial file without an error.
func TestMediaFolderBuildCancel(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }
	progressed, release := make(chan struct{}), make(chan struct{})
	first := true
	h.m.onBuildProgress = func(done, total int64) { // build goroutine
		if first {
			first = false
			progressed <- struct{}{}
			<-release
		}
	}
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	<-progressed // the build waits in the hook: the window is ours
	if h.m.phase != mediaPreparing || h.m.pop == nil || h.m.ui.title.Text != "Preparing “firmware-tools”" || h.m.ui.bar == nil ||
		h.m.ui.cancel.Text != lang.L("Cancel") {
		t.Fatalf("preparing: phase %d", h.m.phase)
	}
	drawn := false // the bar's container itself is in the tree, so the driver draws it
	walk(h.m.pop.card, func(o fyne.CanvasObject) { drawn = drawn || o == h.m.ui.bar.Container })
	if !drawn {
		t.Fatal("progress bar not in the popover")
	}
	test.Tap(h.m.ui.cancel)
	if h.m.phase != mediaIdle || h.m.ui.title.Text != "Media" {
		t.Fatalf("Cancel: phase %d", h.m.phase)
	}
	close(release)
	h.wait(t, "prepared")
	if len(cachedISOs(t)) != 0 || len(h.errs) != 0 || len(dev.IDERStarts()) != 0 || h.m.phase != mediaIdle {
		t.Fatalf("after cancel: images %v errors %d", cachedISOs(t), len(h.errs))
	}
}

// TestMediaBootCheckbox: ticking sets the boot option for the next start, unticking clears it;
// a failure puts the checkbox back and is reported.
func TestMediaBootCheckbox(t *testing.T) {
	boom := errors.New("boom")
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	h.tapCheck(t)
	if n, c := dev.ISOCalls(); n != 1 || c != 0 || !h.m.ui.check.Checked || h.m.ui.check.Disabled() || !h.m.bootNext {
		t.Fatalf("on: calls %d %d checked %v", n, c, h.m.ui.check.Checked)
	}
	h.tapCheck(t)
	if n, c := dev.ISOCalls(); n != 1 || c != 1 || h.m.ui.check.Checked || h.m.bootNext {
		t.Fatalf("off: calls %d %d checked %v", n, c, h.m.ui.check.Checked)
	}
	dev.NextBootErr = boom
	h.tapCheck(t)
	if err := <-h.errs; !errors.Is(err, boom) || h.m.ui.check.Checked || h.m.bootNext || h.m.ui.check.Disabled() {
		t.Fatalf("failure: err %v checked %v", err, h.m.ui.check.Checked)
	}
}

// TestMediaUnmount closes the session and clears the boot option only if it was set.
func TestMediaUnmount(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	iso := writeISO(t, "alpine-standard.iso", 8192)
	h.mountISO(t, iso)
	c := h.m.mount.c
	test.Tap(h.m.ui.unmount)
	if h.m.phase != mediaIdle || h.m.ui.title.Text != "Media" || h.d.mediaLine.Visible() || !h.d.powerItems[amt.BootToISO].Disabled {
		t.Fatalf("after Unmount: phase %d status %q", h.m.phase, h.d.mediaLine.Text)
	}
	h.wait(t, "unmounted")
	if !errors.Is(c.Err(), ider.ErrClosed) {
		t.Fatalf("client err %v", c.Err())
	}
	if _, cl := dev.ISOCalls(); cl != 0 {
		t.Fatalf("ClearISOBoot without the checkbox: %d", cl)
	}

	h.mountISO(t, iso)
	h.tapCheck(t)
	test.Tap(h.m.ui.unmount)
	h.wait(t, "unmounted")
	if _, cl := dev.ISOCalls(); cl != 1 || len(h.errs) != 0 {
		t.Fatalf("ClearISOBoot after Unmount with the checkbox: %d", cl)
	}
}

// TestMediaSessionEndedByDevice: the device ends the session; the window returns to "nothing
// mounted", clears a set boot option and reports the cause.
func TestMediaSessionEndedByDevice(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	p := h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	h.tapCheck(t)
	p.Close(t) // CLOSE 0x43
	h.wait(t, "ended")
	if h.m.phase != mediaIdle || h.d.mediaLine.Visible() || h.m.ui.title.Text != "Media" {
		t.Fatalf("phase %d status %q", h.m.phase, h.d.mediaLine.Text)
	}
	if err := <-h.errs; !errors.Is(err, ider.ErrClosedByDevice) {
		t.Fatalf("reported %v", err)
	}
	h.wait(t, "cleared")
	if _, cl := dev.ISOCalls(); cl != 1 {
		t.Fatalf("ClearISOBoot %d", cl)
	}
}

// TestBootFromISOFollowsMount: "Boot from ISO…" sits between Reboot to BIOS and the separator
// before Shutdown and is enabled only while an ISO is mounted, also on a device that is off.
func TestBootFromISOFollowsMount(t *testing.T) {
	dev := &session.Fake{St: amt.Status{PowerState: models.PowerStateOffSoft}}
	h := newMediaDevice(t, dev)
	items := h.d.powerMenu.Items
	var i int
	for i = range items {
		if items[i] == h.d.powerItems[amt.BootToBIOS] {
			break
		}
	}
	if items[i+1] != h.d.powerItems[amt.BootToISO] || !items[i+2].IsSeparator || items[i+3] != h.d.powerItems[amt.PowerShutdown] {
		t.Fatal("Boot from ISO is not between Reboot to BIOS and the separator before Shutdown")
	}
	it := h.d.powerItems[amt.BootToISO]
	if it.Label != "Boot from ISO…" || !it.Disabled {
		t.Fatalf("%q disabled %v", it.Label, it.Disabled)
	}
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	if it.Disabled {
		t.Fatal("Boot from ISO disabled on a device that is off")
	}
	h.d.apply(amt.Status{PowerState: models.PowerStateOn}, nil)
	if it.Disabled {
		t.Fatal("Boot from ISO must stay enabled on a running device")
	}
}

// TestRestartFromISO asks once (not the general power confirmation) and sends BootToISO; the
// Power menu entry asks the same question.
func TestRestartFromISO(t *testing.T) {
	dev := &session.Fake{St: amt.Status{PowerState: models.PowerStateOn}}
	h := newMediaDevice(t, dev)
	h.d.confirm = func(amt.PowerAction, func()) { t.Fatal("general power confirmation shown") }
	h.show(t)
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	test.Tap(h.m.ui.restart)
	if h.m.pop != nil {
		t.Fatal("popover still open behind the question")
	}
	bs := h.question(t, "Restart “Büro” from the ISO?")
	if texts(bs) != lang.L("Cancel")+"|"+lang.L("Restart") ||
		labelText(h.d.win, "The device restarts now and boots once from “alpine-standard.iso”. Unsaved work on the device is lost.") == "" {
		t.Fatalf("buttons %q", texts(bs))
	}
	test.Tap(bs[0])
	if len(dev.Powered()) != 0 {
		t.Fatal("Cancel ran the action")
	}
	h.tapPower(t, amt.BootToISO)
	bs = h.question(t, "Restart “Büro” from the ISO?")
	test.Tap(bs[1])
	recv(t, h.actionDone)
	recv(t, h.applied) // the read after the action
	if got := dev.Powered(); len(got) != 1 || got[0] != amt.BootToISO {
		t.Fatalf("powered %v", got)
	}
	if h.m.phase != mediaMounted {
		t.Fatal("the restart unmounted the ISO")
	}
}

// TestCloseWindowWithISOAsks: closing the window with an ISO mounted asks; Keep Open keeps
// both, Unmount and Close closes the window, unmounts and clears the set boot option. Without
// an ISO the window closes at once.
func TestCloseWindowWithISOAsks(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	h.tapCheck(t)
	c := h.m.mount.c

	interceptOf(h.d.win)()
	bs := h.question(t, "An ISO is still mounted")
	if texts(bs) != "Keep Open|Unmount and Close" ||
		labelText(h.d.win, "“alpine-standard.iso” is mounted on “Büro”. Closing the window unmounts it.") == "" {
		t.Fatalf("buttons %q", texts(bs))
	}
	interceptOf(h.d.win)() // a second close request while the question is open adds nothing
	test.Tap(bs[0])
	if h.wasClosed || h.m.phase != mediaMounted || c.Err() != nil {
		t.Fatal("Keep Open closed the window or the ISO")
	}
	interceptOf(h.d.win)()
	test.Tap(h.question(t, "An ISO is still mounted")[1])
	recv(t, h.closed)
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ISO not unmounted")
	}
	waitFor(t, func() bool { _, cl := dev.ISOCalls(); return cl == 1 })
	if h.m.phase != mediaIdle {
		t.Fatalf("phase %d", h.m.phase)
	}

	h2 := newMediaDevice(t, &session.Fake{})
	interceptOf(h2.d.win)()
	recv(t, h2.closed)
}

// TestQuitGuardRoutesDeviceCloseThroughMedia: the start window's close intercept of a device
// window asks while an ISO is mounted on it.
func TestQuitGuardRoutesDeviceCloseThroughMedia(t *testing.T) {
	q := newQuitHarness(t)
	d := q.addDevice("a", false)
	d.media = &mediaPanel{d: d, phase: mediaMounted, name: "alpine-standard.iso"}
	q.pass(d.win)
	if q.closed["a"] || d.media.closeAsk == nil {
		t.Fatal("closed without the question")
	}
	test.Tap(panelButtons(d.win.Canvas().Overlays().Top())[0]) // Keep Open
	if q.closed["a"] {
		t.Fatal("Keep Open closed the window")
	}
	q.pass(d.win)
	test.Tap(panelButtons(d.win.Canvas().Overlays().Top())[1]) // Unmount and Close
	if !q.closed["a"] {
		t.Fatal("Unmount and Close did not close the window")
	}
}

// TestMismatchUnmounts: a certificate mismatch aborts the window and unmounts without touching
// the boot option over the suspicious connection.
func TestMismatchUnmounts(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	h.tapCheck(t)
	c := h.m.mount.c
	h.d.apply(amt.Status{}, &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)})
	if h.m.phase != mediaIdle || h.m.pop != nil || !h.d.mediaBtn.Disabled() || h.d.mediaLine.Visible() {
		t.Fatalf("after abort: phase %d", h.m.phase)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ISO not unmounted")
	}
	if _, cl := dev.ISOCalls(); cl != 0 {
		t.Fatalf("ClearISOBoot after a mismatch: %d", cl)
	}
}

func TestMediaTexts(t *testing.T) {
	englishUI(t)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		n    int64
		want string
	}{{0, "0 KB"}, {640_000, "640 KB"}, {999_400, "999 KB"}, {214_000_000, "214 MB"}, {999_400_000, "999 MB"},
		{999_600_000, "1.0 GB"}, {1_049_000_000, "1.0 GB"}, {2_850_000_000, "2.9 GB"}} {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	for _, c := range []struct {
		bps  float64
		want string
	}{{0, "0.0 MB/s"}, {-1, "0.0 MB/s"}, {18_240_000, "18.2 MB/s"}, {49_999, "0.0 MB/s"}, {50_000, "0.1 MB/s"}} {
		if got := formatRate(c.bps); got != c.want {
			t.Errorf("formatRate(%v) = %q, want %q", c.bps, got, c.want)
		}
	}
	for _, c := range []struct {
		s    ider.Stats
		rate float64
		want string
	}{
		{ider.Stats{Started: now.Add(-6 * time.Minute)}, 0, "Read 0 KB · 0.0 MB/s · connected 6 min"},
		{ider.Stats{Started: now.Add(-6*time.Minute - 59*time.Second), BytesRead: 118_000_000, LastRead: now.Add(-3 * time.Second)},
			18_200_000, "Read 118 MB · 18.2 MB/s · last read 3 s ago · connected 6 min"},
		{ider.Stats{Started: now.Add(-65 * time.Minute), BytesRead: 4096, LastRead: now.Add(-2 * time.Minute)},
			0, "Read 4 KB · 0.0 MB/s · last read 2 min ago · connected 1 h 5 min"},
		{ider.Stats{Started: now.Add(-12 * time.Second)}, 0, "Read 0 KB · 0.0 MB/s · connected 12 s"},
	} {
		if got := statsLine(c.s, c.rate, now); got != c.want {
			t.Errorf("statsLine = %q, want %q", got, c.want)
		}
	}
	for _, c := range []struct {
		done, total int64
		info        *mkiso.Info
		want        string
	}{
		{640_000_000, 1_032_000_000, nil, "62 % · 640 MB of 1.0 GB"},
		{0, 0, nil, "0 % · 0 KB of 0 KB"},
		{1_000_000, 1_000_000, &mkiso.Info{Files: 84, Bootable: true}, "100 % · 1 MB of 1 MB · 84 files · UEFI-bootable"},
		{0, 0, &mkiso.Info{Files: 1}, "100 % · 0 KB of 0 KB · 1 file"},
	} {
		if got := progressLine(c.done, c.total, c.info); got != c.want {
			t.Errorf("progressLine = %q, want %q", got, c.want)
		}
	}
}

func TestRateMeterUsesTheLastSeconds(t *testing.T) {
	t0 := time.Now()
	var r rateMeter
	if r.rate() != 0 {
		t.Fatal("rate without samples")
	}
	r.add(t0, 0)
	if r.rate() != 0 {
		t.Fatal("rate with one sample")
	}
	for i := 1; i <= 10; i++ {
		r.add(t0.Add(time.Duration(i)*time.Second), int64(i)*1_000_000)
	}
	if got := r.rate(); got != 1_000_000 || len(r.samples) != 6 {
		t.Fatalf("rate %v over %d samples", got, len(r.samples))
	}
	for i := 11; i <= 20; i++ {
		r.add(t0.Add(time.Duration(i)*time.Second), 10_000_000) // no further reads
	}
	if r.rate() != 0 {
		t.Fatalf("rate after idle %v", r.rate())
	}
}

// TestExitWaitsForUnmounting: after "Unmount and Close" the work the process waits for at exit
// ends only once the folder ISO is deleted and the boot option cleared.
func TestExitWaitsForUnmounting(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	h.d.exitWork = &pendingWork{}
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }
	h.serve(t)
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	h.wait(t, "opened")
	<-h.peers
	h.tapCheck(t)
	if len(cachedISOs(t)) != 1 {
		t.Fatalf("folder ISO %v", cachedISOs(t))
	}
	interceptOf(h.d.win)()
	test.Tap(h.question(t, "An ISO is still mounted")[1])
	if !h.d.exitWork.wait(5 * time.Second) {
		t.Fatal("unmounting did not finish")
	}
	if _, cl := dev.ISOCalls(); cl != 1 || len(cachedISOs(t)) != 0 {
		t.Fatalf("at exit: ClearISOBoot %d, images %v", cl, cachedISOs(t))
	}
}

// TestExitWaitsForACancelledFolderBuild: a folder ISO that is still being built when the window
// closes is removed before the process may exit.
func TestExitWaitsForACancelledFolderBuild(t *testing.T) {
	h := newMediaDevice(t, &session.Fake{})
	h.d.exitWork = &pendingWork{}
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }
	progressed, release := make(chan struct{}), make(chan struct{})
	first := true
	h.m.onBuildProgress = func(done, total int64) {
		if first {
			first = false
			progressed <- struct{}{}
			<-release
		}
	}
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	<-progressed
	interceptOf(h.d.win)()
	test.Tap(h.question(t, "An ISO is still mounted")[1])
	recv(t, h.closed)
	if h.d.exitWork.wait(10 * time.Millisecond) {
		t.Fatal("did not wait for the build")
	}
	close(release)
	if !h.d.exitWork.wait(5*time.Second) || len(cachedISOs(t)) != 0 {
		t.Fatalf("images %v", cachedISOs(t))
	}
}

// TestCloseWhileSettingTheBootOptionClearsItAfterwards: the window closes while "Boot from this
// ISO on the next start" is still being set; the boot option is cleared after it was set, not
// before, so it does not stay set without the image.
func TestCloseWhileSettingTheBootOptionClearsItAfterwards(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	h.d.exitWork = &pendingWork{}
	clearsAtBoot := make(chan int, 1)
	h.m.beforeBoot = func() {
		<-h.bootGo
		_, cl := dev.ISOCalls()
		clearsAtBoot <- cl
	}
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	c := h.m.mount.c
	test.Tap(h.m.ui.check) // NextBootFromISO waits for bootGo
	interceptOf(h.d.win)()
	test.Tap(h.question(t, "An ISO is still mounted")[1])
	recv(t, h.closed)
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ISO not unmounted")
	}
	// Give an early ClearISOBoot of the shutdown the time to run.
	for end := time.Now().Add(100 * time.Millisecond); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if _, cl := dev.ISOCalls(); cl != 0 {
			break
		}
	}
	h.bootGo <- struct{}{}
	if cl := <-clearsAtBoot; cl != 0 {
		t.Fatalf("ClearISOBoot ran before NextBootFromISO (%d)", cl)
	}
	if !h.d.exitWork.wait(5 * time.Second) {
		t.Fatal("unmounting did not finish")
	}
	if n, cl := dev.ISOCalls(); n != 1 || cl != 1 {
		t.Fatalf("NextBootFromISO %d, ClearISOBoot %d", n, cl)
	}
}

// TestUnmountWhileSettingTheBootOptionClearsItAfterwards: the same for Unmount.
func TestUnmountWhileSettingTheBootOptionClearsItAfterwards(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	clearsAtBoot := make(chan int, 1)
	h.m.beforeBoot = func() {
		<-h.bootGo
		_, cl := dev.ISOCalls()
		clearsAtBoot <- cl
	}
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	c := h.m.mount.c
	test.Tap(h.m.ui.check)
	test.Tap(h.m.ui.unmount)
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ISO not unmounted")
	}
	for end := time.Now().Add(100 * time.Millisecond); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if _, cl := dev.ISOCalls(); cl != 0 {
			break
		}
	}
	h.bootGo <- struct{}{}
	if cl := <-clearsAtBoot; cl != 0 {
		t.Fatalf("ClearISOBoot ran before NextBootFromISO (%d)", cl)
	}
	h.wait(t, "unmounted")
	if n, cl := dev.ISOCalls(); n != 1 || cl != 1 || len(h.errs) != 0 {
		t.Fatalf("NextBootFromISO %d, ClearISOBoot %d", n, cl)
	}
}

// TestClosingThePopoverKeepsTheQuestionAboveIt: closing the popover (as a KVM resume does
// through closeMenus) while "Turn on media redirection?" is shown above it keeps the question;
// Esc (Cancel) still closes the image.
func TestClosingThePopoverKeepsTheQuestionAboveIt(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	dev.Cfg.Settings.Features.IDER = false
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }
	h.openPopover(t)
	test.Tap(h.m.ui.folder)
	h.wait(t, "settings")
	q := h.m.askIDER
	if q == nil || h.m.pop == nil {
		t.Fatal("no question over the popover")
	}
	h.d.closeMenus()
	if h.m.pop != nil {
		t.Fatal("popover still open")
	}
	h.question(t, "Turn on media redirection?")
	if q.done || !q.pop.Visible() || len(h.d.win.Canvas().Overlays().List()) != 1 {
		t.Fatal("the question went with the popover")
	}
	if f := h.d.win.Canvas().Focused(); f != nil {
		f.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	} else {
		h.d.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
	}
	if h.m.askIDER != nil || h.m.phase != mediaIdle || len(cachedISOs(t)) != 0 {
		t.Fatalf("Cancel: phase %d images %v", h.m.phase, cachedISOs(t))
	}
}

// TestEscClosesThePopoverAfterTheCheckbox: the checkbox takes the focus when tapped; Esc still
// closes the popover.
func TestEscClosesThePopoverAfterTheCheckbox(t *testing.T) {
	h := newMediaDevice(t, &session.Fake{})
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	h.tapCheck(t)
	f := h.d.win.Canvas().Focused()
	if f == nil {
		t.Fatal("nothing focused after the tap")
	}
	f.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if h.m.pop != nil {
		t.Fatal("Esc did not close the popover")
	}
}
