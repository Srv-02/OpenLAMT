package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The confirmations of the Settings page: host name and domain (orange, HostEnv.dc.html),
// Dynamic DNS and disabling KVM, IDE-R or SOL (orange with a plain text, Extras.dc.html),
// environment detection (red, HostEnv.dc.html, manage_settings_env.go), network settings (red,
// Forms.dc.html middle), TLS certificate (orange with the red warning box, Forms.dc.html right)
// and turning off AMT (red, Danger.dc.html middle).

// openName opens "Change host name" or, for domain, "Change domain": the field holds the device's
// value; the button needs the box ticked and a new, valid, non-empty value.
func (p *settingsPane) openName(domain bool) {
	cur := p.d.src.val.General
	title, label, button, value := lang.L("Change host name"), lang.L("New host name"), lang.L("Change Name"), cur.HostName
	if domain {
		title, label, button, value = lang.L("Change domain"), lang.L("New domain"), lang.L("Change Domain"), cur.DomainName
	}
	var field *dialogEntry
	change := func() amt.GeneralChange {
		v := strings.TrimSpace(field.Text)
		if domain {
			return amt.GeneralChange{DomainName: &v}
		}
		return amt.GeneralChange{HostName: &v}
	}
	p.dialog = showOrangeConfirm(p.window(), orangeSpec{
		op: amt.OpHostName, host: p.m.confirmHost(), title: title,
		warning: lang.L("With Dynamic DNS, the device may no longer be found under its current name. Favorites that use the name stop working until you edit them."),
		lead: func(d *riskDialog) []fyne.CanvasObject {
			field = d.newEntry(false)
			field.SetText(value)
			field.OnChanged = func(string) { d.Revalidate() }
			d.focus = field
			return []fyne.CanvasObject{formField(label, framedEntry(field))}
		},
		ready: func() bool {
			v := strings.TrimSpace(field.Text)
			return v != "" && v != value && change().Validate() == nil
		},
		button: button,
		onConfirm: func(c amt.Confirmation) {
			ch := change()
			p.d.write(func() error { return p.m.sess.SetGeneral(ch, c) }, true)
		},
	})
	p.nameField = field
}

// confirmDDNS asks before Dynamic DNS is switched on (the AMT client registers the name,
// DDNSClient) or off (also DDNS by the DHCP server ends).
func (p *settingsPane) confirmDDNS(on bool) {
	mode := amt.DDNSOff
	title, text, button := lang.L("Disable Dynamic DNS?"),
		lang.L("The device's host name is no longer registered in DNS. Favorites that use this name may stop working."),
		lang.L("Disable")
	if on {
		mode = amt.DDNSClient
		title, text, button = lang.L("Enable Dynamic DNS?"),
			lang.L("AMT registers its own host name in DNS. The device may then be reachable under a different name than in your favorites."),
			lang.L("Enable")
	}
	p.dialog = showOrangeConfirm(p.window(), orangeSpec{
		op: amt.OpHostName, host: p.m.confirmHost(), title: title, text: text, button: button,
		onConfirm: func(c amt.Confirmation) {
			p.d.write(func() error { return p.m.sess.SetGeneral(amt.GeneralChange{DynamicDNS: &mode}, c) }, true)
		},
	})
}

// feature is a redirection service of the Remote features group.
type feature int

const (
	featureKVM feature = iota
	featureIDER
	featureSOL
)

// confirmFeatureOff asks before KVM, IDE-R or SOL is disabled (Extras.dc.html; IDE-R and SOL use
// the KVM dialog with the names of their rows).
func (p *settingsPane) confirmFeatureOff(f feature, change amt.FeatureChange) {
	args := map[string]any{"Name": p.m.name}
	var title, text string
	switch f {
	case featureKVM:
		title = lang.L("Disable KVM on “{{.Name}}”?", args)
		text = lang.L("Open KVM sessions end right away. You can enable KVM again in Settings.")
	case featureIDER:
		title = lang.L("Disable media redirection (IDE-R) on “{{.Name}}”?", args)
		text = lang.L("Open media redirection (IDE-R) sessions end right away. You can enable media redirection again in Settings.")
	case featureSOL:
		title = lang.L("Disable Serial over LAN (SOL) on “{{.Name}}”?", args)
		text = lang.L("Open Serial over LAN (SOL) sessions end right away. You can enable Serial over LAN again in Settings.")
	}
	p.dialog = showOrangeConfirm(p.window(), orangeSpec{
		op: amt.OpFeaturesOff, host: p.m.confirmHost(), title: title, text: text, button: lang.L("Disable"),
		onConfirm: func(c amt.Confirmation) {
			p.d.write(func() error { return p.m.sess.SetFeatures(change, c) }, true)
		},
	})
}

// confirmTurnOff asks before AMT is turned off on the network. Afterwards the device does not
// answer any more, so the settings are not read again.
func (p *settingsPane) confirmTurnOff() {
	p.dialog = showRedConfirm(p.window(), redSpec{
		op: amt.OpDisableNetwork, host: p.m.confirmHost(),
		title:       lang.L("Turn off AMT on the network?"),
		warningBold: lang.L("This cannot be undone remotely."),
		warningRest: lang.L("“{{.Name}}” stops answering on the network right away: no power control, KVM or media, not from OpenLAMT and not from any other tool. Only the BIOS (MEBx) at the device can turn it back on.",
			map[string]any{"Name": p.m.name}),
		button: lang.L("Turn Off AMT"),
		onConfirm: func(c amt.Confirmation) {
			p.d.write(func() error { return p.m.sess.DisableAMTNetwork(c) }, false)
		},
	})
}

// networkForm is the content of "Change network settings".
type networkForm struct {
	cur                      amt.NetworkSettings
	mode                     *widgets.Segmented // 0 DHCP, 1 static
	ip, mask, gw, dns1, dns2 *dialogEntry
}

// openNetwork opens "Change network settings" with segment i (0 DHCP, 1 static) chosen; the
// segment of the page keeps the device's value until the change is saved.
func (p *settingsPane) openNetwork(i int) {
	cur := p.d.src.val.Network
	p.address.SetSelected(map[bool]int{true: 0, false: 1}[cur.DHCP])
	f := &networkForm{cur: cur}
	p.netForm = f
	p.dialog = showRedConfirm(p.window(), redSpec{
		op: amt.OpNetwork, host: p.m.confirmHost(),
		title:       lang.L("Change network settings"),
		warningBold: lang.L("Risk of lockout."),
		warningRest: lang.L("A wrong address makes the device unreachable. Only the BIOS (MEBx) at the device can fix it."),
		extra:       func(d *riskDialog) []fyne.CanvasObject { return f.build(d, i) },
		ready:       f.ready,
		button:      lang.L("Save"),
		onConfirm: func(c amt.Confirmation) {
			change := f.change()
			p.d.write(func() error { return p.m.sess.SetNetwork(change, c) }, true)
		},
	})
}

func (f *networkForm) build(d *riskDialog, mode int) []fyne.CanvasObject {
	entry := func(v string) *dialogEntry {
		e := d.newEntry(false)
		e.SetText(v)
		e.OnChanged = func(string) { d.Revalidate() }
		return e
	}
	c := f.cur
	f.ip, f.mask, f.gw = entry(c.IPAddress), entry(c.SubnetMask), entry(c.Gateway)
	f.dns1, f.dns2 = entry(c.PrimaryDNS), entry(c.SecondaryDNS)
	f.mode = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("DHCP"), lang.L("Static")), nil)
	f.mode.SetSelected(mode)
	f.mode.OnChanged = func(int) { f.sync(); d.Revalidate() }
	f.sync()
	return []fyne.CanvasObject{
		container.New(&rowLayout{fill: -1}, f.mode),
		container.New(&settingsColumns{gap: 10}, settingsField(lang.L("IP address"), f.ip), settingsField(lang.L("Subnet mask"), f.mask)),
		settingsField(lang.L("Gateway"), f.gw),
		container.New(&settingsColumns{gap: 10}, settingsField(lang.L("Primary DNS"), f.dns1), settingsField(lang.L("Secondary DNS"), f.dns2)),
	}
}

// sync enables the address fields for a static address only.
func (f *networkForm) sync() {
	for _, e := range []*dialogEntry{f.ip, f.mask, f.gw, f.dns1, f.dns2} {
		setEnabled(e, f.mode.Selected() == 1)
	}
}

// change is the change the form describes. A static address turns IP sync off: the typed
// addresses are the configuration.
func (f *networkForm) change() amt.NetworkChange {
	if f.mode.Selected() == 0 {
		on := true
		return amt.NetworkChange{DHCP: &on}
	}
	off := false
	val := func(e *dialogEntry) *string { s := strings.TrimSpace(e.Text); return &s }
	return amt.NetworkChange{DHCP: &off, IPSync: &off, IPAddress: val(f.ip), SubnetMask: val(f.mask),
		Gateway: val(f.gw), PrimaryDNS: val(f.dns1), SecondaryDNS: val(f.dns2)}
}

// ready: the values are valid (IPv4, contiguous mask, gateway in the subnet) and differ from the
// device's.
func (f *networkForm) ready() bool {
	c := f.change()
	return c.Validate(f.cur) == nil && c.Risky(f.cur) != nil
}

// settingsField is a labeled entry of a form dialog (Forms.dc.html .lbl, .inp): 12 px semibold
// label, 5 px above the framed entry.
func settingsField(label string, e *dialogEntry) fyne.CanvasObject {
	border := style.BindRect(&canvas.Rectangle{CornerRadius: confirmFieldRadius, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder)
	field := container.NewStack(border, container.NewThemeOverride(e, style.CardTheme{}))
	return container.New(layout.NewCustomPaddedVBoxLayout(5), designText(label, 12, style.ColorNameControlText, true), field)
}

// settingsColumns puts two objects side by side in equal halves (Forms.dc.html .two).
type settingsColumns struct{ gap float32 }

func (l *settingsColumns) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := max(0, (size.Width-l.gap)/2)
	for i, o := range objs {
		o.Move(fyne.NewPos(float32(i)*(w+l.gap), 0))
		o.Resize(fyne.NewSize(w, size.Height))
	}
}

func (l *settingsColumns) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		w, h = max(w, o.MinSize().Width), max(h, o.MinSize().Height)
	}
	return fyne.NewSize(2*w+l.gap, h)
}

// tlsForm is the content of "Change TLS certificate".
type tlsForm struct {
	certs    []amt.Certificate // usable ones: private key, no trusted root
	options  []*tlsOption
	current  string // ID of the bound certificate
	selected string
}

// openTLS reads the certificates and opens "Change TLS certificate". Mutual authentication is not
// offered: the change only names the certificate.
func (p *settingsPane) openTLS() {
	var certs []amt.Certificate
	p.d.run(func() error {
		var err error
		certs, err = p.m.sess.TLSCertificates()
		return err
	}, func(err error) {
		if err != nil {
			p.d.fail(err)
			return
		}
		p.showTLS(certs)
	})
}

func (p *settingsPane) showTLS(all []amt.Certificate) {
	f := &tlsForm{}
	for _, c := range all {
		if c.Usable() {
			f.certs = append(f.certs, c)
			if c.TLS {
				f.current = c.ID
			}
		}
	}
	f.selected = f.current
	p.tlsForm = f
	p.dialog = showOrangeConfirm(p.window(), orangeSpec{
		op: amt.OpTLS, host: p.m.confirmHost(), danger: true,
		title:       lang.L("Change TLS certificate"),
		warningBold: lang.L("Risk of lockout."),
		warning:     lang.L("AMT restarts TLS with the new certificate. OpenLAMT updates the saved fingerprint for this device."),
		extra:       f.build,
		ready:       func() bool { return f.selected != "" && f.selected != f.current },
		button:      lang.L("Change Certificate"),
		onConfirm: func(c amt.Confirmation) {
			id := f.selected
			p.d.write(func() error {
				return p.m.sess.SetTLS(amt.TLSChange{Certificate: &id}, c, p.m.deps.loginStore)
			}, true)
		},
	})
}

func (f *tlsForm) build(d *riskDialog) []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, c := range f.certs {
		o := newTLSOption(c, c.ID == f.current, func(id string) {
			f.selected = id
			for _, x := range f.options {
				x.setOn(x.id == id)
			}
			d.Revalidate()
		})
		o.setOn(c.ID == f.selected)
		f.options = append(f.options, o)
		if len(rows) > 0 {
			rows = append(rows, hline(style.ColorNameCardBorder))
		}
		rows = append(rows, o)
	}
	head := designText(lang.L("Certificate"), 12, style.ColorNameTextSecondary, true)
	list := widgets.NewPanel(style.ColorNameField, style.ColorNameCardBorder, 10, container.New(layout.NewCustomPaddedVBoxLayout(0), rows...), 1, 1, 1, 1)
	return []fyne.CanvasObject{container.New(layout.NewCustomPaddedLayout(4, 0, 0, 0), head), list}
}

// commonName returns the CN of an X.500 subject ("C=…,O=…,CN=…"), else the subject.
func commonName(subject string) string {
	for _, part := range strings.Split(subject, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok && strings.EqualFold(k, "CN") && v != "" {
			return v
		}
	}
	return subject
}

// tlsOption is a certificate of the list: radio mark, name with "(current)" for the bound one,
// and "Valid until Mar 2028 · SHA-256 7B:11:…:E0" below (Forms.dc.html .opt).
type tlsOption struct {
	widget.BaseWidget
	id      string
	on      bool
	mark    *widget.Icon
	onTap   func(id string)
	content fyne.CanvasObject
}

func newTLSOption(c amt.Certificate, current bool, tapped func(string)) *tlsOption {
	o := &tlsOption{id: c.ID, onTap: tapped, mark: widget.NewIcon(nil)}
	name := []fyne.CanvasObject{designText(commonName(c.Subject), 13, theme.ColorNameForeground, false)}
	if current {
		name = append(name, designText(lang.L("(current)"), 13, style.ColorNameTextSecondary, false))
	}
	hint := designText(lang.L("Valid until {{.Date}} · SHA-256 {{.Fingerprint}}", map[string]any{
		"Date": c.NotAfter.UTC().Format("Jan 2006"), "Fingerprint": shortFingerprint(c.Fingerprint)}),
		12, style.ColorNameTextSecondary, false)
	text := container.New(layout.NewCustomPaddedVBoxLayout(1), container.New(&rowLayout{gap: 4, fill: -1}, name...), hint)
	mark := container.New(&manageSizeLayout{w: 18, h: 18}, o.mark)
	o.content = container.New(layout.NewCustomPaddedLayout(9, 9, 12, 12),
		container.New(&rowLayout{gap: 10, fill: 1}, mark, text))
	o.ExtendBaseWidget(o)
	o.setOn(false)
	return o
}

func (o *tlsOption) setOn(on bool) {
	o.on = on
	if on {
		o.mark.SetResource(theme.NewPrimaryThemedResource(theme.RadioButtonCheckedIcon()))
	} else {
		o.mark.SetResource(theme.RadioButtonIcon())
	}
}

func (o *tlsOption) Tapped(*fyne.PointEvent) {
	if o.onTap != nil {
		o.onTap(o.id)
	}
}

func (o *tlsOption) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(o.content) }
