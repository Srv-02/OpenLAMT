package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/i18n"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

func settingsParts(t *testing.T, w fyne.Window) (entry *escEntry, drops []*widgets.Dropdown, sw *widgets.Switch) {
	t.Helper()
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *escEntry:
			entry = v
		case *widgets.Dropdown:
			drops = append(drops, v)
		case *widgets.Switch:
			sw = v
		}
	})
	if entry == nil || len(drops) != 5 || sw == nil {
		t.Fatalf("settings dialog incomplete: %v %d %v", entry, len(drops), sw)
	}
	return
}

// Save is disabled while the interval is invalid, and tapping it then changes nothing.
func TestSettingsSaveDisabledForInvalidInterval(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	showSettingsDialog(s.win, &s.settings)
	entry, drops, _ := settingsParts(t, s.win)
	_, _, buttons := dialogTexts(s.win)
	save := buttons[lang.L("Save")]
	if save.Disabled() {
		t.Fatal("Save disabled for a valid interval")
	}
	for _, bad := range []string{"", "0", "abc", "3601", "-5", "1.5"} {
		entry.SetText(bad)
		if !save.Disabled() {
			t.Errorf("Save enabled for %q", bad)
		}
	}
	drops[3].SetSelected("Never") // idle
	test.Tap(save)
	if s.win.Canvas().Overlays().Top() == nil || s.settings.KVMIdleTimeout() == 0 {
		t.Fatal("a disabled Save stored or closed")
	}
	entry.SetText("3600")
	if save.Disabled() {
		t.Fatal("Save still disabled for 3600")
	}
	entry.OnSubmitted("3600") // Enter in the field saves
	if s.settings.PollInterval() != time.Hour || s.win.Canvas().Overlays().Top() != nil {
		t.Fatalf("Enter did not save: %v", s.settings.PollInterval())
	}
}

func TestSettingsCancelAndEscKeepValues(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	c := s.win.Canvas()
	for name, closeIt := range map[string]func(){
		"Cancel":        func() { _, _, b := dialogTexts(s.win); test.Tap(b[lang.L("Cancel")]) },
		"Esc":           func() { c.OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape}) },
		"Esc in entry":  func() { e, _, _ := settingsParts(t, s.win); e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape}) },
		"Esc on switch": func() { _, _, sw := settingsParts(t, s.win); sw.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape}) },
		"Esc on choice": func() { _, d, _ := settingsParts(t, s.win); d[0].TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape}) },
	} {
		showSettingsDialog(s.win, &s.settings)
		e, d, sw := settingsParts(t, s.win)
		e.SetText("99")
		d[3].SetSelected("Never") // idle
		sw.SetChecked(true)
		closeIt()
		if c.Overlays().Top() != nil {
			t.Errorf("%s: dialog still open", name)
		}
		if s.settings.PollInterval() != 10*time.Second || s.settings.KVMIdleTimeout() != DefaultKVMIdleTimeout || s.settings.KVMAutoConnect() {
			t.Errorf("%s: stored %v %v %v", name, s.settings.PollInterval(), s.settings.KVMIdleTimeout(), s.settings.KVMAutoConnect())
		}
	}
}

// The choice fields open the design's menu below them; choosing an entry sets it.
func TestSettingsDropdownMenuChoosesOption(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	showSettingsDialog(s.win, &s.settings)
	_, drops, _ := settingsParts(t, s.win)
	layout := drops[4]
	test.Tap(layout)
	m := layout.Menu()
	if m == nil || !m.Visible() {
		t.Fatal("menu not shown")
	}
	test.Tap(m.Item(3)) // Automatic, Danish, English (UK), English (US)
	if layout.Selected != "English (US)" || m.Visible() {
		t.Fatalf("selected %q, menu visible %v", layout.Selected, m.Visible())
	}
}

// Save stores the language and, only when it changed, says that it applies at the next start.
func TestSettingsLanguageSaveShowsRestartHint(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	hint := lang.L("The language changes the next time OpenLAMT starts.")
	hasHint := func() bool {
		texts, _, _ := dialogTexts(s.win)
		for _, x := range texts {
			if x == hint {
				return true
			}
		}
		return false
	}
	showSettingsDialog(s.win, &s.settings)
	_, _, buttons := dialogTexts(s.win)
	test.Tap(buttons[lang.L("Save")])
	if s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("unchanged language: a dialog stayed open")
	}
	showSettingsDialog(s.win, &s.settings)
	_, drops, _ := settingsParts(t, s.win)
	drops[0].SetSelected("Deutsch") // language
	_, _, buttons = dialogTexts(s.win)
	test.Tap(buttons[lang.L("Save")])
	if s.settings.Language() != i18n.German {
		t.Fatalf("stored %q", s.settings.Language())
	}
	if !hasHint() {
		t.Fatal("no restart hint after changing the language")
	}
}

// settingsOrder is the order of the rows in the settings dialog, top to bottom and for Tab alike.
var settingsOrder = []string{
	"Language", "Appearance", "Color blind mode", "Check for updates", "Poll interval",
	"KVM quality", "KVM auto-connect", "Disconnect KVM without input after", "Type clipboard layout",
}

// The rows show in settingsOrder, and Tab moves through their controls in the same order.
func TestSettingsRowOrder(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	showSettingsDialog(s.win, &s.settings)

	texts, _, _ := dialogTexts(s.win)
	var labels []string
	for _, x := range texts {
		for _, want := range settingsOrder {
			if x == lang.L(want) {
				labels = append(labels, want)
			}
		}
	}
	if !slices.Equal(labels, settingsOrder) {
		t.Fatalf("rows %q, want %q", labels, settingsOrder)
	}

	// The control of each row, named by its row: the choice fields by their options, the
	// switches by their position among the switches.
	entry, drops, _ := settingsParts(t, s.win)
	dropRow := map[*widgets.Dropdown]string{}
	for _, d := range drops {
		switch {
		case slices.Contains(d.Options, "Deutsch"):
			dropRow[d] = "Language"
		case slices.Contains(d.Options, lang.L("Dark")):
			dropRow[d] = "Appearance"
		case slices.Contains(d.Options, lang.L("Never")):
			dropRow[d] = "Disconnect KVM without input after"
		case slices.Contains(d.Options, lang.L("Automatic (system language)")):
			dropRow[d] = "Type clipboard layout"
		default:
			dropRow[d] = "KVM quality"
		}
	}
	switchRows := []string{"Color blind mode", "Check for updates", "KVM auto-connect"}

	c := s.win.Canvas()
	var got []string
	switches := 0
	for range 2 * len(settingsOrder) {
		c.FocusNext()
		switch v := c.Focused().(type) {
		case *escEntry:
			if v == entry {
				got = append(got, "Poll interval")
			}
		case *widgets.Dropdown:
			got = append(got, dropRow[v])
		case *widgets.Switch:
			if switches < len(switchRows) {
				got = append(got, switchRows[switches])
			}
			switches++
		}
		if len(got) == len(settingsOrder) {
			break
		}
	}
	if !slices.Equal(got, settingsOrder) {
		t.Fatalf("Tab order %q, want %q", got, settingsOrder)
	}
}

// The last row "Hidden messages" explains itself and has "Reset Warnings" at the right, which is
// disabled while nothing is hidden and resets at once (not on Save).
func TestSettingsHiddenMessagesRow(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	showSettingsDialog(s.win, &s.settings)
	texts, _, buttons := dialogTexts(s.win)
	reset := buttons["Reset Warnings"]
	if reset == nil || !reset.Disabled() {
		t.Fatal("Reset Warnings missing or enabled without a hidden message")
	}
	i := slices.Index(texts, "Hidden messages")
	if i < 0 || i < slices.Index(texts, "Type clipboard layout") {
		t.Fatalf("Hidden messages is not the last row: %q", texts)
	}
	const full = "Show all messages again that were hidden with “Don’t show again”."
	var lines []string
	for _, x := range texts[i+1:] {
		if x == "" || !strings.Contains(full, x) {
			break
		}
		lines = append(lines, x)
	}
	if got := strings.Join(lines, " "); got != full {
		t.Fatalf("explanation %q", got)
	}
	// The explanation stays left of the button.
	var label fyne.CanvasObject
	walk(s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if x, ok := o.(*canvas.Text); ok && strings.HasPrefix(x.Text, "Show all messages") {
			label = x
		}
	})
	abs := fyne.CurrentApp().Driver().AbsolutePositionForObject
	if right := abs(label).X + label.MinSize().Width; right > abs(reset).X {
		t.Fatalf("explanation ends at %v, button starts at %v", right, abs(reset).X)
	}
	s.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})

	s.settings.SetHideFolderMountHint(true)
	showSettingsDialog(s.win, &s.settings)
	_, _, buttons = dialogTexts(s.win)
	reset = buttons["Reset Warnings"]
	if reset.Disabled() {
		t.Fatal("Reset Warnings disabled with a hidden message")
	}
	test.Tap(reset)
	if s.settings.HideFolderMountHint() || !reset.Disabled() || s.win.Canvas().Overlays().Top() == nil {
		t.Fatal("Reset Warnings did not reset at once, or closed the dialog")
	}
}
