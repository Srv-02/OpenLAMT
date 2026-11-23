package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/i18n"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

const (
	guideEN = "https://srv-02.github.io/OpenLAMT/"
	guideDE = "https://srv-02.github.io/OpenLAMT/de/"
)

// TestUserGuideURLFollowsLanguage: German texts open the German guide, English texts the
// English one. i18n.TestUILanguageFollowsTheTexts checks which language the texts use.
func TestUserGuideURLFollowsLanguage(t *testing.T) {
	if got := userGuideURL(i18n.German); got != guideDE {
		t.Errorf("German: %s, want %s", got, guideDE)
	}
	if got := userGuideURL(i18n.English); got != guideEN {
		t.Errorf("English: %s, want %s", got, guideEN)
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
	// The tests of this package register no app translations, so the texts are English.
	if len(*opened) != 1 || (*opened)[0] != guideEN {
		t.Fatalf("opened %v, want %s", *opened, guideEN)
	}
}
