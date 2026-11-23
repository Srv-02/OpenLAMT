package ui

import (
	"github.com/Srv-02/openlamt/internal/ui/i18n"
)

// guideURL is where GitHub Pages serves the user guide (site/); the German pages are in de/.
// internal/sitecheck checks that this is the address the guide itself names.
const guideURL = "https://srv-02.github.io/OpenLAMT/"

// userGuideURL returns the user guide for language, the language of the app's texts
// (i18n.UILanguage). German opens the German guide, every other language the English one,
// which is also what the texts fall back to.
func userGuideURL(language string) string {
	if language == i18n.German {
		return guideURL + "de/"
	}
	return guideURL
}

// showHelp opens the user guide in the browser: the Help button of the sidebar footer.
func (s *StartWindow) showHelp() { openURL(userGuideURL(i18n.UILanguage())) }
