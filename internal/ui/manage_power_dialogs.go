package ui

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The "Add wake alarm" dialog (overall design, Power page, V24): name, date and time in the
// local time zone, the repeat and "Delete after it fires". AMT stores the time in UTC; the
// device cannot edit an alarm, so changing one means deleting it and adding it again.

const (
	alarmDateLayout = "2006-01-02"
	alarmTimeLayout = "15:04"
)

// alarmDialog is the shown "Add wake alarm" dialog; its controls are exposed for tests.
type alarmDialog struct {
	*panel
	name, date, clock *dialogEntry
	repeat            *widgets.Dropdown
	every             *dialogEntry // custom repeat: the number
	unit              *widgets.Dropdown
	custom            fyne.CanvasObject // the row of every and unit; shown for "Custom"
	del               *widgets.Switch   // "Delete after it fires"
	err               *manageError
	busy              bool
	loc               *time.Location
	m                 *manager
}

// repeat choices of the dialog, in the order of the dropdown.
func repeatChoices() []string {
	return []string{lang.L("Never"), lang.L("Every day"), lang.L("Every week"), lang.L("Custom")}
}

// unit choices of the custom repeat.
func unitChoices() []string { return []string{lang.L("minutes"), lang.L("hours"), lang.L("days")} }

// spec reads the form: ok is false while a field is not valid (the button stays disabled).
func (d *alarmDialog) spec() (amt.AlarmSpec, bool) {
	name := strings.TrimSpace(d.name.Text)
	if name == "" || utf8.RuneCountInString(name) > amt.MaxAlarmName {
		return amt.AlarmSpec{}, false
	}
	start, err := time.ParseInLocation(alarmDateLayout+" "+alarmTimeLayout,
		strings.TrimSpace(d.date.Text)+" "+strings.TrimSpace(d.clock.Text), d.loc)
	if err != nil {
		return amt.AlarmSpec{}, false
	}
	var every time.Duration
	switch d.repeat.SelectedIndex() {
	case 1:
		every = 24 * time.Hour
	case 2:
		every = 7 * 24 * time.Hour
	case 3:
		n, err := strconv.Atoi(strings.TrimSpace(d.every.Text))
		if err != nil || n < 1 {
			return amt.AlarmSpec{}, false
		}
		unit := []time.Duration{time.Minute, time.Hour, 24 * time.Hour}[max(0, d.unit.SelectedIndex())]
		if time.Duration(n) > amt.MaxAlarmInterval/unit {
			return amt.AlarmSpec{}, false
		}
		every = time.Duration(n) * unit
	}
	return amt.AlarmSpec{Name: name, Start: start.UTC(), Interval: every,
		DeleteOnCompletion: d.del.Checked() && every == 0}, true
}

// validate enables the button while the form is valid and nothing is being sent; "Delete after
// it fires" only applies to an alarm that does not repeat.
func (d *alarmDialog) validate() {
	once := d.repeat.SelectedIndex() == 0
	if !once {
		d.del.SetChecked(false)
	}
	setEnabled(d.del, once)
	setShown(d.custom, d.repeat.SelectedIndex() == 3)
	_, ok := d.spec()
	if ok && !d.busy {
		d.action.Enable()
	} else {
		d.action.Disable()
	}
	d.panel.frame.Refresh()
}

// openAdd is "+ Add Alarm".
func (p *powerPane) openAdd() {
	m := p.m
	d := &alarmDialog{m: m, loc: m.now().Location(), err: newManageError()}
	tomorrow := m.now().In(d.loc).AddDate(0, 0, 1)
	d.panel = showPanel(p.window(), panelSpec{
		title: lang.L("Add wake alarm"), action: lang.L("Add Alarm"), cancel: lang.L("Cancel"), flush: true, keep: true,
		onAction: func() { d.submit(p) },
	}, func(pn *panel) []fyne.CanvasObject {
		d.panel = pn
		d.name = pn.newEntry(false)
		d.date, d.clock = pn.newEntry(false), pn.newEntry(false)
		d.date.SetPlaceHolder(alarmDateLayout)
		d.clock.SetPlaceHolder(alarmTimeLayout)
		d.date.SetText(tomorrow.Format(alarmDateLayout))
		d.clock.SetText("02:00")
		d.every = pn.newEntry(false)
		d.every.SetText("12")
		d.repeat = widgets.NewDropdown(repeatChoices())
		d.repeat.OnEscape = pn.Dismiss
		d.unit = widgets.NewDropdown(unitChoices())
		d.unit.OnEscape = pn.Dismiss
		d.unit.SetSelectedIndex(1)
		d.del = widgets.NewSwitch(nil)
		d.del.OnEscape = pn.Dismiss
		for _, e := range []*dialogEntry{d.name, d.date, d.clock, d.every} {
			e.OnChanged = func(string) { d.err.set(nil); d.validate() }
		}
		d.name.OnChanged = func(s string) { // at most 32 characters, as MeshCommander
			if r := []rune(s); len(r) > amt.MaxAlarmName {
				d.name.SetText(string(r[:amt.MaxAlarmName]))
				return
			}
			d.err.set(nil)
			d.validate()
		}
		d.repeat.OnChanged = func(string) { d.validate() }
		d.unit.OnChanged = func(string) { d.validate() }
		d.custom = container.NewGridWithColumns(2, framedEntry(d.every), d.unit)
		pn.focus = d.name
		return []fyne.CanvasObject{
			formField(lang.L("Name"), framedEntry(d.name)),
			container.NewGridWithColumns(2, formField(lang.L("Date"), framedEntry(d.date)), formField(lang.L("Time"), framedEntry(d.clock))),
			formField(lang.L("Repeat"), d.repeat),
			d.custom,
			container.New(&rowLayout{gap: 8, fill: 1}, designText(lang.L("Delete after it fires"), 13, theme.ColorNameForeground, false), spacer(), d.del),
			formHint(lang.L("Only for alarms that do not repeat.")),
			container.New(layout.NewCustomPaddedLayout(0, 0, 0, 0), d.err.box),
		}
	})
	d.validate()
	p.addDlg = d
}

// submit sends the alarm. A refusal stays in the dialog as a red line, so the input is kept.
func (d *alarmDialog) submit(p *powerPane) {
	m := p.m
	spec, ok := d.spec()
	if !ok || d.busy {
		return
	}
	if !spec.Start.After(m.now()) {
		d.showError(amt.ErrAlarmPast)
		return
	}
	d.busy = true
	d.validate()
	manageWrite(m, p.d.alarms, func() error { return m.sess.AddAlarm(spec) }, func(err error) {
		d.busy = false
		if err != nil {
			d.showError(err)
			d.validate()
			return
		}
		d.close()
	}, nil)
}

func (d *alarmDialog) showError(err error) {
	d.err.label.SetText(d.m.alarmErrorText(err))
	d.err.box.Show()
	d.panel.frame.Refresh()
}
