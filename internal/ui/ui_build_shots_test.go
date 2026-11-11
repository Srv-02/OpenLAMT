package ui

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider/idertest"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/sol/soltest"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// uiShotSize is the window size of the mockups (Main.dc.html and the others).
var uiShotSize = fyne.NewSize(1280, 800)

// uiShotPage is one device management page to render: the file name, the window size (zero:
// uiShotSize) and what makes the page show (setup may be nil).
type uiShotPage struct {
	file  string
	page  int // index in manager.pages
	size  fyne.Size
	setup func(p *managePane)
}

// uiShotSidebar are the sidebar pages, uiShotWindow the pages of the window of their own.
var (
	uiShotSidebar = []uiShotPage{
		{"01-device-sidebar-hardware.png", 0, fyne.Size{}, nil},
		{"02-device-sidebar-events.png", 1, fyne.Size{}, nil},
		{"03-device-sidebar-audit.png", 2, fyne.Size{}, nil},
		{"04-device-sidebar-settings.png", 3, fyne.Size{}, nil},
		{"05-device-sidebar-users.png", 4, fyne.Size{}, nil},
	}
	uiShotWindow = []uiShotPage{
		{"10-window-hardware.png", 0, fyne.Size{}, nil},
		{"11-window-events.png", 1, fyne.Size{}, func(p *managePane) { p.impl.(*logPane).filter.SetText("boot") }},
		{"12-window-audit.png", 2, fyne.Size{}, nil},
		{"13-window-settings.png", 3, fyne.NewSize(1280, 880), nil}, // high enough for the Danger zone
		{"14-window-users.png", 4, fyne.Size{}, nil},
	}
)

// TestUIBuildShots renders the device window with device management (also Settings at 1280 x 880,
// so the Danger zone shows), the dialogs of Settings and Users, the Media menu, the questions of
// Media and Console, the Console view and the global settings, light, English, at the mockups'
// 1280 x 800, into
// $OPENLAMT_UI_SHOTS for review against the mockups (issues #1 and #3). Skipped without the
// variable. The device is a session.Fake with kvmtest, idertest and soltest servers and made-up
// data only. Run:
//
//	OPENLAMT_UI_SHOTS=$PWD/workbench/ui-shots go test ./internal/ui -run TestUIBuildShots -count=1
func TestUIBuildShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_UI_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_UI_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newUIShots(t, dir)

	// 01–03: the sidebar beside a running KVM session.
	test.Tap(h.d.manageBtn)
	m := h.d.manager
	h.settle()
	for _, p := range uiShotSidebar {
		m.side.tabs.Tap(p.page)
		h.settle()
		if p.setup != nil {
			p.setup(m.side.view.panes[p.page])
		}
		h.readAgo(m)
		h.shot(p.file, h.d.win)
	}
	m.side.tabs.Tap(0)

	// 10–12: the window of its own (the pop-out closes the sidebar).
	test.Tap(m.side.popOut)
	w := m.win
	scaleWindow(w.win)
	for _, p := range uiShotWindow {
		size := p.size
		if size.IsZero() {
			size = uiShotSize
		}
		w.win.Resize(size)
		w.showPage(p.page)
		h.settle()
		if p.setup != nil {
			p.setup(w.view.panes[p.page])
		}
		h.readAgo(m)
		h.shot(p.file, w.win)
	}
	w.win.Resize(uiShotSize)
	h.manageDialogs(w)
	w.win.Close()

	// 20, 44, 21, 30: the Media menu with nothing mounted; the hint before the folder dialog; a
	// folder ISO being built; IDE-R is off.
	md := h.d.media
	h.dev.Cfg.Settings.Features.IDER = false
	test.Tap(h.d.mediaBtn)
	h.shot("20-media-menu-idle.png", h.d.win)
	folder := sourceFolder(t)
	md.pickFolder = func(chosen func(string)) { chosen(folder) }
	progressed, release := make(chan struct{}), make(chan struct{})
	first := true
	md.onBuildProgress = func(done, total int64) { // build goroutine
		if first {
			first = false
			progressed <- struct{}{}
			<-release
		}
	}
	h.tapMediaItem(md.items.folder)
	h.shot("44-dialog-mount-folder.png", h.d.win)
	test.Tap(hintCheck(t, h.d.win)) // "Don’t show again": 45 has a hidden message then
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[1])
	<-progressed // the build waits in the hook: the window is ours
	md.done, md.total = 640_000_000, 1_030_000_000
	md.refreshProgress()
	h.shot("21-media-menu-preparing.png", h.d.win)
	md.dismiss() // the question shows without the menu behind it
	close(release)
	h.mediaEvent("settings")
	h.shot("30-dialog-ider-on.png", h.d.win)
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[0]) // Cancel

	// 22, 23: an ISO is mounted and boots on the next start; the Power menu.
	h.dev.Cfg.Settings.Features.IDER = true
	iso := sparseISO(t, "alpine-standard.iso", 214_000_000)
	md.pickISO = func(chosen func(string)) { chosen(iso) }
	go func() { h.iderPeers <- h.ider.AcceptSession(t) }()
	test.Tap(h.d.mediaBtn)
	h.tapMediaItem(md.items.iso)
	h.mediaEvent("opened")
	<-h.iderPeers
	h.tapMediaItem(md.items.boot)
	h.mediaEvent("boot")
	test.Tap(h.d.mediaBtn)
	rate := 18_200_000.0
	md.line.SetDetail(formatRate(rate))
	h.d.setMediaStatus("ISO mounted · " + formatRate(rate))
	h.shot("22-media-menu-mounted.png", h.d.win)
	md.dismiss()
	h.d.showPowerMenu()
	h.shot("23-power-menu-boot-iso.png", h.d.win)
	h.d.powerPopup.Dismiss()

	// 32, 33: closing with the ISO mounted; Boot from ISO.
	h.d.requestClose(func() { t.Error("the window closed without the question") })
	h.shot("32-dialog-iso-still-mounted.png", h.d.win)
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[0]) // Keep Open
	h.d.run(amt.BootToISO)
	h.shot("33-dialog-restart-iso.png", h.d.win)
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[0]) // Cancel
	test.Tap(h.d.mediaBtn)
	h.tapMediaItem(md.items.unmount)
	h.mediaEvent("unmounted")

	// 31, 40: SOL is off; after Enable the Console view with some output of the server.
	h.dev.Cfg.Settings.Features.SOL = false
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged) // the question
	h.shot("31-dialog-sol-on.png", h.d.win)
	go func() { h.solPeers <- h.sol.AcceptSession(t) }()
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[1]) // Enable
	recv(t, h.conChanged)
	if h.d.con.client == nil {
		t.Fatalf("console not connected: %d errors", len(h.errs))
	}
	p := <-h.solPeers
	h.solPeer = p
	p.Data(t, []byte(strings.ReplaceAll(uiShotConsoleText, "\n", "\r\n")))
	waitFor(t, func() bool { return strings.Contains(h.d.con.term.Text(), "load average") })
	h.shot("40-console.png", h.d.win)

	// 43: the Console view in full screen, with the KVM|Console switch in the bar.
	test.Tap(h.d.fullBtn)
	h.d.win.Resize(uiShotSize)
	h.shot("43-console-fullscreen.png", h.d.win)
	test.Tap(h.d.exitFullBtn)
	h.d.win.Resize(uiShotSize)

	// 45: the global settings with a hidden message (44 ticked "Don’t show again").
	h.d.win.Canvas().Unfocus()
	showSettingsDialog(h.d.win, &h.d.settings)
	h.shot("45-global-settings-hidden-messages.png", h.d.win)
	h.d.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})

	if len(h.errs) > 0 {
		t.Fatalf("errors: %v", <-h.errs)
	}
}

// uiShotConsoleText is the made-up output of the SOL server in 40-console.png (Console.dc.html).
const uiShotConsoleText = `[    0.000000] Linux version 6.8.12 (build@example) #1 SMP
[    0.000000] Command line: BOOT_IMAGE=/boot/vmlinuz root=/dev/sda2 console=ttyS0,115200
[    1.204318] serial8250: ttyS0 at I/O 0x3f8 (irq = 4) is a 16550A
[    3.881922] EXT4-fs (sda2): mounted filesystem with ordered data mode
[    5.112004] systemd[1]: Reached target Multi-User System.

Example Linux 6.8 server ttyS0

server login: root
Password:
Last login: Tue Nov 10 22:14:03 on ttyS0
root@server:~# uptime
 18:52:11 up 2 days,  3:40,  1 user,  load average: 0.21, 0.18, 0.12
root@server:~# `

// uiShots is the device window "Lab Server" of TestUIBuildShots with its fake servers.
type uiShots struct {
	t   *testing.T
	dir string
	a   fyne.App
	d   *DeviceWindow
	dev *session.Fake

	loaded     chan func() // read results of device management, applied by settle
	readAt     time.Time   // the clock of device management
	errs       chan error
	kvmChanged chan struct{}
	frames     chan struct{}
	media      chan string
	conChanged chan struct{}

	ider      *idertest.Server
	iderPeers chan *idertest.Peer
	sol       *soltest.Server
	solPeers  chan *soltest.Peer
	solPeer   *soltest.Peer
}

func newUIShots(t *testing.T, dir string) *uiShots {
	t.Helper()
	englishUI(t)
	restoreDarkVariant(t)
	cache := t.TempDir() // mkiso.BuildTemp writes below os.UserCacheDir
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	h := &uiShots{t: t, dir: dir, a: test.NewTempApp(t), loaded: make(chan func(), 64), errs: make(chan error, 16),
		kvmChanged: make(chan struct{}, 8), frames: make(chan struct{}, 16), media: make(chan string, 256),
		conChanged: make(chan struct{}, 16), iderPeers: make(chan *idertest.Peer, 2), solPeers: make(chan *soltest.Peer, 2),
		readAt: time.Date(2026, 11, 11, 18, 52, 0, 0, time.Local)}
	h.a.Settings().SetTheme(style.Theme{})

	kvmSrv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{blackUpdate(1024, 768)}})
	h.ider = idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	h.sol = soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	h.dev = &session.Fake{St: on, FP: certFP, HW: uiShotHardware(), Events: uiShotEvents(), Audit: uiShotAudit(),
		KVMAddr: kvmSrv.Addr, KVMUser: "admin", KVMPassword: "pw",
		IDERAddr: h.ider.Addr, IDERUser: "admin", IDERPassword: "pw"}
	h.dev.SOL.Addr, h.dev.SOL.User, h.dev.SOL.Password = h.sol.Addr, "admin", "pw"
	h.dev.Cfg.Settings, h.dev.Cfg.Certificates, h.dev.Cfg.Users = exampleSettings(), exampleCerts(), uiShotUsers()

	cfg := amt.Config{Host: "192.0.2.10", Username: "admin", UseTLS: true, Fingerprint: certFP}
	sess, st, err := session.Open(context.Background(), cfg, nil, session.ConnectFake(h.dev))
	if err != nil {
		t.Fatal(err)
	}
	d := NewDeviceWindow(h.a, sess, "Lab Server", st, NewSettings(h.a.Preferences()), nil)
	h.d = d
	d.onError = func(err error) { h.errs <- err }
	d.onKVMChanged = func() { h.kvmChanged <- struct{}{} }
	d.view.onFrame = func() { h.frames <- struct{}{} }
	d.wake = nil
	d.media.onEvent = func(e string) { h.media <- e }
	d.media.ticker = func(func()) func() { return func() {} } // the shot sets its own statistics
	d.media.now = func() time.Time { return h.readAt }
	d.con.onChanged = func() { h.conChanged <- struct{}{} }
	d.onManager = func(m *manager) {
		m.tickEvery = 0
		m.later = func(f func()) { h.loaded <- f }
		m.now = func() time.Time { return h.readAt }
	}
	// Runs before the servers stop: close the window and wait until the KVM and console
	// sessions it ends have reported.
	t.Cleanup(func() {
		kvm, console := d.kvmClient != nil, d.con.client != nil
		d.win.Close()
		if h.solPeer != nil {
			h.solPeer.Disconnect() // Close need not wait for END_SOL_REPLY
		}
		if console {
			recv(t, h.conChanged)
		}
		if kvm {
			recv(t, h.kvmChanged)
		}
		d.poller.Stop()
	})

	d.win.SetPadded(false)
	scaleWindow(d.win)
	d.win.Resize(uiShotSize)
	switchVariant(h.a, theme.VariantLight)
	tapKVM(d)
	recv(t, h.kvmChanged)
	recv(t, h.frames)
	if d.kvmClient == nil {
		t.Fatalf("KVM not connected: %d errors", len(h.errs))
	}
	return h
}

// manageDialogs renders the dialogs of Settings and Users on the window of its own w (34–39,
// 41, 42) in the states of Danger.dc.html and Forms.dc.html.
func (h *uiShots) manageDialogs(w *manageWindow) {
	host := h.d.sess.Config().Host
	w.showPage(3)
	h.settle()
	sp := w.view.panes[3].impl.(*settingsPane)

	// 34: orange, the box ticked
	sp.host.SetText("lab01")
	sp.host.OnSubmitted(sp.host.Text)
	test.Tap(sp.dialog.check)
	w.win.Canvas().Unfocus() // the resting state: no focus ring around the ticked box
	h.shot("34-dialog-host-name.png", w.win)
	sp.dialog.Dismiss()

	// 35: red, the address typed correctly
	test.Tap(sp.turnOff)
	sp.dialog.entry.SetText(host)
	h.shot("35-dialog-turn-off-amt.png", w.win)
	sp.dialog.Dismiss()

	// 39: Static chosen, the address typed
	sp.address.Tap(1)
	sp.dialog.entry.SetText(host)
	h.shot("39-dialog-network.png", w.win)
	sp.dialog.Dismiss()

	// 41: the certificates are read first; nothing chosen yet
	test.Tap(sp.change)
	h.settle()
	h.shot("41-dialog-tls.png", w.win)
	sp.dialog.Dismiss()

	w.showPage(4)
	h.settle()
	up := w.view.panes[4].impl.(*usersPane)

	// 36: the address one digit short while typing: the button stays disabled, the field red
	test.Tap(up.table.lines[0].edit)
	ad := up.adminDlg
	ad.pw.SetText(newPassword)
	ad.repeat.SetText(newPassword)
	ad.entry.SetText(host[:len(host)-1])
	h.shot("36-dialog-admin-account.png", w.win)
	ad.Dismiss()

	// 37: Add user as in Forms.dc.html
	test.Tap(up.add)
	a := up.userDlg
	a.name.SetText("helpdesk")
	a.pw.SetText(newPassword)
	a.repeat.SetText(newPassword)
	for _, i := range []int{0, 1, 2} { // Power control; KVM, media, console; Hardware
		test.Tap(a.checks[i])
	}
	w.win.Canvas().Unfocus()
	h.shot("37-dialog-add-user.png", w.win)
	a.Dismiss()

	// 38: Edit user "helpdesk"
	test.Tap(up.table.lines[1].edit)
	h.shot("38-dialog-edit-user.png", w.win)
	up.userDlg.Dismiss()

	// 42
	ns := showNotSavedDialog(w.win, h.d.name, newPassword)
	h.shot("42-dialog-password-not-saved.png", w.win)
	ns.Dismiss()
}

// settle applies the read results of device management until none arrives for a moment.
func (h *uiShots) settle() {
	for {
		select {
		case f := <-h.loaded:
			f()
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

// readAgo lets 12 s pass since the reads ("Read 12 s ago") and updates the shown pages.
func (h *uiShots) readAgo(m *manager) {
	at := h.readAt
	m.now = func() time.Time { return at.Add(12 * time.Second) }
	m.tick()
	m.now = func() time.Time { return h.readAt }
}

// tapMediaItem taps the row of it in the open Media menu.
func (h *uiShots) tapMediaItem(it *fyne.MenuItem) {
	h.t.Helper()
	md := h.d.media
	for i, x := range md.shown.Items {
		if x == it {
			test.Tap(md.menu.Item(i))
			return
		}
	}
	h.t.Fatalf("item %v not in the Media menu", it)
}

// mediaEvent waits for the media event want (media.go onEvent).
func (h *uiShots) mediaEvent(want string) {
	h.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-h.media:
			if e == want {
				return
			}
		case <-timeout:
			h.t.Fatalf("no media event %q (%d errors)", want, len(h.errs))
		}
	}
}

func (h *uiShots) shot(name string, w fyne.Window) {
	h.t.Helper()
	f, err := os.Create(filepath.Join(h.dir, name))
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		h.t.Fatal(err)
	}
}

// blackUpdate is a black w x h frame in 64 px tiles (RGB565).
func blackUpdate(w, h int) []byte {
	var rects []kvmtest.Rect
	for y := 0; y < h; y += 64 {
		for x := 0; x < w; x += 64 {
			tw, th := min(64, w-x), min(64, h-y)
			rects = append(rects, kvmtest.RawRect(x, y, tw, th, bytes.Repeat([]byte{0x00, 0x00}, tw*th)))
		}
	}
	return kvmtest.FramebufferUpdate(rects...)
}

// sparseISO is an empty file of size bytes that takes no disk space.
func sparseISO(t *testing.T, name string, size int64) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	return p
}

// uiShotHardware is the made-up inventory of Main.dc.html.
func uiShotHardware() amt.HardwareInfo {
	return amt.HardwareInfo{
		System: amt.SystemInfo{Manufacturer: "Example Systems", Model: "EX-7000", Version: "Workstation 7 Tiny", SerialNumber: "EX-12345678"},
		BIOS:   amt.BIOSInfo{Version: "2.14.0", ReleaseDate: time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)},
		CPUs:   []amt.CPUInfo{{Socket: "CPU 0", Name: "Example Core 7", MaxClockMHz: 4600}},
		Memory: []amt.MemoryModule{
			{Bank: "Bank 0", CapacityBytes: 16 << 30, SpeedMTs: 5600, Type: "DDR5"},
			{Bank: "Bank 1", CapacityBytes: 16 << 30, SpeedMTs: 5600, Type: "DDR5"},
		},
		Drives: []amt.Drive{{ID: "MEDIA DEV 0", CapacityBytes: 1000204886016}, {ID: "MEDIA DEV 1", CapacityBytes: 2000398934016}},
		AMT:    amt.AMTInfo{Version: "16.1.25", Build: "2052"},
	}
}

// uiShotEvents are the nine entries of WindowB.dc.html and 200 older ones (209 entries).
func uiShotEvents() []amt.EventLogEntry {
	at := func(d, hh, mm, ss int) time.Time { return time.Date(2026, 11, d, hh, mm, ss, 0, time.Local) }
	e := []amt.EventLogEntry{
		{Time: at(11, 16, 41, 52), Severity: amt.SeverityInfo, Source: "BIOS", Description: "Starting operating system boot process"},
		{Time: at(11, 16, 41, 39), Severity: amt.SeverityInfo, Source: "BIOS", Description: "System reset initiated"},
		{Time: at(10, 3, 12, 8), Severity: amt.SeverityWarning, Source: "Watchdog", Description: "Agent heartbeat timeout"},
		{Time: at(9, 22, 48, 2), Severity: amt.SeverityInfo, Source: "Power", Description: "Power supply AC restored"},
		{Time: at(9, 22, 47, 15), Severity: amt.SeverityCritical, Source: "Power", Description: "Power supply AC lost"},
		{Time: at(8, 9, 30, 44), Severity: amt.SeverityInfo, Source: "BIOS", Description: "Performing memory initialization"},
		{Time: at(8, 9, 30, 41), Severity: amt.SeverityInfo, Source: "BIOS", Description: "Starting CPU initialization"},
		{Time: at(5, 14, 2, 19), Severity: amt.SeverityWarning, Source: "Chassis", Description: "Chassis intrusion detected"},
		{Time: at(5, 14, 1, 57), Severity: amt.SeverityInfo, Source: "BIOS", Description: "Starting operating system boot process"},
	}
	older := []string{"Performing memory initialization", "Starting CPU initialization", "System reset initiated"}
	for i := 0; i < 200; i++ {
		e = append(e, amt.EventLogEntry{Time: at(4, 12, 0, 0).Add(-time.Duration(i) * 37 * time.Minute), Severity: amt.SeverityInfo,
			Source: "BIOS", Description: older[i%len(older)]})
	}
	return e
}

// uiShotUsers are the accounts of Users.dc.html: the signed-in admin, helpdesk, the disabled
// monitor and the system account.
func uiShotUsers() amt.UserList {
	return amt.UserList{Admin: "admin", Users: []amt.User{
		{Handle: 1, Name: "helpdesk", Access: amt.AccessNetwork, Enabled: true, EnabledKnown: true,
			Realms: []amt.Realm{amt.RealmRemoteControl, amt.RealmRedirection}},
		{Handle: 2, Name: "monitor", Access: amt.AccessNetwork, EnabledKnown: true,
			Realms: []amt.Realm{amt.RealmEventLogReader, amt.RealmHardwareAsset}},
		{Handle: 3, Name: "$$OsAdmin", Access: amt.AccessLocal, System: true, Realms: []amt.Realm{amt.RealmLocalUN}},
	}}
}

// uiShotAudit is a made-up audit log.
func uiShotAudit() []amt.AuditLogEntry {
	at := func(d, hh, mm, ss int) time.Time { return time.Date(2026, 11, d, hh, mm, ss, 0, time.Local) }
	return []amt.AuditLogEntry{
		{Time: at(11, 18, 50, 3), Initiator: "admin", Address: "192.0.2.20", Event: "KVM Session Started"},
		{Time: at(11, 16, 41, 30), Initiator: "admin", Address: "192.0.2.20", Event: "Remote Power Control", Details: "Reset"},
		{Time: at(11, 16, 38, 12), Initiator: "admin", Address: "192.0.2.20", Event: "IDE-R Session Opened"},
		{Time: at(10, 9, 15, 44), Initiator: "admin", Address: "192.0.2.21", Event: "SOL Session Started"},
		{Time: at(10, 9, 2, 7), Initiator: "admin", Address: "192.0.2.21", Event: "SOL Session Ended"},
		{Time: at(9, 22, 50, 1), InitiatorType: 2, Event: "Time Set"},
		{Time: at(8, 11, 0, 0), Initiator: "operator", Address: "192.0.2.30", Event: "Account Modified", Details: "operator"},
		{Time: at(5, 14, 3, 10), InitiatorType: 2, Event: "Provisioning Completed"},
	}
}
