package ui

import (
	"errors"
	"strings"
	"testing"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
)

// Sleep and Hibernate (issue #27, V20): active only where the device lists the state (4, 7),
// asking with a simple confirmation first.
var (
	statesSleepOnly  = []power.PowerState{2, 4, 5, 8, 10, 11}
	statesSleepHiber = []power.PowerState{2, 4, 5, 7, 8, 10, 11}
)

func TestSleepHibernateGreyedUnlessListed(t *testing.T) {
	for _, c := range []struct {
		name          string
		states        []power.PowerState
		sleep, hibern bool // enabled
	}{
		{"neither", statesNoLMS, false, false},
		{"sleep only", statesSleepOnly, true, false},
		{"both", statesSleepHiber, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := shutdownDevice(t, models.PowerStateOn, c.states, nil)
			if got := !h.d.powerItems[amt.PowerSleep].Disabled; got != c.sleep {
				t.Errorf("Sleep enabled %v, want %v", got, c.sleep)
			}
			if got := !h.d.powerItems[amt.PowerHibernate].Disabled; got != c.hibern {
				t.Errorf("Hibernate enabled %v, want %v", got, c.hibern)
			}
		})
	}
}

func TestSleepHibernateGreyedWhenStatesUnknownOrOff(t *testing.T) {
	h := shutdownDevice(t, models.PowerStateOn, nil, errors.New("boom"))
	if !h.d.powerItems[amt.PowerSleep].Disabled || !h.d.powerItems[amt.PowerHibernate].Disabled {
		t.Fatal("offered after a failed read of the power states")
	}
	for _, ps := range []models.PowerState{models.PowerStateOffHard, models.PowerStateOffSoft} {
		h := shutdownDevice(t, ps, statesSleepHiber, nil)
		if !h.d.powerItems[amt.PowerSleep].Disabled || !h.d.powerItems[amt.PowerHibernate].Disabled {
			t.Fatalf("%v: offered on a machine that is off", ps)
		}
	}
}

func TestSleepAndHibernateAskAndSend(t *testing.T) {
	for _, act := range []amt.PowerAction{amt.PowerSleep, amt.PowerHibernate} {
		h := shutdownDevice(t, models.PowerStateOn, statesSleepHiber, nil)
		var asked []amt.PowerAction
		h.d.confirm = func(a amt.PowerAction, proceed func()) { asked = append(asked, a); proceed() }
		h.tapPower(t, act)
		recv(t, h.actionDone)
		if len(asked) != 1 || asked[0] != act {
			t.Fatalf("%s: asked %v", act, asked)
		}
		if got := h.dev.Powered(); len(got) != 1 || got[0] != act {
			t.Fatalf("%s: powered %v", act, got)
		}
	}
}

func TestSleepDeclinedSendsNothing(t *testing.T) {
	h := shutdownDevice(t, models.PowerStateOn, statesSleepHiber, nil)
	h.d.confirm = func(amt.PowerAction, func()) {} // the user cancels: proceed never runs
	h.d.run(amt.PowerHibernate)
	if len(h.dev.Powered()) != 0 {
		t.Fatal("hibernate sent without confirmation")
	}
}

func TestSleepHibernateConfirmText(t *testing.T) {
	englishUI(t)
	title, msg, button := confirmText(amt.PowerHibernate, "Büro")
	if title != "Hibernate Büro?" || msg != "The operating system saves its memory to disk and switches off." || button != "Hibernate" {
		t.Errorf("hibernate: %q %q %q", title, msg, button)
	}
	title, msg, button = confirmText(amt.PowerSleep, "Büro")
	if title != "Sleep Büro?" || !strings.Contains(msg, "goes to sleep") || button != "Sleep" {
		t.Errorf("sleep: %q %q %q", title, msg, button)
	}
	if r := amt.RiskOf(amt.OpSleepHibernate); r.Level != amt.LevelSimple {
		t.Errorf("level %v, want simple", r.Level)
	}
}
