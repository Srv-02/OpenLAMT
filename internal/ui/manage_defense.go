package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The System Defense page of device management (overall design 2026-11-23, issues #28 and #19,
// V4 and V21): three lists, Policies, Filters and Agent watchdog, each with "+ Add …".
//
// AMT cannot change a filter, a policy or a watchdog (no Put); the design's Edit dialogs are
// therefore left out and each row has "Delete…" (simple), as the wake alarms of the Power page.
// A policy row has "Activate…", the active one "Turn Off…". Activating a policy that drops
// traffic is orange (V21): the filters act on the operating system's traffic only, Intel AMT
// stays reachable. A card of a class the device does not offer is hidden; with neither one the
// page says so.

// defenseData is the data of the page for one device, shared by the sidebar and the window.
type defenseData struct {
	sd *manageSource[amt.SystemDefense]
	wd *manageSource[amt.Watchdogs]
}

// notOffered maps session.ErrUnsupported (a device without the capability) to "not offered".
func notOffered[T any](read func() (T, error)) func() (T, error) {
	return func() (T, error) {
		v, err := read()
		if errors.Is(err, session.ErrUnsupported) {
			var zero T
			return zero, nil
		}
		return v, err
	}
}

func (m *manager) defenseData() *defenseData {
	if m.defense == nil {
		m.defense = &defenseData{
			sd: newManageSource(m, notOffered(m.sess.SystemDefense)),
			wd: newManageSource(m, notOffered(m.sess.Watchdogs)),
		}
	}
	return m.defense
}

func (d *defenseData) ensure() {
	d.sd.ensure()
	d.wd.ensure()
}

func (d *defenseData) reload() {
	d.sd.reload()
	d.wd.reload()
}

func (d *defenseData) loading() bool { return d.sd.loading || d.wd.loading }

func init() {
	registerManagePage(managePageDef{section: sectionMore, order: 30, icon: icons.ShieldOK, build: (*manager).defensePane,
		names: func() (string, string) { return lang.L("System Defense"), lang.L("System Defense") }})
}

// defensePane is one view of the System Defense page.
type defensePane struct {
	m       *manager
	compact bool
	d       *defenseData
	pane    *managePane
	err     *manageError
	refresh *widgets.PrimaryButton
	active  *canvas.Text // window head: "· Active policy: …"

	policies, filters, watchdogs         *listTable
	addPolicy, addFilter, addWatchdog    *widgets.PrimaryButton
	policySect, filterSect, watchdogSect []fyne.CanvasObject // head and table of each list
	watchdogNote, unavailable            fyne.CanvasObject

	// the dialogs last opened from this view (tests)
	filterDlg   *filterDialog
	policyDlg   *policyDialog
	watchdogDlg *watchdogDialog
	deleteDlg   *widget.PopUp
	riskDlg     *riskDialog
}

// defensePane builds the System Defense page.
func (m *manager) defensePane(compact bool) *managePane {
	d := m.defenseData()
	p := &defensePane{m: m, compact: compact, d: d, err: newManageError()}
	p.addPolicy = p.headButton(lang.L("+ Add Policy"), p.openAddPolicy)
	p.addFilter = p.headButton(lang.L("+ Add Filter"), p.openAddFilter)
	p.addWatchdog = p.headButton(lang.L("+ Add Watchdog"), p.openAddWatchdog)
	p.refresh = newManageButton(lang.L("Refresh"), d.reload)

	p.policies = newListTable(compact, p.policyColumns(), &listAction{width: 196, compactWidth: 160})
	p.filters = newListTable(compact, p.filterColumns(), &listAction{width: 100, compactWidth: 74})
	p.watchdogs = newListTable(compact, p.watchdogColumns(), &listAction{width: 100, compactWidth: 74})

	note := widget.NewRichText(cellSeg(lang.L("An agent in the operating system sends heartbeats to Intel AMT. OpenLAMT only sets up the watchdog; it runs no agent."),
		style.ColorNameTextSecondary, style.SizeNameSmallText, false))
	note.Wrapping = fyne.TextWrapWord
	ip := theme.InnerPadding()
	p.watchdogNote = container.New(layout.NewCustomPaddedLayout(-4-ip, 0, -ip, 0), note)
	p.unavailable = newEmptyCard(lang.L("Not available on this device"),
		lang.L("{{.Name}} offers neither System Defense nor the agent watchdog. Intel AMT has them up to version 11.", map[string]any{"Name": m.name}))
	p.unavailable.Hide()

	p.policySect = []fyne.CanvasObject{p.sectionHead(lang.L("Policies"), policyHelp(), p.addPolicy), p.policies}
	p.filterSect = []fyne.CanvasObject{p.sectionHead(lang.L("Filters"), nil, p.addFilter), p.filters}
	p.watchdogSect = []fyne.CanvasObject{p.sectionHead(lang.L("Agent watchdog"), watchdogHelp(), p.addWatchdog), p.watchdogs, p.watchdogNote}

	objs := []fyne.CanvasObject{p.err.box, p.unavailable}
	objs = append(append(append(objs, p.policySect...), p.filterSect...), p.watchdogSect...)
	if compact {
		objs = append([]fyne.CanvasObject{container.New(&rowLayout{gap: 8, fill: 0}, spacer(), p.refresh)}, objs...)
	}
	p.pane = &managePane{body: container.New(layout.NewCustomPaddedVBoxLayout(12), objs...), scroll: true,
		shown: d.ensure, impl: p}
	if !compact {
		p.pane.tools = p.refresh
		p.active = designText("", 14, style.ColorNameTextSecondary, false)
		p.pane.info = p.active
	}
	for _, t := range []*listTable{p.policies, p.filters, p.watchdogs} {
		t.onHeight = p.pane.changed
	}
	render := func() { p.render() }
	cancel := []func(){d.sd.subscribe(render), d.wd.subscribe(render)}
	p.pane.close = func() {
		for _, c := range cancel {
			c()
		}
	}
	render()
	return p.pane
}

func (p *defensePane) headButton(text string, tapped func()) *widgets.PrimaryButton {
	b := newManageButton(text, tapped)
	if p.compact {
		b.Metrics.Height, b.Metrics.PaddingH, b.Metrics.TextSize = 26, 10, 12
	}
	return b
}

// sectionHead is the line above a list: its title, an optional ⓘ and the add button on the right.
func (p *defensePane) sectionHead(title string, help *widgets.InfoButton, add fyne.CanvasObject) fyne.CanvasObject {
	var left fyne.CanvasObject = designText(title, 12, style.ColorNameTextSecondary, true)
	if help != nil {
		left = container.NewHBox(left, help)
	}
	return container.New(&rowLayout{gap: 8, fill: 1}, left, spacer(), add)
}

func policyHelp() *widgets.InfoButton {
	return widgets.NewInfoButton(widgets.HelpContent{
		Title:   lang.L("Policies"),
		What:    lang.L("A policy bundles filters and decides what happens to the traffic no filter matches. The active policy filters the network traffic of the operating system; Intel AMT’s own traffic is never filtered."),
		When:    lang.L("Activate a policy to cut a computer off the network, for example after an infection, and keep remote access through Intel AMT."),
		Wrong:   lang.L("A policy that drops traffic takes the network away from the operating system and its users."),
		Recover: lang.L("Turn the policy off on this page. Intel AMT stays reachable while a policy is active."),
	})
}

func watchdogHelp() *widgets.InfoButton {
	return widgets.NewInfoButton(widgets.HelpContent{
		Title:   lang.L("Agent watchdog"),
		What:    lang.L("Intel AMT watches an agent in the operating system that sends heartbeats. When they stop, AMT writes an event log entry and can activate a policy."),
		When:    lang.L("Use it with an agent you run in the operating system, for example a backup or anti-virus agent."),
		Wrong:   lang.L("When the agent stops, the policy can cut the operating system off the network."),
		Recover: lang.L("Turn the policy off on this page, or delete the watchdog."),
	})
}

// Columns: the window shows all of the design, the 400 px sidebar the name, the state and the
// buttons (policies, watchdogs) or the name, match and action (filters).
func (p *defensePane) policyColumns() []listColumn {
	if p.compact {
		return []listColumn{{title: lang.L("Policy")}, {title: lang.L("State"), compactWidth: 58, compactGrow: 70}}
	}
	return []listColumn{{title: lang.L("Policy")}, {title: lang.L("Precedence"), width: 104}, {title: lang.L("Otherwise"), width: 120},
		{title: lang.L("Filters"), width: 74}, {title: lang.L("State"), width: 84}}
}

func (p *defensePane) filterColumns() []listColumn {
	if p.compact {
		return []listColumn{{title: lang.L("Filter")}, {title: lang.L("Match"), compactWidth: 110, compactGrow: 130}, {title: lang.L("Action"), compactWidth: 62, compactGrow: 80}}
	}
	return []listColumn{{title: lang.L("Filter")}, {title: lang.L("Type"), width: 84}, {title: lang.L("Direction"), width: 100},
		{title: lang.L("Match"), width: 210}, {title: lang.L("Action"), width: 150}}
}

func (p *defensePane) watchdogColumns() []listColumn {
	if p.compact {
		return []listColumn{{title: lang.L("Agent")}, {title: lang.L("State"), compactWidth: 74, compactGrow: 90}}
	}
	return []listColumn{{title: lang.L("Agent")}, {title: lang.L("Startup"), width: 84}, {title: lang.L("Heartbeat"), width: 94},
		{title: lang.L("State"), width: 100}, {title: lang.L("When heartbeats stop"), width: 210}}
}

// render shows the sources.
func (p *defensePane) render() {
	d := p.d
	sd, wd := d.sd.val, d.wd.val
	sdHidden := d.sd.loaded() && !sd.Offered
	wdHidden := d.wd.loaded() && !wd.Offered
	for _, o := range p.policySect {
		setShown(o, !sdHidden)
	}
	for _, o := range p.filterSect {
		setShown(o, !sdHidden)
	}
	for _, o := range p.watchdogSect {
		setShown(o, !wdHidden)
	}
	setShown(p.unavailable, sdHidden && wdHidden)

	p.policies.setRows(p.policyRows(sd))
	p.filters.setRows(p.filterRows(sd))
	p.watchdogs.setRows(p.watchdogRows(wd, sd))
	setEnabled(p.addPolicy, d.sd.loaded() && sd.Offered && !d.sd.loading)
	setEnabled(p.addFilter, d.sd.loaded() && sd.Offered && !d.sd.loading)
	setEnabled(p.addWatchdog, d.wd.loaded() && wd.Offered && !d.loading())
	setEnabled(p.refresh, !d.loading())

	err := d.sd.err
	if err == nil {
		err = sd.LinkError() // a failed switch left more than one link (issue #167)
	}
	if err == nil {
		err = d.wd.err
	}
	p.err.set(err)
	if p.active != nil {
		p.active.Text = p.activeText()
		p.active.Refresh()
	}
	p.pane.changed()
}

// activeText is the window head after the title: "· Active policy: Allow all", "None" or "N/A"
// when the account may not read it; "" while unknown or without System Defense.
func (p *defensePane) activeText() string {
	d := p.d
	switch {
	case d.sd.loaded() && !d.sd.val.Offered:
		return ""
	case !d.sd.loaded() && errors.Is(d.sd.err, amt.ErrAccessDenied):
		return "· " + lang.L("Active policy: {{.Policy}}", map[string]any{"Policy": lang.L("N/A")})
	case !d.sd.loaded():
		return ""
	}
	return "· " + lang.L("Active policy: {{.Policy}}", map[string]any{"Policy": activeName(d.sd.val)})
}

// activeName is the name of the active policy or "None"; with more than one linked policy
// their names (the page names the problem above the lists).
func activeName(sd amt.SystemDefense) string {
	var links *amt.DefenseLinksError
	if errors.As(sd.LinkError(), &links) {
		return strings.Join(links.Policies, ", ")
	}
	if pol, ok := sd.Policy(sd.Active); ok && sd.Active != "" {
		return pol.Name
	}
	return lang.L("None")
}

func (p *defensePane) cellSize() fyne.ThemeSizeName {
	if p.compact {
		return style.SizeNameSmallText
	}
	return theme.SizeNameText
}

func (p *defensePane) cell(s string) []widget.RichTextSegment {
	return []widget.RichTextSegment{cellSeg(s, theme.ColorNameForeground, p.cellSize(), false)}
}

func (p *defensePane) colored(s string, c fyne.ThemeColorName) []widget.RichTextSegment {
	return []widget.RichTextSegment{cellSeg(s, c, p.cellSize(), false)}
}

// otherwiseText is the Otherwise column: what happens to the traffic no filter matches.
func otherwiseText(pol amt.DefensePolicy) string {
	switch {
	case pol.DropIn && pol.DropOut:
		return lang.L("Drop")
	case pol.DropIn:
		return lang.L("Drop inbound")
	case pol.DropOut:
		return lang.L("Drop outbound")
	}
	return lang.L("Allow")
}

func (p *defensePane) policyRows(sd amt.SystemDefense) []listRow {
	rows := make([]listRow, 0, len(sd.Policies))
	for _, pol := range sd.Policies {
		state := p.colored(lang.L("Off"), style.ColorNameTextSecondary)
		lead := newRowButton(lang.L("Activate…"), p.compact, func() { p.activate(pol) })
		if pol.Active {
			state = p.colored(lang.L("Active"), theme.ColorNameSuccess)
			lead = newRowButton(lang.L("Turn Off…"), p.compact, p.turnOff)
		}
		cells := [][]widget.RichTextSegment{p.cell(pol.Name)}
		if !p.compact {
			cells = append(cells, p.cell(strconv.Itoa(pol.Precedence)), p.cell(otherwiseText(pol)), p.cell(strconv.Itoa(len(pol.Filters))))
		}
		cells = append(cells, state)
		rows = append(rows, listRow{cells: cells, lead: lead,
			action: newRowButton(lang.L("Delete…"), p.compact, func() { p.confirmDeletePolicy(pol) })})
	}
	return rows
}

// filterKindText is the Type column.
func filterKindText(k amt.FilterKind) string {
	switch k {
	case amt.FilterIPv4:
		return "IPv4"
	case amt.FilterIPv6:
		return "IPv6"
	case amt.FilterEthernet:
		return lang.L("Ethernet")
	}
	return "IP"
}

func directionText(dir amt.FilterDirection) string {
	if dir == amt.Inbound {
		return lang.L("Inbound")
	}
	return lang.L("Outbound")
}

// protocolText is the protocol of a filter in the Match column.
func protocolText(f amt.DefenseFilter) string {
	if f.Kind == amt.FilterEthernet {
		switch f.Protocol {
		case amt.EtherTypeIP:
			return lang.L("IP packets")
		case amt.EtherTypeARP:
			return lang.L("ARP packets")
		}
		return fmt.Sprintf("EtherType 0x%04X", f.Protocol)
	}
	switch f.Protocol {
	case amt.ProtocolAny:
		return lang.L("Any")
	case amt.ProtocolTCP:
		return "TCP"
	case amt.ProtocolUDP:
		return "UDP"
	case amt.ProtocolICMP:
		return "ICMP"
	case amt.ProtocolICMPv6:
		return "ICMPv6"
	}
	return lang.L("Protocol {{.Number}}", map[string]any{"Number": f.Protocol})
}

// matchText is the Match column: "UDP 67-68", "UDP 53 to 192.0.2.1", "TCP 22 from 192.0.2.0/24".
func matchText(f amt.DefenseFilter) string {
	s := protocolText(f)
	switch {
	case f.PortStart == 0 && f.PortEnd == 0:
	case f.PortStart == f.PortEnd:
		s += " " + strconv.Itoa(f.PortStart)
	default:
		s += " " + strconv.Itoa(f.PortStart) + "-" + strconv.Itoa(f.PortEnd)
	}
	if !f.Remote.IsValid() {
		return s
	}
	addr := f.Remote.String()
	if f.Remote.IsSingleIP() {
		addr = f.Remote.Addr().String()
	}
	if f.Direction == amt.Inbound {
		return lang.L("{{.Match}} from {{.Address}}", map[string]any{"Match": s, "Address": addr})
	}
	return lang.L("{{.Match}} to {{.Address}}", map[string]any{"Match": s, "Address": addr})
}

// actionText is the Action column with the count of the active policy's statistics.
func actionText(f amt.DefenseFilter) string {
	s := lang.L("Allow")
	switch {
	case f.RateLimit > 0:
		s = lang.L("Rate limit {{.Count}}/s", map[string]any{"Count": f.RateLimit})
	case f.Drop:
		s = lang.L("Drop")
	}
	if f.HasMatches {
		if f.Matches == 1 {
			return s + ", " + lang.L("1 match")
		}
		return s + ", " + lang.L("{{.Count}} matches", map[string]any{"Count": f.Matches})
	}
	return s
}

func (p *defensePane) filterRows(sd amt.SystemDefense) []listRow {
	rows := make([]listRow, 0, len(sd.Filters))
	for _, f := range sd.Filters {
		cells := [][]widget.RichTextSegment{p.cell(f.Name)}
		if !p.compact {
			cells = append(cells, p.cell(filterKindText(f.Kind)), p.cell(directionText(f.Direction)))
		}
		cells = append(cells, p.cell(matchText(f)), p.cell(actionText(f)))
		rows = append(rows, listRow{cells: cells,
			action: newRowButton(lang.L("Delete…"), p.compact, func() { p.confirmDeleteFilter(f, sd) })})
	}
	return rows
}

// watchdogStateText is the State column and its color.
func watchdogStateText(s amt.WatchdogState) (string, fyne.ThemeColorName) {
	switch s {
	case amt.WatchdogRunning:
		return lang.L("Running"), theme.ColorNameSuccess
	case amt.WatchdogExpired:
		return lang.L("Expired"), theme.ColorNameError
	case amt.WatchdogStopped:
		return lang.L("Stopped"), theme.ColorNameForeground
	case amt.WatchdogSuspended:
		return lang.L("Suspended"), style.ColorNameTextSecondary
	}
	return lang.L("Not started"), style.ColorNameTextSecondary
}

// stopText is the column "When heartbeats stop": "Log event, activate Isolate".
func stopText(a amt.WatchdogActions, policy string) string {
	n := map[string]any{"Policy": policy}
	switch {
	case a.Event && a.Policy == amt.PolicyActivate:
		return lang.L("Log event, activate {{.Policy}}", n)
	case a.Event && a.Policy == amt.PolicyDeactivate:
		return lang.L("Log event, turn off {{.Policy}}", n)
	case a.Event:
		return lang.L("Log event")
	case a.Policy == amt.PolicyActivate:
		return lang.L("Activate {{.Policy}}", n)
	case a.Policy == amt.PolicyDeactivate:
		return lang.L("Turn off {{.Policy}}", n)
	}
	return "–"
}

// secondsText is "120 s".
func secondsText(n int) string { return strconv.Itoa(n) + " s" }

func (p *defensePane) watchdogRows(wd amt.Watchdogs, sd amt.SystemDefense) []listRow {
	policy := lang.L("the policy")
	if pol, ok := sd.Policy(wd.Policy); ok && wd.Policy != "" {
		policy = pol.Name
	}
	rows := make([]listRow, 0, len(wd.List))
	for _, w := range wd.List {
		name := w.Name
		if name == "" {
			name = w.ID
		}
		st, c := watchdogStateText(w.State)
		cells := [][]widget.RichTextSegment{p.cell(name)}
		if !p.compact {
			cells = append(cells, p.cell(secondsText(w.Startup)), p.cell(secondsText(w.Heartbeat)))
		}
		cells = append(cells, p.colored(st, c))
		if !p.compact {
			cells = append(cells, p.cell(stopText(w.OnStop, policy)))
		}
		rows = append(rows, listRow{cells: cells,
			action: newRowButton(lang.L("Delete…"), p.compact, func() { p.confirmDeleteWatchdog(w, name) })})
	}
	return rows
}

// window is the window the dialogs of this view open on.
func (p *defensePane) window() fyne.Window {
	if !p.compact && p.m.win != nil {
		return p.m.win.win
	}
	return p.m.parent
}

// write runs write off the Fyne thread, reads the sources again and shows an error.
func (p *defensePane) write(src interface{ reload() }, write func() error) {
	manageWrite(p.m, src, write, func(err error) {
		if err != nil {
			showError(p.window(), err)
		}
	}, nil)
}

// activate is "Activate…": a policy that drops traffic asks first (orange, V21), one that
// drops nothing is activated at once.
func (p *defensePane) activate(pol amt.DefensePolicy) {
	sd := p.d.sd.val
	apply := func(c amt.Confirmation) {
		p.write(p.d.sd, func() error { return p.m.sess.ActivateDefensePolicy(pol.ID, c) })
	}
	if !sd.Drops(pol.ID) {
		apply(amt.Confirmation{})
		return
	}
	p.riskDlg = showOrangeConfirm(p.window(), orangeSpec{
		op: amt.OpDefenseDrop, host: p.m.confirmHost(), device: p.m.confirmDevice(),
		title:    lang.L("Activate “{{.Policy}}” on {{.Name}}?", map[string]any{"Policy": pol.Name, "Name": p.m.name}),
		warning:  dropWarning(p.m.name),
		oldValue: lang.L("Active policy: {{.Policy}}", map[string]any{"Policy": activeName(sd)}), newValue: pol.Name,
		button:    lang.L("Activate Policy"),
		onConfirm: apply,
	})
}

// dropWarning is the orange warning of a policy that drops traffic.
func dropWarning(name string) string {
	return lang.L("The operating system of {{.Name}} loses all network traffic that the policy drops. Intel AMT stays reachable, so you can turn the policy off here.",
		map[string]any{"Name": name})
}

// turnOff is "Turn Off…" of the active policy: no confirmation.
func (p *defensePane) turnOff() {
	p.write(p.d.sd, p.m.sess.DeactivateDefensePolicy)
}

// confirmDeletePolicy asks before a policy is deleted (simple, OpDefenseDelete). The active
// policy is refused first: AMT cannot delete it.
func (p *defensePane) confirmDeletePolicy(pol amt.DefensePolicy) {
	if pol.Active {
		showError(p.window(), amt.ErrDefensePolicyActive)
		return
	}
	p.deleteDlg = showConfirm(p.window(), icons.Delete,
		lang.L("Delete “{{.Name}}”?", map[string]any{"Name": pol.Name}),
		lang.L("The policy is removed from {{.Name}}. Its filters stay.", map[string]any{"Name": p.m.name}),
		lang.L("Delete"), func() { p.write(p.d.sd, func() error { return p.m.sess.DeleteDefensePolicy(pol.ID) }) }, nil)
}

// confirmDeleteFilter asks before a filter is deleted (simple, OpDefenseDelete); a filter a
// policy uses is refused first.
func (p *defensePane) confirmDeleteFilter(f amt.DefenseFilter, sd amt.SystemDefense) {
	for _, pol := range sd.Policies {
		for _, id := range pol.Filters {
			if id == f.ID {
				showError(p.window(), &amt.DefenseFilterInUseError{Policy: pol.Name})
				return
			}
		}
	}
	p.deleteDlg = showConfirm(p.window(), icons.Delete,
		lang.L("Delete “{{.Name}}”?", map[string]any{"Name": f.Name}),
		lang.L("The filter is removed from {{.Name}}.", map[string]any{"Name": p.m.name}),
		lang.L("Delete"), func() { p.write(p.d.sd, func() error { return p.m.sess.DeleteDefenseFilter(f.ID) }) }, nil)
}

// confirmDeleteWatchdog asks before a watchdog is deleted (simple, OpWatchdogDelete).
func (p *defensePane) confirmDeleteWatchdog(w amt.Watchdog, name string) {
	p.deleteDlg = showConfirm(p.window(), icons.Delete,
		lang.L("Delete “{{.Name}}”?", map[string]any{"Name": name}),
		lang.L("Intel AMT stops watching this agent and drops its actions."),
		lang.L("Delete"), func() { p.write(p.d.wd, func() error { return p.m.sess.DeleteWatchdog(w.ID) }) }, nil)
}

// describeDefense covers the refusals of System Defense and the agent watchdogs.
func describeDefense(err error) (string, bool) {
	var inUse *amt.DefenseFilterInUseError
	var shared *amt.WatchdogPolicySharedError
	switch {
	case errors.Is(err, amt.ErrDefenseNameExists):
		return lang.L("A filter or policy with this name exists. Choose a different name."), true
	case errors.As(err, &inUse):
		return lang.L("The policy “{{.Policy}}” uses this filter. Delete the policy first.", map[string]any{"Policy": inUse.Policy}), true
	case errors.Is(err, amt.ErrDefensePolicyActive):
		return lang.L("Turn the policy off before you delete it."), true
	case errors.Is(err, amt.ErrDefenseNotFound), errors.Is(err, amt.ErrWatchdogNotFound):
		return lang.L("The device no longer has this entry. Refresh the page."), true
	case errors.Is(err, amt.ErrWatchdogExists):
		return lang.L("A watchdog with this agent ID exists. Generate a new ID."), true
	case errors.As(err, &shared):
		return lang.L("All watchdogs switch the same policy, “{{.Policy}}”. Choose it, or delete the watchdogs that use it first.", map[string]any{"Policy": shared.Policy}), true
	}
	return "", false
}

// trimmed is the text of e without surrounding spaces.
func trimmed(e *dialogEntry) string { return strings.TrimSpace(e.Text) }
