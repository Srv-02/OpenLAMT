package ui

import (
	"errors"
	"fmt"
	"image/png"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

const testAgentID = "5f1c2a9e-0d3b-4e57-9a61-2c8b7e4d1f03"

// exampleDefense is the design's page: "Allow all" active, "Isolate" drops and holds two filters,
// one more filter, one watchdog that activates Isolate.
func exampleDefense(st *session.FakeDefense) {
	st.Defense = amt.SystemDefense{Offered: true, Active: "allow",
		Policies: []amt.DefensePolicy{
			{ID: "allow", Name: "Allow all", Precedence: 2},
			{ID: "iso", Name: "Isolate", Precedence: 1, DropIn: true, DropOut: true, Filters: []string{"dhcp", "dns"}},
		},
		Filters: []amt.DefenseFilter{
			{ID: "dhcp", Name: "allow-dhcp", Kind: amt.FilterIPv4, Direction: amt.Outbound, Protocol: amt.ProtocolUDP, PortStart: 67, PortEnd: 68},
			{ID: "dns", Name: "allow-dns", Kind: amt.FilterIPv4, Direction: amt.Outbound, Protocol: amt.ProtocolUDP, PortStart: 53, PortEnd: 53,
				Remote: netip.MustParsePrefix("192.0.2.1/32"), Count: true, Matches: 12, HasMatches: true},
			{ID: "ssh", Name: "allow-ssh", Kind: amt.FilterIPv4, Direction: amt.Inbound, Protocol: amt.ProtocolTCP, PortStart: 22, PortEnd: 22,
				Remote: netip.MustParsePrefix("192.0.2.0/24")},
		}}
	st.Watchdogs = amt.Watchdogs{Offered: true, Policy: "iso", List: []amt.Watchdog{{ID: testAgentID, Name: "backup-agent",
		Startup: 120, Heartbeat: 30, State: amt.WatchdogRunning,
		OnStop: amt.WatchdogActions{Event: true, Policy: amt.PolicyActivate}, OnStart: amt.WatchdogActions{Policy: amt.PolicyDeactivate}}}}
}

func defensePageIndex(t *testing.T, m *manager) int {
	t.Helper()
	for i, p := range m.pages {
		if p.title == "System Defense" && p.section == sectionMore {
			return i
		}
	}
	t.Fatal("no System Defense page in the More section")
	return -1
}

// defenseHarness opens the System Defense page in the sidebar (or the window) on the example.
func defenseHarness(t *testing.T, window bool, mod func(*session.FakeDefense)) (*manageHarness, *defensePane) {
	t.Helper()
	englishUI(t)
	dev := &session.Fake{FP: fp}
	exampleDefense(dev.DefensePage())
	if mod != nil {
		mod(dev.DefensePage())
	}
	h := newManageHarness(t, dev)
	h.m.Sidebar()
	h.wait(t) // hardware
	i := defensePageIndex(t, h.m)
	view := h.m.side.view
	if window {
		h.m.OpenWindow()
		view = h.m.win.view
		h.m.win.showPage(i)
	} else {
		view.show(i)
	}
	h.wait(t) // system defense
	h.wait(t) // watchdogs
	return h, view.panes[i].impl.(*defensePane)
}

func cellTexts(l *listLine) []string {
	var out []string
	for _, c := range l.cells {
		out = append(out, richText(c))
	}
	return out
}

func TestDefensePageRegisteredInMore(t *testing.T) {
	englishUI(t)
	for _, d := range managePageDefs {
		if tab, title := d.names(); title == "System Defense" {
			if d.section != sectionMore || d.order != 30 || tab != "System Defense" || d.icon != icons.ShieldOK {
				t.Fatalf("registered as %+v, tab %q", d, tab)
			}
			return
		}
	}
	t.Fatal("System Defense is not registered")
}

func TestDefensePageWindowShowsTheDesignColumns(t *testing.T) {
	_, p := defenseHarness(t, true, nil)
	if got := p.active.Text; got != "· Active policy: Allow all" {
		t.Errorf("head %q", got)
	}
	want := [][]string{
		{"Allow all", "2", "Allow", "0", "Active"},
		{"Isolate", "1", "Drop", "2", "Off"},
	}
	for i, w := range want {
		if got := cellTexts(p.policies.lines[i]); !slices.Equal(got, w) {
			t.Errorf("policy %d = %q, want %q", i, got, w)
		}
	}
	if l := p.policies.lines[0]; l.lead.Text != "Turn Off…" || l.action.Text != "Delete…" {
		t.Errorf("active policy buttons %q %q", l.lead.Text, l.action.Text)
	}
	if l := p.policies.lines[1]; l.lead.Text != "Activate…" || l.action.Text != "Delete…" {
		t.Errorf("policy buttons %q %q", l.lead.Text, l.action.Text)
	}
	for _, l := range p.policies.lines {
		if lx, ax := l.lead.Position().X+l.lead.Size().Width, l.action.Position().X; ax <= 0 || lx+listLeadGap > ax+0.5 {
			t.Errorf("%q ends at %.1f, %q starts at %.1f", l.lead.Text, lx, l.action.Text, ax)
		}
	}
	wantF := [][]string{
		{"allow-dhcp", "IPv4", "Outbound", "UDP 67-68", "Allow"},
		{"allow-dns", "IPv4", "Outbound", "UDP 53 to 192.0.2.1", "Allow, 12 matches"},
		{"allow-ssh", "IPv4", "Inbound", "TCP 22 from 192.0.2.0/24", "Allow"},
	}
	for i, w := range wantF {
		if got := cellTexts(p.filters.lines[i]); !slices.Equal(got, w) {
			t.Errorf("filter %d = %q, want %q", i, got, w)
		}
	}
	if got := cellTexts(p.watchdogs.lines[0]); !slices.Equal(got, []string{"backup-agent", "120 s", "30 s", "Running", "Log event, activate Isolate"}) {
		t.Errorf("watchdog %q", got)
	}
	for _, b := range []string{p.addPolicy.Text, p.addFilter.Text, p.addWatchdog.Text} {
		if !strings.HasPrefix(b, "+ Add ") {
			t.Errorf("add button %q", b)
		}
	}
	if p.addPolicy.Disabled() || p.addFilter.Disabled() || p.addWatchdog.Disabled() {
		t.Error("add buttons locked after the read")
	}
	if !hasText(p.pane.body, "Agent watchdog") || !hasText(p.pane.body, "Policies") || !hasText(p.pane.body, "Filters") {
		t.Error("section heads missing")
	}
	if p.pane.tools != p.refresh {
		t.Error("the window head lacks Refresh")
	}
}

func TestDefensePageSidebarColumns(t *testing.T) {
	_, p := defenseHarness(t, false, nil)
	if got := cellTexts(p.policies.lines[1]); !slices.Equal(got, []string{"Isolate", "Off"}) {
		t.Errorf("policy %q", got)
	}
	if got := cellTexts(p.filters.lines[1]); !slices.Equal(got, []string{"allow-dns", "UDP 53 to 192.0.2.1", "Allow, 12 matches"}) {
		t.Errorf("filter %q", got)
	}
	if got := cellTexts(p.watchdogs.lines[0]); !slices.Equal(got, []string{"backup-agent", "Running"}) {
		t.Errorf("watchdog %q", got)
	}
	if p.active != nil {
		t.Error("the sidebar has no head line")
	}
}

func TestDefensePageHidesWhatTheDeviceLacks(t *testing.T) {
	_, p := defenseHarness(t, false, func(st *session.FakeDefense) { st.Watchdogs = amt.Watchdogs{} })
	if !p.policies.Visible() || !p.filters.Visible() || p.watchdogs.Visible() || p.watchdogNote.Visible() || p.unavailable.Visible() {
		t.Errorf("watchdog card shown without the class: policies %v filters %v watchdogs %v", p.policies.Visible(), p.filters.Visible(), p.watchdogs.Visible())
	}

	_, p = defenseHarness(t, false, func(st *session.FakeDefense) { st.Defense = amt.SystemDefense{} })
	if p.policies.Visible() || p.filters.Visible() || !p.watchdogs.Visible() || p.unavailable.Visible() {
		t.Error("System Defense cards shown without the classes")
	}

	_, p = defenseHarness(t, false, func(st *session.FakeDefense) { st.Defense, st.Watchdogs = amt.SystemDefense{}, amt.Watchdogs{} })
	if !p.unavailable.Visible() || p.policies.Visible() || p.watchdogs.Visible() {
		t.Fatal("no notice without both")
	}
	var found bool
	walk(p.unavailable, func(o fyne.CanvasObject) {
		if c, ok := o.(*canvas.Text); ok && c.Text == "Not available on this device" {
			found = true
		}
	})
	if !found {
		t.Error("notice lacks its title")
	}
}

func TestDefensePageAccessDeniedShowsNA(t *testing.T) {
	_, p := defenseHarness(t, true, func(st *session.FakeDefense) { st.Err = amt.ErrAccessDenied })
	if !p.err.box.Visible() || p.active.Text != "· Active policy: N/A" {
		t.Errorf("error %v, head %q", p.err.box.Visible(), p.active.Text)
	}
	if !p.addPolicy.Disabled() || !p.addFilter.Disabled() || !p.addWatchdog.Disabled() {
		t.Error("add buttons usable without a read")
	}
	if len(p.policies.lines) != 0 || !p.policies.Visible() {
		t.Error("the list must stay, empty")
	}
}

// Activating a policy that drops traffic is orange (V21): warning, old → new, the checkbox and
// a red button; it writes only after the checkbox.
func TestActivateDropPolicyIsOrange(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.policies.lines[1].lead)
	d := p.riskDlg
	if d == nil || !open(h.m.parent) {
		t.Fatal("no confirmation")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Activate “Isolate” on Lab?")
	if !hasRichText(t, top, "The operating system of Lab loses all network traffic that the policy drops. Intel AMT stays reachable, so you can turn the policy off here.", false) {
		t.Error("warning text missing")
	}
	if !hasRichText(t, top, "Active policy: Allow all → ", false) || !hasRichText(t, top, "Isolate", true) {
		t.Error("old → new line missing")
	}
	if !strings.HasPrefix(d.check.Text, "I understand the risk for Lab") || d.action.Text != "Activate Policy" || !d.action.Disabled() {
		t.Fatalf("check %q, button %q disabled %v", d.check.Text, d.action.Text, d.action.Disabled())
	}
	test.Tap(d.check)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.DefensePage().Written(); !slices.Equal(got, []string{"ActivateDefensePolicy"}) {
		t.Fatalf("writes %v", got)
	}
	if got := cellTexts(p.policies.lines[1]); got[1] != "Active" || p.policies.lines[1].lead.Text != "Turn Off…" {
		t.Errorf("after activation %q", got)
	}
}

func TestActivateAllowPolicyAndTurnOffNeedNoConfirmation(t *testing.T) {
	h, p := defenseHarness(t, false, func(st *session.FakeDefense) { st.Defense.Active = "iso" })
	test.Tap(p.policies.lines[0].lead) // Activate… on Allow all
	if open(h.m.parent) {
		t.Fatal("a policy that drops nothing asked first")
	}
	h.wait(t)
	h.wait(t)
	test.Tap(p.policies.lines[0].lead) // Turn Off…
	if open(h.m.parent) {
		t.Fatal("Turn Off asked first")
	}
	h.wait(t)
	h.wait(t)
	if got := h.dev.DefensePage().Written(); !slices.Equal(got, []string{"ActivateDefensePolicy", "DeactivateDefensePolicy"}) {
		t.Fatalf("writes %v", got)
	}
	if h.dev.DefensePage().Defense.Active != "" {
		t.Error("still active")
	}
}

// Issue #167: two linked policies are not shown as one active policy. The page names both
// above the lists, and Turn Off on either one turns both off.
func TestDefensePageTwoLinkedPolicies(t *testing.T) {
	h, p := defenseHarness(t, true, func(st *session.FakeDefense) {
		st.Defense.Active, st.Defense.Linked = "", []string{"allow", "iso"}
	})
	want := "More than one policy is linked to the wired port: Allow all, Isolate. It is unclear which one filters. Turn the policy off and activate one again."
	if !p.err.box.Visible() || p.err.Text() != want {
		t.Fatalf("error %v %q", p.err.box.Visible(), p.err.Text())
	}
	if p.active.Text != "· Active policy: Allow all, Isolate" {
		t.Errorf("head %q", p.active.Text)
	}
	for i, l := range p.policies.lines {
		if got := cellTexts(l); got[len(got)-1] != "Active" || l.lead.Text != "Turn Off…" {
			t.Errorf("row %d %q, lead %q", i, got, l.lead.Text)
		}
	}
	test.Tap(p.policies.lines[1].lead)
	h.wait(t)
	h.wait(t)
	if got := h.dev.DefensePage().Written(); !slices.Equal(got, []string{"DeactivateDefensePolicy"}) {
		t.Fatalf("writes %v", got)
	}
	if p.err.box.Visible() || p.active.Text != "· Active policy: None" {
		t.Errorf("after Turn Off: error %v, head %q", p.err.box.Visible(), p.active.Text)
	}
}

func TestDeletePolicySimpleAndActiveRefused(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.policies.lines[0].action) // the active one
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Turn the policy off before you delete it.")
	h.m.parent.Canvas().Overlays().Top().Hide()
	h.m.parent.Canvas().Overlays().Remove(h.m.parent.Canvas().Overlays().Top())

	test.Tap(p.policies.lines[1].action)
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete “Isolate”?")
	findLabel(t, top, "The policy is removed from Lab. Its filters stay.")
	bs := panelButtons(p.deleteDlg.Content)
	if texts(bs) != "Cancel|Delete" {
		t.Fatalf("buttons %q (a simple confirmation has no checkbox)", texts(bs))
	}
	test.Tap(bs[1])
	h.wait(t)
	h.wait(t)
	if got := h.dev.DefensePage().Written(); !slices.Equal(got, []string{"DeleteDefensePolicy"}) {
		t.Fatalf("writes %v", got)
	}
	if len(p.policies.lines) != 1 {
		t.Errorf("%d policies after the delete", len(p.policies.lines))
	}
}

func TestDeleteFilterInUseRefusedAndUnusedDeleted(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.filters.lines[0].action) // allow-dhcp, in Isolate
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "The policy “Isolate” uses this filter. Delete the policy first.")
	h.m.parent.Canvas().Overlays().Remove(h.m.parent.Canvas().Overlays().Top())
	if len(h.dev.DefensePage().Written()) != 0 {
		t.Fatal("wrote a filter in use")
	}
	test.Tap(p.filters.lines[2].action) // allow-ssh
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Delete “allow-ssh”?")
	test.Tap(panelButtons(p.deleteDlg.Content)[1])
	h.wait(t)
	h.wait(t)
	if got := h.dev.DefensePage().Written(); !slices.Equal(got, []string{"DeleteDefenseFilter"}) || len(p.filters.lines) != 2 {
		t.Fatalf("writes %v, %d filters", got, len(p.filters.lines))
	}
}

func TestDeleteWatchdogSimple(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.watchdogs.lines[0].action)
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete “backup-agent”?")
	findLabel(t, top, "Intel AMT stops watching this agent and drops its actions.")
	test.Tap(panelButtons(p.deleteDlg.Content)[1])
	h.wait(t)
	h.wait(t)
	if got := h.dev.DefensePage().Written(); !slices.Equal(got, []string{"DeleteWatchdog"}) || len(p.watchdogs.lines) != 0 {
		t.Fatalf("writes %v, %d watchdogs", got, len(p.watchdogs.lines))
	}
}

func TestAddFilterDialog(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.addFilter)
	d := p.filterDlg
	if d == nil || !open(h.m.parent) {
		t.Fatal("no dialog")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Add filter")
	if d.action.Text != "Add Filter" || !d.action.Disabled() {
		t.Fatalf("button %q disabled %v without a name", d.action.Text, d.action.Disabled())
	}
	if d.kind.Selected() != 0 || d.dir.Selected() != 0 || d.verdict.Selected() != 0 || d.proto.Selected != "TCP" {
		t.Errorf("defaults: type %d direction %d action %d protocol %q", d.kind.Selected(), d.dir.Selected(), d.verdict.Selected(), d.proto.Selected)
	}
	d.name.SetText("web-from-lan-and-more")
	if n := len([]rune(d.name.Text)); n != amt.MaxDefenseName {
		t.Errorf("name keeps %d characters", n)
	}
	d.name.SetText("allow-web")
	d.ports.SetText("80-443")
	d.remote.SetText("192.0.2.7/24")
	if d.action.Disabled() {
		t.Fatal("button locked for a valid filter")
	}
	for _, bad := range []struct{ ports, remote string }{{"0", ""}, {"70000", ""}, {"443-80", ""}, {"http", ""}, {"80", "2001:db8::/32"}, {"80", "lan"}} {
		d.ports.SetText(bad.ports)
		d.remote.SetText(bad.remote)
		if !d.action.Disabled() {
			t.Errorf("button usable for ports %q remote %q", bad.ports, bad.remote)
		}
	}
	d.ports.SetText("80-443")
	d.remote.SetText("192.0.2.7/24")
	d.dir.Tap(1)
	d.count.SetChecked(true)
	d.verdict.Tap(1)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if open(h.m.parent) {
		t.Error("dialog stays open after success")
	}
	var got amt.DefenseFilter
	for _, f := range h.dev.DefensePage().Defense.Filters {
		if f.Name == "allow-web" {
			got = f
		}
	}
	want := amt.DefenseFilter{ID: got.ID, Name: "allow-web", Kind: amt.FilterIPv4, Direction: amt.Outbound, Protocol: amt.ProtocolTCP,
		PortStart: 80, PortEnd: 443, Remote: netip.MustParsePrefix("192.0.2.0/24"), Drop: true, Count: true}
	if got != want {
		t.Errorf("filter\n got %+v\nwant %+v", got, want)
	}
	if len(p.filters.lines) != 4 {
		t.Errorf("%d filters after the reload", len(p.filters.lines))
	}
}

func TestAddFilterDialogEthernetAndICMP(t *testing.T) {
	_, p := defenseHarness(t, false, nil)
	test.Tap(p.addFilter)
	d := p.filterDlg
	d.name.SetText("no-arp")
	d.kind.Tap(2)
	if !slices.Equal(d.proto.Options, []string{"IP", "ARP"}) {
		t.Errorf("Ethernet protocols %v", d.proto.Options)
	}
	for _, o := range d.ipOnly {
		if o.Visible() {
			t.Error("ports or address shown for Ethernet")
		}
	}
	d.proto.SetSelected("ARP")
	s, ok := d.spec()
	if !ok || s.Kind != amt.FilterEthernet || s.Protocol != amt.EtherTypeARP {
		t.Errorf("spec %+v %v", s, ok)
	}
	d.kind.Tap(1)
	d.proto.SetSelected("ICMP")
	if !d.ports.Disabled() {
		t.Error("ports usable for ICMP")
	}
	d.remote.SetText("2001:db8::1")
	if s, ok := d.spec(); !ok || s.Kind != amt.FilterIPv6 || s.Protocol != amt.ProtocolICMPv6 || s.Remote.String() != "2001:db8::1/128" {
		t.Errorf("IPv6 ICMP spec %+v %v", s, ok)
	}
	d.Dismiss()
}

func TestAddFilterRefusalStaysInDialog(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.addFilter)
	d := p.filterDlg
	d.name.SetText("ALLOW-SSH")
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if !open(h.m.parent) || !d.err.box.Visible() || d.err.label.Text != "A filter or policy with this name exists. Choose a different name." {
		t.Fatalf("open %v, error %q", open(h.m.parent), d.err.label.Text)
	}
	d.Dismiss()
}

func TestAddPolicyDialog(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.addPolicy)
	d := p.policyDlg
	if d == nil {
		t.Fatal("no dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Add policy")
	if len(d.filters) != 3 || d.filters[0].Text != "allow-dhcp" || d.precedence.Text != "1" || d.otherwise.Selected() != 1 {
		t.Fatalf("filters %d, precedence %q, otherwise %d", len(d.filters), d.precedence.Text, d.otherwise.Selected())
	}
	if hasText(top, "Rate limit") || hasText(top, "Lower number wins when several policies are active.") {
		t.Error("the design's rate limit or wrong precedence hint is shown")
	}
	if !d.action.Disabled() {
		t.Error("button usable without a name")
	}
	d.name.SetText("SSH only")
	d.precedence.SetText("x")
	if !d.action.Disabled() {
		t.Error("button usable with a bad precedence")
	}
	d.precedence.SetText("7")
	d.filters[2].SetChecked(true)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	pols := h.dev.DefensePage().Defense.Policies
	got := pols[len(pols)-1]
	if got.Name != "SSH only" || got.Precedence != 7 || !got.DropIn || !got.DropOut || !slices.Equal(got.Filters, []string{"ssh"}) {
		t.Errorf("policy %+v", got)
	}
}

func TestAddWatchdogDialogDefaults(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.addWatchdog)
	d := p.watchdogDlg
	if d == nil {
		t.Fatal("no dialog")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Add agent watchdog")
	if !amt.ValidAgentID(d.id.Text) || d.startup.Text != "120" || d.heartbeat.Text != "30" {
		t.Errorf("id %q startup %q heartbeat %q", d.id.Text, d.startup.Text, d.heartbeat.Text)
	}
	if !d.stopEvent.Checked || !d.activate.Checked || !d.startEvent.Checked || !d.deactivate.Checked || d.policy.Selected != "Isolate" {
		t.Errorf("checks %v %v %v %v policy %q", d.stopEvent.Checked, d.activate.Checked, d.startEvent.Checked, d.deactivate.Checked, d.policy.Selected)
	}
	old := d.id.Text
	test.Tap(d.generate)
	if d.id.Text == old || !amt.ValidAgentID(d.id.Text) {
		t.Errorf("Generate gave %q", d.id.Text)
	}
	d.activate.SetChecked(false)
	if !d.deactivate.Disabled() {
		t.Error("“Turn the policy off again” usable without a policy")
	}
	d.startup.SetText("0")
	if !d.action.Disabled() {
		t.Error("button usable with startup 0")
	}
	d.Dismiss()
}

// A watchdog that activates a drop policy asks with the orange warning before it is added.
func TestAddWatchdogActivatingDropPolicyIsOrange(t *testing.T) {
	h, p := defenseHarness(t, false, func(st *session.FakeDefense) { st.Watchdogs.List = nil })
	test.Tap(p.addWatchdog)
	d := p.watchdogDlg
	d.name.SetText("av-agent")
	test.Tap(d.action)
	r := p.riskDlg
	if r == nil {
		t.Fatal("no orange confirmation")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Let the watchdog activate “Isolate”?")
	if !hasRichText(t, top, "The operating system of Lab loses all network traffic that the policy drops. Intel AMT stays reachable, so you can turn the policy off here.", false) {
		t.Error("warning text missing")
	}
	if !r.action.Disabled() {
		t.Fatal("button usable before the checkbox")
	}
	test.Tap(r.check)
	test.Tap(r.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.DefensePage().Written(); !slices.Equal(got, []string{"AddWatchdog"}) {
		t.Fatalf("writes %v", got)
	}
	w := h.dev.DefensePage().Watchdogs.List[0]
	if w.Name != "av-agent" || w.OnStop != (amt.WatchdogActions{Event: true, Policy: amt.PolicyActivate}) ||
		w.OnStart != (amt.WatchdogActions{Event: true, Policy: amt.PolicyDeactivate}) {
		t.Errorf("watchdog %+v", w)
	}
	if open(h.m.parent) {
		t.Error("dialogs stay open after success")
	}
}

func TestAddWatchdogWithoutPolicyNeedsNoConfirmation(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.addWatchdog)
	d := p.watchdogDlg
	d.activate.SetChecked(false)
	test.Tap(d.action)
	if p.riskDlg != nil {
		t.Fatal("asked without a policy action")
	}
	h.wait(t)
	h.wait(t)
	list := h.dev.DefensePage().Watchdogs.List
	if len(list) != 2 || list[1].OnStop.Policy != amt.PolicyKeep || list[1].OnStart.Policy != amt.PolicyKeep || !list[1].OnStop.Event {
		t.Errorf("watchdogs %+v", list)
	}
}

func TestAddWatchdogSharedPolicyErrorStaysInDialog(t *testing.T) {
	h, p := defenseHarness(t, false, nil)
	test.Tap(p.addWatchdog)
	d := p.watchdogDlg
	d.policy.SetSelected("Allow all")
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if !d.err.box.Visible() || !strings.Contains(d.err.label.Text, "All watchdogs switch the same policy, “Isolate”.") {
		t.Fatalf("error %q", d.err.label.Text)
	}
	d.Dismiss()
}

func TestAddWatchdogWithoutPoliciesLocksActivate(t *testing.T) {
	_, p := defenseHarness(t, false, func(st *session.FakeDefense) { st.Defense = amt.SystemDefense{} })
	test.Tap(p.addWatchdog)
	d := p.watchdogDlg
	if d.activate.Checked || !d.activate.Disabled() {
		t.Error("“Activate policy” usable without policies")
	}
	if d.action.Disabled() {
		t.Error("button locked")
	}
	d.Dismiss()
}

func TestDefenseTexts(t *testing.T) {
	englishUI(t)
	for _, c := range []struct {
		f    amt.DefenseFilter
		want string
	}{
		{amt.DefenseFilter{Kind: amt.FilterIPv4, Protocol: amt.ProtocolAny}, "Any"},
		{amt.DefenseFilter{Kind: amt.FilterIPv4, Protocol: amt.ProtocolAny, Direction: amt.Outbound, Remote: netip.MustParsePrefix("192.0.2.0/28")}, "Any to 192.0.2.0/28"},
		{amt.DefenseFilter{Kind: amt.FilterIPv6, Protocol: amt.ProtocolICMPv6, Direction: amt.Inbound, Remote: netip.MustParsePrefix("2001:db8::/32")}, "ICMPv6 from 2001:db8::/32"},
		{amt.DefenseFilter{Kind: amt.FilterIPv4, Protocol: 47}, "Protocol 47"},
		{amt.DefenseFilter{Kind: amt.FilterEthernet, Protocol: amt.EtherTypeARP}, "ARP packets"},
		{amt.DefenseFilter{Kind: amt.FilterEthernet, Protocol: 0x86DD}, "EtherType 0x86DD"},
	} {
		if got := matchText(c.f); got != c.want {
			t.Errorf("matchText = %q, want %q", got, c.want)
		}
	}
	for _, c := range []struct {
		f    amt.DefenseFilter
		want string
	}{
		{amt.DefenseFilter{Drop: true}, "Drop"},
		{amt.DefenseFilter{RateLimit: 100}, "Rate limit 100/s"},
		{amt.DefenseFilter{Drop: true, Count: true, HasMatches: true, Matches: 1}, "Drop, 1 match"},
		{amt.DefenseFilter{HasMatches: true}, "Allow, 0 matches"},
	} {
		if got := actionText(c.f); got != c.want {
			t.Errorf("actionText = %q, want %q", got, c.want)
		}
	}
	if otherwiseText(amt.DefensePolicy{DropIn: true}) != "Drop inbound" || otherwiseText(amt.DefensePolicy{DropOut: true}) != "Drop outbound" {
		t.Error("one-way Otherwise")
	}
	for a, want := range map[amt.WatchdogActions]string{
		{}:                           "–",
		{Event: true}:                "Log event",
		{Policy: amt.PolicyActivate}: "Activate Isolate",
		{Event: true, Policy: amt.PolicyDeactivate}: "Log event, turn off Isolate",
	} {
		if got := stopText(a, "Isolate"); got != want {
			t.Errorf("stopText(%+v) = %q, want %q", a, got, want)
		}
	}
	if s, c := watchdogStateText(amt.WatchdogExpired); s != "Expired" || c == "" {
		t.Errorf("expired %q", s)
	}
}

func TestDefenseErrorTexts(t *testing.T) {
	englishUI(t)
	for err, want := range map[error]string{
		amt.ErrDefensePolicyActive:                       "Turn the policy off before you delete it.",
		&amt.DefenseFilterInUseError{Policy: "Isolate"}:  "The policy “Isolate” uses this filter. Delete the policy first.",
		&amt.WatchdogPolicySharedError{Policy: "Strict"}: "All watchdogs switch the same policy, “Strict”. Choose it, or delete the watchdogs that use it first.",
		amt.ErrWatchdogExists:                            "A watchdog with this agent ID exists. Generate a new ID.",
		fmt.Errorf("activate system defense policy: %w: %w", &amt.DefenseLinksError{Policies: []string{"Allow all", "Isolate"}}, errors.New("connection reset")): "More than one policy is linked to the wired port: Allow all, Isolate. It is unclear which one filters. Turn the policy off and activate one again.",
	} {
		if got := describe(err); got != want {
			t.Errorf("describe(%v) = %q, want %q", err, got, want)
		}
	}
	if got := describe(errors.Join(errors.New("context"), amt.ErrDefenseNotFound)); got != "The device no longer has this entry. Refresh the page." {
		t.Errorf("not found: %q", got)
	}
}

// TestDefenseShots renders the System Defense page in the sidebar and the window and its
// dialogs, light and dark, into $OPENLAMT_SHOTS for review against the overall design. Skipped
// without the variable. Only made-up data.
func TestDefenseShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := func(name string, w fyne.Window) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, w.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
	restoreDarkVariant(t)
	h, p := defenseHarness(t, false, nil)
	switchVariant(h.a, theme.VariantLight)
	device := h.m.parent
	device.SetPadded(false)
	scaleWindow(device)
	stage := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "")
	device.SetContent(container.NewBorder(nil, nil, nil, h.m.Sidebar(), stage))
	device.Resize(fyne.NewSize(1280, 800))
	shot("defense-sidebar.png", device)
	test.Tap(p.policies.lines[1].lead)
	shot("defense-activate.png", device)
	p.riskDlg.Dismiss()

	h.m.OpenWindow()
	w := h.m.win
	scaleWindow(w.win)
	w.win.Resize(fyne.NewSize(1280, 800))
	i := defensePageIndex(t, h.m)
	w.showPage(i)
	wp := w.view.panes[i].impl.(*defensePane)
	shot("defense-window.png", w.win)
	test.Tap(wp.addFilter)
	shot("defense-add-filter.png", w.win)
	wp.filterDlg.Dismiss()
	test.Tap(wp.addPolicy)
	shot("defense-add-policy.png", w.win)
	wp.policyDlg.Dismiss()
	test.Tap(wp.addWatchdog)
	shot("defense-add-watchdog.png", w.win)
	wp.watchdogDlg.Dismiss()
	switchVariant(h.a, theme.VariantDark)
	shot("defense-window-dark.png", w.win)
}

// Issue #161: a failed policy switch that may leave the port without policy says so, whatever
// the cause behind it.
func TestDescribeDefensePolicyLost(t *testing.T) {
	englishUI(t)
	err := fmt.Errorf("activate system defense policy: %w: %w", amt.ErrDefensePolicyLost, amt.ErrIncompleteResponse)
	if got := describe(err); !strings.Contains(got, "may have no policy now") {
		t.Fatalf("describe = %q", got)
	}
}
