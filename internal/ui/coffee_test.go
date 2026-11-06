package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

const day = 24 * time.Hour

// startDue simulates one program start: count it and say whether the nudge is due.
func startDue(s coffeeSchedule) bool {
	s.RegisterStart()
	return s.Due()
}

func TestCoffeeScheduleFirstAtThirdStartThen14ThenEvery90Days(t *testing.T) {
	a := test.NewTempApp(t)
	clk := &fakeClock{t: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	s := newCoffeeSchedule(a.Preferences(), clk.now)

	if startDue(s) || startDue(s) {
		t.Fatal("start 1 and 2 must show nothing")
	}
	if !startDue(s) {
		t.Fatal("start 3 must show")
	}
	s.MarkShown()

	clk.advance(13 * day)
	if startDue(s) {
		t.Fatal("13 days after the first time: nothing")
	}
	clk.advance(day)
	if !startDue(s) {
		t.Fatal("day 14: show")
	}
	s.MarkShown()

	clk.advance(89 * day)
	if startDue(s) {
		t.Fatal("89 days after the second time: nothing")
	}
	clk.advance(day)
	if !startDue(s) {
		t.Fatal("day 90: show")
	}
	s.MarkShown()

	clk.advance(89 * day)
	if startDue(s) {
		t.Fatal("89 days after the third time: nothing")
	}
	clk.advance(day)
	if !startDue(s) {
		t.Fatal("day 90 again: show")
	}
}

func TestCoffeeScheduleSkippedShowStaysDue(t *testing.T) {
	a := test.NewTempApp(t)
	clk := &fakeClock{t: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	s := newCoffeeSchedule(a.Preferences(), clk.now)
	startDue(s)
	startDue(s)
	if !startDue(s) { // start 3, not marked (no quiet moment)
		t.Fatal("start 3 must be due")
	}
	if !startDue(s) { // start 4 checks again
		t.Fatal("still due at the next start when nothing was shown")
	}
}

// nudgeHarness is a start window with the nudge enabled and no timer (tests call coffeeFire).
func nudgeHarness(t *testing.T, starts int) (*startHarness, *fakeClock) {
	t.Helper()
	h := newStart(t)
	clk := &fakeClock{t: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	pre := newCoffeeSchedule(h.s.app.Preferences(), clk.now)
	for range starts - 1 {
		pre.RegisterStart()
	}
	h.s.enableCoffee(clk.now, -1) // registers the last start
	return h, clk
}

func TestCoffeeNotShownWithoutEnabling(t *testing.T) {
	h := newStart(t)
	h.s.coffeeFire() // no-op without enableCoffee
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("a window that never enabled the nudge must stay quiet")
	}
}

func TestCoffeeShownAtThirdStartWithCoffeeAndLater(t *testing.T) {
	h, _ := nudgeHarness(t, 3)
	h.s.coffeeFire()
	texts, _, buttons := dialogTexts(h.s.win)
	all := strings.Join(texts, "|")
	for _, want := range []string{"OpenLAMT", "Support OpenLAMT"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q missing: %q", want, all)
		}
	}
	if buttons["Buy me a coffee"] == nil || buttons["Maybe later"] == nil || len(buttons) != 2 {
		t.Fatalf("buttons: %v", buttons)
	}
}

func TestCoffeeNotShownBeforeThirdStart(t *testing.T) {
	h, _ := nudgeHarness(t, 2)
	h.s.coffeeFire()
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("start 2 must show nothing")
	}
}

func TestCoffeeButtonOpensURLAndCloses(t *testing.T) {
	opened := urlsOpened(t)
	h, _ := nudgeHarness(t, 3)
	h.s.coffeeFire()
	_, _, buttons := dialogTexts(h.s.win)
	test.Tap(buttons["Buy me a coffee"])
	if len(*opened) != 1 || (*opened)[0] != "https://buymeacoffee.com/michaelmayer" {
		t.Fatalf("opened %v", *opened)
	}
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("dialog still open")
	}
}

func TestCoffeeLaterAndEscClose(t *testing.T) {
	opened := urlsOpened(t)
	h, _ := nudgeHarness(t, 3)
	h.s.coffeeFire()
	_, _, buttons := dialogTexts(h.s.win)
	test.Tap(buttons["Maybe later"])
	if h.s.win.Canvas().Overlays().Top() != nil || len(*opened) != 0 {
		t.Fatalf("Later: overlay %v opened %v", h.s.win.Canvas().Overlays().Top(), *opened)
	}

	h2, _ := nudgeHarness(t, 3)
	h2.s.coffeeFire()
	h2.s.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if h2.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("Esc must close")
	}
}

func TestCoffeeShownOnlyOncePerSchedule(t *testing.T) {
	h, clk := nudgeHarness(t, 3)
	h.s.coffeeFire()
	_, _, buttons := dialogTexts(h.s.win)
	test.Tap(buttons["Maybe later"])
	clk.advance(13 * day)
	h.s.enableCoffee(clk.now, -1)
	h.s.coffeeFire()
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("13 days later nothing")
	}
	clk.advance(day)
	h.s.enableCoffee(clk.now, -1)
	h.s.coffeeFire()
	if h.s.win.Canvas().Overlays().Top() == nil {
		t.Fatal("day 14 shows")
	}
}

func TestCoffeeQuietRules(t *testing.T) {
	const key = "admin@host.example.com"
	cases := map[string]func(h *startHarness){
		"device window open":  func(h *startHarness) { h.s.open[key] = &DeviceWindow{} },
		"connect in progress": func(h *startHarness) { h.s.busy[key] = "x" },
		"another dialog open": func(h *startHarness) { showAboutDialog(h.s.win, h.s.app) },
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			h, clk := nudgeHarness(t, 3)
			block(h)
			before := h.s.win.Canvas().Overlays().Top()
			h.s.coffeeFire()
			if h.s.win.Canvas().Overlays().Top() != before {
				t.Fatal("must not show")
			}
			// Not marked as shown: the next start checks again and shows when it is quiet.
			delete(h.s.open, key)
			delete(h.s.busy, key)
			if before != nil {
				h.s.win.Canvas().Overlays().Remove(before)
			}
			h.s.enableCoffee(clk.now, -1) // next start
			h.s.coffeeFire()
			if h.s.win.Canvas().Overlays().Top() == nil {
				t.Fatal("quiet at the next start: must show")
			}
		})
	}
}

func TestCoffeeShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	_ = os.MkdirAll(dir, 0o755)
	h, _ := nudgeHarness(t, 3)
	h.s.app.Settings().SetTheme(style.Theme{})
	h.s.coffeeFire()
	f, err := os.Create(dir + "/coffee.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	shotPNG(t, f, h.s.win)
}
