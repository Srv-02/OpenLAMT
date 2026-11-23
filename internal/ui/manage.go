package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Device management (issue #1, Main.dc.html and WindowB.dc.html): the pages Hardware, Event log,
// Audit log, Settings and Users of one device, shown in the 400 px sidebar of the device window
// (tabs) or in a window of their own (navigation on the left). Both views build their own widgets
// on the data the manager reads once per device (manageSource); a new result updates both.

// manageDeps is what the device window hands to its device management.
type manageDeps struct {
	host       string                    // Session.Config().Host
	status     func() (amt.Status, bool) // last status read of the device window (AMT version, control mode, power)
	loginStore session.LoginStore        // favorites manager; nil = nothing stored
	onError    func(error)
	// mainWin shows "New password not saved" of a write that ends after device management
	// closed (the start window); nil drops it with the other results.
	mainWin fyne.Window
}

// Measures of the sidebar (Main.dc.html) and the window (WindowB.dc.html).
const (
	manageSidebarWidth float32 = 400
	manageNavWidth     float32 = 240
	manageHeadHeight   float32 = 64
	manageTick                 = time.Second // "Read … ago" and the window head follow the clock
)

// managePage is one page of device management. build returns a new view of it; compact is the
// sidebar (the page lays out its own controls), otherwise the window (head controls go to
// managePane.info and tools).
type managePage struct {
	section manageSection
	tab     string // sidebar tab caption, e.g. "Events"
	title   string // navigation entry and page head of the window, e.g. "Event log"
	icon    icons.Name
	build   func(compact bool) *managePane
}

// managePane is one view of a page.
type managePane struct {
	body   fyne.CanvasObject
	info   *canvas.Text      // window only: shown after the title, e.g. "· 209 entries"; nil = none
	tools  fyne.CanvasObject // window only: right part of the page head; nil = none
	scroll bool              // the view puts body into a vertical scroll area
	shown  func()            // the pane became visible: the first read starts here
	tick   func()            // every manageTick while the pane is the current one
	close  func()            // the view goes away: stop listening to the data
	impl   any               // the page's own state (tests)

	// relayout is set by the view: the pane calls it after its content changed size, so the
	// scroll area (and the window's page head) place it again.
	relayout func()
}

// changed calls relayout if the view has set it.
func (p *managePane) changed() {
	if p.relayout != nil {
		p.relayout()
	}
}

// manageSource is the data of one page for one device: read off the Fyne thread, kept with the
// time of the read, shared by every view. Only touched on the Fyne thread.
type manageSource[T any] struct {
	m       *manager
	read    func() (T, error) // blocks; runs on its own goroutine
	val     T                 // last successful read
	err     error             // last read failed; val keeps the data before it
	at      time.Time         // time of the last successful read (manager.now); zero = none yet
	took    time.Duration     // duration of the last successful read
	started bool              // a read was started once (only Refresh reads again)
	loading bool
	reads   int // reads started (tests)
	subs    map[int]func()
	nextSub int
}

func newManageSource[T any](m *manager, read func() (T, error)) *manageSource[T] {
	return &manageSource[T]{m: m, read: read, subs: map[int]func(){}}
}

// ensure starts the first read; later calls do nothing (Refresh reads again).
func (s *manageSource[T]) ensure() {
	if !s.started {
		s.reload()
	}
}

// reload reads the data again unless a read is running or the manager is closed.
func (s *manageSource[T]) reload() {
	if s.loading || s.m.closed {
		return
	}
	s.started, s.loading = true, true
	s.reads++
	s.notify()
	read, now := s.read, s.m.now // now is read here, on the Fyne thread: tests replace it
	go func() {
		start := now()
		v, err := read()
		took := now().Sub(start)
		s.m.later(func() { s.done(v, err, took) })
	}()
}

// done applies a result on the Fyne thread; after Close it is dropped.
func (s *manageSource[T]) done(v T, err error, took time.Duration) {
	if s.m.closed {
		return
	}
	s.loading = false
	if err != nil {
		s.err = err
	} else {
		s.val, s.err, s.at, s.took = v, nil, s.m.now(), took
	}
	s.notify()
}

// loaded reports whether a read has succeeded once.
func (s *manageSource[T]) loaded() bool { return !s.at.IsZero() }

// subscribe calls fn after every change (read started, result); the returned function stops it.
func (s *manageSource[T]) subscribe(fn func()) (cancel func()) {
	id := s.nextSub
	s.nextSub++
	s.subs[id] = fn
	return func() { delete(s.subs, id) }
}

func (s *manageSource[T]) notify() {
	for _, fn := range s.subs {
		fn()
	}
}

// manager is the device management of one device window.
type manager struct {
	app    fyne.App
	parent fyne.Window
	sess   *session.Session
	name   string
	deps   manageDeps

	hw     *manageSource[amt.HardwareInfo]
	events *manageSource[[]logRow]
	audit  *manageSource[[]logRow]

	settings *settingsData

	pages    []managePage
	side     *manageSidebar
	win      *manageWindow // open window or nil
	onPopOut func()
	users    *manageSource[amt.UserList] // created by the first Users pane
	closed   bool
	tlsAsk   *panel // the last "without TLS" question (tests)

	now       func() time.Time
	tickEvery time.Duration // 0: no clock (tests call tick)
	tickStop  chan struct{}
	later     func(func()) // fyne.Do; tests apply the results on their own goroutine
}

func newManager(a fyne.App, parent fyne.Window, sess *session.Session, name string, deps manageDeps) *manager {
	m := &manager{app: a, parent: parent, sess: sess, name: name, deps: deps, now: time.Now, tickEvery: manageTick, later: fyne.Do}
	if m.deps.host == "" && sess != nil {
		m.deps.host = sess.Config().Host
	}
	m.hw = newManageSource(m, sess.HardwareInfo)
	m.events = newManageSource(m, func() ([]logRow, error) {
		e, err := sess.EventLog()
		return eventRows(e, time.Local), err
	})
	m.audit = newManageSource(m, func() ([]logRow, error) {
		e, err := sess.AuditLog()
		return auditRows(e, time.Local), err
	})
	m.settings = newSettingsData(m)
	m.pages = managePages(m)
	return m
}

// status returns the device window's last status read.
func (m *manager) status() (amt.Status, bool) {
	if m.deps.status == nil {
		return amt.Status{}, false
	}
	return m.deps.status()
}

// controlMode is ACM or CCM, "" while unknown.
func (m *manager) controlMode() amt.ControlMode {
	if st, ok := m.status(); ok && (st.ControlMode == amt.ControlModeAdmin || st.ControlMode == amt.ControlModeClient) {
		return st.ControlMode
	}
	return ""
}

// SetOnPopOut sets what runs when the pop-out symbol is tapped, before the window opens (the
// device window closes its sidebar).
func (m *manager) SetOnPopOut(fn func()) { m.onPopOut = fn }

// Sidebar returns the 400 px panel for the device window, built on the first call. Its first
// page (Hardware) is read then, so the device window should call it when it opens the sidebar.
func (m *manager) Sidebar() fyne.CanvasObject {
	if m.side == nil {
		m.side = newManageSidebar(m)
		m.startClock()
	}
	return m.side.obj
}

// OpenWindow opens the window with the navigation on the page the sidebar shows, or brings the
// open one to the front.
func (m *manager) OpenWindow() {
	if m.closed {
		return
	}
	if m.win != nil {
		m.win.win.RequestFocus()
		return
	}
	page := 0
	if m.side != nil {
		page = m.side.view.current
	}
	m.openWindowAt(page)
}

// openWindowAt opens the window on page, or brings the open one to the front on that page.
func (m *manager) openWindowAt(page int) {
	if m.closed {
		return
	}
	if m.win != nil {
		m.win.win.RequestFocus()
		m.win.showPage(page)
		return
	}
	m.win = newManageWindow(m, page)
	m.startClock()
	m.win.win.Show()
}

// popOut is the pop-out symbol of the sidebar.
func (m *manager) popOut() {
	if m.onPopOut != nil {
		m.onPopOut()
	}
	m.OpenWindow()
}

// popOutTo opens the window on page like the pop-out symbol does (a page the sidebar cannot
// show): the device window closes its sidebar first.
func (m *manager) popOutTo(page int) {
	if m.onPopOut != nil {
		m.onPopOut()
	}
	m.openWindowAt(page)
}

// Close ends device management with the device window: its own window closes, the clock stops
// and results that arrive later are dropped.
func (m *manager) Close() {
	if m.closed {
		return
	}
	m.closed = true
	if m.tickStop != nil {
		close(m.tickStop)
		m.tickStop = nil
	}
	if m.win != nil {
		m.win.win.Close()
	}
	if m.side != nil {
		m.side.dismissMore()
		m.side.view.close()
	}
}

// startClock starts the clock that updates "Read … ago" and the window head once.
func (m *manager) startClock() {
	if m.tickStop != nil || m.tickEvery <= 0 || m.closed {
		return
	}
	stop := make(chan struct{})
	m.tickStop = stop
	every := m.tickEvery
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fyne.Do(m.tick)
			}
		}
	}()
}

// tick updates the current page of each view and the window head.
func (m *manager) tick() {
	if m.closed {
		return
	}
	if m.side != nil {
		m.side.view.tick()
	}
	if m.win != nil {
		m.win.view.tick()
		m.win.refreshHead()
	}
}

// describeManage is describe with the text for a missing AMT permission.
func describeManage(err error) string {
	if errors.Is(err, amt.ErrAccessDenied) {
		return lang.L("This account is not allowed to read this information on the device.")
	}
	return describe(err)
}

// manageView holds the panes of one view (sidebar or window); a pane is built when it is
// first shown and stays until the view closes.
type manageView struct {
	m       *manager
	compact bool
	panes   []*managePane
	slots   []fyne.CanvasObject
	stack   *fyne.Container
	current int

	onRelayout func(*managePane) // the window places its page head again
}

func newManageView(m *manager, compact bool) *manageView {
	return &manageView{m: m, compact: compact, panes: make([]*managePane, len(m.pages)),
		slots: make([]fyne.CanvasObject, len(m.pages)), stack: container.NewStack(), current: -1}
}

// show makes page i the visible one and returns its pane.
func (v *manageView) show(i int) *managePane {
	if i < 0 || i >= len(v.panes) {
		return nil
	}
	p := v.panes[i]
	if p == nil {
		p = v.m.pages[i].build(v.compact)
		v.panes[i] = p
		slot := v.frame(p)
		v.slots[i] = slot
		p.relayout = func() {
			slot.Refresh()
			if v.onRelayout != nil {
				v.onRelayout(p)
			}
		}
		v.stack.Add(slot)
	}
	for j, s := range v.slots {
		if s != nil {
			setShown(s, j == i)
		}
	}
	v.current = i
	if p.shown != nil {
		p.shown()
	}
	return p
}

// frame puts a pane body into the padding of the view, in a scroll area if it asks for one.
func (v *manageView) frame(p *managePane) fyne.CanvasObject {
	top, side, bottom := float32(16), float32(24), float32(16)
	if v.compact {
		top, side = 4, 16 // Main.dc.html: padding 4px 16px 16px
	}
	body := container.New(layout.NewCustomPaddedLayout(top, bottom, side, side), p.body)
	if p.scroll {
		return container.NewVScroll(body)
	}
	return body
}

func (v *manageView) pane() *managePane {
	if v.current < 0 {
		return nil
	}
	return v.panes[v.current]
}

func (v *manageView) tick() {
	if p := v.pane(); p != nil && p.tick != nil {
		p.tick()
	}
}

func (v *manageView) close() {
	for _, p := range v.panes {
		if p != nil && p.close != nil {
			p.close()
		}
	}
}

func setShown(o fyne.CanvasObject, on bool) {
	if on {
		o.Show()
	} else {
		o.Hide()
	}
}

// manageSidebar is the panel of the device window (Main.dc.html): head with title and pop-out
// symbol, the page tabs and the current page. The main pages are the tabs; further pages sit in
// the menu of the last tab, "More".
type manageSidebar struct {
	m      *manager
	obj    fyne.CanvasObject
	tabs   *widgets.Segmented
	popOut *managerButton
	view   *manageView

	main      int                // number of main pages: the tabs 0..main-1 are pages 0..main-1
	more      int                // index of the "More" tab, -1 without extra pages
	morePopup *widgets.MenuPopup // the open More menu (nil = closed)
	moreMenu  *fyne.Menu         // its items (tests)
}

func newManageSidebar(m *manager) *manageSidebar {
	s := &manageSidebar{m: m, view: newManageView(m, true), more: -1}
	var options []widgets.SegmentOption
	for _, p := range m.pages {
		if p.section == sectionMain {
			options = append(options, widgets.SegmentOption{Label: p.tab})
		}
	}
	s.main = len(options)
	if s.main < len(m.pages) {
		s.more = s.main
		options = append(options, widgets.SegmentOption{Label: lang.L("More"), Icon: icons.ChevronDown, Natural: true})
	}
	s.tabs = widgets.NewSegmented(widgets.SegmentedTabs, options, s.tabChanged)
	s.tabs.OnReselected = func(i int) {
		if i == s.more {
			s.openMore()
		}
	}
	s.popOut = &managerButton{OnTapped: m.popOut, icon: icons.ExternalLink, iconSize: 16, size: fyne.NewSquareSize(28)}
	s.popOut.ExtendBaseWidget(s.popOut)

	title := designText(lang.L("Device management"), 15, "foreground", true)
	headRow := container.New(&rowLayout{gap: 8, fill: 1}, title, spacer(), s.popOut)
	head := container.New(layout.NewCustomPaddedLayout(14, 10, 16, 16),
		container.New(layout.NewCustomPaddedVBoxLayout(10), headRow, s.tabs))
	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameSidePanel, "")
	edge := style.BindRect(canvas.NewRectangle(nil), style.ColorNameSeparatorDark, "")
	edge.SetMinSize(fyne.NewSize(1, 1))
	panel := container.NewStack(bg, container.New(&columnLayout{fill: 1}, head, s.view.stack))
	s.obj = container.New(&manageSizeLayout{w: manageSidebarWidth}, container.New(&manageHSplit{fill: 1}, edge, panel))
	s.view.show(0)
	return s
}

// tabIndex is the tab that stands for page: the page's own tab, or "More" for a More page.
// Diagnostics pages have none (the sidebar never shows them); the selection stays then.
func (s *manageSidebar) tabIndex(page int) int {
	if page >= 0 && page < len(s.m.pages) {
		switch s.m.pages[page].section {
		case sectionMain:
			return page
		case sectionMore:
			return s.more
		}
	}
	return s.tabs.Selected()
}

// tabChanged is a tap on another tab: a main page is shown; "More" keeps the page and opens
// its menu.
func (s *manageSidebar) tabChanged(i int) {
	if i == s.more {
		s.tabs.SetSelected(s.tabIndex(s.view.current))
		s.openMore()
		return
	}
	s.view.show(i)
	if s.more >= 0 {
		s.tabs.SetLabel(s.more, lang.L("More"))
	}
}

// openMore shows the More menu below the tabs, its right edge at theirs: the More pages, then,
// after a separator, the diagnostics pages (they open the window). The items are built anew
// each time, so their symbols follow the theme.
func (s *manageSidebar) openMore() {
	s.dismissMore()
	c := s.m.parent.Canvas()
	if c == nil {
		return
	}
	var items, diag []*fyne.MenuItem
	for i, p := range s.m.pages {
		switch p.section {
		case sectionMore:
			it := fyne.NewMenuItem(p.title, func() {
				s.view.show(i)
				s.tabs.SetLabel(s.more, p.tab)
				s.tabs.SetSelected(s.more)
			})
			it.Icon = icons.Get(p.icon, icons.Foreground)
			items = append(items, it)
		case sectionDiagnostics:
			it := fyne.NewMenuItem(p.title, func() { s.m.popOutTo(i) })
			it.Icon = icons.Get(p.icon, icons.Foreground)
			diag = append(diag, it)
		}
	}
	if len(items) > 0 && len(diag) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
	}
	items = append(items, diag...)
	s.moreMenu = fyne.NewMenu("", items...)
	popup := widgets.NewMenuPopup(s.moreMenu, c)
	for _, it := range diag {
		popup.SetTrailing(it, icons.ExternalLink)
	}
	popup.OnDismiss = func() {
		if s.morePopup == popup {
			s.morePopup = nil
		}
	}
	s.morePopup = popup
	abs := fyne.CurrentApp().Driver().AbsolutePositionForObject(s.tabs)
	size := s.tabs.Size()
	popup.ShowAtPosition(fyne.NewPos(abs.X+size.Width-popup.PanelSize().Width, abs.Y+size.Height+6))
}

// dismissMore closes the More menu if it is open.
func (s *manageSidebar) dismissMore() {
	if s.morePopup != nil {
		s.morePopup.Dismiss()
	}
}

// manageSizeLayout gives its objects the full size; w and h, if set, are the minimum size
// (other than 0, the objects' own minimum is ignored in that direction).
type manageSizeLayout struct{ w, h float32 }

func (l *manageSizeLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l *manageSizeLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		ms := o.MinSize()
		w, h = max(w, ms.Width), max(h, ms.Height)
	}
	if l.w > 0 {
		w = l.w
	}
	if l.h > 0 {
		h = l.h
	}
	return fyne.NewSize(w, h)
}

// manageHSplit lays out its objects left to right at full height without gaps; the object at
// index fill takes the width left over, the others their minimum width.
type manageHSplit struct{ fill int }

func (l *manageHSplit) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	rest := size.Width
	for i, o := range objs {
		if i != l.fill {
			rest -= o.MinSize().Width
		}
	}
	x := float32(0)
	for i, o := range objs {
		w := o.MinSize().Width
		if i == l.fill {
			w = max(0, rest)
		}
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(w, size.Height))
		x += w
	}
}

func (l *manageHSplit) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		ms := o.MinSize()
		w += ms.Width
		h = max(h, ms.Height)
	}
	return fyne.NewSize(w, h)
}

// manageWindow is the window of its own (WindowB.dc.html): navigation on the left with the
// device, page head and page on the right.
type manageWindow struct {
	m          *manager
	win        fyne.Window
	view       *manageView
	nav        []*manageNavItem
	navBox     *fyne.Container   // the navigation's entries (tests)
	navSep     fyne.CanvasObject // separator before the More pages; nil without them
	navHeading *canvas.Text      // heading "Diagnostics"; nil without such pages
	dot        *widgets.StatusDot
	sub        *ellipsisText
	title      *canvas.Text
	heading    *fyne.Container // title and the pane's info
	tools      *fyne.Container // the pane's tools
	headRow    *fyne.Container // heading and tools
}

func newManageWindow(m *manager, page int) *manageWindow {
	w := &manageWindow{m: m, win: m.app.NewWindow(m.name), view: newManageView(m, false)}
	w.view.onRelayout = func(p *managePane) {
		if w.view.pane() == p {
			w.relayoutHead()
		}
	}

	// navigation
	w.dot = widgets.NewStatusDot(widgets.DotUnknown, 9)
	name := newEllipsisText(m.name, 14, "foreground", true)
	name.shrink = true
	w.sub = newEllipsisText("", 12, style.ColorNameTextSecondary, false)
	w.sub.shrink = true
	device := container.New(layout.NewCustomPaddedLayout(4, 16, 8, 8),
		container.New(&manageHSplit{fill: 1}, container.NewCenter(w.dot),
			container.New(layout.NewCustomPaddedLayout(0, 0, 10, 0), container.New(layout.NewCustomPaddedVBoxLayout(1), name, w.sub))))
	items := []fyne.CanvasObject{device}
	var prev manageSection
	for i, p := range m.pages {
		// a separator before the first More page, a heading before the first diagnostics page
		if p.section != prev {
			switch p.section {
			case sectionMore:
				w.navSep = container.New(layout.NewCustomPaddedLayout(2, 2, 4, 4), hline(style.ColorNameSeparatorDark))
				items = append(items, w.navSep)
			case sectionDiagnostics:
				w.navHeading = designText(lang.L("Diagnostics"), 11, style.ColorNameTextSecondary, true)
				items = append(items, container.New(layout.NewCustomPaddedLayout(8, 0, navPadH, 0), w.navHeading))
			}
			prev = p.section
		}
		it := newManageNavItem(p.icon, p.title, func() { w.showPage(i) })
		w.nav = append(w.nav, it)
		items = append(items, it)
	}
	w.navBox = container.New(layout.NewCustomPaddedVBoxLayout(4), items...)
	navBg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameSidebar, "")
	navEdge := style.BindRect(canvas.NewRectangle(nil), style.ColorNameSeparatorDark, "")
	navEdge.SetMinSize(fyne.NewSize(1, 1))
	nav := container.New(&manageSizeLayout{w: manageNavWidth}, container.NewStack(navBg,
		container.New(&manageHSplit{fill: 0}, container.New(layout.NewCustomPaddedLayout(16, 16, 12, 12),
			w.navBox), navEdge)))

	// page head and page
	w.title = designText("", 18, "foreground", true)
	w.heading = container.New(&baselineLayout{gap: 6}, w.title)
	w.tools = container.NewStack()
	w.headRow = container.New(&rowLayout{gap: 12, fill: 1}, w.heading, spacer(), w.tools)
	head := container.New(&manageSizeLayout{h: manageHeadHeight}, container.New(layout.NewCustomPaddedLayout(0, 0, 24, 24), w.headRow))
	right := container.New(&columnLayout{fill: 2}, head, hline(style.ColorNameSeparatorDark), w.view.stack)
	bg := style.BindRect(canvas.NewRectangle(nil), "background", "")

	w.win.SetPadded(false)
	w.win.SetContent(container.NewStack(bg, container.New(&manageHSplit{fill: 1}, nav, right)))
	w.win.Resize(fyne.NewSize(1120, 720))
	w.win.SetOnClosed(func() {
		w.view.close()
		if m.win == w {
			m.win = nil
		}
	})
	w.refreshHead()
	w.showPage(page)
	return w
}

// showPage shows page i with its head.
func (w *manageWindow) showPage(i int) {
	p := w.view.show(i)
	if p == nil {
		return
	}
	for j, it := range w.nav {
		it.SetActive(j == i)
	}
	w.title.Text = w.m.pages[i].title
	w.title.Refresh()
	w.heading.Objects = []fyne.CanvasObject{w.title}
	if p.info != nil {
		w.heading.Objects = append(w.heading.Objects, p.info)
	}
	w.tools.Objects = nil
	if p.tools != nil {
		w.tools.Objects = []fyne.CanvasObject{p.tools}
	}
	w.relayoutHead()
}

// relayoutHead places title, info and tools again after one of them changed its width.
func (w *manageWindow) relayoutHead() {
	w.heading.Refresh()
	w.tools.Refresh()
	w.headRow.Refresh()
}

// refreshHead shows power dot, host, AMT version and control mode from the device window's
// last status read: "192.0.2.10 · AMT 16.1 · ACM".
func (w *manageWindow) refreshHead() {
	st, known := w.m.status()
	w.dot.SetState(powerDot(known && st.Allowed(amt.StatusPower), st.PowerState))
	sub := w.m.deps.host
	if v := majorMinor(st.AMTVersion); known && v != "" {
		sub += " · AMT " + v
	}
	if mode := w.m.controlMode(); mode != "" {
		sub += " · " + string(mode)
	}
	w.sub.SetText(sub)
}

// manageNavItem is an entry of the window's navigation: symbol and caption, blue with white
// text while active (WindowB.dc.html .nav).
type manageNavItem struct {
	widget.BaseWidget
	OnTapped func()

	icon    icons.Name
	text    string
	active  bool
	hovered bool
}

func newManageNavItem(icon icons.Name, text string, tapped func()) *manageNavItem {
	n := &manageNavItem{icon: icon, text: text, OnTapped: tapped}
	n.ExtendBaseWidget(n)
	return n
}

func (n *manageNavItem) SetActive(on bool) { n.active = on; n.Refresh() }
func (n *manageNavItem) Active() bool      { return n.active }
func (n *manageNavItem) Tapped(*fyne.PointEvent) {
	if n.OnTapped != nil {
		n.OnTapped()
	}
}
func (n *manageNavItem) MouseIn(*desktop.MouseEvent)    { n.hovered = true; n.Refresh() }
func (n *manageNavItem) MouseMoved(*desktop.MouseEvent) {}
func (n *manageNavItem) MouseOut()                      { n.hovered = false; n.Refresh() }

// Geometry of a navigation entry: padding 8 x 12, radius 8, 16 px symbol, gap 10, 13 px text.
const (
	navPadV, navPadH, navGap, navIcon float32 = 8, 12, 10, 16
)

func (n *manageNavItem) CreateRenderer() fyne.WidgetRenderer {
	r := &manageNavRenderer{n: n, bg: &canvas.Rectangle{CornerRadius: 8}, icon: widgets.NewSymbol(nil, navIcon),
		text: canvas.NewText("", nil)}
	r.text.TextSize = 13
	r.Refresh()
	return r
}

type manageNavRenderer struct {
	n    *manageNavItem
	bg   *canvas.Rectangle
	icon *widgets.Symbol
	text *canvas.Text
}

func (r *manageNavRenderer) MinSize() fyne.Size {
	ts := fyne.MeasureText(r.n.text, 13, fyne.TextStyle{})
	return fyne.NewSize(2*navPadH+navIcon+navGap+ts.Width, 2*navPadV+max(navIcon, ts.Height))
}

func (r *manageNavRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.icon.Move(fyne.NewPos(navPadH, (size.Height-navIcon)/2))
	r.icon.Resize(fyne.NewSquareSize(navIcon))
	ts := r.text.MinSize()
	r.text.Move(fyne.NewPos(navPadH+navIcon+navGap, (size.Height-ts.Height)/2))
	r.text.Resize(ts)
}

func (r *manageNavRenderer) Refresh() {
	n := r.n
	r.bg.FillColor = style.Color(style.ColorNameSidebar)
	fg, variant := style.Color("foreground"), icons.Foreground
	switch {
	case n.active:
		r.bg.FillColor = style.Color("selection")
		fg, variant = style.Color(style.ColorNameWhite), icons.White
	case n.hovered:
		r.bg.FillColor = style.Color("hover")
	}
	r.bg.Refresh()
	if res := icons.Get(n.icon, variant); r.icon.Resource() != res {
		r.icon.SetResource(res)
	}
	r.text.Text, r.text.Color = n.text, fg
	r.text.Refresh()
	r.Layout(n.Size())
}

func (r *manageNavRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.icon, r.text}
}
func (r *manageNavRenderer) Destroy() {}

// manageError is the red error text of a page, hidden while there is no error.
type manageError struct {
	label *widget.Label
	box   fyne.CanvasObject
}

func newManageError() *manageError {
	l := widget.NewLabel("")
	l.Importance = widget.DangerImportance
	l.Wrapping = fyne.TextWrapWord
	// danger labels draw in "error", the red of the button fill; the text red is lighter in dark
	e := &manageError{label: l, box: container.NewThemeOverride(l, style.ErrorTextTheme{Theme: overrideTheme})}
	e.box.Hide()
	return e
}

// set shows describeManage(err), or hides the text for nil.
func (e *manageError) set(err error) {
	if err == nil {
		e.label.SetText("")
		e.box.Hide()
		return
	}
	e.label.SetText(describeManage(err))
	e.box.Show()
}

// Text returns the shown text.
func (e *manageError) Text() string { return e.label.Text }

// newManageButton is the white bordered button of the page heads ("Refresh", WindowB.dc.html;
// "+ Add user", Users.dc.html), 13 px regular like the mockups.
func newManageButton(text string, tapped func()) *widgets.PrimaryButton {
	b := widgets.NewPrimaryButton(text, nil, tapped)
	b.Importance = widget.MediumImportance
	b.Metrics = &widgets.ButtonMetrics{Height: 30, Radius: 8, PaddingH: 12, TextSize: 13, Regular: true,
		Neutral: style.ColorNameField, NeutralBorder: style.ColorNameControlBorder}
	return b
}
