package ui

import (
	"testing"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// textX returns the absolute x of the canvas.Text that draws text in the detail pane (the
// rightmost one; the sidebar shows the host too), and whether there is one: where the glyphs of
// a label start.
func textX(s *StartWindow, text string) (float32, bool) {
	var x float32
	found := false
	walkAbs(s.win.Content(), fyne.Position{}, func(o fyne.CanvasObject, pos fyne.Position) {
		if t, ok := o.(*canvas.Text); ok && t.Text == text && o.Visible() && (!found || pos.X > x) {
			x, found = pos.X, true
		}
	})
	return x, found
}

// dotBox returns the absolute position and size of the status dot of the detail card.
func dotBox(t *testing.T, s *StartWindow) (fyne.Position, fyne.Size) {
	t.Helper()
	var pos fyne.Position
	found := false
	walkAbs(s.win.Content(), fyne.Position{}, func(o fyne.CanvasObject, p fyne.Position) {
		if o == s.statusDot {
			pos, found = p, true
		}
	})
	if !found {
		t.Fatal("status dot not in the window")
	}
	return pos, s.statusDot.Size()
}

// TestStatusRowAlignment: the dot of the status row starts exactly where the text of the values
// below starts (Host/IP, Name …), and its text follows 6 to 8 px after the dot, for a filled dot
// and for the wider ring of Unknown. Before the fix the dot sat at the left edge of the value
// label, 8 px (the label's inner padding) left of the value text, and the text started 16 px
// after the dot (gap plus the label's padding).
func TestStatusRowAlignment(t *testing.T) {
	restoreDarkVariant(t)
	englishUI(t)
	h := newStart(t)
	s := h.s
	fav := favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}
	addFavorites(t, h, fav)
	k := deviceKey(fav.Host, fav.Username)
	test.Tap(s.rows[k])
	for _, c := range []struct {
		label string
		r     probeResult
		text  string
	}{
		{"on", readProbe(amt.Status{PowerState: models.PowerStateOn}), "Powered on"},
		{"unknown", probeResult{dot: widgets.DotUnknown}, "Unknown"},
	} {
		s.applyProbe(k, c.r)
		valueX, ok := textX(s, fav.Host)
		if !ok {
			t.Fatal("Host/IP value not drawn")
		}
		statusX, ok := textX(s, c.text)
		if !ok {
			t.Fatalf("%s: %q not drawn", c.label, c.text)
		}
		pos, size := dotBox(t, s)
		if size.Width < style.StatusDotSize {
			t.Fatalf("%s: dot %v smaller than the dot size", c.label, size)
		}
		if d := pos.X - valueX; d < -0.5 || d > 0.5 {
			t.Errorf("%s: dot starts at x=%.1f, the values at x=%.1f (%.1f px off)", c.label, pos.X, valueX, d)
		}
		if gap := statusX - (pos.X + size.Width); gap < 6 || gap > 8 {
			t.Errorf("%s: text starts %.1f px after the dot, want 6 to 8", c.label, gap)
		}
	}
}

// TestStatusRowEmptyUntilFirstResult: while no result is there for the selected favorite the
// status row shows neither dot nor text (no "Unknown" while loading); the sidebar keeps its
// gray ring. A result, also a failed probe, fills it.
func TestStatusRowEmptyUntilFirstResult(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	fav := favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}
	other := favorites.Favorite{Name: "Keller", Host: "other.example.com", Username: "admin", UseTLS: true}
	addFavorites(t, h, fav, other)
	k, k2 := deviceKey(fav.Host, fav.Username), deviceKey(other.Host, other.Username)
	test.Tap(s.rows[k])
	empty := func(label string) {
		t.Helper()
		if s.statusDot.State() != widgets.DotNone || s.statusText.Text != "" {
			t.Errorf("%s: dot %d, text %q; want an empty status row", label, s.statusDot.State(), s.statusText.Text)
		}
		if hasText(statusRow(s), "Unknown") {
			t.Errorf("%s: the status row shows Unknown", label)
		}
	}
	empty("before any result")
	if s.rows[k].Dot() != widgets.DotUnknown {
		t.Errorf("sidebar dot %d before any result, want the gray ring", s.rows[k].Dot())
	}
	s.applyProbe(k2, readProbe(amt.Status{PowerState: models.PowerStateOn}))
	empty("result of another favorite")
	s.applyProbe(k, probeResult{dot: widgets.DotUnknown})
	if s.statusDot.State() != widgets.DotUnknown || s.statusText.Text != "Unknown" {
		t.Errorf("failed probe: dot %d, text %q; want Unknown", s.statusDot.State(), s.statusText.Text)
	}
	test.Tap(s.rows[k2])
	if s.statusDot.State() != widgets.DotOn || s.statusText.Text != "Powered on" {
		t.Errorf("second favorite: dot %d, text %q; want Powered on", s.statusDot.State(), s.statusText.Text)
	}
}
