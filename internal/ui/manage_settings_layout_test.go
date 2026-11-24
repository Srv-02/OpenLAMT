package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// checkRowInside lays row out at width w and its minimum height and fails if a child of the
// row, or a text drawn inside its right side, reaches outside the row, or if the row has less
// than settingsRowVPad above and below its tallest child.
func checkRowInside(t *testing.T, name string, row fyne.CanvasObject, w float32) {
	t.Helper()
	c := row.(*fyne.Container)
	ms := row.MinSize()
	w, h := max(w, ms.Width), ms.Height // Fyne never lays a row out below its minimum
	row.Resize(fyne.NewSize(w, h))
	for i, o := range c.Objects {
		pos, size := o.Position(), o.Size()
		if pos.X < 0 || pos.Y < 0 || pos.X+size.Width > w+0.5 || pos.Y+size.Height > h+0.5 {
			t.Errorf("%s at %v: child %d at %v size %v outside row %vx%v", name, w, i, pos, size, w, h)
		}
		if need := o.MinSize().Height + 2*settingsRowVPad; h+0.5 < need {
			t.Errorf("%s at %v: row %v high, child %d needs %v", name, w, h, i, need)
		}
	}
	right := c.Objects[1]
	walkTexts(t, right, func(txt *canvas.Text, origin fyne.Position) {
		x := origin.X + txt.Position().X
		drawn := fyne.MeasureText(txt.Text, txt.TextSize, txt.TextStyle).Width
		if x < -0.5 || x+drawn > right.Size().Width+0.5 {
			t.Errorf("%s at %v: text %q (%v wide at %v) drawn outside %v", name, w, txt.Text, drawn, x, right.Size().Width)
		}
	})
}

// walkTexts calls f for every visible canvas.Text under o (o itself included), with the
// position of the text's parent relative to o.
func walkTexts(t *testing.T, o fyne.CanvasObject, f func(*canvas.Text, fyne.Position)) {
	var walk func(fyne.CanvasObject, fyne.Position, bool)
	walk = func(o fyne.CanvasObject, at fyne.Position, top bool) {
		if !o.Visible() {
			return
		}
		inner := at
		if !top {
			inner = at.Add(o.Position())
		}
		switch v := o.(type) {
		case *canvas.Text:
			f(v, at)
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c, inner, false)
			}
		case fyne.Widget:
			for _, c := range test.TempWidgetRenderer(t, v).Objects() {
				walk(c, inner, false)
			}
		}
	}
	walk(o, fyne.Position{}, true)
}

var narrowWidths = []float32{400, 300, 260, 220, 180}

// The IP address shows in two right-aligned lines that shorten with "…" and never leave the row
// (#197), however narrow the sidebar.
func TestSettingsIPRowStaysInside(t *testing.T) {
	_, p := settingsHarness(t, func(f *session.Fake) {
		f.Cfg.Settings.Network = amt.NetworkSettings{Available: true, IPAddress: "192.0.2.250",
			SubnetMask: "255.255.255.192", Gateway: "192.0.2.254"}
	})
	if got := p.ipValue(); got != "192.0.2.250 / 26\ngateway 192.0.2.254" {
		t.Fatalf("ip %q", got)
	}
	row := p.network.rows[1]
	for _, w := range narrowWidths {
		checkRowInside(t, "IP", row, w)
	}
	row.Resize(fyne.NewSize(200, row.MinSize().Height))
	if p.ipAddr.Size().Width >= p.ipAddr.natural() {
		t.Errorf("at 200 the address gets %v of %v", p.ipAddr.Size().Width, p.ipAddr.natural())
	}
}

// The rows with a description under the key keep the padding below it (#197).
func TestSettingsDescriptionRowsStayInside(t *testing.T) {
	_, p := settingsHarness(t, nil)
	for name, row := range map[string]fyne.CanvasObject{"RPE": p.rpeSettingsRow(), "OCR": p.ocrSettingsRow()} {
		for _, w := range narrowWidths {
			checkRowInside(t, name, row, w)
		}
	}
}

// Rows with one line stay 40 high.
func TestSettingsSingleLineRowsStay40(t *testing.T) {
	_, p := settingsHarness(t, func(f *session.Fake) { f.Cfg.Settings.Network.Gateway = "" })
	if got := p.ipValue(); got != "192.0.2.10 / 24" {
		t.Fatalf("ip %q", got)
	}
	rows := map[string]fyne.CanvasObject{
		"host":     p.general.rows[0],
		"ping":     p.general.rows[2],
		"address":  p.network.rows[0],
		"ip alone": p.network.rows[1],
		"kvm":      p.features.rows[0],
		"consent":  p.features.rows[3],
		"clock":    p.clockTLS.rows[0],
	}
	for name, row := range rows {
		if h := row.MinSize().Height; h != settingsRowHeight {
			t.Errorf("%s: %v high", name, h)
		}
	}
}

// The plain-text values on the right (host name, domain, intranet domains, clock, TLS, forwarding
// target) shorten with "…" and never leave the row (#200), however narrow the sidebar.
func TestSettingsTextValuesStayInside(t *testing.T) {
	long := "a-very-long-value-that-needs-much-more-room-than-the-row-has.example.com"
	_, p := settingsHarness(t, func(f *session.Fake) {
		f.Cfg.Settings.General.HostName, f.Cfg.Settings.General.DomainName = long, long
		f.Cfg.Settings.Environment.Domains = []string{long, "corp.example.com"}
	})
	rows := map[string]fyne.CanvasObject{
		"host": p.general.rows[0], "domain": p.general.rows[1], "env": p.env.rows[0],
		"clock": p.clockTLS.rows[0], "tls": p.clockTLS.rows[1],
	}
	card := targetEditCard(amt.ForwardingTarget{Address: long, FilterName: long, Delivery: amt.DeliveryPush})
	for i, r := range card.rows {
		rows["target"+string(rune('0'+i))] = r
	}
	for name, row := range rows {
		for _, w := range narrowWidths {
			checkRowInside(t, name, row, w)
		}
	}
}
