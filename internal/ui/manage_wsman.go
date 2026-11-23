package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The WSMAN Browser page of device management (overall design 2026-11-23, issue #24): a window
// page of the Diagnostics group. The class list (AMT_, CIM_, IPS_, with a search) sits on the
// left; a click reads all instances of the class and shows the properties of one instance on the
// right. Read only. The sidebar never shows the page: its "More" menu opens the window.

const (
	wsmanListWidth   float32 = 270 // .clslist
	wsmanValueMaxLen         = 300 // runes of a value shown in the table; "Copy as XML" has all
)

func init() {
	registerManagePage(managePageDef{section: sectionDiagnostics, order: 10, icon: icons.Code, build: (*manager).wsmanPane,
		names: func() (string, string) { return lang.L("WSMAN Browser"), lang.L("WSMAN Browser") }})
}

// wsmanItem is a row of the class list: a group heading ("AMT · 64") or a class.
type wsmanItem struct {
	header bool
	text   string
}

// wsmanPane is the WSMAN Browser page.
type wsmanPane struct {
	m *manager

	filter   string
	items    []wsmanItem
	selected string // class shown on the right; "" = none chosen yet
	selID    int    // its row in items, -1 if the filter hides it
	applying bool   // the pane selects a row itself: OnSelected does nothing

	gen     int // read counter: a late result of an older read is dropped
	loading bool
	res     amt.WSManResult
	err     error
	read    bool // res or err belongs to selected
	inst    int  // instance shown

	search   *widgets.FilterField
	list     *widget.List
	count    *canvas.Text // the page head: "· 258 classes"
	copyBtn  *widgets.PrimaryButton
	refresh  *widgets.PrimaryButton
	name     *canvas.Text // class name above the table
	number   *canvas.Text // "· 1 instance"
	note     *canvas.Text // "Truncated …", "Reading…"
	chips    []*widgets.Chip
	chipRow  *fyne.Container
	chipBox  *container.Scroll
	table    *listTable
	card     *fyne.Container // the message instead of the table (error, none chosen, no instances)
	cardText [2]string       // its title and text (tests)
	rightBox *fyne.Container
	scroll   *container.Scroll
}

func (m *manager) wsmanPane(bool) *managePane {
	p := &wsmanPane{m: m, selID: -1}

	p.search = widgets.NewFilterField(func(s string) { p.setFilter(s) })
	p.search.Entry.SetPlaceHolder(lang.L("Find class"))
	p.list = widget.NewList(
		func() int { return len(p.items) },
		func() fyne.CanvasObject {
			return container.New(layout.NewCustomPaddedLayout(0, 0, 8, 8), designText("", 12, theme.ColorNameForeground, false))
		},
		p.updateRow)
	p.list.OnSelected = p.rowSelected
	left := container.New(&manageSizeLayout{w: wsmanListWidth}, container.New(&manageHSplit{fill: 0},
		container.NewBorder(container.New(layout.NewCustomPaddedLayout(10, 6, 10, 10), p.search), nil, nil, nil,
			container.New(layout.NewCustomPaddedLayout(0, 0, 4, 4), p.list)),
		hlineV()))

	p.name = designText("", 13, theme.ColorNameForeground, true)
	p.name.TextStyle.Monospace = true
	p.number = designText("", 13, style.ColorNameTextSecondary, false)
	p.note = designText("", 12, style.ColorNameTextSecondary, false)
	p.chipRow = container.New(layout.NewCustomPaddedHBoxLayout(6))
	p.chipBox = container.NewHScroll(p.chipRow)
	p.table = newListTable(false, []listColumn{{title: lang.L("Property"), width: 240}, {title: lang.L("Value")}}, nil)
	p.card = container.NewStack()
	head := container.New(layout.NewCustomPaddedHBoxLayout(8), p.name, p.number)
	p.rightBox = container.New(layout.NewCustomPaddedVBoxLayout(10), head, p.note, p.chipBox, p.table, p.card)
	p.scroll = container.NewVScroll(container.New(layout.NewCustomPaddedLayout(16, 16, 20, 20), p.rightBox))
	p.table.onHeight = func() { p.scroll.Refresh() }

	p.copyBtn = newManageButton(lang.L("Copy as XML"), p.copyXML)
	p.refresh = newManageButton(lang.L("Refresh"), p.reload)
	p.count = designText(lang.L("· {{.Count}} classes", map[string]any{"Count": len(amt.WSManClasses())}), 14, style.ColorNameTextSecondary, false)

	pane := &managePane{
		body:  container.New(&manageHSplit{fill: 1}, left, p.scroll),
		flush: true,
		badge: wsmanPill(lang.L("Read only")),
		info:  p.count,
		tools: container.New(layout.NewCustomPaddedHBoxLayout(12), p.copyBtn, p.refresh),
		impl:  p,
	}
	pane.close = func() { p.gen++ } // a read that ends later has nowhere to go
	p.rebuild()
	p.render()
	return pane
}

// wsmanPill is the gray pill of the page head (design .pill).
func wsmanPill(text string) fyne.CanvasObject {
	return widgets.NewPanel(style.ColorNamePill, style.ColorNamePillBorder, 9,
		designText(text, 11, style.ColorNameTextSecondary, false), 1, 7, 1, 7)
}

// hlineV is the 1 px line at the right edge of the class list.
func hlineV() fyne.CanvasObject { return hline(style.ColorNameSeparatorDark) }

// rebuild lists the classes that match the filter, grouped by prefix with their count.
func (p *wsmanPane) rebuild() {
	p.items = p.items[:0]
	q := strings.ToLower(strings.TrimSpace(p.filter))
	var groups [3][]string
	prefixes := []string{"AMT", "CIM", "IPS"}
	for _, c := range amt.WSManClasses() {
		if q != "" && !strings.Contains(strings.ToLower(c), q) {
			continue
		}
		for i, pre := range prefixes {
			if strings.HasPrefix(c, pre+"_") {
				groups[i] = append(groups[i], c)
			}
		}
	}
	p.selID = -1
	for i, g := range groups {
		if len(g) == 0 {
			continue
		}
		p.items = append(p.items, wsmanItem{header: true, text: fmt.Sprintf("%s · %d", prefixes[i], len(g))})
		for _, c := range g {
			if c == p.selected {
				p.selID = len(p.items)
			}
			p.items = append(p.items, wsmanItem{text: c})
		}
	}
	for id, it := range p.items {
		h := float32(26)
		if it.header {
			h = 32
		}
		p.list.SetItemHeight(id, h)
	}
	p.list.Refresh()
	p.applying = true
	if p.selID >= 0 {
		p.list.Select(p.selID)
	} else {
		p.list.UnselectAll()
	}
	p.applying = false
}

func (p *wsmanPane) setFilter(s string) {
	p.filter = s
	p.rebuild()
}

// updateRow draws one row: a heading, or a class in monospace (white on the selected row).
func (p *wsmanPane) updateRow(id widget.ListItemID, o fyne.CanvasObject) {
	if id < 0 || id >= len(p.items) {
		return
	}
	t := o.(*fyne.Container).Objects[0].(*canvas.Text)
	it := p.items[id]
	t.Text = it.text
	switch {
	case it.header:
		t.TextStyle, t.TextSize, t.Color = fyne.TextStyle{Bold: true}, 11, style.Color(style.ColorNameTextSecondary)
	case id == p.selID:
		t.TextStyle, t.TextSize, t.Color = fyne.TextStyle{Monospace: true}, 12, style.Color(style.ColorNameWhite)
	default:
		t.TextStyle, t.TextSize, t.Color = fyne.TextStyle{Monospace: true}, 12, style.Color(theme.ColorNameForeground)
	}
	t.Refresh()
}

func (p *wsmanPane) rowSelected(id widget.ListItemID) {
	if p.applying || id < 0 || id >= len(p.items) {
		return
	}
	if p.items[id].header {
		p.list.Unselect(id)
		if p.selID >= 0 {
			p.applying = true
			p.list.Select(p.selID)
			p.applying = false
		}
		return
	}
	p.selID = id
	p.list.Refresh()
	p.selected, p.inst = p.items[id].text, 0
	p.reload()
}

// reload reads the chosen class again.
func (p *wsmanPane) reload() {
	if p.selected == "" || p.m.closed {
		return
	}
	p.gen++
	g, class := p.gen, p.selected
	p.loading, p.read = true, false
	p.render()
	go func() {
		res, err := p.m.sess.BrowseWSMan(class)
		p.m.later(func() {
			if p.m.closed || g != p.gen {
				return
			}
			p.loading, p.read, p.res, p.err = false, true, res, err
			p.inst = 0
			p.render()
		})
	}()
}

// copyXML puts the XML of the instances on the clipboard.
func (p *wsmanPane) copyXML() {
	if p.read && p.err == nil {
		p.m.app.Clipboard().SetContent(p.res.XML())
	}
}

// instanceText is "1 instance" or "N instances".
func instanceText(n int) string {
	if n == 1 {
		return lang.L("1 instance")
	}
	return lang.L("{{.Count}} instances", map[string]any{"Count": n})
}

// render shows the state: nothing chosen, reading, an error, or the instances.
func (p *wsmanPane) render() {
	ok := p.read && p.err == nil
	setShown(p.copyBtn, ok && len(p.res.Instances) > 0)
	if p.loading || p.selected == "" {
		p.refresh.Disable()
	} else {
		p.refresh.Enable()
	}
	p.name.Text, p.number.Text, p.note.Text = p.selected, "", ""
	setShown(p.name, p.selected != "")
	p.chipRow.Objects = nil
	setShown(p.chipBox, false)
	setShown(p.table, false)
	p.card.Objects = nil
	p.cardText = [2]string{}

	switch {
	case p.selected == "":
		p.setCard(lang.L("Choose a class"), lang.L("Choose a class on the left to read its instances from {{.Name}}.", map[string]any{"Name": p.m.name}))
	case p.loading:
		p.note.Text = lang.L("Reading…")
	case p.err != nil:
		p.setCard(p.errorText(p.err))
	case len(p.res.Instances) == 0:
		p.number.Text = "· " + instanceText(0)
		p.setCard(lang.L("No instances"), lang.L("{{.Class}} exists on {{.Name}} but has no instances.", map[string]any{"Class": p.selected, "Name": p.m.name}))
	default:
		p.number.Text = "· " + instanceText(len(p.res.Instances))
		if p.res.Truncated {
			p.note.Text = lang.L("Truncated: this class has more data than OpenLAMT reads at once.")
		}
		p.showInstance()
	}
	p.name.Refresh()
	p.number.Refresh()
	p.note.Refresh()
	setShown(p.note, p.note.Text != "")
	p.rightBox.Refresh()
	p.scroll.Refresh()
}

// showInstance fills the instance chips and the table.
func (p *wsmanPane) showInstance() {
	if n := len(p.res.Instances); n > 1 {
		p.chips = p.chips[:0]
		for i := range n {
			c := widgets.NewChip(lang.L("Instance {{.Number}}", map[string]any{"Number": i + 1}), func() { p.inst = i; p.showInstance(); p.rightBox.Refresh(); p.scroll.Refresh() })
			c.SetActive(i == p.inst)
			p.chips = append(p.chips, c)
			p.chipRow.Objects = append(p.chipRow.Objects, c)
		}
		setShown(p.chipBox, true)
		p.chipRow.Refresh()
		p.chipBox.Refresh()
	}
	in := p.res.Instances[p.inst]
	if in.Class != p.selected {
		p.note.Text = joinDot(p.note.Text, in.Class)
		p.note.Refresh()
		setShown(p.note, true)
	}
	rows := make([]listRow, len(in.Props))
	for i, pr := range in.Props {
		rows[i] = listRow{cells: [][]widget.RichTextSegment{
			{wsmanSeg(pr.Name, theme.ColorNameForeground)},
			{wsmanSeg(wsmanValue(p.selected, pr), theme.ColorNameForeground)},
		}}
	}
	p.table.setRows(rows)
	setShown(p.table, true)
}

// wsmanSeg is a monospace table cell (design .mt .mono).
func wsmanSeg(text string, c fyne.ThemeColorName) *widget.TextSegment {
	return &widget.TextSegment{Text: text, Style: widget.RichTextStyle{Inline: true, ColorName: c,
		SizeName: style.SizeNameSmallText, TextStyle: fyne.TextStyle{Monospace: true}}}
}

// wsmanValue is the text of a value: a long one is cut, a known number gets its meaning
// ("1 (Enabled)").
func wsmanValue(class string, pr amt.WSManProp) string {
	v := pr.Value
	if r := []rune(v); len(r) > wsmanValueMaxLen {
		v = string(r[:wsmanValueMaxLen]) + "…"
	}
	if m := amt.WSManMeaning(class, pr.Name, pr.Value); m != "" {
		return v + " (" + m + ")"
	}
	return v
}

// setCard shows a message card (title and text) in place of the table.
func (p *wsmanPane) setCard(title, text string) {
	p.cardText = [2]string{title, text}
	p.card.Objects = []fyne.CanvasObject{newEmptyCard(title, text)}
	p.card.Refresh()
}

// errorText is the card of a failed read: the device does not have the class, the account
// lacks the permission, no answer in time, or anything else.
func (p *wsmanPane) errorText(err error) (title, text string) {
	var cu *amt.ClassUnsupportedError
	var ne net.Error
	switch {
	case errors.As(err, &cu):
		return lang.L("{{.Name}} does not support {{.Class}}", map[string]any{"Name": p.m.name, "Class": p.selected}),
			lang.L("The device answered: {{.Fault}}. Choose another class.", map[string]any{"Fault": cu.Fault})
	case errors.Is(err, amt.ErrAccessDenied):
		return lang.L("The account {{.User}} cannot read this class.", map[string]any{"User": p.m.sess.Config().Username}),
			lang.L("The device refused the request. Choose another class.")
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return lang.L("{{.Name}} did not answer in time", map[string]any{"Name": p.m.name}),
			lang.L("Reading {{.Class}} took too long. Try Refresh.", map[string]any{"Class": p.selected})
	}
	return lang.L("{{.Class}} could not be read", map[string]any{"Class": p.selected}), describeManage(err)
}
