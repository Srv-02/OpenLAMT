package style

import (
	_ "embed"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Inter 4.1 from https://github.com/rsms/inter (SIL Open Font License 1.1, fonts/OFL.txt).
// SemiBold serves as the bold face, as the design uses 600 for titles.
var (
	//go:embed fonts/Inter-Regular.ttf
	interRegular []byte
	//go:embed fonts/Inter-SemiBold.ttf
	interSemiBold []byte

	FontRegular  = fyne.NewStaticResource("Inter-Regular.ttf", interRegular)
	FontSemiBold = fyne.NewStaticResource("Inter-SemiBold.ttf", interSemiBold)
)

var fallback = theme.DefaultTheme()

// Theme is OpenLAMT's Fyne theme: one theme, two color tables (dark and light).
type Theme struct{}

var _ fyne.Theme = Theme{}

// Color returns the design color for name in the current variant (SetVariant), not in the one
// Fyne passes: Fyne always passes the system's variant, the app may pin light or dark (setting
// "Appearance"). So every widget, theme override and sub-theme draws in the same variant as the
// primitives that read Color.
func (Theme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return Color(name)
}

// Font returns Inter for text (SemiBold for bold), and Fyne's fonts for monospace and symbols.
// Inter has no italic in the bundle: italic text uses the upright face.
func (Theme) Font(s fyne.TextStyle) fyne.Resource {
	if s.Monospace || s.Symbol {
		return fallback.Font(s)
	}
	if s.Bold {
		return FontSemiBold
	}
	return FontRegular
}

// Icon returns Fyne's standard icons; OpenLAMT's own symbols live in package icons.
func (Theme) Icon(name fyne.ThemeIconName) fyne.Resource { return fallback.Icon(name) }

// Size returns the design size for name.
func (Theme) Size(name fyne.ThemeSizeName) float32 { return Size(name) }

// CardTheme is Theme for the inside of form cards: entries there have no border of their own
// (the card rows are the fields), only the focus outline remains.
type CardTheme struct{ Theme }

// Color returns a transparent input border and Theme's colors otherwise.
func (t CardTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameInputBorder {
		return color.Transparent
	}
	return t.Theme.Color(name, v)
}

// ErrorTextTheme wraps a theme for red warning text: Fyne draws a label with DangerImportance in its
// error color, which is also the fill of a danger button (white text on it). Inside this theme
// "error" is the readable text red, so only the text changes, not the button fill.
type ErrorTextTheme struct{ fyne.Theme }

// Color returns the error text color for Fyne's error color and Theme's colors otherwise.
func (t ErrorTextTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameError {
		name = ColorNameErrorText
	}
	return t.Theme.Color(name, v)
}
