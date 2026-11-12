package ui

import (
	"errors"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

var (
	statesNoLMS = []power.PowerState{5, 8, 2, 10, 11}     // measured on AMT 16.1 without LMS
	statesLMS   = []power.PowerState{2, 5, 8, 10, 11, 12} // with Off - Soft Graceful
)

func shutdownDevice(t *testing.T, ps models.PowerState, states []power.PowerState, statesErr error) *deviceHarness {
	t.Helper()
	englishUI(t)
	dev := &session.Fake{FP: fp, St: amt.Status{PowerState: ps}, States: states, StatesErr: statesErr}
	h := newDevice(t, test.NewTempApp(t), dev, true)
	h.show(t)
	return h
}

// The Power menu: Power On, Power Off | Reset, Power Cycle, Reboot to BIOS, Boot from ISO | Shutdown.
func TestPowerMenuOrder(t *testing.T) {
	h := shutdownDevice(t, models.PowerStateOn, statesLMS, nil)
	var got []string
	for _, it := range h.d.powerMenu.Items {
		if it.IsSeparator {
			got = append(got, "|")
			continue
		}
		got = append(got, it.Label)
	}
	want := []string{"Power On", "Power Off…", "|", "Reset…", "Power Cycle…", "Reboot to BIOS…", "Boot from ISO…", "|", "Shutdown…"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("menu %q, want %q", got, want)
	}
}

func TestShutdownGreyedWithoutGraceful(t *testing.T) {
	h := shutdownDevice(t, models.PowerStateOn, statesNoLMS, nil)
	if !h.d.powerItems[amt.PowerShutdown].Disabled || h.d.powerItems[amt.PowerOff].Disabled {
		t.Fatal("Shutdown offered although the device does not list 12")
	}
	h.d.confirm = func(amt.PowerAction, func()) { t.Fatal("confirm for a locked entry") }
	h.d.run(amt.PowerShutdown)
}

func TestShutdownGreyedWhenStatesUnknown(t *testing.T) {
	englishUI(t)
	// Before the poller's first read only Session.Open's status is known, without power states.
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn}, States: statesLMS}, true)
	if !h.d.powerItems[amt.PowerShutdown].Disabled {
		t.Fatal("Shutdown offered before the power states were read")
	}
	// A failed read of the power states.
	h = shutdownDevice(t, models.PowerStateOn, nil, errors.New("boom"))
	if !h.d.powerItems[amt.PowerShutdown].Disabled {
		t.Fatal("Shutdown offered after a failed read of the power states")
	}
}

func TestShutdownLockedWhenOff(t *testing.T) {
	for _, ps := range []models.PowerState{models.PowerStateOffHard, models.PowerStateOffSoft} {
		h := shutdownDevice(t, ps, statesLMS, nil)
		if !h.d.powerItems[amt.PowerShutdown].Disabled {
			t.Fatalf("%v: Shutdown offered on a machine that is off", ps)
		}
	}
}

// With 12 listed and the machine on, Shutdown asks and then sends the action.
func TestShutdownAsksAndSends(t *testing.T) {
	h := shutdownDevice(t, models.PowerStateOn, statesLMS, nil)
	if h.d.powerItems[amt.PowerShutdown].Disabled {
		t.Fatal("Shutdown greyed although the device lists 12")
	}
	var asked []amt.PowerAction
	h.d.confirm = func(act amt.PowerAction, proceed func()) { asked = append(asked, act); proceed() }
	h.tapPower(t, amt.PowerShutdown)
	recv(t, h.actionDone)
	if len(asked) != 1 || asked[0] != amt.PowerShutdown {
		t.Fatalf("asked %v", asked)
	}
	if got := h.dev.Powered(); len(got) != 1 || got[0] != amt.PowerShutdown {
		t.Fatalf("powered %v", got)
	}
}

// LMS starts after the window connected: opening the Power menu reads the power states again
// and the open menu offers Shutdown.
func TestShutdownFollowsPowerStatesOnMenuOpen(t *testing.T) {
	h := shutdownDevice(t, models.PowerStateOn, statesNoLMS, nil)
	h.dev.SetPowerStates(statesLMS, nil)
	test.Tap(h.d.powerBtn)
	recv(t, h.applied) // the status, with the states known so far
	recv(t, h.applied) // the changed power states
	if h.d.powerPopup == nil || h.d.powerItems[amt.PowerShutdown].Disabled {
		t.Fatal("open menu did not offer Shutdown after LMS started")
	}
	h.d.powerPopup.Dismiss()
}
