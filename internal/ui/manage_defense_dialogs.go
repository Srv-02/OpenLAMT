package ui

import (
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The dialogs "Add filter", "Add policy" and "Add agent watchdog" of the System Defense page
// (overall design 2026-11-23). The design's rate limit of a policy is left out: AMT has no such
// property on a policy (a rate limit is a filter profile). So is the hint "Lower number wins":
// AMT applies the policy with the higher precedence.

// limitRunes keeps e at most n characters (as the name fields of the device allow).
func limitRunes(e *dialogEntry, n int, then func()) {
	e.OnChanged = func(s string) {
		if r := []rune(s); len(r) > n {
			e.SetText(string(r[:n]))
			return
		}
		then()
	}
}

// formRow is a dialog row with a label on the left and control on the right.
func formRow(label string, control fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&rowLayout{gap: 8, fill: 1}, designText(label, 13, theme.ColorNameForeground, false), spacer(), control)
}

// dialogError shows err as the red line of a dialog.
func dialogError(pn *panel, e *manageError, err error) {
	e.label.SetText(describe(err))
	e.box.Show()
	pn.frame.Refresh()
}

// filterDialog is the shown "Add filter" dialog; its controls are exposed for tests.
type filterDialog struct {
	*panel
	name, ports, remote *dialogEntry
	kind, dir, verdict  *widgets.Segmented // verdict: Allow or Drop
	proto               *widgets.Dropdown
	count, event        *dialogCheck
	ipOnly              []fyne.CanvasObject // ports and remote address: hidden for Ethernet
	err                 *manageError
	busy                bool
}

// protocol choices of the dialog per filter type.
func ipProtocols() []string { return []string{"TCP", "UDP", "ICMP", lang.L("Any")} }

func etherProtocols() []string { return []string{"IP", "ARP"} }

// parsePorts reads "22" or "67-68"; empty is all ports (0, 0).
func parsePorts(s string) (int, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, true
	}
	a, b, rng := strings.Cut(s, "-")
	lo, err := strconv.Atoi(strings.TrimSpace(a))
	if err != nil {
		return 0, 0, false
	}
	hi := lo
	if rng {
		if hi, err = strconv.Atoi(strings.TrimSpace(b)); err != nil {
			return 0, 0, false
		}
	}
	return lo, hi, lo >= 1 && hi <= 65535 && lo <= hi
}

// parseRemote reads "192.0.2.0/24" or a single address; empty is any address.
func parseRemote(s string) (netip.Prefix, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Prefix{}, true
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a, a.BitLen()), true
}

// spec reads the form; ok is false while a field is not valid (the button stays disabled).
func (d *filterDialog) spec() (amt.FilterSpec, bool) {
	s := amt.FilterSpec{Name: trimmed(d.name), Direction: amt.Inbound, Drop: d.verdict.Selected() == 1,
		Count: d.count.Checked, Event: d.event.Checked}
	if d.dir.Selected() == 1 {
		s.Direction = amt.Outbound
	}
	proto := d.proto.SelectedIndex()
	switch d.kind.Selected() {
	case 2:
		s.Kind = amt.FilterEthernet
		s.Protocol = []int{amt.EtherTypeIP, amt.EtherTypeARP}[max(0, proto)]
		return s, s.Validate() == nil
	case 1:
		s.Kind = amt.FilterIPv6
	default:
		s.Kind = amt.FilterIPv4
	}
	switch proto {
	case 0:
		s.Protocol = amt.ProtocolTCP
	case 1:
		s.Protocol = amt.ProtocolUDP
	case 2:
		s.Protocol = amt.ProtocolICMP
		if s.Kind == amt.FilterIPv6 {
			s.Protocol = amt.ProtocolICMPv6
		}
	}
	if s.Protocol == amt.ProtocolTCP || s.Protocol == amt.ProtocolUDP {
		lo, hi, ok := parsePorts(d.ports.Text)
		if !ok {
			return s, false
		}
		s.PortStart, s.PortEnd = lo, hi
	}
	r, ok := parseRemote(d.remote.Text)
	if !ok {
		return s, false
	}
	s.Remote = r
	return s, s.Validate() == nil
}

// validate shows the fields of the type, enables Ports for TCP and UDP and the button while the
// form is valid.
func (d *filterDialog) validate() {
	ip := d.kind.Selected() != 2
	for _, o := range d.ipOnly {
		setShown(o, ip)
	}
	if p := d.proto.SelectedIndex(); ip && (p == 0 || p == 1) {
		d.ports.Enable()
	} else {
		d.ports.Disable()
	}
	_, ok := d.spec()
	setEnabled(d.panel.action, ok && !d.busy)
	d.panel.frame.Refresh()
}

// kindChanged puts the protocols of the new type into the dropdown.
func (d *filterDialog) kindChanged() {
	opts := ipProtocols()
	if d.kind.Selected() == 2 {
		opts = etherProtocols()
	}
	if strings.Join(opts, "\x00") != strings.Join(d.proto.Options, "\x00") {
		d.proto.Options = opts
		d.proto.Selected = opts[0]
		d.proto.Refresh()
	}
	d.validate()
}

// openAddFilter is "+ Add Filter".
func (p *defensePane) openAddFilter() {
	d := &filterDialog{err: newManageError()}
	d.panel = showPanel(p.window(), panelSpec{
		title: lang.L("Add filter"), action: lang.L("Add Filter"), cancel: lang.L("Cancel"), flush: true, keep: true,
		onAction: func() { d.submit(p) },
	}, func(pn *panel) []fyne.CanvasObject {
		d.panel = pn
		d.name, d.ports, d.remote = pn.newEntry(false), pn.newEntry(false), pn.newEntry(false)
		limitRunes(d.name, amt.MaxDefenseName, func() { d.err.set(nil); d.validate() })
		for _, e := range []*dialogEntry{d.ports, d.remote} {
			e.OnChanged = func(string) { d.err.set(nil); d.validate() }
		}
		d.kind = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions("IPv4", "IPv6", lang.L("Ethernet")), func(int) { d.kindChanged() })
		d.dir = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("Inbound"), lang.L("Outbound")), func(int) { d.validate() })
		d.verdict = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("Allow"), lang.L("Drop")), func(int) { d.validate() })
		d.proto = widgets.NewDropdown(ipProtocols())
		d.proto.OnEscape = pn.Dismiss
		d.proto.OnChanged = func(string) { d.validate() }
		d.count = pn.newCheck(lang.L("Count matches"), false)
		d.event = pn.newCheck(lang.L("Write an event log entry on match"), false)
		d.count.OnChanged = func(bool) { d.validate() }
		d.event.OnChanged = func(bool) { d.validate() }
		portsField := formField(lang.L("Ports"), framedEntry(d.ports))
		remote := formField(lang.L("Remote address"), framedEntry(d.remote))
		hint := formHint(lang.L("Empty: any address."))
		d.ipOnly = []fyne.CanvasObject{portsField, remote, hint}
		pn.focus = d.name
		return []fyne.CanvasObject{
			formField(lang.L("Name"), framedEntry(d.name)),
			container.NewGridWithColumns(2, formField(lang.L("Type"), container.NewHBox(d.kind)), formField(lang.L("Direction"), container.NewHBox(d.dir))),
			container.NewGridWithColumns(2, formField(lang.L("Protocol"), d.proto), portsField),
			remote, hint,
			formField(lang.L("Action"), container.NewHBox(d.verdict)),
			container.New(layout.NewCustomPaddedVBoxLayout(6), d.count, d.event),
			d.err.box,
		}
	})
	d.validate()
	p.filterDlg = d
}

// submit sends the filter; a refusal stays in the dialog as a red line.
func (d *filterDialog) submit(p *defensePane) {
	s, ok := d.spec()
	if !ok || d.busy {
		return
	}
	d.busy = true
	d.validate()
	manageWrite(p.m, p.d.sd, func() error { return p.m.sess.AddDefenseFilter(s) }, func(err error) {
		d.busy = false
		if err != nil {
			dialogError(d.panel, d.err, err)
			d.validate()
			return
		}
		d.close()
	}, nil)
}

// policyDialog is the shown "Add policy" dialog.
type policyDialog struct {
	*panel
	name, precedence *dialogEntry
	filters          []*dialogCheck
	filterIDs        []string
	otherwise        *widgets.Segmented
	err              *manageError
	busy             bool
}

func (d *policyDialog) spec() (amt.PolicySpec, bool) {
	n, err := strconv.ParseUint(trimmed(d.precedence), 10, 32)
	if err != nil {
		return amt.PolicySpec{}, false
	}
	s := amt.PolicySpec{Name: trimmed(d.name), Precedence: int(n), Drop: d.otherwise.Selected() == 1}
	for i, c := range d.filters {
		if c.Checked {
			s.Filters = append(s.Filters, d.filterIDs[i])
		}
	}
	return s, s.Validate() == nil
}

func (d *policyDialog) validate() {
	_, ok := d.spec()
	setEnabled(d.panel.action, ok && !d.busy)
	d.panel.frame.Refresh()
}

// openAddPolicy is "+ Add Policy".
func (p *defensePane) openAddPolicy() {
	sd := p.d.sd.val
	d := &policyDialog{err: newManageError()}
	d.panel = showPanel(p.window(), panelSpec{
		title: lang.L("Add policy"), action: lang.L("Add Policy"), cancel: lang.L("Cancel"), flush: true, keep: true,
		onAction: func() { d.submit(p) },
	}, func(pn *panel) []fyne.CanvasObject {
		d.panel = pn
		d.name, d.precedence = pn.newEntry(false), pn.newEntry(false)
		limitRunes(d.name, amt.MaxDefenseName, func() { d.err.set(nil); d.validate() })
		d.precedence.SetText("1")
		d.precedence.OnChanged = func(string) { d.err.set(nil); d.validate() }
		d.otherwise = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("Allow"), lang.L("Drop")), func(int) { d.validate() })
		d.otherwise.SetSelected(1)
		var checks []fyne.CanvasObject
		for _, f := range sd.Filters {
			c := pn.newCheck(f.Name, false)
			c.OnChanged = func(bool) { d.validate() }
			d.filters = append(d.filters, c)
			d.filterIDs = append(d.filterIDs, f.ID)
			checks = append(checks, c)
		}
		pn.focus = d.name
		objs := []fyne.CanvasObject{container.NewGridWithColumns(2,
			formField(lang.L("Name"), framedEntry(d.name)), formField(lang.L("Precedence"), framedEntry(d.precedence)))}
		if len(checks) > 0 {
			objs = append(objs, formSubhead(lang.L("Filters")), container.NewGridWithColumns(2, checks...))
		}
		return append(objs, formField(lang.L("Traffic no filter matches"), container.NewHBox(d.otherwise)), d.err.box)
	})
	d.validate()
	p.policyDlg = d
}

func (d *policyDialog) submit(p *defensePane) {
	s, ok := d.spec()
	if !ok || d.busy {
		return
	}
	d.busy = true
	d.validate()
	manageWrite(p.m, p.d.sd, func() error { return p.m.sess.AddDefensePolicy(s) }, func(err error) {
		d.busy = false
		if err != nil {
			dialogError(d.panel, d.err, err)
			d.validate()
			return
		}
		d.close()
	}, nil)
}

// watchdogDialog is the shown "Add agent watchdog" dialog.
type watchdogDialog struct {
	*panel
	name, id, startup, heartbeat *dialogEntry
	generate                     *escButton
	stopEvent, activate          *dialogCheck
	startEvent, deactivate       *dialogCheck
	policy                       *widgets.Dropdown
	policyIDs                    []string
	err                          *manageError
	busy                         bool
}

func parseSeconds(e *dialogEntry) (int, bool) {
	n, err := strconv.Atoi(trimmed(e))
	return n, err == nil && n >= 1 && n <= amt.MaxWatchdogSeconds
}

func (d *watchdogDialog) spec() (amt.WatchdogSpec, bool) {
	start, ok1 := parseSeconds(d.startup)
	beat, ok2 := parseSeconds(d.heartbeat)
	if !ok1 || !ok2 || utf8.RuneCountInString(trimmed(d.name)) > amt.MaxAgentName {
		return amt.WatchdogSpec{}, false
	}
	s := amt.WatchdogSpec{Name: trimmed(d.name), ID: trimmed(d.id), Startup: start, Heartbeat: beat,
		OnStop: amt.WatchdogActions{Event: d.stopEvent.Checked}, OnStart: amt.WatchdogActions{Event: d.startEvent.Checked}}
	if d.activate.Checked {
		i := d.policy.SelectedIndex()
		if i < 0 || i >= len(d.policyIDs) {
			return s, false
		}
		s.Policy = d.policyIDs[i]
		s.OnStop.Policy = amt.PolicyActivate
		if d.deactivate.Checked {
			s.OnStart.Policy = amt.PolicyDeactivate
		}
	}
	return s, s.Validate() == nil
}

func (d *watchdogDialog) validate() {
	can := len(d.policyIDs) > 0
	setEnabled(d.activate, can)
	if !can {
		d.activate.SetChecked(false)
	}
	setEnabled(d.deactivate, d.activate.Checked)
	_, ok := d.spec()
	setEnabled(d.panel.action, ok && !d.busy)
	d.panel.frame.Refresh()
}

// openAddWatchdog is "+ Add Watchdog". The policy dropdown starts on the policy the other
// watchdogs switch (all share one), else on the first one.
func (p *defensePane) openAddWatchdog() {
	sd, wd := p.d.sd.val, p.d.wd.val
	d := &watchdogDialog{err: newManageError()}
	d.panel = showPanel(p.window(), panelSpec{
		title: lang.L("Add agent watchdog"), action: lang.L("Add Watchdog"), cancel: lang.L("Cancel"), flush: true, keep: true,
		onAction: func() { d.submit(p) },
	}, func(pn *panel) []fyne.CanvasObject {
		d.panel = pn
		d.name, d.id, d.startup, d.heartbeat = pn.newEntry(false), pn.newEntry(false), pn.newEntry(false), pn.newEntry(false)
		d.id.SetText(amt.NewAgentID())
		d.startup.SetText("120")
		d.heartbeat.SetText("30")
		limitRunes(d.name, amt.MaxAgentName, func() { d.err.set(nil); d.validate() })
		for _, e := range []*dialogEntry{d.id, d.startup, d.heartbeat} {
			e.OnChanged = func(string) { d.err.set(nil); d.validate() }
		}
		d.generate = pn.newButton(lang.L("Generate"), func() { d.id.SetText(amt.NewAgentID()) })
		var names []string
		sel := 0
		for i, pol := range sd.Policies {
			names = append(names, pol.Name)
			d.policyIDs = append(d.policyIDs, pol.ID)
			if pol.ID == wd.Policy {
				sel = i
			}
		}
		d.policy = widgets.NewDropdown(names)
		d.policy.OnEscape = pn.Dismiss
		d.policy.SetSelectedIndex(sel)
		d.policy.OnChanged = func(string) { d.validate() }
		d.stopEvent = pn.newCheck(lang.L("Write an event log entry"), true)
		d.activate = pn.newCheck(lang.L("Activate policy"), len(names) > 0)
		d.startEvent = pn.newCheck(lang.L("Write an event log entry"), true)
		d.deactivate = pn.newCheck(lang.L("Turn the policy off again"), true)
		for _, c := range []*dialogCheck{d.stopEvent, d.activate, d.startEvent, d.deactivate} {
			c.OnChanged = func(bool) { d.validate() }
		}
		seconds := func(e *dialogEntry) fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil, designText("s", 13, style.ColorNameTextSecondary, false), framedEntry(e))
		}
		pn.focus = d.name
		return []fyne.CanvasObject{
			formField(lang.L("Agent name"), framedEntry(d.name)),
			formField(lang.L("Agent ID"), container.NewBorder(nil, nil, nil, d.generate, framedEntry(d.id))),
			formHint(lang.L("The agent in the operating system must use this ID.")),
			container.NewGridWithColumns(2, formField(lang.L("Startup timeout"), seconds(d.startup)), formField(lang.L("Heartbeat timeout"), seconds(d.heartbeat))),
			formSubhead(lang.L("When heartbeats stop")),
			container.New(layout.NewCustomPaddedVBoxLayout(6), d.stopEvent,
				container.New(&rowLayout{gap: 8, fill: 2}, d.activate, d.policy, spacer())),
			formSubhead(lang.L("When heartbeats start again")),
			container.New(layout.NewCustomPaddedVBoxLayout(6), d.startEvent, d.deactivate),
			d.err.box,
		}
	})
	d.validate()
	p.watchdogDlg = d
}

// submit sends the watchdog. One that activates a policy which drops traffic asks first with
// the orange warning of "Activate…" (OpDefenseDrop); the dialog stays open below it.
func (d *watchdogDialog) submit(p *defensePane) {
	s, ok := d.spec()
	if !ok || d.busy {
		return
	}
	send := func(c amt.Confirmation) {
		d.busy = true
		d.validate()
		manageWrite(p.m, p.d.wd, func() error { return p.m.sess.AddWatchdog(s, c) }, func(err error) {
			d.busy = false
			if err != nil {
				dialogError(d.panel, d.err, err)
				d.validate()
				return
			}
			d.close()
		}, nil)
	}
	sd := p.d.sd.val
	if s.OnStop.Policy != amt.PolicyActivate || !sd.Drops(s.Policy) {
		send(amt.Confirmation{})
		return
	}
	pol, _ := sd.Policy(s.Policy)
	p.riskDlg = showOrangeConfirm(p.window(), orangeSpec{
		op: amt.OpDefenseDrop, host: p.m.confirmHost(), device: p.m.confirmDevice(),
		title:     lang.L("Let the watchdog activate “{{.Policy}}”?", map[string]any{"Policy": pol.Name}),
		warning:   dropWarning(p.m.name),
		button:    lang.L("Add Watchdog"),
		onConfirm: send,
	})
}
