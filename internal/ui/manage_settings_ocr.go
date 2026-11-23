package ui

import (
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The row "One Click Recovery (HTTPS boot)" under Settings › Remote features (issue #21,
// gesamtdesign "Verwaltung: Settings"): a switch for the AMT boot service, written at once in
// both directions (no confirmation: it only allows a boot target). It shows where the firmware
// supports HTTPS boot and the device has the boot service of AMT 16. A BIOS that has HTTPS boot
// off refuses it: "Turn it on in the BIOS first." (describeBoot).

// ocrSettingsRow builds the row; showOCR fills it.
func (p *settingsPane) ocrSettingsRow() fyne.CanvasObject {
	p.ocr = p.newSwitch(func(on bool) {
		p.d.write(func() error { return p.m.sess.SetOCR(on) }, true)
	})
	title := container.NewHBox(designText(lang.L("One Click Recovery (HTTPS boot)"), 13, style.ColorNameControlText, false), ocrHelpButton())
	desc := designText(lang.L("Also needs to be on in the BIOS."), 12, style.ColorNameTextSecondary, false)
	left := container.New(layout.NewCustomPaddedVBoxLayout(0), title, desc)
	return container.New(settingsRowLayout{}, left, p.ocr)
}

// showOCR shows the row where the device offers One Click Recovery, with its state.
func (p *settingsPane) showOCR() {
	info, ok := p.d.boot.get()
	p.ocrRow.show(ok && info.Caps.HTTPSBoot && info.OCR.Known)
	p.ocr.SetChecked(info.OCR.Enabled)
}

// bootInfoCell holds the boot info of the last settings read. The read runs inside the
// settings read (manageSource), so the page reads and waits once; a device that cannot tell
// (an older one, a refused read) leaves the row hidden.
type bootInfoCell struct {
	mu   sync.Mutex
	info amt.BootInfo
	ok   bool
}

func (c *bootInfoCell) read(s *session.Session) {
	info, err := s.BootInfo()
	c.mu.Lock()
	c.info, c.ok = info, err == nil
	c.mu.Unlock()
}

func (c *bootInfoCell) get() (amt.BootInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info, c.ok
}

// ocrHelpButton is the info button of the row (#8).
func ocrHelpButton() *widgets.InfoButton {
	return widgets.NewInfoButton(widgets.HelpContent{
		Title:   lang.L("One Click Recovery (HTTPS boot)"),
		What:    lang.L("Allows Intel AMT to start the device once from a recovery image over HTTPS, chosen in Power › Boot Options."),
		When:    lang.L("Turn it on to boot a recovery or installation image without a USB stick or a PXE server. HTTPS boot must also be on in the BIOS."),
		Wrong:   lang.L("While it is on, anyone with a power control account can start the device from an image of their choice."),
		Recover: lang.L("Turn it off again here."),
	})
}
