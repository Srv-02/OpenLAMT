package ui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// logRow is one line of the event or audit log table, already formatted.
type logRow struct {
	cells  [4]string
	short  string // the time in the sidebar (formatLogTimeShort)
	sev    amt.Severity
	hasSev bool   // the second column is a severity with a dot (event log)
	search string // lower-case text of all cells, for the filter
}

func newLogRow(cells [4]string, sev amt.Severity, hasSev bool) logRow {
	return logRow{cells: cells, sev: sev, hasSev: hasSev, search: strings.ToLower(strings.Join(cells[:], "\x00"))}
}

// newTimedLogRow is a log row whose first column is the time at.
func newTimedLogRow(at time.Time, loc *time.Location, rest [3]string, sev amt.Severity, hasSev bool) logRow {
	r := newLogRow([4]string{formatLogTime(at, loc), rest[0], rest[1], rest[2]}, sev, hasSev)
	r.short = formatLogTimeShort(at, loc)
	return r
}

// formatLogTime is a log time in loc as in WindowB.dc.html: "Nov 11, 2026 16:41:52".
func formatLogTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("Jan 02, 2006 15:04:05")
}

// formatLogTimeShort is the time in the 400 px sidebar, without year and seconds, so the
// description keeps most of the width: "Nov 11 16:41". The window shows the full time.
func formatLogTimeShort(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("Jan 02 15:04")
}

// logShortTimeWidth is the width of the time column in the sidebar: the widest short time of
// any month at the sidebar's 12 px, plus the cell padding.
func logShortTimeWidth() float32 {
	var w float32
	for m := time.January; m <= time.December; m++ {
		s := time.Date(2026, m, 20, 20, 0, 0, 0, time.UTC).Format("Jan 02 15:04")
		w = max(w, fyne.MeasureText(s, 12, fyne.TextStyle{}).Width)
	}
	return float32(math.Ceil(float64(w))) + 2*8 + 2
}

// severityText is the caption of a severity: Info, Warning, Critical.
func severityText(s amt.Severity) string {
	switch s {
	case amt.SeverityWarning:
		return lang.L("Warning")
	case amt.SeverityCritical:
		return lang.L("Critical")
	}
	return lang.L("Info")
}

// severityColor is the token of the severity dot.
func severityColor(s amt.Severity) fyne.ThemeColorName {
	switch s {
	case amt.SeverityWarning:
		return style.ColorNameSeverityWarning
	case amt.SeverityCritical:
		return style.ColorNameSeverityCritical
	}
	return style.ColorNameSeverityInfo
}

// eventRows formats the event log (newest first, as read) as Time | Severity | Source | Description.
func eventRows(entries []amt.EventLogEntry, loc *time.Location) []logRow {
	out := make([]logRow, len(entries))
	for i, e := range entries {
		out[i] = newTimedLogRow(e.Time, loc, [3]string{severityText(e.Severity), e.Source, e.Description}, e.Severity, true)
	}
	return out
}

// auditInitiator names who caused an audit event: the user name or Kerberos SID, otherwise the
// kind of initiator (2 local, 3 KVM default port).
func auditInitiator(e amt.AuditLogEntry) string {
	if e.Initiator != "" {
		return e.Initiator
	}
	switch e.InitiatorType {
	case 2:
		return lang.L("Local")
	case 3:
		return lang.L("KVM default port")
	}
	return ""
}

// auditRows formats the audit log (newest first, as read) as Time | Initiator | Address | Event;
// details of the event follow it after " · ".
func auditRows(entries []amt.AuditLogEntry, loc *time.Location) []logRow {
	out := make([]logRow, len(entries))
	for i, e := range entries {
		out[i] = newTimedLogRow(e.Time, loc, [3]string{auditInitiator(e), e.Address, joinDot(e.Event, e.Details)}, 0, false)
	}
	return out
}

// logAll is the filter level of the chip "All".
const logAll = -1

// filterLogRows returns the indexes of the rows with severity level (logAll: any) whose text
// contains text (case-insensitive), appended to dst[:0].
func filterLogRows(rows []logRow, level int, text string, dst []int) []int {
	dst = dst[:0]
	text = strings.ToLower(strings.TrimSpace(text))
	for i := range rows {
		r := &rows[i]
		if level != logAll && (!r.hasSev || int(r.sev) != level) {
			continue
		}
		if text != "" && !strings.Contains(r.search, text) {
			continue
		}
		dst = append(dst, i)
	}
	return dst
}

// entriesText is "209 entries".
func entriesText(n int) string {
	if n == 1 {
		return lang.L("1 entry")
	}
	return lang.L("{{.Count}} entries", map[string]any{"Count": n})
}

// descriptionText is the column caption "Description". go-i18n reserves the key "description"
// (any case) for its message format, so the key carries a trailing space that is cut off here.
func descriptionText() string { return strings.TrimSpace(lang.L("Description ")) }

// logSpec describes a log page.
type logSpec struct {
	columns [4]string
	widths  [3]float32 // width of the first three columns in the window; the last takes the rest
	compact [3]float32 // the same in the sidebar, with the short time: the last column is the widest
	chips   bool       // filter chips All, Warning, Critical
	src     *manageSource[[]logRow]
}

func (m *manager) eventLogPane(compact bool) *managePane {
	return m.logPane(logSpec{
		columns: [4]string{lang.L("Time"), lang.L("Severity"), lang.L("Source"), descriptionText()},
		widths:  [3]float32{170, 110, 110},
		compact: [3]float32{logShortTimeWidth(), 78, 76},
		chips:   true,
		src:     m.events,
	}, compact)
}

func (m *manager) auditLogPane(compact bool) *managePane {
	return m.logPane(logSpec{
		columns: [4]string{lang.L("Time"), lang.L("Initiator"), lang.L("Address"), lang.L("Event")},
		widths:  [3]float32{170, 160, 150},
		compact: [3]float32{logShortTimeWidth(), 68, 80},
		src:     m.audit,
	}, compact)
}

// logPane is the state of one view of a log page.
type logPane struct {
	spec    logSpec
	rows    []logRow
	shown   []int // indexes into rows after the filters
	level   int   // logAll or an amt.Severity
	chips   []*widgets.Chip
	levels  []int
	filter  *widgets.FilterField
	refresh *widgets.PrimaryButton
	count   *canvas.Text
	note    *canvas.Text
	err     *manageError
	list    *widget.List
}

// logPane builds an event or audit log page (WindowB.dc.html): in the window the page head
// holds the count, the chips, the filter and Refresh; in the sidebar they sit in two rows above
// the table. The table is a virtual list, so thousands of entries stay fast.
func (m *manager) logPane(spec logSpec, compact bool) *managePane {
	p := &logPane{spec: spec, level: logAll, err: newManageError()}
	widths := spec.widths
	if compact {
		widths = spec.compact
	}

	p.list = widget.NewList(func() int { return len(p.shown) },
		func() fyne.CanvasObject { return newLogLine(widths, compact, false) },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= 0 && id < len(p.shown) {
				o.(*logLine).set(&p.rows[p.shown[id]])
			}
		})
	p.list.OnSelected = func(id widget.ListItemID) { p.list.Unselect(id) } // rows are read-only

	header := newLogLine(widths, compact, true)
	header.set(&logRow{cells: spec.columns})
	table := logTable(header, p.list)

	if spec.chips {
		for i, c := range []struct {
			text  string
			level int
		}{{lang.L("All"), logAll}, {lang.L("Warning"), int(amt.SeverityWarning)}, {lang.L("Critical"), int(amt.SeverityCritical)}} {
			p.levels = append(p.levels, c.level)
			p.chips = append(p.chips, widgets.NewChip(c.text, func() { p.setLevel(i) }))
		}
		p.chips[0].SetActive(true)
	}
	p.filter = widgets.NewFilterField(func(string) { p.apply() })
	p.refresh = newManageButton(lang.L("Refresh"), spec.src.reload)
	p.note = designText("", 12, style.ColorNameTextSecondary, false)
	note := container.New(layout.NewCustomPaddedLayout(12, 0, 2, 2), p.note)

	var chips fyne.CanvasObject = container.NewStack()
	if len(p.chips) > 0 {
		objs := make([]fyne.CanvasObject, len(p.chips))
		for i, c := range p.chips {
			objs[i] = c
		}
		chips = container.New(layout.NewCustomPaddedHBoxLayout(6), objs...)
	}

	pane := &managePane{shown: spec.src.ensure, impl: p}
	if compact {
		p.count = designText("", 12, style.ColorNameTextSecondary, false)
		row1 := container.New(&rowLayout{gap: 8, fill: 1}, chips, spacer(), p.count)
		row2 := container.New(&rowLayout{gap: 8, fill: 0}, p.filter, p.refresh)
		top := container.New(layout.NewCustomPaddedVBoxLayout(8), row1, row2, p.err.box)
		pane.body = container.NewBorder(top, note, nil, nil, table)
	} else {
		p.count = designText("", 14, style.ColorNameTextSecondary, false)
		pane.info = p.count
		pane.tools = container.New(layout.NewCustomPaddedHBoxLayout(12), chips, p.filter, p.refresh)
		top := container.New(layout.NewCustomPaddedVBoxLayout(8), p.err.box)
		pane.body = container.NewBorder(top, note, nil, nil, table)
	}

	render := func() {
		p.rows = spec.src.val
		p.render(spec.src, compact)
		p.apply()
		pane.changed()
	}
	pane.close = spec.src.subscribe(render)
	render()
	return pane
}

// render shows count, note, error and the state of Refresh.
func (p *logPane) render(src *manageSource[[]logRow], compact bool) {
	count := ""
	if src.loaded() {
		count = entriesText(len(p.rows))
		if !compact {
			count = "· " + count
		}
	}
	p.count.Text = count
	p.count.Refresh()
	note := ""
	switch {
	case src.loading:
		note = lang.L("Reading…")
	case src.loaded():
		note = lang.L("Newest first. Read in {{.Seconds}} s.", map[string]any{"Seconds": fmt.Sprintf("%.1f", src.took.Seconds())})
	}
	p.note.Text = note
	p.note.Refresh()
	p.err.set(src.err)
	if src.loading {
		p.refresh.Disable()
	} else {
		p.refresh.Enable()
	}
}

// setLevel selects chip i.
func (p *logPane) setLevel(i int) {
	p.level = p.levels[i]
	for j, c := range p.chips {
		c.SetActive(j == i)
	}
	p.apply()
}

// apply filters the rows again and shows the result from the top.
func (p *logPane) apply() {
	p.shown = filterLogRows(p.rows, p.level, p.filter.Text(), p.shown)
	p.list.Refresh()
	p.list.ScrollToTop()
}

// logTable frames header and list as the table of WindowB.dc.html: 1 px border, radius 10.
func logTable(header *logLine, list *widget.List) fyne.CanvasObject {
	bg := style.BindRect(&canvas.Rectangle{CornerRadius: 10}, "background", "")
	outline := style.BindRect(&canvas.Rectangle{CornerRadius: 10, StrokeWidth: 1, FillColor: color.Transparent}, "", style.ColorNameCardBorder)
	body := container.NewThemeOverride(list, logListTheme{overrideTheme})
	inner := container.New(layout.NewCustomPaddedLayout(1, 1, 1, 1),
		container.New(&columnLayout{fill: 2}, header, hline(style.ColorNameCardBorder), body))
	return container.NewStack(bg, inner, outline)
}

// logListTheme draws the list rows 1 px apart with a hairline between them and square hover.
type logListTheme struct{ fyne.Theme }

func (t logListTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 1
	case theme.SizeNameSelectionRadius:
		return 0
	}
	return t.Theme.Size(name)
}

// logLine is one line of a log table, or its header: four columns, the first three of fixed
// width, text cut with "…"; the event severity has a dot before it.
type logLine struct {
	widget.BaseWidget
	widths  [3]float32
	compact bool
	header  bool
	row     *logRow
}

func newLogLine(widths [3]float32, compact, header bool) *logLine {
	l := &logLine{widths: widths, compact: compact, header: header}
	l.ExtendBaseWidget(l)
	return l
}

// set shows r.
func (l *logLine) set(r *logRow) {
	l.row = r
	l.Refresh()
}

// Text returns the full text of column i (tests).
func (l *logLine) Text(i int) string {
	if l.row == nil {
		return ""
	}
	return l.row.cells[i]
}

// Geometry: cell padding 8 x 12 (WindowB.dc.html .td/.th), in the sidebar 7 x 8; 8 px dot, 6 px gap.
func (l *logLine) pads() (v, h float32) {
	if l.compact {
		return 7, 8
	}
	return 8, 12
}

func (l *logLine) textSize() float32 {
	if l.compact || l.header {
		return 12
	}
	return 13
}

func (l *logLine) CreateRenderer() fyne.WidgetRenderer {
	r := &logLineRenderer{l: l, dot: &canvas.Circle{}}
	if l.header {
		r.bg = &canvas.Rectangle{TopLeftCornerRadius: 9, TopRightCornerRadius: 9}
	}
	for i := range r.texts {
		r.texts[i] = canvas.NewText("", nil)
		r.texts[i].TextStyle = fyne.TextStyle{Bold: l.header}
	}
	r.Refresh()
	return r
}

type logLineRenderer struct {
	l     *logLine
	bg    *canvas.Rectangle // header only
	texts [4]*canvas.Text
	dot   *canvas.Circle
}

const (
	logDot    float32 = 8
	logDotGap float32 = 6
)

func (r *logLineRenderer) MinSize() fyne.Size {
	padV, padH := r.l.pads()
	h := fyne.MeasureText("Mg", r.l.textSize(), fyne.TextStyle{Bold: r.l.header}).Height
	w := r.l.widths[0] + r.l.widths[1] + r.l.widths[2] + 2*padH + 40
	return fyne.NewSize(w, h+2*padV)
}

func (r *logLineRenderer) Layout(size fyne.Size) {
	l := r.l
	_, padH := l.pads()
	if r.bg != nil {
		r.bg.Resize(size)
	}
	st := fyne.TextStyle{Bold: l.header}
	ts := l.textSize()
	x := float32(0)
	dotShown := l.row != nil && l.row.hasSev && !l.header
	for i, t := range r.texts {
		w := size.Width - x
		if i < 3 {
			w = l.widths[i]
		}
		left, room := x+padH, w-2*padH
		if i == 1 && dotShown {
			r.dot.Move(fyne.NewPos(left, (size.Height-logDot)/2))
			r.dot.Resize(fyne.NewSquareSize(logDot))
			left += logDot + logDotGap
			room -= logDot + logDotGap
		}
		full := ""
		if l.row != nil {
			full = l.row.cells[i]
			if i == 0 && l.compact && l.row.short != "" {
				full = l.row.short
			}
		}
		t.Text = ellipsize(full, ts, st, max(0, room))
		tsz := fyne.MeasureText(t.Text, ts, st)
		t.Move(fyne.NewPos(left, (size.Height-tsz.Height)/2))
		t.Resize(tsz)
		x += w
	}
	r.dot.Hidden = !dotShown
}

func (r *logLineRenderer) Refresh() {
	l := r.l
	fg := style.Color("foreground")
	if l.header {
		fg = style.Color(style.ColorNameTextSecondary)
		r.bg.FillColor = style.Color(style.ColorNameTableHeader)
		r.bg.Refresh()
	}
	for _, t := range r.texts {
		t.Color, t.TextSize = fg, l.textSize()
	}
	if l.row != nil && l.row.hasSev {
		r.dot.FillColor = style.Color(severityColor(l.row.sev))
	}
	r.Layout(l.Size())
	for _, t := range r.texts {
		t.Refresh()
	}
	r.dot.Refresh()
}

func (r *logLineRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{}
	if r.bg != nil {
		objs = append(objs, r.bg)
	}
	objs = append(objs, r.dot)
	for _, t := range r.texts {
		objs = append(objs, t)
	}
	return objs
}
func (r *logLineRenderer) Destroy() {}
