package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

const riskHost = "device.example.com"

func TestShowQuestionTileAndButtons(t *testing.T) {
	w, c := panelHarness(t)
	ran := 0
	p := showQuestion(w, icons.Media, toneInfo, "Enable media redirection?", "Media redirection (IDE-R) is disabled.",
		"Cancel", "Enable", func() { ran++ })
	if got := texts(panelButtons(w.Canvas().Overlays().Top())); got != "Cancel|Enable" {
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
	title := findLabel(t, w.Canvas().Overlays().Top(), "Enable media redirection?")
	if absPos(title).Y <= absPos(tile).Y || absPos(title).X > absPos(tile).X+1 {
		t.Errorf("title at %v, tile at %v: tile must be above", absPos(title), absPos(tile))
	}
	// tile, title text and message start at the same x (the labels have an inner padding)
	if d := absPos(tile).X - (absPos(title).X + theme.InnerPadding()); d < -0.5 || d > 0.5 {
		t.Errorf("tile at x %v, title text at x %v", absPos(tile).X, absPos(title).X+theme.InnerPadding())
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
	if d.check == nil || d.check.Text != "I understand the risk for device.example.com" || d.check.Checked {
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

// The TLS dialog of Forms.dc.html is orange (checkbox) but shows the red warning box with a
// semibold first sentence and a list between the warning and the checkbox.
func TestOrangeConfirmWithRedBoxAndExtra(t *testing.T) {
	w, _ := panelHarness(t)
	var pick *dialogCheck
	confirmed := 0
	d := showOrangeConfirm(w, orangeSpec{
		op: amt.OpTLS, host: riskHost, title: "Change TLS certificate", danger: true,
		warningBold: "Risk of lockout.", warning: "AMT restarts TLS with the new certificate.",
		extra: func(d *riskDialog) []fyne.CanvasObject {
			pick = d.newCheck("other certificate", false)
			pick.OnChanged = func(bool) { d.Revalidate() }
			return []fyne.CanvasObject{pick}
		},
		ready:  func() bool { return pick.Checked },
		button: "Change Certificate", onConfirm: func(c amt.Confirmation) {
			if c.Allows(amt.OpTLS, riskHost) {
				confirmed++
			}
		},
	})
	if !same(d.warnBox.FillColor, style.Color(style.ColorNameDangerSurface)) || !hasRichText(t, d.frame, "Risk of lockout. ", true) {
		t.Errorf("warning box %v, bold first sentence missing", d.warnBox.FillColor)
	}
	if absPos(pick).Y >= absPos(d.check).Y {
		t.Errorf("extra content at %v below the checkbox at %v", absPos(pick), absPos(d.check))
	}
	test.Tap(d.check)
	if !d.action.Disabled() {
		t.Fatal("button enabled while the extra condition fails")
	}
	test.Tap(pick)
	if d.action.Disabled() {
		t.Fatal("button disabled with checkbox and extra condition")
	}
	test.Tap(d.action)
	if confirmed != 1 || open(w) {
		t.Fatalf("confirmed %d open %v", confirmed, open(w))
	}
}

// Since 11.11.2026 every level is confirmed with the checkbox, which names the device; the
// lockout operations too (Michael: "einfach nur eine Checkbox").
func TestOrangeConfirmAcceptsLockoutOperations(t *testing.T) {
	w, _ := panelHarness(t)
	var got []amt.Confirmation
	d := showOrangeConfirm(w, orangeSpec{op: amt.OpNetwork, host: riskHost, title: "x", warning: "y", button: "Go",
		onConfirm: func(c amt.Confirmation) { got = append(got, c) }})
	test.Tap(d.check)
	test.Tap(d.action)
	if len(got) != 1 || !got[0].Allows(amt.OpNetwork, riskHost) {
		t.Fatalf("confirmations %v", got)
	}
}

func TestRiskCheckTextNamesTheDevice(t *testing.T) {
	for _, c := range []struct{ device, host, want string }{
		{deviceLabel("Lab", riskHost), riskHost, "I understand the risk for Lab (device.example.com)"},
		{deviceLabel("device.example.com", riskHost), riskHost, "I understand the risk for device.example.com"},
		{deviceLabel("", riskHost), riskHost, "I understand the risk for device.example.com"},
		{"", riskHost, "I understand the risk for device.example.com"},
		{"", "", "I understand the risk"},
	} {
		if got := riskCheckText(c.device, c.host); got != c.want {
			t.Errorf("riskCheckText(%q, %q) = %q, want %q", c.device, c.host, got, c.want)
		}
	}
}

// TestFlushPanelLinesUpTitle: in the orange and red confirmations the title starts where the
// warning box and the fields start (Danger.dc.html, Forms.dc.html); its label padding is taken away.
func TestFlushPanelLinesUpTitle(t *testing.T) {
	w, _ := panelHarness(t)
	d := showRedConfirm(w, redSpec{op: amt.OpDisableNetwork, host: riskHost, title: "Turn off AMT on the network?",
		warningBold: "Bold.", warningRest: "Rest.", button: "Turn Off AMT"})
	head := findLabel(t, w.Canvas().Overlays().Top(), "Turn off AMT on the network?")
	textX := absPos(head).X + theme.InnerPadding()
	for name, o := range map[string]fyne.CanvasObject{"warning box": d.warnBox, "checkbox": d.check} {
		if dx := absPos(o).X - textX; dx < -0.5 || dx > 0.5 {
			t.Errorf("%s %v px from the title", name, dx)
		}
	}
	d.Dismiss()
}

func TestRedConfirmNeedsTheCheckbox(t *testing.T) {
	w, _ := panelHarness(t)
	var got []amt.Confirmation
	d := showRedConfirm(w, redSpec{
		op: amt.OpDisableNetwork, host: riskHost, device: deviceLabel("Lab", riskHost), title: "Turn off AMT on the network?",
		warningBold: "This cannot be undone remotely.", warningRest: "Only the BIOS (MEBx) at the device can turn it back on.",
		button: "Turn Off AMT", onConfirm: func(c amt.Confirmation) { got = append(got, c) },
	})
	if d.check == nil || d.check.Checked || d.check.Text != "I understand the risk for Lab (device.example.com)" {
		t.Fatalf("checkbox %+v", d.check)
	}
	if !d.action.Disabled() || d.action.Importance != widget.DangerImportance {
		t.Fatal("button enabled or not red before ticking")
	}
	if !same(d.warnBox.FillColor, style.Color(style.ColorNameDangerSurface)) {
		t.Errorf("warning box %v", d.warnBox.FillColor)
	}
	test.Tap(d.check)
	if d.action.Disabled() {
		t.Fatal("button disabled with the ticked checkbox")
	}
	test.Tap(d.check)
	if !d.action.Disabled() {
		t.Fatal("button enabled after unticking")
	}
	test.Tap(d.check)
	d.check.TypedKey(ret()) // a red dialog never confirms with Enter
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
	test.Tap(d.check)
	if !d.action.Disabled() {
		t.Fatal("extra condition does not block the button")
	}
	name.SetText("admin")
	if d.action.Disabled() {
		t.Fatal("button still disabled with checkbox and extra condition met")
	}
	// the extra fields sit between the warning and the confirmation
	if absPos(name).Y >= absPos(d.check).Y {
		t.Errorf("extra field at %v below the checkbox at %v", absPos(name), absPos(d.check))
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

// Extras.dc.html: a plain text instead of the warning box; without an operation the checkbox
// alone enables the button (deleting a user needs no amt.Confirmation).
func TestOrangeConfirmPlainTextWithoutOperation(t *testing.T) {
	w, _ := panelHarness(t)
	confirmed := 0
	d := showOrangeConfirm(w, orangeSpec{title: "Delete user “helpdesk”?", text: "The user can no longer sign in.",
		button: "Delete User", onConfirm: func(amt.Confirmation) { confirmed++ }})
	if d.warnBox != nil {
		t.Error("a warning box in the plain variant")
	}
	if !hasRichText(t, d.frame, "The user can no longer sign in.", false) {
		t.Error("text missing")
	}
	if !d.action.Disabled() {
		t.Fatal("button enabled before the box is ticked")
	}
	test.Tap(d.check)
	if d.action.Disabled() {
		t.Fatal("button disabled after the box was ticked")
	}
	test.Tap(d.action)
	if confirmed != 1 || open(w) {
		t.Fatalf("confirmed %d open %v", confirmed, open(w))
	}
}

// HostEnv.dc.html "Change host name": the field is above the warning box and has the focus.
func TestOrangeConfirmLeadAboveTheWarning(t *testing.T) {
	w, c := panelHarness(t)
	var field *dialogEntry
	d := showOrangeConfirm(w, orangeSpec{op: amt.OpHostName, host: riskHost, title: "Change host name",
		warning: "With Dynamic DNS, the device may no longer be found under its current name.",
		lead: func(d *riskDialog) []fyne.CanvasObject {
			field = d.newEntry(false)
			d.focus = field
			return []fyne.CanvasObject{field}
		},
		button: "Change Name"})
	if field == nil || absPos(field).Y >= absPos(d.warnBox).Y {
		t.Fatalf("field not above the warning box")
	}
	if c.Focused() != field {
		t.Error("the field has no focus")
	}
}
