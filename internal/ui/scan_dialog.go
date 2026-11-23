package ui

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/scan"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The dialog "Scan Network" (gesamtdesign #12, V17): finds Intel AMT devices in a network and
// saves the chosen ones as favorites. The scan only reads (internal/scan). Tests replace the two
// variables, so that no test scans a real network.
var (
	// newScanner makes the scanner of one run.
	newScanner = func() *scan.Scanner { return &scan.Scanner{} }
	// localNetwork is the network that prefills the address range.
	localNetwork = scan.LocalNetwork
)

const (
	scanWidth  float32 = 640
	scanHeight float32 = 600
)

// scanRow is a device of the result list and whether it is ticked.
type scanRow struct {
	res    scan.Result
	known  bool // already a favorite: grey, not selectable
	ticked bool
}

// scanDialog is the open dialog.
type scanDialog struct {
	parent  fyne.Window
	mgr     *favorites.Manager
	onAdded func(added []favorites.Favorite)
	d       *designDialog

	entry  *escEntry
	goBtn  *widgets.PrimaryButton
	addBtn *widgets.PrimaryButton
	dyn    *fyne.Container // note, progress and status below the input
	holder *fyne.Container // the result list or the empty state, inside the scroll area

	rows     []*scanRow
	hosts    map[string]bool // hosts of the favorites
	cancel   context.CancelFunc
	running  bool
	finished bool // a scan ran to its end or was stopped
	stopped  bool
	rangeIn  string // the range of the last run
	done     int
	total    int
	started  time.Time
	elapsed  time.Duration
	inputErr string
	run      int // number of the latest run; callbacks of an older one are dropped

	onFinished func() // test hook: a run ended and the dialog shows it
}

// showScanDialog opens the dialog on parent. onAdded runs after "Add Favorites" with the new
// favorites.
func showScanDialog(parent fyne.Window, mgr *favorites.Manager, onAdded func([]favorites.Favorite)) *scanDialog {
	sd := &scanDialog{parent: parent, mgr: mgr, onAdded: onAdded, hosts: map[string]bool{}}
	if list, err := mgr.List(); err == nil {
		for _, f := range list {
			sd.hosts[f.Host] = true
		}
	}
	sd.d = showDesignDialog(parent, dialogSpec{width: scanWidth, height: scanHeight, padTop: 20, padRight: 22, padBottom: 18, padL: 22, gap: 12, flex: 3},
		sd.build)
	return sd
}

func (sd *scanDialog) build(d *designDialog) []fyne.CanvasObject {
	d.onClose = func() {
		if sd.cancel != nil {
			sd.cancel()
		}
	}
	sd.entry = newEscEntry()
	if n, ok := localNetwork(); ok {
		sd.entry.SetText(n)
	}
	sd.entry.onEscape = d.Close
	sd.entry.OnSubmitted = func(string) { sd.toggle() }
	sd.entry.OnChanged = func(string) {
		if sd.inputErr != "" {
			sd.inputErr = ""
			sd.entry.SetValidationError(nil)
			sd.render()
		}
	}
	sd.goBtn = dialogButton(lang.L("Scan"), true, 18, sd.toggle)
	sd.goBtn.OnEscape = d.Close
	cancel := dialogButton(lang.L("Cancel"), false, 16, d.Close)
	cancel.OnEscape = d.Close
	sd.addBtn = dialogButton(lang.L("Add Favorites"), true, 18, sd.add)
	sd.addBtn.OnEscape = d.Close

	title := designText(lang.L("Scan Network"), style.Size("subHeadingText"), "foreground", true)
	label := designText(lang.L("Address range"), style.Size(style.SizeNameSecondaryText), style.ColorNameTextSecondary, true)
	input := container.New(&rowLayout{gap: 8, fill: 0}, container.NewThemeOverride(sd.entry, fieldTheme{}), sd.goBtn)
	sd.dyn = container.New(layout.NewCustomPaddedVBoxLayout(8))
	sd.holder = container.New(layout.NewCustomPaddedVBoxLayout(10))
	scroll := container.NewVScroll(sd.holder)
	scroll.SetMinSize(fyne.NewSize(0, minScrollHeight))
	buttons := container.New(&rowLayout{gap: 8, fill: 0}, spacer(), cancel, sd.addBtn)
	sd.render()
	return []fyne.CanvasObject{title, container.New(layout.NewCustomPaddedVBoxLayout(6), label, input), sd.dyn, scroll, buttons}
}

// toggle is the button "Scan" or "Stop".
func (sd *scanDialog) toggle() {
	if sd.running {
		sd.cancel()
		return
	}
	sd.start()
}

// scanErrorText is the red line below the input.
func scanErrorText(err error) string {
	if errors.Is(err, scan.ErrTooMany) {
		return lang.L("Scan up to 4,096 addresses at a time, for example 192.0.2.0/20.")
	}
	return lang.L("Enter a network such as 192.0.2.0/24 or a range such as 192.0.2.10-192.0.2.80.")
}

// start checks the input and runs the scan on a goroutine.
func (sd *scanDialog) start() {
	input := strings.TrimSpace(sd.entry.Text)
	targets, err := scan.ParseTargets(input)
	if err != nil {
		sd.inputErr = scanErrorText(err)
		sd.entry.SetValidationError(err)
		sd.render()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	sd.cancel, sd.running, sd.finished, sd.stopped = cancel, true, false, false
	sd.rows, sd.done, sd.total, sd.rangeIn, sd.started = nil, 0, len(targets), input, time.Now()
	sd.run++
	run := sd.run
	sd.render()
	scanner := newScanner()
	go func() {
		scanner.Scan(ctx, targets,
			func(done, _ int) { fyne.Do(func() { sd.progress(run, done) }) },
			func(r scan.Result) { fyne.Do(func() { sd.found(run, r) }) })
		fyne.Do(func() { sd.end(run, ctx.Err() != nil) })
	}()
}

func (sd *scanDialog) progress(run, done int) {
	if run != sd.run {
		return
	}
	sd.done = done
	sd.render()
}

// found adds a device to the list, ordered by address.
func (sd *scanDialog) found(run int, r scan.Result) {
	if run != sd.run {
		return
	}
	row := &scanRow{res: r, known: sd.hosts[r.Addr.String()] || (r.Name != "" && sd.hosts[r.Name])}
	row.ticked = !row.known && !r.PortsClosed()
	i, _ := slices.BinarySearchFunc(sd.rows, r.Addr, func(x *scanRow, a netip.Addr) int { return x.res.Addr.Compare(a) })
	sd.rows = slices.Insert(sd.rows, i, row)
	sd.render()
}

func (sd *scanDialog) end(run int, stopped bool) {
	if run != sd.run {
		return
	}
	sd.running, sd.finished, sd.stopped = false, true, stopped
	sd.elapsed = time.Since(sd.started)
	sd.cancel()
	sd.render()
	if sd.onFinished != nil {
		sd.onFinished()
	}
}

// ticked is the devices the user chose.
func (sd *scanDialog) ticked() []*scanRow {
	var out []*scanRow
	for _, r := range sd.rows {
		if r.ticked && !r.known {
			out = append(out, r)
		}
	}
	return out
}

// add saves the ticked devices as favorites: name is the DNS name or the address, user "admin",
// TLS if port 16993 is open, no password (the known password dialog asks on the first connect).
func (sd *scanDialog) add() {
	var items []favorites.Incoming
	for _, r := range sd.ticked() {
		name := r.res.Name
		if name == "" {
			name = r.res.Addr.String()
		}
		items = append(items, favorites.Incoming{Favorite: favorites.Favorite{
			Name: name, Host: r.res.Addr.String(), Username: "admin", UseTLS: r.res.TLS}})
	}
	if len(items) == 0 {
		return
	}
	added, _, _, err := sd.mgr.Merge(items)
	sd.d.Close()
	if len(added) > 0 && sd.onAdded != nil {
		sd.onAdded(added)
	}
	if err != nil {
		showError(sd.parent, err)
	}
}

// render shows the state: the note or error, the progress, the list.
func (sd *scanDialog) render() {
	if sd.running {
		sd.goBtn.SetText(lang.L("Stop"))
		sd.goBtn.Importance = widget.MediumImportance
	} else {
		sd.goBtn.SetText(lang.L("Scan"))
		sd.goBtn.Importance = widget.HighImportance
	}
	sd.goBtn.Refresh()

	var dyn []fyne.CanvasObject
	if sd.inputErr != "" {
		dyn = append(dyn, designText(sd.inputErr, style.Size(style.SizeNameSecondaryText), style.ColorNameErrorText, false))
	} else {
		dyn = append(dyn, wrappedNote(lang.L("A network such as 192.0.2.0/24 or a range such as 192.0.2.10-192.0.2.80. Up to 4,096 addresses."), scanWidth-44))
	}
	if sd.running {
		bar := widget.NewProgressBar()
		bar.TextFormatter = func() string { return "" }
		bar.Max = float64(max(sd.total, 1))
		bar.SetValue(float64(sd.done))
		dyn = append(dyn, bar)
	}
	if status := sd.status(); status != "" {
		dyn = append(dyn, wrappedNote(status, scanWidth-44))
	}
	sd.dyn.Objects = dyn
	sd.dyn.Refresh()
	if sd.d != nil && sd.d.pop != nil { // the notes changed the height: the dialog lays out again
		sd.d.pop.Content.Refresh()
	}

	sd.holder.Objects = sd.list()
	sd.holder.Refresh()

	n := len(sd.ticked())
	switch {
	case n == 0:
		sd.addBtn.SetText(lang.L("Add Favorites"))
		sd.addBtn.Disable()
	case n == 1:
		sd.addBtn.SetText(lang.L("Add 1 Favorite"))
		sd.addBtn.Enable()
	default:
		sd.addBtn.SetText(lang.L("Add {{.Count}} Favorites", map[string]any{"Count": n}))
		sd.addBtn.Enable()
	}
}

// wrappedNote is a hint in the secondary color, wrapped at width.
func wrappedNote(text string, width float32) fyne.CanvasObject {
	size := style.Size(style.SizeNameSecondaryText)
	var lines []fyne.CanvasObject
	for _, l := range wrapWords(text, size, width) {
		lines = append(lines, designText(l, size, style.ColorNameTextSecondary, false))
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(1), lines...)
}

// status is the line below the progress.
func (sd *scanDialog) status() string {
	args := map[string]any{"Done": sd.done, "Total": sd.total, "Found": len(sd.rows), "Seconds": int(sd.elapsed.Round(time.Second) / time.Second)}
	switch {
	case sd.running:
		return lang.L("Scanning… {{.Done}} of {{.Total}} addresses · {{.Found}} found", args)
	case sd.finished && sd.stopped:
		return lang.L("Stopped after {{.Done}} of {{.Total}} addresses · {{.Found}} found", args)
	case sd.finished:
		return lang.L("{{.Total}} addresses scanned in {{.Seconds}} s · {{.Found}} found", args)
	}
	return ""
}

// The columns of the result table: tick, address, name (takes the rest), version, connection, note.
var scanColumns = []float32{24, 108, 0, 64, 84, 118}

// list is the result table, or the empty state of a finished scan that found nothing.
func (sd *scanDialog) list() []fyne.CanvasObject {
	if len(sd.rows) == 0 {
		if !sd.finished {
			return nil
		}
		items := bulletList(
			lang.L("Intel AMT is turned off or not activated."),
			lang.L("A firewall blocks ports 623, 16992 or 16993."),
			lang.L("The devices are in another network."))
		head := designText(lang.L("No Intel AMT devices found in {{.Range}}", map[string]any{"Range": sd.rangeIn}), style.Size("text"), "foreground", true)
		card := widgets.NewFormCard(container.New(layout.NewCustomPaddedLayout(12, 12, rowPadH, rowPadH),
			container.New(layout.NewCustomPaddedVBoxLayout(4), head, items)))
		card.Background = style.ColorNameDialogCard
		return []fyne.CanvasObject{card}
	}
	small := style.Size(style.SizeNameSecondaryText)
	header := func(t string) fyne.CanvasObject { return designText(t, small, style.ColorNameTextSecondary, true) }
	rows := []fyne.CanvasObject{sd.row(widget.NewLabel(""), header(lang.L("Address")), header(lang.L("Name")),
		header(lang.L("Intel AMT")), header(lang.L("Connection")), header(""))}
	for _, r := range sd.rows {
		rows = append(rows, sd.resultRow(r))
	}
	card := widgets.NewFormCard(rows...)
	card.Background = style.ColorNameDialogCard
	return []fyne.CanvasObject{card}
}

func (sd *scanDialog) row(cells ...fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(6, 6, rowPadH, rowPadH), container.New(&columnsLayout{widths: scanColumns, gap: 8}, cells...))
}

func (sd *scanDialog) resultRow(r *scanRow) fyne.CanvasObject {
	tick := widget.NewCheck("", func(on bool) { r.ticked = on; sd.render() })
	tick.Checked = r.ticked
	if r.known {
		tick.Disable()
	}
	color := style.ColorNameTextSecondary
	if !r.known && !r.res.PortsClosed() {
		color = "foreground"
	}
	text := func(t string) fyne.CanvasObject { return designText(t, style.Size("text"), color, false) }
	name := widget.NewLabel(r.res.Name)
	name.Truncation = fyne.TextTruncateEllipsis
	nameCell := flushText(name)
	version := r.res.Version
	if version == "" {
		version = lang.L("unknown")
	}
	conn, note := lang.L("No TLS"), ""
	switch {
	case r.res.TLS:
		conn = lang.L("TLS")
	case r.res.PortsClosed():
		conn, note = lang.L("Ports closed"), lang.L("Not activated?")
	}
	if r.known {
		note = lang.L("Already a favorite")
	}
	return sd.row(tick, text(r.res.Addr.String()), nameCell, text(version), text(conn), designText(note, style.Size(style.SizeNameSecondaryText), style.ColorNameTextSecondary, false))
}

// columnsLayout puts its objects in columns of fixed widths; a width of 0 takes the rest.
type columnsLayout struct {
	widths []float32
	gap    float32
}

func (l *columnsLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var used float32
	for _, w := range l.widths {
		used += w
	}
	rest := max(0, size.Width-used-l.gap*float32(len(l.widths)-1))
	x := float32(0)
	for i, o := range objs {
		w := l.widths[i]
		if w == 0 {
			w = rest
		}
		h := o.MinSize().Height
		o.Move(fyne.NewPos(x, (size.Height-h)/2))
		o.Resize(fyne.NewSize(w, h))
		x += w + l.gap
	}
}

func (l *columnsLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, cw := range l.widths {
		w += cw
	}
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(w+l.gap*float32(len(l.widths)-1), h)
}
