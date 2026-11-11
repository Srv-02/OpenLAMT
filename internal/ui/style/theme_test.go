package style

import (
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

func hex(c color.Color) string {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return fmtHex(n)
}

func fmtHex(n color.NRGBA) string {
	const digits = "0123456789ABCDEF"
	b := []byte{'#', 0, 0, 0, 0, 0, 0, 0, 0}
	for i, v := range []uint8{n.R, n.G, n.B, n.A} {
		b[1+2*i] = digits[v>>4]
		b[2+2*i] = digits[v&0xf]
	}
	return string(b)
}

// TestHandoffColors pins the Fyne color names to HANDOFF.md section 2 (dark); variant 2, which
// Fyne's test app reports, counts as dark.
func TestHandoffColors(t *testing.T) {
	want := map[fyne.ThemeColorName]string{
		theme.ColorNameBackground:        "#1E1E20FF",
		theme.ColorNameMenuBackground:    "#2C2C2FFF",
		theme.ColorNameOverlayBackground: "#2C2C2FFF",
		theme.ColorNameSeparator:         "#FFFFFF0F", // rgba(255,255,255,0.06)
		theme.ColorNameForeground:        "#F5F5F7FF",
		theme.ColorNamePlaceHolder:       "#8E8E93FF",
		theme.ColorNameDisabled:          "#6E6E73FF",
		theme.ColorNamePrimary:           "#0A84FFFF",
		theme.ColorNameHover:             "#FFFFFF17", // 0.09
		theme.ColorNamePressed:           "#FFFFFF1F", // 0.12
		theme.ColorNameInputBackground:   "#00000000",
		theme.ColorNameSuccess:           "#30D158FF",
		theme.ColorNameError:             "#D70015FF",
		ColorNameSidebar:                 "#262628FF",
		ColorNameToolbar:                 "#2A2A2CFF",
		ColorNameStatusBar:               "#232325FF",
		ColorNameCard:                    "#2A2A2DFF",
		ColorNameSeparatorDark:           "#0000008C", // rgba(0,0,0,0.55)
		ColorNameTextSecondary:           "#A1A1A6FF",
		ColorNameSectionLabel:            "#98989DFF",
		ColorNameDangerTile:              "#FF453A29", // rgba(255,69,58,0.16)
		ColorNameLetterbox:               "#0B0B0CFF",
	}
	th := Theme{}
	for name, w := range want {
		for _, v := range []fyne.ThemeVariant{theme.VariantDark, 2} {
			if got := hex(th.Color(name, v)); got != w {
				t.Errorf("%s (variant %d) = %s, want %s", name, v, got, w)
			}
		}
	}
	// Every Fyne color name has a value, so Color never needs Fyne's theme (and its app).
	for _, n := range []fyne.ThemeColorName{theme.ColorNameButton, theme.ColorNameDisabledButton, theme.ColorNameFocus,
		theme.ColorNameForegroundOnError, theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnSuccess,
		theme.ColorNameForegroundOnWarning, theme.ColorNameHeaderBackground, theme.ColorNameHyperlink,
		theme.ColorNameInnerWindowBorder, theme.ColorNameInnerWindowBorderInactive, theme.ColorNameInputBorder,
		theme.ColorNameScrollBar, theme.ColorNameScrollBarBackground, theme.ColorNameSelection, theme.ColorNameShadow,
		theme.ColorNameWarning} {
		for tbl, m := range map[string]map[fyne.ThemeColorName]color.Color{"dark": darkColors, "light": lightColors} {
			if _, ok := m[n]; !ok {
				t.Errorf("%s not set in the %s table", n, tbl)
			}
		}
	}
}

func TestSizes(t *testing.T) {
	want := map[fyne.ThemeSizeName]float32{
		theme.SizeNameText:           13,
		theme.SizeNameHeadingText:    22,
		theme.SizeNameSubHeadingText: 15,
		theme.SizeNameCardRadius:     10,
		theme.SizeNameDialogRadius:   12,
		theme.SizeNameMenuRadius:     10,
		SizeNameSecondaryText:        11.5,
		SizeNameToolbarCaption:       10.5,
		SizeNameSectionLabel:         11,
		SizeNameDetailHeading:        22,
	}
	for name, w := range want {
		if got := (Theme{}).Size(name); got != w {
			t.Errorf("%s = %v, want %v", name, got, w)
		}
	}
	if got, w := (Theme{}).Size(theme.SizeNamePadding), theme.DefaultTheme().Size(theme.SizeNamePadding); got != w {
		t.Errorf("padding = %v, want default %v", got, w)
	}
}

func TestFonts(t *testing.T) {
	th := Theme{}
	if th.Font(fyne.TextStyle{}) != FontRegular {
		t.Error("regular is not Inter Regular")
	}
	if th.Font(fyne.TextStyle{Bold: true}) != FontSemiBold || th.Font(fyne.TextStyle{Bold: true, Italic: true}) != FontSemiBold {
		t.Error("bold is not Inter SemiBold")
	}
	if th.Font(fyne.TextStyle{Monospace: true}) != theme.DefaultTheme().Font(fyne.TextStyle{Monospace: true}) {
		t.Error("monospace is not Fyne's")
	}
	for _, r := range []fyne.Resource{FontRegular, FontSemiBold} {
		if len(r.Content()) < 100_000 {
			t.Errorf("%s: %d bytes", r.Name(), len(r.Content()))
		}
	}
}

func TestCardThemeHidesInputBorder(t *testing.T) {
	if _, _, _, a := (CardTheme{}).Color(theme.ColorNameInputBorder, theme.VariantDark).RGBA(); a != 0 {
		t.Error("input border visible in cards")
	}
	if hex((CardTheme{}).Color(theme.ColorNamePrimary, theme.VariantDark)) != "#0A84FFFF" {
		t.Error("card theme lost the other colors")
	}
}

func TestOver(t *testing.T) {
	// 35 % white over black is 89 (0.35 * 255).
	if got := fmtHex(Over(color.White, 0.35, color.Black)); got != "#595959FF" {
		t.Errorf("got %s", got)
	}
	// A translucent color multiplies its own alpha: 0.5 * 0.5 = 25 %.
	if got := fmtHex(Over(color.NRGBA{R: 255, G: 255, B: 255, A: 128}, 0.5, color.Black)); got != "#404040FF" {
		t.Errorf("got %s", got)
	}
}

// TestLightColors pins the light table: the handoff values, the derived ones, and every own name.
func TestLightColors(t *testing.T) {
	t.Cleanup(func() { SetVariant(theme.VariantDark) })
	SetVariant(theme.VariantLight)
	want := map[fyne.ThemeColorName]string{
		theme.ColorNameBackground:        "#FFFFFFFF",
		theme.ColorNameForeground:        "#1D1D1FFF",
		theme.ColorNamePrimary:           "#007AFFFF",
		theme.ColorNameSelection:         "#007AFFFF",
		theme.ColorNameHover:             "#0000000D", // 0.05
		theme.ColorNamePressed:           "#00000014", // 0.08
		theme.ColorNameSeparator:         "#00000012", // 0.07
		theme.ColorNameDisabled:          "#AEAEB2FF",
		theme.ColorNamePlaceHolder:       "#8E8E93FF",
		theme.ColorNameError:             "#D70015FF",
		theme.ColorNameSuccess:           "#28A745FF",
		theme.ColorNameHyperlink:         "#0066CCFF",
		theme.ColorNameMenuBackground:    "#FFFFFFFF",
		theme.ColorNameOverlayBackground: "#FFFFFFFF",
		theme.ColorNameHeaderBackground:  "#F2F2F4FF",
		theme.ColorNameInputBorder:       "#0000001F", // 0.12
		theme.ColorNameButton:            "#0000000F", // 0.06
		theme.ColorNameDisabledButton:    "#0000000A", // 0.04
		theme.ColorNameFocus:             "#007AFF80", // 0.5
		theme.ColorNameWarning:           "#FF9500FF",
		theme.ColorNameShadow:            "#00000044",
		theme.ColorNameInputBackground:   "#00000000",
		ColorNameSidebar:                 "#F2F2F4FF",
		ColorNameToolbar:                 "#F5F5F7FF",
		ColorNameStatusBar:               "#F2F2F4FF",
		ColorNameCard:                    "#F5F5F7FF",
		ColorNameCardBorder:              "#0000000F",
		ColorNameSeparatorDark:           "#0000001A", // 0.10
		ColorNameGroupDivider:            "#0000001F",
		ColorNameTextSecondary:           "#636366FF",
		ColorNameSectionLabel:            "#636366FF",
		ColorNameDialogText:              "#48484AFF",
		ColorNameIconMuted:               "#48484AFF",
		ColorNameToolbarIcon:             "#1D1D1FFF",
		ColorNameTile:                    "#0000000F",
		ColorNameTileSelected:            "#FFFFFF38", // 0.22
		ColorNameTileBorder:              "#0000000F",
		ColorNameSearchField:             "#0000000F",
		ColorNameSelectedText2:           "#FFFFFFE6", // 0.90
		ColorNameActive:                  "#0000000F",
		ColorNameSwitchOff:               "#E3E3E8FF",
		ColorNameDangerTile:              "#FF3B301F", // 0.12
		ColorNameLetterbox:               "#E3E3E6FF",
		ColorNameEmptyStateTile:          "#F2F2F4FF",
		ColorNameWhite:                   "#FFFFFFFF",
		ColorNameDialogCard:              "#F5F5F7FF",
		ColorNameDialogBorder:            "#0000001F",
		ColorNameField:                   "#FFFFFFFF",
		ColorNameAvatar:                  "#FFFFFFFF",
		ColorNamePill:                    "#FFFFFFFF",
		ColorNameLink:                    "#0066CCFF",
		ColorNameLinkHover:               "#004C99FF",
	}
	for name, w := range want {
		if got := hex((Theme{}).Color(name, theme.VariantDark)); got != w {
			t.Errorf("%s = %s, want %s", name, got, w)
		}
	}
	// Both tables name the same colors, so no variant answers transparent for a name the other has.
	for n := range darkColors {
		if _, ok := lightColors[n]; !ok {
			t.Errorf("%s missing in the light table", n)
		}
	}
	for n := range lightColors {
		if _, ok := darkColors[n]; !ok {
			t.Errorf("%s missing in the dark table", n)
		}
	}
}

// TestColorFollowsVariant: Color answers for the variant set with SetVariant, dark by default and
// for Fyne's test-app variant 2; Theme.Color answers for the current variant too, whatever Fyne asks for.
func TestColorFollowsVariant(t *testing.T) {
	t.Cleanup(func() { SetVariant(theme.VariantDark) })
	SetVariant(theme.VariantDark)
	if got := hex(Color(ColorNameSidebar)); got != "#262628FF" || IsLight() {
		t.Errorf("dark: %s light=%v", got, IsLight())
	}
	SetVariant(theme.VariantLight)
	if got := hex(Color(ColorNameSidebar)); got != "#F2F2F4FF" || !IsLight() || Variant() != theme.VariantLight {
		t.Errorf("light: %s light=%v", got, IsLight())
	}
	// Theme.Color uses the current variant, not the one Fyne passes (the system's).
	if got := hex((Theme{}).Color(ColorNameSidebar, theme.VariantDark)); got != "#F2F2F4FF" {
		t.Errorf("Theme.Color under light, asked for dark: %s", got)
	}
	SetVariant(2)
	if IsLight() || Variant() != theme.VariantDark || hex(Color(ColorNameSidebar)) != "#262628FF" {
		t.Error("variant 2 is not treated as dark")
	}
	if _, _, _, a := Color("no-such-color").RGBA(); a != 0 {
		t.Error("unknown name is not transparent")
	}
}

// TestLightColorsDifferFromDark: every own name that is not deliberately shared changes with the
// variant, so a name forgotten in the light table (copied from dark) is noticed.
func TestLightColorsDifferFromDark(t *testing.T) {
	same := map[fyne.ThemeColorName]bool{ // identical in both tables on purpose
		theme.ColorNameError: true, theme.ColorNamePlaceHolder: true, theme.ColorNameInputBackground: true,
		theme.ColorNameForegroundOnError: true, theme.ColorNameForegroundOnPrimary: true,
		ColorNameWhite: true, ColorNameConsole: true,
	}
	for n, d := range darkColors {
		if same[n] {
			continue
		}
		if hex(d) == hex(lightColors[n]) {
			t.Errorf("%s: light equals dark (%s)", n, hex(d))
		}
	}
}

// TestVariantOnlyTokens pins the tokens that exist because the two variants draw a part
// differently (issue #51 part B): the detail tile, the row separators of a card, the border of
// the avatar and the pill (light only), the tagline, the menu panel and its shadow.
func TestVariantOnlyTokens(t *testing.T) {
	want := map[fyne.ThemeColorName][2]string{ // dark, light
		ColorNameDetailTile:    {"#2C2C2FFF", "#F2F2F4FF"},
		ColorNameCardSeparator: {"#FFFFFF0F", "#00000012"}, // 0.06, 0.07
		ColorNameAvatarBorder:  {"#00000000", "#0000000F"}, // none, 0.06
		ColorNamePillBorder:    {"#00000000", "#0000001F"}, // none, 0.12
		ColorNameTagline:       {"#D1D1D6FF", "#3A3A3CFF"},
		ColorNameMenuPanel:     {"#2C2C2FF7", "#FFFFFFFF"},
		ColorNameMenuShadow:    {"#0000008C", "#0000002E"}, // 0.55, 0.18
	}
	for name, w := range want {
		if got := hex(ColorFor(name, theme.VariantDark)); got != w[0] {
			t.Errorf("%s dark = %s, want %s", name, got, w[0])
		}
		if got := hex(ColorFor(name, theme.VariantLight)); got != w[1] {
			t.Errorf("%s light = %s, want %s", name, got, w[1])
		}
	}
}

// luminance and contrast are WCAG 2.x relative luminance and contrast ratio.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		f := float64(v) / 65535
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestErrorTextContrast: red warning text must stay readable on the dialog background in both
// variants (4.5:1), while the red button fill keeps white text (foregroundOnError).
func TestErrorTextContrast(t *testing.T) {
	t.Cleanup(func() { SetVariant(theme.VariantDark) })
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		SetVariant(v)
		bg := ColorFor(theme.ColorNameOverlayBackground, v)
		txt := ColorFor(ColorNameErrorText, v)
		if c := contrast(txt, bg); c < 4.5 {
			t.Errorf("variant %d: error text %s on %s = %.2f:1", v, hex(txt), hex(bg), c)
		}
		if c := contrast(ColorFor(theme.ColorNameForegroundOnError, v), ColorFor(theme.ColorNameError, v)); c < 4.5 {
			t.Errorf("variant %d: text on the red button = %.2f:1", v, c)
		}
		// the error text theme maps Fyne's error color (used by danger labels) to the text token
		if got, want := (ErrorTextTheme{Theme{}}).Color(theme.ColorNameError, v), txt; hex(got) != hex(want) {
			t.Errorf("variant %d: ErrorTextTheme error = %s, want %s", v, hex(got), hex(want))
		}
	}
	if got := hex(ColorFor(ColorNameErrorText, theme.VariantDark)); got != "#FF6961FF" {
		t.Errorf("dark error text = %s", got)
	}
	if got := hex(ColorFor(ColorNameErrorText, theme.VariantLight)); got != "#D70015FF" {
		t.Errorf("light error text = %s", got)
	}
	if got := hex(ColorFor(theme.ColorNameError, theme.VariantDark)); got != "#D70015FF" {
		t.Errorf("the red fill must stay %s in dark", got)
	}
}

// TestDeviceManagementTokens pins the light values of the device management mockups
// (Main, WindowB, Settings, Dialogs, Danger .dc.html); the dark ones are derived and only have
// to exist and differ from the light ones.
func TestDeviceManagementTokens(t *testing.T) {
	want := map[fyne.ThemeColorName]string{
		ColorNameSegment:          "#EDEDF0FF",
		ColorNameSegmentActive:    "#FFFFFFFF",
		ColorNameSegmentShadow:    "#00000024", // rgba(0,0,0,.14)
		ColorNameAccentText:       "#0A64D6FF",
		ColorNameAccentTint:       "#E8F1FEFF",
		ColorNameSidePanel:        "#FBFBFCFF",
		ColorNameTableHeader:      "#F7F7F8FF",
		ColorNameControlBorder:    "#D9D9DEFF",
		ColorNameControlText:      "#3A3A3CFF",
		ColorNameChipActive:       "#1C1C1EFF",
		ColorNameChipActiveText:   "#FFFFFFFF",
		ColorNameSeverityInfo:     "#6E6E73FF",
		ColorNameSeverityWarning:  "#C75300FF",
		ColorNameSeverityCritical: "#C2001AFF",
		ColorNameDanger:           "#C2001AFF",
		ColorNameDangerDisabled:   "#EBC2C8FF",
		ColorNameDangerSurface:    "#FDE8EAFF",
		ColorNameDangerZoneBorder: "#E9A3ADFF",
		ColorNameDangerBoxBorder:  "#F0B8C0FF",
		ColorNameDangerZoneText:   "#A3001AFF",
		ColorNameDangerBoxText:    "#7A0014FF",
		ColorNameWarningSurface:   "#FFF1E5FF",
		ColorNameWarningBorder:    "#F3CFAEFF",
		ColorNameWarningText:      "#6B2C00FF",
		ColorNameWarningIcon:      "#9A3F00FF",
		ColorNameFilterClear:      "#C7C7CCFF",
		ColorNameCodeBackground:   "#F2F2F4FF",
	}
	for name, w := range want {
		if got := hex(ColorFor(name, theme.VariantLight)); got != w {
			t.Errorf("%s light = %s, want %s", name, got, w)
		}
		d, ok := darkColors[name]
		if !ok {
			t.Errorf("%s missing in the dark table", name)
			continue
		}
		if hex(d) == w {
			t.Errorf("%s: dark equals light", name)
		}
	}
}
