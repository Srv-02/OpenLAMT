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
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Made-up settings and certificates (example.com, 192.0.2.0/24).
func exampleSettings() amt.Settings {
	return amt.Settings{
		General: amt.GeneralSettings{HostName: "labserver", DomainName: "example.com", PingResponse: true, NetworkEnabled: true},
		Network: amt.NetworkSettings{Available: true, DHCP: true, IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0",
			Gateway: "192.0.2.1", PrimaryDNS: "192.0.2.53"},
		Features:    amt.Features{Redirection: true, KVM: true, KVMAvailable: true, IDER: true},
		Consent:     amt.ConsentSettings{Available: true, Policy: amt.ConsentKVM, CanModify: true},
		TLS:         amt.TLSSettings{Remote: amt.TLSInterface{Available: true, Enabled: true}},
		Clock:       amt.ClockInfo{Available: true, Offset: -15 * time.Second},
		Environment: amt.EnvironmentDetection{Available: true, Domains: []string{"example.com", "corp.example.com"}},
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

func TestSettingsPageShowsTheValues(t *testing.T) {
	_, p := settingsHarness(t, nil)
	if p.hostText.Text != "labserver" || p.domainText.Text != "example.com" || !p.ping.Checked() || p.ddns.Checked() {
		t.Errorf("general: %q %q ping %v ddns %v", p.hostText.Text, p.domainText.Text, p.ping.Checked(), p.ddns.Checked())
	}
	if !p.envBox.Visible() || p.envValue() != "example.com, corp.example.com" || !hasText(p.envBox, "Intranet domains") {
		t.Errorf("environment detection: shown %v value %q", p.envBox.Visible(), p.envValue())
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
	if p.err.box.Visible() {
		t.Errorf("error %q", p.err.Text())
	}
}

func TestSettingsPageHidesWhatTheDeviceLacks(t *testing.T) {
	_, p := settingsHarness(t, func(f *session.Fake) {
		f.Cfg.Settings.Network.Available = false
		f.Cfg.Settings.Features.KVMAvailable = false
		f.Cfg.Settings.Consent.CanModify = false
		f.Cfg.Settings.Environment.Available = false
	})
	if p.networkBox.Visible() || p.kvmRow.visibleRow() || !p.consent.Disabled() || p.envBox.Visible() {
		t.Errorf("network %v kvm %v consent disabled %v environment %v", p.networkBox.Visible(), p.kvmRow.visibleRow(),
			p.consent.Disabled(), p.envBox.Visible())
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
	if !p.ping.Disabled() || !p.changeHost.Disabled() || !p.changeEnv.Disabled() || !p.turnOff.Disabled() {
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

func TestSettingsHostNameDialog(t *testing.T) {
	h, p := settingsHarness(t, nil)
	test.Tap(p.changeHost)
	d, f := p.dialog, p.nameField
	if d == nil || f == nil || !open(h.m.parent) || d.check == nil {
		t.Fatal("Change… did not open the orange dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Change host name")
	if !hasText(top, "New host name") || f.Text != "labserver" || d.action.Text != "Change Name" || h.m.parent.Canvas().Focused() != f {
		t.Errorf("field %q button %q focus %v", f.Text, d.action.Text, h.m.parent.Canvas().Focused() == f)
	}
	if !hasRichText(t, top, "With Dynamic DNS, the device may no longer be found under its current name. Favorites that use the name stop working until you edit them.", false) ||
		!same(d.warnBox.FillColor, style.Color(style.ColorNameWarningSurface)) {
		t.Error("orange warning box")
	}
	if absPos(f).Y >= absPos(d.warnBox).Y {
		t.Error("the field is not above the warning")
	}
	test.Tap(d.check)
	for _, bad := range []string{"labserver", "", "  ", "not a host!"} {
		f.SetText(bad)
		if !d.action.Disabled() {
			t.Errorf("enabled with %q", bad)
		}
	}
	f.SetText("lab01")
	if d.action.Disabled() {
		t.Fatal("disabled with a new valid name and the box ticked")
	}
	test.Tap(d.check)
	if !d.action.Disabled() {
		t.Fatal("enabled without the box")
	}
	test.Tap(d.check)
	test.Tap(d.action)
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetGeneral"}) || h.dev.Cfg.Settings.General.HostName != "lab01" {
		t.Fatalf("writes %v host %q (the fake checks the confirmation)", got, h.dev.Cfg.Settings.General.HostName)
	}
	if p.hostText.Text != "lab01" || p.domainText.Text != "example.com" {
		t.Errorf("after the write: %q %q", p.hostText.Text, p.domainText.Text)
	}
}

func TestSettingsDomainDialog(t *testing.T) {
	h, p := settingsHarness(t, nil)
	test.Tap(p.changeDomain)
	d, f := p.dialog, p.nameField
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Change domain")
	if !hasText(top, "New domain") || f.Text != "example.com" || d.action.Text != "Change Domain" {
		t.Fatalf("field %q button %q", f.Text, d.action.Text)
	}
	d.Dismiss()
	if len(h.dev.ConfigWrites()) != 0 || p.domainText.Text != "example.com" {
		t.Fatalf("cancel: writes %v", h.dev.ConfigWrites())
	}
	test.Tap(p.changeDomain)
	d, f = p.dialog, p.nameField
	f.SetText("example.org")
	test.Tap(d.check)
	test.Tap(d.action)
	h.written(t)
	g := h.dev.Cfg.Settings.General
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetGeneral"}) || g.DomainName != "example.org" || g.HostName != "labserver" {
		t.Fatalf("writes %v general %+v", got, g)
	}
	if p.domainText.Text != "example.org" {
		t.Errorf("page %q", p.domainText.Text)
	}
}

func TestSettingsNameWriteErrorKeepsTheDeviceValue(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.WriteErr = amt.ErrNotModifiable })
	test.Tap(p.changeHost)
	p.nameField.SetText("lab01")
	test.Tap(p.dialog.check)
	test.Tap(p.dialog.action)
	h.written(t)
	if p.hostText.Text != "labserver" || !p.err.box.Visible() || p.changeHost.Disabled() {
		t.Errorf("host %q error %v button disabled %v", p.hostText.Text, p.err.box.Visible(), p.changeHost.Disabled())
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
	test.Tap(p.sol)
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetFeatures"}) || !h.dev.Cfg.Settings.Features.SOL || !p.sol.Checked() || open(h.m.parent) {
		t.Fatalf("SOL on: writes %v", got)
	}
	p.consent.Tap(2)
	h.written(t)
	if h.dev.Cfg.Settings.Consent.Policy != amt.ConsentAll || p.consent.Selected() != 2 {
		t.Errorf("consent %v page %d", h.dev.Cfg.Settings.Consent.Policy, p.consent.Selected())
	}
}

// Extras.dc.html: disabling KVM, IDE-R or SOL asks with a plain text and the checkbox.
func TestSettingsDisableFeatureAsks(t *testing.T) {
	for _, c := range []struct {
		name        string
		sw          func(*settingsPane) *widgets.Switch
		title, text string
		on          func(amt.Features) bool
	}{
		{"KVM", func(p *settingsPane) *widgets.Switch { return p.kvm }, "Disable KVM on “Lab”?",
			"Open KVM sessions end right away. You can enable KVM again in Settings.", func(f amt.Features) bool { return f.KVM }},
		{"IDE-R", func(p *settingsPane) *widgets.Switch { return p.ider }, "Disable media redirection (IDE-R) on “Lab”?",
			"Open media redirection (IDE-R) sessions end right away. You can enable media redirection again in Settings.",
			func(f amt.Features) bool { return f.IDER }},
		{"SOL", func(p *settingsPane) *widgets.Switch { return p.sol }, "Disable Serial over LAN (SOL) on “Lab”?",
			"Open Serial over LAN (SOL) sessions end right away. You can enable Serial over LAN again in Settings.",
			func(f amt.Features) bool { return f.SOL }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.Settings.Features.SOL = true })
			s := c.sw(p)
			test.Tap(s)
			d := p.dialog
			if d == nil || !open(h.m.parent) || !s.Checked() || len(h.dev.ConfigWrites()) != 0 {
				t.Fatalf("no dialog, or switched off before the confirmation: checked %v writes %v", s.Checked(), h.dev.ConfigWrites())
			}
			top := h.m.parent.Canvas().Overlays().Top()
			findLabel(t, top, c.title)
			if !hasRichText(t, top, c.text, false) || d.warnBox != nil || d.action.Text != "Disable" {
				t.Errorf("text, warning box %v, button %q", d.warnBox != nil, d.action.Text)
			}
			if !d.action.Disabled() {
				t.Fatal("button enabled before the box is ticked")
			}
			d.Dismiss()
			if !s.Checked() || len(h.dev.ConfigWrites()) != 0 {
				t.Fatal("cancel changed something")
			}
			test.Tap(s)
			test.Tap(p.dialog.check)
			test.Tap(p.dialog.action)
			h.written(t)
			if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetFeatures"}) || c.on(h.dev.Cfg.Settings.Features) || s.Checked() {
				t.Fatalf("writes %v checked %v (the fake checks the confirmation)", got, s.Checked())
			}
		})
	}
}

func TestSettingsDisableFeatureErrorPutsTheSwitchBack(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.WriteErr = amt.ErrNotModifiable })
	test.Tap(p.kvm)
	test.Tap(p.dialog.check)
	test.Tap(p.dialog.action)
	h.written(t)
	if !p.kvm.Checked() || !p.err.box.Visible() || p.kvm.Disabled() {
		t.Errorf("kvm %v error %v disabled %v", p.kvm.Checked(), p.err.box.Visible(), p.kvm.Disabled())
	}
}

func TestSettingsDynamicDNS(t *testing.T) {
	h, p := settingsHarness(t, nil)
	test.Tap(p.ddns)
	d := p.dialog
	if d == nil || p.ddns.Checked() || len(h.dev.ConfigWrites()) != 0 {
		t.Fatal("no dialog, or switched before the confirmation")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Enable Dynamic DNS?")
	if !hasRichText(t, top, "AMT registers its own host name in DNS. The device may then be reachable under a different name than in your favorites.", false) ||
		d.action.Text != "Enable" || !d.action.Disabled() {
		t.Fatalf("text or button %q", d.action.Text)
	}
	test.Tap(d.check)
	test.Tap(d.action)
	h.written(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetGeneral"}) || h.dev.Cfg.Settings.General.DynamicDNS != amt.DDNSClient || !p.ddns.Checked() {
		t.Fatalf("writes %v mode %v", got, h.dev.Cfg.Settings.General.DynamicDNS)
	}

	test.Tap(p.ddns)
	top = h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Disable Dynamic DNS?")
	if !hasRichText(t, top, "The device's host name is no longer registered in DNS. Favorites that use this name may stop working.", false) ||
		p.dialog.action.Text != "Disable" {
		t.Fatalf("button %q", p.dialog.action.Text)
	}
	test.Tap(p.dialog.check)
	test.Tap(p.dialog.action)
	h.written(t)
	if h.dev.Cfg.Settings.General.DynamicDNS != amt.DDNSOff || p.ddns.Checked() {
		t.Fatalf("mode %v", h.dev.Cfg.Settings.General.DynamicDNS)
	}
}

// DDNS by the DHCP server counts as on: the switch shows it, switching off turns it off.
func TestSettingsDynamicDNSByDHCPServerIsOn(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.Settings.General.DynamicDNS = amt.DDNSByDHCPServer })
	if !p.ddns.Checked() {
		t.Fatal("DDNS by the DHCP server shown as off")
	}
	test.Tap(p.ddns)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Disable Dynamic DNS?")
	test.Tap(p.dialog.check)
	test.Tap(p.dialog.action)
	h.written(t)
	if h.dev.Cfg.Settings.General.DynamicDNS != amt.DDNSOff {
		t.Fatalf("mode %v", h.dev.Cfg.Settings.General.DynamicDNS)
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
