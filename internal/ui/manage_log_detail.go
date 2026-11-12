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
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// logDetail is the full content of one log entry for the detail dialog (LogDetail.dc.html):
// the table cuts long texts, the dialog wraps them.
type logDetail struct {
	title   string
	fields  [][2]string // label and value, in order; empty values are left out
	details string      // audit log: the decoded extended data in a box; "" for none
	raw     [2]string   // label and the raw fields in monospace; empty label for none
}

// logTimeFull is the time of a log entry with its offset: "Nov 11, 2026 23:03:46 (UTC+01:00)".
func logTimeFull(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return formatLogTime(t, loc) + " (UTC" + t.In(loc).Format("-07:00") + ")"
}

func eventDetail(e amt.EventLogEntry, loc *time.Location) logDetail {
	title := e.Description
	if title == "" {
		title = lang.L("Event")
	}
	data := make([]string, len(e.EventData))
	for i, b := range e.EventData {
		data[i] = fmt.Sprintf("%02X", b)
	}
	return logDetail{
		title: title,
		fields: [][2]string{
			{lang.L("Time"), logTimeFull(e.Time, loc)},
			{lang.L("Severity"), severityText(e.Severity)},
			{lang.L("Source"), e.Source},
			{descriptionText(), e.Description},
		},
		raw: [2]string{lang.L("Raw data"), lang.L("Sensor type 0x{{.Sensor}} · offset 0x{{.Offset}} · data {{.Data}}", map[string]any{
			"Sensor": fmt.Sprintf("%02X", e.SensorType), "Offset": fmt.Sprintf("%02X", e.EventOffset), "Data": strings.Join(data, " "),
		})},
	}
}

// auditInitiatorFull is the initiator with its kind for a user name or SID.
func auditInitiatorFull(e amt.AuditLogEntry) string {
	who := auditInitiator(e)
	if e.Initiator == "" {
		return who
	}
	switch e.InitiatorType {
	case 0:
		return who + " (" + lang.L("digest user") + ")"
	case 1:
		return who + " (Kerberos)"
	}
	return who
}

func auditDetail(e amt.AuditLogEntry, loc *time.Location) logDetail {
	title := e.Event
	if title == "" {
		title = e.App
	}
	if title == "" {
		title = lang.L("Event")
	}
	return logDetail{
		title: title,
		fields: [][2]string{
			{lang.L("Time"), logTimeFull(e.Time, loc)},
			{lang.L("Application"), e.App},
			{lang.L("Initiator"), auditInitiatorFull(e)},
			{lang.L("Address"), e.Address},
		},
		details: e.Details,
		raw: [2]string{lang.L("Raw IDs"), lang.L("Application {{.App}} · event {{.Event}}", map[string]any{
			"App": e.AppID, "Event": e.EventID,
		})},
	}
}

// text is the entry as plain text for the clipboard: one "label: value" line per field.
func (d logDetail) text() string {
	var b strings.Builder
	b.WriteString(d.title + "\n")
	for _, f := range d.fields {
		if f[1] != "" {
			b.WriteString(f[0] + ": " + f[1] + "\n")
		}
	}
	if d.details != "" {
		b.WriteString(lang.L("Details") + ": " + d.details + "\n")
	}
	if d.raw[0] != "" {
		b.WriteString(d.raw[0] + ": " + d.raw[1] + "\n")
	}
	return b.String()
}

// logDetailDialog is the open detail dialog of a log page.
type logDetailDialog struct {
	*panel
	parent fyne.Window
	log    string // "Event log" or "Audit log"
	rows   []logRow
	shown  []int
	pos    int // index into shown
	newer  *escButton
	older  *escButton
	copyB  *escButton
}

// showLogDetail opens the entry shown[pos] of rows (LogDetail.dc.html): title, "<log> · entry n
// of m", the fields, the details and the raw data, Copy and Close. The arrows and Up/Down step
// to the newer and older entry of the filtered list without closing.
func showLogDetail(parent fyne.Window, log string, rows []logRow, shown []int, pos int) *logDetailDialog {
	d := &logDetailDialog{parent: parent, log: log, rows: rows, shown: shown}
	d.open(pos)
	return d
}

func (d *logDetailDialog) open(pos int) {
	if pos < 0 || pos >= len(d.shown) {
		return
	}
	if d.panel != nil {
		d.close()
	}
	d.pos = pos
	r := &d.rows[d.shown[pos]]
	p := showPanel(d.parent, panelSpec{title: r.detail.title, action: lang.L("Close"), flush: true},
		func(p *panel) []fyne.CanvasObject {
			d.copyB = p.newButton(lang.L("Copy"), func() { fyne.CurrentApp().Clipboard().SetContent(r.detail.text()) })
			p.extras = []fyne.CanvasObject{d.copyB}
			return d.body(p, r)
		})
	d.panel = p
	prev := p.canvas.OnTypedKey() // the panel's Esc and Enter
	p.canvas.SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeyUp:
			d.step(-1)
		case fyne.KeyDown:
			d.step(1)
		default:
			prev(e)
		}
	})
}

// step moves to the newer (-1) or older (+1) entry.
func (d *logDetailDialog) step(delta int) {
	if !d.done {
		d.open(d.pos + delta)
	}
}

func (d *logDetailDialog) body(p *panel, r *logRow) []fyne.CanvasObject {
	sub := designText(lang.L("{{.Log}} · entry {{.N}} of {{.Count}}", map[string]any{"Log": d.log, "N": d.pos + 1, "Count": len(d.shown)}),
		12, style.ColorNameTextSecondary, false)
	d.newer = p.newButton("", func() { d.step(-1) })
	d.newer.Icon = theme.MoveUpIcon()
	d.older = p.newButton("", func() { d.step(1) })
	d.older.Icon = theme.MoveDownIcon()
	if d.pos == 0 {
		d.newer.Disable()
	}
	if d.pos == len(d.shown)-1 {
		d.older.Disable()
	}
	head := container.New(&rowLayout{gap: 8, fill: 0}, sub, spacer(), d.newer, d.older)

	labels := func(s string) fyne.CanvasObject {
		l := widget.NewLabel(s)
		l.Importance = widget.LowImportance
		return l
	}
	var grid []fyne.CanvasObject
	add := func(label string, value fyne.CanvasObject) {
		grid = append(grid, labels(label), value)
	}
	for _, f := range r.detail.fields {
		if f[1] != "" {
			add(f[0], wrappedValue(f[1], false))
		}
	}
	raw := r.detail.raw[0] != ""
	if raw && r.detail.details == "" { // without the details box the raw data join the grid
		add(r.detail.raw[0], wrappedValue(r.detail.raw[1], true))
		raw = false
	}
	objs := []fyne.CanvasObject{head, flushText(container.New(layout.NewFormLayout(), grid...))}
	if r.detail.details != "" {
		bg := style.BindRect(&canvas.Rectangle{CornerRadius: 10, StrokeWidth: 1}, style.ColorNameField, style.ColorNameCardBorder)
		box := container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(4, 4, 4, 4), flushText(wrappedValue(r.detail.details, false))))
		objs = append(objs, formField(lang.L("Details"), box))
	}
	if raw {
		objs = append(objs, flushText(container.New(layout.NewFormLayout(), labels(r.detail.raw[0]), wrappedValue(r.detail.raw[1], true))))
	}
	return objs
}

// wrappedValue is a value of the detail dialog, wrapped; Copy takes all of them.
func wrappedValue(s string, mono bool) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	l.TextStyle.Monospace = mono
	return l
}
