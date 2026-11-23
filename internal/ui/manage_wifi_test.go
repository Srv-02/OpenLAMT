package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

const (
	wifiOffice   = "Intel(r) AMT:WiFi Endpoint Settings Office"
	wifiWorkshop = "Intel(r) AMT:WiFi Endpoint Settings Workshop"
	wifiCorp     = "Intel(r) AMT:WiFi Endpoint Settings Corp"
	wifiKey      = "correct-horse-battery"
)

// exampleWiFi is the design's state: on in S0 and Sx, sync on, UEFI sharing off, the wireless
// port at 192.0.2.61 and the profiles Office (WPA2) and Workshop (WPA3).
func exampleWiFi() amt.WiFiSettings {
	return amt.WiFiSettings{Available: true, State: amt.WiFiS0Sx, SyncKnown: true, Sync: true, UEFIShareKnown: true,
		LinkUp: true, IPAddress: "192.0.2.61",
		Profiles: []amt.WiFiProfile{
			{InstanceID: wifiOffice, Name: "Office", SSID: "ACME-Lab", Priority: 1, Security: amt.WiFiWPA2Personal},
			{InstanceID: wifiWorkshop, Name: "Workshop", SSID: "ACME-Workshop", Priority: 2, Security: amt.WiFiWPA3Personal},
		},
		Securities: slices.Clone(amt.WiFiSecurities),
	}
}

// wifiPageDef is the registered Wi-Fi page.
func wifiPageDef(t *testing.T) managePageDef {
	t.Helper()
	for _, d := range managePageDefs {
		if d.section == sectionMore && d.order == 20 {
			return d
		}
	}
	t.Fatal("the Wi-Fi page is not registered")
	return managePageDef{}
}

// wifiHarness opens the Wi-Fi page (the only More page) in the sidebar on a fake with st.
func wifiHarness(t *testing.T, st amt.WiFiSettings, useTLS bool) (*manageHarness, *wifiPane) {
	t.Helper()
	englishUI(t)
	withManagePages(t, wifiPageDef(t))
	dev := &session.Fake{FP: fp}
	dev.WiFiFake().State = st
	h := newManageHarnessTLS(t, dev, useTLS)
	h.m.Sidebar()
	h.wait(t) // hardware
	side := h.m.side
	side.tabs.Tap(side.more)
	side.morePopup.Item(0).Tapped(nil)
	h.wait(t) // Wi-Fi
	return h, side.view.panes[side.more].impl.(*wifiPane)
}

// wifiWritten applies the result of a write and the read after it.
func (h *manageHarness) wifiWritten(t *testing.T) {
	t.Helper()
	h.wait(t) // write
	h.wait(t) // read again
}

func wifiMethods(dev *session.Fake) []string {
	var out []string
	for _, c := range dev.WiFiCalls() {
		out = append(out, c.Method)
	}
	return out
}

func TestWiFiPageIsRegisteredInMore(t *testing.T) {
	englishUI(t)
	d := wifiPageDef(t)
	tab, title := d.names()
	if tab != "Wi-Fi" || title != "Wi-Fi" || d.icon != icons.WiFi {
		t.Errorf("tab %q title %q icon %q", tab, title, d.icon)
	}
}

func TestWiFiPageShowsStateAndProfiles(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), true)
	if !p.cardBox.Visible() || !p.list.table.Visible() || p.empty.Visible() || !p.add.Visible() {
		t.Fatal("card, table and Add must show, the empty state not")
	}
	if p.state.Selected() != 2 || p.state.Label(0) != "Off" || p.state.Label(1) != "While on" || p.state.Label(2) != "Always" {
		t.Errorf("state segment %d", p.state.Selected())
	}
	if !p.sync.Checked() || p.uefi.Checked() || !p.syncRow.visibleRow() || !p.uefiRow.visibleRow() {
		t.Error("sync on, UEFI off, both rows shown")
	}
	if got := richText(p.connText); got != "192.0.2.61" {
		t.Errorf("connection %q", got)
	}
	want := [][]string{{"1", "Office", "ACME-Lab", "WPA2-Personal (AES)"}, {"2", "Workshop", "ACME-Workshop", "WPA3-Personal"}}
	if len(p.list.table.lines) != 2 {
		t.Fatalf("%d rows", len(p.list.table.lines))
	}
	for i, l := range p.list.table.lines {
		for j, c := range l.cells {
			if got := richText(c); got != want[i][j] {
				t.Errorf("row %d col %d = %q, want %q", i, j, got, want[i][j])
			}
		}
		if l.action.Text != "Edit" || l.action.Disabled() {
			t.Errorf("row %d: Edit %q disabled %v", i, l.action.Text, l.action.Disabled())
		}
	}
	if got := richText(p.footnote); got != "Lower number = tried first. Profiles from the operating system are not listed." || !p.footnoteBox.Visible() {
		t.Errorf("footnote %q", got)
	}
	if p.infoState.Content.Title != "Wi-Fi in Intel AMT" || p.infoState.Content.Recover == "" || p.infoSync.Content.What == "" || p.infoUEFI.Content.When == "" {
		t.Error("help texts")
	}

	h.m.OpenWindow()
	w := h.m.win
	i := slices.IndexFunc(h.m.pages, func(pg managePage) bool { return pg.title == "Wi-Fi" })
	test.Tap(w.nav[i])
	wp := w.view.panes[i].impl.(*wifiPane)
	if wp.count.Text != "· 2 profiles" || w.title.Text != "Wi-Fi" {
		t.Errorf("window head %q %q", w.title.Text, wp.count.Text)
	}
}

func TestWiFiPageHidesWhatTheFirmwareLacks(t *testing.T) {
	st := exampleWiFi()
	st.UEFIShareKnown, st.LinkUp = false, false
	_, p := wifiHarness(t, st, true)
	if !p.syncRow.visibleRow() || p.uefiRow.visibleRow() {
		t.Error("the UEFI row must hide on firmware without it")
	}
	if got := richText(p.connText); got != "Not connected" {
		t.Errorf("connection %q", got)
	}
}

func TestWiFiPageWithoutAdapter(t *testing.T) {
	h, p := wifiHarness(t, amt.WiFiSettings{}, true)
	if !p.empty.Visible() || p.cardBox.Visible() || p.list.table.Visible() || p.add.Visible() || p.footnoteBox.Visible() {
		t.Fatal("only the empty state may show")
	}
	if !hasRichText(t, p.empty, "Lab has no Wi-Fi adapter that Intel AMT can use.", false) {
		t.Error("empty text")
	}
	found := false
	walk(p.empty, func(o fyne.CanvasObject) {
		if c, ok := o.(*canvas.Text); ok && c.Text == "No Wi-Fi in Intel AMT" {
			found = true
		}
	})
	if !found {
		t.Error("empty title")
	}
	if p.list.refresh.Disabled() {
		t.Error("Refresh stays available")
	}
	_ = h
}

// The design: Add and Edit share the dialog; a key over a session without TLS asks first (V18).
func TestWiFiAddProfileAsksBeforeSendingTheKeyWithoutTLS(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), false)
	test.Tap(p.add)
	d := p.profileDlg
	if d == nil || !open(h.m.parent) || d.edit != nil || d.del != nil {
		t.Fatal("no Add dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Add Wi-Fi profile")
	if !hasRichText(t, top, "8–63 characters.", false) {
		t.Error("password hint")
	}
	if d.priority.Text != "3" || d.security.Selected != "WPA2-Personal (AES)" || d.name.Disabled() {
		t.Errorf("priority %q security %q", d.priority.Text, d.security.Selected)
	}
	if !slices.Equal(d.security.Options, []string{"WPA-Personal (TKIP)", "WPA2-Personal (AES)", "WPA3-Personal"}) {
		t.Errorf("options %v", d.security.Options)
	}
	if !d.action.Disabled() || d.action.Text != "Add Profile" {
		t.Fatalf("button %q enabled before the values", d.action.Text)
	}
	d.name.SetText("Lab")
	d.ssid.SetText("ACME-Lab2")
	d.security.SetSelected("WPA3-Personal")
	d.pw.SetText(wifiKey)
	if !d.action.Disabled() {
		t.Fatal("enabled with the password typed once")
	}
	d.repeat.SetText(wifiKey)
	if d.action.Disabled() {
		t.Fatal("disabled with valid values")
	}
	test.Tap(d.action)
	if d.pw.Text() != "" || d.repeat.Text() != "" {
		t.Error("the dialog keeps the password")
	}
	ask := h.m.tlsAsk
	if ask == nil || len(h.dev.WiFiCalls()) != 0 {
		t.Fatalf("no TLS question, or a write before it: %v", wifiMethods(h.dev))
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Send the key without TLS?")
	test.Tap(ask.action)
	h.wifiWritten(t)
	calls := h.dev.WiFiCalls()
	if len(calls) != 1 || calls[0].Method != "AddWiFiProfile" {
		t.Fatalf("calls %v", wifiMethods(h.dev))
	}
	want := amt.WiFiProfileSpec{Name: "Lab", SSID: "ACME-Lab2", Priority: 3, Security: amt.WiFiWPA3Personal, Passphrase: wifiKey}
	if calls[0].Spec != want {
		t.Errorf("spec %+v", calls[0].Spec)
	}
	if len(p.list.table.lines) != 3 {
		t.Error("the list was not read again")
	}
}

func TestWiFiAddCancelOnTheTLSQuestionWritesNothing(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), false)
	test.Tap(p.add)
	d := p.profileDlg
	d.name.SetText("Lab")
	d.ssid.SetText("ACME-Lab2")
	d.pw.SetText(wifiKey)
	d.repeat.SetText(wifiKey)
	test.Tap(d.action)
	h.m.tlsAsk.Dismiss()
	if open(h.m.parent) || len(h.dev.WiFiCalls()) != 0 {
		t.Fatalf("open %v calls %v", open(h.m.parent), wifiMethods(h.dev))
	}
}

func TestWiFiAddWithTLSWritesAtOnce(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), true)
	test.Tap(p.add)
	d := p.profileDlg
	d.name.SetText("Lab")
	d.ssid.SetText("ACME-Lab2")
	d.pw.SetText(wifiKey)
	d.repeat.SetText(wifiKey)
	test.Tap(d.action)
	if h.m.tlsAsk != nil {
		t.Fatal("TLS question with TLS")
	}
	h.wifiWritten(t)
	if got := wifiMethods(h.dev); !slices.Equal(got, []string{"AddWiFiProfile"}) {
		t.Errorf("calls %v", got)
	}
}

func TestWiFiAddRejectsInvalidValues(t *testing.T) {
	_, p := wifiHarness(t, exampleWiFi(), true)
	test.Tap(p.add)
	d := p.profileDlg
	d.name.SetText("Lab")
	d.ssid.SetText("ACME-Lab2")
	d.pw.SetText(wifiKey)
	d.repeat.SetText(wifiKey)
	for name, set := range map[string]func(){
		"short password": func() { d.pw.SetText("short"); d.repeat.SetText("short") },
		"priority 0":     func() { d.priority.SetText("0") },
		"priority text":  func() { d.priority.SetText("x") },
		"long SSID":      func() { d.ssid.SetText("0123456789012345678901234567890123") },
		"no name":        func() { d.name.SetText("") },
	} {
		set()
		if !d.action.Disabled() {
			t.Errorf("%s: enabled", name)
		}
		d.name.SetText("Lab")
		d.ssid.SetText("ACME-Lab2")
		d.priority.SetText("3")
		d.pw.SetText(wifiKey)
		d.repeat.SetText(wifiKey)
		if d.action.Disabled() {
			t.Fatalf("%s: not enabled again", name)
		}
	}
}

// Edit keeps the name and, with the password left empty, the current key: no TLS question.
func TestWiFiEditKeepsNameAndPassword(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), false)
	test.Tap(p.list.table.lines[0].action)
	d := p.profileDlg
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Edit Wi-Fi profile")
	if !hasRichText(t, top, "8–63 characters. Leave empty to keep the current password.", false) {
		t.Error("hint")
	}
	if d.name.Text != "Office" || !d.name.Disabled() || d.ssid.Text != "ACME-Lab" || d.priority.Text != "1" ||
		d.security.Selected != "WPA2-Personal (AES)" || d.pw.Entry.PlaceHolder != "Unchanged" || d.repeat.Entry.PlaceHolder != "Repeat password" {
		t.Errorf("fields: %q %v %q %q %q %q", d.name.Text, d.name.Disabled(), d.ssid.Text, d.priority.Text, d.security.Selected, d.pw.Entry.PlaceHolder)
	}
	if d.del == nil || d.del.Text != "Delete Profile…" || d.action.Text != "Save" {
		t.Fatal("Delete Profile… or Save missing")
	}
	if !d.action.Disabled() {
		t.Fatal("Save enabled without a change")
	}
	d.priority.SetText("4")
	test.Tap(d.action)
	if h.m.tlsAsk != nil {
		t.Fatal("TLS question without a key")
	}
	h.wifiWritten(t)
	calls := h.dev.WiFiCalls()
	want := amt.WiFiProfileSpec{Name: "Office", SSID: "ACME-Lab", Priority: 4, Security: amt.WiFiWPA2Personal}
	if len(calls) != 1 || calls[0].Method != "UpdateWiFiProfile" || calls[0].InstanceID != wifiOffice || calls[0].Spec != want {
		t.Fatalf("calls %+v", calls)
	}
}

func TestWiFiEditWithNewPasswordAsksWithoutTLS(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), false)
	test.Tap(p.list.table.lines[0].action)
	d := p.profileDlg
	d.pw.SetText(wifiKey)
	if !d.action.Disabled() {
		t.Fatal("enabled with the password typed once")
	}
	d.repeat.SetText(wifiKey)
	test.Tap(d.action)
	if h.m.tlsAsk == nil {
		t.Fatal("no TLS question")
	}
	test.Tap(h.m.tlsAsk.action)
	h.wifiWritten(t)
	if c := h.dev.WiFiCalls(); len(c) != 1 || c[0].Spec.Passphrase != wifiKey {
		t.Fatalf("calls %+v", c)
	}
}

// Security offers only what the device lists; a profile keeps its own security in the list.
func TestWiFiSecurityShowsWhatTheDeviceCan(t *testing.T) {
	st := exampleWiFi()
	st.Securities = []amt.WiFiSecurity{amt.WiFiWPA2Personal}
	_, p := wifiHarness(t, st, true)
	test.Tap(p.add)
	if got := p.profileDlg.security.Options; !slices.Equal(got, []string{"WPA2-Personal (AES)"}) {
		t.Errorf("Add options %v", got)
	}
	p.profileDlg.Dismiss()
	test.Tap(p.list.table.lines[1].action) // Workshop, WPA3
	if got := p.profileDlg.security.Options; !slices.Equal(got, []string{"WPA2-Personal (AES)", "WPA3-Personal"}) ||
		p.profileDlg.security.Selected != "WPA3-Personal" {
		t.Errorf("Edit options %v selected %q", got, p.profileDlg.security.Selected)
	}
}

// An 802.1X profile is listed as Other; Save needs a security of this dialog and a password.
func TestWiFiOtherSecurityNeedsNewSecurityAndPassword(t *testing.T) {
	st := exampleWiFi()
	st.Profiles = append(st.Profiles, amt.WiFiProfile{InstanceID: wifiCorp, Name: "Corp", SSID: "ACME-Corp", Priority: 3})
	h, p := wifiHarness(t, st, true)
	if got := richText(p.list.table.lines[2].cells[3]); got != "Other method" {
		t.Errorf("security %q", got)
	}
	test.Tap(p.list.table.lines[2].action)
	d := p.profileDlg
	if d.security.Selected != "Other method" {
		t.Errorf("selected %q", d.security.Selected)
	}
	d.priority.SetText("5")
	if !d.action.Disabled() {
		t.Fatal("Save with security Other")
	}
	d.security.SetSelected("WPA2-Personal (AES)")
	if !d.action.Disabled() {
		t.Fatal("Save without a password")
	}
	d.pw.SetText(wifiKey)
	d.repeat.SetText(wifiKey)
	if d.action.Disabled() {
		t.Fatal("Save stays disabled")
	}
	test.Tap(d.action)
	h.wifiWritten(t)
	if got := wifiMethods(h.dev); !slices.Equal(got, []string{"UpdateWiFiProfile"}) {
		t.Errorf("calls %v", got)
	}
}

// Deleting asks the simple question (amt.OpWiFiDelete) when the session does not run over Wi-Fi.
func TestWiFiDeleteAsksTheSimpleQuestion(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), true)
	test.Tap(p.list.table.lines[0].action)
	d := p.profileDlg
	test.Tap(d.del)
	q := p.deleteDlg
	if q == nil || p.lockDlg != nil || !q.spec.danger || q.spec.icon != icons.Delete {
		t.Fatal("no simple delete question")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete the profile “Office”?")
	findLabel(t, top, "Intel AMT no longer connects to ACME-Lab with this profile.")
	if q.action.Text != "Delete Profile" || len(h.dev.WiFiCalls()) != 0 {
		t.Fatalf("button %q, calls %v", q.action.Text, wifiMethods(h.dev))
	}
	test.Tap(q.action)
	if open(h.m.parent) {
		t.Error("the Edit dialog stays open")
	}
	h.wifiWritten(t)
	if c := h.dev.WiFiCalls(); len(c) != 1 || c[0].Method != "DeleteWiFiProfile" || c[0].InstanceID != wifiOffice {
		t.Fatalf("calls %+v", c)
	}
	if len(p.list.table.lines) != 1 {
		t.Error("the list was not read again")
	}
}

func TestWiFiDeleteCancelReturnsToEdit(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), true)
	test.Tap(p.list.table.lines[0].action)
	test.Tap(p.profileDlg.del)
	p.deleteDlg.Dismiss()
	if !open(h.m.parent) || p.profileDlg.done || len(h.dev.WiFiCalls()) != 0 {
		t.Fatal("Cancel must return to the Edit dialog without a write")
	}
}

// Over a Wi-Fi session the delete is red (amt.OpWiFiLockout): the button waits for the checkbox.
func TestWiFiDeleteOverWiFiIsRed(t *testing.T) {
	st := exampleWiFi()
	st.OverWiFi = true
	h, p := wifiHarness(t, st, true)
	test.Tap(p.list.table.lines[0].action)
	test.Tap(p.profileDlg.del)
	r := p.lockDlg
	if r == nil || p.deleteDlg != nil {
		t.Fatal("no red question")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete the profile “Office”?")
	if !hasRichText(t, top, "Risk of lockout. ", true) ||
		!hasRichText(t, top, "OpenLAMT reaches Lab over Wi-Fi right now. Without the profile, Lab can lose the connection and only a cable or the device itself brings it back.", false) {
		t.Error("warning text")
	}
	if r.check.Text != "I understand the risk for Lab (device.example.com)" || !r.action.Disabled() || r.action.Text != "Delete Profile" {
		t.Fatalf("check %q, button %q disabled %v", r.check.Text, r.action.Text, r.action.Disabled())
	}
	test.Tap(r.check)
	test.Tap(r.action)
	h.wifiWritten(t)
	if got := wifiMethods(h.dev); !slices.Equal(got, []string{"DeleteWiFiProfile"}) {
		t.Fatalf("calls %v (the fake refuses a missing lockout confirmation)", got)
	}
}

// Off over a Wi-Fi session asks the red question; the segment keeps the device's value until then.
func TestWiFiOffOverWiFiIsRed(t *testing.T) {
	st := exampleWiFi()
	st.OverWiFi = true
	h, p := wifiHarness(t, st, true)
	p.state.Tap(0)
	r := p.stateLockDlg
	if r == nil || p.state.Selected() != 2 || len(h.dev.WiFiCalls()) != 0 {
		t.Fatalf("question %v, segment %d, calls %v", r != nil, p.state.Selected(), wifiMethods(h.dev))
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Turn off Wi-Fi in Intel AMT?")
	if !hasRichText(t, top, "OpenLAMT reaches Lab over Wi-Fi right now. Without Wi-Fi, Lab loses the connection and only a cable or the device itself brings it back.", false) {
		t.Error("warning text")
	}
	if !r.action.Disabled() || r.action.Text != "Turn Off Wi-Fi" {
		t.Fatalf("button %q disabled %v", r.action.Text, r.action.Disabled())
	}
	test.Tap(r.check)
	test.Tap(r.action)
	h.wifiWritten(t)
	if c := h.dev.WiFiCalls(); len(c) != 1 || c[0].State != amt.WiFiOff {
		t.Fatalf("calls %+v", c)
	}
	if p.state.Selected() != 0 {
		t.Errorf("segment %d after the write", p.state.Selected())
	}
}

func TestWiFiStateWritesAtOnceWithoutLockout(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), true)
	p.state.Tap(1)
	if open(h.m.parent) {
		t.Fatal("a question for While on")
	}
	if !p.state.Disabled() {
		t.Error("the segment is not locked while writing")
	}
	h.wifiWritten(t)
	p.state.Tap(0)
	if open(h.m.parent) {
		t.Fatal("a question for Off without a Wi-Fi session")
	}
	h.wifiWritten(t)
	c := h.dev.WiFiCalls()
	if len(c) != 2 || c[0].State != amt.WiFiS0 || c[1].State != amt.WiFiOff {
		t.Fatalf("calls %+v", c)
	}
}

func TestWiFiSwitchesWrite(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), true)
	p.sync.SetChecked(false)
	h.wifiWritten(t)
	p.uefi.SetChecked(true)
	h.wifiWritten(t)
	c := h.dev.WiFiCalls()
	if len(c) != 2 || c[0].Method != "SetWiFiSync" || c[0].On || c[1].Method != "SetWiFiUEFIShare" || !c[1].On {
		t.Fatalf("calls %+v", c)
	}
	if p.sync.Checked() || !p.uefi.Checked() {
		t.Error("switches do not show the device's values")
	}
}

// A refusal of the device is shown as an error and the page reads the state again.
func TestWiFiRefusalIsShown(t *testing.T) {
	h, p := wifiHarness(t, exampleWiFi(), true)
	h.dev.WiFiFake().WriteErr = &amt.MethodError{Method: "AddWiFiSettings", ReturnValue: 1}
	test.Tap(p.add)
	d := p.profileDlg
	d.name.SetText("Lab")
	d.ssid.SetText("ACME-Lab2")
	d.security.SetSelected("WPA3-Personal")
	d.pw.SetText(wifiKey)
	d.repeat.SetText(wifiKey)
	test.Tap(d.action)
	h.wifiWritten(t)
	if !open(h.m.parent) {
		t.Fatal("no error dialog")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(),
		"The device does not support this Wi-Fi profile. Older Intel AMT versions have no WPA3-Personal; choose WPA2-Personal.")
}

func TestDescribeWiFiRefusals(t *testing.T) {
	englishUI(t)
	for _, tt := range []struct {
		err  *amt.MethodError
		want string
	}{
		{&amt.MethodError{Method: "UpdateWiFiSettings", ReturnValue: 1}, "The device does not support this Wi-Fi profile. Older Intel AMT versions have no WPA3-Personal; choose WPA2-Personal."},
		{&amt.MethodError{Method: "AddWiFiSettings", ReturnValue: 3}, "The device did not accept the Wi-Fi profile. Check the values; older Intel AMT versions have no WPA3-Personal."},
		{&amt.MethodError{Method: "CIM_WiFiPort.RequestStateChange", ReturnValue: 2}, "The device refused the request CIM_WiFiPort.RequestStateChange (return value 2)."},
	} {
		if got := describe(tt.err); got != tt.want {
			t.Errorf("%v: %q", tt.err, got)
		}
	}
}

func TestWiFiReadErrorIsShown(t *testing.T) {
	englishUI(t)
	withManagePages(t, wifiPageDef(t))
	dev := &session.Fake{FP: fp}
	dev.WiFiFake().ReadErr = amt.ErrAccessDenied
	h := newManageHarness(t, dev)
	h.m.Sidebar()
	h.wait(t)
	side := h.m.side
	side.tabs.Tap(side.more)
	side.morePopup.Item(0).Tapped(nil)
	h.wait(t)
	p := side.view.panes[side.more].impl.(*wifiPane)
	if got := p.list.err.Text(); got != "This account is not allowed to read this information on the device." {
		t.Errorf("error %q", got)
	}
	if p.cardBox.Visible() || p.empty.Visible() {
		t.Error("nothing but the error before a successful read")
	}
}
