package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

func wsmanInstance(class string, props ...string) amt.WSManInstance {
	in := amt.WSManInstance{Class: class}
	var xml strings.Builder
	xml.WriteString("<" + class + ">\n")
	for i := 0; i+1 < len(props); i += 2 {
		in.Props = append(in.Props, amt.WSManProp{Name: props[i], Value: props[i+1]})
		fmt.Fprintf(&xml, "  <%s>%s</%s>\n", props[i], props[i+1], props[i])
	}
	xml.WriteString("</" + class + ">")
	in.XML = xml.String()
	return in
}

func wsmanFake() *session.Fake {
	dev := &session.Fake{FP: fp}
	w := dev.WSMan()
	w.Results = map[string]amt.WSManResult{
		"AMT_GeneralSettings": {Class: "AMT_GeneralSettings", Instances: []amt.WSManInstance{wsmanInstance("AMT_GeneralSettings",
			"HostName", "labserver", "DomainName", "example.com", "AMTNetworkEnabled", "1", "PowerSource", "0")}},
		"CIM_SoftwareIdentity": {Class: "CIM_SoftwareIdentity", Instances: []amt.WSManInstance{
			wsmanInstance("CIM_SoftwareIdentity", "InstanceID", "Flash", "VersionString", "16.1.25"),
			wsmanInstance("CIM_SoftwareIdentity", "InstanceID", "Netstack", "VersionString", "16.1.25"),
		}},
		"IPS_OptInService": {Class: "IPS_OptInService"},
		"CIM_SettingData":  {Class: "CIM_SettingData", Truncated: true, Instances: []amt.WSManInstance{wsmanInstance("AMT_Derived", "Name", "x")}},
	}
	return dev
}

func wsmanPageIndex(t *testing.T, m *manager) int {
	t.Helper()
	for i, p := range m.pages {
		if p.title == "WSMAN Browser" && p.section == sectionDiagnostics {
			return i
		}
	}
	t.Fatal("no WSMAN Browser page in the Diagnostics section")
	return -1
}

// wsmanHarness opens the management window on the WSMAN Browser page.
func wsmanHarness(t *testing.T, dev *session.Fake) (*manageHarness, *wsmanPane) {
	t.Helper()
	englishUI(t)
	h := newManageHarness(t, dev)
	i := wsmanPageIndex(t, h.m)
	h.m.openWindowAt(i)
	return h, h.m.win.view.panes[i].impl.(*wsmanPane)
}

// choose taps the class row and applies the read.
func (p *wsmanPane) choose(t *testing.T, h *manageHarness, class string) {
	t.Helper()
	for id, it := range p.items {
		if !it.header && it.text == class {
			p.list.Select(id)
			return
		}
	}
	t.Fatalf("class %s is not listed", class)
}

func tableCells(p *wsmanPane) [][2]string {
	var out [][2]string
	for _, l := range p.table.lines {
		out = append(out, [2]string{l.cells[0].String(), l.cells[1].String()})
	}
	return out
}

func TestWSManPageIsRegisteredInDiagnostics(t *testing.T) {
	var found []managePageDef
	for _, d := range managePageDefs {
		if d.section == sectionDiagnostics {
			found = append(found, d)
		}
	}
	if len(found) == 0 || found[0].order != 10 || found[0].icon != icons.Code {
		t.Fatalf("diagnostics pages %+v", found)
	}
	tab, title := found[0].names()
	if tab != "WSMAN Browser" || title != "WSMAN Browser" {
		t.Errorf("names %q %q", tab, title)
	}
}

func TestWSManInitialState(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	if p.cardText[0] != "Choose a class" || p.table.Visible() || p.copyBtn.Visible() || !p.refresh.Disabled() {
		t.Errorf("start: card %q table %v copy %v refresh disabled %v", p.cardText, p.table.Visible(), p.copyBtn.Visible(), p.refresh.Disabled())
	}
	if len(h.dev.WSMan().Read()) != 0 {
		t.Error("the page read a class before one was chosen")
	}
	total := 0
	var heads []string
	for _, it := range p.items {
		if it.header {
			heads = append(heads, it.text[:3])
			var n int
			if _, err := fmt.Sscanf(it.text[6:], "%d", &n); err != nil {
				t.Fatalf("heading %q", it.text)
			}
			total += n
		}
	}
	if strings.Join(heads, ",") != "AMT,CIM,IPS" || total != len(amt.WSManClasses()) || len(p.items) != total+3 {
		t.Errorf("groups %v, %d classes, %d rows", heads, total, len(p.items))
	}
	if p.count.Text != fmt.Sprintf("· %d classes", len(amt.WSManClasses())) {
		t.Errorf("count %q", p.count.Text)
	}
}

func TestWSManShowsPropertiesOfTheChosenClass(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.choose(t, h, "AMT_GeneralSettings")
	if !p.loading || p.note.Text != "Reading…" || !p.refresh.Disabled() {
		t.Errorf("while reading: loading %v note %q", p.loading, p.note.Text)
	}
	h.wait(t)
	if p.name.Text != "AMT_GeneralSettings" || p.number.Text != "· 1 instance" || p.note.Visible() {
		t.Errorf("head %q %q note %q", p.name.Text, p.number.Text, p.note.Text)
	}
	cells := tableCells(p)
	want := [][2]string{{"HostName", "labserver"}, {"DomainName", "example.com"}, {"AMTNetworkEnabled", "1 (Enabled)"}, {"PowerSource", "0 (AC)"}}
	if fmt.Sprint(cells) != fmt.Sprint(want) {
		t.Errorf("table %v", cells)
	}
	if !p.table.Visible() || p.chipBox.Visible() || p.card.Visible() && len(p.card.Objects) > 0 || !p.copyBtn.Visible() || p.refresh.Disabled() {
		t.Error("state after the read")
	}
	if got := h.dev.WSMan().Read(); len(got) != 1 || got[0] != "AMT_GeneralSettings" {
		t.Errorf("reads %v", got)
	}
	// Refresh reads the class again.
	test.Tap(p.refresh)
	h.wait(t)
	if got := h.dev.WSMan().Read(); len(got) != 2 {
		t.Errorf("after Refresh: reads %v", got)
	}
}

func TestWSManCopyAsXML(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.choose(t, h, "CIM_SoftwareIdentity")
	h.wait(t)
	test.Tap(p.copyBtn)
	got := h.a.Clipboard().Content()
	if !strings.Contains(got, "<VersionString>16.1.25</VersionString>") || strings.Count(got, "<CIM_SoftwareIdentity>") != 2 {
		t.Errorf("clipboard %q", got)
	}
}

func TestWSManInstanceChips(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.choose(t, h, "CIM_SoftwareIdentity")
	h.wait(t)
	if p.number.Text != "· 2 instances" || !p.chipBox.Visible() || len(p.chips) != 2 || !p.chips[0].Active() || p.chips[1].Active() {
		t.Fatalf("chips: %q visible %v %d", p.number.Text, p.chipBox.Visible(), len(p.chips))
	}
	if p.chips[0].Text() != "Instance 1" || p.chips[1].Text() != "Instance 2" {
		t.Errorf("chip captions %q %q", p.chips[0].Text(), p.chips[1].Text())
	}
	if c := tableCells(p); c[0][1] != "Flash" {
		t.Errorf("first instance %v", c)
	}
	test.Tap(p.chips[1])
	if c := tableCells(p); c[0][1] != "Netstack" || !p.chips[1].Active() || p.chips[0].Active() {
		t.Errorf("second instance %v", c)
	}
}

func TestWSManTruncatedAndDerivedClass(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.choose(t, h, "CIM_SettingData")
	h.wait(t)
	if !p.note.Visible() || !strings.Contains(p.note.Text, "Truncated: this class has more data than OpenLAMT reads at once.") || !strings.Contains(p.note.Text, "AMT_Derived") {
		t.Errorf("note %q", p.note.Text)
	}
}

func TestWSManClassWithoutInstances(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.choose(t, h, "IPS_OptInService")
	h.wait(t)
	if p.cardText[0] != "No instances" || p.table.Visible() || p.copyBtn.Visible() || p.number.Text != "· 0 instances" {
		t.Errorf("card %q table %v copy %v number %q", p.cardText, p.table.Visible(), p.copyBtn.Visible(), p.number.Text)
	}
}

func TestWSManErrors(t *testing.T) {
	dev := wsmanFake()
	dev.WSMan().Errs = map[string]error{
		"AMT_AuditLog":                   fmt.Errorf("read AMT_AuditLog: %w", amt.ErrAccessDenied),
		"AMT_TimeSynchronizationService": fmt.Errorf("read: %w", context.DeadlineExceeded),
		"AMT_PCIDevice":                  errors.New("boom"),
		"AMT_RedirectionService":         &amt.ClassUnsupportedError{Class: "AMT_RedirectionService", Fault: "DestinationUnreachable"},
		"AMT_TLSSettingData":             fmt.Errorf("x: %w", amterror.NewAMTError("e:AccessDenied", "x", "")),
	}
	h, p := wsmanHarness(t, dev)
	for _, tc := range []struct{ class, title, text string }{
		{"AMT_RedirectionService", "Lab does not support AMT_RedirectionService", "The device answered: DestinationUnreachable. Choose another class."},
		{"AMT_AuditLog", "The account admin cannot read this class.", "The device refused the request. Choose another class."},
		{"AMT_TLSSettingData", "The account admin cannot read this class.", ""},
		{"AMT_TimeSynchronizationService", "Lab did not answer in time", "Reading AMT_TimeSynchronizationService took too long. Try Refresh."},
		{"AMT_PCIDevice", "AMT_PCIDevice could not be read", "Unexpected error: boom"},
		{"CIM_Chassis", "Lab does not support CIM_Chassis", ""}, // the fake answers like a device without the class
	} {
		p.choose(t, h, tc.class)
		h.wait(t)
		if p.cardText[0] != tc.title || (tc.text != "" && p.cardText[1] != tc.text) || p.table.Visible() || p.copyBtn.Visible() {
			t.Errorf("%s: card %q table %v copy %v", tc.class, p.cardText, p.table.Visible(), p.copyBtn.Visible())
		}
		if p.name.Text != tc.class || p.refresh.Disabled() {
			t.Errorf("%s: head %q, refresh disabled %v", tc.class, p.name.Text, p.refresh.Disabled())
		}
	}
	// A good class after an error clears the message.
	p.choose(t, h, "AMT_GeneralSettings")
	h.wait(t)
	if p.cardText[0] != "" || !p.table.Visible() {
		t.Errorf("after an error: card %q table %v", p.cardText, p.table.Visible())
	}
}

func TestWSManLateResultOfAnotherClassIsDropped(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.choose(t, h, "CIM_SoftwareIdentity")
	p.choose(t, h, "AMT_GeneralSettings") // before the first read ended
	h.wait(t)
	h.wait(t)
	if p.name.Text != "AMT_GeneralSettings" || p.number.Text != "· 1 instance" || p.res.Class != "AMT_GeneralSettings" {
		t.Errorf("shows %q %q %q", p.name.Text, p.number.Text, p.res.Class)
	}
}

func TestWSManSearchFiltersTheClassList(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.choose(t, h, "AMT_GeneralSettings")
	h.wait(t)
	p.search.SetText("generalset")
	p.search.Entry.OnChanged("generalset")
	if len(p.items) != 2 || p.items[0].text != "AMT · 1" || p.items[1].text != "AMT_GeneralSettings" {
		t.Fatalf("items %+v", p.items)
	}
	if p.selID != 1 || p.name.Text != "AMT_GeneralSettings" || !p.table.Visible() {
		t.Errorf("the chosen class lost: selID %d name %q", p.selID, p.name.Text)
	}
	p.search.Entry.OnChanged("no such class")
	if len(p.items) != 0 || p.selID != -1 || p.name.Text != "AMT_GeneralSettings" {
		t.Errorf("no match: %d items, selID %d", len(p.items), p.selID)
	}
	p.search.Entry.OnChanged("")
	if len(p.items) != len(amt.WSManClasses())+3 || p.selID < 0 || p.items[p.selID].text != "AMT_GeneralSettings" {
		t.Errorf("cleared: %d items, selID %d", len(p.items), p.selID)
	}
	if got := len(h.dev.WSMan().Read()); got != 1 {
		t.Errorf("filtering read %d classes", got)
	}
}

func TestWSManHeadingRowDoesNotSelect(t *testing.T) {
	h, p := wsmanHarness(t, wsmanFake())
	p.list.Select(0)
	if p.selected != "" || len(h.dev.WSMan().Read()) != 0 {
		t.Error("a group heading was read as a class")
	}
}

func TestWSManValueIsCutButKeepsMeaning(t *testing.T) {
	long := strings.Repeat("a", wsmanValueMaxLen+50)
	got := wsmanValue("X", amt.WSManProp{Name: "N", Value: long})
	if r := []rune(got); len(r) != wsmanValueMaxLen+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("%d runes", len(r))
	}
	if got := wsmanValue("AMT_GeneralSettings", amt.WSManProp{Name: "PowerSource", Value: "1"}); got != "1 (DC)" {
		t.Errorf("got %q", got)
	}
}

func TestWSManWindowHeadAndNavigation(t *testing.T) {
	h, _ := wsmanHarness(t, wsmanFake())
	w := h.m.win
	if w.navHeading == nil || w.navHeading.Text != "Diagnostics" {
		t.Fatal("no Diagnostics heading in the navigation")
	}
	i := wsmanPageIndex(t, h.m)
	if !w.nav[i].Active() || w.title.Text != "WSMAN Browser" {
		t.Errorf("active %v title %q", w.nav[i].Active(), w.title.Text)
	}
	if len(w.heading.Objects) != 3 || w.heading.Objects[1] == nil {
		t.Errorf("heading holds title, pill and count: %d objects", len(w.heading.Objects))
	}
	if w.tools.Objects == nil {
		t.Error("Copy as XML and Refresh are missing in the head")
	}
}

func TestWSManSidebarOffersNoTab(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, wsmanFake())
	h.m.Sidebar()
	h.wait(t)
	if got := h.m.side.tabs.Selected(); h.m.pages[got].section != sectionMain {
		t.Errorf("selected tab %d", got)
	}
	i := wsmanPageIndex(t, h.m)
	if h.m.side.view.panes[i] != nil {
		t.Error("the sidebar built the window-only page")
	}
}
