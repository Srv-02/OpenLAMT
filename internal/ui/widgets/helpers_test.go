package widgets

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// themedApp starts a test app with OpenLAMT's theme, so text is measured in Inter.
func themedApp(t *testing.T) fyne.App {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(style.Theme{})
	return a
}
