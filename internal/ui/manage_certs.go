package ui

import (
	"slices"
	"strconv"
	"time"

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

// The Certificates page of device management (issue #16, gesamtdesign "Verwaltung:
// Certificates"): one table of every certificate in the AMT store (Name | Type | Valid until |
// Used for), a row opens the details; below it the key pairs with "Delete…". "Import…" and
// "Refresh" sit in the page head. The dialogs are in cert_dialogs.go. Choosing the TLS
// certificate stays in Settings › Clock and TLS.

func init() {
	registerManagePage(managePageDef{section: sectionMore, order: 40, icon: icons.Certificate, build: (*manager).certsPane,
		// the sidebar tab is short: the 400 px tab row is full in German already
		names: func() (string, string) { return lang.L("Certs"), lang.L("Certificates") }})
}

// certsPane is the state of one view of the Certificates page.
type certsPane struct {
	m        *manager
	compact  bool
	list     *listPage
	keys     *listTable
	keysHead fyne.CanvasObject
	footnote *widget.RichText // "Click a certificate for details. …" (wraps in the sidebar)
	count    *canvas.Text     // window: "· 4 certificates"
	importB  *widgets.PrimaryButton
	tip      *widget.PopUp // the open tooltip of a disabled "Delete…"
	tipText  string        // its text (tests)

	// the dialogs last opened from this view (tests)
	details   *certDetails
	importDlg *certImport
	confirm   *panel      // the simple confirmation (certificate or key)
	rootDlg   *riskDialog // the orange one (trusted root)
	inUse     *panel      // "Certificate in use"

	// openFile and saveFile are the file dialogs; tests replace them. chosen runs on the Fyne
	// thread with the path.
	openFile func(win fyne.Window, exts []string, chosen func(path string))
	saveFile func(win fyne.Window, name string, chosen func(path string))
}

// certColumns are Name (the rest), Type, Valid until and Used for; the chevron is the action column.
func certColumns() []listColumn {
	return []listColumn{
		{title: lang.L("Name")},
		{title: lang.L("Type"), width: 190, compactWidth: 72, compactGrow: 96},
		{title: lang.L("Valid until"), width: 150, compactWidth: 74, compactGrow: 116},
		{title: lang.L("Used for"), width: 120, compactWidth: 64, compactGrow: 64},
	}
}

// keyColumns are Key (the rest) and Certificate; "Delete…" is the action column.
func keyColumns() []listColumn {
	return []listColumn{
		{title: lang.L("Key")},
		{title: lang.L("Certificate"), width: 220, compactWidth: 96, compactGrow: 120},
	}
}

// certsPane builds the Certificates page. The store (m.certs) is shared by both views and read
// when the page is first shown; every write reads it again.
func (m *manager) certsPane(compact bool) *managePane {
	if m.certs == nil {
		m.certs = newManageSource(m, m.sess.CertificateStore)
	}
	p := &certsPane{m: m, compact: compact, openFile: openCertFile, saveFile: saveCertFile}
	p.importB = newManageButton(lang.L("Import…"), p.openImport)
	p.keys = newListTable(compact, keyColumns(), &listAction{width: 100, compactWidth: 76})
	p.keysHead = container.New(layout.NewCustomPaddedLayout(6, -4, 2, 2), designText(lang.L("Key pairs"), 12, style.ColorNameTextSecondary, true))
	p.footnote = widget.NewRichText(cellSeg(lang.L("Click a certificate for details. Choose the TLS certificate in Settings › Clock and TLS."),
		style.ColorNameTextSecondary, style.SizeNameSmallText, false))
	p.footnote.Wrapping = fyne.TextWrapWord
	ip := theme.InnerPadding()
	p.list = m.newListPage(compact, listSpec{columns: certColumns(), action: &listAction{width: 36, compactWidth: 24},
		tools: []fyne.CanvasObject{p.importB}, refresh: m.certs.reload, src: m.certs,
		note: container.New(layout.NewCustomPaddedVBoxLayout(12), p.keysHead, p.keys,
			container.New(layout.NewCustomPaddedLayout(-4-ip, 0, 2-ip, 0), p.footnote))})
	p.keys.onHeight = p.list.pane.changed
	pane := p.list.pane
	if !compact {
		p.count = designText("", 14, style.ColorNameTextSecondary, false)
		pane.info = p.count
	}
	pane.shown, pane.impl = m.certs.ensure, p
	pane.close = m.certs.subscribe(p.render)
	p.render()
	return pane
}

// render shows the store, the count, the error and the busy state.
func (p *certsPane) render() {
	src := p.m.certs
	var st amt.CertificateStore
	if src.loaded() {
		st = src.val
	}
	rows := make([]listRow, len(st.Certificates))
	for i, c := range st.Certificates {
		rows[i] = p.certRow(c)
	}
	p.list.update(rows, src.loaded(), src.loading, src.err)
	keys := make([]listRow, len(st.Keys))
	for i, k := range st.Keys {
		keys[i] = p.keyRow(k, st.Certificates)
	}
	p.keys.setRows(keys)
	setShown(p.keysHead, src.loaded())
	setShown(p.keys, src.loaded())
	if p.count != nil {
		p.count.Text = ""
		if src.loaded() {
			p.count.Text = "· " + certificatesText(len(st.Certificates))
		}
		p.count.Refresh()
	}
	p.list.pane.changed()
}

// certificatesText is "4 certificates".
func certificatesText(n int) string {
	if n == 1 {
		return lang.L("1 certificate")
	}
	return lang.L("{{.Count}} certificates", map[string]any{"Count": n})
}

// certName is the name of a certificate in the table and the dialogs: the CN of its subject,
// the subject, or the short fingerprint.
func certName(c amt.Certificate) string {
	if n := commonName(c.Subject); n != "" {
		return n
	}
	return shortFingerprint(c.Fingerprint)
}

// certType is the Type column.
func certType(c amt.Certificate) string {
	switch {
	case c.TrustedRoot:
		return lang.L("Trusted root")
	case c.HasPrivateKey:
		return lang.L("Certificate with key")
	}
	return lang.L("Certificate")
}

// certDate is a date of the table and the details, e.g. "Nov 10, 2036".
func certDate(t time.Time) string { return t.UTC().Format("Jan 02, 2006") }

// expired reports whether c is no longer valid at now.
func expired(c amt.Certificate, now time.Time) bool {
	return !c.NotAfter.IsZero() && now.After(c.NotAfter)
}

// keyText is the Key column: "RSA 2048 · 4F:2A:…:9C".
func keyText(k amt.KeyPair) string {
	fp := shortFingerprint(k.Fingerprint)
	if k.Algorithm == "" {
		return fp
	}
	if k.Bits > 0 {
		return k.Algorithm + " " + strconv.Itoa(k.Bits) + " · " + fp
	}
	return k.Algorithm + " · " + fp
}

// size is the text size of the cells.
func (p *certsPane) size() fyne.ThemeSizeName {
	if p.compact {
		return style.SizeNameSmallText
	}
	return theme.SizeNameText
}

// certRow is the row of c: name, type, the date (a red pill once expired), the "TLS" pill and
// the chevron; a tap opens the details.
func (p *certsPane) certRow(c amt.Certificate) listRow {
	s := p.size()
	cells := [][]widget.RichTextSegment{
		{cellSeg(certName(c), theme.ColorNameForeground, s, false)},
		{cellSeg(certType(c), theme.ColorNameForeground, s, false)},
		nil, nil,
	}
	objects := make([]fyne.CanvasObject, 4)
	switch {
	case expired(c, p.m.now()):
		objects[2] = newPill(lang.L("Expired {{.Date}}", map[string]any{"Date": c.NotAfter.UTC().Format("Jan 2006")}), pillBad)
	case !c.NotAfter.IsZero():
		cells[2] = []widget.RichTextSegment{cellSeg(certDate(c.NotAfter), theme.ColorNameForeground, s, false)}
	}
	if c.TLS {
		objects[3] = newPill(lang.L("TLS"), pillOK)
	}
	chevron := widgets.NewSymbol(icons.GetStroke(icons.ChevronRight, icons.Secondary, 1.7), 14)
	return listRow{cells: cells, objects: objects, trail: chevron, onTap: func() { p.openDetails(c) }}
}

// keyRow is the row of key pair k: key, its certificate ("none") and "Delete…", which is gray
// with a tooltip naming the certificate while the key has one.
func (p *certsPane) keyRow(k amt.KeyPair, certs []amt.Certificate) listRow {
	s := p.size()
	name := lang.L("none")
	if i := slices.IndexFunc(certs, func(c amt.Certificate) bool { return c.ID == k.Certificate }); k.Certificate != "" && i >= 0 {
		name = certName(certs[i])
	} else if k.Certificate != "" {
		name = shortFingerprint(k.Certificate)
	}
	key := cellSeg(keyText(k), theme.ColorNameForeground, s, false)
	key.Style.TextStyle.Monospace = true
	del := newSmallButton(p.compact, lang.L("Delete…"), func() { p.confirmDeleteKey(k) })
	if k.Certificate != "" {
		del.Disable()
		tip := lang.L("Used by “{{.Name}}”. Delete the certificate first.", map[string]any{"Name": name})
		del.OnHover = func(in bool) { p.hoverTip(del, tip, in) }
	}
	return listRow{cells: [][]widget.RichTextSegment{{key}, {cellSeg(name, theme.ColorNameForeground, s, false)}}, action: del}
}

// newSmallButton is the small bordered button of a table row (.b.out.sm): 26 px, radius 7.
func newSmallButton(compact bool, text string, tapped func()) *widgets.PrimaryButton {
	b := widgets.NewPrimaryButton(text, nil, tapped)
	b.Importance = widget.MediumImportance
	m := &widgets.ButtonMetrics{Height: 26, Radius: 7, PaddingH: 10, TextSize: 12.5, Regular: true,
		Neutral: style.ColorNameField, NeutralBorder: style.ColorNameControlBorder}
	if compact {
		m.Height, m.PaddingH, m.TextSize = 24, 8, 12
	}
	b.Metrics = m
	return b
}

// hoverTip shows text below b while the pointer is on it (in) and hides it afterwards.
func (p *certsPane) hoverTip(b fyne.CanvasObject, text string, in bool) {
	if p.tip != nil {
		p.tip.Hide()
		p.tip, p.tipText = nil, ""
	}
	c := fyne.CurrentApp().Driver().CanvasForObject(b)
	if !in || c == nil {
		return
	}
	label := designText(text, 12, theme.ColorNameForeground, false)
	box := widgets.NewPanel(style.ColorNameMenuPanel, style.ColorNameCardBorder, 6, label, 8, 4, 8, 4)
	p.tip, p.tipText = widget.NewPopUp(box, c), text
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(b)
	w := box.MinSize().Width
	x := min(pos.X+b.Size().Width-w, c.Size().Width-w-4)
	p.tip.ShowAtPosition(fyne.NewPos(max(4, x), pos.Y+b.Size().Height+4))
}

// pillTone is the color of a pill.
type pillTone int

const (
	pillOK  pillTone = iota // green: "TLS"
	pillBad                 // red: "Expired Mar 2025"
)

// statusPill is a small rounded label in a table cell (.pill: 11 px, padding 1 x 7, fully rounded).
type statusPill struct {
	widget.BaseWidget
	text string
	tone pillTone
}

func newPill(text string, tone pillTone) *statusPill {
	p := &statusPill{text: text, tone: tone}
	p.ExtendBaseWidget(p)
	return p
}

func (p *statusPill) CreateRenderer() fyne.WidgetRenderer {
	fill, fg := style.ColorNameSuccessTint, theme.ColorNameSuccess
	if p.tone == pillBad {
		fill, fg = style.ColorNameDangerSurface, style.ColorNameDanger
	}
	t := designText(p.text, 11, fg, false)
	ts := t.MinSize()
	bg := style.BindRect(&canvas.Rectangle{CornerRadius: (ts.Height + 2) / 2}, fill, "")
	return widget.NewSimpleRenderer(container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(1, 1, 7, 7), t)))
}
