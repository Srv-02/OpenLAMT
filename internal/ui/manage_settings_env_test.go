package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// envDialog opens "Change environment detection" on the Settings page.
func envDialog(t *testing.T, h *manageHarness, p *settingsPane) (*riskDialog, *envForm) {
	t.Helper()
	test.Tap(p.changeEnv)
	d, f := p.dialog, p.envForm
	if d == nil || f == nil || !open(h.m.parent) {
		t.Fatal("Change… did not open the environment dialog")
	}
	return d, f
}

// addDomain types s into "Add domain" and presses Enter.
func addDomain(f *envForm, s string) {
	f.input.SetText(s)
	f.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
}

func TestSettingsEnvironmentDialog(t *testing.T) {
	h, p := settingsHarness(t, nil)
	d, f := envDialog(t, h, p)
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Change environment detection")
	if !hasRichText(t, top, "Risk of lockout. ", true) ||
		!hasRichText(t, top, "Outside these domains, AMT closes its network access. Only the BIOS (MEBx) at the device can fix a wrong entry.", false) ||
		!same(d.warnBox.FillColor, style.Color(style.ColorNameDangerSurface)) {
		t.Error("red warning")
	}
	if !hasText(top, "Intranet domains") || f.input.PlaceHolder != "Add domain" || f.add.Text != "Add" || d.action.Text != "Save" {
		t.Errorf("subhead, placeholder %q, add %q, button %q", f.input.PlaceHolder, f.add.Text, d.action.Text)
	}
	if !slices.Equal(f.domains, []string{"example.com", "corp.example.com"}) || len(f.rows) != 2 || d.check.Text != "I understand the risk for Lab (device.example.com)" {
		t.Fatalf("domains %v rows %d checkbox %q", f.domains, len(f.rows), d.check.Text)
	}
	d.check.SetChecked(true)
	if !d.action.Disabled() {
		t.Fatal("Save enabled without a change")
	}
	addDomain(f, "lab.example.com")
	if !slices.Equal(f.domains, []string{"example.com", "corp.example.com", "lab.example.com"}) || f.input.Text != "" || len(f.rows) != 3 {
		t.Fatalf("Enter did not add: %v input %q", f.domains, f.input.Text)
	}
	if d.action.Disabled() || open(h.m.parent) == false {
		t.Fatal("Save disabled with a changed list and the address, or Enter closed the dialog")
	}
	d.check.SetChecked(false)
	if !d.action.Disabled() {
		t.Fatal("enabled without the ticked checkbox")
	}
	d.check.SetChecked(true)
	test.Tap(f.rows[0].remove)
	if !slices.Equal(f.domains, []string{"corp.example.com", "lab.example.com"}) || len(f.rows) != 2 {
		t.Fatalf("remove: %v", f.domains)
	}
	test.Tap(d.action)
	h.written(t)
	e := h.dev.Cfg.Settings.Environment
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetEnvironmentDetection"}) || !slices.Equal(e.Domains, []string{"corp.example.com", "lab.example.com"}) {
		t.Fatalf("writes %v domains %v (the fake checks the confirmation)", got, e.Domains)
	}
	if p.envValue() != "corp.example.com, lab.example.com" {
		t.Errorf("page %q", p.envValue())
	}
}

func TestSettingsEnvironmentRefusesBadEntries(t *testing.T) {
	h, p := settingsHarness(t, nil)
	_, f := envDialog(t, h, p)
	for _, bad := range []string{"", "  ", "Example.COM", "corp.example.com", "not a domain!", "-bad.example.com"} {
		f.input.SetText(bad)
		if !f.add.Disabled() {
			t.Errorf("Add enabled for %q", bad)
		}
		f.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		test.Tap(f.add)
		if len(f.domains) != 2 {
			t.Fatalf("%q taken: %v", bad, f.domains)
		}
	}
	f.input.SetText(" lab.example.com ")
	if f.add.Disabled() {
		t.Fatal("Add disabled for a valid domain")
	}
	test.Tap(f.add)
	if !slices.Equal(f.domains, []string{"example.com", "corp.example.com", "lab.example.com"}) {
		t.Fatalf("Add: %v", f.domains)
	}
}

func TestSettingsEnvironmentAtMostFive(t *testing.T) {
	h, p := settingsHarness(t, nil)
	_, f := envDialog(t, h, p)
	for _, s := range []string{"a.example.com", "b.example.com", "c.example.com"} {
		addDomain(f, s)
	}
	if len(f.domains) != 5 || !f.input.Disabled() || !f.add.Disabled() {
		t.Fatalf("five: %v input disabled %v add disabled %v", f.domains, f.input.Disabled(), f.add.Disabled())
	}
	addDomain(f, "d.example.com")
	if len(f.domains) != 5 {
		t.Fatalf("a sixth was taken: %v", f.domains)
	}
	test.Tap(f.rows[4].remove)
	if len(f.domains) != 4 || f.input.Disabled() {
		t.Fatalf("after remove: %v input disabled %v", f.domains, f.input.Disabled())
	}
}

// Removing every domain turns detection off; that is a change too.
func TestSettingsEnvironmentRemoveAll(t *testing.T) {
	h, p := settingsHarness(t, nil)
	d, f := envDialog(t, h, p)
	test.Tap(f.rows[1].remove)
	test.Tap(f.rows[0].remove)
	d.check.SetChecked(true)
	if len(f.domains) != 0 || d.action.Disabled() {
		t.Fatalf("domains %v disabled %v", f.domains, d.action.Disabled())
	}
	test.Tap(d.action)
	h.written(t)
	if len(h.dev.Cfg.Settings.Environment.Domains) != 0 || p.envValue() != "" {
		t.Fatalf("domains %v page %q", h.dev.Cfg.Settings.Environment.Domains, p.envValue())
	}
}

func TestSettingsEnvironmentWriteError(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.WriteErr = amt.ErrNotModifiable })
	d, f := envDialog(t, h, p)
	addDomain(f, "lab.example.com")
	d.check.SetChecked(true)
	test.Tap(d.action)
	h.written(t)
	if p.envValue() != "example.com, corp.example.com" || !p.err.box.Visible() || p.changeEnv.Disabled() {
		t.Errorf("page %q error %v disabled %v", p.envValue(), p.err.box.Visible(), p.changeEnv.Disabled())
	}
}

// The dialog takes the height of a longer list: the added row lies inside the dialog.
func TestSettingsEnvironmentDialogGrows(t *testing.T) {
	h, p := settingsHarness(t, nil)
	h.m.parent.Resize(fyne.NewSize(1000, 900))
	d, f := envDialog(t, h, p)
	before := d.frame.Size().Height
	addDomain(f, "lab.example.com")
	addDomain(f, "a.example.com")
	if d.frame.Size().Height <= before {
		t.Fatalf("height %v, before %v", d.frame.Size().Height, before)
	}
	last := f.rows[len(f.rows)-1].remove
	if bottom := absPos(last).Y + last.Size().Height; bottom > absPos(d.check).Y || last.Size().Height == 0 {
		t.Errorf("the new row (bottom %v) overlaps the checkbox at %v", bottom, absPos(d.check).Y)
	}
}
