package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
)

// TestPowerAndBootRefusedWhileAnotherActionRuns (issue #135): the guard of #126 holds in every
// direction. A Reboot to BIOS confirmation or a Boot Options dialog accepted while another power
// action (here Erase drives) holds the lock is refused with the message, sends nothing and keeps
// the running action's consent cancel, which closing the window still reaches.
func TestPowerAndBootRefusedWhileAnotherActionRuns(t *testing.T) {
	for name, start := range map[string]func(*bootHarness){
		"reboot to BIOS": func(h *bootHarness) { h.d.execute(amt.BootToBIOS) },
		"power cycle":    func(h *bootHarness) { h.d.execute(amt.PowerCycle) },
		"boot options":   func(h *bootHarness) { h.d.startBoot(amt.BootOptions{Target: amt.BootPXE}, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			h := bootWindow(t, models.PowerStateOn, fullBoot(), true)
			cancelled := false
			h.d.biosCancel = func() { cancelled = true } // the consent dialog of a running Erase drives
			h.d.setPowerBusy(true)
			start(h)
			if len(h.errs) != 1 || len(h.asked) != 0 || len(h.dev.Powered()) != 0 || len(h.dev.BootFake().Boots()) != 0 {
				t.Fatalf("errors %d asked %d powered %v boots %v", len(h.errs), len(h.asked), h.dev.Powered(), h.dev.BootFake().Boots())
			}
			if err := <-h.errs; describe(err) != "Wait until the running power action ends, then try again." {
				t.Fatalf("message %q", describe(err))
			}
			if !h.d.powerBusy {
				t.Fatal("the refusal unlocked the running action")
			}
			h.d.shutdown()
			if !cancelled {
				t.Fatal("the cancel of the running action was lost")
			}
		})
	}
}

// TestBootOptionsAcceptedDuringEraseIsRefused (issue #135): the path of the issue. The Boot
// Options dialog is open in the device window, Erase drives starts in the popped-out device
// management and waits for its consent; Restart in the dialog is then refused, and the erase
// ends as if alone.
func TestBootOptionsAcceptedDuringEraseIsRefused(t *testing.T) {
	info := fullBoot()
	info.Erase = rpeInfo(allRPE, true).Erase
	h := bootWindow(t, models.PowerStateOn, info, true)
	b := h.open(t)
	b.pick(t, amt.BootPXE)
	c, err := amt.ConfirmChecked(amt.OpErase, "device.example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	o := amt.EraseOptions{Platform: true, Actions: amt.EraseSSDs}
	h.d.startErase(o, c, func(err error) { done <- err })
	answer := recvAnswer(t, h.asked)
	test.Tap(b.action)
	if len(h.errs) != 1 || len(h.asked) != 0 {
		t.Fatalf("errors %d asked %d", len(h.errs), len(h.asked))
	}
	answer("123456", true)
	h.finished(t)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := h.dev.BootFake().Erases(); len(got) != 1 || len(h.dev.BootFake().Boots()) != 0 || h.dev.BootConsents() != 1 {
		t.Fatalf("erases %v boots %v consents %d", got, h.dev.BootFake().Boots(), h.dev.BootConsents())
	}
	if h.d.powerBusy || h.d.biosCancel != nil {
		t.Fatal("state after the erase")
	}
}
