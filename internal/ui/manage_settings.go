package ui

import (
	"net/netip"
	"strconv"
	"strings"
	"time"

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

// The Settings page of device management (Settings.dc.html): one column over the full width with
// the groups General, Network, Remote features, Clock and TLS and the Danger zone. Harmless
// changes are written at once, risky ones after an orange or red confirmation
// (manage_settings_dialogs.go). Every write locks the page and reads the settings again.

// settingsData is the settings of one device and the write in progress, shared by the sidebar and
// the window. Only touched on the Fyne thread.
type settingsData struct {
	m        *manager
	src      *manageSource[amt.Settings]
	busy     bool  // a write (or the read of the certificates) runs: the page is locked
	writeErr error // the last write failed and the device window has no onError: the page shows it
}

func newSettingsData(m *manager) *settingsData {
	return &settingsData{m: m, src: newManageSource(m, m.sess.Settings)}
}

// locked reports whether the controls are locked: a write or a read runs.
func (d *settingsData) locked() bool { return d.busy || d.src.loading }

// run calls fn off the Fyne thread with the page locked and then done with its error on the Fyne
// thread; after Close the result is dropped.
func (d *settingsData) run(fn func() error, done func(error)) {
	if d.busy || d.m.closed {
		return
	}
	d.busy, d.writeErr = true, nil
	d.src.notify()
	go func() {
		err := fn()
		d.m.later(func() {
			if d.m.closed {
				return
			}
			d.busy = false
			done(err)
			d.src.notify()
		})
	}()
}

// write runs a change of the device: a failure goes to the device window's error path, then the
// settings are read again (unless reload is false), so every control shows the device's value.
func (d *settingsData) write(fn func() error, reload bool) {
	d.run(fn, func(err error) {
		if err != nil {
			d.fail(err)
		}
		if reload {
			d.src.reload()
		}
	})
}

// fail reports err: through the device window (onError), else on the page.
func (d *settingsData) fail(err error) {
	if d.m.deps.onError != nil {
		d.m.deps.onError(err)
		return
	}
	d.writeErr = err
}

// confirmHost is the host a confirmation is bound to: the host of the connection, which the
// device checks (Session.Config().Host).
func (m *manager) confirmHost() string {
	if m.sess == nil {
		return m.deps.host
	}
	return m.sess.Config().Host
}

// settingsPane is one view of the Settings page.
type settingsPane struct {
	m       *manager
	d       *settingsData
	compact bool
	pane    *managePane

	err                                           *manageError
	general, network, features, clockTLS          *settingsCard
	generalBox, networkBox, featuresBox, clockBox fyne.CanvasObject
	host, domain                                  *settingsEntry
	ping, ddns, kvm, ider, sol                    *widgets.Switch
	address, consent                              *widgets.Segmented
	ipText, clockText, tlsText                    *canvas.Text
	sync, change, turnOff                         *widgets.PrimaryButton
	ipRow, kvmRow, clockRow, tlsRow               *settingsRowRef
	applying                                      bool // render sets values: the callbacks do nothing
	dialog                                        *riskDialog
	netForm                                       *networkForm
	tlsForm                                       *tlsForm
}

// settingsPane builds the Settings page.
func (m *manager) settingsPane(compact bool) *managePane {
	p := &settingsPane{m: m, d: m.settings, compact: compact, err: newManageError()}

	p.host = newSettingsEntry(func(v string) { p.confirmName(&v, nil) })
	p.domain = newSettingsEntry(func(v string) { p.confirmName(nil, &v) })
	p.ping = p.newSwitch(func(on bool) {
		p.d.write(func() error {
			return m.sess.SetGeneral(amt.GeneralChange{PingResponse: &on}, amt.Confirmation{})
		}, true)
	})
	p.ddns = p.newSwitch(func(on bool) {
		// TODO(design): the orange confirmation for Dynamic DNS has no approved text yet.
		notApproved("confirmation for Dynamic DNS")
		p.setSwitch(p.ddns, !on)
	})
	p.general = newSettingsCard(
		amtSettingsRow(lang.L("Host name"), p.host.obj),
		amtSettingsRow(lang.L("Domain"), p.domain.obj),
		amtSettingsRow(lang.L("Respond to ping"), p.ping),
		amtSettingsRow(lang.L("Dynamic DNS"), p.ddns))

	p.address = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("DHCP"), lang.L("Static")), nil)
	p.address.OnChanged = p.openNetwork
	p.address.OnReselected = p.openNetwork
	p.ipText = designText("", 13, theme.ColorNameForeground, false)
	ip := amtSettingsRow(lang.L("IP address"), p.ipText)
	p.network = newSettingsCard(amtSettingsRow(lang.L("Address"), p.address), ip)
	p.ipRow = p.network.ref(1)

	p.kvm = p.newFeatureSwitch("KVM", func(on bool) amt.FeatureChange { return amt.FeatureChange{KVM: &on} })
	p.ider = p.newFeatureSwitch("media redirection", func(on bool) amt.FeatureChange { return amt.FeatureChange{IDER: &on} })
	p.sol = p.newFeatureSwitch("Serial over LAN", func(on bool) amt.FeatureChange { return amt.FeatureChange{SOL: &on} })
	p.consent = widgets.NewSegmented(widgets.SegmentedText,
		widgets.TextOptions(lang.L("None"), lang.L("KVM only"), lang.L("All")), nil)
	p.consent.OnChanged = func(i int) {
		policy := amt.ConsentPolicy(i)
		p.d.write(func() error { return m.sess.SetConsent(policy) }, true)
	}
	p.features = newSettingsCard(
		amtSettingsRow(lang.L("KVM"), p.kvm),
		amtSettingsRow(lang.L("Media redirection (IDE-R)"), p.ider),
		amtSettingsRow(lang.L("Serial over LAN (SOL)"), p.sol),
		amtSettingsRow(lang.L("User consent"), p.consent))
	p.kvmRow = p.features.ref(0)

	p.clockText = designText("", 13, style.ColorNameTextSecondary, false)
	p.sync = settingsButton(lang.L("Sync Now"), func() {
		p.d.write(m.sess.SyncClock, true)
	})
	p.tlsText = designText("", 13, style.ColorNameTextSecondary, false)
	p.change = settingsButton(lang.L("Change…"), p.openTLS)
	p.clockTLS = newSettingsCard(
		amtSettingsRow(lang.L("AMT clock"), container.New(&rowLayout{gap: 12, fill: -1}, p.clockText, p.sync)),
		amtSettingsRow(lang.L("TLS certificate"), container.New(&rowLayout{gap: 12, fill: -1}, p.tlsText, p.change)))
	p.clockRow, p.tlsRow = p.clockTLS.ref(0), p.clockTLS.ref(1)

	p.turnOff = widgets.NewPrimaryButton(lang.L("Turn Off…"), nil, p.confirmTurnOff)
	p.turnOff.Importance = widget.DangerImportance
	p.turnOff.Metrics = &widgets.ButtonMetrics{Height: 30, Radius: 7, PaddingH: 16, TextSize: 13}

	p.generalBox = settingsGroup(lang.L("General"), style.ColorNameTextSecondary, p.general.box)
	p.networkBox = settingsGroup(lang.L("Network"), style.ColorNameTextSecondary, p.network.box)
	p.featuresBox = settingsGroup(lang.L("Remote features"), style.ColorNameTextSecondary, p.features.box)
	p.clockBox = settingsGroup(lang.L("Clock and TLS"), style.ColorNameTextSecondary, p.clockTLS.box)
	danger := settingsGroup(lang.L("Danger zone"), style.ColorNameDangerZoneText, p.dangerCard())
	groups := container.New(layout.NewCustomPaddedVBoxLayout(18), p.generalBox, p.networkBox, p.featuresBox, p.clockBox, danger)
	shown := container.New(layout.NewCustomPaddedVBoxLayout(14), p.err.box, groups)

	p.pane = &managePane{body: shown, scroll: true, shown: m.settings.src.ensure, impl: p}
	render := func() {
		setShown(groups, m.settings.src.loaded())
		p.render()
	}
	p.pane.close = m.settings.src.subscribe(render)
	render()
	return p.pane
}

// window returns the window this view lives in, for its dialogs.
func (p *settingsPane) window() fyne.Window {
	if !p.compact && p.m.win != nil {
		return p.m.win.win
	}
	return p.m.parent
}

// render shows the device's values and locks the controls while a write or read runs.
func (p *settingsPane) render() {
	d, src := p.d, p.d.src
	switch {
	case d.writeErr != nil:
		p.err.set(d.writeErr)
	default:
		p.err.set(src.err)
	}
	if src.loaded() {
		p.applying = true
		p.show(src.val)
		p.applying = false
	}
	p.lock(d.locked())
	p.pane.changed()
}

// show puts s into the controls.
func (p *settingsPane) show(s amt.Settings) {
	g := s.General
	p.host.setValue(g.HostName)
	p.domain.setValue(g.DomainName)
	p.ping.SetChecked(g.PingResponse)
	p.ddns.SetChecked(g.DynamicDNS != amt.DDNSOff)

	n := s.Network
	setShown(p.networkBox, n.Available)
	p.address.SetSelected(map[bool]int{true: 0, false: 1}[n.DHCP])
	p.ipText.Text = ipSummary(n)
	p.ipText.Refresh()
	p.ipRow.show(n.IPAddress != "")
	p.network.sync()

	f := s.Features
	p.kvmRow.show(f.KVMAvailable)
	p.kvm.SetChecked(f.KVM)
	p.ider.SetChecked(f.IDER)
	p.sol.SetChecked(f.SOL)
	p.consent.SetSelected(int(s.Consent.Policy))
	p.features.sync()

	p.clockText.Text = clockOffsetText(s.Clock.Offset)
	p.clockText.Refresh()
	p.clockRow.show(s.Clock.Available)
	fp := ""
	if p.m.sess != nil {
		fp = p.m.sess.Config().Fingerprint
	}
	p.tlsText.Text = lang.L("SHA-256 {{.Fingerprint}}", map[string]any{"Fingerprint": shortFingerprint(fp)})
	p.tlsText.Refresh()
	p.tlsRow.show(fp != "")
	p.clockTLS.sync()
	setShown(p.clockBox, s.Clock.Available || fp != "")
}

// lock disables every control while on; the consent stays disabled where the device does not
// allow changing it.
func (p *settingsPane) lock(on bool) {
	for _, w := range []fyne.Disableable{p.host, p.domain, p.ping, p.ddns, p.address, p.kvm, p.ider, p.sol,
		p.sync, p.change, p.turnOff} {
		setEnabled(w, !on)
	}
	c := p.d.src.val.Consent
	setEnabled(p.consent, !on && c.Available && c.CanModify)
}

func setEnabled(w fyne.Disableable, on bool) {
	if on {
		w.Enable()
	} else {
		w.Disable()
	}
}

// newSwitch is a switch of the page (green while on) that calls changed for a change by the user.
func (p *settingsPane) newSwitch(changed func(bool)) *widgets.Switch {
	s := widgets.NewSwitch(nil)
	s.OnColor = theme.ColorNameSuccess
	s.OnChanged = func(on bool) {
		if !p.applying {
			changed(on)
		}
	}
	return s
}

// setSwitch puts s back to on without acting on it.
func (p *settingsPane) setSwitch(s *widgets.Switch, on bool) {
	p.applying = true
	s.SetChecked(on)
	p.applying = false
}

// newFeatureSwitch switches a redirection service: on is written at once, off needs the orange
// confirmation, which has no approved text yet.
func (p *settingsPane) newFeatureSwitch(what string, change func(bool) amt.FeatureChange) *widgets.Switch {
	var s *widgets.Switch
	s = p.newSwitch(func(on bool) {
		if !on {
			// TODO(design): the orange confirmation for turning a feature off has no approved text yet.
			notApproved("confirmation for turning off " + what)
			p.setSwitch(s, true)
			return
		}
		c := change(true)
		p.d.write(func() error { return p.m.sess.SetFeatures(c, amt.Confirmation{}) }, true)
	})
	return s
}

// Geometry of the settings rows (Settings.dc.html .row, .inp): 40 high, 16 px side padding, 13 px
// text, a 16 px gap; the inputs 220 wide with radius 6 and 4 x 8 px padding.
const (
	settingsRowHeight  float32 = 40
	settingsRowPad     float32 = 16
	settingsRowGap     float32 = 16
	settingsInputWidth float32 = 220
	settingsInputRound float32 = 6
)

// amtSettingsRow is a row of a settings card: the key on the left, control or value on the right.
func amtSettingsRow(key string, right fyne.CanvasObject) fyne.CanvasObject {
	return container.New(settingsRowLayout{}, designText(key, 13, style.ColorNameControlText, false), right)
}

// settingsRowLayout places the key on the left and the right object at its minimum width on
// the right (narrower if the row is too narrow), both in the vertical middle.
type settingsRowLayout struct{}

func (settingsRowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	ks, rs := objs[0].MinSize(), objs[1].MinSize()
	objs[0].Move(fyne.NewPos(settingsRowPad, (size.Height-ks.Height)/2))
	objs[0].Resize(ks)
	w := max(0, min(rs.Width, size.Width-2*settingsRowPad-ks.Width-settingsRowGap))
	objs[1].Move(fyne.NewPos(size.Width-settingsRowPad-w, (size.Height-rs.Height)/2))
	objs[1].Resize(fyne.NewSize(w, rs.Height))
}

func (settingsRowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	ks, rs := objs[0].MinSize(), objs[1].MinSize()
	return fyne.NewSize(2*settingsRowPad+ks.Width+settingsRowGap+min(rs.Width, 40), max(settingsRowHeight, rs.Height+8))
}

// settingsCard is a card of rows with hairlines between the visible ones (Settings.dc.html .card).
type settingsCard struct {
	rows    []fyne.CanvasObject
	visible []bool
	list    *fyne.Container
	box     fyne.CanvasObject
}

func newSettingsCard(rows ...fyne.CanvasObject) *settingsCard {
	c := &settingsCard{rows: rows, visible: make([]bool, len(rows)), list: container.New(layout.NewCustomPaddedVBoxLayout(0))}
	for i := range c.visible {
		c.visible[i] = true
	}
	c.box = widgets.NewPanel(style.ColorNameCard, style.ColorNameCardBorder, 12, c.list, 1, 1, 1, 1)
	c.sync()
	return c
}

// settingsRowRef switches one row of a card on or off.
type settingsRowRef struct {
	c *settingsCard
	i int
}

func (c *settingsCard) ref(i int) *settingsRowRef { return &settingsRowRef{c, i} }

func (r *settingsRowRef) show(on bool) { r.c.visible[r.i] = on }

// visibleRow reports whether row i is shown (tests).
func (r *settingsRowRef) visibleRow() bool { return r.c.visible[r.i] }

// sync lays out the visible rows with hairlines between them.
func (c *settingsCard) sync() {
	var objs []fyne.CanvasObject
	for i, r := range c.rows {
		if !c.visible[i] {
			continue
		}
		if len(objs) > 0 {
			objs = append(objs, hline(style.ColorNameCardBorder))
		}
		objs = append(objs, r)
	}
	c.list.Objects = objs
	c.list.Refresh()
}

// settingsGroup is a heading (12 px semibold, 4 px in) above a card.
func settingsGroup(title string, c fyne.ThemeColorName, card fyne.CanvasObject) fyne.CanvasObject {
	head := container.New(layout.NewCustomPaddedLayout(0, 0, 4, 0), designText(title, 12, c, true))
	return container.New(layout.NewCustomPaddedVBoxLayout(6), head, card)
}

// settingsButton is the white bordered button of a row ("Sync Now", "Change…"): radius 7,
// padding 4 x 12, 13 px regular.
func settingsButton(text string, tapped func()) *widgets.PrimaryButton {
	b := widgets.NewPrimaryButton(text, nil, tapped)
	b.Importance = widget.MediumImportance
	b.Metrics = &widgets.ButtonMetrics{Height: 26, Radius: 7, PaddingH: 12, TextSize: 13, Regular: true,
		Neutral: style.ColorNameField, NeutralBorder: style.ColorNameControlBorder}
	return b
}

// dangerCard is the card of the danger zone: light red with a red border, "Turn off AMT" in
// semibold red above the explanation, the full red button on the right.
func (p *settingsPane) dangerCard() fyne.CanvasObject {
	title := designText(lang.L("Turn off AMT"), 14, style.ColorNameDangerZoneText, true)
	text := richLine(textSeg(lang.L("Only the BIOS (MEBx) at the device can turn it back on."), style.ColorNameDangerZoneText, false))
	// the rich text has the theme's inner padding around its text; pull it to the title
	pad := theme.InnerPadding()
	col := container.New(layout.NewCustomPaddedVBoxLayout(3-pad), title,
		container.New(layout.NewCustomPaddedLayout(0, -pad, -pad, -pad), text))
	button := container.NewCenter(p.turnOff)
	row := container.NewBorder(nil, nil, nil, container.New(layout.NewCustomPaddedLayout(0, 0, 24, 0), button), col)
	return widgets.NewPanel(style.ColorNameDangerSurface, style.ColorNameDangerZoneBorder, 12, row, 14, 16, 14, 16)
}

// settingsEntry is the host name or domain field: Enter hands the text to onSubmit (which asks
// for the confirmation); Esc or leaving the field without Enter puts the device's value back.
type settingsEntry struct {
	widget.Entry
	value    string // the device's value
	held     bool   // Enter was pressed: the confirmation decides, leaving the field keeps the text
	focused  bool
	onSubmit func(string)
	obj      fyne.CanvasObject
}

func newSettingsEntry(submit func(string)) *settingsEntry {
	e := &settingsEntry{onSubmit: submit}
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(s string) {
		s = strings.TrimSpace(s)
		if s == e.value {
			e.revert()
			return
		}
		e.held = true
		e.onSubmit(s)
	}
	frame := style.BindRect(&canvas.Rectangle{CornerRadius: settingsInputRound, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder)
	e.obj = container.NewStack(frame, container.New(&vCenterLayout{}, container.NewThemeOverride(e, settingsInputTheme{})))
	return e
}

// setValue takes the device's value; the text follows unless the user is editing it.
func (e *settingsEntry) setValue(v string) {
	edited := e.focused && e.Text != e.value
	e.value = v
	if !edited && !e.held && e.Text != v {
		e.SetText(v)
	}
}

// release ends a confirmation: cancelled puts the device's value back.
func (e *settingsEntry) release(cancelled bool) {
	e.held = false
	if cancelled {
		e.revert()
	}
}

func (e *settingsEntry) revert() {
	if e.Text != e.value {
		e.SetText(e.value)
	}
}

func (e *settingsEntry) FocusGained() { e.focused = true; e.Entry.FocusGained() }

func (e *settingsEntry) FocusLost() {
	e.focused = false
	e.Entry.FocusLost()
	if !e.held {
		e.revert()
	}
}

func (e *settingsEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape {
		e.revert()
		if c := fyne.CurrentApp().Driver().CanvasForObject(e); c != nil {
			c.Unfocus()
		}
		return
	}
	e.Entry.TypedKey(k)
}

// MinSize is the width of the design's input.
func (e *settingsEntry) MinSize() fyne.Size {
	return fyne.NewSize(settingsInputWidth, e.Entry.MinSize().Height)
}

// settingsInputTheme draws the entry of a settings field without its own border, with 13 px
// text and a 4 px inner padding, so it fits the 40 px row.
type settingsInputTheme struct{ style.CardTheme }

func (t settingsInputTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInnerPadding:
		return 4
	case theme.SizeNameText:
		return 13
	}
	return t.CardTheme.Size(name)
}

// fqdn is host.domain, or host alone without a domain.
func fqdn(host, domain string) string {
	if domain == "" {
		return host
	}
	return host + "." + domain
}

// prefixLength returns the prefix length of a contiguous IPv4 mask, -1 for anything else.
func prefixLength(mask string) int {
	a, err := netip.ParseAddr(mask)
	if err != nil || !a.Is4() {
		return -1
	}
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	n := 0
	for v&0x80000000 != 0 {
		n++
		v <<= 1
	}
	if v != 0 {
		return -1
	}
	return n
}

// ipSummary is "192.0.2.10 / 24 · gateway 192.0.2.1"; a mask that is no prefix is shown as is.
func ipSummary(n amt.NetworkSettings) string {
	prefix := n.SubnetMask
	if l := prefixLength(n.SubnetMask); l >= 0 {
		prefix = strconv.Itoa(l)
	}
	args := map[string]any{"Address": n.IPAddress, "Prefix": prefix, "Gateway": n.Gateway}
	if n.Gateway == "" {
		return lang.L("{{.Address}} / {{.Prefix}}", args)
	}
	return lang.L("{{.Address}} / {{.Prefix}} · gateway {{.Gateway}}", args)
}

// clockOffsetText is "15 s behind" or "15 s ahead" (device clock against this computer), from
// 60 s on in whole minutes.
func clockOffsetText(off time.Duration) string {
	s := int(off.Round(time.Second) / time.Second)
	ahead := s > 0
	s = max(s, -s)
	if s >= 60 {
		args := map[string]any{"Minutes": s / 60}
		if ahead {
			return lang.L("{{.Minutes}} min ahead", args)
		}
		return lang.L("{{.Minutes}} min behind", args)
	}
	args := map[string]any{"Seconds": s}
	if ahead {
		return lang.L("{{.Seconds}} s ahead", args)
	}
	return lang.L("{{.Seconds}} s behind", args)
}

// shortFingerprint is the first two bytes, "…" and the last byte of a SHA-256 fingerprint:
// "4F:2A:…:9C".
func shortFingerprint(fp string) string {
	h := strings.ToUpper(strings.NewReplacer(":", "", " ", "").Replace(fp))
	if len(h) < 8 {
		return amt.FormatFingerprint(h)
	}
	return h[0:2] + ":" + h[2:4] + ":…:" + h[len(h)-2:]
}
