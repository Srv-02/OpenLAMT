package ui

import (
	"image/color"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The list page helper: a page that shows one table (Users.dc.html pattern) with an optional
// empty-state card, a footer line and a Refresh button. The Users page is built on it.

// listColumn is one text column of a listTable.
type listColumn struct {
	title        string
	width        float32 // window: fixed width; 0 = takes the width left over (min 48)
	compactWidth float32 // sidebar: least width
	compactGrow  float32 // sidebar: grows up to this so the longest word does not break; 0 = fixed
}

// listAction is the button column at the right end of each row (Users: "Edit").
type listAction struct {
	width, compactWidth float32
}

// listRow is one line: one cell per column (rich-text segments) and the row button (nil = none).
type listRow struct {
	cells  [][]widget.RichTextSegment
	action *widgets.PrimaryButton
}

// listLine is a laid-out row.
type listLine struct {
	cells  []*widget.RichText
	action *widgets.PrimaryButton
}

// listTable is the table of a list page (Users.dc.html): a 1 px frame with radius 10, a gray
// header, hairlines between the rows. Columns have fixed widths, one takes the rest, the
// action button sits at the right; long texts wrap, so a row grows. The table reports a new
// height through onHeight after its width changed the wrapping.
type listTable struct {
	widget.BaseWidget
	compact  bool
	cols     []listColumn
	act      *listAction // nil = none
	lines    []*listLine
	words    []float32 // longest word of each column (header included)
	onHeight func()

	width    float32 // width of the last layout; 0 before the first one
	reported float32 // height MinSize returned last
}

func newListTable(compact bool, cols []listColumn, act *listAction) *listTable {
	t := &listTable{compact: compact, cols: cols, act: act}
	t.ExtendBaseWidget(t)
	return t
}

// plainText joins the text of the segments.
func plainText(segs []widget.RichTextSegment) string {
	var b strings.Builder
	for _, s := range segs {
		if ts, ok := s.(*widget.TextSegment); ok {
			b.WriteString(ts.Text)
		}
	}
	return b.String()
}

// setRows replaces the rows.
func (t *listTable) setRows(rows []listRow) {
	size := theme.SizeNameText
	if t.compact {
		size = style.SizeNameSmallText
	}
	ts := style.Size(size)
	t.lines = nil
	t.words = make([]float32, len(t.cols))
	for i, c := range t.cols {
		t.words[i] = longestWord(12, true, c.title)
	}
	for _, r := range rows {
		for i := range t.cols {
			if i < len(r.cells) {
				t.words[i] = max(t.words[i], longestWord(ts, false, plainText(r.cells[i])))
			}
		}
	}
	for _, r := range rows {
		l := &listLine{action: r.action}
		for i := range t.cols {
			var segs []widget.RichTextSegment
			if i < len(r.cells) {
				segs = r.cells[i]
			}
			cell := widget.NewRichText(segs...)
			cell.Wrapping = fyne.TextWrapWord
			l.cells = append(l.cells, cell)
		}
		t.lines = append(t.lines, l)
	}
	t.Refresh()
}

// Geometry (Users.dc.html .th/.td): header 8 x 12, cells 9 x 12, 13 px text; in the sidebar
// 7 x 6 and 12 px. Fixed columns have their width; in the sidebar they have at least their
// compact width and grow up to compactGrow so that their longest word does not break (a long
// word still does).
func (t *listTable) pads() (padV, padH, headV float32) {
	if t.compact {
		return 7, 6, 7
	}
	return 9, 12, 8
}

// actionWidth is the width of the action column.
func (t *listTable) actionWidth() float32 {
	switch {
	case t.act == nil:
		return 0
	case t.compact:
		return t.act.compactWidth
	}
	return t.act.width
}

// fill reports whether column i takes the width left over.
func (t *listTable) fill(i int) bool { return t.cols[i].width == 0 }

// columns returns the widths of the columns and, last, of the action for an inner width.
func (t *listTable) columns(inner float32) []float32 {
	out := make([]float32, len(t.cols)+1)
	_, padH, _ := t.pads()
	rest, fills := inner, 0
	for i, c := range t.cols {
		switch {
		case t.fill(i):
			fills++
			continue
		case t.compact:
			out[i] = c.compactWidth
			if c.compactGrow > 0 && i < len(t.words) {
				out[i] = min(c.compactGrow, max(c.compactWidth, t.words[i]+2*padH+1))
			}
		default:
			out[i] = c.width
		}
		rest -= out[i]
	}
	out[len(t.cols)] = t.actionWidth()
	rest -= out[len(t.cols)]
	for i := range t.cols {
		if t.fill(i) {
			out[i] = max(48, rest/float32(fills))
		}
	}
	return out
}

// minWidth is the width of the fixed columns, the action and 48 for a fill column.
func (t *listTable) minWidth() float32 {
	var w float32
	fills := false
	for i, c := range t.cols {
		switch {
		case t.fill(i):
			fills = true
		case t.compact:
			w += c.compactWidth
		default:
			w += c.width
		}
	}
	w += t.actionWidth() + 2
	if fills {
		w += 48
	}
	return w
}

// longestWord is the width of the longest word of texts at size.
func longestWord(size float32, bold bool, texts ...string) float32 {
	var w float32
	for _, s := range texts {
		for _, word := range strings.Fields(s) {
			w = max(w, fyne.MeasureText(word, size, fyne.TextStyle{Bold: bold}).Width)
		}
	}
	return w
}

func (t *listTable) headHeight() float32 {
	_, _, headV := t.pads()
	return fyne.MeasureText("Mg", 12, fyne.TextStyle{Bold: true}).Height + 2*headV
}

// rowHeight wraps the cells of l at the column widths and returns the row height.
func (t *listTable) rowHeight(l *listLine, cols []float32) float32 {
	padV, padH, _ := t.pads()
	var h float32
	if l.action != nil {
		h = l.action.MinSize().Height
	}
	for i, c := range l.cells {
		ip := c.Theme().Size(theme.SizeNameInnerPadding)
		w := max(0, cols[i]-2*padH) + 2*ip
		for range 2 {
			c.Resize(fyne.NewSize(w, c.MinSize().Height))
		}
		h = max(h, c.MinSize().Height-2*ip)
	}
	return h + 2*padV
}

// height is the full height of the table at width.
func (t *listTable) height(width float32) float32 {
	cols := t.columns(width - 2)
	h := 1 + t.headHeight() + 1 + 1 // frame, header, line under it, frame
	for i, l := range t.lines {
		if i > 0 {
			h++
		}
		h += t.rowHeight(l, cols)
	}
	return h
}

// defaultWidth is assumed before the first layout: the sidebar's content width, or a window.
func (t *listTable) defaultWidth() float32 {
	if t.compact {
		return manageSidebarWidth - 2*16
	}
	return 800
}

func (t *listTable) CreateRenderer() fyne.WidgetRenderer {
	r := &listTableRenderer{t: t,
		bg:       style.BindRect(&canvas.Rectangle{CornerRadius: 10}, "background", ""),
		outline:  style.BindRect(&canvas.Rectangle{CornerRadius: 10, StrokeWidth: 1, FillColor: color.Transparent}, "", style.ColorNameCardBorder),
		headBg:   style.BindRect(&canvas.Rectangle{TopLeftCornerRadius: 9, TopRightCornerRadius: 9}, style.ColorNameTableHeader, ""),
		headLine: style.BindRect(canvas.NewRectangle(nil), style.ColorNameCardBorder, ""),
	}
	for _, c := range t.cols {
		r.names = append(r.names, c.title)
		r.head = append(r.head, designText(c.title, 12, style.ColorNameTextSecondary, true))
	}
	r.Refresh()
	return r
}

type listTableRenderer struct {
	t                   *listTable
	bg, outline, headBg *canvas.Rectangle
	headLine            *canvas.Rectangle
	names               []string
	head                []*canvas.Text
	seps                []*canvas.Rectangle
	objects             []fyne.CanvasObject
	inHeightCall        bool // onHeight runs; a layout it causes does not report again
}

func (r *listTableRenderer) MinSize() fyne.Size {
	t := r.t
	w := t.width
	if w <= 0 {
		w = t.defaultWidth()
	}
	t.reported = t.height(w)
	return fyne.NewSize(t.minWidth(), t.reported)
}

func (r *listTableRenderer) Layout(size fyne.Size) {
	t := r.t
	t.width = size.Width
	r.bg.Resize(size)
	r.outline.Resize(size)
	_, padH, headV := t.pads()
	inner := size.Width - 2
	cols := t.columns(inner)
	headH := t.headHeight()
	r.headBg.Move(fyne.NewPos(1, 1))
	r.headBg.Resize(fyne.NewSize(inner, headH))
	x := float32(1)
	for i, h := range r.head {
		h.Text = ellipsize(r.names[i], 12, h.TextStyle, max(0, cols[i]-2*padH))
		h.Move(fyne.NewPos(x+padH, 1+headV))
		h.Resize(h.MinSize())
		x += cols[i]
	}
	r.headLine.Move(fyne.NewPos(1, 1+headH))
	r.headLine.Resize(fyne.NewSize(inner, 1))
	y := 1 + headH + 1
	for i, l := range t.lines {
		if i > 0 {
			s := r.seps[i-1]
			s.Move(fyne.NewPos(1, y))
			s.Resize(fyne.NewSize(inner, 1))
			y++
		}
		rh := t.rowHeight(l, cols)
		x := float32(1)
		for c, cell := range l.cells {
			ip := cell.Theme().Size(theme.SizeNameInnerPadding)
			ms := cell.MinSize()
			cell.Move(fyne.NewPos(x+padH-ip, y+(rh-ms.Height)/2))
			cell.Resize(fyne.NewSize(max(0, cols[c]-2*padH)+2*ip, ms.Height))
			x += cols[c]
		}
		if l.action != nil {
			bs := l.action.MinSize()
			l.action.Move(fyne.NewPos(x+cols[len(t.cols)]-padH-bs.Width, y+(rh-bs.Height)/2))
			l.action.Resize(bs)
		}
		y += rh
	}
	total := y + 1
	if total != t.reported && !r.inHeightCall {
		t.reported = total
		if t.onHeight != nil {
			r.inHeightCall = true
			t.onHeight()
			r.inHeightCall = false
		}
	}
}

func (r *listTableRenderer) Refresh() {
	t := r.t
	for len(r.seps) < max(0, len(t.lines)-1) {
		r.seps = append(r.seps, style.BindRect(canvas.NewRectangle(nil), style.ColorNameCardSeparator, ""))
	}
	r.objects = []fyne.CanvasObject{r.bg, r.headBg, r.headLine}
	for _, h := range r.head {
		r.objects = append(r.objects, h)
	}
	for i, l := range t.lines {
		if i > 0 {
			r.objects = append(r.objects, r.seps[i-1])
		}
		for _, c := range l.cells {
			r.objects = append(r.objects, c)
		}
		if l.action != nil {
			r.objects = append(r.objects, l.action)
		}
	}
	r.objects = append(r.objects, r.outline)
	r.Layout(t.Size())
	canvas.Refresh(t)
}

func (r *listTableRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *listTableRenderer) Destroy()                     {}

// listSpec describes a list page.
type listSpec struct {
	columns []listColumn
	action  *listAction           // nil = no action column
	tools   []fyne.CanvasObject   // page head (window) / right-aligned row above the table (sidebar)
	refresh func()                // non-nil: a "Refresh" button after the tools
	src     interface{ reload() } // read again after a write; nil = none

	emptyTitle, emptyText string            // a title shows the empty-state card instead of an empty table
	note                  fyne.CanvasObject // under the table, its visibility is up to the caller
	footer                bool              // a 12 px secondary text line under the table (setFooter)
}

// listPage is the frame of a page that shows one table.
type listPage struct {
	m       *manager
	compact bool
	src     interface{ reload() }
	pane    *managePane
	err     *manageError
	table   *listTable
	empty   fyne.CanvasObject      // nil = none
	footer  *canvas.Text           // nil = none
	refresh *widgets.PrimaryButton // nil = none
	tools   []fyne.CanvasObject
}

// newListPage builds the page. The caller sets pane.shown, pane.close and pane.impl and feeds
// the page through update.
func (m *manager) newListPage(compact bool, s listSpec) *listPage {
	p := &listPage{m: m, compact: compact, src: s.src, err: newManageError(),
		table: newListTable(compact, s.columns, s.action), tools: slices.Clone(s.tools)}
	if s.refresh != nil {
		p.refresh = newManageButton(lang.L("Refresh"), s.refresh)
		p.tools = append(p.tools, p.refresh)
	}
	objs := []fyne.CanvasObject{p.err.box, p.table}
	if s.emptyTitle != "" {
		p.empty = newEmptyCard(s.emptyTitle, s.emptyText)
		p.empty.Hide()
		objs = append(objs, p.empty)
	}
	if s.note != nil {
		objs = append(objs, s.note)
	}
	if s.footer {
		p.footer = designText("", 12, style.ColorNameTextSecondary, false)
		p.footer.Hide()
		objs = append(objs, container.New(layout.NewCustomPaddedLayout(0, 0, 2, 2), p.footer))
	}
	if compact && len(p.tools) > 0 {
		objs = append([]fyne.CanvasObject{container.New(&rowLayout{gap: 8, fill: 0}, append([]fyne.CanvasObject{spacer()}, p.tools...)...)}, objs...)
	}
	p.pane = &managePane{body: container.New(layout.NewCustomPaddedVBoxLayout(12), objs...), scroll: true}
	if !compact {
		switch len(p.tools) {
		case 0:
		case 1:
			p.pane.tools = p.tools[0]
		default:
			p.pane.tools = container.New(layout.NewCustomPaddedHBoxLayout(12), p.tools...)
		}
	}
	p.table.onHeight = p.pane.changed
	return p
}

// newEmptyCard is the empty state of a list (design: .card .empty): a centered bold title and
// a wrapped, secondary text.
func newEmptyCard(title, text string) fyne.CanvasObject {
	t := designText(title, 14, theme.ColorNameForeground, true)
	t.Alignment = fyne.TextAlignCenter
	body := widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{ColorName: style.ColorNameTextSecondary,
		SizeName: style.SizeNameSmallText, Alignment: fyne.TextAlignCenter}})
	body.Wrapping = fyne.TextWrapWord
	col := container.New(layout.NewCustomPaddedVBoxLayout(4), t, body)
	return widgets.NewPanel(style.ColorNameCard, style.ColorNameCardBorder, 10, col, 22, 16, 22, 16)
}

// update shows the rows (loaded: a read succeeded once), the error and the busy state.
func (p *listPage) update(rows []listRow, loaded, loading bool, err error) {
	p.table.setRows(rows)
	if p.empty != nil {
		none := loaded && len(rows) == 0
		setShown(p.empty, none)
		setShown(p.table, !none)
	}
	p.err.set(err)
	if p.refresh != nil {
		if loading {
			p.refresh.Disable()
		} else {
			p.refresh.Enable()
		}
	}
	p.pane.changed()
}

// setFooter shows text under the table; "" hides the line.
func (p *listPage) setFooter(text string) {
	if p.footer == nil {
		return
	}
	p.footer.Text = text
	p.footer.Refresh()
	setShown(p.footer, text != "")
	p.pane.changed()
}

// window is the window the dialogs of this view open on: the device window for the sidebar, the
// management window for its own view.
func (p *listPage) window() fyne.Window {
	if !p.compact && p.m.win != nil {
		return p.m.win.win
	}
	return p.m.parent
}

// write runs write off the Fyne thread, reads the source again and shows an error in a dialog.
func (p *listPage) write(write func() error) {
	manageWrite(p.m, p.src, write, func(err error) {
		if err != nil {
			showError(p.window(), err)
		}
	}, nil)
}

// manageWrite runs write off the Fyne thread; afterwards, on the Fyne thread, it reads src again
// (nil = none) and calls done. If the manager closed meanwhile, late runs instead (nil drops the
// result).
func manageWrite[R any](m *manager, src interface{ reload() }, write func() R, done, late func(R)) {
	go func() {
		r := write()
		m.later(func() {
			if m.closed {
				if late != nil {
					late(r)
				}
				return
			}
			if src != nil {
				src.reload()
			}
			if done != nil {
				done(r)
			}
		})
	}()
}
