// Package icons holds OpenLAMT's UI symbols (24 px grid, 1.7 stroke, issue #44) as Fyne
// resources: 35 stroked ones and the two filled ones (heart, star) of the About dialog.
//
// Fyne recolors only the fill of themed SVGs, not the stroke these symbols are drawn with, so
// each symbol is served as a static resource with the stroke color already set: one per
// Variant and theme variant (dark or light, style.Variant). A Variant names the role of the
// color (primary text, toolbar icon, ...); the dark and the light table give it its color.
// Get returns the resource of the current theme variant; Render, which widgets.Symbol uses,
// always draws in the current one, so a symbol follows a theme change on its next refresh.
//
// Fyne also draws SVG strokes in device pixels without scaling them to the image size. The
// resources are therefore only a description of the symbol: Lookup recovers it and Render
// draws it with the stroke scaled to the pixel size (design stroke * size / 24), which is what
// widgets.Symbol does at any screen scale. Other widgets show the resource as is.
package icons

import (
	"embed"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Name is a symbol name, the file name in svg/ without extension.
type Name string

// The 20 symbols of the design (HANDOFF.md section 3).
const (
	Power         Name = "power"
	Reset         Name = "reset"
	PowerCycle    Name = "power-cycle"
	BIOS          Name = "bios"
	KVM           Name = "kvm"
	Keyboard      Name = "keyboard"
	ClipboardType Name = "clipboard-type"
	Fullscreen    Name = "fullscreen"
	Settings      Name = "settings"
	Server        Name = "server"
	Search        Name = "search"
	Lock          Name = "lock"
	Eye           Name = "eye"
	Edit          Name = "edit"
	Duplicate     Name = "duplicate"
	Delete        Name = "delete"
	Add           Name = "add"
	Connect       Name = "connect"
	Key           Name = "key"
	ChevronDown   Name = "chevron-down"
)

// Stroked symbols of the About and settings dialogs (HANDOFF.md section 5.4, 5.5).
const (
	Coffee       Name = "coffee"
	ExternalLink Name = "external-link"
	Info         Name = "info"
	Clear        Name = "clear"
)

// Stroked symbols of the device management design (Main.dc.html toolbar, WindowB.dc.html
// navigation, Media.dc.html, Dialogs.dc.html and Danger.dc.html).
const (
	Console  Name = "console"   // SOL text console, KVM|Console segment
	Media    Name = "media"     // disc: toolbar Media, "Mount ISO…"
	Folder   Name = "folder"    // "Mount Folder…"
	Manage   Name = "manage"    // window with a sidebar: toolbar Manage
	Hardware Name = "hardware"  // chip: navigation Hardware
	EventLog Name = "event-log" // navigation Event log
	AuditLog Name = "audit-log" // shield: navigation Audit log
	Users    Name = "users"     // navigation Users
	Warning  Name = "warning"   // triangle: orange and red warning boxes, orange question tile

	DeviceSettings Name = "device-settings" // navigation Settings (Settings.dc.html)
	Unmount        Name = "unmount"         // arrow up onto a line: "Unmount" of the Media menu (Extras.dc.html)
)

// Filled symbols (no stroke): the file's fill is recolored like the stroke of the others.
// Their color is fixed, see HeartPink and StarYellow; Get and Render ignore the stroke width.
const (
	Heart Name = "heart"
	Star  Name = "star"
)

// All lists every symbol, in the order of HANDOFF.md.
var All = []Name{Power, Reset, PowerCycle, BIOS, KVM, Keyboard, ClipboardType, Fullscreen, Settings,
	Server, Search, Lock, Eye, Edit, Duplicate, Delete, Add, Connect, Key, ChevronDown,
	Coffee, ExternalLink, Info, Clear,
	Console, Media, Folder, Manage, Hardware, EventLog, AuditLog, Users, Warning, DeviceSettings, Unmount}

// Filled lists the filled symbols.
var Filled = []Name{Heart, Star}

// Variant is the stroke color of a symbol.
type Variant string

// Stroke colors used by the mockups. The hex values in the comments are the dark ones; the
// light ones are in lightColors.
const (
	Foreground Variant = "foreground" // #F5F5F7 menu items, primary text
	Toolbar    Variant = "toolbar"    // #E5E5EA session toolbar icons
	Muted      Variant = "muted"      // #C7C7CC small icon buttons, sidebar tile
	Secondary  Variant = "secondary"  // #A1A1A6 card row icons, chevrons, status bar
	Disabled   Variant = "disabled"   // #6E6E73 disabled menu items
	Accent     Variant = "accent"     // #0A84FF toggled toolbar item, detail tile
	White      Variant = "white"      // #FFFFFF on accent backgrounds (light too)
	MenuOpen   Variant = "menu-open"  // #FFFFFF the toolbar button of an open menu; light #1D1D1F
	Danger     Variant = "danger"     // #FF6961 confirm dialog tile
	Link       Variant = "link"       // #4DA3FF hyperlinks (About dialog)
	LinkHover  Variant = "link-hover" // #7DBDFF hyperlink under the pointer

	// Device management design (light values from the mockups, dark ones derived).
	AccentText  Variant = "accent-text"  // #4DA3FF active segment, toggled toolbar item, info question tile
	WarningIcon Variant = "warning-icon" // #FF9F0A orange question tile
	WarningText Variant = "warning-text" // #FFC78A triangle in the orange warning box
	DangerText  Variant = "danger-text"  // #FF8A80 triangle in the red warning box
)

// Fill colors of the filled symbols (Heart, Star); not part of Variants.
const (
	HeartPink  Variant = "heart-pink"  // #FF6482
	StarYellow Variant = "star-yellow" // #FFD60A
)

// variantColors is the dark table.
var variantColors = map[Variant]string{
	Foreground: "#F5F5F7",
	Toolbar:    "#E5E5EA",
	Muted:      "#C7C7CC",
	Secondary:  "#A1A1A6",
	Disabled:   "#6E6E73",
	Accent:     "#0A84FF",
	White:      "#FFFFFF",
	MenuOpen:   "#FFFFFF",
	Danger:     "#FF6961",
	Link:       "#4DA3FF",
	LinkHover:  "#7DBDFF",
	// derived for the dark variant
	AccentText:  "#4DA3FF",
	WarningIcon: "#FF9F0A",
	WarningText: "#FFC78A",
	DangerText:  "#FF8A80",
	HeartPink:   "#FF6482",
	StarYellow:  "#FFD60A",
}

// lightColors is the light table (issue #51); same roles as variantColors.
var lightColors = map[Variant]string{
	Foreground:  "#1D1D1F",
	Toolbar:     "#1D1D1F",
	Muted:       "#48484A",
	Secondary:   "#636366",
	Disabled:    "#AEAEB2",
	Accent:      "#007AFF",
	White:       "#FFFFFF",
	MenuOpen:    "#1D1D1F",
	Danger:      "#FF3B30",
	Link:        "#0066CC",
	LinkHover:   "#004C99",
	AccentText:  "#0A64D6",
	WarningIcon: "#9A3F00",
	WarningText: "#6B2C00",
	DangerText:  "#7A0014",
	HeartPink:   "#FF2D55",
	StarYellow:  "#F5B400",
}

// Variants lists every Variant.
var Variants = []Variant{Foreground, Toolbar, Muted, Secondary, Disabled, Accent, White, MenuOpen, Danger, Link, LinkHover,
	AccentText, WarningIcon, WarningText, DangerText}

// table returns the color table of the current theme variant.
func table(light bool) map[Variant]string {
	if light {
		return lightColors
	}
	return variantColors
}

// DefaultStroke is the stroke width of the source files.
const DefaultStroke float32 = 1.7

const (
	sourceColor  = `stroke="#000000"`
	sourceFill   = `fill="#000000"`
	sourceStroke = `stroke-width="1.7"`
)

//go:embed svg/*.svg
var files embed.FS

type key struct {
	name    Name
	variant Variant
	stroke  float32
	light   bool // theme variant the resource was made for
}

var (
	mu    sync.Mutex
	cache = map[key]fyne.Resource{}
	known = map[fyne.Resource]key{}
)

// Get returns symbol name in stroke color v with the design's 1.7 stroke. It panics on an
// unknown name or variant: both are compile-time constants, so that is a programming error.
func Get(name Name, v Variant) fyne.Resource { return GetStroke(name, v, DefaultStroke) }

// GetStroke is Get with another stroke width, in units of the 24 px grid; the mockups use 1.4
// to 2.4 for large or tiny symbols (empty-state tile, primary button arrow, chevrons).
func GetStroke(name Name, v Variant, stroke float32) fyne.Resource {
	k := key{name, v, stroke, style.IsLight()}
	mu.Lock()
	defer mu.Unlock()
	if r, ok := cache[k]; ok {
		return r
	}
	c, ok := table(k.light)[v]
	if !ok {
		panic(fmt.Sprintf("icons: unknown variant %q", v))
	}
	src, err := files.ReadFile("svg/" + string(name) + ".svg")
	if err != nil {
		panic(fmt.Sprintf("icons: unknown symbol %q", name))
	}
	svg := recolor(string(src), c)
	if stroke != DefaultStroke {
		svg = strings.ReplaceAll(svg, sourceStroke, fmt.Sprintf(`stroke-width="%g"`, stroke))
	}
	res := fyne.NewStaticResource(resourceName(k), []byte(svg))
	cache[k] = res
	known[res] = k
	return res
}

// Spec describes a symbol as returned by Get and GetStroke. It does not name a theme variant:
// the same Spec stands for the dark and the light resource.
type Spec struct {
	Name    Name
	Variant Variant
	Stroke  float32 // in units of the 24 px grid
}

// Lookup returns the description of a resource obtained from Get or GetStroke.
func Lookup(r fyne.Resource) (Spec, bool) {
	mu.Lock()
	defer mu.Unlock()
	k, ok := known[r]
	return Spec{k.name, k.variant, k.stroke}, ok
}

// Render draws the symbol in the colors of the current theme variant into a w x h image: a square of min(w, h) pixels, centered, with the
// stroke scaled to it (Stroke * side / 24 pixels). It returns nil for an empty size.
func Render(s Spec, w, h int) *image.RGBA {
	side := min(w, h)
	if side <= 0 {
		return nil
	}
	c, ok := table(style.IsLight())[s.Variant]
	if !ok {
		panic(fmt.Sprintf("icons: unknown variant %q", s.Variant))
	}
	src, err := files.ReadFile("svg/" + string(s.Name) + ".svg")
	if err != nil {
		panic(fmt.Sprintf("icons: unknown symbol %q", s.Name))
	}
	svg := recolor(string(src), c)
	icon, err := oksvg.ReadIconStream(strings.NewReader(svg), oksvg.StrictErrorMode)
	if err != nil {
		panic(fmt.Sprintf("icons: symbol %q: %v", s.Name, err))
	}
	// oksvg takes the stroke width as it is, in target pixels; only the path points follow
	// SetTarget.
	for i := range icon.SVGPaths {
		icon.SVGPaths[i].LineWidth = float64(s.Stroke) * float64(side) / 24
	}
	icon.SetTarget(float64((w-side)/2), float64((h-side)/2), float64(side), float64(side))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(w, h, scanner), 1)
	return img
}

// recolor sets the stroke color and the fill of the filled symbols; the "none" fills of the
// stroked ones stay.
func recolor(svg, c string) string {
	svg = strings.ReplaceAll(svg, sourceColor, `stroke="`+c+`"`)
	return strings.ReplaceAll(svg, sourceFill, `fill="`+c+`"`)
}

func resourceName(k key) string {
	theme := ""
	if k.light {
		theme = "-light"
	}
	if k.stroke == DefaultStroke {
		return fmt.Sprintf("%s-%s%s.svg", k.name, k.variant, theme)
	}
	return fmt.Sprintf("%s-%s%s-%g.svg", k.name, k.variant, theme, k.stroke)
}

// Color returns the stroke color of v in the current theme variant.
func Color(v Variant) color.NRGBA {
	var c color.NRGBA
	fmt.Sscanf(table(style.IsLight())[v], "#%02X%02X%02X", &c.R, &c.G, &c.B)
	c.A = 0xff
	return c
}
