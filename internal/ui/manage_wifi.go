package ui

import (
	"strconv"

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

// The Wi-Fi page of device management (issue #22, gesamtdesign-2026-11-23 "Verwaltung: Wi-Fi"):
// a card with when Intel AMT uses Wi-Fi, the profile sync with the operating system and UEFI and
// the wireless address; below it the AMT profiles by priority with "Edit", "+ Add Profile" and
// "Refresh" in the page head. A device without Wi-Fi shows the empty state instead. The dialogs
// are in wifi_dialogs.go. The signal strength and the network of the current connection in the
// design's "Connection now" row are left out: Intel AMT does not report them.

// wifiData is the Wi-Fi state of one device and the write in progress, shared by both views.
// Only touched on the Fyne thread.
type wifiData struct {
	m    *manager
	src  *manageSource[amt.WiFiSettings]
	busy bool // a write runs: the controls are locked
}

// locked reports whether the controls are locked: a write or a read runs.
func (d *wifiData) locked() bool { return d.busy || d.src.loading }

// write runs fn off the Fyne thread with the controls locked, then shows its error on win and
// reads the state again. A second write while one runs is dropped.
func (d *wifiData) write(win func() fyne.Window, fn func() error) {
	if d.busy || d.m.closed {
		return
	}
	d.busy = true
	d.src.notify()
	manageWrite(d.m, nil, fn, func(err error) {
		d.busy = false
		if err != nil {
			showError(win(), err)
		}
		d.src.reload()
	}, nil)
}

// wifiStates are the segments Off | While on | Always.
var wifiStates = []amt.WiFiState{amt.WiFiOff, amt.WiFiS0, amt.WiFiS0Sx}

// securityText names a profile's security as the table and the dialog show it.
func securityText(s amt.WiFiSecurity) string {
	switch s {
	case amt.WiFiWPAPersonal:
		return lang.L("WPA-Personal (TKIP)")
	case amt.WiFiWPA2Personal:
		return lang.L("WPA2-Personal (AES)")
	case amt.WiFiWPA3Personal:
		return lang.L("WPA3-Personal")
	}
	return lang.L("Other method")
}

// profilesText is "2 profiles".
func profilesText(n int) string {
	if n == 1 {
		return lang.L("1 profile")
	}
	return lang.L("{{.Count}} profiles", map[string]any{"Count": n})
}

// wifiPane is one view of the Wi-Fi page.
type wifiPane struct {
	m       *manager
	d       *wifiData
	compact bool
	list    *listPage
	pane    *managePane

	card         *settingsCard
	cardBox      fyne.CanvasObject
	state        *widgets.Segmented
	sync, uefi   *widgets.Switch
	syncRow      *settingsRowRef
	uefiRow      *settingsRowRef
	connText     *widget.RichText
	empty        fyne.CanvasObject
	footnote     *widget.RichText // under the table; wraps in the sidebar
	footnoteBox  fyne.CanvasObject
	add          *widgets.PrimaryButton
	count        *canvas.Text // window: "· 2 profiles" after the title; nil in the sidebar
	infoState    *widgets.InfoButton
	infoSync     *widgets.InfoButton
	infoUEFI     *widgets.InfoButton
	applying     bool // render sets values: the callbacks do nothing
	rows         []amt.WiFiProfile
	profileDlg   *wifiProfileDialog // the dialogs last opened from this view (tests)
	deleteDlg    *panel
	lockDlg      *riskDialog
	stateLockDlg *riskDialog
}

func init() {
	registerManagePage(managePageDef{section: sectionMore, order: 20, icon: icons.WiFi, build: (*manager).wifiPane,
		names: func() (string, string) { return lang.L("Wi-Fi"), lang.L("Wi-Fi") }})
}

// wifiColumns are Priority (fixed), Profile and Network (the rest), Security (fixed); Edit is the action.
func wifiColumns() []listColumn {
	return []listColumn{
		{title: lang.L("Priority"), width: 90, compactWidth: 44, compactGrow: 76},
		{title: lang.L("Profile")},
		{title: lang.L("Network (SSID)")},
		{title: lang.L("Security"), width: 200, compactWidth: 72, compactGrow: 112},
	}
}

// wifiPane builds the Wi-Fi page. The data (m.wifi) is shared by both views and read when the
// page is first shown; every write reads it again.
func (m *manager) wifiPane(compact bool) *managePane {
	if m.wifi == nil {
		m.wifi = &wifiData{m: m, src: newManageSource(m, m.sess.WiFi)}
	}
	p := &wifiPane{m: m, d: m.wifi, compact: compact}
	p.add = newManageButton(lang.L("+ Add Profile"), p.openAdd)

	p.state = widgets.NewSegmented(widgets.SegmentedText,
		widgets.TextOptions(lang.L("Off"), lang.L("While on"), lang.L("Always")), p.stateChanged)
	p.sync = p.newSwitch(func(on bool) { p.d.write(p.window, func() error { return m.sess.SetWiFiSync(on) }) })
	p.uefi = p.newSwitch(func(on bool) { p.d.write(p.window, func() error { return m.sess.SetWiFiUEFIShare(on) }) })
	p.connText = widget.NewRichText(cellSeg("", theme.ColorNameForeground, theme.SizeNameText, false))

	p.infoState = widgets.NewInfoButton(widgets.HelpContent{Title: lang.L("Wi-Fi in Intel AMT"),
		What:    lang.L("Sets when Intel AMT may use the Wi-Fi adapter: never, only while the computer is on, or always, also in sleep and while it is off but plugged in."),
		When:    lang.L("Choose Always to reach the device without a cable while it sleeps or is off."),
		Wrong:   lang.L("If OpenLAMT reaches the device over Wi-Fi, Off cuts the connection."),
		Recover: lang.L("Connect a network cable, or turn Wi-Fi on again in the BIOS (MEBx) at the device.")})
	p.infoSync = widgets.NewInfoButton(widgets.HelpContent{Title: lang.L("Sync profiles from the operating system"),
		What: lang.L("Intel AMT also connects to the Wi-Fi networks that Windows has saved, through the Intel drivers in Windows."),
		When: lang.L("Turn it on if Intel AMT should reach the same networks as Windows without its own profiles.")})
	p.infoUEFI = widgets.NewInfoButton(widgets.HelpContent{Title: lang.L("Share profiles with UEFI"),
		What: lang.L("Hands the Wi-Fi profiles of Intel AMT to the UEFI firmware of the device."),
		When: lang.L("Turn it on if One Click Recovery should boot over Wi-Fi.")})

	p.card = newSettingsCard(
		wifiRow(p.infoState, lang.L("Wi-Fi in Intel AMT"), "", p.state),
		wifiRow(p.infoSync, lang.L("Sync profiles from the operating system"), lang.L("Intel AMT also uses Wi-Fi networks saved in Windows."), p.sync),
		wifiRow(p.infoUEFI, lang.L("Share profiles with UEFI"), lang.L("Lets One Click Recovery boot over Wi-Fi."), p.uefi),
		wifiRow(nil, lang.L("Connection now"), "", p.connText))
	p.syncRow, p.uefiRow = p.card.ref(1), p.card.ref(2)
	p.cardBox = p.card.box
	p.empty = newEmptyCard(lang.L("No Wi-Fi in Intel AMT"),
		lang.L("{{.Name}} has no Wi-Fi adapter that Intel AMT can use.", map[string]any{"Name": m.name}))

	size := style.SizeNameSmallText
	p.footnote = widget.NewRichText(cellSeg(lang.L("Lower number = tried first. Profiles from the operating system are not listed."),
		style.ColorNameTextSecondary, size, false))
	p.footnote.Wrapping = fyne.TextWrapWord
	// like the Users footnote: 4 px closer to the table, aligned with it
	ip := theme.InnerPadding()
	p.footnoteBox = container.New(layout.NewCustomPaddedLayout(-4-ip, 0, -ip+2, 0), p.footnote)

	p.list = m.newListPage(compact, listSpec{columns: wifiColumns(), action: &listAction{width: 90, compactWidth: 54},
		tools: []fyne.CanvasObject{p.add}, refresh: p.d.src.reload, lead: []fyne.CanvasObject{p.cardBox},
		note: container.New(layout.NewCustomPaddedVBoxLayout(0), p.empty, p.footnoteBox)})
	p.pane = p.list.pane
	p.pane.shown, p.pane.impl = p.d.src.ensure, p
	if !compact {
		p.count = designText("", 14, style.ColorNameTextSecondary, false)
		p.pane.info = p.count
	}
	p.pane.close = p.d.src.subscribe(p.render)
	p.render()
	return p.pane
}

// window is the window the dialogs of this view open on.
func (p *wifiPane) window() fyne.Window { return p.list.window() }

// newSwitch is a green switch that calls changed only for a change by the user.
func (p *wifiPane) newSwitch(changed func(bool)) *widgets.Switch {
	s := widgets.NewSwitch(nil)
	s.OnColor = theme.ColorNameSuccess
	s.OnChanged = func(on bool) {
		if !p.applying {
			changed(on)
		}
	}
	return s
}

// render shows the device's state, the profiles and the empty state, and locks the controls
// while a write or read runs.
func (p *wifiPane) render() {
	src := p.d.src
	w := src.val
	loaded := src.loaded()
	has := loaded && w.Available

	p.rows = nil
	if has {
		p.rows = w.Profiles
	}
	rows := make([]listRow, len(p.rows))
	for i, pr := range p.rows {
		rows[i] = p.listRow(pr)
	}
	p.list.update(rows, loaded, src.loading, src.err)
	setShown(p.cardBox, has)
	setShown(p.list.table, has)
	setShown(p.empty, loaded && !w.Available)
	setShown(p.add, has)
	setShown(p.footnoteBox, has)

	if p.count != nil {
		p.count.Text = ""
		if has {
			p.count.Text = "· " + profilesText(len(p.rows))
		}
		p.count.Refresh()
	}

	if has {
		p.applying = true
		p.state.SetSelected(wifiStateIndex(w.State))
		p.sync.SetChecked(w.Sync)
		p.uefi.SetChecked(w.UEFIShare)
		p.applying = false
		p.syncRow.show(w.SyncKnown)
		p.uefiRow.show(w.UEFIShareKnown)
		p.card.sync()
		conn := lang.L("Not connected")
		if w.LinkUp && w.IPAddress != "" {
			conn = w.IPAddress
		}
		p.setRich(p.connText, conn, theme.ColorNameForeground, theme.SizeNameText)
	}
	locked := p.d.locked()
	for _, c := range []fyne.Disableable{p.state, p.sync, p.uefi, p.add} {
		setEnabled(c, !locked)
	}
	p.pane.changed()
}

// setRich replaces the single segment of rt.
func (p *wifiPane) setRich(rt *widget.RichText, text string, c fyne.ThemeColorName, size fyne.ThemeSizeName) {
	if p.compact && size == theme.SizeNameText {
		size = style.SizeNameSmallText
	}
	rt.Segments = []widget.RichTextSegment{cellSeg(text, c, size, false)}
	rt.Refresh()
}

// wifiStateIndex is the segment of s, NoSelection for a state outside the three.
func wifiStateIndex(s amt.WiFiState) int {
	for i, v := range wifiStates {
		if v == s {
			return i
		}
	}
	return widgets.NoSelection
}

// stateChanged is a tap on Off, While on or Always: Off while the session runs over Wi-Fi asks
// the red question first (amt.OpWiFiLockout); everything else is written at once.
func (p *wifiPane) stateChanged(i int) {
	w := p.d.src.val
	next := wifiStates[i]
	if next == amt.WiFiOff && w.OverWiFi {
		p.state.SetSelected(wifiStateIndex(w.State)) // the device's value until confirmed
		p.confirmStateOff()
		return
	}
	p.d.write(p.window, func() error { return p.m.sess.SetWiFiState(next, amt.Confirmation{}) })
}

// listRow is a profile: priority, name, SSID, security and its Edit button.
func (p *wifiPane) listRow(pr amt.WiFiProfile) listRow {
	size := theme.SizeNameText
	if p.compact {
		size = style.SizeNameSmallText
	}
	var cells [][]widget.RichTextSegment
	for _, s := range []string{strconv.Itoa(pr.Priority), pr.Name, pr.SSID, securityText(pr.Security)} {
		cells = append(cells, []widget.RichTextSegment{cellSeg(s, theme.ColorNameForeground, size, false)})
	}
	b := newUserEditButton(p.compact, false, func() { p.openEdit(pr) })
	if p.d.locked() {
		b.Disable()
	}
	return listRow{cells: cells, action: b}
}

// Geometry of a Wi-Fi card row with a description (gesamtdesign .r .lw .d): the key in 13 px,
// the description below it in 12 px secondary, wrapped beside the control.
const wifiRowDescGap float32 = 2

// wifiRow is a row of the Wi-Fi card: the key with its info symbol (nil = none), a description
// below it ("" = none) and the control or value on the right.
func wifiRow(info *widgets.InfoButton, key, desc string, right fyne.CanvasObject) fyne.CanvasObject {
	head := []fyne.CanvasObject{designText(key, 13, style.ColorNameControlText, false)}
	if info != nil {
		head = append(head, info)
	}
	left := []fyne.CanvasObject{container.New(&rowLayout{gap: 2, fill: -1}, head...)}
	if desc != "" {
		d := widget.NewRichText(cellSeg(desc, style.ColorNameTextSecondary, style.SizeNameSmallText, false))
		d.Wrapping = fyne.TextWrapWord
		left = append(left, flushText(d))
	}
	col := container.New(layout.NewCustomPaddedVBoxLayout(wifiRowDescGap), left...)
	return container.New(&wifiRowLayout{}, col, right)
}

// wifiRowLayout places the left column (key, description) beside the right object at its
// minimum width, both in the vertical middle; the description wraps in the width left over.
type wifiRowLayout struct{ width float32 }

func (l *wifiRowLayout) leftWidth(total float32, right fyne.Size) float32 {
	return max(40, total-2*settingsRowPad-right.Width-settingsRowGap)
}

func (l *wifiRowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	l.width = size.Width
	rs := objs[1].MinSize()
	lw := l.leftWidth(size.Width, rs)
	lh := settle(objs[0], lw)
	objs[0].Move(fyne.NewPos(settingsRowPad, (size.Height-lh)/2))
	objs[0].Resize(fyne.NewSize(lw, lh))
	w := max(0, min(rs.Width, size.Width-2*settingsRowPad-settingsRowGap-40))
	objs[1].Move(fyne.NewPos(size.Width-settingsRowPad-w, (size.Height-rs.Height)/2))
	objs[1].Resize(fyne.NewSize(w, rs.Height))
}

func (l *wifiRowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	rs := objs[1].MinSize()
	w := l.width
	if w <= 0 {
		w = manageSidebarWidth - 2*16
	}
	lh := settle(objs[0], l.leftWidth(w, rs))
	return fyne.NewSize(2*settingsRowPad+40+settingsRowGap+rs.Width, max(settingsRowHeight, lh+16, rs.Height+8))
}
