package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/localme"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// fakeCheck answers Check with r and err and counts the calls.
type fakeCheck struct {
	r     localme.Result
	err   error
	calls int
}

func (f *fakeCheck) check() (localme.Result, error) { f.calls++; return f.r, f.err }

// newCheckStart builds a start window on app a (its preferences hold the last result) with an
// Intel ME, whose Check answers through f inline, and selects "This computer".
func newCheckStart(t *testing.T, a fyne.App, f *fakeCheck) *StartWindow {
	t.Helper()
	keys := keychain.NewMemory()
	mgr := favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys)
	s := newStartWindow(a, mgr, keys, session.ConnectFake(&session.Fake{FP: fp}), func(s *StartWindow) {
		s.prober.Stop()
		s.lookupPassword = func(_, _ string, answer func(bool)) { answer(false) }
		s.localME = localme.Fake(true)
		s.localCheck = f.check
		s.localRun = func(fn func()) { fn() }
	})
	t.Cleanup(func() { s.win.Close() })
	test.Tap(s.localRow)
	return s
}

func newCheckApp(t *testing.T) fyne.App {
	t.Helper()
	englishUI(t)
	a := test.NewTempApp(t)
	a.Settings().SetTheme(style.Theme{})
	return a
}

// cardTexts returns every text of the local card: labels, canvas texts and rich text segments.
func cardTexts(s *StartWindow) string {
	var out []string
	walk(s.localCard, func(c fyne.CanvasObject) {
		if r, ok := c.(*widget.RichText); ok {
			for _, seg := range r.Segments {
				if l, ok := seg.(*widget.ListSegment); ok {
					for _, it := range l.Items {
						out = append(out, it.Textual())
					}
					continue
				}
				out = append(out, seg.Textual())
			}
		}
	})
	walk(s.localCard, func(c fyne.CanvasObject) {
		switch v := c.(type) {
		case *widget.Label:
			out = append(out, v.Text)
		}
	})
	return strings.Join(out, "|")
}

func wiredDHCP() *localme.Link {
	return &localme.Link{Enabled: true, LinkUp: true, DHCP: true, IP: "192.0.2.50"}
}

// TestLocalCheckStates: every answer of Check shows its subline, button and card.
func TestLocalCheckStates(t *testing.T) {
	cases := []struct {
		name    string
		r       localme.Result
		subline string
		card    []string
		notCard []string
	}{
		{"not activated", localme.Result{Found: true, Version: "16.1.25", ControlMode: localme.ModeNone, Wired: wiredDHCP()},
			"AMT 16.1 · not activated", []string{"On", "16.1.25", "Not activated", "Wired · 192.0.2.50 · DHCP"}, []string{"Turned off"}},
		{"turned off", localme.Result{Found: true, Version: "16.1.25", TurnedOff: true, ControlMode: localme.ModeNone},
			"AMT 16.1 · turned off", []string{"Turned off", "16.1.25", "Not activated", "Unknown"}, []string{"On"}},
		{"CCM", localme.Result{Found: true, Version: "16.1.25", ControlMode: localme.ModeCCM,
			Wireless: &localme.Link{LinkUp: true, IP: "192.0.2.51"}, Wired: &localme.Link{Enabled: true}},
			"AMT 16.1 · CCM", []string{"On", "Client Control Mode (CCM)", "Wireless · 192.0.2.51 · Static"}, nil},
		{"ACM", localme.Result{Found: true, Version: "18.0.5", ControlMode: localme.ModeACM, Wired: &localme.Link{Enabled: true}},
			"AMT 18.0 · ACM", []string{"Admin Control Mode (ACM)", "Wired · No link"}, nil},
		{"not found", localme.Result{Reason: localme.ReasonNoDriver},
			"No Intel AMT found", []string{"The Intel ME did not report Intel AMT. Possible reasons:", "Intel AMT is turned off in the BIOS.",
				"The Intel ME driver is missing or outdated.", "This computer is not a vPro platform."}, []string{"16.1"}},
		{"unclear", localme.Result{Reason: localme.ReasonNoAMT, Detail: "AMT Not Found"},
			"No Intel AMT found", []string{"Intel AMT is turned off in the BIOS."}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeCheck{r: c.r}
			s := newCheckStart(t, newCheckApp(t), f)
			before := time.Now()
			test.Tap(s.checkBtn)
			if f.calls != 1 || s.localChecking || s.checkBtn.Disabled() {
				t.Fatalf("calls=%d checking=%v disabled=%v", f.calls, s.localChecking, s.checkBtn.Disabled())
			}
			if s.localRow.Host() != c.subline || s.checkBtn.Text != "Check Again" {
				t.Fatalf("subline %q, button %q", s.localRow.Host(), s.checkBtn.Text)
			}
			texts := cardTexts(s)
			for _, want := range c.card {
				if !hasText(s.localCard, want) && !strings.Contains(texts, want) {
					t.Errorf("card lacks %q: %s", want, texts)
				}
			}
			for _, not := range c.notCard {
				if hasText(s.localCard, not) {
					t.Errorf("card shows %q", not)
				}
			}
			if strings.Contains(strings.ToLower(texts), "not support") {
				t.Errorf("card says the hardware does not support AMT: %s", texts)
			}
			if c.r.Found {
				if !s.localLast.Checked.After(before.Add(-time.Second)) || !hasText(s.localCard, formatCheckTime(s.localLast.Checked)) {
					t.Errorf("last check %v not shown", s.localLast.Checked)
				}
			}
		})
	}
}

// TestLocalCheckIsStored (V15): the last result survives a restart and shows as the subline.
func TestLocalCheckIsStored(t *testing.T) {
	a := newCheckApp(t)
	f := &fakeCheck{r: localme.Result{Found: true, Version: "16.1.25", ControlMode: localme.ModeNone, UUID: "u", Wired: wiredDHCP()}}
	s := newCheckStart(t, a, f)
	test.Tap(s.checkBtn)
	stored := a.Preferences().String(prefLocalCheck)
	if !strings.Contains(stored, `"version":"16.1.25"`) || strings.Contains(strings.ToLower(stored), "password") {
		t.Fatalf("stored %q", stored)
	}

	again := newCheckStart(t, a, &fakeCheck{})
	if again.localRow.Host() != "AMT 16.1 · not activated" || again.checkBtn.Text != "Check Again" || !hasText(again.localCard, "Not activated") {
		t.Fatalf("after restart: subline %q, button %q", again.localRow.Host(), again.checkBtn.Text)
	}
	// A damaged value counts as not checked.
	a.Preferences().SetString(prefLocalCheck, "{")
	damaged := newCheckStart(t, a, &fakeCheck{})
	if damaged.localRow.Host() != "Intel ME found, not checked" || damaged.checkBtn.Text != "Check" {
		t.Fatalf("damaged: %q %q", damaged.localRow.Host(), damaged.checkBtn.Text)
	}
}

// TestLocalCheckDeclined: refused administrator rights show the info and change nothing.
func TestLocalCheckDeclined(t *testing.T) {
	a := newCheckApp(t)
	s := newCheckStart(t, a, &fakeCheck{err: localme.ErrDeclined})
	var shown []error
	s.onError = func(err error) { shown = append(shown, err) }
	test.Tap(s.checkBtn)
	texts, _, _ := dialogTexts(s.win)
	if !strings.Contains(strings.Join(texts, "|"), "Administrator rights were not granted. Nothing was changed.") || len(shown) != 0 {
		t.Fatalf("dialog %q, errors %v", texts, shown)
	}
	if s.localLast != nil || a.Preferences().String(prefLocalCheck) != "" || s.localRow.Host() != "Intel ME found, not checked" ||
		!hasText(s.localCard, "Not checked yet") || s.checkBtn.Disabled() {
		t.Fatal("a declined check changed the state")
	}
}

// TestLocalCheckFailureKeepsLastResult: a helper that fails shows the error and keeps the
// stored result.
func TestLocalCheckFailureKeepsLastResult(t *testing.T) {
	a := newCheckApp(t)
	NewSettings(a.Preferences()).SetLastLocalCheck(localme.Result{Found: true, Version: "16.1.25", ControlMode: localme.ModeCCM})
	f := &fakeCheck{err: errors.New("helper crashed")}
	s := newCheckStart(t, a, f)
	var shown []error
	s.onError = func(err error) { shown = append(shown, err) }
	test.Tap(s.checkBtn)
	if len(shown) != 1 || s.localRow.Host() != "AMT 16.1 · CCM" || s.checkBtn.Disabled() {
		t.Fatalf("errors %v, subline %q", shown, s.localRow.Host())
	}
	if !strings.Contains(describe(localme.ErrNoPkexec), "pkexec") {
		t.Fatalf("no pkexec: %q", describe(localme.ErrNoPkexec))
	}
}

// TestLocalCheckRunsOnce: while the helper runs, Check is disabled and a second tap starts
// nothing.
func TestLocalCheckRunsOnce(t *testing.T) {
	f := &fakeCheck{r: localme.Result{Reason: localme.ReasonNoAMT}}
	s := newCheckStart(t, newCheckApp(t), f)
	var pending func()
	s.localRun = func(fn func()) { pending = fn }
	test.Tap(s.checkBtn)
	if !s.checkBtn.Disabled() || !s.localChecking {
		t.Fatal("Check enabled while it runs")
	}
	s.runLocalCheck()
	if pending == nil || f.calls != 0 {
		t.Fatalf("calls=%d", f.calls)
	}
	pending()
	if f.calls != 1 || s.checkBtn.Disabled() || s.localRow.Host() != "No Intel AMT found" {
		t.Fatalf("calls=%d disabled=%v", f.calls, s.checkBtn.Disabled())
	}
}

// TestLocalCheckBetaMarker: the "Beta" pill sits next to Check, the two hints below.
func TestLocalCheckBetaMarker(t *testing.T) {
	s := newCheckStart(t, newCheckApp(t), &fakeCheck{})
	if !hasText(s.betaPill, "Beta") || !hasText(s.localActions, "Beta") {
		t.Fatal("no Beta pill")
	}
	for _, h := range []string{"Asks for administrator rights.", "Uses a preview of Intel’s rpc-go library."} {
		if !hasText(s.localActions, h) {
			t.Errorf("hint %q missing", h)
		}
	}
	w := test.NewWindow(s.win.Content())
	defer w.Close()
	w.Resize(fyne.NewSize(1000, 660))
	pill := fyne.CurrentApp().Driver().AbsolutePositionForObject(s.betaPill)
	btn := fyne.CurrentApp().Driver().AbsolutePositionForObject(s.checkBtn)
	if pill.X >= btn.X || pill.Y+s.betaPill.Size().Height < btn.Y || pill.Y > btn.Y+s.checkBtn.Size().Height {
		t.Fatalf("pill at %v, button at %v", pill, btn)
	}
}

// TestActivationRowHasHelp: the Activation row carries the ⓘ of #8.
func TestActivationRowHasHelp(t *testing.T) {
	s := newCheckStart(t, newCheckApp(t), &fakeCheck{r: localme.Result{Found: true, Version: "16.1.25", ControlMode: localme.ModeCCM}})
	test.Tap(s.checkBtn)
	var help *widgets.InfoButton
	walk(s.localCard, func(c fyne.CanvasObject) {
		if b, ok := c.(*widgets.InfoButton); ok {
			help = b
		}
	})
	if help == nil || help.Content.Title != "Activation" || help.Content.What == "" {
		t.Fatalf("help %+v", help)
	}
}

// TestLocalCheckShots writes the states of "This computer" as PNGs to $OPENLAMT_SHOTS.
func TestLocalCheckShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*localme.Result{
		"local-not-checked.png":   nil,
		"local-not-activated.png": {Found: true, Version: "16.1.25", ControlMode: localme.ModeNone, Wired: wiredDHCP()},
		"local-turned-off.png":    {Found: true, Version: "16.1.25", TurnedOff: true, ControlMode: localme.ModeNone},
		"local-ccm.png":           {Found: true, Version: "16.1.25", ControlMode: localme.ModeCCM, Wired: wiredDHCP()},
		"local-none.png":          {Reason: localme.ReasonNoDriver},
	} {
		f := &fakeCheck{}
		if r != nil {
			f.r = *r
		}
		s := newCheckStart(t, newCheckApp(t), f)
		if r != nil {
			test.Tap(s.checkBtn)
		}
		out, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		shotPNG(t, out, s.win)
		out.Close()
	}
}
