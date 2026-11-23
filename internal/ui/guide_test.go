package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/i18n"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

const (
	guideEN = "https://srv-02.github.io/OpenLAMT/"
	guideDE = "https://srv-02.github.io/OpenLAMT/de/"
)

// TestUserGuideURLFollowsLanguage: a forced language wins over the OS; with System the OS
// language decides, German (any region) opens the German guide, everything else the English one.
func TestUserGuideURLFollowsLanguage(t *testing.T) {
	for _, c := range []struct {
		setting string
		system  fyne.Locale
		want    string
	}{
		{i18n.German, "en-US", guideDE},
		{i18n.English, "de-AT", guideEN},
		{i18n.System, "de", guideDE},
		{i18n.System, "de-AT", guideDE},
		{i18n.System, "de-DE-Latn", guideDE},
		{i18n.System, "en-GB", guideEN},
		{i18n.System, "fr-FR", guideEN},
		{i18n.System, "", guideEN},
	} {
		if got := userGuideURL(c.setting, c.system); got != c.want {
			t.Errorf("setting %q, OS %q: %s, want %s", c.setting, c.system, got, c.want)
		}
	}
}

// TestSidebarFooterHasHelpBetweenScanAndSettings: the footer reads Scan, Help, Settings, and
// Help opens the user guide in the browser in the language of the texts.
func TestSidebarFooterHasHelpBetweenScanAndSettings(t *testing.T) {
	h := newStart(t)
	s := h.s
	var order []icons.Name
	walk(s.win.Content(), func(o fyne.CanvasObject) {
		if b, ok := o.(*managerButton); ok && (b == s.scanBtn || b == s.helpBtn || b == s.settingsBtn) {
			order = append(order, b.icon)
		}
	})
	if want := []icons.Name{icons.Radar, icons.Help, icons.Settings}; !slices.Equal(order, want) {
		t.Fatalf("footer icons %v, want %v", order, want)
	}
	if s.helpBtn.size != s.scanBtn.size || s.helpBtn.iconSize != s.scanBtn.iconSize || s.helpBtn.text != "" {
		t.Error("Help is not an icon button like Scan")
	}
	opened := urlsOpened(t)
	test.Tap(s.helpBtn)
	if want := userGuideURL(i18n.Active(), lang.SystemLocale()); len(*opened) != 1 || (*opened)[0] != want {
		t.Fatalf("opened %v, want %s", *opened, want)
	}
}
