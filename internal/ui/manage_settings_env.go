package ui

import (
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// "Change environment detection" (HostEnv.dc.html, right): the red warning, the intranet domains
// with a remove button each, "Add domain" with Add, and the typed device address. Outside the
// listed domains AMT closes its network access, so every change is a red confirmation.

// maxEnvDomains is the number of intranet domains AMT takes (amt.ValidateEnvironmentDomains).
const maxEnvDomains = 5

// envForm is the content of "Change environment detection".
type envForm struct {
	cur     []string // the device's domains
	domains []string // the edited list
	rows    []*envRow
	list    *fyne.Container
	listBox fyne.CanvasObject
	input   *dialogEntry
	add     *escButton
	d       *riskDialog
}

// envRow is a domain of the list with its remove button.
type envRow struct {
	domain string
	remove *envRemoveButton
	obj    fyne.CanvasObject
}

// openEnvironment opens "Change environment detection" with the device's domains. Save needs a
// changed, valid list (also an empty one: detection off) and the typed device address.
func (p *settingsPane) openEnvironment() {
	cur := slices.Clone(p.d.src.val.Environment.Domains)
	f := &envForm{cur: cur, domains: slices.Clone(cur)}
	p.envForm = f
	p.dialog = showRedConfirm(p.window(), redSpec{
		op: amt.OpEnvironment, host: p.m.confirmHost(), device: p.m.confirmDevice(),
		title:       lang.L("Change environment detection"),
		warningBold: lang.L("Risk of lockout."),
		warningRest: lang.L("Outside these domains, AMT closes its network access. Only the BIOS (MEBx) at the device can fix a wrong entry."),
		extra:       f.build,
		ready:       f.ready,
		button:      lang.L("Save"),
		onConfirm: func(c amt.Confirmation) {
			domains := slices.Clone(f.domains)
			p.d.write(func() error { return p.m.sess.SetEnvironmentDetection(domains, c) }, true)
		},
	})
}

func (f *envForm) build(d *riskDialog) []fyne.CanvasObject {
	f.d = d
	f.list = container.New(layout.NewCustomPaddedVBoxLayout(0))
	f.listBox = widgets.NewPanel(style.ColorNameField, style.ColorNameCardBorder, 10, f.list, 1, 1, 1, 1)
	f.input = d.newEntry(false)
	f.input.PlaceHolder = lang.L("Add domain")
	f.input.OnChanged = func(string) { f.sync() }
	f.input.OnSubmitted = func(string) { f.addTyped() } // Enter adds, it never saves
	f.add = d.newButton(lang.L("Add"), f.addTyped)
	f.rebuild()
	head := designText(lang.L("Intranet domains"), 12, style.ColorNameControlText, true)
	return []fyne.CanvasObject{head, f.listBox, container.New(&rowLayout{gap: 8, fill: 0}, framedEntry(f.input), f.add)}
}

// ready: the list differs from the device's and is valid.
func (f *envForm) ready() bool {
	cur := amt.EnvironmentDetection{Available: true, Domains: f.cur}
	return amt.EnvironmentRisky(cur, f.domains) != nil && amt.ValidateEnvironmentDomains(f.domains) == nil
}

// candidate returns the typed domain and whether Add takes it: not empty, not in the list (case
// does not matter), the list not full and valid with it.
func (f *envForm) candidate() (string, bool) {
	v := strings.TrimSpace(f.input.Text)
	if v == "" || len(f.domains) >= maxEnvDomains {
		return v, false
	}
	for _, d := range f.domains {
		if strings.EqualFold(d, v) {
			return v, false
		}
	}
	return v, amt.ValidateEnvironmentDomains(append(slices.Clone(f.domains), v)) == nil
}

// addTyped adds the typed domain if Add takes it; otherwise the text stays for correction.
func (f *envForm) addTyped() {
	v, ok := f.candidate()
	if !ok {
		return
	}
	f.domains = append(f.domains, v)
	f.input.SetText("")
	f.rebuild()
}

func (f *envForm) remove(domain string) {
	f.domains = slices.DeleteFunc(f.domains, func(d string) bool { return d == domain })
	f.rebuild()
}

// rebuild shows the list (none: no list box), updates Add and the main button and lets the dialog
// take its new height.
func (f *envForm) rebuild() {
	f.rows = nil
	var objs []fyne.CanvasObject
	for _, d := range f.domains {
		r := f.newRow(d)
		f.rows = append(f.rows, r)
		if len(objs) > 0 {
			objs = append(objs, hline(style.ColorNameCardBorder))
		}
		objs = append(objs, r.obj)
	}
	f.list.Objects = objs
	f.list.Refresh()
	setShown(f.listBox, len(f.domains) > 0)
	f.sync()
	f.d.Revalidate()
	if f.d.panel != nil && f.d.pop != nil {
		f.d.pop.Refresh()
	}
}

// sync enables Add for a domain it takes and the field while the list has room.
func (f *envForm) sync() {
	_, ok := f.candidate()
	setEnabled(f.add, ok)
	setEnabled(f.input, len(f.domains) < maxEnvDomains)
}

// newRow is a line of the list (HostEnv.dc.html .li): 13 px text, the remove button on the right,
// 8 x 10 px padding (12 px on the left).
func (f *envForm) newRow(domain string) *envRow {
	r := &envRow{domain: domain, remove: newEnvRemoveButton(func() { f.remove(domain) })}
	text := designText(domain, 13, theme.ColorNameForeground, false)
	r.obj = container.New(layout.NewCustomPaddedLayout(8, 8, 12, 10), container.New(&rowLayout{gap: 8, fill: 0}, text, r.remove))
	return r
}

// envRemoveButton is the "×" of a domain (HostEnv.dc.html .x): 24 x 24 with a cross in the
// secondary text color.
type envRemoveButton struct {
	widget.BaseWidget
	onTap func()
	icon  *widget.Icon
}

func newEnvRemoveButton(tapped func()) *envRemoveButton {
	b := &envRemoveButton{onTap: tapped, icon: widget.NewIcon(theme.NewColoredResource(theme.CancelIcon(), style.ColorNameTextSecondary))}
	b.ExtendBaseWidget(b)
	return b
}

func (b *envRemoveButton) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *envRemoveButton) CreateRenderer() fyne.WidgetRenderer {
	icon := container.New(&manageSizeLayout{w: 16, h: 16}, b.icon)
	return widget.NewSimpleRenderer(container.New(&manageSizeLayout{w: 24, h: 24}, container.NewCenter(icon)))
}
