package ui

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/typing"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// detailMode is what the detail pane of the manager shows.
type detailMode int

const (
	modeNew  detailMode = iota // "Quick Connect": empty form, Connect and Save as favorite; after Duplicate Cancel and Save
	modeView                   // a favorite, read only, Connect
	modeEdit                   // a favorite in edit mode, Save and Cancel
)

// StartWindow is the manager window (ManagerA.html): the favorites in the sidebar, the
// detail pane of the selected favorite or the "New Connection" form, and the registry of open
// device windows (one per host and user).
type StartWindow struct {
	app      fyne.App
	win      fyne.Window
	mgr      *favorites.Manager
	settings Settings
	conn     *connector
	keys     keychain.Store
	prober   *statusProber

	// Quit guard (quit_guard.go)
	later       func(func()) // fyne.Do; tests collect the batch themselves
	closeReqs   []fyne.Window
	closeQueued bool
	quitAsking  bool
	quitApp     func()       // closes the master window: its SetOnClosed closes the device windows
	exitWork    *pendingWork // unmounting of closed device windows; WaitForCleanup waits for it

	items  []favorites.Favorite
	shown  []favorites.Favorite // items matching the search
	probes map[string]probeResult
	saved  map[string]bool // deviceKey → password in the keychain, as last looked up

	// Sidebar
	search      *widget.Entry
	list        *fyne.Container // the SidebarRows of shown, or the empty-state hint
	rows        map[string]*widgets.SidebarRow
	newConnBtn  *managerButton
	settingsBtn *managerButton

	// Detail pane
	mode     detailMode
	selKey   string              // deviceKey of the selected favorite; "" in modeNew
	dupOf    *favorites.Favorite // modeNew opened by Duplicate: Save must not replace a favorite
	editBtn  *managerButton      // toolbar
	dupBtn   *managerButton      // toolbar
	delBtn   *managerButton      // toolbar
	title    *ellipsisText       // 22 px heading
	meta     *fyne.Container     // "AMT … · ACM · TLS"
	busyRow  *fyne.Container     // "Connecting to …" in place of meta
	actions  *fyne.Container     // header buttons
	cards    *fyne.Container     // the cards below the header
	scroller *container.Scroll   // scrolls the detail content
	detail   fyne.CanvasObject   // the whole detail pane
	advanced *fyne.Container     // heading and Advanced card
	card1    *widgets.FormCard   // Host, Name, Username, Password
	card2    *widgets.FormCard   // Use TLS, Save password
	card3    *widgets.FormCard   // Advanced: layout, quality, reset fingerprint
	values   map[string]*widget.Label

	// Status row of the read-only view: the dot and text of the selected favorite's last result
	statusDot  *widgets.StatusDot
	statusText *widget.Label
	statusLine fyne.CanvasObject

	host, name, user, password    *widget.Entry
	passwordEye                   fyne.CanvasObject // the password entry's revealer
	useTLS, savePassword, resetFP *widgets.Switch
	layoutSel, qualitySel         *widget.Select
	autoSel                       *widget.Select // KVM auto-connect override: Default, On, Off
	editQuality                   string         // the favorite's quality when edit mode opened
	editLayout, editAuto          string         // layout and auto-connect override when edit mode opened or last followed the file
	connectBtn                    *widgets.PrimaryButton
	saveBtn, cancelBtn            *widget.Button
	editSaveBtn, editCancelBtn    *widget.Button
	status                        *widget.Label
	activity                      *widget.Activity

	open map[string]*DeviceWindow // key: deviceKey(host, user)
	busy map[string]string        // attempts in progress, value: display name

	onError        func(error)
	openDevice     func(s *session.Session, st amt.Status, name string)
	showSettings   func()
	lookupPassword func(host, user string, answer func(saved bool)) // answer runs on the Fyne thread
	savedGen       map[string]int                                   // per device: lookups started before the latest one are stale
	onReloaded     func()                                           // test hook
	coffee         *coffeeNudge                                     // nil until EnableCoffeeNudge
	updates        *updateNotice                                    // nil until EnableUpdateCheck (and for dev builds)
}

func deviceKey(host, user string) string { return user + "@" + host }

func NewStartWindow(a fyne.App, mgr *favorites.Manager, keys keychain.Store, connect session.ConnectFunc) *StartWindow {
	return newStartWindow(a, mgr, keys, connect, nil)
}

// newStartWindow is NewStartWindow; tests pass a setup that runs before the favorites are
// loaded, the first one is selected and the background probe starts.
func newStartWindow(a fyne.App, mgr *favorites.Manager, keys keychain.Store, connect session.ConnectFunc, setup func(*StartWindow)) *StartWindow {
	s := &StartWindow{later: fyne.Do, exitWork: &pendingWork{}, app: a, win: a.NewWindow("OpenLAMT"), mgr: mgr, keys: keys, settings: NewSettings(a.Preferences()),
		open: map[string]*DeviceWindow{}, busy: map[string]string{}, probes: map[string]probeResult{}, saved: map[string]bool{}, savedGen: map[string]int{},
		rows: map[string]*widgets.SidebarRow{}, values: map[string]*widget.Label{}}
	s.onError = func(err error) { showError(s.win, err) }
	s.openDevice = s.showDevice
	s.showSettings = func() { showSettingsDialog(s.win, &s.settings) }
	s.lookupPassword = s.lookupPasswordAsync
	s.conn = newConnector(s.win, mgr, keys, connect)
	// A password saved while connecting (dialog, "Save password") is also in the keychain when
	// the attempt was cancelled afterwards, so this is not tied to onOpened.
	s.conn.onPasswordSaved = func(host, user string) { s.refreshSaved(host, user) }
	s.conn.onOpened = func(sess *session.Session, st amt.Status, name string) {
		s.password.SetText("")
		s.openDevice(sess, st, name)
	}
	s.conn.onError = func(err error) { s.onError(err) }
	s.conn.onDone = func(host, user string) { s.end(host, user) }
	s.prober = newStatusProber(keys, connect, s.settings.PollInterval, func(key string, r probeResult) {
		fyne.Do(func() { s.applyProbe(key, r) })
	})

	s.buildDetail()
	s.win.SetPadded(false) // sidebar and detail pane reach the window edges, as in the design
	s.win.SetContent(container.New(sidebarLayout{}, s.buildSidebar(), s.detail))
	// 760 like the device window: the settings dialog (700) fits without scrolling.
	s.win.Resize(fyne.NewSize(1000, 760))
	// Master window: closing it quits the app, so a TrustPrompt dialog still open on it cannot
	// keep the process alive (the connector goroutine waits for the answer). Device windows are
	// closed first (the glfw driver runs onClosed before it quits) so that every KVM session
	// releases its held keys synchronously; the socket close runs on a goroutine and may not
	// finish before the process exits, which is harmless.
	s.quitApp = s.win.Close
	s.win.SetMaster()
	s.win.SetCloseIntercept(func() { s.requestClose(s.win) })
	s.win.SetOnClosed(func() {
		s.prober.Stop()
		if s.coffee != nil && s.coffee.timer != nil {
			s.coffee.timer.Stop()
		}
		s.stopUpdate()
		s.closeDevices()
	})
	a.Preferences().AddChangeListener(func() {
		// fsnotify may fire this off the Fyne thread (external edit of preferences.json).
		fyne.Do(func() {
			iv := s.settings.PollInterval()
			for _, d := range s.open {
				d.poller.SetInterval(iv)
			}
			s.prober.Wake()
		})
	})
	if setup != nil {
		setup(s)
	}
	s.reload()
	if len(s.items) > 0 {
		s.selectFavorite(s.items[0])
	}
	s.prober.Start()
	return s
}

func (s *StartWindow) Window() fyne.Window { return s.win }

// ---- Sidebar ----

func (s *StartWindow) buildSidebar() fyne.CanvasObject {
	s.search = widget.NewEntry()
	s.search.OnChanged = func(string) { s.refreshList() }
	s.list = container.New(layout.NewCustomPaddedVBoxLayout(2))
	s.newConnBtn = newManagerTextButton(icons.Add, lang.L("Quick Connect"), s.newConnection)
	s.settingsBtn = newManagerIconButton(icons.Settings, func() { s.showSettings() })

	searchBox := container.New(layout.NewCustomPaddedLayout(12, 10, 12, 12), newSearchField(s.search))
	heading := container.New(layout.NewCustomPaddedLayout(8, 6, 20, 20), sectionLabel(lang.L("Favorites")))
	top := container.NewVBox(searchBox, heading)
	listBox := container.NewVScroll(container.New(layout.NewCustomPaddedLayout(0, 10, 10, 10), s.list))
	footer := container.NewBorder(hairline(style.ColorNameCardBorder), nil, nil, nil,
		container.New(layout.NewCustomPaddedLayout(10, 10, 10, 10),
			container.NewBorder(nil, nil, nil, container.NewCenter(s.settingsBtn), s.newConnBtn)))
	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameSidebar, "")
	pane := container.NewStack(bg, container.NewBorder(top, footer, nil, nil, listBox))
	edge := hairline(style.ColorNameSeparatorDark)
	return container.New(sidebarLayout{}, container.New(fixedWidth{style.SidebarWidth - 1}, pane), edge)
}

// sidebarLayout places the first object at its minimum width on the left and the second
// right next to it, without the padding container.NewBorder would add.
type sidebarLayout struct{}

func (sidebarLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := objs[0].MinSize().Width
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(w, size.Height))
	objs[1].Move(fyne.NewPos(w, 0))
	objs[1].Resize(fyne.NewSize(size.Width-w, size.Height))
}

func (sidebarLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	a, b := objs[0].MinSize(), objs[1].MinSize()
	return fyne.NewSize(a.Width+b.Width, max(a.Height, b.Height))
}

// fixedWidth gives its objects a fixed width (the sidebar is not resizable).
type fixedWidth struct{ w float32 }

func (f fixedWidth) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (f fixedWidth) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(f.w, h)
}

// matches reports whether f fits the search text (name or host, case-insensitive).
func matches(f favorites.Favorite, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(f.DisplayName()), q) || strings.Contains(strings.ToLower(f.Host), q)
}

// refreshList rebuilds the sidebar rows from items and the search text.
func (s *StartWindow) refreshList() {
	s.shown = s.shown[:0]
	for _, f := range s.items {
		if matches(f, s.search.Text) {
			s.shown = append(s.shown, f)
		}
	}
	s.rows = map[string]*widgets.SidebarRow{}
	var objs []fyne.CanvasObject
	for _, f := range s.shown {
		k := deviceKey(f.Host, f.Username)
		r := widgets.NewSidebarRow(f.DisplayName(), f.Host)
		r.SetDot(s.dotOf(k))
		r.SetSelected(k == s.selKey)
		r.OnTapped = func() { s.selectFavorite(f) }
		r.OnDoubleTapped = func() { s.selectFavorite(f); s.connectFavorite(f) }
		s.rows[k] = r
		objs = append(objs, r)
	}
	if len(objs) == 0 {
		objs = []fyne.CanvasObject{s.emptyHint()}
	}
	s.list.Objects = objs
	s.list.Refresh()
}

// emptyHint is the sidebar's empty state: no favorites at all, or none matching the search.
func (s *StartWindow) emptyHint() fyne.CanvasObject {
	title, text := lang.L("No favorites yet"), lang.L("Save a connection as a favorite to find it here.")
	if len(s.items) > 0 {
		title, text = lang.L("No matches"), lang.L("No favorite has this name or host.")
	}
	t := widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	m := widget.NewLabelWithStyle(text, fyne.TextAlignCenter, fyne.TextStyle{})
	m.Wrapping = fyne.TextWrapWord
	m.SizeName = theme.SizeNameCaptionText // 11.5 in style.Theme
	m.Importance = widget.LowImportance
	return container.New(layout.NewCustomPaddedLayout(24, 0, 0, 0), container.NewVBox(t, m))
}

func (s *StartWindow) dotOf(key string) widgets.DotState {
	if r, ok := s.probes[key]; ok {
		return r.dot
	}
	return widgets.DotUnknown
}

// applyProbe shows a background result. While the favorite's device window is open its poller
// reports the status (windowStatus), so a probe that was already running when the window
// opened is dropped.
func (s *StartWindow) applyProbe(key string, r probeResult) {
	if _, ok := s.open[key]; ok {
		return
	}
	s.setStatus(key, r)
}

// windowStatus shows a status read of the open device window of key.
func (s *StartWindow) windowStatus(key string, st amt.Status, known bool) {
	r := probeResult{dot: widgets.DotUnknown}
	if known {
		r = readProbe(st)
	}
	s.setStatus(key, r)
}

// setStatus shows the status of a favorite: the dot of the row, and the metadata line when
// the favorite is selected.
func (s *StartWindow) setStatus(key string, r probeResult) {
	if _, ok := s.find(key); !ok {
		return // deleted or renamed meanwhile
	}
	s.probes[key] = r
	if row, ok := s.rows[key]; ok {
		row.SetDot(r.dot)
	}
	if key == s.selKey {
		s.refreshMeta()
		s.refreshStatusRow()
	}
}

// statusRowGap is the space between the dot and the text of the status row.
const statusRowGap float32 = 7

// statusLineLayout lays out the status row of the detail card, objs being the dot and the label:
// the dot starts where the text of the value labels below starts (a label draws its text
// inside its inner padding), the text follows statusRowGap after the dot. The label is moved
// left by its inner padding for that, and takes the rest of the width.
type statusLineLayout struct{}

func (statusLineLayout) inset() float32 { return theme.InnerPadding() }

func (l statusLineLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	dot, text := objs[0], objs[1]
	pad := l.inset()
	ds, ts := dot.MinSize(), text.MinSize()
	dot.Resize(ds)
	dot.Move(fyne.NewPos(pad, (size.Height-ds.Height)/2))
	x := ds.Width + statusRowGap // pad + dot + gap, less the label's own padding
	text.Resize(fyne.NewSize(max(0, size.Width-x), ts.Height))
	text.Move(fyne.NewPos(x, (size.Height-ts.Height)/2))
}

func (l statusLineLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	ds, ts := objs[0].MinSize(), objs[1].MinSize()
	return fyne.NewSize(max(l.inset()+ds.Width, ds.Width+statusRowGap+ts.Width), max(ds.Height, ts.Height))
}

// powerStatusText is the text of the status row for a dot state: the same result as the dot.
func powerStatusText(dot widgets.DotState) string {
	switch dot {
	case widgets.DotOn:
		return lang.L("Powered on")
	case widgets.DotAsleep:
		return lang.L("Standby")
	case widgets.DotOff:
		return lang.L("Powered off")
	}
	return lang.L("Unknown")
}

// refreshStatusRow shows the selected favorite's last result in the status row of the detail
// card, from the same probe result as its sidebar dot. Until the first result is there the row
// stays empty, no dot and no text, instead of showing "Unknown" while the device is still being
// read; the sidebar keeps its gray ring meanwhile. A favorite that cannot be read (no TLS, no
// password, no pin) gets its neutral result in the first probe round without waiting for a
// probe slot (statusProber.round), so "Unknown" appears only when reading is not possible or
// failed.
func (s *StartWindow) refreshStatusRow() {
	if s.statusDot == nil {
		s.statusDot = widgets.NewStatusDot(widgets.DotNone, style.StatusDotSize)
		s.statusText = widget.NewLabel("")
		s.statusText.Truncation = fyne.TextTruncateEllipsis
		s.statusLine = container.New(statusLineLayout{}, s.statusDot, s.statusText)
	}
	r, ok := s.probes[s.selKey]
	if !ok {
		s.statusDot.SetState(widgets.DotNone)
		s.statusText.SetText("")
		return
	}
	s.statusDot.SetState(r.dot)
	s.statusText.SetText(powerStatusText(r.dot))
}

func (s *StartWindow) find(key string) (favorites.Favorite, bool) {
	for _, f := range s.items {
		if deviceKey(f.Host, f.Username) == key {
			return f, true
		}
	}
	return favorites.Favorite{}, false
}

// selected returns the selected favorite.
func (s *StartWindow) selected() (favorites.Favorite, bool) {
	if s.selKey == "" {
		return favorites.Favorite{}, false
	}
	return s.find(s.selKey)
}

func (s *StartWindow) markSelection() {
	for k, r := range s.rows {
		r.SetSelected(k == s.selKey)
	}
}

// selectFavorite shows f read only; an edit in progress is discarded.
func (s *StartWindow) selectFavorite(f favorites.Favorite) {
	k := deviceKey(f.Host, f.Username)
	s.selKey, s.dupOf = k, nil
	s.markSelection()
	s.setMode(modeView)
	if _, known := s.saved[k]; known { // no keychain round trip (a process on macOS) per click
		return
	}
	s.refreshSaved(f.Host, f.Username)
}

// refreshSaved looks the keychain up again and stores the result; the detail follows if the
// favorite is still selected. An answer that is overtaken by a newer lookup or by
// forgetSaved (the keychain changed meanwhile) is dropped.
func (s *StartWindow) refreshSaved(host, user string) {
	k := deviceKey(host, user)
	s.savedGen[k]++
	gen := s.savedGen[k]
	s.lookupPassword(host, user, func(saved bool) {
		if s.savedGen[k] != gen {
			return
		}
		s.saved[k] = saved
		if s.selKey == k {
			s.showDetail()
		}
	})
}

// forgetSaved drops the cached keychain state of a device after the keychain changed and
// invalidates lookups that are still running.
func (s *StartWindow) forgetSaved(host, user string) {
	k := deviceKey(host, user)
	delete(s.saved, k)
	s.savedGen[k]++
}

// lookupPasswordAsync checks on a goroutine whether the keychain holds a password; the value
// itself is dropped at once and never reaches a field.
func (s *StartWindow) lookupPasswordAsync(host, user string, answer func(bool)) {
	go func() {
		pw, err := s.keys.Get(host, user)
		saved := err == nil && pw != ""
		fyne.Do(func() { answer(saved) })
	}()
}

// newConnection shows the empty form.
func (s *StartWindow) newConnection() {
	s.selKey, s.dupOf = "", nil
	s.markSelection()
	s.resetForm()
	s.setMode(modeNew)
	s.win.Canvas().Focus(s.host)
}

func (s *StartWindow) resetForm() {
	s.host.SetText("")
	s.name.SetText("")
	s.user.SetText("admin")
	s.password.SetText("")
	s.setPasswordPlaceholder("")
	s.useTLS.SetChecked(true)
	s.savePassword.SetChecked(true)
}

// duplicate opens the form prefilled with f; saving creates a new favorite (host or user must
// differ, the original is never replaced). The password is not copied.
func (s *StartWindow) duplicate(f favorites.Favorite) {
	s.selKey = ""
	s.markSelection()
	s.resetForm()
	s.host.SetText(f.Host)
	s.name.SetText(lang.L("{{.Name}} copy", map[string]any{"Name": f.DisplayName()}))
	s.user.SetText(f.Username)
	s.useTLS.SetChecked(f.UseTLS)
	src := f
	s.dupOf = &src
	s.setMode(modeNew)
}

// ---- Detail pane ----

func (s *StartWindow) buildDetail() {
	s.editBtn = newManagerIconButton(icons.Edit, s.toggleEdit)
	s.dupBtn = newManagerIconButton(icons.Duplicate, func() {
		if f, ok := s.selected(); ok {
			s.duplicate(f)
		}
	})
	s.delBtn = newManagerIconButton(icons.Delete, func() {
		if f, ok := s.selected(); ok {
			s.showDelete(f)
		}
	})
	tools := container.New(layout.NewCustomPaddedHBoxLayout(4), s.editBtn, s.dupBtn, s.delBtn)
	toolbar := container.New(&columnLayout{fill: -1},
		container.New(&fixedHeight{h: style.FormRowHeight - 1},
			container.New(layout.NewCustomPaddedLayout(0, 0, 14, 14), container.NewBorder(nil, nil, nil, container.NewCenter(tools)))),
		hairline(style.ColorNameCardBorder))

	s.host = widget.NewEntry()
	s.host.SetPlaceHolder("device.example.com")
	s.host.OnChanged = func(string) { s.refreshTitle() }
	s.name = widget.NewEntry()
	s.name.OnChanged = func(string) { s.refreshTitle() }
	s.user = widget.NewEntry()
	s.user.SetText("admin")
	s.password = widget.NewPasswordEntry()
	s.passwordEye = s.password.ActionItem
	s.password.OnChanged = func(string) { s.refreshPasswordEye() }
	s.password.OnSubmitted = func(string) {
		switch s.mode {
		case modeNew:
			if s.dupOf != nil {
				s.saveFromForm()
				return
			}
			s.connectFromForm()
		case modeEdit:
			s.saveEdit()
		}
	}
	s.useTLS = widgets.NewSwitch(nil)
	s.useTLS.SetChecked(true)
	s.savePassword = widgets.NewSwitch(nil)
	s.savePassword.SetChecked(true)
	s.resetFP = widgets.NewSwitch(nil)

	s.connectBtn = widgets.NewPrimaryButton(lang.L("Connect"), icons.GetStroke(icons.Connect, icons.White, 2), s.connectSelectedOrForm)
	s.connectBtn.IconTrailing = true
	s.saveBtn = widget.NewButton(lang.L("Save as favorite"), s.saveFromForm)
	s.editSaveBtn = widget.NewButton(lang.L("Save"), func() {
		if s.dupOf != nil {
			s.saveFromForm()
			return
		}
		s.saveEdit()
	})
	s.editSaveBtn.Importance = widget.HighImportance
	s.editCancelBtn = widget.NewButton(lang.L("Cancel"), func() {
		if s.dupOf != nil { // Cancel of a copy returns to the original
			s.selectFavorite(*s.dupOf)
			return
		}
		s.setMode(modeView)
	})
	s.cancelBtn = widget.NewButton(lang.L("Cancel"), s.conn.cancelAll)
	s.status = widget.NewLabel("")
	s.status.SizeName = theme.SizeNameCaptionText
	s.activity = widget.NewActivity()
	s.busyRow = container.New(layout.NewCustomPaddedHBoxLayout(6), s.activity, s.status)
	s.actions = container.New(layout.NewCustomPaddedHBoxLayout(8))

	tileBg := style.BindRect(&canvas.Rectangle{CornerRadius: style.DetailTileRadius, StrokeWidth: 1},
		style.ColorNameDetailTile, style.ColorNameTileBorder)
	tileBg.SetMinSize(fyne.NewSquareSize(style.DetailTileSize))
	tileIcon := widgets.NewSymbol(icons.GetStroke(icons.Server, icons.Accent, 1.5), 32)
	tile := container.NewStack(tileBg, container.NewCenter(tileIcon))

	// ManagerA.html: name (22 px, bold) and metadata line 4 px apart, centred next to the tile;
	// tile, heading and buttons 18 px apart.
	s.title = newEllipsisText("", style.Size(style.SizeNameDetailHeading), theme.ColorNameForeground, true)
	s.title.shrink = true
	s.meta = container.New(layout.NewCustomPaddedHBoxLayout(8))
	heading := container.New(&vCenterLayout{gap: headingGap}, s.title, container.NewStack(s.meta, s.busyRow))
	header := container.New(&headerLayout{gap: headerGap}, tile, heading, s.actions)

	s.card1, s.card2, s.card3 = widgets.NewFormCard(), widgets.NewFormCard(), widgets.NewFormCard()
	s.advanced = container.New(layout.NewCustomPaddedVBoxLayout(6),
		container.New(layout.NewCustomPaddedLayout(0, 0, 14, 0), sectionLabel(lang.L("Advanced"))), s.card3)
	s.cards = container.New(&detailLayout{}, header, s.card1, s.card2, s.advanced)
	s.scroller = container.NewVScroll(s.cards)
	s.detail = container.New(&columnLayout{fill: 1}, toolbar, s.scroller) // no padding below the toolbar
	s.setMode(modeNew)
}

// Spacing of the detail pane (ManagerA.html): 32 px above and below, 48 px at the sides, 24 px
// between header and cards. The design has no "Advanced" card; so that header and all cards
// still fit the default window (1000×660) without scrolling, the vertical spacing shrinks
// evenly down to detailMinPad and detailMinGap when the pane is too low. Only below that the
// pane scrolls.
const (
	detailPad    float32 = 32
	detailPadH   float32 = 48
	detailGap    float32 = 24
	detailMinPad float32 = 16
	detailMinGap float32 = 12
	headingGap   float32 = 4
	headerGap    float32 = 18
)

// detailLayout stacks the header and the cards of the detail pane with the design spacing,
// or less when the height is short (see detailPad); hidden objects take no space.
type detailLayout struct{}

func (detailLayout) visible(objs []fyne.CanvasObject) (vis []fyne.CanvasObject, content float32, width float32) {
	for _, o := range objs {
		if o.Visible() {
			m := o.MinSize()
			vis = append(vis, o)
			content += m.Height
			width = max(width, m.Width)
		}
	}
	return vis, content, width
}

func (l detailLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	vis, content, width := l.visible(objs)
	gaps := float32(max(0, len(vis)-1))
	return fyne.NewSize(width+2*detailPadH, content+2*detailMinPad+gaps*detailMinGap)
}

func (l detailLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	vis, content, _ := l.visible(objs)
	gaps := float32(max(0, len(vis)-1))
	full := 2*detailPad + gaps*detailGap // spacing of the design
	least := 2*detailMinPad + gaps*detailMinGap
	f := float32(1)
	if room := size.Height - content; room < full {
		f = max(0, (room-least)/(full-least))
	}
	pad := detailMinPad + f*(detailPad-detailMinPad)
	gap := detailMinGap + f*(detailGap-detailMinGap)
	y := pad
	for _, o := range vis {
		h := o.MinSize().Height
		o.Move(fyne.NewPos(detailPadH, y))
		o.Resize(fyne.NewSize(size.Width-2*detailPadH, h))
		y += h + gap
	}
}

// headerLayout places the tile left and the buttons right at their minimum size and gives the
// heading the width between them, gap apart; all vertically centred.
type headerLayout struct{ gap float32 }

func (l *headerLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	a, b, c := objs[0].MinSize(), objs[1].MinSize(), objs[2].MinSize()
	return fyne.NewSize(a.Width+b.Width+c.Width+2*l.gap, max(a.Height, b.Height, c.Height))
}

func (l *headerLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	place := func(o fyne.CanvasObject, x, w float32) {
		h := o.MinSize().Height
		o.Move(fyne.NewPos(x, (size.Height-h)/2))
		o.Resize(fyne.NewSize(w, h))
	}
	tw, aw := objs[0].MinSize().Width, objs[2].MinSize().Width
	place(objs[0], 0, tw)
	place(objs[2], size.Width-aw, aw)
	place(objs[1], tw+l.gap, max(0, size.Width-tw-aw-2*l.gap))
}

// fixedHeight gives its objects a fixed height.
type fixedHeight struct{ h float32 }

func (f *fixedHeight) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (f *fixedHeight) MinSize(objs []fyne.CanvasObject) fyne.Size {
	w := float32(0)
	for _, o := range objs {
		w = max(w, o.MinSize().Width)
	}
	return fyne.NewSize(w, f.h)
}

// setMode switches the detail pane; entering modeEdit loads the selected favorite into the fields.
func (s *StartWindow) setMode(m detailMode) {
	if m == modeEdit {
		f, ok := s.selected()
		if !ok {
			return
		}
		s.host.SetText(f.Host)
		s.name.SetText(f.Name)
		s.user.SetText(f.Username)
		s.password.SetText("") // never loaded from the keychain
		saved := s.saved[s.selKey]
		if saved {
			s.setPasswordPlaceholder(lang.L("Saved in keychain"))
		} else {
			s.setPasswordPlaceholder("")
		}
		s.useTLS.SetChecked(f.UseTLS)
		s.savePassword.SetChecked(saved)
		s.resetFP.SetChecked(false)
		s.layoutSel = newLayoutSelect(lang.L("Default"), f.Layout)
		s.editLayout = layoutChoiceOf(f.Layout)
		s.qualitySel = newQualitySelect(f.QualityChoice())
		s.editQuality = f.QualityChoice()
		s.autoSel = newAutoConnectSelect(f.AutoConnectChoice())
		s.editAuto = f.AutoConnectChoice()
	}
	if m != modeNew {
		s.dupOf = nil
	}
	s.mode = m
	s.showDetail()
}

// deviceOpen reports whether the device window of the selected favorite is open.
func (s *StartWindow) deviceOpen() bool {
	_, ok := s.open[s.selKey]
	return ok
}

// relockEdit re-evaluates the host and user lock when a device window opens or closes while
// edit mode is showing.
func (s *StartWindow) relockEdit() {
	if s.mode == modeEdit {
		s.showDetail()
	}
}

func (s *StartWindow) toggleEdit() {
	if s.mode == modeEdit {
		s.setMode(modeView)
		return
	}
	if _, ok := s.selected(); ok {
		s.setMode(modeEdit)
	}
}

// value returns the read-only label of a card row, created once per row name.
func (s *StartWindow) value(name, text string) *widget.Label {
	l, ok := s.values[name]
	if !ok {
		l = widget.NewLabel("")
		l.Truncation = fyne.TextTruncateEllipsis
		s.values[name] = l
	}
	l.SetText(text)
	return l
}

// showDetail lays out header, cards and buttons for the current mode.
func (s *StartWindow) showDetail() {
	f, haveFav := s.selected()
	for _, b := range []*managerButton{s.editBtn, s.dupBtn, s.delBtn} {
		if haveFav {
			b.Enable()
		} else {
			b.Disable()
		}
	}
	s.editBtn.SetActive(s.mode == modeEdit)
	s.refreshTitle()
	s.refreshPasswordEye()

	switch s.mode {
	case modeNew:
		s.host.Enable()
		s.user.Enable()
		s.useTLS.Enable()
		s.card1.SetRows(
			widgets.NewFormRow(lang.L("Host/IP"), s.host),
			widgets.NewFormRow(lang.L("Name"), s.name),
			widgets.NewFormRow(lang.L("Username"), s.user),
			widgets.NewFormRow(lang.L("Password"), s.password),
		)
		s.advanced.Hide()
	case modeView:
		saved := s.saved[s.selKey]
		pw := lang.L("Not saved")
		if saved {
			pw = lang.L("Saved in keychain")
		}
		s.useTLS.SetChecked(f.UseTLS)
		s.savePassword.SetChecked(saved)
		s.useTLS.Disable()
		s.savePassword.Disable()
		s.refreshStatusRow()
		s.card1.SetRows(
			widgets.NewFormRow(lang.L("Status"), s.statusLine),
			widgets.NewFormRow(lang.L("Host/IP"), s.value("host", f.Host)),
			widgets.NewFormRow(lang.L("Name"), s.value("name", f.Name)),
			widgets.NewFormRow(lang.L("Username"), s.value("user", f.Username)),
			widgets.NewFormRow(lang.L("Password"), s.value("password", pw)),
		)
		s.card3.SetRows(
			widgets.NewFormRow(lang.L("Keyboard"), s.value("layout", layoutText(f.Layout))),
			widgets.NewFormRow(lang.L("KVM quality"), s.value("quality", qualityText(f.QualityChoice()))),
			widgets.NewFormRow(lang.L("KVM auto-connect"), s.value("autoconnect", autoConnectText(f.AutoConnectChoice()))),
		)
		s.advanced.Show()
	case modeEdit:
		s.useTLS.Enable()
		s.savePassword.Enable()
		rows := []fyne.CanvasObject{
			widgets.NewFormRow(lang.L("Host/IP"), s.host),
			widgets.NewFormRow(lang.L("Name"), s.name),
			widgets.NewFormRow(lang.L("Username"), s.user),
			widgets.NewFormRow(lang.L("Password"), s.password),
		}
		if s.deviceOpen() {
			// The open device window is registered under host and user: changing them would
			// orphan it (lost quality choice, double probe, second session on double click).
			s.host.Disable()
			s.user.Disable()
			rows = append(rows, widgets.NewIconRow(icons.Lock, lang.L("Close the device window to change host and username."), nil))
		} else {
			s.host.Enable()
			s.user.Enable()
		}
		s.card1.SetRows(rows...)
		s.card3.SetRows(
			widgets.NewFormRow(lang.L("Keyboard"), s.layoutSel),
			widgets.NewFormRow(lang.L("KVM quality"), s.qualitySel),
			widgets.NewFormRow(lang.L("KVM auto-connect"), s.autoSel),
			widgets.NewIconRow(icons.Lock, lang.L("Reset fingerprint"), s.resetFP),
		)
		s.advanced.Show()
	}
	// Quick Connect has no "Save password" switch: Save as favorite stores an entered password.
	rows2 := []fyne.CanvasObject{widgets.NewIconRow(icons.Lock, lang.L("Use TLS"), s.useTLS)}
	if s.mode != modeNew {
		rows2 = append(rows2, widgets.NewIconRow(icons.Key, lang.L("Save password in keychain"), s.savePassword))
	}
	s.card2.SetRows(rows2...)
	s.refreshMeta()
	s.refreshActions()
	s.cards.Refresh()
}

// refreshTitle sets the heading: in the forms it mirrors the name, else the host, else
// "Quick Connect" and follows the typing; the read-only view shows the favorite's name.
func (s *StartWindow) refreshTitle() {
	if s.title == nil {
		return
	}
	if s.mode == modeView {
		if f, ok := s.selected(); ok {
			s.title.SetText(f.DisplayName())
		}
		return
	}
	switch {
	case strings.TrimSpace(s.name.Text) != "":
		s.title.SetText(strings.TrimSpace(s.name.Text))
	case strings.TrimSpace(s.host.Text) != "":
		s.title.SetText(strings.TrimSpace(s.host.Text))
	default:
		s.title.SetText(lang.L("Quick Connect"))
	}
}

// setPasswordPlaceholder sets the hint of the password field ("" for none).
func (s *StartWindow) setPasswordPlaceholder(t string) {
	s.password.SetPlaceHolder(t)
	s.refreshPasswordEye()
}

// refreshPasswordEye hides the eye while the field is empty and the password sits in the
// keychain (placeholder shown); once something is typed it is there again.
func (s *StartWindow) refreshPasswordEye() {
	if s.passwordEye == nil {
		return
	}
	if s.password.Text == "" && s.password.PlaceHolder != "" {
		if !s.password.Password { // back to hidden text before the eye goes
			s.password.Password = true
			s.password.Refresh()
		}
		s.passwordEye.Hide()
	} else {
		s.passwordEye.Show()
	}
}

// layoutText names a favorite's layout override for the read-only view.
func layoutText(l string) string {
	if v, ok := typing.ParseLayout(l); ok {
		return layoutLabel(v)
	}
	return lang.L("Default")
}

// autoConnectChoices is the order of the auto-connect select: global setting, on, off.
var autoConnectChoices = []string{"", favorites.AutoConnectOn, favorites.AutoConnectOff}

func autoConnectText(choice string) string {
	switch choice {
	case favorites.AutoConnectOn:
		return lang.L("On")
	case favorites.AutoConnectOff:
		return lang.L("Off")
	}
	return lang.L("Default")
}

func newAutoConnectSelect(current string) *widget.Select {
	var options []string
	for _, c := range autoConnectChoices {
		options = append(options, autoConnectText(c))
	}
	sel := widget.NewSelect(options, nil)
	sel.SetSelected(autoConnectText(current))
	return sel
}

// selectedAutoConnect returns the override of a newAutoConnectSelect, empty for the global setting.
func selectedAutoConnect(sel *widget.Select) string {
	return autoConnectChoices[max(0, sel.SelectedIndex())]
}

// qualityChoices is the order of the quality select of a favorite: global setting, High, Low.
var qualityChoices = []string{"", favorites.QualityHigh, favorites.QualityLow}

// qualityText names a stored quality; empty (the global setting) reads "Default".
func qualityText(q string) string {
	switch q {
	case favorites.QualityLow:
		return lang.L("Low")
	case favorites.QualityHigh:
		return lang.L("High")
	}
	return lang.L("Default")
}

func newQualitySelect(current string) *widget.Select {
	var options []string
	for _, c := range qualityChoices {
		options = append(options, qualityText(c))
	}
	sel := widget.NewSelect(options, nil)
	sel.SetSelected(qualityText(current))
	return sel
}

// selectedQuality returns the override of a newQualitySelect, empty for the global setting.
func selectedQuality(sel *widget.Select) string {
	return qualityChoices[max(0, sel.SelectedIndex())]
}

// refreshMeta fills the line under the heading with what the background probe knows (AMT
// version, control mode) and the TLS setting; it is hidden when there is nothing to show.
func (s *StartWindow) refreshMeta() {
	var parts []fyne.CanvasObject
	text := func(t string) *canvas.Text {
		c := style.BindText(canvas.NewText(t, nil), style.ColorNameTextSecondary)
		c.TextSize = style.Size(style.SizeNameMetadataText)
		return c
	}
	add := func(o fyne.CanvasObject) {
		if len(parts) > 0 {
			parts = append(parts, text("·"))
		}
		parts = append(parts, o)
	}
	if f, ok := s.selected(); ok && s.mode != modeNew {
		if r, ok := s.probes[s.selKey]; ok && r.known() {
			if r.st.AMTVersion != "" {
				add(text("AMT " + r.st.AMTVersion))
			}
			if r.st.ControlMode == amt.ControlModeAdmin || r.st.ControlMode == amt.ControlModeClient {
				add(text(string(r.st.ControlMode)))
			}
		}
		if f.UseTLS {
			lock := widgets.NewSymbol(icons.Get(icons.Lock, icons.Secondary), 13)
			add(container.New(layout.NewCustomPaddedHBoxLayout(4), container.NewCenter(lock), text(lang.L("TLS"))))
		} else {
			add(text(lang.L("Without TLS")))
		}
	}
	s.meta.Objects = parts
	if len(parts) == 0 || len(s.busy) > 0 {
		s.meta.Hide()
	} else {
		s.meta.Show()
	}
	s.meta.Refresh()
}

// refreshActions shows the header buttons of the mode and the connecting state: one attempt at
// a time, so Connect is disabled and replaced by "Connecting to …" and Cancel while one runs.
func (s *StartWindow) refreshActions() {
	connecting := len(s.busy) > 0
	var objs []fyne.CanvasObject
	if connecting {
		for _, name := range s.busy {
			s.status.SetText(lang.L("Connecting to {{.Name}}", map[string]any{"Name": name}))
			break
		}
		s.activity.Start()
		s.activity.Show()
		s.status.Show()
		s.busyRow.Show()
		objs = append(objs, s.cancelBtn)
		s.connectBtn.Disable()
	} else {
		s.status.SetText("")
		s.activity.Stop()
		s.activity.Hide()
		s.status.Hide()
		s.busyRow.Hide()
		s.connectBtn.Enable()
	}
	s.refreshMeta()
	switch s.mode {
	case modeNew:
		if s.dupOf != nil {
			objs = append(objs, s.editCancelBtn, s.editSaveBtn)
			break
		}
		objs = append(objs, s.saveBtn)
		if !connecting {
			objs = append(objs, s.connectBtn)
		}
	case modeView:
		if !connecting {
			objs = append(objs, s.connectBtn)
		}
	case modeEdit:
		objs = append(objs, s.editCancelBtn, s.editSaveBtn)
	}
	for _, o := range []fyne.CanvasObject{s.cancelBtn, s.saveBtn, s.connectBtn, s.editCancelBtn, s.editSaveBtn} {
		o.Hide()
	}
	for _, o := range objs {
		o.Show()
	}
	s.actions.Objects = objs
	s.actions.Refresh()
}

// reload reads the favorites file, keeps the selection if the favorite still exists and
// otherwise shows the "New Connection" form.
func (s *StartWindow) reload() {
	list, err := s.mgr.List()
	if err != nil {
		s.onError(err)
		list = nil
	}
	s.items = list
	keep := map[string]bool{}
	for _, f := range list {
		keep[deviceKey(f.Host, f.Username)] = true
	}
	for k := range s.probes {
		if !keep[k] {
			delete(s.probes, k)
		}
	}
	s.prober.SetFavorites(list)
	s.followFile()
	if s.selKey != "" && !keep[s.selKey] {
		s.selKey = ""
		s.resetForm()
		s.setMode(modeNew)
	} else {
		s.showDetail()
	}
	s.refreshList()
	if s.onReloaded != nil {
		s.onReloaded()
	}
}

// followFile lets open edit mode show what the file now holds for the fields the device window
// can change (Keyboard menu): a field the user has not touched since it was loaded follows the
// file, a touched one keeps the user's value.
func (s *StartWindow) followFile() {
	if s.mode != modeEdit {
		return
	}
	f, ok := s.selected()
	if !ok {
		return
	}
	if layout := layoutChoiceOf(f.Layout); selectedLayout(s.layoutSel) == s.editLayout && layout != s.editLayout {
		s.layoutSel.SetSelected(layoutText(layout))
		s.editLayout = layout
	}
	if q := f.QualityChoice(); selectedQuality(s.qualitySel) == s.editQuality && q != s.editQuality {
		s.qualitySel.SetSelected(qualityText(q))
		s.editQuality = q
	}
	if auto := f.AutoConnectChoice(); selectedAutoConnect(s.autoSel) == s.editAuto && auto != s.editAuto {
		s.autoSel.SetSelected(autoConnectText(auto))
		s.editAuto = auto
	}
}

func (s *StartWindow) connectSelectedOrForm() {
	if f, ok := s.selected(); ok && s.mode == modeView {
		s.connectFavorite(f)
		return
	}
	s.connectFromForm()
}

func (s *StartWindow) connectFromForm() {
	s.begin(connectRequest{
		cfg:  amt.Config{Host: s.host.Text, Username: s.user.Text, Password: s.password.Text, UseTLS: s.useTLS.Checked()},
		name: s.name.Text,
	})
}

// saveFromForm stores the form as a favorite without connecting; the password goes into the
// keychain when one was entered. An existing favorite with the same
// host and user is updated, except after Duplicate. The fingerprint is pinned on the first TLS
// connection. The saved favorite is selected afterwards.
func (s *StartWindow) saveFromForm() {
	host, err := normalizeHost(s.host.Text)
	if err != nil {
		s.onError(err)
		return
	}
	user := strings.TrimSpace(s.user.Text)
	if user == "" {
		s.onError(userError(lang.L("Username is required.")))
		return
	}
	f := favorites.Favorite{Name: strings.TrimSpace(s.name.Text), Host: host, Username: user, UseTLS: s.useTLS.Checked()}
	createOnly := s.dupOf != nil
	if createOnly {
		f.Layout, f.Quality, f.AutoConnect = s.dupOf.Layout, s.dupOf.Quality, s.dupOf.AutoConnect
	}
	password := s.password.Text // the form has no switch: an entered password is saved
	s.async(func() error {
		if createOnly {
			if _, found, err := s.mgr.Find(host, user); err != nil {
				return err
			} else if found {
				return favorites.ErrExists
			}
		}
		if err := s.mgr.Upsert(f); err != nil {
			return err
		}
		if password != "" {
			if err := s.keys.Set(host, user, password); err != nil {
				return &favorites.KeychainError{Err: err}
			}
		}
		return nil
	}, func(err error) {
		if favoriteWritten(err) {
			s.password.SetText("")
			s.selKey = deviceKey(host, user)
			s.dupOf = nil
			s.mode = modeView
			s.markSelection()
			s.refreshSaved(host, user)
		}
	})
}

// favoriteWritten reports whether the favorites file was written: no error, or only the
// keychain step after it failed.
func favoriteWritten(err error) bool {
	var ke *favorites.KeychainError
	return err == nil || errors.As(err, &ke)
}

// saveEdit applies edit mode: favorite (name, host, user, TLS, layout, a changed quality,
// fingerprint reset) and password. A new password replaces the saved one when "Save password" is on;
// turning the switch off deletes the saved password.
func (s *StartWindow) saveEdit() {
	f, ok := s.selected()
	if !ok || s.mode != modeEdit {
		return
	}
	host, user := s.host.Text, s.user.Text
	if s.deviceOpen() { // locked while the device window is open
		host, user = f.Host, f.Username
	}
	edited := favorites.Favorite{Name: s.name.Text, Host: host, Username: user, UseTLS: s.useTLS.Checked(),
		Layout: selectedLayout(s.layoutSel), Quality: selectedQuality(s.qualitySel), AutoConnect: selectedAutoConnect(s.autoSel)}
	pw := passwordChange{keep: true}
	switch {
	case s.savePassword.Checked() && s.password.Text != "":
		pw = passwordChange{set: s.password.Text}
	case !s.savePassword.Checked() && s.saved[s.selKey]:
		pw = passwordChange{remove: true}
	}
	s.edit(f, edited, s.resetFP.Checked(), pw)
}

// passwordChange is what saving edit mode does with the keychain password.
type passwordChange struct {
	keep   bool
	set    string
	remove bool
}

func (s *StartWindow) connectFavorite(f favorites.Favorite) {
	s.begin(connectRequest{cfg: amt.Config{Host: f.Host, Username: f.Username, UseTLS: f.UseTLS}, name: f.Name})
}

// begin starts one attempt at a time: an open device window is focused instead,
// a running attempt is left alone.
func (s *StartWindow) begin(req connectRequest) {
	host := strings.ToLower(strings.TrimSpace(req.cfg.Host))
	user := strings.TrimSpace(req.cfg.Username)
	k := deviceKey(host, user)
	if d, ok := s.open[k]; ok {
		d.win.RequestFocus()
		return
	}
	// One attempt at a time, like the disabled buttons: Enter and double-click must not start a second one.
	if len(s.busy) > 0 {
		return
	}
	name := strings.TrimSpace(req.name)
	if name == "" {
		name = host
	}
	s.busy[k] = name
	s.refreshActions()
	if !s.conn.start(req) {
		s.end(host, user)
	}
}

func (s *StartWindow) end(host, user string) {
	delete(s.busy, deviceKey(host, user))
	s.refreshActions()
}

// addDevice builds the device window and registers it until it is closed. While it is open its
// poller drives the status dot of the favorite and the background probe skips it; afterwards
// the probe takes over again with its own rules.
func (s *StartWindow) addDevice(sess *session.Session, st amt.Status, name string) *DeviceWindow {
	k := deviceKey(sess.Config().Host, sess.Config().Username)
	var d *DeviceWindow
	// Only remove the entry while it still points at this window: after an abort the key may
	// already belong to a newer window.
	d = NewDeviceWindow(s.app, sess, name, st, s.settings, func() {
		if s.open[k] == d {
			delete(s.open, k)
			s.prober.SetSkip(k, false)
			s.relockEdit()
			if d.aborted { // certificate mismatch: the last read no longer counts
				s.setStatus(k, probeResult{dot: widgets.DotUnknown})
			}
			// The window's last read must not stay up for a whole interval (favorites without
			// TLS or password turn neutral only through the probe).
			s.prober.Wake()
		}
	})
	d.layouts = s.mgr
	d.exitWork = s.exitWork
	d.mainWin = s.win
	if s.mgr != nil { // a typed nil would count as a store
		d.loginStore = s.mgr
	}
	d.onFavoriteChanged = s.reload // the read-only view and edit mode show the stored quality
	d.onStatus = func(st amt.Status, known bool) {
		if s.open[k] == d { // a late read of a closed window does not count
			s.windowStatus(k, st, known)
		}
	}
	d.win.SetCloseIntercept(func() { s.requestClose(d.win) })
	s.open[k] = d
	s.prober.SetSkip(k, true)
	s.relockEdit()
	s.windowStatus(k, st, true) // the status read by the connect
	return d
}

func (s *StartWindow) showDevice(sess *session.Session, st amt.Status, name string) {
	s.addDevice(sess, st, name).Show()
}

// closeDevices closes every open device window (each stops its poller and KVM session on a
// goroutine, so quitting never blocks on a running power action).
func (s *StartWindow) closeDevices() {
	for _, d := range s.open {
		d.win.Close()
	}
}

// closeAll stops the background probe and every poller and closes the device windows. Only
// tests call it.
func (s *StartWindow) closeAll() {
	s.prober.Stop()
	for _, d := range s.open {
		d.poller.Stop()
		d.win.Close()
	}
}

// edit validates on the Fyne thread and runs the file and keychain work on a goroutine; the
// list is reloaded afterwards, also after an error. On success the edited favorite stays
// selected in view mode; after an error edit mode stays open.
func (s *StartWindow) edit(f, edited favorites.Favorite, resetFingerprint bool, pw passwordChange) {
	host, err := normalizeHost(edited.Host)
	if err != nil {
		s.onError(err)
		return
	}
	edited.Host = host
	edited.Username = strings.TrimSpace(edited.Username)
	edited.Name = strings.TrimSpace(edited.Name)
	if edited.Username == "" {
		s.onError(userError(lang.L("Username is required.")))
		return
	}
	newKey := deviceKey(edited.Host, edited.Username)
	s.async(func() error {
		if err := s.mgr.Edit(f.Host, f.Username, edited, resetFingerprint); err != nil {
			return err
		}
		switch {
		case pw.set != "":
			if err := s.keys.Set(edited.Host, edited.Username, pw.set); err != nil {
				return &favorites.KeychainError{Err: err}
			}
		case pw.remove:
			if err := s.keys.Delete(edited.Host, edited.Username); err != nil && !errors.Is(err, keychain.ErrNotFound) {
				return &favorites.KeychainError{Err: err}
			}
		}
		return nil
	}, func(err error) {
		if !favoriteWritten(err) {
			return // nothing written: edit mode stays open
		}
		s.password.SetText("")
		if d, ok := s.open[deviceKey(f.Host, f.Username)]; ok { // an open window takes the stored values over
			d.adoptFavorite(edited.Layout, edited.AutoConnect)
			if edited.Quality != f.QualityChoice() { // only a quality changed in the form reaches the window
				d.adoptQuality(s.settings.QualityFor(edited.Quality))
			}
		}
		s.selKey = newKey
		s.mode = modeView
		s.markSelection()
		s.forgetSaved(f.Host, f.Username) // the password moved or went with the old key
		s.refreshSaved(edited.Host, edited.Username)
	})
}

func (s *StartWindow) remove(f favorites.Favorite, deletePassword bool) {
	// The state may change whatever the outcome (the favorite and its password go separately).
	s.async(func() error { return s.mgr.Delete(f.Host, f.Username, deletePassword) }, func(error) {
		s.forgetSaved(f.Host, f.Username)
	})
}

// async runs fn on a goroutine; on the Fyne thread it reports the error, calls after (may be
// nil) and reloads the list.
func (s *StartWindow) async(fn func() error, after func(error)) {
	go func() {
		err := fn()
		fyne.Do(func() {
			if err != nil {
				s.onError(err)
			}
			if after != nil {
				after(err)
			}
			s.reload()
		})
	}()
}

// showDelete asks before deleting, in the panel dialog style: danger tile, title, text, and
// whether the saved password goes too. Enter does not delete.
func (s *StartWindow) showDelete(f favorites.Favorite) {
	var dropPW *dialogCheck
	showPanel(s.win, panelSpec{
		icon: icons.Delete, danger: true,
		title:  lang.L("Delete {{.Name}}?", map[string]any{"Name": f.DisplayName()}),
		action: lang.L("Delete"), cancel: lang.L("Cancel"),
		onAction: func() { s.remove(f, dropPW.Checked) },
	}, func(p *panel) []fyne.CanvasObject {
		dropPW = p.newCheck(lang.L("Also delete the saved password from the keychain"), true)
		msg := widget.NewLabel(lang.L("The favorite and its trusted certificate are removed from OpenLAMT."))
		msg.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{msg, dropPW}
	})
}
