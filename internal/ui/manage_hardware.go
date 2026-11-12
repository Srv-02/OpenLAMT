package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// hwRow is one line of a hardware card: label and value.
type hwRow struct{ key, value string }

// hwSection is one hardware card with its heading.
type hwSection struct {
	title string
	rows  []hwRow
}

// hardwareSections turns the inventory into the cards of Main.dc.html: System, Processor and
// memory, Drives, AMT. Empty values and empty cards are left out; the row Mode shows only ACM
// or CCM.
func hardwareSections(hw amt.HardwareInfo, mode amt.ControlMode) []hwSection {
	var out []hwSection
	add := func(title string, rows []hwRow) {
		var kept []hwRow
		for _, r := range rows {
			if r.value != "" {
				kept = append(kept, r)
			}
		}
		if len(kept) > 0 {
			out = append(out, hwSection{title, kept})
		}
	}

	model := hw.System.Version // on many vendors the product name; Model is the type number
	if model == "" {
		model = hw.System.Model
	}
	add(lang.L("System"), []hwRow{
		{lang.L("Manufacturer"), hw.System.Manufacturer},
		{lang.L("Model"), model},
		{lang.L("Serial number"), hw.System.SerialNumber},
		{"BIOS", formatBIOS(hw.BIOS)},
	})

	var pm []hwRow
	for i, c := range hw.CPUs {
		key := "CPU"
		if len(hw.CPUs) > 1 {
			key = fmt.Sprintf("CPU %d", i)
		}
		pm = append(pm, hwRow{key, joinDot(c.Name, formatGHz(c.MaxClockMHz))})
	}
	for _, r := range hw.Memory {
		key := strings.TrimSpace("RAM " + r.Bank)
		size := strings.TrimSpace(formatMemory(r.CapacityBytes) + " " + r.Type)
		speed := ""
		if r.SpeedMTs > 0 {
			speed = fmt.Sprintf("%d MT/s", r.SpeedMTs)
		}
		pm = append(pm, hwRow{key, joinDot(size, speed)})
	}
	add(lang.L("Processor and memory"), pm)

	var drives []hwRow
	for _, d := range hw.Drives {
		drives = append(drives, hwRow{d.ID, formatDrive(d.CapacityBytes)})
	}
	add(lang.L("Drives"), drives)

	if mode != amt.ControlModeAdmin && mode != amt.ControlModeClient {
		mode = ""
	}
	build := ""
	if hw.AMT.Build != "" {
		build = lang.L("Build {{.Build}}", map[string]any{"Build": hw.AMT.Build})
	}
	add("AMT", []hwRow{
		{lang.L("Version"), joinDot(hw.AMT.Version, build)},
		{lang.L("Mode"), string(mode)},
	})
	return out
}

// joinDot joins the non-empty parts with " · ".
func joinDot(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// formatBIOS is "2.14.0 · Sep 18, 2026".
func formatBIOS(b amt.BIOSInfo) string {
	date := ""
	if !b.ReleaseDate.IsZero() {
		date = b.ReleaseDate.UTC().Format("Jan 2, 2006")
	}
	return joinDot(b.Version, date)
}

// formatGHz is a clock in MHz as "4.6 GHz"; 0 is "".
func formatGHz(mhz int) string {
	if mhz <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f GHz", float64(mhz)/1000)
}

// formatMemory is a module size in binary units, as RAM is labeled: 16 GiB is "16 GB".
func formatMemory(b uint64) string {
	switch {
	case b == 0:
		return ""
	case b >= 1<<30 && b%(1<<30) == 0:
		return fmt.Sprintf("%d GB", b>>30)
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MB", b>>20)
}

// formatDrive is a drive size in decimal units, as drives are labeled: "1.0 TB", "512 GB".
func formatDrive(b uint64) string {
	switch {
	case b == 0:
		return ""
	case b >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(b)/1e12)
	case b >= 1e9:
		return fmt.Sprintf("%.0f GB", float64(b)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(b)/1e6)
}

// majorMinor shortens "16.1.25" to "16.1".
func majorMinor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, ".")
}

// readAgo is the footer "Read 12 s ago".
func readAgo(d time.Duration) string {
	s := max(0, int(d/time.Second))
	switch {
	case s < 60:
		return lang.L("Read {{.Seconds}} s ago", map[string]any{"Seconds": s})
	case s < 3600:
		return lang.L("Read {{.Minutes}} min ago", map[string]any{"Minutes": s / 60})
	}
	return lang.L("Read {{.Hours}} h ago", map[string]any{"Hours": s / 3600})
}

// hwPane is the state of one view of the Hardware page.
type hwPane struct {
	cards   *fyne.Container
	err     *manageError
	ago     *canvas.Text
	footer  *fyne.Container
	refresh *widgets.Link
}

// hardwarePane builds the Hardware page: the cards, then "Read 12 s ago" and the link Refresh.
// The window shows the same cards over the full width.
func (m *manager) hardwarePane(compact bool) *managePane {
	h := &hwPane{
		cards: container.New(layout.NewCustomPaddedVBoxLayout(14)),
		err:   newManageError(),
		ago:   designText("", 12, style.ColorNameTextSecondary, false),
	}
	h.refresh = &widgets.Link{Text: lang.L("Refresh"), OnTapped: m.hw.reload, TextSize: 12,
		Color: style.ColorNameLink, HoverColor: style.ColorNameLinkHover}
	h.refresh.ExtendBaseWidget(h.refresh)
	h.footer = container.New(&rowLayout{gap: 8, fill: 1}, h.ago, spacer(), h.refresh)
	body := container.New(layout.NewCustomPaddedVBoxLayout(14), h.err.box, h.cards, h.footer)

	var pane *managePane
	render := func() {
		var objs []fyne.CanvasObject
		if m.hw.loaded() {
			for _, s := range hardwareSections(m.hw.val, m.controlMode()) {
				objs = append(objs, hwCard(s))
			}
		}
		h.cards.Objects = objs
		h.cards.Refresh()
		h.err.set(m.hw.err)
		h.updateAgo(m)
		if pane != nil {
			pane.changed()
		}
	}
	pane = &managePane{body: body, scroll: true, shown: m.hw.ensure, tick: func() { h.updateAgo(m) }, impl: h}
	pane.close = m.hw.subscribe(render)
	render()
	return pane
}

// updateAgo shows "Reading…" while a read runs, else the age of the data.
func (h *hwPane) updateAgo(m *manager) {
	text := ""
	switch {
	case m.hw.loading:
		text = lang.L("Reading…")
	case m.hw.loaded():
		text = readAgo(m.now().Sub(m.hw.at))
	}
	if h.ago.Text != text {
		h.ago.Text = text
		h.ago.Refresh()
		h.footer.Refresh()
	}
}

// hwCard is a card of Main.dc.html: heading 12 semibold secondary, then a rounded card (radius
// 12) with one line per value, hairlines between them.
func hwCard(s hwSection) fyne.CanvasObject {
	head := container.New(layout.NewCustomPaddedLayout(0, 0, 4, 0), designText(s.title, 12, style.ColorNameTextSecondary, true))
	var rows []fyne.CanvasObject
	for i, r := range s.rows {
		if i > 0 {
			rows = append(rows, hline(style.ColorNameCardBorder))
		}
		rows = append(rows, newKVRow(r.key, r.value))
	}
	card := widgets.NewPanel(style.ColorNameCard, style.ColorNameCardBorder, 12,
		container.New(layout.NewCustomPaddedVBoxLayout(0), rows...), 1, 1, 1, 1)
	return container.New(layout.NewCustomPaddedVBoxLayout(6), head, card)
}

// kvRow is a line of a hardware card: label on the left (secondary), value on the right; the
// value ends in "…" when it does not fit. Padding 9 x 14, 13 px text.
type kvRow struct {
	widget.BaseWidget
	key, value string
}

const (
	kvPadV, kvPadH, kvGap, kvText float32 = 9, 14, 12, 13
)

func newKVRow(key, value string) *kvRow {
	r := &kvRow{key: key, value: value}
	r.ExtendBaseWidget(r)
	return r
}

func (r *kvRow) CreateRenderer() fyne.WidgetRenderer {
	k := canvas.NewText(r.key, nil)
	v := canvas.NewText(r.value, nil)
	k.TextSize, v.TextSize = kvText, kvText
	rr := &kvRowRenderer{r: r, k: k, v: v}
	rr.Refresh()
	return rr
}

type kvRowRenderer struct {
	r    *kvRow
	k, v *canvas.Text
}

func (rr *kvRowRenderer) MinSize() fyne.Size {
	ks := fyne.MeasureText(rr.r.key, kvText, fyne.TextStyle{})
	return fyne.NewSize(2*kvPadH+ks.Width+kvGap+40, 2*kvPadV+ks.Height)
}

func (rr *kvRowRenderer) Layout(size fyne.Size) {
	ks := fyne.MeasureText(rr.r.key, kvText, fyne.TextStyle{})
	rr.k.Move(fyne.NewPos(kvPadH, (size.Height-ks.Height)/2))
	rr.k.Resize(ks)
	room := size.Width - 2*kvPadH - ks.Width - kvGap
	rr.v.Text = ellipsize(rr.r.value, kvText, fyne.TextStyle{}, room)
	vs := fyne.MeasureText(rr.v.Text, kvText, fyne.TextStyle{})
	rr.v.Move(fyne.NewPos(size.Width-kvPadH-vs.Width, (size.Height-vs.Height)/2))
	rr.v.Resize(vs)
}

func (rr *kvRowRenderer) Refresh() {
	rr.k.Text = rr.r.key
	rr.k.Color = style.Color(style.ColorNameTextSecondary)
	rr.v.Color = style.Color("foreground")
	rr.Layout(rr.r.Size())
	rr.k.Refresh()
	rr.v.Refresh()
}

func (rr *kvRowRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{rr.k, rr.v} }
func (rr *kvRowRenderer) Destroy()                     {}
