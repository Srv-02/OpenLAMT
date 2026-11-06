package ui

import (
	"fyne.io/fyne/v2"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// FollowTheme makes the app draw in the variant of the setting "Appearance" (issue #103): the
// pinned light or dark one, or for System the one the OS reports (issue #51). It sets style's
// variant now and on every settings change, from Settings.ThemeVariant, and the palette of the
// status dots from the setting "Color blind mode". Fyne calls the listener
// before it refreshes the windows (setupTheme, then the listeners in registration order, the
// driver's own refresh registered last), so the refresh already reads the new colors. Call it
// before the windows are created, after SetTheme(style.Theme{}).
func FollowTheme(a fyne.App) {
	setLook(a)
	a.Settings().AddListener(func(fyne.Settings) { setLook(a) })
}

// setLook sets style's variant and status palette from the stored settings.
func setLook(a fyne.App) {
	style.SetColorBlind(NewSettings(a.Preferences()).ColorBlind())
	style.SetVariant(appVariant(a))
}

// appVariant is the variant a draws in: Settings.ThemeVariant of the stored appearance and the
// variant the system reports.
func appVariant(a fyne.App) fyne.ThemeVariant {
	return NewSettings(a.Preferences()).ThemeVariant(a.Settings().ThemeVariant())
}

// applyAppearance makes every open window, overlay and dialog follow a changed "Appearance" or
// "Color blind mode" at once: it sets the variant and the status palette, then sets the current
// theme again, which makes Fyne refresh every window as on a system switch. The variant comes
// first because not every driver calls the FollowTheme listener before its refresh (the test
// driver refreshes first).
func applyAppearance(a fyne.App) {
	setLook(a)
	a.Settings().SetTheme(a.Settings().Theme())
}

// overrideTheme is the theme of the container.NewThemeOverride wrappers that pin the design
// theme inside dialogs and the empty state (test apps use Fyne's theme). A variable so that tests
// can put their own theme there.
var overrideTheme fyne.Theme = style.Theme{}
