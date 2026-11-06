package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The coffee nudge: an occasional, quiet request for a coffee in the main window.
const (
	prefCoffeeStarts           = "coffee_starts"
	prefCoffeeShown            = "coffee_shown"
	prefCoffeeLast             = "coffee_last_shown"
	coffeeFirstStart           = 3                   // shown at the third start at the earliest
	coffeeSecondAfter          = 14 * 24 * time.Hour // second time, counted from the first
	coffeeLaterAfter           = 90 * 24 * time.Hour // every further time, counted from the previous one
	coffeeDelay                = 4 * time.Second     // after the main window opened
	coffeeDialogWidth  float32 = 480
	coffeeDialogHeight float32 = 316
	coffeeIconSize     float32 = 56
	coffeeTextWidth            = coffeeDialogWidth - 2*aboutPadH - 2*supportPadH - supportAvatar - supportGap
)

// coffeeSchedule keeps the start counter and the last time the nudge was shown in the
// preferences; now is injectable for tests.
type coffeeSchedule struct {
	prefs fyne.Preferences
	now   func() time.Time
}

func newCoffeeSchedule(p fyne.Preferences, now func() time.Time) coffeeSchedule {
	return coffeeSchedule{prefs: p, now: now}
}

// RegisterStart counts one program start.
func (c coffeeSchedule) RegisterStart() {
	c.prefs.SetInt(prefCoffeeStarts, c.prefs.Int(prefCoffeeStarts)+1)
}

// Due reports whether the nudge may be shown now: from the third start on, then 14 days
// after the first time, and 90 days after each further time.
func (c coffeeSchedule) Due() bool {
	if c.prefs.Int(prefCoffeeStarts) < coffeeFirstStart {
		return false
	}
	n := c.prefs.Int(prefCoffeeShown)
	if n == 0 {
		return true
	}
	last, err := time.Parse(time.RFC3339, c.prefs.String(prefCoffeeLast))
	if err != nil {
		return true
	}
	wait := coffeeLaterAfter
	if n == 1 {
		wait = coffeeSecondAfter
	}
	return !c.now().Before(last.Add(wait))
}

// MarkShown records that the nudge was shown now; the coffee button and "Maybe later" both
// continue the same schedule.
func (c coffeeSchedule) MarkShown() {
	c.prefs.SetInt(prefCoffeeShown, c.prefs.Int(prefCoffeeShown)+1)
	c.prefs.SetString(prefCoffeeLast, c.now().Format(time.RFC3339))
}

type coffeeNudge struct {
	sched coffeeSchedule
	timer *time.Timer
}

// EnableCoffeeNudge counts this start and, if the schedule allows it, shows the coffee
// dialog a few seconds after the main window opened. Only main calls it, so no test window
// shows it by accident.
func (s *StartWindow) EnableCoffeeNudge() { s.enableCoffee(time.Now, coffeeDelay) }

// enableCoffee is EnableCoffeeNudge with a clock and delay; a negative delay starts no timer
// (tests call coffeeFire).
func (s *StartWindow) enableCoffee(now func() time.Time, delay time.Duration) {
	if s.coffee != nil && s.coffee.timer != nil {
		s.coffee.timer.Stop()
	}
	n := &coffeeNudge{sched: newCoffeeSchedule(s.app.Preferences(), now)}
	n.sched.RegisterStart()
	s.coffee = n
	if delay >= 0 && n.sched.Due() {
		n.timer = time.AfterFunc(delay, func() { fyne.Do(s.coffeeFire) })
	}
}

// coffeeFire shows the dialog if it is due and the window is quiet: no device window (which
// includes a running KVM session), no connection attempt and no other dialog. Otherwise
// nothing happens and the next start asks again. The update notice goes first: while its check
// is pending the nudge waits for the answer, and in a start that showed it the nudge stays away.
func (s *StartWindow) coffeeFire() {
	n := s.coffee
	if n == nil || !n.sched.Due() {
		return
	}
	if u := s.updates; u != nil {
		if u.pending {
			u.coffeeWaiting = true
			return
		}
		if u.shown {
			return
		}
	}
	if !s.quiet() {
		return
	}
	n.sched.MarkShown()
	showCoffeeDialog(s.win, s.app)
}

func coffeeHeader(a fyne.App) fyne.CanvasObject {
	title := designText("OpenLAMT", style.Size(style.SizeNameDetailHeading), "foreground", true)
	titleRow := container.New(&baselineLayout{gap: 10}, title)
	if v := versionText(a, style.Size(style.SizeNameSmallText)); v != nil {
		titleRow.Add(v)
	}
	return container.New(&rowLayout{gap: aboutGap, fill: 1}, widgets.NewAppIcon(icons.AppFull, coffeeIconSize), titleRow)
}

// showCoffeeDialog is the compact About: header, support card with the coffee button and
// "Maybe later" below it. Esc is "Maybe later".
func showCoffeeDialog(parent fyne.Window, a fyne.App) {
	showDesignDialog(parent, dialogSpec{width: coffeeDialogWidth, height: coffeeDialogHeight, padTop: aboutPadH, padRight: aboutPadH - scrollInset,
		padBottom: 18, padL: aboutPadH - scrollInset, gap: aboutGap, flex: 0},
		func(d *designDialog) []fyne.CanvasObject {
			coffee := pillAction(lang.L("Buy me a coffee"), icons.GetStroke(icons.Coffee, icons.White, 1.9), 15, true,
				func() { openURL(supportURL); d.Close() })
			card := supportCard(lang.L("OpenLAMT is free and open source. If it saves you time, I'd be happy about a coffee."), coffeeTextWidth, coffee)
			scroll := scrollBody(dialogSpec{gap: aboutGap, flex: 2}, coffeeHeader(a), card)
			later := dialogButton(lang.L("Maybe later"), false, 18, d.Close)
			later.OnEscape = d.Close
			return []fyne.CanvasObject{scroll, container.New(&rowLayout{fill: 0}, spacer(), later)}
		})
}
