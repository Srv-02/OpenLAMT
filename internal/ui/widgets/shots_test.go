package widgets

import (
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// TestShots renders the widgets with the software painter into $OPENLAMT_SHOTS (PNG, scale 2)
// for review against the design. Skipped without the variable. Only made-up data.
func TestShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	themedApp(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := func(name string, content fyne.CanvasObject, size fyne.Size) {
		c := software.NewCanvas()
		c.SetPadded(false)
		c.SetScale(shotScale())
		c.SetContent(content)
		c.Resize(size)
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, c.Capture()); err != nil {
			t.Fatal(err)
		}
	}
	bg := func(name fyne.ThemeColorName, o fyne.CanvasObject) fyne.CanvasObject {
		return container.NewStack(canvas.NewRectangle(style.Color(name)), o)
	}

	shot("toolbar.png", container.NewVBox(toolbar(true), toolbar(false)), fyne.NewSize(760, 2*style.ToolbarHeight+1))
	shot("sidebar.png", bg(style.ColorNameSidebar, container.NewVBox(sectionLabel(), container.NewPadded(sidebar()))), fyne.NewSize(style.SidebarWidth, 230))
	shot("sidebar-selected.png", bg(style.ColorNameSidebar, container.NewPadded(selectedRows())), fyne.NewSize(style.SidebarWidth, 180))
	shot("formcard.png", bg("background", container.New(layout.NewCustomPaddedLayout(24, 24, 24, 24), container.NewVBox(cards()...))), fyne.NewSize(560, 420))
	shot("icons.png", bg("background", container.NewPadded(iconGrid())), fyne.NewSize(20*30+20, float32(len(icons.Variants))*30+20))
}

// shotScale is $OPENLAMT_SHOT_SCALE, default 2.
func shotScale() float32 {
	if f, err := strconv.ParseFloat(os.Getenv("OPENLAMT_SHOT_SCALE"), 32); err == nil && f > 0 {
		return float32(f)
	}
	return 2
}

func divider() fyne.CanvasObject {
	r := canvas.NewRectangle(style.Color(style.ColorNameGroupDivider))
	r.SetMinSize(fyne.NewSize(1, 30))
	return container.NewCenter(r)
}

func toolbar(connected bool) fyne.CanvasObject {
	name := canvas.NewText("Lab-Server", style.Color("foreground"))
	name.TextStyle.Bold = true
	name.TextSize = style.Size(style.SizeNameButtonText)
	host := canvas.NewText("192.0.2.10", style.Color(style.ColorNameTextSecondary))
	host.TextSize = 11

	power := NewToolbarMenuButton(icons.Power, "Power", nil)
	power.SetDot(DotOn)
	kvm := NewToolbarButton(icons.KVM, "KVM", nil)
	quality := NewToolbarMenuButton(icons.KVM, "Quality", nil)
	quality.SetBadge("HQ")
	cad := NewToolbarButton(icons.Keyboard, "Ctrl+Alt+Del", nil)
	clip := NewToolbarButton(icons.ClipboardType, "Type Clipboard", nil)
	full := NewToolbarButton(icons.Fullscreen, "Full Screen", nil)
	settings := NewToolbarButton(icons.Settings, "Settings", nil)
	if connected {
		kvm.SetActive(true)
	} else {
		power.SetOpen(true)
		for _, b := range []*ToolbarButton{cad, clip, full} {
			b.Disable()
		}
	}
	row := container.NewHBox(
		container.NewCenter(container.NewVBox(name, host)), layout.NewSpacer(),
		power, divider(), kvm, quality, divider(), cad, clip, divider(), full, settings,
	)
	padded := container.New(layout.NewCustomPaddedLayout(0, 0, 18, 12), row)
	line := canvas.NewRectangle(style.Color(style.ColorNameSeparatorDark))
	line.SetMinSize(fyne.NewSize(1, 1))
	return container.NewVBox(bgSized(style.ColorNameToolbar, padded, style.ToolbarHeight), line)
}

func bgSized(name fyne.ThemeColorName, o fyne.CanvasObject, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(style.Color(name))
	r.SetMinSize(fyne.NewSize(1, h))
	return container.NewStack(r, o)
}

func sectionLabel() fyne.CanvasObject {
	l := canvas.NewText("Favorites", style.Color(style.ColorNameSectionLabel))
	l.TextSize = style.Size(style.SizeNameSectionLabel)
	l.TextStyle.Bold = true
	return container.New(layout.NewCustomPaddedLayout(8, 6, 20, 0), l)
}

func sidebar() fyne.CanvasObject {
	a := NewSidebarRow("Lab-Server", "192.0.2.10")
	a.SetSelected(true)
	a.SetDot(DotOn)
	b := NewSidebarRow("Build Host", "build.example.com")
	b.SetDot(DotOff)
	c := NewSidebarRow("Spare machine with a rather long name", "192.0.2.30")
	c.SetDot(DotUnknown)
	d := NewSidebarRow("Test Bench", "bench.example.com")
	return container.New(layout.NewCustomPaddedVBoxLayout(2), a, b, c, d)
}

// selectedRows is a selected row with the dot on, off, asleep and unknown (issues #69, #120).
func selectedRows() fyne.CanvasObject {
	row := func(name string, d DotState) fyne.CanvasObject {
		r := NewSidebarRow(name, "192.0.2.10")
		r.SetSelected(true)
		r.SetDot(d)
		return r
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(2), row("Dot on", DotOn), row("Dot off", DotOff), row("Dot asleep", DotAsleep), row("Dot unknown", DotUnknown))
}

func cards() []fyne.CanvasObject {
	entry := func(s string) *widget.Entry { e := widget.NewEntry(); e.SetText(s); return e }
	pw := widget.NewPasswordEntry()
	pw.SetText("secret-example")
	tls := NewSwitch(nil)
	tls.SetChecked(true)
	save := NewSwitch(nil)
	off := NewSwitch(nil)
	off.SetChecked(true)
	off.Disable()
	return []fyne.CanvasObject{
		NewFormCard(
			NewFormRow("Host", entry("192.0.2.10")),
			NewFormRow("Name", entry("Lab-Server")),
			NewFormRow("Username", entry("admin")),
			NewFormRow("Password", pw),
		),
		NewFormCard(
			NewIconRow(icons.Lock, "Use TLS", tls),
			NewIconRow(icons.Key, "Save password in keychain", save),
			NewIconRow(icons.Keyboard, "Disabled switch (on)", off),
		),
	}
}

func iconGrid() fyne.CanvasObject {
	g := container.NewGridWithColumns(len(icons.All))
	for _, v := range icons.Variants {
		for _, n := range icons.All {
			img := canvas.NewImageFromResource(icons.Get(n, v))
			img.FillMode = canvas.ImageFillContain
			img.SetMinSize(fyne.NewSquareSize(20))
			g.Add(container.NewCenter(img))
		}
	}
	return g
}
