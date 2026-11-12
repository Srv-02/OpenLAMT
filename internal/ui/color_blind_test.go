package ui

import (
	"fmt"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// restoreColorBlind puts the palette back to standard after the test.
func restoreColorBlind(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { style.SetColorBlind(false) })
}

func TestSettingsColorBlindDefaultAndStore(t *testing.T) {
	s := NewSettings(test.NewTempApp(t).Preferences())
	if s.ColorBlind() {
		t.Fatal("color blind mode on by default")
	}
	s.SetColorBlind(true)
	if !s.ColorBlind() {
		t.Fatal("not stored")
	}
	f := newSettingsForm(&s)
	if !f.colorBlind.Checked() {
		t.Fatal("the form does not show the stored setting")
	}
	f.colorBlind.SetChecked(false)
	if err := f.apply(&s); err != nil || s.ColorBlind() {
		t.Fatalf("Save did not store off (%v)", err)
	}
}

// settingsCardRows returns the rows of the card in the open settings dialog.
func settingsCardRows(t *testing.T, w fyne.Window) []fyne.CanvasObject {
	t.Helper()
	var card *widgets.FormCard
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if c, ok := o.(*widgets.FormCard); ok && card == nil {
			card = c
		}
	})
	if card == nil {
		t.Fatal("no card in the settings dialog")
	}
	return card.Rows()
}

// TestSettingsColorBlindRow: the row sits between Appearance and Check for updates, has the
// two-line label, four preview dots (on, standby, off, unknown) in the color-blind palette of the
// current variant, also while the setting is off, and a switch.
func TestSettingsColorBlindRow(t *testing.T) {
	englishUI(t)
	restoreDarkVariant(t)
	restoreColorBlind(t)
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		t.Run(fmt.Sprint(v), func(t *testing.T) {
			h := newStart(t)
			s := h.s
			style.SetVariant(v)
			showSettingsDialog(s.win, &s.settings)
			rows := settingsCardRows(t, s.win)
			if len(rows) != 10 || !hasText(rows[1], "Appearance") || !hasText(rows[3], "Check for updates") {
				t.Fatalf("%d rows, order wrong", len(rows))
			}
			row := rows[2]
			if !hasText(row, "Color blind mode") || !hasText(row, "Status colors that stay distinct with red-green color blindness.") {
				t.Fatal("label missing")
			}
			var dots []*widgets.StatusDot
			var sw *widgets.Switch
			walk(row, func(o fyne.CanvasObject) {
				switch x := o.(type) {
				case *widgets.StatusDot:
					dots = append(dots, x)
				case *widgets.Switch:
					sw = x
				}
			})
			if sw == nil || sw.Checked() {
				t.Fatal("no switch, or on by default")
			}
			want := []widgets.DotState{widgets.DotOn, widgets.DotAsleep, widgets.DotOff, widgets.DotUnknown}
			if len(dots) != len(want) {
				t.Fatalf("%d preview dots", len(dots))
			}
			for i, d := range dots {
				if d.State() != want[i] {
					t.Errorf("dot %d shows %d, want %d", i, d.State(), want[i])
				}
				if got, w := dotColor(d), style.StatusColor(want[i].Status(), v, true); rgba(got) != rgba(w) {
					t.Errorf("dot %d color %s, want the color-blind %s", i, rgba(got), rgba(w))
				}
			}
		})
	}
}

// dotColor is the palette color a status dot draws: the fill of a filled dot, the ring of an
// unknown one (on the Power button the inner ring, the outer disk is the button background).
func dotColor(d *widgets.StatusDot) color.Color {
	objs := test.WidgetRenderer(d).Objects()
	outer, inner := objs[0].(*canvas.Circle), objs[1].(*canvas.Circle)
	if d.State() != widgets.DotUnknown {
		return outer.FillColor
	}
	if !inner.Hidden {
		return inner.StrokeColor
	}
	return outer.StrokeColor
}

// dotsDrawPalette checks that every status dot below the roots draws its state in the current
// palette; it returns how many dots it checked.
func dotsDrawPalette(t *testing.T, label string, roots map[string]fyne.CanvasObject) int {
	t.Helper()
	n := 0
	for where, root := range roots {
		if root == nil {
			continue
		}
		walk(root, func(o fyne.CanvasObject) {
			d, ok := o.(*widgets.StatusDot)
			if !ok || d.State() == widgets.DotNone {
				return
			}
			n++
			want := style.CurrentStatusColor(d.State().Status())
			got := dotColor(d)
			if d.State() == widgets.DotUnknown && rgba(got) == rgba(style.Color(style.ColorNameSelectedText2)) {
				return // the ring of an unknown state on the selected (accent) row is white
			}
			if rgba(got) != rgba(want) {
				t.Errorf("%s: %s: dot in state %d draws %s, want %s", label, where, d.State(), rgba(got), rgba(want))
			}
		})
	}
	return n
}

// TestColorBlindModeSwitchesOpenWindows: saving "Color blind mode" recolors every status dot at
// once, without a restart: the sidebar, the status row of the detail card, the toolbar dot, the
// Power button and the status bar of an open device window, in dark and light; turning it off
// brings the standard palette back.
func TestColorBlindModeSwitchesOpenWindows(t *testing.T) {
	restoreDarkVariant(t)
	restoreColorBlind(t)
	englishUI(t)
	overrideTheme = style.Theme{}
	h := newStart(t)
	a := h.s.app
	a.Settings().SetTheme(style.Theme{})
	FollowTheme(a)
	fav := favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}
	other := favorites.Favorite{Name: "Keller", Host: "other.example.com", Username: "admin", UseTLS: true}
	addFavorites(t, h, fav, other)
	h.s.selectFavorite(fav)
	h.s.applyProbe(deviceKey(fav.Host, fav.Username), readProbe(amt.Status{PowerState: models.PowerStateOn}))
	h.s.applyProbe(deviceKey(other.Host, other.Username), readProbe(amt.Status{PowerState: models.PowerStateSleepDeep}))

	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	d := newDevice(t, a, &session.Fake{FP: fp, St: on}, true)
	d.d.apply(on, nil)

	roots := func() map[string]fyne.CanvasObject {
		return map[string]fyne.CanvasObject{"manager": h.s.win.Content(), "device": d.d.win.Content()}
	}
	save := func(colorBlind bool, appearance string) {
		t.Helper()
		showSettingsDialog(h.s.win, &h.s.settings)
		_, drops, _ := settingsParts(t, h.s.win)
		drops[1].SetSelected(appearance) // appearance
		var sw *widgets.Switch
		walk(settingsCardRows(t, h.s.win)[2], func(o fyne.CanvasObject) {
			if x, ok := o.(*widgets.Switch); ok {
				sw = x
			}
		})
		sw.SetChecked(colorBlind)
		_, _, buttons := dialogTexts(h.s.win)
		test.Tap(buttons["Save"])
		if h.s.settings.ColorBlind() != colorBlind || style.ColorBlind() != colorBlind {
			t.Fatalf("stored %v, drawn %v, want %v", h.s.settings.ColorBlind(), style.ColorBlind(), colorBlind)
		}
	}
	if n := dotsDrawPalette(t, "standard dark", roots()); n < 5 {
		t.Fatalf("only %d dots found", n)
	}
	save(true, "Dark")
	dotsDrawPalette(t, "color blind dark", roots())
	save(true, "Light")
	dotsDrawPalette(t, "color blind light", roots())
	save(false, "Light")
	dotsDrawPalette(t, "standard light", roots())
	save(false, "System")
	dotsDrawPalette(t, "standard system", roots())
}

// TestFollowThemeReadsColorBlind: the stored setting applies at the start.
func TestFollowThemeReadsColorBlind(t *testing.T) {
	restoreDarkVariant(t)
	restoreColorBlind(t)
	a := test.NewTempApp(t)
	NewSettings(a.Preferences()).SetColorBlind(true)
	FollowTheme(a)
	if !style.ColorBlind() {
		t.Fatal("stored color blind mode not applied at the start")
	}
}

// statusRow returns the "Status" row of the detail card, or nil.
func statusRow(s *StartWindow) fyne.CanvasObject {
	for _, r := range s.card1.Rows() {
		if hasText(r, "Status") {
			return r
		}
	}
	return nil
}

// TestDetailStatusRow: the read-only view of a favorite shows "Status" above Host/IP with the dot
// and the text of the same result as the sidebar dot, and follows every new result; edit mode and
// Quick Connect have no such row.
func TestDetailStatusRow(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	fav := favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}
	other := favorites.Favorite{Name: "Keller", Host: "other.example.com", Username: "admin", UseTLS: true}
	addFavorites(t, h, fav, other)
	k, k2 := deviceKey(fav.Host, fav.Username), deviceKey(other.Host, other.Username)
	test.Tap(s.rows[k])
	rows := s.card1.Rows()
	if row := statusRow(s); row == nil || rows[0] != row || !hasText(rows[1], "Host/IP") {
		t.Fatal("no Status row above Host/IP")
	}
	check := func(label string, dot widgets.DotState, text string) {
		t.Helper()
		row := statusRow(s)
		if s.statusDot.State() != dot || s.rows[k].Dot() != dot || !hasText(row, text) {
			t.Errorf("%s: dot %d (sidebar %d), want %d; text %q shown %v", label, s.statusDot.State(), s.rows[k].Dot(), dot, text, hasText(row, text))
		}
		var found bool
		walk(row, func(o fyne.CanvasObject) { found = found || o == s.statusDot })
		if !found {
			t.Errorf("%s: the dot is not in the row", label)
		}
	}
	// Before any result the row is empty (TestStatusRowEmptyUntilFirstResult), the sidebar shows the ring.
	if s.statusDot.State() != widgets.DotNone || s.statusText.Text != "" || s.rows[k].Dot() != widgets.DotUnknown {
		t.Errorf("before any result: dot %d, text %q, sidebar %d", s.statusDot.State(), s.statusText.Text, s.rows[k].Dot())
	}
	for _, c := range []struct {
		ps   models.PowerState
		dot  widgets.DotState
		text string
	}{
		{models.PowerStateOn, widgets.DotOn, "Powered on"},
		{models.PowerStateSleepLight, widgets.DotAsleep, "Standby"},
		{models.PowerStateSleepDeep, widgets.DotAsleep, "Standby"},
		{models.PowerStateHibernate, widgets.DotAsleep, "Standby"},
		{models.PowerStateOffSoft, widgets.DotOff, "Powered off"},
		{models.PowerStateOffHard, widgets.DotOff, "Powered off"},
		{models.PowerState(99), widgets.DotUnknown, "Unknown"},
	} {
		s.applyProbe(k, readProbe(amt.Status{PowerState: c.ps}))
		check(fmt.Sprint(c.ps), c.dot, c.text)
	}
	s.applyProbe(k, readProbe(amt.Status{PowerState: models.PowerStateOn}))
	s.applyProbe(k, probeResult{dot: widgets.DotUnknown}) // a failed probe
	check("failed probe", widgets.DotUnknown, "Unknown")
	s.windowStatus(k, amt.Status{PowerState: models.PowerStateOffSoft}, true) // the open device window reports
	check("device window", widgets.DotOff, "Powered off")
	s.windowStatus(k, amt.Status{}, false)
	check("device window, not read", widgets.DotUnknown, "Unknown")

	// A result for another favorite leaves the row alone; selecting it shows its own state.
	s.applyProbe(k, readProbe(amt.Status{PowerState: models.PowerStateOn}))
	s.applyProbe(k2, readProbe(amt.Status{PowerState: models.PowerStateHibernate}))
	check("other favorite probed", widgets.DotOn, "Powered on")
	test.Tap(s.rows[k2])
	if s.statusDot.State() != widgets.DotAsleep || !hasText(statusRow(s), "Standby") {
		t.Fatal("the second favorite does not show its state")
	}

	test.Tap(s.editBtn)
	if statusRow(s) != nil {
		t.Fatal("edit mode shows the Status row")
	}
	test.Tap(s.editCancelBtn)
	if statusRow(s) == nil {
		t.Fatal("the view lost the Status row")
	}
	test.Tap(s.newConnBtn)
	if statusRow(s) != nil {
		t.Fatal("Quick Connect shows the Status row")
	}
}
