package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Made-up settings and certificates (example.com, 192.0.2.0/24).
func exampleSettings() amt.Settings {
	return amt.Settings{
		General: amt.GeneralSettings{HostName: "labserver", DomainName: "example.com", PingResponse: true, NetworkEnabled: true},
		Network: amt.NetworkSettings{Available: true, DHCP: true, IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0",
			Gateway: "192.0.2.1", PrimaryDNS: "192.0.2.53"},
		Features: amt.Features{Redirection: true, KVM: true, KVMAvailable: true, IDER: true},
		Consent:  amt.ConsentSettings{Available: true, Policy: amt.ConsentKVM, CanModify: true},
		TLS:      amt.TLSSettings{Remote: amt.TLSInterface{Available: true, Enabled: true}},
		Clock:    amt.ClockInfo{Available: true, Offset: -15 * time.Second},
	}
}

const (
	certFP     = "4f2a" + "0000000000000000000000000000000000000000000000000000000000" + "9c"
	certFPNext = "7b11" + "1111111111111111111111111111111111111111111111111111111111" + "e0"
)

func exampleCerts() []amt.Certificate {
	return []amt.Certificate{
		{ID: "Intel(r) AMT Certificate: Handle: 0", Subject: "C=US,O=Example,CN=labserver.example.com",
			NotAfter: time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC), Fingerprint: certFPNext, HasPrivateKey: true},
		{ID: "Intel(r) AMT Certificate: Handle: 1", Subject: "CN=AMT self-signed",
			NotAfter: time.Date(2041, 12, 31, 0, 0, 0, 0, time.UTC), Fingerprint: certFP, HasPrivateKey: true, TLS: true},
		{ID: "Intel(r) AMT Certificate: Handle: 2", Subject: "CN=Example Root CA", Fingerprint: certFP, TrustedRoot: true, HasPrivateKey: true},
		{ID: "Intel(r) AMT Certificate: Handle: 3", Subject: "CN=nokey.example.com", Fingerprint: certFP},
	}
}

// settingsHarness opens the Settings tab of the sidebar on a fake with exampleSettings.
func settingsHarness(t *testing.T, edit func(*session.Fake)) (*manageHarness, *settingsPane) {
	t.Helper()
	englishUI(t)
	dev := &session.Fake{FP: certFP}
	dev.Cfg.Settings = exampleSettings()
	dev.Cfg.Certificates = exampleCerts()
	if edit != nil {
		edit(dev)
	}
	h := newManageHarness(t, dev)
	h.m.parent.SetContent(h.m.Sidebar())
	h.m.parent.Resize(fyne.NewSize(400, 800))
	h.wait(t) // hardware
	h.m.side.tabs.Tap(3)
	h.wait(t)
	return h, h.m.side.view.panes[3].impl.(*settingsPane)
}

// written waits for a write and the read after it.
func (h *manageHarness) written(t *testing.T) {
	t.Helper()
	h.wait(t)
	h.wait(t)
}

func catchNotApproved(t *testing.T) *[]string {
	var got []string
	notApprovedHook = func(what string) { got = append(got, what) }
	t.Cleanup(func() { notApprovedHook = nil })
	return &got
}

func TestSettingsPageShowsTheValues(t *testing.T) {
	_, p := settingsHarness(t, nil)
	if p.host.Text != "labserver" || p.domain.Text != "example.com" || !p.ping.Checked() || p.ddns.Checked() {
		t.Errorf("general: %q %q ping %v ddns %v", p.host.Text, p.domain.Text, p.ping.Checked(), p.ddns.Checked())
	}
	if p.address.Selected() != 0 || p.ipText.Text != "192.0.2.10 / 24 · gateway 192.0.2.1" || !p.networkBox.Visible() {
		t.Errorf("network: %d %q", p.address.Selected(), p.ipText.Text)
	}
	if !p.kvmRow.visibleRow() || !p.kvm.Checked() || !p.ider.Checked() || p.sol.Checked() || p.consent.Selected() != 1 || p.consent.Disabled() {
		t.Errorf("features: kvm %v ider %v sol %v consent %d", p.kvm.Checked(), p.ider.Checked(), p.sol.Checked(), p.consent.Selected())
	}
	if p.clockText.Text != "15 s behind" || p.tlsText.Text != "SHA-256 4F:2A:…:9C" {
		t.Errorf("clock %q tls %q", p.clockText.Text, p.tlsText.Text)
	}
	if p.host.MinSize().Width != 220 || p.err.box.Visible() {
		t.Errorf("host field %v, error %q", p.host.MinSize(), p.err.Text())
	}
}

func TestSettingsPageHidesWhatTheDeviceLacks(t *testing.T) {
	_, p := settingsHarness(t, func(f *session.Fake) {
		f.Cfg.Settings.Network.Available = false
		f.Cfg.Settings.Features.KVMAvailable = false
		f.Cfg.Settings.Consent.CanModify = false
	})
	if p.networkBox.Visible() || p.kvmRow.visibleRow() || !p.consent.Disabled() {
		t.Errorf("network %v kvm %v consent disabled %v", p.networkBox.Visible(), p.kvmRow.visibleRow(), p.consent.Disabled())
	}
}

func TestSettingsFormats(t *testing.T) {
	englishUI(t)
	for off, want := range map[time.Duration]string{
		-15 * time.Second: "15 s behind", 4 * time.Second: "4 s ahead", 0: "0 s behind",
		-3 * time.Minute: "3 min behind", 90 * time.Second: "1 min ahead",
	} {
		if got := clockOffsetText(off); got != want {
			t.Errorf("%v: %q, want %q", off, got, want)
		}
	}
	if got := ipSummary(amt.NetworkSettings{IPAddress: "192.0.2.10", SubnetMask: "255.255.240.0"}); got != "192.0.2.10 / 20" {
		t.Errorf("without gateway: %q", got)
	}
	if got := ipSummary(amt.NetworkSettings{IPAddress: "192.0.2.10", SubnetMask: "255.0.255.0", Gateway: "192.0.2.1"}); got != "192.0.2.10 / 255.0.255.0 · gateway 192.0.2.1" {
		t.Errorf("mask without prefix: %q", got)
	}
	if got := shortFingerprint(strings.ToUpper(amt.FormatFingerprint(certFPNext))); got != "7B:11:…:E0" {
		t.Errorf("fingerprint %q", got)
	}
	if got := commonName("C=US, O=Example, CN=amt.example.com"); got != "amt.example.com" {
		t.Errorf("CN %q", got)
	}
}

func TestSettingsPingWritesAtOnce(t *testing.T) {
	h, p := settingsHarness(t, nil)
	test.Tap(p.ping)
	if !p.ping.Disabled() || !p.host.Disabled() || !p.turnOff.Disabled() {
		t.Fatal("the page is not locked while writing")
	}
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetGeneral"}) || h.dev.Cfg.Settings.General.PingResponse {
		t.Fatalf("writes %v ping %v", got, h.dev.Cfg.Settings.General.PingResponse)
	}
	if p.ping.Checked() || p.ping.Disabled() || h.m.settings.src.reads != 2 {
		t.Errorf("after the write: checked %v disabled %v reads %d", p.ping.Checked(), p.ping.Disabled(), h.m.settings.src.reads)
	}
}

func TestSettingsHostNameAsksOrange(t *testing.T) {
	h, p := settingsHarness(t, nil)
	c := h.m.parent.Canvas()
	c.Focus(p.host)
	p.host.SetText("lab01")
	p.host.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	d := p.dialog
	if d == nil || !open(h.m.parent) || d.check == nil {
		t.Fatal("Enter did not open the orange confirmation")
	}
	if !hasRichText(t, d.frame, "labserver.example.com → ", false) || !hasRichText(t, d.frame, "lab01.example.com", true) {
		t.Error("detail line labserver.example.com → lab01.example.com missing")
	}
	if p.host.Text != "lab01" {
		t.Errorf("the field lost the new name while asking: %q", p.host.Text)
	}
	if !d.action.Disabled() {
		t.Fatal("button enabled before the checkbox")
	}
	test.Tap(d.check)
	test.Tap(d.action)
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetGeneral"}) || h.dev.Cfg.Settings.General.HostName != "lab01" {
		t.Fatalf("writes %v host %q (the fake checks the confirmation)", got, h.dev.Cfg.Settings.General.HostName)
	}
	if p.host.Text != "lab01" {
		t.Errorf("after the write: %q", p.host.Text)
	}
}

func TestSettingsDomainUsesTheNameDialogAndCancelReverts(t *testing.T) {
	h, p := settingsHarness(t, nil)
	h.m.parent.Canvas().Focus(p.domain)
	p.domain.SetText("example.org")
	p.domain.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	d := p.dialog
	if d == nil || !hasRichText(t, d.frame, "labserver.example.org", true) {
		t.Fatal("the domain does not ask with the full name")
	}
	d.Dismiss()
	if p.domain.Text != "example.com" || len(h.dev.ConfigWrites()) != 0 {
		t.Errorf("cancel: %q writes %v", p.domain.Text, h.dev.ConfigWrites())
	}
}

func TestSettingsNameRevertsWithoutEnter(t *testing.T) {
	h, p := settingsHarness(t, nil)
	c := h.m.parent.Canvas()
	c.Focus(p.host)
	p.host.SetText("lab02")
	p.host.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if p.host.Text != "labserver" || open(h.m.parent) {
		t.Errorf("Esc: %q dialog %v", p.host.Text, open(h.m.parent))
	}
	c.Focus(p.host)
	p.host.SetText("lab03")
	c.Unfocus()
	if p.host.Text != "labserver" {
		t.Errorf("focus lost: %q", p.host.Text)
	}
	c.Focus(p.host)
	p.host.SetText("not a host!")
	p.host.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if open(h.m.parent) || p.host.Text != "labserver" || !p.err.box.Visible() {
		t.Errorf("invalid name: dialog %v text %q error %v", open(h.m.parent), p.host.Text, p.err.box.Visible())
	}
}

func TestSettingsNetworkDialog(t *testing.T) {
	h, p := settingsHarness(t, nil)
	p.address.Tap(1)
	d, f := p.dialog, p.netForm
	if d == nil || f == nil || p.address.Selected() != 0 || f.mode.Selected() != 1 {
		t.Fatalf("dialog %v page segment %d form segment %v", d != nil, p.address.Selected(), f)
	}
	if f.ip.Text != "192.0.2.10" || f.mask.Text != "255.255.255.0" || f.gw.Text != "192.0.2.1" || f.dns1.Text != "192.0.2.53" || f.ip.Disabled() {
		t.Errorf("fields: %q %q %q %q disabled %v", f.ip.Text, f.mask.Text, f.gw.Text, f.dns1.Text, f.ip.Disabled())
	}
	if d.phrase.Text != "device.example.com" {
		t.Errorf("phrase %q", d.phrase.Text)
	}
	d.entry.SetText("device.example.com")
	if d.action.Disabled() {
		t.Fatal("DHCP to static with valid values stays disabled")
	}
	for _, bad := range []func(){
		func() { f.ip.SetText("192.0.2.300") },
		func() { f.mask.SetText("255.0.255.0") },
		func() { f.gw.SetText("198.51.100.1") },
	} {
		bad()
		if !d.action.Disabled() {
			t.Error("invalid value accepted")
		}
		f.ip.SetText("192.0.2.10")
		f.mask.SetText("255.255.255.0")
		f.gw.SetText("192.0.2.1")
	}
	d.entry.SetText("device.example.co")
	if !d.action.Disabled() {
		t.Error("enabled with a mistyped address")
	}
	d.entry.SetText("device.example.com")
	f.mode.Tap(0)
	if !f.ip.Disabled() || !f.dns2.Disabled() || !d.action.Disabled() {
		t.Errorf("DHCP: fields disabled %v, button disabled %v (nothing changed)", f.ip.Disabled(), d.action.Disabled())
	}
	f.mode.Tap(1)
	f.ip.SetText("192.0.2.20")
	test.Tap(d.action)
	h.written(t)
	n := h.dev.Cfg.Settings.Network
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetNetwork"}) || n.DHCP || n.IPAddress != "192.0.2.20" || n.Gateway != "192.0.2.1" {
		t.Fatalf("writes %v network %+v", got, n)
	}
	if p.address.Selected() != 1 || p.ipText.Text != "192.0.2.20 / 24 · gateway 192.0.2.1" {
		t.Errorf("page: %d %q", p.address.Selected(), p.ipText.Text)
	}
}

func TestSettingsFeatures(t *testing.T) {
	h, p := settingsHarness(t, nil)
	asked := catchNotApproved(t)
	test.Tap(p.sol)
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetFeatures"}) || !h.dev.Cfg.Settings.Features.SOL || !p.sol.Checked() {
		t.Fatalf("SOL on: writes %v", got)
	}
	test.Tap(p.kvm)
	test.Tap(p.ddns)
	if !p.kvm.Checked() || p.ddns.Checked() || len(h.dev.ConfigWrites()) != 1 || len(*asked) != 2 || p.kvm.Disabled() {
		t.Errorf("off without design: kvm %v ddns %v writes %v asked %v", p.kvm.Checked(), p.ddns.Checked(), h.dev.ConfigWrites(), *asked)
	}
	p.consent.Tap(2)
	h.written(t)
	if h.dev.Cfg.Settings.Consent.Policy != amt.ConsentAll || p.consent.Selected() != 2 {
		t.Errorf("consent %v page %d", h.dev.Cfg.Settings.Consent.Policy, p.consent.Selected())
	}
}

func TestSettingsWriteErrorPutsTheControlBack(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.WriteErr = amt.ErrNotModifiable })
	p.consent.Tap(0)
	h.written(t)
	if p.consent.Selected() != 1 || !p.err.box.Visible() {
		t.Fatalf("consent %d error %v", p.consent.Selected(), p.err.box.Visible())
	}
	var got []error
	h.m.deps.onError = func(err error) { got = append(got, err) }
	test.Tap(p.ping)
	h.written(t)
	if len(got) != 1 || !errors.Is(got[0], amt.ErrNotModifiable) || !p.ping.Checked() || p.err.box.Visible() {
		t.Errorf("onError %v ping %v page error %v", got, p.ping.Checked(), p.err.box.Visible())
	}
}

func TestSettingsSyncClock(t *testing.T) {
	h, p := settingsHarness(t, nil)
	test.Tap(p.sync)
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SyncClock"}) || p.clockText.Text != "0 s behind" {
		t.Errorf("writes %v clock %q", got, p.clockText.Text)
	}
}

func TestSettingsTLSDialog(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) {
		f.Cfg.Settings.TLS.Remote.MutualAuth = true // the change must leave it alone
		f.Cfg.Settings.TLS.Remote.TrustedCNs = []string{"client.example.com"}
	})
	test.Tap(p.change)
	h.wait(t) // certificates
	d, f := p.dialog, p.tlsForm
	if d == nil || f == nil || len(f.options) != 2 || f.selected != f.current || f.current != "Intel(r) AMT Certificate: Handle: 1" {
		t.Fatalf("dialog %v form %+v", d != nil, f)
	}
	if !same(d.warnBox.FillColor, style.Color(style.ColorNameDangerSurface)) {
		t.Error("the warning box is not red")
	}
	test.Tap(d.check)
	if !d.action.Disabled() {
		t.Fatal("enabled with the current certificate")
	}
	test.Tap(f.options[0])
	if d.action.Disabled() || !f.options[0].on || f.options[1].on {
		t.Fatal("other certificate and checkbox do not enable the button")
	}
	test.Tap(d.action)
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetTLS"}) {
		t.Fatalf("writes %v", got)
	}
	if !h.dev.Cfg.Certificates[0].TLS || !h.dev.Cfg.Settings.TLS.Remote.MutualAuth {
		t.Errorf("bound %v mutual auth %v", h.dev.Cfg.Certificates[0].TLS, h.dev.Cfg.Settings.TLS.Remote.MutualAuth)
	}
	if p.tlsText.Text != "SHA-256 7B:11:…:E0" {
		t.Errorf("page shows %q after the change", p.tlsText.Text)
	}
}

func TestSettingsTurnOffAMT(t *testing.T) {
	h, p := settingsHarness(t, nil)
	test.Tap(p.turnOff)
	d := p.dialog
	if d == nil || d.entry == nil || !hasRichText(t, d.frame, "This cannot be undone remotely. ", true) {
		t.Fatal("no red confirmation")
	}
	if !hasRichText(t, d.frame, "“Lab” stops answering on the network right away: no power control, KVM or media, not from OpenLAMT and not from any other tool. Only the BIOS (MEBx) at the device can turn it back on.", false) {
		t.Error("warning text")
	}
	d.entry.SetText("device.example.com")
	test.Tap(d.action)
	h.wait(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"DisableAMTNetwork"}) || h.dev.Cfg.Settings.General.NetworkEnabled {
		t.Fatalf("writes %v", got)
	}
	if h.m.settings.src.reads != 1 {
		t.Errorf("read again after turning AMT off: %d reads", h.m.settings.src.reads)
	}
}

func TestSettingsReadError(t *testing.T) {
	_, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.SettingsErr = amt.ErrAccessDenied })
	if !p.err.box.Visible() || p.err.Text() != "This account is not allowed to read this information on the device." || p.d.src.loaded() {
		t.Errorf("error %v %q", p.err.box.Visible(), p.err.Text())
	}
}
