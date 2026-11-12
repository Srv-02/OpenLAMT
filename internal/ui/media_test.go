package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/ider/idertest"
	"github.com/Srv-02/openlamt/internal/mkiso"
	"github.com/Srv-02/openlamt/internal/session"
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
	h.d.settings.SetHideFolderMountHint(true) // the hint has its own tests
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

// openMenu taps Media unless the menu is open.
func (h *mediaHarness) openMenu(t *testing.T) {
	t.Helper()
	if h.m.menu == nil {
		test.Tap(h.d.mediaBtn)
	}
	if h.m.menu == nil {
		t.Fatal("menu not open")
	}
}

// The items of the Media menu, for tapItem.
var (
	itemISO     = func(i mediaItems) *fyne.MenuItem { return i.iso }
	itemFolder  = func(i mediaItems) *fyne.MenuItem { return i.folder }
	itemBoot    = func(i mediaItems) *fyne.MenuItem { return i.boot }
	itemRestart = func(i mediaItems) *fyne.MenuItem { return i.restart }
	itemUnmount = func(i mediaItems) *fyne.MenuItem { return i.unmount }
	itemCancel  = func(i mediaItems) *fyne.MenuItem { return i.cancel }
)

// tapItem opens the Media menu and taps the item that pick takes from its state.
func (h *mediaHarness) tapItem(t *testing.T, pick func(mediaItems) *fyne.MenuItem) {
	t.Helper()
	h.openMenu(t)
	it := pick(h.m.items)
	if it == nil {
		t.Fatalf("item not in the menu of phase %d", h.m.phase)
	}
	test.Tap(h.menuRow(t, it))
}

// menuRow is the row of it in the open menu.
func (h *mediaHarness) menuRow(t *testing.T, it *fyne.MenuItem) fyne.Tappable {
	t.Helper()
	for i, x := range h.m.shown.Items {
		if x == it {
			return h.m.menu.Item(i)
		}
	}
	t.Fatalf("item %q not shown", it.Label)
	return nil
}

// labels are the labels of the open menu's items, "|" for a separator.
func (h *mediaHarness) labels() string {
	var l []string
	for _, it := range h.m.shown.Items {
		if it.IsSeparator {
			l = append(l, "|")
		} else {
			l = append(l, it.Label)
		}
	}
	return strings.Join(l, " ")
}

// mountISO mounts the file at path through "Mount ISO…" and returns the device side.
func (h *mediaHarness) mountISO(t *testing.T, path string) *idertest.Peer {
	t.Helper()
	h.m.pickISO = func(chosen func(string)) { chosen(path) }
	h.serve(t)
	h.tapItem(t, itemISO)
	h.wait(t, "opened")
	if h.m.phase != mediaMounted {
		t.Fatalf("not mounted: phase %d, errors %d", h.m.phase, len(h.errs))
	}
	return <-h.peers
}

// tapBoot taps "Boot from ISO on next start" and waits for the session call. The call waits
// until the tap has returned: the test driver runs fyne.Do at once.
func (h *mediaHarness) tapBoot(t *testing.T) {
	t.Helper()
	h.tapItem(t, itemBoot)
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

// TestMediaMenuNothingMounted: the Media button opens a drop-down menu below it like Power, with
// "Mount ISO…" and "Mount Folder…"; Esc and a tap outside close it.
func TestMediaMenuNothingMounted(t *testing.T) {
	h := newMediaDevice(t, &session.Fake{})
	h.openMenu(t)
	if h.labels() != "Mount ISO… Mount Folder…" || h.m.line != nil {
		t.Fatalf("items %q, info line %v", h.labels(), h.m.line)
	}
	if h.m.items.iso.Icon == nil || h.m.items.folder.Icon == nil {
		t.Fatal("items need their symbols")
	}
	if h.d.win.Canvas().Overlays().Top() != h.m.menu || h.d.win.Canvas().Focused() != h.m.menu {
		t.Fatal("menu not on top or not focused")
	}
	// The first item sits where the Power menu's first item sits relative to its button.
	abs := fyne.CurrentApp().Driver().AbsolutePositionForObject
	media := abs(h.m.menu.Item(0).(fyne.CanvasObject)).Subtract(abs(h.d.mediaBtn))
	h.m.dismiss()
	h.d.showPowerMenu()
	power := abs(h.d.powerPopup.Item(0).(fyne.CanvasObject)).Subtract(abs(h.d.powerBtn))
	h.d.powerPopup.Dismiss()
	if media != power {
		t.Fatalf("first item at %v below Media, at %v below Power", media, power)
	}
	h.openMenu(t)
	h.m.menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if h.m.menu != nil || h.m.shown != nil {
		t.Fatal("Esc did not close the menu")
	}
	h.openMenu(t)
	h.m.menu.Tapped(&fyne.PointEvent{Position: fyne.NewPos(1, 700)})
	if h.m.menu != nil || len(h.d.win.Canvas().Overlays().List()) != 0 {
		t.Fatal("a tap outside did not close the menu")
	}
	// The Media button toggles the menu.
	test.Tap(h.d.mediaBtn)
	test.Tap(h.d.mediaBtn)
	if h.m.menu != nil {
		t.Fatal("the Media button did not close its menu")
	}
}

// TestMediaMountISOShowsStatistics: an ISO file is mounted (IDE-R on, StartNow); the menu, the
// status bar and Boot from ISO follow; the read rate comes from the client once a second and
// follows in the open menu.
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
	// The menu opened for the mount and shows the mounted state now.
	if h.m.menu == nil || h.labels() != "| Boot from ISO on next start Restart from ISO… | Unmount" {
		t.Fatalf("mounted menu %q", h.labels())
	}
	if h.m.line.Name() != "alpine-standard.iso" || h.m.line.Detail() != "0.0 MB/s" {
		t.Fatalf("info line %q %q", h.m.line.Name(), h.m.line.Detail())
	}
	if _, bar := h.m.line.Progress(); bar {
		t.Fatal("progress bar while mounted")
	}
	it := h.m.items
	if it.boot.Checked || it.boot.Disabled || it.boot.Icon != nil || it.restart.Disabled || it.restart.Icon == nil || it.unmount.Icon == nil {
		t.Fatalf("items: boot %v/%v restart %v", it.boot.Checked, it.boot.Disabled, it.restart.Disabled)
	}
	if !h.d.mediaLine.Visible() || h.d.mediaLine.Text != "ISO mounted · 0.0 MB/s" {
		t.Fatalf("status %q", h.d.mediaLine.Text)
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
	if h.d.mediaLine.Text != "ISO mounted · 1.0 MB/s" || h.m.line.Detail() != "1.0 MB/s" {
		t.Fatalf("status %q, menu %q", h.d.mediaLine.Text, h.m.line.Detail())
	}
	// Without reads the rate falls to zero once the window has passed.
	h.m.dismiss()
	for range 6 {
		h.clock = h.clock.Add(time.Second)
		h.tick()
	}
	if h.d.mediaLine.Text != "ISO mounted · 0.0 MB/s" {
		t.Fatalf("status after idle %q", h.d.mediaLine.Text)
	}
	h.openMenu(t)
	if h.m.line.Detail() != "0.0 MB/s" {
		t.Fatalf("menu after idle %q", h.m.line.Detail())
	}
	if len(h.errs) != 0 {
		t.Fatalf("errors: %v", <-h.errs)
	}
}

// TestMediaEnableRedirection: with IDE-R off the window asks first; Cancel deletes the built
// image without mounting, Enable mounts (OpenIDER enables IDE-R).
func TestMediaEnableRedirection(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	dev.Cfg.Settings.Features.IDER = false
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }

	h.tapItem(t, itemFolder)
	h.wait(t, "settings")
	bs := h.question(t, "Enable media redirection?")
	if texts(bs) != lang.L("Cancel")+"|Enable" ||
		labelText(h.d.win, "Media redirection (IDE-R) is disabled on “Büro”. Enable it to mount ISO files and folders on this device?") == "" {
		t.Fatalf("buttons %q", texts(bs))
	}
	if len(cachedISOs(t)) != 1 || h.m.phase != mediaPreparing {
		t.Fatalf("built image %v, phase %d", cachedISOs(t), h.m.phase)
	}
	// The menu below the question shows the finished build.
	if v, bar := h.m.line.Progress(); h.m.line.Name() != "firmware-tools" || h.m.line.Detail() != "100 %" || v != 1 || !bar {
		t.Fatalf("info line %q %q bar %v %v", h.m.line.Name(), h.m.line.Detail(), v, bar)
	}
	test.Tap(bs[0])
	if h.m.phase != mediaIdle || len(cachedISOs(t)) != 0 || len(dev.IDERStarts()) != 0 {
		t.Fatalf("Cancel: phase %d, images %v, OpenIDER calls %d", h.m.phase, cachedISOs(t), len(dev.IDERStarts()))
	}

	h.serve(t)
	h.tapItem(t, itemFolder)
	h.wait(t, "settings")
	test.Tap(h.question(t, "Enable media redirection?")[1])
	h.wait(t, "opened")
	<-h.peers
	if h.m.phase != mediaMounted || h.m.line.Name() != "firmware-tools" || len(dev.IDERStarts()) != 1 {
		t.Fatalf("Enable: phase %d", h.m.phase)
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
	h.tapItem(t, itemFolder)
	h.wait(t, "opened")
	if err := <-h.errs; !errors.Is(err, boom) || h.m.phase != mediaIdle || len(cachedISOs(t)) != 0 || h.d.mediaLine.Visible() {
		t.Fatalf("err %v phase %d images %v", err, h.m.phase, cachedISOs(t))
	}

	dev.IDERErr = nil
	dev.Consent, dev.ConsentCode = true, "123456"
	h.tapItem(t, itemFolder)
	(<-h.asked)("", false)
	h.wait(t, "opened")
	if h.m.phase != mediaIdle || len(h.errs) != 0 || len(cachedISOs(t)) != 0 {
		t.Fatalf("cancelled consent: phase %d errors %d images %v", h.m.phase, len(h.errs), cachedISOs(t))
	}
	h.serve(t)
	h.tapItem(t, itemFolder)
	(<-h.asked)("123456", true)
	h.wait(t, "opened")
	<-h.peers
	if h.m.phase != mediaMounted {
		t.Fatalf("consent given: phase %d", h.m.phase)
	}
}

// TestMediaFolderBuildCancel: the folder ISO is built with progress in the menu (name,
// percentage, bar, Cancel); Cancel stops the build and removes the partial file without an error.
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
	h.tapItem(t, itemFolder)
	<-progressed // the build waits in the hook: the window is ours
	if h.m.phase != mediaPreparing || h.m.menu == nil || h.labels() != "| "+lang.L("Cancel") || h.m.line.Name() != "firmware-tools" {
		t.Fatalf("preparing: phase %d items %q", h.m.phase, h.labels())
	}
	if v, bar := h.m.line.Progress(); !bar || v != 0 || h.m.line.Detail() != "" {
		t.Fatalf("before the first progress: %v %v %q", v, bar, h.m.line.Detail())
	}
	h.m.done, h.m.total = 640_000_000, 1_030_000_000
	h.m.refreshProgress()
	if v, _ := h.m.line.Progress(); h.m.line.Detail() != "62 %" || v < 0.62 || v > 0.63 {
		t.Fatalf("progress %q %v", h.m.line.Detail(), v)
	}
	h.tapItem(t, itemCancel)
	if h.m.phase != mediaIdle || h.m.menu != nil {
		t.Fatalf("Cancel: phase %d", h.m.phase)
	}
	close(release)
	h.wait(t, "prepared")
	if len(cachedISOs(t)) != 0 || len(h.errs) != 0 || len(dev.IDERStarts()) != 0 || h.m.phase != mediaIdle {
		t.Fatalf("after cancel: images %v errors %d", cachedISOs(t), len(h.errs))
	}
	h.openMenu(t)
	if h.labels() != "Mount ISO… Mount Folder…" {
		t.Fatalf("after cancel: %q", h.labels())
	}
}

// TestMediaPreparingISO: an ISO file being mounted shows its name and Cancel, without a
// percentage or bar (the progress is not known).
func TestMediaPreparingISO(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	dev.Cfg.Settings.Features.IDER = false
	h.m.pickISO = func(chosen func(string)) { chosen(writeISO(t, "alpine-standard.iso", 8192)) }
	h.tapItem(t, itemISO)
	h.wait(t, "settings")
	if _, bar := h.m.line.Progress(); h.m.phase != mediaPreparing || h.labels() != "| "+lang.L("Cancel") ||
		h.m.line.Name() != "alpine-standard.iso" || h.m.line.Detail() != "" || bar {
		t.Fatalf("preparing ISO: %q %q bar %v", h.labels(), h.m.line.Detail(), bar)
	}
	h.question(t, "Enable media redirection?")
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[0])
	if h.m.phase != mediaIdle {
		t.Fatalf("phase %d", h.m.phase)
	}
}

// TestMediaBootItem: "Boot from ISO on next start" sets the boot option for the next start and
// shows a check mark; again clears it; it is locked while the call runs; a failure takes the
// check mark back and is reported.
func TestMediaBootItem(t *testing.T) {
	boom := errors.New("boom")
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	h.tapItem(t, itemBoot)
	h.openMenu(t)
	if !h.m.items.boot.Checked || !h.m.items.boot.Disabled {
		t.Fatalf("while setting: checked %v disabled %v", h.m.items.boot.Checked, h.m.items.boot.Disabled)
	}
	h.bootGo <- struct{}{}
	h.wait(t, "boot")
	if n, c := dev.ISOCalls(); n != 1 || c != 0 || !h.m.items.boot.Checked || h.m.items.boot.Disabled || !h.m.bootNext {
		t.Fatalf("on: calls %d %d checked %v", n, c, h.m.items.boot.Checked)
	}
	h.tapBoot(t)
	h.openMenu(t)
	if n, c := dev.ISOCalls(); n != 1 || c != 1 || h.m.items.boot.Checked || h.m.bootNext {
		t.Fatalf("off: calls %d %d checked %v", n, c, h.m.items.boot.Checked)
	}
	dev.NextBootErr = boom
	h.tapBoot(t)
	h.openMenu(t)
	if err := <-h.errs; !errors.Is(err, boom) || h.m.items.boot.Checked || h.m.bootNext || h.m.items.boot.Disabled {
		t.Fatalf("failure: err %v checked %v", err, h.m.items.boot.Checked)
	}
}

// TestMediaUnmount closes the session and clears the boot option only if it was set.
func TestMediaUnmount(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	iso := writeISO(t, "alpine-standard.iso", 8192)
	h.mountISO(t, iso)
	c := h.m.mount.c
	h.tapItem(t, itemUnmount)
	if h.m.phase != mediaIdle || h.m.menu != nil || h.d.mediaLine.Visible() || !h.d.powerItems[amt.BootToISO].Disabled {
		t.Fatalf("after Unmount: phase %d status %q", h.m.phase, h.d.mediaLine.Text)
	}
	h.wait(t, "unmounted")
	if !errors.Is(c.Err(), ider.ErrClosed) {
		t.Fatalf("client err %v", c.Err())
	}
	if _, cl := dev.ISOCalls(); cl != 0 {
		t.Fatalf("ClearISOBoot without the boot option: %d", cl)
	}

	h.mountISO(t, iso)
	h.tapBoot(t)
	h.tapItem(t, itemUnmount)
	h.wait(t, "unmounted")
	if _, cl := dev.ISOCalls(); cl != 1 || len(h.errs) != 0 {
		t.Fatalf("ClearISOBoot after Unmount with the boot option: %d", cl)
	}
}

// TestMediaSessionEndedByDevice: the device ends the session; the window returns to "nothing
// mounted", clears a set boot option and reports the cause.
func TestMediaSessionEndedByDevice(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	p := h.mountISO(t, writeISO(t, "alpine-standard.iso", 8192))
	h.tapBoot(t)
	h.openMenu(t)
	p.Close(t) // CLOSE 0x43
	h.wait(t, "ended")
	if h.m.phase != mediaIdle || h.d.mediaLine.Visible() || h.m.menu == nil || h.labels() != "Mount ISO… Mount Folder…" {
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

// TestMediaButtonActiveWhileMounted: the Media button shows the active accent exactly while an
// image is mounted: not while idle or while a folder ISO is built, and no longer after Unmount
// or after the device ended the session.
func TestMediaButtonActiveWhileMounted(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	want := func(step string, on bool) {
		t.Helper()
		if h.d.mediaBtn.Active() != on {
			t.Fatalf("%s: Media active %v, want %v (phase %d)", step, !on, on, h.m.phase)
		}
	}
	want("idle", false)

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
	h.tapItem(t, itemFolder)
	<-progressed
	want("building", false)
	h.tapItem(t, itemCancel)
	close(release)
	h.wait(t, "prepared")
	want("cancelled", false)

	iso := writeISO(t, "alpine-standard.iso", 8192)
	h.mountISO(t, iso)
	want("mounted", true)
	h.tick()
	want("reading", true)
	h.tapItem(t, itemUnmount)
	want("unmounted", false)
	h.wait(t, "unmounted")

	p := h.mountISO(t, iso)
	want("mounted again", true)
	p.Close(t)
	h.wait(t, "ended")
	want("ended by the device", false)
	if err := <-h.errs; !errors.Is(err, ider.ErrClosedByDevice) {
		t.Fatalf("reported %v", err)
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
	h.tapItem(t, itemRestart)
	if h.m.menu != nil {
		t.Fatal("menu still open behind the question")
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
	h.tapBoot(t)
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
	h.tapBoot(t)
	c := h.m.mount.c
	h.d.apply(amt.Status{}, &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)})
	if h.m.phase != mediaIdle || h.m.menu != nil || !h.d.mediaBtn.Disabled() || h.d.mediaLine.Visible() {
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
	for _, c := range []struct {
		bps  float64
		want string
	}{{0, "0.0 MB/s"}, {-1, "0.0 MB/s"}, {18_240_000, "18.2 MB/s"}, {49_999, "0.0 MB/s"}, {50_000, "0.1 MB/s"}} {
		if got := formatRate(c.bps); got != c.want {
			t.Errorf("formatRate(%v) = %q, want %q", c.bps, got, c.want)
		}
	}
	for _, c := range []struct {
		done, total int64
		built       bool
		frac        float32
		want        string
	}{
		{640_000_000, 1_032_000_000, false, 640_000_000.0 / 1_032_000_000, "62 %"},
		{0, 0, false, 0, ""},
		{1_000_000, 1_000_000, true, 1, "100 %"},
		{0, 0, true, 1, "100 %"},
	} {
		if frac, got := buildProgress(c.done, c.total, c.built); got != c.want || frac != c.frac {
			t.Errorf("buildProgress(%d, %d, %v) = %v %q, want %v %q", c.done, c.total, c.built, frac, got, c.frac, c.want)
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
	h.tapItem(t, itemFolder)
	h.wait(t, "opened")
	<-h.peers
	h.tapBoot(t)
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
	h.tapItem(t, itemFolder)
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

// TestCloseWhileSettingTheBootOptionClearsItAfterwards: the window closes while "Boot from ISO
// on next start" is still being set; the boot option is cleared after it was set, not
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
	h.tapItem(t, itemBoot) // NextBootFromISO waits for bootGo
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
	h.tapItem(t, itemBoot)
	h.tapItem(t, itemUnmount)
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

// TestClosingTheMenuKeepsTheQuestionAboveIt: closing the menu (as a KVM resume does through
// closeMenus) while "Enable media redirection?" is shown above it keeps the question; Esc
// (Cancel) still closes the image.
func TestClosingTheMenuKeepsTheQuestionAboveIt(t *testing.T) {
	dev := &session.Fake{}
	h := newMediaDevice(t, dev)
	dev.Cfg.Settings.Features.IDER = false
	dir := sourceFolder(t)
	h.m.pickFolder = func(chosen func(string)) { chosen(dir) }
	h.tapItem(t, itemFolder)
	h.wait(t, "settings")
	q := h.m.askIDER
	if q == nil || h.m.menu == nil {
		t.Fatal("no question over the menu")
	}
	h.d.closeMenus()
	if h.m.menu != nil {
		t.Fatal("menu still open")
	}
	h.question(t, "Enable media redirection?")
	if q.done || !q.pop.Visible() || len(h.d.win.Canvas().Overlays().List()) != 1 {
		t.Fatal("the question went with the menu")
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

// TestMountFolderHint: "Mount Folder…" shows "Mount a folder" before the folder dialog; Cancel
// ends there, "Choose Folder…" opens the folder dialog and stores a ticked "Don’t show again",
// after which the hint is skipped.
func TestMountFolderHint(t *testing.T) {
	h := newMediaDevice(t, &session.Fake{})
	h.d.settings.SetHideFolderMountHint(false)
	picked := 0
	h.m.pickFolder = func(func(string)) { picked++ }

	h.tapItem(t, itemFolder)
	bs := h.question(t, "Mount a folder")
	if texts(bs) != lang.L("Cancel")+"|Choose Folder…" || picked != 0 {
		t.Fatalf("buttons %q, picked %d", texts(bs), picked)
	}
	shown, _, _ := dialogTexts(h.d.win)
	for _, want := range []string{"The folder is mounted on the device as a CD drive.", "Read only",
		"Each file must be smaller than 4 GB", "Large folders take a moment to prepare"} {
		if !slices.Contains(shown, want) {
			t.Errorf("no text %q in %q", want, shown)
		}
	}
	if bs[1].Importance != widget.HighImportance {
		t.Fatal("Choose Folder… is not the blue button")
	}
	check := hintCheck(t, h.d.win)
	if check.Text != "Don’t show again" || check.Checked {
		t.Fatalf("checkbox %q %v", check.Text, check.Checked)
	}
	test.Tap(bs[0]) // Cancel
	if picked != 0 || h.d.settings.HideFolderMountHint() || h.m.phase != mediaIdle {
		t.Fatalf("Cancel: picked %d", picked)
	}

	// Choose Folder… without the box: the hint stays.
	h.tapItem(t, itemFolder)
	test.Tap(h.question(t, "Mount a folder")[1])
	if picked != 1 || h.d.settings.HideFolderMountHint() {
		t.Fatalf("picked %d hidden %v", picked, h.d.settings.HideFolderMountHint())
	}
	// With the box ticked it is stored and the next time the folder dialog opens at once.
	h.tapItem(t, itemFolder)
	bs = h.question(t, "Mount a folder")
	test.Tap(hintCheck(t, h.d.win))
	test.Tap(bs[1])
	if picked != 2 || !h.d.settings.HideFolderMountHint() {
		t.Fatalf("picked %d hidden %v", picked, h.d.settings.HideFolderMountHint())
	}
	h.tapItem(t, itemFolder)
	if picked != 3 || len(h.d.win.Canvas().Overlays().List()) != 0 {
		t.Fatalf("hint shown again: picked %d", picked)
	}
	// Reset Warnings shows it again.
	h.d.settings.ResetHiddenMessages()
	h.tapItem(t, itemFolder)
	h.question(t, "Mount a folder")
	if picked != 3 {
		t.Fatalf("picked %d", picked)
	}
}

// hintCheck is the checkbox of the dialog on top of w.
func hintCheck(t *testing.T, w fyne.Window) *dialogCheck {
	t.Helper()
	var c *dialogCheck
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if x, ok := o.(*dialogCheck); ok {
			c = x
		}
	})
	if c == nil {
		t.Fatal("no checkbox")
	}
	return c
}
