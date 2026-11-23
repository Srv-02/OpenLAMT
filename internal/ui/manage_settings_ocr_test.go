package ui

import (
	"fmt"
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// The row "One Click Recovery (HTTPS boot)" shows only where the firmware supports HTTPS boot
// and the device has the boot service of AMT 16.
func TestSettingsOCRRowFollowsTheDevice(t *testing.T) {
	_, p := settingsHarness(t, nil)
	if p.ocrRow.visibleRow() {
		t.Fatal("row shown on a device without HTTPS boot")
	}
	_, p = settingsHarness(t, func(f *session.Fake) {
		f.BootFake().Info = amt.BootInfo{Caps: amt.BootCapabilities{HTTPSBoot: true}, OCR: amt.OCRState{Known: true, Enabled: true}}
	})
	if !p.ocrRow.visibleRow() || !p.ocr.Checked() || p.ocr.Disabled() {
		t.Fatalf("visible %v checked %v disabled %v", p.ocrRow.visibleRow(), p.ocr.Checked(), p.ocr.Disabled())
	}
}

// The switch writes at once in both directions (no confirmation) and reads again.
func TestSettingsOCRSwitchWrites(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) {
		f.BootFake().Info = amt.BootInfo{Caps: amt.BootCapabilities{HTTPSBoot: true}, OCR: amt.OCRState{Known: true}}
	})
	test.Tap(p.ocr)
	h.written(t)
	if got := h.dev.BootFake().OCRWrites(); !slices.Equal(got, []bool{true}) || !p.ocr.Checked() {
		t.Fatalf("writes %v checked %v", got, p.ocr.Checked())
	}
	test.Tap(p.ocr)
	h.written(t)
	if got := h.dev.BootFake().OCRWrites(); !slices.Equal(got, []bool{true, false}) || p.ocr.Checked() {
		t.Fatalf("writes %v checked %v", got, p.ocr.Checked())
	}
}

// A BIOS with HTTPS boot off refuses: "Turn it on in the BIOS first."; the switch stays off.
func TestSettingsOCRRefusedByBIOS(t *testing.T) {
	h, p := settingsHarness(t, func(f *session.Fake) {
		f.BootFake().Info = amt.BootInfo{Caps: amt.BootCapabilities{HTTPSBoot: true}, OCR: amt.OCRState{Known: true}}
		f.BootFake().OCRErr = fmt.Errorf("set one click recovery: %w", amt.ErrOCROffInBIOS)
	})
	test.Tap(p.ocr)
	h.written(t)
	if p.ocr.Checked() || p.d.writeErr == nil || describe(p.d.writeErr) != "Turn it on in the BIOS first." {
		t.Fatalf("checked %v err %v", p.ocr.Checked(), p.d.writeErr)
	}
}
