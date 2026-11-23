package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

var (
	schemeDesktopS0   = amt.PowerScheme{ID: "SCHEME 0", Description: "Desktop: ON in S0"}
	schemeDesktopS345 = amt.PowerScheme{ID: "SCHEME 1", Description: "Desktop: ON in S0, ME Wake in S3, S4-5", KeepsRemoteStart: true}
	schemeDesktopS3   = amt.PowerScheme{ID: "SCHEME 2", Description: "Desktop: ON in S0, ME Wake in S3"}
	schemeAllWake     = amt.PowerScheme{ID: "SCHEME 3", Description: "Desktop: ON in S0, ME Wake in S0-S5", KeepsRemoteStart: true}
)

func examplePolicy() amt.PowerPolicy {
	return amt.PowerPolicy{Current: "SCHEME 1", IdleTimeoutMinutes: 1,
		Schemes: []amt.PowerScheme{schemeDesktopS0, schemeDesktopS345, schemeDesktopS3, schemeAllWake}}
}

func exampleAlarms() []amt.Alarm {
	at := func(m time.Month, d, hh int) time.Time { return time.Date(2026, m, d, hh, 0, 0, 0, time.UTC) }
	return []amt.Alarm{
		{Name: "Nightly backup", Start: at(11, 24, 2), Interval: 24 * time.Hour},
		{Name: "Patch day", Start: at(12, 8, 22), Interval: 28 * 24 * time.Hour},
		{Name: "Maintenance window", Start: at(11, 28, 6), DeleteOnCompletion: true},
	}
}

// powerHarness opens the Power page in the sidebar on a fake with the example policy, alarms and
// power states (sleep supported, hibernate not).
func powerHarness(t *testing.T, mod func(*session.FakePower)) (*manageHarness, *powerPane) {
	t.Helper()
	englishUI(t)
	dev := &session.Fake{FP: fp, States: []power.PowerState{2, 4, 5, 8, 10, 11}}
	st := dev.PowerPage()
	st.Policy, st.Alarms = examplePolicy(), exampleAlarms()
	if mod != nil {
		mod(st)
	}
	h := newManageHarness(t, dev)
	h.m.deps.host = riskHost
	h.m.Sidebar()
	h.wait(t) // hardware
	i := powerPageIndex(t, h.m)
	h.m.side.view.show(i)
	for range 3 {
		h.wait(t) // policy, alarms, power states
	}
	return h, h.m.side.view.panes[i].impl.(*powerPane)
}

func powerPageIndex(t *testing.T, m *manager) int {
	t.Helper()
	for i, p := range m.pages {
		if p.title == "Power" && p.section == sectionMore {
			return i
		}
	}
	t.Fatal("no Power page in the More section")
	return -1
}

func TestPowerPageIsFirstMorePage(t *testing.T) {
	englishUI(t)
	var more []managePageDef
	for _, d := range managePageDefs {
		if d.section == sectionMore {
			more = append(more, d)
		}
	}
	found := false
	for _, d := range more {
		if tab, title := d.names(); tab == "Power" && title == "Power" && d.order == 10 {
			found = true
		}
	}
	if !found {
		t.Fatal("Power is not registered in More with order 10")
	}
}

func TestPowerPageAlarmTable(t *testing.T) {
	_, p := powerHarness(t, nil)
	if len(p.table.lines) != 3 {
		t.Fatalf("%d rows", len(p.table.lines))
	}
	want := [][3]string{
		{"Nightly backup", "Nov 24 02:00", "Every day"},
		{"Patch day", "Dec 08 22:00", "Every 28 days"},
		{"Maintenance window", "Nov 28 06:00", "Once, then deleted"},
	}
	for i, w := range want {
		l := p.table.lines[i]
		got := [3]string{richText(l.cells[0]), richText(l.cells[1]), richText(l.cells[2])}
		if got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
		if l.action.Text != "Delete…" {
			t.Errorf("row %d button %q", i, l.action.Text)
		}
	}
	if p.add.Text != "+ Add Alarm" || !p.add.Visible() {
		t.Errorf("add button %q", p.add.Text)
	}
	if got := richText(p.note); got != "Times in this computer’s time zone (UTC, UTC+00:00). Intel AMT stores them in UTC." {
		t.Errorf("zone note %q", got)
	}
}

func TestPowerPageWindowShowsFullDates(t *testing.T) {
	h, _ := powerHarness(t, nil)
	h.m.OpenWindow()
	w := h.m.win
	i := powerPageIndex(t, h.m)
	w.showPage(i)
	for h.m.power.policy.loading || h.m.power.alarms.loading {
		h.wait(t)
	}
	p := w.view.panes[i].impl.(*powerPane)
	if got := richText(p.table.lines[0].cells[1]); got != "Nov 24, 2026 02:00" {
		t.Errorf("window date %q", got)
	}
	if w.view.panes[i].tools != p.list.refresh {
		t.Errorf("the page head holds %v, want Refresh", w.view.panes[i].tools)
	}
}

func TestRepeatText(t *testing.T) {
	englishUI(t)
	h := time.Hour
	for _, c := range []struct {
		a    amt.Alarm
		want string
	}{
		{amt.Alarm{}, "Once"},
		{amt.Alarm{DeleteOnCompletion: true}, "Once, then deleted"},
		{amt.Alarm{Interval: 24 * h}, "Every day"},
		{amt.Alarm{Interval: 7 * 24 * h}, "Every week"},
		{amt.Alarm{Interval: h}, "Every hour"},
		{amt.Alarm{Interval: time.Minute}, "Every minute"},
		{amt.Alarm{Interval: 28 * 24 * h}, "Every 28 days"},
		{amt.Alarm{Interval: 12 * h}, "Every 12 hours"},
		{amt.Alarm{Interval: 90 * time.Minute}, "Every 90 minutes"},
		{amt.Alarm{Interval: 25 * h}, "Every 25 hours"},
	} {
		if got := repeatText(c.a); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
}

func TestZoneNote(t *testing.T) {
	englishUI(t)
	for _, c := range []struct {
		zone string
		off  int
		want string
	}{
		{"CET", 3600, "(CET, UTC+01:00)"}, {"IST", 19800, "(IST, UTC+05:30)"}, {"EST", -5 * 3600, "(EST, UTC-05:00)"},
	} {
		if got := zoneNote(c.zone, c.off); !strings.Contains(got, c.want) {
			t.Errorf("%q does not contain %q", got, c.want)
		}
	}
}

func TestPowerPageEmptyState(t *testing.T) {
	_, p := powerHarness(t, func(s *session.FakePower) { s.Alarms = nil })
	if p.list.empty == nil || !p.list.empty.Visible() || p.table.Visible() {
		t.Fatal("the empty card must replace the table")
	}
	var found bool
	walk(p.list.empty, func(o fyne.CanvasObject) {
		if c, ok := o.(*canvas.Text); ok && c.Text == "No wake alarms" {
			found = true
		}
	})
	if !found {
		t.Error("empty card lacks its title")
	}
}

func TestPowerPolicyCard(t *testing.T) {
	_, p := powerHarness(t, nil)
	if p.pkg.Selected != "Desktop: ON in S0, ME Wake in S3, S4-5" || len(p.pkg.Options) != 4 {
		t.Errorf("package %q of %v", p.pkg.Selected, p.pkg.Options)
	}
	if p.idleText.Text != "1 minute" || p.idleBtn.Text != "Change…" || p.idleBtn.Disabled() {
		t.Errorf("idle %q button %q disabled %v", p.idleText.Text, p.idleBtn.Text, p.idleBtn.Disabled())
	}
	if p.statesTxt.Text != "Sleep supported · Hibernate not supported" {
		t.Errorf("states %q", p.statesTxt.Text)
	}
	if p.pkgNone.Visible() || !p.pkg.Visible() {
		t.Error("package choice hidden")
	}
}

func TestPowerPolicyCardTexts(t *testing.T) {
	englishUI(t)
	if got := supportText([]power.PowerState{2, 4, 7}); got != "Sleep supported · Hibernate supported" {
		t.Errorf("both: %q", got)
	}
	if got := supportText(nil); got != "Sleep not supported · Hibernate not supported" {
		t.Errorf("none: %q", got)
	}
	if idleTimeoutText(1) != "1 minute" || idleTimeoutText(30) != "30 minutes" {
		t.Error("idle text")
	}
}

func TestPowerPolicyWithoutPackages(t *testing.T) {
	_, p := powerHarness(t, func(s *session.FakePower) { s.Policy.Schemes, s.Policy.Current = nil, "" })
	if !p.pkgNone.Visible() || p.pkg.Visible() || p.pkgNone.Text != "Lab reports no power packages." {
		t.Errorf("no-packages text %q visible %v", p.pkgNone.Text, p.pkgNone.Visible())
	}
}

func TestPowerPageShowsReadErrors(t *testing.T) {
	_, p := powerHarness(t, func(s *session.FakePower) { s.Err = amt.ErrAccessDenied })
	if !p.err.box.Visible() || len(p.table.lines) != 0 {
		t.Fatalf("error visible %v, %d rows", p.err.box.Visible(), len(p.table.lines))
	}
}

func TestPowerPageRefreshReadsAgain(t *testing.T) {
	h, p := powerHarness(t, nil)
	h.dev.PowerPage().Lock()
	h.dev.PowerPage().Alarms = h.dev.PowerPage().Alarms[:1]
	h.dev.PowerPage().Unlock()
	test.Tap(p.list.refresh)
	for range 3 {
		h.wait(t)
	}
	if len(p.table.lines) != 1 {
		t.Fatalf("%d rows after Refresh", len(p.table.lines))
	}
}

// Delete: a simple confirmation; Cancel writes nothing, Delete removes the alarm and reads again.
func TestDeleteAlarmSimpleConfirmation(t *testing.T) {
	h, p := powerHarness(t, nil)
	test.Tap(p.table.lines[1].action) // Patch day
	if p.deleteDlg == nil || !open(h.m.parent) {
		t.Fatal("no confirmation")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Delete “Patch day”?")
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Lab no longer powers on at this time.")
	bs := panelButtons(p.deleteDlg.Content)
	if texts(bs) != "Cancel|Delete" {
		t.Fatalf("buttons %q", texts(bs))
	}
	test.Tap(bs[0])
	if open(h.m.parent) || len(h.dev.PowerPage().Written()) != 0 {
		t.Fatal("Cancel wrote or stayed open")
	}
	test.Tap(p.table.lines[1].action)
	bs = panelButtons(p.deleteDlg.Content)
	if bs[1].Importance == bs[0].Importance {
		t.Error("the Delete button is not the red one")
	}
	test.Tap(bs[1])
	h.wait(t)
	h.wait(t)
	if got := h.dev.PowerPage().Written(); !slices.Equal(got, []string{"DeleteAlarm"}) {
		t.Fatalf("writes %v", got)
	}
	if len(p.table.lines) != 2 || richText(p.table.lines[0].cells[0]) == "Patch day" || richText(p.table.lines[1].cells[0]) == "Patch day" {
		t.Errorf("list not reloaded: %d rows", len(p.table.lines))
	}
}

func TestDeleteAlarmErrorUsesTheErrorDialog(t *testing.T) {
	h, p := powerHarness(t, nil)
	h.dev.PowerPage().Lock()
	h.dev.PowerPage().Err = errors.New("example failure")
	h.dev.PowerPage().Unlock()
	test.Tap(p.table.lines[0].action)
	test.Tap(panelButtons(p.deleteDlg.Content)[1])
	h.wait(t)
	h.wait(t)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Something went wrong")
}

// the future, far beyond the real clock the fake checks against
const farDate = "2099-06-01"

func fill(d *alarmDialog, name, date, clock string) {
	d.name.SetText(name)
	d.date.SetText(date)
	d.clock.SetText(clock)
}

func TestAddAlarmDialogDefaultsAndButton(t *testing.T) {
	h, p := powerHarness(t, nil)
	test.Tap(p.add)
	d := p.addDlg
	if d == nil || !open(h.m.parent) {
		t.Fatal("no dialog")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Add wake alarm")
	if d.date.Text != "2026-11-12" || d.clock.Text != "02:00" || d.repeat.Selected != "Never" || d.name.Text != "" {
		t.Errorf("defaults %q %q %q %q", d.name.Text, d.date.Text, d.clock.Text, d.repeat.Selected)
	}
	if got := d.repeat.Options; !slices.Equal(got, []string{"Never", "Every day", "Every week", "Custom"}) {
		t.Errorf("repeat options %v", got)
	}
	if !d.action.Disabled() || d.action.Text != "Add Alarm" {
		t.Errorf("button %q disabled %v with an empty name", d.action.Text, d.action.Disabled())
	}
	if d.custom.Visible() || d.del.Disabled() {
		t.Error("custom row shown or delete switch locked for a single alarm")
	}
	d.name.SetText("Nightly backup")
	if d.action.Disabled() {
		t.Error("button disabled with a name")
	}
	for _, bad := range []struct{ date, clock string }{{"24.11.2026", "02:00"}, {"2026-11-24", "25:00"}, {"2026-13-01", "02:00"}, {"", "02:00"}, {"2026-11-24", ""}} {
		d.date.SetText(bad.date)
		d.clock.SetText(bad.clock)
		if !d.action.Disabled() {
			t.Errorf("button enabled for %q %q", bad.date, bad.clock)
		}
	}
	d.Dismiss()
}

func TestAddAlarmNameLimit(t *testing.T) {
	_, p := powerHarness(t, nil)
	test.Tap(p.add)
	d := p.addDlg
	d.name.SetText(strings.Repeat("ä", 40))
	if n := len([]rune(d.name.Text)); n != amt.MaxAlarmName {
		t.Errorf("name keeps %d characters", n)
	}
	d.Dismiss()
}

func TestAddAlarmSendsUTCFromLocalTime(t *testing.T) {
	h, p := powerHarness(t, nil)
	test.Tap(p.add)
	d := p.addDlg
	fill(d, "  Weekly job ", farDate, "08:30")
	d.repeat.SetSelected("Every week")
	if !d.del.Disabled() {
		t.Error("the delete switch must be locked for a repeating alarm")
	}
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if open(h.m.parent) {
		t.Error("the dialog stays open after success")
	}
	if got := h.dev.PowerPage().Written(); !slices.Equal(got, []string{"AddAlarm"}) {
		t.Fatalf("writes %v", got)
	}
	var got amt.Alarm
	for _, a := range h.dev.PowerPage().Alarms {
		if a.Name == "Weekly job" {
			got = a
		}
	}
	// the harness' local zone is UTC, so local and UTC time agree
	if got.Interval != 7*24*time.Hour || !got.Start.Equal(time.Date(2099, 6, 1, 8, 30, 0, 0, time.UTC)) || got.DeleteOnCompletion {
		t.Errorf("alarm %+v", got)
	}
	if len(p.table.lines) != 4 {
		t.Errorf("list has %d rows after the reload", len(p.table.lines))
	}
}

func TestAddAlarmUsesTheLocalZone(t *testing.T) {
	h, p := powerHarness(t, nil)
	zone := time.FixedZone("CET", 3600)
	h.m.now = func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, zone) }
	test.Tap(p.add)
	d := p.addDlg
	fill(d, "Zoned", farDate, "02:00")
	spec, ok := d.spec()
	if !ok || !spec.Start.Equal(time.Date(2099, 6, 1, 1, 0, 0, 0, time.UTC)) || spec.Start.Location() != time.UTC {
		t.Fatalf("spec %+v %v", spec, ok)
	}
	d.Dismiss()
}

func TestAddAlarmOnceAndDeleteAfterwards(t *testing.T) {
	h, p := powerHarness(t, nil)
	test.Tap(p.add)
	d := p.addDlg
	fill(d, "Once", farDate, "03:00")
	d.del.SetChecked(true)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	var got amt.Alarm
	for _, a := range h.dev.PowerPage().Alarms {
		if a.Name == "Once" {
			got = a
		}
	}
	if !got.DeleteOnCompletion || got.Interval != 0 {
		t.Errorf("alarm %+v", got)
	}
}

func TestAddAlarmCustomRepeat(t *testing.T) {
	_, p := powerHarness(t, nil)
	test.Tap(p.add)
	d := p.addDlg
	fill(d, "Custom", farDate, "03:00")
	d.repeat.SetSelected("Custom")
	if !d.custom.Visible() || !d.del.Disabled() {
		t.Fatal("custom row hidden or delete switch active")
	}
	for _, c := range []struct {
		n, unit string
		want    time.Duration
		ok      bool
	}{
		{"12", "hours", 12 * time.Hour, true},
		{"90", "minutes", 90 * time.Minute, true},
		{"3", "days", 72 * time.Hour, true},
		{"800", "days", 800 * 24 * time.Hour, true},
		{"801", "days", 0, false},
		{"0", "hours", 0, false},
		{"-1", "hours", 0, false},
		{"x", "hours", 0, false},
		{"", "days", 0, false},
	} {
		d.every.SetText(c.n)
		d.unit.SetSelected(c.unit)
		spec, ok := d.spec()
		if ok != c.ok || ok && spec.Interval != c.want {
			t.Errorf("%s %s: %v %v, want %v %v", c.n, c.unit, spec.Interval, ok, c.want, c.ok)
		}
		if d.action.Disabled() == c.ok {
			t.Errorf("%s %s: button disabled %v", c.n, c.unit, d.action.Disabled())
		}
	}
	d.repeat.SetSelected("Never")
	if d.custom.Visible() {
		t.Error("custom row stays after Never")
	}
	d.Dismiss()
}

func TestAddAlarmRefusalsStayInTheDialog(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*session.FakePower)
		date string
		in   string
		want string
	}{
		{"past time", nil, "2026-11-10", "Fresh", "Choose a time in the future."},
		{"duplicate name", nil, farDate, "Patch day", "A wake alarm with this name exists."},
		{"five alarms", func(s *session.FakePower) {
			s.Alarms = append(exampleAlarms(), amt.Alarm{Name: "Four"}, amt.Alarm{Name: "Five"})
		}, farDate, "Sixth", "Lab stores no more alarms. Delete one first."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, p := powerHarness(t, c.mod)
			test.Tap(p.add)
			d := p.addDlg
			fill(d, c.in, c.date, "02:00")
			test.Tap(d.action)
			if c.name != "past time" {
				h.wait(t)
				h.wait(t)
			}
			if !open(h.m.parent) || !d.err.box.Visible() || d.err.Text() != c.want {
				t.Fatalf("open %v error %v %q, want %q", open(h.m.parent), d.err.box.Visible(), d.err.Text(), c.want)
			}
			if got := h.dev.PowerPage().Written(); len(got) != 0 {
				t.Errorf("writes %v", got)
			}
			if d.action.Disabled() {
				t.Error("the button stays locked after the refusal")
			}
			d.name.SetText(c.in + "!") // typing again clears the line
			if d.err.box.Visible() {
				t.Error("error line stays while the user edits")
			}
			d.Dismiss()
		})
	}
}

func TestAlarmErrorTexts(t *testing.T) {
	englishUI(t)
	m := &manager{name: "Lab"}
	if got := m.alarmErrorText(&amt.ValidationError{Field: "Name", Reason: "x"}); got != "Enter a name of up to 32 characters." {
		t.Errorf("name: %q", got)
	}
	if got := m.alarmErrorText(errors.New("boom")); got == "" {
		t.Error("generic error has no text")
	}
}

// A refused write says "do this", not "read" (#107); the same holds for the settings page.
func TestAlarmErrorTextAccessDeniedIsWrite(t *testing.T) {
	englishUI(t)
	m := &manager{name: "Lab"}
	want := "This account is not allowed to do this on the device."
	if got := m.alarmErrorText(amt.ErrAccessDenied); got != want {
		t.Errorf("alarm: %q", got)
	}
	e := newManageError()
	e.setWrite(amt.ErrAccessDenied)
	if e.Text() != want {
		t.Errorf("setWrite: %q", e.Text())
	}
	e.set(amt.ErrAccessDenied)
	if e.Text() == want {
		t.Error("a read error uses the write wording")
	}
}

// Changing to a package that keeps the remote start needs no question.
func TestChangePackageSafeWritesAtOnce(t *testing.T) {
	h, p := powerHarness(t, nil)
	p.pkg.SetSelected(schemeAllWake.Description)
	if open(h.m.parent) {
		t.Fatal("a safe change asked")
	}
	h.wait(t)
	h.wait(t)
	if got := h.dev.PowerPage().Written(); !slices.Equal(got, []string{"SetPowerScheme"}) {
		t.Fatalf("writes %v", got)
	}
	if h.dev.PowerPage().Policy.Current != "SCHEME 3" || p.pkg.Selected != schemeAllWake.Description {
		t.Errorf("current %q shown %q", h.dev.PowerPage().Policy.Current, p.pkg.Selected)
	}
}

// Changing to a package that turns AMT off in S4-S5 is orange: warning, old and new package, the
// checkbox for the device and a red button.
func TestChangePackageRiskyIsOrange(t *testing.T) {
	h, p := powerHarness(t, nil)
	p.pkg.SetSelected(schemeDesktopS0.Description)
	d := p.policyDlg
	if d == nil || !open(h.m.parent) {
		t.Fatal("no confirmation")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Change the power package?")
	if !hasRichText(t, top, "With this package, Intel AMT is off while Lab sleeps or is switched off. You cannot power it on remotely until someone switches it on at the device.", false) {
		t.Error("warning text missing")
	}
	if !hasRichText(t, top, "Desktop: ON in S0, ME Wake in S3, S4-5 → ", false) || !hasRichText(t, top, "Desktop: ON in S0", true) {
		t.Error("old → new line missing")
	}
	if d.check.Text != "I understand the risk for Lab (device.example.com)" && !strings.HasPrefix(d.check.Text, "I understand the risk for ") {
		t.Errorf("checkbox %q", d.check.Text)
	}
	if d.action.Text != "Change Package" || !d.action.Disabled() {
		t.Fatalf("button %q disabled %v", d.action.Text, d.action.Disabled())
	}
	d.submit() // Enter never confirms an orange dialog
	if len(h.dev.PowerPage().Written()) != 0 {
		t.Fatal("Enter confirmed")
	}
	test.Tap(d.check)
	if d.action.Disabled() {
		t.Fatal("button locked after the checkbox")
	}
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.PowerPage().Written(); !slices.Equal(got, []string{"SetPowerScheme"}) || h.dev.PowerPage().Policy.Current != "SCHEME 0" {
		t.Fatalf("writes %v current %q", got, h.dev.PowerPage().Policy.Current)
	}
}

func TestChangePackageCancelRestoresTheChoice(t *testing.T) {
	h, p := powerHarness(t, nil)
	p.pkg.SetSelected(schemeDesktopS3.Description)
	if p.policyDlg == nil {
		t.Fatal("no confirmation")
	}
	p.policyDlg.Dismiss()
	if p.pkg.Selected != schemeDesktopS345.Description || len(h.dev.PowerPage().Written()) != 0 {
		t.Errorf("shown %q writes %v", p.pkg.Selected, h.dev.PowerPage().Written())
	}
}

// Without the confirmation the session refuses; the page shows the error and the device's package.
func TestChangePackageWriteErrorUsesTheErrorDialog(t *testing.T) {
	h, p := powerHarness(t, nil)
	h.dev.PowerPage().Lock()
	h.dev.PowerPage().Err = errors.New("example failure")
	h.dev.PowerPage().Unlock()
	p.pkg.SetSelected(schemeAllWake.Description)
	h.wait(t)
	h.wait(t)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Something went wrong")
}

func TestIdleTimeoutDialog(t *testing.T) {
	h, p := powerHarness(t, nil)
	test.Tap(p.idleBtn)
	d := p.idleDlg
	if d == nil || !open(h.m.parent) || d.field.Text != "1" || !d.action.Disabled() {
		t.Fatal("dialog missing, wrong value or Save enabled for the unchanged value")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Idle timeout")
	for _, bad := range []string{"0", "65536", "-3", "x", "", "1.5"} {
		d.field.SetText(bad)
		if !d.action.Disabled() {
			t.Errorf("Save enabled for %q", bad)
		}
	}
	for _, ok := range []string{"2", "65535", " 30 "} {
		d.field.SetText(ok)
		if d.action.Disabled() {
			t.Errorf("Save disabled for %q", ok)
		}
	}
	d.field.SetText("30")
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.PowerPage().Written(); !slices.Equal(got, []string{"SetIdleTimeout"}) || h.dev.PowerPage().Policy.IdleTimeoutMinutes != 30 {
		t.Fatalf("writes %v idle %d", got, h.dev.PowerPage().Policy.IdleTimeoutMinutes)
	}
	if p.idleText.Text != "30 minutes" {
		t.Errorf("shown %q", p.idleText.Text)
	}
}

func TestIdleTimeoutCancelWritesNothing(t *testing.T) {
	h, p := powerHarness(t, nil)
	test.Tap(p.idleBtn)
	p.idleDlg.field.SetText("30")
	p.idleDlg.Dismiss()
	if len(h.dev.PowerPage().Written()) != 0 {
		t.Fatal("cancel wrote")
	}
}

func TestParseIdle(t *testing.T) {
	for in, want := range map[string]int{"1": 1, "65535": 65535, " 7 ": 7} {
		if n, ok := parseIdle(in); !ok || n != want {
			t.Errorf("%q: %d %v", in, n, ok)
		}
	}
	for _, in := range []string{"", "0", "65536", "a", "1e3"} {
		if _, ok := parseIdle(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestPowerSchemeFlashLimitText(t *testing.T) {
	englishUI(t)
	got := describe(&amt.MethodError{Method: "SetPowerScheme", ReturnValue: 38})
	if !strings.Contains(got, "flash write limit") {
		t.Errorf("text %q", got)
	}
	if other := describe(&amt.MethodError{Method: "SetPowerScheme", ReturnValue: 1}); strings.Contains(other, "flash") {
		t.Errorf("other value: %q", other)
	}
}
