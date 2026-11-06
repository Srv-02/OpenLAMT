package ui

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// panelButtons returns the buttons of a panel dialog from left to right.
func panelButtons(top fyne.CanvasObject) []*escButton {
	var bs []*escButton
	walk(top, func(o fyne.CanvasObject) {
		if b, ok := o.(*escButton); ok {
			bs = append(bs, b)
		}
	})
	x := func(b *escButton) float32 { return fyne.CurrentApp().Driver().AbsolutePositionForObject(b).X }
	sort.SliceStable(bs, func(i, j int) bool { return x(bs[i]) < x(bs[j]) })
	return bs
}

func texts(bs []*escButton) string {
	var s []string
	for _, b := range bs {
		s = append(s, b.Text)
	}
	return strings.Join(s, "|")
}

// panelHarness is a window of the default size with the app theme set.
func panelHarness(t *testing.T) (fyne.Window, fyne.Canvas) {
	t.Helper()
	englishUI(t)
	a := test.NewTempApp(t)
	a.Settings().SetTheme(style.Theme{})
	w := a.NewWindow("x")
	w.Resize(fyne.NewSize(900, 600))
	t.Cleanup(w.Close)
	return w, w.Canvas()
}

func open(w fyne.Window) bool { return w.Canvas().Overlays().Top() != nil }

func ret() *fyne.KeyEvent { return &fyne.KeyEvent{Name: fyne.KeyReturn} }
func esc() *fyne.KeyEvent { return &fyne.KeyEvent{Name: fyne.KeyEscape} }

// All dialogs: Cancel left of the main action, same width.
func TestPanelDialogsShareWidthAndButtonOrder(t *testing.T) {
	w, _ := panelHarness(t)
	fpr := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	cases := []struct {
		name    string
		show    func()
		buttons string
	}{
		{"delete", func() { (&StartWindow{win: w}).showDelete(favorites.Favorite{Host: "h.example.com", Username: "u"}) }, lang.L("Cancel") + "|Delete"},
		{"power", func() { showPowerConfirm(w, amt.PowerOff, "Büro", func() {}) }, lang.L("Cancel") + "|Power Off"},
		{"quit", func() { showConfirm(w, "power", "Quit OpenLAMT?", "A KVM session is open.", "Quit", func() {}, nil) }, lang.L("Cancel") + "|Quit"},
		{"error", func() { showMessage(w, "Oops", "text", "details") }, "Copy details|" + lang.L("OK")},
		{"message", func() { showMessage(w, "Info", "text", "") }, lang.L("OK")},
		{"trust", func() { showTrustDialog(w, "h.example.com", fpr, func(bool) {}) }, lang.L("Cancel") + "|Trust device"},
		{"mismatch", func() {
			showMismatchDialog(w, &amt.FingerprintMismatchError{Host: "h.example.com", Expected: fpr, Actual: fpr})
		}, "Close"},
		{"password", func() { showPasswordDialog(w, "h.example.com", "admin", true, func(string, bool, bool) {}) }, lang.L("Cancel") + "|Connect"},
		{"consent", func() { showConsentDialog(w, func(string, bool) {}) }, lang.L("Cancel") + "|Continue"},
	}
	for _, c := range cases {
		c.show()
		top := w.Canvas().Overlays().Top()
		if got := texts(panelButtons(top)); got != c.buttons {
			t.Errorf("%s: buttons %q, want %q", c.name, got, c.buttons)
		}
		var frame fyne.CanvasObject
		walk(top, func(o fyne.CanvasObject) {
			if f, ok := o.(*fyne.Container); ok && frame == nil {
				if _, ok := f.Layout.(*panelLayout); ok {
					frame = f
				}
			}
		})
		if frame == nil || frame.Size().Width != dialogWidth {
			t.Errorf("%s: frame %v, want width %v", c.name, frame, dialogWidth)
		}
		closeOverlays(w)
	}
}

// The destructive dialogs: Esc cancels, Enter never runs the action, Cancel has the focus.
func TestDestructiveDialogsEscAndEnter(t *testing.T) {
	w, c := panelHarness(t)
	ran, cancelled := 0, 0
	show := func() *panel {
		return showPanel(w, panelSpec{danger: true, icon: "delete", title: "Delete?", action: "Delete", cancel: "Cancel",
			onAction: func() { ran++ }, onCancel: func() { cancelled++ }}, nil)
	}
	p := show()
	if c.Focused() != p.cancel {
		t.Fatal("Cancel has no focus")
	}
	p.cancel.TypedKey(ret())
	p.action.TypedKey(ret())
	c.Unfocus()
	c.OnTypedKey()(ret())
	if ran != 0 || !open(w) {
		t.Fatalf("Enter ran=%d open=%v", ran, open(w))
	}
	if p.action.Importance != widget.DangerImportance {
		t.Fatal("action is not red")
	}
	c.OnTypedKey()(esc())
	if ran != 0 || cancelled != 1 || open(w) {
		t.Fatalf("Esc ran=%d cancelled=%d", ran, cancelled)
	}
	p = show()
	p.action.TypedKey(esc()) // Esc on the focused action button
	if cancelled != 2 || ran != 0 {
		t.Fatalf("Esc on action: ran=%d cancelled=%d", ran, cancelled)
	}
	p = show()
	test.Tap(p.action)
	if ran != 1 {
		t.Fatal("click did not run the action")
	}
}

// Non destructive dialogs confirm with Enter and cancel with Esc.
func TestNonDestructiveDialogsEnterAndEsc(t *testing.T) {
	w, c := panelHarness(t)
	var got []bool
	showConsentDialog(w, func(string, bool) { got = append(got, true) })
	c.Unfocus()
	c.OnTypedKey()(esc())
	if open(w) {
		t.Fatal("Esc did not close the consent dialog")
	}
	showMessage(w, "Oops", "text", "")
	c.OnTypedKey()(ret())
	if open(w) {
		t.Fatal("Enter did not close the message")
	}
}

// Trusting a certificate is a security decision: Enter never trusts, Cancel has the focus, Esc
// declines, and only the click on the (not red) action trusts.
func TestTrustDialogEnterNeverTrusts(t *testing.T) {
	w, c := panelHarness(t)
	var got []bool
	show := func() *panel {
		return showTrustDialog(w, "h.example.com", "AA:BB", func(ok bool) { got = append(got, ok) })
	}
	p := show()
	if c.Focused() != p.cancel {
		t.Fatal("Cancel has no focus")
	}
	if p.action.Importance == widget.DangerImportance {
		t.Fatal("trust button is red")
	}
	p.cancel.TypedKey(ret())
	p.action.TypedKey(ret())
	c.Unfocus()
	c.OnTypedKey()(ret())
	if len(got) != 0 || !open(w) {
		t.Fatalf("Enter answered %v, open=%v", got, open(w))
	}
	c.OnTypedKey()(esc())
	if len(got) != 1 || got[0] || open(w) {
		t.Fatalf("Esc: %v", got)
	}
	p = show()
	test.Tap(p.action)
	if len(got) != 2 || !got[1] {
		t.Fatalf("click: %v", got)
	}
}

func TestPasswordDialogFocusEnterEscAndAnswers(t *testing.T) {
	w, c := panelHarness(t)
	type res struct {
		pw       string
		save, ok bool
		calls    int
	}
	var r res
	answer := func(pw string, save, ok bool) { r = res{pw, save, ok, r.calls + 1} }
	showPasswordDialog(w, "h.example.com", "admin", true, answer)
	var entry *dialogEntry
	walk(c.Overlays().Top(), func(o fyne.CanvasObject) {
		if e, ok := o.(*dialogEntry); ok {
			entry = e
		}
	})
	if entry == nil || c.Focused() != entry || !entry.Password {
		t.Fatal("focus is not in the password field")
	}
	test.Type(entry, "secret")
	entry.TypedKey(ret()) // Enter in the field: Connect
	if r.pw != "secret" || !r.save || !r.ok || r.calls != 1 || open(w) {
		t.Fatalf("Enter: %+v", r)
	}

	showPasswordDialog(w, "h.example.com", "admin", true, answer)
	check := overlayCheck(t, w)
	test.Tap(check)
	test.Tap(overlayButton(t, w, "Connect"))
	if r.save || !r.ok || r.calls != 2 {
		t.Fatalf("save unchecked: %+v", r)
	}

	// Without an offer there is no check box and save is false.
	showPasswordDialog(w, "h.example.com", "admin", false, answer)
	walk(c.Overlays().Top(), func(o fyne.CanvasObject) {
		if _, ok := o.(*dialogCheck); ok {
			t.Error("check box offered")
		}
	})
	entry = nil
	walk(c.Overlays().Top(), func(o fyne.CanvasObject) {
		if e, ok := o.(*dialogEntry); ok {
			entry = e
		}
	})
	entry.TypedKey(esc())
	if r.ok || r.save || r.calls != 3 || open(w) {
		t.Fatalf("Esc: %+v", r)
	}
	showPasswordDialog(w, "h.example.com", "admin", true, answer)
	test.Tap(overlayButton(t, w, lang.L("Cancel")))
	if r.ok || r.calls != 4 {
		t.Fatalf("Cancel: %+v", r)
	}
}

func TestConsentDialogNeedsSixDigits(t *testing.T) {
	w, c := panelHarness(t)
	var code string
	var okAns, calls int
	dismiss := showConsentDialog(w, func(cd string, ok bool) {
		code, calls = cd, calls+1
		if ok {
			okAns++
		}
	})
	var entry *dialogEntry
	walk(c.Overlays().Top(), func(o fyne.CanvasObject) {
		if e, ok := o.(*dialogEntry); ok {
			entry = e
		}
	})
	cont := overlayButton(t, w, "Continue")
	if entry == nil || c.Focused() != entry || !cont.Disabled() {
		t.Fatal("focus or Continue state wrong")
	}
	entry.TypedKey(ret()) // Enter with an incomplete code does nothing
	test.Type(entry, "12345")
	if !cont.Disabled() {
		t.Fatal("Continue enabled with five digits")
	}
	entry.TypedKey(ret())
	if calls != 0 {
		t.Fatal("Enter confirmed an incomplete code")
	}
	test.Type(entry, "6")
	if cont.Disabled() {
		t.Fatal("Continue disabled with six digits")
	}
	test.Type(entry, "7") // seven digits: not a code
	if !cont.Disabled() {
		t.Fatal("Continue enabled with seven digits")
	}
	entry.SetText("123456")
	entry.TypedKey(ret())
	if code != "123456" || okAns != 1 || calls != 1 || open(w) {
		t.Fatalf("Enter: code %q ok %d calls %d", code, okAns, calls)
	}

	// The returned function dismisses (answer with ok false); Esc cancels too.
	calls = 0
	dismiss2 := showConsentDialog(w, func(string, bool) { calls++ })
	dismiss2()
	dismiss2()
	if calls != 1 || open(w) {
		t.Fatalf("dismiss: calls %d", calls)
	}
	showConsentDialog(w, func(string, bool) { calls++ })
	c.Focused().TypedKey(esc())
	if calls != 2 || open(w) {
		t.Fatalf("Esc: calls %d", calls)
	}
	_ = dismiss
}

func TestErrorDialogEnterClosesAndKeepsCopyDetails(t *testing.T) {
	w, c := panelHarness(t)
	a := fyne.CurrentApp()
	showError(w, errors.New("power on: dial tcp: refused"))
	cp := overlayButton(t, w, "Copy details")
	test.Tap(cp)
	if a.Clipboard().Content() != "power on: dial tcp: refused" || cp.Text != "Copied" || !cp.Disabled() {
		t.Fatalf("copy: %q %q", a.Clipboard().Content(), cp.Text)
	}
	c.OnTypedKey()(ret())
	if open(w) {
		t.Fatal("Enter did not close the error")
	}
	showError(w, errors.New("x"))
	c.OnTypedKey()(esc())
	if open(w) {
		t.Fatal("Esc did not close the error")
	}
}

// A small window: the dialog never gets wider or taller than the window, and the buttons stay in it.
func TestPanelDialogFitsSmallWindow(t *testing.T) {
	w, _ := panelHarness(t)
	w.Resize(fyne.NewSize(300, 200))
	showMismatchDialog(w, &amt.FingerprintMismatchError{Host: "h.example.com",
		Expected: strings.Repeat("AA:", 31) + "AA", Actual: strings.Repeat("BB:", 31) + "BB"})
	top := w.Canvas().Overlays().Top()
	bs := panelButtons(top)
	if len(bs) != 1 {
		t.Fatal("no button")
	}
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(bs[0])
	if pos.Y+bs[0].Size().Height > 200 || pos.X < 0 || pos.X+bs[0].Size().Width > 300 {
		t.Fatalf("button at %v size %v outside 300x200", pos, bs[0].Size())
	}
}
