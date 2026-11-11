package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

const riskHost = "device.example.com"

func TestShowQuestionTileAndButtons(t *testing.T) {
	w, c := panelHarness(t)
	ran := 0
	p := showQuestion(w, icons.Media, toneInfo, "Turn on media redirection?", "Media redirection (IDE-R) is turned off.",
		"Cancel", "Turn On", func() { ran++ })
	if got := texts(panelButtons(w.Canvas().Overlays().Top())); got != "Cancel|Turn On" {
		t.Fatalf("buttons %q", got)
	}
	if p.action.Importance != widget.HighImportance {
		t.Error("main button is not the blue one")
	}
	tile := findTile(t, w.Canvas().Overlays().Top())
	if tile.Size() != fyne.NewSquareSize(36) || tile.CornerRadius != 9 || !same(tile.FillColor, style.Color(style.ColorNameAccentTint)) {
		t.Errorf("info tile %v radius %v fill %v", tile.Size(), tile.CornerRadius, tile.FillColor)
	}
	// the tile sits above the title (Dialogs.dc.html), not left of it
	title := findLabel(t, w.Canvas().Overlays().Top(), "Turn on media redirection?")
	if absPos(title).Y <= absPos(tile).Y || absPos(title).X > absPos(tile).X+1 {
		t.Errorf("title at %v, tile at %v: tile must be above", absPos(title), absPos(tile))
	}
	c.Unfocus()
	c.OnTypedKey()(ret()) // a question confirms with Enter
	if ran != 1 || open(w) {
		t.Fatalf("Enter: ran %d open %v", ran, open(w))
	}

	showQuestion(w, icons.Warning, toneWarning, "An ISO is still mounted", "text", "Keep Open", "Unmount and Close", func() { ran++ })
	tile = findTile(t, w.Canvas().Overlays().Top())
	if !same(tile.FillColor, style.Color(style.ColorNameWarningSurface)) {
		t.Errorf("warning tile fill %v", tile.FillColor)
	}
	c.OnTypedKey()(esc())
	if ran != 1 || open(w) {
		t.Errorf("Esc: ran %d open %v", ran, open(w))
	}
}

func TestOrangeConfirmNeedsTheCheckbox(t *testing.T) {
	w, _ := panelHarness(t)
	var got []amt.Confirmation
	d := showOrangeConfirm(w, orangeSpec{
		op: amt.OpHostName, host: riskHost, title: "Change the host name?",
		warning:  "With Dynamic DNS, the device may no longer be found under its current name.",
		oldValue: "old.example.com", newValue: "new.example.com", button: "Change Name",
		onConfirm: func(c amt.Confirmation) { got = append(got, c) },
	})
	if d.check == nil || d.check.Text != "I understand the risk" || d.check.Checked {
		t.Fatalf("checkbox %+v", d.check)
	}
	if !d.action.Disabled() {
		t.Fatal("button enabled before the box is ticked")
	}
	if d.action.Importance != widget.DangerImportance {
		t.Error("button is not red")
	}
	d.action.Tapped(nil)
	if len(got) != 0 {
		t.Fatal("disabled button confirmed")
	}
	test.Tap(d.check)
	if d.action.Disabled() {
		t.Fatal("button still disabled after the box was ticked")
	}
	test.Tap(d.check)
	if !d.action.Disabled() {
		t.Fatal("button enabled after the box was cleared")
	}
	test.Tap(d.check)
	test.Tap(d.action)
	if len(got) != 1 || !got[0].Allows(amt.OpHostName, riskHost) || open(w) {
		t.Fatalf("confirmations %v open %v", got, open(w))
	}
	if !hasRichText(t, d.frame, "new.example.com", true) {
		t.Error("detail line without the new value in semibold")
	}
}

func TestOrangeConfirmRefusesLockoutOperations(t *testing.T) {
	w, _ := panelHarness(t)
	d := showOrangeConfirm(w, orangeSpec{op: amt.OpNetwork, host: riskHost, title: "x", warning: "y", button: "Go",
		onConfirm: func(amt.Confirmation) { t.Error("confirmed a lockout operation with a checkbox") }})
	test.Tap(d.check)
	if !d.action.Disabled() {
		t.Error("a lockout operation must not be confirmable with the checkbox")
	}
}

func TestRedConfirmNeedsTheExactHost(t *testing.T) {
	w, _ := panelHarness(t)
	var got []amt.Confirmation
	d := showRedConfirm(w, redSpec{
		op: amt.OpDisableNetwork, host: riskHost, title: "Turn off AMT on the network?",
		warningBold: "This cannot be undone remotely.", warningRest: "Only the BIOS (MEBx) at the device can turn it back on.",
		button: "Turn Off AMT", onConfirm: func(c amt.Confirmation) { got = append(got, c) },
	})
	if d.phrase == nil || d.phrase.Text != riskHost || !d.phrase.TextStyle.Monospace {
		t.Fatalf("phrase %+v", d.phrase)
	}
	if dy := absPos(d.phrase).Y - absPos(d.prefix).Y; dy > 4 || dy < -4 || absPos(d.phrase).X <= absPos(d.prefix).X {
		t.Errorf("label not on one line: prefix %v phrase %v", absPos(d.prefix), absPos(d.phrase))
	}
	if !d.action.Disabled() || d.action.Importance != widget.DangerImportance {
		t.Fatal("button enabled or not red before typing")
	}
	normal := style.Color(style.ColorNameControlBorder)
	if !same(d.border.StrokeColor, normal) {
		t.Errorf("empty field border %v", d.border.StrokeColor)
	}
	d.entry.SetText("device.example.co")
	if !d.action.Disabled() || !same(d.border.StrokeColor, style.Color(style.ColorNameDanger)) || d.border.StrokeWidth != 1.5 {
		t.Errorf("mistyped: disabled %v border %v width %v", d.action.Disabled(), d.border.StrokeColor, d.border.StrokeWidth)
	}
	d.entry.SetText("  DEVICE.Example.com ")
	if d.action.Disabled() || !same(d.border.StrokeColor, normal) {
		t.Fatalf("exact host (case and spaces ignored): disabled %v border %v", d.action.Disabled(), d.border.StrokeColor)
	}
	d.entry.SetText("")
	if !d.action.Disabled() || !same(d.border.StrokeColor, normal) {
		t.Errorf("cleared: disabled %v border %v", d.action.Disabled(), d.border.StrokeColor)
	}
	d.entry.SetText(riskHost)
	d.entry.TypedKey(ret()) // a red dialog never confirms with Enter
	if len(got) != 0 || !open(w) {
		t.Fatal("Enter confirmed a red dialog")
	}
	test.Tap(d.action)
	if len(got) != 1 || !got[0].Allows(amt.OpDisableNetwork, riskHost) || open(w) {
		t.Fatalf("confirmations %v open %v", got, open(w))
	}
}

func TestRedConfirmExtraCondition(t *testing.T) {
	w, _ := panelHarness(t)
	var name *dialogEntry
	confirmed := 0
	d := showRedConfirm(w, redSpec{
		op: amt.OpAdminAccount, host: riskHost, title: "Change the signed-in admin account",
		warningBold: "Risk of lockout.", warningRest: "If the new password is lost, only the BIOS (MEBx) at the device can reset it.",
		extra: func(d *riskDialog) []fyne.CanvasObject {
			name = d.newEntry(false)
			name.OnChanged = func(string) { d.Revalidate() }
			return []fyne.CanvasObject{name}
		},
		ready:  func() bool { return name.Text != "" },
		button: "Change Account", onConfirm: func(amt.Confirmation) { confirmed++ },
	})
	d.entry.SetText(riskHost)
	if !d.action.Disabled() {
		t.Fatal("extra condition does not block the button")
	}
	name.SetText("admin")
	if d.action.Disabled() {
		t.Fatal("button still disabled with host and extra condition met")
	}
	// the extra fields sit between the warning and the confirmation
	if absPos(name).Y >= absPos(d.entry).Y {
		t.Errorf("extra field at %v below the confirmation at %v", absPos(name), absPos(d.entry))
	}
	name.TypedKey(esc())
	if open(w) || confirmed != 0 {
		t.Errorf("Esc in an extra field: open %v confirmed %d", open(w), confirmed)
	}
}

func TestRiskButtonsUseTheDesignRed(t *testing.T) {
	w, _ := panelHarness(t)
	d := showRedConfirm(w, redSpec{op: amt.OpNetwork, host: riskHost, title: "t", warningBold: "b", warningRest: "r", button: "Go",
		onConfirm: func(amt.Confirmation) {}})
	th := d.action.Theme()
	if !same(th.Color("error", 0), style.Color(style.ColorNameDanger)) || !same(th.Color("disabledButton", 0), style.Color(style.ColorNameDangerDisabled)) ||
		!same(th.Color("disabled", 0), style.Color(style.ColorNameWhite)) {
		t.Error("the main button does not use the danger tokens")
	}
	if !same(d.warnBox.FillColor, style.Color(style.ColorNameDangerSurface)) ||
		!same(d.warnBox.StrokeColor, style.Color(style.ColorNameDangerBoxBorder)) {
		t.Errorf("red warning box %v / %v", d.warnBox.FillColor, d.warnBox.StrokeColor)
	}
}

// findTile returns the symbol tile of a question dialog.
func findTile(t *testing.T, top fyne.CanvasObject) *canvas.Rectangle {
	t.Helper()
	var tile *canvas.Rectangle
	walk(top, func(o fyne.CanvasObject) {
		if r, ok := o.(*canvas.Rectangle); ok && tile == nil && r.Size().Width == r.Size().Height && r.Size().Width >= 36 && r.Size().Width <= 40 {
			tile = r
		}
	})
	if tile == nil {
		t.Fatal("no symbol tile")
	}
	return tile
}

func findLabel(t *testing.T, top fyne.CanvasObject, text string) *widget.Label {
	t.Helper()
	var l *widget.Label
	walk(top, func(o fyne.CanvasObject) {
		if v, ok := o.(*widget.Label); ok && v.Text == text {
			l = v
		}
	})
	if l == nil {
		t.Fatalf("no label %q", text)
	}
	return l
}

// hasRichText reports whether root holds a rich text segment with text in bold (or not).
func hasRichText(t *testing.T, root fyne.CanvasObject, text string, bold bool) bool {
	t.Helper()
	found := false
	walk(root, func(o fyne.CanvasObject) {
		if rt, ok := o.(*widget.RichText); ok {
			for _, s := range rt.Segments {
				if ts, ok := s.(*widget.TextSegment); ok && ts.Text == text && ts.Style.TextStyle.Bold == bold {
					found = true
				}
			}
		}
	})
	return found
}

func absPos(o fyne.CanvasObject) fyne.Position {
	return fyne.CurrentApp().Driver().AbsolutePositionForObject(o)
}

func same(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
