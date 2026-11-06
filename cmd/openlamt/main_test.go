package main

import (
	"runtime"
	"testing"

	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui"
	"github.com/Srv-02/openlamt/internal/ui/i18n"
)

// The stored language is applied at start, whatever the OS language of the test machine (set here
// through the environment, which only Linux reads; elsewhere the runner's own locale applies). This
// package is its own test binary, so switching Fyne's process-wide translations does not touch the
// tests of internal/ui, which rely on the English keys.
func TestInitLanguageAppliesSetting(t *testing.T) {
	for _, sys := range []string{"de_AT.UTF-8", "en_US.UTF-8", "fr_FR.UTF-8", "C"} {
		t.Setenv("LANGUAGE", "")
		t.Setenv("LC_ALL", sys)
		for _, c := range []struct{ language, want string }{
			{i18n.German, "Einstellungen"},
			{i18n.English, "Settings"},
			{i18n.German, "Einstellungen"},
		} {
			a := test.NewTempApp(t)
			ui.NewSettings(a.Preferences()).SetLanguage(c.language)
			initLanguage(a)
			if got := lang.L("Settings"); got != c.want {
				t.Errorf("system %s, language %q: %q, want %q", sys, c.language, got, c.want)
			}
			if got := lang.L("Connecting to {{.Name}}", map[string]any{"Name": "device.example.com"}); c.language == i18n.German && got != "Verbinde mit device.example.com" {
				t.Errorf("system %s, German template: %q", sys, got)
			}
		}
	}
}

// Without a stored choice the OS decides, as before the setting existed.
func TestInitLanguageDefaultIsSystem(t *testing.T) {
	a := test.NewTempApp(t)
	if l := ui.NewSettings(a.Preferences()).Language(); l != i18n.System {
		t.Fatalf("default %q", l)
	}
	if runtime.GOOS != "linux" {
		t.Skip("the system locale comes from the environment only on Linux")
	}
	for sys, want := range map[string]string{"de_AT.UTF-8": "Einstellungen", "en_US.UTF-8": "Settings"} {
		t.Setenv("LANGUAGE", "")
		t.Setenv("LC_ALL", sys)
		initLanguage(test.NewTempApp(t))
		if got := lang.L("Settings"); got != want {
			t.Errorf("system %s: %q, want %q", sys, got, want)
		}
	}
}
