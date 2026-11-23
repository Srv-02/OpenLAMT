package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// TestBootActionsGreyedWhileAsleep (issue #142): while the device is read in Sleep - Light or
// Deep (S3), Reboot to BIOS, Boot from ISO and Boot Options are greyed, because waking does not
// restart it; Power On and the other actions stay. Hibernate starts through the BIOS and offers
// them; after the device was read as on again they come back.
func TestBootActionsGreyedWhileAsleep(t *testing.T) {
	for _, ps := range []models.PowerState{models.PowerStateSleepLight, models.PowerStateSleepDeep, models.PowerStateHibernate} {
		h := bootWindow(t, ps, fullBoot(), false)
		h.d.media.phase = mediaMounted // Boot from ISO needs an image
		s3 := ps != models.PowerStateHibernate
		if h.d.powerItems[amt.BootToBIOS].Disabled != s3 || h.d.powerAllowed(amt.BootToISO) == s3 || h.d.boot.item.Disabled != s3 {
			t.Fatalf("%v: Reboot to BIOS disabled %v, Boot from ISO allowed %v, Boot Options disabled %v",
				ps, h.d.powerItems[amt.BootToBIOS].Disabled, h.d.powerAllowed(amt.BootToISO), h.d.boot.item.Disabled)
		}
		if h.d.powerItems[amt.PowerOn].Disabled || h.d.powerItems[amt.PowerReset].Disabled {
			t.Fatalf("%v: Power On or Reset greyed", ps)
		}
		h.d.media.phase = mediaIdle
		if !s3 {
			continue
		}
		h.d.run(amt.BootToBIOS)
		h.d.openBootOptions()
		if h.d.powerBusy || h.d.boot.opening || h.dev.BootConsents() != 0 {
			t.Fatalf("%v: a greyed action started", ps)
		}
		h.dev.SetStatus(amt.Status{PowerState: models.PowerStateOn})
		h.d.poller.Refresh()
		recv(t, h.applied)
		if h.d.powerItems[amt.BootToBIOS].Disabled || h.d.boot.item.Disabled {
			t.Fatalf("%v: still greyed after the device was read as on", ps)
		}
	}
}

// TestEraseGreyedWhileAsleep (issue #142): "Erase…" in the danger zone is greyed while the
// device window reads the device in S3 and comes back once it is read as on; Hibernate keeps it.
func TestEraseGreyedWhileAsleep(t *testing.T) {
	h, p := eraseHarness(t, true, withBoot(rpeInfo(allRPE, true)))
	if p.erase.Disabled() {
		t.Fatal("greyed on a running device")
	}
	for _, c := range []struct {
		ps     models.PowerState
		greyed bool
	}{
		{models.PowerStateSleepDeep, true}, {models.PowerStateOn, false}, {models.PowerStateSleepLight, true},
		{models.PowerStateHibernate, false},
	} {
		h.st.PowerState = c.ps
		h.m.statusChanged()
		if p.erase.Disabled() != c.greyed {
			t.Fatalf("%v: greyed %v", c.ps, p.erase.Disabled())
		}
	}
	h.st.PowerState = models.PowerStateSleepDeep
	h.m.statusChanged()
	p.openErase()
	if p.eraseDlg != nil {
		t.Fatal("the erase dialog opened on a sleeping device")
	}
	nothingErased(t, h)
}

// The device window passes every status read to its device management (statusChanged).
func TestDeviceWindowTellsManagerTheSleepState(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, rpeInfo(allRPE, true), false)
	m := h.d.ensureManager()
	h.dev.SetStatus(amt.Status{PowerState: models.PowerStateSleepLight})
	h.d.poller.Refresh()
	recv(t, h.applied)
	if !m.s3 || !m.readS3() {
		t.Fatal("device management did not follow the sleep state")
	}
}

// A sleep refusal after the temporary root was added, whose removal failed, does not claim that
// nothing was changed (issue #142).
func TestDescribeAsleepWithRootLeftBehind(t *testing.T) {
	englishUI(t)
	asleep := fmt.Errorf("boot: %w", amt.ErrAsleep)
	if got := describe(asleep); !strings.HasSuffix(got, "Nothing was changed.") {
		t.Fatalf("asleep: %q", got)
	}
	left := errors.Join(asleep, fmt.Errorf("%w: %w", session.ErrRootNotRemoved, errors.New("connection reset")))
	if got := describe(left); strings.Contains(got, "Nothing was changed") || !strings.Contains(got, "OpenLAMT recovery (temporary)") {
		t.Fatalf("root left: %q", got)
	}
}

// Every cause of a failed boot from a file names the leftover temporary root and never claims that
// nothing was changed (issue #149).
func TestDescribeRootLeftBehindForEveryCause(t *testing.T) {
	englishUI(t)
	left := fmt.Errorf("%w: %w", session.ErrRootNotRemoved, errors.New("connection reset"))
	for name, cause := range map[string]error{
		"invalid value":   fmt.Errorf("boot from file: %w", amt.ErrInvalidValue),
		"connection lost": context.DeadlineExceeded,
		"may be set":      &amt.BootMayBeSetError{Err: context.DeadlineExceeded},
	} {
		got := describe(errors.Join(cause, left))
		if strings.Contains(got, "Nothing was changed") || !strings.Contains(got, "Delete “OpenLAMT recovery (temporary)” under Device management › Certificates.") {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := describe(errors.Join(fmt.Errorf("boot from file: %w", amt.ErrInvalidValue), left)); !strings.HasPrefix(got, "Some of the values you entered are not valid.") {
		t.Errorf("cause missing: %q", got)
	}
}
