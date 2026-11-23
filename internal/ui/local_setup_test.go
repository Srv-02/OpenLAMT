package ui

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/localme"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// fakeAction answers the setup actions with res and err and records what it got.
type fakeAction struct {
	res       localme.ActionResult
	err       error
	actions   []localme.Action
	passwords []string
	given     []byte // the password slice as handed over, to check that it is wiped
}

func (f *fakeAction) run(a localme.Action, pw []byte) (localme.ActionResult, error) {
	f.actions = append(f.actions, a)
	f.passwords = append(f.passwords, string(pw))
	f.given = pw
	return f.res, f.err
}

// newSetupStart is a start window on "This computer" whose last check answered r; setup
// actions answer through f.
func newSetupStart(t *testing.T, r localme.Result, f *fakeAction) *StartWindow {
	t.Helper()
	s := newCheckStart(t, newCheckApp(t), &fakeCheck{r: r})
	s.localAction = f.run
	test.Tap(s.checkBtn)
	return s
}

func offState(supported, allowed bool) localme.Result {
	return localme.Result{Found: true, Version: "16.1.25", TurnedOff: true, TurnOnSupported: supported, TurnOnAllowed: allowed,
		ControlMode: localme.ModeNone, Wired: wiredDHCP()}
}

func onState(mode string) localme.Result {
	return localme.Result{Found: true, Version: "16.1.25", ControlMode: mode, Wired: wiredDHCP()}
}

// riskFor is the checkbox text of the setup confirmations on this computer.
func riskFor(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "I understand the risk for this computer"
	}
	return "I understand the risk for this computer (" + strings.TrimSpace(h) + ")"
}

// TestLocalSetupOffersOneAction (design "Ablauf"): each state shows the Setup card with exactly
// one button or the hint to MEBx, next to the "Beta" pill.
func TestLocalSetupOffersOneAction(t *testing.T) {
	cases := []struct {
		name   string
		r      localme.Result
		title  string
		button string
		hint   string
	}{
		{"off", offState(true, true), "Turn on Intel AMT", "Turn On…", ""},
		{"off, not allowed", offState(true, false), "Turn on Intel AMT", "", "Turn on Intel AMT in the BIOS (MEBx)."},
		{"off, before 16.1", offState(false, false), "Turn on Intel AMT", "",
			"Turn on Intel AMT in the BIOS (MEBx). This version cannot turn it on from the app."},
		{"not activated", onState(localme.ModeNone), "Activate in Client Control Mode (CCM)", "Activate…", ""},
		{"CCM", onState(localme.ModeCCM), "Deactivate Intel AMT", "Deactivate…", ""},
		{"ACM", onState(localme.ModeACM), "Deactivate Intel AMT", "",
			"Deactivate Intel AMT in the BIOS (MEBx). Admin Control Mode cannot be deactivated from the app."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSetupStart(t, c.r, &fakeAction{})
			if !hasText(s.localCard, "Setup") || !hasText(s.localCard, c.title) {
				t.Fatalf("no Setup card %q: %s", c.title, cardTexts(s))
			}
			if c.button == "" {
				if s.setupBtn != nil || hasText(s.localCard, "Beta") {
					t.Fatalf("button %v shown", s.setupBtn)
				}
			} else if s.setupBtn == nil || s.setupBtn.Text != c.button || s.setupBtn.Disabled() || !hasText(s.localCard, "Beta") {
				t.Fatalf("button %+v", s.setupBtn)
			}
			if c.hint != "" && !strings.Contains(cardTexts(s), c.hint) {
				t.Fatalf("hint %q missing: %s", c.hint, cardTexts(s))
			}
			for _, other := range []string{"Turn On…", "Activate…", "Deactivate…"} {
				if other != c.button && strings.Contains(cardTexts(s), other) {
					t.Errorf("also offers %q", other)
				}
			}
		})
	}
	// No AMT, no setup.
	s := newSetupStart(t, localme.Result{Reason: localme.ReasonNoAMT}, &fakeAction{})
	if s.setupBtn != nil || hasText(s.localCard, "Setup") {
		t.Fatal("setup offered without Intel AMT")
	}
}

// TestLocalTurnOn: "Turn On…" asks in orange; the action runs only after the tick, and the
// card shows the state the helper read afterwards.
func TestLocalTurnOn(t *testing.T) {
	f := &fakeAction{res: localme.ActionResult{State: onState(localme.ModeNone)}}
	s := newSetupStart(t, offState(true, true), f)
	test.Tap(s.setupBtn)
	d := s.localDialog
	top := s.win.Canvas().Overlays().Top()
	findLabel(t, top, "Turn on Intel AMT on this computer?")
	if !hasRichText(t, top, "Intel AMT starts answering on the network (ports 16992 and 16993). Anyone with an AMT password can then control this computer, also while it is switched off.", false) {
		t.Error("warning missing")
	}
	if d.action.Text != "Turn On AMT" || !d.action.Disabled() || d.check.Text != riskFor(t) {
		t.Fatalf("button %q disabled %v, box %q", d.action.Text, d.action.Disabled(), d.check.Text)
	}
	if !same(d.warnBox.FillColor, style.Color(style.ColorNameWarningSurface)) {
		t.Error("the Turn On confirmation is not orange")
	}
	if !hasText(top, "Beta") || !hasText(top, "Uses a preview of Intel’s rpc-go library.") {
		t.Error("no Beta marker in the dialog")
	}
	test.Tap(d.action)
	if len(f.actions) != 0 {
		t.Fatal("ran before the tick")
	}
	test.Tap(d.check)
	test.Tap(d.action)
	if !slices.Equal(f.actions, []localme.Action{localme.ActionTurnOn}) || f.passwords[0] != "" {
		t.Fatalf("actions %v", f.actions)
	}
	if s.localRow.Host() != "AMT 16.1 · not activated" || s.setupBtn == nil || s.setupBtn.Text != "Activate…" || s.localChecking {
		t.Fatalf("after Turn On: subline %q", s.localRow.Host())
	}
	if r, ok := s.settings.LastLocalCheck(); !ok || r.TurnedOff || r.Checked.IsZero() {
		t.Fatalf("stored %+v", r)
	}
}

// TestLocalActivate: the password must follow AMT's rule and match before the red button
// works; it goes to the action once, is wiped and stored nowhere.
func TestLocalActivate(t *testing.T) {
	f := &fakeAction{res: localme.ActionResult{State: onState(localme.ModeCCM)}}
	s := newSetupStart(t, onState(localme.ModeNone), f)
	test.Tap(s.setupBtn)
	d := s.localDialog
	top := s.win.Canvas().Overlays().Top()
	findLabel(t, top, "Activate Intel AMT in Client Control Mode?")
	texts, _, _ := dialogTexts(s.win)
	if !slices.Contains(texts, "Admin password") || !hasText(top, "Beta") {
		t.Fatalf("texts %q", texts)
	}
	var fields []*widgets.PasswordField
	walk(top, func(o fyne.CanvasObject) {
		if p, ok := o.(*widgets.PasswordField); ok {
			fields = append(fields, p)
		}
	})
	if len(fields) != 2 || fields[0].Entry.PlaceHolder != "Password" || fields[1].Entry.PlaceHolder != "Repeat password" {
		t.Fatalf("%d password fields", len(fields))
	}
	pw, repeat := fields[0], fields[1]
	test.Tap(d.check)
	for _, c := range []struct{ a, b string }{
		{"weak", "weak"}, {"Secr3t!pw", "Secr3t!px"}, {"Secr3t:pw", "Secr3t:pw"}, {"", ""},
	} {
		pw.SetText(c.a)
		repeat.SetText(c.b)
		if !d.action.Disabled() {
			t.Fatalf("%q/%q accepted", c.a, c.b)
		}
	}
	pw.SetText("Secr3t!pw")
	repeat.SetText("Secr3t!pw")
	if d.action.Disabled() || d.action.Text != "Activate" {
		t.Fatal("a valid password is not accepted")
	}
	test.Tap(d.action)
	if !slices.Equal(f.actions, []localme.Action{localme.ActionActivateCCM}) || f.passwords[0] != "Secr3t!pw" {
		t.Fatalf("actions %v", f.actions)
	}
	if strings.Trim(string(f.given), "\x00") != "" {
		t.Fatal("the password bytes were not wiped")
	}
	if s.localRow.Host() != "AMT 16.1 · CCM" || s.setupBtn == nil || s.setupBtn.Text != "Deactivate…" {
		t.Fatalf("after Activate: %q", s.localRow.Host())
	}
	if strings.Contains(s.app.Preferences().String(prefLocalCheck), "Secr3t") {
		t.Fatal("the password was stored")
	}
}

// TestLocalDeactivate (V16): "Deactivate…" asks in red and runs the action after the tick.
func TestLocalDeactivate(t *testing.T) {
	f := &fakeAction{res: localme.ActionResult{State: onState(localme.ModeNone)}}
	s := newSetupStart(t, onState(localme.ModeCCM), f)
	test.Tap(s.setupBtn)
	d := s.localDialog
	top := s.win.Canvas().Overlays().Top()
	findLabel(t, top, "Deactivate Intel AMT on this computer?")
	if !hasRichText(t, top, "This deletes all Intel AMT settings. ", true) || !same(d.warnBox.FillColor, style.Color(style.ColorNameDangerSurface)) {
		t.Error("the Deactivate confirmation is not red")
	}
	if d.action.Text != "Deactivate" || !d.action.Disabled() || d.check.Text != riskFor(t) || !hasText(top, "Beta") {
		t.Fatalf("button %q, box %q", d.action.Text, d.check.Text)
	}
	test.Tap(d.check)
	test.Tap(d.action)
	if !slices.Equal(f.actions, []localme.Action{localme.ActionDeactivate}) || s.localRow.Host() != "AMT 16.1 · not activated" {
		t.Fatalf("actions %v, subline %q", f.actions, s.localRow.Host())
	}
}

// TestLocalActionRefused: a refusal shows "Intel AMT refused the change" and the new state.
func TestLocalActionRefused(t *testing.T) {
	f := &fakeAction{res: localme.ActionResult{Refused: "unprovision status 1", State: onState(localme.ModeCCM)}}
	s := newSetupStart(t, onState(localme.ModeCCM), f)
	var shown []error
	s.onError = func(err error) { shown = append(shown, err) }
	test.Tap(s.setupBtn)
	test.Tap(s.localDialog.check)
	test.Tap(s.localDialog.action)
	if len(shown) != 1 || describe(shown[0]) != "Intel AMT refused the change: unprovision status 1." {
		t.Fatalf("errors %v", shown)
	}
	if s.localRow.Host() != "AMT 16.1 · CCM" || s.setupBtn == nil || s.setupBtn.Disabled() {
		t.Fatal("state after the refusal")
	}
	// The Intel ME went silent: only the card "No Intel AMT found", no refusal dialog.
	shown = nil
	f.res = localme.ActionResult{Refused: "AMTConnectionFailed", State: localme.Result{Reason: localme.ReasonNoAMT}}
	test.Tap(s.setupBtn)
	test.Tap(s.localDialog.check)
	test.Tap(s.localDialog.action)
	if len(shown) != 0 || s.localRow.Host() != "No Intel AMT found" {
		t.Fatalf("errors %v, subline %q", shown, s.localRow.Host())
	}
}

// TestLocalActivateForeignLMS: another program on an LMS port stops the activation with its own
// error, not as a refusal of the firmware.
func TestLocalActivateForeignLMS(t *testing.T) {
	f := &fakeAction{res: localme.ActionResult{ForeignPort: 16992, State: onState(localme.ModeNone)}}
	s := newSetupStart(t, onState(localme.ModeNone), f)
	var shown []error
	s.onError = func(err error) { shown = append(shown, err) }
	test.Tap(s.setupBtn)
	fields := passwordFields(s)
	fields[0].SetText("Secr3t!pw")
	fields[1].SetText("Secr3t!pw")
	test.Tap(s.localDialog.check)
	test.Tap(s.localDialog.action)
	if len(shown) != 1 || describe(shown[0]) != "Another program uses the Intel AMT port 16992 on this computer. Nothing was changed." {
		t.Fatalf("errors %v", shown)
	}
	if s.localRow.Host() != "AMT 16.1 · not activated" || s.setupBtn == nil || s.setupBtn.Text != "Activate…" {
		t.Fatal("state after the stopped activation")
	}
}

// passwordFields are the password fields of the top dialog.
func passwordFields(s *StartWindow) []*widgets.PasswordField {
	var fields []*widgets.PasswordField
	walk(s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if p, ok := o.(*widgets.PasswordField); ok {
			fields = append(fields, p)
		}
	})
	return fields
}

// TestLocalActionDeclined: refused administrator rights show the information and keep the state.
func TestLocalActionDeclined(t *testing.T) {
	f := &fakeAction{err: localme.ErrDeclined}
	s := newSetupStart(t, offState(true, true), f)
	var shown []error
	s.onError = func(err error) { shown = append(shown, err) }
	test.Tap(s.setupBtn)
	test.Tap(s.localDialog.check)
	test.Tap(s.localDialog.action)
	texts, _, _ := dialogTexts(s.win)
	if !strings.Contains(strings.Join(texts, "|"), "Administrator rights were not granted. Nothing was changed.") || len(shown) != 0 {
		t.Fatalf("dialog %q, errors %v", texts, shown)
	}
	if s.localRow.Host() != "AMT 16.1 · turned off" || s.setupBtn == nil || s.setupBtn.Text != "Turn On…" {
		t.Fatal("a declined action changed the state")
	}
	f.err = errors.New("helper crashed")
	test.Tap(s.setupBtn)
	test.Tap(s.localDialog.check)
	test.Tap(s.localDialog.action)
	if len(shown) != 1 || s.localRow.Host() != "AMT 16.1 · turned off" {
		t.Fatalf("errors %v", shown)
	}
}

// TestLocalSetupWaitsForTheHelper: while a helper runs, Check and the setup button are disabled
// and a second action starts nothing.
func TestLocalSetupWaitsForTheHelper(t *testing.T) {
	f := &fakeAction{res: localme.ActionResult{State: onState(localme.ModeNone)}}
	s := newSetupStart(t, offState(true, true), f)
	var pending func()
	s.localRun = func(fn func()) { pending = fn }
	test.Tap(s.setupBtn)
	test.Tap(s.localDialog.check)
	test.Tap(s.localDialog.action)
	if !s.checkBtn.Disabled() || s.setupBtn == nil || !s.setupBtn.Disabled() {
		t.Fatal("buttons enabled while the helper runs")
	}
	s.runLocalAction(localme.ActionTurnOn, nil)
	pw := []byte("Secr3t!pw")
	s.runLocalAction(localme.ActionActivateCCM, pw)
	if pending == nil || len(f.actions) != 0 || string(pw) != "\x00\x00\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("actions %v, password %q", f.actions, pw)
	}
	pending()
	if len(f.actions) != 1 || s.checkBtn.Disabled() || s.setupBtn.Disabled() {
		t.Fatalf("actions %v", f.actions)
	}
}

// TestLocalSetupShots writes the Setup states and dialogs as PNGs to $OPENLAMT_SHOTS.
func TestLocalSetupShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		r    localme.Result
		open bool
	}{
		"setup-turn-on.png":        {offState(true, true), false},
		"setup-turn-on-bios.png":   {offState(true, false), false},
		"setup-activate.png":       {onState(localme.ModeNone), false},
		"setup-deactivate.png":     {onState(localme.ModeCCM), false},
		"setup-acm.png":            {onState(localme.ModeACM), false},
		"setup-dlg-turn-on.png":    {offState(true, true), true},
		"setup-dlg-activate.png":   {onState(localme.ModeNone), true},
		"setup-dlg-deactivate.png": {onState(localme.ModeCCM), true},
	} {
		s := newSetupStart(t, c.r, &fakeAction{})
		if c.open {
			test.Tap(s.setupBtn)
		}
		out, err := os.Create(dir + string(os.PathSeparator) + name)
		if err != nil {
			t.Fatal(err)
		}
		shotPNG(t, out, s.win)
		out.Close()
	}
}
