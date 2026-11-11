// Command openlamt is the desktop client for Intel AMT: favorites, power control and (later) KVM.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui"
	"github.com/Srv-02/openlamt/internal/ui/i18n"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

const appID = "io.github.srv02.openlamt"

// Icon.png is also the icon `fyne package` bundles (it looks for Icon.png in the source
// directory); embedding it sets the window icon for unpackaged builds as well.
// openlamt-icon.svg is its source.
//
//go:embed Icon.png
var iconPNG []byte

func main() {
	a := app.NewWithID(appID)
	a.SetIcon(fyne.NewStaticResource("Icon.png", iconPNG))
	a.Settings().SetTheme(style.Theme{})
	ui.FollowTheme(a)
	initLanguage(a)
	path, err := favorites.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "openlamt:", err)
		os.Exit(1)
	}
	keys := keychain.OS{}
	mgr := favorites.NewManager(favorites.NewStore(path), keys)
	start := ui.NewStartWindow(a, mgr, keys, session.AMTConnect)
	ui.InstallAboutMenu(a, start.Window())
	start.EnableUpdateCheck()
	start.EnableCoffeeNudge()
	start.Window().ShowAndRun()
	// The device windows unmount their ISOs on goroutines: deleting a folder ISO and clearing
	// the next-start boot option must not be cut off by the process exit.
	if !start.WaitForCleanup() {
		fyne.LogError("quitting", errors.New("unmounting did not finish in time"))
	}
}

// initLanguage loads the translations in the language stored in the settings (System by default);
// it runs before the first window, because every text is translated when it is built.
func initLanguage(a fyne.App) {
	if err := i18n.Init(ui.NewSettings(a.Preferences()).Language()); err != nil {
		fyne.LogError("loading translations", err)
	}
}
