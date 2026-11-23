package ui

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/bootserve"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Boot Options (issue #13) and One Click Recovery (issue #21), gesamtdesign "Boot Options und
// One Click Recovery" (V6, V7): Power menu › "Boot Options…" opens a dialog with the boot
// target (hard disk, PXE, HTTPS boot, BIOS setup, the mounted ISO), the options of
// MeshCommander's boot dialog and the firmware messages. What the device does not report in
// AMT_BootCapabilities is greyed with "(not supported)". The dialog is the confirmation
// (simple); HTTPS boot from a file of this computer adds the orange warning and checkbox,
// because OpenLAMT puts a temporary root certificate on the device for it.
//
// Every boot goes through the user consent first (Session.Boot, Session.BootFromFile, the
// pattern of Reboot to BIOS from issue #90) as a poller job; a cancelled consent writes nothing.
// HTTPS boot from a file runs a local HTTPS server (internal/bootserve) on the chosen address
// until the device has loaded the image or 10 minutes passed, shows the progress in the status
// bar and removes the temporary root afterwards.

// bootState is the Boot Options part of the device window; only touched on the Fyne thread.
type bootState struct {
	item    *fyne.MenuItem // "Boot Options…" in the Power menu
	opening bool           // the device's boot capabilities are being read for the dialog
	dlg     *bootDialog    // the shown dialog, or nil
	ocr     *ocrRun        // a running One Click Recovery from a file, or nil
	line    *ellipsisText  // "Serving recovery.efi · 48 of 118 MB" in the status bar; hidden while empty
}

// Test hooks: the local addresses, the address that reaches the device and the server.
var (
	bootAddresses   = bootserve.Addresses
	bootRoute       = bootserve.RouteTo
	startBootServer = bootserve.Start
)

// bootSetup is what the dialog needs, read off the Fyne thread before it opens.
type bootSetup struct {
	info  amt.BootInfo
	addrs []bootserve.Address
	route netip.Addr // the local address towards the device; may be invalid
}

// bootMenuItem creates "Boot Options…" of the Power menu.
func (d *DeviceWindow) bootMenuItem() *fyne.MenuItem {
	d.boot.item = fyne.NewMenuItem(lang.L("Boot Options…"), d.openBootOptions)
	return d.boot.item
}

// bootOptionsAllowed reports whether "Boot Options…" can be chosen: not while a power action or
// a One Click Recovery runs, not while the device sleeps (S3, issue #142), and only once at a
// time.
func (d *DeviceWindow) bootOptionsAllowed() bool {
	return !d.aborted && !d.powerBusy && !d.readS3() && d.boot.ocr == nil && !d.boot.opening && d.boot.dlg == nil
}

// updateBootItem follows bootOptionsAllowed; updatePowerItems calls it.
func (d *DeviceWindow) updateBootItem() {
	it := d.boot.item
	if it == nil {
		return
	}
	it.Disabled = !d.bootOptionsAllowed()
	v := icons.Foreground
	if it.Disabled {
		v = icons.Disabled
	}
	it.Icon = icons.Get(icons.Boot, v)
}

// openBootOptions reads the boot capabilities and the local addresses, then shows the dialog.
func (d *DeviceWindow) openBootOptions() {
	if !d.bootOptionsAllowed() {
		return
	}
	d.boot.opening = true
	d.updatePowerItems()
	host := d.sess.Config().Host
	var s bootSetup
	// A poller job, so the read does not run beside a status read and its result arrives in order.
	d.poller.Do(func() error {
		info, err := d.sess.BootInfo()
		s.info = info
		if err == nil {
			s.addrs = bootAddresses()
			s.route, _ = bootRoute(host)
		}
		return err
	}, func(err error) {
		fyne.Do(func() {
			d.boot.opening = false
			defer d.updatePowerItems()
			switch {
			case d.closed || d.aborted || errors.Is(err, session.ErrStopped):
			case err != nil && isMismatch(err):
				d.abort(err)
			case err != nil:
				d.onError(err)
			default:
				d.showBootDialog(s)
			}
		})
	})
}

// bootChoice is one boot target of the dialog: a radio of its own, so that a single target can
// be greyed out.
type bootChoice struct {
	target  amt.BootTarget
	radio   *widget.RadioGroup
	enabled bool
}

// bootCheck is one option of the dialog.
type bootCheck struct {
	check   *dialogCheck
	cap     bool                      // the device supports it
	applies func(amt.BootTarget) bool // nil: every target
}

// bootDialog is the shown Boot Options dialog; its controls are exposed for tests.
type bootDialog struct {
	*panel
	d       *DeviceWindow
	setup   bootSetup
	powerOn bool // the device is off: the button is "Power On"

	choices   []*bootChoice
	target    amt.BootTarget
	chosen    bool
	ocrOff    fyne.CanvasObject // the hint below HTTPS boot while One Click Recovery is off
	ocrBox    fyne.CanvasObject // source, file or URL
	source    *widgets.Segmented
	fileBox   fyne.CanvasObject
	urlBox    fyne.CanvasObject
	file      string
	fileSize  int64
	fileText  *canvas.Text
	chooseBtn *widgets.PrimaryButton
	addr      *widgets.Dropdown
	addrs     []netip.Addr
	port      *dialogEntry
	url       *dialogEntry

	sol, safe, pause, secure, lockPower, lockReset, lockSleep, lockKeyboard, bypass, progress *bootCheck
	checks                                                                                    []*bootCheck
	verbosity                                                                                 *widgets.Dropdown
	verbs                                                                                     []amt.Verbosity

	warn fyne.CanvasObject // orange warning and checkbox (file of this computer)
	risk *dialogCheck
	hint fyne.CanvasObject // "Applies to the next boot only."
}

const (
	sourceFile = 0
	sourceURL  = 1
)

// notSupported marks an option the device does not report.
func notSupported(label string, ok bool) string {
	if ok {
		return label
	}
	return lang.L("{{.Option}} (not supported)", map[string]any{"Option": label})
}

// showBootDialog shows the Boot Options dialog for setup.
func (d *DeviceWindow) showBootDialog(s bootSetup) *bootDialog {
	d.releaseKeys()
	b := &bootDialog{d: d, setup: s, powerOn: d.powerKnown && isOff(d.powerState)}
	action := lang.L("Restart")
	if b.powerOn {
		action = lang.L("Power On")
	}
	b.panel = showPanel(d.win, panelSpec{
		title: lang.L("Boot options for {{.Name}}", map[string]any{"Name": d.name}), action: action, cancel: lang.L("Cancel"),
		flush: true, width: 560, keep: true, danger: !b.powerOn, redButton: !b.powerOn,
		onAction: b.submit, onCancel: func() { d.boot.dlg = nil; d.updatePowerItems() },
	}, func(p *panel) []fyne.CanvasObject {
		b.panel = p
		return b.build()
	})
	d.boot.dlg = b
	d.updatePowerItems()
	b.update()
	if d.onConfirmShown != nil {
		d.onConfirmShown()
	}
	return b
}

func (b *bootDialog) build() []fyne.CanvasObject {
	c, info, d := b.setup.info.Caps, b.setup.info, b.d
	add := func(t amt.BootTarget, label string, ok bool) *bootChoice {
		ch := &bootChoice{target: t, enabled: ok}
		ch.radio = widget.NewRadioGroup([]string{label}, func(v string) {
			if v != "" {
				b.choose(t)
			}
		})
		ch.radio.Required = true
		if !ok {
			ch.radio.Disable()
		}
		b.choices = append(b.choices, ch)
		return ch
	}
	add(amt.BootHardDisk, lang.L("Hard disk (default order)"), true)
	add(amt.BootPXE, notSupported(lang.L("Network (PXE)"), c.PXE), c.PXE)
	httpsSupported := c.HTTPSBoot && info.OCR.Known
	add(amt.BootHTTPS, notSupported(lang.L("HTTPS boot (One Click Recovery)"), httpsSupported), info.HTTPSBootAvailable())
	add(amt.BootBIOSSetup, notSupported(lang.L("BIOS setup"), c.BIOSSetup), c.BIOSSetup)
	iso := lang.L("Mounted ISO")
	switch {
	case !c.IDER:
		iso = notSupported(iso, false)
	case !d.media.mounted():
		iso = lang.L("Mounted ISO · mount an ISO in Media first")
	}
	add(amt.BootISO, iso, c.IDER && d.media.mounted())

	b.ocrOff = container.New(layout.NewCustomPaddedLayout(-4, 4, 23, 0),
		formHint(lang.L("One Click Recovery is off. Turn it on in Device management › Settings › Remote features.")))
	setShown(b.ocrOff, httpsSupported && !info.OCR.Enabled)

	targets := []fyne.CanvasObject{}
	for _, ch := range b.choices {
		targets = append(targets, ch.radio)
		if ch.target == amt.BootHTTPS {
			targets = append(targets, b.ocrOff, b.buildOCR())
		}
	}

	notHTTPS := func(t amt.BootTarget) bool { return t != amt.BootHTTPS }
	opt := func(label string, ok bool, applies func(amt.BootTarget) bool) *bootCheck {
		bc := &bootCheck{check: b.newCheck(notSupported(label, ok), false), cap: ok, applies: applies}
		bc.check.OnChanged = func(bool) { b.validate() }
		b.checks = append(b.checks, bc)
		return bc
	}
	b.sol = opt(lang.L("Show boot in the console (SOL)"), c.SOL, notHTTPS)
	b.safe = opt(lang.L("Safe mode"), c.SafeMode, func(t amt.BootTarget) bool { return t == amt.BootHardDisk })
	b.pause = opt(lang.L("Pause in BIOS before boot"), c.BIOSPause, func(t amt.BootTarget) bool { return t != amt.BootPXE && t != amt.BootHTTPS })
	b.secure = opt(lang.L("Enforce Secure Boot"), c.SecureBootControl, nil)
	b.secure.check.Checked = info.EnforceSecureBoot // the device's value; kept where AMT may not change it
	b.lockPower = opt(lang.L("Lock power button"), c.LockPower, nil)
	b.lockReset = opt(lang.L("Lock reset button"), c.LockReset, nil)
	b.lockSleep = opt(lang.L("Lock sleep button"), c.LockSleep, nil)
	b.lockKeyboard = opt(lang.L("Lock keyboard"), c.LockKeyboard, nil)
	b.bypass = opt(lang.L("Bypass BIOS password"), c.PasswordBypass, notHTTPS)
	b.progress = opt(lang.L("Log boot progress"), c.ProgressEvents, nil)
	grid := container.NewGridWithColumns(2)
	for _, bc := range b.checks {
		grid.Add(bc.check)
	}

	labels := []string{lang.L("System default")}
	b.verbs = []amt.Verbosity{amt.VerbosityDefault}
	for _, v := range []struct {
		ok    bool
		v     amt.Verbosity
		label string
	}{{c.VerbosityQuiet, amt.VerbosityQuiet, lang.L("Quiet")}, {c.VerboseAll, amt.VerbosityVerbose, lang.L("Verbose")},
		{c.ScreenBlank, amt.VerbosityBlank, lang.L("Blank screen")}} {
		if v.ok {
			labels = append(labels, v.label)
			b.verbs = append(b.verbs, v.v)
		}
	}
	b.verbosity = widgets.NewDropdown(labels)
	b.verbosity.OnEscape = b.Dismiss
	b.verbosity.SetSelectedIndex(0)
	firmware := container.NewBorder(nil, nil, designText(lang.L("Firmware messages"), 12.5, style.ColorNameControlText, true), b.verbosity)

	box, _ := warningBox(false, "", lang.L("OpenLAMT adds a temporary root certificate to {{.Name}}, so the device trusts this computer’s HTTPS server. OpenLAMT removes it after the download or after 10 minutes.",
		map[string]any{"Name": d.name}))
	host := d.sess.Config().Host
	b.risk = b.newWrapCheck(riskCheckText(deviceLabel(d.name, host), host), false)
	b.risk.OnChanged = func(bool) { b.validate() }
	b.warn = container.New(layout.NewCustomPaddedVBoxLayout(panelFormGap), box, b.risk)
	b.hint = formHint(lang.L("Applies to the next boot only."))

	return []fyne.CanvasObject{
		formField(lang.L("Boot from"), container.New(layout.NewCustomPaddedVBoxLayout(0), targets...)),
		formField(lang.L("Options"), grid),
		firmware,
		b.warn,
		b.hint,
	}
}

// buildOCR builds the HTTPS boot box: the source switch, the file with its address and port, or
// the URL.
func (b *bootDialog) buildOCR() fyne.CanvasObject {
	d := b.d
	b.source = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("File on this computer"), lang.L("URL")), func(int) { b.update() })
	b.source.SetSelected(sourceFile)

	b.fileText = designText("", 12, theme.ColorNameForeground, false)
	b.fileText.TextStyle.Monospace = true
	field := style.BindRect(&canvas.Rectangle{CornerRadius: confirmFieldRadius, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder)
	fileField := container.NewStack(field, container.New(layout.NewCustomPaddedLayout(7, 7, 10, 10), b.fileText))
	b.chooseBtn = settingsButton(lang.L("Choose File…"), func() {
		pickBootImage(d.win, lang.L("Choose File…"), b.setFile)
	})
	labels := []string{}
	sel := -1
	for _, a := range b.setup.addrs {
		if !a.IP.IsValid() || containsAddr(b.addrs, a.IP) {
			continue
		}
		if a.IP == b.setup.route {
			sel = len(b.addrs)
		}
		b.addrs = append(b.addrs, a.IP)
		labels = append(labels, fmt.Sprintf("%s (%s)", a.IP, a.Interface))
	}
	if r := b.setup.route; r.IsValid() && sel < 0 {
		sel = len(b.addrs)
		b.addrs = append(b.addrs, r)
		labels = append(labels, r.String())
	}
	b.addr = widgets.NewDropdown(labels)
	b.addr.OnEscape = b.Dismiss
	if sel < 0 && len(labels) > 0 {
		sel = 0
	}
	if sel >= 0 {
		b.addr.SetSelectedIndex(sel)
	}
	b.addr.OnChanged = func(string) { b.validate() }
	b.port = b.newEntry(false)
	b.port.SetText(strconv.Itoa(bootserve.DefaultPort))
	b.port.OnChanged = func(string) { b.validate() }
	portBox := container.New(&fixedWidthLayout{width: 90}, framedEntry(b.port))
	b.fileBox = container.New(layout.NewCustomPaddedVBoxLayout(8),
		formField(lang.L("Image"), container.NewBorder(nil, nil, nil, b.chooseBtn, fileField)),
		formField(lang.L("Address the device downloads from"), container.NewBorder(nil, nil, nil, portBox, b.addr)),
		formHint(lang.L("OpenLAMT serves the file over HTTPS until the device has loaded it, at most 10 minutes. Allow incoming connections on this port.")))

	b.url = b.newEntry(false)
	b.url.SetPlaceHolder("https://boot.example.com/recovery.efi")
	b.url.OnChanged = func(string) { b.validate() }
	b.urlBox = container.New(layout.NewCustomPaddedVBoxLayout(8),
		formField(lang.L("Image URL"), framedEntry(b.url)),
		formHint(lang.L("The server’s certificate must chain to a trusted root on {{.Name}} (Device management › Certificates).", map[string]any{"Name": d.name})))

	inner := container.New(layout.NewCustomPaddedVBoxLayout(8), container.NewHBox(b.source), b.fileBox, b.urlBox)
	b.ocrBox = widgets.NewPanel(style.ColorNameCard, style.ColorNameCardBorder, 8, inner, 10, 12, 10, 12)
	return b.ocrBox
}

func containsAddr(list []netip.Addr, a netip.Addr) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

// setFile takes the chosen image.
func (b *bootDialog) setFile(path string) {
	st, err := os.Stat(path)
	if err != nil {
		showError(b.d.win, err)
		return
	}
	b.file, b.fileSize = path, st.Size()
	b.fileText.Text = filepath.Base(path) + " · " + recSize(st.Size())
	b.fileText.Refresh()
	b.validate()
}

// choose selects target t and clears the other radios.
func (b *bootDialog) choose(t amt.BootTarget) {
	b.target, b.chosen = t, true
	for _, ch := range b.choices {
		if ch.target != t && ch.radio.Selected != "" {
			ch.radio.SetSelected("")
		}
	}
	b.update()
}

// fileMode reports whether HTTPS boot from a file of this computer is chosen.
func (b *bootDialog) fileMode() bool {
	return b.chosen && b.target == amt.BootHTTPS && b.source.Selected() == sourceFile
}

// update shows the parts of the chosen target and source and locks the options that do not
// apply to it.
func (b *bootDialog) update() {
	https := b.chosen && b.target == amt.BootHTTPS
	setShown(b.ocrBox, https)
	setShown(b.fileBox, b.source.Selected() == sourceFile)
	setShown(b.urlBox, b.source.Selected() == sourceURL)
	setShown(b.warn, b.fileMode())
	setShown(b.hint, !b.fileMode())
	for _, bc := range b.checks {
		on := bc.cap && (bc.applies == nil || !b.chosen || bc.applies(b.target))
		if !on && bc != b.secure {
			bc.check.SetChecked(false)
		}
		setEnabled(bc.check, on)
	}
	b.validate()
	b.fit()
}

// port number of the field, 0 when it is not one.
func (b *bootDialog) portNumber() int {
	n, err := strconv.Atoi(strings.TrimSpace(b.port.Text))
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}

// validHTTPSURL accepts what amt.Device.Boot accepts for HTTPS boot.
func validHTTPSURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && u.Scheme == "https" && u.Host != "" && len(strings.TrimSpace(s)) <= 300
}

// ready reports whether the button may run.
func (b *bootDialog) ready() bool {
	if !b.chosen {
		return false
	}
	if b.target != amt.BootHTTPS {
		return true
	}
	if b.fileMode() {
		return b.file != "" && b.addr.SelectedIndex() >= 0 && b.portNumber() > 0 && b.risk.Checked
	}
	return validHTTPSURL(b.url.Text)
}

func (b *bootDialog) validate() {
	if b.action != nil {
		setEnabled(b.action, b.ready())
	}
}

// options are the boot options of the dialog; the URL of a file is set when its server runs.
func (b *bootDialog) options() amt.BootOptions {
	on := func(bc *bootCheck) bool { return bc.check.Checked && !bc.check.Disabled() }
	o := amt.BootOptions{Target: b.target, SOL: on(b.sol), SafeMode: on(b.safe), BIOSPause: on(b.pause),
		EnforceSecureBoot: b.secure.check.Checked, LockPower: on(b.lockPower), LockReset: on(b.lockReset),
		LockSleep: on(b.lockSleep), LockKeyboard: on(b.lockKeyboard), PasswordBypass: on(b.bypass), ProgressEvents: on(b.progress)}
	if i := b.verbosity.SelectedIndex(); i >= 0 && i < len(b.verbs) {
		o.Verbosity = b.verbs[i]
	}
	if b.target == amt.BootHTTPS && !b.fileMode() {
		o.URL = strings.TrimSpace(b.url.Text)
	}
	return o
}

// submit is the Restart (Power On) button: it closes the dialog and starts the boot.
func (b *bootDialog) submit() {
	if !b.ready() {
		return
	}
	o := b.options()
	var file *bootFile
	if b.fileMode() {
		host := b.d.sess.Config().Host
		c, err := amt.ConfirmChecked(amt.OpBootFile, host, b.risk.Checked)
		if err != nil {
			return
		}
		file = &bootFile{path: b.file, size: b.fileSize, addr: b.addrs[b.addr.SelectedIndex()], port: b.portNumber(), confirm: c}
	}
	b.close()
	b.d.boot.dlg = nil
	b.d.startBoot(o, file)
}

// bootFile is the image of a One Click Recovery from this computer.
type bootFile struct {
	path    string
	size    int64
	addr    netip.Addr
	port    int
	confirm amt.Confirmation // amt.OpBootFile: adds and removes the temporary root
}

// ocrRun is a running One Click Recovery from a file: the local server and the temporary root
// on the device. Its server is set on the poller goroutine; release and the watcher take mu.
type ocrRun struct {
	name     string
	total    int64
	sent     atomic.Int64
	confirm  amt.Confirmation
	cleaned  chan struct{} // closed once the server is gone and the root is removed (or was never added)
	stopTick func()        // the status line's ticker; Fyne thread only

	mu       sync.Mutex
	srv      *bootserve.Server
	released bool // the window closed: stop at once
}

// setServer keeps srv unless the run was released meanwhile (then it closes srv).
func (r *ocrRun) setServer(srv *bootserve.Server) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released {
		srv.Close()
		return false
	}
	r.srv = srv
	return true
}

// release stops the server and waits until the temporary root is removed.
func (r *ocrRun) release() {
	r.mu.Lock()
	r.released = true
	srv := r.srv
	r.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
	<-r.cleaned
}

// releaseBoot returns what shutdown runs off the Fyne thread before the session goes: stop a
// running One Click Recovery and remove its temporary root.
func (d *DeviceWindow) releaseBoot() func() {
	if d.boot.dlg != nil {
		d.boot.dlg.close()
		d.boot.dlg = nil
	}
	r := d.boot.ocr
	if r == nil {
		return func() {}
	}
	return r.release
}

// startBoot runs the boot as a poller job: the consent (KVM's dialog), then the boot options,
// for a file also the local server and the temporary root first. It is refused while another
// power action runs (claimPower).
func (d *DeviceWindow) startBoot(o amt.BootOptions, file *bootFile) {
	if d.claimPower(lang.L("Wait until the running power action ends, then try again.")) != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.biosCancel = cancel // shutdown ends a consent dialog through it, as for Reboot to BIOS
	prompt := &consentPrompt{ctx: ctx, ask: d.askConsent}
	if file == nil {
		d.poller.Do(func() error { return d.sess.Boot(prompt, o) }, func(err error) {
			cancel()
			fyne.Do(func() { d.bootDone(o, err) })
		})
		return
	}
	run := &ocrRun{name: filepath.Base(file.path), total: file.size, confirm: file.confirm, cleaned: make(chan struct{})}
	d.boot.ocr = run
	var rootID string
	d.poller.Do(func() error {
		srv, err := startBootServer(bootserve.Config{Path: file.path, Addr: file.addr, Port: file.port,
			Progress: func(sent, _ int64) { run.sent.Store(sent) }})
		if err != nil {
			return err
		}
		if !run.setServer(srv) {
			return session.ErrStopped
		}
		o.URL = srv.URL()
		id, err := d.sess.BootFromFile(prompt, srv.RootDER(), o, file.confirm)
		if err != nil {
			srv.Close()
			return err
		}
		rootID = id
		return nil
	}, func(err error) {
		cancel()
		if err != nil {
			close(run.cleaned)
		}
		fyne.Do(func() {
			if err != nil && d.boot.ocr == run {
				d.boot.ocr = nil
			}
			if err == nil && d.boot.ocr == run {
				run.stopTick = secondTicker(func() { d.showServing(run) })
				d.showServing(run)
			}
			d.bootDone(o, err)
		})
		if err == nil {
			go d.watchOCR(run, rootID) // after the UI has the run: its end is reported behind it
		}
	})
}

// bootDone reports the end of the device part of a boot, like a power action.
func (d *DeviceWindow) bootDone(o amt.BootOptions, err error) {
	d.biosCancel = nil
	d.powerDone(amt.PowerReset, err)
	if err == nil && o.SOL && !d.closed && !d.aborted {
		d.setConsoleView(true) // "Show boot in the console (SOL)"
	}
}

// showServing shows the progress of the download in the status bar.
func (d *DeviceWindow) showServing(r *ocrRun) {
	if d.boot.ocr != r || d.boot.line == nil {
		return
	}
	d.setStatusExtra(d.boot.line, lang.L("Serving {{.Name}} · {{.Sent}} of {{.Total}}",
		map[string]any{"Name": r.name, "Sent": strings.TrimSuffix(recSize(r.sent.Load()), " MB"), "Total": recSize(r.total)}))
}

// watchOCR waits for the server to end, removes the temporary root (a poller job, beside no
// status read; directly once the poller has stopped) and reports the result.
func (d *DeviceWindow) watchOCR(r *ocrRun, rootID string) {
	r.mu.Lock()
	srv := r.srv
	r.mu.Unlock()
	<-srv.Done()
	serr := srv.Err()
	var derr error
	remove := func() error {
		derr = d.sess.DeleteCertificate(rootID, r.confirm)
		return nil
	}
	d.poller.Do(remove, func(err error) {
		if errors.Is(err, session.ErrStopped) {
			remove()
		}
		close(r.cleaned)
		fyne.Do(func() { d.ocrEnded(r, serr, derr) })
	})
}

// ocrEnded clears the status line and reports a download that did not happen or a root that
// stayed on the device.
func (d *DeviceWindow) ocrEnded(r *ocrRun, serr, derr error) {
	if r.stopTick != nil {
		r.stopTick()
	}
	if d.boot.ocr == r {
		d.boot.ocr = nil
		if d.boot.line != nil {
			d.setStatusExtra(d.boot.line, "")
		}
		d.updatePowerItems()
	}
	if d.closed || d.aborted {
		return
	}
	if errors.Is(serr, bootserve.ErrNotDownloaded) {
		d.onError(userError(lang.L("{{.Name}} did not download the image. Check the firewall on this computer.", map[string]any{"Name": d.name})))
	} else if serr != nil && !errors.Is(serr, bootserve.ErrClosed) {
		d.onError(serr)
	}
	if derr != nil {
		d.onError(fmt.Errorf("%w: %w", userError(lang.L("OpenLAMT could not remove the temporary root certificate from {{.Name}}. Delete “{{.Cert}}” under Device management › Certificates.",
			map[string]any{"Name": d.name, "Cert": bootserve.RootName})), derr))
	}
	if d.onActionDone != nil {
		d.onActionDone()
	}
}

// describeBoot words the errors of Boot Options and One Click Recovery.
func describeBoot(err error) (string, bool) {
	var port *bootserve.PortInUseError
	switch {
	case errors.As(err, &port):
		return lang.L("Port {{.Port}} is in use. Choose another port.", map[string]any{"Port": port.Port}), true
	case errors.Is(err, bootserve.ErrNotDownloaded):
		return lang.L("The device did not download the image. Check the firewall on this computer."), true
	case errors.Is(err, amt.ErrOCROffInBIOS):
		return lang.L("Turn it on in the BIOS first."), true
	case errors.Is(err, amt.ErrAsleep) && errors.Is(err, session.ErrRootNotRemoved): // something was changed
		return lang.L("The device is asleep. Waking it does not restart it, so the boot options would not apply. OpenLAMT could not remove the temporary root certificate. Delete “{{.Cert}}” under Device management › Certificates.",
			map[string]any{"Cert": bootserve.RootName}), true
	case errors.Is(err, amt.ErrAsleep):
		return lang.L("The device is asleep. Waking it does not restart it, so the boot options would not apply. Wake it with Power On, wait until it runs, then try again. Nothing was changed."), true
	}
	return "", false
}

// describeRootLeft is the main text of a failed boot from a file whose temporary root could not
// be removed (issue #149): the cause, then what to delete. It never says that nothing was changed.
func describeRootLeft(err error) string {
	cause := err // session joins the boot error first, then the root failure
	if j, ok := err.(interface{ Unwrap() []error }); ok && len(j.Unwrap()) > 0 {
		cause = j.Unwrap()[0]
	}
	text := ""
	if errors.Is(cause, amt.ErrInvalidValue) {
		text = lang.L("Some of the values you entered are not valid. Check them and try again.")
	} else {
		text = describe(cause)
	}
	return text + " " + lang.L("OpenLAMT could not remove the temporary root certificate. Delete “{{.Cert}}” under Device management › Certificates.",
		map[string]any{"Cert": bootserve.RootName})
}

// describeMayBeSet is the main text of an *amt.BootMayBeSetError (issue #123): the cause, then
// that the boot options or the erase may still apply at the next restart, and what to do.
func describeMayBeSet(err error) string {
	warning := lang.L("OpenLAMT could not clear the boot options again: the next restart of the device may still use them. To clear them, connect again and start Boot Options with Hard disk (default order).")
	if errors.Is(err, amt.ErrEraseMayBeSet) {
		warning = lang.L("OpenLAMT could not clear the erase again: the next restart of the device may still erase the drives. If you do not want that, do not restart the device. Connect again and start Boot Options with Hard disk (default order): it clears the erase before the restart.")
	}
	var be *amt.BootMayBeSetError
	if !errors.As(err, &be) || be.Err == nil {
		return warning
	}
	cause := describe(be.Err)
	if cause == lang.L("Unexpected error: {{.Err}}", map[string]any{"Err": be.Err.Error()}) {
		return warning // the technical text stays in the details
	}
	return cause + " " + warning
}
