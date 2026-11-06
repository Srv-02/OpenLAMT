package ui

import (
	"fmt"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// followVariant is style.Theme for tests: Fyne's test app always reports variant 2 (dark), so the
// theme answers for the variant the test set with style.SetVariant, as the real app does after
// FollowTheme.
type followVariant struct{ style.Theme }

func (followVariant) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return style.Color(name)
}

// switchVariant changes the variant the way a system switch does: the variant first, then the
// settings change that makes Fyne refresh every window and overlay.
func switchVariant(a fyne.App, v fyne.ThemeVariant) {
	style.SetVariant(v)
	a.Settings().SetTheme(followVariant{})
}

// restoreDarkVariant starts the test dark and puts the variant (and the override theme, which
// follows the variant here) back afterwards.
func restoreDarkVariant(t *testing.T) {
	t.Helper()
	prev := overrideTheme
	t.Cleanup(func() { style.SetVariant(theme.VariantDark); overrideTheme = prev })
	overrideTheme = followVariant{}
	style.SetVariant(theme.VariantDark)
}

func rgba(c color.Color) string {
	if c == nil {
		return ""
	}
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return fmt.Sprintf("#%02X%02X%02X/%02X", n.R, n.G, n.B, n.A)
}

// variantOnly returns the colors of the table for v that the other table does not contain: a
// primitive showing one of them in the other variant was not recolored.
func variantOnly(v, other fyne.ThemeVariant) map[string]fyne.ThemeColorName {
	names := []fyne.ThemeColorName{}
	for _, n := range []fyne.ThemeColorName{theme.ColorNameBackground, theme.ColorNameForeground, theme.ColorNamePrimary,
		theme.ColorNameHover, theme.ColorNamePressed, theme.ColorNameSeparator, theme.ColorNameDisabled, theme.ColorNameButton,
		theme.ColorNameDisabledButton, theme.ColorNameHyperlink, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground,
		theme.ColorNameHeaderBackground, theme.ColorNameInputBorder, theme.ColorNameFocus, theme.ColorNameSuccess} {
		names = append(names, n)
	}
	for _, n := range []fyne.ThemeColorName{style.ColorNameSidebar, style.ColorNameToolbar, style.ColorNameStatusBar, style.ColorNameCard,
		style.ColorNameCardBorder, style.ColorNameSeparatorDark, style.ColorNameGroupDivider, style.ColorNameTextSecondary,
		style.ColorNameSectionLabel, style.ColorNameDialogText, style.ColorNameIconMuted, style.ColorNameToolbarIcon,
		style.ColorNameTile, style.ColorNameTileSelected, style.ColorNameTileBorder, style.ColorNameSearchField,
		style.ColorNameSelectedText2, style.ColorNameActive, style.ColorNameSwitchOff,
		style.ColorNameDangerTile, style.ColorNameLetterbox, style.ColorNameEmptyStateTile, style.ColorNameDialogCard,
		style.ColorNameDialogBorder, style.ColorNameField, style.ColorNameAvatar, style.ColorNamePill, style.ColorNameLink,
		style.ColorNameLinkHover, style.ColorNameWhite, style.ColorNameDetailTile, style.ColorNameCardSeparator, style.ColorNameAvatarBorder,
		style.ColorNamePillBorder, style.ColorNameTagline, style.ColorNameMenuPanel, style.ColorNameMenuShadow} {
		names = append(names, n)
	}
	otherSet := map[string]bool{}
	for _, n := range names {
		otherSet[rgba(style.ColorFor(n, other))] = true
	}
	out := map[string]fyne.ThemeColorName{}
	for _, n := range names {
		c := rgba(style.ColorFor(n, v))
		if !otherSet[c] && c != "#000000/00" {
			out[c] = n
		}
	}
	return out
}

// staleColors walks root and returns every canvas primitive that still has a color of the
// variant that is not current.
func staleColors(root fyne.CanvasObject, stale map[string]fyne.ThemeColorName) []string {
	var out []string
	if root == nil {
		return nil
	}
	walk(root, func(o fyne.CanvasObject) {
		check := func(what string, c color.Color) {
			if n, ok := stale[rgba(c)]; ok {
				out = append(out, fmt.Sprintf("%T %s %s (%s)", o, what, rgba(c), n))
			}
		}
		switch v := o.(type) {
		case *canvas.Rectangle:
			check("fill", v.FillColor)
			check("stroke", v.StrokeColor)
			check("shadow", v.Shadow.Color)
		case *canvas.Circle:
			check("fill", v.FillColor)
			check("stroke", v.StrokeColor)
		case *canvas.Text:
			check("text "+v.Text, v.Color)
		}
	})
	return out
}

// TestLiveVariantSwitch builds the app dark (manager window with a selected favorite, device
// window with the Power menu open, About dialog, confirm dialog), switches to light and back,
// and checks that no canvas primitive keeps a color of the variant it left.
func TestLiveVariantSwitch(t *testing.T) {
	restoreDarkVariant(t)
	h := newStart(t)
	a := h.s.app
	a.Settings().SetTheme(followVariant{})
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"})
	h.s.selectFavorite(favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"})
	showAboutDialog(h.s.win, a)

	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	d := newDevice(t, a, &session.Fake{FP: fp, St: on}, true)
	a.Settings().SetTheme(followVariant{}) // newDevice installs style.Theme
	d.d.apply(on, nil)
	d.d.showPowerMenu()
	showPowerConfirm(d.d.win, amt.PowerOff, d.d.name, func() {})

	roots := func() map[string]fyne.CanvasObject {
		m := map[string]fyne.CanvasObject{
			"manager": h.s.win.Content(), "device": d.d.win.Content(), "power menu": d.d.powerPopup,
		}
		for i, o := range h.s.win.Canvas().Overlays().List() {
			m[fmt.Sprintf("manager overlay %d", i)] = o
		}
		for i, o := range d.d.win.Canvas().Overlays().List() {
			m[fmt.Sprintf("device overlay %d", i)] = o
		}
		return m
	}
	check := func(label string, gone map[string]fyne.ThemeColorName) {
		t.Helper()
		for where, root := range roots() {
			if root == nil {
				continue
			}
			for _, s := range staleColors(root, gone) {
				t.Errorf("%s: %s: %s", label, where, s)
			}
		}
	}
	// The status dots are not in the color tables (style.StatusColor): each must draw its state
	// in the palette of the current variant.
	check("dark start", variantOnly(theme.VariantLight, theme.VariantDark))
	dotsDrawPalette(t, "dark start", roots())

	switchVariant(a, theme.VariantLight)
	check("after light", variantOnly(theme.VariantDark, theme.VariantLight))
	dotsDrawPalette(t, "after light", roots())
	switchVariant(a, theme.VariantDark)
	check("back to dark", variantOnly(theme.VariantLight, theme.VariantDark))
	dotsDrawPalette(t, "back to dark", roots())
}

// TestLiveVariantSwitchSamples pins a few colors by name after the switch: background, sidebar,
// card, text, link and dialog frame.
func TestLiveVariantSwitchSamples(t *testing.T) {
	restoreDarkVariant(t)
	h := newStart(t)
	a := h.s.app
	a.Settings().SetTheme(followVariant{})
	showAboutDialog(h.s.win, a)
	switchVariant(a, theme.VariantLight)

	want := map[string]bool{}
	for _, n := range []fyne.ThemeColorName{style.ColorNameSidebar, style.ColorNameDialogCard, style.ColorNameCardBorder,
		style.ColorNameAvatar, style.ColorNameDialogBorder, "overlayBackground"} {
		want[rgba(style.ColorFor(n, theme.VariantLight))] = true
	}
	found := map[string]bool{}
	for _, root := range []fyne.CanvasObject{h.s.win.Content(), h.s.win.Canvas().Overlays().Top()} {
		walk(root, func(o fyne.CanvasObject) {
			switch v := o.(type) {
			case *canvas.Rectangle:
				found[rgba(v.FillColor)], found[rgba(v.StrokeColor)] = true, true
			case *canvas.Circle:
				found[rgba(v.FillColor)] = true
			}
		})
	}
	for c := range want {
		if !found[c] {
			t.Errorf("no light primitive with %s", c)
		}
	}
	// text: the title is drawn in the light foreground
	var titleColor string
	walk(h.s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if tx, ok := o.(*canvas.Text); ok && tx.Text == "OpenLAMT" {
			titleColor = rgba(tx.Color)
		}
	})
	if titleColor != rgba(style.ColorFor("foreground", theme.VariantLight)) {
		t.Errorf("About title color %s", titleColor)
	}
	_ = test.NewApp
}

// TestFollowThemeReadsTheAppVariant: with Appearance System the variant is set at the start and on
// every settings change from what Fyne reports (the test app always reports variant 2, which is dark).
func TestFollowThemeReadsTheAppVariant(t *testing.T) {
	restoreDarkVariant(t)
	a := test.NewTempApp(t)
	style.SetVariant(theme.VariantLight)
	FollowTheme(a)
	if style.IsLight() {
		t.Error("start variant not read from the app")
	}
	style.SetVariant(theme.VariantLight)
	a.Settings().SetTheme(style.Theme{}) // a settings change calls the listener
	if style.IsLight() {
		t.Error("settings change did not set the variant")
	}
}

// TestFollowThemeAppearance: System follows the system in both directions, Light and Dark pin
// the variant whatever the system reports, at the start and after a system switch.
func TestFollowThemeAppearance(t *testing.T) {
	for _, c := range []struct {
		appearance string
		system     fyne.ThemeVariant
		want       fyne.ThemeVariant
	}{
		{AppearanceSystem, theme.VariantDark, theme.VariantDark},
		{AppearanceSystem, theme.VariantLight, theme.VariantLight},
		{AppearanceLight, theme.VariantDark, theme.VariantLight},
		{AppearanceLight, theme.VariantLight, theme.VariantLight},
		{AppearanceDark, theme.VariantLight, theme.VariantDark},
		{AppearanceDark, theme.VariantDark, theme.VariantDark},
	} {
		restoreDarkVariant(t)
		base := test.NewTempApp(t)
		NewSettings(base.Preferences()).SetAppearance(c.appearance)
		a := variantApp{base, c.system}
		FollowTheme(a)
		if got := style.Variant(); got != c.want {
			t.Errorf("%q on system %d: start variant %d, want %d", c.appearance, c.system, got, c.want)
		}
		base.Settings().SetTheme(style.Theme{}) // a system switch runs the listener
		if got := style.Variant(); got != c.want {
			t.Errorf("%q on system %d: after a settings change %d, want %d", c.appearance, c.system, got, c.want)
		}
	}
}

// TestAppearanceSwitchesOpenWindows: on a dark system, saving Light in the settings dialog
// recolors the manager window (with a selected favorite), the device window with its Power menu
// open and the dialogs on both at once; System brings dark back. The production theme is used,
// so this also checks that Fyne's widgets draw in the pinned variant, not the system's.
func TestAppearanceSwitchesOpenWindows(t *testing.T) {
	restoreDarkVariant(t)
	englishUI(t)
	overrideTheme = style.Theme{}
	h := newStart(t)
	a := h.s.app
	a.Settings().SetTheme(style.Theme{})
	FollowTheme(a)
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"})
	h.s.selectFavorite(favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"})

	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	d := newDevice(t, a, &session.Fake{FP: fp, St: on}, true)
	d.d.apply(on, nil)
	d.d.showPowerMenu()
	showPowerConfirm(d.d.win, amt.PowerOff, d.d.name, func() {})

	check := func(label string, gone map[string]fyne.ThemeColorName) {
		t.Helper()
		roots := map[string]fyne.CanvasObject{"manager": h.s.win.Content(), "device": d.d.win.Content(), "power menu": d.d.powerPopup}
		for i, o := range h.s.win.Canvas().Overlays().List() {
			roots[fmt.Sprintf("manager overlay %d", i)] = o
		}
		for i, o := range d.d.win.Canvas().Overlays().List() {
			roots[fmt.Sprintf("device overlay %d", i)] = o
		}
		for where, root := range roots {
			if root == nil {
				continue
			}
			for _, s := range staleColors(root, gone) {
				t.Errorf("%s: %s: %s", label, where, s)
			}
		}
	}
	save := func(label string) {
		t.Helper()
		showSettingsDialog(h.s.win, &h.s.settings)
		_, drops, _ := settingsParts(t, h.s.win)
		drops[1].SetSelected(label) // appearance
		_, _, buttons := dialogTexts(h.s.win)
		test.Tap(buttons["Save"])
	}
	check("dark start", variantOnly(theme.VariantLight, theme.VariantDark))

	save("Light")
	if h.s.settings.Appearance() != AppearanceLight || !style.IsLight() {
		t.Fatalf("stored %q, light %v", h.s.settings.Appearance(), style.IsLight())
	}
	showAboutDialog(h.s.win, a) // a dialog opened after the switch is light as well
	check("light on a dark system", variantOnly(theme.VariantDark, theme.VariantLight))

	a.Settings().SetTheme(style.Theme{}) // a system switch does not undo the choice
	if !style.IsLight() {
		t.Fatal("a settings change dropped the pinned light variant")
	}

	closeOverlays(h.s.win)
	save("System")
	if h.s.settings.Appearance() != AppearanceSystem || style.IsLight() {
		t.Fatalf("stored %q, light %v", h.s.settings.Appearance(), style.IsLight())
	}
	check("back to the system", variantOnly(theme.VariantLight, theme.VariantDark))
}
