package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/ui/i18n"
)

// guideURL is where GitHub Pages serves the user guide (site/); the German pages are in de/.
// internal/sitecheck checks that this is the address the guide itself names.
const guideURL = "https://srv-02.github.io/OpenLAMT/"

// userGuideURL returns the user guide in the language of the app's texts. setting is the
// language the texts were loaded in (i18n.Active); with i18n.System the OS locale system decides,
// as it does for the texts. German opens the German guide, every other language the English one,
// which is also what the texts fall back to.
func userGuideURL(setting string, system fyne.Locale) string {
	language := setting
	if language == i18n.System {
		language, _, _ = strings.Cut(string(system), "-") // "de-AT" -> "de"
	}
	if language == i18n.German {
		return guideURL + "de/"
	}
	return guideURL
}

// showHelp opens the user guide in the browser: the Help button of the sidebar footer.
func (s *StartWindow) showHelp() { openURL(userGuideURL(i18n.Active(), lang.SystemLocale())) }
