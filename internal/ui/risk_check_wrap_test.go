package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
)

const (
	longRiskName = "Office NUC"
	longRiskHost = "amt-nuc01.home.example.com"
)

// showLongRisk shows an orange confirmation whose checkbox names a device with a long label.
func showLongRisk(w fyne.Window, name, host string) *riskDialog {
	return showOrangeConfirm(w, orangeSpec{
		op: amt.OpHostName, host: host, device: deviceLabel(name, host), title: "Change the host name?",
		warning:  "With Dynamic DNS, the device may no longer be found under its current name.",
		oldValue: "old.example.com", newValue: "new.example.com", button: "Change Name",
	})
}

// TestRiskCheckWrapShots writes the Turn On dialog and a remote orange confirmation with long
// and short device names as PNGs to $OPENLAMT_SHOTS.
func TestRiskCheckWrapShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := func(name string, w fyne.Window) {
		out, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		shotPNG(t, out, w)
	}

	s := newSetupStart(t, offState(true, true), &fakeAction{})
	test.Tap(s.setupBtn)
	save("risk-turn-on.png", s.win)

	for name, dev := range map[string][2]string{
		"risk-remote-long.png":  {longRiskName, longRiskHost},
		"risk-remote-short.png": {"Lab", riskHost},
	} {
		w, _ := panelHarness(t)
		w.SetContent(widget.NewLabel(""))
		showLongRisk(w, dev[0], dev[1])
		save(name, w)
	}
}

// checkRows returns the lines the wrapping check c draws and the x of the first one, relative
// to the check.
func checkRows(t *testing.T, c *dialogCheck) ([]string, float32) {
	t.Helper()
	var rows []string
	var x float32
	walkAbs(c, fyne.NewPos(0, 0), func(o fyne.CanvasObject, pos fyne.Position) {
		if txt, ok := o.(*canvas.Text); ok && txt.Visible() && txt.Text != "" {
			if len(rows) == 0 {
				x = pos.X - c.Position().X
			}
			rows = append(rows, txt.Text)
		}
	})
	return rows, x
}

// A long device name wraps in the risk checkbox instead of being cut off at the panel's edge,
// and the check grows by the extra lines; the whole text stays inside the panel.
func TestRiskCheckWrapsLongDeviceName(t *testing.T) {
	w, _ := panelHarness(t)
	short := showLongRisk(w, "Lab", riskHost)
	shortHeight := short.check.Size().Height
	short.Dismiss()

	d := showLongRisk(w, longRiskName, longRiskHost)
	want := "I understand the risk for Office NUC (amt-nuc01.home.example.com)"
	if d.check.Text != want || !hasRichText(t, d.check, want, false) {
		t.Fatalf("checkbox %q", d.check.Text)
	}
	rows, _ := checkRows(t, d.check)
	if len(rows) < 2 || strings.Join(rows, " ") != want {
		t.Fatalf("drawn lines %q", rows)
	}
	if h := d.check.Size().Height; h <= shortHeight || d.check.MinSize().Height != h {
		t.Errorf("check height %v (one line %v, min %v)", h, shortHeight, d.check.MinSize().Height)
	}
	inner := absPos(d.frame).X + dialogWidth - panelPadH
	if right := absPos(d.check).X + d.check.Size().Width; right > inner+0.5 {
		t.Errorf("check ends at %v, the panel's content at %v", right, inner)
	}
	th := d.check.Theme()
	for _, o := range test.WidgetRenderer(d.check).Objects() {
		if txt, ok := o.(*canvas.Text); ok {
			t.Errorf("Fyne's single line %q is still drawn", txt.Text)
		}
	}
	// the text keeps the x of widget.Check's text, the box stays on the first line
	if _, x := checkRows(t, d.check); x != wrapCheckTextX(th)+th.Size(theme.SizeNameInnerPadding) {
		t.Errorf("text at x %v", x)
	}
}

// A short device name keeps the single line of widget.Check.
func TestRiskCheckShortNameKeepsOneLine(t *testing.T) {
	w, _ := panelHarness(t)
	d := showLongRisk(w, "Lab", riskHost)
	rows, x := checkRows(t, d.check)
	if len(rows) != 1 || rows[0] != "I understand the risk for Lab (device.example.com)" {
		t.Fatalf("drawn lines %q", rows)
	}
	r := test.WidgetRenderer(d.check).(*wrapCheckRenderer)
	th := d.check.Theme()
	if d.check.Size().Height != r.firstLine() || x != wrapCheckTextX(th)+th.Size(theme.SizeNameInnerPadding) {
		t.Errorf("height %v (widget.Check %v), text at x %v", d.check.Size().Height, r.firstLine(), x)
	}
}

// Clicking any line of the wrapped text toggles the box and enables the red button, as a click on
// widget.Check's text does; Space still toggles; a click right of a short text does nothing.
func TestRiskCheckTapOnWrappedText(t *testing.T) {
	w, c := panelHarness(t)
	d := showLongRisk(w, longRiskName, longRiskHost)
	if !d.action.Disabled() {
		t.Fatal("button enabled before the box is ticked")
	}
	last := fyne.NewPos(wrapCheckTextX(d.check.Theme())+20, d.check.Size().Height-4) // on the second line
	test.TapAt(d.check, last)
	if !d.check.Checked || d.action.Disabled() || c.Focused() != d.check {
		t.Fatalf("tap on the second line: checked %v, button disabled %v, focus %v", d.check.Checked, d.action.Disabled(), c.Focused())
	}
	d.check.TypedRune(' ')
	if d.check.Checked || !d.action.Disabled() {
		t.Fatal("Space did not clear the box")
	}
	test.TapAt(d.check, fyne.NewPos(5, 10)) // the box
	if !d.check.Checked {
		t.Fatal("tap on the box did not tick it")
	}
	d.Dismiss()

	s := showLongRisk(w, "Lab", riskHost)
	test.TapAt(s.check, fyne.NewPos(s.check.Size().Width-2, s.check.Size().Height/2))
	if s.check.Checked {
		t.Error("a tap right of the short text ticked the box")
	}
}
