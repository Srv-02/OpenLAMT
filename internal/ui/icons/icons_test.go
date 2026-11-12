package icons

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// useVariant sets the theme variant for a test and restores dark.
func useVariant(t *testing.T, v func()) {
	t.Helper()
	t.Cleanup(func() { style.SetVariant(theme.VariantDark) })
	v()
}

func TestAllSymbolsEmbedded(t *testing.T) {
	entries, err := fs.ReadDir(files, "svg")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 37 || len(All) != 35 || len(Filled) != 2 {
		t.Fatalf("%d files, %d stroked, %d filled; want 37, 35, 2", len(entries), len(All), len(Filled))
	}
	for _, n := range Filled {
		b, err := files.ReadFile("svg/" + string(n) + ".svg")
		if err != nil || !strings.Contains(string(b), sourceFill) || strings.Contains(string(b), "stroke") {
			t.Errorf("%s: want a filled symbol without stroke: %v", n, err)
		}
	}
	for _, n := range All {
		b, err := files.ReadFile("svg/" + string(n) + ".svg")
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		// The recoloring relies on exactly these attributes.
		if !strings.Contains(string(b), sourceColor) || !strings.Contains(string(b), sourceStroke) {
			t.Errorf("%s: no %s / %s", n, sourceColor, sourceStroke)
		}
	}
}

func TestGetRecolorsStroke(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range All {
		for _, v := range Variants {
			r := Get(n, v)
			svg := string(r.Content())
			if strings.Contains(svg, "#000000") {
				t.Errorf("%s/%s still black", n, v)
			}
			if !strings.Contains(svg, `stroke="`+variantColors[v]+`"`) {
				t.Errorf("%s/%s: stroke not %s", n, v, variantColors[v])
			}
			// Fyne caches rasterized SVGs by resource name: names must be unique.
			if seen[r.Name()] {
				t.Errorf("duplicate resource name %s", r.Name())
			}
			seen[r.Name()] = true
			if Get(n, v) != r {
				t.Errorf("%s/%s not cached", n, v)
			}
		}
	}
}

func TestFilledSymbolsKeepTheirFixedColors(t *testing.T) {
	for n, v := range map[Name]Variant{Heart: HeartPink, Star: StarYellow} {
		svg := string(Get(n, v).Content())
		if !strings.Contains(svg, `fill="`+variantColors[v]+`"`) || strings.Contains(svg, "#000000") {
			t.Errorf("%s: %s", n, svg)
		}
		img := Render(Spec{n, v, DefaultStroke}, 24, 24)
		c := img.RGBAAt(12, 12)
		if c.A != 255 {
			t.Errorf("%s: center pixel %v is not filled", n, c)
		}
	}
}

func TestGetStroke(t *testing.T) {
	r := GetStroke(Connect, White, 2)
	if !strings.Contains(string(r.Content()), `stroke-width="2"`) || strings.Contains(string(r.Content()), `stroke-width="1.7"`) {
		t.Errorf("stroke not replaced: %s", r.Content())
	}
	if r.Name() == Get(Connect, White).Name() {
		t.Error("stroke variant shares the resource name")
	}
}

func TestColor(t *testing.T) {
	if c := Color(Accent); c.R != 0x0A || c.G != 0x84 || c.B != 0xFF || c.A != 0xFF {
		t.Errorf("accent = %v", c)
	}
}

func TestUnknownPanics(t *testing.T) {
	for _, f := range []func(){
		func() { Get("nope", Foreground) },
		func() { Get(Power, "nope") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			f()
		}()
	}
}

func TestLightVariantUsesOtherStrokeColors(t *testing.T) {
	useVariant(t, func() { style.SetVariant(theme.VariantLight) })
	for _, v := range Variants {
		want := lightColors[v]
		for _, n := range All {
			if svg := string(Get(n, v).Content()); !strings.Contains(svg, `stroke="`+want+`"`) {
				t.Errorf("%s/%s: stroke not %s", n, v, want)
			}
		}
		if c := Color(v); hexOf(c.R, c.G, c.B) != want {
			t.Errorf("Color(%s) = %v, want %s", v, c, want)
		}
	}
	for n, v := range map[Name]Variant{Heart: HeartPink, Star: StarYellow} {
		if svg := string(Get(n, v).Content()); !strings.Contains(svg, `fill="`+lightColors[v]+`"`) {
			t.Errorf("%s: %s", n, svg)
		}
	}
	if lightColors[HeartPink] != "#FF2D55" || lightColors[StarYellow] != "#F5B400" {
		t.Error("filled symbols are not the light design colors")
	}
	// Light strokes are dark and the dark ones are light: the two tables really differ.
	for _, v := range []Variant{Foreground, Toolbar, Muted, Secondary, Disabled, Accent, MenuOpen, Danger, Link, LinkHover, HeartPink, StarYellow,
		AccentText, WarningIcon, WarningText, DangerText} {
		if lightColors[v] == variantColors[v] {
			t.Errorf("%s: light equals dark", v)
		}
	}
	if lightColors[White] != variantColors[White] {
		t.Error("White (on accent) must stay white")
	}
}

func hexOf(r, g, b uint8) string {
	const d = "0123456789ABCDEF"
	return string([]byte{'#', d[r>>4], d[r&15], d[g>>4], d[g&15], d[b>>4], d[b&15]})
}

func TestMenuOpenIsWhiteInDarkAndInkInLight(t *testing.T) {
	useVariant(t, func() { style.SetVariant(theme.VariantDark) })
	if variantColors[MenuOpen] != "#FFFFFF" || lightColors[MenuOpen] != "#1D1D1F" {
		t.Errorf("menu-open: dark %s light %s", variantColors[MenuOpen], lightColors[MenuOpen])
	}
}

func TestCacheSeparatesThemeVariants(t *testing.T) {
	useVariant(t, func() { style.SetVariant(theme.VariantDark) })
	dark := Get(Power, Foreground)
	style.SetVariant(theme.VariantLight)
	light := Get(Power, Foreground)
	if dark == light || dark.Name() == light.Name() {
		t.Fatal("dark and light share a resource")
	}
	if string(dark.Content()) == string(light.Content()) {
		t.Error("same content in both variants")
	}
	if Get(Power, Foreground) != light {
		t.Error("light not cached")
	}
	style.SetVariant(theme.VariantDark)
	if Get(Power, Foreground) != dark {
		t.Error("dark not cached")
	}
	// Both resources describe the same symbol.
	ds, _ := Lookup(dark)
	ls, _ := Lookup(light)
	if ds != ls {
		t.Errorf("spec differs: %v %v", ds, ls)
	}
}

func TestRenderFollowsThemeVariant(t *testing.T) {
	useVariant(t, func() { style.SetVariant(theme.VariantDark) })
	spec := Spec{Heart, HeartPink, DefaultStroke}
	d := Render(spec, 24, 24).RGBAAt(12, 12)
	style.SetVariant(theme.VariantLight)
	l := Render(spec, 24, 24).RGBAAt(12, 12)
	if d == l || l.R != 0xFF || l.G != 0x2D || l.B != 0x55 {
		t.Errorf("dark %v light %v", d, l)
	}
}

// TestDeviceManagementSymbols pins the symbols and stroke colors of the device management design
// (Main.dc.html, WindowB.dc.html, Media.dc.html, Dialogs.dc.html, Danger.dc.html).
func TestDeviceManagementSymbols(t *testing.T) {
	for _, n := range []Name{Console, Media, Folder, Manage, Hardware, EventLog, AuditLog, Users, Warning, DeviceSettings} {
		if !slices.Contains(All, n) {
			t.Errorf("%s missing in All", n)
		}
		if img := Render(Spec{n, Foreground, DefaultStroke}, 24, 24); img == nil {
			t.Errorf("%s does not render", n)
		}
	}
	want := map[Variant]string{AccentText: "#0A64D6", WarningIcon: "#9A3F00", WarningText: "#6B2C00", DangerText: "#7A0014"}
	for v, light := range want {
		if lightColors[v] != light || variantColors[v] == "" || !slices.Contains(Variants, v) {
			t.Errorf("%s: light %s dark %s, want light %s", v, lightColors[v], variantColors[v], light)
		}
	}
}
